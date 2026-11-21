package workspace

// The two seams the roster contract is used through (C1): where the roster node
// is read and written, and where an audit event is created. They are interfaces
// and not the concrete types WU1 already merged (RosterStore, AuditWriter) for
// one reason and one only -- the level-2 tasks that read a roster and write one
// are written in parallel with each other, and each of them has to be testable
// against its own double, with no database, no network and no cloud (the plan's
// hermetic-tests rule). I1 deletes this scaffolding when it adapts the real
// stores onto these interfaces, which is a wrapper of a few lines
// (docs/workspace-projects-users/contract.md says so, and I1's own test is what
// holds it).
//
// What the two sides may rely on is the whole of the contract, and it is short
// enough to state here as well as there:
//
//   - a read answers bytes and the revision they were read at, together, and
//     never one without the other. A read that FAILS is not "no roster": a node
//     nobody could read has no revision for a change to be judged against, and
//     a writer that took the failure for an empty node would overwrite whatever
//     is really there. Every caller refuses on it.
//   - a write is conditional on the revision, always. There is no unconditional
//     write in this contract, because the node is written by several parties
//     (the workspace's own service, its admins, the workspace project's
//     owners) and the ETag is what makes two of them unable to lose each
//     other's work (D2817).
//   - a write that did not land because the node moved under the writer is
//     rtdb.ErrPrecondition, and it is the ANSWER and not a failure: the caller
//     reads the node again and judges its change against what is really there.
//     errors.Is is how a caller tells that answer from a failure, and a failure
//     -- denied, unreachable, a body that is not a document -- is reported
//     rather than retried into.
//   - an absent node reads as Found false with the store's null ETag, and a
//     write under that revision CREATES the node. That is the first write of a
//     workspace, and it is why no `fugaro update` stage seeds a roster (R7):
//     two first writers cannot both create one, because the node carries the
//     null ETag only until the first of them lands.
//   - the bytes a caller writes are a roster document this package encoded
//     (EncodeRoster), and the bytes it reads are one it decodes (DecodeRoster).
//     Neither side interprets them: a store moves bytes and revisions, and the
//     shape of the document is internal/workspace/roster.go's.
//   - an audit event is CREATED and never updated (D2811). An id that is
//     already there is firestore.ErrPrecondition with nothing stored, so a
//     caller that mints an id per event (NewAuditEventID) never loses an entry
//     to a second write of the same document, and a caller that reuses one is
//     told rather than silently overwriting the trail.

import "context"

// Revision is where a roster node stood when it was read: the ETag a
// conditional write is made under, and whether the node was there at all.
//
// A revision comes from Read and from nowhere else. An ETag a caller made up is
// a precondition on a document nobody saw -- which is exactly the overwrite the
// ETag exists to prevent -- and a store refuses an empty one rather than reading
// it as "write unconditionally" (internal/rtdb's PutIfMatch says the same).
//
// Found false is a workspace whose first write has not happened: the bytes are
// absent and ETag is the store's null ETag, which is the revision that creates
// the node.
type Revision struct {
	ETag  string
	Found bool
}

// Store is where one workspace's roster node is: its path, a read that answers
// the document and the revision together, and a write that lands only while the
// node still carries the revision it was read at.
//
// The workspace is the store's, not the method's: a store reads and writes one
// workspace's roster, which is why Read takes no name and why there is no method
// that writes one workspace's roster into another's node.
type Store interface {
	// Path is the roster node's path, relative to the workspace's database
	// root: `workspaceRosters/<name>` (R5), the sibling of the published record
	// and never under it.
	Path() string
	// Read reads the roster document and the revision a write is made under.
	// The bytes are nil and Found is false for a workspace whose roster has
	// never been written, which is the state the first write creates and not an
	// error; a read that fails is an error, and the caller refuses on it.
	Read(ctx context.Context) (Revision, []byte, error)
	// Write writes the roster document, conditionally on rev. A node that moved
	// under this writer is rtdb.ErrPrecondition with nothing written, which the
	// caller answers by reading again; anything else is a failure to report.
	Write(ctx context.Context, rev Revision, roster []byte) error
}

// AuditSink is where a workspace's audit events are created: one document per
// event, created and never updated (D2811).
//
// The id and the event are both the caller's: the id is minted per event
// (NewAuditEventID) and the event is an encoded AuditEvent, and neither side of
// this interface reads the other's bytes. A sink is a workspace's own; a
// member's trail is a different node of a different database, and it is reached
// through the member-side contract (C2) and not through this one.
type AuditSink interface {
	// Create creates the event as the document named id and never updates one.
	// An id that is already there is firestore.ErrPrecondition with nothing
	// stored: an audit entry is never replaced, so a repeat is a refusal the
	// caller sees rather than an entry it silently lost.
	Create(ctx context.Context, id string, event []byte) error
}
