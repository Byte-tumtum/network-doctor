// Key bindings as data: one table drives what a key does, what the help bar
// advertises, and what the ? cheatsheet lists, so the three cannot drift.

package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// keyAction is one rebindable thing the user can ask for. The zero value is
// "no action", which is what an unbound key resolves to.
type keyAction int

const (
	actNone keyAction = iota
	actUp
	actDown
	actTop
	actBottom
	actPageUp
	actPageDown
	actHalfPageUp
	actHalfPageDown
	actOpen
	actBack
	actClearFilter
	actCancelJob
	actSwitchJob
	actCopy
	actSave
	actFilter
	actRestart
	actSSH
	actNetworkMap
	actHelp
	actQuit
)

// keyContext is the screen a binding applies to. The text-entry screens (the
// restart prompt, the SSH form, the filter line) and the confirm gate are
// absent: every printable key there belongs to the input or to a safety
// question, not to a shortcut.
type keyContext int

const (
	ctxList keyContext = iota
	ctxViewer
	numContexts
)

// actionDef is an action's config name and its cheatsheet line per context.
// A context missing from desc is one the action does not exist in.
type actionDef struct {
	act  keyAction
	name string
	desc map[keyContext]string
}

// actionDefs is in cheatsheet order, and the cheatsheet reads top to bottom as
// movement, then output, then session.
var actionDefs = []actionDef{
	{actUp, "up", map[keyContext]string{
		ctxList:   "previous check — or device on the network map",
		ctxViewer: "scroll up",
	}},
	{actDown, "down", map[keyContext]string{
		ctxList:   "next check — or device on the network map",
		ctxViewer: "scroll down",
	}},
	{actTop, "top", map[keyContext]string{
		ctxList:   "first check — or device on the network map",
		ctxViewer: "jump to top",
	}},
	{actBottom, "bottom", map[keyContext]string{
		ctxList:   "last check — or device on the network map",
		ctxViewer: "jump to bottom (re-enables follow)",
	}},
	// Paging is viewer-only: the check list is rendered whole, so top/bottom
	// already reach everything a page key could.
	{actPageUp, "page-up", map[keyContext]string{ctxViewer: "page up"}},
	{actPageDown, "page-down", map[keyContext]string{ctxViewer: "page down"}},
	{actHalfPageUp, "half-page-up", map[keyContext]string{ctxViewer: "half page up"}},
	{actHalfPageDown, "half-page-down", map[keyContext]string{ctxViewer: "half page down"}},
	{actOpen, "open", map[keyContext]string{
		ctxList: "full output — or set target on the network map",
	}},
	{actFilter, "filter", map[keyContext]string{ctxViewer: "filter lines"}},
	{actCopy, "copy", map[keyContext]string{
		ctxList:   "copy selected portal URL, otherwise report",
		ctxViewer: "copy output (filtered if a filter is on)",
	}},
	{actSave, "save", map[keyContext]string{
		ctxList:   "save report",
		ctxViewer: "save output (filtered if a filter is on)",
	}},
	{actSwitchJob, "switch-job", map[keyContext]string{
		ctxList:   "switch job",
		ctxViewer: "switch job",
	}},
	{actCancelJob, "cancel-job", map[keyContext]string{ctxList: "cancel the focused job"}},
	{actNetworkMap, "network-map", map[keyContext]string{ctxList: "toggle network map"}},
	{actRestart, "restart", map[keyContext]string{ctxList: "restart with a new target"}},
	{actSSH, "ssh", map[keyContext]string{ctxList: "ssh to a host — hands the terminal to ssh"}},
	// Two actions, so a filtered viewer keeps its two-step exit: the key that
	// clears a filter is not the key that leaves with one applied.
	{actClearFilter, "clear-filter", map[keyContext]string{ctxViewer: "clear the filter, or back when none is set"}},
	{actBack, "back", map[keyContext]string{ctxViewer: "back"}},
	// Also in the viewer: a tool's full output is where the question comes up.
	{actHelp, "help", map[keyContext]string{
		ctxList:   "full-screen key cheatsheet",
		ctxViewer: "full-screen key cheatsheet",
	}},
	{actQuit, "quit", map[keyContext]string{ctxList: "quit"}},
}

