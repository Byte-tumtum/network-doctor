package ui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// scheduleMsg asks Update to dispatch any newly-runnable probes for generation
// gen. A stale gen is ignored.
type scheduleMsg struct{ gen int }

type noticeDoneMsg struct{ deadline time.Time }

// probeDoneMsg carries a finished probe's result. Accepted only when gen matches.
type probeDoneMsg struct {
	id  diagnostic.ProbeID
	gen int
	res diagnostic.ProbeResult
}

// lanNamesMsg carries OS-resolver hostnames for LAN-scan IPs. They override
// nmap's own reverse-DNS guesses in the network map (see networkHosts).
type lanNamesMsg struct {
	gen   int
	names map[string]string
}

// pendingAction is a state change deferred until the active job's terminal event
// arrives, so Update never blocks waiting on it (that would deadlock — Update is
// the goroutine that consumes the event).
type pendingKind int

const (
	pendQuit pendingKind = iota
	pendRestart
)

type pendingAction struct {
	kind   pendingKind
	target *diagnostic.Target
}

// jobState is one tool run's process and display state. The selected run is
// model.cur; unselected runs wait in otherJobs until Tab selects them.
type jobState struct {
	active  *job
	status  JobStatus
	name    string
	display string
	lines   []string
	dropped int64
	evicted int
	start   time.Time
	dur     time.Duration
}

const (
	maxJobLines  = 5000 // ring-buffer cap: older lines become a "discarded" count, not a memory bill
	jobTailLines = 14   // main-screen tail fallback when the terminal height is unknown
	ctrlCWindow  = 2 * time.Second
	noticeWindow = 4 * time.Second
	ctrlCNotice  = "Press Ctrl+C again to quit"
)

type model struct {
	target *diagnostic.Target
	probes []diagnostic.Probe

	// results + started are owned exclusively by Update; probe goroutines get an
	// immutable snapshot, never the live map.
	results map[diagnostic.ProbeID]diagnostic.ProbeResult
	started map[diagnostic.ProbeID]bool

	selected int
	// selMoved: the user touched the cursor this run, so completion must not
	// yank it to the first failure.
	selMoved    bool
	networkMap  bool
	mapSelected int
	networkCIDR string
	// hostNames maps discovered IPs to OS-resolved names; entries beat the
	// names nmap printed.
	hostNames map[string]string
	spinner   spinner.Model

	generation int
	// Generation context; cancel kills all in-flight probes and the active job on
	// restart/quit. Kept alive after the chain completes so tools can run under it.
	ctx    context.Context
	cancel context.CancelFunc

	// Drill-down job state (Phase 2).
	tools     []Tool
	pending   *pendingAction
	cur       jobState // the selected run; zero value means none yet
	otherJobs []jobState

	// Output viewport (Enter). follow sticks to the tail while output arrives;
	// scrolling up turns it off, scrolling back to the bottom re-enables it.
	viewing bool
	follow  bool
	vp      viewport.Model
	// Filter (/): while filtering, filterInput has focus and mirrors into
	// filter on every keystroke; the committed value stays applied until
	// esc while typing or leaving the viewer clears it.
	filtering   bool
	filter      string
	filterInput textinput.Model

	// Restart prompt (r): an editable netdoc command line. Enter parses
	// and restarts; esc closes without touching the current run. Up/down
	// walk this session's past targets, shell-style; histDraft keeps the
	// line being typed while browsing.
	entering  bool
	input     textinput.Model
	inputErr  string
	history   []string
	histIdx   int
	histDraft string
	histPath  string // ""; disables persistence (tests, or no config dir)

	// Confirm gate: a tool marked Confirm (nmap) is held here after its hotkey,
	// showing the exact command until 'y' runs it or esc cancels.
	confirmTool *Tool

	helping bool // ?: full-screen key cheatsheet; any key closes it

	toolbox bool // --toolbox: chain deferred until 'r'

	// notice is one-line feedback from export or the Ctrl+C quit hint.
	notice         string
	noticeOK       bool
	noticeDeadline time.Time

	width, height int
}

