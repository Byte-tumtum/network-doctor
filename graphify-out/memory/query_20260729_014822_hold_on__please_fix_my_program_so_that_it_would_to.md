---
type: "query"
date: "2026-07-29T01:48:22.316121+00:00"
question: "Hold on, please fix my program so that it would to display the advertised name of 192.168.1.79 and other devices too"
contributor: "graphify"
outcome: "useful"
source_nodes: ["ResolveNames()", ".networkHosts()", "parseAvahiNames()"]
---

# Q: Hold on, please fix my program so that it would to display the advertised name of 192.168.1.79 and other devices too

## Answer

Expanded from original query via graph vocab: [discovery, discovered, dns, dnsname, host, hostname, lan, name, network, resolved]. The LAN map routes discovered IPs through ResolveNames and networkHosts. ResolveNames now merges reverse DNS with bounded Linux DNS-SD discovery via avahi-browse, preferring advertised friendly fn and ty fields; 192.168.1.79 resolves to Living Room TV. Non-Linux builds retain reverse-DNS fallback.

## Outcome

- Signal: useful

## Source Nodes

- ResolveNames()
- .networkHosts()
- parseAvahiNames()