// Link guard for the documentation that ships in a normal checkout: README.md
// and docs/*.md. Every relative link there is a path GitHub resolves when a
// reader clicks it and nothing resolves at build time, so a renamed page or a
// reworded heading leaves a link only a reader discovers. cmd/docsite already
// checks the published site's links, but only after a wiki clone and a Jekyll
// build; this test needs neither, and it covers README.md, which the site does
// not publish at all.
//
// The supported syntax is inline links and images, "](target)", plus raw HTML
// href and src. Anchors come from ATX headings ("## Title"), slugged the way
// GitHub does: inline code spans, bold markers and link syntax are dropped, the
// text is lowercased, everything outside letters, digits, hyphen and underscore
// is removed, spaces become hyphens, and a repeated slug gains "-1", "-2".
//
// Anything else is out of contract and fails loudly rather than passing
// quietly: setext headings, closing "##" sequences, HTML anchor definitions,
// underscore emphasis or non-ASCII text in a heading, link titles, query
// strings, nested parentheses in a destination, root-relative destinations, and
// reference-style link definitions. A link written inside a code span is read
// as a link, for the same reason.

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docLine is a line of Markdown prose with its 1-based number, so a failure
// points at the line to edit.
type docLine struct {
	num  int
	text string
}

// docLink is one link destination as written in the source.
type docLink struct {
	line   int
	target string
}

var (
	// uriScheme matches the "https:" or "mailto:" prefix of a link that leaves
	// the repository.
	uriScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	// inlineLink reduces "[text](url)" in a heading to its text, which is what
	// GitHub slugs.
	inlineLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// htmlRef matches the raw HTML links GitHub renders alongside Markdown,
	// spelled as cmd/docsite's site verifier spells it.
	htmlRef = regexp.MustCompile(`(?:href|src)="([^"]+)"`)
	// refDef matches a reference-style link definition, which this guard
	// rejects rather than resolving.
	refDef = regexp.MustCompile(`^\[[^\]]+\]:\s`)
)

// markdownProse returns the lines outside fenced code blocks, and reports a
// fence left open at the end of the file. Shell comments in a fenced example
// are not headings and sample command lines are not links, so an unnoticed open
// fence would quietly stop the guard reading.
func markdownProse(src string) (lines []docLine, unclosedFence bool) {
	fence := ""
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "" && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")):
			fence = trimmed[:3]
		case fence != "" && strings.HasPrefix(trimmed, fence):
			fence = ""
		case fence == "":
			lines = append(lines, docLine{num: i + 1, text: line})
		}
	}
	return lines, fence != ""
}

// headingSlug is GitHub's anchor for a heading's text.
func headingSlug(text string) string {
	text = inlineLink.ReplaceAllString(text, "$1")
	text = strings.NewReplacer("`", "", "*", "").Replace(text)
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// markdownAnchors returns every anchor a document offers.
func markdownAnchors(src string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	lines, _ := markdownProse(src)
	for _, line := range lines {
		// Four spaces of indent is an unfenced code block, where a shell
		// comment would otherwise mint an anchor no reader can reach.
		if strings.HasPrefix(line.text, "    ") {
			continue
		}
		text := strings.TrimLeft(line.text, " ")
		hashes := len(text) - len(strings.TrimLeft(text, "#"))
		if hashes < 1 || hashes > 6 || !strings.HasPrefix(text[hashes:], " ") {
			continue
		}
		slug := headingSlug(text[hashes:])
		if slug == "" {
			continue
		}
		if n := seen[slug]; n > 0 {
			anchors[fmt.Sprintf("%s-%d", slug, n)] = true
		} else {
			anchors[slug] = true
		}
		seen[slug]++
	}
	return anchors
}

// markdownLinks returns every link and image destination on the given lines.
// Scanning for "](" rather than matching a whole link keeps nested forms such
// as a badge image wrapped in a link from hiding either destination.
func markdownLinks(lines []docLine) []docLink {
	var links []docLink
	for _, line := range lines {
		rest := line.text
		for {
			open := strings.Index(rest, "](")
			if open < 0 {
				break
			}
			rest = rest[open+2:]
			end := strings.IndexByte(rest, ')')
			if end < 0 {
				break
			}
			links = append(links, docLink{line: line.num, target: strings.TrimSpace(rest[:end])})
			rest = rest[end+1:]
		}
		for _, m := range htmlRef.FindAllStringSubmatch(line.text, -1) {
			links = append(links, docLink{line: line.num, target: m[1]})
		}
	}
	return links
}

// checkMarkdownLinks reports every repository-local link in files that names a
// missing file or a missing heading anchor. Paths are relative to root.
func checkMarkdownLinks(root string, files []string) []string {
	var problems []string
	for _, file := range files {
		// #nosec G304 -- file comes from the caller's fixed list of documentation paths.
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		lines, unclosedFence := markdownProse(string(data))
		if unclosedFence {
			problems = append(problems, fmt.Sprintf("%s: a fenced code block is never closed, so this guard stopped reading there", file))
		}
		for _, line := range lines {
			if refDef.MatchString(line.text) {
				problems = append(problems, fmt.Sprintf("%s:%d: reference-style link definitions are out of this guard's contract; write the link inline", file, line.num))
			}
		}
		for _, link := range markdownLinks(lines) {
			target := link.target
			if target == "" || strings.HasPrefix(target, "//") || uriScheme.MatchString(target) {
				continue
			}
			path, fragment, _ := strings.Cut(target, "#")
			if decoded, err := url.PathUnescape(fragment); err == nil {
				fragment = decoded
			}
			dest := file
			if path != "" {
				dest = filepath.ToSlash(filepath.Join(filepath.Dir(file), path))
				if strings.HasPrefix(dest, "../") {
					problems = append(problems, fmt.Sprintf("%s:%d: link %q escapes the repository",
						file, link.line, target))
					continue
				}
				if _, err := os.Stat(filepath.Join(root, dest)); err != nil {
					problems = append(problems, fmt.Sprintf("%s:%d: link %q points at %s, which does not exist",
						file, link.line, target, dest))
					continue
				}
			}
			if fragment == "" || !strings.HasSuffix(dest, ".md") {
				continue
			}
			// #nosec G304 -- dest is a link destination this guard just resolved inside the tree under test.
			destData, err := os.ReadFile(filepath.Join(root, dest))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s:%d: link %q: %v", file, link.line, target, err))
				continue
			}
			if !markdownAnchors(string(destData))[fragment] {
				problems = append(problems, fmt.Sprintf("%s:%d: link %q wants anchor %q, which no heading in %s produces",
					file, link.line, target, fragment, dest))
			}
		}
	}
	return problems
}

