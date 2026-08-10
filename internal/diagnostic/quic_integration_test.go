//go:build integration

package diagnostic

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

func TestQUICProbeValidExchangePasses(t *testing.T) {
	const host = "quic.test"
	cert, roots := selfSignedCert(t, host)
	udp := startQUICFixture(t, cert, []string{"h3"})

	ops := *defaultOps
	ops.tlsRootCAs = roots
	ops.lookupIP = loopbackQUICLookup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := ops.quicProbe(host, udp.LocalAddr().(*net.UDPAddr).Port)(ctx, nil)
	if r.Status != StatusPass || r.Cause != "" || !r.SelectedIP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("result = %+v", r)
	}
}

func TestQUICProbeRejectsUnauthenticatedWrongHostAndWrongALPN(t *testing.T) {
	for _, tc := range []struct {
		name         string
		certificate  string
		probeHost    string
		serverProtos []string
		trustRoots   bool
	}{
		{name: "untrusted CA", certificate: "quic.test", probeHost: "quic.test", serverProtos: []string{"h3"}},
		{name: "wrong hostname", certificate: "other.test", probeHost: "quic.test", serverProtos: []string{"h3"}, trustRoots: true},
		{name: "wrong ALPN", certificate: "quic.test", probeHost: "quic.test", serverProtos: []string{"not-h3"}, trustRoots: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, roots := selfSignedCert(t, tc.certificate)
			udp := startQUICFixture(t, cert, tc.serverProtos)
			ops := *defaultOps
			if tc.trustRoots {
				ops.tlsRootCAs = roots
			}
			ops.lookupIP = loopbackQUICLookup
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			r := ops.quicProbe(tc.probeHost, udp.LocalAddr().(*net.UDPAddr).Port)(ctx, nil)
			if r.Status != StatusFail || r.Cause != QUICCauseHandshake {
				t.Fatalf("result = %+v, want authenticated h3 handshake failure", r)
			}
		})
	}
}

func startQUICFixture(t *testing.T, cert tls.Certificate, nextProtos []string) *net.UDPConn {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	listener, err := quic.Listen(udp, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: nextProtos}, nil)
	if err != nil {
		udp.Close()
		t.Fatalf("listen QUIC: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			_ = conn.CloseWithError(0, "fixture complete")
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		_ = udp.Close()
		<-done
	})
	return udp
}

func TestQUICProbeSilentUDPDropCannotPass(t *testing.T) {
	udp, received := startUDPFixture(t, nil)
	ops := quicLoopbackOps()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result := make(chan ProbeResult, 1)
	go func() { result <- ops.quicProbe("quic.test", udp.LocalAddr().(*net.UDPAddr).Port)(ctx, nil) }()
	<-received // the UDP Write succeeded and reached the fixture
	r := <-result
	if r.Status != StatusFail || r.Cause != QUICCauseTimeout {
		t.Fatalf("result after successful UDP Write and silent drop = %+v", r)
	}
}

func TestQUICProbeMalformedResponseCannotPass(t *testing.T) {
	udp, received := startUDPFixture(t, []byte("unrelated UDP response"))
	ops := quicLoopbackOps()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r := ops.quicProbe("quic.test", udp.LocalAddr().(*net.UDPAddr).Port)(ctx, nil)
	<-received
	if r.Status == StatusPass {
		t.Fatalf("malformed response produced PASS: %+v", r)
	}
}

func TestQUICProbeCancellationReturnsPromptly(t *testing.T) {
	udp, received := startUDPFixture(t, nil)
	ops := quicLoopbackOps()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan ProbeResult, 1)
	go func() { result <- ops.quicProbe("quic.test", udp.LocalAddr().(*net.UDPAddr).Port)(ctx, nil) }()
	<-received
	cancel()
	select {
	case r := <-result:
		if r.Status != StatusNA {
			t.Fatalf("canceled result = %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("QUIC probe did not terminate after cancellation")
	}
}

func loopbackQUICLookup(context.Context, string) ([]net.IP, string, error) {
	return []net.IP{net.ParseIP("127.0.0.1")}, "127.0.0.1:53", nil
}

func quicLoopbackOps() *netops {
	ops := *defaultOps
	ops.lookupIP = loopbackQUICLookup
	return &ops
}

func startUDPFixture(t *testing.T, reply []byte) (*net.UDPConn, <-chan struct{}) {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	received := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		_, from, err := udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		close(received)
		if reply != nil {
			_, _ = udp.WriteToUDP(reply, from)
		}
		for {
			if _, _, err := udp.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = udp.Close()
		<-done
	})
	return udp, received
}
