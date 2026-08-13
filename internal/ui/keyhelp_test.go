// The cheatsheet as a screen of its own: reachable from the output viewer,
// scrollable, and still closed by anything that is not a motion.

package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// viewerWith opens the output viewer on a finished job holding n lines — the
// shape a route table or open-sockets dump arrives in.
func viewerWith(t *testing.T, name string, n int) model {
	t.Helper()
	m := newModel(nil, false)
	m.width, m.height = 92, 20
	m.cur.name, m.cur.display, m.cur.status = name, name, JobDone
	for i := range n {
		m.appendJobLine(fmt.Sprintf("%s line %d", name, i))
	}
	u, _ := m.handleKey(keyPress("enter"))
	m = asModel(t, u)
	if !m.viewing {
		t.Fatal("enter did not open the output viewer")
	}
	return m
}

// Both jump keys have to work on tool output with no config file in sight.
func TestJumpKeysScrollToolOutputByDefault(t *testing.T) {
	for _, tool := range []string{"route table", "open sockets"} {
		t.Run(tool, func(t *testing.T) {
			m := viewerWith(t, tool, 300)
			if m.vp.YOffset == 0 {
				t.Fatal("the viewer did not open at the tail")
			}
			press := func(m model, key string) model {
				t.Helper()
				u, _ := m.handleViewKey(keyPress(key))
				return asModel(t, u)
			}
			// gg is a chord: the first g must move nothing.
			mid := press(m, "g")
			if mid.vp.YOffset != m.vp.YOffset {
				t.Errorf("the first g scrolled to %d on its own", mid.vp.YOffset)
			}
			top := press(mid, "g")
			if top.vp.YOffset != 0 || top.follow {
				t.Errorf("gg left offset %d follow %v, want the top", top.vp.YOffset, top.follow)
			}
			bottom := press(top, "G")
			if bottom.vp.YOffset == 0 || !bottom.follow {
				t.Errorf("G left offset %d follow %v, want the bottom", bottom.vp.YOffset, bottom.follow)
			}
		})
	}
}

// The same two keys on the check list and the network map.
func TestJumpKeysMoveTheListAndMapByDefault(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), false)
	if len(m.probes) < 2 {
		t.Fatalf("need at least two probes, got %d", len(m.probes))
	}
	last := pressed(t, m, keyPress("G"))
	if last.selected != len(m.probes)-1 {
		t.Errorf("G selected row %d, want %d", last.selected, len(m.probes)-1)
	}
	mid := pressed(t, last, keyPress("g"))
	if mid.selected != last.selected {
		t.Errorf("the first g moved the cursor to %d", mid.selected)
	}
	if first := pressed(t, mid, keyPress("g")); first.selected != 0 {
		t.Errorf("gg selected row %d, want 0", first.selected)
	}

	nm := newModel(nil, false)
	nm.networkMap = true
	nm.cur.name, nm.cur.status = lanDiscoveryName, JobDone
	nm.cur.lines = []string{
		"Host: 192.168.12.1 (router.lan.example)\tStatus: Up",
		"Host: 192.168.12.50 (living-room-tv.lan.example)\tStatus: Up",
		"Host: 192.168.12.51 ()\tStatus: Up",
	}
	end := pressed(t, nm, keyPress("G"))
	if end.mapSelected != len(nm.networkHosts())-1 {
		t.Errorf("G selected device %d, want the last", end.mapSelected)
	}
	back := pressed(t, pressed(t, end, keyPress("g")), keyPress("g"))
	if back.mapSelected != 0 {
		t.Errorf("gg selected device %d, want 0", back.mapSelected)
	}
}

// The viewer used to be the one screen that could not answer "what does this
// key do", which is exactly where the question comes up.
func TestHelpOpensFromTheOutputViewer(t *testing.T) {
	m := viewerWith(t, "route table", 40)
	u, _ := m.Update(keyPress("?"))
	hm := asModel(t, u)
	if !hm.helping {
		t.Fatal("? did not open the cheatsheet from the viewer")
	}
	if !strings.Contains(hm.View(), "Output viewer") {
		t.Error("the cheatsheet is missing its viewer section")
	}
	// The viewer is still open underneath, so closing returns to the output
	// rather than dropping the user back on the check list.
	if !hm.viewing {
		t.Fatal("opening the cheatsheet closed the viewer")
	}
	closed := asModel(t, mustModel(hm.Update(keyPress("x"))))
	if closed.helping || !closed.viewing {
		t.Errorf("after closing: helping=%v viewing=%v, want false/true", closed.helping, closed.viewing)
	}
	if !strings.Contains(closed.View(), "route table line 39") {
		t.Error("the output did not come back")
	}
	// ? toggles: it is not a motion, so it closes like anything else.
	if again := asModel(t, mustModel(hm.Update(keyPress("?")))); again.helping {
		t.Error("? did not close the cheatsheet")
	}
}

func mustModel(m tea.Model, _ tea.Cmd) tea.Model { return m }

