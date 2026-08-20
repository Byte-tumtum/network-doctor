package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The documentation explains what netdoc prints, so some of its sentences are
// netdoc's own sentences. Nothing compared them: the wiki is a git repository
// of its own with no Actions in it, and this repository has no copy of it to
// test against. Staging the site is the one moment both halves exist on the
// same disk, which is why the comparison lives here rather than in a test, and
// why docs/ is held to the same rule from the same place: one marker means the
// same thing on both halves of one site.
//
// A quotation opts in with a marker immediately before it:
//
//	<!-- netdoc-output -->
//	**"Online via the environment proxy: direct egress is blocked (proxy-only
//	network)."**
//
// The marked text then has to appear in the program's own strings. A `…` inside
// the quotation stands for text the quotation leaves out, either because the
// program interpolates a hostname there or because the sentence is longer than
// the point being made, so a quotation is checked as its pieces, in order,
// inside one program string. Runs of whitespace are collapsed on both sides, so
// neither Markdown's line wrapping nor the Go source's own layout is
// significant.
//
// The check runs in both directions. An unmarked quotation that is long enough
// to be program text and matches some of it fails too, because otherwise a
// verified quotation could be quietly demoted to prose by deleting one line.
const outputMarker = "<!-- netdoc-output -->"

// programText names every file a marked quotation may resolve in: the text
// netdoc puts in front of a person. diagnosis.go decides every sentence netdoc
// calls a diagnosis, the summaries and the verdict details; fixhints.go writes
// the Fix line a failed row shows and the JSON report carries; main.go writes
// the usage banner, the flag descriptions --help prints, and the errors the
// command exits with; target.go owns TargetForms, the target grammar block
// --help and the restart prompt render verbatim.
//
// It is a list of named files and not a walk of a package, because the marker
// has to keep meaning "netdoc prints this" rather than "some literal exists
// somewhere under internal", which would sweep in import paths, exec arguments,
// and wire-format checks. Adding a file here is a deliberate statement that its
// strings are netdoc's own words; the paths are repository-relative, as this
// command's link checking already is. Order is fixed so a failure reports the
// same nearest string on every run.
var programText = []string{
	"internal/diagnostic/diagnosis.go",
	"internal/diagnostic/fixhints.go",
	"internal/diagnostic/target.go",
	"main.go",
}

// unmarkedQuoteLen is how much quoted text has to match program text before the
// match is treated as a quotation rather than a coincidence.
//
// ponytail: a length threshold, not a parser. A short quoted phrase that
// happens to occur inside a program string goes unchecked; move the number if
// that ever stops being the right trade.
const unmarkedQuoteLen = 40

// checkQuotations fails the build when the documentation quotes program output
// that the program no longer prints. Every directory the site publishes pages
// from is checked, and they are checked together: the marker is a property of
// the site, so losing every one of them anywhere is the removal worth naming.
func checkQuotations(pageDirs []string, sourceFiles []string) error {
	corpus, err := programStrings(sourceFiles)
	if err != nil {
		return err
	}
	var problems []string
	marked := 0
	for _, dir := range pageDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			// #nosec G304 -- a Markdown file this walk just found in a documentation directory the command was pointed at.
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return err
			}
			for _, q := range quotations(string(data)) {
				where := fmt.Sprintf("%s:%d", filepath.Join(dir, e.Name()), q.line)
				switch {
				case q.marked:
					marked++
					if !quoteMatches(corpus, q.text) {
						problems = append(problems, fmt.Sprintf("%s: quotation %q is marked %s but netdoc prints no such text.\n      Closest program string: %s\n      Either update the quotation, or drop the marker if this is no longer program output.",
							where, q.text, outputMarker, nearest(corpus, q.text)))
					}
				case len(q.text) >= unmarkedQuoteLen && quoteMatches(corpus, q.text):
					problems = append(problems, fmt.Sprintf("%s: quotation %q is netdoc's own output but carries no %s marker immediately before it, so nothing would notice it going stale.",
						where, q.text, outputMarker))
				case q.orphanMarker:
					problems = append(problems, fmt.Sprintf("%s: %s marks nothing; it has to sit immediately before a \"quotation\", with nothing but emphasis in between.", where, outputMarker))
				}
			}
		}
	}
	if marked == 0 && len(problems) == 0 {
		return fmt.Errorf("%s: no %s marker anywhere in the documentation; the quotations that repeat netdoc's output are no longer verified", strings.Join(pageDirs, ", "), outputMarker)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%d documentation quotations no longer agree with the program:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return nil
}

type quotation struct {
	line         int
	text         string
	marked       bool
	orphanMarker bool
}

