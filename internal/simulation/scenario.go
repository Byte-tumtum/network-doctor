// Package simulation builds a throwaway virtual network, runs netdoc inside
// it, and compares the diagnosis against what the scenario said should break.
//
// Nothing here touches the host's networking. Every interface, route, resolver
// and firewall rule lives inside namespaces the simulator created and owns, and
// they cease to exist when the process tree that holds them dies. See
// netns_linux.go for the mechanism.
package simulation

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"gopkg.in/yaml.v3"
)

// Scenario is one simulation: a topology to build, faults to inject, netdoc
// runs to make, and the diagnosis those runs should produce.
type Scenario struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Topology    Topology `yaml:"topology"`
	Faults      []Fault  `yaml:"faults"`
	Tests       []Test   `yaml:"tests"`
	Expect      Expect   `yaml:"expect"`
}

// Topology describes L2 segments, node interfaces, and routes. Subnet remains
// the backward-compatible shorthand used by the original single-segment
// scenarios; validation normalizes it into Segments, Interfaces, and Routes.
type Topology struct {
	Subnet   string    `yaml:"subnet"`
	Segments []Segment `yaml:"segments"`
	Nodes    []Node    `yaml:"nodes"`
	Routes   []Route   `yaml:"routes"`
}

// Segment is one simulator-owned Linux bridge.
type Segment struct {
	Name   string `yaml:"name"`
	Subnet string `yaml:"subnet"`
}

// Interface attaches a node to one logical segment. Scenario authors never
// provide the kernel interface name; the backend derives a short safe name.
type Interface struct {
	Segment string `yaml:"segment"`
	Address string `yaml:"address"`
}

// Route is a validated unicast route. Destination is either "default" or a
// canonical prefix; free-form iproute expressions are deliberately impossible.
type Route struct {
	Node        string `yaml:"node"`
	Destination string `yaml:"destination"`
	Via         string `yaml:"via"`
	Metric      int    `yaml:"metric"`
	Default     bool   `yaml:"-"`
}

// Node is one network namespace on the segment.
type Node struct {
	Name string `yaml:"name"`
	// Role client marks the namespace netdoc runs in; every other node is
	// scenery. Exactly one client per scenario.
	Role    string `yaml:"role"`
	Address string `yaml:"address"`
	// Interfaces is the explicit multi-segment form. Address is retained only
	// for legacy single-segment scenario compatibility.
	Interfaces []Interface `yaml:"interfaces"`
	// Aliases are extra addresses put on the node's loopback, which is how the
	// simulated internet claims the public IPs netdoc probes (1.1.1.1, 8.8.8.8)
	// without anything leaving the namespace.
	Aliases  []string  `yaml:"aliases"`
	Gateway  string    `yaml:"gateway"`
	Resolver string    `yaml:"resolver"`
	Services []Service `yaml:"services"`
}

// Service is a test server the node runs. Ports are bound inside the node's
// namespace, so two nodes may both serve :53 or :443.
type Service struct {
	// Name is optional for existing scenarios, but required when another
	// scenario object needs to identify the service. Non-empty names are unique
	// across the topology.
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	Port int    `yaml:"port"`
	// Zone maps a name to an address for ServiceDNS. A name that is absent
	// answers NXDOMAIN, which is how "DNS returns NXDOMAIN" is expressed.
	Zone map[string]string `yaml:"zone"`
	// Body and Status shape the ServiceHTTP reply; /generate_204 always answers
	// 204 so netdoc's captive-portal check passes.
	Status int    `yaml:"status"`
	Body   string `yaml:"body"`
	// Certificate describes simulator-generated TLS identity. Scenario files
	// select intent only; they cannot provide keys, PEM, paths, or algorithms.
	Certificate *TLSCertificate `yaml:"certificate"`
}

// TLSCertificate is the narrow certificate intent accepted by a TLS service.
type TLSCertificate struct {
	Mode     string   `yaml:"mode"`
	DNSNames []string `yaml:"dns_names"`
}

