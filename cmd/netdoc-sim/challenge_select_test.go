package main

import (
	"io"
	"strings"
	"testing"
)

func parseChallengeArgs(t *testing.T, args ...string) (*challengeFlags, error) {
	t.Helper()
	f := newChallengeFlags(io.Discard)
	return f, f.parse(args)
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
