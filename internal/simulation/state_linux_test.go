// Adversarial cover for kept-simulation cleanup: a record on disk decides
// which process gets signalled and which directory tree gets removed, so every
// test here doctors one and asserts that Release refuses to act on it. The
// sentinel trees and the helper processes are what a tampered record would
// reach if validation were missing or ran too late.

package simulation

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess stands in for a kept simulation's director. It is the same
// binary as the test that starts it, which is what the executable-identity
// check expects of a real director, and it does nothing but wait to be
// signalled. Inert in a normal run.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// stateSandbox points both the record directory and the workspace root at
// throwaway directories, so nothing here can see or touch a real simulation.
func stateSandbox(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
}

// startDirector runs a helper process that passes for this simulation's
// director, and returns it with a record that truthfully describes it.
func startDirector(t *testing.T, id string) (*exec.Cmd, *State) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GO_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start director: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pid := cmd.Process.Pid
	return cmd, &State{
		ID: id, Scenario: "helper", PID: pid, Started: time.Now(),
		Stamp: processStamp(pid), Workspace: workspaceFor(id),
	}
}

// waitSignalled fails unless the director is brought down.
func waitSignalled(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("director was never signalled")
	}
}

// mustStillRun is the assertion behind every refusal: a record that failed
// validation must not have cost this process so much as a signal.
func mustStillRun(t *testing.T, s *State) {
	t.Helper()
	// A signal that should never have been sent is given time to land.
	time.Sleep(200 * time.Millisecond)
	if processStamp(s.PID) != s.Stamp {
		t.Fatalf("pid %d was signalled", s.PID)
	}
}

// makeWorkspace lays down the directory a legitimate run would have left.
func makeWorkspace(t *testing.T, id string) string {
	t.Helper()
	ws := workspaceFor(id)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "node.log"), []byte("scratch"), 0o600); err != nil {
		t.Fatalf("workspace file: %v", err)
	}
	return ws
}

// plant is a directory tree that no correct cleanup may ever reach. dir is
// created if it does not exist, so it can stand either outside the workspace
// root or beside the real workspace under a plausible name.
func plant(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("sentinel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "precious"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("sentinel file: %v", err)
	}
	return dir
}

func makeSentinel(t *testing.T) string {
	t.Helper()
	return plant(t, filepath.Join(t.TempDir(), "not-a-simulation"))
}

func mustSurvive(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "precious")); err != nil {
		t.Fatalf("cleanup removed something it must not touch: %v", err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should still be here: %v", path, err)
	}
}

func mustBeGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should have been swept, stat error was %v", path, err)
	}
}

