// Stub-network tests for probe plumbing: dialIPs, path identity, dial
// warnings, proxy CONNECT handling, and the egress downgrade.

package diagnostic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeConn is a no-network net.Conn stand-in; only LocalAddr and Close are
// used by the code under test.
type fakeConn struct {
	net.Conn
	local net.Addr
}

func (c fakeConn) LocalAddr() net.Addr { return c.local }
func (fakeConn) Close() error          { return nil }

func TestStatusString(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusPass, "PASS"},
		{StatusWarn, "WARN"},
		{StatusFail, "FAIL"},
		{StatusSkip, "SKIP"},
		{StatusNA, "N/A"},
		{Status(255), "?"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Status(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestJoinIPs(t *testing.T) {
	if got := joinIPs(nil); got != "" {
		t.Errorf("joinIPs(nil) = %q, want empty", got)
	}
	ips := []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.8.8")}
	if got := joinIPs(ips); got != "1.1.1.1, 8.8.8.8" {
		t.Errorf("joinIPs = %q, want '1.1.1.1, 8.8.8.8'", got)
	}
}

func TestPreferredSourceIP(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("2001:db8::2")},
		&net.IPNet{IP: net.ParseIP("192.0.2.2")},
	}
	if got := preferredSourceIP(addrs); !got.Equal(net.ParseIP("192.0.2.2")) {
		t.Errorf("preferred source = %v, want IPv4 address", got)
	}
	if got := preferredSourceIP(addrs[:1]); !got.Equal(net.ParseIP("2001:db8::2")) {
		t.Errorf("IPv6-only source = %v, want 2001:db8::2", got)
	}
}

func TestDialerFromUsesNetworkAddressType(t *testing.T) {
	source := net.ParseIP("192.0.2.2")
	if _, ok := dialerFrom(source, "tcp").LocalAddr.(*net.TCPAddr); !ok {
		t.Error("TCP dialer LocalAddr is not *net.TCPAddr")
	}
	if _, ok := dialerFrom(source, "udp").LocalAddr.(*net.UDPAddr); !ok {
		t.Error("UDP dialer LocalAddr is not *net.UDPAddr")
	}
}

// dialIPs with a stubbed dialer returns a connection pinned to the address
// that won, with the attempt recorded. No real sockets.
func TestDialIPsSuccess(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		time.Sleep(time.Millisecond) // make the recorded RTT observable
		return fakeConn{}, nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, sel, attempts, rtt := ops.dialIPs(ctx, []net.IP{net.ParseIP("192.0.2.1")}, 80)
	if conn == nil {
		t.Fatal("expected a connection from the stub dialer")
	}
	defer conn.Close()
	if !sel.Equal(net.ParseIP("192.0.2.1")) {
		t.Errorf("selected = %v, want 192.0.2.1", sel)
	}
	if len(attempts) != 1 {
		t.Errorf("attempts = %d, want 1", len(attempts))
	}
	if rtt <= 0 {
		t.Errorf("rtt = %v, want > 0", rtt)
	}
}

func TestDialIPsEmpty(t *testing.T) {
	conn, sel, attempts, rtt := (&netops{}).dialIPs(context.Background(), nil, 80)
	if conn != nil || sel != nil || attempts != nil || rtt != 0 {
		t.Errorf("dialIPs(empty) = (%v,%v,%v,%v), want all zero", conn, sel, attempts, rtt)
	}
}

// A refused dial fails deterministically: no conn, the failed attempt is
// recorded with its error.
func TestDialIPsRefused(t *testing.T) {
	errRefused := errors.New("connection refused")
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errRefused
	}}

	conn, _, attempts, _ := ops.dialIPs(context.Background(), []net.IP{net.ParseIP("192.0.2.1")}, 80)
	if conn != nil {
		conn.Close()
		t.Fatal("expected no connection from the failing dialer")
	}
	if len(attempts) != 1 || !errors.Is(attempts[0].Err, errRefused) {
		t.Errorf("want one failed attempt with the dialer's error, got %+v", attempts)
	}
}

