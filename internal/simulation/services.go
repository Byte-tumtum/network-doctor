package simulation

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Holder protocol. The director and the node holder exchange three lines over
// the holder's stdin/stdout: the holder announces its namespace is ready, the
// director answers once the namespace is addressed and routed, and the holder
// confirms its listeners are up. Nothing is reachable before that last line, so
// a probe can never race the topology.
// After services-ready the pipe stays open for one more exchange: the fault
// scheduler sends "dns <service> <outcome> <delay-ms>" and the holder answers
// dns-applied or dns-error. A scheduled DNS transition is therefore timed by
// the director's single epoch and confirmed before it is recorded as applied.
const (
	// NodeCommand is the hidden argv[1] that makes the binary a node holder.
	NodeCommand         = "__node"
	holderNSReady       = "ns-ready"
	holderStart         = "start"
	holderServicesReady = "services-ready"
	holderDNSCommand    = "dns"
	holderDNSApplied    = "dns-applied"
	holderDNSError      = "dns-error"
	holderEvidenceCheck = "evidence-check"
	holderEvidenceReady = "evidence-ready"
	holderProbeCommand  = "probe"
	holderProbeResult   = "probe-result"
	holderProbeReached  = "reachable"
	holderProbeFailed   = "unreachable"
	// holderProbeRefused is the answer to a SYN that got a reset back rather than
	// silence. An older director that does not know the word treats it as an
	// unparseable reply and fails loudly, which is the safe direction: a refusal
	// silently read as a timeout would make two different faults one observation.
	holderProbeRefused = "refused"
	holderProbeError   = "error"
)

// nodeConfig is what the director hands a holder.
type nodeConfig struct {
	Name             string `json:"name"`
	Resolver         string `json:"resolver,omitempty"`
	Evidence         string `json:"evidence,omitempty"`
	TrustDir         string `json:"trust_dir,omitempty"`
	ForwardIPv4      bool   `json:"forward_ipv4,omitempty"`
	ForwardIPv6      bool   `json:"forward_ipv6,omitempty"`
	EnableIPv6       bool   `json:"enable_ipv6,omitempty"`
	ForwardingStatus string `json:"forwarding_status,omitempty"`
	// Addresses is every address the node answers on. UDP needs them by name:
	// a wildcard-bound socket replies from whatever source the route table
	// picks, and a resolver whose answer arrives from a different address than
	// the query went to is — correctly — ignored by the client.
	Addresses []string  `json:"addresses,omitempty"`
	Services  []Service `json:"services,omitempty"`
}

// startServices binds every listener the node declares. On any failure it
// closes the ones already up, so a node is either fully serving or not at all.
// The returned map is the live response state of each named DNS service, which
// is what a scheduled_dns transition moves.
func startServices(ctx context.Context, services []Service, addresses []string, resolver, trustDir string, recorder *evidenceRecorder) ([]io.Closer, map[string]*dnsState, error) {
	var open []io.Closer
	states := make(map[string]*dnsState)
	for _, svc := range services {
		c, state, err := startService(ctx, svc, addresses, resolver, trustDir, recorder)
		if err != nil {
			return nil, nil, errors.Join(fmt.Errorf("%s/%d: %w", svc.Type, svc.Port, err), closeServices(open))
		}
		if svc.Type == ServiceHTTP || svc.Type == ServiceTLS || svc.Type == ServiceEncryptedDNS {
			mode := svc.DoHResponse
			if svc.Type == ServiceTLS {
				mode = svc.Certificate.Mode
			}
			if err := recorder.record(evidenceEvent{Kind: evidenceServiceState, Service: svc.Name,
				ServiceType: svc.Type, ServicePort: svc.Port, ServiceMode: mode, ServiceStatus: svc.Status}); err != nil {
				return nil, nil, errors.Join(err, closeServices(append(open, c...)))
			}
		}
		if state != nil && svc.Name != "" {
			states[svc.Name] = state
		}
		open = append(open, c...)
	}
	return open, states, nil
}

