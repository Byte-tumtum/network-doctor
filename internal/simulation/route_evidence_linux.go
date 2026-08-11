//go:build linux

package simulation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

const maxRouteGetOutput = 4096
const maxTCOutput = 4096

func (e *netnsEnv) Evidence(ctx context.Context) (Evidence, error) {
	paths := make([]string, 0, len(e.nodes))
	for _, np := range e.nodes {
		if err := np.checkEvidence(ctx); err != nil {
			return Evidence{}, fmt.Errorf("verify evidence from node %s: %w", np.node.Name, err)
		}
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
			address := iface.logical.Address
			if address == "" {
				address = iface.logical.IPv4
				if address == "" {
					address = iface.logical.IPv6
				}
			}
			out.Links = append(out.Links, LinkEvidence{Node: np.node.Name, Segment: iface.logical.Segment,
				Address: address, IPv4: iface.logical.IPv4, IPv6: iface.logical.IPv6, Up: parseLinkUp(string(res.Stdout))})
		}
		if np.node.Role == "router" {
			raw, readErr := os.ReadFile(filepath.Join(e.work, np.node.Name+"-forwarding"))
			if readErr != nil {
				return Evidence{}, fmt.Errorf("read router %s forwarding status: %w", np.node.Name, readErr)
			}
			var status forwardingStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return Evidence{}, fmt.Errorf("parse router %s forwarding status: %w", np.node.Name, err)
			}
			out.Routers = append(out.Routers, RouterEvidence{Node: np.node.Name, IPv4Forwarding: status.IPv4, IPv6Forwarding: status.IPv6})
		}
	}

	for _, route := range e.scenario.Topology.Routes {
		np := e.byName[route.Node]
		segment, _ := nodeSegmentForAddress(np.node, netip.MustParseAddr(route.Via))
		item := RouteEvidence{Node: route.Node, Destination: route.Destination, Via: route.Via,
			Segment: segment, Metric: route.Metric, Family: route.Family}
		item.GatewayReachable = e.gatewayReachability(ctx, np, segment, route.Via, route.Family)
		out.Routes = append(out.Routes, item)
	}
	for _, fault := range e.scenario.Faults {
		if fault.Type != FaultNetem && fault.Type != FaultScheduledNetem {
			continue
		}
		np := e.byName[fault.Node]
		iface := np.interfaceForSegment(fault.Segment)
		res := e.Exec(ctx, fault.Node, []string{"tc", "-s", "qdisc", "show", "dev", iface.iface}, nil)
		if res.Err != nil || res.ExitCode != 0 {
			return Evidence{}, fmt.Errorf("inspect netem %s/%s: %w", fault.Node, fault.Segment, execResultError(res))
		}
		condition, parseErr := parseNetemCondition(res.Stdout)
		if parseErr != nil {
			return Evidence{}, fmt.Errorf("parse netem %s/%s: %w", fault.Node, fault.Segment, parseErr)
		}
		condition.Node, condition.Segment = fault.Node, fault.Segment
		if fault.Seed == 0 {
			// tc reports an internal 64-bit seed even when none was requested.
			// Zero preserves the evidence contract: Seed is the configured,
			// reproducible netem seed, not tc's private default.
			condition.Seed = 0
		}
		out.PacketConditions = append(out.PacketConditions, condition)
	}

	for node, destinations := range e.evidenceDestinations() {
		np := e.byName[node]
		for _, destination := range destinations {
			addr, parseAddrErr := netip.ParseAddr(destination)
			if parseAddrErr != nil {
				continue
			}
			family, flag := addressFamily(addr), "-6"
			if addr.Is4() {
				flag = "-4"
			}
			res := e.Exec(ctx, node, []string{"ip", flag, "route", "get", destination}, nil)
			if res.Err != nil || res.ExitCode != 0 {
				continue
			}
			selected, parseErr := parseRouteGet(res.Stdout, np, family)
			if parseErr != nil {
				return Evidence{}, fmt.Errorf("parse route selection for %s to %s: %w", node, destination, parseErr)
			}
			selected.Node, selected.Destination, selected.Selected = node, destination, true
			selected.Metric = selectedRouteMetric(e.scenario.Topology.Routes, node, destination, selected.Via)
			if selected.Via != "" {
				selected.GatewayReachable = e.gatewayReachability(ctx, np, selected.Segment, selected.Via, family)
			}
			out.Routes = append(out.Routes, selected)
		}
	}
	sort.Slice(out.Links, func(i, j int) bool {
		return out.Links[i].Node+"\x00"+out.Links[i].Segment < out.Links[j].Node+"\x00"+out.Links[j].Segment
	})
	sort.Slice(out.Routers, func(i, j int) bool { return out.Routers[i].Node < out.Routers[j].Node })
	sort.Slice(out.PacketConditions, func(i, j int) bool {
		return out.PacketConditions[i].Node+"\x00"+out.PacketConditions[i].Segment <
			out.PacketConditions[j].Node+"\x00"+out.PacketConditions[j].Segment
	})
	sort.Slice(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i], out.Routes[j]
		return strings.Join([]string{a.Node, a.Destination, a.Family, a.Via, a.Segment, strconv.Itoa(a.Metric),
			strconv.FormatBool(a.Selected), a.Source}, "\x00") <
			strings.Join([]string{b.Node, b.Destination, b.Family, b.Via, b.Segment, strconv.Itoa(b.Metric),
				strconv.FormatBool(b.Selected), b.Source}, "\x00")
	})
	if err := e.observeInternetReachability(ctx, &out); err != nil {
		return Evidence{}, err
	}
	if err := e.observeControlledTargetReachability(ctx, &out); err != nil {
		return Evidence{}, err
	}
	return out, nil
}

