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

// dropped is the trace a query leaves at a silenced resolver: it arrived, and
// the service chose not to answer. Its presence is what proves the client had a
// path to the resolver at all.
func dropped(offset time.Duration) DNSQueryEvidence {
	return DNSQueryEvidence{Service: "r", Offset: offset, ActualOutcome: "DROPPED", OffsetKnown: true}
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
	rep.Evidence.DNSQueries = []DNSQueryEvidence{dropped(150 * time.Millisecond)}
	if !codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("no resampling gap where the resolver recovered mid-run: %+v", rep.timelineSuggestions())
	}

	// A query that reached the recovered resolver inside the same run means
	// netdoc did resample; there is no gap to report.
	rep = timedReport(timeline, failing)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{dropped(150 * time.Millisecond),
		{Service: "r", Offset: 600 * time.Millisecond, ActualOutcome: "ANSWER", OffsetKnown: true}}
	if codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("resampling gap reported although a later query was answered: %+v", rep.timelineSuggestions())
	}

	// Recovery after the run ended is not something netdoc could have seen.
	late := failing
	late.EndOffset = 200 * time.Millisecond
	rep = timedReport(timeline, late)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{dropped(150 * time.Millisecond)}
	if codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("resampling gap reported for a recovery outside the run: %+v", rep.timelineSuggestions())
	}
}

// A resolver that netdoc's queries never reached is not evidence about
// resampling. When the path to it is blackholed, by a route fault the resolver
// schedule knows nothing about, the resolver records no query before the
// outage and none after it recovers, and that second silence is the path
// rather than a sample netdoc declined to take. Reading it as a coverage gap
// accuses netdoc of skipping a query it did send and that never arrived, and
// it also has nothing left to call transient: the failure outlives the run.
func TestTimelineSuggestionsIgnoreAResolverNoQueryReached(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(0, dnsEvent(DNSOutcomeAnswer)),
		applied(150*time.Millisecond, dnsEvent(DNSOutcomeDrop)),
		applied(770*time.Millisecond, dnsEvent(DNSOutcomeAnswer)),
	}
	blackholed := TestOutcome{Name: "t", EndOffset: 4 * time.Second,
		Diagnosis: diag("Offline: neither direct egress nor DNS is working.",
			DiagnosisCheck{ID: "dns", Status: "FAIL", Cause: "dns_timeout"})}

	rep := timedReport(timeline, blackholed)
	got := codes(rep.timelineSuggestions())
	if got[SuggestTransientNotResampled] || got[SuggestTransientReportedPermanent] {
		t.Errorf("transient reported for a resolver no query reached: %+v", rep.timelineSuggestions())
	}

	// The only difference that matters: one query got through before the
	// outage healed. Now the silence afterwards is netdoc's to answer for.
	rep = timedReport(timeline, blackholed)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{dropped(200 * time.Millisecond)}
	if !codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("no resampling gap although a query reached the resolver: %+v", rep.timelineSuggestions())
	}

	// Timing the director could not place is not proof of a reachable path.
	rep = timedReport(timeline, blackholed)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{{Service: "r", ActualOutcome: "DROPPED"}}
	got = codes(rep.timelineSuggestions())
	if got[SuggestTransientNotResampled] || got[SuggestTransientReportedPermanent] {
		t.Errorf("transient reported from a query with no known offset: %+v", rep.timelineSuggestions())
	}

	// Another resolver's traffic proves nothing about the path to this one.
	// The premise is checked before the later-query loop, so a scenario with a
	// second resolver cannot borrow its way into either finding.
	rep = timedReport(timeline, blackholed)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{{Service: "other", Offset: 200 * time.Millisecond,
		ActualOutcome: "DROPPED", OffsetKnown: true}}
	got = codes(rep.timelineSuggestions())
	if got[SuggestTransientNotResampled] || got[SuggestTransientReportedPermanent] {
		t.Errorf("transient reported from another resolver's queries: %+v", rep.timelineSuggestions())
	}

	// A query that only arrives after the recovery is not a gap either: it is
	// netdoc resampling, and it still says nothing about a path that was
	// carrying nothing while the resolver was silent.
	rep = timedReport(timeline, blackholed)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{{Service: "r", Offset: 900 * time.Millisecond,
		ActualOutcome: "ANSWER", OffsetKnown: true}}
	if codes(rep.timelineSuggestions())[SuggestTransientNotResampled] {
		t.Errorf("resampling gap reported although the only query landed after recovery: %+v", rep.timelineSuggestions())
	}
}

