package simulation

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const (
	legacySegmentName = "lan"
	maxRouteMetric    = int(^uint32(0) >> 1)
)

func (t *Topology) normalizeAndValidate(nodes map[string]*Node) error {
	if len(t.Segments) == 0 {
		if err := t.normalizeLegacy(); err != nil {
			return err
		}
	} else if t.Subnet != "" {
		return errors.New("topology: subnet shorthand cannot be combined with segments")
	}

	segments, err := t.validateSegments()
	if err != nil {
		return err
	}
	if err := t.validateInterfaces(segments); err != nil {
		return err
	}
	if err := t.validateRoutes(nodes); err != nil {
		return err
	}
	return nil
}

func (t *Topology) normalizeLegacy() error {
	for _, n := range t.Nodes {
		if len(n.Interfaces) != 0 {
			return errors.New("topology: node interfaces require explicit segments")
		}
	}
	if len(t.Routes) != 0 {
		return errors.New("topology: routes require explicit segments")
	}
	prefix, err := parsePrefix(t.subnetOrDefault())
	if err != nil {
		return fmt.Errorf("topology.subnet: %w", err)
	}
	if !prefix.Addr().Is4() {
		return errors.New("topology.subnet: only IPv4 segments are supported")
	}
	t.Subnet = prefix.String()
	t.Segments = []Segment{{Name: legacySegmentName, Subnet: prefix.String()}}
	for i := range t.Nodes {
		n := &t.Nodes[i]
		addr, canonical, err := parseAddr(n.Address)
		if err != nil {
			return fmt.Errorf("node %q: address: %w", n.Name, err)
		}
		if !prefix.Contains(addr) {
			return fmt.Errorf("node %q: address %s is outside %s", n.Name, addr, prefix)
		}
		n.Address = canonical
		n.Interfaces = []Interface{{Segment: legacySegmentName, Address: canonical + "/" + strconv.Itoa(prefix.Bits())}}
		if n.Gateway != "" {
			_, gateway, err := parseAddr(n.Gateway)
			if err != nil {
				return fmt.Errorf("node %q: gateway: %w", n.Name, err)
			}
			n.Gateway = gateway
			t.Routes = append(t.Routes, Route{Node: n.Name, Destination: "default", Via: gateway})
		}
	}
	return nil
}

func (t *Topology) validateSegments() (map[string]netip.Prefix, error) {
	if len(t.Segments) == 0 {
		return nil, errors.New("topology.segments: at least one segment is required")
	}
	out := make(map[string]netip.Prefix, len(t.Segments))
	for i := range t.Segments {
		segment := &t.Segments[i]
		if !isSafeName(segment.Name) {
			return nil, fmt.Errorf("topology.segments[%d].name %q must be letters, digits or dashes", i, segment.Name)
		}
		if _, exists := out[segment.Name]; exists {
			return nil, fmt.Errorf("duplicate segment name %q", segment.Name)
		}
		prefix, err := parsePrefix(segment.Subnet)
		if err != nil {
			return nil, fmt.Errorf("segment %q subnet: %w", segment.Name, err)
		}
		if !prefix.Addr().Is4() {
			return nil, fmt.Errorf("segment %q: only IPv4 segments are supported", segment.Name)
		}
		for otherName, other := range out {
			if prefixesOverlap(prefix, other) {
				return nil, fmt.Errorf("segment %q subnet %s overlaps segment %q subnet %s", segment.Name, prefix, otherName, other)
			}
		}
		segment.Subnet = prefix.String()
		out[segment.Name] = prefix
	}
	return out, nil
}

func (t *Topology) validateInterfaces(segments map[string]netip.Prefix) error {
	addresses := make(map[netip.Addr]string)
	aliases := make(map[netip.Addr]string)
	for ni := range t.Nodes {
		n := &t.Nodes[ni]
		if len(n.Interfaces) == 0 {
			return fmt.Errorf("node %q: at least one interface is required", n.Name)
		}
		if t.Subnet == "" && (n.Address != "" || n.Gateway != "") {
			return fmt.Errorf("node %q: legacy address/gateway cannot be combined with explicit interfaces", n.Name)
		}
		seenSegments := make(map[string]bool, len(n.Interfaces))
		for ii := range n.Interfaces {
			iface := &n.Interfaces[ii]
			segment, ok := segments[iface.Segment]
			if !ok {
				return fmt.Errorf("node %q interface %d: unknown segment %q", n.Name, ii, iface.Segment)
			}
			if seenSegments[iface.Segment] {
				return fmt.Errorf("node %q: duplicate interface on segment %q", n.Name, iface.Segment)
			}
			seenSegments[iface.Segment] = true
			prefix, err := parseInterfacePrefix(iface.Address)
			if err != nil {
				return fmt.Errorf("node %q interface %q: %w", n.Name, iface.Segment, err)
			}
			if prefix.Bits() != segment.Bits() || !segment.Contains(prefix.Addr()) {
				return fmt.Errorf("node %q interface address %s is outside segment %q %s", n.Name, prefix, iface.Segment, segment)
			}
			if owner, exists := addresses[prefix.Addr()]; exists {
				return fmt.Errorf("duplicate interface address %s on %s and %s", prefix.Addr(), owner, n.Name)
			}
			if owner, exists := aliases[prefix.Addr()]; exists {
				return fmt.Errorf("interface address %s conflicts with alias on %s", prefix.Addr(), owner)
			}
			addresses[prefix.Addr()] = n.Name
			iface.Address = prefix.String()
		}
		if n.Role == "router" && len(seenSegments) < 2 {
			return fmt.Errorf("router %q needs interfaces on at least two segments", n.Name)
		}
		if n.Address == "" {
			prefix, _ := netip.ParsePrefix(n.Interfaces[0].Address)
			n.Address = prefix.Addr().String()
		}
		for i, raw := range n.Aliases {
			addr, canonical, err := parseAddr(raw)
			if err != nil {
				return fmt.Errorf("node %q: alias: %w", n.Name, err)
			}
			if owner, exists := addresses[addr]; exists {
				return fmt.Errorf("node %q alias %s conflicts with interface on %s", n.Name, addr, owner)
			}
			if owner, exists := aliases[addr]; exists {
				return fmt.Errorf("duplicate alias address %s on %s and %s", addr, owner, n.Name)
			}
			aliases[addr] = n.Name
			n.Aliases[i] = canonical
		}
		if n.Resolver != "" {
			_, canonical, err := parseAddr(n.Resolver)
			if err != nil {
				return fmt.Errorf("node %q: resolver: %w", n.Name, err)
			}
			n.Resolver = canonical
		}
	}
	return nil
}

