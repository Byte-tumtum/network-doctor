# Graph Report - network-doctor  (2026-07-29)

## Corpus Check
- 107 files · ~153,298 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 904 nodes · 1736 edges · 112 communities (71 shown, 41 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 275 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `25bc2a42`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- Model Extra Tests
- UI Model State
- Probe Configuration Build
- Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]
- Network Connection Test Doubles
- ExitCode
- Job Execution Lifecycle
- Probe Construction Tests
- CLI Reporting Entry Point
- Architecture Decision Memory
- ssid
- Gateway Route Parsing
- TestFixHintsPerGOOS
- model
- Viewer Feedback Improvements
- Platform Fix Hints
- ExitCode
- Integration Network Checks
- Failure Details Navigation
- Job Output Simplification
- toolsFor
- Other Process Groups
- Unix Process Groups
- Windows Process Groups
- TestFixHintsPerGOOS
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
- advertisedNames
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
- Q: Does it work in complex network setups like Openstack, SDN (OvS, OpenDaylight), etc?
- parseSSHAliases
- Q: Commit the existing fix for canceling a running job with Esc; do not edit Go files.
- TestFixHintsPerGOOS
- CancelFunc
- Cmd
- report_test.go
- Cmd
- IP
- IP
- T
- model

## God Nodes (most connected - your core abstractions)
1. `newModel()` - 81 edges
2. `asModel()` - 55 edges
3. `mustTarget()` - 44 edges
4. `ProbeID` - 30 edges
5. `ProbeResult` - 30 edges
6. `keyMsg()` - 30 edges
7. `netops` - 28 edges
8. `model` - 27 edges
9. `model` - 25 edges
10. `Diagnose()` - 21 edges

## Surprising Connections (you probably didn't know these)
- `buildReport()` --calls--> `Ms()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `run()` --calls--> `SourceIP()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `runJSON()` --calls--> `BuildProbesFrom()`  [INFERRED]
  main.go → internal/diagnostic/checks.go
- `wrapRun()` --calls--> `run()`  [INFERRED]
  internal/diagnostic/checks.go → main.go
- `TestWrapRunTimesAndCleans()` --calls--> `run()`  [INFERRED]
  internal/diagnostic/checks_test.go → main.go

## Import Cycles
- None detected.

## Communities (112 total, 41 thin omitted)

### Community 0 - "Model Extra Tests"
Cohesion: 0.09
Nodes (84): T, TestAppendJobLine(), TestClassifyJob(), TestConcurrentToolsCanSwitch(), TestDeferredQuit(), TestDeferredRestart(), TestDeferredRestartDefersTargetSwap(), TestEscClearsCommittedFilterBeforeClosing() (+76 more)

### Community 1 - "UI Model State"
Cohesion: 0.07
Nodes (27): AdvertisedNames(), Context, ReverseName(), Reader, parseSSHAliases(), SSHHostAliases(), T, TestParseSSHAliases() (+19 more)

### Community 2 - "Probe Configuration Build"
Cohesion: 0.07
Nodes (59): CertPool, Config, Attempt, netops, Portal, Probe, ProbeID, ProbeResult (+51 more)

### Community 3 - "Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78]"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: 6. shrink: osc52Mode — switch with one case. if os.Getenv("TMUX") != "" { … }. -4 lines (moot if #2 lands). [internal/ui/report.go:78], Source Nodes

### Community 4 - "Network Connection Test Doubles"
Cohesion: 0.09
Nodes (38): Builder, fakeConn, scriptConn, Addr, Conn, Context, Reader, T (+30 more)

### Community 5 - "ExitCode"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Hold on, please fix my program so that it would to display the advertised name of 192.168.1.79 and other devices too, Source Nodes

### Community 6 - "Job Execution Lifecycle"
Cohesion: 0.14
Nodes (27): classifyJob(), CancelFunc, Cmd, Context, Duration, Msg, Reader, readCappedLine() (+19 more)

### Community 7 - "Probe Construction Tests"
Cohesion: 0.14
Nodes (23): silentConn, T, Time, TestBannerProbeReadTimeout(), TestBannerProbeReadTimeoutHonorsContext(), TestBannerProbeValidatesProtocol(), TestDialIPsCancelledStopsEarly(), TestDialIPsRacesStaggered() (+15 more)

