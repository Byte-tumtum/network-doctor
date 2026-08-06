//go:build linux

package simulation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// This backend builds the simulated network out of unprivileged Linux
// namespaces. The shape is:
//
//	launcher (netdoc-sim run)          host namespaces, no privileges
//	  └── director                     new user + net + mount namespace
//	        ├── bridge nb<id>          the shared L2 segment, in the director
//	        ├── node holder ×N         each in its own net + mount namespace
//	        └── nsenter … netdoc       diagnostics, inside a node
//
// The user namespace is what makes this rootless: inside it the director holds
// CAP_NET_ADMIN and CAP_NET_BIND_SERVICE, so it can create bridges, veths,
// routes, nftables rules, tc qdiscs and privileged listeners — all of which
// exist only inside namespaces it owns. It has no capabilities in the host
// namespaces at all, so the safety rules this package promises (never touch the
// host's routes, resolver, or firewall) are enforced by the kernel rather than
// by this code being careful.
//
// Nothing is registered under /run/netns or /etc/netns either: the namespaces
// are held open by the holder processes and are reclaimed by the kernel when
// they exit. Cleanup is therefore "kill the tree", and a crashed or killed run
// leaks nothing.

// requiredTools are the executables the backend shells out to. Command
// arguments are always passed as argv slices; no string ever reaches a shell.
var requiredTools = []string{"ip", "nsenter"}

// optionalTools are needed only by the fault types that use them.
var optionalTools = map[string]string{"tc": FaultNetem, "nft": FaultDrop}

// DefaultBackend returns the backend for this platform. With dry set, Prepare
// prints the commands it would run to log instead of running them, and creates
// nothing.
func DefaultBackend(dry bool, log io.Writer) Backend {
	return &netnsBackend{dry: dry, log: log}
}

type netnsBackend struct {
	dry bool
	log io.Writer
}

func (b *netnsBackend) Name() string { return "linux-netns" }

func (b *netnsBackend) Capabilities(ctx context.Context) Capabilities {
	c := Capabilities{
		Backend: b.Name(),
		Privileged: []string{
			"create a user namespace mapping your uid to root inside it (no host privileges are gained)",
			"create network and mount namespaces, a bridge, and veth pairs inside them",
			"set addresses, routes, nftables rules and tc qdiscs inside those namespaces only",
			"bind-mount a generated resolv.conf over /etc/resolv.conf inside a simulated node's private mount namespace",
			"run the netdoc binary inside a simulated node",
		},
	}
	var missingRequired []string
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool); err != nil {
			missingRequired = append(missingRequired, tool)
			c.Missing = append(c.Missing, tool+" (required; from "+toolPackage(tool)+")")
		}
	}
	for _, tool := range sortedKeys(optionalTools) {
		if _, err := exec.LookPath(tool); err != nil {
			c.Missing = append(c.Missing, tool+" (only needed for "+optionalTools[tool]+" faults; from "+toolPackage(tool)+")")
		}
	}
	switch {
	case len(missingRequired) > 0:
		c.Reason = "cannot simulate: " + strings.Join(missingRequired, " and ") +
			" not found in $PATH. Install iproute2 (ip) and util-linux (nsenter)."
	default:
		c.Reason = userNamespaceReason()
		c.Supported = c.Reason == ""
	}
	return c
}

