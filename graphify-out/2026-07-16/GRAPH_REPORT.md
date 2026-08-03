# Graph Report - network-doctor  (2026-07-16)

## Corpus Check
- 73 files · ~38,246 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 543 nodes · 1024 edges · 69 communities (29 shown, 40 thin omitted)
- Extraction: 93% EXTRACTED · 7% INFERRED · 0% AMBIGUOUS · INFERRED: 73 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `61096455`
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
- report_test.go
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
- KeyMsg
- job
- Model
- T
- Stream
- Probe
- ProbeID
- github.com/heymaikol/network-doctor
- ProbeResult
- Target
- T
- T
- T
- Target

## God Nodes (most connected - your core abstractions)
1. `model` - 61 edges
2. `newModel()` - 24 edges
3. `netops` - 22 edges
4. `asModel()` - 21 edges
5. `toolsFor()` - 18 edges
6. `ProbeResult` - 17 edges
7. `ProbeID` - 15 edges
8. `keyMsg()` - 14 edges
9. `Tool` - 13 edges
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

## Communities (69 total, 40 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.12
Nodes (15): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+7 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.07
Nodes (69): doneResults(), ProbeID, TestBannerFixVerdicts(), TestDeferredFix(), TestFixFor(), TestFixToolGating(), TestFixVerifyRestart(), TestPendingOverridesFix() (+61 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.13
Nodes (29): Addr, Config, Conn, Attempt, netops, Probe, ProbeID, ProbeResult (+21 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.07
Nodes (31): CancelFunc, Cmd, Context, Duration, depsState(), KeyMsg, Target, helpKeys() (+23 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.12
Nodes (29): fixFor(), ProbeID, T, TestToolboxExitZero(), TestToolHotkeysUnique(), TestToolsFor(), cacheAvailability(), curlTool() (+21 more)

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

### Community 38 - "report_test.go"
Cohesion: 0.22
Nodes (11): copyReport(), exportReport(), osc52Mode(), TestCopyReportPrefersNativeClipboard(), TestExportReportSavePath(), TestOSC52Mode(), TestRenderJobLineTreatsStderrAsOutput(), TestReportBracketsIPv6() (+3 more)

### Community 52 - "report.go"
Cohesion: 0.33
Nodes (5): ExitCode(), Model, T, TestInit(), TestNewAndExitCode()

## Knowledge Gaps
- **31 isolated node(s):** `scheduleMsg`, `How it diagnoses`, `Arch Linux (AUR)`, `macOS (Homebrew)`, `Everywhere else` (+26 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **40 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `Model` (5× useful, score=4.793658681)
- `.handleKey()` (4× useful, score=3.930611449) _(code changed — re-verify)_
- `.Update()` (4× useful, score=3.929938543) _(code changed — re-verify)_
- `.handleViewKey()` (4× useful, score=3.862704921) _(code changed — re-verify)_
- `.outputView()` (4× useful, score=3.862300685) _(code changed — re-verify)_
- `.noticeView()` (2× useful, score=1.999614979) _(code changed — re-verify)_
- `.viewerFooter()` (2× useful, score=1.999489035) _(code changed — re-verify)_
- `TestCtrlCWarnsThenQuits()` (2× useful, score=1.931282586) _(code changed — re-verify)_
- `README.md` (2× useful, score=1.408119359)
- `Install` (2× useful, score=1.408119359)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`?**
  _High betweenness centrality (0.062) - this node is a cross-community bridge._
- **Why does `New()` connect `TUI Model Tests` to `Network Checks Engine`, `Linux Route/ARP Parsing`?**
  _High betweenness centrality (0.033) - this node is a cross-community bridge._
- **Why does `scriptConn` connect `Community 16` to `Community 31`?**
  _High betweenness centrality (0.029) - this node is a cross-community bridge._
- **What connects `scheduleMsg`, `How it diagnoses`, `Arch Linux (AUR)` to the rest of the system?**
  _31 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Diagnostic Concepts & Docs` be split into smaller, more focused modules?**
  _Cohesion score 0.125 - nodes in this community are weakly interconnected._
- **Should `Network Checks Engine` be split into smaller, more focused modules?**
  _Cohesion score 0.07191780821917808 - nodes in this community are weakly interconnected._
- **Should `TUI Model (Bubble Tea)` be split into smaller, more focused modules?**
  _Cohesion score 0.12828282828282828 - nodes in this community are weakly interconnected._