---
type: "query"
date: "2026-07-14T01:37:44.231037+00:00"
question: "Set ti.Placeholder = \"example.com:443 — empty for a general check\" in the restart prompt (model.go:312)."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".handleKey()", ".promptView()"]
---

# Q: Set ti.Placeholder = "example.com:443 — empty for a general check" in the restart prompt (model.go:312).

## Answer

Expanded from original query via graph vocab: [model, restart, prompt, target, check, start]. The restart prompt is constructed in model.handleKey in internal/ui/model.go; setting textinput.Placeholder there adds the requested empty-input guidance. Verified with go test ./internal/ui using a writable GOCACHE.

## Outcome

- Signal: useful

## Source Nodes

- model
- .handleKey()
- .promptView()