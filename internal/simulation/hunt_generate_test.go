package simulation

import (
	"encoding/json"
	mathrand "math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func loadHuntBase(t testing.TB, name string) *Scenario {
	t.Helper()
	s, err := LibraryScenario(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHuntMutationRegistryOrder(t *testing.T) {
	wantBases := []string{"dual-stack-healthy", "healthy", "healthy-routed-network",
		"socks5h-remote-dns-succeeds", "tls-valid", "two-path-healthy", "two-path-ipv6-healthy",
		"two-router-healthy"}
	if got := HuntBaseNames(); !reflect.DeepEqual(got, wantBases) {
		t.Fatalf("hunt bases = %v, want sorted %v", got, wantBases)
	}
	want := []string{
		"netem.loss", "netem.latency", "netem.jitter", "timeline.netem_spike",
		"dns.servfail", "dns.drop", "timeline.dns_outage", "service.tcp_reset",
		"service.tls_expired", "proxy.connect_refused", "quic.udp_443_block",
		"encrypted_dns.doh_invalid", "http.status_503",
		"family.ipv4_drop", "family.ipv6_drop", "link.transient_down",
		"routing.preferred_path_failure",
		// v4, appended. Order is the contract: an older generator is this list
		// truncated at its own version, so a new id may only ever go last.
		"service.connection_refused", "service.tcp_port_blocked", "service.tls_hostname_mismatch",
		"routing.no_default_route", "routing.wrong_default_route", "routing.missing_subnet_route",
		// v5, appended for the same reason.
		"pmtu.blackhole",
	}
	got := make([]string, len(huntMutationRegistry))
	for i := range huntMutationRegistry {
		got[i] = huntMutationRegistry[i].id
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry = %v, want %v", got, want)
	}
	if !hasWorkingAlternatePath(loadHuntBase(t, "two-path-healthy"), familyIPv4) {
		t.Error("the multipath hunt base does not satisfy preferred-path applicability")
	}
	if hasWorkingAlternatePath(loadHuntBase(t, "two-path-healthy"), familyIPv6) ||
		hasWorkingAlternatePath(loadHuntBase(t, "healthy"), familyIPv4) ||
		hasWorkingAlternatePath(loadHuntBase(t, "healthy-routed-network"), familyIPv4) ||
		hasWorkingAlternatePath(loadHuntBase(t, "dual-stack-healthy"), familyIPv4) {
		t.Error("a curated base claims a working alternate route it does not have")
	}
	if !hasWorkingAlternatePath(loadHuntBase(t, "two-path-ipv6-healthy"), familyIPv6) ||
		hasWorkingAlternatePath(loadHuntBase(t, "two-path-ipv6-healthy"), familyIPv4) {
		t.Error("IPv6-only multipath applicability leaked across address families")
	}
}

func TestPreferredPathFailureGeneratorTargetsPreferredRouterUpstream(t *testing.T) {
	base := loadHuntBase(t, "two-path-healthy")
	op := huntOperator(t, "routing.preferred_path_failure")
	if !op.applicable(base) {
		t.Fatal("production applicability rejected the healthy multipath base")
	}
	mutation, err := op.generate(newTestRNG(), base)
	if err != nil {
		t.Fatal(err)
	}
	mutation.ID = op.id
	if mutation.Node != "preferred-gateway" || mutation.TargetNode != "client" ||
		mutation.Segment != "preferred-upstream" || mutation.Family != "ipv4" ||
		mutation.PreferredVia != "10.79.1.1" || mutation.PreferredSegment != "preferred-lan" || mutation.PreferredMetric != 50 ||
		mutation.AlternateVia != "10.79.3.1" || mutation.AlternateSegment != "alternate-lan" || mutation.AlternateMetric != 100 ||
		mutation.ControlTarget != "9.9.9.9:80" {
		t.Fatalf("generated mutation = %+v", mutation)
	}
	mutated := cloneScenario(base)
	canonicalScenarioInput(mutated)
	if err := applyGeneratedMutation(mutated, mutation); err != nil {
		t.Fatal(err)
	}
	if got := mutated.Faults[len(mutated.Faults)-1]; got.Type != FaultLinkDown ||
		got.Node != mutation.Node || got.Segment != mutation.Segment {
		t.Fatalf("applied fault = %+v", got)
	}
	if hasWorkingAlternatePath(loadHuntBase(t, "healthy-routed-network"), familyIPv4) ||
		hasWorkingAlternatePath(loadHuntBase(t, "dual-stack-healthy"), familyIPv4) {
		t.Fatal("single-path controls became applicable")
	}
}

func TestPreferredPathFailureGeneratorTargetsIPv6PreferredRouterUpstream(t *testing.T) {
	base := loadHuntBase(t, "two-path-ipv6-healthy")
	op := huntOperator(t, "routing.preferred_path_failure")
	mutation, err := op.generate(newTestRNG(), base)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Node != "preferred-gateway" || mutation.TargetNode != "client" ||
		mutation.Segment != "preferred-upstream" || mutation.Family != "ipv6" ||
		mutation.PreferredVia != "2001:db8:79:1::1" || mutation.PreferredSegment != "preferred-lan" || mutation.PreferredMetric != 50 ||
		mutation.AlternateVia != "2001:db8:79:3::1" || mutation.AlternateSegment != "alternate-lan" || mutation.AlternateMetric != 100 ||
		mutation.ControlTarget != "[2001:db8:79::99]:80" {
		t.Fatalf("generated IPv6 mutation = %+v", mutation)
	}
}

func TestPreferredPathApplicabilityDoesNotMixFamilyPathCounts(t *testing.T) {
	mixed := cloneScenario(loadHuntBase(t, "two-path-healthy"))
	mixed.Topology.Routes = append(mixed.Topology.Routes, Route{Node: "client", Destination: "default",
		Via: "2001:db8:79:1::1", Metric: 50, Default: true, Family: "ipv6"})
	if !hasWorkingAlternatePath(mixed, familyIPv4) {
		t.Fatal("two working IPv4 paths stopped satisfying IPv4 applicability")
	}
	if hasWorkingAlternatePath(mixed, familyIPv6) {
		t.Fatal("two IPv4 defaults plus one IPv6 default satisfied IPv6 applicability")
	}

	ambiguous := cloneScenario(loadHuntBase(t, "two-path-healthy"))
	ambiguous.Topology.Routes[1].Metric = ambiguous.Topology.Routes[0].Metric
	if hasWorkingAlternatePath(ambiguous, familyIPv4) {
		t.Fatal("equal-metric defaults were treated as an unambiguous preferred path")
	}

	sharedRouter := cloneScenario(loadHuntBase(t, "two-path-healthy"))
	sharedRouter.Topology.Nodes[1].Interfaces = append(sharedRouter.Topology.Nodes[1].Interfaces,
		Interface{Segment: "alternate-lan", Address: "10.79.3.1/24"})
	sharedRouter.Topology.Nodes[2].Role = "target"
	if hasWorkingAlternatePath(sharedRouter, familyIPv4) {
		t.Fatal("defaults through the same router were treated as independently fail-able paths")
	}
}

func TestGenerateHuntCaseCanSelectPreferredPathFailure(t *testing.T) {
	base := loadHuntBase(t, "two-path-healthy")
	generated, err := GenerateHuntCase("two-path-healthy", base, 20260811, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Manifest.Mutations) != 1 || generated.Manifest.Mutations[0].ID != "routing.preferred_path_failure" {
		t.Fatalf("generated mutations = %+v", generated.Manifest.Mutations)
	}
}

func TestGenerateHuntCaseCanSelectIPv6PreferredPathFailure(t *testing.T) {
	base := loadHuntBase(t, "two-path-ipv6-healthy")
	generated, err := GenerateHuntCase("two-path-ipv6-healthy", base, 20260811, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Manifest.Mutations) != 1 || generated.Manifest.Mutations[0].ID != "routing.preferred_path_failure" ||
		generated.Manifest.Mutations[0].Family != "ipv6" {
		t.Fatalf("generated mutations = %+v", generated.Manifest.Mutations)
	}
}

// The black hole has to land on the hop the client's own bulk write actually
// crosses, which is what separates it from an interface that merely got
// narrower somewhere. The two-router base is where that distinction is
// load-bearing: the internet gateway carries the default, and the briefed
// target sits behind the other router entirely.
func TestPMTUBlackholeGeneratorNarrowsTheHopToTheBriefedTarget(t *testing.T) {
	for _, tc := range []struct{ base, node, segment string }{
		{"healthy-routed-network", "gateway", "upstream"},
		{"two-router-healthy", "target-gateway", "target-lan"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			base := validatedHuntBase(t, tc.base)
			op := huntOperator(t, "pmtu.blackhole")
			if !op.applicable(base) {
				t.Fatal("production applicability rejected a base with an IPv4 transit hop")
			}
			mutation, err := op.generate(newTestRNG(), base)
			if err != nil {
				t.Fatal(err)
			}
			mutation.ID = op.id
			if mutation.Node != tc.node || mutation.Segment != tc.segment ||
				mutation.TargetNode != "client" || mutation.MTU != minBlackholeMTU {
				t.Fatalf("generated mutation = %+v", mutation)
			}
			control, mutated := cloneScenario(base), cloneScenario(base)
			if err := applyGeneratedMutation(mutated, mutation); err != nil {
				t.Fatal(err)
			}
			if len(mutated.Faults) != len(base.Faults)+1 {
				t.Fatalf("faults = %+v, want exactly one more than the base", mutated.Faults)
			}
			got := mutated.Faults[len(mutated.Faults)-1]
			if got.Type != FaultPMTUBlackhole || got.Node != tc.node || got.Segment != tc.segment ||
				got.MTU != minBlackholeMTU {
				t.Fatalf("applied fault = %+v", got)
			}
			// The fault is the whole change. Nothing about the endpoints moves,
			// which is what leaves them offering full-size packets to a hop that
			// can no longer carry them.
			control.Faults = mutated.Faults
			if !reflect.DeepEqual(control, mutated) {
				t.Fatal("the black hole changed something other than the fault list")
			}
			revalidated := cloneScenario(mutated)
			canonicalScenarioInput(revalidated)
			if err := revalidated.Validate(); err != nil {
				t.Fatalf("the generated black hole does not validate: %v", err)
			}
		})
	}
}

// A black hole is a transit condition on the way to the briefed endpoint, so a
// base with no forwarding hop in between must not host one. Neither must a hop
// carrying IPv6: minIPv6MTU is the floor IPv6 requires of a link, so such a hop
// cannot be narrowed to anything an IPv6 sender would not already fit inside,
// and a black hole that black-holes nothing is a different fault wearing this
// one's name.
func TestPMTUBlackholeApplicabilityNeedsAnIPv4TransitHop(t *testing.T) {
	op := huntOperator(t, "pmtu.blackhole")
	for _, name := range []string{"healthy", "tls-valid", "socks5h-remote-dns-succeeds",
		"dual-stack-healthy", "two-path-healthy", "two-path-ipv6-healthy"} {
		if op.applicable(validatedHuntBase(t, name)) {
			t.Errorf("%s hosts a path-MTU black hole it has no IPv4 transit hop for", name)
		}
	}
	// The control that isolates the family rule rather than trusting it: the
	// dual-stack base has exactly the router and route shape the applicable
	// bases have, and only the IPv6 address on the forwarding hop keeps it out.
	dual := validatedHuntBase(t, "dual-stack-healthy")
	for ni := range dual.Topology.Nodes {
		if dual.Topology.Nodes[ni].Name != "gateway" {
			continue
		}
		for ii := range dual.Topology.Nodes[ni].Interfaces {
			if dual.Topology.Nodes[ni].Interfaces[ii].Segment == "upstream" {
				dual.Topology.Nodes[ni].Interfaces[ii].IPv6 = ""
			}
		}
	}
	if !op.applicable(dual) {
		t.Error("a forwarding hop that lost IPv6 still could not host a black hole")
	}
}

// The catalogue entry is only worth having if the ordinary generator path
// reaches it, and anything replaying a published case has to land on it again.
func TestGenerateHuntCaseCanSelectPMTUBlackhole(t *testing.T) {
	for _, tc := range []struct {
		base          string
		seed          int64
		caseNumber    int
		node, segment string
	}{
		{"healthy-routed-network", 20260102, 39, "gateway", "upstream"},
		{"two-router-healthy", 20260108, 12, "target-gateway", "target-lan"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			generated, err := GenerateHuntCase(tc.base, loadHuntBase(t, tc.base), tc.seed, tc.caseNumber, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(generated.Manifest.Mutations) != 1 || generated.Manifest.Mutations[0].ID != "pmtu.blackhole" {
				t.Fatalf("generated mutations = %+v", generated.Manifest.Mutations)
			}
			if mutation := generated.Manifest.Mutations[0]; mutation.Node != tc.node ||
				mutation.Segment != tc.segment || mutation.MTU != minBlackholeMTU {
				t.Fatalf("generated mutation = %+v", mutation)
			}
			// The materialized scenario, not only the manifest: a case that
			// records a black hole and then executes a healthy network is an
			// unused fault declaration rather than a test.
			var faults []Fault
			for _, fault := range generated.Scenario.Faults {
				if fault.Type == FaultPMTUBlackhole {
					faults = append(faults, fault)
				}
			}
			if len(faults) != 1 || faults[0].Node != tc.node || faults[0].Segment != tc.segment ||
				faults[0].MTU != minBlackholeMTU {
				t.Fatalf("materialized faults = %+v", generated.Scenario.Faults)
			}
			replay, err := GenerateHuntCase(tc.base, loadHuntBase(t, tc.base), tc.seed, tc.caseNumber, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(replay.Manifest, generated.Manifest) {
				t.Fatalf("replay = %+v, want %+v", replay.Manifest, generated.Manifest)
			}
			// The other half of the version contract: the generator this case
			// was minted under is the only one that reaches the operator, so an
			// artifact naming v4 keeps resolving to the case it always did.
			older, err := generateHuntCase("v4", tc.base, loadHuntBase(t, tc.base), tc.seed, tc.caseNumber, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, mutation := range older.Manifest.Mutations {
				if mutation.ID == "pmtu.blackhole" {
					t.Error("a v4 case reached an operator that did not exist at v4")
				}
			}
		})
	}
}

func validatedHuntBase(t testing.TB, name string) *Scenario {
	t.Helper()
	base := loadHuntBase(t, name)
	canonicalScenarioInput(base)
	if err := base.Validate(); err != nil {
		t.Fatalf("%s does not validate: %v", name, err)
	}
	return base
}

func TestProbeFamilyMutationGenerators(t *testing.T) {
	tests := []struct {
		id, base, inapplicable, removeService string
		check                                 func(*testing.T, GeneratedMutation, *Scenario)
	}{
		{"service.tls_expired", "tls-valid", "tls-expired-certificate", "", func(t *testing.T, m GeneratedMutation, s *Scenario) {
			svc := namedService(t, s, m.Service)
			if m.Node != "target" || svc.Type != ServiceTLS || svc.Certificate.Mode != TLSCertificateExpired || svc.Port != 9443 {
				t.Errorf("TLS mutation = %+v, service = %+v", m, svc)
			}
		}},
		{"proxy.connect_refused", "socks5h-remote-dns-succeeds", "healthy", "", func(t *testing.T, m GeneratedMutation, s *Scenario) {
			if m.Node != "proxy" || m.TargetNode != "destination" || m.Service != "socks-proxy" || m.TargetPort != 443 {
				t.Errorf("proxy mutation = %+v", m)
			}
			for _, service := range s.Topology.node(m.TargetNode).Services {
				if service.Type == ServiceTCP && service.Port == m.TargetPort {
					t.Errorf("proxy CONNECT destination still listens: %+v", service)
				}
			}
		}},
		{"quic.udp_443_block", "healthy", "quic-udp-443-blocked", "", func(t *testing.T, m GeneratedMutation, s *Scenario) {
			fault := s.Faults[len(s.Faults)-1]
			if m.Node != "internet" || m.Service != quicProbeService || fault.Type != FaultDrop ||
				fault.Direction != DirectionInbound || fault.Protocol != "udp" || fault.Port != 443 {
				t.Errorf("QUIC mutation = %+v, fault = %+v", m, fault)
			}
		}},
		{"encrypted_dns.doh_invalid", "healthy", "healthy", encryptedDNSProbeService, func(t *testing.T, m GeneratedMutation, s *Scenario) {
			svc := namedService(t, s, m.Service)
			if m.Node != "internet" || svc.Type != ServiceEncryptedDNS || svc.DoHResponse != DoHResponseInvalid ||
				svc.Port != 443 || svc.Certificate.Mode != TLSCertificateValid {
				t.Errorf("DoH mutation = %+v, service = %+v", m, svc)
			}
		}},
		{"http.status_503", "healthy", "http-error", "", func(t *testing.T, m GeneratedMutation, s *Scenario) {
			target, ok := findHTTPTestTarget(s, s.Tests)
			if !ok {
				t.Fatal("mutated HTTP target disappeared")
			}
			svc := s.Topology.node(target.node).Services[target.service]
			if m.Node != "server" || m.TargetPort != 80 || m.Status != 503 || svc.Type != ServiceHTTP || svc.Status != 503 || svc.Port != 80 {
				t.Errorf("HTTP mutation = %+v, service = %+v", m, svc)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			base := loadHuntBase(t, tc.base)
			before, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			control := cloneScenario(base)
			canonicalScenarioInput(control)
			if err := control.Validate(); err != nil {
				t.Fatal(err)
			}
			op := huntOperator(t, tc.id)
			if !op.applicable(control) {
				t.Fatal("operator is not applicable to its control")
			}
			first, err := op.generate(newTestRNG(), control)
			if err != nil {
				t.Fatal(err)
			}
			second, err := op.generate(newTestRNG(), control)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("generator is not deterministic: %+v != %+v", first, second)
			}
			first.ID = tc.id
			mutated := cloneScenario(control)
			canonicalScenarioInput(mutated)
			if err := applyGeneratedMutation(mutated, first); err != nil {
				t.Fatal(err)
			}
			if err := mutated.Validate(); err != nil {
				t.Fatalf("mutated scenario is invalid: %v", err)
			}
			if after, _ := json.Marshal(base); string(after) != string(before) {
				t.Fatal("generator or application modified the source scenario")
			}
			assertUnrelatedTopology(t, control, mutated)
			tc.check(t, first, mutated)

			inapplicable := loadHuntBase(t, tc.inapplicable)
			if tc.removeService != "" {
				removeNamedService(inapplicable, tc.removeService)
			}
			if op.applicable(inapplicable) {
				t.Fatal("operator emitted a bogus mutation for an inapplicable scenario")
			}
		})
	}
}

func TestTLSExpiryRequiresMatchingValidCertificate(t *testing.T) {
	s := loadHuntBase(t, "tls-valid")
	named := namedService(t, s, "tls-target")
	named.Certificate.DNSNames = []string{"different-target.test"}
	for ni := range s.Topology.Nodes {
		for si := range s.Topology.Nodes[ni].Services {
			if s.Topology.Nodes[ni].Services[si].Name == named.Name {
				s.Topology.Nodes[ni].Services[si] = named
			}
		}
	}
	if hasValidTLSTestTarget(s) {
		t.Fatal("TLS expiry applies to a certificate that already fails hostname validation")
	}
}

func huntOperator(t testing.TB, id string) mutationOperator {
	t.Helper()
	for _, op := range huntMutationRegistry {
		if op.id == id {
			return op
		}
	}
	t.Fatalf("unknown hunt operator %q", id)
	return mutationOperator{}
}

// #nosec G404 -- fixed pseudo-randomness makes mutation tests deterministic.
func newTestRNG() *mathrand.Rand { return mathrand.New(mathrand.NewSource(12345)) }

func namedService(t testing.TB, s *Scenario, name string) Service {
	t.Helper()
	for _, node := range s.Topology.Nodes {
		for _, service := range node.Services {
			if service.Name == name {
				return service
			}
		}
	}
	t.Fatalf("service %q not found", name)
	return Service{}
}

func removeNamedService(s *Scenario, name string) {
	for ni := range s.Topology.Nodes {
		services := s.Topology.Nodes[ni].Services[:0]
		for _, service := range s.Topology.Nodes[ni].Services {
			if service.Name != name {
				services = append(services, service)
			}
		}
		s.Topology.Nodes[ni].Services = services
	}
}

func assertUnrelatedTopology(t testing.TB, before, after *Scenario) {
	t.Helper()
	if !reflect.DeepEqual(before.Topology.Segments, after.Topology.Segments) {
		t.Fatalf("mutation changed segments: %+v != %+v", before.Topology.Segments, after.Topology.Segments)
	}
	if !reflect.DeepEqual(before.Topology.Routes, after.Topology.Routes) {
		t.Fatalf("mutation changed routes: %+v != %+v", before.Topology.Routes, after.Topology.Routes)
	}
	if !reflect.DeepEqual(before.Tests, after.Tests) {
		t.Fatalf("mutation changed tests: %+v != %+v", before.Tests, after.Tests)
	}
	if len(before.Topology.Nodes) != len(after.Topology.Nodes) {
		t.Fatal("mutation changed node count")
	}
	for i := range before.Topology.Nodes {
		a, b := before.Topology.Nodes[i], after.Topology.Nodes[i]
		a.Services, b.Services = nil, nil
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("mutation changed unrelated node topology: %+v != %+v", a, b)
		}
	}
}

func TestHuntCaseGenerationIsIndependentAndDeterministic(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	const seed int64 = 12345
	var sequential []GeneratedCaseManifest
	for caseNumber := 0; caseNumber < 100; caseNumber++ {
		generated, err := GenerateHuntCase("healthy-routed-network", base, seed, caseNumber, 2)
		if err != nil {
			t.Fatalf("case %d: %v", caseNumber, err)
		}
		sequential = append(sequential, generated.Manifest)
	}
	direct, err := GenerateHuntCase("healthy-routed-network", base, seed, 17, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(direct.Manifest, sequential[17]) {
		t.Fatalf("direct case 17 differs:\n direct %+v\n batch  %+v", direct.Manifest, sequential[17])
	}
	if direct.Manifest.CaseSeed != DeriveHuntCaseSeed(seed, "healthy-routed-network", 17) {
		t.Error("manifest did not carry the independently derived seed")
	}
}

func TestHuntGeneratorVersion3Reproduction(t *testing.T) {
	generated, err := generateHuntCase("v3", "healthy-routed-network", loadHuntBase(t, "healthy-routed-network"), 12345, 76, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := GeneratedCaseManifest{
		GeneratorVersion: "v3", BaseScenario: "healthy-routed-network", HuntSeed: 12345, Case: 76,
		CaseSeed: 1449211129837338081, MaxFaults: 2,
		Mutations: []GeneratedMutation{
			{ID: "netem.jitter", Description: "add 426 ms latency with 249 ms jitter on gateway/upstream",
				Node: "gateway", Segment: "upstream", LatencyMS: 426, JitterMS: 249, NetemSeed: 2467781340},
			{ID: "service.tcp_reset", Description: "replace target TCP port 80 with an accept-then-reset service",
				Node: "target", TargetPort: 80},
		},
		CaseFingerprint: "3b23592ff5c01fd9",
	}
	if !reflect.DeepEqual(generated.Manifest, want) {
		t.Fatalf("v3 reproduction changed:\n got  %+v\n want %+v", generated.Manifest, want)
	}
}

// The fault ceiling is one of the inputs a case is drawn from: it bounds the
// first number taken from the case seed, which is how many mutations to apply.
// The same scenario, seed and case under a different ceiling is therefore a
// different network, so the manifest records it and every reproduction carries
// it. These are the exact coordinates the nightly triage hunts.
func TestMaxFaultsIsPartOfTheExperimentAndOfItsReproduction(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	two, err := GenerateHuntCase("healthy", base, 20260101, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	three, err := GenerateHuntCase("healthy", base, 20260101, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	if two.Manifest.CaseFingerprint == three.Manifest.CaseFingerprint {
		t.Fatalf("ceilings 2 and 3 both produced case fingerprint %s, so this case cannot show the difference",
			two.Manifest.CaseFingerprint)
	}
	if two.Manifest.MaxFaults != 2 || three.Manifest.MaxFaults != 3 {
		t.Errorf("manifests record ceilings %d and %d, want 2 and 3", two.Manifest.MaxFaults, three.Manifest.MaxFaults)
	}
	if got := reproductionFor(two.Manifest).MaxFaults; got != 2 {
		t.Errorf("reproduction carries ceiling %d, want 2", got)
	}
	// The command a reader pastes has to name it too, or it reproduces this
	// case only for as long as the CLI default happens to agree.
	finding := NewTriageFinding(HuntFinding{Reproduce: reproductionFor(two.Manifest)})
	if !strings.Contains(finding.ReproduceCommand(), "--max-faults 2") {
		t.Errorf("reproduce command = %q, want it to name the ceiling", finding.ReproduceCommand())
	}
}

// Challenge versions V3, V4 and A1 name "v4" as a literal so that moving
// HuntGeneratorVersion cannot repoint the ids they have already published. That
// only holds while "v4" is still a version this build can materialize a case
// for, so a bump has to append to huntGeneratorVersions rather than replace the
// entry it finds there. This is what fails if it replaces instead.
func TestPublishedHuntGeneratorVersionsStayResolvable(t *testing.T) {
	// Every version any published artifact records: a challenge manifest, a
	// filed triage finding, a shared hunt reproduction.
	for _, version := range []string{"v3", "v4", "v5"} {
		if huntGeneratorIndex(version) < 0 {
			t.Errorf("hunt generator %s was published but this build can no longer materialize a case for it;"+
				" add it back to huntGeneratorVersions, since challenge ids and filed findings still name it", version)
		}
	}
	if newest := huntGeneratorVersions[len(huntGeneratorVersions)-1]; newest != HuntGeneratorVersion {
		t.Errorf("HuntGeneratorVersion is %s but the newest listed version is %s;"+
			" new cases would be minted at a version huntOperators cannot place, so append rather than edit",
			HuntGeneratorVersion, newest)
	}
}

func TestGeneratedHuntCasesValidateAndStayBounded(t *testing.T) {
	seen := make(map[string]bool)
	for _, baseID := range HuntBaseNames() {
		base := loadHuntBase(t, baseID)
		for caseNumber := 0; caseNumber < 1000; caseNumber++ {
			generated, err := GenerateHuntCase(baseID, base, 8675309, caseNumber, HuntMaxFaults)
			if err != nil {
				t.Fatalf("%s case %d: %v", baseID, caseNumber, err)
			}
			revalidated := cloneScenario(generated.Scenario)
			canonicalScenarioInput(revalidated)
			if err := revalidated.Validate(); err != nil {
				t.Fatalf("%s case %d failed repeat validation: %v", baseID, caseNumber, err)
			}
			if generated.Manifest.CaseFingerprint == "" || len(generated.Manifest.Mutations) < 1 || len(generated.Manifest.Mutations) > HuntMaxFaults {
				t.Fatalf("%s case %d manifest = %+v", baseID, caseNumber, generated.Manifest)
			}
			assertCompatibleManifest(t, generated.Manifest)
			assertMutationBounds(t, generated.Manifest)
			for _, mutation := range generated.Manifest.Mutations {
				seen[mutation.ID] = true
			}
		}
	}
	// Every operator applicable to today's controls must appear in this large
	// deterministic sample.
	for _, op := range huntMutationRegistry {
		if !seen[op.id] {
			t.Errorf("operator %s was never generated", op.id)
		}
	}
}

func assertCompatibleManifest(t testing.TB, manifest GeneratedCaseManifest) {
	t.Helper()
	byID := make(map[string]mutationOperator)
	for _, op := range huntMutationRegistry {
		byID[op.id] = op
	}
	var selected []mutationOperator
	for _, mutation := range manifest.Mutations {
		op, ok := byID[mutation.ID]
		if !ok {
			t.Fatalf("unknown mutation in manifest: %s", mutation.ID)
		}
		if !operatorsCompatible(selected, op) {
			t.Fatalf("conflicting mutations in manifest: %+v", manifest.Mutations)
		}
		selected = append(selected, op)
	}
}

func assertMutationBounds(t testing.TB, manifest GeneratedCaseManifest) {
	t.Helper()
	for _, mutation := range manifest.Mutations {
		switch mutation.ID {
		case "netem.loss":
			if mutation.LossPercent < 5 || mutation.LossPercent > 30 {
				t.Errorf("loss out of bounds: %+v", mutation)
			}
		case "netem.latency":
			if mutation.LatencyMS < 50 || mutation.LatencyMS > 800 {
				t.Errorf("latency out of bounds: %+v", mutation)
			}
		case "netem.jitter":
			if mutation.JitterMS < 10 || mutation.JitterMS > 250 || mutation.LatencyMS <= mutation.JitterMS || mutation.LatencyMS > 800 {
				t.Errorf("jitter combination out of bounds: %+v", mutation)
			}
		case "timeline.netem_spike":
			if mutation.StartMS < 150 || mutation.DurationMS < 700 || mutation.DurationMS > 1200 || mutation.NetemSeed == 0 {
				t.Errorf("spike out of bounds: %+v", mutation)
			}
		case "timeline.dns_outage", "link.transient_down":
			if mutation.DurationMS < 450 || mutation.DurationMS > 900 {
				t.Errorf("outage out of bounds: %+v", mutation)
			}
		}
	}
}

func TestHuntGenerationDoesNotMutateBase(t *testing.T) {
	base := loadHuntBase(t, "dual-stack-healthy")
	before, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	for caseNumber := 0; caseNumber < 500; caseNumber++ {
		generated, err := GenerateHuntCase("dual-stack-healthy", base, -9223372036854775807, caseNumber, 3)
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately mutate nested generated storage. Any shallow alias would
		// make the byte-for-byte comparison below fail.
		if len(generated.Scenario.Faults) > 0 && len(generated.Scenario.Faults[0].Events) > 0 {
			generated.Scenario.Faults[0].Events[0].At = "29s"
		}
		if len(generated.Scenario.Topology.Nodes[0].Interfaces) > 0 {
			generated.Scenario.Topology.Nodes[0].Interfaces[0].Segment = "changed"
		}
	}
	after, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("generating cases mutated the loaded base scenario")
	}
}

func TestHuntCaseFingerprintUsesSemanticContent(t *testing.T) {
	manifest := GeneratedCaseManifest{GeneratorVersion: HuntGeneratorVersion, BaseScenario: "healthy",
		HuntSeed: 1, Case: 2, CaseSeed: 3,
		Mutations: []GeneratedMutation{{ID: "netem.loss", Node: "client", Segment: "lan", LossPercent: 10, NetemSeed: 4}}}
	first := huntCaseFingerprint(manifest)
	manifest.HuntSeed, manifest.Case, manifest.CaseSeed = 90, 91, 92
	if second := huntCaseFingerprint(manifest); second != first {
		t.Errorf("reproduction metadata changed semantic fingerprint: %s != %s", first, second)
	}
	manifest.Mutations[0].LossPercent = 11
	if changed := huntCaseFingerprint(manifest); changed == first {
		t.Error("semantic mutation change did not alter case fingerprint")
	}
}

func TestGeneratedMutationFieldsAreDisplaySafe(t *testing.T) {
	identifier := regexp.MustCompile(`^[a-z0-9._-]+$`)
	for _, baseID := range HuntBaseNames() {
		base := loadHuntBase(t, baseID)
		for caseNumber := 0; caseNumber < 500; caseNumber++ {
			generated, err := GenerateHuntCase(baseID, base, 42, caseNumber, 3)
			if err != nil {
				t.Fatal(err)
			}
			for _, mutation := range generated.Manifest.Mutations {
				if !identifier.MatchString(mutation.ID) {
					t.Errorf("unsafe mutation ID %q", mutation.ID)
				}
				for label, value := range map[string]string{"node": mutation.Node, "target_node": mutation.TargetNode, "segment": mutation.Segment, "service": mutation.Service} {
					if value != "" && !isSafeName(value) {
						t.Errorf("unsafe %s %q", label, value)
					}
					if strings.ContainsAny(value, `/\\;$`+"`") {
						t.Errorf("%s exposes command or path syntax: %q", label, value)
					}
				}
			}
		}
	}
}

func TestGenerateHuntCaseRejectsBounds(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	for _, tc := range []struct {
		base       string
		caseNumber int
		maxFaults  int
	}{
		{"healthy", -1, 1}, {"healthy", HuntMaxCaseNumber + 1, 1},
		{"healthy", 0, 0}, {"healthy", 0, HuntMaxFaults + 1}, {"broken-dns", 0, 1},
	} {
		if _, err := GenerateHuntCase(tc.base, base, 1, tc.caseNumber, tc.maxFaults); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
}

func FuzzGenerateHuntCase(f *testing.F) {
	for _, seed := range []int64{0, 1, -1, 12345, -9223372036854775807} {
		f.Add(seed, uint32(17), uint8(2), uint8(2))
	}
	f.Fuzz(func(t *testing.T, seed int64, rawCase uint32, rawBase, rawFaults uint8) {
		bases := HuntBaseNames()
		baseID := bases[int(rawBase)%len(bases)]
		base := loadHuntBase(t, baseID)
		caseNumber := int(rawCase % (HuntMaxCaseNumber + 1))
		maxFaults := int(rawFaults%HuntMaxFaults) + 1
		// Every input this harness builds is in range, so an error here is a
		// generator defect (a mutation that cannot apply, a compatibility gap,
		// an invalid result), never a rejected input.
		generated, err := GenerateHuntCase(baseID, base, seed, caseNumber, maxFaults)
		if err != nil {
			t.Fatalf("%s seed %d case %d maxFaults %d: %v", baseID, seed, caseNumber, maxFaults, err)
		}
		revalidated := cloneScenario(generated.Scenario)
		canonicalScenarioInput(revalidated)
		if err := revalidated.Validate(); err != nil {
			t.Fatalf("generated invalid case: %v", err)
		}
		if generated.Manifest.CaseFingerprint == "" || len(generated.Manifest.Mutations) > HuntMaxFaults {
			t.Fatalf("unbounded manifest: %+v", generated.Manifest)
		}
	})
}
