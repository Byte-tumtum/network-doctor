package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/heymaikol/network-doctor/internal/simulation"
)

func parseChallengeArgs(t *testing.T, args ...string) (*challengeFlags, error) {
	t.Helper()
	f := newChallengeFlags(io.Discard)
	return f, f.parse(args)
}

// A starter pack is a way of drawing one, so it conflicts with the other ways of
// drawing one and is checked for existence before a network is built.
func TestStarterFlagIsValidatedAndDrawsFromThePack(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"an id", "has nothing to draw"},
		{"a difficulty", "has nothing to choose"},
	} {
		args := []string{"-starter", "routing", "-id", "V3-8F42C1"}
		if tt.name == "a difficulty" {
			args = []string{"-starter", "routing", "-difficulty", "hard"}
		}
		if _, err := parseChallengeArgs(t, args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v with %s was refused with %v", args, tt.name, err)
		}
	}
	if _, err := parseChallengeArgs(t, "-starter", "no-such-pack"); err == nil ||
		!strings.Contains(err.Error(), "unknown starter pack") {
		t.Fatalf("an unknown pack was accepted or misreported: %v", err)
	}
	f, err := parseChallengeArgs(t, "-starter", "TLS")
	if err != nil {
		t.Fatalf("a pack named in capitals was refused: %v", err)
	}
	pack, ok := simulation.StarterPackByID("tls")
	if !ok {
		t.Fatal("no tls pack")
	}
	for range 20 {
		challenge, err := resolveChallenge(f)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, id := range pack.Challenges {
			found = found || id == challenge.ID
		}
		if !found {
			t.Fatalf("-starter tls drew %s, which is not in %v", challenge.ID, pack.Challenges)
		}
	}
}

// Discovery has to work without reading the source or the README, and it prints
// the ids so a pack can be worked through in order rather than only drawn from.
func TestStartersCommandListsPacksAndTheirChallenges(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"starters"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	listing := stdout.String()
	for _, pack := range simulation.StarterPacks() {
		if !strings.Contains(listing, pack.ID) || !strings.Contains(listing, pack.Name) ||
			!strings.Contains(listing, pack.Description) {
			t.Errorf("the listing does not offer pack %s:\n%s", pack.ID, listing)
		}
	}
	if !strings.Contains(listing, "-starter <pack>") {
		t.Errorf("the listing does not say how to play one:\n%s", listing)
	}

	stdout.Reset()
	if code := run([]string{"starters", "routing"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	pack, _ := simulation.StarterPackByID("routing")
	for _, id := range pack.Challenges {
		if !strings.Contains(stdout.String(), id) {
			t.Errorf("the routing pack listing omits %s:\n%s", id, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"starters", "no-such-pack"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("an unknown pack exited %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown starter pack") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// -answer takes the diagnosis by name now, not only by internal identifier. The
// point of the change is that nobody has to read the source to submit an answer
// from a script, so the accepted forms are pinned.
func TestAnswerFlagAcceptsNamesAndRejectsResemblance(t *testing.T) {
	for _, answer := range []string{"dns_failure", "dns", "DNS resolution", "tcp-port-blocked",
		"TCP port blocked", "refused", "ok"} {
		if _, err := parseChallengeArgs(t, "-answer", answer, "-json"); err != nil {
			t.Errorf("-answer %q was refused: %v", answer, err)
		}
	}
	for _, answer := range []string{"tcp", "dns failure!", "connection", "", "1"} {
		f := newChallengeFlags(io.Discard)
		err := f.parse([]string{"-answer", answer, "-json"})
		if answer == "" {
			// An empty -answer is not a submission, and -json says there is nobody to
			// ask, so the flags contradict each other rather than naming a bad answer.
			if err == nil || !strings.Contains(err.Error(), "needs -answer or -give-up") {
				t.Errorf("-answer \"\" -json was refused with %v", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "unknown answer") {
			t.Errorf("-answer %q was accepted or misreported: %v", answer, err)
		}
	}
}
