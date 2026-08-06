//go:build linux

package diagnostic

import (
	"encoding/hex"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	procIPv4Routes = "/proc/net/route"
	procARPTable   = "/proc/net/arp"
	routeFlagUp    = 0x1
	routeFlagGW    = 0x2
)

type defaultRouteState struct {
	iface   string
	gateway net.IP
	metric  int
}

func routeFailureCause(destination net.IP) string {
	if destination.To4() == nil {
		return ""
	}
	routes, err := os.ReadFile(procIPv4Routes)
	if err != nil {
		return ""
	}
	arp, _ := os.ReadFile(procARPTable)
	return routeFailureCauseFrom(routes, arp)
}

func routeFailureCauseFrom(routeData, arpData []byte) string {
	routes := parseDefaultRoutes(routeData)
	if len(routes) == 0 {
		return RouteCauseNoDefaultRoute
	}
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].metric < routes[j].metric })
	if len(routes) > 1 {
		return RouteCausePreferredPathFailed
	}
	selected := routes[0]
	if selected.gateway != nil && arpGatewayFailed(arpData, selected.gateway.String(), selected.iface) {
		return RouteCauseGatewayUnreachable
	}
	return RouteCauseSelectedPathFailed
}

func parseDefaultRoutes(raw []byte) []defaultRouteState {
	lines := strings.Split(string(raw), "\n")
	var out []defaultRouteState
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&routeFlagUp == 0 {
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil || metric < 0 {
			continue
		}
		var gateway net.IP
		if flags&routeFlagGW != 0 {
			gateway = parseProcIPv4(fields[2])
			if gateway == nil {
				continue
			}
		}
		out = append(out, defaultRouteState{iface: fields[0], gateway: gateway, metric: metric})
	}
	return out
}

func parseProcIPv4(raw string) net.IP {
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != net.IPv4len {
		return nil
	}
	return net.IPv4(b[3], b[2], b[1], b[0])
}

func arpGatewayFailed(raw []byte, gateway, iface string) bool {
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] != gateway || fields[5] != iface {
			continue
		}
		flags, err := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 32)
		if err != nil {
			return false
		}
		return flags == 0 || fields[3] == "00:00:00:00:00:00"
	}
	return false
}
