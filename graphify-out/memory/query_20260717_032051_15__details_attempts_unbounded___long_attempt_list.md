---
type: "query"
date: "2026-07-17T03:20:51.717004+00:00"
question: "15. Details attempts unbounded — long attempt list can push top region past terminal height on short screens; body has no height clamp, masthead gets cut. Edge case, clamp when it bites. Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".View()", ".bodyView()", "TestViewClampsLongDetailsToTerminal()"]
---

# Q: 15. Details attempts unbounded — long attempt list can push top region past terminal height on short screens; body has no height clamp, masthead gets cut. Edge case, clamp when it bites. Commit when done.

## Answer

Expanded from original query via graph vocab: [attempt, body, height, terminal, view, viewport, render, output]. View now clamps the rendered body only when fixed content exceeds the known terminal height, preserving the masthead; TestViewClampsLongDetailsToTerminal covers a realistic 16-attempt result on a 20-row terminal. Committed as 6109645.

## Outcome

- Signal: useful

## Source Nodes

- .View()
- .bodyView()
- TestViewClampsLongDetailsToTerminal()