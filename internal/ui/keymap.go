// Key bindings as data: one table drives dispatch, the help bar, and the
// cheatsheet so the three cannot drift.

package ui

import (
	"fmt"
	"maps"
	"strings"
)

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
	actExpand
	actHelp
	actQuit
)

type keyContext int

const (
	ctxList keyContext = iota
	ctxViewer
	numContexts
)

type actionDef struct {
	act  keyAction
	name string
	help map[keyContext]actionHelp
}

type actionHelp struct {
	bar     string
	details string
}

// actionDefs is the shared dispatch context, help text, and cheatsheet order.
var actionDefs = []actionDef{
	{actUp, "up", map[keyContext]actionHelp{
		ctxList:   {"select", "previous check, or device/service on the network map"},
		ctxViewer: {"scroll", "scroll up"},
	}},
	{actDown, "down", map[keyContext]actionHelp{
		ctxList:   {"select", "next check, or device/service on the network map"},
		ctxViewer: {"scroll", "scroll down"},
	}},
	{actTop, "top", map[keyContext]actionHelp{
		ctxList:   {"first/last", "first check, or device/service on the network map"},
		ctxViewer: {"top/bottom", "jump to top"},
	}},
	{actBottom, "bottom", map[keyContext]actionHelp{
		ctxList:   {"first/last", "last check, or device/service on the network map"},
		ctxViewer: {"top/bottom", "jump to bottom (re-enables follow)"},
	}},
	{actPageUp, "page-up", map[keyContext]actionHelp{ctxViewer: {"page", "page up"}}},
	{actPageDown, "page-down", map[keyContext]actionHelp{ctxViewer: {"page", "page down"}}},
	{actHalfPageUp, "half-page-up", map[keyContext]actionHelp{ctxViewer: {"half page", "half page up"}}},
	{actHalfPageDown, "half-page-down", map[keyContext]actionHelp{ctxViewer: {"half page", "half page down"}}},
	{actOpen, "open", map[keyContext]actionHelp{ctxList: {"open", "full output; on the network map, open a device then diagnose one of its services"}}},
	{actFilter, "filter", map[keyContext]actionHelp{ctxViewer: {"filter", "filter lines"}}},
	{actCopy, "copy", map[keyContext]actionHelp{
		ctxList:   {"copy", "copy selected portal URL, otherwise report"},
		ctxViewer: {"copy output", "copy output (filtered if a filter is on)"},
	}},
	{actSave, "save", map[keyContext]actionHelp{
		ctxList:   {"save report", "save report"},
		ctxViewer: {"save output", "save output (filtered if a filter is on)"},
	}},
	{actSwitchJob, "switch-job", map[keyContext]actionHelp{
		ctxList:   {"switch job", "switch job"},
		ctxViewer: {"switch job", "switch job"},
	}},
	{actCancelJob, "cancel-job", map[keyContext]actionHelp{ctxList: {"cancel job", "cancel the focused job, or leave an opened device on the network map"}}},
	{actNetworkMap, "network-map", map[keyContext]actionHelp{ctxList: {"network map", "find a device on the local network, and back to the checks"}}},
	{actExpand, "expand", map[keyContext]actionHelp{ctxList: {"expand", "show the collapsed passing checks and the whole toolbox"}}},
	{actRestart, "restart", map[keyContext]actionHelp{ctxList: {"restart", "restart with a new target"}}},
	{actSSH, "ssh", map[keyContext]actionHelp{ctxList: {"ssh login", "log in to a host, handing the terminal to ssh"}}},
	{actClearFilter, "clear-filter", map[keyContext]actionHelp{ctxViewer: {"clear filter", "clear the filter, or back when none is set"}}},
	{actBack, "back", map[keyContext]actionHelp{ctxViewer: {"back", "back"}}},
	{actHelp, "help", map[keyContext]actionHelp{ctxList: {"help", "full-screen key cheatsheet"}}},
	{actQuit, "quit", map[keyContext]actionHelp{ctxList: {"quit", "quit"}}},
}

func actionHelpFor(ctx keyContext, act keyAction) (actionHelp, bool) {
	for _, def := range actionDefs {
		help, ok := def.help[ctx]
		if def.act == act {
			return help, ok
		}
	}
	return actionHelp{}, false
}

type actionBindings map[keyAction][]string
type keyPreset [numContexts]actionBindings