func TestPermanentWordingRespectsAPointInTimeClaim(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(0, TimedEvent{Type: FaultScheduledLink, Node: "target", Segment: "lan", State: LinkStateDown}),
		applied(400*time.Millisecond, TimedEvent{Type: FaultScheduledLink, Node: "target", Segment: "lan", State: LinkStateUp}),
	}
	blunt := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: &Diagnosis{Summary: "the target is unreachable: remote port closed or firewalled",
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

// A resolver outage that healed mid-run explains a DNS failure, not whatever
// unrelated check happens to fail first in the same diagnosis.
func TestPermanentWordingPairsTheRecoveryWithTheFailureItExplains(t *testing.T) {
	timeline := []FaultEventEvidence{
		applied(100*time.Millisecond, dnsEvent(DNSOutcomeDrop)),
		applied(500*time.Millisecond, dnsEvent(DNSOutcomeAnswer)),
	}
	unrelated := TestOutcome{Name: "t", EndOffset: time.Second,
		Diagnosis: diag("no HTTP response from the target: application-layer or proxy block",
			DiagnosisCheck{ID: "dns", Status: "PASS"},
			DiagnosisCheck{ID: "http", Status: "FAIL", Detail: "connection reset"})}
	if codes(timedReport(timeline, unrelated).timelineSuggestions())[SuggestTransientReportedPermanent] {
		t.Errorf("a healed DNS outage was pinned to an unrelated HTTP failure: %+v",
			timedReport(timeline, unrelated).timelineSuggestions())
	}

	dnsFailed := unrelated
	dnsFailed.Diagnosis = diag("DNS is not resolving", DiagnosisCheck{ID: "dns", Status: "FAIL"})
	// The resample happened, so only the wording is at issue here.
	rep := timedReport(timeline, dnsFailed)
	rep.Evidence.DNSQueries = []DNSQueryEvidence{{Service: "r", Offset: 600 * time.Millisecond, ActualOutcome: "ANSWER", OffsetKnown: true}}
	if !codes(rep.timelineSuggestions())[SuggestTransientReportedPermanent] {
		t.Error("a healed DNS outage was not paired with the DNS failure it explains")
	}
}

// A dropped query followed by an answered one is a resolver that recovered and
// a netdoc that resampled it, so PASS is the truth, not a missed failure.
func TestDNSFailureDuringJudgesTheLastQueryPerService(t *testing.T) {
	test := TestOutcome{EndOffset: time.Second}
	recovered := []DNSQueryEvidence{
		{Service: "r", Offset: 200 * time.Millisecond, ActualOutcome: "DROPPED", OffsetKnown: true},
		{Service: "r", Offset: 700 * time.Millisecond, ActualOutcome: "ANSWER", OffsetKnown: true},
	}
	if dnsFailureDuring(test, recovered) {
		t.Error("a resolver that answered the resample was reported as still failing")
	}
	if !dnsFailureDuring(test, append(recovered,
		DNSQueryEvidence{Service: "public", Offset: 800 * time.Millisecond, ActualOutcome: "SERVFAIL", OffsetKnown: true})) {
		t.Error("a second resolver still failing at the end of the run was not reported")
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

// The window predicate has to answer three different questions apart, and the
// one that matters most is the one production evidence collection cannot reach
// today: a query the director could not place must not be read as a query
// observed at T0. Offset carries zero in both cases, so only OffsetKnown keeps
// "not placed" from arguing that a resolver failed inside a window it was
// never located in.
func TestDNSFailureDuringSeparatesUnknownTimingFromOffsetZero(t *testing.T) {
	window := TestOutcome{StartOffset: 100 * time.Millisecond, EndOffset: 500 * time.Millisecond}
	fromZero := TestOutcome{StartOffset: 0, EndOffset: 500 * time.Millisecond}

	for _, tc := range []struct {
		name  string
		test  TestOutcome
		query DNSQueryEvidence
		want  bool
	}{
		{"known failure inside the window", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 300 * time.Millisecond, OffsetKnown: true}, true},
		{"known failure before the window", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 50 * time.Millisecond, OffsetKnown: true}, false},
		{"known failure after the window", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 900 * time.Millisecond, OffsetKnown: true}, false},
		{"start offset is inclusive", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "SERVFAIL", Offset: 100 * time.Millisecond, OffsetKnown: true}, true},
		{"end offset is exclusive", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "SERVFAIL", Offset: 500 * time.Millisecond, OffsetKnown: true}, false},
		// The pair this whole distinction exists for: identical Offset, opposite
		// answers, decided only by whether the observation was ever placed.
		{"a failure known to be at T0 is inside a window starting at T0", fromZero,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 0, OffsetKnown: true}, true},
		{"an unplaced failure does not match a window containing zero", fromZero,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 0}, false},
		{"an unplaced failure does not match a window starting later", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 0}, false},
		{"a served answer inside the window is not a failure", window,
			DNSQueryEvidence{Service: "r", ActualOutcome: "ANSWER", Offset: 300 * time.Millisecond, OffsetKnown: true}, false},
	} {
		if got := dnsFailureDuring(tc.test, []DNSQueryEvidence{tc.query}); got != tc.want {
			t.Errorf("%s: dnsFailureDuring = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An unplaced query cannot clear a resolver either: the last-query-per-service
// rule needs to know which query was last, and an observation with no offset
// never earns that position.
func TestDNSFailureDuringIgnoresUnplacedQueriesForTheLastQueryRule(t *testing.T) {
	test := TestOutcome{EndOffset: time.Second}
	failing := DNSQueryEvidence{Service: "r", ActualOutcome: "DROPPED", Offset: 200 * time.Millisecond, OffsetKnown: true}

	if !dnsFailureDuring(test, []DNSQueryEvidence{failing, {Service: "r", ActualOutcome: "ANSWER"}}) {
		t.Error("an unplaced answer was allowed to exonerate a resolver that failed inside the run")
	}
	if !dnsFailureDuring(test, []DNSQueryEvidence{{Service: "r", ActualOutcome: "ANSWER", OffsetKnown: true, Offset: 100 * time.Millisecond}, failing}) {
		t.Error("a placed answer before the placed failure was read as a recovery")
	}
}
