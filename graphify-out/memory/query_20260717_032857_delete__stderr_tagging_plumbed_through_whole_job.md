---
type: "query"
date: "2026-07-17T03:28:57.754720+00:00"
question: "- delete: stderr tagging plumbed through whole job pipeline, never used — ToolOutputMsg.Stderr, streamReader stderr param, outLine.stderr, and identity wrapper renderJobLine (returns ln.text, 3 call sites). Replacement: jobLines []string, inline .text. ~-25 lines + test churn. [internal/ui/jobs.go:43, internal/ui/model.go:65,737]"
contributor: "graphify"
outcome: "useful"
source_nodes: ["ToolOutputMsg", "streamReader", "outLine", "appendJobLine", "renderJobLine", "model"]
---

# Q: - delete: stderr tagging plumbed through whole job pipeline, never used — ToolOutputMsg.Stderr, streamReader stderr param, outLine.stderr, and identity wrapper renderJobLine (returns ln.text, 3 call sites). Replacement: jobLines []string, inline .text. ~-25 lines + test churn. [internal/ui/jobs.go:43, internal/ui/model.go:65,737]

## Answer

Expanded from original query via graph vocab: [job, jobs, line, model, output, reader, render, stderr, stream, tool]. The graph identified ToolOutputMsg, streamReader, outLine, appendJobLine, renderJobLine, report, and their tests. Live rg verification confirmed stderr metadata was never rendered differently. Replaced jobLines with []string, removed ToolOutputMsg.Stderr and the streamReader stderr parameter, inlined string rendering, and updated/deleted obsolete tests. go test ./..., go vet ./..., go build ./..., and go test -race ./internal/ui pass.

## Outcome

- Signal: useful

## Source Nodes

- ToolOutputMsg
- streamReader
- outLine
- appendJobLine
- renderJobLine
- model