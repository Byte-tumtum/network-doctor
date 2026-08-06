// Package simulation builds a throwaway virtual network, runs netdoc inside
// it, and compares the diagnosis against what the scenario said should break.
//
// Nothing here touches the host's networking. Every interface, route, resolver
// and firewall rule lives inside namespaces the simulator created and owns, and
// they cease to exist when the process tree that holds them dies. See
// netns_linux.go for the mechanism.
package simulation

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"gopkg.in/yaml.v3"
)

// Scenario is one simulation: a topology to build, faults to inject, netdoc
// runs to make, and the diagnosis those runs should produce.
type Scenario struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Topology    Topology `yaml:"topology"`
	Faults      []Fault  `yaml:"faults"`
	Tests       []Test   `yaml:"tests"`
	Expect      Expect   `yaml:"expect"`
}

// Topology is a single shared L2 segment with one node per participant. One
// segment covers every scenario in the initial library; multi-subnet routing
// (loops, misconfigured masks, multiple interfaces) needs a second link type
// and is deliberately not modelled yet.
type Topology struct {
	Subnet string `yaml:"subnet"`
	Nodes  []Node `yaml:"nodes"`
}

// Node is one network namespace on the segment.
type Node struct {
	Name string `yaml:"name"`
	// Role client marks the namespace netdoc runs in; every other node is
	// scenery. Exactly one client per scenario.
	Role    string `yaml:"role"`
	Address string `yaml:"address"`
	// Aliases are extra addresses put on the node's loopback, which is how the
	// simulated internet claims the public IPs netdoc probes (1.1.1.1, 8.8.8.8)
	// without anything leaving the namespace.
	Aliases  []string  `yaml:"aliases"`
	Gateway  string    `yaml:"gateway"`
	Resolver string    `yaml:"resolver"`
	Services []Service `yaml:"services"`
}

// Service is a test server the node runs. Ports are bound inside the node's
// namespace, so two nodes may both serve :53 or :443.
type Service struct {
	Type string `yaml:"type"`
	Port int    `yaml:"port"`
	// Zone maps a name to an address for ServiceDNS. A name that is absent
	// answers NXDOMAIN, which is how "DNS returns NXDOMAIN" is expressed.
	Zone map[string]string `yaml:"zone"`
	// Body and Status shape the ServiceHTTP reply; /generate_204 always answers
	// 204 so netdoc's captive-portal check passes.
	Status int    `yaml:"status"`
	Body   string `yaml:"body"`
}

// Service types.
const (
	ServiceDNS  = "dns"
	ServiceHTTP = "http"
	// ServiceTCP accepts a connection and closes it — enough for the direct
	// egress probe, which only proves a handshake completes.
	ServiceTCP = "tcp"
)

// Fault types.
const (
	// FaultDrop discards matching packets with an nftables rule. Direction
	// decides whether the sender is refused or left waiting; see Fault.
	FaultDrop = "drop"
	// FaultNetem attaches delay/jitter/loss to the node's segment interface.
	FaultNetem = "netem"
	// FaultNoDefaultRoute deletes the node's default route.
	FaultNoDefaultRoute = "no_default_route"
)

// FaultDrop directions.
const (
	DirectionOutbound = "outbound"
	DirectionInbound  = "inbound"
)

// Fault is one impairment applied after the topology is up and healthy.
type Fault struct {
	Type string `yaml:"type"`
	Node string `yaml:"node"`
	// To, Protocol and Port select the traffic FaultDrop discards. An empty To
	// matches every destination; a zero Port matches every port.
	To       string `yaml:"to"`
	Protocol string `yaml:"protocol"`
	Port     int    `yaml:"port"`
	// Direction chooses where FaultDrop bites, and the two are not
	// interchangeable. Outbound drops the packet on the way out of this node,
	// which the kernel reports to the sender as a refusal — a local firewall.
	// Inbound drops it as it arrives at this node, so the sender hears nothing
	// and waits out its timeout — a black hole in the path. Default outbound.
	Direction string `yaml:"direction"`
	// Delay, Jitter and Loss configure FaultNetem. Delay and Jitter are Go
	// durations; Loss is a percentage such as "10%".
	Delay  string `yaml:"delay"`
	Jitter string `yaml:"jitter"`
	Loss   string `yaml:"loss"`
}

