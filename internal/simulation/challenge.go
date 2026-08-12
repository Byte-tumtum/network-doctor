package simulation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	mathrand "math/rand"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// Challenge Mode is the human-facing counterpart to the hunt. The simulator
// breaks a network, a person investigates it with ordinary tools and commits to
// a structured diagnosis, and only then does the real netdoc get its turn at
// the same live network. Both answers are graded against the same independently
// observed simulator truth, so neither contestant can move the answer key.
//
// This file is the domain model: identity, generation, truth and scoring. It
// knows nothing about terminals — challenge_run.go runs one against a backend,
// and cmd/netdoc-sim/challenge.go is the interaction.

const (
	// ChallengeIDVersion is the version new ids are minted with. It is part of
	// the id itself, not a note about it: an id names the generation rules that
	// resolve it, so a future change to selection adds a version instead of
	// quietly repointing every id that has already been shared.
	ChallengeIDVersion = "V2"
	challengeIDDomain  = "netdoc-sim-challenge"
	// challengeIDDigits is the width of a shareable challenge id, in hex digits.
	challengeIDDigits = 6
	// challengeSearchLimit bounds the deterministic scan an id makes for a
	// case whose single mutation is challenge-capable.
	challengeSearchLimit = 200
	// challengeIDSearchLimit bounds the scan for an id of a requested
	// difficulty. Difficulty is a property of the case behind an id, so the only
	// way to honour a request is to look at ids until one carries it.
	challengeIDSearchLimit = 500
	// challengeHealthyOdds gives one challenge in this many no injected fault at
	// all. "Nothing is wrong with this network" has to be a real possibility, or
	// the game teaches people to always find something.
	challengeHealthyOdds = 6
)

// Difficulty levels. A level is reviewed metadata on a challenge family, not a
// score computed from the topology: it says how directly the symptoms expose
// the fault, whether the families differ, and how many plausible competing
// diagnoses the evidence leaves open.
const (
	DifficultyEasy   = "easy"
	DifficultyMedium = "medium"
	DifficultyHard   = "hard"
)

// ChallengeDifficulties is ordered API.
var ChallengeDifficulties = []string{DifficultyEasy, DifficultyMedium, DifficultyHard}

// ChallengeAnswer is one entry in the structured diagnosis vocabulary both
// contestants are graded against. Scoring never reads free text.
type ChallengeAnswer string

const (
	AnswerHealthy        ChallengeAnswer = "healthy"
	AnswerDNSFailure     ChallengeAnswer = "dns_failure"
	AnswerNoDefaultRoute ChallengeAnswer = "no_default_route"
	AnswerPreferredRoute ChallengeAnswer = "preferred_route_failure"
	AnswerIPv4Failure    ChallengeAnswer = "ipv4_failure"
	AnswerIPv6Failure    ChallengeAnswer = "ipv6_failure"
	AnswerPortBlocked    ChallengeAnswer = "tcp_port_blocked"
	AnswerRefused        ChallengeAnswer = "connection_refused"
	AnswerReset          ChallengeAnswer = "connection_reset"
	AnswerTLSCertificate ChallengeAnswer = "tls_certificate"
	AnswerHTTPService    ChallengeAnswer = "http_service"
	AnswerProxy          ChallengeAnswer = "proxy_failure"
	AnswerQUICBlocked    ChallengeAnswer = "quic_udp_blocked"
	AnswerPacketLoss     ChallengeAnswer = "packet_loss"
)

// ChallengeAnswerInfo is one menu entry.
type ChallengeAnswerInfo struct {
	ID    ChallengeAnswer `json:"id"`
	Label string          `json:"label"`
	Help  string          `json:"help,omitempty"`
}

// ChallengeAnswerMenu is ordered API, and is deliberately wider than the set of
// faults a challenge can currently inject: a menu listing only the possible
// faults would be most of the answer.
var ChallengeAnswerMenu = []ChallengeAnswerInfo{
	{AnswerHealthy, "Nothing is wrong with this network", "every layer works; the reported problem is elsewhere"},
	{AnswerDNSFailure, "DNS resolution", "names do not resolve, or the resolver refuses"},
	{AnswerNoDefaultRoute, "No default route", "the client has no way out of its own subnet"},
	{AnswerPreferredRoute, "Wrong or failed preferred route", "a route is selected whose path cannot reach the target, while another one can"},
	{AnswerIPv4Failure, "IPv4 connectivity", "the IPv4 path is down while IPv6 works"},
	{AnswerIPv6Failure, "IPv6 connectivity", "the IPv6 path is down while IPv4 works"},
	{AnswerPortBlocked, "TCP port blocked", "connections to the target port are silently discarded"},
	{AnswerRefused, "Connection refused", "the target answers the connection with a refusal"},
	{AnswerReset, "Connection reset by the service", "the target accepts the connection and then tears it down"},
	{AnswerTLSCertificate, "TLS certificate", "the handshake fails on the certificate itself"},
	{AnswerHTTPService, "HTTP service error", "the server answers, with an error status"},
	{AnswerProxy, "Proxy", "the configured proxy is the thing that fails"},
	{AnswerQUICBlocked, "QUIC / UDP 443", "UDP/443 is filtered while TCP/443 is not"},
	{AnswerPacketLoss, "Packet loss or latency", "the path works but drops or delays traffic"},
}

// ChallengeAnswerByID resolves a submitted answer name.
func ChallengeAnswerByID(raw string) (ChallengeAnswerInfo, bool) {
	want := ChallengeAnswer(strings.ToLower(strings.TrimSpace(raw)))
	for _, item := range ChallengeAnswerMenu {
		if item.ID == want {
			return item, true
		}
	}
	return ChallengeAnswerInfo{}, false
}

