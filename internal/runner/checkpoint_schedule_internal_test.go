package runner

import (
	"fmt"
	"testing"
	"time"
)

// TestCheckpointSchedule pins when a polled tip is pushed: after a quiet
// period, at most once a minute, the retry after a failure at the
// fallback, and a tip that never goes quiet at the fallback.
func TestCheckpointSchedule(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var s checkpointSchedule
	check := func(d time.Duration, tip, pushed string, want bool) {
		t.Helper()
		if got := s.due(t0.Add(d), tip, pushed); got != want {
			t.Fatalf("due(+%v, %s, pushed %s) = %v, want %v", d, tip, pushed, got, want)
		}
	}
	check(0, "a", "a", false)             // unchanged: nothing to push
	check(0, "b", "a", false)             // new: the quiet period starts
	check(3*time.Second, "c", "a", false) // a burst: it starts again
	check(7*time.Second, "c", "a", false)
	check(8*time.Second, "c", "a", true) // still for checkpointQuiet: push the latest
	s.pushed(t0.Add(8 * time.Second))
	check(10*time.Second, "c", "c", false)
	check(20*time.Second, "d", "c", false)
	check(40*time.Second, "d", "c", false) // quiet, but inside the minute
	check(67*time.Second, "d", "c", false)
	check(68*time.Second, "d", "c", true) // the window ended: pushed then
	s.failed(t0.Add(68 * time.Second))
	check(2*time.Minute, "d", "c", false) // a failed push waits for the fallback
	check(68*time.Second+checkpointFallback, "d", "c", true)
	end := 68*time.Second + checkpointFallback
	s.pushed(t0.Add(end))
	// An agent that commits every 4 s never leaves the tip quiet: the tip
	// is pushed anyway once the oldest unpushed one is checkpointFallback old.
	start := end + checkpointMinGap
	for i := 0; ; i++ {
		d := time.Duration(i) * 4 * time.Second
		want := d >= checkpointFallback
		check(start+d, fmt.Sprint("busy", i), "c", want)
		if want {
			break
		}
	}
}
