// The SSH login form (S) and the interactive session it launches. Unlike the
// drill-down tools this is not a bounded job: ssh gets the real terminal, so
// the session is fully interactive and netdoc is suspended until it ends.

package ui

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

// sshDoneMsg reports an interactive ssh session's exit once the TUI has the
// terminal back, carrying what ssh said on stderr so a failure can be read
// here instead of in the scrollback the TUI paints over on its way back.
type sshDoneMsg struct {
	err     error
	display string
	output  string
}

// The form's fields, in tab order.
const (
	sshUser = iota
	sshKey
	sshPass
	sshFields
)

// AskpassEnv carries the password to the askpass helper, which is this binary
// re-executed by ssh: main reads the variable and writes the secret to stdout,
// which is where ssh expects an askpass helper's answer.
const AskpassEnv = "NETDOC_ASKPASS"

// AskpassHostEnv names the host the password was collected for. ssh's whole
// subtree inherits the askpass setting, so a ProxyJump child asking for the
// jump host's password reaches the same helper; the helper compares the host
// in the prompt against this one and refuses anything else.
const AskpassHostEnv = "NETDOC_ASKPASS_HOST"

// sshForm is the SSH login prompt. The host is the run target, not a field:
// the form logs in to the machine the checks are about.
type sshForm struct {
	host   string
	user   textinput.Model
	pass   textinput.Model
	keys   []string // private keys found in ~/.ssh; keyIdx 0 means "none"
	keyIdx int
	focus  int
	err    string
}

// newSSHForm seeds the form from the run target, the local login name, and the
// keys sitting in ~/.ssh — the three answers netdoc can give on the user's
// behalf.
func newSSHForm(t *diagnostic.Target) sshForm {
	f := sshForm{host: sshHostValue(t), keys: append([]string{""}, listSSHKeys()...)}

	f.user = textinput.New()
	f.user.Prompt = "Username  "
	f.user.Placeholder = "your login name on that host"
	f.user.PromptStyle = keyStyle
	if u, err := user.Current(); err == nil {
		f.user.SetValue(u.Username)
	}

	f.pass = textinput.New()
	f.pass.Prompt = "Password  "
	f.pass.Placeholder = "optional"
	f.pass.PromptStyle = keyStyle
	// A typed password is echoed as dots; it is still held in memory, so it
	// never reaches a notice, the report, or an argv.
	f.pass.EchoMode = textinput.EchoPassword

	f.setFocus(sshUser)
	return f
}

// sshHostValue renders the target for ssh: bare host, plus the port when the
// target named a non-default one. JoinHostPort, because concatenating a port
// onto a bare IPv6 literal yields another valid address — sshCommand would
// parse it back as a host nobody asked for and connect there on port 22.
func sshHostValue(t *diagnostic.Target) string {
	if t == nil {
		return ""
	}
	if t.PortExplicit && t.Port != 22 {
		return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	}
	return t.Host
}

// listSSHKeys returns the private keys in ~/.ssh, recognized by their public
// half sitting next to them — which costs no parsing and never opens the
// secret. ponytail: ~/.ssh only; a key elsewhere is what ssh_config is for.
func listSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var keys []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".pub")
		if !ok {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
			keys = append(keys, filepath.Join(dir, name))
		}
	}
	slices.Sort(keys)
	return keys
}

// keyLabel names the selected key for the form: the file name is what the user
// recognizes, and the directory is the same for all of them.
func (f sshForm) keyLabel() string {
	if f.keyIdx == 0 {
		if len(f.keys) == 1 {
			return "none found in ~/.ssh — the agent or a password it is"
		}
		return "none — use the agent or a password"
	}
	return filepath.Base(f.keys[f.keyIdx])
}

func (f sshForm) keyPath() string { return f.keys[f.keyIdx] }

// cycleKey walks the key list, wrapping at both ends.
func (f *sshForm) cycleKey(delta int) {
	f.keyIdx = (f.keyIdx + delta + len(f.keys)) % len(f.keys)
}

func (f *sshForm) setFocus(i int) tea.Cmd {
	f.focus = (i + sshFields) % sshFields
	f.user.Blur()
	f.pass.Blur()
	switch f.focus {
	case sshUser:
		f.user.CursorEnd()
		return f.user.Focus()
	case sshPass:
		f.pass.CursorEnd()
		return f.pass.Focus()
	}
	return nil // the key row is a chooser, not an input
}

