package creds

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Tier is a credential's cost class: A is a shared subscription OAuth token
// (someone's donated quota, free to spend and scarce), B is the project's
// Anthropic API key (paid, bounded by the caps). The cascade walks them in that
// order — cheapest usable first — and never the other way round.
type Tier string

const (
	TierA Tier = "a"
	TierB Tier = "b"
	// TierC is reserved and never selected. It is the third-party route (an
	// OpenRouter key) that serves a Claude model under another model ID, and
	// this release does not build it: it waits for Check 50 to measure Claude's
	// prompt-cache markers through that route (D150, C3.6). The constant exists
	// so that a state node or a policy a later release writes reads as a tier
	// here and not as a name, and so that a caller offering one is refused
	// rather than quietly served by something else.
	TierC Tier = "c"
)

// Credential is one named credential a call may be routed to. It carries the
// name, never a value: every name here is read, logged (C1.13) and shown in
// `fugaro accounts ls` (C1.21), and F27's rule is that only the name travels.
type Credential struct {
	Name string
	Tier Tier
}

// Run is the run a selection is made for: the repository slug and the run id
// the service verified from the run identity (C1.10). They are the run's nodes
// under creds/, so stickiness and the lease are per run without either of them
// being a name a caller invents.
type Run struct {
	Slug string
	ID   string
}

// Selector selects the credential a run's next call goes to. It holds no state
// of its own: everything it decides is read from the Store and written back,
// because the service is stateless and several instances select at once.
type Selector struct {
	// Store is the cascade's shared state. It must not be nil.
	Store Store
	// Settings are the policy's settings as this package enforces them.
	Settings Settings
	// Now is the clock. nil is time.Now.
	Now func() time.Time
}

// Select answers the credential the run's next call goes to, or the wait until
// one is usable.
//
// The answer is one of:
//
//   - (credential, 0, nil) — send the call there. The run's current credential
//     and its lease have been written, so the next call is sticky on the same
//     one (D154) and the credential's slot is held for the lease's TTL.
//   - ("", wait, *ExhaustedUntil) — nothing can be used now and something
//     becomes usable at `wait`: answer the typed refusal (C1.10) and let the
//     runner wait (C1.17), or, when Probe is set, probe first (C1.12).
//   - ("", 0, ErrNoCredential) — nothing can be used and nothing becomes usable:
//     every admitted candidate is broken and clearing one is the operator's
//     (C1.21). The caller refuses; it never falls back to something else
//     (D143).
//   - ("", 0, ErrFallbackForbidden) — the credentials that could serve the call
//     are all of a tier this policy does not admit (tier B under `oauth-only`,
//     D152). A refusal, and a different one from the above, because the fix is
//     a policy and not a token.
//   - another error — the state could not be read or written. Fail closed: an
//     unreadable state is not an empty one.
//
// candidates is the caller's list, and Select never widens it: exactly the
// credentials the project policy admits for this call, tier A before tier B.
// Nothing here reads an admission (that is C1.11's, from what C1.2 publishes)
// and nothing here invents a credential the caller did not offer. The order
// within a tier does not matter; the tier does, and a candidate whose tier is
// not one this release selects is refused rather than skipped. A tier B
// candidate offered under `oauth-only` is not refused — it carries no mistake —
// but it is never selected, and a call that nothing else can serve is answered
// with ErrFallbackForbidden.
func (s *Selector) Select(ctx context.Context, run Run, candidates []Credential) (Credential, time.Duration, error) {
	set := s.Settings.withDefaults()
	now := s.now()
	if err := run.valid(); err != nil {
		return Credential{}, 0, err
	}
	cs, err := checkCandidates(candidates)
	if err != nil {
		return Credential{}, 0, err
	}
	marks, err := s.Store.Marks(ctx)
	if err != nil {
		return Credential{}, 0, err
	}
	leases, err := s.Store.Leases(ctx)
	if err != nil {
		return Credential{}, 0, err
	}

	// D154's stickiness first, and before any preference: a run keeps the
	// credential it is on for as long as that credential is offered, usable and
	// not held by the cap — the reason is the prompt cache, and every rule below
	// this one would move the run off it for a reason the cache does not care
	// about.
	cur, found, err := s.Store.Current(ctx, run)
	if err != nil {
		return Credential{}, 0, err
	}
	if found {
		if c, ok := lookup(cs, cur); ok && reachable(c, set) && free(c, marks, leases, run, now, set) {
			if err := s.take(ctx, run, c, now, set); err != nil {
				return Credential{}, 0, err
			}
			return c, 0, nil
		}
	}

	// The cascade, cheapest tier first, and only the tiers this policy admits
	// (tiers): under oauth-only the paid tier is not in the cascade at all, so
	// no path below — not the cascade, not the fallback at the end — can return
	// a tier B credential, and a call that only the paid key could serve is
	// refused with ErrFallbackForbidden rather than served (D152).
	for _, tier := range set.tiers() {
		group := groupOf(cs, tier)
		if len(group) == 0 {
			continue
		}
		usable := make([]Credential, 0, len(group))
		for _, c := range group {
			if free(c, marks, leases, run, now, set) {
				usable = append(usable, c)
			}
		}
		if len(usable) > 0 {
			c, err := s.leastRecentlyExhausted(ctx, usable, marks, set)
			if err != nil {
				return Credential{}, 0, err
			}
			if err := s.take(ctx, run, c, now, set); err != nil {
				return Credential{}, 0, err
			}
			return c, 0, nil
		}
		// Nothing in this tier can be used now. Whether the run waits or moves
		// to the next tier is D152's wait-or-switch rule, and it is here because
		// this is where the wait is known: it waits while the wait is inside the
		// threshold and moves on beyond it. Under oauth-only there is no next
		// tier in this loop — and so nothing to switch to — which is the whole
		// of the threshold's being "ignored" under that policy: the wait is
		// returned whatever its length.
		at, probe, ok := earliest(group, marks, leases, run, now, set)
		if !ok {
			// Every credential of this tier is broken: there is no wait to
			// answer with, and the next tier may still serve the call.
			continue
		}
		if wait := waitUntil(now, at); wait <= set.WaitThreshold {
			return Credential{}, wait, &ExhaustedUntil{At: at, Wait: wait, Probe: probe}
		}
	}

	// Neither admitted tier could serve the call and neither answered with a
	// wait inside the threshold. A credential of an admitted tier is still
	// coming back somewhere — the run waits for the earliest one rather than
	// fail: D143's halt is for a service that cannot be reached, not for
	// accounts that are exhausted.
	// A wait among the credentials this policy admits is still a wait, whatever
	// the threshold said: the threshold decides whether to move on to another
	// tier, and there is no other tier left to move to.
	allowed := reachableOnly(cs, set)
	if at, probe, ok := earliest(allowed, marks, leases, run, now, set); ok {
		wait := waitUntil(now, at)
		return Credential{}, wait, &ExhaustedUntil{At: at, Wait: wait, Probe: probe}
	}
	if len(allowed) < len(cs) {
		// Nothing this policy admits can serve the call — every one of them is
		// broken, or there are none — and the caller offered a tier it does not
		// admit. The run is refused with the error that names the policy and
		// the word that would change it (D152), rather than with the one that
		// says a token needs an operator: a subscription token that has died is
		// exactly when the two answers differ.
		return Credential{}, 0, ErrFallbackForbidden
	}
	return Credential{}, 0, ErrNoCredential
}

