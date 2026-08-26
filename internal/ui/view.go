// Rendering: the View tree and its helpers. Nothing here mutates the
// model; every function takes a value receiver and returns a string.

package ui

import (
	"fmt"
	"net"
	"runtime"
	"slices"
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

	// shrink re-renders the results block inside a row budget. The network map
	// has no cursor-following list to scroll, so it is clipped instead.
	shrink := func(rows int) string { return m.bodyView(deferred, rows) }
	if m.networkMap {
		shrink = func(rows int) string {
			if rows <= 0 {
				return m.networkMapView()
			}
			return lipgloss.NewStyle().MaxHeight(rows).Render(m.networkMapView())
		}
	}
	body := shrink(0)
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
		fixed = banner + header
		if body != "" {
			fixed += "\n" + body + "\n"
		}
		if toolbox != "" {
			fixed += toolbox + "\n"
		}
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
	// Still overflowing: shed in order of what the reader can do without. The
	// toolbox chips lose their names first (they wrap to a row per couple of
	// tools on a narrow terminal), then the results block scrolls down toward
	// a single probe row, then the chips and finally the block go entirely.
	// The banner carries the answer with its Fix, Next and Evidence lines, the
	// header carries the target that answer is about, and the help bar is the
	// way to anywhere else, so those three never yield to the results block.
	if m.height > 0 && avail < minAvail {
		toolbox = m.toolboxView(true)
		budget()
	}
	if m.height > 0 && avail < minAvail {
		body = shrink(max(lipgloss.Height(body)+avail-minAvail, bodyMinRows))
		budget()
	}
	if m.height > 0 && avail < minAvail && toolbox != "" {
		toolbox = ""
		budget()
	}
	if m.height > 0 && avail < minAvail && body != "" {
		body = ""
		budget()
	}
	job := m.jobView(avail)
	if m.networkMap && m.cur.name == lanDiscoveryName {
		job = ""
	}
	out := fixed + job + tail
	if m.height > 0 {
		// Last resort: a terminal too short for the banner, the header and the
		// help bar together. Clip from the bottom: losing the help bar beats
		// the renderer eating the top of the screen, which is where the
		// verdict and the guidance under it live.
		out = lipgloss.NewStyle().MaxHeight(m.height).Render(out)
	}
	return out
}

// bodyMinRows is the shortest useful results block: both borders, the Checks
// title, and one probe row. Below that the block is dropped rather than
// rendered as a frame with nothing in it.
const bodyMinRows = 4

// consequenceLabel marks a failed row the diagnosis has already explained as
// downstream of the failure it blames. It is spelled out rather than left to
// the dimmed glyph, because colour alone is not a distinction every reader or
// every terminal can see.
const consequenceLabel = "consequence"

// detailsMinWidth is the narrowest the Details panel is allowed to be beside
// the Checks panel. Below it the evidence lines wrap into unreadable stubs.
const detailsMinWidth = 36

