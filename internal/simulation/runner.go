package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

	for _, t := range s.Tests {
		rep.Tests = append(rep.Tests, runTest(ctx, env, t, s.Expect, opts))
	}
	rep.Evidence, err = env.Evidence()
	if err != nil {
		rep.Error = "collecting evidence failed: " + textsafe.Clean(err.Error())
	}
	return rep
}

// runTest runs netdoc inside a node and compares the diagnosis. Repeats reuse
// the first run for the comparison and contribute only their verdict, so a
// flaky diagnosis shows up as instability rather than as a coin flip.
func runTest(ctx context.Context, env Env, t Test, expect Expect, opts Options) TestOutcome {
	out := TestOutcome{Name: t.Name, Node: t.Node, Target: t.Target}
	argv := []string{opts.Netdoc, "-json", "-timeout", opts.ProbeTimeout.String()}
	var commandEnv []string
	if t.Proxy != nil {
		out.Proxy = t.Proxy.Scheme + "://" + net.JoinHostPort(t.Proxy.address, fmt.Sprint(t.Proxy.Port))
		commandEnv = []string{"ALL_PROXY=" + out.Proxy}
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