// Service types.
const (
	ServiceDNS  = "dns"
	ServiceHTTP = "http"
	// ServiceTCP accepts a connection and closes it — enough for the direct
	// egress probe, which only proves a handshake completes.
	ServiceTCP = "tcp"
	// ServiceSOCKS5 is a simulator-owned, no-auth CONNECT proxy. It supports
	// address and domain destinations; BIND and UDP ASSOCIATE are intentionally
	// outside the simulator's needs.
	ServiceSOCKS5 = "socks5"
	// ServiceTLS generates an in-memory private CA and leaf key, writes only the
	// public CA certificate to the simulator workspace, and serves bounded TLS.
	ServiceTLS = "tls"
)

const (
	TLSCertificateValid            = "valid"
	TLSCertificateExpired          = "expired"
	TLSCertificateHostnameMismatch = "hostname_mismatch"
	tlsMaxDNSNames                 = 16
)

// Fault types.
const (
	// FaultDrop discards matching packets with an nftables rule. Direction
	// decides whether the sender is refused or left waiting; see Fault.
	FaultDrop = "drop"
	// FaultNetem attaches delay/jitter/loss to the node's segment interface.
	FaultNetem = "netem"
	// FaultNoDefaultRoute deletes the node's default route.
	FaultNoDefaultRoute = "no_default_route"
	// FaultReplaceDefaultRoute replaces every default route on a node with one
	// validated on-link next hop.
	FaultReplaceDefaultRoute = "replace_default_route"
	// FaultLinkDown administratively lowers one logical node interface.
	FaultLinkDown = "link_down"
)

// FaultDrop directions.
const (
	DirectionOutbound = "outbound"
	DirectionInbound  = "inbound"
)

// Fault is one impairment applied after the topology is up and healthy.
type Fault struct {
	Type string `yaml:"type"`
	Node string `yaml:"node"`
	// Segment identifies an interface by logical topology name. Via and Metric
	// are used only by replace_default_route.
	Segment string `yaml:"segment"`
	Via     string `yaml:"via"`
	Metric  int    `yaml:"metric"`
	// To, Protocol and Port select the traffic FaultDrop discards. An empty To
	// matches every destination; a zero Port matches every port.
	To       string `yaml:"to"`
	Protocol string `yaml:"protocol"`
	Port     int    `yaml:"port"`
	// Direction chooses where FaultDrop bites, and the two are not
	// interchangeable. Outbound drops the packet on the way out of this node,
	// which the kernel reports to the sender as a refusal — a local firewall.
	// Inbound drops it as it arrives at this node, so the sender hears nothing
	// and waits out its timeout — a black hole in the path. Default outbound.
	Direction string `yaml:"direction"`
	// Delay, Jitter and Loss configure FaultNetem. Delay and Jitter are Go
	// durations; Loss is a percentage such as "10%".
	Delay  string `yaml:"delay"`
	Jitter string `yaml:"jitter"`
	Loss   string `yaml:"loss"`
}

// Test is one netdoc run inside a node. An empty Target runs the generic
// (no-target) checks, exactly as `netdoc` with no argument does.
type Test struct {
	Name          string     `yaml:"name"`
	Type          string     `yaml:"type"`
	Node          string     `yaml:"node"`
	Target        string     `yaml:"target"`
	SourceSegment string     `yaml:"source_segment"`
	Proxy         *TestProxy `yaml:"proxy"`
	Trust         *TestTrust `yaml:"trust"`
	Expect        *Expect    `yaml:"expect"`
}

// TestProxy selects one SOCKS service and the public URL scheme netdoc should
// receive. The address is derived from the validated node; scenarios cannot
// supply a raw proxy URL or environment variable.
type TestProxy struct {
	Scheme  string `yaml:"scheme"`
	Node    string `yaml:"node"`
	Port    int    `yaml:"port"`
	address string
}

// TestTrust selects the public root generated by one validated TLS service.
// The runner turns it into SSL_CERT_FILE; scenarios cannot supply environment
// names, paths, or certificate bytes.
type TestTrust struct {
	Service string `yaml:"service"`
}

// TestNetdoc is the only test type. Named so a scenario can be explicit, and
// so an unknown type is rejected rather than silently treated as this one.
const TestNetdoc = "netdoc"