// save writes the record where LoadState will look for it.
func save(t *testing.T, s *State) {
	t.Helper()
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// A kept simulation whose record matches reality is released: its director is
// signalled, its workspace and its record go, and nothing else moves.
func TestReleaseKeptSimulation(t *testing.T) {
	stateSandbox(t)
	const id = "a1b2c3"
	keep := makeSentinel(t)
	ws := makeWorkspace(t, id)
	cmd, s := startDirector(t, id)
	save(t, s)

	if !s.Alive() {
		t.Fatal("director should be recognised as alive")
	}
	if err := s.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	waitSignalled(t, cmd)
	mustBeGone(t, ws)
	mustBeGone(t, statePath(id))
	mustSurvive(t, keep)
}

// The round trip a real cleanup takes: written by one process, read back by
// another, and released on the strength of what came off disk.
func TestReleaseThroughLoadState(t *testing.T) {
	stateSandbox(t)
	const id = "e1f2a3"
	keep := makeSentinel(t)
	ws := makeWorkspace(t, id)
	cmd, s := startDirector(t, id)
	save(t, s)

	loaded, err := LoadState(id)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if err := loaded.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	waitSignalled(t, cmd)
	mustBeGone(t, ws)
	mustBeGone(t, statePath(id))
	mustSurvive(t, keep)
}

// A director that has already exited is the ordinary stale record: there is
// nothing to signal, and Release still sweeps what it left behind.
func TestReleaseAlreadyExited(t *testing.T) {
	stateSandbox(t)
	const id = "b2c3d4"
	keep := makeSentinel(t)
	ws := makeWorkspace(t, id)
	cmd, s := startDirector(t, id)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()
	save(t, s)

	if s.Alive() {
		t.Fatal("an exited director must not read as alive")
	}
	if err := s.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustBeGone(t, ws)
	mustBeGone(t, statePath(id))
	mustSurvive(t, keep)
}

// A record naming a process that is not a director — with a stamp copied
// straight out of /proc, which is all a tamperer needs to defeat the stamp on
// its own — must not get that process signalled.
func TestReleaseWillNotSignalUnrelatedProcess(t *testing.T) {
	stateSandbox(t)
	const id = "c3d4e5"
	keep := makeSentinel(t)
	makeWorkspace(t, id)

	victim := exec.Command("sleep", "60")
	if err := victim.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	t.Cleanup(func() {
		_ = victim.Process.Kill()
		_ = victim.Wait()
	})
	pid := victim.Process.Pid
	s := &State{ID: id, Scenario: "x", PID: pid, Started: time.Now(),
		Stamp: processStamp(pid), Workspace: workspaceFor(id)}
	save(t, s)

	if s.Alive() {
		t.Fatal("an unrelated process must not read as this simulation's director")
	}
	if err := s.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustStillRun(t, s)
	mustSurvive(t, keep)
}

// A pid no process can ever have is a corrupt record, and a corrupt record is
// not acted on at all — not even the sweep.
func TestReleaseRejectsImpossiblePID(t *testing.T) {
	const id = "d4e5f6"
	for _, pid := range []int{0, -1, -12345} {
		stateSandbox(t)
		ws := makeWorkspace(t, id)
		s := &State{ID: id, Scenario: "x", PID: pid, Started: time.Now(),
			Stamp: "1234", Workspace: workspaceFor(id)}
		save(t, s)
		if err := s.Release(); err == nil {
			t.Fatalf("pid %d: Release should have refused", pid)
		}
		mustExist(t, ws)
		mustExist(t, statePath(id))
	}
}

// A record with no stamp cannot identify its pid at all, so it cannot be
// released either.
func TestReleaseRejectsMissingStamp(t *testing.T) {
	stateSandbox(t)
	const id = "e5f6a7"
	ws := makeWorkspace(t, id)
	_, s := startDirector(t, id)
	s.Stamp = ""
	save(t, s)

	if err := s.Release(); err == nil {
		t.Fatal("Release should have refused a record with no stamp")
	}
	mustExist(t, ws)
	mustExist(t, statePath(id))
}

// Every way a record can point somewhere other than its own workspace. The
// director in each case is genuine, so a Release that validated the workspace
// only after signalling would show up as a killed helper rather than as a
// missing directory.
func TestReleaseRejectsForeignWorkspace(t *testing.T) {
	const id = "f6a7b8"
	cases := map[string]func(t *testing.T) (recorded, protected string){
		"unrelated directory": func(t *testing.T) (string, string) {
			keep := makeSentinel(t)
			return keep, keep
		},
		"another simulation's workspace": func(t *testing.T) (string, string) {
			other := plant(t, workspaceFor("999999"))
			return other, other
		},
		"traversal onto an unrelated directory": func(t *testing.T) (string, string) {
			keep := makeSentinel(t)
			rel, err := filepath.Rel(filepath.Dir(workspaceFor(id)), keep)
			if err != nil {
				t.Fatalf("rel: %v", err)
			}
			return workspaceFor(id) + "/../" + rel, keep
		},
		"traversal onto a sibling simulation": func(t *testing.T) (string, string) {
			other := plant(t, workspaceFor("999999"))
			return workspaceFor(id) + "/../" + filepath.Base(other), other
		},
	}
	for name, setUp := range cases {
		t.Run(name, func(t *testing.T) {
			stateSandbox(t)
			ws := makeWorkspace(t, id)
			recorded, protected := setUp(t)
			_, s := startDirector(t, id)
			s.Workspace = recorded
			save(t, s)

			if err := s.Release(); err == nil {
				t.Fatalf("Release should have refused workspace %q", recorded)
			}
			mustStillRun(t, s)
			mustSurvive(t, protected)
			mustExist(t, ws)
			mustExist(t, statePath(id))
		})
	}
}

// The recorded path can be the right one and still be a trap: a symlink
// standing where the workspace belongs points cleanup at someone else's tree.
func TestReleaseRejectsSymlinkedWorkspace(t *testing.T) {
	stateSandbox(t)
	const id = "a7b8c9"
	keep := makeSentinel(t)
	if err := os.Symlink(keep, workspaceFor(id)); err != nil {
		t.Fatalf("symlink workspace: %v", err)
	}
	_, s := startDirector(t, id)
	save(t, s)

	if err := s.Release(); err == nil {
		t.Fatal("Release should have refused a symlinked workspace")
	}
	mustStillRun(t, s)
	mustSurvive(t, keep)
	mustExist(t, workspaceFor(id))
	mustExist(t, statePath(id))
}

// The id in the record has to be the id of the file it was found in: the id is
// what every path is rebuilt from.
func TestLoadStateRejectsIDMismatch(t *testing.T) {
	stateSandbox(t)
	const id = "b8c9d0"
	other := &State{ID: "999999", Scenario: "x", PID: os.Getpid(), Started: time.Now(),
		Stamp: "1234", Workspace: workspaceFor("999999")}
	save(t, other)
	blob, err := os.ReadFile(statePath("999999"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(statePath(id), blob, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadState(id); err == nil {
		t.Fatal("LoadState should have refused a record naming another simulation")
	}
}

// A record that is really a symlink is not read at all, however innocent the
// file at the far end looks.
func TestLoadStateRejectsSymlink(t *testing.T) {
	stateSandbox(t)
	const id = "c9d0e1"
	record := &State{ID: id, Scenario: "x", PID: os.Getpid(), Started: time.Now(),
		Stamp: "1234", Workspace: workspaceFor(id)}
	save(t, record)
	blob, err := os.ReadFile(statePath(id))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	elsewhere := filepath.Join(t.TempDir(), "planted.json")
	if err := os.WriteFile(elsewhere, blob, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Remove(statePath(id)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Symlink(elsewhere, statePath(id)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := LoadState(id); err == nil {
		t.Fatal("LoadState should have refused a symlinked record")
	}
}

// Save writes records 0600. One that anybody could have edited is not one this
// process is willing to act on.
func TestLoadStateRejectsSharedRecord(t *testing.T) {
	stateSandbox(t)
	const id = "d0e1f2"
	s := &State{ID: id, Scenario: "x", PID: os.Getpid(), Started: time.Now(),
		Stamp: "1234", Workspace: workspaceFor(id)}
	save(t, s)
	if err := os.Chmod(statePath(id), 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := LoadState(id); err == nil {
		t.Fatal("LoadState should have refused a world-writable record")
	}
}

// An id that could climb out of the record directory never reaches the
// filesystem.
func TestLoadStateRejectsUnsafeID(t *testing.T) {
	stateSandbox(t)
	for _, id := range []string{"", "../escape", "a/b", ".", "..", "a b"} {
		if _, err := LoadState(id); err == nil {
			t.Fatalf("LoadState(%q) should have refused", id)
		}
	}
}

// The workspace root is storage every user on the host can write into, so
// anything already sitting at a run's workspace path was put there by somebody
// else. Creation has to fail on all three shapes, and — the part that matters —
// it has to leave what it found exactly as it was: the caller is about to own
// a directory it will later remove recursively.
func TestCreateWorkspaceRefusesAPreExistingPath(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatalf("plant directory: %v", err)
			}
			if err := os.WriteFile(filepath.Join(path, "precious"), []byte("keep"), 0o600); err != nil {
				t.Fatalf("plant file: %v", err)
			}
		}},
		{"file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
				t.Fatalf("plant file: %v", err)
			}
		}},
		{"symlink", func(t *testing.T, path string) {
			if err := os.Symlink(makeSentinel(t), path); err != nil {
				t.Fatalf("plant symlink: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateSandbox(t)
			id := NewID()
			path := workspaceFor(id)
			tc.plant(t, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("lstat: %v", err)
			}
			if work, err := createWorkspace(id); err == nil {
				t.Fatalf("createWorkspace adopted a pre-existing %s at %s", tc.name, work)
			}
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("createWorkspace removed the %s it refused: %v", tc.name, err)
			}
			if !os.SameFile(before, after) {
				t.Fatalf("the %s at %s was replaced", tc.name, path)
			}
		})
	}
}

// The successful case, and the ownership rule that follows from it: the
// workspace is private, and the run that created it is the run that removes it.
func TestCreateWorkspaceOwnsWhatItCreated(t *testing.T) {
	stateSandbox(t)
	id := NewID()
	work, err := createWorkspace(id)
	if err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}
	if work != workspaceFor(id) {
		t.Fatalf("workspace = %s, want %s", work, workspaceFor(id))
	}
	fi, err := os.Lstat(work)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("workspace mode = %v, want a private directory", fi.Mode())
	}
	// The second run to ask for the same id is told no, rather than handed the
	// first one's directory.
	if _, err := createWorkspace(id); err == nil {
		t.Fatal("createWorkspace handed the same workspace out twice")
	}
	mustExist(t, work)
}

// An id that could climb out of the workspace root never reaches the
// filesystem, the same rule LoadState applies to records.
func TestCreateWorkspaceRejectsUnsafeID(t *testing.T) {
	stateSandbox(t)
	for _, id := range []string{"", "../escape", "a/b", ".", "..", "a b"} {
		if work, err := createWorkspace(id); err == nil {
			t.Fatalf("createWorkspace(%q) created %s", id, work)
		}
	}
}

// Prepare is where the exclusive create actually earns its keep. A workspace
// path that is already occupied stops the run before there is an Env, which is
// what keeps Cleanup away from a directory this process never created: an Env
// handed back would have swept it.
func TestPrepareRefusesAnOccupiedWorkspaceAndSweepsNothing(t *testing.T) {
	stateSandbox(t)
	s, err := ParseScenario(strings.NewReader(routedScenario))
	if err != nil {
		t.Fatal(err)
	}
	id := NewID()
	keep := plant(t, workspaceFor(id))

	var log bytes.Buffer
	env, err := (&netnsBackend{dry: true, log: &log}).Prepare(context.Background(), s, id)
	if err == nil {
		t.Fatal("Prepare accepted an occupied workspace path")
	}
	if env != nil {
		t.Fatal("Prepare returned an Env for a workspace it did not create; Cleanup would remove it")
	}
	mustSurvive(t, keep)
}

// The other half of the same rule: a workspace this run did create is its own
// to sweep, and the sweep stops at its own directory.
func TestPrepareCleanupRemovesOnlyItsOwnWorkspace(t *testing.T) {
	stateSandbox(t)
	s, err := ParseScenario(strings.NewReader(routedScenario))
	if err != nil {
		t.Fatal(err)
	}
	id := NewID()
	neighbour := plant(t, workspaceFor(NewID()))

	var log bytes.Buffer
	env, err := (&netnsBackend{dry: true, log: &log}).Prepare(context.Background(), s, id)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	mustExist(t, workspaceFor(id))
	if info := env.Cleanup(context.Background(), false); !info.Done {
		t.Fatalf("cleanup = %+v", info)
	}
	mustBeGone(t, workspaceFor(id))
	mustSurvive(t, neighbour)
}
