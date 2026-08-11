package simulation

import (
	"slices"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// A hunt false negative means exactly one thing: the simulator independently
// established a network condition whose diagnostic meaning Network Doctor
// failed to recognize. It deliberately does not mean "a mutation expected probe
// X to fail and probe X did not fail" — a mutation is intent rather than truth,
// and a probe id is an implementation detail of how the diagnosis is assembled
// rather than the thing a user is told.
//
// NetworkCondition is the vocabulary in between: one domain-level fact about
// the network. Every condition is established from simulator-side observation
// alone and recognized from one diagnosis alone. Nothing in this file reads the
// mutation manifest, and no diagnosis ever feeds back into truth.
type NetworkCondition string

const (
	ConditionIPv4InternetUnreachable NetworkCondition = "ipv4_internet_unreachable"
	ConditionIPv6InternetUnreachable NetworkCondition = "ipv6_internet_unreachable"
	ConditionTLSCertificateExpired   NetworkCondition = "tls_certificate_expired"
	ConditionProxyDestinationRefused NetworkCondition = "proxy_destination_refused"
	ConditionQUICUDP443Blocked       NetworkCondition = "quic_udp_443_blocked"
)

// conditionRule holds the two halves of one oracle entry side by side, because
// the pair is what has to be reviewable: what simulator truth establishes this
// condition, and what diagnosis output counts as recognizing it. The two
// functions never see each other's input.
type conditionRule struct {
	condition NetworkCondition
	// family scopes a condition to one address family so per-family findings
	// stay distinguishable. Empty for conditions that have no family.
	family string
	// summary is the domain sentence a finding is built from — a network fact,
	// not a probe row.
	summary string
	// evidence names the independent observation that established the
	// condition. It describes the simulator side only; a finding must be
	// readable without trusting anything netdoc said.
	evidence string
	// observed takes evidence and derived simulator truth rather than the whole
	// report, so a rule cannot reach netdoc's diagnosis even by accident.
	observed func(Evidence, ObservedTruth) bool
	// recognized reads one diagnosis. It must never read simulator evidence.
	recognized func(*Diagnosis) bool
}

// conditionOracle is ordered API. A slice rather than a map so repeated runs
// emit findings in one order regardless of Go's map iteration.
//
// Recognition is expressed over netdoc's cause vocabulary and its structured
// per-family verdicts, not over probe ids, because those are the parts of the
// report that say what is wrong with the network rather than which row noticed.
// Splitting or merging probe rows therefore leaves this table correct.
var conditionOracle = []conditionRule{
	{
		condition: ConditionIPv4InternetUnreachable,
		family:    "ipv4",
		summary:   "IPv4 internet reachability lost",
		evidence:  "the client node's own TCP dial of the controlled IPv4 endpoints did not complete",
		observed: func(_ Evidence, truth ObservedTruth) bool {
			return truth.IPv4 == FamilyStateUnreachable
		},
		recognized: func(d *Diagnosis) bool {
			return diagnosedFamily(d, "ipv4") == FamilyStateUnreachable ||
				flaggedCause(d, nil, diagnostic.FamilyCauseIPv4Unreachable)
		},
	},
	{
		condition: ConditionIPv6InternetUnreachable,
		family:    "ipv6",
		summary:   "IPv6 internet reachability lost",
		evidence:  "the client node's own TCP dial of the controlled IPv6 endpoints did not complete",
		observed: func(_ Evidence, truth ObservedTruth) bool {
			return truth.IPv6 == FamilyStateUnreachable
		},
		recognized: func(d *Diagnosis) bool {
			return diagnosedFamily(d, "ipv6") == FamilyStateUnreachable ||
				flaggedCause(d, nil, diagnostic.FamilyCauseIPv6Unreachable)
		},
	},
	{
		condition: ConditionTLSCertificateExpired,
		summary:   "a target serving an expired TLS certificate",
		evidence:  "a controlled TLS service presented an expired certificate and watched a client refuse it",
		observed: func(evidence Evidence, _ ObservedTruth) bool {
			return anyExpiredCertificateRejected(evidence)
		},
		// An expired certificate has its own cause in netdoc's vocabulary and
		// its own fix, so a bare handshake failure is deliberately not
		// equivalent: it tells the user to suspect a MITM proxy or clock skew
		// for a certificate whose dates are readable on the wire.
		recognized: func(d *Diagnosis) bool {
			return flaggedCause(d, nil, diagnostic.TLSCauseCertificateExpired)
		},
	},
	{
		condition: ConditionProxyDestinationRefused,
		summary:   "a proxy refusing its CONNECT destination",
		evidence:  "a controlled SOCKS5 proxy answered a CONNECT request with a refusal",
		observed: func(evidence Evidence, _ ObservedTruth) bool {
			return anyProxyCONNECTRefused(evidence)
		},
		// Reaching the proxy and being refused by it is a different fault from
		// not reaching the proxy at all, and netdoc separates the two. Only the
		// destination cause says the proxy answered and declined; proxy
		// unreachable or a protocol failure would be a wrong answer, not a
		// second way of giving the right one.
		recognized: func(d *Diagnosis) bool {
			return flaggedCause(d, nil, diagnostic.ProxyCauseDestinationUnreachable)
		},
	},
	{
		condition: ConditionQUICUDP443Blocked,
		summary:   "QUIC datagrams to UDP/443 being dropped on the path",
		evidence:  "the kernel drop counter on the controlled path counted inbound UDP/443 packets",
		// The counter is also the control for "while relevant connectivity
		// remains healthy": packets can only be counted once a client resolved
		// the endpoint and put datagrams on the wire, which a dead path would
		// have prevented.
		observed: func(evidence Evidence, _ ObservedTruth) bool {
			return anyUDPPortDropped(evidence, 443)
		},
		// Scoped to the QUIC row because "timeout" is not unique in netdoc's
		// cause vocabulary — TLS and encrypted DNS spend it too, and an
		// unrelated TLS timeout must not read as QUIC being recognized. This is
		// the only entry that needs a row scope, and it names the probe through
		// the diagnostic constant so a renamed id stays in step.
		recognized: func(d *Diagnosis) bool {
			return flaggedCause(d, []string{string(diagnostic.ProbeQUIC)},
				diagnostic.QUICCauseHandshake, diagnostic.QUICCauseTimeout)
		},
	},
}

// observedConditions returns the semantic conditions this run's independent
// simulator observations establish, in registry order.
func observedConditions(evidence Evidence, truth ObservedTruth) []NetworkCondition {
	var out []NetworkCondition
	for _, rule := range conditionOracle {
		if rule.observed(evidence, truth) {
			out = append(out, rule.condition)
		}
	}
	return out
}

// recognizedConditions returns the semantic conditions one diagnosis
// communicates, in registry order.
func recognizedConditions(d *Diagnosis) []NetworkCondition {
	var out []NetworkCondition
	if d == nil {
		return out
	}
	for _, rule := range conditionOracle {
		if rule.recognized(d) {
			out = append(out, rule.condition)
		}
	}
	return out
}

// unrecognizedConditionFindings reconciles the two halves. A condition the
// simulator never established produces nothing, however loudly the manifest
// says a mutation was scheduled or applied: an unobserved effect is a coverage
// question, not an accusation about the diagnosis.
func unrecognizedConditionFindings(report *Report, truth ObservedTruth) []HuntCaseFinding {
	if !finalStateComparable(report) {
		return nil
	}
	diagnosis := finalClientDiagnosis(report)
	if diagnosis == nil {
		return nil
	}
	recognized := recognizedConditions(diagnosis)
	var findings []HuntCaseFinding
	for _, rule := range conditionOracle {
		if !rule.observed(report.Evidence, truth) || slices.Contains(recognized, rule.condition) {
			continue
		}
		findings = append(findings, HuntCaseFinding{Category: FindingFalseNegative, Severity: SeverityHigh,
			Code: "unrecognized_network_condition", Family: rule.family,
			Expected: string(rule.condition), Actual: "unrecognized",
			Summary:  "The simulator independently observed " + rule.summary + ", but the diagnosis did not recognize it.",
			Evidence: rule.evidence})
	}
	return findings
}

// finalClientDiagnosis returns the last diagnosis the one client node produced.
// Final because it is closest in time to the final evidence collection; earlier
// runs in a multi-test timeline scenario can legitimately describe an older
// state.
func finalClientDiagnosis(report *Report) *Diagnosis {
	client := observedClient(report)
	if client == "" {
		return nil
	}
	for i := len(report.Tests) - 1; i >= 0; i-- {
		if report.Tests[i].Node == client {
			return report.Tests[i].Diagnosis
		}
	}
	return nil
}

// flaggedCause reports whether the diagnosis raised its hand on a row carrying
// one of these causes. Only FAIL and WARN count: a cause on a passing row is
// context, not a finding. rows scopes the search to particular report rows and
// is set only where netdoc reuses one cause string across subjects; nil means
// any row, which is what keeps recognition independent of which probe noticed.
func flaggedCause(d *Diagnosis, rows []string, causes ...string) bool {
	for _, check := range d.Checks {
		if !flagged(check.Status) || (rows != nil && !slices.Contains(rows, check.ID)) {
			continue
		}
		if slices.Contains(causes, check.Cause) {
			return true
		}
	}
	return false
}

// diagnosedFamily reads netdoc's structured per-family egress verdict from
// whichever row published one. Status is not required here: a row that states
// "ipv4: unreachable" has communicated the fact even while the run as a whole
// passes on the other family, which is exactly the dual-stack case.
func diagnosedFamily(d *Diagnosis, family string) string {
	for _, check := range d.Checks {
		if check.Families == nil {
			continue
		}
		state := check.Families.IPv6
		if family == "ipv4" {
			state = check.Families.IPv4
		}
		if state != "" {
			return state
		}
	}
	return ""
}

// The three predicates below define what it takes for one fault to have reached
// the wire, judged from a single evidence record. They are shared so the
// unscoped question the oracle asks and the scoped question per-mutation
// observation asks cannot drift apart, and each requires the record to name the
// node that produced it: a holder always stamps its own name, so a record
// without one is not an observation anybody made.
//
// rejectedExpiredCertificate wants the handshake rather than the service's
// configured mode, because a certificate nobody was ever shown expired in
// private.
func rejectedExpiredCertificate(handshake TLSEvidence) bool {
	return handshake.Node != "" && handshake.CertificateMode == TLSCertificateExpired &&
		handshake.CertificatePresented && handshake.Result == "client_rejected_certificate" &&
		handshake.Count > 0
}

func refusedCONNECT(request SOCKSEvidence) bool {
	return request.Node != "" && request.Event == "connect" &&
		request.Result == "connection_refused" && request.Count > 0
}

// droppedInboundUDP reports the rule's own kernel counter, not the fault record
// that installed it: an installed rule that never matched a packet blocked
// nothing.
func droppedInboundUDP(drop PacketDropEvidence, port int) bool {
	return drop.Node != "" && drop.Protocol == "udp" && drop.Direction == DirectionInbound &&
		drop.Packets > 0 && drop.Port == port
}

// The any* readers answer the oracle's question, which is unscoped on purpose:
// a network condition is a fact about the run, and the oracle must not know
// which node a mutation aimed at. That wildcard is spelled out in the name
// rather than inferred from an empty argument, so the scoped readers below are
// free to treat a missing scope as insufficient evidence.
func anyExpiredCertificateRejected(evidence Evidence) bool {
	return slices.ContainsFunc(evidence.TLS, rejectedExpiredCertificate)
}

func anyProxyCONNECTRefused(evidence Evidence) bool {
	return slices.ContainsFunc(evidence.SOCKSRequests, refusedCONNECT)
}

func anyUDPPortDropped(evidence Evidence, port int) bool {
	return port != 0 && slices.ContainsFunc(evidence.PacketDrops, func(drop PacketDropEvidence) bool {
		return droppedInboundUDP(drop, port)
	})
}

// The *At readers answer the different question of whether one mutation took
// effect where it said it would. A mutation that does not name the place its
// effect belongs establishes nothing: an unanswerable question is insufficient
// evidence, not a wildcard, and treating it as one would let a partially filled
// manifest confirm itself from a fault some other node produced.
func expiredCertificateRejectedAt(evidence Evidence, node, service string) bool {
	if node == "" || service == "" {
		return false
	}
	return slices.ContainsFunc(evidence.TLS, func(handshake TLSEvidence) bool {
		return handshake.Node == node && handshake.Service == service && rejectedExpiredCertificate(handshake)
	})
}

func proxyCONNECTRefusedAt(evidence Evidence, node, service, destination string, port int) bool {
	if node == "" || service == "" || destination == "" || port == 0 {
		return false
	}
	return slices.ContainsFunc(evidence.SOCKSRequests, func(request SOCKSEvidence) bool {
		return request.Node == node && request.Service == service &&
			request.Destination == destination && request.Port == port && refusedCONNECT(request)
	})
}

func udpPortDroppedAt(evidence Evidence, node string, port int) bool {
	if node == "" || port == 0 {
		return false
	}
	return slices.ContainsFunc(evidence.PacketDrops, func(drop PacketDropEvidence) bool {
		return drop.Node == node && droppedInboundUDP(drop, port)
	})
}
