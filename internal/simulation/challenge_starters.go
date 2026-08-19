package simulation

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
)

// Starter packs: a way in that is not "here is a random hard one".
//
// A pack is a curated list of ordinary challenge ids and nothing else. It adds
// no second simulator, no second generator and no second scoring path: every
// entry resolves through BuildChallenge exactly as a shared id does, plays on the
// same namespaces, is graded by the same oracle, and is replayable by its own id
// forever. What a pack contributes is the choice of which ids, and the sentence
// saying what practising them teaches.
//
// The ids are written down rather than searched for at run time. A pack whose
// contents were rediscovered by scanning would quietly re-point itself whenever
// generation changed, and the lesson a beginner was promised would move under
// them. Written down, TestStarterPacksStayPlayable fails loudly instead.

// starterEntry is one curated challenge.
type starterEntry struct {
	id string
	// condition is the mutation this entry was curated for. It is never printed,
	// never returned by the public API and never reaches the player: it is here so
	// a test can prove the entry still sets the fault its pack promises to teach,
	// which is the only thing that makes curation a claim rather than a wish.
	condition string
}

// starterPack is a pack as it is written down here.
type starterPack struct {
	id          string
	name        string
	description string
	entries     []starterEntry
}

// StarterPack is a pack as it is published: a stable machine id, a name and a
// sentence for a person, and the challenge ids in the order they are meant to be
// worked through. The ids are the whole content, and anybody can play one directly
// without going through a pack at all.
type StarterPack struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Challenges  []string `json:"challenges"`
}

// starterPacks is ordered API: the order here is the order a beginner is
// offered, easiest first, and within a pack the entries climb.
//
// Every pack holds at least two different answers, and that is a rule rather
// than a coincidence. A pack names the layer it is about, which is a hint the
// player asked for, but a pack with only one possible answer would not be a
// hint, it would be the answer key.
//
// `dns` is the pack that has to earn that rule rather than get it for free. The
// answer vocabulary has exactly one DNS entry, deliberately (see
// challengeRecognition), so its two faults score the same word and a pack of
// faults alone would be solved by its own name. Its third entry is a network
// with nothing wrong with it, which is the shape `tls` already uses: the
// question the pack asks is not "which DNS fault" but "is the resolver at fault
// at all", and it carries both faults because the resolver fails two visibly
// different ways, refusing and going quiet.
//
// Path MTU, QUIC and proxy have no pack because they are not challenge
// conditions and cannot be: the first is not in the hunt generator any published
// id resolves through, and the other two are excluded from the challenge
// contract by name. See the exclusion list above challengeConditions. They are
// practised through the scenario library instead, with `netdoc-sim run`.
//
// TestStarterPacksStayPlayable checks both halves of the rule: every entry still
// sets the condition it was curated for, and no pack has collapsed to a single
// answer.
var starterPacks = []starterPack{
	{
		id:          "fundamentals",
		name:        "Fundamentals",
		description: "Start here. Four unrelated layers, one of which is not broken at all.",
		entries: []starterEntry{
			{"V3-01B112", "service.connection_refused"},
			{"V3-02E668", "dns.servfail"},
			{"V3-019223", "routing.no_default_route"},
			{"V3-022CCE", ""},
		},
	},
	{
		id:          "dns",
		name:        "Name resolution",
		description: "One network three times over: a resolver that refuses, a resolver that says nothing, and a resolver that is fine.",
		entries: []starterEntry{
			{"V3-00B99A", "dns.servfail"},
			{"V3-032446", "dns.drop"},
			{"V3-024BBD", ""},
		},
	},
	{
		id:          "service",
		name:        "Reaching the service",
		description: "Three ways a connection to a live host ends badly, and how to tell them apart.",
		entries: []starterEntry{
			{"V3-00D889", "service.connection_refused"},
			{"V3-005CCD", "service.tcp_reset"},
			{"V3-001EEF", "service.tcp_port_blocked"},
		},
	},
	{
		id:          "tls",
		name:        "Certificates",
		description: "What the handshake objected to, including once when it did not object at all.",
		entries: []starterEntry{
			{"V3-01EEF0", ""},
			{"V3-058EF2", "service.tls_expired"},
			{"V3-05EBBF", "service.tls_hostname_mismatch"},
		},
	},
	{
		id:          "paths",
		name:        "Address families and impairment",
		description: "One family down while the other works, and a path that works but leaks packets.",
		entries: []starterEntry{
			{"V3-000000", "family.ipv4_drop"},
			{"V3-034335", "netem.loss"},
			{"V3-07F99E", "family.ipv6_drop"},
		},
	},
	{
		id:          "routing",
		name:        "Routes and gateways",
		description: "Four route-shaped holes that all look like \"the internet is down\" at first.",
		entries: []starterEntry{
			{"V3-009AAB", "routing.no_default_route"},
			{"V3-011667", "routing.wrong_default_route"},
			{"V3-020DDF", "routing.missing_subnet_route"},
			{"V3-04599C", "routing.preferred_path_failure"},
		},
	},
}

// StarterPacks lists the curated packs, in the order they are offered.
func StarterPacks() []StarterPack {
	out := make([]StarterPack, 0, len(starterPacks))
	for _, pack := range starterPacks {
		out = append(out, pack.published())
	}
	return out
}

func (p starterPack) published() StarterPack {
	ids := make([]string, 0, len(p.entries))
	for _, entry := range p.entries {
		ids = append(ids, entry.id)
	}
	return StarterPack{ID: p.id, Name: p.name, Description: p.description, Challenges: ids}
}

// StarterPackNames lists the pack ids, for a usage or error message.
func StarterPackNames() []string {
	out := make([]string, 0, len(starterPacks))
	for _, pack := range starterPacks {
		out = append(out, pack.id)
	}
	return out
}

func findStarterPack(raw string) (starterPack, bool) {
	want := strings.ToLower(strings.TrimSpace(raw))
	for _, pack := range starterPacks {
		if pack.id == want {
			return pack, true
		}
	}
	return starterPack{}, false
}

// StarterPackByID resolves a pack a person named.
func StarterPackByID(raw string) (StarterPack, bool) {
	pack, ok := findStarterPack(raw)
	if !ok {
		return StarterPack{}, false
	}
	return pack.published(), true
}

// StarterChallenge draws one challenge from a pack. The draw is random because
// the command has no memory of which ones you have played, and inventing a
// progress file for a game whose whole appeal is that it leaves nothing behind
// would be the wrong trade. Anyone who wants to work through a pack in order has
// the ids: `netdoc-sim starters <pack>` prints them, and each plays with -id.
func StarterChallenge(pack string) (*Challenge, error) {
	found, ok := findStarterPack(pack)
	if !ok {
		return nil, fmt.Errorf("unknown starter pack %q (have: %s)",
			pack, strings.Join(StarterPackNames(), ", "))
	}
	index, err := randomIndex(len(found.entries))
	if err != nil {
		return nil, err
	}
	return BuildChallenge(found.entries[index].id)
}

func randomIndex(n int) (int, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	// #nosec G115 -- n is the non-empty starter slice length; modulo bounds the result to int.
	return int(binary.BigEndian.Uint64(raw[:]) % uint64(n)), nil
}
