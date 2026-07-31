// Probe behavior against local listeners and stubs: Happy Eyeballs stagger and
// cancellation, DNS/TLS/banner failure paths, and the HTTP header cap.

package diagnostic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// silentConn simulates a server that accepts the connection but never sends a
// banner: every read fails immediately as a deadline timeout, so the test
// doesn't wait out the real 2s read deadline.
type silentConn struct{ fakeConn }

func (silentConn) Read([]byte) (int, error)        { return 0, os.ErrDeadlineExceeded }
func (silentConn) SetReadDeadline(time.Time) error { return nil }

// Target TCP reports only the addresses dialIPs actually attempted.
func TestTargetTCPProbeAttemptCap(t *testing.T) {
	calls := 0
	ops := &netops{dialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
		if network == "tcp" {
			calls++
		}
		return nil, errors.New("connection refused")
	}}
	ips := make([]net.IP, maxAttempts+4)
	for i := range ips {
		ips[i] = net.ParseIP(fmt.Sprintf("192.0.2.%d", i+1))
	}

	r := ops.targetTCPProbe(80)(context.Background(), map[ProbeID]ProbeResult{ProbeDNS: {Addrs: ips}})
	if calls != maxAttempts || len(r.Attempts) != maxAttempts {
		t.Errorf("calls = %d, attempts = %d, want %d each", calls, len(r.Attempts), maxAttempts)
	}
	want := fmt.Sprintf("port 80 unreachable on all %d address(es): %s", len(r.Attempts), joinIPs(ips[:maxAttempts]))
	if r.Detail != want {
		t.Errorf("detail = %q, want %q", r.Detail, want)
	}
}

// A cancelled context dials nothing instead of grinding through addresses.
func TestDialIPsCancelledStopsEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, context.Canceled
	}}
	ips := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.3")}

	conn, _, attempts, _ := ops.dialIPs(ctx, ips, 80)
	if conn != nil {
		t.Fatal("expected no connection under a cancelled context")
	}
	if calls != 0 || len(attempts) != 0 {
		t.Errorf("calls = %d, attempts = %d, want 0 each (cancelled ctx must not dial)", calls, len(attempts))
	}
}

// Happy Eyeballs: while an early address hangs, a later one is started after
// the stagger delay and its success wins without waiting out the first.
func TestDialIPsRacesStaggered(t *testing.T) {
	win := net.ParseIP("192.0.2.2")
	ops := &netops{dialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "192.0.2.1") {
			<-ctx.Done() // first address black-holes
			return nil, ctx.Err()
		}
		return fakeConn{}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	conn, sel, _, _ := ops.dialIPs(ctx, []net.IP{net.ParseIP("192.0.2.1"), win}, 80)
	if conn == nil || !sel.Equal(win) {
		t.Fatalf("sel = %v, want the second address to win the race", sel)
	}
	conn.Close()
	if e := time.Since(start); e > 2*time.Second {
		t.Errorf("race took %v, want well under the hung address's deadline", e)
	}
}

// Addresses are interleaved by family, IPv6 first, per RFC 8305.
func TestInterleaveFamilies(t *testing.T) {
	got := interleaveFamilies([]net.IP{
		net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2"),
		net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8::2"),
	})
	want := []string{"2001:db8::1", "192.0.2.1", "2001:db8::2", "192.0.2.2"}
	for i, w := range want {
		if got[i].String() != w {
			t.Fatalf("interleave[%d] = %v, want %v (full: %v)", i, got[i], w, got)
		}
	}
}

// DNS failure modes: resolver error and an empty (no A record) answer both
// fail with an actionable detail, never panic or pass.
func TestDNSProbeErrors(t *testing.T) {
	ops := &netops{lookupIP: func(context.Context, string) ([]net.IP, string, error) {
		return nil, "192.168.1.1:53", errors.New("SERVFAIL")
	}}
	r := ops.dnsProbe("example.com", nil)(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "cannot resolve example.com via 192.168.1.1") || r.Fix == "" {
		t.Errorf("lookup error = %+v, want FAIL naming the resolver, plus a fix", r)
	}

	ops.lookupIP = func(context.Context, string) ([]net.IP, string, error) { return nil, "", nil }
	r = ops.dnsProbe("example.com", nil)(context.Background(), nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "no A/AAAA records") {
		t.Errorf("empty answer = %+v, want FAIL with 'no A/AAAA records'", r)
	}
}

