# Graph Report - network-doctor  (2026-07-22)

## Corpus Check
- 83 files · ~85,381 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 716 nodes · 1322 edges · 107 communities (59 shown, 48 thin omitted)
- Extraction: 85% EXTRACTED · 15% INFERRED · 0% AMBIGUOUS · INFERRED: 197 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `fb32b41b`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Model Extra Tests
- UI Model State
- Probe Configuration Build
- Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]
- Network Connection Test Doubles
- UI Application Toolbox
- Job Execution Lifecycle
- Probe Construction Tests
- CLI Reporting Entry Point
- Architecture Decision Memory
- Linux Route Decoding
- Gateway Route Parsing
- SSID Detection Parsing
- CI and Release Automation
- Viewer Feedback Improvements
- Platform Fix Hints
- ExitCode
- Integration Network Checks
- Failure Details Navigation
- Job Output Simplification
- model
- Other Process Groups
- Unix Process Groups
- Windows Process Groups
- Dependency Automation Priorities
- Viewer Navigation Layout
- Lint Configuration
- Probe Progress Glyphs
- Tool Availability Lookup
- Network Address Type
- Cancellation Function Type
- Command Process Type
- Network Connection Type
- Execution Context Type
- Time Duration Type
- Diagnostic Fact Type
- Fake Connection Type
- Clipboard OSC 52
- Ctrl C Interaction
- Restart Prompt UX
- Target Type
- Target Type
- Probe Identifier Type
- Probe Result Type
- Target Type
- Target Type
- Probe Identifier Type
- Diagnostic Tool Type
- UI Model Type
- Probe Identifier Type
- Test Handle Type
- UI Model Type
- Diagnostic Probe Type
- Probe Identifier Type
- Probe Result Type
- Target Type
- Diagnostic Tool Type
- Conn
- Target Type
- Target Type
- Target Type
- Network IP Type
- Job State Type
- Job Status Type
- Keyboard Message Type
- Diagnostic Probe Type
- Probe Identifier Type
- Probe Result Type
- Target Type
- UI Model Type
- Tea Message Type
- Network Doctor Module
- Diagnostic Probe Type
- Probe Identifier Type
- Probe Result Type
- Stream Reader Type
- Probe Status Type
- Output Stream Type
- Test Handle Type
- Target Type
- Time Value Type
- Diagnostic Tool Type
- Remove Unused Stderr Tagging
- Replace Job Output Builder with Join
- strings.Join Job Output
- Keep Available Fallback Readable
- TestFixHintsPerGOOS
- Target
- CancelFunc
- Cmd
- Context
- Q: Make a list of things to work on in my project.
- Q: Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2
- FuzzSanitize
- Q: Tell me how to make Reddit posts so this project gets more attention.
- Q: Can you give me all the materials so I can make one post?
- New
- ExitCode
- parseESSID
- CancelFunc
- Cmd
- Context
- Duration
- IP
- KeyMsg
- Msg
- Time

## God Nodes (most connected - your core abstractions)
1. `model` - 62 edges
2. `newModel()` - 61 edges
3. `asModel()` - 40 edges
4. `mustTarget()` - 40 edges
5. `netops` - 23 edges
6. `keyMsg()` - 23 edges
7. `toolsFor()` - 23 edges
8. `New()` - 19 edges
9. `ProbeResult` - 18 edges
10. `ProbeID` - 17 edges

## Surprising Connections (you probably didn't know these)
- `run()` --calls--> `New()`  [INFERRED]
  main.go → internal/ui/model.go
- `run()` --calls--> `ParseTarget()`  [INFERRED]
  main.go → internal/diagnostic/target.go
- `TestBuildReport()` --calls--> `ParseTarget()`  [INFERRED]
  main_test.go → internal/diagnostic/target.go
- `run()` --calls--> `ExitCode()`  [INFERRED]
  main.go → internal/ui/app.go
- `TestBuildReport()` --calls--> `buildReport()`  [INFERRED]
  main_test.go → main.go

## Import Cycles
- None detected.

## Communities (107 total, 48 thin omitted)

