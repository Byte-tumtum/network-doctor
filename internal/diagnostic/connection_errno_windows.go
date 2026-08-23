//go:build windows

package diagnostic

import (
	"errors"

	"golang.org/x/sys/windows"
)

const connectionRefusedErrno = windows.WSAECONNREFUSED

func isConnectionRefused(err error) bool { return errors.Is(err, connectionRefusedErrno) }
