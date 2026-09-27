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
