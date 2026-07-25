// Init across toolbox and normal modes.

package ui

import (
	"testing"
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
