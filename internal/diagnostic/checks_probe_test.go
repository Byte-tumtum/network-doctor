// Probe behavior against local listeners and stubs: Happy Eyeballs stagger and
// cancellation, DNS/TLS/banner failure paths, and the HTTP header cap.

package diagnostic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// silentConn simulates a server that accepts the connection but never sends a
// banner: every read fails immediately as a deadline timeout, so the test
// doesn't wait out the real 2s read deadline.
type silentConn struct{ fakeConn }

func (silentConn) Read([]byte) (int, error)        { return 0, os.ErrDeadlineExceeded }
func (silentConn) SetReadDeadline(time.Time) error { return nil }

type resetConn struct{ fakeConn }

func (resetConn) Read([]byte) (int, error) {
	return 0, fmt.Errorf("wrapped read: %w", syscall.ECONNRESET)
}
func (resetConn) SetReadDeadline(time.Time) error { return nil }

type deadlineErrorConn struct{ fakeConn }

func (deadlineErrorConn) SetReadDeadline(time.Time) error { return errors.New("unsupported") }

func TestBannerProbeClassifiesWrappedReset(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) { return resetConn{}, nil }}
	probe := ops.bannerProbe(ProbeSSH, "SSH", 22)
	r := probe.Run(context.Background(), map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}})
	if r.Status != StatusFail || r.Cause != ConnectionCauseReset {
		t.Fatalf("reset result = %+v", r)
	}
}

func TestBannerProbeRejectsUnboundedRead(t *testing.T) {
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) { return deadlineErrorConn{}, nil }}
	r := ops.bannerProbe(ProbeSSH, "SSH", 22).Run(context.Background(), map[ProbeID]ProbeResult{
		ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")},
	})
	if r.Status != StatusFail || !strings.Contains(r.Detail, "cannot set banner read deadline") {
		t.Fatalf("unbounded banner read = %+v, want failure", r)
	}
}

func TestDNSFailureCauseUsesStructuredErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"timeout", fmt.Errorf("wrapped: %w", &net.DNSError{IsTimeout: true}), DNSCauseTimeout},
		{"temporary", fmt.Errorf("wrapped: %w", &net.DNSError{IsTemporary: true}), DNSCauseTemporaryFailure},
		{"not found", &net.DNSError{IsNotFound: true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dnsFailureCause(tc.err); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

// Target TCP reports only the addresses dialIPs actually attempted.
func TestTargetTCPProbeAttemptCap(t *testing.T) {
	calls := 0
	ops := &netops{dialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
		if network == "tcp4" {
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
	_ = conn.Close()
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

// A resolver that goes quiet and comes back inside one run is resampled: the
// probe retries a timeout once and reports what the second query saw. NXDOMAIN
// is conclusive, so it costs exactly one query.
func TestDNSProbeRetriesTransientFailure(t *testing.T) {
	timeout := &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true}
	notFound := &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}
	for _, tc := range []struct {
		name     string
		first    error
		want     Status
		attempts int
	}{
		{"recovered resolver passes on the retry", timeout, StatusPass, 2},
		{"down resolver still fails", timeout, StatusFail, 2},
		{"nxdomain is not retried", notFound, StatusFail, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			ops := &netops{lookupIP: func(context.Context, string) ([]net.IP, string, error) {
				attempts++
				if attempts == 1 || tc.want == StatusFail {
					return nil, "", tc.first
				}
				return []net.IP{net.ParseIP("192.0.2.1")}, "", nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), DefaultProbeTimeout)
			defer cancel()
			r := ops.dnsProbe("example.com", nil)(ctx, nil)
			if r.Status != tc.want || attempts != tc.attempts {
				t.Errorf("status = %v after %d lookups, want %v after %d", r.Status, attempts, tc.want, tc.attempts)
			}
		})
	}
}

// The second sample runs alongside the first query rather than in place of it,
// which is what lets both of these hold at once: a resolver that answers late
// but inside the budget keeps its answer, and one that is silent until it
// recovers mid-probe is still asked again in time to hear it.
func TestDNSProbeResamplesWithoutCuttingTheFirstQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		// answers is how long after the probe starts the resolver begins
		// answering; a query sent before then is never answered at all.
		answers time.Duration
		budget  time.Duration
	}{
		{"a late answer is waited out", 0, 500 * time.Millisecond},
		{"a resolver that recovers mid-probe is re-asked", 400 * time.Millisecond, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			start := time.Now()
			ops := &netops{lookupIP: func(ctx context.Context, _ string) ([]net.IP, string, error) {
				attempts.Add(1)
				if time.Since(start) < tc.answers {
					<-ctx.Done() // sent too early to ever be answered
					return nil, "", &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true}
				}
				select {
				case <-time.After(300 * time.Millisecond):
					return []net.IP{net.ParseIP("192.0.2.1")}, "", nil
				case <-ctx.Done():
					return nil, "", &net.DNSError{Err: "i/o timeout", Name: "example.com", IsTimeout: true}
				}
			}}
			ctx, cancel := context.WithTimeout(context.Background(), tc.budget)
			defer cancel()
			if r := ops.dnsProbe("example.com", nil)(ctx, nil); r.Status != StatusPass {
				t.Errorf("status = %v (%s) after %d lookups, want PASS", r.Status, r.Detail, attempts.Load())
			}
		})
	}
}

