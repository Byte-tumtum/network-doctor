// Package snapshot defines the .ndoc diagnostic snapshot: Network Doctor's
// portable record of one finished run, and the only place its on-disk shape is
// written down.
//
// A snapshot is an external artifact, not a dump of runtime state. This package
// deliberately imports nothing from internal/diagnostic, so the file format
// cannot drift with a probe struct: a reader built years from now decodes an
// .ndoc with this package alone, no probes, no TUI, no network. The conversion
// from live results runs the other way, in internal/diagnostic, which is the
// only package that can see its own evidence.
//
// Every field is explicitly tagged and every type is a struct or a slice: no
// maps, so the encoder's output order is the declaration order here and two
// runs of the same data produce the same bytes.
package snapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Schema is the identity of the format this package reads and writes. It is
// the first thing in the file and the first thing Decode checks.
//
// The version is a whole number and it moves for one reason: a change a v1
// reader would misread. Adding an optional field is not that, which is why
// Decode never rejects unknown keys, and why every optional field is omitempty
// with an absent-means-unknown reading. Renaming a field, changing what one
// means, or changing a unit is that, and takes netdoc.snapshot.v2.
const Schema = "netdoc.snapshot.v1"

// Extension is the conventional file suffix. Nothing here enforces it: the
// schema string in the file is the identity, not the name it was saved under.
const Extension = ".ndoc"

// The outcomes a check row can carry. They are written down here rather than
// borrowed from the runtime status type, because the vocabulary of the file is
// part of the file format: a reader decides what a row means by comparing
// against these, and never by trusting a Go zero value.
//
// StatusIncomplete is the one no probe can return. It marks a row that never
// produced a result at all, which happens when a run is cancelled or
// interrupted before that check reports. It exists because the alternative is
// worse: leaving the field empty would make the absence of evidence indistinct
// from a row whose outcome simply was not written down, and a snapshot is
// evidence. An incomplete row is not a skipped row either; nothing decided to
// leave it out, the run just ended first.
const (
	StatusPass       = "PASS"
	StatusWarn       = "WARN"
	StatusFail       = "FAIL"
	StatusSkip       = "SKIP"
	StatusNA         = "N/A"
	StatusIncomplete = "INCOMPLETE"
)

// Causal-evidence vocabulary is part of the v1 file contract. These mirror
// the diagnostic model without importing it, preserving the package boundary
// that lets snapshots be decoded without the live probe engine.
const (
	EvidenceSupport       = "support"
	EvidenceContradiction = "contradiction"
	EvidenceRuledOut      = "ruled_out"
	EvidenceNotEvaluated  = "not_evaluated"

	ObservationStatusPass       = "status_pass"
	ObservationStatusWarn       = "status_warn"
	ObservationStatusFail       = "status_fail"
	ObservationStatusSkip       = "status_skip"
	ObservationStatusNA         = "status_not_applicable"
	ObservationCause            = "cause"
	ObservationDNSAnswers       = "dns_answers"
	ObservationDNSNotFound      = "dns_not_found"
	ObservationCaptivePortal    = "captive_portal"
	ObservationTimeout          = "timeout"
	ObservationClockOffset      = "clock_offset"
	ObservationStatusDowngraded = "status_downgraded"
	ObservationFamilyReachable  = "family_reachable"
	ObservationFamilyFailed     = "family_failed"
	ObservationAddressSucceeded = "address_succeeded"
	ObservationAddressFailed    = "address_failed"
	// Route observations. Each is a fact about the path the row that carries
	// it takes, so it stays verifiable against that row alone.
	ObservationRouteTunneled     = "route_tunneled"
	ObservationRouteDirect       = "route_direct"
	ObservationRouteUnreachable  = "route_unreachable"
	ObservationRoutePathDiffers  = "route_path_differs"
	ObservationRouteFamilySplit  = "route_family_split"
	ObservationRouteInterfaceMTU = "route_interface_mtu"

	NotEvaluatedPrerequisite  = "prerequisite_failed"
	NotEvaluatedNotSelected   = "not_selected"
	NotEvaluatedNotApplicable = "not_applicable"
	NotEvaluatedIncomplete    = "incomplete"
)

