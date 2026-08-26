// The SSH transport and the worker protocol, exercised against a stand-in ssh
// rather than a live server: the failures worth testing here are the ones a
// real host would be an unreliable way to produce.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/report"
	"github.com/heymaikol/network-doctor/internal/snapshot"
)

// The stand-in ssh is this test binary, re-executed. TestMain answers before
// the testing package parses flags, which is what lets the process be started
// with ssh's argv instead of a test binary's.
const (
	fakeSSHEnv     = "NETDOC_TEST_FAKE_SSH"
	fakeSSHArgv    = "NETDOC_TEST_FAKE_SSH_ARGV"
	fakeSSHRequest = "NETDOC_TEST_FAKE_SSH_REQUEST"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeSSHEnv); mode != "" {
		os.Exit(fakeSSH(mode))
	}
	os.Exit(m.Run())
}

// fakeSSH behaves like the ssh client for one exchange. Every mode is a real
// thing OpenSSH or a remote shell does: a command that is not there, a
// connection that never opened, a peer that says something other than the
// protocol.
func fakeSSH(mode string) int {
	if path := os.Getenv(fakeSSHArgv); path != "" {
		// #nosec G703 -- path comes from this test binary's own environment,
		// set by useFakeSSH to a directory the test just created.
		_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	}
	switch mode {
	case "missing":
		// What a remote shell says when the program is not on its PATH. The
		// status is the POSIX one: an exit status is eight bits by the time
		// the local ssh process spends it, so cmd.exe's 9009 arrives smaller.
		fmt.Fprintln(os.Stderr, "'netdoc' is not recognized as an internal or external command,")
		return 127
	case "sshfail":
		fmt.Fprintln(os.Stderr, "ssh: Could not resolve hostname nope: Name or service not known")
		return sshFailedStatus
	case "silent":
		return 0
	case "garbage":
		fmt.Fprintln(os.Stdout, "Welcome to the machine.")
		return 0
	case "hang":
		time.Sleep(30 * time.Second)
		return 0
	}

	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, "fake ssh:", err)
		return 1
	}
	if path := os.Getenv(fakeSSHRequest); path != "" {
		body, _ := json.Marshal(req)
		// #nosec G703 -- as above: the path is the test harness's own.
		_ = os.WriteFile(path, body, 0o600)
	}
	resp := Response{Protocol: Protocol, Tool: snapshot.Tool{Version: "9.9.9", OS: "windows", Arch: "amd64"}}
	switch mode {
	case "ok", "unhealthy":
		rep := &report.Report{Version: "9.9.9", OK: mode == "ok", Verdict: "ok", Checks: []report.Check{}}
		if mode == "unhealthy" {
			rep.Verdict, rep.FailedStage = "network", "internet"
		}
		resp.Report = rep
		resp.Snapshot = &snapshot.Snapshot{Schema: snapshot.Schema, Tool: resp.Tool, OK: rep.OK}
	case "refuse":
		resp.Error = "-public-dns: \"nope\" is not an IP address"
	case "protocol2":
		resp.Protocol = 2
	case "twice":
		resp.Report = &report.Report{OK: true}
		resp.Snapshot = &snapshot.Snapshot{Schema: snapshot.Schema}
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(resp)
		_ = enc.Encode(resp)
		return 0
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
	return 0
}