func (t *Topology) validateRoutes(nodes map[string]*Node) error {
	seen := make(map[string]Route)
	defaults := make(map[string][]Route)
	for i := range t.Routes {
		route := &t.Routes[i]
		node := nodes[route.Node]
		if node == nil {
			return fmt.Errorf("topology.routes[%d]: unknown node %q", i, route.Node)
		}
		if route.Metric < 0 || route.Metric > maxRouteMetric {
			return fmt.Errorf("topology.routes[%d]: metric %d is out of range", i, route.Metric)
		}
		via, canonicalVia, err := parseAddr(route.Via)
		if err != nil {
			return fmt.Errorf("topology.routes[%d].via: %w", i, err)
		}
		if !via.Is4() {
			return fmt.Errorf("topology.routes[%d]: only IPv4 routes are supported", i)
		}
		segment, onLink := nodeSegmentForAddress(node, via)
		if !onLink {
			return fmt.Errorf("topology.routes[%d]: gateway %s is not on a directly connected subnet for node %q", i, via, route.Node)
		}
		route.Via = canonicalVia
		route.Default = route.Destination == "default"
		if !route.Default {
			prefix, err := parsePrefix(route.Destination)
			if err != nil {
				return fmt.Errorf("topology.routes[%d].destination: %w", i, err)
			}
			if !prefix.Addr().Is4() {
				return fmt.Errorf("topology.routes[%d]: IPv4 gateway cannot be used for IPv6 destination %s", i, prefix)
			}
			route.Destination = prefix.String()
		}
		key := route.Node + "\x00" + route.Destination + "\x00" + strconv.Itoa(route.Metric)
		if previous, exists := seen[key]; exists {
			if previous.Via != route.Via {
				return fmt.Errorf("conflicting routes for node %q destination %s metric %d", route.Node, route.Destination, route.Metric)
			}
			return fmt.Errorf("duplicate route for node %q destination %s metric %d", route.Node, route.Destination, route.Metric)
		}
		seen[key] = *route
		if route.Default {
			defaults[route.Node] = append(defaults[route.Node], *route)
		}
		_ = segment
	}
	for nodeName, routes := range defaults {
		sort.Slice(routes, func(i, j int) bool { return routes[i].Metric < routes[j].Metric })
		nodes[nodeName].Gateway = routes[0].Via
	}
	return nil
}

func nodeSegmentForAddress(node *Node, addr netip.Addr) (string, bool) {
	for _, iface := range node.Interfaces {
		prefix, err := netip.ParsePrefix(iface.Address)
		if err == nil && prefix.Contains(addr) {
			return iface.Segment, true
		}
	}
	return "", false
}

func (n *Node) interfaceOn(segment string) (*Interface, bool) {
	for i := range n.Interfaces {
		if n.Interfaces[i].Segment == segment {
			return &n.Interfaces[i], true
		}
	}
	return nil, false
}

func (n *Node) addresses() []string {
	out := make([]string, 0, len(n.Interfaces)+len(n.Aliases))
	for _, iface := range n.Interfaces {
		if prefix, err := netip.ParsePrefix(iface.Address); err == nil {
			out = append(out, prefix.Addr().String())
		}
	}
	return append(out, n.Aliases...)
}

func parsePrefix(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, err
	}
	if prefix.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q: scoped addresses are not supported", raw)
	}
	return prefix.Masked(), nil
}

func parseInterfacePrefix(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, err
	}
	if prefix.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q: scoped addresses are not supported", raw)
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, errors.New("only IPv4 interfaces are supported")
	}
	return prefix, nil
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func (t *Topology) routeForNode(node, destination string) []Route {
	var out []Route
	for _, route := range t.Routes {
		if route.Node == node && route.Destination == destination {
			out = append(out, route)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return strings.Compare(out[i].Via, out[j].Via) < 0
	})
	return out
}
