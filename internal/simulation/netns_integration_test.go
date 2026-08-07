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
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
)

const proxyProbeName = "connectivitycheck.gstatic.com"

// buildBinaries produces the two binaries an end-to-end run needs, from the
// tree under test rather than whatever is installed.
//
// CGO_ENABLED=0, which is how releases are built, and the only setting that
// simulates anything. A cgo build resolves through glibc's getaddrinfo, which
// inside a node namespace does not use the node's resolver: on a systemd-resolved
// host it reaches the host's resolver over a Unix socket in the shared /run, and
// where that is absent it blocks — ignoring the probe's context — until the probe
// deadline expires. Either way the run measures the host, not the simulation.
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
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
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
func runScenario(t *testing.T, name string, extra ...string) Report {
	t.Helper()
	netdoc, sim := buildBinaries(t)
	cmd := exec.Command(sim, append([]string{"run", name, "-json", "-netdoc", netdoc}, extra...)...)
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

func hasSuggestion(suggestions []Suggestion, code string) bool {
	for _, suggestion := range suggestions {
		if suggestion.Code == code {
			return true
		}
	}
	return false
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
	if cause := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)).Cause; cause != diagnostic.RouteCauseNoDefaultRoute {
		t.Errorf("internet_tcp cause = %q, want %q", cause, diagnostic.RouteCauseNoDefaultRoute)
	}
	assertCleanedUp(t, rep)
}

func TestHighLatencyScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "high-latency")
	if rep.Result != ResultPass {
		t.Errorf("result = %s (error %q); suggestions: %+v", rep.Result, rep.Error, rep.Suggestions)
	}
	assertCleanedUp(t, rep)
}

func TestPacketLossScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "packet-loss")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v suggestions=%+v", rep.Result, rep.Error, rep.Tests, rep.Suggestions)
	}
	if len(rep.Faults) != 1 || rep.Faults[0].Type != FaultNetem || rep.Faults[0].LossPercent != 10 || rep.Faults[0].Seed == 0 {
		t.Fatalf("packet-loss qdisc evidence = %+v", rep.Faults)
	}
	if len(rep.Evidence.PacketConditions) != 1 || !rep.Evidence.PacketConditions[0].Active || rep.Evidence.PacketConditions[0].DroppedPackets == 0 {
		t.Fatalf("tc did not report an active netem qdisc: %+v", rep.Evidence.PacketConditions)
	}
	assertCleanedUp(t, rep)
}

func TestHighJitterScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "high-jitter")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v suggestions=%+v", rep.Result, rep.Error, rep.Tests, rep.Suggestions)
	}
	if len(rep.Faults) != 1 || rep.Faults[0].Jitter != 100000000 {
		t.Fatalf("jitter evidence = %+v", rep.Faults)
	}
	condition := rep.Evidence.PacketConditions[0]
	if condition.RTTSamples < 5 || condition.ObservedMaxRTT <= condition.ObservedMinRTT {
		t.Errorf("RTT observations did not vary: %+v", condition)
	}
	if !hasSuggestion(rep.Suggestions, "jitter_sampling_gap") {
		t.Errorf("no deterministic jitter coverage-gap suggestion: %+v", rep.Suggestions)
	}
	assertCleanedUp(t, rep)
}

func TestIntermittentDNSScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "intermittent-dns")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v DNS=%+v", rep.Result, rep.Error, rep.Tests, rep.Evidence.DNSQueries)
	}
	servfail, answer := false, false
	for _, query := range rep.Evidence.DNSQueries {
		if query.Service != "intermittent-resolver" {
			continue
		}
		servfail = servfail || query.ScheduledOutcome == DNSOutcomeSERVFAIL && query.ActualOutcome == "SERVFAIL"
		answer = answer || query.ScheduledOutcome == DNSOutcomeAnswer && query.ActualOutcome == "ANSWER"
	}
	if !servfail || !answer {
		t.Errorf("scheduled resolver did not prove both outcomes: %+v", rep.Evidence.DNSQueries)
	}
	if cause := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeDNS)).Cause; cause != diagnostic.DNSCauseTemporaryFailure {
		t.Errorf("first DNS cause = %q", cause)
	}
	assertCleanedUp(t, rep)
}

func TestTCPResetScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "tcp-reset")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v reset=%+v", rep.Result, rep.Error, rep.Tests, rep.Evidence.TCPResets)
	}
	if target := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeTargetTCP)); target.Status != "PASS" {
		t.Errorf("target TCP = %+v", target)
	}
	if banner := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeSSH)); banner.Status != "FAIL" || banner.Cause != diagnostic.ConnectionCauseReset {
		t.Errorf("SSH reset classification = %+v", banner)
	}
	accepted, reset := false, false
	for _, event := range rep.Evidence.TCPResets {
		accepted = accepted || event.Event == "accepted" && event.Count > 0
		reset = reset || event.Event == "reset" && event.Count > 0
	}
	if !accepted || !reset {
		t.Errorf("reset service evidence = %+v", rep.Evidence.TCPResets)
	}
	assertCleanedUp(t, rep)
}

