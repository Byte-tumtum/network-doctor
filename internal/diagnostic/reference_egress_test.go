// The direct-egress row dials a fixed pair of anycast reference addresses. What
// it can prove is therefore a statement about those addresses, and these tests
// hold the interpretation pass to it: a reference failure may not be promoted
// into a claim about egress in general that another observation in the same run
// contradicts, nor into a claim about where a path breaks that no observation
// supports. The conclusions local route state genuinely does prove are pinned
// here too, so correcting the overreach cannot quietly cost them.

package diagnostic

import (
	"net"
	"strings"
	"testing"
)

// blanketClaims are the sentences a failed reference sample cannot carry:
// egress as a whole, or a located break. The prose is checked as well as the
// finding because the summary is the surface most users read, and a correct
// identity underneath does not excuse a sentence that says more than the run
// observed.
var blanketClaims = []string{
	"direct internet egress is blocked",
	"the general internet is",
	"no working network egress",
	"local egress problem",
}

func assertNoBlanketClaim(t *testing.T, where, summary string) {
	t.Helper()
	lower := strings.ToLower(summary)
	for _, claim := range blanketClaims {
		if strings.Contains(lower, claim) {
			t.Errorf("%s: %q asserts %q, which failing to reach the reference endpoints does not establish", where, summary, claim)
		}
	}
}

func hasEvidenceItem(evidence []CausalEvidence, want CausalEvidence) bool {
	for _, e := range evidence {
		if e == want {
			return true
		}
	}
	return false
}

// TestReachedTargetRefutesABlockedEgressVerdict is the counterexample the whole
// distinction rests on: the fixed reference endpoints are unreachable while an
// unrelated public destination answers a direct connection on the same run.
// That success is not a mitigating detail, it is a refutation, so the run may
// report the reference endpoints as unreachable and nothing more.
func TestReachedTargetRefutesABlockedEgressVerdict(t *testing.T) {
	target := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP, ProbeTLS, ProbeHTTP, ProbeHTTPS}
	build := func() map[ProbeID]ProbeResult {
		return map[ProbeID]ProbeResult{
			ProbeIface: {Status: StatusPass},
			ProbeInternet: {
				Status: StatusFail,
				Detail: "no direct TCP egress to 1.1.1.1, 8.8.8.8 (port 443)",
				Fix:    egressFix,
			},
			ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{net.ParseIP("93.184.216.34")}},
			ProbeTargetTCP: {Status: StatusPass, SelectedIP: net.ParseIP("93.184.216.34")},
			ProbeTLS:       {Status: StatusPass},
			ProbeHTTP:      {Status: StatusPass},
			ProbeHTTPS:     {Status: StatusPass},
		}
	}

	// Both shapes of the same run: the raw failure, and the WARN the
	// cross-probe pass leaves once another path has proved the network usable.
	// The verdict must not depend on which pass a caller looks at.
	for _, tc := range []struct {
		name string
		// finalize runs the cross-probe pass, which relaxes the egress failure
		// to a WARN once another path has proved the network usable. The
		// verdict must not depend on which pass a caller looks at.
		finalize bool
		// targetStatus is the state the endpoint row reported. A slow target
		// still answered, so it refutes the blocked reading just as a clean
		// one does, and the recorded evidence has to say so either way.
		targetStatus Status
	}{
		{"raw failure", false, StatusPass},
		{"after Finalize", true, StatusPass},
		{"target answered but degraded", true, StatusWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := build()
			endpoint := res[ProbeTargetTCP]
			endpoint.Status = tc.targetStatus
			res[ProbeTargetTCP] = endpoint
			if tc.finalize {
				Finalize(res)
			}
			d := Interpret(target, order, res)
			assertNoBlanketClaim(t, "summary", d.Summary)
			if len(d.Findings) != 1 {
				t.Fatalf("findings = %+v, want exactly one", d.Findings)
			}
			f := d.Findings[0]
			if f.ID == DiagnosisDirectEgressBlocked {
				t.Errorf("finding is %q while a public destination answered a direct connection in the same run", f.ID)
			}
			if f.Focus != ProbeInternet {
				t.Errorf("focus = %q, want the egress row that actually failed", f.Focus)
			}
			if d.Verdict != VerdictDegraded {
				t.Errorf("verdict = %q, want %q: the run is impaired, not broken", d.Verdict, VerdictDegraded)
			}
			// The refutation has to be recorded, not merely obeyed: the
			// evidence model is where a reader checks why the stronger
			// conclusion was not taken.
			reached := ObservationStatusPass
			if tc.targetStatus == StatusWarn {
				reached = ObservationStatusWarn
			}
			ruledOut := CausalEvidence{
				Kind: EvidenceRuledOut, Check: ProbeTargetTCP,
				Observation: reached, Candidate: DiagnosisDirectEgressBlocked,
			}
			if !hasEvidenceItem(f.Evidence, ruledOut) {
				t.Errorf("evidence %+v does not rule out %q from the target's success", f.Evidence, DiagnosisDirectEgressBlocked)
			}
			// Evidence must be narrowed, never dropped.
			if !strings.Contains(res[ProbeInternet].Detail, "1.1.1.1") {
				t.Errorf("egress detail lost the endpoints it tried: %q", res[ProbeInternet].Detail)
			}
			if strings.Contains(res[ProbeInternet].Fix, "no internet egress") {
				t.Errorf("egress row hint %q claims there is no internet egress, which this run refuted", res[ProbeInternet].Fix)
			}
		})
	}
}

