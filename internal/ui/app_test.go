// Init across toolbox and normal modes.

package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Toolbox Init emits one tick, then sleeps until the deferred chain is started.
func TestInit(t *testing.T) {
	if newModel(nil, false).Init() == nil {
		t.Error("normal Init must return a cmd")
	}
	tb := newModel(nil, true)
	cmd := tb.Init()
	if cmd == nil {
		t.Error("toolbox Init must still return the spinner tick")
	}
	_, next := tb.Update(cmd())
	if next != nil {
		t.Error("idle toolbox spinner tick must not rearm")
	}
	tb.doRestart()
	if !tb.spinnerActive() {
		t.Error("toolbox spinner must activate when the deferred chain starts")
	}
}

// Two jobs that never send their terminal event must share one exit grace, not
// serialize it. Regression: a time.After channel fires once, so the first stuck
// job consumed it and the second blocked forever.
func TestCleanupStuckJobsShareOneGrace(t *testing.T) {
	m := newModel(nil, false)
	stuck := func(id string) *job { return &job{id: id, ch: make(chan tea.Msg)} }
	m.cur.active = stuck("a")
	m.otherJobs = []jobState{{active: stuck("b")}, {active: stuck("c")}}

	done := make(chan struct{})
	go func() { defer close(done); Cleanup(m) }()
	select {
	case <-done:
	case <-time.After(2 * exitGrace):
		t.Fatal("Cleanup outlived one exit grace with multiple stuck jobs")
	}
}
