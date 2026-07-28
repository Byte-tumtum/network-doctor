// Rendering: the View tree and its helpers. Nothing here mutates the
// model — every function takes a value receiver and returns a string.

package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func (m model) glyph(id diagnostic.ProbeID) string {
	r, ok := m.results[id]
	if !ok {
		if !m.started[id] {
			return faintStyle.Render("·")
		}
		return m.spinner.View()
	}
	return statusStyles[r.Status].Render(probeGlyph(r.Status))
}

func probeGlyph(s diagnostic.Status) string {
	if s < diagnostic.StatusPass || s > diagnostic.StatusNA {
		return "?"
	}
	return [...]string{"✓", "!", "✗", "⊘", "–"}[s]
}

// networkLine is the connected-network label shown under the title: the Wi-Fi
// SSID when wireless, else the wired interface name. Empty until the interface
// probe has passed.
func (m model) networkLine() string {
	r, ok := m.results[diagnostic.ProbeIface]
	if !ok || r.Status != diagnostic.StatusPass {
		return ""
	}
	if r.Network != "" {
		return "Wi-Fi: " + r.Network
	}
	if r.Iface != "" {
		return "Wired: " + r.Iface
	}
	return ""
}

func (m model) View() string {
	if m.helping {
		return m.helpOverlay()
	}
	if m.viewing {
		return m.outputView()
	}
	deferred := m.toolbox && !m.chainRan()

	header := m.headerView()
	body := m.bodyView(deferred)
	if m.networkMap {
		body = m.networkMapView()
	}
	help := m.helpView(deferred)
	if m.entering {
		help = m.promptView(true)
	}
	if m.confirmTool != nil {
		help = m.confirmView()
	}
	toolbox := m.toolboxView()
	top := header + "\n" + m.banner() + "\n\n"
	// Adaptive tail: the job pane gets whatever rows the rest doesn't use.
	// avail is a budget in newlines: jobView's output must add at most avail
	// of them, or the view exceeds the terminal and the renderer cuts the top.
	fixed := top + body + "\n" + toolbox + "\n"
	tail := help + "\n"
	avail := m.height - strings.Count(fixed, "\n") - strings.Count(tail, "\n") - 1
	if m.entering && m.confirmTool == nil && m.height > 0 {
		// The forms cheatsheet yields first: drop it when the view would
		// overflow, or when it would starve a live job pane below jobView's
		// 5-row minimum. m.height == 0 means size unknown — keep the forms.
		if avail < 0 || (m.hasJob() && avail < 5) {
			tail = m.promptView(false) + "\n"
			avail = m.height - strings.Count(fixed, "\n") - strings.Count(tail, "\n") - 1
		}
	}
	if m.height > 0 && avail < 0 {
		body = lipgloss.NewStyle().MaxHeight(max(lipgloss.Height(body)+avail, 1)).Render(body)
		fixed = top + body + "\n" + toolbox + "\n"
		avail = m.height - strings.Count(fixed, "\n") - strings.Count(tail, "\n") - 1
	}
	job := m.jobView(avail)
	if m.networkMap && m.cur.name == lanDiscoveryName {
		job = ""
	}
	return fixed + job + tail
}

// targetHP is the target endpoint as host:port; JoinHostPort brackets IPv6
// literals so the rendered endpoint reads back as the same target.
func (m model) targetHP() string {
	return net.JoinHostPort(m.target.Host, strconv.Itoa(m.target.Port))
}

// headerView is the one-line masthead: app name, target, connected network.
func (m model) headerView() string {
	h := selStyle.Render("◆ ") + titleStyle.Render("Network Doctor")
	if m.target != nil {
		h += faintStyle.Render("  " + m.targetHP())
	}
	if n := m.networkLine(); n != "" {
		h += faintStyle.Render("  ·  " + n)
	}
	return h
}

