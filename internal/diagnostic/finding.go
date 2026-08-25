package diagnostic

import "slices"

// DiagnosisID is the stable machine-readable identity of one diagnostic
// conclusion: what netdoc decided is wrong, independent of the sentence it
// prints. Prose gets reworded; an ID does not, so scripts branch on this and
// never on English.
//
// These are Network Doctor's own conclusions about a network. They are not the
// simulator's hunt findings, which are defects discovered in Network Doctor
// itself and live in internal/simulation under Hunt-prefixed names.
//
// An ID names a condition netdoc can actually prove from probe evidence. There
// is deliberately no ID for a guess: a run that establishes nothing specific
// reports its broad verdict and no finding at all, rather than being pushed
// into an identity it did not earn.
type DiagnosisID string

// The diagnosis vocabulary, grouped by the rung the conclusion is about.
// Values are lower_snake_case and permanent; renaming one is a breaking
// change to the JSON report. docs/reference.md tabulates the whole set, and
// TestDiagnosisIDsAreDocumented keeps that table honest.
const (
	// Link and path.
	DiagnosisNoUsableInterface    DiagnosisID = "no_usable_interface"
	DiagnosisCaptivePortal        DiagnosisID = "captive_portal"
	DiagnosisOffline              DiagnosisID = "offline"
	DiagnosisDirectEgressBlocked  DiagnosisID = "direct_egress_blocked"
	DiagnosisDirectEgressDegraded DiagnosisID = "direct_egress_degraded"
	DiagnosisProxyOnlyNetwork     DiagnosisID = "proxy_only_network"
	DiagnosisLocalEgressFailure   DiagnosisID = "local_egress_failure"
	DiagnosisProbablePathMTU      DiagnosisID = "probable_path_mtu_problem"

	// Name resolution.
	DiagnosisSystemDNSFailure        DiagnosisID = "system_dns_failure"
	DiagnosisDNSNameNotFound         DiagnosisID = "dns_name_not_found"
	DiagnosisDNSFailure              DiagnosisID = "dns_failure"
	DiagnosisDNSDisagreement         DiagnosisID = "dns_disagreement"
	DiagnosisEncryptedDNSUnavailable DiagnosisID = "encrypted_dns_unavailable"

	// Paths that run beside the direct one.
	DiagnosisQUICUnavailable DiagnosisID = "quic_unavailable"
	DiagnosisProxyFailure    DiagnosisID = "proxy_failure"

	// Reaching the endpoint under test.
	DiagnosisTCPConnectionRefused   DiagnosisID = "tcp_connection_refused"
	DiagnosisTargetUnreachable      DiagnosisID = "target_unreachable"
	DiagnosisLocalDeviceUnreachable DiagnosisID = "local_device_unreachable"
	DiagnosisReachabilityUntested   DiagnosisID = "reachability_untested"

	// TLS, which keeps the fine-grained causes the handshake already
	// classified rather than flattening them into one service failure.
	DiagnosisTLSCertificateExpired     DiagnosisID = "tls_certificate_expired"
	DiagnosisTLSCertificateNotYetValid DiagnosisID = "tls_certificate_not_yet_valid"
	DiagnosisTLSHostnameMismatch       DiagnosisID = "tls_hostname_mismatch"
	DiagnosisTLSUntrustedIssuer        DiagnosisID = "tls_untrusted_issuer"
	DiagnosisTLSClockSkew              DiagnosisID = "tls_clock_skew"
	DiagnosisTLSTimeout                DiagnosisID = "tls_timeout"
	DiagnosisTLSConnectionClosed       DiagnosisID = "tls_connection_closed"
	DiagnosisTLSTCPUnreachable         DiagnosisID = "tls_tcp_unreachable"
	DiagnosisTLSHandshakeFailure       DiagnosisID = "tls_handshake_failure"

	// The application on top of a working connection.
	DiagnosisHTTPSNoResponse      DiagnosisID = "https_no_response"
	DiagnosisHTTPNoResponse       DiagnosisID = "http_no_response"
	DiagnosisServiceBannerFailure DiagnosisID = "service_banner_failure"
	DiagnosisServiceBannerMissing DiagnosisID = "service_banner_missing"

	// A selection of checks whose failure no other case is about. These are
	// what --check and --skip can leave behind: a run with the rungs that
	// would have explained the failure taken out of it.
	DiagnosisSelectedDNSCheckFailed     DiagnosisID = "selected_dns_check_failed"
	DiagnosisSelectedServiceCheckFailed DiagnosisID = "selected_service_check_failed"
	DiagnosisSelectedNetworkCheckFailed DiagnosisID = "selected_network_check_failed"
)

