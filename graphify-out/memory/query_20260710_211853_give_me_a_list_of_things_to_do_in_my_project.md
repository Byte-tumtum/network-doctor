---
type: "query"
date: "2026-07-10T21:18:53.362665+00:00"
question: "Give me a list of things to do in my project."
contributor: "graphify"
outcome: "useful"
source_nodes: ["Network Doctor", "runJSON()", "buildReport()", "TestReportSanitized()", "TestJobTimeout()", "TestToolsForWindows()"]
---

# Q: Give me a list of things to do in my project.

## Answer

Expanded from original query via graph vocab: tests, integration, errors, races, platform, release, fixes, sanitized, timeout, tools, diagnosis, build. Prioritized work: implement route-quality verdicts from existing mtr/pathping parsing; add injectable tests for runJSON and exportReport; cover targetTCPProbe and familyNote branches; add a coverage floor; extend Dependabot to GitHub Actions and add vulnerability scanning; make release gating match CI; design bounded concurrent tool jobs; expand platform-specific fix and localization validation. Baseline: go test, go test -race, and go vet pass; aggregate coverage is 84.1 percent.

## Outcome

- Signal: useful

## Source Nodes

- Network Doctor
- runJSON()
- buildReport()
- TestReportSanitized()
- TestJobTimeout()
- TestToolsForWindows()