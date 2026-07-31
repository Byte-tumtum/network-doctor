package diagnostic

import (
	"maps"
	"net"
	"strconv"
)

// Diagnose computes the plain-English summary and its machine-readable
// classification from current-generation native probe state only (tool output
// never feeds in). First-fail ordering + combination rules. Returns
// "Running diagnostics…" until every probe in order has a result. A completed
// run always returns a verdict.
func Diagnose(t *Target, order []ProbeID, res map[ProbeID]ProbeResult) (string, string) {
	degraded := false
	for _, id := range order {
		r, ok := res[id]
		if !ok {
			return "Running diagnostics…", VerdictIncomplete
		}
		degraded = degraded || r.Status == StatusWarn
	}
	// Everything the caller asked for works; a Warn anywhere still counts.
	healthy := VerdictOK
	if degraded {
		healthy = VerdictDegraded
	}
	pass := func(id ProbeID) bool { return res[id].Status == StatusPass }
	fail := func(id ProbeID) bool { return res[id].Status == StatusFail }
	warn := func(id ProbeID) bool { return res[id].Status == StatusWarn }
	has := func(id ProbeID) bool { _, ok := res[id]; return ok }
	anyFail := func(ids ...ProbeID) bool {
		for _, id := range ids {
			if has(id) && fail(id) {
				return true
			}
		}
		return false
	}
	// directOK means direct egress genuinely worked: a Pass, or a Warn the
	// probe produced itself. A Warn planted by downgradeEgress doesn't count —
	// that's a Fail wearing a nicer hat.
	directOK := func() bool {
		r := res[ProbeInternet]
		return functional(r.Status) && !r.downgraded
	}

	if fail(ProbeIface) {
		return "No usable network interface — the link is down.", VerdictNetwork
	}

	prx := has(ProbeProxy) && functional(res[ProbeProxy].Status)
	prxDown := has(ProbeProxy) && fail(ProbeProxy)
	publicResolves := has(ProbeDNSPublic) && len(res[ProbeDNSPublic].Addrs) > 0
	bothNotFound := res[ProbeDNS].DNSNotFound && has(ProbeDNSPublic) && res[ProbeDNSPublic].DNSNotFound

	// Generic mode (no target): the verdict is a truth table over egress, DNS,
	// and proxy state. Cases are ordered most-specific first because several
	// overlap — reordering them changes answers, so don't.
	if t == nil {
		ip, dn := pass(ProbeInternet), pass(ProbeDNS)
		// Generic mode has only two rungs to lose, and which one broke doesn't
		// partition the prose cases cleanly, so classify it once up front.
		// Without egress a DNS failure is a symptom of the outage, not a
		// separate one; a working proxy makes a dead direct route a degradation.
		gv := healthy
		switch {
		case !directOK() && fail(ProbeDNS):
			gv = VerdictNetwork
		case fail(ProbeDNS):
			gv = VerdictDNS
		case !directOK() && !prx:
			gv = VerdictNetwork
		case !directOK():
			gv = VerdictDegraded
		}
		switch {
		case res[ProbeInternet].Portal != nil:
			return "Behind a captive portal — traffic is intercepted until you sign in to the network.", VerdictNetwork
		case directOK() && fail(ProbeDNS) && publicResolves:
			return "System DNS is failing, but public DNS resolves the name — check the configured resolver, VPN, or DNS filter.", gv
		case directOK() && fail(ProbeDNS) && bothNotFound:
			return "Internet egress works, but the DNS test name has no A/AAAA records according to either resolver.", gv
		case warn(ProbeDNSPublic) && functional(res[ProbeDNS].Status):
			return "Online, but system DNS and public DNS disagree — split DNS or filtering may be intentional (see the DNS rows).", gv
		case ip && dn && prxDown:
			return "Online directly — but the configured environment proxy check failed, so apps that honor HTTP(S)_PROXY will fail (see the proxy row).", gv
		case ip && dn:
			return "Online — direct TCP egress and DNS both work.", gv
		case warn(ProbeInternet) && res[ProbeInternet].downgraded && dn && prx:
			return "Online via the environment proxy — direct egress is blocked (proxy-only network).", gv
		case directOK() && warn(ProbeInternet) && dn:
			return "Online but degraded — direct egress is impaired (see the ! row for details).", gv
		case warn(ProbeInternet) && !dn:
			return "Internet egress works (degraded) but DNS resolution is failing.", gv
		case ip && !dn:
			return "Internet egress works but DNS resolution is failing.", gv
		case !ip && dn:
			return "DNS resolves but there's no direct TCP egress (proxy-only or filtered network?).", gv
		default:
			return "Offline — neither direct egress nor DNS is working.", gv
		}
	}

	host := t.Host
	hp := net.JoinHostPort(host, strconv.Itoa(t.Port)) // brackets IPv6 literals

	// Targeted mode: walk up the protocol stack (DNS → TCP → TLS → HTTP →
	// banner) and report the first rung that broke — everything above it
	// failing is implied, everything below it passing is context.
	switch {
	case res[ProbeInternet].Portal != nil:
		// Ahead of the DNS rung: behind a portal every rung below is answering
		// for the portal, so nothing further down the stack means what it says.
		return "Behind a captive portal — sign in to the network before trusting anything about " + host + ".", VerdictNetwork
	case fail(ProbeDNS):
		v := "Cannot resolve " + host + " — DNS failure."
		if publicResolves {
			v = "System DNS cannot resolve " + host + ", but public DNS can — the configured resolver is failing or filtering the name."
		} else if bothNotFound {
			v = host + " has no A/AAAA records according to either system or public DNS."
		}
		if directOK() {
			v += " (The general internet is reachable.)"
		}
		return v, VerdictDNS
	case fail(ProbeTargetTCP):
		// Without working direct egress we can't tell a closed port from a dead
		// path, so we don't guess — that's a network answer, not a service one.
		// A working proxy proves the box is online but says nothing about the
		// direct route this target needs.
		if directOK() {
			return hp + " is unreachable though DNS and the general internet work — remote port closed, firewall, or VPN routing.", VerdictService
		}
		if prx {
			return hp + " is unreachable directly, but the environment proxy has egress — proxy-only network; route traffic through the proxy.", VerdictNetwork
		}
		return host + " resolves but neither it nor the general internet is reachable — local egress problem.", VerdictNetwork
	case warn(ProbePMTU) && anyFail(ProbeTLS, ProbeHTTP, ProbeHTTPS, ProbeSSH, ProbeSMTP):
		// Ahead of every protocol rung: a black hole lets the handshake through
		// and then eats the first full-size segment of whatever follows, so each
		// of those rows is reporting the same one problem — and it's a path
		// problem, not the broken service they'd otherwise be blamed on.
		return "TCP reaches " + hp + " but anything larger than the handshake is being dropped — a path MTU black hole, not a broken service (see the Path MTU row).", VerdictNetwork
	case has(ProbeTLS) && fail(ProbeTLS):
		return "TCP reaches " + hp + " but the TLS handshake fails — bad/expired cert, clock skew, or MITM proxy.", VerdictService
	case has(ProbeHTTPS) && fail(ProbeHTTPS):
		return "TLS is fine but no HTTPS response from " + hp + " — application-layer or proxy block.", VerdictService
	case has(ProbeHTTP) && fail(ProbeHTTP):
		if t.Proto == ProtoTLSHTTP {
			return "HTTPS works but no HTTP response from " + net.JoinHostPort(host, "80") + " — the redirect/plain-HTTP endpoint may be blocked.", VerdictService
		}
		return "No HTTP response from " + hp + " — application-layer or proxy block.", VerdictService
	case (has(ProbeSSH) && fail(ProbeSSH)) || (has(ProbeSMTP) && fail(ProbeSMTP)):
		return hp + " accepts TCP but the service banner check failed.", VerdictService
	case (has(ProbeSSH) && warn(ProbeSSH)) || (has(ProbeSMTP) && warn(ProbeSMTP)):
		return hp + " accepts TCP but sent no service banner.", VerdictDegraded
	case fail(ProbeInternet) || (warn(ProbeInternet) && res[ProbeInternet].downgraded):
		return "The target works but direct internet egress is blocked (proxy-only or filtered network?).", VerdictDegraded
	case prxDown && directOK():
		return "The target and direct egress work, but the configured environment proxy check failed — apps that honor HTTP(S)_PROXY will fail (see the proxy row).", healthy
	case warn(ProbeInternet):
		return "The target works but direct internet egress is degraded (see the ! row for details).", VerdictDegraded
	case warn(ProbeDNSPublic):
		return "The target works, but system DNS and public DNS disagree — split DNS or filtering may be intentional (see the DNS rows).", VerdictDegraded
	case degraded:
		return "The target works, but some checks are degraded (see the ! rows for details).", VerdictDegraded
	default:
		return "All checks passed — " + hp + " looks healthy.", VerdictOK
	}
}

