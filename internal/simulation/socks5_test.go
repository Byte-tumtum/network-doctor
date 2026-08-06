package simulation

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func testSOCKSServer(t *testing.T, lookup socksLookup, dial socksDial) (*socksServer, net.Conn, <-chan struct{}, string) {
	t.Helper()
	path := t.TempDir() + "/evidence.jsonl"
	recorder, err := openEvidenceRecorder(path, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	serverConn, clientConn := net.Pipe()
	s := &socksServer{
		service: "socks-proxy", recorder: recorder, lookup: lookup, dial: dial,
		ctx: context.Background(), cancel: func() {}, conns: make(map[net.Conn]struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handle(serverConn)
		_ = serverConn.Close()
	}()
	return s, clientConn, done, path
}

func socksTestGreeting(t *testing.T, conn net.Conn, methods ...byte) []byte {
	t.Helper()
	request := []byte{socksVersion, byte(len(methods))}
	request = append(request, methods...)
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		t.Fatal(err)
	}
	return reply[:]
}

func readSOCKSTestReply(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	n := 0
	switch header[3] {
	case socksIPv4:
		n = 4
	case socksIPv6:
		n = 16
	case socksDomain:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			t.Fatal(err)
		}
		n = int(length[0])
	default:
		t.Fatalf("reply address type = %d", header[3])
	}
	tail := make([]byte, n+2)
	if _, err := io.ReadFull(conn, tail); err != nil {
		t.Fatal(err)
	}
	return header[:]
}

func successfulDial(t *testing.T, got *string) socksDial {
	t.Helper()
	return func(_ context.Context, _, address string) (net.Conn, error) {
		*got = address
		server, peer := net.Pipe()
		go func() {
			_, _ = io.Copy(io.Discard, peer)
			_ = peer.Close()
		}()
		return server, nil
	}
}

func TestSOCKS5GreetingAndNoAuthNegotiation(t *testing.T) {
	_, client, done, _ := testSOCKSServer(t, nil, nil)
	if got := socksTestGreeting(t, client, 2, socksNoAuth); string(got) != string([]byte{socksVersion, socksNoAuth}) {
		t.Errorf("greeting = %v", got)
	}
	_ = client.Close()
	<-done

	_, client, done, _ = testSOCKSServer(t, nil, nil)
	if got := socksTestGreeting(t, client, 2); string(got) != string([]byte{socksVersion, socksNoMatch}) {
		t.Errorf("unsupported auth greeting = %v", got)
	}
	_ = client.Close()
	<-done
}

func TestSOCKS5IPv4AndDomainRequests(t *testing.T) {
	tests := []struct {
		name       string
		request    []byte
		lookup     socksLookup
		wantDial   string
		wantType   string
		wantTarget string
	}{
		{
			name: "IPv4", request: []byte{socksVersion, socksConnect, 0, socksIPv4, 10, 77, 0, 40, 0, 80},
			wantDial: "10.77.0.40:80", wantType: "ipv4", wantTarget: "10.77.0.40",
		},
		{
			name: "IPv6",
			request: append(append([]byte{socksVersion, socksConnect, 0, socksIPv6},
				netip.MustParseAddr("2001:db8::1").AsSlice()...), 1, 187),
			wantDial: "[2001:db8::1]:443", wantType: "ipv6", wantTarget: "2001:db8::1",
		},
		{
			name:    "domain",
			request: append(append([]byte{socksVersion, socksConnect, 0, socksDomain, 19}, []byte("private-target.test")...), 1, 187),
			lookup: func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("10.77.0.40")}, nil
			},
			wantDial: "10.77.0.40:443", wantType: "domain", wantTarget: "private-target.test",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var dialed string
			_, client, done, path := testSOCKSServer(t, tc.lookup, successfulDial(t, &dialed))
			if got := socksTestGreeting(t, client, socksNoAuth); got[1] != socksNoAuth {
				t.Fatalf("greeting = %v", got)
			}
			if _, err := client.Write(tc.request); err != nil {
				t.Fatal(err)
			}
			if reply := readSOCKSTestReply(t, client); reply[1] != socksReplyOK {
				t.Fatalf("reply = %v", reply)
			}
			_ = client.Close()
			<-done
			if dialed != tc.wantDial {
				t.Errorf("dialed = %q, want %q", dialed, tc.wantDial)
			}
			evidence, err := readEvidence([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence.SOCKSRequests) != 2 {
				t.Fatalf("evidence = %+v", evidence.SOCKSRequests)
			}
			var connected SOCKSEvidence
			for _, item := range evidence.SOCKSRequests {
				if item.Event == "connect" {
					connected = item
				}
			}
			if connected.AddressType != tc.wantType || connected.Destination != tc.wantTarget || connected.Result != "connected" {
				t.Errorf("request evidence = %+v", connected)
			}
		})
	}
}