// ChallengeAnswerNames lists every accepted answer id, for a usage message.
func ChallengeAnswerNames() []string {
	out := make([]string, 0, len(ChallengeAnswerMenu))
	for _, item := range ChallengeAnswerMenu {
		out = append(out, string(item.ID))
	}
	return out
}

func challengeAnswerLabel(answer ChallengeAnswer) string {
	if info, ok := ChallengeAnswerByID(string(answer)); ok {
		return info.Label
	}
	return string(answer)
}

// challengeCondition is one condition the simulator can create and then prove,
// from its own observations, that it created. It is eligibility metadata and
// nothing else: every field is a fact about the simulated network, about what
// evidence settles it, or about fairness between the two contestants. Network
// Doctor does not appear here.
//
// Whether netdoc has any way to state this condition lives in a separate table,
// challengeRecognition, which this side never reads. That separation is the
// point. When eligibility and recognition were one struct, a condition could
// not be set as a challenge until somebody had first decided what netdoc ought
// to say about it — which made the game a quiz written from the answer sheet.
type challengeCondition struct {
	// mutation is the hunt mutation id, empty for the healthy challenge.
	mutation    string
	answer      ChallengeAnswer
	difficulty  string
	explanation string
	// requires is the independent observation this condition needs on top of
	// the shared per-mutation evidence check in mutationObserved. nil means that
	// check is the whole requirement. It may only ever narrow: a challenge may
	// demand more evidence than the hunt does, never less, so nothing here can
	// manufacture a fault the hunt would not also have seen.
	requires func(Evidence, GeneratedMutation) bool
	// briefed, when set, is the extra condition that makes a generated case a
	// fair puzzle rather than merely a possible one: it is handed the unmutated
	// base and the mutation, and reports whether the fault sits where the player
	// was pointed. A base with more than one client test can place a service
	// fault on a target the briefing never names — a clue nobody was shown, and a
	// question the graded netdoc run was never asked.
	briefed func(*Scenario, GeneratedMutation) bool
}

// challengeConditions is the challenge contract: the hunt mutations Challenge
// Mode is willing to set as puzzles. A mutation qualifies on four
// simulator-side tests, none of which mentions a diagnosis:
//
//  1. independently observable — the executed run leaves evidence, read back
//     off the wire or off the kernel, that the fault reached live traffic.
//     mutationObserved is that check, narrowed further by requires.
//  2. deterministic and replayable — the same id sets the same puzzle, and the
//     condition either holds for the whole run or does not hold at all.
//  3. same network for both contestants — a person in the shell and the netdoc
//     process must be able to see the same thing.
//  4. inside the diagnostic scope — the condition is a fault of the network
//     itself, the thing Network Doctor exists to name, rather than a fault of
//     an application that the network delivered correctly.
//
// Nothing in that list is "netdoc already recognizes it". A condition netdoc
// has no vocabulary for is deliberately still eligible, and is a loss for
// netdoc rather than a challenge that could never be set — see
// challengeRecognition and docs/simulation.md#the-challenge-contract.
//
// Ids select through this list, so adding or removing an entry changes what ids
// already in circulation resolve to. That is a new id version, not an edit —
// see challengeGenerators and challengeV1Mutations.
//
// Deliberately absent, and which test each one fails:
//   - every timed family (timeline.*, link.transient_down) fails (2) and (3): a
//     scheduled fault is measured from the instant the first netdoc process
//     starts, so it would be over — or not yet started — while the person was
//     investigating.
//   - netem.latency and netem.jitter fail (1): delay leaves no counter of its
//     own, so the only thing that could establish them is a round-trip sample
//     read back after the run, which is not evidence that the shaper delayed
//     anybody's traffic. netem.loss is in, because the qdisc's own drop counter
//     is exactly that evidence.
//   - http.status_503 and encrypted_dns.doh_invalid fail (4): the network
//     carried the request and carried the answer back. What the answer said is
//     the application's business, and the `http-error` control pins down that
//     netdoc reports the network as working there on purpose.
//   - proxy.connect_refused fails (3): the netdoc process is handed the proxy
//     through its environment and a shell is not.
//   - quic.udp_443_block fails (3): a filtered UDP port and a silent one look
//     identical to the tools in the shell, so the person is left with nothing
//     to reason from while netdoc's QUIC probe measures it directly.
var challengeConditions = []challengeCondition{
	{
		mutation: "", answer: AnswerHealthy, difficulty: "",
		explanation: "Nothing was injected. Every layer the simulator measured — the link, the gateway, name resolution and the path to the controlled endpoints — was working.",
	},
	{
		mutation: "dns.servfail", answer: AnswerDNSFailure, difficulty: DifficultyEasy,
		explanation: "The client's resolver answered queries with SERVFAIL. Nothing below DNS was touched: the link, the gateway and the path to the target's address kept working.",
	},
	{
		mutation: "dns.drop", answer: AnswerDNSFailure, difficulty: DifficultyEasy,
		explanation: "The client's resolver silently discarded queries, so name resolution timed out rather than failing fast. Nothing below DNS was touched.",
	},
	{
		mutation: "service.tcp_reset", answer: AnswerReset, difficulty: DifficultyEasy,
		explanation: "The target's service accepted the TCP connection and then reset it. The path to the target is intact — the connection completes before it is torn down.",
		briefed:     resetTargetIsBriefed,
	},
	{
		mutation: "service.tls_expired", answer: AnswerTLSCertificate, difficulty: DifficultyMedium,
		explanation: "The target served an expired certificate and the client refused it. TCP to the port succeeds, so everything below TLS is healthy.",
	},
	{
		mutation: "family.ipv4_drop", answer: AnswerIPv4Failure, difficulty: DifficultyMedium,
		explanation: "IPv4 forwarding was dropped at the gateway while IPv6 kept working. The client still has an IPv4 address and an IPv4 route — what it does not have is an IPv4 path.",
	},
	{
		mutation: "family.ipv6_drop", answer: AnswerIPv6Failure, difficulty: DifficultyHard,
		explanation: "IPv6 forwarding was dropped at the gateway while IPv4 kept working. Most traffic still succeeds by falling back to IPv4, which is what makes this one quiet.",
	},
	{
		mutation: "routing.preferred_path_failure", answer: AnswerPreferredRoute, difficulty: DifficultyHard,
		explanation: "The client's lower-metric default route is still selected and its gateway still answers, but that router's own upstream link is down. The higher-metric alternate default remains healthy, which a controlled target on that path proves.",
	},
	{
		mutation: "netem.loss", answer: AnswerPacketLoss, difficulty: DifficultyMedium,
		explanation: "A shaper on the path to the internet discarded a percentage of the packets crossing it. Nothing is misconfigured — addresses, routes, name resolution and every service are intact — and connections that survive the loss still work, which is what makes it read as flaky rather than broken.",
		// The qdisc's own drop counter, not the qdisc being installed with the
		// requested parameters. A shaper that matched no traffic impaired
		// nobody, and this is the only reading that separates the two.
		requires: func(evidence Evidence, m GeneratedMutation) bool {
			return shapedPacketsDroppedAt(evidence, m.Node, m.Segment)
		},
	},
}

