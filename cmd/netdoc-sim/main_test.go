package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/simulation"
)

func TestRunDispatch(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	scenarios := strings.Join(simulation.LibraryNames(), "\n") + "\n"
	tests := []struct {
		name           string
		args           []string
		code           int
		stdout         string
		stdoutContains string
		stderr         string
	}{
		{name: "no arguments", code: exitUsage, stdoutContains: "Usage: netdoc-sim <command> [arguments]"},
		{name: "unknown command", args: []string{"bogus"}, code: exitUsage,
			stderr: "netdoc-sim: unknown command \"bogus\"\nrun 'netdoc-sim help' for usage\n"},
		{name: "validate missing scenario", args: []string{"validate"}, code: exitUsage,
			stderr: "netdoc-sim: validate takes one scenario\n"},
		{name: "validate extra scenario", args: []string{"validate", "healthy", "healthy"}, code: exitUsage,
			stderr: "netdoc-sim: validate takes one scenario\n"},
		{name: "validate scenario", args: []string{"validate", "healthy"}, code: exitOK,
			stdout: "ok: healthy-network — 3 node(s), 0 fault(s), 1 test(s), 6 expected check(s)\n"},
		{name: "inspect missing id", args: []string{"inspect"}, code: exitUsage,
			stderr: "netdoc-sim: inspect takes one simulation id\n"},
		{name: "cleanup all with id", args: []string{"cleanup", "-all", "123"}, code: exitUsage,
			stderr: "netdoc-sim: -all takes no simulation id\n"},
		{name: "scenarios", args: []string{"scenarios"}, code: exitOK, stdout: scenarios},
		{name: "list empty", args: []string{"list"}, code: exitOK,
			stdout: "no simulations are being kept alive\n"},
		{name: "cleanup all empty", args: []string{"cleanup", "-all"}, code: exitOK,
			stdout: "nothing to clean up\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Errorf("code = %d, want %d", code, tt.code)
			}
			if tt.stdoutContains != "" {
				if !strings.Contains(stdout.String(), tt.stdoutContains) {
					t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.stdoutContains)
				}
			} else if stdout.String() != tt.stdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.stdout)
			}
			if stderr.String() != tt.stderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.stderr)
			}
		})
	}
}

// The run launcher deliberately receives the subcommand name and removes it
// before parsing. Pin both the scenario and its trailing flag so another argv
// slice cannot silently shift or drop either one.
func TestRunDispatchKeepsArgvAligned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "healthy", "-repeat", "0"}, &stdout, &stderr)
	if code != exitUsage || stdout.Len() != 0 || stderr.String() != "netdoc-sim: -repeat must be at least 1\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestRunDispatchCapabilities(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"capabilities"}, &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Backend:", "Supported:", "A run will:", "It will not touch the host's interfaces"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	switch {
	case strings.Contains(out, "Supported: true") && code != exitOK:
		t.Errorf("supported host returned code %d", code)
	case strings.Contains(out, "Supported: false") && code != exitError:
		t.Errorf("unsupported host returned code %d", code)
	case !strings.Contains(out, "Supported: true") && !strings.Contains(out, "Supported: false"):
		t.Errorf("stdout has no supported status: %q", out)
	}
}

// The launcher resolves the netdoc binary in the user's own working directory
// and $PATH, then hands the director a command line built from what it parsed.
// These tests pin that hand-off: the director must receive the resolved
// absolute path and nothing that could override it, with every other flag and
// the scenario reference arriving unchanged.