// The palette sticks to the 16 ANSI colors so it follows the user's terminal
// theme, and every status is also carried by a glyph or word — color is never
// the only signal (NO_COLOR and monochrome terminals stay usable).
var (
	accentColor = lipgloss.Color("6")
	borderColor = lipgloss.Color("8")

	passStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	failStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	skipStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	faintStyle      = lipgloss.NewStyle().Faint(true)
	titleStyle      = lipgloss.NewStyle().Bold(true)
	selStyle        = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	keyStyle        = lipgloss.NewStyle().Foreground(accentColor)
	panelStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Padding(0, 1)
	focusPanelStyle = panelStyle.BorderForeground(accentColor) // input focus lives here
	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	statusStyles    = map[fmt.Stringer]lipgloss.Style{
		diagnostic.StatusPass: passStyle, diagnostic.StatusWarn: warnStyle,
		diagnostic.StatusFail: failStyle, diagnostic.StatusSkip: skipStyle, diagnostic.StatusNA: faintStyle,
		JobDone: passStyle, JobFailed: failStyle, JobTimedOut: failStyle, JobCanceled: skipStyle,
	}
)

// New constructs the terminal application. histFile is where target history
// persists across sessions; "" keeps it in-memory only.
func New(t *diagnostic.Target, toolbox bool, histFile string) tea.Model {
	probes := diagnostic.BuildProbes(t)
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	m := model{
		target:   t,
		probes:   probes,
		results:  map[diagnostic.ProbeID]diagnostic.ProbeResult{},
		started:  map[diagnostic.ProbeID]bool{},
		tools:    toolsFor(t, runtime.GOOS),
		spinner:  sp,
		toolbox:  toolbox,
		histPath: histFile,
		width:    100, // placeholder until the terminal introduces itself (WindowSizeMsg)
	}
	m.history = loadHistory(histFile)
	if t != nil {
		if n := len(m.history); n == 0 || m.history[n-1] != t.Raw {
			m.history = append(m.history, t.Raw)
		}
		saveHistory(histFile, m.history) // launch targets count as history too
	}
	return m
}

// Target history persists as one line per target, oldest first. Everything
// here is best-effort: history is a convenience, never worth failing a run
// over, so errors just leave it empty.
const histMax = 50

func loadHistory(path string) []string {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var hist []string
	for line := range strings.SplitSeq(string(b), "\n") {
		// Clean guards the prompt against a garbled or tampered file.
		if line = strings.TrimSpace(textsafe.Clean(line)); line != "" {
			hist = append(hist, line)
		}
	}
	if len(hist) > histMax {
		hist = hist[len(hist)-histMax:]
	}
	return hist
}