// The resample is bounded and borrows nothing. A resolver that answers costs
// exactly one query, so the common path pays nothing for the retry; a cancelled
// parent stops the probe at two queries rather than letting the retry outlive
// the context it was handed or wait out a budget that is already gone.
func TestDNSProbeResampleStaysInsideTheParentContext(t *testing.T) {
	answer := func(context.Context, string) ([]net.IP, string, error) {
		return []net.IP{net.ParseIP("192.0.2.1")}, "", nil
	}
	for _, tc := range []struct {
		name     string
		lookup   func(context.Context, string) ([]net.IP, string, error)
		cancel   bool
		want     Status
		attempts int32
	}{
		{"an answered query is not resampled", answer, false, StatusPass, 1},
		{"a cancelled parent stops at the one resample", func(ctx context.Context, _ string) ([]net.IP, string, error) {
			// Bounded rather than a bare <-ctx.Done(): a query handed a context
			// detached from the parent would otherwise wedge here until the
			// suite timeout instead of failing on the assertions below.
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				return []net.IP{net.ParseIP("192.0.2.1")}, "", nil
			}
			return nil, "", &net.DNSError{Err: ctx.Err().Error(), Name: "example.com"}
		}, true, StatusFail, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			var mu sync.Mutex
			var seen []context.Context
			ops := &netops{lookupIP: func(ctx context.Context, host string) ([]net.IP, string, error) {
				attempts.Add(1)
				mu.Lock()
				seen = append(seen, ctx)
				mu.Unlock()
				return tc.lookup(ctx, host)
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			start := time.Now()
			r := ops.dnsProbe("example.com", nil)(ctx, nil)
			// Without a deadline the resample timer is DefaultProbeTimeout/2.
			// Returning well inside that is what proves the probe followed the
			// parent rather than waiting out a budget of its own.
			if elapsed := time.Since(start); tc.cancel && elapsed >= DefaultProbeTimeout/2 {
				t.Errorf("probe took %s after the parent was cancelled", elapsed)
			}
			if r.Status != tc.want || attempts.Load() != tc.attempts {
				t.Errorf("status = %v after %d lookups, want %v after %d", r.Status, attempts.Load(), tc.want, tc.attempts)
			}
			cancel()
			mu.Lock()
			defer mu.Unlock()
			for i, c := range seen {
				select {
				case <-c.Done():
				default:
					t.Errorf("query %d still holds a live context after the parent was cancelled", i+1)
				}
			}
		})
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
			server := ""
			ops := &netops{lookupPublicIP: func(_ context.Context, _, srv string) ([]net.IP, error) {
				called, server = true, srv
				return tc.ips, tc.err
			}}
			r := ops.publicDNSProbe("example.com", tc.litIP, DefaultPublicDNS)(context.Background(), nil)
			if called && server != "8.8.8.8:53" {
				t.Errorf("queried %q, want 8.8.8.8:53", server)
			}
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
	if r.Families == nil || r.Families.IPv4 != FamilyReachable || r.Families.IPv6 != FamilyUnreachable {
		t.Errorf("v4-only families = %+v", r.Families)
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
	if r.Families == nil || r.Families.IPv4 != FamilyUnreachable || r.Families.IPv6 != FamilyUnreachable {
		t.Errorf("down families = %+v", r.Families)
	}
}

func TestInternetProbeAddsBackwardCompatibleRouteCause(t *testing.T) {
	called := false
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no route to host")
		},
		interfaces: func() ([]net.Interface, error) { return nil, nil },
		routeCause: func(destination net.IP) string {
			called = destination.Equal(net.ParseIP("1.1.1.1"))
			return RouteCauseNoDefaultRoute
		},
	}
	r := ops.internetProbe(context.Background(), nil)
	if r.Status != StatusFail || r.Cause != RouteCauseNoDefaultRoute || !called {
		t.Errorf("route-classified egress = %+v, called=%t", r, called)
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
	if r.Status != StatusWarn || r.Cause != FamilyCauseIPv6Unreachable || !strings.Contains(r.Detail, "black-holed") {
		t.Errorf("black-holed IPv6 = %+v, want WARN naming it", r)
	}

	linkLocalOnly := *blackholed
	linkLocalOnly.interfaceAddrs = func(*net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("fe80::1")}}, nil
	}
	if r := linkLocalOnly.internetProbe(context.Background(), nil); r.Status != StatusPass {
		t.Errorf("v4-only network = %+v, want PASS: no global IPv6 means nothing is broken", r)
	}

	ulaOnly := *blackholed
	ulaOnly.interfaceAddrs = func(*net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("fd00::1")}}, nil
	}
	if r := ulaOnly.internetProbe(context.Background(), nil); r.Status != StatusPass {
		t.Errorf("ULA-only network = %+v, want PASS: fc00::/7 was never meant to reach the internet", r)
	}
}

func TestInternetProbeBlackHoledIPv4WithWorkingIPv6(t *testing.T) {
	ops := &netops{
		dialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
			if network == "tcp4" {
				return nil, errors.New("connection timed out")
			}
			return fakeConn{}, nil
		},
		interfaces: func() ([]net.Interface, error) {
			return []net.Interface{{Name: "eth0", Flags: net.FlagUp | net.FlagRunning}}, nil
		},
		interfaceAddrs: func(*net.Interface) ([]net.Addr, error) {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("10.77.0.10"), Mask: net.CIDRMask(24, 32)}}, nil
		},
	}
	r := ops.internetProbe(context.Background(), nil)
	if r.Status != StatusWarn || r.Cause != FamilyCauseIPv4Unreachable || r.Families == nil ||
		r.Families.IPv4 != FamilyUnreachable || r.Families.IPv6 != FamilyReachable {
		t.Errorf("black-holed IPv4 = %+v", r)
	}
}

