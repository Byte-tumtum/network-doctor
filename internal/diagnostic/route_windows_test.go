//go:build windows

package diagnostic

import (
	"net"
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
)

// Windows says what kind of device an adapter is through a structural type,
// and netdoc reads that rather than matching product names. A type that does
// not settle the question falls through to shape, where a guess can only reach
// "likely".
func TestWindowsLinkKindOnlySettlesWhatTheTypeSettles(t *testing.T) {
	cases := []struct {
		ifType uint32
		kind   string
		state  TunnelState
	}{
		{ifTypeTunnel, "tunnel", TunnelUnknown},
		{ifTypePPP, "ppp", TunnelKnown},
		{ifTypeEthernet, "ethernet", TunnelDirect},
		{ifTypeIEEE80211, "ethernet", TunnelDirect},
		{ifTypeSoftwareLpb, "ethernet", TunnelDirect},
		{ifTypeOther, "", TunnelUnknown},
		{53, "", TunnelUnknown}, // IF_TYPE_PROP_VIRTUAL
	}
	for _, c := range cases {
		if got := windowsLinkKind(c.ifType); got != c.kind {
			t.Errorf("windowsLinkKind(%d) = %q, want %q", c.ifType, got, c.kind)
		}
	}
	// "tunnel" is in the encapsulating vocabulary, so an IF_TYPE_TUNNEL
	// adapter is asserted rather than guessed at.
	if state, kind := classifyTunnel(ifaceFacts{Name: "vpn0", Kind: windowsLinkKind(ifTypeTunnel)}); state != TunnelKnown || kind != "tunnel" {
		t.Errorf("an IF_TYPE_TUNNEL adapter = %q/%q, want a known tunnel", state, kind)
	}
	// An adapter that declines to say is read by shape, and shape can only
	// ever reach "likely".
	if state, _ := classifyTunnel(ifaceFacts{Name: "vpn0", Kind: windowsLinkKind(ifTypeOther), NoLinkLayer: true}); state != TunnelLikely {
		t.Errorf("an unnamed adapter with a tunnel's shape = %q, want %q", state, TunnelLikely)
	}
	if state, _ := classifyTunnel(ifaceFacts{Name: "Ethernet", Kind: windowsLinkKind(ifTypeEthernet)}); state != TunnelDirect {
		t.Errorf("an Ethernet adapter = %q, want %q", state, TunnelDirect)
	}
}

// The destination union GetBestRoute2 takes, and the address reading that goes
// the other way, agree in both families.
func TestSockaddrInetRoundTripsBothFamilies(t *testing.T) {
	for _, want := range []string{"198.51.100.7", "2001:db8::7"} {
		addr := netip.MustParseAddr(want)
		sa := sockaddrInet(addr)
		wantFamily := uint16(windows.AF_INET)
		if addr.Is6() {
			wantFamily = windows.AF_INET6
		}
		if sa.Family != wantFamily {
			t.Errorf("%s family = %d, want %d", want, sa.Family, wantFamily)
		}
		if got := sockaddrInetIP(&sa); got == nil || !got.Equal(net.ParseIP(want)) {
			t.Errorf("sockaddrInetIP round trip = %v, want %s", got, want)
		}
	}
}

// Windows ranks routes by the sum of the route metric and the interface
// metric, and a prefix length the destination cannot hold is dropped rather
// than clamped into an entry Windows never reported.
func TestRouteDecisionFromBestRouteReadsThePrefixItWasGiven(t *testing.T) {
	dst := netip.MustParseAddr("198.51.100.7")
	best := windows.MibIpForwardRow2{}
	best.DestinationPrefix.PrefixLength = 16
	best.DestinationPrefix.Prefix.Family = windows.AF_INET
	var source windows.RawSockaddrInet
	got := routeDecisionFromBestRoute(&best, &source, dst)
	if got.Prefix.String() != "198.51.0.0/16" {
		t.Errorf("prefix = %q, want the /16 Windows reported", got.Prefix)
	}
	if got.Gateway != nil {
		t.Errorf("gateway = %v, want none: an unspecified next hop is on-link", got.Gateway)
	}
	best.DestinationPrefix.PrefixLength = 99
	if got := routeDecisionFromBestRoute(&best, &source, dst); got.Prefix.IsValid() {
		t.Errorf("prefix = %q, want none for an impossible length", got.Prefix)
	}
}

// The real API, asked about the loopback address. It needs no privileges and
// no network.
func TestLookupRouteDecisionAnswersForLoopback(t *testing.T) {
	got, ok := lookupRouteDecision(net.ParseIP("127.0.0.1"), nil)
	if !ok {
		t.Skip("Windows did not answer a route lookup in this environment")
	}
	if got.Unreachable {
		t.Fatalf("loopback reported unreachable: %+v", got)
	}
}
