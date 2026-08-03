# Graph Report - network-doctor  (2026-07-13)

## Corpus Check
- 65 files · ~35,973 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 530 nodes · 986 edges · 68 communities (30 shown, 38 thin omitted)
- Extraction: 92% EXTRACTED · 8% INFERRED · 0% AMBIGUOUS · INFERRED: 77 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `042477b9`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Diagnostic Concepts & Docs
- Network Checks Engine
- TUI Model (Bubble Tea)
- TUI Model Tests
- Linux Route/ARP Parsing
- Main Entry & Target Tests
- Go Module
- Community 7
- Community 8
- Community 9
- Community 10
- Q: I would like for you to help me make an ideal prompt I can send to Fable to help me make my program cross platform.
- Community 12
- Community 13
- Community 14
- Community 15
- Community 16
- Community 17
- Community 18
- Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?
- Community 20
- Addr
- report.go
- CancelFunc
- Community 24
- Community 25
- Cmd
- Community 27
- Context
- Duration
- Msg
- Community 31
- Time
- Model
- T
- parseESSID
- TestToolboxExitZero
- T
- Time
- TestToolboxExitZero
- Model
- Probe
- ProbeID
- ProbeResult
- Target
- Tool
- Probe
- ProbeID
- ProbeResult
- Probe
- ProbeID
- ProbeResult
- report.go
- Addr
- Conn
- Time
- job
- Model
- ProbeResult
- T
- Stream
- Probe
- ProbeID
- github.com/heymaikol/network-doctor
- ProbeResult
- Target
- T
- T

## God Nodes (most connected - your core abstractions)
1. `model` - 58 edges
2. `netops` - 22 edges
3. `asModel()` - 20 edges
4. `newModel()` - 20 edges
5. `toolsFor()` - 17 edges
6. `ProbeResult` - 17 edges
7. `ProbeID` - 16 edges
8. `keyMsg()` - 13 edges
9. `Tool` - 12 edges
10. `Diagnose()` - 12 edges

## Surprising Connections (you probably didn't know these)
- `TestRun()` --calls--> `run()`  [INFERRED]
  main_test.go → main.go
- `TestPrintUsageTargetForms()` --calls--> `printUsage()`  [INFERRED]
  main_test.go → main.go
- `TestBuildReport()` --calls--> `buildReport()`  [INFERRED]
  main_test.go → main.go
- `TestBuildReportGenericAllPass()` --calls--> `buildReport()`  [INFERRED]
  main_test.go → main.go
- `TestClassifyJob()` --calls--> `New()`  [INFERRED]
  internal/ui/model_extra_test.go → internal/ui/model.go

## Import Cycles
- None detected.

