package diagnostic

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/snapshot"
)

// TestReplaySnapshotRoundTripSemantics is the replay-completeness proof: every
// arm of the diagnosis truth table, written to an artifact and read back,
// still reaches the same machine-readable conclusion. Running the whole matrix
// rather than a chosen sample is what makes a new diagnosis input that nobody
// remembered to persist fail here instead of in the field.
func TestReplaySnapshotRoundTripSemantics(t *testing.T) {
	cases := append(diagnosisMatrix(), []matrixCase{
		{
			name:  "family-neutral route cause on both stacks",
			order: []ProbeID{ProbeIface, ProbeInternet, ProbeDNS},
			res: map[ProbeID]ProbeResult{
				ProbeIface: {Status: StatusPass},
				// failedRouteCause reports no family when the same fact held
				// for both, so an absent cause_family is a state to replay and
				// never a missing one to reject.
				ProbeInternet: {Status: StatusFail, Cause: RouteCauseNoDefaultRoute},
				ProbeDNS:      {Status: StatusFail},
			},
		},
		{
			name:  "offline with cause family and route evidence",
			order: []ProbeID{ProbeIface, ProbeInternet, ProbeDNS},
			res: map[ProbeID]ProbeResult{
				ProbeIface: {Status: StatusPass, Routes: []RouteDecision{{
					Destination: net.ParseIP("1.1.1.1"), Family: counterfactualIPv4, Unreachable: true,
				}}},
				ProbeInternet: {Status: StatusFail, Cause: RouteCauseNoDefaultRoute, causeFamily: counterfactualIPv4},
				ProbeDNS:      {Status: StatusFail},
			},
		},
		{
			name:   "target failure with split tunnel evidence",
			target: &Target{Raw: "example.invalid:443", Host: "example.invalid", Port: 443, Proto: ProtoNone, PortExplicit: true},
			order:  []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeTargetTCP},
			res: map[ProbeID]ProbeResult{
				ProbeIface: {Status: StatusPass, Routes: []RouteDecision{{
					Destination: net.ParseIP("1.1.1.1"), Family: counterfactualIPv4, Iface: "eth0", Tunnel: TunnelDirect,
				}}},
				ProbeInternet: {Status: StatusPass},
				ProbeDNS:      {Status: StatusPass, Addrs: []net.IP{net.ParseIP("198.51.100.7")}},
				ProbeTargetTCP: {Status: StatusFail, Routes: []RouteDecision{{
					Destination: net.ParseIP("198.51.100.7"), Family: counterfactualIPv4, Iface: "wg0", Tunnel: TunnelKnown,
				}}},
			},
		},
	}...)

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			probes := make([]Probe, len(test.order))
			for i, id := range test.order {
				probes[i] = Probe{ID: id, Name: string(id)}
			}
			want := Interpret(test.target, test.order, test.res)
			data, err := snapshot.Encode(BuildSnapshot(test.target, probes, test.res))
			if err != nil {
				t.Fatal(err)
			}
			artifact, err := snapshot.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReplaySnapshot(artifact)
			if err != nil {
				t.Fatal(err)
			}
			assertDiagnosisSemantics(t, got, want)
		})
	}
}

