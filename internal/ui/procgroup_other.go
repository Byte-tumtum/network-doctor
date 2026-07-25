//go:build !unix

package ui

import "os/exec"

// setProcGroup is a no-op: on Windows the tool set (ping/tracert/pathping/
// netstat/nslookup/curl) spawns no descendant trees, and on unsupported
// GOOSes a plain Kill is the only portable cancellation.
// ponytail: plain Kill; Job Objects are the upgrade path if a tree-killing
// tool ever lands in the table.
func setProcGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