// timedTimeout keeps the timed scenarios short in CI. Their timelines are
// designed so the transitions land the same way at any probe timeout above a
// few hundred milliseconds; this only decides how long the run that waits out a
// dropped query takes.
const timedTimeout = "1s"

func appliedEvent(t *testing.T, rep Report, kind, state string) FaultEventEvidence {
	t.Helper()
	for _, item := range rep.Timeline {
		if item.Result == EventApplied && item.Event.Type == kind &&
			(item.Event.Outcome == state || item.Event.State == state) {
			return item
		}
	}
	t.Fatalf("no applied %s event reaching %q: %+v", kind, state, rep.Timeline)
	return FaultEventEvidence{}
}

func TestTransientDNSOutageScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "transient-dns-outage", "-timeout", timedTimeout)
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v timeline=%+v", rep.Result, rep.Error, rep.Tests, rep.Timeline)
	}
	drop := appliedEvent(t, rep, FaultScheduledDNS, DNSOutcomeDrop)
	recover := appliedEvent(t, rep, FaultScheduledDNS, DNSOutcomeAnswer)
	if !(drop.AppliedOffset < recover.AppliedOffset) {
		t.Fatalf("outage did not precede recovery: %+v %+v", drop, recover)
	}
	// The resolver answered before the outage, went silent during it, and the
	// silent query is the one that failed.
	before, during, after := 0, 0, 0
	for _, q := range rep.Evidence.DNSQueries {
		if q.Service != "outage-resolver" {
			continue
		}
		switch {
		case q.Offset < drop.AppliedOffset && q.ActualOutcome != "DROPPED":
			before++
		case q.Offset >= drop.AppliedOffset && q.Offset < recover.AppliedOffset && q.ActualOutcome == "DROPPED":
			during++
		case q.Offset >= recover.AppliedOffset && q.ActualOutcome == "ANSWER":
			after++
		}
	}
	if before == 0 || during == 0 || after == 0 {
		t.Fatalf("queries before/during/after the outage = %d/%d/%d: %+v", before, during, after, rep.Evidence.DNSQueries)
	}
	if got := diagnosisCheck(rep.Tests[1], string(diagnostic.ProbeDNS)); got.Status != "FAIL" || got.Cause != diagnostic.DNSCauseTimeout {
		t.Errorf("the outage run's DNS row = %+v", got)
	}
	if got := diagnosisCheck(rep.Tests[2], string(diagnostic.ProbeDNS)); got.Status != "PASS" {
		t.Errorf("the recovery run's DNS row = %+v", got)
	}
	// Recovery happened while the failing run was still waiting, and netdoc
	// asked the resolver nothing more before concluding.
	if !(rep.Tests[1].StartOffset < recover.AppliedOffset && recover.AppliedOffset < rep.Tests[1].EndOffset) {
		t.Errorf("recovery at %s is outside the failing run %s..%s",
			recover.AppliedOffset, rep.Tests[1].StartOffset, rep.Tests[1].EndOffset)
	}
	if !hasSuggestion(rep.Suggestions, SuggestTransientNotResampled) {
		t.Errorf("no resampling-gap suggestion: %+v", rep.Suggestions)
	}
	assertCleanedUp(t, rep)
}

func TestLatencySpikeScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "latency-spike", "-timeout", "4s")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v timeline=%+v", rep.Result, rep.Error, rep.Tests, rep.Timeline)
	}
	// The kernel's own view of the qdisc at each state, not just what we asked.
	var netem []FaultEventEvidence
	for _, item := range rep.Timeline {
		if item.Event.Type == FaultScheduledNetem && item.Result == EventApplied {
			netem = append(netem, item)
		}
	}
	if len(netem) != 3 {
		t.Fatalf("scheduled netem events applied = %+v", netem)
	}
	for i, want := range []string{"delay 10ms", "delay 700ms", "delay 10ms"} {
		if !strings.Contains(netem[i].Observed, want) {
			t.Errorf("qdisc after event %d = %q, want %q", i, netem[i].Observed, want)
		}
	}
	// The spike opened and closed inside the single run that spans it.
	run := rep.Tests[0]
	if !(run.StartOffset < netem[1].AppliedOffset && netem[2].AppliedOffset < run.EndOffset) {
		t.Errorf("the spike did not open and close inside the run %s..%s: %+v",
			run.StartOffset, run.EndOffset, netem)
	}
	// Baseline before, spike during: the same path measured at two speeds. The
	// egress attempt that completed before the spike is the baseline sample; the
	// target handshake happened after it and cost two orders of magnitude more.
	if len(rep.Evidence.PacketConditions) != 1 || !rep.Evidence.PacketConditions[0].Active ||
		rep.Evidence.PacketConditions[0].Latency != 10*time.Millisecond {
		t.Fatalf("the qdisc did not return to baseline: %+v", rep.Evidence.PacketConditions)
	}
	baseline := rep.Evidence.PacketConditions[0].ObservedMinRTT
	spiked := time.Duration(diagnosisCheck(run, string(diagnostic.ProbeTargetTCP)).Ms) * time.Millisecond
	if baseline <= 0 || baseline > 200*time.Millisecond || spiked < 500*time.Millisecond {
		t.Errorf("observed RTTs did not reflect the spike: baseline %s, during the spike %s", baseline, spiked)
	}
	assertCleanedUp(t, rep)
}

func TestTransientConnectivityLossScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "transient-connectivity-loss", "-timeout", timedTimeout)
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v timeline=%+v", rep.Result, rep.Error, rep.Tests, rep.Timeline)
	}
	down := appliedEvent(t, rep, FaultScheduledLink, LinkStateDown)
	up := appliedEvent(t, rep, FaultScheduledLink, LinkStateUp)
	if down.Observed != "kernel link up=false" || up.Observed != "kernel link up=true" {
		t.Errorf("kernel link evidence = %q / %q", down.Observed, up.Observed)
	}
	// Reachable before the outage, unreachable during it, reachable after.
	if got := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeTargetTCP)); got.Status != "PASS" {
		t.Errorf("target before the outage = %+v", got)
	}
	if got := diagnosisCheck(rep.Tests[1], string(diagnostic.ProbeTargetTCP)); got.Status != "FAIL" {
		t.Errorf("target during the outage = %+v", got)
	}
	if got := diagnosisCheck(rep.Tests[2], string(diagnostic.ProbeTargetTCP)); got.Status != "PASS" {
		t.Errorf("target after recovery = %+v", got)
	}
	// The first run's handshake happened milliseconds into it, well before the
	// link dropped; the second run's target probe is gated behind a delayed DNS
	// answer and so always starts after it.
	if !(rep.Tests[0].StartOffset < down.AppliedOffset && down.AppliedOffset < rep.Tests[1].EndOffset) {
		t.Errorf("the outage did not fall between the first and second runs: down at %s, runs %s..%s and %s..%s",
			down.AppliedOffset, rep.Tests[0].StartOffset, rep.Tests[0].EndOffset,
			rep.Tests[1].StartOffset, rep.Tests[1].EndOffset)
	}
	// The scenario breaks transport, never routing: the client's routes and its
	// own link are exactly as configured, and the target's link is back up.
	for _, link := range rep.Evidence.Links {
		if !link.Up {
			t.Errorf("link left down: %+v", link)
		}
	}
	routes := 0
	for _, route := range rep.Evidence.Routes {
		if route.Node == "client" && route.Destination == "default" && route.Via == "10.77.0.1" {
			routes++
		}
	}
	if routes != 1 {
		t.Errorf("the client's default route did not survive the outage: %+v", rep.Evidence.Routes)
	}
	if !hasSuggestion(rep.Suggestions, SuggestTransientReportedPermanent) {
		t.Errorf("no transient-versus-permanent suggestion: %+v", rep.Suggestions)
	}
	assertCleanedUp(t, rep)
}

func TestFaultDuringProbeScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "fault-during-probe", "-timeout", timedTimeout)
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests=%+v timeline=%+v", rep.Result, rep.Error, rep.Tests, rep.Timeline)
	}
	drop := appliedEvent(t, rep, FaultScheduledDNS, DNSOutcomeDrop)
	// The point of the scenario: the transition provably happened while an
	// answer was still being held, and that answer was still delivered. The
	// service's own record of when the query arrived and how long it was held
	// is the synchronisation — nothing here waits and hopes.
	held := 0
	for _, q := range rep.Evidence.DNSQueries {
		if q.Service != "inflight-resolver" || q.ScheduledOutcome != DNSOutcomeDelay {
			continue
		}
		due := q.Offset + time.Duration(q.DelayMs)*time.Millisecond
		if q.Offset < drop.AppliedOffset && drop.AppliedOffset < due && q.ActualOutcome != "DROPPED" {
			held++
		}
	}
	if held == 0 {
		t.Fatalf("no answer was in flight across the transition at %s: %+v", drop.AppliedOffset, rep.Evidence.DNSQueries)
	}
	if got := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeDNS)); got.Status != "PASS" {
		t.Errorf("the held answer did not reach netdoc: %+v", got)
	}
	if got := diagnosisCheck(rep.Tests[1], string(diagnostic.ProbeDNS)); got.Status != "FAIL" || got.Cause != diagnostic.DNSCauseTimeout {
		t.Errorf("a query after the transition = %+v", got)
	}
	assertCleanedUp(t, rep)
}