// challengeRecognition is the contestant's half: what Network Doctor's own
// report has to say for it to have named a condition. It reads one diagnosis
// and nothing else, exactly as the hunt oracle's half does — netdoc is never
// told which condition it is looking at, and never graded on prose.
//
// The map is deliberately partial, and nothing that decides what may be
// generated is allowed to consult it. A condition with no entry is one netdoc's
// vocabulary has no way to express, which scores ChallengeUnrecognized: a loss,
// stated as the distinct thing it is rather than folded into "wrong answer".
//
// Keyed by answer rather than by mutation because recognition is about the
// condition, not about how the simulator produced it: dns.servfail and dns.drop
// are one question to netdoc.
var challengeRecognition = map[ChallengeAnswer]func(*Diagnosis) bool{
	AnswerHealthy:        func(d *Diagnosis) bool { return d.Verdict == diagnostic.VerdictOK },
	AnswerDNSFailure:     flaggedRow(diagnostic.ProbeDNS),
	AnswerReset:          causeRecognizer(diagnostic.ConnectionCauseReset),
	AnswerTLSCertificate: conditionRecognizer(ConditionTLSCertificateExpired),
	AnswerIPv4Failure:    conditionRecognizer(ConditionIPv4InternetUnreachable),
	AnswerIPv6Failure:    conditionRecognizer(ConditionIPv6InternetUnreachable),
	AnswerPreferredRoute: causeRecognizer(diagnostic.RouteCausePreferredPathFailed),
	// AnswerPacketLoss has no entry on purpose. netdoc's cause vocabulary
	// carries no impairment verdict at all, so there is no report it could
	// produce that would name observed packet loss — the `packet-loss` control
	// scenario expects a clean `ok`. A challenge on it is netdoc's to lose.
}

// flaggedRow recognizes a diagnosis by the row it raised its hand on. Used only
// where the row is the whole message — a failing DNS row is netdoc telling the
// user the name did not resolve, and there is no narrower cause to match. The
// probe id comes from the diagnostic constant so a rename stays in step.
func flaggedRow(ids ...diagnostic.ProbeID) func(*Diagnosis) bool {
	return func(d *Diagnosis) bool {
		for _, check := range d.Checks {
			if flagged(check.Status) && slices.Contains(ids, diagnostic.ProbeID(check.ID)) {
				return true
			}
		}
		return false
	}
}

// causeRecognizer recognizes a diagnosis by netdoc's own cause vocabulary,
// which is what the report says about the network rather than which row
// noticed. A cause on a passing row is context, not recognition.
func causeRecognizer(causes ...string) func(*Diagnosis) bool {
	return func(d *Diagnosis) bool { return flaggedCause(d, nil, causes...) }
}

// conditionRecognizer reuses the hunt oracle's recognition half verbatim, so a
// condition the hunt already knows how to grade cannot be graded two ways.
func conditionRecognizer(condition NetworkCondition) func(*Diagnosis) bool {
	for _, rule := range conditionOracle {
		if rule.condition == condition {
			return rule.recognized
		}
	}
	panic("challenge: no oracle rule for condition " + string(condition))
}

// resetTargetIsBriefed reports whether the HTTP target the reset was installed
// on is the one the player is asked about. The generator picks the first test
// with an HTTP target, which on a base with several client tests is not
// necessarily the primary one — and a reset on a target the briefing never
// names is a puzzle with no findable clue whose graded netdoc run never dials
// it.
func resetTargetIsBriefed(base *Scenario, m GeneratedMutation) bool {
	// The same canonical form the generator worked from, so the two are looking
	// at one scenario.
	base = cloneScenario(base)
	canonicalScenarioInput(base)
	primary, ok := primaryTest(base)
	if !ok {
		return false
	}
	target, ok := findHTTPTestTarget(base, []Test{primary})
	return ok && target.node == m.Node && target.port == m.TargetPort
}

