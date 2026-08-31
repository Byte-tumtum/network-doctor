package diagnostic

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"time"
)

// lookupIPWithDial resolves host and reports which resolver was on the other
// end of the wire. The server identity comes free from the Go resolver's Dial
// hook, which already parses resolv.conf, so we don't have to. Release builds are
// CGO_ENABLED=0 and so already resolve this way; PreferGo only pins that
// behavior for local cgo builds.
//
// Windows (and anywhere the Go resolver isn't used) never calls the hook, so the
// server comes back empty and the row reads as it did before. Reading
// GetNetworkParams would fix that, if the missing identity ever bites.
func lookupIPWithDial(ctx context.Context, host string, dial func(context.Context, string, string) (net.Conn, error)) ([]net.IP, string, error) {
	var (
		mu     sync.Mutex
		server string
	)
	r := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			mu.Lock()
			// Last dial wins rather than the provably-answering one, which is right for
			// a single server, and right on failover, since the resolver
			// exhausts one server before trying the next.
			server = addr
			mu.Unlock()
			return dial(ctx, network, addr)
		},
	}
	ips, err := r.LookupIP(ctx, "ip", host)
	mu.Lock()
	defer mu.Unlock()
	return ips, server, err
}

// lookupIPPublicWithDial bypasses the configured resolver for a second opinion.
// Unavailability is reported as N/A by publicDNSProbe, never as a failure.
func lookupIPPublicWithDial(ctx context.Context, host string, dial func(context.Context, string, string) (net.Conn, error), server string) ([]net.IP, error) {
	r := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dial(ctx, network, server)
		},
	}
	return r.LookupIP(ctx, "ip", host)
}

// dnsServerLabel shortens a resolver dial address for a probe row: the bare host
// on port 53, host:port otherwise, since a stub resolver on 5353 is worth seeing.
func dnsServerLabel(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if port == "53" {
		return host
	}
	return addr
}

func dnsFailureCause(err error) string {
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return ""
	}
	if dnsErr.IsTimeout || timeoutError(err) {
		return DNSCauseTimeout
	}
	// The pure-Go resolver reports a received SERVFAIL as a DNSError whose
	// IsTemporary bit varies across supported Go versions. Once timeout and
	// NXDOMAIN are excluded, the remaining resolver failures are retryable
	// transport/server failures and belong to the temporary class.
	if dnsErr.IsTemporary || !dnsErr.IsNotFound {
		return DNSCauseTemporaryFailure
	}
	return ""
}

// dnsAnswer is one query's outcome, carried back from the goroutine that asked.
type dnsAnswer struct {
	ips    []net.IP
	server string
	err    error
}

// retryableDNS reports whether a failure is worth a second query. A timeout or
// a temporary server failure says something about the resolver's health at that
// instant; NXDOMAIN, like an answer, says something about the name, and asking
// twice cannot change either.
func retryableDNS(err error) bool {
	switch dnsFailureCause(err) {
	case DNSCauseTimeout, DNSCauseTemporaryFailure:
		return true
	}
	return false
}

// lookupIPRetrying resolves host and samples the resolver a second time when
// the first query neither answers nor fails conclusively. The second query goes
// out as soon as the first fails (SERVFAIL, or a stub that gives up early),
// and otherwise halfway through the probe budget, alongside a first query that
// is still waiting.
//
// Alongside, not instead of: cutting the first query short to make room would
// halve the patience of every DNS probe, and a resolver that answers late but
// within the budget would be reported as a timeout it never had. Two queries in
// flight cost one extra packet and settle both cases: the resolver that
// recovers mid-probe is heard, and the slow one keeps its own answer.
func (o *netops) lookupIPRetrying(ctx context.Context, host string) ([]net.IP, string, error) {
	// Buffered for both queries: the loser of the race writes after the winner
	// has been returned, and must not block until the context releases it.
	answers := make(chan dnsAnswer, 2)
	ask := func() {
		ips, server, err := o.lookupIP(ctx, host)
		answers <- dnsAnswer{ips, server, err}
	}
	go ask()
	budget := DefaultProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	resample := time.NewTimer(budget / 2)
	defer resample.Stop()
	outstanding, spare := 1, true
	send := func() {
		if spare {
			spare, outstanding = false, outstanding+1
			go ask()
		}
	}
	var last dnsAnswer
	for outstanding > 0 {
		select {
		case <-resample.C:
			send()
		case answer := <-answers:
			outstanding--
			if !retryableDNS(answer.err) {
				return answer.ips, answer.server, answer.err
			}
			last = answer
			send()
		}
	}
	return last.ips, last.server, last.err
}

func (o *netops) dnsProbe(host string, litIP net.IP) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		if litIP != nil {
			r.Status, r.Addrs, r.SelectedIP = StatusNA, []net.IP{litIP}, litIP
			r.Detail = "literal IP " + litIP.String() + ", no DNS needed"
			return r
		}
		ips, server, err := o.lookupIPRetrying(ctx, host)
		// "which server told me this" is the first question on a split-DNS or
		// router-vs-Pi-hole setup, and often the whole answer. Where the
		// resolver's address is known, the path to it is recorded too: a
		// resolver reached over one interface while the application traffic
		// leaves by another is the shape of split DNS, and it is not visible
		// from either row alone.
		via, paren := "", ""
		if server != "" {
			via = " via " + dnsServerLabel(server)
			paren = " (via " + dnsServerLabel(server) + ")"
		}
		routes := o.explainedRouteDecisions(o.referenceRouteDecisions(), resolverIP(server))
		if err != nil {
			r.Status = StatusFail
			r.Cause = dnsFailureCause(err)
			r.DNSNotFound = dnsNotFound(err)
			r.Detail = "cannot resolve " + host + via + ": " + err.Error()
			r.Fix = dnsFix(runtime.GOOS)
			r.Routes = routes
			return r
		}
		if len(ips) == 0 {
			r.Status = StatusFail
			r.DNSNotFound = true
			r.Detail, r.Fix = "no A/AAAA records for "+host+via, "no address returned: check the hostname / DNS"
			r.Routes = routes
			return r
		}
		r.Status, r.Addrs, r.Routes = StatusPass, ips, routes
		r.Detail = host + " → " + joinIPs(ips) + paren
		return r
	}
}

func (o *netops) publicDNSProbe(host string, litIP net.IP, publicDNSIP string) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
		if litIP != nil {
			return ProbeResult{Status: StatusNA, Detail: "literal IP " + litIP.String() + ", no DNS needed"}
		}
		ips, err := o.lookupPublicIP(ctx, host, publicDNSServer(publicDNSIP))
		if dnsNotFound(err) || err == nil && len(ips) == 0 {
			return ProbeResult{
				Status:      StatusPass,
				DNSNotFound: true,
				resolver:    publicDNSIP,
				Detail:      publicDNSIP + " reports no A/AAAA records for " + host,
			}
		}
		if err != nil {
			return ProbeResult{Status: StatusNA, resolver: publicDNSIP, Detail: "public DNS unavailable via " + publicDNSIP + ": " + err.Error()}
		}
		return ProbeResult{
			Status:   StatusPass,
			Addrs:    ips,
			resolver: publicDNSIP,
			Detail:   host + " → " + joinIPs(ips) + " (via " + publicDNSIP + ")",
		}
	}
}

func dnsNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}
