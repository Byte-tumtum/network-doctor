// Package diagnostic implements target parsing, native network probes, and
// diagnosis without depending on terminal presentation.
package diagnostic

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// Status is a probe's five-state outcome. Warn = degraded but functional
// (high latency, some addresses failing, ambiguous source interface, missing
// service banner, or direct egress blocked while another path works), never
// counted as a failure. Skip = a prerequisite failed (an
// independent probe is never Skipped for an unrelated sibling's failure).
// NotApplicable = the probe doesn't apply at all (DNS on an IP literal, a
// protocol row absent for this port), not counted as a failure.
type Status int

const (
	StatusPass Status = iota
	StatusWarn
	StatusFail
	StatusSkip
	StatusNA
)

var statusNames = [...]string{"PASS", "WARN", "FAIL", "SKIP", "N/A"}

// Route failure causes are attached to the existing direct-egress row. They
// describe only evidence the local kernel exposes; none claims an alternate
// path works without a successful probe proving that separately.
const (
	RouteCauseNoDefaultRoute      = "no_default_route"
	RouteCauseGatewayUnreachable  = "gateway_unreachable"
	RouteCauseSelectedPathFailed  = "selected_path_failed"
	RouteCausePreferredPathFailed = "preferred_route_failed"
	FamilyCauseIPv4Unreachable    = "ipv4_unreachable"
	FamilyCauseIPv6Unreachable    = "ipv6_unreachable"
	DNSCauseTimeout               = "dns_timeout"
	DNSCauseTemporaryFailure      = "dns_temporary_failure"
	ConnectionCauseReset          = "connection_reset"
)

const (
	FamilyReachable   = "reachable"
	FamilyUnreachable = "unreachable"
)

// FamilyConnectivity records the independently tested direct-egress result
// for each address family. It is additive JSON evidence: generic hostname
// fallback cannot turn one family's failure into an apparent success here.
type FamilyConnectivity struct {
	IPv4 string
	IPv6 string
}

func (s Status) String() string {
	if s < 0 || s >= Status(len(statusNames)) {
		return "?"
	}
	return statusNames[s]
}

// Attempt is one connection attempt against a single address.
type Attempt struct {
	IP  net.IP
	Dur time.Duration
	Err error
}

// Ms renders a duration that actually elapsed as at least 1ms. Milliseconds()
// truncates, so a fast local check (an interface lookup, a cached resolve) or a
// LAN connect would report the same 0 as work that never happened at all, and
// 0 is the only signal a reader has for the latter.
func Ms(d time.Duration) int64 {
	if d > 0 && d < time.Millisecond {
		return 1
	}
	return d.Milliseconds()
}

// since is time.Since with a nonzero floor. Windows' clock granularity is
// coarse enough to report 0 for a fast connect, which Ms would then render as
// the 0ms that means "never ran". A tick that coarse can't tell us anything
// finer than "under a millisecond", and Ms already says that as 1ms.
func since(start time.Time) time.Duration {
	if d := time.Since(start); d > 0 {
		return d
	}
	return time.Nanosecond
}

// ProbeID is a stable DAG node id.
type ProbeID string

const (
	ProbeIface     ProbeID = "iface"
	ProbeSSID      ProbeID = "ssid"
	ProbeInternet  ProbeID = "internet_tcp"
	ProbeQUIC      ProbeID = "quic_udp_443"
	ProbeProxy     ProbeID = "proxy_connect"
	ProbeDNS       ProbeID = "dns"
	ProbeDNSPublic ProbeID = "dns_public"
	// ProbeDNSEncrypted is an independent branch off the interface, not a child
	// of the plaintext rows: plain and encrypted DNS are separate capabilities,
	// and either can work while the other is blocked.
	ProbeDNSEncrypted ProbeID = "dns_encrypted"
	ProbeTargetTCP    ProbeID = "target_tcp"
	ProbePMTU         ProbeID = "path_mtu"
	ProbeTLS          ProbeID = "tls"
	ProbeHTTP         ProbeID = "http"
	ProbeHTTPS        ProbeID = "https"
	ProbeSSH          ProbeID = "ssh_banner"
	ProbeSMTP         ProbeID = "smtp_banner"
)

// QUIC failure causes distinguish a silent UDP/443 path from endpoint or
// protocol failures without claiming that a firewall was proven responsible.
const (
	QUICCauseTimeout   = "timeout"
	QUICCauseHandshake = "quic_handshake_failure"
)

// ProbeResult is the typed contract the diagnosis engine and renderer consume.
// Detail/Fix are derived human text, never parsed back.
type ProbeResult struct {
	ID     ProbeID
	Status Status
	// Cause is an optional stable machine-readable reason for failures where a
	// single probe has materially different remediation paths.
	Cause       string
	Families    *FamilyConnectivity
	downgraded  bool     // downgradeEgress rewrote a direct-egress failure to Warn.
	Portal      *Portal  // non-nil when egress is intercepted, not dead.
	Addrs       []net.IP // DNS publishes all A records here
	DNSNotFound bool     // the resolver found no A/AAAA records
	SelectedIP  net.IP   // winning/pinned IP used by this probe
	Source      net.IP
	Iface       string
	Network     string // connected Wi-Fi SSID, empty when wired/unknown
	Attempts    []Attempt
	Dur         time.Duration // wall time the probe took; zero for probes that never ran
	Detail      string
	Fix         string
	timedOut    bool // protocol failure was a timeout; used to correlate PMTU evidence.
	// clockOffset is this machine's clock minus the Date of a 204 from
	// portalProbeURL: positive when the local clock runs fast. Zero means
	// there was no usable reading, which behaves the same as a correct clock
	// because both leave nothing to say.
	clockOffset time.Duration
	// resolver is the second-opinion DNS server ProbeDNSPublic queried, so the
	// cross-probe pass can name it in prose without reaching for a constant.
	resolver string
}

// Portal is structured captive-portal evidence. RedirectURL is empty when the
// interception did not advertise a valid HTTP(S) sign-in URL.
type Portal struct {
	RedirectURL string
}

// Probe is one DAG node. Run receives an immutable snapshot of just its
// dependency outputs and must honor ctx and never panic.
type Probe struct {
	ID   ProbeID
	Name string
	Deps []ProbeID
	Run  func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult
}

// DefaultProbeTimeout bounds a single probe when the caller names no other
// value. The runners take the effective timeout as an argument (RunAll) or a
// field (the TUI model), so two diagnoses in one process keep separate budgets;
// this is only the default the -timeout flag starts from, and the fallback for
// the rare probe handed a context with no deadline at all.
const DefaultProbeTimeout = 4 * time.Second

const (
	// attemptDelay is the Happy Eyeballs (RFC 8305) connection-attempt stagger:
	// the next address starts this long after the previous one, or immediately
	// once the previous attempt fails.
	attemptDelay = 250 * time.Millisecond
	// maxAttempts bounds the recorded/attempted addresses per probe.
	maxAttempts = 16
	// warnRTT is the connect latency above which a successful dial is reported
	// as degraded rather than a clean pass.
	warnRTT = 500 * time.Millisecond
)

// Proxy failure causes. Detail and Fix remain the user-facing explanation;
// these values let reports and simulators distinguish the failing stage
// without parsing prose.
const (
	ProxyCauseUnreachable            = "proxy_unreachable"
	ProxyCauseClientDNS              = "client_dns_failure"
	ProxyCauseProxyDNS               = "proxy_side_dns_failure"
	ProxyCauseDestinationUnreachable = "destination_unreachable_from_proxy"
	ProxyCauseProtocol               = "proxy_protocol_failure"
)

// TLS failure causes preserve the existing TLS row while making its distinct
// remediation paths available to JSON consumers without parsing Go errors.
const (
	TLSCauseCertificateExpired = "certificate_expired"
	TLSCauseCertificateNotYet  = "certificate_not_yet_valid"
	TLSCauseHostnameMismatch   = "hostname_mismatch"
	TLSCauseUntrustedIssuer    = "untrusted_issuer"
	TLSCauseHandshake          = "tls_handshake_failure"
	TLSCauseTCPUnreachable     = "tcp_unreachable"
	TLSCauseTimeout            = "timeout"
	TLSCauseConnectionClosed   = "connection_closed"
)

// Path-MTU probe sizes. See pmtuProbe for what the asymmetry between them
// proves.
const (
	// pmtuSendBuffer is the SO_SNDBUF the probe asks for. Small on purpose:
	// with the kernel's autotuned buffer a Write returns hundreds of KiB before
	// any acknowledgement is due, and an unacknowledged Write proves nothing.
	pmtuSendBuffer = 4 << 10
	// pmtuPayloadSize is pushed at the target in a single Write. Enough to
	// require multiple ordinary TCP segments, small enough to stay a polite
	// amount of traffic.
	pmtuPayloadSize = 24 << 10
	// pmtuWriteWait bounds the stall the probe is willing to sit through.
	pmtuWriteWait = 2 * time.Second
	// pmtuHeadroom keeps the write deadline inside the probe budget, so a stall
	// is reported as evidence instead of as a cancelled probe.
	pmtuHeadroom = 250 * time.Millisecond
)

// tlsRecordHeader frames a handshake record declaring a full 16 KiB body, and
// prefixes the PMTU payload on TLS targets. An OpenSSL-based server buffers,
// and so acknowledges, the whole declared record before rejecting the garbage
// inside it, where an unprefixed write gets reset after a few bytes and leaves
// the path question unanswered.
var tlsRecordHeader = []byte{0x16, 0x03, 0x01, 0x40, 0x00}

// ConnectivityProbeHost is the host used by the generic (no-target) DNS and
// connectivity probes.
const ConnectivityProbeHost = "connectivitycheck.gstatic.com"

// quicProbePort is separate from the target port: this Internet-health probe
// always asks the fixed connectivity endpoint whether QUIC works on UDP/443.
const quicProbePort = 443

// DefaultPublicDNS is the second-opinion resolver used unless --public-dns
// names another. Every label and detail string derives from the configured
// value, and an empty one drops the probe entirely, so a network with a strict
// egress policy never dials it.
const DefaultPublicDNS = "8.8.8.8"

// publicDNSServer is the dial address for a second-opinion resolver IP; it
// brackets IPv6 literals.
func publicDNSServer(ip string) string { return net.JoinHostPort(ip, "53") }

// portalProbeURL answers 204 with an empty body on an unintercepted path.
// Plain HTTP on purpose, since that's the request a captive portal grabs.
// A var only so tests can point it at a local server; nothing reassigns it.
var portalProbeURL = "http://" + ConnectivityProbeHost + "/generate_204"

// internetEndpoints4/6 are the ordered direct-egress endpoints per address
// family; first connect wins within a family. Honestly "direct TCP egress":
// proxy-only networks can fail this.
const (
	internetEndpointCloudflareIPv4 = "1.1.1.1"
	internetEndpointGoogleIPv4     = "8.8.8.8"
	internetEndpointCloudflareIPv6 = "2606:4700:4700::1111"
	internetEndpointGoogleIPv6     = "2001:4860:4860::8888"
)

