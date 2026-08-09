//go:build !unix && !windows

package ui

import "os/exec"

// Unsupported non-Unix platforms have only portable direct-process
// cancellation.
func setProcGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func startProcess(cmd *exec.Cmd) (func(), error) {
	setProcGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	return func() {}, cmd.Start()
}
