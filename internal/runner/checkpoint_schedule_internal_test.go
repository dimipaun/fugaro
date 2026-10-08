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
	s := newCheckpointSchedule("base")
	check := func(d time.Duration, tip, pushed string, want bool) {
		t.Helper()
		if got := s.due(t0.Add(d), tip, pushed); got != want {
			t.Fatalf("due(+%v, %s, pushed %s) = %v, want %v", d, tip, pushed, got, want)
		}
	}
	check(0, "base", "", false) // the base is never pushed (no push yet)
	check(20*time.Second, "base", "", false)
	check(0, "a", "a", false)             // unchanged: nothing to push
	check(0, "b", "a", false)             // new: the quiet period starts
	check(3*time.Second, "c", "a", false) // a burst: it starts again
	check(7*time.Second, "c", "a", false)
	check(8*time.Second, "c", "a", true) // still for checkpointQuiet: push the latest
	s.pushed(t0.Add(8 * time.Second))
	check(10*time.Second, "c", "c", false)
	check(5*time.Minute, "c", "c", false) // unchanged after a push: never due again
	check(5*time.Minute, "base", "c", false)
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
	for i := 0; i < 1000; i++ { // bounded: a broken schedule fails, never hangs
		d := time.Duration(i) * 4 * time.Second
		want := d >= checkpointFallback
		check(start+d, fmt.Sprint("busy", i), "c", want)
		if want {
			break
		}
	}
}

// boundedDue finds the first offset (stepping 1 s from 0) at which tip is due,
// failing instead of hanging when it never is.
func boundedDue(t *testing.T, s *checkpointSchedule, from time.Time, tip, pushed string) time.Duration {
	t.Helper()
	for d := time.Duration(0); d <= 10*time.Minute; d += time.Second {
		if s.due(from.Add(d), tip, pushed) {
			return d
		}
	}
	t.Fatalf("tip %s never became due within 10m", tip)
	return 0
}

func TestCheckpointScheduleFirstPoll(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := newCheckpointSchedule("base")
	for d := time.Duration(0); d <= 5*time.Minute; d += 10 * time.Second {
		if s.due(t0.Add(d), "base", "") {
			t.Fatalf("the base was due at +%v before any push", d)
		}
	}
	if got := boundedDue(t, s, t0.Add(6*time.Minute), "x", ""); got != checkpointQuiet {
		t.Fatalf("a real tip was due after %v, want %v", got, checkpointQuiet)
	}
	s.pushed(t0.Add(7 * time.Minute))
	for d := time.Duration(0); d <= 5*time.Minute; d += 10 * time.Second {
		if s.due(t0.Add(8*time.Minute+d), "base", "x") {
			t.Fatalf("the base was due at +%v after a push", d)
		}
	}
}

func TestCheckpointScheduleFailureThenNewTip(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := newCheckpointSchedule("base")
	boundedDue(t, s, t0, "a", "")
	s.failed(t0.Add(10 * time.Second))
	if s.due(t0.Add(time.Minute), "a", "") {
		t.Fatal("a failed push was retried before the fallback")
	}
	if s.due(t0.Add(2*time.Minute), "a", "a") {
		t.Fatal("the pushed tip was due")
	}
	// A new tip long after the failure waits for its own quiet period.
	later := t0.Add(5 * time.Minute)
	if s.due(later, "b", "a") {
		t.Fatal("a new tip was due on first sight after a stale failure")
	}
	if got := boundedDue(t, s, later, "b", "a"); got != checkpointQuiet {
		t.Fatalf("new tip due after %v, want %v", got, checkpointQuiet)
	}
}

func TestCheckpointScheduleClockBackwards(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := newCheckpointSchedule("base")
	s.pushed(t0.Add(5 * time.Minute)) // a push, then the clock steps back 60 s
	back := t0.Add(4 * time.Minute)
	if s.due(back, "a", "") {
		t.Fatal("due on first sight")
	}
	if got := boundedDue(t, s, back, "a", ""); got > checkpointQuiet+time.Second {
		t.Fatalf("a push after a backwards step waited %v", got)
	}
	// A failure stamped in the future retries rather than waiting it out.
	s = newCheckpointSchedule("base")
	s.due(t0.Add(10*time.Minute), "a", "")
	s.failed(t0.Add(11 * time.Minute))
	if got := boundedDue(t, s, t0.Add(10*time.Minute), "a", ""); got > checkpointQuiet+time.Second {
		t.Fatalf("a failure in the future delayed the retry by %v", got)
	}
}

func TestCheckpointScheduleClockStepsBackWhileWaiting(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	s := newCheckpointSchedule("base")
	if s.due(t0.Add(10*time.Minute), "a", "") {
		t.Fatal("due on first sight")
	}
	// The wall clock steps back 60 s while the tip waits: the quiet period
	// restarts from the new time instead of waiting out the old one.
	back := t0.Add(9 * time.Minute)
	if s.due(back, "a", "") {
		t.Fatal("due right after the step back")
	}
	if got := boundedDue(t, s, back, "a", ""); got != checkpointQuiet {
		t.Fatalf("due %v after a backwards step, want %v", got, checkpointQuiet)
	}
}
