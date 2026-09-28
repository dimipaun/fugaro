package lock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func buckets(t *testing.T) map[string]*blobx.Bucket {
	return map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	}
}

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func TestKey(t *testing.T) {
	k := Key("acme-app", "fugaro/20260927-100000-abcd")
	if len(k) != len("locks/acme-app/")+16 || k != Key("acme-app", "fugaro/20260927-100000-abcd") || k == Key("acme-app", "fugaro/other") {
		t.Fatalf("Key = %q", k)
	}
}

func TestAcquireBusyRelease(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			a := Holder{RunID: "20260927-100000-aaaa", Execution: "e1", ExpiresAt: t0.Add(time.Hour)}
			l, err := Acquire(ctx, b, key, a, t0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Acquire(ctx, b, key, Holder{RunID: "20260927-100000-bbbb", ExpiresAt: t0.Add(time.Hour)}, t0.Add(time.Minute))
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != a.RunID || busy.Holder.Execution != "e1" {
				t.Fatalf("second Acquire = %v", err)
			}
			if err := l.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, _ := b.Exists(ctx, key); ok {
				t.Fatal("lock object survives Release")
			}
			if _, err := Acquire(ctx, b, key, a, t0); err != nil {
				t.Fatalf("Acquire after Release = %v", err)
			}
		})
	}
}

func TestAcquireTakesOverExpiredLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			old, err := Acquire(ctx, b, key, Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Minute)}, t0)
			if err != nil {
				t.Fatal(err)
			}
			next := Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}
			if _, err := Acquire(ctx, b, key, next, t0.Add(2*time.Minute)); err != nil {
				t.Fatalf("takeover = %v", err)
			}
			// The old holder's late Release must not delete the new lock.
			if err := old.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, _ := b.Exists(ctx, key); !ok {
				t.Fatal("a stale Release deleted the new holder's lock")
			}
		})
	}
}

func TestAcquireTakesOverUnreadableLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			if err := b.WriteAll(ctx, key, []byte("garbage"), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(time.Hour)}, t0); err != nil {
				t.Fatalf("Acquire over garbage = %v", err)
			}
		})
	}
}

// TestAcquireLosesConcurrentTakeover: two runners find the same expired lock;
// the one whose conditional overwrite lands second must get a BusyError
// naming the winner, not the expired holder.
func TestAcquireLosesConcurrentTakeover(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			if _, err := Acquire(ctx, b, key, Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Minute)}, t0); err != nil {
				t.Fatal(err)
			}
			rival := Holder{RunID: "20260927-110000-cccc", Execution: "e3", ExpiresAt: t0.Add(2 * time.Hour)}
			now := t0.Add(2 * time.Minute)
			setBeforeTakeover(t, func() {
				beforeTakeover = nil // the rival runs straight through
				if _, err := Acquire(ctx, b, key, rival, now); err != nil {
					t.Errorf("rival takeover = %v", err)
				}
			})
			_, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}, now)
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != rival.RunID || busy.Holder.Execution != "e3" {
				t.Fatalf("losing takeover = %v, want BusyError naming %s", err, rival.RunID)
			}
		})
	}
}

// TestAcquireSurfacesTakeoverReadError: an I/O error during the takeover is
// returned as itself, never as a BusyError with an empty holder.
func TestAcquireSurfacesTakeoverReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	ctx := context.Background()
	dir := t.TempDir()
	fb, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fb.Close() })
	b := blobx.Wrap(fb)
	key := Key("acme-app", "fugaro/x")
	if err := b.WriteAll(ctx, key, []byte("garbage"), nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(key))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	setBeforeTakeover(t, func() {
		if err := os.Chmod(path, 0); err != nil {
			t.Error(err)
		}
	})
	_, err = Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(time.Hour)}, t0)
	var busy *BusyError
	if err == nil || errors.As(err, &busy) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Acquire with an unreadable lock mid-takeover = %v, want the permission error", err)
	}
}

func TestBusyErrorWithoutHolder(t *testing.T) {
	if got := (&BusyError{}).Error(); got != "branch busy: another runner holds its lock" {
		t.Fatalf("Error() = %q", got)
	}
}

func setBeforeTakeover(t *testing.T, f func()) {
	beforeTakeover = f
	t.Cleanup(func() { beforeTakeover = nil })
}

// TestAcquireAdoptsOwnLock covers a create whose first attempt committed
// but whose response was lost: the storage client's retry finds this very
// holder's lock. The same run and execution (however the project is
// spelled) is acquired at the existing generation; the same run from
// another execution, or a run with no execution, is still busy.
func TestAcquireAdoptsOwnLock(t *testing.T) {
	const (
		execByID     = "projects/proj-1/locations/r1/jobs/j/executions/e-aaaaa"
		execByNumber = "projects/123456/locations/r1/jobs/j/executions/e-aaaaa"
		otherExec    = "projects/proj-1/locations/r1/jobs/j/executions/e-bbbbb"
	)
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/own")
			mine := Holder{RunID: "20260927-100000-aaaa", Execution: execByID, ExpiresAt: t0.Add(time.Hour)}
			if _, err := Acquire(ctx, b, key, mine, t0); err != nil {
				t.Fatal(err)
			}
			var busy *BusyError
			for _, h := range []Holder{
				{RunID: mine.RunID, Execution: otherExec, ExpiresAt: mine.ExpiresAt},
				{RunID: mine.RunID, ExpiresAt: mine.ExpiresAt},
			} {
				if _, err := Acquire(ctx, b, key, h, t0); !errors.As(err, &busy) {
					t.Fatalf("Acquire for %+v = %v, want busy", h, err)
				}
			}
			// The identical holder (a retried create) is adopted as is.
			if _, err := Acquire(ctx, b, key, mine, t0); err != nil {
				t.Fatalf("Acquire of our identical lock = %v", err)
			}
			// A restart of the same execution, spelled with the project
			// number, gets the lock with its own later expiry.
			again := mine
			again.Execution = execByNumber
			again.ExpiresAt = mine.ExpiresAt.Add(10 * time.Minute)
			l, err := Acquire(ctx, b, key, again, t0.Add(time.Minute))
			if err != nil {
				t.Fatalf("Acquire of our own lock = %v", err)
			}
			if _, err := Acquire(ctx, b, key, Holder{RunID: "20260927-100000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}, mine.ExpiresAt.Add(time.Minute)); !errors.As(err, &busy) || !busy.Holder.ExpiresAt.Equal(again.ExpiresAt) {
				t.Fatalf("after adoption the lock holds %+v (err %v), want the refreshed expiry %s", busy, err, again.ExpiresAt)
			}
			if err := l.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, _ := b.Exists(ctx, key); ok {
				t.Fatal("the adopted lock survives Release")
			}
		})
	}
}
