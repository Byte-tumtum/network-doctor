//go:build netns_integration

// Opt-in (-tags netns_integration) tests that build real network namespaces.
//
//	go test -tags netns_integration ./internal/simulation
//
// No root needed: the simulator runs in an unprivileged user namespace. The
// tests skip themselves on a host where that is unavailable, and nothing they
// create is reachable from — or visible to — the host network.

package simulation

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinaries produces the two binaries an end-to-end run needs, from the
// tree under test rather than whatever is installed.
func buildBinaries(t *testing.T) (netdoc, sim string) {
	t.Helper()
	dir := t.TempDir()
	netdoc = filepath.Join(dir, "netdoc")
	sim = filepath.Join(dir, "netdoc-sim")
	for out, pkg := range map[string]string{
		netdoc: "github.com/heymaikol/network-doctor",
		sim:    "github.com/heymaikol/network-doctor/cmd/netdoc-sim",
	} {
		cmd := exec.Command("go", "build", "-o", out, pkg)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, msg)
		}
	}
	return netdoc, sim
}

// RequireNetnsEnv turns "this host cannot simulate" from a skip into a failure.
// A CI job that is supposed to exercise namespaces has to fail when it silently
// stops doing so — a green run full of skips is how a backend regression hides
// for a release cycle. Developers on a host without user namespaces leave it
// unset and get the skip.
const RequireNetnsEnv = "NETDOC_SIM_REQUIRE_NETNS"

func requireBackend(t *testing.T) {
	t.Helper()
	caps := DefaultBackend(false, nil).Capabilities(context.Background())
	if caps.Supported {
		return
	}
	if os.Getenv(RequireNetnsEnv) != "" {
		t.Fatalf("%s is set, so these tests must run, but the backend is unavailable: %s",
			RequireNetnsEnv, caps.Reason)
	}
	t.Skip("simulation backend unavailable: " + caps.Reason)
}

// runScenario runs one scenario end to end and returns the parsed report.
func runScenario(t *testing.T, name string) Report {
	t.Helper()
	netdoc, sim := buildBinaries(t)
	cmd := exec.Command(sim, "run", name, "-json", "-netdoc", netdoc)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !asExitError(err, &exit) {
		t.Fatalf("run %s: %v", name, err)
	}
	var rep Report
	if jsonErr := json.Unmarshal(out, &rep); jsonErr != nil {
		t.Fatalf("run %s: report is not JSON (%v): %s", name, jsonErr, out)
	}
	return rep
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// TestHealthyScenario is the control: netdoc must find nothing wrong with a
// network where nothing is wrong.
func TestHealthyScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "healthy")
	if rep.Result != ResultPass {
		t.Errorf("result = %s (error %q); suggestions: %+v", rep.Result, rep.Error, rep.Suggestions)
	}
	if len(rep.Tests) != 1 || rep.Tests[0].ActualVerdict != "ok" {
		t.Fatalf("tests = %+v", rep.Tests)
	}
	if rep.Tests[0].FalsePositives != 0 {
		t.Errorf("netdoc flagged %d things in a healthy network", rep.Tests[0].FalsePositives)
	}
	assertCleanedUp(t, rep)
}

// TestBrokenDNSScenario is the vertical slice: a fault that only breaks the
// name, with the path underneath left working, and a diagnosis that has to say
// which of the two it was.
func TestBrokenDNSScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "broken-dns")
	if rep.Result != ResultPass {
		t.Errorf("result = %s (error %q); suggestions: %+v", rep.Result, rep.Error, rep.Suggestions)
	}
	out := rep.Tests[0]
	// Stable ids, never the prose: the wording is allowed to change.
	byID := map[string]string{}
	for _, c := range out.Diagnosis.Checks {
		byID[c.ID] = c.Status
	}
	if byID["dns"] != "FAIL" {
		t.Errorf("dns = %s, want FAIL", byID["dns"])
	}
	if byID["internet_tcp"] != "PASS" {
		t.Errorf("internet_tcp = %s, want PASS — the point is that the path still works", byID["internet_tcp"])
	}
	if out.ActualVerdict != "dns" {
		t.Errorf("verdict = %s, want dns", out.ActualVerdict)
	}
	assertCleanedUp(t, rep)
}

func TestNoDefaultRouteScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "no-default-route")
	if rep.Result != ResultPass {
		t.Errorf("result = %s (error %q); suggestions: %+v", rep.Result, rep.Error, rep.Suggestions)
	}
	assertCleanedUp(t, rep)
}

// assertCleanedUp proves the run released everything, and — the part that
// matters — that the host never had any of it in the first place.
func assertCleanedUp(t *testing.T, rep Report) {
	t.Helper()
	if !rep.Cleanup.Done || len(rep.Cleanup.Errors) > 0 {
		t.Errorf("cleanup: done=%t errors=%v", rep.Cleanup.Done, rep.Cleanup.Errors)
	}
	out, err := exec.Command("ip", "-o", "link", "show").Output()
	if err != nil {
		t.Skip("cannot list host links:", err)
	}
	for _, name := range []string{"nb" + rep.ID, "np" + rep.ID, "ne" + rep.ID} {
		if strings.Contains(string(out), name) {
			t.Errorf("simulation interface %s is visible on the host", name)
		}
	}
}

// TestDryRunCreatesNothing checks the audit path: -dry-run has to print the
// privileged commands without making any of them happen.
func TestDryRunCreatesNothing(t *testing.T) {
	requireBackend(t)
	_, sim := buildBinaries(t)
	cmd := exec.Command(sim, "run", "healthy", "-dry-run")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, want := range []string{"ip link add", "type veth peer name", "spawn node holder"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("dry run did not mention %q:\n%s", want, out)
		}
	}
}
