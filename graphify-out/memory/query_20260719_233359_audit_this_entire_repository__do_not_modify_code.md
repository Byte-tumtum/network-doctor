---
type: "query"
date: "2026-07-19T23:33:59.235928+00:00"
question: "Audit this entire repository. Do not modify code."
contributor: "graphify"
outcome: "useful"
source_nodes: [".httpProbe()", ".proxyProbe()", "Diagnose()", "DowngradeEgress()", "exportReport()", "parseRunArgs()", "README.md"]
---

# Q: Audit this entire repository. Do not modify code.

## Answer

Expanded from original query via graph vocab: [model, update, command, cancel, timeout, parse, target, diagnose, probe, windows, sanitize, json]. Confirmed defects: the custom HTTP transport disables HTTP/2 and falsely fails HTTP/2-only HTTPS endpoints; malformed empty-host proxy URLs are dialed and misclassified; Diagnose ignores a working independent HTTP branch and mislabels proxy-only plus broken DNS; report notices render raw path escapes; report filenames collide within one second; Windows .exe command names are rejected in restart parsing; README still claims removed route parsing. Source remained unchanged.

## Outcome

- Signal: useful

## Source Nodes

- .httpProbe()
- .proxyProbe()
- Diagnose()
- DowngradeEgress()
- exportReport()
- parseRunArgs()
- README.md