### Community 0 - "Model Extra Tests"
Cohesion: 0.11
Nodes (66): containsEnv(), T, TestAppendJobLine(), TestClassifyJob(), TestConcurrentToolsCanSwitch(), TestDeferredQuit(), TestDeferredRestart(), TestDeferredRestartDefersTargetSwap() (+58 more)

### Community 1 - "UI Model State"
Cohesion: 0.06
Nodes (33): CancelFunc, Cmd, Context, Duration, appendJobLine(), depsState(), helpKeys(), joinChips() (+25 more)

### Community 2 - "Probe Configuration Build"
Cohesion: 0.07
Nodes (50): CertPool, Config, Attempt, netops, Probe, ProbeID, ProbeResult, Status (+42 more)

### Community 3 - "Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78], Source Nodes

### Community 4 - "Network Connection Test Doubles"
Cohesion: 0.08
Nodes (33): fakeConn, iwreq, scriptConn, Addr, Conn, Context, Reader, T (+25 more)

### Community 5 - "UI Application Toolbox"
Cohesion: 0.18
Nodes (27): cacheAvailability(), curlTool(), Duration, lanDiscoveryTool(), nmapTool(), psArgs(), quoterFor(), shellArgs() (+19 more)

### Community 6 - "Job Execution Lifecycle"
Cohesion: 0.14
Nodes (27): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, Reader, readCappedLine() (+19 more)

### Community 7 - "Probe Construction Tests"
Cohesion: 0.18
Nodes (17): FlagSet, init(), main(), printUsage(), run(), T, TestBuildReport(), TestBuildReportGenericAllPass() (+9 more)

### Community 8 - "CLI Reporting Entry Point"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I want the user to be able to press v to visualize the network, Source Nodes

### Community 9 - "Architecture Decision Memory"
Cohesion: 0.12
Nodes (15): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+7 more)

### Community 11 - "Gateway Route Parsing"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Confirm that the problem is fixed, and that there is no new issue because of the fix., Source Nodes

### Community 12 - "SSID Detection Parsing"
Cohesion: 0.18
Nodes (10): Context, ssid(), blockHasValue(), parseAirportSSID(), parseNetshSSID(), T, TestParseAirportSSID(), TestParseNetshSSID() (+2 more)

### Community 13 - "CI and Release Automation"
Cohesion: 0.67
Nodes (3): GoReleaser Publishing, Main Branch Ancestry Gate, Release Automation Workflow

### Community 15 - "Platform Fix Hints"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: shrink: winSafe() GOOS branch — unconditional strings.ToValidUTF8(line, "?") is one line, consistent across platforms, Source Nodes

### Community 16 - "ExitCode"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Tell me how to get more people to this project., Source Nodes

### Community 17 - "Integration Network Checks"
Cohesion: 0.60
Nodes (4): T, TestDialIPsLoopback(), TestDialIPsRefusedLoopback(), TestPathIdentityLoopback()

