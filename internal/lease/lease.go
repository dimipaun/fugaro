// Package lease is the ownership record of one job's shepherd: the seat an
// orchestrator holds while it acts on a job, and the compare-and-set that
// hands the seat to another orchestrator (central-orchestrator plan P3,
// decisions D1, D2, D3).
//
// The invariant is one job, one shepherd: two orchestrators never act on the
// same PR at the same time. Three things enforce it, and each one is the
// answer to a way the invariant fails:
//
//  1. **The fencing token.** Every record carries a `generation` that only
//     ever moves forward, and a holder acts on the generation it holds. A
//     holder whose generation has moved on is refused, and the refusal is a
//     typed value (LostError), never a silent continue: the local
//     orchestrator learns it lost the seat from the return value of the call
//     it was making, not from a subscription it may not have.
//  2. **The compare-and-set.** Every write reads the node's ETag and writes
//     back with PutIfMatch, so of two orchestrators racing for one seat
//     exactly one wins and the other is told it lost. internal/rtdb has no
//     transaction (reconciliation #8), so the ETag *is* the compare-and-set.
//     A lease is never written with an unconditional Put: that would clobber
//     a takeover that landed between this caller's read and its write, which
//     is precisely two orchestrators acting on one job.
//  3. **The database's clock.** Expiry is `renewed_at + grace`, where
//     `renewed_at` is read from the database's own clock
//     (rtdb.Client.ServerNow, the Date header of the most recent response)
//     and the comparison is made against that same clock. The writer stores
//     no deadline it computed itself, and there is no expiry field to store
//     one in (D3): a machine whose clock is an hour wrong cannot make its
//     lease look fresh or stale, and the deployed rules refuse a
//     `renewed_at` ahead of the database's own `now`. This package never
//     reads the local clock (TestThePackageNeverReadsTheLocalClock).
//
// What this package does not do, stated plainly:
//
//   - It does not decide who *may* hold a seat. The deployed rules grant the
//     subtree to the project's user claim (the device allowlist, D6); the
//     identity an orchestrator's actions are attributed to is P2's access
//     record and P5's authenticated session, neither of which is built here.
//   - It does not carry the generation into the actions a shepherd takes
//     while it holds the seat. D2's second half — every later action carries
//     the generation it holds and is refused when it is stale — needs an
//     action that a generation can be attached to, which is the resident
//     service's (P4) and the protocol's (P5).
//   - It does not watch. A local orchestrator notices a takeover by calling
//     Renew (or any other guarded call) and reading the typed refusal; the
//     stream `fugaro watch` would carry it on is D15, which is still open.
package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// DefaultHeartbeat is how often a holder renews its lease (D2): the
	// shepherd's own loop calls Renew this often, and a holder that has not
	// renewed for DefaultGrace has lost the seat. It is the number the plan
	// recommends and the one a caller uses unless it has a reason not to.
	DefaultHeartbeat = 60 * time.Second

	// DefaultGrace is how long a lease stays valid without a renewal (D2,
	// and the spec's resolved "Grace period": 10 minutes). It is the same
	// number runstore.ClaimTTL, shared/lease's CLAIM_TTL and the spec all
	// already use (reconciliation #12): one number, one meaning, three
	// mechanisms that all mean "this owner has gone quiet".
	//
	// The deployed rules bake this number in as well
	// (internal/budget/rules's LeaseGraceMs, which the template renders and
	// TestTheRulesCarryTheLeaseAndTheGraceWeApply holds to this constant): the
	// database refuses a takeover of a lease that is still fresh, so a client
	// that disagreed about the grace could not take a seat the database
	// considers held. The number therefore cannot be a per-call choice — a
	// shorter grace here would only mean this process attempts takeovers the
	// database refuses, and the caller would get a bare rtdb.ErrPermission
	// where this package promises a typed *HeldError. Options has no Grace
	// field for that reason.
	DefaultGrace = 10 * time.Minute

	// MaxString bounds a holder's name, its uid and the recorded mode. It is
	// internal/budget/rules's MaxString, the bound the deployed rules apply
	// to every registry string (TestTheRulesCarryTheLeaseAndTheGraceWeApply
	// holds the two together): a longer one is a record the database refuses,
	// and refusing it here says so before the write is sent.
	MaxString = 200
)

