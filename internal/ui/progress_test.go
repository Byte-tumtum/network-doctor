// The context strip's aggregate progress: what counts as complete, what counts
// as running, and when the strip stops saying either.

package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// contextStrip is the header context strip with its styling taken off.
func contextStrip(m model) string { return ansi.Strip(m.headerView()) }

// midRun is a run with the interface probe answered, the system resolver in
// flight, and everything downstream of them still waiting on a dependency.
func midRun(t *testing.T) model {
	t.Helper()
	m := newModel(mustTarget(t, "github.com:443"), false)
	m.started[diagnostic.ProbeIface] = true
	m.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass}
	m.started[diagnostic.ProbeDNS] = true
	return m
}

// One answered probe, one dispatched, and a dozen probes that have not been
// dispatched because their dependencies have not answered. Only the first two
// are counted, and the rest are pending rather than running.
func TestProgressCountsCompleteAndRunning(t *testing.T) {
	m := midRun(t)
	want := fmt.Sprintf("1/%d complete  ·  1 running", len(m.probes))
	if got := contextStrip(m); !strings.Contains(got, want) {
		t.Fatalf("context strip = %q, want it to carry %q", got, want)
	}
	if m.started[diagnostic.ProbeTLS] {
		t.Fatal("the fixture dispatched a probe that should still be waiting on a dependency")
	}
}

// Nothing has been dispatched yet, so nothing may be described as running.
func TestProgressIsSilentBeforeTheRunStarts(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	if got := contextStrip(m); strings.Contains(got, "running") || strings.Contains(got, "complete") {
		t.Fatalf("context strip = %q, want no progress before the chain starts", got)
	}
}

// A pass with work behind it and nothing in flight is a truthful state: the
// completed count stands and no running count is invented to look busy.
func TestProgressOmitsRunningWhenNothingIsInFlight(t *testing.T) {
	m := midRun(t)
	m.results[diagnostic.ProbeDNS] = diagnostic.ProbeResult{ID: diagnostic.ProbeDNS, Status: diagnostic.StatusPass}
	got := contextStrip(m)
	if want := fmt.Sprintf("2/%d complete", len(m.probes)); !strings.Contains(got, want) {
		t.Fatalf("context strip = %q, want it to carry %q", got, want)
	}
	if strings.Contains(got, "running") {
		t.Errorf("context strip = %q, want no running count while nothing is dispatched", got)
	}
}

// Skip and N/A are results, so they finish their probe the moment the
// scheduler emits them, exactly as a pass or a fail does.
func TestProgressCountsSkipAndNAAsComplete(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	for _, id := range []diagnostic.ProbeID{diagnostic.ProbeIface, diagnostic.ProbeInternet} {
		m.started[id] = true
		m.results[id] = diagnostic.ProbeResult{ID: id, Status: diagnostic.StatusPass}
	}
	m.started[diagnostic.ProbeSSID] = true
	m.results[diagnostic.ProbeSSID] = diagnostic.ProbeResult{ID: diagnostic.ProbeSSID, Status: diagnostic.StatusNA}
	// A failed resolver skips the whole target rung through the real scheduler,
	// so the skips counted here are the ones the run actually emitted.
	m.started[diagnostic.ProbeDNS] = true
	m.results[diagnostic.ProbeDNS] = diagnostic.ProbeResult{ID: diagnostic.ProbeDNS, Status: diagnostic.StatusFail}
	m.scheduleStep()
	if m.results[diagnostic.ProbeTargetTCP].Status != diagnostic.StatusSkip {
		t.Fatal("the fixture did not produce a dependency-driven skip")
	}
	done, running := m.runProgress()
	var wantDone int
	for _, p := range m.probes {
		if _, ok := m.results[p.ID]; ok {
			wantDone++
		}
	}
	if done != wantDone {
		t.Errorf("complete = %d, want every emitted result counted (%d)", done, wantDone)
	}
	if done+running > len(m.probes) {
		t.Errorf("complete+running = %d, more than the %d probes in the plan", done+running, len(m.probes))
	}
	if got := contextStrip(m); !strings.Contains(got, fmt.Sprintf("%d/%d complete", done, len(m.probes))) {
		t.Errorf("context strip = %q, want the emitted results counted", got)
	}
}

