package diagnostic

import (
	"net"
	"strconv"
)

// Diagnose computes the plain-English verdict from current-generation native
// probe state only (tool output never feeds in). First-fail ordering + combination
// rules. Returns "Running diagnostics…" until every probe in order has a result.
// A completed run always returns a verdict.
func Diagnose(t *Target, order []ProbeID, res map[ProbeID]ProbeResult) string {
	degraded := false
	for _, id := range order {
		r, ok := res[id]
		if !ok {
			return "Running diagnostics…"
		}
		degraded = degraded || r.Status == StatusWarn
	}
	pass := func(id ProbeID) bool { return res[id].Status == StatusPass }
	fail := func(id ProbeID) bool { return res[id].Status == StatusFail }
	warn := func(id ProbeID) bool { return res[id].Status == StatusWarn }
	has := func(id ProbeID) bool { _, ok := res[id]; return ok }
	// directOK means direct egress genuinely worked: a Pass, or a Warn the
	// probe produced itself. A Warn planted by DowngradeEgress doesn't count —
	// that's a Fail wearing a nicer hat.
	directOK := func() bool {
		r := res[ProbeInternet]
		return functional(r.Status) && !r.downgraded
	}

	if fail(ProbeIface) {
		return "No usable network interface — the link is down."
	}

	prx := has(ProbeProxy) && functional(res[ProbeProxy].Status)
	prxDown := has(ProbeProxy) && fail(ProbeProxy)

	// Generic mode (no target): the verdict is a truth table over egress, DNS,
	// and proxy state. Cases are ordered most-specific first because several
	// overlap — reordering them changes answers, so don't.
	if t == nil {
		ip, dn := pass(ProbeInternet), pass(ProbeDNS)
		switch {
		case ip && dn && prxDown:
			return "Online directly — but the configured environment proxy check failed, so apps that honor HTTP(S)_PROXY will fail (see the proxy row)."
		case ip && dn:
			return "Online — direct TCP egress and DNS both work."
		case warn(ProbeInternet) && res[ProbeInternet].downgraded && dn && prx:
			return "Online via the environment proxy — direct egress is blocked (proxy-only network)."
		case warn(ProbeInternet) && dn:
			return "Online but degraded — direct egress is impaired (see the ! row for details)."
		case warn(ProbeInternet) && !dn:
			return "Internet egress works (degraded) but DNS resolution is failing."
		case ip && !dn:
			return "Internet egress works but DNS resolution is failing."
		case !ip && dn:
			return "DNS resolves but there's no direct TCP egress (proxy-only or filtered network?)."
		default:
			return "Offline — neither direct egress nor DNS is working."
		}
	}

	host := t.Host
	hp := net.JoinHostPort(host, strconv.Itoa(t.Port)) // brackets IPv6 literals

	// Targeted mode: walk up the protocol stack (DNS → TCP → TLS → HTTP →
	// banner) and report the first rung that broke — everything above it
	// failing is implied, everything below it passing is context.
	switch {
	case fail(ProbeDNS):
		v := "Cannot resolve " + host + " — DNS failure."
		if directOK() {
			v += " (The general internet is reachable.)"
		}
		return v
	case fail(ProbeTargetTCP):
		if directOK() {
			return hp + " is unreachable though DNS and the general internet work — remote port closed, firewall, or VPN routing."
		}
		if prx {
			return hp + " is unreachable directly, but the environment proxy has egress — proxy-only network; route traffic through the proxy."
		}
		return host + " resolves but neither it nor the general internet is reachable — local egress problem."
	case has(ProbeTLS) && fail(ProbeTLS):
		return "TCP reaches " + hp + " but the TLS handshake fails — bad/expired cert, clock skew, or MITM proxy."
	case has(ProbeHTTPS) && fail(ProbeHTTPS):
		return "TLS is fine but no HTTPS response from " + hp + " — application-layer or proxy block."
	case has(ProbeHTTP) && fail(ProbeHTTP):
		if t.Proto == ProtoTLSHTTP {
			return "HTTPS works but no HTTP response from " + net.JoinHostPort(host, "80") + " — the redirect/plain-HTTP endpoint may be blocked."
		}
		return "No HTTP response from " + hp + " — application-layer or proxy block."
	case (has(ProbeSSH) && fail(ProbeSSH)) || (has(ProbeSMTP) && fail(ProbeSMTP)):
		return hp + " accepts TCP but the service banner check failed."
	case (has(ProbeSSH) && warn(ProbeSSH)) || (has(ProbeSMTP) && warn(ProbeSMTP)):
		return hp + " accepts TCP but sent no service banner."
	case fail(ProbeInternet) || (warn(ProbeInternet) && res[ProbeInternet].downgraded):
		return "The target works but direct internet egress is blocked (proxy-only or filtered network?)."
	case prxDown && directOK():
		return "The target and direct egress work, but the configured environment proxy check failed — apps that honor HTTP(S)_PROXY will fail (see the proxy row)."
	case warn(ProbeInternet):
		return "The target works but direct internet egress is degraded (see the ! row for details)."
	case degraded:
		return "The target works, but some checks are degraded (see the ! rows for details)."
	default:
		return "All checks passed — " + hp + " looks healthy."
	}
}

