package simulation

import (
	"net/http"
	"slices"
	"testing"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// oracleReport is a finished run on a stable path: one client node, one netdoc
// process, and whatever independent evidence the caller attaches. The two
// halves are supplied separately on purpose — no fixture here derives evidence
// from a diagnosis or a diagnosis from evidence.
func oracleReport(diagnosis *Diagnosis, evidence Evidence) *Report {
	return &Report{Cleanup: CleanupInfo{Done: true}, Topology: []NodeInfo{{Name: "client", Role: "client"}},
		Tests:    []TestOutcome{{Name: "netdoc", Node: "client", ProcessOutcome: ProcessExited, Diagnosis: diagnosis}},
		Evidence: evidence, Suggestions: []Suggestion{}}
}

func oracleDiagnosis(checks ...DiagnosisCheck) *Diagnosis { return &Diagnosis{Checks: checks} }

// Independent evidence fixtures. Each one is what a node holder would have
// written down; none of them can be produced by reading netdoc's report.
func expiredTLSEvidence() Evidence {
	return Evidence{TLS: []TLSEvidence{{Node: "target", Service: "web", CertificateMode: TLSCertificateExpired,
		CertificatePresented: true, Result: "client_rejected_certificate", Count: 1}}}
}

func refusedProxyEvidence() Evidence {
	return Evidence{SOCKSRequests: []SOCKSEvidence{{Node: "proxy", Service: "socks", Event: "connect",
		Destination: proxyCONNECTTarget, Port: 443, Result: "connection_refused", Count: 1}}}
}

func droppedQUICEvidence() Evidence {
	return Evidence{PacketDrops: []PacketDropEvidence{{Node: "target", Protocol: "udp", Port: 443,
		Direction: DirectionInbound, Packets: 4}}}
}

func unrecognizedConditions(findings []HuntCaseFinding) []string {
	var out []string
	for _, finding := range findings {
		if finding.Category == FindingFalseNegative && finding.Code == "unrecognized_network_condition" {
			out = append(out, finding.Expected)
		}
	}
	return out
}

func analyzedConditions(t *testing.T, manifest GeneratedCaseManifest, report *Report, truth ObservedTruth) []string {
	t.Helper()
	return unrecognizedConditions(analyzeHuntCase(manifest, report, truth))
}

// TestHuntOracleAccusesOnlyUnrecognizedObservedConditions is the core contract:
// independently observed truth implies a semantic condition, and a diagnosis
// that does not communicate it is a false negative. Each case pairs one
// evidence fixture with two diagnoses that differ in nothing but whether they
// say what the network is doing.
func TestHuntOracleAccusesOnlyUnrecognizedObservedConditions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		evidence   Evidence
		truth      ObservedTruth
		condition  NetworkCondition
		missed     *Diagnosis
		recognized *Diagnosis
	}{
		{
			name: "TLS certificate expired", evidence: expiredTLSEvidence(), condition: ConditionTLSCertificateExpired,
			// A bare handshake failure is deliberately not equivalent: the user
			// is sent to look for a MITM proxy instead of at the clock.
			missed:     oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "FAIL", Cause: diagnostic.TLSCauseHandshake}),
			recognized: oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "FAIL", Cause: diagnostic.TLSCauseCertificateExpired}),
		},
		{
			name: "proxy refused its destination", evidence: refusedProxyEvidence(), condition: ConditionProxyDestinationRefused,
			// Being unable to reach the proxy is a different fault from the
			// proxy answering and declining, so it does not recognize this one.
			missed:     oracleDiagnosis(DiagnosisCheck{ID: "proxy_connect", Status: "FAIL", Cause: diagnostic.ProxyCauseUnreachable}),
			recognized: oracleDiagnosis(DiagnosisCheck{ID: "proxy_connect", Status: "FAIL", Cause: diagnostic.ProxyCauseDestinationUnreachable}),
		},
		{
			name: "QUIC UDP/443 dropped", evidence: droppedQUICEvidence(), condition: ConditionQUICUDP443Blocked,
			missed: oracleDiagnosis(DiagnosisCheck{ID: string(diagnostic.ProbeQUIC), Status: "PASS"}),
			recognized: oracleDiagnosis(DiagnosisCheck{ID: string(diagnostic.ProbeQUIC), Status: "FAIL",
				Cause: diagnostic.QUICCauseTimeout}),
		},
		{
			name: "IPv4 internet reachability lost", truth: ObservedTruth{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable},
			condition: ConditionIPv4InternetUnreachable,
			missed: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "PASS",
				Families: &DiagnosisFamilies{IPv4: FamilyStateReachable, IPv6: FamilyStateReachable}}),
			recognized: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "WARN",
				Families: &DiagnosisFamilies{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable}}),
		},
		{
			name: "IPv6 internet reachability lost", truth: ObservedTruth{IPv4: FamilyStateReachable, IPv6: FamilyStateUnreachable},
			condition: ConditionIPv6InternetUnreachable,
			missed: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "PASS",
				Families: &DiagnosisFamilies{IPv4: FamilyStateReachable, IPv6: FamilyStateReachable}}),
			recognized: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "WARN",
				Families: &DiagnosisFamilies{IPv4: FamilyStateReachable, IPv6: FamilyStateUnreachable}}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missed := analyzedConditions(t, huntManifest(), oracleReport(tc.missed, tc.evidence), tc.truth)
			if !slices.Equal(missed, []string{string(tc.condition)}) {
				t.Fatalf("unrecognized observed condition = %v, want %q", missed, tc.condition)
			}
			seen := analyzedConditions(t, huntManifest(), oracleReport(tc.recognized, tc.evidence), tc.truth)
			if len(seen) != 0 {
				t.Fatalf("recognized condition still accused: %v", seen)
			}
		})
	}
}