var (
	internetEndpoints4 = []net.IP{net.ParseIP(internetEndpointCloudflareIPv4), net.ParseIP(internetEndpointGoogleIPv4)}
	internetEndpoints6 = []net.IP{net.ParseIP(internetEndpointCloudflareIPv6), net.ParseIP(internetEndpointGoogleIPv6)}
)

// InternetProbeEndpoints returns the ordered direct-egress endpoints for IPv4
// and IPv6. The slices and every IP in them are defensive copies.
func InternetProbeEndpoints() (ipv4, ipv6 []net.IP) {
	return cloneIPs(internetEndpoints4), cloneIPs(internetEndpoints6)
}

func cloneIPs(ips []net.IP) []net.IP {
	out := make([]net.IP, len(ips))
	for i, ip := range ips {
		out[i] = append(net.IP(nil), ip...)
	}
	return out
}

// netops holds every network/OS touchpoint the probes use, as function fields
// so tests can stub them and run probes deterministically without real
// network access. Production code always goes through defaultOps.
type netops struct {
	interfaces     func() ([]net.Interface, error)
	interfaceAddrs func(*net.Interface) ([]net.Addr, error)
	sources        *SourceAddresses
	// lookupIP resolves host and reports the resolver that was dialed, as a
	// host:port string. An empty server means "couldn't tell", not "none".
	lookupIP func(ctx context.Context, host string) ([]net.IP, string, error)
	// lookupPublicIP resolves host against server (a host:port second-opinion
	// resolver), bypassing whatever the system is configured to use.
	lookupPublicIP func(ctx context.Context, host, server string) ([]net.IP, error)
	dialContext    func(ctx context.Context, network, addr string) (net.Conn, error)
	quicHandshake  func(context.Context, net.Conn, *tls.Config) (quicState, error)
	sendBuffer     func(net.Conn) (int, error)
	// queued reports the bytes a socket has written but not had acknowledged.
	// It fails on platforms with no such query, and on anything that is not a
	// real socket, which is how the PMTU probe picks its inference.
	queued       func(net.Conn) (int, error)
	tcpMSS       func(net.Conn) (int, error)
	dialTLS      func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error)
	tlsRootCAs   *x509.CertPool
	ssid         func(ctx context.Context, iface string) string
	proxyFromEnv func(*http.Request) (*url.URL, error)
	// portalCheck returns the status code portalProbeURL answered with, an
	// optional validated HTTP(S) redirect URL, and the response's Date, which
	// is the zero time when the header was absent or unparsable.
	// Nil means "don't ask", which is how tests opt out of the HTTP round trip.
	portalCheck func(ctx context.Context) (int, string, time.Time, error)
	// routeCause classifies a failed direct path from OS route/neighbor state.
	// Nil keeps deterministic probe unit tests independent of the host.
	routeCause func(net.IP) string
}

// SourceAddresses are the usable IPv4 and IPv6 addresses selected by
// --iface. An exact-IP selection sets only its own family.
type SourceAddresses struct {
	IPv4 net.IP
	IPv6 net.IP
	// Iface is the selected interface name. It is empty when --iface was
	// omitted or given an exact local IP; probes bind by the addresses above,
	// while drill-down tools may bind by this name.
	Iface string
}

var defaultOps = &netops{
	interfaces:     net.Interfaces,
	interfaceAddrs: (*net.Interface).Addrs,
	lookupIP: func(ctx context.Context, host string) ([]net.IP, string, error) {
		return lookupIPWithDial(ctx, host, new(net.Dialer).DialContext)
	},
	lookupPublicIP: func(ctx context.Context, host, server string) ([]net.IP, error) {
		return lookupIPPublicWithDial(ctx, host, new(net.Dialer).DialContext, server)
	},
	dialContext:   new(net.Dialer).DialContext,
	quicHandshake: handshakeQUIC,
	sendBuffer:    socketSendBuffer,
	queued:        socketQueued,
	tcpMSS:        socketMSS,
	dialTLS: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		d := tls.Dialer{NetDialer: new(net.Dialer), Config: cfg}
		return d.DialContext(ctx, network, addr)
	},
	ssid:         ssid,
	proxyFromEnv: proxyFromEnvironment,
	portalCheck: func(ctx context.Context) (int, string, time.Time, error) {
		return portalCheckWithDial(ctx, new(net.Dialer).DialContext)
	},
	routeCause: routeFailureCause,
}

// ResolveSource resolves an interface name to one usable address per family,
// or an exact local IP to only that address's family.
func ResolveSource(iface string) (*SourceAddresses, error) {
	if want := net.ParseIP(iface); want != nil {
		ifaces, err := net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("list interfaces: %w", err)
		}
		for i := range ifaces {
			addrs, err := ifaces[i].Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipFromAddr(addr).Equal(want) {
					return sourceAddresses([]net.Addr{&net.IPAddr{IP: want}}), nil
				}
			}
		}
		return nil, fmt.Errorf("source IP %s is not assigned to a local interface", want)
	}
	chosen, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("interface %q: %w", iface, err)
	}
	addrs, err := chosen.Addrs()
	if err != nil {
		return nil, fmt.Errorf("addresses for interface %q: %w", iface, err)
	}
	if sources := sourceAddresses(addrs); sources != nil {
		sources.Iface = chosen.Name
		return sources, nil
	}
	return nil, fmt.Errorf("interface %q has no usable IP address", iface)
}

func ipFromAddr(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.IPNet:
		return a.IP
	case *net.IPAddr:
		return a.IP
	}
	return nil
}

func sourceAddresses(addrs []net.Addr) *SourceAddresses {
	var sources SourceAddresses
	for _, addr := range addrs {
		ip := ipFromAddr(addr)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil && sources.IPv4 == nil {
			sources.IPv4 = append(net.IP(nil), ip4...)
		}
		if ip.To4() == nil && sources.IPv6 == nil {
			sources.IPv6 = append(net.IP(nil), ip...)
		}
	}
	if sources.IPv4 == nil && sources.IPv6 == nil {
		return nil
	}
	return &sources
}

func (s SourceAddresses) primary() net.IP {
	if s.IPv4 != nil {
		return s.IPv4
	}
	return s.IPv6
}

func containsSources(addrs []net.Addr, want *SourceAddresses) bool {
	found4, found6 := want.IPv4 == nil, want.IPv6 == nil
	for _, addr := range addrs {
		ip := ipFromAddr(addr)
		found4 = found4 || ip.Equal(want.IPv4)
		found6 = found6 || ip.Equal(want.IPv6)
	}
	return found4 && found6
}

func opsFromSources(sources *SourceAddresses) *netops {
	o := *defaultOps
	copySources := &SourceAddresses{
		IPv4: append(net.IP(nil), sources.IPv4...),
		IPv6: append(net.IP(nil), sources.IPv6...),
	}
	o.sources = copySources
	o.dialContext = dialContextFromSources(copySources)
	o.dialTLS = func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		conn, err := o.dialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, cfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	o.lookupIP = func(ctx context.Context, host string) ([]net.IP, string, error) {
		return lookupIPWithDial(ctx, host, o.dialContext)
	}
	o.lookupPublicIP = func(ctx context.Context, host, server string) ([]net.IP, error) {
		return lookupIPPublicWithDial(ctx, host, o.dialContext, server)
	}
	o.portalCheck = func(ctx context.Context) (int, string, time.Time, error) {
		return portalCheckWithDial(ctx, o.dialContext)
	}
	return &o
}

func dialerFromSource(source net.IP, network string, resolverDial func(context.Context, string, string) (net.Conn, error)) *net.Dialer {
	var local net.Addr = &net.TCPAddr{IP: source}
	if strings.HasPrefix(network, "udp") {
		local = &net.UDPAddr{IP: source}
	}
	d := &net.Dialer{LocalAddr: local}
	// Hostname resolution performed inside DialContext must use the same source
	// path too. Resolver destinations are already numeric, so this cannot recurse.
	d.Resolver = &net.Resolver{PreferGo: true, Dial: resolverDial}
	return d
}

// dialFamily performs one source-bound dial. It is a variable so tests can
// order per-family results without a live network.
var dialFamily = func(ctx context.Context, source net.IP, network, addr string, resolverDial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	return dialerFromSource(source, network, resolverDial).DialContext(ctx, network, addr)
}

// errFamilyLost stands in for a connection discarded because the other address
// family answered first. It never reaches a caller: the winning family's
// connection is in the same channel and ends the fan-in loop.
var errFamilyLost = errors.New("connection superseded by the other address family")

func dialContextFromSources(sources *SourceAddresses) func(context.Context, string, string) (net.Conn, error) {
	var dial func(context.Context, string, string) (net.Conn, error)
	dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if source, family := sources.forDial(network, addr); family != 0 {
			if source == nil {
				return nil, fmt.Errorf("selected interface has no IPv%d source address", family)
			}
			return dialFamily(ctx, source, network, addr, dial)
		}

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		type result struct {
			conn net.Conn
			err  error
		}
		results := make(chan result, 2)
		// won marks the connection the fan-in below will hand to the caller.
		// Whoever loses the claim owns its socket and has to close it.
		var won atomic.Bool
		families := 0
		for _, item := range []struct {
			source  net.IP
			network string
		}{{sources.IPv4, network + "4"}, {sources.IPv6, network + "6"}} {
			if item.source == nil {
				continue
			}
			families++
			go func(source net.IP, familyNetwork string) {
				conn, err := dialFamily(ctx, source, familyNetwork, addr, dial)
				// Exactly one report per launched goroutine, or the fan-in waits
				// for a result that can never arrive. A connection that cannot be
				// handed back, because the deadline passed or the other family
				// already won, is closed here rather than left to the GC.
				if err == nil && (ctx.Err() != nil || !won.CompareAndSwap(false, true)) {
					_ = conn.Close()
					conn = nil
					if err = ctx.Err(); err == nil {
						err = errFamilyLost
					}
				}
				results <- result{conn, err}
			}(item.source, item.network)
		}
		var errs []error
		for range families {
			result := <-results
			if result.err == nil {
				return result.conn, nil
			}
			errs = append(errs, result.err)
		}
		return nil, errors.Join(errs...)
	}
	return dial
}

// forDial returns the selected source and address family. Family zero means a
// hostname on a generic network, which must try each selected family.
func (s SourceAddresses) forDial(network, addr string) (net.IP, int) {
	if strings.HasSuffix(network, "4") {
		return s.IPv4, 4
	}
	if strings.HasSuffix(network, "6") {
		return s.IPv6, 6
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() != nil {
				return s.IPv4, 4
			}
			return s.IPv6, 6
		}
	}
	return nil, 0
}

// lookupIPWithDial resolves host and reports which resolver was on the other
// end of the wire. The server identity comes free from the Go resolver's Dial
// hook, which already parses resolv.conf, so we don't have to. Release builds are
// CGO_ENABLED=0 and so already resolve this way; PreferGo only pins that
// behavior for local cgo builds.
//
// Windows (and anywhere the Go resolver isn't used) never calls the hook, so the
// server comes back empty and the row reads as it did before. Reading
// GetNetworkParams would fix that, if the missing identity ever bites.
func lookupIPWithDial(ctx context.Context, host string, dial func(context.Context, string, string) (net.Conn, error)) ([]net.IP, string, error) {
	var (
		mu     sync.Mutex
		server string
	)
	r := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			mu.Lock()
			// Last dial wins rather than the provably-answering one, which is right for
			// a single server, and right on failover, since the resolver
			// exhausts one server before trying the next.
			server = addr
			mu.Unlock()
			return dial(ctx, network, addr)
		},
	}
	ips, err := r.LookupIP(ctx, "ip", host)
	mu.Lock()
	defer mu.Unlock()
	return ips, server, err
}

