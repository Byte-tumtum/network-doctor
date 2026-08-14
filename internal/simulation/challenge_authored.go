package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	mathrand "math/rand"
	"slices"
	"strings"
)

// Authored challenges: the cases somebody chose, rather than the ones the
// generator happened to produce.
//
// A generated challenge is a draw. To get one that sets a particular fault you
// scan ids until one falls out, which is how the starter packs were built and
// is a poor way to write a lesson: the case you end up teaching is whichever
// one the search reached first, and it moves if generation ever changes.
//
// An authored challenge names its base scenario and its fault directly, and
// resolves through the same machinery as everything else. It is not a second
// kind of challenge:
//
//   - the fault is produced by the ordinary hunt mutation operator for that
//     family, against the ordinary base scenario, so nothing here hand-writes a
//     node name, a port or an address that could drift out of step with the
//     topology it refers to;
//   - truth is established by the same scoped predicate, mutationObserved plus
//     the condition's requires, reading the same independently collected
//     evidence;
//   - scoring is ScoreChallenge, unchanged, and netdoc is graded by the same
//     challengeRecognition table.
//
// What authorship contributes is the choice: which fault, on which topology,
// and the sentence saying why it is worth playing.
//
// Authored ids carry their own version, A1, so they resolve through
// challengeGenerators like any other id and are shareable and replayable the
// same way. The digits are derived from the slug rather than from a position in
// the table, so reordering or inserting entries cannot repoint an id somebody
// has already played.

const (
	// AuthoredIDVersion is the id version authored challenges resolve through.
	AuthoredIDVersion = "A1"
	authoredIDDomain  = "netdoc-sim-authored"
)

// authoredChallenge is one deliberately written case.
//
// The fields are the whole declaration, and there are deliberately no others.
// Difficulty and the reveal explanation are properties of the condition and are
// read from challengeConditions rather than restated here, so an authored case
// cannot disagree with the table about what its own fault is; category and
// tagging metadata are absent because nothing currently reads them.
type authoredChallenge struct {
	// slug is the stable identity. It derives the id and the deterministic seed,
	// so renaming one is minting a different challenge, not editing this one.
	slug string
	name string
	// base is the library control scenario, and mutation is the hunt mutation id
	// the fault is produced by.
	base     string
	mutation string
	// answer is the diagnosis this case is written to teach. It is declared
	// rather than looked up so that it is checkable: TestAuthoredChallengesAreValid
	// fails if it disagrees with what challengeConditions says the mutation
	// establishes, which is what stops an authored case quietly teaching the
	// wrong lesson after a table change.
	answer ChallengeAnswer
	// teaches is the sentence shown when the pack is listed. It says what
	// distinguishing this fault requires, never what the fault is.
	teaches string
}

// authoredChallenges is ordered API: the order here is the order they are
// listed. Every entry is checked by TestAuthoredChallengesAreValid, which
// builds it, proves the mutation applies, proves the declared answer matches
// the condition table, and proves the fault lands on the target the player is
// pointed at.
//
// The set is small on purpose. These are the cases where choosing beats
// drawing: the three ways a connection to a live host ends badly, which are
// only tellable apart from one another; the two certificate faults, which a
// generated draw produces rarely; and the three single-default route faults,
// which are the rarest families in generation and the ones most worth
// practising deliberately.
var authoredChallenges = []authoredChallenge{
	{
		slug: "refused-vs-blocked-refused", name: "The port that answers",
		base: "healthy-routed-network", mutation: "service.connection_refused", answer: AnswerRefused,
		teaches: "A dial that comes back immediately. Work out what a fast failure proves about the path before naming the fault.",
	},
	{
		slug: "refused-vs-blocked-blocked", name: "The port that says nothing",
		base: "healthy-routed-network", mutation: "service.tcp_port_blocked", answer: AnswerPortBlocked,
		teaches: "The same target, failing slowly instead. Its neighbour above is the case to compare it against.",
	},
	{
		slug: "reset-after-accept", name: "The connection that opened first",
		base: "healthy-routed-network", mutation: "service.tcp_reset", answer: AnswerReset,
		teaches: "Something is listening and the handshake completes. What happens next is the whole diagnosis.",
	},
	{
		slug: "certificate-expired", name: "The handshake that checked a clock",
		base: "tls-valid", mutation: "service.tls_expired", answer: AnswerTLSCertificate,
		teaches: "TCP is fine and TLS is not. The certificate is readable on the wire, so read it.",
	},
	{
		slug: "certificate-wrong-name", name: "The certificate for somewhere else",
		base: "tls-valid", mutation: "service.tls_hostname_mismatch", answer: AnswerTLSHostname,
		teaches: "Trusted issuer, valid dates, refused anyway. Its neighbour above is the answer it is most often confused with.",
	},
	{
		slug: "no-default-route", name: "Nothing to point at",
		base: "two-router-healthy", mutation: "routing.no_default_route", answer: AnswerNoDefaultRoute,
		teaches: "The link is up and the address is fine. Read the routing table before reading anything else.",
	},
	{
		slug: "wrong-default-route", name: "The gateway that answers and goes nowhere",
		base: "two-router-healthy", mutation: "routing.wrong_default_route", answer: AnswerWrongDefaultRoute,
		teaches: "There is a default route and its next hop replies. That is not the same as it being the right one.",
	},
	{
		slug: "missing-subnet-route", name: "The hole shaped like one subnet",
		base: "two-router-healthy", mutation: "routing.missing_subnet_route", answer: AnswerMissingRoute,
		teaches: "The internet works and name resolution works. Only one destination does not, which narrows it a long way.",
	},
}

