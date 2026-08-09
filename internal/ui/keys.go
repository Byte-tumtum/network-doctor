// Key handling and the actions keys dispatch: restarts, tool launches, and
// job selection/cancellation. Handlers take a value receiver and return the
// updated model; the action helpers take a pointer and mutate in place.

package ui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

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
		m.entering, m.sshPrompt, m.inputErr = true, false, ""
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
	case "S":
		// The SSH login form is offered for every target, unlike the 'c'
		// handshake check, which only fits an SSH one. It logs in to the
		// machine under test, so it needs a target and takes the host from it.
		if m.target == nil {
			return m, m.setNotice("SSH needs a target — press r to set one", false)
		}
		if _, err := toolLookPath("ssh"); err != nil {
			return m, m.setNotice("ssh not found — install an OpenSSH client", false)
		}
		m.sshPrompt, m.ssh = true, newSSHForm(m.target)
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
		// A scan parked in the ring still has a map; re-show it instead of
		// gating a fresh sweep.
		for i := range m.otherJobs {
			if m.otherJobs[i].name != lanDiscoveryName {
				continue
			}
			m.selectJob(i)
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
		tool.Available = true
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
		if portalURL := m.selectedPortalURL(); portalURL != "" && msg.String() == "y" {
			notice, ok := "portal URL sent to clipboard (OSC 52)", true
			if err := copyReport(portalURL); err != nil {
				notice, ok = "copy failed: "+err.Error(), false
			}
			return m, m.setNotice(notice, ok)
		}
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
		notice, ok := "output sent to clipboard (OSC 52) — w saves a file", true
		if err := copyReport(strings.Join(m.visibleJobLines(), "\n")); err != nil {
			notice, ok = "copy failed: "+err.Error(), false
		}
		return m, m.setNotice(notice, ok)
	case "w":
		notice, ok := exportReport(strings.Join(m.visibleJobLines(), "\n"), true)
		notice = strings.Replace(notice, "report saved", "output saved", 1)
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
	m.probes = diagnostic.BuildProbesFromSources(t, m.sources)
	m.selected = 0
	m.runHistory = map[diagnostic.ProbeID][]diagnostic.Status{}
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
	m.pending, m.confirmTool, m.sshPrompt = nil, nil, false
	m.dropJobs()
	m.networkMap, m.mapSelected, m.networkCIDR = false, 0, ""
	m.hostNames, m.namesPending = nil, nil
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
	// Refuse before stashing — stashJob clears the slot, so a late bail would
	// blank the pane the user is looking at. A missing binary spawns nothing,
	// so "not found" still gets through at the limit.
	if tool.Available && m.runningJobs() >= maxActiveJobs {
		return m.setNotice(fmt.Sprintf("%d tools already running — wait for one to finish", maxActiveJobs), false)
	}
	m.stashJob()
	m.networkMap = tool.Key == "v"
	if m.networkMap {
		m.mapSelected = 0
	}
	if !tool.Available {
		m.cur.name, m.cur.status = tool.Name, JobFailed
		m.cur.lines, m.cur.dropped, m.cur.evicted = []string{tool.Bin + " not found — install it"}, 0, 0
		m.cur.dur = 0
		m.cur.display = tool.Name
		return nil
	}
	wasTicking := m.spinnerActive()
	args, env, display := tool.Build(m.target, m.selectedIP())
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