// internetObservationTimeout bounds one simulator-owned connection attempt. A
// black-holed path spends the whole budget, so the ceiling for a run is this
// times the endpoints of every family of every test node.
const internetObservationTimeout = 2 * time.Second

// observeInternetReachability dials the controlled Internet endpoints from
// inside each node netdoc was run in, and records what it found. It runs after
// the tests, so it is a point-in-time observation of the state the run finished
// in — the same instant the route and neighbor evidence above describes.
//
// Nothing here reads a diagnosis, a verdict or a scenario expectation. That is
// the point: this evidence has to be able to contradict netdoc.
func (e *netnsEnv) observeInternetReachability(ctx context.Context, out *Evidence) error {
	seen := map[string]bool{}
	for _, test := range e.scenario.Tests {
		if seen[test.Node] {
			continue
		}
		seen[test.Node] = true
		np := e.byName[test.Node]
		if np == nil {
			continue
		}
		for _, probe := range internetFamilyProbes(np.node) {
			reachable, err := e.observeFamily(ctx, np, probe.endpoints)
			if err != nil {
				return fmt.Errorf("observe %s reachability from %s: %w", probe.family, test.Node, err)
			}
			out.Reachability = append(out.Reachability, ReachabilityEvidence{
				From: test.Node, To: probe.target, Family: probe.family,
				Via: selectedPath(out.Routes, test.Node, probe.family), Reachable: reachable,
			})
		}
	}
	return nil
}

