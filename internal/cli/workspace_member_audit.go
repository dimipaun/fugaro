package cli

// workspace_member_audit.go is WU4's audit writer: the member's half of the
// trail `workspace audit` is a document of, and the one write in this task that
// is not conditional on an ETag a caller read.
//
// **It is create-only** (D2811). The entry is created under its own id with the
// null ETag as the precondition, so a second write of one id is a refusal and
// the entry that is there is left exactly as it was: the trail says what
// happened, and an entry that could be rewritten would be a record of whatever
// the last writer preferred. There is no update path and no delete path, in this
// file or in the contract it satisfies.
//
// That is also why the event is validated BEFORE anything is written, and with
// C1's own reader (`workspace.DecodeAuditEvent`): an entry that cannot be read
// back cannot be corrected afterwards either, so an unreadable one must never be
// created. The bytes the caller hands over are read by that decoder rather than
// passed through, which is stricter than the node needs and cheaper than a
// document nobody can answer questions from.
//
// **The id is the key, and the event's own id must be it.** An id carrying a `/`
// would write a nested path — `audit/<id>/…` the trail does not have — and an
// event filed under an id that is not its own is a record whose name and content
// disagree, which no reader can resolve. C1's grammar (`validateAuditEventID`)
// is what refuses the first, and it is stricter than the database's own key
// rules; the equality check here is what refuses the second, and it is here
// because C1 cannot see the id the caller filed under.
//
// **The path is already denied to clients** (R7): the member's `config/admin`
// rules deny every client read and write, as the workspace's own rules do, so
// this node is reached by IAM alone. Nothing in this file writes a rule.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// memberAuditNullETag is the precondition that CREATES a node (rtdb's GetETag
// answers it for an absent one, and access.Node's own doc names the literal).
// rtdb exports no constant for it, and a write under an ETag a caller read is
// the only other kind this program makes.
const memberAuditNullETag = "null_etag"

// Audit creates one entry in the member's trail at workspace.PathMemberAudit.
//
// event is C1's workspace.AuditEvent as JSON, and this call reads it with C1's
// own reader: the trail is create-only, so a document that reader refuses is
// refused here rather than stored where nothing could correct or understand it.
// id must be the event's own id — it is the key the entry is created at — and a
// second create of one id is a refusal the caller sees, with the first entry
// untouched.
func (o *WorkspaceMemberOps) Audit(ctx context.Context, m workspace.MemberTarget, id string, event []byte) error {
	ev, err := workspace.DecodeAuditEvent(event)
	if err != nil {
		return userErr("the entry written to member project %s's audit trail is not an event this program can read back: %v. Nothing was written: the trail is create-only (D2811), so an entry that cannot be read is one that can never be corrected", safetext.Field(m.Project), err)
	}
	if ev.ID != id {
		return userErr("the entry written to member project %s's audit trail is filed under %q and its own id is %q. Nothing was written: the id is the key the entry is created at, and an entry whose name and content disagree is one no reader can resolve", safetext.Field(m.Project), id, ev.ID)
	}
	db, err := o.memberDatabase(ctx, m)
	if err != nil {
		return err
	}
	path := workspace.PathMemberAudit + "/" + id
	if err := db.PutIfMatch(ctx, path, memberAuditNullETag, json.RawMessage(bytes.TrimSpace(event))); err != nil {
		return memberAuditErr(m, id, err)
	}
	return nil
}

// memberAuditErr classifies a refused create. The trail's own refusal is the one
// that matters and it is named rather than described: a precondition here means
// the id is taken, so the caller has a NEW event to make (a fresh id) and not a
// retry of this one.
func memberAuditErr(m workspace.MemberTarget, id string, err error) error {
	switch {
	case errors.Is(err, rtdb.ErrPrecondition):
		return userErr("member project %s's audit trail already holds an entry with the id %s, and nothing was written: the trail is create-only (D2811), so an entry that is there is what happened and is never rewritten (%v). An event about a new change is a new entry, with an id of its own", safetext.Field(m.Project), safetext.Field(id), err)
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("member project %s's database refused the write to its audit trail: a workspace admin writes it through the authority over the member (the folder that holds it, or the workspace IAM account's roles/owner) (%w)", safetext.Field(m.Project), err)
	default:
		return remote(err)
	}
}
