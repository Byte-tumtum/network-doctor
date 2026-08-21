package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The README's hero GIF is recorded by .github/workflows/demo.yml from
// hero.tape. These tests hold the parts of that arrangement that break
// silently: a recording is still produced when the tape has drifted off the
// program, and a workflow that quietly went back to installing VHS by hand, or
// grew a write permission, looks exactly like one that did not.

func demoWorkflow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(".github/workflows/demo.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// demoSteps is the workflow with its commentary removed, for the checks that
// ask what the workflow does rather than what it says. The comments here
// explain why VHS is not installed by hand and why nothing is committed, so
// scanning the raw file for "ttyd" or "write" finds the explanation and calls
// it the offence.
func demoSteps(t *testing.T) string {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(demoWorkflow(t), "\n") {
		code, _, _ := strings.Cut(line, "#")
		if strings.TrimSpace(code) != "" {
			kept = append(kept, code)
		}
	}
	return strings.Join(kept, "\n")
}

func heroSteps(t *testing.T) string {
	t.Helper()
	tape, err := os.ReadFile("hero.tape")
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, line := range strings.Split(string(tape), "\n") {
		code, _, _ := strings.Cut(line, "#")
		if strings.TrimSpace(code) != "" {
			steps = append(steps, code)
		}
	}
	return strings.Join(steps, "\n")
}

// VHS, ttyd and ffmpeg are the action's job. Installing any of them here would
// mean a second, unpinned toolchain deciding what the recording looks like.
func TestDemoWorkflowRecordsThroughTheOfficialVHSAction(t *testing.T) {
	workflow := demoWorkflow(t)

	if !strings.Contains(workflow, "uses: charmbracelet/vhs-action@") {
		t.Error("demo.yml does not use charmbracelet/vhs-action; nothing records the tape through the official action")
	}
	if !strings.Contains(workflow, "path: hero.tape") {
		t.Error("demo.yml does not hand hero.tape to the action")
	}
	// The tape names its own output, and the workflow checks that file. If
	// the tape's Output line moves, the check is watching a stale path.
	tape, err := os.ReadFile("hero.tape")
	if err != nil {
		t.Fatal(err)
	}
	output := regexp.MustCompile(`(?m)^Output\s+(\S+)`).FindSubmatch(tape)
	if output == nil {
		t.Fatal("hero.tape has no Output line")
	}
	if !strings.Contains(workflow, string(output[1])) {
		t.Errorf("hero.tape records to %q, which demo.yml never verifies or uploads", output[1])
	}

	// The dimensions the workflow asserts are copied out of the tape, so a
	// tape resize has to be made deliberately in both places rather than
	// turning the check red on the next unrelated commit.
	for _, set := range []string{"Width", "Height"} {
		m := regexp.MustCompile(`(?m)^Set\s+` + set + `\s+(\d+)`).FindSubmatch(tape)
		if m == nil {
			t.Fatalf("hero.tape does not Set %s", set)
		}
		if !strings.Contains(workflow, `"`+string(m[1])+`"`) {
			t.Errorf("hero.tape sets %s %s, which demo.yml does not check for", set, m[1])
		}
	}
}

// demoAptPackages is every package the workflow installs with apt, in the order
// the workflow names them. Flags are not packages.
func demoAptPackages(t *testing.T) []string {
	t.Helper()
	var pkgs []string
	for _, line := range strings.Split(demoSteps(t), "\n") {
		_, args, ok := strings.Cut(line, "apt-get install")
		if !ok {
			continue
		}
		for _, field := range strings.Fields(args) {
			if strings.HasPrefix(field, "-") {
				continue
			}
			pkgs = append(pkgs, field)
		}
	}
	return pkgs
}

// The recorded screen greys out every Dig deeper action whose binary LookPath
// cannot find, so these two are fixtures of the demo rather than build
// dependencies. The first hosted recording shipped a hero GIF advertising
// `trace the path` and `port scan` as unavailable, because the runner image
// carries neither binary while an ordinary installation does. Pinned as an
// exact set, so a later cleanup cannot quietly drop one and grey the actions
// out again, and so the recorder's own dependencies cannot ride along beside
// them.
func TestDemoWorkflowInstallsTheToolsTheRecordedScreenOffers(t *testing.T) {
	want := []string{"nmap", "traceroute"}
	got := demoAptPackages(t)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("demo.yml apt-installs %q, want exactly %q: the recorded screen offers both and greys out whichever binary is missing", got, want)
	}
}

func TestHeroRecordingRunsNetworkDoctorDirectly(t *testing.T) {
	recording := heroSteps(t)
	if !strings.Contains(recording, `Type "netdoc printer.invalid"`) {
		t.Error("hero.tape does not visibly invoke netdoc printer.invalid")
	}
	for _, forbidden := range []string{"netdoc-sim", "nsenter", "netdoc()", `$PWD/netdoc`, "has no A/AAAA", "Fix:", "Next:"} {
		if strings.Contains(recording, forbidden) {
			t.Errorf("hero.tape executable steps contain %q; the recording must run netdoc directly without injected results", forbidden)
		}
	}
}

