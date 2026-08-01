package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func TestSSHCommand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	key := filepath.Join(home, ".ssh", "id_ed25519")

	tests := []struct {
		name                          string
		host, login, password, keyArg string
		want                          []string
	}{
		{name: "host only", host: "example.com", want: []string{"example.com"}},
		{name: "login", host: "example.com", login: "alice", want: []string{"alice@example.com"}},
		{
			name: "port", host: "example.com:2222", login: "alice",
			want: []string{"-p", "2222", "alice@example.com"},
		},
		{
			name: "port 22 is the default", host: "example.com:22", login: "alice",
			want: []string{"alice@example.com"},
		},
		{
			name: "chosen key wins over the agent", host: "example.com", keyArg: key,
			want: []string{"-i", key, "-o", "IdentitiesOnly=yes", "example.com"},
		},
		{
			name: "password bounds the prompts", host: "example.com", password: "hunter2",
			want: []string{"-o", "NumberOfPasswordPrompts=1", "example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, env, err := sshCommand(tt.host, tt.login, tt.keyArg, tt.password, "/usr/bin/netdoc")
			if err != nil {
				t.Fatalf("sshCommand: %v", err)
			}
			if !slices.Equal(args, tt.want) {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
			if tt.password == "" {
				if env != nil {
					t.Error("no password, yet the environment was overridden")
				}
				return
			}
			if !slices.Contains(env, AskpassEnv+"="+tt.password) ||
				!slices.Contains(env, "SSH_ASKPASS=/usr/bin/netdoc") ||
				!slices.Contains(env, "SSH_ASKPASS_REQUIRE=force") {
				t.Errorf("askpass environment incomplete: %v", env)
			}
			// The secret must never reach the command line.
			if slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, tt.password) }) {
				t.Errorf("password leaked into argv: %v", args)
			}
		})
	}
}

// Without a helper path there is nothing to feed the password to, so ssh must
// be left to ask on the terminal rather than told the prompt count.
func TestSSHCommandWithoutHelper(t *testing.T) {
	args, env, err := sshCommand("example.com", "alice", "", "hunter2", "")
	if err != nil {
		t.Fatalf("sshCommand: %v", err)
	}
	if !slices.Equal(args, []string{"alice@example.com"}) {
		t.Errorf("args = %v, want [alice@example.com]", args)
	}
	if env != nil {
		t.Errorf("env = %v, want nil", env)
	}
}

func TestSSHCommandNeedsHost(t *testing.T) {
	if _, _, err := sshCommand("", "alice", "", "", "/usr/bin/netdoc"); err == nil {
		t.Error("empty host = nil error, want one")
	}
}

// The form's host string is written by sshHostValue and read back by
// sshCommand, so the round trip has to land on the target the run was about.
// An IPv6 literal is the case that punishes a plain host+":"+port: the result
// parses as a different, perfectly valid address, and ssh would connect there
// without anyone noticing.
func TestSSHHostValueRoundTrip(t *testing.T) {
	tests := []struct {
		target string
		want   []string
	}{
		{"example.com:2222", []string{"-p", "2222", "example.com"}},
		{"192.0.2.1:2222", []string{"-p", "2222", "192.0.2.1"}},
		{"[2001:db8::1]:2222", []string{"-p", "2222", "2001:db8::1"}},
		{"[2001:db8::1]:22", []string{"2001:db8::1"}},
		{"2001:db8::1", []string{"2001:db8::1"}},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			target, err := diagnostic.ParseTarget(tt.target)
			if err != nil {
				t.Fatalf("ParseTarget: %v", err)
			}
			args, _, err := sshCommand(sshHostValue(target), "", "", "", "")
			if err != nil {
				t.Fatalf("sshCommand: %v", err)
			}
			if !slices.Equal(args, tt.want) {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
		})
	}
}

// S is hinted in the help bar and the cheatsheet only once the banner probe
// has found an SSH server, but the binding itself stays live either way.
func TestSSHHintFollowsTheBannerProbe(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(string) (string, error) { return "ssh", nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	m := newModel(mustTarget(t, "example.com:22"), false)
	m = asModel(t, must(m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})))
	doneResults(&m, diagnostic.ProbeSSH) // the SSH banner probe is the one that fails

	if strings.Contains(m.View(), "ssh login") {
		t.Error("the help bar hints S with no SSH server on the target")
	}
	if strings.Contains(asModel(t, must(m.Update(keyMsg("?")))).View(), "hands the terminal to ssh") {
		t.Error("the cheatsheet lists S with no SSH server on the target")
	}
	// Hidden, not disabled: the form still opens.
	if !asModel(t, must(m.Update(keyMsg("S")))).sshPrompt {
		t.Error("S must still open the form when the banner probe failed")
	}

	doneResults(&m, "")
	if !strings.Contains(m.View(), "ssh login") {
		t.Error("the help bar drops S even though the banner probe passed")
	}
	if !strings.Contains(asModel(t, must(m.Update(keyMsg("?")))).View(), "hands the terminal to ssh") {
		t.Error("the cheatsheet drops S even though the banner probe passed")
	}
}

