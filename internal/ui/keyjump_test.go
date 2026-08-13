// Jumping to the first and last row, in the check list and on the network map.

package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pressed(t *testing.T, m model, msg tea.KeyMsg) model {
	t.Helper()
	u, _ := m.handleKey(msg)
	return asModel(t, u)
}

var (
	homeKey = tea.KeyMsg{Type: tea.KeyHome}
	endKey  = tea.KeyMsg{Type: tea.KeyEnd}
)

func TestListJumpsToFirstAndLastCheck(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), false)
	if len(m.probes) < 2 {
		t.Fatalf("need at least two probes, got %d", len(m.probes))
	}
	end := pressed(t, m, endKey)
	if end.selected != len(m.probes)-1 {
		t.Errorf("end selected row %d, want %d", end.selected, len(m.probes)-1)
	}
	if !end.selMoved {
		t.Error("end did not count as the user moving the cursor, so completion may yank it away")
	}
	if home := pressed(t, end, homeKey); home.selected != 0 {
		t.Errorf("home selected row %d, want 0", home.selected)
	}
}

// The jump can arrive before any probe has reported, when the list is still
// empty; selecting row -1 would be an out-of-range render.
func TestListJumpSurvivesAnEmptyList(t *testing.T) {
	m := newModel(nil, false)
	m.probes = nil
	if got := pressed(t, m, endKey).selected; got != 0 {
		t.Errorf("end on an empty list selected %d, want 0", got)
	}
}

// On the network map the same keys move the device cursor, as up/down do.
func TestMapJumpsToFirstAndLastDevice(t *testing.T) {
	m := newModel(nil, false)
	m.networkMap = true
	m.cur.name, m.cur.status = lanDiscoveryName, JobDone
	m.cur.lines = []string{
		"Host: 192.168.12.1 (router.lan.example)\tStatus: Up",
		"Host: 192.168.12.50 (living-room-tv.lan.example)\tStatus: Up",
		"Host: 192.168.12.51 ()\tStatus: Up",
	}
	hosts := len(m.networkHosts())
	if hosts != 3 {
		t.Fatalf("networkHosts() = %d, want 3", hosts)
	}
	end := pressed(t, m, endKey)
	if end.mapSelected != hosts-1 {
		t.Errorf("end selected device %d, want %d", end.mapSelected, hosts-1)
	}
	if end.selected != 0 {
		t.Errorf("the check cursor moved to %d while the map was open", end.selected)
	}
	if home := pressed(t, end, homeKey); home.mapSelected != 0 {
		t.Errorf("home selected device %d, want 0", home.mapSelected)
	}
}