// handleSSHKey handles keys while the SSH form is open: tab/shift+tab (and
// up/down) move between fields, ←/→ picks a key while that row is focused,
// enter connects, esc backs out.
func (m model) handleSSHKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.sshPrompt = false
		return m, nil
	case "tab", "down":
		return m, m.ssh.setFocus(m.ssh.focus + 1)
	case "shift+tab", "up":
		return m, m.ssh.setFocus(m.ssh.focus - 1)
	case "left", "right":
		if m.ssh.focus != sshKey {
			break // a cursor move inside the focused input
		}
		delta := 1
		if msg.String() == "left" {
			delta = -1
		}
		m.ssh.cycleKey(delta)
		return m, nil
	case "enter":
		self, err := os.Executable()
		if err != nil {
			self = "" // no askpass helper; ssh falls back to asking on the tty
		}
		args, env, err := sshCommand(m.ssh.host, strings.TrimSpace(m.ssh.user.Value()),
			m.ssh.keyPath(), m.ssh.pass.Value(), self)
		if err != nil {
			m.ssh.err = err.Error()
			return m, nil
		}
		m.sshPrompt = false
		// The password is in env now, so the form has no reason to keep it.
		// ponytail: Go strings can't be wiped, this only shortens the window.
		m.ssh.pass.SetValue("")
		return m, runSSH(args, env)
	}
	var cmd tea.Cmd
	switch m.ssh.focus {
	case sshUser:
		m.ssh.user, cmd = m.ssh.user.Update(msg)
	case sshPass:
		m.ssh.pass, cmd = m.ssh.pass.Update(msg)
	}
	m.ssh.err = ""
	return m, cmd
}

// sshCommand builds the argv and environment for the form's answers. self is
// this binary's path, used as ssh's askpass helper; an empty self drops the
// password and leaves ssh to prompt for it on the terminal.
func sshCommand(host, login, key, password, self string) (args, env []string, err error) {
	if host == "" {
		return nil, nil, errors.New("no target host — press r to set one")
	}
	t, err := diagnostic.ParseTarget(host)
	if err != nil {
		return nil, nil, err
	}
	if t.PortExplicit && t.Port != 22 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	if key != "" {
		// IdentitiesOnly keeps ssh from offering the agent's keys ahead of the
		// one that was picked here — otherwise a full agent can exhaust the
		// server's auth attempts before this key is ever tried.
		args = append(args, "-i", key, "-o", "IdentitiesOnly=yes")
	}
	if password != "" && self != "" {
		// The secret goes through the environment (readable only by this user)
		// to the helper, never through argv, which every process can read.
		// One prompt only: the helper would just repeat a rejected password.
		args = append(args, "-o", "NumberOfPasswordPrompts=1")
		env = append(os.Environ(),
			"SSH_ASKPASS="+self,
			"SSH_ASKPASS_REQUIRE=force", // ask the helper even though ssh has a tty
			AskpassEnv+"="+password,
			AskpassHostEnv+"="+t.Host)
	}
	dest := t.Host
	if login != "" {
		dest = login + "@" + dest
	}
	return append(args, dest), env, nil
}

// runSSH hands the terminal to ssh and takes it back when the session ends.
// tea.ExecProcess (not startTool) because ssh needs the real tty: that is what
// makes the session interactive, and what lets ssh ask for anything the form
// left blank — a key passphrase, a host-key confirmation, a 2FA code.
//
// stderr is teed rather than captured: it still scrolls past live during the
// session, and the copy comes back with the terminal so "Permission denied"
// survives in a job pane instead of being painted over by the restored TUI.
func runSSH(args, env []string) tea.Cmd {
	cmd := exec.Command("ssh", args...)
	cmd.Env = env // nil inherits
	tee := &capWriter{}
	cmd.Stderr = io.MultiWriter(os.Stderr, tee)
	display := "ssh " + shellArgs(args)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return sshDoneMsg{err: err, display: display, output: tee.String()}
	})
}

// capWriter keeps the first capBytes it is given and counts the rest away. The
// first bytes are the useful ones: ssh reports why a login failed up front,
// while a long session's remote stderr is scrollback nobody needs a copy of.
// The exec pipe's copier is done before Wait returns, so no lock is needed.
type capWriter struct {
	b       []byte
	dropped int
}

const capBytes = 64 << 10

func (w *capWriter) Write(p []byte) (int, error) {
	kept := min(max(capBytes-len(w.b), 0), len(p))
	w.b = append(w.b, p[:kept]...)
	w.dropped += len(p) - kept
	return len(p), nil
}

func (w *capWriter) String() string {
	if w.dropped > 0 {
		return string(w.b) + "\n…[" + strconv.Itoa(w.dropped) + " more bytes discarded]"
	}
	return string(w.b)
}