// The flapping campaign's determinism guarantee is the requested timeline, not
// netdoc's answer to it: a transition that lands on a probe boundary is
// genuinely a coin flip, and the campaign exists to say so. So the seeds,
// schedules and timeline fingerprints must reproduce exactly, and the diagnosis
// is only compared where the campaign itself claims stability.
func TestFlappingCampaignTimelinesAreReproducible(t *testing.T) {
	requireBackend(t)
	netdoc, sim := buildBinaries(t)
	run := func(args ...string) CampaignResult {
		base := []string{"campaign", "flapping-connectivity", "--json", "--seed", "4242",
			"--netdoc", netdoc, "-timeout", timedTimeout}
		cmd := exec.Command(sim, append(base, args...)...)
		out, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !asExitError(err, &exit) {
			t.Fatalf("campaign: %v", err)
		}
		var result CampaignResult
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("campaign JSON: %v: %s", err, out)
		}
		return result
	}
	first, second := run("--runs", "3"), run("--runs", "3")
	if len(first.Outcomes) != 3 || len(second.Outcomes) != 3 {
		t.Fatalf("campaign lengths = %d/%d", len(first.Outcomes), len(second.Outcomes))
	}
	for i := range first.Outcomes {
		a, b := first.Outcomes[i], second.Outcomes[i]
		if a.IterationSeed != b.IterationSeed || a.ScheduleID != b.ScheduleID {
			t.Fatalf("iteration %d is not reproducible: %+v != %+v", i, a, b)
		}
		if a.Report.TimelineID != b.Report.TimelineID {
			t.Fatalf("iteration %d timeline fingerprint changed: %q != %q", i, a.Report.TimelineID, b.Report.TimelineID)
		}
		// Five netem phases and three resolver phases, every one of them either
		// applied or explicitly skipped, and none applied before its offset.
		if len(a.Report.Timeline) != 8 {
			t.Fatalf("iteration %d timeline = %+v", i, a.Report.Timeline)
		}
		for _, item := range a.Report.Timeline {
			switch item.Result {
			case EventApplied:
				if item.AppliedOffset < item.ScheduledOffset {
					t.Errorf("iteration %d applied %s early: %+v", i, item.State, item)
				}
			case EventSkipped:
			default:
				t.Errorf("iteration %d event failed: %+v", i, item)
			}
		}
		assertCleanedUp(t, *a.Report)
		assertCleanedUp(t, *b.Report)
	}
	// Direct reproduction of one iteration, by the documented command.
	direct := run("--iteration", "2")
	if len(direct.Outcomes) != 1 || direct.Outcomes[0].Iteration != 2 ||
		direct.Outcomes[0].IterationSeed != first.Outcomes[2].IterationSeed ||
		direct.Outcomes[0].ScheduleID != first.Outcomes[2].ScheduleID ||
		direct.Outcomes[0].Report.TimelineID != first.Outcomes[2].Report.TimelineID {
		t.Fatalf("direct reproduction differs: direct=%+v original=%+v", direct.Outcomes, first.Outcomes[2])
	}
	assertCleanedUp(t, *direct.Outcomes[0].Report)
}