func saveHistory(path string, hist []string) {
	if path == "" || len(hist) == 0 {
		return
	}
	if len(hist) > histMax {
		hist = hist[len(hist)-histMax:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(strings.Join(hist, "\n")+"\n"), 0o600)
}

func (m model) Init() tea.Cmd {
	if m.toolbox {
		return m.spinner.Tick // chain deferred until 'r'; tick drives the job timer
	}
	return tea.Batch(m.spinner.Tick, func() tea.Msg { return scheduleMsg{gen: 0} })
}

// chainRan reports whether the diagnostic chain has been started this session.
func (m model) chainRan() bool { return len(m.started) > 0 }

func (m model) allDone() bool { return len(m.results) == len(m.probes) }

// hasJob reports whether the current slot holds a job pane worth showing:
// either a running process or a finished/failed one still displaying output.
func (m model) hasJob() bool { return m.cur.active != nil || m.cur.status != JobQueued }

// reportReady reports whether every check has a result and no tool is running.
func (m model) reportReady() bool {
	return m.allDone() && !m.jobsRunning() && m.cur.status != JobRunning
}

// spinnerActive reports whether the spinner tick chain should keep running:
// while a started probe chain is pending or a drill-down job is live.
func (m model) spinnerActive() bool {
	return ((!m.toolbox || m.generation > 0 || m.chainRan()) && !m.allDone()) || m.jobsRunning()
}

// setNotice shows one-line feedback and schedules its expiry. The expiry tick
// carries the deadline it was armed with, so a leftover tick from an earlier
// notice can't blank a newer one — equality is the identity check.
func (m *model) setNotice(msg string, ok bool) tea.Cmd {
	window := noticeWindow
	if msg == ctrlCNotice {
		window = ctrlCWindow
	}
	m.notice, m.noticeOK = msg, ok
	m.noticeDeadline = time.Now().Add(window)
	deadline := m.noticeDeadline
	if m.viewing {
		m.refreshViewport()
	}
	return tea.Tick(window, func(time.Time) tea.Msg { return noticeDoneMsg{deadline: deadline} })
}

// Update is the only goroutine that touches model state; probes and jobs talk
// to it strictly through messages. Async messages carry the generation they
// were born in, so a restart doesn't have to chase them down — it just bumps
// the counter and lets the stale ones bounce off.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.viewing {
			m.refreshViewport()
		}
		return m, nil

	case tea.KeyMsg:
		if m.helping {
			m.helping = false
			return m, nil
		}
		// Ctrl+C while the confirm gate is up cancels the gate, not the app.
		if msg.Type == tea.KeyCtrlC && m.confirmTool != nil {
			return m.handleConfirmKey(msg)
		}
		if msg.Type == tea.KeyCtrlC {
			if m.notice == ctrlCNotice && time.Now().Before(m.noticeDeadline) {
				return m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
			}
			return m, m.setNotice(ctrlCNotice, false)
		}
		// Runes read from stdin in one batch arrive as a single KeyMsg
		// ("jjj"), which matches no binding; replay them one key at a time.
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			cur := tea.Model(m)
			for _, r := range msg.Runes {
				var cmd tea.Cmd
				cur, cmd = cur.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				cmds = append(cmds, cmd)
			}
			return cur, tea.Batch(cmds...)
		}
		if m.confirmTool != nil {
			return m.handleConfirmKey(msg)
		}
		if m.entering {
			return m.handlePromptKey(msg)
		}
		if m.viewing {
			return m.handleViewKey(msg)
		}
		return m.handleKey(msg)

	case noticeDoneMsg:
		if msg.deadline.Equal(m.noticeDeadline) {
			m.noticeDeadline = time.Time{}
			m.notice = ""
			if m.viewing {
				m.refreshViewport()
			}
		}
		return m, nil

	case scheduleMsg:
		if msg.gen != m.generation {
			return m, nil
		}
		if m.ctx == nil {
			m.ctx, m.cancel = context.WithCancel(context.Background())
		}
		return m, tea.Batch(m.scheduleStep()...)

	case probeDoneMsg:
		if msg.gen != m.generation {
			return m, nil // stale restart
		}
		res := msg.res
		res.ID = msg.id // scheduler identity wins over whatever the probe wrote on its own name tag
		m.results[msg.id] = res
		// scheduleStep first: it records skip results synchronously, which can
		// be what completes the run.
		cmds := m.scheduleStep()
		if m.allDone() {
			diagnostic.DowngradeEgress(m.results)
			if !m.selMoved && !m.viewing {
				for i, p := range m.probes {
					if m.results[p.ID].Status == diagnostic.StatusFail {
						m.selected = i
						break
					}
				}
			}
		}
		return m, tea.Batch(cmds...)

	case ToolOutputMsg:
		if msg.Generation != m.generation {
			return m, nil
		}
		if m.cur.active != nil && msg.JobID == m.cur.active.id {
			m.appendJobLine(msg.Line)
			if m.viewing {
				m.refreshViewport()
			}
			return m, waitForMsg(m.cur.active.ch)
		}
		for i := range m.otherJobs {
			j := &m.otherJobs[i]
			if j.active != nil && msg.JobID == j.active.id {
				appendJobLine(&j.lines, &j.evicted, msg.Line)
				return m, waitForMsg(j.active.ch)
			}
		}
		return m, nil

	case ToolDoneMsg:
		if msg.Generation != m.generation {
			return m, nil
		}
		var done *jobState
		if m.cur.active != nil && msg.JobID == m.cur.active.id {
			m.cur.status, m.cur.dropped, m.cur.active = msg.Status, msg.Dropped, nil
			m.cur.dur = time.Since(m.cur.start)
			done = &m.cur
		} else {
			for i := range m.otherJobs {
				j := &m.otherJobs[i]
				if j.active != nil && msg.JobID == j.active.id {
					j.status, j.dropped, j.active = msg.Status, msg.Dropped, nil
					j.dur = time.Since(j.start)
					done = j
					break
				}
			}
		}
		if done == nil {
			return m, nil
		}
		if m.pending != nil && !m.jobsRunning() {
			p := m.pending
			m.pending = nil
			return m.runPending(p)
		}
		if done.name == lanDiscoveryName && msg.Status == JobDone {
			if ips := discoveredIPs(done.lines); len(ips) > 0 {
				ctx, gen := m.ctx, m.generation
				return m, func() tea.Msg {
					names := diagnostic.ResolveNames(ctx, ips)
					// The user's own ssh aliases outrank whatever DNS thinks.
					maps.Copy(names, diagnostic.SSHHostAliases())
					return lanNamesMsg{gen: gen, names: names}
				}
			}
		}
		return m, nil

	case lanNamesMsg:
		if msg.gen != m.generation {
			return m, nil
		}
		if m.hostNames == nil {
			m.hostNames = msg.names
		} else {
			maps.Copy(m.hostNames, msg.names)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// Bubble Tea spinners run on a self-perpetuating tick: each TickMsg
		// schedules the next. Returning nil here ends the chain when nothing
		// is animating; launchTool/doRestart re-seed it (wasTicking) later.
		if !m.spinnerActive() {
			return m, nil
		}
		return m, cmd
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		if m.jobsRunning() {
			m.cancelJobs() // non-blocking; quit after every terminal event
			m.pending = &pendingAction{kind: pendQuit}
			return m, m.setNotice("stopping jobs, then quitting", true)
		}
		m.clearCancel()
		return m, tea.Quit
	case "r":
		// Open the restart prompt; an active job keeps streaming until Enter commits.
		m.entering, m.inputErr = true, ""
		ti := textinput.New()
		ti.Prompt = "netdoc "
		ti.Placeholder = "example.com:443 — empty for a general check"
		ti.PromptStyle = keyStyle
		if m.target != nil {
			ti.SetValue(m.target.Raw)
		}
		m.histIdx, m.histDraft = len(m.history), ""
		ti.Focus()
		ti.CursorEnd()
		m.input = ti
		return m, textinput.Blink
	case "v":
		if m.networkMap {
			m.networkMap = false
			return m, nil
		}
		if m.cur.name == lanDiscoveryName {
			m.networkMap = true
			return m, nil
		}
		// A scan parked in otherJobs still has a map; re-show it instead of
		// gating a fresh sweep.
		for i := range m.otherJobs {
			if m.otherJobs[i].name != lanDiscoveryName {
				continue
			}
			lan := m.otherJobs[i]
			if m.hasJob() {
				m.otherJobs[i] = m.cur
			} else {
				m.otherJobs = append(m.otherJobs[:i], m.otherJobs[i+1:]...)
			}
			m.cur = lan
			m.networkMap = true
			return m, nil
		}
		_, cidr := m.discoveryNetwork()
		if cidr == "" {
			return m, m.setNotice("local private IPv4 network not available yet", false)
		}
		tool := lanDiscoveryTool(quoterFor(runtime.GOOS), cidr)
		if _, err := toolLookPath(tool.Bin); err != nil {
			return m, m.setNotice("network discovery needs nmap", false)
		}
		tool.available = true
		m.networkCIDR = cidr
		// Same confirm gate as nmap: a /24 sweep is an active scan too.
		m.confirmTool = &tool
		return m, nil
	case "esc":
		// Cancel only the focused job (tab picks which); q remains the
		// nuke-everything path. The terminal event arrives as JobCanceled.
		if m.cur.active != nil && m.cur.active.cancel != nil {
			m.cur.active.cancel()
			return m, m.setNotice("canceling "+m.cur.name, true)
		}
		return m, nil
	case "tab":
		return m, m.switchJob()
	case "up", "k":
		if m.networkMap {
			if m.mapSelected > 0 {
				m.mapSelected--
			}
			return m, nil
		}
		if m.selected > 0 {
			m.selected--
		}
		m.selMoved = true
		return m, nil
	case "down", "j":
		if m.networkMap {
			if m.mapSelected < len(m.networkHosts())-1 {
				m.mapSelected++
			}
			return m, nil
		}
		if m.selected < len(m.probes)-1 {
			m.selected++
		}
		m.selMoved = true
		return m, nil
	case "enter":
		if hosts := m.networkHosts(); m.networkMap && len(hosts) > 0 {
			m.mapSelected = min(m.mapSelected, len(hosts)-1)
			address, _, _ := strings.Cut(hosts[m.mapSelected], " ")
			t, err := diagnostic.ParseTarget(address)
			if err != nil {
				return m, m.setNotice("invalid discovered target: "+err.Error(), false)
			}
			return m.restartWithTarget(t)
		}
		if !m.hasJob() {
			return m, m.setNotice("nothing to view yet", false)
		}
		m.viewing, m.follow = true, true
		m.vp = viewport.New(m.width, m.vpHeight())
		// Zero-value bindings disable everything else (b/f/space, u/d).
		m.vp.KeyMap = viewport.KeyMap{
			Up:       key.NewBinding(key.WithKeys("up", "k")),
			Down:     key.NewBinding(key.WithKeys("down", "j")),
			PageUp:   key.NewBinding(key.WithKeys("pgup")),
			PageDown: key.NewBinding(key.WithKeys("pgdown")),
		}
		m.refreshViewport()
		return m, nil
	case "y", "w":
		if !m.reportReady() {
			return m, m.setNotice("report not ready until checks finish", false)
		}
		notice, ok := exportReport(m.report(), msg.String() == "w")
		return m, m.setNotice(notice, ok)
	case "?":
		m.helping = true
		return m, nil
	}
	// Tool hotkeys (contextual toolbox).
	for _, tool := range m.tools {
		if msg.String() == tool.Key {
			if tool.Confirm {
				t := tool // hold for the confirm gate; run happens on 'y'
				m.confirmTool = &t
				return m, nil
			}
			return m, m.launchTool(tool)
		}
	}
	return m, nil
}

