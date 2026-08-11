//go:build linux

package simulation

import (
	"bufio"
	"context"
	"errors"
	"io"
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

func TestCloseServicesReturnsCloseFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "service-")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := closeServices([]io.Closer{f}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("close services error = %v", err)
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

func TestNodeStopReturnsForcedKillFailure(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	np := &nodeProc{node: &Node{Name: "client"}, logs: new(safeLog)}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	np.cmd, np.pid = cmd, cmd.Process.Pid
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := np.stop(ctx); err == nil {
		t.Fatal("forced holder termination was reported as successful cleanup")
	}
}

func TestStartServicesStartsHolderWithoutServices(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	np := &nodeProc{node: &Node{Name: "client"}, stdin: writer,
		stdout: bufio.NewReader(strings.NewReader(holderServicesReady + "\n")), logs: new(safeLog)}

	err = (&netnsEnv{backend: &netnsBackend{}}).startServices(context.Background(), np)
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != holderStart+"\n" {
		t.Fatalf("holder command = %q, want %q", got, holderStart+"\n")
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

// The black hole is two commands that have to arrive together: narrowing the
// hop without suppressing the report produces ordinary PMTU discovery, and
// suppressing without narrowing produces nothing at all. The suppression must
// also be narrow — the output hook, one ICMP code per family — or the fault
// stops being a black hole and becomes a dead control plane.
func TestPMTUBlackholeFaultArgv(t *testing.T) {
	logical := &Interface{Segment: "transit"}
	np := &nodeProc{pid: 42, node: &Node{Name: "edge"}, ifaces: []*interfaceProc{{logical: logical, iface: "neabc1230"}}}
	env := &netnsEnv{tables: map[string]bool{}}
	steps, summary, err := env.faultSteps(Fault{Type: FaultPMTUBlackhole, Node: "edge", Segment: "transit", MTU: 576}, np)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(steps[0], " ")
	if !strings.Contains(got, "ip link set neabc1230 mtu 576") {
		t.Errorf("first step %q does not narrow the interface", got)
	}
	all := ""
	for _, step := range steps {
		all += strings.Join(step, " ") + "\n"
	}
	for _, want := range []string{
		"hook output priority 0",
		"icmp type destination-unreachable icmp code frag-needed drop",
		"icmpv6 type packet-too-big drop",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("steps do not contain %q:\n%s", want, all)
		}
	}
	// Anything broader would drop traffic the fault is not modelling.
	for _, unwanted := range []string{"hook forward", "hook postrouting", "meta l4proto icmp drop"} {
		if strings.Contains(all, unwanted) {
			t.Errorf("steps contain over-broad rule %q:\n%s", unwanted, all)
		}
	}
	if !strings.Contains(summary, "576") {
		t.Errorf("summary = %q, want it to name the MTU", summary)
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