func TestUnstableConnectivityCampaignIsReproducible(t *testing.T) {
	requireBackend(t)
	hostBefore := captureHostNetworkState(t)
	netdoc, sim := buildBinaries(t)
	run := func(iteration string) CampaignResult {
		args := []string{"campaign", "unstable-connectivity", "--json", "--seed", "12345", "--netdoc", netdoc}
		if iteration != "" {
			// The documented reproduction command: seed and iteration only.
			args = append(args, "--iteration", iteration)
		} else {
			args = append(args, "--runs", "5")
		}
		cmd := exec.Command(sim, args...)
		out, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !asExitError(err, &exit) {
			t.Fatalf("campaign: %v", err)
		}
		var result CampaignResult
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("campaign JSON: %v: %s", err, out)
		}
		return result
	}
	first, second := run(""), run("")
	if len(first.Outcomes) != 5 || len(second.Outcomes) != 5 {
		t.Fatalf("campaign lengths = %d/%d", len(first.Outcomes), len(second.Outcomes))
	}
	for i := range first.Outcomes {
		a, b := first.Outcomes[i], second.Outcomes[i]
		if a.IterationSeed != b.IterationSeed || a.ScheduleID != b.ScheduleID || a.Fingerprint.ID != b.Fingerprint.ID {
			t.Fatalf("iteration %d is not reproducible: %+v != %+v", i, a, b)
		}
		assertCleanedUp(t, *a.Report)
		assertCleanedUp(t, *b.Report)
	}
	direct := run("3")
	if len(direct.Outcomes) != 1 || direct.Outcomes[0].Iteration != 3 ||
		direct.Outcomes[0].IterationSeed != first.Outcomes[3].IterationSeed ||
		direct.Outcomes[0].ScheduleID != first.Outcomes[3].ScheduleID ||
		direct.Outcomes[0].Fingerprint.ID != first.Outcomes[3].Fingerprint.ID {
		t.Fatalf("direct reproduction differs: direct=%+v original=%+v", direct.Outcomes, first.Outcomes[3])
	}
	assertCleanedUp(t, *direct.Outcomes[0].Report)
	hostAfter := captureHostNetworkState(t)
	if hostBefore != hostAfter {
		t.Errorf("host routes, interfaces, or forwarding changed across campaign\nbefore:\n%s\nafter:\n%s", hostBefore, hostAfter)
	}
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
	// The direct-egress row's captive-portal check asks the client's own
	// resolver for this name, and should: it is not the proxied path. What
	// socks5h promises is that the client never *learns* the address — the
	// client's view answers NXDOMAIN and only the proxy resolves it.
	if hasDNSEvidence(rep, "client-dns", "10.77.0.10", proxyProbeName, "ANSWER") {
		t.Errorf("the client's own resolver answered the proxied name: %+v", rep.Evidence.DNS)
	}
	if !hasDNSEvidence(rep, "client-dns", "10.77.0.10", proxyProbeName, "NXDOMAIN") {
		t.Errorf("the client's view of the proxied name is not absent: %+v", rep.Evidence.DNS)
	}
	if !hasDNSEvidence(rep, "proxy", "10.77.0.30", proxyProbeName, "ANSWER") {
		t.Errorf("no proxy-side answer evidence: %+v", rep.Evidence.DNS)
	}
	assertCleanedUp(t, rep)
}

func TestTLSValidScenario(t *testing.T) {
	rep := runTLSScenario(t, "tls-valid")
	out := rep.Tests[0]
	assertTLSCheck(t, out, "PASS", "")
	if tcp := diagnosisCheck(out, string(diagnostic.ProbeTargetTCP)); tcp.Status != "PASS" {
		t.Errorf("target_tcp = %+v", tcp)
	}
	if https := diagnosisCheck(out, string(diagnostic.ProbeHTTPS)); https.Status != "PASS" {
		t.Errorf("https = %+v", https)
	}
	if out.Trust != "tls-target" {
		t.Errorf("trusted service = %q", out.Trust)
	}
	if !hasTLSEvidence(rep, TLSCertificateValid, "secure-target.test", "secure-target.test", true, "passed") {
		t.Errorf("no successful valid handshake evidence: %+v", rep.Evidence.TLS)
	}
	assertCleanedUp(t, rep)
}

func TestTLSExpiredCertificateScenario(t *testing.T) {
	rep := runTLSScenario(t, "tls-expired-certificate")
	out := rep.Tests[0]
	assertTLSCheck(t, out, "FAIL", diagnostic.TLSCauseCertificateExpired)
	if tcp := diagnosisCheck(out, string(diagnostic.ProbeTargetTCP)); tcp.Status != "PASS" {
		t.Errorf("target_tcp = %+v", tcp)
	}
	if !hasTLSEvidence(rep, TLSCertificateExpired, "secure-target.test", "secure-target.test", true, "client_rejected_certificate") {
		t.Errorf("no expired certificate rejection evidence: %+v", rep.Evidence.TLS)
	}
	for _, item := range rep.Evidence.TLS {
		if item.CertificateMode == TLSCertificateExpired && !item.NotAfter.Before(rep.StartedAt) {
			t.Errorf("expired NotAfter %s is not before evaluation %s", item.NotAfter, rep.StartedAt)
		}
	}
	assertCleanedUp(t, rep)
}

func TestTLSHostnameMismatchScenario(t *testing.T) {
	rep := runTLSScenario(t, "tls-hostname-mismatch")
	out := rep.Tests[0]
	assertTLSCheck(t, out, "FAIL", diagnostic.TLSCauseHostnameMismatch)
	if tcp := diagnosisCheck(out, string(diagnostic.ProbeTargetTCP)); tcp.Status != "PASS" {
		t.Errorf("target_tcp = %+v", tcp)
	}
	if !hasTLSEvidence(rep, TLSCertificateHostnameMismatch, "secure-target.test", "different-target.test", true, "client_rejected_certificate") {
		t.Errorf("no hostname mismatch evidence: %+v", rep.Evidence.TLS)
	}
	assertCleanedUp(t, rep)
}

