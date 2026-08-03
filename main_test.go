// CLI surface: flag parsing and re-parsing, usage text, the version fallback,
// and the JSON report builder.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/ui"
)

func TestVersionString(t *testing.T) {
	tests := []struct {
		name, injected, module, want string
	}{
		{"injected wins", "1.2.3", "v9.9.9", "1.2.3"},
		{"module fallback", "dev", "v1.2.3", "v1.2.3"},
		{"development build", "dev", "(devel)", "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionString(tt.injected, tt.module); got != tt.want {
				t.Errorf("versionString(%q, %q) = %q, want %q", tt.injected, tt.module, got, tt.want)
			}
		})
	}
}

// Only exercises paths that return before the TUI starts.
func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		want       int
		wantStdout string
		wantStderr string
	}{
		{"version", []string{"-version"}, 0, "netdoc dev", ""},
		{"bad flag", []string{"-nope"}, 2, "", "flag provided but not defined"},
		{"extra args", []string{"example.com", "extra"}, 2, "", "unexpected arguments"},
		{"bad target", []string{"bad_host!"}, 2, "", "netdoc:"},
		{"json+toolbox", []string{"-json", "-toolbox"}, 2, "", "cannot be combined"},
		{"bad iface", []string{"-iface", "netdoc-no-such-interface"}, 2, "", "-iface:"},
		{"version ignores bad timeout", []string{"-timeout", "-1s", "-version"}, 0, "netdoc dev", ""},
		{"bad timeout", []string{"-timeout", "-1s"}, 2, "", "-timeout must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.want {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want contains %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want contains %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// Run as ssh's askpass helper, netdoc prints the secret it was handed and
// nothing else — no flag parsing, no TUI. Only a password or passphrase
// question earns the secret; everything else, a missing prompt included, gets
// nothing: forced askpass sees the host-key question too, and that one must not
// be answered with a secret.
func TestRunAsAskpass(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		proxied    bool
		want       int
		wantStdout string
	}{
		{name: "password", prompt: "alice@example.com's password:", want: 0, wantStdout: "hunter2\n"},
		{name: "passphrase", prompt: "Enter passphrase for key '/home/a/.ssh/id_ed25519':", want: 0, wantStdout: "hunter2\n"},
		{
			// ProxyJump: the jump host's ssh inherits the helper and asks for its
			// own password. Same "password" prompt, different machine.
			name: "password for a host on the way", prompt: "alice@jump.example.com's password:", want: 1,
		},
		{
			// The user@ half is not the target's, so it can't stand in for it.
			name: "password for a look-alike login", prompt: "example.com@evil.example's password:", want: 1,
		},
		// Keyboard-interactive: ssh puts the machine in front of the server's
		// own wording, so a PAM question is attributable after all.
		{
			name: "keyboard-interactive for the target", prompt: "(alice@example.com) Password: ",
			want: 0, wantStdout: "hunter2\n",
		},
		{
			name: "keyboard-interactive for a host on the way", prompt: "(alice@jump.example.com) Password: ",
			want: 1,
		},
		{
			// The text after ssh's prefix is the server's, and a jump host is
			// free to write the target's name into it. The prefix decides.
			name:   "keyboard-interactive impersonating the target",
			prompt: "(alice@jump.example.com) root@example.com's password: ", want: 1,
		},
		// A PAM prompt from a client too old to prefix them names no host, and
		// refusing those outright would break the logins netdoc is there to
		// make — as long as only one machine could be asking.
		{name: "hostless PAM prompt", prompt: "Password: ", want: 0, wantStdout: "hunter2\n"},
		{
			// With a jump host in the way, either end could have sent it.
			name: "hostless PAM prompt on a proxied connection", prompt: "Password: ",
			proxied: true, want: 1,
		},
		{
			// The passphrase unlocks a local file, so no host is on the
			// receiving end of it and a proxy changes nothing.
			name: "passphrase on a proxied connection", prompt: "Enter passphrase for key '/home/a/.ssh/id_ed25519':",
			proxied: true, want: 0, wantStdout: "hunter2\n",
		},
		// A helper whose job is to refuse what it doesn't recognize has no
		// business guessing at a prompt that isn't there.
		{name: "no prompt", prompt: "", want: 1},
		{
			name:   "host key",
			prompt: "The authenticity of host 'example.com' can't be established.\nAre you sure you want to continue connecting (yes/no/[fingerprint])?",
			want:   1,
		},
		{
			// The host name rides along in the first line of the host-key
			// message, so it must not be able to answer the question.
			name:   "host key for a host named password",
			prompt: "The authenticity of host 'password-reset.example.com (192.0.2.1)' can't be established.\nAre you sure you want to continue connecting (yes/no/[fingerprint])?",
			want:   1,
		},
		{
			name:   "host key for a key stored in a passphrase directory",
			prompt: "The authenticity of host 'passphrase.example.com' can't be established.\nAre you sure you want to continue connecting (yes/no/[fingerprint])?",
			want:   1,
		},
		{
			// Trailing blank line must not read as "no prompt at all".
			name:   "host key with a trailing newline",
			prompt: "The authenticity of host 'password.example.com' can't be established.\nAre you sure you want to continue connecting (yes/no/[fingerprint])? \n",
			want:   1,
		},
		{
			// The price of refusing anything that mentions a fingerprint: a key
			// filed under that name has its passphrase prompt turned away too,
			// and the user types it on the terminal instead. Cheap at the price.
			name:   "passphrase for a key named fingerprint",
			prompt: "Enter passphrase for key '/home/a/.ssh/fingerprint_id':",
			want:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(ui.AskpassEnv, "hunter2")
			t.Setenv(ui.AskpassHostEnv, "example.com")
			if tt.proxied {
				t.Setenv(ui.AskpassProxyEnv, "1")
			}
			var stdout, stderr bytes.Buffer
			args := []string{tt.prompt}
			if tt.prompt == "" {
				args = nil
			}
			if got := run(args, &stdout, &stderr); got != tt.want {
				t.Errorf("run = %d, want %d", got, tt.want)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
			if tt.want == 0 && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

// An extra argument is echoed back to the terminal, so it has to come back as
// inert text: no OSC 52 clipboard grab, no CSI, no bidi override.
func TestRunBadArgsAreInert(t *testing.T) {
	const payload = "\x1b]52;c;aGk=\x07\x1b[31m\u202eevil\n"
	for _, args := range [][]string{
		{"example.com", payload},
		{"-" + payload},
	} {
		var stdout, stderr bytes.Buffer
		run(args, &stdout, &stderr)
		for _, bad := range []string{"\x1b", "\x07", "\u202e"} {
			if strings.Contains(stderr.String(), bad) {
				t.Errorf("run(%q): stderr = %q, want no %q", args, stderr.String(), bad)
			}
		}
	}
}

// Pins the seams around the shared TargetForms const: the "Target forms:"
// header, the blank line before "Flags:", and no trailing newline in the
// const itself — without freezing stdlib flag formatting.
func TestPrintUsageTargetForms(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf, flag.NewFlagSet("netdoc", flag.ContinueOnError))
	want := "Target forms:\n" + diagnostic.TargetForms + "\n\nFlags:"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("usage output missing the target-forms section:\n%s", buf.String())
	}
}

// Drives the real -json path through run() with probe execution stubbed out,
// pinning the headless contract: valid JSON on stdout, exit 1 iff a check failed.
func TestRunJSON(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	tests := []struct {
		name   string
		status diagnostic.Status
		want   int
	}{
		{"all pass", diagnostic.StatusPass, 0},
		{"a failure", diagnostic.StatusFail, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runAll = func(_ context.Context, probes []diagnostic.Probe) map[diagnostic.ProbeID]diagnostic.ProbeResult {
				results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
				for _, p := range probes {
					results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: tt.status}
				}
				return results
			}
			var stdout, stderr bytes.Buffer
			if got := run([]string{"-json", "example.com:443"}, &stdout, &stderr); got != tt.want {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, tt.want, stderr.String())
			}
			var rep report
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
			}
			if rep.OK != (tt.want == 0) {
				t.Errorf("ok = %v, want %v", rep.OK, tt.want == 0)
			}
			if rep.Target == nil || rep.Target.Host != "example.com" {
				t.Errorf("target = %+v", rep.Target)
			}
			if len(rep.Checks) == 0 {
				t.Error("checks empty, want the probe DAG")
			}
		})
	}
}

