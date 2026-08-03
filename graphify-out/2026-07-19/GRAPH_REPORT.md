# Graph Report - network-doctor  (2026-07-19)

## Corpus Check
- 79 files · ~76,026 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 659 nodes · 1245 edges · 96 communities (56 shown, 40 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 199 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `179b674d`
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
- Cmd
- CancelFunc
- Cmd
- Context
- Q: Make a list of things to work on in my project.
- Q: Can you solve this GitHub Issue, Diagnose claims the general internet is unreachable when the egress probe is Warn (connected but degraded) #2
- CancelFunc
- Q: Tell me how to make Reddit posts so this project gets more attention.
- Q: Can you give me all the materials so I can make one post?

## God Nodes (most connected - your core abstractions)
1. `model` - 57 edges
2. `newModel()` - 56 edges
3. `mustTarget()` - 38 edges
4. `asModel()` - 35 edges
5. `toolsFor()` - 25 edges
6. `ProbeID` - 24 edges
7. `ProbeResult` - 23 edges
8. `netops` - 22 edges
9. `New()` - 21 edges
10. `keyMsg()` - 21 edges

## Surprising Connections (you probably didn't know these)
- `runJSON()` --calls--> `BuildProbes()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `buildReport()` --calls--> `Diagnose()`  [INFERRED]
  main.go → internal/diagnostic/diagnosis.go
- `runJSON()` --calls--> `RunAll()`  [INFERRED]
  main.go → internal/diagnostic/runall.go
- `run()` --calls--> `ExitCode()`  [INFERRED]
  main.go → internal/ui/app.go
- `run()` --calls--> `New()`  [INFERRED]
  main.go → internal/ui/model.go

## Import Cycles
- None detected.

## Communities (96 total, 40 thin omitted)

### Community 0 - "Model Extra Tests"
Cohesion: 0.11
Nodes (62): containsEnv(), T, TestAppendJobLine(), TestClassifyJob(), TestDeferredQuit(), TestDeferredRestart(), TestDeferredRestartDefersTargetSwap(), TestDeferredTool() (+54 more)

### Community 1 - "UI Model State"
Cohesion: 0.08
Nodes (18): CancelFunc, Cmd, Context, Duration, KeyMsg, Msg, Time, model (+10 more)

### Community 2 - "Probe Configuration Build"
Cohesion: 0.12
Nodes (31): Config, Attempt, netops, Probe, ProbeID, ProbeResult, Status, Interface (+23 more)

### Community 3 - "Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78], Source Nodes

### Community 4 - "Network Connection Test Doubles"
Cohesion: 0.08
Nodes (40): fakeConn, scriptConn, silentConn, Addr, Conn, Context, Reader, T (+32 more)

### Community 5 - "UI Application Toolbox"
Cohesion: 0.20
Nodes (25): cacheAvailability(), curlTool(), Duration, nmapTool(), psArgs(), quoterFor(), shellArgs(), smtpTool() (+17 more)

### Community 6 - "Job Execution Lifecycle"
Cohesion: 0.14
Nodes (27): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, Reader, readCappedLine() (+19 more)

### Community 7 - "Probe Construction Tests"
Cohesion: 0.28
Nodes (11): copyReport(), exportReport(), osc52Sequence(), T, TestCopyReportPrefersNativeClipboard(), TestExportReportSavePath(), TestOSC52Sequence(), TestReportBracketsIPv6() (+3 more)

### Community 8 - "CLI Reporting Entry Point"
Cohesion: 0.29
Nodes (6): iwreq, Context, parseESSID(), ssid(), T, TestParseESSID()

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
Cohesion: 0.21
Nodes (9): ExitCode(), Model, T, TestInit(), TestNewAndExitCode(), T, TestToolboxExitZero(), TestToolHotkeysUnique() (+1 more)

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
Cohesion: 0.53
Nodes (5): F, FuzzSanitize(), T, noControl(), TestSanitize()

### Community 86 - "TestFixHintsPerGOOS"
Cohesion: 0.38
Nodes (5): dnsFix(), ifaceFix(), T, TestFixHintsPerGOOS(), TestSSIDSmoke()

### Community 87 - "Cmd"
Cohesion: 0.16
Nodes (18): BuildProbes(), TestBuildProbesProtoShapes(), T, mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), Diagnose(), T (+10 more)

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

### Community 93 - "CancelFunc"
Cohesion: 0.11
Nodes (28): Proto, Target, FlagSet, IP, parsePort(), ParseTarget(), T, TestParseTarget() (+20 more)

### Community 94 - "Q: Tell me how to make Reddit posts so this project gets more attention."
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Tell me how to make Reddit posts so this project gets more attention., Source Nodes

### Community 95 - "Q: Can you give me all the materials so I can make one post?"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Can you give me all the materials so I can make one post?, Source Nodes

## Knowledge Gaps
- **155 isolated node(s):** `github.com/heymaikol/network-doctor`, `iwreq`, `ToolOutputMsg`, `scheduleMsg`, `Arch Linux (AUR)` (+150 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **40 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `.outputView()` (5× useful, score=4.667348764) _(code changed — re-verify)_
- `Diagnose()` (4× useful, score=3.834849511) _(code changed — re-verify)_
- `.handleKey()` (4× useful, score=3.760125369) _(code changed — re-verify)_
- `.Update()` (4× useful, score=3.759481649) _(code changed — re-verify)_
- `.handleViewKey()` (4× useful, score=3.695164213) _(code changed — re-verify)_
- `README.md` (4× useful, score=3.305140388)
- `Tool` (3× useful, score=2.644226441)
- `Install` (3× useful, score=2.325900354)
- `Status` (2× useful, score=1.987598059) _(code changed — re-verify)_
- `DowngradeEgress()` (2× useful, score=1.987598059) _(code changed — re-verify)_

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `model` connect `UI Model State` to `Probe Configuration Build`, `Network Connection Test Doubles`, `UI Application Toolbox`, `Job Execution Lifecycle`, `CancelFunc`?**
  _High betweenness centrality (0.101) - this node is a cross-community bridge._
- **Why does `New()` connect `Network Connection Test Doubles` to `Model Extra Tests`, `UI Model State`, `UI Application Toolbox`, `Probe Construction Tests`, `model`, `Cmd`, `CancelFunc`?**
  _High betweenness centrality (0.096) - this node is a cross-community bridge._
- **Why does `newModel()` connect `Model Extra Tests` to `Network Connection Test Doubles`, `UI Application Toolbox`, `Probe Construction Tests`, `model`, `CancelFunc`?**
  _High betweenness centrality (0.073) - this node is a cross-community bridge._
- **Are the 30 inferred relationships involving `newModel()` (e.g. with `TestInit()` and `TestDeferredQuit()`) actually correct?**
  _`newModel()` has 30 INFERRED edges - model-reasoned connections that need verification._
- **Are the 31 inferred relationships involving `mustTarget()` (e.g. with `TestDeferredQuit()` and `TestDeferredRestart()`) actually correct?**
  _`mustTarget()` has 31 INFERRED edges - model-reasoned connections that need verification._
- **Are the 11 inferred relationships involving `asModel()` (e.g. with `TestDeferredQuit()` and `TestDeferredRestart()`) actually correct?**
  _`asModel()` has 11 INFERRED edges - model-reasoned connections that need verification._
- **Are the 15 inferred relationships involving `toolsFor()` (e.g. with `TestToolBuildCurl()` and `TestToolBuildCurlScheme()`) actually correct?**
  _`toolsFor()` has 15 INFERRED edges - model-reasoned connections that need verification._