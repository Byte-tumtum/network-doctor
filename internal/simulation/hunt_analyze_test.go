package simulation

import (
	"bytes"
	"context"
	"errors"
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
			DNSQueries: []DNSQueryEvidence{{Service: "r", ActualOutcome: "DROPPED"}, {Service: "r", ActualOutcome: "ANSWER"}},
			PacketConditions: []PacketConditionEvidence{{Node: "gateway", Segment: "upstream", Active: true,
				LossPercent: 20, DroppedPackets: 7}},
			Routes: []RouteEvidence{{Selected: true, GatewayReachable: boolPointer(true)}},
			Links:  []LinkEvidence{{Up: true}},
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

func familyTruthReport(ipv4, ipv6 *bool) *Report {
	report := &Report{Topology: []NodeInfo{{Name: "client", Role: "client"}}}
	if ipv4 != nil {
		report.Evidence.Reachability = append(report.Evidence.Reachability,
			ReachabilityEvidence{From: "client", Family: "ipv4", Reachable: *ipv4})
	}
	if ipv6 != nil {
		report.Evidence.Reachability = append(report.Evidence.Reachability,
			ReachabilityEvidence{From: "client", Family: "ipv6", Reachable: *ipv6})
	}
	return report
}

func TestObservedTruthUsesIndependentFamilyReachability(t *testing.T) {
	reachable, unreachable := true, false
	for _, tc := range []struct {
		name       string
		ipv4, ipv6 *bool
		want4      string
		want6      string
	}{
		{"dual stack reachable", &reachable, &reachable, "reachable", "reachable"},
		{"IPv4 broken", &unreachable, &reachable, "unreachable", "reachable"},
		{"IPv6 broken", &reachable, &unreachable, "reachable", "unreachable"},
		{"IPv4 only", &reachable, nil, "reachable", "unavailable"},
		{"IPv6 only", nil, &reachable, "unavailable", "reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truth := collectObservedTruth(huntManifest(), familyTruthReport(tc.ipv4, tc.ipv6))
			if truth.IPv4 != tc.want4 || truth.IPv6 != tc.want6 {
				t.Fatalf("family truth = IPv4 %q, IPv6 %q; want %q, %q", truth.IPv4, truth.IPv6, tc.want4, tc.want6)
			}
		})
	}
	report := familyTruthReport(&reachable, nil)
	report.Topology = append(report.Topology, NodeInfo{Name: "router", Role: "router"})
	report.Evidence.Reachability = append(report.Evidence.Reachability,
		ReachabilityEvidence{From: "router", Family: "ipv4", Reachable: false},
		ReachabilityEvidence{From: "client", To: "target.test:443", Reachable: false})
	if got := collectObservedTruth(huntManifest(), report).IPv4; got != "reachable" {
		t.Fatalf("non-family observation changed client IPv4 truth to %q", got)
	}
}

func TestObservedTruthRejectsAmbiguousFamilyEvidence(t *testing.T) {
	reachable := true
	for _, tc := range []struct {
		name        string
		observation ReachabilityEvidence
	}{
		{"duplicate", ReachabilityEvidence{From: "client", Family: "ipv4", Reachable: true}},
		{"conflicting duplicate", ReachabilityEvidence{From: "client", Family: "ipv4", Reachable: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := familyTruthReport(&reachable, nil)
			report.Evidence.Reachability = append(report.Evidence.Reachability, tc.observation)
			if got := collectObservedTruth(huntManifest(), report).IPv4; got != "unknown" {
				t.Fatalf("ambiguous IPv4 truth = %q, want unknown", got)
			}
		})
	}
}

func TestObservedTruthFamilyReachabilityIgnoresDiagnosis(t *testing.T) {
	reachable, unreachable := true, false
	a := familyTruthReport(&unreachable, &reachable)
	b := familyTruthReport(&unreachable, &reachable)
	a.Tests = []TestOutcome{{Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "internet_tcp", Families: &DiagnosisFamilies{IPv4: "reachable", IPv6: "unreachable"}}}}}}
	b.Tests = []TestOutcome{{Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "internet_tcp", Families: &DiagnosisFamilies{IPv4: "unreachable", IPv6: "reachable"}}}}}}
	if gotA, gotB := collectObservedTruth(huntManifest(), a), collectObservedTruth(huntManifest(), b); !reflect.DeepEqual(gotA, gotB) {
		t.Fatalf("diagnosis changed family truth:\n%+v\n%+v", gotA, gotB)
	}
}

func TestMutationObservedUsesIndependentFamilyTruth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutation string
		ipv4     string
		ipv6     string
		want     bool
	}{
		{"IPv4 drop observed", "family.ipv4_drop", "unreachable", "reachable", true},
		{"IPv4 remains reachable", "family.ipv4_drop", "reachable", "reachable", false},
		{"IPv4 unavailable", "family.ipv4_drop", "unavailable", "reachable", false},
		{"IPv4 unknown", "family.ipv4_drop", "unknown", "reachable", false},
		{"IPv4 wrong family", "family.ipv4_drop", "reachable", "unreachable", false},
		{"IPv6 drop observed", "family.ipv6_drop", "reachable", "unreachable", true},
		{"IPv6 remains reachable", "family.ipv6_drop", "reachable", "reachable", false},
		{"IPv6 unavailable", "family.ipv6_drop", "reachable", "unavailable", false},
		{"IPv6 unknown", "family.ipv6_drop", "reachable", "unknown", false},
		{"IPv6 wrong family", "family.ipv6_drop", "unreachable", "reachable", false},
		{"unrelated mutation", "quic.udp_443_block", "reachable", "unreachable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truth := ObservedTruth{IPv4: tc.ipv4, IPv6: tc.ipv6}
			if got := mutationObserved(GeneratedMutation{ID: tc.mutation}, &Report{}, truth); got != tc.want {
				t.Fatalf("mutationObserved(%q, IPv4=%q, IPv6=%q) = %t, want %t", tc.mutation, tc.ipv4, tc.ipv6, got, tc.want)
			}
		})
	}
}

