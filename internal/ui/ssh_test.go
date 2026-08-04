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
		{name: "login", host: "example.com", login: "alice", want: []string{"-l", "alice", "example.com"}},
		{
			name: "port", host: "example.com:2222", login: "alice",
			want: []string{"-p", "2222", "-l", "alice", "example.com"},
		},
		{
			name: "port 22 is the default", host: "example.com:22", login: "alice",
			want: []string{"-l", "alice", "example.com"},
		},
		{
			// A login is free text: as an operand it would be read as an option,
			// as -l's value it cannot be.
			name: "login starting with a dash stays a login", host: "example.com", login: "-oProxyCommand=touch /tmp/pwned",
			want: []string{"-l", "-oProxyCommand=touch /tmp/pwned", "example.com"},
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
	stubSSHEffective(t, "example.com", false)
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
				!slices.Contains(env, AskpassHostEnv+"=example.com") ||
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
	if !slices.Equal(args, []string{"-l", "alice", "example.com"}) {
		t.Errorf("args = %v, want [-l alice example.com]", args)
	}
	if env != nil {
		t.Errorf("env = %v, want nil", env)
	}
}

// stubSSHEffective stands in for the `ssh -G` lookup, keeping the suite off
// the real ssh binary and off whatever the developer's own ~/.ssh/config says
// about example.com.
func stubSSHEffective(t *testing.T, host string, proxied bool) {
	t.Helper()
	old := sshEffective
	sshEffective = func([]string) (string, bool, error) { return host, proxied, nil }
	t.Cleanup(func() { sshEffective = old })
}

// stubSSHEffectiveErr stands in for an `ssh -G` that couldn't answer: no ssh on
// PATH, a config it refused to parse, a Match exec that exited nonzero.
func stubSSHEffectiveErr(t *testing.T, err error) {
	t.Helper()
	old := sshEffective
	sshEffective = func([]string) (string, bool, error) { return "", false, err }
	t.Cleanup(func() { sshEffective = old })
}

// ssh substitutes a Host alias's HostName before it asks for a password, so
// the helper has to be told the name it will actually see — an alias would
// never match its own prompt, and the legitimate password would be refused.
func TestSSHCommandResolvesTheAlias(t *testing.T) {
	stubSSHEffective(t, "real.example.com", false)
	_, env, err := sshCommand("alias", "alice", "", "hunter2", "/usr/bin/netdoc")
	if err != nil {
		t.Fatalf("sshCommand: %v", err)
	}
	if !slices.Contains(env, AskpassHostEnv+"=real.example.com") {
		t.Errorf("env = %v, want the resolved HostName", env)
	}
	if slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, AskpassProxyEnv+"=") }) {
		t.Error("a direct connection was marked as proxied")
	}
}

// A jump host runs an ssh of its own that inherits the helper, so the helper
// has to know it cannot attribute a host-less prompt to the target.
func TestSSHCommandMarksProxiedConnections(t *testing.T) {
	stubSSHEffective(t, "example.com", true)
	_, env, err := sshCommand("example.com", "alice", "", "hunter2", "/usr/bin/netdoc")
	if err != nil {
		t.Fatalf("sshCommand: %v", err)
	}
	if !slices.Contains(env, AskpassProxyEnv+"=1") {
		t.Errorf("env = %v, want the proxy marker", env)
	}
}