// UnsupportedSchemaError is what Decode returns for a file whose schema is not
// this one, including a future version. It carries the schema it found so a
// caller can say what it was handed rather than "not a snapshot".
type UnsupportedSchemaError struct{ Found string }

func (e UnsupportedSchemaError) Error() string {
	if e.Found == "" {
		return "not a Network Doctor snapshot (no schema field, want " + Schema + ")"
	}
	return fmt.Sprintf("unsupported snapshot schema %q, this build reads %s", e.Found, Schema)
}

// Snapshot is one finished diagnostic run, whole enough to reopen, compare, or
// attach to a report without rerunning a probe.
type Snapshot struct {
	Schema string `json:"schema"`
	// CreatedAt is RFC 3339 in UTC. It is when the snapshot was written, which
	// is the end of the run, not the start of it.
	CreatedAt string `json:"created_at"`
	Tool      Tool   `json:"tool"`
	// Target is null for a generic run, the same absence --json publishes.
	Target    *Target   `json:"target"`
	Options   Options   `json:"options"`
	Checks    []Check   `json:"checks"`
	Diagnosis Diagnosis `json:"diagnosis"`
	// OK means no check failed, the same rule as the JSON report and the exit
	// code. Warn, Skip, and N/A do not count against it.
	OK bool `json:"ok"`
}

