package simulation

import (
	"slices"
	"strings"
	"testing"
)

// Curation is a claim about what an entry teaches, and a claim nobody checks is
// a wish. Every entry has to still resolve to the condition it was picked for,
// since a generator change that moved one would otherwise leave a beginner practising
// something else under a pack that promised routing.
func TestStarterPacksStayPlayable(t *testing.T) {
	seen := map[string]string{}
	for _, pack := range starterPacks {
		t.Run(pack.id, func(t *testing.T) {
			if len(pack.entries) < 3 {
				t.Fatalf("pack %s has %d entries; a pack that small is a single challenge with a name",
					pack.id, len(pack.entries))
			}
			answers := map[ChallengeAnswer]bool{}
			for _, entry := range pack.entries {
				challenge, err := BuildChallenge(entry.id)
				if err != nil {
					t.Fatalf("%s entry %s does not resolve: %v", pack.id, entry.id, err)
				}
				if challenge.condition.mutation != entry.condition {
					t.Fatalf("%s entry %s now sets %q, but was curated for %q."+
						" Pick a different id rather than relabelling this one.",
						pack.id, entry.id, nameOrHealthy(challenge.condition.mutation), nameOrHealthy(entry.condition))
				}
				if challenge.Scenario == nil {
					t.Fatalf("%s entry %s has no scenario to play", pack.id, entry.id)
				}
				if other, ok := seen[entry.id]; ok && other != pack.id {
					t.Errorf("%s is in both %s and %s", entry.id, other, pack.id)
				}
				seen[entry.id] = pack.id
				answers[challenge.condition.answer] = true
			}
			// The rule that keeps a pack a puzzle. A pack names the layer, which is
			// a hint the player asked for; a pack with one possible answer would be
			// the answer key instead.
			if len(answers) < 2 {
				t.Fatalf("every challenge in pack %s answers %v, so naming the pack gives the answer away",
					pack.id, answers)
			}
		})
	}
}

// A pack is a selection of ordinary challenges, not a second kind of challenge.
// Everything a shared id gets, a starter entry gets: the same id form, the same
// difficulty metadata, the same replay command.
func TestStarterEntriesAreOrdinaryChallenges(t *testing.T) {
	for _, pack := range StarterPacks() {
		for _, id := range pack.Challenges {
			normalized, err := NormalizeChallengeID(id)
			if err != nil {
				t.Fatalf("%s entry %q is not a challenge id: %v", pack.ID, id, err)
			}
			if normalized != id {
				t.Fatalf("%s entry %q is not written canonically (%q)", pack.ID, id, normalized)
			}
			challenge, err := BuildChallenge(id)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(ChallengeDifficulties, challenge.Difficulty) && challenge.Difficulty != "" {
				t.Fatalf("%s entry %s has difficulty %q", pack.ID, id, challenge.Difficulty)
			}
			if want := challengeCommand() + " -id " + id; challenge.Replay() != want {
				t.Fatalf("%s entry %s replays as %q, want %q", pack.ID, id, challenge.Replay(), want)
			}
		}
	}
}

// Discovery is the point of a pack, so the published view has to be stable,
// deterministic, and separate from the internal identity.
func TestStarterPackDiscovery(t *testing.T) {
	packs := StarterPacks()
	if len(packs) == 0 {
		t.Fatal("no starter packs are published")
	}
	ids := map[string]bool{}
	for _, pack := range packs {
		switch {
		case pack.ID == "" || pack.ID != strings.ToLower(pack.ID):
			t.Errorf("pack id %q is not a stable lowercase machine name", pack.ID)
		case pack.Name == "" || pack.Description == "":
			t.Errorf("pack %s has no human name or description", pack.ID)
		case pack.Name == pack.ID:
			t.Errorf("pack %s does not distinguish its machine id from its display name", pack.ID)
		case ids[pack.ID]:
			t.Errorf("duplicate pack id %s", pack.ID)
		}
		ids[pack.ID] = true

		found, ok := StarterPackByID(strings.ToUpper(pack.ID))
		if !ok {
			t.Fatalf("pack %s cannot be looked up by name", pack.ID)
		}
		if !slices.Equal(found.Challenges, pack.Challenges) {
			t.Fatalf("pack %s lists %v by name and %v in the listing", pack.ID, found.Challenges, pack.Challenges)
		}
	}
	if _, ok := StarterPackByID("no-such-pack"); ok {
		t.Fatal("an unknown pack was resolved")
	}
	// Contents and order are fixed, so `starters <pack>` prints the same ramp
	// every time and a walkthrough somebody wrote down still matches.
	if !slices.Equal(StarterPacks()[0].Challenges, packs[0].Challenges) {
		t.Fatal("pack contents are not stable between calls")
	}
	if !slices.Equal(StarterPackNames(), []string{"fundamentals", "dns", "service", "tls", "paths", "routing"}) {
		t.Fatalf("the offered order changed: %v", StarterPackNames())
	}
}

// StarterChallenge draws from the pack and nowhere else, and what it returns is
// a challenge already resolved through the ordinary path.
func TestStarterChallengeDrawsFromItsPack(t *testing.T) {
	pack, ok := StarterPackByID("routing")
	if !ok {
		t.Fatal("no routing pack")
	}
	// Enough draws to make hitting only one entry vanishingly unlikely, and every
	// draw checked for membership.
	drawn := map[string]bool{}
	for i := 0; i < 60; i++ {
		challenge, err := StarterChallenge("routing")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(pack.Challenges, challenge.ID) {
			t.Fatalf("drew %s, which is not in %v", challenge.ID, pack.Challenges)
		}
		drawn[challenge.ID] = true
	}
	if len(drawn) < 2 {
		t.Fatalf("60 draws from a %d-entry pack produced only %v", len(pack.Challenges), drawn)
	}
	if _, err := StarterChallenge("nope"); err == nil {
		t.Fatal("an unknown pack must be rejected rather than drawn from")
	}
}

// The curated condition is metadata for this test and must never reach a player.
// It is the answer to every starter challenge, written down in the source.
func TestStarterCurationNeverReachesThePlayer(t *testing.T) {
	for _, pack := range starterPacks {
		for _, entry := range pack.entries {
			challenge, err := BuildChallenge(entry.id)
			if err != nil {
				t.Fatal(err)
			}
			var briefing strings.Builder
			challenge.WriteBriefing(&briefing)
			if entry.condition != "" && strings.Contains(briefing.String(), entry.condition) {
				t.Fatalf("the briefing for starter %s names its condition:\n%s", entry.id, briefing.String())
			}
		}
	}
	// And the published view carries ids only, with no conditions and no answers.
	for _, pack := range StarterPacks() {
		blob := pack.ID + pack.Name + pack.Description + strings.Join(pack.Challenges, " ")
		for _, condition := range challengeConditions {
			if condition.mutation != "" && strings.Contains(blob, condition.mutation) {
				t.Fatalf("published pack %s names the condition %q", pack.ID, condition.mutation)
			}
		}
	}
}
