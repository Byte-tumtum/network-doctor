// The two panels of the results block: which probes earn a Checks row, when
// the Details panel is worth drawing at all, and the rule that neither panel
// is padded out to the other's height.

package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// panelHeight is the display height of the panel whose border opens at column
// col, its own border rows included, and 0 when no panel opens there.
func panelHeight(v string, col int) int {
	n := 0
	for _, line := range strings.Split(v, "\n") {
		runes := []rune(ansi.Strip(line))
		if col < len(runes) && strings.ContainsRune("╭│╰", runes[col]) {
			n++
		}
	}
	return n
}

// detailsPanelCol is the display column the Details panel's border opens at in
// the side-by-side layout, and -1 when the view drew no second panel.
func detailsPanelCol(v string) int {
	for _, line := range strings.Split(v, "\n") {
		runes := []rune(ansi.Strip(line))
		if len(runes) == 0 || runes[0] != '╭' {
			continue
		}
		if second := slices.Index(runes[1:], '╭'); second >= 0 {
			return second + 1
		}
		return -1
	}
	return -1
}

// wifiModel is a finished healthy run on a wireless machine: the interface
// probe names the link and the Wi-Fi probe carries the network name.
func wifiModel(t *testing.T) model {
	t.Helper()
	m := newModel(mustTarget(t, "example.com:443"), false)
	doneResults(&m, "")
	m.results[diagnostic.ProbeIface] = diagnostic.ProbeResult{
		ID: diagnostic.ProbeIface, Status: diagnostic.StatusPass,
		Iface: "wlan0", Detail: "interface wlan0 is up",
	}
	m.results[diagnostic.ProbeSSID] = diagnostic.ProbeResult{
		ID: diagnostic.ProbeSSID, Status: diagnostic.StatusPass,
		Network: "homewifi", Detail: "connected to homewifi",
	}
	return m
}

// TestNetworkNameIsToldOnceOnTheContextStrip: the context strip under the
// banner already prints "Wi-Fi: homewifi" from the Wi-Fi probe's result, so a
// Checks row saying the same thing is the same fact twice. Collapsed or
// expanded, the panel does not draw one.
func TestNetworkNameIsToldOnceOnTheContextStrip(t *testing.T) {
	m, compact := renderAt(t, wifiModel(t))
	strip := m.headerView()
	if !strings.Contains(ansi.Strip(strip), "Wi-Fi: homewifi") {
		t.Fatalf("the context strip lost the network name: %q", ansi.Strip(strip))
	}
	if !hasLine(compact, strip) {
		t.Errorf("the context strip is not on screen:\n%s", compact)
	}
	// Expanded, so every row the Checks panel has to offer is on screen and a
	// missing row cannot be blamed on the compact view collapsing it.
	expanded, ev := renderAt(t, press(t, m, "a"))
	if collapsedRow(ev) != "" {
		t.Fatalf("the expanded view is still collapsing rows:\n%s", ev)
	}
	name := probeName(t, m, diagnostic.ProbeSSID)
	for _, tc := range []struct{ where, v string }{{"compact", compact}, {"expanded", ev}} {
		if hasRow(tc.v, name) {
			t.Errorf("%s: the Checks panel still draws the %q row:\n%s", tc.where, name, tc.v)
		}
		for _, row := range checksRows(tc.v) {
			if strings.Contains(row, "homewifi") {
				t.Errorf("%s: a Checks row repeats the network name: %q\n%s", tc.where, row, tc.v)
			}
		}
	}
	// The probe itself is untouched: everything else still reads its result.
	if got := expanded.results[diagnostic.ProbeSSID].Network; got != "homewifi" {
		t.Errorf("the Wi-Fi probe result was dropped along with its row: Network = %q", got)
	}
	if rep := expanded.report(); !strings.Contains(rep, "homewifi") {
		t.Errorf("the report lost the network name:\n%s", rep)
	}
}

