package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/heymaikol/network-doctor/internal/simulation"
)

// These tests drive triage with a fake hunt and a fake gh, so nothing here
// builds a namespace, runs netdoc, or touches GitHub.

func candidate(scenario string, seed int64, caseNumber int, code string) simulation.HuntFinding {
	return simulation.HuntFinding{
		Fingerprint: "fp-" + code, Category: simulation.FindingFalseNegative, Severity: simulation.SeverityHigh,
		Code: code, Summary: "netdoc disagreed with the simulator.", Occurrences: 1,
		FirstCase: caseNumber, ExampleCases: []int{caseNumber},
		Reproduce: simulation.HuntReproduction{BaseScenario: scenario, Seed: seed, Case: caseNumber,
			CaseSeed: 7, GeneratorVersion: simulation.HuntGeneratorVersion,
			CaseFingerprint: "case-" + code},
	}
}

// replay is one generated case as the re-run reports it back.
func replay(finding simulation.HuntFinding, reproduces bool) *simulation.HuntResult {
	item := simulation.HuntCaseResult{
		Manifest: simulation.GeneratedCaseManifest{GeneratorVersion: finding.Reproduce.GeneratorVersion,
			BaseScenario: finding.Reproduce.BaseScenario, HuntSeed: finding.Reproduce.Seed,
			Case: finding.Reproduce.Case, CaseSeed: finding.Reproduce.CaseSeed,
			CaseFingerprint: finding.Reproduce.CaseFingerprint,
			Mutations:       []simulation.GeneratedMutation{{ID: "dns.drop", Description: "resolver drops responses"}}},
		Truth:                simulation.ObservedTruth{DNS: "unavailable"},
		DiagnosisFingerprint: simulation.DiagnosisFingerprint{ID: "d1"},
		Status:               "clean",
	}
	result := &simulation.HuntResult{Result: simulation.HuntResultClean, ExecutedCases: 1,
		Cases: []simulation.HuntCaseResult{item}}
	if reproduces {
		result.Cases[0].Findings = []simulation.HuntCaseFinding{{Fingerprint: finding.Fingerprint,
			Category: finding.Category, Severity: finding.Severity, Code: finding.Code,
			Summary: finding.Summary, Reproduce: finding.Reproduce}}
		result.Cases[0].Status, result.Findings, result.Result = "findings",
			[]simulation.HuntFinding{finding}, simulation.HuntResultFindings
	}
	return result
}

// hunts serves a scripted full hunt and a scripted answer per re-run case.
type hunts struct {
	full    *simulation.HuntResult
	perCase map[int]*simulation.HuntResult
	err     error
	calls   []int
}

func (h *hunts) fn() huntFunc {
	return func(_ context.Context, _ string, _ int64, _, caseNumber int) (*simulation.HuntResult, error) {
		h.calls = append(h.calls, caseNumber)
		if h.err != nil {
			return nil, h.err
		}
		if caseNumber < 0 {
			return h.full, nil
		}
		result, ok := h.perCase[caseNumber]
		if !ok {
			return nil, errors.New("unscripted case")
		}
		return result, nil
	}
}

type ghCalls struct {
	args   [][]string
	list   string
	err    error
	bodies []string
}

func (g *ghCalls) fn() ghFunc {
	return func(_ context.Context, args ...string) ([]byte, error) {
		g.args = append(g.args, args)
		if g.err != nil {
			return nil, g.err
		}
		switch args[1] {
		case "list":
			if g.list == "" {
				return []byte("[]"), nil
			}
			return []byte(g.list), nil
		case "create":
			body, err := readBodyFile(args)
			if err != nil {
				return nil, err
			}
			g.bodies = append(g.bodies, body)
			return []byte("https://github.test/heymaikol/network-doctor/issues/1\n"), nil
		}
		return nil, errors.New("unexpected gh call")
	}
}

func readBodyFile(args []string) (string, error) {
	for i, arg := range args {
		if arg == "--body-file" && i+1 < len(args) {
			blob, err := os.ReadFile(args[i+1])
			return string(blob), err
		}
	}
	return "", errors.New("no --body-file")
}

func (g *ghCalls) subcommands() []string {
	var out []string
	for _, args := range g.args {
		out = append(out, strings.Join(args[:2], " "))
	}
	return out
}

func baselineOpts(create bool) triageOptions {
	return triageOptions{baselines: []simulation.TriageBaseline{{Scenario: "healthy", Seed: 20260101}},
		cases: 20, minSeverity: simulation.SeverityMedium, create: create,
		revision: "abc123", context: "workflow run 42"}
}