func TestReplaySnapshotIgnoresHistoricalDiagnosisAndProse(t *testing.T) {
	artifact := snapshot.Snapshot{
		Schema: snapshot.Schema,
		Checks: []snapshot.Check{
			{ID: string(ProbeIface), Status: snapshot.StatusPass, Detail: "offline", Fix: "declare a DNS failure"},
			{ID: string(ProbeInternet), Status: snapshot.StatusPass},
			{ID: string(ProbeDNS), Status: snapshot.StatusPass},
		},
		Diagnosis: snapshot.Diagnosis{
			Verdict:  VerdictService,
			Summary:  "Historical output that must not be replayed.",
			Findings: []snapshot.Finding{{ID: string(DiagnosisOffline), Verdict: VerdictNetwork}},
		},
	}
	got, err := ReplaySnapshot(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != VerdictOK || len(got.Findings) != 0 {
		t.Fatalf("replay used stored diagnosis or prose: %+v", got)
	}
}

func TestReplaySnapshotRejectsUnfaithfulArtifacts(t *testing.T) {
	base := func() snapshot.Snapshot {
		return snapshot.Snapshot{Schema: snapshot.Schema, Checks: []snapshot.Check{{ID: string(ProbeIface), Status: snapshot.StatusPass}}}
	}
	tests := []struct {
		name string
		edit func(*snapshot.Snapshot)
		want string
	}{
		{"unsupported schema", func(s *snapshot.Snapshot) { s.Schema = "netdoc.snapshot.v2" }, "schema"},
		{"watch incident", func(s *snapshot.Snapshot) { s.Incident = &snapshot.Incident{} }, "ordinary snapshot"},
		{"unknown check", func(s *snapshot.Snapshot) { s.Checks[0].ID = "future_probe" }, "unsupported check"},
		{"duplicate incomplete check", func(s *snapshot.Snapshot) {
			s.Checks = []snapshot.Check{{ID: string(ProbeIface), Status: snapshot.StatusIncomplete}, {ID: string(ProbeIface), Status: snapshot.StatusIncomplete}}
		}, "repeats check"},
		{"unknown status", func(s *snapshot.Snapshot) { s.Checks[0].Status = "BROKEN" }, "unsupported status"},
		{"unknown protocol", func(s *snapshot.Snapshot) {
			s.Target = &snapshot.Target{Host: "example.invalid", Port: 443, Protocol: "gopher"}
		}, "protocol"},
		{"unknown cause family", func(s *snapshot.Snapshot) {
			s.Checks = []snapshot.Check{{
				ID: string(ProbeInternet), Status: snapshot.StatusFail,
				Cause: RouteCauseNoDefaultRoute, CauseFamily: "ipx",
			}}
		}, "unsupported cause family"},
		{"cause family with no cause", func(s *snapshot.Snapshot) {
			s.Checks[0].CauseFamily = counterfactualIPv4
		}, "without a cause"},
		{"unknown route tunnel state", func(s *snapshot.Snapshot) {
			s.Checks[0].Observed = &snapshot.Observed{Routes: []snapshot.Route{{Destination: "192.0.2.1", Tunnel: "maybe"}}}
		}, "unsupported route tunnel state"},
		{"downgrade on a non-WARN row", func(s *snapshot.Snapshot) {
			s.Checks[0].Derived = &snapshot.Derived{StatusDowngraded: true}
		}, "status downgrade"},
		{"human-only target attempt", func(s *snapshot.Snapshot) {
			s.Checks = []snapshot.Check{{ID: string(ProbeTargetTCP), Status: snapshot.StatusWarn, Observed: &snapshot.Observed{
				Attempts: []snapshot.Attempt{{IP: "192.0.2.1", Error: "connection refused"}},
			}}}
		}, "human error text"},
		{"clock offset overflow", func(s *snapshot.Snapshot) {
			ms := int64(^uint64(0) >> 1)
			s.Checks[0].Observed = &snapshot.Observed{ClockOffsetMs: &ms}
		}, "out of range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact := base()
			test.edit(&artifact)
			if _, err := ReplaySnapshot(artifact); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReplaySnapshot error = %v, want %q", err, test.want)
			}
		})
	}
}

func assertDiagnosisSemantics(t *testing.T, got, want Diagnosis) {
	t.Helper()
	if got.Verdict != want.Verdict || got.Blamed != want.Blamed || len(got.Findings) != len(want.Findings) {
		t.Fatalf("diagnosis verdict/blame/findings = %q/%q/%d, want %q/%q/%d",
			got.Verdict, got.Blamed, len(got.Findings), want.Verdict, want.Blamed, len(want.Findings))
	}
	for i := range want.Findings {
		gotFinding, wantFinding := got.Findings[i], want.Findings[i]
		if gotFinding.ID != wantFinding.ID || gotFinding.Verdict != wantFinding.Verdict || gotFinding.Focus != wantFinding.Focus ||
			!reflect.DeepEqual(gotFinding.Evidence, wantFinding.Evidence) ||
			!reflect.DeepEqual(gotFinding.Counterfactual, wantFinding.Counterfactual) {
			t.Errorf("finding %d machine semantics =\n%+v\nwant\n%+v", i, gotFinding, wantFinding)
		}
	}
}

