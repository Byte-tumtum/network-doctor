package diagnostic

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// portalCheckWithDial fetches portalProbeURL and reports the status code it got
// plus a valid HTTP(S) redirect URL, if the response advertised one, and the
// Date the responder stamped on it.
// Proxy and redirect following are both off: the direct-egress row must not
// borrow the proxy's path, and an interception usually announces itself as
// the 302 we'd otherwise chase to a sign-in page.
func portalCheckWithDial(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) (int, string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, portalProbeURL, nil)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	c := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dial,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 64 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	_ = resp.Body.Close()
	// http.ParseTime accepts the three date formats RFC 9110 allows. An absent
	// or unparsable Date leaves the zero time, and every caller reads that as
	// "no reading" rather than as a time.
	date, _ := http.ParseTime(resp.Header.Get("Date"))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if u, err := resp.Location(); err == nil && u.Hostname() != "" {
			switch strings.ToLower(u.Scheme) {
			case "http", "https":
				return resp.StatusCode, u.String(), date, nil
			}
		}
	}
	return resp.StatusCode, "", date, nil
}

func (o *netops) internetProbe(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
	var r ProbeResult
	type famResult struct {
		ips      []net.IP
		conn     net.Conn
		sel      net.IP
		attempts []Attempt
		rtt      time.Duration
	}
	// Each family is probed independently and in parallel: a black-holing
	// family only spends its own share of the probe deadline, and IPv4 and
	// IPv6 egress are diagnosed separately.
	var v4, v6 famResult
	// --iface binds every dial to that interface's address of the destination's
	// family, so a family it has no address for cannot be dialed at all. Drop it
	// here rather than learn it from a bind error: an impossible attempt proves
	// nothing about the network, and calling it unreachable reports an outage in
	// a family nobody tested.
	v4.ips, v6.ips = o.compatibleSourceIPs(internetEndpoints4), o.compatibleSourceIPs(internetEndpoints6)
	if len(v4.ips) == 0 && len(v6.ips) == 0 {
		return ProbeResult{Status: StatusNA, Detail: "the selected interface has no address family available for direct egress"}
	}
	// portalCode stays 0 when the check is stubbed out or never answered; only
	// a real status code is evidence either way.
	var portalCode int
	var portalURL string
	// portalSkew is this machine's clock minus the responder's Date, sampled
	// in the goroutine so it carries the HTTP round trip and nothing else.
	var portalSkew time.Duration
	var wg sync.WaitGroup
	wg.Go(func() {
		v4.conn, v4.sel, v4.attempts, v4.rtt = o.dialIPs(ctx, v4.ips, 443)
	})
	wg.Go(func() {
		v6.conn, v6.sel, v6.attempts, v6.rtt = o.dialIPs(ctx, v6.ips, 443)
	})
	if o.portalCheck != nil {
		// Runs alongside the dials rather than after them: it costs nothing
		// when egress is clean, and its answer is only consulted on success.
		wg.Go(func() {
			if code, redirect, date, err := o.portalCheck(ctx); err == nil {
				portalCode, portalURL = code, redirect
				if !date.IsZero() {
					portalSkew = time.Since(date)
				}
			}
		})
	}
	wg.Wait()
	// Only the 204 leaves usable clock evidence: any other status is an
	// interception this check can see, and an interceptor's clock speaks for
	// the interceptor. A 204 is not proof of an unmodified path, since this
	// is plain HTTP and a transparent proxy could synthesize both the status
	// and the Date. It is the same heuristic the portal verdict already rests
	// on, and no stronger.
	if portalCode == http.StatusNoContent {
		r.clockOffset = portalSkew
	}
	r.Families = &FamilyConnectivity{IPv4: familyState(v4.ips, v4.conn), IPv6: familyState(v6.ips, v6.conn)}

	// IPv4 headlines the result unless it lost and IPv6 won. Not a value
	// judgment, just a stable order for the Detail string and warnings.
	prim, sec, primName, secName := v4, v6, "IPv4", "IPv6"
	if v4.conn == nil && v6.conn != nil {
		prim, sec, primName, secName = v6, v4, "IPv6", "IPv4"
	}
	if prim.conn == nil {
		r.Attempts = append(v4.attempts, v6.attempts...)
		all := append(append([]net.IP{}, v4.ips...), v6.ips...)
		r.Detail = "no direct TCP egress to " + joinIPs(all) + " (port 443)"
		src, iface, ambiguous := o.pathIdentity(ctx, nil, all[0], 443)
		r.Status, r.Source, r.Iface, r.ifaceAmbiguous = StatusFail, src, iface, ambiguous
		// Portals commonly drop 443 outright while still intercepting plain
		// HTTP. The 204 endpoint answering anything else is proof of a portal
		// even with no handshake to show for it, so report that, not "check
		// upstream".
		if portalCode != 0 && portalCode != http.StatusNoContent {
			r.Portal = &Portal{RedirectURL: portalURL}
			r.Detail += fmt.Sprintf(", and HTTP is intercepted: %s answered %d, want 204", portalProbeURL, portalCode)
			r.Fix = "captive portal or transparent filter: open a browser and sign in to the network"
			return r
		}
		if o.routeCause != nil {
			r.Cause, r.causeFamily = failedRouteCause(o.routeCause, v4.ips, v6.ips)
		}
		// The routing table decides the advice: a missing default route and a
		// filtered upstream are different repairs. An empty or unrecognized
		// cause keeps the generic hint.
		r.Fix = routeFix(r.Cause)
		return r
	}
	defer prim.conn.Close()
	if sec.conn != nil {
		_ = sec.conn.Close()
	}
	src, iface, ambiguous := o.pathIdentity(ctx, prim.conn, prim.sel, 443)
	// A completed handshake only proves that something answered. A captive
	// portal or transparent filter terminates the connection itself and is
	// indistinguishable from real egress at this layer; the 204 endpoint is
	// what tells them apart, so ask before calling the network online.
	if portalCode != 0 && portalCode != http.StatusNoContent {
		r.Status, r.SelectedIP, r.Source, r.Iface, r.ifaceAmbiguous = StatusFail, prim.sel, src, iface, ambiguous
		r.Attempts = append(prim.attempts, sec.attempts...)
		r.Portal = &Portal{RedirectURL: portalURL}
		r.Detail = fmt.Sprintf("TCP reaches %s but HTTP is intercepted: %s answered %d, want 204", prim.sel, portalProbeURL, portalCode)
		r.Fix = "captive portal or transparent filter: open a browser and sign in to the network"
		return r
	}
	r.Status, r.SelectedIP, r.Source, r.Iface, r.ifaceAmbiguous = StatusPass, prim.sel, src, iface, ambiguous
	r.Detail = fmt.Sprintf("%s egress via %s in %dms (src %s %s)", primName, prim.sel, Ms(prim.rtt), src, iface)
	switch {
	case sec.conn != nil:
		r.Detail += fmt.Sprintf("; %s egress via %s in %dms", secName, sec.sel, Ms(sec.rtt))
	case len(sec.ips) == 0:
		r.Detail += "; " + secName + " not tested (the selected interface has no " + secName + " address)"
	default:
		r.Detail += "; no " + secName + " egress"
	}
	// Warnings judge only the winning family: a network without the other
	// family at all is normal, not degraded. The other family's attempts are
	// appended afterwards so the details panel still shows them.
	r.Attempts = prim.attempts
	var extra []string
	// The exception: a machine that took a global IPv6 address and still can't
	// reach IPv6 is broken, not v4-only. Happy Eyeballs hides that from netdoc
	// and from browsers, but not from software that dials AAAA and waits.
	if sec.conn == nil && secName == "IPv6" && o.hasGlobalUnicast(false) {
		extra = append(extra, "IPv6 address configured but no IPv6 egress (black-holed)")
		r.Cause = FamilyCauseIPv6Unreachable
		r.causeFamily = counterfactualIPv6
		r.Fix = "check the IPv6 default route, gateway, and forwarding path"
	}
	if sec.conn == nil && secName == "IPv4" && o.hasGlobalUnicast(true) {
		extra = append(extra, "IPv4 address configured but no IPv4 egress (black-holed)")
		r.Cause = FamilyCauseIPv4Unreachable
		r.causeFamily = counterfactualIPv4
		r.Fix = "check the IPv4 default route, gateway, and forwarding path"
	}
	applyDialWarnings(&r, prim.rtt, extra...)
	r.Attempts = append(prim.attempts, sec.attempts...)
	return r
}

// failedRouteCause chooses the strongest route fact proved by any failed
// family. A routed failure is more useful than an unrelated family's missing
// default, and the order of endpoint candidates cannot decide the repair.
func failedRouteCause(classify func(net.IP) string, families ...[]net.IP) (cause, family string) {
	priority := func(cause string) int {
		switch cause {
		case RouteCausePreferredPathFailed:
			return 4
		case RouteCauseGatewayUnreachable:
			return 3
		case RouteCauseSelectedPathFailed:
			return 2
		case RouteCauseNoDefaultRoute:
			return 1
		default:
			return 0
		}
	}
	best := -1
	for _, ips := range families {
		if len(ips) == 0 {
			continue
		}
		candidate := classify(ips[0])
		if candidate == "" {
			continue
		}
		rank := priority(candidate)
		switch {
		case rank > best:
			cause, family, best = candidate, routeFamily(ips[0]), rank
		case rank == best && candidate == cause:
			// The same fact held for both families, so neither alone owns it.
			family = ""
		}
	}
	return cause, family
}