// Siblings that fail before the win stay in the attempt list. Every stub dial
// returns instantly, so failures pile up faster than the race loop drains them
// and the winning hand-off becomes ready with several still queued — the exact
// shape that used to lose them. Repeated because the interleaving is a race.
func TestDialIPsKeepsFailuresThatLostToTheWinner(t *testing.T) {
	ops := &netops{dialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "192.0.2.255:") {
			return fakeConn{}, nil
		}
		return nil, errors.New("connection refused")
	}}
	var ips []net.IP
	for i := range maxAttempts - 1 {
		ips = append(ips, net.IPv4(192, 0, 2, byte(i+1)))
	}
	ips = append(ips, net.IPv4(192, 0, 2, 255)) // only address the stub connects to

	for round := range 200 {
		conn, sel, attempts, _ := ops.dialIPs(context.Background(), ips, 80)
		if conn == nil {
			t.Fatalf("round %d: expected the last address to win", round)
		}
		conn.Close()
		if len(attempts) != len(ips) {
			t.Fatalf("round %d: recorded %d of %d attempts: %+v", round, len(attempts), len(ips), attempts)
		}
		if !attempts[len(attempts)-1].IP.Equal(sel) {
			t.Fatalf("round %d: winner is not the last attempt: %+v", round, attempts)
		}
	}
}

// pathIdentity reads the winning conn's LocalAddr as ground truth and maps it
// back to an interface via the stubbed interface list.
func TestPathIdentityFromConn(t *testing.T) {
	ops := &netops{
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "fake0"}}, nil
		},
		interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)}}, nil
		},
	}
	conn := fakeConn{local: &net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 40000}}

	src, iface := ops.pathIdentity(context.Background(), conn, net.ParseIP("192.0.2.1"), 80)
	if !src.Equal(net.ParseIP("192.0.2.7")) {
		t.Errorf("src = %v, want 192.0.2.7", src)
	}
	if iface != "fake0" {
		t.Errorf("iface = %q, want fake0", iface)
	}
}

// ifaceForIP for an address assigned to no interface is an explicit unknown,
// never a guess. The interface list is stubbed, so the verdict comes from the
// fixture rather than from whatever addresses the test machine happens to hold.
func TestIfaceForIPUnknown(t *testing.T) {
	ops := &netops{
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "fake0"}, {Name: "fake1"}}, nil
		},
		interfaceAddrs: func(ifi *net.Interface) ([]net.Addr, error) {
			if ifi.Name == "fake1" {
				return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)}}, nil
			}
			return nil, nil
		},
	}
	if got := ops.ifaceForIP(net.ParseIP("203.0.113.213")); got != "(unknown iface)" {
		t.Errorf("ifaceForIP(unassigned) = %q, want '(unknown iface)'", got)
	}
	// The same stub still resolves an address it does hold, so the unknown
	// above is a real miss and not an empty interface list.
	if got := ops.ifaceForIP(net.ParseIP("192.0.2.7")); got != "fake1" {
		t.Errorf("ifaceForIP(assigned) = %q, want fake1", got)
	}
}

// Probes run against stubbed netops: no real network, DNS, or OS interface
// access — the point of the function-field seam.
func TestNetopsInjection(t *testing.T) {
	ops := &netops{
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "fake0", Flags: net.FlagUp | net.FlagRunning}}, nil
		},
		lookupIP: func(context.Context, string) ([]net.IP, string, error) {
			return []net.IP{net.ParseIP("192.0.2.1")}, "192.0.2.53:53", nil
		},
		ssid: func(context.Context, string) string { return "FakeNet" },
	}

	r := ops.ifaceProbe(context.Background(), nil)
	if r.Status != StatusPass || r.Iface != "fake0" || r.Network != "" {
		t.Errorf("ifaceProbe with stubs = %+v, want PASS on fake0 without SSID", r)
	}
	network := ops.ssidProbe(context.Background(), map[ProbeID]ProbeResult{ProbeIface: r})
	if network.Status != StatusPass || network.Network != "FakeNet" {
		t.Errorf("ssidProbe with stubs = %+v, want PASS on FakeNet", network)
	}

	r = ops.dnsProbe("example.com", nil)(context.Background(), nil)
	if r.Status != StatusPass || len(r.Addrs) != 1 || !r.Addrs[0].Equal(net.ParseIP("192.0.2.1")) {
		t.Errorf("dnsProbe with stubs = %+v, want PASS with 192.0.2.1", r)
	}
}

// Degraded-but-functional dials downgrade to WARN: high latency, sibling
// address failures before a win, and an ambiguous source interface. A clean
// fast dial stays PASS.
func TestApplyDialWarnings(t *testing.T) {
	ip := net.ParseIP("192.0.2.1")
	cases := []struct {
		name     string
		attempts []Attempt
		rtt      time.Duration
		iface    string
		want     Status
		note     string
	}{
		{"clean", []Attempt{{IP: ip}}, 10 * time.Millisecond, "eth0", StatusPass, ""},
		{"high latency", []Attempt{{IP: ip}}, warnRTT, "eth0", StatusWarn, "high latency"},
		{"partial addresses", []Attempt{{IP: ip, Err: errors.New("refused")}, {IP: ip}}, 10 * time.Millisecond, "eth0", StatusWarn, "1 of 2 address(es) failed"},
		{"ambiguous iface", []Attempt{{IP: ip}}, 10 * time.Millisecond, "(ambiguous)", StatusWarn, "ambiguous source interface"},
	}
	for _, c := range cases {
		r := ProbeResult{Status: StatusPass, Attempts: c.attempts, Iface: c.iface, Detail: "connected"}
		applyDialWarnings(&r, c.rtt)
		if r.Status != c.want {
			t.Errorf("%s: status = %v, want %v", c.name, r.Status, c.want)
		}
		if c.note != "" && !strings.Contains(r.Detail, c.note) {
			t.Errorf("%s: detail = %q, want it to mention %q", c.name, r.Detail, c.note)
		}
	}
}

