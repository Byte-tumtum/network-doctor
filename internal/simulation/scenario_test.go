package simulation

import (
	"fmt"
	"strings"
	"testing"
)

const minimalScenario = `
name: t
topology:
  nodes:
    - {name: client, role: client, address: 10.77.0.10, gateway: 10.77.0.1}
    - {name: server, address: 10.77.0.1}
tests:
  - {node: client, target: example.test:80}
expect:
  verdict: ok
`

func TestParseScenarioDefaults(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(minimalScenario))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := s.Topology.subnetOrDefault(); got != "10.77.0.0/24" {
		t.Errorf("subnet default = %q", got)
	}
	if s.Topology.Nodes[1].Role != "server" {
		t.Errorf("role default = %q, want server", s.Topology.Nodes[1].Role)
	}
	if s.Tests[0].Type != TestNetdoc {
		t.Errorf("test type default = %q", s.Tests[0].Type)
	}
	if s.Tests[0].Name == "" {
		t.Error("test name should be derived when omitted")
	}
	if c := s.Client(); c == nil || c.Name != "client" {
		t.Errorf("Client() = %v", c)
	}
}

func TestParseScenarioRejects(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown field", minimalScenario + "\nwidgets: 3\n", "widgets"},
		{"unknown node field", strings.Replace(minimalScenario, "role: client", "rolle: client", 1), "rolle"},
		{"empty", "", "empty scenario"},
		{"two documents", minimalScenario + "\n---\n" + minimalScenario, "exactly one document"},
		{"no name", strings.Replace(minimalScenario, "name: t", "name: \"\"", 1), "name is required"},
		{"no client", strings.Replace(minimalScenario, "role: client", "role: server", 1), "exactly one node"},
		{"unknown role", strings.Replace(minimalScenario, "role: client", "role: router", 1), "unknown role"},
		{"bad address", strings.Replace(minimalScenario, "10.77.0.10", "not-an-ip", 1), "address"},
		{"address off subnet", strings.Replace(minimalScenario, "10.77.0.10", "192.0.2.5", 1), "outside"},
		{"duplicate node", strings.Replace(minimalScenario, "name: server", "name: client", 1), "duplicate node"},
		{"unsafe node name", strings.Replace(minimalScenario, "name: server", "name: \"a b\"", 1), "letters, digits"},
		{"bad target", strings.Replace(minimalScenario, "example.test:80", "'::::'", 1), "invalid target"},
		{"unknown verdict", strings.Replace(minimalScenario, "verdict: ok", "verdict: broken", 1), "unknown verdict"},
		{"no tests", strings.Replace(minimalScenario, "  - {node: client, target: example.test:80}", "", 1), "at least one test"},
		{"nothing expected", strings.Replace(minimalScenario, "  verdict: ok", "  checks: []", 1), "must expect"},
		{
			"unknown probe id",
			strings.Replace(minimalScenario, "  verdict: ok", "  checks: [{id: nope, status: PASS}]", 1),
			"unknown probe id",
		},
		{
			"unknown status",
			strings.Replace(minimalScenario, "  verdict: ok", "  checks: [{id: dns, status: BROKEN}]", 1),
			"unknown status",
		},
		{
			"unknown cause",
			strings.Replace(minimalScenario, "  verdict: ok", "  checks: [{id: tls, status: FAIL, cause: invented}]", 1),
			"unknown cause",
		},
		{
			"cause on passing expectation",
			strings.Replace(minimalScenario, "  verdict: ok", "  checks: [{id: tls, status: PASS, cause: hostname_mismatch}]", 1),
			"requires FAIL",
		},
		{
			"duplicate expectation",
			strings.Replace(minimalScenario, "  verdict: ok", "  checks: [{id: dns, status: PASS}, {id: dns, status: FAIL}]", 1),
			"duplicate probe id",
		},
		{
			"unknown service",
			strings.Replace(minimalScenario, "address: 10.77.0.1}", "address: 10.77.0.1, services: [{type: gopher}]}", 1),
			"unknown service type",
		},
		{
			"duplicate service name",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: duplicate, type: tcp, port: 80}, {name: duplicate, type: tcp, port: 81}]}", 1),
			"duplicate service name",
		},
		{
			"conflicting DNS record spelling",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{type: dns, zone: {Example.Test: 10.77.0.1, example.test.: 10.77.0.2}}]}", 1),
			"duplicate DNS record",
		},
		{
			"unsupported SOCKS option",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, resolver: 10.77.0.1, services: [{type: socks5, body: nope}]}", 1),
			"unsupported options",
		},
		{
			"TLS missing certificate",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: tls-target, type: tls, port: 9443}]}", 1),
			"configuration is required",
		},
		{
			"TLS unknown certificate mode",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: tls-target, type: tls, port: 9443, certificate: {mode: forged, dns_names: [example.test]}}]}", 1),
			"unknown mode",
		},
		{
			"TLS empty SAN list",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: tls-target, type: tls, port: 9443, certificate: {mode: valid, dns_names: []}}]}", 1),
			"must not be empty",
		},
		{
			"TLS IP literal SAN",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: tls-target, type: tls, port: 9443, certificate: {mode: valid, dns_names: [192.0.2.1]}}]}", 1),
			"IP literal",
		},
		{
			"TLS unknown certificate field",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{name: tls-target, type: tls, certificate: {mode: valid, dns_names: [example.test], private_key: /tmp/key}}]}", 1),
			"private_key",
		},
		{
			"zone name is not a hostname",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{type: dns, zone: {'a b': 10.77.0.1}}]}", 1),
			"not a hostname",
		},
		{
			"unknown fault",
			minimalScenario + "\nfaults: [{type: meltdown, node: client}]\n",
			"unknown fault type",
		},
		{
			"fault on unknown node",
			minimalScenario + "\nfaults: [{type: no_default_route, node: ghost}]\n",
			"unknown node",
		},
		{
			"netem with nothing to do",
			minimalScenario + "\nfaults: [{type: netem, node: client}]\n",
			"needs delay",
		},
		{
			"jitter without delay",
			minimalScenario + "\nfaults: [{type: netem, node: client, jitter: 10ms}]\n",
			"jitter needs a delay",
		},
		{
			"loss is not a percentage",
			minimalScenario + "\nfaults: [{type: netem, node: client, loss: lots}]\n",
			"not a percentage",
		},
		{
			"drop port without protocol",
			minimalScenario + "\nfaults: [{type: drop, node: client, port: 53}]\n",
			"needs a protocol",
		},
		{
			"unknown direction",
			minimalScenario + "\nfaults: [{type: drop, node: client, direction: sideways}]\n",
			"unknown direction",
		},
		{
			"unknown test type",
			strings.Replace(minimalScenario, "  - {node: client,", "  - {type: fuzz, node: client,", 1),
			"unknown test type",
		},
		{
			"unsupported proxy scheme",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks4, node: server}", 1),
			"unsupported scheme",
		},
		{
			"proxy on unknown node",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5, node: missing}", 1),
			"unknown node",
		},
		{
			"proxy port out of range",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5, node: server, port: 70000}", 1),
			"out of range",
		},
		{
			"proxy references no service",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5, node: server}", 1),
			"has no socks5 service",
		},
		{
			"unknown proxy option",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5, node: server, command: sh}", 1),
			"command",
		},
		{
			"unknown trust service",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, trust: {service: missing}", 1),
			"unknown tls service",
		},
		{
			"unknown trust option",
			strings.Replace(minimalScenario, "target: example.test:80", "target: example.test:80, trust: {service: tls-target, path: /tmp/ca}", 1),
			"path",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseScenario(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFaultDefaults(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(
		minimalScenario + "\nfaults: [{type: drop, node: client, protocol: udp, port: 53}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Faults[0].Direction != DirectionOutbound {
		t.Errorf("direction default = %q, want %q", s.Faults[0].Direction, DirectionOutbound)
	}
}

func TestProxyDefaultsAndUsesValidatedNodeAddress(t *testing.T) {
	raw := strings.Replace(minimalScenario, "address: 10.77.0.1}",
		"address: 10.77.0.1, resolver: 10.77.0.1, services: [{name: proxy, type: socks5}]}", 1)
	raw = strings.Replace(raw, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5h, node: server}", 1)
	s, err := ParseScenario(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	proxy := s.Tests[0].Proxy
	if proxy == nil || proxy.Port != 1080 || proxy.address != "10.77.0.1" {
		t.Errorf("proxy = %+v", proxy)
	}
	if got := s.Topology.Nodes[1].Services[0].Port; got != 1080 {
		t.Errorf("service port = %d", got)
	}
}

func TestTLSServiceDefaultsAndValidatedTrust(t *testing.T) {
	raw := strings.Replace(minimalScenario, "address: 10.77.0.1}",
		"address: 10.77.0.1, services: [{name: tls-target, type: tls, certificate: {mode: valid, dns_names: [Secure-Target.Test.]}}]}", 1)
	raw = strings.Replace(raw, "target: example.test:80", "target: example.test:80, trust: {service: tls-target}", 1)
	s, err := ParseScenario(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	svc := s.Topology.Nodes[1].Services[0]
	if svc.Port != 443 || svc.Certificate.DNSNames[0] != "secure-target.test" {
		t.Errorf("TLS service = %+v", svc)
	}
	if s.Tests[0].Trust == nil || s.Tests[0].Trust.Service != "tls-target" {
		t.Errorf("trust = %+v", s.Tests[0].Trust)
	}
}

func TestTLSCertificateRejectsExcessiveOrInvalidNames(t *testing.T) {
	many := make([]string, tlsMaxDNSNames+1)
	for i := range many {
		many[i] = fmt.Sprintf("host-%d.test", i)
	}
	for _, cfg := range []*TLSCertificate{
		{Mode: TLSCertificateValid, DNSNames: many},
		{Mode: TLSCertificateValid, DNSNames: []string{strings.Repeat("a", 64) + ".test"}},
		{Mode: TLSCertificateValid, DNSNames: []string{"Example.Test", "example.test."}},
	} {
		if err := validateTLSCertificate(cfg); err == nil {
			t.Errorf("accepted invalid TLS certificate intent: %+v", cfg)
		}
	}
}

// TestLibraryScenariosValidate is the guard that keeps a shipped scenario from
// rotting: every one of them has to parse and validate in the plain unit suite,
// where no namespace is needed to find out.
func TestLibraryScenariosValidate(t *testing.T) {
	names := LibraryNames()
	if len(names) < 2 {
		t.Fatalf("library has %d scenarios, want at least 2", len(names))
	}
	for _, name := range names {
		if _, err := LibraryScenario(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := LibraryScenario("no-such-scenario"); err == nil {
		t.Error("want an error for an unknown scenario")
	}
}

func TestLoadPrefersFilesForPathLikeRefs(t *testing.T) {
	// A reference that looks like a path must never resolve to a built-in, or a
	// local scenario could be silently shadowed by one shipped in the binary.
	if _, err := Load("./healthy.yaml"); err == nil {
		t.Error("want a file-not-found error, got a built-in")
	}
}

func TestIsPercent(t *testing.T) {
	for _, ok := range []string{"0%", "10%", "0.5%", "100%"} {
		if !isPercent(ok) {
			t.Errorf("isPercent(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "%", "10", "1.2.3%", "ten%", "10%%"} {
		if isPercent(bad) {
			t.Errorf("isPercent(%q) = true", bad)
		}
	}
}

func TestIsSafeName(t *testing.T) {
	for _, ok := range []string{"client", "node-1", "a"} {
		if !isSafeName(ok) {
			t.Errorf("isSafeName(%q) = false", ok)
		}
	}
	// These are the ones that matter: every name reaches an argv.
	for _, bad := range []string{"", "-lead", "a b", "a;b", "a/b", "a$b", "a\nb", strings.Repeat("a", 33)} {
		if isSafeName(bad) {
			t.Errorf("isSafeName(%q) = true", bad)
		}
	}
}

func TestIsSafeHostname(t *testing.T) {
	for _, ok := range []string{"private-target.test", "EXAMPLE.test.", strings.Repeat("a", 63) + ".test"} {
		if !isSafeHostname(ok) {
			t.Errorf("isSafeHostname(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", ".", "-bad.test", "bad-.test", "bad_name.test", strings.Repeat("a", 64) + ".test"} {
		if isSafeHostname(bad) {
			t.Errorf("isSafeHostname(%q) = true", bad)
		}
	}
}
