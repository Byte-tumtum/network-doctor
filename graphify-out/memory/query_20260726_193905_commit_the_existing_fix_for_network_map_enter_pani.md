---
type: "query"
date: "2026-07-26T19:39:05.311601+00:00"
question: "Commit the existing fix for network map Enter panic after the host list shrinks; do not edit Go files."
contributor: "graphify"
outcome: "useful"
source_nodes: ["model", ".handleKey()", ".networkHosts()", "TestNetworkMapEnterClampsAfterShrink()"]
---

# Q: Commit the existing fix for network map Enter panic after the host list shrinks; do not edit Go files.

## Answer

Expanded from original query via graph vocabulary: network map enter host hosts select shrink clamp handle key model test. The fix belongs in model.handleKey immediately before indexing the fresh networkHosts slice; TestNetworkMapEnterClampsAfterShrink covers a stale mapSelected after the list shrinks. Validation passed.

## Outcome

- Signal: useful

## Source Nodes

- model
- .handleKey()
- .networkHosts()
- TestNetworkMapEnterClampsAfterShrink()