package diagnostic

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Diagnose computes the plain-English summary and its machine-readable
// classification from current-generation native probe state only (tool output
// never feeds in). First-fail ordering + combination rules. Returns
// "Running diagnostics…" until every probe in order has a result. A completed
// run always returns a verdict.
func Diagnose(t *Target, order []ProbeID, res map[ProbeID]ProbeResult) (string, string) {
	if len(order) == 0 {
		return "No checks selected.", VerdictOK
	}
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
	has := func(id ProbeID) bool { _, ok := res[id]; return ok }
	pass := func(id ProbeID) bool { r, ok := res[id]; return ok && r.Status == StatusPass }
	fail := func(id ProbeID) bool { r, ok := res[id]; return ok && r.Status == StatusFail }
	warn := func(id ProbeID) bool { r, ok := res[id]; return ok && r.Status == StatusWarn }
	timedOut := func(id ProbeID) bool { r, ok := res[id]; return ok && r.timedOut }
	directOK := func() bool { return directEgressOK(res) }
	fallback := func() (string, string) {
		for _, id := range order {
			switch {
			case !fail(id):
				continue
			case id == ProbeDNS:
				return "A selected DNS check failed.", VerdictDNS
			case id == ProbeTLS || id == ProbeHTTP || id == ProbeHTTPS || id == ProbeSSH || id == ProbeSMTP:
				return "A selected service check failed.", VerdictService
			default:
				return "A selected network check failed.", VerdictNetwork
			}
		}
		if degraded {
			return "Selected checks completed with warnings.", VerdictDegraded
		}
		return "Selected checks passed.", VerdictOK
	}

	if fail(ProbeIface) {
		return "No usable network interface: the link is down.", VerdictNetwork
	}

	prx := has(ProbeProxy) && functional(res[ProbeProxy].Status)
	prxDown := has(ProbeProxy) && fail(ProbeProxy)
	publicResolves := has(ProbeDNSPublic) && len(res[ProbeDNSPublic].Addrs) > 0
	bothNotFound := has(ProbeDNS) && res[ProbeDNS].DNSNotFound && has(ProbeDNSPublic) && res[ProbeDNSPublic].DNSNotFound

	// Generic mode (no target): the verdict is a truth table over egress, DNS,
	// and proxy state. Cases are ordered most-specific first because several
	// overlap, and reordering them changes answers, so don't.
	if t == nil {
		ip, dn := pass(ProbeInternet), pass(ProbeDNS)
		hasInternet := has(ProbeInternet)
		// Generic mode has only two rungs to lose, and which one broke doesn't
		// partition the prose cases cleanly, so classify it once up front.
		// Without egress a DNS failure is a symptom of the outage, not a
		// separate one; a working proxy makes a dead direct route a degradation.
		gv := healthy
		switch {
		case hasInternet && !directOK() && fail(ProbeDNS):
			gv = VerdictNetwork
		case fail(ProbeDNS):
			gv = VerdictDNS
		case hasInternet && !directOK() && !prx:
			gv = VerdictNetwork
		case hasInternet && !directOK():
			gv = VerdictDegraded
		}
		switch {
		case hasInternet && res[ProbeInternet].Portal != nil:
			return "Behind a captive portal: traffic is intercepted until you sign in to the network.", VerdictNetwork
		case directOK() && fail(ProbeDNS) && publicResolves:
			return "System DNS is failing, but public DNS resolves the name. Check the configured resolver, VPN, or DNS filter.", gv
		case directOK() && fail(ProbeDNS) && bothNotFound:
			return "Internet egress works, but the DNS test name has no A/AAAA records according to either resolver.", gv
		case directOK() && has(ProbeQUIC) && fail(ProbeQUIC):
			return "Direct TCP/443 works, but the QUIC handshake over UDP/443 failed. Applications can fall back to TCP, which may feel slower.", VerdictDegraded
		case encryptedDNSBlocked(res):
			return encryptedDNSSummary, VerdictDegraded
		case warn(ProbeDNSPublic) && has(ProbeDNS) && functional(res[ProbeDNS].Status):
			return "Online, but system DNS and public DNS disagree; split DNS or filtering may be intentional (see the DNS rows).", gv
		case ip && dn && prxDown:
			return "Online directly, but the configured environment proxy check failed, so apps that use the proxy will fail (see the proxy row).", VerdictDegraded
		case ip && dn:
			return "Online: direct TCP egress and DNS both work.", gv
		case warn(ProbeInternet) && res[ProbeInternet].downgraded && dn && prx:
			return "Online via the environment proxy: direct egress is blocked (proxy-only network).", gv
		case warn(ProbeInternet) && res[ProbeInternet].downgraded && fail(ProbeDNS) && prx:
			// The same proxy-only network as the case above, with its resolver
			// gone too. The Warn here is downgradeEgress's, not the probe's, so
			// the direct route is dead rather than impaired and the prose must
			// not promise egress the machine does not have.
			return "Direct egress is blocked and DNS resolution is failing; only the environment proxy is carrying traffic.", gv
		case directOK() && warn(ProbeInternet) && dn:
			return "Online but degraded: direct egress is impaired (see the ! row for details).", gv
		case directOK() && warn(ProbeInternet) && fail(ProbeDNS):
			// directOK, so this is a Warn the egress probe raised itself: one
			// family down, packet loss, and so on. Direct egress really does
			// carry traffic here, which is what separates it from the case
			// above.
			return "Internet egress works (degraded) but DNS resolution is failing.", gv
		case ip && fail(ProbeDNS):
			return "Internet egress works but DNS resolution is failing.", gv
		case hasInternet && !directOK() && dn:
			return "DNS resolves but there's no direct TCP egress (proxy-only or filtered network?).", gv
		case fail(ProbeInternet) && fail(ProbeDNS):
			return "Offline: neither direct egress nor DNS is working.", gv
		default:
			return fallback()
		}
	}

	host := t.Host
	hp := net.JoinHostPort(host, strconv.Itoa(t.Port)) // brackets IPv6 literals

	// Targeted mode: walk up the protocol stack (DNS → TCP → TLS → HTTP →
	// banner) and report the first rung that broke. Everything above it
	// failing is implied, everything below it passing is context.
	targetOK := false
	switch t.Proto {
	case ProtoTLSHTTP:
		targetOK = has(ProbeHTTP) && functional(res[ProbeHTTP].Status) && has(ProbeHTTPS) && functional(res[ProbeHTTPS].Status)
	case ProtoHTTP:
		targetOK = has(ProbeHTTP) && functional(res[ProbeHTTP].Status)
	case ProtoSSH:
		targetOK = has(ProbeSSH) && functional(res[ProbeSSH].Status)
	case ProtoSMTP:
		targetOK = has(ProbeSMTP) && functional(res[ProbeSMTP].Status)
	default:
		targetOK = has(ProbeTargetTCP) && functional(res[ProbeTargetTCP].Status)
	}

	switch {
	case has(ProbeInternet) && res[ProbeInternet].Portal != nil:
		// Ahead of the DNS rung: behind a portal every rung below is answering
		// for the portal, so nothing further down the stack means what it says.
		return "Behind a captive portal: sign in to the network before trusting anything about " + host + ".", VerdictNetwork
	case fail(ProbeDNS):
		v := "Cannot resolve " + host + ": DNS failure."
		if publicResolves {
			v = "System DNS cannot resolve " + host + ", but public DNS can, so the configured resolver is failing or filtering the name."
		} else if bothNotFound {
			v = host + " has no A/AAAA records according to either system or public DNS."
		}
		if directOK() {
			v += " (The general internet is reachable.)"
		}
		return v, VerdictDNS
	case fail(ProbeTargetTCP):
		// Without working direct egress we can't tell a closed port from a dead
		// path, so we don't guess: that's a network answer, not a service one.
		// A working proxy proves the box is online but says nothing about the
		// direct route this target needs.
		if directOK() {
			return hp + " is unreachable though DNS and the general internet work: remote port closed, firewall, or VPN routing.", VerdictService
		}
		if prx {
			return hp + " is unreachable directly, but the environment proxy has egress: this is a proxy-only network, so route traffic through the proxy.", VerdictNetwork
		}
		if !has(ProbeInternet) {
			return hp + " is unreachable, but general internet reachability was not checked, so a local path problem cannot be told apart from a remote service failure.", VerdictNetwork
		}
		return host + " resolves but neither it nor the general internet is reachable: local egress problem.", VerdictNetwork
	case warn(ProbePMTU) && ((fail(ProbeTLS) && timedOut(ProbeTLS)) ||
		(fail(ProbeHTTP) && timedOut(ProbeHTTP)) || (fail(ProbeHTTPS) && timedOut(ProbeHTTPS))):
		// A protocol timeout and a separate bulk-write stall are correlated
		// evidence for a path problem. Immediate failures such as a bad
		// certificate must continue down to their service-specific verdict.
		return "TCP reaches " + hp + " but the protocol and bulk-transfer checks both stall, which is evidence of a path MTU black hole rather than a broken service (see the Path MTU row).", VerdictNetwork
	case has(ProbeTLS) && fail(ProbeTLS):
		// With a measured offset that points the same way as the certificate
		// error there is nothing left to hedge about, so name it instead.
		// An offset pointing the other way settles the hedge just as well, in
		// the opposite direction: it cannot have caused this, so it comes off
		// the list of maybes instead of onto it.
		if d, ok := clockSkew(res); ok {
			switch {
			case skewExplainsTLS(res[ProbeTLS].Cause, d):
				return "TCP reaches " + hp + " but the TLS handshake fails because " + clockSkewPhrase(d) + ", so " + clockSkewEffect(d) + ".", VerdictService
			case skewDisprovesTLS(res[ProbeTLS].Cause, d):
				return "TCP reaches " + hp + " but the TLS handshake fails: bad/expired cert or MITM proxy.", VerdictService
			}
		}
		return "TCP reaches " + hp + " but the TLS handshake fails: bad/expired cert, clock skew, or MITM proxy.", VerdictService
	case has(ProbeHTTPS) && fail(ProbeHTTPS):
		return "TLS is fine but no HTTPS response from " + hp + ": application-layer or proxy block.", VerdictService
	case has(ProbeHTTP) && fail(ProbeHTTP):
		if t.Proto == ProtoTLSHTTP {
			return "HTTPS works but no HTTP response from " + net.JoinHostPort(host, "80") + ": the redirect/plain-HTTP endpoint may be blocked.", VerdictService
		}
		return "No HTTP response from " + hp + ": application-layer or proxy block.", VerdictService
	case (has(ProbeSSH) && fail(ProbeSSH)) || (has(ProbeSMTP) && fail(ProbeSMTP)):
		return hp + " accepts TCP but the service banner check failed.", VerdictService
	case (has(ProbeSSH) && warn(ProbeSSH)) || (has(ProbeSMTP) && warn(ProbeSMTP)):
		return hp + " accepts TCP but sent no service banner.", VerdictDegraded
	case targetOK && has(ProbeQUIC) && fail(ProbeQUIC) && directOK():
		return "The target and direct TCP/443 work, but the QUIC handshake over UDP/443 failed. Applications can fall back to TCP, which may feel slower.", VerdictDegraded
	case encryptedDNSBlocked(res):
		return encryptedDNSSummary, VerdictDegraded
	case targetOK && (fail(ProbeInternet) || (warn(ProbeInternet) && res[ProbeInternet].downgraded)):
		return "The target works but direct internet egress is blocked (proxy-only or filtered network?).", VerdictDegraded
	case targetOK && prxDown && directOK():
		return "The target and direct egress work, but the configured environment proxy check failed, so apps that use the proxy will fail (see the proxy row).", VerdictDegraded
	case targetOK && warn(ProbeInternet):
		return "The target works but direct internet egress is degraded (see the ! row for details).", VerdictDegraded
	case targetOK && warn(ProbeDNSPublic) && has(ProbeDNS) && functional(res[ProbeDNS].Status):
		return "The target works, but system DNS and public DNS disagree; split DNS or filtering may be intentional (see the DNS rows).", VerdictDegraded
	case targetOK && degraded:
		return "The target works, but some checks are degraded (see the ! rows for details).", VerdictDegraded
	case targetOK:
		return "All checks passed. " + hp + " looks healthy.", VerdictOK
	default:
		return fallback()
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
	VerdictNetwork    = "network"    // the path is unavailable, so we never got to ask the service
	VerdictService    = "service"    // the path works, the service on the far end does not
	VerdictIncomplete = "incomplete" // a probe has no result yet
)

func functional(s Status) bool {
	return s == StatusPass || s == StatusWarn
}

// encryptedDNSSummary is what the probes can actually support: plaintext
// resolution demonstrably works and encrypted resolution demonstrably does not.
// It stops there. Nothing here proves the network *forced* anything: an
// operator's intent is not observable from a client, and neither is whatever a
// browser did next about it.
const encryptedDNSSummary = "Plain DNS works, but encrypted DNS could not complete a verified exchange. The resolver may be unavailable, or the network may be blocking or interfering with DoH/DoT."

// directEgressOK means direct egress genuinely worked: a Pass, or a Warn the
// probe produced itself. A Warn planted by downgradeEgress doesn't count:
// that's a Fail wearing a nicer hat.
func directEgressOK(res map[ProbeID]ProbeResult) bool {
	r, ok := res[ProbeInternet]
	return ok && functional(r.Status) && !r.downgraded
}

// encryptedDNSBlocked reports the one state that is specific to encrypted DNS:
// the encrypted row failed while the network is otherwise carrying traffic and
// plaintext resolution still works. When DNS is failing outright, or when there
// is no working egress to reach any resolver, the encrypted row is a symptom
// and not a diagnosis, so this deliberately says no.
func encryptedDNSBlocked(res map[ProbeID]ProbeResult) bool {
	encrypted, ok := res[ProbeDNSEncrypted]
	if !ok || encrypted.Status != StatusFail {
		return false
	}
	// Without working direct egress the encrypted resolver is simply one more
	// address this machine cannot reach, and the path is the story. Naming
	// encrypted DNS there would blame the transport for a broken route.
	if !directEgressOK(res) {
		return false
	}
	// Presence is checked per row rather than read off the map: an absent probe
	// yields the zero ProbeResult, whose Status is StatusPass.
	for _, id := range []ProbeID{ProbeDNS, ProbeDNSPublic} {
		if plain, ok := res[id]; ok && functional(plain.Status) {
			return true
		}
	}
	return false
}

// Finalize applies the cross-probe passes that only make sense once every probe
// has a result: results that read each other, rather than the network. Both
// runners call it (RunAll on its way out, the ui scheduler when the last
// result lands), so a new pass added here reaches --json and the TUI at once,
// in the same order. Idempotent, but there's no reason to call it twice.
func Finalize(res map[ProbeID]ProbeResult) {
	reconcileDNS(res)
	downgradeEgress(res)
	// After the downgrade, so "direct egress worked" means what the finished
	// report says it means rather than what it said mid-pass.
	reconcileEncryptedDNS(res)
	reconcileClockSkew(res)
}

// clockSkewThreshold is how far this machine's clock has to be off the
// network's before netdoc will name it rather than list it as a maybe. HTTP
// Date has one-second granularity and the reading carries a round trip of
// latency and scheduler jitter, so the floor sits far above all three; it is
// also roughly where a wrong clock stops being cosmetic and starts rejecting
// certificates that everyone else accepts.
const clockSkewThreshold = 5 * time.Minute

// clockSkew returns the signed local-minus-remote offset the egress probe
// measured, and whether it is large enough to act on. The reading only exists
// when the captive-portal check got a clean 204 from the fixed connectivity
// endpoint, so a portal that check can see never supplies it. That is a
// heuristic over plain HTTP, not authentication, which is why this is usable
// evidence rather than proof: the causal claim is only made when a
// certificate-date rejection independently points the same way. An offset
// exactly at the threshold counts.
func clockSkew(res map[ProbeID]ProbeResult) (time.Duration, bool) {
	d := res[ProbeInternet].clockOffset
	return d, d.Abs() >= clockSkewThreshold
}

// skewExplainsTLS reports whether the offset points the same way as the
// certificate error. A slow clock is what makes a valid certificate look not
// yet valid, and a fast one is what makes it look expired. The other two
// pairings are a genuinely bad certificate standing next to an unrelated bad
// clock, and saying otherwise would send the user to fix the wrong thing.
func skewExplainsTLS(cause string, d time.Duration) bool {
	switch cause {
	case TLSCauseCertificateNotYet:
		return d < 0
	case TLSCauseCertificateExpired:
		return d > 0
	}
	return false
}

// skewDisprovesTLS reports whether the offset rules the clock out. A
// certificate-date rejection can only be explained by an offset pointing the
// same way, so a material offset pointing the other way is evidence against
// the clock rather than an absence of evidence for it, and the hint should
// stop offering it.
func skewDisprovesTLS(cause string, d time.Duration) bool {
	switch cause {
	case TLSCauseCertificateNotYet, TLSCauseCertificateExpired:
		return !skewExplainsTLS(cause, d)
	}
	return false
}

// reconcileClockSkew replaces the TLS row's guess about the clock with the
// reading the egress probe already took, which settles the guess either way:
// an offset pointing at the certificate-date rejection becomes the answer, one
// pointing away from it takes the clock off the list, and the unclassified
// handshake failure gets the measurement alongside its maybes. Anything else
// has a specific cause of its own, and a bad clock beside it is a coincidence.
func reconcileClockSkew(res map[ProbeID]ProbeResult) {
	d, ok := clockSkew(res)
	if !ok {
		return
	}
	r, has := res[ProbeTLS]
	if !has || r.Status != StatusFail {
		return
	}
	switch {
	case skewExplainsTLS(r.Cause, d):
		r.Fix = clockSkewPhrase(d) + ", so " + clockSkewEffect(d) + ": set the clock (enable network time) and retry"
	case skewDisprovesTLS(r.Cause, d):
		// The certificate remedy stands; only the clock maybe goes, since the
		// offset runs the wrong way to have produced this rejection.
		r.Fix = strings.TrimSuffix(r.Fix, clockHedgeSuffix)
	case r.Cause == TLSCauseHandshake:
		r.Fix = "TLS broken: bad/expired cert or MITM proxy, and separately " + clockSkewPhrase(d) + ", which can break certificate validation on its own"
	default:
		return
	}
	res[ProbeTLS] = r
}

// clockSkewPhrase states the measurement. Direction is the half that decides
// what the user changes, so it is never dropped.
func clockSkewPhrase(d time.Duration) string {
	direction := " fast"
	if d < 0 {
		direction = " slow"
	}
	return "this machine's clock is about " + humanSkew(d) + direction
}

// clockSkewEffect names what that offset does to certificate validation.
func clockSkewEffect(d time.Duration) string {
	if d < 0 {
		return "certificates that are already valid look not yet valid"
	}
	return "certificates that are still valid look expired"
}

// humanSkew renders the magnitude of an offset at one sensible unit. Nothing
// below clockSkewThreshold reaches here, and the cutoffs are placed so the
// rounded count is never 1, which keeps the plural honest without a special
// case for it.
func humanSkew(d time.Duration) string {
	d = d.Abs()
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d.Round(24*time.Hour)/(24*time.Hour))) + " days"
	case d >= 90*time.Minute:
		return strconv.Itoa(int(d.Round(time.Hour)/time.Hour)) + " hours"
	default:
		return strconv.Itoa(int(d.Round(time.Minute)/time.Minute)) + " minutes"
	}
}