// The DNS row names the resolver that answered when the platform reveals it, and
// says nothing rather than "unknown" when it doesn't (Windows).
func TestDNSProbeNamesResolver(t *testing.T) {
	for _, tc := range []struct {
		name, server, want string
	}{
		{"standard port is bare", "192.168.1.1:53", "example.com → 192.0.2.1 (via 192.168.1.1)"},
		{"odd port is kept", "127.0.0.1:5353", "example.com → 192.0.2.1 (via 127.0.0.1:5353)"},
		{"IPv6 resolver", "[2001:db8::1]:53", "example.com → 192.0.2.1 (via 2001:db8::1)"},
		{"unknown resolver omitted", "", "example.com → 192.0.2.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &netops{lookupIP: func(context.Context, string) ([]net.IP, string, error) {
				return []net.IP{net.ParseIP("192.0.2.1")}, tc.server, nil
			}}
			r := ops.dnsProbe("example.com", nil)(context.Background(), nil)
			if r.Status != StatusPass || r.Detail != tc.want {
				t.Errorf("detail = %q (%v), want %q", r.Detail, r.Status, tc.want)
			}
		})
	}
}

func TestPublicDNSProbe(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}
	for _, tc := range []struct {
		name    string
		ips     []net.IP
		err     error
		litIP   net.IP
		status  Status
		missing bool
	}{
		{"answer", []net.IP{net.ParseIP("192.0.2.1")}, nil, nil, StatusPass, false},
		{"nxdomain is evidence", nil, notFound, nil, StatusPass, true},
		{"unreachable is unavailable", nil, errors.New("network unreachable"), nil, StatusNA, false},
		{"literal is not applicable", nil, nil, net.ParseIP("192.0.2.2"), StatusNA, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			ops := &netops{lookupPublicIP: func(context.Context, string) ([]net.IP, error) {
				called = true
				return tc.ips, tc.err
			}}
			r := ops.publicDNSProbe("example.com", tc.litIP)(context.Background(), nil)
			if r.Status != tc.status || r.DNSNotFound != tc.missing {
				t.Errorf("result = %+v, want status %s, not-found %v", r, tc.status, tc.missing)
			}
			if tc.litIP != nil && called {
				t.Error("literal IP must not contact public DNS")
			}
		})
	}
}

// The egress probe diagnoses each family independently: IPv4 up + IPv6 down is
// a PASS that names the missing family; both down is a FAIL naming both.
func TestInternetProbeFamilies(t *testing.T) {
	v4only := &netops{
		dialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "[") { // IPv6 endpoints are bracketed
				return nil, errors.New("no route to host")
			}
			return fakeConn{}, nil
		},
		interfaces: func() ([]net.Interface, error) { return nil, nil },
	}
	r := v4only.internetProbe(context.Background(), nil)
	if r.Status != StatusPass || !strings.Contains(r.Detail, "IPv4 egress via") || !strings.Contains(r.Detail, "no IPv6 egress") {
		t.Errorf("v4-only network = %+v, want PASS naming the missing IPv6 egress", r)
	}

	down := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no route to host")
		},
		interfaces: func() ([]net.Interface, error) { return nil, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r = down.internetProbe(ctx, nil)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "1.1.1.1") || !strings.Contains(r.Detail, "2606:4700:4700::1111") {
		t.Errorf("both families down = %+v, want FAIL naming endpoints from both families", r)
	}
}

