// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// This file is M10's audit record: what one identity's calls come to, in the
// store of D935 and nowhere else (spec.md §5's "the audit record of D920" read
// with the store's own answer of 2026-11-05).
//
// **Why the store and not the project's database.** D935 moved these records
// out of the project's Realtime Database because no rule can keep a launcher, an
// operator or a budget admin out of it (reconciliation.md F32), and the audit
// is one of the records it names. Writing it back into the `mcpauth` node would
// put it behind the database admin grant D935 exists to get away from and leave
// it readable by everyone that ruling found the rules cannot exclude. The node
// stays in the rules as the second barrier, refused whole
// (internal/budget/rules, PathMCPAuth), and TestTheRecordIsNotInTheProjectsDatabase
// is the row that fails the day something writes the audit there again.
//
// **Who reads it, and how.** Through a tool and never through the objects
// (D933): an operator may read one person's record, a launcher does not read it
// at all, and no caller reaches the bucket — its IAM names the front door's own
// account `mcp_server` and no other member, so the store's grant and the tool's
// rule are the same rule read twice rather than two rules that could part. The
// tool is AuditRecordTool; the operator's half of it is stated in the handler,
// which is the one place that sees Call.Role.
//
// **It is a bounded view and not the evidence.** MaxAuditEntries is what one
// identity's record keeps, so a caller that fills it erases its own oldest
// entries and nothing else (the amendment of 2026-11-04: "the bounded
// per-identity audit record can be flushed by spam. M10 therefore treats the
// Cloud Logging line as the durable copy and the per-identity record as the
// readable view"). audit.go's line is written for every event whether or not
// this record takes the entry.

// MaxAuditEntries is how many entries one identity's record keeps: the newest
// ones, oldest dropped. It is a constant and not a configuration because it is
// the shape of a read an operator makes (one screenful of a person's calls),
// and because the bound's only other purpose — keeping one caller from growing
// an object without limit — is served by any bound at all. A record is
// per-identity, so what a caller can grow is its own record and no other.
const MaxAuditEntries = 64

// auditPrefix is where the records live under the store's own prefix. It is
// here rather than beside the other prefixes in store.go because the record is
// this file's: the store's layout is M2's, and this is one more tree in it.
const auditPrefix = PathPrefix + "audit/"

// auditAttempts bounds the read-modify-write below. Two writers of one person's
// record are two calls that person made at once, and a lost round is retried
// rather than refused: the entry is one line of an audit, and dropping it
// because somebody else's call landed first would lose an event that happened.
const auditAttempts = 3