func TestMutationObservedUsesIndependentProtocolServiceEvidence(t *testing.T) {
	serviceState := func(item ServiceStateEvidence) Report {
		return Report{Evidence: Evidence{ServiceStates: []ServiceStateEvidence{item}}}
	}
	socksRequest := func(item SOCKSEvidence) Report {
		return Report{Evidence: Evidence{SOCKSRequests: []SOCKSEvidence{item}}}
	}
	drop := func(node, protocol string, port int, direction string) Report {
		return Report{Faults: []FaultInfo{{Type: FaultDrop, Node: node, Protocol: protocol, Port: port, Direction: direction}}}
	}
	tests := []struct {
		name        string
		mutation    GeneratedMutation
		positive    Report
		wrongObject Report
		wrongState  Report
	}{
		{
			name:     "TCP reset",
			mutation: GeneratedMutation{ID: "service.tcp_reset", Node: "target", TargetPort: 80},
			positive: Report{Evidence: Evidence{TCPResets: []TCPResetEvidence{{Node: "target", Service: "http-target",
				Event: "reset", Result: "connection_reset", Count: 1}}}},
			wrongObject: Report{Evidence: Evidence{TCPResets: []TCPResetEvidence{{Node: "other", Service: "http-target",
				Event: "reset", Result: "connection_reset", Count: 1}}}},
			wrongState: Report{Evidence: Evidence{TCPResets: []TCPResetEvidence{{Node: "target", Service: "http-target",
				Event: "accepted", Result: "connected", Count: 1}}}},
		},
		{
			name:     "expired TLS certificate",
			mutation: GeneratedMutation{ID: "service.tls_expired", Node: "target", Service: "tls-target"},
			positive: serviceState(ServiceStateEvidence{Node: "target", Service: "tls-target", Type: ServiceTLS,
				Port: 443, Mode: TLSCertificateExpired}),
			wrongObject: serviceState(ServiceStateEvidence{Node: "other", Service: "tls-target", Type: ServiceTLS,
				Port: 443, Mode: TLSCertificateExpired}),
			wrongState: serviceState(ServiceStateEvidence{Node: "target", Service: "tls-target", Type: ServiceTLS,
				Port: 443, Mode: TLSCertificateValid}),
		},
		{
			name: "proxy CONNECT refused",
			mutation: GeneratedMutation{ID: "proxy.connect_refused", Node: "proxy", TargetNode: "internet",
				Service: "socks-proxy", TargetPort: 443},
			positive: socksRequest(SOCKSEvidence{Node: "proxy", Service: "socks-proxy", Event: "connect",
				AddressType: "domain", Destination: proxyCONNECTTarget, Port: 443, Result: "connection_refused", Count: 1}),
			wrongObject: socksRequest(SOCKSEvidence{Node: "proxy", Service: "other-proxy", Event: "connect",
				AddressType: "domain", Destination: proxyCONNECTTarget, Port: 443, Result: "connection_refused", Count: 1}),
			wrongState: socksRequest(SOCKSEvidence{Node: "proxy", Service: "socks-proxy", Event: "connect",
				AddressType: "domain", Destination: proxyCONNECTTarget, Port: 443, Result: "connected", Count: 1}),
		},
		{
			name: "QUIC UDP 443 blocked",
			mutation: GeneratedMutation{ID: "quic.udp_443_block", Node: "internet",
				Service: quicProbeService, TargetPort: 443},
			positive:    drop("internet", "udp", 443, DirectionInbound),
			wrongObject: drop("other", "udp", 443, DirectionInbound),
			wrongState:  drop("internet", "tcp", 443, DirectionInbound),
		},
		{
			name:     "invalid DoH response",
			mutation: GeneratedMutation{ID: "encrypted_dns.doh_invalid", Node: "internet", Service: encryptedDNSProbeService},
			positive: serviceState(ServiceStateEvidence{Node: "internet", Service: encryptedDNSProbeService,
				Type: ServiceEncryptedDNS, Port: 443, Mode: DoHResponseInvalid}),
			wrongObject: serviceState(ServiceStateEvidence{Node: "internet", Service: "other-doh",
				Type: ServiceEncryptedDNS, Port: 443, Mode: DoHResponseInvalid}),
			wrongState: serviceState(ServiceStateEvidence{Node: "internet", Service: encryptedDNSProbeService,
				Type: ServiceEncryptedDNS, Port: 443}),
		},
		{
			name:        "HTTP status 503",
			mutation:    GeneratedMutation{ID: "http.status_503", Node: "target", TargetPort: 80, Status: 503},
			positive:    serviceState(ServiceStateEvidence{Node: "target", Type: ServiceHTTP, Port: 80, Status: 503}),
			wrongObject: serviceState(ServiceStateEvidence{Node: "target", Type: ServiceHTTP, Port: 81, Status: 503}),
			wrongState:  serviceState(ServiceStateEvidence{Node: "target", Type: ServiceHTTP, Port: 80, Status: 200}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if mutationObserved(tc.mutation, &Report{}, ObservedTruth{}) {
				t.Fatal("manifest-only mutation counted as observed")
			}
			if !mutationObserved(tc.mutation, &tc.positive, ObservedTruth{}) {
				t.Fatal("matching independent evidence did not observe mutation")
			}
			if mutationObserved(tc.mutation, &tc.wrongObject, ObservedTruth{}) {
				t.Fatal("evidence for the wrong object counted as observed")
			}
			if mutationObserved(tc.mutation, &tc.wrongState, ObservedTruth{}) {
				t.Fatal("healthy or different state counted as observed")
			}

			truth := collectObservedTruth(huntManifest(tc.mutation), &tc.positive)
			if !reflect.DeepEqual(truth.ObservedFaults, []string{tc.mutation.ID}) {
				t.Fatalf("observed faults = %v, want %q", truth.ObservedFaults, tc.mutation.ID)
			}
			withoutObservation := truth
			withoutObservation.ObservedFaults = []string{}
			if truthFingerprint(truth) == truthFingerprint(withoutObservation) {
				t.Fatal("observed mutation did not change truth fingerprint")
			}
		})
	}
}

