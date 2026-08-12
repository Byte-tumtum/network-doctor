package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/simulation"
)

func receivedChallenge(t *testing.T, argv []string) *challengeFlags {
	t.Helper()
	if argv[0] != challengeDirectorCommand {
		t.Fatalf("argv[0] = %q, want %q", argv[0], challengeDirectorCommand)
	}
	f := newChallengeFlags(io.Discard)
	if err := f.parse(argv[1:]); err != nil {
		t.Fatalf("the challenge director could not parse %v: %v", argv, err)
	}
	return f
}

// The challenge is resolved once, in the launcher, and the director is handed
// the id rather than the job of drawing one. That is what keeps the case the
// player is briefed on and the case netdoc is graded on the same case.
func TestChallengeLaunchResolvesTheIDTheDirectorRunsWith(t *testing.T) {
	abs := fakeNetdoc(t)
	stubBackends(t, true)
	directors := stubDirectors(t, &fakeDirectors{code: exitOK})
	var stdout, stderr bytes.Buffer
	args := []string{"challenge", "-netdoc", "./netdoc", "-difficulty", "hard", "-timeout", "9s"}
	for range 2 {
		if code := run(args, &stdout, &stderr); code != exitOK {
			t.Fatalf("code = %d, stderr = %q", code, stderr.String())
		}
	}
	if len(directors.calls) != 2 {
		t.Fatalf("directors = %+v, want two", directors.calls)
	}
	ids := make([]string, 2)
	for i, call := range directors.calls {
		f := receivedChallenge(t, call.argv)
		if *f.netdoc != abs || *f.timeout != 9*time.Second {
			t.Errorf("challenge got netdoc %q timeout %s", *f.netdoc, *f.timeout)
		}
		challenge, err := simulation.BuildChallenge(*f.id)
		if err != nil {
			t.Fatalf("the director was handed an id it cannot build: %v", err)
		}
		if challenge.Difficulty != "hard" {
			t.Errorf("asked for a hard challenge, director got a %s one", challenge.Difficulty)
		}
		ids[i] = *f.id
	}
	if ids[0] == ids[1] {
		t.Errorf("both challenges were %s; ids are not being drawn", ids[0])
	}
	for _, offered := range directors.stdin {
		if !offered {
			t.Error("a challenge director was not given a terminal to ask with")
		}
	}
}

// Every other command is automation and reads nothing from the terminal.
func TestOnlyTheChallengeDirectorGetsATerminal(t *testing.T) {
	for _, args := range [][]string{
		{"run", "healthy", "-netdoc", "./netdoc"},
		{"campaign", "unstable-connectivity", "-netdoc", "./netdoc"},
		{"hunt", "-netdoc", "./netdoc", "-seed", "1"},
	} {
		t.Run(args[0], func(t *testing.T) {
			fakeNetdoc(t)
			stubBackends(t, true)
			directors := stubDirectors(t, &fakeDirectors{code: exitOK})
			var stdout, stderr bytes.Buffer
			run(args, &stdout, &stderr)
			for _, offered := range directors.stdin {
				if offered {
					t.Errorf("%s was given a terminal", args[0])
				}
			}
		})
	}
}

func TestChallengeReplayHandsTheDirectorTheSameID(t *testing.T) {
	fakeNetdoc(t)
	stubBackends(t, true)
	directors := stubDirectors(t, &fakeDirectors{code: exitOK})
	var stdout, stderr bytes.Buffer
	// Positional and flag forms, bare and version-prefixed, are one request.
	for _, args := range [][]string{
		{"challenge", "-id", "8f42c1", "-netdoc", "./netdoc"},
		{"challenge", "8F42C1", "-netdoc", "./netdoc"},
		{"challenge", "v1-8f42c1", "-netdoc", "./netdoc"},
	} {
		if code := run(args, &stdout, &stderr); code != exitOK {
			t.Fatalf("%v: code = %d, stderr = %q", args, code, stderr.String())
		}
	}
	if len(directors.calls) != 3 {
		t.Fatalf("directors = %+v, want three", directors.calls)
	}
	for _, call := range directors.calls {
		if got := *receivedChallenge(t, call.argv).id; got != "V1-8F42C1" {
			t.Errorf("director got id %q, want the canonical V1-8F42C1", got)
		}
	}
}

func TestChallengeStopsBeforeTheDirector(t *testing.T) {
	runRejections(t, []dispatchCase{
		{name: "unknown id", supported: true, args: []string{"challenge", "-id", "nope", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "challenge id is 6 hex characters"},
		{name: "unresolvable id version", supported: true,
			args: []string{"challenge", "-id", "V9-8F42C1", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "not one this build can resolve"},
		{name: "unknown answer", supported: true, args: []string{"challenge", "-answer", "vibes", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "unknown answer"},
		{name: "two submissions", supported: true,
			args: []string{"challenge", "-answer", "dns_failure", "-give-up", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "two different submissions"},
		{name: "json without an answer", supported: true, args: []string{"challenge", "-json", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "needs -answer or -give-up"},
		{name: "id and difficulty", supported: true,
			args: []string{"challenge", "-id", "8F42C1", "-difficulty", "hard", "-netdoc", "./netdoc"},
			code: exitUsage, stderrHas: "has nothing to choose"},
		{name: "host cannot simulate", supported: false, args: []string{"challenge", "-netdoc", "./netdoc"},
			code: exitError, stderrHas: "stub backend: this host cannot simulate"},
		{name: "no netdoc anywhere", supported: true, args: []string{"challenge"}, code: exitUsage,
			stderrHas: "cannot find the netdoc binary"},
	})
}

// The usage text lists the answer vocabulary when a submission is rejected, so
// a scripted challenge does not have to guess at the names.
func TestChallengeRejectionListsTheAnswers(t *testing.T) {
	fakeNetdoc(t)
	stubBackends(t, true)
	stubDirectors(t, &fakeDirectors{})
	var stdout, stderr bytes.Buffer
	run([]string{"challenge", "-answer", "vibes", "-netdoc", "./netdoc"}, &stdout, &stderr)
	for _, name := range simulation.ChallengeAnswerNames() {
		if !strings.Contains(stderr.String(), name) {
			t.Fatalf("stderr does not offer %q: %s", name, stderr.String())
		}
	}
}

func TestUsageMentionsChallenge(t *testing.T) {
	var out bytes.Buffer
	usage(&out)
	for _, want := range []string{"challenge [id] [flags]", "-difficulty", "-give-up"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage does not mention %q", want)
		}
	}
}