func challengeConditionFor(mutation string) (challengeCondition, bool) {
	for _, condition := range challengeConditions {
		if condition.mutation != "" && condition.mutation == mutation {
			return condition, true
		}
	}
	return challengeCondition{}, false
}

func healthyChallengeCondition() challengeCondition {
	for _, condition := range challengeConditions {
		if condition.mutation == "" {
			return condition
		}
	}
	panic("challenge: no healthy condition")
}

// challengeBases are the known-good controls a challenge may be generated from.
// Every one is already a hunt base, so the generator, the mutation registry and
// the observation rules are shared rather than reimplemented.
//
// socks5h-remote-dns-succeeds is left out: its netdoc run is handed a proxy
// through the environment and an investigator's shell is not, so the two
// contestants would not be looking at the same network.
//
// V1 ids select through this list. Reordering or changing it is a new id
// version — see challengeGenerators.
var challengeBases = []string{"dual-stack-healthy", "healthy", "healthy-routed-network",
	"tls-valid", "two-path-healthy", "two-path-ipv6-healthy"}

// Challenge is one reproducible puzzle. Everything the player may see before
// they answer is above the line; Base, Seed, Case and Manifest name the case
// and are therefore the answer, so nothing prints them before the reveal.
type Challenge struct {
	ID         string `json:"id"`
	Difficulty string `json:"difficulty"`
	// Node is the node the player investigates from, and Target is what they
	// were asked about. Both come from the scenario's own primary test, so
	// neither is a hint the netdoc run does not also get.
	Node   string `json:"node"`
	Target string `json:"target,omitempty"`

	Base     string                `json:"base_scenario"`
	Seed     int64                 `json:"seed"`
	Case     int                   `json:"case"`
	Manifest GeneratedCaseManifest `json:"manifest"`

	Scenario  *Scenario          `json:"-"`
	condition challengeCondition `json:"-"`
}

// Replay is the command that reproduces this exact challenge.
func (c *Challenge) Replay() string { return "netdoc-sim challenge -id " + c.ID }

// challengeGenerators maps an id version to the selection rules that version
// means. An entry is frozen the moment ids carrying it have been shared:
// changing how a version picks a base, a case or a family repoints every id
// ever published with it, so such a change adds a version here rather than
// editing one. Resolution of an old id then keeps working unchanged.
//
// A version covers the whole chain an id resolves through — this file's
// selection, the hunt generator behind it and the base scenarios it draws
// from — because all three decide what a shared id means.
// TestChallengeIDsResolveToTheSameCaseForever pins that chain.
var challengeGenerators = map[string]func(id, digits string) (*Challenge, error){
	"V1": buildChallengeV1,
	"V2": buildChallengeV2,
}

// challengeV1Mutations freezes the conditions V1 ids select through. V1 ids are
// in circulation, so this list can never change: a condition added to
// challengeConditions afterwards would repoint every V1 id whose scan used to
// skip that mutation's case. Conditions added later are reachable from V2
// onwards, which is what the version in an id is for.
var challengeV1Mutations = []string{"dns.drop", "dns.servfail", "family.ipv4_drop", "family.ipv6_drop",
	"routing.preferred_path_failure", "service.tcp_reset", "service.tls_expired"}

func challengeIDVersions() []string {
	out := make([]string, 0, len(challengeGenerators))
	for version := range challengeGenerators {
		out = append(out, version)
	}
	slices.Sort(out)
	return out
}

// NormalizeChallengeID accepts the id in the form a person would type or paste
// it and returns the canonical one, `V1-8F42C1`. It is deliberately strict: an
// id is the whole reproduction contract, so a near miss has to be a rejection
// rather than a different challenge.
//
// A bare `8F42C1` means V1 and always will. Bare was the only form the first
// release published, so re-pointing it at whatever version is current would be
// exactly the silent drift the version prefix exists to prevent.
func NormalizeChallengeID(raw string) (string, error) {
	id := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(raw)), "#")
	version, digits := "V1", id
	if before, after, found := strings.Cut(id, "-"); found {
		version, digits = before, after
	}
	if len(digits) != challengeIDDigits || strings.TrimLeft(digits, "0123456789ABCDEF") != "" {
		return "", fmt.Errorf("a challenge id is %d hex characters after an optional version, like %s-8F42C1, got %q",
			challengeIDDigits, ChallengeIDVersion, id)
	}
	if _, ok := challengeGenerators[version]; !ok {
		return "", fmt.Errorf("challenge id version %q is not one this build can resolve (have: %s)",
			version, strings.Join(challengeIDVersions(), ", "))
	}
	return version + "-" + digits, nil
}

// RandomChallengeID draws a fresh id, at the current version.
func RandomChallengeID() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	value := binary.BigEndian.Uint32(raw[:]) & 0xFFFFFF
	return fmt.Sprintf("%s-%0*X", ChallengeIDVersion, challengeIDDigits, value), nil
}

// challengeSeed derives this id's independent PRNG stream. Every generation
// decision behind an id comes out of this one seed, which is what makes an id
// the whole reproduction contract. The version is in the domain, so the same
// digits under a future version are a different challenge rather than a
// coincidence.
func challengeSeed(version, digits string) int64 {
	h := sha256.New()
	h.Write([]byte(challengeIDDomain))
	h.Write([]byte{0})
	h.Write([]byte(version))
	h.Write([]byte{0})
	h.Write([]byte(digits))
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]))
}

