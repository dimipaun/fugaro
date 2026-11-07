// The state store: the compare-and-swap the single-writer rule rests on (O3,
// F5, G9).
//
// `orchestrator/<id>` carries three siblings — `state`, `lease` and `audit` —
// and every write in this package goes to one of them and never to the parent:
// a PUT of the parent wipes its children, and one shared ETag would make every
// heartbeat a write to the state node (TestHeartbeatDoesNotMoveTheStateETag).
// The id is escaped with budget.Key, the tree's one escaper, so the node a
// writer names and the node O2's rules grant are the same key.
//
// Nothing here reads a clock, and nothing here calls a model or a network of
// its own: a Node is an ETag, a path and a decision about whether a write may
// happen. That is what makes the fence — the write that must be refused — a
// unit test rather than a cloud experiment.

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The orchestrator subtree. SubtreeRoot is the prefix O2's rules grant and the
// name a reader looks up, so it is one constant rather than four string
// literals. It is deliberately not called PathRoot: that name is the
// coordinator's subtree's (internal/coordpath), and one name for two subtrees
// is the drift the rules cannot see (TestCoordpathIsTheOnlyCopy).
const (
	// SubtreeRoot is the orchestrator's top-level subtree.
	SubtreeRoot = "orchestrator"
	// StateNode, LeaseNode and AuditNode are the three siblings, one of each
	// per orchestrator id.
	StateNode = "state"
	LeaseNode = "lease"
	AuditNode = "audit"
)

// The refusals this node's three siblings share. They wrap like the patch's
// own (patch.go), so a caller tells a lost race from a refusal from an outage.
var (
	// ErrStateMoved is a save refused because another writer moved the node
	// under every attempt. Nothing was written.
	ErrStateMoved = errors.New("orchestrator: the state moved and is not this writer's")
	// ErrLeaseLost is a write (or a beat) refused because this daemon no
	// longer holds the lease: something else owns the orchestrator id now, and
	// a daemon that lost the lease acts on nothing.
	ErrLeaseLost = errors.New("orchestrator: the lease is not this daemon's")
	// ErrLeaseContention is a lease claim refused because the ETag moved under
	// every bounded attempt.
	ErrLeaseContention = errors.New("orchestrator: the lease node moved under every attempt")
	// ErrNotThisNode is a load (or a save) of a document that belongs to
	// another orchestrator id or another loop. The paths are built from an id,
	// so a state read by the wrong loop would be acted on as if it were its
	// own.
	ErrNotThisNode = errors.New("orchestrator: the document belongs to another orchestrator")
	// ErrNoServerTime is a stamp asked for before any reading of the database
	// server's clock. Nothing is written with a laptop's clock instead
	// (D1230): an unavailable server time is a refusal, not a fallback.
	ErrNoServerTime = errors.New("orchestrator: no server time has been read")
)

// saveAttempts bounds the re-read a lost race is answered with. Five is this
// repo's number for that loop (internal/budget's SetAttempts, the mailbox's
// LeaseAttempts, ee/coordinator's writeAttempts): a race that survives five
// re-reads is not a race but another writer, and a store that kept spinning
// would be a daemon that never ticked.
const saveAttempts = 5

// CAS is the compare-and-swap half of the Realtime Database client
// (internal/rtdb). It is an interface because the fence is what this package
// is about: a test holds a daemon to a store it controls, and nothing here
// reaches for a transport of its own.
type CAS interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// ETag identifies the version of the state node a caller read. It is the
// precondition of the next save, and an empty one is refused rather than
// turned into an unconditional write.
type ETag string

// Store is the state node as the CLI (O5), the planner (O6) and the daemon
// (O8) use it.
type Store interface {
	Load(ctx context.Context) (State, ETag, error)
	Save(ctx context.Context, s State, etag ETag) error
}

// ServerNow is the database server's clock (rtdb.Client.ServerNow): the only
// clock a stamp under this subtree may come from, because a laptop's own clock
// is exactly what the lease and the audit's order must not depend on (D1230).
// ok is false before any response has been read.
type ServerNow func() (time.Time, bool)

