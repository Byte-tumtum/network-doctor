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
	// Families is direct egress tested per address family, each independently
	// measured. An absent family was never dialed.
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
}

// Families is per address family direct-egress state, using the same
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
	for _, c := range s.Checks {
		switch {
		case c.Status == "":
			return nil, fmt.Errorf("snapshot check %q has no status: a row with no completed result must say %s", c.ID, StatusIncomplete)
		case c.Status == StatusIncomplete && c.Ran:
			return nil, fmt.Errorf("snapshot check %q is %s and also ran: a row that reported has an outcome", c.ID, StatusIncomplete)
		case c.Status == StatusIncomplete && s.OK:
			return nil, fmt.Errorf("snapshot check %q is %s, so the run cannot be reported ok", c.ID, StatusIncomplete)
		}
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

// Decode reads an .ndoc file. It refuses anything that is not this schema
// before looking at a single other field, so a v2 file written by a later
// netdoc is reported as unreadable rather than half understood.
//
// Unknown fields inside a v1 file are ignored on purpose: that is what makes
// an added optional field a compatible change.
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
