---
type: "query"
date: "2026-07-17T02:23:40.305514+00:00"
question: "3. Viewer overflows on narrow terminals — vpHeight() reserves fixed 4 rows (model.go:741), but viewer footer helpKeys wraps to 2-3 lines under ~70 cols. Total exceeds terminal, renderer cuts title. Subtract lipgloss.Height(footer) instead of constant."
contributor: "graphify"
outcome: "useful"
source_nodes: [".vpHeight()", ".outputView()", "helpKeys()"]
---

# Q: 3. Viewer overflows on narrow terminals — vpHeight() reserves fixed 4 rows (model.go:741), but viewer footer helpKeys wraps to 2-3 lines under ~70 cols. Total exceeds terminal, renderer cuts title. Subtract lipgloss.Height(footer) instead of constant.

## Answer

Expanded from original query via graph vocab: [viewer, viewport, height, footer, help, model]. The viewport is sized centrally by .vpHeight(), while .outputView() renders the helpKeys() footer. Fixed .vpHeight() to subtract lipgloss.Height of the same viewerFooter rendered by .outputView(), and added a 60-column regression assertion that the viewer stays within terminal height.

## Outcome

- Signal: useful

## Source Nodes

- .vpHeight()
- .outputView()
- helpKeys()