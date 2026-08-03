# Graph Report - network-doctor  (2026-07-01)

## Corpus Check
- 47 files · ~20,509 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 356 nodes · 707 edges · 25 communities (24 shown, 1 thin omitted)
- Extraction: 81% EXTRACTED · 19% INFERRED · 0% AMBIGUOUS · INFERRED: 135 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `650efed1`
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
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 23|Community 23]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 25|Community 25]]
- [[_COMMUNITY_Community 26|Community 26]]
- [[_COMMUNITY_Community 27|Community 27]]

## God Nodes (most connected - your core abstractions)
1. `model` - 48 edges
2. `newModel()` - 28 edges
3. `T` - 20 edges
4. `toolsFor()` - 16 edges
5. `asModel()` - 15 edges
6. `mustTarget()` - 15 edges
7. `dialIPs()` - 13 edges
8. `Clean()` - 13 edges
9. `ProbeResult` - 12 edges
10. `BuildProbes()` - 12 edges

## Surprising Connections (you probably didn't know these)
- `main()` --calls--> `New()`  [INFERRED]
  main.go → internal/ui/app.go
- `main()` --calls--> `ExitCode()`  [INFERRED]
  main.go → internal/ui/app.go
- `main()` --calls--> `ParseTarget()`  [INFERRED]
  main.go → internal/diagnostic/target.go
- `New()` --calls--> `newModel()`  [INFERRED]
  internal/ui/app.go → internal/ui/model.go
- `TestClassifyJob()` --calls--> `New()`  [INFERRED]
  internal/ui/model_extra_test.go → internal/ui/app.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Ordered Local Diagnosis Check Sequence** — readme_link_check, readme_ip_address_check, readme_gateway_check, readme_name_resolution_check, readme_internet_check [EXTRACTED 1.00]
- **Target Host Path Probe Sequence** — readme_gateway_reachable_check, readme_dns_target_check, readme_tcp_443_check, readme_tls_handshake_check, readme_http_target_check, readme_ssh_22_check [EXTRACTED 1.00]
- **Charm TUI Stack** — readme_bubble_tea, readme_bubbles, readme_lip_gloss [EXTRACTED 1.00]

## Communities (25 total, 1 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.17
Nodes (11): Built with, Development, Drill-down tools, Exit codes, How it diagnoses, Install, network-doctor, Platform support (+3 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.11
Nodes (43): Model, T, Target, KeyMsg, Model, T, T, Target (+35 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.16
Nodes (33): Conn, Attempt, bannerProbe(), BuildProbes(), dialIPs(), dnsProbe(), TestBuildProbesProtoShapes(), TestDialIPsEmpty() (+25 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.10
Nodes (22): Fact, CancelFunc, Cmd, Context, KeyMsg, Msg, job, Probe (+14 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.16
Nodes (28): Duration, Target, Fact, T, Tool, Fact, Tool, curlTool() (+20 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.17
Nodes (11): errReader, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), parseDefaultRoute(), TestParseDefaultRoute(), TestParseDefaultRouteReadError(), T (+3 more)

### Community 7 - "Community 7"
Cohesion: 0.21
Nodes (12): Diagnose(), TestDiagnoseBannerFail(), TestDiagnoseGenericEgressNoDNS(), TestDiagnoseTargetBranches(), TestDiagnoseGeneric(), TestDiagnoseIncomplete(), TestDiagnoseTarget(), T (+4 more)

### Community 8 - "Community 8"
Cohesion: 0.52
Nodes (6): mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), TestRemaining(), T, Target

### Community 9 - "Community 9"
Cohesion: 0.31
Nodes (14): Context, job, Msg, T, ToolDoneMsg, drain(), startHelper(), TestHelperProcess() (+6 more)

### Community 10 - "Community 10"
Cohesion: 0.12
Nodes (19): Proto, Target, ParseTarget(), TestParseTarget(), TestParseTargetErrors(), IP, T, Model (+11 more)

### Community 11 - "Community 11"
Cohesion: 0.20
Nodes (17): CancelFunc, Cmd, Context, Duration, Msg, Reader, job, classifyJob() (+9 more)

### Community 13 - "Community 13"
Cohesion: 0.53
Nodes (5): F, T, FuzzSanitize(), noControl(), TestSanitize()

### Community 16 - "Community 16"
Cohesion: 0.29
Nodes (6): iwreq, parseESSID(), ssid(), TestParseESSID(), Context, T

### Community 17 - "Community 17"
Cohesion: 0.18
Nodes (10): parseDarwinRoute(), parseWindowsRoute(), TestParseDarwinRoute(), TestParseWindowsRoute(), defaultRoute(), defaultRoute(), Reader, T (+2 more)

### Community 18 - "Community 18"
Cohesion: 0.18
Nodes (10): ssid(), blockHasValue(), parseAirportSSID(), parseNetshSSID(), TestParseAirportSSID(), TestParseNetshSSID(), ssid(), Context (+2 more)

### Community 19 - "Community 19"
Cohesion: 0.36
Nodes (6): dnsFix(), ifaceFix(), TestDefaultRouteSmoke(), TestFixHintsPerGOOS(), TestSSIDSmoke(), T

### Community 20 - "Community 20"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I would like for you to help me make an ideal prompt I can send to Fable to help me make my program cross platform., Source Nodes

### Community 21 - "Community 21"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?, Source Nodes

### Community 22 - "Community 22"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Why use nslookup instead of Resolve-DnsName?, Source Nodes

### Community 23 - "Community 23"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?, Source Nodes

### Community 24 - "Community 24"
Cohesion: 0.67
Nodes (3): defaultRoute(), ssid(), Context

### Community 25 - "Community 25"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 26 - "Community 26"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 27 - "Community 27"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

## Knowledge Gaps
- **61 isolated node(s):** `Target`, `CancelFunc`, `Duration`, `scheduleMsg`, `Context` (+56 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `newModel()` connect `Network Checks Engine` to `TUI Model (Bubble Tea)`, `Community 10`, `TUI Model Tests`, `Linux Route/ARP Parsing`?**
  _High betweenness centrality (0.283) - this node is a cross-community bridge._
- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`?**
  _High betweenness centrality (0.198) - this node is a cross-community bridge._
- **Why does `Clean()` connect `TUI Model (Bubble Tea)` to `TUI Model Tests`, `Community 11`, `Community 13`, `Community 16`, `Community 18`?**
  _High betweenness centrality (0.181) - this node is a cross-community bridge._
- **Are the 26 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 26 INFERRED edges - model-reasoned connections that need verification._
- **Are the 10 inferred relationships involving `toolsFor()` (e.g. with `TestToolBuildCurl()` and `TestToolBuildCurlScheme()`) actually correct?**
  _`toolsFor()` has 10 INFERRED edges - model-reasoned connections that need verification._
- **Are the 7 inferred relationships involving `asModel()` (e.g. with `TestDeferredQuit()` and `TestDeferredRerun()`) actually correct?**
  _`asModel()` has 7 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Target`, `CancelFunc`, `Duration` to the rest of the system?**
  _61 weakly-connected nodes found - possible documentation gaps or missing edges._