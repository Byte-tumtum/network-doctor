---
type: "query"
date: "2026-07-14T01:45:40.076417+00:00"
question: "8. No home/end in viewer — only arrows + pgup/pgdn (model.go:338). Add home/end bindings; long tool output needs jump-to-top."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handleViewKey()", ".outputView()", "TestViewportFollow"]
---

# Q: 8. No home/end in viewer — only arrows + pgup/pgdn (model.go:338). Add home/end bindings; long tool output needs jump-to-top.

## Answer

Expanded from original query via graph vocab: [viewer, viewport, output, model, handle, key]. Added Home and End handling in model.handleViewKey: Home calls viewport.GotoTop and pauses follow; End calls viewport.GotoBottom and resumes follow. Updated outputView help and extended TestViewportFollow. go vet ./..., go build ./..., and go test ./... pass.

## Outcome

- Signal: useful

## Source Nodes

- .handleViewKey()
- .outputView()
- TestViewportFollow