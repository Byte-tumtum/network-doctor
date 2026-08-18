package simulation

import "slices"

// The evidence side of Challenge Mode's truth model.
//
// Every condition a challenge can set carries two predicates over simulator
// observation, and neither of them can reach a diagnosis:
//
//   - the scoped one, mutationObserved plus the condition's own requires,
//     answers "did this exact mutation, on this node and this port, meet live
//     traffic". It is what establishes the answer for a challenge that injected
//     a fault, and it is deliberately narrow: it knows which node was mutated.
//
//   - the unscoped one, the signature in this file, answers the different
//     question "does this run show a fault of this class at all, anywhere". It
//     is what a healthy challenge is graded against, where there is no mutation
//     to scope by and the only honest test is that no condition's trace is
//     present.
//
// Splitting them is what let the hand-maintained half go. healthyObserved used
// to restate every condition's negation in prose, kept in step with
// challengeConditions by a test that spelled the mapping out a third time.
// Adding a playable diagnosis meant editing all three and remembering to. Now
// the healthy verdict is derived by walking challengeConditions, so a condition
// added without a signature fails loudly in one place rather than quietly
// widening what counts as a healthy network.
//
// A signature may be shared by several conditions, and several of them are. It
// is a class of trace rather than a fingerprint of one mutation: the scoped
// predicate is where a family is told apart from its neighbours, and asking the
// unscoped one to do that too would be asking it to name a fault from evidence
// that does not identify one.

// challengeObservation is everything an evidence signature may read. It carries
// evidence and derived simulator truth, and no *Report, so a signature cannot
// reach report.Tests and therefore cannot reach Network Doctor's diagnosis even
// by accident. The node is the client the challenge is played from, which is
// what scopes the observations that are per-node.
type challengeObservation struct {
	node     string
	evidence Evidence
	truth    ObservedTruth
}

// challengeSignature reports whether this run carries the trace of one
// condition class, and says in a person's words what was seen. The reason is
// returned rather than composed by the caller so that each condition explains
// its own evidence, which is what keeps a rejected healthy challenge
// diagnostic instead of merely unscoreable.
type challengeSignature func(challengeObservation) (string, bool)

// signatureDNSFailing is the resolver's own per-query outcomes. "mixed" counts:
// a resolver that answered some queries and refused others still refused
// somebody, and a healthy network refuses nobody.
func signatureDNSFailing(obs challengeObservation) (string, bool) {
	if obs.truth.DNS == "unavailable" || obs.truth.DNS == "mixed" {
		return "the simulator observed failing DNS answers, so this network was not healthy", true
	}
	return "", false
}

// signatureTCPReset is the target service's own record of tearing a connection
// down, not a client-side failure that could have been anything.
func signatureTCPReset(obs challengeObservation) (string, bool) {
	if obs.truth.TCP == "reset" {
		return "the simulator observed a service resetting connections, so this network was not healthy", true
	}
	return "", false
}

// signatureExpiredCertificate is the precise predicate the tls_expired family is
// established by, not the coarse TLS aggregate. A handshake record that is
// merely not "passed" is ordinary traffic on a healthy TLS scenario, and reading
// it as a fault would make a working network unscoreable.
func signatureExpiredCertificate(obs challengeObservation) (string, bool) {
	if anyExpiredCertificateRejected(obs.evidence) {
		return "the simulator observed a client refusing an expired certificate, so this network was not healthy", true
	}
	return "", false
}

func signatureMismatchedCertificate(obs challengeObservation) (string, bool) {
	if anyMismatchedCertificateRejected(obs.evidence) {
		return "the simulator observed a client refusing a certificate issued for another name, so this network was not healthy", true
	}
	return "", false
}

// signatureFamilyUnreachable reads the client's own measured reachability for
// one address family, or for any of them when family is empty. Empty is what
// the route faults use: losing the way out is visible as a family that stopped
// answering, and which family it was is the scoped predicate's business.
func signatureFamilyUnreachable(family string) challengeSignature {
	return func(obs challengeObservation) (string, bool) {
		for _, item := range obs.evidence.FamilyReachability {
			if item.Node != obs.node || (family != "" && item.Family != family) {
				continue
			}
			if item.State == FamilyStateUnreachable {
				return "the simulator could not reach the " + item.Family +
					" internet endpoints, so this network was not healthy", true
			}
		}
		return "", false
	}
}

// signatureControlledTargetRefused and signatureControlledTargetUnreachable are
// one another's negative, the same way their scoped counterparts are. A refusal
// is something only a reachable host can send, so collapsing the two into "the
// dial failed" would erase the distinction the scoring turns on.
func signatureControlledTargetRefused(obs challengeObservation) (string, bool) {
	for _, item := range obs.evidence.ControlledTargets {
		if item.From == obs.node && !item.Reachable && item.Outcome == TargetStateRefused {
			return "the simulator's own connection to " + item.To + " was refused, so this network was not healthy", true
		}
	}
	return "", false
}

