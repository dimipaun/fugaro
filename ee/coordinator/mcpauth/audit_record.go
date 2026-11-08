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

// auditAttempts bounds the read-modify-write below. It is the record's own
// bound and not a small count, because a lost round is a lost entry and the
// number of rounds a writer needs is the number of calls of that person that
// interleave with it: the loser of a round re-reads and writes against the
// newer generation, so it needs about one attempt for each writer that lands
// first. A client that runs tools in parallel is the ordinary case this loop
// exists for — audit_record.go's own comment says so — which makes a small
// count lose an entry on arithmetic rather than under load: three attempts
// lose the fourth call of a client running four tools at once, and the fourth
// call is no less a call than the others.
const auditAttempts = MaxAuditEntries

// auditGapSuffix names the marker a record keeps beside itself when an entry it
// should hold was lost. It is a second object and not a field of the record's
// own for the one reason a field cannot serve: the write that lost the entry is
// the write that could not touch the record. The key cannot collide with a
// record's own, which is the audit prefix and a hash.
const auditGapSuffix = ".gap"

// auditGapMarker is what the marker above holds: the same claim GetAudit
// answers as AuditRecord.Incomplete, written where a reader of the bucket can
// see it too.
var auditGapMarker = []byte(`{"incomplete":true}`)

// auditGapTimeout bounds the marker's own write, which is made on a context of
// its own (markAuditGap) and has no caller left to bound it: a write that
// reports a loss must not be the one that holds an answer open.
const auditGapTimeout = 2 * time.Second

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
	// the object rather than in its key. That is a shape and not a hiding place:
	// the key is a hash of this same value, so a reader who can guess the value
	// reaches the object (auditKey).
	Identity string `json:"identity"`
	// Entries are the calls, newest first, at most MaxAuditEntries of them. It
	// is empty and never null for an identity no call has been served for: an
	// empty list is an answer, and a client that has to tell it from a missing
	// field is reading this tool wrong.
	Entries []AuditEntry `json:"entries"`
	// Incomplete is true when this record is known to be short: a call the door
	// audited and could not place here (AppendAudit's own bound). It is the one
	// difference between a record that lost an entry and a record of a person
	// who has called this many times, which is why it is answered beside the
	// entries rather than inside them — a reader that has to tell the two apart
	// by counting would take a short history for a whole one. It is sticky: the
	// entry is gone and the record never stops saying so. The line is the copy
	// that cannot be lost (audit.go), so a record that says this sends its
	// reader to the log rather than to another read of the same object.
	Incomplete bool `json:"incomplete"`
}

// auditKey is where one identity's record lives: the audit prefix and the hash
// of the identity, which is the same derivation the store uses for a token
// value and for the same reason — a key is a path in a bucket, and an address
// in a path is an address in every listing, metric and error that names the
// path. The record says whose it is inside itself, so a reader that has the
// object knows whose it is.
//
// **This key hides less than a token's, and what it hides it hides by
// guesswork.** The value hashed here is a `user:<email>` entry a person wrote,
// not NewValue's 32 random bytes: a reader who can list this store has a
// dictionary of addresses, so a walk of it confirms which identities have
// called here, and the object's own Identity then names the person outright.
// What the hash still buys is that no key of this store carries an address as a
// path. What would buy the walk away is the keyed hash D922's amendment designs
// — and it is specified over the token records, so when it lands this one is
// keyed with that same pepper beside them, which is the thing to keep in mind
// there.
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
// store is not writing what it is asked to write — and then the entry is gone
// (auditCall answers the call anyway, which the amendment of 2026-11-04
// decides), so the one thing this method owes the reader who comes next is that
// the record does not go on looking whole: the loss is marked beside it.
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
	r.markAuditGap(ctx, key)
	return fmt.Errorf("mcpauth: the audit record changed under every write of it: %w", ErrConflict)
}

// markAuditGap records, beside the record, that an entry it should hold is not
// in it. It is a create and its failure is dropped: the first loss writes the
// marker and a later one finds it already there, which is the same fact
// arriving twice rather than a write that could lose the same race the entry
// was lost to, and a marker that could not be written at all is the store fault
// auditCall reports on the door's logger — where a fault of this installation
// is read, this layer having no logger of its own.
//
// The write is made on a context of its own, bounded, for the reason the marker
// exists: the loss is owed to the record and to whoever reads it next, not to
// the caller whose request has already ended, and a marker written on that
// caller's context would be the one write that reports the loss and cannot
// arrive.
func (r records) markAuditGap(ctx context.Context, key string) {
	gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditGapTimeout)
	defer cancel()
	_, _ = r.objs.create(gctx, key+auditGapSuffix, auditGapMarker)
}

// GetAudit reads one identity's record. An identity no call has been served for
// has an empty record — an answer and not an error, because "this person has
// called nothing" is what an operator asking about them wants to know, and a
// refusal would read as a fault of the installation.
//
// Whether the record is short is read from the marker beside it and not from
// the record, because the write that lost the entry is the write that could not
// touch the record (AppendAudit). A marker that cannot be read is not a record
// that may be called whole: what cannot be settled is answered as incomplete,
// which is the answer that sends a reader to the log rather than the one that
// takes a short history for a whole one.
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
	if _, _, err := r.objs.read(ctx, key+auditGapSuffix); !errors.Is(err, errMissing) {
		rec.Incomplete = true
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
			// The identity is checked here as well as in the store, because
			// the store answers a record it cannot hold and a store it cannot
			// reach with one error: an identity that is not one person would
			// otherwise be answered — and durably recorded — as this
			// installation's fault rather than as the caller's own argument.
			// It is the store's own predicate and not a second rule, so a
			// store that tightens what an identity may be tightens this too.
			if _, ok := userEntry(args.Identity); !ok {
				return nil, errors.New("the identity is not one person written as user:<email>: this tool reads one person's record and nobody else's")
			}
			rec, err := store.GetAudit(ctx, args.Identity)
			if err != nil {
				// As revoke_family's: the store's own error is not the
				// caller's to read, and the sentinel is what callTool maps to
				// the door's fault rather than to a denial of the read.
				return nil, fmt.Errorf("%w: the record was not read", ErrStoreUnreadable)
			}
			return rec, nil
		},
	}
}