// dialedNetworks runs the egress probe against a dial stub that always
// succeeds and reports which address families it was actually asked to dial.
// Success everywhere is the point: what the probe declines to attempt is then
// the only thing separating the cases.
func dialedNetworks(t *testing.T, ops *netops, fail map[string]bool) (ProbeResult, string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]bool{}
	ops.interfaces = func() ([]net.Interface, error) { return nil, nil }
	ops.dialContext = func(_ context.Context, network, _ string) (net.Conn, error) {
		mu.Lock()
		seen[network] = true
		mu.Unlock()
		if fail[network] {
			return nil, errors.New("no route to host")
		}
		return fakeConn{}, nil
	}
	r := ops.internetProbe(context.Background(), nil)
	// TCP only: the probe also dials UDP to learn the source address of a
	// failed path, which is not an egress attempt against an endpoint.
	networks := make([]string, 0, len(seen))
	for network := range seen {
		if strings.HasPrefix(network, "tcp") {
			networks = append(networks, network)
		}
	}
	sort.Strings(networks)
	return r, fmt.Sprint(networks)
}

// --iface binds probe traffic to one interface's addresses, so an interface
// holding no address of a family cannot test that family at all. Dialing it
// anyway only proves the bind is impossible, and reporting that as
// FamilyUnreachable accuses the network of an outage nobody measured. The
// incompatible family has to be dropped before the dial, the way the QUIC and
// encrypted-DNS rows already drop theirs.
func TestInternetProbeSkipsFamiliesTheSelectedSourceCannotDial(t *testing.T) {
	v4, v6 := net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::10")
	for _, tc := range []struct {
		name     string
		sources  *SourceAddresses
		want4    string
		want6    string
		wantDial string
	}{
		{name: "unrestricted source tests both families", sources: nil,
			want4: FamilyReachable, want6: FamilyReachable, wantDial: "[tcp4 tcp6]"},
		{name: "dual-stack interface tests both families", sources: &SourceAddresses{IPv4: v4, IPv6: v6, Iface: "eth0"},
			want4: FamilyReachable, want6: FamilyReachable, wantDial: "[tcp4 tcp6]"},
		{name: "IPv4-only interface never dials IPv6", sources: &SourceAddresses{IPv4: v4, Iface: "eth0"},
			want4: FamilyReachable, want6: "", wantDial: "[tcp4]"},
		{name: "IPv6-only interface never dials IPv4", sources: &SourceAddresses{IPv6: v6, Iface: "eth0"},
			want4: "", want6: FamilyReachable, wantDial: "[tcp6]"},
		// An exact local IP sets only its own family, so it reaches this path
		// through the same struct an interface name does.
		{name: "IPv4 literal source never dials IPv6", sources: &SourceAddresses{IPv4: v4},
			want4: FamilyReachable, want6: "", wantDial: "[tcp4]"},
		{name: "IPv6 literal source never dials IPv4", sources: &SourceAddresses{IPv6: v6},
			want4: "", want6: FamilyReachable, wantDial: "[tcp6]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dialed := dialedNetworks(t, &netops{sources: tc.sources}, nil)
			if dialed != tc.wantDial {
				t.Errorf("dialed %s, want %s: an incompatible family must not be attempted", dialed, tc.wantDial)
			}
			if r.Status != StatusPass {
				t.Errorf("status = %s, want PASS: %s", r.Status, r.Detail)
			}
			if r.Families == nil || r.Families.IPv4 != tc.want4 || r.Families.IPv6 != tc.want6 {
				t.Fatalf("families = %+v, want IPv4 %q IPv6 %q", r.Families, tc.want4, tc.want6)
			}
			// "no IPv6 egress" is a claim about the network. A family that was
			// never dialed has earned no such claim.
			for family, state := range map[string]string{"IPv4": tc.want4, "IPv6": tc.want6} {
				if state == "" && strings.Contains(r.Detail, "no "+family+" egress") {
					t.Errorf("detail = %q, must not report %s egress it never tested", r.Detail, family)
				}
			}
		})
	}
}

// The other half of the invariant: a family the selected source can dial is
// judged exactly as before, and only the families actually attempted appear in
// the failure text.
func TestInternetProbeStillFailsFamiliesTheSelectedSourceCanDial(t *testing.T) {
	v4, v6 := net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::10")

	var routed net.IP
	ops := &netops{sources: &SourceAddresses{IPv4: v4, Iface: "eth0"},
		routeCause: func(destination net.IP) string { routed = destination; return RouteCauseNoDefaultRoute }}
	r, dialed := dialedNetworks(t, ops, map[string]bool{"tcp4": true})
	if r.Status != StatusFail || r.Cause != RouteCauseNoDefaultRoute || !routed.Equal(net.ParseIP("1.1.1.1")) {
		t.Errorf("IPv4-only interface with dead IPv4 = %+v, routeCause saw %v, want the unchanged FAIL", r, routed)
	}
	if r.Families == nil || r.Families.IPv4 != FamilyUnreachable || r.Families.IPv6 != "" {
		t.Errorf("families = %+v, want IPv4 unreachable and IPv6 untested", r.Families)
	}
	if dialed != "[tcp4]" || strings.Contains(r.Detail, "2606:4700:4700::1111") {
		t.Errorf("dialed %s, detail = %q: the failure must name only endpoints it tried", dialed, r.Detail)
	}

	// A dual-stack selection has both families available, so one of them going
	// down is a real partial outage and must keep saying so.
	partial := &netops{sources: &SourceAddresses{IPv4: v4, IPv6: v6, Iface: "eth0"}}
	r, dialed = dialedNetworks(t, partial, map[string]bool{"tcp6": true})
	if r.Status != StatusWarn || r.Cause != FamilyCauseIPv6Unreachable || r.Families == nil ||
		r.Families.IPv4 != FamilyReachable || r.Families.IPv6 != FamilyUnreachable {
		t.Errorf("dual-stack with dead IPv6 = %+v, families %+v, want the unchanged black-hole WARN", r, r.Families)
	}
	if dialed != "[tcp4 tcp6]" || !strings.Contains(r.Detail, "no IPv6 egress") {
		t.Errorf("dialed %s, detail = %q: an available family that fails is still unreachable", dialed, r.Detail)
	}
}

