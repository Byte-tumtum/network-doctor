package ui

import (
	"net"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func resolverExplanationModel(t *testing.T) model {
	t.Helper()
	skip := diagnostic.ProbeResult{Status: diagnostic.StatusSkip, Detail: "not run: a check it depends on failed"}
	return answerScenario{
		target: "example.com:443",
		over: map[diagnostic.ProbeID]diagnostic.ProbeResult{
			diagnostic.ProbeDNS: {
				Status: diagnostic.StatusFail, Detail: "the configured resolver returned SERVFAIL",
				Fix: "check the configured resolver",
			},
			diagnostic.ProbeDNSPublic: {
				Status: diagnostic.StatusPass, Detail: "independent DNS resolved example.com",
				Addrs: []net.IP{net.ParseIP("192.0.2.1")},
			},
			diagnostic.ProbeTargetTCP: skip,
			diagnostic.ProbePMTU:      skip,
			diagnostic.ProbeTLS:       skip,
			diagnostic.ProbeHTTP:      skip,
			diagnostic.ProbeHTTPS:     skip,
		},
	}.build(t)
}

func TestWhyActionUsesTheExistingDetailsPanel(t *testing.T) {
	m := resolverExplanationModel(t)
	d := m.diagnosis()
	if len(d.Findings) != 1 || d.Findings[0].ID != diagnostic.DiagnosisSystemDNSFailure {
		t.Fatalf("diagnosis = %+v", d)
	}
	if strings.Contains(ansi.Strip(strings.Join(m.detailRows(false), "\n")), "Ruled out") {
		t.Fatal("details panel showed the causal explanation before e was pressed")
	}
	if bar := ansi.Strip(m.helpView(false)); !strings.Contains(bar, "e why") {
		t.Fatalf("help bar = %q, want the explanation action", bar)
	}

	m = pressed(t, m, keyPress("e"))
	if !m.explaining || m.selected != m.answerRow() {
		t.Fatalf("explanation state = %v, selected = %d, answer = %d", m.explaining, m.selected, m.answerRow())
	}
	view := ansi.Strip(strings.Join(m.detailRows(false), "\n"))
	for _, want := range []string{
		"Why: DNS example.com",
		"Evidence",
		"the configured resolver returned SERVFAIL",
		"DNS (public 8.8.8.8) returned an address",
		"Ruled out",
		"Missing DNS record",
		"General network outage",
		"Against alternatives",
		"General DNS failure",
		"Not evaluated",
		"TCP example.com:443: a prerequisite failed",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("Why view is missing %q:\n%s", want, view)
		}
	}
	if bar := ansi.Strip(m.helpView(false)); !strings.Contains(bar, "e details") {
		t.Errorf("open Why help bar = %q, want the toggle back", bar)
	}

	m = pressed(t, m, keyPress("e"))
	if m.explaining || !strings.Contains(ansi.Strip(strings.Join(m.detailRows(false), "\n")), "Details: DNS example.com") {
		t.Errorf("second e did not restore normal details: explaining=%v", m.explaining)
	}
}

func TestWhyActionIsUnavailableWithoutACompletedFinding(t *testing.T) {
	m := newModel(nil, false)
	if bar := ansi.Strip(m.helpView(false)); strings.Contains(bar, "e why") {
		t.Errorf("unfinished help bar advertises Why: %q", bar)
	}
	m = pressed(t, m, keyPress("e"))
	if m.explaining || !strings.Contains(m.notice, "no diagnosis explanation") {
		t.Errorf("unfinished e left explaining=%v notice=%q", m.explaining, m.notice)
	}
}

func TestCausalExplanationReachesTheTextReport(t *testing.T) {
	rep := resolverExplanationModel(t).report()
	for _, want := range []string{
		"why:\n  Evidence",
		"General network outage",
		"TCP example.com:443: a prerequisite failed",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report is missing %q:\n%s", want, rep)
		}
	}
}