// ErrHeld is the sentinel of a refused takeover: another orchestrator holds a
// lease that has not expired. errors.Is(err, ErrHeld) is true of every
// *HeldError.
var ErrHeld = errors.New("lease: held by another orchestrator")

// ErrLost is the sentinel of a lost seat: the generation moved under the
// caller. errors.Is(err, ErrLost) is true of every *LostError. A caller that
// sees it must stop acting on the job — this is the "never a silent continue"
// half of D15.
var ErrLost = errors.New("lease: the seat is gone")

// ErrNoClock is the refusal to judge a lease with a clock that is not the
// database's: the client has not yet seen a response from the database, so it
// does not know what time the database thinks it is. It fails closed rather
// than falling back to the local clock, which is the whole point of D3.
var ErrNoClock = errors.New("lease: the database has not told us its clock")

// ErrNoHolder is the refusal of a holder that is not a named identity: an
// empty name or an empty uid (D3: holders are named identities, never
// host/pid/rand).
var ErrNoHolder = errors.New("lease: a holder needs a name and a uid")

// Path is the lease of one job: orchestrators/<slug>/<job>/lease (D1). The
// slug and the job id are escaped as database keys (budget.Key), as every
// other path in the tree is, and an empty one panics: an empty segment would
// collapse the path onto another node (budget.PathRun does the same).
func Path(slug, job string) string {
	if slug == "" {
		panic("lease: empty slug in a database path")
	}
	if job == "" {
		panic("lease: empty job id in a database path")
	}
	return "orchestrators/" + budget.Key(slug) + "/" + budget.Key(job) + "/lease"
}

// Holder is the named identity that takes a seat: the name a person reads
// ("dimi@example.com on the laptop", "the resident orchestrator"), and the
// authenticated uid behind it. Both are required: D3's ruling is that a
// holder is a named identity, never `host/pid/rand` (the launch claim's
// claimHolder), and the deployed rules require the recorded holder_uid to
// equal the writer's own auth.uid — so the uid half is the database's to
// verify and cannot be self-asserted, while the name is what the record is
// read by.
type Holder struct {
	Name string
	UID  string
}

// validate refuses a holder that is not a named identity, and a name the
// deployed rules would refuse (too long, or carrying characters that are not
// text).
func (h Holder) validate() error {
	if strings.TrimSpace(h.Name) == "" || strings.TrimSpace(h.UID) == "" {
		return ErrNoHolder
	}
	for _, s := range []string{h.Name, h.UID} {
		if len(s) > MaxString {
			return fmt.Errorf("lease: %q is longer than %d characters, which the database's rules refuse", s, MaxString)
		}
		if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) }) {
			return fmt.Errorf("lease: %q carries control or format characters: a holder's name is text a person reads", s)
		}
	}
	return nil
}

// Record is what the database holds at a lease's path (D1): the seat, the
// identity in it and the fencing token. The JSON field names are the deployed
// rules' field names (rules.json.tmpl's orchestrators family), and the rules
// refuse a record with any other field ($other: false).
//
// There is deliberately no expiry field. D3's ruling is that the writer
// stores no absolute deadline it computed itself, and the shape that cannot
// break that ruling is a shape with nowhere to put one: a lease is stale when
// the database's clock has moved Grace past RenewedAt, and every reader —
// this package and the deployed rules alike — applies that one rule to the
// same two numbers. A `deadline` a writer computed would be a number the
// writer's own clock produced, which is exactly the input D3 removes.
type Record struct {
	// Holder is the name of the orchestrator holding the seat, and HolderUID
	// the authenticated identity behind it. Empty HolderUID means the seat is
	// free: Release writes that, and it is why a released lease is not
	// deleted (the generation must not restart, D15).
	Holder    string `json:"holder"`
	HolderUID string `json:"holder_uid"`

	// Generation is the fencing token. It only ever moves forward, by one per
	// takeover or release, and it never restarts: a holder's actions are
	// judged against the generation it holds.
	Generation int64 `json:"generation"`

	// RenewedAt is when the database's clock read the last write, in epoch
	// milliseconds. It is a reading, not a deadline: see the type's comment.
	RenewedAt int64 `json:"renewed_at"`

	// Mode and PR are what the shepherd is doing, recorded on the lease (D1's
	// `mode`, D2's "the PR recorded on the lease"). They are attribution and
	// nothing reads them to make a decision.
	Mode string `json:"mode,omitempty"`
	PR   int64  `json:"pr,omitempty"`
}

