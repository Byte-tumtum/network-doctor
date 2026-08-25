package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPackageLayering(t *testing.T) {
	// Internal dependencies must descend this order. UI, peer transport, and
	// simulation are peers, so none can depend on another, and so are the live
	// diagnosis and the snapshot comparison: comparison reads artifacts, never
	// probes. report and textsafe are dependency-free leaves. Checking every
	// direct edge also rules out a transitive path to the same or a higher layer.
	layers := map[string]int{
		"internal/textsafe":   0,
		"internal/report":     0,
		"internal/snapshot":   0,
		"internal/compare":    1,
		"internal/diagnostic": 1,
		"internal/peer":       2,
		"internal/ui":         2,
		"internal/simulation": 2,
	}
	const internalPrefix = "github.com/heymaikol/network-doctor/internal/"

	files := make(map[string]int)
	edges := make(map[string]bool)
	scanned := 0
	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "internal" && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		pkg := filepath.ToSlash(filepath.Dir(path))
		files[pkg]++
		scanned++
		sourceLayer, sourceKnown := layers[pkg]
		if !sourceKnown {
			t.Errorf("%s: package layering violation: %s has no architecture layer", path, pkg)
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(importPath, internalPrefix) {
				continue
			}
			dependency := "internal/" + strings.TrimPrefix(importPath, internalPrefix)
			edges[pkg+" -> "+dependency] = true
			dependencyLayer, dependencyKnown := layers[dependency]
			if !dependencyKnown {
				t.Errorf("%s: package layering violation: %s depends on unclassified %s", fset.Position(spec.Pos()), pkg, dependency)
				continue
			}
			if sourceKnown && dependencyLayer >= sourceLayer {
				t.Errorf("%s: package layering violation: %s depends on %s; rule: dependencies must point down ui/peer/simulation -> diagnostic/compare -> report/snapshot/textsafe", fset.Position(spec.Pos()), pkg, dependency)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal packages: %v", err)
	}
	for pkg := range layers {
		if files[pkg] == 0 {
			t.Errorf("scanned no production Go files in expected package %s", pkg)
		}
	}
	if scanned < 20 || len(edges) < 3 {
		t.Errorf("scan reached only %d production Go files and %d internal package edges; the guard is not reaching the repository", scanned, len(edges))
	}
}
