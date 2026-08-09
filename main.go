package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/textsafe"
	"github.com/heymaikol/network-doctor/internal/ui"
)

// version is injected by GoReleaser via -X main.version={{.Version}}.
var version = "dev"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		version = versionString(version, info.Main.Version)
	}
}

// versionString picks what -version reports: the GoReleaser-injected value
// when there is one, else the module version stamped into the binary — so a
// plain `go install ...@vX.Y.Z` build doesn't introduce itself as "dev".
func versionString(injected, module string) string {
	if injected == "dev" && module != "" && module != "(devel)" {
		return module
	}
	return injected
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// askpass answers one ssh prompt, whose text arrives as the argument. ssh is
// told to force askpass, which routes *every* prompt here — including the
// unknown-host-key question, where handing over the password would silently
// spend it on a host nobody vouched for. So only a password or passphrase
// prompt is answered; anything else is refused and ssh says so on the
// terminal, leaving the user to answer it themselves on a run with the
// password field left blank.
// host is the machine the password was collected for; a prompt naming any
// other one is refused, since ssh's whole subtree inherits the helper. proxied
// says a jump host or proxy command is in the way, which is the one case where
// a prompt that names nobody is refused too — see promptHost.
func askpass(args []string, secret, host string, proxied bool, stdout, stderr io.Writer) int {
	prompt := strings.ToLower(strings.Join(args, " "))
	// The host-key question embeds the host name, so a target like
	// "passwordless.example.com" would otherwise satisfy the check below and
	// spend the secret on a host nobody has vouched for. Refuse the
	// confirmation question on its own terms, and refuse a promptless call
	// rather than guessing what it wanted.
	confirm := strings.Contains(prompt, "(yes/no") || strings.Contains(prompt, "fingerprint")
	if prompt == "" || confirm || (!strings.Contains(prompt, "password") && !strings.Contains(prompt, "passphrase")) {
		fmt.Fprintln(stderr, "netdoc: not answering this prompt with the stored password")
		return 1
	}
	h := promptHost(prompt)
	if h != "" && h != strings.ToLower(host) {
		fmt.Fprintln(stderr, "netdoc: not answering a password prompt for", h)
		return 1
	}
	// With a proxy in the way an unattributed prompt could be either end's, and
	// the wording is no help: a keyboard-interactive prompt is the far end's to
	// write, so a jump host that calls its question a passphrase would collect
	// the target's password. Nothing in the text is evidence, so refuse them
	// all — the run with the password field blank puts the question back on the
	// terminal, where the user can see who is asking.
	if h == "" && proxied {
		fmt.Fprintln(stderr, "netdoc: not answering an unattributed prompt on a proxied connection")
		return 1
	}
	fmt.Fprintln(stdout, secret)
	return 0
}

// promptHost names the machine an ssh password prompt is for, or "" when the
// prompt names none. ssh asks two ways: "user@host's password:" for password
// auth, and "(user@host) " in front of whatever the server sent for
// keyboard-interactive. A ProxyJump or ProxyCommand child asks in the same
// shapes for the jump host — a prompt that matches on "password" but wants a
// secret this form never collected.
//
// The parenthesized form is read first, and it settles the question: it is
// ssh's own prefix, while the text after it belongs to the server, which is
// free to send "root@target's password:" and would otherwise pass itself off
// as the machine the user meant to log in to.
//
// A key passphrase carries no host, and neither does a keyboard-interactive
// prompt from a client too old to prefix them (< OpenSSH 8.6). Both stay
// answerable only while there is one machine it could be — a proxy makes them
// indistinguishable from each other, and the wording can't tell them apart.
func promptHost(prompt string) string {
	if rest, ok := strings.CutPrefix(prompt, "("); ok {
		if head, _, ok := strings.Cut(rest, ")"); ok {
			return hostAfterAt(head)
		}
	}
	if head, _, ok := strings.Cut(prompt, "'s password"); ok {
		return hostAfterAt(head)
	}
	return ""
}

// hostAfterAt takes the host half of a "user@host" pair. Last "@" wins: the
// login is the free-text half, and one containing an "@" must not be able to
// pass its own tail off as the host.
func hostAfterAt(pair string) string {
	if i := strings.LastIndex(pair, "@"); i >= 0 {
		return pair[i+1:]
	}
	return ""
}

func run(args []string, stdout, stderr io.Writer) int {
	// The SSH login form (S) runs this binary as ssh's askpass helper so the
	// password reaches ssh through the environment instead of an argv every
	// process on the machine could read. First thing in run: ssh wants a
	// secret on stdout and nothing else.
	if secret, ok := os.LookupEnv(ui.AskpassEnv); ok {
		return askpass(args, secret, os.Getenv(ui.AskpassHostEnv), os.Getenv(ui.AskpassProxyEnv) != "", stdout, stderr)
	}
	fs := flag.NewFlagSet("netdoc", flag.ContinueOnError)
	// Buffered, not stderr: the flag package quotes nothing, so an undefined
	// flag name made of escape bytes would land on the terminal verbatim.
	var flagErr bytes.Buffer
	fs.SetOutput(&flagErr)
	// Suppress the automatic usage dump: an explicit -help prints the full
	// usage on stdout and exits 0, a parse error gets only a one-line hint.
	fs.Usage = func() {}
	toolbox := fs.Bool("toolbox", false, "start in toolbox mode")
	jsonOut := fs.Bool("json", false, "run the checks headless and print a JSON report")
	watch := fs.Bool("watch", false, "continuously re-run checks (with -json, stream one report per line)")
	iface := fs.String("iface", "", "bind probes to an interface name or exact local IP")
	showVersion := fs.Bool("version", false, "print version and exit")
	timeout := fs.Duration("timeout", diagnostic.ProbeTimeout, "per-check probe timeout")

	// The stdlib flag package stops parsing at the first non-flag argument;
	// peel positionals off and re-parse the remainder so flags are accepted
	// both before and after the target.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printUsage(stdout, fs)
				return 0
			}
			if msg := textsafe.Clean(strings.TrimSpace(flagErr.String())); msg != "" {
				fmt.Fprintln(stderr, msg)
			}
			fmt.Fprintln(stderr, "run 'netdoc --help' for usage")
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positional) > 1 {
		// %q, not %v: argv is untrusted enough to matter, and quoting escapes
		// control bytes so an OSC 52 in an argument prints instead of running.
		fmt.Fprintf(stderr, "netdoc: unexpected arguments: %q\n", positional[1:])
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "netdoc", version)
		return 0
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "netdoc: -timeout must be positive")
		return 2
	}
	diagnostic.ProbeTimeout = *timeout
	if *jsonOut && *toolbox {
		fmt.Fprintln(stderr, "netdoc: -json and -toolbox cannot be combined")
		return 2
	}
	var sources *diagnostic.SourceAddresses
	if *iface != "" {
		var err error
		sources, err = diagnostic.ResolveSource(*iface)
		if err != nil {
			fmt.Fprintln(stderr, "netdoc: -iface:", err)
			return 2
		}
	}

	var t *diagnostic.Target
	if len(positional) == 1 {
		parsed, err := diagnostic.ParseTarget(positional[0])
		if err != nil {
			fmt.Fprintln(stderr, "netdoc:", err)
			return 2 // bad args / validation reject
		}
		t = parsed
	}

	if *jsonOut {
		return runJSON(context.Background(), t, sources, *watch, stdout, stderr)
	}

	// No mouse tracking: terminals translate the wheel to arrow keys in the
	// alt screen (alternate scroll), and grabbing the mouse would break
	// native text selection.
	histFile := ""
	if dir, err := os.UserConfigDir(); err == nil {
		histFile = filepath.Join(dir, "netdoc", "history")
	}
	p := tea.NewProgram(ui.NewWithSources(t, sources, *toolbox, *watch, histFile, version), tea.WithAltScreen())
	final, err := p.Run()
	// Every way out of Run lands here, including the ones that never reached
	// the model's own quit path.
	ui.Cleanup(final)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc:", err)
		return 1
	}
	return ui.ExitCode(final)
}

