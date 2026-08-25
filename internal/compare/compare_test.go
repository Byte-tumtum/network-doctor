package compare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/snapshot"
)

// The fixture is the one internal/diagnostic builds from a real probe run and
// internal/snapshot decodes, so these tests compare the artifact netdoc
// actually writes rather than a parallel struct invented for comparison.
var goldenSnapshot = filepath.Join("..", "snapshot", "testdata", "example.ndoc")

func fixture(t *testing.T) snapshot.Snapshot {
	t.Helper()
	// #nosec G304 -- goldenSnapshot is a fixed repository-owned fixture path.
	data, err := os.ReadFile(goldenSnapshot)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	s, err := snapshot.Decode(data)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return s
}

// check returns a pointer to the named row so a test can move one field and
// leave the rest of a real run alone.
func check(t *testing.T, s *snapshot.Snapshot, id string) *snapshot.Check {
	t.Helper()
	for i := range s.Checks {
		if s.Checks[i].ID == id {
			return &s.Checks[i]
		}
	}
	t.Fatalf("fixture has no check %q", id)
	return nil
}

// paths is the change list reduced to its stable identities, which is what a
// test should assert on: the prose is allowed to be reworded, the path is not.
func paths(c Comparison) []string {
	out := make([]string, len(c.Changes))
	for i, change := range c.Changes {
		out[i] = change.Path
	}
	return out
}

func changeAt(t *testing.T, c Comparison, path string) Change {
	t.Helper()
	for _, change := range c.Changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("no change at %q; got %v", path, paths(c))
	return Change{}
}

func mustNotChange(t *testing.T, c Comparison, why string) {
	t.Helper()
	if !c.Same() {
		t.Errorf("%s produced %d differences: %v", why, len(c.Changes), paths(c))
	}
}

// The baseline every other test leans on: a snapshot compared with itself has
// nothing to report, and says so through Same rather than through empty prose.
func TestIdenticalSnapshotsHaveNoDifferences(t *testing.T) {
	s := fixture(t)
	c := Snapshots(s, s)
	mustNotChange(t, c, "a snapshot compared with itself")
	if !c.SameTarget {
		t.Error("SameTarget = false for one snapshot against itself")
	}
	if len(c.Checks) != len(s.Checks) {
		t.Errorf("%d check rows, want one per check (%d)", len(c.Checks), len(s.Checks))
	}
	for _, row := range c.Checks {
		if row.Kind != KindUnchanged || row.Differs {
			t.Errorf("%s = %+v, want an unchanged row", row.ID, row)
		}
	}
}

// The headline case: one probe's outcome moves and nothing else does.
func TestOneProbeStatusChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "dns").Status = snapshot.StatusFail

	c := Snapshots(before, after)
	if got := paths(c); !slices.Equal(got, []string{"checks.dns.status"}) {
		t.Fatalf("changes = %v, want only the dns status", got)
	}
	change := c.Changes[0]
	if change.Before != snapshot.StatusPass || change.After != snapshot.StatusFail {
		t.Errorf("change = %+v, want PASS to FAIL", change)
	}
	if change.Direction != DirectionWorse {
		t.Errorf("direction = %q, want %q", change.Direction, DirectionWorse)
	}
	if change.Check != "dns" || change.Section != SectionCheck {
		t.Errorf("change = %+v, want it attributed to the dns check", change)
	}
	if !strings.Contains(change.Summary, "dns changed from PASS to FAIL") {
		t.Errorf("summary = %q", change.Summary)
	}
}

