//go:build integration

package simulation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func startDNSFaultServer(t *testing.T, fault *DNSFault) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	zone := testZone(t)
	delays := newDelayGroup(context.Background())
	delays.wg.Add(1)
	go func() {
		defer delays.wg.Done()
		serveDNS(pc, zone, "test-resolver", newDNSState(fault), delays, nil)
	}()
	t.Cleanup(func() {
		_ = pc.Close()
		_ = delays.Close()
	})
	return pc.LocalAddr().String()
}

func exchangeDNS(t *testing.T, server string, query []byte) []byte {
	t.Helper()
	conn, err := net.DialTimeout("udp4", server, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, dnsMaxMsg)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

func TestDNSFaultREFUSEDWireResponse(t *testing.T) {
	query := dnsQuery("example.test", dnsTypeA)
	reply := exchangeDNS(t, startDNSFaultServer(t, &DNSFault{A: []string{DNSOutcomeREFUSED}}), query)
	if !isReply(reply) || msgID(reply) != msgID(query) || questions(reply) != 1 || answers(reply) != 0 || rcode(reply) != dnsRcodeRefused {
		t.Fatalf("REFUSED reply = %v", reply)
	}
}

func TestDNSFaultTruncatedWireResponseHasNoTCPFallback(t *testing.T) {
	server := startDNSFaultServer(t, &DNSFault{A: []string{DNSOutcomeTruncated}})
	query := dnsQuery("example.test", dnsTypeA)
	reply := exchangeDNS(t, server, query)
	flags := binary.BigEndian.Uint16(reply[2:4])
	if !isReply(reply) || msgID(reply) != msgID(query) || questions(reply) != 1 || answers(reply) != 0 ||
		rcode(reply) != dnsRcodeSuccess || flags&dnsFlagTC == 0 {
		t.Fatalf("truncated reply = %v", reply)
	}
	// Plain DNS fixtures intentionally have no TCP listener. The Go resolver's
	// fallback therefore cannot turn this TC response into a normal answer.
	if conn, err := net.DialTimeout("tcp4", server, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("truncated DNS fault unexpectedly had a TCP fallback listener")
	}
}

func TestDNSFaultWrongAddressWireResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		qtype uint16
		fault *DNSFault
		want  netip.Addr
	}{
		{"A", dnsTypeA, &DNSFault{A: []string{DNSOutcomeWrongAnswer}, WrongA: "192.0.2.7"}, netip.MustParseAddr("192.0.2.7")},
		{"AAAA", dnsTypeAAAA, &DNSFault{AAAA: []string{DNSOutcomeWrongAnswer}, WrongAAAA: "2001:db8::7"}, netip.MustParseAddr("2001:db8::7")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := dnsQuery("example.test", tc.qtype)
			reply := exchangeDNS(t, startDNSFaultServer(t, tc.fault), query)
			if rcode(reply) != dnsRcodeSuccess || answers(reply) != 1 {
				t.Fatalf("wrong-address reply = %v", reply)
			}
			got, ok := netip.AddrFromSlice(reply[len(reply)-tc.want.BitLen()/8:])
			if !ok || got != tc.want || got.Is4() != tc.want.Is4() {
				t.Fatalf("wrong-address RDATA = %v, want %s", got, tc.want)
			}
		})
	}
}

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

func TestTCPServiceKeepsAccepting(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go serveSink(listener)
	t.Cleanup(func() { _ = listener.Close() })
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
		conn.Close()
	}
}

func TestBannerServiceWritesAndStaysOpenUntilShutdown(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := startBannerServer(ctx, []net.Listener{listener}, "SSH-2.0-test\r\n")
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})

	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "SSH-2.0-test\r\n" {
		t.Fatalf("banner = %q, error = %v", line, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := conn.Read(one[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read after banner = %v, want an open connection", err)
	}
	if _, err := conn.Write([]byte("client interaction\r\n")); err != nil {
		t.Fatalf("write after banner: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := conn.Read(one[:]); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read after cancellation = %v, want the connection closed", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("banner service did not shut down")
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

// The captive-portal fixture, over a real socket. A portal answers the
// connectivity check with a redirect to its sign-in page instead of the 204 the
// probe wants, and touches nothing else it serves: a mode that also rewrote the
// ordinary paths would break every unrelated row in the same scenario, and a
// scenario without the mode has to keep answering 204 or every other fixture
// turns into a portal by accident.
func TestHTTPServicePortalModeInterceptsOnlyTheConnectivityCheck(t *testing.T) {
	serve := func(t *testing.T, svc Service) string {
		t.Helper()
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go serveHTTP(ln, svc, nil)
		return "http://" + ln.Addr().String()
	}
	// The probe reports the 3xx rather than chasing it, so the fixture has to be
	// read the same way: a client that followed the redirect would report the
	// sign-in page's status and hide the interception entirely.
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	get := func(t *testing.T, url string) *http.Response {
		t.Helper()
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	portal := serve(t, Service{Type: ServiceHTTP, Port: 80, Status: 200, Portal: true})
	resp := get(t, portal+"/generate_204")
	if resp.StatusCode != http.StatusFound {
		t.Errorf("portal /generate_204 = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if got := resp.Header.Get("Location"); got != portalSignInURL {
		t.Errorf("portal Location = %q, want %q", got, portalSignInURL)
	}
	if resp := get(t, portal+"/"); resp.StatusCode != 200 {
		t.Errorf("portal / = %d, want the configured 200: portal mode is scoped to the connectivity check", resp.StatusCode)
	}

	plain := serve(t, Service{Type: ServiceHTTP, Port: 80, Status: 200})
	resp = get(t, plain+"/generate_204")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("plain /generate_204 = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got := resp.Header.Get("Location"); got != "" {
		t.Errorf("plain Location = %q, want none", got)
	}
}

func TestHTTPServiceDateOffset(t *testing.T) {
	client := http.Client{Timeout: 5 * time.Second}
	for _, tc := range []struct {
		name   string
		raw    string
		offset time.Duration
	}{
		{name: "omitted"},
		{name: "future", raw: "2h", offset: 2 * time.Hour},
		{name: "past", raw: "-2h", offset: -2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
			go serveHTTP(ln, Service{Type: ServiceHTTP, Port: 80, Status: 200, DateOffset: tc.raw}, nil)

			before := time.Now()
			resp, err := client.Get("http://" + ln.Addr().String() + "/generate_204")
			after := time.Now()
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}
			date, err := http.ParseTime(resp.Header.Get("Date"))
			if err != nil {
				t.Fatalf("Date = %q: %v", resp.Header.Get("Date"), err)
			}
			if date.Before(before.Add(tc.offset-2*time.Second)) || date.After(after.Add(tc.offset+2*time.Second)) {
				t.Errorf("Date offset = %v, want about %v", date.Sub(before), tc.offset)
			}
		})
	}
}