// Renewed is RenewedAt as a time.
func (r Record) Renewed() time.Time { return time.UnixMilli(r.RenewedAt).UTC() }

// Free reports whether the seat is empty: a released lease (Release writes
// an empty HolderUID and keeps the generation moving) or an absent node.
func (r Record) Free() bool { return r.HolderUID == "" }

// Expires is when the lease stops being fresh: RenewedAt plus grace, on the
// database's clock.
func (r Record) Expires(grace time.Duration) time.Time { return r.Renewed().Add(grace) }

// Fresh reports whether the seat is still held at now, a reading of the
// database's clock. A free lease is never fresh: an empty seat is one anyone
// may take.
//
// The boundary is the deployed rules' own, and it is why the comparison is
// written as it is. The template allows a takeover only while
// `now - renewed_at > LeaseGraceMs`, so a seat whose grace has run out exactly
// now (now == renewed_at + grace) is still fresh here and a takeover of it is
// still refused by the database. A client one instant looser would attempt a
// takeover the rules refuse, and the caller would get a bare
// rtdb.ErrPermission where this package promises the typed *HeldError.
func (r Record) Fresh(now time.Time, grace time.Duration) bool {
	return !r.Free() && !now.After(r.Expires(grace))
}

// valid reports whether the record is one this code wrote. A node the rules
// would have refused — a generation below 1, a stamp before the epoch — is a
// hand-written node, and a hand-written lease is not one to take over or
// renew: the caller gets an error rather than a guess.
func (r Record) valid() error {
	switch {
	case r.Generation < 1:
		return fmt.Errorf("lease: the lease node carries generation %d, and the rules require 1 or more: it is not one fugaro wrote", r.Generation)
	case r.RenewedAt <= 0:
		return fmt.Errorf("lease: the lease node carries renewed_at %d, and the rules require a positive instant: it is not one fugaro wrote", r.RenewedAt)
	}
	return nil
}

// DB is the database a lease lives in: the two conditional calls and the
// clock, and nothing else. *rtdb.Client is one. The interface deliberately
// has no unconditional write and no delete — there is no call this package
// could make that would overwrite a lease it had not just read, or remove one
// and let its generation restart.
type DB interface {
	// GetETag reads the node and its ETag ("null_etag" when it is absent).
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	// PutIfMatch writes v (nil deletes) only while the node's ETag is still
	// etag, and answers rtdb.ErrPrecondition otherwise.
	PutIfMatch(ctx context.Context, path, etag string, v any) error
	// ServerNow is the database's own clock, and false before it has told
	// one.
	ServerNow() (time.Time, bool)
}

// Options are what a caller records on the lease beside the holder: D1's mode
// and D2's PR. The zero value records neither, which is the recommended
// setting for a job that is not carrying a pull request.
//
// There is no Grace field here on purpose. The grace is one number,
// DefaultGrace, and the deployed rules bake the same one in; a per-call grace
// could only disagree with the database it is judged by — a shorter one would
// make this process attempt takeovers the rules refuse, and the caller would
// see a bare rtdb.ErrPermission instead of the typed refusal. A caller that
// believes it needs another grace needs a rules change first, which is what
// DefaultGrace's own comment says.
type Options struct {
	// Mode is what the shepherd is doing, recorded on the lease.
	Mode string
	// PR is the pull request the job is carrying, recorded on the lease
	// (D2: per job, with the PR on the lease).
	PR int64
}

// HeldError is the typed refusal of a takeover: another orchestrator holds
// the seat. It is returned by Take when the lease is fresh, and by Take when
// a racing orchestrator wrote first.
//
// Found is nil when the caller lost the compare-and-set and the re-read could
// not name the winner (the lease was deleted under it, or the re-read failed):
// the one thing that is certain in every case is that this caller does not
// hold the seat, which is why the refusal is never a zero-value success.
type HeldError struct {
	Path  string
	Found *Record // what the database holds, nil when it could not be read back
	Now   time.Time
	Until time.Time // when the held lease stops being fresh
}

