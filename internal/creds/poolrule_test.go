package creds

// The pool's rule (C2, R5, D3177; plan task PL3): which member of one pool
// serves a call, by the rule the pool was written with (poolrule.go).
//
// The state is the fake the selection tests already use (select_test.go), so a
// rule here is read off the same marks, leases and counter the cascade reads,
// and nothing in this file is, or looks like, a credential: every name is a
// plain word (Convention 2).
//
// What is held apart is the thing PL3 is about. `ordered` follows the pool's
// listed order and only that; `round-robin` turns through the usable members;
// and `failover` — the rule every pool had before rules existed — is untouched,
// which is why it is pinned here rather than left to select_test.go: it is now
// reached through this file, and a picker that answered a different member than
// the cascade would be a selection that moved with nobody asking it to.
//
// The fixture marks below are what tells the three apart, and each test's
// comment says which answer it would have got from the wrong rule.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// picker is a PoolRule over a fake store with the test's clock, as selector is
// a Selector over one.
func picker(st Store, set Settings) PoolRule {
	return PoolRule{Store: st, Settings: set, Now: func() time.Time { return now }}
}

// ref is a resolved pool reference: the rule a test is about, and the threshold
// none of them writes (PL8's).
func ref(rule routing.Rule) routing.PoolRef {
	return routing.PoolRef{Project: "acme", Pool: "team", Rule: rule}
}

// pool is the fixture's member list: three shared subscriptions in the order
// the owner listed them, which is the order every answer below is read in.
func pool() []Credential { return []Credential{a1, a2, a3} }

// pick is one pick, for the call number n of the fixture run: a fresh run per
// call is the shape a rotation spreads over, and drop is the run's own hold on
// the member it is already placed on (none of these tests places one).
func pick(t *testing.T, p PoolRule, rule routing.Rule, n int, members []Credential) (Credential, time.Duration, error) {
	t.Helper()
	r := Run{Slug: run.Slug, ID: fmt.Sprintf("%s-%d", run.ID, n)}
	return p.PickInPool(ctx, ref(rule), r, members)
}

// recencyMarks is the state all three rules are read against: the member the pool
// lists FIRST was exhausted an hour ago and its reset has passed, so it is
// usable again — and it is the only member that has ever been exhausted, which
// makes it the LAST one `failover` would take and the first one the rotation
// does.
func recencyMarks(st *fakeStore) {
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})
}

