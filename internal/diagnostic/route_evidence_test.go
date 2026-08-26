package diagnostic

import (
	"net"
	"slices"
	"testing"
)

// routeRun assembles a run whose rows carry route decisions, so the evidence
// pass has something to read without a network of any kind.
func routeRun(target, reference []RouteDecision, resolver []RouteDecision) ([]ProbeID, map[ProbeID]ProbeResult) {
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP}
	res := map[ProbeID]ProbeResult{
		ProbeIface:     {Status: StatusPass, Routes: reference},
		ProbeInternet:  {Status: StatusPass},
		ProbeDNS:       {Status: StatusPass, Addrs: []net.IP{net.ParseIP("198.51.100.7")}, Routes: resolver},
		ProbeTargetTCP: {Status: StatusFail, Routes: target},
	}
	return order, res
}

func hasEvidence(items []CausalEvidence, want CausalEvidence) bool {
	return slices.Contains(items, want)
}

// A tunnel is not a diagnosis. Route intelligence may only decorate a
// conclusion the checks already reached, so a healthy run behind a VPN must
// produce no finding at all.
func TestRouteEvidenceNeverCreatesADiagnosis(t *testing.T) {
	tunnel := tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")
	order, res := routeRun([]RouteDecision{tunnel}, []RouteDecision{decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")}, []RouteDecision{tunnel})
	for _, id := range order {
		r := res[id]
		r.Status = StatusPass
		res[id] = r
	}
	d := Interpret(&Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}, order, res)
	if len(d.Findings) != 0 {
		t.Errorf("a healthy run behind a tunnel produced findings: %+v", d.Findings)
	}
}

// The split-tunnel case: the target enters a tunnel that general traffic does
// not use. It is recorded as two locally checkable facts, one per row.
func TestRouteEvidenceRecordsASplitTunnelAsAPairOfFacts(t *testing.T) {
	target := tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")
	reference := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	order, res := routeRun([]RouteDecision{target}, []RouteDecision{reference}, nil)
	got := routeEvidence(DiagnosisTargetUnreachable, order, res)
	for _, want := range []CausalEvidence{
		{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationRouteTunneled, Value: "wg0"},
		{Kind: EvidenceSupport, Check: ProbeIface, Observation: ObservationRouteDirect, Value: "eth0"},
	} {
		if !hasEvidence(got, want) {
			t.Errorf("missing %+v in %+v", want, got)
		}
		assertEvidenceObservation(t, DiagnosisTargetUnreachable, want, order, res)
	}
}

// The other direction: everything general goes through the tunnel and this one
// destination bypasses it.
func TestRouteEvidenceRecordsATargetBypassingTheTunnel(t *testing.T) {
	target := decisionTo("198.51.100.7", "eth0", "198.51.100.0/24")
	reference := tunneled(decisionTo("1.1.1.1", "wg0", "0.0.0.0/0"), "wireguard")
	order, res := routeRun([]RouteDecision{target}, []RouteDecision{reference}, nil)
	got := routeEvidence(DiagnosisTargetUnreachable, order, res)
	for _, want := range []CausalEvidence{
		{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationRouteDirect, Value: "eth0"},
		{Kind: EvidenceSupport, Check: ProbeIface, Observation: ObservationRouteTunneled, Value: "wg0"},
	} {
		if !hasEvidence(got, want) {
			t.Errorf("missing %+v in %+v", want, got)
		}
	}
}

// A machine where everything goes through the same tunnel has no split to
// explain, and saying "there is a VPN" would be a fact about the configuration
// rather than about this destination.
func TestRouteEvidenceStaysQuietWhenEveryPathAgrees(t *testing.T) {
	tunnel := tunneled(decisionTo("198.51.100.7", "wg0", "0.0.0.0/0"), "wireguard")
	reference := tunneled(decisionTo("1.1.1.1", "wg0", "0.0.0.0/0"), "wireguard")
	order, res := routeRun([]RouteDecision{tunnel}, []RouteDecision{reference}, []RouteDecision{tunnel})
	for _, e := range routeEvidence(DiagnosisTargetUnreachable, order, res) {
		if e.Observation == ObservationRouteTunneled || e.Observation == ObservationRouteDirect {
			t.Errorf("a fully tunneled machine reported a split: %+v", e)
		}
	}
}

// The kernel answering that there is no route at all.
func TestRouteEvidenceReportsNoRoute(t *testing.T) {
	target := RouteDecision{Destination: net.ParseIP("198.51.100.7"), Family: counterfactualIPv4, Unreachable: true}
	order, res := routeRun([]RouteDecision{target}, nil, nil)
	want := CausalEvidence{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationRouteUnreachable, Value: "198.51.100.7"}
	if got := routeEvidence(DiagnosisTargetUnreachable, order, res); !hasEvidence(got, want) {
		t.Errorf("missing %+v in %+v", want, got)
	}
	assertEvidenceObservation(t, DiagnosisTargetUnreachable, want, order, res)
}

// The two families of one target leaving by different interfaces.
func TestRouteEvidenceReportsAFamilySplit(t *testing.T) {
	target := []RouteDecision{
		decisionTo("198.51.100.7", "eth0", "0.0.0.0/0"),
		tunneled(decisionTo("2001:db8::7", "wg0", "::/0"), "wireguard"),
	}
	order, res := routeRun(target, []RouteDecision{decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")}, nil)
	want := CausalEvidence{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationRouteFamilySplit}
	if got := routeEvidence(DiagnosisPartialReachability, order, res); !hasEvidence(got, want) {
		t.Errorf("missing %+v in %+v", want, got)
	}
	assertEvidenceObservation(t, DiagnosisPartialReachability, want, order, res)
}

// The resolver answering over a path the application traffic does not take.
// The item lives on the DNS row and names the other path, so it stays
// checkable from the row that recorded it.
func TestRouteEvidenceReportsSplitDNS(t *testing.T) {
	target := tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")
	resolver := decisionTo("192.168.1.1", "eth0", "192.168.1.0/24")
	order, res := routeRun([]RouteDecision{target}, []RouteDecision{decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")}, []RouteDecision{resolver})
	want := CausalEvidence{Kind: EvidenceSupport, Check: ProbeDNS, Observation: ObservationRoutePathDiffers, Value: "wg0"}
	if got := routeEvidence(DiagnosisSystemDNSFailure, order, res); !hasEvidence(got, want) {
		t.Errorf("missing %+v in %+v", want, got)
	}
	assertEvidenceObservation(t, DiagnosisSystemDNSFailure, want, order, res)
}

// Interface MTU is offered beside a measured stall and nowhere else, and never
// as a claim about the end-to-end path MTU.
func TestRouteEvidenceOffersInterfaceMTUOnlyToThePathMTUFinding(t *testing.T) {
	target := tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")
	target.MTU = 1420
	reference := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	reference.MTU = 1500
	order, res := routeRun([]RouteDecision{target}, []RouteDecision{reference}, nil)
	want := CausalEvidence{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationRouteInterfaceMTU, Value: "wg0"}
	if got := routeEvidence(DiagnosisProbablePathMTU, order, res); !hasEvidence(got, want) {
		t.Errorf("missing %+v in %+v", want, got)
	}
	assertEvidenceObservation(t, DiagnosisProbablePathMTU, want, order, res)
	for _, e := range routeEvidence(DiagnosisTargetUnreachable, order, res) {
		if e.Observation == ObservationRouteInterfaceMTU {
			t.Errorf("interface MTU offered to an unrelated finding: %+v", e)
		}
	}
}

// A platform netdoc cannot ask records nothing, which must never read as
// "there was no route".
func TestRouteEvidenceIsEmptyWithoutDecisions(t *testing.T) {
	order, res := routeRun(nil, nil, nil)
	for _, id := range []DiagnosisID{DiagnosisTargetUnreachable, DiagnosisOffline, DiagnosisSystemDNSFailure} {
		if got := routeEvidence(id, order, res); len(got) != 0 {
			t.Errorf("%s produced route evidence with no decisions: %+v", id, got)
		}
	}
}

// Evidence may only reference a row the run actually reported, since a
// consumer resolves every item against the checks in the artifact.
func TestRouteEvidenceNeverReferencesAnUnreportedRow(t *testing.T) {
	target := tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")
	reference := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	_, res := routeRun([]RouteDecision{target}, []RouteDecision{reference}, nil)
	order := []ProbeID{ProbeTargetTCP} // the iface row was never selected
	for _, e := range routeEvidence(DiagnosisTargetUnreachable, order, res) {
		if e.Check == ProbeIface {
			t.Errorf("evidence references a row this run did not report: %+v", e)
		}
	}
}

// Route evidence is appended to a finding the interpretation already reached,
// so the branch's own reasoning stays first and nothing above it moves.
func TestAttachRouteEvidenceLeavesExistingReasoningFirst(t *testing.T) {
	c := matrixCaseNamed(t, "target refuses the connection")
	before := Interpret(c.target, c.order, c.res)
	res := map[ProbeID]ProbeResult{}
	for id, r := range c.res {
		res[id] = r
	}
	targetRow := res[ProbeTargetTCP]
	targetRow.Routes = []RouteDecision{tunneled(decisionTo("198.51.100.7", "wg0", "10.20.0.0/16"), "wireguard")}
	res[ProbeTargetTCP] = targetRow
	ifaceRow := res[ProbeIface]
	ifaceRow.Routes = []RouteDecision{decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")}
	res[ProbeIface] = ifaceRow
	after := Interpret(c.target, c.order, res)
	if len(after.Findings) != len(before.Findings) || after.Findings[0].ID != before.Findings[0].ID {
		t.Fatalf("route decisions changed the diagnosis: %+v, want %+v", after.Findings, before.Findings)
	}
	if after.Verdict != before.Verdict || after.Blamed != before.Blamed {
		t.Errorf("route decisions changed the verdict: %q/%q, want %q/%q", after.Verdict, after.Blamed, before.Verdict, before.Blamed)
	}
	old := before.Findings[0].Evidence
	if got := after.Findings[0].Evidence; len(got) < len(old) || !slices.Equal(got[:len(old)], old) {
		t.Errorf("existing evidence moved:\n%+v\nwant it unchanged at the front of\n%+v", old, got)
	}
}