// handleConfirmKey handles keys while an advanced tool's command is shown: 'y'
// runs it (deferred if a job is still live), esc cancels, and anything else is
// ignored — a stray j/k mid-browse shouldn't silently eat the prompt.
func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		tool := *m.confirmTool
		m.confirmTool = nil
		return m, m.launchTool(tool)
	case "esc", "ctrl+c":
		m.confirmTool = nil
	}
	return m, nil
}

// handleViewKey handles keys while the output viewport is open. Everything not
// handled here scrolls the viewport; leaving the bottom disables follow mode.
func (m model) handleViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter = ""
			m.refreshViewport()
			return m, nil
		case "enter":
			m.filtering = false
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.filter = m.filterInput.Value()
		m.refreshViewport()
		return m, cmd
	}
	switch msg.String() {
	case "esc", "q":
		if msg.String() == "esc" && m.filter != "" {
			// First esc clears the committed filter, second one leaves.
			m.filter = ""
			m.refreshViewport()
			return m, nil
		}
		m.viewing = false
		m.filter = "" // a stale filter reopening as a blank screen reads as lost output
		return m, nil
	case "/":
		m.filtering = true
		ti := textinput.New()
		ti.Prompt = "/"
		ti.PromptStyle = keyStyle
		ti.SetValue(m.filter)
		ti.Focus()
		ti.CursorEnd()
		m.filterInput = ti
		m.refreshViewport()
		return m, textinput.Blink
	case "y":
		notice, ok := "output copied to clipboard", true
		if err := copyReport(strings.Join(m.visibleJobLines(), "\n")); err != nil {
			notice, ok = "copy failed: "+err.Error(), false
		}
		return m, m.setNotice(notice, ok)
	case "home":
		m.vp.GotoTop()
		m.follow = false
		return m, nil
	case "end":
		m.vp.GotoBottom()
		m.follow = true
		return m, nil
	case "tab":
		return m, m.switchJob()
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	m.follow = m.vp.AtBottom()
	return m, cmd
}

