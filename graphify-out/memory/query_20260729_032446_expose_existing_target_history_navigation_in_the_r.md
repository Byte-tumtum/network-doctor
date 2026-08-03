---
type: "query"
date: "2026-07-29T03:24:46.437500+00:00"
question: "Expose existing target-history navigation in the restart footer."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handlePromptKey()", ".promptView()", "helpKeys()"]
---

# Q: Expose existing target-history navigation in the restart footer.

## Answer

Expanded from original query via graph vocab: [restart, prompt, history, key, help, footer, target, view]. handlePromptKey already navigates persisted target history with Up and Down; promptView now advertises that existing behavior with an ↑/↓ history help chip alongside Enter and Esc. No history browser or new state was added.

## Outcome

- Signal: useful

## Source Nodes

- .handlePromptKey()
- .promptView()
- helpKeys()