---
type: "query"
date: "2026-07-17T20:29:41.193022+00:00"
question: "shrink: Available() uncached fallback branch — production tools all pass through cacheAvailability, so the live-LookPath path only serves hand-built zero-value Tools in tests. Return t.available when checked, else one LookPath line; or accept as-is, it's 3 lines. [internal/ui/tools.go:32-38]"
contributor: "graphify"
outcome: "useful"
source_nodes: [".Available()", "cacheAvailability()", "Tool"]
---

# Q: shrink: Available() uncached fallback branch — production tools all pass through cacheAvailability, so the live-LookPath path only serves hand-built zero-value Tools in tests. Return t.available when checked, else one LookPath line; or accept as-is, it's 3 lines. [internal/ui/tools.go:32-38]

## Answer

Expanded from original query via graph vocab: [available, availability, cache, cached, tool, tools, path]. Verified .Available() already returns the cached bool when availabilityChecked and performs one LookPath fallback for test-built Tool values. All production toolsFor returns pass through cacheAvailability. No code change: compressing the existing three statements would only add a semicolon or helper and reduce readability.

## Outcome

- Signal: useful

## Source Nodes

- .Available()
- cacheAvailability()
- Tool