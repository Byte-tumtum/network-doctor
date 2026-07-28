// CLI surface: flag parsing and re-parsing, usage text, the version fallback,
// and the JSON report builder.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"strings"
	"testing"

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

func TestBuildReport(t *testing.T) {
	target, err := diagnostic.ParseTarget("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	probes := []diagnostic.Probe{
		{ID: diagnostic.ProbeIface, Name: "Interface"},
		{ID: diagnostic.ProbeDNS, Name: "DNS example.com"},
	}
	results := map[diagnostic.ProbeID]diagnostic.ProbeResult{
		diagnostic.ProbeIface: {ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass, Detail: "interface eth0 is up", Iface: "eth0"},
		diagnostic.ProbeDNS:   {ID: diagnostic.ProbeDNS, Status: diagnostic.StatusFail, Detail: "cannot resolve example.com", Fix: "check DNS"},
	}
	rep := buildReport(target, probes, results)

	if rep.OK {
		t.Error("OK = true, want false (DNS failed)")
	}
	if rep.Target == nil || rep.Target.Host != "example.com" || rep.Target.Port != 443 || rep.Target.Protocol != "tls+http" {
		t.Errorf("target = %+v", rep.Target)
	}
	if len(rep.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(rep.Checks))
	}
	if rep.Checks[0].Status != "PASS" || rep.Checks[0].Fix != "" {
		t.Errorf("iface check = %+v", rep.Checks[0])
	}
	if rep.Checks[1].Status != "FAIL" || rep.Checks[1].Fix != "check DNS" {
		t.Errorf("dns check = %+v", rep.Checks[1])
	}
	if !strings.Contains(rep.Summary, "Cannot resolve example.com") {
		t.Errorf("summary = %q", rep.Summary)
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
				Summary: "degraded",
				OK:      true,
			},
			want: `{"version":"1.2.3","target":{"host":"example.com","port":443,"protocol":"tls+http"},"checks":[{"id":"target_tcp","name":"Target TCP","status":"WARN","detail":"slow","fix":"check firewall","addrs":["192.0.2.1"],"selected_ip":"192.0.2.1","source":"192.0.2.2","iface":"eth0","network":"office","attempts":[{"ip":"192.0.2.1","ms":12},{"ip":"192.0.2.3","ms":34,"error":"timeout"}]}],"summary":"degraded","ok":true}`,
		},
		{
			name: "empty",
			rep:  report{Checks: []reportCheck{{}}},
			want: `{"version":"","target":null,"checks":[{"id":"","name":"","status":"","detail":""}],"summary":"","ok":false}`,
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
