---
type: "query"
date: "2026-07-31T01:32:39.764626+00:00"
question: "Which reading is yours — and does no external packages include OS builtins like dig and traceroute, or only true add-ons like nmap/mtr?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["toolsFor()", "nmapTool()", "Tool", "Drill-down tools"]
---

# Q: Which reading is yours — and does no external packages include OS builtins like dig and traceroute, or only true add-ons like nmap/mtr?

## Answer

Expanded from original query via graph vocab: [tool, tools, linux, darwin, windows, nmap, dig, install, advertised, availability, command]. Choose C. No external packages means no package-manager-installed add-ons are required; it does not ban subprocesses to commands supplied by the base OS. Keep platform builtins. Replace cheap missing-package cases with Go standard library equivalents, remove mtr instead of recreating it, and only expose traceroute where the OS supplies it. Do not bundle third-party binaries or add raw-ICMP privilege requirements. The tool table is centralized in toolsFor() and nmap has its own nmapTool() definition in internal/ui/tools.go.

## Outcome

- Signal: useful

## Source Nodes

- toolsFor()
- nmapTool()
- Tool
- Drill-down tools