// BuildChallenge resolves an id into the case behind it. It is pure and
// deterministic: the same id builds the same challenge on any machine whose
// build knows that id's version, with no state on disk and no network.
func BuildChallenge(raw string) (*Challenge, error) {
	id, err := NormalizeChallengeID(raw)
	if err != nil {
		return nil, err
	}
	version, digits, _ := strings.Cut(id, "-")
	return challengeGenerators[version](id, digits)
}

// buildChallengeV1 is the V1 selection. Frozen — see challengeGenerators. It
// sees only the conditions that existed when V1 ids were first published.
func buildChallengeV1(id, digits string) (*Challenge, error) {
	return buildChallengeCase(id, "V1", digits, func(mutation string) bool {
		return slices.Contains(challengeV1Mutations, mutation)
	})
}

// buildChallengeV2 is the current selection: every condition the challenge
// contract admits, including the ones netdoc has no vocabulary for.
func buildChallengeV2(id, digits string) (*Challenge, error) {
	return buildChallengeCase(id, "V2", digits, func(string) bool { return true })
}

// buildChallengeCase is the selection every version shares. selectable is the
// only thing a version varies, and it is a question about mutation ids —
// nothing in this function, or in anything it calls, can reach a diagnosis or
// challengeRecognition. Eligibility is settled before Network Doctor exists.
func buildChallengeCase(id, version, digits string, selectable func(string) bool) (*Challenge, error) {
	seed := challengeSeed(version, digits)
	rng := mathrand.New(mathrand.NewSource(seed))
	if rng.Intn(challengeHealthyOdds) == 0 {
		base := challengeBases[rng.Intn(len(challengeBases))]
		return healthyChallenge(id, base, seed, ChallengeDifficulties[rng.Intn(len(ChallengeDifficulties))])
	}
	for attempt := 0; attempt < challengeSearchLimit; attempt++ {
		base := challengeBases[rng.Intn(len(challengeBases))]
		caseNumber := rng.Intn(HuntMaxCaseNumber + 1)
		scenario, err := LibraryScenario(base)
		if err != nil {
			return nil, err
		}
		// One mutation, always: a challenge with two faults would have two
		// defensible answers and no honest way to score either.
		generated, err := GenerateHuntCase(base, scenario, seed, caseNumber, 1)
		if err != nil || len(generated.Manifest.Mutations) != 1 {
			continue
		}
		condition, ok := challengeConditionFor(generated.Manifest.Mutations[0].ID)
		if !ok || !selectable(condition.mutation) {
			continue
		}
		// Asked of the unmutated base, which is where the target the fault
		// replaced is still readable.
		if condition.briefed != nil && !condition.briefed(scenario, generated.Manifest.Mutations[0]) {
			continue
		}
		return newChallenge(id, base, seed, caseNumber, condition.difficulty, generated.Manifest, generated.Scenario, condition)
	}
	return nil, fmt.Errorf("challenge %s: no challenge-capable case within %d candidates", id, challengeSearchLimit)
}

// healthyChallenge is the base scenario with nothing done to it. It carries an
// ordinary manifest with an empty mutation list so the reproduction artifact,
// the fingerprint and the truth rules stay one shape.
func healthyChallenge(id, base string, seed int64, difficulty string) (*Challenge, error) {
	scenario, err := LibraryScenario(base)
	if err != nil {
		return nil, err
	}
	canonicalScenarioInput(scenario)
	if err := scenario.Validate(); err != nil {
		return nil, fmt.Errorf("challenge base %s: %w", base, err)
	}
	manifest := GeneratedCaseManifest{GeneratorVersion: HuntGeneratorVersion, BaseScenario: base,
		HuntSeed: seed, Case: -1, CaseSeed: seed, Mutations: []GeneratedMutation{}}
	manifest.CaseFingerprint = huntCaseFingerprint(manifest)
	return newChallenge(id, base, seed, -1, difficulty, manifest, scenario, healthyChallengeCondition())
}

func newChallenge(id, base string, seed int64, caseNumber int, difficulty string,
	manifest GeneratedCaseManifest, scenario *Scenario, condition challengeCondition) (*Challenge, error) {
	primary, ok := primaryTest(scenario)
	if !ok {
		return nil, fmt.Errorf("challenge base %s has no client test", base)
	}
	return &Challenge{ID: id, Difficulty: difficulty, Node: primary.Node, Target: primary.Target,
		Base: base, Seed: seed, Case: caseNumber, Manifest: manifest, Scenario: scenario, condition: condition}, nil
}

// primaryTest is the run the player is asked about: the first test on the
// client node. It is also the netdoc run scoring reads, so both contestants are
// answering the same question about the same target.
func primaryTest(s *Scenario) (Test, bool) {
	client := clientNode(s)
	for _, test := range s.Tests {
		if test.Node == client {
			return test, true
		}
	}
	return Test{}, false
}

// FindChallenge draws a fresh challenge, optionally of a requested difficulty.
// Difficulty is a property of the case an id resolves to, so honouring a
// request means looking at ids until one carries it.
func FindChallenge(difficulty string) (*Challenge, error) {
	if difficulty != "" && !slices.Contains(ChallengeDifficulties, difficulty) {
		return nil, fmt.Errorf("unknown difficulty %q (have: %s)", difficulty, strings.Join(ChallengeDifficulties, ", "))
	}
	for attempt := 0; attempt < challengeIDSearchLimit; attempt++ {
		id, err := RandomChallengeID()
		if err != nil {
			return nil, err
		}
		challenge, err := BuildChallenge(id)
		if err != nil {
			continue
		}
		if difficulty == "" || challenge.Difficulty == difficulty {
			return challenge, nil
		}
	}
	return nil, fmt.Errorf("no %s challenge found in %d attempts", difficulty, challengeIDSearchLimit)
}