// TestHuntOracleAcceptsEquivalentRecognition proves the oracle asks whether the
// condition was communicated, not which row communicated it. Both alternatives
// are things netdoc genuinely produces.
func TestHuntOracleAcceptsEquivalentRecognition(t *testing.T) {
	for _, tc := range []struct {
		name      string
		evidence  Evidence
		truth     ObservedTruth
		diagnosis *Diagnosis
	}{
		{
			// The expiry cause on a different row than the one the current
			// probe graph happens to put it on.
			name: "expiry named by a different row", evidence: expiredTLSEvidence(),
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "https", Status: "FAIL", Cause: diagnostic.TLSCauseCertificateExpired}),
		},
		{
			// The family stated as a cause rather than as a structured
			// address_families verdict.
			name:  "family named by cause rather than the families block",
			truth: ObservedTruth{IPv4: FamilyStateReachable, IPv6: FamilyStateUnreachable},
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "WARN",
				Cause: diagnostic.FamilyCauseIPv6Unreachable}),
		},
		{
			// A QUIC block that shows up as a handshake failure instead of the
			// timeout the control scenario records.
			name: "QUIC block reported as a handshake failure", evidence: droppedQUICEvidence(),
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: string(diagnostic.ProbeQUIC), Status: "FAIL",
				Cause: diagnostic.QUICCauseHandshake}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := analyzedConditions(t, huntManifest(), oracleReport(tc.diagnosis, tc.evidence), tc.truth); len(got) != 0 {
				t.Fatalf("equivalent recognition accused anyway: %v", got)
			}
		})
	}
}