// reconcileEncryptedDNS adds the one thing the encrypted row cannot know on its
// own: whether plaintext resolution was working at the same time. That
// comparison is the difference between "encrypted DNS specifically is not
// getting through" and "DNS is broken", and it is as far as the evidence goes:
// the row states what both probes observed and never what the network intended.
func reconcileEncryptedDNS(res map[ProbeID]ProbeResult) {
	if !encryptedDNSBlocked(res) {
		return
	}
	encrypted := res[ProbeDNSEncrypted]
	encrypted.Detail += "; plain DNS resolves at the same time, so this is specific to encrypted DNS"
	encrypted.Fix = "encrypted DNS cannot complete a verified exchange while ordinary DNS works; the resolver may be unavailable, or a filter or middlebox may block or intercept DoH/DoT"
	res[ProbeDNSEncrypted] = encrypted
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
		system.Detail += "; public DNS agrees there are no A/AAAA records"
		system.Fix = "check the hostname or publish the missing DNS record"
	case system.Status == StatusPass && public.DNSNotFound:
		public.Status = StatusWarn
		public.Detail = "system DNS resolves the name, but " + public.resolver + " reports no records; split DNS or filtering may be intentional"
	case system.DNSNotFound && len(public.Addrs) > 0:
		public.Status = StatusWarn
		public.Detail += " (system DNS reports no records, so split DNS or filtering may be intentional)"
		system.Detail += ", but public DNS resolves it"
		system.Fix = "check the configured resolver, VPN, or DNS filter"
	case system.Status == StatusPass && len(public.Addrs) > 0 && !answersAgree(system.Addrs, public.Addrs):
		public.Status = StatusWarn
		public.Detail = "answers point elsewhere; system: " + joinIPs(system.Addrs) + "; public " + public.resolver + ": " + joinIPs(public.Addrs) + " (split DNS may be intentional)"
	case system.Status == StatusFail && len(public.Addrs) > 0:
		system.Detail += ", but public DNS resolves it"
		system.Fix = "check the configured resolver, VPN, or DNS filter"
	}
	res[ProbeDNS], res[ProbeDNSPublic] = system, public
}

