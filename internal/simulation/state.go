package simulation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// workspaceFor is where the run with this id keeps its scratch directory.
// Deriving it from the id alone is what lets cleanup reconstruct the path
// instead of trusting the one a record happens to carry.
func workspaceFor(id string) string {
	return filepath.Join(os.TempDir(), "netdoc-sim-"+id)
}

// createWorkspace makes this run's scratch directory, and says so only when it
// was this call that made it. That is the whole point of the plain Mkdir: the
// workspace sits in storage every user on the host can write to, so a
// directory, file or symlink already occupying the path means someone else got
// there first, and adopting it would hand this run — and later the recursive
// removal in Cleanup — an object it never created. The collision is reported
// and nothing is deleted; only the shared parent is created if it is missing.
func createWorkspace(id string) (string, error) {
	if !isSafeName(id) {
		return "", fmt.Errorf("%q is not a simulation id", id)
	}
	work := workspaceFor(id)
	if err := os.MkdirAll(filepath.Dir(work), 0o700); err != nil {
		return "", err
	}
	if err := os.Mkdir(work, 0o700); err != nil {
		return "", fmt.Errorf("simulation %s: workspace: %w", id, err)
	}
	return work, nil
}

// Save writes the record for a kept simulation. The record is this process's
// claim on the id, so it is created exclusively: an id that already has a
// record is a collision to report, never a file to truncate, and O_EXCL also
// refuses the symlink somebody may have left where the record belongs — the
// kernel does not follow the final component when it is set.
func (s *State) Save() error {
	if !isSafeName(s.ID) {
		return fmt.Errorf("%q is not a simulation id", s.ID)
	}
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(statePath(s.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(blob); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// LoadState reads one simulation's record. The id the caller asked for is the
// only one that ever reaches the filesystem, and the decoded record has to
// agree with it: releasing acts destructively on paths derived from the id, so
// a record is not allowed to name a different simulation than the file it was
// found in.
func LoadState(id string) (*State, error) {
	id = strings.TrimSuffix(id, ".json")
	if !isSafeName(id) {
		return nil, fmt.Errorf("%q is not a simulation id", id)
	}
	path := statePath(id)
	blob, err := readStateFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.ID != id {
		return nil, fmt.Errorf("%s: record claims to be simulation %q", path, s.ID)
	}
	return &s, nil
}

// readStateFile reads a record without following anything into it. The path is
// inspected before it is opened and the open file is confirmed to be that same
// object, so a symlink swapped in between the two calls cannot hand cleanup a
// file it never looked at.
func readStateFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: is a symlink, not a simulation record", path)
	}
	// #nosec G304 -- LoadState validated the ID and lstat rejects symlinks before opening.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, fi) {
		return nil, fmt.Errorf("%s: was replaced while it was being read", path)
	}
	if err := checkStateFile(path, fi); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
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
// still running. Two things have to hold, because killing an unrelated process
// would be the worst bug this package could have. The stamp catches a recycled
// pid, and the executable check catches the case the stamp cannot: a stamp is
// readable out of /proc by anything that can also doctor the record, so on its
// own it proves nothing about which program is behind the number.
func (s *State) Alive() bool {
	if s.PID <= 0 || s.Stamp == "" {
		return false
	}
	return processStamp(s.PID) == s.Stamp && sameExecutable(s.PID)
}

// validate proves from the record alone that releasing it can only touch this
// simulation, and returns the workspace it may remove ("" for a record that
// never had one). Everything destructive depends on this, so it runs to
// completion before the first signal or removal: a record that fails it is
// left exactly as it was found.
func (s *State) validate() (string, error) {
	if !isSafeName(s.ID) {
		return "", fmt.Errorf("%q is not a simulation id", s.ID)
	}
	if s.PID <= 0 {
		return "", fmt.Errorf("simulation %s: record names pid %d, which is no process", s.ID, s.PID)
	}
	if s.Stamp == "" {
		return "", fmt.Errorf("simulation %s: record has no process stamp, so pid %d cannot be identified", s.ID, s.PID)
	}
	if s.Workspace == "" {
		return "", nil
	}
	want := workspaceFor(s.ID)
	if filepath.Clean(s.Workspace) != want {
		return "", fmt.Errorf("simulation %s: record points at workspace %q, but this run's workspace is %s", s.ID, s.Workspace, want)
	}
	// Reconstructing the path is not enough on its own: a symlink or a plain
	// file sitting where the workspace belongs means somebody has been here,
	// and cleanup is about to recurse through whatever it finds.
	if fi, err := os.Lstat(want); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("simulation %s: %s is not a directory", s.ID, want)
	}
	return want, nil
}

// Release ends a kept simulation: the director is asked to stop, which takes
// its namespaces and every holder with it, then the leftovers on disk go.
// Idempotent — releasing an already-dead simulation just sweeps its files.
//
// A record that does not survive validation is not swept at all. Nothing is
// signalled unless the pid is provably still this simulation's director; a pid
// that is gone, or recycled by some other program, is simply left alone while
// the reconstructed leftovers go.
func (s *State) Release() error {
	workspace, err := s.validate()
	if err != nil {
		return err
	}
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
	if workspace != "" {
		if err := os.RemoveAll(workspace); err != nil {
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
