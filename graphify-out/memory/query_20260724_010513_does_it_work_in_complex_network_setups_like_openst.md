---
type: "query"
date: "2026-07-24T01:05:13.344873+00:00"
question: "Does it work in complex network setups like Openstack, SDN (OvS, OpenDaylight), etc?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["BuildProbes()", "Diagnose()", ".pathIdentity()", "toolsFor()", "Network Doctor"]
---

# Q: Does it work in complex network setups like Openstack, SDN (OvS, OpenDaylight), etc?

## Answer

Expanded from original query via graph vocab: [network, diagnose, probe, route, path, interface, tcp, dns, http, proxy, lan]. Partly: Network Doctor works as an endpoint-level black-box diagnostic inside a VM, host, container, or network namespace. It tests the interface, IPv4/IPv6 TCP egress, environment proxy, DNS, target TCP, TLS, HTTP(S), and source IP/interface, and offers route, traceroute, mtr/pathping, socket, and nmap drill-downs. It does not understand or query the OpenStack/SDN control plane: there are no OpenStack, Neutron, OVS/OpenFlow, OVN, or OpenDaylight integrations. It cannot correlate a failure with security groups, virtual routers, OVS flows, tunnels, or controller intent, and overlay/underlay details may be invisible. Use it to establish what fails from a particular execution context, then use OpenStack/OVS/controller tooling to locate why. Best results come from running it at multiple vantage points.

## Outcome

- Signal: useful

## Source Nodes

- BuildProbes()
- Diagnose()
- .pathIdentity()
- toolsFor()
- Network Doctor