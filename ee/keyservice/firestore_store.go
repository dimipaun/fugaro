// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// This file is the durable half of store.go's interface: §7.3's issuance
// records, §4.5's applications, §4.1's pending verifications, §5.2's
// subscriptions, §7's key state, §8's revocation list, §14's trials and
// D2135's handover, kept in the portal's document store
// (ee/keyservice/docstore) rather than in the process that answers a request.
//
// It is written against that package's seam and not against Firestore's own
// API, which is what makes its rules testable without a database: the suite in
// firestore_store_test.go runs the same cases against this store on a memory
// document store and against store.go's Memory, so the two answer alike, and
// the deploy is one constructor argument away (docstore.NewFirestore over the
// client P1.5's database is reached through). What the seam cannot express is
// named where it is used: it has no field-level update and no delete, and both
// cost something here — a read-modify-write, a record the database's own TTL
// collects — rather than being papered over.
//
// **Everything a check needs is one transaction.** store.go states the two
// places where a read-then-write is the whole rule — an issuance is refused
// under a source the store already holds (§5.2), and a trial is claimed once
// per installation and per account (§14) — and both are written here as one
// transaction, because the document store the deploy names really can run two
// of them at once. That is the closed half of the limit store.go records; the
// pairs a caller makes out of *separate* calls (a mint's record and its key
// state) stay two writes, and what bounds those is the service's own order.
//
// **A record nobody named is refused rather than kept.** A record's own id is
// its document's id here — an issuance's source, an application's request id, a
// key's id — and the document store refuses a document nothing named, because
// the empty id is the one id every unnamed caller would share
// (docstore.ErrUnnamed). store.go's memory store holds records without ids and
// keeps such a record anyway, so the two stores differ on an input no caller
// produces: merchant, renew and admin each name what they minted for. The suite
// in firestore_store_test.go runs against both stores and so cannot state this,
// which is why it is written here and pinned beside it
// (TestTheDocstoreRefusesARecordNobodyNamed).

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// The document store's collections, one per record store.go's interface keeps.
// Collection names are a wire shape of their own — a deploy's existing data is
// read under them — so a record that moves to another collection is a
// migration and not a rename.
const (
	issuanceCollection     = "issuances"
	applicationCollection  = "applications"
	verificationCollection = "verifications"
	subscriptionCollection = "subscriptions"
	revocationCollection   = "revocations"
	keyStateCollection     = "keyStates"
	handoverCollection     = "handovers"
	trialCollection        = "trials"
	// trialAccountCollection holds one document per account that has claimed a
	// trial, naming the installation it was claimed for. It is what makes §14's
	// "one trial per account" a rule the store can hold in one transaction
	// beside the installation's own document: a check that read the trials and
	// then wrote one would be a check two asks could pass together, which is
	// exactly the pair ClaimTrial exists to refuse.
	trialAccountCollection = "trialAccounts"
)

// DocStore is store.go's Store over the portal's document store.
type DocStore struct {
	db docstore.Store
}

// NewDocStore keeps the service's records in db. The client behind db is the
// caller's — the deploy builds it against the portal's own database, a test
// against docstore's memory store — and nothing here reads the clock, so a
// caller that moves time moves it for these records too.
func NewDocStore(db docstore.Store) *DocStore { return &DocStore{db: db} }

var _ Store = (*DocStore)(nil)

// SaveIssuance writes one issuance, refusing a source the store already holds.
// The read and the write are one transaction: §5.2's rule is that a redelivery
// does not mint twice, and two deliveries in flight at once are the case the
// rule is for (store.go's Store states the limit this closes).
func (s *DocStore) SaveIssuance(ctx context.Context, r Record) error {
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		held, err := tx.Get(ctx, issuanceCollection, r.Source, nil)
		if err != nil {
			return err
		}
		if held {
			return ErrAlreadyRecorded
		}
		return tx.Put(ctx, issuanceCollection, r.Source, r)
	})
}

func (s *DocStore) Issuances(ctx context.Context) ([]Record, error) {
	return whole[Record](ctx, s.db, issuanceCollection)
}

func (s *DocStore) SaveApplication(ctx context.Context, a Application) error {
	return s.db.Put(ctx, applicationCollection, a.ID, a)
}

func (s *DocStore) Applications(ctx context.Context) ([]Application, error) {
	return whole[Application](ctx, s.db, applicationCollection)
}

func (s *DocStore) SaveVerification(ctx context.Context, v Verification) error {
	return s.db.Put(ctx, verificationCollection, v.ID, v)
}

func (s *DocStore) Verifications(ctx context.Context) ([]Verification, error) {
	return whole[Verification](ctx, s.db, verificationCollection)
}

func (s *DocStore) SaveSubscription(ctx context.Context, sub Subscription) error {
	return s.db.Put(ctx, subscriptionCollection, sub.ID, sub)
}

func (s *DocStore) Subscriptions(ctx context.Context) ([]Subscription, error) {
	return whole[Subscription](ctx, s.db, subscriptionCollection)
}

func (s *DocStore) Subscription(ctx context.Context, id string) (Subscription, bool, error) {
	var sub Subscription
	held, err := s.one(ctx, subscriptionCollection, id, &sub)
	return sub, held, err
}