// observeControlledTargetReachability proves a multipath scenario's alternate
// path using a literal target owned by a simulator TCP fixture. Single-path,
// hostname and arbitrary external targets remain diagnosis concerns.
func (e *netnsEnv) observeControlledTargetReachability(ctx context.Context, out *Evidence) error {
	if !hasWorkingAlternatePath(e.scenario) {
		return nil
	}
	seen := map[string]bool{}
	for _, test := range e.scenario.Tests {
		target, err := diagnostic.ParseTarget(test.Target)
		if err != nil || target.IP == nil || !e.controlledTCPPort(target.IP.String(), target.Port) {
			continue
		}
		addr := target.IP.String()
		key := test.Node + "\x00" + addr + "\x00" + strconv.Itoa(target.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		reachable, err := e.byName[test.Node].probeTCP(ctx, addr, target.Port, internetObservationTimeout)
		if err != nil {
			return fmt.Errorf("observe controlled target %s from %s: %w", target.Raw, test.Node, err)
		}
		out.Reachability = append(out.Reachability, ReachabilityEvidence{
			From: test.Node, To: netip.AddrPortFrom(netip.MustParseAddr(addr), uint16(target.Port)).String(),
			Via: selectedDestinationPath(out.Routes, test.Node, addr), Reachable: reachable,
		})
	}
	return nil
}

func (e *netnsEnv) controlledTCPPort(address string, port int) bool {
	for _, node := range e.scenario.Topology.Nodes {
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

func selectedDestinationPath(routes []RouteEvidence, node, destination string) []string {
	for _, route := range routes {
		if route.Node == node && route.Destination == destination && route.Selected {
			path := []string{route.Segment}
			if route.Via != "" {
				path = append(path, route.Via)
			}
			return path
		}
	}
	return nil
}

// observeFamily reaches a family when any one controlled endpoint answers,
// which is the same bar netdoc's happy-eyeballs dial clears — reached
// independently, by connecting, not by reading what netdoc concluded.
func (e *netnsEnv) observeFamily(ctx context.Context, np *nodeProc, endpoints []string) (bool, error) {
	for _, endpoint := range endpoints {
		reachable, err := np.probeTCP(ctx, endpoint, internetProbePort, internetObservationTimeout)
		if err != nil {
			return false, err
		}
		if reachable {
			return true, nil
		}
	}
	return false, nil
}

func parseNetemStats(raw []byte) (bool, uint64, error) {
	if len(raw) == 0 || len(raw) > maxTCOutput {
		return false, 0, errors.New("tc output is empty or oversized")
	}
	text := string(raw)
	active := strings.Contains(text, "qdisc netem ")
	if !active {
		return false, 0, nil
	}
	const marker = "(dropped "
	start := strings.Index(text, marker)
	if start < 0 {
		return true, 0, nil
	}
	start += len(marker)
	end := start
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == start || end >= len(text) || text[end] != ',' {
		return false, 0, errors.New("tc dropped counter is malformed")
	}
	dropped, err := strconv.ParseUint(text[start:end], 10, 64)
	if err != nil {
		return false, 0, fmt.Errorf("tc dropped counter: %w", err)
	}
	return true, dropped, nil
}

func parseNetemCondition(raw []byte) (PacketConditionEvidence, error) {
	active, dropped, err := parseNetemStats(raw)
	condition := PacketConditionEvidence{Active: active, DroppedPackets: dropped}
	if err != nil || !active {
		return condition, err
	}
	fields := strings.Fields(netemParameters(raw))
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "delay":
			if i+1 >= len(fields) {
				return PacketConditionEvidence{}, errors.New("tc netem delay is missing a value")
			}
			condition.Latency, err = time.ParseDuration(fields[i+1])
			if err != nil {
				return PacketConditionEvidence{}, fmt.Errorf("tc netem delay: %w", err)
			}
			if i+2 < len(fields) {
				if jitter, parseErr := time.ParseDuration(fields[i+2]); parseErr == nil {
					condition.Jitter = jitter
				}
			}
		case "loss":
			value := i + 1
			if value < len(fields) && fields[value] == "random" {
				value++
			}
			if value >= len(fields) {
				return PacketConditionEvidence{}, errors.New("tc netem loss is missing a value")
			}
			rawLoss, ok := strings.CutSuffix(fields[value], "%")
			if !ok {
				return PacketConditionEvidence{}, fmt.Errorf("tc netem loss %q is malformed", fields[value])
			}
			condition.LossPercent, err = strconv.ParseFloat(rawLoss, 64)
			if err != nil {
				return PacketConditionEvidence{}, fmt.Errorf("tc netem loss: %w", err)
			}
		case "seed":
			if i+1 >= len(fields) {
				return PacketConditionEvidence{}, errors.New("tc netem seed is missing a value")
			}
			seed, parseErr := strconv.ParseUint(fields[i+1], 10, 64)
			if parseErr != nil {
				return PacketConditionEvidence{}, fmt.Errorf("tc netem seed: %w", parseErr)
			}
			if seed <= uint64(^uint32(0)) {
				condition.Seed = uint32(seed)
			}
		}
	}
	return condition, nil
}

func selectedRouteMetric(routes []Route, node, destination, via string) int {
	addr, err := netip.ParseAddr(destination)
	if err != nil {
		return 0
	}
	bestBits, metric := -1, 0
	for _, configured := range routes {
		if configured.Node != node || configured.Via != via || configured.Family != addressFamily(addr) {
			continue
		}
		bits := 0
		if configured.Destination != "default" {
			prefix, parseErr := netip.ParsePrefix(configured.Destination)
			if parseErr != nil || !prefix.Contains(addr) {
				continue
			}
			bits = prefix.Bits()
		}
		if bits > bestBits {
			bestBits, metric = bits, configured.Metric
		}
	}
	return metric
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

func parseRouteGet(raw []byte, np *nodeProc, family string) (RouteEvidence, error) {
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
	out := RouteEvidence{Family: family}
	for i := 1; i < len(fields); i++ {
		switch fields[i] {
		case "via", "src", "dev", "metric":
			if i+1 >= len(fields) {
				return RouteEvidence{}, fmt.Errorf("%s is missing a value", fields[i])
			}
			value := fields[i+1]
			switch fields[i] {
			case "via":
				addr, canonical, err := parseAddr(value)
				if err != nil || addressFamily(addr) != family {
					return RouteEvidence{}, fmt.Errorf("invalid via %q", value)
				}
				out.Via = canonical
			case "src":
				addr, canonical, err := parseAddr(value)
				if err != nil || addressFamily(addr) != family {
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
			case "metric":
				metric, err := strconv.Atoi(value)
				if err != nil || metric < 0 || metric > maxRouteMetric {
					return RouteEvidence{}, fmt.Errorf("invalid metric %q", value)
				}
				out.Metric = metric
			}
			i++
		}
	}
	if out.Segment == "" {
		return RouteEvidence{}, errors.New("route output did not name a device")
	}
	return out, nil
}

func (e *netnsEnv) gatewayReachability(ctx context.Context, np *nodeProc, segment, via, family string) *bool {
	iface := np.interfaceForSegment(segment)
	if iface == nil {
		return nil
	}
	flag := "-4"
	if family == "ipv6" {
		flag = "-6"
	}
	res := e.Exec(ctx, np.node.Name, []string{"ip", flag, "neigh", "show", "to", via, "dev", iface.iface}, nil)
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
		for _, endpoint := range append(append([]string{}, internetEndpoints4...), internetEndpoints6...) {
			byNode[test.Node][endpoint] = true
		}
		if test.Target == "" {
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
					for _, record := range service.Records {
						if dnsKey(record.Name) == dnsKey(target.Host) {
							byNode[test.Node][record.Address] = true
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
