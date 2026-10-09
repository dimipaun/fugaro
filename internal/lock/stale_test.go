package lock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestStale covers lock.Stale's table: fails closed on anything but a
// positive proof of termination.
func TestStale(t *testing.T) {
	running := &runstore.Record{Status: runstore.StatusRunning}
	unwritten := &runstore.Record{} // no status yet: a record that just has no Status set, not a known terminal one
	done := &runstore.Record{Status: runstore.StatusFailed}
	cases := []struct {
		name         string
		rec          *runstore.Record
		execTerminal bool
		want         bool
	}{
		{"no record, no backend proof: busy", nil, false, false},
		{"running record, no backend proof: busy", running, false, false},
		{"record with no status yet, no backend proof: busy", unwritten, false, false},
		{"terminal record: stale", done, false, true},
		{"no record but backend confirms the execution ended: stale", nil, true, true},
		{"running record but backend confirms the execution ended: stale", running, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Stale(c.rec, c.execTerminal); got != c.want {
				t.Errorf("Stale(%+v, %v) = %v, want %v", c.rec, c.execTerminal, got, c.want)
			}
		})
	}
}

// TestAcquireTakesOverLiveLockWhenStale: a lock that has not expired yet is
// still taken over at once when WithStale's callback says its holder is
// provably over.
func TestAcquireTakesOverLiveLockWhenStale(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/stale")
			old := Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Hour)}
			if _, err := Acquire(ctx, b, key, old, t0); err != nil {
				t.Fatal(err)
			}
			next := Holder{RunID: "20260927-110000-bbbb", Execution: "e2", ExpiresAt: t0.Add(2 * time.Hour)}
			var seen Holder
			stale := func(h Holder) bool { seen = h; return true }
			l, err := Acquire(ctx, b, key, next, t0.Add(time.Minute), WithStale(stale))
			if err != nil {
				t.Fatalf("takeover of a stale live lock = %v", err)
			}
			if seen.RunID != old.RunID {
				t.Fatalf("stale was called with %+v, want the old holder %+v", seen, old)
			}
			if err := l.Release(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestAcquireLeavesLiveLockBusyWhenNotStale: WithStale's callback is
// consulted, but a "no" leaves a live lock busy, as before.
func TestAcquireLeavesLiveLockBusyWhenNotStale(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/notstale")
			old := Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Hour)}
			if _, err := Acquire(ctx, b, key, old, t0); err != nil {
				t.Fatal(err)
			}
			var called bool
			stale := func(Holder) bool { called = true; return false }
			next := Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}
			_, err := Acquire(ctx, b, key, next, t0.Add(time.Minute), WithStale(stale))
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != old.RunID {
				t.Fatalf("Acquire = %v, want BusyError naming %s", err, old.RunID)
			}
			if !called {
				t.Fatal("WithStale's callback was never consulted")
			}
		})
	}
}

// TestAcquireNeverAsksStaleAboutOurOwnLock: the own-lock path (refresh) is
// taken before WithStale's callback ever runs.
func TestAcquireNeverAsksStaleAboutOurOwnLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/mine")
			mine := Holder{RunID: "20260927-100000-aaaa", Execution: "projects/proj-1/locations/r1/jobs/j/executions/e-aaaaa", ExpiresAt: t0.Add(time.Hour)}
			if _, err := Acquire(ctx, b, key, mine, t0); err != nil {
				t.Fatal(err)
			}
			again := mine
			again.ExpiresAt = mine.ExpiresAt.Add(time.Hour)
			stale := func(Holder) bool { t.Fatal("stale asked about our own lock"); return false }
			if _, err := Acquire(ctx, b, key, again, t0.Add(time.Minute), WithStale(stale)); err != nil {
				t.Fatalf("refreshing our own lock = %v", err)
			}
		})
	}
}

// TestAcquireLosesConcurrentStaleTakeover: two runners find the same live
// lock stale at once; the one whose conditional overwrite lands second
// must get a BusyError naming the winner, exactly as for an expired lock
// (TestAcquireLosesConcurrentTakeover).
func TestAcquireLosesConcurrentStaleTakeover(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/race")
			old := Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Hour)}
			if _, err := Acquire(ctx, b, key, old, t0); err != nil {
				t.Fatal(err)
			}
			alwaysStale := func(Holder) bool { return true }
			rival := Holder{RunID: "20260927-110000-cccc", Execution: "e3", ExpiresAt: t0.Add(2 * time.Hour)}
			now := t0.Add(time.Minute)
			setBeforeTakeover(t, func() {
				beforeTakeover = nil // the rival runs straight through
				if _, err := Acquire(ctx, b, key, rival, now, WithStale(alwaysStale)); err != nil {
					t.Errorf("rival takeover = %v", err)
				}
			})
			_, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}, now, WithStale(alwaysStale))
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != rival.RunID || busy.Holder.Execution != "e3" {
				t.Fatalf("losing stale takeover = %v, want BusyError naming %s", err, rival.RunID)
			}
		})
	}
}
