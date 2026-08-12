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
	ChallengeIDVersion = "V1"
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

// challengeFamily binds one hunt mutation to the answer its observed effect
// makes true, the difficulty that effect carries for a person, and what counts
// as Network Doctor recognizing it.
//
// recognized reads one diagnosis and nothing else, exactly as the hunt oracle's
// half does: netdoc is never told which family it is looking at, and never
// graded on prose.
type challengeFamily struct {
	// mutation is the hunt mutation id, empty for the healthy challenge.
	mutation    string
	answer      ChallengeAnswer
	difficulty  string
	explanation string
	recognized  func(*Diagnosis) bool
	// briefed, when set, is the extra condition that makes a generated case a
	// fair puzzle rather than merely a possible one: it is handed the unmutated
	// base and the mutation, and reports whether the fault sits where the player
	// was pointed. A base with more than one client test can place a service
	// fault on a target the briefing never names — a clue nobody was shown, and a
	// question the graded netdoc run was never asked.
	briefed func(*Scenario, GeneratedMutation) bool
}

// challengeFamilies is the reviewed subset of hunt mutations that make fair
// human puzzles. A hunt mutation is useful for adversarial automation without
// being a good challenge, so membership here is explicit rather than derived.
//
// V1 ids select through this list, so adding or removing an entry changes what
// ids already in circulation resolve to. That is a new id version, not an edit
// — see challengeGenerators.
//
// Deliberately absent, and why:
//   - every timed family (timeline.*, link.transient_down): a scheduled fault is
//     measured from the instant the first netdoc process starts, so it would be
//     over — or not yet started — while the person was investigating. The two
//     contestants have to face the same network.
//   - netem loss, latency and jitter: netdoc has no impairment verdict to give,
//     so grading it against one would score it on a contract its probes never
//     made.
//   - http.status_503 and encrypted_dns.doh_invalid: both are a working service
//     answering, which netdoc reports as working on purpose. The hunt control
//     `http-error` pins that down.
//   - proxy.connect_refused: the netdoc process is handed the proxy through its
//     environment and a shell is not, so the two contestants would be looking at
//     different networks.
//   - quic.udp_443_block: ordinary tools cannot distinguish a filtered UDP port
//     from a silent one, so there is no evidence a person could reason from.
var challengeFamilies = []challengeFamily{
	{
		mutation: "", answer: AnswerHealthy, difficulty: "",
		explanation: "Nothing was injected. Every layer the simulator measured — the link, the gateway, name resolution and the path to the controlled endpoints — was working.",
		recognized:  func(d *Diagnosis) bool { return d.Verdict == diagnostic.VerdictOK },
	},
	{
		mutation: "dns.servfail", answer: AnswerDNSFailure, difficulty: DifficultyEasy,
		explanation: "The client's resolver answered queries with SERVFAIL. Nothing below DNS was touched: the link, the gateway and the path to the target's address kept working.",
		recognized:  flaggedRow(diagnostic.ProbeDNS),
	},
	{
		mutation: "dns.drop", answer: AnswerDNSFailure, difficulty: DifficultyEasy,
		explanation: "The client's resolver silently discarded queries, so name resolution timed out rather than failing fast. Nothing below DNS was touched.",
		recognized:  flaggedRow(diagnostic.ProbeDNS),
	},
	{
		mutation: "service.tcp_reset", answer: AnswerReset, difficulty: DifficultyEasy,
		explanation: "The target's service accepted the TCP connection and then reset it. The path to the target is intact — the connection completes before it is torn down.",
		recognized:  causeRecognizer(diagnostic.ConnectionCauseReset),
		briefed:     resetTargetIsBriefed,
	},
	{
		mutation: "service.tls_expired", answer: AnswerTLSCertificate, difficulty: DifficultyMedium,
		explanation: "The target served an expired certificate and the client refused it. TCP to the port succeeds, so everything below TLS is healthy.",
		recognized:  conditionRecognizer(ConditionTLSCertificateExpired),
	},
	{
		mutation: "family.ipv4_drop", answer: AnswerIPv4Failure, difficulty: DifficultyMedium,
		explanation: "IPv4 forwarding was dropped at the gateway while IPv6 kept working. The client still has an IPv4 address and an IPv4 route — what it does not have is an IPv4 path.",
		recognized:  conditionRecognizer(ConditionIPv4InternetUnreachable),
	},
	{
		mutation: "family.ipv6_drop", answer: AnswerIPv6Failure, difficulty: DifficultyHard,
		explanation: "IPv6 forwarding was dropped at the gateway while IPv4 kept working. Most traffic still succeeds by falling back to IPv4, which is what makes this one quiet.",
		recognized:  conditionRecognizer(ConditionIPv6InternetUnreachable),
	},
	{
		mutation: "routing.preferred_path_failure", answer: AnswerPreferredRoute, difficulty: DifficultyHard,
		explanation: "The client's lower-metric default route is still selected and its gateway still answers, but that router's own upstream link is down. The higher-metric alternate default remains healthy, which a controlled target on that path proves.",
		recognized:  causeRecognizer(diagnostic.RouteCausePreferredPathFailed),
	},
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

func challengeFamilyFor(mutation string) (challengeFamily, bool) {
	for _, family := range challengeFamilies {
		if family.mutation != "" && family.mutation == mutation {
			return family, true
		}
	}
	return challengeFamily{}, false
}

func healthyChallengeFamily() challengeFamily {
	for _, family := range challengeFamilies {
		if family.mutation == "" {
			return family
		}
	}
	panic("challenge: no healthy family")
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

	Scenario *Scenario       `json:"-"`
	family   challengeFamily `json:"-"`
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
	ChallengeIDVersion: buildChallengeV1,
}

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

// buildChallengeV1 is the V1 selection. Frozen — see challengeGenerators.
func buildChallengeV1(id, digits string) (*Challenge, error) {
	seed := challengeSeed("V1", digits)
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
		family, ok := challengeFamilyFor(generated.Manifest.Mutations[0].ID)
		if !ok {
			continue
		}
		// Asked of the unmutated base, which is where the target the fault
		// replaced is still readable.
		if family.briefed != nil && !family.briefed(scenario, generated.Manifest.Mutations[0]) {
			continue
		}
		return newChallenge(id, base, seed, caseNumber, family.difficulty, generated.Manifest, generated.Scenario, family)
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
	return newChallenge(id, base, seed, -1, difficulty, manifest, scenario, healthyChallengeFamily())
}

func newChallenge(id, base string, seed int64, caseNumber int, difficulty string,
	manifest GeneratedCaseManifest, scenario *Scenario, family challengeFamily) (*Challenge, error) {
	primary, ok := primaryTest(scenario)
	if !ok {
		return nil, fmt.Errorf("challenge base %s has no client test", base)
	}
	return &Challenge{ID: id, Difficulty: difficulty, Node: primary.Node, Target: primary.Target,
		Base: base, Seed: seed, Case: caseNumber, Manifest: manifest, Scenario: scenario, family: family}, nil
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
const (
	ChallengeCorrect     = "correct"
	ChallengeIncorrect   = "incorrect"
	ChallengeGaveUp      = "gave_up"
	ChallengeUnscoreable = "unscoreable"
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

	result.NetworkDoctor.Score = ChallengeIncorrect
	if c.family.recognized(diagnosis) {
		result.NetworkDoctor.Score = ChallengeCorrect
		result.NetworkDoctor.Answer = result.Truth.Answer
		result.NetworkDoctor.Label = result.Truth.Label
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
	truth := ChallengeTruth{Explanation: c.family.explanation, ObservedFaults: []string{}, Evidence: []string{}}
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

	if c.family.mutation == "" {
		if reason, ok := healthyObserved(c, report, observed); !ok {
			truth.Reason = reason
			return truth
		}
		truth.Answer, truth.Scoreable = AnswerHealthy, true
		truth.Label = challengeAnswerLabel(AnswerHealthy)
		return truth
	}
	if !slices.Contains(observed.ObservedFaults, c.family.mutation) {
		truth.Reason = "the injected fault left no independent evidence that it took effect, so this challenge has no answer to grade against"
		return truth
	}
	truth.Answer, truth.Scoreable = c.family.answer, true
	truth.Label = challengeAnswerLabel(c.family.answer)
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
//
// TestHealthyChallengeCoversEveryFamily keeps that list and challengeFamilies
// in step.
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
