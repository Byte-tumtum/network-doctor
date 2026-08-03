# Graph Report - network-doctor  (2026-07-10)

## Corpus Check
- 57 files · ~36,222 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 520 nodes · 1137 edges · 35 communities (26 shown, 9 thin omitted)
- Extraction: 82% EXTRACTED · 18% INFERRED · 0% AMBIGUOUS · INFERRED: 199 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `87e6ea76`
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

## God Nodes (most connected - your core abstractions)
1. `model` - 57 edges
2. `newModel()` - 45 edges
3. `asModel()` - 24 edges
4. `netops` - 23 edges
5. `mustTarget()` - 23 edges
6. `extractFacts()` - 20 edges
7. `toolsFor()` - 19 edges
8. `ProbeID` - 18 edges
9. `ProbeResult` - 18 edges
10. `New()` - 16 edges

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

## Communities (35 total, 9 thin omitted)

### Community 0 - "Diagnostic Concepts & Docs"
Cohesion: 0.12
Nodes (15): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+7 more)

### Community 1 - "Network Checks Engine"
Cohesion: 0.10
Nodes (59): doneResults(), model, T, TestBannerFixVerdicts(), TestDeferredFix(), TestFixFor(), TestFixToolGating(), TestFixVerifyRerun() (+51 more)

### Community 2 - "TUI Model (Bubble Tea)"
Cohesion: 0.12
Nodes (30): Config, Attempt, netops, Probe, ProbeID, ProbeResult, Status, Interface (+22 more)

### Community 3 - "TUI Model Tests"
Cohesion: 0.07
Nodes (32): CancelFunc, Cmd, Context, Duration, Fact, depsState(), KeyMsg, helpKeys() (+24 more)

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
Cohesion: 0.08
Nodes (37): Proto, Target, FlagSet, IP, parsePort(), ParseTarget(), T, TestParseTarget() (+29 more)

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
Cohesion: 0.20
Nodes (13): silentConn, T, Time, TestBannerProbeReadTimeout(), TestDialIPsAttemptCap(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered(), TestDNSProbeErrors() (+5 more)

### Community 16 - "Community 16"
Cohesion: 0.09
Nodes (30): fakeConn, iwreq, scriptConn, Addr, Conn, Context, Reader, T (+22 more)

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
Cohesion: 0.14
Nodes (29): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, Reader, readCappedLine() (+21 more)

## Knowledge Gaps
- **30 isolated node(s):** `scheduleMsg`, `github.com/mplaczek99/network-doctor`, `iwreq`, `How it diagnoses`, `Arch Linux (AUR)` (+25 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **9 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `README.md` (2× useful, score=1.627401344)
- `Install` (2× useful, score=1.627401344)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `newModel()` connect `Network Checks Engine` to `Community 9`, `TUI Model Tests`?**
  _High betweenness centrality (0.268) - this node is a cross-community bridge._
- **Why does `New()` connect `Community 9` to `Community 16`, `Network Checks Engine`, `Community 15`?**
  _High betweenness centrality (0.176) - this node is a cross-community bridge._
- **Why does `model` connect `TUI Model Tests` to `Network Checks Engine`?**
  _High betweenness centrality (0.157) - this node is a cross-community bridge._
- **Are the 42 inferred relationships involving `newModel()` (e.g. with `New()` and `TestInit()`) actually correct?**
  _`newModel()` has 42 INFERRED edges - model-reasoned connections that need verification._
- **Are the 10 inferred relationships involving `asModel()` (e.g. with `TestDeferredFix()` and `TestDeferredQuit()`) actually correct?**
  _`asModel()` has 10 INFERRED edges - model-reasoned connections that need verification._
- **Are the 17 inferred relationships involving `mustTarget()` (e.g. with `TestDeferredQuit()` and `TestDeferredRerun()`) actually correct?**
  _`mustTarget()` has 17 INFERRED edges - model-reasoned connections that need verification._
- **What connects `scheduleMsg`, `github.com/mplaczek99/network-doctor`, `iwreq` to the rest of the system?**
  _30 weakly-connected nodes found - possible documentation gaps or missing edges._