// -timeout is the only knob on the bounded-time contract, and rejecting a bad
// value proves nothing about a good one: a positive duration has to reach the
// probes before the run starts.
func TestRunAppliesTimeout(t *testing.T) {
	origRun, origTimeout := runAll, diagnostic.ProbeTimeout
	t.Cleanup(func() { runAll, diagnostic.ProbeTimeout = origRun, origTimeout })

	var applied time.Duration
	runAll = func(_ context.Context, probes []diagnostic.Probe) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		applied = diagnostic.ProbeTimeout
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"-timeout", "250ms", "-json", "example.com:443"}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", got, stderr.String())
	}
	if applied != 250*time.Millisecond {
		t.Errorf("ProbeTimeout during the run = %v, want 250ms", applied)
	}
}

// cancelAfter stops the watch loop once it has written n reports, standing in
// for the Ctrl-C that ends it in real use.
type cancelAfter struct {
	buf    *bytes.Buffer
	n      int
	cancel context.CancelFunc
}

func (c *cancelAfter) Write(p []byte) (int, error) {
	n, err := c.buf.Write(p)
	c.n--
	if c.n <= 0 {
		c.cancel()
	}
	return n, err
}

// The watch stream's promise is one self-contained, timestamped report per
// line — anything indented or unterminated breaks every line-oriented reader.
func TestRunJSONWatchStreamsOnePerLine(t *testing.T) {
	origRun, origEvery := runAll, watchEvery
	t.Cleanup(func() { runAll, watchEvery = origRun, origEvery })
	watchEvery = time.Millisecond
	runAll = func(_ context.Context, probes []diagnostic.Probe) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusFail}
		}
		return results
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf, stderr bytes.Buffer
	out := &cancelAfter{buf: &buf, n: 3, cancel: cancel}
	if got := runJSON(ctx, nil, nil, true, out, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", got, stderr.String())
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		var rep report
		if err := json.Unmarshal([]byte(line), &rep); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i, err, line)
		}
		if rep.Ts == "" {
			t.Errorf("line %d has no ts", i)
		}
		if len(rep.Checks) == 0 {
			t.Errorf("line %d has no checks", i)
		}
	}
}