### Community 8 - "CLI Reporting Entry Point"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I want the user to be able to press v to visualize the network, Source Nodes

### Community 9 - "Architecture Decision Memory"
Cohesion: 0.12
Nodes (16): Arch Linux (AUR), Built with, Development, Drill-down tools, Everywhere else, Exit codes, How it diagnoses, Install (+8 more)

### Community 10 - "ssid"
Cohesion: 0.06
Nodes (32): advertisedName, iwreq, F, avahiTXT(), advertisedNames(), Context, parseAvahiNames(), unescapeAvahi() (+24 more)

### Community 11 - "Gateway Route Parsing"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Confirm that the problem is fixed, and that there is no new issue because of the fix., Source Nodes

### Community 12 - "TestFixHintsPerGOOS"
Cohesion: 0.13
Nodes (8): discoveredIPs(), IP, model, helpKeys(), joinChips(), probeGlyph(), progressBar(), upHostLine()

### Community 13 - "model"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Restore the green verification baseline by resolving one unchecked cleanup and four intentional Unicode test literals so lint runs through vulnerability and release checks., Source Nodes

### Community 15 - "Platform Fix Hints"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: shrink: winSafe() GOOS branch — unconditional strings.ToValidUTF8(line, "?") is one line, consistent across platforms, Source Nodes

### Community 16 - "ExitCode"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Tell me how to get more people to this project., Source Nodes

### Community 17 - "Integration Network Checks"
Cohesion: 0.53
Nodes (5): T, TestDialFromSourceLoopback(), TestDialIPsLoopback(), TestDialIPsRefusedLoopback(), TestPathIdentityLoopback()

### Community 20 - "toolsFor"
Cohesion: 0.19
Nodes (27): cacheAvailability(), curlTool(), Duration, lanDiscoveryTool(), nmapTool(), psArgs(), quoterFor(), shellArgs() (+19 more)

### Community 21 - "Other Process Groups"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 22 - "Unix Process Groups"
Cohesion: 0.67
Nodes (3): Cmd, killGroup(), setProcGroup()

### Community 24 - "TestFixHintsPerGOOS"
Cohesion: 0.25
Nodes (12): Certificate, certNames(), certWindow(), dnsFix(), ifaceFix(), timeoutError(), tlsFix(), T (+4 more)

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

### Community 87 - "Target"
Cohesion: 0.19
Nodes (22): T, mustTarget(), TestBuildProbesNamesProtocolApplicationRow(), TestBuildProbesShape(), TestSSIDDoesNotGateNetworkProbes(), TestWrapRunTimesAndCleans(), Diagnose(), T (+14 more)

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
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I just fixed an issue. Commit it. Here was the problem: Committed filter can't be cleared without leaving viewer; to clear filter while staying, you must press / then esc. No hint anywhere., Source Nodes

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
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: My friend said this about my program: Nice approach. One thing I would find especially useful is making the stages explicit as DNS → route → TCP → TLS → HTTP and treating ICMP as advisory, since many healthy hosts drop ping. A --json output with timings, resolved IPs, exit codes and the first failing stage would also make it handy for CI and bug reports. Do you already distinguish service unavailable from network path unavailable in that way?, Source Nodes

### Community 98 - "parseESSID"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Pressing v will run a LAN Scan, not prompt to user to run a LAN Scan. Also update the text to say that pressing v runs a network map and then it says lan scan., Source Nodes

### Community 99 - "Q: Does it work in complex network setups like Openstack, SDN (OvS, OpenDaylight), etc?"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Does it work in complex network setups like Openstack, SDN (OvS, OpenDaylight), etc?, Source Nodes

### Community 100 - "parseSSHAliases"
Cohesion: 0.09
Nodes (31): Proto, Target, FileMode, IP, parsePort(), ParseTarget(), T, TestParseTarget() (+23 more)

### Community 101 - "Q: Commit the existing fix for canceling a running job with Esc; do not edit Go files."
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Commit the existing fix for canceling a running job with Esc; do not edit Go files., Source Nodes

