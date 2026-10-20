package creds

import (
	"testing"
	"time"
)

// A credential whose exhaustion reported no reset time is held for a wait that
// doubles on each failed probe, to its cap (D153), and the doubling continues
// from the mark rather than restarting: the wait in force is a field, so the
// fifth failed probe is not the first again.
func TestBackoffDoublesToCap(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}

	// The first exhaustion with no reset: the initial wait, doubling from here.
	m := Exhaust(Mark{}, now, time.Time{}, set)
	if m.State != StateBackedOff {
		t.Fatalf("an exhaustion with no reset left %q; want %q", m.State, StateBackedOff)
	}
	if m.Backoff != 15*time.Minute || !m.Until.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("the first wait = %s until %s; want 15m until %s", m.Backoff, m.Until, now.Add(15*time.Minute))
	}
	if !m.Since.Equal(now) {
		t.Fatalf("the exhaustion is dated %s; want %s (it was exhausted now)", m.Since, now)
	}

	// Each failed probe doubles the wait, and the cap is where it stops: the
	// last step would be two hours and is one.
	since := now.Add(-3 * time.Hour)
	m.Since = since
	for _, want := range []time.Duration{30 * time.Minute, time.Hour, time.Hour, time.Hour} {
		m = ProbeFailed(m, now, set)
		if m.Backoff != want {
			t.Fatalf("a failed probe left a wait of %s; want %s", m.Backoff, want)
		}
		if !m.Until.Equal(now.Add(want)) {
			t.Fatalf("the wait of %s ends at %s; want %s", want, m.Until, now.Add(want))
		}
		if !m.Since.Equal(since) {
			t.Fatalf("a failed probe moved the exhaustion date to %s; want it to stay %s: the mark is the same exhaustion still being held", m.Since, since)
		}
	}

	// A wait that is already at the cap and a cap below the initial wait are
	// both bounded, never exceeded.
	if got := ProbeFailed(Mark{Backoff: 4 * time.Hour}, now, set).Backoff; got != time.Hour {
		t.Fatalf("a wait already beyond the cap doubled to %s; want the cap of 1h", got)
	}
	low := Settings{BackoffInitial: time.Hour, BackoffCap: time.Minute}
	if got := Exhaust(Mark{}, now, time.Time{}, low).Backoff; got != time.Hour {
		t.Fatalf("with a cap below the initial wait the first wait = %s; want the initial hour", got)
	}
}

// Waiting out a backed-off credential's wait is not enough: it is probed before
// real work goes to it again (C1.12), and the answer says so, because a caller
// that reads "usable in 0s" as "go" would send work to a token that failed.
func TestBackedOffMarkWaitsForProbe(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	m := Exhaust(Mark{}, now.Add(-16*time.Minute), time.Time{}, set)

	if m.Usable(now, set) {
		t.Fatal("a backed-off credential is usable by having waited: it needs a probe (C1.12)")
	}
	if !m.NeedsProbe(now) {
		t.Fatalf("a backed-off mark whose wait passed at %s does not report needing a probe", m.Until)
	}
	// Before its wait is out it is neither usable nor probed: probing early is
	// how a backoff becomes a retry storm.
	if m.NeedsProbe(m.Until.Add(-time.Minute)) {
		t.Fatal("a backed-off mark whose wait has not passed reports needing a probe")
	}

	// The two other states are never probe targets: an exhausted credential
	// comes back at a time, and a broken one at an operator's hand (C1.21).
	if (Mark{State: StateExhausted, Until: now}).NeedsProbe(now) {
		t.Fatal("an exhausted mark reports needing a probe")
	}
	if Break(now).NeedsProbe(now.Add(24 * time.Hour)) {
		t.Fatal("a broken mark reports needing a probe: a probe would be selecting it again (D139)")
	}

	// A successful probe clears the mark and the wait with it, so the next
	// exhaustion starts at the initial wait rather than the cap.
	c := Cleared()
	if !c.Available() || c.Backoff != 0 || !c.Until.IsZero() {
		t.Fatalf("a cleared mark = %+v; want an available one that remembers nothing", c)
	}
	if got := Exhaust(c, now, time.Time{}, set).Backoff; got != 15*time.Minute {
		t.Fatalf("the wait after a clear = %s; want the initial 15m", got)
	}
}

// A usage limit that reports a reset is waited out until that reset, and the
// reset replaces whatever backoff was in force: the credential said when it
// comes back, and that is better information than the doubling.
func TestResetReplacesTheBackoff(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	backedOff := ProbeFailed(ProbeFailed(Mark{Backoff: 15 * time.Minute}, now, set), now, set)

	reset := now.Add(4 * time.Hour)
	m := Exhaust(backedOff, now, reset, set)
	if m.State != StateExhausted || !m.Until.Equal(reset) || !m.Since.Equal(now) {
		t.Fatalf("an exhaustion reporting a reset left %+v; want exhausted until %s from %s", m, reset, now)
	}
	if m.Backoff != 0 {
		t.Fatalf("the mark carries a backoff of %s beside a reported reset: the doubling is over", m.Backoff)
	}

	// A reset that is not after now is not a reset: a response whose header is
	// in the past, or is the zero time, says nothing, and the credential backs
	// off instead of being read as available.
	for _, bad := range []time.Time{time.Time{}, now, now.Add(-time.Hour)} {
		if got := Exhaust(Mark{}, now, bad, set); got.State != StateBackedOff {
			t.Fatalf("an exhaustion reporting %s left %q; want a backoff", bad, got.State)
		}
	}
}

// A state this release does not know is not usable and never becomes so by
// waiting: a node written by a newer release, or by hand, must fail closed
// rather than be read as "no hold" (the one rule Usable has beyond the four
// states).
func TestUnknownStateFailsClosed(t *testing.T) {
	m := Mark{State: State("draining"), Until: now.Add(-time.Hour)}
	if m.Usable(now, Settings{}) {
		t.Fatal("a mark in an unknown state is usable")
	}
	if m.NeedsProbe(now) {
		t.Fatal("a mark in an unknown state is a probe target")
	}
	if _, ok := m.Ready(now, Settings{}); ok {
		t.Fatal("a mark in an unknown state has a time at which it becomes usable")
	}
}

// The zero mark and an explicitly available one are the same credential: no
// reader has to tell a node that was never written from one that says
// available, and both are ready now.
func TestZeroMarkIsAvailable(t *testing.T) {
	for _, m := range []Mark{{}, {State: StateAvailable}, Cleared()} {
		if !m.Usable(now, Settings{}) || !m.Available() {
			t.Fatalf("a mark of %+v is not available", m)
		}
		if at, ok := m.Ready(now, Settings{}); !ok || !at.Equal(now) {
			t.Fatalf("a mark of %+v is ready at %s, %v; want now", m, at, ok)
		}
	}
}
