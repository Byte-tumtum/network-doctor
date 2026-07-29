// The tool-output ring buffer and the viewport that displays it: appends,
// eviction accounting, the viewer filter, and re-render sizing.

package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
	if _, ok := m.cur.routeQuality(); ok {
		h--
	}
	if h < 3 {
		h = 3
	}
	return h
}