// defaultPreset is the historical keymap plus the jump keys that had no
// binding before. gg and G are here rather than in the vim preset because they
// cost a non-vim user nothing: both keys did nothing at all before. home and
// end are listed first, so the help bar advertises the familiar spelling.
var defaultPreset = map[keyAction][]string{
	actUp:           {"up", "k"},
	actDown:         {"down", "j"},
	actTop:          {"home", "g g"},
	actBottom:       {"end", "G"},
	actPageUp:       {"pgup"},
	actPageDown:     {"pgdown"},
	actHalfPageUp:   nil,
	actHalfPageDown: nil,
	actOpen:         {"enter"},
	actBack:         {"q"},
	actClearFilter:  {"esc"},
	actCancelJob:    {"esc"},
	actSwitchJob:    {"tab"},
	actCopy:         {"y"},
	actSave:         {"w"},
	actFilter:       {"/"},
	actRestart:      {"r"},
	actSSH:          {"S"},
	actNetworkMap:   {"v"},
	actHelp:         {"?"},
	actQuit:         {"q"},
}

// vimOverlay is what `keys: vim` adds: the ctrl-motions, the only ones that
// would cost a non-vim user a key. Nothing is taken away — pgup/pgdown keep
// working. top and bottom are relisted only to put vim's spelling first, which
// is what makes the help bar read gg/G instead of home/end.
var vimOverlay = map[keyAction][]string{
	actTop:          {"g g", "home"},
	actBottom:       {"G", "end"},
	actPageUp:       {"ctrl+b", "pgup"},
	actPageDown:     {"ctrl+f", "pgdown"},
	actHalfPageUp:   {"ctrl+u"},
	actHalfPageDown: {"ctrl+d"},
}

// presets are the names accepted by -keys and by `keys:` in the config file.
var presets = map[string]map[keyAction][]string{
	"default": defaultPreset,
	"vim":     mergePreset(defaultPreset, vimOverlay),
}

func mergePreset(base, overlay map[keyAction][]string) map[keyAction][]string {
	out := maps.Clone(base)
	maps.Copy(out, overlay)
	return out
}

// PresetKeymap returns a named preset's keymap, for -keys.
func PresetKeymap(name string) (Keymap, error) {
	km, errs := buildKeymap(name, nil)
	if len(errs) > 0 {
		return km, errs[0]
	}
	return km, nil
}

// presetNames lists the presets for error messages, in a stable order.
func presetNames() []string { return slices.Sorted(maps.Keys(presets)) }

// bindableKeyTypes are the keys a binding may name. Bubble Tea supplies the
// spellings, so a binding can never be written in one no keypress will match
// ("pgdn" for "pgdown"); printable single runes are accepted on top of them.
// Absent on purpose: ctrl+c (the terminal's own way out), ctrl+h/i/j/m (the
// same bytes as backspace/tab/enter), and ctrl+z (job control).
var bindableKeyTypes = []tea.KeyType{
	tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
	tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyShiftLeft, tea.KeyShiftRight,
	tea.KeyCtrlUp, tea.KeyCtrlDown, tea.KeyCtrlLeft, tea.KeyCtrlRight,
	tea.KeyEnter, tea.KeyEsc, tea.KeyTab, tea.KeyShiftTab, tea.KeySpace,
	tea.KeyBackspace, tea.KeyDelete, tea.KeyInsert,
	tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
	tea.KeyCtrlHome, tea.KeyCtrlEnd, tea.KeyCtrlPgUp, tea.KeyCtrlPgDown,
	tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlD, tea.KeyCtrlE,
	tea.KeyCtrlF, tea.KeyCtrlG, tea.KeyCtrlK, tea.KeyCtrlL,
	tea.KeyCtrlN, tea.KeyCtrlO, tea.KeyCtrlP, tea.KeyCtrlQ,
	tea.KeyCtrlR, tea.KeyCtrlS, tea.KeyCtrlT, tea.KeyCtrlU,
	tea.KeyCtrlV, tea.KeyCtrlW, tea.KeyCtrlX, tea.KeyCtrlY,
	tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6,
	tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12,
}

var keyNames = func() map[string]bool {
	names := make(map[string]bool, len(bindableKeyTypes))
	for _, kt := range bindableKeyTypes {
		names[tea.Key{Type: kt}.String()] = true
	}
	return names
}()

