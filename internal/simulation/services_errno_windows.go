package simulation

import (
	"errors"
	"syscall"
)

func isConnectionRefused(err error) bool { return errors.Is(err, syscall.Errno(10061)) }
