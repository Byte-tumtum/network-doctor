package simulation

import (
	"testing"
	"time"
)

// The daily's one promise: same date, same challenge, on every machine. No clock
// is read here — the date is the input — so this is a property of the function
// rather than of the day the test runs on.
func TestDailyChallengeIsDeterministicPerDate(t *testing.T) {
	for _, date := range []string{"2026-01-01", "2026-02-28", "2026-08-12", "2027-12-31"} {
		first, err := DailyChallenge(date)
		if err != nil {
			t.Fatalf("daily %s: %v", date, err)
		}
		second, err := DailyChallenge(date)
		if err != nil {
			t.Fatalf("daily %s again: %v", date, err)
		}
		if first.ID != second.ID {
			t.Fatalf("daily %s resolved to %s then %s", date, first.ID, second.ID)
		}
		if first.Daily != date {
			t.Fatalf("daily %s says it is the daily for %q", date, first.Daily)
		}
		// And it is not the same challenge every day, which would make the feature
		// pointless rather than merely wrong.
		if other, err := DailyChallenge("2026-06-15"); err == nil && date != "2026-06-15" && other.ID == first.ID {
			t.Fatalf("daily %s and 2026-06-15 are the same challenge %s", date, first.ID)
		}
	}
}

// A daily is played on the day and read about later, so the id it produced has
// to reproduce it — with no -daily, no date, and no memory of when it was.
func TestDailyChallengeReplaysByItsID(t *testing.T) {
	daily, err := DailyChallenge("2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := BuildChallenge(daily.ID)
	if err != nil {
		t.Fatalf("replay %s: %v", daily.ID, err)
	}
	if replayed.Base != daily.Base || replayed.Case != daily.Case ||
		replayed.Manifest.CaseFingerprint != daily.Manifest.CaseFingerprint ||
		replayed.Target != daily.Target || replayed.condition.answer != daily.condition.answer {
		t.Fatalf("replaying %s produced a different challenge:\n%+v\n%+v", daily.ID, replayed, daily)
	}
	// The date is a label on the session, not part of the puzzle: a replay is the
	// same network without claiming to be that day's daily.
	if replayed.Daily != "" {
		t.Fatalf("replaying %s claims to be the daily for %q", daily.ID, replayed.Daily)
	}
}

// The date is the UTC calendar date and nothing else. Two players whose local
// clocks disagree about what day it is have to get the same challenge, which is
// the only reason this is UTC rather than local.
func TestDailyDateIsUTCAcrossTheDayBoundary(t *testing.T) {
	// One instant, named from four zones. Every one of them is the same UTC day.
	instant := time.Date(2026, 8, 12, 23, 30, 0, 0, time.UTC)
	for _, zone := range []string{"UTC", "America/Los_Angeles", "Pacific/Auckland", "Asia/Kolkata"} {
		location, err := time.LoadLocation(zone)
		if err != nil {
			t.Skipf("no zone database for %s", zone)
		}
		if got := DailyDate(instant.In(location)); got != "2026-08-12" {
			t.Fatalf("the same instant read as %q in %s", got, zone)
		}
	}
	// And the boundary is midnight UTC, to the nanosecond either side of it.
	for _, tt := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), "2026-08-12"},
		{time.Date(2026, 8, 12, 23, 59, 59, 999999999, time.UTC), "2026-08-12"},
		{time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), "2026-08-13"},
	} {
		if got := DailyDate(tt.at); got != tt.want {
			t.Fatalf("DailyDate(%s) = %q, want %q", tt.at, got, tt.want)
		}
	}
	// Which means the two sides of the boundary are two different challenges.
	before, err := DailyChallenge("2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	after, err := DailyChallenge("2026-08-13")
	if err != nil {
		t.Fatal(err)
	}
	if before.ID == after.ID {
		t.Fatalf("2026-08-12 and 2026-08-13 are the same daily %s", before.ID)
	}
}

func TestParseDailyDateIsStrict(t *testing.T) {
	for _, raw := range []string{"2026-08-12", " 2026-08-12 "} {
		got, err := ParseDailyDate(raw)
		if err != nil || got != "2026-08-12" {
			t.Fatalf("ParseDailyDate(%q) = %q, %v", raw, got, err)
		}
	}
	// A near miss has to be a rejection: the date is half of the reproduction
	// contract, so guessing at one would hand somebody a different day's puzzle.
	for _, raw := range []string{"", "2026-8-12", "12-08-2026", "2026/08/12", "2026-13-01",
		"2026-02-30", "today", "2026-08-12T00:00:00Z", "2026-08-12 extra"} {
		if got, err := ParseDailyDate(raw); err == nil {
			t.Fatalf("ParseDailyDate(%q) was accepted as %q", raw, got)
		}
	}
}

// The epoch table is what stops a future generator redefining last month's
// daily. A row may be appended; the rows already there decide dates people have
// already played and can never be edited.
func TestDailyEpochsStayFrozen(t *testing.T) {
	if len(dailyEpochs) == 0 {
		t.Fatal("there has to be at least one epoch, or no date resolves")
	}
	if got, want := dailyEpochs[0], (dailyEpoch{from: "2026-01-01", version: "V3"}); got != want {
		t.Fatalf("the first epoch is now %+v; it was published as %+v. Append a row instead.", got, want)
	}
	for i := 1; i < len(dailyEpochs); i++ {
		if dailyEpochs[i].from <= dailyEpochs[i-1].from {
			t.Fatalf("epoch %d starts at %s, which is not after %s", i, dailyEpochs[i].from, dailyEpochs[i-1].from)
		}
	}
	for _, epoch := range dailyEpochs {
		if _, ok := challengeGenerators[epoch.version]; !ok {
			t.Fatalf("epoch %s mints %s ids, which this build cannot resolve", epoch.from, epoch.version)
		}
		if _, err := ParseDailyDate(epoch.from); err != nil {
			t.Fatalf("epoch start %q is not a date: %v", epoch.from, err)
		}
	}
	// A date before the first epoch resolves through it rather than through
	// nothing. There is no daily older than the feature, so this is a total
	// function rather than a special case.
	if got := dailyEpochFor("2020-01-01"); got != dailyEpochs[0].version {
		t.Fatalf("a date before the first epoch resolves at %s, want %s", got, dailyEpochs[0].version)
	}
	// And a daily's id carries the version its date's epoch names, so the id
	// itself says which rules resolve it.
	daily, err := DailyChallenge("2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	if got := challengeIDVersionOf(daily.ID); got != dailyEpochFor("2026-08-12") {
		t.Fatalf("the 2026-08-12 daily is a %s id under epoch %s", got, dailyEpochFor("2026-08-12"))
	}
}

// A year of dailies, all playable. A date whose first candidate id has no
// challenge-capable case walks on deterministically, and this is what proves the
// walk terminates on real days rather than only on the ones a spot check hit.
func TestEveryDailyOfAYearIsPlayable(t *testing.T) {
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	answers := map[ChallengeAnswer]int{}
	for i := 0; i < 365; i++ {
		date := DailyDate(day.AddDate(0, 0, i))
		challenge, err := DailyChallenge(date)
		if err != nil {
			t.Fatalf("daily %s: %v", date, err)
		}
		if challenge.Scenario == nil {
			t.Fatalf("daily %s has no scenario", date)
		}
		answers[challenge.condition.answer]++
	}
	if len(answers) < 8 {
		t.Fatalf("a year of dailies covered only %d answers: %v", len(answers), answers)
	}
}