// tiers is the cascade's order under this policy: tier A always, tier B only
// when the policy spends. It is the one place the paid tier is admitted, so
// "oauth-only never spends" is a property of this function rather than of a
// check below it — the earlier shape of this code gated only the wait-vs-switch
// decision and fell through to the paid key whenever every shared token was
// broken, which is exactly when the platform must refuse (D152).
func (s Settings) tiers() []Tier {
	if s.Fallback.Spends() {
		return []Tier{TierA, TierB}
	}
	return []Tier{TierA}
}

// reachable reports whether a credential is of a tier this policy admits. It is
// what the sticky path is checked against as well: a run placed on the paid key
// while the policy was `auto` does not keep spending after the operator turns
// the policy back to `oauth-only` — its call waits for a shared credential, or
// is refused, and the run is moved when one is taken.
func reachable(c Credential, set Settings) bool {
	return c.Tier == TierA || set.Fallback.Spends()
}

// reachableOnly is the candidates this policy admits, in the order offered.
func reachableOnly(cs []Credential, set Settings) []Credential {
	if set.Fallback.Spends() {
		return cs
	}
	return groupOf(cs, TierA)
}

// Mark applies a state change to one credential's mark, under the store's
// compare-and-set: f is handed the mark as it is now and answers what it should
// become. It is how the service records an exhaustion, a failed probe, a 401
// and a clear, and it is the only way this package changes a mark — a caller
// never computes a mark from a value it read earlier, because that is how one
// call's mark would erase another's.
func (s *Selector) Mark(ctx context.Context, name string, f func(prev Mark, found bool) (Mark, error)) (Mark, error) {
	if name == "" {
		return Mark{}, fmt.Errorf("creds: a mark needs the credential's name")
	}
	if f == nil {
		return Mark{}, fmt.Errorf("creds: a mark of %q was asked for with no change to make", name)
	}
	return s.Store.UpdateMark(ctx, name, f)
}

