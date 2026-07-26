//go:build unix

package ui

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestKillGroupNilProcess(t *testing.T) {
	if err := killGroup(exec.Command("true")); err != nil {
		t.Fatalf("nil Process: %v", err)
	}
}

func TestKillGroupReapedLeaderFallsBack(t *testing.T) {
	cmd := exec.Command("true")
	setProcGroup(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Leader reaped, so Getpgid fails; the fallback Process.Kill on a waited
	// process reports ErrProcessDone rather than signalling a stale pgid.
	if err := killGroup(cmd); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("want ErrProcessDone from fallback, got %v", err)
	}
}
