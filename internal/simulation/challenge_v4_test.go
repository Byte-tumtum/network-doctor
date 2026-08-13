package simulation

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// v4SampleIDs is the deterministic corpus the V4 tests share. Fixed rather than
// random so a failure is reproducible from the test name alone.
func v4SampleIDs(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("V4-%06X", (i*7919)&0xFFFFFF))
	}
	return out
}

// A V4 id is the whole reproduction contract, same as every earlier version.
func TestV4ChallengesReplayDeterministically(t *testing.T) {
	for _, id := range v4SampleIDs(60) {
		first, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		second, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("rebuild %s: %v", id, err)
		}
		if !reflect.DeepEqual(first.Manifest, second.Manifest) {
			t.Fatalf("%s produced two manifests:\n%+v\n%+v", id, first.Manifest, second.Manifest)
		}
		if first.Base != second.Base || first.Case != second.Case ||
			first.Node != second.Node || first.Target != second.Target ||
			first.Difficulty != second.Difficulty {
			t.Fatalf("%s produced two briefings: %+v %+v", id, first, second)
		}
	}
}

// Every V4 challenge has to land inside the truth model. A generated case whose
// answer no condition establishes would be one nobody could be scored on.
func TestV4OnlyGeneratesPlayableChallenges(t *testing.T) {
	playable := challengePlayableAnswers()
	for _, id := range v4SampleIDs(120) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		answer := challenge.condition.answer
		if answer == AnswerHealthy {
			if len(challenge.Manifest.Mutations) != 0 {
				t.Fatalf("%s is healthy but carries %d mutations", id, len(challenge.Manifest.Mutations))
			}
			continue
		}
		if !slices.Contains(playable, answer) {
			t.Fatalf("%s resolves to %s, which is not in the playable truth set", id, answer)
		}
		if len(challenge.Manifest.Mutations) != 1 {
			t.Fatalf("%s sets %d faults; a challenge with two would have two defensible answers",
				id, len(challenge.Manifest.Mutations))
		}
		// The case the search settled on has to be the one the family choice
		// asked for, or the search is still choosing the diagnosis.
		if got := challenge.Manifest.Mutations[0].ID; got != challenge.condition.mutation {
			t.Fatalf("%s selected %s but resolved to a case setting %s", id, challenge.condition.mutation, got)
		}
		if challenge.condition.signature == nil {
			t.Fatalf("%s resolves to %s, which has no evidence signature", id, challenge.condition.mutation)
		}
	}
}

// V4 exists because V1 to V3 let three implementation details decide what the
// game was about: how many mutation variants a family has, how many bases its
// operator applies to, and which case the scan reached first. Measured on V3
// that put DNS at roughly 23% and a missing subnet route under 2%.
//
// The invariant here is deliberately not a demand for exact percentages, which
// would be brittle. It is that every intended family is reachable and none of
// them runs away with the distribution.
func TestV4DistributionIsUniformOverAnswers(t *testing.T) {
	const sample = 1400
	counts := map[ChallengeAnswer]int{}
	built := 0
	for _, id := range v4SampleIDs(sample) {
		challenge, err := BuildChallenge(id)
		if err != nil {
			t.Fatalf("build %s: %v", id, err)
		}
		built++
		counts[challenge.condition.answer]++
	}
	playable := challengePlayableAnswers()
	// Healthy takes one draw in challengeHealthyOdds; the rest is split evenly
	// over the playable answers, which is the intended policy.
	faults := built - counts[AnswerHealthy]
	expected := float64(faults) / float64(len(playable))
	for _, answer := range playable {
		got := counts[answer]
		if got == 0 {
			t.Errorf("%s is playable but never generated in %d ids, so it is unreachable", answer, sample)
			continue
		}
		// A wide band: this is a uniformity check, not a chi-squared test. It
		// catches a family that has become dominant or nearly unreachable, which
		// is the failure V4 was built to prevent.
		if float64(got) < expected*0.5 || float64(got) > expected*2 {
			t.Errorf("%s appeared %d times against an intended %.0f; the distribution is no longer the one chosen",
				answer, got, expected)
		}
	}
	// Healthy has to stay a real possibility, or the game teaches people to
	// always find something.
	if counts[AnswerHealthy] == 0 {
		t.Error("no healthy challenge in the sample")
	}
	for answer := range counts {
		if answer != AnswerHealthy && !slices.Contains(playable, answer) {
			t.Errorf("%s was generated but is not in the playable set", answer)
		}
	}
}

// The V4 bases for a mutation are derived by asking the operator, not written
// down. This proves the derivation agrees with what generation actually does.
func TestV4BasesForMutationAreDerivedFromTheOperator(t *testing.T) {
	for _, condition := range challengeConditions {
		if condition.mutation == "" {
			continue
		}
		bases, err := challengeBasesForMutation(condition.mutation)
		if err != nil {
			t.Errorf("%s: %v", condition.mutation, err)
			continue
		}
		if len(bases) == 0 {
			t.Errorf("%s is a challenge condition that no control scenario can express, "+
				"so V4 would never resolve an id that selected it", condition.mutation)
		}
		for _, base := range bases {
			if !slices.Contains(challengeBasesV3, base) {
				t.Errorf("%s named base %q, which is not a challenge control", condition.mutation, base)
			}
		}
	}
}

// Adding V4 must not have moved anything a V3 id resolves to. The golden rows
// live in TestChallengeIDsResolveToTheSameCaseForever; this is the cheaper
// structural half — every earlier version still resolves, and each keeps its
// own frozen condition list.
func TestEarlierChallengeVersionsStillResolve(t *testing.T) {
	for _, version := range []string{"V1", "V2", "V3"} {
		if _, ok := challengeGenerators[version]; !ok {
			t.Fatalf("%s ids have been published; this build can no longer resolve them", version)
		}
	}
	for _, mutation := range challengeV2Mutations {
		if _, ok := challengeConditionFor(mutation); !ok {
			t.Fatalf("V2 ids select %s, which is no longer a challenge condition", mutation)
		}
	}
	// The daily is pinned to its own epoch table, which V4 must not have moved:
	// a historical daily naming a different challenge would break results people
	// have already posted.
	for _, epoch := range dailyEpochs {
		if _, ok := challengeGenerators[epoch.version]; !ok {
			t.Fatalf("dailies from %s resolve through %s, which this build cannot resolve",
				epoch.from, epoch.version)
		}
	}
}