func TestProtocolServiceMutationObservationIgnoresDiagnosis(t *testing.T) {
	mutations := []struct {
		mutation GeneratedMutation
		report   Report
	}{
		{
			GeneratedMutation{ID: "service.tls_expired", Node: "target", Service: "tls-target"},
			Report{Evidence: Evidence{ServiceStates: []ServiceStateEvidence{{
				Node: "target", Service: "tls-target", Type: ServiceTLS, Port: 443, Mode: TLSCertificateExpired,
			}}}},
		},
		{
			GeneratedMutation{ID: "http.status_503", Node: "target", TargetPort: 80, Status: 503},
			Report{Evidence: Evidence{ServiceStates: []ServiceStateEvidence{{
				Node: "target", Type: ServiceHTTP, Port: 80, Status: 503,
			}}}},
		},
	}
	for _, tc := range mutations {
		a, b := tc.report, tc.report
		a.Tests = []TestOutcome{{Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{Status: "PASS"}}}}}
		b.Tests = []TestOutcome{{Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{Status: "FAIL"}}}}}
		if !mutationObserved(tc.mutation, &a, ObservedTruth{}) || !mutationObserved(tc.mutation, &b, ObservedTruth{}) {
			t.Fatalf("diagnosis changed observation for %s", tc.mutation.ID)
		}
	}
}

func TestMutationObservedMatchesExactNetemDNSTimelineAndLinkEvidence(t *testing.T) {
	packet := func(item PacketConditionEvidence) Report {
		return Report{Evidence: Evidence{PacketConditions: []PacketConditionEvidence{item}}}
	}
	event := func(item TimedEvent) Report {
		return Report{Timeline: []FaultEventEvidence{{Event: item, Result: EventApplied}}}
	}
	tests := []struct {
		name       string
		mutation   GeneratedMutation
		positive   Report
		nearMisses []Report
	}{
		{
			name: "persistent loss",
			mutation: GeneratedMutation{ID: "netem.loss", Node: "gateway", Segment: "upstream",
				LossPercent: 17, NetemSeed: 11},
			positive: packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
				LossPercent: 17, Seed: 11}),
			nearMisses: []Report{
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", LossPercent: 17, Seed: 11}),
				packet(PacketConditionEvidence{Node: "other", Segment: "upstream", Active: true, LossPercent: 17, Seed: 11}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "other", Active: true, LossPercent: 17, Seed: 11}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true, LossPercent: 18, Seed: 11}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true, LossPercent: 17, Seed: 12}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true, Latency: 17 * time.Millisecond}),
			},
		},
		{
			name:     "persistent latency",
			mutation: GeneratedMutation{ID: "netem.latency", Node: "gateway", Segment: "upstream", LatencyMS: 250},
			positive: packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
				Latency: 250 * time.Millisecond}),
			nearMisses: []Report{
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Latency: 250 * time.Millisecond}),
				packet(PacketConditionEvidence{Node: "other", Segment: "upstream", Active: true, Latency: 250 * time.Millisecond}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "other", Active: true, Latency: 250 * time.Millisecond}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true, Latency: 300 * time.Millisecond}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true, LossPercent: 25}),
			},
		},
		{
			name: "persistent jitter",
			mutation: GeneratedMutation{ID: "netem.jitter", Node: "gateway", Segment: "upstream",
				LatencyMS: 250, JitterMS: 40, NetemSeed: 12},
			positive: packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
				Latency: 250 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 12}),
			nearMisses: []Report{
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream",
					Latency: 250 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 12}),
				packet(PacketConditionEvidence{Node: "other", Segment: "upstream", Active: true,
					Latency: 250 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 12}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "other", Active: true,
					Latency: 250 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 12}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
					Latency: 250 * time.Millisecond, Jitter: 60 * time.Millisecond, Seed: 12}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
					Latency: 250 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 13}),
				packet(PacketConditionEvidence{Node: "gateway", Segment: "upstream", Active: true,
					Latency: 250 * time.Millisecond}),
			},
		},
		{
			name: "timeline netem impairment",
			mutation: GeneratedMutation{ID: "timeline.netem_spike", Node: "gateway", Segment: "upstream",
				StartMS: 200, DurationMS: 800, LatencyMS: 400, LossPercent: 17, NetemSeed: 13},
			positive: event(TimedEvent{Offset: 200 * time.Millisecond, Type: FaultScheduledNetem,
				Node: "gateway", Segment: "upstream", Latency: 400 * time.Millisecond, LossPercent: 17, NetemSeed: 13}),
			nearMisses: []Report{
				event(TimedEvent{Type: FaultScheduledNetem, Node: "gateway", Segment: "upstream", NetemSeed: 13}),
				event(TimedEvent{Offset: time.Second, Type: FaultScheduledNetem, Node: "gateway", Segment: "upstream", NetemSeed: 13}),
				event(TimedEvent{Offset: 200 * time.Millisecond, Type: FaultScheduledNetem,
					Node: "gateway", Segment: "upstream", Latency: 450 * time.Millisecond, LossPercent: 17, NetemSeed: 13}),
				event(TimedEvent{Offset: 200 * time.Millisecond, Type: FaultScheduledNetem,
					Node: "gateway", Segment: "upstream", Latency: 400 * time.Millisecond, LossPercent: 17, NetemSeed: 14}),
				event(TimedEvent{Offset: 200 * time.Millisecond, Type: FaultScheduledNetem,
					Node: "gateway", Segment: "other", Latency: 400 * time.Millisecond, LossPercent: 17, NetemSeed: 13}),
			},
		},
		{
			name:     "DNS SERVFAIL",
			mutation: GeneratedMutation{ID: "dns.servfail", Service: "resolver"},
			positive: event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeSERVFAIL}),
			nearMisses: []Report{
				event(TimedEvent{Type: FaultScheduledDNS, Service: "other", Outcome: DNSOutcomeSERVFAIL}),
				event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeDrop}),
				event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeAnswer}),
			},
		},
		{
			name:     "DNS drop",
			mutation: GeneratedMutation{ID: "dns.drop", Service: "resolver"},
			positive: event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeDrop}),
			nearMisses: []Report{
				event(TimedEvent{Type: FaultScheduledDNS, Service: "other", Outcome: DNSOutcomeDrop}),
				event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeSERVFAIL}),
				event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeAnswer}),
			},
		},
		{
			name: "timeline DNS outage",
			mutation: GeneratedMutation{ID: "timeline.dns_outage", Service: "resolver",
				StartMS: 150, DurationMS: 700},
			positive: event(TimedEvent{Offset: 150 * time.Millisecond, Type: FaultScheduledDNS,
				Service: "resolver", Outcome: DNSOutcomeDrop}),
			nearMisses: []Report{
				event(TimedEvent{Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeDelay, Delay: 200 * time.Millisecond}),
				event(TimedEvent{Offset: 850 * time.Millisecond, Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeAnswer}),
				event(TimedEvent{Offset: 150 * time.Millisecond, Type: FaultScheduledDNS, Service: "other", Outcome: DNSOutcomeDrop}),
				event(TimedEvent{Offset: 150 * time.Millisecond, Type: FaultScheduledDNS, Service: "resolver", Outcome: DNSOutcomeSERVFAIL}),
			},
		},
		{
			name:     "transient link down",
			mutation: GeneratedMutation{ID: "link.transient_down", Node: "gateway", Segment: "upstream", DurationMS: 700},
			positive: event(TimedEvent{Type: FaultScheduledLink, Node: "gateway", Segment: "upstream", State: LinkStateDown}),
			nearMisses: []Report{
				event(TimedEvent{Offset: 700 * time.Millisecond, Type: FaultScheduledLink,
					Node: "gateway", Segment: "upstream", State: LinkStateUp}),
				event(TimedEvent{Type: FaultScheduledLink, Node: "other", Segment: "upstream", State: LinkStateDown}),
				event(TimedEvent{Type: FaultScheduledLink, Node: "gateway", Segment: "other", State: LinkStateDown}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if mutationObserved(tc.mutation, &Report{}, ObservedTruth{}) {
				t.Fatal("manifest-only mutation counted as observed")
			}
			if !mutationObserved(tc.mutation, &tc.positive, ObservedTruth{}) {
				t.Fatal("matching independent evidence did not observe mutation")
			}
			for i := range tc.nearMisses {
				if mutationObserved(tc.mutation, &tc.nearMisses[i], ObservedTruth{}) {
					t.Errorf("near miss %d counted as observed: %+v", i, tc.nearMisses[i])
				}
			}
			failed := tc.positive
			if len(failed.Timeline) > 0 {
				failed.Timeline = append([]FaultEventEvidence(nil), failed.Timeline...)
				failed.Timeline[0].Result = EventFailed
				if mutationObserved(tc.mutation, &failed, ObservedTruth{}) {
					t.Error("failed event counted as observed")
				}
			}
			truth := collectObservedTruth(huntManifest(tc.mutation), &tc.positive)
			if !reflect.DeepEqual(truth.ObservedFaults, []string{tc.mutation.ID}) {
				t.Fatalf("observed faults = %v, want %q", truth.ObservedFaults, tc.mutation.ID)
			}
			withoutObservation := truth
			withoutObservation.ObservedFaults = []string{}
			if truthFingerprint(truth) == truthFingerprint(withoutObservation) {
				t.Fatal("observed mutation did not change truth fingerprint")
			}
		})
	}
}

