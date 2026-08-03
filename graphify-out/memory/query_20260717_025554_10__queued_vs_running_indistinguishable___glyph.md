---
type: "query"
date: "2026-07-17T02:55:54.794623+00:00"
question: "10. Queued vs running indistinguishable — glyph() shows spinner for every unfinished probe, including deps-blocked ones not started. You have started map: faint · for queued, spinner only for in-flight. Makes the dependency chain visible. Commit when done."
contributor: "graphify"
outcome: "useful"
source_nodes: [".glyph()", "model", ".spinnerActive()"]
---

# Q: 10. Queued vs running indistinguishable — glyph() shows spinner for every unfinished probe, including deps-blocked ones not started. You have started map: faint · for queued, spinner only for in-flight. Makes the dependency chain visible. Commit when done.

## Answer

Expanded from original query via graph vocab: [glyph, probe, spinner, model, update]. The shared model.glyph renderer now checks model.started when a probe has no result: not-started probes render a faint middle dot, while started probes retain the spinner; completed probes retain their status glyph. TestGlyph covers queued, running, and completed states.

## Outcome

- Signal: useful

## Source Nodes

- .glyph()
- model
- .spinnerActive()