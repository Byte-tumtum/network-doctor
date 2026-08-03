---
type: "query"
date: "2026-07-18T18:50:00.138939+00:00"
question: "Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2"
contributor: "graphify"
outcome: "useful"
source_nodes: ["Diagnose()", "DowngradeEgress()", "Status"]
---

# Q: Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2

## Answer

Expanded from original query via graph vocabulary: diagnose, diagnosis, egress, general, internet, probe, result, status, unreachable. Diagnose treated only StatusPass as direct connectivity in target failure branches. Native StatusWarn is functional, while DowngradeEgress can also create Warn from a true direct-egress failure. The fix records that downgrade internally, treats native Warn as reachable, and preserves proxy-only wording for downgraded failures.

## Outcome

- Signal: useful

## Source Nodes

- Diagnose()
- DowngradeEgress()
- Status