// Direction is a statement about two readings and only exists where the
// vocabulary has an order. SKIP, N/A, and INCOMPLETE have none, and a guess
// there would be a claim the comparison cannot support.
func TestStatusDirectionOnlyWhereTheOutcomesAreRanked(t *testing.T) {
	tests := []struct {
		before, after, want string
	}{
		{snapshot.StatusPass, snapshot.StatusFail, DirectionWorse},
		{snapshot.StatusPass, snapshot.StatusWarn, DirectionWorse},
		{snapshot.StatusWarn, snapshot.StatusFail, DirectionWorse},
		{snapshot.StatusFail, snapshot.StatusPass, DirectionBetter},
		{snapshot.StatusWarn, snapshot.StatusPass, DirectionBetter},
		{snapshot.StatusPass, snapshot.StatusSkip, ""},
		{snapshot.StatusSkip, snapshot.StatusPass, ""},
		{snapshot.StatusFail, snapshot.StatusNA, ""},
		{snapshot.StatusSkip, snapshot.StatusIncomplete, ""},
		{snapshot.StatusIncomplete, snapshot.StatusFail, ""},
	}
	for _, tt := range tests {
		t.Run(tt.before+" to "+tt.after, func(t *testing.T) {
			before, after := fixture(t), fixture(t)
			check(t, &before, "dns").Status = tt.before
			check(t, &after, "dns").Status = tt.after
			c := Snapshots(before, after)
			if got := changeAt(t, c, "checks.dns.status").Direction; got != tt.want {
				t.Errorf("direction = %q, want %q", got, tt.want)
			}
		})
	}
}

// SKIP, N/A, and INCOMPLETE are three different things that happened to a row,
// and collapsing any pair of them would hide the difference between a check
// that was left out, one that did not apply, and one the run never reached.
func TestAbsentOutcomesStayDistinct(t *testing.T) {
	states := []string{snapshot.StatusSkip, snapshot.StatusNA, snapshot.StatusIncomplete}
	for _, a := range states {
		for _, b := range states {
			if a == b {
				continue
			}
			before, after := fixture(t), fixture(t)
			check(t, &before, "tls").Status = a
			check(t, &after, "tls").Status = b
			// An INCOMPLETE row cannot sit in a run reported ok, which is the
			// artifact's own rule; the fixture is already not ok.
			c := Snapshots(before, after)
			if c.Same() {
				t.Errorf("%s and %s compared as the same state", a, b)
			}
		}
	}
}

func TestDiagnosisChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Diagnosis.Verdict = "dns"
	after.Diagnosis.Blamed = "dns"
	after.Diagnosis.FailedStage = "dns"
	after.OK = true

	c := Snapshots(before, after)
	want := []string{"ok", "diagnosis.verdict", "diagnosis.blamed", "diagnosis.failed_stage"}
	if got := paths(c); !slices.Equal(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	if got := changeAt(t, c, "ok"); got.Direction != DirectionBetter || got.Before != "not ok" || got.After != "ok" {
		t.Errorf("overall change = %+v", got)
	}
}

// Findings are a set keyed by ID, except that the first one is the diagnosis's
// primary conclusion, so that one position is compared as a position.
func TestFindingsCompareAsASetWithAPrimary(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Diagnosis.Findings = append([]snapshot.Finding{
		{ID: "dns_failure", Verdict: "dns", Focus: "dns", Evidence: []string{"dns"}},
	}, after.Diagnosis.Findings...)

	c := Snapshots(before, after)
	if got := changeAt(t, c, "diagnosis.findings.primary"); got.Before != "local_egress_failure" || got.After != "dns_failure" {
		t.Errorf("primary finding change = %+v", got)
	}
	added := changeAt(t, c, "diagnosis.findings.dns_failure")
	if added.Kind != KindAdded || added.After != "dns_failure" {
		t.Errorf("finding membership change = %+v, want it added", added)
	}
	// The finding both runs share is untouched, so nothing is reported for it.
	for _, change := range c.Changes {
		if strings.HasPrefix(change.Path, "diagnosis.findings.local_egress_failure") {
			t.Errorf("unchanged finding reported: %+v", change)
		}
	}
}

// Evidence is a list the diagnosis reasoned over in order, so its order is
// meaningful and a reordering is a difference.
func TestFindingEvidenceKeepsItsOrder(t *testing.T) {
	before, after := fixture(t), fixture(t)
	evidence := after.Diagnosis.Findings[0].Evidence
	reversed := make([]string, len(evidence))
	for i, id := range evidence {
		reversed[len(evidence)-1-i] = id
	}
	after.Diagnosis.Findings[0].Evidence = reversed

	c := Snapshots(before, after)
	if got := changeAt(t, c, "diagnosis.findings.local_egress_failure.evidence"); got.Kind != KindChanged {
		t.Errorf("evidence change = %+v", got)
	}
}

func TestSelectedInterfaceChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	for _, id := range []string{"iface", "internet_tcp", "target_tcp"} {
		check(t, &after, id).Observed.Interface = "wg0"
	}
	c := Snapshots(before, after)
	for _, id := range []string{"iface", "internet_tcp", "target_tcp"} {
		got := changeAt(t, c, "checks."+id+".observed.interface")
		if got.Before != "eth0" || got.After != "wg0" {
			t.Errorf("%s interface change = %+v", id, got)
		}
	}
	// The rows whose status did not move still say that something under them
	// did, so a status-only reading cannot miss it.
	for _, row := range c.Checks {
		if row.ID == "iface" && (row.Kind != KindUnchanged || !row.Differs) {
			t.Errorf("iface row = %+v, want an unchanged status that still differs", row)
		}
	}
}

func TestSourceAddressAndBindingChanges(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "iface").Observed.SourceIP = "10.8.0.2"
	after.Options.Source = &snapshot.Source{Interface: "wg0", IPv4: "10.8.0.2"}

	c := Snapshots(before, after)
	if got := changeAt(t, c, "options.source.interface"); got.Kind != KindAdded || got.After != "wg0" {
		t.Errorf("bound interface change = %+v", got)
	}
	if got := changeAt(t, c, "checks.iface.observed.source_ip"); got.Before != "192.0.2.10" || got.After != "10.8.0.2" {
		t.Errorf("source address change = %+v", got)
	}
	// Nothing is invented for the family the binding did not name.
	for _, change := range c.Changes {
		if change.Path == "options.source.ipv6" {
			t.Errorf("reported a change for a source family neither run had: %+v", change)
		}
	}
}

func TestSecondOpinionResolverChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Options.PublicDNS = "9.9.9.9"
	check(t, &after, "dns_public").Observed.Resolver = "9.9.9.9"

	c := Snapshots(before, after)
	if got := changeAt(t, c, "options.public_dns"); got.Before != "8.8.8.8" || got.After != "9.9.9.9" {
		t.Errorf("resolver option change = %+v", got)
	}
	if got := changeAt(t, c, "checks.dns_public.observed.resolver"); got.Kind != KindChanged {
		t.Errorf("observed resolver change = %+v", got)
	}
}

// Switching the second opinion off entirely is not the same as pointing it
// somewhere else, and the empty value has to survive as a removal.
func TestSecondOpinionTurnedOffIsARemoval(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Options.PublicDNS = ""
	c := Snapshots(before, after)
	if got := changeAt(t, c, "options.public_dns"); got.Kind != KindRemoved || got.After != "" {
		t.Errorf("change = %+v, want the option removed", got)
	}
}

// A resolver hands back its records in whatever order it likes, and two
// identical lookups routinely disagree about it. Order alone is not a change.
func TestResolvedAddressOrderIsNotADifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	addrs := check(t, &after, "dns").Observed.Addresses
	if len(addrs) < 2 {
		t.Fatalf("fixture resolves %d addresses, need at least 2", len(addrs))
	}
	slices.Reverse(addrs)
	mustNotChange(t, Snapshots(before, after), "reordering the resolved addresses")
}

func TestResolvedAddressSetDifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	observed := check(t, &after, "dns").Observed
	observed.Addresses = []string{"93.184.216.34", "203.0.113.9"}

	c := Snapshots(before, after)
	added := changeAt(t, c, "checks.dns.observed.addresses.203.0.113.9")
	if added.Kind != KindAdded || added.After != "203.0.113.9" {
		t.Errorf("added address = %+v", added)
	}
	removed := changeAt(t, c, "checks.dns.observed.addresses.2606:2800:220:1:248:1893:25c8:1946")
	if removed.Kind != KindRemoved || removed.Before == "" {
		t.Errorf("removed address = %+v", removed)
	}
	// The address both runs saw is not mentioned at all.
	for _, change := range c.Changes {
		if strings.Contains(change.Path, "93.184.216.34") {
			t.Errorf("unchanged address reported: %+v", change)
		}
	}
}

