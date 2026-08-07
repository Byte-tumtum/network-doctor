package simulation

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Diagnosis is the part of netdoc's --json report the comparison reads. The
// field names are netdoc's stable machine-readable contract (see the report
// struct in main.go); decoding only what is compared keeps this from having to
// track every field the report gains.
type Diagnosis struct {
	Checks      []DiagnosisCheck `json:"checks"`
	Summary     string           `json:"summary"`
	Verdict     string           `json:"verdict"`
	FailedStage string           `json:"failed_stage"`
	OK          bool             `json:"ok"`
}

// DiagnosisCheck is one probe row.
type DiagnosisCheck struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Status   string             `json:"status"`
	Cause    string             `json:"cause,omitempty"`
	Ms       int64              `json:"ms"`
	Detail   string             `json:"detail"`
	Fix      string             `json:"fix"`
	Families *DiagnosisFamilies `json:"address_families,omitempty"`
	Attempts []DiagnosisAttempt `json:"attempts,omitempty"`
}

type DiagnosisAttempt struct {
	IP    string `json:"ip"`
	Ms    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}

type DiagnosisFamilies struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

// Check outcomes, in the order the report prints them.
const (
	OutcomeMatched     = "matched"
	OutcomeWrongStatus = "wrong_status"
	OutcomeWrongCause  = "wrong_cause"
	OutcomeWrongFamily = "wrong_family"
	OutcomeMissing     = "missing"
	OutcomeUnexpected  = "unexpected"
)

// CheckComparison is one expected-versus-actual pairing.
type CheckComparison struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Expected string `json:"expected,omitempty"`
	// ExpectedCause is optional so existing status-only scenarios retain their
	// comparison contract.
	ExpectedCause string `json:"expected_cause,omitempty"`
	Actual        string `json:"actual,omitempty"`
	Cause         string `json:"cause,omitempty"`
	ExpectedIPv4  string `json:"expected_ipv4,omitempty"`
	ExpectedIPv6  string `json:"expected_ipv6,omitempty"`
	ActualIPv4    string `json:"actual_ipv4,omitempty"`
	ActualIPv6    string `json:"actual_ipv6,omitempty"`
	Outcome       string `json:"outcome"`
	Detail        string `json:"detail,omitempty"`
	Fix           string `json:"fix,omitempty"`
	Ms            int64  `json:"ms,omitempty"`
}

// TestOutcome is one netdoc run and how its diagnosis lined up.
type TestOutcome struct {
	Name          string        `json:"name"`
	Node          string        `json:"node"`
	Target        string        `json:"target,omitempty"`
	Proxy         string        `json:"proxy,omitempty"`
	Trust         string        `json:"trust,omitempty"`
	SourceSegment string        `json:"source_segment,omitempty"`
	Command       []string      `json:"command"`
	Duration      time.Duration `json:"duration_ms"`
	// StartOffset and EndOffset place this netdoc process on the fault
	// timeline, relative to T0.
	StartOffset time.Duration `json:"start_offset_ms"`
	EndOffset   time.Duration `json:"end_offset_ms"`
	ExitCode    int           `json:"exit_code"`
	// ProcessOutcome distinguishes a whole-netdoc deadline or signal from a
	// probe row that used its own timeout budget.
	ProcessOutcome string `json:"process_outcome"`
	Signal         string `json:"signal,omitempty"`
	// Error is set when netdoc could not be run or produced no report at all.
	Error     string     `json:"error,omitempty"`
	Stderr    string     `json:"stderr,omitempty"`
	Diagnosis *Diagnosis `json:"diagnosis,omitempty"`

	ExpectedVerdict string            `json:"expected_verdict,omitempty"`
	ActualVerdict   string            `json:"actual_verdict,omitempty"`
	Checks          []CheckComparison `json:"checks"`
	// TimedOut names probes whose failure was the probe deadline expiring
	// rather than an answer — a diagnosis that cost the full budget.
	TimedOut []string `json:"timed_out,omitempty"`
	// RepeatVerdicts holds the verdict of every repeat run, present only when
	// --repeat asked for more than one.
	RepeatVerdicts []string `json:"repeat_verdicts,omitempty"`

	FalseNegatives int `json:"false_negatives"`
	FalsePositives int `json:"false_positives"`
	Matched        int `json:"matched"`
}

const (
	ProcessExited    = "exited"
	ProcessTimedOut  = "timed_out"
	ProcessCancelled = "cancelled"
	ProcessSignaled  = "signaled"
	ProcessExecError = "exec_error"
)

// Suggestion is one deterministic, evidence-backed improvement for netdoc.
type Suggestion struct {
	Code     string `json:"code"`
	Test     string `json:"test,omitempty"`
	Probe    string `json:"probe,omitempty"`
	Cause    string `json:"cause,omitempty"`
	Message  string `json:"message"`
	Evidence string `json:"evidence,omitempty"`
}

