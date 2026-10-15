package backend

import (
	"context"
	"time"
)

const (
	// DefaultFollowPoll is Follow's poll interval when the caller sets none.
	DefaultFollowPoll = 3 * time.Second
	// FollowLookback is how far before the newest entry already seen each
	// follow poll reads again. Cloud Logging can ingest an entry after a
	// newer one; the lookback catches it, and insert IDs keep it from being
	// emitted twice.
	FollowLookback = 10 * time.Second
)

// FollowOptions are one follow loop's inputs. The two backends' follow loops
// differ only in how they read an entry and how they ask whether the
// execution has ended, which is what the callbacks carry; everything about
// the window, the dedup and the settle is the same and lives in Follow.
type FollowOptions struct {
	// Since is the first read's lower bound; zero reads everything.
	Since time.Time
	// Poll is the interval between reads; zero means DefaultFollowPoll.
	Poll time.Duration
	// Settle is how long the loop keeps reading after the execution has
	// ended with nothing new arriving before it stops: it waits out Cloud
	// Logging's ingestion lag, which is not the execution's clock.
	Settle time.Duration
	// Read reads the entries at or after from, oldest first, calling fn for
	// each of them. It may be called many times.
	Read func(ctx context.Context, from time.Time, fn func(LogEntry) error) error
	// State reports whether the execution has ended: terminal for a terminal
	// state, gone for one the backend no longer has (also ended).
	State func(ctx context.Context) (terminal, gone bool, err error)
}

// Follow streams an execution's entries, oldest first, to fn. It keeps
// polling until the execution has ended and no new entry has arrived for
// Settle, or until ctx is done (it then returns ctx.Err()).
//
// Each poll re-reads from FollowLookback before the newest entry already
// seen, never from the future (one future-dated entry must not hide the
// rest), and emits an entry once: the dedup key is its insert ID and
// timestamp, and keys older than the read's own lower bound are dropped
// because no later read can return them again.
//
// Both backends follow through this, so the two cannot drift: a change to
// the window or the settle rule is one change, and internal/backend's tests
// pin both.
func Follow(ctx context.Context, o FollowOptions, fn func(LogEntry) error) error {
	poll := o.Poll
	if poll <= 0 {
		poll = DefaultFollowPoll
	}
	seen := map[string]time.Time{} // insertId|timestamp → timestamp, within the lookback
	var newest, settleFrom time.Time
	for {
		// Read from the lookback before the newest entry seen, but never
		// from the future: one future-dated entry must not hide the rest.
		from := o.Since
		if !newest.IsZero() {
			if f := minTime(newest, time.Now()).Add(-FollowLookback); f.After(from) {
				from = f
			}
		}
		fresh := 0
		err := o.Read(ctx, from, func(e LogEntry) error {
			k := e.InsertID + "|" + e.Time.Format(time.RFC3339Nano)
			if _, dup := seen[k]; dup {
				return nil
			}
			seen[k] = e.Time
			if e.Time.After(newest) {
				newest = e.Time
			}
			fresh++
			return fn(e)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		// The next read starts no earlier than this one: older keys can't recur.
		for k, t := range seen {
			if t.Before(from) {
				delete(seen, k)
			}
		}

		terminal, gone, err := o.State(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// An execution the backend has forgotten has finished: settle on its
		// logs like any ended one.
		if err != nil && !gone {
			return err
		}
		ended := gone || terminal
		now := time.Now()
		switch {
		case !ended:
			settleFrom = time.Time{}
		case settleFrom.IsZero() || fresh > 0:
			settleFrom = now // wait out Cloud Logging's ingestion lag
		case now.Sub(settleFrom) >= o.Settle:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// minTime is the earlier of a and b.
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
