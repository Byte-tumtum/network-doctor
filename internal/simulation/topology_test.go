package simulation

import (
	"strings"
	"testing"
)

const routedScenario = `
name: routed
topology:
  segments:
    - {name: client-lan, subnet: 10.77.1.0/24}
    - {name: upstream, subnet: 10.77.2.0/24}
  nodes:
    - name: client
      role: client
      resolver: 10.77.2.20
      interfaces:
        - {segment: client-lan, address: 10.77.1.10/24}
    - name: gateway
      role: router
      interfaces:
        - {segment: client-lan, address: 10.77.1.1/24}
        - {segment: upstream, address: 10.77.2.1/24}
    - name: target
      interfaces:
        - {segment: upstream, address: 10.77.2.20/24}
  routes:
    - {node: client, destination: default, via: 10.77.1.1, metric: 100}
    - {node: target, destination: 10.77.1.0/24, via: 10.77.2.1}
tests:
  - {node: client, target: 10.77.2.20:80}
expect:
  verdict: ok
`

func TestRoutedTopologyValidationAndCanonicalization(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(routedScenario))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Topology.Segments) != 2 || len(s.Topology.Nodes[1].Interfaces) != 2 {
		t.Fatalf("topology was not retained: %+v", s.Topology)
	}
	if got := s.Topology.Nodes[0].Address; got != "10.77.1.10" {
		t.Errorf("primary address = %q", got)
	}
	if got := s.Topology.Nodes[0].Gateway; got != "10.77.1.1" {
		t.Errorf("preferred gateway = %q", got)
	}
	routes := s.Topology.routeForNode("client", "default")
	if len(routes) != 1 || routes[0].Metric != 100 {
		t.Errorf("routes = %+v", routes)
	}
}

func TestLegacyTopologyNormalizesWithoutChangingSourceSchema(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(minimalScenario))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Topology.Segments) != 1 || s.Topology.Segments[0].Name != legacySegmentName {
		t.Fatalf("segments = %+v", s.Topology.Segments)
	}
	if got := s.Topology.Nodes[0].Interfaces[0].Address; got != "10.77.0.10/24" {
		t.Errorf("legacy interface = %q", got)
	}
	if len(s.Topology.Routes) != 1 || !s.Topology.Routes[0].Default {
		t.Errorf("legacy routes = %+v", s.Topology.Routes)
	}
}

func TestRoutedTopologyRejects(t *testing.T) {
	tests := []struct {
		name, old, replacement, want string
	}{
		{"duplicate segment", "- {name: upstream, subnet: 10.77.2.0/24}", "- {name: client-lan, subnet: 10.77.2.0/24}", "duplicate segment"},
		{"overlap segment", "10.77.2.0/24", "10.77.1.128/25", "overlaps"},
		{"unknown segment", "segment: upstream, address: 10.77.2.20/24", "segment: missing, address: 10.77.2.20/24", "unknown segment"},
		{"outside segment", "10.77.2.20/24", "10.77.3.20/24", "outside segment"},
		{"duplicate address", "10.77.2.20/24", "10.77.2.1/24", "duplicate interface address"},
		{"router needs two links", "role: router", "role: server", ""},
		{"gateway off link", "via: 10.77.1.1, metric: 100", "via: 10.77.2.1, metric: 100", "not on a directly connected"},
		{"negative metric", "metric: 100", "metric: -1", "out of range"},
		{"scoped gateway", "via: 10.77.1.1, metric: 100", "via: fe80::1%eth0, metric: 100", "scoped"},
	}
	for _, tc := range tests {
		if tc.want == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(routedScenario, tc.old, tc.replacement, 1)
			_, err := ParseScenario(strings.NewReader(raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestConflictingDefaultsNeedDistinctMetrics(t *testing.T) {
	insert := "    - {node: client, destination: default, via: 10.77.1.254, metric: 100}\n"
	raw := strings.Replace(routedScenario, "    - {node: target, destination:", insert+"    - {node: target, destination:", 1)
	if _, err := ParseScenario(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "conflicting routes") {
		t.Fatalf("same metric defaults error = %v", err)
	}
	raw = strings.Replace(raw, "via: 10.77.1.254, metric: 100", "via: 10.77.1.254, metric: 200", 1)
	s, err := ParseScenario(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	routes := s.Topology.routeForNode("client", "default")
	if len(routes) != 2 || routes[0].Metric != 100 || routes[1].Metric != 200 {
		t.Errorf("metric order = %+v", routes)
	}
}

func TestFaultLogicalRouteAndLinkValidation(t *testing.T) {
	for _, addition := range []string{
		"faults: [{type: replace_default_route, node: client, via: 10.77.1.254, metric: 50}]\n",
		"faults: [{type: link_down, node: gateway, segment: client-lan}]\n",
	} {
		if _, err := ParseScenario(strings.NewReader(routedScenario + addition)); err != nil {
			t.Errorf("valid fault %q: %v", addition, err)
		}
	}
	bad := routedScenario + "faults: [{type: link_down, node: gateway, segment: missing}]\n"
	if _, err := ParseScenario(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "no interface") {
		t.Fatalf("bad link fault error = %v", err)
	}
}

func TestUnknownRoutedFieldsAreRejected(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(routedScenario, "subnet: 10.77.1.0/24", "subnet: 10.77.1.0/24, bridge: br0", 1),
		strings.Replace(routedScenario, "address: 10.77.1.10/24", "address: 10.77.1.10/24, name: eth0", 1),
		strings.Replace(routedScenario, "metric: 100", "metric: 100, expression: default-via", 1),
	} {
		if _, err := ParseScenario(strings.NewReader(raw)); err == nil {
			t.Fatal("unknown topology field was accepted")
		}
	}
}
