package simulation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// challengeIDs walks a deterministic spread of ids. Tests that only need "a
// challenge of family X" search it rather than pinning constants, so ordinary
// changes to the mutation registry do not break them. The ids that must never
// move are pinned once, in TestChallengeIDsResolveToTheSameCaseForever.
func challengeIDs(count int) []string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, fmt.Sprintf("%06X", i*7919%0xFFFFFF))
	}
	return out
}

// challengeWithMutation finds a challenge whose single mutation is the named
// one. An empty name finds the healthy challenge.
func challengeWithMutation(t *testing.T, mutation string) *Challenge {
	t.Helper()
	for _, id := range challengeIDs(400) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build challenge %s: %v", id, err)
		}
		if challenge.family.mutation == mutation {
			return challenge
		}
	}
	t.Fatalf("no challenge generated the %q family in the sampled ids", mutation)
	return nil
}

// A published challenge id is a promise: whoever types it gets the puzzle the
// person who shared it played. That promise spans this file's selection, the
// hunt generator behind it and the base scenario YAML it draws from, so this
// table pins the whole chain.
//
// A failure here is not a broken test. It means a change repointed ids that may
// already be in circulation, and the fix is to leave V1 alone and add a V2
// entry to challengeGenerators — never to update these rows.
func TestChallengeIDsResolveToTheSameCaseForever(t *testing.T) {
	for _, tt := range []struct {
		id, base    string
		caseNumber  int
		mutation    string
		difficulty  string
		fingerprint string
	}{
		{"V1-000000", "two-path-healthy", 949668, "routing.preferred_path_failure", "hard", "7bd2f4bbb737c782"},
		{"V1-001EEF", "tls-valid", 619652, "dns.servfail", "easy", "ebe836dc0861aa5b"},
		{"V1-003DDE", "tls-valid", -1, "", "medium", "09e369387b93f6db"},
		{"V1-005CCD", "tls-valid", 492100, "dns.drop", "easy", "639903dfbc7956f1"},
		{"V1-013556", "tls-valid", 391463, "service.tls_expired", "medium", "46d2b0f2b4af38d5"},
		{"V1-01B112", "dual-stack-healthy", 715611, "service.tcp_reset", "easy", "059f3d44d5c36606"},
		{"V1-04977A", "dual-stack-healthy", 476967, "family.ipv4_drop", "medium", "3faa09b3c67c5370"},
		{"V1-04B669", "dual-stack-healthy", 538431, "family.ipv6_drop", "hard", "2e5c19bb54c29117"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			challenge, err := BuildChallenge(tt.id)
			if err != nil {
				t.Fatalf("build %s: %v", tt.id, err)
			}
			mutation := ""
			if len(challenge.Manifest.Mutations) == 1 {
				mutation = challenge.Manifest.Mutations[0].ID
			}
			if challenge.ID != tt.id || challenge.Base != tt.base || challenge.Case != tt.caseNumber ||
				mutation != tt.mutation || challenge.Difficulty != tt.difficulty ||
				challenge.Manifest.CaseFingerprint != tt.fingerprint {
				t.Fatalf("%s now resolves to base %s case %d mutation %q %s fingerprint %s;"+
					" it was shared as base %s case %d mutation %q %s fingerprint %s."+
					" Add a V2 generator instead of changing what V1 means.",
					tt.id, challenge.Base, challenge.Case, mutation, challenge.Difficulty,
					challenge.Manifest.CaseFingerprint, tt.base, tt.caseNumber, tt.mutation,
					tt.difficulty, tt.fingerprint)
			}
		})
	}
	// The version an id carries is the version it is resolved by, whatever this
	// build happens to mint.
	if _, ok := challengeGenerators["V1"]; !ok {
		t.Fatal("V1 ids have been published; this build can no longer resolve them")
	}
}

func TestChallengeIDReproducesTheSameCase(t *testing.T) {
	for _, id := range challengeIDs(40) {
		first, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		second, err := BuildChallenge(strings.ToLower(id))
		if err != nil {
			t.Fatalf("rebuild %s: %v", id, err)
		}
		if first.Base != second.Base || first.Case != second.Case || first.Seed != second.Seed ||
			first.Difficulty != second.Difficulty || first.family.answer != second.family.answer {
			t.Fatalf("challenge %s is not reproducible: %+v vs %+v", id, first, second)
		}
		if first.Manifest.CaseFingerprint != second.Manifest.CaseFingerprint {
			t.Fatalf("challenge %s: case fingerprint %s then %s", id,
				first.Manifest.CaseFingerprint, second.Manifest.CaseFingerprint)
		}
		one, err := yaml.Marshal(first.Scenario)
		if err != nil {
			t.Fatal(err)
		}
		two, err := yaml.Marshal(second.Scenario)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(one, two) {
			t.Fatalf("challenge %s built two different scenarios", id)
		}
	}
}

