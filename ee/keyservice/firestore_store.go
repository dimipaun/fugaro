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
// named where it is used: it has no field-level update, so a record that moves
// one field is read and written back whole (TouchKey), and it is the reason
// that method is a transaction.
//
// **Everything a check needs is one transaction.** store.go states the two
// places where a read-then-write is the whole rule — an issuance is refused
// under a source the store already holds (§5.2), and a trial is claimed once
// per installation and per account (§14) — and both are written here as one
// transaction, because the document store the deploy names really can run two
// of them at once. That is the closed half of the limit store.go records; the
// pairs a caller makes out of *separate* calls (a mint's record and its key
// state) stay two writes, and what bounds those is the service's own order.
// Three rules are checks of that shape — an issuance, a revocation and a trial,
// the third reading two documents — and firestore_store_test.go drives all
// three through a store that refuses to be read or written beside a
// transaction, so the sentence above is held by a case and not only by review
// (TestTheDocstoreChecksAndWritesInOneTransaction).
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
// (TestTheDocstoreRefusesARecordNobodyNamed). A lookup of such a name is the
// other half of the rule and is answered by both stores alike, as a record the
// store does not hold (one, below): the id it is handed is the request's.

import (
	"context"
	"errors"
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

// SaveHandover keeps one rotation's answer for its window, and it sweeps the
// windows that have closed as it writes — store.go's memory store does exactly
// this, and the reason is store.go's own: a handover is the one record here
// that holds key material, so an expired one goes rather than waiting for
// somebody to ask about that key again, which is the case the sweep is for
// (a holder whose answer was lost never asks a second time).
//
// A TTL on the collection could not do this job, and it is worth saying why,
// because it is the cheaper thing to reach for: the seam writes every value as
// the JSON it marshals to, so a handover's Until is stored as a string, and a
// Firestore TTL policy fires on a Timestamp field rather than on the text of
// one. The sweep is what keeps this store from growing a list of live
// credentials, and docstore's Delete exists for it.
func (s *DocStore) SaveHandover(ctx context.Context, h Handover, now time.Time) error {
	if h.KeyID == "" {
		return ErrHandoverUnnamed
	}
	if err := s.sweptHandovers(ctx, now); err != nil {
		return err
	}
	return s.db.Put(ctx, handoverCollection, h.KeyID, h)
}

// Handover answers the rotation a key is still inside its window for. It sweeps
// first, as the write does and for the same reason, so a key nobody asks about
// again is still collected by whichever call comes next; the window is checked
// again below, because another instance whose clock is behind can write an
// expired handover in between the sweep and this read.
func (s *DocStore) Handover(ctx context.Context, keyID string, now time.Time) (Handover, bool, error) {
	if err := s.sweptHandovers(ctx, now); err != nil {
		return Handover{}, false, err
	}
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

// sweptHandovers drops every handover whose window has closed.
//
// It reads the collection rather than asking the database for the expired ones:
// a query by Until would be a comparison this seam does not have (its filters
// are equalities, docstore's Where), and the size here is bounded by the
// rotations of the last day — store.go states the same scale for the same
// reason. The cost of that read is on every handover read and write, which is
// once per rotation and once per replay, not once per request.
func (s *DocStore) sweptHandovers(ctx context.Context, now time.Time) error {
	docs, err := s.db.Query(ctx, handoverCollection, docstore.Query{})
	if err != nil {
		return err
	}
	for _, d := range docs {
		var h Handover
		if err := d.Decode(&h); err != nil {
			return err
		}
		if now.Before(h.Until) {
			continue
		}
		if err := s.db.Delete(ctx, handoverCollection, d.ID); err != nil {
			return err
		}
	}
	return nil
}

// ClaimTrial writes §14's one trial per organisation and installation and per
// account. Both halves are read and both documents are written inside one
// transaction, so two asks arriving together cannot both find the store empty —
// which is the pair store.go names as the one check that cannot be split.
func (s *DocStore) ClaimTrial(ctx context.Context, t Trial) error {
	if t.Organisation == "" || t.Installation == "" || t.Account == "" {
		return ErrTrialUnnamed
	}
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		for _, claim := range []struct{ collection, id string }{
			{trialCollection, trialPair(t.Organisation, t.Installation)},
			{trialAccountCollection, trialAccountName(t.Account)},
		} {
			held, err := tx.Get(ctx, claim.collection, claim.id, nil)
			if err != nil {
				return err
			}
			if held {
				return ErrTrialAlreadyClaimed
			}
		}
		if err := tx.Put(ctx, trialCollection, trialPair(t.Organisation, t.Installation), t); err != nil {
			return err
		}
		// The account's document names the installation it claimed and the
		// organisation that asked for it, which is what the owner reads when an
		// ask arrives for an account whose trial is already out: the installation
		// alone does not say which of two organisations' trials it is. It is an
		// object like every other document here: what the store holds is
		// documents. It is filed under the escaped account for the reason the
		// pair is (`trialAccountName`): an address may hold characters this
		// store cannot name a document with.
		return tx.Put(ctx, trialAccountCollection, trialAccountName(t.Account), map[string]string{
			"organisation": t.Organisation,
			"installation": t.Installation,
		})
	})
}

func (s *DocStore) Trials(ctx context.Context) ([]Trial, error) {
	return whole[Trial](ctx, s.db, trialCollection)
}

// one reads a record by id. An id nobody named, and one the backend cannot file,
// are answered as a record the store does not hold rather than refused, which is
// what store.go's memory store answers and what the callers do with it: the id
// here is the request's — a merchant event naming no subscription reaches this
// lookup (merchant.go's apply), and renew.go's proved hands it the key_id of an
// unauthenticated body — so the seam's refusal, right as it is for a write,
// would turn a stranger's key_id into the service failing to read, where every
// route answers one uniform refusal. It would also answer one request two ways:
// the memory store the handler tests drive files whatever it is handed, so a
// divergence here is one the tests of those callers never show.
//
// What decides it is the seam's own sentinel and not a second copy of the name
// rule here, so the two stay one rule.
func (s *DocStore) one(ctx context.Context, collection, id string, out any) (bool, error) {
	if id == "" {
		return false, nil
	}
	held, err := s.db.Get(ctx, collection, id, out)
	if errors.Is(err, docstore.ErrUnstorableName) {
		return false, nil
	}
	return held, err
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
