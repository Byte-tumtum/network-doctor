package simulation

import (
	"strings"
	"testing"
	"time"
)

func diag(verdict string, checks ...DiagnosisCheck) *Diagnosis {
	return &Diagnosis{Verdict: verdict, Summary: "summary", Checks: checks}
}

func check(id, status string) DiagnosisCheck {
	return DiagnosisCheck{ID: id, Name: id, Status: status, Detail: "detail", Fix: "fix", Ms: 1}
}

func TestCompareMatches(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("dns",
		check("iface", "PASS"), check("dns", "FAIL"), check("internet_tcp", "PASS"))}
	o.compare(Expect{Verdict: "dns", Checks: []ExpectedCheck{
		{ID: "iface", Status: "PASS"}, {ID: "dns", Status: "FAIL"}, {ID: "internet_tcp", Status: "PASS"},
	}}, 4*time.Second)

	if !o.ok() {
		t.Fatalf("want ok, got %+v", o.Checks)
	}
	if o.Matched != 3 || o.FalseNegatives != 0 || o.FalsePositives != 0 {
		t.Errorf("matched=%d fn=%d fp=%d", o.Matched, o.FalseNegatives, o.FalsePositives)
	}
	if len(o.suggest()) != 0 {
		t.Errorf("a clean run should suggest nothing, got %v", o.suggest())
	}
}

func TestCompareFalseNegative(t *testing.T) {
	// The scenario broke DNS; netdoc called it fine. That is the miss this
	// whole package exists to name.
	o := TestOutcome{Name: "t", Diagnosis: diag("ok", check("dns", "PASS"))}
	o.compare(Expect{Verdict: "dns", Checks: []ExpectedCheck{{ID: "dns", Status: "FAIL"}}}, 4*time.Second)

	if o.ok() {
		t.Error("want not ok")
	}
	if o.FalseNegatives != 1 || o.FalsePositives != 0 {
		t.Errorf("fn=%d fp=%d, want 1/0", o.FalseNegatives, o.FalsePositives)
	}
	if o.Checks[0].Outcome != OutcomeWrongStatus {
		t.Errorf("outcome = %q", o.Checks[0].Outcome)
	}
	codes := suggestionCodes(&o)
	if !contains(codes, SuggestMissedFinding) || !contains(codes, SuggestWrongVerdict) {
		t.Errorf("codes = %v", codes)
	}
}

func TestCompareFalsePositive(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("ok", check("iface", "PASS"), check("internet_tcp", "FAIL"))}
	o.compare(Expect{Verdict: "ok", Checks: []ExpectedCheck{{ID: "iface", Status: "PASS"}}}, 4*time.Second)

	if o.FalsePositives != 1 {
		t.Errorf("fp = %d, want 1", o.FalsePositives)
	}
	if !contains(suggestionCodes(&o), SuggestFalsePositive) {
		t.Errorf("codes = %v", suggestionCodes(&o))
	}
	// A PASS nobody mentioned is not a false positive.
	o2 := TestOutcome{Name: "t", Diagnosis: diag("ok", check("iface", "PASS"), check("http", "PASS"))}
	o2.compare(Expect{Verdict: "ok", Checks: []ExpectedCheck{{ID: "iface", Status: "PASS"}}}, 4*time.Second)
	if o2.FalsePositives != 0 || !o2.ok() {
		t.Errorf("an unmentioned PASS should be ignored, got fp=%d ok=%t", o2.FalsePositives, o2.ok())
	}
}

func TestCompareMissingRow(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("ok", check("iface", "PASS"))}
	o.compare(Expect{Checks: []ExpectedCheck{{ID: "dns", Status: "FAIL"}}}, 4*time.Second)
	if o.Checks[0].Outcome != OutcomeMissing || o.FalseNegatives != 1 {
		t.Errorf("outcome=%q fn=%d", o.Checks[0].Outcome, o.FalseNegatives)
	}
}

func TestCompareWrongSeverity(t *testing.T) {
	// WARN where FAIL was expected is still a finding — the severity is what is
	// wrong, and the suggestion has to say so rather than cry "missed".
	o := TestOutcome{Name: "t", Diagnosis: diag("degraded", check("internet_tcp", "WARN"))}
	o.compare(Expect{Checks: []ExpectedCheck{{ID: "internet_tcp", Status: "FAIL"}}}, 4*time.Second)
	if o.FalseNegatives != 0 {
		t.Errorf("fn = %d, want 0: the finding was made, at the wrong level", o.FalseNegatives)
	}
	if !contains(suggestionCodes(&o), SuggestWrongSeverity) {
		t.Errorf("codes = %v", suggestionCodes(&o))
	}
}

func TestCompareTimeout(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("dns", DiagnosisCheck{ID: "dns", Status: "FAIL", Ms: 4000, Fix: "f"})}
	o.compare(Expect{Verdict: "dns", Checks: []ExpectedCheck{{ID: "dns", Status: "FAIL"}}}, 4*time.Second)
	if len(o.TimedOut) != 1 {
		t.Fatalf("timed out = %v", o.TimedOut)
	}
	if !contains(suggestionCodes(&o), SuggestProbeTimedOut) {
		t.Errorf("codes = %v", suggestionCodes(&o))
	}
	// A timeout is evidence, not a mismatch: the expectation still held.
	if !o.ok() {
		t.Error("a timed-out probe that reported what was expected is still a match")
	}
}