// TestCursorNeverStopsOnARowlessProbe: hiding a row without taking it out of
// the cursor's path would leave the reader on a position with no row under it
// and a Details panel describing something they cannot see.
func TestCursorNeverStopsOnARowlessProbe(t *testing.T) {
	m := press(t, wifiModel(t), "a")
	m.width, m.height = 100, 40
	ssid := probeIndex(t, m, diagnostic.ProbeSSID)
	rows := m.checkRows()
	if slices.Contains(rows, ssid) {
		t.Fatalf("the Wi-Fi probe still has a row, so there is nothing to skip over")
	}

	seen := map[int]bool{}
	walk := func(key string) {
		for range len(m.probes) + 2 {
			if m.selected == ssid {
				t.Fatalf("%q parked the cursor on the rowless Wi-Fi probe", key)
			}
			seen[m.selected] = true
			m = press(t, m, key)
		}
	}
	walk("j")
	walk("k")
	for _, key := range []string{"G", "g"} { // bottom, then top on the default map
		m = press(t, m, key)
		if m.selected == ssid {
			t.Fatalf("%q parked the cursor on the rowless Wi-Fi probe", key)
		}
	}
	if len(seen) != len(rows) {
		t.Errorf("the cursor reached %d of the %d rows the panel offers", len(seen), len(rows))
	}
	// Every stop the cursor made is a row the panel is actually showing.
	for i := range seen {
		if v := m.View(); !strings.Contains(v, "Details: "+m.probes[i].Name) && m.selected == i {
			t.Errorf("Details is not describing the cursor row %q:\n%s", m.probes[i].Name, v)
		}
	}
}

// TestCollapsedCountIsWhatExpandBringsBack: the summary line stands in for
// rows, so it must count rows. A probe with no row of its own is not one of
// them, and counting it would promise the reader a row expand cannot deliver.
func TestCollapsedCountIsWhatExpandBringsBack(t *testing.T) {
	m, compact := renderAt(t, wifiModel(t))
	summary := collapsedRow(compact)
	if summary == "" {
		t.Fatalf("nothing collapsed, so there is no count to check:\n%s", compact)
	}
	_, ev := renderAt(t, press(t, m, "a"))
	gained := len(checksRows(ev)) - (len(checksRows(compact)) - 1) // the summary is not a row
	if !strings.Contains(summary, fmt.Sprintf("%d other checks passed", gained)) {
		t.Errorf("summary = %q, but expand brought back %d rows:\n%s", summary, gained, ev)
	}
}

// noChecksModel is the run that selects nothing: every probe hangs off the
// interface row, so skipping that one leaves an empty DAG. The run is finished
// and healthy the moment it starts, and there is no row to describe.
func noChecksModel(t *testing.T) model {
	t.Helper()
	selection := diagnostic.ProbeSelection{
		Skip: map[diagnostic.ProbeID]struct{}{diagnostic.ProbeIface: {}},
	}
	m := NewWithSelection(mustTarget(t, "example.com:443"), nil, false, false, "", "test",
		diagnostic.DefaultPublicDNS, selection).(model)
	if len(m.probes) != 0 {
		t.Fatalf("this selection still runs %d probes, so it has details to show", len(m.probes))
	}
	return m
}

// TestDetailsPanelIsDroppedWhenItHasNothingToSay: a bordered box of whitespace
// under a title is worse than no panel. The block is the Checks panel alone,
// with no orphaned title and no rows reserved for the panel that is not there.
//
// This is also the run that used to panic. With no probes selected there is no
// cursor row to describe, and the Details panel indexed the empty slice to look
// for one, so rendering it at all is half of what this test proves.
func TestDetailsPanelIsDroppedWhenItHasNothingToSay(t *testing.T) {
	m, v := renderAt(t, noChecksModel(t))
	if strings.Contains(ansi.Strip(v), "Details") {
		t.Errorf("an empty Details panel (or its title) is still drawn:\n%s", v)
	}
	if n := strings.Count(v, "╭"); n != 1 {
		t.Errorf("the block drew %d panels, want the Checks panel alone:\n%s", n, v)
	}
	if n := unclosedPanels(v); n != 0 {
		t.Errorf("%d panel border(s) left unclosed:\n%s", n, v)
	}
	// No blank rows kept back for the panel that is not drawn: the block is
	// exactly the Checks panel, which here is its title between two borders.
	if h := lipgloss.Height(m.bodyView(false, 0)); h != 3 {
		t.Errorf("the results block is %d rows tall, want 3 (border, title, border):\n%s",
			h, m.bodyView(false, 0))
	}
	// There is no row to move onto and none to blame, so every movement key has
	// to leave the cursor alone rather than walk off the end of an empty row set.
	if rows := m.checkRows(); len(rows) != 0 {
		t.Fatalf("this run offers %d rows, so the empty row set is not under test", len(rows))
	}
	for _, key := range []string{"j", "k", "G", "g"} {
		m = press(t, m, key)
		if m.selected != 0 {
			t.Fatalf("%q moved the cursor to %d on a run with no rows", key, m.selected)
		}
		if i := m.focusRow(); i >= 0 {
			t.Fatalf("after %q the view blames row %d of a run with no rows", key, i)
		}
		if v := m.View(); strings.Contains(ansi.Strip(v), "Details") || unclosedPanels(v) != 0 {
			t.Errorf("%q left the view malformed:\n%s", key, v)
		}
	}
}

