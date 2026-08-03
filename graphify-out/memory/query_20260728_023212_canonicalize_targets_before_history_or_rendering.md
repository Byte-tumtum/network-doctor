---
type: "query"
date: "2026-07-28T02:32:12.153034+00:00"
question: "Canonicalize targets before history or rendering"
contributor: "graphify"
outcome: "useful"
source_nodes: ["ParseTarget()", "Target", "New()", ".handleKey()"]
---

# Q: Canonicalize targets before history or rendering

## Answer

Expanded from original query via graph vocab: [target, parse, history, render, valid, url, host, port, model]. ParseTarget stores Target.Raw only after endpoint validation and excludes ignored path, query, fragment, and suffix control characters. New persists Raw and handleKey renders Raw, so both consumers receive the validated endpoint spelling.

## Outcome

- Signal: useful

## Source Nodes

- ParseTarget()
- Target
- New()
- .handleKey()