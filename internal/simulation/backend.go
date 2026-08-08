package simulation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// ErrUnsupported is returned by a backend that cannot run on this host. It is a
// capability answer, not a failure: the caller prints Capabilities.Reason.
var ErrUnsupported = errors.New("simulation backend unsupported on this platform")

// Backend builds virtual networks. The only implementation today is Linux
// network namespaces; the interface exists so a container, libvirt or
// platform-native backend can be added without the runner noticing.
type Backend interface {
	// Name is the short backend id used in reports.
	Name() string
	// Capabilities reports whether this host can run a simulation, and which
	// privileged operations a run performs. Cheap and side-effect free.
	Capabilities(ctx context.Context) Capabilities
	// Prepare builds the topology. It returns a usable Env, or an error and —
	// when setup got far enough to create anything — a non-nil Env that must
	// still be cleaned up. Callers must handle both being non-nil.
	Prepare(ctx context.Context, s *Scenario, id string) (Env, error)
}

// Env is one live simulated network.
type Env interface {
	// Nodes describes what was actually created.
	Nodes() []NodeInfo
	// ApplyFaults injects impairments into an already-healthy topology.
	ApplyFaults(ctx context.Context, faults []Fault) ([]FaultInfo, error)
	// ApplyTimedEvent applies one already-validated scheduled change while the
	// tests are running, and returns what the kernel says the new state is.
	// Only the fault scheduler calls it, and only before Cleanup begins — the
	// runner joins the scheduler first.
	ApplyTimedEvent(ctx context.Context, event TimedEvent) (string, error)
	// Exec runs argv inside a node's namespaces. argv is passed to the kernel
	// as a slice; no shell is involved anywhere in this package.
	Exec(ctx context.Context, node string, argv, env []string) ExecResult
	// TrustAnchor returns the simulator-generated public CA bundle for a
	// validated TLS service. Scenario files never supply this path.
	TrustAnchor(service string) (string, error)
	// Evidence reads structured service observations collected so far.
	Evidence(ctx context.Context) (Evidence, error)
	// Cleanup releases everything. It is idempotent, safe after a partial
	// Prepare, and reports errors rather than swallowing them. With keep set it
	// leaves the environment running and says how to reach it.
	Cleanup(ctx context.Context, keep bool) CleanupInfo
}

// ExecResult is the outcome of one command run inside a node.
type ExecResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool
	Cancelled bool
	Signal    string
	Err       error
}

// Capabilities answers "can this host simulate, and what will it cost me".
type Capabilities struct {
	Backend   string `json:"backend"`
	Supported bool   `json:"supported"`
	// Reason explains an unsupported host in terms the user can act on.
	Reason string `json:"reason,omitempty"`
	// Missing lists required executables that were not found in PATH.
	Missing []string `json:"missing,omitempty"`
	// Privileged lists the operations a run performs, so a user can read the
	// blast radius before granting anything.
	Privileged []string `json:"privileged,omitempty"`
}

// runIDBytes is how much randomness a simulation id carries. An id names a
// directory and a record in shared temporary storage, where colliding with
// another run — or with something a stranger pre-created under a guessed name
// — is the whole hazard, so it is wide enough that neither is a case anyone
// has to reason about. Hex keeps the result inside isSafeName.
const runIDBytes = 12

// NewID returns a unique id for one simulation. Every namespace, interface and
// state file derives from it, so two concurrent runs — or a run started while
// an abandoned one is still around — cannot collide.
func NewID() string {
	var b [runIDBytes]byte
	// crypto/rand.Read fills b or the process dies trying: it has not returned
	// an error since Go 1.24, so there is no degraded id worth falling back to.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
