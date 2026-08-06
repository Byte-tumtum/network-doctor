package simulation

import (
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
	"time"
)

// Holder protocol. The director and the node holder exchange three lines over
// the holder's stdin/stdout: the holder announces its namespace is ready, the
// director answers once the namespace is addressed and routed, and the holder
// confirms its listeners are up. Nothing is reachable before that last line, so
// a probe can never race the topology.
const (
	// NodeCommand is the hidden argv[1] that makes the binary a node holder.
	NodeCommand         = "__node"
	holderNSReady       = "ns-ready"
	holderStart         = "start"
	holderServicesReady = "services-ready"
)

// nodeConfig is what the director hands a holder.
type nodeConfig struct {
	Name             string `json:"name"`
	Resolver         string `json:"resolver,omitempty"`
	Evidence         string `json:"evidence,omitempty"`
	TrustDir         string `json:"trust_dir,omitempty"`
	ForwardIPv4      bool   `json:"forward_ipv4,omitempty"`
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
func startServices(ctx context.Context, services []Service, addresses []string, resolver, trustDir string, recorder *evidenceRecorder) ([]io.Closer, error) {
	var open []io.Closer
	closeAll := func() {
		for _, c := range open {
			c.Close()
		}
	}
	for _, svc := range services {
		c, err := startService(ctx, svc, addresses, resolver, trustDir, recorder)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("%s/%d: %w", svc.Type, svc.Port, err)
		}
		open = append(open, c...)
	}
	return open, nil
}

func startService(ctx context.Context, svc Service, addresses []string, resolver, trustDir string, recorder *evidenceRecorder) ([]io.Closer, error) {
	port := strconv.Itoa(svc.Port)
	switch svc.Type {
	case ServiceDNS:
		zone, err := parseZone(svc.Zone)
		if err != nil {
			return nil, err
		}
		var open []io.Closer
		// One socket per address rather than one wildcard socket, so every
		// answer leaves from the address the question arrived at.
		for _, a := range bindAddresses(addresses) {
			pc, err := net.ListenPacket("udp", net.JoinHostPort(a, port))
			if err != nil {
				for _, c := range open {
					c.Close()
				}
				return nil, err
			}
			go serveDNS(pc, zone, svc.Name, recorder)
			open = append(open, pc)
		}
		return open, nil
	case ServiceHTTP:
		// TCP needs no such care: a connection's replies carry the local
		// address the handshake settled on.
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			return nil, err
		}
		go serveHTTP(ln, svc)
		return []io.Closer{ln}, nil
	case ServiceTCP:
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			return nil, err
		}
		go serveSink(ln)
		return []io.Closer{ln}, nil
	case ServiceSOCKS5:
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			return nil, err
		}
		return []io.Closer{startSOCKS5(ln, svc.Name, resolver, recorder)}, nil
	case ServiceTLS:
		server, err := startTLSService(ctx, svc, trustDir, recorder)
		if err != nil {
			return nil, err
		}
		return []io.Closer{server}, nil
	}
	return nil, fmt.Errorf("unknown service type %q", svc.Type)
}

// bindAddresses falls back to the wildcard when a node declares none, which is
// what unit tests and a single-address host want.
func bindAddresses(addresses []string) []string {
	if len(addresses) == 0 {
		return []string{""}
	}
	return addresses
}

func parseZone(zone map[string]string) (map[string]netip.Addr, error) {
	out := make(map[string]netip.Addr, len(zone))
	for name, ip := range zone {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("zone %s: %w", name, err)
		}
		out[dnsKey(name)] = addr
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
func serveHTTP(ln net.Listener, svc Service) {
	body := svc.Body
	if body == "" {
		body = "netdoc-sim\n"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(svc.Status)
		fmt.Fprint(w, body)
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
	dnsRcodeNXDomain = 3
	dnsRcodeNotImpl  = 4

	dnsHeaderLen = 12
	dnsMaxMsg    = 1500
)

// serveDNS is a static authoritative resolver: it answers A and AAAA from the
// scenario's zone, NODATA for a name it knows in a family it does not have, and
// NXDOMAIN for everything else. That is the whole resolver — a scenario proves
// things about netdoc, not about DNS, and this is small enough to audit.
func serveDNS(pc net.PacketConn, zone map[string]netip.Addr, service string, recorder *evidenceRecorder) {
	buf := make([]byte, dnsMaxMsg)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		msg := buf[:n]
		if name, qtype, result, ok := dnsObservation(msg, zone); ok {
			source, _, splitErr := net.SplitHostPort(from.String())
			if splitErr != nil {
				source = from.String()
			}
			recorder.record(evidenceEvent{Kind: ServiceDNS, Service: service, Name: dnsKey(name),
				Source: source, QueryType: dnsTypeName(qtype), Result: result})
		}
		if reply := dnsReply(msg, zone); reply != nil {
			_, _ = pc.WriteTo(reply, from)
		}
	}
}

func dnsObservation(msg []byte, zone map[string]netip.Addr) (string, uint16, string, bool) {
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
	addr, known := zone[dnsKey(name)]
	if !known {
		return name, qtype, "NXDOMAIN", true
	}
	if qtype == dnsTypeA && addr.Is4() || qtype == dnsTypeAAAA && addr.Is6() {
		return name, qtype, "ANSWER", true
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
func dnsReply(msg []byte, zone map[string]netip.Addr) []byte {
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
	addr, known := zone[dnsKey(name)]
	if !known {
		return append(dnsHeader(id, out, dnsRcodeNXDomain, 1, 0), question...)
	}
	// The name exists. A family it does not have is NODATA — NOERROR with no
	// records — which is what makes an A-only name resolve cleanly for a client
	// that asks for both.
	match := qtype == dnsTypeA && addr.Is4() || qtype == dnsTypeAAAA && addr.Is6()
	if !match {
		return append(dnsHeader(id, out, dnsRcodeSuccess, 1, 0), question...)
	}
	reply := append(dnsHeader(id, out, dnsRcodeSuccess, 1, 1), question...)
	return append(reply, dnsAnswer(question[:len(question)-4], qtype, addr)...)
}

func dnsHeader(id, flags, rcode uint16, qd, an int) []byte {
	h := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(h[0:2], id)
	binary.BigEndian.PutUint16(h[2:4], flags|rcode)
	binary.BigEndian.PutUint16(h[4:6], uint16(qd))
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
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(rdata)))
	return append(rr, rdata...)
}

// waitForShutdown blocks until the director closes the holder's stdin or the
// context is cancelled. Either one means the simulation is over.
func waitForShutdown(ctx context.Context, r io.Reader) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, r)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

var errNoStart = errors.New("director closed the connection before the network was configured")