// Verdict classifications: the machine-readable half of Diagnose, for scripts
// that need the shape of the failure without parsing English. Stable vocabulary.
const (
	VerdictOK         = "ok"         // nothing failed, nothing degraded
	VerdictDegraded   = "degraded"   // everything asked for works, some rung is impaired
	VerdictDNS        = "dns"        // the name did not resolve
	VerdictNetwork    = "network"    // the path is unavailable — we never got to ask the service
	VerdictService    = "service"    // the path works, the service on the far end does not
	VerdictIncomplete = "incomplete" // a probe has no result yet
)

// Verdict answers the question the prose summary answers, in five words instead
// of a sentence: is this a broken path or a broken service? It reads the same
// probe state as Diagnose and follows the same case order, so the two never
// disagree — change one, change the other.
//
// The service/network split hinges on whether some other path proved the
// network usable: an unreachable target with working direct egress is the
// remote end's problem, the same target with no egress at all is ours.
func Verdict(t *Target, order []ProbeID, res map[ProbeID]ProbeResult) string {
	degraded := false
	for _, id := range order {
		r, ok := res[id]
		if !ok {
			return VerdictIncomplete
		}
		degraded = degraded || r.Status == StatusWarn
	}
	fail := func(id ProbeID) bool { return res[id].Status == StatusFail }
	has := func(id ProbeID) bool { _, ok := res[id]; return ok }
	failed := func(id ProbeID) bool { return has(id) && fail(id) }
	// Same definition as Diagnose's: a Warn that DowngradeEgress planted is a
	// Fail wearing a nicer hat, so it doesn't count as working egress.
	directOK := func() bool {
		r := res[ProbeInternet]
		return functional(r.Status) && !r.downgraded
	}
	proxyOK := func() bool { return has(ProbeProxy) && functional(res[ProbeProxy].Status) }

	switch {
	case fail(ProbeIface):
		return VerdictNetwork

	case t == nil:
		// Generic mode has only two rungs to lose: egress and DNS.
		switch {
		case !directOK() && fail(ProbeDNS):
			return VerdictNetwork
		case fail(ProbeDNS):
			return VerdictDNS
		case !directOK() && !proxyOK():
			return VerdictNetwork
		case !directOK():
			return VerdictDegraded // proxy-only network: online, just not directly
		}

	case fail(ProbeDNS):
		return VerdictDNS
	case fail(ProbeTargetTCP):
		// Without working direct egress we can't tell a closed port from a
		// dead path, so we don't guess — that's a network answer, not a
		// service one. A working proxy proves the box is online but says
		// nothing about the direct route this target needs.
		if directOK() {
			return VerdictService
		}
		return VerdictNetwork
	case failed(ProbeTLS), failed(ProbeHTTPS), failed(ProbeHTTP), failed(ProbeSSH), failed(ProbeSMTP):
		return VerdictService
	case !directOK():
		return VerdictDegraded // the target works; only the general internet is blocked
	}

	if degraded {
		return VerdictDegraded
	}
	return VerdictOK
}

func functional(s Status) bool {
	return s == StatusPass || s == StatusWarn
}

// DowngradeEgress rewrites a direct-egress failure to Warn once another path
// has proven the network usable: the target TCP connect succeeded, the
// environment proxy tunnels traffic, or — in generic mode, where DNS is the
// only other network path — DNS answered. Call it once, after every probe has
// a result; degraded-but-functional must not read as an outage.
func DowngradeEgress(res map[ProbeID]ProbeResult) {
	r, ok := res[ProbeInternet]
	if !ok || r.Status != StatusFail {
		return
	}
	other, hasTarget := res[ProbeTargetTCP]
	if !hasTarget {
		other = res[ProbeDNS]
	}
	prx, hasProxy := res[ProbeProxy]
	otherOK := functional(other.Status)
	proxyOK := hasProxy && functional(prx.Status)
	if !otherOK && !proxyOK {
		return
	}
	r.Status = StatusWarn
	r.downgraded = true
	if otherOK {
		r.Detail += " — but another path works"
	} else {
		r.Detail += " — but the environment proxy works"
	}
	res[ProbeInternet] = r
}