// Same dial outcome, two verdicts: a v4-only network passes, but a machine
// holding a global IPv6 address with no IPv6 egress is black-holed and warns.
func TestInternetProbeBlackHoledIPv6(t *testing.T) {
	blackholed := &netops{
		dialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "[") {
				return nil, errors.New("connection timed out")
			}
			return fakeConn{}, nil
		},
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "eth0", Flags: net.FlagUp | net.FlagRunning}}, nil
		},
		interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("fe80::1")},     // link-local doesn't count
				&net.IPNet{IP: net.ParseIP("2001:db8::1")}, // this does
			}, nil
		},
	}
	r := blackholed.internetProbe(context.Background(), nil)
	if r.Status != StatusWarn || !strings.Contains(r.Detail, "black-holed") {
		t.Errorf("black-holed IPv6 = %+v, want WARN naming it", r)
	}

	linkLocalOnly := *blackholed
	linkLocalOnly.interfaceAddrs = func(*net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("fe80::1")}}, nil
	}
	if r := linkLocalOnly.internetProbe(context.Background(), nil); r.Status != StatusPass {
		t.Errorf("v4-only network = %+v, want PASS — no global IPv6 means nothing is broken", r)
	}
}

// A network whose TCP handshakes all succeed is still not online if the 204
// endpoint comes back as anything else: that's a portal answering for it.
func TestInternetProbeCaptivePortal(t *testing.T) {
	dialOK := func(context.Context, string, string) (net.Conn, error) { return fakeConn{}, nil }
	ifaces := func() ([]net.Interface, error) { return nil, nil }

	portal := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, error) {
			return http.StatusFound, "https://portal.example/signin", nil
		},
	}
	r := portal.internetProbe(context.Background(), nil)
	if r.Status != StatusFail || r.Portal == nil || r.Portal.RedirectURL != "https://portal.example/signin" ||
		r.Fix == "" || !strings.Contains(r.Detail, "intercepted") {
		t.Errorf("portal network = %+v, want FAIL flagged as a portal with a fix", r)
	}
	// And the exemption holds: DNS answering must not launder it into a Warn.
	res := map[ProbeID]ProbeResult{ProbeInternet: r, ProbeDNS: {Status: StatusPass}}
	downgradeEgress(res)
	if res[ProbeInternet].Status != StatusFail {
		t.Errorf("downgraded portal to %v, want FAIL to survive a passing DNS", res[ProbeInternet].Status)
	}

	clean := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, error) { return http.StatusNoContent, "", nil },
	}
	if r := clean.internetProbe(context.Background(), nil); r.Status != StatusPass || r.Portal != nil {
		t.Errorf("204 network = %+v, want a plain PASS", r)
	}

	noRedirect := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, error) { return http.StatusOK, "", nil },
	}
	if r := noRedirect.internetProbe(context.Background(), nil); r.Status != StatusFail ||
		r.Portal == nil || r.Portal.RedirectURL != "" {
		t.Errorf("non-redirect interception = %+v, want portal evidence without a URL", r)
	}

	// An unreachable check is not evidence of a portal — the dial result stands.
	broken := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, error) {
			return 0, "", errors.New("no route to host")
		},
	}
	if r := broken.internetProbe(context.Background(), nil); r.Status != StatusPass || r.Portal != nil {
		t.Errorf("failed check = %+v, want the TCP verdict to stand", r)
	}
}

// A TLS handshake error is a FAIL with the cleaned error in the detail and a
// fix hint — not a panic and not a skip.
func TestTLSProbeHandshakeFailure(t *testing.T) {
	ops := &netops{dialTLS: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
		return nil, errors.New("x509: certificate has expired")
	}}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}

	r := ops.tlsProbe("example.com", 443)(context.Background(), deps)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "TLS handshake to 192.0.2.1 failed") ||
		!strings.Contains(r.Detail, "certificate has expired") || r.Fix == "" {
		t.Errorf("handshake failure = %+v, want FAIL with error detail and a fix", r)
	}
}

func TestTLSProbeTimeoutReportsMTU(t *testing.T) {
	ops := &netops{
		dialTLS: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "fake0", MTU: 1420}}, nil
		},
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {
		SelectedIP: net.ParseIP("192.0.2.1"),
		Iface:      "fake0",
	}}

	r := ops.tlsProbe("example.com", 443)(context.Background(), deps)
	if !strings.Contains(r.Detail, "fake0 MTU is 1420") ||
		!strings.Contains(r.Fix, "Path MTU row") {
		t.Errorf("TLS timeout = %+v, want MTU detail and a pointer at the PMTU row", r)
	}
}

