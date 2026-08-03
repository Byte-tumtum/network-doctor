# Graph Report - network-doctor  (2026-07-12)

## Corpus Check
- 61 files · ~34,244 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 513 nodes · 997 edges · 61 communities (25 shown, 36 thin omitted)
- Extraction: 87% EXTRACTED · 13% INFERRED · 0% AMBIGUOUS · INFERRED: 134 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `cbd372ba`
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
- JobStatus
- Msg
- Status
- Stream

## God Nodes (most connected - your core abstractions)
1. `model` - 58 edges
2. `newModel()` - 46 edges
3. `ProbeID` - 25 edges
4. `ProbeResult` - 23 edges
5. `netops` - 22 edges
6. `toolsFor()` - 16 edges
7. `asModel()` - 16 edges
8. `Diagnose()` - 13 edges
9. `Probe` - 12 edges
10. `Tool` - 12 edges

## Surprising Connections (you probably didn't know these)
- `run()` --calls--> `SetEgressEndpoints()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `runJSON()` --calls--> `BuildProbes()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `buildReport()` --calls--> `Diagnose()`  [INFERRED]
  main.go → internal/diagnostic/diagnosis.go
- `TestBuildReport()` --calls--> `ParseTarget()`  [INFERRED]
  main_test.go → internal/diagnostic/target.go
- `buildReport()` --references--> `ProbeID`  [EXTRACTED]
  main.go → internal/diagnostic/checks.go

## Import Cycles
- None detected.

## Communities (61 total, 36 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.12
Nodes (15): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+7 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.12
Nodes (39): doneResults(), model, T, TestBannerFixVerdicts(), TestDeferredFix(), TestFixFor(), TestFixToolGating(), TestFixVerifyRerun() (+31 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.12
Nodes (33): Addr, Config, Conn, Attempt, netops, Probe, ProbeID, ProbeResult (+25 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.08
Nodes (19): CancelFunc, Cmd, Context, Duration, Msg, Target, helpKeys(), joinChips() (+11 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.19
Nodes (22): fixFor(), curlTool(), Duration, Target, nmapTool(), psArgs(), shellArgs(), smtpTool() (+14 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.17
Nodes (11): errReader, T, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), Context, Reader, parseDefaultRoute() (+3 more)

### Community 7 - "Community 7"
Cohesion: 0.19
Nodes (17): T, Target, mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), Diagnose(), T, TestDiagnoseBannerFail() (+9 more)

### Community 8 - "Community 8"
Cohesion: 0.36
Nodes (6): dnsFix(), ifaceFix(), T, TestDefaultRouteSmoke(), TestFixHintsPerGOOS(), TestSSIDSmoke()

### Community 9 - "Community 9"
Cohesion: 0.21
Nodes (16): FlagSet, buildReport(), Target, main(), printUsage(), run(), runJSON(), T (+8 more)

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
Cohesion: 0.12
Nodes (30): silentConn, fakeConn, TestBannerProbeReadTimeout(), TestDialIPsAttemptCap(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered(), TestDNSProbeErrors(), TestHTTPProbeHeaderLimit() (+22 more)

### Community 16 - "Community 16"
Cohesion: 0.12
Nodes (24): fakeConn, scriptConn, Addr, Conn, Context, T, Time, proxyOps() (+16 more)

### Community 17 - "Community 17"
Cohesion: 0.18
Nodes (10): Reader, parseDarwinRoute(), parseWindowsRoute(), T, TestParseDarwinRoute(), TestParseWindowsRoute(), defaultRoute(), Context (+2 more)

### Community 18 - "Community 18"
Cohesion: 0.09
Nodes (22): iwreq, F, Context, ssid(), Context, parseESSID(), ssid(), T (+14 more)

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
Cohesion: 0.13
Nodes (29): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, readCappedLine(), startTool() (+21 more)

### Community 39 - "TestToolboxExitZero"
Cohesion: 0.11
Nodes (24): Proto, Target, IP, parsePort(), ParseTarget(), T, TestParseTarget(), TestParseTargetErrors() (+16 more)

## Knowledge Gaps
- **31 isolated node(s):** `ToolOutputMsg`, `scheduleMsg`, `github.com/mplaczek99/network-doctor`, `How it diagnoses`, `Arch Linux (AUR)` (+26 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **36 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `README.md` (2× useful, score=1.551215186)
- `Install` (2× useful, score=1.551215186)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`, `TUI Model (Bubble Tea)`, `Community 31`, `Community 15`?**
  _High betweenness centrality (0.253) - this node is a cross-community bridge._
- **Why does `newModel()` connect `Network Checks Engine` to `TUI Model (Bubble Tea)`, `TUI Model Tests`, `Community 15`, `TestToolboxExitZero`?**
  _High betweenness centrality (0.199) - this node is a cross-community bridge._
- **Why does `ProbeID` connect `TUI Model (Bubble Tea)` to `Network Checks Engine`, `TUI Model Tests`, `Linux Route/ARP Parsing`, `Community 7`, `Community 9`?**
  _High betweenness centrality (0.105) - this node is a cross-community bridge._
- **Are the 43 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 43 INFERRED edges - model-reasoned connections that need verification._
- **What connects `ToolOutputMsg`, `scheduleMsg`, `github.com/mplaczek99/network-doctor` to the rest of the system?**
  _31 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Diagnostic Concepts & Docs` be split into smaller, more focused modules?**
  _Cohesion score 0.125 - nodes in this community are weakly interconnected._
- **Should `Network Checks Engine` be split into smaller, more focused modules?**
  _Cohesion score 0.1173054587688734 - nodes in this community are weakly interconnected._