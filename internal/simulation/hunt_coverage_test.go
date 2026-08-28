package simulation

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func coverageOf(t *testing.T, base string, opts HuntOptions) *HuntResult {
	t.Helper()
	return RunHunt(context.Background(), base, loadHuntBase(t, base), nil, opts)
}

func operatorRow(t *testing.T, coverage HuntCoverage, id string) HuntOperatorCoverage {
	t.Helper()
	i := slices.IndexFunc(coverage.Operators, func(op HuntOperatorCoverage) bool { return op.ID == id })
	if i < 0 {
		t.Fatalf("coverage has no row for operator %q", id)
	}
	return coverage.Operators[i]
}

func conditionRow(t *testing.T, coverage HuntCoverage, condition NetworkCondition) HuntConditionCoverage {
	t.Helper()
	i := slices.IndexFunc(coverage.Conditions, func(c HuntConditionCoverage) bool { return c.Condition == condition })
	if i < 0 {
		t.Fatalf("coverage has no row for condition %q", condition)
	}
	return coverage.Conditions[i]
}

// The rows are ordered API: the registry decides operator order and the oracle
// decides condition order, so a report read by eye or diffed between two runs
// shows the same thing in the same place.
func TestHuntCoverageRowsFollowRegistryOrderAndAreDeterministic(t *testing.T) {
	opts := HuntOptions{Cases: 12, Seed: 20260101, MaxFaults: 2, DryRun: true}
	first := coverageOf(t, "healthy-routed-network", opts)
	second := coverageOf(t, "healthy-routed-network", opts)
	if !reflect.DeepEqual(first.Coverage, second.Coverage) {
		t.Fatalf("coverage is not deterministic:\n%+v\n%+v", first.Coverage, second.Coverage)
	}
	operators, err := huntOperatorsForLane(HuntGeneratorVersion, HuntLaneBugOracle)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Coverage.Operators) != len(operators) {
		t.Fatalf("coverage rows = %d, want one per lane operator (%d)", len(first.Coverage.Operators), len(operators))
	}
	for i, op := range operators {
		if want := op.contractFor(HuntGeneratorVersion); first.Coverage.Operators[i].ID != op.id ||
			first.Coverage.Operators[i].Contract != string(want) {
			t.Fatalf("operator row %d = %+v, want id %q contract %q", i, first.Coverage.Operators[i], op.id, want)
		}
	}
	if len(first.Coverage.Conditions) != len(conditionOracle) {
		t.Fatalf("condition rows = %d, want one per oracle rule (%d)",
			len(first.Coverage.Conditions), len(conditionOracle))
	}
	for i, rule := range conditionOracle {
		if got := first.Coverage.Conditions[i]; got.Condition != rule.condition || got.Family != rule.family {
			t.Fatalf("condition row %d = %+v, want %q family %q", i, got, rule.condition, rule.family)
		}
	}
}

// Applicability is a fact about the base scenario, not about the run, so it has
// to be reported for operators the hunt never drew. Its counterpart matters
// just as much: nothing outside the applicable set can ever be generated, so a
// generated operator the base cannot host would mean the accounting is lying.
func TestHuntCoverageReportsApplicabilityIndependentlyOfGeneration(t *testing.T) {
	result := coverageOf(t, "healthy", HuntOptions{Cases: 1, Seed: 20260101, MaxFaults: 2, DryRun: true})
	want := []string{"service.tcp_reset", "quic.udp_443_block", "routing.no_default_route"}
	var applicable []string
	for _, op := range result.Coverage.Operators {
		if op.Applicable {
			applicable = append(applicable, op.ID)
		}
		if op.Generated > 0 && !op.Applicable {
			t.Errorf("operator %s was generated but reported inapplicable", op.ID)
		}
		if op.Observed > op.Generated {
			t.Errorf("operator %s observed %d times in %d generated cases", op.ID, op.Observed, op.Generated)
		}
	}
	if !slices.Equal(applicable, want) {
		t.Fatalf("applicable operators = %v, want %v", applicable, want)
	}
	// One case cannot have drawn all three, so the row for the ones it missed
	// is the whole point: the hunt could have gone there and did not.
	if len(result.Coverage.Gaps()) == 0 {
		t.Fatal("a one-case hunt over three applicable operators reported no coverage gap")
	}
}

