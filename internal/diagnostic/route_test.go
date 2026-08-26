package diagnostic

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

// decisionTo builds a decision the way the platform collectors do, so a test states
// only the fields it is about.
func decisionTo(dst, iface, prefix string) RouteDecision {
	r := RouteDecision{Destination: net.ParseIP(dst), Iface: iface, Tunnel: TunnelDirect}
	r.Family = routeFamily(r.Destination)
	if prefix != "" {
		r.Prefix = netip.MustParsePrefix(prefix)
	}
	return r
}

func tunneled(r RouteDecision, kind string) RouteDecision {
	r.Tunnel, r.TunnelKind = TunnelKnown, kind
	return r
}

// An interface kind the operating system itself reported settles the question
// in both directions. Shape alone never gets past "likely", because the shape
// of a tunnel is also the shape of a mobile broadband modem.
func TestClassifyTunnelTrustsTheKernelKindAndGuessesOnlyFromShape(t *testing.T) {
	cases := []struct {
		name  string
		facts ifaceFacts
		want  TunnelState
		kind  string
	}{
		{"wireguard", ifaceFacts{Name: "wg0", Kind: "wireguard", PointToPoint: true, NoLinkLayer: true}, TunnelKnown, "wireguard"},
		{"tun", ifaceFacts{Name: "tun0", Kind: "tun", NoLinkLayer: true}, TunnelKnown, "tun"},
		{"gre", ifaceFacts{Name: "gre1", Kind: "gre"}, TunnelKnown, "gre"},
		{"bridge is virtual but not a tunnel", ifaceFacts{Name: "br0", Kind: "bridge"}, TunnelDirect, ""},
		{"vlan is virtual but not a tunnel", ifaceFacts{Name: "eth0.7", Kind: "vlan"}, TunnelDirect, ""},
		{"ordinary ethernet", ifaceFacts{Name: "eth0"}, TunnelDirect, ""},
		{"loopback", ifaceFacts{Name: "lo", Loopback: true, NoLinkLayer: true}, TunnelDirect, ""},
		{"unnamed interface is never classified", ifaceFacts{}, TunnelUnknown, ""},
		{"unnamed kind, tunnel shape", ifaceFacts{Name: "utun3", PointToPoint: true, NoLinkLayer: true}, TunnelLikely, ""},
		{"unnamed kind, no link layer", ifaceFacts{Name: "ppp0", NoLinkLayer: true}, TunnelLikely, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, kind := classifyTunnel(c.facts)
			if state != c.want || kind != c.kind {
				t.Errorf("classifyTunnel(%+v) = %q/%q, want %q/%q", c.facts, state, kind, c.want, c.kind)
			}
		})
	}
}

// TunnelUnknown is not "no tunnel", and nothing may read it as one.
func TestUnknownTunnelStateIsNotDirect(t *testing.T) {
	if (RouteDecision{Tunnel: TunnelUnknown}).Tunneled() {
		t.Error("an unclassified interface reports as tunneled")
	}
	if state := splitTunnelState(RouteDecision{Iface: "wg0", Tunnel: TunnelKnown}, RouteDecision{Iface: "eth0"}); state != "" {
		t.Errorf("split state against an unclassified reference = %q, want none", state)
	}
	if _, known := pathsDiffer(RouteDecision{Iface: "eth0", Tunnel: TunnelUnknown}, RouteDecision{Iface: "eth0", Tunnel: TunnelKnown}); !known {
		t.Error("two decisions on the same named interface should still be comparable")
	}
	differ, known := pathsDiffer(RouteDecision{Iface: "eth0", Tunnel: TunnelUnknown}, RouteDecision{Iface: "eth0", Tunnel: TunnelKnown})
	if differ {
		t.Error("an unknown tunnel state was reported as a split")
	}
	_ = known
}