func closeServices(closers []io.Closer) error {
	var err error
	for _, closer := range closers {
		err = errors.Join(err, closer.Close())
	}
	return err
}

func startService(ctx context.Context, svc Service, addresses []string, resolver, trustDir string, recorder *evidenceRecorder) ([]io.Closer, *dnsState, error) {
	port := strconv.Itoa(svc.Port)
	switch svc.Type {
	case ServiceDNS:
		zone, err := parseZone(svc.Zone, svc.Records)
		if err != nil {
			return nil, nil, err
		}
		var open []io.Closer
		state := newDNSState(svc.DNSFault)
		// Delayed answers run on goroutines of their own; this group bounds them
		// and is joined after the sockets close, so none outlives the service.
		delays := newDelayGroup(ctx)
		// One socket per address rather than one wildcard socket, so every
		// answer leaves from the address the question arrived at.
		for _, a := range bindAddresses(addresses) {
			pc, err := net.ListenPacket("udp", net.JoinHostPort(a, port))
			if err != nil {
				for _, c := range open {
					_ = c.Close()
				}
				_ = delays.Close()
				return nil, nil, err
			}
			delays.wg.Add(1)
			go func() {
				defer delays.wg.Done()
				serveDNS(pc, zone, svc.Name, state, delays, recorder)
			}()
			open = append(open, pc)
		}
		return append(open, delays), state, nil
	case ServiceHTTP:
		listeners, err := listenTCPFamilies(addresses, port)
		if err != nil {
			return nil, nil, err
		}
		for _, ln := range listeners {
			go serveHTTP(ln, svc, recorder)
		}
		return listenersAsClosers(listeners), nil, nil
	case ServiceTCP:
		listeners, err := listenTCPFamilies(addresses, port)
		if err != nil {
			return nil, nil, err
		}
		for _, ln := range listeners {
			go serveSink(ln)
		}
		return listenersAsClosers(listeners), nil, nil
	case ServiceTCPReset:
		listeners, err := listenTCPFamilies(addresses, port)
		if err != nil {
			return nil, nil, err
		}
		return []io.Closer{startTCPResetServer(listeners, svc.Name, recorder)}, nil, nil
	case ServiceSOCKS5:
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			return nil, nil, err
		}
		return []io.Closer{startSOCKS5(ln, svc.Name, resolver, recorder)}, nil, nil
	case ServiceTLS:
		server, err := startTLSService(ctx, svc, trustDir, recorder)
		if err != nil {
			return nil, nil, err
		}
		return []io.Closer{server}, nil, nil
	case ServiceQUIC:
		server, err := startQUICService(ctx, svc, addresses, trustDir)
		if err != nil {
			return nil, nil, err
		}
		return []io.Closer{server}, nil, nil
	case ServiceEncryptedDNS:
		server, err := startEncryptedDNSService(ctx, svc, addresses, trustDir, recorder)
		if err != nil {
			return nil, nil, err
		}
		return []io.Closer{server}, nil, nil
	}
	return nil, nil, fmt.Errorf("unknown service type %q", svc.Type)
}

func listenTCPFamilies(addresses []string, port string) ([]net.Listener, error) {
	want4, want6 := false, false
	for _, raw := range addresses {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		if addr.Is4() {
			want4 = true
		} else {
			want6 = true
		}
	}
	if !want4 && !want6 {
		want4 = true
	}
	var listeners []net.Listener
	open := func(network, host string) error {
		ln, err := net.Listen(network, net.JoinHostPort(host, port))
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			return err
		}
		listeners = append(listeners, ln)
		return nil
	}
	if want4 {
		if err := open("tcp4", "0.0.0.0"); err != nil {
			return nil, err
		}
	}
	if want6 {
		if err := open("tcp6", "::"); err != nil {
			return nil, err
		}
	}
	return listeners, nil
}

func listenersAsClosers(listeners []net.Listener) []io.Closer {
	out := make([]io.Closer, len(listeners))
	for i, listener := range listeners {
		out[i] = listener
	}
	return out
}