// Revoke writes one revocation and refuses to change one the store already
// holds: a refund delivered twice is one revocation, and the key is revoked
// from the first instant either way. First writer wins, inside a transaction,
// so a second delivery cannot replace the reason and the instant of the entry
// that withdrew the key.
func (s *DocStore) Revoke(ctx context.Context, r Revocation) error {
	if r.KeyID == "" {
		return ErrRevocationUnnamed
	}
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		held, err := tx.Get(ctx, revocationCollection, r.KeyID, nil)
		if err != nil {
			return err
		}
		if held {
			return nil
		}
		return tx.Put(ctx, revocationCollection, r.KeyID, r)
	})
}

func (s *DocStore) Revocation(ctx context.Context, keyID string) (Revocation, bool, error) {
	var r Revocation
	held, err := s.one(ctx, revocationCollection, keyID, &r)
	return r, held, err
}

func (s *DocStore) Revocations(ctx context.Context) ([]Revocation, error) {
	return whole[Revocation](ctx, s.db, revocationCollection)
}

func (s *DocStore) SaveKeyState(ctx context.Context, state KeyState) error {
	return s.db.Put(ctx, keyStateCollection, state.KeyID, state)
}

func (s *DocStore) KeyState(ctx context.Context, keyID string) (KeyState, bool, error) {
	var state KeyState
	held, err := s.one(ctx, keyStateCollection, keyID, &state)
	return state, held, err
}

// TouchKey moves a key's last-seen and nothing else. The document seam writes a
// document whole, so this reads the record and writes it back with the new
// instant — inside a transaction, which is what keeps store.go's reason for the
// method: a status call in flight cannot put back a digest a rotation has
// already replaced, because the rotation moves the version this read recorded
// and the write is refused and retried onto the rotated record.
//
// A key the store does not hold is left alone rather than given an entry, and
// the instant only moves forward, both as store.go states them.
func (s *DocStore) TouchKey(ctx context.Context, keyID string, at time.Time) error {
	if keyID == "" {
		return nil
	}
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		var held KeyState
		known, err := tx.Get(ctx, keyStateCollection, keyID, &held)
		if err != nil || !known {
			return err
		}
		if !at.After(held.LastSeen) {
			return nil
		}
		held.LastSeen = at
		return tx.Put(ctx, keyStateCollection, keyID, held)
	})
}

// SaveHandover keeps one rotation's answer for its window. It sweeps nothing:
// store.go's memory store drops the handovers whose window has closed, and this
// store has no delete to drop them with — the seam has none, because nothing
// else here needs one. What bounds the window instead is the read below, which
// answers an expired handover as one the store does not hold, and the
// database's own TTL on the collection (P1.5), which is what keeps the store
// from growing a list of live credentials nothing collects.
func (s *DocStore) SaveHandover(ctx context.Context, h Handover, _ time.Time) error {
	if h.KeyID == "" {
		return ErrHandoverUnnamed
	}
	return s.db.Put(ctx, handoverCollection, h.KeyID, h)
}

// Handover answers the rotation a key is still inside its window for. The
// window is read here and not left to the store: what a handover holds is a
// successor's key and its renewal secret, so one whose window has closed is
// answered as absent even if the database still holds it.
func (s *DocStore) Handover(ctx context.Context, keyID string, now time.Time) (Handover, bool, error) {
	var h Handover
	held, err := s.one(ctx, handoverCollection, keyID, &h)
	if err != nil || !held {
		return Handover{}, false, err
	}
	if !now.Before(h.Until) {
		return Handover{}, false, nil
	}
	return h, true, nil
}

// ClaimTrial writes §14's one trial per installation and per account. Both
// halves are read and both documents are written inside one transaction, so two
// asks arriving together cannot both find the store empty — which is the pair
// store.go names as the one check that cannot be split.
func (s *DocStore) ClaimTrial(ctx context.Context, t Trial) error {
	if t.Installation == "" || t.Account == "" {
		return ErrTrialUnnamed
	}
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		for _, claim := range []struct{ collection, id string }{
			{trialCollection, t.Installation},
			{trialAccountCollection, t.Account},
		} {
			held, err := tx.Get(ctx, claim.collection, claim.id, nil)
			if err != nil {
				return err
			}
			if held {
				return ErrTrialAlreadyClaimed
			}
		}
		if err := tx.Put(ctx, trialCollection, t.Installation, t); err != nil {
			return err
		}
		// The account's document names the installation it claimed, which is
		// what the owner reads when an ask arrives for an account whose trial is
		// already out. It is an object like every other document here: what the
		// store holds is documents.
		return tx.Put(ctx, trialAccountCollection, t.Account, map[string]string{"installation": t.Installation})
	})
}

func (s *DocStore) Trials(ctx context.Context) ([]Trial, error) {
	return whole[Trial](ctx, s.db, trialCollection)
}

// one reads a record by id. An id nobody named is answered as a record the
// store does not hold rather than refused, which is what store.go's memory
// store answers and what the callers do with it — a merchant event naming no
// subscription reaches this lookup (merchant.go's apply), and the document seam
// on its own would turn that into an error the handler has no answer for.
func (s *DocStore) one(ctx context.Context, collection, id string, out any) (bool, error) {
	if id == "" {
		return false, nil
	}
	return s.db.Get(ctx, collection, id, out)
}

// whole reads one collection and decodes every document into its record. The
// order is the store's (by document id) and not an insertion order, which the
// interface does not promise either way: every reader of these lists filters
// them itself (store.go).
func whole[T any](ctx context.Context, db docstore.Store, collection string) ([]T, error) {
	docs, err := db.Query(ctx, collection, docstore.Query{})
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(docs))
	for _, d := range docs {
		var rec T
		if err := d.Decode(&rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}