// defaultPreset preserves the existing TUI key bindings as the default.
var defaultPreset = keyPreset{
	ctxList: {
		actUp:         {"up", "k"},
		actDown:       {"down", "j"},
		actOpen:       {"enter"},
		actCancelJob:  {"esc"},
		actSwitchJob:  {"tab"},
		actCopy:       {"y"},
		actSave:       {"w"},
		actRestart:    {"r"},
		actSSH:        {"S"},
		actNetworkMap: {"v"},
		actExpand:     {"a"},
		actHelp:       {"?"},
		actQuit:       {"q"},
	},
	ctxViewer: {
		actUp:          {"up", "k"},
		actDown:        {"down", "j"},
		actTop:         {"home"},
		actBottom:      {"end"},
		actPageUp:      {"pgup"},
		actPageDown:    {"pgdown"},
		actBack:        {"q"},
		actClearFilter: {"esc"},
		actSwitchJob:   {"tab"},
		actCopy:        {"y"},
		actSave:        {"w"},
		actFilter:      {"/"},
	},
}

var vimPreset = func() keyPreset {
	p := clonePreset(defaultPreset)
	p[ctxList][actTop] = []string{"g g"}
	p[ctxList][actBottom] = []string{"G"}
	p[ctxViewer][actTop] = []string{"g g", "home"}
	p[ctxViewer][actBottom] = []string{"G", "end"}
	p[ctxViewer][actPageUp] = []string{"ctrl+b", "pgup"}
	p[ctxViewer][actPageDown] = []string{"ctrl+f", "pgdown"}
	p[ctxViewer][actHalfPageUp] = []string{"ctrl+u"}
	p[ctxViewer][actHalfPageDown] = []string{"ctrl+d"}
	return p
}()

var presets = []struct {
	name   string
	preset keyPreset
}{
	{"default", defaultPreset},
	{"vim", vimPreset},
}

// KeyPresets lists the built-in presets in user-facing order.
func KeyPresets() []string {
	names := make([]string, len(presets))
	for i, preset := range presets {
		names[i] = preset.name
	}
	return names
}

func clonePreset(p keyPreset) keyPreset {
	var out keyPreset
	for ctx, bindings := range p {
		out[ctx] = maps.Clone(bindings)
	}
	return out
}

// PresetKeymap resolves one built-in preset. Empty selects the default.
func PresetKeymap(name string) (Keymap, error) {
	if name == "" {
		name = presets[0].name
	}
	for _, preset := range presets {
		if preset.name == name {
			return newKeymap(preset.preset), nil
		}
	}
	return Keymap{}, fmt.Errorf("unknown key preset %q (have: %s)", name, strings.Join(KeyPresets(), ", "))
}

// Keymap resolves keys to actions. Its zero value is the default keymap.
type Keymap struct {
	keys   keyPreset
	byKey  [numContexts]map[string]keyAction
	prefix [numContexts]map[string]bool
}

func newKeymap(bindings keyPreset) Keymap {
	km := Keymap{keys: clonePreset(bindings)}
	for ctx := range numContexts {
		km.byKey[ctx] = map[string]keyAction{}
		km.prefix[ctx] = map[string]bool{}
	}
	for _, def := range actionDefs {
		for ctx := range def.help {
			for _, seq := range bindings[ctx][def.act] {
				km.byKey[ctx][seq] = def.act
				parts := strings.Fields(seq)
				for i := 1; i < len(parts); i++ {
					km.prefix[ctx][strings.Join(parts[:i], " ")] = true
				}
			}
		}
	}
	return km
}

var defaultKeymap = newKeymap(defaultPreset)

func (k Keymap) resolved() Keymap {
	if k.keys[ctxList] == nil {
		return defaultKeymap
	}
	return k
}

func (k Keymap) lookup(ctx keyContext, seq []string) (keyAction, bool) {
	act, ok := k.resolved().byKey[ctx][strings.Join(seq, " ")]
	return act, ok
}

func (k Keymap) isPrefix(ctx keyContext, seq []string) bool {
	return k.resolved().prefix[ctx][strings.Join(seq, " ")]
}

func (k Keymap) keysFor(ctx keyContext, act keyAction) []string {
	return k.resolved().keys[ctx][act]
}

func (k Keymap) bound(ctx keyContext, act keyAction) bool {
	return len(k.keysFor(ctx, act)) > 0
}

func (k Keymap) label(ctx keyContext, act keyAction) string {
	seqs := k.keysFor(ctx, act)
	out := make([]string, len(seqs))
	for i, seq := range seqs {
		out[i] = displaySeq(seq)
	}
	return strings.Join(out, "/")
}

func (k Keymap) pairLabel(ctx keyContext, a, b keyAction) string {
	first := func(act keyAction) string {
		if seqs := k.keysFor(ctx, act); len(seqs) > 0 {
			return displaySeq(seqs[0])
		}
		return ""
	}
	x, y := first(a), first(b)
	switch {
	case x == "":
		return y
	case y == "":
		return x
	default:
		return x + "/" + y
	}
}

func displaySeq(seq string) string {
	var b strings.Builder
	for _, key := range strings.Fields(seq) {
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
