---
type: "query"
date: "2026-07-12T22:17:37.253727+00:00"
question: "Promote OSC 52 and remove clipboard direct dependency; inline fallback verdict and one-caller helpers remaining, jobTailN, envProxyURL, and skipResult"
contributor: "graphify"
outcome: "useful"
source_nodes: ["Diagnose()", ".proxyProbe()", ".scheduleStep()", ".jobView()", "report"]
---

# Q: Promote OSC 52 and remove clipboard direct dependency; inline fallback verdict and one-caller helpers remaining, jobTailN, envProxyURL, and skipResult

## Answer

Expanded from original query via graph vocab: [diagnose, verdict, report, proxy, probe, deadline, remaining, schedule, step, job, tail, model]. Removed the direct atotto clipboard use and made OSC 52 the report copy path; atotto remains indirect because bubbles/textinput imports it. Diagnose now returns the completed target success verdict, allowing empty-summary fallback branches to be removed. Inlined the context deadline, proxy scheme lookup, skipped ProbeResult, and adaptive job tail calculation. go test ./..., go vet ./..., and go build ./... pass.

## Outcome

- Signal: useful

## Source Nodes

- Diagnose()
- .proxyProbe()
- .scheduleStep()
- .jobView()
- report