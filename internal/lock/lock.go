// Package lock is the one-run-per-branch lock (design §4.1): an object
// locks/<slug>/<branch-hash> holding its holder and an expiry equal to the
// job's task timeout. Creation is create-if-absent; an expired or
// unreadable lock is taken over with a generation-matched overwrite, so two
// runners can never both take it over.
package lock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Holder is what the lock object records. Execution is the canonical
// execution name; compare it with backend.SameExecution, never ==.
type Holder struct {
	RunID     string    `json:"run_id"`
	Execution string    `json:"execution,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BusyError means a live lock is held by someone else.
type BusyError struct{ Holder Holder }

// Error names the holder and its expiry. A holder that could not be read
// back (the lock changed again after a lost takeover) is reported without them.
func (e *BusyError) Error() string {
	if e.Holder.RunID == "" {
		return "branch busy: another runner holds its lock"
	}
	return fmt.Sprintf("branch busy: run %s holds its lock until %s", e.Holder.RunID, e.Holder.ExpiresAt.Format(time.RFC3339))
}

// Stale reports whether a lock's holder is provably over, so Acquire may
// take its lock over at once instead of waiting for it to expire. rec is
// the holder's own run record (runs/<slug>/<run-id>/result.json), read the
// same way the CLI and the runner already do (runstore.Store.ReadRecord);
// nil when it could not be read at all.
//
// This is deliberately the only signal: the runner, which is the only
// place a lock is ever taken over (the CLI only reads locks/ to decide
// whether to launch, never writes it, §4.1), has no Cloud Run Admin
// credential and so can never confirm a holder's execution through the
// backend, only through its own record. A decision that used a stronger
// signal (such as the backend's execution state, which ls and diagnose do
// use to call a run failed after a kill that never finalized it) would let
// the CLI launch a run whose own lock.Acquire then finds the same lock
// still live and not stale by this test, and fails it outright: a launch
// that was never going to succeed. So the CLI's checkBranchLock calls this
// exact function too, never a more lenient one, and the two always agree.
//
// Fails closed: an unreadable or missing record, or one whose status is
// still "running", is never stale on its own.
func Stale(rec *runstore.Record) bool {
	if rec == nil {
		return false
	}
	switch rec.Status {
	case runstore.StatusSucceeded, runstore.StatusFailed, runstore.StatusInfraError, runstore.StatusCancelled, runstore.StatusHalted:
		return true
	default:
		// "running", or a record with no status yet (not written, or
		// unrecognized): fail closed.
		return false
	}
}

// Option configures Acquire.
type Option func(*acquireOptions)

type acquireOptions struct {
	stale func(Holder) bool
}

// WithStale makes Acquire take over a lock that still looks live (its
// ExpiresAt is in the future) immediately, when stale reports that its
// holder is provably over (see Stale), instead of waiting for it to
// expire. stale is never called for h's own lock, which is refreshed
// rather than taken over.
func WithStale(stale func(Holder) bool) Option {
	return func(o *acquireOptions) { o.stale = stale }
}

// beforeTakeover, when set by a test, runs between reading an expired or
// unreadable lock and the conditional overwrite that takes it over.
var beforeTakeover func()

// beforeRefresh, when set by a test, runs between reading our own lock
// and the conditional overwrite that refreshes its expiry.
var beforeRefresh func()

// Lock is a held lock.
type Lock struct {
	b    *blobx.Bucket
	key  string
	gen  int64
	body []byte
}

// Key is the lock object for branch in slug's prefix.
func Key(slug, branch string) string {
	sum := sha256.Sum256([]byte(branch))
	return "locks/" + slug + "/" + hex.EncodeToString(sum[:8])
}

// Acquire takes the lock at key for h, taking over one that expired before
// now or that cannot be parsed. A lock already held by h's run and
// execution (compared with backend.SameExecution, so a run without an
// execution never matches) is h's own and is returned as acquired.
func Acquire(ctx context.Context, b *blobx.Bucket, key string, h Holder, now time.Time, opts ...Option) (*Lock, error) {
	var o acquireOptions
	for _, opt := range opts {
		opt(&o)
	}
	body, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ { // one retry: the holder may release between our calls
		gen, err := b.Create(ctx, key, body, "application/json")
		if err == nil {
			return &Lock{b: b, key: key, gen: gen, body: body}, nil
		}
		if !errors.Is(err, blobx.ErrExists) {
			return nil, fmt.Errorf("creating lock %s: %w", key, err)
		}
		prev, prevGen, err := b.Read(ctx, key)
		if errors.Is(err, blobx.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading lock %s: %w", key, err)
		}
		var cur Holder
		parsed := json.Unmarshal(prev, &cur) == nil
		if parsed && cur.RunID != "" && cur.RunID == h.RunID && backend.SameExecution(cur.Execution, h.Execution) {
			// Our own lock: the storage client retried a create whose first
			// attempt committed (the body is identical), or this execution
			// restarted. A restart has a later start and so a later expiry,
			// which the lock is refreshed to, generation-matched.
			if bytes.Equal(prev, body) {
				return &Lock{b: b, key: key, gen: prevGen, body: prev}, nil
			}
			if beforeRefresh != nil {
				beforeRefresh()
			}
			newGen, err := b.ReplaceIf(ctx, key, body, prevGen, prev)
			if errors.Is(err, blobx.ErrConflict) {
				// Changed since the read. It may be our own refresh, whose
				// response was lost and whose retry then conflicted: read
				// again, which adopts an identical body or reports the
				// real holder.
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("refreshing lock %s: %w", key, err)
			}
			return &Lock{b: b, key: key, gen: newGen, body: body}, nil
		}
		if parsed && cur.RunID != "" && now.Before(cur.ExpiresAt) && (o.stale == nil || !o.stale(cur)) {
			return nil, &BusyError{Holder: cur}
		}
		if beforeTakeover != nil {
			beforeTakeover()
		}
		newGen, err := b.ReplaceIf(ctx, key, body, prevGen, prev)
		if errors.Is(err, blobx.ErrConflict) {
			return nil, &BusyError{Holder: winner(ctx, b, key)} // another runner took it over first
		}
		if err != nil {
			return nil, fmt.Errorf("taking over lock %s: %w", key, err)
		}
		return &Lock{b: b, key: key, gen: newGen, body: body}, nil
	}
	return nil, fmt.Errorf("lock %s kept changing; giving up", key)
}

// winner reads back the holder that beat us to a takeover. It returns the
// zero Holder when the lock is gone or unreadable again; BusyError words
// that case without a name.
func winner(ctx context.Context, b *blobx.Bucket, key string) Holder {
	var h Holder
	if data, _, err := b.Read(ctx, key); err == nil && json.Unmarshal(data, &h) == nil {
		return h
	}
	return Holder{}
}

// Release deletes the lock if this holder still has it. A lock that was
// taken over (it expired) is left alone.
func (l *Lock) Release(ctx context.Context) error {
	err := l.b.DeleteIf(ctx, l.key, l.gen, l.body)
	if errors.Is(err, blobx.ErrConflict) {
		return nil
	}
	return err
}
