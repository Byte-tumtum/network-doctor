package simulation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
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
// IPv4 and IPv6 describe point-in-time reachability when final evidence was
// collected, after the diagnostic run and fault scheduler had stopped.
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
	truth.IPv4 = observedFamilyTruth(report, "ipv4")
	truth.IPv6 = observedFamilyTruth(report, "ipv6")
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
	for _, event := range report.Evidence.TCPResets {
		if event.Event == "reset" && event.Result == "connection_reset" && event.Count > 0 {
			truth.TCP = "reset"
			break
		}
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

// observedFamilyTruth reads the one measured record for this family and repeats
// its state. It invents nothing: no record means no observation was taken, and
// an unobserved family is unknown, not unavailable — only the holder-side probe
// gets to say a family was absent. Two records leave no single answer.
func observedFamilyTruth(report *Report, family string) string {
	client := observedClient(report)
	if client == "" {
		return "unknown"
	}
	state := "unknown"
	for _, observation := range report.Evidence.FamilyReachability {
		if observation.Node != client || observation.Family != family {
			continue
		}
		if state != "unknown" {
			return "unknown"
		}
		switch observation.State {
		case FamilyStateReachable, FamilyStateUnreachable, FamilyStateUnavailable:
			state = observation.State
		default:
			return "unknown"
		}
	}
	return state
}

func observedClient(report *Report) string {
	client := ""
	for _, node := range report.Topology {
		if node.Role != "client" {
			continue
		}
		if client != "" {
			return ""
		}
		client = node.Name
	}
	return client
}

func mutationObserved(mutation GeneratedMutation, report *Report, truth ObservedTruth) bool {
	switch mutation.ID {
	case "netem.loss", "netem.latency", "netem.jitter":
		for _, condition := range report.Evidence.PacketConditions {
			if condition.Node != mutation.Node || condition.Segment != mutation.Segment || !condition.Active {
				continue
			}
			latency := time.Duration(mutation.LatencyMS) * time.Millisecond
			jitter := time.Duration(mutation.JitterMS) * time.Millisecond
			if condition.Latency == latency && condition.Jitter == jitter &&
				condition.LossPercent == mutation.LossPercent && condition.Seed == mutation.NetemSeed {
				return true
			}
		}
	case "timeline.netem_spike":
		return timelineApplied(report, func(event TimedEvent) bool {
			return event.Type == FaultScheduledNetem && event.Node == mutation.Node && event.Segment == mutation.Segment &&
				event.Offset == time.Duration(mutation.StartMS)*time.Millisecond &&
				event.Latency == time.Duration(mutation.LatencyMS)*time.Millisecond &&
				event.Jitter == time.Duration(mutation.JitterMS)*time.Millisecond &&
				event.LossPercent == mutation.LossPercent && event.NetemSeed == mutation.NetemSeed
		})
	case "dns.servfail", "dns.drop":
		outcome := DNSOutcomeSERVFAIL
		if mutation.ID == "dns.drop" {
			outcome = DNSOutcomeDrop
		}
		return timelineApplied(report, func(event TimedEvent) bool {
			return event.Type == FaultScheduledDNS && event.Service == mutation.Service && event.Offset == 0 && event.Outcome == outcome
		})
	case "timeline.dns_outage":
		return timelineApplied(report, func(event TimedEvent) bool {
			return event.Type == FaultScheduledDNS && event.Service == mutation.Service &&
				event.Offset == time.Duration(mutation.StartMS)*time.Millisecond && event.Outcome == DNSOutcomeDrop
		})
	case "service.tcp_reset":
		for _, event := range report.Evidence.TCPResets {
			if event.Node == mutation.Node && event.Event == "reset" && event.Result == "connection_reset" && event.Count > 0 {
				return true
			}
		}
	case "service.tls_expired":
		// The handshake, not the service's configured mode: a certificate that
		// was never presented to anyone expired in private. The rejection is
		// what makes it a fault, so the service has to have watched a client
		// refuse the certificate it held out.
		for _, handshake := range report.Evidence.TLS {
			if handshake.Node == mutation.Node && handshake.Service == mutation.Service &&
				handshake.CertificateMode == TLSCertificateExpired && handshake.CertificatePresented &&
				handshake.Result == "client_rejected_certificate" && handshake.Count > 0 {
				return true
			}
		}
	case "proxy.connect_refused":
		for _, request := range report.Evidence.SOCKSRequests {
			if request.Node == mutation.Node && request.Service == mutation.Service && request.Event == "connect" &&
				request.Destination == proxyCONNECTTarget && request.Port == mutation.TargetPort &&
				request.Result == "connection_refused" && request.Count > 0 {
				return true
			}
		}
	case "quic.udp_443_block":
		// The rule's own counter, not the fault record that installed it: an
		// installed rule that never matched a packet blocked nothing.
		for _, drop := range report.Evidence.PacketDrops {
			if drop.Node == mutation.Node && drop.Protocol == "udp" && drop.Port == mutation.TargetPort &&
				drop.Direction == DirectionInbound && drop.Packets > 0 {
				return true
			}
		}
	case "encrypted_dns.doh_invalid":
		for _, reply := range report.Evidence.ServiceReplies {
			if reply.Node == mutation.Node && reply.Service == mutation.Service &&
				reply.Type == ServiceEncryptedDNS && reply.Result == DoHResponseInvalid && reply.Count > 0 {
				return true
			}
		}
	case "http.status_503":
		for _, reply := range report.Evidence.ServiceReplies {
			if reply.Node == mutation.Node && reply.Type == ServiceHTTP && reply.Port == mutation.TargetPort &&
				reply.Status == mutation.Status && reply.Count > 0 {
				return true
			}
		}
	case "family.ipv4_drop":
		return truth.IPv4 == "unreachable"
	case "family.ipv6_drop":
		return truth.IPv6 == "unreachable"
	case "link.transient_down":
		return timelineApplied(report, func(event TimedEvent) bool {
			return event.Type == FaultScheduledLink && event.Node == mutation.Node && event.Segment == mutation.Segment &&
				event.Offset == 0 && event.State == LinkStateDown
		})
	case "routing.preferred_path_failure":
		return preferredPathFailureObserved(mutation, report)
	}
	return false
}

func preferredPathFailureObserved(mutation GeneratedMutation, report *Report) bool {
	if mutation.TargetNode == "" || mutation.Family != "ipv4" {
		return false
	}
	var selected *RouteEvidence
	for i := range report.Evidence.Routes {
		route := &report.Evidence.Routes[i]
		if route.Node == mutation.TargetNode && route.Selected && route.Family == mutation.Family &&
			(route.Destination == "1.1.1.1" || route.Destination == "8.8.8.8") {
			selected = route
			break
		}
	}
	if selected == nil || !nodeInterfaceOwns(report, mutation.Node, selected.Segment, selected.Via) ||
		!linkState(report, mutation.Node, mutation.Segment, false) ||
		!linkState(report, mutation.Node, selected.Segment, true) ||
		!routerForwardsIPv4(report, mutation.Node) ||
		!familyReachabilityMatches(report, mutation.TargetNode, mutation.Family,
			[]string{selected.Segment, selected.Via}, FamilyStateUnreachable) {
		return false
	}

	for _, node := range report.Topology {
		if node.Name != mutation.TargetNode {
			continue
		}
		for _, alternate := range node.Routes {
			if alternate.Destination != "default" || alternate.Family != mutation.Family ||
				alternate.Via == selected.Via || alternate.Segment == selected.Segment || alternate.Metric <= selected.Metric ||
				!gatewayReachable(report, mutation.TargetNode, alternate) ||
				!reachabilityMatches(report, mutation.TargetNode, []string{alternate.Segment, alternate.Via}, true) {
				continue
			}
			for _, gateway := range report.Topology {
				if gateway.Role == "router" && nodeInterfaceOwns(report, gateway.Name, alternate.Segment, alternate.Via) &&
					routerForwardsIPv4(report, gateway.Name) && routerHasOtherLiveLink(report, gateway.Name, alternate.Segment) {
					return true
				}
			}
		}
	}
	return false
}

func nodeInterfaceOwns(report *Report, node, segment, address string) bool {
	for _, item := range report.Topology {
		if item.Name != node {
			continue
		}
		for _, iface := range item.Interfaces {
			if iface.Segment != segment {
				continue
			}
			for _, raw := range []string{iface.Address, iface.IPv4} {
				if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Addr().String() == address {
					return true
				}
			}
		}
	}
	return false
}

func linkState(report *Report, node, segment string, up bool) bool {
	for _, link := range report.Evidence.Links {
		if link.Node == node && link.Segment == segment && link.Up == up {
			return true
		}
	}
	return false
}

func routerForwardsIPv4(report *Report, node string) bool {
	for _, router := range report.Evidence.Routers {
		if router.Node == node {
			return router.IPv4Forwarding
		}
	}
	return false
}

func reachabilityMatches(report *Report, from string, via []string, reachable bool) bool {
	for _, item := range report.Evidence.Reachability {
		if item.From == from && item.Family == "" && item.Reachable == reachable && slices.Equal(item.Via, via) {
			return true
		}
	}
	return false
}

func familyReachabilityMatches(report *Report, node, family string, via []string, state string) bool {
	for _, item := range report.Evidence.FamilyReachability {
		if item.Node == node && item.Family == family && item.State == state && slices.Equal(item.Via, via) {
			return true
		}
	}
	return false
}

func gatewayReachable(report *Report, node string, route RouteInfo) bool {
	for _, item := range report.Evidence.Routes {
		if item.Node == node && item.Destination == "default" && item.Family == route.Family &&
			item.Via == route.Via && item.Segment == route.Segment && item.Metric == route.Metric &&
			item.GatewayReachable != nil && *item.GatewayReachable {
			return true
		}
	}
	return false
}

func routerHasOtherLiveLink(report *Report, node, clientSegment string) bool {
	for _, link := range report.Evidence.Links {
		if link.Node == node && link.Segment != clientSegment && link.Up {
			return true
		}
	}
	return false
}

func timelineApplied(report *Report, match func(TimedEvent) bool) bool {
	for _, event := range report.Timeline {
		if event.Result == EventApplied && match(event.Event) {
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
	for _, finding := range familyDiagnosisFindings(report, truth) {
		add(finding)
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

func familyDiagnosisFindings(report *Report, truth ObservedTruth) []HuntCaseFinding {
	if !familyTruthComparable(report) {
		return nil
	}
	client := observedClient(report)
	if client == "" {
		return nil
	}
	// Final evidence is closest in time to the last netdoc run. Earlier runs can
	// legitimately describe an older state in multi-test timeline scenarios.
	var diagnosis *Diagnosis
	for i := len(report.Tests) - 1; i >= 0; i-- {
		if report.Tests[i].Node == client {
			diagnosis = report.Tests[i].Diagnosis
			break
		}
	}
	if diagnosis == nil {
		return nil
	}
	var families *DiagnosisFamilies
	if check := checkByID(diagnosis, "internet_tcp"); check != nil {
		families = check.Families
	}
	diagnosed := func(family string) string {
		if families == nil {
			return ""
		}
		if family == "ipv4" {
			return families.IPv4
		}
		return families.IPv6
	}
	var findings []HuntCaseFinding
	for _, family := range []struct {
		name, truth string
	}{{"ipv4", truth.IPv4}, {"ipv6", truth.IPv6}} {
		actual := diagnosed(family.name)
		category := ""
		switch {
		case family.truth == "unreachable" && actual != "unreachable":
			category = FindingFalseNegative
		case family.truth == "reachable" && actual == "unreachable":
			category = FindingDiagnosticContradiction
		}
		if category == "" {
			continue
		}
		if actual == "" {
			actual = "unreported"
		}
		findings = append(findings, HuntCaseFinding{Category: category, Severity: SeverityHigh,
			Code: "family_reachability_mismatch", Probe: "internet_tcp", Family: family.name,
			Expected: family.truth, Actual: actual,
			Summary:  fmt.Sprintf("Final simulator truth says %s is %s, but Network Doctor reported %s.", family.name, family.truth, actual),
			Evidence: fmt.Sprintf("simulator final %s=%s; internet_tcp address_families.%s=%s", family.name, family.truth, family.name, actual)})
	}
	return findings
}

func familyTruthComparable(report *Report) bool {
	// Separate samples can disagree under packet impairment or after a timed
	// path transition without either observer being wrong. Final-state truth is
	// not a temporal oracle, so leave those cases to the timeline analysis.
	for _, fault := range report.Faults {
		if fault.Type == FaultNetem {
			return false
		}
	}
	for _, event := range report.Timeline {
		if event.Result != EventApplied {
			continue
		}
		switch event.Event.Type {
		case FaultScheduledNetem:
			if event.Event.Latency > 0 || event.Event.Jitter > 0 || event.Event.LossPercent > 0 {
				return false
			}
		case FaultScheduledLink:
			if event.Event.State == LinkStateDown {
				return false
			}
		}
	}
	return true
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
