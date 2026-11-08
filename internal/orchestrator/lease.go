// The daemon lease: one daemon per orchestrator id (O3, D1232).
//
// It is the mailbox's lease (internal/mailbox/lease.go, ruling M1) on the
// other subtree, and it takes the same two things from it: **the lease IS the
// heartbeat** — one node naming a holder and the server time of that holder's
// last beat, no fourth node and no separate grant — and **the ETag is the
// fence**, so two daemons that both read a free lease cannot both take it.
// Nothing here reads a clock of its own: the stamp is rtdb.ServerNow's, so a
// laptop whose clock is wrong cannot hold an orchestrator id for ever, and a
// claim with no server time behind it is not made at all.
//
// The lease decides **who writes**, never **what may be written**: the state's
// compare-and-swap is what serialises intents, because a daemon that has lost
// its lease still holds a fresh state ETag (G9, and that is why Node.Save
// checks this lease before the one conditional write it makes). What the lease buys is
// that two machines do not both drive the same id: a second daemon refuses to
// start at all, so the person's second laptop is told rather than quietly
// racing.

package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The lease's timing. The beat is a third of the TTL, so a holder keeps its
// claim across two missed beats — a tick that ran long, a request that timed
// out — while a holder that stops beating loses it inside a minute and a
// half: long enough that a laptop lid does not hand the id to another machine
// mid-tick, short enough that a dead daemon does not hold it for a day.
const (
	// LeaseTTL is how long a claim stays fresh without a beat.
	LeaseTTL = 90 * time.Second
	// LeaseBeat is how often a holder must beat to keep it.
	LeaseBeat = 30 * time.Second
	// LeaseSkew bounds how far ahead of this daemon's own clock a claim may be
	// stamped and still be honoured. Two clocks disagreeing slightly is not a
	// dead holder, but a stamp far in the future would hold the orchestrator id
	// until it arrived, so beyond this the claim is treated as expired — and
	// taken over by a daemon that does beat.
	LeaseSkew = 30 * time.Second
)

// leaseAttempts bounds the re-read a lost lease race is answered with: a claim,
// a beat or a release re-reads and re-decides this many times and then reports
// ErrLeaseContention. The state node has no such bound — its Save writes once
// (store.go) — because a state body is derived from the document the caller
// read, so a second attempt would write over one the caller never saw.
const leaseAttempts = 5

// leaseDoc is the lease node's shape on the wire: the holder and the server
// time of its last beat. testdata/lease-a.json is this shape written by
// another program, and reading it is what keeps the two ends from drifting.
type leaseDoc struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}

// LeaseState is what a daemon is told about one orchestrator id: whether
// **this** daemon holds a fresh lease on it right now, who the node names (a
// name that is this daemon's whenever Held is true), and when that holder last
// beat. Held is deliberately about this daemon and not about the node: a
// caller that had to compare Holder itself would be one comparison away from
// acting on another machine's lease, so the only safe reading is the one that
// fails closed.
type LeaseState struct {
	Held   bool
	Holder string
	At     time.Time
}

// claim is what the node says before this daemon is asked about it: who it
// names and whether that name's stamp is still fresh.
type claim struct {
	holder string
	at     time.Time
	fresh  bool
}

// Lease is one daemon's claim on one orchestrator id. Holder is the daemon,
// not the orchestrator: a run id (task.NewRunID), so a takeover is visible in
// the node as a change of holder rather than as a gap.
type Lease struct {
	db      CAS
	agentID string
	holder  string
	path    string
	now     ServerNow
}

// The guard Node.Save consults is this type: a lease that stopped satisfying it
// would make that write unfenced.
var _ LeaseGuard = (*Lease)(nil)

// NewLease builds the claim of one daemon on one orchestrator id.
func NewLease(db CAS, agentID, holder string, now ServerNow) (*Lease, error) {
	if db == nil {
		return nil, errors.New("orchestrator: a lease needs a store to be read and written through")
	}
	if now == nil {
		return nil, errors.New("orchestrator: a lease needs the server's clock")
	}
	if !idRE.MatchString(holder) {
		return nil, fmt.Errorf("%w: the lease holder %q is not a name", ErrInvalid, safetext.OneLine(holder))
	}
	path, err := PathLease(agentID)
	if err != nil {
		return nil, err
	}
	return &Lease{db: db, agentID: agentID, holder: holder, path: path, now: now}, nil
}

// Path returns the lease node's path, `orchestrator/<id>/lease`.
func (l *Lease) Path() string { return l.path }

