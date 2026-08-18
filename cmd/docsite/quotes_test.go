package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The wiki repeats sentences netdoc prints, and the two repositories have no
// other moment in common: staging the site is where they meet, so it is where
// the drift has to be caught. These tests go through build rather than calling
// the checker, so deleting the call is as loud as breaking the check.

// staged writes one program source file and one wiki page over the fixture and
// stages the site, returning whatever the build made of them.
func staged(t *testing.T, program, page string) error {
	t.Helper()
	docs, wiki, shell := fixture(t)
	for _, f := range []struct{ path, body string }{
		{filepath.Join("internal", "diagnostic", "diagnosis.go"), program},
		{filepath.Join(wiki, "Challenge-Mode.md"), "# Challenge Mode\n\n## Scoring\n\n" + page},
	} {
		if err := os.WriteFile(f.path, []byte(f.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return build("/network-doctor", shell, docs, wiki, "assets", filepath.Join(t.TempDir(), "out"))
}

const portalSource = `package diagnostic

func summary(hp string) string {
	if hp == "" {
		return "Behind a captive portal: traffic is intercepted " +
			"until you sign in to the network."
	}
	return "TCP reaches " + hp + " but the TLS handshake fails."
}
`

// Quoting the program is meant to stay comfortable: the wiki wraps its prose,
// writes emphasis around its quotations, and elides the parts of a sentence
// that are not the point. None of that is drift.
func TestWikiMayQuoteTheProgramTheWayAWikiWrites(t *testing.T) {
	for _, tc := range []struct{ name, page string }{
		{
			// The sentence is two adjacent literals in the source. A
			// quotation matches the value they produce, not the layout.
			name: "a sentence the source splits across literals",
			page: "<!-- netdoc-output -->\n*\"Behind a captive portal: traffic is intercepted until you sign in to the network.\"*\n",
		},
		{
			name: "a quotation Markdown wrapped across lines",
			page: "<!-- netdoc-output -->\n**\"Behind a captive portal: traffic is\nintercepted until you sign in to\nthe network.\"**\n",
		},
		{
			name: "a quotation that elides the clause it does not need",
			page: "<!-- netdoc-output -->\n*\"…traffic is intercepted until you sign in…\"*\n",
		},
		{
			name: "a quotation that starts after an interpolated host",
			page: "<!-- netdoc-output -->\n*\"…but the TLS handshake fails.\"*\n",
		},
		{
			// Fences hold commands to type and sketches of rows, not
			// sentences the program prints.
			name: "program text inside a fenced block",
			page: "<!-- netdoc-output -->\n*\"…but the TLS handshake fails.\"*\n\n```\n\"Behind a captive portal: traffic is intercepted until you sign in to the network.\"\n```\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := staged(t, portalSource, tc.page); err != nil {
				t.Fatalf("the build rejected a quotation the program does print: %v", err)
			}
		})
	}
}

// Each case is a way the wiki and the program could come apart without anybody
// editing both, which is the only reason this check exists.
func TestWikiQuotationsThatNoLongerMatchFailTheBuild(t *testing.T) {
	for _, tc := range []struct{ name, page, want string }{
		{
			name: "a quotation the program stopped printing",
			page: "<!-- netdoc-output -->\n*\"Behind a captive portal: traffic is intercepted until you log in.\"*\n",
			want: "netdoc prints no such text",
		},
		{
			// Demoting a verified sentence to prose is one deleted line,
			// so the unmarked direction is checked too.
			name: "program output quoted without the marker",
			page: "*\"Behind a captive portal: traffic is intercepted until you sign in to the network.\"*\n",
			want: "carries no <!-- netdoc-output --> marker",
		},
		{
			// A marker that drifted away from its quotation would
			// otherwise verify whatever sentence came next.
			name: "a marker that claims nothing",
			page: "<!-- netdoc-output -->\n\nUnrelated prose, and then a quotation of *\"something else entirely\"*.\n",
			want: "marks nothing",
		},
		{
			// An elision cannot bridge a value the program interpolates:
			// the two halves are not one sentence.
			name: "an elision reaching across an interpolated value",
			page: "<!-- netdoc-output -->\n*\"TCP reaches…until you sign in to the network.\"*\n",
			want: "netdoc prints no such text",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := staged(t, portalSource, tc.page)
			if err == nil {
				t.Fatal("the build published a wiki quotation the program does not back")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "Challenge-Mode.md:") {
				t.Errorf("error %q does not name the page and line that failed", err)
			}
		})
	}
}

// A stale quotation is fixed by comparing it with the sentence it drifted from,
// so the failure carries that sentence rather than leaving it to be hunted.
func TestAStaleQuotationIsReportedBesideTheProgramText(t *testing.T) {
	err := staged(t, portalSource,
		"<!-- netdoc-output -->\n*\"Behind a captive portal: traffic is intercepted until you log in.\"*\n")
	if err == nil {
		t.Fatal("a stale quotation was published")
	}
	if !strings.Contains(err.Error(), "until you sign in to the network.") {
		t.Errorf("error %q does not show the program string the quotation drifted from", err)
	}
}

// Deleting every marker would otherwise be the quiet way to switch the whole
// check off.
func TestAWikiWithNoMarkedQuotationsFailsTheBuild(t *testing.T) {
	err := staged(t, portalSource, "Nothing quoted here.\n")
	if err == nil {
		t.Fatal("the build accepted a wiki with nothing verified in it")
	}
	if !strings.Contains(err.Error(), "no <!-- netdoc-output --> marker anywhere") {
		t.Errorf("error %q does not say the verification was removed rather than passing", err)
	}
}