// DiagnosisFinding is one specific thing a run proved wrong, with everything a
// caller needs to act on it without diagnosing the run a second time: the
// stable identity, the sentence, the broad class that identity falls into, the
// row whose remediation and evidence belong to it, and the rows the conclusion
// rests on.
//
// It carries no remedy text of its own. Fix hints live on the probe rows,
// which is the single place they are written, and Focus is the row to take
// this finding's hint from.
type DiagnosisFinding struct {
	ID DiagnosisID
	// Verdict is this finding's broad class, from the same vocabulary as
	// Diagnosis.Verdict. It doubles as the finding's severity: ok and degraded
	// are impairments, dns/network/service are failures.
	Verdict string
	Summary string
	// Focus is the probe row the sentence is about, empty when the conclusion
	// is about no single row.
	Focus ProbeID
	// Evidence names the rows the conclusion was drawn from, Focus first, and
	// only rows that were part of the run. It is references, never copies:
	// what each row observed stays on the ProbeResult.
	Evidence []ProbeID
}

// Diagnosis is the one authoritative interpretation of a finished run: what
// the run's overall state is, and which specific conclusions it supports.
//
// The run's broad status and a specific finding are deliberately separate. A
// healthy run, a run still in progress, a run with nothing selected, and a run
// that is degraded in a way no single case is about all have a verdict and a
// sentence with no finding under them, because forcing an identity onto them
// would be inventing one.
//
// Findings is a slice because a run can hold more than one conclusion. Today
// the pass reports the single most specific one it reaches, which is what
// keeps the summary, the verdict and the blamed row from ever disagreeing.
type Diagnosis struct {
	// Verdict is the run's broad class, the stable vocabulary the JSON report
	// has always published. See the Verdict constants.
	Verdict string
	// Summary is the plain-English headline, and the sentence the primary
	// finding is about when there is one.
	Summary string
	// Blamed is the row a caller should put a cursor on: the row the diagnosis
	// names, or the first failed row when it names none. Unlike a finding's
	// Focus it is a presentation answer, so it falls back rather than staying
	// empty. Empty when nothing failed.
	Blamed   ProbeID
	Findings []DiagnosisFinding
}

// Focus is the row the diagnosis itself blames, empty when it blames none.
// Unlike Blamed this never falls back to a row the diagnosis is not about.
func (d Diagnosis) Focus() ProbeID {
	if len(d.Findings) == 0 {
		return ""
	}
	return d.Findings[0].Focus
}

// evidenceRows keeps a finding's evidence honest: it drops rows that were not
// part of this run, because naming a check nobody made as support for a
// conclusion is a claim about evidence that does not exist. Order is the order
// the caller listed, focus first, with duplicates removed.
func evidenceRows(res map[ProbeID]ProbeResult, rows []ProbeID) []ProbeID {
	out := make([]ProbeID, 0, len(rows))
	for _, id := range rows {
		if id == "" || slices.Contains(out, id) {
			continue
		}
		if _, ran := res[id]; !ran {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// tlsDiagnosisID turns the cause the TLS probe already classified into the
// diagnosis identity. The prose for a failed handshake is deliberately hedged,
// because netdoc cannot always tell a bad certificate from a MITM proxy from a
// wrong clock; the cause, where the handshake produced one, can, and the
// identity is where that precision belongs. An unclassified failure keeps the
// generic handshake identity rather than being assigned a specific one.
func tlsDiagnosisID(cause string) DiagnosisID {
	switch cause {
	case TLSCauseCertificateExpired:
		return DiagnosisTLSCertificateExpired
	case TLSCauseCertificateNotYet:
		return DiagnosisTLSCertificateNotYetValid
	case TLSCauseHostnameMismatch:
		return DiagnosisTLSHostnameMismatch
	case TLSCauseUntrustedIssuer:
		return DiagnosisTLSUntrustedIssuer
	case TLSCauseTimeout:
		return DiagnosisTLSTimeout
	case TLSCauseConnectionClosed:
		return DiagnosisTLSConnectionClosed
	case TLSCauseTCPUnreachable:
		return DiagnosisTLSTCPUnreachable
	}
	return DiagnosisTLSHandshakeFailure
}