// TestDetailsPanelComesBackWithContent is the other half of that rule: the
// panel is decided by what it would contain, not by the verdict. A healthy run
// still has a cursor row with evidence, and the panel is there to carry it.
func TestDetailsPanelComesBackWithContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) model
		want  string
	}{
		{"healthy run", wifiModel, "interface wlan0 is up"},
		{"failing row", evidenceModel, "no A or AAAA record"},
		{"checks not run yet", func(t *testing.T) model { return newModel(mustTarget(t, "example.com:443"), true) },
			"the checks haven't run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, v := renderAt(t, tc.build(t))
			if !strings.Contains(ansi.Strip(v), "Details") {
				t.Fatalf("the Details panel is missing:\n%s", v)
			}
			if !containsRow(detailsRows(v), tc.want) {
				t.Errorf("Details lost %q:\n%s", tc.want, v)
			}
		})
	}
}

// TestPanelBodyDropsAWhitespaceOnlyPanel: emptiness is measured on the text
// with its styling stripped, so a body that is only spaces, or only escape
// sequences, still counts as nothing to draw.
func TestPanelBodyDropsAWhitespaceOnlyPanel(t *testing.T) {
	title := panelTitleStyle.Render("Details: DNS")
	for _, tc := range []struct {
		name string
		rows []string
		want bool
	}{
		{"title alone", []string{title}, false},
		{"blank body", []string{title, ""}, false},
		{"spaces", []string{title, "   ", "\t"}, false},
		{"styling only", []string{title, faintStyle.Render("")}, false},
		{"nothing at all", nil, false},
		{"real content", []string{title, faintStyle.Render("src 192.0.2.7 eth0")}, true},
		{"blank row above content", []string{title, "  ", "PASS: up"}, true},
	} {
		if got := panelBody(tc.rows) != nil; got != tc.want {
			t.Errorf("%s: panelBody kept the panel = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPanelsTakeTheirOwnHeights: neither panel is padded out to match the
// other. Whichever has more to say is taller, and the block is as tall as that
// one rather than as tall as both.
func TestPanelsTakeTheirOwnHeights(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func(t *testing.T) model
		taller string
	}{
		// One collapsed row of checks beside a failing row's source identity
		// and its two per-address attempts.
		{"details taller", evidenceModel, "details"},
		// Every passing row expanded beside a one-line PASS.
		{"checks taller", func(t *testing.T) model { return press(t, wifiModel(t), "a") }, "checks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, v := renderAt(t, tc.build(t))
			col := detailsPanelCol(v)
			if col < 0 {
				t.Fatalf("no Details panel to compare heights against:\n%s", v)
			}
			checks, details := panelHeight(v, 0), panelHeight(v, col)
			if checks == details {
				t.Fatalf("both panels are %d rows tall, so one is still padded to the other:\n%s",
					checks, v)
			}
			if (details > checks) != (tc.taller == "details") {
				t.Fatalf("checks = %d rows, details = %d, want %s taller:\n%s",
					checks, details, tc.taller, v)
			}
			if got, want := lipgloss.Height(v), 40; got > want {
				t.Errorf("the view is %d rows on a %d-row terminal:\n%s", got, want, v)
			}
		})
	}
}

// TestUnevenPanelsStayInsideAShortTerminal: the panels no longer share a
// height, so the taller one is what the row budget has to answer for. It still
// closes its border at every size, and the block still yields rather than
// overflow.
func TestUnevenPanelsStayInsideAShortTerminal(t *testing.T) {
	for _, width := range []int{100, 60} {
		for _, h := range []int{40, 24, 20, 16, 12, 10, 8, 6} {
			u, _ := evidenceModel(t).Update(tea.WindowSizeMsg{Width: width, Height: h})
			v := asModel(t, u).View()
			if rows := lipgloss.Height(v); rows > h {
				t.Errorf("%dx%d: view is %d display rows tall:\n%s", width, h, rows, v)
			}
			if n := unclosedPanels(v); n != 0 {
				t.Errorf("%dx%d: %d panel border(s) left unclosed:\n%s", width, h, n, v)
			}
		}
	}
}
