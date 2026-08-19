package simulation

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	socksVersion = 5
	socksNoAuth  = 0
	socksNoMatch = 0xff
	socksConnect = 1

	socksIPv4   = 1
	socksDomain = 3
	socksIPv6   = 4

	socksReplyOK            = 0
	socksReplyFailure       = 1
	socksReplyNetwork       = 3
	socksReplyHost          = 4
	socksReplyRefused       = 5
	socksReplyCommand       = 7
	socksReplyAddress       = 8
	socksHandshakeTimeout   = 5 * time.Second
	socksConnectionLifetime = 15 * time.Second
	socksMaxBytesEachWay    = 1 << 20
)

type socksLookup func(context.Context, string) ([]netip.Addr, error)
type socksDial func(context.Context, string, string) (net.Conn, error)

// socksServer is the deliberately small SOCKS5 subset the simulator needs:
// no-auth CONNECT with IPv4, IPv6, or domain destinations. BIND, UDP
// ASSOCIATE, authentication, and chained proxies are not implemented.
type socksServer struct {
	listener net.Listener
	service  string
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

// nodeResolverLookup resolves through the node's own configured resolver
// rather than through the holder process's resolver. That is what makes a
// proxy-side lookup observably the proxy's, which is the whole distinction
// socks5h and an HTTP CONNECT proxy both rest on.
func nodeResolverLookup(resolver string) socksLookup {
	address := net.JoinHostPort(resolver, "53")
	dialer := new(net.Dialer)
	proxyResolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}}
	return func(ctx context.Context, name string) ([]netip.Addr, error) {
		return proxyResolver.LookupNetIP(ctx, "ip", name)
	}
}

func startSOCKS5(listener net.Listener, service, resolver string, recorder *evidenceRecorder) *socksServer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &socksServer{
		listener: listener, service: service, recorder: recorder,
		lookup: nodeResolverLookup(resolver),
		dial:   new(net.Dialer).DialContext,
		ctx:    ctx, cancel: cancel, conns: make(map[net.Conn]struct{}),
	}
	s.wg.Add(1)
	go s.serve()
	return s
}

func (s *socksServer) serve() {
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

func (s *socksServer) handle(client net.Conn) {
	accepted := time.Now()
	_ = client.SetDeadline(accepted.Add(socksHandshakeTimeout))
	if err := s.greet(client); err != nil {
		return
	}
	if err := s.recorder.record(evidenceEvent{Kind: ServiceSOCKS5, Service: s.service, Event: "greeting", Result: "accepted"}); err != nil {
		return
	}

	request, reply, err := readSOCKSRequest(client)
	if err != nil {
		if reply != 0 {
			// #nosec G115 -- reply is one of the byte-sized SOCKS reply constants.
			_ = writeSOCKSReply(client, byte(reply), netip.AddrPort{})
		}
		return
	}
	if request.command != socksConnect {
		if err := s.recordRequest(request, "command_not_supported"); err != nil {
			return
		}
		_ = writeSOCKSReply(client, socksReplyCommand, netip.AddrPort{})
		return
	}

	addresses := []netip.Addr{request.address}
	if request.addressType == "domain" {
		lookupCtx, cancel := context.WithDeadline(s.ctx, accepted.Add(socksHandshakeTimeout))
		addresses, err = s.lookup(lookupCtx, request.destination)
		cancel()
		if err != nil || len(addresses) == 0 {
			if err := s.recordRequest(request, "dns_failure"); err != nil {
				return
			}
			_ = writeSOCKSReply(client, socksReplyHost, netip.AddrPort{})
			return
		}
	}

	dialCtx, cancel := context.WithDeadline(s.ctx, accepted.Add(socksHandshakeTimeout))
	var upstream net.Conn
	var dialErr error
	for _, address := range addresses {
		if !address.IsValid() {
			continue
		}
		upstream, dialErr = s.dial(dialCtx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(request.port)))
		if dialErr == nil {
			break
		}
	}
	cancel()
	if upstream == nil {
		if err := s.recordRequest(request, dialResult(dialErr)); err != nil {
			return
		}
		_ = writeSOCKSReply(client, socksReplyForError(dialErr), netip.AddrPort{})
		return
	}
	defer upstream.Close()
	s.track(upstream, true)
	defer s.track(upstream, false)

	bound := addrPort(upstream.LocalAddr())
	if err := writeSOCKSReply(client, socksReplyOK, bound); err != nil {
		if err := s.recordRequest(request, "reply_failure"); err != nil {
			return
		}
		return
	}
	if err := s.recordRequest(request, "connected"); err != nil {
		return
	}
	deadline := accepted.Add(socksConnectionLifetime)
	_ = client.SetDeadline(deadline)
	_ = upstream.SetDeadline(deadline)
	proxyBounded(client, upstream)
}

