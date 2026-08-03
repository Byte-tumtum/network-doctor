---
type: "query"
date: "2026-07-10T23:41:59.533017+00:00"
question: "How are these ideas? Roadmap and known debt list; item 9 is complete."
contributor: "graphify"
outcome: "useful"
source_nodes: ["Roadmap", "mtrHops()", "routeFacts()", "routeSuspect()", ".refreshViewport()", "killGroup()", "Clean()", "windowsPingFacts()"]
---

# Q: How are these ideas? Roadmap and known debt list; item 9 is complete.

## Answer

Expanded from original query via graph vocab: roadmap, mtr, route, jobs, viewport, output, windows, process, kill, report, sanitize, ping. Assessment: item 1 is already implemented through mtrHops/pathpingHops/routeFacts and should become a README cleanup. Item 9 is complete and should be removed. Item 4 is strong actionable debt. Item 10 is worthwhile but needs real localized fixtures before choosing positional count parsing. Item 2 is a large singleton-model and UX redesign. Item 3 should be benchmark-driven because output is capped at 5000 lines and incremental rendering must handle wrapping, resizing, and eviction. Item 5 should not use a dynamic baseline from only five mtr cycles; improve configuration or evidence first. Item 6 is future-proofing because current Windows tools are documented as not spawning trees. Item 7 is useful compatibility work. Item 8 should not change Clean: current streams are split into lines first, and stripping embedded newlines protects report layout; add a separate multiline API only with a real caller.

## Outcome

- Signal: useful

## Source Nodes

- Roadmap
- mtrHops()
- routeFacts()
- routeSuspect()
- .refreshViewport()
- killGroup()
- Clean()
- windowsPingFacts()