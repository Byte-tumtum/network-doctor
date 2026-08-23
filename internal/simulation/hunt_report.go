package simulation

import (
	"fmt"
	"io"
	"strings"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

func (r *HuntResult) WriteJSON(w io.Writer) error { return writeJSON(w, r) }

func (r *HuntResult) WriteText(w io.Writer) {
	fmt.Fprintln(w, "Network Doctor bug hunt")
	fmt.Fprintf(w, "\nBase:      %s\nSeed:      %d\nCases:     %d\nGenerator: %s\n",
		textsafe.Clean(r.BaseScenario), r.HuntSeed, r.RequestedCases, r.GeneratorVersion)
	if r.Shard != nil {
		fmt.Fprintf(w, "Shard:     %d/%d (zero-based)\n", r.Shard.Index, r.Shard.Count)
	}
	if r.ExecutedCases == 0 {
		fmt.Fprintf(w, "\nGenerated: %d unique case(s); duplicates skipped: %d\n", r.GeneratedCases, r.DuplicateCandidates)
		for _, item := range r.Cases {
			fmt.Fprintf(w, "\nCase %d  seed %d  fingerprint %s\n", item.Manifest.Case, item.Manifest.CaseSeed, item.Manifest.CaseFingerprint)
			for _, mutation := range item.Manifest.Mutations {
				fmt.Fprintf(w, "  %-28s %s\n", mutation.ID, textsafe.Clean(mutation.Description))
			}
		}
	} else {
		fmt.Fprintf(w, "\nCases executed: %d\nClean:          %d\nFindings:       %d\nDuplicates:     %d\n",
			r.ExecutedCases, r.CleanCases, len(r.Findings), r.DuplicateCandidates)
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(w, "\nFindings")
		for _, finding := range r.Findings {
			fmt.Fprintf(w, "\n%-8s Case %-6d %s\n", strings.ToUpper(string(finding.Severity)), finding.FirstCase, finding.Code)
			fmt.Fprintf(w, "         %s\n", textsafe.Clean(finding.Summary))
			fmt.Fprintf(w, "         occurrences: %d  cases: %s\n", finding.Occurrences, intList(finding.ExampleCases))
			if finding.Evidence != "" {
				fmt.Fprintf(w, "         evidence: %s\n", textsafe.Clean(finding.Evidence))
			}
			fmt.Fprintf(w, "\n         Reproduce:\n           %s\n", finding.Reproduce.Command())
		}
	}
	if len(r.Suggestions) > 0 {
		fmt.Fprintln(w, "\nSuggested netdoc improvements")
		for i, suggestion := range r.Suggestions {
			fmt.Fprintf(w, "\n%d. %s\n", i+1, textsafe.Clean(suggestion.Description))
			fmt.Fprintf(w, "   code: %s  evidence: %d case(s)  highest severity: %s\n",
				suggestion.Code, suggestion.Evidence, suggestion.HighestSeverity)
			fmt.Fprintf(w, "   first reproduction: %s\n", suggestion.Reproduce.Command())
		}
	}
	if r.FailFastStopped {
		fmt.Fprintln(w, "\nStopped by --fail-fast after the first case with a reportable finding.")
	}
	if r.Error != "" {
		fmt.Fprintf(w, "\nError: %s\n", textsafe.Clean(r.Error))
	}
}

func intList(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	return strings.Join(parts, ", ")
}
