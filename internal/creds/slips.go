package creds

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Slips are the service's grant to one run: what a launcher asked for on a
// run's behalf, written under creds/slips/<id> before the job starts and read
// on every call the run makes (D1932).
//
// The shape of the exchange, and where each half lives:
//
//   - a launcher asks the service for a grant, naming the run and the
//     credentials it needs (DS1.3);
//   - the service verifies the caller and writes the Slip here, answering its
//     id;
//   - the launcher passes the id to the job (DS1.4), and the run's first call
//     CLAIMS it with the run identity and the job account it can prove
//     (D1937, R8), answering a claim secret;
//   - every later call presents the id, the identity, the account and the
//     claim secret, which the service checks against the record (DS1.6).
//
// # What a slip is not
//
// It is not a bearer ticket, and the id is not a secret. The id travels to the
// job in its environment, and the agent shares the runner's container and reads
// its environment through /proc (F22, U5), so the record is protected by what it
// BINDS and not by who can read it: the run's verified identity (D134's Firebase
// token for r~<slug>~<run>, plus the job service account's own Google token),
// the account the service expects to see, and the claim secret the claim
// answers exactly once (D1941). A leaked id buys one run's credentials until
// that run ends, and nothing else.
//
// It is not a store of values either (G9, F19). A slip names the secrets a run
// may use — Allowed carries their secret IDs — and holds the owner, the run,
// the account, a runtime and the HASH of the claim secret. No credential's
// value is here in any field, and none ever may be: every call reads this node,
// the service may cache it, and an operator may read it at the database.
//
// # Time
//
// Nothing here reads the clock. Create takes no instant at all and Claim is
// given now, exactly as the selection path is (Selector.Now): a test states the
// time it means, and a service has one clock per call.
type Slip struct {
	// ID is the slip's own id, and it is the node's PATH rather than a member
	// of its value (json:"-", as Lease's Slug and Run are): the value's own
	// copy would be a second place for it to be wrong, and a reader fills it in
	// from the path it read. It is minted by the service at the grant, and it
	// is an id and not a secret — see the note above.
	ID string `json:"-"`
	// Owner is the verified `sub` of the launcher that asked for the grant
	// (D1938, U2): the stable numeric Google account ID, which is what the
	// secret name grammar and the intake are keyed on. It is recorded so an
	// operator can see who asked for a run, and it is never the caller's own
	// text.
	Owner string `json:"owner"`
	// Slug and Run are the run this slip is for, as the service verified them
	// from the run identity (D134). They are the whole of the binding: a claim,
	// and every call after it, must present a verified identity for this pair.
	Slug string `json:"slug"`
	Run  string `json:"run"`
	// ExpectedAccount is the job service account the service expects to see at
	// the claim. One account is dedicated to one Cloud Run job (auth.go), so the
	// account is what says which job called — while the execution name the spec
	// would have bound instead is unverifiable from a job's token (F7, D1937),
	// which is why it is not here.
	ExpectedAccount string `json:"expected_account"`
	// ClaimHash is the hash of the claim secret the claim answered
	// (ClaimSecretHash), and it is empty until a run claims the slip. The secret
	// itself is never stored, here or anywhere: it lives in the runner's memory
	// and is presented on every call (D1941).
	ClaimHash string `json:"claim_hash"`
	// Allowed are the secret IDs the run may be served, exactly and only: the
	// service resolves these names at each call and never widens the set (DS3.2).
	// They are names, never values (G9).
	Allowed []string `json:"allowed"`
	// Pool is the pool the granted credentials belong to, empty when the grant
	// names no pool. The pool's owner and allowed users are checked at the grant
	// (DS3.2, G8), so this records which pool decided the grant.
	Pool string `json:"pool"`
	// MaxRuntime is how long the run may be served, measured from the claim, and
	// it is the service's capped answer to what the launcher asked for (R10): the
	// slip cannot outlive the run it was granted for, whatever the job does.
	// Issuance ends at Expires.
	MaxRuntime time.Duration `json:"max_runtime"`
	// Claimed is who claimed the slip and when; its zero value is an unclaimed
	// slip, which is not live and serves nothing (R9).
	Claimed Claim `json:"claimed"`
}

// Claim is one run's claim on a slip: the account that claimed it and the
// instant it did.
//
// At is what MaxRuntime is measured from, so the slip's own clock starts when
// the run starts rather than when the launcher asked — a job that waited for a
// slot does not lose the runtime it was granted. An unclaimed slip therefore
// has no expiry of its own, and that is deliberate: it is worth nothing to
// anyone, because only the run it names can claim it and the claim answers the
// secret the record keeps only a hash of (D1941).
type Claim struct {
	// Account is the verified job service account that claimed the slip. The
	// claim checked it against ExpectedAccount, so the two agree; both are kept
	// because the record then says what was expected and what arrived, which is
	// what an operator reading a refusal needs.
	Account string `json:"account"`
	// At is the instant of the claim.
	At time.Time `json:"at"`
}

