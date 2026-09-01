package diagnostic

import (
	"fmt"
	"sort"
	"strings"
)

// ProbeSelection filters a built probe DAG by stable ID.
type ProbeSelection struct {
	Check map[ProbeID]struct{}
	Skip  map[ProbeID]struct{}
}

// Validate rejects IDs that are not stable nodes in any normal probe DAG.
func (s ProbeSelection) Validate() error {
	if len(s.Check) == 0 && len(s.Skip) == 0 {
		return nil
	}
	ids := selectableProbeIDs()
	known := make(map[ProbeID]struct{}, len(ids))
	valid := make([]string, len(ids))
	for i, id := range ids {
		known[id] = struct{}{}
		valid[i] = string(id)
	}
	for _, group := range []struct {
		name string
		ids  map[ProbeID]struct{}
	}{{"check", s.Check}, {"skip", s.Skip}} {
		var unknown []string
		for id := range group.ids {
			if _, ok := known[id]; !ok {
				unknown = append(unknown, string(id))
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("--%s: unknown probe ID %q; valid IDs: %s", group.name, unknown[0], strings.Join(valid, ","))
		}
	}
	return nil
}

// selectableProbeIDs derives the public inventory from the same descriptor
// builder used for runs. Building descriptors performs no probe work.
func selectableProbeIDs() []ProbeID {
	seen := map[ProbeID]struct{}{}
	var ids []ProbeID
	add := func(probes []Probe) {
		for _, p := range probes {
			if _, ok := seen[p.ID]; ok {
				continue
			}
			seen[p.ID] = struct{}{}
			ids = append(ids, p.ID)
		}
	}
	for proto := range protoNames {
		add(defaultOps.buildProbes(&Target{Host: "example.invalid", Port: 443, Proto: Proto(proto)}, DefaultPublicDNS, true))
	}
	add(defaultOps.buildProbes(nil, DefaultPublicDNS, true))
	return ids
}

// Apply returns the selected DAG in its original order. Check pulls in the
// actual dependency closure; Skip removes a node and every node that can no
// longer satisfy its dependencies.
func (s ProbeSelection) Apply(probes []Probe) []Probe {
	if len(s.Check) == 0 && len(s.Skip) == 0 {
		return probes
	}
	byID := make(map[ProbeID]Probe, len(probes))
	for _, p := range probes {
		byID[p.ID] = p
	}

	keep := make(map[ProbeID]struct{}, len(probes))
	var add func(ProbeID)
	add = func(id ProbeID) {
		if _, ok := keep[id]; ok {
			return
		}
		p, ok := byID[id]
		if !ok {
			return
		}
		keep[id] = struct{}{}
		for _, dep := range p.Deps {
			add(dep)
		}
	}
	if len(s.Check) == 0 {
		for _, p := range probes {
			keep[p.ID] = struct{}{}
		}
	} else {
		for _, p := range probes {
			if _, ok := s.Check[p.ID]; ok {
				add(p.ID)
			}
		}
	}
	for id := range s.Skip {
		delete(keep, id)
	}

	for changed := true; changed; {
		changed = false
		for _, p := range probes {
			if _, ok := keep[p.ID]; !ok {
				continue
			}
			for _, dep := range p.Deps {
				if _, ok := keep[dep]; !ok {
					delete(keep, p.ID)
					changed = true
					break
				}
			}
		}
	}
	if len(s.Check) > 0 {
		needed := make(map[ProbeID]struct{}, len(keep))
		var addNeeded func(ProbeID)
		addNeeded = func(id ProbeID) {
			if _, ok := keep[id]; !ok {
				return
			}
			if _, ok := needed[id]; ok {
				return
			}
			needed[id] = struct{}{}
			for _, dep := range byID[id].Deps {
				addNeeded(dep)
			}
		}
		for _, p := range probes {
			if _, requested := s.Check[p.ID]; requested {
				addNeeded(p.ID)
			}
		}
		keep = needed
	}

	selected := make([]Probe, 0, len(keep))
	for _, p := range probes {
		if _, ok := keep[p.ID]; ok {
			selected = append(selected, p)
		}
	}
	return selected
}