// Suggestion codes. Stable identifiers so a CI job can allow-list the ones a
// scenario is known to trip.
const (
	SuggestMissedFinding    = "missed_finding"
	SuggestWrongSeverity    = "wrong_severity"
	SuggestWrongCause       = "wrong_cause"
	SuggestFalsePositive    = "false_positive"
	SuggestWrongVerdict     = "wrong_verdict"
	SuggestProbeTimedOut    = "probe_timed_out"
	SuggestNoFixHint        = "no_fix_hint"
	SuggestNondeterministic = "nondeterministic"
	SuggestNoDiagnosis      = "no_diagnosis"
)

// compare fills in the expected-versus-actual half of a TestOutcome.
// probeTimeout is netdoc's per-probe budget; a failed check that spent it is
// reported as a timeout rather than an answer.
func (o *TestOutcome) compare(expect Expect, probeTimeout time.Duration) {
	o.ExpectedVerdict = expect.Verdict
	if o.Diagnosis == nil {
		return
	}
	o.ActualVerdict = o.Diagnosis.Verdict

	actual := make(map[string]DiagnosisCheck, len(o.Diagnosis.Checks))
	for _, c := range o.Diagnosis.Checks {
		actual[c.ID] = c
	}
	expected := make(map[string]string, len(expect.Checks))
	for _, e := range expect.Checks {
		expected[e.ID] = e.Status
		c, ran := actual[e.ID]
		cmp := CheckComparison{ID: e.ID, Name: c.Name, Expected: e.Status, ExpectedCause: e.Cause, Actual: c.Status,
			ExpectedIPv4: e.IPv4, ExpectedIPv6: e.IPv6,
			Outcome: OutcomeMatched, Cause: c.Cause, Detail: c.Detail, Fix: c.Fix, Ms: c.Ms}
		if c.Families != nil {
			cmp.ActualIPv4, cmp.ActualIPv6 = c.Families.IPv4, c.Families.IPv6
		}
		switch {
		case !ran:
			cmp.Outcome = OutcomeMissing
		case c.Status != e.Status:
			cmp.Outcome = OutcomeWrongStatus
		case e.Cause != "" && c.Cause != e.Cause:
			cmp.Outcome = OutcomeWrongCause
		case e.IPv4 != "" && cmp.ActualIPv4 != e.IPv4, e.IPv6 != "" && cmp.ActualIPv6 != e.IPv6:
			cmp.Outcome = OutcomeWrongFamily
		}
		o.Checks = append(o.Checks, cmp)
	}
	// Anything netdoc flagged that the scenario did not ask for. Only FAIL and
	// WARN count: a PASS nobody mentioned is not a false positive, it is the
	// rest of a working network.
	for _, c := range o.Diagnosis.Checks {
		if _, want := expected[c.ID]; want {
			continue
		}
		if c.Status != "FAIL" && c.Status != "WARN" {
			continue
		}
		o.Checks = append(o.Checks, CheckComparison{ID: c.ID, Name: c.Name, Actual: c.Status,
			Outcome: OutcomeUnexpected, Cause: c.Cause, Detail: c.Detail, Fix: c.Fix, Ms: c.Ms})
	}

	for _, c := range o.Checks {
		switch {
		case c.Outcome == OutcomeMatched:
			o.Matched++
		case c.Outcome == OutcomeUnexpected:
			o.FalsePositives++
		case flagged(c.Expected) && !flagged(c.Actual):
			// The scenario broke something and netdoc did not say so.
			o.FalseNegatives++
		case !flagged(c.Expected) && flagged(c.Actual):
			// The scenario said this part works and netdoc flagged it anyway.
			// Naming the row in expect.checks must not make the false positive
			// count for less than an unmentioned one — the healthy scenario names
			// every row it cares about, and that is where this shows up.
			o.FalsePositives++
		}
	}

	// A failing probe that spent the whole budget answered "I ran out of time",
	// which is a different — and worse — finding than a fast, definite failure.
	for _, c := range o.Diagnosis.Checks {
		if c.Status == "FAIL" && probeTimeout > 0 && time.Duration(c.Ms)*time.Millisecond >= probeTimeout {
			o.TimedOut = append(o.TimedOut, c.ID)
		}
	}
}

// flagged reports whether a status is netdoc raising its hand.
func flagged(status string) bool { return status == "FAIL" || status == "WARN" }

// verdictMatches reports whether the run reached the expected verdict. An
// empty expectation matches anything.
func (o *TestOutcome) verdictMatches() bool {
	return o.ExpectedVerdict == "" || o.ExpectedVerdict == o.ActualVerdict
}

