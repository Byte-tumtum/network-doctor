package simulation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type HuntSeverity string

const (
	SeverityCritical HuntSeverity = "critical"
	SeverityHigh     HuntSeverity = "high"
	SeverityMedium   HuntSeverity = "medium"
	SeverityLow      HuntSeverity = "low"
	SeverityInfo     HuntSeverity = "info"
)

const (
	FindingFalsePositive           = "comparison_false_positive"
	FindingFalseNegative           = "comparison_false_negative"
	FindingDiagnosticInstability   = "diagnostic_instability"
	FindingDiagnosticContradiction = "diagnostic_contradiction"
	FindingCoverageGap             = "coverage_gap"
	FindingUnexpectedRuntimeError  = "unexpected_runtime_error"
	FindingProbeTimeout            = "probe_timeout"
	FindingNetdocCrash             = "netdoc_crash"
	FindingNetdocHang              = "netdoc_hang"
	FindingCleanupFailure          = "cleanup_failure"
	FindingSimulatorFailure        = "simulator_failure"
	FindingGeneratorDefect         = "generator_defect"
)

// ObservedTruth is deliberately coarse. It records only simulator evidence,
// not conclusions inferred from the mutation request or copied from netdoc.
type ObservedTruth struct {
	DNS            string   `json:"dns"`
	IPv4           string   `json:"ipv4"`
	IPv6           string   `json:"ipv6"`
	Gateway        string   `json:"gateway"`
	Proxy          string   `json:"proxy"`
	TLS            string   `json:"tls"`
	TCP            string   `json:"tcp"`
	Link           string   `json:"link"`
	Packet         string   `json:"packet"`
	Route          string   `json:"route"`
	ObservedFaults []string `json:"observed_faults"`
}

type HuntReproduction struct {
	BaseScenario     string `json:"base_scenario"`
	Seed             int64  `json:"seed"`
	Case             int    `json:"case"`
	CaseSeed         int64  `json:"case_seed"`
	GeneratorVersion string `json:"generator_version"`
	CaseFingerprint  string `json:"case_fingerprint"`
}

type HuntCaseFinding struct {
	Fingerprint    string           `json:"fingerprint"`
	Category       string           `json:"category"`
	Severity       HuntSeverity     `json:"severity"`
	Code           string           `json:"code"`
	SuggestionCode string           `json:"suggestion_code,omitempty"`
	Probe          string           `json:"probe,omitempty"`
	Expected       string           `json:"expected,omitempty"`
	Actual         string           `json:"actual,omitempty"`
	Cause          string           `json:"cause,omitempty"`
	Family         string           `json:"family,omitempty"`
	Summary        string           `json:"summary"`
	Evidence       string           `json:"evidence,omitempty"`
	Reproduce      HuntReproduction `json:"reproduce"`
}

type HuntFinding struct {
	Fingerprint    string           `json:"fingerprint"`
	Category       string           `json:"category"`
	Severity       HuntSeverity     `json:"severity"`
	Code           string           `json:"code"`
	SuggestionCode string           `json:"suggestion_code,omitempty"`
	Probe          string           `json:"probe,omitempty"`
	Expected       string           `json:"expected,omitempty"`
	Actual         string           `json:"actual,omitempty"`
	Cause          string           `json:"cause,omitempty"`
	Family         string           `json:"family,omitempty"`
	Summary        string           `json:"summary"`
	Evidence       string           `json:"evidence,omitempty"`
	Occurrences    int              `json:"occurrences"`
	FirstCase      int              `json:"first_case"`
	ExampleCases   []int            `json:"example_cases"`
	Reproduce      HuntReproduction `json:"reproduce"`
}

