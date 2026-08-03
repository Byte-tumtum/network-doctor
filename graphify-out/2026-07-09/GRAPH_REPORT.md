# Graph Report - network-doctor  (2026-07-09)

## Corpus Check
- 57 files · ~36,034 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 498 nodes · 1153 edges · 26 communities (25 shown, 1 thin omitted)
- Extraction: 81% EXTRACTED · 19% INFERRED · 0% AMBIGUOUS · INFERRED: 220 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `ee3a27d4`
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
- Community 24
- Community 25
- Community 27
- Community 31

## God Nodes (most connected - your core abstractions)
1. `model` - 57 edges
2. `newModel()` - 46 edges
3. `mustTarget()` - 28 edges
4. `ProbeID` - 24 edges
5. `ProbeResult` - 23 edges
6. `netops` - 23 edges
7. `asModel()` - 23 edges
8. `toolsFor()` - 21 edges
9. `extractFacts()` - 21 edges
10. `Target` - 19 edges

## Surprising Connections (you probably didn't know these)
- `run()` --calls--> `SetEgressEndpoints()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `runJSON()` --calls--> `BuildProbes()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `buildReport()` --calls--> `Diagnose()`  [INFERRED]
  main.go → internal/diagnostic/diagnosis.go
- `runJSON()` --calls--> `RunAll()`  [INFERRED]
  main.go → internal/diagnostic/runall.go
- `buildReport()` --references--> `ProbeID`  [EXTRACTED]
  main.go → internal/diagnostic/checks.go

## Import Cycles
- None detected.

## Communities (26 total, 1 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.15
Nodes (12): Built with, Development, Drill-down tools, Exit codes, How it diagnoses, Install, JSON output, network-doctor (+4 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.10
Nodes (58): doneResults(), model, T, TestBannerFixVerdicts(), TestDeferredFix(), TestFixFor(), TestFixToolGating(), TestFixVerifyRerun() (+50 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.11
Nodes (35): Config, Attempt, netops, Probe, ProbeID, ProbeResult, Status, Interface (+27 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.09
Nodes (15): CancelFunc, Cmd, Context, Duration, KeyMsg, Msg, Time, model (+7 more)

### Community 4 - "Linux Route/ARP Parsing"
Cohesion: 0.12
Nodes (44): fixFor(), curlTool(), extractFacts(), Duration, mtrHops(), nmapFacts(), nmapTool(), nslookupFacts() (+36 more)

### Community 5 - "Main Entry & Target Tests"
Cohesion: 0.17
Nodes (11): errReader, T, TestDecodeGatewayHex(), decodeGatewayHex(), defaultRoute(), Context, Reader, parseDefaultRoute() (+3 more)

### Community 7 - "Community 7"
Cohesion: 0.16
Nodes (19): BuildProbes(), TestBuildProbesProtoShapes(), T, mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), TestRemaining(), Diagnose() (+11 more)

### Community 8 - "Community 8"
Cohesion: 0.36
Nodes (6): dnsFix(), ifaceFix(), T, TestDefaultRouteSmoke(), TestFixHintsPerGOOS(), TestSSIDSmoke()

### Community 9 - "Community 9"
Cohesion: 0.07
Nodes (45): Proto, Target, FlagSet, T, TestBannerProbeReadTimeout(), TestDialIPsAttemptCap(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered() (+37 more)

### Community 10 - "Community 10"
Cohesion: 0.53
Nodes (5): F, FuzzSanitize(), T, noControl(), TestSanitize()

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
Cohesion: 0.60
Nodes (4): T, TestToolboxExitZero(), TestToolHotkeysUnique(), TestToolsFor()

### Community 16 - "Community 16"
Cohesion: 0.08
Nodes (32): fakeConn, iwreq, scriptConn, silentConn, Addr, Conn, Context, Reader (+24 more)

### Community 17 - "Community 17"
Cohesion: 0.18
Nodes (10): Reader, parseDarwinRoute(), parseWindowsRoute(), T, TestParseDarwinRoute(), TestParseWindowsRoute(), defaultRoute(), Context (+2 more)

### Community 18 - "Community 18"
Cohesion: 0.18
Nodes (10): Context, ssid(), blockHasValue(), parseAirportSSID(), parseNetshSSID(), T, TestParseAirportSSID(), TestParseNetshSSID() (+2 more)

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
Cohesion: 0.12
Nodes (31): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, Reader, readCappedLine() (+23 more)

## Knowledge Gaps
- **28 isolated node(s):** `github.com/mplaczek99/network-doctor`, `iwreq`, `scheduleMsg`, `How it diagnoses`, `Install` (+23 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `README.md` (2× useful, score=1.668953917)
- `Install` (2× useful, score=1.668953917)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`, `TUI Model (Bubble Tea)`, `Linux Route/ARP Parsing`, `Community 9`, `Community 31`?**
  _High betweenness centrality (0.197) - this node is a cross-community bridge._
- **Why does `newModel()` connect `Network Checks Engine` to `TUI Model Tests`, `Linux Route/ARP Parsing`, `Community 7`, `Community 9`, `Community 15`?**
  _High betweenness centrality (0.162) - this node is a cross-community bridge._
- **Why does `Clean()` connect `TUI Model (Bubble Tea)` to `TUI Model Tests`, `Community 7`, `Community 10`, `Community 16`, `Community 18`, `Community 31`?**
  _High betweenness centrality (0.099) - this node is a cross-community bridge._
- **Are the 43 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 43 INFERRED edges - model-reasoned connections that need verification._
- **Are the 22 inferred relationships involving `mustTarget()` (e.g. with `TestDeferredQuit()` and `TestDeferredRerun()`) actually correct?**
  _`mustTarget()` has 22 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/mplaczek99/network-doctor`, `iwreq`, `scheduleMsg` to the rest of the system?**
  _28 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Network Checks Engine` be split into smaller, more focused modules?**
  _Cohesion score 0.10100475938656796 - nodes in this community are weakly interconnected._