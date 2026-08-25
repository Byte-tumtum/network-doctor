// The structured next action in the TUI: the remediation the finished
// diagnosis reached, on the row it focuses, and in the pasted report.

package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// TestRemediationBlockFollowsTheDiagnosis: the Details panel's next action is
// the diagnosis's, on the row the diagnosis focuses, and nowhere else. A block
// repeated under every cursor position would read as advice about whichever
// row the reader happened to land on.
func TestRemediationBlockFollowsTheDiagnosis(t *testing.T) {
	m := pinnedRun(t, nil)
	rem, ok := m.remediation()
	if !ok || rem.ID != diagnostic.RemedyRestoreDefaultRoute {
		t.Fatalf("remediation = %q (ok=%v), want %q", rem.ID, ok, diagnostic.RemedyRestoreDefaultRoute)
	}
	if m.probes[m.answerRow()].ID != diagnostic.ProbeInternet {
		t.Fatalf("the diagnosis focuses %q, not the egress row", m.probes[m.answerRow()].ID)
	}

	block := ansi.Strip(strings.Join(m.detailRows(false), "\n"))
	for _, want := range []string{"Do: " + rem.Action, rem.Why, rem.Steps[0], "Run: " + rem.CommandLine(), rem.Expect, "Then press R to retest"} {
		if !strings.Contains(block, want) {
			t.Errorf("the focused row's details must carry %q:\n%s", want, block)
		}
	}

	// Move to a row the diagnosis is not about: it keeps its own detail and
	// fix, and gets none of the diagnosis's advice.
	other := slices.IndexFunc(m.probes, func(p diagnostic.Probe) bool { return p.ID == diagnostic.ProbeDNS })
	if other < 0 {
		t.Fatal("no DNS row in this run")
	}
	m.selected = other
	if got := ansi.Strip(strings.Join(m.detailRows(false), "\n")); strings.Contains(got, "Do: ") {
		t.Errorf("a row the diagnosis is not about carries its remediation:\n%s", got)
	}
}

// TestRemediationBlockIsAbsentWithoutADiagnosis: a healthy run has nothing to
// act on, and an unfinished one has not concluded anything yet. Advice in
// either place would be advice the probes did not support.
func TestRemediationBlockIsAbsentWithoutADiagnosis(t *testing.T) {
	healthy := newModel(nil, false)
	finishedRun(&healthy, nil)
	if _, ok := healthy.remediation(); ok {
		t.Error("a healthy run was given a remediation")
	}
	if healthy.remediationBlock() != "" {
		t.Error("a healthy run rendered a remediation block")
	}

	running := newModel(nil, false)
	running.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{Status: diagnostic.StatusFail}
	if _, ok := running.remediation(); ok {
		t.Error("an unfinished run was given a remediation")
	}
}

// TestRemediationReachesTheTextReport: the pasted report carries the same
// advice the screen showed, and labels the command as a suggestion rather than
// something netdoc ran.
func TestRemediationReachesTheTextReport(t *testing.T) {
	m := pinnedRun(t, nil)
	rem, _ := m.remediation()
	rep := m.report()
	for _, want := range []string{
		"next action: " + rem.Action,
		"  why: " + rem.Why,
		"  step: " + rem.Steps[0],
		"  run (suggested, not executed): " + rem.CommandLine(),
		"  expect: " + rem.Expect,
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report is missing %q:\n%s", want, rep)
		}
	}
	// Additive: the per-row fix lines the report has always carried stay.
	if !strings.Contains(rep, "        fix: fix") {
		t.Errorf("the report lost its per-check fix lines:\n%s", rep)
	}

	if healthy := newModel(nil, false); true {
		finishedRun(&healthy, nil)
		if strings.Contains(healthy.report(), "next action:") {
			t.Error("a healthy run's report offered a next action")
		}
	}
}
