package orchestrator_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestCapsStopALoop: the caps are what stops a daemon from starting everything
// at once — the per-cycle cap bounds one tick and the running cap bounds the
// whole loop, so a node full of approved work settles instead of spinning.
func TestCapsStopALoop(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	items := make([]orchestrator.WorkItem, 0, 5)
	for i := range 5 {
		items = append(items, o6Item(fmt.Sprintf("wi-%d", i+1), orchestrator.PhaseQueued, approvedAt))
	}
	s := o6Node(t, now, items...)
	caps := withCaps(func(c *orchestrator.Caps) {
		c.MaxPerCycle = 2
		c.MaxRunning = 3
	})

	first := orchestrator.Plan(s, orchestrator.RunView{}, caps, now)
	if len(first) != 2 {
		t.Fatalf("the cycle plans %d launches and the per-cycle cap is 2: %+v", len(first), first)
	}

	o := newO6Starter(t, s, now, "20261107-120000-aaaa", "20261107-120000-bbbb", "20261107-120000-cccc")
	for _, a := range first {
		if err := o.run(a); err != nil {
			t.Fatalf("launching %s was refused: %v", a.ItemID, err)
		}
	}

	// Two runs in flight, a running cap of three and two more items ready: the
	// next cycle plans one, and the one after that plans nothing at all.
	second := orchestrator.Plan(o.s, orchestrator.RunView{}, caps, now.Add(time.Minute))
	if len(second) != 1 {
		t.Fatalf("the cycle with two runs in flight and a running cap of 3 plans %+v", second)
	}
	if err := o.run(second[0]); err != nil {
		t.Fatalf("launching %s was refused: %v", second[0].ItemID, err)
	}
	if got := orchestrator.Plan(o.s, orchestrator.RunView{}, caps, now.Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("the cycle with three runs in flight plans %+v", got)
	}
	// Two items are still approved and ready, and every attempt stays
	// uncommitted: the cap held them, not a decision about them.
	for _, id := range []string{"wi-4", "wi-5"} {
		if n := orchestrator.AttemptsUnder(o.s, id, approvedAt); n != 0 {
			t.Errorf("%s carries %d committed attempts", id, n)
		}
	}
}

// TestKillSwitchStopsSpawning: one flag stops every start — the first attempt
// and the retry as much as a recovery — and stops nothing else, so the state
// is one the switch can be lifted on.
func TestKillSwitchStopsSpawning(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	item, failed := o6FailedItem("wi-1", approvedAt, now.Add(-time.Minute))
	queued := o6Item("wi-2", orchestrator.PhaseQueued, approvedAt)
	s := o6Node(t, now, item, queued)
	s.Intents = []orchestrator.Intent{failed}
	off := withCaps(func(c *orchestrator.Caps) { c.KillSwitch = true })

	if got := orchestrator.Plan(s, orchestrator.RunView{}, off, now); len(got) != 0 {
		t.Fatalf("the plan with the kill switch on is %+v", got)
	}
	got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now)
	if len(got) != 2 {
		t.Fatalf("the plan with the kill switch off is %+v", got)
	}
	if got[0].Kind != orchestrator.ActionRetry || got[1].Kind != orchestrator.ActionLaunch {
		t.Errorf("the plan with the kill switch off is %+v", got)
	}

	// A run that never started is not re-issued either: the switch stops every
	// start, and the attempt waits under the run id it recorded until it is
	// off.
	openItem, open := o6OpenItem("wi-3", "20261107-120000-cccc", approvedAt, now.Add(-orchestrator.ClaimTTL))
	rec := o6Node(t, now, openItem)
	rec.Intents = []orchestrator.Intent{open}
	if got := orchestrator.Plan(rec, orchestrator.RunView{}, off, now); len(got) != 0 {
		t.Fatalf("the plan with the kill switch on is %+v", got)
	}
	a := onlyAction(t, orchestrator.Plan(rec, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionReissue {
		t.Fatalf("the plan with the kill switch off is %+v, not a re-issue", a)
	}
}

// TestCapsSurviveARestart: the counts are derived from the intents the node
// carries and the server's clock, so a daemon that has just started counts
// what the node says. Nothing about a cap lives in the process, and a cap that
// stopped a loop still stops it after a restart.
func TestCapsSurviveARestart(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	item, failed := o6FailedItem("wi-1", approvedAt, now.Add(-orchestrator.WindowMinute/2))
	other, inflight := o6OpenItem("wi-2", "20261107-115900-bbbb", approvedAt, now.Add(-orchestrator.WindowMinute/4))
	s := o6Node(t, now, item, other)
	s.Intents = []orchestrator.Intent{failed, inflight}
	caps := withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 2 })

	if got := orchestrator.Plan(s, orchestrator.RunView{}, caps, now); len(got) != 0 {
		t.Fatalf("the cycle with two attempts in the minute plans %+v and the minute cap is 2", got)
	}

	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the node does not encode: %v", err)
	}
	restarted, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("the node does not decode: %v", err)
	}
	if got, want := orchestrator.CountsOf(restarted, now), orchestrator.CountsOf(s, now); got != want {
		t.Errorf("the restarted daemon counts %+v where the state says %+v", got, want)
	}
	if got := orchestrator.Plan(restarted, orchestrator.RunView{}, caps, now); len(got) != 0 {
		t.Fatalf("the restarted daemon plans %+v and the minute cap is still 2", got)
	}

	// Widen the cap and the attempt that was held is the one that starts: what
	// stopped it was the count, and not a decision about the item.
	wider := withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 3 })
	a := onlyAction(t, orchestrator.Plan(restarted, orchestrator.RunView{}, wider, now))
	if a.Kind != orchestrator.ActionRetry || a.ItemID != "wi-1" {
		t.Fatalf("the plan is %+v, not the retry wi-1 was held back from", a)
	}
}