func preferredPathReport() Report {
	unreachable, reachable := false, true
	return Report{
		Topology: []NodeInfo{
			{Name: "client", Role: "client", Routes: []RouteInfo{
				{Destination: "default", Via: "10.79.1.1", Segment: "preferred-lan", Metric: 50, Family: "ipv4"},
				{Destination: "default", Via: "10.79.3.1", Segment: "alternate-lan", Metric: 100, Family: "ipv4"},
			}},
			{Name: "preferred-gateway", Role: "router", Interfaces: []InterfaceInfo{
				{Segment: "preferred-lan", IPv4: "10.79.1.1/24"},
				{Segment: "preferred-upstream", IPv4: "10.79.2.1/24"},
			}},
			{Name: "alternate-gateway", Role: "router", Interfaces: []InterfaceInfo{
				{Segment: "alternate-lan", IPv4: "10.79.3.1/24"},
				{Segment: "alternate-upstream", IPv4: "10.79.4.1/24"},
			}},
		},
		Evidence: Evidence{
			Routes: []RouteEvidence{
				{Node: "client", Destination: "1.1.1.1", Via: "10.79.1.1", Segment: "preferred-lan",
					Metric: 50, Family: "ipv4", Selected: true, GatewayReachable: &reachable},
				{Node: "client", Destination: "default", Via: "10.79.3.1", Segment: "alternate-lan",
					Metric: 100, Family: "ipv4", GatewayReachable: &reachable},
				{Node: "client", Destination: "9.9.9.9", Via: "10.79.3.1", Segment: "alternate-lan",
					Family: "ipv4", Selected: true, GatewayReachable: &reachable},
			},
			Links: []LinkEvidence{
				{Node: "preferred-gateway", Segment: "preferred-lan", Up: true},
				{Node: "preferred-gateway", Segment: "preferred-upstream", Up: false},
				{Node: "alternate-gateway", Segment: "alternate-lan", Up: true},
				{Node: "alternate-gateway", Segment: "alternate-upstream", Up: true},
			},
			Routers: []RouterEvidence{
				{Node: "preferred-gateway", IPv4Forwarding: true},
				{Node: "alternate-gateway", IPv4Forwarding: true},
			},
			Reachability: []ReachabilityEvidence{
				{From: "client", To: "IPv4 internet endpoints", Family: "ipv4",
					Via: []string{"preferred-lan", "10.79.1.1"}, Reachable: unreachable},
				{From: "client", To: "9.9.9.9:80", Via: []string{"alternate-lan", "10.79.3.1"}, Reachable: reachable},
			},
		},
	}
}