// Generated is intent and observed is evidence, and the model must never let
// the first stand in for the second. This backend produces a run in which the
// route mutation really is observed and nothing else is, so the two columns
// have to disagree.
func TestHuntCoverageSeparatesGeneratedIntentFromObservedEffect(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	result := RunHunt(context.Background(), "healthy", base, func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: namesTheMissingRouteReport, evidence: deadRouteEvidence()}}
	}, HuntOptions{Cases: 12, Seed: 20260101, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}})
	route := operatorRow(t, result.Coverage, "routing.no_default_route")
	if route.Generated == 0 || route.Observed == 0 {
		t.Fatalf("route operator = %+v, want it both generated and observed", route)
	}
	// The QUIC block was generated into the same cases, but no drop counter
	// exists in this evidence, so nothing independent says it reached the wire.
	quic := operatorRow(t, result.Coverage, "quic.udp_443_block")
	if quic.Generated == 0 || quic.Observed != 0 {
		t.Fatalf("quic operator = %+v, want it generated and never observed", quic)
	}
	if !slices.Contains(result.Coverage.Gaps(),
		HuntCoverageGap{Kind: HuntGapOperatorNotObserved, ID: "quic.udp_443_block"}) {
		t.Fatalf("gaps = %+v, want the generated-but-unobserved operator named", result.Coverage.Gaps())
	}
}

// Established counts the cases the oracle actually compared, and recognized
// counts how many of those the diagnosis named. The difference has to be the
// false negatives this hunt reported, or the two halves are describing
// different runs.
func TestHuntCoverageCountsOracleConditionsAgainstFindings(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	opts := HuntOptions{Cases: 12, Seed: 20260101, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}}
	for _, tc := range []struct {
		name        string
		stdout      string
		wantMissing bool
	}{
		{"diagnosis names the missing route", namesTheMissingRouteReport, false},
		{"diagnosis blames the gateway instead", blamesTheGatewayReport, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout := tc.stdout
			result := RunHunt(context.Background(), "healthy", base, func() Backend {
				return &clientRoleBackend{env: &fakeEnv{stdout: stdout, evidence: deadRouteEvidence()}}
			}, opts)
			row := conditionRow(t, result.Coverage, ConditionNoDefaultRoute)
			if !row.Reachable {
				t.Fatal("no_default_route is generated from this base but was reported unreachable")
			}
			if row.Established == 0 {
				t.Fatal("the deleted default route established no condition")
			}
			missed := 0
			for _, item := range result.Cases {
				missed += len(unrecognizedConditions(item.Findings))
			}
			if got := row.Established - row.Recognized; got != missed {
				t.Fatalf("established-recognized = %d, want the %d reported false negatives", got, missed)
			}
			if tc.wantMissing != (missed > 0) {
				t.Fatalf("reported %d false negatives, want any = %v", missed, tc.wantMissing)
			}
			if result.Coverage.OracleCases != result.ExecutedCases {
				t.Fatalf("oracle-comparable cases = %d, want all %d stable executed cases",
					result.Coverage.OracleCases, result.ExecutedCases)
			}
		})
	}
}

// A condition nothing in this hunt aimed at or reached is not a gap, it is a
// question this base was never able to ask. Reporting it as reachable would
// make every hunt look permanently incomplete.
func TestHuntCoverageMarksUnreachableConditionsInapplicable(t *testing.T) {
	result := coverageOf(t, "healthy", HuntOptions{Cases: 4, Seed: 20260101, MaxFaults: 2, DryRun: true})
	if row := conditionRow(t, result.Coverage, ConditionTLSCertificateExpired); row.Reachable {
		t.Fatal("healthy has no TLS target, so the expired-certificate condition cannot be reachable")
	}
	if row := conditionRow(t, result.Coverage, ConditionNoDefaultRoute); !row.Reachable {
		t.Fatal("healthy hosts routing.no_default_route, so its condition is reachable")
	}
	for _, gap := range result.Coverage.Gaps() {
		if gap.Kind == HuntGapConditionNotEstablished && gap.ID == string(ConditionTLSCertificateExpired) {
			t.Fatal("an unreachable condition was reported as a coverage gap")
		}
	}
}