// scriptConn plays a canned proxy response and swallows writes; stands in for
// the wire in the CONNECT probe tests.
type scriptConn struct {
	fakeConn
	r             io.Reader
	w             strings.Builder
	writeDeadline time.Time
}

func (c *scriptConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *scriptConn) Write(p []byte) (int, error)        { return c.w.Write(p) }
func (*scriptConn) SetReadDeadline(time.Time) error      { return nil }
func (c *scriptConn) SetWriteDeadline(d time.Time) error { c.writeDeadline = d; return nil }
func (c *scriptConn) SetDeadline(d time.Time) error      { c.writeDeadline = d; return nil }

func proxyOps(proxy string, dial func(context.Context, string, string) (net.Conn, error)) *netops {
	return &netops{
		proxyFromEnv: func(*http.Request) (*url.URL, error) {
			if proxy == "" {
				return nil, nil
			}
			return url.Parse(proxy)
		},
		dialContext: dial,
		lookupIP: func(context.Context, string) ([]net.IP, string, error) {
			return []net.IP{net.ParseIP("192.0.2.10")}, "192.0.2.53:53", nil
		},
	}
}

func TestProxyProbeNoProxyIsNA(t *testing.T) {
	r := proxyOps("", nil).proxyProbe(context.Background(), nil)
	if r.Status != StatusNA || !strings.Contains(r.Detail, "no proxy") {
		t.Errorf("no env proxy = %+v, want N/A", r)
	}
}

func TestProxyProbeUnknownSchemeIsNA(t *testing.T) {
	ops := proxyOps("socks4://proxy.corp", func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("an unsupported proxy scheme must not be dialed")
		return nil, nil
	})
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusNA || !strings.Contains(r.Detail, "socks4") {
		t.Errorf("SOCKS4 proxy = %+v, want N/A", r)
	}
}

// socks5Reply is a granted CONNECT: greeting picks no-auth, then reply code 0
// with a bound IPv4 address.
var socks5Reply = string([]byte{5, 0, 5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

// Both SOCKS schemes use a real handshake. socks5 sends the address resolved
// by the client; socks5h sends the hostname for the proxy to resolve.
func TestProxyProbeSocks5(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			conn := &scriptConn{r: strings.NewReader(socks5Reply)}
			var dialed string
			ops := proxyOps(scheme+"://proxy.corp", func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = addr
				return conn, nil
			})
			r := ops.proxyProbe(context.Background(), nil)
			if r.Status != StatusPass || !strings.Contains(r.Detail, "proxy proxy.corp:1080 tunnels") {
				t.Errorf("granted SOCKS5 CONNECT = %+v, want PASS", r)
			}
			if dialed != "proxy.corp:1080" {
				t.Errorf("dialed %q, want proxy.corp:1080 (default SOCKS port)", dialed)
			}
			want := string([]byte{5, 1, 0, 5, 1, 0})
			if scheme == "socks5h" {
				want += string([]byte{3, byte(len(probeHost))}) + probeHost
			} else {
				want += string([]byte{1, 192, 0, 2, 10})
			}
			want += string([]byte{1, 187})
			if conn.w.String() != want {
				t.Errorf("wire bytes = %q, want %q", conn.w.String(), want)
			}
			if conn.writeDeadline.IsZero() {
				t.Error("deadline is zero, want the probe budget as a leash")
			}
		})
	}
}

func TestProxyProbeSocks5LocalDNSFailure(t *testing.T) {
	conn := &scriptConn{r: strings.NewReader(string([]byte{5, 0}))}
	ops := proxyOps("socks5://proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	ops.lookupIP = func(context.Context, string) ([]net.IP, string, error) {
		return nil, "192.0.2.53:53", errors.New("no such host")
	}
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "is reachable, but local DNS cannot resolve") ||
		!strings.Contains(r.Detail, "192.0.2.53") {
		t.Errorf("local DNS failure = %+v, want reachable proxy and resolver evidence", r)
	}
	if r.Cause != ProxyCauseClientDNS {
		t.Errorf("cause = %q, want %q", r.Cause, ProxyCauseClientDNS)
	}
	if got := conn.w.String(); got != string([]byte{5, 1, 0}) {
		t.Errorf("wire bytes = %q, want greeting only", got)
	}
}

