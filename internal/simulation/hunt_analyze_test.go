package simulation

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func huntManifest(mutations ...GeneratedMutation) GeneratedCaseManifest {
	manifest := GeneratedCaseManifest{GeneratorVersion: HuntGeneratorVersion, BaseScenario: "healthy-routed-network",
		HuntSeed: 12345, Case: 17, CaseSeed: -99, Mutations: mutations}
	manifest.CaseFingerprint = huntCaseFingerprint(manifest)
	return manifest
}

func TestObservedTruthUsesServiceAndKernelEvidence(t *testing.T) {
	manifest := huntManifest(
		GeneratedMutation{ID: "dns.drop", Service: "r"},
		GeneratedMutation{ID: "netem.loss", Node: "gateway", Segment: "upstream", LossPercent: 20},
	)
	report := &Report{
		Faults:   []FaultInfo{{Type: FaultNetem, Node: "gateway", LossPercent: 20}},
		Timeline: []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledDNS, Service: "r", Outcome: DNSOutcomeDrop}, Result: EventApplied}},
		Evidence: Evidence{
			DNSQueries:       []DNSQueryEvidence{{Service: "r", ActualOutcome: "DROPPED"}, {Service: "r", ActualOutcome: "ANSWER"}},
			PacketConditions: []PacketConditionEvidence{{Node: "gateway", Segment: "upstream", Active: true, DroppedPackets: 7}},
			Routes:           []RouteEvidence{{Selected: true, GatewayReachable: boolPointer(true)}},
			Links:            []LinkEvidence{{Up: true}},
		},
	}
	truth := collectObservedTruth(manifest, report)
	if truth.DNS != "mixed" || truth.Packet != "drops_observed" || truth.Gateway != "reachable" || truth.Link != "up" {
		t.Fatalf("truth = %+v", truth)
	}
	if !reflect.DeepEqual(truth.ObservedFaults, []string{"dns.drop", "netem.loss"}) {
		t.Errorf("observed faults = %v", truth.ObservedFaults)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestObservedTruthProxyIgnoresGreetingResult(t *testing.T) {
	healthy := []SOCKSEvidence{
		{Event: "greeting", Result: "accepted"},
		{Event: "connect", Result: "connected"},
	}
	if truth := collectObservedTruth(huntManifest(), &Report{Evidence: Evidence{SOCKSRequests: healthy}}); truth.Proxy != "reached" {
		t.Fatalf("healthy proxy session = %q, want reached", truth.Proxy)
	}
	broken := append(healthy[:1:1], SOCKSEvidence{Event: "connect", Result: "dns_failure"})
	if truth := collectObservedTruth(huntManifest(), &Report{Evidence: Evidence{SOCKSRequests: broken}}); truth.Proxy != "failed" {
		t.Fatalf("failed proxy session = %q, want failed", truth.Proxy)
	}
}

func TestHuntAnalysisRequiresObservedFailureForFalseNegative(t *testing.T) {
	manifest := huntManifest(GeneratedMutation{ID: "netem.loss", Node: "gateway", Segment: "upstream", LossPercent: 20})
	diagnosis := &Diagnosis{Verdict: "ok", Checks: []DiagnosisCheck{{ID: "internet_tcp", Status: "PASS"}}}
	report := &Report{Cleanup: CleanupInfo{Done: true}, Faults: []FaultInfo{{Type: FaultNetem, Node: "gateway", LossPercent: 20}},
		Tests:    []TestOutcome{{Name: "t", Diagnosis: diagnosis, ProcessOutcome: ProcessExited}},
		Evidence: Evidence{PacketConditions: []PacketConditionEvidence{{Active: true, DroppedPackets: 7}}}, Suggestions: []Suggestion{}}
	truth := collectObservedTruth(manifest, report)
	if findings := analyzeHuntCase(manifest, report, truth); len(findings) != 0 {
		t.Fatalf("packet loss alone was overclaimed as a bug: %+v", findings)
	}
}

func TestHuntAnalysisFindsObservedDNSFalseNegative(t *testing.T) {
	manifest := huntManifest(GeneratedMutation{ID: "dns.drop", Service: "r"})
	report := &Report{Cleanup: CleanupInfo{Done: true},
		Timeline: []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledDNS, Service: "r", Outcome: DNSOutcomeDrop}, Result: EventApplied}},
		Tests: []TestOutcome{{Name: "t", StartOffset: 0, EndOffset: time.Second, ProcessOutcome: ProcessExited,
			Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS", Detail: "resolved"}}}}},
		Evidence:    Evidence{DNSQueries: []DNSQueryEvidence{{Service: "r", ActualOutcome: "DROPPED", Offset: 100 * time.Millisecond}}},
		Suggestions: []Suggestion{}}
	truth := collectObservedTruth(manifest, report)
	findings := analyzeHuntCase(manifest, report, truth)
	if len(findings) != 1 || findings[0].Category != FindingFalseNegative || findings[0].Severity != SeverityHigh {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestHuntSuggestionTaxonomyAndRanking(t *testing.T) {
	for code, want := range map[string]struct {
		category string
		severity HuntSeverity
	}{
		SuggestTransientNotResampled: {FindingCoverageGap, SeverityMedium},
		SuggestTimelineInconsistent:  {FindingDiagnosticContradiction, SeverityHigh},
		"jitter_sampling_gap":        {FindingCoverageGap, SeverityLow},
		"gateway_unreachable":        {FindingCoverageGap, SeverityInfo},
	} {
		category, severity, ok := huntSuggestionClass(code)
		if !ok || category != want.category || severity != want.severity {
			t.Errorf("%s = %s/%s/%t", code, category, severity, ok)
		}
	}
	if severityRank(SeverityCritical) <= severityRank(SeverityHigh) || severityRank(SeverityHigh) <= severityRank(SeverityMedium) ||
		severityRank(SeverityMedium) <= severityRank(SeverityLow) || severityRank(SeverityLow) <= severityRank(SeverityInfo) {
		t.Error("severity ranking is not strict")
	}
}

func TestHuntFindingFingerprintExcludesCaseSpecificText(t *testing.T) {
	a := HuntCaseFinding{Category: FindingCoverageGap, Code: SuggestTransientNotResampled, Probe: "dns", Cause: "dns_timeout",
		Summary: "one", Evidence: "case 3 at 500ms"}
	b := a
	b.Summary, b.Evidence = "two", "case 99 at 700ms"
	if huntFindingFingerprint(a) != huntFindingFingerprint(b) {
		t.Error("case-specific prose changed finding fingerprint")
	}
	b.Cause = "temporary_failure"
	if huntFindingFingerprint(a) == huntFindingFingerprint(b) {
		t.Error("semantic cause change did not alter finding fingerprint")
	}
}

func TestHuntAggregatesFindingsAndSuggestions(t *testing.T) {
	manifestA, manifestB := huntManifest(), huntManifest()
	manifestA.Case, manifestB.Case = 3, 8
	makeFinding := func(manifest GeneratedCaseManifest) HuntCaseFinding {
		finding := HuntCaseFinding{Category: FindingCoverageGap, Severity: SeverityMedium, Code: SuggestTransientNotResampled,
			SuggestionCode: SuggestTransientNotResampled, Probe: "dns", Summary: "resample temporary DNS", Reproduce: reproductionFor(manifest)}
		finding.Fingerprint = huntFindingFingerprint(finding)
		return finding
	}
	cases := []HuntCaseResult{{Manifest: manifestA, Findings: []HuntCaseFinding{makeFinding(manifestA)}},
		{Manifest: manifestB, Findings: []HuntCaseFinding{makeFinding(manifestB)}}}
	findings := aggregateHuntFindings(cases)
	if len(findings) != 1 || findings[0].Occurrences != 2 || !reflect.DeepEqual(findings[0].ExampleCases, []int{3, 8}) {
		t.Fatalf("findings = %+v", findings)
	}
	suggestions := aggregateHuntSuggestions(findings)
	if len(suggestions) != 1 || suggestions[0].Code != SuggestTransientNotResampled || suggestions[0].Evidence != 2 {
		t.Fatalf("suggestions = %+v", suggestions)
	}
}

func TestRunHuntDryRunReproducesDirectCaseAndSkipsDuplicates(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	batch := RunHunt(context.Background(), "healthy-routed-network", base, nil, HuntOptions{Cases: 50, Seed: 12345, MaxFaults: 2, DryRun: true})
	if batch.Result != HuntResultClean || batch.ExecutedCases != 0 || batch.GeneratedCases != 50 {
		t.Fatalf("batch = %+v", batch)
	}
	if batch.DuplicateCandidates == 0 {
		t.Fatal("test seed did not exercise duplicate-case suppression")
	}
	selected := batch.Cases[17]
	caseNumber := selected.Manifest.Case
	direct := RunHunt(context.Background(), "healthy-routed-network", base, nil,
		HuntOptions{Seed: 12345, Case: &caseNumber, MaxFaults: 2, DryRun: true})
	if len(direct.Cases) != 1 || !reflect.DeepEqual(direct.Cases[0].Manifest, selected.Manifest) {
		t.Fatalf("direct manifest differs:\n%+v\n%+v", direct.Cases, selected.Manifest)
	}
	var text bytes.Buffer
	direct.WriteText(&text)
	if !strings.Contains(text.String(), selected.Manifest.CaseFingerprint) || strings.Contains(text.String(), "tc qdisc") {
		t.Errorf("dry report = %s", text.String())
	}
}

func TestRunHuntFailFastStopsAfterFirstFinding(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	seed := int64(0)
	// Keep this lifecycle unit test fast by choosing a first candidate without a
	// delayed timeline. Cleanup failure itself supplies the finding.
	for ; seed < 10000; seed++ {
		generated, err := GenerateHuntCase("healthy-routed-network", base, seed, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(generated.Manifest.Mutations[0].ID, "timeline.") && generated.Manifest.Mutations[0].ID != "link.transient_down" {
			break
		}
	}
	result := RunHunt(context.Background(), "healthy-routed-network", base, func() Backend {
		return &fakeBackend{caps: supported(), env: &fakeEnv{stdout: okReport, cleanupErrors: []string{"left bridge"}}}
	}, HuntOptions{Cases: 10, Seed: seed, MaxFaults: 1, FailFast: true, Run: Options{Netdoc: "netdoc"}})
	if !result.FailFastStopped || result.ExecutedCases != 1 || len(result.Findings) == 0 || !result.RuntimeFailure {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunRecordsWholeProcessOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  *fakeEnv
		want string
	}{
		{"timeout", &fakeEnv{timedOut: true}, ProcessTimedOut},
		{"cancel", &fakeEnv{cancelled: true}, ProcessCancelled},
		{"signal", &fakeEnv{signal: "segmentation fault"}, ProcessSignaled},
		{"exec", &fakeEnv{execCtxErr: context.Canceled}, ProcessExecError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := Run(context.Background(), testScenario(t), &fakeBackend{caps: supported(), env: tc.env}, Options{Netdoc: "netdoc"})
			if got := rep.Tests[0].ProcessOutcome; got != tc.want {
				t.Errorf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
}
