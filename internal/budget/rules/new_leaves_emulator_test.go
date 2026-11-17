//go:build firebase

package rules

// The four leaves the spend-accuracy rules add under a model's day counter
// (`notional`, `rep`, `tlo` and `thi`), against the two documents a run really
// meets: the generation the release behind deployed, where they are not in the
// document at all, and the one this binary deploys, where they are granted
// exactly as `micros` is. The database is the only place the claim can be
// made — `TestRulesGolden` reads the same template's text — and what it proves
// is that the bump is load-bearing in both directions: a run under the old
// document cannot write them, and under the new one it can write them no
// differently from `micros` (spend-accuracy task SA5.0, D2648 as amended by
// D2650).

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The largest single write the fixture's `config/limits/maxReserveMicros`
// allows a leaf to grow by, which is what MAXRES reads.
const maxReservePerWrite = 5_000_000

func TestNewLeavesNeedTheirGeneration(t *testing.T) {
	previous, err := os.ReadFile("testdata/rules.generation6.json")
	if err != nil {
		t.Fatalf("reading the generation-6 fixture: %v", err)
	}
	current, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithRules(t, previous)
	h.set("config/limits", map[string]any{"maxReserveMicros": maxReservePerWrite})

	// A model's whole day counter, at a name the rules' `$model` wildcard takes
	// and under this project's own slug: the two things the grants below bind.
	day := fmt.Sprintf("spend/%d/repos/aurora-app/byModel/claude-sonnet-5-5/", budget.Day(time.Now()))
	run := h.run("aurora-app", "20261116-120000-ab12")

	// Under the document the release behind deployed the four leaves are not in
	// the rules at all, and Realtime Database grants nothing at a path no rule
	// names. `micros` is the control: it is admitted under the very same document
	// with the very same token, so what the four meet is the generation and not
	// the identity or the path. That the refusal is the report's own is the point
	// of the generation bump: a stage report is one multi-path update, and RTDB
	// applies those atomically, so one leaf the old document has no grant for
	// refuses the report whole rather than the four leaves of it (F10).
	for _, leaf := range []string{"notional", "rep", "tlo", "thi"} {
		h.mustDeny(run, "a "+leaf+" leaf under the generation the release behind deployed", map[string]any{day + leaf: 1_000_000})
	}
	h.mustAllow(run, "the micros leaf the generation behind already carries", map[string]any{day + "micros": 1_000_000})

	// The generation this binary deploys, put there the way `fugaro update` puts
	// one: the same database, the same namespace, one document later. The
	// counters are cleared so every case below starts from a known state, and
	// `config/limits` is rewritten because the wipe takes it with it.
	h.wipe()
	h.set("config/limits", map[string]any{"maxReserveMicros": maxReservePerWrite})
	h.deployRules(current)

	// The acceptance: each of the four is granted exactly as `micros` is, so the
	// cases are run once per leaf including `micros` itself, and a difference
	// between them fails whichever leaf is not `micros`. In order: a figure from
	// nothing; a fractional figure and a negative one (AMOUNT: a number, at
	// least zero, whole); a figure below the one the leaf holds (increase only);
	// a raise; a raise of exactly one write's worth, which MAXRES admits; and a
	// raise one micro past one write's worth, which it refuses. The last two
	// figures are read against what the step before left, so the pair is
	// MAXRES's bound and not the amount's own ceiling.
	type step struct {
		why     string
		value   any
		allowed bool
	}
	steps := []step{
		{"a figure written from nothing", 250_000, true},
		{"a fractional figure", 250_000.5, false},
		{"a negative figure", -1, false},
		{"a figure below the one the leaf holds", 249_999, false},
		{"a raise", 300_000, true},
		{"a raise of one write's worth", 300_000 + maxReservePerWrite, true},
		{"a raise one micro past one write's worth", 300_000 + 2*maxReservePerWrite + 1, false},
	}
	for _, leaf := range []string{"micros", "notional", "rep", "tlo", "thi"} {
		t.Run(leaf, func(t *testing.T) {
			for i, s := range steps {
				updates := map[string]any{day + leaf: s.value}
				if s.allowed {
					h.mustAllow(run, fmt.Sprintf("step %d, %s", i, s.why), updates)
					continue
				}
				h.mustDeny(run, fmt.Sprintf("step %d, %s", i, s.why), updates)
			}
		})
	}

	// And the leaves are this project's run's, as micros is: a run token of
	// another project is refused at each of them, which is the `auth.token.fs ==
	// $slug` conjunct of the grant rather than the presence of the leaf.
	other := h.run("aurora-other", "20261116-120000-cd34")
	for _, leaf := range []string{"micros", "notional", "rep", "tlo", "thi"} {
		h.mustDeny(other, "another project's run writing the "+leaf+" leaf", map[string]any{day + leaf: 400_000})
	}
}