func TestProxyProbeSocks5Failures(t *testing.T) {
	cases := []struct {
		name  string
		reply []byte
		want  string
	}{
		{"auth demanded", []byte{5, 2}, "cleartext"},
		{"refused", []byte{5, 0, 5, 5, 0, 1, 0, 0, 0, 0, 0, 0}, "connection refused"},
		{"no domain names", []byte{5, 0, 5, 8, 0, 1, 0, 0, 0, 0, 0, 0}, "address type not supported"},
		{"unknown reply code", []byte{5, 0, 5, 99, 0, 1, 0, 0, 0, 0, 0, 0}, "reply code 99"},
		{"not a SOCKS port", []byte("HTTP/1.1 400 Bad Request\r\n"), "not a SOCKS5 proxy"},
		{"truncated", []byte{5, 0, 5, 0, 0, 1}, "truncated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn := &scriptConn{r: strings.NewReader(string(c.reply))}
			ops := proxyOps("socks5h://user:pw@proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
				return conn, nil
			})
			r := ops.proxyProbe(context.Background(), nil)
			if r.Status != StatusFail || !strings.Contains(r.Detail, c.want) {
				t.Errorf("SOCKS5 %s = %+v, want FAIL mentioning %q", c.name, r, c.want)
			}
			if strings.Contains(conn.w.String(), "pw") {
				t.Errorf("SOCKS5 handshake leaked credentials: %q", conn.w.String())
			}
		})
	}
}

func TestProxyProbeSocks5Unreachable(t *testing.T) {
	ops := proxyOps("socks5h://proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "cannot reach proxy") {
		t.Errorf("unreachable SOCKS5 proxy = %+v, want FAIL", r)
	}
	if r.Cause != ProxyCauseUnreachable {
		t.Errorf("cause = %q, want %q", r.Cause, ProxyCauseUnreachable)
	}
}

func TestSOCKS5ReplyCausesDistinguishFailureStages(t *testing.T) {
	for _, tc := range []struct {
		code  byte
		cause string
	}{
		{3, ProxyCauseDestinationUnreachable},
		{4, ProxyCauseProxyDNS},
		{5, ProxyCauseDestinationUnreachable},
		{8, ProxyCauseProtocol},
	} {
		conn := &scriptConn{r: strings.NewReader(string([]byte{5, 0, 5, tc.code, 0, 1, 0, 0, 0, 0, 0, 0}))}
		ops := proxyOps("socks5h://proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
			return conn, nil
		})
		if got := ops.proxyProbe(context.Background(), nil).Cause; got != tc.cause {
			t.Errorf("reply %d cause = %q, want %q", tc.code, got, tc.cause)
		}
	}
}

func TestSOCKS5HostUnreachableCauseDependsOnResolutionLocation(t *testing.T) {
	for _, tc := range []struct {
		scheme string
		cause  string
	}{
		{"socks5", ProxyCauseDestinationUnreachable},
		{"socks5h", ProxyCauseProxyDNS},
	} {
		conn := &scriptConn{r: strings.NewReader(string([]byte{5, 0, 5, 4, 0, 1, 0, 0, 0, 0, 0, 0}))}
		ops := proxyOps(tc.scheme+"://proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
			return conn, nil
		})
		if got := ops.proxyProbe(context.Background(), nil).Cause; got != tc.cause {
			t.Errorf("%s reply 4 cause = %q, want %q", tc.scheme, got, tc.cause)
		}
	}
}

func TestSOCKS5RejectsInvalidConnectReplyHeader(t *testing.T) {
	for _, reply := range [][]byte{
		{4, 0, 0, 1, 0, 0, 0, 0, 0, 0},
		{5, 0, 1, 1, 0, 0, 0, 0, 0, 0},
	} {
		conn := &scriptConn{r: strings.NewReader(string(append([]byte{5, 0}, reply...)))}
		ops := proxyOps("socks5h://proxy.corp:1080", func(context.Context, string, string) (net.Conn, error) {
			return conn, nil
		})
		r := ops.proxyProbe(context.Background(), nil)
		if r.Status != StatusFail || r.Cause != ProxyCauseProtocol || !strings.Contains(r.Detail, "invalid CONNECT reply header") {
			t.Errorf("reply %v result = %+v", reply[:4], r)
		}
	}
}

