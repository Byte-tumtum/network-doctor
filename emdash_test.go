// Style guard for the repository's em dash rule (see AGENTS.md): prose that
// wants one gets rewritten, never re-punctuated with the character itself. It
// came back a few at a time in comments, help strings, docs, scenario YAML and
// workflow files, so the rule is enforced here instead of remembered.
//
// The file list comes from git on purpose: it is exactly the tracked files, it
// needs no skip list for build output or local scratch, and -I leaves binary
// files alone, where the three bytes of U+2014 turn up inside compressed image
// data by coincidence and mean nothing. A build from a source tarball has no
// repository and skips.

package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// emDash is written as an escape so this guard does not trip over itself.
const emDash = "\u2014"

func TestNoEmDashInTrackedTextFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, and the tracked-file list comes from it")
	}
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		t.Skip("not a git checkout, so there are no tracked files to read")
	}
	// Exit status 1 is git grep for "no match", which is the passing case here.
	out, err := exec.Command("git", "grep", "-In", "--fixed-strings", "-e", emDash).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return
	}
	if err != nil {
		t.Fatalf("git grep: %v", err)
	}
	hits := strings.Split(strings.TrimSpace(string(out)), "\n")
	t.Errorf("em dash found on %d tracked line(s); rewrite the sentence rather than swapping the character:\n%s",
		len(hits), out)
}