// spaceKey is Bubble Tea's name for the space bar. Chords are space-separated,
// so bindings spell it "space" and arriving keys are translated to match.
const spaceKey = " "

func normalizeKey(key string) string {
	if key == spaceKey {
		return "space"
	}
	return key
}

// parseKeySeq turns one config binding into its canonical form, e.g.
// "g  g" → "g g". A chord's keys are separated by spaces; a key that is not a
// single printable rune must be one of the names above.
func parseKeySeq(seq string) (string, error) {
	keys := strings.Fields(seq)
	if len(keys) == 0 {
		return "", fmt.Errorf("empty key")
	}
	for _, key := range keys {
		if key == "space" || keyNames[key] {
			continue
		}
		if r := []rune(key); len(r) == 1 && unicode.IsPrint(r[0]) {
			continue
		}
		// Case matters (G and g differ), so a name that is only miscapitalized is
		// worth saying out loud rather than reporting as unknown.
		hint := ""
		switch {
		case keyNames[strings.ToLower(key)]:
			hint = fmt.Sprintf(" (named keys are lower case: %q)", strings.ToLower(key))
		case len([]rune(key)) > 1:
			hint = ` (a chord is written with spaces, e.g. "g g")`
		}
		return "", fmt.Errorf("unknown key %q%s", textsafe.Clean(key), hint)
	}
	return strings.Join(keys, " "), nil
}

// validateBindings reports everything wrong with a binding set, all at once —
// a config with three mistakes should take one edit to fix, not three runs.
func validateBindings(bindings map[keyAction][]string) []error {
	var errs []error
	toolKeys := allToolKeys()
	// owner is indexed by context, so the same key may mean one thing in the
	// check list and another in the viewer, as esc always has.
	owner := make([]map[string]string, numContexts)
	for ctx := range numContexts {
		owner[ctx] = map[string]string{}
	}
	for _, def := range actionDefs {
		for _, seq := range bindings[def.act] {
			for ctx := range def.desc {
				if held := owner[ctx][seq]; held != "" {
					errs = append(errs, fmt.Errorf("%q is bound to both %s and %s", displaySeq(seq), held, def.name))
					continue
				}
				owner[ctx][seq] = def.name
			}
			if slices.Contains(toolKeys, strings.Fields(seq)[0]) && def.desc[ctxList] != "" {
				errs = append(errs, fmt.Errorf("%s is bound to %q, which the toolbox uses to run a tool", def.name, displaySeq(seq)))
			}
		}
	}
	// A binding buried under a chord that starts with it would never fire:
	// the first key would always wait for the second.
	for ctx := range numContexts {
		for seq, name := range owner[ctx] {
			for other, otherName := range owner[ctx] {
				if seq != other && strings.HasPrefix(other, seq+" ") {
					errs = append(errs, fmt.Errorf("%s is bound to %q, which is the start of %s's %q", name, displaySeq(seq), otherName, displaySeq(other)))
				}
			}
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	// One mistake, one sentence: an action on both screens is checked on both.
	return slices.CompactFunc(errs, func(a, b error) bool { return a.Error() == b.Error() })
}

// buildKeymap resolves a preset name and the user's per-action overrides into
// a keymap. An override replaces that action's keys outright rather than
// adding to them; an empty list unbinds it. Errors never yield a half-applied
// keymap — a partly honored config is a puzzle nothing on screen explains — so
// anything wrong returns the plain preset and reports every reason at once.
func buildKeymap(preset string, overrides map[string][]string) (Keymap, []error) {
	base, ok := presets[preset]
	if !ok {
		return defaultKeymap, []error{fmt.Errorf("unknown key preset %q: pick one of %s",
			textsafe.Clean(preset), strings.Join(presetNames(), ", "))}
	}
	byName := make(map[string]keyAction, len(actionDefs))
	for _, def := range actionDefs {
		byName[def.name] = def.act
	}
	bindings, errs := maps.Clone(base), []error(nil)
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		act, ok := byName[name]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown action %q", textsafe.Clean(name)))
			continue
		}
		parsed := make([]string, 0, len(overrides[name]))
		for _, seq := range overrides[name] {
			canonical, err := parseKeySeq(seq)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				continue
			}
			parsed = append(parsed, canonical)
		}
		bindings[act] = parsed
	}
	errs = append(errs, validateBindings(bindings)...)
	if len(errs) > 0 {
		return newKeymap(base), errs
	}
	return newKeymap(bindings), nil
}