func toolPackage(tool string) string {
	if tool == "ip" || tool == "tc" {
		return "iproute2"
	}
	if tool == "nft" {
		return "nftables"
	}
	return "util-linux"
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// userNamespaceReason explains why this host will not hand out an unprivileged
// user namespace, or "" when it will. It names the specific knob that is off,
// because "namespaces are disabled" is not something a user can act on and
// "user.max_user_namespaces is 0" is.
//
// There is no fallback to look for. Without a user namespace the simulator
// would have to run privileged against the host's real network stack, which is
// exactly the thing this design exists to make impossible — so an unsupported
// host is reported and the run stops.
func userNamespaceReason() string {
	// Root can always unshare, and still does: running as root does not switch
	// the simulator to host networking, it just means the user namespace is
	// entered from an account that already had the capabilities.
	if os.Geteuid() == 0 {
		return ""
	}
	type knob struct {
		path, wants, why string
	}
	// Each of these independently blocks clone(CLONE_NEWUSER) for an
	// unprivileged process, and distributions disagree about which they ship.
	for _, k := range []knob{
		{"/proc/sys/user/max_user_namespaces", "a count above 0",
			"raise it with `sudo sysctl -w user.max_user_namespaces=15000`"},
		{"/proc/sys/kernel/unprivileged_userns_clone", "1",
			"enable it with `sudo sysctl -w kernel.unprivileged_userns_clone=1`"},
		{"/proc/sys/kernel/apparmor_restrict_unprivileged_userns", "0",
			"AppArmor is restricting unprivileged user namespaces (Ubuntu 24.04 and later); " +
				"allow this binary in an AppArmor profile, or clear the restriction with " +
				"`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`"},
	} {
		raw, err := os.ReadFile(k.path)
		if err != nil {
			// An absent knob is a kernel that does not have that restriction.
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		blocked := n == 0
		if k.path == "/proc/sys/kernel/apparmor_restrict_unprivileged_userns" {
			blocked = n != 0
		}
		if blocked {
			return fmt.Sprintf(
				"cannot simulate: unprivileged user namespaces are unavailable — %s is %d, wanted %s. %s. "+
					"netdoc-sim will not fall back to the host network stack or ask for elevated privileges.",
				k.path, n, k.wants, k.why)
		}
	}
	return ""
}

// LaunchDirector re-executes this binary with argv inside a fresh user,
// network and mount namespace, and returns its exit code. The child is where
// the backend actually runs; the parent keeps no privileges and no namespaces.
func LaunchDirector(ctx context.Context, self string, argv []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, self, argv...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET | syscall.CLONE_NEWNS,
		// uid 0 inside the namespace is this user outside it. That is the whole
		// privilege story: root here, nobody out there.
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		// Mounts made inside must not propagate back to the host.
		Unshareflags: syscall.CLONE_NEWNS,
		// A director orphaned by a killed launcher must not keep namespaces
		// (and listeners) alive behind the user's back.
		Pdeathsig: syscall.SIGKILL,
	}
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		// Reached only when the capability check passed and the kernel still
		// refused — a container seccomp profile, an LSM, or a knob that changed
		// underneath us. Say what was attempted; never retry without the
		// namespace, which would put the simulation on the host's real stack.
		return 1, fmt.Errorf(
			"cannot create the user, network and mount namespaces a simulation runs in: %w. "+
				"Run `netdoc-sim capabilities` for this host's state; netdoc-sim will not run without them", err)
	}
	return 0, nil
}

// netnsEnv is one live simulated network, owned by the director process.
type netnsEnv struct {
	backend  *netnsBackend
	id       string
	scenario *Scenario
	work     string
	bridge   string
	nodes    []*nodeProc
	byName   map[string]*nodeProc
	prefix   netip.Prefix
	// tables records the nodes that already have the simulator's nftables table,
	// so repeated faults on one node do not re-add it.
	tables map[string]bool
	// holderCtx bounds the holder processes. It is deliberately detached from
	// the context Prepare was called with: that one is cancelled the moment
	// setup finishes, and it would take the namespaces with it. Cleanup is what
	// ends the holders, on every exit path.
	holderCtx  context.Context
	endHolders context.CancelFunc
	// cleaned caches the outcome of a completed teardown. Cleanup is called
	// from a defer that fires on every exit path, and a partially built
	// environment can reach it more than once; releasing twice must report the
	// same thing rather than inventing errors about resources already gone.
	cleaned *CleanupInfo
}

// nodeProc is a holder process: it exists to own a network namespace and to run
// that node's test services inside it.
type nodeProc struct {
	node    *Node
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	logs    *bytes.Buffer
	pid     int
	iface   string // interface name inside the node
	peer    string // the same veth's other end, enslaved to the bridge
	stopped bool
}

func (b *netnsBackend) Prepare(ctx context.Context, s *Scenario, id string) (Env, error) {
	prefix, err := netip.ParsePrefix(s.Topology.subnetOrDefault())
	if err != nil {
		return nil, err
	}
	e := &netnsEnv{
		backend: b, id: id, scenario: s, prefix: prefix,
		bridge: "nb" + id,
		byName: make(map[string]*nodeProc, len(s.Topology.Nodes)),
		tables: map[string]bool{},
	}
	e.holderCtx, e.endHolders = context.WithCancel(context.WithoutCancel(ctx))
	// Deterministic, not MkdirTemp: derived from the run id so `netdoc-sim
	// cleanup <id>` can find the workspace of a run whose process is gone.
	e.work = filepath.Join(os.TempDir(), "netdoc-sim-"+id)
	if err = os.MkdirAll(e.work, 0o700); err != nil {
		return nil, err
	}
	// Every failure past this point still returns e: partially built namespaces
	// and half-started holders are exactly what has to be cleaned up.
	if err := e.build(ctx); err != nil {
		return e, err
	}
	return e, nil
}

