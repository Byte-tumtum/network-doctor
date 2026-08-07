package simulation

import (
	"encoding/json"
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
	want := []string{
		"netem.loss", "netem.latency", "netem.jitter", "timeline.netem_spike",
		"dns.servfail", "dns.drop", "timeline.dns_outage", "service.tcp_reset",
		"family.ipv4_drop", "family.ipv6_drop", "link.transient_down",
		"routing.preferred_path_failure",
	}
	if got := HuntMutationIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registry = %v, want %v", got, want)
	}
	if hasWorkingAlternatePath(loadHuntBase(t, "healthy")) ||
		hasWorkingAlternatePath(loadHuntBase(t, "healthy-routed-network")) ||
		hasWorkingAlternatePath(loadHuntBase(t, "dual-stack-healthy")) {
		t.Error("a curated base claims a working alternate route it does not have")
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
	// Preferred-route failure is deliberately dormant until a known-good
	// multi-path base exists. Every operator applicable to today's controls must
	// appear in this large deterministic sample.
	for _, id := range HuntMutationIDs() {
		if id == "routing.preferred_path_failure" {
			continue
		}
		if !seen[id] {
			t.Errorf("operator %s was never generated", id)
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
				for label, value := range map[string]string{"node": mutation.Node, "segment": mutation.Segment, "service": mutation.Service} {
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
