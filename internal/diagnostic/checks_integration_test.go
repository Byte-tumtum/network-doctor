//go:build integration

// Opt-in (-tags integration) tests that dial real loopback sockets.

package diagnostic

// Real-socket tests, kept out of the unit suite. Run with:
//
//	go test -tags integration ./internal/diagnostic
//
// Offline-safe: loopback only.

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// dialIPs against a live loopback listener returns a connection pinned to the
// address that won, with the attempt recorded.
func TestDialIPsLoopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, sel, attempts, rtt := defaultOps.dialIPs(ctx, []net.IP{net.ParseIP("127.0.0.1")}, port)
	if conn == nil {
		t.Fatal("expected a connection to the loopback listener")
	}
	defer conn.Close()
	if !sel.Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("selected = %v, want 127.0.0.1", sel)
	}
	if len(attempts) != 1 {
		t.Errorf("attempts = %d, want 1", len(attempts))
	}
	if rtt <= 0 {
		t.Errorf("rtt = %v, want > 0", rtt)
	}
}

// The configured source reaches the kernel socket rather than only probe
// metadata: the live connection reports the address we pinned.
func TestDialFromSourceLoopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	conn, err := opsFromSources(&SourceAddresses{IPv4: net.ParseIP("127.0.0.1")}).dialContext(
		context.Background(), "tcp4", ln.Addr().String(),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if got := conn.LocalAddr().(*net.TCPAddr).IP; !got.Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("source = %v, want 127.0.0.1", got)
	}
}

// A refused loopback port fails fast and deterministically: no conn, the failed
// attempt is recorded with its error.
func TestDialIPsRefusedLoopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listening now → connection refused

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, attempts, _ := defaultOps.dialIPs(ctx, []net.IP{net.ParseIP("127.0.0.1")}, port)
	if conn != nil {
		conn.Close()
		t.Fatal("expected no connection to a closed port")
	}
	if len(attempts) != 1 || attempts[0].Err == nil {
		t.Errorf("want one failed attempt with an error, got %+v", attempts)
	}
}

// pathIdentity reads a real winning conn's LocalAddr as ground truth.
func TestPathIdentityLoopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	src, iface := defaultOps.pathIdentity(context.Background(), conn, net.ParseIP("127.0.0.1"), port)
	if src == nil || !src.IsLoopback() {
		t.Errorf("src = %v, want a loopback address", src)
	}
	if iface == "" {
		t.Error("iface should resolve for the loopback source")
	}
}

// The PMTU probe over a real socket, against the case most likely to produce a
// false alarm: a listener that accepts the connection and then never reads a
// byte. Its receive buffer has to absorb the whole payload — if it doesn't, the
// probe's own write stalls and every healthy peer that pauses gets accused of
// black-holing packets. Nothing here is a black hole, so nothing may warn.
func TestPMTUProbeLoopbackSilentPeerDoesNotWarn(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- conn // held open, never read from
	}()

	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("127.0.0.1")}}
	r := defaultOps.pmtuProbe(port, ProtoNone)(ctx, deps)
	if conn := <-accepted; conn != nil {
		conn.Close()
	}
	if r.Status != StatusPass {
		t.Errorf("silent-but-healthy peer = %+v, want PASS (%d KiB must fit in its receive buffer)", r, pmtuPayloadSize>>10)
	}
}

// SourceIP resolves both of the forms -iface accepts, without this test
// knowing what the loopback interface is called (lo, lo0, "Loopback
// Pseudo-Interface 1", ...).
func TestSourceIPResolvesLoopbackInterface(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("interfaces: %v", err)
	}
	name := ""
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagLoopback == 0 {
			continue
		}
		if addrs, err := ifaces[i].Addrs(); err == nil && preferredSourceIP(addrs) != nil {
			name = ifaces[i].Name
			break
		}
	}
	if name == "" {
		t.Skip("no loopback interface with a usable address")
	}

	ip, err := SourceIP(name)
	if err != nil {
		t.Fatalf("SourceIP(%q): %v", name, err)
	}
	if !ip.IsLoopback() {
		t.Errorf("SourceIP(%q) = %v, want a loopback address", name, ip)
	}
	// The interface name rides along for the drill-down tools whose binding
	// option takes a name; the exact-IP form has no name to report.
	sources, err := ResolveSource(name)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", name, err)
	}
	if sources.Iface != name {
		t.Errorf("ResolveSource(%q).Iface = %q, want %q", name, sources.Iface, name)
	}
	for _, literal := range []net.IP{sources.IPv4, sources.IPv6} {
		if literal == nil {
			continue
		}
		byIP, err := ResolveSource(literal.String())
		if err != nil {
			t.Fatalf("ResolveSource(%q): %v", literal, err)
		}
		if byIP.Iface != "" {
			t.Errorf("ResolveSource(%q).Iface = %q, want empty", literal, byIP.Iface)
		}
		if !byIP.primary().Equal(literal) || byIP.IPv4 != nil && byIP.IPv6 != nil {
			t.Errorf("ResolveSource(%q) = %+v, want only that address", literal, byIP)
		}
	}

	// The exact-IP form has to accept what the name form just handed back.
	again, err := SourceIP(ip.String())
	if err != nil {
		t.Fatalf("SourceIP(%q): %v", ip, err)
	}
	if !again.Equal(ip) {
		t.Errorf("SourceIP(%q) = %v, want %v", ip, again, ip)
	}

	// TEST-NET-1 is reserved for documentation, so it is never a local address.
	if _, err := SourceIP("192.0.2.1"); err == nil {
		t.Error("an unassigned IP should be rejected")
	}
	if _, err := SourceIP("netdoc-no-such-interface"); err == nil {
		t.Error("an unknown interface name should be rejected")
	}
}