func TestHealthyRoutedNetworkScenario(t *testing.T) {
	requireBackend(t)
	hostForwardingBefore, err := os.ReadFile(ipv4ForwardPath)
	if err != nil {
		t.Fatalf("read host forwarding before scenario: %v", err)
	}
	rep := runScenario(t, "healthy-routed-network")
	hostForwardingAfter, err := os.ReadFile(ipv4ForwardPath)
	if err != nil {
		t.Fatalf("read host forwarding after scenario: %v", err)
	}
	if string(hostForwardingBefore) != string(hostForwardingAfter) {
		t.Errorf("host IPv4 forwarding changed from %q to %q", hostForwardingBefore, hostForwardingAfter)
	}
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); suggestions: %+v", rep.Result, rep.Error, rep.Suggestions)
	}
	if !hasLink(rep, "client", "client-lan", true) || !hasLink(rep, "target", "upstream", true) {
		t.Errorf("client and target were not proven on distinct live segments: %+v", rep.Evidence.Links)
	}
	if countNodeLinks(rep, "gateway") != 2 || !hasForwarding(rep, "gateway") {
		t.Errorf("gateway topology/forwarding evidence = links %+v routers %+v", rep.Evidence.Links, rep.Evidence.Routers)
	}
	if !hasSelectedRoute(rep, "client", "10.77.2.20", "10.77.1.1", "client-lan", nil) {
		t.Errorf("no selected routed target path: %+v", rep.Evidence.Routes)
	}
	if target := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeTargetTCP)); target.Status != "PASS" {
		t.Errorf("target_tcp = %+v", target)
	}
	if rep.Tests[0].FalsePositives != 0 || rep.Tests[0].FalseNegatives != 0 {
		t.Errorf("comparison fp=%d fn=%d", rep.Tests[0].FalsePositives, rep.Tests[0].FalseNegatives)
	}
	assertCleanedUp(t, rep)
}

func TestGatewayUnreachableScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "gateway-unreachable")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); suggestions: %+v; routes: %+v", rep.Result, rep.Error, rep.Suggestions, rep.Evidence.Routes)
	}
	unreachable := false
	if !hasSelectedRoute(rep, "client", "1.1.1.1", "10.77.1.254", "client-lan", &unreachable) {
		t.Errorf("dead gateway selection/neighbor failure not proven: %+v", rep.Evidence.Routes)
	}
	if !hasDefaultRoute(rep, "client") {
		t.Error("default route was absent from topology report")
	}
	if check := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)); check.Status != "FAIL" {
		t.Errorf("internet_tcp = %+v", check)
	} else if check.Cause != diagnostic.RouteCauseGatewayUnreachable {
		t.Errorf("internet_tcp cause = %q", check.Cause)
	}
	assertCleanedUp(t, rep)
}

func TestWrongDefaultRouteScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "wrong-default-route")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests: %+v; suggestions: %+v; routes: %+v", rep.Result, rep.Error, rep.Tests, rep.Suggestions, rep.Evidence.Routes)
	}
	reachable := true
	if !hasSelectedRoute(rep, "client", "1.1.1.1", "10.77.1.254", "client-lan", &reachable) {
		t.Errorf("wrong but locally reachable gateway not selected: %+v", rep.Evidence.Routes)
	}
	if len(rep.Tests) != 2 || diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)).Status != "WARN" ||
		diagnosisCheck(rep.Tests[1], string(diagnostic.ProbeTargetTCP)).Status != "PASS" {
		t.Errorf("wrong/default and correct/specific paths were not distinguished: %+v", rep.Tests)
	}
	if cause := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)).Cause; cause != diagnostic.RouteCauseSelectedPathFailed {
		t.Errorf("wrong default cause = %q", cause)
	}
	assertCleanedUp(t, rep)
}

func TestMultipleInterfacesWrongPreferredRouteScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "multiple-interfaces-wrong-preferred-route")
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); tests: %+v; suggestions: %+v; routes: %+v", rep.Result, rep.Error, rep.Tests, rep.Suggestions, rep.Evidence.Routes)
	}
	if countNodeLinks(rep, "client") != 2 || !hasLink(rep, "client", "working-lan", true) || !hasLink(rep, "client", "wrong-lan", true) {
		t.Errorf("client link evidence = %+v", rep.Evidence.Links)
	}
	reachable := true
	if !hasSelectedRoute(rep, "client", "1.1.1.1", "10.77.3.1", "wrong-lan", &reachable) {
		t.Errorf("lower-metric wrong route was not selected: %+v", rep.Evidence.Routes)
	}
	if !hasGatewayState(rep, "client", "10.77.1.1", true) || !hasGatewayState(rep, "client", "10.77.3.1", true) {
		t.Errorf("both gateway neighbor states were not reachable: %+v", rep.Evidence.Routes)
	}
	if len(rep.Tests) != 2 || diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)).Status != "WARN" ||
		diagnosisCheck(rep.Tests[1], string(diagnostic.ProbeTargetTCP)).Status != "PASS" || rep.Tests[1].SourceSegment != "working-lan" {
		t.Errorf("preferred failure/alternate success evidence missing: %+v", rep.Tests)
	}
	if cause := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet)).Cause; cause != diagnostic.RouteCausePreferredPathFailed {
		t.Errorf("preferred route cause = %q", cause)
	}
	assertCleanedUp(t, rep)
}