func (e *netnsEnv) build(ctx context.Context) error {
	if err := e.run(ctx, "ip", "link", "add", e.bridge, "type", "bridge"); err != nil {
		return err
	}
	if err := e.run(ctx, "ip", "link", "set", e.bridge, "up"); err != nil {
		return err
	}
	for i := range e.scenario.Topology.Nodes {
		n := &e.scenario.Topology.Nodes[i]
		np := &nodeProc{
			node:  n,
			iface: fmt.Sprintf("ne%s%d", e.id, i),
			peer:  fmt.Sprintf("np%s%d", e.id, i),
		}
		e.nodes = append(e.nodes, np)
		e.byName[n.Name] = np
		if err := e.startHolder(ctx, np); err != nil {
			return fmt.Errorf("node %s: %w", n.Name, err)
		}
		if err := e.wireNode(ctx, np); err != nil {
			return fmt.Errorf("node %s: %w", n.Name, err)
		}
	}
	// Services come last: a listener must not be reachable before every node is
	// addressed, or an early probe could race the topology into existence.
	for _, np := range e.nodes {
		if err := e.startServices(ctx, np); err != nil {
			return fmt.Errorf("node %s: %w", np.node.Name, err)
		}
	}
	return nil
}

// startHolder spawns the process that owns the node's namespaces. It reports
// readiness once its mount namespace is set up, then waits to be told the
// network is configured.
func (e *netnsEnv) startHolder(ctx context.Context, np *nodeProc) error {
	cfg := nodeConfig{
		Name: np.node.Name, Resolver: np.node.Resolver, Services: np.node.Services,
		Addresses: append([]string{np.node.Address}, np.node.Aliases...),
	}
	path := filepath.Join(e.work, np.node.Name+".json")
	blob, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	e.record("spawn node holder %q in a new net + mount namespace", np.node.Name)
	if e.backend.dry {
		return nil
	}
	cmd := exec.CommandContext(e.holderCtx, self, NodeCommand, path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWNET | syscall.CLONE_NEWNS,
		Unshareflags: syscall.CLONE_NEWNS,
		Pdeathsig:    syscall.SIGKILL,
	}
	np.logs = new(bytes.Buffer)
	cmd.Stderr = np.logs
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	np.cmd, np.stdin, np.stdout, np.pid = cmd, stdin, bufio.NewReader(stdout), cmd.Process.Pid
	return np.await(ctx, holderNSReady)
}

// wireNode gives the node its veth, addresses, and route.
func (e *netnsEnv) wireNode(ctx context.Context, np *nodeProc) error {
	n := np.node
	pid := strconv.Itoa(np.pid)
	steps := [][]string{
		{"ip", "link", "add", np.peer, "type", "veth", "peer", "name", np.iface},
		{"ip", "link", "set", np.peer, "master", e.bridge},
		{"ip", "link", "set", np.peer, "up"},
		{"ip", "link", "set", np.iface, "netns", pid},
		{"nsenter", "-t", pid, "-n", "--", "ip", "link", "set", "lo", "up"},
		{"nsenter", "-t", pid, "-n", "--", "ip", "addr", "add", n.Address + "/" + strconv.Itoa(e.prefix.Bits()), "dev", np.iface},
		{"nsenter", "-t", pid, "-n", "--", "ip", "link", "set", np.iface, "up"},
	}
	// Aliases go on loopback: an address on lo is answered for on every
	// interface, which is how one node can be "the internet" at 1.1.1.1 and
	// 8.8.8.8 without a second link.
	for _, a := range n.Aliases {
		bits := "/32"
		if addr, err := netip.ParseAddr(a); err == nil && addr.Is6() {
			bits = "/128"
		}
		steps = append(steps, []string{"nsenter", "-t", pid, "-n", "--", "ip", "addr", "add", a + bits, "dev", "lo"})
	}
	if n.Gateway != "" {
		steps = append(steps, []string{"nsenter", "-t", pid, "-n", "--", "ip", "route", "add", "default", "via", n.Gateway})
	}
	for _, argv := range steps {
		if err := e.run(ctx, argv...); err != nil {
			return err
		}
	}
	return nil
}

func (e *netnsEnv) startServices(ctx context.Context, np *nodeProc) error {
	if len(np.node.Services) == 0 {
		return nil
	}
	e.record("start %d service(s) in node %q", len(np.node.Services), np.node.Name)
	if e.backend.dry {
		return nil
	}
	if _, err := io.WriteString(np.stdin, holderStart+"\n"); err != nil {
		return err
	}
	return np.await(ctx, holderServicesReady)
}