func TestDialIPsConstrainsAddressFamily(t *testing.T) {
	var networks []string
	var mu sync.Mutex
	ops := &netops{dialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
		mu.Lock()
		networks = append(networks, network)
		mu.Unlock()
		return nil, errors.New("refused")
	}}
	_, _, _, _ = ops.dialIPs(context.Background(), []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, 443)
	sort.Strings(networks)
	if fmt.Sprint(networks) != "[tcp4 tcp6]" {
		t.Errorf("networks = %v", networks)
	}
}

// A network whose TCP handshakes all succeed is still not online if the 204
// endpoint comes back as anything else: that's a portal answering for it.
func TestInternetProbeCaptivePortal(t *testing.T) {
	dialOK := func(context.Context, string, string) (net.Conn, error) { return fakeConn{}, nil }
	ifaces := func() ([]net.Interface, error) { return nil, nil }

	portal := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, time.Time, error) {
			return http.StatusFound, "https://portal.example/signin", time.Time{}, nil
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
		portalCheck: func(context.Context) (int, string, time.Time, error) {
			return http.StatusNoContent, "", time.Time{}, nil
		},
	}
	if r := clean.internetProbe(context.Background(), nil); r.Status != StatusPass || r.Portal != nil {
		t.Errorf("204 network = %+v, want a plain PASS", r)
	}

	noRedirect := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, time.Time, error) { return http.StatusOK, "", time.Time{}, nil },
	}
	if r := noRedirect.internetProbe(context.Background(), nil); r.Status != StatusFail ||
		r.Portal == nil || r.Portal.RedirectURL != "" {
		t.Errorf("non-redirect interception = %+v, want portal evidence without a URL", r)
	}

	// Portals that drop 443 entirely still answer plain HTTP; the evidence
	// must survive having no handshake to attach it to.
	blocked443 := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("connection refused")
		},
		interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, time.Time, error) {
			return http.StatusFound, "https://portal.example/signin", time.Time{}, nil
		},
	}
	if r := blocked443.internetProbe(context.Background(), nil); r.Status != StatusFail ||
		r.Portal == nil || r.Portal.RedirectURL != "https://portal.example/signin" ||
		!strings.Contains(r.Fix, "sign in") {
		t.Errorf("portal blocking 443 = %+v, want portal evidence, not a bare no-egress verdict", r)
	}

	// An unreachable check is not evidence of a portal, so the dial result stands.
	broken := &netops{
		dialContext: dialOK, interfaces: ifaces,
		portalCheck: func(context.Context) (int, string, time.Time, error) {
			return 0, "", time.Time{}, errors.New("no route to host")
		},
	}
	if r := broken.internetProbe(context.Background(), nil); r.Status != StatusPass || r.Portal != nil {
		t.Errorf("failed check = %+v, want the TCP verdict to stand", r)
	}
}

// A TLS handshake error is a FAIL with the cleaned error in the detail and a
// fix hint, not a panic and not a skip.
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

func TestTLSProbeClassifiesStructuredFailures(t *testing.T) {
	now := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{
		DNSNames:  []string{"secure-target.test"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour),
	}
	tests := []struct {
		name  string
		err   error
		cause string
	}{
		{"expired", x509.CertificateInvalidError{Cert: &x509.Certificate{NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-24 * time.Hour)}, Reason: x509.Expired}, TLSCauseCertificateExpired},
		{"not yet valid", x509.CertificateInvalidError{Cert: &x509.Certificate{NotBefore: now.Add(24 * time.Hour), NotAfter: now.Add(48 * time.Hour)}, Reason: x509.Expired}, TLSCauseCertificateNotYet},
		{"hostname", x509.HostnameError{Certificate: leaf, Host: "wrong.test"}, TLSCauseHostnameMismatch},
		{"unknown issuer", x509.UnknownAuthorityError{Cert: leaf}, TLSCauseUntrustedIssuer},
		{"timeout", context.DeadlineExceeded, TLSCauseTimeout},
		{"closed", io.ErrUnexpectedEOF, TLSCauseConnectionClosed},
		{"reset", syscall.ECONNRESET, TLSCauseConnectionClosed},
		{"refused", syscall.ECONNREFUSED, TLSCauseTCPUnreachable},
		{"protocol", tls.RecordHeaderError{Msg: "not TLS"}, TLSCauseHandshake},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, err := range []error{tc.err, fmt.Errorf("wrapped TLS failure: %w", tc.err)} {
				if got := tlsFailureCause(err, now); got != tc.cause {
					t.Errorf("tlsFailureCause(%T) = %q, want %q", tc.err, got, tc.cause)
				}
			}
		})
	}
}

