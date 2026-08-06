//go:build linux

package simulation

import "testing"

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