// await waits for one line from the holder. A holder that died instead of
// answering reports its own stderr, which is where a bind failure shows up.
func (np *nodeProc) await(ctx context.Context, want string) error {
	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		s, err := np.stdout.ReadString('\n')
		ch <- line{strings.TrimSpace(s), err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case got := <-ch:
		switch {
		case got.s == want:
			return nil
		case got.err != nil:
			return fmt.Errorf("holder exited before %q: %s", want, strings.TrimSpace(np.logs.String()))
		default:
			return fmt.Errorf("holder said %q, want %q: %s", got.s, want, strings.TrimSpace(np.logs.String()))
		}
	}
}

func (e *netnsEnv) Nodes() []NodeInfo {
	out := make([]NodeInfo, 0, len(e.nodes))
	for _, np := range e.nodes {
		info := NodeInfo{
			Name: np.node.Name, Role: np.node.Role, Address: np.node.Address,
			Aliases: np.node.Aliases, Interface: np.iface, Gateway: np.node.Gateway,
			Resolver: np.node.Resolver, PID: np.pid,
		}
		for _, s := range np.node.Services {
			info.Services = append(info.Services, fmt.Sprintf("%s/%d", s.Type, s.Port))
		}
		out = append(out, info)
	}
	return out
}

func (e *netnsEnv) ApplyFaults(ctx context.Context, faults []Fault) ([]FaultInfo, error) {
	var out []FaultInfo
	for _, f := range faults {
		np, ok := e.byName[f.Node]
		if !ok {
			return out, fmt.Errorf("fault targets unknown node %q", f.Node)
		}
		steps, summary, err := e.faultSteps(f, np)
		if err != nil {
			return out, err
		}
		for _, argv := range steps {
			if err := e.run(ctx, argv...); err != nil {
				return out, fmt.Errorf("fault %s on %s: %w", f.Type, f.Node, err)
			}
		}
		out = append(out, FaultInfo{Type: f.Type, Node: f.Node, Summary: summary, Command: steps[len(steps)-1]})
	}
	return out, nil
}

// faultSteps turns one fault into the commands that inject it, plus a one-line
// description for the report.
func (e *netnsEnv) faultSteps(f Fault, np *nodeProc) ([][]string, string, error) {
	pid := strconv.Itoa(np.pid)
	in := func(argv ...string) []string {
		return append([]string{"nsenter", "-t", pid, "-n", "--"}, argv...)
	}
	switch f.Type {
	case FaultDrop:
		// Outbound drops on the way out, which the kernel reports back to the
		// local sender as EPERM — a refusal, the way a host firewall behaves.
		// Inbound drops on arrival at this node, so the sender is told nothing
		// and waits out its timeout — the way a black hole in the path behaves.
		chain, hook, where := "out", "postrouting", "refuses"
		if f.Direction == DirectionInbound {
			chain, hook, where = "in", "input", "swallows"
		}
		var steps [][]string
		key := f.Node + "/" + chain
		if !e.tables[key] {
			steps = append(steps,
				in("nft", "add", "table", "inet", nftTable),
				in("nft", "add", "chain", "inet", nftTable, chain, "{ type filter hook "+hook+" priority 0; }"))
			e.tables[key] = true
		}
		rule := in("nft", "add", "rule", "inet", nftTable, chain)
		what := "all traffic"
		if f.To != "" {
			family := "ip"
			if addr, err := netip.ParseAddr(f.To); err == nil && addr.Is6() {
				family = "ip6"
			}
			rule = append(rule, family, "daddr", f.To)
			what = "traffic to " + f.To
		}
		if f.Protocol != "" {
			rule = append(rule, f.Protocol)
			what = f.Protocol + " " + what
			if f.Port != 0 {
				rule = append(rule, "dport", strconv.Itoa(f.Port))
				what += " port " + strconv.Itoa(f.Port)
			}
		}
		rule = append(rule, "drop")
		return append(steps, rule), np.node.Name + " " + where + " " + what, nil
	case FaultNetem:
		argv := in("tc", "qdisc", "add", "dev", np.iface, "root", "netem")
		var parts []string
		if f.Delay != "" {
			d, _ := time.ParseDuration(f.Delay)
			argv = append(argv, "delay", tcDuration(d))
			parts = append(parts, "delay "+f.Delay)
			if f.Jitter != "" {
				j, _ := time.ParseDuration(f.Jitter)
				argv = append(argv, tcDuration(j))
				parts = append(parts, "jitter "+f.Jitter)
			}
		}
		if f.Loss != "" {
			argv = append(argv, "loss", f.Loss)
			parts = append(parts, "loss "+f.Loss)
		}
		return [][]string{argv}, np.node.Name + " link: " + strings.Join(parts, ", "), nil
	case FaultNoDefaultRoute:
		return [][]string{in("ip", "route", "del", "default")}, np.node.Name + " has no default route", nil
	}
	return nil, "", fmt.Errorf("unknown fault type %q", f.Type)
}

