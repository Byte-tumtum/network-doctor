//go:build linux

package simulation

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestKernelInterfaceNamesAreBoundedAndCollisionFree(t *testing.T) {
	id := NewID()
	if !isRunID(id) {
		t.Fatalf("generated id %q is not accepted as a run id", id)
	}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		for _, prefix := range []string{"nb", "ne", "np"} {
			name := kernelLinkName(prefix, id, i)
			if len(name) > 15 {
				t.Fatalf("name %q has %d bytes", name, len(name))
			}
			if seen[name] {
				t.Fatalf("duplicate name %q", name)
			}
			seen[name] = true
		}
	}
	for _, bad := range []string{"", "abc123", strings.ToUpper(id), id[:len(id)-1], id + "0", id[:len(id)-1] + "g"} {
		if isRunID(bad) {
			t.Errorf("unsafe run id %q accepted", bad)
		}
	}
}

func TestParseRouteGetMapsOnlyKnownKernelInterfaces(t *testing.T) {
	np := &nodeProc{ifaces: []*interfaceProc{{logical: &Interface{Segment: "client-lan"}, iface: "neabc1230"}}}
	got, err := parseRouteGet([]byte("10.77.2.20 via 10.77.1.1 dev neabc1230 src 10.77.1.10 uid 0\n"), np, "ipv4")
	if err != nil {
		t.Fatal(err)
	}
	if got.Via != "10.77.1.1" || got.Source != "10.77.1.10" || got.Segment != "client-lan" {
		t.Errorf("route = %+v", got)
	}
	if _, err := parseRouteGet([]byte("10.77.2.20 via 10.77.1.1 dev neabc1230 src 10.77.1.10\n    cache\n"), np, "ipv4"); err != nil {
		t.Errorf("kernel cache continuation rejected: %v", err)
	}
	for _, raw := range [][]byte{
		[]byte("10.77.2.20 via 10.77.1.1 dev attacker src 10.77.1.10\n"),
		[]byte("10.77.2.20 via bad dev neabc1230\n"),
		[]byte("one\ntwo\n"),
		make([]byte, maxRouteGetOutput+1),
	} {
		if _, err := parseRouteGet(raw, np, "ipv4"); err == nil {
			t.Errorf("malformed route output accepted: %q", raw)
		}
	}
}

func TestParseIPv6RouteGet(t *testing.T) {
	np := &nodeProc{ifaces: []*interfaceProc{{logical: &Interface{Segment: "client-lan"}, iface: "neabc1230"}}}
	got, err := parseRouteGet([]byte("2001:db8:77:2::20 via 2001:db8:77:1::1 dev neabc1230 src 2001:db8:77:1::10 metric 100 pref medium\n"), np, "ipv6")
	if err != nil {
		t.Fatal(err)
	}
	if got.Family != "ipv6" || got.Via != "2001:db8:77:1::1" || got.Source != "2001:db8:77:1::10" || got.Metric != 100 {
		t.Errorf("IPv6 route = %+v", got)
	}
	direct, err := parseRouteGet([]byte("2001:db8:77:1::53 dev neabc1230 src 2001:db8:77:1::10 pref medium\n"), np, "ipv6")
	if err != nil || direct.Via != "" || direct.Segment != "client-lan" {
		t.Errorf("direct IPv6 route = %+v, %v", direct, err)
	}
	for _, raw := range []string{
		"2001:db8::1 via 10.0.0.1 dev neabc1230 src 2001:db8::2",
		"2001:db8::1 dev neabc1230 src 10.0.0.2",
		"2001:db8::1 dev neabc1230 metric huge",
		"2001:db8::1 dev",
	} {
		if _, err := parseRouteGet([]byte(raw), np, "ipv6"); err == nil {
			t.Errorf("malformed IPv6 route accepted: %q", raw)
		}
	}
}

func TestParseLinkUp(t *testing.T) {
	if !parseLinkUp("2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500") {
		t.Error("UP flag not recognized")
	}
	if parseLinkUp("2: eth0: <BROADCAST,MULTICAST> mtu 1500") {
		t.Error("down link reported up")
	}
}

func TestParseNetemStats(t *testing.T) {
	raw := []byte("qdisc netem 8001: root refcnt 2 limit 1000 delay 20ms loss 10% seed 123\n Sent 400 bytes 10 pkt (dropped 3, overlimits 0 requeues 0)\n")
	active, dropped, err := parseNetemStats(raw)
	if err != nil || !active || dropped != 3 {
		t.Fatalf("active=%t dropped=%d err=%v", active, dropped, err)
	}
	if active, _, err := parseNetemStats([]byte("qdisc noqueue 0: root\n")); err != nil || active {
		t.Fatalf("non-netem active=%t err=%v", active, err)
	}
	for _, bad := range [][]byte{nil, []byte("qdisc netem 1: root\n(dropped nope, overlimits 0)\n"), make([]byte, maxTCOutput+1)} {
		if _, _, err := parseNetemStats(bad); err == nil {
			t.Errorf("accepted malformed tc output %q", bad)
		}
	}
}

func TestMultiSegmentDryPlanUsesOneBridgePerSegmentAndInterface(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(routedScenario))
	if err != nil {
		t.Fatal(err)
	}
	stateSandbox(t)
	var log bytes.Buffer
	backend := &netnsBackend{dry: true, log: &log}
	env, err := backend.Prepare(context.Background(), s, NewID())
	if err != nil {
		t.Fatal(err)
	}
	cleanup := env.Cleanup(context.Background(), false)
	if !cleanup.Done {
		t.Fatalf("dry cleanup = %+v", cleanup)
	}
	if got := strings.Count(log.String(), "type bridge"); got != 2 {
		t.Errorf("bridge creates = %d, want 2:\n%s", got, log.String())
	}
	if got := strings.Count(log.String(), "type veth peer name"); got != 4 {
		t.Errorf("veth creates = %d, want 4:\n%s", got, log.String())
	}
	if !strings.Contains(log.String(), "ip -4 route add default via 10.77.1.1") || !strings.Contains(log.String(), "ip -4 route add 10.77.1.0/24 via 10.77.2.1") {
		t.Errorf("route plan missing:\n%s", log.String())
	}
}

func TestDualStackDryPlanUsesOneInterfaceAndFamilyRoutes(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(dualStackScenario))
	if err != nil {
		t.Fatal(err)
	}
	stateSandbox(t)
	var log bytes.Buffer
	env, err := (&netnsBackend{dry: true, log: &log}).Prepare(context.Background(), s, NewID())
	if err != nil {
		t.Fatal(err)
	}
	defer env.Cleanup(context.Background(), false)
	plan := log.String()
	if got := strings.Count(plan, "type veth peer name"); got != 4 {
		t.Errorf("veth creates = %d, want one per logical interface (4):\n%s", got, plan)
	}
	for _, want := range []string{
		"ip -4 addr add 10.88.1.10/24",
		"ip -6 addr add fd88:1::10/64",
		"ip -4 route add default via 10.88.1.1",
		"ip -6 route add ::/0 via fd88:1::1",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("dual-stack plan missing %q:\n%s", want, plan)
		}
	}
}