// TestHuntOracleRejectsUnrelatedFailures is the guard against a permissive
// oracle: netdoc failing loudly about something else is not recognition. The
// QUIC case is the sharp one, because "timeout" is not a unique cause in
// netdoc's vocabulary and TLS spends it too.
func TestHuntOracleRejectsUnrelatedFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		evidence  Evidence
		truth     ObservedTruth
		diagnosis *Diagnosis
		want      NetworkCondition
	}{
		{
			name: "observed TLS expiry with an unrelated DNS failure", evidence: expiredTLSEvidence(),
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "dns", Status: "FAIL", Cause: diagnostic.DNSCauseTimeout}),
			want:      ConditionTLSCertificateExpired,
		},
		{
			name: "observed QUIC block with an unrelated TLS timeout", evidence: droppedQUICEvidence(),
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "FAIL", Cause: diagnostic.TLSCauseTimeout}),
			want:      ConditionQUICUDP443Blocked,
		},
		{
			name:  "observed IPv4 loss with the other family reported unreachable",
			truth: ObservedTruth{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable},
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "WARN",
				Cause: diagnostic.FamilyCauseIPv6Unreachable}),
			want: ConditionIPv4InternetUnreachable,
		},
		{
			// A cause on a passing row is context, not netdoc raising its hand.
			name: "expiry cause carried by a passing row", evidence: expiredTLSEvidence(),
			diagnosis: oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "PASS", Cause: diagnostic.TLSCauseCertificateExpired}),
			want:      ConditionTLSCertificateExpired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := analyzedConditions(t, huntManifest(), oracleReport(tc.diagnosis, tc.evidence), tc.truth)
			if !slices.Contains(got, string(tc.want)) {
				t.Fatalf("unrelated failure satisfied %q; accusations = %v", tc.want, got)
			}
		})
	}
}

// TestHuntOracleIgnoresTheMutationSchedule is requirement zero: a manifest is
// intent. Every mutation below claims to have been generated and applied, and
// the run recorded no independent evidence that any of them reached the
// network, so nothing may be said about the diagnosis.
func TestHuntOracleIgnoresTheMutationSchedule(t *testing.T) {
	manifest := huntManifest(
		GeneratedMutation{ID: "service.tls_expired", Node: "target", Service: "web"},
		GeneratedMutation{ID: "proxy.connect_refused", Node: "proxy", Service: "socks", TargetPort: 443},
		GeneratedMutation{ID: "quic.udp_443_block", Node: "target", TargetPort: 443},
		GeneratedMutation{ID: "family.ipv4_drop", Node: "gateway", Family: "ipv4"},
	)
	report := oracleReport(oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "PASS",
		Families: &DiagnosisFamilies{IPv4: FamilyStateReachable, IPv6: FamilyStateReachable}}), Evidence{})
	truth := collectObservedTruth(manifest, report)
	if len(truth.ObservedFaults) != 0 {
		t.Fatalf("scheduled mutations were treated as observed: %v", truth.ObservedFaults)
	}
	if got := analyzedConditions(t, manifest, report, truth); len(got) != 0 {
		t.Fatalf("mutation schedule alone produced false-negative accusations: %v", got)
	}
}

// TestHuntOracleRequiresTheFaultToHaveReachedTheWire separates a fault that was
// configured from one that did something. Each fixture is the near miss of a
// real observation: a certificate nobody was shown, a rule that matched no
// packet, a proxy that answered normally.
func TestHuntOracleRequiresTheFaultToHaveReachedTheWire(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence Evidence
	}{
		{"expired certificate never presented", Evidence{TLS: []TLSEvidence{{Node: "target", Service: "web",
			CertificateMode: TLSCertificateExpired, CertificatePresented: false, Result: "no_handshake", Count: 1}}}},
		{"expired certificate the client accepted", Evidence{TLS: []TLSEvidence{{Node: "target", Service: "web",
			CertificateMode: TLSCertificateExpired, CertificatePresented: true, Result: "passed", Count: 1}}}},
		{"service configured but never reached", Evidence{ServiceStates: []ServiceStateEvidence{{Node: "target",
			Service: "web", Type: ServiceTLS, Mode: TLSCertificateExpired}}}},
		{"drop rule that matched no packet", Evidence{PacketDrops: []PacketDropEvidence{{Node: "target",
			Protocol: "udp", Port: 443, Direction: DirectionInbound, Packets: 0}}}},
		{"proxy that connected normally", Evidence{SOCKSRequests: []SOCKSEvidence{{Node: "proxy", Service: "socks",
			Event: "connect", Destination: proxyCONNECTTarget, Port: 443, Result: "connected", Count: 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := oracleReport(oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "PASS"}), tc.evidence)
			if got := observedConditions(report.Evidence, ObservedTruth{}); len(got) != 0 {
				t.Fatalf("unobserved effect established %v", got)
			}
		})
	}
}