func TestChallengeIDsVaryAndStayChallengeCapable(t *testing.T) {
	cases := map[string]int{}
	answers := map[ChallengeAnswer]int{}
	for _, id := range challengeIDs(200) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		if len(challenge.Manifest.Mutations) > 1 {
			t.Fatalf("challenge %s carries %d mutations; a challenge is a single primary fault",
				id, len(challenge.Manifest.Mutations))
		}
		if len(challenge.Manifest.Mutations) == 1 {
			if _, ok := challengeFamilyFor(challenge.Manifest.Mutations[0].ID); !ok {
				t.Fatalf("challenge %s generated %q, which is not challenge-capable",
					id, challenge.Manifest.Mutations[0].ID)
			}
		}
		if challenge.Node == "" {
			t.Fatalf("challenge %s has no client node", id)
		}
		cases[challenge.Base+"/"+fmt.Sprint(challenge.Case)]++
		answers[challenge.family.answer]++
	}
	if len(cases) < 100 {
		t.Fatalf("200 ids produced only %d distinct cases", len(cases))
	}
	if len(answers) < 5 {
		t.Fatalf("200 ids produced only %d distinct answers: %v", len(answers), answers)
	}
}

// A fault on a target the briefing never names is a puzzle with no findable
// clue, and the netdoc run scoring reads was never asked about it. Both
// contestants would be guaranteed to lose.
func TestChallengeFaultsSitOnTheTargetThePlayerIsPointedAt(t *testing.T) {
	for _, id := range challengeIDs(300) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		if challenge.family.briefed == nil || len(challenge.Manifest.Mutations) != 1 {
			continue
		}
		base, err := LibraryScenario(challenge.Base)
		if err != nil {
			t.Fatal(err)
		}
		if !challenge.family.briefed(base, challenge.Manifest.Mutations[0]) {
			t.Fatalf("challenge %s puts %s on a target the briefing (%s → %q) never names",
				id, challenge.Manifest.Mutations[0].ID, challenge.Node, challenge.Target)
		}
	}
	// The rule has to actually exclude something, or it is decoration: the
	// two-path bases run a second client test whose target the briefing omits,
	// and that is where the generator places the reset.
	base, err := LibraryScenario("two-path-healthy")
	if err != nil {
		t.Fatal(err)
	}
	excluded := 0
	for caseNumber := range 200 {
		generated, err := GenerateHuntCase("two-path-healthy", base, 1234, caseNumber, 1)
		if err != nil || len(generated.Manifest.Mutations) != 1 ||
			generated.Manifest.Mutations[0].ID != "service.tcp_reset" {
			continue
		}
		if resetTargetIsBriefed(base, generated.Manifest.Mutations[0]) {
			t.Fatalf("case %d puts a reset on the second client test's target and it was accepted as briefed",
				caseNumber)
		}
		excluded++
	}
	if excluded == 0 {
		t.Fatal("no two-path reset case was found, so this rule was never exercised")
	}
}

func TestChallengeDifficultyComesFromTheFamily(t *testing.T) {
	for _, family := range challengeFamilies {
		if family.mutation == "" {
			continue
		}
		if family.difficulty == "" {
			t.Fatalf("family %s has no reviewed difficulty", family.mutation)
		}
	}
	for _, id := range challengeIDs(60) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		if challenge.family.mutation != "" && challenge.Difficulty != challenge.family.difficulty {
			t.Fatalf("challenge %s says %s, family %s is %s", id, challenge.Difficulty,
				challenge.family.mutation, challenge.family.difficulty)
		}
	}
}

func TestChallengeBriefingHidesTheAnswer(t *testing.T) {
	for _, id := range challengeIDs(120) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		var out bytes.Buffer
		challenge.WriteBriefing(&out)
		text := strings.ToLower(out.String())
		forbidden := []string{challenge.Base, fmt.Sprint(challenge.Seed), challenge.Manifest.CaseFingerprint,
			string(challenge.family.answer), strings.ToLower(challenge.family.explanation)}
		for _, mutation := range challenge.Manifest.Mutations {
			forbidden = append(forbidden, mutation.ID, strings.ToLower(mutation.Description))
		}
		for _, secret := range forbidden {
			if secret == "" {
				continue
			}
			if strings.Contains(text, secret) {
				t.Fatalf("challenge %s briefing leaks %q:\n%s", id, secret, out.String())
			}
		}
		if !strings.Contains(out.String(), id) || !strings.Contains(text, challenge.Difficulty) {
			t.Fatalf("challenge %s briefing must still name the id and difficulty:\n%s", id, out.String())
		}
	}
}