// handlePromptKey handles keys while the restart prompt is open. Enter parses
// the line and restarts (deferred if a job is still running), esc closes, and
// everything else edits the input.
func (m model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.entering = false
		return m, nil
	case "enter":
		t, err := parseRunArgs(m.input.Value())
		if err != nil {
			m.inputErr = err.Error()
			return m, nil
		}
		if v := strings.TrimSpace(m.input.Value()); v != "" {
			if n := len(m.history); n == 0 || m.history[n-1] != v {
				m.history = append(m.history, v)
				saveHistory(m.histPath, m.history)
			}
		}
		m.entering = false
		return m.restartWithTarget(t)
	case "up":
		if m.histIdx == 0 {
			return m, nil
		}
		if m.histIdx == len(m.history) {
			m.histDraft = m.input.Value()
		}
		m.histIdx--
		m.input.SetValue(m.history[m.histIdx])
		m.input.CursorEnd()
		return m, nil
	case "down":
		if m.histIdx >= len(m.history) {
			return m, nil
		}
		m.histIdx++
		if m.histIdx == len(m.history) {
			m.input.SetValue(m.histDraft)
		} else {
			m.input.SetValue(m.history[m.histIdx])
		}
		m.input.CursorEnd()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.inputErr = ""
	return m, cmd
}

