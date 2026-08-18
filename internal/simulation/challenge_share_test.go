package simulation

import (
	"strings"
	"testing"
)

// shareResult is a finished matchup with nothing in it but what the share block
// is allowed to read, so a golden here fails for a format change and not for a
// generator change somewhere else.
func shareResult(mutate func(*ChallengeResult)) *ChallengeResult {
	r := &ChallengeResult{
		ChallengeID: "V4-8F42C1", Difficulty: "easy", Daily: "2026-03-04",
		Human:         ChallengeContestant{Score: ChallengeCorrect, Label: "TCP port blocked"},
		NetworkDoctor: ChallengeContestant{Score: ChallengeIncorrect, Label: "healthy"},
		Result:        ChallengeHumanWins,
		Timing:        ChallengeTiming{HumanMS: 200_000, NetdocMS: 4_000},
		Replay:        "netdoc-sim challenge -id V4-8F42C1",
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

// The share block is a user-facing contract: it is pasted into chats and posts
// where nobody will run it through a renderer, so it is frozen exactly rather
// than checked for substrings.
func TestChallengeShareBlockIsAPostableResult(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*ChallengeResult)
		want   string
	}{
		{
			name: "the player wins the daily",
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me ✅   🤖 Network Doctor ❌\n" +
				"🏆 I beat Network Doctor in 3m 20s\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name: "netdoc wins a challenge that was not the daily",
			mutate: func(r *ChallengeResult) {
				r.Daily = ""
				r.Human.Score, r.NetworkDoctor.Score = ChallengeIncorrect, ChallengeCorrect
				r.Result = ChallengeNetdocWins
				r.Timing.HumanMS = 45_000
			},
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"🧑 Me ❌   🤖 Network Doctor ✅\n" +
				"🤖 Network Doctor beat me in 45s\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name: "both get it",
			mutate: func(r *ChallengeResult) {
				r.NetworkDoctor.Score, r.Result = ChallengeCorrect, ChallengeDraw
			},
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me ✅   🤖 Network Doctor ✅\n" +
				"🤝 Draw, we both got it in 3m 20s\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name: "neither gets it, and netdoc had no word for it",
			mutate: func(r *ChallengeResult) {
				r.Human.Score, r.NetworkDoctor.Score = ChallengeIncorrect, ChallengeUnrecognized
				r.Result = ChallengeNobodyWins
			},
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me ❌   🤖 Network Doctor ❌\n" +
				"😵 Neither of us got it in 3m 20s\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name: "the player gives up",
			mutate: func(r *ChallengeResult) {
				r.Human.Score, r.Human.Label = ChallengeGaveUp, ""
				r.NetworkDoctor.Score, r.Result = ChallengeCorrect, ChallengeNetdocWins
			},
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me 🏳️ (gave up)   🤖 Network Doctor ✅\n" +
				"🤖 Network Doctor beat me in 3m 20s\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name: "nothing could be scored",
			mutate: func(r *ChallengeResult) {
				r.Human.Score, r.NetworkDoctor.Score = "", ""
				r.Result = ChallengeNoResult
			},
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me ➖ (no result)   🤖 Network Doctor ➖ (no result)\n" +
				"➖ No result\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
		{
			name:   "a submission with no session behind it posts no time",
			mutate: func(r *ChallengeResult) { r.Timing.HumanMS = 0 },
			want: "🩺 Network Doctor Challenge V4-8F42C1 (easy)\n" +
				"📅 Daily 2026-03-04\n" +
				"🧑 Me ✅   🤖 Network Doctor ❌\n" +
				"🏆 I beat Network Doctor\n" +
				"🔁 Your turn: netdoc-sim challenge -id V4-8F42C1\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := shareResult(tt.mutate)
			got := result.Share()
			if got != tt.want {
				t.Errorf("share block\n got:\n%s\nwant:\n%s", got, tt.want)
			}
			// Same finished result, same bytes: the block is the artifact people
			// compare, so it cannot depend on when it was rendered.
			if again := result.Share(); again != got {
				t.Errorf("share block is not deterministic:\n%s\n%s", got, again)
			}
		})
	}
}

// The block used to be a terminal column, `Me: ✓ / Network Doctor: ✗`, which
// read as report output wherever it was pasted. The marks and the labels are
// the format now, and a regression to the old one is what this catches.
func TestChallengeShareBlockIsNotAReportColumn(t *testing.T) {
	got := shareResult(nil).Share()
	for _, gone := range []string{"✓", "✗", "Me:", "Network Doctor:", "YOU BEAT NETWORK DOCTOR"} {
		if strings.Contains(got, gone) {
			t.Errorf("share block still carries the terminal-report form %q:\n%s", gone, got)
		}
	}
	// It is pasted, never rendered: styling bytes would arrive as garbage in a
	// chat client, and the clipboard copies exactly what is printed.
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("share block carries an escape sequence:\n%q", got)
	}
	// Compact enough to post without a reader scrolling past it.
	if lines := strings.Count(got, "\n"); lines > 5 {
		t.Errorf("share block grew to %d lines; it is meant to be postable:\n%s", lines, got)
	}
	// The identity is the reason a post is worth reading: it is what lets
	// somebody play the same puzzle, and what makes two dailies comparable.
	for _, want := range []string{"V4-8F42C1", "2026-03-04", "netdoc-sim challenge -id V4-8F42C1"} {
		if !strings.Contains(got, want) {
			t.Errorf("share block lost %q, which is what makes it replayable:\n%s", want, got)
		}
	}
}