func TestChallengeAnswerMenuIsWiderThanTheGeneratableFaults(t *testing.T) {
	generatable := map[ChallengeAnswer]bool{}
	for _, family := range challengeFamilies {
		generatable[family.answer] = true
	}
	if len(ChallengeAnswerMenu) <= len(generatable) {
		t.Fatalf("the menu (%d) must offer more answers than a challenge can inject (%d), or it is the answer key",
			len(ChallengeAnswerMenu), len(generatable))
	}
	seen := map[ChallengeAnswer]bool{}
	for _, item := range ChallengeAnswerMenu {
		if seen[item.ID] {
			t.Fatalf("duplicate answer %s", item.ID)
		}
		seen[item.ID] = true
	}
	for answer := range generatable {
		if !seen[answer] {
			t.Fatalf("family answer %s is not offered in the menu", answer)
		}
	}
}

// resetChallengeReport is a report for a tcp_reset challenge: the target's
// service recorded a reset, which is what makes that fault observed.
func resetChallengeReport(t *testing.T, challenge *Challenge, cause string) *Report {
	t.Helper()
	mutation := challenge.Manifest.Mutations[0]
	check := DiagnosisCheck{ID: "http", Status: "FAIL", Cause: cause, Detail: "no HTTP response"}
	return &Report{
		Cleanup:  CleanupInfo{Done: true},
		Topology: []NodeInfo{{Name: challenge.Node, Role: "client"}},
		Tests: []TestOutcome{{Node: challenge.Node, Duration: 3 * time.Second, Diagnosis: &Diagnosis{
			Checks: []DiagnosisCheck{check}, Summary: "the target did not answer", Verdict: "service"}}},
		Evidence: Evidence{
			TCPResets: []TCPResetEvidence{{Node: mutation.Node, Event: "reset", Result: "connection_reset", Count: 1}},
			FamilyReachability: []FamilyReachabilityEvidence{
				{Node: challenge.Node, Family: "ipv4", State: FamilyStateReachable},
			},
		},
	}
}

func TestChallengeScoringMatchup(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	right, wrong := AnswerReset, AnswerDNSFailure
	if challenge.family.answer != right {
		t.Fatalf("tcp_reset family answers %s", challenge.family.answer)
	}
	for _, tt := range []struct {
		name     string
		answer   ChallengeAnswer
		gaveUp   bool
		cause    string
		human    string
		netdoc   string
		expected string
	}{
		{"human right, netdoc wrong", right, false, "", ChallengeCorrect, ChallengeIncorrect, ChallengeHumanWins},
		{"human wrong, netdoc right", wrong, false, "connection_reset", ChallengeIncorrect, ChallengeCorrect, ChallengeNetdocWins},
		{"both right", right, false, "connection_reset", ChallengeCorrect, ChallengeCorrect, ChallengeDraw},
		{"both wrong", wrong, false, "", ChallengeIncorrect, ChallengeIncorrect, ChallengeNobodyWins},
		{"gave up, netdoc right", "", true, "connection_reset", ChallengeGaveUp, ChallengeCorrect, ChallengeNetdocWins},
		{"gave up, netdoc wrong", "", true, "", ChallengeGaveUp, ChallengeIncorrect, ChallengeNobodyWins},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := resetChallengeReport(t, challenge, tt.cause)
			result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: tt.answer, GaveUp: tt.gaveUp})
			if !result.Truth.Scoreable || result.Truth.Answer != right {
				t.Fatalf("truth = %+v", result.Truth)
			}
			if result.Human.Score != tt.human || result.NetworkDoctor.Score != tt.netdoc {
				t.Fatalf("scores = %s/%s, want %s/%s", result.Human.Score, result.NetworkDoctor.Score, tt.human, tt.netdoc)
			}
			if result.Result != tt.expected {
				t.Fatalf("result = %s, want %s", result.Result, tt.expected)
			}
		})
	}
}