// TestPickOrdered is R5's pin on the picker: `ordered` answers the first listed
// member that is usable, and the pool's own order is the whole of it.
//
// Two facts are read off the answers. The member the pool lists first is HELD
// by a mark, so the member beside it serves — a rule that answered the first
// listed member whatever its state would answer a credential the state refuses.
// And a second call under a fresh run answers the same member: a rotation,
// which the counter would have moved on by then, answers the next one, so this
// is what tells `ordered` from `round-robin` below.
func TestPickOrdered(t *testing.T) {
	st := newFakeStore()
	st.setMark(a1.Name, Mark{State: StateExhausted, Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	p := picker(st, noWindow())

	for n := 0; n < 2; n++ {
		got, wait, err := pick(t, p, routing.RuleOrdered, n, pool())
		if err != nil {
			t.Fatalf("call %d failed: %v", n, err)
		}
		if got.Name != a2.Name || wait != 0 {
			t.Errorf("ordered answered %q after %v on call %d, want %q now: the first listed member is held by its mark, and the member after it is where an ordered pool stops", got.Name, wait, n, a2.Name)
		}
	}
}

// TestPickRoundRobin is the rotation, which a pool did not have before this
// task: three usable members are served in the order the owner listed them, one
// call at a time.
//
// The state is `recencyMarks`, and that is what makes the answer evidence rather
// than the listing read out loud: the member listed FIRST is the one exhausted
// most recently, so `failover` — which the same state is read under in the test
// below — answers the second member first. A rotation that turned in any order
// but the pool's would answer the same thing here.
func TestPickRoundRobin(t *testing.T) {
	st := newFakeStore()
	recencyMarks(st)
	p := picker(st, noWindow())

	want := []Credential{a1, a2, a3}
	for n, w := range want {
		got, _, err := pick(t, p, routing.RuleRoundRobin, n, pool())
		if err != nil {
			t.Fatalf("call %d failed: %v", n, err)
		}
		if got.Name != w.Name {
			t.Fatalf("call %d was served by %q, want %q: the rotation turns through the members in the order the pool lists them", n, got.Name, w.Name)
		}
	}

	// A member a mark holds is stepped over rather than answered: the rotation
	// is over the usable set and never over the pool's list as written.
	st.setMark(a2.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(2 * time.Hour)})
	for n := 0; n < 3; n++ {
		got, _, err := pick(t, p, routing.RuleRoundRobin, 10+n, pool())
		if err != nil {
			t.Fatalf("a call with a held member failed: %v", err)
		}
		if got.Name == a2.Name {
			t.Fatalf("call %d was served by %q, which its mark holds", n, got.Name)
		}
	}
}

// TestPickFailoverIsTheSharedRule is the third rule read through the picker,
// and it is the one that must not move: `failover` answers the member exhausted
// longest ago, exactly as the cascade does within a tier, so a pool that names
// no rule is served by what it has always been served by.
//
// The state is `recencyMarks` again, read the other way: the member listed first was
// exhausted an hour ago and the other two never were, and a credential that has
// never been exhausted is the OLDEST of all — so the answer is the second
// listed member, which `ordered` above answers last and the rotation reaches
// third.
func TestPickFailoverIsTheSharedRule(t *testing.T) {
	st := newFakeStore()
	recencyMarks(st)
	p := picker(st, noWindow())

	got, _, err := p.PickInPool(ctx, ref(routing.RuleFailover), run, pool())
	if err != nil {
		t.Fatalf("the pick failed: %v", err)
	}
	if got.Name != a2.Name {
		t.Errorf("failover answered %q, want %q: the two members that were never exhausted are the tied oldest, and the pool's listed order breaks that tie", got.Name, a2.Name)
	}

	// A reference that arrived unresolved is read the way C2 reads it — a pool
	// that names neither a rule nor a strategy is served by failover — rather
	// than refused: a zero Rule is failover everywhere in this contract.
	if _, _, err := p.PickInPool(ctx, routing.PoolRef{Project: "acme", Pool: "team"}, run, pool()); err != nil {
		t.Errorf("an unresolved rule was refused: %v", err)
	}
}

// TestPickAnswersTheSelectionPathsShapes is the rest of the interface's own
// answers, so that the walk meets what Selector.Select answers and not a second
// vocabulary: the wait until the earliest member, the refusal when none becomes
// usable, and ErrNoCandidates for a caller that offered nothing at all — which
// is not an exhausted pool, and which the walk must not move past.
//
// The placement arm is the one this file's whole shape rests on: a pick writes
// no placement. Nothing is found on the run afterwards — no current credential,
// no lease — because a placement is sticky for one step and the path that
// serves the step writes it (R16, CS1).
func TestPickAnswersTheSelectionPathsShapes(t *testing.T) {
	t.Run("every member held: the earliest reset", func(t *testing.T) {
		st := newFakeStore()
		st.setMark(a1.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(30 * time.Minute)})
		st.setMark(a2.Name, Mark{State: StateExhausted, Since: now, Until: now.Add(10 * time.Minute)})
		st.setMark(a3.Name, Mark{State: StateBroken, Since: now, Until: now})
		p := picker(st, noWindow())

		_, wait, err := p.PickInPool(ctx, ref(routing.RuleOrdered), run, pool())
		var exhausted *ExhaustedUntil
		if !errors.As(err, &exhausted) {
			t.Fatalf("a pool with nothing usable answered %v, want *ExhaustedUntil", err)
		}
		if wait != 10*time.Minute || exhausted.Wait != wait || !exhausted.At.Equal(now.Add(10*time.Minute)) {
			t.Errorf("the answer waits %v (at %v), want 10m at %v: it is the earliest member that becomes usable, and a broken one never does", wait, exhausted.At, now.Add(10*time.Minute))
		}
		if exhausted.Probe {
			t.Errorf("the answer asks for a probe, and no member's wait has passed (NeedsProbe)")
		}
	})

	t.Run("a backed-off member whose wait has passed: a probe, not a sleep", func(t *testing.T) {
		st := newFakeStore()
		st.setMark(a1.Name, Mark{State: StateBackedOff, Since: now.Add(-time.Hour), Until: now.Add(-time.Minute)})
		p := picker(st, noWindow())

		_, wait, err := p.PickInPool(ctx, ref(routing.RuleOrdered), run, []Credential{a1})
		var exhausted *ExhaustedUntil
		if !errors.As(err, &exhausted) {
			t.Fatalf("a pool holding a backed-off member answered %v, want *ExhaustedUntil", err)
		}
		if !exhausted.Probe || wait != 0 {
			t.Errorf("the answer is (%v, wait %v), want a probe with no wait: the wait the mark carried has passed, and a backed-off credential is tried again by probing it (C1.12)", exhausted.Probe, wait)
		}
	})

	t.Run("nothing becomes usable: the refusal, never a fallback", func(t *testing.T) {
		st := newFakeStore()
		for _, c := range pool() {
			st.setMark(c.Name, Mark{State: StateBroken, Since: now, Until: now})
		}
		p := picker(st, noWindow())

		_, wait, err := p.PickInPool(ctx, ref(routing.RuleFailover), run, pool())
		if !errors.Is(err, ErrNoCredential) || wait != 0 {
			t.Errorf("every member broken answered (%v, %v), want ErrNoCredential with no wait", err, wait)
		}
	})

	t.Run("no members at all: the caller's mistake", func(t *testing.T) {
		p := picker(newFakeStore(), noWindow())
		if _, _, err := p.PickInPool(ctx, ref(routing.RuleOrdered), run, nil); !errors.Is(err, ErrNoCandidates) {
			t.Errorf("an empty member list answered %v, want ErrNoCandidates: the walk must not read it as an exhausted pool", err)
		}
		// And with no run identity either: a caller that offered nothing is
		// told so, whatever else is missing from the call.
		if _, _, err := p.PickInPool(ctx, ref(routing.RuleOrdered), Run{}, nil); !errors.Is(err, ErrNoCandidates) {
			t.Errorf("an empty member list and no run answered %v, want ErrNoCandidates", err)
		}
		// A run identity that is missing on its own is the selection path's own
		// error, word for word: one run, one rule about what a run is.
		if _, _, err := p.PickInPool(ctx, ref(routing.RuleOrdered), Run{}, pool()); err == nil {
			t.Errorf("a pool was picked for a run with no identity")
		}
	})

	t.Run("the pick writes no placement", func(t *testing.T) {
		st := newFakeStore()
		p := picker(st, noWindow())

		if _, _, err := p.PickInPool(ctx, ref(routing.RuleRoundRobin), run, pool()); err != nil {
			t.Fatalf("the pick failed: %v", err)
		}
		if name, found := st.currentOf(run); found {
			t.Errorf("the pool rule placed the run on %q: a placement is sticky for one step, and the walk writes it (R16)", name)
		}
		if l, found := st.lease(run); found {
			t.Errorf("the pool rule took a lease (%v), and the rule is not the path that serves the step", l)
		}
	})

	t.Run("a rule this release does not know is refused", func(t *testing.T) {
		p := picker(newFakeStore(), noWindow())
		_, _, err := p.PickInPool(ctx, ref(routing.Rule("spread-fairly")), run, pool())
		if err == nil {
			t.Fatal("a pool served by a rule outside the three served a call")
		}
		if !strings.Contains(err.Error(), "spread-fairly") {
			t.Errorf("the refusal %q does not name the rule it read", err)
		}
	})
}
