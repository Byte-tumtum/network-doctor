package main

import (
	"encoding/base64"
	"io"
	"os"
)

// Copying from here has one hard constraint: a challenge runs inside namespaces
// this process created, and on macOS or Windows it runs inside a Linux
// container. pbcopy, wl-copy and xclip are all on the far side of that
// boundary — the image ships none of them, and a fresh network namespace cuts
// the X11 abstract socket out from under the ones a Linux host does have.
//
// The terminal is the one thing left that is still the user's own machine, so
// the request goes to the terminal. OSC 52 is the escape that asks it to put
// text on the clipboard, and it crosses ssh, tmux and `docker run -it` without
// a mount, a socket or a capability, which is exactly the set of things
// Challenge Mode refuses to require.
//
// It is fire-and-forget by nature: a terminal that does not implement it, or
// has it switched off, drops it in silence. Nothing here may treat a written
// escape as a clipboard that changed, and nothing may treat an unwritten one as
// a failure worth reporting.

// clipboardCopy is the seam tests replace, so a test run never reaches the
// developer's own clipboard.
var clipboardCopy = copyToTerminalClipboard

// copyToTerminalClipboard asks the terminal behind w to copy text, and reports
// whether the request was written at all. It writes nothing unless w is a
// terminal: an escape sequence in a redirected file, a piped result or a CI log
// is noise in somebody's artifact, and the whole point of this is a person who
// is about to paste something.
func copyToTerminalClipboard(w io.Writer, text string) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	// A character device is as close as the standard library gets to "a terminal
	// is on the other end". /dev/null passes it, which costs a notice nobody is
	// there to read; a pipe, a file and a CI log all fail it, which is the case
	// that matters.
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	_, err = f.WriteString(osc52Clipboard(text))
	return err == nil
}

// osc52Clipboard encodes text as the OSC 52 clipboard request. Inside tmux the
// sequence has to ride tmux's DCS passthrough envelope, or tmux quietly eats it
// instead of forwarding it to the terminal that can honour it.
func osc52Clipboard(text string) string {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if os.Getenv("TMUX") != "" {
		return "\x1bPtmux;\x1b" + seq + "\x1b\\"
	}
	return seq
}