// Record is Mark with the settings this selector enforces, for the two
// transitions that need them: a usage limit (which resets, or backs off) and a
// failed probe (which doubles). Both are D153's schedule, and both are here so
// that a caller does not have to hold the settings and the mark algebra apart.
func (s *Selector) Record(ctx context.Context, name string, f func(prev Mark, found bool, set Settings) (Mark, error)) (Mark, error) {
	set := s.Settings.withDefaults()
	return s.Mark(ctx, name, func(prev Mark, found bool) (Mark, error) { return f(prev, found, set) })
}

// Exhausted records a usage limit on one credential: the reset the response
// reported, or, with none, a backoff (see Exhaust). reset is the zero time when
// the response reported no reset.
func (s *Selector) Exhausted(ctx context.Context, name string, reset time.Time) (Mark, error) {
	now := s.now()
	return s.Record(ctx, name, func(prev Mark, _ bool, set Settings) (Mark, error) {
		return Exhaust(prev, now, reset, set), nil
	})
}

// ProbeFailed records a failed probe on one credential: the wait doubles, to
// its cap (C1.12).
func (s *Selector) ProbeFailed(ctx context.Context, name string) (Mark, error) {
	now := s.now()
	return s.Record(ctx, name, func(prev Mark, _ bool, set Settings) (Mark, error) {
		return ProbeFailed(prev, now, set), nil
	})
}

// Cleared records a successful probe (C1.12) — or the operator's clear of a
// broken mark (C1.21), which is the same write: both say the credential may be
// used again.
func (s *Selector) Cleared(ctx context.Context, name string) (Mark, error) {
	return s.Mark(ctx, name, func(Mark, bool) (Mark, error) { return Cleared(), nil })
}

// Broken records a 401 on one credential (D139). It is the one mark this
// package will not undo on its own: only Cleared — an operator's (C1.21) or a
// probe's, and a probe is never run on a broken credential (C1.12) — takes it
// off.
func (s *Selector) Broken(ctx context.Context, name string) (Mark, error) {
	now := s.now()
	return s.Mark(ctx, name, func(Mark, bool) (Mark, error) { return Break(now), nil })
}

// take records a decision: the run's current credential, which is D154's
// stickiness, and its lease, which is the slot it holds under D142's cap.
//
// Both are writes, and either can fail; the error is returned rather than
// swallowed, so the caller fails closed — a call is never sent on a decision
// the service could not record. The order is deliberate: the lease is written
// second, so a failure between them leaves a run that is sticky on a credential
// it holds no slot for, and its next selection takes the slot, rather than a
// run holding a slot nobody uses until the lease lapses.
func (s *Selector) take(ctx context.Context, run Run, c Credential, now time.Time, set Settings) error {
	if err := s.Store.SetCurrent(ctx, run, c.Name); err != nil {
		return err
	}
	return s.Store.SetLease(ctx, run, Lease{Credential: c.Name, Until: now.Add(set.LeaseTTL)})
}

// leastRecentlyExhausted picks from a usable set: the credential exhausted
// longest ago (D154), and among credentials exhausted equally recently — of
// which the commonest case is a set that has never been exhausted at all, where
// every `Since` is zero — a round robin through the counter in creds/.
//
// The order is by the exhaustion time and never by "who was used more": the
// state does not record uses, and it does not need to. A credential that has
// been idle for a week and one that has just come back from a reset are the
// same thing to a token's owner — quota they can spend — and the round robin is
// what spreads new runs over them evenly.
func (s *Selector) leastRecentlyExhausted(ctx context.Context, usable []Credential, marks map[string]Mark, set Settings) (Credential, error) {
	ordered := make([]Credential, len(usable))
	copy(ordered, usable)
	since := func(c Credential) time.Time { return recency(marks[c.Name]) }
	// Sorted by name as well, so the tied set has one order for every caller:
	// candidates arrive in whatever order the policy read produced, and a
	// round-robin over an unordered set is not a round robin.
	sort.Slice(ordered, func(i, j int) bool {
		si, sj := since(ordered[i]), since(ordered[j])
		if !si.Equal(sj) {
			return si.Before(sj)
		}
		return ordered[i].Name < ordered[j].Name
	})
	tied := []Credential{ordered[0]}
	for _, c := range ordered[1:] {
		if !since(c).Equal(since(ordered[0])) {
			break
		}
		tied = append(tied, c)
	}
	if len(tied) == 1 {
		return tied[0], nil
	}
	n, err := s.Store.RoundRobin(ctx)
	if err != nil {
		return Credential{}, err
	}
	if n < 0 {
		// A counter a hand-edit made negative must not index out of the set.
		n = 0
	}
	return tied[int(n%int64(len(tied)))], nil
}