// Go's own ProxyFromEnvironment ignores ALL_PROXY; netdoc must not, or a box
// proxied only through ALL_PROXY reads as having no proxy at all.
func TestProxyFromEnvironmentAllProxy(t *testing.T) {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: probeHost}}
	if u, err := http.ProxyFromEnvironment(req); u != nil || err != nil {
		t.Skipf("test environment already has HTTP(S)_PROXY set (%v, %v)", u, err)
	}
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ALL_PROXY", "")
			t.Setenv("all_proxy", "")
			t.Setenv(name, "socks5h://proxy.corp:1080")
			u, err := proxyFromEnvironment(req)
			if err != nil || u == nil || u.Scheme != "socks5h" || u.Host != "proxy.corp:1080" {
				t.Errorf("proxyFromEnvironment = %v, %v; want socks5h://proxy.corp:1080", u, err)
			}
		})
	}
	// A NO_PROXY hit is why net/http returned nil; falling back to ALL_PROXY
	// there would report a proxy this host would never go through.
	for _, np := range []string{"*", "gstatic.com", ".gstatic.com", "connectivitycheck.gstatic.com"} {
		t.Run("NO_PROXY="+np, func(t *testing.T) {
			t.Setenv("ALL_PROXY", "socks5h://proxy.corp:1080")
			t.Setenv("NO_PROXY", np)
			if u, err := proxyFromEnvironment(req); u != nil || err != nil {
				t.Errorf("proxyFromEnvironment = %v, %v; want nil, nil", u, err)
			}
		})
	}
	t.Run("NO_PROXY miss still falls back", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "socks5h://proxy.corp:1080")
		t.Setenv("NO_PROXY", "example.com,notgstatic.com")
		if u, err := proxyFromEnvironment(req); err != nil || u == nil || u.Host != "proxy.corp:1080" {
			t.Errorf("proxyFromEnvironment = %v, %v; want socks5h://proxy.corp:1080", u, err)
		}
	})
	t.Run("bare host defaults to http", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "proxy.corp:3128")
		u, err := proxyFromEnvironment(req)
		if err != nil || u == nil || u.Scheme != "http" || u.Host != "proxy.corp:3128" {
			t.Errorf("proxyFromEnvironment = %v, %v; want http://proxy.corp:3128", u, err)
		}
	})
}

func TestProxyProbeUnreachable(t *testing.T) {
	var dialed string
	ops := proxyOps("http://proxy.corp", func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = addr
		return nil, errors.New("connection refused")
	})
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "cannot reach proxy") {
		t.Errorf("unreachable proxy = %+v, want FAIL", r)
	}
	if dialed != "proxy.corp:80" {
		t.Errorf("dialed %q, want proxy.corp:80 (default http port)", dialed)
	}
}

func TestProxyProbeMalformedURLFailsWithoutDial(t *testing.T) {
	// The last value is net/http's fallback parse of http://proxy:65536.
	for _, proxy := range []string{"://bad", "http://:3128", "http://proxy:0", "http://proxy:65536", "https://proxy:65536", "http://http://proxy:65536"} {
		t.Run(proxy, func(t *testing.T) {
			ops := proxyOps(proxy, func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("malformed proxy must not be dialed")
				return nil, nil
			})
			r := ops.proxyProbe(context.Background(), nil)
			if r.Status != StatusFail || !strings.Contains(r.Detail, "bad proxy configuration") {
				t.Errorf("malformed proxy = %+v, want FAIL bad proxy configuration", r)
			}
		})
	}
}

// TestProxyProbeParseErrorIsRedacted guards against a regression where the
// raw net/url parser error — which can repeat the malformed HTTPS_PROXY/
// HTTP_PROXY value verbatim, including any embedded credentials — leaks into
// the report. The probe must fail with a fixed, generic detail instead.
func TestProxyProbeParseErrorIsRedacted(t *testing.T) {
	const sentinel = "SENSITIVE_PROXY_VALUE"
	ops := &netops{
		proxyFromEnv: func(*http.Request) (*url.URL, error) {
			return nil, errors.New("parse " + sentinel + ": invalid control character in URL")
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("a proxy parse error must not be dialed")
			return nil, nil
		},
	}
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail {
		t.Errorf("status = %v, want FAIL", r.Status)
	}
	if !strings.Contains(r.Detail, "bad proxy configuration") || !strings.Contains(r.Detail, "HTTPS_PROXY") || !strings.Contains(r.Detail, "HTTP_PROXY") {
		t.Errorf("detail = %q, want an actionable bad proxy configuration message", r.Detail)
	}
	if strings.Contains(r.Detail, sentinel) || strings.Contains(r.Fix, sentinel) {
		t.Errorf("parser error text leaked into report: detail=%q fix=%q", r.Detail, r.Fix)
	}
}

