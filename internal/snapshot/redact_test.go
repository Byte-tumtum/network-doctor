package snapshot

import (
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func supportFixture() Snapshot {
	const portalURL = "https://portal-user:portal-password@portal.unique.local/private/path?cookie=unique-cookie" //nolint:gosec // Deliberate credential sentinels verify redaction.
	clock := int64(1234)
	metric := 7
	return Snapshot{
		Schema:    Schema,
		CreatedAt: "2026-08-25T17:00:00Z",
		Tool:      Tool{Version: "1.2.3", OS: "linux", Arch: "amd64"},
		Target: &Target{
			Raw: "https://uniquesupporthost.local:8443/private", Host: "uniquesupporthost.local",
			Port: 8443, Protocol: "tls+http", PortExplicit: true,
		},
		Options: Options{
			ProbeTimeoutMs: 4000, PublicDNS: "9.9.9.9", Check: []string{"dns"},
			Source: &Source{Interface: "unique-wg-interface", IPv4: "10.23.45.67", IPv6: "fd12:3456::67"},
		},
		Checks: []Check{{
			ID: "dns", Name: "DNS uniquesupporthost.local", Status: StatusFail,
			Cause: "timeout", Ran: true, DurationMs: 12,
			Detail: "user=uniquesupportuser hostname=uniquemachine SSID=Unique Support SSID " +
				"path /home/uniquesupportuser/private/config Authorization: Bearer unique-bearer-token\n" +
				"proxy https://proxy-user:proxy-password@proxy.unique.internal/path?token=unique-query-token " +
				"certificate is for unique-cert-name AWS_SECRET_ACCESS_KEY=unique-env-secret " +
				"-----BEGIN PRIVATE KEY-----unique-private-key-----END PRIVATE KEY-----",
			Fix: "inspect C:\\Users\\uniquesupportuser\\unique-private-file and password=unique-password",
			Observed: &Observed{
				Addresses:  []string{"10.23.45.67", "10.23.45.68", "93.184.216.34"},
				SelectedIP: "10.23.45.67", Resolver: "10.23.45.68",
				SourceIP: "203.0.113.99", Interface: "unique-wg-interface",
				SSID: "Unique Support SSID", Families: &Families{IPv4: "reachable", IPv6: "unreachable"},
				Portal: &Portal{RedirectURL: portalURL},
				Attempts: []Attempt{
					{IP: "10.23.45.67", Error: "Cookie: session=unique-cookie\nBearer unique-nested-token", Cause: "timeout"},
					{IP: "10.23.45.68", DurationMs: 3},
				},
				ClockOffsetMs: &clock, Timeout: true,
				Routes: []Route{{
					Destination: "93.184.216.34", Family: "ipv4", Interface: "unique-wg-interface",
					Gateway: "10.23.45.67", Source: "203.0.113.99", Prefix: "10.23.45.0/24",
					Metric: &metric, Table: "unique-corporate-table", InterfaceMTU: 1420,
					Tunnel: "tunnel", TunnelKind: "wireguard", Reason: "lowest_metric",
					Competing: []CompetingRoute{{Interface: "unique-ethernet-interface", Metric: 20}},
				}},
			},
		}},
		Diagnosis: Diagnosis{
			Verdict: "dns", Summary: "uniquesupporthost.local failed from unique-wg-interface at 10.23.45.67",
			Blamed: "dns", FailedStage: "dns",
			Findings: []Finding{{
				ID: "dns_failure", Verdict: "dns",
				Summary: "uniquesupporthost.local failed; token=unique-finding-token",
				Focus:   "dns", Evidence: []string{"dns"},
				CausalEvidence: []CausalEvidence{{
					Kind: EvidenceSupport, Check: "dns", Observation: ObservationDNSAnswers, Value: "10.23.45.67",
				}},
			}},
		},
	}
}

func TestSupportSerializationContainsNoSensitiveOriginals(t *testing.T) {
	original := supportFixture()
	before := supportFixture()
	sanitized := SanitizeForSupport(original)
	if !reflect.DeepEqual(original, before) {
		t.Fatal("SanitizeForSupport modified the full-fidelity snapshot")
	}
	data, err := Encode(sanitized)
	if err != nil {
		t.Fatalf("Encode sanitized snapshot: %v", err)
	}
	for _, secret := range []string{
		"uniquesupportuser", "uniquesupporthost.local", "uniquemachine", "Unique Support SSID",
		"unique-wg-interface", "unique-ethernet-interface", "unique-corporate-table",
		"/home/uniquesupportuser/private/config", `C:\Users\uniquesupportuser\unique-private-file`,
		"proxy-user", "proxy-password", "portal-user", "portal-password",
		"unique-bearer-token", "unique-query-token", "unique-cookie", "unique-nested-token",
		"unique-password", "unique-finding-token", "10.23.45.67", "10.23.45.68",
		"10.23.45.0/24", "fd12:3456::67", "203.0.113.99", "93.184.216.34",
		"unique-cert-name", "unique-private-key",
		"unique-env-secret",
	} {
		if strings.Contains(string(data), secret) {
			t.Errorf("serialized support snapshot leaked %q:\n%s", secret, data)
		}
	}
	for _, retained := range []string{"9.9.9.9", `"sanitized": true`, `"policy": "support-v1"`} {
		if !strings.Contains(string(data), retained) {
			t.Errorf("serialized support snapshot lost %q:\n%s", retained, data)
		}
	}
}

func TestSupportPseudonymsPreserveRelationshipsAndRoundTrip(t *testing.T) {
	original := supportFixture()
	first := SanitizeForSupport(original)
	second := SanitizeForSupport(original)
	firstData, err := Encode(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := Encode(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstData) != string(secondData) {
		t.Fatal("the same support snapshot did not sanitize deterministically")
	}

	observed := first.Checks[0].Observed
	if observed.Addresses[0] != observed.SelectedIP || observed.SelectedIP != observed.Routes[0].Gateway ||
		observed.SelectedIP != first.Options.Source.IPv4 || observed.SelectedIP != first.Diagnosis.Findings[0].CausalEvidence[0].Value {
		t.Errorf("repeated private IP did not keep one pseudonym: %+v", first)
	}
	if observed.Addresses[0] == observed.Addresses[1] {
		t.Errorf("distinct private IPs collapsed to %q", observed.Addresses[0])
	}
	rawTarget, err := url.Parse(first.Target.Raw)
	if err != nil || first.Target.Host == "" || first.Target.Host != rawTarget.Hostname() {
		t.Errorf("target relationship was not preserved: %+v", first.Target)
	}
	if !strings.Contains(first.Checks[0].Name, first.Target.Host) || !strings.Contains(first.Diagnosis.Summary, first.Target.Host) {
		t.Errorf("target pseudonym was not reused in derived text: check=%q summary=%q", first.Checks[0].Name, first.Diagnosis.Summary)
	}
	portalURL, err := url.Parse(observed.Portal.RedirectURL)
	if err != nil || portalURL.Hostname() == first.Target.Host {
		t.Errorf("distinct portal and target hostnames collapsed: target=%q portal=%q", first.Target.Host, observed.Portal.RedirectURL)
	}
	publicAlias, err := netip.ParseAddr(observed.Addresses[2])
	if err != nil || publicAlias.IsPrivate() || publicAlias == netip.MustParseAddr("93.184.216.34") {
		t.Errorf("public destination class was not preserved by its pseudonym: %q", observed.Addresses[2])
	}
	mappedPrefix, err := netip.ParsePrefix(observed.Routes[0].Prefix)
	if err != nil || !mappedPrefix.Contains(netip.MustParseAddr(observed.Routes[0].Gateway)) {
		t.Errorf("gateway %q lost its route-prefix relationship to %q", observed.Routes[0].Gateway, observed.Routes[0].Prefix)
	}
	sharedResolver := original
	sharedResolver.Options.PublicDNS = "100.64.0.53"
	if got := SanitizeForSupport(sharedResolver).Options.PublicDNS; got == sharedResolver.Options.PublicDNS {
		t.Errorf("shared-address-space resolver was retained as if it were public: %q", got)
	}

	decoded, err := Decode(firstData)
	if err != nil {
		t.Fatalf("Decode support snapshot: %v", err)
	}
	if decoded.Redaction == nil || !decoded.Redaction.Sanitized || decoded.Redaction.Policy != SupportRedactionPolicy {
		t.Fatalf("redaction metadata did not round trip: %+v", decoded.Redaction)
	}
}

// Resanitizing a support artifact is not part of the support-v1 contract. This
// fixture demonstrates why callers must not use fixed-point equality to prove
// that an artifact was produced by SanitizeForSupport.
func TestSupportResanitizationChangesComprehensiveFixture(t *testing.T) {
	once := SanitizeForSupport(supportFixture())
	if once.Redaction == nil || !once.Redaction.Sanitized || once.Redaction.Policy != SupportRedactionPolicy {
		t.Fatalf("first pass has invalid redaction metadata: %+v", once.Redaction)
	}
	twice := SanitizeForSupport(once)
	onceData, err := Encode(once)
	if err != nil {
		t.Fatal(err)
	}
	twiceData, err := Encode(twice)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(once, twice) || string(onceData) == string(twiceData) {
		t.Fatal("fixture no longer demonstrates non-idempotent support redaction; reevaluate artifact validation")
	}
	t.Logf("private address changed from %s to %s; route prefix changed from %s to %s",
		once.Checks[0].Observed.SelectedIP, twice.Checks[0].Observed.SelectedIP,
		once.Checks[0].Observed.Routes[0].Prefix, twice.Checks[0].Observed.Routes[0].Prefix)
}

func TestSupportIncidentUsesOneMappingAndMarksNestedSnapshots(t *testing.T) {
	onset := supportFixture()
	onset.CreatedAt = "2026-08-25T17:00:00Z"
	before := supportFixture()
	before.CreatedAt, before.OK = "2026-08-25T16:59:55Z", true
	before.Checks[0].Status = StatusPass
	recovered := supportFixture()
	recovered.CreatedAt, recovered.OK = "2026-08-25T17:00:05Z", true
	recovered.Checks[0].Status = StatusPass
	onset.Incident = &Incident{
		StartedAt: onset.CreatedAt, EndedAt: recovered.CreatedAt, Passes: 1,
		Before: &before, Recovered: &recovered,
	}

	data, err := Encode(SanitizeForSupport(onset))
	if err != nil {
		t.Fatalf("Encode sanitized incident: %v", err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode sanitized incident: %v", err)
	}
	for _, secret := range []string{"uniquesupporthost.local", "Unique Support SSID", "unique-wg-interface", "10.23.45.67"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("serialized support incident leaked %q", secret)
		}
	}
	for name, state := range map[string]*Snapshot{
		"onset": &got, "before": got.Incident.Before, "recovered": got.Incident.Recovered,
	} {
		if state.Redaction == nil || !state.Redaction.Sanitized {
			t.Errorf("%s snapshot has no redaction metadata", name)
		}
		if state.Target.Host != got.Target.Host {
			t.Errorf("%s target = %q, want shared pseudonym %q", name, state.Target.Host, got.Target.Host)
		}
	}
}

func TestOlderSnapshotRemainsFullFidelity(t *testing.T) {
	original := supportFixture()
	data, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Redaction != nil || decoded.Target.Host != original.Target.Host || !strings.Contains(string(data), original.Target.Host) {
		t.Errorf("ordinary snapshot was unexpectedly sanitized: %+v", decoded)
	}

	old, err := Decode([]byte(`{"schema":"` + Schema + `","created_at":"2026-08-25T12:00:00Z","target":null,"options":{"probe_timeout_ms":0,"public_dns":""},"checks":[],"diagnosis":{"verdict":"ok","summary":"healthy"},"ok":true}`))
	if err != nil || old.Redaction != nil {
		t.Fatalf("older v1 compatibility changed: redaction=%+v err=%v", old.Redaction, err)
	}
}

// A /32 host route is ordinary on a VPN, and a mapped /32 holds exactly one
// address. When that one address collides with something the snapshot already
// contains, the pseudonym search cannot count its way out, so it has to give up
// and take a family-wide alias instead of spinning. This ran forever before the
// search was bounded.
func TestSupportSanitizerTerminatesOnNarrowRoutePrefix(t *testing.T) {
	pinLocalIdentity(t)
	s := Snapshot{
		Schema: Schema, CreatedAt: "2026-08-25T12:00:00Z",
		Checks: []Check{{ID: "route", Name: "route", Status: StatusPass, Observed: &Observed{
			// 10.0.1.0 is the address the mapped /32 below wants to hand out,
			// and it is already an original somewhere else in this snapshot.
			Addresses: []string{"10.0.1.0"},
			Routes:    []Route{{Destination: "10.9.9.9", Prefix: "10.9.9.9/32"}},
		}}},
		Diagnosis: Diagnosis{Verdict: "ok", Summary: "healthy"},
	}
	done := make(chan Snapshot, 1)
	go func() { done <- SanitizeForSupport(s) }()
	select {
	case got := <-done:
		observed := got.Checks[0].Observed
		if observed.Routes[0].Destination == "10.9.9.9" || observed.Addresses[0] == "10.0.1.0" {
			t.Errorf("narrow-prefix fallback stopped sanitizing: %+v", observed)
		}
		if observed.Routes[0].Destination == observed.Addresses[0] {
			t.Errorf("two different addresses collapsed onto %q", observed.Addresses[0])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SanitizeForSupport did not terminate on a /32 route prefix")
	}
}

// The machine's own name and account name are in no structured field, and
// neither has a shape a pattern could match: "labbox" and "jrivera" are just
// words. They are seeded before the walk so the text passes replace them the
// way they replace any other known value.
func TestSupportRedactsLocalMachineIdentity(t *testing.T) {
	pinLocalIdentity(t)
	s := Snapshot{
		Schema: Schema, CreatedAt: "2026-08-25T12:00:00Z",
		Checks: []Check{{
			ID: "tls", Name: "TLS", Status: StatusFail,
			Detail: "certificate is for sanitizer-test-box.example, presented to sanitizer-test-box",
			Fix:    "run as sanitizer-test-account or fix the name",
		}},
		Diagnosis: Diagnosis{Verdict: "tls", Summary: "sanitizer-test-box.example is not the expected name"},
	}
	data, err := Encode(SanitizeForSupport(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sanitizer-test-box", "sanitizer-test-account"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("support snapshot leaked local identity %q:\n%s", secret, data)
		}
	}
	// The fully qualified name and the short name are one machine, so they
	// share one alias rather than reading as two different hosts.
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	detail := got.Checks[0].Detail
	if before, after, ok := strings.Cut(detail, ", presented to "); !ok ||
		!strings.HasSuffix(before, after) {
		t.Errorf("the machine's long and short names did not share one alias: %q", detail)
	}
}

// A generic account name identifies nobody and appears inside ordinary words,
// so seeding one would rewrite diagnostic text for no privacy gain.
func TestSupportLeavesGenericLocalIdentityAlone(t *testing.T) {
	original := localIdentity
	localIdentity = func() (string, string) { return "localhost", "root" }
	t.Cleanup(func() { localIdentity = original })
	s := Snapshot{
		Schema: Schema, CreatedAt: "2026-08-25T12:00:00Z",
		Checks:    []Check{{ID: "dns", Name: "DNS", Status: StatusPass, Detail: "resolved via localhost as root"}},
		Diagnosis: Diagnosis{Verdict: "ok", Summary: "healthy"},
	}
	if got := SanitizeForSupport(s).Checks[0].Detail; got != "resolved via localhost as root" {
		t.Errorf("detail = %q, want the generic names left alone", got)
	}
}

// Text rewriting must not corrupt the diagnosis it is trying to protect. Both
// cases here came out of a real run: a check named "QUIC / UDP 443" registered
// a bare "/" as a known value, and every path separator in the artifact after
// it was rewritten.
func TestSupportKeepsUnrelatedTextIntact(t *testing.T) {
	pinLocalIdentity(t)
	s := Snapshot{
		Schema: Schema, CreatedAt: "2026-08-25T12:00:00Z",
		Checks: []Check{
			{ID: "quic", Name: "QUIC / UDP 443", Status: StatusPass,
				Detail: "QUIC v1 handshake with host.example over UDP/443 in 53ms (h3)"},
			{ID: "proxy", Name: "Proxy", Status: StatusNA,
				Detail: "no proxy in environment (HTTPS_PROXY/HTTP_PROXY/ALL_PROXY unset)"},
			// "lo" is a real interface name and a substring of ordinary words.
			{ID: "iface", Name: "Interface", Status: StatusPass,
				Detail:   "hello from interface lo, below the loopback",
				Observed: &Observed{Interface: "lo"}},
		},
		Diagnosis: Diagnosis{Verdict: "ok", Summary: "healthy"},
	}
	got := SanitizeForSupport(s)
	if want := "no proxy in environment (HTTPS_PROXY/HTTP_PROXY/ALL_PROXY unset)"; got.Checks[1].Detail != want {
		t.Errorf("proxy detail = %q, want %q", got.Checks[1].Detail, want)
	}
	if !strings.Contains(got.Checks[0].Detail, "UDP/443") {
		t.Errorf("QUIC detail lost its port separator: %q", got.Checks[0].Detail)
	}
	iface := got.Checks[2].Observed.Interface
	if iface == "lo" {
		t.Errorf("the interface name was not pseudonymized: %q", iface)
	}
	// The name is replaced where it stands alone and nowhere else.
	detail := got.Checks[2].Detail
	if !strings.HasPrefix(detail, "hello from interface "+iface+",") || !strings.HasSuffix(detail, "the loopback") {
		t.Errorf("interface detail = %q, want %q replaced only where it stands alone", detail, iface)
	}
}

// An address that appears in no structured field is caught by the text pass
// alone, which has to read the dot that ends a sentence as punctuation rather
// than as part of the address before it.
func TestSupportRedactsAddressesFoundOnlyInText(t *testing.T) {
	pinLocalIdentity(t)
	s := Snapshot{
		Schema: Schema, CreatedAt: "2026-08-25T12:00:00Z",
		Checks: []Check{{ID: "route", Name: "Route", Status: StatusFail,
			Detail: "no route to 192.168.7.31.",
			Fix:    "check the gateway at 192.168.7.31, then retry"}},
		Diagnosis: Diagnosis{Verdict: "route", Summary: "unreachable via 192.168.7.31."},
	}
	got := SanitizeForSupport(s)
	data, err := Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "192.168.7.31") {
		t.Errorf("an address reached the artifact through free text alone:\n%s", data)
	}
	// One address, one pseudonym, whether or not a period followed it.
	detail := strings.TrimSuffix(strings.TrimPrefix(got.Checks[0].Detail, "no route to "), ".")
	summary := strings.TrimSuffix(strings.TrimPrefix(got.Diagnosis.Summary, "unreachable via "), ".")
	if detail != summary || !strings.Contains(got.Checks[0].Fix, detail) {
		t.Errorf("one address got more than one pseudonym: detail=%q summary=%q fix=%q",
			detail, summary, got.Checks[0].Fix)
	}
}