// The headline measurement, pinned. Five hundred cases against this base are
// not five hundred experiments: the three operators it can host are all
// parameterless, so the whole reachable universe is six networks and every
// further case is one of them again. Coverage is what makes that visible, and
// this test is what stops it from silently stopping being true.
func TestHuntCoverageShowsSemanticSaturationLongBeforeTheCaseBudget(t *testing.T) {
	const wantSets, wantExperiments = 6, 6
	for _, cases := range []int{20, 60, 200} {
		result := coverageOf(t, "healthy", HuntOptions{Cases: cases, Seed: 20260101, MaxFaults: 2, DryRun: true})
		if result.Coverage.MutationSets != wantSets || result.Coverage.DistinctExperiments != wantExperiments {
			t.Fatalf("%d cases produced %d set(s) and %d experiment(s), want %d and %d", cases,
				result.Coverage.MutationSets, result.Coverage.DistinctExperiments, wantSets, wantExperiments)
		}
		if result.Coverage.LastNewSetCase < 0 || result.Coverage.LastNewSetCase >= cases {
			continue
		}
		if cases > 60 && result.Coverage.LastNewSetCase > 60 {
			t.Fatalf("%d cases kept discovering new operator sets until case %d", cases, result.Coverage.LastNewSetCase)
		}
	}
}

// Coverage is derived from the case results and from nothing else, which is
// what makes a merged hunt's coverage the unsharded hunt's coverage rather
// than a sum of partial views that happens to look similar.
func TestMergedHuntCoverageEqualsTheUnshardedHunt(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	backend := func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: blamesTheGatewayReport, evidence: deadRouteEvidence()}}
	}
	opts := HuntOptions{Cases: 24, Seed: 12345, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}}
	full := RunHunt(context.Background(), "healthy-routed-network", base, backend, opts)
	for _, shardCount := range []int{2, 3, 5} {
		var shards []*HuntResult
		for index := 0; index < shardCount; index++ {
			shard := HuntShard{Index: index, Count: shardCount}
			shardOpts := opts
			shardOpts.Shard = &shard
			shards = append(shards, RunHunt(context.Background(), "healthy-routed-network", base, backend, shardOpts))
		}
		merged, err := MergeHuntResults(shards...)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(merged.Coverage, full.Coverage) {
			t.Fatalf("%d shards merged to different coverage:\n%+v\n%+v", shardCount, merged.Coverage, full.Coverage)
		}
		if err := ValidateMergedHuntResult(merged); err != nil {
			t.Fatalf("%d shards: %v", shardCount, err)
		}
	}
}

// Coverage travels in the same artifact as the findings, so a downstream
// consumer has to be able to reject one that does not describe the cases
// beside it.
func TestValidationRejectsCoverageThatDoesNotMatchTheCases(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	result := RunHunt(context.Background(), "healthy", base, func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: namesTheMissingRouteReport, evidence: deadRouteEvidence()}}
	}, HuntOptions{Cases: 8, Seed: 20260101, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}})
	if err := ValidateMergedHuntResult(result); err != nil {
		t.Fatalf("honest result did not validate: %v", err)
	}
	for _, tamper := range []struct {
		name string
		with func(*HuntResult)
	}{
		{"inflated observation", func(r *HuntResult) { r.Coverage.Operators[0].Observed += 3 }},
		{"invented applicability", func(r *HuntResult) {
			for i := range r.Coverage.Operators {
				r.Coverage.Operators[i].Applicable = true
			}
		}},
		{"invented condition", func(r *HuntResult) { r.Coverage.Conditions[0].Established++ }},
		{"hidden repetition", func(r *HuntResult) { r.Coverage.DistinctExperiments = len(r.Cases) }},
		{"moved saturation", func(r *HuntResult) { r.Coverage.LastNewSetCase = 999 }},
		{"invented oracle comparison", func(r *HuntResult) { r.Coverage.OracleCases++ }},
		{"invented third fault", func(r *HuntResult) { r.Coverage.Cardinality = append(r.Coverage.Cardinality, 1) }},
		{"invented interaction", func(r *HuntResult) { r.Coverage.MultiFaultCases++ }},
	} {
		t.Run(tamper.name, func(t *testing.T) {
			tampered := cloneHuntResult(t, result)
			tamper.with(tampered)
			if err := ValidateMergedHuntResult(tampered); err == nil {
				t.Fatal("tampered coverage validated")
			}
		})
	}
}

