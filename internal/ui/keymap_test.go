// The keymap's structural invariants — the ones a rebinding user relies on
// without knowing they exist: one action per key per screen, no binding buried
// under a chord that starts with it, and nothing shadowing a tool hotkey.

package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// keyPress builds the message a real terminal would send for a binding's key
// name, so a test that says ctrl+d exercises the same path ctrl+d takes.
func keyPress(name string) tea.KeyMsg {
	for _, kt := range bindableKeyTypes {
		if (tea.Key{Type: kt}).String() == name {
			return tea.KeyMsg{Type: kt}
		}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

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
	km, errs := buildKeymap("default", map[string][]string{"help": {"z z"}})
	if len(errs) > 0 {
		t.Fatalf("chord binding: %v", errs)
	}
	m := newModel(mustTarget(t, "example.com"), true)
	m.keys = km
	u, _ := m.handleKey(keyMsg("z"))
	m = u.(model)
	if !slices.Equal(m.pendingKeys, []string{"z"}) {
		t.Fatalf("pendingKeys = %v, want [z]", m.pendingKeys)
	}
	if m.confirmTool != nil || m.cur.name != "" {
		t.Fatalf("z started something: confirm=%v job=%q", m.confirmTool, m.cur.name)
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
	m.confirmTool, m.pendingKeys = nil, []string{"z"}
	u, _ = m.handleKey(keyMsg("z"))
	if !u.(model).helping {
		t.Fatal("zz did not open the cheatsheet")
	}
}

// The vim preset's whole point is the motions, so each one is pinned against
// the screen it moves — and against the default keys it must not have taken
// away on its way in.
func TestVimPresetMotions(t *testing.T) {
	km, err := PresetKeymap("vim")
	if err != nil {
		t.Fatalf("PresetKeymap(vim): %v", err)
	}
	m := newModel(mustTarget(t, "example.com"), false)
	m.keys = km
	tests := []struct {
		keys []string
		ctx  keyContext
		want keyAction
	}{
		{[]string{"g", "g"}, ctxViewer, actTop},
		{[]string{"g", "g"}, ctxList, actTop},
		{[]string{"G"}, ctxViewer, actBottom},
		{[]string{"G"}, ctxList, actBottom},
		{[]string{"ctrl+d"}, ctxViewer, actHalfPageDown},
		{[]string{"ctrl+u"}, ctxViewer, actHalfPageUp},
		{[]string{"ctrl+f"}, ctxViewer, actPageDown},
		{[]string{"ctrl+b"}, ctxViewer, actPageUp},
		{[]string{"j"}, ctxViewer, actDown},
		{[]string{"k"}, ctxList, actUp},
		// Kept, not replaced: the preset adds motions, it doesn't evict the
		// keys a non-vim user in the same terminal already knows.
		{[]string{"home"}, ctxList, actTop},
		{[]string{"end"}, ctxViewer, actBottom},
		{[]string{"pgdown"}, ctxViewer, actPageDown},
		{[]string{"down"}, ctxList, actDown},
		{[]string{"q"}, ctxList, actQuit},
		{[]string{"/"}, ctxViewer, actFilter},
	}
	for _, tt := range tests {
		m.pendingKeys = nil
		var act keyAction
		for _, key := range tt.keys {
			act, m.pendingKeys = m.resolveKey(tt.ctx, key)
		}
		if act != tt.want {
			t.Errorf("%v in context %d = action %d, want %d", tt.keys, tt.ctx, act, tt.want)
		}
	}
	// The half-page motions have no default binding, so the viewer footer only
	// offers them once a preset supplies one.
	m.viewing, m.pendingKeys = true, nil
	if footer := m.viewerFooter(); !strings.Contains(footer, "half page") {
		t.Errorf("viewer footer = %q, want a half page chip", footer)
	}
	if footer := newModel(nil, false).viewerFooter(); strings.Contains(footer, "half page") {
		t.Errorf("default viewer footer = %q, want no half page chip", footer)
	}
}

// gg has to scroll the viewport, not just resolve: the chord path and the
// scrolling path are separate pieces of machinery.
func TestVimChordScrollsTheViewer(t *testing.T) {
	km, err := PresetKeymap("vim")
	if err != nil {
		t.Fatalf("PresetKeymap(vim): %v", err)
	}
	m := newModel(nil, false)
	m.keys, m.width, m.height = km, 80, 24
	m.cur.name, m.cur.status = "ping the host", JobDone
	for i := range 200 {
		m.appendJobLine(fmt.Sprintf("line %d", i))
	}
	u, _ := m.handleKey(keyPress("enter"))
	m = asModel(t, u)
	if !m.viewing || !m.follow {
		t.Fatalf("enter did not open the viewer at the tail (viewing=%v follow=%v)", m.viewing, m.follow)
	}
	view := func(m model, key string) model {
		t.Helper()
		u, _ := m.handleViewKey(keyPress(key))
		return asModel(t, u)
	}
	m = view(m, "g")
	if m.vp.YOffset == 0 {
		t.Fatal("the first g scrolled on its own, before the chord finished")
	}
	if m = view(m, "g"); m.vp.YOffset != 0 || m.follow {
		t.Errorf("gg left offset %d follow %v, want the top and no follow", m.vp.YOffset, m.follow)
	}
	if m = view(m, "ctrl+d"); m.vp.YOffset == 0 {
		t.Error("ctrl+d did not scroll down half a page")
	}
	if m = view(m, "G"); m.vp.YOffset == 0 || !m.follow {
		t.Errorf("G left offset %d follow %v, want the bottom and follow back on", m.vp.YOffset, m.follow)
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

// A rebound key can be much longer than the one it replaced, and the
// cheatsheet's key column has to grow with it instead of running the label
// into its description.
func TestCheatsheetKeyColumnFitsLongLabels(t *testing.T) {
	km, errs := buildKeymap("default", map[string][]string{"help": {"ctrl+x", "f12", "?"}})
	if len(errs) > 0 {
		t.Fatalf("rebind: %v", errs)
	}
	m := newModel(nil, false)
	m.keys, m.width, m.height = km, 100, 40
	m.helping = true
	want := km.label(actHelp)
	for _, line := range strings.Split(m.View(), "\n") {
		if !strings.Contains(line, want) {
			continue
		}
		if !strings.Contains(line, want+"  ") {
			t.Errorf("cheatsheet row %q runs the key label into its description", line)
		}
		return
	}
	t.Errorf("no cheatsheet row for %q:\n%s", want, m.View())
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