// Test is one netdoc run inside a node. An empty Target runs the generic
// (no-target) checks, exactly as `netdoc` with no argument does.
type Test struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Node   string `yaml:"node"`
	Target string `yaml:"target"`
}

// TestNetdoc is the only test type. Named so a scenario can be explicit, and
// so an unknown type is rejected rather than silently treated as this one.
const TestNetdoc = "netdoc"

// Expect is the diagnosis the scenario claims netdoc should reach. Matching is
// on netdoc's stable machine-readable contract — probe ids, the PASS/WARN/FAIL/
// SKIP/N/A vocabulary, and the verdict word — never on the English prose.
type Expect struct {
	Verdict string          `yaml:"verdict"`
	Checks  []ExpectedCheck `yaml:"checks"`
}

// ExpectedCheck names one probe row and the status it should carry.
type ExpectedCheck struct {
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
}

// knownProbeIDs is the set a scenario may name. It references the constants so
// a rename breaks the build; a newly added probe has to be listed here before
// a scenario can assert on it.
var knownProbeIDs = []diagnostic.ProbeID{
	diagnostic.ProbeIface, diagnostic.ProbeSSID, diagnostic.ProbeInternet,
	diagnostic.ProbeProxy, diagnostic.ProbeDNS, diagnostic.ProbeDNSPublic,
	diagnostic.ProbeTargetTCP, diagnostic.ProbePMTU, diagnostic.ProbeTLS,
	diagnostic.ProbeHTTP, diagnostic.ProbeHTTPS, diagnostic.ProbeSSH,
	diagnostic.ProbeSMTP,
}

// statuses is netdoc's status vocabulary as it appears in the JSON report.
var statuses = []string{"PASS", "WARN", "FAIL", "SKIP", "N/A"}

// verdicts is netdoc's verdict vocabulary. Incomplete is omitted on purpose —
// a finished run never reports it, so expecting it is always a scenario bug.
var verdicts = []string{
	diagnostic.VerdictOK, diagnostic.VerdictDegraded, diagnostic.VerdictDNS,
	diagnostic.VerdictNetwork, diagnostic.VerdictService,
}

// LoadScenario reads and validates a scenario file.
func LoadScenario(path string) (*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := ParseScenario(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ParseScenario decodes YAML and validates it. Unknown fields are an error, so
// a typo'd key fails loudly instead of being silently ignored.
func ParseScenario(r io.Reader) (*Scenario, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty scenario")
		}
		return nil, err
	}
	// A second document would be silently dropped, and a scenario file is one
	// scenario.
	var extra Scenario
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("scenario file must contain exactly one document")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate reports the first thing wrong with the scenario. It runs before any
// namespace exists, so a bad scenario costs nothing.
func (s *Scenario) Validate() error {
	if s.Name == "" {
		return errors.New("name is required")
	}
	subnet, err := netip.ParsePrefix(s.Topology.subnetOrDefault())
	if err != nil {
		return fmt.Errorf("topology.subnet: %w", err)
	}
	if !subnet.Addr().Is4() {
		return errors.New("topology.subnet: only IPv4 segments are supported")
	}
	if len(s.Topology.Nodes) == 0 {
		return errors.New("topology.nodes: at least one node is required")
	}
	seen := make(map[string]bool, len(s.Topology.Nodes))
	clients := 0
	for i := range s.Topology.Nodes {
		n := &s.Topology.Nodes[i]
		switch {
		case n.Name == "":
			return fmt.Errorf("topology.nodes[%d].name is required", i)
		case seen[n.Name]:
			return fmt.Errorf("duplicate node name %q", n.Name)
		case !isSafeName(n.Name):
			return fmt.Errorf("node %q: name must be letters, digits or dashes", n.Name)
		}
		seen[n.Name] = true
		switch n.Role {
		case "client":
			clients++
		case "server", "":
			n.Role = "server"
		default:
			return fmt.Errorf("node %q: unknown role %q (client or server)", n.Name, n.Role)
		}
		if err := n.validateAddrs(subnet); err != nil {
			return err
		}
		if err := n.validateServices(); err != nil {
			return err
		}
	}
	if clients != 1 {
		return fmt.Errorf("exactly one node must have role client, found %d", clients)
	}
	for i := range s.Faults {
		if err := s.Faults[i].validate(seen); err != nil {
			return fmt.Errorf("faults[%d]: %w", i, err)
		}
	}
	if len(s.Tests) == 0 {
		return errors.New("tests: at least one test is required")
	}
	for i := range s.Tests {
		if err := s.Tests[i].validate(seen); err != nil {
			return fmt.Errorf("tests[%d]: %w", i, err)
		}
	}
	return s.Expect.validate()
}

func (t Topology) subnetOrDefault() string {
	if t.Subnet == "" {
		return "10.77.0.0/24"
	}
	return t.Subnet
}

// parseAddr is the only way an address from a scenario becomes a string this
// package will use. It rejects what ParseAddr alone would let through and
// returns the canonical rendering, so what reaches an `ip` argv or a generated
// resolv.conf is the kernel's own spelling of the address rather than the bytes
// that were in the file.
//
// The zone is the part worth refusing: netip.ParseAddr accepts an essentially
// arbitrary string after "%", and while a zone could only ever reach a command
// as one argv element — nothing here builds a shell string — an unbounded value
// has no business in an interface name or a nameserver line.
func parseAddr(raw string) (netip.Addr, string, error) {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, "", err
	}
	if addr.Zone() != "" {
		return netip.Addr{}, "", fmt.Errorf("%q: scoped addresses (the %%zone suffix) are not supported", raw)
	}
	return addr, addr.String(), nil
}