func (m model) restartWithTarget(t *diagnostic.Target) (tea.Model, tea.Cmd) {
	if m.jobsRunning() {
		m.cancelJobs()
		m.pending = &pendingAction{kind: pendRestart, target: t}
		return m, nil
	}
	m.applyTarget(t)
	return m, m.doRestart()
}

// parseRunArgs parses the restart prompt as a netdoc command line: an optional
// leading binary name, then at most one target argument. An
// empty line means a general, targetless run.
func parseRunArgs(line string) (*diagnostic.Target, error) {
	fields := strings.Fields(line)
	if len(fields) > 0 && (fields[0] == "netdoc" || fields[0] == "network-doctor") {
		fields = fields[1:]
	}
	switch {
	case len(fields) == 0:
		return nil, nil
	case len(fields) > 1:
		return nil, errors.New("one target only, e.g. example.com:443")
	case strings.HasPrefix(fields[0], "-"):
		return nil, errors.New("flags aren't supported here — enter a target")
	}
	return diagnostic.ParseTarget(fields[0])
}

// applyTarget swaps the run target and rebuilds its probes.
func (m *model) applyTarget(t *diagnostic.Target) {
	m.target = t
	m.probes = diagnostic.BuildProbes(t)
	m.selected = 0
}

func (m model) runPending(p *pendingAction) (tea.Model, tea.Cmd) {
	switch p.kind {
	case pendQuit:
		m.clearCancel()
		return m, tea.Quit
	case pendRestart:
		m.applyTarget(p.target)
		return m, m.doRestart()
	}
	return m, nil
}

// doRestart bumps the generation (invalidating outstanding probe/job messages),
// clears run state and old tool output, resets the context, and reschedules
// from the root.
func (m *model) doRestart() tea.Cmd {
	wasTicking := m.spinnerActive()
	m.clearCancel()
	m.ctx = nil
	m.tools = toolsFor(m.target, runtime.GOOS)
	m.generation++
	m.selMoved = false
	m.results = map[diagnostic.ProbeID]diagnostic.ProbeResult{}
	m.started = map[diagnostic.ProbeID]bool{}
	m.pending, m.confirmTool = nil, nil
	m.cur, m.otherJobs = jobState{}, nil
	m.networkMap, m.mapSelected, m.networkCIDR = false, 0, ""
	m.notice = ""
	if m.viewing {
		m.refreshViewport()
	}
	gen := m.generation
	cmds := []tea.Cmd{func() tea.Msg { return scheduleMsg{gen: gen} }}
	if !wasTicking {
		cmds = append(cmds, m.spinner.Tick)
	}
	return tea.Batch(cmds...)
}

func (m *model) launchTool(tool Tool) tea.Cmd {
	m.stashJob()
	m.networkMap = tool.Key == "v"
	if m.networkMap {
		m.mapSelected = 0
	}
	if !tool.Available() {
		m.cur.name, m.cur.status = tool.Name, JobFailed
		m.cur.lines, m.cur.dropped, m.cur.evicted = []string{tool.Bin + " not found — install it"}, 0, 0
		m.cur.dur = 0
		m.cur.display = tool.Name
		return nil
	}
	wasTicking := m.spinnerActive()
	args, env, display := tool.Build(m.target)
	id := fmt.Sprintf("%s-%d-%d", tool.Key, m.generation, time.Now().UnixNano())
	// Toolbox mode: a tool can launch before the first 'r' creates the
	// generation context — initialize it lazily, exactly as scheduleMsg does.
	if m.ctx == nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
	}
	j, cmd, err := startTool(m.ctx, m.generation, id, tool.Bin, args, env, tool.Timeout)
	if err != nil {
		m.cur.name, m.cur.status = tool.Name, JobFailed
		m.cur.lines, m.cur.dropped, m.cur.evicted = []string{textsafe.Clean(err.Error())}, 0, 0
		m.cur.display, m.cur.dur = display, 0
		return nil
	}
	m.cur.active, m.cur.status = j, JobRunning
	m.cur.lines, m.cur.dropped, m.cur.evicted = nil, 0, 0
	if m.viewing {
		m.refreshViewport()
	}
	m.cur.name, m.cur.display, m.cur.start = tool.Name, display, time.Now()
	if !wasTicking {
		return tea.Batch(cmd, m.spinner.Tick)
	}
	return cmd
}

