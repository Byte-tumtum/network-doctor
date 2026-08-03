---
type: "query"
date: "2026-08-02T23:10:24.763481+00:00"
question: "$ponytail:ponytail-audit"
contributor: "graphify"
outcome: "useful"
source_nodes: ["Config", "Tool", "BuildProbesFrom()", "model"]
---

# Q: $ponytail:ponytail-audit

## Answer

Expanded from the audit request via graph vocabulary: [wrapper, interface, config, helper, model, scheduler, command, parse, render, report, tool, build]. Repo-wide audit found four meaningful maintenance cuts: delete the completed one-time attestation rehearsal, consolidate duplicate probe-shape count tests, delete redundant tool-count coverage, and delete the negative test for a removed shortcut. One expression can also drop a redundant net.IP conversion. Estimated net reduction: about 75 lines and no dependencies.

## Outcome

- Signal: useful

## Source Nodes

- Config
- Tool
- BuildProbesFrom()
- model