// The human's answer is an input to one grading function and to nothing else.
// Changing it must not move the truth or Network Doctor's score by so much as a
// field.
func TestChallengeHumanAnswerCannotMoveTruthOrNetdoc(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	baseline := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerReset})
	for _, item := range ChallengeAnswerMenu {
		result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: item.ID})
		if !reflectEqualJSON(t, result.Truth, baseline.Truth) {
			t.Fatalf("answering %s changed the truth", item.ID)
		}
		if !reflectEqualJSON(t, result.NetworkDoctor, baseline.NetworkDoctor) {
			t.Fatalf("answering %s changed Network Doctor's score", item.ID)
		}
		want := ChallengeIncorrect
		if item.ID == AnswerReset {
			want = ChallengeCorrect
		}
		if result.Human.Score != want {
			t.Fatalf("answering %s scored %s, want %s", item.ID, result.Human.Score, want)
		}
	}
}

// A mutation that was generated and applied but left no independent evidence is
// not an answer. Scoring it would tell the player the simulator knows something
// it does not.
func TestChallengeWithoutObservedFaultIsNotScored(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	report.Evidence.TCPResets = nil
	result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: challenge.family.answer})
	if result.Truth.Scoreable || result.Truth.Answer != "" {
		t.Fatalf("truth = %+v, want unscoreable with no answer", result.Truth)
	}
	if result.Result != ChallengeNoResult {
		t.Fatalf("result = %s, want %s", result.Result, ChallengeNoResult)
	}
	if result.Human.Score != ChallengeUnscoreable || result.NetworkDoctor.Score != ChallengeUnscoreable {
		t.Fatalf("scores = %s/%s, want both unscoreable", result.Human.Score, result.NetworkDoctor.Score)
	}
	if result.Truth.Reason == "" {
		t.Fatal("an unscoreable challenge has to say why")
	}
}

// A service reset the mutation did not install cannot answer for it either: the
// evidence has to name the node the mutation aimed at.
func TestChallengeFaultObservedElsewhereIsNotScored(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	report.Evidence.TCPResets[0].Node = "some-other-node"
	result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: challenge.family.answer})
	if result.Truth.Scoreable {
		t.Fatalf("a reset on another node scored the challenge: %+v", result.Truth)
	}
}

func TestChallengeWithoutADiagnosisIsNotScored(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	report.Tests[0].Diagnosis = nil
	result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: challenge.family.answer})
	if result.Truth.Scoreable || result.Result != ChallengeNoResult {
		t.Fatalf("no diagnosis must be no matchup: %+v", result)
	}
}

func TestChallengeSimulatorFailureIsNotScored(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	for _, tt := range []struct {
		name    string
		mutate  func(*Report)
		wantHas string
	}{
		{"setup failed", func(r *Report) { r.Error = "setup failed: no namespaces" }, "did not complete"},
		{"cleanup failed", func(r *Report) { r.Cleanup = CleanupInfo{Done: false} }, "clean up"},
		{"no report", nil, "no report"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var report *Report
			if tt.mutate != nil {
				report = resetChallengeReport(t, challenge, "connection_reset")
				tt.mutate(report)
			}
			result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: challenge.family.answer})
			if result.Truth.Scoreable || result.Result != ChallengeNoResult {
				t.Fatalf("result = %+v, want no result", result)
			}
			if !strings.Contains(result.Truth.Reason, tt.wantHas) {
				t.Fatalf("reason = %q, want it to mention %q", result.Truth.Reason, tt.wantHas)
			}
		})
	}
}