func TestProxyProbeConnectOK(t *testing.T) {
	conn := &scriptConn{r: strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\n")}
	ops := proxyOps("http://proxy.corp:3128", func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r := ops.proxyProbe(ctx, nil)
	if r.Status != StatusPass || !strings.Contains(r.Detail, "proxy proxy.corp:3128 tunnels") {
		t.Errorf("granted CONNECT = %+v, want PASS", r)
	}
	if deadline, _ := ctx.Deadline(); !conn.writeDeadline.Equal(deadline) {
		t.Errorf("CONNECT write deadline = %v, want %v", conn.writeDeadline, deadline)
	}
}

// A deadline-less ctx must not leave the conn with no deadline at all.
func TestProxyProbeDeadlinelessCtxStillBoundsConn(t *testing.T) {
	conn := &scriptConn{r: strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\n")}
	ops := proxyOps("http://proxy.corp:3128", func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	if r := ops.proxyProbe(context.Background(), nil); r.Status != StatusPass {
		t.Fatalf("granted CONNECT = %+v, want PASS", r)
	}
	if conn.writeDeadline.IsZero() {
		t.Error("write deadline is zero, want the probe budget as a fallback leash")
	}
}

func TestProxyProbeRefusesCleartextCredentials(t *testing.T) {
	conn := &scriptConn{r: strings.NewReader("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")}
	ops := proxyOps("http://user:pw@proxy.corp:3128", func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "refusing") {
		t.Errorf("auth over http proxy = %+v, want FAIL refusing cleartext credentials", r)
	}
	if strings.Contains(conn.w.String(), "Proxy-Authorization") {
		t.Errorf("CONNECT sent credentials before refusing:\n%s", conn.w.String())
	}
}

func TestProxyProbeHTTPSCredentialsWaitForChallenge(t *testing.T) {
	first := &scriptConn{r: strings.NewReader("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")}
	second := &scriptConn{r: strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\n")}
	conns := []*scriptConn{first, second}
	ops := proxyOps("https://user:pw@proxy.corp:3128", nil)
	ops.dialTLS = func(context.Context, string, string, *tls.Config) (net.Conn, error) {
		conn := conns[0]
		conns = conns[1:]
		return conn, nil
	}
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusPass {
		t.Fatalf("authenticated CONNECT = %+v, want PASS", r)
	}
	if strings.Contains(first.w.String(), "Proxy-Authorization") {
		t.Errorf("first CONNECT sent credentials preemptively:\n%s", first.w.String())
	}
	if !strings.Contains(second.w.String(), "Proxy-Authorization: Basic dXNlcjpwdw==") {
		t.Errorf("second CONNECT did not answer challenge:\n%s", second.w.String())
	}
}

func TestProxyProbeAuthRequired(t *testing.T) {
	ops := proxyOps("http://proxy.corp:3128", func(context.Context, string, string) (net.Conn, error) {
		return &scriptConn{r: strings.NewReader("HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")}, nil
	})
	r := ops.proxyProbe(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Fix, "credentials") {
		t.Errorf("407 from proxy = %+v, want FAIL with credentials fix", r)
	}
}

// noProxyBypasses matches suffixes and "*" only — no IP literals, no CIDR — and
// is safe solely because probeHost is the one host it is ever asked about. Pin
// that: whatever target the user names, the proxy probe still asks about
// probeHost. If this fails, noProxyBypasses must become httpproxy.
func TestProxyProbeOnlyAsksAboutProbeHost(t *testing.T) {
	targets := []*Target{
		nil,
		{Host: "10.0.0.7", IP: net.ParseIP("10.0.0.7"), Port: 443, Proto: ProtoTLSHTTP},
		{Host: "intranet.corp", Port: 22, Proto: ProtoSSH},
	}
	for _, target := range targets {
		var asked []string
		o := &netops{proxyFromEnv: func(req *http.Request) (*url.URL, error) {
			asked = append(asked, req.URL.Hostname())
			return nil, nil
		}}
		for _, p := range o.buildProbes(target) {
			if p.ID == ProbeProxy {
				p.Run(context.Background(), nil)
			}
		}
		if len(asked) == 0 {
			t.Fatalf("target %+v: proxy probe never consulted the environment", target)
		}
		for _, h := range asked {
			if h != probeHost {
				t.Errorf("target %+v: proxy probe asked about %q, want %q", target, h, probeHost)
			}
		}
	}
}

// HTTP_PROXY-only environments (no HTTPS_PROXY) still count as configured.
func TestProxyProbeFallsBackToHTTP(t *testing.T) {
	ops := &netops{proxyFromEnv: func(req *http.Request) (*url.URL, error) {
		if req.URL.Scheme == "http" {
			return url.Parse("http://proxy:8080")
		}
		return nil, nil
	}, dialContext: func(context.Context, string, string) (net.Conn, error) {
		return &scriptConn{r: strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\n")}, nil
	}}
	if r := ops.proxyProbe(context.Background(), nil); r.Status != StatusPass {
		t.Errorf("HTTP_PROXY fallback = %+v, want PASS", r)
	}
}

// downgradeEgress turns a direct-egress FAIL into WARN only when another path
// proved the network works: target TCP when a target exists, the environment
// proxy, or else DNS.
func TestDowngradeEgress(t *testing.T) {
	cases := []struct {
		name string
		res  map[ProbeID]ProbeResult
		want Status
	}{
		{"generic dns works", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusPass},
		}, StatusWarn},
		{"generic dns fails too", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusFail},
		}, StatusFail},
		{"target tcp works", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusPass}, ProbeTargetTCP: {Status: StatusPass},
		}, StatusWarn},
		{"target tcp works with warnings", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusPass}, ProbeTargetTCP: {Status: StatusWarn},
		}, StatusWarn},
		{"target tcp fails, dns pass not enough", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusPass}, ProbeTargetTCP: {Status: StatusFail},
		}, StatusFail},
		{"egress passing untouched", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusPass}, ProbeDNS: {Status: StatusPass},
		}, StatusPass},
		{"proxy path saves generic", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusFail}, ProbeProxy: {Status: StatusPass},
		}, StatusWarn},
		{"degraded proxy path saves generic", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusFail}, ProbeProxy: {Status: StatusWarn},
		}, StatusWarn},
		{"proxy path saves target", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusPass}, ProbeTargetTCP: {Status: StatusFail}, ProbeProxy: {Status: StatusPass},
		}, StatusWarn},
		{"proxy NA not enough", map[ProbeID]ProbeResult{
			ProbeInternet: {Status: StatusFail}, ProbeDNS: {Status: StatusFail}, ProbeProxy: {Status: StatusNA},
		}, StatusFail},
	}
	for _, c := range cases {
		downgradeEgress(c.res)
		if got := c.res[ProbeInternet].Status; got != c.want {
			t.Errorf("%s: internet status = %v, want %v", c.name, got, c.want)
		}
	}
}

