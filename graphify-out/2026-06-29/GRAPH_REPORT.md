# Graph Report - network-doctor  (2026-06-29)

## Corpus Check
- 34 files · ~18,523 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 293 nodes · 594 edges · 17 communities (16 shown, 1 thin omitted)
- Extraction: 81% EXTRACTED · 19% INFERRED · 0% AMBIGUOUS · INFERRED: 111 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `be1da64e`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Diagnostic Concepts & Docs|Diagnostic Concepts & Docs]]
- [[_COMMUNITY_Network Checks Engine|Network Checks Engine]]
- [[_COMMUNITY_TUI Model (Bubble Tea)|TUI Model (Bubble Tea)]]
- [[_COMMUNITY_TUI Model Tests|TUI Model Tests]]
- [[_COMMUNITY_Linux RouteARP Parsing|Linux Route/ARP Parsing]]
- [[_COMMUNITY_Main Entry & Target Tests|Main Entry & Target Tests]]
- [[_COMMUNITY_Go Module|Go Module]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]

## God Nodes (most connected - your core abstractions)
1. `model` - 41 edges
2. `newModel()` - 30 edges
3. `T` - 20 edges
4. `asModel()` - 15 edges
5. `ProbeResult` - 13 edges
6. `dialIPs()` - 13 edges
7. `BuildProbes()` - 12 edges
8. `Diagnose()` - 12 edges
9. `toolsFor()` - 12 edges
10. `ProbeID` - 10 edges

## Surprising Connections (you probably didn't know these)
- `main()` --calls--> `ParseTarget()`  [INFERRED]
  main.go → internal/diagnostic/target.go
- `main()` --calls--> `New()`  [INFERRED]
  main.go → internal/ui/app.go
- `main()` --calls--> `ExitCode()`  [INFERRED]
  main.go → internal/ui/app.go
- `TestBuildProbesNamesProtocolApplicationRow()` --calls--> `BuildProbes()`  [INFERRED]
  internal/diagnostic/checks_test.go → internal/diagnostic/checks.go
- `TestBuildProbesShape()` --calls--> `BuildProbes()`  [INFERRED]
  internal/diagnostic/checks_test.go → internal/diagnostic/checks.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Ordered Local Diagnosis Check Sequence** — readme_link_check, readme_ip_address_check, readme_gateway_check, readme_name_resolution_check, readme_internet_check [EXTRACTED 1.00]
- **Target Host Path Probe Sequence** — readme_gateway_reachable_check, readme_dns_target_check, readme_tcp_443_check, readme_tls_handshake_check, readme_http_target_check, readme_ssh_22_check [EXTRACTED 1.00]
- **Charm TUI Stack** — readme_bubble_tea, readme_bubbles, readme_lip_gloss [EXTRACTED 1.00]

## Communities (17 total, 1 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.18
Nodes (10): Built with, Development, Drill-down tools, Exit codes, How it diagnoses, Install, network-doctor, Roadmap (+2 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.13
Nodes (37): Model, T, Target, KeyMsg, Model, T, JobStatus, asModelP() (+29 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.16
Nodes (33): Conn, Attempt, bannerProbe(), BuildProbes(), dialIPs(), dnsProbe(), TestBuildProbesProtoShapes(), TestDialIPsEmpty() (+25 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.12
Nodes (22): CancelFunc, Cmd, Context, Fact, job, KeyMsg, Msg, ProbeID (+14 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.17
Nodes (19): T, Target, Fact, T, Fact, Tool, TestToolboxExitZero(), TestToolHotkeysUnique() (+11 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.19
Nodes (10): errReader, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), parseDefaultRoute(), TestParseDefaultRoute(), TestParseDefaultRouteReadError(), T (+2 more)

### Community 7 - "Community 7"
Cohesion: 0.21
Nodes (12): Diagnose(), TestDiagnoseBannerFail(), TestDiagnoseGenericEgressNoDNS(), TestDiagnoseTargetBranches(), TestDiagnoseGeneric(), TestDiagnoseIncomplete(), TestDiagnoseTarget(), T (+4 more)

### Community 8 - "Community 8"
Cohesion: 0.17
Nodes (11): Approach (one candidate; its own commit, fully gated), Candidate 1 — `staticTool` helper in `internal/ui/tools.go`, Done, Frozen behavior contract (must stay byte-identical / user-observable), Goal, Key decisions & tradeoffs, Out of scope / deferred, Plan: KISS simplification pass on network-doctor (+3 more)

### Community 9 - "Community 9"
Cohesion: 0.20
Nodes (9): Claude's response, Claude's response, Claude's response, Claude's response, Plan Review Log: KISS simplification pass on network-doctor, Round 1 — Codex, Round 2 — Codex, Round 3 — Codex (+1 more)

### Community 10 - "Community 10"
Cohesion: 0.12
Nodes (21): Proto, Target, ParseTarget(), TestParseTarget(), TestParseTargetErrors(), IP, T, Model (+13 more)

### Community 11 - "Community 11"
Cohesion: 0.22
Nodes (16): CancelFunc, Cmd, Context, Msg, Reader, job, classifyJob(), killGroup() (+8 more)

### Community 12 - "Community 12"
Cohesion: 0.33
Nodes (13): Context, job, Msg, T, ToolDoneMsg, drain(), startHelper(), TestHelperProcess() (+5 more)

### Community 13 - "Community 13"
Cohesion: 0.52
Nodes (6): mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), TestRemaining(), T, Target

### Community 15 - "Community 15"
Cohesion: 0.53
Nodes (5): F, T, FuzzSanitize(), noControl(), TestSanitize()

### Community 16 - "Community 16"
Cohesion: 0.14
Nodes (16): iwreq, parseESSID(), ssid(), TestParseESSID(), T, model, T, Status (+8 more)

## Knowledge Gaps
- **54 isolated node(s):** `github.com/mplaczek99/network-doctor`, `Target`, `Target`, `Target`, `ProbeID` (+49 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `newModel()` connect `Network Checks Engine` to `TUI Model (Bubble Tea)`, `TUI Model Tests`, `Linux Route/ARP Parsing`, `Community 10`, `Community 16`?**
  _High betweenness centrality (0.440) - this node is a cross-community bridge._
- **Why does `model` connect `TUI Model Tests` to `Community 16`, `Network Checks Engine`?**
  _High betweenness centrality (0.283) - this node is a cross-community bridge._
- **Why does `BuildProbes()` connect `TUI Model (Bubble Tea)` to `Network Checks Engine`, `Community 13`?**
  _High betweenness centrality (0.251) - this node is a cross-community bridge._
- **Are the 28 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 28 INFERRED edges - model-reasoned connections that need verification._
- **Are the 7 inferred relationships involving `asModel()` (e.g. with `TestDeferredQuit()` and `TestDeferredRerun()`) actually correct?**
  _`asModel()` has 7 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/mplaczek99/network-doctor`, `Target`, `Target` to the rest of the system?**
  _54 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Network Checks Engine` be split into smaller, more focused modules?**
  _Cohesion score 0.1282051282051282 - nodes in this community are weakly interconnected._