// LeaseGuard is the lease a writer holds (Lease). Save re-checks it before
// every retry, because the lease and the state ETag are separate nodes: a
// daemon that lost its lease can still win a PutIfMatch after a re-read, and
// only this check stops it (G9).
type LeaseGuard interface {
	Held(ctx context.Context) (bool, error)
}

// NodeOptions is what a state node is built from. It is a struct rather than
// four arguments because the agent id and the role are both names of the same
// shape, and an argument swap between them would build a node that reads
// nothing and refuses everything.
type NodeOptions struct {
	// DB is the store the node is read and written through.
	DB CAS
	// AgentID is the orchestrator id: the `<id>` of `orchestrator/<id>`.
	AgentID string
	// Role is the loop that keeps this node (RoleOrchestrator or
	// RoleCoordinator). A state that says another one is refused
	// (ErrNotThisNode), because two loops sharing an id would read each
	// other's work as their own.
	Role string
	// Lease is the fence this writer holds, or nil for a writer that holds
	// none — a person's own patch (D1241). A writer with no lease makes
	// exactly one attempt per save: see Save.
	Lease LeaseGuard
}

// Node is one orchestrator's state node.
type Node struct {
	db       CAS
	agentID  string
	role     string
	lease    LeaseGuard
	path     string
	attempts int
}

// The store the daemon is handed is this one, and the client it is handed is
// rtdb's: a narrowing of either interface would otherwise be found by the next
// task rather than here.
var (
	_ Store = (*Node)(nil)
	_ CAS   = (*rtdb.Client)(nil)
)

// NewNode builds the state node of one orchestrator id. The id, the role and
// the lease's holder are checked here rather than at the first write, so a
// daemon is refused its own construction rather than a tick.
func NewNode(opts NodeOptions) (*Node, error) {
	if opts.DB == nil {
		return nil, errors.New("orchestrator: a state node needs a store to be read and written through")
	}
	path, err := PathState(opts.AgentID)
	if err != nil {
		return nil, err
	}
	if !roleRE.MatchString(opts.Role) {
		return nil, fmt.Errorf("%w: the role %q is not a loop's name", ErrInvalid, safetext.OneLine(opts.Role))
	}
	attempts := saveAttempts
	if opts.Lease == nil {
		attempts = 1
	}
	return &Node{db: opts.DB, agentID: opts.AgentID, role: opts.Role, lease: opts.Lease, path: path, attempts: attempts}, nil
}

// Path returns the node's path, `orchestrator/<id>/state`.
func (n *Node) Path() string { return n.path }

// Load reads the state node and returns it with the ETag the next save must
// carry.
//
// A node that does not exist yet loads as this orchestrator's empty state at
// this release's schema version, so a daemon's first tick creates the node
// with Save and there is no second, unconditional "create" path. A document
// this release cannot read is refused rather than repaired: the decode is
// strict, an older schema version is raised by Migrate (which the next Save
// then writes), a newer one is refused (ErrSchemaVersion), and a document
// ValidState refuses is refused here too (D1241).
//
// A node another id or role wrote is refused (ErrNotThisNode) and comes back
// as no state at all: fail closed, on the one document a daemon would
// otherwise act on as if it were its own.
func (n *Node) Load(ctx context.Context) (State, ETag, error) {
	var raw json.RawMessage
	etag, found, err := n.db.GetETag(ctx, n.path, &raw)
	if err != nil {
		return State{}, "", err
	}
	if !found {
		return State{SchemaVersion: SchemaVersion, AgentID: n.agentID, Role: n.role}, ETag(etag), nil
	}
	s, err := DecodeState(raw)
	if err != nil {
		return State{}, "", err
	}
	if err := n.owns(s.AgentID, s.Role); err != nil {
		return State{}, "", err
	}
	return s, ETag(etag), nil
}