// useFakeSSH points the transport at the stand-in and returns the files it
// records the invocation in.
func useFakeSSH(t *testing.T, mode string) (argvPath, requestPath string) {
	t.Helper()
	previous := sshProgram
	sshProgram = os.Args[0]
	t.Cleanup(func() { sshProgram = previous })
	argvPath = t.TempDir() + string(os.PathSeparator) + "argv"
	requestPath = t.TempDir() + string(os.PathSeparator) + "request"
	t.Setenv(fakeSSHEnv, mode)
	t.Setenv(fakeSSHArgv, argvPath)
	t.Setenv(fakeSSHRequest, requestPath)
	return argvPath, requestPath
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a path this test just created.
	if err != nil {
		t.Fatalf("the stand-in ssh recorded nothing: %v", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestRunPassesTheDestinationThroughUntouchedWithAFixedRemoteCommand(t *testing.T) {
	argvPath, requestPath := useFakeSSH(t, "ok")
	// An alias out of ~/.ssh/config, which only resolves because ssh is handed
	// it exactly as typed. Anything netdoc did to it here would break it.
	resp, err := Run(context.Background(), "ideapad", "", Request{Target: "example.com", TimeoutMs: 3000})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Report == nil || !resp.Report.OK {
		t.Fatalf("response did not carry a healthy report: %+v", resp)
	}
	want := []string{"-T", "ideapad", DefaultCommand, WorkerFlag}
	got := readLines(t, argvPath)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ssh argv = %q, want %q", got, want)
	}
	// The target travels as data on stdin, never as a word in the remote
	// command line, so no remote shell ever has a chance to read it.
	var req Request
	data, err := os.ReadFile(requestPath) // #nosec G304 -- a path this test just created.
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	if req.Target != "example.com" || req.Protocol != Protocol || req.TimeoutMs != 3000 {
		t.Errorf("the worker received %+v", req)
	}
}

func TestRunKeepsAHostileTargetOutOfTheRemoteCommandLine(t *testing.T) {
	argvPath, requestPath := useFakeSSH(t, "ok")
	hostile := `a" & calc.exe & "b`
	if _, err := Run(context.Background(), "server", "", Request{Target: hostile}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, arg := range readLines(t, argvPath) {
		if strings.Contains(arg, "calc.exe") {
			t.Fatalf("a target reached the remote command line: %q", arg)
		}
	}
	data, err := os.ReadFile(requestPath) // #nosec G304 -- a path this test just created.
	if err != nil {
		t.Fatal(err)
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	if req.Target != hostile {
		t.Errorf("target = %q, want it delivered verbatim as data", req.Target)
	}
}

func TestRunUsesTheCommandOverrideForThePrograms(t *testing.T) {
	argvPath, _ := useFakeSSH(t, "ok")
	if _, err := Run(context.Background(), "server", `C:\tmp\netdoc.exe`, Request{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := readLines(t, argvPath)
	if len(got) != 4 || got[2] != `C:\tmp\netdoc.exe` {
		t.Errorf("ssh argv = %q, want the override as the remote program", got)
	}
}

func TestRunReportsAFailedDiagnosisAsASuccessfulExchange(t *testing.T) {
	useFakeSSH(t, "unhealthy")
	resp, err := Run(context.Background(), "server", "", Request{})
	if err != nil {
		t.Fatalf("a remote network that fails checks is not a transport error: %v", err)
	}
	if resp.Report == nil || resp.Report.OK {
		t.Fatalf("response = %+v, want an unhealthy report", resp)
	}
}

func TestRunDistinguishesTheTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"missing", []string{"netdoc did not run on the SSH host", "ssh exited 127", "is not recognized", CommandEnv, WorkerFlag}},
		{"sshfail", []string{"ssh could not open the connection", "Could not resolve hostname"}},
		{"silent", []string{"netdoc did not run on the SSH host"}},
		{"garbage", []string{"could not read the remote response"}},
		{"twice", []string{"more than one response"}},
		{"protocol2", []string{"remote protocol 2, not 1", "9.9.9"}},
		{"refuse", []string{"is not an IP address"}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			useFakeSSH(t, tc.mode)
			_, err := Run(context.Background(), "server", "", Request{})
			if err == nil {
				t.Fatal("want an error, got none")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestRunEndsWhenTheContextIsCancelled(t *testing.T) {
	useFakeSSH(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, "server", "", Request{})
	if err == nil {
		t.Fatal("want an error, got none")
	}
	if !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("error = %q, want it to say the run was interrupted", err)
	}
	// The stand-in sleeps for 30 seconds; returning anywhere near that would
	// mean the cancellation never reached the ssh process.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %s to notice the cancellation", elapsed)
	}
}

func TestValidateDestinationRefusesArgvOptionInjection(t *testing.T) {
	for _, dest := range []string{
		"-oProxyCommand=touch /tmp/pwned",
		"-i/tmp/key",
		"--",
		"server -oProxyCommand=x",
		"serv\ner",
		"serv er",
		"",
	} {
		if err := validateDestination(dest); err == nil {
			t.Errorf("validateDestination(%q) accepted it", dest)
		}
	}
	// Everything OpenSSH itself accepts as a destination still has to work.
	for _, dest := range []string{"ideapad", "user@host", "ssh://user@host:2222", "10.0.0.4", "[fe80::1]", "host.example.com"} {
		if err := validateDestination(dest); err != nil {
			t.Errorf("validateDestination(%q) = %v, want it accepted", dest, err)
		}
	}
}

func TestValidateCommandRefusesAnythingARemoteShellWouldReinterpret(t *testing.T) {
	for _, command := range []string{
		"netdoc; rm -rf /",
		"netdoc & calc.exe",
		"netdoc $(id)",
		"netdoc `id`",
		`C:\Program Files\netdoc.exe`,
		"%COMSPEC%",
		"-oProxyCommand=x",
		"netdoc|tee",
	} {
		if err := validateCommand(command); err == nil {
			t.Errorf("validateCommand(%q) accepted it", command)
		}
	}
	for _, command := range []string{"netdoc", "/usr/local/bin/netdoc", `C:\Users\me\bin\netdoc.exe`, "~/bin/netdoc"} {
		if err := validateCommand(command); err != nil {
			t.Errorf("validateCommand(%q) = %v, want it accepted", command, err)
		}
	}
}

func TestServeAnswersOneRequestWithOneObjectAndNothingElse(t *testing.T) {
	var out strings.Builder
	tool := snapshot.Tool{Version: "1.2.3", OS: "windows", Arch: "amd64"}
	var seen Request
	err := Serve(context.Background(), liveStdin(t, `{"protocol":1,"target":"example.com","timeout_ms":2000}`), &out, tool,
		func(_ context.Context, req Request) (*report.Report, *snapshot.Snapshot, error) {
			seen = req
			return &report.Report{OK: true}, &snapshot.Snapshot{Schema: snapshot.Schema}, nil
		})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if seen.Target != "example.com" || seen.TimeoutMs != 2000 {
		t.Errorf("the worker was handed %+v", seen)
	}
	// Exactly one JSON value and no trailing prose: this stream is the protocol.
	dec := json.NewDecoder(strings.NewReader(out.String()))
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("worker stdout is not one JSON object: %v", err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		t.Errorf("worker stdout carried more than the response: %q", out.String())
	}
	if resp.Protocol != Protocol || resp.Tool != tool || resp.Report == nil || resp.Snapshot == nil {
		t.Errorf("response = %+v", resp)
	}
}

func TestServeRefusesWhatItCannotRun(t *testing.T) {
	ran := func(context.Context, Request) (*report.Report, *snapshot.Snapshot, error) {
		t.Error("the diagnosis ran for a request that should have been refused")
		return nil, nil, nil
	}
	for _, tc := range []struct {
		name, in, want string
	}{
		{"another protocol", `{"protocol":7}`, "protocol 1, not 7"},
		{"not json", "hello", "could not read the remote request"},
		{"empty stream", "", "could not read the remote request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			if err := Serve(context.Background(), strings.NewReader(tc.in), &out, snapshot.Tool{}, ran); err != nil {
				t.Fatalf("Serve: %v", err)
			}
			var resp Response
			if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Protocol != Protocol {
				t.Errorf("a refusal has to stay readable to the version that asked: %+v", resp)
			}
			if !strings.Contains(resp.Error, tc.want) {
				t.Errorf("error = %q, want it to mention %q", resp.Error, tc.want)
			}
		})
	}
}

func TestServeReportsARejectedRequestInsteadOfFailingTheExchange(t *testing.T) {
	var out strings.Builder
	err := Serve(context.Background(), liveStdin(t, `{"protocol":1}`), &out, snapshot.Tool{},
		func(context.Context, Request) (*report.Report, *snapshot.Snapshot, error) {
			return nil, nil, errors.New("-timeout must be positive")
		})
	if err != nil {
		t.Fatalf("a refused request is still a completed exchange: %v", err)
	}
	var resp Response
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != "-timeout must be positive" || resp.Report != nil {
		t.Errorf("response = %+v", resp)
	}
}

func TestServeStripsEscapeSequencesFromWhatItSaysBack(t *testing.T) {
	var out strings.Builder
	_ = Serve(context.Background(), liveStdin(t, `{"protocol":1}`), &out, snapshot.Tool{},
		func(context.Context, Request) (*report.Report, *snapshot.Snapshot, error) {
			return nil, nil, errors.New("bad target \x1b[31mred\x1b]0;title\x07")
		})
	var resp Response
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(resp.Error, 0x1b) {
		t.Errorf("an escape sequence survived into the response: %q", resp.Error)
	}
}

func TestServeStopsProbingWhenTheCallerGoesAway(t *testing.T) {
	// Stdin at EOF after the request is what a dropped SSH channel looks like
	// from the worker's side, and it has to reach the running diagnosis.
	var out strings.Builder
	stopped := make(chan struct{})
	err := Serve(context.Background(), strings.NewReader(`{"protocol":1}`), &out, snapshot.Tool{},
		func(ctx context.Context, _ Request) (*report.Report, *snapshot.Snapshot, error) {
			select {
			case <-ctx.Done():
				close(stopped)
				return nil, nil, errors.New("the run was interrupted before it finished")
			case <-time.After(10 * time.Second):
				return nil, nil, errors.New("the cancellation never arrived")
			}
		})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("the diagnosis was not cancelled when the caller's stream ended")
	}
}

func TestServeRefusesARequestThatNeverEnds(t *testing.T) {
	var out strings.Builder
	err := Serve(context.Background(), endless{}, &out, snapshot.Tool{},
		func(context.Context, Request) (*report.Report, *snapshot.Snapshot, error) {
			t.Error("an unbounded request reached the diagnosis")
			return nil, nil, nil
		})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), "could not read the remote request") {
		t.Errorf("response = %q", out.String())
	}
}

func TestDecodeResponseRefusesAnUnboundedStream(t *testing.T) {
	if _, err := decodeResponse(endless{}); err == nil {
		t.Fatal("an unbounded response was accepted")
	}
}

func TestDecodeResponseNamesWhatIsMissing(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"nothing at all", "", "no response"},
		{"cut short", `{"protocol":1,"report":`, "could not read the remote response"},
		{"neither answer nor error", `{"protocol":1}`, "carried no diagnosis and no error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeResponse(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("decodeResponse = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// liveStdin is a request followed by a stream that stays open, which is what
// the local side holds while it waits for an answer. A reader that ends at the
// request would be a caller that hung up, and the worker treats that as one.
func liveStdin(t *testing.T, request string) io.Reader {
	t.Helper()
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	return io.MultiReader(strings.NewReader(request), blockUntil{held})
}

type blockUntil struct{ done <-chan struct{} }

func (b blockUntil) Read([]byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

// endless never stops producing plausible-looking bytes, the shape of a peer
// that answers a request with an unbounded stream.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}
