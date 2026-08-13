// The keymap's structural invariants — the ones a rebinding user relies on
// without knowing they exist: one action per key per screen, no binding buried
// under a chord that starts with it, and nothing shadowing a tool hotkey.

package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// Every shipped preset has to survive the same validation a user's config
// does; a preset that couldn't be written by hand would be a keymap the
// validator calls impossible.
func TestPresetsValidate(t *testing.T) {
	for name, preset := range presets {
		if errs := validateBindings(preset); len(errs) > 0 {
			t.Errorf("preset %q: %v", name, errs)
		}
	}
}

// The cheatsheet and the help bar both read descriptions out of actionDefs, so
// an action missing from that table is an action nobody can discover.
func TestEveryActionIsDescribed(t *testing.T) {
	seen := map[keyAction]bool{}
	for _, def := range actionDefs {
		if seen[def.act] {
			t.Errorf("action %q listed twice", def.name)
		}
		if len(def.desc) == 0 {
			t.Errorf("action %q describes no context", def.name)
		}
		seen[def.act] = true
	}
	for act := range defaultPreset {
		if !seen[act] {
			t.Errorf("action %d is bound by the default preset but has no actionDef", act)
		}
	}
	for act := actNone + 1; act <= actQuit; act++ {
		if !seen[act] {
			t.Errorf("action %d has no actionDef", act)
		}
	}
}

// A chord is resolved key by key, and the key that ends a dead chord still
// gets its own turn — otherwise a stray prefix would eat the keystroke after
// it, which reads as the app dropping input.
func TestChordResolution(t *testing.T) {
	km, errs := buildKeymap("default", map[string][]string{"top": {"g g"}})
	if len(errs) > 0 {
		t.Fatalf("chord binding: %v", errs)
	}
	m := newModel(nil, false)
	m.keys = km

	act, pending := m.resolveKey(ctxViewer, "g")
	if act != actNone || !slices.Equal(pending, []string{"g"}) {
		t.Fatalf("first g = (%d, %v), want (none, [g])", act, pending)
	}
	m.pendingKeys = pending
	if act, pending := m.resolveKey(ctxViewer, "g"); act != actTop || pending != nil {
		t.Fatalf("gg = (%d, %v), want (top, nil)", act, pending)
	}
	// g then an unrelated key: the chord dies, the key still runs.
	if act, pending := m.resolveKey(ctxViewer, "/"); act != actFilter || pending != nil {
		t.Fatalf("g then / = (%d, %v), want (filter, nil)", act, pending)
	}
	// top exists in both screens, so its chord is live in both.
	m.pendingKeys = nil
	act, pending = m.resolveKey(ctxList, "g")
	if act != actNone || !slices.Equal(pending, []string{"g"}) {
		t.Fatalf("first g in the list = (%d, %v), want (none, [g])", act, pending)
	}
	m.pendingKeys = pending
	if act, pending := m.resolveKey(ctxList, "g"); act != actTop || pending != nil {
		t.Fatalf("gg in the list = (%d, %v), want (top, nil)", act, pending)
	}
}

// A chord in flight launches nothing, and the key that ends an unfinished one
// behaves exactly as it would have if the prefix had never been typed — an
// abandoned chord costs the user nothing, in either direction.
func TestChordHoldsTheToolboxThenReleasesIt(t *testing.T) {
	km, errs := buildKeymap("default", map[string][]string{"help": {"g g"}})
	if len(errs) > 0 {
		t.Fatalf("chord binding: %v", errs)
	}
	m := newModel(mustTarget(t, "example.com"), true)
	m.keys = km
	u, _ := m.handleKey(keyMsg("g"))
	m = u.(model)
	if !slices.Equal(m.pendingKeys, []string{"g"}) {
		t.Fatalf("pendingKeys = %v, want [g]", m.pendingKeys)
	}
	if m.confirmTool != nil || m.cur.name != "" {
		t.Fatalf("g started something: confirm=%v job=%q", m.confirmTool, m.cur.name)
	}
	// "n" is nmap's letter. It does not complete the chord, so the chord ends
	// and n gets the turn it would have had on its own — the confirm gate,
	// never a launch.
	u, _ = m.handleKey(keyMsg("n"))
	m = u.(model)
	if m.pendingKeys != nil {
		t.Fatalf("pendingKeys = %v after the chord died, want nil", m.pendingKeys)
	}
	if m.confirmTool == nil {
		t.Fatal("n did not reach the port-scan gate after the chord ended")
	}
	// The completed chord runs its action instead, and never the tool.
	m.confirmTool, m.pendingKeys = nil, []string{"g"}
	u, _ = m.handleKey(keyMsg("g"))
	if !u.(model).helping {
		t.Fatal("gg did not open the cheatsheet")
	}
}

// The help bar and cheatsheet must show the keys that actually run, not the
// ones the defaults used to use.
func TestHelpFollowsRebinding(t *testing.T) {
	km, errs := buildKeymap("default", map[string][]string{"quit": {"Q"}, "restart": {"R"}})
	if len(errs) > 0 {
		t.Fatalf("rebind: %v", errs)
	}
	m := newModel(mustTarget(t, "example.com"), false)
	m.keys = km
	help := m.helpView(false)
	if !strings.Contains(help, "Q") || !strings.Contains(help, "R") {
		t.Errorf("help bar = %q, want the rebound keys", help)
	}
	m.helping = true
	if overlay := m.View(); !strings.Contains(overlay, "Q") || !strings.Contains(overlay, "R") {
		t.Errorf("cheatsheet = %q, want the rebound keys", overlay)
	}
	// And the rebound key has to work.
	u, _ := m.handleKey(keyMsg("R"))
	if !u.(model).entering {
		t.Error("R did not open the restart prompt")
	}
}

// An unbound action is not advertised: a help bar offering a key that does
// nothing is worse than a shorter help bar.
func TestUnboundActionsVanishFromHelp(t *testing.T) {
	km, errs := buildKeymap("default", map[string][]string{"network-map": {}})
	if len(errs) > 0 {
		t.Fatalf("unbind: %v", errs)
	}
	m := newModel(nil, false)
	m.keys = km
	if help := m.helpView(false); strings.Contains(help, "network map") {
		t.Errorf("help bar = %q, want no network map chip", help)
	}
	if km.bound(actNetworkMap) {
		t.Error("network-map still reports as bound")
	}
}

// allToolKeys is hand-written, so it needs a guard that walks the real
// toolbox: a new drill-down tool must not become bindable by accident.
func TestAllToolKeysMatchesTheToolbox(t *testing.T) {
	targets := []*diagnostic.Target{
		nil,
		mustTarget(t, "example.com"),
		mustTarget(t, "example.com:22"),
		mustTarget(t, "example.com:25"),
		mustTarget(t, "example.com:80"),
		mustTarget(t, "1.1.1.1:443"),
	}
	found := map[string]bool{}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, tgt := range targets {
			for _, tool := range toolsFor(tgt, goos, toolBind{}) {
				found[tool.Key] = true
			}
		}
	}
	for _, key := range allToolKeys() {
		if !found[key] {
			t.Errorf("allToolKeys lists %q, which no tool uses", key)
		}
		delete(found, key)
	}
	for key := range found {
		t.Errorf("tool key %q is missing from allToolKeys, so a binding could shadow it", key)
	}
}