// TestBothPathsFailingDoesNotLocateTheBreak is the ambiguous case. The
// reference endpoints and the endpoint under test both failed, and nothing
// looked at local routing state, so the run has a correlation and no location.
// It may say what did not answer; it may not say whose fault that is.
func TestBothPathsFailingDoesNotLocateTheBreak(t *testing.T) {
	remote := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	device := &Target{Host: "192.168.1.10", IP: net.ParseIP("192.168.1.10"), Port: 9100, Proto: ProtoNone}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP}

	for _, tc := range []struct {
		name   string
		target *Target
		dns    ProbeResult
	}{
		{"public target", remote, ProbeResult{Status: StatusPass, Addrs: []net.IP{net.ParseIP("93.184.216.34")}}},
		{"device on the local network", device, ProbeResult{Status: StatusNA}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := map[ProbeID]ProbeResult{
				ProbeIface:     {Status: StatusPass},
				ProbeInternet:  {Status: StatusFail, Detail: "no direct TCP egress to 1.1.1.1, 8.8.8.8 (port 443)"},
				ProbeDNS:       tc.dns,
				ProbeTargetTCP: {Status: StatusFail},
			}
			d := Interpret(tc.target, order, res)
			assertNoBlanketClaim(t, "summary", d.Summary)
			if len(d.Findings) != 1 {
				t.Fatalf("findings = %+v, want exactly one", d.Findings)
			}
			f := d.Findings[0]
			if f.ID == DiagnosisLocalEgressFailure {
				t.Errorf("finding is %q, but nothing in this run observed where the path breaks", f.ID)
			}
			if f.Confidence == ConfidenceHigh {
				t.Errorf("confidence = %q for a conclusion with no localizing observation", f.Confidence)
			}
			// The observation itself still has to survive: both rows failed and
			// the report must keep saying so.
			for _, id := range []ProbeID{ProbeInternet, ProbeTargetTCP} {
				if !hasEvidenceItem(f.Evidence, CausalEvidence{Kind: EvidenceSupport, Check: id, Observation: ObservationStatusFail}) {
					t.Errorf("evidence %+v drops the observed failure of %q", f.Evidence, id)
				}
			}
			if rem, ok := Remediate(d, res, "linux"); ok && strings.Contains(rem.Why, "Nothing this machine sends is arriving anywhere") {
				t.Errorf("remediation states a located failure the run did not observe: %q", rem.Why)
			}
		})
	}
}

// TestObservedRouteStateStillLocatesTheBreak is the other half of the rule.
// Where the operating system's own routing and neighbor tables classified the
// dead path, the location is observed rather than inferred from silence, and
// the stronger conclusion and its route-specific repair must both survive.
func TestObservedRouteStateStillLocatesTheBreak(t *testing.T) {
	target := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP}
	for _, tc := range []struct {
		cause string
		want  RemediationID
	}{
		{RouteCauseNoDefaultRoute, RemedyRestoreDefaultRoute},
		{RouteCauseGatewayUnreachable, RemedyReachGateway},
		{RouteCauseSelectedPathFailed, RemedyCheckUpstream},
		{RouteCausePreferredPathFailed, RemedyFixPreferredRoute},
	} {
		t.Run(tc.cause, func(t *testing.T) {
			res := map[ProbeID]ProbeResult{
				ProbeIface:     {Status: StatusPass},
				ProbeInternet:  {Status: StatusFail, Cause: tc.cause},
				ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{net.ParseIP("93.184.216.34")}},
				ProbeTargetTCP: {Status: StatusFail},
			}
			d := Interpret(target, order, res)
			if len(d.Findings) != 1 || d.Findings[0].ID != DiagnosisLocalEgressFailure {
				t.Fatalf("findings = %+v, want %q kept where route state proves it", d.Findings, DiagnosisLocalEgressFailure)
			}
			if rem, ok := Remediate(d, res, "linux"); !ok || rem.ID != tc.want {
				t.Errorf("remediation = %q (ok=%v), want %q", rem.ID, ok, tc.want)
			}
		})
	}
}