func (n *Node) validateAddrs(subnet netip.Prefix) error {
	addr, canonical, err := parseAddr(n.Address)
	if err != nil {
		return fmt.Errorf("node %q: address: %w", n.Name, err)
	}
	if !subnet.Contains(addr) {
		return fmt.Errorf("node %q: address %s is outside %s", n.Name, addr, subnet)
	}
	n.Address = canonical
	for i, a := range n.Aliases {
		if _, n.Aliases[i], err = parseAddr(a); err != nil {
			return fmt.Errorf("node %q: alias: %w", n.Name, err)
		}
	}
	for label, v := range map[string]*string{"gateway": &n.Gateway, "resolver": &n.Resolver} {
		if *v == "" {
			continue
		}
		if _, *v, err = parseAddr(*v); err != nil {
			return fmt.Errorf("node %q: %s: %w", n.Name, label, err)
		}
	}
	return nil
}

func (n *Node) validateServices() error {
	for i := range n.Services {
		svc := &n.Services[i]
		switch svc.Type {
		case ServiceDNS:
			if svc.Port == 0 {
				svc.Port = 53
			}
			for name, ip := range svc.Zone {
				if !isSafeHostname(name) {
					return fmt.Errorf("node %q: zone name %q is not a hostname", n.Name, name)
				}
				_, canonical, err := parseAddr(ip)
				if err != nil {
					return fmt.Errorf("node %q: zone %s: %w", n.Name, name, err)
				}
				svc.Zone[name] = canonical
			}
		case ServiceHTTP:
			if svc.Port == 0 {
				svc.Port = 80
			}
			if svc.Status == 0 {
				svc.Status = 200
			}
			if svc.Status < 100 || svc.Status > 599 {
				return fmt.Errorf("node %q: http status %d is out of range", n.Name, svc.Status)
			}
		case ServiceTCP:
			if svc.Port == 0 {
				return fmt.Errorf("node %q: tcp service needs a port", n.Name)
			}
		default:
			return fmt.Errorf("node %q: unknown service type %q", n.Name, svc.Type)
		}
		if svc.Port < 1 || svc.Port > 65535 {
			return fmt.Errorf("node %q: port %d is out of range", n.Name, svc.Port)
		}
	}
	return nil
}