// fakeNetdoc drops an executable named netdoc in a fresh directory and makes it
// the working directory, so a relative -netdoc has something real to resolve.
// Windows recognizes an executable only by its PATHEXT suffix, and LookPath
// appends one to an extensionless argument, so the file on disk needs .exe for
// the callers' plain "./netdoc" to resolve there.
func fakeNetdoc(t *testing.T) string {
	t.Helper()
	name := "netdoc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// t.TempDir can sit behind a symlink; compare against what Abs will produce.
	abs, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestHuntDirectorReceivesExactGenerationInputs(t *testing.T) {
	abs := fakeNetdoc(t)
	f := newHuntFlags(io.Discard)
	base, err := f.parse([]string{"dual-stack-healthy", "--seed", "-44", "--cases", "6", "--case", "17",
		"--max-faults", "3", "--fail-fast", "--json", "--netdoc", "./netdoc", "--timeout", "7s"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := findNetdoc(*f.netdoc, filepath.Join(t.TempDir(), "netdoc-sim"))
	if err != nil {
		t.Fatal(err)
	}
	argv := huntDirectorArgv(f, base, path)
	got := newHuntFlags(io.Discard)
	gotBase, err := got.parse(argv[1:])
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != huntDirectorCommand || gotBase != base || !got.seed.set || got.seed.v != -44 ||
		*got.cases != 6 || *got.caseNum != 17 || *got.maxFaults != 3 || !*got.failFast || !*got.json ||
		*got.netdoc != abs || *got.timeout != 7*time.Second {
		t.Fatalf("forwarded hunt = argv %v base %q seed %+v", argv, gotBase, got.seed)
	}
}

func TestHuntDryRunNeedsNoNetdocOrNamespaces(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"hunt", "healthy-routed-network", "--dry-run", "--json", "--seed", "12345", "--cases", "6"}, &stdout, &stderr)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("code = %d stderr = %q", code, stderr.String())
	}
	var result simulation.HuntResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.GeneratedCases != 6 || result.ExecutedCases != 0 || len(result.Cases) != 6 {
		t.Fatalf("result = %+v", result)
	}
	selected := result.Cases[3].Manifest
	stdout.Reset()
	code = run([]string{"hunt", "healthy-routed-network", "--dry-run", "--json", "--seed", "12345",
		"--case", stringInt(selected.Case)}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("direct code = %d stderr = %q", code, stderr.String())
	}
	var direct simulation.HuntResult
	if err := json.Unmarshal(stdout.Bytes(), &direct); err != nil {
		t.Fatal(err)
	}
	if len(direct.Cases) != 1 || direct.Cases[0].Manifest.CaseFingerprint != selected.CaseFingerprint ||
		direct.Cases[0].Manifest.CaseSeed != selected.CaseSeed {
		t.Fatalf("direct = %+v, selected = %+v", direct.Cases, selected)
	}
}

func stringInt(value int) string { return strconv.Itoa(value) }

func TestHuntUsageBoundsAndBases(t *testing.T) {
	for _, args := range [][]string{
		{"hunt", "broken-dns", "--dry-run", "--seed", "1"},
		{"hunt", "--dry-run", "--seed", "1", "--cases", "501"},
		{"hunt", "--dry-run", "--seed", "1", "--case", "-2"},
		{"hunt", "--dry-run", "--seed", "1", "--max-faults", "4"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("run(%v) = %d, stderr %q", args, code, stderr.String())
		}
	}
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

func TestCampaignDirectorReceivesExactSeedAndIteration(t *testing.T) {
	abs := fakeNetdoc(t)
	f := newCampaignFlags(io.Discard)
	ref, err := f.parse([]string{"unstable-connectivity", "--seed", "0", "--runs", "5", "--iteration", "37", "--fail-fast", "--json", "--netdoc", "./netdoc"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := findNetdoc(*f.netdoc, filepath.Join(t.TempDir(), "netdoc-sim"))
	if err != nil {
		t.Fatal(err)
	}
	argv := campaignDirectorArgv(f, ref, path)
	got := newCampaignFlags(io.Discard)
	gotRef, err := got.parse(argv[1:])
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != campaignDirectorCommand || gotRef != ref || !got.seed.set || got.seed.v != 0 ||
		*got.runs != 5 || *got.iteration != 37 || !*got.failFast || !*got.json || *got.netdoc != abs {
		t.Fatalf("forwarded campaign = argv %v ref %q seed %+v runs %d iteration %d", argv, gotRef, got.seed, *got.runs, *got.iteration)
	}
}
