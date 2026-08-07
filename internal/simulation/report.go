package simulation

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// Overall results.
const (
	ResultPass    = "PASS"    // every expectation held
	ResultPartial = "PARTIAL" // some expectations held
	ResultFail    = "FAIL"    // none did
	ResultError   = "ERROR"   // the simulation itself did not run
)

// Report is one simulation, start to finish. It is the machine-readable
// artifact: `netdoc-sim run --json` prints exactly this.
type Report struct {
	Scenario    string        `json:"scenario"`
	Description string        `json:"description,omitempty"`
	ID          string        `json:"id"`
	Backend     string        `json:"backend"`
	StartedAt   time.Time     `json:"started_at"`
	Duration    time.Duration `json:"duration_ms"`
	Result      string        `json:"result"`
	// Error is set when setup, not diagnosis, is what failed.
	Error string `json:"error,omitempty"`

	Topology []NodeInfo  `json:"topology"`
	Faults   []FaultInfo `json:"faults"`
	// Timeline is what the fault scheduler did, relative to T0 — the instant
	// just before the first netdoc process started. TimelineID identifies the
	// requested timeline and ignores how long the OS took to apply it.
	Timeline    []FaultEventEvidence `json:"fault_timeline"`
	TimelineID  string               `json:"fault_timeline_id,omitempty"`
	Tests       []TestOutcome        `json:"tests"`
	Evidence    Evidence             `json:"evidence"`
	Cleanup     CleanupInfo          `json:"cleanup"`
	Suggestions []Suggestion         `json:"suggestions"`
}

// NodeInfo is a namespace the simulation created.
type NodeInfo struct {
	Name       string          `json:"name"`
	Role       string          `json:"role"`
	Address    string          `json:"address"`
	Aliases    []string        `json:"aliases,omitempty"`
	Interface  string          `json:"interface"`
	Gateway    string          `json:"gateway,omitempty"`
	Resolver   string          `json:"resolver,omitempty"`
	Services   []string        `json:"services,omitempty"`
	PID        int             `json:"pid"`
	Interfaces []InterfaceInfo `json:"interfaces,omitempty"`
	Routes     []RouteInfo     `json:"routes,omitempty"`
}

// InterfaceInfo and RouteInfo expose logical topology names rather than the
// generated Linux names used to implement them.
type InterfaceInfo struct {
	Segment string `json:"segment"`
	Address string `json:"address"`
	IPv4    string `json:"ipv4,omitempty"`
	IPv6    string `json:"ipv6,omitempty"`
}

type RouteInfo struct {
	Destination string `json:"destination"`
	Via         string `json:"via"`
	Segment     string `json:"segment"`
	Metric      int    `json:"metric"`
	Family      string `json:"family,omitempty"`
}

// FaultInfo is one injected impairment and the exact command that injected it.
type FaultInfo struct {
	Type        string        `json:"type"`
	Node        string        `json:"node"`
	Family      string        `json:"family,omitempty"`
	Summary     string        `json:"summary"`
	Command     []string      `json:"command"`
	Latency     time.Duration `json:"latency_ms,omitempty"`
	Jitter      time.Duration `json:"jitter_ms,omitempty"`
	LossPercent float64       `json:"loss_percent,omitempty"`
	Seed        uint32        `json:"seed,omitempty"`
}