// TestHuntEvidenceScopeIsRequiredNotWildcarded pins the one place where two
// different questions share an evidence reader. The oracle asks whether a
// condition happened at all, which is unscoped by design because a network fact
// is not owned by a node. Per-mutation observation asks whether one mutation
// took effect where it said it would, which a manifest that names no place
// cannot answer. Neither missing half may quietly widen into "matches
// anything": a mutation would confirm itself from a fault someone else caused,
// and a record from nowhere would establish a condition nobody observed.
func TestHuntEvidenceScopeIsRequiredNotWildcarded(t *testing.T) {
	// Controls first. Same evidence, fully scoped mutations, all observed —
	// without these the cases below would pass on a reader that always says no.
	for _, tc := range []struct {
		name     string
		mutation GeneratedMutation
		evidence Evidence
	}{
		{"expired TLS", GeneratedMutation{ID: "service.tls_expired", Node: "target", Service: "web"}, expiredTLSEvidence()},
		{"proxy refusal", GeneratedMutation{ID: "proxy.connect_refused", Node: "proxy", Service: "socks", TargetPort: 443}, refusedProxyEvidence()},
		{"QUIC block", GeneratedMutation{ID: "quic.udp_443_block", Node: "target", TargetPort: 443}, droppedQUICEvidence()},
	} {
		t.Run("scoped "+tc.name, func(t *testing.T) {
			if !mutationObserved(tc.mutation, oracleReport(nil, tc.evidence), ObservedTruth{}) {
				t.Fatal("a fully scoped mutation was not observed in its own evidence")
			}
		})
	}

	// A hand-built manifest that forgot to say where its effect belongs. The
	// evidence is the genuine article in every case; only the scope is missing.
	for _, tc := range []struct {
		name     string
		mutation GeneratedMutation
		evidence Evidence
	}{
		{"expired TLS naming no node", GeneratedMutation{ID: "service.tls_expired", Service: "web"}, expiredTLSEvidence()},
		{"expired TLS naming no service", GeneratedMutation{ID: "service.tls_expired", Node: "target"}, expiredTLSEvidence()},
		{"expired TLS naming nothing", GeneratedMutation{ID: "service.tls_expired"}, expiredTLSEvidence()},
		{"proxy refusal naming no node", GeneratedMutation{ID: "proxy.connect_refused", Service: "socks", TargetPort: 443}, refusedProxyEvidence()},
		{"proxy refusal naming no service", GeneratedMutation{ID: "proxy.connect_refused", Node: "proxy", TargetPort: 443}, refusedProxyEvidence()},
		{"proxy refusal naming no port", GeneratedMutation{ID: "proxy.connect_refused", Node: "proxy", Service: "socks"}, refusedProxyEvidence()},
		{"QUIC block naming no node", GeneratedMutation{ID: "quic.udp_443_block", TargetPort: 443}, droppedQUICEvidence()},
		{"QUIC block naming no port", GeneratedMutation{ID: "quic.udp_443_block", Node: "target"}, droppedQUICEvidence()},
		{"QUIC block naming nothing", GeneratedMutation{ID: "quic.udp_443_block"}, droppedQUICEvidence()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if mutationObserved(tc.mutation, oracleReport(nil, tc.evidence), ObservedTruth{}) {
				t.Fatal("a mutation that never said where its effect belongs was counted as observed")
			}
			// The oracle's own question is unchanged by any of this: it never
			// read the manifest, so an incomplete manifest cannot silence it.
			if got := observedConditions(tc.evidence, ObservedTruth{}); len(got) != 1 {
				t.Fatalf("independently observed conditions = %v, want exactly one", got)
			}
		})
	}

	// The case where the two blanks would have met: a manifest that names no
	// service and a record that names none either. Exact comparison alone would
	// call that a match, which is why the scope is required rather than merely
	// compared.
	t.Run("blank scope must not meet blank evidence", func(t *testing.T) {
		anonymous := expiredTLSEvidence()
		anonymous.TLS[0].Service = ""
		if mutationObserved(GeneratedMutation{ID: "service.tls_expired", Node: "target"},
			oracleReport(nil, anonymous), ObservedTruth{}) {
			t.Fatal("a mutation naming no service was confirmed by evidence naming no service")
		}
	})

	// Evidence that does not say which node produced it. A holder stamps its own
	// name on every record it writes, so these are hand-built; a record from
	// nowhere must not establish a condition merely because no scope disagrees
	// with it.
	unstampedTLS, unstampedProxy, unstampedQUIC := expiredTLSEvidence(), refusedProxyEvidence(), droppedQUICEvidence()
	unstampedTLS.TLS[0].Node = ""
	unstampedProxy.SOCKSRequests[0].Node = ""
	unstampedQUIC.PacketDrops[0].Node = ""
	for _, tc := range []struct {
		name     string
		evidence Evidence
	}{
		{"handshake from no node", unstampedTLS},
		{"CONNECT refusal from no node", unstampedProxy},
		{"packet drop from no node", unstampedQUIC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := observedConditions(tc.evidence, ObservedTruth{}); len(got) != 0 {
				t.Fatalf("unattributable evidence established %v", got)
			}
		})
	}
}

