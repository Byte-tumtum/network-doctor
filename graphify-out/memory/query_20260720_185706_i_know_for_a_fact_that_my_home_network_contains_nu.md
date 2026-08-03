---
type: "query"
date: "2026-07-20T18:57:06.675300+00:00"
question: "I know for a fact that my home network contains numerous devices, but the network map only shows one"
contributor: "graphify"
outcome: "corrected"
correction: "The network map must show discovered LAN hosts, not the diagnostic probe dependency DAG."
source_nodes: ["model", ".handleKey()", "Tool", "nmapTool()"]
---

# Q: I know for a fact that my home network contains numerous devices, but the network map only shows one

## Answer

Expanded via graph vocab: [network, map, local, interface, route, nmap, confirm, tool, model, key, view]. The v view incorrectly rendered Probe.Deps, which describes diagnostic execution rather than LAN devices. Corrected v to confirm and run bounded unprivileged Nmap host discovery on the local private /24, then parse responding hosts into the map.

## Outcome

- Signal: corrected
- Correction: The network map must show discovered LAN hosts, not the diagnostic probe dependency DAG.

## Source Nodes

- model
- .handleKey()
- Tool
- nmapTool()