// A dry run generated cases and executed none, so it may claim generation and
// must claim nothing else. This is also the cheap way to ask what a budget
// would cover before spending it.
func TestDryRunCoverageClaimsGenerationAndNoObservation(t *testing.T) {
	result := coverageOf(t, "healthy-routed-network", HuntOptions{Cases: 20, Seed: 12345, MaxFaults: 2, DryRun: true})
	if result.Coverage.MutationSets == 0 {
		t.Fatal("a dry run reported no generated operator sets")
	}
	if result.Coverage.OracleCases != 0 {
		t.Fatalf("dry-run oracle-comparable cases = %d, want 0", result.Coverage.OracleCases)
	}
	for _, op := range result.Coverage.Operators {
		if op.Observed != 0 {
			t.Fatalf("dry-run operator %s claims %d observations", op.ID, op.Observed)
		}
	}
	for _, condition := range result.Coverage.Conditions {
		if condition.Established != 0 || condition.Recognized != 0 {
			t.Fatalf("dry-run condition %s claims %+v", condition.Condition, condition)
		}
	}
}

// A hunt that stopped early, failed, or was never allowed to run describes the
// ground it did stand on, and reports no saturation it never reached.
func TestPartialAndFailedHuntCoverageDescribesOnlyWhatRan(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	failing := RunHunt(context.Background(), "healthy", base, func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: blamesTheGatewayReport, evidence: deadRouteEvidence()}}
	}, HuntOptions{Cases: 20, Seed: 20260101, MaxFaults: 2, FailFast: true, Run: Options{Netdoc: "netdoc"}})
	if !failing.FailFastStopped {
		t.Fatal("fail-fast hunt did not stop on a finding")
	}
	if failing.Coverage.MutationSets > len(failing.Cases) {
		t.Fatalf("fail-fast coverage claims %d set(s) from %d case(s)",
			failing.Coverage.MutationSets, len(failing.Cases))
	}
	if failing.Coverage.LastNewSetCase != failing.Cases[len(failing.Cases)-1].Manifest.Case &&
		failing.Coverage.LastNewSetCase > failing.Cases[len(failing.Cases)-1].Manifest.Case {
		t.Fatalf("fail-fast saturation case %d is past the last executed case",
			failing.Coverage.LastNewSetCase)
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	empty := RunHunt(cancelled, "healthy", base, nil, HuntOptions{Cases: 5, Seed: 20260101, MaxFaults: 2, DryRun: true})
	if empty.Result != HuntResultCancelled {
		t.Fatalf("cancelled hunt result = %q", empty.Result)
	}
	if empty.Coverage.MutationSets != 0 || empty.Coverage.LastNewSetCase != -1 {
		t.Fatalf("cancelled hunt claims coverage: %+v", empty.Coverage)
	}
	if len(empty.Coverage.Operators) == 0 {
		t.Fatal("a cancelled hunt still knows which operators it could have drawn, and reported none")
	}

	broken := RunHunt(context.Background(), "no-such-base", nil, nil, HuntOptions{Cases: 1, DryRun: true})
	if broken.Result != HuntResultError {
		t.Fatalf("unknown base result = %q", broken.Result)
	}
	if broken.Coverage.MutationSets != 0 || broken.Coverage.LastNewSetCase != -1 {
		t.Fatalf("failed hunt claims coverage: %+v", broken.Coverage)
	}
}

// Coverage is additive: it says more about a clean result without changing what
// clean means, and it survives the JSON round trip a shard artifact takes.
func TestCoverageIsAdditiveAndSurvivesJSON(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	result := RunHunt(context.Background(), "healthy", base, func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: namesTheMissingRouteReport, evidence: deadRouteEvidence()}}
	}, HuntOptions{Cases: 8, Seed: 20260101, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}})
	if result.Result != HuntResultClean || len(result.Findings) != 0 {
		t.Fatalf("result = %q with %d finding(s), want a clean run to test against", result.Result, len(result.Findings))
	}
	if len(result.Coverage.Gaps()) == 0 {
		t.Fatal("this clean run has unobserved operators and should report gaps")
	}
	blob, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var round HuntResult
	if err := json.Unmarshal(blob, &round); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(round.Coverage, result.Coverage) {
		t.Fatalf("coverage did not survive JSON:\n%+v\n%+v", round.Coverage, result.Coverage)
	}
	// A reader that predates coverage still gets a hunt it understands.
	var legacy map[string]any
	if err := json.Unmarshal(blob, &legacy); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"result", "findings", "case_results", "clean_cases", "coverage"} {
		if _, ok := legacy[key]; !ok {
			t.Fatalf("hunt JSON lost %q", key)
		}
	}
}