func (s *socksServer) greet(conn net.Conn) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != socksVersion || header[1] == 0 {
		return errors.New("invalid greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	chosen := byte(socksNoMatch)
	for _, method := range methods {
		if method == socksNoAuth {
			chosen = socksNoAuth
			break
		}
	}
	if _, err := conn.Write([]byte{socksVersion, chosen}); err != nil {
		return err
	}
	if chosen == socksNoMatch {
		return errors.New("no supported authentication method")
	}
	return nil
}

type socksRequest struct {
	command     byte
	addressType string
	destination string
	address     netip.Addr
	port        int
}

func readSOCKSRequest(conn net.Conn) (socksRequest, int, error) {
	var request socksRequest
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return request, 0, err
	}
	if header[0] != socksVersion || header[2] != 0 {
		return request, socksReplyFailure, errors.New("invalid request header")
	}
	request.command = header[1]
	var raw []byte
	switch header[3] {
	case socksIPv4:
		request.addressType, raw = "ipv4", make([]byte, 4)
	case socksIPv6:
		request.addressType, raw = "ipv6", make([]byte, 16)
	case socksDomain:
		request.addressType = "domain"
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return request, 0, err
		}
		if length[0] == 0 || int(length[0]) > 253 {
			return request, socksReplyAddress, errors.New("invalid domain length")
		}
		raw = make([]byte, int(length[0]))
	default:
		return request, socksReplyAddress, fmt.Errorf("unsupported address type %d", header[3])
	}
	if _, err := io.ReadFull(conn, raw); err != nil {
		return request, 0, err
	}
	if request.addressType == "domain" {
		request.destination = string(raw)
		if !isSafeHostname(request.destination) {
			return request, socksReplyAddress, errors.New("invalid domain name")
		}
	} else {
		request.address, _ = netip.AddrFromSlice(raw)
		request.address = request.address.Unmap()
		request.destination = request.address.String()
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return request, 0, err
	}
	request.port = int(binary.BigEndian.Uint16(port[:]))
	if request.port == 0 {
		return request, socksReplyFailure, errors.New("invalid destination port")
	}
	return request, 0, nil
}

func (s *socksServer) recordRequest(request socksRequest, result string) error {
	return s.recorder.record(evidenceEvent{Kind: ServiceSOCKS5, Service: s.service, Event: "connect",
		AddressType: request.addressType, Destination: request.destination, Port: request.port, Result: result})
}

func writeSOCKSReply(conn net.Conn, code byte, bound netip.AddrPort) error {
	reply := []byte{socksVersion, code, 0}
	address := bound.Addr()
	switch {
	case address.Is4():
		reply = append(reply, socksIPv4)
		reply = append(reply, address.AsSlice()...)
	case address.Is6():
		reply = append(reply, socksIPv6)
		reply = append(reply, address.AsSlice()...)
	default:
		reply = append(reply, socksIPv4, 0, 0, 0, 0)
	}
	reply = binary.BigEndian.AppendUint16(reply, bound.Port())
	_, err := conn.Write(reply)
	return err
}

func socksReplyForError(err error) byte {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return socksReplyRefused
	case errors.Is(err, syscall.ENETUNREACH):
		return socksReplyNetwork
	case errors.Is(err, syscall.EHOSTUNREACH):
		return socksReplyHost
	default:
		return socksReplyFailure
	}
}

func dialResult(err error) string {
	switch socksReplyForError(err) {
	case socksReplyRefused:
		return "connection_refused"
	case socksReplyNetwork:
		return "network_unreachable"
	case socksReplyHost:
		return "host_unreachable"
	default:
		return "connection_failure"
	}
}

func addrPort(addr net.Addr) netip.AddrPort {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		if ip, valid := netip.AddrFromSlice(tcp.IP); valid {
			// #nosec G115 -- net.TCPAddr ports supplied by the network stack fit uint16.
			return netip.AddrPortFrom(ip.Unmap(), uint16(tcp.Port))
		}
	}
	return netip.AddrPort{}
}

func proxyBounded(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, io.LimitReader(src, socksMaxBytesEachWay+1))
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

func (s *socksServer) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *socksServer) Close() error {
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