// lookupIPPublicWithDial bypasses the configured resolver for a second opinion.
// Unavailability is reported as N/A by publicDNSProbe, never as a failure.
func lookupIPPublicWithDial(ctx context.Context, host string, dial func(context.Context, string, string) (net.Conn, error), server string) ([]net.IP, error) {
	r := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dial(ctx, network, server)
		},
	}
	return r.LookupIP(ctx, "ip", host)
}

// dnsServerLabel shortens a resolver dial address for a probe row: the bare host
// on port 53, host:port otherwise, since a stub resolver on 5353 is worth seeing.
func dnsServerLabel(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if port == "53" {
		return host
	}
	return addr
}

// portalCheckWithDial fetches portalProbeURL and reports the status code it got
// plus a valid HTTP(S) redirect URL, if the response advertised one, and the
// Date the responder stamped on it.
// Proxy and redirect following are both off: the direct-egress row must not
// borrow the proxy's path, and an interception usually announces itself as
// the 302 we'd otherwise chase to a sign-in page.
func portalCheckWithDial(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) (int, string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, portalProbeURL, nil)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	c := &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dial,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: 64 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	_ = resp.Body.Close()
	// http.ParseTime accepts the three date formats RFC 9110 allows. An absent
	// or unparsable Date leaves the zero time, and every caller reads that as
	// "no reading" rather than as a time.
	date, _ := http.ParseTime(resp.Header.Get("Date"))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if u, err := resp.Location(); err == nil && u.Hostname() != "" {
			switch strings.ToLower(u.Scheme) {
			case "http", "https":
				return resp.StatusCode, u.String(), date, nil
			}
		}
	}
	return resp.StatusCode, "", date, nil
}

// BuildProbesFromSources constructs the DAG with separate selected-interface
// addresses for IPv4 and IPv6. publicDNS is a bare IP, or "" to leave that
// probe out of the DAG altogether, since a skipped row would still have had to dial
// to be skipped.
func BuildProbesFromSources(t *Target, sources *SourceAddresses, publicDNS string) []Probe {
	o := defaultOps
	if sources != nil {
		o = opsFromSources(sources)
	}
	probes := o.buildProbes(t, publicDNS)
	for i := range probes {
		probes[i].Run = wrapRun(probes[i].Run)
	}
	return probes
}

// wrapRun times and sanitizes one probe's Run.
func wrapRun(run func(context.Context, map[ProbeID]ProbeResult) ProbeResult) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		start := time.Now()
		r := cleanResult(run(ctx, deps))
		r.Dur = since(start)
		return r
	}
}

// cleanResult scrubs the human-readable fields of a probe result. Names and
// IPs are not here: probe names are ours, and net.IP renders itself.
func cleanResult(r ProbeResult) ProbeResult {
	r.Detail, r.Fix = textsafe.Clean(r.Detail), textsafe.Clean(r.Fix)
	r.Iface, r.Network = textsafe.Clean(r.Iface), textsafe.Clean(r.Network)
	if r.Portal != nil {
		portal := *r.Portal
		portal.RedirectURL = textsafe.Clean(portal.RedirectURL)
		r.Portal = &portal
	}
	for i, a := range r.Attempts {
		if a.Err != nil {
			// Replaced, not wrapped: nothing downstream unwraps an attempt
			// error, it only ever gets printed.
			r.Attempts[i].Err = errors.New(textsafe.Clean(a.Err.Error()))
		}
	}
	return r
}

// buildProbes assembles the DAG. publicDNSIP names the second-opinion
// resolver; "" leaves the row out entirely rather than emitting a skipped one.
func (o *netops) buildProbes(t *Target, publicDNSIP string) []Probe {
	iface := Probe{ID: ProbeIface, Name: "Interface", Run: o.ifaceProbe}
	network := Probe{ID: ProbeSSID, Name: "Wi-Fi network", Deps: []ProbeID{ProbeIface}, Run: o.ssidProbe}
	internet := Probe{ID: ProbeInternet, Name: "Internet (TCP egress)", Deps: []ProbeID{ProbeIface}, Run: o.internetProbe}
	quicProbe := Probe{ID: ProbeQUIC, Name: "QUIC / UDP 443", Deps: []ProbeID{ProbeIface}, Run: o.quicProbe(ConnectivityProbeHost, quicProbePort)}
	// Direct and proxied egress are reported separately: the native probes
	// deliberately bypass proxies, so on a proxy-only network the direct row
	// fails while this row proves the environment proxy carries traffic.
	proxy := Probe{ID: ProbeProxy, Name: "Internet (env proxy)", Deps: []ProbeID{ProbeIface}, Run: o.proxyProbe}
	// Encrypted DNS is its own branch off the interface, beside the plaintext
	// rows rather than under them: a network can carry ordinary DNS while
	// blocking DoH/DoT, which is what makes "DNS works but the browser cannot
	// resolve" diagnosable at all.
	encryptedDNS := Probe{ID: ProbeDNSEncrypted, Name: "DNS (encrypted DoH/DoT)", Deps: []ProbeID{ProbeIface}, Run: o.encryptedDNSProbe(defaultEncryptedDNS, ConnectivityProbeHost)}

	if t == nil {
		// Egress, proxy egress, system DNS, and public DNS are siblings: each
		// depends only on the interface, so one failure never hides another.
		dns := Probe{ID: ProbeDNS, Name: "DNS", Deps: []ProbeID{ProbeIface}, Run: o.dnsProbe(ConnectivityProbeHost, nil)}
		probes := []Probe{iface, internet, quicProbe, proxy, dns}
		if publicDNSIP != "" {
			probes = append(probes, Probe{ID: ProbeDNSPublic, Name: "DNS (public " + publicDNSIP + ")", Deps: []ProbeID{ProbeIface}, Run: o.publicDNSProbe(ConnectivityProbeHost, nil, publicDNSIP)})
		}
		probes = append(probes, encryptedDNS)
		return append(probes, network)
	}

	host, port := t.Host, t.Port
	hp := net.JoinHostPort(host, strconv.Itoa(port)) // brackets IPv6 literals
	dns := Probe{ID: ProbeDNS, Name: "DNS " + host, Deps: []ProbeID{ProbeIface}, Run: o.dnsProbe(host, t.IP)}
	ttcp := Probe{ID: ProbeTargetTCP, Name: "TCP " + hp, Deps: []ProbeID{ProbeDNS}, Run: o.targetTCPProbe(port)}
	// Path MTU hangs off the TCP connect rather than off any protocol row: a
	// black hole breaks SSH and SMTP exactly as thoroughly as it breaks TLS.
	pmtu := Probe{ID: ProbePMTU, Name: "Path MTU " + hp, Deps: []ProbeID{ProbeTargetTCP}, Run: o.pmtuProbe(port, t.Proto)}
	probes := []Probe{iface, internet, quicProbe, proxy, dns}
	if publicDNSIP != "" {
		probes = append(probes, Probe{ID: ProbeDNSPublic, Name: "DNS (public " + publicDNSIP + ")", Deps: []ProbeID{ProbeIface}, Run: o.publicDNSProbe(host, t.IP, publicDNSIP)})
	}
	probes = append(probes, encryptedDNS, ttcp, pmtu, network)

	switch t.Proto {
	case ProtoTLSHTTP:
		probes = append(probes,
			Probe{ID: ProbeTLS, Name: "TLS " + host, Deps: []ProbeID{ProbeTargetTCP}, Run: o.tlsProbe(host, port)},
			Probe{ID: ProbeHTTP, Name: "HTTP " + host, Deps: []ProbeID{ProbeDNS}, Run: o.httpProbe(host, 80, "http", ProbeDNS)},
			Probe{ID: ProbeHTTPS, Name: "HTTPS " + host, Deps: []ProbeID{ProbeTLS}, Run: o.httpProbe(host, port, "https", ProbeTLS)},
		)
	case ProtoHTTP:
		probes = append(probes,
			Probe{ID: ProbeHTTP, Name: "HTTP " + host, Deps: []ProbeID{ProbeTargetTCP}, Run: o.httpProbe(host, port, "http", ProbeTargetTCP)},
		)
	case ProtoSSH:
		probes = append(probes, o.bannerProbe(ProbeSSH, "SSH banner "+hp, port))
	case ProtoSMTP:
		probes = append(probes, o.bannerProbe(ProbeSMTP, "SMTP banner "+hp, port))
	}
	return probes
}

// ---- probe implementations ----

func (o *netops) ifaceProbe(_ context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
	var r ProbeResult
	ifaces, err := o.interfaces()
	if err != nil {
		r.Status = StatusFail
		r.Detail, r.Fix = "cannot list interfaces: "+err.Error(), "check permissions / network stack"
		return r
	}
	if o.sources != nil {
		var matches []net.Interface
		for i := range ifaces {
			addrs, err := o.interfaceAddrs(&ifaces[i])
			if err != nil {
				continue
			}
			if containsSources(addrs, o.sources) {
				matches = append(matches, ifaces[i])
			}
		}
		primary := o.sources.primary()
		if len(matches) == 0 {
			r.Status, r.Detail = StatusFail, "selected source address is no longer assigned"
			r.Fix = "choose an active interface with --iface"
			return r
		}
		if len(matches) > 1 {
			r.Status, r.Source, r.Iface = StatusWarn, primary, "(ambiguous)"
			r.Detail = "selected source address is assigned to multiple interfaces"
			return r
		}
		if matches[0].Flags&net.FlagUp == 0 || matches[0].Flags&net.FlagRunning == 0 {
			r.Status, r.Source, r.Iface = StatusFail, primary, matches[0].Name
			r.Detail, r.Fix = "interface "+matches[0].Name+" is down", ifaceFix(runtime.GOOS)
			return r
		}
		r.Status, r.Source, r.Iface = StatusPass, primary, matches[0].Name
		r.Detail = "using " + matches[0].Name + " source " + primary.String()
		if o.sources.IPv4 != nil && o.sources.IPv6 != nil {
			r.Detail = "using " + matches[0].Name + " sources " + o.sources.IPv4.String() + ", " + o.sources.IPv6.String()
		}
		return r
	}
	// First up-and-running non-loopback interface wins, which is kernel
	// enumeration order, not the routing table's opinion. With Wi-Fi and
	// Ethernet both up this may name the one traffic doesn't use; that's fine,
	// this probe only proves "a link is alive". The egress probes report the
	// interface packets actually take (pathIdentity).
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagRunning != 0 {
			r.Status, r.Iface, r.Detail = StatusPass, ifi.Name, "interface "+ifi.Name+" is up"
			return r
		}
	}
	r.Status = StatusFail
	r.Detail, r.Fix = "no interface up", ifaceFix(runtime.GOOS)
	return r
}

