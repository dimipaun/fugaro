//go:build firebase

package rules

// A repository's per-run cap read as its ceiling, against the real rules engine
// (spec §5.6, B18; plan task RC2). `budget.Caps.RepoPerRun` clamps the same way
// (RC1), the clauses that carry it here are named in rules_test.go's
// namedRuleChanges, and the golden file pins the text; this file is where the
// deployed expression is evaluated, because a clause that reads right and
// evaluates to something else is exactly what a security document must not
// ship.
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

// TestLeaseClampsRepoCap: the lease's per-run comparison reads a repository node
// above its ceiling as the ceiling. The red step is the first subtest's second
// row: without the clamp the rules grant a lease that takes the run past the
// default, because the node beside it says $50.
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
}