// Acquire takes the lease, or renews it if this daemon already holds it.
//
// Another daemon's fresh claim is not an error and not a write: this daemon is
// told who holds the orchestrator id (Held false, Holder set) and acts on
// nothing. A claim whose stamp has gone stale — the holder stopped beating —
// is taken over, and a claim stamped further ahead than LeaseSkew is treated
// as expired rather than honoured, so no stamp holds an id for ever. The write
// carries the ETag of the read it decided from, so of two daemons that both
// read a free lease exactly one lands; the loser re-reads and re-decides, a
// bounded number of times (ErrLeaseContention), and never writes again without
// a fresh read.
func (l *Lease) Acquire(ctx context.Context) (LeaseState, error) {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		etag, current, err := l.read(ctx)
		if err != nil {
			return LeaseState{}, err
		}
		if current.fresh && current.holder != l.holder {
			return l.state(current), nil
		}
		at, err := l.stamped()
		if err != nil {
			return LeaseState{}, err
		}
		err = l.db.PutIfMatch(ctx, l.path, etag, leaseDoc{Holder: l.holder, At: at})
		if err == nil {
			return LeaseState{Held: true, Holder: l.holder, At: at}, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return LeaseState{}, err
		}
		last = err
	}
	return LeaseState{}, fmt.Errorf("%w: %d claims, each refused by a moved ETag: %w", ErrLeaseContention, leaseAttempts, last)
}

// Beat renews the lease this daemon holds, and is how the fence expires for a
// daemon that stops beating.
//
// It is deliberately narrower than Acquire: a node that does not name this
// daemon is ErrLeaseLost and nothing is written, however stale that other
// claim is. A daemon whose lease went to another machine must stop (O8), not
// take it back on its next tick; taking an id over is a start, and a start is
// Acquire.
func (l *Lease) Beat(ctx context.Context) error {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		etag, current, err := l.read(ctx)
		if err != nil {
			return err
		}
		if current.holder != l.holder {
			return fmt.Errorf("%w: the lease node names %q", ErrLeaseLost, safetext.OneLine(current.holder))
		}
		at, err := l.stamped()
		if err != nil {
			return err
		}
		err = l.db.PutIfMatch(ctx, l.path, etag, leaseDoc{Holder: l.holder, At: at})
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return err
		}
		last = err
	}
	return fmt.Errorf("%w: %d beats, each refused by a moved ETag: %w", ErrLeaseContention, leaseAttempts, last)
}

// Read is what the lease node says now, whoever holds it. It is the read a
// daemon's status reports (O9) and the one a person's command answers "who is
// driving this id" with.
func (l *Lease) Read(ctx context.Context) (LeaseState, error) {
	_, current, err := l.read(ctx)
	if err != nil {
		return LeaseState{}, err
	}
	return l.state(current), nil
}

// Held is the guard Node.Save re-checks: does **this** daemon hold a fresh
// lease on this id right now. A node it cannot read is an error and never a
// yes — a daemon that cannot tell is a daemon that does not write.
func (l *Lease) Held(ctx context.Context) (bool, error) {
	state, err := l.Read(ctx)
	if err != nil {
		return false, err
	}
	return state.Held, nil
}

// Release frees the lease a clean exit leaves behind (O8 exits 0 on an upgrade
// so the service manager starts the new binary), so the next daemon starts
// immediately instead of waiting out the TTL. Only this daemon's own claim is
// released: a node naming someone else is left alone (false, nil), because
// releasing another daemon's lease is how two daemons end up on one id.
func (l *Lease) Release(ctx context.Context) (bool, error) {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		etag, current, err := l.read(ctx)
		if err != nil {
			return false, err
		}
		if current.holder != l.holder {
			return false, nil
		}
		err = l.db.PutIfMatch(ctx, l.path, etag, nil)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return false, err
		}
		last = err
	}
	return false, fmt.Errorf("%w: %d releases, each refused by a moved ETag: %w", ErrLeaseContention, leaseAttempts, last)
}

// read is one conditional read of the node: the ETag a claim would carry and
// what the node says.
func (l *Lease) read(ctx context.Context) (string, claim, error) {
	var raw json.RawMessage
	etag, found, err := l.db.GetETag(ctx, l.path, &raw)
	if err != nil {
		return "", claim{}, err
	}
	current, err := l.interpret(found, raw)
	if err != nil {
		return "", claim{}, err
	}
	return etag, current, nil
}

// state is one claim as this daemon is told about it.
func (l *Lease) state(c claim) LeaseState {
	return LeaseState{Held: c.fresh && c.holder == l.holder, Holder: c.holder, At: c.at}
}

// interpret reads the node and decides whether the claim it carries is still
// fresh. Anything this release cannot read is a refusal (fail closed): a lease
// node a future release wrote, or one a hand-edit broke, must not be read as
// "nobody holds it" — that is how two daemons take one id.
func (l *Lease) interpret(found bool, raw json.RawMessage) (claim, error) {
	if !found {
		return claim{}, nil
	}
	doc, err := decodeStrict[leaseDoc](raw)
	if err != nil {
		return claim{}, err
	}
	if !idRE.MatchString(doc.Holder) {
		return claim{}, fmt.Errorf("%w: the lease names %q, which is not a holder", ErrInvalid, safetext.OneLine(doc.Holder))
	}
	now, ok := l.now()
	if !ok {
		return claim{}, ErrNoServerTime
	}
	at := stamp(doc.At)
	return claim{
		holder: doc.Holder,
		at:     at,
		fresh:  now.Sub(at) < LeaseTTL && at.Sub(now) <= LeaseSkew,
	}, nil
}

// stamped is the server's time as this subtree records it, and a refusal when
// the clock has never answered.
func (l *Lease) stamped() (time.Time, error) {
	now, ok := l.now()
	if !ok {
		return time.Time{}, ErrNoServerTime
	}
	return stamp(now), nil
}