// AuthoredChallenge is one authored case as it is published.
type AuthoredChallenge struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Teaches string `json:"teaches,omitempty"`
}

// AuthoredChallenges lists the authored cases, in the order they are offered.
func AuthoredChallenges() []AuthoredChallenge {
	out := make([]AuthoredChallenge, 0, len(authoredChallenges))
	for _, entry := range authoredChallenges {
		out = append(out, AuthoredChallenge{ID: entry.id(), Slug: entry.slug, Name: entry.name, Teaches: entry.teaches})
	}
	return out
}

// AuthoredChallengeSlugs lists the slugs, for a usage or error message.
func AuthoredChallengeSlugs() []string {
	out := make([]string, 0, len(authoredChallenges))
	for _, entry := range authoredChallenges {
		out = append(out, entry.slug)
	}
	return out
}

// authoredDigits derives an authored id's digits from its slug alone. Stable
// under reordering the table, which is the point: a published id may not depend
// on where its entry happens to sit.
func authoredDigits(slug string) string {
	h := sha256.New()
	h.Write([]byte(authoredIDDomain))
	h.Write([]byte{0})
	h.Write([]byte(slug))
	value := binary.BigEndian.Uint32(h.Sum(nil)[:4]) & 0xFFFFFF
	return fmt.Sprintf("%0*X", challengeIDDigits, value)
}

func (a authoredChallenge) id() string { return AuthoredIDVersion + "-" + authoredDigits(a.slug) }

// authoredSeed is this case's generation stream. Derived from the slug, so an
// authored challenge reproduces identically on any machine and stays the same
// case when the table around it changes.
func authoredSeed(slug string) int64 {
	h := sha256.New()
	h.Write([]byte(authoredIDDomain))
	h.Write([]byte{0})
	h.Write([]byte("seed"))
	h.Write([]byte{0})
	h.Write([]byte(slug))
	// #nosec G115 -- all 64 hash bits are intentionally preserved in the signed seed.
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]))
}

// AuthoredChallengeBySlug resolves a case somebody named. Slugs are what a
// contributor edits and what `netdoc-sim authored` prints, so they are accepted
// alongside the id everywhere a challenge is chosen.
func AuthoredChallengeBySlug(raw string) (AuthoredChallenge, bool) {
	want := strings.ToLower(strings.TrimSpace(raw))
	for _, entry := range authoredChallenges {
		if entry.slug == want {
			return AuthoredChallenge{ID: entry.id(), Slug: entry.slug, Name: entry.name, Teaches: entry.teaches}, true
		}
	}
	return AuthoredChallenge{}, false
}

// buildChallengeAuthored resolves an A1 id. Frozen the same way every other
// version is: the digits name a slug, and a slug's case is produced by the hunt
// operator registry at a pinned generator version, so an authored id keeps
// setting the puzzle it was published with.
func buildChallengeAuthored(id, digits string) (*Challenge, error) {
	for _, entry := range authoredChallenges {
		if authoredDigits(entry.slug) != digits {
			continue
		}
		return entry.build(id)
	}
	return nil, fmt.Errorf("challenge %s: no authored challenge has that id (have: %s)",
		id, strings.Join(AuthoredChallengeSlugs(), ", "))
}