// Expires is the instant the slip's issuance ends: its max runtime from the
// claim. The zero time for an unclaimed slip, which is not live at all.
func (s Slip) Expires() time.Time { return s.Claimed.At.Add(s.MaxRuntime) }

// Live reports whether the slip may serve a call at now: it has been claimed,
// and now is inside its max runtime.
//
// It is a strict Before on purpose, as Lease.Live is: a slip whose runtime has
// arrived is over rather than current, so the two instants that matter — the
// claim and the expiry — are the run's own and no tick longer.
// TestExpiredSlipRefused pins the boundary, because flipping the comparison
// would extend every slip by an instant no other test would notice.
//
// An unclaimed slip is never live. That is R9's fail-closed rule read at the
// store: a credential served only under a claimed slip is a credential a
// slip-less call cannot reach, and a record nobody has claimed is nobody's.
func (s Slip) Live(now time.Time) bool {
	return !s.Claimed.At.IsZero() && now.Before(s.Expires())
}

// ClaimMatches reports whether secret is the claim secret this slip was claimed
// with.
//
// The comparison is constant-time (crypto/subtle). It is not strictly needed —
// the secret is drawn at full entropy, so a timing signal about a hash of it
// buys nothing — but it costs one call and removes the question, which is the
// same trade the rest of this file makes about what a caller may learn from a
// refusal.
func (s Slip) ClaimMatches(secret string) bool {
	if s.ClaimHash == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(ClaimSecretHash(secret)), []byte(s.ClaimHash)) == 1
}

// ClaimSecretBytes is the entropy of a claim secret. Thirty-two bytes is the
// width of the hash that stands for it, so the secret cannot be found by
// guessing whatever the hash's own strength is.
const ClaimSecretBytes = 32

