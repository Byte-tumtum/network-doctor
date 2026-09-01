package diagnostic

import (
	"net"
	"reflect"
	"slices"
	"testing"
	"time"
)

func matrixCaseNamed(t *testing.T, name string) matrixCase {
	t.Helper()
	for _, c := range diagnosisMatrix() {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("diagnosis matrix has no case %q", name)
	return matrixCase{}
}

func TestCausalEvidenceRecordsTheSelectingBranch(t *testing.T) {
	tests := []struct {
		name string
		want []CausalEvidence
	}{
		{
			name: "generic system resolver failing",
			want: []CausalEvidence{
				{Kind: EvidenceSupport, Check: ProbeDNS, Observation: ObservationStatusFail},
				{Kind: EvidenceSupport, Check: ProbeDNSPublic, Observation: ObservationDNSAnswers},
				{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationStatusPass},
				{Kind: EvidenceContradiction, Check: ProbeDNSPublic, Observation: ObservationDNSAnswers, Candidate: DiagnosisDNSFailure},
				{Kind: EvidenceRuledOut, Check: ProbeDNSPublic, Observation: ObservationDNSAnswers, Candidate: DiagnosisDNSNameNotFound},
				{Kind: EvidenceRuledOut, Check: ProbeInternet, Observation: ObservationStatusPass, Candidate: DiagnosisOffline},
			},
		},
		{
			name: "target refuses the connection",
			want: []CausalEvidence{
				{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationCause},
				{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationStatusPass},
				{Kind: EvidenceRuledOut, Check: ProbeTargetTCP, Observation: ObservationCause, Candidate: DiagnosisTargetUnreachable},
				{Kind: EvidenceRuledOut, Check: ProbeInternet, Observation: ObservationStatusPass, Candidate: DiagnosisLocalEgressFailure},
			},
		},
		{
			name: "TLS expiry explained by a fast clock",
			want: []CausalEvidence{
				{Kind: EvidenceSupport, Check: ProbeTLS, Observation: ObservationCause},
				{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationStatusPass},
				{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationStatusPass},
				{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationClockOffset},
				{Kind: EvidenceRuledOut, Check: ProbeTargetTCP, Observation: ObservationStatusPass, Candidate: DiagnosisTLSTCPUnreachable},
			},
		},
		{
			name: "TLS expiry with the clock ruled out",
			want: []CausalEvidence{
				{Kind: EvidenceSupport, Check: ProbeTLS, Observation: ObservationCause},
				{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationStatusPass},
				{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationStatusPass},
				{Kind: EvidenceRuledOut, Check: ProbeInternet, Observation: ObservationClockOffset, Candidate: DiagnosisTLSClockSkew},
				{Kind: EvidenceRuledOut, Check: ProbeTargetTCP, Observation: ObservationStatusPass, Candidate: DiagnosisTLSTCPUnreachable},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := matrixCaseNamed(t, tt.name)
			got := Interpret(c.target, c.order, c.res)
			if len(got.Findings) != 1 {
				t.Fatalf("findings = %+v", got.Findings)
			}
			if !reflect.DeepEqual(got.Findings[0].Evidence, tt.want) {
				t.Errorf("causal evidence =\n%+v\nwant\n%+v", got.Findings[0].Evidence, tt.want)
			}
		})
	}
}

func TestCausalEvidenceKeepsNotEvaluatedDistinct(t *testing.T) {
	target := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP}
	res := map[ProbeID]ProbeResult{
		ProbeIface:     {Status: StatusPass},
		ProbeInternet:  {Status: StatusPass},
		ProbeDNS:       {Status: StatusFail},
		ProbeTargetTCP: SkipPrereq(ProbeTargetTCP),
	}
	finding := Interpret(target, order, res).Findings[0]
	want := CausalEvidence{Kind: EvidenceNotEvaluated, Check: ProbeTargetTCP,
		Observation: ObservationStatusSkip, Reason: NotEvaluatedPrerequisite}
	if !slices.Contains(finding.Evidence, want) {
		t.Fatalf("evidence = %+v, want prerequisite item %+v", finding.Evidence, want)
	}
	for _, evidence := range finding.Evidence {
		if evidence.Check == ProbeTargetTCP && evidence.Kind == EvidenceRuledOut {
			t.Errorf("skipped target check was represented as ruled out: %+v", evidence)
		}
	}

	c := matrixCaseNamed(t, "target unreachable with egress unchecked")
	finding = Interpret(c.target, c.order, c.res).Findings[0]
	want = CausalEvidence{Kind: EvidenceNotEvaluated, Check: ProbeInternet, Reason: NotEvaluatedNotSelected}
	if !slices.Contains(finding.Evidence, want) {
		t.Fatalf("evidence = %+v, want not-selected item %+v", finding.Evidence, want)
	}
}