func TestPreferredPathMutationObservedFromIndependentPathConsequence(t *testing.T) {
	mutation := GeneratedMutation{ID: "routing.preferred_path_failure", Node: "preferred-gateway",
		TargetNode: "client", Segment: "preferred-upstream", Family: "ipv4"}
	positive := preferredPathReport()
	if !mutationObserved(mutation, &positive, ObservedTruth{IPv4: "unreachable"}) {
		t.Fatal("matching selected-route, failed-path, and alternate-path evidence did not observe mutation")
	}
	if mutationObserved(mutation, &Report{}, ObservedTruth{}) {
		t.Fatal("manifest-only mutation counted as observed")
	}
	appliedOnly := Report{Faults: []FaultInfo{{Type: FaultLinkDown, Node: mutation.Node}}}
	if mutationObserved(mutation, &appliedOnly, ObservedTruth{IPv4: "unreachable"}) {
		t.Fatal("applied link-down bookkeeping counted as the routing consequence")
	}
	wrongMutation := mutation
	wrongMutation.Node = "other-gateway"
	if mutationObserved(wrongMutation, &positive, ObservedTruth{IPv4: "unreachable"}) {
		t.Fatal("path evidence for another router counted as observed")
	}
	wrongMutation = mutation
	wrongMutation.Segment = "other-upstream"
	if mutationObserved(wrongMutation, &positive, ObservedTruth{IPv4: "unreachable"}) {
		t.Fatal("path evidence for another upstream segment counted as observed")
	}

	nearMisses := map[string]func(*Report){
		"alternate selected": func(report *Report) {
			report.Evidence.Routes[0].Via, report.Evidence.Routes[0].Segment = "10.79.3.1", "alternate-lan"
			report.Evidence.Reachability[0].Via = []string{"alternate-lan", "10.79.3.1"}
		},
		"wrong gateway": func(report *Report) {
			report.Evidence.Routes[0].Via = "10.79.1.254"
			report.Evidence.Reachability[0].Via[1] = "10.79.1.254"
		},
		"wrong selected segment": func(report *Report) {
			report.Evidence.Routes[0].Segment = "other-lan"
			report.Evidence.Reachability[0].Via[0] = "other-lan"
		},
		"targeted link remains up":         func(report *Report) { report.Evidence.Links[1].Up = true },
		"preferred path remains reachable": func(report *Report) { report.Evidence.Reachability[0].Reachable = true },
		"alternate route absent": func(report *Report) {
			report.Topology[0].Routes = report.Topology[0].Routes[:1]
			report.Evidence.Routes = report.Evidence.Routes[:1]
			report.Evidence.Reachability = report.Evidence.Reachability[:1]
		},
		"alternate transport unavailable": func(report *Report) { report.Evidence.Reachability[1].Reachable = false },
		"wrong family": func(report *Report) {
			report.Evidence.Routes[0].Family = "ipv6"
			report.Evidence.Reachability[0].Family = "ipv6"
		},
	}
	for name, change := range nearMisses {
		t.Run(name, func(t *testing.T) {
			report := preferredPathReport()
			change(&report)
			if mutationObserved(mutation, &report, ObservedTruth{IPv4: "unreachable"}) {
				t.Fatal("near-miss route evidence counted as observed")
			}
		})
	}

	truth := collectObservedTruth(huntManifest(mutation), &positive)
	if !reflect.DeepEqual(truth.ObservedFaults, []string{mutation.ID}) {
		t.Fatalf("observed faults = %v, want %q", truth.ObservedFaults, mutation.ID)
	}
}

func TestTruthFingerprintIncludesFamilyReachability(t *testing.T) {
	base := ObservedTruth{DNS: "available", IPv4: "reachable", IPv6: "reachable", Gateway: "reachable",
		Proxy: "unknown", TLS: "unknown", TCP: "unknown", Link: "up", Packet: "unknown", Route: "selected", ObservedFaults: []string{}}
	equivalent := base
	if truthFingerprint(base) != truthFingerprint(equivalent) {
		t.Fatal("identical truth did not produce a stable fingerprint")
	}
	for _, tc := range []struct {
		name   string
		change func(*ObservedTruth)
	}{
		{"IPv4 unreachable", func(truth *ObservedTruth) { truth.IPv4 = "unreachable" }},
		{"IPv6 unreachable", func(truth *ObservedTruth) { truth.IPv6 = "unreachable" }},
		{"IPv4 unavailable", func(truth *ObservedTruth) { truth.IPv4 = "unavailable" }},
		{"non-family truth", func(truth *ObservedTruth) { truth.DNS = "unavailable" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.change(&changed)
			if truthFingerprint(base) == truthFingerprint(changed) {
				t.Fatalf("truth change did not alter fingerprint: %+v", changed)
			}
		})
	}
	unavailable, unreachable := base, base
	unavailable.IPv6, unreachable.IPv6 = "unavailable", "unreachable"
	if truthFingerprint(unavailable) == truthFingerprint(unreachable) {
		t.Fatal("unavailable and unreachable IPv6 produced the same fingerprint")
	}
}

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

func TestObservedTruthRequiresAnActualTCPReset(t *testing.T) {
	accepted := &Report{Evidence: Evidence{TCPResets: []TCPResetEvidence{{Event: "accepted", Result: "connected", Count: 1}}}}
	if truth := collectObservedTruth(huntManifest(), accepted); truth.TCP != "unknown" {
		t.Fatalf("accepted connection counted as a reset: %+v", truth)
	}
	reset := &Report{Evidence: Evidence{TCPResets: []TCPResetEvidence{{Event: "reset", Result: "connection_reset", Count: 1}}}}
	if truth := collectObservedTruth(huntManifest(), reset); truth.TCP != "reset" {
		t.Fatalf("recorded connection reset was not observed: %+v", truth)
	}
}

func TestObservedTruthTLSUsesHandshakeResultVocabulary(t *testing.T) {
	passed := []TLSEvidence{{Result: tlsHandshakeResult(nil, true)}}
	if truth := collectObservedTruth(huntManifest(), &Report{Evidence: Evidence{TLS: passed}}); truth.TLS != "observed" {
		t.Fatalf("successful handshake = %q, want observed", truth.TLS)
	}
	broken := []TLSEvidence{{Result: "handshake_failure"}}
	if truth := collectObservedTruth(huntManifest(), &Report{Evidence: Evidence{TLS: broken}}); truth.TLS != "failed" {
		t.Fatalf("failed handshake = %q, want failed", truth.TLS)
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

func familyDiagnosisReport(ipv4, ipv6 string) *Report {
	return &Report{Cleanup: CleanupInfo{Done: true}, Topology: []NodeInfo{{Name: "client", Role: "client"}},
		Tests: []TestOutcome{{Name: "netdoc", Node: "client", ProcessOutcome: ProcessExited,
			Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "internet_tcp",
				Families: &DiagnosisFamilies{IPv4: ipv4, IPv6: ipv6}}}}}}, Suggestions: []Suggestion{}}
}