// VHS gives the recorded command a real xterm-256color pseudo-terminal, but
// inherits NO_COLOR locally and CI on GitHub-hosted runners. termenv treats
// either as an instruction to return its uncolored profile before inspecting
// the TTY, so the tape clears only those inherited policy flags.
func TestHeroRecordingUsesItsColorCapableTerminal(t *testing.T) {
	recording := heroSteps(t)
	for _, env := range []string{`Env NO_COLOR ""`, `Env CI ""`} {
		if !strings.Contains(recording, env) {
			t.Errorf("hero.tape lacks %q; termenv will disable color in a supported recording environment", env)
		}
	}
	for _, forced := range []string{"CLICOLOR_FORCE", "FORCE_COLOR"} {
		if strings.Contains(recording, forced) {
			t.Errorf("hero.tape contains %q; advertise the pseudo-terminal's real capability instead of forcing color", forced)
		}
	}
}

// The recorder belongs to the action, which installs the pinned VHS binary
// along with ttyd, ffmpeg and the fonts it renders with. A second copy
// installed here would be unpinned, and whichever one won the PATH would decide
// what the recording looks like.
func TestDemoWorkflowLeavesTheRecorderToTheAction(t *testing.T) {
	for _, pkg := range demoAptPackages(t) {
		if slices.Contains([]string{"vhs", "ttyd", "ffmpeg", "fontconfig"}, pkg) {
			t.Errorf("demo.yml apt-installs %q, which charmbracelet/vhs-action already provides", pkg)
		}
		if strings.HasPrefix(pkg, "fonts-") {
			t.Errorf("demo.yml apt-installs the font package %q; the action installs the fonts VHS renders with", pkg)
		}
	}
	// Not every install is an apt install: what this workflow replaced fetched
	// a release archive and piped an installer into a shell.
	steps := demoSteps(t)
	for _, vector := range []string{"curl -", "wget ", "install-fonts"} {
		if strings.Contains(steps, vector) {
			t.Errorf("demo.yml uses %q; VHS, ttyd, ffmpeg and the fonts are the action's to install", vector)
		}
	}
}

// Same supply-chain rule the other workflows follow, plus one of its own: the
// VHS binary is a version too, and `latest` would let an upstream release
// change the recording with nothing in this repository to point at.
func TestDemoWorkflowPinsItsActionsAndItsVHS(t *testing.T) {
	workflow := demoWorkflow(t)

	uses := regexp.MustCompile(`uses: (\S+)@(\S+)`).FindAllStringSubmatch(workflow, -1)
	if len(uses) < 3 {
		t.Fatalf("found %d action uses in demo.yml; the test is not reaching the workflow", len(uses))
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, m := range uses {
		if !sha.MatchString(m[2]) {
			t.Errorf("demo.yml uses %q, which is not pinned to a commit SHA", m[0])
		}
	}

	version := regexp.MustCompile(`(?m)^\s+version: (\S+)`).FindStringSubmatch(workflow)
	if version == nil {
		t.Fatal("demo.yml never pins a VHS version, so the action installs `latest`")
	}
	if version[1] == "latest" {
		t.Error("demo.yml pins VHS to `latest`; an upstream release would change the recording on its own")
	}
}

// Nothing here writes: the manually requested recording is uploaded as an
// artifact for review rather than committed. This also prevents a recording
// run from triggering itself.
func TestDemoWorkflowIsReadOnlyAndManual(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/demo.yml")
	if err != nil {
		t.Fatal(err)
	}
	// yaml.Node, because `workflow_dispatch:` with no inputs decodes to a
	// null scalar, which is indistinguishable from absent in any other type.
	var workflow struct {
		On          map[string]yaml.Node `yaml:"on"`
		Permissions map[string]string    `yaml:"permissions"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("demo.yml is not valid YAML: %v", err)
	}

	if len(workflow.On) != 1 {
		t.Errorf("demo.yml triggers on %v; live network recording must be manual only", workflow.On)
	}
	if _, ok := workflow.On["workflow_dispatch"]; !ok {
		t.Error("demo.yml cannot be triggered by hand")
	}
	if got := workflow.Permissions["contents"]; got != "read" {
		t.Errorf("demo.yml grants contents: %q, want read", got)
	}
	steps := demoSteps(t)
	if strings.Contains(steps, "write") {
		t.Error("demo.yml grants a write permission somewhere; recording a GIF needs none, and a fork's pull request never gets one")
	}
	// An auto-commit would push to main, which pushes to main are a trigger
	// for, which is a loop that runs until someone notices the bill.
	for _, needle := range []string{"git-auto-commit", "git push", "git commit"} {
		if strings.Contains(steps, needle) {
			t.Errorf("demo.yml contains %q; committing the recording from a workflow that runs on push to main triggers itself", needle)
		}
	}
}