// The reported path: run the tool, then ask for help — on the pane it lands in
// and in the full-screen view of it.
func TestHelpAnswersAfterRunningATool(t *testing.T) {
	oldLookPath := toolLookPath
	// Missing on purpose: launchTool then reports "not found" in a pane
	// instead of forking anything, which is the same pane a real run fills.
	toolLookPath = func(string) (string, error) { return "", fmt.Errorf("not found") }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	for _, tool := range []struct{ key, name string }{{"i", "route table"}, {"s", "open sockets"}} {
		t.Run(tool.name, func(t *testing.T) {
			m := newModel(nil, true)
			m.width, m.height = 92, 30
			ran := asModel(t, mustModel(m.Update(keyPress(tool.key))))
			if !ran.hasJob() || !strings.Contains(ran.cur.name, tool.name) {
				t.Fatalf("%q did not open the %s pane (job %q)", tool.key, tool.name, ran.cur.name)
			}
			// On the main screen, with the tool's pane on it.
			onPane := asModel(t, mustModel(ran.Update(keyPress("?"))))
			if !onPane.helping {
				t.Error("? did not open the cheatsheet with the tool pane showing")
			}
			// And in the full-screen view of the same output.
			viewing := asModel(t, mustModel(ran.Update(keyPress("enter"))))
			if !viewing.viewing {
				t.Fatal("enter did not open the full output")
			}
			inViewer := asModel(t, mustModel(viewing.Update(keyPress("?"))))
			if !inViewer.helping {
				t.Error("? did not open the cheatsheet from the full output")
			}
			if !inViewer.viewing {
				t.Error("the output was closed instead of being covered")
			}
		})
	}
}

// The sheet is longer than a short terminal, and used to be cut off with no
// way to reach the rest.
func TestCheatsheetScrollsInsteadOfBeingCutOff(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), false)
	m.width, m.height = 92, 16
	body := m.helpBody()
	if len(body) <= m.helpVisible() {
		t.Fatalf("cheatsheet is %d rows and %d fit; this test needs it to overflow", len(body), m.helpVisible())
	}
	// A row from the viewer section, the half that used to be cut off. Not the
	// last row: that one is ?, which the list section carries too.
	var lastRow string
	for _, line := range body {
		if strings.Contains(line, "clear the filter") {
			lastRow = line
		}
	}
	if lastRow == "" {
		t.Fatal("no clear-filter row in the cheatsheet to look for")
	}

	open := asModel(t, mustModel(m.Update(keyPress("?"))))
	if strings.Contains(open.View(), lastRow) {
		t.Fatal("the whole sheet fits after all; the test proves nothing")
	}
	if got := strings.Count(open.View(), "\n") + 1; got > m.height {
		t.Errorf("the sheet renders %d rows into a %d-row terminal", got, m.height)
	}

	bottom := asModel(t, mustModel(open.Update(keyPress("G"))))
	if !bottom.helping {
		t.Fatal("G closed the sheet instead of scrolling it")
	}
	if !strings.Contains(bottom.View(), lastRow) {
		t.Errorf("G did not reach the last row:\n%s", bottom.View())
	}
	// A half-typed chord is not "any other key".
	half := asModel(t, mustModel(bottom.Update(keyPress("g"))))
	if !half.helping {
		t.Fatal("the first g of gg closed the sheet")
	}
	top := asModel(t, mustModel(half.Update(keyPress("g"))))
	if !top.helping || top.helpOffset != 0 {
		t.Errorf("gg left helping=%v offset=%d, want open at the top", top.helping, top.helpOffset)
	}
	if !strings.Contains(top.View(), body[0]) {
		t.Error("gg did not bring the first row back")
	}
	// Line and page motions keep it open too; everything else still closes it.
	for _, key := range []string{"down", "j", "up", "k", "pgdown", "pgup", "home", "end"} {
		if next := asModel(t, mustModel(top.Update(keyPress(key)))); !next.helping {
			t.Errorf("%q closed the sheet instead of scrolling it", key)
		}
	}
	for _, key := range []string{"x", "r", "esc", "q", "?"} {
		if next := asModel(t, mustModel(top.Update(keyPress(key)))); next.helping {
			t.Errorf("%q did not close the sheet", key)
		}
	}
	// Reopening starts at the top rather than wherever it was left.
	reopened := asModel(t, mustModel(asModel(t, mustModel(bottom.Update(keyPress("x")))).Update(keyPress("?"))))
	if reopened.helpOffset != 0 {
		t.Errorf("reopened at offset %d, want the top", reopened.helpOffset)
	}
}

// Keys typed fast arrive as one KeyMsg. Splitting them has to happen before
// any screen sees them, or a chord reads as one unknown key — here, "close".
func TestFastChordIsNotOneKey(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), false)
	m.width, m.height = 92, 16
	open := asModel(t, mustModel(m.Update(keyPress("?"))))
	bottom := asModel(t, mustModel(open.Update(keyPress("G"))))
	if bottom.helpOffset == 0 {
		t.Fatal("G did not scroll")
	}
	fast := asModel(t, mustModel(bottom.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gg")})))
	if !fast.helping {
		t.Fatal("a fast gg closed the cheatsheet")
	}
	if fast.helpOffset != 0 {
		t.Errorf("a fast gg left offset %d, want the top", fast.helpOffset)
	}
}