// Expect is the diagnosis the scenario claims netdoc should reach. Matching is
// on netdoc's stable machine-readable contract — probe ids, the PASS/WARN/FAIL/
// SKIP/N/A vocabulary, and the verdict word — never on the English prose.
type Expect struct {
	Verdict string          `yaml:"verdict"`
	Checks  []ExpectedCheck `yaml:"checks"`
}

// ExpectedCheck names one probe row and the status it should carry.
type ExpectedCheck struct {
	ID     string `yaml:"id"`
	Status string `yaml:"status"`
	Cause  string `yaml:"cause"`
}

// knownProbeIDs is the set a scenario may name. It references the constants so
// a rename breaks the build; a newly added probe has to be listed here before
// a scenario can assert on it.
var knownProbeIDs = []diagnostic.ProbeID{
	diagnostic.ProbeIface, diagnostic.ProbeSSID, diagnostic.ProbeInternet,
	diagnostic.ProbeProxy, diagnostic.ProbeDNS, diagnostic.ProbeDNSPublic,
	diagnostic.ProbeTargetTCP, diagnostic.ProbePMTU, diagnostic.ProbeTLS,
	diagnostic.ProbeHTTP, diagnostic.ProbeHTTPS, diagnostic.ProbeSSH,
	diagnostic.ProbeSMTP,
}

// statuses is netdoc's status vocabulary as it appears in the JSON report.
var statuses = []string{"PASS", "WARN", "FAIL", "SKIP", "N/A"}

var knownCauses = []string{
	diagnostic.ProxyCauseUnreachable,
	diagnostic.ProxyCauseClientDNS,
	diagnostic.ProxyCauseProxyDNS,
	diagnostic.ProxyCauseDestinationUnreachable,
	diagnostic.ProxyCauseProtocol,
	diagnostic.TLSCauseCertificateExpired,
	diagnostic.TLSCauseCertificateNotYet,
	diagnostic.TLSCauseHostnameMismatch,
	diagnostic.TLSCauseUntrustedIssuer,
	diagnostic.TLSCauseHandshake,
	diagnostic.TLSCauseTCPUnreachable,
	diagnostic.TLSCauseTimeout,
	diagnostic.TLSCauseConnectionClosed,
}

// verdicts is netdoc's verdict vocabulary. Incomplete is omitted on purpose —
// a finished run never reports it, so expecting it is always a scenario bug.
var verdicts = []string{
	diagnostic.VerdictOK, diagnostic.VerdictDegraded, diagnostic.VerdictDNS,
	diagnostic.VerdictNetwork, diagnostic.VerdictService,
}