// bodyView renders the Checks and Details panels side by side, stacking them
// vertically when the terminal is too narrow for two columns.
func (m model) bodyView(deferred bool) string {
	var left strings.Builder
	left.WriteString(panelTitleStyle.Render("Checks") + "\n")
	for i, probe := range m.probes {
		if deferred {
			left.WriteString(faintStyle.Render("  · "+probe.Name) + "\n")
			continue
		}
		marker, name := "  ", probe.Name
		if i == m.selected {
			marker, name = selStyle.Render("› "), selStyle.Render(name)
		}
		left.WriteString(marker + m.glyph(probe.ID) + " " + name + "\n")
	}

	var right strings.Builder
	if deferred {
		right.WriteString(panelTitleStyle.Render("Details") + "\n")
		right.WriteString(faintStyle.Render("Nothing to show yet — the checks haven't run.") + "\n")
	} else {
		probe := m.probes[m.selected]
		right.WriteString(panelTitleStyle.Render("Details — "+probe.Name) + "\n")
		if r, ok := m.results[probe.ID]; ok {
			right.WriteString(statusStyles[r.Status].Render(r.Status.String()) + " — " + r.Detail + "\n")
			if (r.Status == diagnostic.StatusFail || r.Status == diagnostic.StatusWarn) && r.Fix != "" {
				right.WriteString(skipStyle.Render("Fix: ") + r.Fix + "\n")
			}
			if r.Source != nil {
				right.WriteString(faintStyle.Render("src "+r.Source.String()+" "+r.Iface) + "\n")
			}
			for _, a := range r.Attempts {
				st := "ok"
				if a.Err != nil {
					st = a.Err.Error()
				}
				right.WriteString(faintStyle.Render(fmt.Sprintf("  %s %dms %s", a.IP, a.Dur.Milliseconds(), st)) + "\n")
			}
		} else {
			right.WriteString(m.spinner.View() + faintStyle.Render(" checking…") + "\n")
		}
	}

	leftStr := strings.TrimRight(left.String(), "\n")
	rightStr := strings.TrimRight(right.String(), "\n")

	if m.width < 80 { // too narrow for two columns — stack
		w := max(m.width-2, 24)
		return lipgloss.JoinVertical(lipgloss.Left,
			panelStyle.Width(w).Render(leftStr),
			panelStyle.Width(w).Render(rightStr))
	}
	leftW := 38
	rightW := max(m.width-leftW-5, 36)
	h := max(lipgloss.Height(leftStr), lipgloss.Height(rightStr))
	return lipgloss.JoinHorizontal(lipgloss.Top,
		panelStyle.Width(leftW).Height(h).Render(leftStr),
		" ",
		panelStyle.Width(rightW).Height(h).Render(rightStr))
}

// upHostLine pulls the host field out of an nmap -oG "Host: … Status: Up" line.
func upHostLine(line string) (string, bool) {
	host, ok := strings.CutPrefix(line, "Host: ")
	if !ok {
		return "", false
	}
	host, status, ok := strings.Cut(host, "Status: ")
	if !ok || strings.TrimSpace(status) != "Up" {
		return "", false
	}
	return strings.TrimSpace(host), true
}

func (m model) networkHosts() []string {
	source, _ := m.discoveryNetwork()
	var hosts []string
	for _, line := range m.cur.lines {
		host, ok := upHostLine(line)
		if !ok || source != nil && strings.HasPrefix(host, source.String()+" ") {
			continue
		}
		host = strings.TrimSuffix(host, " ()")
		// An OS-resolved name beats whatever nmap's raw PTR race printed.
		if ip, _, _ := strings.Cut(host, " "); m.hostNames[ip] != "" {
			host = ip + " (" + m.hostNames[ip] + ")"
		}
		hosts = append(hosts, host)
	}
	return hosts
}

