package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// The daily challenge.
//
// One property matters more than every other one here: two people who ask for
// the same day must get the same puzzle. That rules out anything local — no
// timezone, no locale, no clock beyond the calendar date, no filesystem, no
// process randomness, and no server to ask. What is left is a pure function from
// a UTC date to a challenge id, which is exactly what this file is.
//
// The id it produces is the artifact. A daily is playable after its day is over
// by its id like any other challenge, and nothing about replay depends on
// "today" still being that day.

// dailyIDDomain separates this derivation from every other use of the same
// hash, so a date and a challenge id can never collide into each other.
const dailyIDDomain = "netdoc-sim-daily"

// dailyDateLayout is the one spelling of a date this accepts and prints. ISO
// 8601, so it sorts, and unambiguous between the conventions that disagree about
// which number is the month.
const dailyDateLayout = "2006-01-02"

// dailySearchLimit bounds the deterministic walk for a date whose first
// candidate id does not resolve. Ids resolve overwhelmingly often, so this is
// slack rather than a budget.
const dailySearchLimit = 64

// dailyEpoch is the id version dailies are minted at from one date onwards.
type dailyEpoch struct {
	from    string
	version string
}

// dailyEpochs freezes what a date means, the same way challengeGenerators
// freezes what an id means. A daily is derived from the date and an id version,
// so a build that started minting a newer version would otherwise silently
// redefine every historical daily — somebody's posted result for last month
// would stop naming the challenge they played.
//
// Adding a version therefore appends a row here with the first UTC date it
// applies from, and leaves every earlier row alone. Dates before the first row
// resolve through it too; there is no daily older than the feature.
//
// Ordered oldest first, and the applicable epoch is the last row whose date has
// arrived. TestDailyEpochsStayFrozen pins the rows.
var dailyEpochs = []dailyEpoch{
	{from: "2026-01-01", version: "V3"},
}

// DailyDate renders an instant as the UTC calendar date a daily is keyed by.
// The conversion to UTC is the whole point: a player at 23:00 in Auckland and a
// player at 23:00 in Los Angeles are on different local dates, and a daily that
// followed the local one would not be the same challenge.
func DailyDate(at time.Time) string { return at.UTC().Format(dailyDateLayout) }

// ParseDailyDate accepts a date a person typed and returns the canonical
// rendering of it. Strict on purpose: a date is half of the reproduction
// contract for a daily, so a near miss has to be a rejection rather than a
// different day.
func ParseDailyDate(raw string) (string, error) {
	date := strings.TrimSpace(raw)
	parsed, err := time.ParseInLocation(dailyDateLayout, date, time.UTC)
	if err != nil {
		return "", fmt.Errorf("a daily date is a UTC calendar date like 2026-08-12, got %q", date)
	}
	return parsed.Format(dailyDateLayout), nil
}

// dailyEpochFor is the id version this date's daily is minted at.
func dailyEpochFor(date string) string {
	version := dailyEpochs[0].version
	for _, epoch := range dailyEpochs {
		if date >= epoch.from {
			version = epoch.version
		}
	}
	return version
}

// DailyChallenge resolves one UTC date to the challenge everybody asking for
// that date gets. It is pure: no clock is read, no state is consulted, and the
// returned challenge carries an ordinary id that reproduces it forever.
func DailyChallenge(date string) (*Challenge, error) {
	date, err := ParseDailyDate(date)
	if err != nil {
		return nil, err
	}
	version := dailyEpochFor(date)
	for attempt := 0; attempt < dailySearchLimit; attempt++ {
		id := dailyChallengeID(version, date, attempt)
		challenge, err := BuildChallenge(id)
		if err != nil {
			// A date whose candidate id has no challenge-capable case walks on to
			// the next candidate. The walk is part of the derivation, so it lands on
			// the same id everywhere.
			continue
		}
		challenge.Daily = date
		return challenge, nil
	}
	return nil, fmt.Errorf("no daily challenge could be derived for %s", date)
}

// dailyChallengeID is the derivation itself: a hash of the domain, the id
// version and the date, narrowed to the width of an id. attempt is mixed in so
// the walk past an unresolvable candidate is as deterministic as the first pick.
func dailyChallengeID(version, date string, attempt int) string {
	h := sha256.New()
	h.Write([]byte(dailyIDDomain))
	h.Write([]byte{0})
	h.Write([]byte(version))
	h.Write([]byte{0})
	h.Write([]byte(date))
	h.Write([]byte{0})
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(attempt))
	h.Write(raw[:])
	value := binary.BigEndian.Uint32(h.Sum(nil)[:4]) & 0xFFFFFF
	return fmt.Sprintf("%s-%0*X", version, challengeIDDigits, value)
}
