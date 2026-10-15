package backend

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// fixed is a time in the past, so that Follow's "never read from the future"
// clamp (minTime(newest, time.Now())) is the newest entry and not the clock,
// and the window arithmetic below is exact.
var fixed = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

// The lookback window is a contract with Cloud Logging, not a tuning knob:
// the API can ingest an entry after a newer one, and Follow only sees that
// entry because each poll re-reads that far back. The window is written out
// here rather than derived from the constant, so that changing it fails this
// test instead of quietly narrowing what `fugaro logs` can see -- the same
// reason the Cloud Run filter is pinned as a string (gcp's
// TestCloudRunLogFilterIsUnchanged).
func TestFollowRereadsTheLookbackWindow(t *testing.T) {
	newest := fixed.Add(time.Minute)
	var froms []time.Time
	err := Follow(context.Background(), FollowOptions{
		Since:  fixed,
		Poll:   time.Millisecond,
		Settle: time.Millisecond,
		Read: func(_ context.Context, from time.Time, fn func(LogEntry) error) error {
			froms = append(froms, from)
			if len(froms) == 1 {
				return fn(LogEntry{InsertID: "1", Time: newest, Message: "newest"})
			}
			return nil
		},
		State: func(context.Context) (bool, bool, error) { return true, false, nil },
	}, func(LogEntry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(froms) < 2 {
		t.Fatalf("reads = %d, want a second read after the first poll", len(froms))
	}
	if !froms[0].Equal(fixed) {
		t.Errorf("first read from %s, want Since (%s)", froms[0], fixed)
	}
	if want := newest.Add(-10 * time.Second); !froms[1].Equal(want) {
		t.Errorf("second read from %s, want the newest entry less the 10s lookback (%s)", froms[1], want)
	}
}

// The window is what makes a late entry visible, and its far edge is what
// keeps the re-read finite: an entry ingested behind a newer one is emitted,
// and one older than the window is not read again. The read filters by `from`
// exactly as the API does, so a window of zero loses the late entry and a
// window wider than a minute re-emits the stale one.
func TestFollowEmitsALateEntryButNotOneOlderThanTheWindow(t *testing.T) {
	newest := fixed.Add(time.Minute)
	entries := []LogEntry{
		{InsertID: "newest", Time: newest, Message: "newest"},
		{InsertID: "late", Time: newest.Add(-9 * time.Second), Message: "late"},    // inside the 10s window
		{InsertID: "stale", Time: newest.Add(-11 * time.Second), Message: "stale"}, // outside it
	}
	reads := 0
	var got []string
	err := Follow(context.Background(), FollowOptions{
		Since:  fixed,
		Poll:   time.Millisecond,
		Settle: time.Millisecond,
		Read: func(_ context.Context, from time.Time, fn func(LogEntry) error) error {
			reads++
			for _, e := range entries {
				// The late pair is ingested after the first read: only the
				// newest entry is there when it runs.
				if reads == 1 && e.InsertID != "newest" {
					continue
				}
				if e.Time.Before(from) {
					continue
				}
				if err := fn(e); err != nil {
					return err
				}
			}
			return nil
		},
		State: func(context.Context) (bool, bool, error) { return true, false, nil },
	}, func(e LogEntry) error { got = append(got, e.Message); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "newest" || got[1] != "late" {
		t.Fatalf("emitted %q, want [newest late]: the lookback window is not what it was", got)
	}
}

// The settle window is how long Follow keeps reading after the execution has
// ended. The execution's clock and Cloud Logging's ingestion are not the same
// clock, so a window that closes too early drops the last lines. Asserted as
// an elapsed-time floor, so a rule that settles on the first poll fails here.
func TestFollowWaitsOutTheSettleWindow(t *testing.T) {
	const settle = 120 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := Follow(ctx, FollowOptions{
		Poll:   5 * time.Millisecond,
		Settle: settle,
		Read:   func(context.Context, time.Time, func(LogEntry) error) error { return nil },
		State:  func(context.Context) (bool, bool, error) { return true, false, nil },
	}, func(LogEntry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < settle {
		t.Errorf("follow returned %s after the execution ended with nothing arriving, want at least the settle window (%s)", elapsed, settle)
	}
}

// A fresh entry restarts the settle window: an ended execution can still be
// having its last lines ingested, and a window that ran from the end alone
// would stop in the middle of them. The arrivals outlast the window, so a
// loop that does not restart it stops early and emits only some of them.
func TestFollowSettleWindowRestartsOnAFreshEntry(t *testing.T) {
	const (
		settle   = 100 * time.Millisecond
		arrivals = 15 // one per 10ms poll: 150ms of arrivals, longer than settle
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reads := 0
	var got []string
	err := Follow(ctx, FollowOptions{
		Poll:   10 * time.Millisecond,
		Settle: settle,
		Read: func(_ context.Context, _ time.Time, fn func(LogEntry) error) error {
			reads++
			if reads > arrivals {
				return nil
			}
			return fn(LogEntry{InsertID: strconv.Itoa(reads), Time: time.Now(), Message: "line"})
		},
		State: func(context.Context) (bool, bool, error) { return true, false, nil },
	}, func(LogEntry) error { got = append(got, "line"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != arrivals {
		t.Errorf("emitted %d entries, want all %d: the loop settled while entries were still arriving", len(got), arrivals)
	}
}
