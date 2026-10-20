package creds

import "time"

// State is one credential's shared state, as the service stores it under
// creds/marks/<name>.
//
// Four states, and each is a different answer to "may a call go here now":
//
//   - available — yes;
//   - exhausted — not until Until (the reset the upstream reported), and, with
//     D142's first control on, not until the subscription window opened by the
//     exhaustion has ended either (see Ready);
//   - backed_off — no reset time was reported, so the credential is held for a
//     wait that doubles on each failed probe to its cap, and it is *probed*
//     before real work goes to it again (C1.12): waiting out the wait is not
//     enough;
//   - broken — a 401 (D139): the token is refused, and only the operator clears
//     it (C1.21). Nothing here ever selects it again on its own.
type State string

const (
	StateAvailable State = "available"
	StateExhausted State = "exhausted"
	StateBackedOff State = "backed_off"
	StateBroken    State = "broken"
)

// Mark is one credential's shared state (D136): when it was last taken out of
// service, until when, and the wait in force for a backed-off one.
//
// The zero Mark is an available credential: a credential with no node, one with
// a node that says available, and one whose mark was cleared are the same
// credential, and no reader has to tell them apart.
//
// The four fields are the whole history a selection needs, and keeping it to
// four is deliberate. Since is when the credential last entered the state it is
// in — for an exhaustion, when the exhaustion happened — and it is what
// "least recently exhausted" orders by (D154) and what D142's window is
// measured from; it does not move when a probe fails (the credential was not
// exhausted again, it is the same exhaustion still being held) and it does move
// on a fresh exhaustion. Until is when the hold lapses. Backoff is the wait in
// force for a backed-off mark, so a failed probe doubles *it* rather than
// starting over.
type Mark struct {
	State   State         `json:"state,omitempty"`
	Until   time.Time     `json:"until,omitempty"`
	Since   time.Time     `json:"since,omitempty"`
	Backoff time.Duration `json:"backoff,omitempty"`
}

// Available reports a mark that holds nothing.
func (m Mark) Available() bool { return m.State == "" || m.State == StateAvailable }

// Broken reports a credential the operator must clear (C1.21).
func (m Mark) Broken() bool { return m.State == StateBroken }

// Usable reports whether real work may be routed to the credential at now.
//
// A state this Fugaro does not know is *not* usable: a node written by a newer
// release, or by hand, must fail closed rather than be read as "no hold". That
// is the one rule this function has that the four states do not spell out.
func (m Mark) Usable(now time.Time, set Settings) bool {
	switch m.State {
	case "", StateAvailable:
		return true
	case StateExhausted:
		at, ok := m.Ready(now, set)
		return ok && !now.Before(at)
	case StateBackedOff, StateBroken:
		// A backed-off credential waits for a probe (C1.12), not for its clock;
		// a broken one waits for the operator (C1.21).
		return false
	}
	return false
}

// NeedsProbe reports whether the credential must be probed before real work is
// routed to it again: a backed-off mark whose wait has passed (C1.12). It is
// never true for a broken credential — a probe is not what clears one — and
// never true for an exhausted one, whose reset is a time, not a question.
//
// "Passed" includes the instant the wait ends: a mark whose Until is exactly now
// holds no longer (Ready says the same — it answers a time that is not after
// now), so the probe is owed at that instant rather than one tick later.
// TestAProbeIsDueExactlyAtTheMarksWait pins it, because flipping the comparison
// to a strict one would delay every probe by an instant no other test would
// notice.
func (m Mark) NeedsProbe(now time.Time) bool {
	return m.State == StateBackedOff && !now.Before(m.Until)
}