// labelRight sets label against the right edge of a row in a panel width
// columns wide, the way the network map pairs its title with the shared
// domain. A row too long to share its line keeps the label on a second one,
// still at the right edge, rather than dropping it or cutting the probe name.
// The pair stays one row as far as the panel is concerned, so it scrolls as
// one and the block's budget prices both of its display rows.
func labelRight(row, label string, width int) string {
	if gap := width - lipgloss.Width(row) - lipgloss.Width(label); gap > 0 {
		return row + strings.Repeat(" ", gap) + label
	}
	return row + "\n" + lipgloss.NewStyle().Width(width).Align(lipgloss.Right).Render(label)
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

// bodyView renders the Checks panel and, beside it, the Details panel when
// there is anything to put in one. They stack vertically when the terminal is
// too narrow for two columns. rows caps the block's display height; 0 means no
// cap. A capped block scrolls its probe list rather than being clipped from the
// bottom, because clipping a bordered panel eats the border that closes it.
//
// Neither panel is padded out to the other's height: each is as tall as its own
// content, and the block is as tall as the taller of the two.
func (m model) bodyView(deferred bool, rows int) string {
	shown, hiddenPass, hiddenNA := m.compactRows()
	// The cursor row is always in shown, so the windowed panel still scrolls to
	// the row the Details panel is describing.
	sel := max(slices.Index(shown, m.selected), 0)
	pad := panelStyle.GetHorizontalPadding()
	// Which of the failed rows the diagnosis has already explained as
	// downstream of another one. It is read from the current results on every
	// render, so a watch pass that repairs the path takes the labels with it.
	collateral := diagnostic.Collateral(m.target, m.probeOrder(), m.results)

	// The rows are built before the panel has a width, because a labelled row
	// is what decides that width, so the label is placed in a second pass.
	label := faintStyle.Render(consequenceLabel)
	checks := make([]string, 0, len(shown))
	labelled := make([]bool, 0, len(shown))
	want := 0
	for _, i := range shown {
		probe := m.probes[i]
		if deferred {
			checks = append(checks, faintStyle.Render("  · "+probe.Name))
			labelled = append(labelled, false)
			continue
		}
		marker, name := "  ", probe.Name
		if i == m.selected {
			marker, name = selStyle.Render("› "), selStyle.Render(name)
		}
		// A collateral failure keeps its glyph and its Fail status; only the
		// red comes off it, so the row still carrying red is the one the
		// reader has something to do about.
		glyph := m.glyph(probe.ID)
		if collateral[probe.ID] {
			glyph = faintStyle.Render(probeGlyph(diagnostic.StatusFail))
		}
		row := marker + glyph + " " + name
		if m.watch && len(m.runHistory[probe.ID]) > 0 {
			row += "  " + m.statusSparkline(probe.ID, 8)
		}
		checks = append(checks, row)
		labelled = append(labelled, collateral[probe.ID])
		if collateral[probe.ID] {
			want = max(want, lipgloss.Width(row)+1+lipgloss.Width(label)+pad)
		}
	}
	// checkPanel is the Checks panel's rows once the panel width is settled:
	// the labels are placed against that width, so they land at its right edge.
	checkPanel := func(width int) []string {
		out := make([]string, 0, len(checks)+2)
		out = append(out, panelTitleStyle.Render("Checks"))
		for j, row := range checks {
			if labelled[j] {
				row = labelRight(row, label, width-pad)
			}
			out = append(out, row)
		}
		if hiddenPass+hiddenNA > 0 {
			out = append(out, m.collapsedChecksRow(hiddenPass, hiddenNA))
		}
		return out
	}

	rightRows := m.detailRows(deferred)
	frame := panelStyle.GetVerticalFrameSize()

	if m.width < 80 { // too narrow for two columns, so stack
		w := max(m.width-2, 24)
		leftRows := checkPanel(w)
		stack := func(left, right []string) string {
			panel := panelStyle.Width(w).Render(strings.Join(left, "\n"))
			if len(right) == 0 {
				return panel
			}
			return lipgloss.JoinVertical(lipgloss.Left,
				panel, panelStyle.Width(w).Render(strings.Join(right, "\n")))
		}
		if rows <= 0 {
			return stack(leftRows, rightRows)
		}
		// Stacked, there are two borders to pay for. Details keeps at most half
		// of what is left, so a long attempt list cannot squeeze the checks
		// out, and it gives its panel up entirely when the two of them still do
		// not fit. Whether they fit is measured rather than predicted: a probe
		// row that wraps costs a display row no row count can see coming.
		inner := rows - 2*frame
		if right := panelBody(fitRows(rightRows, max(inner/2, 1), w-pad)); len(right) > 0 {
			both := stack(windowRows(leftRows, sel, inner-displayRows(right, w-pad), w-pad), right)
			if lipgloss.Height(both) <= rows {
				return both
			}
		}
		// Checks on its own, so there is only one border to pay for.
		return fitBlock(stack(windowRows(leftRows, sel, rows-frame, w-pad), nil), rows)
	}
	leftW := 38
	if want > leftW {
		// A label pushes the widest probe row past the panel's usual width,
		// and a row that wraps costs the block a display row its budget never
		// saw coming. Take the columns those rows ask for, but never out of
		// the width the Details panel is guaranteed beside them: a terminal
		// with nothing to spare takes the wrap instead.
		leftW = min(want, max(m.width-detailsMinWidth-5, leftW))
	}
	leftRows := checkPanel(leftW)
	rightW := max(m.width-leftW-5, detailsMinWidth)
	if rows > 0 {
		leftRows = windowRows(leftRows, sel, rows-frame, leftW-pad)
		rightRows = panelBody(fitRows(rightRows, rows-frame, rightW-pad))
	}
	left := panelStyle.Width(leftW).Render(strings.Join(leftRows, "\n"))
	if len(rightRows) == 0 {
		return fitBlock(left, rows)
	}
	return fitBlock(lipgloss.JoinHorizontal(lipgloss.Top, left, " ",
		panelStyle.Width(rightW).Render(strings.Join(rightRows, "\n"))), rows)
}

// detailRows is the Details panel: its title row followed by the evidence for
// the cursor row, or nil when there is no evidence to show. A panel with
// nothing under its title is a bordered box of whitespace, so it is left out
// rather than drawn empty, and it comes back the moment the cursor row has
// something to say.
func (m model) detailRows(deferred bool) []string {
	if deferred {
		return []string{
			panelTitleStyle.Render("Details"),
			faintStyle.Render("Nothing to show yet: the checks haven't run."),
		}
	}
	// No row to describe: --check and --skip can between them select nothing
	// at all, and a run with no checks has no evidence to lay out.
	if m.selected < 0 || m.selected >= len(m.probes) {
		return nil
	}
	probe := m.probes[m.selected]
	if m.explaining && m.selected == m.answerRow() {
		why := m.whyLines()
		if len(why) > 1 {
			rows := append([]string{panelTitleStyle.Render("Why: " + probe.Name)}, why[1:]...)
			return panelBody(rows)
		}
	}
	var body strings.Builder
	if r, ok := m.results[probe.ID]; ok {
		// The answer block above is already quoting this row's finding and its
		// remedy, a few rows higher and ahead of the panels, so the panel adds
		// only what that quote left out: it repeats the finding when the quote
		// had to be clipped to one row, and never repeats the remedy, which is
		// up there in full either way. A panel that says again what the answer
		// said reads as a second, competing answer. Every other row keeps both
		// lines, because nothing else on screen is saying them.
		_, quoted := m.evidenceLine(r.Detail)
		answered := m.selected == m.answerRow()
		if !answered || !quoted {
			body.WriteString(statusStyles[r.Status].Render(r.Status.String()) + ": " + r.Detail + "\n")
		}
		if !answered && (r.Status == diagnostic.StatusFail || r.Status == diagnostic.StatusWarn) && r.Fix != "" {
			body.WriteString(skipStyle.Render("Fix: ") + r.Fix + "\n")
		}
		// The remediation belongs to the diagnosis rather than to any one row,
		// so it is shown on the row the diagnosis focuses and nowhere else:
		// repeated under every cursor position it would read as advice about
		// whatever row the reader happened to land on. It goes here, above the
		// per-attempt evidence, because a run with sixteen connection attempts
		// must not push the one actionable block off the panel.
		if answered {
			body.WriteString(m.remediationBlock())
		}
		if r.Portal != nil && r.Portal.RedirectURL != "" {
			body.WriteString(faintStyle.Render("portal "+r.Portal.RedirectURL) + "\n")
		}
		if r.Source != nil {
			body.WriteString(faintStyle.Render("src "+r.Source.String()+" "+r.Iface) + "\n")
		}
		// One line per destination the operating system was asked about, and
		// only where it answered. A platform that cannot answer shows nothing
		// here rather than a row of empty fields.
		for _, route := range r.Routes {
			if summary := route.Summary(); summary != "" {
				body.WriteString(faintStyle.Render("  route "+route.Destination.String()+": "+summary) + "\n")
			}
		}
		for _, a := range r.Attempts {
			st := "ok"
			if a.Err != nil {
				st = a.Err.Error()
			}
			body.WriteString(faintStyle.Render(fmt.Sprintf("  %s %dms %s", a.IP, diagnostic.Ms(a.Dur), st)) + "\n")
		}
	} else {
		body.WriteString(m.spinner.View() + faintStyle.Render(" checking…") + "\n")
	}
	if history := m.historyLine(probe.ID); history != "" {
		body.WriteString(history + "\n")
	}
	rows := append([]string{panelTitleStyle.Render("Details: " + probe.Name)},
		strings.Split(strings.TrimRight(body.String(), "\n"), "\n")...)
	return panelBody(rows)
}

func (m model) whyLines() []string {
	d := m.diagnosis()
	if len(d.Findings) == 0 || len(d.Findings[0].Evidence) == 0 {
		return nil
	}
	finding := d.Findings[0]
	lines := []string{"Why?"}
	sections := []struct {
		kind  diagnostic.EvidenceKind
		title string
	}{
		{diagnostic.EvidenceSupport, "Evidence"},
		{diagnostic.EvidenceRuledOut, "Ruled out"},
		{diagnostic.EvidenceContradiction, "Against alternatives"},
		{diagnostic.EvidenceNotEvaluated, "Not evaluated"},
	}
	for _, section := range sections {
		start := len(lines)
		for _, evidence := range finding.Evidence {
			if evidence.Kind != section.kind {
				continue
			}
			if len(lines) == start {
				lines = append(lines, section.title)
			}
			lines = append(lines, "  "+m.causalEvidenceLine(evidence))
		}
	}
	return lines
}

func (m model) causalEvidenceLine(e diagnostic.CausalEvidence) string {
	observation := m.observationLine(e)
	switch e.Kind {
	case diagnostic.EvidenceRuledOut:
		return "✓ " + diagnosisLabel(e.Candidate) + ": " + observation
	case diagnostic.EvidenceContradiction:
		return "! " + diagnosisLabel(e.Candidate) + ": " + observation
	case diagnostic.EvidenceNotEvaluated:
		return "⊘ " + m.probeLabel(e.Check) + ": " + notEvaluatedLabel(e.Reason)
	default:
		return evidenceGlyph(e.Observation) + " " + observation
	}
}

func evidenceGlyph(observation diagnostic.ObservationID) string {
	switch observation {
	case diagnostic.ObservationStatusPass, diagnostic.ObservationDNSAnswers,
		diagnostic.ObservationFamilyReachable, diagnostic.ObservationAddressSucceeded,
		diagnostic.ObservationRouteDirect:
		return "✓"
	case diagnostic.ObservationStatusWarn, diagnostic.ObservationClockOffset,
		diagnostic.ObservationRouteTunneled, diagnostic.ObservationRoutePathDiffers,
		diagnostic.ObservationRouteFamilySplit, diagnostic.ObservationRouteInterfaceMTU:
		// A tunnel, a split path, and a narrow link are observations about the
		// path, not failures of the row that recorded them.
		return "!"
	case diagnostic.ObservationStatusSkip, diagnostic.ObservationStatusNA:
		return "⊘"
	default:
		return "✗"
	}
}

// routeTunnelNamed reports whether the operating system itself called this
// row's route out of iface an encapsulating device, as opposed to the link
// merely looking like one. It is the difference between a sentence netdoc may
// state and one it may only suggest.
func routeTunnelNamed(r diagnostic.ProbeResult, iface string) bool {
	return slices.ContainsFunc(r.Routes, func(route diagnostic.RouteDecision) bool {
		return route.Iface == iface && route.Tunnel == diagnostic.TunnelKnown
	})
}

func (m model) observationLine(e diagnostic.CausalEvidence) string {
	name := m.probeLabel(e.Check)
	r, ok := m.results[e.Check]
	switch e.Observation {
	case diagnostic.ObservationStatusPass:
		return name + " passed"
	case diagnostic.ObservationStatusWarn:
		return name + " worked with a warning"
	case diagnostic.ObservationStatusFail:
		if ok && r.Detail != "" {
			return name + ": " + r.Detail
		}
		return name + " failed"
	case diagnostic.ObservationCause:
		if ok && r.Detail != "" {
			return name + ": " + r.Detail
		}
		return name + " identified the failure"
	case diagnostic.ObservationDNSAnswers:
		if e.Value != "" {
			return name + " returned " + e.Value
		}
		return name + " returned an address"
	case diagnostic.ObservationDNSNotFound:
		return name + " returned no A/AAAA records"
	case diagnostic.ObservationCaptivePortal:
		return name + " was intercepted by a network sign-in page"
	case diagnostic.ObservationTimeout:
		return name + " timed out"
	case diagnostic.ObservationClockOffset:
		return name + " measured a material clock offset"
	case diagnostic.ObservationStatusDowngraded:
		return name + " failed before another working path reduced its severity"
	case diagnostic.ObservationFamilyReachable:
		return name + " reached the target over " + addressFamilyLabel(e.Value)
	case diagnostic.ObservationFamilyFailed:
		return name + " could not reach the target over " + addressFamilyLabel(e.Value)
	case diagnostic.ObservationAddressSucceeded:
		return name + " reached " + e.Value
	case diagnostic.ObservationAddressFailed:
		return name + " could not reach " + e.Value
	case diagnostic.ObservationRouteTunneled:
		// How sure this sentence may sound is the row's own tunnel state. The
		// operating system naming the device an encapsulating kind is a fact
		// to repeat; a link that merely has the shape of a tunnel is a guess,
		// and a mobile broadband modem has the same shape.
		if e.Value == "" {
			return name + " left through a tunnel"
		}
		if ok && routeTunnelNamed(r, e.Value) {
			return name + " left through the tunnel " + e.Value
		}
		return name + " left over " + e.Value + ", which has the shape of a tunnel"
	case diagnostic.ObservationRouteDirect:
		// The counterpart claim, and a weaker one than it reads: nothing
		// classified this link as encapsulating, which is not the same as
		// having established that nothing encapsulates it. A VPN that presents
		// an ordinary Ethernet device lands here.
		if e.Value != "" {
			return name + " left over " + e.Value + ", which is not reported as a tunnel"
		}
		return name + " left over a link that is not reported as a tunnel"
	case diagnostic.ObservationRouteUnreachable:
		if e.Value != "" {
			return name + " has no route to " + e.Value
		}
		return name + " has no route to its destination"
	case diagnostic.ObservationRoutePathDiffers:
		if e.Value != "" {
			return name + " takes a different path from the target traffic on " + e.Value
		}
		return name + " takes a different path from the target traffic"
	case diagnostic.ObservationRouteFamilySplit:
		return name + " reaches IPv4 and IPv6 over different interfaces"
	case diagnostic.ObservationRouteInterfaceMTU:
		// The link's own MTU against the general path's link MTU. Neither side
		// is a measured path MTU, and this never says it is.
		if e.Value != "" {
			return name + " left over " + e.Value + ", whose link MTU is smaller than the general path's"
		}
		return name + " left over a link whose MTU is smaller than the general path's"
	case diagnostic.ObservationStatusSkip:
		return name + " was skipped"
	case diagnostic.ObservationStatusNA:
		return name + " did not apply"
	}
	return name + " supplied evidence"
}

func addressFamilyLabel(value string) string {
	switch value {
	case "ipv4":
		return "IPv4"
	case "ipv6":
		return "IPv6"
	}
	return value
}

func (m model) probeLabel(id diagnostic.ProbeID) string {
	for _, probe := range m.probes {
		if probe.ID == id {
			return probe.Name
		}
	}
	switch id {
	case diagnostic.ProbeInternet:
		return "General internet reachability"
	case diagnostic.ProbeTargetTCP:
		return "Target TCP connection"
	case diagnostic.ProbeDNS:
		return "System DNS"
	case diagnostic.ProbeDNSPublic:
		return "Independent DNS"
	}
	return "Relevant check"
}

func diagnosisLabel(id diagnostic.DiagnosisID) string {
	switch id {
	case diagnostic.DiagnosisOffline:
		return "General network outage"
	case diagnostic.DiagnosisDNSFailure:
		return "General DNS failure"
	case diagnostic.DiagnosisDNSNameNotFound:
		return "Missing DNS record"
	case diagnostic.DiagnosisSystemDNSFailure:
		return "Configured resolver problem"
	case diagnostic.DiagnosisLocalDeviceUnreachable:
		return "Device unreachable"
	case diagnostic.DiagnosisTargetUnreachable:
		return "Destination unreachable"
	case diagnostic.DiagnosisLocalEgressFailure:
		return "This machine's general network path"
	case diagnostic.DiagnosisIPv4TargetUnreachable:
		return "IPv4 target connectivity"
	case diagnostic.DiagnosisIPv6TargetUnreachable:
		return "IPv6 target connectivity"
	case diagnostic.DiagnosisPartialReachability:
		return "Partial endpoint reachability"
	case diagnostic.DiagnosisTLSClockSkew:
		return "Wrong local clock"
	case diagnostic.DiagnosisTLSTCPUnreachable:
		return "TLS endpoint unreachable"
	case diagnostic.DiagnosisTLSHandshakeFailure:
		return "TLS handshake problem"
	}
	return "Alternative explanation"
}

func notEvaluatedLabel(reason diagnostic.NotEvaluatedReason) string {
	switch reason {
	case diagnostic.NotEvaluatedPrerequisite:
		return "a prerequisite failed"
	case diagnostic.NotEvaluatedNotSelected:
		return "the check was not selected"
	case diagnostic.NotEvaluatedNotApplicable:
		return "the check did not apply"
	case diagnostic.NotEvaluatedIncomplete:
		return "the check did not complete"
	}
	return "no observation was available"
}

// remediation is this run's structured next action, and false when the
// diagnosis supports none: an unfinished run, a healthy one, or a verdict
// about no single conclusion. It reads the model's one diagnosis, so the
// advice cannot point somewhere the banner and the cursor do not.
func (m model) remediation() (diagnostic.Remediation, bool) {
	return diagnostic.Remediate(m.diagnosis(), m.results, runtime.GOOS)
}

// remediationBlock is the Details panel's remediation section, newline
// terminated and empty when there is nothing to advise. It is progressive
// disclosure rather than a screen of its own: the answer block above the
// panels already carries the verdict, the row's own hint and the evidence, and
// this is the part a reader who wants to act on it opens the row for.
//
// The command is shown, never run. netdoc is a diagnostic tool, and a
// remediation command is a thing to read and decide about, which is also why
// it is only ever a read-only inspection of local state.
func (m model) remediationBlock() string {
	rem, ok := m.remediation()
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(skipStyle.Render("Do: ") + rem.Action + "\n")
	if rem.Why != "" {
		b.WriteString(faintStyle.Render(rem.Why) + "\n")
	}
	for _, step := range rem.Steps {
		b.WriteString("· " + step + "\n")
	}
	if line := rem.CommandLine(); line != "" {
		b.WriteString(faintStyle.Render("Run: ") + line + "\n")
	}
	if rem.Expect != "" {
		b.WriteString(faintStyle.Render("Expect: "+rem.Expect) + "\n")
	}
	// The payoff line: the advice is only half a workflow without the way to
	// ask the same question again. Dropped when the action has no key, since a
	// custom keymap is free to leave it unbound.
	if m.keys.bound(ctxList, actRetest) {
		b.WriteString(faintStyle.Render("Then press ") + selStyle.Render(m.keys.label(ctxList, actRetest)) +
			faintStyle.Render(" to retest") + "\n")
	}
	return b.String()
}

// panelBody drops a panel that has a title and nothing readable under it,
// which the reader sees as a bordered box of whitespace. Emptiness is measured
// on the text with its styling stripped, because a row of spaces or of escape
// sequences alone still draws the box.
func panelBody(rows []string) []string {
	if len(rows) < 2 {
		return nil
	}
	for _, row := range rows[1:] {
		if strings.TrimSpace(ansi.Strip(row)) != "" {
			return rows
		}
	}
	return nil
}

// fitBlock is bodyView's last word on its own row budget: a block that still
// does not fit is dropped, because View counts the rows it hands back and a
// bordered panel cannot be clipped without losing the border that closes it.
func fitBlock(block string, rows int) string {
	if rows > 0 && lipgloss.Height(block) > rows {
		return ""
	}
	return block
}

// displayRows is what rows cost on screen once a panel width columns wide has
// wrapped them. Budgeting in logical rows instead undercounts every row that
// wraps, which is how a narrow terminal used to overflow its own row budget.
func displayRows(rows []string, width int) int {
	total := 0
	for _, r := range rows {
		total += rowCost(r, width)
	}
	return total
}

// rowCost is what one row costs on screen once a panel width columns wide has
// wrapped it. Lip Gloss breaks on word boundaries, so dividing the row's width
// by the panel's undercounts every row that has to break early at a space.
// Render it at that width and count instead: the renderer is the only oracle
// that cannot drift from what actually lands on screen.
func rowCost(row string, width int) int {
	if width <= 0 {
		return 1
	}
	return lipgloss.Height(lipgloss.NewStyle().Width(width).Render(row))
}

// fitRows keeps the longest prefix of rows that costs at most n display rows.
func fitRows(rows []string, n, width int) []string {
	spent := 0
	for i, r := range rows {
		if spent += rowCost(r, width); spent > n {
			return rows[:i]
		}
	}
	return rows
}

// windowRows fits a panel's rows into n display rows in a panel width columns
// wide. rows[0] is the panel title and stays pinned; the list below it scrolls
// so that sel, an index into that list, is always on screen. A truncated list
// spends its last row saying how many it hid, since nothing else on the panel
// admits that it goes on.
func windowRows(rows []string, sel, n, width int) []string {
	if len(rows) < 2 || displayRows(rows, width) <= n {
		return rows // a bare title has nothing to scroll
	}
	list := rows[1:]
	sel = min(max(sel, 0), len(list)-1)
	// The cursor row is not optional: a window that drops it to keep inside the
	// budget shows the reader a row they did not select, or no rows at all. So
	// it is spent first and the rest grows outward from it, downward first. A
	// cursor row that overruns the budget on its own leaves fitBlock to drop
	// the block, which beats a panel pointing at the wrong row.
	budget := n - rowCost(rows[0], width) - rowCost(list[sel], width)
	lo, hi := sel, sel+1
	// The "… N more" line costs rows of its own, priced at the widest count it
	// could carry, and it yields to the last row the list can still show: a
	// panel with the cursor in it beats a panel that only says how much it is
	// hiding.
	cost := rowCost(faintStyle.Render(fmt.Sprintf("… %d more", len(list))), width)
	marker := budget >= cost
	if marker {
		budget -= cost
	}
	for grow := true; grow; {
		grow = false
		if hi < len(list) {
			if c := rowCost(list[hi], width); c <= budget {
				budget -= c
				hi++
				grow = true
			}
		}
		if lo == 0 {
			continue
		}
		if c := rowCost(list[lo-1], width); c <= budget {
			budget -= c
			lo--
			grow = true
		}
	}
	shown := append([]string{rows[0]}, list[lo:hi]...)
	if hidden := len(list) - (hi - lo); marker && hidden > 0 {
		shown = append(shown, faintStyle.Render(fmt.Sprintf("… %d more", hidden)))
	}
	return shown
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

// serviceChooserView renders what one opened device answered on the common
// service ports: the endpoints the checks can be pointed at, or, when there
// are none, exactly what was learned instead of guessing at one.
func (m model) serviceChooserView() string {
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Services on "+m.svc.name) + "\n")
	scan := m.svc.scan
	switch {
	case !m.svc.done:
		b.WriteString(m.spinner.View() + faintStyle.Render(" checking common service ports…") + "\n")
	case len(scan.Open) > 0:
		for i, svc := range scan.Open {
			branch := "├─ "
			if i == len(scan.Open)-1 {
				branch = "└─ "
			}
			row := fmt.Sprintf("%-5d %s", svc.Port, svc.Name)
			marker := "  "
			if i == m.svc.sel {
				marker, row = selStyle.Render("› "), selStyle.Render(row)
			}
			b.WriteString(marker + faintStyle.Render(branch) + passStyle.Render("●") + " " + row + "\n")
		}
	case scan.Refused > 0:
		// A refusal is an answer: the device is there and reachable, and the
		// only thing missing is something listening.
		b.WriteString(faintStyle.Render(fmt.Sprintf("└─ No common service answered, but %d of %d ports refused the connection, so the device is on the network.", scan.Refused, scan.Checked())) + "\n")
		b.WriteString(faintStyle.Render("Press "+m.keys.label(ctxList, actRestart)+" to name a port yourself.") + "\n")
	default:
		b.WriteString(faintStyle.Render(fmt.Sprintf("└─ Nothing answered on any of the %d ports checked: the device may be powered off, may have left the network, or may be dropping connections.", scan.Checked())) + "\n")
		b.WriteString(faintStyle.Render("Press "+m.keys.label(ctxList, actRestart)+" to name a port yourself.") + "\n")
	}
	return panelStyle.Width(max(m.width-2, 24)).Render(strings.TrimRight(b.String(), "\n"))
}

// networkMapView renders hosts found by the LAN scan, or the services of the
// device opened from it.
func (m model) networkMapView() string {
	if m.svc.host != "" {
		return m.serviceChooserView()
	}
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
	// Only the device-list branch cares how many devices there are, and
	// networkHosts walks the whole job line buffer, so don't pay for it on
	// every checks frame or while the services of one device are on screen.
	hosts := 0
	if m.networkMap && m.svc.host == "" {
		hosts = len(m.networkHosts())
	}
	// Every chip is looked up in the selected preset.
	var kv []string
	add := func(label, desc string) {
		if label != "" {
			kv = append(kv, label, desc)
		}
	}
	addAction := func(act keyAction) {
		help, ok := actionHelpFor(ctxList, act)
		if ok {
			add(m.keys.label(ctxList, act), help.bar)
		}
	}
	addPair := func(a, b keyAction) {
		help, ok := actionHelpFor(ctxList, a)
		if ok {
			add(m.keys.pairLabel(ctxList, a, b), help.bar)
		}
	}
	switch {
	case m.networkMap && m.svc.host != "":
		if len(m.svc.scan.Open) > 0 {
			add(m.keys.pairLabel(ctxList, actUp, actDown), "select service")
			addPair(actTop, actBottom)
			add(m.keys.label(ctxList, actOpen), "diagnose it")
		}
		add(m.keys.label(ctxList, actCancelJob), "devices")
		add(m.keys.label(ctxList, actNetworkMap), "checks")
	case m.networkMap:
		if hosts > 0 {
			add(m.keys.pairLabel(ctxList, actUp, actDown), "select device")
			addPair(actTop, actBottom)
			add(m.keys.label(ctxList, actOpen), "open device")
		}
		add(m.keys.label(ctxList, actNetworkMap), "checks")
	case deferred:
		add(m.keys.label(ctxList, actRestart), "run the checks")
		addAction(actNetworkMap)
		if len(m.tools) > 0 {
			kv = append(kv, "letter", "runs that tool")
		}
	default:
		add(m.keys.pairLabel(ctxList, actUp, actDown), "scroll")
		addPair(actTop, actBottom)
		addAction(actNetworkMap)
		// The way back is only on the help bar: expanding removes the summary
		// line that advertised the key.
		if _, hiddenPass, hiddenNA := m.compactRows(); m.expanded && m.allDone() {
			add(m.keys.label(ctxList, actExpand), "collapse")
		} else if hiddenPass+hiddenNA > 0 || m.toolsCollapsed() {
			addAction(actExpand)
		}
	}
	// Open works whenever a job pane exists (same condition as jobView), so the
	// hint tracks exactly when the key does something. On the map it opens a
	// device or diagnoses a service instead, so it is only the empty device
	// list that leaves the key free.
	if m.hasJob() && (!m.networkMap || m.svc.host == "" && hosts == 0) {
		add(m.keys.label(ctxList, actOpen), "full output")
	}
	if !deferred && m.cur.active != nil {
		addAction(actCancelJob)
	}
	if len(m.otherJobs) > 0 {
		addAction(actSwitchJob)
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
		addAction(actHelp)
		addAction(actQuit)
		return withNotice(m.chordHint(helpKeys(m.width, kv...)))
	}
	if d := m.diagnosis(); m.allDone() && len(d.Findings) > 0 && len(d.Findings[0].Evidence) > 0 {
		if m.explaining {
			add(m.keys.label(ctxList, actExplain), "details")
		} else {
			addAction(actExplain)
		}
	}
	if m.selectedPortalURL() != "" {
		add(m.keys.label(ctxList, actCopy), "copy portal URL")
	} else if m.reportReady() {
		add(m.keys.label(ctxList, actCopy), "copy report")
	}
	if m.reportReady() {
		addAction(actSave)
	}
	// Retest is offered once there is a finished run to rerun. Before that the
	// chain is either already running or waiting on the restart key, and a
	// second way to start it would only be a second name for r.
	if m.allDone() && m.chainRan() {
		addAction(actRetest)
	}
	if m.sshDetected() {
		addAction(actSSH)
	}
	addAction(actRestart)
	addAction(actHelp)
	addAction(actQuit)
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
	summary, verdict := m.diagnose(m.probeOrder())
	st := verdictStatus(verdict)
	// Bold as well as coloured: the panel titles under this sentence are bold,
	// and the answer must not be the lighter of the two. A terminal that
	// renders no attributes at all drops both together, which is why the
	// hierarchy is carried by position, by the glyph and by the labels below,
	// and never by weight alone.
	lines := []string{statusStyles[st].Bold(true).Render(probeGlyph(st) + " " + summary)}
	// All three lines under the verdict follow the row the diagnosis blames
	// rather than the first failing row: a path MTU black hole fails TLS but
	// the evidence and the remedy are both on the Path MTU row, and a "Fix:"
	// taken from one row with a "Next:" taken from another sends the reader
	// two ways at once.
	i := m.answerRow()
	if i < 0 {
		// Nothing to fix and nothing to chase, which is the one moment there
		// is room to say what else netdoc can be pointed at.
		if hint := m.localDeviceHint(); hint != "" {
			return lines[0] + "\n  " + hint
		}
		return lines[0]
	}
	blamed := m.probes[i].ID
	if fix := m.results[blamed].Fix; fix != "" {
		lines = append(lines, faintStyle.Render("  Fix: "+fix))
	}
	if next := m.nextStep(blamed); next != "" {
		lines = append(lines, "  "+next)
	}
	// The evidence comes last, because it is what supports the answer rather
	// than what the reader does about it. One row: it is quoted here to save
	// the reader a trip to the Checks panel for the "why", not to reproduce
	// the panel, and the cursor is already parked on the row that holds the
	// whole of it.
	if line, _ := m.evidenceLine(m.results[blamed].Detail); line != "" {
		lines = append(lines, faintStyle.Render(line))
	}
	return strings.Join(lines, "\n")
}

// localDeviceHint is the way into the local-device workflow for a reader with
// no failure to chase. A finished targetless run on a private network has
// nothing else to suggest, and finding another device is the part of netdoc
// least likely to be stumbled upon. Empty everywhere else, including on a
// machine with no private IPv4 network to sweep, where the key would only
// answer that there is nothing to map.
func (m model) localDeviceHint() string {
	if m.target != nil || m.networkMap || !m.keys.bound(ctxList, actNetworkMap) {
		return ""
	}
	if _, cidr := m.discoveryNetwork(); cidr == "" {
		return ""
	}
	return "Next: press " + selStyle.Render(m.keys.label(ctxList, actNetworkMap)) + " to find a device on your network and diagnose it"
}

// evidenceLine is the answer block's quote of a probe's finding, clipped to one
// terminal row, plus whether the whole finding fit inside it. The clip is what
// holds the evidence to one line however long a probe's detail runs: some of
// them are a paragraph, and supporting evidence that reflows into six rows
// crowds out the answer it is supporting. The Details panel reads the second
// return to decide whether it still has the rest of that finding to add. Empty
// detail yields an empty line, and the caller drops it rather than labelling
// nothing.
func (m model) evidenceLine(detail string) (line string, whole bool) {
	if detail == "" {
		return "", true
	}
	line = "  Evidence: " + detail
	if m.width <= 0 {
		return line, true
	}
	clipped := ansi.Truncate(line, m.width, "…")
	return clipped, clipped == line
}

// answerRow is the probe row the answer block above the panels is quoting: the
// row the finished diagnosis blames, and -1 when the run is unfinished or the
// diagnosis blames no row. The Details panel reads it to avoid printing the
// same finding twice, a few rows apart, in two different places.
func (m model) answerRow() int {
	if !m.allDone() {
		return -1
	}
	return m.focusRow()
}

// probeOrder is the run's probe IDs in DAG order, which is what the diagnosis
// reads. It deliberately reports nothing about which row failed: the blamed
// row is Diagnose's call alone, and focusRow is the only place that asks.
func (m model) probeOrder() []diagnostic.ProbeID {
	order := make([]diagnostic.ProbeID, len(m.probes))
	for i, probe := range m.probes {
		order[i] = probe.ID
	}
	return order
}

// verdictStatus is the presentation severity of a finished run, and the
// diagnosis verdict is the only thing that decides it. A degraded verdict may
// legitimately carry a failed row (QUIC blocked while TCP carries the traffic,
// encrypted DNS blocked while plain DNS resolves), so painting the banner red
// for any failed row would contradict the sentence printed next to it.
func verdictStatus(verdict string) diagnostic.Status {
	switch verdict {
	case diagnostic.VerdictOK:
		return diagnostic.StatusPass
	case diagnostic.VerdictDegraded:
		return diagnostic.StatusWarn
	}
	return diagnostic.StatusFail
}

// focusRow is the probe row a finished run should put the cursor on and take
// its drill-down from: the row the diagnosis blames, which is the row the
// verdict names when it names one and otherwise the first failing row. -1 when
// the run blames no row at all.
//
// The choice is the diagnosis's, not this function's: it asks for the blamed
// row and only maps it to a list index. The first-failure scan that remains is
// about the list rather than the diagnosis, for the case where the blamed probe
// has no row to put a cursor on.
func (m model) focusRow() int {
	blamed := m.diagnosis().Blamed
	first := -1
	for i, probe := range m.probes {
		// A probe with no row cannot be the row to send the reader to, and
		// putting the cursor there would leave the Details panel describing a
		// row the list does not offer.
		if !hasCheckRow(probe.ID) {
			continue
		}
		if probe.ID == blamed {
			return i
		}
		if first < 0 && m.results[probe.ID].Status == diagnostic.StatusFail {
			first = i
		}
	}
	return first
}

// compactRows is the Checks panel's default row set: of the probes that have a
// row at all (see checkRows), the indices worth reading, and how many passing
// and not-applicable rows that left behind. A finished run keeps every Fail,
// Warn and Skip, the row the diagnosis blames
// (a path MTU black hole rests on a Warn, so severity alone would drop the one
// row the verdict sends the reader to), and the cursor row (the Details panel
// is showing it, and a panel describing a row the list does not offer is worse
// than the row it costs). Everything else is one summary line. Nothing is
// hidden while the run is still going, while the reader has expanded the list,
// or when the expand action has no key to reach it by.
//
// N/A rows collapse alongside the passing ones: an unset proxy or an
// unreachable second-opinion resolver is not something to act on, and the
// Checks panel answers what needs attention. Their evidence is not lost,
// because the cursor still walks every row and a hidden row the cursor lands
// on comes back with the Details panel that describes it.
//
// The blamed row comes from focusRow, so the list and the banner cannot
// disagree about which row matters. Every row FocusProbe can name today is
// also a Warn, so that clause currently keeps nothing the severity test would
// have dropped; it stays because the row worth reading is the diagnosis's
// call, not a severity comparison made over here.
func (m model) compactRows() (shown []int, hiddenPass, hiddenNA int) {
	all := m.checkRows()
	if m.expanded || !m.allDone() || !m.keys.bound(ctxList, actExpand) {
		return all, 0, 0
	}
	blamed := m.focusRow()
	for _, i := range all {
		probe := m.probes[i]
		if i == blamed || i == m.selected {
			shown = append(shown, i)
			continue
		}
		switch m.results[probe.ID].Status {
		case diagnostic.StatusPass:
			hiddenPass++
		case diagnostic.StatusNA:
			hiddenNA++
		default:
			shown = append(shown, i)
		}
	}
	return shown, hiddenPass, hiddenNA
}

// hasCheckRow reports whether a probe is worth a row in the Checks panel.
//
// The Wi-Fi probe is not. Its whole result is the network name, and the
// context strip under the banner already prints that name from the same
// result ("Wi-Fi: homewifi"), so the row is the same fact a second time. It
// carries no actionable finding to lose either: the probe reports Pass with
// the name or N/A without it, never a Warn or a Fail, and the interface row
// beside it is the one that speaks when the link itself is the problem. The
// probe keeps running: the context strip, the report, and the JSON output all
// read its result.
func hasCheckRow(id diagnostic.ProbeID) bool { return id != diagnostic.ProbeSSID }

// checkRows is the probe indices the Checks panel offers as rows, in probe
// order. It is also the list the cursor walks: an index that is not in here is
// a cursor position with no row under it and a Details panel describing
// something the reader cannot see.
func (m model) checkRows() []int {
	rows := make([]int, 0, len(m.probes))
	for i, probe := range m.probes {
		if hasCheckRow(probe.ID) {
			rows = append(rows, i)
		}
	}
	return rows
}

// collapsedChecksRow is the one line the hidden passing and not-applicable
// rows are worth. It counts what compactRows actually hid, never how many rows
// passed: the blamed row and the cursor row pass too and are still on screen.
// The two counts are named apart, because "passed" is a claim about the
// network and "N/A" is a claim about the question having no answer here.
//
// It sits in the marker column rather than indented with the probe rows,
// which is also what keeps it inside the 36 columns the Checks panel has:
// a row that wraps costs the panel a second display row that no row count
// saw coming, and the block has a row budget to stay inside. That budget is
// why the mixed wording is the terse one and why "N/A" is not spelled out.
func (m model) collapsedChecksRow(hiddenPass, hiddenNA int) string {
	checks := func(n int) string {
		if n == 1 {
			return "check"
		}
		return "checks"
	}
	var summary string
	switch {
	case hiddenNA == 0:
		summary = fmt.Sprintf("%d other %s passed", hiddenPass, checks(hiddenPass))
	case hiddenPass == 0:
		summary = fmt.Sprintf("%d other %s N/A", hiddenNA, checks(hiddenNA))
	default:
		summary = fmt.Sprintf("%d passed, %d N/A", hiddenPass, hiddenNA)
	}
	return faintStyle.Render(fmt.Sprintf("· %s (%s expand)", summary, m.keys.label(ctxList, actExpand)))
}

// toolsCollapsed reports whether the "Dig deeper" chips have to justify their
// rows. A finished run the diagnosis calls healthy has nothing to dig into, so
// they collapse to their count. Every other state keeps them: an unfinished
// run has no verdict yet, and an abnormal one puts the banner's "Next:" line
// one keypress from a chip it names.
//
// --toolbox is the state that outranks the verdict. That mode opens on the
// chips and holds the chain back until r, so the chips are what the reader
// came for; a clean run is not a reason to take away the thing they asked
// for. The checks still collapse there, since the diagnosis is the part a
// clean verdict has finished talking about.
func (m model) toolsCollapsed() bool {
	if m.toolbox || m.expanded || !m.allDone() || !m.keys.bound(ctxList, actExpand) {
		return false
	}
	_, verdict := m.diagnose(m.probeOrder())
	return verdict == diagnostic.VerdictOK
}

// probeNextTool maps a blamed probe to the toolbox hotkey that best
// investigates it.
var probeNextTool = map[diagnostic.ProbeID]string{
	diagnostic.ProbeInternet:  "p",
	diagnostic.ProbeDNS:       "d",
	diagnostic.ProbeTargetTCP: "t",
	diagnostic.ProbePMTU:      "t",
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
	// The SSH chip below is not in m.tools, so the count has to include it.
	if m.toolsCollapsed() {
		return titleStyle.Render("Dig deeper") + faintStyle.Render(fmt.Sprintf("  %d tools (%s expand)",
			len(m.tools)+1, m.keys.label(ctxList, actExpand))) + "\n"
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
