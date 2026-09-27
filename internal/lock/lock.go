// Package lock is the one-run-per-branch lock (design §4.1): an object
// locks/<slug>/<branch-hash> holding its holder and an expiry equal to the
// job's task timeout. Creation is create-if-absent; an expired or
// unreadable lock is taken over with a generation-matched overwrite, so two
// runners can never both take it over.
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// beforeTakeover, when set by a test, runs between reading an expired or
// unreadable lock and the conditional overwrite that takes it over.
var beforeTakeover func()

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
// now or that cannot be parsed.
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
		if json.Unmarshal(prev, &cur) == nil && cur.RunID != "" && now.Before(cur.ExpiresAt) {
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
