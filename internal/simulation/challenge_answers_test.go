package simulation

import (
	"encoding/json"
	"strings"
	"testing"
)

// The catalog is the single source of truth for the answer vocabulary, so
// everything the rest of the game keys on has to be unique within it: the id a
// result records, the label a person reads, and every spelling input accepts.
func TestChallengeAnswerCatalogIsUnambiguous(t *testing.T) {
	ids := map[ChallengeAnswer]bool{}
	spellings := map[string]ChallengeAnswer{}
	for _, item := range ChallengeAnswerMenu {
		if item.ID == "" || item.Label == "" || item.Help == "" {
			t.Fatalf("answer %+v is missing an id, a label or its help text", item)
		}
		if ids[item.ID] {
			t.Fatalf("duplicate answer id %s", item.ID)
		}
		ids[item.ID] = true
		// An id that reads like prose would be one somebody uses as a display
		// string, and then reworded.
		if string(item.ID) != normalizeAnswerName(string(item.ID)) {
			t.Fatalf("answer id %q is not a stable machine name", item.ID)
		}
		if item.Label == string(item.ID) {
			t.Fatalf("answer %s does not distinguish its identity from its display name", item.ID)
		}
		for _, spelling := range append([]string{string(item.ID), item.Label}, item.Aliases...) {
			key := normalizeAnswerName(spelling)
			if key == "" {
				t.Fatalf("answer %s has an empty spelling", item.ID)
			}
			// An answer's own spellings are allowed to coincide: "No default route"
			// normalizes to exactly its id, which is one answer reachable two ways
			// rather than two answers fighting over one word. Across answers it is a
			// collision, and the input would silently pick whichever came first.
			if owner, ok := spellings[key]; ok && owner != item.ID {
				t.Fatalf("%q selects both %s and %s", spelling, owner, item.ID)
			}
			spellings[key] = item.ID
		}
	}
}

// Every alias is deliberate, so every alias is named here. A new one has to be
// added to this table too, which is the point: an alias nobody wrote down is an
// accident, and an accident that silently selects a diagnosis is the worst kind.
func TestChallengeAnswerAliasesAreDeliberate(t *testing.T) {
	for _, tt := range []struct {
		typed string
		want  ChallengeAnswer
	}{
		{"ok", AnswerHealthy},
		{"none", AnswerHealthy},
		{"nothing", AnswerHealthy},
		{"dns", AnswerDNSFailure},
		{"nxdomain", AnswerDNSFailure},
		{"dns_timeout", AnswerDNSFailure},
		{"servfail", AnswerDNSFailure},
		{"no_route", AnswerNoDefaultRoute},
		{"bad_gateway", AnswerWrongDefaultRoute},
		{"wrong_gateway", AnswerWrongDefaultRoute},
		{"missing_route", AnswerMissingRoute},
		{"preferred_route", AnswerPreferredRoute},
		{"ipv4", AnswerIPv4Failure},
		{"ipv6", AnswerIPv6Failure},
		{"port_blocked", AnswerPortBlocked},
		{"blocked", AnswerPortBlocked},
		{"filtered", AnswerPortBlocked},
		{"refused", AnswerRefused},
		{"reset", AnswerReset},
		{"tls_expired", AnswerTLSCertificate},
		{"expired_certificate", AnswerTLSCertificate},
		{"tls_hostname", AnswerTLSHostname},
		{"hostname_mismatch", AnswerTLSHostname},
		{"http_error", AnswerHTTPService},
		{"proxy", AnswerProxy},
		{"quic", AnswerQUICBlocked},
		{"loss", AnswerPacketLoss},
		{"packet_loss", AnswerPacketLoss},
	} {
		got, ok := ChallengeAnswerByName(tt.typed)
		if !ok || got.ID != tt.want {
			t.Errorf("ChallengeAnswerByName(%q) = %q, %t; want %s", tt.typed, got.ID, ok, tt.want)
		}
	}
	// Nothing on the menu may be reachable by a spelling this table has not
	// listed, so the aliases stay a reviewed set rather than a growing one.
	listed := map[string]bool{}
	for _, item := range ChallengeAnswerMenu {
		for _, alias := range item.Aliases {
			listed[normalizeAnswerName(alias)] = true
		}
	}
	if len(listed) != 28 {
		t.Errorf("the catalog carries %d aliases; this test pins 28. Add the new one above.", len(listed))
	}
}

