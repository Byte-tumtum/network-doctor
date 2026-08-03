---
type: "query"
date: "2026-07-10T21:19:09.674579+00:00"
question: "Give me a list of things to do in my project."
contributor: "graphify"
outcome: "corrected"
correction: "Mark route-quality parsing as implemented, update the README roadmap, and scope future work to improving the naive suspect-hop attribution heuristic."
source_nodes: ["Network Doctor", "mtrHops()", "routeFacts()", "routeSuspect()", "runJSON()"]
---

# Q: Give me a list of things to do in my project.

## Answer

Correction: mtr/pathping route-quality parsing is already implemented in internal/ui/tools.go through mtrHops, pathpingHops, routeFacts, and routeSuspect. README.md line 264 is stale. The actionable work is to update the roadmap and optionally improve the documented naive loss-origin attribution heuristic. Other priorities remain tests for runJSON, exportReport, targetTCPProbe, and familyNote; coverage gating; dependency and release automation; and bounded concurrent tool jobs.

## Outcome

- Signal: corrected
- Correction: Mark route-quality parsing as implemented, update the README roadmap, and scope future work to improving the naive suspect-hop attribution heuristic.

## Source Nodes

- Network Doctor
- mtrHops()
- routeFacts()
- routeSuspect()
- runJSON()