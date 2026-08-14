//go:build integration

package simulation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func TestTCPResetServiceAcceptsThenResets(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := startTCPResetServer([]net.Listener{ln}, "reset", nil)
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp4", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var one [1]byte
		_, err = conn.Read(one[:])
		_ = conn.Close()
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("read error = %v, want connection reset", err)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp4", ln.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("closed listener still accepted a connection")
	}
}

// The encrypted-DNS fixture has to answer both transports for real, or every
// scenario's encrypted-DNS row is measuring the fixture rather than netdoc.
// Loopback listeners on ephemeral ports stand in for the privileged ones the
// protocols fix, which a rootless test cannot bind.
func TestEncryptedDNSServiceAnswersDoHAndDoT(t *testing.T) {
	work := t.TempDir()
	svc := Service{
		Name:        encryptedDNSProbeService,
		Type:        ServiceEncryptedDNS,
		Port:        443,
		Zone:        map[string]string{"probe.test": "192.0.2.7"},
		Certificate: &TLSCertificate{Mode: TLSCertificateValid, DNSNames: []string{diagnostic.EncryptedDNSHost}},
	}
	var listeners []net.Listener
	server, err := startEncryptedDNSServiceWith(context.Background(), svc, nil, work,
		func([]string, string) ([]net.Listener, error) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			listeners = append(listeners, ln)
			return []net.Listener{ln}, nil
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if len(listeners) != 2 {
		t.Fatalf("listeners = %d, want one for DoH and one for DoT", len(listeners))
	}
	roots := x509.NewCertPool()
	ca, err := os.ReadFile(probeTrustAnchorPath(work, svc.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA is not usable as a trust anchor")
	}
	// The query netdoc's probe sends: one A question, recursion desired.
	query := []byte{0xab, 0xcd, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0,
		5, 'p', 'r', 'o', 'b', 'e', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, listeners[0].Addr().String())
		},
		TLSClientConfig: &tls.Config{ServerName: diagnostic.EncryptedDNSHost, RootCAs: roots},
	}}
	resp, err := client.Post("https://"+diagnostic.EncryptedDNSHost+"/dns-query", encryptedDNSMediaType, bytes.NewReader(query))
	if err != nil {
		t.Fatalf("DoH request: %v", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !answersQuery(body, query) {
		t.Fatalf("DoH answered %d with % x", resp.StatusCode, body)
	}

	conn, err := tls.Dial("tcp4", listeners[1].Addr().String(), &tls.Config{ServerName: diagnostic.EncryptedDNSHost, RootCAs: roots})
	if err != nil {
		t.Fatalf("DoT handshake: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(append([]byte{0, byte(len(query))}, query...)); err != nil {
		t.Fatalf("DoT write: %v", err)
	}
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatalf("DoT framing: %v", err)
	}
	answer := make([]byte, int(header[0])<<8|int(header[1]))
	if _, err := io.ReadFull(conn, answer); err != nil {
		t.Fatalf("DoT body: %v", err)
	}
	if !answersQuery(answer, query) {
		t.Fatalf("DoT answered % x", answer)
	}

	// Leave one DoH connection between its TLS handshake and HTTP request, and
	// the DoT connection between framed queries. Shutdown must close both and
	// join every serve goroutine rather than waiting for client deadlines.
	dohActive, err := tls.Dial("tcp4", listeners[0].Addr().String(), &tls.Config{ServerName: diagnostic.EncryptedDNSHost, RootCAs: roots})
	if err != nil {
		t.Fatalf("active DoH handshake: %v", err)
	}
	defer dohActive.Close()
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("fixture shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fixture shutdown blocked on active encrypted-DNS connections")
	}
	for name, active := range map[string]net.Conn{"DoH": dohActive, "DoT": conn} {
		_ = active.SetReadDeadline(time.Now().Add(time.Second))
		var one [1]byte
		if _, err := active.Read(one[:]); err == nil {
			t.Errorf("active %s connection survived fixture shutdown", name)
		}
	}
}

// answersQuery is the correlation netdoc's probe performs: same transaction id,
// response bit set, and the question echoed back.
func answersQuery(response, query []byte) bool {
	if len(response) < len(query) {
		return false
	}
	return bytes.Equal(response[:2], query[:2]) &&
		response[2]&0x80 != 0 &&
		bytes.Equal(response[dnsHeaderLen:len(query)], query[dnsHeaderLen:])
}

// TestHolderProbeReplyObservesRealSockets covers the holder end of the
// simulator's independent reachability observation over loopback: a listening
// port answers reachable, a closed one answers refused, and neither answer
// comes from anything netdoc reported.
//
// A closed port on a live host is refused, not unreachable, and the difference
// is the whole point of the third answer: a kernel that sends a reset is a
// different fault from a filter that sends nothing. The unreachable half needs
// a packet to be discarded on a path, which loopback has none of; the namespace
// integration tests cover it against a real filter.
func TestHolderProbeReplyObservesRealSockets(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			conn.Close()
		}
	}()
	open := ln.Addr().(*net.TCPAddr).Port

	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	shut := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	for _, tc := range []struct {
		name string
		port int
		want string
	}{
		{"listening port", open, "probe-result reachable"},
		{"closed port", shut, "probe-result refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := fmt.Sprintf("probe 127.0.0.1 %d 2000", tc.port)
			if got := holderCommandReply(line, nil); got != tc.want {
				t.Errorf("%q = %q, want %q", line, got, tc.want)
			}
		})
	}
}
