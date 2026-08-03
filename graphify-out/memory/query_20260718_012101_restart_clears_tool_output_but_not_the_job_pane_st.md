---
type: "query"
date: "2026-07-18T01:21:01.958915+00:00"
question: "Restart clears tool output but not the job pane status/header — stale UI"
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".jobView()", "TestRestartResets()"]
---

# Q: Restart clears tool output but not the job pane status/header — stale UI

## Answer

Expanded from original query via graph vocab: [restart, job, pane, status, name, display, duration, queued, output, enter]. The shared job-pane, help-hint, and Enter guards all treat JobQueued with no active job as absent. Resetting jobStatus to JobQueued and clearing jobName, jobDisplay, and jobDur in doRestart removes the stale pane; TestRestartResets now verifies cleared state, no rendered pane, and inert Enter.

## Outcome

- Signal: useful

## Source Nodes

- model
- .jobView()
- TestRestartResets()