package creds

// The pool's rule (C2, R5, D3177; plan task PL3): which member of ONE pool
// serves a call, by the rule its owner wrote the pool with.
//
// # A second selection, and not a second vocabulary
//
// A pool is a set of credentials a call may be served from. The walk that
// reaches one hands this file the members it gathered and the rule the lookup
// resolved (routing.PoolRef.Rule, C2's `ordered` / `round-robin` / `failover`),
// and what decides among them is the selection path's own rule, REUSED rather
// than restated: `round-robin` is Selector.rotate, `failover` is
// Selector.leastRecentlyExhausted, and the two questions every rule is asked
// before it picks — does this member's mark hold it, and does the cap leave
// room — are free and earliest (select.go). So a member a mark holds is held
// here for exactly the reason it is held on the cascade's own path, and a pool
// cannot drift from a tier. That is also why this file carries the Settings a
// Selector is built with and nothing else: a pool has no rule of its own beyond
// the three, and a setting invented here would be a policy input nothing wrote.
//
// # What it does not do
//
// It writes no placement. A placement — the run's current credential and its
// lease, one fact written as one write (Selector.take) — is written by the path
// that SERVES a step, and R16 makes a placement sticky for one step rather than
// for the run: the walk re-walks the chain from its first source at the next
// step, and this signature carries no step to key a placement by. The one write
// it makes is the round-robin counter's turn (Store.RoundRobin), because a
// rotation is not a rotation without it, and that counter is the project's own
// — the same one the cascade breaks an exact tie with (D1625), which is what
// keeps two rules from counting twice.
//
// It reads no configuration. ref.Rule arrives resolved (PoolRef.Rule: never
// ""), so this file never looks at a pool's block a second time, and a rule
// outside C2's three is refused rather than read as one of them: the rule was
// resolved by the side that read the pool, so a fourth name is a node a later
// release wrote or a hand-edit made, and serving it as `failover` would be
// picking by a rule nobody wrote.
//
// # The three rules
//
//   - `ordered` is the first listed member that is usable, and the pool's own
//     order is the whole of it: a member the owner listed after a held one
//     serves, and a rotation never moves it (R5).
//   - `round-robin` steps through the usable members in the listed order, one
//     selection at a time, and a usable set of one is answered without spending
//     a turn. It is the same rotation the selection path takes within a tier,
//     counter and all.
//   - `failover` is the member exhausted longest ago, with the counter breaking
//     an exact tie — which is what every pool was served by before rules
//     existed, and what a pool that names neither a rule nor a strategy is
//     still served by (RuleOrDefault).
//
// The state a member is judged by is the whole state the cascade reads for it:
// a broken member, one a mark holds with no time to wait for, and one the
// concurrency cap refuses are all equally unusable, and a pool whose every
// member is unusable answers the selection path's own two shapes — the wait
// until the earliest of them, or ErrNoCredential. The walk applies Select's
// wait-or-switch rule to the wait, so there is one rule about waiting on both
// paths (PoolPicker, slip_chain.go).

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// PoolRule is the picker the walk calls inside one pool (PoolPicker). It holds
// no state of its own, exactly as the Selector does not: everything it decides
// is read from the Store and written back to it, because the service is
// stateless and several instances pick at once.
type PoolRule struct {
	// Store is the cascade's shared state: the marks, the leases and the
	// round-robin counter, and nothing else. It must not be nil.
	Store CascadeState
	// Settings are the policy's settings as this package enforces them — the
	// same ones a selection is handed, so D142's window and cap mean the same
	// thing on this path.
	Settings Settings
	// Now is the clock. nil is time.Now.
	Now func() time.Time
}

var _ PoolPicker = PoolRule{}

