package simulation

import (
	"reflect"
	"testing"
)

// An authored challenge is a claim: this scenario, plus this fault, teaches
// this diagnosis. This is what turns the claim into something the build
// checks. An entry whose declaration has come apart from the condition table,
// whose mutation no longer applies to its base, or whose fault lands somewhere
// the player was never pointed at fails here rather than reaching a player.
func TestAuthoredChallengesAreValid(t *testing.T) {
	if len(authoredChallenges) == 0 {
		t.Fatal("no authored challenges")
	}
	seenSlug := map[string]bool{}
	seenID := map[string]string{}
	for _, entry := range authoredChallenges {
		t.Run(entry.slug, func(t *testing.T) {
			if seenSlug[entry.slug] {
				t.Fatalf("duplicate slug %q", entry.slug)
			}
			seenSlug[entry.slug] = true
			// Two slugs hashing to one id would silently make one of them
			// unplayable, and the other one ambiguous to anybody replaying it.
			if other, clash := seenID[entry.id()]; clash {
				t.Fatalf("%q and %q both mint id %s", entry.slug, other, entry.id())
			}
			seenID[entry.id()] = entry.slug

			challenge, err := BuildChallenge(entry.id())
			if err != nil {
				t.Fatalf("build %s: %v", entry.id(), err)
			}
			if len(challenge.Manifest.Mutations) != 1 {
				t.Fatalf("authored challenges set exactly one fault, got %d", len(challenge.Manifest.Mutations))
			}
			if got := challenge.Manifest.Mutations[0].ID; got != entry.mutation {
				t.Fatalf("declared mutation %s, built %s", entry.mutation, got)
			}
			// The declared diagnosis has to be the one the condition table says
			// this mutation establishes. build() enforces it; this proves it.
			condition, ok := challengeConditionFor(entry.mutation)
			if !ok {
				t.Fatalf("%s is not a challenge condition", entry.mutation)
			}
			if condition.answer != entry.answer {
				t.Fatalf("declared %s, table establishes %s", entry.answer, condition.answer)
			}
			if challenge.Base != entry.base {
				t.Fatalf("declared base %s, built on %s", entry.base, challenge.Base)
			}
			if challenge.Node == "" || challenge.Target == "" {
				t.Fatalf("authored challenge has no node or target to brief: %+v", challenge)
			}
			if challenge.Difficulty == "" {
				t.Fatalf("authored challenge carries no difficulty")
			}
			// The evidence model has to be able to evaluate it, or it is a case
			// nobody could be scored on.
			if condition.signature == nil {
				t.Fatalf("%s has no evidence signature, so this case could not be graded", entry.mutation)
			}
		})
	}
}

// An authored id is a shareable artifact like any other, so it has to keep
// resolving to the same network. Slug-derived rather than position-derived is
// the property being checked: reordering the table must not move anything.
func TestAuthoredChallengesReplayDeterministically(t *testing.T) {
	for _, entry := range authoredChallenges {
		t.Run(entry.slug, func(t *testing.T) {
			first, err := BuildChallenge(entry.id())
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			second, err := BuildChallenge(entry.id())
			if err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			if !reflect.DeepEqual(first.Manifest, second.Manifest) {
				t.Fatalf("the same authored id produced two manifests:\n%+v\n%+v", first.Manifest, second.Manifest)
			}
			if first.Target != second.Target || first.Node != second.Node ||
				first.Difficulty != second.Difficulty {
				t.Fatalf("the same authored id produced two briefings: %+v %+v", first, second)
			}
			// Reaching the same case by slug and by id has to be the same case,
			// or choosing one way and replaying the other would differ.
			bySlug, err := AuthoredChallengeByID(entry.slug)
			if err != nil {
				t.Fatalf("by slug: %v", err)
			}
			if bySlug.ID != first.ID || !reflect.DeepEqual(bySlug.Manifest, first.Manifest) {
				t.Fatalf("slug %q and id %s resolved differently", entry.slug, entry.id())
			}
		})
	}
}

// Authored ids are frozen the moment they are published, exactly as generated
// ids are. This pins the id each slug mints, so a change to the derivation
// fails here instead of quietly repointing a case somebody linked to.
func TestAuthoredChallengeIDsAreFrozen(t *testing.T) {
	for _, tt := range []struct{ slug, id string }{
		{"refused-vs-blocked-refused", "A1-F78DCB"},
		{"refused-vs-blocked-blocked", "A1-0DD077"},
		{"reset-after-accept", "A1-493CD9"},
		{"certificate-expired", "A1-BB32AE"},
		{"certificate-wrong-name", "A1-C30E0A"},
		{"no-default-route", "A1-BE3F71"},
		{"wrong-default-route", "A1-0CA25C"},
		{"missing-subnet-route", "A1-48CFF9"},
	} {
		found, ok := AuthoredChallengeBySlug(tt.slug)
		if !ok {
			t.Errorf("authored challenge %q has been removed; it was published as %s", tt.slug, tt.id)
			continue
		}
		if found.ID != tt.id {
			t.Errorf("%q was published as %s and now mints %s; that repoints an id somebody may have shared",
				tt.slug, tt.id, found.ID)
		}
	}
}

