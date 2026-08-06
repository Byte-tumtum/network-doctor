//go:build linux

package simulation

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

const maxRouteGetOutput = 4096

func (e *netnsEnv) Evidence(ctx context.Context) (Evidence, error) {
	paths := make([]string, 0, len(e.nodes))
	for _, np := range e.nodes {
		paths = append(paths, filepath.Join(e.work, np.node.Name+"-evidence.jsonl"))
	}
	out, err := readEvidence(paths)
	if err != nil {
		return Evidence{}, err
	}
	for _, np := range e.nodes {
		for _, iface := range np.ifaces {
			res := e.Exec(ctx, np.node.Name, []string{"ip", "-o", "link", "show", "dev", iface.iface}, nil)
			if res.Err != nil || res.ExitCode != 0 {
				return Evidence{}, fmt.Errorf("inspect link %s/%s: %w", np.node.Name, iface.logical.Segment, execResultError(res))
			}
			out.Links = append(out.Links, LinkEvidence{Node: np.node.Name, Segment: iface.logical.Segment,
				Address: iface.logical.Address, Up: parseLinkUp(string(res.Stdout))})
		}
		if np.node.Role == "router" {
			raw, readErr := os.ReadFile(filepath.Join(e.work, np.node.Name+"-forwarding"))
			if readErr != nil {
				return Evidence{}, fmt.Errorf("read router %s forwarding status: %w", np.node.Name, readErr)
			}
			out.Routers = append(out.Routers, RouterEvidence{Node: np.node.Name, IPv4Forwarding: strings.TrimSpace(string(raw)) == "1"})
		}
	}

	for _, route := range e.scenario.Topology.Routes {
		np := e.byName[route.Node]
		segment, _ := nodeSegmentForAddress(np.node, netip.MustParseAddr(route.Via))
		item := RouteEvidence{Node: route.Node, Destination: route.Destination, Via: route.Via,
			Segment: segment, Metric: route.Metric}
		item.GatewayReachable = e.gatewayReachability(ctx, np, segment, route.Via)
		out.Routes = append(out.Routes, item)
	}

	for node, destinations := range e.evidenceDestinations() {
		np := e.byName[node]
		for _, destination := range destinations {
			res := e.Exec(ctx, node, []string{"ip", "route", "get", destination}, nil)
			if res.Err != nil || res.ExitCode != 0 {
				continue
			}
			selected, parseErr := parseRouteGet(res.Stdout, np)
			if parseErr != nil {
				return Evidence{}, fmt.Errorf("parse route selection for %s to %s: %w", node, destination, parseErr)
			}
			selected.Node, selected.Destination, selected.Selected = node, destination, true
			for _, configured := range e.scenario.Topology.Routes {
				if configured.Node == node && configured.Via == selected.Via {
					selected.Metric = configured.Metric
					break
				}
			}
			if selected.Via != "" {
				selected.GatewayReachable = e.gatewayReachability(ctx, np, selected.Segment, selected.Via)
			}
			out.Routes = append(out.Routes, selected)
		}
	}
	sort.Slice(out.Links, func(i, j int) bool {
		return out.Links[i].Node+out.Links[i].Segment < out.Links[j].Node+out.Links[j].Segment
	})
	sort.Slice(out.Routers, func(i, j int) bool { return out.Routers[i].Node < out.Routers[j].Node })
	sort.Slice(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i], out.Routes[j]
		return a.Node+a.Destination+a.Via+strconv.Itoa(a.Metric)+strconv.FormatBool(a.Selected) <
			b.Node+b.Destination+b.Via+strconv.Itoa(b.Metric)+strconv.FormatBool(b.Selected)
	})
	return out, nil
}

func execResultError(res ExecResult) error {
	if res.Err != nil {
		return res.Err
	}
	return fmt.Errorf("command exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

func parseLinkUp(raw string) bool {
	start, end := strings.IndexByte(raw, '<'), strings.IndexByte(raw, '>')
	if start < 0 || end <= start {
		return false
	}
	for _, flag := range strings.Split(raw[start+1:end], ",") {
		if flag == "UP" {
			return true
		}
	}
	return false
}

func parseRouteGet(raw []byte, np *nodeProc) (RouteEvidence, error) {
	if len(raw) == 0 || len(raw) > maxRouteGetOutput {
		return RouteEvidence{}, errors.New("route output is empty or oversized")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) > 2 || len(lines) == 2 && strings.TrimSpace(lines[1]) != "cache" {
		return RouteEvidence{}, errors.New("route output has an unexpected continuation")
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 3 {
		return RouteEvidence{}, errors.New("route output has too few fields")
	}
	var out RouteEvidence
	for i := 1; i < len(fields); i++ {
		switch fields[i] {
		case "via", "src", "dev":
			if i+1 >= len(fields) {
				return RouteEvidence{}, fmt.Errorf("%s is missing a value", fields[i])
			}
			value := fields[i+1]
			switch fields[i] {
			case "via":
				addr, canonical, err := parseAddr(value)
				if err != nil || !addr.Is4() {
					return RouteEvidence{}, fmt.Errorf("invalid via %q", value)
				}
				out.Via = canonical
			case "src":
				_, canonical, err := parseAddr(value)
				if err != nil {
					return RouteEvidence{}, fmt.Errorf("invalid source %q", value)
				}
				out.Source = canonical
			case "dev":
				found := false
				for _, iface := range np.ifaces {
					if iface.iface == value {
						out.Segment, found = iface.logical.Segment, true
						break
					}
				}
				if !found {
					return RouteEvidence{}, fmt.Errorf("unknown selected interface %q", value)
				}
			}
			i++
		}
	}
	if out.Segment == "" {
		return RouteEvidence{}, errors.New("route output did not name a device")
	}
	return out, nil
}

func (e *netnsEnv) gatewayReachability(ctx context.Context, np *nodeProc, segment, via string) *bool {
	iface := np.interfaceForSegment(segment)
	if iface == nil {
		return nil
	}
	res := e.Exec(ctx, np.node.Name, []string{"ip", "neigh", "show", "to", via, "dev", iface.iface}, nil)
	if res.Err != nil || res.ExitCode != 0 || strings.TrimSpace(string(res.Stdout)) == "" {
		return nil
	}
	fields := strings.Fields(string(res.Stdout))
	state := fields[len(fields)-1]
	reachable := state != "FAILED" && state != "INCOMPLETE"
	return &reachable
}

func (e *netnsEnv) evidenceDestinations() map[string][]string {
	byNode := make(map[string]map[string]bool)
	for _, test := range e.scenario.Tests {
		if byNode[test.Node] == nil {
			byNode[test.Node] = make(map[string]bool)
		}
		if test.Target == "" {
			byNode[test.Node]["1.1.1.1"] = true
			byNode[test.Node]["8.8.8.8"] = true
			continue
		}
		target, err := diagnostic.ParseTarget(test.Target)
		if err != nil {
			continue
		}
		if target.IP != nil {
			byNode[test.Node][target.IP.String()] = true
			continue
		}
		for _, node := range e.scenario.Topology.Nodes {
			for _, service := range node.Services {
				if service.Type == ServiceDNS {
					for name, raw := range service.Zone {
						if dnsKey(name) == dnsKey(target.Host) {
							byNode[test.Node][raw] = true
						}
					}
				}
			}
		}
	}
	out := make(map[string][]string, len(byNode))
	for node, set := range byNode {
		for destination := range set {
			out[node] = append(out[node], destination)
		}
		sort.Strings(out[node])
	}
	return out
}