## Communities (68 total, 38 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.12
Nodes (15): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+7 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.09
Nodes (52): asModelP(), containsEnv(), TestAppendJobLine(), TestClassifyJob(), TestDeferredQuit(), TestDeferredRestart(), TestDeferredRestartDefersTargetSwap(), TestDeferredTool() (+44 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.13
Nodes (29): Addr, Config, Conn, Attempt, netops, Probe, ProbeID, ProbeResult (+21 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.07
Nodes (31): CancelFunc, Cmd, Context, Duration, depsState(), KeyMsg, Target, helpKeys() (+23 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.17
Nodes (24): fixFor(), ProbeID, curlTool(), Duration, Target, nmapTool(), psArgs(), quoterFor() (+16 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.17
Nodes (11): errReader, T, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), Context, Reader, parseDefaultRoute() (+3 more)

### Community 6 - "Go Module"
Cohesion: 0.39
Nodes (6): F, Clean(), FuzzSanitize(), T, noControl(), TestSanitize()

### Community 7 - "Community 7"
Cohesion: 0.19
Nodes (17): T, Target, mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), Diagnose(), T, TestDiagnoseBannerFail() (+9 more)

### Community 8 - "Community 8"
Cohesion: 0.36
Nodes (6): dnsFix(), ifaceFix(), T, TestDefaultRouteSmoke(), TestFixHintsPerGOOS(), TestSSIDSmoke()

### Community 9 - "Community 9"
Cohesion: 0.20
Nodes (16): FlagSet, buildReport(), main(), printUsage(), run(), runJSON(), TestBuildReport(), TestBuildReportGenericAllPass() (+8 more)

### Community 11 - "Q: I would like for you to help me make an ideal prompt I can send to Fable to help me make my program cross platform."
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I would like for you to help me make an ideal prompt I can send to Fable to help me make my program cross platform., Source Nodes

### Community 12 - "Community 12"
Cohesion: 0.60
Nodes (4): T, TestDialIPsLoopback(), TestDialIPsRefusedLoopback(), TestPathIdentityLoopback()

### Community 13 - "Community 13"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?, Source Nodes

### Community 14 - "Community 14"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Why use nslookup instead of Resolve-DnsName?, Source Nodes

### Community 15 - "Community 15"
Cohesion: 0.18
Nodes (14): silentConn, fakeConn, T, Time, TestBannerProbeReadTimeout(), TestDialIPsAttemptCap(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered() (+6 more)

### Community 16 - "Community 16"
Cohesion: 0.12
Nodes (24): fakeConn, scriptConn, Addr, Conn, Context, T, Time, proxyOps() (+16 more)

### Community 17 - "Community 17"
Cohesion: 0.18
Nodes (10): Reader, parseDarwinRoute(), parseWindowsRoute(), T, TestParseDarwinRoute(), TestParseWindowsRoute(), defaultRoute(), Context (+2 more)

### Community 18 - "Community 18"
Cohesion: 0.29
Nodes (6): iwreq, Context, parseESSID(), ssid(), T, TestParseESSID()

### Community 19 - "Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?, Source Nodes

### Community 20 - "Community 20"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Which LICENSE should I use?, Source Nodes

### Community 21 - "Addr"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 24 - "Community 24"
Cohesion: 0.67
Nodes (3): defaultRoute(), Context, ssid()

### Community 25 - "Community 25"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 27 - "Community 27"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 31 - "Community 31"
Cohesion: 0.14
Nodes (28): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, readCappedLine(), startTool() (+20 more)

### Community 33 - "Model"
Cohesion: 0.36
Nodes (6): blockHasValue(), parseAirportSSID(), parseNetshSSID(), T, TestParseAirportSSID(), TestParseNetshSSID()

### Community 37 - "T"
Cohesion: 0.31
Nodes (7): Proto, Target, parsePort(), ParseTarget(), T, TestParseTarget(), TestParseTargetErrors()

### Community 38 - "Time"
Cohesion: 0.70
Nodes (4): copyReport(), exportReport(), osc52Mode(), Mode

### Community 40 - "Model"
Cohesion: 0.42
Nodes (9): doneResults(), T, TestBannerFixVerdicts(), TestDeferredFix(), TestFixFor(), TestFixToolGating(), TestFixVerifyRestart(), TestPendingOverridesFix() (+1 more)

### Community 52 - "report.go"
Cohesion: 0.16
Nodes (16): ExitCode(), Model, T, TestInit(), TestNewAndExitCode(), T, Target, mustTarget() (+8 more)

## Knowledge Gaps
- **31 isolated node(s):** `scheduleMsg`, `github.com/heymaikol/network-doctor`, `ToolOutputMsg`, `How it diagnoses`, `Arch Linux (AUR)` (+26 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **38 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `Model` (3× useful, score=2.999443232)
- `.handleViewKey()` (2× useful, score=1.999826483) _(code changed — re-verify)_
- `.outputView()` (2× useful, score=1.999826483) _(code changed — re-verify)_
- `README.md` (2× useful, score=1.511362375)
- `Install` (2× useful, score=1.511362375)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `ProbeID` connect `TUI Model (Bubble Tea)` to `Model`, `Community 7`?**
  _High betweenness centrality (0.236) - this node is a cross-community bridge._
- **Why does `doneResults()` connect `Model` to `TUI Model (Bubble Tea)`?**
  _High betweenness centrality (0.232) - this node is a cross-community bridge._
- **Why does `asModel()` connect `Network Checks Engine` to `Model`, `report.go`?**
  _High betweenness centrality (0.193) - this node is a cross-community bridge._
- **Are the 2 inferred relationships involving `asModel()` (e.g. with `TestDeferredFix()` and `TestDowngradeRunsWhenSkipsFinishRun()`) actually correct?**
  _`asModel()` has 2 INFERRED edges - model-reasoned connections that need verification._
- **What connects `scheduleMsg`, `github.com/heymaikol/network-doctor`, `ToolOutputMsg` to the rest of the system?**
  _31 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Diagnostic Concepts & Docs` be split into smaller, more focused modules?**
  _Cohesion score 0.125 - nodes in this community are weakly interconnected._
- **Should `Network Checks Engine` be split into smaller, more focused modules?**
  _Cohesion score 0.09494949494949495 - nodes in this community are weakly interconnected._