// Tool is the build that produced the snapshot. GOOS and GOARCH are here
// because a diagnosis reads differently per platform, and because the
// remediation advice for a finding is chosen by OS: a later reader can
// reproduce the advice this run showed without guessing which machine ran it.
type Tool struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Target keeps the spelling the user typed next to what netdoc made of it, so
// a comparison can tell "the same host, entered differently" from "a different
// host". No credentials can reach Raw: the parser rejects userinfo outright.
type Target struct {
	Raw  string `json:"raw"`
	Host string `json:"host"`
	// IP is set only when the target was an IP literal, in which case the run
	// had no name to resolve.
	IP           string `json:"ip,omitempty"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	PortExplicit bool   `json:"port_explicit"`
}

// Options are the run settings that change what the probes did, and therefore
// what two snapshots can fairly be compared on. Flags that only affect
// presentation are not here: they change nothing a comparison would read.
type Options struct {
	ProbeTimeoutMs int64 `json:"probe_timeout_ms"`
	// PublicDNS is the second-opinion resolver, empty when that row was
	// switched off.
	PublicDNS string `json:"public_dns"`
	// Check and Skip are the probe selection as given, absent when the run
	// took the whole graph.
	Check []string `json:"check,omitempty"`
	Skip  []string `json:"skip,omitempty"`
	// Source is the --iface binding in effect, absent when probes used the
	// system's own routing choice.
	Source *Source `json:"source,omitempty"`
}

// Source is the local binding probes were given, as netdoc resolved it. Either
// family may be absent, which means the selected interface had no address for
// it. Interface is the name that was named, empty when an exact local IP was
// given instead: what was asked for and what was used are different questions,
// and the second one is answered per check under Observed.
type Source struct {
	Interface string `json:"interface,omitempty"`
	IPv4      string `json:"ipv4,omitempty"`
	IPv6      string `json:"ipv6,omitempty"`
}

// Check is one probe row: its outcome, what it saw, and what was done to that
// outcome afterwards.
//
// Every check the run built appears here, including the ones that were skipped,
// did not apply, or never reported, because a comparison needs to know a row
// existed and was not reached. Status says which of those a row is, on its own:
// four states, four values, no cross-referencing.
//
// Ran is a second, narrower question, and only that: did the probe body
// execute. It separates a check that finished in under a millisecond from one
// that did not run, since DurationMs cannot, both being 0. It is not how a
// reader tells an incomplete row from a passing one; Status is.
type Check struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Deps are the probe IDs this row waited on, so a reader can rebuild the
	// dependency graph the run actually executed without a live probe list.
	Deps []string `json:"deps,omitempty"`
	// Status is one of the status constants above, never empty. A row with no
	// completed result reads StatusIncomplete, which is never a pass: no
	// consumer has to know that PASS happens to be a Go zero value, because a
	// snapshot in which it could mean "unknown" cannot be written. Encode
	// refuses one.
	Status string `json:"status"`
	// Cause is the stable machine-readable reason, empty when the outcome
	// needs none. Branch on this, never on Detail.
	Cause      string `json:"cause,omitempty"`
	Ran        bool   `json:"ran"`
	DurationMs int64  `json:"duration_ms"`
	// Detail and Fix are derived human sentences. They are kept because a
	// snapshot is also read by a person, and never parsed back.
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
	// Observed is what this probe measured. Absent when it measured nothing
	// beyond its status, which is the case for every row that never ran.
	Observed *Observed `json:"observed,omitempty"`
	// Derived is what the cross-probe reasoning pass did to this row after the
	// probes finished. Absent when it left the row alone, so its presence is
	// the signal that the status above is not purely what the probe returned.
	Derived *Derived `json:"derived,omitempty"`
}

// Observed is evidence a probe measured directly. Everything here is a
// reading; nothing here is a conclusion.
type Observed struct {
	// Addresses are every record the resolver returned, SelectedIP the one
	// this probe actually used.
	Addresses  []string `json:"addresses,omitempty"`
	SelectedIP string   `json:"selected_ip,omitempty"`
	// DNSNotFound distinguishes "the resolver answered, with nothing" from
	// "the resolver did not answer", which have different fixes.
	DNSNotFound bool `json:"dns_not_found,omitempty"`
	// Resolver is the second-opinion DNS server this row queried.
	Resolver string `json:"resolver,omitempty"`
	// SourceIP and Interface are the local end of the connection this probe
	// made, as the kernel chose it.
	SourceIP  string `json:"source_ip,omitempty"`
	Interface string `json:"interface,omitempty"`
	// SSID is the connected Wi-Fi network name, absent when wired or unknown.
	// It is retained because it is often the thing that changed between two
	// snapshots, and it is the field a future redaction pass will want first.
	SSID string `json:"ssid,omitempty"`
	// Families is independently tested reachability per address family. An
	// absent family was never dialed.
	Families *Families `json:"address_families,omitempty"`
	Portal   *Portal   `json:"portal,omitempty"`
	Attempts []Attempt `json:"attempts,omitempty"`
	// ClockOffsetMs is this machine's clock minus the one the captive-portal
	// endpoint reported, positive when the local clock runs fast. Absent when
	// there was no usable reading. It is here because it is the only evidence
	// that separates a real certificate problem from a wrong local clock, and
	// it is gone the moment the process exits.
	ClockOffsetMs *int64 `json:"clock_offset_ms,omitempty"`
	// Timeout marks a failure that was a timeout rather than a refusal or a
	// reset, for the rows that do not already say so through Cause.
	Timeout bool `json:"timeout,omitempty"`
	// InterfaceAmbiguous means the source address resolved to more than one
	// interface, so Interface above is display text and not a name.
	InterfaceAmbiguous bool `json:"interface_ambiguous,omitempty"`
	// Routes are the operating system's own route decisions for the
	// destinations this row is about, one per destination address. Absent on a
	// row with no destination to look up and on a platform netdoc cannot ask,
	// and an absent list never means "there is no route": that is what a
	// present entry with unreachable set says.
	//
	// This is diagnostic information, not a routing table. Only destinations
	// the run already cared about are here, and each entry is the decision the
	// kernel reported for one of them.
	Routes []Route `json:"routes,omitempty"`
}

// Route is one destination's selected path, as the operating system reported
// its own decision at the time of the run.
//
// Every optional field is absent when the platform did not supply it, which a
// reader must take as "not known on that machine" and never as zero. Metric is
// a pointer for exactly that reason: 0 is a real route metric on Linux and on
// Windows, so absence cannot be spelled as 0.
type Route struct {
	Destination string `json:"destination"`
	// Family is "ipv4" or "ipv6", the same vocabulary the per-family
	// reachability fields use.
	Family    string `json:"family,omitempty"`
	Interface string `json:"interface,omitempty"`
	// Gateway is the next hop, absent when the destination is on-link.
	Gateway string `json:"gateway,omitempty"`
	// Source is the local address the kernel would send from.
	Source string `json:"source,omitempty"`
	// Prefix is the route entry the kernel said it matched, in CIDR form.
	Prefix string `json:"prefix,omitempty"`
	Metric *int   `json:"metric,omitempty"`
	// Table names the routing table or routing domain the decision came from,
	// absent where the platform has one table or did not say which it used.
	Table string `json:"table,omitempty"`
	// InterfaceMTU is the selected link's own MTU. It is never a measured path
	// MTU: the path_mtu check is the only thing that measures one, and reading
	// this as an end-to-end number is the mistake the name exists to prevent.
	InterfaceMTU int `json:"interface_mtu,omitempty"`
	// Tunnel is "tunnel", "likely", or "direct", and absent when nothing
	// classified the interface. Absent is not "direct".
	Tunnel string `json:"tunnel,omitempty"`
	// TunnelKind is the operating system's own name for the device kind, set
	// only alongside a "tunnel" state.
	TunnelKind string `json:"tunnel_kind,omitempty"`
	// Unreachable is the kernel answering that no route exists.
	Unreachable bool `json:"unreachable,omitempty"`
	// Reason is why this route won, from the documented vocabulary.
	Reason string `json:"reason,omitempty"`
	// Competing are routes that covered the same destination and lost,
	// recorded only where seeing one explains the decision.
	Competing []CompetingRoute `json:"competing,omitempty"`
}

// CompetingRoute is one route that lost. Metric is not a pointer here because
// a competitor is only ever recorded on a platform that ranks routes by one.
type CompetingRoute struct {
	Interface string `json:"interface,omitempty"`
	Metric    int    `json:"metric"`
}

// Families is per-address-family reachability, using the same
// reachable/unreachable vocabulary as the JSON report.
type Families struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// Portal is captive-portal evidence. Present means egress was intercepted
// rather than dead; RedirectURL is empty when the interception advertised no
// usable sign-in URL.
type Portal struct {
	RedirectURL string `json:"redirect_url,omitempty"`
}

// Attempt is one connection attempt against a single address.
type Attempt struct {
	IP         string `json:"ip"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Cause      string `json:"cause,omitempty"`
	Aborted    bool   `json:"aborted,omitempty"`
}