// PickInPool answers the member of this pool to serve one call from, by the
// pool's rule over the members that are usable now, or the wait until one is.
//
// Its answers are the interface's own, shape for shape with Selector.Select: a
// member and a zero wait, *ExhaustedUntil with the same wait beside it, or
// ErrNoCredential. The master list is the pool's members in the order its owner
// listed them, and the order is kept in every answer but `failover`'s — which
// is the point of `ordered`, of the rotation and of the tie-break alike.
func (p PoolRule) PickInPool(ctx context.Context, ref routing.PoolRef, run Run, members []Credential) (Credential, time.Duration, error) {
	// An empty member list is a caller's mistake and never an exhausted pool,
	// so it is answered before anything else and before the run is even looked
	// at: the walk must not move past it to the chain's next source, which may
	// be a paid key (D143, PoolPicker).
	if len(members) == 0 {
		return Credential{}, 0, ErrNoCandidates
	}
	now := p.now()
	if err := run.valid(); err != nil {
		return Credential{}, 0, err
	}
	set := p.Settings.withDefaults()
	marks, err := p.Store.Marks(ctx)
	if err != nil {
		return Credential{}, 0, err
	}
	leases, err := p.Store.Leases(ctx, now)
	if err != nil {
		return Credential{}, 0, err
	}

	usable := make([]Credential, 0, len(members))
	for _, c := range members {
		if free(c, marks, leases, run, now, set) {
			usable = append(usable, c)
		}
	}
	if len(usable) > 0 {
		c, err := p.within(ctx, ref, usable, marks)
		if err != nil {
			return Credential{}, 0, err
		}
		return c, 0, nil
	}
	// Nothing here is usable now. The mark set and the leases are read once, at
	// the top, and this answer is taken from that same reading, so the wait and
	// the judgement above cannot be about two different states (select.go's own
	// note on the window that leaves open).
	if at, probe, ok := earliest(members, marks, leases, run, now, set); ok {
		wait := waitUntil(now, at)
		return Credential{}, wait, &ExhaustedUntil{At: at, Wait: wait, Probe: probe}
	}
	return Credential{}, 0, ErrNoCredential
}

// within is the pool's rule over the members that are usable now: the three
// rules of C2, with the two the selection path already has taken from it.
//
// The usable set is the pool's own members filtered once (PickInPool), which is
// what makes "the first listed member that is usable" a statement about the
// pool's order and not about a second ordering done here, and what keeps a
// rotation from ever reaching a member a mark or the cap holds.
func (p PoolRule) within(ctx context.Context, ref routing.PoolRef, usable []Credential, marks map[string]Mark) (Credential, error) {
	sel := p.selection()
	// The strategy argument is empty because this path has no pool
	// configuration to read a second time: the rule was resolved by whoever
	// read the pool (PoolRef.Rule), and RuleOrDefault's own reading of "" —
	// failover — is what a reference that arrived unresolved gets, which is the
	// rule a zero Rule means everywhere in this contract (Rule.Valid).
	switch rule := ref.Rule.RuleOrDefault(""); rule {
	case routing.RuleOrdered:
		return usable[0], nil
	case routing.RuleRoundRobin:
		return sel.rotate(ctx, usable)
	case routing.RuleFailover:
		return sel.leastRecentlyExhausted(ctx, usable, marks)
	default:
		return Credential{}, fmt.Errorf("creds: the pool %q is served by the rule %q, which is not a rule this release knows: a pool is served `ordered`, `round-robin` or `failover` (C2)", ref.Pool, ref.Rule)
	}
}

// selection is the selection path over this pool's members: rotate and
// leastRecentlyExhausted are the Selector's own (select.go), so a pool and a
// tier are served by one implementation of each rule and cannot drift apart.
//
// Its Strategy, Model and Provider are deliberately left unset: the dispatcher
// above reads the pool's rule and never Selector.within, so there is no
// strategy to set, and a pool's members are not a call's candidate list — the
// class rule and the provider tie (checkCandidates, D3000) were applied where
// the chain was narrowed, before this path was reached (CS1).
func (p PoolRule) selection() *Selector {
	return &Selector{Store: p.Store, Settings: p.Settings, Now: p.Now}
}

// now is the clock, with the same rule Selector.now states (nil is time.Now).
func (p PoolRule) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}