func familyMismatchFindings(findings []HuntCaseFinding) []HuntCaseFinding {
	var out []HuntCaseFinding
	for _, finding := range findings {
		if finding.Code == "family_reachability_mismatch" {
			out = append(out, finding)
		}
	}
	return out
}

func TestHuntAnalysisComparesFamilyTruthWithDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		truth4, truth6           string
		diagnosis4, diagnosis6   string
		wantCategory, wantFamily string
		wantExpected, wantActual string
	}{
		{"IPv4 false negative", "unreachable", "reachable", "reachable", "reachable", FindingFalseNegative, "ipv4", "unreachable", "reachable"},
		{"IPv6 false negative", "reachable", "unreachable", "reachable", "reachable", FindingFalseNegative, "ipv6", "unreachable", "reachable"},
		{"IPv4 contradiction", "reachable", "reachable", "unreachable", "reachable", FindingDiagnosticContradiction, "ipv4", "reachable", "unreachable"},
		{"IPv6 contradiction", "reachable", "reachable", "reachable", "unreachable", FindingDiagnosticContradiction, "ipv6", "reachable", "unreachable"},
		{"reachable agreement", "reachable", "reachable", "reachable", "reachable", "", "", "", ""},
		{"unreachable agreement", "unreachable", "reachable", "unreachable", "reachable", "", "", "", ""},
		{"unreachable unreported", "unreachable", "reachable", "", "reachable", FindingFalseNegative, "ipv4", "unreachable", "unreported"},
		{"unavailable unreported", "unavailable", "reachable", "", "reachable", "", "", "", ""},
		{"unavailable reported reachable is outside reachability comparison", "unavailable", "reachable", "reachable", "reachable", "", "", "", ""},
		{"unavailable reported unreachable is outside reachability comparison", "unavailable", "reachable", "unreachable", "reachable", "", "", "", ""},
		{"unknown is not an oracle", "unknown", "reachable", "unreachable", "reachable", "", "", "", ""},
		{"dual-stack mixed isolates IPv6", "reachable", "unreachable", "reachable", "reachable", FindingFalseNegative, "ipv6", "unreachable", "reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truth := ObservedTruth{IPv4: tc.truth4, IPv6: tc.truth6}
			findings := familyMismatchFindings(analyzeHuntCase(huntManifest(), familyDiagnosisReport(tc.diagnosis4, tc.diagnosis6), truth))
			if tc.wantCategory == "" {
				if len(findings) != 0 {
					t.Fatalf("family findings = %+v, want none", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("family findings = %+v, want one", findings)
			}
			finding := findings[0]
			if finding.Category != tc.wantCategory || finding.Family != tc.wantFamily ||
				finding.Expected != tc.wantExpected || finding.Actual != tc.wantActual || finding.Probe != "internet_tcp" {
				t.Fatalf("family finding = %+v", finding)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*Report)
	}{
		{"families object absent", func(report *Report) {
			report.Tests[0].Diagnosis.Checks[0].Families = nil
		}},
		{"internet check absent", func(report *Report) {
			report.Tests[0].Diagnosis.Checks = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := familyDiagnosisReport("reachable", "reachable")
			tc.change(report)
			findings := familyMismatchFindings(analyzeHuntCase(huntManifest(), report,
				ObservedTruth{IPv4: "unreachable", IPv6: "reachable"}))
			if len(findings) != 1 || findings[0].Family != "ipv4" || findings[0].Actual != "unreported" {
				t.Fatalf("absent family diagnosis findings = %+v", findings)
			}
		})
	}
}

func TestHuntFamilyMismatchIsGenericAndExpectationIndependent(t *testing.T) {
	truth := ObservedTruth{IPv4: "reachable", IPv6: "unreachable"}
	report := familyDiagnosisReport("reachable", "reachable")
	first := familyMismatchFindings(analyzeHuntCase(huntManifest(), report, truth))
	if len(first) != 1 || first[0].Family != "ipv6" {
		t.Fatalf("mutation-free family findings = %+v", first)
	}
	report.Tests[0].ExpectedVerdict = "deliberately-wrong"
	report.Tests[0].Checks = []CheckComparison{{ID: "internet_tcp", ExpectedIPv6: "reachable", ActualIPv6: "reachable", Outcome: OutcomeMatched}}
	second := familyMismatchFindings(analyzeHuntCase(huntManifest(), report, truth))
	if len(second) != 1 || second[0].Fingerprint != first[0].Fingerprint {
		t.Fatalf("scenario expectation changed generic finding:\n%+v\n%+v", first, second)
	}
}

func TestHuntFamilyMismatchKeepsMutationObservationSeparate(t *testing.T) {
	mutation := GeneratedMutation{ID: "family.ipv6_drop"}
	manifest := huntManifest(mutation)
	truth := ObservedTruth{IPv4: "reachable", IPv6: "unreachable"}
	report := familyDiagnosisReport("reachable", "reachable")
	if !mutationObserved(mutation, report, truth) {
		t.Fatal("independent IPv6 consequence did not observe the mutation")
	}
	findings := familyMismatchFindings(analyzeHuntCase(manifest, report, truth))
	if len(findings) != 1 || findings[0].Category != FindingFalseNegative || findings[0].Family != "ipv6" {
		t.Fatalf("observed mutation with missed consequence = %+v", findings)
	}
}

func TestHuntFamilyMismatchExcludesSampledAndTransientPaths(t *testing.T) {
	truth := ObservedTruth{IPv4: "reachable", IPv6: "reachable"}
	for _, tc := range []struct {
		name   string
		change func(*Report)
	}{
		{"netem", func(report *Report) { report.Faults = []FaultInfo{{Type: FaultNetem, LossPercent: 20}} }},
		{"timed netem", func(report *Report) {
			report.Timeline = []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledNetem, LossPercent: 100}, Result: EventApplied}}
		}},
		{"transient link", func(report *Report) {
			report.Timeline = []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledLink, State: LinkStateDown}, Result: EventApplied}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := familyDiagnosisReport("unreachable", "reachable")
			tc.change(report)
			if findings := familyMismatchFindings(analyzeHuntCase(huntManifest(), report, truth)); len(findings) != 0 {
				t.Fatalf("final-state comparison overclaimed a changing/sampled path: %+v", findings)
			}
		})
	}
	for _, event := range []TimedEvent{
		{Type: FaultScheduledNetem},
		{Type: FaultScheduledLink, State: LinkStateUp},
	} {
		report := familyDiagnosisReport("unreachable", "reachable")
		report.Timeline = []FaultEventEvidence{{Event: event, Result: EventApplied}}
		if findings := familyMismatchFindings(analyzeHuntCase(huntManifest(), report, truth)); len(findings) != 1 {
			t.Fatalf("non-impairing timeline event suppressed final-state comparison: %+v", findings)
		}
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
	b = a
	b.Family = "ipv6"
	if huntFindingFingerprint(a) == huntFindingFingerprint(b) {
		t.Error("family change did not alter finding fingerprint")
	}
	b = a
	b.Expected, b.Actual = "reachable", "unreachable"
	if huntFindingFingerprint(a) == huntFindingFingerprint(b) {
		t.Error("mismatch direction did not alter finding fingerprint")
	}
}

