//go:build linux

package simulation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// sameExecutable reports whether pid is running the same program that is now
// asking. A kept simulation's director is always netdoc-sim, and so is the
// process releasing it, which makes this the one identity check a doctored
// record cannot satisfy by naming some unrelated process on the host. The path
// is compared by name, not in full: a rebuild or a reinstall moves the binary
// without making the running director someone else.
func sameExecutable(pid int) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false
	}
	return executableName(target) == executableName(self)
}

// executableName drops the " (deleted)" the kernel appends once a running
// binary has been replaced or removed on disk.
func executableName(path string) string {
	return filepath.Base(strings.TrimSuffix(path, " (deleted)"))
}

// checkStateFile enforces what Save writes: a private regular file owned by
// whoever is releasing it. Anything else is another object sitting where a
// record belongs, and cleanup acts on what it reads there.
func checkStateFile(path string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", path)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s: mode %#o leaves the record open to other users", path, perm)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s: not owned by uid %d", path, os.Getuid())
	}
	return nil
}

// stopProcess asks a director to stop, or insists. A process that is already
// gone is not an error, since Release has to be idempotent.
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
