---
type: "query"
date: "2026-07-17T20:26:18.347950+00:00"
question: "shrink: jobOutput() builder loop = strings.Join(m.jobLines, \"\\n\"). One line replaces ten. [internal/ui/model.go:676]"
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".outputView()", "job"]
---

# Q: shrink: jobOutput() builder loop = strings.Join(m.jobLines, "\n"). One line replaces ten. [internal/ui/model.go:676]

## Answer

Expanded from original query via graph vocab: [job, join, model, output]. The model graph and source inspection showed jobOutput feeds copyReport and jobContent/output rendering. Replaced the equivalent strings.Builder loop with strings.Join(m.jobLines, "\n"); internal/ui tests pass.

## Outcome

- Signal: useful

## Source Nodes

- model
- .outputView()
- job