// ClaimSecretHash is the one spelling of a claim secret's hash, so that the
// write and the check cannot drift: the claim stores this of the secret it
// answers, and every call compares this of the secret it is presented.
//
// SHA-256 with no salt and no stretching, which is the right choice for exactly
// this input: the secret is 32 bytes drawn from crypto/rand, so there is no
// dictionary to run and nothing a work factor would slow down. A salt would add
// a field to the record and buy nothing against an attacker who has the node.
func ClaimSecretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newClaimSecret draws one claim secret: ClaimSecretBytes from crypto/rand, in
// hex, which is the one alphabet that needs no escaping wherever the secret
// travels (a header, a body, an environment).
//
// A refused draw is an error and never a fallback to something guessable: the
// claim is the whole of what fences one launcher from another (D1941), and a
// predictable secret would make the fence a formality.
func newClaimSecret() (string, error) {
	raw := make([]byte, ClaimSecretBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("creds: drawing a claim secret failed: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// The slip store's refusals. Each is a sentinel so a caller can answer the wire
// contract without reading a message, and none of them carries a value or a
// caller's own text (F32): a refusal names what is wrong with the record.
var (
	// ErrNoSlip is a slip id that names no node: never written, or ended.
	ErrNoSlip = errors.New("creds: no such slip")
	// ErrSlipExists is a create whose id is already taken. It is refused rather
	// than overwritten, because the record it would replace is another run's
	// grant.
	ErrSlipExists = errors.New("creds: a slip with that id already exists")
	// ErrSlipMalformed is a slip that could not be written as it stands: it must
	// name its id, its owner, the run, the account the run's job is expected to
	// present and a positive max runtime, and it must be unclaimed — a record
	// created already claimed would be a claim nobody made, and one with no
	// runtime would never end. Unclaimed is the zero Claim in every field, so a
	// record carrying a claimant's account with no instant, or an instant with no
	// account, is refused as well: neither is a state a claim can be read from.
	ErrSlipMalformed = errors.New("creds: a slip must name its id, owner, run, expected account and a positive max runtime, and must be unclaimed")
	// ErrSlipClaimed is a second claim of a slip a run already holds. It is the
	// refusal D1941 exists for: the first claim keeps the secret, and a later
	// claimant — another launcher that minted the same run identity, F28 — is
	// told the slip is taken rather than handed a working one.
	ErrSlipClaimed = errors.New("creds: the slip has already been claimed")
	// ErrSlipExpired is a CLAIMED slip whose max runtime has passed since it was
	// claimed. Only a claimed slip can expire: the runtime is measured from the
	// claim (Slip.Expires), so an unclaimed record has no expiry at all — it is
	// not live (Slip.Live), so it serves nothing until the run it names claims
	// it, and a claim of one is either granted to that run or refused as the
	// identity it does not name, never as expired. The service cannot expire an
	// unclaimed slip either: the record carries no creation time to measure
	// from, which is deliberate (a grant that waits for a slot must not lose the
	// runtime it was granted).
	ErrSlipExpired = errors.New("creds: the slip's max runtime has passed")
	// ErrSlipIdentity is a claim by a run or an account the slip does not name
	// (G3): the id alone is never enough, so a slip found or guessed by another
	// run is refused whole.
	ErrSlipIdentity = errors.New("creds: the slip is not this run's, or not this account's")
)

// SlipStore is the slip half of the service's state, and it is deliberately
// apart from Store: Store is what a selection reads and writes on every call,
// while this is the grant a launcher asked for and the claim a run makes once.
// The service holds one RTDBStore and uses it as both.
//
// Every method takes the slip's id, and an id that is empty is refused as
// ErrNoSlip rather than reaching a path builder: an id arrives from a request
// (DS1.3), and the path builders in this package panic on an empty segment, so
// a request that carried nothing must be a refusal and not a panic.
type SlipStore interface {
	// Create writes one unclaimed slip, and refuses an id that is already taken.
	Create(ctx context.Context, sl Slip) error
	// Slip reads one slip. found=false is a slip that is not there — never
	// written, or ended — and it is not an error; the caller decides what an
	// absent slip means (DS1.6 refuses the call).
	Slip(ctx context.Context, id string) (Slip, bool, error)
	// Claim claims one slip for one run, once: it answers the claimed slip and
	// the claim secret the run must present on every call. It is a
	// compare-and-set, so two claims arriving at the same instant leave exactly
	// one winner (D1941).
	Claim(ctx context.Context, id string, run Run, account string, now time.Time) (sl Slip, secret string, err error)
	// End removes one slip, which is how a run's grant stops at its outcome or
	// at its max runtime. It is idempotent: a slip that is already gone is the
	// state the caller wanted.
	End(ctx context.Context, id string) error
}

// RTDBStore is a SlipStore, checked by the compiler rather than left to the
// first caller that wires the two together.
var _ SlipStore = (*RTDBStore)(nil)

// pathSlips is the subtree the slips live under. It is under creds/ with the
// marks, the placements and the counter, and the rules deny the whole subtree
// to every run identity (O1): the service writes it with its own account.
const pathSlips = "creds/slips"

// PathSlip is one slip's node.
func PathSlip(id string) string { return pathSlips + "/" + seg("slip id", id) }

// Create implements SlipStore.
//
// The write is conditional on the node being absent, so two creates of one id
// cannot both land: the id is minted by the service and a collision means a
// bug, but the cost of refusing it is one precondition, and the cost of not
// refusing it is one run served under another run's grant.
func (s *RTDBStore) Create(ctx context.Context, sl Slip) error {
	if sl.ID == "" {
		return ErrNoSlip
	}
	if err := sl.checkWritable(); err != nil {
		return err
	}
	path := PathSlip(sl.ID)
	// The id is the path, not the value (Slip.ID): a record that carried its own
	// id would be a second place for it to be wrong.
	sl.ID = ""
	var raw json.RawMessage
	etag, found, err := s.db().GetETag(ctx, path, &raw)
	if err != nil {
		return fmt.Errorf("creds: writing the slip %q: %w", path, err)
	}
	if found {
		return fmt.Errorf("creds: writing the slip %q: %w", path, ErrSlipExists)
	}
	if err := s.db().PutIfMatch(ctx, path, etag, sl); err != nil {
		return fmt.Errorf("creds: writing the slip %q: %w", path, err)
	}
	return nil
}

// checkWritable holds Create to the fields a slip cannot do without. A record
// missing any of them is one nothing can be decided from: the run it names
// could never claim it, the account it expects is what the claim is checked
// against, and a runtime that is not positive would leave a claimed slip that
// never ends. Fail closed at the writer, where the mistake is, rather than at
// every reader afterwards.
//
// Unclaimed means the zero Claim in EVERY field, and the test is the whole
// struct rather than the instant alone: a record carrying a claimant's account
// and no instant, or an instant and no account, is a claim nobody made just as
// surely as one carrying both, and it is a state no reader can decide from —
// Live keys on the instant, while the account is what an operator reading the
// node takes for who asked for the run.
func (sl Slip) checkWritable() error {
	switch {
	case sl.Owner == "", sl.Slug == "", sl.Run == "", sl.ExpectedAccount == "":
		return fmt.Errorf("creds: writing the slip %q: %w", sl.ID, ErrSlipMalformed)
	case sl.MaxRuntime <= 0:
		return fmt.Errorf("creds: writing the slip %q: %w", sl.ID, ErrSlipMalformed)
	case sl.ClaimHash != "" || sl.Claimed != (Claim{}):
		return fmt.Errorf("creds: writing the slip %q: %w", sl.ID, ErrSlipMalformed)
	}
	return nil
}

// Slip implements SlipStore.
//
// The node is decoded with the strict decoder, as the marks and the leases are
// and for the same reason: a member this release does not model would otherwise
// be dropped silently, and a dropped member is a zero field — for a slip, an
// unclaimed one with no allowed set. A record that cannot be read in full is
// therefore an error and never a slip that is half read.
func (s *RTDBStore) Slip(ctx context.Context, id string) (Slip, bool, error) {
	if id == "" {
		return Slip{}, false, ErrNoSlip
	}
	raw := json.RawMessage{}
	_, found, err := s.db().GetETag(ctx, PathSlip(id), &raw)
	if err != nil {
		return Slip{}, false, fmt.Errorf("creds: reading the slip %q: %w", id, err)
	}
	if !found {
		return Slip{}, false, nil
	}
	sl, err := decodeStrict[Slip](raw)
	if err != nil {
		return Slip{}, false, fmt.Errorf("creds: reading the slip %q: %w", id, err)
	}
	sl.ID = id
	return sl, true, nil
}

// Claim implements SlipStore.
//
// The refusals are ordered so that the answer says the most useful true thing:
// an id that names nothing is ErrNoSlip, a caller the slip does not name is
// ErrSlipIdentity (G3 — the id alone is never enough), a slip whose runtime has
// passed since it was claimed is ErrSlipExpired, and only then is a slip
// another run already holds ErrSlipClaimed. The expiry is asked before the
// claim flag on purpose: a dead slip is dead whatever else it is, and a caller
// reading the refusal learns the fact it can act on.
//
// The write is the package's cas — read the node, decide from it, write under
// the ETag that was read, and re-read and re-decide if the database answers
// ErrPrecondition. It has to be, because the claim is the one place in this
// package where two callers race over the same node on purpose: the runner
// claims on its first call, and a job that Cloud Run retried, or a second
// instance of the same run, claims at the same instant with the same verified
// identity. A read followed by an unconditional write would answer both of them
// a secret, and the second secret would be the first runner's credentials
// (D1941); here the loser re-reads, sees the claim the winner wrote, and is
// refused. The secret is drawn before the loop rather than inside it, because f
// is called again after a conflict and must answer only from its arguments — a
// second draw would be a second secret for one claim.
func (s *RTDBStore) Claim(ctx context.Context, id string, run Run, account string, now time.Time) (Slip, string, error) {
	if id == "" {
		return Slip{}, "", ErrNoSlip
	}
	secret, err := newClaimSecret()
	if err != nil {
		return Slip{}, "", err
	}
	claimed, err := cas(ctx, s.db(), PathSlip(id), s.attempts(), func(prev Slip, found bool) (Slip, error) {
		switch {
		case !found:
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrNoSlip)
		case prev.Slug != run.Slug || prev.Run != run.ID || prev.ExpectedAccount != account:
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipIdentity)
		case !prev.Claimed.At.IsZero() && !prev.Live(now):
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipExpired)
		case !prev.Claimed.At.IsZero():
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipClaimed)
		}
		prev.ClaimHash = ClaimSecretHash(secret)
		prev.Claimed = Claim{Account: account, At: now}
		return prev, nil
	})
	if err != nil {
		return Slip{}, "", err
	}
	claimed.ID = id
	return claimed, secret, nil
}

// End implements SlipStore.
//
// It deletes the node rather than marking it, because nothing is served from a
// slip that is not there (a missing node is a refusal at every reader), and a
// node left behind would be a record of a run's credentials that outlives the
// run. It is not conditional and takes no identity: its callers are the service
// itself, at the run's outcome and at the max runtime, and there is no end
// route for anyone to reach (G10).
func (s *RTDBStore) End(ctx context.Context, id string) error {
	if id == "" {
		return ErrNoSlip
	}
	path := PathSlip(id)
	var raw json.RawMessage
	etag, found, err := s.db().GetETag(ctx, path, &raw)
	if err != nil {
		return fmt.Errorf("creds: ending the slip %q: %w", id, err)
	}
	if !found {
		return nil
	}
	if err := s.db().PutIfMatch(ctx, path, etag, nil); err != nil {
		return fmt.Errorf("creds: ending the slip %q: %w", id, err)
	}
	return nil
}
