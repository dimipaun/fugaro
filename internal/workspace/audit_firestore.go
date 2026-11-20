package workspace

// The workspace's audit trail, in the workspace's own Firestore (D2811): one
// document per event, created and never updated. It is where a workspace admin's
// change — a member added, a grant written, a domain edited, an admin's binding
// taken away — is recorded with who did it and when; a member's own trail is
// written into that member's database instead (WU4), because a project admin
// holds no write on the workspace's project at all (F11, D2813).
//
// Create-only is a property of the write and not a convention: every document is
// created under Firestore's `currentDocument.exists=false` precondition, so an id
// that is already there is firestore.ErrPrecondition with nothing stored. An
// audit entry is therefore never replaced — a second write of one id is a refusal
// the caller sees, and the entry it did not write is the one that is lost.
//
// What that does NOT claim is that the trail cannot be tampered with, and the
// threat model says so in as many words (threat-model.md §3, §7 item 5): whoever
// holds the workspace project's IAM writes this database with the rules bypassed
// — IAM reaches it, a client SDK does not — so an entry can be deleted or
// rewritten by an admin or a project owner. The property the
// design rests on is that Fugaro never updates one, and the cross-check against
// Cloud Audit Logs — where a SetIamPolicy whose resulting binding is drift and
// which no trail of Fugaro's holds is a `status` row (D2811, R13, WU12) — is
// what makes a change that no Fugaro command made visible.
//
// The collection is closed to every client: the Firestore rules every
// installation deploys deny every read and write (infra.FirestoreRules), so only
// an IAM principal reaches it, exactly as the spend history beside it is reached.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dimipaun/fugaro/internal/firestore"
)

// AuditCollection is the Firestore collection the workspace's audit events are
// created in, in the workspace's own project's database.
const AuditCollection = "workspaceAudit"

// AuditWriter creates the workspace's audit documents through db, a client of
// the workspace's own project's Firestore.
type AuditWriter struct {
	db *firestore.Client
}

// NewAuditWriter returns a writer of the workspace's trail.
func NewAuditWriter(db *firestore.Client) *AuditWriter { return &AuditWriter{db: db} }

// Create writes event as the audit document id in AuditCollection and never
// updates one. The event is one JSON object — C1's AuditEvent, and this file
// reads no field of it — and a number in it is stored as a Firestore double (the
// client's one conversion), every other JSON value as itself.
//
// A document id that is already there is firestore.ErrPrecondition and nothing is
// written, which is the whole of "create-only": making ids unique is the caller's
// (C1's event carries what names one), and a repeat is a refusal rather than a
// replacement either way.
//
// An event that is not a JSON object, or one carrying no field at all, is refused
// here rather than stored: a document with no fields is not an entry, and a
// Firestore write of one is refused by the client with a sentence that names
// neither the event nor the trail.
func (w *AuditWriter) Create(ctx context.Context, id string, event []byte) error {
	var fields map[string]any
	if err := json.Unmarshal(event, &fields); err != nil {
		return fmt.Errorf("workspace: the audit event is not a JSON object: %w", err)
	}
	if len(fields) == 0 {
		return fmt.Errorf("workspace: the audit event for %s carries no field, so there is no entry to create", id)
	}
	if _, err := w.db.Patch(ctx, AuditCollection, id, fields, firestore.MustNotExist()); err != nil {
		return fmt.Errorf("creating the audit event %s in %s: %w", id, AuditCollection, err)
	}
	return nil
}