func (o *netops) ssidProbe(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
	network := o.ssid(ctx, deps[ProbeIface].Iface)
	if network == "" {
		return ProbeResult{Status: StatusNA, Detail: "Wi-Fi network unavailable"}
	}
	return ProbeResult{Status: StatusPass, Network: network, Detail: "connected to " + network}
}

func (o *netops) internetProbe(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
	var r ProbeResult
	type famResult struct {
		ips      []net.IP
		conn     net.Conn
		sel      net.IP
		attempts []Attempt
		rtt      time.Duration
	}
	// Each family is probed independently and in parallel: a black-holing
	// family only spends its own share of the probe deadline, and IPv4 and
	// IPv6 egress are diagnosed separately.
	var v4, v6 famResult
	// --iface binds every dial to that interface's address of the destination's
	// family, so a family it has no address for cannot be dialed at all. Drop it
	// here rather than learn it from a bind error: an impossible attempt proves
	// nothing about the network, and calling it unreachable reports an outage in
	// a family nobody tested.
	v4.ips, v6.ips = o.compatibleSourceIPs(internetEndpoints4), o.compatibleSourceIPs(internetEndpoints6)
	if len(v4.ips) == 0 && len(v6.ips) == 0 {
		return ProbeResult{Status: StatusNA, Detail: "the selected interface has no address family available for direct egress"}
	}
	// portalCode stays 0 when the check is stubbed out or never answered; only
	// a real status code is evidence either way.
	var portalCode int
	var portalURL string
	// portalSkew is this machine's clock minus the responder's Date, sampled
	// in the goroutine so it carries the HTTP round trip and nothing else.
	var portalSkew time.Duration
	var wg sync.WaitGroup
	wg.Go(func() {
		v4.conn, v4.sel, v4.attempts, v4.rtt = o.dialIPs(ctx, v4.ips, 443)
	})
	wg.Go(func() {
		v6.conn, v6.sel, v6.attempts, v6.rtt = o.dialIPs(ctx, v6.ips, 443)
	})
	if o.portalCheck != nil {
		// Runs alongside the dials rather than after them: it costs nothing
		// when egress is clean, and its answer is only consulted on success.
		wg.Go(func() {
			if code, redirect, date, err := o.portalCheck(ctx); err == nil {
				portalCode, portalURL = code, redirect
				if !date.IsZero() {
					portalSkew = time.Since(date)
				}
			}
		})
	}
	wg.Wait()
	// Only the 204 leaves usable clock evidence: any other status is an
	// interception this check can see, and an interceptor's clock speaks for
	// the interceptor. A 204 is not proof of an unmodified path, since this
	// is plain HTTP and a transparent proxy could synthesize both the status
	// and the Date. It is the same heuristic the portal verdict already rests
	// on, and no stronger.
	if portalCode == http.StatusNoContent {
		r.clockOffset = portalSkew
	}
	r.Families = &FamilyConnectivity{IPv4: familyState(v4.ips, v4.conn), IPv6: familyState(v6.ips, v6.conn)}

	// IPv4 headlines the result unless it lost and IPv6 won. Not a value
	// judgment, just a stable order for the Detail string and warnings.
	prim, sec, primName, secName := v4, v6, "IPv4", "IPv6"
	if v4.conn == nil && v6.conn != nil {
		prim, sec, primName, secName = v6, v4, "IPv6", "IPv4"
	}
	if prim.conn == nil {
		r.Attempts = append(v4.attempts, v6.attempts...)
		all := append(append([]net.IP{}, v4.ips...), v6.ips...)
		r.Detail = "no direct TCP egress to " + joinIPs(all) + " (port 443)"
		src, iface := o.pathIdentity(ctx, nil, all[0], 443)
		r.Status, r.Source, r.Iface = StatusFail, src, iface
		// Portals commonly drop 443 outright while still intercepting plain
		// HTTP. The 204 endpoint answering anything else is proof of a portal
		// even with no handshake to show for it, so report that, not "check
		// upstream".
		if portalCode != 0 && portalCode != http.StatusNoContent {
			r.Portal = &Portal{RedirectURL: portalURL}
			r.Detail += fmt.Sprintf(", and HTTP is intercepted: %s answered %d, want 204", portalProbeURL, portalCode)
			r.Fix = "captive portal or transparent filter: open a browser and sign in to the network"
			return r
		}
		r.Fix = "no internet egress: proxy-only/filtered network? check upstream"
		if o.routeCause != nil {
			r.Cause = o.routeCause(all[0])
		}
		return r
	}
	defer prim.conn.Close()
	if sec.conn != nil {
		_ = sec.conn.Close()
	}
	src, iface := o.pathIdentity(ctx, prim.conn, prim.sel, 443)
	// A completed handshake only proves that something answered. A captive
	// portal or transparent filter terminates the connection itself and is
	// indistinguishable from real egress at this layer; the 204 endpoint is
	// what tells them apart, so ask before calling the network online.
	if portalCode != 0 && portalCode != http.StatusNoContent {
		r.Status, r.SelectedIP, r.Source, r.Iface = StatusFail, prim.sel, src, iface
		r.Attempts = append(prim.attempts, sec.attempts...)
		r.Portal = &Portal{RedirectURL: portalURL}
		r.Detail = fmt.Sprintf("TCP reaches %s but HTTP is intercepted: %s answered %d, want 204", prim.sel, portalProbeURL, portalCode)
		r.Fix = "captive portal or transparent filter: open a browser and sign in to the network"
		return r
	}
	r.Status, r.SelectedIP, r.Source, r.Iface = StatusPass, prim.sel, src, iface
	r.Detail = fmt.Sprintf("%s egress via %s in %dms (src %s %s)", primName, prim.sel, Ms(prim.rtt), src, iface)
	switch {
	case sec.conn != nil:
		r.Detail += fmt.Sprintf("; %s egress via %s in %dms", secName, sec.sel, Ms(sec.rtt))
	case len(sec.ips) == 0:
		r.Detail += "; " + secName + " not tested (the selected interface has no " + secName + " address)"
	default:
		r.Detail += "; no " + secName + " egress"
	}
	// Warnings judge only the winning family: a network without the other
	// family at all is normal, not degraded. The other family's attempts are
	// appended afterwards so the details panel still shows them.
	r.Attempts = prim.attempts
	var extra []string
	// The exception: a machine that took a global IPv6 address and still can't
	// reach IPv6 is broken, not v4-only. Happy Eyeballs hides that from netdoc
	// and from browsers, but not from software that dials AAAA and waits.
	if sec.conn == nil && secName == "IPv6" && o.hasGlobalUnicast(false) {
		extra = append(extra, "IPv6 address configured but no IPv6 egress (black-holed)")
		r.Cause = FamilyCauseIPv6Unreachable
		r.Fix = "check the IPv6 default route, gateway, and forwarding path"
	}
	if sec.conn == nil && secName == "IPv4" && o.hasGlobalUnicast(true) {
		extra = append(extra, "IPv4 address configured but no IPv4 egress (black-holed)")
		r.Cause = FamilyCauseIPv4Unreachable
		r.Fix = "check the IPv4 default route, gateway, and forwarding path"
	}
	applyDialWarnings(&r, prim.rtt, extra...)
	r.Attempts = append(prim.attempts, sec.attempts...)
	return r
}

// proxyFromEnvironment is http.ProxyFromEnvironment plus ALL_PROXY, which Go
// ignores but curl, ssh and the SOCKS ecosystem honor. A box whose only proxy
// setting is ALL_PROXY=socks5h://... is proxied, and the report has to say so.
func proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	u, err := http.ProxyFromEnvironment(req)
	if u != nil || err != nil {
		return u, err
	}
	// net/http already applied NO_PROXY to HTTP(S)_PROXY, so a nil here can
	// equally mean "exempted", and falling back to ALL_PROXY on that would report
	// a proxy nothing would use for this host.
	if noProxyBypasses(req.URL.Hostname()) {
		return nil, nil
	}
	all := os.Getenv("ALL_PROXY")
	if all == "" {
		all = os.Getenv("all_proxy")
	}
	if all == "" {
		return nil, nil
	}
	// Same tolerance net/http grants HTTP_PROXY: a bare host:port means http.
	if u, err := url.Parse(all); err == nil && u.Scheme != "" && u.Host != "" {
		return u, nil
	}
	return url.Parse("http://" + all)
}