// Scores one contestant can earn. There is deliberately no partial credit: the
// diagnosis model can say whether an answer names what the simulator observed,
// and inventing degrees of nearly-right would be scoring on resemblance.
//
// ChallengeUnrecognized is not a softer ChallengeIncorrect. Both lose the
// round; they say different things about why, and the difference is the whole
// point of the contract. Incorrect means netdoc had the words for this
// condition and reached for different ones. Unrecognized means its vocabulary
// has no way to state the condition at all, so no report it could have written
// would have won — which is the finding worth acting on.
const (
	ChallengeCorrect      = "correct"
	ChallengeIncorrect    = "incorrect"
	ChallengeUnrecognized = "unrecognized"
	ChallengeGaveUp       = "gave_up"
	ChallengeUnscoreable  = "unscoreable"
)

// Matchups.
const (
	ChallengeHumanWins  = "human_wins"
	ChallengeNetdocWins = "network_doctor_wins"
	ChallengeDraw       = "draw"
	ChallengeNobodyWins = "nobody_wins"
	ChallengeNoResult   = "no_result"
)

// ChallengeSubmission is what the person committed to, before any netdoc
// process ran.
type ChallengeSubmission struct {
	Answer  ChallengeAnswer
	GaveUp  bool
	Note    string
	Elapsed time.Duration
}

// ChallengeTruth is what the simulator independently established. It is derived
// from evidence and the mutation manifest only — never from a diagnosis, never
// from the player's answer, and never from a mutation having merely been
// scheduled.
type ChallengeTruth struct {
	Answer      ChallengeAnswer `json:"answer,omitempty"`
	Label       string          `json:"label,omitempty"`
	Scoreable   bool            `json:"scoreable"`
	Reason      string          `json:"reason,omitempty"`
	Explanation string          `json:"explanation,omitempty"`
	// Injected is what the generator asked for. It is intent rather than truth,
	// is never what a score is computed from, and is printed only after both
	// answers are in.
	Injected       string   `json:"injected,omitempty"`
	ObservedFaults []string `json:"observed_faults"`
	Evidence       []string `json:"evidence"`
}

// ChallengeContestant is one graded answer.
type ChallengeContestant struct {
	Answer ChallengeAnswer `json:"answer,omitempty"`
	Label  string          `json:"label,omitempty"`
	Score  string          `json:"score"`
	Detail string          `json:"detail,omitempty"`
	Note   string          `json:"note,omitempty"`
}

// ChallengeTiming is every field of a result that a replay will not reproduce.
// It is one object rather than two loose fields so a consumer diffing two runs
// of the same id knows exactly what it has to ignore: everything else in a
// ChallengeResult is determined by the id and the network.
type ChallengeTiming struct {
	HumanMS  int64 `json:"human_ms"`
	NetdocMS int64 `json:"network_doctor_ms"`
}

// NetdocIdentity is which Network Doctor executable produced a result: the
// absolute path the run launched, and the line that same executable printed for
// -version. It is what makes a saved result reproducible — `netdoc` on the next
// machine, or in the next month, is not necessarily this build.
//
// The version is recorded as the binary reported it. A local build says `dev`,
// and that is the honest answer; nothing here infers a version from the
// checkout, the filename, or the simulator's own build.
type NetdocIdentity struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// ChallengeResult is the whole matchup, and the machine-readable artifact.
type ChallengeResult struct {
	ChallengeID      string              `json:"challenge_id"`
	Difficulty       string              `json:"difficulty"`
	IDVersion        string              `json:"id_version"`
	GeneratorVersion string              `json:"generator_version"`
	BaseScenario     string              `json:"base_scenario"`
	Seed             int64               `json:"seed"`
	Case             int                 `json:"case"`
	CaseFingerprint  string              `json:"case_fingerprint"`
	Node             string              `json:"node"`
	Target           string              `json:"target,omitempty"`
	Netdoc           NetdocIdentity      `json:"netdoc"`
	Truth            ChallengeTruth      `json:"truth"`
	Human            ChallengeContestant `json:"human"`
	NetworkDoctor    ChallengeContestant `json:"network_doctor"`
	Result           string              `json:"result"`
	Timing           ChallengeTiming     `json:"timing"`
	Replay           string              `json:"replay"`
	// Error is the simulator falling over, which is not a matchup.
	Error string `json:"error,omitempty"`
}

