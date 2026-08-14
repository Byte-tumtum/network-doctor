package simulation

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed scenarios/*.yaml
var library embed.FS

var libraryScenarios sync.Map

// LibraryNames lists the built-in scenarios, by file stem.
func LibraryNames() []string {
	entries, err := fs.ReadDir(library, "scenarios")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// LibraryScenario loads a built-in scenario by name.
func LibraryScenario(name string) (*Scenario, error) {
	if cached, ok := libraryScenarios.Load(name); ok {
		return cloneScenario(cached.(*Scenario)), nil
	}
	blob, err := library.ReadFile(path.Join("scenarios", path.Base(name)+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("no built-in scenario %q (have: %s)", name, strings.Join(LibraryNames(), ", "))
	}
	s, err := ParseScenario(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("built-in scenario %s: %w", name, err)
	}
	libraryScenarios.Store(name, s)
	return cloneScenario(s), nil
}

// Load resolves a scenario reference: a built-in name, or a path to a YAML
// file. A reference containing a path separator or a .yaml suffix is always
// treated as a file, so a local scenario can never be shadowed by a built-in.
func Load(ref string) (*Scenario, error) {
	if ref == "" {
		return nil, errors.New("no scenario given")
	}
	if strings.ContainsAny(ref, `/\`) || strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") {
		return LoadScenario(ref)
	}
	return LibraryScenario(ref)
}
