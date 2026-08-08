//go:build !linux

package simulation

import (
	"fmt"
	"os"
)

// No backend runs off Linux, so no simulation can be recorded and none can be
// alive. These keep the shared state code compiling everywhere.

func processStamp(int) string { return "" }

func sameExecutable(int) bool { return false }

func stopProcess(int, bool) error { return ErrUnsupported }

// checkStateFile has no ownership convention to enforce where no record is
// ever written; refusing anything that is not a plain file is all that is left
// to check.
func checkStateFile(path string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", path)
	}
	return nil
}
