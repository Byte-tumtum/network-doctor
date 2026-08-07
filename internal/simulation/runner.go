package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// Options tune one simulation run.
type Options struct {
	// Netdoc is the netdoc binary the tests execute. Required.
	Netdoc string
	// ProbeTimeout is passed to netdoc as -timeout, and is what the report uses
	// to tell a probe that answered from one that ran out of time.
	ProbeTimeout time.Duration
	// Repeat runs each test this many times to catch a diagnosis that is not
	// reproducible. Values below 1 mean once.
	Repeat int
	// Keep leaves the environment running after the report is written.
	Keep bool
	// SetupTimeout, TestTimeout and CleanupTimeout bound the three phases. Each
	// falls back to a sane default when zero.
	SetupTimeout   time.Duration
	TestTimeout    time.Duration
	CleanupTimeout time.Duration
	// Log receives a line per privileged command as it runs. Nil is quiet.
	Log io.Writer
}

func (o Options) withDefaults() Options {
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = diagnostic.ProbeTimeout
	}
	if o.Repeat < 1 {
		o.Repeat = 1
	}
	if o.SetupTimeout <= 0 {
		o.SetupTimeout = 30 * time.Second
	}
	if o.TestTimeout <= 0 {
		// Every probe in the DAG could serially spend its budget; give the run
		// enough room that a slow scenario fails on its merits, not the clock.
		o.TestTimeout = 15*time.Second + 8*o.ProbeTimeout
	}
	if o.CleanupTimeout <= 0 {
		o.CleanupTimeout = 15 * time.Second
	}
	return o
}

// Run executes one scenario end to end and always returns a report — setup
// failures, cancellation and panics are reported, not returned as bare errors,
// because a simulation that fell over is itself a result worth printing.
//
// Cleanup runs on every exit path. It gets a context detached from the caller's
// so a cancelled or timed-out run still releases its namespaces.
func Run(ctx context.Context, s *Scenario, b Backend, opts Options) (rep *Report) {
	opts = opts.withDefaults()
	rep = &Report{
		Scenario: s.Name, Description: s.Description, ID: NewID(),
		Backend: b.Name(), StartedAt: time.Now(),
	}
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			rep.Error = fmt.Sprintf("simulator panic: %v", r)
		}
		rep.Duration = time.Since(start)
		rep.finish()
	}()

	if caps := b.Capabilities(ctx); !caps.Supported {
		rep.Error = caps.Reason
		return rep
	}

	setupCtx, cancel := context.WithTimeout(ctx, opts.SetupTimeout)
	env, err := b.Prepare(setupCtx, s, rep.ID)
	cancel()
	// Prepare may return both: a partially built environment still owns
	// namespaces and processes that have to go.
	if env != nil {
		// Registered after the recover defer, so it runs before it: a panic in
		// the test loop tears the environment down first and is reported second.
		// The context is detached from the caller's so a cancelled or timed-out
		// run still releases its namespaces, under a deadline of its own.
		defer func() {
			// A backend that panics while cleaning up must not take the report
			// with it, or the user loses both the diagnosis and any idea what
			// was left behind.
			defer func() {
				if r := recover(); r != nil {
					rep.Cleanup.Errors = append(rep.Cleanup.Errors,
						fmt.Sprintf("cleanup panicked: %v", r))
					rep.Cleanup.Done = false
				}
			}()
			cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), opts.CleanupTimeout)
			defer ccancel()
			rep.Cleanup = env.Cleanup(cctx, opts.Keep)
		}()
		rep.Topology = env.Nodes()
	}
	if err != nil {
		rep.Error = "setup failed: " + textsafe.Clean(err.Error())
		return rep
	}

	faultCtx, cancel := context.WithTimeout(ctx, opts.SetupTimeout)
	rep.Faults, err = env.ApplyFaults(faultCtx, s.Faults)
	cancel()
	if err != nil {
		rep.Error = "fault injection failed: " + textsafe.Clean(err.Error())
		return rep
	}

	// T0. Every scheduled offset is measured from this instant — the moment
	// just before the first netdoc process starts. The timeline was resolved
	// during validation and does not change from here on.
	timeline := timelineFrom(s.Faults)
	t0 := time.Now()
	scheduleCtx, endSchedule := context.WithCancel(ctx)
	sched := startScheduler(scheduleCtx, env, timeline, t0)

	for _, t := range s.Tests {
		rep.Tests = append(rep.Tests, runTest(ctx, env, t, s.Expect, opts, t0))
	}
	// Stop the scheduler and join it before anything else touches the
	// environment: no scheduled event may reach a namespace that evidence
	// collection or cleanup is already working on.
	endSchedule()
	rep.Timeline = sched.wait()
	rep.TimelineID = timelineFingerprint(timeline)

	evidenceCtx, evidenceCancel := context.WithTimeout(ctx, opts.SetupTimeout)
	rep.Evidence, err = env.Evidence(evidenceCtx)
	evidenceCancel()
	if err != nil {
		rep.Error = "collecting evidence failed: " + textsafe.Clean(err.Error())
	} else {
		rep.addReachabilityEvidence()
		rep.addPacketObservations()
		rep.placeEvidenceOnTimeline(t0)
	}
	return rep
}

