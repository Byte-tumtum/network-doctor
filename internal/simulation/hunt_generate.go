package simulation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

const (
	// HuntGeneratorVersion is part of every manifest and seed domain. A future
	// algorithm change must increment it instead of silently changing old cases.
	HuntGeneratorVersion = "v3"
	huntSeedDomain       = "netdoc-sim-hunt-v3"
	HuntMaxFaults        = 3
	HuntMaxCases         = 500
	HuntMaxCaseNumber    = 999999
)

// Sorted: validHuntBase binary-searches this list.
var huntBaseNames = []string{"dual-stack-healthy", "healthy", "healthy-routed-network",
	"socks5h-remote-dns-succeeds", "tls-valid", "two-path-healthy", "two-path-ipv6-healthy"}

// HuntBaseNames returns the deliberately small set of known-good controls the
// first generator is allowed to mutate.
func HuntBaseNames() []string { return append([]string(nil), huntBaseNames...) }

func validHuntBase(name string) bool {
	i := sort.SearchStrings(huntBaseNames, name)
	return i < len(huntBaseNames) && huntBaseNames[i] == name
}

// GeneratedMutation is a completely materialized semantic operation. It has
// no command strings, kernel interface names, paths, or deferred randomness.
type GeneratedMutation struct {
	ID               string  `json:"id"`
	Description      string  `json:"description"`
	Node             string  `json:"node,omitempty"`
	TargetNode       string  `json:"target_node,omitempty"`
	Segment          string  `json:"segment,omitempty"`
	Service          string  `json:"service,omitempty"`
	Family           string  `json:"family,omitempty"`
	PreferredVia     string  `json:"preferred_via,omitempty"`
	PreferredSegment string  `json:"preferred_segment,omitempty"`
	PreferredMetric  int     `json:"preferred_metric,omitempty"`
	AlternateVia     string  `json:"alternate_via,omitempty"`
	AlternateSegment string  `json:"alternate_segment,omitempty"`
	AlternateMetric  int     `json:"alternate_metric,omitempty"`
	ControlTarget    string  `json:"control_target,omitempty"`
	LossPercent      float64 `json:"loss_percent,omitempty"`
	LatencyMS        int64   `json:"latency_ms,omitempty"`
	JitterMS         int64   `json:"jitter_ms,omitempty"`
	StartMS          int64   `json:"start_ms,omitempty"`
	DurationMS       int64   `json:"duration_ms,omitempty"`
	NetemSeed        uint32  `json:"netem_seed,omitempty"`
	TargetPort       int     `json:"target_port,omitempty"`
	Status           int     `json:"status,omitempty"`
}

// GeneratedCaseManifest is the stable, display-safe reproduction artifact.
type GeneratedCaseManifest struct {
	GeneratorVersion string              `json:"generator_version"`
	BaseScenario     string              `json:"base_scenario"`
	HuntSeed         int64               `json:"hunt_seed"`
	Case             int                 `json:"case"`
	CaseSeed         int64               `json:"case_seed"`
	Mutations        []GeneratedMutation `json:"mutations"`
	CaseFingerprint  string              `json:"case_fingerprint"`
}

// GeneratedCase couples a public manifest to the validated scenario that will
// execute. Scenario is intentionally omitted from JSON: reports contain the
// normal simulation artifact, while the manifest is the reproduction contract.
type GeneratedCase struct {
	Manifest GeneratedCaseManifest `json:"manifest"`
	Scenario *Scenario             `json:"-"`
}

type mutationOperator struct {
	id           string
	description  string
	conflictTags []string
	applicable   func(*Scenario) bool
	generate     func(*mathrand.Rand, *Scenario) (GeneratedMutation, error)
}

