package simulation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// State is the record a kept simulation leaves behind so another process can
// find, inspect and release it. A run that is not kept writes none: its
// namespaces die with its process tree, so there is nothing to record.
type State struct {
	ID       string    `json:"id"`
	Scenario string    `json:"scenario"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	// Stamp identifies the process behind PID across pid reuse. Without it,
	// releasing a simulation whose director has since exited would signal
	// whichever unrelated process inherited the number.
	Stamp     string     `json:"stamp"`
	Workspace string     `json:"workspace"`
	Nodes     []NodeInfo `json:"nodes"`
}

// NewState records the calling process as the holder of a kept simulation.
func NewState(id, scenario, workspace string, started time.Time, nodes []NodeInfo) *State {
	pid := os.Getpid()
	return &State{
		ID: id, Scenario: scenario, PID: pid, Started: started,
		Stamp: processStamp(pid), Workspace: workspace, Nodes: nodes,
	}
}

// StateDir is where kept-simulation records live. The per-user runtime
// directory is the right home for them: it is private, and it is emptied on
// logout, which matches how long a simulation can possibly survive.
func StateDir() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "netdoc-sim")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("netdoc-sim-%d", os.Getuid()))
}

func statePath(id string) string { return filepath.Join(StateDir(), id+".json") }

// Save writes the record for a kept simulation.
func (s *State) Save() error {
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(s.ID), blob, 0o600)
}

// LoadState reads one simulation's record.
func LoadState(id string) (*State, error) {
	if !isSafeName(strings.TrimSuffix(id, ".json")) {
		return nil, fmt.Errorf("%q is not a simulation id", id)
	}
	blob, err := os.ReadFile(statePath(id))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", statePath(id), err)
	}
	return &s, nil
}

// ListStates returns every recorded simulation, newest first. Records whose
// process is gone are returned too — Alive tells them apart, and Release is how
// their leftovers get swept up.
func ListStates() ([]*State, error) {
	entries, err := os.ReadDir(StateDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*State
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		s, err := LoadState(id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// Alive reports whether the process holding this simulation's namespaces is
// still running. The cmdline check guards against a recycled pid: killing an
// unrelated process because its number was reused would be the worst bug this
// package could have.
func (s *State) Alive() bool {
	if s.PID <= 0 || s.Stamp == "" {
		return false
	}
	return processStamp(s.PID) == s.Stamp
}

// Release ends a kept simulation: the director is asked to stop, which takes
// its namespaces and every holder with it, then the leftovers on disk go.
// Idempotent — releasing an already-dead simulation just sweeps its files.
func (s *State) Release() error {
	var errs []string
	if s.Alive() {
		if err := stopProcess(s.PID, false); err != nil {
			errs = append(errs, fmt.Sprintf("signal pid %d: %v", s.PID, err))
		}
		// The director cleans up on SIGTERM; give it a moment, then insist.
		for i := 0; i < 40 && s.Alive(); i++ {
			time.Sleep(50 * time.Millisecond)
		}
		if s.Alive() {
			if err := stopProcess(s.PID, true); err != nil {
				errs = append(errs, fmt.Sprintf("kill pid %d: %v", s.PID, err))
			}
		}
	}
	if s.Workspace != "" && strings.HasPrefix(filepath.Base(s.Workspace), "netdoc-sim-") {
		if err := os.RemoveAll(s.Workspace); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if err := os.Remove(statePath(s.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
