package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The wiki repeats sentences netdoc prints, and the two repositories have no
// other moment in common: staging the site is where they meet, so it is where
// the drift has to be caught. These tests go through build rather than calling
// the checker, so deleting the call is as loud as breaking the check.

// diagnosisFile, helpFile and grammarFile are the fixture's stand-ins for the
// files programText names, addressed the way that list addresses them.
const (
	diagnosisFile = "internal/diagnostic/diagnosis.go"
	fixHintsFile  = "internal/diagnostic/fixhints.go"
	grammarFile   = "internal/diagnostic/target.go"
	helpFile      = "main.go"
)

// staged writes one diagnosis source file and one wiki page over the fixture
// and stages the site, returning whatever the build made of them.
func staged(t *testing.T, program, page string) error {
	t.Helper()
	return stagedFrom(t, map[string]string{diagnosisFile: program}, page)
}

// stagedFrom is staged over an arbitrary set of source files, so a test can say
// which file a quotation is supposed to resolve in, or put a sentence in a file
// programText does not name.
func stagedFrom(t *testing.T, sources map[string]string, page string) error {
	t.Helper()
	docs, wiki, shell := fixture(t)
	for rel, body := range sources {
		path := filepath.FromSlash(rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pagePath := filepath.Join(wiki, "Challenge-Mode.md")
	if err := os.WriteFile(pagePath, []byte("# Challenge Mode\n\n## Scoring\n\n"+page), 0o600); err != nil {
		t.Fatal(err)
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
	// With several files in the corpus, the sentence alone no longer says
	// where to go and edit it.
	if !strings.Contains(err.Error(), diagnosisFile+":") {
		t.Errorf("error %q does not name the source file the program string lives in", err)
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

// The help text netdoc prints is assembled in main.go from its own banner and
// the target grammar target.go owns, so a wiki page quoting either half is
// quoting the program just as much as a page quoting a diagnosis. Each of these
// sources contributes exactly the sentences it writes.
const (
	// The banner wraps mid-sentence, the way the real one does, so a
	// quotation of it has to survive the Go source's own line breaks as well
	// as the wiki's.
	helpSource = `package main

func printUsage(w io.Writer) {
	fmt.Fprint(w, "Usage: netdoc [flags] [target]\n\nDiagnoses network connectivity layer by layer. Flags may be\ngiven before or after the target.\n")
}
`
	grammarSource = `package diagnostic

const TargetForms = "  example.com:8022       hostname with port (protocol inferred from the port)"
`
	// The Fix line under a failed row, per-GOOS the way the real hints are.
	fixHintsSource = `package diagnostic

func dnsFix(goos string) string {
	if goos == "windows" {
		return "name resolution failing: check DNS in ` + "`ipconfig /all`" + `"
	}
	return "name resolution failing: check /etc/resolv.conf / DNS"
}
`
)

// corpusSources is every file programText names, each holding the text the
// tests below quote from it.
func corpusSources() map[string]string {
	return map[string]string{
		diagnosisFile: portalSource,
		fixHintsFile:  fixHintsSource,
		helpFile:      helpSource,
		grammarFile:   grammarSource,
	}
}

// The corpus is more than the diagnosis file now, so a marked quotation has to
// resolve wherever netdoc's own words actually live.
func TestAQuotationMayResolveInAnyNamedSourceFile(t *testing.T) {
	for _, tc := range []struct{ name, page string }{
		{
			name: "a diagnosis summary",
			page: "<!-- netdoc-output -->\n*\"Behind a captive portal: traffic is intercepted until you sign in to the network.\"*\n",
		},
		{
			// Wrapped in the Go source and rewrapped by the wiki, on
			// different words.
			name: "the usage banner --help prints",
			page: "<!-- netdoc-output -->\n*\"Diagnoses network connectivity layer by\nlayer. Flags may be given before or after the target.\"*\n",
		},
		{
			// A column-aligned grammar table; the padding between the form
			// and its description is layout, not text to reproduce.
			name: "a line of the target grammar --help renders",
			page: "<!-- netdoc-output -->\n*\"example.com:8022 hostname with port (protocol inferred from the port)\"*\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := stagedFrom(t, corpusSources(), tc.page); err != nil {
				t.Fatalf("the build rejected a quotation the program does print: %v", err)
			}
		})
	}
}

// Widening the corpus is only worth anything if drift in the newly named files
// is as loud as drift in the diagnosis file: reword the sentence, or delete it,
// and the quotation has to stop verifying.
func TestDriftInANewlyNamedSourceFileFailsTheBuild(t *testing.T) {
	const quoted = "<!-- netdoc-output -->\n*\"Diagnoses network connectivity layer by layer. Flags may be given before or after the target.\"*\n"
	for _, tc := range []struct{ name, help string }{
		{
			name: "the help text was reworded",
			help: strings.Replace(helpSource, "layer by layer", "one layer at a time", 1),
		},
		{
			name: "the help text was deleted",
			help: "package main\n\nconst other = \"netdoc: -timeout must be positive\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sources := corpusSources()
			sources[helpFile] = tc.help
			err := stagedFrom(t, sources, quoted)
			if err == nil {
				t.Fatal("the build published a quotation of help text the program no longer prints")
			}
			if !strings.Contains(err.Error(), "netdoc prints no such text") {
				t.Errorf("error %q does not explain that the program prints no such text", err)
			}
			if !strings.Contains(err.Error(), "Challenge-Mode.md:") {
				t.Errorf("error %q does not name the page and line that failed", err)
			}
		})
	}
}

// The corpus is a list of named files, not a walk: a sibling in the same
// directory is implementation, and marking it verified would quietly turn the
// marker back into "some literal exists somewhere under internal".
func TestASiblingOfANamedFileIsNotQuotable(t *testing.T) {
	sources := corpusSources()
	sources["internal/diagnostic/checks.go"] = `package diagnostic

const probeFailure = "The neighbour file is implementation, not something netdoc says."
`
	err := stagedFrom(t, sources,
		"<!-- netdoc-output -->\n*\"The neighbour file is implementation, not something netdoc says.\"*\n")
	if err == nil {
		t.Fatal("a Go file nobody named was pulled into the quotation corpus")
	}
	if !strings.Contains(err.Error(), "netdoc prints no such text") {
		t.Errorf("error %q does not explain that the program prints no such text", err)
	}
}

// Two source files can legitimately hold the same sentence, and one of them can
// hold a longer sentence the other's is a prefix of. Neither makes a quotation
// ambiguous: it either occurs in some program string or it does not.
func TestASentenceSeveralSourcesShareStaysVerified(t *testing.T) {
	const shared = "Behind a captive portal: traffic is intercepted until you sign in to the network."
	sources := corpusSources()
	sources[helpFile] = "package main\n\nconst notice = \"" + shared + "\"\n"
	sources[grammarFile] = "package diagnostic\n\nconst hint = \"" + shared + " Sign in, then run netdoc again.\"\n"
	if err := stagedFrom(t, sources, "<!-- netdoc-output -->\n*\""+shared+"\"*\n"); err != nil {
		t.Fatalf("a sentence three sources share was rejected: %v", err)
	}
}

// A failure has to read the same way every time it is produced, or a
// documentation build turns into a coin toss over which sentence it blames.
func TestTheReportedDriftIsStableAcrossRuns(t *testing.T) {
	const shared = "Behind a captive portal: traffic is intercepted until you sign in to the network."
	sources := corpusSources()
	// The same near-miss in two files, so a tie has to be broken by something
	// fixed rather than by map or directory order.
	sources[helpFile] = "package main\n\nconst notice = \"" + shared + "\"\n"
	first := ""
	for i := range 5 {
		err := stagedFrom(t, sources,
			"<!-- netdoc-output -->\n*\"Behind a captive portal: traffic is intercepted until you log in.\"*\n")
		if err == nil {
			t.Fatal("a stale quotation was published")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("run %d reported\n%s\nbut the first run reported\n%s", i, err, first)
		}
	}
}

// The corpus is exactly the files programText names, in the order it names
// them, so the same repository always produces the same corpus.
func TestTheCorpusIsTheNamedFilesInTheOrderTheyAreNamed(t *testing.T) {
	fixture(t)
	first, err := programStrings(programText)
	if err != nil {
		t.Fatal(err)
	}
	second, err := programStrings(programText)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first, second) {
		t.Fatal("two reads of the same sources produced different corpora")
	}
	var order []string
	for _, s := range first {
		if len(order) == 0 || order[len(order)-1] != s.file {
			order = append(order, s.file)
		}
	}
	if !slices.Equal(order, programText) {
		t.Errorf("corpus covers %v, want exactly %v in that order", order, programText)
	}
}

// A source file that is present but says nothing means the checkout is wrong or
// the file moved, and every quotation resolving there would silently stop being
// checked. Failing names the file rather than the symptom.
func TestANamedSourceWithNoStringsFailsTheBuild(t *testing.T) {
	sources := corpusSources()
	sources[helpFile] = "package main\n\nfunc main() {}\n"
	err := stagedFrom(t, sources, "<!-- netdoc-output -->\n*\"…but the TLS handshake fails.\"*\n")
	if err == nil {
		t.Fatal("the build ran with a source file contributing nothing to the corpus")
	}
	if !strings.Contains(err.Error(), helpFile+": no program strings") {
		t.Errorf("error %q does not name the source file that came up empty", err)
	}
	// Emptying the list itself is the same removal, one level up.
	if _, err := programStrings(nil); err == nil {
		t.Error("a corpus of no source files was accepted; nothing would be verified against it")
	}
}

// The repository's own docs/ are the other half of the same site, and they
// quote netdoc as freely as the wiki does. The marker has to mean the same
// thing on both halves, or half the site drifts unwatched.
//
// stagedDocsPage puts the quotation on a repository doc instead, leaving the
// wiki and the program sources as the fixture wrote them, and overwrites the
// named sources on top of that.
func stagedDocsPage(t *testing.T, page string, sources map[string]string) error {
	t.Helper()
	docs, wiki, shell := fixture(t)
	for rel, body := range sources {
		if err := os.WriteFile(filepath.FromSlash(rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture's own docs page links here, so the heading has to stay.
	if err := os.WriteFile(filepath.Join(docs, "scenarios.md"), []byte("# Scenarios\n\n## Authoring\n\n"+page), 0o600); err != nil {
		t.Fatal(err)
	}
	return build("/network-doctor", shell, docs, wiki, "assets", filepath.Join(t.TempDir(), "out"))
}

// The Fix line under a failed row is netdoc's own words as much as a diagnosis
// is, so a doc quoting one is verified rather than trusted.
func TestARepositoryDocQuotationIsVerified(t *testing.T) {
	const hint = "name resolution failing: check /etc/resolv.conf / DNS"
	if err := stagedDocsPage(t, "<!-- netdoc-output -->\n**\""+hint+"\"**\n", nil); err != nil {
		t.Fatalf("the build rejected a fix hint netdoc does print: %v", err)
	}
}

// Each case is a way a repository doc could come apart from the program, the
// same ways the wiki can.
func TestRepositoryDocQuotationsThatNoLongerMatchFailTheBuild(t *testing.T) {
	for _, tc := range []struct{ name, page, want string }{
		{
			name: "a fix hint the program stopped printing",
			page: "<!-- netdoc-output -->\n**\"name resolution failing: check /etc/hosts / DNS\"**\n",
			want: "netdoc prints no such text",
		},
		{
			name: "a diagnosis quoted without the marker",
			page: "*\"…is unreachable though DNS and the general internet work.\"*\n",
			want: "carries no <!-- netdoc-output --> marker",
		},
		{
			name: "a marker that claims nothing",
			page: "<!-- netdoc-output -->\n\nUnrelated prose, and then a quotation of *\"something else entirely\"*.\n",
			want: "marks nothing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := stagedDocsPage(t, tc.page, nil)
			if err == nil {
				t.Fatal("the build published a docs quotation the program does not back")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), filepath.Join("docs", "scenarios.md")+":") {
				t.Errorf("error %q does not name the docs page and line that failed", err)
			}
		})
	}
}

// Rewording a fix hint is a documentation change: the doc that quotes it has to
// stop verifying until it is updated too.
func TestDriftInAFixHintFailsTheBuild(t *testing.T) {
	const hint = "name resolution failing: check /etc/resolv.conf / DNS"
	err := stagedDocsPage(t, "<!-- netdoc-output -->\n**\""+hint+"\"**\n", map[string]string{
		fixHintsFile: "package diagnostic\n\nfunc dnsFix() string { return \"DNS is broken, good luck\" }\n",
	})
	if err == nil {
		t.Fatal("the build published a quotation of a fix hint the program no longer prints")
	}
	if !strings.Contains(err.Error(), "netdoc prints no such text") {
		t.Errorf("error %q does not explain that the program prints no such text", err)
	}
}