// quotations returns every double-quoted span of a Markdown page that is not
// inside a fenced code block, in the order they appear, each flagged with
// whether a marker claimed it. Fenced blocks are excluded because they hold
// shell commands and diagrams rather than sentences the program prints.
func quotations(page string) []quotation {
	var text strings.Builder
	var lineOf []int
	fenced := false
	for i, line := range strings.Split(strings.ReplaceAll(page, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for range len(line) + 1 {
			lineOf = append(lineOf, i+1)
		}
		text.WriteString(line)
		text.WriteString("\n")
	}
	body := text.String()

	var out []quotation
	pending := -1 // offset of a marker that has not claimed a quotation yet
	for i := 0; i < len(body); {
		if strings.HasPrefix(body[i:], outputMarker) {
			if pending >= 0 {
				out = append(out, quotation{line: lineOf[pending], orphanMarker: true})
			}
			pending = i
			i += len(outputMarker)
			continue
		}
		// An inline code span is a name being written down, not a sentence:
		// the marker appears in one on the wiki page that documents it.
		if body[i] == '`' {
			if j := strings.IndexByte(body[i+1:], '`'); j >= 0 {
				i += j + 2
				continue
			}
		}
		if body[i] != '"' {
			i++
			continue
		}
		end := strings.IndexByte(body[i+1:], '"')
		if end < 0 {
			break
		}
		end += i + 1
		claimed := pending >= 0 && adjacent(body[pending+len(outputMarker):i])
		if pending >= 0 && !claimed {
			out = append(out, quotation{line: lineOf[pending], orphanMarker: true})
		}
		out = append(out, quotation{
			line:   lineOf[i],
			text:   strings.Join(strings.Fields(body[i+1:end]), " "),
			marked: claimed,
		})
		pending = -1
		i = end + 1
	}
	if pending >= 0 {
		out = append(out, quotation{line: lineOf[pending], orphanMarker: true})
	}
	return out
}

// adjacent reports whether a marker and the quotation after it have nothing
// between them but line breaks and Markdown emphasis, which is what keeps a
// marker from drifting away from its quotation and silently claiming the next
// sentence that happens to have quotes in it.
func adjacent(gap string) bool {
	return strings.TrimLeft(gap, " \t\n*_`>") == ""
}

// A programString is one string a named source file builds, kept beside the
// file it came from so a failure can say which of them the quotation drifted
// from rather than leaving that to be hunted across the corpus.
type programString struct {
	file string
	text string
}

// quoteMatches reports whether a quotation's pieces occur, in order, inside one
// program string. Which of several equal matches wins is never asked, so a
// sentence two source files happen to share is matched, not made ambiguous.
func quoteMatches(corpus []programString, quote string) bool {
	pieces := strings.Split(quote, "…")
	for _, s := range corpus {
		rest, ok := s.text, true
		for _, piece := range pieces {
			i := strings.Index(rest, piece)
			if i < 0 {
				ok = false
				break
			}
			rest = rest[i+len(piece):]
		}
		if ok {
			return true
		}
	}
	return false
}

// nearest picks the program string that shares the longest opening run with a
// quotation, so a failure can show what the sentence has drifted from rather
// than only that it matched nothing, and names the file it lives in. Ties keep
// the first candidate in corpus order, which programStrings fixes, so the same
// drift reports the same sentence on every run.
func nearest(corpus []programString, quote string) string {
	quote = strings.TrimLeft(quote, "…")
	best, bestRun := programString{}, 0
	for _, s := range corpus {
		run := 0
		for run < len(quote) && strings.Contains(s.text, quote[:run+1]) {
			run++
		}
		if run > bestRun {
			best, bestRun = s, run
		}
	}
	if bestRun == 0 {
		return "none; no program string shares even its first character"
	}
	// Shown to a human, so an interpolation point reads as the `…` a quotation
	// would write there.
	return fmt.Sprintf("%s: %q", best.file, strings.ReplaceAll(best.text, interpolated, "…"))
}

// interpolated stands where a program string takes a runtime value: a hostname,
// a port, a measurement. Nothing quotable contains a NUL, so a quotation's
// pieces can never match across one, which is what stops an elision from
// pretending two unrelated sentences are one.
const interpolated = "\x00"

// programStrings returns every string the named source files build, in the
// order the files are named and the order they appear in each, so the corpus is
// the same on every run. Only the files named are read: a sibling in the same
// directory contributes nothing, which is what keeps the marker a statement
// about netdoc's own words. Literals joined by + are folded to the value they
// produce, so a sentence split over several source lines is one string here and
// rewrapping the Go source cannot break a wiki quotation. Runtime operands
// become interpolated, which is exactly where a quotation puts its `…`.
func programStrings(files []string) ([]programString, error) {
	var out []programString
	for _, file := range files {
		strs, err := fileStrings(file)
		if err != nil {
			return nil, err
		}
		if len(strs) == 0 {
			return nil, fmt.Errorf("%s: no program strings to check wiki quotations against; the source is empty or was not checked out", file)
		}
		for _, s := range strs {
			out = append(out, programString{file: file, text: s})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no program source files named; nothing would hold the wiki's quotations to netdoc's output")
	}
	return out, nil
}

// fileStrings is that extraction for one file, which is where the parsing and
// the folding happen; programStrings is the loop over the named files.
func fileStrings(file string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		b, isSum := e.(*ast.BinaryExpr)
		s, ok := constString(e)
		switch {
		case !ok && (!isSum || b.Op != token.ADD):
			return true
		case !ok:
			s = flatten(b)
		}
		// Collapsed the same way a quotation is, so the line breaks and the
		// column padding a Go source uses to lay a paragraph or a grammar
		// table out are not what a wiki page has to reproduce.
		if s = strings.Join(strings.Fields(s), " "); strings.Trim(s, interpolated) != "" {
			out = append(out, s)
		}
		// Its parts are covered by the value assembled here.
		return false
	})
	return out, nil
}

// flatten renders a + chain as the text it produces, with every runtime operand
// standing in as interpolated. Folding is what lets a failure quote the whole
// sentence a quotation drifted from: without it the nearest program string to a
// summary about a host is the fragment before the hostname.
func flatten(e ast.Expr) string {
	if s, ok := constString(e); ok {
		return s
	}
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
		return flatten(b.X) + flatten(b.Y)
	}
	return interpolated
}

// constString unquotes a string literal. A + of two literals is left to flatten,
// which folds it by recursion.
func constString(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			return s, err == nil
		}
	case *ast.ParenExpr:
		return constString(e.X)
	}
	return "", false
}