// LoadScenario reads and validates a scenario file.
func LoadScenario(path string) (*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := ParseScenario(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// ParseScenario decodes YAML and validates it. Unknown fields are an error, so
// a typo'd key fails loudly instead of being silently ignored.
func ParseScenario(r io.Reader) (*Scenario, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty scenario")
		}
		return nil, err
	}
	// A second document would be silently dropped, and a scenario file is one
	// scenario.
	var extra Scenario
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("scenario file must contain exactly one document")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate reports the first thing wrong with the scenario. It runs before any
// namespace exists, so a bad scenario costs nothing.
func (s *Scenario) Validate() error {
	if s.Name == "" {
		return errors.New("name is required")
	}
	if len(s.Topology.Nodes) == 0 {
		return errors.New("topology.nodes: at least one node is required")
	}
	seen := make(map[string]bool, len(s.Topology.Nodes))
	nodes := make(map[string]*Node, len(s.Topology.Nodes))
	clients := 0
	for i := range s.Topology.Nodes {
		n := &s.Topology.Nodes[i]
		switch {
		case n.Name == "":
			return fmt.Errorf("topology.nodes[%d].name is required", i)
		case seen[n.Name]:
			return fmt.Errorf("duplicate node name %q", n.Name)
		case !isSafeName(n.Name):
			return fmt.Errorf("node %q: name must be letters, digits or dashes", n.Name)
		}
		seen[n.Name] = true
		nodes[n.Name] = n
		switch n.Role {
		case "client":
			clients++
		case "server", "router":
		case "":
			n.Role = "server"
		default:
			return fmt.Errorf("node %q: unknown role %q (client, server or router)", n.Name, n.Role)
		}
	}
	if clients != 1 {
		return fmt.Errorf("exactly one node must have role client, found %d", clients)
	}
	if err := s.Topology.normalizeAndValidate(nodes); err != nil {
		return err
	}
	serviceNames := make(map[string]bool)
	for i := range s.Topology.Nodes {
		if err := s.Topology.Nodes[i].validateServices(serviceNames); err != nil {
			return err
		}
	}
	for i := range s.Faults {
		if err := s.Faults[i].validate(&s.Topology, seen); err != nil {
			return fmt.Errorf("faults[%d]: %w", i, err)
		}
	}
	if len(s.Tests) == 0 {
		return errors.New("tests: at least one test is required")
	}
	for i := range s.Tests {
		if err := s.Tests[i].validate(nodes); err != nil {
			return fmt.Errorf("tests[%d]: %w", i, err)
		}
	}
	return s.Expect.validate()
}

func (t Topology) subnetOrDefault() string {
	if t.Subnet == "" {
		return "10.77.0.0/24"
	}
	return t.Subnet
}

// parseAddr is the only way an address from a scenario becomes a string this
// package will use. It rejects what ParseAddr alone would let through and
// returns the canonical rendering, so what reaches an `ip` argv or a generated
// resolv.conf is the kernel's own spelling of the address rather than the bytes
// that were in the file.
//
// The zone is the part worth refusing: netip.ParseAddr accepts an essentially
// arbitrary string after "%", and while a zone could only ever reach a command
// as one argv element — nothing here builds a shell string — an unbounded value
// has no business in an interface name or a nameserver line.
func parseAddr(raw string) (netip.Addr, string, error) {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, "", err
	}
	if addr.Zone() != "" {
		return netip.Addr{}, "", fmt.Errorf("%q: scoped addresses (the %%zone suffix) are not supported", raw)
	}
	return addr, addr.String(), nil
}

func (n *Node) validateAddrs(subnet netip.Prefix) error {
	addr, canonical, err := parseAddr(n.Address)
	if err != nil {
		return fmt.Errorf("node %q: address: %w", n.Name, err)
	}
	if !subnet.Contains(addr) {
		return fmt.Errorf("node %q: address %s is outside %s", n.Name, addr, subnet)
	}
	n.Address = canonical
	for i, a := range n.Aliases {
		if _, n.Aliases[i], err = parseAddr(a); err != nil {
			return fmt.Errorf("node %q: alias: %w", n.Name, err)
		}
	}
	for label, v := range map[string]*string{"gateway": &n.Gateway, "resolver": &n.Resolver} {
		if *v == "" {
			continue
		}
		if _, *v, err = parseAddr(*v); err != nil {
			return fmt.Errorf("node %q: %s: %w", n.Name, label, err)
		}
	}
	return nil
}