func (e *HeldError) Error() string {
	if e.Found == nil {
		return fmt.Sprintf("lease: %s: another orchestrator changed the lease first; this one does not hold it", e.Path)
	}
	left := e.Until.Sub(e.Now)
	if left < 0 {
		left = 0
	}
	return fmt.Sprintf("lease: %s: held by %s (%s) at generation %d, fresh for another %s; a takeover is refused until it expires (or the holder releases it)",
		e.Path, oneLine(e.Found.Holder), oneLine(e.Found.HolderUID), e.Found.Generation, left.Round(time.Second))
}

func (e *HeldError) Unwrap() error { return ErrHeld }

// LostError is the typed result of a lost seat: the generation moved under
// this holder, so the seat it believed it held is someone else's now. Every
// guarded call returns it — Renew, Release — and a caller that sees it must
// stop acting on the job and say so; this is what a local orchestrator sees
// when the central one takes over, and it is a return value rather than a
// subscription because a CLI that is not running cannot be pushed to (D15).
//
// Found is nil when the lease was released (or deleted) under this holder:
// nobody holds the seat, and this holder still does not.
type LostError struct {
	Path  string
	Held  Record  // the seat this caller held
	Found *Record // what the database holds now, nil when the seat was given up
}

func (e *LostError) Error() string {
	if e.Found == nil {
		return fmt.Sprintf("lease: %s: the seat is gone: it was released while this orchestrator held generation %d, so its next action would be refused; stop acting on the job",
			e.Path, e.Held.Generation)
	}
	return fmt.Sprintf("lease: %s: the seat is gone: %s (%s) holds generation %d and this orchestrator holds generation %d, so its next action would be refused; stop acting on the job",
		e.Path, oneLine(e.Found.Holder), oneLine(e.Found.HolderUID), e.Found.Generation, e.Held.Generation)
}

func (e *LostError) Unwrap() error { return ErrLost }

// oneLine is s as one line: a holder's name and uid are read in a message a
// person sees, and the rules bound them but do not stop a newline.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }), " ")
}

// Read is the lease as the database holds it. found is false for an absent
// node; a record that is not one this code wrote is an error (Record.valid).
func Read(ctx context.Context, db DB, path string) (Record, bool, error) {
	cur, _, found, _, err := read(ctx, db, path)
	return cur, found, err
}

// Take takes the seat of the job at path for h: it creates the lease when
// there is none, takes it over when the lease is stale (the database's clock
// has moved the grace past its renewed_at) or free (its holder released it),
// and refuses with *HeldError when another orchestrator holds a fresh one.
//
// The takeover is a compare-and-set: the node is read with its ETag and
// written back with PutIfMatch against that same ETag, and the winner
// increments the generation. Two orchestrators that race for one seat
// therefore both read the same ETag, exactly one write matches it, and the
// loser gets *HeldError — the invariant, in one call. The generation is what
// makes the loser's *next* action refusable: it holds the generation it read,
// and the winner's write moved it on.
func Take(ctx context.Context, db DB, path string, h Holder, o Options) (Record, error) {
	if err := h.validate(); err != nil {
		return Record{}, err
	}
	cur, etag, found, now, err := read(ctx, db, path)
	if err != nil {
		return Record{}, err
	}
	next := Record{Holder: h.Name, HolderUID: h.UID, RenewedAt: now.UnixMilli(), Mode: o.Mode, PR: o.PR}
	switch {
	case !found:
		next.Generation = 1
	case cur.Free(), !cur.Fresh(now, DefaultGrace):
		if err := cur.valid(); err != nil {
			return Record{}, err
		}
		next.Generation = cur.Generation + 1
	default:
		return Record{}, &HeldError{Path: path, Found: &cur, Now: now, Until: cur.Expires(DefaultGrace)}
	}
	if err := db.PutIfMatch(ctx, path, etag, next); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return Record{}, heldFirst(ctx, db, path, now)
		}
		return Record{}, err
	}
	return next, nil
}

