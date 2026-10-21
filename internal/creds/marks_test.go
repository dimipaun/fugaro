package creds

import (
	"errors"
	"math"
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
	// exhaustion starts at the initial wait rather than the cap. (The control is
	// off here — noWindow — and ProbeSucceeded says below what it does when it
	// is on.)
	c := ProbeSucceeded(m, m.Until, noWindow())
	if !c.Available() || c.Backoff != 0 || !c.Until.IsZero() {
		t.Fatalf("a cleared mark = %+v; want an available one that remembers nothing", c)
	}
	if got := Exhaust(c, now, time.Time{}, set).Backoff; got != 15*time.Minute {
		t.Fatalf("the wait after a clear = %s; want the initial 15m", got)
	}
}

// A successful probe of a mark that is not backed off is a no-op, and that is
// the whole of review finding M4: a probe asks whether the wait is over for a
// credential that reported no reset, so its "yes" is no answer at all for a
// broken credential (out until the operator clears it, C1.21) or for an
// exhausted one (out until the reset it reported). A probe launched just before
// a 401 landed, or two instances answering out of order, would otherwise be the
// write that puts a refused token back in rotation.
func TestAProbeClearsOnlyABackedOffMark(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	broken := Break(now.Add(-time.Hour))
	exhausted := Mark{State: StateExhausted, Until: now.Add(time.Hour), Since: now}

	for _, prev := range []Mark{broken, exhausted, {}, {State: State("draining")}} {
		if got := ProbeSucceeded(prev, now, set); got != prev {
			t.Fatalf("a successful probe of %+v left %+v; want the mark that was there", prev, got)
		}
	}
	// The broken case once more, through the service's own write: Clear is the
	// operator's, and Probe is not it.
	st := newFakeStore()
	st.setMark(a1.Name, broken)
	s := selector(st, set)
	if _, err := s.Probed(ctx, a1.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Broken() || !m.Since.Equal(broken.Since) {
		t.Fatalf("a successful probe of a broken credential left %+v; want the 401 to stand (C1.21)", m)
	}
	// And the backed-off case is the one it does write, which is what keeps this
	// test from passing on a ProbeSucceeded that does nothing at all.
	backedOff := Exhaust(Mark{}, now.Add(-16*time.Minute), time.Time{}, noWindow())
	st.setMark(a2.Name, backedOff)
	if _, err := selector(st, noWindow()).Probed(ctx, a2.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a2.Name); !m.Available() {
		t.Fatalf("a successful probe of a backed-off credential left %+v; want it cleared", m)
	}
}

// With D142's first control on, a successful probe does not put a shared token
// back in rotation inside the window its exhaustion opened: the probe answers
// whether the credential works, the control answers whether the owner's donated
// quota may be spent on it again yet. Review finding M3 — without this, a probe
// fifteen minutes after an exhaustion was the way around the control that is on
// by default.
func TestAProbeInsideTheWindowLeavesTheTokenHeld(t *testing.T) {
	const window = 5 * time.Hour
	set := Settings{Window: window, BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	since := now.Add(-16 * time.Minute) // the exhaustion, one wait ago
	m := Exhaust(Mark{}, since, time.Time{}, set)

	// The wait has passed, so the probe is due; the window has not.
	if !m.NeedsProbe(now) {
		t.Fatal("a backed-off mark whose wait passed is not a probe target")
	}
	got := ProbeSucceeded(m, now, set)
	if got.State != StateExhausted || !got.Until.Equal(since.Add(window)) || !got.Since.Equal(since) {
		t.Fatalf("the probe's success left %+v; want the token held until %s, dated from the exhaustion", got, since.Add(window))
	}
	if got.Usable(now, set) {
		t.Fatal("the token is usable inside the window its exhaustion opened, with the control on")
	}
	// It serves again exactly when the window ends, and no probe is owed then:
	// the probe's "yes" is not dated, and the mark's answer is.
	end := since.Add(window)
	if ready, ok := got.Ready(end, set); !ok || !ready.Equal(end) || got.Usable(end, set) == false {
		t.Fatalf("at the window's end the mark = %s, %v, usable=%v; want it ready at %s", ready, ok, got.Usable(end, set), end)
	}
	if got.NeedsProbe(end) {
		t.Fatal("an exhausted mark asks for a probe: its reset is a time, not a question")
	}
	// A probe that answers *after* the window has ended does clear it, and one
	// with the control off clears it immediately: the held case above is the
	// control, not the probe's own state.
	if after := ProbeSucceeded(m, end.Add(time.Second), set); !after.Available() {
		t.Fatalf("a probe after the window left %+v; want it cleared", after)
	}
	if off := ProbeSucceeded(m, now, noWindow()); !off.Available() {
		t.Fatalf("a probe with the control off left %+v; want it cleared", off)
	}
}

// A mark that says a credential is held and carries no time at all is a node
// this release cannot read, and it must fail closed: the previous shape answered
// the zero time, which is before now, so {"state":"exhausted"} with no `until`
// read as an available credential. Review finding m1.
func TestAMarkHeldWithNoTimeFailsClosed(t *testing.T) {
	for _, m := range []Mark{
		{State: StateExhausted},
		{State: StateBackedOff},
		{State: StateExhausted, Backoff: time.Minute},
	} {
		if m.Usable(now, Settings{}) {
			t.Fatalf("a mark of %+v is usable: a hold with no time to wait for must not read as no hold", m)
		}
		if _, ok := m.Ready(now, Settings{}); ok {
			t.Fatalf("a mark of %+v is ready at some time: there is no time in it to be ready at", m)
		}
	}

	// Through a selection: the credential is not used, and the run is not told
	// to sleep either — nothing there becomes usable, so the refusal is the one
	// that needs a person.
	st := newFakeStore()
	st.setMark(a1.Name, Mark{State: StateExhausted})
	if _, _, err := selector(st, Settings{}).Select(ctx, run, []Credential{a1}); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("the selection of a mark with no time in it = %v; want the refusal that says nothing can serve", err)
	}
}

// A usage limit with no reset time that arrives while a hold is already in force
// is the same exhaustion seen again: the credential is not selected while it is
// backed off, so the only calls that can answer 429 are the ones that were in
// flight when the first one landed. N of them must leave the state one of them
// left — otherwise a burst doubles the wait once per call and walks the
// credential to its five-hour cap in a second. Review finding m2.
func TestConcurrentUsageLimitsDoNotDoubleTheWait(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: 5 * time.Hour}
	first := Exhaust(Mark{}, now, time.Time{}, set)

	// Ten copies of the same exhaustion, an instant apart, all inside the hold
	// the first one wrote.
	m := first
	for i := 0; i < 10; i++ {
		m = Exhaust(m, now.Add(time.Duration(i)*time.Second), time.Time{}, set)
		if m != first {
			t.Fatalf("a usage limit inside the hold left %+v; want %+v: the hold in force is the first one's", m, first)
		}
	}

	// Once the hold has passed, the next one really is a fresh exhaustion: the
	// guard is the window, not an exemption from doubling.
	after := Exhaust(first, first.Until.Add(time.Second), time.Time{}, set)
	if after.Backoff != 30*time.Minute {
		t.Fatalf("a usage limit after the hold doubled to %s; want 30m", after.Backoff)
	}
	// And a reset that is reported is a reset: a credential that says when it
	// comes back is not backed off at all, whatever it was in before.
	withReset := Exhaust(first, now.Add(time.Second), now.Add(time.Hour), set)
	if withReset.State != StateExhausted || !withReset.Until.Equal(now.Add(time.Hour)) {
		t.Fatalf("a usage limit reporting a reset while backed off left %+v; want the reset to replace the wait", withReset)
	}
}

