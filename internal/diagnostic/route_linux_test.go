//go:build linux

package diagnostic

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

// The tests below build kernel replies byte by byte, so the parser is checked
// against the wire format rather than against whatever this developer's own
// machine happens to route today.

func u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.NativeEndian.PutUint16(b, v)
	return b
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)
	return b
}

// rtMsg lays out an rtmsg header the way the kernel does.
func rtMsg(family, dstLen, table, rtType uint8, attrs ...[]byte) netlinkMessage {
	data := make([]byte, rtMsgLen)
	data[0], data[1], data[4], data[7] = family, dstLen, table, rtType
	for _, a := range attrs {
		data = append(data, a...)
	}
	return netlinkMessage{Type: unix.RTM_NEWROUTE, Data: data}
}

func TestParseRouteReplyReadsTheKernelsDecision(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	msg := rtMsg(unix.AF_INET, 16, unix.RT_TABLE_MAIN, unix.RTN_UNICAST,
		rtAttr(unix.RTA_GATEWAY, net.ParseIP("192.168.1.1").To4()),
		rtAttr(unix.RTA_PREFSRC, net.ParseIP("192.168.1.20").To4()),
		rtAttr(unix.RTA_PRIORITY, u32(100)),
		rtAttr(unix.RTA_TABLE, u32(unix.RT_TABLE_MAIN)),
	)
	got, ok := parseRouteReply([]netlinkMessage{msg}, dst)
	if !ok {
		t.Fatal("a well formed reply was not parsed")
	}
	// The kernel echoes the length the request asked about rather than the
	// entry it matched, so no prefix is claimed on this platform.
	if got.Prefix.IsValid() {
		t.Errorf("prefix = %q, want none: a route lookup reply does not carry the matched entry", got.Prefix)
	}
	if got.Gateway.String() != "192.168.1.1" || got.Source.String() != "192.168.1.20" {
		t.Errorf("gateway/source = %v/%v, want 192.168.1.1/192.168.1.20", got.Gateway, got.Source)
	}
	if !got.MetricKnown || got.Metric != 100 {
		t.Errorf("metric = %d (known %t), want 100", got.Metric, got.MetricKnown)
	}
	if got.Table != "" {
		t.Errorf("table = %q, want the main table reported as unremarkable", got.Table)
	}
	if got.Unreachable {
		t.Error("a unicast route was reported as unreachable")
	}
}

// A metric of 0 is a real metric on Linux, and an absent RTA_PRIORITY is not
// the same thing.
func TestParseRouteReplyKeepsAZeroMetricApartFromNone(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	with, _ := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, 0, unix.RT_TABLE_MAIN, unix.RTN_UNICAST, rtAttr(unix.RTA_PRIORITY, u32(0)))}, dst)
	if !with.MetricKnown || with.Metric != 0 {
		t.Errorf("an explicit metric of 0 = %d (known %t), want a recorded zero", with.Metric, with.MetricKnown)
	}
	without, _ := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, 0, unix.RT_TABLE_MAIN, unix.RTN_UNICAST)}, dst)
	if without.MetricKnown {
		t.Error("an absent metric was reported as known")
	}
}

// The kernel's own way of saying there is no route. RTN_THROW is not one of
// them: it abandons a table and sends the lookup on to the next rule, so a
// later table may still route the destination and calling it "no route" would
// answer a question the kernel had not finished asking.
func TestParseRouteReplyReportsUnreachableRouteTypes(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	thrown, ok := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, 0, unix.RT_TABLE_MAIN, unix.RTN_THROW)}, dst)
	if ok && thrown.Unreachable {
		t.Error("a throw route was reported as no route, but it only ends one table's lookup")
	}
	for _, rtType := range []uint8{unix.RTN_UNREACHABLE, unix.RTN_BLACKHOLE, unix.RTN_PROHIBIT} {
		got, ok := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, 0, unix.RT_TABLE_MAIN, rtType)}, dst)
		if !ok || !got.Unreachable {
			t.Errorf("rtm_type %d = %+v/%v, want an unreachable decision", rtType, got, ok)
		}
		if got.Iface != "" || got.Prefix.IsValid() {
			t.Errorf("rtm_type %d invented path fields: %+v", rtType, got)
		}
	}
}

