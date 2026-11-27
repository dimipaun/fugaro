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

	"github.com/dimipaun/fugaro/internal/routing"
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
	// Launcher is the verified PRINCIPAL that asked, kind and id (R7, plan
	// constraint row 67): a person's Google subject, or a verified service
	// account's address (D3123). It is the pool a run is served from that is
	// resolved from it (PL6), and it is a member of its own rather than a
	// reading of Owner, because Owner cannot say which of the two a run's
	// launcher is: a service account's id is its address and a person's is
	// digits, and D2976 turns exactly on that difference — a service account's
	// run never draws on a person's pool.
	Launcher routing.Principal `json:"launcher"`
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
	//
	// It is NOT the pool a launch named with `--pool`: that is NamedPool below,
	// and the two are separate facts (a launch that named no pool still has a
	// pool if its credentials are served from one, and a launch may name a pool
	// none of its credentials come from).
	Pool string `json:"pool"`
	// Resumes is the paused run this run continues, empty for a run that
	// continues none (runstore.PauseMemberResumes, D3185). It is recorded
	// because it is what carries a chain's paused time forward: the credentials
	// service cannot walk the runs bucket the chain lives in, so a run whose
	// slip names one inherits the chain and the paused time of the pause it
	// continues, and the pause it later takes is measured against them (D3190).
	//
	// It is a member of the record and not of the request alone (the request's
	// own `resumes` is what chose the grant), because it is read days later by
	// the run's own /pause call, which holds the slip and nothing else of its
	// grant.
	Resumes string `json:"resumes,omitempty"`
	// SlipChain is the grant's two own members (C2, CS14): `sources`, the
	// sources this grant covers, and `named_pool`, the pool the launch asked
	// for. They are embedded rather than written out here so that the two names
	// are declared once, in the file that is the contract for them
	// (slip_chain.go), and so that a later member of the grant's chain is a
	// member of one shape rather than a fourth field on this record.
	SlipChain
	// MaxRuntime is how long the run may be served, measured from the claim, and
	// it is the service's capped answer to what the launcher asked for (R10): the
	// slip cannot outlive the run it was granted for, whatever the job does.
	// Issuance ends at Expires.
	MaxRuntime time.Duration `json:"max_runtime"`
	// ClaimDeadline is the instant an UNCLAIMED slip can no longer be claimed,
	// counted from the grant. It is the zero time for a slip that carries none,
	// which is every slip this package wrote before the field existed: a
	// member's own grant is asked for by a launcher the member's service has
	// just verified and claimed by a run that is starting at that instant, so
	// an unclaimed one is worth nothing to anyone and needs no clock of its
	// own (Claim's own comment). A WORKSPACE's grant is the case the field
	// exists for (WC4, threat-model §11 finding 1): the workspace's service
	// mints a slip over the workspace's paid entries for a person's own token,
	// and a grant nobody claims would otherwise stay claimable for ever.
	//
	// It bounds the CLAIM and nothing else. Once the slip is claimed, Expires
	// is the claim plus MaxRuntime, exactly as it always was, so a person's
	// runtime is not shortened by the wait between the grant and the run's
	// first call.
	//
	// It is a POINTER so that a slip which carries none writes no key at all,
	// and that is the reason and not a preference: the deadline is one
	// deployment's (the workspace's) fact, and every slip this package wrote
	// before it existed — every member's grant, and every workspace slip of a
	// reader older than the field — must keep the node shape it had, because
	// `omitempty` does not omit a time.Time and a zero one would write
	// "0001-01-01T00:00:00Z" on every record forever. nil is therefore "no
	// deadline", never "a deadline at the zero time".
	ClaimDeadline *time.Time `json:"claim_deadline,omitempty"`
	// OwnerAddress is the case-folded address of the person the slip was
	// granted to — the verified address the roster was read for (F11, WC4) —
	// and it is empty for a slip that is not a person's: a member's grant is a
	// run's, and Owner and ExpectedAccount are the whole of it.
	//
	// **It is the only fact the check on a later call can make about the
	// roster.** A workspace slip is admitted by the workspace's service on
	// every call under it, and one of the two things that admission asks on
	// every call is whether the person it belongs to is still on the roster
	// (R3, WC4): the roster is keyed by address, so an address on the record is
	// what answers it without a second read of the launcher's token — which the
	// route does not hold. It is case-FOLDED because that is the shape the
	// roster's comparison is in (foldedAddress), and it is an address and never
	// a token: it is written here at the grant, from the token's verified
	// claims, and reaches no log, no error and no caller.
	OwnerAddress string `json:"owner_address,omitempty"`
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
// claim, and for a slip no run has claimed the claim DEADLINE it was granted
// with. The zero time for a slip that carries neither, which is an unclaimed
// slip with no deadline and is not live at all.
//
// The two are one question asked of two records, and they are the only two
// instants a slip's life has: a claimed slip ends MaxRuntime after its claim,
// and an unclaimed one that carries a deadline (a workspace's grant) ends at
// that deadline. Both are strict Befores at their readers (Live, Claim), so a
// slip whose instant has arrived is over rather than current.
func (s Slip) Expires() time.Time {
	if !s.isClaimed() {
		if s.ClaimDeadline == nil {
			return time.Time{}
		}
		return *s.ClaimDeadline
	}
	return s.Claimed.At.Add(s.MaxRuntime)
}

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
// slip-less call cannot reach, and a record nobody has claimed is nobody's. A
// record that carries a claim with no instant is not live either, its runtime
// measured from an instant that never arrived — which is why what counts as
// claimed is asked of the record (isClaimed) rather than of the instant.
func (s Slip) Live(now time.Time) bool {
	return s.isClaimed() && now.Before(s.Expires())
}

