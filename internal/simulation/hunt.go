package simulation

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	HuntResultClean     = "clean"
	HuntResultFindings  = "findings"
	HuntResultError     = "error"
	HuntResultCancelled = "cancelled"
)

type HuntOptions struct {
	Run       Options
	Cases     int
	Seed      int64
	Case      *int
	MaxFaults int
	FailFast  bool
	DryRun    bool
}

type HuntCaseResult struct {
	Manifest             GeneratedCaseManifest `json:"manifest"`
	Truth                ObservedTruth         `json:"truth"`
	TruthFingerprint     string                `json:"truth_fingerprint"`
	DiagnosisFingerprint DiagnosisFingerprint  `json:"diagnosis_fingerprint"`
	Findings             []HuntCaseFinding     `json:"findings"`
	Report               *Report               `json:"report,omitempty"`
	Status               string                `json:"status"`
}

type HuntSuggestion struct {
	Code            string           `json:"code"`
	Description     string           `json:"description"`
	Evidence        int              `json:"evidence_cases"`
	HighestSeverity HuntSeverity     `json:"highest_severity"`
	FirstCase       int              `json:"first_case"`
	ExampleCases    []int            `json:"example_cases"`
	Reproduce       HuntReproduction `json:"reproduce"`
}

type HuntResult struct {
	GeneratorVersion    string           `json:"generator_version"`
	BaseScenario        string           `json:"base_scenario"`
	HuntSeed            int64            `json:"hunt_seed"`
	RequestedCases      int              `json:"requested_cases"`
	GeneratedCases      int              `json:"generated_cases"`
	ExecutedCases       int              `json:"executed_cases"`
	UniqueCases         int              `json:"unique_cases"`
	DuplicateCandidates int              `json:"duplicate_candidates"`
	CleanCases          int              `json:"clean_cases"`
	Findings            []HuntFinding    `json:"findings"`
	Suggestions         []HuntSuggestion `json:"suggestions"`
	Cases               []HuntCaseResult `json:"case_results"`
	FailFastStopped     bool             `json:"fail_fast_stopped"`
	Cancelled           bool             `json:"cancelled"`
	RuntimeFailure      bool             `json:"runtime_failure"`
	Result              string           `json:"result"`
	ErrorKind           string           `json:"error_kind,omitempty"`
	Error               string           `json:"error,omitempty"`
}

func (o *HuntOptions) withDefaults() error {
	if o.Cases == 0 {
		o.Cases = 50
	}
	if o.MaxFaults == 0 {
		o.MaxFaults = 2
	}
	if o.Cases < 1 || o.Cases > HuntMaxCases {
		return fmt.Errorf("cases must be between 1 and %d", HuntMaxCases)
	}
	if o.MaxFaults < 1 || o.MaxFaults > HuntMaxFaults {
		return fmt.Errorf("max faults must be between 1 and %d", HuntMaxFaults)
	}
	if o.Case != nil && (*o.Case < 0 || *o.Case > HuntMaxCaseNumber) {
		return fmt.Errorf("case must be between 0 and %d", HuntMaxCaseNumber)
	}
	return nil
}