func TestTLSProbeIncludesBackwardCompatibleCause(t *testing.T) {
	now := time.Now()
	errExpired := x509.CertificateInvalidError{
		Cert:   &x509.Certificate{NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-24 * time.Hour)},
		Reason: x509.Expired,
	}
	ops := &netops{dialTLS: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
		return nil, fmt.Errorf("verify peer: %w", errExpired)
	}}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	r := ops.tlsProbe("secure-target.test", 443)(context.Background(), deps)
	if r.Status != StatusFail || r.Cause != TLSCauseCertificateExpired ||
		!strings.HasPrefix(r.Detail, "TLS handshake to 192.0.2.1 failed:") || r.Fix == "" {
		t.Fatalf("TLS result = %+v", r)
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
		!strings.Contains(r.Fix, "Path MTU row") || r.Cause != TLSCauseTimeout {
		t.Errorf("TLS timeout = %+v, want MTU detail and a pointer at the PMTU row", r)
	}
}

// pmtuTestBudget is the probe budget the stall cases run under. The probe
// derives its write deadline as budget minus pmtuHeadroom, so this same figure
// is how long a descheduled goroutine may sit between the deadline being set
// here and the probe reading it back before the probe gives up with "not enough
// of the probe budget left" instead of measuring the stall under test. The
// stall cases wait it out, so it buys loaded-runner margin at its own cost.
const pmtuTestBudget = pmtuHeadroom + time.Second

// The PMTU probe reads one asymmetry: a payload that must travel as full-size
// segments either drains the (deliberately small) send buffer or stalls in it.
// A stall is the black-hole evidence and never more than a WARN; a peer that
// drains the payload clears the path; a peer that hangs up says nothing either
// way. net.Pipe stands in for the socket, since its writes block until the far end
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
			detail: "stalled after 0 KiB of 24 KiB without draining the measured 4 KiB TCP send buffer",
			fix:    true,
		},
		{
			name:   "payload delivered",
			serve:  func(c net.Conn) { _, _ = io.Copy(io.Discard, c) },
			status: StatusPass,
			detail: "24 KiB drained past the measured 4 KiB TCP send buffer",
		},
		{
			name: "peer hangs up",
			// Take a byte before hanging up: the read blocks until the probe's
			// Write, so the close lands during the write rather than racing the
			// setup that precedes it.
			serve:  func(c net.Conn) { _, _ = c.Read(make([]byte, 1)); _ = c.Close() },
			status: StatusNA,
			detail: "inconclusive; the peer dropped the connection after 0 KiB",
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
				sendBuffer:  func(net.Conn) (int, error) { return pmtuSendBuffer, nil },
				tcpMSS:      func(net.Conn) (int, error) { return 1380, nil },
			}
			deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1"), Iface: "wg0"}}
			// Short budget so the stall case doesn't wait out pmtuWriteWait.
			ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
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

// The evidence has to name the interface MTU it contradicts, since that number is
// what turns "something is dropping packets" into a value to lower.
func TestPMTUProbeWarnNamesInterfaceMTU(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		interfaces:  func() ([]net.Interface, error) { return []net.Interface{{Name: "wg0", MTU: 1420}}, nil },
		sendBuffer:  func(net.Conn) (int, error) { return pmtuSendBuffer, nil },
		tcpMSS:      func(net.Conn) (int, error) { return 1380, nil },
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1"), Iface: "wg0"}}
	ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
	defer cancel()

	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps); !strings.Contains(r.Detail, "wg0 advertises a 1420-byte MTU") {
		t.Errorf("pmtu detail = %q, want the interface MTU named", r.Detail)
	}
	// An unreadable MTU costs the note, not the verdict.
	ops.interfaces = func() ([]net.Interface, error) { return nil, errors.New("nope") }
	client2, server2 := net.Pipe()
	t.Cleanup(func() { _ = server2.Close() })
	ops.dialContext = func(context.Context, string, string) (net.Conn, error) { return client2, nil }
	ctx2, cancel2 := context.WithTimeout(context.Background(), pmtuTestBudget)
	defer cancel2()
	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx2, deps); r.Status != StatusWarn || strings.Contains(r.Detail, "advertises") {
		t.Errorf("pmtu without a readable MTU = %+v, want WARN with no MTU note", r)
	}
}

func TestPMTUProbeSkipsWithoutPinnedIP(t *testing.T) {
	r := new(netops).pmtuProbe(443, ProtoTLSHTTP)(context.Background(), map[ProbeID]ProbeResult{})
	if r.Status != StatusSkip {
		t.Errorf("pmtu without a pinned IP = %+v, want SKIP", r)
	}
}

func TestPMTUProbeDeclinesUnmeasurableSendBuffer(t *testing.T) {
	tests := []struct {
		name   string
		buffer func(net.Conn) (int, error)
		detail string
	}{
		{
			name: "socket option unavailable",
			buffer: func(net.Conn) (int, error) {
				return 0, errors.New("unsupported")
			},
			detail: "cannot read the effective TCP send buffer",
		},
		{
			name: "kernel buffer holds payload",
			buffer: func(net.Conn) (int, error) {
				return pmtuPayloadSize, nil
			},
			detail: "large enough to hold the whole probe locally",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close() })
			ops := &netops{
				dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
				sendBuffer:  tc.buffer,
			}
			deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
			r := ops.pmtuProbe(443, ProtoTLSHTTP)(context.Background(), deps)
			if r.Status != StatusNA || !strings.Contains(r.Detail, tc.detail) {
				t.Errorf("pmtu = %+v, want N/A containing %q", r, tc.detail)
			}
		})
	}
}