// noProxyBypasses reports whether NO_PROXY exempts host from proxying, the
// check net/http applies to HTTP(S)_PROXY and this file has to apply itself to
// the ALL_PROXY fallback.
// ponytail: suffix and "*" matching only, no IP or CIDR entries; the only host
// asked about is ConnectivityProbeHost, a fixed public name that is never a literal IP.
// proxyProbe is the sole caller and hardcodes it even when the user names a
// target, an invariant TestProxyProbeOnlyAsksAboutProbeHost pins. The day a
// caller passes a host the user chose, that test fails: drop this for
// golang.org/x/net/http/httpproxy, the matcher net/http itself uses.
func noProxyBypasses(host string) bool {
	np := os.Getenv("NO_PROXY")
	if np == "" {
		np = os.Getenv("no_proxy")
	}
	host = strings.ToLower(host)
	for _, entry := range strings.Split(np, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "*" {
			return true
		}
		entry = strings.TrimPrefix(entry, ".")
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// proxyProbe checks egress through the environment-configured proxy: dial the
// proxy and ask it to tunnel to ConnectivityProbeHost:443, by HTTP CONNECT or a SOCKS5
// handshake. This is exactly what proxied HTTPS clients do, minus the TLS
// handshake inside the tunnel.
func (o *netops) proxyProbe(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
	var r ProbeResult
	var proxyURL *url.URL
	var err error
	// ProxyFromEnvironment answers per request scheme (HTTPS_PROXY vs
	// HTTP_PROXY), so ask for both; https first since that's what almost all
	// tunneled traffic is.
	for _, scheme := range []string{"https", "http"} {
		proxyURL, err = o.proxyFromEnv(&http.Request{URL: &url.URL{Scheme: scheme, Host: ConnectivityProbeHost}})
		if err != nil || proxyURL != nil {
			break
		}
	}
	if err != nil {
		r.Status = StatusFail
		r.Cause = ProxyCauseProtocol
		r.Detail = "bad proxy configuration: HTTPS_PROXY/HTTP_PROXY/ALL_PROXY is not a valid proxy URL"
		r.Fix = "fix the HTTPS_PROXY/HTTP_PROXY/ALL_PROXY value"
		return r
	}
	if proxyURL == nil {
		r.Status = StatusNA
		r.Detail = "no proxy in environment (HTTPS_PROXY/HTTP_PROXY/ALL_PROXY unset)"
		return r
	}
	if proxyURL.Hostname() == "" || proxyURL.Path != "" || proxyURL.RawQuery != "" || proxyURL.ForceQuery || proxyURL.Fragment != "" {
		r.Status = StatusFail
		r.Cause = ProxyCauseProtocol
		r.Detail = "bad proxy configuration: proxy URL must have a valid host and no path, query, or fragment"
		r.Fix = "fix the HTTPS_PROXY/HTTP_PROXY/ALL_PROXY value"
		return r
	}
	socks := proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h"
	if !socks && proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		r.Status = StatusNA
		r.Detail = "proxy scheme " + proxyURL.Scheme + " is not supported by this probe"
		return r
	}
	if port := proxyURL.Port(); port != "" {
		if _, err := parsePort(port); err != nil {
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "bad proxy configuration: " + err.Error()
			r.Fix = "fix the HTTPS_PROXY/HTTP_PROXY/ALL_PROXY value"
			return r
		}
	}
	addr := proxyURL.Host
	if proxyURL.Port() == "" {
		port := "80"
		switch proxyURL.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		}
		addr = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	start := time.Now()
	var conn net.Conn
	// A ctx without a deadline yields the zero time, which *clears* the conn
	// deadlines rather than setting them, so the CONNECT read would then block
	// forever. Fall back to the probe budget.
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Now().Add(DefaultProbeTimeout)
	}
	if socks {
		return o.socks5Probe(ctx, addr, proxyURL.Scheme == "socks5h", dl, start)
	}
	var resp *http.Response
	auth := false
	for {
		if proxyURL.Scheme == "https" {
			conn, err = o.dialTLS(ctx, "tcp", addr, &tls.Config{ServerName: proxyURL.Hostname()})
		} else {
			conn, err = o.dialContext(ctx, "tcp", addr)
		}
		if err != nil {
			r.Status = StatusFail
			r.Cause = ProxyCauseUnreachable
			r.Detail = "cannot reach proxy " + addr + ": " + err.Error()
			r.Fix = "proxy configured but unreachable: check HTTPS_PROXY/HTTP_PROXY/ALL_PROXY and the proxy host"
			return r
		}
		req := "CONNECT " + ConnectivityProbeHost + ":443 HTTP/1.1\r\nHost: " + ConnectivityProbeHost + ":443\r\n"
		if auth {
			pw, _ := proxyURL.User.Password()
			req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+pw)) + "\r\n"
		}
		if err := conn.SetWriteDeadline(dl); err != nil {
			_ = conn.Close()
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "cannot set proxy write deadline: " + err.Error()
			return r
		}
		if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
			_ = conn.Close()
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "proxy write failed: " + err.Error()
			return r
		}
		// net.Conn reads don't know ctx exists; the read deadline is the only leash.
		if err := conn.SetReadDeadline(dl); err != nil {
			_ = conn.Close()
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "cannot set proxy read deadline: " + err.Error()
			return r
		}
		// Bounded read: the response is attacker-controlled.
		resp, err = http.ReadResponse(bufio.NewReader(io.LimitReader(conn, 4096)), &http.Request{Method: http.MethodConnect})
		if err != nil {
			_ = conn.Close()
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "no CONNECT response from proxy " + addr + ": " + err.Error()
			r.Fix = "proxy reachable but not speaking HTTP: wrong port or scheme?"
			return r
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusProxyAuthRequired || proxyURL.User == nil || auth {
			break
		}
		_ = conn.Close()
		if proxyURL.Scheme == "http" {
			r.Status = StatusFail
			r.Cause = ProxyCauseProtocol
			r.Detail = "proxy " + addr + " requires authentication; refusing to send credentials unencrypted"
			r.Fix = "use an https:// proxy before supplying credentials"
			return r
		}
		auth = true
	}
	defer conn.Close()
	rtt := since(start)
	if resp.StatusCode/100 != 2 {
		r.Status = StatusFail
		r.Cause = ProxyCauseProtocol
		r.Detail = "proxy " + addr + " refused CONNECT: " + resp.Status
		if resp.StatusCode == http.StatusProxyAuthRequired {
			r.Fix = "proxy requires credentials: set user:pass@host in the proxy URL"
		} else {
			r.Fix = "proxy reachable but refusing tunnels: check proxy policy"
		}
		return r
	}
	return o.proxyTunnelOK(ctx, conn, addr, rtt)
}

func dnsFailureCause(err error) string {
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return ""
	}
	if dnsErr.IsTimeout || timeoutError(err) {
		return DNSCauseTimeout
	}
	// The pure-Go resolver reports a received SERVFAIL as a DNSError whose
	// IsTemporary bit varies across supported Go versions. Once timeout and
	// NXDOMAIN are excluded, the remaining resolver failures are retryable
	// transport/server failures and belong to the temporary class.
	if dnsErr.IsTemporary || !dnsErr.IsNotFound {
		return DNSCauseTemporaryFailure
	}
	return ""
}

// proxyTunnelOK builds the PASS result shared by the CONNECT and SOCKS5 paths.
func (o *netops) proxyTunnelOK(ctx context.Context, conn net.Conn, addr string, rtt time.Duration) ProbeResult {
	var r ProbeResult
	src, iface := o.pathIdentity(ctx, conn, nil, 0)
	r.Status, r.Source, r.Iface = StatusPass, src, iface
	r.Detail = fmt.Sprintf("proxy %s tunnels to %s:443 in %dms", addr, ConnectivityProbeHost, Ms(rtt))
	applyDialWarnings(&r, rtt)
	return r
}

// socks5Probe dials a SOCKS5 proxy and asks it to tunnel to ConnectivityProbeHost:443.
// socks5 resolves the destination with the client's configured resolver and
// sends an address request; socks5h sends the hostname so the proxy resolves
// it. The distinction is observable on split-DNS networks and is why both
// schemes exist.
func (o *netops) socks5Probe(ctx context.Context, addr string, remoteDNS bool, dl, start time.Time) ProbeResult {
	var r ProbeResult
	conn, err := o.dialContext(ctx, "tcp", addr)
	if err != nil {
		r.Status = StatusFail
		r.Cause = ProxyCauseUnreachable
		r.Detail = "cannot reach proxy " + addr + ": " + err.Error()
		r.Fix = "proxy configured but unreachable: check HTTPS_PROXY/HTTP_PROXY/ALL_PROXY and the proxy host"
		return r
	}
	defer conn.Close()
	// net.Conn reads don't know ctx exists; the deadline is the only leash.
	if err := conn.SetDeadline(dl); err != nil {
		r.Status = StatusFail
		r.Cause = ProxyCauseProtocol
		r.Detail = "cannot set proxy deadline: " + err.Error()
		return r
	}
	if err := socks5Greeting(conn); err != nil {
		r.Status = StatusFail
		r.Cause = ProxyCauseProtocol
		r.Detail = "SOCKS5 proxy " + addr + ": " + err.Error()
		r.Fix = "check that the proxy URL names a SOCKS5 port and that the proxy allows this destination"
		return r
	}
	destination := socks5Destination{host: ConnectivityProbeHost, port: 443, remoteDNS: remoteDNS}
	if !remoteDNS {
		ips, server, lookupErr := o.lookupIP(ctx, ConnectivityProbeHost)
		if lookupErr != nil || len(ips) == 0 {
			r.Status = StatusFail
			r.Cause = ProxyCauseClientDNS
			via := ""
			if server != "" {
				via = " via " + dnsServerLabel(server)
			}
			r.Detail = "SOCKS5 proxy " + addr + " is reachable, but local DNS cannot resolve " + ConnectivityProbeHost + via
			if lookupErr != nil {
				r.Detail += ": " + lookupErr.Error()
			}
			r.Fix = "fix the client's DNS resolver, or use socks5h:// to resolve names through the proxy"
			return r
		}
		destination.ip = ips[0]
	}
	if err := socks5Request(conn, destination); err != nil {
		r.Status = StatusFail
		r.Cause = proxyCauseForSOCKSError(err, remoteDNS)
		r.Detail = "SOCKS5 proxy " + addr + ": " + err.Error()
		r.Fix = "check that the proxy URL names a SOCKS5 port and that the proxy allows this destination"
		return r
	}
	return o.proxyTunnelOK(ctx, conn, addr, since(start))
}

type socks5Destination struct {
	host      string
	ip        net.IP
	port      int
	remoteDNS bool
}

// socks5Greeting runs the RFC 1928 no-auth negotiation. Every read is a fixed,
// small size because replies are attacker-controlled.
func socks5Greeting(conn net.Conn) error {
	// VER 5, offering exactly one method: 0 (no authentication).
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return fmt.Errorf("greeting failed: %w", err)
	}
	hello := make([]byte, 2)
	if _, err := io.ReadFull(conn, hello); err != nil {
		return fmt.Errorf("no SOCKS5 greeting reply: %w", err)
	}
	if hello[0] != 5 {
		return fmt.Errorf("not a SOCKS5 proxy (reply version %d): wrong port or scheme?", hello[0])
	}
	if hello[1] != 0 {
		return errors.New("requires authentication; SOCKS5 credentials travel in cleartext, so this probe does not send them")
	}
	return nil
}

func socks5Request(conn net.Conn, destination socks5Destination) error {
	// CONNECT, reserved, followed by the destination address and port.
	req := []byte{5, 1, 0}
	if destination.remoteDNS {
		if len(destination.host) == 0 || len(destination.host) > 255 {
			return errors.New("destination hostname is too long for SOCKS5")
		}
		// #nosec G115 -- the preceding bound proves the length fits one SOCKS byte.
		req = append(req, 3, byte(len(destination.host)))
		req = append(req, destination.host...)
	} else if ip4 := destination.ip.To4(); ip4 != nil {
		req = append(req, 1)
		req = append(req, ip4...)
	} else if ip6 := destination.ip.To16(); ip6 != nil {
		req = append(req, 4)
		req = append(req, ip6...)
	} else {
		return errors.New("local DNS returned an invalid address")
	}
	// #nosec G115 -- the only caller supplies the fixed HTTPS port 443.
	req = binary.BigEndian.AppendUint16(req, uint16(destination.port))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("CONNECT failed: %w", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("no CONNECT reply: %w", err)
	}
	if reply[0] != 5 || reply[2] != 0 {
		return fmt.Errorf("invalid CONNECT reply header (version %d, reserved %d)", reply[0], reply[2])
	}
	if reply[1] != 0 {
		return socks5ReplyError{code: reply[1]}
	}
	// Drain the bound address so the conn sits at the tunnel's first byte.
	var n int
	switch reply[3] {
	case 1:
		n = 4
	case 4:
		n = 16
	case 3:
		var b [1]byte
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			return fmt.Errorf("truncated CONNECT reply: %w", err)
		}
		n = int(b[0])
	default:
		return fmt.Errorf("bad address type %d in CONNECT reply", reply[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, n+2)); err != nil {
		return fmt.Errorf("truncated CONNECT reply: %w", err)
	}
	return nil
}

type socks5ReplyError struct{ code byte }

func (e socks5ReplyError) Error() string { return "refused CONNECT: " + socks5Error(e.code) }

func proxyCauseForSOCKSError(err error, remoteDNS bool) string {
	var reply socks5ReplyError
	if errors.As(err, &reply) {
		switch reply.code {
		case 3, 5:
			return ProxyCauseDestinationUnreachable
		case 4:
			// RFC 1928 names code 4 "host unreachable"; it can only
			// represent proxy-side name resolution in this probe when the
			// request actually carried a domain name. A locally resolving
			// socks5 request sent an address and must not be labelled DNS.
			if remoteDNS {
				return ProxyCauseProxyDNS
			}
			return ProxyCauseDestinationUnreachable
		}
	}
	return ProxyCauseProtocol
}