// Verdict classifications: the second half of Diagnose's return, for scripts
// that need the shape of the failure without parsing English. It answers the
// question the prose answers, in one word: is this a broken path or a broken
// service? Stable vocabulary.
const (
	VerdictOK         = "ok"         // nothing failed, nothing degraded
	VerdictDegraded   = "degraded"   // everything asked for works, some rung is impaired
	VerdictDNS        = "dns"        // the name did not resolve
	VerdictNetwork    = "network"    // the path is unavailable — we never got to ask the service
	VerdictService    = "service"    // the path works, the service on the far end does not
	VerdictIncomplete = "incomplete" // a probe has no result yet
)

func functional(s Status) bool {
	return s == StatusPass || s == StatusWarn
}

// Finalize applies the cross-probe passes that only make sense once every probe
// has a result: results that read each other, rather than the network. Both
// runners call it — RunAll on its way out, the ui scheduler when the last
// result lands — so a new pass added here reaches --json and the TUI at once,
// in the same order. Idempotent, but there's no reason to call it twice.
func Finalize(res map[ProbeID]ProbeResult) {
	reconcileDNS(res)
	downgradeEgress(res)
}

// reconcileDNS compares the independently collected system and public answers.
// Public DNS can add context or a warning, but never fails a run on its own.
func reconcileDNS(res map[ProbeID]ProbeResult) {
	system, systemOK := res[ProbeDNS]
	public, publicOK := res[ProbeDNSPublic]
	if !systemOK || !publicOK || public.Status != StatusPass {
		return
	}
	switch {
	case system.DNSNotFound && public.DNSNotFound:
		system.Detail += " — public DNS agrees there are no A/AAAA records"
		system.Fix = "check the hostname or publish the missing DNS record"
	case system.Status == StatusPass && public.DNSNotFound:
		public.Status = StatusWarn
		public.Detail = "system DNS resolves the name, but " + publicDNSIP + " reports no records — split DNS or filtering may be intentional"
	case system.DNSNotFound && len(public.Addrs) > 0:
		public.Status = StatusWarn
		public.Detail += " — system DNS reports no records; split DNS or filtering may be intentional"
		system.Detail += " — but public DNS resolves it"
		system.Fix = "check the configured resolver, VPN, or DNS filter"
	case system.Status == StatusPass && len(public.Addrs) > 0 && !sameIPSet(system.Addrs, public.Addrs):
		public.Status = StatusWarn
		public.Detail = "answers differ — system: " + joinIPs(system.Addrs) + "; public " + publicDNSIP + ": " + joinIPs(public.Addrs) + " (split DNS may be intentional)"
	case system.Status == StatusFail && len(public.Addrs) > 0:
		system.Detail += " — but public DNS resolves it"
		system.Fix = "check the configured resolver, VPN, or DNS filter"
	}
	res[ProbeDNS], res[ProbeDNSPublic] = system, public
}

func sameIPSet(a, b []net.IP) bool {
	set := func(ips []net.IP) map[string]struct{} {
		m := make(map[string]struct{}, len(ips))
		for _, ip := range ips {
			m[ip.String()] = struct{}{}
		}
		return m
	}
	return maps.Equal(set(a), set(b))
}

// downgradeEgress rewrites a direct-egress failure to Warn once another path
// has proven the network usable: the target TCP connect succeeded, the
// environment proxy tunnels traffic, or — in generic mode, where DNS is the
// only other network path — DNS answered. Call it once, after every probe has
// a result; degraded-but-functional must not read as an outage.
func downgradeEgress(res map[ProbeID]ProbeResult) {
	r, ok := res[ProbeInternet]
	// An intercepted path is exempt: behind a portal DNS and the target
	// connect both "work" because the portal answers them, so the usual
	// evidence that the network is usable is exactly the thing being faked.
	if !ok || r.Status != StatusFail || r.Portal != nil {
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