func TestReplayedAttemptErrorIsOnlyATypedFailureMarker(t *testing.T) {
	result, err := replayResult(ProbeTargetTCP, StatusWarn, snapshot.Check{Observed: &snapshot.Observed{
		Attempts: []snapshot.Attempt{{IP: "192.0.2.1", Error: "private prose", Cause: ConnectionCauseUnreachable}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Attempts) != 1 || !errors.Is(result.Attempts[0].Err, errReplayedAttempt) || result.Attempts[0].Err.Error() == "private prose" {
		t.Fatalf("replayed attempt = %+v", result.Attempts)
	}
}

func TestReplayClockOffsetKeepsThresholdSemantics(t *testing.T) {
	ms := clockSkewThreshold.Milliseconds()
	result, err := replayResult(ProbeInternet, StatusPass, snapshot.Check{Observed: &snapshot.Observed{ClockOffsetMs: &ms}})
	if err != nil {
		t.Fatal(err)
	}
	if result.clockOffset != clockSkewThreshold {
		t.Fatalf("clock offset = %v, want %v", result.clockOffset, clockSkewThreshold)
	}
}

// A real field fixture is a --support artifact, so replay has to survive
// redaction as well as encoding. Pseudonymization preserves the class of every
// address (loopback, private, link-local, public) and the shape of every
// structured field, which is all the truth table reads, so the conclusion must
// come out the same. Only the address strings inside evidence change, and they
// change to exactly the addresses the sanitized artifact records.
//
// The one place redaction can still cost a deduction: answersAgree compares
// the /16 an answer sits in, and every public address in one artifact is
// pseudonymized into a single /16, so two disagreeing public answer sets can
// read as agreeing. The dns_disagreement finding survives anyway because
// reconcileDNS stamped the WARN before the snapshot was written; only the
// resolver counterfactual is at risk, and only when neither answer is private.
func TestReplayAfterSupportSanitizationKeepsTheConclusion(t *testing.T) {
	for _, test := range diagnosisMatrix() {
		t.Run(test.name, func(t *testing.T) {
			probes := make([]Probe, len(test.order))
			for i, id := range test.order {
				probes[i] = Probe{ID: id, Name: string(id)}
			}
			want := Interpret(test.target, test.order, test.res)
			data, err := snapshot.Encode(snapshot.SanitizeForSupport(BuildSnapshot(test.target, probes, test.res)))
			if err != nil {
				t.Fatal(err)
			}
			artifact, err := snapshot.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReplaySnapshot(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if got.Verdict != want.Verdict || got.Blamed != want.Blamed {
				t.Fatalf("sanitized replay = %q/%q, want %q/%q", got.Verdict, got.Blamed, want.Verdict, want.Blamed)
			}
			if len(got.Findings) != len(want.Findings) {
				t.Fatalf("sanitized replay has %d findings, want %d", len(got.Findings), len(want.Findings))
			}
			for i := range want.Findings {
				if got.Findings[i].ID != want.Findings[i].ID || got.Findings[i].Focus != want.Findings[i].Focus {
					t.Errorf("finding %d = %s@%s, want %s@%s", i,
						got.Findings[i].ID, got.Findings[i].Focus, want.Findings[i].ID, want.Findings[i].Focus)
				}
			}
		})
	}
}

// Replay reads an allowlist of probe IDs so an artifact from a future version
// is rejected rather than silently reinterpreted. That only stays honest while
// the list covers the graph this checkout builds.
func TestEveryProbeTheGraphBuildsIsReplayable(t *testing.T) {
	targets := []*Target{
		nil,
		{Host: "example.invalid", Port: 443, Proto: ProtoTLSHTTP},
		{Host: "example.invalid", Port: 80, Proto: ProtoHTTP},
		{Host: "example.invalid", Port: 22, Proto: ProtoSSH},
		{Host: "example.invalid", Port: 25, Proto: ProtoSMTP},
		{Host: "example.invalid", Port: 9100, Proto: ProtoNone},
	}
	for _, target := range targets {
		for _, p := range BuildProbesFromSources(target, nil, "8.8.8.8") {
			if !replayProbeID(p.ID) {
				t.Errorf("probe %q is in the graph but not replayable", p.ID)
			}
		}
	}
}