// Only a finding that survives its own re-run becomes an issue.
func TestTriageFilesOnlyReproducibleFindings(t *testing.T) {
	solid, flaky := candidate("healthy", 20260101, 4, "solid"), candidate("healthy", 20260101, 9, "flaky")
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings, ExecutedCases: 20,
			Findings: []simulation.HuntFinding{solid, flaky}},
		perCase: map[int]*simulation.HuntResult{4: replay(solid, true), 9: replay(flaky, false)},
	}
	gh := &ghCalls{}
	report := triage(context.Background(), baselineOpts(true), h.fn(), gh.fn())

	if report.Result != simulation.TriageResultFindings || report.Error != "" {
		t.Fatalf("report = %+v", report)
	}
	if len(h.calls) != 3 || h.calls[0] != -1 || h.calls[1] != 4 || h.calls[2] != 9 {
		t.Fatalf("hunt calls = %v, want the hunt then each candidate's case", h.calls)
	}
	if len(report.Baselines) != 1 || report.Baselines[0].Cases != 20 || report.Baselines[0].Candidates != 2 {
		t.Fatalf("baselines = %+v", report.Baselines)
	}
	if len(report.Findings) != 2 || !report.Findings[0].Reproducible || report.Findings[1].Reproducible {
		t.Fatalf("findings = %+v", report.Findings)
	}
	if report.Findings[0].Issue.Status != simulation.IssueStatusCreated ||
		report.Findings[0].Issue.URL != "https://github.test/heymaikol/network-doctor/issues/1" ||
		report.Findings[1].Issue.Status != simulation.IssueStatusNotFiled {
		t.Fatalf("issues = %+v, %+v", report.Findings[0].Issue, report.Findings[1].Issue)
	}
	if got := gh.subcommands(); len(got) != 2 || got[0] != "issue list" || got[1] != "issue create" {
		t.Fatalf("gh calls = %v", got)
	}
	// The re-run's truth, diagnosis and mutations are what the issue reports.
	if report.Findings[0].Truth.DNS != "unavailable" || report.Findings[0].Diagnosis.ID != "d1" ||
		len(report.Findings[0].Mutations) != 1 {
		t.Fatalf("finding = %+v", report.Findings[0])
	}
	if len(gh.bodies) != 1 || !strings.Contains(gh.bodies[0], "--seed 20260101 --case 4") ||
		!strings.Contains(gh.bodies[0], "workflow run 42") || !strings.Contains(gh.bodies[0], "abc123") {
		t.Fatalf("issue body = %q", gh.bodies)
	}
	if title := findArg(gh.args[1], "--title"); !strings.Contains(title, report.Findings[0].Fingerprint) {
		t.Fatalf("title = %q, want the fingerprint %s", title, report.Findings[0].Fingerprint)
	}
	if code := triageExit(report); code != exitOK {
		t.Fatalf("exit = %d, want 0: a captured bug is not a failed run", code)
	}
}

// An open issue carrying the fingerprint stops a second one being filed.
func TestTriageSkipsAnIssueThatAlreadyTracksTheFinding(t *testing.T) {
	solid := candidate("healthy", 20260101, 4, "solid")
	fingerprint := simulation.NewTriageFinding(solid).Fingerprint
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings, ExecutedCases: 20,
			Findings: []simulation.HuntFinding{solid}},
		perCase: map[int]*simulation.HuntResult{4: replay(solid, true)},
	}
	gh := &ghCalls{list: `[{"number":7,"title":"netdoc-sim hunt: solid in healthy (case 4) [` +
		fingerprint + `]","url":"https://github.test/issues/7"}]`}
	report := triage(context.Background(), baselineOpts(true), h.fn(), gh.fn())

	if got := gh.subcommands(); len(got) != 1 || got[0] != "issue list" {
		t.Fatalf("gh calls = %v, want only the duplicate search", got)
	}
	if report.Findings[0].Issue.Status != simulation.IssueStatusExisting ||
		report.Findings[0].Issue.URL != "https://github.test/issues/7" {
		t.Fatalf("issue = %+v", report.Findings[0].Issue)
	}
	if search := findArg(gh.args[0], "--search"); !strings.Contains(search, fingerprint) {
		t.Fatalf("search = %q, want the fingerprint", search)
	}
	if code := triageExit(report); code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// A search hit that does not actually carry the fingerprint is not a duplicate.
func TestTriageIgnoresUnrelatedSearchHits(t *testing.T) {
	solid := candidate("healthy", 20260101, 4, "solid")
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings,
			Findings: []simulation.HuntFinding{solid}},
		perCase: map[int]*simulation.HuntResult{4: replay(solid, true)},
	}
	gh := &ghCalls{list: `[{"number":3,"title":"something else entirely","url":"https://github.test/issues/3"}]`}
	report := triage(context.Background(), baselineOpts(true), h.fn(), gh.fn())
	if report.Findings[0].Issue.Status != simulation.IssueStatusCreated {
		t.Fatalf("issue = %+v", report.Findings[0].Issue)
	}
}

