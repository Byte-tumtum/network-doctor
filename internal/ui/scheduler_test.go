// TUI probe scheduling: sibling independence, skip propagation, and
// end-of-run bookkeeping.

package ui

import (
	"net"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func mustTarget(t *testing.T, s string) *diagnostic.Target {
	t.Helper()
	target, err := diagnostic.ParseTarget(s)
	if err != nil {
		t.Fatalf("parseTarget(%q): %v", s, err)
	}
	return target
}

// Generic mode: egress, proxy egress, and DNS are siblings — an egress
// failure must not skip DNS, so DNS-down-but-internet-up remains diagnosable.
func TestSiblingIndependence(t *testing.T) {
	m := newModel(nil, false)
	m.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass}
	m.started[diagnostic.ProbeIface] = true
	cmds := m.scheduleStep()
	if len(cmds) != 5 {
		t.Fatalf("want 5 dispatched (internet, proxy, system/public dns, ssid), got %d", len(cmds))
	}
	if !m.started[diagnostic.ProbeInternet] || !m.started[diagnostic.ProbeProxy] || !m.started[diagnostic.ProbeDNS] || !m.started[diagnostic.ProbeDNSPublic] || !m.started[diagnostic.ProbeSSID] {
		t.Fatal("internet+proxy+system/public dns+ssid should all be dispatched")
	}
	if _, ok := m.results[diagnostic.ProbeDNS]; ok {
		t.Error("dns must be dispatched, not skipped by an egress failure")
	}
}

func TestSkipPropagation(t *testing.T) {
	m := newModel(mustTarget(t, "github.com"), false)
	m.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	m.results[diagnostic.ProbeInternet] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	m.results[diagnostic.ProbeDNS] = diagnostic.ProbeResult{Status: diagnostic.StatusFail}
	m.started[diagnostic.ProbeIface], m.started[diagnostic.ProbeInternet], m.started[diagnostic.ProbeDNS] = true, true, true
	m.scheduleStep()
	if m.results[diagnostic.ProbeTargetTCP].Status != diagnostic.StatusSkip {
		t.Fatalf("target_tcp = %v, want Skip", m.results[diagnostic.ProbeTargetTCP].Status)
	}
	if m.results[diagnostic.ProbeTLS].Status != diagnostic.StatusSkip || m.results[diagnostic.ProbeHTTP].Status != diagnostic.StatusSkip || m.results[diagnostic.ProbeHTTPS].Status != diagnostic.StatusSkip {
		t.Error("skip must propagate through TLS, HTTP, and HTTPS")
	}
}

// When the last real probe result arrives and the run only completes via the
// skip cascade inside scheduleStep, DowngradeEgress must still run — otherwise
// a proxy-only network shows internet FAIL in the TUI but WARN in -json.
func TestDowngradeRunsWhenSkipsFinishRun(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	pass := func(id diagnostic.ProbeID) {
		m.results[id] = diagnostic.ProbeResult{ID: id, Status: diagnostic.StatusPass}
		m.started[id] = true
	}
	fail := func(id diagnostic.ProbeID) {
		m.results[id] = diagnostic.ProbeResult{ID: id, Status: diagnostic.StatusFail}
		m.started[id] = true
	}
	pass(diagnostic.ProbeIface)
	pass(diagnostic.ProbeSSID)
	fail(diagnostic.ProbeInternet)
	pass(diagnostic.ProbeProxy)
	pass(diagnostic.ProbeDNS)
	pass(diagnostic.ProbeDNSPublic)
	fail(diagnostic.ProbeHTTP)
	m.started[diagnostic.ProbeTargetTCP] = true // in flight; its done-msg arrives below

	u, _ := m.Update(probeDoneMsg{id: diagnostic.ProbeTargetTCP, gen: 0, res: diagnostic.ProbeResult{Status: diagnostic.StatusFail}})
	nm := asModel(t, u)
	if !nm.allDone() {
		t.Fatal("run should complete via the tls/https skip cascade")
	}
	if got := nm.results[diagnostic.ProbeInternet].Status; got != diagnostic.StatusWarn {
		t.Fatalf("internet = %v, want Warn (proxy works, egress downgraded)", got)
	}
}

