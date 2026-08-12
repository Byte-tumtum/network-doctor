package simulation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ChallengeOptions configures one challenge run.
type ChallengeOptions struct {
	Run Options
	// NetdocVersion is what Run.Netdoc answered for -version, asked of that same
	// executable by whoever resolved it. Only the version travels: the path in
	// the result is read straight back off Run.Netdoc, so the binary a result
	// names cannot drift from the binary the run launched.
	NetdocVersion string
	// Play is handed the live network once every fault is in place and before
	// any netdoc process starts, and returns the diagnosis the person committed
	// to. A nil Play submits nothing, which is how a non-interactive answer runs.
	Play func(context.Context, *ChallengeSession) (ChallengeSubmission, error)
}

// ChallengeSession is the live network, while the player has it. It exposes the
// two things an investigation needs — where to stand and how to get a shell
// there — and nothing that would answer the question for them.
type ChallengeSession struct {
	Challenge *Challenge
	env       Env
	shellEnv  []string
}

// interactiveEnv is the subset of a backend that can hand a terminal to a
// process inside a node. Only the namespace backend implements it; a backend
// that cannot is a challenge without a shell, not a broken challenge.
type interactiveEnv interface {
	ExecInteractive(ctx context.Context, node string, argv, env []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// ShellAvailable reports whether this backend can open a shell in the node.
func (s *ChallengeSession) ShellAvailable() bool {
	_, ok := s.env.(interactiveEnv)
	return ok
}

// Shell runs an interactive shell inside the challenge node's network and mount
// namespaces, with the caller's terminal attached. It is the same nsenter the
// simulator already uses to run netdoc there, so the player sees exactly the
// network the diagnosis will be made against — the node's own routes, its own
// /etc/resolv.conf, and the simulator's trust anchors where the netdoc run gets
// them too.
//
// The shell is not given a new process group: an interactive shell claims the
// terminal for itself, which is what keeps Ctrl-C inside the challenge from
// reaching the simulator holding the network open.
func (s *ChallengeSession) Shell(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	interactive, ok := s.env.(interactiveEnv)
	if !ok {
		return errors.New("this backend cannot open a shell inside a node")
	}
	return interactive.ExecInteractive(ctx, s.Challenge.Node, []string{loginShell(), "-i"},
		append([]string{"NETDOC_CHALLENGE=" + s.Challenge.ID}, s.shellEnv...), stdin, stdout, stderr)
}

// loginShell picks the player's shell, falling back to the one every POSIX host
// has. An absolute path is required because this is passed to execve, not to a
// shell — there is no PATH lookup and no interpretation.
func loginShell() string {
	if shell := os.Getenv("SHELL"); filepath.IsAbs(shell) {
		return shell
	}
	return "/bin/sh"
}

// RunChallenge builds the challenge network, hands it to the player, runs the
// real netdoc in it, and scores both answers against the simulator's own
// observations.
//
// netdoc runs through the ordinary simulation path with the ordinary arguments:
// it is never told a challenge is happening, never handed a mutation manifest
// or oracle evidence, and its probes are not selected from the hidden answer.
// A non-nil error means no matchup took place.
func RunChallenge(ctx context.Context, c *Challenge, backend Backend, opts ChallengeOptions) (*ChallengeResult, error) {
	if c == nil || c.Scenario == nil {
		return nil, errors.New("challenge has no scenario")
	}
	if backend == nil {
		return nil, errors.New("challenge backend is nil")
	}
	var submission ChallengeSubmission
	var playErr error
	run := opts.Run
	run.Hold = func(ctx context.Context, env Env) error {
		if opts.Play == nil {
			return nil
		}
		session := &ChallengeSession{Challenge: c, env: env}
		if primary, ok := primaryTest(c.Scenario); ok {
			// Whatever trust the netdoc run gets, the player gets: a certificate
			// the simulator generated has to be verifiable by both, or one
			// contestant is looking at a different network.
			session.shellEnv, _ = probeTrustEnv(env, primary)
		}
		got, err := opts.Play(ctx, session)
		if err != nil {
			playErr = err
			return err
		}
		submission = got
		return nil
	}
	report := Run(ctx, c.Scenario, backend, run)
	if playErr != nil {
		return nil, playErr
	}
	if fingerprint := huntCaseFingerprint(c.Manifest); fingerprint != c.Manifest.CaseFingerprint {
		// The manifest the player was given and the one being scored have to be
		// the same case. Nothing mutates it in between, and this is what keeps
		// that a checked fact rather than a comment.
		return nil, fmt.Errorf("challenge %s: case fingerprint changed during the run", c.ID)
	}
	result := ScoreChallenge(c, report, submission)
	// run.Netdoc is the string every netdoc argv in this run was built from, so
	// recording it here is recording what actually executed rather than what was
	// separately looked up and hoped to be the same thing.
	result.Netdoc = NetdocIdentity{Path: run.Netdoc, Version: opts.NetdocVersion}
	return result, nil
}