func TestHuntFamilyMismatchAggregatesByFamilyAndDirection(t *testing.T) {
	manifestA, manifestB := huntManifest(), huntManifest()
	manifestA.Case, manifestB.Case = 3, 8
	ipv4 := familyMismatchFindings(analyzeHuntCase(manifestA, familyDiagnosisReport("reachable", "reachable"),
		ObservedTruth{IPv4: "unreachable", IPv6: "reachable"}))
	ipv6 := familyMismatchFindings(analyzeHuntCase(manifestA, familyDiagnosisReport("reachable", "reachable"),
		ObservedTruth{IPv4: "reachable", IPv6: "unreachable"}))
	contradiction := familyMismatchFindings(analyzeHuntCase(manifestA, familyDiagnosisReport("unreachable", "reachable"),
		ObservedTruth{IPv4: "reachable", IPv6: "reachable"}))
	if len(ipv4) != 1 || len(ipv6) != 1 || len(contradiction) != 1 ||
		ipv4[0].Fingerprint == ipv6[0].Fingerprint || ipv4[0].Fingerprint == contradiction[0].Fingerprint {
		t.Fatalf("family/direction fingerprints collapsed: IPv4=%+v IPv6=%+v contradiction=%+v", ipv4, ipv6, contradiction)
	}
	repeated := ipv6[0]
	repeated.Reproduce = reproductionFor(manifestB)
	findings := aggregateHuntFindings([]HuntCaseResult{
		{Manifest: manifestA, Findings: ipv6},
		{Manifest: manifestB, Findings: []HuntCaseFinding{repeated}},
	})
	if len(findings) != 1 || findings[0].Occurrences != 2 || !reflect.DeepEqual(findings[0].ExampleCases, []int{3, 8}) {
		t.Fatalf("repeated family mismatch did not aggregate: %+v", findings)
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
	if item := result.Cases[0]; item.TruthFingerprint != "" || item.DiagnosisFingerprint.ID != "" {
		t.Errorf("runtime failure analyzed incomplete evidence: %+v", item)
	}
}

func TestHuntAnalysisStopsAfterRuntimeFailure(t *testing.T) {
	manifest := huntManifest(GeneratedMutation{ID: "dns.drop", Service: "r"})
	report := &Report{Cleanup: CleanupInfo{Errors: []string{"evidence recorder failed"}},
		Tests: []TestOutcome{{StartOffset: 0, EndOffset: time.Second,
			Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS"}}}}},
		Evidence: Evidence{DNSQueries: []DNSQueryEvidence{{Service: "r", ActualOutcome: "DROPPED", Offset: time.Millisecond}}}}
	findings := analyzeHuntCase(manifest, report, collectObservedTruth(manifest, report))
	if len(findings) != 1 || findings[0].Code != "simulation_cleanup_failed" {
		t.Fatalf("findings from incomplete evidence = %+v", findings)
	}
}

// A hunt that could not run says so in one sentence, and the sentence names
// the reason. The per-case report is where the runner records it, and a caller
// that prints only the summary — the nightly triage — used to get "runtime" and
// nothing else, which is a failure nobody can act on from a CI log.
func TestRunHuntReportsWhyACaseFailed(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	result := RunHunt(context.Background(), "healthy-routed-network", base, func() Backend {
		return &fakeBackend{caps: supported(), env: &fakeEnv{stdout: okReport,
			faultErr: errors.New("this tc has no `netem seed`")}}
	}, HuntOptions{Cases: 1, Seed: 7, MaxFaults: 1, Run: Options{Netdoc: "netdoc"}})
	if result.Result != HuntResultError || result.ErrorKind != "runtime" {
		t.Fatalf("result = %s, kind = %s", result.Result, result.ErrorKind)
	}
	if !strings.Contains(result.Error, "netem seed") || !strings.Contains(result.Error, "case 0") {
		t.Errorf("error = %q, want the failing case and its reason", result.Error)
	}
	var text bytes.Buffer
	result.WriteText(&text)
	if !strings.Contains(text.String(), "netem seed") {
		t.Errorf("report = %s", text.String())
	}
}

// huntCaseResult finalizes one case the way RunHunt does for a completed run:
// truth and both fingerprints come from production code, so the detector sees
// exactly the values a real hunt hands it.
func huntCaseResult(caseNumber int, manifest GeneratedCaseManifest, report *Report) HuntCaseResult {
	manifest.Case = caseNumber
	manifest.CaseFingerprint = huntCaseFingerprint(manifest)
	truth := collectObservedTruth(manifest, report)
	return HuntCaseResult{Manifest: manifest, Truth: truth, TruthFingerprint: truthFingerprint(truth),
		DiagnosisFingerprint: diagnosisFingerprint(report), Status: "clean"}
}

// huntDNSReport is a finished run of a case whose resolver mutation was applied.
// The query outcome moves observed truth; the check status moves the structured
// diagnosis. Varying them independently is what separates the two halves of the
// instability question.
func huntDNSReport(queryOutcome, checkStatus string) *Report {
	return &Report{Cleanup: CleanupInfo{Done: true},
		Timeline: []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledDNS, Service: "r", Outcome: DNSOutcomeDrop}, Result: EventApplied}},
		Tests: []TestOutcome{{Name: "netdoc", ProcessOutcome: ProcessExited, EndOffset: time.Second,
			Diagnosis: &Diagnosis{Verdict: "diagnosed", Checks: []DiagnosisCheck{{ID: "dns", Status: checkStatus}}}}},
		Evidence: Evidence{DNSQueries: []DNSQueryEvidence{{Service: "r", ActualOutcome: queryOutcome, Offset: 100 * time.Millisecond}}}}
}

