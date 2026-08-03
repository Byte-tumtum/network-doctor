---
type: "query"
date: "2026-07-17T03:11:20.281761+00:00"
question: "Viewer lacks search + copy — 5000 lines, no / filter, no y copy-full-output. Copy is cheap (clipboard path exists in report.go); search is bigger, add when needed."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handleViewKey()", ".outputView()", ".viewerFooter()", "copyReport()"]
---

# Q: Viewer lacks search + copy — 5000 lines, no / filter, no y copy-full-output. Copy is cheap (clipboard path exists in report.go); search is bigger, add when needed.

## Answer

Expanded from original query via graph vocab: [viewer, viewport, output, key, handle, clipboard, copy, export, report]. The output viewer routes keys through model.handleViewKey in internal/ui/model.go; report export routes clipboard writes through copyReport in internal/ui/report.go. Reuse copyReport for y in handleViewKey and join jobLines without viewport wrapping so all retained output is copied.

## Outcome

- Signal: useful

## Source Nodes

- .handleViewKey()
- .outputView()
- .viewerFooter()
- copyReport()