// Ready is the time the credential becomes usable again by waiting, and whether
// it ever does. False is "no amount of waiting clears this": a broken credential
// (the operator's, C1.21) and a hold that carries no time to wait for at all.
//
// The window it measures from is in the set the caller passes, and the caller
// has applied Settings.forTier to it: the window is a shared tier's, so a tier B
// credential's answer is its reset and nothing else. Nothing here needs to know
// a tier — one settings value per credential is what forTier is for.
//
// An exhausted mark's answer is max(Until, window end): the reset the upstream
// reported, and — with D142's first control on — the end of the subscription
// window the exhaustion opened at Since. The second is what makes the control
// mean anything. The spec's own headroom rule is "stop after hitting the limit
// once, so contributors keep quota for their own work": without a window, a
// token that reports a one-minute reset is back in rotation a minute later and
// the donor's quota is spent again; with it, one exhaustion costs the token the
// rest of its window whatever reset it reported. The window is Settings.Window,
// whose default and whose relationship to the policy's backoff cap are stated
// there.
func (m Mark) Ready(now time.Time, set Settings) (time.Time, bool) {
	set = set.withDefaults()
	switch m.State {
	case "", StateAvailable:
		// Usable now. The only thing that can hold it is a lease, which the
		// caller adds (see earliest).
		return now, true
	case StateExhausted, StateBackedOff:
		// Both states are an exhaustion, and both answer with the later of the
		// two times in play: the wait (the reset the upstream reported, or the
		// doubling for a credential that reported none) and, with D142's first
		// control on, the end of the subscription window the exhaustion opened at
		// Since.
		//
		// A backed_off mark is in this branch on purpose, and it is the review's
		// finding M3. Its `Until` is the wait — fifteen minutes, then thirty —
		// and a selection that answered with the wait alone would be saying "this
		// can serve work again in fifteen minutes" about a token the control
		// holds for the rest of its window: the caller would probe it, the probe
		// would succeed, and the token would be back in rotation inside the
		// window, which is the control's whole point reversed. With the window in
		// the answer, the earliest a backed-off mark can serve is the window's
		// end, and until then it is not a probe target either — `earliest` reads
		// the answer this function gives.
		at := m.Until
		if set.stops() && !m.Since.IsZero() {
			if end := m.Since.Add(set.Window); end.After(at) {
				at = end
			}
		}
		if at.IsZero() {
			// A node that says a credential is held and carries no time at all —
			// no reset, no wait, no window to measure from — is a node this
			// release cannot read as a hold, and "no time" must not be read as
			// "no hold": the previous shape answered the zero time, which is
			// before now, and so reported an unknown node as an available
			// credential. Fail closed instead: nothing is served from it, and
			// nothing waits for it.
			return time.Time{}, false
		}
		return at, true
	case StateBroken:
		return time.Time{}, false
	}
	return time.Time{}, false
}

// holdInForce reports whether the mark holds the credential against work at now,
// and until when. It is the fact the two writers of a race are judged against
// (Exhaust, ProbeFailed): a credential that holds is not being selected, so a
// usage limit or a probe result arriving for one is an answer from a call that
// was already in flight when the hold was written, and never a fresh
// observation.
//
// The two states hold by different clocks, and the difference is a real one
// rather than a convenience:
//
//   - an exhausted mark holds until Ready — its reset, and the window its
//     exhaustion opened (D142). It is never probed (NeedsProbe is false for it),
//     so nothing happens against it at all until that time is behind us: every
//     429 that arrives before then is a call already in flight.
//   - a backed-off mark holds until its OWN wait, m.Until, and not until Ready.
//     Its wait is when C1.12 sends a probe, and a probe is work: when Until
//     passes the credential is being tried again, so a 429 after it is a fresh
//     observation and the doubling it leads to is C1.12's. The window may still
//     keep the credential from serving real traffic for hours after that, but it
//     does not make the probes in between pre-hold calls.
func (m Mark) holdInForce(now time.Time, set Settings) (time.Time, bool) {
	switch m.State {
	case StateExhausted:
		if at, ok := m.Ready(now, set); ok && at.After(now) {
			return at, true
		}
	case StateBackedOff:
		if now.Before(m.Until) {
			return m.Until, true
		}
	}
	return time.Time{}, false
}

