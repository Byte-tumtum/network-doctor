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
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/heymaikol/network-doctor/internal/simulation"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// directorCommand is the hidden argv[1] the launcher re-executes itself with,
// once it is inside the simulation's namespaces.
const directorCommand = "__director"
const campaignDirectorCommand = "__campaign_director"
const huntDirectorCommand = "__hunt_director"

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
	case "campaign":
		return launchCampaign(ctx, args[1:], stdout, stderr)
	case "hunt":
		return launchHunt(ctx, args[1:], stdout, stderr)
	case directorCommand:
		return direct(ctx, args[1:], stdout, stderr)
	case campaignDirectorCommand:
		return directCampaign(ctx, args[1:], stdout, stderr)
	case huntDirectorCommand:
		return directHunt(ctx, args[1:], stdout, stderr)
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
  campaign <scenario>      run a seeded scenario campaign sequentially
  hunt [base] [flags]      generate deterministic faults and rank likely bugs
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

Flags for campaign:
  -runs <n>                override the campaign's bounded default run count
  -seed <int64>            root seed (generated and printed when omitted)
  -iteration <n>           run exactly one independently derived iteration,
                           repeated -runs times when both are given
  -fail-fast               stop after the first mismatch or simulator error
  -json                    print the machine-readable aggregate report
  -netdoc <path>           the netdoc binary to run
  -timeout <duration>      netdoc's per-probe timeout (default 4s)
  -v                       log each privileged command as it runs

Flags for hunt:
  -cases <n>               unique generated cases to run (default 50, maximum 500)
  -seed <int64>            hunt seed (generated and printed when omitted)
  -case <n>                generate and run exactly one independently derived case
  -max-faults <n>          maximum mutations per case (default 2, maximum 3)
  -fail-fast               stop after the first case with a reportable finding
  -dry-run                 print generated manifests without creating namespaces
  -json                    print the machine-readable hunt report
  -netdoc <path>           the netdoc binary to run
  -timeout <duration>      netdoc's per-probe timeout (default 4s)
  -v                       log each privileged command as it runs

Exit codes: 0 the diagnosis matched, 1 it did not, 2 bad arguments,
3 the simulation could not run.

For hunt: 0 no reportable finding, 1 findings, 2 usage or generation
failure, 3 simulator runtime failure or cancellation.