// The PMTU probe reads one asymmetry: a payload that must travel as full-size
// segments either drains the (deliberately small) send buffer or stalls in it.
// A stall is the black-hole evidence and never more than a WARN; a peer that
// drains the payload clears the path; a peer that hangs up says nothing either
// way. net.Pipe stands in for the socket — its writes block until the far end
// reads, which is exactly the behavior being classified.
func TestPMTUProbeClassifiesWrite(t *testing.T) {
	tests := []struct {
		name   string
		serve  func(net.Conn) // runs against the far end of the pipe
		status Status
		detail string
		fix    bool
	}{
		{
			name:   "nothing acknowledged",
			serve:  func(net.Conn) {}, // a black hole: our segments never land
			status: StatusWarn,
			detail: "stalled after 0 KiB of 24 KiB with a 4 KiB send buffer",
			fix:    true,
		},
		{
			name:   "payload delivered",
			serve:  func(c net.Conn) { _, _ = io.Copy(io.Discard, c) },
			status: StatusPass,
			detail: "24 KiB went out in full-size segments",
		},
		{
			name:   "peer hangs up",
			serve:  func(c net.Conn) { c.Close() },
			status: StatusNA,
			detail: "inconclusive — the peer dropped the connection after 0 KiB",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close() })
			go tc.serve(server)
			ops := &netops{
				dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
				interfaces:  func() ([]net.Interface, error) { return []net.Interface{{Name: "wg0", MTU: 1420}}, nil },
			}
			deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1"), Iface: "wg0"}}
			// Short budget so the stall case doesn't wait out pmtuWriteWait.
			ctx, cancel := context.WithTimeout(context.Background(), pmtuHeadroom+300*time.Millisecond)
			defer cancel()

			r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps)
			if r.Status != tc.status || !strings.Contains(r.Detail, tc.detail) {
				t.Errorf("pmtu = %+v, want %v containing %q", r, tc.status, tc.detail)
			}
			if (r.Fix != "") != tc.fix {
				t.Errorf("pmtu fix = %q, want present: %v", r.Fix, tc.fix)
			}
			if !r.SelectedIP.Equal(net.ParseIP("192.0.2.1")) {
				t.Errorf("SelectedIP = %v, want the pinned dependency IP", r.SelectedIP)
			}
		})
	}
}

// The evidence has to name the interface MTU it contradicts — that number is
// what turns "something is dropping packets" into a value to lower.
func TestPMTUProbeWarnNamesInterfaceMTU(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		interfaces:  func() ([]net.Interface, error) { return []net.Interface{{Name: "wg0", MTU: 1420}}, nil },
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1"), Iface: "wg0"}}
	ctx, cancel := context.WithTimeout(context.Background(), pmtuHeadroom+300*time.Millisecond)
	defer cancel()

	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps); !strings.Contains(r.Detail, "wg0 advertises a 1420-byte MTU") {
		t.Errorf("pmtu detail = %q, want the interface MTU named", r.Detail)
	}
	// An unreadable MTU costs the note, not the verdict.
	ops.interfaces = func() ([]net.Interface, error) { return nil, errors.New("nope") }
	client2, server2 := net.Pipe()
	t.Cleanup(func() { _ = server2.Close() })
	ops.dialContext = func(context.Context, string, string) (net.Conn, error) { return client2, nil }
	ctx2, cancel2 := context.WithTimeout(context.Background(), pmtuHeadroom+300*time.Millisecond)
	defer cancel2()
	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx2, deps); r.Status != StatusWarn || strings.Contains(r.Detail, "MTU") {
		t.Errorf("pmtu without a readable MTU = %+v, want WARN with no MTU note", r)
	}
}

func TestPMTUProbeSkipsWithoutPinnedIP(t *testing.T) {
	r := new(netops).pmtuProbe(443, ProtoTLSHTTP)(context.Background(), map[ProbeID]ProbeResult{})
	if r.Status != StatusSkip {
		t.Errorf("pmtu without a pinned IP = %+v, want SKIP", r)
	}
}

