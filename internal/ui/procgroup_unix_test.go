//go:build unix

package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
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

// The exit path has to reach a tool's descendants, not just the process we
// spawned: mtr leaves an mtr-packet behind, and it would outlive netdoc.
func TestCleanupKillsGrandchild(t *testing.T) {
	var m model
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.cur.active = startHelper(t, m.ctx, "grandchild")
	pid := grandchildPID(t, m.cur.active.ch)

	Cleanup(m)

	// The grandchild is reparented to init once the leader dies, so it is gone
	// a moment after the signal rather than the instant Cleanup returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL) // don't leak it into the next test
			t.Fatalf("grandchild %d survived Cleanup", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
