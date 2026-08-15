package simulation

import (
	"fmt"
	"strings"
)

// The fourth screen, and the only one written for somebody who was not there.
//
// The reveal in challenge_report.go explains the fault to the person who just
// played; this is posted where the next player will read it, so it carries the
// identity and the two verdicts and nothing that would narrow the answer. The
// two are separate renderers on purpose: a share block that grew out of the
// reveal would inherit the reveal's licence to say everything.
//
// It is pasted rather than printed — Slack, Discord, a GitHub comment, a post —
// so it is plain UTF-8 with no ANSI, no column alignment a proportional font
// would break, and no table. Five lines that mean something on their own.

// Share is the copyable block. The same completed result renders the same
// bytes, every time: it is the artifact people compare, so it cannot depend on
// where or when it was rendered.
func (r *ChallengeResult) Share() string {
	var b strings.Builder
	fmt.Fprintf(&b, "🩺 Network Doctor Challenge %s (%s)\n", r.ChallengeID, r.Difficulty)
	if r.Daily != "" {
		// The date is what makes two people's results the same puzzle, and it
		// names no fault, so it belongs in the block that gets posted.
		fmt.Fprintf(&b, "📅 Daily %s\n", r.Daily)
	}
	fmt.Fprintf(&b, "🧑 Me %s   🤖 Network Doctor %s\n", shareMark(r.Human.Score), shareMark(r.NetworkDoctor.Score))
	fmt.Fprintf(&b, "%s\n", shareVerdict(r))
	// The same invitation the reveal prints, so a reader of the share block and
	// a reader of the full result are told to run the same thing.
	fmt.Fprintf(&b, "🔁 Your turn: %s\n", r.Replay)
	return b.String()
}

// shareMark is one contestant's outcome, as a mark rather than a word: the
// block is read at a glance in a chat client, and the reader already knows who
// the two players are.
func shareMark(score string) string {
	switch score {
	case ChallengeCorrect:
		return "✅"
	case ChallengeIncorrect, ChallengeUnrecognized:
		// One mark for both losses. "not recognized" would tell a reader of the
		// share block that the fault is one netdoc has no words for, which
		// narrows the answer for whoever plays the id next.
		return "❌"
	case ChallengeGaveUp:
		return "🏳️ (gave up)"
	default:
		return "➖ (no result)"
	}
}

// shareVerdict is the headline, in the first person because the block is posted
// by the player. The reveal's own result line stays in the second person, where
// the reader is the player: one sentence cannot be both.
func shareVerdict(r *ChallengeResult) string {
	var line string
	switch r.Result {
	case ChallengeHumanWins:
		line = "🏆 I beat Network Doctor"
	case ChallengeNetdocWins:
		line = "🤖 Network Doctor beat me"
	case ChallengeDraw:
		line = "🤝 Draw, we both got it"
	case ChallengeNobodyWins:
		line = "😵 Neither of us got it"
	default:
		// Nothing was scored, so there is no time to be proud of either.
		return "➖ No result"
	}
	// Only a session somebody actually sat through has a time worth posting.
	// -answer submits without one, and "in 0s" would read as a boast.
	if r.Timing.HumanMS > 0 {
		line += " in " + formatElapsed(msDuration(r.Timing.HumanMS))
	}
	return line
}
