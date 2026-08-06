// Command netdoc-sim builds throwaway virtual networks, runs netdoc inside
// them, and reports whether netdoc diagnosed what the scenario actually broke.
//
// It is a test harness for netdoc, not part of netdoc: nothing here ships in
// the netdoc binary, and the release builds only the root package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/heymaikol/network-doctor/internal/simulation"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// directorCommand is the hidden argv[1] the launcher re-executes itself with,
// once it is inside the simulation's namespaces.
const directorCommand = "__director"

// Exit codes. Separated so a CI job can tell "netdoc diagnosed this wrong"
// from "the simulator could not run".
const (
	exitOK       = 0 // every expectation held
	exitMismatch = 1 // the simulation ran; the diagnosis did not match
	exitUsage    = 2 // bad arguments
	exitError    = 3 // the simulation could not run
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	case simulation.NodeCommand:
		// A node holder. Never invoked by hand; the director spawns it.
		if len(args) != 2 {
			fmt.Fprintln(stderr, "netdoc-sim: internal: node holder needs a config path")
			return exitUsage
		}
		if err := simulation.RunNode(ctx, args[1], os.Stdin, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim: node:", err)
			return exitError
		}
		return exitOK
	case "run":
		return launch(ctx, args, stdout, stderr)
	case directorCommand:
		return direct(ctx, args[1:], stdout, stderr)
	case "validate":
		return validate(args[1:], stdout, stderr)
	case "scenarios":
		for _, n := range simulation.LibraryNames() {
			fmt.Fprintln(stdout, n)
		}
		return exitOK
	case "capabilities":
		return capabilities(ctx, stdout)
	case "list":
		return list(stdout, stderr)
	case "inspect":
		return inspect(args[1:], stdout, stderr)
	case "cleanup":
		return cleanup(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "netdoc-sim: unknown command %q\n", textsafe.Clean(args[0]))
	fmt.Fprintln(stderr, "run 'netdoc-sim help' for usage")
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: netdoc-sim <command> [arguments]

Builds a virtual network from a scenario, runs netdoc inside it, and reports
whether netdoc's diagnosis matched what the scenario broke.

Commands:
  run <scenario> [flags]   build the network, run the tests, print the report
  validate <scenario>      parse and check a scenario without building anything
  scenarios                list the built-in scenarios
  capabilities             report whether this host can simulate, and what a run does
  list                     list simulations left running by 'run -keep'
  inspect <id>             show a kept simulation's nodes and how to enter them
  cleanup [<id>|-all]      release a kept simulation's namespaces and files

A <scenario> is a built-in name or a path to a YAML file.

Flags for run:
  -json                    print the machine-readable report instead of text
  -keep                    hold the network open after the report until interrupted
  -netdoc <path>           the netdoc binary to run (default: alongside this one, then $PATH)
  -timeout <duration>      netdoc's per-probe timeout (default 4s)
  -repeat <n>              run each test n times to catch an unstable diagnosis
  -dry-run                 print every privileged command the run would make, and stop
  -v                       log each privileged command as it runs

Exit codes: 0 the diagnosis matched, 1 it did not, 2 bad arguments,
3 the simulation could not run.

Simulations are unprivileged: everything lives in a user namespace that owns
nothing on the host. Run 'netdoc-sim capabilities' for the details.
`)
}

// runFlags is the flag set shared by the launcher and the director, so the
// launcher can forward argv verbatim and both agree on what it means.
type runFlags struct {
	fs      *flag.FlagSet
	json    *bool
	keep    *bool
	netdoc  *string
	timeout *time.Duration
	repeat  *int
	dry     *bool
	verbose *bool
}

func newRunFlags(out io.Writer) *runFlags {
	fs := flag.NewFlagSet("netdoc-sim run", flag.ContinueOnError)
	fs.SetOutput(out)
	return &runFlags{
		fs:      fs,
		json:    fs.Bool("json", false, "print the machine-readable report"),
		keep:    fs.Bool("keep", false, "hold the network open after the report"),
		netdoc:  fs.String("netdoc", "", "path to the netdoc binary"),
		timeout: fs.Duration("timeout", 4*time.Second, "netdoc per-probe timeout"),
		repeat:  fs.Int("repeat", 1, "run each test n times"),
		dry:     fs.Bool("dry-run", false, "print the privileged commands and stop"),
		verbose: fs.Bool("v", false, "log each privileged command as it runs"),
	}
}

// parse pulls the scenario reference out of argv and parses the flags around
// it, so flags may come before or after the scenario — the same grammar netdoc
// itself accepts.
func (f *runFlags) parse(args []string) (string, error) {
	var ref string
	for {
		if err := f.fs.Parse(args); err != nil {
			return "", err
		}
		if f.fs.NArg() == 0 {
			break
		}
		if ref != "" {
			return "", fmt.Errorf("unexpected argument %q", textsafe.Clean(f.fs.Arg(0)))
		}
		ref = f.fs.Arg(0)
		args = f.fs.Args()[1:]
	}
	if ref == "" {
		return "", errors.New("a scenario is required")
	}
	if *f.repeat < 1 {
		return "", errors.New("-repeat must be at least 1")
	}
	if *f.timeout <= 0 {
		return "", errors.New("-timeout must be positive")
	}
	return ref, nil
}

// launch is `netdoc-sim run` in the user's own namespaces. It validates the
// scenario where a mistake is cheap, then re-executes itself inside a fresh
// user, network and mount namespace and lets that copy do the work.
func launch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newRunFlags(stderr)
	ref, err := f.parse(args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	if _, err := simulation.Load(ref); err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	if caps := simulation.DefaultBackend(false, nil).Capabilities(ctx); !caps.Supported {
		fmt.Fprintln(stderr, "netdoc-sim:", caps.Reason)
		return exitError
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	// The netdoc binary is resolved out here, where $PATH and the working
	// directory still mean what the user meant by them.
	path, err := findNetdoc(*f.netdoc, self)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	forward := append([]string{directorCommand, "-netdoc", path}, args[1:]...)
	code, err := simulation.LaunchDirector(ctx, self, forward, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	return code
}

// direct is the run, from inside the namespaces the launcher created.
func direct(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newRunFlags(stderr)
	ref, err := f.parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	scenario, err := simulation.Load(ref)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	var log io.Writer
	if *f.verbose || *f.dry {
		log = stderr
	}
	backend := simulation.DefaultBackend(*f.dry, log)
	if *f.dry {
		fmt.Fprintf(stderr, "would run, inside a user namespace owning nothing on this host:\n")
	}
	report := simulation.Run(ctx, scenario, backend, simulation.Options{
		Netdoc:       *f.netdoc,
		ProbeTimeout: *f.timeout,
		Repeat:       *f.repeat,
		Keep:         *f.keep && !*f.dry,
		Log:          log,
	})
	if *f.dry {
		return exitOK
	}
	if *f.json {
		if err := report.WriteJSON(stdout); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", err)
			return exitError
		}
	} else {
		report.WriteText(stdout)
	}
	if *f.keep {
		hold(ctx, report, stdout)
	}
	switch report.Result {
	case simulation.ResultPass:
		return exitOK
	case simulation.ResultError:
		return exitError
	}
	return exitMismatch
}

// hold keeps a simulation alive after its report so the user can walk around
// inside it. The record it leaves is what `list`, `inspect` and `cleanup` read.
func hold(ctx context.Context, report *simulation.Report, w io.Writer) {
	state := simulation.NewState(report.ID, report.Scenario, report.Cleanup.Workspace, report.StartedAt, report.Topology)
	if err := state.Save(); err != nil {
		fmt.Fprintln(w, "netdoc-sim: cannot record this simulation:", err)
	}
	fmt.Fprintf(w, "\nSimulation %s is still up. Enter a node with:\n", report.ID)
	for _, n := range report.Topology {
		fmt.Fprintf(w, "  nsenter -t %d -n -m -- sh   # %s (%s)\n", n.PID, n.Name, n.Address)
	}
	fmt.Fprintf(w, "Release it with `netdoc-sim cleanup %s`, or press Ctrl-C here.\n", report.ID)
	<-ctx.Done()
	// The namespaces go when this process does; the record must not outlive
	// them or `list` would advertise a simulation nobody can enter. A sweep
	// that failed gets said out loud — `cleanup` can still finish the job.
	if err := os.Remove(filepath.Join(simulation.StateDir(), report.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w, "netdoc-sim: cleanup:", err)
	}
	if err := os.RemoveAll(report.Cleanup.Workspace); err != nil {
		fmt.Fprintln(w, "netdoc-sim: cleanup:", err)
	}
}

func validate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "netdoc-sim: validate takes one scenario")
		return exitUsage
	}
	s, err := simulation.Load(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	fmt.Fprintf(stdout, "ok: %s — %d node(s), %d fault(s), %d test(s), %d expected check(s)\n",
		textsafe.Clean(s.Name), len(s.Topology.Nodes), len(s.Faults), len(s.Tests), len(s.Expect.Checks))
	return exitOK
}

func capabilities(ctx context.Context, w io.Writer) int {
	caps := simulation.DefaultBackend(false, nil).Capabilities(ctx)
	fmt.Fprintf(w, "Backend:   %s\nSupported: %t\n", caps.Backend, caps.Supported)
	if caps.Reason != "" {
		fmt.Fprintf(w, "Reason:    %s\n", caps.Reason)
	}
	if len(caps.Missing) > 0 {
		fmt.Fprintf(w, "Missing:   %s\n", strings.Join(caps.Missing, ", "))
	}
	fmt.Fprintln(w, "\nA run will:")
	for _, p := range caps.Privileged {
		fmt.Fprintln(w, "  -", p)
	}
	fmt.Fprintln(w, "\nIt will not touch the host's interfaces, routes, resolver or firewall:")
	fmt.Fprintln(w, "  inside its user namespace the simulator has no privileges over them at all.")
	if !caps.Supported {
		return exitError
	}
	return exitOK
}

func list(stdout, stderr io.Writer) int {
	states, err := simulation.ListStates()
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	if len(states) == 0 {
		fmt.Fprintln(stdout, "no simulations are being kept alive")
		return exitOK
	}
	for _, s := range states {
		status := "alive"
		if !s.Alive() {
			status = "gone (run `netdoc-sim cleanup` to sweep)"
		}
		fmt.Fprintf(stdout, "%s  %-40s pid %-7d %s  %s\n", s.ID, textsafe.Clean(s.Scenario), s.PID,
			s.Started.Format(time.RFC3339), status)
	}
	return exitOK
}

func inspect(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "netdoc-sim: inspect takes one simulation id")
		return exitUsage
	}
	s, err := simulation.LoadState(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	fmt.Fprintf(stdout, "Simulation %s (%s)\n  started   %s\n  director  pid %d (%s)\n  workspace %s\n\n",
		s.ID, textsafe.Clean(s.Scenario), s.Started.Format(time.RFC3339), s.PID, aliveWord(s.Alive()), s.Workspace)
	for _, n := range s.Nodes {
		fmt.Fprintf(stdout, "  %-10s %-14s %s\n", n.Name, n.Address, strings.Join(n.Services, " "))
		fmt.Fprintf(stdout, "             nsenter -t %d -n -m -- sh\n", n.PID)
	}
	return exitOK
}

func aliveWord(alive bool) string {
	if alive {
		return "alive"
	}
	return "gone"
}

func cleanup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("netdoc-sim cleanup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "release every kept simulation")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	var states []*simulation.State
	switch {
	case *all && fs.NArg() > 0:
		fmt.Fprintln(stderr, "netdoc-sim: -all takes no simulation id")
		return exitUsage
	case *all:
		var err error
		if states, err = simulation.ListStates(); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", err)
			return exitError
		}
	case fs.NArg() == 1:
		s, err := simulation.LoadState(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
			return exitUsage
		}
		states = []*simulation.State{s}
	default:
		fmt.Fprintln(stderr, "netdoc-sim: cleanup takes one simulation id, or -all")
		return exitUsage
	}
	if len(states) == 0 {
		fmt.Fprintln(stdout, "nothing to clean up")
		return exitOK
	}
	code := exitOK
	for _, s := range states {
		if err := s.Release(); err != nil {
			// Never silent: a cleanup that half worked is the one thing the
			// user has to know about.
			fmt.Fprintf(stderr, "netdoc-sim: %s: %s\n", s.ID, textsafe.Clean(err.Error()))
			code = exitError
			continue
		}
		fmt.Fprintf(stdout, "released %s (%s)\n", s.ID, textsafe.Clean(s.Scenario))
	}
	return code
}

// findNetdoc locates the binary the tests will run: the one asked for, else one
// sitting next to netdoc-sim, else one on $PATH. A build in the repo root
// beside a `go run ./cmd/netdoc-sim` binary will not be found, which is why the
// error says how to point at one.
func findNetdoc(want, self string) (string, error) {
	if want != "" {
		path, err := exec.LookPath(want)
		if err != nil {
			return "", fmt.Errorf("-netdoc %s: %w", want, err)
		}
		return filepath.Abs(path)
	}
	sibling := filepath.Join(filepath.Dir(self), "netdoc")
	if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
		return sibling, nil
	}
	if path, err := exec.LookPath("netdoc"); err == nil {
		return filepath.Abs(path)
	}
	return "", errors.New("cannot find the netdoc binary: build one with `go build -o netdoc .` and pass it with -netdoc ./netdoc")
}