// Renew is the heartbeat: it writes held back with the database's current
// time, keeping the generation, and refuses with *LostError when the seat is
// not this holder's any more — the generation moved (someone took over), the
// holder changed, or the lease was released.
//
// The record written back is held, so the holder records what it passes: a
// shepherd that has moved on to another mode sets held.Mode before it renews,
// and the new mode is on the lease from that heartbeat on. There is no Options
// argument for the same reason there is no grace one — a record is written
// from one value, not from two that could disagree.
//
// The generation check and the ETag compare-and-set answer two different
// cases, and both are needed: the check catches the takeover that happened
// before this call (the honest, common one, and the one whose refusal names
// the new holder), and the ETag catches the takeover that lands between this
// call's read and its write (the race, where nothing but the compare-and-set
// can decide).
func Renew(ctx context.Context, db DB, path string, held Record) (Record, error) {
	cur, etag, found, now, err := read(ctx, db, path)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, &LostError{Path: path, Held: held}
	}
	if cur.Generation != held.Generation || cur.HolderUID != held.HolderUID {
		return Record{}, &LostError{Path: path, Held: held, Found: &cur}
	}
	next := held
	next.Generation, next.RenewedAt = cur.Generation, now.UnixMilli()
	if err := db.PutIfMatch(ctx, path, etag, next); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return Record{}, lostAfterRace(ctx, db, path, held)
		}
		return Record{}, err
	}
	return next, nil
}

// Release gives the seat up: the lease is written with an empty holder and
// the generation moved on, so the seat is free for the next taker and the
// released holder's generation is stale at once. It refuses with *LostError
// exactly as Renew does — a holder that no longer holds the seat must not be
// able to free it from under the one that does.
//
// A release does not delete the node: the generation is the fencing token and
// it must not restart (D15's "the generation stays monotonic"), so the record
// stays and says nobody is sitting in it.
func Release(ctx context.Context, db DB, path string, held Record) error {
	cur, etag, found, now, err := read(ctx, db, path)
	if err != nil {
		return err
	}
	if !found {
		return &LostError{Path: path, Held: held}
	}
	if cur.Generation != held.Generation || cur.HolderUID != held.HolderUID {
		return &LostError{Path: path, Held: held, Found: &cur}
	}
	next := Record{Generation: cur.Generation + 1, RenewedAt: now.UnixMilli()}
	if err := db.PutIfMatch(ctx, path, etag, next); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return lostAfterRace(ctx, db, path, held)
		}
		return err
	}
	return nil
}

// read is the one read every guarded call starts with: the record, its ETag,
// whether it is there, and the database's clock. It is also why the clock is
// always available to the caller — the read's own response carries the Date
// header ServerNow projects from — and why an absent clock is an error rather
// than a fallback: a lease judged with the local clock is the skew D3 exists
// to remove.
func read(ctx context.Context, db DB, path string) (cur Record, etag string, found bool, now time.Time, err error) {
	etag, found, err = db.GetETag(ctx, path, &cur)
	if err != nil {
		return Record{}, "", false, time.Time{}, err
	}
	now, ok := db.ServerNow()
	if !ok {
		return Record{}, "", false, time.Time{}, fmt.Errorf("%w (%s)", ErrNoClock, path)
	}
	return cur, etag, found, now.UTC(), nil
}

// heldFirst is the refusal of a takeover that lost the compare-and-set. The
// re-read is what lets it name the winner; a re-read that fails, or a lease
// that has since been deleted, still means this caller does not hold the seat,
// so the refusal stands and Found is nil.
func heldFirst(ctx context.Context, db DB, path string, now time.Time) error {
	var cur Record
	_, found, err := db.GetETag(ctx, path, &cur)
	if err != nil || !found {
		return &HeldError{Path: path, Now: now}
	}
	return &HeldError{Path: path, Found: &cur, Now: now, Until: cur.Expires(DefaultGrace)}
}

// lostAfterRace is the refusal of a Renew or a Release that lost the
// compare-and-set: someone wrote between the read and the write. The re-read
// names them when it can; a re-read that fails still means this caller's seat
// is not its own.
func lostAfterRace(ctx context.Context, db DB, path string, held Record) error {
	var cur Record
	_, found, err := db.GetETag(ctx, path, &cur)
	if err != nil || !found {
		return &LostError{Path: path, Held: held}
	}
	return &LostError{Path: path, Held: held, Found: &cur}
}