// Losing every record is not the same shape of event as the resolver answering
// with nothing, and both have to be visible.
func TestEmptyAddressSetAndNoRecordsAreSeparateFacts(t *testing.T) {
	before, after := fixture(t), fixture(t)
	observed := check(t, &after, "dns").Observed
	observed.Addresses = nil
	observed.DNSNotFound = true

	c := Snapshots(before, after)
	if got := changeAt(t, c, "checks.dns.observed.dns_not_found"); got.Before != "no" || got.After != "yes" {
		t.Errorf("dns_not_found change = %+v", got)
	}
	for _, addr := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if got := changeAt(t, c, "checks.dns.observed.addresses."+addr); got.Kind != KindRemoved {
			t.Errorf("%s = %+v, want it removed", addr, got)
		}
	}
}

// Connection attempts are keyed by the address they went to, so the dial order
// that follows the resolver's answer order carries no difference of its own.
func TestConnectionAttemptOrderIsNotADifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	attempts := check(t, &after, "target_tcp").Observed.Attempts
	if len(attempts) < 2 {
		t.Fatalf("fixture made %d attempts, need at least 2", len(attempts))
	}
	slices.Reverse(attempts)
	mustNotChange(t, Snapshots(before, after), "reordering the connection attempts")
}

func TestConnectionAttemptErrorChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	attempts := check(t, &after, "target_tcp").Observed.Attempts
	attempts[0].Error = "connect: connection timed out"

	c := Snapshots(before, after)
	got := changeAt(t, c, "checks.target_tcp.observed.attempts."+attempts[0].IP+".error")
	if got.Kind != KindChanged || !strings.Contains(got.After, "timed out") {
		t.Errorf("attempt change = %+v", got)
	}
}

// The selection is applied as a set, so writing the same probes in another
// order is the same run.
func TestProbeSelectionOrderIsNotADifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	before.Options.Check = []string{"dns", "target_tcp", "tls"}
	after.Options.Check = []string{"tls", "dns", "target_tcp"}
	before.Options.Skip = []string{"ssid", "quic_udp_443"}
	after.Options.Skip = []string{"quic_udp_443", "ssid"}
	mustNotChange(t, Snapshots(before, after), "reordering the probe selection")
}

func TestProbeSelectionMembershipChange(t *testing.T) {
	before, after := fixture(t), fixture(t)
	before.Options.Check = []string{"dns", "tls"}
	after.Options.Check = []string{"dns"}
	c := Snapshots(before, after)
	if got := changeAt(t, c, "options.check"); got.Before != "dns,tls" || got.After != "dns" {
		t.Errorf("check selection change = %+v", got)
	}
}

// Everything that is a clock reading, a stopwatch reading, or a sentence built
// out of one. None of it describes the network, and all of it moves between two
// runs of an unchanged machine.
func TestVolatileMetadataIsNotADifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.CreatedAt = "2027-06-07T08:09:10Z"
	after.Diagnosis.Summary = "a differently worded sentence about the same verdict"
	for i := range after.Checks {
		c := &after.Checks[i]
		c.Name = c.Name + " (renamed by a later build)"
		c.DurationMs += 137
		c.Detail = "a differently worded detail"
		c.Fix = "a differently worded fix"
		if c.Observed == nil {
			continue
		}
		for j := range c.Observed.Attempts {
			c.Observed.Attempts[j].DurationMs += 91
		}
	}
	for i := range after.Diagnosis.Findings {
		after.Diagnosis.Findings[i].Summary = "a differently worded finding"
	}
	mustNotChange(t, Snapshots(before, after), "moving only the timestamps, timings, and derived sentences")
}

// A clock offset is a measurement, and its milliseconds wander on a machine
// whose clock is fine. Whole seconds is the resolution the comparison keeps.
func TestClockOffsetComparesAtWholeSeconds(t *testing.T) {
	offset := func(ms int64) *int64 { return &ms }
	tests := []struct {
		name           string
		before, after  *int64
		want           bool
		wantBefore     string
		wantAfterValue string
	}{
		{"millisecond jitter", offset(3000), offset(3400), false, "", ""},
		{"a real move", offset(3000), offset(7200000), true, "3s", "2h0m0s"},
		{"a reading appearing", nil, offset(7200000), true, "", "2h0m0s"},
		{"a reading disappearing", offset(3000), nil, true, "3s", ""},
		{"a correct clock is not the absence of a reading", nil, offset(0), true, "", "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := fixture(t), fixture(t)
			check(t, &before, "internet_tcp").Observed.ClockOffsetMs = tt.before
			check(t, &after, "internet_tcp").Observed.ClockOffsetMs = tt.after
			c := Snapshots(before, after)
			if !tt.want {
				mustNotChange(t, c, tt.name)
				return
			}
			got := changeAt(t, c, "checks.internet_tcp.observed.clock_offset_ms")
			if got.Before != tt.wantBefore || got.After != tt.wantAfterValue {
				t.Errorf("change = %+v, want %q to %q", got, tt.wantBefore, tt.wantAfterValue)
			}
		})
	}
}

