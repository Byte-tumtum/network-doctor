//go:build darwin

package diagnostic

import (
	"net"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// inet builds the RIB address form the kernel uses for this family, so a test
// route can be written the way the routing socket reports one.
func inet(af int, ip string) route.Addr {
	parsed := net.ParseIP(ip)
	if af == unix.AF_INET {
		addr := &route.Inet4Addr{}
		copy(addr.IP[:], parsed.To4())
		return addr
	}
	addr := &route.Inet6Addr{}
	copy(addr.IP[:], parsed.To16())
	return addr
}

// defaultRoute is one entry of a NET_RT_DUMP reply: destination and netmask
// both unspecified, with whatever gateway the caller wants tested.
func defaultRoute(af, index, flags int, gateway route.Addr) *route.RouteMessage {
	return &route.RouteMessage{
		Version: unix.RTM_VERSION, Type: unix.RTM_GET, Flags: flags | unix.RTF_UP, Index: index,
		Addrs: []route.Addr{unix.RTAX_DST: inet(af, unspecified(af)), unix.RTAX_GATEWAY: gateway,
			unix.RTAX_NETMASK: inet(af, unspecified(af))},
	}
}

// neighbor is one entry of a NET_RT_FLAGS/RTF_LLINFO reply: the neighbor's
// address as the destination, its link-layer address as the gateway.
func neighbor(af, index int, ip string, hardware []byte) *route.RouteMessage {
	return &route.RouteMessage{
		Version: unix.RTM_VERSION, Type: unix.RTM_GET, Flags: unix.RTF_UP | unix.RTF_HOST | unix.RTF_LLINFO, Index: index,
		Addrs: []route.Addr{unix.RTAX_DST: inet(af, ip),
			unix.RTAX_GATEWAY: &route.LinkAddr{Index: index, Name: "en0", Addr: hardware}},
	}
}

func unspecified(af int) string {
	if af == unix.AF_INET {
		return "0.0.0.0"
	}
	return "::"
}

func TestRouteFailureCauseFromRIB(t *testing.T) {
	mac := []byte{0x02, 0x00, 0x5e, 0x10, 0x00, 0x01}
	tests := []struct {
		name      string
		af        int
		routes    []route.Message
		neighbors []route.Message
		want      string
	}{
		{"an empty table is no default route", unix.AF_INET, nil, nil, RouteCauseNoDefaultRoute},
		{"only a subnet route is no default route", unix.AF_INET, []route.Message{
			&route.RouteMessage{Flags: unix.RTF_UP, Index: 4,
				Addrs: []route.Addr{unix.RTAX_DST: inet(unix.AF_INET, "10.77.1.0"),
					unix.RTAX_GATEWAY: &route.LinkAddr{Index: 4}, unix.RTAX_NETMASK: inet(unix.AF_INET, "255.255.255.0")}},
		}, nil, RouteCauseNoDefaultRoute},
		{"a down default route does not count", unix.AF_INET, []route.Message{
			&route.RouteMessage{Type: unix.RTM_GET, Index: 4,
				Addrs: []route.Addr{unix.RTAX_DST: inet(unix.AF_INET, "0.0.0.0"),
					unix.RTAX_GATEWAY: inet(unix.AF_INET, "10.77.1.1"), unix.RTAX_NETMASK: inet(unix.AF_INET, "0.0.0.0")}},
		}, nil, RouteCauseNoDefaultRoute},
		{"scoped copies are not the route an unbound socket uses", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY|unix.RTF_IFSCOPE, inet(unix.AF_INET, "10.77.1.1")),
		}, nil, RouteCauseNoDefaultRoute},
		{"a resolved gateway leaves the neighbour unaccused", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, []route.Message{neighbor(unix.AF_INET, 4, "10.77.1.1", mac)}, RouteCauseSelectedPathFailed},
		{"an incomplete ARP entry is a dead gateway", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, []route.Message{neighbor(unix.AF_INET, 4, "10.77.1.1", nil)}, RouteCauseGatewayUnreachable},
		{"an all-zero hardware address is a dead gateway", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, []route.Message{neighbor(unix.AF_INET, 4, "10.77.1.1", make([]byte, 6))}, RouteCauseGatewayUnreachable},
		{"an absent ARP entry proves nothing", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, nil, RouteCauseSelectedPathFailed},
		{"a dead neighbour on another interface is not this gateway", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, []route.Message{neighbor(unix.AF_INET, 9, "10.77.1.1", nil)}, RouteCauseSelectedPathFailed},
		{"an on-link default route has no next hop to resolve", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, 0, &route.LinkAddr{Index: 4, Name: "utun3"}),
		}, []route.Message{neighbor(unix.AF_INET, 4, "10.77.1.1", nil)}, RouteCauseSelectedPathFailed},
		{"two unscoped defaults leave no gateway provable", unix.AF_INET, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
			defaultRoute(unix.AF_INET, 9, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.3.1")),
		}, []route.Message{neighbor(unix.AF_INET, 4, "10.77.1.1", nil)}, RouteCauseSelectedPathFailed},
		{"IPv6 has no default route", unix.AF_INET6, nil, nil, RouteCauseNoDefaultRoute},
		{"an incomplete NDP entry is a dead IPv6 gateway", unix.AF_INET6, []route.Message{
			defaultRoute(unix.AF_INET6, 4, unix.RTF_GATEWAY, inet(unix.AF_INET6, "fe80::1")),
		}, []route.Message{neighbor(unix.AF_INET6, 4, "fe80::1", nil)}, RouteCauseGatewayUnreachable},
		{"a resolved NDP entry leaves the IPv6 gateway unaccused", unix.AF_INET6, []route.Message{
			defaultRoute(unix.AF_INET6, 4, unix.RTF_GATEWAY, inet(unix.AF_INET6, "fe80::1")),
		}, []route.Message{neighbor(unix.AF_INET6, 4, "fe80::1", mac)}, RouteCauseSelectedPathFailed},
		{"an IPv4 default route is not an IPv6 default route", unix.AF_INET6, []route.Message{
			defaultRoute(unix.AF_INET, 4, unix.RTF_GATEWAY, inet(unix.AF_INET, "10.77.1.1")),
		}, nil, RouteCauseNoDefaultRoute},
		{"messages that are not route messages are ignored", unix.AF_INET,
			[]route.Message{&route.InterfaceMessage{Index: 4}}, nil, RouteCauseNoDefaultRoute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeFailureCauseFromRIB(tc.af, tc.routes, tc.neighbors); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRouteFailureCauseAddressFamilyGuard pins the only judgement made before
// any kernel table is read: an address of neither family gets no diagnosis.
func TestRouteFailureCauseAddressFamilyGuard(t *testing.T) {
	if got := routeFailureCause(net.IP{1, 2, 3}); got != "" {
		t.Errorf("cause for a malformed address = %q, want empty", got)
	}
}

// TestDarwinRoutingSysctlAvailable is the availability half of this file: the
// unit tests above prove the reasoning, and this proves the two sysctl dumps
// the reasoning is fed by still parse on the macOS actually running the test.
// It reads local kernel state only and asserts nothing about its contents, so
// it holds on a laptop, a CI runner, and a machine with no network at all.
func TestDarwinRoutingSysctlAvailable(t *testing.T) {
	for _, af := range []int{unix.AF_INET, unix.AF_INET6} {
		if _, err := fetchRIB(af, route.RIBTypeRoute, 0); err != nil {
			t.Errorf("routing table dump for family %d: %v", af, err)
		}
		if _, err := fetchRIB(af, ribTypeLinkInfo, unix.RTF_LLINFO); err != nil {
			t.Errorf("neighbour cache dump for family %d: %v", af, err)
		}
	}
}
