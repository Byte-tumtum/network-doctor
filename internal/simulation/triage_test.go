package simulation

import (
	"bytes"
	"strings"
	"testing"
)

func sampleFinding() HuntFinding {
	return HuntFinding{
		Fingerprint: "aabbccdd11223344", Category: FindingFalseNegative, Severity: SeverityHigh,
		Code: "observed_dns_failure_reported_healthy", Probe: "dns", Expected: "FAIL or WARN", Actual: "PASS",
		Summary:  "The resolver service observed failed queries while netdoc reported DNS healthy.",
		Evidence: "System DNS answered", Occurrences: 2, FirstCase: 4, ExampleCases: []int{4, 9},
		Reproduce: HuntReproduction{BaseScenario: "healthy", Seed: 20260101, Case: 4, CaseSeed: -8811,
			MaxFaults: 2, GeneratorVersion: HuntGeneratorVersion, CaseFingerprint: "0f0f0f0f0f0f0f0f"},
	}
}

// The seeds are a published contract: a finding filed last night has to
// reproduce tonight from the same scenario and seed.
func TestTriageBaselineSeedsAreFixed(t *testing.T) {
	want := map[string]int64{"healthy": 20260101, "healthy-routed-network": 20260102, "dual-stack-healthy": 20260103,
		"tls-valid": 20260104, "socks5h-remote-dns-succeeds": 20260105}
	baselines := TriageBaselines()
	if len(baselines) != len(want) {
		t.Fatalf("baselines = %+v", baselines)
	}
	seen := make(map[string]int, len(want))
	for _, baseline := range baselines {
		seen[baseline.Scenario]++
		if want[baseline.Scenario] != baseline.Seed {
			t.Errorf("%s seed = %d, want %d", baseline.Scenario, baseline.Seed, want[baseline.Scenario])
		}
		if _, err := LibraryScenario(baseline.Scenario); err != nil {
			t.Errorf("%s does not load: %v", baseline.Scenario, err)
		}
		if !validHuntBase(baseline.Scenario) {
			t.Errorf("%s is not a hunt base", baseline.Scenario)
		}
		got, ok := TriageBaselineFor(baseline.Scenario)
		if !ok || got != baseline {
			t.Errorf("TriageBaselineFor(%s) = %+v, %t", baseline.Scenario, got, ok)
		}
	}
	for scenario, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times, want exactly once", scenario, count)
		}
	}
	if _, ok := TriageBaselineFor("broken-dns"); ok {
		t.Error("broken-dns is not a triage baseline")
	}
}

// The fingerprint is the issue's identity. It must survive a rerun unchanged
// and must separate every field a reader would use to tell two bugs apart.
func TestTriageFingerprintIsStableAndDistinct(t *testing.T) {
	base := NewTriageFinding(sampleFinding())
	if again := NewTriageFinding(sampleFinding()); again.Fingerprint != base.Fingerprint {
		t.Fatalf("fingerprint = %s and %s for the same finding", base.Fingerprint, again.Fingerprint)
	}
	seen := map[string]string{base.Fingerprint: "base"}
	for name, mutate := range map[string]func(*HuntFinding){
		"scenario": func(f *HuntFinding) { f.Reproduce.BaseScenario = "dual-stack-healthy" },
		"seed":     func(f *HuntFinding) { f.Reproduce.Seed = 20260102 },
		"case":     func(f *HuntFinding) { f.Reproduce.Case = 5 },
		"finding":  func(f *HuntFinding) { f.Fingerprint = "ffffffffffffffff" },
	} {
		finding := sampleFinding()
		mutate(&finding)
		got := NewTriageFinding(finding).Fingerprint
		if other, clash := seen[got]; clash {
			t.Errorf("changing %s kept the fingerprint of %s", name, other)
		}
		seen[got] = name
	}
	// Cosmetic prose is not identity: the same bug reworded stays one issue.
	reworded := sampleFinding()
	reworded.Summary, reworded.Evidence, reworded.Occurrences = "different words", "other evidence", 7
	if NewTriageFinding(reworded).Fingerprint != base.Fingerprint {
		t.Error("rewording the summary changed the fingerprint")
	}
}

func TestTriageFindingCarriesScenarioSeedAndCase(t *testing.T) {
	finding := NewTriageFinding(sampleFinding())
	if finding.Scenario != "healthy" || finding.Seed != 20260101 || finding.Case != 4 ||
		finding.CaseSeed != -8811 || finding.CaseFingerprint != "0f0f0f0f0f0f0f0f" ||
		finding.GeneratorVersion != HuntGeneratorVersion || finding.Issue.Status != IssueStatusNotFiled {
		t.Fatalf("finding = %+v", finding)
	}
	if want := "netdoc-sim hunt healthy --seed 20260101 --case 4 --max-faults 2 --json"; finding.ReproduceCommand() != want {
		t.Errorf("reproduce = %q, want %q", finding.ReproduceCommand(), want)
	}
	if title := finding.IssueTitle(); !strings.Contains(title, finding.Fingerprint) ||
		!strings.Contains(title, "observed_dns_failure_reported_healthy") || !strings.Contains(title, "healthy") {
		t.Errorf("title = %q", title)
	}
}

