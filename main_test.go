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
			want: `{"version":"1.2.3","target":{"host":"example.com","port":443,"protocol":"tls+http"},"checks":[{"id":"target_tcp","name":"Target TCP","status":"WARN","ms":46,"detail":"slow","fix":"check firewall","addrs":["192.0.2.1"],"selected_ip":"192.0.2.1","source":"192.0.2.2","iface":"eth0","network":"office","attempts":[{"ip":"192.0.2.1","ms":12},{"ip":"192.0.2.3","ms":34,"error":"timeout"}]}],"summary":"degraded","verdict":"degraded","failed_stage":"tls","ok":true}`,
		},
		{
			name: "empty",
			rep:  report{Checks: []reportCheck{{}}},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","ms":0,"detail":""}],"summary":"","verdict":"","ok":false}`,
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
