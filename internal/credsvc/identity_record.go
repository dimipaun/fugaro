package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The issuance record's store (R3, LM1): the real IssuanceStore
// (identity_config.go) over the installation's database.
//
// One node per slug and run, at identitywire.PathRecord — creds/issuance/<slug>
// /<run> — which the rules already close to every signed-in identity, read and
// write (F26), so this task adds no rule (LM5 pins the path and generates
// nothing).
//
// Every decision this contract makes about a record is one atomic read-then-
// write HERE, and that is the whole reason the store is the interface it is
// (identity_config.go): a re-issue replaces another ticket, a claim happens
// once, and a claim's retry is the same claim — three decisions that are only
// correct if the value they are made from is the value that is still there when
// the write lands. The loop below is therefore the credential cascade's own
// rule (creds' cas, which this store cannot call: it is unexported, and what it
// decides is a cascade credential's value and not this record) spelled at the
// size of one node, with the same two properties: a conflict is retried from a
// fresh read and never applied to a value it did not read, and a node this
// release cannot decode is refused rather than decoded into a record whose
// unknown member is zero — the zero Record is an unclaimed ticket for no
// account, which is exactly the shape that would let a stale ask through.

// rtdbIssuanceStore is that store. It holds the database and no memory of its
// own: two services of one installation, and two asks of one service, write the
// same node, so a decision remembered here would be a decision the other writer
// never saw.
type rtdbIssuanceStore struct {
	db creds.DB
}

// NewIssuanceStore is the store a deployment wires into IdentityConfig.Store
// (I1): the database client the service's other nodes are written with — the
// same *rtdb.Client the cascade's state and the outcomes are read through — so
// the record lives beside them under the same creds subtree and no second
// client is built for it.
//
// It takes creds.DB rather than *rtdb.Client because the compare-and-set is
// those three calls and nothing else, which is what lets a test drive this
// store against a fake that speaks the interface (creds.Store's own reason for
// the seam); *rtdb.Client is the implementation every deployment hands in.
func NewIssuanceStore(db creds.DB) IssuanceStore { return rtdbIssuanceStore{db: db} }

// Put writes the record for one slug and run, replacing the one there.
//
// Replacing is R6: before the ticket is claimed, the same caller asking again
// for the same slug and run gets a new ticket and the old one dies, and the old
// ticket dies because the node now holds the digest of the new one — nothing
// else remembers a ticket, so nothing else has to be unwound. The re-ask stops
// at the registration (BR17, R19): it writes a whole new record, and the record
// it would replace carries the job's id, which is written once.
//
// It refuses ANOTHER caller's record itself (identity_config.go's contract), a
// record whose ticket is claimed, and a record whose job is registered: whose
// record it is, whether it is still claimable and whether its id was already
// written is what the compare-and-set reads, so the answer is made from the
// value that is there rather than from a read the route made earlier (R5, R19).
func (s rtdbIssuanceStore) Put(ctx context.Context, slug, run string, rec identitywire.Record) error {
	path, err := issuancePath(slug, run)
	if err != nil {
		return err
	}
	_, err = s.update(ctx, path, func(cur identitywire.Record, found bool) (identitywire.Record, bool, error) {
		switch {
		case !found:
			return rec, true, nil
		case cur.Claimed.Account != "":
			// A claimed ticket is never replaced, whoever asks: the record is
			// what the identity was signed from, and a replacement would either
			// rewrite an identity already in a run's hands or leave a claimed
			// node whose claims are the new ask's (R5, R6).
			return cur, false, identitywire.ErrClaimed
		case cur.Caller != rec.Caller:
			// The recorded caller is the verified address (R3), so this is the
			// re-issue fence of R5 read the other way round: the caller on the
			// record is the only one it may be re-issued to.
			return cur, false, identitywire.ErrAnotherCaller
		case cur.JobID != "":
			// A registered job's id is written once and never overwritten
			// (BR17, R19), and this ask would overwrite it with nothing: the
			// record below is a whole new one (identity_ticket.go) that carries
			// no id, so serving the re-ask would wipe the registration and let
			// the next registration write a different id under the same run id.
			// Once the job is registered the record is past the point of a
			// re-ask, exactly as it is past the claim.
			return cur, false, ErrJobAlreadyRegistered
		}
		return rec, true, nil
	})
	return err
}

// Get reads the record for one slug and run. found=false with no error is "this
// run has no record", which is a fact about the run and not a failure: the claim
// route answers it an unknown ticket (LM2), and the ticket route answers it
// nothing at all, because Put creates the record it is asked for.
func (s rtdbIssuanceStore) Get(ctx context.Context, slug, run string) (identitywire.Record, bool, error) {
	path, err := issuancePath(slug, run)
	if err != nil {
		return identitywire.Record{}, false, err
	}
	var raw json.RawMessage
	_, found, err := s.db.GetETag(ctx, path, &raw)
	if err != nil {
		return identitywire.Record{}, false, &identitywire.StoreError{Path: path, Err: err}
	}
	if !found {
		return identitywire.Record{}, false, nil
	}
	rec, err := decodeRecord(path, raw)
	if err != nil {
		return identitywire.Record{}, false, err
	}
	return rec, true, nil
}

