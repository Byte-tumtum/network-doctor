---
type: "query"
date: "2026-07-14T01:35:52.653930+00:00"
question: "q inside output viewer quits whole app (model.go:409). less/pager convention: q closes pager. Surprise app-exit is worse than redundant key. Make q = back in viewer, quit only from main screen."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".handleViewKey()", ".outputView()", "TestViewerQGoesBack()"]
---

# Q: q inside output viewer quits whole app (model.go:409). less/pager convention: q closes pager. Surprise app-exit is worse than redundant key. Make q = back in viewer, quit only from main screen.

## Answer

Expanded from original query via graph vocab: viewer, output, key, quit, back, model. handleViewKey now treats q like esc and closes the output viewport without returning tea.Quit; handleKey retains q-to-quit on the main screen. outputView now labels q as back. TestViewerQGoesBack covers the viewer behavior.

## Outcome

- Signal: useful

## Source Nodes

- model
- .handleViewKey()
- .outputView()
- TestViewerQGoesBack()