// A dual-stack name on a single-stack network is the common case, not a
// degraded one: the resolver hands out AAAA records an IPv4-only link can
// never reach, and every one of them fails. The connect stays a clean PASS and
// the dead addresses stay visible in Attempts. Sibling failures in the family
// that actually carried the connection still warn.
func TestTargetTCPProbeIgnoresTheAbsentFamily(t *testing.T) {
	v4, v4b, v6 := net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2"), net.ParseIP("2001:db8::1")
	cases := []struct {
		name     string
		addrs    []net.IP
		reach    net.IP // the only address the stub dialer accepts
		want     Status
		attempts int
	}{
		{"v6 unreachable, v4 won", []net.IP{v4, v6}, v4, StatusPass, 2},
		// dialIPs leads with IPv6 (RFC 8305), so a v6 win returns before the
		// staggered v4 attempt ever starts — one attempt, and nothing to warn about.
		{"v4 unreachable, v6 won", []net.IP{v4, v6}, v6, StatusPass, 1},
		{"same-family sibling failed", []net.IP{v4b, v4}, v4, StatusWarn, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ops := &netops{
				dialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
					if host, _, _ := net.SplitHostPort(addr); net.ParseIP(host).Equal(c.reach) {
						return fakeConn{local: &net.TCPAddr{IP: net.ParseIP("192.0.2.7")}}, nil
					}
					return nil, errors.New("network is unreachable")
				},
				interfaces: func() ([]net.Interface, error) {
					return []net.Interface{{Name: "fake0"}}, nil
				},
				interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
					return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)}}, nil
				},
			}
			r := ops.targetTCPProbe(443)(context.Background(), map[ProbeID]ProbeResult{ProbeDNS: {Addrs: c.addrs}})
			if r.Status != c.want {
				t.Errorf("status = %v, want %v (detail %q)", r.Status, c.want, r.Detail)
			}
			if !r.SelectedIP.Equal(c.reach) {
				t.Errorf("selected = %v, want %v", r.SelectedIP, c.reach)
			}
			// Every address tried stays in the report either way.
			if len(r.Attempts) != c.attempts {
				t.Errorf("attempts = %d, want %d", len(r.Attempts), c.attempts)
			}
		})
	}
}

