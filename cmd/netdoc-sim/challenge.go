package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heymaikol/network-doctor/internal/simulation"
	"github.com/heymaikol/network-doctor/internal/textsafe"
)

// Challenge Mode: one foreground session, start to finish.
//
// It is not split into start/shell/diagnose/reveal commands because a simulated
// network only exists while the process holding its namespaces does. Separate
// commands would need a daemon and a state file to point at it; one session
// needs neither, and gets the two properties that matter for free — the player
// and netdoc cannot be handed different networks, and the network is gone when
// the command returns.

// challengeNoteLimit bounds the free-text note. It is recorded and printed, not
// scored, so it only has to be short enough to stay a note.
const challengeNoteLimit = 200

type challengeFlags struct {
	fs         *flag.FlagSet
	id         *string
	difficulty *string
	answer     *string
	giveUp     *bool
	json       *bool
	netdoc     *string
	// netdocVersion is not for people to set. The launcher resolves the binary
	// and asks it for its version out here, where $PATH and the working
	// directory still mean what the user meant, and hands both to the director
	// so the identity in the result belongs to the binary that was found.
	netdocVersion *string
	timeout       *time.Duration
	verbose       *bool
}

func newChallengeFlags(out io.Writer) *challengeFlags {
	f := &challengeFlags{fs: flag.NewFlagSet("netdoc-sim challenge", flag.ContinueOnError)}
	f.fs.SetOutput(out)
	f.id = f.fs.String("id", "", "replay a specific challenge")
	f.difficulty = f.fs.String("difficulty", "", "draw an easy, medium or hard challenge")
	f.answer = f.fs.String("answer", "", "submit this diagnosis without opening a shell")
	f.giveUp = f.fs.Bool("give-up", false, "skip straight to the answer")
	f.json = f.fs.Bool("json", false, "print the machine-readable result")
	f.netdoc = f.fs.String("netdoc", "", "path to the netdoc binary")
	f.netdocVersion = f.fs.String("netdoc-version", "", "internal: what the resolved netdoc binary reports for -version")
	f.timeout = f.fs.Duration("timeout", 4*time.Second, "netdoc per-probe timeout")
	f.verbose = f.fs.Bool("v", false, "log each privileged command as it runs")
	return f
}

func (f *challengeFlags) parse(args []string) error {
	id, err := parseRef(f.fs, args)
	if err != nil {
		return err
	}
	// `challenge 8F42C1` is what people type, so accept it as well as -id.
	if id != "" {
		if *f.id != "" && *f.id != id {
			return errors.New("a challenge id was given twice")
		}
		*f.id = id
	}
	if *f.id != "" && *f.difficulty != "" {
		return errors.New("-id names one challenge, so -difficulty has nothing to choose")
	}
	if *f.answer != "" && *f.giveUp {
		return errors.New("-answer and -give-up are two different submissions")
	}
	if *f.answer != "" {
		if _, ok := simulation.ChallengeAnswerByID(*f.answer); !ok {
			return fmt.Errorf("unknown answer %q (have: %s)", textsafe.Clean(*f.answer),
				strings.Join(simulation.ChallengeAnswerNames(), ", "))
		}
	}
	if *f.json && *f.answer == "" && !*f.giveUp {
		return errors.New("-json runs without a terminal, so it needs -answer or -give-up")
	}
	if *f.timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	return nil
}

// launchChallenge resolves the challenge where a mistake is cheap, then hands
// the work to a director inside fresh namespaces. Resolution happens exactly
// once, out here: the director is told an id, so the case the player is briefed
// on and the case netdoc is graded on cannot come apart.
func launchChallenge(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := newChallengeFlags(stderr)
	if err := f.parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	challenge, err := resolveChallenge(*f.id, *f.difficulty)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	if caps := newBackend(false, nil).Capabilities(ctx); !caps.Supported {
		fmt.Fprintln(stderr, "netdoc-sim:", caps.Reason)
		return exitError
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	// Resolved once, before any namespace exists: the executable recorded in the
	// result has to be the executable the run launches, and a binary that cannot
	// even print its version is worth rejecting while the error is still cheap.
	netdoc, err := resolveNetdoc(ctx, *f.netdoc, self)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	code, err := launchDirector(ctx, self, challengeDirectorArgv(f, challenge.ID, netdoc), stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitError
	}
	return code
}

func resolveChallenge(id, difficulty string) (*simulation.Challenge, error) {
	if id != "" {
		return simulation.BuildChallenge(id)
	}
	return simulation.FindChallenge(difficulty)
}

func challengeDirectorArgv(f *challengeFlags, id string, netdoc netdocIdentity) []string {
	return []string{challengeDirectorCommand,
		"-netdoc", netdoc.path,
		"-netdoc-version", netdoc.version,
		"-timeout", f.timeout.String(),
		"-id", id,
		"-answer", *f.answer,
		fmt.Sprintf("-give-up=%t", *f.giveUp),
		fmt.Sprintf("-json=%t", *f.json),
		fmt.Sprintf("-v=%t", *f.verbose),
	}
}

// directChallenge is the challenge, from inside the namespaces the launcher
// created. It owns the whole session: the briefing, the shell, the submission,
// the netdoc run, and the reveal.
func directChallenge(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f := newChallengeFlags(stderr)
	if err := f.parse(args); err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", err)
		return exitUsage
	}
	if *f.id == "" {
		fmt.Fprintln(stderr, "netdoc-sim: internal challenge director did not receive an id")
		return exitError
	}
	if *f.netdoc == "" || *f.netdocVersion == "" {
		fmt.Fprintln(stderr, "netdoc-sim: internal challenge director did not receive a resolved netdoc")
		return exitError
	}
	challenge, err := simulation.BuildChallenge(*f.id)
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitUsage
	}
	var log io.Writer
	if *f.verbose {
		log = stderr
	}
	// With -json the result is the only thing on stdout, so everything the
	// session says goes to stderr and stays pipeable.
	console := stdout
	if *f.json {
		console = stderr
	}
	play := &challengePlay{challenge: challenge, stdin: stdin, in: bufio.NewReader(stdin), out: console,
		answer: *f.answer, giveUp: *f.giveUp}
	result, err := simulation.RunChallenge(ctx, challenge, newBackend(false, log), simulation.ChallengeOptions{
		Run:           simulation.Options{Netdoc: *f.netdoc, ProbeTimeout: *f.timeout, Log: log},
		NetdocVersion: *f.netdocVersion,
		Play:          play.run,
	})
	if err != nil {
		fmt.Fprintln(stderr, "netdoc-sim:", textsafe.Clean(err.Error()))
		return exitError
	}
	if *f.json {
		if err := result.WriteJSON(stdout); err != nil {
			fmt.Fprintln(stderr, "netdoc-sim:", err)
			return exitError
		}
	} else {
		result.WriteText(stdout)
	}
	switch {
	case result.Result == simulation.ChallengeNoResult:
		return exitError
	case result.Human.Score == simulation.ChallengeCorrect:
		return exitOK
	default:
		return exitMismatch
	}
}