// A usage limit with no reset header arriving while an EXHAUSTED hold is in
// force leaves the hold exactly as it is — the exhausted half of the in-flight
// race above, and the half that was missing.
//
// The guard that fixed the backed-off half covered only backed_off, so this case
// fell through to ProbeFailed: a second in-flight 429 replaced the reset the
// upstream had just reported with a fifteen-minute backoff, Ready answered
// fifteen minutes for a credential four hours from coming back, and — on the
// tier that spends money, after one successful probe — the credential was handed
// back about three hours and three quarters early.
func TestAUsageLimitDoesNotDiscardAnExhaustedHold(t *testing.T) {
	set := noWindow()
	reset := now.Add(4 * time.Hour)
	held := Exhaust(Mark{}, now, reset, set)
	if held.State != StateExhausted || !held.Until.Equal(reset) {
		t.Fatalf("a 429 reporting a four-hour reset left %+v; want exhausted until %s", held, reset)
	}

	// The same limit, reported again by calls that were in flight when the reset
	// landed: a minute later, and three hours in.
	for _, at := range []time.Time{now.Add(time.Minute), now.Add(3 * time.Hour)} {
		m := Exhaust(held, at, time.Time{}, set)
		if m != held {
			t.Fatalf("a no-reset 429 at +%s left %+v; want the hold the reset wrote, %+v", at.Sub(now), m, held)
		}
	}

	// And the time a selection answers with is the reset's.
	if at, ok := held.Ready(now, set); !ok || !at.Equal(reset) {
		t.Fatalf("the held mark's wait = %s, %v; want the reset %s", at, ok, reset)
	}

	// A reset that is reported WHILE THE HOLD STANDS does not shorten it: the
	// call that reported it was in flight when the four-hour reset landed, and
	// its one-minute answer is stale (review finding N5). On this tier the
	// window is off, so Until is the whole hold.
	if m := Exhaust(held, now.Add(time.Minute), now.Add(time.Hour), set); m != held {
		t.Fatalf("a 429 reporting a shorter reset left %+v; want the four-hour hold, %+v", m, held)
	}
	// A reset that lands *after* the hold is a different matter: it is the later
	// of the two answers, and the upstream's newer word replaces the older.
	if m := Exhaust(held, now.Add(time.Minute), now.Add(6*time.Hour), set); !m.Until.Equal(now.Add(6 * time.Hour)) {
		t.Fatalf("a 429 reporting a reset beyond the hold left %+v; want the later reset", m)
	}

	// Once the reset has passed the credential is in rotation again, and the
	// next no-reset 429 is a fresh exhaustion rather than a racing one.
	if m := Exhaust(held, reset.Add(time.Minute), time.Time{}, set); m.State != StateBackedOff {
		t.Fatalf("a no-reset 429 after the reset left %+v; want the first backoff", m)
	}
}

