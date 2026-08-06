package simulation

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func campaignScenario(t *testing.T) *Scenario {
	t.Helper()
	s, err := LibraryScenario("unstable-connectivity")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIterationSeedDerivationIsIndependent(t *testing.T) {
	const seed int64 = -9223372036854775000
	want := DeriveIterationSeed(seed, "unstable-connectivity", 37)
	for i := 0; i < 37; i++ {
		_ = DeriveIterationSeed(seed, "unstable-connectivity", i)
	}
	if got := DeriveIterationSeed(seed, "unstable-connectivity", 37); got != want {
		t.Fatalf("iteration 37 changed after deriving predecessors: %d != %d", got, want)
	}
	if DeriveIterationSeed(seed+1, "unstable-connectivity", 37) == want {
		t.Error("different campaign seed produced the same iteration seed")
	}
}

func TestCampaignScheduleDeterminismAndBounds(t *testing.T) {
	s := campaignScenario(t)
	seed := DeriveIterationSeed(12345, s.Name, 7)
	_, first, err := compileCampaignIteration(s, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := compileCampaignIteration(s, seed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed schedule differs:\n%+v\n%+v", first, second)
	}
	if scheduleFingerprint(first) != scheduleFingerprint(second) {
		t.Error("same schedule has different fingerprint")
	}
	for _, event := range first {
		if event.Type == FaultNetem {
			if event.LossPercent < 0 || event.LossPercent > 20 || event.Latency < 0 || event.Jitter < 0 || event.NetemSeed == 0 {
				t.Errorf("out-of-range netem event: %+v", event)
			}
		}
	}
	_, different, err := compileCampaignIteration(s, seed+1)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first, different) {
		t.Error("different iteration seed produced the same full schedule")
	}
}

func TestDiagnosisFingerprintCanonicalizesProbeOrder(t *testing.T) {
	checks := []DiagnosisCheck{
		{ID: "dns", Status: "PASS"},
		{ID: "internet_tcp", Status: "WARN", Cause: "ipv6_unreachable", Families: &DiagnosisFamilies{IPv4: "reachable", IPv6: "unreachable"}},
	}
	a := &Report{Tests: []TestOutcome{{Name: "one", Diagnosis: &Diagnosis{Verdict: "degraded", Checks: checks}}}}
	b := &Report{Tests: []TestOutcome{{Name: "one", Diagnosis: &Diagnosis{Verdict: "degraded", Checks: []DiagnosisCheck{checks[1], checks[0]}}}}}
	fa, fb := diagnosisFingerprint(a), diagnosisFingerprint(b)
	if fa.ID != fb.ID || !reflect.DeepEqual(fa, fb) {
		t.Fatalf("order changed fingerprint: %+v != %+v", fa, fb)
	}
	blob, err := json.Marshal(fa)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip DiagnosisFingerprint
	if err := json.Unmarshal(blob, &roundTrip); err != nil || roundTrip.ID != fa.ID {
		t.Fatalf("JSON changed fingerprint: %v %+v", err, roundTrip)
	}
}

func TestCampaignFailFastAndReproduction(t *testing.T) {
	s := campaignScenario(t)
	created := 0
	result := RunCampaign(context.Background(), s, func() Backend {
		created++
		return &fakeBackend{caps: supported(), env: &fakeEnv{stdout: okReport}}
	}, CampaignOptions{Seed: 12345, Runs: 5, FailFast: true, Run: Options{Netdoc: "netdoc"}})
	if created != 1 || result.Runs != 1 || result.FirstFailure == nil {
		t.Fatalf("created=%d result=%+v", created, result)
	}
	if result.FirstFailure.Seed != 12345 || result.FirstFailure.Iteration != 0 {
		t.Errorf("reproduction = %+v", result.FirstFailure)
	}
	var text bytes.Buffer
	result.WriteText(&text)
	if !bytes.Contains(text.Bytes(), []byte("--seed 12345 --iteration 0")) {
		t.Errorf("no direct reproduction command:\n%s", text.String())
	}
}

func TestCampaignCancellationStartsNoIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	created := 0
	result := RunCampaign(ctx, campaignScenario(t), func() Backend { created++; return nil }, CampaignOptions{Seed: 1, Runs: 3})
	if created != 0 || !result.Cancelled || result.Result != ResultError {
		t.Fatalf("created=%d result=%+v", created, result)
	}
}

func TestCampaignAggregateCountsDivergentEquivalentSchedules(t *testing.T) {
	r := &CampaignResult{Runs: 2, Failed: 1, Outcomes: []IterationResult{
		{ScheduleID: "same", Fingerprint: DiagnosisFingerprint{ID: "a"}, Report: &Report{Duration: 3}},
		{ScheduleID: "same", Fingerprint: DiagnosisFingerprint{ID: "b"}, Report: &Report{Duration: 7}},
	}}
	r.finish()
	if r.DivergentRuns != 2 || r.StableRuns != 0 || len(r.Fingerprints) != 2 || r.Result != ResultFail {
		t.Fatalf("aggregate = %+v", r)
	}
}