func TestCompletedRunSelectsFirstFailure(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	for _, id := range []diagnostic.ProbeID{
		diagnostic.ProbeIface,
		diagnostic.ProbeSSID,
		diagnostic.ProbeInternet,
		diagnostic.ProbeProxy,
		diagnostic.ProbeDNS,
		diagnostic.ProbeDNSPublic,
		diagnostic.ProbeHTTP,
	} {
		m.results[id] = diagnostic.ProbeResult{ID: id, Status: diagnostic.StatusPass}
		m.started[id] = true
	}
	m.started[diagnostic.ProbeTargetTCP] = true

	u, _ := m.Update(probeDoneMsg{id: diagnostic.ProbeTargetTCP, res: diagnostic.ProbeResult{Status: diagnostic.StatusFail, Detail: "connection refused"}})
	nm := asModel(t, u)
	if nm.selected != 5 {
		t.Fatalf("selected = %d, want first failed probe 5", nm.selected)
	}
	if !strings.Contains(nm.bodyView(false), "connection refused") {
		t.Error("details panel must show the selected failure")
	}
}

func TestWatchRecordsAndRestarts(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	m.watch = true
	for _, probe := range m.probes {
		m.started[probe.ID] = true
		m.results[probe.ID] = diagnostic.ProbeResult{ID: probe.ID, Status: diagnostic.StatusPass}
	}
	last := m.probes[len(m.probes)-1]
	m.results[last.ID] = diagnostic.ProbeResult{ID: last.ID, Status: diagnostic.StatusFail}

	u, cmd := m.Update(probeDoneMsg{id: last.ID, res: m.results[last.ID]})
	completed := asModel(t, u)
	if cmd == nil {
		t.Fatal("watched completion must schedule another pass")
	}
	for _, probe := range completed.probes {
		if got := len(completed.runHistory[probe.ID]); got != 1 {
			t.Fatalf("%s history length = %d, want 1", probe.ID, got)
		}
	}
	if got := completed.historyLine(last.ID); !strings.Contains(got, "failed 1 of 1 runs") {
		t.Fatalf("history summary = %q", got)
	}

	u, cmd = completed.Update(watchMsg{gen: completed.generation})
	restarted := asModel(t, u)
	if restarted.generation != completed.generation+1 || len(restarted.results) != 0 {
		t.Fatalf("watch restart generation/results = %d/%d", restarted.generation, len(restarted.results))
	}
	if len(restarted.runHistory[last.ID]) != 1 || cmd == nil {
		t.Fatal("watch restart must retain history and schedule the probe root")
	}
}

func TestWatchHistoryKeepsLastTwentyRuns(t *testing.T) {
	m := newModel(nil, false)
	m.watch = true
	for run := 0; run < watchRuns+1; run++ {
		for _, probe := range m.probes {
			status := diagnostic.StatusPass
			if probe.ID == diagnostic.ProbeIface && run%2 == 0 {
				status = diagnostic.StatusFail
			}
			m.results[probe.ID] = diagnostic.ProbeResult{ID: probe.ID, Status: status}
		}
		m.recordRun()
	}
	history := m.runHistory[diagnostic.ProbeIface]
	if len(history) != watchRuns {
		t.Fatalf("history length = %d, want %d", len(history), watchRuns)
	}
	if history[0] != diagnostic.StatusPass {
		t.Fatalf("oldest retained status = %v, want second run's PASS", history[0])
	}
}

func TestNADoesNotBlock(t *testing.T) {
	m := newModel(mustTarget(t, "1.1.1.1"), false)
	m.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	m.results[diagnostic.ProbeInternet] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	m.results[diagnostic.ProbeDNS] = diagnostic.ProbeResult{Status: diagnostic.StatusNA, Addrs: []net.IP{net.ParseIP("1.1.1.1")}}
	m.started[diagnostic.ProbeIface], m.started[diagnostic.ProbeInternet], m.started[diagnostic.ProbeDNS] = true, true, true
	m.scheduleStep()
	if _, ok := m.results[diagnostic.ProbeTargetTCP]; ok {
		t.Fatal("an NA dependency must not skip target_tcp")
	}
	if !m.started[diagnostic.ProbeTargetTCP] {
		t.Fatal("target_tcp should be dispatched after an NA dependency")
	}
}