// Exhaust is the mark a usage limit leaves.
//
// reset is the reset time the response reported, and the zero time means it
// reported none (Check 90/U3 is where that becomes known for subscription
// traffic; until then a usage limit with no reset header is the backed-off
// case). An exhaustion with no reset time is a backoff, so it goes through the
// same doubling as a failed probe — the credential has now failed twice without
// saying when it comes back — but Since moves, because the credential *was*
// exhausted again.
//
// A hold already in force may only be EXTENDED by a report, never shortened
// (review finding N5), and that is a rule about both shapes of report rather
// than about the no-reset one. A credential that holds is not selected, so every
// 429 arriving while it holds comes from a call that was already in flight when
// the hold was written; those calls are re-reporting the same exhaustion, and
// one of them naming an EARLIER reset than the hold's must not be what hands the
// credential back early. On tier B — where D142's window is off and the hold is
// the reported reset alone — a racing 429 reporting a one-minute reset used to
// rewrite a four-hour hold into a one-minute one, and the project's paid key was
// spent again inside the window the upstream had just named. So the reset branch
// below is guarded in the same place and by the same fact as the no-reset one: a
// report that does not reach past the hold in force leaves the mark exactly as
// it is, and a report that does reach past it extends it, which is the only
// direction a hold may move.
func Exhaust(prev Mark, now, reset time.Time, set Settings) Mark {
	if prev.Broken() {
		// A credential that answered 401 is out until the operator clears it
		// (C1.21), and a usage limit is not a clear: two calls on one shared
		// token can answer 401 and 429 in either order, and the 429 arriving
		// second must not put a refused token back in rotation.
		return prev
	}
	set = set.withDefaults()
	heldUntil, held := prev.holdInForce(now, set)
	if reset.After(now) {
		// A reset that is reported is a reset: the upstream named a time, and a
		// named time is a better answer than any wait this package computes, so
		// it replaces whatever hold was in force — when it reaches past it. A
		// reset that does not is the racing case above: the hold stands, and the
		// mark is returned untouched rather than rewritten with a time this
		// credential is already held beyond.
		if held && !reset.After(heldUntil) {
			return prev
		}
		return Mark{State: StateExhausted, Until: reset, Since: now}
	}
	if held {
		// A hold is already in force on this credential, and a usage limit with
		// no reset time cannot be a fresh exhaustion of it: a credential that
		// holds is not selected (Usable refuses an exhausted one whose time has
		// not come and a backed-off one outright), so the only calls that can be
		// answering 429 here are the ones that were already in flight when the
		// hold was written. Every one of those reports the same exhaustion
		// again, and N of them must leave the state one of them left — otherwise
		// a burst of concurrent calls doubles the wait once per call and walks
		// the credential to its five-hour cap in a second. So the hold in force
		// is left exactly as it is, date and all.
		//
		// Which states hold, and by which clock, is holdInForce's: an exhausted
		// mark holds until its reset (and the window), a backed-off one until
		// its own wait. The guard used to cover only the backed-off case, so an
		// *exhausted* hold fell through to ProbeFailed, and a second in-flight
		// 429 carrying no reset header replaced the reset the upstream had just
		// reported with a fifteen-minute backoff: a four-hour reset became a
		// wait of fifteen minutes, and Select answered wait=15m for a credential
		// that was four hours from coming back. The racing call is the same call
		// in either state.
		return prev
	}
	m := ProbeFailed(prev, now, set)
	m.Since = now
	return m
}

// ProbeFailed is the mark a failed probe leaves (C1.12): the wait doubles, to
// its cap. Since does not move — the exhaustion this backoff belongs to is the
// one it was set for — so a credential that has been backed off for hours is
// still "least recently exhausted" after the one exhausted a minute ago.
func ProbeFailed(prev Mark, now time.Time, set Settings) Mark {
	if prev.Broken() {
		// For the same reason Exhaust refuses a broken mark: a probe is never
		// run on a broken credential (NeedsProbe is false for one), so this is
		// the racing case — a probe of a token that answered 401 in between —
		// and it must not be the write that takes the 401 off.
		return prev
	}
	if prev.State == StateExhausted {
		if _, held := prev.holdInForce(now, set); held {
			// The mirror of the guard in Exhaust, on the probe's side of the
			// same race. A probe launched while the credential was backed off
			// can land after the credential answered 429 with a reset: the probe
			// was in flight, the reset arrived, and the probe's failure is now
			// being reported against a mark that is no longer backed off.
			// Doubling from there would *shorten* the hold the upstream just
			// named — a four-hour reset becomes fifteen minutes, and with a
			// successful probe behind it the credential is back in rotation long
			// before the reset — so the exhausted hold wins. It is both the
			// later of the two and the one the upstream actually said.
			//
			// An exhausted mark whose hold has lapsed is not this case: there
			// the credential is in rotation again, the probe's failure is a
			// fresh observation, and the first backoff is the right answer.
			return prev
		}
	}
	set = set.withDefaults()
	wait := set.BackoffInitial
	switch {
	case prev.Backoff <= 0:
		// The first wait. It stands for a stored value that is absent, and for
		// one that is negative — a node written by hand, or a value this release
		// never writes — which must not be doubled into a longer hold.
	case prev.Backoff >= set.BackoffCap/2:
		// Doubling this reaches or passes the cap, and the comparison is the
		// cap's half rather than "wait > cap" because of what a stored value near
		// the int64 limit does: the multiply wraps to a *negative* duration, the
		// "> cap" test then passes it through untouched, and the mark comes out
		// with a wait that is already over — a credential probed again
		// immediately, for ever. Deciding at the half is deciding before the
		// multiply that would wrap.
		wait = set.BackoffCap
	default:
		wait = prev.Backoff * 2
	}
	if wait > set.BackoffCap {
		wait = set.BackoffCap
	}
	m := Mark{State: StateBackedOff, Until: now.Add(wait), Backoff: wait, Since: now}
	if !prev.Since.IsZero() {
		m.Since = prev.Since
	}
	return m
}

