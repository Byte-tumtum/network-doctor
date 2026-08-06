//go:build linux

package simulation

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestKernelInterfaceNamesAreBoundedAndCollisionFree(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		for _, prefix := range []string{"nb", "ne", "np"} {
			name := kernelLinkName(prefix, "abc123", i)
			if len(name) > 15 {
				t.Fatalf("name %q has %d bytes", name, len(name))
			}
			if seen[name] {
				t.Fatalf("duplicate name %q", name)
			}
			seen[name] = true
		}
	}
	for _, id := range []string{"", "ABC123", "abc12g", "abc1234", "abc12"} {
		if isRunID(id) {
			t.Errorf("unsafe run id %q accepted", id)
		}
	}
}

func TestParseRouteGetMapsOnlyKnownKernelInterfaces(t *testing.T) {
	np := &nodeProc{ifaces: []*interfaceProc{{logical: &Interface{Segment: "client-lan"}, iface: "neabc1230"}}}
	got, err := parseRouteGet([]byte("10.77.2.20 via 10.77.1.1 dev neabc1230 src 10.77.1.10 uid 0\n"), np)
	if err != nil {
		t.Fatal(err)
	}
	if got.Via != "10.77.1.1" || got.Source != "10.77.1.10" || got.Segment != "client-lan" {
		t.Errorf("route = %+v", got)
	}
	if _, err := parseRouteGet([]byte("10.77.2.20 via 10.77.1.1 dev neabc1230 src 10.77.1.10\n    cache\n"), np); err != nil {
		t.Errorf("kernel cache continuation rejected: %v", err)
	}
	for _, raw := range [][]byte{
		[]byte("10.77.2.20 via 10.77.1.1 dev attacker src 10.77.1.10\n"),
		[]byte("10.77.2.20 via bad dev neabc1230\n"),
		[]byte("one\ntwo\n"),
		make([]byte, maxRouteGetOutput+1),
	} {
		if _, err := parseRouteGet(raw, np); err == nil {
			t.Errorf("malformed route output accepted: %q", raw)
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

func TestMultiSegmentDryPlanUsesOneBridgePerSegmentAndInterface(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(routedScenario))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	backend := &netnsBackend{dry: true, log: &log}
	env, err := backend.Prepare(context.Background(), s, "abc123")
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
	if !strings.Contains(log.String(), "ip route add default via 10.77.1.1") || !strings.Contains(log.String(), "ip route add 10.77.1.0/24 via 10.77.2.1") {
		t.Errorf("route plan missing:\n%s", log.String())
	}
}