// socks5Error names an RFC 1928 reply code. Codes 6-7 can't come back from a
// CONNECT, so they fall through to the number; 8 can, because this probe always
// asks for ATYP 3.
func socks5Error(code byte) string {
	msgs := [...]string{1: "general failure", 2: "not allowed by ruleset", 3: "network unreachable", 4: "host unreachable", 5: "connection refused", 8: "address type not supported"}
	if int(code) < len(msgs) && msgs[code] != "" {
		return msgs[code]
	}
	return "reply code " + strconv.Itoa(int(code))
}

// dnsAnswer is one query's outcome, carried back from the goroutine that asked.
type dnsAnswer struct {
	ips    []net.IP
	server string
	err    error
}

// retryableDNS reports whether a failure is worth a second query. A timeout or
// a temporary server failure says something about the resolver's health at that
// instant; NXDOMAIN, like an answer, says something about the name, and asking
// twice cannot change either.
func retryableDNS(err error) bool {
	switch dnsFailureCause(err) {
	case DNSCauseTimeout, DNSCauseTemporaryFailure:
		return true
	}
	return false
}

// lookupIPRetrying resolves host and samples the resolver a second time when
// the first query neither answers nor fails conclusively. The second query goes
// out as soon as the first fails (SERVFAIL, or a stub that gives up early),
// and otherwise halfway through the probe budget, alongside a first query that
// is still waiting.
//
// Alongside, not instead of: cutting the first query short to make room would
// halve the patience of every DNS probe, and a resolver that answers late but
// within the budget would be reported as a timeout it never had. Two queries in
// flight cost one extra packet and settle both cases: the resolver that
// recovers mid-probe is heard, and the slow one keeps its own answer.
func (o *netops) lookupIPRetrying(ctx context.Context, host string) ([]net.IP, string, error) {
	// Buffered for both queries: the loser of the race writes after the winner
	// has been returned, and must not block until the context releases it.
	answers := make(chan dnsAnswer, 2)
	ask := func() {
		ips, server, err := o.lookupIP(ctx, host)
		answers <- dnsAnswer{ips, server, err}
	}
	go ask()
	budget := DefaultProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	resample := time.NewTimer(budget / 2)
	defer resample.Stop()
	outstanding, spare := 1, true
	send := func() {
		if spare {
			spare, outstanding = false, outstanding+1
			go ask()
		}
	}
	var last dnsAnswer
	for outstanding > 0 {
		select {
		case <-resample.C:
			send()
		case answer := <-answers:
			outstanding--
			if !retryableDNS(answer.err) {
				return answer.ips, answer.server, answer.err
			}
			last = answer
			send()
		}
	}
	return last.ips, last.server, last.err
}

func (o *netops) dnsProbe(host string, litIP net.IP) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		if litIP != nil {
			r.Status, r.Addrs, r.SelectedIP = StatusNA, []net.IP{litIP}, litIP
			r.Detail = "literal IP " + litIP.String() + ", no DNS needed"
			return r
		}
		ips, server, err := o.lookupIPRetrying(ctx, host)
		// "which server told me this" is the first question on a split-DNS or
		// router-vs-Pi-hole setup, and often the whole answer.
		via, paren := "", ""
		if server != "" {
			via = " via " + dnsServerLabel(server)
			paren = " (via " + dnsServerLabel(server) + ")"
		}
		if err != nil {
			r.Status = StatusFail
			r.Cause = dnsFailureCause(err)
			r.DNSNotFound = dnsNotFound(err)
			r.Detail = "cannot resolve " + host + via + ": " + err.Error()
			r.Fix = dnsFix(runtime.GOOS)
			return r
		}
		if len(ips) == 0 {
			r.Status = StatusFail
			r.DNSNotFound = true
			r.Detail, r.Fix = "no A/AAAA records for "+host+via, "no address returned: check the hostname / DNS"
			return r
		}
		r.Status, r.Addrs = StatusPass, ips
		r.Detail = host + " → " + joinIPs(ips) + paren
		return r
	}
}

func (o *netops) publicDNSProbe(host string, litIP net.IP, publicDNSIP string) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
		if litIP != nil {
			return ProbeResult{Status: StatusNA, Detail: "literal IP " + litIP.String() + ", no DNS needed"}
		}
		ips, err := o.lookupPublicIP(ctx, host, publicDNSServer(publicDNSIP))
		if dnsNotFound(err) || err == nil && len(ips) == 0 {
			return ProbeResult{
				Status:      StatusPass,
				DNSNotFound: true,
				resolver:    publicDNSIP,
				Detail:      publicDNSIP + " reports no A/AAAA records for " + host,
			}
		}
		if err != nil {
			return ProbeResult{Status: StatusNA, resolver: publicDNSIP, Detail: "public DNS unavailable via " + publicDNSIP + ": " + err.Error()}
		}
		return ProbeResult{
			Status:   StatusPass,
			Addrs:    ips,
			resolver: publicDNSIP,
			Detail:   host + " → " + joinIPs(ips) + " (via " + publicDNSIP + ")",
		}
	}
}

func dnsNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func (o *netops) targetTCPProbe(port int) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		addrs := deps[ProbeDNS].Addrs
		if len(addrs) == 0 {
			r.Status, r.Detail = StatusFail, "no resolved addresses"
			return r
		}
		conn, sel, attempts, rtt := o.dialIPs(ctx, addrs, port)
		r.Attempts = attempts
		if conn != nil {
			defer conn.Close()
			src, iface := o.pathIdentity(ctx, conn, sel, port)
			r.Status, r.SelectedIP, r.Source, r.Iface = StatusPass, sel, src, iface
			r.Detail = fmt.Sprintf("connected to %s:%d in %dms (src %s %s)", sel, port, Ms(rtt), src, iface)
			// Warnings judge only the winning family, exactly as the egress row
			// does: dialIPs races both at once, so a family this network simply
			// doesn't carry arrives as a pile of failed siblings, and a dual-stack
			// name on an IPv4-only link would otherwise read as degraded forever.
			// A family that is configured and still unreachable is the egress
			// row's story to tell. The losers are appended back afterwards so the
			// details panel still lists every address that was tried.
			same, other := splitAttemptFamilies(attempts, sel)
			r.Attempts = same
			applyDialWarnings(&r, rtt)
			r.Attempts = append(same, other...)
			return r
		}
		// All addresses failed: deterministic fallback path = first address.
		src, iface := o.pathIdentity(ctx, nil, addrs[0], port)
		r.Status, r.Source, r.Iface = StatusFail, src, iface
		tried := make([]net.IP, len(attempts))
		for i, a := range attempts {
			tried[i] = a.IP
		}
		r.Detail = fmt.Sprintf("port %d unreachable on all %d address(es): %s", port, len(attempts), joinIPs(tried))
		r.Fix = fmt.Sprintf("port %d blocked/refused: firewall, wrong network, or VPN routing?", port)
		return r
	}
}

func (o *netops) tlsProbe(host string, port int) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		ip := deps[ProbeTargetTCP].SelectedIP
		if ip == nil {
			r.Status, r.Detail = StatusSkip, "no pinned IP from Target TCP"
			return r
		}
		conn, err := o.dialTLS(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)), &tls.Config{ServerName: host})
		if err != nil {
			// Name the address: the cert that failed belongs to whatever the
			// resolver handed us, and that's often the actual culprit.
			r.Status, r.SelectedIP = StatusFail, ip
			r.Cause = tlsFailureCause(err, time.Now())
			r.timedOut = timeoutError(err)
			r.Detail = "TLS handshake to " + ip.String() + " failed: " + err.Error()
			r.Fix = tlsFix(err)
			if iface := deps[ProbeTargetTCP].Iface; timeoutError(err) {
				if mtu := o.mtuFor(iface); mtu > 0 {
					r.Detail += fmt.Sprintf(" (%s MTU is %d)", iface, mtu)
				}
			}
			return r
		}
		_ = conn.Close()
		r.Status, r.SelectedIP, r.Detail = StatusPass, ip, "TLS handshake OK (SNI "+host+")"
		return r
	}
}

func tlsFailureCause(err error, now time.Time) string {
	var (
		hostErr x509.HostnameError
		invalid x509.CertificateInvalidError
		unknown x509.UnknownAuthorityError
	)
	switch {
	case errors.As(err, &hostErr):
		return TLSCauseHostnameMismatch
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		if invalid.Cert != nil && now.Before(invalid.Cert.NotBefore) {
			return TLSCauseCertificateNotYet
		}
		return TLSCauseCertificateExpired
	case errors.As(err, &unknown):
		return TLSCauseUntrustedIssuer
	case timeoutError(err):
		return TLSCauseTimeout
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return TLSCauseConnectionClosed
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return TLSCauseTCPUnreachable
	default:
		return TLSCauseHandshake
	}
}

func (o *netops) httpProbe(host string, port int, scheme string, addressDep ProbeID) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		protocol := strings.ToUpper(scheme)
		var addrs []net.IP
		if addressDep == ProbeDNS {
			addrs = deps[addressDep].Addrs
		} else if ip := deps[addressDep].SelectedIP; ip != nil {
			addrs = []net.IP{ip}
		}
		if len(addrs) == 0 {
			r.Status, r.Detail = StatusSkip, "no address available for "+protocol
			return r
		}
		// Fresh, non-reusing transport restricted to the resolved/pinned IPs;
		// redirects and proxy off; bounded response headers (attacker-controlled).
		// The transport dials on its own goroutine, which can outlive client.Do
		// on ctx timeout, so the closure must not write to r directly.
		var dialMu sync.Mutex
		var dialIP net.IP
		var dialAttempts []Attempt
		tr := &http.Transport{
			Proxy:             nil,
			ForceAttemptHTTP2: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				conn, selected, attempts, _ := o.dialIPs(ctx, addrs, port)
				dialMu.Lock()
				dialIP, dialAttempts = selected, attempts
				dialMu.Unlock()
				if conn == nil {
					if len(attempts) > 0 && attempts[len(attempts)-1].Err != nil {
						return nil, attempts[len(attempts)-1].Err
					}
					return nil, fmt.Errorf("all %s addresses failed", protocol)
				}
				return conn, nil
			},
			TLSClientConfig:        &tls.Config{ServerName: host, RootCAs: o.tlsRootCAs},
			MaxResponseHeaderBytes: 64 << 10,
			DisableKeepAlives:      true,
		}
		client := &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		url := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			r.Status, r.Detail = StatusFail, "cannot build request: "+err.Error()
			return r
		}
		resp, err := client.Do(req)
		dialMu.Lock()
		r.SelectedIP, r.Attempts = dialIP, dialAttempts
		dialMu.Unlock()
		if err != nil {
			r.Status = StatusFail
			r.timedOut = timeoutError(err)
			// Name the winner if one address connected and the failure came
			// later, otherwise everything tried.
			tried := joinIPs(addrs)
			if r.SelectedIP != nil {
				tried = r.SelectedIP.String()
			}
			r.Detail = "no " + protocol + " response from " + tried + ": " + err.Error()
			r.Fix = protocol + " blocked: proxy or firewall?"
			return r
		}
		_ = resp.Body.Close()
		r.Status = StatusPass
		r.Detail = fmt.Sprintf("%s %d (responded)", protocol, resp.StatusCode)
		return r
	}
}

