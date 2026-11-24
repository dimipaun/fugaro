package runindex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The index's writer lease (F39, D3215): `runIndex/lease` in the budget
// database.
//
// The index has one writer at a time, and the summary is why. The sweep folds
// what changed since `folded_through` and moves the summary's counts and money
// by each run's own delta, and a reindex (RI5) walks history and recomputes
// them from the documents; two writers folding one run would each add the same
// delta to a figure they both read, and the summary would carry a run twice --
// a figure no reader can tell from a right one. The lease is what makes "one
// writer" true rather than likely.
//
// It is the tree's other leases' shape (internal/orchestrator/lease.go,
// internal/mailbox/lease.go): a node naming a holder and the server time of
// the write, taken with an ETag compare-and-swap, so two writers that both
// read a free node cannot both take it, and never stamped from a local clock,
// because a machine whose clock is wrong must not hold the index for ever.
const (
	// PathLease is the lease node, `runIndex/lease`.
	PathLease = "runIndex/lease"
	// LeaseTTL is how long a claim stands without the writer that took it: at
	// least twice the longest execution that may hold it (the rollover's own
	// 1800s, deploy/terraform/gcp/modules/installation/history.tf, whose
	// Scheduler call overrides the job's 600s), so a dead execution's lease
	// lapses instead of stopping the fold for ever.
	LeaseTTL = 60 * time.Minute
	// leaseAttempts bounds the re-read a lost race is answered with: a claim
	// or a release re-reads and re-decides this many times and then reports
	// what kept refusing it.
	leaseAttempts = 5
)

// LeaseDoc is the lease node on the wire: who holds it, when the server saw
// that write, when it lapses, and -- while a reindex walk is unfinished -- the
// walk's own position (F39). The cursor is RI5's: a sweep that takes a free
// lease keeps whatever it finds there and walks nothing.
type LeaseDoc struct {
	Holder    string    `json:"holder"`
	At        time.Time `json:"at"`
	ExpiresAt time.Time `json:"expires_at"`
	Cursor    string    `json:"cursor,omitempty"`
}

// CAS is the database surface the lease is taken through. It is rtdb.Client's
// own two methods.
type CAS interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// ServerNow is the database's clock (rtdb.Client.ServerNow): the moment the
// server answered with, and false when it was not seen at all.
type ServerNow func() (time.Time, bool)

// Lease is one writer's claim on the index.
type Lease struct {
	db     CAS
	now    ServerNow
	holder string
}

// NewLease builds a claim on the index for holder: the name of the execution
// that will write (F39), so a lease a person reads names a job run rather than
// a process. A lease with no holder is refused rather than taken: a node
// naming nobody is not a claim, and the next writer would have nothing to
// report but a free node it cannot tell from a lapsed one.
func NewLease(db CAS, holder string, now ServerNow) (*Lease, error) {
	if db == nil {
		return nil, errors.New("runindex: a lease needs a database")
	}
	if now == nil {
		return nil, errors.New("runindex: a lease needs the database's clock")
	}
	if holder == "" {
		return nil, errors.New("runindex: a lease needs a holder (the execution's name)")
	}
	return &Lease{db: db, now: now, holder: holder}, nil
}

// Acquire takes the lease, or reports the claim that is in the way.
//
// held is false when another writer's claim is still fresh, and the document
// it returns is that claim: the caller skips the fold and says who holds it.
// A claim that has lapsed -- its holder died, or took longer than the TTL --
// is taken over, and so is a node nothing has written. The write carries the
// ETag of the read it decided from, so of two writers that both read a free
// node exactly one lands; the loser re-reads and re-decides, a bounded number
// of times, and never writes without a fresh read.
//
// The claim that lands keeps the cursor it found (F39): the cursor belongs to
// the walk that is unfinished, and a writer taking a lapsed lease must not
// erase a position it is not walking from.
func (l *Lease) Acquire(ctx context.Context) (LeaseDoc, bool, error) {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		// The read comes first: the database's clock is the Date header of a
		// response, and before this one no response has been seen at all.
		var cur LeaseDoc
		etag, found, err := l.db.GetETag(ctx, PathLease, &cur)
		if err != nil {
			return LeaseDoc{}, false, err
		}
		srv, ok := l.now()
		if !ok {
			return LeaseDoc{}, false, errors.New("runindex: the database's clock was not seen (no Date header), so a claim cannot be judged fresh or lapsed; nothing was folded")
		}
		if found && cur.ExpiresAt.After(srv) {
			return cur, false, nil
		}
		claim := LeaseDoc{Holder: l.holder, At: srv.UTC(), ExpiresAt: srv.UTC().Add(LeaseTTL), Cursor: cur.Cursor}
		err = l.db.PutIfMatch(ctx, PathLease, etag, claim)
		if err == nil {
			return claim, true, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return LeaseDoc{}, false, err
		}
		last = err
	}
	return LeaseDoc{}, false, fmt.Errorf("runindex: %d claims, each refused by a moved ETag: %w", leaseAttempts, last)
}

