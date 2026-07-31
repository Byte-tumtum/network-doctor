package ui

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

// watchMsg starts the next pass after a completed watched run.
type watchMsg struct{ gen int }

type noticeDoneMsg struct{ deadline time.Time }

// probeDoneMsg carries a finished probe's result. Accepted only when gen matches.
type probeDoneMsg struct {
	id  diagnostic.ProbeID
	gen int
	res diagnostic.ProbeResult
}

// lanNamesMsg carries the advertised names and ssh aliases for the LAN-scan
// IPs, which arrive as one batch. They override nmap's own reverse-DNS guesses
// in the network map (see networkHosts). ips is the full scanned set, so Update
// knows which addresses still need a reverse lookup.
type lanNamesMsg struct {
	gen   int
	ips   []string
	names map[string]string
}

// lanNameMsg is one address's reverse-DNS result, empty name included: it also
// means "stop spinning for this row".
type lanNameMsg struct {
	gen  int
	ip   string
	name string
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
	watchEvery   = 5 * time.Second
	watchRuns    = 20
	ctrlCNotice  = "Press Ctrl+C again to quit"
)

type model struct {
	target  *diagnostic.Target
	probes  []diagnostic.Probe
	source  net.IP
	version string

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
	// hostNames maps discovered IPs to resolved or advertised names; entries
	// beat the names nmap printed.
	hostNames map[string]string
	// namesPending holds the IPs whose name lookup is still in flight; those
	// rows show a spinner instead of nmap's PTR guess.
	namesPending map[string]bool
	spinner      spinner.Model

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

	// SSH login form (S): host, username, key file, password. Enter hands the
	// terminal to ssh; esc closes it. Every S starts a fresh form, so a typed
	// password lives only as long as the form is open.
	sshPrompt bool
	ssh       sshForm

	// Confirm gate: a tool marked Confirm (nmap) is held here after its hotkey,
	// showing the exact command until 'y' runs it or esc cancels.
	confirmTool *Tool

	helping bool // ?: full-screen key cheatsheet; any key closes it

	toolbox    bool // --toolbox: chain deferred until 'r'
	watch      bool
	runHistory map[diagnostic.ProbeID][]diagnostic.Status

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

// NewWithSource constructs the terminal application with probe dials pinned to
// source; a nil source leaves them unpinned. histFile is where target history
// persists across sessions; "" keeps it in-memory only.
func NewWithSource(t *diagnostic.Target, source net.IP, toolbox, watch bool, histFile, version string) tea.Model {
	probes := diagnostic.BuildProbesFrom(t, source)
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	m := model{
		target:     t,
		probes:     probes,
		source:     source,
		results:    map[diagnostic.ProbeID]diagnostic.ProbeResult{},
		started:    map[diagnostic.ProbeID]bool{},
		tools:      toolsFor(t, runtime.GOOS),
		spinner:    sp,
		toolbox:    toolbox,
		watch:      watch,
		runHistory: map[diagnostic.ProbeID][]diagnostic.Status{},
		histPath:   histFile,
		version:    version,
		width:      100, // placeholder until the terminal introduces itself (WindowSizeMsg)
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	// os.WriteFile would follow a symlink planted at path and truncate whatever
	// it points at. Renaming a sibling temp file over the name replaces the link
	// itself, and costs nothing beyond the write we were doing anyway.
	f, err := os.CreateTemp(dir, "history-*") // 0600 by definition
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(f.Name()) }() // no-op once the rename lands
	if _, err := f.WriteString(strings.Join(hist, "\n") + "\n"); err != nil {
		f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}
	_ = os.Rename(f.Name(), path)
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

// sshDetected reports whether this run found an SSH server to log in to: the
// banner probe passed, i.e. the port answered with an SSH- identification
// string. A warn is deliberately not enough — that one means the port answered
// with silence, and ssh always speaks first.
func (m model) sshDetected() bool {
	return m.results[diagnostic.ProbeSSH].Status == diagnostic.StatusPass
}

// spinnerActive reports whether the spinner tick chain should keep running:
// while a started probe chain is pending or a drill-down job is live.
func (m model) spinnerActive() bool {
	return ((!m.toolbox || m.generation > 0 || m.chainRan()) && !m.allDone()) || m.jobsRunning() || len(m.namesPending) > 0
}

// setNotice shows one-line feedback and schedules its expiry. The expiry tick
// carries the deadline it was armed with, so a leftover tick from an earlier
// notice can't blank a newer one — equality is the identity check.
func (m *model) setNotice(msg string, ok bool) tea.Cmd {
	window := noticeWindow
	if msg == ctrlCNotice {
		window = ctrlCWindow
	}
	m.notice, m.noticeOK = textsafe.Clean(msg), ok
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
		if m.sshPrompt {
			return m.handleSSHKey(msg)
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

	case sshDoneMsg:
		// The session's own screen is gone the moment the TUI repaints, so
		// whatever ssh said lands in a job pane like any other tool's output.
		m.stashJob()
		m.cur = jobState{name: "SSH login", display: msg.display, status: JobDone}
		for line := range strings.SplitSeq(strings.TrimRight(msg.output, "\n"), "\n") {
			if line != "" {
				m.appendJobLine(textsafe.Clean(line))
			}
		}
		notice := "ssh session ended"
		if msg.err != nil {
			m.cur.status = JobFailed
			m.appendJobLine(textsafe.Clean(msg.err.Error()))
			notice = "ssh failed — press enter for the output"
		}
		if m.viewing {
			m.refreshViewport()
		}
		return m, m.setNotice(notice, msg.err == nil)

	case scheduleMsg:
		if msg.gen != m.generation {
			return m, nil
		}
		if m.ctx == nil {
			m.ctx, m.cancel = context.WithCancel(context.Background())
		}
		return m, tea.Batch(m.scheduleStep()...)

	case watchMsg:
		if !m.watch || msg.gen != m.generation || !m.allDone() {
			return m, nil
		}
		if m.jobsRunning() {
			return m, m.watchCmd()
		}
		cur, other := m.cur, m.otherJobs
		cmd := m.doRestart()
		m.cur, m.otherJobs = cur, other
		if m.viewing {
			m.refreshViewport()
		}
		return m, cmd

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
			diagnostic.ReconcileDNS(m.results)
			diagnostic.DowngradeEgress(m.results)
			if !m.selMoved && !m.viewing {
				for i, p := range m.probes {
					if m.results[p.ID].Status == diagnostic.StatusFail {
						m.selected = i
						break
					}
				}
			}
			if m.watch {
				m.recordRun()
				cmds = append(cmds, m.watchCmd())
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
				j.appendLine(msg.Line)
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
				m.namesPending = make(map[string]bool, len(ips))
				for _, ip := range ips {
					m.namesPending[ip] = true
				}
				return m, func() tea.Msg {
					names := diagnostic.AdvertisedNames(ctx, ips)
					// The user's own ssh aliases outrank whatever DNS thinks.
					maps.Copy(names, diagnostic.SSHHostAliases())
					return lanNamesMsg{gen: gen, ips: ips, names: names}
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
		// Reverse DNS runs only for what advertised nothing, one command per
		// address: each row stops spinning as its own name lands, and no row
		// ever shows a PTR guess that an advertised name would then replace.
		// ponytail: unbounded fan-out — a /24 tops out at a couple dozen Up hosts.
		var cmds []tea.Cmd
		for _, ip := range msg.ips {
			if m.hostNames[ip] != "" {
				delete(m.namesPending, ip)
				continue
			}
			ctx, gen := m.ctx, m.generation
			cmds = append(cmds, func() tea.Msg {
				return lanNameMsg{gen: gen, ip: ip, name: diagnostic.ReverseName(ctx, ip)}
			})
		}
		return m, tea.Batch(cmds...)

	case lanNameMsg:
		if msg.gen != m.generation {
			return m, nil
		}
		delete(m.namesPending, msg.ip)
		if msg.name != "" {
			if m.hostNames == nil {
				m.hostNames = map[string]string{}
			}
			m.hostNames[msg.ip] = msg.name
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

func (m *model) clearCancel() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *model) recordRun() {
	for _, p := range m.probes {
		history := append(m.runHistory[p.ID], m.results[p.ID].Status)
		if len(history) > watchRuns {
			history = history[len(history)-watchRuns:]
		}
		m.runHistory[p.ID] = history
	}
}

func (m model) watchCmd() tea.Cmd {
	gen := m.generation
	return tea.Tick(watchEvery, func(time.Time) tea.Msg { return watchMsg{gen: gen} })
}
