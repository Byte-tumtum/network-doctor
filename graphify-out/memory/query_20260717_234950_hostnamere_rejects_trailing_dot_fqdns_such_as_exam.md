---
type: "query"
date: "2026-07-17T23:49:50.766119+00:00"
question: "hostnameRe rejects trailing-dot FQDNs such as example.com.; should target parsing accept them?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["ParseTarget", "Target", "TestParseTarget"]
---

# Q: hostnameRe rejects trailing-dot FQDNs such as example.com.; should target parsing accept them?

## Answer

Expanded from original query via graph vocab: [dns, host, parse, target, url, port]. ParseTarget is the shared validation boundary used by CLI and restart input. Strip one terminal DNS root dot only for hostname validation, while preserving the original spelling in Target.Host so absolute-name semantics reach probes and tools. TestParseTarget covers example.com.

## Outcome

- Signal: useful

## Source Nodes

- ParseTarget
- Target
- TestParseTarget