// resetWriteConn takes half the payload and then reports the peer's reset, the
// way a real socket does when the far end goes away mid-write.
type resetWriteConn struct{ fakeConn }

func (resetWriteConn) Write(b []byte) (int, error) {
	return len(b) / 2, fmt.Errorf("wrapped write: %w", syscall.ECONNRESET)
}
func (resetWriteConn) SetWriteDeadline(time.Time) error { return nil }

// Where a platform can account for its send queue, the verdict comes from what
// the peer acknowledged and never from the write returning. The distinction is
// the whole probe on Linux, where a socket reporting an 8 KiB send buffer still
// swallows a 24 KiB write with nothing on the wire.
func TestPMTUProbeClassifiesByAcknowledgement(t *testing.T) {
	// A far end that drains the pipe, so every case below gets the same
	// complete, successful Write and can only be told apart by the queue.
	tests := []struct {
		name   string
		queued func(net.Conn) (int, error)
		status Status
		detail string
		fix    bool
	}{
		{
			// The regression: the old logic saw a 24 KiB write accepted past an
			// 8 KiB send buffer and called it PASS. Nothing was acknowledged.
			name:   "write accepted but nothing acknowledged",
			queued: func(net.Conn) (int, error) { return pmtuPayloadSize, nil },
			status: StatusWarn,
			detail: "24 KiB written, none of it acknowledged within",
			fix:    true,
		},
		{
			name:   "payload acknowledged",
			queued: func(net.Conn) (int, error) { return 0, nil },
			status: StatusPass,
			detail: "24 KiB of the 24 KiB payload acknowledged by the peer",
		},
		{
			// Forward progress is forward progress: a full-size segment landed,
			// so the path carries them even with a tail still in flight.
			name:   "still draining at the deadline",
			queued: func(net.Conn) (int, error) { return pmtuPayloadSize - 4096, nil },
			status: StatusPass,
			detail: "4 KiB of the 24 KiB payload acknowledged by the peer",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close() })
			go func() { _, _ = io.Copy(io.Discard, server) }()
			ops := &netops{
				dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
				// The doubled value Linux hands back for a 4 KiB request, and
				// comfortably less than the payload: the old inference had
				// everything it wanted and still got this wrong.
				sendBuffer: func(net.Conn) (int, error) { return 2 * pmtuSendBuffer, nil },
				tcpMSS:     func(net.Conn) (int, error) { return 1448, nil },
				queued:     tc.queued,
			}
			deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
			ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
			defer cancel()

			r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps)
			if r.Status != tc.status || !strings.Contains(r.Detail, tc.detail) {
				t.Errorf("pmtu = %+v, want %v containing %q", r, tc.status, tc.detail)
			}
			if (r.Fix != "") != tc.fix {
				t.Errorf("pmtu fix = %q, want present: %v", r.Fix, tc.fix)
			}
			// The row reports what it measured. Naming the black hole outright
			// is reconciliation's job, once an independent probe agrees.
			if tc.status == StatusWarn && !strings.Contains(r.Detail, "consistent with a path-MTU black hole") {
				t.Errorf("pmtu detail = %q, want hedged black-hole wording", r.Detail)
			}
		})
	}
}

// A healthy path acknowledges nothing at the instant Write returns, because the bytes
// are still in flight. The probe has to wait out that latency instead of
// reading the queue once and calling a working link a black hole.
func TestPMTUProbeWaitsForAcknowledgement(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }()
	var samples int
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		sendBuffer:  func(net.Conn) (int, error) { return 2 * pmtuSendBuffer, nil },
		queued: func(net.Conn) (int, error) {
			// Nothing acknowledged for the first few samples, then the ACKs land.
			if samples++; samples <= 3 {
				return pmtuPayloadSize, nil
			}
			return 0, nil
		},
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
	defer cancel()

	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps); r.Status != StatusPass {
		t.Errorf("delayed acknowledgement = %+v, want PASS", r)
	}
	if samples < 4 {
		t.Errorf("queue sampled %d times, want the probe to keep watching until the ACKs arrive", samples)
	}
}

// A reset purges the send queue, so an empty queue after one reads as a fully
// acknowledged payload. Inconclusive has to win over that.
func TestPMTUProbeReportsResetOverDrainedQueue(t *testing.T) {
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return resetWriteConn{}, nil },
		sendBuffer:  func(net.Conn) (int, error) { return 2 * pmtuSendBuffer, nil },
		queued:      func(net.Conn) (int, error) { return 0, nil },
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}

	r := ops.pmtuProbe(443, ProtoTLSHTTP)(context.Background(), deps)
	if r.Status != StatusNA || !strings.Contains(r.Detail, "the peer dropped the connection") {
		t.Errorf("reset mid-write = %+v, want N/A naming the dropped connection", r)
	}
}

// Windows has no send-queue query, so it keeps the send-buffer inference, and
// says so, because that inference cannot see a black hole on a kernel that
// buffers past the size it reports.
func TestPMTUProbeFallsBackWithoutQueueAccounting(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }()
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		sendBuffer:  func(net.Conn) (int, error) { return pmtuSendBuffer, nil },
		queued:      func(net.Conn) (int, error) { return 0, errors.New("no TCP send-queue accounting on windows") },
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
	defer cancel()

	r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps)
	if r.Status != StatusPass || !strings.Contains(r.Detail, "cannot read the TCP send queue") {
		t.Errorf("pmtu without queue accounting = %+v, want PASS disclosing the limitation", r)
	}
}

