package ui

import (
	"context"
	"fmt"
	"maps"
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

func (m *model) clearCancel() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}
