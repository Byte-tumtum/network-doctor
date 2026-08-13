// Guard for the repository's deterministic-test boundary: tests that run in
// the ordinary suite must not touch the host network. Real sockets, real DNS,
// and real interface enumeration belong in files gated behind an opt-in build
// tag, where `go test ./...` never builds them.

package main

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// optInTags are the build tags this repository's gate passes with -tags, and
// so the only tags that keep a file out of `go test ./...`. Every other
// constraint — a GOOS tag, `unix`, or a negation like `!integration` — still
// builds in the ordinary suite and stays in scope here. An unknown tag is
// treated as in scope, so a new opt-in tag trips this guard rather than
// silently opening a hole in it.
var optInTags = map[string]bool{
	"integration":       true,
	"netns_integration": true,
	"container":         true,
}

// hostCalls names the calls that reach the machine running the test. It is
// deliberately not an exhaustive census of package net: it covers listening,
// dialing, resolving, and interface enumeration, which is the whole surface
// the deterministic-test rule is about. Selector matching is exact, so sibling
// identifiers that only look similar (net.Listener as a parameter type,
// httptest.NewRequest, which builds a request in memory and dials nothing) are
// not swept in.
var hostCalls = []struct{ pkg, name string }{
	{"net", "Listen"}, {"net", "ListenPacket"}, {"net", "ListenTCP"}, {"net", "ListenUDP"},
	{"tls", "Listen"},
	{"httptest", "NewServer"}, {"httptest", "NewUnstartedServer"}, {"httptest", "NewTLSServer"},
	{"net", "Dial"}, {"net", "DialTimeout"}, {"tls", "Dial"},
	{"http", "Get"}, {"http", "Head"}, {"http", "Post"}, {"http", "PostForm"},
	{"http", "DefaultClient"}, {"http", "DefaultTransport"},
	{"net", "LookupHost"}, {"net", "LookupIP"}, {"net", "LookupAddr"}, {"net", "DefaultResolver"},
	{"net", "Interfaces"}, {"net", "InterfaceAddrs"}, {"net", "InterfaceByName"},
}

// hostTypes reach the host only once one is built, so these are flagged on
// construction — `net.Dialer{...}`, `new(net.Dialer)`, `var d net.Dialer`, the
// zero value being usable — and not where the type is merely named, as a
// parameter or a return type. A real dialer handed to a netops field is how the
// old HTTP/2 test dialed a real port while no call in it read as networking.
var hostTypes = []struct{ pkg, name string }{
	{"net", "Dialer"},
	{"net", "Resolver"},
}

// defaultOps is the one *netops wired to the real net.Dialer, real resolver,
// and real net.Interfaces, and probes reach the host through its function
// fields — so a call through it has no other textual sign of host access at
// the call site. That is exactly how TestIfaceForIPUnknown came to read the
// machine's own interface list. Only calls are flagged: naming a field to
// assert the production wiring is set touches nothing.
const hostOps = "defaultOps"

func TestOrdinaryTestsStayOffTheHost(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("this guard must run from the repository root: %v", err)
	}
	self := filepath.Join(root, "hostdeps_test.go") // names every banned call as data

	var scanned int
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The same directories the go tool itself ignores, so a fixture
			// that is deliberately unparseable stays a fixture.
			if name := d.Name(); path != root && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || path == self {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if buildsOnlyWithOptInTag(f) {
			return nil
		}
		scanned++
		rel, _ := filepath.Rel(root, path) // both are absolute and path is under root
		// match reports sel against one of the lists above, by exact package
		// qualifier and name.
		match := func(sel *ast.SelectorExpr, list []struct{ pkg, name string }) {
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return
			}
			for _, h := range list {
				if x.Name == h.pkg && sel.Sel.Name == h.name {
					t.Errorf("%s:%d: %s.%s reaches the host from a test in the ordinary suite; stub the netops seam (see pipeNet in internal/diagnostic) or move the test behind an opt-in build tag",
						rel, fset.Position(sel.Pos()).Line, h.pkg, h.name)
				}
			}
		}
		// Walking the AST rather than the text keeps comments and string
		// literals from being read as calls.
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if sel, ok := node.Type.(*ast.SelectorExpr); ok {
					match(sel, hostTypes)
				}
			case *ast.ValueSpec:
				// `var d net.Dialer` builds one just as surely as a literal
				// does; a *net.Dialer declaration is nil and reaches nothing.
				if sel, ok := node.Type.(*ast.SelectorExpr); ok {
					match(sel, hostTypes)
				}
			case *ast.CallExpr:
				if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "new" && len(node.Args) == 1 {
					if sel, ok := node.Args[0].(*ast.SelectorExpr); ok {
						match(sel, hostTypes)
					}
				}
				// Only a call through defaultOps counts; reading a field to
				// assert the production wiring reaches nothing.
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == hostOps {
					t.Errorf("%s:%d: %s.%s reaches the host from a test in the ordinary suite; use a *netops literal holding only the fields the test needs, or move the test behind an opt-in build tag",
						rel, fset.Position(sel.Pos()).Line, hostOps, sel.Sel.Name)
				}
			case *ast.SelectorExpr:
				match(node, hostCalls)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// A guard that silently stops finding files is worse than no guard.
	if scanned < 10 {
		t.Errorf("scanned only %d in-scope test files; the walk is not reaching the tree", scanned)
	}
}

// buildsOnlyWithOptInTag reports whether the file's build constraint keeps it
// out of every ordinary build. It evaluates the constraint with each opt-in
// tag unset and every other tag set, so a file is exempt only when no ordinary
// `go test ./...` on any platform can build it.
func buildsOnlyWithOptInTag(f *ast.File) bool {
	for _, group := range f.Comments {
		for _, c := range group.List {
			if c.Pos() > f.Package { // constraints live above the package clause
				return false
			}
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue
			}
			if !expr.Eval(func(tag string) bool { return !optInTags[tag] }) {
				return true
			}
		}
	}
	return false
}