func severityRank(severity HuntSeverity) int {
	switch severity {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func collectObservedTruth(manifest GeneratedCaseManifest, report *Report) ObservedTruth {
	truth := ObservedTruth{DNS: "unknown", IPv4: "unknown", IPv6: "unknown", Gateway: "unknown",
		Proxy: "unknown", TLS: "unknown", TCP: "unknown", Link: "unknown", Packet: "unknown", Route: "unknown"}
	if report == nil {
		truth.ObservedFaults = []string{}
		return truth
	}
	answered, dnsFailed := false, false
	for _, query := range report.Evidence.DNSQueries {
		switch query.ActualOutcome {
		case "ANSWER", "NODATA":
			answered = true
		case "DROPPED", "SERVFAIL":
			dnsFailed = true
		}
	}
	switch {
	case answered && dnsFailed:
		truth.DNS = "mixed"
	case dnsFailed:
		truth.DNS = "unavailable"
	case answered:
		truth.DNS = "available"
	}
	selectedRoute := false
	for _, route := range report.Evidence.Routes {
		if !route.Selected {
			continue
		}
		selectedRoute = true
		truth.Route = "selected"
		if route.GatewayReachable != nil {
			if *route.GatewayReachable {
				truth.Gateway = "reachable"
			} else {
				truth.Gateway = "unreachable"
			}
		}
	}
	if !selectedRoute && len(report.Evidence.Routes) > 0 {
		truth.Route = "not_selected"
	}
	if len(report.Evidence.SOCKSRequests) > 0 {
		truth.Proxy = "reached"
		for _, item := range report.Evidence.SOCKSRequests {
			if item.Event == "connect" && item.Result != "connected" {
				truth.Proxy = "failed"
			}
		}
	}
	if len(report.Evidence.TLS) > 0 {
		truth.TLS = "observed"
		for _, item := range report.Evidence.TLS {
			if item.Result != "passed" {
				truth.TLS = "failed"
			}
		}
	}
	if len(report.Evidence.TCPResets) > 0 {
		truth.TCP = "reset"
	}
	linkDown, linkRecovered := false, false
	for _, event := range report.Timeline {
		if event.Result != EventApplied || event.Event.Type != FaultScheduledLink {
			continue
		}
		if event.Event.State == LinkStateDown {
			linkDown = true
		}
		if linkDown && event.Event.State == LinkStateUp {
			linkRecovered = true
		}
	}
	switch {
	case linkRecovered:
		truth.Link = "transient_loss"
	case linkDown:
		truth.Link = "down"
	default:
		for _, link := range report.Evidence.Links {
			if !link.Up {
				truth.Link = "down"
				break
			}
		}
		if truth.Link == "unknown" && len(report.Evidence.Links) > 0 {
			truth.Link = "up"
		}
	}
	packetActive, packetDropped := false, false
	for _, condition := range report.Evidence.PacketConditions {
		packetActive = packetActive || condition.Active
		packetDropped = packetDropped || condition.DroppedPackets > 0
	}
	switch {
	case packetDropped:
		truth.Packet = "drops_observed"
	case packetActive:
		truth.Packet = "impairment_active"
	}
	for _, mutation := range manifest.Mutations {
		if mutationObserved(mutation, report, truth) {
			truth.ObservedFaults = append(truth.ObservedFaults, mutation.ID)
		}
	}
	sort.Strings(truth.ObservedFaults)
	if truth.ObservedFaults == nil {
		truth.ObservedFaults = []string{}
	}
	return truth
}

func mutationObserved(mutation GeneratedMutation, report *Report, truth ObservedTruth) bool {
	switch mutation.ID {
	case "netem.loss", "netem.latency", "netem.jitter":
		for _, fault := range report.Faults {
			if fault.Type == FaultNetem && fault.Node == mutation.Node {
				return true
			}
		}
	case "timeline.netem_spike":
		return timelineApplied(report, FaultScheduledNetem, mutation.Node, "")
	case "dns.servfail", "dns.drop", "timeline.dns_outage":
		return timelineApplied(report, FaultScheduledDNS, "", mutation.Service)
	case "service.tcp_reset":
		return truth.TCP == "reset"
	case "family.ipv4_drop", "family.ipv6_drop":
		for _, fault := range report.Faults {
			if fault.Type == FaultDrop && fault.Family == mutation.Family {
				return true
			}
		}
	case "link.transient_down":
		return timelineApplied(report, FaultScheduledLink, mutation.Node, "")
	case "routing.preferred_path_failure":
		for _, fault := range report.Faults {
			if fault.Type == FaultLinkDown && fault.Node == mutation.Node {
				return true
			}
		}
	}
	return false
}

func timelineApplied(report *Report, kind, node, service string) bool {
	for _, event := range report.Timeline {
		if event.Result == EventApplied && event.Event.Type == kind &&
			(node == "" || event.Event.Node == node) && (service == "" || event.Event.Service == service) {
			return true
		}
	}
	return false
}

func truthFingerprint(truth ObservedTruth) string {
	blob, _ := json.Marshal(truth)
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func reproductionFor(manifest GeneratedCaseManifest) HuntReproduction {
	return HuntReproduction{BaseScenario: manifest.BaseScenario, Seed: manifest.HuntSeed, Case: manifest.Case,
		CaseSeed: manifest.CaseSeed, GeneratorVersion: manifest.GeneratorVersion, CaseFingerprint: manifest.CaseFingerprint}
}

func analyzeHuntCase(manifest GeneratedCaseManifest, report *Report, truth ObservedTruth) []HuntCaseFinding {
	var findings []HuntCaseFinding
	add := func(f HuntCaseFinding) {
		f.Reproduce = reproductionFor(manifest)
		f.Fingerprint = huntFindingFingerprint(f)
		findings = append(findings, f)
	}
	if report == nil {
		return findings
	}
	if report.Error != "" {
		add(HuntCaseFinding{Category: FindingSimulatorFailure, Severity: SeverityCritical, Code: "simulation_run_failed",
			Summary: "The simulator could not complete the generated case.", Evidence: report.Error})
	}
	if !report.Cleanup.Done {
		add(HuntCaseFinding{Category: FindingCleanupFailure, Severity: SeverityCritical, Code: "simulation_cleanup_failed",
			Summary: "Simulation resources did not clean up completely.", Evidence: strings.Join(report.Cleanup.Errors, "; ")})
	}
	if report.Error != "" || !report.Cleanup.Done {
		return findings
	}
	for _, event := range report.Timeline {
		if event.Result == EventFailed {
			add(HuntCaseFinding{Category: FindingSimulatorFailure, Severity: SeverityHigh, Code: "scheduled_fault_failed",
				Summary: "A generated timed fault failed to reach the simulated network.", Evidence: event.State + ": " + event.Error})
		}
	}
	for _, test := range report.Tests {
		switch test.ProcessOutcome {
		case ProcessTimedOut:
			add(HuntCaseFinding{Category: FindingNetdocHang, Severity: SeverityCritical, Code: "netdoc_process_deadline",
				Summary: "The whole netdoc process exceeded the simulator deadline.", Evidence: test.Name})
		case ProcessSignaled:
			add(HuntCaseFinding{Category: FindingNetdocCrash, Severity: SeverityCritical, Code: "netdoc_signal_termination",
				Summary: "The netdoc process terminated from a signal.", Actual: test.Signal, Evidence: test.Name})
		case ProcessCancelled:
			add(HuntCaseFinding{Category: FindingSimulatorFailure, Severity: SeverityHigh, Code: "netdoc_cancelled",
				Summary: "The generated run was cancelled before netdoc completed.", Evidence: test.Name})
		case ProcessExecError:
			add(HuntCaseFinding{Category: FindingUnexpectedRuntimeError, Severity: SeverityHigh, Code: "netdoc_exec_error",
				Summary: "The simulator could not execute netdoc.", Evidence: test.Error})
		}
		if test.Diagnosis == nil && test.Error != "" && test.ProcessOutcome == ProcessExited {
			add(HuntCaseFinding{Category: FindingUnexpectedRuntimeError, Severity: SeverityHigh, Code: "netdoc_invalid_report",
				Summary: "Netdoc exited without a valid structured diagnosis.", Evidence: test.Error})
		}
		if test.Diagnosis != nil && dnsFailureDuring(test, report.Evidence.DNSQueries) {
			if check := checkByID(test.Diagnosis, "dns"); check != nil && check.Status == "PASS" {
				add(HuntCaseFinding{Category: FindingFalseNegative, Severity: SeverityHigh, Code: "observed_dns_failure_reported_healthy",
					Probe: "dns", Expected: "FAIL or WARN", Actual: check.Status,
					Summary: "The resolver service observed failed queries while netdoc reported DNS healthy.", Evidence: check.Detail})
			}
		}
	}
	if truth.TCP == "reset" && !diagnosisClassifiedReset(report) {
		add(HuntCaseFinding{Category: FindingCoverageGap, Severity: SeverityLow, Code: "tcp_reset_not_distinguished",
			Probe: protocolProbe(report), Expected: "connection_reset",
			Summary:  "The simulator observed a TCP reset, but no protocol diagnosis classified it as a reset.",
			Evidence: "TCP reset service recorded connection_reset"})
	}
	for _, suggestion := range report.Suggestions {
		category, severity, ok := huntSuggestionClass(suggestion.Code)
		if !ok {
			continue
		}
		add(HuntCaseFinding{Category: category, Severity: severity, Code: suggestion.Code, SuggestionCode: suggestion.Code,
			Probe: suggestion.Probe, Cause: suggestion.Cause, Summary: suggestion.Message, Evidence: suggestion.Evidence})
	}
	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		return findings[i].Fingerprint < findings[j].Fingerprint
	})
	return findings
}

// dnsFailureDuring reports whether a resolver was still refusing netdoc's
// queries by the end of this run. Judging it on the last query per service, not
// on any query, is what separates a missed failure from a resolver that dropped
// one query, recovered, and answered the resample: reporting PASS after that is
// the truth about the run, not a false negative.
func dnsFailureDuring(test TestOutcome, queries []DNSQueryEvidence) bool {
	last := map[string]DNSQueryEvidence{}
	for _, query := range queries {
		if query.Offset < test.StartOffset || query.Offset >= test.EndOffset {
			continue
		}
		if prev, seen := last[query.Service]; !seen || query.Offset > prev.Offset {
			last[query.Service] = query
		}
	}
	for _, query := range last {
		if query.ActualOutcome == "DROPPED" || query.ActualOutcome == "SERVFAIL" {
			return true
		}
	}
	return false
}

func diagnosisClassifiedReset(report *Report) bool {
	for _, test := range report.Tests {
		if test.Diagnosis == nil {
			continue
		}
		for _, check := range test.Diagnosis.Checks {
			if check.Cause == "connection_reset" {
				return true
			}
		}
	}
	return false
}

func protocolProbe(report *Report) string {
	for _, test := range report.Tests {
		if test.Diagnosis == nil {
			continue
		}
		for _, check := range test.Diagnosis.Checks {
			switch check.ID {
			case "http", "https", "ssh_banner", "smtp_banner", "tls":
				return check.ID
			}
		}
	}
	return "target_tcp"
}

func huntSuggestionClass(code string) (string, HuntSeverity, bool) {
	switch code {
	case SuggestTransientNotResampled:
		return FindingCoverageGap, SeverityMedium, true
	case SuggestTransientReportedPermanent:
		return FindingCoverageGap, SeverityLow, true
	case SuggestTransientMissed:
		return FindingCoverageGap, SeverityLow, true
	case SuggestTimelineInconsistent:
		return FindingDiagnosticContradiction, SeverityHigh, true
	case "jitter_sampling_gap":
		return FindingCoverageGap, SeverityLow, true
	case "alternate_route_available", "wrong_default_route_evidence":
		return FindingCoverageGap, SeverityMedium, true
	case "gateway_unreachable":
		return FindingCoverageGap, SeverityInfo, true
	case SuggestNondeterministic:
		return FindingDiagnosticInstability, SeverityMedium, true
	default:
		return "", "", false
	}
}

func huntFindingFingerprint(f HuntCaseFinding) string {
	semantic := struct {
		Category string `json:"category"`
		Code     string `json:"code"`
		Probe    string `json:"probe,omitempty"`
		Expected string `json:"expected,omitempty"`
		Actual   string `json:"actual,omitempty"`
		Cause    string `json:"cause,omitempty"`
		Family   string `json:"family,omitempty"`
	}{f.Category, f.Code, f.Probe, f.Expected, f.Actual, f.Cause, f.Family}
	blob, _ := json.Marshal(semantic)
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func aggregateHuntFindings(cases []HuntCaseResult) []HuntFinding {
	byFingerprint := make(map[string]*HuntFinding)
	for _, item := range cases {
		for _, finding := range item.Findings {
			aggregate := byFingerprint[finding.Fingerprint]
			if aggregate == nil {
				aggregate = &HuntFinding{Fingerprint: finding.Fingerprint, Category: finding.Category,
					Severity: finding.Severity, Code: finding.Code, SuggestionCode: finding.SuggestionCode,
					Probe: finding.Probe, Expected: finding.Expected, Actual: finding.Actual, Cause: finding.Cause,
					Family: finding.Family, Summary: finding.Summary, Evidence: finding.Evidence,
					FirstCase: item.Manifest.Case, Reproduce: finding.Reproduce}
				byFingerprint[finding.Fingerprint] = aggregate
			}
			aggregate.Occurrences++
			if len(aggregate.ExampleCases) < 3 {
				aggregate.ExampleCases = append(aggregate.ExampleCases, item.Manifest.Case)
			}
		}
	}
	out := make([]HuntFinding, 0, len(byFingerprint))
	for _, finding := range byFingerprint {
		out = append(out, *finding)
	}
	sort.Slice(out, func(i, j int) bool {
		if severityRank(out[i].Severity) != severityRank(out[j].Severity) {
			return severityRank(out[i].Severity) > severityRank(out[j].Severity)
		}
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

func addTruthInstabilityFindings(cases []HuntCaseResult) {
	type group struct {
		indexes   []int
		diagnoses map[string]bool
	}
	groups := make(map[string]*group)
	for i := range cases {
		if cases[i].DiagnosisFingerprint.ID == "" || cases[i].TruthFingerprint == "" {
			continue
		}
		ids := make([]string, len(cases[i].Manifest.Mutations))
		for j := range cases[i].Manifest.Mutations {
			ids[j] = cases[i].Manifest.Mutations[j].ID
		}
		key := cases[i].TruthFingerprint + "\x00" + strings.Join(ids, ",")
		g := groups[key]
		if g == nil {
			g = &group{diagnoses: make(map[string]bool)}
			groups[key] = g
		}
		g.indexes = append(g.indexes, i)
		g.diagnoses[cases[i].DiagnosisFingerprint.ID] = true
	}
	for _, g := range groups {
		if len(g.indexes) < 2 || len(g.diagnoses) < 2 {
			continue
		}
		index := g.indexes[0]
		manifest := cases[index].Manifest
		finding := HuntCaseFinding{Category: FindingDiagnosticInstability, Severity: SeverityMedium,
			Code:      "truth_equivalent_diagnosis_divergence",
			Summary:   "Equivalent observed truth produced multiple structured diagnosis fingerprints.",
			Evidence:  fmt.Sprintf("%d cases produced %d diagnosis fingerprints", len(g.indexes), len(g.diagnoses)),
			Reproduce: reproductionFor(manifest)}
		finding.Fingerprint = huntFindingFingerprint(finding)
		cases[index].Findings = append(cases[index].Findings, finding)
	}
}
