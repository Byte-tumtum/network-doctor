package simulation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend and fakeEnv stand in for a real backend so the lifecycle
// guarantees — cleanup after a partial setup, a cancellation, a timeout, a
// panic, and cleanup called twice — can be tested without a namespace.
type fakeBackend struct {
	caps       Capabilities
	env        *fakeEnv
	prepareErr error
	// prepareEnvOnErr models a Prepare that got far enough to create resources
	// before failing: it must hand back the half-built environment anyway.
	prepareEnvOnErr bool
}

func (b *fakeBackend) Name() string { return "fake" }

func (b *fakeBackend) Capabilities(context.Context) Capabilities { return b.caps }

func (b *fakeBackend) Prepare(context.Context, *Scenario, string) (Env, error) {
	if b.prepareErr != nil {
		if b.prepareEnvOnErr {
			return b.env, b.prepareErr
		}
		return nil, b.prepareErr
	}
	return b.env, nil
}

type fakeEnv struct {
	cleanups      int
	execs         int
	panicOnExec   bool
	panicOnClean  bool
	stdout        string
	lastEnv       []string
	execCtxErr    error
	timedOut      bool
	cancelled     bool
	signal        string
	sawCancelled  bool
	cleanupErrors []string

	mu      sync.Mutex
	applied []TimedEvent
	// applyErr, when set, fails every scheduled event.
	applyErr error
	// applyHold blocks inside a scheduled apply, so a test can cancel while one
	// is in flight.
	applyHold chan struct{}
	// cleanedAt records when Cleanup ran, so a late event can be caught.
	cleanedAt  time.Time
	lateEvents int
	// execBeforeInitial catches a run that starts netdoc before an offset-zero
	// scheduled state has reached the environment.
	execBeforeInitial bool
}

func (e *fakeEnv) Nodes() []NodeInfo { return []NodeInfo{{Name: "client", Address: "10.77.0.10"}} }

func (e *fakeEnv) ApplyFaults(context.Context, []Fault) ([]FaultInfo, error) { return nil, nil }

func (e *fakeEnv) ApplyTimedEvent(ctx context.Context, event TimedEvent) (string, error) {
	if e.applyHold != nil {
		<-e.applyHold
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cleanedAt.IsZero() {
		e.lateEvents++
	}
	e.applied = append(e.applied, event)
	return "fake", e.applyErr
}

func (e *fakeEnv) appliedEvents() []TimedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]TimedEvent(nil), e.applied...)
}