// isClaimed reports whether a record carries a claim, in either of the two
// fields a claim is written in.
//
// It is the whole of what a claim is rather than the instant alone (a review of
// this change). A record whose hash and instant did not both arrive is still one
// a claim was written to, and a reader that asks only for the instant reads it
// as unclaimed: a second claim is then granted, it replaces the first claim's
// hash, and the runner that claimed first is locked out while the secret it was
// answered opens nothing — the once-only fence of D1941 gone with no error
// anywhere. Create refuses such a record (checkWritable), so the ones that exist
// are a hand-edit or a release that wrote differently, which is exactly when a
// reader must not decide from one field.
func (s Slip) isClaimed() bool {
	return s.ClaimHash != "" || s.Claimed != (Claim{})
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
	// Claim answers it too, for a claim at the zero instant: that write would
	// leave exactly the half-claim this refuses.
	ErrSlipMalformed = errors.New("creds: a slip must name its id, owner, run, expected account and a positive max runtime, and must be unclaimed")
	// ErrSlipClaimed is a second claim of a slip a run already holds. It is the
	// refusal D1941 exists for: the first claim keeps the secret, and a later
	// claimant — another launcher that minted the same run identity, F28 — is
	// told the slip is taken rather than handed a working one. It is asked of the
	// RECORD rather than of the instant it was claimed at (Slip.isClaimed): a
	// record that carries a claim in either field is a slip taken, so one that
	// was half written cannot be claimed over.
	ErrSlipClaimed = errors.New("creds: the slip has already been claimed")
	// ErrSlipExpired is a CLAIMED slip whose max runtime has passed since it was
	// claimed. Only a claimed slip can expire: the runtime is measured from the
	// claim (Slip.Expires), so an unclaimed record has no expiry at all — it is
	// not live (Slip.Live), so it serves nothing until the run it names claims
	// it, and a claim of one is either granted to that run or refused as the
	// identity it does not name, never as expired. A record that carries a claim
	// with no instant is not expired either: nothing measured a runtime from, so
	// Claim refuses it as taken (ErrSlipClaimed). The service cannot expire an
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
// which is why the question is asked in one place, Slip.isClaimed, by this
// writer, by Claim and by Live.
func (sl Slip) checkWritable() error {
	switch {
	case sl.Owner == "", sl.Slug == "", sl.Run == "", sl.ExpectedAccount == "":
		return fmt.Errorf("creds: writing the slip %q: %w", sl.ID, ErrSlipMalformed)
	case sl.MaxRuntime <= 0:
		return fmt.Errorf("creds: writing the slip %q: %w", sl.ID, ErrSlipMalformed)
	case sl.isClaimed():
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
// ErrSlipIdentity (G3 — the id alone is never enough), a slip claimed at an
// instant whose runtime has since passed is ErrSlipExpired, and then a slip the
// record already carries a claim on is ErrSlipClaimed. The expiry is asked
// before the claim flag on purpose: a dead slip is dead whatever else it is, and
// a caller reading the refusal learns the fact it can act on. What counts as
// taken is asked of the record rather than of the instant (Slip.isClaimed), so a
// claim that arrived with no instant is refused as taken rather than as expired
// — no runtime was ever measured from it — and cannot be claimed over.
//
// A claim is also where the instant enters the record, and an instant that is
// zero is refused rather than written: the record it would leave is the
// half-claim Create refuses, and one this package's readers — isClaimed, and so
// Live and the next claim — would take for an unclaimed slip.
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
		case !prev.isClaimed() && prev.ClaimDeadline != nil && !now.Before(*prev.ClaimDeadline):
			// The claim DEADLINE of an unclaimed slip (Slip.ClaimDeadline): a
			// grant nobody claimed inside its window is over, and it is refused
			// as expired rather than granted, which is what keeps a workspace's
			// grant from being claimable for ever. It is asked beside the
			// claimed slip's own expiry and BEFORE the taken-flag, for that
			// case's reason read the other way: a deadline that has passed is
			// the fact the caller can act on, and a slip that is both dead and
			// unclaimed is dead.
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipExpired)
		case prev.isClaimed():
			// The record decides, not the instant: a claim that arrived with no
			// instant is not a slip that expired — nothing measured a runtime
			// from — it is one already taken, and it is refused as taken.
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipClaimed)
		case now.IsZero():
			// A claim at the zero instant would write the half-claim Create
			// refuses, and one this package then reads as unclaimed: its expiry
			// is the zero time plus the runtime, long past, so it is never live
			// and the next claim would be granted over it. Refused here, where
			// the caller's mistake is, as the empty id is.
			return Slip{}, fmt.Errorf("creds: claiming the slip %q: %w", id, ErrSlipMalformed)
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

// The pause record (docs/model-credentials-routing, PR3): what the credentials
// service decided about one run it suspended, and the whole of what it reads
// back when that run is resumed.
//
// IT IS THE SERVICE'S OWN RECORD AND NOT THE DATABASE NODE. The node under
// runstore.PauseNodeRoot is what the orchestrator reads and what a launcher's
// view draws (R19, R26): it carries the pause and nothing else, because it is
// display data to every launcher. What a RESUME needs is the run's whole grant
// — the launcher's principal, the pool its launch named, the chain and the
// chain's paused time — and a resume is granted days after the pause it
// continues, so none of it can be held in the service's memory: Cloud Run
// scales to zero, and a service that forgot would leave a chain suspended with
// nothing to resume it. The record is therefore written where the service's own
// records already live, under the paths the database rules deny every run
// token (C1.3), and the node is derived from it.
const pathPauses = "creds/pauses"

// PathPause is the service's record of one paused run: its repository and the
// run id, both escaped for the database exactly as every other path in this
// package is (seg). It is built here because the two sides that need it are the
// service's own — the pause that writes it and the resume that reads it.
func PathPause(slug, run string) string {
	return pathPauses + "/" + seg("slug", slug) + "/" + seg("run id", run)
}

// Pause is one paused run as the service recorded it (R8, D3189, D3191). Every
// member is a fact the service itself read or computed, and none is text the
// paused run's job could have chosen: the job names nothing about its own
// pause (runstore.PausePath carries no member at all).
type Pause struct {
	// Launcher is the verified principal of the run's slip (R7): a person's
	// Google subject, or a verified service account's address. It is what the
	// resumed run's slip records as its launcher, so a run that resumes a
	// person's paused run spends that person's tokens and draws on that
	// person's pool (D3191).
	Launcher routing.Principal `json:"launcher"`
	// Pool is the pool the paused run's launch NAMED (`fugaro run --pool`),
	// empty when it named none. It is kept apart from the resumed slip's own
	// deciding pool for the reason Slip.Pool and Slip.NamedPool are: which pool
	// decided a grant and which pool a launch asked for are two facts.
	Pool string `json:"pool,omitempty"`
	// ResumeAt is the instant the service decided the pause ends, in epoch
	// milliseconds UTC — the unit the wire's resume_at and the node's resume_at
	// are in. It is this service's figure, computed when the run's call was
	// answered exhausted, and a resume is refused until it has passed.
	ResumeAt int64 `json:"resume_at"`
	// Chain is the id of the chain the paused run belongs to (runstore.Chain's
	// Root): the run the chain was rooted at, which the resumed run continues.
	// The credentials service cannot read the runs bucket the chain is walked
	// over, so it takes the chain forward the one way it can: a run whose own
	// slip names `resumes` inherits the chain of the pause it continues.
	Chain string `json:"chain"`
	// PausedSeconds is the chain's paused time so far, this pause included, the
	// unit the chain's own Time dimension is measured in (D3190). It is
	// inherited and added to the same way the chain is, and it is what bounds
	// how long one chain may spend suspended.
	PausedSeconds int64 `json:"paused_seconds,omitempty"`
	// ResumedBy is the run that continued this pause, empty while none has. It
	// is the record's mark of single use (contract-pause, "Once"): the grant of
	// a resume is what writes it, under a compare-and-set, so two asks at the
	// same instant resume a chain once.
	ResumedBy string `json:"resumed_by,omitempty"`
	// Cancelled is the mark the paused run's own launcher sets to stop the pause
	// ever being resumed (D3197). A cancelled pause is refused by every resume
	// and stays readable as the pause it was; the mark is written by the cancel
	// route and never by the run itself.
	Cancelled bool `json:"cancelled,omitempty"`
}

// The pause record's refusals, as sentinels so a caller can answer without
// reading a message. None carries a value.
var (
	// ErrNoPause is a run this service recorded no pause of its own for: an
	// ask to resume one is refused rather than answered, because the record is
	// the whole of the evidence a resume is granted on (D3189).
	ErrNoPause = errors.New("creds: this service recorded no pause of that run")
	// ErrPauseExists is a pause record already written for that run. A run
	// pauses once: a second record would be a second resume time for one pause.
	ErrPauseExists = errors.New("creds: that run has already paused")
	// ErrPauseMalformed is a record that could not be written or read as it
	// stands: a launcher that names nobody, no resume instant, a chain that is
	// empty, or a paused time before zero. Fail closed at the writer, where the
	// mistake is, rather than at a resume days later.
	ErrPauseMalformed = errors.New("creds: the pause record is not one this service could have written")
	// ErrPauseResumed is a pause that has already been resumed. A pause is
	// resumed at most once (contract-pause, "Once").
	ErrPauseResumed = errors.New("creds: that pause has already been resumed")
	// ErrPauseCancelled is a pause its launcher cancelled: nothing resumes it.
	ErrPauseCancelled = errors.New("creds: that pause was cancelled by its launcher")
	// ErrPauseEarly is a resume asked for before the instant the service
	// computed. The caller cannot move it.
	ErrPauseEarly = errors.New("creds: that pause is not due yet")
)

// checkWritable holds a pause record to the members nothing can be decided
// without.
func (p Pause) checkWritable() error {
	switch {
	case !p.Launcher.Valid():
		return fmt.Errorf("%w: it names no launcher", ErrPauseMalformed)
	case p.ResumeAt <= 0:
		return fmt.Errorf("%w: it names no resume instant", ErrPauseMalformed)
	case p.Chain == "":
		return fmt.Errorf("%w: it names no chain", ErrPauseMalformed)
	case p.PausedSeconds < 0:
		return fmt.Errorf("%w: its paused time is negative", ErrPauseMalformed)
	}
	return nil
}

// WritePause records a run's pause. It is written once, by the run's own
// /pause call and by nothing else, and a second write for the same run is
// refused rather than allowed to move a resume instant the run is already
// waiting on.
func (s *RTDBStore) WritePause(ctx context.Context, slug, run string, p Pause) error {
	if slug == "" || run == "" {
		return ErrPauseMalformed
	}
	if err := p.checkWritable(); err != nil {
		return err
	}
	path := PathPause(slug, run)
	var raw json.RawMessage
	etag, found, err := s.db().GetETag(ctx, path, &raw)
	if err != nil {
		return fmt.Errorf("creds: writing the pause of run %q: %w", run, err)
	}
	if found {
		return fmt.Errorf("creds: writing the pause of run %q: %w", run, ErrPauseExists)
	}
	if err := s.db().PutIfMatch(ctx, path, etag, p); err != nil {
		return fmt.Errorf("creds: writing the pause of run %q: %w", run, err)
	}
	return nil
}

// Pause reads one run's pause record: found is false for a run this service
// recorded no pause of. The node is decoded with the strict decoder, as every
// record in this package is and for the same reason — a member this release
// does not model would otherwise be dropped silently, and a dropped member here
// is a resume granted on half a record.
func (s *RTDBStore) Pause(ctx context.Context, slug, run string) (Pause, bool, error) {
	if slug == "" || run == "" {
		return Pause{}, false, ErrPauseMalformed
	}
	raw := json.RawMessage{}
	_, found, err := s.db().GetETag(ctx, PathPause(slug, run), &raw)
	if err != nil {
		return Pause{}, false, fmt.Errorf("creds: reading the pause of run %q: %w", run, err)
	}
	if !found {
		return Pause{}, false, nil
	}
	p, err := decodeStrict[Pause](raw)
	if err != nil {
		return Pause{}, false, fmt.Errorf("creds: reading the pause of run %q: %w", run, err)
	}
	if err := p.checkWritable(); err != nil {
		return Pause{}, false, fmt.Errorf("creds: reading the pause of run %q: %w", run, err)
	}
	return p, true, nil
}

// Resume marks a pause resumed by one run, and it is the whole of "once": the
// mark is written under a compare-and-set against the record it read, so two
// asks arriving in the same instant resume one chain once and the loser is
// refused (ErrPauseResumed). The three refusals that are not about the mark are
// asked in the same read for that reason — a pause its launcher cancelled, or
// one whose resume instant has not passed, is refused by the write that would
// have resumed it and not only by the check before it.
func (s *RTDBStore) Resume(ctx context.Context, slug, run, by string, now time.Time) error {
	if slug == "" || run == "" || by == "" {
		return ErrPauseMalformed
	}
	_, err := cas(ctx, s.db(), PathPause(slug, run), s.attempts(), func(prev Pause, found bool) (Pause, error) {
		switch {
		case !found:
			return Pause{}, ErrNoPause
		case prev.checkWritable() != nil:
			return Pause{}, ErrPauseMalformed
		case prev.Cancelled:
			return Pause{}, ErrPauseCancelled
		case prev.ResumedBy != "":
			return Pause{}, ErrPauseResumed
		case now.UnixMilli() < prev.ResumeAt:
			return Pause{}, ErrPauseEarly
		}
		prev.ResumedBy = by
		return prev, nil
	})
	return err
}