func (n *Node) validateServices(names map[string]bool) error {
	for i := range n.Services {
		svc := &n.Services[i]
		if svc.Name != "" {
			if !isSafeName(svc.Name) {
				return fmt.Errorf("node %q: service name %q must be letters, digits or dashes", n.Name, svc.Name)
			}
			if names[svc.Name] {
				return fmt.Errorf("duplicate service name %q", svc.Name)
			}
			names[svc.Name] = true
		}
		switch svc.Type {
		case ServiceDNS:
			if svc.Port == 0 {
				svc.Port = 53
			}
			if svc.Certificate != nil || svc.Status != 0 || svc.Body != "" {
				return fmt.Errorf("node %q: dns service has unsupported options", n.Name)
			}
			zoneNames := make(map[string]string, len(svc.Zone))
			for name, ip := range svc.Zone {
				if !isSafeHostname(name) {
					return fmt.Errorf("node %q: zone name %q is not a hostname", n.Name, name)
				}
				key := dnsKey(name)
				if previous, exists := zoneNames[key]; exists {
					return fmt.Errorf("node %q: duplicate DNS record %q conflicts with %q", n.Name, name, previous)
				}
				zoneNames[key] = name
				_, canonical, err := parseAddr(ip)
				if err != nil {
					return fmt.Errorf("node %q: zone %s: %w", n.Name, name, err)
				}
				svc.Zone[name] = canonical
			}
		case ServiceHTTP:
			if svc.Port == 0 {
				svc.Port = 80
			}
			if svc.Status == 0 {
				svc.Status = 200
			}
			if svc.Status < 100 || svc.Status > 599 {
				return fmt.Errorf("node %q: http status %d is out of range", n.Name, svc.Status)
			}
			if svc.Certificate != nil || len(svc.Zone) != 0 {
				return fmt.Errorf("node %q: http service has unsupported options", n.Name)
			}
		case ServiceTCP:
			if svc.Port == 0 {
				return fmt.Errorf("node %q: tcp service needs a port", n.Name)
			}
			if svc.Certificate != nil || len(svc.Zone) != 0 || svc.Status != 0 || svc.Body != "" {
				return fmt.Errorf("node %q: tcp service has unsupported options", n.Name)
			}
		case ServiceSOCKS5:
			if svc.Port == 0 {
				svc.Port = 1080
			}
			if n.Resolver == "" {
				return fmt.Errorf("node %q: socks5 service needs the node resolver", n.Name)
			}
			if len(svc.Zone) != 0 || svc.Status != 0 || svc.Body != "" || svc.Certificate != nil {
				return fmt.Errorf("node %q: socks5 service has unsupported options", n.Name)
			}
		case ServiceTLS:
			if svc.Port == 0 {
				svc.Port = 443
			}
			if svc.Name == "" {
				return fmt.Errorf("node %q: tls service needs a name", n.Name)
			}
			if len(svc.Zone) != 0 || svc.Status != 0 || svc.Body != "" {
				return fmt.Errorf("node %q: tls service has unsupported options", n.Name)
			}
			if err := validateTLSCertificate(svc.Certificate); err != nil {
				return fmt.Errorf("node %q: tls service certificate: %w", n.Name, err)
			}
		default:
			return fmt.Errorf("node %q: unknown service type %q", n.Name, svc.Type)
		}
		if svc.Port < 1 || svc.Port > 65535 {
			return fmt.Errorf("node %q: port %d is out of range", n.Name, svc.Port)
		}
	}
	return nil
}