func (e *fakeEnv) Exec(ctx context.Context, _ string, _ []string, env []string) ExecResult {
	e.execs++
	e.lastEnv = append([]string(nil), env...)
	e.mu.Lock()
	if len(e.applied) == 0 {
		e.execBeforeInitial = true
	}
	e.mu.Unlock()
	if e.panicOnExec {
		panic("probe exploded")
	}
	if ctx.Err() != nil {
		e.sawCancelled = true
		return ExecResult{Err: ctx.Err(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
			Cancelled: !errors.Is(ctx.Err(), context.DeadlineExceeded)}
	}
	return ExecResult{Stdout: []byte(e.stdout), Err: e.execCtxErr, TimedOut: e.timedOut,
		Cancelled: e.cancelled, Signal: e.signal}
}

func (e *fakeEnv) TrustAnchor(service string) (string, error) {
	if service == "tls-target" {
		return "/simulator/tls-target-ca.pem", nil
	}
	return "", errors.New("unknown trust anchor")
}

func TestRunPassesOnlyValidatedProxyEnvironment(t *testing.T) {
	raw := strings.Replace(minimalScenario, "address: 10.77.0.1}",
		"address: 10.77.0.1, resolver: 10.77.0.1, services: [{name: proxy, type: socks5, port: 1080}]}", 1)
	raw = strings.Replace(raw, "target: example.test:80", "target: example.test:80, proxy: {scheme: socks5h, node: server, port: 1080}", 1)
	s, err := ParseScenario(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	env := &fakeEnv{stdout: okReport}
	Run(context.Background(), s, &fakeBackend{caps: supported(), env: env}, Options{Netdoc: "netdoc"})
	if len(env.lastEnv) != 1 || env.lastEnv[0] != "ALL_PROXY=socks5h://10.77.0.1:1080" {
		t.Errorf("env = %q", env.lastEnv)
	}
}

func TestRunPassesOnlyGeneratedTLSRootEnvironment(t *testing.T) {
	raw := strings.Replace(minimalScenario, "address: 10.77.0.1}",
		"address: 10.77.0.1, services: [{name: tls-target, type: tls, port: 9443, certificate: {mode: valid, dns_names: [example.test]}}]}", 1)
	raw = strings.Replace(raw, "target: example.test:80", "target: example.test:80, trust: {service: tls-target}", 1)
	s, err := ParseScenario(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	env := &fakeEnv{stdout: okReport}
	Run(context.Background(), s, &fakeBackend{caps: supported(), env: env}, Options{Netdoc: "netdoc"})
	if len(env.lastEnv) != 1 || env.lastEnv[0] != "SSL_CERT_FILE=/simulator/tls-target-ca.pem" {
		t.Errorf("env = %q", env.lastEnv)
	}
}

func (e *fakeEnv) Evidence(context.Context) (Evidence, error) { return aggregateEvidence(nil), nil }

func (e *fakeEnv) Cleanup(ctx context.Context, keep bool) CleanupInfo {
	e.mu.Lock()
	e.cleanedAt = time.Now()
	e.mu.Unlock()
	e.cleanups++
	if e.panicOnClean {
		panic("teardown exploded")
	}
	// The contract the real backend has to honour: cleanup runs under a context
	// that the caller's cancellation does not reach.
	if ctx.Err() != nil {
		e.cleanupErrors = append(e.cleanupErrors, "cleanup ran with an already-cancelled context")
	}
	return CleanupInfo{Done: len(e.cleanupErrors) == 0, Kept: keep, Errors: e.cleanupErrors}
}

const okReport = `{"checks":[{"id":"dns","status":"PASS","detail":"d"}],"verdict":"ok","summary":"s","ok":true}`

func testScenario(t *testing.T) *Scenario {
	t.Helper()
	s, err := ParseScenario(strings.NewReader(minimalScenario))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func supported() Capabilities { return Capabilities{Backend: "fake", Supported: true} }

func TestRunCleansUpAfterPartialSetupFailure(t *testing.T) {
	env := &fakeEnv{}
	b := &fakeBackend{caps: supported(), env: env, prepareErr: errors.New("veth wedged"), prepareEnvOnErr: true}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc"})

	if env.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1: a half-built environment still owns resources", env.cleanups)
	}
	if env.execs != 0 {
		t.Error("no test should run after setup failed")
	}
	if rep.Result != ResultError || !strings.Contains(rep.Error, "veth wedged") {
		t.Errorf("result = %s, error = %q", rep.Result, rep.Error)
	}
	// The topology that did get built is still worth reporting.
	if len(rep.Topology) != 1 {
		t.Errorf("topology = %v", rep.Topology)
	}
}

func TestRunCleansUpWhenPrepareBuiltNothing(t *testing.T) {
	env := &fakeEnv{}
	b := &fakeBackend{caps: supported(), env: env, prepareErr: errors.New("nope")}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc"})
	if env.cleanups != 0 {
		t.Errorf("cleanups = %d, want 0: there was no environment", env.cleanups)
	}
	if rep.Result != ResultError {
		t.Errorf("result = %s", rep.Result)
	}
}

func TestRunCleansUpAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := &fakeEnv{stdout: okReport}
	b := &fakeBackend{caps: supported(), env: env}
	rep := Run(ctx, testScenario(t), b, Options{Netdoc: "netdoc"})

	if env.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1", env.cleanups)
	}
	// The whole point of detaching cleanup's context: it must not arrive
	// already cancelled, or the backend could not run a single command.
	if len(env.cleanupErrors) > 0 {
		t.Errorf("cleanup context: %v", env.cleanupErrors)
	}
	if !rep.Cleanup.Done {
		t.Errorf("cleanup not done: %+v", rep.Cleanup)
	}
}

func TestRunCleansUpAfterTimeout(t *testing.T) {
	env := &fakeEnv{stdout: okReport}
	b := &fakeBackend{caps: supported(), env: env}
	rep := Run(context.Background(), testScenario(t), b, Options{
		Netdoc: "netdoc", TestTimeout: time.Nanosecond, CleanupTimeout: 5 * time.Second,
	})
	if env.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1", env.cleanups)
	}
	if len(env.cleanupErrors) > 0 {
		t.Errorf("cleanup context: %v", env.cleanupErrors)
	}
	if !rep.Cleanup.Done {
		t.Errorf("cleanup not done: %+v", rep.Cleanup)
	}
}

