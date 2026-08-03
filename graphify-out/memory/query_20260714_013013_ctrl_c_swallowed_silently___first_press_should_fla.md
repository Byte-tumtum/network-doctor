---
type: "query"
date: "2026-07-14T01:30:13.358841+00:00"
question: "Ctrl+C swallowed silently — first press should flash “Press q to quit”, second within ~2s quits, and Ctrl+C should cancel the confirm gate."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".Update()", ".handleConfirmKey()", "TestCtrlCWarnsThenQuits()"]
---

# Q: Ctrl+C swallowed silently — first press should flash “Press q to quit”, second within ~2s quits, and Ctrl+C should cancel the confirm gate.

## Answer

Expanded from original query via graph vocab: [ctrl, key, model, quit, confirm, cancel, update]. Updated model.Update so Ctrl+C cancels an active confirm gate, otherwise the first press shows a two-second warning and the second routes through the existing q quit path. Added focused tests for the warning/quit gesture and confirm cancellation.

## Outcome

- Signal: useful

## Source Nodes

- model
- .Update()
- .handleConfirmKey()
- TestCtrlCWarnsThenQuits()