// The reason a route won, in every shape the platforms report. Nothing here is
// re-derived from a routing table: a reason exists only where the kernel's own
// answer supports it.
func TestRouteReasonExplainsTheSelection(t *testing.T) {
	defaultRef := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	cases := []struct {
		name      string
		decision  RouteDecision
		reference RouteDecision
		want      RouteReason
	}{
		{"default route", decisionTo("93.184.216.34", "eth0", "0.0.0.0/0"), defaultRef, RouteReasonDefault},
		{"more specific beats the default", decisionTo("10.20.0.5", "wg0", "10.20.0.0/16"), defaultRef, RouteReasonMoreSpecific},
		{"on-link", func() RouteDecision {
			r := decisionTo("192.168.1.5", "eth0", "192.168.1.0/24")
			return r
		}(), defaultRef, RouteReasonOnLink},
		{"host route", func() RouteDecision {
			r := decisionTo("10.9.9.9", "wg0", "10.9.9.9/32")
			r.Gateway = net.ParseIP("10.0.0.1")
			return r
		}(), defaultRef, RouteReasonHostRoute},
		{"no route", RouteDecision{Unreachable: true}, defaultRef, RouteReasonNoRoute},
		// Linux names no matched entry, so these are the shapes that platform
		// produces: the answer comes from comparing the two kernel decisions.
		{"no prefix, no next hop", decisionTo("93.184.216.34", "eth0", ""), defaultRef, RouteReasonOnLink},
		{"no prefix, same way out as general traffic", func() RouteDecision {
			r := decisionTo("93.184.216.34", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), func() RouteDecision {
			r := decisionTo("1.1.1.1", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), RouteReasonSamePathAsDefault},
		{"no prefix, a different way out from general traffic", func() RouteDecision {
			r := decisionTo("10.20.0.5", "wg0", "")
			r.Gateway = net.ParseIP("10.20.0.1")
			return r
		}(), func() RouteDecision {
			r := decisionTo("1.1.1.1", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), RouteReasonDiffersFromDefault},
		{"no prefix, a different next hop on the same interface", func() RouteDecision {
			r := decisionTo("10.20.0.5", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.9")
			return r
		}(), func() RouteDecision {
			r := decisionTo("1.1.1.1", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), RouteReasonDiffersFromDefault},
		{"no prefix and nothing to compare against", func() RouteDecision {
			r := decisionTo("93.184.216.34", "eth0", "")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), RouteDecision{}, RouteReasonUnknown},
		{"no interface at all", RouteDecision{Destination: net.ParseIP("93.184.216.34")}, defaultRef, RouteReasonUnknown},
		{"metric decided it", func() RouteDecision {
			r := decisionTo("93.184.216.34", "eth0", "0.0.0.0/0")
			r.Metric, r.MetricKnown = 100, true
			r.Competing = []CompetingRoute{{Iface: "wlan0", Metric: 600}}
			return r
		}(), RouteDecision{}, RouteReasonMetric},
		// With no decision for general traffic to compare against, a gatewayless
		// prefix is only what it plainly is. Nothing claims it beat a default
		// route that was never observed.
		{"more specific, but nothing to have beaten", decisionTo("10.20.0.5", "wg0", "10.20.0.0/16"), RouteDecision{}, RouteReasonOnLink},
		{"attached network on the interface general traffic uses", decisionTo("192.168.1.5", "eth0", "192.168.1.0/24"), defaultRef, RouteReasonOnLink},
		// A named entry narrower than a default route is more specific
		// whatever path it points down. The platform said which entry matched,
		// so this is read off the entry and not off the interfaces agreeing.
		{"a narrower named entry down the interface general traffic uses", func() RouteDecision {
			r := decisionTo("10.20.0.5", "eth0", "10.20.0.0/16")
			r.Gateway = net.ParseIP("192.168.1.1")
			return r
		}(), defaultRef, RouteReasonMoreSpecific},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := routeReason(c.decision, c.reference); got != c.want {
				t.Errorf("routeReason = %q, want %q", got, c.want)
			}
		})
	}
}

// The reason a Linux-shaped decision may never claim. This is a real
// namespace reproduced as a table: a policy rule sends 203.0.113.5 to table
// 100, whose winning entry is itself a default route out of another interface,
// so the destination leaves by a different path with no narrower prefix
// anywhere in the decision. The kernel reports only the interface, the next
// hop, and the table, which is exactly what a genuine more-specific route
// would report, so nothing here can tell the two apart and the reason must not
// pretend otherwise.
func TestADifferentPathIsNotEvidenceOfAMoreSpecificPrefix(t *testing.T) {
	reference := decisionTo("1.1.1.1", "veth0", "")
	reference.Gateway = net.ParseIP("10.0.0.1")
	policyRouted := decisionTo("203.0.113.5", "veth1", "")
	policyRouted.Gateway, policyRouted.Table = net.ParseIP("10.1.0.1"), "table 100"

	got := routeReason(policyRouted, reference)
	if got == RouteReasonMoreSpecific || got == RouteReasonDefault || got == RouteReasonHostRoute {
		t.Fatalf("routeReason = %q, which claims a matched entry the kernel never named", got)
	}
	if got != RouteReasonDiffersFromDefault {
		t.Errorf("routeReason = %q, want %q", got, RouteReasonDiffersFromDefault)
	}
	// And the mirror image: agreeing with the general path does not prove the
	// default route is what matched, since a narrower entry can point exactly
	// the same way.
	narrowButSameWay := decisionTo("10.9.0.1", "veth0", "")
	narrowButSameWay.Gateway = net.ParseIP("10.0.0.1")
	if got := routeReason(narrowButSameWay, reference); got != RouteReasonSamePathAsDefault {
		t.Errorf("routeReason = %q, want %q", got, RouteReasonSamePathAsDefault)
	}
}

// The invariant behind the whole reason vocabulary, checked over every shape a
// decision can take rather than over the handful the table above lists: a
// reason that names which entry matched may only be reported when the platform
// actually named one. Without a prefix, the strongest thing two kernel answers
// support is whether the paths agree, and no combination of interfaces, next
// hops, tables, metrics, or competitors may be talked up into more than that.
func TestAnEntryLevelReasonNeedsANamedEntry(t *testing.T) {
	entryLevel := []RouteReason{RouteReasonDefault, RouteReasonMoreSpecific, RouteReasonHostRoute}
	gateways := []net.IP{nil, net.ParseIP("192.168.1.1"), net.ParseIP("10.1.0.1")}
	ifaces := []string{"", "eth0", "wg0"}
	tables := []string{"", "table 100"}
	references := []RouteDecision{{}, decisionTo("1.1.1.1", "eth0", "0.0.0.0/0"), func() RouteDecision {
		r := decisionTo("1.1.1.1", "eth0", "")
		r.Gateway = net.ParseIP("192.168.1.1")
		return r
	}()}
	for _, iface := range ifaces {
		for _, gw := range gateways {
			for _, table := range tables {
				for _, metricKnown := range []bool{false, true} {
					for _, reference := range references {
						r := RouteDecision{
							Destination: net.ParseIP("203.0.113.5"), Family: counterfactualIPv4,
							Iface: iface, Gateway: gw, Table: table, MetricKnown: metricKnown,
						}
						if got := routeReason(r, reference); slices.Contains(entryLevel, got) {
							t.Fatalf("routeReason(%+v, %+v) = %q, which names an entry the platform never did",
								r, reference, got)
						}
					}
				}
			}
		}
	}
}

// A split tunnel in both directions, and the two cases that are not one.
func TestSplitTunnelStateNamesTheDirection(t *testing.T) {
	direct := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	tunnel := tunneled(decisionTo("1.1.1.1", "wg0", "0.0.0.0/0"), "wireguard")
	cases := []struct {
		name              string
		target, reference RouteDecision
		want              string
	}{
		{"target enters the tunnel", tunneled(decisionTo("10.20.0.5", "wg0", "10.20.0.0/16"), "wireguard"), direct, SplitTargetTunneled},
		{"target bypasses the tunnel", decisionTo("93.184.216.34", "eth0", "0.0.0.0/0"), tunnel, SplitTargetBypassesTunnel},
		{"everything is tunneled, nothing to explain", tunnel, tunnel, ""},
		{"nothing is tunneled", direct, direct, ""},
		{"no reference path", tunnel, RouteDecision{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := splitTunnelState(c.target, c.reference); got != c.want {
				t.Errorf("splitTunnelState = %q, want %q", got, c.want)
			}
		})
	}
}

// The two families of one target reaching the network by different interfaces.
// A single-stack host has nothing to compare and must say so rather than
// reporting agreement.
func TestFamilyPathsDifferNeedsBothFamilies(t *testing.T) {
	split := []RouteDecision{decisionTo("93.184.216.34", "eth0", "0.0.0.0/0"), tunneled(decisionTo("2606:2800::1", "wg0", "::/0"), "wireguard")}
	if differ, known := familyPathsDiffer(split); !known || !differ {
		t.Errorf("families on different interfaces = %v/%v, want a known difference", differ, known)
	}
	same := []RouteDecision{decisionTo("93.184.216.34", "eth0", "0.0.0.0/0"), decisionTo("2606:2800::1", "eth0", "::/0")}
	if differ, known := familyPathsDiffer(same); !known || differ {
		t.Errorf("families on one interface = %v/%v, want a known agreement", differ, known)
	}
	if _, known := familyPathsDiffer(same[:1]); known {
		t.Error("a single-stack run claimed to know whether the families differ")
	}
}

// Interface MTU only, and only when the selected path is the narrower one.
func TestNarrowerPathMTUIsInterfaceMTUOnly(t *testing.T) {
	narrow, wide := decisionTo("10.20.0.5", "wg0", "10.20.0.0/16"), decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	narrow.MTU, wide.MTU = 1420, 1500
	if mtu, ok := narrowerPathMTU(narrow, wide); !ok || mtu != 1420 {
		t.Errorf("narrowerPathMTU = %d/%v, want 1420/true", mtu, ok)
	}
	if _, ok := narrowerPathMTU(wide, narrow); ok {
		t.Error("the wider path was reported as narrower")
	}
	if _, ok := narrowerPathMTU(RouteDecision{Iface: "wg0"}, wide); ok {
		t.Error("an unknown MTU was compared as if it were 0")
	}
}

// One lookup per destination per pass, and a platform that cannot answer is
// remembered as such rather than asked again by every later probe.
func TestRouteCacheAsksTheKernelOncePerDestination(t *testing.T) {
	asked := map[string]int{}
	c := newRouteCache(func(dst net.IP) (RouteDecision, bool) {
		asked[dst.String()]++
		if dst.String() == "203.0.113.9" {
			return RouteDecision{}, false
		}
		return RouteDecision{Iface: "eth0"}, true
	})
	for range 3 {
		if _, ok := c.get(net.ParseIP("93.184.216.34")); !ok {
			t.Fatal("cached lookup lost its answer")
		}
		if _, ok := c.get(net.ParseIP("203.0.113.9")); ok {
			t.Fatal("an unanswerable lookup reported an answer")
		}
	}
	if asked["93.184.216.34"] != 1 || asked["203.0.113.9"] != 1 {
		t.Errorf("lookups per destination = %v, want one each", asked)
	}
	if _, ok := (*routeCache)(nil).get(net.ParseIP("93.184.216.34")); ok {
		t.Error("a nil cache answered a lookup")
	}
}

// The cache fills the identity fields, so a collector that reports only what
// the kernel told it still produces a decision keyed to its destination.
func TestRouteCacheStampsDestinationAndFamily(t *testing.T) {
	c := newRouteCache(func(net.IP) (RouteDecision, bool) { return RouteDecision{Iface: "wg0"}, true })
	v6, _ := c.get(net.ParseIP("2606:2800::1"))
	if v6.Family != counterfactualIPv6 || v6.Destination.String() != "2606:2800::1" {
		t.Errorf("IPv6 decision = %+v, want the destination and family stamped", v6)
	}
	v4, _ := c.get(net.ParseIP("93.184.216.34"))
	if v4.Family != counterfactualIPv4 {
		t.Errorf("IPv4 family = %q, want %q", v4.Family, counterfactualIPv4)
	}
}

// Route lookups are bounded and deduplicated, so a hostname with a long answer
// cannot turn route intelligence into a scan of the address space.
func TestRouteDecisionsAreBoundedAndDeduplicated(t *testing.T) {
	var asked int
	o := &netops{routes: newRouteCache(func(net.IP) (RouteDecision, bool) {
		asked++
		return RouteDecision{Iface: "eth0"}, true
	})}
	var dsts []net.IP
	for i := range maxRouteLookups + 5 {
		dsts = append(dsts, net.IPv4(198, 51, 100, byte(i)))
	}
	dsts = append(dsts, dsts[0], nil)
	if got := len(o.routeDecisions(dsts...)); got != maxRouteLookups {
		t.Errorf("recorded %d decisions, want the cap of %d", got, maxRouteLookups)
	}
	if asked != maxRouteLookups {
		t.Errorf("asked the kernel %d times, want %d", asked, maxRouteLookups)
	}
	if (&netops{}).routeDecisions(dsts...) != nil {
		t.Error("a run with no route collector produced decisions")
	}
}

// Competing routes explain a decision and never mirror a table: one default
// route is not a competition, and a platform reporting no metric cannot say
// which of several would win.
func TestCompetingDefaultsStaySilentWithoutARealCompetition(t *testing.T) {
	selected := decisionTo("1.1.1.1", "eth0", "0.0.0.0/0")
	selected.Metric, selected.MetricKnown = 100, true
	many := []defaultRouteState{{iface: "eth0", metric: 100}}
	for i := range maxCompetingRoutes + 3 {
		many = append(many, defaultRouteState{iface: "wlan0", metric: 600 + i})
	}
	o := &netops{defaultRoutes: func(string) []defaultRouteState { return many }}
	got := o.competingDefaults(selected)
	if len(got) != maxCompetingRoutes {
		t.Errorf("recorded %d competitors, want the cap of %d", len(got), maxCompetingRoutes)
	}
	if got[0].Metric != 600 {
		t.Errorf("competitors are not ordered by preference: %+v", got)
	}
	only := &netops{defaultRoutes: func(string) []defaultRouteState { return many[:1] }}
	if c := only.competingDefaults(selected); c != nil {
		t.Errorf("one default route reported %d competitors", len(c))
	}
	noMetric := selected
	noMetric.MetricKnown = false
	if c := o.competingDefaults(noMetric); c != nil {
		t.Errorf("a platform with no metrics named %d competitors", len(c))
	}
	notDefault := decisionTo("10.20.0.5", "wg0", "10.20.0.0/16")
	notDefault.MetricKnown = true
	if c := o.competingDefaults(notDefault); c != nil {
		t.Errorf("a non-default route named %d competing defaults", len(c))
	}
}

func TestResolverIPReadsTheDialTargetAndNothingElse(t *testing.T) {
	cases := map[string]string{
		"192.0.2.53:53":     "192.0.2.53",
		"[2001:db8::1]:53":  "2001:db8::1",
		"192.0.2.53":        "192.0.2.53",
		"":                  "",
		"resolver.local:53": "",
		// A local stub is reached over lo by definition, so its path is
		// always different from the application's and never means what a
		// different resolver path is supposed to mean. The stub's own
		// upstream path is the interesting one and is not visible from here,
		// so no resolver path is recorded at all.
		"127.0.0.53:53":  "",
		"127.0.0.1:5353": "",
		"[::1]:53":       "",
	}
	for server, want := range cases {
		got := resolverIP(server)
		if want == "" {
			if got != nil {
				t.Errorf("resolverIP(%q) = %v, want none", server, got)
			}
			continue
		}
		if got == nil || got.String() != want {
			t.Errorf("resolverIP(%q) = %v, want %s", server, got, want)
		}
	}
}

// The route for the address the connection actually used, which is not
// necessarily the first one resolved.
func TestSelectedTargetRouteFollowsTheSelectedAddress(t *testing.T) {
	routes := []RouteDecision{decisionTo("93.184.216.34", "eth0", "0.0.0.0/0"), tunneled(decisionTo("198.51.100.7", "wg0", "198.51.100.0/24"), "wireguard")}
	res := map[ProbeID]ProbeResult{ProbeTargetTCP: {Routes: routes, SelectedIP: net.ParseIP("198.51.100.7")}}
	got, ok := selectedTargetRoute(res)
	if !ok || got.Iface != "wg0" {
		t.Errorf("selected route = %+v/%v, want the wg0 decision", got, ok)
	}
	// A run where every address failed still chose a path for the first one.
	res[ProbeTargetTCP] = ProbeResult{Routes: routes}
	if got, ok := selectedTargetRoute(res); !ok || got.Iface != "eth0" {
		t.Errorf("fallback route = %+v/%v, want the first decision", got, ok)
	}
	if _, ok := selectedTargetRoute(map[ProbeID]ProbeResult{}); ok {
		t.Error("a run with no routes produced a selected route")
	}
}

// The details line is display text and says only what the decision holds.
func TestRouteSummaryReportsOnlyWhatIsKnown(t *testing.T) {
	r := tunneled(decisionTo("10.20.0.5", "wg0", "10.20.0.0/16"), "wireguard")
	if got, want := r.Summary(), "10.20.0.0/16 dev wg0 (wireguard)"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	gw := decisionTo("93.184.216.34", "eth0", "0.0.0.0/0")
	gw.Gateway = net.ParseIP("192.168.1.1")
	if got, want := gw.Summary(), "0.0.0.0/0 dev eth0 via 192.168.1.1"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got, want := (RouteDecision{Unreachable: true}).Summary(), "no route"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got := (RouteDecision{}).Summary(); got != "" {
		t.Errorf("an empty decision rendered %q, want nothing", got)
	}
}