// Nothing reproduced: no search, no issue, and a clean run.
func TestTriageWithNoReproducibleFindingFilesNothing(t *testing.T) {
	flaky := candidate("healthy", 20260101, 9, "flaky")
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings,
			Findings: []simulation.HuntFinding{flaky}},
		perCase: map[int]*simulation.HuntResult{9: replay(flaky, false)},
	}
	gh := &ghCalls{}
	report := triage(context.Background(), baselineOpts(true), h.fn(), gh.fn())
	if report.Result != simulation.TriageResultClean || len(gh.args) != 0 {
		t.Fatalf("report = %s, gh = %v", report.Result, gh.args)
	}
	if code := triageExit(report); code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// Without -create a reproducible finding is real but unrecorded, which is a
// failure: the run found a bug and nothing is tracking it.
func TestTriageWithoutCreateReportsUnfiledFindings(t *testing.T) {
	solid := candidate("healthy", 20260101, 4, "solid")
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings,
			Findings: []simulation.HuntFinding{solid}},
		perCase: map[int]*simulation.HuntResult{4: replay(solid, true)},
	}
	gh := &ghCalls{}
	report := triage(context.Background(), baselineOpts(false), h.fn(), gh.fn())
	if len(gh.args) != 0 {
		t.Fatalf("gh was called without -create: %v", gh.args)
	}
	if code := triageExit(report); code != exitMismatch {
		t.Fatalf("exit = %d, want %d", code, exitMismatch)
	}
}

// Anything that stops triage from proving a finding fails the run instead of
// filing a guess.
func TestTriageFailsInsteadOfFilingOnError(t *testing.T) {
	solid := candidate("healthy", 20260101, 4, "solid")
	withFinding := &simulation.HuntResult{Result: simulation.HuntResultFindings,
		Findings: []simulation.HuntFinding{solid}}
	wrongCase := replay(solid, true)
	wrongCase.Cases[0].Manifest.CaseFingerprint = "drifted"

	for name, test := range map[string]struct {
		hunts *hunts
		gh    *ghCalls
		want  string
	}{
		"hunt cannot run": {
			hunts: &hunts{err: errors.New("namespaces unavailable")}, gh: &ghCalls{}, want: "namespaces unavailable"},
		"simulator failure": {
			hunts: &hunts{full: &simulation.HuntResult{Result: simulation.HuntResultError,
				ErrorKind: "runtime", Error: "cleanup failed"}}, gh: &ghCalls{}, want: "cleanup failed"},
		"cancelled hunt": {
			hunts: &hunts{full: &simulation.HuntResult{Result: simulation.HuntResultCancelled,
				ErrorKind: "cancellation", Error: "context canceled"}}, gh: &ghCalls{}, want: "context canceled"},
		"reproduction fails": {
			hunts: &hunts{full: withFinding, perCase: map[int]*simulation.HuntResult{
				4: {Result: simulation.HuntResultError, ErrorKind: "runtime", Error: "netdoc missing"}}},
			gh: &ghCalls{}, want: "netdoc missing"},
		"reproduction generated another case": {
			hunts: &hunts{full: withFinding, perCase: map[int]*simulation.HuntResult{4: wrongCase}},
			gh:    &ghCalls{}, want: "case fingerprint"},
		"malformed reproduction": {
			hunts: &hunts{full: withFinding, perCase: map[int]*simulation.HuntResult{
				4: {Result: simulation.HuntResultClean}}},
			gh: &ghCalls{}, want: "produced 0 cases"},
		"gh fails": {
			hunts: &hunts{full: withFinding, perCase: map[int]*simulation.HuntResult{4: replay(solid, true)}},
			gh:    &ghCalls{err: errors.New("gh: HTTP 403")}, want: "HTTP 403"},
	} {
		t.Run(name, func(t *testing.T) {
			report := triage(context.Background(), baselineOpts(true), test.hunts.fn(), test.gh.fn())
			if report.Result != simulation.TriageResultError || !strings.Contains(report.Error, test.want) {
				t.Fatalf("result = %s, error = %q, want %q", report.Result, report.Error, test.want)
			}
			if code := triageExit(report); code != exitError {
				t.Errorf("exit = %d, want %d", code, exitError)
			}
			for _, args := range test.gh.args {
				if args[1] == "create" {
					t.Errorf("an issue was filed despite %s", name)
				}
			}
		})
	}
}

// Low-severity findings are mostly known probe limits, so the floor keeps them
// out of the nightly issue tracker without a reproduction run.
func TestTriageSkipsFindingsBelowTheSeverityFloor(t *testing.T) {
	noisy := candidate("healthy", 20260101, 4, "jitter_sampling_gap")
	noisy.Severity, noisy.Category = simulation.SeverityLow, simulation.FindingCoverageGap
	h := &hunts{full: &simulation.HuntResult{Result: simulation.HuntResultFindings, ExecutedCases: 20,
		Findings: []simulation.HuntFinding{noisy}}}
	gh := &ghCalls{}
	report := triage(context.Background(), baselineOpts(true), h.fn(), gh.fn())
	if len(h.calls) != 1 || len(gh.args) != 0 || len(report.Findings) != 0 {
		t.Fatalf("calls = %v, gh = %v, findings = %+v", h.calls, gh.args, report.Findings)
	}
	if report.Baselines[0].Candidates != 1 || report.Baselines[0].Filtered != 1 {
		t.Fatalf("baseline = %+v", report.Baselines[0])
	}
	if report.Result != simulation.TriageResultClean || triageExit(report) != exitOK {
		t.Fatalf("result = %s, exit = %d", report.Result, triageExit(report))
	}
}

