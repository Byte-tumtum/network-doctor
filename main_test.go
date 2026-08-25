// CLI surface: flag parsing and re-parsing, usage text, the version fallback,
// and the JSON report builder.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/report"
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
		{"help lists check", []string{"-help"}, 0, "-check", ""},
		{"help lists skip", []string{"-help"}, 0, "-skip", ""},
		{"help lists -no-history", []string{"-help"}, 0, "-no-history", ""},
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
// nothing else, with no flag parsing and no TUI. Only a password or passphrase
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
		// make, as long as only one machine could be asking.
		{name: "hostless PAM prompt", prompt: "Password: ", want: 0, wantStdout: "hunter2\n"},
		{
			// With a jump host in the way, either end could have sent it.
			name: "hostless PAM prompt on a proxied connection", prompt: "Password: ",
			proxied: true, want: 1,
		},
		{
			// A real key passphrase names no host either, so under a proxy it is
			// indistinguishable from the case below and goes to the terminal.
			name: "passphrase on a proxied connection", prompt: "Enter passphrase for key '/home/a/.ssh/id_ed25519':",
			proxied: true, want: 1,
		},
		{
			// Why the line above can't be relaxed: the prompt a jump host sends
			// over keyboard-interactive is text it chose, so "passphrase" in it
			// is a claim, not evidence.
			name: "jump host calling its prompt a passphrase", prompt: "Enter passphrase for key '/home/a/.ssh/id_ed25519': ",
			proxied: true, want: 1,
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
// const itself, without freezing stdlib flag formatting.
func TestPrintUsageTargetForms(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf, flag.NewFlagSet("netdoc", flag.ContinueOnError))
	want := "Target forms:\n" + diagnostic.TargetForms + "\n\nFlags:"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("usage output missing the target-forms section:\n%s", buf.String())
	}
}

// -no-history is only a choice of the path handed to the UI, which already
// treats "" as in-memory only, so the seam worth pinning is that path: the
// default still points at the config file, the flag still resolves to "".
func TestHistoryFile(t *testing.T) {
	if got := historyFile(true); got != "" {
		t.Errorf("historyFile(true) = %q, want the empty path", got)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir on this host: %v", err)
	}
	want := filepath.Join(dir, "netdoc", "history")
	if got := historyFile(false); got != want {
		t.Errorf("historyFile(false) = %q, want %q", got, want)
	}
}

// A misspelled preset is rejected before anything runs, including under
// -json, where the keymap has no effect but the typo is still worth hearing
// about on the run that carries it.
func TestRunRejectsUnknownKeyPreset(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-keys", "emacs", "-json"}, &stdout, &stderr); code != 2 {
		t.Errorf("run(-keys emacs) = %d, want 2", code)
	}
	want := fmt.Sprintf("netdoc: -keys: unknown key preset %q (have: %s)\n", "emacs", strings.Join(ui.KeyPresets(), ", "))
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
	if stdout.Len() > 0 {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
}