// pmtuProbe looks for evidence of a path-MTU black hole with no root, raw
// sockets, or DF flag, by reading the one asymmetry a normal socket exposes.
//
// The TCP handshake is the small-packet control: SYN/SYN-ACK are small enough to
// cross a narrowed link, so a completed connect already proves small packets
// arrive. The evidence is what happens to a write that requires multiple
// ordinary TCP segments: the probe pushes a payload through and then asks the
// kernel how much of it the peer has acknowledged. Acknowledged bytes are the
// only proof of forward progress an ordinary socket can offer, because a
// full-size segment that is acknowledged is a full-size segment that crossed.
//
// A completed Write proves nothing. Linux treats SO_SNDBUF as an accounting
// hint, not a ceiling, and will absorb a 24 KiB write into a socket reporting
// an 8 KiB send buffer without a byte reaching the wire, which is exactly what
// a path-MTU black hole looks like from userspace. Only socketQueued separates
// the two, so the send buffer survives here as detail and as the fallback for
// platforms that cannot account for their send queue.
//
// There is deliberately no small-write control: a small Write returns out of the
// send buffer whether or not the bytes ever leave the machine, so it is not
// evidence of anything.
//
// Never a Fail, by design. A peer that accepts the connection and then stops
// reading stalls us the same way, so the Warn states its evidence (bytes
// written, send buffer size, and that the handshake got through) and leaves the
// reader room to judge. Only an independent protocol timeout promotes this
// evidence into a network-path verdict.
func (o *netops) pmtuProbe(port int, proto Proto) func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
	return func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		dep := deps[ProbeTargetTCP]
		if dep.SelectedIP == nil {
			r.Status, r.Detail = StatusSkip, "no pinned IP from Target TCP"
			return r
		}
		// The stall is the measurement, so it needs a deadline of its own inside
		// the probe budget, since a ctx cancellation would report no bytes at all.
		wait := pmtuWriteWait
		if dl, ok := ctx.Deadline(); ok {
			if left := time.Until(dl) - pmtuHeadroom; left < wait {
				wait = left
			}
		}
		if wait <= 0 {
			r.Status, r.Detail = StatusNA, "not enough of the probe budget left to measure a stall"
			return r
		}
		ip := dep.SelectedIP
		conn, err := o.dialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		if err != nil {
			// TCP connected moments ago, so a second refusal is flakiness on the
			// path, not a verdict about it.
			r.Status, r.SelectedIP = StatusNA, ip
			r.Detail = "second connection to " + ip.String() + " failed: " + err.Error()
			return r
		}
		defer conn.Close()
		r.SelectedIP = ip
		// A requested SO_SNDBUF is only a hint. Linux doubles it, and other
		// kernels may impose a much larger minimum. Read the effective value
		// back rather than treating a locally queued Write as remote delivery.
		if tc, ok := conn.(*net.TCPConn); ok {
			if err := tc.SetWriteBuffer(pmtuSendBuffer); err != nil {
				r.Status = StatusNA
				r.Detail = "cannot bound the TCP send buffer: " + err.Error()
				return r
			}
		}
		measureBuffer := o.sendBuffer
		if measureBuffer == nil {
			measureBuffer = socketSendBuffer
		}
		measureQueued := o.queued
		if measureQueued == nil {
			measureQueued = socketQueued
		}
		// One reading before the payload decides which inference the rest of the
		// probe gets to make. A platform that can account for its send queue
		// does not care how big the send buffer is; one that cannot has nothing
		// but the send buffer, and then the buffer has to be smaller than the
		// payload for a stalled write to mean anything.
		_, queueErr := measureQueued(conn)

		sendBuffer, err := measureBuffer(conn)
		if queueErr != nil {
			if err != nil || sendBuffer <= 0 {
				r.Status = StatusNA
				r.Detail = "cannot read the effective TCP send buffer; a completed write would only prove local buffering"
				return r
			}
			if sendBuffer >= pmtuPayloadSize {
				r.Status = StatusNA
				r.Detail = fmt.Sprintf("effective TCP send buffer is %s, large enough to hold the whole probe locally", kib(sendBuffer))
				return r
			}
		}
		mss := 0
		measureMSS := o.tcpMSS
		if measureMSS == nil {
			measureMSS = socketMSS
		}
		if measured, measureErr := measureMSS(conn); measureErr == nil && measured > 0 {
			mss = measured
		}
		deadline := time.Now().Add(wait)
		if err := conn.SetWriteDeadline(deadline); err != nil {
			r.Status = StatusNA
			r.Detail = "cannot bound the bulk write: " + err.Error()
			return r
		}
		n, err := conn.Write(pmtuPayload(proto))

		mtu := o.mtuFor(dep.Iface)
		blackHole := "; the TCP handshake succeeded" + mtuNote(dep.Iface, mtu, ", and %s advertises a %d-byte MTU") +
			", consistent with a path-MTU black hole"
		delivered, queueErr := awaitAcknowledged(measureQueued, conn, n, deadline)
		switch {
		case queueErr != nil:
			// No send-queue accounting here, so fall back to the send buffer:
			// a write that advanced past it had to have drained some of it.
			// This over-reports Pass on any kernel that accepts more than the
			// buffer it reports, which is why it is the fallback and not the
			// measurement.
			const blind = " (this platform cannot read the TCP send queue, so delivery is inferred from the send buffer and a black hole can still read as a pass)"
			switch {
			case err == nil:
				r.Status = StatusPass
				r.Detail = fmt.Sprintf("%s drained past the measured %s TCP send buffer%s", kib(n), kib(sendBuffer), mssNote(mss)) + blind
			case n > sendBuffer:
				r.Status = StatusPass
				r.Detail = fmt.Sprintf("%s drained past the measured %s TCP send buffer%s before the write stopped (%v)", kib(n), kib(sendBuffer), mssNote(mss), err) + blind
			case timeoutError(err):
				r.Status = StatusWarn
				r.Detail = fmt.Sprintf("stalled after %s of %s without draining the measured %s TCP send buffer%s; the TCP handshake succeeded%s, consistent with a path-MTU black hole",
					kib(n), kib(pmtuPayloadSize), kib(sendBuffer), mssNote(mss), mtuNote(dep.Iface, mtu, ", and %s advertises a %d-byte MTU"))
				r.Fix = pmtuFix(runtime.GOOS)
			default:
				r.Status = StatusNA
				r.Detail = fmt.Sprintf("inconclusive; the peer dropped the connection after %s: %v", kib(n), err)
			}
		case err != nil && !timeoutError(err):
			// Ahead of the acknowledgement check on purpose: a reset purges the
			// send queue, so a dropped connection reads as a fully drained one.
			r.Status = StatusNA
			r.Detail = fmt.Sprintf("inconclusive; the peer dropped the connection after %s: %v", kib(n), err)
		case delivered > 0:
			// Acknowledgement is cumulative and TCP fills segments from the front
			// of the payload, so the peer cannot have acknowledged any of a
			// 24 KiB write without its leading full-size segment crossing. A
			// small tail that arrives out of order is only SACKed, which does
			// not move this counter.
			r.Status = StatusPass
			r.Detail = fmt.Sprintf("%s of the %s payload acknowledged by the peer%s", kib(delivered), kib(pmtuPayloadSize), mssNote(mss))
		default:
			r.Status = StatusWarn
			r.Detail = fmt.Sprintf("%s written, none of it acknowledged within %v%s%s", kib(n), wait, mssNote(mss), blackHole)
			r.Fix = pmtuFix(runtime.GOOS)
		}
		return r
	}
}

// pmtuQueueSample paces the drain watch. It is a polling interval, not a
// threshold: the answer does not depend on it, only how soon a healthy path is
// let off the deadline.
const pmtuQueueSample = 50 * time.Millisecond

// awaitAcknowledged reports how much of the payload the peer has acknowledged,
// watching the local send queue until some of it drains or the deadline passes.
//
// Sampling rather than reading once is what keeps the answer honest on a path
// with real latency: Write returns the moment the kernel accepts the bytes, so
// immediately afterwards nothing is acknowledged yet even on a healthy link.
// Once Write has returned nothing more enters the queue, so every later sample
// is monotone and the first sign of progress settles it. A black hole never
// produces one: its segments are all too big to cross, so nothing is ever
// acknowledged and the loop runs out the deadline it was already going to
// spend.
func awaitAcknowledged(measure func(net.Conn) (int, error), conn net.Conn, written int, deadline time.Time) (int, error) {
	for {
		queued, err := measure(conn)
		if err != nil {
			return 0, err
		}
		if delivered := written - queued; delivered > 0 || !time.Now().Before(deadline) {
			return delivered, nil
		}
		time.Sleep(pmtuQueueSample)
	}
}

func mssNote(mss int) string {
	if mss <= 0 {
		return ""
	}
	return fmt.Sprintf(" at a %d-byte TCP MSS", mss)
}

// pmtuPayload is the byte pattern the PMTU probe pushes at the target: legible
// to whoever finds it in a packet capture or a server log, and worthless to
// anything that parses it.
func pmtuPayload(proto Proto) []byte {
	filler := []byte("netdoc path-mtu probe, discard me. ")
	out := make([]byte, 0, pmtuPayloadSize)
	if proto == ProtoTLSHTTP {
		out = append(out, tlsRecordHeader...)
	}
	for len(out) < pmtuPayloadSize {
		out = append(out, filler[:min(len(filler), pmtuPayloadSize-len(out))]...)
	}
	return out
}

// kib renders a byte count the way the numbers in this probe are chosen: in
// whole KiB, rounded down, since a partial KiB never changes the reading.
func kib(n int) string {
	return strconv.Itoa(n>>10) + " KiB"
}

// mtuNote fills format with iface and mtu, or returns nothing when the MTU
// couldn't be read, since the verdict doesn't depend on knowing the number.
func mtuNote(iface string, mtu int, format string) string {
	if mtu <= 0 {
		return ""
	}
	return fmt.Sprintf(format, iface, mtu)
}

// mtuFor reports the MTU of the named interface, or 0 when there isn't one to
// read.
func (o *netops) mtuFor(name string) int {
	if name == "" || o.interfaces == nil {
		return 0
	}
	ifaces, _ := o.interfaces()
	for _, ifi := range ifaces {
		if ifi.Name == name && ifi.MTU > 0 {
			return ifi.MTU
		}
	}
	return 0
}