// build materializes one authored case. It is the generated path with the
// search taken out: instead of drawing base and case numbers until a wanted
// mutation falls out, the base and the operator are named, and the only random
// input is the operator's own parameter draw off a seed derived from the slug.
//
// Everything after that is shared. The mutation is applied by
// applyGeneratedMutation, the scenario is validated, and truth will be
// established later by the same scoped predicate a generated challenge uses.
func (a authoredChallenge) build(id string) (*Challenge, error) {
	condition, ok := challengeConditionFor(a.mutation)
	if !ok {
		return nil, fmt.Errorf("authored challenge %s: %q is not a condition Challenge Mode can set", a.slug, a.mutation)
	}
	// The declaration has to agree with the table. An authored case that says it
	// teaches one diagnosis while its mutation establishes another would be
	// graded against the table and read by the player as the declaration.
	if condition.answer != a.answer {
		return nil, fmt.Errorf("authored challenge %s declares %q but %s establishes %q",
			a.slug, a.answer, a.mutation, condition.answer)
	}
	if !slices.Contains(challengeBasesV3, a.base) {
		return nil, fmt.Errorf("authored challenge %s: %q is not a challenge base scenario", a.slug, a.base)
	}
	base, err := LibraryScenario(a.base)
	if err != nil {
		return nil, fmt.Errorf("authored challenge %s: %w", a.slug, err)
	}
	canonicalScenarioInput(base)
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("authored challenge %s: base %s: %w", a.slug, a.base, err)
	}
	operator, ok := huntOperatorByID(HuntGeneratorVersion, a.mutation)
	if !ok {
		return nil, fmt.Errorf("authored challenge %s: hunt generator %s has no %q operator",
			a.slug, HuntGeneratorVersion, a.mutation)
	}
	if !operator.applicable(base) {
		return nil, fmt.Errorf("authored challenge %s: %q does not apply to base %s",
			a.slug, a.mutation, a.base)
	}
	seed := authoredSeed(a.slug)
	// #nosec G404 -- authored challenge generation must reproduce from its stable slug.
	rng := mathrand.New(mathrand.NewSource(seed))
	mutation, err := operator.generate(rng, base)
	if err != nil {
		return nil, fmt.Errorf("authored challenge %s: generate %s: %w", a.slug, a.mutation, err)
	}
	mutation.ID = operator.id
	if mutation.Description == "" {
		mutation.Description = operator.description
	}
	// The same fairness test a generated case has to pass: the fault has to sit
	// on the target the briefing names, or the player is being asked about a
	// clue they were never shown.
	if condition.briefed != nil && !condition.briefed(base, mutation) {
		return nil, fmt.Errorf("authored challenge %s: %s lands off the briefed target on base %s",
			a.slug, a.mutation, a.base)
	}
	scenario := cloneScenario(base)
	scenario.Campaign = nil
	if err := applyGeneratedMutation(scenario, mutation); err != nil {
		return nil, fmt.Errorf("authored challenge %s: apply %s: %w", a.slug, a.mutation, err)
	}
	scenario.Name = "authored-" + a.slug
	scenario.Description = "Authored netdoc-sim challenge " + a.slug + " from " + a.base + "."
	canonicalScenarioInput(scenario)
	if err := scenario.Validate(); err != nil {
		return nil, fmt.Errorf("authored challenge %s: generated scenario validation: %w", a.slug, err)
	}
	manifest := GeneratedCaseManifest{GeneratorVersion: HuntGeneratorVersion, BaseScenario: a.base,
		HuntSeed: seed, Case: authoredCaseNumber, CaseSeed: seed, Mutations: []GeneratedMutation{mutation}}
	manifest.CaseFingerprint = huntCaseFingerprint(manifest)
	return newChallenge(id, a.base, seed, authoredCaseNumber, condition.difficulty, manifest, scenario, condition)
}

// authoredCaseNumber marks a manifest that did not come from a case scan. It is
// the same sentinel a healthy challenge uses, because it means the same thing:
// the id is the reproduction contract, and there is no case number behind it.
const authoredCaseNumber = -1

// huntOperatorByID finds one mutation operator at a named generator version.
func huntOperatorByID(version, id string) (mutationOperator, bool) {
	for _, op := range huntOperators(version) {
		if op.id == id {
			return op, true
		}
	}
	return mutationOperator{}, false
}

// AuthoredChallengeByID builds the authored case a slug names. It resolves the
// slug to that case's ordinary id and goes through BuildChallenge, so choosing
// by slug and replaying by id cannot produce different networks.
func AuthoredChallengeByID(slug string) (*Challenge, error) {
	found, ok := AuthoredChallengeBySlug(slug)
	if !ok {
		return nil, fmt.Errorf("unknown authored challenge %q (have: %s)",
			slug, strings.Join(AuthoredChallengeSlugs(), ", "))
	}
	return BuildChallenge(found.ID)
}