// If the context is already cancelled before the first pass completes, there
// is no report to trust — runJSON must fail closed rather than default to 0.
func TestRunJSONInterruptedBeforeFirstReport(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runAll = func(ctx context.Context, probes []diagnostic.Probe) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		if ctx.Err() == nil {
			t.Error("runAll called with a live context, want it already cancelled")
		}
		return nil
	}

	var stdout, stderr bytes.Buffer
	if got := runJSON(ctx, nil, nil, true, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", got, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (no report for a cancelled pass)", stdout.String())
	}
}

// ms is the one field with an out-of-band meaning: 0 has to keep saying "never
// ran", which a sub-millisecond check would otherwise steal. Per-attempt ms
// carries the same promise — a LAN connect lands under a millisecond often.
func TestBuildReportFloorsSubMillisecondChecks(t *testing.T) {
	probes := []diagnostic.Probe{
		{ID: diagnostic.ProbeIface, Name: "Interface"},
		{ID: diagnostic.ProbeInternet, Name: "Internet"},
		{ID: diagnostic.ProbeTargetTCP, Name: "TCP"},
	}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeIface:    {ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass, Dur: 120 * time.Microsecond},
		diagnostic.ProbeInternet: {ID: diagnostic.ProbeInternet, Status: diagnostic.StatusSkip},
		diagnostic.ProbeTargetTCP: {
			ID:       diagnostic.ProbeTargetTCP,
			Status:   diagnostic.StatusPass,
			Dur:      2 * time.Millisecond,
			Attempts: []diagnostic.Attempt{{IP: net.ParseIP("192.168.1.1"), Dur: 300 * time.Microsecond}},
		},
	}
	rep := buildReport(nil, probes, results)
	if rep.Checks[0].Ms != 1 {
		t.Errorf("sub-millisecond check ms = %d, want 1", rep.Checks[0].Ms)
	}
	if rep.Checks[1].Ms != 0 {
		t.Errorf("check that never ran ms = %d, want 0", rep.Checks[1].Ms)
	}
	if got := rep.Checks[2].Attempts[0].Ms; got != 1 {
		t.Errorf("sub-millisecond attempt ms = %d, want 1", got)
	}
}