func (m *model) stashJob() {
	if m.hasJob() {
		m.otherJobs = append(m.otherJobs, m.cur)
		m.cur = jobState{}
	}
}

func (m *model) switchJob() tea.Cmd {
	if len(m.otherJobs) == 0 {
		return nil
	}
	next := m.otherJobs[0]
	m.otherJobs = append(m.otherJobs[1:], m.cur)
	m.cur = next
	m.networkMap = false
	if m.viewing {
		m.follow = true
	}
	// Keep the armed quit intact so the next Ctrl+C still quits.
	if m.notice == ctrlCNotice && time.Now().Before(m.noticeDeadline) {
		if m.viewing {
			m.refreshViewport()
		}
		return nil
	}
	return m.setNotice("switched to "+m.cur.name, true)
}

func (m model) jobsRunning() bool {
	if m.cur.active != nil {
		return true
	}
	for _, j := range m.otherJobs {
		if j.active != nil {
			return true
		}
	}
	return false
}

func (m *model) cancelJobs() {
	if m.cur.active != nil && m.cur.active.cancel != nil {
		m.cur.active.cancel()
	}
	for _, j := range m.otherJobs {
		if j.active != nil && j.active.cancel != nil {
			j.active.cancel()
		}
	}
}

// appendJobLine appends one output line to the ring buffer, counting evictions
// separately from channel-overflow drops (jobDropped) so the viewport context
// line stays accurate.
func (m *model) appendJobLine(text string) {
	oldLen := len(m.cur.lines)
	var evictedLine string
	if oldLen == maxJobLines {
		evictedLine = m.cur.lines[0]
	}
	appendJobLine(&m.cur.lines, &m.cur.evicted, text)
	// Only correct the offset when the evicted line was actually visible: under
	// a filter that hides it, the filtered view lost nothing at the top.
	if len(m.cur.lines) == oldLen && m.viewing && !m.follow && matchesFilter(evictedLine, m.filter) {
		h := lipgloss.Height(lipgloss.NewStyle().Width(m.width).Render(evictedLine))
		m.vp.SetYOffset(m.vp.YOffset - h)
	}
}

func appendJobLine(lines *[]string, evicted *int, text string) {
	*lines = append(*lines, text)
	if n := len(*lines) - maxJobLines; n > 0 {
		*evicted += n
		*lines = (*lines)[n:]
	}
}

// visibleJobLines is the selected run's output after the viewer filter:
// what the viewport shows and what 'y' copies.
func (m model) visibleJobLines() []string {
	return filterLines(m.cur.lines, m.filter)
}

// filterLines keeps the lines containing f, case-insensitively; an empty f
// keeps everything.
// ponytail: substring only — regex when someone actually asks for it.
func filterLines(lines []string, f string) []string {
	if f == "" {
		return lines
	}
	var out []string
	for _, ln := range lines {
		if matchesFilter(ln, f) {
			out = append(out, ln)
		}
	}
	return out
}

// matchesFilter reports whether ln survives the viewer filter; an empty filter
// matches everything.
func matchesFilter(ln, f string) bool {
	return f == "" || strings.Contains(strings.ToLower(ln), strings.ToLower(f))
}

// jobContent renders the interleaved stream wrapped to the viewport width.
// Line numbers in the context line refer to these wrapped display lines.
func (m model) jobContent() string {
	w := m.width
	lines := m.visibleJobLines()
	if len(lines) == 0 {
		empty := "(no output yet)"
		if m.filter != "" {
			empty = "(no lines match)"
		}
		return lipgloss.NewStyle().Width(w).Render(faintStyle.Render(empty))
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

// refreshViewport resizes and re-renders the open viewport, sticking to the
// tail in follow mode.
// ponytail: full content rebuild per line while open; fine at the 5000-line
// cap, switch to incremental append if it ever lags.
func (m *model) refreshViewport() {
	m.vp.Width, m.vp.Height = m.width, m.vpHeight()
	m.vp.SetContent(m.jobContent())
	if m.follow {
		m.vp.GotoBottom()
	}
}

func (m model) vpHeight() int {
	if m.height <= 0 {
		return 20
	}
	h := m.height - 3 - lipgloss.Height(m.viewerFooter()) // header + status above, context below
	if h < 3 {
		h = 3
	}
	return h
}

func (m *model) clearCancel() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}