// Authored cases go through the one scoring engine, with no path of their own.
// The intended diagnosis has to win and a wrong one has to lose, graded from
// the same simulator evidence a generated challenge is graded from.
func TestAuthoredChallengesScoreThroughTheSharedEngine(t *testing.T) {
	for _, entry := range authoredChallenges {
		t.Run(entry.slug, func(t *testing.T) {
			challenge, err := BuildChallenge(entry.id())
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			report := observedReportFor(t, challenge)
			result := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: entry.answer})
			if !result.Truth.Scoreable {
				t.Fatalf("authored case is not scoreable: %s", result.Truth.Reason)
			}
			if result.Truth.Answer != entry.answer {
				t.Fatalf("truth is %s, the case declares %s", result.Truth.Answer, entry.answer)
			}
			if result.Human.Score != ChallengeCorrect {
				t.Fatalf("the declared diagnosis was not scored correct: %+v", result.Human)
			}
			// Every other playable answer has to lose. An authored case that
			// accepted a second answer would be ambiguous.
			for _, other := range challengePlayableAnswers() {
				if other == entry.answer {
					continue
				}
				wrong := ScoreChallenge(challenge, report, ChallengeSubmission{Answer: other})
				if wrong.Human.Score == ChallengeCorrect {
					t.Errorf("%s was also scored correct, so this case has two answers", other)
				}
			}
		})
	}
}

// observedReportFor builds the report a run of this challenge would leave
// behind: independent evidence that the injected fault met live traffic, and
// nothing netdoc said. It is the fixture the scoring tests grade against.
func observedReportFor(t *testing.T, c *Challenge) *Report {
	t.Helper()
	report := &Report{
		Cleanup:  CleanupInfo{Done: true},
		Topology: []NodeInfo{{Name: c.Node, Role: "client"}},
		Tests: []TestOutcome{{Node: c.Node, Diagnosis: &Diagnosis{
			Checks: []DiagnosisCheck{{ID: "dns", Status: "PASS"}}, Verdict: "ok", OK: true}}},
	}
	mutation := c.Manifest.Mutations[0]
	report.Evidence = evidenceEstablishing(c, mutation)
	return report
}

// evidenceEstablishing is the observation each authored family's scoped
// predicate demands. It is written per family on purpose: this is a test
// fixture standing in for what the kernel and the services would have recorded,
// and generating it from the predicate under test would prove nothing.
func evidenceEstablishing(c *Challenge, m GeneratedMutation) Evidence {
	reachable := []FamilyReachabilityEvidence{
		{Node: c.Node, Family: "ipv4", State: FamilyStateReachable},
		{Node: c.Node, Family: "ipv6", State: FamilyStateUnavailable},
	}
	switch m.ID {
	case "service.connection_refused":
		return Evidence{FamilyReachability: reachable,
			ControlledTargets: []ControlledTargetEvidence{
				{From: c.Node, To: m.TargetEndpoint, Family: "ipv4", Outcome: TargetStateRefused}}}
	case "service.tcp_port_blocked":
		return Evidence{FamilyReachability: reachable,
			ControlledTargets: []ControlledTargetEvidence{
				{From: c.Node, To: m.TargetEndpoint, Family: "ipv4", Outcome: FamilyStateUnreachable}},
			PacketDrops: []PacketDropEvidence{{Node: m.Node, Protocol: "tcp", Port: m.TargetPort,
				Direction: DirectionInbound, Packets: 4}}}
	case "service.tcp_reset":
		return Evidence{FamilyReachability: reachable,
			TCPResets: []TCPResetEvidence{{Node: m.Node, Service: m.Service,
				Event: "reset", Result: "connection_reset", Count: 1}}}
	case "service.tls_expired":
		return Evidence{FamilyReachability: reachable,
			TLS: []TLSEvidence{{Node: m.Node, Service: m.Service, CertificateMode: TLSCertificateExpired,
				CertificatePresented: true, Result: "client_rejected_certificate", Count: 1}}}
	case "service.tls_hostname_mismatch":
		return Evidence{FamilyReachability: reachable,
			TLS: []TLSEvidence{{Node: m.Node, Service: m.Service, CertificateMode: TLSCertificateHostnameMismatch,
				RequestedServer: "requested.test", CertificateDNS: []string{"somewhere-else.test"},
				CertificatePresented: true, Result: "client_rejected_certificate", Count: 1}}}
	case "routing.no_default_route":
		return Evidence{
			FamilyReachability: []FamilyReachabilityEvidence{
				{Node: c.Node, Family: m.Family, State: FamilyStateUnreachable}},
			RouteTables: []RouteTableEvidence{{Node: m.Node, Family: m.Family,
				Routes: []KernelRoute{{Destination: "10.77.0.0/24", Segment: "client-lan"}}}}}
	case "routing.wrong_default_route":
		yes := true
		return Evidence{
			FamilyReachability: []FamilyReachabilityEvidence{
				{Node: c.Node, Family: m.Family, State: FamilyStateUnreachable}},
			RouteTables: []RouteTableEvidence{{Node: m.Node, Family: m.Family,
				Routes: []KernelRoute{{Destination: "default", Via: m.RouteVia, Segment: m.PreferredSegment}}}},
			Routes: []RouteEvidence{{Node: m.Node, Family: m.Family, Destination: "default",
				Via: m.RouteVia, Selected: true, GatewayReachable: &yes}},
			ControlledTargets: []ControlledTargetEvidence{{From: m.Node, To: m.ControlTarget,
				Family: m.Family, Reachable: true, Outcome: FamilyStateReachable,
				Via: []string{m.PreferredVia}}}}
	case "routing.missing_subnet_route":
		return Evidence{FamilyReachability: reachable,
			RouteTables: []RouteTableEvidence{{Node: m.Node, Family: m.Family,
				Routes: []KernelRoute{{Destination: "default", Via: m.PreferredVia, Segment: m.PreferredSegment}}}},
			ControlledTargets: []ControlledTargetEvidence{
				{From: m.Node, To: m.TargetEndpoint, Family: m.Family, Outcome: FamilyStateUnreachable},
				{From: m.Node, To: m.ControlTarget, Family: m.Family, Reachable: true,
					Outcome: FamilyStateReachable}}}
	}
	return Evidence{FamilyReachability: reachable}
}
