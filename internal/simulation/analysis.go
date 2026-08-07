package simulation

import (
	"fmt"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// Timeline-aware suggestion codes. Stable identifiers, like the rest.
const (
	// SuggestTransientNotResampled: the resolver recovered while the run was
	// still going and netdoc never asked it again.
	SuggestTransientNotResampled = "transient_fault_not_resampled"
	// SuggestTransientReportedPermanent: a failure that had already healed
	// before the run ended is described without any hint that it was temporary.
	SuggestTransientReportedPermanent = "transient_fault_reported_permanent"
	// SuggestTransientMissed: an impairment opened and closed entirely inside
	// one run and nothing was flagged.
	SuggestTransientMissed = "transient_fault_missed"
	// SuggestTimelineInconsistent: a probe succeeded while a probe it depends on
	// failed, and no fault transition happened during that run to explain it.
	SuggestTimelineInconsistent = "timeline_inconsistent"
)

// probeDeps are the diagnostic DAG edges that hold in every graph netdoc
// builds, whatever the target's scheme. They reference the ProbeID constants so
// a rename breaks the build rather than silently disabling this check.
//
// Only edges that are unconditional belong here. ProbeHTTP, for instance,
// depends on ProbeTargetTCP for a plain http:// target but on ProbeDNS for an
// https:// one, so only the DNS edge — which holds transitively either way — is
// listed.
var probeDeps = map[diagnostic.ProbeID][]diagnostic.ProbeID{
	diagnostic.ProbeTargetTCP: {diagnostic.ProbeDNS},
	diagnostic.ProbeHTTP:      {diagnostic.ProbeDNS},
	diagnostic.ProbeTLS:       {diagnostic.ProbeTargetTCP},
	diagnostic.ProbeHTTPS:     {diagnostic.ProbeTLS},
	diagnostic.ProbePMTU:      {diagnostic.ProbeTargetTCP},
	diagnostic.ProbeSSH:       {diagnostic.ProbeTargetTCP},
	diagnostic.ProbeSMTP:      {diagnostic.ProbeTargetTCP},
}

// temporalWords are the ways a diagnosis can admit that what it saw may not
// still be true. A failure that healed mid-run and says none of these is
// describing a moment as if it were a state.
var temporalWords = []string{"transient", "intermittent", "temporar", "flapp", "retry", "retried", "again", "at the time", "moment"}

// healthy reports whether an event leaves its target unimpaired. baseline is
// the least-impaired netem latency this fault's own timeline asks for, so the
// judgement is relative to the scenario rather than to a magic threshold.
func (e TimedEvent) healthy(baseline time.Duration) bool {
	switch e.Type {
	case FaultScheduledDNS:
		return e.Outcome == DNSOutcomeAnswer
	case FaultScheduledNetem:
		return e.LossPercent == 0 && e.Latency <= baseline
	case FaultScheduledLink:
		return e.State == LinkStateUp
	}
	return false
}

// netemBaselines is the smallest latency each netem target is ever asked for.
func netemBaselines(timeline []FaultEventEvidence) map[string]time.Duration {
	out := make(map[string]time.Duration)
	for _, item := range timeline {
		if item.Event.Type != FaultScheduledNetem {
			continue
		}
		key := item.Event.Node + "\x00" + item.Event.Segment
		if current, seen := out[key]; !seen || item.Event.Latency < current {
			out[key] = item.Event.Latency
		}
	}
	return out
}

// recoveries are the applied events that put a target back into a healthy
// state after it was impaired, in the order they were applied.
func (r *Report) recoveries() []FaultEventEvidence {
	baselines := netemBaselines(r.Timeline)
	impaired := make(map[string]bool)
	var out []FaultEventEvidence
	for _, item := range r.Timeline {
		if item.Result != EventApplied {
			continue
		}
		key := item.Event.Type + "\x00" + item.Event.Node + "\x00" + item.Event.Segment + "\x00" + item.Event.Service
		if item.Event.healthy(baselines[item.Event.Node+"\x00"+item.Event.Segment]) {
			if impaired[key] {
				out = append(out, item)
			}
			impaired[key] = false
			continue
		}
		impaired[key] = true
	}
	return out
}

// timelineSuggestions reads the diagnosis against what the network actually did
// while it was being diagnosed. Every rule is a plain function of the recorded
// timeline and the recorded diagnosis; none fires without offsets to point at.
func (r *Report) timelineSuggestions() []Suggestion {
	if len(r.Timeline) == 0 {
		return nil
	}
	var out []Suggestion
	recoveries := r.recoveries()
	for i := range r.Tests {
		test := &r.Tests[i]
		if test.Diagnosis == nil {
			continue
		}
		resampled := false
		for _, recovery := range recoveries {
			if recovery.AppliedOffset < test.StartOffset || recovery.AppliedOffset >= test.EndOffset {
				continue
			}
			if s, ok := r.resamplingGap(test, recovery); ok {
				out, resampled = append(out, s), true
			}
		}
		if !resampled {
			out = append(out, r.permanentWording(test, recoveries)...)
		}
		out = append(out, r.missedWindow(test)...)
		out = append(out, r.dependencyContradictions(test)...)
	}
	return out
}

// resamplingGap fires when the resolver came back while netdoc was still
// running and netdoc concluded without asking it again. It is deliberately
// limited to DNS: the per-query evidence is what proves no second sample was
// taken, and no other probe leaves that trace.
func (r *Report) resamplingGap(test *TestOutcome, recovery FaultEventEvidence) (Suggestion, bool) {
	if recovery.Event.Type != FaultScheduledDNS {
		return Suggestion{}, false
	}
	dns := checkByID(test.Diagnosis, string(diagnostic.ProbeDNS))
	if dns == nil || dns.Status != "FAIL" {
		return Suggestion{}, false
	}
	for _, q := range r.Evidence.DNSQueries {
		if q.Service == recovery.Event.Service && q.Offset >= recovery.AppliedOffset &&
			q.Offset < test.EndOffset && q.ActualOutcome != "DROPPED" {
			return Suggestion{}, false
		}
	}
	return Suggestion{Code: SuggestTransientNotResampled, Test: test.Name, Probe: string(diagnostic.ProbeDNS),
		Cause: dns.Cause,
		Message: "DNS failed during the run and recovered before it ended, but netdoc did not resample the resolver. " +
			"Consider a bounded retry before concluding that DNS is persistently unavailable.",
		Evidence: fmt.Sprintf("%s ran +%s..+%s; %s recovered at +%s; no further query reached it",
			test.Name, offsetLabel(test.StartOffset)[1:], offsetLabel(test.EndOffset)[1:],
			recovery.Event.Service, offsetLabel(recovery.AppliedOffset)[1:])}, true
}

// permanentWording fires when a failure had already healed before the run
// ended and the report describes it with no hint that what it saw was a moment
// rather than a state. The wording is inspected on purpose: a diagnosis that
// says "at the time of this check" is making a point-in-time claim and is not
// wrong just because the network later recovered.
func (r *Report) permanentWording(test *TestOutcome, recoveries []FaultEventEvidence) []Suggestion {
	var healed []FaultEventEvidence
	for _, recovery := range recoveries {
		if recovery.AppliedOffset >= test.StartOffset && recovery.AppliedOffset < test.EndOffset {
			healed = append(healed, recovery)
		}
	}
	if len(healed) == 0 {
		return nil
	}
	failed := ""
	for _, check := range test.Diagnosis.Checks {
		if check.Status == "FAIL" {
			failed = check.ID
			break
		}
	}
	if failed == "" {
		return nil
	}
	prose := strings.ToLower(test.Diagnosis.Summary)
	for _, check := range test.Diagnosis.Checks {
		prose += " " + strings.ToLower(check.Detail) + " " + strings.ToLower(check.Fix)
	}
	for _, word := range temporalWords {
		if strings.Contains(prose, word) {
			return nil
		}
	}
	return []Suggestion{{Code: SuggestTransientReportedPermanent, Test: test.Name, Probe: failed,
		Message: "The simulator restored this path during the same diagnostic run, but the diagnosis describes the failure " +
			"without indicating that what it observed was a moment rather than a lasting state.",
		Evidence: fmt.Sprintf("%s recovered at +%s, inside the run +%s..+%s that reported %s as FAIL; summary: %s",
			healed[0].State, offsetLabel(healed[0].AppliedOffset)[1:],
			offsetLabel(test.StartOffset)[1:], offsetLabel(test.EndOffset)[1:], failed, test.Diagnosis.Summary)}}
}

// missedWindow fires when an impairment opened and closed entirely inside one
// run and that run found nothing at all to say.
func (r *Report) missedWindow(test *TestOutcome) []Suggestion {
	baselines := netemBaselines(r.Timeline)
	var opened *FaultEventEvidence
	for i := range r.Timeline {
		item := r.Timeline[i]
		if item.Result != EventApplied || item.AppliedOffset < test.StartOffset || item.AppliedOffset >= test.EndOffset {
			continue
		}
		if !item.Event.healthy(baselines[item.Event.Node+"\x00"+item.Event.Segment]) {
			if opened == nil {
				opened = &r.Timeline[i]
			}
			continue
		}
		if opened == nil {
			continue
		}
		for _, check := range test.Diagnosis.Checks {
			if flagged(check.Status) {
				return nil
			}
		}
		return []Suggestion{{Code: SuggestTransientMissed, Test: test.Name,
			Message: "An impairment opened and closed entirely inside this run and no check reported anything. " +
				"A single sample per path cannot see a fault shorter than the run itself.",
			Evidence: fmt.Sprintf("%s at +%s until %s at +%s, inside the run +%s..+%s",
				opened.State, offsetLabel(opened.AppliedOffset)[1:], item.State, offsetLabel(item.AppliedOffset)[1:],
				offsetLabel(test.StartOffset)[1:], offsetLabel(test.EndOffset)[1:])}}
	}
	return nil
}

// dependencyContradictions looks for a probe that succeeded while a probe it
// depends on failed. With a timed fault schedule that can be honest — the
// network changed between the two — so a transition inside the run makes it
// temporally explainable and is not reported. Without one it is a contradiction
// the DAG produced on a network that never changed.
func (r *Report) dependencyContradictions(test *TestOutcome) []Suggestion {
	var out []Suggestion
	explained := len(transitionsDuring(r.Timeline, test.StartOffset, test.EndOffset)) > 0
	for id, deps := range probeDeps {
		child := checkByID(test.Diagnosis, string(id))
		if child == nil || (child.Status != "PASS" && child.Status != "WARN") {
			continue
		}
		for _, dep := range deps {
			parent := checkByID(test.Diagnosis, string(dep))
			if parent == nil || parent.Status != "FAIL" {
				continue
			}
			if explained {
				continue
			}
			out = append(out, Suggestion{Code: SuggestTimelineInconsistent, Test: test.Name, Probe: string(id),
				Message: fmt.Sprintf("%s reported %s while %s, which it depends on, reported FAIL, and the network did not change during this run. "+
					"Check whether the dependent probe is reading stale upstream state.", id, child.Status, dep),
				Evidence: fmt.Sprintf("%s: %s; %s: %s", dep, parent.Detail, id, child.Detail)})
		}
	}
	sortSuggestions(out)
	return out
}

func checkByID(d *Diagnosis, id string) *DiagnosisCheck {
	for i := range d.Checks {
		if d.Checks[i].ID == id {
			return &d.Checks[i]
		}
	}
	return nil
}

// sortSuggestions keeps a map-driven rule's output in a stable order.
func sortSuggestions(in []Suggestion) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Probe < in[j-1].Probe; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