// The payload is sized exactly, and only a TLS target gets the record header
// that keeps an OpenSSL server reading long enough to acknowledge it.
func TestPMTUPayloadShape(t *testing.T) {
	tlsPayload, plain := pmtuPayload(ProtoTLSHTTP), pmtuPayload(ProtoSSH)
	if len(tlsPayload) != pmtuPayloadSize || len(plain) != pmtuPayloadSize {
		t.Fatalf("payload sizes = %d, %d, want %d", len(tlsPayload), len(plain), pmtuPayloadSize)
	}
	if !bytes.HasPrefix(tlsPayload, tlsRecordHeader) {
		t.Error("TLS payload does not start with a TLS record header")
	}
	if bytes.HasPrefix(plain, tlsRecordHeader) {
		t.Error("non-TLS payload should not claim to be a TLS record")
	}
	if !bytes.Contains(plain, []byte("netdoc path-mtu probe")) {
		t.Error("payload should identify itself in a capture")
	}
}

// A server that connects but never sends a banner is a WARN (the port
// answered, the service didn't) with the explicit no-banner detail, once the
// read deadline hits.
func TestBannerProbeReadTimeout(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		return silentConn{}, nil
	}}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}

	r := ops.bannerProbe(ProbeSSH, "SSH banner", 22).Run(context.Background(), deps)
	if r.Status != StatusWarn || r.Detail != "connected, no banner within deadline" {
		t.Errorf("silent server = %+v, want WARN with no-banner detail", r)
	}
	if !r.SelectedIP.Equal(net.ParseIP("192.0.2.1")) {
		t.Errorf("SelectedIP = %v, want the pinned dependency IP", r.SelectedIP)
	}
}

func TestBannerProbeReadTimeoutHonorsContext(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		return client, nil
	}}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	r := ops.bannerProbe(ProbeSSH, "SSH banner", 22).Run(ctx, deps)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("banner probe took %v, want context deadline to cap the read", elapsed)
	}
	if r.Status != StatusWarn {
		t.Errorf("silent server = %+v, want WARN", r)
	}
}

func TestBannerProbeValidatesProtocol(t *testing.T) {
	tests := []struct {
		name   string
		id     ProbeID
		banner string
		want   Status
	}{
		{"SSH identification", ProbeSSH, "SSH-2.0-OpenSSH_9.7\r\n", StatusPass},
		{"SSH impostor", ProbeSSH, "220 mail.example ESMTP\r\n", StatusFail},
		{"SMTP greeting", ProbeSMTP, "220 mail.example ESMTP\r\n", StatusPass},
		{"SMTP multiline greeting", ProbeSMTP, "220-mail.example ESMTP\r\n", StatusPass},
		{"SMTP impostor", ProbeSMTP, "SSH-2.0-OpenSSH_9.7\r\n", StatusFail},
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
				return &scriptConn{r: strings.NewReader(tt.banner)}, nil
			}}
			r := ops.bannerProbe(tt.id, "service banner", 22).Run(context.Background(), deps)
			if r.Status != tt.want {
				t.Errorf("status = %v, detail = %q, want %v", r.Status, r.Detail, tt.want)
			}
		})
	}
}

// Dependent probes fed an empty/zero dependency map degrade to their explicit
// fail/skip states — no nil-deref, no accidental pass.
func TestProbesMalformedDeps(t *testing.T) {
	ops := &netops{}
	empty := map[ProbeID]ProbeResult{}
	ctx := context.Background()

	if r := ops.targetTCPProbe(443)(ctx, empty); r.Status != StatusFail || !strings.Contains(r.Detail, "no resolved addresses") {
		t.Errorf("targetTCP without DNS result = %+v, want FAIL 'no resolved addresses'", r)
	}
	if r := ops.tlsProbe("example.com", 443)(ctx, empty); r.Status != StatusSkip {
		t.Errorf("tls without pinned IP = %+v, want SKIP", r)
	}
	if r := ops.httpProbe("example.com", 80, "http", ProbeDNS)(ctx, empty); r.Status != StatusSkip {
		t.Errorf("http without DNS addrs = %+v, want SKIP", r)
	}
	if r := ops.httpProbe("example.com", 443, "https", ProbeTLS)(ctx, empty); r.Status != StatusSkip {
		t.Errorf("https without TLS pinned IP = %+v, want SKIP", r)
	}
	if r := ops.bannerProbe(ProbeSSH, "SSH banner", 22).Run(ctx, empty); r.Status != StatusSkip {
		t.Errorf("banner without pinned IP = %+v, want SKIP", r)
	}
}

