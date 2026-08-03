---
type: "query"
date: "2026-07-17T02:40:44.642719+00:00"
question: "Notices immortal — report copied to clipboard sits until restart. Reuse the Ctrl+C tea.Tick expiry pattern for about 4 seconds."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".Update()", ".handleKey()", "ctrlCNoticeDoneMsg", "copyReport"]
---

# Q: Notices immortal — report copied to clipboard sits until restart. Reuse the Ctrl+C tea.Tick expiry pattern for about 4 seconds.

## Answer

Expanded from original query via graph vocab: [clipboard, copy, ctrl, model, notice, report, update, clear]. internal/ui/model.go Update assigns export feedback in handleKey and already expires Ctrl+C feedback through a deadline-tagged tea.Tick message. Generalized that message, added a separate four-second export notice deadline, and cleared only matching deadlines so stale timers cannot erase newer notices. Added TestReportNoticeExpires.

## Outcome

- Signal: useful

## Source Nodes

- model
- .Update()
- .handleKey()
- ctrlCNoticeDoneMsg
- copyReport