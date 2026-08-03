---
type: "query"
date: "2026-07-25T18:39:00.856615+00:00"
question: "I just fixed an issue. Commit it. Here was the problem: Committed filter can't be cleared without leaving viewer; to clear filter while staying, you must press / then esc. No hint anywhere."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handleViewKey()", ".refreshViewport()", ".viewerFooter()", "TestEscClearsCommittedFilterBeforeClosing"]
---

# Q: I just fixed an issue. Commit it. Here was the problem: Committed filter can't be cleared without leaving viewer; to clear filter while staying, you must press / then esc. No hint anywhere.

## Answer

Expanded from original query via graph vocab: [filter, viewer, clear, key, model, input, update, help]. The fix belongs in .handleViewKey(), with .refreshViewport() after clearing and .viewerFooter() documenting the active-filter escape behavior; the regression is covered by TestEscClearsCommittedFilterBeforeClosing.

## Outcome

- Signal: useful

## Source Nodes

- .handleViewKey()
- .refreshViewport()
- .viewerFooter()
- TestEscClearsCommittedFilterBeforeClosing