// Historical hunts still reproduce exactly, and their coverage describes the
// operator universe that version and lane actually had rather than today's.
// Coverage is read off the case results, so it must not have moved a single
// case in any published generator.
func TestHistoricalHuntsReproduceAndReportTheirOwnOperatorUniverse(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	for _, version := range HuntGeneratorVersions() {
		t.Run(version, func(t *testing.T) {
			lane, err := ResolveHuntLane(version, "")
			if err != nil {
				t.Fatal(err)
			}
			result := RunHunt(context.Background(), "healthy-routed-network", base, nil,
				HuntOptions{Cases: 15, Seed: 12345, MaxFaults: 2, GeneratorVersion: version, DryRun: true})
			if result.Result != HuntResultClean || result.Lane != lane {
				t.Fatalf("result %q lane %q", result.Result, result.Lane)
			}
			for _, item := range result.Cases {
				direct, err := generateHuntCaseInLane(version, lane, "healthy-routed-network", base,
					12345, item.Manifest.Case, 2)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(item.Manifest, direct.Manifest) {
					t.Fatalf("case %d no longer reproduces:\n got  %+v\n want %+v",
						item.Manifest.Case, item.Manifest, direct.Manifest)
				}
			}
			operators, err := huntOperatorsForLane(version, lane)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Coverage.Operators) != len(operators) {
				t.Fatalf("%s coverage has %d operator row(s), want this version's %d",
					version, len(result.Coverage.Operators), len(operators))
			}
			for i, op := range operators {
				if got := result.Coverage.Operators[i].ID; got != op.id {
					t.Fatalf("%s operator row %d = %q, want %q", version, i, got, op.id)
				}
				if lane != HuntLaneAllOperators && op.laneFor(version) != lane {
					t.Fatalf("%s lane %q leaked operator %q from lane %q", version, lane, op.id, op.laneFor(version))
				}
			}
		})
	}
}

// A fault reaches conditions it never declared: deleting the default route also
// takes IPv4 off the internet, and the oracle rightly establishes both. Nothing
// may report a condition as unreachable in a hunt that established it, or the
// denominator ends up smaller than the numerator counted into it.
func TestEstablishedConditionsAreAlwaysReportedReachable(t *testing.T) {
	base := loadHuntBase(t, "healthy-routed-network")
	result := RunHunt(context.Background(), "healthy-routed-network", base, func() Backend {
		return &clientRoleBackend{env: &fakeEnv{stdout: namesTheMissingRouteReport, evidence: deadRouteEvidence()}}
	}, HuntOptions{Cases: 16, Seed: 12345, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}})
	sideEffect := conditionRow(t, result.Coverage, ConditionIPv4InternetUnreachable)
	if sideEffect.Established == 0 {
		t.Fatal("the dead-route evidence established no IPv4 unreachability to test with")
	}
	for _, condition := range result.Coverage.Conditions {
		if condition.Established > 0 && !condition.Reachable {
			t.Fatalf("condition %s established %d time(s) while reported unreachable",
				condition.Condition, condition.Established)
		}
		if condition.Recognized > condition.Established {
			t.Fatalf("condition %s recognized %d time(s) in %d establishment(s)",
				condition.Condition, condition.Recognized, condition.Established)
		}
	}
	_, _, _, conditions, established := result.Coverage.Counts()
	if established > conditions {
		t.Fatalf("%d condition(s) established out of %d reachable", established, conditions)
	}
}