// A usage limit with no reset time reported after the hold has LAPSED is a fresh
// exhaustion — the credential is in rotation again, so the 429 is this call's own
// and not one from before the hold — and a fresh exhaustion is dated now. The
// date is what "least recently exhausted" orders by (D154) and what D142's
// window is measured from, so a fresh exhaustion that kept the previous
// exhaustion's date would put the credential at the front of the queue for the
// window it had already served. Review finding N1: this is the one line of
// Exhaust's no-reset path a mutation could delete with every other test green.
func TestAFreshUsageLimitDatesTheExhaustionNow(t *testing.T) {
	set := noWindow()
	old := now.Add(-6 * time.Hour)
	lapsed := Mark{State: StateExhausted, Until: now.Add(-time.Hour), Since: old}

	m := Exhaust(lapsed, now, time.Time{}, set)
	if m.State != StateBackedOff || m.Backoff != DefaultBackoffInitial {
		t.Fatalf("a no-reset 429 after the hold left %+v; want the first backoff of %s", m, DefaultBackoffInitial)
	}
	if !m.Since.Equal(now) {
		t.Fatalf("the fresh exhaustion is dated %s; want %s — the credential was exhausted again, it is not the old exhaustion still being held", m.Since, now)
	}

	// The held case, for contrast: a no-reset 429 inside a standing hold leaves
	// the date alone, because it is the same exhaustion seen again.
	held := Mark{State: StateExhausted, Until: now.Add(4 * time.Hour), Since: old}
	if got := Exhaust(held, now.Add(time.Minute), time.Time{}, set); !got.Since.Equal(old) {
		t.Fatalf("a no-reset 429 inside the hold dated the exhaustion %s; want it to stay %s", got.Since, old)
	}
}