// ProbeSucceeded is the mark a successful probe leaves (C1.12), and it is a
// no-op unless the mark it is applied to is backed off.
//
// That restriction is the review's finding M4, and it is what keeps a probe from
// being a way to clear a mark it was never allowed to touch. A probe asks one
// question — "is the wait over for a credential that reported no reset?" — and
// only a backed-off mark is that question's subject: C1.12 says a probe is never
// run on a broken credential, so a probe's "yes" arriving for a broken one is
// the racing case (a probe launched just before the 401 landed, or two instances
// answering out of order), and it must not be the write that takes the 401 off.
// C1.9's rule is that only the operator clears a broken mark (C1.21). An
// exhausted mark is not a probe's business either: it has a reset time, and
// clearing it would cut that reset short for a credential nobody asked about.
//
// What it writes when it does apply is usually Cleared, with one exception that
// is D142's first control reaching the probe path (review finding M3): with the
// control on, a successful probe does not put a shared token back in rotation
// inside the window its exhaustion opened. The probe's "yes" answers whether the
// credential works; the control answers whether the owner's donated quota may be
// spent on it again yet, and "a probe cleared it" would otherwise be the way
// around the control — back in rotation fifteen minutes after an exhaustion,
// with the control on by default. So the mark becomes exhausted until the window
// ends, dated from the exhaustion that opened it, which is exactly the answer
// Ready gives such a mark and exactly when the credential is selected again.
func ProbeSucceeded(prev Mark, now time.Time, set Settings) Mark {
	if prev.State != StateBackedOff {
		return prev
	}
	set = set.withDefaults()
	if set.stops() && !prev.Since.IsZero() {
		if end := prev.Since.Add(set.Window); end.After(now) {
			return Mark{State: StateExhausted, Until: end, Since: prev.Since}
		}
	}
	return Cleared()
}

// Cleared is the mark the operator's clear leaves (C1.21): available, with the
// wait in force forgotten, so the next exhaustion starts at the initial backoff
// again. It is the operator's transition and not a probe's — a probe leaves what
// ProbeSucceeded answers — and it is the only transition in this package that
// takes a broken mark off: Exhaust and ProbeFailed return a broken mark
// untouched, and ProbeSucceeded is a no-op on one, so nothing a busy token
// reports and nothing a probe answers moves a 401 anywhere.
func Cleared() Mark { return Mark{State: StateAvailable} }

// Break is the mark a 401 leaves (D139). It carries the time it was noticed
// and nothing else: there is no reset to wait for, and the credential is out
// until an operator replaces the token and clears the mark (C1.21). Cleared is
// the only transition that leaves it: a usage limit and a failed probe are
// refused on a broken mark rather than overwriting it, and a *successful* probe
// is a no-op on one (ProbeSucceeded), so nothing a busy token reports — and
// nothing a probe answers — can put a refused one back in rotation.
func Break(now time.Time) Mark { return Mark{State: StateBroken, Since: now} }