// printUsage writes the full help text: usage line, the target grammar
// ParseTarget accepts, and the flags.
func printUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, `Usage: netdoc [flags] [target]

Diagnoses network connectivity layer by layer. With no target it runs the
generic checks; with a target it also probes that endpoint. Flags may be
given before or after the target.

Target forms:
`+diagnostic.TargetForms+"\n\nFlags:\n")
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// The report is the stable machine-readable contract: field names and the
// status vocabulary (PASS/WARN/FAIL/SKIP/N/A) must not change once released.
type report struct {
	Version string `json:"version"`
	// Ts is set only under -json -watch, where the output is a stream and each
	// pass needs to say when it ran. A single report doesn't carry one, so the
	// one-shot output stays byte-identical to what it has always been.
	Ts      string        `json:"ts,omitempty"`
	Target  *reportTarget `json:"target"` // null in generic (no-target) mode
	Checks  []reportCheck `json:"checks"`
	Summary string        `json:"summary"`
	Verdict string        `json:"verdict"`
	// FailedStage is the id of the first check that failed, omitted when none
	// did — the one field a CI job needs to route a bug report.
	FailedStage string `json:"failed_stage,omitempty"`
	OK          bool   `json:"ok"`
}

type reportTarget struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type reportCheck struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Cause      string          `json:"cause,omitempty"`
	Families   *reportFamilies `json:"address_families,omitempty"`
	Ms         int64           `json:"ms"` // wall time, truncated but floored at 1; 0 means the check never ran
	Detail     string          `json:"detail"`
	Fix        string          `json:"fix,omitempty"`
	Addrs      []string        `json:"addrs,omitempty"`
	SelectedIP string          `json:"selected_ip,omitempty"`
	Source     string          `json:"source,omitempty"`
	Iface      string          `json:"iface,omitempty"`
	Network    string          `json:"network,omitempty"`
	Portal     *reportPortal   `json:"portal,omitempty"`
	Attempts   []reportAttempt `json:"attempts,omitempty"`
}