// Keymap resolves keys to actions. The zero value is the default keymap, so
// every model that never asked for one — tests included — behaves exactly as
// it did before bindings became configurable.
type Keymap struct {
	// keys is action → bound sequences, in the order they should be
	// advertised. A sequence is its keys joined by spaces ("g g").
	keys map[keyAction][]string
	// byKey and prefix are the dispatch indexes, one entry per context.
	byKey  []map[string]keyAction
	prefix []map[string]bool
}

// newKeymap indexes a validated binding set for dispatch. An unvalidated one
// resolves ties by map order, which is what validate exists to prevent.
func newKeymap(bindings map[keyAction][]string) Keymap {
	km := Keymap{
		keys:   maps.Clone(bindings),
		byKey:  make([]map[string]keyAction, numContexts),
		prefix: make([]map[string]bool, numContexts),
	}
	for ctx := range numContexts {
		km.byKey[ctx] = map[string]keyAction{}
		km.prefix[ctx] = map[string]bool{}
	}
	for _, def := range actionDefs {
		for ctx := range def.desc {
			for _, seq := range bindings[def.act] {
				km.byKey[ctx][seq] = def.act
				keys := strings.Split(seq, " ")
				for i := 1; i < len(keys); i++ {
					km.prefix[ctx][strings.Join(keys[:i], " ")] = true
				}
			}
		}
	}
	return km
}

var defaultKeymap = newKeymap(defaultPreset)

// resolved substitutes the default keymap for the zero value.
func (k Keymap) resolved() Keymap {
	if k.keys == nil {
		return defaultKeymap
	}
	return k
}

// lookup resolves a complete key sequence in ctx.
func (k Keymap) lookup(ctx keyContext, seq []string) (keyAction, bool) {
	act, ok := k.resolved().byKey[ctx][strings.Join(seq, " ")]
	return act, ok
}

// isPrefix reports whether seq is the unfinished start of a longer binding —
// the "g" of "g g", which must wait for the next key rather than fall through
// to the tool hotkeys.
func (k Keymap) isPrefix(ctx keyContext, seq []string) bool {
	return k.resolved().prefix[ctx][strings.Join(seq, " ")]
}

// keysFor returns an action's bound sequences.
func (k Keymap) keysFor(act keyAction) []string { return k.resolved().keys[act] }

// bound reports whether an action can be reached at all, so the help bar can
// stop advertising one the user unbound.
func (k Keymap) bound(act keyAction) bool { return len(k.keysFor(act)) > 0 }

// label renders every key bound to an action for the help bar and cheatsheet,
// e.g. "↑/k" or "gg/home". Empty when the action is unbound.
func (k Keymap) label(act keyAction) string {
	seqs := k.keysFor(act)
	out := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, displaySeq(seq))
	}
	return strings.Join(out, "/")
}

// pairLabel renders two opposed actions as one chip ("↑/↓") from each one's
// first binding, for the width-constrained help bar. The cheatsheet uses label.
func (k Keymap) pairLabel(a, b keyAction) string {
	first := func(act keyAction) string {
		if seqs := k.keysFor(act); len(seqs) > 0 {
			return displaySeq(seqs[0])
		}
		return ""
	}
	x, y := first(a), first(b)
	switch {
	case x == "" && y == "":
		return ""
	case x == "":
		return y
	case y == "":
		return x
	}
	return x + "/" + y
}

// displaySeq renders a binding for the screen: arrows as glyphs, and a chord
// without the space that only separates its keys in the config file.
func displaySeq(seq string) string {
	var b strings.Builder
	for _, key := range strings.Split(seq, " ") {
		switch key {
		case "up":
			b.WriteString("↑")
		case "down":
			b.WriteString("↓")
		case "left":
			b.WriteString("←")
		case "right":
			b.WriteString("→")
		default:
			b.WriteString(key)
		}
	}
	return b.String()
}