// discoveredIPs pulls the addresses of Up hosts out of nmap -oG output lines.
func discoveredIPs(lines []string) []string {
	var ips []string
	for _, line := range lines {
		host, ok := upHostLine(line)
		if !ok {
			continue
		}
		ip, _, _ := strings.Cut(host, " ")
		if net.ParseIP(ip) == nil {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}

// networkMapView renders hosts found by the LAN scan.
func (m model) networkMapView() string {
	source, _ := m.discoveryNetwork()
	hosts := m.networkHosts()
	// Only names that carry a domain vote here: a bare ssh alias like
	// "pihole" shouldn't veto stripping ".attlocal.net" off its neighbors.
	domains := map[string]int{}
	domainedHosts := 0
	for _, host := range hosts {
		if _, name, ok := strings.Cut(host, " ("); ok {
			if _, domain, ok := strings.Cut(strings.TrimSuffix(name, ")"), "."); ok {
				domainedHosts++
				domains[strings.ToLower(domain)]++
			}
		}
	}
	commonDomain := ""
	for domain, count := range domains {
		if count == domainedHosts {
			commonDomain = domain
		}
	}

	panelWidth := max(m.width-2, 24)
	title := panelTitleStyle.Render("Network map — " + lanDiscoveryName + " — " + m.networkCIDR)
	if commonDomain != "" {
		domain := faintStyle.Render("Domain: " + commonDomain)
		contentWidth := panelWidth - panelStyle.GetHorizontalPadding()
		if gap := contentWidth - lipgloss.Width(title) - lipgloss.Width(domain); gap > 0 {
			title += strings.Repeat(" ", gap) + domain
		} else {
			title += "\n" + lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Right).Render(domain)
		}
	}
	var b strings.Builder
	b.WriteString(title + "\n")
	b.WriteString(selStyle.Render("◆") + " This device")
	if source != nil {
		b.WriteString(" " + source.String())
	}
	b.WriteString("\n")

	for i, host := range hosts {
		if address, name, ok := strings.Cut(host, " ("); ok {
			name = strings.TrimSuffix(name, ")")
			if short, domain, ok := strings.Cut(name, "."); ok && strings.EqualFold(domain, commonDomain) {
				host = address + " (" + short + ")"
			}
		}
		branch := "├─ "
		if i == len(hosts)-1 {
			branch = "└─ "
		}
		marker := "  "
		if i == m.mapSelected {
			marker = selStyle.Render("› ")
			host = selStyle.Render(host)
		}
		b.WriteString(marker + faintStyle.Render(branch) + passStyle.Render("●") + " " + host + "\n")
	}
	if len(hosts) == 0 {
		switch {
		case m.cur.active != nil:
			b.WriteString(m.spinner.View() + faintStyle.Render(" discovering devices…") + "\n")
		case m.cur.status != JobDone:
			b.WriteString(failStyle.Render("└─ Discovery "+m.cur.status.String()) + "\n")
		default:
			b.WriteString(faintStyle.Render("└─ No other devices replied") + "\n")
		}
	}

	return panelStyle.Width(panelWidth).Render(strings.TrimRight(b.String(), "\n"))
}

func (m model) discoveryNetwork() (net.IP, string) {
	for _, id := range []diagnostic.ProbeID{diagnostic.ProbeInternet, diagnostic.ProbeProxy, diagnostic.ProbeTargetTCP} {
		ip := m.results[id].Source
		if v4 := ip.To4(); v4 != nil && ip.IsPrivate() {
			// ponytail: cap discovery at the source /24; widen only if larger LANs matter.
			return ip, net.IP(v4.Mask(net.CIDRMask(24, 32))).String() + "/24"
		}
	}
	return nil, ""
}

// joinChips joins styled chips with sep, wrapping to width only at chip
// boundaries so a "[k] label" pair is never split mid-word.
func joinChips(width int, sep string, chips []string) string {
	var lines []string
	cur := ""
	for _, c := range chips {
		switch {
		case cur == "":
			cur = c
		case lipgloss.Width(cur)+lipgloss.Width(sep)+lipgloss.Width(c) <= width:
			cur += sep + c
		default:
			lines = append(lines, cur)
			cur = c
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// helpKeys renders key/description pairs as a dim help bar with the keys
// highlighted, e.g. "r restart  ·  q quit", wrapped at pair boundaries.
func helpKeys(width int, kv ...string) string {
	parts := make([]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, keyStyle.Render(kv[i])+" "+faintStyle.Render(kv[i+1]))
	}
	return joinChips(width, faintStyle.Render("  ·  "), parts)
}

// confirmView replaces the help bar with the pending advanced tool's exact
// command and a run/cancel gate, so the scan is always shown before it runs.
func (m model) confirmView() string {
	_, _, display := m.confirmTool.Build(m.target)
	body := panelTitleStyle.Render("Run "+m.confirmTool.Name+"?") + "\n" +
		faintStyle.Render("Actively probes the shown scope — may trip intrusion detection.") + "\n" +
		"$ " + display
	w := max(min(m.width-2, 76), 24)
	return focusPanelStyle.Width(w).Render(body) + "\n" + helpKeys(m.width, "y", "run", "esc", "cancel")
}

// promptView is the restart prompt panel, shown in place of the help bar.
// withForms includes the target-grammar cheatsheet; View drops it when the
// terminal is too short (the input and any job pane always outrank it).
func (m model) promptView(withForms bool) string {
	body := panelTitleStyle.Render("Restart") + "\n" + m.input.View()
	if withForms {
		// Dedent the shared const: the two-space indent reads right under
		// "Target forms:" in --help but floats oddly inside the panel.
		forms := strings.TrimPrefix(strings.ReplaceAll(diagnostic.TargetForms, "\n  ", "\n"), "  ")
		body += "\n\n" + faintStyle.Render(forms)
	}
	if m.inputErr != "" {
		body += "\n" + failStyle.Render("✗ "+m.inputErr)
	}
	// 88, not 76: the longest target-form line needs ~86 content cols to
	// render unwrapped on wide terminals.
	w := max(min(m.width-2, 88), 24)
	footer := helpKeys(m.width, "enter", "run", "esc", "back")
	if m.notice == ctrlCNotice {
		footer = m.noticeView()
	}
	return focusPanelStyle.Width(w).Render(body) + "\n" + footer
}

func (m model) helpView(deferred bool) string {
	// Enter opens the output viewer whenever a job pane exists (same condition
	// as jobView), so the hint tracks exactly when the key does something.
	hasJob := m.hasJob()
	if deferred {
		if m.networkMap {
			kv := []string{"v", "checks"}
			if len(m.networkHosts()) > 0 {
				kv = append([]string{"↑/↓", "select device", "enter", "set target"}, kv...)
			} else if hasJob {
				kv = append(kv, "enter", "full output")
			}
			if len(m.otherJobs) > 0 {
				kv = append(kv, "tab", "switch job")
			}
			return helpKeys(m.width, append(kv, "r", "run the checks", "?", "help", "q", "quit")...)
		}
		kv := []string{"r", "run the checks", "v", "network map"}
		if len(m.tools) > 0 {
			kv = append(kv, "letter", "runs that tool")
		}
		if hasJob {
			kv = append(kv, "enter", "full output")
		}
		if len(m.otherJobs) > 0 {
			kv = append(kv, "tab", "switch job")
		}
		return helpKeys(m.width, append(kv, "?", "help", "q", "quit")...)
	}
	kv := []string{"↑/↓", "scroll", "v", "network map"}
	if m.networkMap {
		kv = []string{"v", "checks"}
		if len(m.networkHosts()) > 0 {
			kv = append([]string{"↑/↓", "select device", "enter", "set target"}, kv...)
		}
	}
	if hasJob && (!m.networkMap || len(m.networkHosts()) == 0) {
		kv = append(kv, "enter", "full output")
	}
	if m.cur.active != nil {
		kv = append(kv, "esc", "cancel job")
	}
	if len(m.otherJobs) > 0 {
		kv = append(kv, "tab", "switch job")
	}
	if m.reportReady() {
		kv = append(kv, "y", "copy report", "w", "save report")
	}
	kv = append(kv, "r", "restart", "?", "help", "q", "quit")
	help := helpKeys(m.width, kv...)
	if notice := m.noticeView(); notice != "" {
		help = notice + "\n" + help
	}
	return help
}

// helpOverlay is the full-screen key cheatsheet (?). It lists every binding
// unconditionally — simpler than mirroring the help bar's context rules, and
// a key that currently does nothing is still worth knowing about.
func (m model) helpOverlay() string {
	row := func(k, desc string) string {
		// fmt widths count runes, so ↑/↓ pads the same as ASCII keys.
		return "  " + keyStyle.Render(fmt.Sprintf("%-10s", k)) + faintStyle.Render(desc) + "\n"
	}
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Keys") + "\n")
	b.WriteString(row("↑/↓ j/k", "select check — or device on the network map"))
	b.WriteString(row("enter", "full output — or set target on the network map"))
	b.WriteString(row("tab", "switch job"))
	b.WriteString(row("esc", "cancel the focused job"))
	b.WriteString(row("v", "toggle network map"))
	b.WriteString(row("r", "restart with a new target"))
	b.WriteString(row("y", "copy report"))
	b.WriteString(row("w", "save report"))
	for _, tool := range m.tools {
		b.WriteString(row(tool.Key, "run "+tool.Name))
	}
	b.WriteString(row("q", "quit"))
	b.WriteString("\n" + panelTitleStyle.Render("Output viewer") + "\n")
	b.WriteString(row("↑/↓", "scroll"))
	b.WriteString(row("pgup/pgdn", "page"))
	b.WriteString(row("home/end", "jump to top / bottom"))
	b.WriteString(row("/", "filter lines"))
	b.WriteString(row("y", "copy output (filtered if a filter is on)"))
	b.WriteString(row("esc/q", "back"))
	out := b.String() + "\n" + helpKeys(m.width, "any key", "close")
	if m.height > 0 {
		out = lipgloss.NewStyle().MaxHeight(m.height).Render(out)
	}
	return out
}

func (m model) noticeView() string {
	if m.notice == "" {
		return ""
	}
	if m.notice == ctrlCNotice {
		return warnStyle.Render("! " + m.notice)
	}
	if m.noticeOK {
		return passStyle.Render("✓ " + m.notice)
	}
	return failStyle.Render("✗ " + m.notice)
}

// banner is the full-width guidance block under the header: what is happening,
// what it means in plain English, and — on a failure — what to do about it and
// which tool to reach for next.
func (m model) banner() string {
	if m.toolbox && !m.chainRan() {
		return "Welcome! Press " + selStyle.Render("r") + " to check your connection, or run a tool below."
	}
	if !m.allDone() {
		done, total := len(m.results), len(m.probes)
		return m.spinner.View() + " Checking your connection… " +
			progressBar(done, total, 20) + faintStyle.Render(fmt.Sprintf(" %d of %d done", done, total))
	}
	order, firstFail, anyWarn := m.resultState()
	summary := diagnostic.Diagnose(m.target, order, m.results)
	if firstFail == nil {
		if anyWarn {
			return warnStyle.Render("! " + summary)
		}
		return passStyle.Render("✓ " + summary)
	}
	lines := []string{failStyle.Render("✗ " + summary)}
	if firstFail.Fix != "" {
		lines = append(lines, faintStyle.Render("  Fix: "+firstFail.Fix))
	}
	if next := m.nextStep(firstFail.ID); next != "" {
		lines = append(lines, "  "+next)
	}
	return strings.Join(lines, "\n")
}

// resultState collects the ordered probe IDs and the severity flags shared by
// the styled banner and plain-text report verdict.
func (m model) resultState() (order []diagnostic.ProbeID, firstFail *diagnostic.ProbeResult, anyWarn bool) {
	order = make([]diagnostic.ProbeID, len(m.probes))
	for i, probe := range m.probes {
		order[i] = probe.ID
		r := m.results[probe.ID]
		if firstFail == nil && r.Status == diagnostic.StatusFail {
			rr := r
			firstFail = &rr
		}
		anyWarn = anyWarn || r.Status == diagnostic.StatusWarn
	}
	return order, firstFail, anyWarn
}

// probeNextTool maps a failed probe to the toolbox hotkey that best
// investigates it.
var probeNextTool = map[diagnostic.ProbeID]string{
	diagnostic.ProbeInternet:  "p",
	diagnostic.ProbeDNS:       "d",
	diagnostic.ProbeTargetTCP: "t",
	diagnostic.ProbeTLS:       "c",
	diagnostic.ProbeHTTP:      "c",
	diagnostic.ProbeHTTPS:     "c",
	diagnostic.ProbeSSH:       "t",
	diagnostic.ProbeSMTP:      "t",
}

// nextStep suggests the toolbox key worth pressing after a failure, e.g.
// "Next: press d — DNS lookup (dig)". Empty when no tool applies or the
// binary is missing.
func (m model) nextStep(id diagnostic.ProbeID) string {
	key, ok := probeNextTool[id]
	if !ok {
		return ""
	}
	for _, t := range m.tools {
		if t.Key == key && t.Available() {
			return "Next: press " + selStyle.Render(key) + " — " + t.Purpose + " (" + t.Name + ")"
		}
	}
	return ""
}

// jobStatusLine is the "name — status" line shared by the job pane and the
// output viewer: a live spinner + timer while running, the total duration
// once the job has finished.
func (m model) jobStatusLine() string {
	s := faintStyle.Render(m.cur.name+" — ") + statusStyles[m.cur.status].Render(m.cur.status.String())
	if len(m.otherJobs) > 0 {
		s += faintStyle.Render(fmt.Sprintf(" · %d jobs · tab to switch", len(m.otherJobs)+1))
	}
	if m.cur.active != nil {
		return s + " " + m.spinner.View() + faintStyle.Render(fmt.Sprintf(" %.0fs", time.Since(m.cur.start).Seconds()))
	}
	if m.cur.dur > 0 && m.cur.dur < time.Second {
		s += faintStyle.Render(fmt.Sprintf(" · %dms", m.cur.dur.Milliseconds()))
	} else if m.cur.dur >= time.Second {
		s += faintStyle.Render(fmt.Sprintf(" · %.0fs", m.cur.dur.Seconds()))
	}
	return s
}

// outputView is the full-screen scrollable output viewer (Enter).
func (m model) outputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("$ "+m.cur.display) + "\n")
	b.WriteString(m.jobStatusLine() + "\n")
	b.WriteString(m.vp.View() + "\n")
	b.WriteString(faintStyle.Render(m.vpContext()) + "\n")
	b.WriteString(m.viewerFooter())
	return b.String()
}

func (m model) viewerFooter() string {
	if m.filtering {
		return m.filterInput.View() + "\n" + helpKeys(m.width, "enter", "apply", "esc", "clear")
	}
	if notice := m.noticeView(); notice != "" {
		return notice
	}
	kv := []string{"↑/↓", "scroll", "pgup/pgdn", "page", "home/end", "top/bottom", "/", "filter"}
	if len(m.otherJobs) > 0 {
		kv = append(kv, "tab", "switch job")
	}
	if m.filter != "" {
		return helpKeys(m.width, append(kv, "y", "copy output", "esc", "clear filter", "q", "back")...)
	}
	return helpKeys(m.width, append(kv, "y", "copy output", "esc/q", "back")...)
}

// vpContext is the viewport position line, in wrapped display-line numbers:
// "lines 420–450 of 500 · 37 older lines discarded · following".
func (m model) vpContext() string {
	total := m.vp.TotalLineCount()
	top := m.vp.YOffset + 1
	bot := m.vp.YOffset + m.vp.Height
	if bot > total {
		bot = total
	}
	if top > bot {
		top = bot
	}
	s := fmt.Sprintf("lines %d–%d of %d", top, bot, total)
	if m.filter != "" {
		s += " · filter: " + m.filter
	}
	if m.cur.evicted > 0 {
		s += fmt.Sprintf(" · %d older lines discarded", m.cur.evicted)
	}
	if m.cur.dropped > 0 {
		s += fmt.Sprintf(" · %d dropped (channel overflow)", m.cur.dropped)
	}
	if m.cur.active != nil {
		if m.follow {
			s += " · following"
		} else {
			s += " · follow paused — scroll to bottom to resume"
		}
	}
	return s
}

// progressBar is a w-cell block bar, filled proportionally to done/total.
func progressBar(done, total, w int) string {
	if total <= 0 || w <= 0 {
		return ""
	}
	filled := min(done*w/total, w)
	return selStyle.Render(strings.Repeat("█", filled)) + faintStyle.Render(strings.Repeat("░", w-filled))
}

func (m model) toolboxView() string {
	if len(m.tools) == 0 {
		return faintStyle.Render("Tools need a host — press ") + keyStyle.Render("r") + faintStyle.Render(" to set one") + "\n"
	}
	parts := make([]string, len(m.tools))
	for i, t := range m.tools {
		if t.Available() {
			parts[i] = keyStyle.Render("["+t.Key+"]") + " " + t.Purpose
		} else {
			parts[i] = faintStyle.Render("[" + t.Key + "] " + t.Purpose + " — " + t.Bin + " missing")
		}
	}
	// The title rides on the first chip so line 1's width math includes it;
	// wrapping happens only between chips, never inside one.
	parts[0] = titleStyle.Render("Dig deeper") + "  " + parts[0]
	return joinChips(m.width, faintStyle.Render("  ·  "), parts) + "\n"
}

// jobView renders the job pane with an adaptive tail: avail is the screen
// height left over for this pane; unknown height falls back to jobTailLines.
func (m model) jobView(avail int) string {
	if !m.hasJob() {
		return ""
	}
	if m.height > 0 && avail < 5 {
		return "" // not even rule+title+status+note fit — drop the pane
	}
	tailN := jobTailLines
	if m.height > 0 {
		tailN = avail - 5 // rule, title, status, context note, trailing blank
		if tailN < 0 {
			tailN = 0
		}
	}
	var b strings.Builder
	b.WriteString(faintStyle.Render(strings.Repeat("─", m.width)) + "\n")
	b.WriteString(titleStyle.Render("$ "+m.cur.display) + "\n")
	b.WriteString(m.jobStatusLine() + "\n")

	shown := m.cur.lines
	if len(shown) > tailN {
		shown = shown[len(shown)-tailN:]
	}
	for _, ln := range shown {
		b.WriteString(ln + "\n")
	}
	older := len(m.cur.lines) - len(shown) + m.cur.evicted
	if older > 0 || m.cur.dropped > 0 {
		var notes []string
		if older > 0 {
			notes = append(notes, fmt.Sprintf("… %d earlier lines — enter to scroll", older))
		}
		if m.cur.dropped > 0 {
			notes = append(notes, fmt.Sprintf("%d dropped (channel overflow)", m.cur.dropped))
		}
		b.WriteString(faintStyle.Render("("+strings.Join(notes, " · ")+")") + "\n")
	}
	return b.String() + "\n"
}