func TestCompareMissingFixHint(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("dns", DiagnosisCheck{ID: "dns", Status: "FAIL", Detail: "d"})}
	o.compare(Expect{Verdict: "dns", Checks: []ExpectedCheck{{ID: "dns", Status: "FAIL"}}}, 4*time.Second)
	if !contains(suggestionCodes(&o), SuggestNoFixHint) {
		t.Errorf("codes = %v", suggestionCodes(&o))
	}
}

func TestCompareUnstableVerdicts(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("ok", check("dns", "PASS")),
		RepeatVerdicts: []string{"ok", "dns", "ok"}}
	o.compare(Expect{Verdict: "ok", Checks: []ExpectedCheck{{ID: "dns", Status: "PASS"}}}, 4*time.Second)
	if o.ok() {
		t.Error("a diagnosis that changed between identical runs is not a pass")
	}
	if !contains(suggestionCodes(&o), SuggestNondeterministic) {
		t.Errorf("codes = %v", suggestionCodes(&o))
	}
}

func TestCompareNoDiagnosis(t *testing.T) {
	o := TestOutcome{Name: "t", Error: "netdoc did not run"}
	o.compare(Expect{Verdict: "ok"}, 4*time.Second)
	if o.ok() {
		t.Error("want not ok")
	}
	if codes := suggestionCodes(&o); len(codes) != 1 || codes[0] != SuggestNoDiagnosis {
		t.Errorf("codes = %v", codes)
	}
}

func TestEmptyExpectedVerdictMatchesAnything(t *testing.T) {
	o := TestOutcome{Name: "t", Diagnosis: diag("service", check("tls", "FAIL"))}
	o.compare(Expect{Checks: []ExpectedCheck{{ID: "tls", Status: "FAIL"}}}, 4*time.Second)
	if !o.ok() {
		t.Error("a scenario that expects no particular verdict must not fail on the verdict")
	}
}

func suggestionCodes(o *TestOutcome) []string {
	var out []string
	for _, s := range o.suggest() {
		out = append(out, s.Code)
	}
	return out
}

func TestReportResult(t *testing.T) {
	pass := TestOutcome{Name: "a", Diagnosis: diag("ok", check("dns", "PASS"))}
	pass.compare(Expect{Verdict: "ok", Checks: []ExpectedCheck{{ID: "dns", Status: "PASS"}}}, time.Second)
	fail := TestOutcome{Name: "b", Diagnosis: diag("ok", check("dns", "PASS"))}
	fail.compare(Expect{Verdict: "dns", Checks: []ExpectedCheck{{ID: "dns", Status: "FAIL"}}}, time.Second)

	tests := []struct {
		name           string
		rep            Report
		want           string
		hasSuggestions bool
	}{
		{"all held", Report{Tests: []TestOutcome{pass}}, ResultPass, false},
		{"none held", Report{Tests: []TestOutcome{fail}}, ResultFail, true},
		{"some held", Report{Tests: []TestOutcome{pass, fail}}, ResultPartial, true},
		{"setup broke", Report{Error: "boom", Tests: []TestOutcome{pass}}, ResultError, false},
		{"nothing ran", Report{}, ResultError, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := tc.rep
			rep.finish()
			if rep.Result != tc.want {
				t.Errorf("result = %q, want %q", rep.Result, tc.want)
			}
			if got := len(rep.Suggestions) > 0; got != tc.hasSuggestions {
				t.Errorf("suggestions = %v", rep.Suggestions)
			}
		})
	}
}

func TestWriteTextSanitizesSubprocessText(t *testing.T) {
	// netdoc's detail lines carry text from the far end of the network, and
	// this report lands on a terminal.
	o := TestOutcome{Name: "t", Node: "client",
		Diagnosis: diag("ok", DiagnosisCheck{ID: "dns", Status: "PASS", Detail: "hi\x1b]52;c;cGF5bG9hZA==\x07there"})}
	o.compare(Expect{Verdict: "ok", Checks: []ExpectedCheck{{ID: "dns", Status: "PASS"}}}, time.Second)
	rep := Report{Scenario: "s", Tests: []TestOutcome{o}}
	rep.finish()
	var sb strings.Builder
	rep.WriteText(&sb)
	if strings.Contains(sb.String(), "\x1b") {
		t.Errorf("escape sequence survived into the report: %q", sb.String())
	}
}

func TestWriteJSONIsValid(t *testing.T) {
	rep := Report{Scenario: "s", ID: "abc123", Backend: "linux-netns"}
	rep.finish()
	var sb strings.Builder
	if err := rep.WriteJSON(&sb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"scenario": "s"`, `"result": "ERROR"`, `"id": "abc123"`} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("json is missing %s:\n%s", want, sb.String())
		}
	}
}