// An unreadable config leaves the helper unable to tell the target's prompt
// from a jump host's, and both defaults are wrong: direct spends the password
// on whoever asks, proxied silently refuses the login it was opened for. Refuse
// the login and say why, so the retry with a blank field is the user's choice.
func TestSSHCommandRefusesWhenConfigIsUnreadable(t *testing.T) {
	stubSSHEffectiveErr(t, errors.New("exit status 255"))
	args, env, err := sshCommand("example.com", "alice", "", "hunter2", "/usr/bin/netdoc")
	if err == nil {
		t.Fatal("sshCommand succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "password field blank") {
		t.Errorf("err = %q, want it to name the way out", err)
	}
	// Nothing may be handed back on the refusal path — an argv that reached ssh
	// without the askpass env would prompt on a terminal the TUI still owns,
	// and an env would carry the secret past the check that just failed.
	if args != nil || env != nil {
		t.Errorf("args = %v, env = %v, want both nil", args, env)
	}
	for _, e := range env {
		if strings.Contains(e, "hunter2") {
			t.Fatal("the password survived a refused login")
		}
	}
}

// Without a password there is no secret to misroute, so the lookup is not
// consulted at all and a broken config costs the user nothing.
func TestSSHCommandWithoutPasswordIgnoresConfigFailure(t *testing.T) {
	called := false
	old := sshEffective
	sshEffective = func([]string) (string, bool, error) {
		called = true
		return "", false, errors.New("exit status 255")
	}
	t.Cleanup(func() { sshEffective = old })

	args, env, err := sshCommand("example.com", "alice", "", "", "/usr/bin/netdoc")
	if err != nil {
		t.Fatalf("sshCommand: %v", err)
	}
	if called {
		t.Error("the config lookup ran for a password-less login")
	}
	if !slices.Equal(args, []string{"-l", "alice", "example.com"}) {
		t.Errorf("args = %v, want [-l alice example.com]", args)
	}
	if env != nil {
		t.Errorf("env = %v, want nil", env)
	}
}

// The lookup is asked with the argv the session will use, since a Match block
// can key off the port or the login and answer with a different HostName.
func TestSSHCommandQueriesWithTheRealArgv(t *testing.T) {
	var got []string
	old := sshEffective
	sshEffective = func(args []string) (string, bool, error) { got = args; return "example.com", false, nil }
	t.Cleanup(func() { sshEffective = old })

	if _, _, err := sshCommand("example.com:2222", "alice", "", "hunter2", "/usr/bin/netdoc"); err != nil {
		t.Fatalf("sshCommand: %v", err)
	}
	want := []string{"-p", "2222", "-l", "alice", "example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("query args = %v, want %v", got, want)
	}
	if slices.Contains(got, "NumberOfPasswordPrompts=1") {
		t.Error("the prompt limit leaked into the config query")
	}
}

func TestParseSSHConfig(t *testing.T) {
	const direct = "user alice\nhostname real.example.com\nport 22\nproxycommand none\nproxyjump none\n"
	host, proxied := parseSSHConfig(direct)
	if host != "real.example.com" || proxied {
		t.Errorf("parseSSHConfig(direct) = %q, %v; want real.example.com, false", host, proxied)
	}
	// ssh prints the keywords in no fixed order, and an unset one still prints
	// as "none" — which must not talk a set one back out of it.
	const jumped = "hostname real.example.com\nproxyjump jump.example.com\nproxycommand none\n"
	if host, proxied = parseSSHConfig(jumped); host != "real.example.com" || !proxied {
		t.Errorf("parseSSHConfig(jumped) = %q, %v; want real.example.com, true", host, proxied)
	}
	if host, proxied = parseSSHConfig(""); host != "" || proxied {
		t.Errorf("parseSSHConfig(empty) = %q, %v; want \"\", false", host, proxied)
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

// A target with no SSH rung never gets a ProbeSSH result at all, and a missing
// map key yields the zero ProbeResult — whose Status is StatusPass, the first
// iota. Presence has to be checked alongside the status, or every HTTPS target
// advertises a login nothing has vouched for.
func TestSSHHintStaysHiddenWithoutAnSSHRung(t *testing.T) {
	oldLookPath := toolLookPath
	toolLookPath = func(string) (string, error) { return "ssh", nil }
	t.Cleanup(func() { toolLookPath = oldLookPath })

	m := newModel(mustTarget(t, "example.com:443"), false)
	m = asModel(t, must(m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})))
	doneResults(&m, "") // every rung this target has passes; none of them is SSH

	if _, ok := m.results[diagnostic.ProbeSSH]; ok {
		t.Fatal("an https target grew an SSH rung — pick another target for this test")
	}
	if strings.Contains(m.View(), "ssh login") {
		t.Error("the help bar hints S on a target with no SSH rung")
	}
	if strings.Contains(asModel(t, must(m.Update(keyMsg("?")))).View(), "hands the terminal to ssh") {
		t.Error("the cheatsheet lists S on a target with no SSH rung")
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
	stubSSHEffective(t, "example.com", false)
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