// A next hop the kernel expressed in the other address family is still a next
// hop. Reading only RTA_GATEWAY would leave the decision looking gatewayless,
// and a gatewayless decision is reported as on-link, which would be netdoc
// saying there is no router in the way while the kernel was naming one.
func TestParseRouteReplyReadsAViaNextHop(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	via := append(append([]byte{}, u16(unix.AF_INET6)...), net.ParseIP("fe80::1").To16()...)
	got, ok := parseRouteReply([]netlinkMessage{
		rtMsg(unix.AF_INET, 0, unix.RT_TABLE_MAIN, unix.RTN_UNICAST, rtAttr(unix.RTA_VIA, via)),
	}, dst)
	if !ok || got.Gateway == nil || got.Gateway.String() != "fe80::1" {
		t.Fatalf("gateway = %v (ok %v), want the via next hop", got.Gateway, ok)
	}
	got.Iface = "eth0"
	if reason := routeReason(got, RouteDecision{}); reason == RouteReasonOnLink {
		t.Errorf("reason = %q for a destination the kernel gave a router for", reason)
	}
	// A malformed or unfamiliar rtvia is no next hop rather than a guess.
	for _, bad := range [][]byte{{}, u16(unix.AF_INET6), append(u16(unix.AF_INET), 1, 2, 3), u16(0xff)} {
		if ip := netlinkVia(bad); ip != nil {
			t.Errorf("netlinkVia(%v) = %v, want none", bad, ip)
		}
	}
}

// A policy-routing table keeps its number, because the number is the point.
func TestRouteTableNameKeepsPolicyTableNumbers(t *testing.T) {
	cases := map[uint32]string{
		unix.RT_TABLE_MAIN:    "",
		unix.RT_TABLE_UNSPEC:  "",
		unix.RT_TABLE_LOCAL:   "local",
		unix.RT_TABLE_DEFAULT: "default",
		51820:                 "table 51820",
	}
	for id, want := range cases {
		if got := routeTableName(id); got != want {
			t.Errorf("routeTableName(%d) = %q, want %q", id, got, want)
		}
	}
	// rtm_table saturates at a byte, so the attribute has to win.
	dst := netip.MustParseAddr("198.51.100.7")
	got, _ := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, 0, 253, unix.RTN_UNICAST, rtAttr(unix.RTA_TABLE, u32(51820)))}, dst)
	if got.Table != "table 51820" {
		t.Errorf("table = %q, want the attribute to win over the saturated byte", got.Table)
	}
}

// A reply that is not a route, and a truncated one, are both "no answer"
// rather than a decision assembled out of zeroes.
func TestParseRouteReplyRefusesWhatIsNotADecision(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	if _, ok := parseRouteReply(nil, dst); ok {
		t.Error("an empty reply produced a decision")
	}
	if _, ok := parseRouteReply([]netlinkMessage{{Type: unix.RTM_NEWLINK, Data: make([]byte, rtMsgLen)}}, dst); ok {
		t.Error("a link message was parsed as a route")
	}
	if _, ok := parseRouteReply([]netlinkMessage{{Type: unix.RTM_NEWROUTE, Data: []byte{1, 2}}}, dst); ok {
		t.Error("a truncated route message produced a decision")
	}
}

// rtm_dst_len is the length the request asked about, not the entry the kernel
// matched: every answer to a host lookup comes back at the full address
// length whatever the table holds. Reading it as the matched prefix would
// report every destination as a host route, so no length in the reply may
// produce one.
func TestParseRouteReplyNeverReadsTheEchoedPrefixLength(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	for _, dstLen := range []uint8{0, 16, 32, 99} {
		got, ok := parseRouteReply([]netlinkMessage{rtMsg(unix.AF_INET, dstLen, unix.RT_TABLE_MAIN, unix.RTN_UNICAST)}, dst)
		if !ok {
			t.Fatalf("dst_len %d: the reply was rejected outright", dstLen)
		}
		if got.Prefix.IsValid() {
			t.Errorf("dst_len %d produced prefix %q, want none", dstLen, got.Prefix)
		}
		if routeReason(got, RouteDecision{}) == RouteReasonHostRoute {
			t.Errorf("dst_len %d was read as a host route", dstLen)
		}
	}
}

// The real kernel, on this machine, for a destination that is certainly not a
// host route: it must not come back claiming to be one.
func TestLookupRouteDecisionClaimsNoMatchedPrefix(t *testing.T) {
	got, ok := lookupRouteDecision(net.ParseIP("127.0.0.1"))
	if !ok {
		t.Skip("the kernel did not answer a route lookup in this environment")
	}
	if got.Prefix.IsValid() {
		t.Errorf("prefix = %q, want none on a platform that does not report the matched entry", got.Prefix)
	}
}

