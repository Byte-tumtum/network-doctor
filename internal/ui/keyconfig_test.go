// The config file: what a good one does, and what every bad one must do
// instead — report itself, change nothing, and leave a working keymap behind.

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netdocrc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadKeymapAppliesPresetAndOverrides(t *testing.T) {
	path := writeConfig(t, `
keys: vim
bindings:
  quit: [Q, ctrl+q]
  help: [f1]
  restart: []
`)
	km, errs := LoadKeymap(path, "")
	if len(errs) > 0 {
		t.Fatalf("LoadKeymap: %v", errs)
	}
	m := newModel(nil, false)
	m.keys = km
	tests := []struct {
		key  string
		want keyAction
	}{
		{"Q", actQuit},
		{"ctrl+q", actQuit},
		{"f1", actHelp},
		{"G", actBottom}, // the vim preset underneath
		{"q", actNone},   // the key quit moved off
		{"?", actNone},   // and the key help moved off
		{"r", actNone},   // restart is unbound outright
	}
	for _, tt := range tests {
		if act, _ := m.resolveKey(ctxList, tt.key); act != tt.want {
			t.Errorf("%q = action %d, want %d", tt.key, act, tt.want)
		}
	}
	if km.bound(actRestart) {
		t.Error("an empty binding list left restart bound")
	}
}

// The flag is the more specific instruction and the way out of a file that
// picks a preset the user is done with.
func TestLoadKeymapFlagOverridesFilePreset(t *testing.T) {
	path := writeConfig(t, "keys: vim\n")
	km, errs := LoadKeymap(path, "default")
	if len(errs) > 0 {
		t.Fatalf("LoadKeymap: %v", errs)
	}
	if slices := km.keysFor(actBottom); len(slices) != 1 || slices[0] != "end" {
		t.Errorf("bottom = %v, want the default end only", slices)
	}
	// The file's own bindings still apply on top of the flag's preset.
	path = writeConfig(t, "keys: vim\nbindings:\n  quit: [Q]\n")
	km, errs = LoadKeymap(path, "default")
	if len(errs) > 0 {
		t.Fatalf("LoadKeymap: %v", errs)
	}
	if got := km.label(actQuit); got != "Q" {
		t.Errorf("quit = %q, want Q", got)
	}
}

func TestLoadKeymapMissingFileIsNotAnError(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "absent")} {
		km, errs := LoadKeymap(path, "")
		if len(errs) > 0 {
			t.Errorf("LoadKeymap(%q) = %v, want no errors", path, errs)
		}
		if got := km.label(actQuit); got != "q" {
			t.Errorf("LoadKeymap(%q) quit = %q, want the default q", path, got)
		}
	}
}

// Every rejection has to leave a keymap that works, and leave it whole: a
// half-applied config is a puzzle nothing on screen explains.
func TestLoadKeymapRejectionsChangeNothing(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"unknown action", "bindings:\n  quti: [Q]\n", `unknown action "quti"`},
		{"unknown key", "bindings:\n  quit: [suprr]\n", "unknown key"},
		{"chord written without spaces", "bindings:\n  top: [gg]\n", "a chord is written with spaces"},
		{"named key miscapitalized", "bindings:\n  help: [F1]\n", "named keys are lower case"},
		{"shadows a tool hotkey", "bindings:\n  quit: [n]\n", "toolbox uses"},
		{"two actions on one key", "bindings:\n  help: [r]\n", "bound to both"},
		{"binding buried under a chord", "bindings:\n  help: [g]\n  top: [g g]\n", "start of"},
		{"unknown field", "keybindings:\n  quit: [Q]\n", "field keybindings"},
		{"not yaml at all", "quit = Q\n", "cannot unmarshal"},
		{"unknown preset", "keys: emacs\n", "unknown key preset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km, errs := LoadKeymap(writeConfig(t, tt.body), "")
			if len(errs) == 0 {
				t.Fatalf("%s was accepted", tt.name)
			}
			if !strings.Contains(errs[0].Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", errs[0], tt.want)
			}
			// Whatever was wrong, the surviving keymap is the built-in one.
			if got := km.label(actQuit); got != "q" {
				t.Errorf("quit = %q, want the built-in q", got)
			}
			if got := km.label(actHelp); got != "?" {
				t.Errorf("help = %q, want the built-in ?", got)
			}
		})
	}
}

// Several mistakes are reported together: fixing a keymap one run per typo is
// the kind of loop that makes people give up and delete the file.
func TestLoadKeymapReportsEveryError(t *testing.T) {
	_, errs := LoadKeymap(writeConfig(t, "bindings:\n  quti: [Q]\n  help: [nope]\n  quit: [n]\n"), "")
	if len(errs) != 3 {
		t.Fatalf("got %d errors, want 3: %v", len(errs), errs)
	}
}

// The file is external input, so what it says can reach the screen only after
// the sanitizer has seen it.
func TestLoadKeymapErrorsAreSanitized(t *testing.T) {
	_, errs := LoadKeymap(writeConfig(t, "bindings:\n  \"\\e]52;c;aGk=\\a\": [Q]\n"), "")
	if len(errs) == 0 {
		t.Fatal("an escape-laden action name was accepted")
	}
	for _, err := range errs {
		for _, bad := range []string{"\x1b", "\a"} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("error %q still carries %q", err, bad)
			}
		}
	}
}

func TestLoadKeymapRejectsAnOversizedFile(t *testing.T) {
	_, errs := LoadKeymap(writeConfig(t, strings.Repeat("# padding\n", maxConfigBytes/10+1)), "")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "larger than") {
		t.Fatalf("errs = %v, want one size complaint", errs)
	}
}

// An empty file is a file someone is about to write, not a mistake.
func TestLoadKeymapAcceptsAnEmptyFile(t *testing.T) {
	for _, body := range []string{"", "\n", "# just a comment\n"} {
		km, errs := LoadKeymap(writeConfig(t, body), "")
		if len(errs) > 0 {
			t.Errorf("LoadKeymap(%q) = %v, want no errors", body, errs)
		}
		if got := km.label(actQuit); got != "q" {
			t.Errorf("LoadKeymap(%q) quit = %q, want q", body, got)
		}
	}
}