// The send buffer only has to be smaller than the payload where it is the
// measurement. With a readable send queue its size is beside the point, and
// bailing out on it would cost the platforms that can actually answer.
func TestPMTUProbeIgnoresSendBufferSizeWithQueueAccounting(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }()
	ops := &netops{
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		sendBuffer:  func(net.Conn) (int, error) { return 4 * pmtuPayloadSize, nil },
		queued:      func(net.Conn) (int, error) { return pmtuPayloadSize, nil },
	}
	deps := map[ProbeID]ProbeResult{ProbeTargetTCP: {SelectedIP: net.ParseIP("192.0.2.1")}}
	ctx, cancel := context.WithTimeout(context.Background(), pmtuTestBudget)
	defer cancel()

	if r := ops.pmtuProbe(443, ProtoTLSHTTP)(ctx, deps); r.Status != StatusWarn {
		t.Errorf("oversized send buffer with queue accounting = %+v, want WARN from the queue reading", r)
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
// fail/skip states, with no nil-deref and no accidental pass.
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

// The transport dials on a goroutine that outlives client.Do when the context
// expires mid-dial, so the failure detail has to name the address from the
// guarded snapshot rather than re-read what that goroutine is still writing.
// Only -race fails on the difference.
func TestHTTPProbeDialOutlivesRequest(t *testing.T) {
	// The dial outlasts the request deadline on its own clock, and nothing hands it
	// the baton, or the handoff would order the very access under test.
	ops := &netops{dialContext: func(context.Context, string, string) (net.Conn, error) {
		time.Sleep(60 * time.Millisecond)
		return nil, errors.New("connection refused")
	}}
	deps := map[ProbeID]ProbeResult{ProbeDNS: {Addrs: []net.IP{net.ParseIP("192.0.2.1")}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	r := ops.httpProbe("example.com", 80, "http", ProbeDNS)(ctx, deps)
	time.Sleep(100 * time.Millisecond) // stay alive for the dial's write, the racing access

	if r.Status != StatusFail || !strings.Contains(r.Detail, "192.0.2.1") {
		t.Errorf("dial outliving the request = %+v, want FAIL naming the address", r)
	}
}

// A real HTTP/2 round trip, with ALPN, TLS, and the h2 framing all genuinely
// negotiated, over in-memory pipes, so nothing binds a port. The server hangs
// up on anything that arrives as HTTP/1.1, which is what makes the PASS
// evidence that the probe's transport actually reached agreement on h2.
func TestHTTPSProbeSupportsHTTP2OnlyServer(t *testing.T) {
	const host = "http2.example"
	cert, roots := selfSignedCert(t, host)
	var overHTTP2 atomic.Bool
	p := newPipeNet(t)
	srv := &http.Server{
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 2 {
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			overHTTP2.Store(true)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	p.serve(t, srv, func() error { return srv.ServeTLS(p, "", "") })

	ops := &netops{dialContext: p.dial, tlsRootCAs: roots}
	deps := map[ProbeID]ProbeResult{ProbeTLS: {SelectedIP: net.ParseIP("192.0.2.10")}}
	r := ops.httpProbe(host, 443, "https", ProbeTLS)(context.Background(), deps)
	if r.Status != StatusPass {
		t.Fatalf("HTTP/2-only HTTPS probe = %+v, want PASS", r)
	}
	if !overHTTP2.Load() {
		t.Error("the request never arrived over HTTP/2; the probe's h2 negotiation is untested")
	}
}

// The real portalCheck round trip over in-memory pipes: the status comes back
// verbatim, a redirect is reported rather than chased, and the proxy env never
// enters the path.
func TestPortalCheck(t *testing.T) {
	// internetProbe only runs the round trip below when the field is wired, so
	// a nil here disables captive-portal detection with nothing else failing.
	if defaultOps.portalCheck == nil {
		t.Fatal("defaultOps.portalCheck is nil; captive-portal detection is silently off")
	}

	var chased bool
	p := newPipeNet(t)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})}
	p.serve(t, srv, func() error { return srv.Serve(p) })

	defer func(orig string) { portalProbeURL = orig }(portalProbeURL)
	const base = "http://portal.example"

	// A proxy that would divert the request if the transport honored it.
	t.Setenv("HTTP_PROXY", "http://192.0.2.9:1")
	t.Setenv("http_proxy", "http://192.0.2.9:1")

	portalProbeURL = base + "/generate_204"
	if code, redirect, _, err := portalCheckWithDial(context.Background(), p.dial); err != nil || code != http.StatusNoContent || redirect != "" {
		t.Errorf("clean path = (%d, %q, %v), want (204, empty, nil) with the proxy env ignored", code, redirect, err)
	}

	portalProbeURL = base + "/redirect"
	if code, redirect, _, err := portalCheckWithDial(context.Background(), p.dial); err != nil || code != http.StatusFound || redirect != base+"/signin" {
		t.Errorf("intercepted path = (%d, %q, %v), want the 302 and resolved HTTP URL", code, redirect, err)
	}
	if chased {
		t.Error("followed the redirect to the sign-in page; the 302 is the answer")
	}

	portalProbeURL = base + "/unsafe"
	if code, redirect, _, err := portalCheckWithDial(context.Background(), p.dial); err != nil || code != http.StatusFound || redirect != "" {
		t.Errorf("unsafe redirect = (%d, %q, %v), want the 302 without a non-HTTP URL", code, redirect, err)
	}

	// Every dial went to the probe URL's own host: the proxy env was ignored,
	// which the pipe dialer can assert directly instead of inferring it from a
	// request that would have failed.
	p.mu.Lock()
	for _, addr := range p.dialed {
		if addr != "portal.example:80" {
			t.Errorf("dialed %q, want portal.example:80: the proxy env leaked into the transport", addr)
		}
	}
	p.mu.Unlock()

	// A dead endpoint is an error, not a zero-status verdict callers can read.
	_ = p.Close()
	if code, redirect, _, err := portalCheckWithDial(context.Background(), p.dial); err == nil || code != 0 || redirect != "" {
		t.Errorf("dead endpoint = (%d, %q, %v), want (0, empty, error)", code, redirect, err)
	}
}

// The Date on the 204 is the only remote clock netdoc gets for free, so the
// round trip has to hand it back verbatim and degrade to the zero time rather
// than to a wrong time when the header is absent or unparsable.
func TestPortalCheckDate(t *testing.T) {
	const stamped = "Sun, 06 Nov 1994 08:49:37 GMT"
	want := time.Date(1994, time.November, 6, 8, 49, 37, 0, time.UTC)

	p := newPipeNet(t)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dated":
			w.Header().Set("Date", stamped)
		case "/nodate":
			// net/http stamps a Date of its own unless the header is removed.
			w.Header()["Date"] = nil
		case "/baddate":
			w.Header().Set("Date", "the day before yesterday")
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	p.serve(t, srv, func() error { return srv.Serve(p) })

	defer func(orig string) { portalProbeURL = orig }(portalProbeURL)
	const base = "http://portal.example"

	for _, c := range []struct {
		path string
		want time.Time
	}{
		{"/dated", want},
		{"/nodate", time.Time{}},
		{"/baddate", time.Time{}},
	} {
		portalProbeURL = base + c.path
		code, _, date, err := portalCheckWithDial(context.Background(), p.dial)
		if err != nil || code != http.StatusNoContent {
			t.Fatalf("%s = (%d, %v), want a clean 204", c.path, code, err)
		}
		if !date.Equal(c.want) {
			t.Errorf("%s date = %v, want %v", c.path, date, c.want)
		}
	}
}

// Remote time is evidence only when it came from the endpoint we addressed.
// Anything but the 204 was written by whatever intercepted the request, and an
// interceptor's clock must never be read as the network's.
func TestInternetProbeClockOffset(t *testing.T) {
	dialOK := func(context.Context, string, string) (net.Conn, error) { return fakeConn{}, nil }
	ifaces := func() ([]net.Interface, error) { return nil, nil }
	behind := time.Now().Add(-3 * time.Hour)

	cases := []struct {
		name     string
		code     int
		date     time.Time
		wantSkew bool
	}{
		{"clean 204 with a date", http.StatusNoContent, behind, true},
		{"clean 204 without a date", http.StatusNoContent, time.Time{}, false},
		{"portal redirect with a date", http.StatusFound, behind, false},
		{"interception answering 200 with a date", http.StatusOK, behind, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &netops{
				dialContext: dialOK, interfaces: ifaces,
				portalCheck: func(context.Context) (int, string, time.Time, error) {
					return c.code, "", c.date, nil
				},
			}
			got := o.internetProbe(context.Background(), nil).clockOffset
			if !c.wantSkew {
				if got != 0 {
					t.Errorf("clockOffset = %v, want no reading", got)
				}
				return
			}
			// The offset carries one in-process round trip, so it is exact to
			// far better than the minute of slack allowed here.
			if diff := (got - 3*time.Hour).Abs(); diff > time.Minute {
				t.Errorf("clockOffset = %v, want about 3h", got)
			}
		})
	}
}

// pipeNet is a net.Listener whose connections come from net.Pipe: dial hands
// the caller the client end and queues the server end for Accept. Real HTTP,
// TLS, ALPN and h2 framing included, runs over it without binding a port, so
// the round trips stay deterministic and independent of host network state.
// A closed pipeNet refuses dials, which is this fake's "connection refused".
type pipeNet struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once

	mu     sync.Mutex
	dialed []string
}