// What a person types is matched whole. Case, spaces, hyphens and underscores
// are the differences nobody means; anything else is a mistype, and a mistype
// has to be asked again rather than resolved to whatever it resembled.
func TestChallengeAnswerByNameIsExactNotFuzzy(t *testing.T) {
	for _, typed := range []string{"tcp_port_blocked", "TCP_PORT_BLOCKED", "tcp-port-blocked",
		"TCP port blocked", "  tcp port blocked  "} {
		got, ok := ChallengeAnswerByName(typed)
		if !ok || got.ID != AnswerPortBlocked {
			t.Errorf("ChallengeAnswerByName(%q) = %q, %t", typed, got.ID, ok)
		}
	}
	// Prefixes, substrings and near misses select nothing. `tcp` alone is the
	// important one: it is a prefix of `tcp_port_blocked` and a substring of the
	// reset label, so a fuzzy matcher would answer it with one of the two and the
	// player would never know which.
	for _, typed := range []string{"", "  ", "tcp", "tcp_port", "port", "dnsfailure", "DNS resolution!",
		"connection", "route", "tls", "expired", "d", "1", "dns resolution extra"} {
		if got, ok := ChallengeAnswerByName(typed); ok {
			t.Errorf("ChallengeAnswerByName(%q) resolved to %s; it must ask again", typed, got.ID)
		}
	}
}

// ChallengeAnswerByID stays narrow. It is the lookup a stored result and the
// recognizer table go through, so accepting a label or a shorthand there would
// let one answer arrive under several spellings and be compared as several.
func TestChallengeAnswerByIDAcceptsOnlyIdentities(t *testing.T) {
	if _, ok := ChallengeAnswerByID("tcp_port_blocked"); !ok {
		t.Fatal("ChallengeAnswerByID rejects an id")
	}
	for _, raw := range []string{"TCP port blocked", "blocked", "tcp-port-blocked"} {
		if _, ok := ChallengeAnswerByID(raw); ok {
			t.Errorf("ChallengeAnswerByID(%q) resolved; identities only", raw)
		}
	}
}

// A machine-readable result keys on the identity and carries the display name
// beside it. A consumer that matched on the label would break the next time the
// label was reworded, which is exactly why both are there.
func TestChallengeResultCarriesIdentityAndDisplayName(t *testing.T) {
	challenge := challengeWithMutation(t, "service.tcp_reset")
	result := ScoreChallenge(challenge, resetChallengeReport(t, challenge, "connection_reset"),
		ChallengeSubmission{Answer: AnswerReset})
	blob, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Truth struct {
			Answer, Label string
		}
		Human struct {
			Answer, Label string
		}
	}
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatal(err)
	}
	info, ok := ChallengeAnswerByID(string(AnswerReset))
	if !ok {
		t.Fatal("the reset answer is not in the catalog")
	}
	for _, tt := range []struct{ field, answer, label string }{
		{"truth", decoded.Truth.Answer, decoded.Truth.Label},
		{"human", decoded.Human.Answer, decoded.Human.Label},
	} {
		if tt.answer != string(AnswerReset) {
			t.Errorf("%s.answer = %q, want the stable id %q", tt.field, tt.answer, AnswerReset)
		}
		if tt.label != info.Label {
			t.Errorf("%s.label = %q, want the catalog's %q", tt.field, tt.label, info.Label)
		}
	}
}

// The menu is the one place the vocabulary is written down, so the rendered menu
// has to be the catalog rather than a second list beside it.
func TestAnswerMenuRendersTheCatalog(t *testing.T) {
	var out strings.Builder
	WriteAnswerMenu(&out)
	text := out.String()
	for _, item := range ChallengeAnswerMenu {
		if !strings.Contains(text, item.Label) {
			t.Errorf("the menu does not offer %q", item.Label)
		}
		if !strings.Contains(text, item.Help) {
			t.Errorf("the menu does not explain %q", item.Label)
		}
	}
	// And it tells the player the three things that are not a diagnosis.
	for _, key := range []string{"s.", "b.", "q."} {
		if !strings.Contains(text, key) {
			t.Errorf("the menu does not offer %q", key)
		}
	}
}