func TestTriageIssueBodyHasEverythingNeededToDebug(t *testing.T) {
	finding := NewTriageFinding(sampleFinding())
	finding.Reproducible = true
	finding.Truth = ObservedTruth{DNS: "unavailable", Link: "up", ObservedFaults: []string{"dns.drop"}}
	finding.Diagnosis = DiagnosisFingerprint{ID: "d00d", Verdicts: []string{"healthy"},
		Probes: []ProbeFingerprint{{Test: "resolve", ID: "dns", Status: "PASS"}}}
	finding.Mutations = []GeneratedMutation{{ID: "dns.drop", Description: "resolver drops responses",
		Service: "resolver", Node: "dns", DurationMS: 500, StartMS: 100}}
	body := finding.IssueBody("abc123", "workflow run 42")

	for _, want := range []string{
		"`healthy`",                    // scenario
		"20260101",                     // seed
		"| case | `4` |",               // case number
		"-8811",                        // case seed
		"dns      unavailable",         // simulator truth
		"dns.drop",                     // observed fault and mutation
		"resolver drops responses",     // mutation description
		"start=100ms duration=500ms",   // mutation parameters
		"fingerprint d00d",             // netdoc diagnosis
		"probe       resolve dns PASS", // netdoc probe result
		"simulator expected: `FAIL or WARN`",
		"netdoc reported: `PASS`",
		"resolver service observed failed queries", // why they disagree
		"netdoc-sim hunt healthy --seed 20260101 --case 4 --max-faults 2 --json", // reproduction
		"aabbccdd11223344", // hunt finding fingerprint
		"abc123",           // revision
		"workflow run 42",  // debugging context
		finding.Fingerprint,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q\n%s", want, body)
		}
	}
}

// Untrusted text reaches the issue body through netdoc output and scenario
// names; it must arrive sanitized like every other rendered report.
func TestTriageIssueBodySanitizesText(t *testing.T) {
	raw := sampleFinding()
	raw.Summary = "escape \x1b[2Jhere"
	raw.Reproduce.BaseScenario = "healthy\x1b[31m"
	finding := NewTriageFinding(raw)
	body := finding.IssueBody("\x1brev", "")
	if strings.Contains(body, "\x1b") {
		t.Errorf("body kept a control sequence: %q", body)
	}
	if strings.Contains(finding.IssueTitle(), "\x1b") {
		t.Errorf("title kept a control sequence: %q", finding.IssueTitle())
	}
}

func TestTriageReportTextSummary(t *testing.T) {
	filed := NewTriageFinding(sampleFinding())
	filed.Reproducible = true
	filed.Issue = TriageIssue{Status: IssueStatusCreated, URL: "https://example.test/1"}
	flaky := sampleFinding()
	flaky.Reproduce.Case = 11
	report := &TriageReport{Revision: "abc123", Context: "run 42",
		Baselines: []TriageScenarioResult{{Scenario: "healthy", Seed: 20260101, Cases: 20,
			Candidates: 2, HuntResult: HuntResultFindings}},
		Findings: []TriageFinding{filed, NewTriageFinding(flaky)}, Result: TriageResultFindings}
	var out bytes.Buffer
	report.WriteText(&out)
	for _, want := range []string{"healthy", "20260101", "cases 20", "candidates 2",
		"Candidate findings:    2", "Reproducible findings: 1", "New issues created:    1",
		"https://example.test/1", "not reproducible, not filed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary is missing %q\n%s", want, out.String())
		}
	}
	if err := report.WriteJSON(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

// One case has one reproduction command. The hunt's own report and a filed
// issue both print it, and a reader who pastes either has to land on the same
// experiment, so both go through HuntReproduction.Command rather than each
// formatting their own.
func TestHuntReportAndIssueAgreeOnTheReproductionCommand(t *testing.T) {
	finding := sampleFinding()
	result := &HuntResult{Findings: []HuntFinding{finding}}
	var text bytes.Buffer
	result.WriteText(&text)

	issue := NewTriageFinding(finding)
	want := issue.ReproduceCommand()
	if !strings.Contains(want, "--max-faults 2") {
		t.Fatalf("issue command = %q, want the fault ceiling named", want)
	}
	if !strings.Contains(text.String(), want) {
		t.Errorf("hunt report prints a different reproduction command:\nwant %q\nin\n%s", want, text.String())
	}
}
