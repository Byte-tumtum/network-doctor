---
type: "query"
date: "2026-07-17T20:50:49.976022+00:00"
question: "shrink: winSafe() GOOS branch — unconditional strings.ToValidUTF8(line, \"?\") is one line, consistent across platforms"
contributor: "graphify"
outcome: "useful"
source_nodes: ["winSafe()", "streamReader()"]
---

# Q: shrink: winSafe() GOOS branch — unconditional strings.ToValidUTF8(line, "?") is one line, consistent across platforms

## Answer

Expanded from original query via graph vocab: [win, safe, stream, reader, strings, jobs, line, windows, unix]. winSafe is called by streamReader for every subprocess output line. It now unconditionally returns strings.ToValidUTF8(s, "?"), making invalid UTF-8 visible consistently across platforms and removing the runtime.GOOS branch.

## Outcome

- Signal: useful

## Source Nodes

- winSafe()
- streamReader()