// placeEvidenceOnTimeline converts the wall clock the node holders recorded
// into offsets from this run's epoch. Holders and director share one machine's
// clock; nothing is ordered by it, it only locates an observation.
func (r *Report) placeEvidenceOnTimeline(t0 time.Time) {
	for i := range r.Evidence.DNSQueries {
		if at := r.Evidence.DNSQueries[i].at; !at.IsZero() {
			r.Evidence.DNSQueries[i].Offset = at.Sub(t0)
		}
	}
}

func (r *Report) addPacketObservations() {
	if len(r.Evidence.PacketConditions) == 0 {
		return
	}
	var samples []time.Duration
	for _, test := range r.Tests {
		if test.Diagnosis == nil {
			continue
		}
		for _, check := range test.Diagnosis.Checks {
			if check.ID != string(diagnostic.ProbeInternet) {
				continue
			}
			for _, attempt := range check.Attempts {
				if attempt.Error == "" && attempt.Ms > 0 {
					samples = append(samples, time.Duration(attempt.Ms)*time.Millisecond)
				}
			}
		}
	}
	if len(samples) == 0 {
		return
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for i := range r.Evidence.PacketConditions {
		r.Evidence.PacketConditions[i].ObservedMinRTT = samples[0]
		r.Evidence.PacketConditions[i].ObservedMaxRTT = samples[len(samples)-1]
		r.Evidence.PacketConditions[i].RTTSamples = len(samples)
	}
}

func (r *Report) addReachabilityEvidence() {
	for _, test := range r.Tests {
		if test.Diagnosis == nil {
			continue
		}
		var internet *DiagnosisCheck
		for _, check := range test.Diagnosis.Checks {
			if check.ID == string(diagnostic.ProbeInternet) {
				copy := check
				internet = &copy
				break
			}
		}
		if internet != nil && internet.Families != nil {
			for _, item := range []struct{ family, state, target string }{
				{"ipv4", internet.Families.IPv4, "IPv4 internet endpoints"},
				{"ipv6", internet.Families.IPv6, "IPv6 internet endpoints"},
			} {
				r.Evidence.Reachability = append(r.Evidence.Reachability, ReachabilityEvidence{
					From: test.Node, To: item.target, Family: item.family,
					Via: r.selectedPath(test.Node, item.family), Reachable: item.state == diagnostic.FamilyReachable,
				})
			}
		}
		if test.Target == "" {
			continue
		}
		status := ""
		for _, check := range test.Diagnosis.Checks {
			if check.ID == string(diagnostic.ProbeTargetTCP) {
				status = check.Status
				break
			}
		}
		if status == "" {
			continue
		}
		destination := test.Target
		via := []string{}
		if test.SourceSegment != "" {
			via = append(via, test.SourceSegment)
		} else {
			for _, route := range r.Evidence.Routes {
				if route.Node == test.Node && route.Selected {
					via = append(via, route.Segment)
					if route.Via != "" {
						via = append(via, route.Via)
					}
					break
				}
			}
		}
		r.Evidence.Reachability = append(r.Evidence.Reachability, ReachabilityEvidence{
			From: test.Node, To: destination, Via: via, Reachable: status == "PASS" || status == "WARN",
		})
	}
}

func (r *Report) selectedPath(node, family string) []string {
	for _, route := range r.Evidence.Routes {
		if route.Node == node && route.Selected && route.Family == family {
			via := []string{route.Segment}
			if route.Via != "" {
				via = append(via, route.Via)
			}
			return via
		}
	}
	return nil
}

// runTest runs netdoc inside a node and compares the diagnosis. Repeats reuse
// the first run for the comparison and contribute only their verdict, so a
// flaky diagnosis shows up as instability rather than as a coin flip.
func runTest(ctx context.Context, env Env, t Test, expect Expect, opts Options, t0 time.Time) (out TestOutcome) {
	if t.Expect != nil {
		expect = *t.Expect
	}
	out = TestOutcome{Name: t.Name, Node: t.Node, Target: t.Target, SourceSegment: t.SourceSegment,
		StartOffset: time.Since(t0)}
	// The window this netdoc process occupied on the fault timeline is what
	// makes "the network changed while this probe ran" answerable at all.
	defer func() { out.EndOffset = time.Since(t0) }()
	argv := []string{opts.Netdoc, "-json", "-timeout", opts.ProbeTimeout.String()}
	if t.SourceSegment != "" {
		// Use the validated canonical address rather than exposing or accepting a
		// generated kernel interface name.
		for _, node := range env.Nodes() {
			if node.Name != t.Node {
				continue
			}
			for _, iface := range node.Interfaces {
				if iface.Segment == t.SourceSegment {
					address, _, _ := strings.Cut(iface.Address, "/")
					argv = append(argv, "-iface", address)
				}
			}
		}
	}
	var commandEnv []string
	if t.Proxy != nil {
		out.Proxy = t.Proxy.Scheme + "://" + net.JoinHostPort(t.Proxy.address, fmt.Sprint(t.Proxy.Port))
		commandEnv = []string{"ALL_PROXY=" + out.Proxy}
	}
	if t.Trust != nil {
		out.Trust = t.Trust.Service
		bundle, err := env.TrustAnchor(t.Trust.Service)
		if err != nil {
			out.Error = textsafe.Clean(err.Error())
			out.compare(expect, opts.ProbeTimeout)
			return out
		}
		commandEnv = append(commandEnv, "SSL_CERT_FILE="+bundle)
	}
	if t.Target != "" {
		argv = append(argv, t.Target)
	}
	out.Command = argv

	for i := 0; i < opts.Repeat; i++ {
		tctx, cancel := context.WithTimeout(ctx, opts.TestTimeout)
		res := env.Exec(tctx, t.Node, argv, commandEnv)
		cancel()
		diag, err := decodeDiagnosis(res)
		if i == 0 {
			out.Duration, out.ExitCode = res.Duration, res.ExitCode
			out.Stderr = strings.TrimSpace(textsafe.Clean(string(res.Stderr)))
			out.Diagnosis = diag
			if err != nil {
				out.Error = textsafe.Clean(err.Error())
			}
		}
		if opts.Repeat > 1 {
			verdict := "<no report>"
			if diag != nil {
				verdict = diag.Verdict
			}
			out.RepeatVerdicts = append(out.RepeatVerdicts, verdict)
		}
		if err != nil {
			break
		}
	}
	out.compare(expect, opts.ProbeTimeout)
	return out
}

// decodeDiagnosis reads netdoc's JSON report. netdoc exits 1 when a check
// failed, which is the normal case in a scenario that broke something on
// purpose — so the exit code is recorded, never treated as an error on its own.
func decodeDiagnosis(res ExecResult) (*Diagnosis, error) {
	if res.Err != nil {
		return nil, fmt.Errorf("running netdoc: %w", res.Err)
	}
	var d Diagnosis
	if err := json.Unmarshal(res.Stdout, &d); err != nil {
		stderr := strings.TrimSpace(string(res.Stderr))
		if stderr != "" {
			return nil, fmt.Errorf("netdoc exited %d without a report: %s", res.ExitCode, stderr)
		}
		return nil, fmt.Errorf("netdoc exited %d and its output is not a report: %w", res.ExitCode, err)
	}
	if len(d.Checks) == 0 {
		return nil, fmt.Errorf("netdoc exited %d with an empty report", res.ExitCode)
	}
	return &d, nil
}