// bindAddresses falls back to the wildcard when a node declares none, which is
// what unit tests and a single-address host want.
func bindAddresses(addresses []string) []string {
	if len(addresses) == 0 {
		return []string{""}
	}
	return addresses
}

func parseZone(zone map[string]string, records []DNSRecord) (map[string][]netip.Addr, error) {
	out := make(map[string][]netip.Addr, len(zone)+len(records))
	for name, ip := range zone {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("zone %s: %w", name, err)
		}
		out[dnsKey(name)] = append(out[dnsKey(name)], addr)
	}
	for _, record := range records {
		addr, err := netip.ParseAddr(record.Address)
		if err != nil {
			return nil, fmt.Errorf("record %s: %w", record.Name, err)
		}
		out[dnsKey(record.Name)] = append(out[dnsKey(record.Name)], addr)
	}
	return out, nil
}

// dnsKey normalizes a name for zone lookups: DNS names are case-insensitive and
// the root dot is optional.
func dnsKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

// serveHTTP answers netdoc's captive-portal probe with the 204 it wants and
// everything else with the scenario's configured status.
func serveHTTP(ln net.Listener, svc Service, recorder *evidenceRecorder) {
	body := svc.Body
	if body == "" {
		body = "netdoc-sim\n"
	}
	// The status a client was actually served, recorded per reply. The
	// configured status says what this service would answer; only this says it
	// answered at all.
	served := func(status int) {
		_ = recorder.record(evidenceEvent{Kind: evidenceServiceReply, Service: svc.Name, ServiceType: ServiceHTTP,
			ServicePort: svc.Port, ServiceStatus: status, Result: replyResponded})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		served(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(svc.Status)
		fmt.Fprint(w, body)
		served(svc.Status)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	// Serve returns when the node holder closes the listener on shutdown.
	_ = srv.Serve(ln)
}

// serveSink accepts a connection and drains it. Draining rather than closing:
// netdoc's path-MTU probe writes a few megabytes and times how long the peer
// takes to take them, and a peer that hangs up immediately would look like a
// black hole on a healthy link.
func serveSink(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}()
	}
}

type tcpResetServer struct {
	listeners []net.Listener
	service   string
	recorder  *evidenceRecorder
	wg        sync.WaitGroup
}

func startTCPResetServer(listeners []net.Listener, service string, recorder *evidenceRecorder) *tcpResetServer {
	s := &tcpResetServer{listeners: listeners, service: service, recorder: recorder}
	for _, listener := range listeners {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(listener)
		}()
	}
	return s
}

func (s *tcpResetServer) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if err := s.recorder.record(evidenceEvent{Kind: ServiceTCPReset, Service: s.service, Event: "accepted", Result: "connected"}); err != nil {
			_ = conn.Close()
			return
		}
		s.wg.Add(1)
		go func(conn net.Conn) {
			defer s.wg.Done()
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				_ = conn.Close()
				if err := s.recorder.record(evidenceEvent{Kind: ServiceTCPReset, Service: s.service, Event: "reset", Result: "unsupported_connection"}); err != nil {
					return
				}
				return
			}
			// Give a protocol client a short opportunity to send its greeting, but
			// never let a silent connection outlive the service's bounded lifecycle.
			_ = tcp.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			var one [1]byte
			_, _ = tcp.Read(one[:])
			_ = tcp.SetLinger(0)
			_ = tcp.Close()
			if err := s.recorder.record(evidenceEvent{Kind: ServiceTCPReset, Service: s.service, Event: "reset", Result: "connection_reset"}); err != nil {
				return
			}
		}(conn)
	}
}

func (s *tcpResetServer) Close() error {
	var errs []error
	for _, listener := range s.listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	s.wg.Wait()
	return errors.Join(errs...)
}

