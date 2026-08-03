---
type: "query"
date: "2026-07-17T03:02:29.263282+00:00"
question: "11. w saves to cwd — installed binary run from read-only dir fails; also notice shows bare filename, user hunts for it. Fall back os.UserHomeDir(), print absolute path. Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".report()", ".handleKey()", ".noticeView()"]
---

# Q: 11. w saves to cwd — installed binary run from read-only dir fails; also notice shows bare filename, user hunts for it. Fall back os.UserHomeDir(), print absolute path. Commit when done.

## Answer

Expanded from original query via graph vocab: [report, write, model, handleviewkey, outputview]. exportReport now attempts an absolute cwd path, retries under os.UserHomeDir after a write failure, and shows the final absolute path. The focused UI test and full suite pass. Committed as 9cf13b5.

## Outcome

- Signal: useful

## Source Nodes

- .report()
- .handleKey()
- .noticeView()