// The healthy challenge needs positive evidence that the network worked. The
// absence of a measurement is not a healthy network.
func TestHealthyChallengeNeedsMeasuredHealth(t *testing.T) {
	challenge := challengeWithMutation(t, "")
	healthy := func() *Report {
		return &Report{
			Cleanup:  CleanupInfo{Done: true},
			Topology: []NodeInfo{{Name: challenge.Node, Role: "client"}},
			Tests: []TestOutcome{{Node: challenge.Node, Diagnosis: &Diagnosis{
				Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS"}}, Verdict: "ok", OK: true}}},
			Evidence: Evidence{FamilyReachability: []FamilyReachabilityEvidence{
				{Node: challenge.Node, Family: "ipv4", State: FamilyStateReachable},
				{Node: challenge.Node, Family: "ipv6", State: FamilyStateUnavailable},
			}},
		}
	}
	result := ScoreChallenge(challenge, healthy(), ChallengeSubmission{Answer: AnswerHealthy})
	if !result.Truth.Scoreable || result.Truth.Answer != AnswerHealthy {
		t.Fatalf("truth = %+v", result.Truth)
	}
	if result.Human.Score != ChallengeCorrect || result.NetworkDoctor.Score != ChallengeCorrect {
		t.Fatalf("both named a healthy network: %+v", result)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*Report)
	}{
		{"nothing measured", func(r *Report) { r.Evidence.FamilyReachability = nil }},
		{"a family was unreachable", func(r *Report) {
			r.Evidence.FamilyReachability[0].State = FamilyStateUnreachable
		}},
		{"a link was down", func(r *Report) {
			r.Evidence.Links = []LinkEvidence{{Node: challenge.Node, Segment: "lan", Up: false}}
		}},
		{"DNS was failing", func(r *Report) {
			r.Evidence.DNSQueries = []DNSQueryEvidence{{Node: "resolver", Service: "r", ActualOutcome: "SERVFAIL"}}
		}},
		{"a controlled target was unreachable", func(r *Report) {
			r.Evidence.ControlledTargets = []ControlledTargetEvidence{
				{From: challenge.Node, To: "controlled.test", Family: "ipv4", Reachable: false}}
		}},
		{"the gateway did not answer", func(r *Report) {
			no := false
			r.Evidence.Routes = []RouteEvidence{{Node: challenge.Node, Family: "ipv4", Destination: "default",
				Via: "10.0.0.1", Selected: true, GatewayReachable: &no}}
		}},
		{"a service reset connections", func(r *Report) {
			r.Evidence.TCPResets = []TCPResetEvidence{
				{Node: "target", Event: "reset", Result: "connection_reset", Count: 1}}
		}},
		{"a TLS handshake was refused", func(r *Report) {
			r.Evidence.TLS = []TLSEvidence{{Node: "target", Service: "tls-target",
				CertificateMode: TLSCertificateExpired, CertificatePresented: true,
				Result: "client_rejected_certificate", Count: 1}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := healthy()
			tt.mutate(report)
			result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerHealthy})
			if result.Truth.Scoreable {
				t.Fatalf("scored a healthy challenge on a network that was not measured healthy: %+v", result.Truth)
			}
		})
	}
}

// Every fault a challenge can inject has to be a fault the healthy oracle would
// have caught, or "healthy" means "we did not look" for that family. The
// mapping is spelled out in healthyObserved's comment; this keeps it honest by
// requiring a rejecting check to exist for each one.
func TestHealthyChallengeCoversEveryFamily(t *testing.T) {
	challenge := challengeWithMutation(t, "")
	// Each entry is the evidence the named family leaves behind, dropped into an
	// otherwise-healthy report. It must stop the healthy verdict.
	contradictions := map[string]func(*Report){
		"dns.servfail": func(r *Report) {
			r.Evidence.DNSQueries = []DNSQueryEvidence{{Node: "resolver", Service: "r", ActualOutcome: "SERVFAIL"}}
		},
		"dns.drop": func(r *Report) {
			r.Evidence.DNSQueries = []DNSQueryEvidence{{Node: "resolver", Service: "r", ActualOutcome: "DROPPED"}}
		},
		"service.tcp_reset": func(r *Report) {
			r.Evidence.TCPResets = []TCPResetEvidence{
				{Node: "target", Event: "reset", Result: "connection_reset", Count: 1}}
		},
		"service.tls_expired": func(r *Report) {
			r.Evidence.TLS = []TLSEvidence{{Node: "target", Service: "tls-target",
				CertificateMode: TLSCertificateExpired, CertificatePresented: true,
				Result: "client_rejected_certificate", Count: 1}}
		},
		"family.ipv4_drop": func(r *Report) {
			r.Evidence.FamilyReachability[0].State = FamilyStateUnreachable
		},
		"family.ipv6_drop": func(r *Report) {
			r.Evidence.FamilyReachability[1] = FamilyReachabilityEvidence{
				Node: challenge.Node, Family: "ipv6", State: FamilyStateUnreachable}
		},
		"routing.preferred_path_failure": func(r *Report) {
			r.Evidence.ControlledTargets = []ControlledTargetEvidence{
				{From: challenge.Node, To: "controlled.test", Family: "ipv4", Reachable: false}}
		},
	}
	for _, family := range challengeFamilies {
		if family.mutation == "" {
			continue
		}
		contradict, ok := contradictions[family.mutation]
		if !ok {
			t.Fatalf("%s can be injected, but nothing here proves the healthy oracle would notice it",
				family.mutation)
		}
		t.Run(family.mutation, func(t *testing.T) {
			report := &Report{
				Cleanup:  CleanupInfo{Done: true},
				Topology: []NodeInfo{{Name: challenge.Node, Role: "client"}},
				Tests: []TestOutcome{{Node: challenge.Node, Diagnosis: &Diagnosis{
					Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS"}}, Verdict: "ok", OK: true}}},
				Evidence: Evidence{FamilyReachability: []FamilyReachabilityEvidence{
					{Node: challenge.Node, Family: "ipv4", State: FamilyStateReachable},
					{Node: challenge.Node, Family: "ipv6", State: FamilyStateReachable},
				}},
			}
			contradict(report)
			result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerHealthy})
			if result.Truth.Scoreable {
				t.Fatalf("evidence of %s left a healthy challenge scoreable as healthy: %+v",
					family.mutation, result.Truth)
			}
		})
	}
}

// The recognition half of every family, as a table. Network Doctor is graded on
// what its own report says about the network, never on a Challenge-only
// expected label — for the three conditions the hunt oracle already grades, the
// rule here is that oracle's, reused rather than restated.
func TestChallengeRecognizesNetdocsOwnVocabulary(t *testing.T) {
	fail := func(id, cause string) *Diagnosis {
		return &Diagnosis{Verdict: "service", Checks: []DiagnosisCheck{{ID: id, Status: "FAIL", Cause: cause}}}
	}
	healthy := &Diagnosis{Verdict: diagnostic.VerdictOK, OK: true,
		Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS"}, {ID: "http", Status: "PASS"}}}
	for _, tt := range []struct {
		mutation  string
		recognize []*Diagnosis
		reject    []*Diagnosis
	}{
		{"dns.servfail",
			[]*Diagnosis{fail("dns", "dns_temporary_failure"), fail("dns", "dns_timeout"), fail("dns", "")},
			[]*Diagnosis{healthy, fail("http", "dns_temporary_failure")}},
		{"dns.drop",
			[]*Diagnosis{fail("dns", "dns_timeout")},
			[]*Diagnosis{healthy}},
		{"service.tcp_reset",
			[]*Diagnosis{fail("http", "connection_reset"), fail("ssh_banner", "connection_reset")},
			// netdoc's own words for this run are "application-layer or proxy
			// block", which names a different fault with a different fix. The hunt
			// already tracks the same gap as tcp_reset_not_distinguished.
			[]*Diagnosis{healthy, fail("http", ""), fail("http", "timeout")}},
		{"service.tls_expired",
			[]*Diagnosis{fail("tls", "certificate_expired"), fail("https", "certificate_expired")},
			[]*Diagnosis{healthy, fail("tls", "tls_handshake_failure")}},
		{"family.ipv4_drop",
			[]*Diagnosis{fail("internet_tcp", "ipv4_unreachable"),
				{Checks: []DiagnosisCheck{{ID: "internet_tcp", Status: "WARN",
					Families: &DiagnosisFamilies{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable}}}}},
			[]*Diagnosis{healthy, fail("internet_tcp", "ipv6_unreachable")}},
		{"family.ipv6_drop",
			[]*Diagnosis{fail("internet_tcp", "ipv6_unreachable")},
			[]*Diagnosis{healthy, fail("internet_tcp", "ipv4_unreachable")}},
		{"routing.preferred_path_failure",
			[]*Diagnosis{fail("route", "preferred_route_failed")},
			[]*Diagnosis{healthy, fail("route", "gateway_unreachable"), fail("route", "no_default_route")}},
	} {
		family, ok := challengeFamilyFor(tt.mutation)
		if !ok {
			t.Fatalf("%s is no longer a challenge family; drop it here too", tt.mutation)
		}
		t.Run(tt.mutation, func(t *testing.T) {
			for _, d := range tt.recognize {
				if !family.recognized(d) {
					t.Errorf("%s was not recognized as %s: %+v", tt.mutation, family.answer, d.Checks)
				}
			}
			for _, d := range tt.reject {
				if family.recognized(d) {
					t.Errorf("%s was scored correct on a diagnosis that does not name it: %+v", tt.mutation, d.Checks)
				}
			}
		})
	}
	// The healthy family is the same question asked of a whole report.
	healthyFamily := healthyChallengeFamily()
	if !healthyFamily.recognized(healthy) || healthyFamily.recognized(fail("dns", "dns_timeout")) {
		t.Fatal("the healthy family does not follow netdoc's own verdict")
	}
}

// Network Doctor is graded on the run the player was asked about, and on its
// own structured output alone.
func TestNetdocIsGradedOnThePrimaryClientRun(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "")
	// A later run against a different target that does classify a reset must not
	// be borrowed as an answer to the question the player was asked.
	report.Tests = append(report.Tests, TestOutcome{Node: challenge.Node, Target: "10.0.0.9:80",
		Diagnosis: &Diagnosis{Checks: []DiagnosisCheck{{ID: "http", Status: "FAIL", Cause: "connection_reset"}}}})
	result := ScoreChallenge(challenge, report, ChallengeSubmission{GaveUp: true})
	if result.NetworkDoctor.Score != ChallengeIncorrect {
		t.Fatalf("netdoc scored %s from a run the challenge did not ask about", result.NetworkDoctor.Score)
	}
}

func TestChallengeJSONIsDeterministicExceptTiming(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	render := func(elapsed, netdocRun time.Duration) string {
		report := resetChallengeReport(t, challenge, "connection_reset")
		report.Tests[0].Duration = netdocRun
		result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerReset, Elapsed: elapsed})
		result.Timing = ChallengeTiming{}
		var out bytes.Buffer
		if err := result.WriteJSON(&out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first := render(90*time.Second, 3*time.Second)
	second := render(4*time.Second, 900*time.Millisecond)
	if first != second {
		t.Fatalf("challenge JSON is not deterministic:\n%s\n%s", first, second)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(first), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"challenge_id", "difficulty", "truth", "human", "network_doctor", "netdoc", "result", "replay"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("challenge JSON has no %q field", field)
		}
	}
}

// A challenge result is only reproducible if it says which Network Doctor
// produced it. The path is not carried alongside the run — it is read back off
// the argv the run was built from, so a result cannot name a binary other than
// the one that executed.
func TestChallengeResultRecordsTheNetdocThatRan(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	env := &fakeEnv{stdout: `{"checks":[{"id":"http","status":"FAIL"}],"verdict":"service"}`}
	backend := &fakeBackend{caps: Capabilities{Supported: true, Backend: "fake"}, env: env}
	result, err := RunChallenge(context.Background(), challenge, backend, ChallengeOptions{
		Run:           Options{Netdoc: "/opt/builds/netdoc"},
		NetdocVersion: "netdoc v1.2.3",
		Play: func(context.Context, *ChallengeSession) (ChallengeSubmission, error) {
			return ChallengeSubmission{Answer: AnswerReset}, nil
		},
	})
	if err != nil {
		t.Fatalf("run challenge: %v", err)
	}
	if result.Netdoc.Path != "/opt/builds/netdoc" || result.Netdoc.Version != "netdoc v1.2.3" {
		t.Fatalf("result records %+v", result.Netdoc)
	}
	if len(env.lastArgv) == 0 || env.lastArgv[0] != result.Netdoc.Path {
		t.Fatalf("netdoc ran as %v, but the result names %q", env.lastArgv, result.Netdoc.Path)
	}

	var out bytes.Buffer
	if err := result.WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Netdoc NetdocIdentity `json:"netdoc"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Netdoc != result.Netdoc {
		t.Fatalf("challenge JSON carries %+v, want %+v", decoded.Netdoc, result.Netdoc)
	}

	// The reveal is the one place a person sees it; per-test output stays clean.
	var text bytes.Buffer
	result.WriteText(&text)
	for _, want := range []string{"/opt/builds/netdoc", "netdoc v1.2.3"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the reveal does not show %q:\n%s", want, text.String())
		}
	}
}

// A development build reports `dev`, and that is the honest identity of the
// thing that ran. Nothing may substitute a release-looking version for it.
func TestChallengeResultKeepsADevelopmentVersion(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerReset})
	result.Netdoc = NetdocIdentity{Path: "/home/dev/network-doctor/netdoc", Version: "netdoc dev"}
	var out bytes.Buffer
	if err := result.WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"version": "netdoc dev"`) {
		t.Fatalf("challenge JSON did not preserve a development version:\n%s", out.String())
	}
}