// huntMutationRegistry is ordered API. Selection may shuffle indices, never a
// map, and generated manifests are sorted back into this stable ID vocabulary.
var huntMutationRegistry = []mutationOperator{
	{id: "netem.loss", description: "bounded packet loss", conflictTags: []string{"link-shaping"}, applicable: hasDataPath, generate: generateLoss},
	{id: "netem.latency", description: "bounded path latency", conflictTags: []string{"link-shaping"}, applicable: hasDataPath, generate: generateLatency},
	{id: "netem.jitter", description: "bounded path jitter", conflictTags: []string{"link-shaping"}, applicable: hasDataPath, generate: generateJitter},
	{id: "timeline.netem_spike", description: "temporary latency and loss spike", conflictTags: []string{"link-shaping", "resolver-state", "timeline"}, applicable: hasTimedDataPath, generate: generateNetemSpike},
	{id: "dns.servfail", description: "resolver returns SERVFAIL", conflictTags: []string{"resolver-state"}, applicable: hasNamedResolver, generate: generateDNSSERVFAIL},
	{id: "dns.drop", description: "resolver drops responses", conflictTags: []string{"resolver-state"}, applicable: hasNamedResolver, generate: generateDNSDrop},
	{id: "timeline.dns_outage", description: "temporary resolver outage", conflictTags: []string{"resolver-state", "timeline"}, applicable: hasNamedResolver, generate: generateDNSOutage},
	{id: "service.tcp_reset", description: "target accepts then resets TCP", conflictTags: []string{"target-service"}, applicable: hasHTTPTestTarget, generate: generateTCPReset},
	{id: "service.tls_expired", description: "target presents an expired TLS certificate", conflictTags: []string{"target-service"}, applicable: hasValidTLSTestTarget, generate: generateTLSExpired},
	{id: "proxy.connect_refused", description: "proxy refuses its CONNECT destination", conflictTags: []string{"proxy-state"}, applicable: hasSOCKS5TestProxy, generate: generateProxyCONNECTRefused},
	{id: "quic.udp_443_block", description: "QUIC UDP/443 is blocked while TCP/443 remains reachable", conflictTags: []string{"quic-state"}, applicable: hasWorkingQUICFixture, generate: generateQUICUDP443Block},
	{id: "encrypted_dns.doh_invalid", description: "DoH returns a protocol-invalid DNS message", conflictTags: []string{"encrypted-dns-state"}, applicable: hasWorkingDoHFixture, generate: generateInvalidDoHResponse},
	{id: "http.status_503", description: "HTTP target returns status 503", conflictTags: []string{"target-service"}, applicable: hasSuccessfulHTTPTestTarget, generate: generateHTTP503},
	{id: "family.ipv4_drop", description: "IPv4 path fails while IPv6 remains configured", conflictTags: []string{"family-path"}, applicable: hasDualStackPath, generate: generateIPv4Drop},
	{id: "family.ipv6_drop", description: "IPv6 path fails while IPv4 remains configured", conflictTags: []string{"family-path"}, applicable: hasDualStackPath, generate: generateIPv6Drop},
	{id: "link.transient_down", description: "temporary non-client link loss", conflictTags: []string{"path-outage", "resolver-state", "timeline"}, applicable: hasNonClientDataLink, generate: generateTransientLink},
	{id: "routing.preferred_path_failure", description: "preferred path fails while an alternate remains", conflictTags: []string{"path-outage", "route-choice"}, applicable: hasPreferredPathFailureCandidate, generate: generatePreferredPathFailure},
}

// DeriveHuntCaseSeed makes case N independent of every earlier PRNG stream.
func DeriveHuntCaseSeed(seed int64, base string, caseNumber int) int64 {
	h := sha256.New()
	h.Write([]byte(huntSeedDomain))
	h.Write([]byte{0})
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(seed))
	h.Write(raw[:])
	h.Write([]byte{0})
	h.Write([]byte(base))
	h.Write([]byte{0})
	binary.BigEndian.PutUint64(raw[:], uint64(caseNumber))
	h.Write(raw[:])
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]))
}