// TestRetryCountsTowardEveryCap: a retry is a run like any other. It takes the
// room the first attempt takes, under every cap, so a loop of failures cannot
// spend more than spawns are allowed.
func TestRetryCountsTowardEveryCap(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	failedState := func(t *testing.T, at time.Time, extra ...orchestrator.WorkItem) orchestrator.State {
		t.Helper()
		item, failed := o6FailedItem("wi-1", approvedAt, at)
		s := o6Node(t, now, append([]orchestrator.WorkItem{item}, extra...)...)
		s.Intents = []orchestrator.Intent{failed}
		return s
	}
	inflight := func(t *testing.T) orchestrator.WorkItem {
		t.Helper()
		other, _ := o6OpenItem("wi-2", "20261107-115900-bbbb", approvedAt, now.Add(-orchestrator.WindowMinute/2))
		return other
	}

	tests := []struct {
		name   string
		narrow orchestrator.Caps
		wide   orchestrator.Caps
		held   int // the actions the narrow caps leave room for
		build  func(t *testing.T) orchestrator.State
	}{
		{
			name:   "the running cap",
			narrow: withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 1 }),
			wide:   withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 2 }),
			held:   0,
			build: func(t *testing.T) orchestrator.State {
				s := failedState(t, now.Add(-orchestrator.WindowMinute/2), inflight(t))
				_, open := o6OpenItem("wi-2", "20261107-115900-bbbb", approvedAt, now.Add(-orchestrator.WindowMinute/2))
				s.Intents = append(s.Intents, open)
				return s
			},
		},
		{
			name:   "the minute's spawns",
			narrow: withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 1 }),
			wide:   withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 2 }),
			held:   0,
			build:  func(t *testing.T) orchestrator.State { return failedState(t, now.Add(-orchestrator.WindowMinute/2)) },
		},
		{
			name:   "the hour's spawns",
			narrow: withCaps(func(c *orchestrator.Caps) { c.SpawnPerHour = 1 }),
			wide:   withCaps(func(c *orchestrator.Caps) { c.SpawnPerHour = 2 }),
			held:   0,
			build:  func(t *testing.T) orchestrator.State { return failedState(t, now.Add(-orchestrator.WindowHour/2)) },
		},
		{
			name:   "the day's spawns",
			narrow: withCaps(func(c *orchestrator.Caps) { c.SpawnPerDay = 1 }),
			wide:   withCaps(func(c *orchestrator.Caps) { c.SpawnPerDay = 2 }),
			held:   0,
			build:  func(t *testing.T) orchestrator.State { return failedState(t, now.Add(-orchestrator.WindowDay/2)) },
		},
		{
			name:   "the per-cycle cap",
			narrow: withCaps(func(c *orchestrator.Caps) { c.MaxPerCycle = 1 }),
			wide:   withCaps(func(c *orchestrator.Caps) { c.MaxPerCycle = 2 }),
			held:   1,
			build: func(t *testing.T) orchestrator.State {
				return failedState(t, now.Add(-orchestrator.WindowMinute/2), o6Item("wi-2", orchestrator.PhaseQueued, approvedAt))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.build(t)
			held := orchestrator.Plan(s, orchestrator.RunView{}, tc.narrow, now)
			if len(held) != tc.held {
				t.Fatalf("the plan under the narrow cap is %+v, and %d of them fit", held, tc.held)
			}
			wide := orchestrator.Plan(s, orchestrator.RunView{}, tc.wide, now)
			kinds := make([]orchestrator.ActionKind, 0, len(wide))
			for _, a := range wide {
				kinds = append(kinds, a.Kind)
				if a.ItemID == "wi-1" && a.Kind != orchestrator.ActionRetry {
					t.Errorf("wi-1 is planned as %s, and its first attempt failed", a.Kind)
				}
			}
			if !slices.Contains(kinds, orchestrator.ActionRetry) {
				t.Fatalf("the plan under the wide cap is %+v, and the retry is not among them", wide)
			}
		})
	}
}

