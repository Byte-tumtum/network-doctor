//go:build linux

package diagnostic

import "testing"

func TestRouteFailureCauseFromKernelTables(t *testing.T) {
	header := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n"
	arpHeader := "IP address HW type Flags HW address Mask Device\n"
	tests := []struct {
		name, routes, arp, want string
	}{
		{"no default", header + "eth0 00024D0A 00000000 0001 0 0 0 00FFFFFF 0 0 0\n", arpHeader, RouteCauseNoDefaultRoute},
		{"dead gateway", header + "eth0 00000000 01014D0A 0003 0 0 100 00000000 0 0 0\n",
			arpHeader + "10.77.1.1 0x1 0x0 00:00:00:00:00:00 * eth0\n", RouteCauseGatewayUnreachable},
		{"reachable selected path", header + "eth0 00000000 01014D0A 0003 0 0 100 00000000 0 0 0\n",
			arpHeader + "10.77.1.1 0x1 0x2 02:00:00:00:00:01 * eth0\n", RouteCauseSelectedPathFailed},
		{"multiple defaults", header +
			"eth1 00000000 01034D0A 0003 0 0 50 00000000 0 0 0\n" +
			"eth0 00000000 01014D0A 0003 0 0 100 00000000 0 0 0\n", arpHeader, RouteCausePreferredPathFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeFailureCauseFrom([]byte(tc.routes), []byte(tc.arp)); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDefaultRoutesRejectsMalformedRows(t *testing.T) {
	raw := []byte("Iface Destination Gateway Flags RefCnt Use Metric Mask\n" +
		"bad 00000000 nothex 0003 0 0 1 00000000\n" +
		"down 00000000 01014D0A 0002 0 0 1 00000000\n")
	if got := parseDefaultRoutes(raw); len(got) != 0 {
		t.Errorf("malformed routes = %+v", got)
	}
}