// TestHuntOracleKeepsFamilyStatesDistinct is the unavailable-versus-unreachable
// contract at the semantic layer: only a family that was dialed and did not
// answer expects recognition, and neither family's state may leak into the
// other's expectation.
func TestHuntOracleKeepsFamilyStatesDistinct(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ipv4, ipv6 string
		want       []NetworkCondition
	}{
		{"both reachable", FamilyStateReachable, FamilyStateReachable, nil},
		{"IPv4 unreachable alone", FamilyStateUnreachable, FamilyStateReachable, []NetworkCondition{ConditionIPv4InternetUnreachable}},
		{"IPv6 unreachable alone", FamilyStateReachable, FamilyStateUnreachable, []NetworkCondition{ConditionIPv6InternetUnreachable}},
		{"both unreachable", FamilyStateUnreachable, FamilyStateUnreachable,
			[]NetworkCondition{ConditionIPv4InternetUnreachable, ConditionIPv6InternetUnreachable}},
		// A family the node never had an address in was not tested, and an
		// absent measurement is not a network fact either. Neither may become
		// an expectation, and neither may satisfy the other family's.
		{"IPv6 unavailable", FamilyStateReachable, FamilyStateUnavailable, nil},
		{"IPv4 unavailable", FamilyStateUnavailable, FamilyStateReachable, nil},
		{"IPv4 unavailable while IPv6 is lost", FamilyStateUnavailable, FamilyStateUnreachable,
			[]NetworkCondition{ConditionIPv6InternetUnreachable}},
		{"unmeasured", "unknown", "unknown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observedConditions(Evidence{}, ObservedTruth{IPv4: tc.ipv4, IPv6: tc.ipv6})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("observed conditions = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestHuntOracleHoldsItsDirection pins the dependency arrows. The observed side
// cannot reach a diagnosis at all — it takes Evidence, so the compiler holds
// that half — and this covers the two the compiler cannot: recognition must not
// move with the evidence, and the reconciler, which does see the whole report,
// must let the diagnosis change only the accusation.
func TestHuntOracleHoldsItsDirection(t *testing.T) {
	truth := ObservedTruth{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable}
	blind := oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "PASS",
		Families: &DiagnosisFamilies{IPv4: FamilyStateReachable, IPv6: FamilyStateReachable}})
	naming := oracleDiagnosis(DiagnosisCheck{ID: "internet_tcp", Status: "WARN",
		Families: &DiagnosisFamilies{IPv4: FamilyStateUnreachable, IPv6: FamilyStateReachable}})
	// Recognition is a function of the diagnosis alone: the same diagnosis read
	// beside contradicting evidence answers the same way.
	withTLS := recognizedConditions(naming)
	if !slices.Equal(withTLS, recognizedConditions(naming)) ||
		!slices.Equal(withTLS, []NetworkCondition{ConditionIPv4InternetUnreachable}) {
		t.Fatalf("recognition = %v, want only the reported family loss", withTLS)
	}
	// Same evidence, same truth, two diagnoses: what was observed is identical
	// either way, and only the finding differs.
	evidence := expiredTLSEvidence()
	if !slices.Equal(observedConditions(evidence, truth),
		[]NetworkCondition{ConditionIPv4InternetUnreachable, ConditionTLSCertificateExpired}) {
		t.Fatalf("observed conditions = %v", observedConditions(evidence, truth))
	}
	missed := unrecognizedConditions(unrecognizedConditionFindings(oracleReport(blind, evidence), truth))
	seen := unrecognizedConditions(unrecognizedConditionFindings(oracleReport(naming, evidence), truth))
	if !slices.Equal(missed, []string{string(ConditionIPv4InternetUnreachable), string(ConditionTLSCertificateExpired)}) {
		t.Fatalf("blind diagnosis accusations = %v", missed)
	}
	if !slices.Equal(seen, []string{string(ConditionTLSCertificateExpired)}) {
		t.Fatalf("family-naming diagnosis accusations = %v", seen)
	}
}