func (f *Fault) validate(topology *Topology, nodes map[string]bool) error {
	if !nodes[f.Node] {
		return fmt.Errorf("unknown node %q", f.Node)
	}
	node := topology.node(f.Node)
	switch f.Type {
	case FaultDrop:
		if f.Segment != "" || f.Via != "" || f.Metric != 0 {
			return errors.New("drop has unsupported route or segment options")
		}
		switch f.Direction {
		case DirectionOutbound, DirectionInbound:
		case "":
			f.Direction = DirectionOutbound
		default:
			return fmt.Errorf("unknown direction %q (%s or %s)", f.Direction, DirectionOutbound, DirectionInbound)
		}
		if f.To != "" {
			var err error
			if _, f.To, err = parseAddr(f.To); err != nil {
				return fmt.Errorf("to: %w", err)
			}
		}
		switch f.Protocol {
		case "tcp", "udp":
		case "":
			if f.Port != 0 {
				return errors.New("port needs a protocol (tcp or udp)")
			}
		default:
			return fmt.Errorf("unknown protocol %q (tcp or udp)", f.Protocol)
		}
		if f.Port < 0 || f.Port > 65535 {
			return fmt.Errorf("port %d is out of range", f.Port)
		}
	case FaultNetem:
		if f.Via != "" || f.Metric != 0 {
			return errors.New("netem has unsupported route options")
		}
		if f.Segment == "" {
			if len(node.Interfaces) != 1 {
				return errors.New("netem on a multi-interface node requires segment")
			}
			f.Segment = node.Interfaces[0].Segment
		} else if _, ok := node.interfaceOn(f.Segment); !ok {
			return fmt.Errorf("node %q has no interface on segment %q", f.Node, f.Segment)
		}
		if f.Delay == "" && f.Jitter == "" && f.Loss == "" {
			return errors.New("netem needs delay, jitter or loss")
		}
		for label, v := range map[string]string{"delay": f.Delay, "jitter": f.Jitter} {
			if v == "" {
				continue
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			if d < 0 {
				return fmt.Errorf("%s must not be negative", label)
			}
		}
		if f.Jitter != "" && f.Delay == "" {
			return errors.New("jitter needs a delay to vary")
		}
		if f.Loss != "" && !isPercent(f.Loss) {
			return fmt.Errorf("loss: %q is not a percentage such as \"10%%\"", f.Loss)
		}
	case FaultNoDefaultRoute:
		if f.Segment != "" || f.Via != "" || f.Metric != 0 {
			return errors.New("no_default_route has unsupported options")
		}
	case FaultReplaceDefaultRoute:
		if f.Segment != "" {
			return errors.New("replace_default_route does not accept segment; it is derived from via")
		}
		if f.Metric < 0 || f.Metric > maxRouteMetric {
			return fmt.Errorf("metric %d is out of range", f.Metric)
		}
		via, canonical, err := parseAddr(f.Via)
		if err != nil {
			return fmt.Errorf("via: %w", err)
		}
		if _, ok := nodeSegmentForAddress(node, via); !ok {
			return fmt.Errorf("gateway %s is not on a directly connected subnet for node %q", via, f.Node)
		}
		f.Via = canonical
	case FaultLinkDown:
		if f.Via != "" || f.Metric != 0 {
			return errors.New("link_down has unsupported options")
		}
		if _, ok := node.interfaceOn(f.Segment); !ok {
			return fmt.Errorf("node %q has no interface on segment %q", f.Node, f.Segment)
		}
	default:
		return fmt.Errorf("unknown fault type %q", f.Type)
	}
	return nil
}

func (t *Topology) node(name string) *Node {
	for i := range t.Nodes {
		if t.Nodes[i].Name == name {
			return &t.Nodes[i]
		}
	}
	return nil
}

func (t *Test) validate(nodes map[string]*Node) error {
	if t.Type == "" {
		t.Type = TestNetdoc
	}
	if t.Type != TestNetdoc {
		return fmt.Errorf("unknown test type %q", t.Type)
	}
	if nodes[t.Node] == nil {
		return fmt.Errorf("unknown node %q", t.Node)
	}
	if t.SourceSegment != "" {
		if _, ok := nodes[t.Node].interfaceOn(t.SourceSegment); !ok {
			return fmt.Errorf("source_segment %q is not an interface on node %q", t.SourceSegment, t.Node)
		}
	}
	if t.Proxy != nil {
		if err := t.Proxy.validate(nodes); err != nil {
			return fmt.Errorf("proxy: %w", err)
		}
	}
	if t.Trust != nil {
		if err := t.Trust.validate(nodes); err != nil {
			return fmt.Errorf("trust: %w", err)
		}
	}
	if t.Name == "" {
		t.Name = t.Node + " " + t.Target
		if t.Target == "" {
			t.Name = t.Node + " (generic)"
		}
	}
	if t.Expect != nil {
		if err := t.Expect.validate(); err != nil {
			return fmt.Errorf("expect: %w", err)
		}
	}
	if t.Target == "" {
		return nil
	}
	// The same parser the CLI uses, so a scenario can never ask netdoc for a
	// target netdoc would reject.
	_, err := diagnostic.ParseTarget(t.Target)
	return err
}

func validateTLSCertificate(c *TLSCertificate) error {
	if c == nil {
		return errors.New("configuration is required")
	}
	switch c.Mode {
	case TLSCertificateValid, TLSCertificateExpired, TLSCertificateHostnameMismatch:
	default:
		return fmt.Errorf("unknown mode %q", c.Mode)
	}
	if len(c.DNSNames) == 0 {
		return errors.New("dns_names must not be empty")
	}
	if len(c.DNSNames) > tlsMaxDNSNames {
		return fmt.Errorf("dns_names has %d entries, maximum is %d", len(c.DNSNames), tlsMaxDNSNames)
	}
	seen := make(map[string]bool, len(c.DNSNames))
	for i, name := range c.DNSNames {
		if netipAddr, err := netip.ParseAddr(name); err == nil && netipAddr.IsValid() {
			return fmt.Errorf("dns_names[%d] %q is an IP literal", i, name)
		}
		if !isSafeHostname(name) {
			return fmt.Errorf("dns_names[%d] %q is not a hostname", i, name)
		}
		key := dnsKey(name)
		if seen[key] {
			return fmt.Errorf("duplicate DNS name %q", name)
		}
		seen[key] = true
		c.DNSNames[i] = key
	}
	return nil
}

func (t *TestTrust) validate(nodes map[string]*Node) error {
	if t.Service == "" {
		return errors.New("service is required")
	}
	for _, node := range nodes {
		for _, svc := range node.Services {
			if svc.Name == t.Service && svc.Type == ServiceTLS {
				return nil
			}
		}
	}
	return fmt.Errorf("unknown tls service %q", t.Service)
}

func (p *TestProxy) validate(nodes map[string]*Node) error {
	switch p.Scheme {
	case "socks5", "socks5h":
	default:
		return fmt.Errorf("unsupported scheme %q (socks5 or socks5h)", p.Scheme)
	}
	n := nodes[p.Node]
	if n == nil {
		return fmt.Errorf("unknown node %q", p.Node)
	}
	if p.Port == 0 {
		p.Port = 1080
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port %d is out of range", p.Port)
	}
	for _, svc := range n.Services {
		if svc.Type == ServiceSOCKS5 && svc.Port == p.Port {
			p.address = n.Address
			return nil
		}
	}
	return fmt.Errorf("node %q has no socks5 service on port %d", p.Node, p.Port)
}

func (e *Expect) validate() error {
	if e.Verdict != "" && !contains(verdicts, e.Verdict) {
		return fmt.Errorf("expect.verdict: unknown verdict %q (one of %s)", e.Verdict, strings.Join(verdicts, ", "))
	}
	seen := make(map[string]bool, len(e.Checks))
	for i, c := range e.Checks {
		if c.ID == "" {
			return fmt.Errorf("expect.checks[%d].id is required", i)
		}
		known := false
		for _, id := range knownProbeIDs {
			if string(id) == c.ID {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("expect.checks[%d]: unknown probe id %q", i, c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("expect.checks: duplicate probe id %q", c.ID)
		}
		seen[c.ID] = true
		if !contains(statuses, c.Status) {
			return fmt.Errorf("expect.checks[%d]: unknown status %q (one of %s)", i, c.Status, strings.Join(statuses, ", "))
		}
		if c.Cause != "" {
			if c.Status != "FAIL" {
				return fmt.Errorf("expect.checks[%d]: cause requires FAIL status", i)
			}
			if !contains(knownCauses, c.Cause) {
				return fmt.Errorf("expect.checks[%d]: unknown cause %q", i, c.Cause)
			}
		}
	}
	if e.Verdict == "" && len(e.Checks) == 0 {
		return errors.New("expect: a scenario must expect a verdict, some checks, or both")
	}
	return nil
}

// Client returns the node netdoc runs in. Validate guarantees there is one.
func (s *Scenario) Client() *Node {
	for i := range s.Topology.Nodes {
		if s.Topology.Nodes[i].Role == "client" {
			return &s.Topology.Nodes[i]
		}
	}
	return nil
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// isSafeName gates every string that reaches an argv as a namespace or
// interface name. Conservative on purpose: these end up in command arguments.
func isSafeName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !alnum && (r != '-' || i == 0) {
			return false
		}
	}
	return true
}

// isSafeHostname gates zone names, which are compared against queries and
// written into DNS answers.
func isSafeHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || !isASCIIAlnum(label[0]) || !isASCIIAlnum(label[len(label)-1]) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			if !isASCIIAlnum(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isPercent(s string) bool {
	num, ok := strings.CutSuffix(s, "%")
	if !ok || num == "" {
		return false
	}
	dots := 0
	for _, r := range num {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return dots <= 1
}
