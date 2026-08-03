---
type: "query"
date: "2026-07-17T02:48:52.894633+00:00"
question: "9. q dead in viewer — only esc exits (deliberate per commit), but q falls through to viewport, does nothing. Muscle memory from less/pagers expects q = back. Cheap add. Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".Update()", ".handleViewKey()", ".viewerFooter()", "TestViewerEscAndQGoBack()"]
---

# Q: 9. q dead in viewer — only esc exits (deliberate per commit), but q falls through to viewport, does nothing. Muscle memory from less/pagers expects q = back. Cheap add. Commit when done.

## Answer

Expanded from original query via graph vocab: [viewer, viewport, key, keys, quit, back, esc, update, model]. Update routes viewer key events to handleViewKey; handleViewKey handled esc but passed q to the viewport, so q had no effect. The centralized fix is to handle q with esc and advertise esc/q in viewerFooter; the existing viewer regression test covers both keys.

## Outcome

- Signal: useful

## Source Nodes

- .Update()
- .handleViewKey()
- .viewerFooter()
- TestViewerEscAndQGoBack()