func TestBuildReport(t *testing.T) {
	target, err := diagnostic.ParseTarget("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	probes := []diagnostic.Probe{
		{ID: diagnostic.ProbeIface, Name: "Interface"},
		{ID: diagnostic.ProbeDNS, Name: "DNS example.com"},
		{ID: diagnostic.ProbeTargetTCP, Name: "TCP example.com:443"},
	}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeIface: {ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass, Detail: "interface eth0 is up", Iface: "eth0", Dur: 7 * time.Millisecond},
		diagnostic.ProbeDNS:   {ID: diagnostic.ProbeDNS, Status: diagnostic.StatusFail, Detail: "cannot resolve example.com", Fix: "check DNS", Dur: 1200 * time.Millisecond},
		// One row carrying every address field: buildReport stringifies them the
		// same way regardless of which probe produced them.
		diagnostic.ProbeTargetTCP: {
			ID:         diagnostic.ProbeTargetTCP,
			Status:     diagnostic.StatusPass,
			Detail:     "connected",
			Portal:     &diagnostic.Portal{RedirectURL: "https://portal.example/signin"},
			Addrs:      []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")},
			SelectedIP: net.ParseIP("2001:db8::1"),
			Source:     net.ParseIP("192.168.1.20"),
			Attempts: []diagnostic.Attempt{
				{IP: net.ParseIP("192.0.2.1"), Dur: 90 * time.Millisecond, Err: errors.New("connection refused")},
				{IP: net.ParseIP("2001:db8::1"), Dur: 12 * time.Millisecond},
			},
		},
	}
	rep := buildReport(target, probes, results)

	if rep.OK {
		t.Error("OK = true, want false (DNS failed)")
	}
	if rep.Target == nil || rep.Target.Host != "example.com" || rep.Target.Port != 443 || rep.Target.Protocol != "tls+http" {
		t.Errorf("target = %+v", rep.Target)
	}
	if len(rep.Checks) != 3 {
		t.Fatalf("got %d checks, want 3", len(rep.Checks))
	}
	if rep.Checks[0].Status != "PASS" || rep.Checks[0].Fix != "" {
		t.Errorf("iface check = %+v", rep.Checks[0])
	}
	if rep.Checks[1].Status != "FAIL" || rep.Checks[1].Fix != "check DNS" {
		t.Errorf("dns check = %+v", rep.Checks[1])
	}
	if rep.Checks[0].Ms != 7 || rep.Checks[1].Ms != 1200 {
		t.Errorf("timings = %d, %d ms; want 7, 1200", rep.Checks[0].Ms, rep.Checks[1].Ms)
	}
	if !strings.Contains(rep.Summary, "Cannot resolve example.com") {
		t.Errorf("summary = %q", rep.Summary)
	}
	// The first failing row, not merely a failing one — scripts route on this.
	if rep.FailedStage != string(diagnostic.ProbeDNS) {
		t.Errorf("failed_stage = %q, want %q", rep.FailedStage, diagnostic.ProbeDNS)
	}
	if rep.Verdict != diagnostic.VerdictDNS {
		t.Errorf("verdict = %q, want %q", rep.Verdict, diagnostic.VerdictDNS)
	}
	tcp := rep.Checks[2]
	if got, want := strings.Join(tcp.Addrs, ","), "192.0.2.1,2001:db8::1"; got != want {
		t.Errorf("addrs = %q, want %q", got, want)
	}
	if tcp.SelectedIP != "2001:db8::1" || tcp.Source != "192.168.1.20" {
		t.Errorf("selected_ip = %q, source = %q", tcp.SelectedIP, tcp.Source)
	}
	if tcp.Portal == nil || tcp.Portal.RedirectURL != "https://portal.example/signin" {
		t.Errorf("portal = %+v", tcp.Portal)
	}
	// Attempts keep probe order, and only a failed attempt carries an error.
	want := []reportAttempt{
		{IP: "192.0.2.1", Ms: 90, Err: "connection refused"},
		{IP: "2001:db8::1", Ms: 12},
	}
	if !reflect.DeepEqual(tcp.Attempts, want) {
		t.Errorf("attempts = %+v, want %+v", tcp.Attempts, want)
	}
}