func TestNetlinkAttrsWalksTheAttributeStream(t *testing.T) {
	var b []byte
	b = append(b, rtAttr(unix.RTA_OIF, u32(3))...)
	b = append(b, rtAttr(unix.RTA_GATEWAY, net.ParseIP("192.168.1.1").To4())...)
	b = append(b, rtAttr(iflaIfName, []byte("wg0\x00"))...)
	attrs := netlinkAttrs(b)
	if len(attrs) != 3 {
		t.Fatalf("walked %d attributes, want 3", len(attrs))
	}
	if attrs[0].Type != unix.RTA_OIF || binary.NativeEndian.Uint32(attrs[0].Value) != 3 {
		t.Errorf("first attribute = %+v, want the output interface index", attrs[0])
	}
	if got := nullTerminated(attrs[2].Value); got != "wg0" {
		t.Errorf("interface name = %q, want wg0", got)
	}
	// A length field that runs past the buffer stops the walk instead of
	// reading beyond it.
	truncated := append([]byte(nil), b...)
	binary.NativeEndian.PutUint16(truncated[0:2], uint16(len(truncated)+8)) // #nosec G115 -- a fixed test buffer
	if got := netlinkAttrs(truncated); len(got) != 0 {
		t.Errorf("a length past the end yielded %d attributes, want none", len(got))
	}
	if got := netlinkAttrs([]byte{1}); len(got) != 0 {
		t.Errorf("a stub buffer yielded %d attributes, want none", len(got))
	}
}

// The nested-attribute type bits are masked off, which is what lets the link
// kind be read out of IFLA_LINKINFO.
func TestNetlinkAttrsStripsTheNestedFlag(t *testing.T) {
	b := rtAttr(iflaLinkInfo|0x8000, rtAttr(iflaInfoKind, []byte("wireguard\x00")))
	attrs := netlinkAttrs(b)
	if len(attrs) != 1 || attrs[0].Type != iflaLinkInfo {
		t.Fatalf("attributes = %+v, want one IFLA_LINKINFO", attrs)
	}
	nested := netlinkAttrs(attrs[0].Value)
	if len(nested) != 1 || nullTerminated(nested[0].Value) != "wireguard" {
		t.Errorf("nested attributes = %+v, want the wireguard kind", nested)
	}
}

func TestNetlinkMessagesReportsTheKernelsError(t *testing.T) {
	errMsg := make([]byte, nlMsgHdrLen+4)
	binary.NativeEndian.PutUint32(errMsg[0:4], uint32(len(errMsg))) // #nosec G115 -- a fixed test buffer
	binary.NativeEndian.PutUint16(errMsg[4:6], unix.NLMSG_ERROR)
	code := -int32(unix.ENETUNREACH)
	binary.NativeEndian.PutUint32(errMsg[nlMsgHdrLen:], uint32(code)) // #nosec G115 -- a negated errno, the kernel's own encoding
	if _, err := netlinkMessages(errMsg); !errors.Is(err, unix.ENETUNREACH) {
		t.Errorf("netlinkMessages error = %v, want ENETUNREACH", err)
	}
	// An acknowledgement of zero is success, not an error.
	binary.NativeEndian.PutUint32(errMsg[nlMsgHdrLen:], 0)
	if msgs, err := netlinkMessages(errMsg); err != nil || len(msgs) != 0 {
		t.Errorf("netlinkMessages(ack) = %+v/%v, want no messages and no error", msgs, err)
	}
}

// The errors that mean "there is no route" are told apart from the ones that
// mean the lookup itself failed, which must never be recorded as no route.
func TestUnreachableNetlinkErrorNamesOnlyRoutingFailures(t *testing.T) {
	for _, err := range []error{unix.ENETUNREACH, unix.EHOSTUNREACH, unix.ENETDOWN} {
		if !unreachableNetlinkError(err) {
			t.Errorf("%v is not treated as an absent route", err)
		}
	}
	for _, err := range []error{unix.EACCES, unix.EPERM, unix.EINVAL, unix.ENOBUFS, nil} {
		if unreachableNetlinkError(err) {
			t.Errorf("%v was treated as an absent route", err)
		}
	}
}

// The real kernel, asked about the loopback address. It needs no privileges,
// no network, and no particular routing table: every Linux machine routes
// 127.0.0.1 to loopback, and a machine that does not is broken in a way this
// test is entitled to notice.
func TestLookupRouteDecisionAnswersForLoopback(t *testing.T) {
	got, ok := lookupRouteDecision(net.ParseIP("127.0.0.1"))
	if !ok {
		t.Skip("the kernel did not answer a route lookup in this environment")
	}
	if got.Unreachable {
		t.Fatalf("loopback reported unreachable: %+v", got)
	}
	if got.Iface == "" {
		t.Errorf("decision = %+v, want a selected interface", got)
	}
	if got.Tunnel != TunnelDirect {
		t.Errorf("loopback tunnel state = %q, want %q", got.Tunnel, TunnelDirect)
	}
}