// The configured source has to reach the DNS socket too, not only the TCP one:
// a probe that binds correctly while its resolver leaks onto the default route
// is the failure this test exists to catch.
func TestResolverDialsFromSourceLoopback(t *testing.T) {
	stub := newDNSStub(t, net.ParseIP("192.0.2.7"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The address the hook is handed comes from resolv.conf and varies per
	// machine; send every query to the stub instead. What is under test is the
	// dialer the source produced, not which server the host would have picked.
	dial := dialContextFrom(net.ParseIP("127.0.0.1"))
	ips, server, err := lookupIPWithDial(ctx, "netdoc.test.", func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dial(ctx, network, stub.addr())
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !containsIP(ips, net.ParseIP("192.0.2.7")) {
		t.Errorf("ips = %v, want 192.0.2.7", ips)
	}
	if server == "" {
		t.Error("server should report the resolver that was dialed")
	}
	stub.wantSources(t, net.ParseIP("127.0.0.1"))
}

// Same for the second-opinion resolver, which additionally must ignore the
// address it is given and always ask the public server.
func TestPublicResolverDialsFromSourceLoopback(t *testing.T) {
	stub := newDNSStub(t, net.ParseIP("192.0.2.8"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		mu      sync.Mutex
		targets []string
	)
	dial := dialContextFrom(net.ParseIP("127.0.0.1"))
	ips, err := lookupIPPublicWithDial(ctx, "netdoc.test.", func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		targets = append(targets, addr)
		mu.Unlock()
		return dial(ctx, network, stub.addr())
	}, publicDNSServer(DefaultPublicDNS))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !containsIP(ips, net.ParseIP("192.0.2.8")) {
		t.Errorf("ips = %v, want 192.0.2.8", ips)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(targets) == 0 {
		t.Fatal("the dial hook was never called")
	}
	for _, addr := range targets {
		if addr != publicDNSServer(DefaultPublicDNS) {
			t.Errorf("dialed %q, want %q", addr, publicDNSServer(DefaultPublicDNS))
		}
	}
	stub.wantSources(t, net.ParseIP("127.0.0.1"))
}

func containsIP(ips []net.IP, want net.IP) bool {
	for _, ip := range ips {
		if ip.Equal(want) {
			return true
		}
	}
	return false
}

// dnsStub is a loopback UDP resolver that answers A queries with one fixed
// address and everything else with an empty NOERROR — enough to satisfy the Go
// resolver's parallel A/AAAA pair without a DNS library. It records the source
// address every query arrived from, which is the whole point of it.
type dnsStub struct {
	conn *net.UDPConn
	mu   sync.Mutex
	srcs []net.IP
}

func newDNSStub(t *testing.T, answer net.IP) *dnsStub {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	s := &dnsStub{conn: conn}
	go s.serve(answer)
	return s
}

func (s *dnsStub) addr() string { return s.conn.LocalAddr().String() }

func (s *dnsStub) serve(answer net.IP) {
	buf := make([]byte, 512)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // closed by cleanup
		}
		s.mu.Lock()
		s.srcs = append(s.srcs, from.IP)
		s.mu.Unlock()
		if reply := dnsReply(buf[:n], answer); reply != nil {
			s.conn.WriteToUDP(reply, from)
		}
	}
}

// wantSources asserts every query reached the stub from want. Reading after the
// lookup returned is safe: the resolver has no queries left in flight.
func (s *dnsStub) wantSources(t *testing.T, want net.IP) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.srcs) == 0 {
		t.Fatal("no query reached the stub resolver")
	}
	for _, src := range s.srcs {
		if !src.Equal(want) {
			t.Errorf("query source = %v, want %v", src, want)
		}
	}
}

// dnsReply turns a query into a response: the same header and question back,
// plus a single A record when A is what was asked for. Anything else — AAAA, a
// packet too short to parse — gets an answerless NOERROR, which the resolver
// accepts without retrying.
func dnsReply(q []byte, answer net.IP) []byte {
	if len(q) < 12 {
		return nil
	}
	// Walk the question's length-prefixed labels to the qtype behind them.
	i := 12
	for i < len(q) && q[i] != 0 {
		i += int(q[i]) + 1
	}
	if i+5 > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[i+1:])

	r := append([]byte(nil), q[:i+5]...)
	r[2], r[3] = 0x81, 0x80 // QR + RD, RA + NOERROR
	binary.BigEndian.PutUint16(r[6:], 0)
	// Any EDNS OPT record the resolver appended is dropped along with the
	// counts that described it.
	binary.BigEndian.PutUint16(r[8:], 0)
	binary.BigEndian.PutUint16(r[10:], 0)
	if qtype != 1 {
		return r
	}
	binary.BigEndian.PutUint16(r[6:], 1)
	// 0xc00c points back at the question's name; then A, IN, TTL 60, 4 bytes.
	r = append(r, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
	return append(r, answer.To4()...)
}
