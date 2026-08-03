---
type: "query"
date: "2026-07-17T02:44:14.449493+00:00"
question: "No auto-jump to failure — run finishes with fail #5, selection still on probe 0. Select first fail when allDone(); details panel then shows the thing that matters. Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".Update()", ".allDone()", ".bodyView()", "TestCompletedRunSelectsFirstFailure"]
---

# Q: No auto-jump to failure — run finishes with fail #5, selection still on probe 0. Select first fail when allDone(); details panel then shows the thing that matters. Commit when done.

## Answer

Expanded from original query via graph vocab: [done, fail, failure, finish, model, probe, selection, update]. Updated .Update() so completion runs DowngradeEgress, then selects the first remaining failed probe; .bodyView() consequently renders that failure's details. Added TestCompletedRunSelectsFirstFailure and committed as 3428578.

## Outcome

- Signal: useful

## Source Nodes

- .Update()
- .allDone()
- .bodyView()
- TestCompletedRunSelectsFirstFailure