// The share block is the thing people paste in public. It must not name the
// fault, or the first person to post one spoils the challenge for everybody who
// reads it.
func TestChallengeShareBlockNamesNoFault(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	report := resetChallengeReport(t, challenge, "connection_reset")
	result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: AnswerReset, Note: "the service resets"})
	share := strings.ToLower(result.Share())
	for _, secret := range []string{string(result.Truth.Answer), strings.ToLower(result.Truth.Label),
		strings.ToLower(result.Truth.Explanation), strings.ToLower(result.Truth.Injected),
		"connection_reset", challenge.Base, "the service resets"} {
		if secret != "" && strings.Contains(share, secret) {
			t.Fatalf("share block leaks %q:\n%s", secret, result.Share())
		}
	}
	if !strings.Contains(result.Share(), challenge.ID) {
		t.Fatalf("share block has to carry the id:\n%s", result.Share())
	}
}

func TestNormalizeChallengeID(t *testing.T) {
	// Bare, prefixed, lowercase and hash-prefixed are all one id, and a bare id
	// means V1 permanently — it was the only form the first release printed.
	for _, raw := range []string{"8f42c1", " #8F42C1 ", "8F42C1", "v1-8f42c1", "#V1-8F42C1"} {
		got, err := NormalizeChallengeID(raw)
		if err != nil || got != "V1-8F42C1" {
			t.Fatalf("NormalizeChallengeID(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "8F42C", "8F42C11", "8G42C1", "../../etc", "8F 42C1",
		"V2-8F42C1", "V1-", "-8F42C1", "V1-8F42C1-extra"} {
		if _, err := NormalizeChallengeID(raw); err == nil {
			t.Fatalf("NormalizeChallengeID(%q) was accepted", raw)
		}
	}
	if _, err := BuildChallenge("V9-8F42C1"); err == nil {
		t.Fatal("an id whose version this build cannot resolve must be rejected, not guessed at")
	}
	// A future version reusing the same digits must be a different challenge.
	if challengeSeed("V1", "8F42C1") == challengeSeed("V2", "8F42C1") {
		t.Fatal("the id version is not part of the seed domain")
	}
}

func TestFindChallengeHonoursDifficulty(t *testing.T) {
	for _, difficulty := range ChallengeDifficulties {
		challenge, err := FindChallenge(difficulty)
		if err != nil {
			t.Fatalf("find %s: %v", difficulty, err)
		}
		if challenge.Difficulty != difficulty {
			t.Fatalf("asked for %s, got %s", difficulty, challenge.Difficulty)
		}
	}
	if _, err := FindChallenge("impossible"); err == nil {
		t.Fatal("an unknown difficulty must be rejected")
	}
}

// RunChallenge hands the player the network before netdoc has run, and hands
// netdoc nothing about the challenge.
func TestRunChallengeOrderAndNetdocArguments(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	env := &fakeEnv{stdout: `{"checks":[{"id":"http","status":"FAIL"}],"verdict":"service"}`}
	backend := &fakeBackend{caps: Capabilities{Supported: true, Backend: "fake"}, env: env}
	execsWhenPlayed := -1
	_, err := RunChallenge(context.Background(), challenge, backend, ChallengeOptions{
		Run: Options{Netdoc: "/bin/netdoc"},
		Play: func(_ context.Context, session *ChallengeSession) (ChallengeSubmission, error) {
			execsWhenPlayed = env.execs
			if session.Challenge.ID != challenge.ID {
				t.Errorf("session carries challenge %s", session.Challenge.ID)
			}
			return ChallengeSubmission{Answer: AnswerReset}, nil
		},
	})
	if err != nil {
		t.Fatalf("run challenge: %v", err)
	}
	if execsWhenPlayed != 0 {
		t.Fatalf("netdoc ran %d times before the player answered", execsWhenPlayed)
	}
	if env.execs == 0 {
		t.Fatal("netdoc never ran")
	}
	if env.cleanups == 0 {
		t.Fatal("the challenge network was not cleaned up")
	}
	for _, arg := range env.lastArgv {
		for _, secret := range []string{challenge.ID, challenge.Base, "service.tcp_reset", "challenge", "mutation"} {
			if strings.Contains(arg, secret) {
				t.Fatalf("netdoc was told %q: %v", secret, env.lastArgv)
			}
		}
	}
	for _, kv := range env.lastEnv {
		if strings.Contains(strings.ToUpper(kv), "CHALLENGE") {
			t.Fatalf("netdoc's environment carries %q", kv)
		}
	}
}

// A player who walks out has not been scored, and the network still goes away.
func TestRunChallengeAbandonedRunsNoTestsAndCleansUp(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	env := &fakeEnv{}
	backend := &fakeBackend{caps: Capabilities{Supported: true, Backend: "fake"}, env: env}
	result, err := RunChallenge(context.Background(), challenge, backend, ChallengeOptions{
		Run: Options{Netdoc: "/bin/netdoc"},
		Play: func(context.Context, *ChallengeSession) (ChallengeSubmission, error) {
			return ChallengeSubmission{}, errors.New("challenge abandoned")
		},
	})
	if err == nil || result != nil {
		t.Fatalf("an abandoned challenge is not a result: %v, %+v", err, result)
	}
	if env.execs != 0 {
		t.Fatalf("netdoc ran %d times after the challenge was abandoned", env.execs)
	}
	if env.cleanups == 0 {
		t.Fatal("an abandoned challenge did not clean up")
	}
}

func reflectEqualJSON(t *testing.T, a, b any) bool {
	t.Helper()
	one, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	two, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(one, two)
}