Simulations are unprivileged: everything lives in a user namespace that owns
nothing on the host. Run 'netdoc-sim capabilities' for the details.
`)
}

// parseRef parses the flags around a single positional argument, so flags may
// come before or after it — the same grammar netdoc itself accepts. It returns
// "" when no positional argument was given; each caller decides whether that is
// a default or an error.
func parseRef(fs *flag.FlagSet, args []string) (string, error) {
	ref := ""
	for {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		if fs.NArg() == 0 {
			return ref, nil
		}
		if ref != "" {
			return "", fmt.Errorf("unexpected argument %q", textsafe.Clean(fs.Arg(0)))
		}
		ref = fs.Arg(0)
		args = fs.Args()[1:]
	}
}

type huntFlags struct {
	fs        *flag.FlagSet
	json      *bool
	cases     *int
	seed      optionalSeed
	caseNum   *int
	maxFaults *int
	failFast  *bool
	dry       *bool
	netdoc    *string
	timeout   *time.Duration
	verbose   *bool
}

func newHuntFlags(out io.Writer) *huntFlags {
	f := &huntFlags{fs: flag.NewFlagSet("netdoc-sim hunt", flag.ContinueOnError)}
	f.fs.SetOutput(out)
	f.json = f.fs.Bool("json", false, "print the machine-readable hunt report")
	f.cases = f.fs.Int("cases", 50, "unique generated cases to run")
	f.fs.Var(&f.seed, "seed", "hunt seed")
	f.caseNum = f.fs.Int("case", -1, "run exactly one independently derived case")
	f.maxFaults = f.fs.Int("max-faults", 2, "maximum mutations per case")
	f.failFast = f.fs.Bool("fail-fast", false, "stop after the first reportable finding")
	f.dry = f.fs.Bool("dry-run", false, "print generated manifests without running them")
	f.netdoc = f.fs.String("netdoc", "", "path to the netdoc binary")
	f.timeout = f.fs.Duration("timeout", 4*time.Second, "netdoc per-probe timeout")
	f.verbose = f.fs.Bool("v", false, "log each privileged command as it runs")
	return f
}

func (f *huntFlags) parse(args []string) (string, error) {
	ref, err := parseRef(f.fs, args)
	if err != nil {
		return "", err
	}
	if ref == "" {
		ref = "healthy-routed-network"
	}
	if *f.cases < 1 || *f.cases > simulation.HuntMaxCases {
		return "", fmt.Errorf("-cases must be between 1 and %d", simulation.HuntMaxCases)
	}
	if *f.caseNum < -1 || *f.caseNum > simulation.HuntMaxCaseNumber {
		return "", fmt.Errorf("-case must be between 0 and %d", simulation.HuntMaxCaseNumber)
	}
	if *f.maxFaults < 1 || *f.maxFaults > simulation.HuntMaxFaults {
		return "", fmt.Errorf("-max-faults must be between 1 and %d", simulation.HuntMaxFaults)
	}
	if *f.timeout <= 0 {
		return "", errors.New("-timeout must be positive")
	}
	if !slices.Contains(simulation.HuntBaseNames(), ref) {
		return "", fmt.Errorf("unsupported hunt base %q (have: %s)", textsafe.Clean(ref), strings.Join(simulation.HuntBaseNames(), ", "))
	}
	return ref, nil
}

type optionalSeed struct {
	set bool
	v   int64
}

func (s *optionalSeed) String() string {
	if !s.set {
		return ""
	}
	return strconv.FormatInt(s.v, 10)
}

func (s *optionalSeed) Set(raw string) error {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid seed %q: %w", textsafe.Clean(raw), err)
	}
	s.set, s.v = true, v
	return nil
}

type campaignFlags struct {
	fs        *flag.FlagSet
	json      *bool
	runs      *int
	seed      optionalSeed
	iteration *int
	failFast  *bool
	netdoc    *string
	timeout   *time.Duration
	verbose   *bool
}

func newCampaignFlags(out io.Writer) *campaignFlags {
	f := &campaignFlags{fs: flag.NewFlagSet("netdoc-sim campaign", flag.ContinueOnError)}
	f.fs.SetOutput(out)
	f.json = f.fs.Bool("json", false, "print the machine-readable aggregate report")
	f.runs = f.fs.Int("runs", 0, "override campaign run count")
	f.fs.Var(&f.seed, "seed", "campaign root seed")
	f.iteration = f.fs.Int("iteration", -1, "run exactly one iteration")
	f.failFast = f.fs.Bool("fail-fast", false, "stop after the first failure")
	f.netdoc = f.fs.String("netdoc", "", "path to the netdoc binary")
	f.timeout = f.fs.Duration("timeout", 4*time.Second, "netdoc per-probe timeout")
	f.verbose = f.fs.Bool("v", false, "log each privileged command as it runs")
	return f
}

func (f *campaignFlags) parse(args []string) (string, error) {
	ref, err := parseRef(f.fs, args)
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", errors.New("a campaign scenario is required")
	}
	if *f.runs < 0 || *f.runs > 1000 {
		return "", errors.New("-runs must be between 1 and 1000 when supplied")
	}
	if *f.iteration < -1 || *f.iteration > 999999 {
		return "", errors.New("-iteration must be between 0 and 999999")
	}
	if *f.timeout <= 0 {
		return "", errors.New("-timeout must be positive")
	}
	return ref, nil
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

// parse pulls the scenario reference out of argv and bounds the flags.
func (f *runFlags) parse(args []string) (string, error) {
	ref, err := parseRef(f.fs, args)
	if err != nil {
		return "", err
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
	code, err := simulation.LaunchDirector(ctx, self, directorArgv(f, ref, path), stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	return code
}

func launchCampaign(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newCampaignFlags(stderr)
	ref, err := f.parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	scenario, err := simulation.Load(ref)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	if scenario.Campaign == nil {
		fmt.Fprintln(stderr, "netdoc-sim: scenario has no campaign definition")
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
	path, err := findNetdoc(*f.netdoc, self)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	if !f.seed.set {
		f.seed.v, err = simulation.RandomSeed()
		if err != nil {
			fmt.Fprintln(stderr, "netdoc-sim: choose campaign seed:", err)
			return exitError
		}
		f.seed.set = true
	}
	code, err := simulation.LaunchDirector(ctx, self, campaignDirectorArgv(f, ref, path), stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	return code
}

func campaignDirectorArgv(f *campaignFlags, ref, netdoc string) []string {
	return []string{campaignDirectorCommand,
		"-netdoc", netdoc,
		"-timeout", f.timeout.String(),
		"-runs", strconv.Itoa(*f.runs),
		"-seed", strconv.FormatInt(f.seed.v, 10),
		"-iteration", strconv.Itoa(*f.iteration),
		fmt.Sprintf("-json=%t", *f.json),
		fmt.Sprintf("-fail-fast=%t", *f.failFast),
		fmt.Sprintf("-v=%t", *f.verbose),
		"--", ref,
	}
}

func directCampaign(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newCampaignFlags(stderr)
	ref, err := f.parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	if !f.seed.set {
		fmt.Fprintln(stderr, "netdoc-sim: internal campaign director did not receive a seed")
		return exitError
	}
	scenario, err := simulation.Load(ref)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	var log io.Writer
	if *f.verbose {
		log = stderr
	}
	var iteration *int
	if *f.iteration >= 0 {
		value := *f.iteration
		iteration = &value
	}
	result := simulation.RunCampaign(ctx, scenario, func() simulation.Backend {
		return simulation.DefaultBackend(false, log)
	}, simulation.CampaignOptions{
		Run:  simulation.Options{Netdoc: *f.netdoc, ProbeTimeout: *f.timeout, Log: log},
		Runs: *f.runs, Seed: f.seed.v, Iteration: iteration, FailFast: *f.failFast,
	})
	if *f.json {
		if err := result.WriteJSON(stdout); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", err)
			return exitError
		}
	} else {
		result.WriteText(stdout)
	}
	switch result.Result {
	case simulation.ResultPass:
		return exitOK
	case simulation.ResultError:
		return exitError
	default:
		return exitMismatch
	}
}

func launchHunt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newHuntFlags(stderr)
	baseID, err := f.parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	base, err := simulation.LibraryScenario(baseID)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	if !f.seed.set {
		f.seed.v, err = simulation.RandomSeed()
		if err != nil {
			fmt.Fprintln(stderr, "netdoc-sim: choose hunt seed:", err)
			return exitError
		}
		f.seed.set = true
	}
	if *f.dry {
		result := simulation.RunHunt(ctx, baseID, base, nil, huntOptions(f, nil, true))
		return writeHuntResult(result, *f.json, stdout, stderr)
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
	path, err := findNetdoc(*f.netdoc, self)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	code, err := simulation.LaunchDirector(ctx, self, huntDirectorArgv(f, baseID, path), stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	return code
}

func huntDirectorArgv(f *huntFlags, baseID, netdoc string) []string {
	return []string{huntDirectorCommand,
		"-netdoc", netdoc,
		"-timeout", f.timeout.String(),
		"-cases", strconv.Itoa(*f.cases),
		"-seed", strconv.FormatInt(f.seed.v, 10),
		"-case", strconv.Itoa(*f.caseNum),
		"-max-faults", strconv.Itoa(*f.maxFaults),
		fmt.Sprintf("-json=%t", *f.json),
		fmt.Sprintf("-fail-fast=%t", *f.failFast),
		fmt.Sprintf("-v=%t", *f.verbose),
		"--", baseID,
	}
}

func directHunt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := newHuntFlags(stderr)
	baseID, err := f.parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	if !f.seed.set {
		fmt.Fprintln(stderr, "netdoc-sim: internal hunt director did not receive a seed")
		return exitError
	}
	base, err := simulation.LibraryScenario(baseID)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	var log io.Writer
	if *f.verbose {
		log = stderr
	}
	result := simulation.RunHunt(ctx, baseID, base, func() simulation.Backend {
		return simulation.DefaultBackend(false, log)
	}, huntOptions(f, log, false))
	return writeHuntResult(result, *f.json, stdout, stderr)
}

func huntOptions(f *huntFlags, log io.Writer, dry bool) simulation.HuntOptions {
	var caseNumber *int
	if *f.caseNum >= 0 {
		value := *f.caseNum
		caseNumber = &value
	}
	return simulation.HuntOptions{Cases: *f.cases, Seed: f.seed.v, Case: caseNumber,
		MaxFaults: *f.maxFaults, FailFast: *f.failFast, DryRun: dry,
		Run: simulation.Options{Netdoc: *f.netdoc, ProbeTimeout: *f.timeout, Log: log}}
}

func writeHuntResult(result *simulation.HuntResult, jsonOutput bool, stdout, stderr io.Writer) int {
	if jsonOutput {
		if err := result.WriteJSON(stdout); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", err)
			return exitError
		}
	} else {
		result.WriteText(stdout)
	}
	switch {
	case result.Result == simulation.HuntResultClean:
		return exitOK
	case result.Result == simulation.HuntResultFindings:
		return exitMismatch
	case result.ErrorKind == "configuration" || result.ErrorKind == simulation.FindingGeneratorDefect:
		return exitUsage
	default:
		return exitError
	}
}

// directorArgv builds the director's command line out of what the launcher
// parsed, rather than forwarding the user's argv with an extra flag bolted on
// the front. Forwarding raw would leave two -netdoc flags on the line whenever
// the user passed one, and the flag package takes the last — quietly throwing
// away the path the launcher resolved and validated while $PATH and the working
// directory still meant what the user meant by them.
//
// Every value here is the parsed one rendered canonically, so there is nothing
// left to re-interpret: exactly one occurrence of each flag, and the scenario
// behind a "--" so a reference that starts with a dash stays a scenario.
func directorArgv(f *runFlags, ref, netdoc string) []string {
	return []string{
		directorCommand,
		"-netdoc", netdoc,
		"-timeout", f.timeout.String(),
		"-repeat", strconv.Itoa(*f.repeat),
		fmt.Sprintf("-json=%t", *f.json),
		fmt.Sprintf("-keep=%t", *f.keep),
		fmt.Sprintf("-dry-run=%t", *f.dry),
		fmt.Sprintf("-v=%t", *f.verbose),
		"--", ref,
	}
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

var aliveWord = map[bool]string{true: "alive", false: "gone"}

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
		s.ID, textsafe.Clean(s.Scenario), s.Started.Format(time.RFC3339), s.PID, aliveWord[s.Alive()], s.Workspace)
	for _, n := range s.Nodes {
		fmt.Fprintf(stdout, "  %-10s %-14s %s\n", n.Name, n.Address, strings.Join(n.Services, " "))
		fmt.Fprintf(stdout, "             nsenter -t %d -n -m -- sh\n", n.PID)
	}
	return exitOK
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