func TestTriageFlagsBoundsAndBaselines(t *testing.T) {
	for _, args := range [][]string{
		{"triage", "--cases", "0"},
		{"triage", "--cases", "501"},
		{"triage", "--max-faults", "4"},
		{"triage", "--timeout", "0s"},
		{"triage", "--scenarios", "broken-dns"},
		{"triage", "--scenarios", "healthy,not-a-baseline"},
		{"triage", "--min-severity", "cosmetic"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) = %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestTriageFlagsSelectBaselinesAndOverrideSeeds(t *testing.T) {
	f := newTriageFlags(io.Discard)
	opts, err := f.parse([]string{"--scenarios", "healthy, dual-stack-healthy", "--seed", "-99",
		"--min-severity", "high"})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.baselines) != 2 || opts.baselines[0].Scenario != "healthy" ||
		opts.baselines[1].Scenario != "dual-stack-healthy" ||
		opts.baselines[0].Seed != -99 || opts.baselines[1].Seed != -99 ||
		opts.minSeverity != simulation.SeverityHigh {
		t.Fatalf("options = %+v", opts)
	}
	// The fixed seeds and a medium floor are the defaults, and selecting
	// scenarios does not move them.
	f = newTriageFlags(io.Discard)
	opts, err = f.parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := simulation.TriageBaselines(); len(opts.baselines) != len(want) || opts.baselines[0] != want[0] ||
		opts.minSeverity != simulation.SeverityMedium || opts.cases != 20 {
		t.Fatalf("options = %+v, want %+v", opts, want)
	}
}

// The JSON report is what a workflow reads back; it must round-trip.
func TestTriageReportJSONRoundTrip(t *testing.T) {
	solid := candidate("healthy", 20260101, 4, "solid")
	h := &hunts{
		full: &simulation.HuntResult{Result: simulation.HuntResultFindings, ExecutedCases: 20,
			Findings: []simulation.HuntFinding{solid}},
		perCase: map[int]*simulation.HuntResult{4: replay(solid, true)},
	}
	report := triage(context.Background(), baselineOpts(true), h.fn(), (&ghCalls{}).fn())
	var out bytes.Buffer
	if err := report.WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	var back simulation.TriageReport
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Findings) != 1 || back.Findings[0].Fingerprint != report.Findings[0].Fingerprint ||
		back.Findings[0].Seed != 20260101 || back.Findings[0].Case != 4 || !back.Findings[0].Reproducible {
		t.Fatalf("decoded = %+v", back)
	}
}

