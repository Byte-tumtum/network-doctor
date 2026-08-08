//go:build linux

package simulation

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAwaitReadsHolderLogsSafely drives the setup-failure path: a holder that
// answers the wrong line is still running and still writing to stderr while
// await reads that stderr to explain itself. The assertions are deterministic;
// the concurrency is the point, so run it under -race, where dropping safeLog's
// lock reports a data race on a good fraction of runs.
func TestAwaitReadsHolderLogsSafely(t *testing.T) {
	// Answers the wrong line, then keeps stderr busy while await explains itself.
	cmd := exec.Command("sh", "-c", `echo wrong; i=0; while [ $i -lt 20000 ]; do echo noise >&2; i=$((i+1)); done`)
	np := &nodeProc{node: &Node{Name: "n"}, logs: new(safeLog)}
	cmd.Stderr = np.logs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	np.cmd, np.stdout, np.pid = cmd, bufio.NewReader(stdout), cmd.Process.Pid

	if err := np.await(context.Background(), holderNSReady); err == nil {
		t.Error("await must reject a holder that said the wrong thing")
	}
	if err := np.stop(context.Background()); err != nil {
		t.Errorf("stop: %v", err)
	}
	if err := np.stop(context.Background()); err != nil {
		t.Errorf("stop is called from every exit path, so it has to be idempotent: %v", err)
	}
}

func TestFinishEvidenceRecordingReturnsCloseFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "evidence-")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	err = finishEvidenceRecording(nil, &evidenceRecorder{node: "client", file: f})
	if !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), `finalize evidence recording for node "client"`) {
		t.Fatalf("close error = %v", err)
	}
}

func TestFinishEvidenceRecordingJoinsExecutionAndCloseFailures(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "evidence-")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	primary := errors.New("service execution failed")

	err = finishEvidenceRecording(primary, &evidenceRecorder{node: "client", file: f})
	if !errors.Is(err, primary) || !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "finalize evidence recording") {
		t.Fatalf("joined error = %v", err)
	}
}

func TestNodeStopReturnsHolderFailure(t *testing.T) {
	cmd := exec.Command("sh", "-c", `echo 'record evidence for node client: broken pipe' >&2; exit 7`)
	np := &nodeProc{node: &Node{Name: "client"}, logs: new(safeLog)}
	cmd.Stderr = np.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	np.cmd, np.pid = cmd, cmd.Process.Pid

	err := np.stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "record evidence") {
		t.Fatalf("stop error = %v", err)
	}
}

func TestNetemFaultArgvIsGeneratedAndSeeded(t *testing.T) {
	logical := &Interface{Segment: "upstream"}
	np := &nodeProc{pid: 42, node: &Node{Name: "gateway"}, ifaces: []*interfaceProc{{logical: logical, iface: "neabc1230"}}}
	env := &netnsEnv{}
	steps, _, err := env.faultSteps(Fault{Type: FaultNetem, Node: "gateway", Segment: "upstream",
		Delay: "40ms", Jitter: "20ms", Loss: "15.25%", Seed: 12345}, np)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(steps[0], " ")
	for _, want := range []string{"tc qdisc replace", "dev neabc1230 root netem", "delay 40ms 20ms", "loss 15.25%", "seed 12345"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q does not contain %q", got, want)
		}
	}
}

// The version gate decides whether seeded loss and jitter scenarios can run at
// all, and it reads a string from another project, so pin the shapes it sees.
func TestParseNetemSeedSupport(t *testing.T) {
	for _, c := range []struct {
		out     string
		want    bool
		version string
	}{
		{"tc utility, iproute2-6.17.0\n", true, "6.17.0"},
		{"tc utility, iproute2-6.6.0", true, "6.6.0"},
		{"tc utility, iproute2-6.5.0", false, "6.5.0"},
		{"tc utility, iproute2-6.1.0", false, "6.1.0"},
		{"tc utility, iproute2-7.0.0", true, "7.0.0"},
		{"tc utility, iproute2-5.15.0", false, "5.15.0"},
		// Pre-6.x releases named a snapshot, not a version.
		{"tc utility, iproute2-ss200127", false, "ss200127"},
		{"something else entirely", false, "something else entirely"},
	} {
		got, version := parseNetemSeedSupport(c.out)
		if got != c.want || version != c.version {
			t.Errorf("parseNetemSeedSupport(%q) = %t %q, want %t %q", c.out, got, version, c.want, c.version)
		}
	}
}