// challengePlay is the interaction: it briefs the player, lends them a shell in
// the challenge node, and takes their answer. It is handed the live network and
// deliberately reads nothing out of it — everything it prints comes from the
// challenge briefing, which is the same information netdoc's run receives.
type challengePlay struct {
	challenge *simulation.Challenge
	// stdin goes to the shell as it is, so the child inherits the terminal
	// itself: a wrapped reader would cost it job control and line editing, and
	// would leave a copier goroutine racing the prompts for the player's keys.
	// in is for the prompts, which read one line at a time from the same fd.
	stdin  io.Reader
	in     *bufio.Reader
	out    io.Writer
	answer string
	giveUp bool
}

func (p *challengePlay) run(ctx context.Context, session *simulation.ChallengeSession) (simulation.ChallengeSubmission, error) {
	if p.giveUp {
		return simulation.ChallengeSubmission{GaveUp: true}, nil
	}
	if p.answer != "" {
		info, _ := simulation.ChallengeAnswerByID(p.answer)
		return simulation.ChallengeSubmission{Answer: info.ID}, nil
	}
	started := time.Now()
	p.challenge.WriteBriefing(p.out)
	if !session.ShellAvailable() {
		fmt.Fprintln(p.out, "\nThis backend cannot open a shell, so answer from what you already know.")
	}
	for {
		if session.ShellAvailable() {
			fmt.Fprintln(p.out, "\nOpening a shell in the challenge node. Type `exit` when you are ready to answer.")
			if err := session.Shell(ctx, p.stdin, p.out, p.out); err != nil {
				return simulation.ChallengeSubmission{}, err
			}
		}
		submission, again, err := p.ask()
		if err != nil {
			return simulation.ChallengeSubmission{}, err
		}
		if again {
			continue
		}
		submission.Elapsed = time.Since(started)
		fmt.Fprintln(p.out, "\nAnswer locked in. Network Doctor is taking its turn on the same network…")
		return submission, nil
	}
}

// ask runs the menu once. It returns again=true when the player wants back into
// the shell, which is the only way out of here that is not a submission.
func (p *challengePlay) ask() (simulation.ChallengeSubmission, bool, error) {
	for {
		simulation.WriteAnswerMenu(p.out)
		fmt.Fprint(p.out, "\n> ")
		line, err := p.in.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))
		if err != nil && choice == "" {
			// No terminal left to ask with. Ending the challenge as a give-up is
			// the honest reading: nobody committed to a diagnosis.
			fmt.Fprintln(p.out, "\n(no answer given)")
			return simulation.ChallengeSubmission{GaveUp: true}, false, nil
		}
		switch choice {
		case "s":
			return simulation.ChallengeSubmission{}, true, nil
		case "q", "quit", "":
			return simulation.ChallengeSubmission{GaveUp: true}, false, nil
		}
		number, convErr := strconv.Atoi(choice)
		if convErr != nil || number < 1 || number > len(simulation.ChallengeAnswerMenu) {
			fmt.Fprintf(p.out, "\nPick a number between 1 and %d, `s` for the shell, or `q` to give up.\n",
				len(simulation.ChallengeAnswerMenu))
			continue
		}
		picked := simulation.ChallengeAnswerMenu[number-1]
		fmt.Fprintf(p.out, "\nYou answered: %s\nWhy? (optional, one line)\n> ", picked.Label)
		note, _ := p.in.ReadString('\n')
		return simulation.ChallengeSubmission{Answer: picked.ID, Note: clipNote(note)}, false, nil
	}
}

func clipNote(raw string) string {
	note := textsafe.Clean(strings.TrimSpace(raw))
	if len(note) > challengeNoteLimit {
		return note[:challengeNoteLimit]
	}
	return note
}
