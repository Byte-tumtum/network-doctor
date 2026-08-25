// Package report defines netdoc's stable machine-readable report contract.
package report

// Report is the stable JSON object emitted by netdoc -json. Its field names
// and status vocabulary (PASS/WARN/FAIL/SKIP/N/A) are compatibility-sensitive.
type Report struct {
	Version string `json:"version"`
	// Ts is set only under -json -watch, where the output is a stream and each
	// pass needs to say when it ran. A single report doesn't carry one, so the
	// one-shot output stays byte-identical to what it has always been.
	Ts      string  `json:"ts,omitempty"`
	Target  *Target `json:"target"` // null in generic (no-target) mode
	Checks  []Check `json:"checks"`
	Summary string  `json:"summary"`
	Verdict string  `json:"verdict"`
	// FailedStage is the id of the first check that failed, omitted when none
	// did, the one field a CI job needs to route a bug report.
	FailedStage string `json:"failed_stage,omitempty"`
	// Findings are the specific conclusions the diagnosis drew, most important
	// first. Omitted when the run reached none, which is the healthy case and
	// also the run that is impaired in no way worth naming: summary and verdict
	// answer for those, and inventing an identity for them would be a claim the
	// probes did not support.
	Findings []Finding `json:"findings,omitempty"`
	OK       bool      `json:"ok"`
}

// Finding is one thing the run proved wrong, in the report's own vocabulary.
// It is netdoc's conclusion about a network, not to be confused with the
// simulator's hunt findings, which are defects found in netdoc itself.
//
// ID is the stable identity to branch on; Focus is the check id whose detail
// and fix belong to this conclusion, so the remedy stays where it was written
// instead of being copied into a second catalogue; Evidence names the checks
// the conclusion was drawn from, Focus first.
type Finding struct {
	ID       string   `json:"id"`
	Focus    string   `json:"focus,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	// CausalEvidence states how each observed check fact bears on this finding
	// or an alternative. Evidence above remains the compatible row-ID
	// projection for existing consumers.
	CausalEvidence []CausalEvidence `json:"causal_evidence,omitempty"`
	Counterfactual *Counterfactual  `json:"counterfactual,omitempty"`
	// Remediation is what to do about this finding, as data rather than prose
	// to be parsed out of fix. Omitted when the conclusion has no advice
	// beyond what the checks already say.
	Remediation *Remediation `json:"remediation,omitempty"`
}

// CausalEvidence is one typed relationship to an observation in Checks.
// Candidate is present only for contradiction and ruled_out items. Reason is
// present only when the relevant check was not evaluated.
type CausalEvidence struct {
	Kind        string `json:"kind"`
	Check       string `json:"check"`
	Observation string `json:"observation,omitempty"`
	Value       string `json:"value,omitempty"`
	Candidate   string `json:"candidate,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type Counterfactual struct {
	Variable     string                      `json:"variable"`
	Alternatives []CounterfactualAlternative `json:"alternatives"`
}

type CounterfactualAlternative struct {
	Value    string           `json:"value"`
	Outcome  string           `json:"outcome"`
	Evidence []CausalEvidence `json:"evidence"`
}

// Remediation is the structured next action for a finding: an additive object
// that gives automation the same advice the app shows, without parsing fix or
// detail.
//
// Command is an argv, never a shell string, and netdoc never runs it: it is a
// safe read-only command offered for a person to run and read. Absent when the
// advice is prose alone.
type Remediation struct {
	ID      string   `json:"id"`
	Action  string   `json:"action"`
	Why     string   `json:"why,omitempty"`
	Steps   []string `json:"steps,omitempty"`
	Command []string `json:"command,omitempty"`
	Expect  string   `json:"expect,omitempty"`
}

type Target struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type Check struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Cause      string    `json:"cause,omitempty"`
	Families   *Families `json:"address_families,omitempty"`
	Ms         int64     `json:"ms"` // wall time, truncated but floored at 1; 0 means the check never ran
	Detail     string    `json:"detail"`
	Fix        string    `json:"fix,omitempty"`
	Addrs      []string  `json:"addrs,omitempty"`
	SelectedIP string    `json:"selected_ip,omitempty"`
	Source     string    `json:"source,omitempty"`
	Iface      string    `json:"iface,omitempty"`
	Network    string    `json:"network,omitempty"`
	Portal     *Portal   `json:"portal,omitempty"`
	Attempts   []Attempt `json:"attempts,omitempty"`
}

// A family the selected --iface source has no address for was never dialed, so
// its state is empty and the key is omitted rather than serialized as "": an
// absent family reads as untested, which is what it is.
type Families struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

type Portal struct {
	RedirectURL string `json:"redirect_url,omitempty"`
}

type Attempt struct {
	IP      string `json:"ip"`
	Ms      int64  `json:"ms"` // same flooring as Check.Ms
	Err     string `json:"error,omitempty"`
	Cause   string `json:"cause,omitempty"`
	Aborted bool   `json:"aborted,omitempty"`
}