// An inferred outcome and a measured one are not the same state, so the mark
// that says which is compared alongside the status it rewrote.
func TestRelaxedStatusIsItsOwnDifference(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "internet_tcp").Derived = nil

	c := Snapshots(before, after)
	got := changeAt(t, c, "checks.internet_tcp.derived.status_downgraded")
	if got.Before != "yes" || got.After != "no" {
		t.Errorf("change = %+v, want the relaxation to disappear", got)
	}
}

// A portal that advertised no sign-in URL and no portal at all are different
// states, and the empty URL must not read as the absence of interception.
func TestCaptivePortalPresenceIsSeparateFromItsURL(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "internet_tcp").Observed.Portal = &snapshot.Portal{}

	c := Snapshots(before, after)
	if got := changeAt(t, c, "checks.internet_tcp.observed.portal"); got.Kind != KindAdded || got.After != "intercepted" {
		t.Errorf("portal change = %+v", got)
	}
	for _, change := range c.Changes {
		if change.Path == "checks.internet_tcp.observed.portal.redirect_url" {
			t.Errorf("an absent sign-in URL was reported as a change: %+v", change)
		}
	}
}

// A check that only one run has is one difference about the check, not a list
// of differences about every field underneath a row that was never there.
func TestCheckPresentInOnlyOneSnapshot(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Checks = append(after.Checks, snapshot.Check{
		ID: "http", Name: "HTTP example.com", Status: snapshot.StatusFail, Ran: true,
	})
	after.Checks = slices.DeleteFunc(after.Checks, func(c snapshot.Check) bool { return c.ID == "ssid" })

	c := Snapshots(before, after)
	added := changeAt(t, c, "checks.http")
	if added.Kind != KindAdded || added.Check != "http" {
		t.Errorf("added check = %+v", added)
	}
	removed := changeAt(t, c, "checks.ssid")
	if removed.Kind != KindRemoved || removed.Before != "ssid" {
		t.Errorf("removed check = %+v", removed)
	}
	for _, change := range c.Changes {
		if strings.HasPrefix(change.Path, "checks.http.") || strings.HasPrefix(change.Path, "checks.ssid.") {
			t.Errorf("reported a field of a one-sided check: %+v", change)
		}
	}
	rows := map[string]CheckRow{}
	for _, row := range c.Checks {
		rows[row.ID] = row
	}
	if rows["http"].Kind != KindAdded || rows["http"].Before != "" || rows["http"].After != snapshot.StatusFail {
		t.Errorf("http row = %+v", rows["http"])
	}
	if rows["ssid"].Kind != KindRemoved || rows["ssid"].After != "" {
		t.Errorf("ssid row = %+v", rows["ssid"])
	}
}

// Two runs of a machine that did not change produce byte-identical machine
// output, and the human ordering does not depend on Go's map iteration either.
func TestComparisonIsDeterministic(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "dns").Status = snapshot.StatusFail
	check(t, &after, "dns").Observed.Addresses = []string{"203.0.113.9", "203.0.113.10", "198.51.100.4"}
	check(t, &after, "iface").Observed.Interface = "wg0"
	after.Diagnosis.Verdict = "dns"
	after.Options.Check = []string{"tls", "dns"}

	first, err := json.Marshal(Snapshots(before, after))
	if err != nil {
		t.Fatal(err)
	}
	firstText := Snapshots(before, after).Text()
	// Enough runs that a map range would have to be lucky many times over.
	for i := 0; i < 50; i++ {
		again, err := json.Marshal(Snapshots(before, after))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d produced different bytes:\n%s\n%s", i, first, again)
		}
		if got := Snapshots(before, after).Text(); got != firstText {
			t.Fatalf("run %d produced different text:\n%s\n%s", i, firstText, got)
		}
	}
}