func TestDualStackHealthyScenario(t *testing.T) {
	requireBackend(t)
	before := captureHostNetworkState(t)
	rep := runScenario(t, "dual-stack-healthy")
	after := captureHostNetworkState(t)
	if before != after {
		t.Errorf("host network state changed across dual-stack run\nbefore:\n%s\nafter:\n%s", before, after)
	}
	assertDualStackScenario(t, rep, diagnostic.FamilyReachable, diagnostic.FamilyReachable, "")
	if !hasForwardingFamilies(rep, "gateway", true, true) {
		t.Errorf("dual forwarding evidence = %+v", rep.Evidence.Routers)
	}
	if !hasSelectedFamilyRoute(rep, "client", "1.1.1.1", "ipv4") ||
		!hasSelectedFamilyRoute(rep, "client", "2606:4700:4700::1111", "ipv6") {
		t.Errorf("family route selections = %+v", rep.Evidence.Routes)
	}
}

func TestIPv4WorksIPv6BrokenScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "ipv4-works-ipv6-broken")
	assertDualStackScenario(t, rep, diagnostic.FamilyReachable, diagnostic.FamilyUnreachable, diagnostic.FamilyCauseIPv6Unreachable)
	if len(rep.Faults) != 1 || rep.Faults[0].Family != "ipv6" {
		t.Errorf("IPv6 fault evidence = %+v", rep.Faults)
	}
}

func TestIPv6WorksIPv4BrokenScenario(t *testing.T) {
	requireBackend(t)
	rep := runScenario(t, "ipv6-works-ipv4-broken")
	assertDualStackScenario(t, rep, diagnostic.FamilyUnreachable, diagnostic.FamilyReachable, diagnostic.FamilyCauseIPv4Unreachable)
	if len(rep.Faults) != 1 || rep.Faults[0].Family != "ipv4" {
		t.Errorf("IPv4 fault evidence = %+v", rep.Faults)
	}
}

func assertDualStackScenario(t *testing.T, rep Report, ipv4, ipv6, cause string) {
	t.Helper()
	if rep.Result != ResultPass {
		t.Fatalf("result = %s error=%q tests=%+v suggestions=%+v evidence=%+v", rep.Result, rep.Error, rep.Tests, rep.Suggestions, rep.Evidence)
	}
	if !hasDualStackLink(rep, "client", "client-lan") || !hasDualStackLink(rep, "gateway", "upstream") {
		t.Errorf("dual-stack links = %+v", rep.Evidence.Links)
	}
	check := diagnosisCheck(rep.Tests[0], string(diagnostic.ProbeInternet))
	if check.Families == nil || check.Families.IPv4 != ipv4 || check.Families.IPv6 != ipv6 || check.Cause != cause {
		t.Errorf("internet family diagnosis = %+v, want IPv4=%s IPv6=%s cause=%q", check, ipv4, ipv6, cause)
	}
	dnsName := proxyProbeName
	if rep.Tests[0].Target != "" {
		dnsName = "dual-target.test"
	}
	if !hasDNSQueryType(rep, dnsName, "A") || !hasDNSQueryType(rep, dnsName, "AAAA") {
		t.Errorf("A/AAAA query evidence = %+v", rep.Evidence.DNS)
	}
	if !hasReachabilityFamily(rep, "ipv4", ipv4 == diagnostic.FamilyReachable) ||
		!hasReachabilityFamily(rep, "ipv6", ipv6 == diagnostic.FamilyReachable) {
		t.Errorf("family reachability = %+v", rep.Evidence.Reachability)
	}
	if rep.Tests[0].FalsePositives != 0 || rep.Tests[0].FalseNegatives != 0 {
		t.Errorf("comparison fp=%d fn=%d", rep.Tests[0].FalsePositives, rep.Tests[0].FalseNegatives)
	}
	assertCleanedUp(t, rep)
}