// RunHunt generates cases independently, skips semantic duplicates in batch
// mode, and runs each accepted case sequentially through the normal simulator.
func RunHunt(ctx context.Context, baseID string, base *Scenario, backend func() Backend, opts HuntOptions) *HuntResult {
	result := &HuntResult{GeneratorVersion: HuntGeneratorVersion, BaseScenario: baseID, HuntSeed: opts.Seed}
	if err := opts.withDefaults(); err != nil {
		result.Result, result.ErrorKind, result.Error = HuntResultError, "configuration", err.Error()
		result.finish()
		return result
	}
	result.RequestedCases = opts.Cases
	if opts.Case != nil {
		result.RequestedCases = 1
	}
	if !opts.DryRun && backend == nil {
		result.Result, result.ErrorKind, result.Error = HuntResultError, "configuration", "hunt backend is nil"
		result.finish()
		return result
	}
	seen := make(map[string]bool, result.RequestedCases)
	candidate := 0
	maxCandidates := result.RequestedCases * 20
	if opts.Case != nil {
		candidate, maxCandidates = *opts.Case, 1
	}
	for attempts := 0; attempts < maxCandidates && len(result.Cases) < result.RequestedCases; attempts++ {
		if err := ctx.Err(); err != nil {
			result.Cancelled, result.RuntimeFailure = true, true
			result.Result, result.ErrorKind, result.Error = HuntResultCancelled, "cancellation", err.Error()
			break
		}
		caseNumber := candidate
		candidate++
		if caseNumber > HuntMaxCaseNumber {
			break
		}
		generated, err := GenerateHuntCase(baseID, base, opts.Seed, caseNumber, opts.MaxFaults)
		if err != nil {
			result.Result, result.ErrorKind = HuntResultError, FindingGeneratorDefect
			result.Error = fmt.Sprintf("case %d: %v", caseNumber, err)
			break
		}
		if opts.Case == nil && seen[generated.Manifest.CaseFingerprint] {
			result.DuplicateCandidates++
			continue
		}
		seen[generated.Manifest.CaseFingerprint] = true
		item := HuntCaseResult{Manifest: generated.Manifest, Status: "generated",
			Truth: collectObservedTruth(generated.Manifest, nil), Findings: []HuntCaseFinding{},
			DiagnosisFingerprint: DiagnosisFingerprint{Verdicts: []string{}, Probes: []ProbeFingerprint{}}}
		result.GeneratedCases++
		if !opts.DryRun {
			report := Run(ctx, generated.Scenario, backend(), opts.Run)
			item.Report = report
			item.Truth = collectObservedTruth(generated.Manifest, report)
			item.TruthFingerprint = truthFingerprint(item.Truth)
			item.DiagnosisFingerprint = diagnosisFingerprint(report)
			item.Findings = analyzeHuntCase(generated.Manifest, report, item.Truth)
			item.Status = "clean"
			if len(item.Findings) > 0 {
				item.Status = "findings"
			}
			result.ExecutedCases++
			if report.Error != "" || !report.Cleanup.Done {
				result.RuntimeFailure = true
				item.Status = "runtime_error"
			}
		}
		result.Cases = append(result.Cases, item)
		if opts.FailFast && len(item.Findings) > 0 {
			result.FailFastStopped = true
			break
		}
		if opts.Case != nil {
			break
		}
	}
	result.UniqueCases = len(seen)
	if result.Error == "" && !result.FailFastStopped && len(result.Cases) < result.RequestedCases {
		result.Result, result.ErrorKind = HuntResultError, FindingGeneratorDefect
		result.Error = fmt.Sprintf("generated %d unique cases after %d bounded candidates; requested %d",
			len(result.Cases), maxCandidates, result.RequestedCases)
	}
	if !opts.DryRun {
		addTruthInstabilityFindings(result.Cases)
	}
	result.finish()
	return result
}

func (r *HuntResult) finish() {
	if r.Cases == nil {
		r.Cases = []HuntCaseResult{}
	}
	for i := range r.Cases {
		if r.Cases[i].Findings == nil {
			r.Cases[i].Findings = []HuntCaseFinding{}
		}
		if r.Cases[i].Status == "clean" {
			r.CleanCases++
		}
	}
	r.Findings = aggregateHuntFindings(r.Cases)
	r.Suggestions = aggregateHuntSuggestions(r.Findings)
	if r.Findings == nil {
		r.Findings = []HuntFinding{}
	}
	if r.Suggestions == nil {
		r.Suggestions = []HuntSuggestion{}
	}
	if r.Result == HuntResultError || r.Result == HuntResultCancelled {
		return
	}
	switch {
	case r.RuntimeFailure:
		r.Result = HuntResultError
		if r.ErrorKind == "" {
			r.ErrorKind = "runtime"
		}
	case len(r.Findings) > 0:
		r.Result = HuntResultFindings
	default:
		r.Result = HuntResultClean
	}
}

func aggregateHuntSuggestions(findings []HuntFinding) []HuntSuggestion {
	byCode := make(map[string]*HuntSuggestion)
	for _, finding := range findings {
		if finding.SuggestionCode == "" {
			continue
		}
		item := byCode[finding.SuggestionCode]
		if item == nil {
			item = &HuntSuggestion{Code: finding.SuggestionCode, Description: finding.Summary,
				HighestSeverity: finding.Severity, FirstCase: finding.FirstCase, Reproduce: finding.Reproduce}
			byCode[finding.SuggestionCode] = item
		}
		item.Evidence += finding.Occurrences
		if severityRank(finding.Severity) > severityRank(item.HighestSeverity) {
			item.HighestSeverity = finding.Severity
		}
		for _, caseNumber := range finding.ExampleCases {
			if len(item.ExampleCases) >= 3 || containsInt(item.ExampleCases, caseNumber) {
				continue
			}
			item.ExampleCases = append(item.ExampleCases, caseNumber)
		}
	}
	out := make([]HuntSuggestion, 0, len(byCode))
	for _, item := range byCode {
		sort.Ints(item.ExampleCases)
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if severityRank(out[i].HighestSeverity) != severityRank(out[j].HighestSeverity) {
			return severityRank(out[i].HighestSeverity) > severityRank(out[j].HighestSeverity)
		}
		if out[i].Evidence != out[j].Evidence {
			return out[i].Evidence > out[j].Evidence
		}
		return strings.Compare(out[i].Code, out[j].Code) < 0
	})
	return out
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
