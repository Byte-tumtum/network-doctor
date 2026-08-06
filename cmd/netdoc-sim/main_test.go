package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The launcher resolves the netdoc binary in the user's own working directory
// and $PATH, then hands the director a command line built from what it parsed.
// These tests pin that hand-off: the director must receive the resolved
// absolute path and nothing that could override it, with every other flag and
// the scenario reference arriving unchanged.

// fakeNetdoc drops an executable named netdoc in a fresh directory and makes it
// the working directory, so a relative -netdoc has something real to resolve.
func fakeNetdoc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "netdoc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// t.TempDir can sit behind a symlink; compare against what Abs will produce.
	abs, err := filepath.Abs("netdoc")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// forward runs the launcher's half: parse the user's argv, resolve the binary,
// build the director's command line.
func forward(t *testing.T, userArgs ...string) []string {
	t.Helper()
	f := newRunFlags(io.Discard)
	ref, err := f.parse(userArgs)
	if err != nil {
		t.Fatalf("parse %v: %v", userArgs, err)
	}
	path, err := findNetdoc(*f.netdoc, filepath.Join(t.TempDir(), "netdoc-sim"))
	if err != nil {
		t.Fatalf("findNetdoc %q: %v", *f.netdoc, err)
	}
	return directorArgv(f, ref, path)
}

// received runs the director's half on what it was handed.
func received(t *testing.T, argv []string) (*runFlags, string) {
	t.Helper()
	if argv[0] != directorCommand {
		t.Fatalf("argv[0] = %q, want %q", argv[0], directorCommand)
	}
	f := newRunFlags(io.Discard)
	ref, err := f.parse(argv[1:])
	if err != nil {
		t.Fatalf("director could not parse %v: %v", argv, err)
	}
	return f, ref
}

func TestRelativeNetdocReachesDirectorResolved(t *testing.T) {
	abs := fakeNetdoc(t)
	f, ref := received(t, forward(t, "healthy", "-netdoc", "./netdoc"))
	if *f.netdoc != abs {
		t.Errorf("director got -netdoc %q, want the resolved %q", *f.netdoc, abs)
	}
	if ref != "healthy" {
		t.Errorf("scenario = %q, want healthy", ref)
	}
}

func TestNetdocEqualsFormReachesDirectorResolved(t *testing.T) {
	abs := fakeNetdoc(t)
	f, _ := received(t, forward(t, "healthy", "-netdoc=./netdoc"))
	if *f.netdoc != abs {
		t.Errorf("director got -netdoc %q, want the resolved %q", *f.netdoc, abs)
	}
	// The double-dash spelling of the same flag, which the flag package accepts.
	f2, _ := received(t, forward(t, "healthy", "--netdoc=./netdoc"))
	if *f2.netdoc != abs {
		t.Errorf("director got --netdoc %q, want the resolved %q", *f2.netdoc, abs)
	}
}

// TestDirectorCannotBeGivenTwoNetdocs is the regression: forwarding the user's
// argv verbatim left a second -netdoc on the line, and the flag package takes
// the last one — so the unresolved spelling won and the launcher's resolution
// was dead code.
func TestDirectorCannotBeGivenTwoNetdocs(t *testing.T) {
	abs := fakeNetdoc(t)
	argv := forward(t, "healthy", "-netdoc", "./netdoc")
	if n := slices.IndexFunc(argv[1:], func(s string) bool { return s == "-netdoc" }); n < 0 {
		t.Fatalf("no -netdoc in %v", argv)
	}
	occurrences := 0
	for _, a := range argv {
		if a == "-netdoc" || a == "--netdoc" || a == "-netdoc="+abs || a == "--netdoc="+abs {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Errorf("-netdoc appears %d times in %v, want exactly 1", occurrences, argv)
	}
	f, _ := received(t, argv)
	if *f.netdoc != abs {
		t.Errorf("director got %q, want %q", *f.netdoc, abs)
	}
}

func TestOtherFlagsAndScenarioSurviveForwarding(t *testing.T) {
	abs := fakeNetdoc(t)
	f, ref := received(t, forward(t,
		"-json", "-keep", "-v", "-timeout", "9s", "-repeat", "3", "./my-scenario.yaml", "-netdoc", "./netdoc"))

	if ref != "./my-scenario.yaml" {
		t.Errorf("scenario = %q, want ./my-scenario.yaml", ref)
	}
	if *f.netdoc != abs {
		t.Errorf("netdoc = %q, want %q", *f.netdoc, abs)
	}
	if !*f.json || !*f.keep || !*f.verbose {
		t.Errorf("json=%t keep=%t v=%t, want all true", *f.json, *f.keep, *f.verbose)
	}
	if *f.dry {
		t.Error("dry-run was not asked for and must not arrive set")
	}
	if *f.timeout != 9*time.Second || *f.repeat != 3 {
		t.Errorf("timeout=%s repeat=%d, want 9s/3", *f.timeout, *f.repeat)
	}
}

// TestDoubleDashIsHandled covers the terminator the flag package honours. A
// scenario after "--" is still a scenario, and the director's own line puts the
// reference behind one so a path beginning with a dash cannot be read as a flag.
func TestDoubleDashIsHandled(t *testing.T) {
	abs := fakeNetdoc(t)
	f, ref := received(t, forward(t, "-netdoc", "./netdoc", "-json", "--", "healthy"))
	if ref != "healthy" || *f.netdoc != abs || !*f.json {
		t.Errorf("ref=%q netdoc=%q json=%t", ref, *f.netdoc, *f.json)
	}

	argv := forward(t, "-netdoc", "./netdoc", "--", "-dash-leading.yaml")
	if argv[len(argv)-2] != "--" {
		t.Errorf("the scenario must be forwarded behind a terminator: %v", argv)
	}
	f2, ref2 := received(t, argv)
	if ref2 != "-dash-leading.yaml" {
		t.Errorf("scenario = %q, want -dash-leading.yaml", ref2)
	}
	if *f2.netdoc != abs {
		t.Errorf("netdoc = %q, want %q", *f2.netdoc, abs)
	}
}
