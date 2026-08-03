---
type: "query"
date: "2026-07-01T20:01:11.761207+00:00"
question: "Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["Install", "README.md", "main"]
---

# Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?

## Answer

Expanded via graph vocab: [build, code, install, linux, main, plan, readme]. Yes. The repository documents go install as its installation path, while release packaging has no existing coupling to runtime architecture. Keep GoReleaser and prebuilt releases separate, but retain cross-platform compile verification or a native OS CI matrix in the portability plan; verification is part of making the code portable, not distribution.

## Outcome

- Signal: useful

## Source Nodes

- Install
- README.md
- main