package simulation

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

const (
	// connectHandshakeTimeout bounds everything before the tunnel exists: the
	// request line and headers, the proxy-side lookup, and the upstream dial.
	connectHandshakeTimeout = 5 * time.Second
	// connectTunnelLifetime bounds the tunnel itself, so no fixture connection
	// outlives the run that opened it.
	connectTunnelLifetime = 15 * time.Second
	// connectMaxRequestBytes caps what the fixture reads before it has a
	// request at all. A client that never sends a blank line runs into this
	// rather than into the node's memory.
	connectMaxRequestBytes = 8 << 10
	// connectEstablished is the RFC 9110 success line. A 2xx CONNECT response
	// carries no body and no framing headers: everything after the blank line
	// is tunnel.
	connectEstablished = "HTTP/1.1 200 Connection established\r\n\r\n"
)

// connectServer is the deliberately small HTTP CONNECT proxy the simulator
// needs: one CONNECT per connection to a host:port authority, no
// authentication, no forwarding of ordinary methods, no chaining, no upstream
// proxy. net/http parses the request, so a malformed one is refused rather
// than guessed at, and every refusal names itself with a status code instead
// of a silent close.
type connectServer struct {
	listener net.Listener
	service  string
	port     int
	recorder *evidenceRecorder
	lookup   socksLookup
	dial     socksDial

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
	once   sync.Once
}

func startHTTPConnect(listener net.Listener, service string, port int, resolver string, recorder *evidenceRecorder) *connectServer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &connectServer{
		listener: listener, service: service, port: port, recorder: recorder,
		lookup: nodeResolverLookup(resolver),
		dial:   new(net.Dialer).DialContext,
		ctx:    ctx, cancel: cancel, conns: make(map[net.Conn]struct{}),
	}
	s.wg.Add(1)
	go s.serve()
	return s
}

func (s *connectServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.track(conn, true)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.track(conn, false)
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *connectServer) handle(client net.Conn) {
	accepted := time.Now()
	deadline := accepted.Add(connectHandshakeTimeout)
	_ = client.SetDeadline(deadline)
	// Bounded read: the request is client-controlled, and a client that never
	// finishes its headers must not be able to grow this node's memory.
	reader := bufio.NewReader(io.LimitReader(client, connectMaxRequestBytes))
	request, err := http.ReadRequest(reader)
	if err != nil {
		_ = s.answer(client, http.StatusBadRequest)
		return
	}
	_ = request.Body.Close()
	if request.Method != http.MethodConnect {
		// This fixture tunnels; it is not a forward proxy for ordinary methods,
		// and answering one as though it were would make a broken client look
		// like a working network.
		_ = s.answer(client, http.StatusMethodNotAllowed)
		return
	}
	// ReadRequest puts the authority form of a CONNECT target in Host.
	host, port, ok := connectAuthority(request.Host)
	if !ok {
		_ = s.answer(client, http.StatusBadRequest)
		return
	}
	upstream, status := s.dialDestination(host, port, deadline)
	if upstream == nil {
		_ = s.answer(client, status)
		return
	}
	defer upstream.Close()
	s.track(upstream, true)
	defer s.track(upstream, false)
	if err := s.answer(client, http.StatusOK); err != nil {
		return
	}
	// Anything the client pipelined behind its blank line is already in the
	// bufio reader and would otherwise be dropped on the floor.
	if buffered := reader.Buffered(); buffered > 0 {
		if _, err := io.CopyN(upstream, reader, int64(buffered)); err != nil {
			return
		}
	}
	tunnelDeadline := accepted.Add(connectTunnelLifetime)
	_ = client.SetDeadline(tunnelDeadline)
	_ = upstream.SetDeadline(tunnelDeadline)
	proxyBounded(client, upstream)
}

// dialDestination resolves the CONNECT target through the node's own resolver
// when it is a name, then opens the upstream connection. The returned status is
// what the client is owed when there is no connection: a request this fixture
// will not parse is the client's fault, and a destination it cannot reach is
// the network's.
func (s *connectServer) dialDestination(host string, port int, deadline time.Time) (net.Conn, int) {
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal.Unmap()}
	} else {
		if !isSafeHostname(host) {
			return nil, http.StatusBadRequest
		}
		lookupCtx, cancel := context.WithDeadline(s.ctx, deadline)
		found, err := s.lookup(lookupCtx, host)
		cancel()
		if err != nil || len(found) == 0 {
			return nil, http.StatusBadGateway
		}
		addresses = found
	}
	dialCtx, cancel := context.WithDeadline(s.ctx, deadline)
	defer cancel()
	for _, address := range addresses {
		if !address.IsValid() {
			continue
		}
		conn, err := s.dial(dialCtx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(port)))
		if err == nil {
			return conn, http.StatusOK
		}
	}
	return nil, http.StatusBadGateway
}

// answer writes one status to the client and records it. Evidence is written
// only once the bytes left the fixture, so a status that never reached the wire
// is never claimed as one that did.
func (s *connectServer) answer(conn net.Conn, status int) error {
	var err error
	if status == http.StatusOK {
		_, err = io.WriteString(conn, connectEstablished)
	} else {
		_, err = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
			status, http.StatusText(status))
	}
	if err != nil {
		return err
	}
	_ = s.recorder.record(evidenceEvent{Kind: evidenceServiceReply, Service: s.service,
		ServiceType: ServiceHTTPConnect, ServicePort: s.port, ServiceStatus: status, Result: replyResponded})
	return nil
}

// connectAuthority parses the host:port form CONNECT requires. Anything else,
// including an origin-form path or a bare host, is refused rather than
// completed with a guessed port.
func connectAuthority(authority string) (string, int, bool) {
	host, rawPort, err := net.SplitHostPort(authority)
	if err != nil || host == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

func (s *connectServer) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *connectServer) Close() error {
	var err error
	s.once.Do(func() {
		s.cancel()
		err = s.listener.Close()
		s.mu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