// ok reports whether everything the scenario claimed came true.
func (o *TestOutcome) ok() bool {
	if o.Error != "" || o.Diagnosis == nil || !o.verdictMatches() {
		return false
	}
	for _, c := range o.Checks {
		if c.Outcome != OutcomeMatched {
			return false
		}
	}
	return len(o.RepeatVerdicts) == 0 || sameVerdicts(o.RepeatVerdicts)
}

func sameVerdicts(v []string) bool {
	for _, s := range v {
		if s != v[0] {
			return false
		}
	}
	return true
}

// suggest derives improvement suggestions from what the comparison found. Every
// rule is a plain function of the report — no heuristics that need a model, and
// no rule that can fire without evidence to point at.
func (o *TestOutcome) suggest() []Suggestion {
	var out []Suggestion
	add := func(code, probe, msg, evidence string) {
		out = append(out, Suggestion{Code: code, Test: o.Name, Probe: probe, Message: msg, Evidence: evidence})
	}
	addCheck := func(code string, check CheckComparison, msg, evidence string) {
		out = append(out, Suggestion{Code: code, Test: o.Name, Probe: check.ID, Cause: check.Cause,
			Message: msg, Evidence: evidence})
	}
	if o.Error != "" || o.Diagnosis == nil {
		add(SuggestNoDiagnosis, "", "netdoc produced no report for this scenario; the run itself is the bug.", o.Error)
		return out
	}
	for _, c := range o.Checks {
		switch {
		case c.Outcome == OutcomeMissing:
			addCheck(SuggestMissedFinding, c, fmt.Sprintf(
				"No %s row in the report, but the scenario expected one at %s. The probe never ran — check the DAG dependency that skipped it.",
				c.ID, c.Expected), "")
		case c.Outcome == OutcomeWrongStatus && flagged(c.Expected) && !flagged(c.Actual):
			addCheck(SuggestMissedFinding, c, fmt.Sprintf(
				"%s reported %s where the scenario broke it and expected %s — the probe does not detect this fault.",
				c.ID, c.Actual, c.Expected), c.Detail)
		case c.Outcome == OutcomeWrongStatus && !flagged(c.Expected) && flagged(c.Actual):
			addCheck(SuggestFalsePositive, c, fmt.Sprintf(
				"%s reported %s where the scenario expected %s — netdoc flagged a part of the network the scenario left working.",
				c.ID, c.Actual, c.Expected), c.Detail)
		case c.Outcome == OutcomeWrongStatus:
			addCheck(SuggestWrongSeverity, c, fmt.Sprintf(
				"%s reported %s, expected %s — same finding, wrong severity.", c.ID, c.Actual, c.Expected), c.Detail)
		case c.Outcome == OutcomeWrongCause:
			addCheck(SuggestWrongCause, c, fmt.Sprintf(
				"%s reported cause %q, expected %q — the failure was detected but classified at the wrong stage.",
				c.ID, c.Cause, c.ExpectedCause), c.Detail)
		case c.Outcome == OutcomeWrongFamily:
			addCheck(SuggestWrongCause, c, fmt.Sprintf(
				"%s reported address families IPv4=%q IPv6=%q, expected IPv4=%q IPv6=%q.",
				c.ID, c.ActualIPv4, c.ActualIPv6, c.ExpectedIPv4, c.ExpectedIPv6), c.Detail)
		case c.Outcome == OutcomeUnexpected:
			addCheck(SuggestFalsePositive, c, fmt.Sprintf(
				"%s reported %s in a network where the scenario expects it to be fine — likely false positive.", c.ID, c.Actual), c.Detail)
		case c.Outcome == OutcomeMatched && flagged(c.Actual) && c.Fix == "":
			addCheck(SuggestNoFixHint, c, fmt.Sprintf(
				"%s reported %s with no fix hint — the user is told what broke but not what to do.", c.ID, c.Actual), c.Detail)
		}
	}
	if !o.verdictMatches() {
		add(SuggestWrongVerdict, "", fmt.Sprintf(
			"Verdict was %q, expected %q — the headline classification does not match the injected root cause.",
			o.ActualVerdict, o.ExpectedVerdict), o.Diagnosis.Summary)
	}
	for _, id := range o.TimedOut {
		add(SuggestProbeTimedOut, id, fmt.Sprintf(
			"%s failed by exhausting the probe timeout rather than returning an answer — consider a cheaper negative signal so the diagnosis is not paced by the deadline.", id), "")
	}
	if len(o.RepeatVerdicts) > 1 && !sameVerdicts(o.RepeatVerdicts) {
		add(SuggestNondeterministic, "", fmt.Sprintf(
			"Repeated identical runs disagreed: %s. A diagnosis that changes without the network changing is not reproducible.",
			strings.Join(uniqueSorted(o.RepeatVerdicts), ", ")), "")
	}
	return out
}

func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