func signatureControlledTargetUnreachable(obs challengeObservation) (string, bool) {
	for _, item := range obs.evidence.ControlledTargets {
		if item.From == obs.node && !item.Reachable && item.Outcome != TargetStateRefused {
			return "the simulator could not reach its controlled target " + item.To +
				", so this network was not healthy", true
		}
	}
	return "", false
}

// signaturePacketDrops is the qdisc's own drop counter, not a shaper having
// been installed. A shaper that matched no traffic impaired nobody, and this is
// the only reading that separates the two, the same reading the netem.loss
// condition is established by, so health and impairment cannot be decided from
// two measurements that disagree.
func signaturePacketDrops(obs challengeObservation) (string, bool) {
	if obs.truth.Packet == "drops_observed" {
		return "the simulator observed a path shaper discarding packets, so this network was not healthy", true
	}
	return "", false
}

// signatureInboundTCPDropped is the kernel's own count of discarded inbound TCP
// packets, which is the tcp_port_blocked condition itself.
func signatureInboundTCPDropped(obs challengeObservation) (string, bool) {
	for _, drop := range obs.evidence.PacketDrops {
		if droppedInbound(drop, "tcp", drop.Port) {
			return "the simulator counted discarded inbound TCP packets at " + drop.Node +
				", so this network was not healthy", true
		}
	}
	return "", false
}

// signatureNoDefaultRoute reads the client's real routing table. It is the only
// signature that establishes an absence, which is why it needs the table rather
// than a per-destination lookup: a failed lookup and a missing route are
// different claims.
func signatureNoDefaultRoute(obs challengeObservation) (string, bool) {
	for _, table := range obs.evidence.RouteTables {
		if table.Node == obs.node && len(defaultRoutesIn(table.Routes)) == 0 {
			return "the client's " + table.Family +
				" routing table held no default route, so this network was not healthy", true
		}
	}
	return "", false
}

// healthyObserved requires positive evidence that the network worked, not the
// absence of evidence that it did not. An empty mutation list establishes
// nothing, and neither does netdoc's verdict; this function never reads
// either. A run whose measurements never happened must not be scored as a
// healthy network.
//
// It is two halves. The floor is the measurements that have to have been taken
// and come back clean for "healthy" to be a finding rather than a shrug, plus
// the two faults no challenge condition covers: a link that went down and a
// selected gateway that did not answer are both excluded from the challenge
// contract, so nothing in challengeConditions would catch them.
//
// The rest is derived. Every condition a challenge can set is asked whether its
// own trace is in this run, so "healthy" means every fault the game can inject
// was looked for with that fault's own predicate and not found.
func healthyObserved(c *Challenge, report *Report, observed ObservedTruth) (string, bool) {
	if len(observed.ObservedFaults) > 0 {
		return "the simulator observed a fault in a challenge that injected none", false
	}
	obs := challengeObservation{node: c.Node, evidence: report.Evidence, truth: observed}
	// The floor, first, because a run that measured nothing has to say so rather
	// than report the first condition that happened not to leave a trace.
	reachable := 0
	for _, item := range report.Evidence.FamilyReachability {
		if item.Node == c.Node && item.State == FamilyStateReachable {
			reachable++
		}
	}
	if reason, present := signatureFamilyUnreachable("")(obs); present {
		return reason, false
	}
	if reachable == 0 {
		return "the simulator took no reachability measurement it could call healthy", false
	}
	for _, route := range report.Evidence.Routes {
		if route.Node == c.Node && route.Selected && route.GatewayReachable != nil && !*route.GatewayReachable {
			return "the simulator's selected " + route.Family + " gateway did not answer, so this network was not healthy", false
		}
	}
	if observed.Link == "down" {
		return "the simulator observed a link that was down, so this network was not healthy", false
	}
	// Derived: no condition the game can set may have left its trace here.
	for _, condition := range challengeConditions {
		if condition.signature == nil {
			continue
		}
		if reason, present := condition.signature(obs); present {
			return reason, false
		}
	}
	return "", true
}

// challengePlayableAnswers is the diagnosis vocabulary a challenge can actually
// be set on, derived from challengeConditions rather than listed again. It is
// what V4 generation draws from and what the answer menu is checked against, so
// there is no second taxonomy to drift: a condition added to the table is
// playable, and one removed stops being playable, without a list anywhere
// agreeing to it separately.
//
// Ordered by first appearance in the table, so repeated calls agree.
func challengePlayableAnswers() []ChallengeAnswer {
	var out []ChallengeAnswer
	for _, condition := range challengeConditions {
		if condition.mutation == "" || slices.Contains(out, condition.answer) {
			continue
		}
		out = append(out, condition.answer)
	}
	return out
}

// challengeConditionsFor lists the conditions that establish one answer. More
// than one is ordinary: dns.servfail and dns.drop are two ways to produce a
// single diagnosis, which is exactly why a generator that drew mutations
// uniformly would hand out twice as much DNS as anything else.
func challengeConditionsFor(answer ChallengeAnswer) []challengeCondition {
	var out []challengeCondition
	for _, condition := range challengeConditions {
		if condition.mutation != "" && condition.answer == answer {
			out = append(out, condition)
		}
	}
	return out
}