// TestHuntOracleStaysQuietOnUnstablePaths keeps the final-state comparison out
// of runs where the simulator's last sample and netdoc's last probe could
// legitimately describe different networks.
func TestHuntOracleStaysQuietOnUnstablePaths(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Report)
	}{
		{"netem", func(report *Report) { report.Faults = []FaultInfo{{Type: FaultNetem, LossPercent: 20}} }},
		{"timed netem", func(report *Report) {
			report.Timeline = []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledNetem, LossPercent: 100}, Result: EventApplied}}
		}},
		{"transient link", func(report *Report) {
			report.Timeline = []FaultEventEvidence{{Event: TimedEvent{Type: FaultScheduledLink, State: LinkStateDown}, Result: EventApplied}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := oracleReport(oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "PASS"}), expiredTLSEvidence())
			tc.change(report)
			if got := analyzedConditions(t, huntManifest(), report, ObservedTruth{}); len(got) != 0 {
				t.Fatalf("unstable path produced accusations: %v", got)
			}
		})
	}
}

// TestHuntOracleWithoutAClientDiagnosis covers the runs that have truth but
// nothing to reconcile it against.
func TestHuntOracleWithoutAClientDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Report)
	}{
		{"netdoc produced no diagnosis", func(report *Report) { report.Tests[0].Diagnosis = nil }},
		{"no client node", func(report *Report) { report.Topology[0].Role = "router" }},
		{"two client nodes", func(report *Report) {
			report.Topology = append(report.Topology, NodeInfo{Name: "second", Role: "client"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := oracleReport(oracleDiagnosis(DiagnosisCheck{ID: "tls", Status: "PASS"}), expiredTLSEvidence())
			tc.change(report)
			if got := analyzedConditions(t, huntManifest(), report, ObservedTruth{}); len(got) != 0 {
				t.Fatalf("reconciliation ran without a client diagnosis: %v", got)
			}
		})
	}
}

// TestHuntOracleClaimsNothingAboutUndiagnosedFaults documents the deliberate
// gaps. Both faults are independently observed, and neither implies a condition
// Network Doctor claims to report: an HTTP error status is a working service
// answering, and DoH failing while DoT still resolves is encrypted DNS
// working. Turning either into an expectation would invent a contract the
// probes never made, which is what the `http-error` and encrypted-DNS controls
// exist to pin down.
func TestHuntOracleClaimsNothingAboutUndiagnosedFaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence Evidence
	}{
		{"HTTP 503", Evidence{ServiceReplies: []ServiceReplyEvidence{{Node: "target", Type: ServiceHTTP,
			Port: 80, Status: http.StatusServiceUnavailable, Result: replyResponded, Count: 1}}}},
		{"invalid DoH while DoT resolves", Evidence{ServiceReplies: []ServiceReplyEvidence{{Node: "resolver",
			Service: "doh", Type: ServiceEncryptedDNS, Result: DoHResponseInvalid, Count: 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := oracleReport(oracleDiagnosis(DiagnosisCheck{ID: "http", Status: "PASS"},
				DiagnosisCheck{ID: "dns_encrypted", Status: "PASS"}), tc.evidence)
			if got := observedConditions(report.Evidence, ObservedTruth{}); len(got) != 0 {
				t.Fatalf("a fault netdoc does not diagnose became an expectation: %v", got)
			}
		})
	}
}
