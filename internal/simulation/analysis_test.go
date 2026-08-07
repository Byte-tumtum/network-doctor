package simulation

import (
	"testing"
	"time"
)

func applied(offset time.Duration, e TimedEvent) FaultEventEvidence {
	return FaultEventEvidence{Event: e, ScheduledOffset: offset, AppliedOffset: offset,
		State: e.summary(), Result: EventApplied}
}

func dnsEvent(outcome string) TimedEvent {
	return TimedEvent{Type: FaultScheduledDNS, Service: "r", Outcome: outcome}
}

func timedReport(timeline []FaultEventEvidence, tests ...TestOutcome) *Report {
	return &Report{Timeline: timeline, Tests: tests}
}

func codes(in []Suggestion) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s.Code] = true
	}
	return out
}

func TestResamplingGapNeedsRecoveryInsideTheRunAndNoLaterQuery(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(0, dnsEvent(DNSOutcomeAnswer)),
		applied(100*time.Millisecond, dnsEvent(DNSOutcomeDrop)),
		applied(500*time.Millisecond, dnsEvent(DNSOutcomeAnswer)),
	}
	failing := TestOutcome{Name: "t", StartOffset: 50 * time.Millisecond, EndOffset: 2 * time.Second,
		Diagnosis: diag("dns", DiagnosisCheck{ID: "dns", Status: "FAIL", Cause: "dns_timeout"})}

	rep := timedReport(timeline, failing)
	if !codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("no resampling gap where the resolver recovered mid-run: %+v", rep.timelineSuggestions())
	}

	// A query that reached the recovered resolver inside the same run means
	// netdoc did resample; there is no gap to report.
	rep = timedReport(timeline, failing)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{{Service: "r", Offset: 600 * time.Millisecond, ActualOutcome: "ANSWER"}}
	if codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("resampling gap reported although a later query was answered: %+v", rep.timelineSuggestions())
	}

	// Recovery after the run ended is not something netdoc could have seen.
	late := failing
	late.EndOffset = 200 * time.Millisecond
	rep = timedReport(timeline, late)
	if codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("resampling gap reported for a recovery outside the run: %+v", rep.timelineSuggestions())
	}
}

func TestPermanentWordingRespectsAPointInTimeClaim(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(0, TimedEvent{Type: FaultScheduledLink, Node: "target", Segment: "lan", State: LinkStateDown}),
		applied(400*time.Millisecond, TimedEvent{Type: FaultScheduledLink, Node: "target", Segment: "lan", State: LinkStateUp}),
	}
	blunt := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: &Diagnosis{Summary: "the target is unreachable — remote port closed or firewalled",
			Checks: []DiagnosisCheck{{ID: "target_tcp", Status: "FAIL", Detail: "port 80 unreachable"}}}}
	if !codes(timedReport(timeline, blunt).timelineSuggestions())[SuggestTransientReportedPermanent] {
		t.Error("a healed failure described as a lasting state was not reported")
	}

	// A diagnosis that says it is describing a moment is not wrong merely
	// because the network recovered afterwards.
	careful := blunt
	careful.Diagnosis = &Diagnosis{Summary: "the target was unreachable at the time of this check",
		Checks: []DiagnosisCheck{{ID: "target_tcp", Status: "FAIL", Detail: "port 80 unreachable"}}}
	if codes(timedReport(timeline, careful).timelineSuggestions())[SuggestTransientReportedPermanent] {
		t.Error("a point-in-time claim was reported as overstating a transient failure")
	}
}

func TestMissedWindowFiresOnlyWhenNothingWasFlagged(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(0, TimedEvent{Type: FaultScheduledNetem, Node: "client", Segment: "lan", Latency: 10 * time.Millisecond}),
		applied(100*time.Millisecond, TimedEvent{Type: FaultScheduledNetem, Node: "client", Segment: "lan", LossPercent: 100, Latency: 10 * time.Millisecond}),
		applied(300*time.Millisecond, TimedEvent{Type: FaultScheduledNetem, Node: "client", Segment: "lan", Latency: 10 * time.Millisecond}),
	}
	quiet := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: diag("ok", DiagnosisCheck{ID: "dns", Status: "PASS"})}
	if !codes(timedReport(timeline, quiet).timelineSuggestions())[SuggestTransientMissed] {
		t.Error("an outage that opened and closed inside the run was not reported as missed")
	}
	noticed := quiet
	noticed.Diagnosis = diag("degraded", DiagnosisCheck{ID: "dns", Status: "WARN"})
	if codes(timedReport(timeline, noticed).timelineSuggestions())[SuggestTransientMissed] {
		t.Error("an outage netdoc did flag was reported as missed")
	}
}

func TestDependencyContradictionIsSuppressedByATransition(t *testing.T) {
	contradiction := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: diag("ok",
			DiagnosisCheck{ID: "dns", Status: "FAIL", Detail: "cannot resolve"},
			DiagnosisCheck{ID: "http", Status: "PASS", Detail: "HTTP 200"})}

	// No timed faults at all: nothing to explain the impossible pair, and the
	// analysis has no timeline to reason from either, so it says nothing.
	if got := timedReport(nil, contradiction).timelineSuggestions(); len(got) != 0 {
		t.Errorf("suggestions without a timeline: %+v", got)
	}

	// A timeline whose transitions all landed outside this run: the network did
	// not change while it ran, so the pair is a real contradiction.
	outside := []FaultEventEvidence{applied(5*time.Second, dnsEvent(DNSOutcomeDrop))}
	if !codes(timedReport(outside, contradiction).timelineSuggestions())[SuggestTimelineInconsistent] {
		t.Error("a dependent PASS over a failed dependency was not reported")
	}

	// The same pair with a transition inside the run is temporally explainable.
	inside := []FaultEventEvidence{applied(500*time.Millisecond, dnsEvent(DNSOutcomeDrop))}
	if codes(timedReport(inside, contradiction).timelineSuggestions())[SuggestTimelineInconsistent] {
		t.Error("a pair explained by a transition during the run was called inconsistent")
	}
}

func TestTimelineSuggestionsAreStableAcrossRuns(t *testing.T) {
	timeline := []FaultEventEvidence{applied(5*time.Second, dnsEvent(DNSOutcomeDrop))}
	out := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: diag("ok",
			DiagnosisCheck{ID: "dns", Status: "FAIL"},
			DiagnosisCheck{ID: "http", Status: "PASS"},
			DiagnosisCheck{ID: "target_tcp", Status: "PASS"})}
	first := timedReport(timeline, out).timelineSuggestions()
	for i := 0; i < 20; i++ {
		next := timedReport(timeline, out).timelineSuggestions()
		if len(next) != len(first) {
			t.Fatalf("suggestion count changed: %d != %d", len(next), len(first))
		}
		for j := range next {
			if next[j].Probe != first[j].Probe || next[j].Code != first[j].Code {
				t.Fatalf("suggestion order changed at %d: %+v != %+v", j, next[j], first[j])
			}
		}
	}
}