// The form takes its host from the target, tab moves the focus, ←/→ picks a
// key, and esc closes it.
func TestSSHFormKeys(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(string) (string, error) { return "ssh", nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	target, err := diagnostic.ParseTarget("example.com:2222")
	if err != nil {
		t.Fatalf("ParseTarget: %v", err)
	}
	m := asModel(t, must(newModel(target, true).Update(keyMsg("S"))))
	if !m.sshPrompt {
		t.Fatal("S did not open the SSH form")
	}
	if got, want := m.ssh.host, "example.com:2222"; got != want {
		t.Errorf("host = %q, want %q", got, want)
	}
	if m.ssh.focus != sshUser {
		t.Errorf("focus = %d, want the username field (%d)", m.ssh.focus, sshUser)
	}
	if !strings.Contains(m.View(), "SSH login to example.com:2222") {
		t.Error("the SSH panel is not on screen")
	}

	// Typing lands in the focused field only.
	m = asModel(t, must(m.Update(keyMsg("x"))))
	if got := m.ssh.user.Value(); !strings.HasSuffix(got, "x") {
		t.Errorf("username = %q, want the typed rune appended", got)
	}
	m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyTab})))
	if m.ssh.focus != sshKey {
		t.Errorf("after tab, focus = %d, want the key row (%d)", m.ssh.focus, sshKey)
	}
	m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyTab})))
	m = asModel(t, must(m.Update(keyMsg("y"))))
	if got := m.ssh.pass.Value(); got != "y" {
		t.Errorf("password = %q, want %q", got, "y")
	}

	m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyEsc})))
	if m.sshPrompt {
		t.Error("esc left the form open")
	}
}

// Once the password has been handed to the askpass environment, the form drops
// its copy rather than holding it until the next S.
func TestSSHFormClearsPasswordOnConnect(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(string) (string, error) { return "ssh", nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	target, err := diagnostic.ParseTarget("example.com:2222")
	if err != nil {
		t.Fatalf("ParseTarget: %v", err)
	}
	m := asModel(t, must(newModel(target, true).Update(keyMsg("S"))))
	m.ssh.pass.SetValue("hunter2")
	m = asModel(t, must(m.Update(tea.KeyMsg{Type: tea.KeyEnter})))
	if m.sshPrompt {
		t.Fatal("enter left the form open")
	}
	if got := m.ssh.pass.Value(); got != "" {
		t.Errorf("password = %q, want it cleared after connecting", got)
	}
}

// Without a target there is no host to log in to, so S declines rather than
// opening a form that cannot be completed.
func TestSSHFormNeedsTarget(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(string) (string, error) { return "ssh", nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	m := asModel(t, must(newModel(nil, true).Update(keyMsg("S"))))
	if m.sshPrompt {
		t.Error("S opened the form with no target")
	}
	if !strings.Contains(m.notice, "needs a target") {
		t.Errorf("notice = %q, want the missing-target hint", m.notice)
	}
}

// ←/→ walks the discovered keys and wraps, and "none" stays reachable.
func TestSSHKeyChooser(t *testing.T) {
	f := newSSHForm(mustTarget(t, "example.com"))
	f.keys = []string{"", "/home/a/.ssh/id_ed25519", "/home/a/.ssh/id_rsa"}
	f.setFocus(sshKey)
	if f.keyPath() != "" {
		t.Errorf("initial key = %q, want none", f.keyPath())
	}
	f.cycleKey(1)
	if got, want := f.keyLabel(), "id_ed25519"; got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
	f.cycleKey(-1)
	f.cycleKey(-1)
	if got, want := f.keyPath(), "/home/a/.ssh/id_rsa"; got != want {
		t.Errorf("wrapped to %q, want %q", got, want)
	}
}

// A password is echoed as dots, never as itself.
func TestSSHPasswordMasked(t *testing.T) {
	f := newSSHForm(mustTarget(t, "example.com"))
	f.setFocus(sshPass)
	f.pass.SetValue("hunter2")
	if got := f.pass.View(); strings.Contains(got, "hunter2") {
		t.Errorf("password rendered in the clear: %q", got)
	}
}

// A failed session's stderr comes back into a job pane instead of vanishing
// with the screen ssh was using.
func TestSSHFailureLandsInJobPane(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), true)
	u, _ := m.Update(sshDoneMsg{
		err:     errors.New("exit status 255"),
		display: "ssh alice@example.com",
		output:  "alice@example.com: Permission denied (publickey,password).\n",
	})
	nm := asModel(t, u)
	if nm.cur.status != JobFailed {
		t.Errorf("status = %v, want failed", nm.cur.status)
	}
	joined := strings.Join(nm.cur.lines, "\n")
	if !strings.Contains(joined, "Permission denied") || !strings.Contains(joined, "exit status 255") {
		t.Errorf("job pane lines = %q, want ssh's stderr and the exit error", joined)
	}
	if !strings.Contains(nm.notice, "ssh failed") {
		t.Errorf("notice = %q, want the failure hint", nm.notice)
	}
}

// A hostile server's banner reaches the pane as inert text.
func TestSSHOutputSanitized(t *testing.T) {
	m := newModel(mustTarget(t, "example.com"), true)
	u, _ := m.Update(sshDoneMsg{display: "ssh example.com", output: "\x1b]52;c;aGk=\x07banner\n"})
	for _, line := range asModel(t, u).cur.lines {
		if strings.ContainsRune(line, '\x1b') {
			t.Errorf("escape survived into the pane: %q", line)
		}
	}
}

func TestCapWriter(t *testing.T) {
	var w capWriter
	big := strings.Repeat("x", capBytes+10)
	if n, err := w.Write([]byte(big)); n != len(big) || err != nil {
		t.Fatalf("Write = %d, %v, want %d, nil", n, err, len(big))
	}
	if _, err := w.Write([]byte("tail")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := w.String()
	if !strings.HasPrefix(got, strings.Repeat("x", capBytes)) {
		t.Error("the first capBytes must be kept verbatim")
	}
	if !strings.Contains(got, "14 more bytes discarded") {
		t.Errorf("String() = %q…, want the discarded count", got[max(len(got)-40, 0):])
	}
}

func must(m tea.Model, _ tea.Cmd) tea.Model { return m }
