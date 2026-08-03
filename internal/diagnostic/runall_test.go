// Headless RunAll: DAG completion with the skip cascade, and the egress
// downgrade applied at the end.

package diagnostic

import (
	"context"
	"testing"
	"time"
)

func staticProbe(id ProbeID, deps []ProbeID, status Status) Probe {
	return Probe{ID: id, Name: string(id), Deps: deps, Run: func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
		return ProbeResult{Status: status}
	}}
}

func TestRunAll(t *testing.T) {
	probes := []Probe{
		staticProbe("a", nil, StatusPass),
		staticProbe("b", []ProbeID{"a"}, StatusFail),
		staticProbe("c", []ProbeID{"b"}, StatusPass), // must be skipped: b failed
		staticProbe("d", []ProbeID{"a"}, StatusPass),
	}
	res := RunAll(context.Background(), probes)
	if len(res) != len(probes) {
		t.Fatalf("got %d results, want %d", len(res), len(probes))
	}
	want := map[ProbeID]Status{"a": StatusPass, "b": StatusFail, "c": StatusSkip, "d": StatusPass}
	for id, st := range want {
		if res[id].Status != st {
			t.Errorf("probe %s = %v, want %v", id, res[id].Status, st)
		}
	}
	if res["c"].ID != "c" {
		t.Errorf("skip result ID = %q, want c", res["c"].ID)
	}
}

// blockUntilDone stands in for a probe that never returns on its own: it
// reports whether the runner handed it a deadline, then waits for the context
// to end the run for it. Every probe in the real DAG trusts the runner for this
// bound, so a runner that forgot to set one would hang here instead of passing.
func blockUntilDone(id ProbeID, deps []ProbeID) Probe {
	return Probe{ID: id, Name: string(id), Deps: deps, Run: func(ctx context.Context, _ map[ProbeID]ProbeResult) ProbeResult {
		if _, ok := ctx.Deadline(); !ok {
			return ProbeResult{Status: StatusFail, Detail: "no deadline"}
		}
		<-ctx.Done()
		return ProbeResult{Status: StatusFail, Detail: ctx.Err().Error()}
	}}
}

// The bounded-time guarantee, headless half: every probe runs under its own
// ProbeTimeout, dependents included — they get a fresh deadline, not what's
// left of their parent's.
func TestRunAllBoundsEveryProbe(t *testing.T) {
	orig := ProbeTimeout
	ProbeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { ProbeTimeout = orig })

	// "b" depends on a WARN so it runs rather than skipping: only a probe that
	// actually starts can prove it got a deadline of its own.
	probes := []Probe{
		blockUntilDone("a", nil),
		staticProbe("warn", nil, StatusWarn),
		blockUntilDone("b", []ProbeID{"warn"}),
	}
	start := time.Now()
	res := RunAll(context.Background(), probes)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("RunAll took %v with a %v probe timeout", elapsed, ProbeTimeout)
	}
	for _, id := range []ProbeID{"a", "b"} {
		if got := res[id].Detail; got != context.DeadlineExceeded.Error() {
			t.Errorf("probe %s detail = %q, want %q", id, got, context.DeadlineExceeded)
		}
	}
}

// A cancelled parent (Ctrl-C, or -json watch shutting down) must reach the
// probes: the per-probe timeout is a child of it, never a detached context.
func TestRunAllPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := RunAll(ctx, []Probe{blockUntilDone("a", nil)})
	if got := res["a"].Detail; got != context.Canceled.Error() {
		t.Errorf("detail = %q, want %q", got, context.Canceled)
	}
}

func TestRunAllDowngradesEgress(t *testing.T) {
	probes := []Probe{
		staticProbe(ProbeIface, nil, StatusPass),
		staticProbe(ProbeInternet, []ProbeID{ProbeIface}, StatusFail),
		staticProbe(ProbeDNS, []ProbeID{ProbeIface}, StatusPass),
	}
	res := RunAll(context.Background(), probes)
	if res[ProbeInternet].Status != StatusWarn {
		t.Errorf("internet = %v, want WARN (DNS path works)", res[ProbeInternet].Status)
	}
}