func (f *Fault) validate(nodes map[string]bool) error {
	if !nodes[f.Node] {
		return fmt.Errorf("unknown node %q", f.Node)
	}
	switch f.Type {
	case FaultDrop:
		switch f.Direction {
		case DirectionOutbound, DirectionInbound:
		case "":
			f.Direction = DirectionOutbound
		default:
			return fmt.Errorf("unknown direction %q (%s or %s)", f.Direction, DirectionOutbound, DirectionInbound)
		}
		if f.To != "" {
			var err error
			if _, f.To, err = parseAddr(f.To); err != nil {
				return fmt.Errorf("to: %w", err)
			}
		}
		switch f.Protocol {
		case "tcp", "udp":
		case "":
			if f.Port != 0 {
				return errors.New("port needs a protocol (tcp or udp)")
			}
		default:
			return fmt.Errorf("unknown protocol %q (tcp or udp)", f.Protocol)
		}
		if f.Port < 0 || f.Port > 65535 {
			return fmt.Errorf("port %d is out of range", f.Port)
		}
	case FaultNetem:
		if f.Delay == "" && f.Jitter == "" && f.Loss == "" {
			return errors.New("netem needs delay, jitter or loss")
		}
		for label, v := range map[string]string{"delay": f.Delay, "jitter": f.Jitter} {
			if v == "" {
				continue
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			if d < 0 {
				return fmt.Errorf("%s must not be negative", label)
			}
		}
		if f.Jitter != "" && f.Delay == "" {
			return errors.New("jitter needs a delay to vary")
		}
		if f.Loss != "" && !isPercent(f.Loss) {
			return fmt.Errorf("loss: %q is not a percentage such as \"10%%\"", f.Loss)
		}
	case FaultNoDefaultRoute:
	default:
		return fmt.Errorf("unknown fault type %q", f.Type)
	}
	return nil
}

func (t *Test) validate(nodes map[string]bool) error {
	if t.Type == "" {
		t.Type = TestNetdoc
	}
	if t.Type != TestNetdoc {
		return fmt.Errorf("unknown test type %q", t.Type)
	}
	if !nodes[t.Node] {
		return fmt.Errorf("unknown node %q", t.Node)
	}
	if t.Name == "" {
		t.Name = t.Node + " " + t.Target
		if t.Target == "" {
			t.Name = t.Node + " (generic)"
		}
	}
	if t.Target == "" {
		return nil
	}
	// The same parser the CLI uses, so a scenario can never ask netdoc for a
	// target netdoc would reject.
	_, err := diagnostic.ParseTarget(t.Target)
	return err
}

func (e *Expect) validate() error {
	if e.Verdict != "" && !contains(verdicts, e.Verdict) {
		return fmt.Errorf("expect.verdict: unknown verdict %q (one of %s)", e.Verdict, strings.Join(verdicts, ", "))
	}
	seen := make(map[string]bool, len(e.Checks))
	for i, c := range e.Checks {
		if c.ID == "" {
			return fmt.Errorf("expect.checks[%d].id is required", i)
		}
		known := false
		for _, id := range knownProbeIDs {
			if string(id) == c.ID {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("expect.checks[%d]: unknown probe id %q", i, c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("expect.checks: duplicate probe id %q", c.ID)
		}
		seen[c.ID] = true
		if !contains(statuses, c.Status) {
			return fmt.Errorf("expect.checks[%d]: unknown status %q (one of %s)", i, c.Status, strings.Join(statuses, ", "))
		}
	}
	if e.Verdict == "" && len(e.Checks) == 0 {
		return errors.New("expect: a scenario must expect a verdict, some checks, or both")
	}
	return nil
}

// Client returns the node netdoc runs in. Validate guarantees there is one.
func (s *Scenario) Client() *Node {
	for i := range s.Topology.Nodes {
		if s.Topology.Nodes[i].Role == "client" {
			return &s.Topology.Nodes[i]
		}
	}
	return nil
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// isSafeName gates every string that reaches an argv as a namespace or
// interface name. Conservative on purpose: these end up in command arguments.
func isSafeName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alnum && (r != '-' || i == 0) {
			return false
		}
	}
	return true
}

// isSafeHostname gates zone names, which are compared against queries and
// written into DNS answers.
func isSafeHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if !isSafeName(label) || len(label) > 63 {
			return false
		}
	}
	return true
}

func isPercent(s string) bool {
	num, ok := strings.CutSuffix(s, "%")
	if !ok || num == "" {
		return false
	}
	dots := 0
	for _, r := range num {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return dots <= 1
}
