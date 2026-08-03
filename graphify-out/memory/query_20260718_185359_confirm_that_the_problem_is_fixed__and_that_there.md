---
type: "query"
date: "2026-07-18T18:53:59.108370+00:00"
question: "Confirm that the problem is fixed, and that there is no new issue because of the fix."
contributor: "graphify"
outcome: "useful"
source_nodes: ["Diagnose()", "DowngradeEgress()", "Status"]
---

# Q: Confirm that the problem is fixed, and that there is no new issue because of the fix.

## Answer

Expanded from graph vocabulary: degraded, diagnose, diagnosis, direct, downgrade, egress, generic, internet, proxy, result, status, target, unreachable, warn. Commit 9ba63b1 fixes the reported native-Warn target verdict. Review confirmed that DowngradeEgress provenance survives both RunAll/headless and UI map-copy paths, preserving proxy-only verdicts. Focused diagnosis tests, full tests, race tests, vet, CGO-disabled builds, Darwin and Windows cross-builds, and golangci-lint all pass. No regression was found in the affected behavior.

## Outcome

- Signal: useful

## Source Nodes

- Diagnose()
- DowngradeEgress()
- Status