func TestRunCleansUpAfterPanic(t *testing.T) {
	env := &fakeEnv{panicOnExec: true}
	b := &fakeBackend{caps: supported(), env: env}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc"})

	if env.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1: a panicking probe must not leak namespaces", env.cleanups)
	}
	if rep.Result != ResultError || !strings.Contains(rep.Error, "probe exploded") {
		t.Errorf("result = %s, error = %q", rep.Result, rep.Error)
	}
	if !rep.Cleanup.Done {
		t.Errorf("cleanup should still have completed: %+v", rep.Cleanup)
	}
}

func TestRunSurvivesPanickingCleanup(t *testing.T) {
	env := &fakeEnv{stdout: okReport, panicOnClean: true}
	b := &fakeBackend{caps: supported(), env: env}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc"})

	if rep.Cleanup.Done {
		t.Error("a panicking cleanup must not report success")
	}
	if len(rep.Cleanup.Errors) == 0 || !strings.Contains(rep.Cleanup.Errors[0], "teardown exploded") {
		t.Errorf("cleanup errors = %v", rep.Cleanup.Errors)
	}
	// The diagnosis still has to survive: losing the report as well would leave
	// the user with nothing.
	if len(rep.Tests) != 1 || rep.Tests[0].ActualVerdict != "ok" {
		t.Errorf("tests = %+v", rep.Tests)
	}
}

func TestRunRefusesUnsupportedHostWithoutFallback(t *testing.T) {
	env := &fakeEnv{}
	b := &fakeBackend{caps: Capabilities{Backend: "fake", Reason: "user namespaces are off"}, env: env}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc"})

	if rep.Result != ResultError || rep.Error != "user namespaces are off" {
		t.Errorf("result = %s, error = %q", rep.Result, rep.Error)
	}
	if env.execs != 0 {
		t.Error("nothing may run on a host that cannot simulate — no fallback to the host network")
	}
}

func TestRunRecordsRepeatVerdicts(t *testing.T) {
	env := &fakeEnv{stdout: okReport}
	b := &fakeBackend{caps: supported(), env: env}
	rep := Run(context.Background(), testScenario(t), b, Options{Netdoc: "netdoc", Repeat: 3})
	if env.execs != 3 {
		t.Errorf("execs = %d, want 3", env.execs)
	}
	if len(rep.Tests[0].RepeatVerdicts) != 3 {
		t.Errorf("repeat verdicts = %v", rep.Tests[0].RepeatVerdicts)
	}
}

func TestDecodeDiagnosisRejectsNonReports(t *testing.T) {
	tests := []struct {
		name string
		res  ExecResult
		want string
	}{
		{"exec failed", ExecResult{Err: errors.New("no such process")}, "running netdoc"},
		{"stderr only", ExecResult{Stderr: []byte("netdoc: bad target"), ExitCode: 2}, "without a report"},
		{"garbage", ExecResult{Stdout: []byte("not json"), ExitCode: 1}, "not a report"},
		{"empty report", ExecResult{Stdout: []byte(`{"checks":[]}`)}, "empty report"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeDiagnosis(tc.res)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// netdoc exits 1 whenever a check failed, which is the normal case in a
	// scenario that broke something. That must not read as a broken run.
	d, err := decodeDiagnosis(ExecResult{Stdout: []byte(okReport), ExitCode: 1})
	if err != nil || d.Verdict != "ok" {
		t.Errorf("d = %+v, err = %v", d, err)
	}
}