### Community 20 - "model"
Cohesion: 0.19
Nodes (16): silentConn, T, Time, TestBannerProbeReadTimeout(), TestBannerProbeReadTimeoutHonorsContext(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered(), TestDNSProbeErrors() (+8 more)

### Community 21 - "Other Process Groups"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 22 - "Unix Process Groups"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 23 - "Windows Process Groups"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 29 - "Network Address Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I would like for you to help me make an ideal prompt I can send to Fable to help me make my program cross platform., Source Nodes

### Community 30 - "Cancellation Function Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?, Source Nodes

### Community 31 - "Command Process Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Why use nslookup instead of Resolve-DnsName?, Source Nodes

### Community 32 - "Network Connection Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Should cross-platform release distribution via GoReleaser and prebuilt GitHub binaries be out of scope for the portability plan?, Source Nodes

### Community 33 - "Execution Context Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Which LICENSE should I use?, Source Nodes

### Community 34 - "Time Duration Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Give me a list of things to do in my project., Source Nodes

### Community 35 - "Diagnostic Fact Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Give me a list of things to do in my project., Source Nodes

### Community 36 - "Fake Connection Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: How are these ideas? Roadmap and known debt list; item 9 is complete., Source Nodes

### Community 40 - "Target Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Promote OSC 52 and remove clipboard direct dependency; inline fallback verdict and one-caller helpers remaining, jobTailN, envProxyURL, and skipResult, Source Nodes

### Community 41 - "Target Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Ctrl+C swallowed silently — first press should flash “Press q to quit”, second within ~2s quits, and Ctrl+C should cancel the confirm gate., Source Nodes

### Community 42 - "Probe Identifier Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: q inside output viewer quits whole app (model.go:409). less/pager convention: q closes pager. Surprise app-exit is worse than redundant key. Make q = back in viewer, quit only from main screen., Source Nodes

### Community 43 - "Probe Result Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Set ti.Placeholder = "example.com:443 — empty for a general check" in the restart prompt (model.go:312)., Source Nodes

### Community 44 - "Target Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 8. No home/end in viewer — only arrows + pgup/pgdn (model.go:338). Add home/end bindings; long tool output needs jump-to-top., Source Nodes

### Community 45 - "Target Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 3. Viewer overflows on narrow terminals — vpHeight() reserves fixed 4 rows (model.go:741), but viewer footer helpKeys wraps to 2-3 lines under ~70 cols. Total exceeds terminal, renderer cuts title. Subtract lipgloss.Height(footer) instead of constant., Source Nodes

### Community 46 - "Probe Identifier Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Notices immortal — report copied to clipboard sits until restart. Reuse the Ctrl+C tea.Tick expiry pattern for about 4 seconds., Source Nodes

### Community 47 - "Diagnostic Tool Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: No auto-jump to failure — run finishes with fail #5, selection still on probe 0. Select first fail when allDone(); details panel then shows the thing that matters. Commit when done., Source Nodes

### Community 48 - "UI Model Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 9. q dead in viewer — only esc exits (deliberate per commit), but q falls through to viewport, does nothing. Muscle memory from less/pagers expects q = back. Cheap add. Commit when done., Source Nodes

### Community 49 - "Probe Identifier Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 10. Queued vs running indistinguishable — glyph() shows spinner for every unfinished probe, including deps-blocked ones not started. You have started map: faint · for queued, spinner only for in-flight. Makes the dependency chain visible. Commit when done., Source Nodes

### Community 50 - "Test Handle Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 11. w saves to cwd — installed binary run from read-only dir fails; also notice shows bare filename, user hunts for it. Fall back os.UserHomeDir(), print absolute path. Commit when done., Source Nodes

### Community 51 - "UI Model Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 12. Ctrl+C hint text — notice says "Press q to quit" but second Ctrl+C inside 2s window also quits. Convention: "Press Ctrl+C again (or q) to quit". Commit when done., Source Nodes

### Community 52 - "Diagnostic Probe Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Viewer lacks search + copy — 5000 lines, no / filter, no y copy-full-output. Copy is cheap (clipboard path exists in report.go); search is bigger, add when needed., Source Nodes

### Community 53 - "Probe Identifier Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 15. Details attempts unbounded — long attempt list can push top region past terminal height on short screens; body has no height clamp, masthead gets cut. Edge case, clamp when it bites. Commit when done., Source Nodes

### Community 54 - "Probe Result Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: - delete: stderr tagging plumbed through whole job pipeline, never used — ToolOutputMsg.Stderr, streamReader stderr param, outLine.stderr, and identity wrapper renderJobLine (returns ln.text, 3 call sites). Replacement: jobLines []string, inline .text. ~-25 lines + test churn. [internal/ui/jobs.go:43, internal/ui/model.go:65,737], Source Nodes

### Community 55 - "Target Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: shrink: jobOutput() builder loop = strings.Join(m.jobLines, "\n"). One line replaces ten. [internal/ui/model.go:676], Source Nodes

### Community 56 - "Diagnostic Tool Type"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: shrink: Available() uncached fallback branch — production tools all pass through cacheAvailability, so the live-LookPath path only serves hand-built zero-value Tools in tests. Return t.available when checked, else one LookPath line; or accept as-is, it's 3 lines. [internal/ui/tools.go:32-38], Source Nodes

### Community 57 - "Conn"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I know for a fact that my home network contains numerous devices, but the network map only shows one, Source Nodes

### Community 86 - "TestFixHintsPerGOOS"
Cohesion: 0.38
Nodes (5): dnsFix(), ifaceFix(), T, TestFixHintsPerGOOS(), TestSSIDSmoke()

### Community 87 - "Target"
Cohesion: 0.21
Nodes (9): ExitCode(), Model, T, TestInit(), TestNewAndExitCode(), T, TestToolboxExitZero(), TestToolHotkeysUnique() (+1 more)

### Community 88 - "CancelFunc"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: hostnameRe rejects trailing-dot FQDNs such as example.com.; should target parsing accept them?, Source Nodes

### Community 89 - "Cmd"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Restart prompt still speaks old binary name; netdoc full-command input fails in parseRunArgs., Source Nodes

### Community 90 - "Context"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Restart clears tool output but not the job pane status/header — stale UI, Source Nodes

### Community 91 - "Q: Make a list of things to work on in my project."
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Make a list of things to work on in my project., Source Nodes

### Community 92 - "Q: Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2, Source Nodes

### Community 93 - "FuzzSanitize"
Cohesion: 0.53
Nodes (5): F, FuzzSanitize(), T, noControl(), TestSanitize()

### Community 94 - "Q: Tell me how to make Reddit posts so this project gets more attention."
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Tell me how to make Reddit posts so this project gets more attention., Source Nodes

### Community 95 - "Q: Can you give me all the materials so I can make one post?"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Can you give me all the materials so I can make one post?, Source Nodes

### Community 96 - "New"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Audit this entire repository. Do not modify code., Source Nodes

### Community 97 - "ExitCode"
Cohesion: 0.14
Nodes (19): Proto, parsePort(), ParseTarget(), T, TestParseTarget(), TestParseTargetErrors(), copyReport(), exportReport() (+11 more)

### Community 98 - "parseESSID"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Pressing v will run a LAN Scan, not prompt to user to run a LAN Scan. Also update the text to say that pressing v runs a network map and then it says lan scan., Source Nodes

## Knowledge Gaps
- **167 isolated node(s):** `scheduleMsg`, `github.com/heymaikol/network-doctor`, `iwreq`, `ToolOutputMsg`, `Arch Linux (AUR)` (+162 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **48 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `Diagnose()` (5× useful, score=4.447679451)
- `.outputView()` (5× useful, score=4.274460161) _(code changed — re-verify)_
- `README.md` (5× useful, score=3.962560241)
- `.Update()` (4× useful, score=3.443015585) _(code changed — re-verify)_
- `.handleViewKey()` (4× useful, score=3.38411227) _(code changed — re-verify)_
- `DowngradeEgress()` (3× useful, score=2.755926137)
- `report` (3× useful, score=2.626583715)
- `Install` (3× useful, score=2.130110456)
- `.View()` (2× useful, score=1.829235262) _(code changed — re-verify)_
- `JSON output` (2× useful, score=1.812302464)

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `model` to `Model Extra Tests`, `ExitCode`, `UI Model State`, `Network Connection Test Doubles`, `UI Application Toolbox`, `Probe Construction Tests`, `Target`?**
  _High betweenness centrality (0.158) - this node is a cross-community bridge._
- **Why does `newModel()` connect `Model Extra Tests` to `ExitCode`, `Probe Configuration Build`, `UI Application Toolbox`, `model`, `Target`?**
  _High betweenness centrality (0.091) - this node is a cross-community bridge._
- **Why does `model` connect `UI Model State` to `model`?**
  _High betweenness centrality (0.080) - this node is a cross-community bridge._
- **Are the 32 inferred relationships involving `newModel()` (e.g. with `TestInit()` and `TestConcurrentToolsCanSwitch()`) actually correct?**
  _`newModel()` has 32 INFERRED edges - model-reasoned connections that need verification._
- **Are the 13 inferred relationships involving `asModel()` (e.g. with `TestConcurrentToolsCanSwitch()` and `TestDeferredQuit()`) actually correct?**
  _`asModel()` has 13 INFERRED edges - model-reasoned connections that need verification._
- **Are the 33 inferred relationships involving `mustTarget()` (e.g. with `TestConcurrentToolsCanSwitch()` and `TestDeferredQuit()`) actually correct?**
  _`mustTarget()` has 33 INFERRED edges - model-reasoned connections that need verification._
- **What connects `scheduleMsg`, `github.com/heymaikol/network-doctor`, `iwreq` to the rest of the system?**
  _178 weakly-connected nodes found - possible documentation gaps or missing edges._