// CleanupInfo records that the resources went away, and never hides a failure
// to make them go away.
type CleanupInfo struct {
	Done bool `json:"done"`
	Kept bool `json:"kept"`
	// Workspace is the scratch directory the run used, set while it still
	// exists so a kept simulation can be found and swept later.
	Workspace string   `json:"workspace,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// finish computes the overall result and collects suggestions. Call once, after
// every test has run.
func (r *Report) finish() {
	for i := range r.Tests {
		r.Suggestions = append(r.Suggestions, r.Tests[i].suggest()...)
	}
	r.Suggestions = append(r.Suggestions, r.routeSuggestions()...)
	r.Suggestions = append(r.Suggestions, r.faultSuggestions()...)
	// Empty lists, not null: a consumer iterating report.faults on a healthy
	// scenario should get nothing to do, not a type error.
	if r.Suggestions == nil {
		r.Suggestions = []Suggestion{}
	}
	if r.Timeline == nil {
		r.Timeline = []FaultEventEvidence{}
	}
	if r.Faults == nil {
		r.Faults = []FaultInfo{}
	}
	if r.Topology == nil {
		r.Topology = []NodeInfo{}
	}
	if r.Tests == nil {
		r.Tests = []TestOutcome{}
	}
	if r.Evidence.DNS == nil {
		r.Evidence.DNS = []DNSEvidence{}
	}
	if r.Evidence.DNSQueries == nil {
		r.Evidence.DNSQueries = []DNSQueryEvidence{}
	}
	if r.Evidence.SOCKSRequests == nil {
		r.Evidence.SOCKSRequests = []SOCKSEvidence{}
	}
	if r.Evidence.TLS == nil {
		r.Evidence.TLS = []TLSEvidence{}
	}
	if r.Evidence.TCPResets == nil {
		r.Evidence.TCPResets = []TCPResetEvidence{}
	}
	if r.Evidence.PacketConditions == nil {
		r.Evidence.PacketConditions = []PacketConditionEvidence{}
	}
	if r.Evidence.Links == nil {
		r.Evidence.Links = []LinkEvidence{}
	}
	if r.Evidence.Routes == nil {
		r.Evidence.Routes = []RouteEvidence{}
	}
	if r.Evidence.Routers == nil {
		r.Evidence.Routers = []RouterEvidence{}
	}
	if r.Evidence.Reachability == nil {
		r.Evidence.Reachability = []ReachabilityEvidence{}
	}
	if r.Error != "" {
		r.Result = ResultError
		return
	}
	pass, total := 0, len(r.Tests)
	for i := range r.Tests {
		if r.Tests[i].ok() {
			pass++
		}
	}
	switch {
	case total == 0:
		r.Result = ResultError
	case pass == total:
		r.Result = ResultPass
	case pass == 0:
		r.Result = ResultFail
	default:
		r.Result = ResultPartial
	}
}

func (r *Report) faultSuggestions() []Suggestion {
	for _, fault := range r.Faults {
		if fault.Type == FaultNetem && fault.Jitter > 0 {
			for _, condition := range r.Evidence.PacketConditions {
				if condition.Node == fault.Node && condition.RTTSamples > 0 {
					return []Suggestion{{Code: "jitter_sampling_gap",
						Message: "The simulator injected jitter, but each netdoc probe reports one connection sample and cannot evaluate variance across time.",
						Evidence: fmt.Sprintf("injected jitter %s; observed successful connection range %s..%s across %d sample(s)",
							fault.Jitter, condition.ObservedMinRTT, condition.ObservedMaxRTT, condition.RTTSamples)}}
				}
			}
		}
	}
	return nil
}

func (r *Report) routeSuggestions() []Suggestion {
	for _, route := range r.Evidence.Routes {
		if route.Selected && route.GatewayReachable != nil && !*route.GatewayReachable {
			return []Suggestion{{Code: "gateway_unreachable", Message: "A default path was selected but its next-hop neighbor was unreachable; expose this distinction from an absent route in the diagnostic result.",
				Evidence: fmt.Sprintf("%s selected %s via %s on %s; neighbor state failed", route.Node, route.Destination, route.Via, route.Segment)}}
		}
	}
	if len(r.Tests) < 2 || r.Tests[0].Diagnosis == nil || r.Tests[1].Diagnosis == nil {
		return nil
	}
	firstInternet := diagnosisStatus(r.Tests[0].Diagnosis, "internet_tcp")
	secondTarget := diagnosisStatus(r.Tests[1].Diagnosis, "target_tcp")
	if (firstInternet == "FAIL" || firstInternet == "WARN") && secondTarget == "PASS" {
		selectedSegment := ""
		for _, route := range r.Evidence.Routes {
			if route.Node == r.Tests[0].Node && route.Selected && (route.Destination == "1.1.1.1" || route.Destination == "8.8.8.8") {
				selectedSegment = route.Segment
				break
			}
		}
		code := "wrong_default_route_evidence"
		message := "The selected default path failed while a controlled specific path reached the target; consider exposing selected-route evidence in the diagnostic result."
		if r.Tests[1].SourceSegment != "" && r.Tests[1].SourceSegment != selectedSegment {
			code = "alternate_route_available"
			message = "The preferred path failed while a controlled test through another live interface reached the target; consider reporting the alternate working path and route metrics."
		}
		return []Suggestion{{Code: code, Message: message,
			Evidence: fmt.Sprintf("selected segment %s failed; %s reached %s", selectedSegment, r.Tests[1].SourceSegment, r.Tests[1].Target)}}
	}
	return nil
}

func offsetLabel(d time.Duration) string {
	return "+" + strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

func diagnosisStatus(d *Diagnosis, id string) string {
	for _, check := range d.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}

// WriteJSON prints the machine-readable report.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText prints the human-readable report. Every string that came from a
// subprocess goes through textsafe first: netdoc's detail lines carry remote
// text, and this one lands on a terminal.
func (r *Report) WriteText(w io.Writer) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	p("Scenario: %s", textsafe.Clean(r.Scenario))
	if r.Description != "" {
		p("          %s", textsafe.Clean(r.Description))
	}
	p("Result:   %s   (%s backend, %s, id %s)", r.Result, r.Backend, r.Duration.Round(time.Millisecond), r.ID)
	if r.Error != "" {
		p("Error:    %s", textsafe.Clean(r.Error))
	}
	p("")

	p("Topology")
	for _, n := range r.Topology {
		extra := []string{n.Role}
		if n.Gateway != "" {
			extra = append(extra, "gw "+n.Gateway)
		}
		if n.Resolver != "" {
			extra = append(extra, "dns "+n.Resolver)
		}
		if len(n.Aliases) > 0 {
			extra = append(extra, "also "+strings.Join(n.Aliases, ","))
		}
		if len(n.Services) > 0 {
			extra = append(extra, "serves "+strings.Join(n.Services, ","))
		}
		p("  %-10s %-14s %s", n.Name, n.Address, strings.Join(extra, ", "))
		for _, iface := range n.Interfaces {
			addresses := iface.Address
			if iface.IPv4 != "" || iface.IPv6 != "" {
				addresses = strings.TrimSpace(strings.Join([]string{iface.IPv4, iface.IPv6}, " "))
			}
			p("      link %-16s %s", iface.Segment, addresses)
		}
		for _, route := range n.Routes {
			p("      route %-15s via %-15s on %s metric %d", route.Destination, route.Via, route.Segment, route.Metric)
		}
	}
	p("")

	p("Faults injected")
	if len(r.Faults) == 0 {
		p("  (none — healthy network)")
	}
	for _, f := range r.Faults {
		p("  %-18s %-4s %s", f.Type, f.Family, f.Summary)
	}
	p("")

	if len(r.Timeline) > 0 {
		p("Fault timeline   (offsets from T0, just before the first netdoc run)")
		for _, e := range r.Timeline {
			switch e.Result {
			case EventApplied:
				p("  %-9s %-52s applied at +%s", offsetLabel(e.ScheduledOffset), textsafe.Clean(e.State), e.AppliedOffset.Round(time.Millisecond))
			case EventSkipped:
				p("  %-9s %-52s skipped — the run ended first", offsetLabel(e.ScheduledOffset), textsafe.Clean(e.State))
			default:
				p("  %-9s %-52s FAILED: %s", offsetLabel(e.ScheduledOffset), textsafe.Clean(e.State), textsafe.Clean(e.Error))
			}
		}
		for _, t := range r.Tests {
			p("  netdoc %q ran +%s..+%s", textsafe.Clean(t.Name),
				t.StartOffset.Round(time.Millisecond), t.EndOffset.Round(time.Millisecond))
		}
		p("")
	}

	for i := range r.Tests {
		r.Tests[i].writeText(w)
	}

	if len(r.Evidence.DNS) > 0 || len(r.Evidence.DNSQueries) > 0 || len(r.Evidence.SOCKSRequests) > 0 || len(r.Evidence.TLS) > 0 || len(r.Evidence.TCPResets) > 0 || len(r.Evidence.PacketConditions) > 0 ||
		len(r.Evidence.Links) > 0 || len(r.Evidence.Routes) > 0 || len(r.Evidence.Routers) > 0 || len(r.Evidence.Reachability) > 0 {
		p("Structured evidence")
		for _, e := range r.Evidence.Links {
			p("  LINK  %-10s %-16s %-18s up=%t", e.Node, e.Segment, e.Address, e.Up)
		}
		for _, e := range r.Evidence.Routers {
			p("  ROUTER %-10s IPv4 forwarding=%t IPv6 forwarding=%t", e.Node, e.IPv4Forwarding, e.IPv6Forwarding)
		}
		for _, e := range r.Evidence.PacketConditions {
			p("  NETEM %-10s %-16s delay=%s jitter=%s loss=%.2f%% seed=%d active=%t dropped=%d RTT=%s..%s (%d samples)", e.Node, e.Segment,
				e.Latency, e.Jitter, e.LossPercent, e.Seed, e.Active, e.DroppedPackets, e.ObservedMinRTT, e.ObservedMaxRTT, e.RTTSamples)
		}
		for _, e := range r.Evidence.Routes {
			selected := "configured"
			if e.Selected {
				selected = "selected"
			}
			gateway := "unknown"
			if e.GatewayReachable != nil {
				gateway = fmt.Sprint(*e.GatewayReachable)
			}
			p("  ROUTE %-10s %-15s via %-15s %-16s metric=%d %s %s gateway=%s", e.Node, e.Destination,
				e.Via, e.Segment, e.Metric, e.Family, selected, gateway)
		}
		for _, e := range r.Evidence.Reachability {
			p("  PATH  %-10s -> %-24s via %-32s %-4s reachable=%t", e.From, e.To, strings.Join(e.Via, " -> "), e.Family, e.Reachable)
		}
		for _, e := range r.Evidence.DNS {
			p("  DNS   %-10s from %-13s %-6s %-28s %-8s %s ×%d", e.Node, e.Source, e.QueryType, e.Name, e.Result, e.Service, e.Count)
		}
		for _, e := range r.Evidence.SOCKSRequests {
			destination := e.Destination
			if e.Port != 0 {
				destination = net.JoinHostPort(destination, fmt.Sprint(e.Port))
			}
			p("  SOCKS %-10s %-8s %-7s %-28s %-22s ×%d", e.Node, e.Event, e.AddressType,
				textsafe.Clean(destination), e.Result, e.Count)
		}
		for _, e := range r.Evidence.TLS {
			presented := "not-presented"
			if e.CertificatePresented {
				presented = "presented"
			}
			p("  TLS   %-10s %-20s SNI %-24s %-13s %-27s %s ×%d", e.Node, e.CertificateMode,
				textsafe.Clean(e.RequestedServer), presented, strings.Join(e.CertificateDNS, ","), e.Result, e.Count)
		}
		for _, e := range r.Evidence.DNSQueries {
			p("  DNSQ  %-10s %-6s #%03d %-28s scheduled=%-8s actual=%s", e.Node, e.QueryType,
				e.Sequence, e.Name, e.ScheduledOutcome, e.ActualOutcome)
		}
		for _, e := range r.Evidence.TCPResets {
			p("  RESET %-10s %-10s %-24s ×%d", e.Node, e.Event, e.Result, e.Count)
		}
		p("")
	}

	if len(r.Suggestions) > 0 {
		p("Suggested improvements")
		for _, s := range r.Suggestions {
			p("  [%s] %s", s.Code, textsafe.Clean(s.Message))
			if s.Cause != "" {
				p("      cause: %s", textsafe.Clean(s.Cause))
			}
			if s.Evidence != "" {
				p("      evidence: %s", textsafe.Clean(s.Evidence))
			}
		}
		p("")
	}

	switch {
	case r.Cleanup.Kept:
		p("Cleanup:  kept — %s", textsafe.Clean(r.Cleanup.Detail))
	case len(r.Cleanup.Errors) > 0:
		p("Cleanup:  INCOMPLETE")
		for _, e := range r.Cleanup.Errors {
			p("  %s", textsafe.Clean(e))
		}
	case r.Cleanup.Done:
		p("Cleanup:  ok — every namespace and process released")
	default:
		p("Cleanup:  not reached")
	}
}

func (o *TestOutcome) writeText(w io.Writer) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	target := o.Target
	if target == "" {
		target = "(generic checks)"
	}
	p("Test: %s — netdoc %s in %s (%s)", textsafe.Clean(o.Name), textsafe.Clean(target), o.Node, o.Duration.Round(time.Millisecond))
	if o.Error != "" {
		p("  ERROR: %s", textsafe.Clean(o.Error))
		if o.Stderr != "" {
			p("  stderr: %s", textsafe.Clean(o.Stderr))
		}
		p("")
		return
	}
	p("  Summary: %s", textsafe.Clean(o.Diagnosis.Summary))
	verdict := o.ActualVerdict
	if !o.verdictMatches() {
		verdict += fmt.Sprintf("   ✗ expected %s", o.ExpectedVerdict)
	} else if o.ExpectedVerdict != "" {
		verdict += "   ✓"
	}
	p("  Verdict: %s", verdict)
	p("  Expected findings: %d   matched: %d   false negatives: %d   false positives: %d",
		len(o.Checks)-o.countUnexpected(), o.Matched, o.FalseNegatives, o.FalsePositives)
	for _, c := range o.Checks {
		mark, note := "✓", ""
		switch c.Outcome {
		case OutcomeMatched:
		case OutcomeMissing:
			mark, note = "✗", "no such row in the report"
		case OutcomeWrongStatus:
			mark, note = "✗", "expected "+c.Expected
		case OutcomeWrongCause:
			mark, note = "✗", "expected cause "+c.ExpectedCause
		case OutcomeUnexpected:
			mark, note = "!", "not expected by the scenario"
		}
		line := fmt.Sprintf("  %s %-14s %-5s %s", mark, c.ID, c.Actual, textsafe.Clean(c.Detail))
		if note != "" {
			line += "   (" + note + ")"
		}
		if c.Cause != "" {
			line += "   [cause: " + textsafe.Clean(c.Cause) + "]"
		}
		p("%s", line)
	}
	if len(o.TimedOut) > 0 {
		p("  Timed out: %s", strings.Join(o.TimedOut, ", "))
	}
	if len(o.RepeatVerdicts) > 1 {
		verdicts := uniqueSorted(o.RepeatVerdicts)
		stability := "stable across " + fmt.Sprint(len(o.RepeatVerdicts)) + " runs"
		if len(verdicts) > 1 {
			stability = "UNSTABLE across " + fmt.Sprint(len(o.RepeatVerdicts)) + " runs: " + strings.Join(verdicts, ", ")
		}
		p("  Repeats: %s", stability)
	}
	p("")
}

func (o *TestOutcome) countUnexpected() int {
	n := 0
	for _, c := range o.Checks {
		if c.Outcome == OutcomeUnexpected {
			n++
		}
	}
	return n
}