// nftTable is the simulator's own table name. Rules only ever go here, and only
// ever inside a simulated node's namespace — the host ruleset is unreachable
// from the director's user namespace in the first place.
const nftTable = "netdocsim"

// tcDuration renders a Go duration the way tc wants it.
func tcDuration(d time.Duration) string {
	if d%time.Millisecond == 0 {
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	return strconv.FormatInt(d.Microseconds(), 10) + "us"
}

func (e *netnsEnv) Exec(ctx context.Context, node string, argv []string) ExecResult {
	np, ok := e.byName[node]
	if !ok {
		return ExecResult{Err: fmt.Errorf("unknown node %q", node)}
	}
	if len(argv) == 0 {
		return ExecResult{Err: errors.New("empty command")}
	}
	// -m as well as -n: the node's private mount namespace is where its
	// /etc/resolv.conf lives, and a resolver test that read the host's would be
	// measuring the wrong machine.
	full := append([]string{"nsenter", "-t", strconv.Itoa(np.pid), "-n", "-m", "--"}, argv...)
	start := time.Now()
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	cmd.Env = simEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(start)}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		res.ExitCode = exit.ExitCode()
	case err != nil:
		res.Err = err
	}
	return res
}

// simEnv is the environment commands run with inside a node. The host's proxy
// variables are stripped: a simulation must not inherit the operator's proxy
// configuration, or a scenario's result would depend on whose laptop ran it.
// A scenario that wants a proxy sets one on its own node.
func simEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (e *netnsEnv) Cleanup(ctx context.Context, keep bool) CleanupInfo {
	if e.cleaned != nil {
		return *e.cleaned
	}
	if keep {
		var pids []string
		for _, np := range e.nodes {
			pids = append(pids, fmt.Sprintf("%s=%d", np.node.Name, np.pid))
		}
		return CleanupInfo{Kept: true, Workspace: e.work, Detail: fmt.Sprintf(
			"simulation %s left running (workspace %s, node pids %s); enter one with `nsenter -t PID -n -m -- sh`, release it with `netdoc-sim cleanup %s`",
			e.id, e.work, strings.Join(pids, " "), e.id)}
	}
	info := CleanupInfo{}
	if e.endHolders != nil {
		defer e.endHolders()
	}
	for _, np := range e.nodes {
		if err := np.stop(ctx); err != nil {
			info.Errors = append(info.Errors, fmt.Sprintf("node %s: %v", np.node.Name, err))
		}
	}
	// The bridge lives in the director's own namespace, which outlives a single
	// scenario when the caller runs several.
	if !e.backend.dry && e.bridge != "" {
		if err := e.run(ctx, "ip", "link", "del", e.bridge); err != nil && !isMissingLink(err) {
			info.Errors = append(info.Errors, err.Error())
		}
	}
	if e.work != "" {
		if err := os.RemoveAll(e.work); err != nil {
			info.Errors = append(info.Errors, err.Error())
		}
	}
	info.Done = len(info.Errors) == 0
	e.cleaned = &info
	return info
}

// stop ends a holder: closing its stdin is the polite request, SIGKILL is the
// answer to one that will not go. Idempotent, and it has to be — a holder may
// already have exited on its own, been killed with the process group, or been
// stopped by an earlier Cleanup.
func (np *nodeProc) stop(ctx context.Context) error {
	if np.cmd == nil || np.cmd.Process == nil || np.stopped {
		return nil
	}
	np.stopped = true
	if np.stdin != nil {
		np.stdin.Close()
	}
	done := make(chan error, 1)
	go func() { done <- np.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	if err := np.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-done
	return nil
}

// run executes one setup command and records it. In dry mode it only records.
func (e *netnsEnv) run(ctx context.Context, argv ...string) error {
	e.record("%s", strings.Join(argv, " "))
	if e.backend.dry {
		return nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", strings.Join(argv, " "), msg)
	}
	return nil
}

func (e *netnsEnv) record(format string, a ...any) {
	if e.backend.log != nil {
		fmt.Fprintf(e.backend.log, "  "+format+"\n", a...)
	}
}

func isMissingLink(err error) bool {
	return strings.Contains(err.Error(), "Cannot find device")
}