// answersAgree reports whether two answer sets point at the same place.
// Round-robin and GeoDNS make byte-identical sets the exception rather than
// the rule (anycast hosts answer differently per resolver by design), so
// agreement means one shared routing prefix, not set equality. Disjoint
// prefixes are the case worth a warning: a different operator, or one side
// private and the other public.
func answersAgree(a, b []net.IP) bool {
	seen := make(map[netip.Prefix]struct{}, len(a))
	for _, ip := range a {
		if p, ok := routePrefix(ip); ok {
			seen[p] = struct{}{}
		}
	}
	for _, ip := range b {
		p, ok := routePrefix(ip)
		if !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			return true
		}
	}
	return false
}

// routePrefix reduces an address to the block its operator was allocated:
// /16 for v4, /32 for v6: the RIR allocation size, deliberately coarser than
// the announced route. One anycast host answers from many /24s within a single
// allocation (142.250.9.94 and 142.250.189.99 are both Google), and treating
// those as different networks is exactly the false positive worth avoiding.
// Erring coarse costs us a warning when two operators share a block; erring
// fine costs a wrong headline verdict on every healthy run.
// Prefix length stands in for an ASN lookup: only an RIR/BGP source gets this
// exactly right, and it would cost either a network call or a bundled dataset
// in a probe that must stay time-bounded and offline.
func routePrefix(ip net.IP) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	bits := 32
	if addr.Is4() {
		bits = 16
	}
	p, err := addr.Prefix(bits)
	return p, err == nil
}

// downgradeEgress rewrites a direct-egress failure to Warn once another path
// has proven the network usable: the target TCP connect succeeded, the
// environment proxy tunnels traffic, or, in generic mode where DNS is the
// only other network path, DNS answered. Call it once, after every probe has
// a result; degraded-but-functional must not read as an outage.
func downgradeEgress(res map[ProbeID]ProbeResult) {
	r, ok := res[ProbeInternet]
	// An intercepted path is exempt: behind a portal DNS and the target
	// connect both "work" because the portal answers them, so the usual
	// evidence that the network is usable is exactly the thing being faked.
	if !ok || r.Status != StatusFail || r.Portal != nil {
		return
	}
	other, hasOther := res[ProbeTargetTCP]
	if !hasOther {
		other, hasOther = res[ProbeDNS]
	}
	prx, hasProxy := res[ProbeProxy]
	otherOK := hasOther && functional(other.Status)
	proxyOK := hasProxy && functional(prx.Status)
	if !otherOK && !proxyOK {
		return
	}
	r.Status = StatusWarn
	r.downgraded = true
	if otherOK {
		r.Detail += ", but another path works"
	} else {
		r.Detail += ", but the environment proxy works"
	}
	res[ProbeInternet] = r
}