// DNS wire constants. Only what a static A/AAAA zone needs.
const (
	dnsTypeA    = 1
	dnsTypeAAAA = 28
	dnsClassIN  = 1
	dnsTTL      = 60

	dnsFlagResponse = 0x8000
	dnsFlagAA       = 0x0400
	dnsFlagRD       = 0x0100
	dnsFlagRA       = 0x0080
	dnsOpcodeMask   = 0x7800

	dnsRcodeSuccess  = 0
	dnsRcodeFormErr  = 1
	dnsRcodeServFail = 2
	dnsRcodeNXDomain = 3
	dnsRcodeNotImpl  = 4

	dnsHeaderLen = 12
	dnsMaxMsg    = 1500
)

func dnsErrorReply(msg []byte, rcode uint16) []byte {
	if len(msg) < dnsHeaderLen || binary.BigEndian.Uint16(msg[2:4])&dnsFlagResponse != 0 {
		return nil
	}
	id := binary.BigEndian.Uint16(msg[0:2])
	flags := binary.BigEndian.Uint16(msg[2:4])
	out := flags&(dnsOpcodeMask|dnsFlagRD) | dnsFlagResponse | dnsFlagAA | dnsFlagRA
	_, qend, ok := dnsParseQuestion(msg)
	if !ok || binary.BigEndian.Uint16(msg[4:6]) != 1 {
		return dnsHeader(id, out, dnsRcodeFormErr, 0, 0)
	}
	return append(dnsHeader(id, out, rcode, 1, 0), msg[dnsHeaderLen:qend]...)
}

// serveDNS is a static authoritative resolver: it answers A and AAAA from the
// scenario's zone, NODATA for a name it knows in a family it does not have, and
// NXDOMAIN for everything else. That is the whole resolver — a scenario proves
// things about netdoc, not about DNS, and this is small enough to audit.
func serveDNS(pc net.PacketConn, zone map[string][]netip.Addr, service string, state *dnsState, delays *delayGroup, recorder *evidenceRecorder) {
	buf := make([]byte, dnsMaxMsg)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		msg := buf[:n]
		scheduled, delay := DNSOutcomeAnswer, time.Duration(0)
		if name, qtype, result, ok := dnsObservation(msg, zone); ok {
			source, _, splitErr := net.SplitHostPort(from.String())
			if splitErr != nil {
				source = from.String()
			}
			var sequence int
			sequence, scheduled, delay = state.next(name, qtype)
			actual := result
			switch scheduled {
			case DNSOutcomeSERVFAIL:
				actual = "SERVFAIL"
			case DNSOutcomeDrop:
				actual = "DROPPED"
			}
			if err := recorder.record(evidenceEvent{Kind: ServiceDNS, Service: service, Name: dnsKey(name),
				Source: source, QueryType: dnsTypeName(qtype), Result: actual, Sequence: sequence,
				ScheduledOutcome: scheduled, ActualOutcome: actual, DelayMs: delay.Milliseconds()}); err != nil {
				return
			}
		}
		switch scheduled {
		case DNSOutcomeDrop:
			// A dropped response is silence, not an error reply: the client has
			// to wait out its own timeout, which is the point of the state.
			continue
		case DNSOutcomeSERVFAIL:
			if reply := dnsErrorReply(msg, dnsRcodeServFail); reply != nil {
				_, _ = pc.WriteTo(reply, from)
			}
			continue
		}
		reply := dnsReply(msg, zone)
		if reply == nil {
			continue
		}
		if scheduled == DNSOutcomeDelay && delay > 0 {
			delays.after(delay, func() { _, _ = pc.WriteTo(reply, from) })
			continue
		}
		_, _ = pc.WriteTo(reply, from)
	}
}

// delayGroup runs bounded delayed answers and tracks their DNS serve loops.
// Close cancels and joins them after the packet sockets close.
type delayGroup struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// pending caps how many answers can be in flight, so a flood of queries
	// during a delay state cannot spawn unbounded goroutines.
	pending chan struct{}
}

const maxPendingDNSDelays = 128

func newDelayGroup(ctx context.Context) *delayGroup {
	g := &delayGroup{pending: make(chan struct{}, maxPendingDNSDelays)}
	g.ctx, g.cancel = context.WithCancel(ctx)
	return g
}