func newPipeNet(t *testing.T) *pipeNet {
	p := &pipeNet{conns: make(chan net.Conn), closed: make(chan struct{})}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func (p *pipeNet) Accept() (net.Conn, error) {
	select {
	case c := <-p.conns:
		return c, nil
	case <-p.closed:
		return nil, net.ErrClosed
	}
}

func (p *pipeNet) Close() error { p.once.Do(func() { close(p.closed) }); return nil }

// Addr is never read for routing; net.Pipe supplies the conns' own addresses.
func (p *pipeNet) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "pipe"} }

// dial is the netops.dialContext stand-in. It records the address it was asked
// for, the only way to tell a proxied request from a direct one when the
// transport's destination no longer decides where the bytes go.
func (p *pipeNet) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	p.mu.Lock()
	p.dialed = append(p.dialed, addr)
	p.mu.Unlock()

	client, server := net.Pipe()
	select {
	case p.conns <- server:
		return client, nil
	case <-p.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

// serve runs srv on this listener until the test ends. Callers pass run so a
// TLS server can go through http.Server.ServeTLS, which is what installs the
// h2 next-proto handler that a hand-rolled tls.Server would miss.
func (p *pipeNet) serve(t *testing.T, srv *http.Server, run func() error) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A serve loop that has stopped must stop accepting too, or the next
		// dial parks on an unbuffered channel until the whole package times out
		// and buries the error that stopped it.
		defer func() { _ = p.Close() }()
		if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
	})
}

// selfSignedCert mints a throwaway leaf for host plus the pool that trusts it,
// so a TLS round trip needs neither fixture files nor the host's trust store.
func selfSignedCert(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, roots
}