// recency is how long ago a credential was last exhausted: the mark's Since,
// and the zero time for a credential that never was — which is the oldest of
// all, and so the first a new run takes.
func recency(m Mark) time.Time {
	switch m.State {
	case StateExhausted, StateBackedOff:
		return m.Since
	}
	return time.Time{}
}

// free reports whether the credential can be used by run at now: its mark holds
// nothing, and the per-token cap has room (D142's second control).
func free(c Credential, marks map[string]Mark, leases []Lease, run Run, now time.Time, set Settings) bool {
	if !marks[c.Name].Usable(now, set) {
		return false
	}
	return Free(leases, c.Name, now, run, set.concurrency())
}

// earliest is the time the first credential of a group becomes usable — by
// waiting out its mark, or by another run's lease lapsing — and whether one
// ever does (false when every credential of the group is broken). probe reports
// that the earliest one is a backed-off credential whose wait has passed, which
// becomes usable by being probed rather than by waiting (C1.12).
func earliest(cs []Credential, marks map[string]Mark, leases []Lease, run Run, now time.Time, set Settings) (time.Time, bool, bool) {
	var (
		best  time.Time
		probe bool
		found bool
	)
	for _, c := range cs {
		m := marks[c.Name]
		at, ok := m.Ready(now, set)
		if !ok {
			continue
		}
		// Probe is what the MARK needs, and it is decided before a lease has a
		// chance to push the time later: a backed-off credential whose wait has
		// passed needs probing whether or not another run is holding its slot,
		// and a caller told to sleep instead would never probe it.
		needs := !at.After(now) && m.NeedsProbe(now)
		if !Free(leases, c.Name, now, run, set.concurrency()) {
			// The cap is the other reason a credential is not usable, and it
			// lifts when the holding lease lapses. It can only push the time
			// later, never earlier: a credential both exhausted and held waits
			// for the later of the two. So with both in play the answer carries
			// both facts — probe it, and it could be used at best at `at` — and
			// neither is a lie.
			if exp, ok := EarliestExpiry(leases, c.Name, now, run); ok && exp.After(at) {
				at = exp
			}
		}
		if !found || at.Before(best) {
			best, probe, found = at, needs, true
		}
	}
	return best, probe, found
}

// waitUntil is at minus now, never negative: a time in the past is a wait of
// nothing, and a negative duration in an answer is a caller's bug waiting to be
// read as "the reset is behind us, so try again" forever.
func waitUntil(now, at time.Time) time.Duration {
	if d := at.Sub(now); d > 0 {
		return d
	}
	return 0
}

// lookup finds a credential in a candidate list by name.
func lookup(cs []Credential, name string) (Credential, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return Credential{}, false
}

// groupOf is the candidates of one tier, in the order they were offered.
func groupOf(cs []Credential, tier Tier) []Credential {
	var out []Credential
	for _, c := range cs {
		if c.Tier == tier {
			out = append(out, c)
		}
	}
	return out
}

// checkCandidates refuses a candidate list this package cannot select from, and
// copies the ones it can. Every refusal is a caller's mistake reaching the
// wrong place, and none of them is skipped quietly:
//
//   - an empty list is ErrNoCandidates, because a caller with no credential to
//     offer (an admission that dropped every account, a bundle read that came
//     back empty) must see that, not an exhausted account;
//   - a tier C candidate is refused, because this release does not select it
//     (D150, C3.6) and serving the call from another tier would hide the fact
//     that the policy asked for something not built;
//   - an unknown tier is refused for the same reason, and an empty name too;
//   - the same name twice is refused: it would be one credential counted twice
//     in a tie, which is a worse round robin for a reason nobody could see.
func checkCandidates(cs []Credential) ([]Credential, error) {
	if len(cs) == 0 {
		return nil, ErrNoCandidates
	}
	out := make([]Credential, 0, len(cs))
	seen := make(map[string]bool, len(cs))
	for _, c := range cs {
		if c.Name == "" {
			return nil, fmt.Errorf("creds: a candidate of tier %q has no name", c.Tier)
		}
		switch c.Tier {
		case TierA, TierB:
		case TierC:
			return nil, fmt.Errorf("creds: %q is tier C, and this release does not select a third-party route for a Claude model: it waits for Check 50 (D150, C3.6)", c.Name)
		default:
			return nil, fmt.Errorf("creds: %q has tier %q, which is not a tier this Fugaro knows: it selects tier a (a shared subscription token) and tier b (the project's API key)", c.Name, c.Tier)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("creds: %q is offered twice, and one credential offered twice would be counted twice", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}

func (s *Selector) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (r Run) valid() error {
	if r.Slug == "" || r.ID == "" {
		return fmt.Errorf("creds: a selection needs the run identity's repository and run id, and this one is slug %q, run %q", r.Slug, r.ID)
	}
	return nil
}
