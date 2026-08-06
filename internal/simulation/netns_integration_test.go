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

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

const proxyProbeName = "connectivitycheck.gstatic.com"

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

func TestSOCKS5LocalDNSScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "socks5-local-dns-fails")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); suggestions: %+v; evidence: %+v", rep.Result, rep.Error, rep.Suggestions, rep.Evidence)
	}
	out := rep.Tests[0]
	proxy := diagnosisCheck(out, string(diagnostic.ProbeProxy))
	if proxy.Status != "FAIL" || !strings.Contains(proxy.Detail, "is reachable, but local DNS cannot resolve") {
		t.Errorf("proxy_connect = %+v", proxy)
	}
	if out.FalsePositives != 0 || out.FalseNegatives != 0 {
		t.Errorf("comparison fp=%d fn=%d", out.FalsePositives, out.FalseNegatives)
	}
	if !hasSOCKSEvidence(rep, "greeting", "", "accepted") {
		t.Errorf("no accepted SOCKS greeting evidence: %+v", rep.Evidence.SOCKSRequests)
	}
	if hasSOCKSEvidence(rep, "connect", "domain", "connected") {
		t.Errorf("local SOCKS unexpectedly sent a domain request: %+v", rep.Evidence.SOCKSRequests)
	}
	if !hasDNSEvidence(rep, "client-dns", "10.77.0.10", proxyProbeName, "NXDOMAIN") {
		t.Errorf("no client-side NXDOMAIN evidence: %+v", rep.Evidence.DNS)
	}
	if hasDNSEvidence(rep, "proxy", "10.77.0.30", proxyProbeName, "ANSWER") {
		t.Errorf("proxy resolved a name it never received: %+v", rep.Evidence.DNS)
	}
	assertCleanedUp(t, rep)
}

func TestSOCKS5hRemoteDNSScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "socks5h-remote-dns-succeeds")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); suggestions: %+v; evidence: %+v", rep.Result, rep.Error, rep.Suggestions, rep.Evidence)
	}
	out := rep.Tests[0]
	if proxy := diagnosisCheck(out, string(diagnostic.ProbeProxy)); proxy.Status != "PASS" {
		t.Errorf("proxy_connect = %+v", proxy)
	}
	if out.FalsePositives != 0 || out.FalseNegatives != 0 {
		t.Errorf("comparison fp=%d fn=%d", out.FalsePositives, out.FalseNegatives)
	}
	if !hasSOCKSEvidence(rep, "connect", "domain", "connected") {
		t.Errorf("no successful domain CONNECT evidence: %+v", rep.Evidence.SOCKSRequests)
	}
	if hasDNSQuery(rep, "10.77.0.10", proxyProbeName) {
		t.Errorf("SOCKS5h caused a client-side lookup: %+v", rep.Evidence.DNS)
	}
	if !hasDNSEvidence(rep, "proxy", "10.77.0.30", proxyProbeName, "ANSWER") {
		t.Errorf("no proxy-side answer evidence: %+v", rep.Evidence.DNS)
	}
	assertCleanedUp(t, rep)
}

func diagnosisCheck(out TestOutcome, id string) DiagnosisCheck {
	if out.Diagnosis == nil {
		return DiagnosisCheck{}
	}
	for _, check := range out.Diagnosis.Checks {
		if check.ID == id {
			return check
		}
	}
	return DiagnosisCheck{}
}

func hasDNSEvidence(rep Report, node, source, name, result string) bool {
	for _, item := range rep.Evidence.DNS {
		if item.Node == node && item.Source == source && item.Name == name && item.Result == result && item.Count > 0 {
			return true
		}
	}
	return false
}

func hasDNSQuery(rep Report, source, name string) bool {
	for _, item := range rep.Evidence.DNS {
		if item.Source == source && item.Name == name && item.Count > 0 {
			return true
		}
	}
	return false
}

func hasSOCKSEvidence(rep Report, event, addressType, result string) bool {
	for _, item := range rep.Evidence.SOCKSRequests {
		if item.Event == event && item.AddressType == addressType && item.Result == result && item.Count > 0 {
			return true
		}
	}
	return false
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
