//go:build darwin

package diagnostic

import (
	"errors"
	"math/bits"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// Route intelligence on Darwin asks the kernel the question `route -n get`
// asks: an RTM_GET written to a PF_ROUTE socket, answered with the entry the
// kernel would use. That is the same reason Linux uses netlink here. Walking
// the whole RIB and re-running BSD's selection would be a second copy of the
// answer the kernel will already give, and one that could disagree with it.
//
// A PF_ROUTE socket is unprivileged for reads. Writing an RTM_GET is a query,
// not a modification: the kernel answers it and changes nothing.

// routeSocketTimeout bounds one exchange with the routing socket.
const routeSocketTimeout = 500 * time.Millisecond

// routeReplyMax bounds one reply read off the routing socket.
const routeReplyMax = 8 << 10

func lookupRouteDecision(dst net.IP) (RouteDecision, bool) {
	addr, ok := netip.AddrFromSlice(dst)
	if !ok {
		return RouteDecision{}, false
	}
	addr = addr.Unmap()
	af := unix.AF_INET
	if !addr.Is4() {
		af = unix.AF_INET6
	}
	reply, err := routeSocketGet(af, addr)
	if err != nil {
		if unreachableRouteError(err) {
			return RouteDecision{Unreachable: true}, true
		}
		return RouteDecision{}, false
	}
	return parseRouteGetReply(af, reply, addr)
}

// parseRouteGetReply reads the RTM_GET answer into a decision. Darwin route
// entries carry no preference metric, so MetricKnown stays false rather than
// reporting a zero that would compare as "best".
func parseRouteGetReply(af int, msgs []route.Message, dst netip.Addr) (RouteDecision, bool) {
	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok || rm.Type != unix.RTM_GET {
			continue
		}
		if rm.Err != nil {
			if unreachableRouteError(rm.Err) {
				return RouteDecision{Unreachable: true}, true
			}
			return RouteDecision{}, false
		}
		out := RouteDecision{Gateway: routeAddrIP(af, addrAt(rm.Addrs, unix.RTAX_GATEWAY))}
		out.Prefix = darwinMatchedPrefix(af, rm, dst)
		if ifi, err := net.InterfaceByIndex(rm.Index); err == nil {
			out.Iface, out.MTU = ifi.Name, ifi.MTU
			out.Tunnel, out.TunnelKind = classifyTunnel(ifaceFacts{
				Name:         ifi.Name,
				PointToPoint: ifi.Flags&net.FlagPointToPoint != 0,
				Loopback:     ifi.Flags&net.FlagLoopback != 0,
				NoLinkLayer:  len(ifi.HardwareAddr) == 0,
			})
		}
		// RTAX_IFA is the address of the selected interface the kernel would
		// source from, which is the closest thing this interface gives to a
		// preferred source. Absent on some replies, and then left unknown.
		out.Source = routeAddrIP(af, addrAt(rm.Addrs, unix.RTAX_IFA))
		return out, true
	}
	return RouteDecision{}, false
}

// darwinMatchedPrefix reconstructs the route entry that matched, from the
// destination and netmask the reply carries. A reply flagged RTF_HOST matched
// a single address, and one with no usable netmask matched the default route,
// which is how the kernel spells both.
func darwinMatchedPrefix(af int, rm *route.RouteMessage, dst netip.Addr) netip.Prefix {
	if rm.Flags&unix.RTF_HOST != 0 {
		return netip.PrefixFrom(dst, dst.BitLen())
	}
	prefixBits, ok := maskBits(af, addrAt(rm.Addrs, unix.RTAX_NETMASK))
	if !ok || prefixBits < 0 || prefixBits > dst.BitLen() {
		return netip.Prefix{}
	}
	prefix, err := dst.Prefix(prefixBits)
	if err != nil {
		return netip.Prefix{}
	}
	return prefix
}

// maskBits counts the leading one bits of a netmask sockaddr. A mask with a
// hole in it is not a prefix length and is reported as unusable rather than as
// the count up to the hole.
func maskBits(af int, addr route.Addr) (int, bool) {
	var raw []byte
	switch v := addr.(type) {
	case *route.Inet4Addr:
		if af != unix.AF_INET {
			return 0, false
		}
		raw = v.IP[:]
	case *route.Inet6Addr:
		if af != unix.AF_INET6 {
			return 0, false
		}
		raw = v.IP[:]
	case nil:
		// No netmask at all is how a default route is written.
		return 0, true
	default:
		return 0, false
	}
	length := 0
	for i, b := range raw {
		if b == 0xff {
			length += 8
			continue
		}
		ones := bits.LeadingZeros8(^b)
		if b<<ones != 0 {
			return 0, false
		}
		length += ones
		for _, rest := range raw[i+1:] {
			if rest != 0 {
				return 0, false
			}
		}
		return length, true
	}
	return length, true
}

// routeSocketGet writes one RTM_GET and reads the kernel's answer back. The
// socket is opened per query and closed immediately: a routing socket left
// open receives every routing change on the machine, which is a subscription
// this has no use for.
func routeSocketGet(af int, dst netip.Addr) ([]route.Message, error) {
	var dstAddr route.Addr
	if af == unix.AF_INET {
		dstAddr = &route.Inet4Addr{IP: dst.As4()}
	} else {
		dstAddr = &route.Inet6Addr{IP: dst.As16()}
	}
	seq := int(time.Now().UnixNano() & 0x7fffffff)
	request := &route.RouteMessage{
		Version: unix.RTM_VERSION,
		Type:    unix.RTM_GET,
		Flags:   unix.RTF_UP | unix.RTF_GATEWAY | unix.RTF_STATIC,
		ID:      uintptr(os.Getpid()),
		Seq:     seq,
		Addrs:   []route.Addr{unix.RTAX_DST: dstAddr},
	}
	raw, err := request.Marshal()
	if err != nil {
		return nil, err
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, af)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	unix.CloseOnExec(fd)
	timeout := unix.NsecToTimeval(int64(routeSocketTimeout))
	// The timeout is a guard against a socket that never answers, not a
	// requirement: a kernel that refuses the option still answers this query.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout)
	if _, err := unix.Write(fd, raw); err != nil {
		return nil, err
	}
	// The socket also carries unrelated routing announcements, so read until
	// the reply to this request arrives or the deadline ends the wait.
	deadline := time.Now().Add(routeSocketTimeout)
	buf := make([]byte, routeReplyMax)
	for time.Now().Before(deadline) {
		n, err := unix.Read(fd, buf)
		if err != nil {
			return nil, err
		}
		msgs, err := route.ParseRIB(route.RIBTypeRoute, buf[:n])
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			rm, ok := msg.(*route.RouteMessage)
			if ok && rm.Seq == seq && rm.ID == uintptr(os.Getpid()) {
				return []route.Message{rm}, nil
			}
		}
	}
	return nil, errors.New("no answer from the routing socket")
}

// defaultRoutesFor answers nothing on Darwin. Its route entries carry no
// preference metric, so netdoc cannot say which of several default routes an
// unbound socket would prefer, and naming a competitor it cannot rank would be
// inventing the comparison.
func defaultRoutesFor(string) []defaultRouteState { return nil }

func unreachableRouteError(err error) bool {
	return errors.Is(err, unix.ENETUNREACH) || errors.Is(err, unix.EHOSTUNREACH) ||
		errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENETDOWN)
}