func TestBuildReportGenericAllPass(t *testing.T) {
	probes := []diagnostic.Probe{{ID: diagnostic.ProbeIface, Name: "Interface"}}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeIface: {ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass, Detail: "up"},
	}
	rep := buildReport(nil, probes, results)
	if !rep.OK {
		t.Error("OK = false, want true")
	}
	if rep.Target != nil {
		t.Errorf("target = %+v, want nil", rep.Target)
	}
	if rep.Summary == "" {
		t.Error("summary empty, want all-clear text")
	}
}

func TestReportJSONContract(t *testing.T) {
	tests := []struct {
		name string
		rep  report
		want string
	}{
		{
			name: "populated",
			rep: report{
				Version: "1.2.3",
				Target:  &reportTarget{Host: "example.com", Port: 443, Protocol: "tls+http"},
				Checks: []reportCheck{{
					ID:         "target_tcp",
					Name:       "Target TCP",
					Status:     "WARN",
					Ms:         46,
					Detail:     "slow",
					Fix:        "check firewall",
					Addrs:      []string{"192.0.2.1"},
					SelectedIP: "192.0.2.1",
					Source:     "192.0.2.2",
					Iface:      "eth0",
					Network:    "office",
					Portal:     &reportPortal{RedirectURL: "https://portal.example/signin"},
					Attempts: []reportAttempt{
						{IP: "192.0.2.1", Ms: 12},
						{IP: "192.0.2.3", Ms: 34, Err: "timeout"},
					},
				}},
				Summary:     "degraded",
				Verdict:     "degraded",
				FailedStage: "tls",
				OK:          true,
			},
			want: `{"version":"1.2.3","target":{"host":"example.com","port":443,"protocol":"tls+http"},"checks":[{"id":"target_tcp","name":"Target TCP","status":"WARN","ms":46,"detail":"slow","fix":"check firewall","addrs":["192.0.2.1"],"selected_ip":"192.0.2.1","source":"192.0.2.2","iface":"eth0","network":"office","portal":{"redirect_url":"https://portal.example/signin"},"attempts":[{"ip":"192.0.2.1","ms":12},{"ip":"192.0.2.3","ms":34,"error":"timeout"}]}],"summary":"degraded","verdict":"degraded","failed_stage":"tls","ok":true}`,
		},
		{
			name: "empty",
			rep:  report{Checks: []reportCheck{{}}},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","ms":0,"detail":""}],"summary":"","verdict":"","ok":false}`,
		},
		{
			name: "portal without redirect",
			rep:  report{Checks: []reportCheck{{Portal: &reportPortal{}}}},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","ms":0,"detail":"","portal":{}}],"summary":"","verdict":"","ok":false}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.rep)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("JSON = %s\nwant   %s", got, tt.want)
			}
		})
	}
}
