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
	"net"
	"strconv"
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

	conn, err := opsFromSource(net.ParseIP("127.0.0.1")).dialContext(
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