func (o *netops) bannerProbe(id ProbeID, label string, port int) Probe {
	return Probe{ID: id, Name: label, Deps: []ProbeID{ProbeTargetTCP}, Run: func(ctx context.Context, deps map[ProbeID]ProbeResult) ProbeResult {
		var r ProbeResult
		ip := deps[ProbeTargetTCP].SelectedIP
		if ip == nil {
			r.Status, r.Detail = StatusSkip, "no pinned IP from Target TCP"
			return r
		}
		conn, err := o.dialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		if err != nil {
			r.Status, r.SelectedIP = StatusFail, ip
			r.Detail = "connect to " + ip.String() + " failed: " + err.Error()
			return r
		}
		defer conn.Close()
		// A banner arrives immediately or (shy server) never. Keep the short
		// read leash, capped by the remaining probe budget because net.Conn
		// reads don't honor ctx directly.
		deadline := time.Now().Add(2 * time.Second)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			r.Status, r.SelectedIP = StatusFail, ip
			r.Detail = "cannot set banner read deadline: " + err.Error()
			return r
		}
		// Strict byte limit: a hostile server streaming without a newline can't
		// exhaust memory.
		br := bufio.NewReader(io.LimitReader(conn, 1024))
		line, readErr := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		r.SelectedIP = ip
		if line == "" && errors.Is(readErr, syscall.ECONNRESET) {
			r.Status, r.Cause = StatusFail, ConnectionCauseReset
			r.Detail = "peer accepted the connection and reset it before sending a banner"
		} else if line == "" {
			// Port answered but the service said nothing: functional, degraded.
			r.Status, r.Detail = StatusWarn, "connected, no banner within deadline"
		} else if valid := id == ProbeSSH && strings.HasPrefix(line, "SSH-") ||
			id == ProbeSMTP && (strings.HasPrefix(line, "220 ") || strings.HasPrefix(line, "220-")); !valid {
			r.Status, r.Detail = StatusFail, "unexpected service banner: "+line
		} else {
			r.Status, r.Detail = StatusPass, "banner: "+line
		}
		return r
	}}
}

// ---- shared helpers ----

// applyDialWarnings downgrades a successful dial result to Warn when it is
// degraded: high connect latency, sibling addresses that failed before one
// won, or an ambiguous source interface. Notes are appended to Detail.
func applyDialWarnings(r *ProbeResult, rtt time.Duration, extra ...string) {
	notes := extra
	if rtt >= warnRTT {
		notes = append(notes, fmt.Sprintf("high latency (%dms)", rtt.Milliseconds()))
	}
	// dialIPs records completed attempts plus the winner (last), so every
	// earlier attempt genuinely failed before the win. Callers hand over only
	// the winning family's attempts (see targetTCPProbe).
	if n := len(r.Attempts) - 1; n > 0 {
		notes = append(notes, fmt.Sprintf("%d of %d address(es) failed", n, len(r.Attempts)))
	}
	if r.Iface == "(ambiguous)" {
		notes = append(notes, "ambiguous source interface")
	}
	if len(notes) > 0 {
		r.Status = StatusWarn
		r.Detail += "; warning: " + strings.Join(notes, ", ")
	}
}

// compatibleSourceIPs drops destinations whose address family the selected
// interface has no source address for. Probes that dial a fixed endpoint use it
// so --iface never produces a cross-family dial, and so an interface that
// simply lacks a family reports N/A rather than a failure it could not avoid.
// Without a selection every family is usable and the list is returned as-is.
func (o *netops) compatibleSourceIPs(ips []net.IP) []net.IP {
	if o.sources == nil {
		return ips
	}
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip.To4() != nil && o.sources.IPv4 != nil || ip.To4() == nil && o.sources.IPv6 != nil {
			out = append(out, ip)
		}
	}
	return out
}

// familyState reports what the direct-egress dial proved about one address
// family. No eligible endpoint means the selected source has no address of that
// family, so nothing was dialed and there is nothing to report: the state stays
// empty rather than claiming an outage in a family that was never tested.
func familyState(ips []net.IP, conn net.Conn) string {
	switch {
	case len(ips) == 0:
		return ""
	case conn != nil:
		return FamilyReachable
	default:
		return FamilyUnreachable
	}
}

// splitAttemptFamilies partitions attempts into the winner's address family
// and everything else, so a dial result can be judged on the family that
// actually carried it. A nil winner leaves everything in same, since there is no
// family to judge against, and the caller is on its failure path anyway.
func splitAttemptFamilies(attempts []Attempt, sel net.IP) (same, other []Attempt) {
	if sel == nil {
		return attempts, nil
	}
	selV4 := sel.To4() != nil
	for _, a := range attempts {
		if (a.IP.To4() != nil) == selV4 {
			same = append(same, a)
		} else {
			other = append(other, a)
		}
	}
	return same, other
}

// interleaveFamilies orders addresses IPv6-first, alternating families
// (RFC 8305 §4), so one broken family can't monopolize the attempt sequence.
func interleaveFamilies(ips []net.IP) []net.IP {
	v4, v6 := splitFamilies(ips)
	if len(v6) == 0 || len(v4) == 0 {
		return ips
	}
	out := make([]net.IP, 0, len(ips))
	for i := 0; i < len(v6) || i < len(v4); i++ {
		if i < len(v6) {
			out = append(out, v6[i])
		}
		if i < len(v4) {
			out = append(out, v4[i])
		}
	}
	return out
}

func splitFamilies(ips []net.IP) (v4, v6 []net.IP) {
	for _, ip := range ips {
		if ip.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	return v4, v6
}

// dialIPs races ip:port connection attempts Happy Eyeballs style (RFC 8305):
// addresses are interleaved by family (IPv6 first), each attempt starts
// attemptDelay after the previous one (sooner once it fails), and the first
// success cancels the rest. Returns the winning conn, the IP that won (pinned
// for protocol probes), the attempts that completed before the win, and the
// winning RTT. A cancelled/expired ctx dials nothing.
func (o *netops) dialIPs(ctx context.Context, ips []net.IP, port int) (net.Conn, net.IP, []Attempt, time.Duration) {
	ips = interleaveFamilies(ips)
	if len(ips) > maxAttempts {
		ips = ips[:maxAttempts]
	}
	if len(ips) == 0 {
		return nil, nil, nil, 0
	}
	dctx, cancel := context.WithCancel(ctx)
	defer cancel() // unblocks pending winner hand-offs so losers close their conns

	type result struct {
		conn net.Conn
		att  Attempt
	}
	winner := make(chan result)           // unbuffered: hand off or close, never leak
	fails := make(chan Attempt, len(ips)) // buffered: a failure never blocks its goroutine
	next := make(chan struct{}, len(ips)) // a failure fast-forwards the stagger

	go func() {
		for i, ip := range ips {
			if i > 0 {
				t := time.NewTimer(attemptDelay)
				select {
				case <-t.C:
				case <-next:
				case <-dctx.Done():
					t.Stop()
					return
				}
				t.Stop()
			}
			if dctx.Err() != nil {
				return
			}
			go func(ip net.IP) {
				start := time.Now()
				network := "tcp6"
				if ip.To4() != nil {
					network = "tcp4"
				}
				conn, err := o.dialContext(dctx, network, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
				att := Attempt{IP: ip, Dur: since(start), Err: err}
				if err != nil {
					fails <- att
					next <- struct{}{}
					return
				}
				select {
				case winner <- result{conn, att}:
				case <-dctx.Done():
					_ = conn.Close() // lost the race
				}
			}(ip)
		}
	}()

	// Every address either wins, fails, or never starts; pending bounds the
	// loop either way. On a win we return at once, and the deferred cancel tells
	// stragglers still dialing to give up and close whatever they got.
	var attempts []Attempt
	for pending := len(ips); pending > 0; pending-- {
		select {
		case w := <-winner:
			// Both channels can be ready at once and select picks blind, so
			// siblings that already failed may still be queued. Collect them
			// before leaving, since dropping one turns a documented WARN into a PASS
			// and loses the evidence. Nothing else reads fails, so no receive
			// here can block.
			for len(fails) > 0 {
				attempts = append(attempts, <-fails)
			}
			attempts = append(attempts, w.att) // winner last; applyDialWarnings counts on it
			return w.conn, w.att.IP, attempts, w.att.Dur
		case att := <-fails:
			attempts = append(attempts, att)
		case <-ctx.Done():
			return nil, nil, attempts, 0
		}
	}
	return nil, nil, attempts, 0
}

// pathIdentity returns the source IP + interface for a destination. On a
// successful connect it reads the winning LocalAddr (ground truth); otherwise it
// falls back to a UDP "connect" (sends no packets) for path identity only, not
// a reachability claim.
func (o *netops) pathIdentity(ctx context.Context, conn net.Conn, dstIP net.IP, port int) (net.IP, string) {
	var src net.IP
	if conn != nil {
		if la, ok := conn.LocalAddr().(*net.TCPAddr); ok {
			src = la.IP
		}
	} else if dstIP != nil {
		if c, err := o.dialContext(ctx, "udp", net.JoinHostPort(dstIP.String(), strconv.Itoa(port))); err == nil {
			if la, ok := c.LocalAddr().(*net.UDPAddr); ok {
				src = la.IP
			}
			_ = c.Close()
		}
	}
	if src == nil {
		return nil, ""
	}
	return src, o.ifaceForIP(src)
}

// hasGlobalUnicast reports whether any live non-loopback interface holds a
// global unicast address of the given family. The machine was configured for
// it (statically, by DHCP, or by a router advertisement), so the network
// claimed to carry that family.
//
// Each family excludes the range that is routable on the LAN but never to the
// internet, where no egress is the design rather than a black hole: fc00::/7
// for IPv6, which Docker hands out and which would otherwise warn on every
// v4-only machine running a v6-enabled bridge, and 169.254.0.0/16 for IPv4,
// which is what a host self-assigns when DHCP never answered.
func (o *netops) hasGlobalUnicast(v4 bool) bool {
	if o.sources != nil {
		ip := o.sources.IPv6
		if v4 {
			ip = o.sources.IPv4
		}
		return ip != nil && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() && (v4 || ip[0]&0xfe != 0xfc)
	}
	ifaces, err := o.interfaces()
	if err != nil {
		return false
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := o.interfaceAddrs(&ifi)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || !n.IP.IsGlobalUnicast() {
				continue
			}
			if v4 && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return true
			}
			if !v4 && n.IP.To4() == nil && n.IP[0]&0xfe != 0xfc {
				return true
			}
		}
	}
	return false
}

// ifaceForIP maps a source IP back to an interface name. LocalAddr gives an IP,
// not a name, so ambiguity (same IP on >1 iface) and no-match are explicit
// states, not a guess.
func (o *netops) ifaceForIP(ip net.IP) string {
	ifaces, err := o.interfaces()
	if err != nil {
		return ""
	}
	name, count := "", 0
	for _, ifi := range ifaces {
		addrs, err := o.interfaceAddrs(&ifi)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.Equal(ip) {
				name, count = ifi.Name, count+1
			}
		}
	}
	switch {
	case count == 0:
		return "(unknown iface)"
	case count > 1:
		return "(ambiguous)"
	default:
		return name
	}
}

func joinIPs(ips []net.IP) string {
	parts := make([]string, len(ips))
	for i, ip := range ips {
		parts[i] = ip.String()
	}
	return strings.Join(parts, ", ")
}
