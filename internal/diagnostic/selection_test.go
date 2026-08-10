package diagnostic

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func probeSet(ids ...ProbeID) map[ProbeID]struct{} {
	set := make(map[ProbeID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func selectedIDs(probes []Probe) []ProbeID {
	ids := make([]ProbeID, len(probes))
	for i, p := range probes {
		ids[i] = p.ID
	}
	return ids
}

func probesWithIDs(ids []ProbeID) []Probe {
	probes := make([]Probe, len(ids))
	for i, id := range ids {
		probes[i].ID = id
	}
	return probes
}

func TestProbeSelection(t *testing.T) {
	target, err := ParseTarget("example.com")
	if err != nil {
		t.Fatal(err)
	}
	probes := BuildProbesFrom(target, nil)
	tests := []struct {
		name       string
		selection  ProbeSelection
		want       []ProbeID
		wantAbsent []ProbeID
	}{
		{
			name:      "one check includes prerequisites",
			selection: ProbeSelection{Check: probeSet(ProbeTLS)},
			want:      []ProbeID{ProbeIface, ProbeDNS, ProbeTargetTCP, ProbeTLS},
		},
		{
			name:      "multiple checks use canonical order",
			selection: ProbeSelection{Check: probeSet(ProbeTLS, ProbeDNS, ProbeTargetTCP)},
			want:      []ProbeID{ProbeIface, ProbeDNS, ProbeTargetTCP, ProbeTLS},
		},
		{
			name:       "one skip",
			selection:  ProbeSelection{Skip: probeSet(ProbeQUIC)},
			wantAbsent: []ProbeID{ProbeQUIC},
		},
		{
			name:       "multiple skips",
			selection:  ProbeSelection{Skip: probeSet(ProbeInternet, ProbeQUIC)},
			wantAbsent: []ProbeID{ProbeInternet, ProbeQUIC},
		},
		{
			name:      "skipped prerequisite removes dependent branch only",
			selection: ProbeSelection{Check: probeSet(ProbeTLS, ProbeQUIC), Skip: probeSet(ProbeDNS)},
			want:      []ProbeID{ProbeIface, ProbeQUIC},
		},
		{
			name:      "all requested branches blocked",
			selection: ProbeSelection{Check: probeSet(ProbeDNS, ProbeTargetTCP, ProbeTLS), Skip: probeSet(ProbeDNS)},
			want:      []ProbeID{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.selection.Apply(probes)
			if tt.want != nil && !reflect.DeepEqual(selectedIDs(got), tt.want) {
				t.Errorf("IDs = %v, want %v", selectedIDs(got), tt.want)
			}
			for _, absent := range tt.wantAbsent {
				for _, id := range selectedIDs(got) {
					if id == absent {
						t.Errorf("selected probes contain skipped %q", absent)
					}
				}
			}
		})
	}

	a := ProbeSelection{Check: probeSet(ProbeTLS, ProbeDNS, ProbeTargetTCP)}.Apply(probes)
	b := ProbeSelection{Check: probeSet(ProbeDNS, ProbeTargetTCP, ProbeTLS)}.Apply(probes)
	if !reflect.DeepEqual(selectedIDs(a), selectedIDs(b)) {
		t.Errorf("check order changed probe order: %v != %v", selectedIDs(a), selectedIDs(b))
	}
}

func TestProbeSelectionGraphClosureAndPruning(t *testing.T) {
	const (
		root    ProbeID = "root"
		a       ProbeID = "a"
		x       ProbeID = "x"
		b       ProbeID = "b"
		left    ProbeID = "left"
		right   ProbeID = "right"
		diamond ProbeID = "diamond"
	)
	probes := []Probe{
		{ID: root},
		{ID: a, Deps: []ProbeID{root}},
		{ID: x, Deps: []ProbeID{a}},
		{ID: b, Deps: []ProbeID{root}},
		{ID: left, Deps: []ProbeID{root}},
		{ID: right, Deps: []ProbeID{root}},
		{ID: diamond, Deps: []ProbeID{left, right}},
	}
	tests := []struct {
		name string
		sel  ProbeSelection
		want []ProbeID
	}{
		{"deep dependency closure", ProbeSelection{Check: probeSet(x)}, []ProbeID{root, a, x}},
		{"diamond closure", ProbeSelection{Check: probeSet(diamond)}, []ProbeID{root, left, right, diamond}},
		{"blocked branch keeps shared root for viable branch", ProbeSelection{Check: probeSet(x, b), Skip: probeSet(a)}, []ProbeID{root, b}},
		{"skipped root blocks descendants", ProbeSelection{Check: probeSet(x, b), Skip: probeSet(root)}, []ProbeID{}},
		{"skip root without check", ProbeSelection{Skip: probeSet(root)}, []ProbeID{}},
		{"requested prerequisite", ProbeSelection{Check: probeSet(a)}, []ProbeID{root, a}},
		{"all requested branches blocked", ProbeSelection{Check: probeSet(x), Skip: probeSet(a)}, []ProbeID{}},
		{"dependency and descendant requested", ProbeSelection{Check: probeSet(a, x)}, []ProbeID{root, a, x}},
		{"broken diamond prunes unused sibling", ProbeSelection{Check: probeSet(diamond), Skip: probeSet(left)}, []ProbeID{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectedIDs(tt.sel.Apply(probes)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEmptyProbeSelectionPreservesBuiltDAG(t *testing.T) {
	probes := BuildProbesFrom(mustTarget(t, "example.com"), nil)
	got := (ProbeSelection{}).Apply(probes)
	if len(got) != len(probes) {
		t.Fatalf("probe count = %d, want %d", len(got), len(probes))
	}
	if &got[0] != &probes[0] {
		t.Fatal("empty policy copied the probe DAG")
	}
	for i := range probes {
		if got[i].ID != probes[i].ID || got[i].Name != probes[i].Name || !reflect.DeepEqual(got[i].Deps, probes[i].Deps) || reflect.ValueOf(got[i].Run).Pointer() != reflect.ValueOf(probes[i].Run).Pointer() {
			t.Fatalf("probe %d changed under empty policy: got %+v, want %+v", i, got[i], probes[i])
		}
	}
}

// ProbeInternet owns the direct-egress and captive-portal traffic. Keeping it
// out of the selected slice proves a narrow CI run cannot invoke either one.
func TestProbeSelectionDoesNotInvokeExcludedEgressOrPortalProbes(t *testing.T) {
	var mu sync.Mutex
	runs := map[ProbeID]int{}
	probe := func(id ProbeID, deps ...ProbeID) Probe {
		return Probe{ID: id, Deps: deps, Run: func(context.Context, map[ProbeID]ProbeResult) ProbeResult {
			mu.Lock()
			runs[id]++
			mu.Unlock()
			return ProbeResult{Status: StatusPass}
		}}
	}
	probes := []Probe{
		probe(ProbeIface),
		probe(ProbeInternet, ProbeIface),
		probe(ProbeQUIC, ProbeIface),
		probe(ProbeProxy, ProbeIface),
		probe(ProbeDNS, ProbeIface),
		probe(ProbeDNSPublic, ProbeIface),
		probe(ProbeDNSEncrypted, ProbeIface),
		probe(ProbeTargetTCP, ProbeDNS),
		probe(ProbePMTU, ProbeTargetTCP),
		probe(ProbeSSID, ProbeIface),
		probe(ProbeTLS, ProbeTargetTCP),
		probe(ProbeHTTP, ProbeDNS),
		probe(ProbeHTTPS, ProbeTLS),
	}
	selected := ProbeSelection{Check: probeSet(ProbeDNS, ProbeTargetTCP, ProbeTLS)}.Apply(probes)
	RunAll(context.Background(), selected)

	if got, want := selectedIDs(selected), []ProbeID{ProbeIface, ProbeDNS, ProbeTargetTCP, ProbeTLS}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected IDs = %v, want %v", got, want)
	}
	for _, id := range []ProbeID{ProbeIface, ProbeDNS, ProbeTargetTCP, ProbeTLS} {
		if runs[id] != 1 {
			t.Errorf("%s ran %d times, want 1", id, runs[id])
		}
	}
	for _, id := range []ProbeID{ProbeInternet, ProbeQUIC, ProbeProxy, ProbeDNSPublic, ProbeDNSEncrypted, ProbePMTU, ProbeSSID, ProbeHTTP, ProbeHTTPS} {
		if runs[id] != 0 {
			t.Errorf("excluded probe %s ran %d times", id, runs[id])
		}
	}

	runs = map[ProbeID]int{}
	selected = ProbeSelection{Check: probeSet(ProbeTLS, ProbeQUIC), Skip: probeSet(ProbeDNS)}.Apply(probes)
	RunAll(context.Background(), selected)
	if got, want := selectedIDs(selected), []ProbeID{ProbeIface, ProbeQUIC}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selection with skipped prerequisite = %v, want %v", got, want)
	}
	if runs[ProbeIface] != 1 || runs[ProbeQUIC] != 1 || runs[ProbeDNS] != 0 || runs[ProbeTargetTCP] != 0 || runs[ProbeTLS] != 0 {
		t.Errorf("runs after skipped prerequisite = %v", runs)
	}
}

func TestProbeSelectionValidation(t *testing.T) {
	for _, selection := range []ProbeSelection{
		{Check: probeSet("bogus")},
		{Skip: probeSet("bogus")},
	} {
		if err := selection.Validate(); err == nil {
			t.Fatal("unknown probe ID accepted")
		}
	}
	for _, selection := range []ProbeSelection{
		{Check: probeSet(ProbeTLS, ProbeSSH, ProbeSMTP)},
		{Skip: probeSet(ProbeDNSPublic)},
	} {
		if err := selection.Validate(); err != nil {
			t.Fatalf("known conditional probe rejected: %v", err)
		}
	}
	err1 := (ProbeSelection{Check: probeSet("zzz", "aaa")}).Validate()
	err2 := (ProbeSelection{Check: probeSet("aaa", "zzz")}).Validate()
	if err1 == nil || err2 == nil || err1.Error() != err2.Error() || !strings.Contains(err1.Error(), `unknown probe ID "aaa"`) {
		t.Fatalf("validation errors are not deterministic: %v / %v", err1, err2)
	}
}

func TestDiagnoseSelectedSubsetDoesNotClaimOmittedChecksPassed(t *testing.T) {
	order := []ProbeID{ProbeIface, ProbeDNS}
	results := map[ProbeID]ProbeResult{
		ProbeIface: {Status: StatusPass},
		ProbeDNS:   {Status: StatusPass},
	}
	got, verdict := DiagnoseSelected(probesWithIDs(order), results)
	if got != "Selected checks passed." || verdict != VerdictOK {
		t.Errorf("Diagnose selected subset = %q/%q", got, verdict)
	}
	if got, verdict := DiagnoseSelected(nil, nil); got != "No checks selected." || verdict != VerdictOK {
		t.Errorf("Diagnose empty selection = %q/%q", got, verdict)
	}
	if got, verdict := DiagnoseSelected(probesWithIDs(order), map[ProbeID]ProbeResult{ProbeIface: {Status: StatusPass}}); got != "Running diagnostics…" || verdict != VerdictIncomplete {
		t.Errorf("Diagnose unfinished selection = %q/%q", got, verdict)
	}
	order = []ProbeID{ProbeIface, ProbeDNS, ProbeTargetTCP}
	results[ProbeTargetTCP] = ProbeResult{Status: StatusFail}
	if got, verdict := DiagnoseSelected(probesWithIDs(order), results); got != "A selected network check failed." || verdict != VerdictNetwork {
		t.Errorf("Diagnose target subset failure = %q/%q", got, verdict)
	}
	results[ProbeTargetTCP] = ProbeResult{Status: StatusPass}
	results[ProbeTLS] = ProbeResult{Status: StatusPass}
	order = append(order, ProbeTLS)
	if got, verdict := DiagnoseSelected(probesWithIDs(order), results); got != "Selected checks passed." || verdict != VerdictOK {
		t.Errorf("Diagnose TLS subset = %q/%q", got, verdict)
	}
}

func TestDiagnoseUnfilteredCompatibility(t *testing.T) {
	target := mustTarget(t, "example.com")
	targetProbes := BuildProbesFrom(target, nil)
	targetOrder := selectedIDs(targetProbes)
	targetResults := make(map[ProbeID]ProbeResult, len(targetOrder))
	for _, id := range targetOrder {
		targetResults[id] = ProbeResult{Status: StatusPass}
	}
	if got, verdict := Diagnose(target, targetOrder, targetResults); got != "All checks passed — example.com:443 looks healthy." || verdict != VerdictOK {
		t.Fatalf("full target all-clear = %q/%q", got, verdict)
	}
	targetResults[ProbeQUIC] = ProbeResult{Status: StatusFail}
	targetResults[ProbeDNSPublic] = ProbeResult{Status: StatusWarn}
	if got, verdict := Diagnose(target, targetOrder, targetResults); got != "The target and direct TCP/443 work, but the QUIC handshake over UDP/443 failed — applications can fall back to TCP, which may feel slower." || verdict != VerdictDegraded {
		t.Fatalf("full target precedence = %q/%q", got, verdict)
	}

	genericProbes := BuildProbesFrom(nil, nil)
	genericOrder := selectedIDs(genericProbes)
	genericResults := make(map[ProbeID]ProbeResult, len(genericOrder))
	for _, id := range genericOrder {
		genericResults[id] = ProbeResult{Status: StatusPass}
	}
	if got, verdict := Diagnose(nil, genericOrder, genericResults); got != "Online — direct TCP egress and DNS both work." || verdict != VerdictOK {
		t.Fatalf("full generic all-clear = %q/%q", got, verdict)
	}
}