### Community 102 - "TestFixHintsPerGOOS"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Canonicalize targets before history or rendering, Source Nodes

### Community 103 - "CancelFunc"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Commit the existing fix for network map Enter panic after the host list shrinks; do not edit Go files., Source Nodes

### Community 104 - "Cmd"
Cohesion: 0.67
Nodes (3): T, TestKillGroupNilProcess(), TestKillGroupReapedLeaderFallsBack()

### Community 105 - "report_test.go"
Cohesion: 0.39
Nodes (6): decodeToolOutput(), decodeWindowsCodePage(), isWindowsOEMTool(), T, TestDecodeWindowsCodePage(), TestWindowsOEMToolSelection()

### Community 108 - "Cmd"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: I believe the project is ripe enough to push a 1.8.3 release, Source Nodes

### Community 109 - "IP"
Cohesion: 0.09
Nodes (35): Buffer, FlagSet, ExitCode(), Model, T, TestToolboxExitZero(), TestToolsFor(), buildReport() (+27 more)

### Community 110 - "IP"
Cohesion: 0.40
Nodes (4): Answer, Outcome, Q: Expose existing target-history navigation in the restart footer., Source Nodes

### Community 114 - "model"
Cohesion: 0.33
Nodes (4): appendJobLine(), filterLines(), model, matchesFilter()

## Knowledge Gaps
- **199 isolated node(s):** `github.com/heymaikol/network-doctor`, `advertisedName`, `iwreq`, `ToolOutputMsg`, `scheduleMsg` (+194 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **41 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Work-memory lessons

**Preferred sources** — corroborated by past sessions; start here.
- `Diagnose()` (7× useful, score=5.596632691)
- `README.md` (6× useful, score=4.323052749) _(code changed — re-verify)_
- `.handleViewKey()` (5× useful, score=3.761584662)
- `.outputView()` (5× useful, score=3.610113139)
- `.Update()` (4× useful, score=2.907893707)
- `JSON output` (3× useful, score=2.502848761) _(code changed — re-verify)_
- `.viewerFooter()` (3× useful, score=2.382928562)
- `DowngradeEgress()` (3× useful, score=2.327593376)
- `report` (3× useful, score=2.218353669) _(code changed — re-verify)_
- `Install` (3× useful, score=1.799043494) _(code changed — re-verify)_

**Known dead ends** — questions that led nowhere; don't re-derive.
- "What do you think? Accept the proposed Linux/macOS/Windows toolbox table plus Windows pathping for m and nslookup for d?" -> `Target`, `BuildProbes`

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Target` connect `parseSSHAliases` to `Model Extra Tests`, `UI Model State`, `Probe Configuration Build`, `IP`, `toolsFor`, `Target`?**
  _High betweenness centrality (0.076) - this node is a cross-community bridge._
- **Why does `newModel()` connect `Model Extra Tests` to `UI Model State`, `parseSSHAliases`, `IP`, `T`, `toolsFor`?**
  _High betweenness centrality (0.075) - this node is a cross-community bridge._
- **Why does `Clean()` connect `ssid` to `UI Model State`, `Probe Configuration Build`, `parseSSHAliases`, `Job Execution Lifecycle`, `IP`?**
  _High betweenness centrality (0.063) - this node is a cross-community bridge._
- **Are the 40 inferred relationships involving `newModel()` (e.g. with `TestInit()` and `TestConcurrentToolsCanSwitch()`) actually correct?**
  _`newModel()` has 40 INFERRED edges - model-reasoned connections that need verification._
- **Are the 20 inferred relationships involving `asModel()` (e.g. with `TestConcurrentToolsCanSwitch()` and `TestDeferredQuit()`) actually correct?**
  _`asModel()` has 20 INFERRED edges - model-reasoned connections that need verification._
- **Are the 36 inferred relationships involving `mustTarget()` (e.g. with `TestConcurrentToolsCanSwitch()` and `TestDeferredQuit()`) actually correct?**
  _`mustTarget()` has 36 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/heymaikol/network-doctor`, `advertisedName`, `iwreq` to the rest of the system?**
  _209 weakly-connected nodes found - possible documentation gaps or missing edges._