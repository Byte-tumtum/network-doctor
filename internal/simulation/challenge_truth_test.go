package simulation

import (
	"testing"
)

// Every condition the game can set has to carry its own evidence signature.
// This is the check that replaced a hand-maintained list: healthyObserved used
// to restate each condition's negation in prose, so a condition added without
// updating it would have widened what counts as a healthy network, silently and
// in the direction that scores a broken network as fine. Now the healthy
// verdict walks this table, and a missing signature fails here instead.
func TestEveryChallengeConditionCarriesASignature(t *testing.T) {
	for _, condition := range challengeConditions {
		if condition.mutation == "" {
			if condition.signature != nil {
				t.Errorf("the healthy condition has an evidence signature; it is the absence of every other one")
			}
			continue
		}
		if condition.signature == nil {
			t.Errorf("%s can be set as a challenge but carries no evidence signature, "+
				"so a run of it would pass for a healthy network", condition.mutation)
		}
	}
}

// The signature table, positive and negative. Each case is one condition's
// canonical trace, plus the traces of its nearest neighbours, which must not
// fire it. A signature that answered yes to a neighbour would let one fault be
// established by another's evidence.
func TestEvidenceSignaturesDiscriminate(t *testing.T) {
	const node = "client"
	// The evidence each condition class actually leaves behind.
	refused := challengeObservation{node: node, evidence: Evidence{
		ControlledTargets: []ControlledTargetEvidence{
			{From: node, To: "10.77.0.20:80", Family: "ipv4", Outcome: TargetStateRefused}}}}
	unreachableTarget := challengeObservation{node: node, evidence: Evidence{
		ControlledTargets: []ControlledTargetEvidence{
			{From: node, To: "10.77.3.20:80", Family: "ipv4", Outcome: FamilyStateUnreachable}}}}
	reachableTarget := challengeObservation{node: node, evidence: Evidence{
		ControlledTargets: []ControlledTargetEvidence{
			{From: node, To: "10.77.0.20:80", Family: "ipv4", Reachable: true, Outcome: FamilyStateReachable}}}}
	blockedPort := challengeObservation{node: node, evidence: Evidence{
		PacketDrops: []PacketDropEvidence{
			{Node: "target", Protocol: "tcp", Port: 80, Direction: DirectionInbound, Packets: 3}}}}
	uncountedFilter := challengeObservation{node: node, evidence: Evidence{
		PacketDrops: []PacketDropEvidence{
			{Node: "target", Protocol: "tcp", Port: 80, Direction: DirectionInbound, Packets: 0}}}}
	expired := challengeObservation{node: node, evidence: Evidence{
		TLS: []TLSEvidence{{Node: "target", Service: "tls-target", CertificateMode: TLSCertificateExpired,
			CertificatePresented: true, Result: "client_rejected_certificate", Count: 1}}}}
	mismatch := challengeObservation{node: node, evidence: Evidence{
		TLS: []TLSEvidence{{Node: "target", Service: "tls-target", CertificateMode: TLSCertificateHostnameMismatch,
			RequestedServer: "secure-target.test", CertificateDNS: []string{"somewhere-else.test"},
			CertificatePresented: true, Result: "client_rejected_certificate", Count: 1}}}}
	goodHandshake := challengeObservation{node: node, evidence: Evidence{
		TLS: []TLSEvidence{{Node: "target", Service: "tls-target", CertificatePresented: true,
			Result: "passed", Count: 1}}}}
	ipv4Down := challengeObservation{node: node, evidence: Evidence{
		FamilyReachability: []FamilyReachabilityEvidence{
			{Node: node, Family: "ipv4", State: FamilyStateUnreachable},
			{Node: node, Family: "ipv6", State: FamilyStateReachable}}}}
	ipv6Down := challengeObservation{node: node, evidence: Evidence{
		FamilyReachability: []FamilyReachabilityEvidence{
			{Node: node, Family: "ipv4", State: FamilyStateReachable},
			{Node: node, Family: "ipv6", State: FamilyStateUnreachable}}}}
	familyElsewhere := challengeObservation{node: node, evidence: Evidence{
		FamilyReachability: []FamilyReachabilityEvidence{
			{Node: "other", Family: "ipv4", State: FamilyStateUnreachable}}}}
	noDefault := challengeObservation{node: node, evidence: Evidence{
		RouteTables: []RouteTableEvidence{{Node: node, Family: "ipv4",
			Routes: []KernelRoute{{Destination: "10.77.0.0/24", Segment: "client-lan"}}}}}}
	haveDefault := challengeObservation{node: node, evidence: Evidence{
		RouteTables: []RouteTableEvidence{{Node: node, Family: "ipv4",
			Routes: []KernelRoute{{Destination: "default", Via: "10.77.0.1", Segment: "client-lan"}}}}}}
	dnsFailing := challengeObservation{node: node, truth: ObservedTruth{DNS: "unavailable"}}
	dnsMixed := challengeObservation{node: node, truth: ObservedTruth{DNS: "mixed"}}
	dnsFine := challengeObservation{node: node, truth: ObservedTruth{DNS: "available"}}
	reset := challengeObservation{node: node, truth: ObservedTruth{TCP: "reset"}}
	drops := challengeObservation{node: node, truth: ObservedTruth{Packet: "drops_observed"}}
	shaperIdle := challengeObservation{node: node, truth: ObservedTruth{Packet: "impairment_active"}}
	nothing := challengeObservation{node: node}

	for _, tt := range []struct {
		name      string
		signature challengeSignature
		fires     []challengeObservation
		quiet     []challengeObservation
	}{
		{"dns failing", signatureDNSFailing,
			[]challengeObservation{dnsFailing, dnsMixed},
			[]challengeObservation{dnsFine, nothing, unreachableTarget}},
		{"tcp reset", signatureTCPReset,
			[]challengeObservation{reset},
			[]challengeObservation{nothing, refused, blockedPort}},
		{"expired certificate", signatureExpiredCertificate,
			[]challengeObservation{expired},
			// The name mismatch next door is the confusion this separation exists
			// for: same handshake, same rejection, different fix.
			[]challengeObservation{mismatch, goodHandshake, nothing}},
		{"mismatched certificate", signatureMismatchedCertificate,
			[]challengeObservation{mismatch},
			[]challengeObservation{expired, goodHandshake, nothing}},
		{"ipv4 unreachable", signatureFamilyUnreachable("ipv4"),
			[]challengeObservation{ipv4Down},
			// A different family being down, and the same family being down at a
			// node this challenge is not played from.
			[]challengeObservation{ipv6Down, familyElsewhere, nothing}},
		{"ipv6 unreachable", signatureFamilyUnreachable("ipv6"),
			[]challengeObservation{ipv6Down},
			[]challengeObservation{ipv4Down, familyElsewhere, nothing}},
		{"any family unreachable", signatureFamilyUnreachable(""),
			[]challengeObservation{ipv4Down, ipv6Down},
			[]challengeObservation{familyElsewhere, nothing}},
		{"controlled target refused", signatureControlledTargetRefused,
			[]challengeObservation{refused},
			// A target that timed out is the fault this one is most often confused
			// with, and a refusal is something only a reachable host can send.
			[]challengeObservation{unreachableTarget, reachableTarget, nothing}},
		{"controlled target unreachable", signatureControlledTargetUnreachable,
			[]challengeObservation{unreachableTarget},
			[]challengeObservation{refused, reachableTarget, nothing}},
		{"path shaper dropping", signaturePacketDrops,
			[]challengeObservation{drops},
			// A shaper that was installed and matched nothing impaired nobody.
			[]challengeObservation{shaperIdle, nothing}},
		{"inbound tcp dropped", signatureInboundTCPDropped,
			[]challengeObservation{blockedPort},
			// A rule with a zero counter is a fault that was injected, not one
			// that took effect.
			[]challengeObservation{uncountedFilter, refused, nothing}},
		{"no default route", signatureNoDefaultRoute,
			[]challengeObservation{noDefault},
			[]challengeObservation{haveDefault, nothing}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i, obs := range tt.fires {
				reason, present := tt.signature(obs)
				if !present {
					t.Errorf("positive case %d did not satisfy the signature", i)
				}
				if present && reason == "" {
					t.Errorf("positive case %d fired with no reason, so a rejection would say nothing", i)
				}
			}
			for i, obs := range tt.quiet {
				if _, present := tt.signature(obs); present {
					t.Errorf("negative case %d satisfied the signature it must exclude", i)
				}
			}
		})
	}
}

// The playable set is derived, not listed. If this ever has to be updated by
// hand alongside a condition, the derivation has been broken.
func TestPlayableAnswersAreDerivedFromTheConditions(t *testing.T) {
	answers := challengePlayableAnswers()
	if len(answers) == 0 {
		t.Fatal("no playable answers")
	}
	for _, answer := range answers {
		if answer == AnswerHealthy {
			t.Error("healthy is not a fault a challenge is set on")
		}
		if len(challengeConditionsFor(answer)) == 0 {
			t.Errorf("%s is playable but no condition establishes it", answer)
		}
		if _, ok := ChallengeAnswerByID(string(answer)); !ok {
			t.Errorf("%s is playable but is not on the answer menu, so nobody could submit it", answer)
		}
	}
	// Every fault condition's answer has to appear, or the derivation is
	// dropping something the generator would then never reach.
	for _, condition := range challengeConditions {
		if condition.mutation == "" {
			continue
		}
		found := false
		for _, answer := range answers {
			found = found || answer == condition.answer
		}
		if !found {
			t.Errorf("%s establishes %s, which the playable set does not contain", condition.mutation, condition.answer)
		}
	}
}