// Sections come out in one order, and the checks inside the check section come
// out in the order the later run executed them, not sorted and not hashed.
func TestChangeOrderFollowsTheSnapshotsOwnOrder(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Target.Host = "other.example"
	after.Tool.Version = "9.9.9"
	after.Options.PublicDNS = "9.9.9.9"
	after.Diagnosis.Verdict = "dns"
	// Changed in reverse probe order, so a sorted or an insertion order would
	// both differ from the run's order.
	check(t, &after, "tls").Status = snapshot.StatusFail
	check(t, &after, "dns").Status = snapshot.StatusFail
	check(t, &after, "iface").Status = snapshot.StatusWarn

	want := []string{
		"target.host",
		"tool.version",
		"options.public_dns",
		"diagnosis.verdict",
		"checks.iface.status",
		"checks.dns.status",
		"checks.tls.status",
	}
	if got := paths(Snapshots(before, after)); !slices.Equal(got, want) {
		t.Errorf("change order = %v, want %v", got, want)
	}
}

// A check only the earlier run has still gets a row, after the later run's own
// order rather than interleaved into it.
func TestCheckRowOrderPutsTheLaterRunFirst(t *testing.T) {
	before, after := fixture(t), fixture(t)
	before.Checks = append(before.Checks, snapshot.Check{ID: "quic_udp_443", Status: snapshot.StatusNA})

	var ids []string
	for _, row := range Snapshots(before, after).Checks {
		ids = append(ids, row.ID)
	}
	want := append(checkIDs(after.Checks), "quic_udp_443")
	if !slices.Equal(ids, want) {
		t.Errorf("check rows = %v, want %v", ids, want)
	}
}

// Comparison is defined as two observations, and the snapshot keeps the typed
// spelling next to the parsed host precisely so it can say which kind of
// difference this is. So it reports both, rather than refusing either.
func TestDifferentTargetsAreComparedAndSaidSo(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Target.Raw = "github.com"
	after.Target.Host = "github.com"

	c := Snapshots(before, after)
	if c.SameTarget {
		t.Error("SameTarget = true for two different hosts")
	}
	if got := changeAt(t, c, "target.host"); got.Before != "example.com" || got.After != "github.com" {
		t.Errorf("host change = %+v", got)
	}
	if c.Changes[0].Path != "target.host" {
		t.Errorf("first change = %q, want the target to lead", c.Changes[0].Path)
	}
}

// The same endpoint typed two ways is the same target. The difference in
// spelling is still reported, because it is a real difference in the run, but
// it does not make the two snapshots incomparable.
func TestSameHostTypedDifferentlyIsStillTheSameTarget(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Target.Raw = "https://example.com"

	c := Snapshots(before, after)
	if !c.SameTarget {
		t.Error("SameTarget = false for the same endpoint entered differently")
	}
	if got := paths(c); !slices.Equal(got, []string{"target.raw"}) {
		t.Errorf("changes = %v, want only the typed spelling", got)
	}
}

// A generic run and a targeted one are not two observations of the same thing,
// and the report says so once rather than as a field-by-field list of a target
// that only ever existed on one side.
func TestGenericRunAgainstATargetedOne(t *testing.T) {
	before, after := fixture(t), fixture(t)
	before.Target = nil

	c := Snapshots(before, after)
	if c.SameTarget {
		t.Error("SameTarget = true comparing a generic run with a targeted one")
	}
	got := changeAt(t, c, "target")
	if got.Kind != KindAdded || got.Before != "" || !strings.Contains(got.After, "example.com") {
		t.Errorf("target change = %+v", got)
	}
	for _, change := range c.Changes {
		if strings.HasPrefix(change.Path, "target.") {
			t.Errorf("reported a field of a target only one run had: %+v", change)
		}
	}
	// Two generic runs are two observations of the same thing.
	before.Target, after.Target = nil, nil
	if !Snapshots(before, after).SameTarget {
		t.Error("SameTarget = false for two generic runs")
	}
}

