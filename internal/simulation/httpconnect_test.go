package simulation

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testCONNECTServer(t *testing.T, lookup socksLookup, dial socksDial) (net.Conn, <-chan struct{}, string) {
	t.Helper()
	path := t.TempDir() + "/evidence.jsonl"
	recorder, err := openEvidenceRecorder(path, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	serverConn, clientConn := net.Pipe()
	s := &connectServer{
		service: "connect-proxy", port: defaultCONNECTProxyPort, recorder: recorder, lookup: lookup, dial: dial,
		ctx: context.Background(), cancel: func() {}, conns: make(map[net.Conn]struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handle(serverConn)
		_ = serverConn.Close()
	}()
	return clientConn, done, path
}

// readCONNECTReply reads the fixture's answer the way the production probe
// does, so a reply netdoc could not parse fails here rather than in a namespace.
func readCONNECTReply(t *testing.T, conn net.Conn) (int, string) {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(io.LimitReader(conn, 4096)), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("reading CONNECT reply: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Status
}

func replyStatuses(t *testing.T, path string) []int {
	t.Helper()
	evidence, err := readEvidence([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, reply := range evidence.ServiceReplies {
		if reply.Type != ServiceHTTPConnect || reply.Result != replyResponded || reply.Count == 0 {
			continue
		}
		out = append(out, reply.Status)
	}
	return out
}

func TestCONNECTTunnelsToAResolvedDestination(t *testing.T) {
	var dialed string
	upstream, peer := net.Pipe()
	client, done, path := testCONNECTServer(t,
		func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.77.0.1")}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return upstream, nil
		})

	if _, err := io.WriteString(client, "CONNECT connectivitycheck.gstatic.com:443 HTTP/1.1\r\nHost: connectivitycheck.gstatic.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, status := readCONNECTReply(t, client); code != http.StatusOK {
		t.Fatalf("status = %s", status)
	}
	if dialed != "10.77.0.1:443" {
		t.Errorf("dialed %q, want the resolved destination", dialed)
	}

	// The tunnel has to carry bytes, not just announce itself.
	relayed := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		_, _ = io.ReadFull(peer, buf)
		relayed <- string(buf)
	}()
	if _, err := io.WriteString(client, "hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-relayed:
		if got != "hello" {
			t.Errorf("upstream received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the upstream side of the tunnel")
	}
	_ = client.Close()
	_ = peer.Close()
	<-done

	if got := replyStatuses(t, path); len(got) != 1 || got[0] != http.StatusOK {
		t.Errorf("recorded replies = %v, want one 200", got)
	}
}

func TestCONNECTRefusesWhatItDoesNotImplement(t *testing.T) {
	refusedDial := func(context.Context, string, string) (net.Conn, error) { return nil, syscall.ECONNREFUSED }
	tests := []struct {
		name    string
		request string
		lookup  socksLookup
		dial    socksDial
		want    int
	}{
		{"ordinary forward proxy request", "GET http://example.test/ HTTP/1.1\r\nHost: example.test\r\n\r\n", nil, nil, http.StatusMethodNotAllowed},
		{"garbage request line", "not a request line\r\n\r\n", nil, nil, http.StatusBadRequest},
		{"authority without a port", "CONNECT example.test HTTP/1.1\r\n\r\n", nil, nil, http.StatusBadRequest},
		{"authority with a bad port", "CONNECT example.test:0 HTTP/1.1\r\n\r\n", nil, nil, http.StatusBadRequest},
		{"authority that is not a hostname", "CONNECT bad_name.test:443 HTTP/1.1\r\n\r\n", nil, nil, http.StatusBadRequest},
		{"destination the proxy cannot resolve", "CONNECT missing.test:443 HTTP/1.1\r\n\r\n",
			func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("NXDOMAIN") }, nil, http.StatusBadGateway},
		{"destination the proxy cannot reach", "CONNECT 10.77.0.1:443 HTTP/1.1\r\n\r\n", nil, refusedDial, http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, done, path := testCONNECTServer(t, tc.lookup, tc.dial)
			if _, err := io.WriteString(client, tc.request); err != nil {
				t.Fatal(err)
			}
			code, status := readCONNECTReply(t, client)
			if code != tc.want {
				t.Errorf("status = %s, want %d", status, tc.want)
			}
			// A refusal must not leave the client believing it holds a tunnel.
			if code/100 == 2 {
				t.Errorf("%s answered success", tc.name)
			}
			_ = client.Close()
			<-done
			if got := replyStatuses(t, path); len(got) != 1 || got[0] != tc.want {
				t.Errorf("recorded replies = %v, want one %d", got, tc.want)
			}
		})
	}
}

func TestCONNECTShutdownClosesPartialClients(t *testing.T) {
	listener := newPipeListener()
	recorder, err := openEvidenceRecorder(t.TempDir()+"/evidence.jsonl", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recorder.Close() }()
	server := startHTTPConnect(listener, "connect-proxy", defaultCONNECTProxyPort, "127.0.0.1", recorder)
	serverSide, client := net.Pipe()
	listener.ch <- serverSide
	if _, err := io.WriteString(client, "CONNECT example.test:443"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked with a truncated client")
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Error("client survived server shutdown")
	}
	if err := server.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

func TestCONNECTAuthorityParsing(t *testing.T) {
	for _, ok := range []struct {
		authority string
		host      string
		port      int
	}{
		{"example.test:443", "example.test", 443},
		{"10.77.0.1:3128", "10.77.0.1", 3128},
		{"[2001:db8::1]:443", "2001:db8::1", 443},
	} {
		host, port, valid := connectAuthority(ok.authority)
		if !valid || host != ok.host || port != ok.port {
			t.Errorf("connectAuthority(%q) = %q, %d, %t", ok.authority, host, port, valid)
		}
	}
	for _, bad := range []string{"", "example.test", "example.test:", ":443", "example.test:0", "example.test:65536", "example.test:https", "/path"} {
		if _, _, valid := connectAuthority(bad); valid {
			t.Errorf("connectAuthority(%q) = true", bad)
		}
	}
}

// The fixture must not become a general-purpose proxy by accident: a request
// body that keeps arriving is capped rather than read forever.
func TestCONNECTRequestReadIsBounded(t *testing.T) {
	client, done, _ := testCONNECTServer(t, nil, nil)
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n")
		for {
			if _, err := io.WriteString(client, "X-Filler: "+strings.Repeat("a", 512)+"\r\n"); err != nil {
				return
			}
		}
	}()
	// The cap is reached long before the header block ends, and what comes back
	// is a refusal rather than a tunnel or an unbounded read.
	if code, status := readCONNECTReply(t, client); code != http.StatusBadRequest {
		t.Errorf("status = %s, want 400", status)
	}
	_ = client.Close()
	<-done
	<-stopped
}
