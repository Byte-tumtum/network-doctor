// The config file: what a good one does, and what every bad one must do
// instead — report itself, change nothing, and leave a working keymap behind.

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
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
	// half-page has no default binding and is the vim preset's alone, so it is
	// the honest test of which preset won.
	if km.bound(actHalfPageDown) {
		t.Errorf("half-page-down = %v, want unbound under the default preset", km.keysFor(actHalfPageDown))
	}
	// Both presets bind the same top/bottom keys; only their order differs, so
	// the advertised one says which preset is in force.
	if got := km.label(actBottom); got != "end/G" {
		t.Errorf("bottom = %q, want the default order end/G", got)
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

// The whole path, as main walks it: a file on disk, through the option, to a
// keypress the model answers.
func TestConfiguredKeymapReachesTheModel(t *testing.T) {
	path := writeConfig(t, "keys: vim\nbindings:\n  restart: [ctrl+r]\n")
	km, errs := LoadKeymap(path, "")
	if len(errs) > 0 {
		t.Fatalf("LoadKeymap: %v", errs)
	}
	m := asModel(t, NewWithSelection(nil, nil, true, false, "", "test",
		diagnostic.DefaultPublicDNS, diagnostic.ProbeSelection{}, WithKeymap(km)))
	u, _ := m.handleKey(keyPress("ctrl+r"))
	if !asModel(t, u).entering {
		t.Error("ctrl+r did not open the restart prompt")
	}
	if u, _ := m.handleKey(keyPress("r")); asModel(t, u).entering {
		t.Error("r still opens the restart prompt after being rebound away")
	}
	if help := m.helpView(true); !strings.Contains(help, "ctrl+r") {
		t.Errorf("help bar = %q, want the configured key", help)
	}
}

// A complaint from before the TUI existed has to survive into it: the model
// opens with it on screen and with the tick that will clear it armed.
func TestStartupNoticeIsShownAndExpires(t *testing.T) {
	m := asModel(t, NewWithSelection(nil, nil, true, false, "", "test",
		diagnostic.DefaultPublicDNS, diagnostic.ProbeSelection{},
		WithStartupNotice("netdocrc: unknown action \"quti\"\x1b]52;c;aGk=\x07")))
	if m.notice == "" || m.noticeOK {
		t.Fatalf("notice = %q ok = %v, want a complaint", m.notice, m.noticeOK)
	}
	if strings.ContainsAny(m.notice, "\x1b\x07") {
		t.Errorf("notice = %q, want the escapes sanitized out", m.notice)
	}
	if !strings.Contains(m.View(), "quti") {
		t.Errorf("the notice is not on the opening screen:\n%s", m.View())
	}
	if m.noticeDeadline.IsZero() {
		t.Fatal("no deadline, so nothing will ever clear the notice")
	}
	// Init arms the expiry, since Init's own mutations are discarded.
	if m.Init() == nil {
		t.Fatal("Init returned no commands")
	}
	cleared := asModel(t, mustUpdated(m.Update(noticeDoneMsg{deadline: m.noticeDeadline})))
	if cleared.notice != "" {
		t.Errorf("notice = %q after its deadline, want it cleared", cleared.notice)
	}
}

func mustUpdated(m tea.Model, _ tea.Cmd) tea.Model { return m }

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