func (g *delayGroup) after(d time.Duration, send func()) {
	select {
	case g.pending <- struct{}{}:
	default:
		return // at capacity: this answer is dropped rather than queued
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer func() { <-g.pending }()
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-g.ctx.Done():
		case <-timer.C:
			send()
		}
	}()
}

func (g *delayGroup) Close() error {
	g.cancel()
	g.wg.Wait()
	return nil
}

// dnsState is one DNS service's response behaviour. It carries both forms of
// schedule: the precomputed per-query outcome list a scenario or campaign
// compiles up front, and the live outcome the fault scheduler sets while netdoc
// runs. A live outcome, once set, wins — the timeline is the newer instruction.
//
// Nothing here draws a random value. Trusted simulator code decides; the
// service only reads.
type dnsState struct {
	mu   sync.Mutex
	a    []string
	aaaa []string
	// asked counts queries per normalized name and family, so the schedule a
	// scenario writes for one name is walked by that name alone. A netdoc run
	// asks this resolver for whatever it likes — the captive-portal host, a
	// public-DNS comparison — and how many of those it sends varies with the
	// host it runs on. A counter shared across names would let that variation
	// slide the target's failure and recovery around.
	//
	// One entry per name asked, for the life of one simulation. The resolver
	// only listens inside the scenario's namespace, and every query it answers
	// already writes a line to the evidence file, which is the larger of the
	// two by far.
	asked map[string]int
	live  string
	delay time.Duration
}

func newDNSState(fault *DNSFault) *dnsState {
	s := &dnsState{asked: make(map[string]int)}
	if fault != nil {
		s.a = append([]string(nil), fault.A...)
		s.aaaa = append([]string(nil), fault.AAAA...)
	}
	return s
}

