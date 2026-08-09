//go:build windows

package ui

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCancelKillsDescendant(t *testing.T) {
	j := startHelper(t, context.Background(), "grandchild")
	pid := grandchildPID(t, j.ch)
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("open descendant %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(process, 1)
		windows.CloseHandle(process)
	})
	if event, err := windows.WaitForSingleObject(process, 0); err != nil {
		t.Fatalf("check descendant %d: %v", pid, err)
	} else if event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("descendant %d exited before cancellation", pid)
	}

	j.cancel()
	_, done := drain(t, j.ch)
	if done.Status != JobCanceled {
		t.Errorf("status = %v, want JobCanceled", done.Status)
	}
	if event, err := windows.WaitForSingleObject(process, 5_000); err != nil {
		t.Fatalf("wait for descendant %d: %v", pid, err)
	} else if event != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant %d survived cancellation", pid)
	}
}