type reportFamilies struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

type reportPortal struct {
	RedirectURL string `json:"redirect_url,omitempty"`
}

type reportAttempt struct {
	IP  string `json:"ip"`
	Ms  int64  `json:"ms"` // same flooring as reportCheck.Ms
	Err string `json:"error,omitempty"`
}

// runAll is stubbed in tests so -json runs don't touch the network.
var runAll = diagnostic.RunAll

// runJSON runs the probe DAG headless and prints the JSON report. Exit code
// mirrors the TUI contract: 1 if any check failed, else 0.
//
// With watch it never stops on its own: one compact report per line, forever,
// until the terminal interrupts it. That's NDJSON, but not a new schema — the
// line is the same report struct, plus the ts that makes a stream readable.
func runJSON(ctx context.Context, t *diagnostic.Target, sources *diagnostic.SourceAddresses, watch bool, stdout, stderr io.Writer) int {
	enc := json.NewEncoder(stdout)
	if !watch {
		enc.SetIndent("", "  ")
	} else {
		// Ctrl-C — or the SIGTERM a supervisor sends — has to end the stream
		// at a line boundary rather than cut one in half, and it cancels the
		// in-flight probes on the way out.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	// code starts at 1: until a pass completes, there's no report to call a
	// success, so an interrupt before the first line lands has to fail closed.
	code := 1
	for {
		probes := diagnostic.BuildProbesFromSources(t, sources)
		results := runAll(ctx, probes)
		if ctx.Err() != nil {
			// Interrupted mid-pass: every probe failed because we cancelled it,
			// so reporting that pass would be a lie.
			return code
		}
		rep := buildReport(t, probes, results)
		code = 0
		if !rep.OK {
			code = 1
		}
		if watch {
			rep.Ts = time.Now().UTC().Format(time.RFC3339)
		}
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(stderr, "netdoc:", err)
			return 1
		}
		if !watch {
			return code
		}
		select {
		case <-ctx.Done():
			return code
		case <-time.After(ui.WatchEvery):
		}
	}
}

// buildReport flattens probe results into the stable JSON shape, preserving
// probe order. OK means "no check failed" — Warn, Skip, and N/A don't count
// against it, same as everywhere else in the app.
func buildReport(t *diagnostic.Target, probes []diagnostic.Probe, results map[diagnostic.ProbeID]diagnostic.ProbeResult) report {
	rep := report{Version: version, OK: true}
	if t != nil {
		rep.Target = &reportTarget{Host: t.Host, Port: t.Port, Protocol: t.Proto.String()}
	}
	order := make([]diagnostic.ProbeID, len(probes))
	for i, p := range probes {
		order[i] = p.ID
		r := results[p.ID]
		rep.OK = rep.OK && r.Status != diagnostic.StatusFail
		if r.Status == diagnostic.StatusFail && rep.FailedStage == "" {
			rep.FailedStage = string(p.ID)
		}
		c := reportCheck{
			ID:      string(p.ID),
			Name:    p.Name,
			Status:  r.Status.String(),
			Cause:   r.Cause,
			Ms:      diagnostic.Ms(r.Dur),
			Detail:  r.Detail,
			Fix:     r.Fix,
			Iface:   r.Iface,
			Network: r.Network,
		}
		if r.Families != nil {
			c.Families = &reportFamilies{IPv4: r.Families.IPv4, IPv6: r.Families.IPv6}
		}
		if r.Portal != nil {
			c.Portal = &reportPortal{RedirectURL: r.Portal.RedirectURL}
		}
		for _, ip := range r.Addrs {
			c.Addrs = append(c.Addrs, ip.String())
		}
		if r.SelectedIP != nil {
			c.SelectedIP = r.SelectedIP.String()
		}
		if r.Source != nil {
			c.Source = r.Source.String()
		}
		for _, a := range r.Attempts {
			ra := reportAttempt{IP: a.IP.String(), Ms: diagnostic.Ms(a.Dur)}
			if a.Err != nil {
				ra.Err = a.Err.Error()
			}
			c.Attempts = append(c.Attempts, ra)
		}
		rep.Checks = append(rep.Checks, c)
	}
	rep.Summary, rep.Verdict = diagnostic.Diagnose(t, order, results)
	return rep
}