// set moves the service into a scheduled outcome. Called only from the holder's
// command loop, which the director drives one request at a time.
func (s *dnsState) set(outcome string, delay time.Duration) error {
	switch outcome {
	case DNSOutcomeAnswer, DNSOutcomeSERVFAIL, DNSOutcomeDrop:
		delay = 0
	case DNSOutcomeDelay:
		if delay <= 0 || delay > maxDNSResponseDelay {
			return fmt.Errorf("delay %s is out of range", delay)
		}
	default:
		return fmt.Errorf("unknown outcome %q", outcome)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live, s.delay = outcome, delay
	return nil
}

// next reports the sequence number, outcome and delay for one query. The
// sequence counts queries for this name and family only.
func (s *dnsState) next(name string, qtype uint16) (int, string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	outcome := DNSOutcomeAnswer
	sequence := 1
	var schedule []string
	var family string
	switch qtype {
	case dnsTypeA:
		schedule, family = s.a, "A"
	case dnsTypeAAAA:
		schedule, family = s.aaaa, "AAAA"
	}
	if family != "" {
		key := dnsKey(name) + "\x00" + family
		s.asked[key]++
		sequence = s.asked[key]
		if sequence <= len(schedule) {
			outcome = schedule[sequence-1]
		}
	}
	if s.live != "" {
		return sequence, s.live, s.delay
	}
	return sequence, outcome, 0
}

func dnsObservation(msg []byte, zone map[string][]netip.Addr) (string, uint16, string, bool) {
	if len(msg) < dnsHeaderLen || binary.BigEndian.Uint16(msg[2:4])&dnsFlagResponse != 0 ||
		binary.BigEndian.Uint16(msg[4:6]) != 1 {
		return "", 0, "", false
	}
	name, qend, ok := dnsParseQuestion(msg)
	if !ok {
		return "", 0, "", false
	}
	qtype := binary.BigEndian.Uint16(msg[qend-4 : qend-2])
	qclass := binary.BigEndian.Uint16(msg[qend-2 : qend])
	if qclass != dnsClassIN {
		return name, qtype, "NOT_IMPLEMENTED", true
	}
	addrs, known := zone[dnsKey(name)]
	if !known {
		return name, qtype, "NXDOMAIN", true
	}
	for _, addr := range addrs {
		if qtype == dnsTypeA && addr.Is4() || qtype == dnsTypeAAAA && addr.Is6() {
			return name, qtype, "ANSWER", true
		}
	}
	return name, qtype, "NODATA", true
}

func dnsTypeName(qtype uint16) string {
	switch qtype {
	case dnsTypeA:
		return "A"
	case dnsTypeAAAA:
		return "AAAA"
	default:
		return "TYPE" + strconv.Itoa(int(qtype))
	}
}

// dnsReply builds the response to one query, or nil when the message is not a
// query worth answering.
func dnsReply(msg []byte, zone map[string][]netip.Addr) []byte {
	if len(msg) < dnsHeaderLen {
		return nil
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	if flags&dnsFlagResponse != 0 {
		return nil // somebody else's answer
	}
	id := binary.BigEndian.Uint16(msg[0:2])
	// Response flags: an authoritative answer that echoes the request's opcode
	// and recursion-desired bit, and claims recursion is available so a stub
	// resolver does not complain.
	out := flags&(dnsOpcodeMask|dnsFlagRD) | dnsFlagResponse | dnsFlagAA | dnsFlagRA

	if binary.BigEndian.Uint16(msg[4:6]) != 1 {
		return dnsHeader(id, out, dnsRcodeFormErr, 0, 0)
	}
	name, qend, ok := dnsParseQuestion(msg)
	if !ok {
		return dnsHeader(id, out, dnsRcodeFormErr, 0, 0)
	}
	qtype := binary.BigEndian.Uint16(msg[qend-4 : qend-2])
	qclass := binary.BigEndian.Uint16(msg[qend-2 : qend])
	question := msg[dnsHeaderLen:qend]

	if qclass != dnsClassIN {
		return append(dnsHeader(id, out, dnsRcodeNotImpl, 1, 0), question...)
	}
	addrs, known := zone[dnsKey(name)]
	if !known {
		return append(dnsHeader(id, out, dnsRcodeNXDomain, 1, 0), question...)
	}
	// The name exists. A family it does not have is NODATA — NOERROR with no
	// records — which is what makes an A-only name resolve cleanly for a client
	// that asks for both.
	var matches []netip.Addr
	for _, addr := range addrs {
		if qtype == dnsTypeA && addr.Is4() || qtype == dnsTypeAAAA && addr.Is6() {
			matches = append(matches, addr)
		}
	}
	if len(matches) == 0 {
		return append(dnsHeader(id, out, dnsRcodeSuccess, 1, 0), question...)
	}
	reply := append(dnsHeader(id, out, dnsRcodeSuccess, 1, len(matches)), question...)
	for _, addr := range matches {
		// #nosec G602 -- dnsParseQuestion only returns qend after a complete four-byte suffix.
		reply = append(reply, dnsAnswer(question[:len(question)-4], qtype, addr)...)
	}
	return reply
}

func dnsHeader(id, flags, rcode uint16, qd, an int) []byte {
	h := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(h[0:2], id)
	binary.BigEndian.PutUint16(h[2:4], flags|rcode)
	// #nosec G115 -- this server emits exactly one question and a bounded static-zone answer set.
	binary.BigEndian.PutUint16(h[4:6], uint16(qd))
	// #nosec G115 -- this server emits exactly one question and a bounded static-zone answer set.
	binary.BigEndian.PutUint16(h[6:8], uint16(an))
	return h
}

// dnsParseQuestion returns the queried name and the offset just past the
// question section. Compression pointers are rejected: they are illegal in a
// question, and refusing them keeps this parser loop-free by construction.
func dnsParseQuestion(msg []byte) (string, int, bool) {
	var labels []string
	i := dnsHeaderLen
	for {
		if i >= len(msg) {
			return "", 0, false
		}
		n := int(msg[i])
		if n == 0 {
			i++
			break
		}
		if n > 63 || i+1+n > len(msg) {
			return "", 0, false
		}
		labels = append(labels, string(msg[i+1:i+1+n]))
		i += 1 + n
	}
	if i+4 > len(msg) {
		return "", 0, false
	}
	return strings.Join(labels, "."), i + 4, true
}

// dnsAnswer builds one resource record for name (already wire-encoded).
func dnsAnswer(name []byte, qtype uint16, addr netip.Addr) []byte {
	rdata := addr.AsSlice()
	rr := make([]byte, 0, len(name)+10+len(rdata))
	rr = append(rr, name...)
	rr = binary.BigEndian.AppendUint16(rr, qtype)
	rr = binary.BigEndian.AppendUint16(rr, dnsClassIN)
	rr = binary.BigEndian.AppendUint32(rr, dnsTTL)
	// #nosec G115 -- netip addresses have exactly 4 or 16 bytes of RDATA.
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(rdata)))
	return append(rr, rdata...)
}

