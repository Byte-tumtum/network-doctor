//go:build !linux

package simulation

import (
	"context"
	"io"
	"runtime"
)

// DefaultBackend returns a backend that explains itself instead of failing
// mysteriously. Namespaces are a Linux mechanism; a container, libvirt or
// platform-native backend would slot in behind the same interface.
func DefaultBackend(bool, io.Writer) Backend { return unsupportedBackend{} }

type unsupportedBackend struct{}

func (unsupportedBackend) Capabilities(context.Context) Capabilities {
	return Capabilities{
		Backend: "unsupported",
		Reason: "network simulation needs Linux network namespaces; " + runtime.GOOS +
			" has no backend yet. Run netdoc-sim on a Linux host, or in a Linux VM or container with unprivileged user namespaces enabled.",
	}
}

func (unsupportedBackend) Prepare(context.Context, *Scenario, string) (Env, error) {
	return nil, ErrUnsupported
}

// LaunchDirector and RunNode exist so the CLI compiles everywhere. Neither can
// be reached: the caller checks Capabilities first, and only the Linux backend
// spawns holders.

func LaunchDirector(context.Context, string, []string, io.Writer, io.Writer) (int, error) {
	return 1, ErrUnsupported
}

func RunNode(context.Context, string, io.Reader, io.Writer, io.Writer) error {
	return ErrUnsupported
}