// ifaceProbe failure branches: enumeration error, and interfaces that exist
// but none up (loopback doesn't count).
func TestIfaceProbeFailures(t *testing.T) {
	ops := &netops{interfaces: func() ([]net.Interface, error) {
		return nil, errors.New("EPERM")
	}}
	if r := ops.ifaceProbe(context.Background(), nil); r.Status != StatusFail || !strings.Contains(r.Detail, "cannot list interfaces") {
		t.Errorf("interfaces error = %+v, want FAIL cannot list interfaces", r)
	}

	ops = &netops{interfaces: func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "lo", Flags: net.FlagLoopback | net.FlagUp | net.FlagRunning},
			{Name: "eth0", Flags: 0}, // present but down
		}, nil
	}}
	if r := ops.ifaceProbe(context.Background(), nil); r.Status != StatusFail || r.Detail != "no interface up" {
		t.Errorf("all down = %+v, want FAIL no interface up", r)
	}
}

// targetTCPProbe against the stub dialer: empty DNS input, a clean connect
// (with path identity resolved through the stubbed interface list), and the
// all-addresses-failed fallback.
func TestTargetTCPProbe(t *testing.T) {
	dst := net.ParseIP("192.0.2.1")
	deps := map[ProbeID]ProbeResult{ProbeDNS: {Addrs: []net.IP{dst}}}

	r := (&netops{}).targetTCPProbe(443)(context.Background(), map[ProbeID]ProbeResult{})
	if r.Status != StatusFail || r.Detail != "no resolved addresses" {
		t.Errorf("no addrs = %+v, want FAIL no resolved addresses", r)
	}

	ops := &netops{
		dialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			if network != "tcp4" || addr != "192.0.2.1:443" {
				t.Errorf("dialed %s %s, want tcp4 192.0.2.1:443", network, addr)
			}
			return fakeConn{local: &net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 40000}}, nil
		},
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "fake0"}}, nil
		},
		interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)}}, nil
		},
	}
	r = ops.targetTCPProbe(443)(context.Background(), deps)
	if r.Status != StatusPass || !r.SelectedIP.Equal(dst) || r.Iface != "fake0" {
		t.Errorf("connect = %+v, want PASS pinned to 192.0.2.1 via fake0", r)
	}
	// The fake dial returns instantly, so the detail exercises the Ms floor: a
	// connect that happened must not read the same 0ms as one that never ran.
	if !strings.Contains(r.Detail, "connected to 192.0.2.1:443 in 1ms") {
		t.Errorf("detail = %q, want it to mention the connect at the 1ms floor", r.Detail)
	}

	// Refuse both the TCP attempt and the UDP path-identity fallback.
	ops = &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("refused")
	}}
	r = ops.targetTCPProbe(443)(context.Background(), deps)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "port 443 unreachable on all 1 address(es)") {
		t.Errorf("all refused = %+v, want FAIL port unreachable", r)
	}
	if !strings.Contains(r.Fix, "firewall") {
		t.Errorf("fix = %q, want the firewall hint", r.Fix)
	}
	if len(r.Attempts) != 1 || r.Attempts[0].Err == nil {
		t.Errorf("attempts = %+v, want the single failed attempt recorded", r.Attempts)
	}
}

// Probes hand their results to buildProbes' wrapper, which is the only place
// external text gets sanitized — so a probe that emits raw escapes must come
// out clean anyway.
func TestCleanResultScrubsEveryTextField(t *testing.T) {
	hostile := "boom\x1b[31m\x07"
	r := cleanResult(ProbeResult{
		Detail:   hostile,
		Fix:      hostile,
		Iface:    hostile,
		Network:  hostile,
		Portal:   &Portal{RedirectURL: "https://portal.example/" + hostile},
		Attempts: []Attempt{{Err: errors.New(hostile)}},
	})
	for name, got := range map[string]string{
		"Detail":  r.Detail,
		"Fix":     r.Fix,
		"Iface":   r.Iface,
		"Network": r.Network,
		"Portal":  r.Portal.RedirectURL,
		"Attempt": r.Attempts[0].Err.Error(),
	} {
		want := "boom"
		if name == "Portal" {
			want = "https://portal.example/boom"
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// A measured attempt must never render as the 0ms that means "never ran". Only
// fails where the clock is coarse enough to return 0 for back-to-back calls,
// which is Windows — the platform that regressed TestTargetTCPProbe.
func TestSinceNeverZero(t *testing.T) {
	if d := since(time.Now()); Ms(d) < 1 {
		t.Errorf("since(now) = %v, Ms = %d, want at least 1ms", d, Ms(d))
	}
}
