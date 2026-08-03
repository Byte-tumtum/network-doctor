---
type: "query"
date: "2026-07-18T03:32:11.817423+00:00"
question: "Make a list of things to work on in my project."
contributor: "graphify"
outcome: "useful"
source_nodes: ["Diagnose()", "Roadmap", "Probe", "Tests", "README.md"]
---

# Q: Make a list of things to work on in my project.

## Answer

Expanded from original query via graph vocab: [roadmap, diagnosis, probe, tests, platform, release, route]. Ranked backlog: 1) Fix issue 2 so a degraded-but-functional Internet Warn cannot produce an unreachable-general-internet verdict; cover native Warn and proxy-downgraded Warn cases. 2) Resolve issue 3 with a README-only correction: remove the unsupported sudo rerun and Windows ping-fact claims, and say output rather than stdout. 3) Add focused deterministic tests for targetTCPProbe and familyNote through the existing netops seam; current coverage is 30.0% and 13.3%. 4) Triage the speculative roadmap: defer or remove mtr route parsing and concurrent tool jobs until users demonstrate need; the parser was previously added and deliberately removed during simplification. Baseline is healthy: go test, race, vet, and lint pass; total statement coverage is 85.6%.

## Outcome

- Signal: useful

## Source Nodes

- Diagnose()
- Roadmap
- Probe
- Tests
- README.md