// ScoreChallenge grades both contestants against one truth.
//
// The order here is the invariant: truth is established first, from simulator
// evidence and the manifest alone, and only then is either answer looked at.
// Neither contestant appears in challengeTruth's inputs, and neither grading
// function can reach the other's answer.
func ScoreChallenge(c *Challenge, report *Report, submission ChallengeSubmission) *ChallengeResult {
	result := &ChallengeResult{
		// The version this id carries, not the one this build mints: a V1 id
		// resolved by a later build is still a V1 challenge.
		ChallengeID: c.ID, Difficulty: c.Difficulty, IDVersion: challengeIDVersionOf(c.ID),
		GeneratorVersion: c.Manifest.GeneratorVersion, BaseScenario: c.Base, Seed: c.Seed, Case: c.Case,
		CaseFingerprint: c.Manifest.CaseFingerprint, Node: c.Node, Target: c.Target,
		Timing: ChallengeTiming{HumanMS: submission.Elapsed.Milliseconds()}, Replay: c.Replay(),
	}
	result.Truth = challengeTruth(c, report)
	diagnosis := primaryClientDiagnosis(c, report)
	if report != nil {
		result.Error = report.Error
		result.Timing.NetdocMS = netdocDuration(c, report).Milliseconds()
	}

	result.Human = ChallengeContestant{Answer: submission.Answer, Note: submission.Note,
		Label: challengeAnswerLabel(submission.Answer)}
	result.NetworkDoctor = ChallengeContestant{}
	if diagnosis != nil {
		result.NetworkDoctor.Detail = diagnosis.Summary
	}

	if !result.Truth.Scoreable {
		result.Human.Score, result.NetworkDoctor.Score = ChallengeUnscoreable, ChallengeUnscoreable
		result.Result = ChallengeNoResult
		return result
	}

	switch {
	case submission.GaveUp:
		result.Human.Answer, result.Human.Label, result.Human.Score = "", "", ChallengeGaveUp
	case submission.Answer == result.Truth.Answer:
		result.Human.Score = ChallengeCorrect
	default:
		result.Human.Score = ChallengeIncorrect
	}

	// Recognition is looked up by the condition the simulator established, never
	// by the condition the generator asked for, and the lookup can fail: a
	// condition netdoc's vocabulary cannot state is a loss it was always going
	// to take, and the result says so in those words.
	switch recognized, known := challengeRecognition[result.Truth.Answer]; {
	case !known:
		result.NetworkDoctor.Score = ChallengeUnrecognized
		result.NetworkDoctor.Note = "Network Doctor's report has no verdict that states this condition."
	case recognized(diagnosis):
		result.NetworkDoctor.Score = ChallengeCorrect
		result.NetworkDoctor.Answer = result.Truth.Answer
		result.NetworkDoctor.Label = result.Truth.Label
	default:
		result.NetworkDoctor.Score = ChallengeIncorrect
	}

	humanRight := result.Human.Score == ChallengeCorrect
	netdocRight := result.NetworkDoctor.Score == ChallengeCorrect
	switch {
	case humanRight && !netdocRight:
		result.Result = ChallengeHumanWins
	case !humanRight && netdocRight:
		result.Result = ChallengeNetdocWins
	case humanRight && netdocRight:
		result.Result = ChallengeDraw
	default:
		result.Result = ChallengeNobodyWins
	}
	return result
}

// challengeTruth establishes what actually happened. It reads the report's
// evidence and the manifest; it never reads report.Tests, so no diagnosis can
// reach it.
//
// The manifest is used for one thing: knowing which mutation to demand
// independent evidence for. A mutation that was generated, applied, or expected
// to break something establishes nothing on its own — collectObservedTruth only
// lists it under ObservedFaults once service, event, kernel-fault or
// reachability evidence from the executed run supports it.
func challengeTruth(c *Challenge, report *Report) ChallengeTruth {
	truth := ChallengeTruth{Explanation: c.condition.explanation, ObservedFaults: []string{}, Evidence: []string{}}
	if len(c.Manifest.Mutations) == 1 {
		truth.Injected = c.Manifest.Mutations[0].Description
	}
	switch {
	case report == nil:
		truth.Reason = "the simulation produced no report"
		return truth
	case report.Error != "":
		truth.Reason = "the simulation did not complete: " + report.Error
		return truth
	case !report.Cleanup.Done && !report.Cleanup.Kept:
		truth.Reason = "the simulation did not clean up, so its final state is not trustworthy"
		return truth
	}
	// Network Doctor not having produced a diagnosis is not a win for anybody:
	// there is no matchup to score, and voiding the round is the honest outcome.
	if primaryClientDiagnosis(c, report) == nil {
		truth.Reason = "Network Doctor produced no diagnosis for this challenge, so there is nothing to compare"
		return truth
	}

	observed := collectObservedTruth(c.Manifest, report)
	truth.ObservedFaults = observed.ObservedFaults
	truth.Evidence = challengeEvidence(c, report, observed)

	if c.condition.mutation == "" {
		if reason, ok := healthyObserved(c, report, observed); !ok {
			truth.Reason = reason
			return truth
		}
		truth.Answer, truth.Scoreable = AnswerHealthy, true
		truth.Label = challengeAnswerLabel(AnswerHealthy)
		return truth
	}
	if !slices.Contains(observed.ObservedFaults, c.condition.mutation) {
		truth.Reason = "the injected fault left no independent evidence that it took effect, so this challenge has no answer to grade against"
		return truth
	}
	// The condition's own narrower demand, where the shared per-mutation check
	// would settle for less than proof that live traffic met the fault.
	if c.condition.requires != nil && !c.condition.requires(report.Evidence, c.Manifest.Mutations[0]) {
		truth.Reason = "the injected fault was in place but no independent evidence shows it reached live traffic, so this challenge has no answer to grade against"
		return truth
	}
	truth.Answer, truth.Scoreable = c.condition.answer, true
	truth.Label = challengeAnswerLabel(c.condition.answer)
	return truth
}