func findArg(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestPrecomputedHuntsUseMergedReportsAndStillReplayExactCases(t *testing.T) {
	base, err := simulation.LibraryScenario("healthy-routed-network")
	if err != nil {
		t.Fatal(err)
	}
	const (
		cases = 4
		seed  = int64(20260823)
	)
	var shards []*simulation.HuntResult
	for index := 0; index < 2; index++ {
		shard := simulation.HuntShard{Index: index, Count: 2}
		shards = append(shards, simulation.RunHunt(context.Background(), "healthy-routed-network", base,
			func() simulation.Backend { return stubBackend{supported: false} }, simulation.HuntOptions{
				Cases: cases, Seed: seed, MaxFaults: 2, Shard: &shard,
				Run: simulation.Options{Netdoc: "netdoc"},
			}))
	}
	merged, err := simulation.MergeHuntResults(shards...)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// #nosec G304 -- path is inside t.TempDir.
	file, err := os.Create(filepath.Join(dir, "name-is-not-metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := merged.WriteJSON(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	replays := 0
	var replaySeed int64
	replay := func(_ context.Context, scenario string, seed int64, gotCases, caseNumber int) (*simulation.HuntResult, error) {
		replays++
		replaySeed = seed
		return &simulation.HuntResult{BaseScenario: scenario, HuntSeed: seed, RequestedCases: gotCases,
			Result: simulation.HuntResultClean}, nil
	}
	opts := triageOptions{baselines: []simulation.TriageBaseline{{Scenario: "healthy-routed-network", Seed: seed}},
		cases: cases, maxFaults: 2}
	hunt, err := precomputedHunts(dir, opts, replay)
	if err != nil {
		t.Fatal(err)
	}
	got, err := hunt(context.Background(), "healthy-routed-network", seed, cases, -1)
	if err != nil || got.BaseScenario != merged.BaseScenario || len(got.Cases) != len(merged.Cases) || replays != 0 {
		t.Fatalf("full hunt base = %q, cases = %d, err = %v, replays = %d",
			got.BaseScenario, len(got.Cases), err, replays)
	}
	if _, err := hunt(context.Background(), "healthy-routed-network", seed, 1, 7); err != nil || replays != 1 || replaySeed != seed {
		t.Fatalf("exact replay seed = %d, err = %v, calls = %d", replaySeed, err, replays)
	}
}

func TestPrecomputedHuntsRejectMissingAndMismatchedReports(t *testing.T) {
	opts := triageOptions{baselines: []simulation.TriageBaseline{{Scenario: "healthy", Seed: 20260101}},
		cases: 5, maxFaults: 2}
	if _, err := precomputedHunts(t.TempDir(), opts, nil); err == nil || !strings.Contains(err.Error(), "missing hunt result") {
		t.Fatalf("empty directory error = %v", err)
	}
}

// The workflow pipes triage through tee. Under bash's default -e that hides a
// nonzero exit behind tee's zero, which once turned a hunt that never ran into
// a green nightly. GitHub only enables pipefail when a workflow names its
// shell, so the naming is load-bearing and worth a test.
func TestHuntWorkflowKeepsTheTriageExitStatus(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "hunt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Defaults workflowDefaults `yaml:"defaults"`
		Jobs     map[string]struct {
			Defaults workflowDefaults `yaml:"defaults"`
			Steps    []struct {
				Run   string `yaml:"run"`
				Shell string `yaml:"shell"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(blob, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Defaults.Run.Shell != "bash" {
		t.Errorf("workflow default shell = %q, want bash: only a named shell gets pipefail",
			workflow.Defaults.Run.Shell)
	}
	piped := false
	for name, job := range workflow.Jobs {
		if job.Defaults.Run.Shell != "" && job.Defaults.Run.Shell != "bash" {
			t.Errorf("job %s overrides the default shell with %q", name, job.Defaults.Run.Shell)
		}
		for _, step := range job.Steps {
			if step.Shell != "" && step.Shell != "bash" {
				t.Errorf("job %s has a step running under %q", name, step.Shell)
			}
			if strings.Contains(step.Run, "netdoc-sim triage") && strings.Contains(step.Run, "|") {
				piped = true
			}
		}
	}
	if !piped {
		t.Skip("triage is no longer piped, so pipefail no longer decides the step's exit status")
	}
}

func TestHuntWorkflowFansOutTheCompleteTriageCampaign(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "hunt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		ID   string            `yaml:"id"`
		Name string            `yaml:"name"`
		Uses string            `yaml:"uses"`
		Env  map[string]string `yaml:"env"`
		Run  string            `yaml:"run"`
	}
	type job struct {
		If       string            `yaml:"if"`
		Needs    []string          `yaml:"needs"`
		Outputs  map[string]string `yaml:"outputs"`
		Strategy struct {
			FailFast bool `yaml:"fail-fast"`
			Matrix   struct {
				Shard []int `yaml:"shard"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []step `yaml:"steps"`
	}
	var workflow struct {
		Env struct {
			Shards    string `yaml:"HUNT_SHARDS"`
			Baselines string `yaml:"HUNT_BASELINES"`
		} `yaml:"env"`
		Jobs map[string]job `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(blob, &workflow); err != nil {
		t.Fatal(err)
	}
	seedJob, ok := workflow.Jobs["seed"]
	if !ok || seedJob.Outputs["value"] != `${{ steps.resolve.outputs.value }}` {
		t.Fatalf("workflow seed job = %+v", seedJob)
	}
	hunt, ok := workflow.Jobs["hunt"]
	if !ok {
		t.Fatal("workflow has no hunt matrix job")
	}
	shardCount, err := strconv.Atoi(workflow.Env.Shards)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hunt.Needs, []string{"seed"}) || hunt.Strategy.FailFast ||
		!reflect.DeepEqual(hunt.Strategy.Matrix.Shard, []int{0, 1, 2, 3}) || shardCount != 4 {
		t.Fatalf("matrix shards = %v, count = %d, fail-fast = %t",
			hunt.Strategy.Matrix.Shard, shardCount, hunt.Strategy.FailFast)
	}
	var gotBaselines []string
	for _, line := range strings.Split(strings.TrimSpace(workflow.Env.Baselines), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			t.Fatalf("invalid workflow baseline row %q", line)
		}
		gotBaselines = append(gotBaselines, fields[0])
	}
	baselines := simulation.TriageBaselines()
	want := make([]string, len(baselines))
	for i, baseline := range baselines {
		want[i] = baseline.Scenario
	}
	if !reflect.DeepEqual(gotBaselines, want) {
		t.Fatalf("workflow baselines = %+v, want %+v", gotBaselines, want)
	}
	joinRuns := func(steps []step) string {
		var runs []string
		for _, step := range steps {
			runs = append(runs, step.Run)
		}
		return strings.Join(runs, "\n")
	}
	huntRuns := joinRuns(hunt.Steps)
	if !strings.Contains(huntRuns, `--shard "$SHARD/$HUNT_SHARDS"`) ||
		!strings.Contains(huntRuns, `--cases "$CASES"`) || !strings.Contains(huntRuns, `--seed "$HUNT_SEED"`) ||
		strings.Contains(huntRuns, "date ") || strings.Contains(huntRuns, "netdoc-sim triage") {
		t.Fatalf("hunt matrix does not run one independent shard:\n%s", huntRuns)
	}
	for _, step := range hunt.Steps {
		if step.Name == "Run shard" && step.Env["HUNT_SEED"] != `${{ needs.seed.outputs.value }}` {
			t.Fatalf("hunt shard seed = %q", step.Env["HUNT_SEED"])
		}
	}
	merge, ok := workflow.Jobs["merge"]
	if !ok || !reflect.DeepEqual(merge.Needs, []string{"seed", "hunt"}) || !strings.Contains(merge.If, "always") {
		t.Fatalf("merge job = %+v", merge)
	}
	mergeRuns := joinRuns(merge.Steps)
	if !strings.Contains(mergeRuns, "netdoc-sim hunt merge") ||
		!strings.Contains(mergeRuns, "--hunt-results merged-hunts") ||
		!strings.Contains(mergeRuns, `--seed "$HUNT_SEED"`) || strings.Contains(mergeRuns, "date ") {
		t.Fatalf("merge job does not merge and triage canonical results:\n%s", mergeRuns)
	}
	for _, step := range merge.Steps {
		if step.ID == "triage" && step.Env["HUNT_SEED"] != `${{ needs.seed.outputs.value }}` {
			t.Fatalf("triage seed = %q", step.Env["HUNT_SEED"])
		}
	}
	// `healthy` is a prefix of `healthy-routed-network`, so a shard file name
	// the merge glob can widen hands one baseline's merge another baseline's
	// reports, and every nightly merge fails on the mismatch.
	if !strings.Contains(huntRuns, `output="hunt-results/$base.$SHARD.json"`) ||
		!strings.Contains(mergeRuns, `hunt merge hunt-shards/"$base".*.json`) {
		t.Fatalf("shard artifact naming changed:\n%s\n%s", huntRuns, mergeRuns)
	}
	var shardFiles []string
	for _, baseline := range want {
		for shard := 0; shard < shardCount; shard++ {
			shardFiles = append(shardFiles, baseline+"."+strconv.Itoa(shard)+".json")
		}
	}
	for _, baseline := range want {
		matched := 0
		for _, name := range shardFiles {
			ok, err := filepath.Match(baseline+".*.json", name)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				matched++
			}
		}
		if matched != shardCount {
			t.Errorf("merge glob for %s matched %d shard files, want %d", baseline, matched, shardCount)
		}
	}
	text := string(blob)
	for _, want := range []string{`default: "60"`, "hunt-shard-${{ matrix.shard }}", "name: hunt-results",
		"actions/upload-artifact@", "actions/download-artifact@"} {
		if !strings.Contains(text, want) {
			t.Errorf("workflow is missing %q", want)
		}
	}
}

func TestHuntWorkflowDerivesExplorationSeedFromUTCDate(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "hunt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(blob, &workflow); err != nil {
		t.Fatal(err)
	}
	resolve := ""
	for _, step := range workflow.Jobs["seed"].Steps {
		if step.ID == "resolve" {
			resolve = step.Run
		}
	}
	if resolve == "" {
		t.Fatal("workflow has no exploration seed resolver")
	}
	for _, want := range []string{"seed=$(LC_ALL=C date -u +%Y%m%d)", "[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]", `printf 'value=%s\n' "$seed"`} {
		if !strings.Contains(resolve, want) {
			t.Errorf("seed resolver is missing %q", want)
		}
	}
	seedFor := func(at time.Time) int64 {
		t.Helper()
		value := at.UTC().Format("20060102")
		seed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("seed %q is not an int64: %v", value, err)
		}
		return seed
	}
	first := seedFor(time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC))
	repeat := seedFor(time.Date(2026, 8, 23, 23, 0, 0, 0, time.UTC))
	next := seedFor(time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC))
	if first != 20260823 || repeat != first || next == first || seedFor(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)) <= 0 {
		t.Fatalf("derived seeds = %d, %d, %d", first, repeat, next)
	}
}

func TestCIKeepsFixedSeedHuntRegressionSeparate(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(blob)
	for _, want := range []string{
		"name: Run fixed-seed Hunt regression",
		"-skip '^TestGeneratedHuntPMTUBlackholeCaseReachesThePathMTUProbe$'",
		"-run '^TestGeneratedHuntPMTUBlackholeCaseReachesThePathMTUProbe$'",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("CI workflow is missing %q", want)
		}
	}
}

type workflowDefaults struct {
	Run struct {
		Shell string `yaml:"shell"`
	} `yaml:"run"`
}

// --- the launcher and its real hunt --------------------------------------

// cleanHunt is what a director writes back when a baseline turned up nothing.
func cleanHunt(t *testing.T, cases int) string {
	t.Helper()
	blob, err := json.Marshal(&simulation.HuntResult{Result: simulation.HuntResultClean,
		GeneratorVersion: simulation.HuntGeneratorVersion, ExecutedCases: cases})
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

// triage's hunt is a real `netdoc-sim hunt -json` in its own namespaces. This
// pins the command line it builds: the scenario, its seed, and the flags the
// launcher was given all have to survive into the director's argv, or triage
// reproduces something other than what it hunted.
func TestDirectorHuntBuildsTheHuntItWasAskedFor(t *testing.T) {
	directors := stubDirectors(t, &fakeDirectors{code: exitOK, stdout: cleanHunt(t, 5)})
	hunt := directorHunt("/opt/netdoc-sim", "/opt/netdoc", 9*time.Second, 3, true, io.Discard)

	result, err := hunt(context.Background(), "healthy-routed-network", -7, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != simulation.HuntResultClean || result.ExecutedCases != 5 {
		t.Fatalf("result = %+v", result)
	}
	if len(directors.calls) != 1 || directors.calls[0].self != "/opt/netdoc-sim" {
		t.Fatalf("directors = %+v", directors.calls)
	}
	f, base := receivedHunt(t, directors.calls[0].argv)
	if base != "healthy-routed-network" || !f.seed.set || f.seed.v != -7 || *f.cases != 5 ||
		*f.caseNum != 2 || *f.maxFaults != 3 || *f.timeout != 9*time.Second || *f.netdoc != "/opt/netdoc" {
		t.Errorf("hunt got base %q seed %+v cases %d case %d max-faults %d timeout %s netdoc %q",
			base, f.seed, *f.cases, *f.caseNum, *f.maxFaults, *f.timeout, *f.netdoc)
	}
	// Without -json there is no report to parse, and -v has to reach the hunt
	// or a verbose triage goes quiet exactly where it matters.
	if !*f.json || !*f.verbose {
		t.Errorf("json = %t, v = %t, want both true", *f.json, *f.verbose)
	}
}

// A hunt whose arguments are out of bounds is caught before a namespace is
// created, not after.
func TestDirectorHuntRejectsImpossibleHuntsWithoutRunning(t *testing.T) {
	directors := stubDirectors(t, &fakeDirectors{code: exitOK, stdout: cleanHunt(t, 1)})
	hunt := directorHunt("/opt/netdoc-sim", "/opt/netdoc", time.Second, 2, false, io.Discard)
	if _, err := hunt(context.Background(), "healthy-routed-network", 1, 0, -1); err == nil {
		t.Fatal("a hunt with -cases 0 was accepted")
	} else if !strings.Contains(err.Error(), "-cases must be between 1 and") {
		t.Fatalf("err = %v", err)
	}
	if len(directors.calls) != 0 {
		t.Errorf("a rejected hunt still created namespaces: %+v", directors.calls)
	}
}

// The report is the only place a hunt's reason lives, so a report triage
// cannot read, or one that disagrees with the exit code it arrived with, is an
// error, never a hunt result triage goes on to file issues from.
func TestDirectorHuntRefusesAReportItCannotTrust(t *testing.T) {
	clean := cleanHunt(t, 1)
	broken, err := json.Marshal(&simulation.HuntResult{Result: simulation.HuntResultError,
		ErrorKind: "runtime", Error: "cleanup failed"})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		director fakeDirectors
		want     string
	}{
		"director cannot start": {
			director: fakeDirectors{code: 1, err: errors.New("namespaces unavailable")},
			want:     "namespaces unavailable"},
		"no report after a failure": {
			director: fakeDirectors{code: exitError, stdout: "kernel is on fire\n"},
			want:     "hunt exited 3 without a readable report"},
		"no report after a usage exit": {
			director: fakeDirectors{code: exitUsage, stdout: "unsupported hunt base\n"},
			want:     "hunt exited 2 without a readable report"},
		"no report after a success": {
			director: fakeDirectors{code: exitOK, stdout: "nothing to see\n"},
			want:     "cannot parse the hunt report"},
		"report disagrees with the exit": {
			director: fakeDirectors{code: exitError, stdout: clean},
			want:     `hunt exited 3 but reported "` + simulation.HuntResultClean + `"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			stubDirectors(t, &tt.director)
			hunt := directorHunt("/opt/netdoc-sim", "/opt/netdoc", time.Second, 2, false, io.Discard)
			result, err := hunt(context.Background(), "healthy-routed-network", 1, 1, -1)
			if err == nil {
				t.Fatalf("accepted %+v", result)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
	// A failing exit with a report that admits the failure is the normal way a
	// hunt reports trouble, and triage has to be able to read it.
	t.Run("failure the report agrees with", func(t *testing.T) {
		stubDirectors(t, &fakeDirectors{code: exitError, stdout: string(broken)})
		hunt := directorHunt("/opt/netdoc-sim", "/opt/netdoc", time.Second, 2, false, io.Discard)
		result, err := hunt(context.Background(), "healthy-routed-network", 1, 1, -1)
		if err != nil {
			t.Fatalf("err = %v, want the report", err)
		}
		if result.Result != simulation.HuntResultError || result.Error != "cleanup failed" {
			t.Fatalf("result = %+v", result)
		}
	})
}

// The launcher hunts every selected baseline at its own fixed seed, through a
// real director each time, and reports what came back.
func TestTriageLaunchHuntsEveryBaselineAtItsSeed(t *testing.T) {
	fakeNetdoc(t)
	stubBackends(t, true)
	directors := stubDirectors(t, &fakeDirectors{code: exitOK, stdout: cleanHunt(t, 3)})
	var stdout, stderr bytes.Buffer
	code := run([]string{"triage", "-json", "-netdoc", "./netdoc", "-cases", "3",
		"-revision", "abc123", "-context", "workflow run 42"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("code = %d, want %d (stderr %q)", code, exitOK, stderr.String())
	}
	baselines := simulation.TriageBaselines()
	// Nothing was found, so nothing needed reproducing: one hunt per baseline.
	if len(directors.calls) != len(baselines) {
		t.Fatalf("%d hunts for %d baselines: %+v", len(directors.calls), len(baselines), directors.calls)
	}
	for i, baseline := range baselines {
		f, base := receivedHunt(t, directors.calls[i].argv)
		if base != baseline.Scenario || !f.seed.set || f.seed.v != baseline.Seed || *f.cases != 3 {
			t.Errorf("hunt %d ran %q seed %+v cases %d, want %q seed %d cases 3",
				i, base, f.seed, *f.cases, baseline.Scenario, baseline.Seed)
		}
	}
	var report simulation.TriageReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a triage report: %v (%q)", err, stdout.String())
	}
	if report.Revision != "abc123" || report.Context != "workflow run 42" ||
		report.Result != simulation.TriageResultClean || len(report.Baselines) != len(baselines) {
		t.Fatalf("report = %+v", report)
	}
	if report.Baselines[0].Cases != 3 || report.Baselines[0].Scenario != baselines[0].Scenario {
		t.Fatalf("baseline = %+v", report.Baselines[0])
	}
}

// Without -json a human gets the text report, and -scenarios narrows the run.
func TestTriageLaunchWritesTheTextReportForOneBaseline(t *testing.T) {
	fakeNetdoc(t)
	stubBackends(t, true)
	directors := stubDirectors(t, &fakeDirectors{code: exitOK, stdout: cleanHunt(t, 2)})
	var stdout, stderr bytes.Buffer
	code := run([]string{"triage", "-netdoc", "./netdoc", "-cases", "2", "-scenarios", "healthy"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if len(directors.calls) != 1 {
		t.Fatalf("directors = %+v, want just the one selected baseline", directors.calls)
	}
	if json.Valid(bytes.TrimSpace(stdout.Bytes())) || stdout.Len() == 0 {
		t.Errorf("stdout = %q, want the text report", stdout.String())
	}
}

// A hunt that could not run is a failed triage, and the reason survives into
// the report a workflow reads.
func TestTriageLaunchFailsWhenAHuntCannotRun(t *testing.T) {
	fakeNetdoc(t)
	stubBackends(t, true)
	stubDirectors(t, &fakeDirectors{code: 1, err: errors.New("namespaces unavailable")})
	var stdout, stderr bytes.Buffer
	code := run([]string{"triage", "-json", "-netdoc", "./netdoc", "-scenarios", "healthy"}, &stdout, &stderr)
	if code != exitError {
		t.Fatalf("code = %d, want %d", code, exitError)
	}
	var report simulation.TriageReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Result != simulation.TriageResultError || !strings.Contains(report.Error, "namespaces unavailable") {
		t.Fatalf("report = %+v", report)
	}
}

// Everything the launcher can check before hunting anything, it checks.
func TestTriageLaunchStopsBeforeHunting(t *testing.T) {
	runRejections(t, []dispatchCase{
		{name: "help", supported: true, args: []string{"triage", "-h"}, code: exitOK,
			stdoutHas: "Usage: netdoc-sim <command> [arguments]"},
		{name: "unknown baseline", supported: true, args: []string{"triage", "-scenarios", "no-such-baseline"},
			code: exitUsage, stderrHas: "unsupported triage baseline"},
		{name: "host cannot simulate", supported: false, args: []string{"triage", "-netdoc", "./netdoc"},
			code: exitError, stderrHas: "stub backend: this host cannot simulate"},
		{name: "no netdoc anywhere", supported: true, args: []string{"triage"}, code: exitUsage,
			stderrHas: "cannot find the netdoc binary"},
	})
}
