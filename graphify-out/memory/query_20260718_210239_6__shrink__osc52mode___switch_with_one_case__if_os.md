---
type: "query"
date: "2026-07-18T21:02:39.697648+00:00"
question: "6. shrink: osc52Mode — switch with one case. if os.Getenv(\"TMUX\") != \"\" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]"
contributor: "graphify"
outcome: "useful"
source_nodes: ["OSC 52 Clipboard Simplification"]
---

# Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]

## Answer

Expanded from original query via graph vocab: [osc, clipboard, report]. The requested simplification is already present in internal/ui/report.go: osc52Mode no longer exists, and osc52Sequence has one TMUX environment case followed by the default sequence. No source change was needed. Focused TestOSC52Sequence passes.

## Outcome

- Signal: useful

## Source Nodes

- OSC 52 Clipboard Simplification