func TestEveryActionableDiagnosisHasObservedSupport(t *testing.T) {
	for _, c := range diagnosisMatrix() {
		t.Run(c.name, func(t *testing.T) {
			for _, finding := range Interpret(c.target, c.order, c.res).Findings {
				hasSupport := false
				for _, evidence := range finding.Evidence {
					if evidence.Kind == EvidenceSupport {
						hasSupport = true
					}
					assertEvidenceObservation(t, finding.ID, evidence, c.order, c.res)
				}
				if !hasSupport {
					t.Errorf("actionable diagnosis %q has no supporting observation", finding.ID)
				}
			}
		})
	}
}

func TestCausalEvidenceOrderingIsDeterministic(t *testing.T) {
	c := matrixCaseNamed(t, "generic system resolver failing")
	want := Interpret(c.target, c.order, c.res)
	for range 20 {
		if got := Interpret(c.target, c.order, c.res); !reflect.DeepEqual(got.Findings, want.Findings) {
			t.Fatalf("evidence order changed:\n%+v\nwant\n%+v", got.Findings, want.Findings)
		}
	}
}

func assertEvidenceObservation(t *testing.T, selected DiagnosisID, evidence CausalEvidence, order []ProbeID, res map[ProbeID]ProbeResult) {
	t.Helper()
	r, exists := res[evidence.Check]
	if evidence.Kind == EvidenceNotEvaluated && evidence.Reason == NotEvaluatedNotSelected {
		if exists || slices.Contains(order, evidence.Check) || evidence.Observation != "" {
			t.Errorf("not-selected evidence has an observation or a result: %+v", evidence)
		}
		return
	}
	if !exists || !slices.Contains(order, evidence.Check) {
		t.Errorf("evidence references a check that did not report: %+v", evidence)
		return
	}
	if (evidence.Kind == EvidenceRuledOut || evidence.Kind == EvidenceContradiction) &&
		(evidence.Candidate == "" || evidence.Candidate == selected || r.Status == StatusSkip || r.Status == StatusNA) {
		t.Errorf("alternative relationship lacks independent positive evidence: %+v from %+v", evidence, r)
	}
	observed := false
	switch evidence.Observation {
	case ObservationStatusPass:
		observed = r.Status == StatusPass
	case ObservationStatusWarn:
		observed = r.Status == StatusWarn && !r.downgraded
	case ObservationStatusFail:
		observed = r.Status == StatusFail
	case ObservationStatusSkip:
		observed = r.Status == StatusSkip && evidence.Kind == EvidenceNotEvaluated && evidence.Reason == NotEvaluatedPrerequisite
	case ObservationStatusNA:
		observed = r.Status == StatusNA && evidence.Kind == EvidenceNotEvaluated && evidence.Reason == NotEvaluatedNotApplicable
	case ObservationCause:
		observed = r.Cause != "" && evidence.Value == r.causeFamily
	case ObservationDNSAnswers:
		observed = len(r.Addrs) > 0 && (evidence.Value == "" || slices.ContainsFunc(r.Addrs, func(ip net.IP) bool {
			return ip.String() == evidence.Value
		}))
	case ObservationDNSNotFound:
		observed = r.DNSNotFound
	case ObservationCaptivePortal:
		observed = r.Portal != nil
	case ObservationTimeout:
		observed = r.timedOut || r.Cause == TLSCauseTimeout
	case ObservationClockOffset:
		observed = r.clockOffset.Abs() >= 5*time.Minute
	case ObservationStatusDowngraded:
		observed = r.downgraded
	case ObservationFamilyReachable:
		observed = r.Families != nil && (evidence.Value == "ipv4" && r.Families.IPv4 == FamilyReachable ||
			evidence.Value == "ipv6" && r.Families.IPv6 == FamilyReachable)
	case ObservationFamilyFailed:
		observed = r.Families != nil && (evidence.Value == "ipv4" && r.Families.IPv4 == FamilyUnreachable ||
			evidence.Value == "ipv6" && r.Families.IPv6 == FamilyUnreachable)
	case ObservationAddressSucceeded:
		for _, attempt := range r.Attempts {
			observed = observed || attempt.IP.String() == evidence.Value && attempt.Err == nil
		}
	case ObservationAddressFailed:
		for _, attempt := range r.Attempts {
			observed = observed || attempt.IP.String() == evidence.Value && attempt.Err != nil && !isCanceledAttempt(attempt)
		}
	// Every route observation is a fact about this row's own recorded
	// decisions. A cross-row claim has to be spelled as one item per row, so
	// there is nothing here that reaches into another result to be verified.
	case ObservationRouteTunneled:
		observed = slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.Iface == evidence.Value && d.Tunneled()
		})
	case ObservationRouteDirect:
		observed = slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.Iface == evidence.Value && d.Tunnel == TunnelDirect
		})
	case ObservationRouteUnreachable:
		observed = slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.Unreachable && d.Destination.String() == evidence.Value
		})
	case ObservationRoutePathDiffers:
		observed = len(r.Routes) > 0 && r.Routes[0].Iface != "" && r.Routes[0].Iface != evidence.Value
	case ObservationRouteNextHopDiffers:
		observed = evidence.Value != "" && slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.Gateway != nil && d.Gateway.String() != evidence.Value
		})
	case ObservationRouteTableDiffers:
		observed = slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.TableKnown && d.Table != ""
		})
	case ObservationRouteFamilySplit:
		split, known := familyPathsDiffer(r.Routes)
		observed = known && split
	case ObservationRouteInterfaceMTU:
		observed = slices.ContainsFunc(r.Routes, func(d RouteDecision) bool {
			return d.Iface == evidence.Value && d.MTU > 0
		})
	}
	if !observed {
		t.Errorf("evidence references an observation that did not occur: %+v from %+v", evidence, r)
	}
}

func TestRoutingCauseRemainsTheSupportingObservation(t *testing.T) {
	target := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP}
	res := map[ProbeID]ProbeResult{
		ProbeIface:     {Status: StatusPass},
		ProbeInternet:  {Status: StatusFail, Cause: RouteCauseNoDefaultRoute},
		ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{net.ParseIP("192.0.2.1")}},
		ProbeTargetTCP: {Status: StatusFail},
	}
	d := Interpret(target, order, res)
	if len(d.Findings) != 1 || d.Findings[0].ID != DiagnosisLocalEgressFailure {
		t.Fatalf("diagnosis = %+v", d)
	}
	want := CausalEvidence{Kind: EvidenceSupport, Check: ProbeInternet, Observation: ObservationCause}
	if len(d.Findings[0].Evidence) == 0 || d.Findings[0].Evidence[0] != want {
		t.Errorf("first evidence = %+v, want %+v", d.Findings[0].Evidence, want)
	}
}