func captureHostNetworkState(t *testing.T) string {
	t.Helper()
	var parts []string
	for _, path := range []string{"/proc/sys/net/ipv4/ip_forward", "/proc/sys/net/ipv6/conf/all/forwarding"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read host network setting %s: %v", path, err)
		}
		parts = append(parts, path+"="+string(raw))
	}
	for _, argv := range [][]string{{"ip", "-4", "route", "show"}, {"ip", "-6", "route", "show"}, {"ip", "-o", "link", "show"}} {
		out, err := exec.Command(argv[0], argv[1:]...).Output()
		if err != nil {
			t.Fatalf("capture host network state %v: %v", argv, err)
		}
		parts = append(parts, strings.Join(argv, " ")+"\n"+string(out))
	}
	return strings.Join(parts, "\n")
}

func runTLSScenario(t *testing.T, name string) Report {
	t.Helper()
	requireBackend(t)
	rep := runScenario(t, name)
	if rep.Result != ResultPass {
		t.Fatalf("result = %s (error %q); suggestions: %+v; evidence: %+v", rep.Result, rep.Error, rep.Suggestions, rep.Evidence)
	}
	if len(rep.Tests) != 1 {
		t.Fatalf("tests = %+v", rep.Tests)
	}
	out := rep.Tests[0]
	if out.FalsePositives != 0 || out.FalseNegatives != 0 {
		t.Fatalf("comparison fp=%d fn=%d", out.FalsePositives, out.FalseNegatives)
	}
	return rep
}

func assertTLSCheck(t *testing.T, out TestOutcome, status, cause string) {
	t.Helper()
	check := diagnosisCheck(out, string(diagnostic.ProbeTLS))
	if check.Status != status || check.Cause != cause {
		t.Errorf("tls = %+v, want status %s cause %q", check, status, cause)
	}
}

func hasTLSEvidence(rep Report, mode, requested, certName string, presented bool, result string) bool {
	for _, item := range rep.Evidence.TLS {
		if item.CertificateMode != mode || item.RequestedServer != requested || item.CertificatePresented != presented || item.Result != result || item.Count < 1 {
			continue
		}
		for _, name := range item.CertificateDNS {
			if name == certName {
				return true
			}
		}
	}
	return false
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

func hasLink(rep Report, node, segment string, up bool) bool {
	for _, item := range rep.Evidence.Links {
		if item.Node == node && item.Segment == segment && item.Up == up {
			return true
		}
	}
	return false
}

func countNodeLinks(rep Report, node string) int {
	count := 0
	for _, item := range rep.Evidence.Links {
		if item.Node == node {
			count++
		}
	}
	return count
}

func hasForwarding(rep Report, node string) bool {
	for _, item := range rep.Evidence.Routers {
		if item.Node == node && item.IPv4Forwarding {
			return true
		}
	}
	return false
}

func hasForwardingFamilies(rep Report, node string, ipv4, ipv6 bool) bool {
	for _, item := range rep.Evidence.Routers {
		if item.Node == node && item.IPv4Forwarding == ipv4 && item.IPv6Forwarding == ipv6 {
			return true
		}
	}
	return false
}

func hasDualStackLink(rep Report, node, segment string) bool {
	for _, item := range rep.Evidence.Links {
		if item.Node == node && item.Segment == segment && item.Up && item.IPv4 != "" && item.IPv6 != "" {
			return true
		}
	}
	return false
}

func hasSelectedFamilyRoute(rep Report, node, destination, family string) bool {
	for _, item := range rep.Evidence.Routes {
		if item.Node == node && item.Destination == destination && item.Family == family && item.Selected {
			return true
		}
	}
	return false
}

func hasDNSQueryType(rep Report, name, queryType string) bool {
	for _, item := range rep.Evidence.DNS {
		if item.Name == name && item.QueryType == queryType && item.Result == "ANSWER" && item.Count > 0 {
			return true
		}
	}
	return false
}

func hasReachabilityFamily(rep Report, family string, reachable bool) bool {
	for _, item := range rep.Evidence.Reachability {
		if item.Family == family && item.Reachable == reachable {
			return true
		}
	}
	return false
}

func hasSelectedRoute(rep Report, node, destination, via, segment string, gateway *bool) bool {
	for _, item := range rep.Evidence.Routes {
		if item.Node != node || item.Destination != destination || item.Via != via || item.Segment != segment || !item.Selected {
			continue
		}
		if gateway == nil || item.GatewayReachable != nil && *item.GatewayReachable == *gateway {
			return true
		}
	}
	return false
}

func hasGatewayState(rep Report, node, via string, want bool) bool {
	for _, item := range rep.Evidence.Routes {
		if item.Node == node && item.Via == via && item.GatewayReachable != nil && *item.GatewayReachable == want {
			return true
		}
	}
	return false
}

func hasDefaultRoute(rep Report, node string) bool {
	for _, item := range rep.Topology {
		if item.Name != node {
			continue
		}
		for _, route := range item.Routes {
			if route.Destination == "default" {
				return true
			}
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