func TestDocumentationLinksResolve(t *testing.T) {
	docs, err := filepath.Glob("docs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal("no docs/*.md found, so this guard would pass by checking nothing")
	}
	files := []string{"README.md"}
	for _, doc := range docs {
		files = append(files, filepath.ToSlash(doc))
	}
	for _, problem := range checkMarkdownLinks(".", files) {
		t.Error(problem)
	}
}

func TestCheckMarkdownLinksCatchesBrokenLinksAndAnchors(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("LICENSE", "")
	write("docs/other.md", "# Other page\n\n## Deep dive\n\n## Deep dive\n")
	write("docs/page.md", strings.Join([]string{
		"# Page",
		"",
		"## Which netdoc gets run?",
		"",
		"### A `netdoc-sim` heading (parenthesised)",
		"",
		"[same file](#which-netdoc-gets-run)",
		"[code span heading](#a-netdoc-sim-heading-parenthesised)",
		"[cross file](other.md#deep-dive)",
		"[duplicate heading](other.md#deep-dive-1)",
		"[escaped](other.md#deep%2Ddive)",
		"[up a level](../LICENSE)",
		"[directory](../docs)",
		"[external](https://example.com/missing.md#nope)",
		"[mail](mailto:someone@example.com)",
		`<a href="https://example.com/">html external</a>`,
		"[missing file](gone.md)",
		"[missing anchor](other.md#no-such-heading)",
		"[missing same-file anchor](#not-here)",
		"[![badge](../LICENSE)](nested-gone.md)",
		`<img src="gone.png" alt="html local">`,
		"[out of tree](../../gone.md)",
		"[ref style]: other.md",
		"",
		"    # Indented code, not a heading",
		"",
		"[phantom anchor](#indented-code-not-a-heading)",
		"",
		"```sh",
		"# Fenced heading",
		"netdoc --help  # see [docs](fenced-gone.md)",
		"```",
	}, "\n"))
	write("docs/open-fence.md", "# Open\n\n```sh\nnetdoc\n")

	problems := checkMarkdownLinks(root, []string{"docs/page.md", "docs/open-fence.md"})
	got := strings.Join(problems, "\n")

	wantSubstrings := []string{
		`docs/page.md:17: link "gone.md" points at docs/gone.md, which does not exist`,
		`docs/page.md:20: link "nested-gone.md" points at docs/nested-gone.md, which does not exist`,
		`docs/page.md:21: link "gone.png" points at docs/gone.png, which does not exist`,
		`docs/page.md:18: link "other.md#no-such-heading" wants anchor "no-such-heading"`,
		`docs/page.md:19: link "#not-here" wants anchor "not-here"`,
		`docs/page.md:27: link "#indented-code-not-a-heading" wants anchor "indented-code-not-a-heading"`,
		`docs/page.md:22: link "../../gone.md" escapes the repository`,
		`docs/page.md:23: reference-style link definitions are out of this guard's contract`,
		`docs/open-fence.md: a fenced code block is never closed`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("checkMarkdownLinks missed %s\ngot:\n%s", want, got)
		}
	}
	if len(problems) != len(wantSubstrings) {
		t.Errorf("checkMarkdownLinks reported %d problems, want %d:\n%s", len(problems), len(wantSubstrings), got)
	}
}

func TestHeadingSlug(t *testing.T) {
	for _, tc := range []struct{ heading, want string }{
		{"Think you can beat Network Doctor?", "think-you-can-beat-network-doctor"},
		{"Network simulation (`netdoc-sim`)", "network-simulation-netdoc-sim"},
		{"Which netdoc gets run", "which-netdoc-gets-run"},
		{"V1 to V3, and why V4 chooses differently", "v1-to-v3-and-why-v4-chooses-differently"},
		{"Route tables, and telling absences apart", "route-tables-and-telling-absences-apart"},
		{"**Bold** and a [link](x.md)", "bold-and-a-link"},
		{"macOS and Linux (Homebrew)", "macos-and-linux-homebrew"},
		{"snake_case stays", "snake_case-stays"},
		{"repeated   spaces", "repeated---spaces"},
	} {
		if got := headingSlug(tc.heading); got != tc.want {
			t.Errorf("headingSlug(%q) = %q, want %q", tc.heading, got, tc.want)
		}
	}
}