// healthyObserved requires positive evidence that the network worked, not the
// absence of evidence that it did not. An empty mutation list establishes
// nothing, and neither does netdoc's verdict — this function never reads
// either. A run whose measurements never happened must not be scored as a
// healthy network.
//
// The checks below cover every dimension a challenge is able to break, so
// "healthy" means the same measurements that would have caught each family
// were taken and came back clean:
//
//	family.ipv4_drop, family.ipv6_drop  → family reachability, measured at this node
//	routing.preferred_path_failure      → family reachability and controlled targets
//	dns.servfail, dns.drop              → the resolver's own per-query outcomes
//	service.tcp_reset                   → the target service's own reset records
//	service.tls_expired                 → the TLS services' own handshake records
//	netem.loss                          → the path shapers' own drop counters
//
// TestHealthyChallengeCoversEveryCondition keeps that list and
// challengeConditions in step.
func healthyObserved(c *Challenge, report *Report, observed ObservedTruth) (string, bool) {
	if len(observed.ObservedFaults) > 0 {
		return "the simulator observed a fault in a challenge that injected none", false
	}
	reachable := 0
	for _, item := range report.Evidence.FamilyReachability {
		if item.Node != c.Node {
			continue
		}
		switch item.State {
		case FamilyStateReachable:
			reachable++
		case FamilyStateUnreachable:
			return "the simulator could not reach the " + item.Family + " internet endpoints, so this network was not healthy", false
		}
	}
	if reachable == 0 {
		return "the simulator took no reachability measurement it could call healthy", false
	}
	for _, item := range report.Evidence.ControlledTargets {
		if item.From == c.Node && !item.Reachable {
			return "the simulator could not reach its controlled target " + item.To + ", so this network was not healthy", false
		}
	}
	for _, route := range report.Evidence.Routes {
		if route.Node == c.Node && route.Selected && route.GatewayReachable != nil && !*route.GatewayReachable {
			return "the simulator's selected " + route.Family + " gateway did not answer, so this network was not healthy", false
		}
	}
	if observed.DNS == "unavailable" || observed.DNS == "mixed" {
		return "the simulator observed failing DNS answers, so this network was not healthy", false
	}
	if observed.TCP == "reset" {
		return "the simulator observed a service resetting connections, so this network was not healthy", false
	}
	// The precise predicate the tls_expired family is observed by, not the
	// coarse TLS aggregate: a handshake record that is merely not "passed" is
	// ordinary traffic on a healthy TLS scenario, and reading it as a fault
	// would make a working network unscoreable.
	if anyExpiredCertificateRejected(report.Evidence) {
		return "the simulator observed a client refusing an expired certificate, so this network was not healthy", false
	}
	if observed.Link == "down" {
		return "the simulator observed a link that was down, so this network was not healthy", false
	}
	// The drop counter, not merely an installed shaper: the same reading the
	// netem.loss condition is established by, so "healthy" and "impaired" are
	// decided from one measurement rather than two that could disagree.
	if observed.Packet == "drops_observed" {
		return "the simulator observed a path shaper discarding packets, so this network was not healthy", false
	}
	return "", true
}

// challengeEvidence is the reveal's evidence block: what the simulator measured
// for itself, in a fixed order, with no diagnosis in it.
func challengeEvidence(c *Challenge, report *Report, observed ObservedTruth) []string {
	var out []string
	for _, family := range []struct{ key, label string }{{"ipv4", "IPv4"}, {"ipv6", "IPv6"}} {
		for _, item := range report.Evidence.FamilyReachability {
			if item.Node != c.Node || item.Family != family.key {
				continue
			}
			out = append(out, "internet path over "+family.label+": "+item.State+viaSuffix(item.Via))
		}
	}
	for _, item := range report.Evidence.ControlledTargets {
		if item.From != c.Node {
			continue
		}
		out = append(out, "controlled target "+item.To+": "+reachableWord(item.Reachable)+viaSuffix(item.Via))
	}
	// One line per gateway, not per destination: the client selects the same
	// route for every controlled endpoint, and repeating it reads as two
	// measurements when it is one.
	for _, route := range report.Evidence.Routes {
		if route.Node != c.Node || !route.Selected || route.GatewayReachable == nil {
			continue
		}
		line := "selected " + route.Family + " gateway " + route.Via + ": " + reachableWord(*route.GatewayReachable)
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	if observed.DNS != "unknown" {
		out = append(out, "DNS answers observed by the resolver: "+observed.DNS)
	}
	if observed.TLS != "unknown" {
		out = append(out, "TLS handshakes observed by the target: "+observed.TLS)
	}
	if observed.TCP != "unknown" {
		out = append(out, "TCP service behaviour: "+observed.TCP)
	}
	if observed.Link != "unknown" {
		out = append(out, "simulated links: "+observed.Link)
	}
	if observed.Packet != "unknown" {
		out = append(out, "path shaping counted by the kernel: "+observed.Packet)
	}
	return out
}

func viaSuffix(via []string) string {
	if len(via) == 0 {
		return ""
	}
	return " (via " + strings.Join(via, " ") + ")"
}

func reachableWord(reachable bool) string {
	if reachable {
		return FamilyStateReachable
	}
	return FamilyStateUnreachable
}

// primaryClientDiagnosis returns the diagnosis from the run the player was
// asked about — the first one on the challenge node. Later runs in a multi-test
// base describe a different target and would answer a different question.
func primaryClientDiagnosis(c *Challenge, report *Report) *Diagnosis {
	if report == nil {
		return nil
	}
	for i := range report.Tests {
		if report.Tests[i].Node == c.Node {
			return report.Tests[i].Diagnosis
		}
	}
	return nil
}

func netdocDuration(c *Challenge, report *Report) time.Duration {
	for i := range report.Tests {
		if report.Tests[i].Node == c.Node {
			return report.Tests[i].Duration
		}
	}
	return 0
}

// challengeSessionLabel is what the shell banner calls the node, kept out of
// the reveal path so it cannot grow a hint.
func challengeSessionLabel(c *Challenge) string {
	if c.Target == "" {
		return c.Node + " (no specific target: the client's own connectivity)"
	}
	return c.Node + " → " + c.Target
}

func challengeIDVersionOf(id string) string {
	version, _, _ := strings.Cut(id, "-")
	return version
}

func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// formatElapsed renders an investigation time the way a person reads one.
func formatElapsed(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
}