func TestSOCKS5RequestFailures(t *testing.T) {
	tests := []struct {
		name      string
		request   []byte
		lookup    socksLookup
		dial      socksDial
		wantReply byte
		wantLog   string
	}{
		{"unsupported command", []byte{socksVersion, 2, 0, socksIPv4, 10, 0, 0, 1, 0, 80}, nil, nil, socksReplyCommand, "command_not_supported"},
		{"unsupported address", []byte{socksVersion, socksConnect, 0, 9}, nil, nil, socksReplyAddress, ""},
		{"failed proxy DNS", append(append([]byte{socksVersion, socksConnect, 0, socksDomain, 12}, []byte("missing.test")...), 0, 80),
			func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("NXDOMAIN") }, nil, socksReplyHost, "dns_failure"},
		{"destination refused", []byte{socksVersion, socksConnect, 0, socksIPv4, 10, 0, 0, 1, 0, 80}, nil,
			func(context.Context, string, string) (net.Conn, error) { return nil, syscall.ECONNREFUSED }, socksReplyRefused, "connection_refused"},
		{"overlong domain", []byte{socksVersion, socksConnect, 0, socksDomain, 254}, nil, nil, socksReplyAddress, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client, done, path := testSOCKSServer(t, tc.lookup, tc.dial)
			socksTestGreeting(t, client, socksNoAuth)
			if _, err := client.Write(tc.request); err != nil {
				t.Fatal(err)
			}
			if reply := readSOCKSTestReply(t, client); reply[1] != tc.wantReply {
				t.Errorf("reply = %v, want code %d", reply, tc.wantReply)
			}
			_ = client.Close()
			<-done
			if tc.wantLog != "" {
				evidence, err := readEvidence([]string{path})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, item := range evidence.SOCKSRequests {
					found = found || item.Result == tc.wantLog
				}
				if !found {
					t.Errorf("evidence = %+v, want result %q", evidence.SOCKSRequests, tc.wantLog)
				}
			}
		})
	}
}

func TestSOCKS5MalformedAndTruncatedInputReturns(t *testing.T) {
	tests := []struct {
		name     string
		greeting bool
		input    []byte
	}{
		{"wrong version", false, []byte{4, 1}},
		{"truncated greeting", false, []byte{socksVersion, 1}},
		{"truncated request", true, []byte{socksVersion, socksConnect}},
		{"truncated address", true, []byte{socksVersion, socksConnect, 0, socksIPv4, 10, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client, done, _ := testSOCKSServer(t, nil, nil)
			if tc.greeting {
				socksTestGreeting(t, client, socksNoAuth)
			}
			if _, err := client.Write(tc.input); err != nil {
				t.Fatal(err)
			}
			_ = client.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("handler did not return for %v", tc.input)
			}
		})
	}
}

type pipeListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return dummyAddr("pipe") }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

func TestSOCKS5CancellationAndShutdownClosesPartialClients(t *testing.T) {
	listener := newPipeListener()
	recorder, err := openEvidenceRecorder(t.TempDir()+"/evidence.jsonl", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recorder.Close() }()
	server := startSOCKS5(listener, "socks-proxy", "127.0.0.1", recorder)
	serverSide, client := net.Pipe()
	listener.ch <- serverSide
	if _, err := client.Write([]byte{socksVersion}); err != nil {
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

func TestSOCKS5TunnelCopyIsBounded(t *testing.T) {
	var dialed string
	_, client, done, _ := testSOCKSServer(t, nil, successfulDial(t, &dialed))
	socksTestGreeting(t, client, socksNoAuth)
	if _, err := client.Write([]byte{socksVersion, socksConnect, 0, socksIPv4, 10, 77, 0, 40, 0, 80}); err != nil {
		t.Fatal(err)
	}
	if reply := readSOCKSTestReply(t, client); reply[1] != socksReplyOK {
		t.Fatalf("reply = %v", reply)
	}
	wrote := make(chan struct{})
	go func() {
		_, _ = client.Write(make([]byte, socksMaxBytesEachWay+2))
		close(wrote)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel did not close after the byte limit")
	}
	_ = client.Close()
	<-wrote
}

func TestDNSAndSOCKSEvidenceAggregation(t *testing.T) {
	events := []evidenceEvent{
		{Kind: ServiceDNS, Node: "proxy", Service: "private-dns", Name: "private.test", QueryType: "A", Result: "ANSWER"},
		{Kind: ServiceDNS, Node: "proxy", Service: "private-dns", Name: "private.test", QueryType: "A", Result: "ANSWER"},
		{Kind: ServiceSOCKS5, Node: "proxy", Service: "socks-proxy", Event: "connect", AddressType: "domain", Destination: "private.test", Port: 443, Result: "connected"},
	}
	got := aggregateEvidence(events)
	if len(got.DNS) != 1 || got.DNS[0].Count != 2 {
		t.Errorf("DNS evidence = %+v", got.DNS)
	}
	if len(got.SOCKSRequests) != 1 || got.SOCKSRequests[0].AddressType != "domain" {
		t.Errorf("SOCKS evidence = %+v", got.SOCKSRequests)
	}
}

func TestEvidenceRecorderWritesWholeConcurrentEvents(t *testing.T) {
	path := t.TempDir() + "/events.jsonl"
	recorder, err := openEvidenceRecorder(path, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder.record(evidenceEvent{Kind: ServiceDNS, Name: "private.test", QueryType: "A", Result: "ANSWER"})
		}()
	}
	wg.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(blob), "\n"); lines != 20 {
		t.Errorf("event lines = %d, want 20", lines)
	}
	got, err := readEvidence([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DNS) != 1 || got.DNS[0].Count != 20 {
		t.Errorf("evidence = %+v", got.DNS)
	}
}

func TestReadSOCKSRequestPortUsesNetworkByteOrder(t *testing.T) {
	server, client := net.Pipe()
	done := make(chan socksRequest, 1)
	go func() {
		request, _, _ := readSOCKSRequest(server)
		done <- request
	}()
	request := []byte{socksVersion, socksConnect, 0, socksIPv4, 192, 0, 2, 1, 0, 0}
	binary.BigEndian.PutUint16(request[len(request)-2:], 8443)
	_, _ = client.Write(request)
	_ = client.Close()
	if got := (<-done).port; got != 8443 {
		t.Errorf("port = %d", got)
	}
	_ = server.Close()
}
