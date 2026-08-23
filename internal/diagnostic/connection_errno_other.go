//go:build !windows

package diagnostic

import (
	"errors"
	"syscall"
)

const connectionRefusedErrno = syscall.ECONNREFUSED

func isConnectionRefused(err error) bool { return errors.Is(err, connectionRefusedErrno) }