// A response whose headers blow past MaxResponseHeaderBytes fails the HTTP
// probe instead of buffering unbounded attacker-controlled bytes.
func TestHTTPProbeHeaderLimit(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			br := bufio.NewReader(server)
			for { // consume the HEAD request up to the blank line
				line, err := br.ReadString('\n')
				if err != nil || line == "\r\n" {
					break
				}
			}
			// 128 KiB header, double the transport's 64 KiB cap.
			_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\nX-Big: " + strings.Repeat("a", 128<<10) + "\r\n\r\n"))
		}()
		return client, nil
	}}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := ops.httpProbe("example.com", 80, "http", ProbeTargetTCP)(ctx, deps)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "no HTTP response from 192.0.2.1") || !strings.Contains(r.Detail, "exceeded") {
		t.Errorf("oversized headers = %+v, want FAIL naming the address and the exceeded header limit", r)
	}
}

// A failing stage names the address it used, so the reader doesn't have to
// cross-reference the DNS row to find out what was actually dialed. When no
// address connected there is no winner to name, so all of them are listed.
func TestHTTPProbeFailureNamesAddresses(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}}
	addrs := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}
	deps := map[ProbeID]ProbeResult{ProbeDNS: {Addrs: addrs}}

	r := ops.httpProbe("example.com", 80, "http", ProbeDNS)(context.Background(), deps)
	if r.Status != StatusFail || !strings.Contains(r.Detail, "192.0.2.1, 192.0.2.2") {
		t.Errorf("total dial failure = %+v, want FAIL listing every attempted address", r)
	}
}

func TestHTTPSProbeSupportsHTTP2OnlyServer(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())

	host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	ops := &netops{dialContext: new(net.Dialer).DialContext, tlsRootCAs: roots}
	deps := map[ProbeID]ProbeResult{ProbeTLS: {SelectedIP: net.ParseIP(host)}}
	r := ops.httpProbe(host, port, "https", ProbeTLS)(context.Background(), deps)
	if r.Status != StatusPass {
		t.Fatalf("HTTP/2-only HTTPS probe = %+v, want PASS", r)
	}
}

// The real portalCheck round trip: the status comes back verbatim, a redirect
// is reported rather than chased, and the proxy env never enters the path.
func TestPortalCheck(t *testing.T) {
	var chased bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/generate_204":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, r, "/signin", http.StatusFound)
		case "/unsafe":
			w.Header().Set("Location", "file:///tmp/signin")
			w.WriteHeader(http.StatusFound)
		case "/signin":
			chased = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)

	defer func(orig string) { portalProbeURL = orig }(portalProbeURL)

	// A proxy that would break the request if the transport honored it.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")

	portalProbeURL = server.URL + "/generate_204"
	if code, redirect, err := defaultOps.portalCheck(context.Background()); err != nil || code != http.StatusNoContent || redirect != "" {
		t.Errorf("clean path = (%d, %q, %v), want (204, empty, nil) with the proxy env ignored", code, redirect, err)
	}

	portalProbeURL = server.URL + "/redirect"
	if code, redirect, err := defaultOps.portalCheck(context.Background()); err != nil || code != http.StatusFound || redirect != server.URL+"/signin" {
		t.Errorf("intercepted path = (%d, %q, %v), want the 302 and resolved HTTP URL", code, redirect, err)
	}
	if chased {
		t.Error("followed the redirect to the sign-in page; the 302 is the answer")
	}

	portalProbeURL = server.URL + "/unsafe"
	if code, redirect, err := defaultOps.portalCheck(context.Background()); err != nil || code != http.StatusFound || redirect != "" {
		t.Errorf("unsafe redirect = (%d, %q, %v), want the 302 without a non-HTTP URL", code, redirect, err)
	}

	// A dead endpoint is an error, not a zero-status verdict callers can read.
	server.Close()
	if code, redirect, err := defaultOps.portalCheck(context.Background()); err == nil || code != 0 || redirect != "" {
		t.Errorf("dead endpoint = (%d, %q, %v), want (0, empty, error)", code, redirect, err)
	}
}