// AuditEntry is one call as the record keeps it: what was called, by which
// token family, with which scopes, and how the door answered.
//
// It carries no token, no code and no secret — a family id names a family and
// is not a value any door accepts — and its fields are the ones the line
// carries plus the two a person reading it needs to act: when the call was
// made, and which family to revoke if the call was not theirs. The family is
// what makes withdrawal reachable from a person's own surface rather than only
// from the API: the record names the family, and RevokeFamilyTool is what ends
// it.
type AuditEntry struct {
	// At is when the door recorded the call, UTC: a record is read beside
	// lines from other hosts and other zones.
	At time.Time `json:"at"`
	// ClientID is the registration the token was issued to, and FamilyID the
	// token family it belongs to (D930). The client is the software; the
	// family is the thing a revocation ends.
	ClientID string `json:"client_id"`
	FamilyID string `json:"family_id"`
	// Tool is the name the call made, which is one the catalogue holds.
	Tool string `json:"tool"`
	// Scopes are the scopes the token carried, verbatim and in the order the
	// token holds them.
	Scopes []string `json:"scopes"`
	// Outcome is what the door did — OutcomeOK, OutcomeRefused, OutcomeFailed —
	// and Reason why, from audit.go's own set. Reason is empty exactly when the
	// outcome is OutcomeOK.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// AuditRecord is one identity's record as the operator's tool answers it: whose
// it is, and the calls it holds, newest first.
type AuditRecord struct {
	// Identity is the exact `user:` entry the record belongs to, carried inside
	// the object rather than in its key: the key is a hash, so the store's own
	// listing names nobody.
	Identity string `json:"identity"`
	// Entries are the calls, newest first, at most MaxAuditEntries of them. It
	// is empty and never null for an identity no call has been served for: an
	// empty list is an answer, and a client that has to tell it from a missing
	// field is reading this tool wrong.
	Entries []AuditEntry `json:"entries"`
}

// auditKey is where one identity's record lives: the audit prefix and the hash
// of the identity, which is the same derivation the store uses for a token
// value and for the same reason — a key is a path in a bucket, and an address
// in a path is an address in every listing, metric and error that names the
// path. The record says whose it is inside itself, so a reader that has the
// object knows and a reader that only lists the store learns nothing.
func auditKey(identity string) (string, error) {
	if _, ok := userEntry(identity); !ok {
		return "", fmt.Errorf("mcpauth: %q is not one person written as user:<email>: an audit record is one person's", identity)
	}
	return auditPrefix + TokenHash(identity), nil
}

// AppendAudit adds one entry to an identity's record, newest first, dropping
// the oldest past MaxAuditEntries.
//
// It is a store method and not a caller's composition of a read and a write for
// the reason RotateRefresh is one: the read that decides the write has to be
// the read the write is matched against. A caller that read the record, put its
// entry at the head and wrote it back would drop every entry another call wrote
// in between — and two calls of one person at once is the ordinary case for a
// client that runs tools in parallel.
//
// A lost round is retried rather than refused (auditAttempts): the record is a
// view, and the line is the copy that cannot be lost (audit.go). Only a record
// that changed under every attempt is an error, because at that point this
// store is not writing what it is asked to write.
func (r records) AppendAudit(ctx context.Context, identity string, entry AuditEntry) error {
	key, err := auditKey(identity)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < auditAttempts; attempt++ {
		rec, gen, err := r.readAudit(ctx, key)
		if err != nil {
			return err
		}
		rec.Identity = identity
		rec.Entries = append([]AuditEntry{entry}, rec.Entries...)
		if len(rec.Entries) > MaxAuditEntries {
			rec.Entries = rec.Entries[:MaxAuditEntries]
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("mcpauth: encoding the audit record: %w", err)
		}
		if gen == 0 {
			if _, err := r.objs.create(ctx, key, data); err != nil {
				if errors.Is(err, errChanged) {
					continue // another call wrote the record first: read it again
				}
				return err
			}
			return nil
		}
		if _, err := r.objs.replace(ctx, key, data, gen); err != nil {
			if errors.Is(err, errChanged) {
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("mcpauth: the audit record changed under every write of it: %w", ErrConflict)
}

// GetAudit reads one identity's record. An identity no call has been served for
// has an empty record — an answer and not an error, because "this person has
// called nothing" is what an operator asking about them wants to know, and a
// refusal would read as a fault of the installation.
func (r records) GetAudit(ctx context.Context, identity string) (AuditRecord, error) {
	key, err := auditKey(identity)
	if err != nil {
		return AuditRecord{}, err
	}
	rec, _, err := r.readAudit(ctx, key)
	if err != nil {
		return AuditRecord{}, err
	}
	rec.Identity = identity
	if rec.Entries == nil {
		rec.Entries = []AuditEntry{}
	}
	return rec, nil
}

// readAudit reads one record and the generation the read saw. An identity with
// no record is no object at all, which is gen 0: the generation that creates.
func (r records) readAudit(ctx context.Context, key string) (AuditRecord, int64, error) {
	data, gen, err := r.objs.read(ctx, key)
	if errors.Is(err, errMissing) {
		return AuditRecord{}, 0, nil
	}
	if err != nil {
		return AuditRecord{}, 0, err
	}
	var rec AuditRecord
	if err := decode(data, &rec); err != nil {
		return AuditRecord{}, 0, fmt.Errorf("%s: %w", key, err)
	}
	return rec, gen, nil
}

// argumentsOf decodes one tool's arguments. It is strict where the door's own
// params decode is not, and the difference is which of the two the strictness
// belongs to: MCP reserves fields inside a request's params (`_meta` on a
// request, for one), while a tool's arguments are its own contract and the
// schema it declares says so with `additionalProperties: false`.
func argumentsOf(args json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("the arguments are not the ones this tool takes: %w", err)
	}
	return nil
}

// The input schemas of M10's two tools. They are the contract a client renders
// its form from, and each says the same as the tool's own handler: one person's
// audit record, and one token family.
const (
	auditRecordInputSchema = `{"type":"object","properties":{"identity":{"type":"string","description":"The person whose record to read, written as user:<email>"}},"required":["identity"],"additionalProperties":false}`
)

// AuditRecordTool is the operator's read of the audit record (D933), ready to
// register: the tool surface the design gives the record in place of a node
// (spec.md §4, §6).
//
// Who may call it is this tool's own rule and not the door's: M7 admits both
// roles to every tool of the catalogue and hands the handler the role to state
// its rule with (Call.Role), so a launcher's call reaches this handler and is
// refused here — with a line of its own in the audit, because a call that
// reached a tool is an event whatever the answer was. A launcher therefore
// cannot read the record by any route: not the objects (the store's IAM is the
// front door's account alone) and not the tool.
//
// A nil store is a wiring mistake and not a state this tool serves in: the
// record is the store's, and the caller that builds this tool is the caller
// that holds the store the door writes through.
func AuditRecordTool(store Store) Tool {
	return Tool{
		Name:        "get_audit_record",
		InputSchema: json.RawMessage(auditRecordInputSchema),
		Handler: func(ctx context.Context, call Call) (any, error) {
			if !call.Role.Operator {
				return nil, ErrOperatorOnly
			}
			var args struct {
				Identity string `json:"identity"`
			}
			if err := argumentsOf(call.Arguments, &args); err != nil {
				return nil, err
			}
			rec, err := store.GetAudit(ctx, args.Identity)
			if err != nil {
				return nil, err
			}
			return rec, nil
		},
	}
}