// Two machines, and the report has to say which build and which platform each
// answer came from, since the diagnosis and its advice are chosen per OS.
func TestDifferentMachinesReportTheirTools(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Tool = snapshot.Tool{Version: "1.3.0", OS: "darwin", Arch: "arm64"}

	c := Snapshots(before, after)
	for path, want := range map[string]string{
		"tool.version": "1.3.0", "tool.os": "darwin", "tool.arch": "arm64",
	} {
		if got := changeAt(t, c, path); got.After != want {
			t.Errorf("%s = %+v, want %q", path, got, want)
		}
	}
}

// The comparison is a published shape of its own, versioned separately from the
// snapshot it reads, because scripts consume it.
func TestJSONShape(t *testing.T) {
	before, after := fixture(t), fixture(t)
	check(t, &after, "dns").Status = snapshot.StatusFail

	data, err := json.Marshal(Snapshots(before, after))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Schema     string `json:"schema"`
		SameTarget *bool  `json:"same_target"`
		Before     *struct {
			CreatedAt string `json:"created_at"`
			Tool      struct {
				OS string `json:"os"`
			} `json:"tool"`
		} `json:"before"`
		After  *struct{} `json:"after"`
		Checks []struct {
			ID      string `json:"id"`
			Before  string `json:"before"`
			After   string `json:"after"`
			Kind    string `json:"kind"`
			Differs *bool  `json:"differs"`
		} `json:"checks"`
		Changes []map[string]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "netdoc.comparison.v1" {
		t.Errorf("schema = %q", got.Schema)
	}
	if got.SameTarget == nil || got.Before == nil || got.After == nil {
		t.Fatalf("top level is missing a key: %s", data)
	}
	if got.Before.CreatedAt == "" || got.Before.Tool.OS == "" {
		t.Errorf("before side does not identify its run: %s", data)
	}
	if len(got.Checks) != len(after.Checks) {
		t.Errorf("%d check rows, want %d", len(got.Checks), len(after.Checks))
	}
	if len(got.Changes) != 1 {
		t.Fatalf("%d changes, want 1: %s", len(got.Changes), data)
	}
	// Before and after are always present, so an empty value is a value and
	// never an absent key a reader has to interpret.
	for _, key := range []string{"section", "path", "label", "kind", "before", "after", "summary"} {
		if _, ok := got.Changes[0][key]; !ok {
			t.Errorf("change has no %q key: %s", key, data)
		}
	}
	// And no rendered human text is smuggled into the machine form.
	if strings.Contains(string(data), "Network Doctor snapshot comparison") {
		t.Error("the JSON carries the human report")
	}
}

// Every kind the model can produce spells its two edges the same way, so a
// consumer reads the kind and never has to guess what an empty value meant.
func TestKindsAgreeWithTheirValues(t *testing.T) {
	before, after := fixture(t), fixture(t)
	after.Options.PublicDNS = ""
	after.Options.Source = &snapshot.Source{Interface: "wg0"}
	check(t, &after, "dns").Status = snapshot.StatusFail

	for _, change := range Snapshots(before, after).Changes {
		switch change.Kind {
		case KindAdded:
			if change.Before != "" || change.After == "" {
				t.Errorf("added change has both edges: %+v", change)
			}
		case KindRemoved:
			if change.Before == "" || change.After != "" {
				t.Errorf("removed change has both edges: %+v", change)
			}
		case KindChanged:
			if change.Before == "" || change.After == "" {
				t.Errorf("changed change has an empty edge: %+v", change)
			}
		default:
			t.Errorf("unknown kind %q on %+v", change.Kind, change)
		}
	}
}

// "No differences" is an answer, and it has to encode as an empty array rather
// than as a null a consumer has to interpret. Same for the check rows of a
// comparison of two runs that had none.
func TestEmptyCollectionsEncodeAsArrays(t *testing.T) {
	s := fixture(t)
	data, err := json.Marshal(Snapshots(s, s))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"changes":[]`) {
		t.Errorf("an unchanged comparison does not encode empty changes as an array: %s", data)
	}
	empty := snapshot.Snapshot{Schema: snapshot.Schema, Checks: []snapshot.Check{}}
	data, err = json.Marshal(Snapshots(empty, empty))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"checks":[]`) {
		t.Errorf("a run with no checks does not encode empty rows as an array: %s", data)
	}
}
