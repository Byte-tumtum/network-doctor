# Graph Report - network-doctor  (2026-07-06)

## Corpus Check
- 57 files · ~34,673 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 528 nodes · 1118 edges · 36 communities (35 shown, 1 thin omitted)
- Extraction: 82% EXTRACTED · 18% INFERRED · 0% AMBIGUOUS · INFERRED: 202 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `02149ffd`
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
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
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
- [[_COMMUNITY_Community 28|Community 28]]
- [[_COMMUNITY_Community 29|Community 29]]
- [[_COMMUNITY_Community 30|Community 30]]
- [[_COMMUNITY_Community 31|Community 31]]
- [[_COMMUNITY_Community 32|Community 32]]
- [[_COMMUNITY_Community 33|Community 33]]
- [[_COMMUNITY_Community 34|Community 34]]
- [[_COMMUNITY_Community 35|Community 35]]

## God Nodes (most connected - your core abstractions)
1. `model` - 67 edges
2. `newModel()` - 40 edges
3. `netops` - 22 edges
4. `toolsFor()` - 22 edges
5. `extractFacts()` - 21 edges
6. `T` - 20 edges
7. `asModel()` - 20 edges
8. `T` - 20 edges
9. `New()` - 19 edges
10. `T` - 16 edges

## Surprising Connections (you probably didn't know these)
- `run()` --calls--> `SetEgressEndpoints()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `buildReport()` --calls--> `Diagnose()`  [INFERRED]
  main.go → internal/diagnostic/diagnosis.go
- `runJSON()` --calls--> `RunAll()`  [INFERRED]
  main.go → internal/diagnostic/runall.go
- `TestBuildReport()` --calls--> `ParseTarget()`  [INFERRED]
  main_test.go → internal/diagnostic/target.go
- `TestRun()` --calls--> `run()`  [INFERRED]
  main_test.go → main.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Ordered Local Diagnosis Check Sequence** — readme_link_check, readme_ip_address_check, readme_gateway_check, readme_name_resolution_check, readme_internet_check [EXTRACTED 1.00]
- **Target Host Path Probe Sequence** — readme_gateway_reachable_check, readme_dns_target_check, readme_tcp_443_check, readme_tls_handshake_check, readme_http_target_check, readme_ssh_22_check [EXTRACTED 1.00]
- **Charm TUI Stack** — readme_bubble_tea, readme_bubbles, readme_lip_gloss [EXTRACTED 1.00]

## Communities (36 total, 1 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.15
Nodes (12): Built with, Development, Drill-down tools, Exit codes, How it diagnoses, Install, JSON output, network-doctor (+4 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.08
Nodes (60): model, ProbeID, T, Model, T, KeyMsg, Model, T (+52 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.16
Nodes (25): Config, Attempt, applyDialWarnings(), BuildProbes(), defaultEgressEndpoints(), familyNote(), interleaveFamilies(), joinIPs() (+17 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.07
Nodes (31): CancelFunc, Cmd, Context, Fact, job, KeyMsg, Msg, Probe (+23 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.11
Nodes (46): Duration, Target, Fact, T, Tool, Fact, routeHop, Tool (+38 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.17
Nodes (11): errReader, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), parseDefaultRoute(), TestParseDefaultRoute(), TestParseDefaultRouteReadError(), T (+3 more)

### Community 7 - "Community 7"
Cohesion: 0.17
Nodes (16): Diagnose(), DowngradeEgress(), TestDiagnoseBannerFail(), TestDiagnoseGenericEgressNoDNS(), TestDiagnoseTargetBranches(), TestDiagnoseGeneric(), TestDiagnoseIncomplete(), TestDiagnoseProxy() (+8 more)

### Community 8 - "Community 8"
Cohesion: 0.36
Nodes (6): dnsFix(), ifaceFix(), TestDefaultRouteSmoke(), TestFixHintsPerGOOS(), TestSSIDSmoke(), T

### Community 9 - "Community 9"
Cohesion: 0.50
Nodes (3): ProbeID, Tool, fixFor()

### Community 10 - "Community 10"
Cohesion: 0.21
Nodes (11): Proto, buildReport(), Probe, ProbeID, ProbeResult, Target, runJSON(), T (+3 more)

### Community 11 - "Community 11"
Cohesion: 0.12
Nodes (31): CancelFunc, Cmd, Context, Duration, Msg, Reader, Context, job (+23 more)

### Community 12 - "Community 12"
Cohesion: 0.60
Nodes (4): TestDialIPsLoopback(), TestDialIPsRefusedLoopback(), TestPathIdentityLoopback(), T

### Community 13 - "Community 13"
Cohesion: 0.30
Nodes (11): TestBannerProbeReadTimeout(), TestDialIPsAttemptCap(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered(), TestDNSProbeErrors(), TestHTTPProbeHeaderLimit(), TestInterleaveFamilies(), TestInternetProbeFamilies() (+3 more)

### Community 14 - "Community 14"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Which LICENSE should I use?, Source Nodes

### Community 15 - "Community 15"
Cohesion: 0.19
Nodes (12): RunAll(), staticProbe(), TestRunAll(), TestRunAllDowngradesEgress(), Context, Probe, ProbeID, ProbeResult (+4 more)

### Community 16 - "Community 16"
Cohesion: 0.11
Nodes (26): proxyOps(), TestApplyDialWarnings(), TestBuildProbesProtoShapes(), TestDialIPsEmpty(), TestDialIPsRefused(), TestDialIPsSuccess(), TestDowngradeEgress(), TestEnvProxyURLFallsBackToHTTP() (+18 more)

### Community 17 - "Community 17"
Cohesion: 0.18
Nodes (10): parseDarwinRoute(), parseWindowsRoute(), TestParseDarwinRoute(), TestParseWindowsRoute(), defaultRoute(), defaultRoute(), Reader, T (+2 more)

### Community 18 - "Community 18"
Cohesion: 0.18
Nodes (10): ssid(), blockHasValue(), parseAirportSSID(), parseNetshSSID(), TestParseAirportSSID(), TestParseNetshSSID(), ssid(), Context (+2 more)

### Community 19 - "Community 19"
Cohesion: 0.30
Nodes (10): ParseTarget(), Model, Target, T, main(), run(), ExitCode(), New() (+2 more)

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

### Community 28 - "Community 28"
Cohesion: 0.53
Nodes (5): F, T, FuzzSanitize(), noControl(), TestSanitize()

### Community 29 - "Community 29"
Cohesion: 0.18
Nodes (10): FlagSet, printUsage(), report, reportAttempt, reportCheck, reportTarget, reportAttempt, reportCheck (+2 more)

### Community 30 - "Community 30"
Cohesion: 0.29
Nodes (6): iwreq, parseESSID(), ssid(), TestParseESSID(), Context, T

### Community 31 - "Community 31"
Cohesion: 0.52
Nodes (6): mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), TestRemaining(), T, Target

### Community 32 - "Community 32"
Cohesion: 0.40
Nodes (3): silentConn, fakeConn, Time

### Community 33 - "Community 33"
Cohesion: 0.67
Nodes (3): TestParseTarget(), TestParseTargetErrors(), T

### Community 34 - "Community 34"
Cohesion: 0.67
Nodes (3): T, TestReportSanitized(), TestReportVerdictPass()

### Community 35 - "Community 35"
Cohesion: 1.00
Nodes (3): Target, parsePort(), IP

## Knowledge Gaps
- **92 isolated node(s):** `github.com/mplaczek99/network-doctor`, `Interface`, `Addr`, `Config`, `Request` (+87 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `newModel()` connect `Network Checks Engine` to `TUI Model (Bubble Tea)`, `TUI Model Tests`, `Linux Route/ARP Parsing`, `Community 34`, `Community 19`?**
  _High betweenness centrality (0.207) - this node is a cross-community bridge._
- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`?**
  _High betweenness centrality (0.186) - this node is a cross-community bridge._
- **Why does `New()` connect `Community 19` to `Network Checks Engine`, `Community 34`, `TUI Model Tests`, `Community 13`, `Community 16`?**
  _High betweenness centrality (0.136) - this node is a cross-community bridge._
- **Are the 38 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 38 INFERRED edges - model-reasoned connections that need verification._
- **Are the 13 inferred relationships involving `toolsFor()` (e.g. with `.applyTarget()` and `TestToolBuildCurl()`) actually correct?**
  _`toolsFor()` has 13 INFERRED edges - model-reasoned connections that need verification._
- **Are the 13 inferred relationships involving `extractFacts()` (e.g. with `TestExtractFactsNone()` and `.Update()`) actually correct?**
  _`extractFacts()` has 13 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/mplaczek99/network-doctor`, `Interface`, `Addr` to the rest of the system?**
  _92 weakly-connected nodes found - possible documentation gaps or missing edges._