// Derived records that the cross-probe pass rewrote this row's outcome, which
// is the difference between "the probe reported this" and "netdoc concluded
// this". A comparison that ignored it would read an inferred Warn and a
// measured Warn as the same state.
type Derived struct {
	// StatusDowngraded means an observed failure was relaxed because another
	// path proved the network still carries traffic.
	StatusDowngraded bool `json:"status_downgraded,omitempty"`
}

// Diagnosis is the run's single interpretation: its class, its sentence, and
// the specific conclusions it supports. It is stated once, so nothing in the
// snapshot can describe a different run than the checks above.
type Diagnosis struct {
	Verdict string `json:"verdict"`
	Summary string `json:"summary"`
	// Blamed is the row to put a cursor on, which falls back to the first
	// failed row when the diagnosis names none. Empty when nothing failed.
	Blamed string `json:"blamed,omitempty"`
	// FailedStage is the first check that failed, the one field a triage
	// script needs. Empty when none did.
	FailedStage string    `json:"failed_stage,omitempty"`
	Findings    []Finding `json:"findings,omitempty"`
}

// Finding is one conclusion the run proved, by stable ID. Remediation text is
// deliberately not stored: fix advice lives on the check rows, and the
// structured next action is regenerable from ID plus tool.os, so keeping a
// copy here would be a second catalogue to keep in step.
type Finding struct {
	ID       string   `json:"id"`
	Verdict  string   `json:"verdict"`
	Summary  string   `json:"summary"`
	Focus    string   `json:"focus,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	// CausalEvidence preserves the interpretation made during the original
	// run. It is optional so pre-evidence v1 snapshots remain valid and load
	// without inventing relationships they never recorded.
	CausalEvidence []CausalEvidence `json:"causal_evidence,omitempty"`
	Counterfactual *Counterfactual  `json:"counterfactual,omitempty"`
}

// CausalEvidence is one typed relationship to an observed check fact. The
// observation value stays on the referenced Check; this records provenance
// and the interpretation selected at capture time.
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

// Encode renders a snapshot as the bytes of an .ndoc file: indented JSON with
// a trailing newline, so it reads in a pager and diffs line by line. The same
// snapshot always produces the same bytes.
//
// It refuses a snapshot whose rows do not say what they are. An empty status
// is the zero value of a Go string and not an outcome, and a run holding a
// check that never reported is not a clean one, so neither can be published:
// the absence of evidence never leaves here looking like evidence.
func Encode(s Snapshot) ([]byte, error) {
	s.Schema = Schema
	if err := validate(s); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	// SetEscapeHTML stays on, matching the encoder --json already uses, so the
	// two outputs escape a hostile detail string the same way.
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// validate holds the rules a snapshot has to satisfy to be a snapshot, rather
// than valid JSON that happens to have these keys. Both directions apply them:
// Encode so a file that says nothing about a row never gets published, and
// Decode so a hand-edited or truncated file is refused instead of being read
// as one where a row simply had nothing to say. A reader that accepted what the
// writer refuses is a reader whose invariants are only true by luck.
func validate(s Snapshot) error {
	checks := make(map[string]Check, len(s.Checks))
	for _, c := range s.Checks {
		switch {
		case c.Status == "":
			return fmt.Errorf("snapshot check %q has no status: a row with no completed result must say %s", c.ID, StatusIncomplete)
		case c.Status == StatusIncomplete && c.Ran:
			return fmt.Errorf("snapshot check %q is %s and also ran: a row that reported has an outcome", c.ID, StatusIncomplete)
		case c.Status == StatusIncomplete && s.OK:
			return fmt.Errorf("snapshot check %q is %s, so the run cannot be reported ok", c.ID, StatusIncomplete)
		}
		checks[c.ID] = c
	}
	for _, finding := range s.Diagnosis.Findings {
		seen := make(map[CausalEvidence]bool, len(finding.CausalEvidence))
		for _, evidence := range finding.CausalEvidence {
			if evidence.Check == "" {
				return fmt.Errorf("snapshot finding %q has causal evidence with no check", finding.ID)
			}
			if seen[evidence] {
				return fmt.Errorf("snapshot finding %q repeats causal evidence for check %q", finding.ID, evidence.Check)
			}
			seen[evidence] = true
			if err := validateCausalEvidence(evidence, checks); err != nil {
				return fmt.Errorf("snapshot finding %q: %w", finding.ID, err)
			}
		}
		if finding.Counterfactual != nil {
			if finding.Counterfactual.Variable == "" || len(finding.Counterfactual.Alternatives) < 2 {
				return fmt.Errorf("snapshot finding %q has an incomplete counterfactual", finding.ID)
			}
			for _, alternative := range finding.Counterfactual.Alternatives {
				if alternative.Value == "" || alternative.Outcome == "" || len(alternative.Evidence) == 0 {
					return fmt.Errorf("snapshot finding %q has an incomplete counterfactual alternative", finding.ID)
				}
				for _, evidence := range alternative.Evidence {
					if !seen[evidence] {
						return fmt.Errorf("snapshot finding %q counterfactual references evidence not carried by the finding", finding.ID)
					}
				}
			}
		}
	}
	return nil
}

func validateCausalEvidence(e CausalEvidence, checks map[string]Check) error {
	check, exists := checks[e.Check]
	switch e.Kind {
	case EvidenceSupport:
		if e.Candidate != "" || e.Reason != "" {
			return fmt.Errorf("support evidence for check %q has an alternative or not-evaluated reason", e.Check)
		}
	case EvidenceContradiction, EvidenceRuledOut:
		if e.Candidate == "" || e.Reason != "" {
			return fmt.Errorf("%s evidence for check %q must name one candidate and no reason", e.Kind, e.Check)
		}
		if e.Observation == ObservationStatusSkip || e.Observation == ObservationStatusNA {
			return fmt.Errorf("%s evidence for check %q cannot use an unevaluated observation", e.Kind, e.Check)
		}
	case EvidenceNotEvaluated:
		if e.Candidate != "" || e.Reason == "" {
			return fmt.Errorf("not-evaluated evidence for check %q must name one reason and no candidate", e.Check)
		}
		switch e.Reason {
		case NotEvaluatedNotSelected:
			if exists || e.Observation != "" {
				return fmt.Errorf("check %q is marked not selected but is present or has an observation", e.Check)
			}
			return nil
		case NotEvaluatedPrerequisite:
			if !exists || check.Status != StatusSkip || e.Observation != ObservationStatusSkip {
				return fmt.Errorf("check %q is marked prerequisite-blocked without a SKIP observation", e.Check)
			}
			return nil
		case NotEvaluatedNotApplicable:
			if !exists || check.Status != StatusNA || e.Observation != ObservationStatusNA {
				return fmt.Errorf("check %q is marked not applicable without an N/A observation", e.Check)
			}
			return nil
		case NotEvaluatedIncomplete:
			if !exists || check.Status != StatusIncomplete || e.Observation != "" {
				return fmt.Errorf("check %q is marked incomplete without an incomplete row", e.Check)
			}
			return nil
		default:
			return fmt.Errorf("check %q has unknown not-evaluated reason %q", e.Check, e.Reason)
		}
	default:
		return fmt.Errorf("check %q has unknown causal evidence kind %q", e.Check, e.Kind)
	}
	if !exists {
		return fmt.Errorf("causal evidence references check %q, which is not in the snapshot", e.Check)
	}
	if !observationMatches(e, check) {
		return fmt.Errorf("causal evidence references %s on check %q, but that observation is absent", e.Observation, e.Check)
	}
	return nil
}

func observationMatches(e CausalEvidence, check Check) bool {
	switch e.Observation {
	case ObservationStatusPass:
		return check.Status == StatusPass
	case ObservationStatusWarn:
		return check.Status == StatusWarn && (check.Derived == nil || !check.Derived.StatusDowngraded)
	case ObservationStatusFail:
		return check.Status == StatusFail
	case ObservationStatusSkip:
		return check.Status == StatusSkip
	case ObservationStatusNA:
		return check.Status == StatusNA
	case ObservationCause:
		return check.Cause != ""
	case ObservationDNSAnswers:
		return check.Observed != nil && len(check.Observed.Addresses) > 0 &&
			(e.Value == "" || slices.Contains(check.Observed.Addresses, e.Value))
	case ObservationDNSNotFound:
		return check.Observed != nil && check.Observed.DNSNotFound
	case ObservationCaptivePortal:
		return check.Observed != nil && check.Observed.Portal != nil
	case ObservationTimeout:
		return (check.Observed != nil && check.Observed.Timeout) || check.Cause == "timeout"
	case ObservationClockOffset:
		return check.Observed != nil && check.Observed.ClockOffsetMs != nil
	case ObservationStatusDowngraded:
		return check.Derived != nil && check.Derived.StatusDowngraded
	case ObservationFamilyReachable:
		return familyObservation(check, e.Value) == "reachable"
	case ObservationFamilyFailed:
		return familyObservation(check, e.Value) == "unreachable"
	case ObservationAddressSucceeded:
		return check.Observed != nil && slices.ContainsFunc(check.Observed.Attempts, func(a Attempt) bool {
			return a.IP == e.Value && a.Error == ""
		})
	case ObservationAddressFailed:
		return check.Observed != nil && slices.ContainsFunc(check.Observed.Attempts, func(a Attempt) bool {
			return a.IP == e.Value && a.Error != "" && !a.Aborted && a.Cause != "" && a.Cause != "canceled"
		})
	case ObservationRouteTunneled:
		return routeMatches(check, func(r Route) bool {
			return r.Interface == e.Value && (r.Tunnel == TunnelStateTunnel || r.Tunnel == TunnelStateLikely)
		})
	case ObservationRouteDirect:
		return routeMatches(check, func(r Route) bool {
			return r.Interface == e.Value && r.Tunnel == TunnelStateDirect
		})
	case ObservationRouteUnreachable:
		return routeMatches(check, func(r Route) bool { return r.Destination == e.Value && r.Unreachable })
	case ObservationRoutePathDiffers:
		// The value names the other path. The claim is checkable from this row
		// alone: its own selected interface is not that one.
		return e.Value != "" && routeMatches(check, func(r Route) bool {
			return r.Interface != "" && r.Interface != e.Value
		})
	case ObservationRouteFamilySplit:
		return routeFamilySplit(check)
	case ObservationRouteInterfaceMTU:
		return routeMatches(check, func(r Route) bool {
			return r.Interface == e.Value && r.InterfaceMTU > 0
		})
	}
	return false
}

// The tunnel-state vocabulary, part of the v1 file contract. An absent state
// means nothing classified the interface, which is not the same as direct.
const (
	TunnelStateDirect = "direct"
	TunnelStateLikely = "likely"
	TunnelStateTunnel = "tunnel"
)

func routeMatches(check Check, match func(Route) bool) bool {
	return check.Observed != nil && slices.ContainsFunc(check.Observed.Routes, match)
}

// routeFamilySplit reports that this row's IPv4 and IPv6 destinations leave by
// different interfaces, which needs a named interface in both families.
func routeFamilySplit(check Check) bool {
	if check.Observed == nil {
		return false
	}
	v4, v6 := "", ""
	for _, r := range check.Observed.Routes {
		switch {
		case r.Interface == "":
		case r.Family == "ipv4" && v4 == "":
			v4 = r.Interface
		case r.Family == "ipv6" && v6 == "":
			v6 = r.Interface
		}
	}
	return v4 != "" && v6 != "" && v4 != v6
}

func familyObservation(check Check, family string) string {
	if check.Observed == nil || check.Observed.Families == nil {
		return ""
	}
	if family == "ipv4" {
		return check.Observed.Families.IPv4
	}
	if family == "ipv6" {
		return check.Observed.Families.IPv6
	}
	return ""
}

// Decode reads an .ndoc file. It refuses anything that is not this schema
// before looking at a single other field, so a v2 file written by a later
// netdoc is reported as unreadable rather than half understood.
//
// Unknown fields inside a v1 file are ignored on purpose: that is what makes
// an added optional field a compatible change. A row that does not say what it
// is, on the other hand, is not an unknown field: it is the one thing Encode
// refuses to write, and Decode refuses to read it back.
func Decode(data []byte) (Snapshot, error) {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Snapshot{}, fmt.Errorf("not a Network Doctor snapshot: %w", err)
	}
	if head.Schema != Schema {
		return Snapshot{}, UnsupportedSchemaError{Found: head.Schema}
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, err
	}
	if err := validate(s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// WriteFile saves a snapshot at path, replacing whatever was there.
//
// The bytes land in a temporary file in the same directory and are renamed
// over the destination, so an interrupted or failing write leaves the previous
// file intact rather than a truncated artifact that still parses as JSON right
// up to the point it stops. Same directory because a rename across filesystems
// is not atomic and, on Windows, not permitted at all.
//
// The file inherits os.CreateTemp's 0600 on the platforms where mode means
// anything: a snapshot carries the addresses, interface names, and network name
// of the machine that ran it, which is nobody else's business until its owner
// chooses to share it.
func WriteFile(path string, s Snapshot) error {
	data, err := Encode(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".netdoc-snapshot-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Best effort on every failure path: the rename below is what makes the
	// artifact real, so a leftover temp file is the only thing to clean up and
	// its removal failing is not worth reporting over the write error.
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
