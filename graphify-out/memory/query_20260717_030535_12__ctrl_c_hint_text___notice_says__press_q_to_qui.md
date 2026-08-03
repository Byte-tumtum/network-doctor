---
type: "query"
date: "2026-07-17T03:05:35.100872+00:00"
question: "12. Ctrl+C hint text — notice says \"Press q to quit\" but second Ctrl+C inside 2s window also quits. Convention: \"Press Ctrl+C again (or q) to quit\". Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".handleKey()", ".noticeView()", "TestCtrlCWarnsThenQuits()"]
---

# Q: 12. Ctrl+C hint text — notice says "Press q to quit" but second Ctrl+C inside 2s window also quits. Convention: "Press Ctrl+C again (or q) to quit". Commit when done.

## Answer

Expanded from original query via graph vocab: [ctrl, notice, quit, quits, warns, hints, key, handle]. The shared ctrlCNotice constant in internal/ui/model.go controls noticeView; TestCtrlCWarnsThenQuits covers display, expiry, and second-press quit. Updated the constant and its assertions.

## Outcome

- Signal: useful

## Source Nodes

- .handleKey()
- .noticeView()
- TestCtrlCWarnsThenQuits()