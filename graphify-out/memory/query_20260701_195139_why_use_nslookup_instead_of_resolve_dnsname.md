---
type: "query"
date: "2026-07-01T19:51:39.878526+00:00"
question: "Why use nslookup instead of Resolve-DnsName?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["toolsFor", "Tool", "shellArgs", "Available"]
---

# Q: Why use nslookup instead of Resolve-DnsName?

## Answer

Expanded via graph vocab: [dns, dig, extract, facts, output, process, run, shell, target, tool, available]. nslookup is a directly executable program and fits Tool's Bin plus argv execution and Available LookPath check. Resolve-DnsName is a PowerShell cmdlet, requiring powershell.exe or pwsh, command-script construction, startup overhead, and explicit structured serialization. Resolve-DnsName is richer, but nslookup better preserves the current shell-free adapter and raw-output-first design.

## Outcome

- Signal: useful

## Source Nodes

- toolsFor
- Tool
- shellArgs
- Available