// The audit log: what the daemon decided, appended under its own node (O3,
// D1229, D1230).
//
// v1 is level-triggered (D1229): every tick derives what to do from the state
// and the run view, so nothing replays this log and there is no cursor over
// it. What it is for is the question a level-shaped store cannot answer — what
// did the daemon decide, and when, and what did it tell a person — which is
// why the plan keeps a small append-only log beside the state rather than
// reconstructing one from the diffs of a document.
//
// Two rules make it trustworthy, and both are D1230's. The id is **derived**
// (`<subject>:<transition>`), so a producer's retry is a write to the same key
// rather than a second line. The order is **the server's** (rtdb.ServerNow),
// never a laptop's clock, because writer-minted stamps from two machines are
// not totally ordered and a log whose order depends on which laptop wrote it
// cannot be read as a sequence at all.
//
// Nothing here is a decision: an entry records one, and the state is where the
// effect of one lives. A log this package cannot write is reported to the
// caller, which decides whether the decision stands — the log is evidence, not
// the fence.

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// MaxAuditTextRunes caps one entry's free text. An entry is a line, not a
// document: what a model said goes in the run's own record, and what the
// daemon decided goes here.
const MaxAuditTextRunes = 300

// The two names an entry's key is built from. They are stricter than idRE,
// which admits a dot and a colon because a work item's id is a name: this key
// is a database key, where a dot is forbidden and where a colon would separate
// the subject from the transition a second time, so `<a>:<b>` could not be
// read back into the two names that made it.
var (
	auditSubjectRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	auditTransitionRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// PatchDB is the atomic multi-path half of the Realtime Database client. An
// append has no ETag to write under and must not pretend to have one; it is
// separate from CAS so that a writer which only appends does not claim the
// power to replace a node.
type PatchDB interface {
	Patch(ctx context.Context, root string, updates map[string]any) error
}

// Entry is one line of the log: what happened to one subject, when the server
// says it happened, and a short line for the person reading it later.
//
// ID and At are derived by Append, never by the caller: the id from the two
// names, the stamp from the server's clock. A caller that set them would be
// minting the two things this log exists to make trustworthy.
type Entry struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Subject    string    `json:"subject"`
	Transition string    `json:"transition"`
	Text       string    `json:"text,omitempty"`
}

// EntryID is the derived id of one event: the subject and what happened to it,
// so the same event delivered twice is a write to the same key.
func EntryID(subject, transition string) string { return subject + ":" + transition }

// Audit is one orchestrator's log.
type Audit struct {
	db   PatchDB
	path string
	now  ServerNow
}

// NewAudit builds the log of one orchestrator id.
func NewAudit(db PatchDB, agentID string, now ServerNow) (*Audit, error) {
	if db == nil {
		return nil, errors.New("orchestrator: an audit log needs a store to be appended to")
	}
	if now == nil {
		return nil, errors.New("orchestrator: an audit log needs the server's clock")
	}
	path, err := PathAudit(agentID)
	if err != nil {
		return nil, err
	}
	return &Audit{db: db, path: path, now: now}, nil
}

// Path returns the log's path, `orchestrator/<id>/audit`.
func (a *Audit) Path() string { return a.path }

// Append writes one entry under its derived id, overwriting that key if it is
// already there — which is the point: a producer that retries the same
// transition writes the same key, so a reader finds one line per event rather
// than one per delivery. The returned entry carries the id and the server
// stamp this append gave it, and the text as it was actually stored.
//
// A retry therefore re-stamps the line with its own server time. The order is
// the server's either way, and the alternative — reading first to keep the
// first stamp — would make an append a read-modify-write on a shared node with
// nothing to serialise it.
//
// The append is one atomic PATCH of one key: an entry is never half written,
// and nothing else in the node is touched.
func (a *Audit) Append(ctx context.Context, e Entry) (Entry, error) {
	if !auditSubjectRE.MatchString(e.Subject) {
		return Entry{}, fmt.Errorf("%w: the audit subject %q is not a name a key can carry", ErrInvalid, safetext.OneLine(e.Subject))
	}
	if !auditTransitionRE.MatchString(e.Transition) {
		return Entry{}, fmt.Errorf("%w: the transition %q is not a name", ErrInvalid, safetext.OneLine(e.Transition))
	}
	at, ok := a.now()
	if !ok {
		return Entry{}, ErrNoServerTime
	}
	e.ID = EntryID(e.Subject, e.Transition)
	e.At = stamp(at)
	e.Text = cleanLine(e.Text, MaxAuditTextRunes)
	if err := a.db.Patch(ctx, a.path, map[string]any{e.ID: e}); err != nil {
		return Entry{}, err
	}
	return e, nil
}
