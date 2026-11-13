// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's audit log (portal plan P2.1, P5.1; D456, F9).
//
// Every operations action is written here, reads included, and it is written by
// the act that does it: wire_audit.go says what an entry is, and this file is the
// only writer an area reaches. What the log exists for is the one question the
// portal cannot answer from its own records — who looked at this? — and an entry
// a reader writes, or one the actor writes after it has answered, answers neither
// that question nor "who changed this?".
//
// **An entry is never edited and never deleted.** Nothing here updates or removes
// one; the collection is written by this service alone, and what a deployment
// holds that with is its own rules (P1.2, P1.7).
//
// **A line that cannot name its actor is refused rather than written.** Every
// entry is filed by the uid and the address the sign-in proved, which is what a
// membership is filed by too, and an entry with neither answers nothing: it is a
// read somebody made that nobody is answerable for. The guard is here rather than
// in the store because a deployment's store is not the place to find out that a
// caller arrived without an account.

import (
	"context"
	"errors"
	"strings"
)

// auditCollection is the collection the log's entries live in. It is this
// service's own name and not the deploy's, for the reason the tenancy's two are
// (orgs.go).
const auditCollection = "audit"

// The actions this area writes, spelled `noun.verb` as wire_audit.go requires:
// one per read the operations membership widens (accessTo). P5.1's own actions
// are named in its file and beside these in the log.
const (
	actionOrganisationRead        = "organisation.read"
	actionOrganisationMembersRead = "organisation.members.read"
)

// auditEntry is one action before the service has named it: what was done, and to
// what. The id, the instant and the actor are not here because they are the
// writer's — an actor taken from a body would be an actor the caller chose, which
// is the one thing a log cannot have.
type auditEntry struct {
	// Action is the operation's own name, `noun.verb`.
	Action string
	// OrganisationID is the organisation the action used or acted on, and nil
	// for one that named none.
	OrganisationID *string
	// TargetID is what the action acted on within the organisation, and nil for
	// an action with no single target.
	TargetID *string
	// Read is true for an action that changed nothing.
	Read bool
	// Detail is what the actor wrote about the action, or nil. It is free text a
	// person typed and it is data on render and never markup.
	Detail *string
}

// errAuditNeedsAnActor is a line this service will not write: an entry that
// nothing and nobody is answerable for.
var errAuditNeedsAnActor = errors.New("keyservice: an audit entry with no actor answers nothing")

// recordAudit writes one entry, and answers the failure of doing so rather than
// swallowing it: what calls this is the act the entry describes, and an act that
// carries on after its line was not written is the one thing the log exists to
// prevent (accessTo).
func (s *Service) recordAudit(ctx context.Context, actor Caller, entry auditEntry) error {
	if actor.UID == "" || strings.TrimSpace(actor.Email) == "" || strings.TrimSpace(entry.Action) == "" {
		return errAuditNeedsAnActor
	}
	id, err := newID()
	if err != nil {
		return err
	}
	doc, err := storedDoc(WireAuditEntry{
		ID:             id,
		At:             s.now(),
		ActorUID:       actor.UID,
		ActorEmail:     actor.Email,
		OrganisationID: entry.OrganisationID,
		Action:         entry.Action,
		TargetID:       entry.TargetID,
		Read:           entry.Read,
		Detail:         entry.Detail,
	})
	if err != nil {
		return err
	}
	return s.docs.Put(ctx, auditCollection, id, doc)
}