func divergenceFindings(cases []HuntCaseResult) []HuntCaseFinding {
	var out []HuntCaseFinding
	for _, item := range cases {
		for _, finding := range item.Findings {
			if finding.Code == "truth_equivalent_diagnosis_divergence" {
				out = append(out, finding)
			}
		}
	}
	return out
}

func distinctFingerprints(cases []HuntCaseResult, of func(HuntCaseResult) string) int {
	seen := map[string]bool{}
	for _, item := range cases {
		seen[of(item)] = true
	}
	return len(seen)
}

// Diagnosis divergence is only a defect report when the cases being compared
// were actually comparable: same observed truth, same applied mutations, more
// than one observation. Each negative case below breaks exactly one of those
// and must go quiet, so a later change to what "same truth" means cannot widen
// or narrow grouping without failing here.
func TestTruthInstabilityRequiresEquivalentTruthAndMutations(t *testing.T) {
	// The generator emits mutations sorted by ID, so these manifests carry the
	// canonical order a real case has; identity is the mutation set, not a
	// hand-picked slice order.
	dnsDrop := GeneratedMutation{ID: "dns.drop", Service: "r"}
	unobservedLoss := GeneratedMutation{ID: "netem.loss", Node: "gateway", Segment: "upstream", LossPercent: 20}
	dropOnly, dropAndLoss := huntManifest(dnsDrop), huntManifest(dnsDrop, unobservedLoss)

	for _, tc := range []struct {
		name          string
		cases         []HuntCaseResult
		truths        int
		diagnoses     int
		wantDivergent int
	}{
		{
			name: "same truth and mutations with disagreeing diagnoses",
			cases: []HuntCaseResult{
				huntCaseResult(3, dropOnly, huntDNSReport("ANSWER", "PASS")),
				huntCaseResult(8, dropOnly, huntDNSReport("ANSWER", "FAIL")),
				huntCaseResult(9, dropOnly, huntDNSReport("ANSWER", "WARN")),
			},
			truths: 1, diagnoses: 3, wantDivergent: 1,
		},
		{
			name: "different observed truth is not an equivalence class",
			cases: []HuntCaseResult{
				huntCaseResult(3, dropOnly, huntDNSReport("ANSWER", "PASS")),
				huntCaseResult(8, dropOnly, huntDNSReport("SERVFAIL", "FAIL")),
			},
			truths: 2, diagnoses: 2, wantDivergent: 0,
		},
		{
			name: "different applied mutations are not an equivalence class",
			cases: []HuntCaseResult{
				huntCaseResult(3, dropOnly, huntDNSReport("ANSWER", "PASS")),
				huntCaseResult(8, dropAndLoss, huntDNSReport("ANSWER", "FAIL")),
			},
			truths: 1, diagnoses: 2, wantDivergent: 0,
		},
		{
			name:   "a single observation cannot be unstable",
			cases:  []HuntCaseResult{huntCaseResult(3, dropOnly, huntDNSReport("ANSWER", "PASS"))},
			truths: 1, diagnoses: 1, wantDivergent: 0,
		},
		{
			name: "equivalent cases that agree are not unstable",
			cases: []HuntCaseResult{
				huntCaseResult(3, dropOnly, huntDNSReport("ANSWER", "PASS")),
				huntCaseResult(8, dropOnly, huntDNSReport("ANSWER", "PASS")),
			},
			truths: 1, diagnoses: 1, wantDivergent: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truths := distinctFingerprints(tc.cases, func(item HuntCaseResult) string { return item.TruthFingerprint })
			diagnoses := distinctFingerprints(tc.cases, func(item HuntCaseResult) string { return item.DiagnosisFingerprint.ID })
			if truths != tc.truths || diagnoses != tc.diagnoses {
				t.Fatalf("inputs carry %d truths and %d diagnoses, want %d and %d", truths, diagnoses, tc.truths, tc.diagnoses)
			}
			addTruthInstabilityFindings(tc.cases)
			findings := divergenceFindings(tc.cases)
			if len(findings) != tc.wantDivergent {
				t.Fatalf("divergence findings = %d, want %d: %+v", len(findings), tc.wantDivergent, findings)
			}
			if tc.wantDivergent == 0 {
				return
			}
			finding := findings[0]
			if finding.Category != FindingDiagnosticInstability || finding.Severity != SeverityMedium {
				t.Errorf("finding = %s/%s, want %s/%s", finding.Category, finding.Severity, FindingDiagnosticInstability, SeverityMedium)
			}
			if finding.Reproduce.Case != tc.cases[0].Manifest.Case || finding.Fingerprint == "" {
				t.Errorf("finding reproduces case %d with fingerprint %q, want case %d",
					finding.Reproduce.Case, finding.Fingerprint, tc.cases[0].Manifest.Case)
			}
			if len(tc.cases[0].Findings) != 1 {
				t.Errorf("group reported %d findings on its first case, want one grouped finding", len(tc.cases[0].Findings))
			}
		})
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