// A dry run has no observations to be missing, so it must not file the absence
// of them as a limit on a result it never produced.
func TestDryRunReportsNoObservationGaps(t *testing.T) {
	result := coverageOf(t, "healthy", HuntOptions{Cases: 12, Seed: 20260101, MaxFaults: 2, DryRun: true})
	if result.Coverage.ExecutedCases != 0 {
		t.Fatalf("dry-run coverage claims %d executed case(s)", result.Coverage.ExecutedCases)
	}
	for _, gap := range result.Coverage.Gaps() {
		if gap.Kind != HuntGapOperatorNotGenerated {
			t.Fatalf("dry run filed a %s gap for %s", gap.Kind, gap.ID)
		}
	}
}

// The fault ceiling says what a hunt asked for; the cardinality says what the
// base could deliver. They come apart wherever the applicable operators share
// conflict tags, and the difference is the whole reason to record the second
// number: a hunt raised to three faults over a base that can only ever build
// two has bought nothing, and only this row says so.
func TestHuntCoverageReportsTheFaultCardinalityTheBaseCouldDeliver(t *testing.T) {
	for _, tc := range []struct {
		base string
		want int
		why  string
	}{
		{"tls-valid", 3, "five conflict-free operator classes leave room for a third fault"},
		{"two-path-ipv6-healthy", 3, "the reset and the promoted preferred-path failure both sit outside the resolver-state tag"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			result := coverageOf(t, tc.base, HuntOptions{Cases: 60, Seed: 20260104, MaxFaults: 3, DryRun: true})
			cardinality := result.Coverage.Cardinality
			if len(cardinality) != tc.want {
				t.Fatalf("largest case carried %d mutation(s) under a ceiling of 3, want %d: %s",
					len(cardinality), tc.want, tc.why)
			}
			total := 0
			for i, cases := range cardinality {
				if cases == 0 {
					t.Errorf("no case carried %d mutation(s), leaving a hole in the histogram", i+1)
				}
				total += cases
			}
			if total != len(result.Cases) {
				t.Fatalf("cardinality accounts for %d case(s), want all %d", total, len(result.Cases))
			}
		})
	}
}

// Interaction is two faults confirmed on one network, not one fault with a wide
// blast radius. A deleted default route establishes two oracle conditions by
// itself, so counting conditions would credit a hunt that never put two faults
// together, which is precisely the coverage a raised fault ceiling is bought
// for.
func TestHuntCoverageCountsOnlyCasesThatObservedSeveralFaultsAtOnce(t *testing.T) {
	base := loadHuntBase(t, "healthy")
	opts := HuntOptions{Cases: 12, Seed: 20260101, MaxFaults: 2, Run: Options{Netdoc: "netdoc"}}
	run := func(evidence Evidence) *HuntResult {
		return RunHunt(context.Background(), "healthy", base, func() Backend {
			return &clientRoleBackend{env: &fakeEnv{stdout: blamesTheGatewayReport, evidence: evidence}}
		}, opts)
	}
	// The dead route on its own: every comparable case establishes both the
	// missing route and the unreachable family, and exactly one fault is
	// observed, so no case here is an interaction.
	wide := run(deadRouteEvidence())
	conditions := 0
	for _, item := range wide.Cases {
		if established, _, comparable := caseConditions(item.Report, item.Truth); comparable && len(established) > 1 {
			conditions++
		}
	}
	if conditions == 0 {
		t.Fatal("the dead-route fixture established no case with two conditions to contrast against")
	}
	if wide.Coverage.MultiFaultCases != 0 {
		t.Fatalf("multi-fault cases = %d in %d case(s) that established two conditions from one fault, want 0",
			wide.Coverage.MultiFaultCases, conditions)
	}
	// The same run with the drop counter the second operator needs, read off
	// the node this base actually blocks QUIC on: the cases that drew both now
	// carry two independently observed faults.
	pair := deadRouteEvidence()
	pair.PacketDrops = []PacketDropEvidence{{Node: "internet", Protocol: "udp", Port: 443,
		Direction: DirectionInbound, Packets: 4}}
	both := run(pair)
	want := 0
	for _, item := range both.Cases {
		if len(item.Truth.ObservedFaults) > 1 {
			want++
		}
	}
	if want == 0 {
		t.Fatal("no case observed two faults at once, so this fixture proves nothing")
	}
	if both.Coverage.MultiFaultCases != want {
		t.Fatalf("multi-fault cases = %d, want the %d case(s) whose evidence confirmed two faults",
			both.Coverage.MultiFaultCases, want)
	}
}
