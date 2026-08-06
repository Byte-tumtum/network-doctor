//go:build linux

package simulation

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// processStamp identifies the process currently occupying a pid: its start
// time in kernel ticks, which the kernel never repeats for the same pid. An
// empty stamp means there is no such process.
//
// /proc/<pid>/stat's second field is the executable name in parentheses and may
// itself contain spaces and parentheses, so the fields after it are counted
// from the last ')' rather than from the start of the line.
func processStamp(pid int) string {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	rest := string(stat)
	if i := strings.LastIndex(rest, ")"); i >= 0 {
		rest = rest[i+1:]
	}
	// starttime is field 22 overall, so the 20th of what is left after pid and
	// comm have been cut off.
	fields := strings.Fields(rest)
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// stopProcess asks a director to stop, or insists. A process that is already
// gone is not an error — Release has to be idempotent.
func stopProcess(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