// serveHolderCommands answers scheduled-fault requests until the director
// closes the holder's stdin or the context is cancelled. Either one means the
// simulation is over.
func serveHolderCommands(ctx context.Context, r io.Reader, w io.Writer, dns map[string]*dnsState, recorder *evidenceRecorder) error {
	lines := make(chan string, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recorder.failed:
			return err
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if line == holderEvidenceCheck {
				if err := recorder.Err(); err != nil {
					return err
				}
			}
			if reply := holderCommandReply(line, dns); reply != "" {
				fmt.Fprintln(w, reply)
			}
		}
	}
}

// holderCommandReply handles one director request. An unknown line is ignored
// rather than answered, so a future director talking to an old holder blocks on
// its own read deadline instead of acting on a misread reply.
func holderCommandReply(line string, dns map[string]*dnsState) string {
	if line == holderEvidenceCheck {
		return holderEvidenceReady
	}
	fields := strings.Fields(line)
	if len(fields) > 0 && fields[0] == holderProbeCommand {
		return holderProbeReply(fields)
	}
	if len(fields) != 4 || fields[0] != holderDNSCommand {
		return ""
	}
	state, ok := dns[fields[1]]
	if !ok {
		return holderDNSError + " unknown dns service"
	}
	ms, err := strconv.ParseInt(fields[3], 10, 32)
	if err != nil || ms < 0 {
		return holderDNSError + " bad delay"
	}
	if err := state.set(fields[2], time.Duration(ms)*time.Millisecond); err != nil {
		return holderDNSError + " " + err.Error()
	}
	return holderDNSApplied
}

// holderProbeReply answers "probe <address> <port> <timeout-ms>" by opening one
// TCP connection from inside this node's namespace. This is the simulator's own
// observation of the path: it is made by simulator code, to a simulator-owned
// endpoint, and it reads nothing netdoc reported — so it is free to contradict
// the diagnosis. Only a literal address is accepted, so an observation can
// never depend on the node's resolver.
func holderProbeReply(fields []string) string {
	if len(fields) != 4 {
		return holderProbeResult + " " + holderProbeError
	}
	addr, err := netip.ParseAddr(fields[1])
	if err != nil || addr.Zone() != "" {
		return holderProbeResult + " " + holderProbeError
	}
	port, err := strconv.ParseUint(fields[2], 10, 16)
	if err != nil || port == 0 {
		return holderProbeResult + " " + holderProbeError
	}
	ms, err := strconv.ParseInt(fields[3], 10, 32)
	if err != nil || ms <= 0 {
		return holderProbeResult + " " + holderProbeError
	}
	dialer := net.Dialer{Timeout: time.Duration(ms) * time.Millisecond}
	conn, err := dialer.Dial("tcp", netip.AddrPortFrom(addr, uint16(port)).String())
	if err != nil {
		// Only an actual reset from the far end is a refusal. Anything the local
		// kernel decided on its own — no route, an administrative reject on the
		// way out — is the path failing, not the target answering, so it stays
		// unreachable rather than being promoted to a statement about a service.
		if isConnectionRefused(err) {
			return holderProbeResult + " " + holderProbeRefused
		}
		return holderProbeResult + " " + holderProbeFailed
	}
	_ = conn.Close()
	return holderProbeResult + " " + holderProbeReached
}

var errNoStart = errors.New("director closed the connection before the network was configured")
