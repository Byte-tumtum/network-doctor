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
		"socks5h-remote-dns-succeeds", "tls-valid", "two-path-healthy"}
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
	}
	got := make([]string, len(huntMutationRegistry))
	for i := range huntMutationRegistry {
		got[i] = huntMutationRegistry[i].id
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registry = %v, want %v", got, want)
	}
	if !hasWorkingAlternatePath(loadHuntBase(t, "two-path-healthy")) {
		t.Error("the multipath hunt base does not satisfy preferred-path applicability")
	}
	if hasWorkingAlternatePath(loadHuntBase(t, "healthy")) ||
		hasWorkingAlternatePath(loadHuntBase(t, "healthy-routed-network")) ||
		hasWorkingAlternatePath(loadHuntBase(t, "dual-stack-healthy")) {
		t.Error("a curated base claims a working alternate route it does not have")
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
		mutation.Segment != "preferred-upstream" || mutation.Family != "ipv4" {
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
	if hasWorkingAlternatePath(loadHuntBase(t, "healthy-routed-network")) ||
		hasWorkingAlternatePath(loadHuntBase(t, "dual-stack-healthy")) {
		t.Fatal("single-path controls became applicable")
	}
}

func TestGenerateHuntCaseCanSelectPreferredPathFailure(t *testing.T) {
	base := loadHuntBase(t, "two-path-healthy")
	generated, err := GenerateHuntCase("two-path-healthy", base, 20260811, 38, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Manifest.Mutations) != 1 || generated.Manifest.Mutations[0].ID != "routing.preferred_path_failure" {
		t.Fatalf("generated mutations = %+v", generated.Manifest.Mutations)
	}
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
			target, ok := findHTTPTestTarget(s)
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

func TestHuntGeneratorVersion2Reproduction(t *testing.T) {
	generated, err := GenerateHuntCase("healthy-routed-network", loadHuntBase(t, "healthy-routed-network"), 12345, 76, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := GeneratedCaseManifest{
		GeneratorVersion: "v2", BaseScenario: "healthy-routed-network", HuntSeed: 12345, Case: 76,
		CaseSeed: -5803233938164469489,
		Mutations: []GeneratedMutation{{
			ID: "timeline.dns_outage", Description: "delay, then silence resolver routed-resolver for 758 ms before recovery",
			Service: "routed-resolver", StartMS: 150, DurationMS: 758,
		}},
		CaseFingerprint: "8581a988c9575ea8",
	}
	if !reflect.DeepEqual(generated.Manifest, want) {
		t.Fatalf("v2 reproduction changed:\n got  %+v\n want %+v", generated.Manifest, want)
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
		generated, err := GenerateHuntCase(baseID, base, seed, caseNumber, maxFaults)
		if err != nil {
			return
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