// GenerateHuntCase materializes and validates one immutable case. No random
// value is drawn after this function returns.
func GenerateHuntCase(baseID string, base *Scenario, huntSeed int64, caseNumber, maxFaults int) (*GeneratedCase, error) {
	if !validHuntBase(baseID) {
		return nil, fmt.Errorf("unsupported hunt base %q (have: %s)", baseID, strings.Join(huntBaseNames, ", "))
	}
	if base == nil {
		return nil, errors.New("hunt base scenario is nil")
	}
	if caseNumber < 0 || caseNumber > HuntMaxCaseNumber {
		return nil, fmt.Errorf("case must be between 0 and %d", HuntMaxCaseNumber)
	}
	if maxFaults < 1 || maxFaults > HuntMaxFaults {
		return nil, fmt.Errorf("max faults must be between 1 and %d", HuntMaxFaults)
	}
	validatedBase := cloneScenario(base)
	canonicalScenarioInput(validatedBase)
	if err := validatedBase.Validate(); err != nil {
		return nil, fmt.Errorf("base scenario: %w", err)
	}
	caseSeed := DeriveHuntCaseSeed(huntSeed, baseID, caseNumber)
	rng := mathrand.New(mathrand.NewSource(caseSeed))
	var applicable []mutationOperator
	for _, op := range huntMutationRegistry {
		if op.applicable(validatedBase) {
			applicable = append(applicable, op)
		}
	}
	if len(applicable) == 0 {
		return nil, fmt.Errorf("base scenario %q has no applicable hunt mutations", baseID)
	}
	want := 1 + rng.Intn(min(maxFaults, len(applicable)))
	perm := rng.Perm(len(applicable))
	selected := make([]mutationOperator, 0, want)
	for _, index := range perm {
		candidate := applicable[index]
		if operatorsCompatible(selected, candidate) {
			selected = append(selected, candidate)
			if len(selected) == want {
				break
			}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no compatible hunt mutation could be selected")
	}
	mutations := make([]GeneratedMutation, 0, len(selected))
	for _, op := range selected {
		mutation, err := op.generate(rng, validatedBase)
		if err != nil {
			return nil, fmt.Errorf("generate %s: %w", op.id, err)
		}
		mutation.ID = op.id
		if mutation.Description == "" {
			mutation.Description = op.description
		}
		mutations = append(mutations, mutation)
	}
	sort.Slice(mutations, func(i, j int) bool { return mutations[i].ID < mutations[j].ID })
	generated := cloneScenario(validatedBase)
	generated.Campaign = nil
	for _, mutation := range mutations {
		if err := applyGeneratedMutation(generated, mutation); err != nil {
			return nil, fmt.Errorf("apply %s: %w", mutation.ID, err)
		}
	}
	generated.Name = fmt.Sprintf("hunt-%s-%d", baseID, caseNumber)
	generated.Description = fmt.Sprintf("Generated by netdoc-sim hunt %s from %s case %d.", HuntGeneratorVersion, baseID, caseNumber)
	canonicalScenarioInput(generated)
	if err := generated.Validate(); err != nil {
		return nil, fmt.Errorf("generated scenario validation: %w", err)
	}
	manifest := GeneratedCaseManifest{GeneratorVersion: HuntGeneratorVersion, BaseScenario: baseID,
		HuntSeed: huntSeed, Case: caseNumber, CaseSeed: caseSeed, Mutations: mutations}
	manifest.CaseFingerprint = huntCaseFingerprint(manifest)
	return &GeneratedCase{Manifest: manifest, Scenario: generated}, nil
}

// Validate normalizes compatibility shorthands into explicit fields. A loaded
// scenario therefore carries both views in memory and cannot be passed to
// Validate a second time verbatim. This removes only those derived aliases,
// then ordinary validation reconstructs them. The source base is never touched.
func canonicalScenarioInput(s *Scenario) {
	if len(s.Topology.Segments) == 0 {
		return
	}
	s.Topology.Subnet = ""
	for i := range s.Topology.Segments {
		if s.Topology.Segments[i].IPv4 != "" {
			s.Topology.Segments[i].Subnet = ""
		}
	}
	for ni := range s.Topology.Nodes {
		node := &s.Topology.Nodes[ni]
		if len(node.Interfaces) == 0 {
			continue
		}
		node.Address, node.Gateway = "", ""
		for ii := range node.Interfaces {
			if node.Interfaces[ii].IPv4 != "" {
				node.Interfaces[ii].Address = ""
			}
		}
	}
}

func operatorsCompatible(selected []mutationOperator, candidate mutationOperator) bool {
	for _, existing := range selected {
		for _, a := range existing.conflictTags {
			for _, b := range candidate.conflictTags {
				if a == b {
					return false
				}
			}
		}
	}
	return true
}

func huntCaseFingerprint(manifest GeneratedCaseManifest) string {
	semantic := struct {
		Version   string              `json:"version"`
		Base      string              `json:"base"`
		Mutations []GeneratedMutation `json:"mutations"`
	}{manifest.GeneratorVersion, manifest.BaseScenario, manifest.Mutations}
	blob, _ := json.Marshal(semantic)
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func int64n(rng *mathrand.Rand, minValue, maxValue int64) int64 {
	if minValue == maxValue {
		return minValue
	}
	return minValue + rng.Int63n(maxValue-minValue+1)
}

func nonzeroUint32(rng *mathrand.Rand) uint32 {
	v := rng.Uint32()
	if v == 0 {
		return 1
	}
	return v
}

func dataPathTarget(s *Scenario) (string, string, bool) {
	for _, node := range s.Topology.Nodes {
		if node.Role != "router" {
			continue
		}
		for _, iface := range node.Interfaces {
			if iface.Segment == "upstream" {
				return node.Name, iface.Segment, true
			}
		}
	}
	for _, node := range s.Topology.Nodes {
		if node.Role == "client" && len(node.Interfaces) > 0 {
			return node.Name, node.Interfaces[0].Segment, true
		}
	}
	return "", "", false
}

func hasDataPath(s *Scenario) bool { _, _, ok := dataPathTarget(s); return ok }

func namedResolver(s *Scenario) (string, bool) {
	var resolver string
	for _, node := range s.Topology.Nodes {
		if node.Role == "client" {
			resolver = node.Resolver
			break
		}
	}
	for _, node := range s.Topology.Nodes {
		if resolver != "" && !nodeOwnsAddress(node, resolver) {
			continue
		}
		for _, service := range node.Services {
			if service.Type == ServiceDNS && service.Name != "" {
				return service.Name, true
			}
		}
	}
	return "", false
}

func nodeOwnsAddress(node Node, raw string) bool {
	want, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	for _, iface := range node.Interfaces {
		for _, candidate := range []string{iface.Address, iface.IPv4, iface.IPv6} {
			address, _, _ := strings.Cut(candidate, "/")
			if got, err := netip.ParseAddr(address); err == nil && got == want {
				return true
			}
		}
	}
	for _, candidate := range node.Aliases {
		if got, err := netip.ParseAddr(candidate); err == nil && got == want {
			return true
		}
	}
	return false
}

func hasNamedResolver(s *Scenario) bool { _, ok := namedResolver(s); return ok }

func hasTimedDataPath(s *Scenario) bool { return hasDataPath(s) && hasNamedResolver(s) }

func nonClientDataLink(s *Scenario) (string, string, bool) {
	for _, node := range s.Topology.Nodes {
		if node.Role != "router" {
			continue
		}
		for _, iface := range node.Interfaces {
			if iface.Segment == "upstream" {
				return node.Name, iface.Segment, true
			}
		}
	}
	return "", "", false
}

func hasNonClientDataLink(s *Scenario) bool { _, _, ok := nonClientDataLink(s); return ok }

func dualStackGateway(s *Scenario) (string, bool) {
	for _, node := range s.Topology.Nodes {
		if node.Role != "router" {
			continue
		}
		for _, iface := range node.Interfaces {
			if iface.IPv4 != "" && iface.IPv6 != "" {
				return node.Name, true
			}
		}
	}
	return "", false
}

func hasDualStackPath(s *Scenario) bool { _, ok := dualStackGateway(s); return ok }

type httpTarget struct {
	node    string
	service int
	port    int
}

func findHTTPTestTarget(s *Scenario) (httpTarget, bool) {
	for _, test := range s.Tests {
		target, err := diagnostic.ParseTarget(test.Target)
		if err != nil {
			continue
		}
		if target.Proto != diagnostic.ProtoHTTP {
			continue
		}
		address := target.Host
		if target.IP == nil {
			address = dnsAddress(s, target.Host)
		}
		for ni, node := range s.Topology.Nodes {
			if address == "" || !nodeOwnsAddress(node, address) {
				continue
			}
			for si, service := range node.Services {
				if service.Type == ServiceHTTP && service.Port == target.Port {
					return httpTarget{node: s.Topology.Nodes[ni].Name, service: si, port: target.Port}, true
				}
			}
		}
	}
	return httpTarget{}, false
}

func dnsAddress(s *Scenario, name string) string {
	key := dnsKey(name)
	for _, node := range s.Topology.Nodes {
		for _, service := range node.Services {
			if service.Type != ServiceDNS {
				continue
			}
			for zoneName, address := range service.Zone {
				if dnsKey(zoneName) == key {
					return address
				}
			}
			for _, record := range service.Records {
				if dnsKey(record.Name) == key && strings.Contains(record.Address, ".") {
					return record.Address
				}
			}
		}
	}
	return ""
}

func hasHTTPTestTarget(s *Scenario) bool { _, ok := findHTTPTestTarget(s); return ok }

func hasSuccessfulHTTPTestTarget(s *Scenario) bool {
	target, ok := findHTTPTestTarget(s)
	return ok && s.Topology.node(target.node).Services[target.service].Status/100 == 2
}

type serviceTarget struct{ node, service string }

func findValidTLSTestTarget(s *Scenario) (serviceTarget, bool) {
	for _, test := range s.Tests {
		if test.Trust == nil {
			continue
		}
		target, err := diagnostic.ParseTarget(test.Target)
		if err != nil || target.Proto != diagnostic.ProtoTLSHTTP {
			continue
		}
		address := target.Host
		if target.IP == nil {
			address = dnsAddress(s, target.Host)
		}
		for _, node := range s.Topology.Nodes {
			if address == "" || !nodeOwnsAddress(node, address) {
				continue
			}
			for _, service := range node.Services {
				if service.Name == test.Trust.Service && service.Type == ServiceTLS && service.Port == target.Port &&
					service.Certificate != nil && service.Certificate.Mode == TLSCertificateValid &&
					target.IP == nil && certificateCoversHost(service.Certificate, target.Host) {
					return serviceTarget{node.Name, service.Name}, true
				}
			}
		}
	}
	return serviceTarget{}, false
}

func hasValidTLSTestTarget(s *Scenario) bool { _, ok := findValidTLSTestTarget(s); return ok }

type proxyTarget struct{ node, service, targetNode string }

const proxyCONNECTTarget = "connectivitycheck.gstatic.com"

func findSOCKS5TestProxy(s *Scenario) (proxyTarget, bool) {
	for _, test := range s.Tests {
		if test.Proxy == nil || test.Proxy.Scheme != "socks5h" {
			continue
		}
		for _, node := range s.Topology.Nodes {
			if node.Name != test.Proxy.Node {
				continue
			}
			for _, service := range node.Services {
				if service.Type != ServiceSOCKS5 || service.Port != test.Proxy.Port {
					continue
				}
				address := ""
				for _, resolver := range node.Services {
					if resolver.Type != ServiceDNS {
						continue
					}
					for name, candidate := range resolver.Zone {
						if dnsKey(name) == dnsKey(proxyCONNECTTarget) {
							address = candidate
						}
					}
				}
				for _, destination := range s.Topology.Nodes {
					if !nodeOwnsAddress(destination, address) {
						continue
					}
					for _, target := range destination.Services {
						if target.Type == ServiceTCP && target.Port == 443 {
							return proxyTarget{node.Name, service.Name, destination.Name}, true
						}
					}
				}
			}
		}
	}
	return proxyTarget{}, false
}

func hasSOCKS5TestProxy(s *Scenario) bool { _, ok := findSOCKS5TestProxy(s); return ok }

func certificateCoversHost(certificate *TLSCertificate, host string) bool {
	for _, name := range certificate.DNSNames {
		if dnsKey(name) == dnsKey(host) {
			return true
		}
	}
	return false
}

func findProbeFixture(s *Scenario, serviceType, name, certificateHost string) (serviceTarget, bool) {
	for _, node := range s.Topology.Nodes {
		for _, service := range node.Services {
			if service.Type == serviceType && service.Name == name && service.Port == 443 &&
				service.Certificate != nil && service.Certificate.Mode == TLSCertificateValid &&
				certificateCoversHost(service.Certificate, certificateHost) {
				return serviceTarget{node.Name, service.Name}, true
			}
		}
	}
	return serviceTarget{}, false
}

func hasWorkingQUICFixture(s *Scenario) bool {
	target, ok := findProbeFixture(s, ServiceQUIC, quicProbeService, proxyCONNECTTarget)
	if !ok {
		return false
	}
	if !nodeOwnsAddress(*s.Topology.node(target.node), dnsAddress(s, proxyCONNECTTarget)) {
		return false
	}
	for _, fault := range s.Faults {
		if fault.Type == FaultDrop && fault.Node == target.node && fault.Direction == DirectionInbound &&
			fault.Protocol == "udp" && fault.Port == 443 {
			return false
		}
	}
	return true
}

func hasWorkingDoHFixture(s *Scenario) bool {
	target, ok := findProbeFixture(s, ServiceEncryptedDNS, encryptedDNSProbeService, "cloudflare-dns.com")
	if !ok {
		return false
	}
	for _, node := range s.Topology.Nodes {
		if node.Name != target.node {
			continue
		}
		for _, service := range node.Services {
			if service.Name == target.service {
				return service.DoHResponse == ""
			}
		}
	}
	return false
}

type routeFamily string

const (
	familyIPv4 routeFamily = "ipv4"
	familyIPv6 routeFamily = "ipv6"
)

type preferredPathCandidate struct {
	family                    routeFamily
	preferred, alternate      Route
	preferredRouter, upstream string
	controlTarget             string
}

func hasWorkingAlternatePath(s *Scenario, family routeFamily) bool {
	_, ok := findPreferredPathCandidate(s, family)
	return ok
}

func hasPreferredPathFailureCandidate(s *Scenario) bool {
	return hasWorkingAlternatePath(s, familyIPv4) || hasWorkingAlternatePath(s, familyIPv6)
}

func findPreferredPathCandidate(s *Scenario, family routeFamily) (preferredPathCandidate, bool) {
	client := clientNode(s)
	var defaults []Route
	for _, route := range s.Topology.Routes {
		if route.Default && route.Node == client && route.Family == string(family) {
			defaults = append(defaults, route)
		}
	}
	sort.SliceStable(defaults, func(i, j int) bool { return defaults[i].Metric < defaults[j].Metric })
	if len(defaults) < 2 || defaults[0].Metric == defaults[1].Metric {
		return preferredPathCandidate{}, false
	}
	preferred := defaults[0]
	preferredRouter, _, upstream, ok := routeGatewayPath(s, preferred, family)
	if !ok {
		return preferredPathCandidate{}, false
	}
	for _, alternate := range defaults[1:] {
		if alternate.Metric <= preferred.Metric || alternate.Via == preferred.Via {
			continue
		}
		alternateRouter, alternateSegment, alternateUpstream, alternateOK := routeGatewayPath(s, alternate, family)
		if !alternateOK || alternateRouter == preferredRouter || alternateSegment == mustRouteSegment(s, preferred) ||
			alternateUpstream == upstream {
			continue
		}
		control, controlOK := controlledTargetOnRoute(s, client, family, alternate)
		if controlOK {
			return preferredPathCandidate{family: family, preferred: preferred, alternate: alternate,
				preferredRouter: preferredRouter, upstream: upstream, controlTarget: control}, true
		}
	}
	return preferredPathCandidate{}, false
}

func routeGatewayPath(s *Scenario, route Route, family routeFamily) (router, clientSegment, upstream string, ok bool) {
	via, err := netip.ParseAddr(route.Via)
	if err != nil || addressFamily(via) != string(family) {
		return "", "", "", false
	}
	clientSegment, ok = nodeSegmentForAddress(s.Topology.node(route.Node), via)
	if !ok {
		return "", "", "", false
	}
	for _, node := range s.Topology.Nodes {
		if node.Role != "router" || !nodeOwnsAddress(node, route.Via) {
			continue
		}
		if gatewaySegment, owns := nodeSegmentForAddress(&node, via); !owns || gatewaySegment != clientSegment {
			continue
		}
		for _, iface := range node.Interfaces {
			if iface.Segment != clientSegment {
				if _, familyOK := iface.addressForFamily(string(family)); familyOK {
					return node.Name, clientSegment, iface.Segment, true
				}
			}
		}
	}
	return "", "", "", false
}

func mustRouteSegment(s *Scenario, route Route) string {
	segment, _ := nodeSegmentForAddress(s.Topology.node(route.Node), netip.MustParseAddr(route.Via))
	return segment
}

func controlledTargetOnRoute(s *Scenario, client string, family routeFamily, want Route) (string, bool) {
	for _, test := range s.Tests {
		target, err := diagnostic.ParseTarget(test.Target)
		if err != nil || test.Node != client || target.IP == nil || addressFamily(netip.MustParseAddr(target.IP.String())) != string(family) ||
			!scenarioControlledTCPPort(s, target.IP.String(), target.Port) {
			continue
		}
		selected, ok := configuredRouteTo(s.Topology.Routes, client, netip.MustParseAddr(target.IP.String()))
		if ok && selected.Via == want.Via {
			return netip.AddrPortFrom(netip.MustParseAddr(target.IP.String()), uint16(target.Port)).String(), true
		}
	}
	return "", false
}

func scenarioControlledTCPPort(s *Scenario, address string, port int) bool {
	for _, node := range s.Topology.Nodes {
		if !nodeOwnsAddress(node, address) {
			continue
		}
		for _, service := range node.Services {
			if service.Port == port && service.Type != ServiceQUIC {
				return true
			}
		}
	}
	return false
}

func configuredRouteTo(routes []Route, node string, destination netip.Addr) (Route, bool) {
	bestBits := -1
	var best Route
	for _, route := range routes {
		if route.Node != node || route.Family != addressFamily(destination) {
			continue
		}
		bits := 0
		if !route.Default {
			prefix, err := netip.ParsePrefix(route.Destination)
			if err != nil || !prefix.Contains(destination) {
				continue
			}
			bits = prefix.Bits()
		}
		if bits > bestBits || bits == bestBits && route.Metric < best.Metric {
			bestBits, best = bits, route
		}
	}
	return best, bestBits >= 0
}

func clientNode(s *Scenario) string {
	for _, node := range s.Topology.Nodes {
		if node.Role == "client" {
			return node.Name
		}
	}
	return ""
}

func generateLoss(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, segment, _ := dataPathTarget(s)
	loss := float64(int64n(rng, 500, 3000)) / 100
	return GeneratedMutation{Node: node, Segment: segment, LossPercent: loss, NetemSeed: nonzeroUint32(rng),
		Description: fmt.Sprintf("inject %.2f%% packet loss on %s/%s", loss, node, segment)}, nil
}

func generateLatency(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, segment, _ := dataPathTarget(s)
	latency := int64n(rng, 50, 800)
	return GeneratedMutation{Node: node, Segment: segment, LatencyMS: latency,
		Description: fmt.Sprintf("add %d ms latency on %s/%s", latency, node, segment)}, nil
}

func generateJitter(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, segment, _ := dataPathTarget(s)
	jitter := int64n(rng, 10, 250)
	latency := int64n(rng, jitter+10, 800)
	return GeneratedMutation{Node: node, Segment: segment, LatencyMS: latency, JitterMS: jitter, NetemSeed: nonzeroUint32(rng),
		Description: fmt.Sprintf("add %d ms latency with %d ms jitter on %s/%s", latency, jitter, node, segment)}, nil
}

func generateNetemSpike(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, segment, _ := dataPathTarget(s)
	start := int64n(rng, 150, 250)
	duration := int64n(rng, 700, 1200)
	latency := int64n(rng, 300, 800)
	loss := float64(int64n(rng, 500, 3000)) / 100
	service, _ := namedResolver(s)
	return GeneratedMutation{Node: node, Segment: segment, Service: service, StartMS: start, DurationMS: duration,
		LatencyMS: latency, LossPercent: loss, NetemSeed: nonzeroUint32(rng),
		Description: fmt.Sprintf("spike %s/%s to %d ms latency and %.2f%% loss for %d ms", node, segment, latency, loss, duration)}, nil
}

func generateDNSSERVFAIL(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	service, _ := namedResolver(s)
	return GeneratedMutation{Service: service, Description: "make resolver " + service + " return SERVFAIL"}, nil
}

func generateDNSDrop(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	service, _ := namedResolver(s)
	return GeneratedMutation{Service: service, Description: "make resolver " + service + " drop responses"}, nil
}

func generateDNSOutage(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	service, _ := namedResolver(s)
	duration := int64n(rng, 450, 900)
	return GeneratedMutation{Service: service, StartMS: 150, DurationMS: duration,
		Description: fmt.Sprintf("delay, then silence resolver %s for %d ms before recovery", service, duration)}, nil
}

func generateTCPReset(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findHTTPTestTarget(s)
	return GeneratedMutation{Node: target.node, TargetPort: target.port,
		Description: fmt.Sprintf("replace %s TCP port %d with an accept-then-reset service", target.node, target.port)}, nil
}

func generateTLSExpired(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findValidTLSTestTarget(s)
	return GeneratedMutation{Node: target.node, Service: target.service,
		Description: "make TLS service " + target.service + " present an expired certificate"}, nil
}

func generateProxyCONNECTRefused(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findSOCKS5TestProxy(s)
	return GeneratedMutation{Node: target.node, TargetNode: target.targetNode, Service: target.service, TargetPort: 443,
		Description: "make proxy " + target.service + " return CONNECT refused for " + target.targetNode + " TCP/443"}, nil
}

func generateQUICUDP443Block(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findProbeFixture(s, ServiceQUIC, quicProbeService, proxyCONNECTTarget)
	return GeneratedMutation{Node: target.node, Service: target.service, TargetPort: 443,
		Description: "silently drop inbound QUIC UDP/443 at " + target.node + " while preserving TCP/443"}, nil
}

func generateInvalidDoHResponse(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findProbeFixture(s, ServiceEncryptedDNS, encryptedDNSProbeService, "cloudflare-dns.com")
	return GeneratedMutation{Node: target.node, Service: target.service,
		Description: "make DoH service " + target.service + " return protocol-invalid DNS bytes while preserving DoT"}, nil
}

func generateHTTP503(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	target, _ := findHTTPTestTarget(s)
	return GeneratedMutation{Node: target.node, TargetPort: target.port, Status: http.StatusServiceUnavailable,
		Description: fmt.Sprintf("make %s HTTP port %d return status 503", target.node, target.port)}, nil
}

func generateIPv4Drop(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, _ := dualStackGateway(s)
	return GeneratedMutation{Node: node, Family: "ipv4", Description: "drop IPv4 forwarding at " + node + " while preserving IPv6"}, nil
}

func generateIPv6Drop(_ *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, _ := dualStackGateway(s)
	return GeneratedMutation{Node: node, Family: "ipv6", Description: "drop IPv6 forwarding at " + node + " while preserving IPv4"}, nil
}

func generateTransientLink(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	node, segment, _ := nonClientDataLink(s)
	duration := int64n(rng, 450, 900)
	return GeneratedMutation{Node: node, Segment: segment, DurationMS: duration,
		Description: fmt.Sprintf("lower non-client link %s/%s for %d ms, then restore it", node, segment, duration)}, nil
}

func generatePreferredPathFailure(rng *mathrand.Rand, s *Scenario) (GeneratedMutation, error) {
	var candidates []preferredPathCandidate
	for _, family := range []routeFamily{familyIPv4, familyIPv6} {
		if candidate, ok := findPreferredPathCandidate(s, family); ok {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return GeneratedMutation{}, errors.New("preferred default route with a working alternate disappeared")
	}
	candidate := candidates[0]
	if len(candidates) > 1 {
		candidate = candidates[rng.Intn(len(candidates))]
	}
	return GeneratedMutation{Node: candidate.preferredRouter, TargetNode: clientNode(s), Segment: candidate.upstream,
		Family: string(candidate.family), PreferredVia: candidate.preferred.Via,
		PreferredSegment: mustRouteSegment(s, candidate.preferred), PreferredMetric: candidate.preferred.Metric,
		AlternateVia: candidate.alternate.Via, AlternateSegment: mustRouteSegment(s, candidate.alternate),
		AlternateMetric: candidate.alternate.Metric, ControlTarget: candidate.controlTarget,
		Description: fmt.Sprintf("disable %s preferred router %s upstream %s while retaining the alternate default", candidate.family, candidate.preferredRouter, candidate.upstream)}, nil
}

func applyGeneratedMutation(s *Scenario, m GeneratedMutation) error {
	switch m.ID {
	case "netem.loss", "netem.latency", "netem.jitter":
		fault := Fault{Type: FaultNetem, Node: m.Node, Segment: m.Segment, Seed: m.NetemSeed}
		if m.LatencyMS > 0 {
			fault.Delay = (time.Duration(m.LatencyMS) * time.Millisecond).String()
		}
		if m.JitterMS > 0 {
			fault.Jitter = (time.Duration(m.JitterMS) * time.Millisecond).String()
		}
		if m.LossPercent > 0 {
			fault.Loss = formatPercent(m.LossPercent)
		}
		s.Faults = append(s.Faults, fault)
	case "timeline.netem_spike":
		end := m.StartMS + m.DurationMS
		s.Faults = append(s.Faults,
			Fault{Type: FaultScheduledDNS, Service: m.Service, Events: []ScheduledEvent{
				{At: "0ms", Outcome: DNSOutcomeDelay, Delay: "300ms"},
				{At: (time.Duration(end) * time.Millisecond).String(), Outcome: DNSOutcomeAnswer},
			}},
			Fault{Type: FaultScheduledNetem, Node: m.Node, Segment: m.Segment, Seed: m.NetemSeed, Events: []ScheduledEvent{
				{At: "0ms"},
				{At: (time.Duration(m.StartMS) * time.Millisecond).String(), Latency: (time.Duration(m.LatencyMS) * time.Millisecond).String(), LossPercent: m.LossPercent},
				{At: (time.Duration(end) * time.Millisecond).String()},
			}},
		)
	case "dns.servfail":
		s.Faults = append(s.Faults, Fault{Type: FaultScheduledDNS, Service: m.Service,
			Events: []ScheduledEvent{{At: "0ms", Outcome: DNSOutcomeSERVFAIL}}})
	case "dns.drop":
		s.Faults = append(s.Faults, Fault{Type: FaultScheduledDNS, Service: m.Service,
			Events: []ScheduledEvent{{At: "0ms", Outcome: DNSOutcomeDrop}}})
	case "timeline.dns_outage":
		end := m.StartMS + m.DurationMS
		s.Faults = append(s.Faults, Fault{Type: FaultScheduledDNS, Service: m.Service, Events: []ScheduledEvent{
			{At: "0ms", Outcome: DNSOutcomeDelay, Delay: "200ms"},
			{At: (time.Duration(m.StartMS) * time.Millisecond).String(), Outcome: DNSOutcomeDrop},
			{At: (time.Duration(end) * time.Millisecond).String(), Outcome: DNSOutcomeAnswer},
		}})
		if len(s.Tests) > 0 {
			base := cloneTest(s.Tests[0])
			first, second, third := cloneTest(base), cloneTest(base), cloneTest(base)
			first.Name = "hunt resolver before outage"
			second.Name = "hunt resolver during outage"
			third.Name = "hunt resolver after recovery"
			s.Tests = []Test{first, second, third}
		}
	case "service.tcp_reset":
		for ni := range s.Topology.Nodes {
			if s.Topology.Nodes[ni].Name != m.Node {
				continue
			}
			for si := range s.Topology.Nodes[ni].Services {
				service := &s.Topology.Nodes[ni].Services[si]
				if service.Type == ServiceHTTP && service.Port == m.TargetPort {
					service.Type, service.Status, service.Body = ServiceTCPReset, 0, ""
					return nil
				}
			}
		}
		return errors.New("HTTP target disappeared")
	case "service.tls_expired":
		for ni := range s.Topology.Nodes {
			for si := range s.Topology.Nodes[ni].Services {
				service := &s.Topology.Nodes[ni].Services[si]
				if s.Topology.Nodes[ni].Name == m.Node && service.Name == m.Service && service.Type == ServiceTLS &&
					service.Certificate != nil && service.Certificate.Mode == TLSCertificateValid {
					service.Certificate.Mode = TLSCertificateExpired
					return nil
				}
			}
		}
		return errors.New("valid TLS target disappeared")
	case "proxy.connect_refused":
		for ni := range s.Topology.Nodes {
			if s.Topology.Nodes[ni].Name != m.TargetNode {
				continue
			}
			for si, service := range s.Topology.Nodes[ni].Services {
				if service.Type == ServiceTCP && service.Port == m.TargetPort {
					s.Topology.Nodes[ni].Services = append(s.Topology.Nodes[ni].Services[:si], s.Topology.Nodes[ni].Services[si+1:]...)
					return nil
				}
			}
		}
		return errors.New("proxy CONNECT destination disappeared")
	case "quic.udp_443_block":
		s.Faults = append(s.Faults, Fault{Type: FaultDrop, Node: m.Node, Direction: DirectionInbound,
			Protocol: "udp", Port: m.TargetPort})
	case "encrypted_dns.doh_invalid":
		for ni := range s.Topology.Nodes {
			for si := range s.Topology.Nodes[ni].Services {
				service := &s.Topology.Nodes[ni].Services[si]
				if s.Topology.Nodes[ni].Name == m.Node && service.Name == m.Service && service.Type == ServiceEncryptedDNS && service.DoHResponse == "" {
					service.DoHResponse = DoHResponseInvalid
					return nil
				}
			}
		}
		return errors.New("working DoH fixture disappeared")
	case "http.status_503":
		for ni := range s.Topology.Nodes {
			if s.Topology.Nodes[ni].Name != m.Node {
				continue
			}
			for si := range s.Topology.Nodes[ni].Services {
				service := &s.Topology.Nodes[ni].Services[si]
				if service.Type == ServiceHTTP && service.Port == m.TargetPort && service.Status/100 == 2 {
					service.Status = m.Status
					return nil
				}
			}
		}
		return errors.New("successful HTTP target disappeared")
	case "family.ipv4_drop", "family.ipv6_drop":
		s.Faults = append(s.Faults, Fault{Type: FaultDrop, Node: m.Node, Family: m.Family, Direction: DirectionOutbound})
	case "link.transient_down":
		s.Faults = append(s.Faults, Fault{Type: FaultScheduledLink, Node: m.Node, Segment: m.Segment, Events: []ScheduledEvent{
			{At: "0ms", State: LinkStateDown},
			{At: (time.Duration(m.DurationMS) * time.Millisecond).String(), State: LinkStateUp},
		}})
	case "routing.preferred_path_failure":
		s.Faults = append(s.Faults, Fault{Type: FaultLinkDown, Node: m.Node, Segment: m.Segment})
	default:
		return fmt.Errorf("unknown generated mutation %q", m.ID)
	}
	return nil
}

func cloneTest(in Test) Test {
	out := in
	if in.Proxy != nil {
		copy := *in.Proxy
		out.Proxy = &copy
	}
	if in.Trust != nil {
		copy := *in.Trust
		out.Trust = &copy
	}
	if in.Expect != nil {
		copy := *in.Expect
		copy.Checks = append([]ExpectedCheck(nil), in.Expect.Checks...)
		out.Expect = &copy
	}
	return out
}