func TestRunAcceptsVimKeyPreset(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, probe := range probes {
			results[probe.ID] = diagnostic.ProbeResult{ID: probe.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--keys=vim", "--json", "--check", "iface"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--keys=vim) = %d; stderr: %s", code, stderr.String())
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
			runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
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
			var rep report.Report
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

func TestRunProbeSelectionFlags(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"one check", []string{"--check", "tls"}, []string{"iface", "dns", "target_tcp", "tls"}},
		{"comma-separated checks", []string{"--check", "dns,target_tcp,tls"}, []string{"iface", "dns", "target_tcp", "tls"}},
		{"repeated checks", []string{"--check", "dns,target_tcp", "--check", "tls"}, []string{"iface", "dns", "target_tcp", "tls"}},
		{"equals check", []string{"--check=dns,target_tcp"}, []string{"iface", "dns", "target_tcp"}},
		{"duplicate checks", []string{"--check", "dns,dns"}, []string{"iface", "dns"}},
		{"one skip", []string{"--skip", "quic_udp_443"}, []string{"iface", "internet_tcp", "proxy_connect", "dns", "dns_public", "dns_encrypted", "target_tcp", "path_mtu", "ssid", "tls", "http", "https"}},
		{"multiple skips", []string{"--skip", "internet_tcp,quic_udp_443"}, []string{"iface", "proxy_connect", "dns", "dns_public", "dns_encrypted", "target_tcp", "path_mtu", "ssid", "tls", "http", "https"}},
		{"repeated skips", []string{"--skip", "internet_tcp", "--skip", "quic_udp_443"}, []string{"iface", "proxy_connect", "dns", "dns_public", "dns_encrypted", "target_tcp", "path_mtu", "ssid", "tls", "http", "https"}},
		{"equals repeated skips", []string{"--skip=dns", "--skip=target_tcp"}, []string{"iface", "internet_tcp", "quic_udp_443", "proxy_connect", "dns_public", "dns_encrypted", "ssid"}},
		{"combined", []string{"--check", "dns,target_tcp,tls", "--skip", "dns"}, []string{}},
		{"argument order", []string{"--check", "tls,dns,target_tcp"}, []string{"iface", "dns", "target_tcp", "tls"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--json", "example.com"}, tt.args...)
			if got := run(args, &stdout, &stderr); got != 0 {
				t.Fatalf("exit = %d, want 0; stderr: %s", got, stderr.String())
			}
			var rep report.Report
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(rep.Checks))
			for i, check := range rep.Checks {
				got[i] = check.ID
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("check IDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunProbeSelectionPreservesDiagnosis(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			status := diagnostic.StatusPass
			if p.ID == diagnostic.ProbeTargetTCP {
				status = diagnostic.StatusFail
			}
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: status}
		}
		return results
	}
	runReport := func(args ...string) report.Report {
		var stdout, stderr bytes.Buffer
		if got := run(args, &stdout, &stderr); got != 1 {
			t.Fatalf("exit = %d, want 1; stderr: %s", got, stderr.String())
		}
		var rep report.Report
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rep
	}

	baseline := runReport("--json", "1.1.1.1:81")
	skipped := runReport("--json", "--skip", "ssid", "1.1.1.1:81")
	if skipped.Summary != baseline.Summary || skipped.Verdict != baseline.Verdict {
		t.Fatalf("skipping SSID changed diagnosis from %q/%q to %q/%q", baseline.Summary, baseline.Verdict, skipped.Summary, skipped.Verdict)
	}
}

func TestRunKnownButInapplicableProbeSelection(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	runReport := func(t *testing.T, args ...string) report.Report {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if got := run(args, &stdout, &stderr); got != 0 {
			t.Fatalf("exit = %d, want 0; stderr: %s", got, stderr.String())
		}
		var rep report.Report
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rep
	}
	ids := func(rep report.Report) []string {
		got := make([]string, len(rep.Checks))
		for i, check := range rep.Checks {
			got[i] = check.ID
		}
		return got
	}

	empty := runReport(t, "--json", "--check", "tls", "host:9999")
	if len(empty.Checks) != 0 || empty.Summary != "No checks selected." || empty.Verdict != diagnostic.VerdictOK || !empty.OK {
		t.Fatalf("inapplicable check report = %+v", empty)
	}

	baseline := runReport(t, "--json", "host:9999")
	skipped := runReport(t, "--json", "--skip", "tls", "host:9999")
	if !reflect.DeepEqual(ids(skipped), ids(baseline)) {
		t.Errorf("inapplicable skip changed DAG: %v != %v", ids(skipped), ids(baseline))
	}
	if skipped.Summary != baseline.Summary || skipped.Verdict != baseline.Verdict || skipped.OK != baseline.OK {
		t.Errorf("inapplicable skip changed diagnosis: %+v != %+v", skipped, baseline)
	}

	withoutPublic := runReport(t, "--json", "--public-dns", "", "--skip", "dns_public", "host:9999")
	for _, id := range ids(withoutPublic) {
		if id == "dns_public" {
			t.Fatal("disabled public DNS probe was constructed")
		}
	}
}

func TestProbeListRejectsAtomically(t *testing.T) {
	var list probeList
	if err := list.Set("dns,"); err == nil {
		t.Fatal("trailing empty probe ID accepted")
	}
	if len(list) != 0 {
		t.Fatalf("failed parse retained IDs: %v", list)
	}
	if err := list.Set("dns"); err != nil {
		t.Fatal(err)
	}
	if err := list.Set("target_tcp,,tls"); err == nil {
		t.Fatal("later malformed probe list accepted")
	}
	if want := (probeList{diagnostic.ProbeDNS}); !reflect.DeepEqual(list, want) {
		t.Fatalf("later failed parse changed IDs: %v, want %v", list, want)
	}
}

func TestBuildReportEmptySelection(t *testing.T) {
	rep := buildReport(nil, nil, nil)
	if rep.Checks == nil || rep.Summary != "No checks selected." || rep.Verdict != diagnostic.VerdictOK || !rep.OK {
		t.Fatalf("empty report = %+v", rep)
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"checks":[]`)) {
		t.Fatalf("empty checks JSON = %s, want []", b)
	}
}

func TestRunRejectsInvalidProbeSelectionBeforeDiagnostics(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runs := 0
	runAll = func(context.Context, []diagnostic.Probe, time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		runs++
		return nil
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown check", []string{"--check", "bogus"}, `unknown probe ID "bogus"`},
		{"unknown skip", []string{"--skip", "bogus"}, `unknown probe ID "bogus"`},
		{"deterministic unknown", []string{"--check", "zzz,aaa"}, `unknown probe ID "aaa"`},
		{"whitespace is not normalized", []string{"--check", " dns"}, `unknown probe ID " dns"`},
		{"empty check", []string{"--check", ""}, "empty probe ID"},
		{"empty equals check", []string{"--check="}, "empty probe ID"},
		{"empty trailing check", []string{"--check", "dns,"}, "empty probe ID"},
		{"empty middle check", []string{"--check", "dns,,tls"}, "empty probe ID"},
		{"empty leading skip", []string{"--skip", ",dns"}, "empty probe ID"},
		{"empty skip", []string{"--skip", ""}, "empty probe ID"},
		{"empty equals skip", []string{"--skip="}, "empty probe ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--json"}, tt.args...)
			args = append(args, "example.com")
			if got := run(args, &stdout, &stderr); got != 2 {
				t.Fatalf("exit = %d, want 2", got)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
	if runs != 0 {
		t.Errorf("diagnostics ran %d times for rejected selections", runs)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--json", "--check", "bogus", "example.com"}, &stdout, &stderr); got != 2 {
		t.Fatalf("global-ID error exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "ssh_banner") || !strings.Contains(stderr.String(), "smtp_banner") {
		t.Errorf("valid-ID list is target-specific: %q", stderr.String())
	}
	if runs != 0 {
		t.Errorf("diagnostics ran %d times while listing valid IDs", runs)
	}
}

// -public-dns is the opt-out for the one third-party resolver netdoc queries.
// Driven through run() rather than the flag set, because the value has to
// survive parsing, validation, and probe construction to reach the report.
func TestRunPublicDNSFlag(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantRow  string // "" means the row must be absent
	}{
		{"default is unchanged", nil, 0, "DNS (public 8.8.8.8)"},
		{"custom IPv4", []string{"-public-dns", "9.9.9.9"}, 0, "DNS (public 9.9.9.9)"},
		{"custom IPv6", []string{"-public-dns", "2620:fe::fe"}, 0, "DNS (public 2620:fe::fe)"},
		{"IPv6 is canonicalized", []string{"-public-dns", "2620:00fe:0000::00FE"}, 0, "DNS (public 2620:fe::fe)"},
		{"empty argument disables", []string{"-public-dns", ""}, 0, ""},
		{"empty via equals disables", []string{"-public-dns="}, 0, ""},
		{"hostname rejected", []string{"-public-dns", "dns.google"}, 2, ""},
		{"truncated address rejected", []string{"-public-dns", "8.8.8"}, 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"-json"}, tt.args...), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if tt.wantCode != 0 {
				if !strings.Contains(stderr.String(), "-public-dns") {
					t.Errorf("stderr = %q, want it to name the rejected flag", stderr.String())
				}
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want no report for a rejected value", stdout.String())
				}
				return
			}
			var rep report.Report
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
			}
			got := ""
			for _, c := range rep.Checks {
				if c.ID == "dns_public" {
					got = c.Name
				}
			}
			if got != tt.wantRow {
				t.Errorf("dns_public row = %q, want %q", got, tt.wantRow)
			}
		})
	}
}

// -timeout is the only knob on the bounded-time contract, and rejecting a bad
// value proves nothing about a good one: a positive duration has to reach the
// probes before the run starts.
func TestRunAppliesTimeout(t *testing.T) {
	origRun := runAll
	t.Cleanup(func() { runAll = origRun })

	var applied time.Duration
	runAll = func(_ context.Context, probes []diagnostic.Probe, timeout time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		applied = timeout
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
		t.Errorf("timeout passed to the runner = %v, want 250ms", applied)
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
// line, and anything indented or unterminated breaks every line-oriented reader.
func TestRunJSONWatchStreamsOnePerLine(t *testing.T) {
	origRun, origEvery := runAll, ui.WatchEvery
	t.Cleanup(func() { runAll, ui.WatchEvery = origRun, origEvery })
	ui.WatchEvery = time.Millisecond
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
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
	if got := runJSON(ctx, nil, nil, true, diagnostic.DefaultPublicDNS, diagnostic.ProbeSelection{}, diagnostic.DefaultProbeTimeout, out, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", got, stderr.String())
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		var rep report.Report
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

func TestRunJSONWatchHandlesEmptySelection(t *testing.T) {
	origRun, origEvery := runAll, ui.WatchEvery
	t.Cleanup(func() { runAll, ui.WatchEvery = origRun, origEvery })
	ui.WatchEvery = time.Millisecond
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		if len(probes) != 0 {
			t.Fatalf("empty selection ran probes: %v", probes)
		}
		return map[diagnostic.ProbeID]diagnostic.ProbeResult{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf, stderr bytes.Buffer
	out := &cancelAfter{buf: &buf, n: 2, cancel: cancel}
	selection := diagnostic.ProbeSelection{Check: map[diagnostic.ProbeID]struct{}{diagnostic.ProbeTLS: {}}}
	if got := runJSON(ctx, nil, nil, true, diagnostic.DefaultPublicDNS, selection, diagnostic.DefaultProbeTimeout, out, &stderr); got != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", got, stderr.String())
	}
	for i, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rep report.Report
		if err := json.Unmarshal([]byte(line), &rep); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if rep.Checks == nil || len(rep.Checks) != 0 || rep.Summary != "No checks selected." || !rep.OK {
			t.Errorf("line %d empty report = %+v", i, rep)
		}
	}
}

// If the context is already cancelled before the first pass completes, there
// is no report to trust, so runJSON must fail closed rather than default to 0.
func TestRunJSONInterruptedBeforeFirstReport(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runAll = func(ctx context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		if ctx.Err() == nil {
			t.Error("runAll called with a live context, want it already cancelled")
		}
		return nil
	}

	var stdout, stderr bytes.Buffer
	if got := runJSON(ctx, nil, nil, true, diagnostic.DefaultPublicDNS, diagnostic.ProbeSelection{}, diagnostic.DefaultProbeTimeout, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", got, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (no report for a cancelled pass)", stdout.String())
	}
}

// ms is the one field with an out-of-band meaning: 0 has to keep saying "never
// ran", which a sub-millisecond check would otherwise steal. Per-attempt ms
// carries the same promise, since a LAN connect lands under a millisecond often.
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

func TestBuildReportAddsAddressFamilyEvidenceWithoutChangingOtherRows(t *testing.T) {
	probes := []diagnostic.Probe{{ID: diagnostic.ProbeInternet, Name: "Internet"}, {ID: diagnostic.ProbeIface, Name: "Interface"}}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeInternet: {Status: diagnostic.StatusWarn, Families: &diagnostic.FamilyConnectivity{
			IPv4: diagnostic.FamilyReachable, IPv6: diagnostic.FamilyUnreachable,
		}},
		diagnostic.ProbeIface: {Status: diagnostic.StatusPass},
	}
	rep := buildReport(nil, probes, results)
	if rep.Checks[0].Families == nil || rep.Checks[0].Families.IPv4 != "reachable" || rep.Checks[0].Families.IPv6 != "unreachable" {
		t.Errorf("internet families = %+v", rep.Checks[0].Families)
	}
	if rep.Checks[1].Families != nil {
		t.Errorf("unrelated row gained family evidence: %+v", rep.Checks[1])
	}
	blob, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(blob, []byte(`"address_families":{"ipv4":"reachable","ipv6":"unreachable"}`)) {
		t.Errorf("JSON missing address families: %s", blob)
	}
	results[diagnostic.ProbeInternet] = diagnostic.ProbeResult{Status: diagnostic.StatusPass, Families: &diagnostic.FamilyConnectivity{
		IPv4: diagnostic.FamilyReachable,
	}}
	availableOnly, err := json.Marshal(buildReport(nil, probes, results))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(availableOnly, []byte(`"address_families":{"ipv4":"reachable"}`)) || bytes.Contains(availableOnly, []byte(`"ipv6"`)) {
		t.Errorf("JSON did not omit the untested family: %s", availableOnly)
	}
}

func TestBuildReportKeepsEncryptedResolverWarningFunctional(t *testing.T) {
	probes := []diagnostic.Probe{
		{ID: diagnostic.ProbeInternet, Name: "Internet"},
		{ID: diagnostic.ProbeDNS, Name: "DNS"},
		{ID: diagnostic.ProbeDNSEncrypted, Name: "DNS (encrypted DoH/DoT)"},
	}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeInternet:     {Status: diagnostic.StatusPass},
		diagnostic.ProbeDNS:          {Status: diagnostic.StatusPass},
		diagnostic.ProbeDNSEncrypted: {Status: diagnostic.StatusWarn, Detail: "resolver answered SERVFAIL"},
	}
	rep := buildReport(nil, probes, results)
	if !rep.OK || rep.FailedStage != "" || rep.Verdict != diagnostic.VerdictDegraded ||
		rep.Checks[2].Status != "WARN" || rep.Summary == "" {
		t.Fatalf("report = %+v, want a visible WARN that remains functional and does not become a failed stage", rep)
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
	// The first failing row, not merely a failing one, since scripts route on this.
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
	want := []report.Attempt{
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
		rep  report.Report
		want string
	}{
		{
			name: "populated",
			rep: report.Report{
				Version: "1.2.3",
				Target:  &report.Target{Host: "example.com", Port: 443, Protocol: "tls+http"},
				Checks: []report.Check{{
					ID:         "target_tcp",
					Name:       "Target TCP",
					Status:     "WARN",
					Cause:      "client_dns_failure",
					Families:   &report.Families{IPv4: "reachable", IPv6: "unreachable"},
					Ms:         46,
					Detail:     "slow",
					Fix:        "check firewall",
					Addrs:      []string{"192.0.2.1"},
					SelectedIP: "192.0.2.1",
					Source:     "192.0.2.2",
					Iface:      "eth0",
					Network:    "office",
					Portal:     &report.Portal{RedirectURL: "https://portal.example/signin"},
					Attempts: []report.Attempt{
						{IP: "192.0.2.1", Ms: 12},
						{IP: "192.0.2.3", Ms: 34, Err: "timeout"},
					},
				}},
				Summary:     "degraded",
				Verdict:     "degraded",
				FailedStage: "tls",
				OK:          true,
			},
			want: `{"version":"1.2.3","target":{"host":"example.com","port":443,"protocol":"tls+http"},"checks":[{"id":"target_tcp","name":"Target TCP","status":"WARN","cause":"client_dns_failure","address_families":{"ipv4":"reachable","ipv6":"unreachable"},"ms":46,"detail":"slow","fix":"check firewall","addrs":["192.0.2.1"],"selected_ip":"192.0.2.1","source":"192.0.2.2","iface":"eth0","network":"office","portal":{"redirect_url":"https://portal.example/signin"},"attempts":[{"ip":"192.0.2.1","ms":12},{"ip":"192.0.2.3","ms":34,"error":"timeout"}]}],"summary":"degraded","verdict":"degraded","failed_stage":"tls","ok":true}`,
		},
		{
			// Findings serialize between failed_stage and ok, and an empty
			// evidence list stays absent rather than becoming [].
			name: "findings",
			rep: report.Report{
				Checks:   []report.Check{{}},
				Summary:  "cert expired",
				Verdict:  "service",
				Findings: []report.Finding{{ID: "tls_certificate_expired", Focus: "tls", Evidence: []string{"tls", "target_tcp"}}, {ID: "quic_unavailable"}},
			},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","ms":0,"detail":""}],"summary":"cert expired","verdict":"service","findings":[{"id":"tls_certificate_expired","focus":"tls","evidence":["tls","target_tcp"]},{"id":"quic_unavailable"}],"ok":false}`,
		},
		{
			name: "empty",
			rep:  report.Report{Checks: []report.Check{{}}},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","ms":0,"detail":""}],"summary":"","verdict":"","ok":false}`,
		},
		{
			name: "portal without redirect",
			rep:  report.Report{Checks: []report.Check{{Portal: &report.Portal{}}}},
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

// The TUI renders to stdout, so without a terminal there it has nowhere to
// draw. Bubble Tea would fail on its own with a /dev/tty message and exit 1,
// which callers cannot tell apart from a failed diagnosis; the guard names the
// supported non-interactive path and exits 2 instead.
func TestRunRejectsInteractiveWithoutTerminalStdout(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(context.Context, []diagnostic.Probe, time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		t.Error("runAll called; the guard must reject before any probe runs")
		return nil
	}
	const want = "netdoc: stdout is not a terminal; use --json for non-interactive output\n"
	for _, args := range [][]string{nil, {"example.com"}, {"-toolbox"}, {"-watch"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 2 {
				t.Errorf("run(%v) = %d, want 2 (environment problem, not a failed diagnosis)", args, code)
			}
			if stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			if strings.Contains(stderr.String(), "/dev/tty") {
				t.Errorf("stderr = %q, want netdoc's own error instead of Bubble Tea's", stderr.String())
			}
			if stdout.Len() > 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}

// Only stdout decides, and only through the descriptor: a real terminal is
// accepted so interactive startup still reaches the TUI, while a pipe, a file,
// and a writer with no descriptor at all are not terminals.
func TestStdoutIsTerminal(t *testing.T) {
	orig := termIsTerminal
	t.Cleanup(func() { termIsTerminal = orig })

	termIsTerminal = func(uintptr) bool { return true }
	if !stdoutIsTerminal(os.Stdout) {
		t.Error("stdoutIsTerminal(terminal) = false, want true: interactive startup must not be rejected")
	}
	if stdoutIsTerminal(&bytes.Buffer{}) {
		t.Error("stdoutIsTerminal(no file descriptor) = true, want false")
	}

	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	termIsTerminal = func(uintptr) bool { return false }
	if stdoutIsTerminal(f) {
		t.Error("stdoutIsTerminal(redirected file) = true, want false")
	}
}

// -json is the supported non-interactive path, so it has to survive the
// redirect that the TUI guard rejects: a real file as stdout, still exit 0 with
// a parseable report in it.
func TestRunJSONAllowedWithRedirectedStdout(t *testing.T) {
	orig := runAll
	t.Cleanup(func() { runAll = orig })
	runAll = func(_ context.Context, probes []diagnostic.Probe, _ time.Duration) map[diagnostic.ProbeID]diagnostic.ProbeResult {
		results := make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(probes))
		for _, p := range probes {
			results[p.ID] = diagnostic.ProbeResult{ID: p.ID, Status: diagnostic.StatusPass}
		}
		return results
	}
	f, err := os.CreateTemp(t.TempDir(), "report")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close() //nolint:errcheck

	var stderr bytes.Buffer
	if code := run([]string{"-json", "-check", "iface"}, f, &stderr); code != 0 {
		t.Fatalf("run(-json) = %d, want 0; stderr: %s", code, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
	if !rep.OK {
		t.Errorf("ok = false, want true with every check passing")
	}
}

// The report's diagnostic fields all come from one interpretation, so a
// finding cannot describe a different run from the summary printed beside it.
// This checks the JSON the flag actually emits against the engine's own answer
// rather than against a copy of it.
func TestBuildReportFindingsComeFromTheDiagnosis(t *testing.T) {
	target := &diagnostic.Target{Host: "example.com", Port: 443, Proto: diagnostic.ProtoTLSHTTP}
	probes := []diagnostic.Probe{
		{ID: diagnostic.ProbeIface, Name: "Interface"},
		{ID: diagnostic.ProbeInternet, Name: "Internet (TCP egress)"},
		{ID: diagnostic.ProbeDNS, Name: "DNS example.com"},
		{ID: diagnostic.ProbeTargetTCP, Name: "TCP example.com:443"},
		{ID: diagnostic.ProbeTLS, Name: "TLS example.com"},
	}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeIface:     {Status: diagnostic.StatusPass},
		diagnostic.ProbeInternet:  {Status: diagnostic.StatusPass},
		diagnostic.ProbeDNS:       {Status: diagnostic.StatusPass},
		diagnostic.ProbeTargetTCP: {Status: diagnostic.StatusPass},
		diagnostic.ProbeTLS:       {Status: diagnostic.StatusFail, Cause: diagnostic.TLSCauseHostnameMismatch},
	}
	order := []diagnostic.ProbeID{}
	for _, p := range probes {
		order = append(order, p.ID)
	}
	want := diagnostic.Interpret(target, order, results)

	rep := buildReport(target, probes, results)
	if rep.Summary != want.Summary || rep.Verdict != want.Verdict {
		t.Errorf("summary/verdict = %q/%q, want %q/%q", rep.Summary, rep.Verdict, want.Summary, want.Verdict)
	}
	if len(rep.Findings) != len(want.Findings) {
		t.Fatalf("%d findings, want %d", len(rep.Findings), len(want.Findings))
	}
	for i, f := range want.Findings {
		got := rep.Findings[i]
		if got.ID != string(f.ID) || got.Focus != string(f.Focus) {
			t.Errorf("finding %d = %+v, want id %q focus %q", i, got, f.ID, f.Focus)
		}
		if len(got.Evidence) != len(f.Evidence) {
			t.Fatalf("finding %d evidence = %v, want %v", i, got.Evidence, f.Evidence)
		}
		for j, id := range f.Evidence {
			if got.Evidence[j] != string(id) {
				t.Errorf("finding %d evidence[%d] = %q, want %q", i, j, got.Evidence[j], id)
			}
		}
	}
	// The blamed row is where the remedy lives, so the report must carry a
	// check with that id rather than a fix copied into the finding.
	if len(rep.Findings) > 0 {
		found := false
		for _, c := range rep.Checks {
			found = found || c.ID == rep.Findings[0].Focus
		}
		if !found {
			t.Errorf("finding blames %q, which is not a check in the report", rep.Findings[0].Focus)
		}
	}

	// A run with nothing to conclude keeps the report it has always emitted:
	// no findings key at all.
	for id := range results {
		results[id] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	}
	blob, err := json.Marshal(buildReport(target, probes, results))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "findings") {
		t.Errorf("a healthy run emitted a findings key: %s", blob)
	}
}
