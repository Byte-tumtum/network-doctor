//go:build darwin

package diagnostic

import (
	"net"
	"strconv"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// ribTypeLinkInfo is the sysctl RIB selector that dumps routing entries
// matching a flag mask. Asked for RTF_LLINFO it returns the link-layer
// resolution cache, which is the same table arp(8) and ndp(8) print and the
// Darwin equivalent of Linux's /proc/net/arp. x/net/route names only the
// whole-table and interface-list selectors, so this one is spelled out.
const ribTypeLinkInfo = route.RIBType(unix.NET_RT_FLAGS)

// routeFailureCause reads the kernel routing table through the PF_ROUTE
// sysctl, the interface every BSD userland routing tool uses. It is a plain
// read of kernel state: unprivileged, bounded by the size of the table, and
// free of any command output to parse.
func routeFailureCause(destination net.IP) string {
	af := unix.AF_INET
	if destination.To4() == nil {
		if destination.To16() == nil {
			return ""
		}
		af = unix.AF_INET6
	}
	routes, err := fetchRIB(af, route.RIBTypeRoute, 0)
	if err != nil {
		return ""
	}
	// A neighbor cache we could not read is not evidence of a healthy
	// neighbor, so a failed fetch leaves the list empty and no gateway is
	// accused of anything.
	neighbors, _ := fetchRIB(af, ribTypeLinkInfo, unix.RTF_LLINFO)
	return routeFailureCauseFromRIB(af, routes, neighbors)
}

func fetchRIB(af int, typ route.RIBType, arg int) ([]route.Message, error) {
	raw, err := route.FetchRIB(af, typ, arg)
	if err != nil {
		return nil, err
	}
	return route.ParseRIB(typ, raw)
}

func routeFailureCauseFromRIB(af int, routes, neighbors []route.Message) string {
	defaults := darwinDefaultRoutes(af, routes)
	// Darwin route entries carry no preference metric, so classifyDefaultRoutes
	// sees every default as equal and selects the first. That selection is only
	// trustworthy when there is exactly one unscoped default; with several we
	// cannot say which one an unbound socket would use, so no gateway is named.
	var gatewayFailed func(defaultRouteState) bool
	if len(defaults) == 1 {
		gatewayFailed = func(r defaultRouteState) bool {
			return r.gateway != nil && darwinNeighborUnresolved(af, neighbors, r.gateway, r.iface)
		}
	}
	return classifyDefaultRoutes(defaults, gatewayFailed)
}

// darwinDefaultRoutes keeps the up, non-host routes whose destination and
// netmask are both the family's unspecified address.
func darwinDefaultRoutes(af int, msgs []route.Message) []defaultRouteState {
	var out []defaultRouteState
	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok || rm.Flags&unix.RTF_UP == 0 || rm.Flags&unix.RTF_HOST != 0 {
			continue
		}
		// Darwin's scoped routing installs a per-interface copy of each
		// default route alongside the single unscoped one that traffic from an
		// unbound socket follows. Only the unscoped route describes the path
		// the probe just failed on.
		if rm.Flags&unix.RTF_IFSCOPE != 0 {
			continue
		}
		if !isUnspecifiedRouteAddr(af, addrAt(rm.Addrs, unix.RTAX_DST)) {
			continue
		}
		// The netmask is absent on some default entries and a zero-length
		// sockaddr on others, which parses to the unspecified address.
		if mask := addrAt(rm.Addrs, unix.RTAX_NETMASK); mask != nil && !isUnspecifiedRouteAddr(af, mask) {
			continue
		}
		// A link-layer gateway means an on-link default route with no next-hop
		// address to resolve, and routeAddrIP reports that as no gateway.
		out = append(out, defaultRouteState{iface: strconv.Itoa(rm.Index),
			gateway: routeAddrIP(af, addrAt(rm.Addrs, unix.RTAX_GATEWAY))})
	}
	return out
}

// darwinNeighborUnresolved reports that the cache holds an entry for this
// gateway on this interface and that entry has no link-layer address, which is
// how an incomplete ARP or NDP entry appears in the RIB. An absent entry proves
// nothing and is not a failure, matching the Linux reading of /proc/net/arp.
func darwinNeighborUnresolved(af int, msgs []route.Message, gateway net.IP, iface string) bool {
	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok || strconv.Itoa(rm.Index) != iface ||
			!gateway.Equal(routeAddrIP(af, addrAt(rm.Addrs, unix.RTAX_DST))) {
			continue
		}
		link, ok := addrAt(rm.Addrs, unix.RTAX_GATEWAY).(*route.LinkAddr)
		if !ok {
			return false
		}
		for _, b := range link.Addr {
			if b != 0 {
				return false
			}
		}
		return true
	}
	return false
}

func addrAt(addrs []route.Addr, i int) route.Addr {
	if i < 0 || i >= len(addrs) {
		return nil
	}
	return addrs[i]
}

// routeAddrIP returns the address only when it belongs to the family being
// diagnosed and names a real host. The unspecified address is reported as no
// address, since it is how the kernel spells "no next hop here".
func routeAddrIP(af int, addr route.Addr) net.IP {
	var ip net.IP
	switch v := addr.(type) {
	case *route.Inet4Addr:
		if af != unix.AF_INET {
			return nil
		}
		ip = append(net.IP(nil), v.IP[:]...)
	case *route.Inet6Addr:
		if af != unix.AF_INET6 {
			return nil
		}
		ip = append(net.IP(nil), v.IP[:]...)
	default:
		return nil
	}
	if ip.IsUnspecified() {
		return nil
	}
	return ip
}

func isUnspecifiedRouteAddr(af int, addr route.Addr) bool {
	switch v := addr.(type) {
	case *route.Inet4Addr:
		return af == unix.AF_INET && net.IP(v.IP[:]).IsUnspecified()
	case *route.Inet6Addr:
		return af == unix.AF_INET6 && net.IP(v.IP[:]).IsUnspecified()
	}
	return false
}
