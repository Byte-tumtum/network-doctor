package diagnostic

import "context"

// DepsState reports whether all deps completed (ready) and whether any completed
// dep blocks this probe. A dep blocks on Fail or Skip (no output); a Pass, a
// Warn (degraded but produced output), or an applicable NotApplicable satisfies.
func DepsState(deps []ProbeID, res map[ProbeID]ProbeResult) (ready, blocked bool) {
	for _, d := range deps {
		r, ok := res[d]
		if !ok {
			return false, false
		}
		if r.Status == StatusFail || r.Status == StatusSkip {
			blocked = true
		}
	}
	return true, blocked
}

// SkipPrereq is the result recorded for a probe DepsState reports as blocked.
func SkipPrereq(id ProbeID) ProbeResult {
	return ProbeResult{ID: id, Status: StatusSkip, Detail: "skipped: a prerequisite failed"}
}

// RunAll executes the probe DAG headlessly with the same semantics as the TUI
// scheduler: every ready probe runs in parallel under its own ProbeTimeout, a
// failed or skipped prerequisite skips its dependents, and the egress
// downgrade is applied once every probe has a result.
func RunAll(ctx context.Context, probes []Probe) map[ProbeID]ProbeResult {
	results := make(map[ProbeID]ProbeResult, len(probes))
	started := make(map[ProbeID]bool, len(probes))
	done := make(chan ProbeResult)
	running := 0

	// schedule launches every probe whose deps all have results. Runs to a
	// fixpoint because starting one probe can make another ready, and skips
	// cascade synchronously (a skip is a result too), so a chain of doomed
	// probes resolves in a single call without ever spawning a goroutine.
	schedule := func() {
		for progress := true; progress; {
			progress = false
			for _, p := range probes {
				if started[p.ID] {
					continue
				}
				ready, blocked := DepsState(p.Deps, results)
				if !ready {
					continue
				}
				started[p.ID] = true
				progress = true
				if blocked {
					results[p.ID] = SkipPrereq(p.ID)
					continue
				}
				// Snapshot the dep results before spawning: the goroutine
				// must not touch the live results map, which this (single)
				// scheduling loop keeps mutating.
				deps := make(map[ProbeID]ProbeResult, len(p.Deps))
				for _, d := range p.Deps {
					deps[d] = results[d]
				}
				running++
				go func(p Probe, deps map[ProbeID]ProbeResult) {
					pctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
					defer cancel()
					res := p.Run(pctx, deps)
					res.ID = p.ID
					done <- res
				}(p, deps)
			}
		}
	}

	// Seed the roots, then drain: each finished probe may unlock more, so
	// reschedule after every receive. running is our only bookkeeping: when
	// it hits zero there's nothing in flight and nothing left to start, since
	// schedule already ran after the final result. Probes that time out still
	// send a result, so this can't hang; ctx cancellation just makes everyone
	// finish early and grumpy.
	schedule()
	for running > 0 {
		res := <-done
		results[res.ID] = res
		running--
		schedule()
	}
	Finalize(results)
	return results
}