// The denominator is this run's plan, not everything netdoc can check: --check
// and --skip both move it.
func TestProgressDenominatorIsTheCurrentPlan(t *testing.T) {
	full := len(newModel(mustTarget(t, "example.com:443"), false).probes)
	for _, tc := range []struct {
		name string
		sel  diagnostic.ProbeSelection
	}{
		{"check", diagnostic.ProbeSelection{Check: map[diagnostic.ProbeID]struct{}{diagnostic.ProbeTLS: {}}}},
		{"skip", diagnostic.ProbeSelection{Skip: map[diagnostic.ProbeID]struct{}{diagnostic.ProbeDNSEncrypted: {}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewWithSelection(mustTarget(t, "example.com:443"), nil, false, false, "", "test", diagnostic.DefaultPublicDNS, true, tc.sel).(model)
			if len(m.probes) >= full {
				t.Fatalf("selection did not reduce the plan: %d of %d probes", len(m.probes), full)
			}
			m.started[diagnostic.ProbeIface] = true
			if got, want := contextStrip(m), fmt.Sprintf("0/%d complete", len(m.probes)); !strings.Contains(got, want) {
				t.Fatalf("context strip = %q, want %q", got, want)
			}
		})
	}
}

// The verdict already says the run finished, so the strip stops repeating it
// rather than parking a permanent "13/13 complete  ·  0 running" under it.
func TestProgressDisappearsWhenTheRunCompletes(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	for _, p := range m.probes {
		m.started[p.ID] = true
	}
	doneResults(&m, "")
	got := contextStrip(m)
	if strings.Contains(got, "complete") || strings.Contains(got, "running") {
		t.Fatalf("context strip = %q, want no progress on a finished run", got)
	}
	if !strings.Contains(got, "github.com:443") {
		t.Errorf("context strip = %q, want it to keep its target", got)
	}
}

// A watch pass describes itself. The previous pass's counts go with its
// results, and the new pass starts from nothing dispatched.
func TestWatchPassDoesNotInheritStaleProgress(t *testing.T) {
	m := newModel(mustTarget(t, "github.com:443"), false)
	m.watch = true
	for _, p := range m.probes {
		m.started[p.ID] = true
	}
	doneResults(&m, "")
	total := len(m.probes)

	next := asModel(t, must(m.Update(watchMsg{gen: m.generation})))
	if got := contextStrip(next); strings.Contains(got, "complete") || strings.Contains(got, "running") {
		t.Fatalf("context strip = %q, want the finished pass's counts gone with its results", got)
	}
	// The first schedule step of the new pass: its own roots, its own zero.
	next = asModel(t, must(next.Update(scheduleMsg{gen: next.generation})))
	if got, want := contextStrip(next), fmt.Sprintf("0/%d complete", total); !strings.Contains(got, want) {
		t.Fatalf("context strip = %q, want the new pass to start at %q", got, want)
	}
	if got := contextStrip(next); !strings.Contains(got, "1 running") {
		t.Errorf("context strip = %q, want the new pass's root counted as running", got)
	}
}

// Progress joins the strip the rest of the context is already on: it does not
// displace the target, the network, or the watch and incident state, and it
// does not add a row of its own.
func TestProgressComposesWithTheRestOfTheStrip(t *testing.T) {
	// onWireless answers the Wi-Fi probe as well as the interface one, so two
	// of this pass's probes are complete and the resolver is still in flight.
	m := onWireless(midRun(t))
	m.watch = true
	got := contextStrip(m)
	for _, want := range []string{"github.com:443", "Wi-Fi: homewifi", "watch", fmt.Sprintf("2/%d complete", len(m.probes)), "1 running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context strip = %q, want it to carry %q", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("context strip grew a second row: %q", got)
	}
	rendered, v := renderAt(t, m)
	if !hasLine(v, rendered.headerView()) {
		t.Errorf("the context strip is not on screen:\n%s", v)
	}
}

// The strip is the widest line the header has, so a run in progress is when it
// is most likely to overrun a narrow terminal and cost the view its top rows.
func TestProgressStripFitsNarrowTerminals(t *testing.T) {
	m := onWireless(midRun(t))
	m.watch = true
	for _, size := range [][2]int{{30, 8}, {40, 10}, {50, 24}, {80, 24}} {
		nm := asModel(t, must(m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})))
		v := nm.View()
		if lipgloss.Height(v) > nm.height {
			t.Errorf("%dx%d: view is %d rows tall:\n%s", size[0], size[1], lipgloss.Height(v), v)
		}
		if n := unclosedPanels(v); n != 0 {
			t.Errorf("%dx%d: %d panel border(s) left unclosed:\n%s", size[0], size[1], n, v)
		}
	}
}
