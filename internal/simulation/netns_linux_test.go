//go:build linux

package simulation

import (
	"bufio"
	"context"
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
