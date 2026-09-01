//go:build darwin

package diagnostic

import (
	"net"
	"net/netip"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// The reply messages here are built rather than captured, so the parser is
// checked against the wire shape and not against this machine's own routes.

func routeAddrs(dst, gateway, netmask, ifa route.Addr) []route.Addr {
	addrs := make([]route.Addr, unix.RTAX_MAX)
	addrs[unix.RTAX_DST] = dst
	addrs[unix.RTAX_GATEWAY] = gateway
	addrs[unix.RTAX_NETMASK] = netmask
	addrs[unix.RTAX_IFA] = ifa
	return addrs
}

func inet4(a, b, c, d byte) *route.Inet4Addr { return &route.Inet4Addr{IP: [4]byte{a, b, c, d}} }

// A netmask is only a prefix length when it is contiguous. One with a hole is
// reported as unusable rather than as the count up to the hole.
func TestMaskBitsRejectsANonContiguousMask(t *testing.T) {
	cases := []struct {
		name string
		mask route.Addr
		want int
		ok   bool
	}{
		{"full", inet4(255, 255, 255, 255), 32, true},
		{"class C", inet4(255, 255, 255, 0), 24, true},
		{"odd but contiguous", inet4(255, 255, 240, 0), 20, true},
		{"zero", inet4(0, 0, 0, 0), 0, true},
		{"absent mask is the default route", nil, 0, true},
		{"hole in the middle", inet4(255, 0, 255, 0), 0, false},
		{"hole inside a byte", inet4(255, 255, 0b1010_0000, 0), 0, false},
		{"wrong family", &route.Inet6Addr{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := maskBits(unix.AF_INET, c.mask)
			if got != c.want || ok != c.ok {
				t.Errorf("maskBits = %d/%v, want %d/%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseRouteGetReplyReadsTheKernelsDecision(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	rm := &route.RouteMessage{
		Type:  unix.RTM_GET,
		Index: 1,
		Addrs: routeAddrs(inet4(198, 51, 100, 7), inet4(192, 168, 1, 1), inet4(255, 255, 0, 0), inet4(192, 168, 1, 20)),
	}
	got, ok := parseRouteGetReply(unix.AF_INET, []route.Message{rm}, dst)
	if !ok {
		t.Fatal("a well formed reply was not parsed")
	}
	if got.Prefix.String() != "198.51.0.0/16" {
		t.Errorf("prefix = %q, want the /16 the netmask describes", got.Prefix)
	}
	if got.Gateway.String() != "192.168.1.1" || got.Source.String() != "192.168.1.20" {
		t.Errorf("gateway/source = %v/%v, want 192.168.1.1/192.168.1.20", got.Gateway, got.Source)
	}
	// Darwin route entries carry no preference metric, so none is claimed.
	if got.MetricKnown {
		t.Errorf("metric = %d, want none: this platform reports no preference", got.Metric)
	}
}

// RTF_HOST is how the kernel says the entry matched one address.
func TestParseRouteGetReplyReadsAHostRoute(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	rm := &route.RouteMessage{Type: unix.RTM_GET, Flags: unix.RTF_HOST, Addrs: routeAddrs(inet4(198, 51, 100, 7), nil, nil, nil)}
	got, _ := parseRouteGetReply(unix.AF_INET, []route.Message{rm}, dst)
	if got.Prefix.String() != "198.51.100.7/32" {
		t.Errorf("prefix = %q, want a host route", got.Prefix)
	}
	if routeReason(got, RouteDecision{}) != RouteReasonHostRoute {
		t.Errorf("reason = %q, want %q", routeReason(got, RouteDecision{}), RouteReasonHostRoute)
	}
}

// The kernel's own way of saying there is no route, and the failures that mean
// the lookup itself did not work, which must never be recorded as no route.
func TestParseRouteGetReplyReportsUnreachableSeparatelyFromFailure(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	noRoute := &route.RouteMessage{Type: unix.RTM_GET, Err: unix.EHOSTUNREACH}
	got, ok := parseRouteGetReply(unix.AF_INET, []route.Message{noRoute}, dst)
	if !ok || !got.Unreachable {
		t.Errorf("EHOSTUNREACH = %+v/%v, want an unreachable decision", got, ok)
	}
	failed := &route.RouteMessage{Type: unix.RTM_GET, Err: unix.EPERM}
	if _, ok := parseRouteGetReply(unix.AF_INET, []route.Message{failed}, dst); ok {
		t.Error("a failed lookup was recorded as a decision")
	}
	if _, ok := parseRouteGetReply(unix.AF_INET, nil, dst); ok {
		t.Error("an empty reply produced a decision")
	}
}

func TestUnreachableRouteErrorNamesOnlyRoutingFailures(t *testing.T) {
	for _, err := range []error{unix.ENETUNREACH, unix.EHOSTUNREACH, unix.ESRCH, unix.ENETDOWN} {
		if !unreachableRouteError(err) {
			t.Errorf("%v is not treated as an absent route", err)
		}
	}
	for _, err := range []error{unix.EPERM, unix.EACCES, unix.EINVAL, nil} {
		if unreachableRouteError(err) {
			t.Errorf("%v was treated as an absent route", err)
		}
	}
}

// Darwin exposes no route preference, so it names no competing default routes
// rather than guessing which of several would win.
func TestDarwinNamesNoCompetingDefaults(t *testing.T) {
	if got := defaultRoutesFor(counterfactualIPv4); got != nil {
		t.Errorf("defaultRoutesFor = %+v, want none on a platform with no metrics", got)
	}
}

// The real kernel, asked about the loopback address. It needs no privileges
// and no network.
func TestLookupRouteDecisionAnswersForLoopback(t *testing.T) {
	got, ok := lookupRouteDecision(net.ParseIP("127.0.0.1"), nil)
	if !ok {
		t.Skip("the kernel did not answer a route lookup in this environment")
	}
	if got.Unreachable {
		t.Fatalf("loopback reported unreachable: %+v", got)
	}
	if got.Iface == "" {
		t.Errorf("decision = %+v, want a selected interface", got)
	}
}
