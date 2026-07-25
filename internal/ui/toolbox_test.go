// Toolbox mode: tool sets and the exit code.

package ui

import (
	"testing"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

func TestToolsFor(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		// Generic mode: target-independent tools only (routes, sockets).
		if got := len(toolsFor(nil, goos)); got != 2 {
			t.Errorf("%s toolsFor(nil) = %d, want 2", goos, got)
		}
		// Target mode: + ping, dns, curl, trace, path-quality, nmap.
		if got := len(toolsFor(mustTarget(t, "github.com"), goos)); got != 8 {
			t.Errorf("%s toolsFor(target) = %d, want 8", goos, got)
		}
	}
}

// Toolbox mode with no chain run exits 0.
func TestToolboxExitZero(t *testing.T) {
	m := newModel(nil, true)
	if ExitCode(m) != 0 {
		t.Error("toolbox mode, no chain run, must exit 0")
	}
	// Once the chain runs and a probe fails, normal rules apply.
	m.started[diagnostic.ProbeIface] = true
	for _, probe := range m.probes {
		m.results[probe.ID] = diagnostic.ProbeResult{Status: diagnostic.StatusPass}
	}
	m.results[diagnostic.ProbeDNS] = diagnostic.ProbeResult{Status: diagnostic.StatusFail}
	if ExitCode(m) != 1 {
		t.Error("toolbox mode after a failed chain must exit 1")
	}
}
