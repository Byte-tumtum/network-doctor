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
	if bar := ansi.Strip(m.helpView(false)); strings.Contains(bar, "e why") {
		t.Fatalf("footer advertises the explanation action: %q", bar)
	}

	m = pressed(t, m, keyPress("e"))
	if !m.explaining || m.selected != m.answerRow() {
		t.Fatalf("explanation state = %v, selected = %d, answer = %d", m.explaining, m.selected, m.answerRow())
	}
	view := ansi.Strip(strings.Join(m.detailRows(false), "\n"))
	for _, want := range []string{
		"Why: DNS example.com",
		// The system resolver failed where an independent one answered, which
		// is an observed comparison, but the branch also records that a general
		// DNS failure was only weakened and not excluded. That surviving
		// alternative is what keeps this off the strongest claim.
		"Confidence",
		"MEDIUM: the best explanation, with an ambiguity unresolved",
		"Evidence",
		"the configured resolver returned SERVFAIL",
		"DNS (public) returned an address",
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
	if bar := ansi.Strip(m.helpView(false)); strings.Contains(bar, "e details") {
		t.Errorf("open Why footer advertises the toggle: %q", bar)
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
		"why:\n  Confidence\n    MEDIUM: the best explanation, with an ambiguity unresolved\n  Evidence",
		"General network outage",
		"TCP example.com:443: a prerequisite failed",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report is missing %q:\n%s", want, rep)
		}
	}
}

// TestConfidenceLineCoversTheVocabulary pins the one presentation rule this
// panel adds: each level renders as a word plus what that word means here, and
// nothing else renders at all. A percentage or a meter would be inventing a
// precision the four categories do not have, and a level the app does not know
// is drawn as nothing rather than as a bare token.
func TestConfidenceLineCoversTheVocabulary(t *testing.T) {
	tests := []struct {
		in   diagnostic.Confidence
		want string
	}{
		{diagnostic.ConfidenceHigh, "HIGH: specific evidence, with no alternative left open"},
		{diagnostic.ConfidenceMedium, "MEDIUM: the best explanation, with an ambiguity unresolved"},
		{diagnostic.ConfidenceLow, "LOW: the failure is named, its cause barely narrowed"},
		{diagnostic.ConfidenceInsufficientEvidence, "INSUFFICIENT EVIDENCE: this run cannot say what caused it"},
		// A finding from an artifact written before confidence existed, and a
		// value from a build that knows more words than this one.
		{"", ""},
		{"certain", ""},
	}
	for _, test := range tests {
		if got := confidenceLine(test.in); got != test.want {
			t.Errorf("confidenceLine(%q) = %q, want %q", test.in, got, test.want)
		}
		if strings.ContainsAny(confidenceLine(test.in), "%") {
			t.Errorf("confidenceLine(%q) renders confidence as a quantity", test.in)
		}
	}
}

// The Why panel is the only place confidence appears. The probe table is a list
// of measurements, and a strength-of-evidence word on one of its rows would read
// as a claim about that measurement.
func TestConfidenceStaysOutOfTheChecksPanel(t *testing.T) {
	m := resolverExplanationModel(t)
	view := ansi.Strip(m.View())
	for _, word := range []string{"HIGH:", "MEDIUM:", "LOW:", "INSUFFICIENT EVIDENCE:"} {
		if strings.Contains(view, word) {
			t.Errorf("the ordinary view shows %q before e was pressed:\n%s", word, view)
		}
	}
}