// The mirror of that guard, on the probe's side of the same race: a probe
// launched while the credential was backed off can land after the credential
// answered 429 with a reset, and its failure must not shorten the hold the
// upstream just named — a four-hour reset must not become fifteen minutes.
func TestAFailedProbeDoesNotShortenAnExhaustedHold(t *testing.T) {
	set := noWindow()
	backed := Mark{State: StateBackedOff, Until: now.Add(15 * time.Minute), Backoff: 15 * time.Minute, Since: now.Add(-time.Hour)}
	held := Exhaust(backed, now, now.Add(4*time.Hour), set)

	if m := ProbeFailed(held, now.Add(time.Minute), set); m != held {
		t.Fatalf("a failed probe a minute into a four-hour reset left %+v; want the reset, %+v", m, held)
	}

	// After the reset the credential is in rotation, and a failed probe there is
	// a fresh observation: the first wait is the right answer.
	lapsed := ProbeFailed(held, held.Until.Add(time.Minute), set)
	if lapsed.State != StateBackedOff || lapsed.Backoff != DefaultBackoffInitial {
		t.Fatalf("a failed probe after the reset left %+v; want a backoff of %s", lapsed, DefaultBackoffInitial)
	}
}

// The wait doubles to its cap, and the doubling is decided before the multiply
// that could pass it: a stored backoff at the value a wrapping multiply would
// turn negative comes out at the cap, and a negative or absurd one is treated as
// the first wait rather than doubled into a longer hold. A negative wait is a
// hold that is already over, which is a credential re-probed for ever. Review
// finding m3.
func TestADoublingNeverOverflows(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}

	for _, stored := range []time.Duration{
		time.Duration(math.MaxInt64),       // the largest a value can be
		time.Duration(math.MaxInt64/2 + 1), // the smallest one whose double wraps
		set.BackoffCap,                     // exactly at the cap
		set.BackoffCap / 2,                 // and exactly at the half the check tests
	} {
		m := ProbeFailed(Mark{State: StateBackedOff, Backoff: stored, Since: now}, now, set)
		if m.Backoff != set.BackoffCap {
			t.Fatalf("a stored wait of %s doubled to %s; want the cap of %s", stored, m.Backoff, set.BackoffCap)
		}
		if !m.Until.Equal(now.Add(set.BackoffCap)) {
			t.Fatalf("a stored wait of %s left a hold ending at %s; want %s", stored, m.Until, now.Add(set.BackoffCap))
		}
	}
	// One nanosecond below the half still doubles, so the check is the half and
	// not "cap the doubling always".
	if got := ProbeFailed(Mark{State: StateBackedOff, Backoff: set.BackoffCap/2 - 1, Since: now}, now, set).Backoff; got != set.BackoffCap-2 {
		t.Fatalf("a stored wait just below the half doubled to %s; want %s", got, set.BackoffCap-2)
	}
	// A negative stored wait is not doubled into a longer hold, and not kept
	// either: it is a node this release never writes, and the first wait is the
	// protective answer.
	if got := ProbeFailed(Mark{State: StateBackedOff, Backoff: -time.Hour, Since: now}, now, set).Backoff; got != set.BackoffInitial {
		t.Fatalf("a negative stored wait doubled to %s; want the initial %s", got, set.BackoffInitial)
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

// A 401 puts a credential out until the operator clears it (D139, C1.21), and
// nothing a busy token reports afterwards takes it off: two calls on one shared
// token can answer 401 and 429 in either order, and the 429 arriving second must
// not put a refused token back in rotation after its reset.
func TestBrokenSurvivesAUsageLimitAndAFailedProbe(t *testing.T) {
	set := Settings{BackoffInitial: 15 * time.Minute, BackoffCap: time.Hour}
	broken := Break(now.Add(-time.Hour))

	for _, m := range []Mark{
		Exhaust(broken, now, now.Add(time.Minute), set), // a reset was reported
		Exhaust(broken, now, time.Time{}, set),          // no reset: a backoff
		ProbeFailed(broken, now, set),
	} {
		if !m.Broken() || !m.Since.Equal(broken.Since) {
			t.Fatalf("a transition on a broken mark left %+v; want the mark that was there", m)
		}
	}

	// The same two transitions through the Selector, which is how C1.11 and
	// C1.12 record them.
	st := newFakeStore()
	st.setMark(a1.Name, broken)
	s := selector(st, set)
	if _, err := s.Exhausted(ctx, a1.Name, TierA, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Broken() {
		t.Fatalf("a usage limit recorded through the service's write left %+v; want the 401 to stand", m)
	}
	if _, err := s.ProbeFailed(ctx, a1.Name, TierA); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Broken() {
		t.Fatalf("a failed probe recorded through the service's write left %+v; want the 401 to stand", m)
	}
	if _, _, err := s.Select(ctx, run, []Credential{a1}); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("the selection of a broken credential = %v; want the refusal that takes an operator", err)
	}

	// Cleared is the one transition that leaves it, and it is the operator's
	// (C1.21) or a successful probe's (C1.12).
	if _, err := s.Cleared(ctx, a1.Name); err != nil {
		t.Fatal(err)
	}
	if m := st.mark(a1.Name); !m.Available() {
		t.Fatalf("the clear left %+v; want available", m)
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

// A reset the upstream reported is a fact about the token, and D142's window is
// a floor under the hold rather than a ceiling over it: an exhaustion whose
// reset is ten hours out is held for ten hours, and the control's window does
// not shorten that to its own end. This is the direction no other test in the
// package exercises -- they use noWindow(), or an Until equal to Since+Window --
// and it is what Ready's `end.After(at)` guard exists for: with the comparison
// read the other way (or dropped for the window), the mark would read usable
// hours before the upstream said it was, which is the M3 fail-open this file's
// comment forbids, with the reported reset as its subject.
//
// Mutation (replace the guard's condition with `true`, so the window always
// wins): this fails, and every other test in the package still passes.
func TestAWindowNeverShortensAReportedReset(t *testing.T) {
	const window = time.Hour
	set := Settings{Window: window, BackoffCap: window}
	since := now.Add(-time.Minute)
	reset := now.Add(10 * time.Hour)
	m := Exhaust(Mark{}, since, reset, set)
	if m.State != StateExhausted || !m.Until.Equal(reset) {
		t.Fatalf("the exhaustion left %+v; want it held until the reset it reported, %s", m, reset)
	}

	at, ok := m.Ready(now, set)
	if !ok || !at.Equal(reset) {
		t.Fatalf("Ready = %s, %v; want the reported reset %s: the window (%s from %s) is a floor under the hold, not a ceiling over it", at, ok, reset, window, since)
	}
	if m.Usable(now, set) || m.Usable(reset.Add(-time.Second), set) {
		t.Fatal("a token whose upstream said it was out for ten hours is usable before then")
	}
	if !m.Usable(reset, set) {
		t.Fatal("the token is not usable at the reset its upstream reported")
	}
	// And a hold the window does reach is still the window's: the guard's two
	// sides are both live, so this is not a test that passes because the window
	// is ignored.
	inside := Exhaust(Mark{}, since, now.Add(time.Minute), set)
	if at, _ := inside.Ready(now, set); !at.Equal(since.Add(window)) {
		t.Fatalf("a reset inside the window = %s; want the window's end %s", at, since.Add(window))
	}
}
