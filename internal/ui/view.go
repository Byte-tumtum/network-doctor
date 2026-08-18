// Rendering: the View tree and its helpers. Nothing here mutates the
// model; every function takes a value receiver and returns a string.

package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// wrap reflows a full-width block that no panel is wrapping for us. Without
// it the terminal hard-wraps mid-word, and the extra display rows aren't in
// View's newline budget, so the renderer eats the top of the screen.
func (m model) wrap(s string) string {
	if m.width <= 0 {
		return s
	}
	return ansi.Wrap(s, m.width, "")
}

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
// and display-only SSID probes have completed.
func (m model) networkLine() string {
	r, ok := m.results[diagnostic.ProbeIface]
	if !ok || r.Status != diagnostic.StatusPass {
		return ""
	}
	network, ok := m.results[diagnostic.ProbeSSID]
	if !ok {
		return ""
	}
	if network.Network != "" {
		return "Wi-Fi: " + network.Network
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

	body := m.bodyView(deferred)
	if m.networkMap {
		body = m.networkMapView()
	}
	help := m.helpView(deferred)
	if m.entering {
		help = m.promptView(true)
	}
	if m.sshPrompt {
		help = m.sshFormView()
	}
	if m.confirmTool != nil {
		help = m.confirmView()
	}
	toolbox := m.toolboxView(false)
	banner := m.wrap(m.banner()) + "\n"
	header := ""
	if h := m.wrap(m.headerView()); h != "" {
		header = h + "\n"
	}
	tail := help + "\n"
	// Adaptive tail: the job pane gets whatever rows the rest doesn't use.
	// avail is a budget in newlines: jobView's output must add at most avail
	// of them, or the view exceeds the terminal and the renderer cuts the top.
	var fixed string
	var avail int
	budget := func() {
		fixed = banner + header + "\n" + body + "\n" + toolbox + "\n"
		avail = m.height - strings.Count(fixed, "\n") - strings.Count(tail, "\n") - 1
	}
	budget()
	if m.entering && m.confirmTool == nil && m.height > 0 {
		// The forms cheatsheet yields first: drop it when the view would
		// overflow, or when it would starve a live job pane below jobView's
		// 5-row minimum. m.height == 0 means size unknown, so keep the forms.
		if avail < 0 || (m.hasJob() && avail < 5) {
			tail = m.promptView(false) + "\n"
			budget()
		}
	}
	minAvail := 0
	if m.entering && m.hasJob() {
		minAvail = 5
	}
	// Still overflowing: shed chrome before content. The toolbox chips go
	// first (they wrap to a row per couple of tools on a narrow terminal),
	// then the header line, then the panels are clipped to what is left. The
	// banner is the plain-English verdict, so it never yields.
	if m.height > 0 && avail < minAvail {
		toolbox = m.toolboxView(true)
		budget()
	}
	if m.height > 0 && avail < minAvail && header != "" {
		header = ""
		budget()
	}
	if m.height > 0 && avail < minAvail {
		body = lipgloss.NewStyle().MaxHeight(max(lipgloss.Height(body)+avail-minAvail, 1)).Render(body)
		budget()
	}
	job := m.jobView(avail)
	if m.networkMap && m.cur.name == lanDiscoveryName {
		job = ""
	}
	out := fixed + job + tail
	if m.height > 0 {
		// Last resort: the panels bottom out at one row and the help bar has no
		// floor at all, so on a very short terminal the view can still overflow.
		// Clip from the bottom: losing the help bar beats the renderer eating
		// the top of the screen, which is where the banner lives.
		out = lipgloss.NewStyle().MaxHeight(m.height).Render(out)
	}
	return out
}

// targetHP is the target endpoint as host:port; JoinHostPort brackets IPv6
// literals so the rendered endpoint reads back as the same target.
func (m model) targetHP() string {
	return net.JoinHostPort(m.target.Host, strconv.Itoa(m.target.Port))
}

// headerView is the one-line context strip under the banner: target, connected
// network, watch mode. Empty when there is nothing to say, and the caller drops
// the line rather than rendering a blank one.
func (m model) headerView() string {
	var parts []string
	if m.target != nil {
		parts = append(parts, m.targetHP())
	}
	if n := m.networkLine(); n != "" {
		parts = append(parts, n)
	}
	if m.watch {
		parts = append(parts, "watch")
	}
	if len(parts) == 0 {
		return ""
	}
	return faintStyle.Render(strings.Join(parts, "  ·  "))
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
		left.WriteString(marker + m.glyph(probe.ID) + " " + name)
		if m.watch && len(m.runHistory[probe.ID]) > 0 {
			left.WriteString("  " + m.statusSparkline(probe.ID, 8))
		}
		left.WriteString("\n")
	}

	var right strings.Builder
	if deferred {
		right.WriteString(panelTitleStyle.Render("Details") + "\n")
		right.WriteString(faintStyle.Render("Nothing to show yet: the checks haven't run.") + "\n")
	} else {
		probe := m.probes[m.selected]
		right.WriteString(panelTitleStyle.Render("Details: "+probe.Name) + "\n")
		if r, ok := m.results[probe.ID]; ok {
			right.WriteString(statusStyles[r.Status].Render(r.Status.String()) + ": " + r.Detail + "\n")
			if (r.Status == diagnostic.StatusFail || r.Status == diagnostic.StatusWarn) && r.Fix != "" {
				right.WriteString(skipStyle.Render("Fix: ") + r.Fix + "\n")
			}
			if r.Portal != nil && r.Portal.RedirectURL != "" {
				right.WriteString(faintStyle.Render("portal "+r.Portal.RedirectURL) + "\n")
			}
			if r.Source != nil {
				right.WriteString(faintStyle.Render("src "+r.Source.String()+" "+r.Iface) + "\n")
			}
			for _, a := range r.Attempts {
				st := "ok"
				if a.Err != nil {
					st = a.Err.Error()
				}
				right.WriteString(faintStyle.Render(fmt.Sprintf("  %s %dms %s", a.IP, diagnostic.Ms(a.Dur), st)) + "\n")
			}
		} else {
			right.WriteString(m.spinner.View() + faintStyle.Render(" checking…") + "\n")
		}
		if history := m.historyLine(probe.ID); history != "" {
			right.WriteString(history + "\n")
		}
	}

	leftStr := strings.TrimRight(left.String(), "\n")
	rightStr := strings.TrimRight(right.String(), "\n")

	if m.width < 80 { // too narrow for two columns, so stack
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

func (m model) historyLine(id diagnostic.ProbeID) string {
	history := m.runHistory[id]
	if len(history) == 0 {
		return ""
	}
	failed := 0
	for _, status := range history {
		if status == diagnostic.StatusFail {
			failed++
		}
	}
	return faintStyle.Render("History: ") + m.statusSparkline(id, 0) +
		faintStyle.Render(fmt.Sprintf("  ·  failed %d of %d runs", failed, len(history)))
}

func (m model) statusSparkline(id diagnostic.ProbeID, limit int) string {
	history := m.runHistory[id]
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	var spark strings.Builder
	for _, status := range history {
		spark.WriteString(statusStyles[status].Render(probeGlyph(status)))
	}
	return spark.String()
}

func (m model) selectedPortalURL() string {
	if m.selected < 0 || m.selected >= len(m.probes) {
		return ""
	}
	r := m.results[m.probes[m.selected].ID]
	if r.Portal == nil {
		return ""
	}
	return r.Portal.RedirectURL
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

// namePending reports whether address is still waiting on a name we'd rather
// show than nmap's: during the scan itself, or until its own lookup returns.
func (m model) namePending(address string) bool {
	return m.hostNames[address] == "" && (m.namesPending[address] || m.cur.active != nil)
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
		if address, name, ok := strings.Cut(host, " ("); ok && !m.namePending(address) {
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
	title := panelTitleStyle.Render("Network map: " + lanDiscoveryName + " · " + m.networkCIDR)
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
		address, name, named := strings.Cut(host, " (")
		if named {
			name = strings.TrimSuffix(name, ")")
			if short, domain, ok := strings.Cut(name, "."); ok && strings.EqualFold(domain, commonDomain) {
				host = address + " (" + short + ")"
			}
		}
		// A name we're still working on beats nmap's "unknownaabbcc" placeholder.
		if m.namePending(address) {
			host = address + " " + m.spinner.View()
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
			// The source /24 is the sweep, not the interface's real prefix: a
			// /16 is 65k hosts, far past what the scan's 60s timeout can cover.
			return ip, v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
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
	_, _, display := m.confirmTool.Build(m.target, m.selectedIP())
	body := panelTitleStyle.Render("Run "+m.confirmTool.Name+"?") + "\n" +
		faintStyle.Render("Actively probes the shown scope, and may trip intrusion detection.") + "\n" +
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
	footer := helpKeys(m.width, "↑/↓", "history", "enter", "run", "esc", "back")
	if m.notice == ctrlCNotice {
		footer = m.noticeView()
	}
	return focusPanelStyle.Width(w).Render(body) + "\n" + footer
}

// sshFormView is the SSH login panel (S), shown in place of the help bar. The
// value receiver is doing real work here: the inputs are resized to the
// terminal on the copy, never on the model.
func (m model) sshFormView() string {
	w := max(min(m.width-2, 76), 24)
	// 6 = panel border + padding + the gutter the key row's ▸ marker sits in.
	inputW := func(prompt string) int { return max(w-6-lipgloss.Width(prompt), 8) }
	m.ssh.user.Width = inputW(m.ssh.user.Prompt)
	m.ssh.pass.Width = inputW(m.ssh.pass.Prompt)

	// The key row is a chooser, so it renders itself: the selected key, and
	// the ←/→ hint only while the row has the focus.
	key := keyStyle.Render("Key       ") + m.ssh.keyLabel()
	if len(m.ssh.keys) > 1 {
		key += faintStyle.Render(fmt.Sprintf("  (%d of %d)", m.ssh.keyIdx+1, len(m.ssh.keys)))
		if m.ssh.focus == sshKey {
			key += faintStyle.Render("  ←/→")
		}
	}
	if m.ssh.focus == sshKey {
		key = selStyle.Render("▸ ") + key
	} else {
		key = "  " + key
	}

	body := panelTitleStyle.Render("SSH login to "+m.ssh.host) + "\n" +
		"  " + m.ssh.user.View() + "\n" +
		key + "\n" +
		"  " + m.ssh.pass.View() +
		"\n\n" + faintStyle.Render("Leave a field blank and ssh asks you itself, as it does for a\nkey passphrase, a host-key check, or a 2FA code.")
	if m.ssh.err != "" {
		body += "\n" + failStyle.Render("✗ "+m.ssh.err)
	}
	if m.ssh.pending != nil {
		body += "\n" + m.spinner.View() + " checking ssh config…"
		return focusPanelStyle.Width(w).Render(body) + "\n" + helpKeys(m.width, "esc", "back")
	}
	return focusPanelStyle.Width(w).Render(body) + "\n" +
		helpKeys(m.width, "tab", "next field", "enter", "connect", "esc", "back")
}

func (m model) helpView(deferred bool) string {
	// Only the map branch cares how many devices there are, and networkHosts
	// walks the whole job line buffer, so don't pay for it on every checks frame.
	hosts := 0
	if m.networkMap {
		hosts = len(m.networkHosts())
	}
	// Every chip is looked up in the selected preset.
	var kv []string
	add := func(label, desc string) {
		if label != "" {
			kv = append(kv, label, desc)
		}
	}
	addAction := func(ctx keyContext, act keyAction) {
		help, ok := actionHelpFor(ctx, act)
		if ok {
			add(m.keys.label(ctx, act), help.bar)
		}
	}
	addPair := func(ctx keyContext, a, b keyAction) {
		help, ok := actionHelpFor(ctx, a)
		if ok {
			add(m.keys.pairLabel(ctx, a, b), help.bar)
		}
	}
	switch {
	case m.networkMap:
		if hosts > 0 {
			add(m.keys.pairLabel(ctxList, actUp, actDown), "select device")
			addPair(ctxList, actTop, actBottom)
			add(m.keys.label(ctxList, actOpen), "set target")
		}
		add(m.keys.label(ctxList, actNetworkMap), "checks")
	case deferred:
		add(m.keys.label(ctxList, actRestart), "run the checks")
		addAction(ctxList, actNetworkMap)
		if len(m.tools) > 0 {
			kv = append(kv, "letter", "runs that tool")
		}
	default:
		add(m.keys.pairLabel(ctxList, actUp, actDown), "scroll")
		addPair(ctxList, actTop, actBottom)
		addAction(ctxList, actNetworkMap)
	}
	// Open works whenever a job pane exists (same condition as jobView), so the
	// hint tracks exactly when the key does something. On the map with devices
	// listed, it sets the target instead.
	if m.hasJob() && (!m.networkMap || hosts == 0) {
		add(m.keys.label(ctxList, actOpen), "full output")
	}
	if !deferred && m.cur.active != nil {
		addAction(ctxList, actCancelJob)
	}
	if len(m.otherJobs) > 0 {
		addAction(ctxList, actSwitchJob)
	}
	// Applied to every exit, including the deferred one: a notice raised before
	// the chain has run has nothing else on screen to explain it.
	withNotice := func(help string) string {
		if notice := m.noticeView(); notice != "" {
			return notice + "\n" + help
		}
		return help
	}
	if deferred {
		if m.networkMap {
			add(m.keys.label(ctxList, actRestart), "run the checks")
		}
		addAction(ctxList, actHelp)
		addAction(ctxList, actQuit)
		return withNotice(m.chordHint(helpKeys(m.width, kv...)))
	}
	if m.selectedPortalURL() != "" {
		add(m.keys.label(ctxList, actCopy), "copy portal URL")
	} else if m.reportReady() {
		add(m.keys.label(ctxList, actCopy), "copy report")
	}
	if m.reportReady() {
		addAction(ctxList, actSave)
	}
	if m.sshDetected() {
		addAction(ctxList, actSSH)
	}
	addAction(ctxList, actRestart)
	addAction(ctxList, actHelp)
	addAction(ctxList, actQuit)
	return withNotice(m.chordHint(helpKeys(m.width, kv...)))
}

// chordHint prefixes the help bar with a half-typed chord, as vim's showcmd
// does, so the first key of one does not look like a dropped keypress.
func (m model) chordHint(help string) string {
	if len(m.pendingKeys) == 0 {
		return help
	}
	return keyStyle.Render(displaySeq(strings.Join(m.pendingKeys, " "))+"…") + faintStyle.Render("  ·  ") + help
}

// helpOverlay is generated from the same actions and bindings used by dispatch.
func (m model) helpOverlay() string {
	keyWidth := 8
	for _, def := range actionDefs {
		for ctx := range def.help {
			keyWidth = max(keyWidth, lipgloss.Width(m.keys.label(ctx, def.act)))
		}
	}
	row := func(k, desc string) string {
		return "  " + keyStyle.Render(k) +
			strings.Repeat(" ", max(keyWidth-lipgloss.Width(k), 0)+2) +
			faintStyle.Render(desc) + "\n"
	}
	// Both sections are generated from the same table dispatch indexes.
	section := func(b *strings.Builder, ctx keyContext) {
		for _, def := range actionDefs {
			help, ok := def.help[ctx]
			if !ok || !m.keys.bound(ctx, def.act) {
				continue
			}
			if def.act == actSSH && !m.sshDetected() {
				continue
			}
			b.WriteString(row(m.keys.label(ctx, def.act), help.details))
		}
	}
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Keys") + "\n")
	section(&b, ctxList)
	for _, tool := range m.tools {
		b.WriteString(row(tool.Key, "run "+tool.Name))
	}
	b.WriteString("\n" + panelTitleStyle.Render("Output viewer") + "\n")
	section(&b, ctxViewer)
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
// what it means in plain English, and, on a failure, what to do about it and
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
	summary, _ := m.diagnose(order)
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
// "Next: press d for DNS lookup (dig)". Empty when no tool applies or the
// binary is missing.
func (m model) nextStep(id diagnostic.ProbeID) string {
	key, ok := probeNextTool[id]
	if !ok {
		return ""
	}
	for _, t := range m.tools {
		if t.Key == key && t.Available {
			return "Next: press " + selStyle.Render(key) + " for " + t.Name + " (" + t.Bin + ")"
		}
	}
	return ""
}

// jobStatusLine is the "name: status" line shared by the job pane and the
// output viewer: a live spinner + timer while running, the total duration
// once the job has finished.
func (m model) jobStatusLine() string {
	s := faintStyle.Render(m.cur.name+": ") + statusStyles[m.cur.status].Render(m.cur.status.String())
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

// viewerHeader is the command line and status above the viewport, wrapped:
// nothing else reflows them, and vpHeight has to know how many rows they cost.
func (m model) viewerHeader() string {
	return m.wrap(titleStyle.Render("$ "+m.cur.display)) + "\n" + m.wrap(m.jobStatusLine())
}

// outputView is the full-screen scrollable output viewer (Enter).
func (m model) outputView() string {
	var b strings.Builder
	b.WriteString(m.viewerHeader() + "\n")
	b.WriteString(m.vp.View() + "\n")
	// The context line is budgeted at exactly one row, so trim it rather than
	// wrap it, since a long filter is the usual way it outgrows the terminal.
	ctx := m.vpContext()
	if m.width > 0 {
		ctx = ansi.Truncate(ctx, m.width, "")
	}
	b.WriteString(faintStyle.Render(ctx) + "\n")
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
	var kv []string
	add := func(label, desc string) {
		if label != "" {
			kv = append(kv, label, desc)
		}
	}
	addAction := func(act keyAction) {
		if help, ok := actionHelpFor(ctxViewer, act); ok {
			add(m.keys.label(ctxViewer, act), help.bar)
		}
	}
	addPair := func(a, b keyAction) {
		if help, ok := actionHelpFor(ctxViewer, a); ok {
			add(m.keys.pairLabel(ctxViewer, a, b), help.bar)
		}
	}
	addPair(actUp, actDown)
	addPair(actPageUp, actPageDown)
	addPair(actHalfPageUp, actHalfPageDown)
	addPair(actTop, actBottom)
	addAction(actFilter)
	if len(m.otherJobs) > 0 {
		addAction(actSwitchJob)
	}
	addAction(actCopy)
	addAction(actSave)
	// With a filter applied the two exits do different things, so they stop
	// sharing a chip and say which is which.
	if m.filter != "" {
		addAction(actClearFilter)
		addAction(actBack)
		return m.chordHint(helpKeys(m.width, kv...))
	}
	addPair(actClearFilter, actBack)
	return m.chordHint(helpKeys(m.width, kv...))
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
			s += " · follow paused, scroll to bottom to resume"
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

// toolboxView renders the "Dig deeper" chip row. compact keeps only the keys:
// on a short terminal the names are the first thing View sheds, and the letters
// alone still say which keys are bound, and ? lists what they do.
func (m model) toolboxView(compact bool) string {
	if len(m.tools) == 0 {
		return faintStyle.Render("Tools need a host, press ") + keyStyle.Render("r") + faintStyle.Render(" to set one") + "\n"
	}
	chip := func(available bool, key, rest string) string {
		if compact {
			rest = ""
		}
		if !available {
			return faintStyle.Render("[" + key + "]" + rest)
		}
		return keyStyle.Render("["+key+"]") + rest
	}
	parts := make([]string, len(m.tools))
	for i, t := range m.tools {
		rest := " " + t.Name
		if !t.Available {
			rest = " " + t.Name + " (" + t.Bin + " missing)"
		}
		parts[i] = chip(t.Available, t.Key, rest)
	}
	// SSH login isn't a Tool: it takes over the terminal instead of streaming
	// output into a job pane, so it rides along as a plain chip. It logs in to
	// the target, which the target-independent tools don't need.
	if m.target == nil {
		parts = append(parts, chip(false, "S", " SSH login (needs a target)"))
	} else {
		parts = append(parts, chip(true, "S", " SSH login"))
	}
	sep := faintStyle.Render("  ·  ")
	if compact {
		sep = " "
	}
	// The title rides on the first chip so line 1's width math includes it;
	// wrapping happens only between chips, never inside one.
	parts[0] = titleStyle.Render("Dig deeper") + "  " + parts[0]
	return joinChips(m.width, sep, parts) + "\n"
}

// jobView renders the job pane with an adaptive tail: avail is the screen
// height left over for this pane; unknown height falls back to jobTailLines.
func (m model) jobView(avail int) string {
	if !m.hasJob() {
		return ""
	}
	if m.height > 0 && avail < 5 {
		return "" // not even rule+title+status+note fit, so drop the pane
	}
	title := m.wrap(titleStyle.Render("$ " + m.cur.display))
	status := m.wrap(m.jobStatusLine())
	tailN := jobTailLines
	if m.height > 0 {
		// rule, context note, trailing blank, plus however many rows the
		// title and status took after wrapping.
		tailN = avail - 3 - lipgloss.Height(title) - lipgloss.Height(status)
		if tailN < 0 {
			tailN = 0
		}
	}
	var b strings.Builder
	b.WriteString(faintStyle.Render(strings.Repeat("─", m.width)) + "\n")
	b.WriteString(title + "\n")
	b.WriteString(status + "\n")

	shown := m.cur.lines
	if len(shown) > tailN {
		shown = shown[len(shown)-tailN:]
	}
	for _, ln := range shown {
		// Truncate rather than wrap: tool output routinely runs past the
		// terminal, and every extra display row is a row tailN never budgeted
		// for. The pane already offers enter to scroll for the whole line.
		if m.width > 0 {
			ln = ansi.Truncate(ln, m.width, "")
		}
		b.WriteString(ln + "\n")
	}
	older := len(m.cur.lines) - len(shown) + m.cur.evicted
	if older > 0 || m.cur.dropped > 0 {
		var notes []string
		if older > 0 {
			notes = append(notes, fmt.Sprintf("… %d earlier lines, enter to scroll", older))
		}
		if m.cur.dropped > 0 {
			notes = append(notes, fmt.Sprintf("%d dropped (channel overflow)", m.cur.dropped))
		}
		b.WriteString(faintStyle.Render("("+strings.Join(notes, " · ")+")") + "\n")
	}
	return b.String() + "\n"
}
