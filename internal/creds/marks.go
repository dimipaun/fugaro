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
func (m Mark) NeedsProbe(now time.Time) bool {
	return m.State == StateBackedOff && !now.Before(m.Until)
}

// Ready is the time the credential becomes usable again by waiting, and
// whether it ever does. It is false only for a broken credential, which no
// amount of waiting clears.
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
	case StateExhausted:
		at := m.Until
		if set.stops() && !m.Since.IsZero() {
			if end := m.Since.Add(set.Window); end.After(at) {
				at = end
			}
		}
		return at, true
	case StateBackedOff:
		// The wait ends at Until; what happens then is a probe, which either
		// clears the mark or doubles it (C1.12).
		return m.Until, true
	case StateBroken:
		return time.Time{}, false
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
func Exhaust(prev Mark, now, reset time.Time, set Settings) Mark {
	if prev.Broken() {
		// A credential that answered 401 is out until the operator clears it
		// (C1.21), and a usage limit is not a clear: two calls on one shared
		// token can answer 401 and 429 in either order, and the 429 arriving
		// second must not put a refused token back in rotation.
		return prev
	}
	if !reset.IsZero() && reset.After(now) {
		return Mark{State: StateExhausted, Until: reset, Since: now}
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
	set = set.withDefaults()
	wait := set.BackoffInitial
	if prev.Backoff > 0 {
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

// Cleared is the mark a successful probe (C1.12) or the operator's clear
// (C1.21) leaves: available, with the wait in force forgotten, so the next
// exhaustion starts at the initial backoff again.
func Cleared() Mark { return Mark{State: StateAvailable} }

// Break is the mark a 401 leaves (D139). It carries the time it was noticed
// and nothing else: there is no reset to wait for, and the credential is out
// until an operator replaces the token and clears the mark (C1.21). Cleared is
// the only transition that leaves it: a usage limit and a failed probe are
// refused on a broken mark rather than overwriting it, so nothing a busy token
// reports can put a refused one back in rotation.
func Break(now time.Time) Mark { return Mark{State: StateBroken, Since: now} }