// Stop gives the lease up with the walk's position in it: the node keeps
// cursor and carries an expiry in the past, so the index is free (no execution
// holds it) while the unfinished walk is still there for the next sweep to
// continue (F39, RI5).
//
// It is not Release: a released node is deleted, and the position would go
// with it -- the next writer would begin at the top of history and re-upsert
// everything this pass already did, and a walk cut off by its own timeout
// would never finish. It is not Acquire either: a claim left fresh would stop
// every sweep behind a writer that has already ended, which is the one state
// the lease's expiry exists to end.
//
// at is the stamp Acquire returned. A node that has moved on -- the claim
// lapsed and another writer took it, or it was freed -- is left alone: another
// writer is walking, and its position is not this one's to overwrite.
func (l *Lease) Stop(ctx context.Context, at time.Time, cursor string) error {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		var cur LeaseDoc
		etag, found, err := l.db.GetETag(ctx, PathLease, &cur)
		if err != nil {
			return err
		}
		if !found || cur.Holder != l.holder || !cur.At.Equal(at) {
			return nil
		}
		srv, ok := l.now()
		if !ok {
			return errors.New("runindex: the database's clock was not seen (no Date header), so the walk's position cannot be left with a lapsed lease; the pass stops with the lease as it stands")
		}
		// The write's own stamp is the claim's, so a release that a later pass
		// reads as this one's compares equal and only this holder may stop it.
		//
		// The expiry is a minute behind the server's clock and not on it: the
		// Date header has one-second resolution and ServerNow lags the true
		// time by it, so an expiry written at `srv` would be judged fresh by a
		// reader whose own `srv` is a few microseconds behind it -- the walk
		// would stop behind a writer that has already ended, which is the one
		// state the expiry exists to end.
		err = l.db.PutIfMatch(ctx, PathLease, etag, LeaseDoc{Holder: l.holder, At: cur.At, ExpiresAt: srv.UTC().Add(-time.Minute), Cursor: cursor})
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return err
		}
		last = err
	}
	return fmt.Errorf("runindex: %d stops, each refused by a moved ETag: %w", leaseAttempts, last)
}

// Reset leaves the node naming the reset's own requester, holding nothing and
// carrying no cursor (RI5, U3): the walk every later sweep continues begins at
// the top of history, and a cursor an earlier walk left is gone rather than
// resumed -- resuming it would skip everything before it, which is exactly
// what the operator asked to have redone.
//
// The node holds nothing because the pass it asks for has not started yet: the
// expiry is a minute behind the server's clock (the writer lease's own rule,
// Stop), so the execution the reset is followed by takes the node over rather
// than waiting out a TTL on a claim nobody is using.
//
// A fresh claim is left exactly as it stands and returned with held=true:
// stamping over a claim a running writer is using would tell it its own lease
// had moved. held says the node was not rewritten, and nothing more: the
// sweep's own fold holds this lease too, so which writer the claim belongs to
// is the caller's to read from the summary (ResetReindex).
func (l *Lease) Reset(ctx context.Context) (claim LeaseDoc, held bool, err error) {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		var cur LeaseDoc
		etag, found, err := l.db.GetETag(ctx, PathLease, &cur)
		if err != nil {
			return LeaseDoc{}, false, err
		}
		srv, ok := l.now()
		if !ok {
			return LeaseDoc{}, false, errors.New("runindex: the database's clock was not seen (no Date header), so a claim cannot be judged fresh or lapsed; nothing was reset")
		}
		if found && cur.ExpiresAt.After(srv) {
			return cur, true, nil
		}
		claim = LeaseDoc{Holder: l.holder, At: srv.UTC(), ExpiresAt: srv.UTC().Add(-time.Minute)}
		err = l.db.PutIfMatch(ctx, PathLease, etag, claim)
		if err == nil {
			return claim, false, nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return LeaseDoc{}, false, err
		}
		last = err
	}
	return LeaseDoc{}, false, fmt.Errorf("runindex: %d resets, each refused by a moved ETag: %w", leaseAttempts, last)
}

// Release frees the lease this writer took, and only if the node still names
// that claim.
//
// at is the stamp Acquire returned. A node that has moved on -- the claim
// lapsed and another writer took it, or it was already freed -- is left alone:
// deleting it would hand the index to a third writer while the one that holds
// it is folding. The comparison is the whole claim and not the holder, because
// two executions of one job have one holder and two claims.
func (l *Lease) Release(ctx context.Context, at time.Time) error {
	var last error
	for attempt := 0; attempt < leaseAttempts; attempt++ {
		var cur LeaseDoc
		etag, found, err := l.db.GetETag(ctx, PathLease, &cur)
		if err != nil {
			return err
		}
		if !found || cur.Holder != l.holder || !cur.At.Equal(at) {
			return nil
		}
		err = l.db.PutIfMatch(ctx, PathLease, etag, nil)
		if err == nil {
			return nil
		}
		if !errors.Is(err, rtdb.ErrPrecondition) {
			return err
		}
		last = err
	}
	return fmt.Errorf("runindex: %d releases, each refused by a moved ETag: %w", leaseAttempts, last)
}