// Claim marks the record claimed: the account that claimed, when, and the digest
// of the nonce it carried (R2, R5).
//
// Three answers, and each one is the record's own:
//
//   - the same account with the same nonce digest, on a record already claimed,
//     is the retry R2 answers again: the record is returned as it stands and
//     NOTHING is written. Writing the same value back would spend the node's
//     ETag for every other writer of it, and a write that failed would fail a
//     retry the record already answers — the run would halt over a call the
//     service had, in fact, already served;
//   - every other claim on a claimed record is refused with ErrClaimed,
//     whatever account made it: a ticket is claimed once, and the run that lost
//     the race halts saying so (R5);
//   - a claim from an account that is not the record's ExpectedAccount is
//     refused with ErrAnotherCaller, on a record that is NOT claimed. That is
//     the fence the claim route already makes before it claims (LM2, R4), and
//     it is repeated here as the store's own floor: the record is the only
//     thing that says which job may claim, and a store that wrote a claim from
//     any other account would have handed a run's identity to whoever asked
//     first.
//
// Both account comparisons fold as every address in this service does
// (sameAccount, the ASCII lower case the contract holds a claim's match to), and
// that is not a loosening: the ticket route admits the account the ask names
// folded and writes the ask's OWN spelling on the record, while the claim's
// account is the CONFIGURED spelling the verifier answers (R10, C1.10), so a
// byte-for-byte comparison here would refuse a run whose ticket names a
// differently-cased account that the route's own check admitted — the two
// spellings are one account, and this floor has to see them as one.
func (s rtdbIssuanceStore) Claim(ctx context.Context, slug, run string, claim identitywire.Claim) (identitywire.Record, error) {
	path, err := issuancePath(slug, run)
	if err != nil {
		return identitywire.Record{}, err
	}
	return s.update(ctx, path, func(cur identitywire.Record, found bool) (identitywire.Record, bool, error) {
		switch {
		case !found:
			return cur, false, identitywire.ErrNoRecord
		case cur.Claimed.Account != "":
			if sameAccount(cur.Claimed.Account, claim.Account) && cur.Claimed.NonceHash == claim.NonceHash {
				return cur, false, nil
			}
			return cur, false, identitywire.ErrClaimed
		case !sameAccount(claim.Account, cur.ExpectedAccount):
			return cur, false, identitywire.ErrAnotherCaller
		}
		cur.Claimed = claim
		return cur, true, nil
	})
}

// update is the one read-then-write of a record: it reads the node, hands what
// it found to f, and writes what f answers under the ETag it read.
//
// f answers write=false for a call with nothing to change — the claim that is a
// retry — and then nothing is written at all, which is why the flag exists
// rather than f answering the value it was given: a no-op write is a write that
// can fail, and this store must not fail a call whose answer is already in the
// node.
//
// A conflict (rtdb.ErrPrecondition: the ETag moved, so another writer won the
// race) is retried from a fresh read, up to creds.DefaultAttempts — the
// cascade's own budget for the same loop — and after that the write is refused
// with creds.ErrContention rather than made unconditionally: an unconditional
// write is exactly the lost update this loop exists to prevent.
//
// The two kinds of failure are kept apart, because the routes tell them apart
// (identity_config.go's IssuanceStore): what f decides is returned BARE (the
// sentinels are decisions about the caller or the run, and the ticket route
// answers them a refusal), and what the DATABASE answers is wrapped in a
// *StoreError (a node nobody could read or write is this service's own failure,
// and R16's rule is that it must never be answered as a caller who is not
// admitted — a run's gateway reads a 403 as a missing invoke grant and halts on
// it).
func (s rtdbIssuanceStore) update(ctx context.Context, path string, f func(cur identitywire.Record, found bool) (identitywire.Record, bool, error)) (identitywire.Record, error) {
	for range creds.DefaultAttempts {
		var raw json.RawMessage
		etag, found, err := s.db.GetETag(ctx, path, &raw)
		if err != nil {
			return identitywire.Record{}, &identitywire.StoreError{Path: path, Err: err}
		}
		var cur identitywire.Record
		if found {
			if cur, err = decodeRecord(path, raw); err != nil {
				return identitywire.Record{}, err
			}
		}
		next, write, err := f(cur, found)
		if err != nil {
			return identitywire.Record{}, err
		}
		if !write {
			return next, nil
		}
		err = s.db.PutIfMatch(ctx, path, etag, next)
		if errors.Is(err, rtdb.ErrPrecondition) {
			continue
		}
		if err != nil {
			return identitywire.Record{}, &identitywire.StoreError{Path: path, Err: err}
		}
		return next, nil
	}
	return identitywire.Record{}, &identitywire.StoreError{Path: path, Err: creds.ErrContention}
}

// decodeRecord reads one record out of the bytes the database answered: the
// contract's own strict reader (identitywire.DecodeStrict), never a plain
// json.Unmarshal, for the reason creds' cas decodes strictly — a node carrying a
// member this release does not model must be refused rather than decoded to a
// record whose unknown member is zero, because the value that comes out is what
// a decision is made from and then written back.
//
// The failure is a *StoreError: a node this release cannot read is this
// service's own state — a newer writer, a hand-edited node — and never a
// decision about the caller, who did nothing but ask.
func decodeRecord(path string, raw json.RawMessage) (identitywire.Record, error) {
	var rec identitywire.Record
	if err := identitywire.DecodeStrict(raw, &rec); err != nil {
		return identitywire.Record{}, &identitywire.StoreError{Path: path, Err: fmt.Errorf("the record is not one this release can read: %w", err)}
	}
	return rec, nil
}

// issuancePath is the node one record lives at, and the refusal of a slug or a
// run that is not one. identitywire.PathRecord PANICS on an empty segment, and a
// panic in a handler is not a refusal — it is a service that dies on a call. The
// ticket route refuses an ask that names no repository or no run long before
// this (identityTicket's own shape checks), so this is the store's floor under a
// caller that skipped them; it names no path, because there is none to name.
func issuancePath(slug, run string) (string, error) {
	if slug == "" || run == "" {
		return "", errors.New("credsvc: the issuance record's path names no repository slug or no run")
	}
	return identitywire.PathRecord(slug, run), nil
}
