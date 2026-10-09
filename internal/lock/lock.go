// Package lock is the one-run-per-branch lock (design §4.1): an object
// locks/<slug>/<branch-hash> holding its holder and an expiry equal to the
// job's task timeout. Creation is create-if-absent; an expired or
// unreadable lock is taken over with a generation-matched overwrite, so two
// runners can never both take it over.
//
// Acquire, the runner's own takeover, trusts only the lock object itself
// (its ExpiresAt): never a runs/ record, which a launcher can write for any
// run (design bucket-iam.md §10, L3) and so can forge. The CLI's own,
// separate way to clear a live lock before it expires (Takeover) trusts
// only the backend's view of the holder's execution, which a launcher
// cannot forge; see Stale.
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

// Stale reports whether a live lock may be cleared before its own expiry,
// by the one signal a launcher cannot forge: executionTerminal is true
// only when the caller has independently confirmed, through the backend,
// that holder.Execution (compared with backend.SameExecution, so the
// comparison is the caller's to make, not a string-equals here) is
// terminal — the same backend.Execution.State.Terminal() signal ls and
// diagnose use to call a run infra_error after a kill that never
// finalized it (runview.Join). A holder with no execution named, or no
// such confirmation, is never stale.
//
// This is deliberately the CLI's decision alone (Takeover), never
// Acquire's: the runner has no Cloud Run Admin credential to confirm any
// execution's state, holder or its own, so Acquire goes only by the lock
// object's own ExpiresAt (and MarkReleasing, its holder's self-release).
// A decision inside Acquire that trusted anything else — a runs/ record
// above all, which any launcher may write for any run (bucket-iam.md
// §10, L3) — would let a forged "succeeded" record free a live run's
// lock to a second launch: exactly what the 0.7.0 bucket hardening closes
// by making locks/ launcher-unwritable.
func Stale(holder Holder, executionTerminal bool) bool {
	return holder.Execution != "" && executionTerminal
}

// ErrHolderChanged means Takeover's lock, re-read immediately before the
// delete, is no longer the one its caller confirmed stale: another
// takeover, a refresh, or a new run on the branch landed first. The lock
// is left alone; the caller should treat the branch as busy.
var ErrHolderChanged = errors.New("the lock's holder changed since it was checked")

// beforeTakeoverDelete, when set by a test, runs between Takeover's fresh
// read and its conditional delete.
var beforeTakeoverDelete func()

// Takeover clears a lock whose holder's termination the caller has already
// confirmed through the backend (Stale): the one way a launcher may free a
// branch before its lock expires. It re-reads the lock immediately before
// deleting it and requires the fresh holder to still be, by RunID and
// execution (backend.SameExecution), the one holder names — ErrHolderChanged
// otherwise, so a lock that changed in between (another takeover, a
// refresh, a new run) is never deleted out from under its new state. The
// generation matched is the one this fresh read returns, never an earlier
// one. A lock already gone is success: the branch is free either way. Any
// other failure, a storage 403 under the 0.7.0 bucket hardening included
// (locks/ is not launcher-writable there), is returned as itself.
func Takeover(ctx context.Context, b *blobx.Bucket, key string, holder Holder) error {
	data, gen, err := b.Read(ctx, key)
	if errors.Is(err, blobx.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cur Holder
	if json.Unmarshal(data, &cur) != nil || cur.RunID != holder.RunID || !backend.SameExecution(cur.Execution, holder.Execution) {
		return ErrHolderChanged
	}
	if beforeTakeoverDelete != nil {
		beforeTakeoverDelete()
	}
	err = b.DeleteIf(ctx, key, gen, data)
	if errors.Is(err, blobx.ErrConflict) {
		return ErrHolderChanged
	}
	return err
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
//
// This is the only test Acquire ever applies: it trusts nothing under
// runs/, which a launcher may write for any run (bucket-iam.md §10, L3)
// and so could use to forge a holder's record as finished. A holder that
// wants its own lock taken over sooner than its expiry marks it so itself,
// in the lock object, with MarkReleasing.
func Acquire(ctx context.Context, b *blobx.Bucket, key string, h Holder, now time.Time) (*Lock, error) {
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
		if parsed && cur.RunID != "" && now.Before(cur.ExpiresAt) {
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

// MarkReleasing rewrites l's lock, generation-matched, to ExpiresAt now:
// its holder is about to call Release, and this is its self-release
// marker. It exists so a container killed between this call and Release's
// delete (mid-writeback: an OOM, cancel --hard, a node loss) leaves a lock
// that Acquire already treats as expired, needing no new field an older
// runner's Acquire might not understand — ExpiresAt is the one field every
// version has always read. l's local generation and body are updated on
// success, so a Release right after still matches. A failure (the lock
// changed, or any I/O error) is returned as itself; the caller logs it and
// carries on to Release, which is unaffected by it either way.
func (l *Lock) MarkReleasing(ctx context.Context, now time.Time) error {
	var h Holder
	if err := json.Unmarshal(l.body, &h); err != nil {
		return fmt.Errorf("marking lock %s as releasing: %w", l.key, err)
	}
	h.ExpiresAt = now
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	newGen, err := l.b.ReplaceIf(ctx, l.key, body, l.gen, l.body)
	if err != nil {
		return fmt.Errorf("marking lock %s as releasing: %w", l.key, err)
	}
	l.gen, l.body = newGen, body
	return nil
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