// TestOversizeStateStopsSpawning: a node over its size cap fails closed. The
// counts a cap is read against are the intents the node still carries, and a
// node too big to write is one whose record can no longer be trusted to hold
// them, so the plan starts nothing at all — the same item that starts when the
// node is small.
func TestOversizeStateStopsSpawning(t *testing.T) {
	now := o6Now()
	bloated := bloatedState(t, now)

	// The item the node carries is approved over the spec it now holds — the
	// fixture lengthens the brief after approving it, which leaves a stale
	// approval, and a plan that starts nothing for that reason would pin the
	// digest refusal rather than the size. With it put back, the size is the
	// only thing between this item and a launch.
	i := slices.IndexFunc(bloated.WorkItems, func(w orchestrator.WorkItem) bool { return w.ID == "wi-approved" })
	if i < 0 {
		t.Fatal("the node under test carries no approved item")
	}
	approved := approvedItem(t, "wi-approved", now, now.Add(-30*time.Minute))
	bloated.WorkItems[i] = approved
	if w := bloated.WorkItems[i]; w.Approved == nil || w.Approved.SpecDigest != orchestrator.SpecDigest(w) {
		t.Fatalf("the item under test %+v is not approved over the spec it has", w.Approved)
	}

	if !orchestrator.OverCap(bloated) {
		t.Fatalf("the state under test is %d bytes and the cap is %d", stateBytes(t, bloated), orchestrator.MaxStateBytes)
	}
	if got := orchestrator.Plan(bloated, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("a node over its size cap planned %+v", got)
	}

	// The same item in a node with room: the size is what stopped it.
	a := onlyAction(t, orchestrator.Plan(o6Node(t, now, approved), orchestrator.RunView{}, o6Caps(), now))
	if a.ItemID != "wi-approved" || a.Kind != orchestrator.ActionLaunch {
		t.Fatalf("the same item in a small node plans %+v", a)
	}
}

// TestFutureStampCountsInEveryWindow: the stamps are the server's, and a clock
// that reads behind one of them may not be read as a window that has passed.
// An intent stamped ahead of the tick counts in the minute, the hour and the
// day at once — every age is negative and so below every window — which is the
// direction that keeps an odd stamp from freeing a cap: read the other way, one
// stamp the clock has not reached would buy a spawn past all three.
func TestFutureStampCountsInEveryWindow(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	// A failed attempt an hour ahead: it reported, so it is in no run in
	// flight, and what this test is about is the windows alone.
	item, ahead := o6FailedItem("wi-1", approvedAt, now.Add(time.Hour))
	s := o6Node(t, now, item)
	s.Intents = []orchestrator.Intent{ahead}

	got := orchestrator.CountsOf(s, now)
	if got.Minute != 1 || got.Hour != 1 || got.Day != 1 {
		t.Fatalf("an attempt stamped an hour ahead counts %+v, and an age below every window is a stamp in all of them", got)
	}
	if got.Running != 0 {
		t.Errorf("the reported attempt counts as %d run(s) in flight", got.Running)
	}

	for _, tc := range []struct {
		name string
		caps orchestrator.Caps
	}{
		{"the minute", withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 1 })},
		{"the hour", withCaps(func(c *orchestrator.Caps) { c.SpawnPerHour = 1 })},
		{"the day", withCaps(func(c *orchestrator.Caps) { c.SpawnPerDay = 1 })},
	} {
		if got.Room(tc.caps, 0) {
			t.Errorf("the cycle has room under %s's cap of 1 while a future-stamped attempt counts in it", tc.name)
		}
	}

	// What that is worth on the money path: the retry this state is otherwise
	// ready for is not planned, and the stamp the clock has not reached is why.
	tight := withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 1 })
	if plan := orchestrator.Plan(s, orchestrator.RunView{}, tight, now); len(plan) != 0 {
		t.Fatalf("the cycle plans %+v, and the future-stamped attempt holds the minute", plan)
	}
	a := onlyAction(t, orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now))
	if a.Kind != orchestrator.ActionRetry || a.ItemID != "wi-1" {
		t.Fatalf("the wide caps plan %+v, and the retry is the one thing this state waits for", a)
	}
}
