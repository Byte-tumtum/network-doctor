package simulation

import "testing"

func routeCase(first, second TestOutcome, routes ...RouteEvidence) *Report {
	report := &Report{Tests: []TestOutcome{first, second}}
	report.Evidence.Routes = routes
	return report
}

func diagnosisWith(id, status string) *Diagnosis {
	return &Diagnosis{Checks: []DiagnosisCheck{{ID: id, Status: status}}}
}

func TestRouteSuggestionsNeedAControlOnAnotherPath(t *testing.T) {
	selectedDefault := RouteEvidence{Node: "client", Destination: "1.1.1.1", Segment: "client-lan", Selected: true}
	failedDefault := TestOutcome{Node: "client", Target: "target.test:80", Diagnosis: diagnosisWith("internet_tcp", "WARN")}
	reachedTarget := diagnosisWith("target_tcp", "PASS")

	cases := []struct {
		name     string
		report   *Report
		code     string
		evidence string
	}{
		{
			// The hunt generator repeats the first test to watch a timeline. A
			// second run of the same probe over the same path proves nothing about
			// routing, and its empty source segment used to reach the report as a
			// hole in the middle of the evidence sentence.
			name: "repeat of the same test on the same path",
			report: routeCase(failedDefault,
				TestOutcome{Node: "client", Target: "target.test:80", Diagnosis: reachedTarget},
				selectedDefault),
		},
		{
			name: "specific route to another target",
			report: routeCase(failedDefault,
				TestOutcome{Node: "client", Target: "routed-target.test:80", Diagnosis: reachedTarget},
				selectedDefault),
			code:     "wrong_default_route_evidence",
			evidence: "selected segment client-lan failed; a specific route reached routed-target.test:80",
		},
		{
			name: "control bound to another interface",
			report: routeCase(failedDefault,
				TestOutcome{Node: "client", Target: "target.test:80", SourceSegment: "working-lan", Diagnosis: reachedTarget},
				selectedDefault),
			code:     "alternate_route_available",
			evidence: "selected segment client-lan failed; segment working-lan reached target.test:80",
		},
		{
			// No selected route to name, but the control still took another
			// interface: the sentence has to stay readable without one.
			name: "no selected route in the evidence",
			report: routeCase(failedDefault,
				TestOutcome{Node: "client", Target: "target.test:80", SourceSegment: "working-lan", Diagnosis: reachedTarget}),
			code:     "alternate_route_available",
			evidence: "the selected default path failed; segment working-lan reached target.test:80",
		},
		{
			name: "control did not reach the target",
			report: routeCase(failedDefault,
				TestOutcome{Node: "client", Target: "routed-target.test:80", Diagnosis: diagnosisWith("target_tcp", "FAIL")},
				selectedDefault),
		},
		{
			name: "selected path worked",
			report: routeCase(TestOutcome{Node: "client", Diagnosis: diagnosisWith("internet_tcp", "PASS")},
				TestOutcome{Node: "client", Target: "routed-target.test:80", Diagnosis: reachedTarget},
				selectedDefault),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.report.routeSuggestions()
			if tc.code == "" {
				if len(got) != 0 {
					t.Fatalf("want no suggestion, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one suggestion, got %+v", got)
			}
			if got[0].Code != tc.code {
				t.Errorf("code: want %q, got %q", tc.code, got[0].Code)
			}
			if got[0].Evidence != tc.evidence {
				t.Errorf("evidence:\nwant %q\ngot  %q", tc.evidence, got[0].Evidence)
			}
		})
	}
}
