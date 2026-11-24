//go:build firebase

package rules

// A repository's cap read as its ceiling, for each of daily and per-run,
// against the real rules engine (spec §5.6, B18; plan task RC2).
// `budget.Caps.RepoDaily` and `RepoPerRun` clamp the same way (RC1), the
// clauses that carry it here are named in rules_test.go's namedRuleChanges, and
// the golden file pins the text; this file is where the deployed expressions
// are evaluated, because a clause that reads right and evaluates to something
// else is exactly what a security document must not ship.
//
// The writes are driven with the run's own token rather than through budget's
// lease client, which clamps the value before it asks: what is under test is
// what the database accepts.

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// capped is the shape the clamp is for: a run holding amt with the repository's
// node above its ceiling. There is no global per-run cap, so the default
// repository cap is the ceiling -- the one combination in which the node and
// its ceiling differ, since a global cap binds through the comparison beside
// this one -- the default is $10, and the node says $50.
func capped(amt budget.Micros) world {
	w := leased(amt)
	w.caps.Global.PerRunMicros = nil
	w.caps.Defaults.RepoPerRunMicros = mp(10 * usd)
	w.caps.Repo = &budget.RepoCaps{PerRunMicros: mp(50 * usd)}
	return w
}

// dailyWorld is the day-counter shape the rows below need: an enforcing project
// whose repository daily node says $500 -- above every ceiling they set -- with
// the day's two counters where the row wants them. The third row seeds them
// apart on purpose: that row is the one shape in which the day ceiling can
// decide.
func dailyWorld(repo, global budget.Micros) world {
	w := stdWorld()
	w.caps.Defaults.RepoDailyMicros = mp(60 * usd)
	w.caps.Repo = &budget.RepoCaps{DailyMicros: mp(500 * usd)}
	w.st = budget.State{
		Repo:   budget.Counters{Counted: repo},
		Global: budget.Counters{Counted: global},
	}
	return w
}

// TestLeaseClampsRepoCap: the lease's check reads a repository node above its
// ceiling as the ceiling, for each of daily and per-run (spec §5.6, B18). The
// red step is the per-run half's first subtest's second row: without the clamp
// the rules grant a lease that takes the run past the default, because the node
// beside it says $50.
func TestLeaseClampsRepoCap(t *testing.T) {
	t.Run("the default is the ceiling where no global cap is set", func(t *testing.T) {
		h := newHarness(t)
		w := capped(9 * usd)
		h.seed(w)
		h.mustAllow(w.client(h), "$9 + $1 reaches the $10 ceiling", leaseUpdates(w, usd))
		w = capped(10 * usd)
		h.seed(w)
		h.mustDeny(w.client(h), "$10 + $1 passes the $10 ceiling, though the node says $50", leaseUpdates(w, usd))
	})
	t.Run("the global cap is the ceiling where it is set", func(t *testing.T) {
		// Both rows hold under the document behind too -- the global comparison
		// beside this one binds no later -- so what they pin is which of the
		// two values the ceiling is, not the clamp itself.
		h := newHarness(t)
		w := capped(9 * usd)
		w.caps.Global.PerRunMicros = mp(20 * usd)
		w.caps.Defaults.RepoPerRunMicros = mp(60 * usd)
		h.seed(w)
		h.mustAllow(w.client(h), "$9 + $1 is under the $20 ceiling", leaseUpdates(w, usd))
		w = capped(20 * usd)
		w.caps.Global.PerRunMicros = mp(20 * usd)
		w.caps.Defaults.RepoPerRunMicros = mp(60 * usd)
		h.seed(w)
		h.mustDeny(w.client(h), "$20 + $1 passes the $20 ceiling, though the node says $50", leaseUpdates(w, usd))
	})
	t.Run("no ceiling leaves the node standing", func(t *testing.T) {
		h := newHarness(t)
		w := capped(30 * usd)
		w.caps.Defaults.RepoPerRunMicros = nil
		h.seed(w)
		h.mustAllow(w.client(h), "$30 + $1 is under the node's own $50", leaseUpdates(w, usd))
		w = capped(50 * usd)
		w.caps.Defaults.RepoPerRunMicros = nil
		h.seed(w)
		h.mustDeny(w.client(h), "$50 + $1 passes the node", leaseUpdates(w, usd))
	})
	t.Run("a node under its ceiling binds as it is", func(t *testing.T) {
		h := newHarness(t)
		w := capped(3 * usd)
		w.caps.Repo.PerRunMicros = mp(4 * usd)
		h.seed(w)
		h.mustAllow(w.client(h), "$3 + $1 reaches its own $4", leaseUpdates(w, usd))
		w = capped(4 * usd)
		w.caps.Repo.PerRunMicros = mp(4 * usd)
		h.seed(w)
		h.mustDeny(w.client(h), "$4 + $1 passes its own $4, though the default is $10", leaseUpdates(w, usd))
	})
	t.Run("observe mode clamps nothing", func(t *testing.T) {
		// The clamp is a cap check like the ones beside it, so it sits inside
		// the enforce mode's branch: a run past the ceiling still writes under
		// observe.
		h := newHarness(t)
		w := capped(30 * usd)
		w.caps.Mode = budget.ModeObserve
		h.seed(w)
		h.mustAllow(w.client(h), "a cap is advisory outside enforce", leaseUpdates(w, usd))
	})
	t.Run("the day counter reads its ceiling too", func(t *testing.T) {
		h := newHarness(t)
		// No global daily cap, so the ceiling is the $60 default and the node
		// says $500. The row refuses, and not only at the ceiling: with the
		// global node's dailyMicros unset the global counter's own check reads
		// null and refuses every growing write, so an enforcing project in this
		// shape grants no day at all, ceiling or no ceiling.
		w := dailyWorld(60*usd, 60*usd)
		w.caps.Global.DailyMicros = nil
		h.seed(w)
		h.mustDeny(w.client(h), "$60 + $1 a day, no global daily cap, node at $500", leaseUpdates(w, usd))
	})
	t.Run("a day node under its ceiling binds as it is", func(t *testing.T) {
		h := newHarness(t)
		// The ceiling must not *loosen* the cap it is read beside: with a $30
		// node under a $60 default and a $150 global cap, the day stops at $30.
		w := dailyWorld(29*usd, 29*usd)
		w.caps.Repo.DailyMicros = mp(30 * usd)
		h.seed(w)
		h.mustAllow(w.client(h), "$29 + $1 reaches its own $30", leaseUpdates(w, usd))
		w = dailyWorld(30*usd, 30*usd)
		w.caps.Repo.DailyMicros = mp(30 * usd)
		h.seed(w)
		h.mustDeny(w.client(h), "$30 + $1 passes its own $30, though the default is $60", leaseUpdates(w, usd))
	})
	t.Run("a day counter already above the ceiling takes no more", func(t *testing.T) {
		h := newHarness(t)
		// The shape in which the day ceiling decides, and the red step for it:
		// a $150 global cap with the repository's counter at $200 while the
		// global one is at $10. The document behind allows this write -- the
		// global check is satisfied and the node beside the clamp says $500 --
		// and the clamp refuses it at $201. The two counters move together in
		// every write the rules allow (COUNTERS_FOLLOW), so a repository's
		// counter above the ceiling with the global one under it is a state an
		// admin's write leaves behind: the case the ceiling is named for.
		w := dailyWorld(200*usd, 10*usd)
		h.seed(w)
		h.mustDeny(w.client(h), "$200 + $1 a day, above the $150 global cap, node at $500", leaseUpdates(w, usd))
	})
}