// Save writes s at the state node, under etag.
//
// The write is conditional, always: an empty ETag is refused rather than
// turned into a PUT, so no path through this method forces a write past the
// node's current version (F5, G9). A 412 is a lost race, and the answer is a
// bounded re-read — at most saveAttempts writes in all, then ErrStateMoved —
// with the lease re-checked before every retry, because a daemon that lost its
// lease can still win a PutIfMatch after a re-read.
//
// A writer that holds no lease gets exactly one attempt. A retry there would
// be a re-read with no fence above it: a person's patch (O5) would overwrite
// whatever landed between its own read and its write, losing a daemon's work
// silently. So the refusal is handed back and the caller re-reads, which is
// what level-triggered means (D1229) — the next tick or the next command
// derives the action again from the state as it now is.
func (n *Node) Save(ctx context.Context, s State, etag ETag) error {
	if err := n.owns(s.AgentID, s.Role); err != nil {
		return err
	}
	raw, err := EncodeState(s)
	if err != nil {
		return err
	}
	if etag == "" {
		return fmt.Errorf("%w: a write with no ETag is a forced write, and this store never forces one", ErrStateMoved)
	}
	var last error
	for attempt := 0; attempt < n.attempts; attempt++ {
		if attempt > 0 {
			if err := n.checkLease(ctx); err != nil {
				return err
			}
			etag, err = n.reload(ctx)
			if err != nil {
				return err
			}
		}
		err = n.db.PutIfMatch(ctx, n.path, string(etag), json.RawMessage(raw))
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return err
		}
		last = err
	}
	return fmt.Errorf("%w: %d writes, each refused by a moved ETag: %w", ErrStateMoved, n.attempts, last)
}

// checkLease is the fence the retry needs. An unreadable lease is a refusal
// too, not a reason to write: a daemon that cannot say whether it still holds
// the orchestrator id is a daemon that does not write the state.
func (n *Node) checkLease(ctx context.Context) error {
	if n.lease == nil {
		return nil
	}
	held, err := n.lease.Held(ctx)
	if err != nil {
		return fmt.Errorf("checking the lease before a retry: %w", err)
	}
	if !held {
		return fmt.Errorf("%w: the state moved and this daemon no longer holds the lease", ErrLeaseLost)
	}
	return nil
}

// reload is the fresh precondition for a retry: the ETag alone, because a
// caller that wanted the state would have loaded it and derived the write
// again rather than pushing the one it built from the old document.
func (n *Node) reload(ctx context.Context) (ETag, error) {
	etag, _, err := n.db.GetETag(ctx, n.path, nil)
	if err != nil {
		return "", err
	}
	return ETag(etag), nil
}

func (n *Node) owns(agentID, role string) error {
	if agentID != n.agentID || role != n.role {
		return fmt.Errorf("%w: the document is %q/%q, this node is %q/%q",
			ErrNotThisNode, safetext.OneLine(agentID), safetext.OneLine(role),
			safetext.OneLine(n.agentID), safetext.OneLine(n.role))
	}
	return nil
}

// PathNode is the parent of the three siblings, `orchestrator/<id>`: the node
// the rules grant and the one a writer must never PUT.
func PathNode(agentID string) (string, error) {
	if err := checkAgentID(agentID); err != nil {
		return "", err
	}
	return SubtreeRoot + "/" + budget.Key(agentID), nil
}

// PathState, PathLease and PathAudit are the three siblings of one
// orchestrator id. They are separate nodes, so a heartbeat moves the lease's
// ETag and nothing else (G9).
func PathState(agentID string) (string, error) { return pathOf(agentID, StateNode) }
func PathLease(agentID string) (string, error) { return pathOf(agentID, LeaseNode) }
func PathAudit(agentID string) (string, error) { return pathOf(agentID, AuditNode) }

func pathOf(agentID, node string) (string, error) {
	parent, err := PathNode(agentID)
	if err != nil {
		return "", err
	}
	return parent + "/" + node, nil
}

// checkAgentID refuses an id a path could not carry. The escaped key is what
// the rules grant and what a reader looks up, so an id that is not a name is
// refused here rather than escaped into a node nobody grants.
func checkAgentID(agentID string) error {
	if !agentRE.MatchString(agentID) {
		return fmt.Errorf("%w: the agent id %q is not a name", ErrInvalid, safetext.OneLine(agentID))
	}
	return nil
}
