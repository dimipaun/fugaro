package rigor

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// TestRoundCeilingIsTheModesTable pins D7b's per-mode ceiling (T3.2): one
// number per mode, the cheaper the mode the fewer the rounds. POC's and
// Hotfix's rows carry one round, and O3's floor is what makes the answer two
// until the owner rules on whether a mode may go below today's default — so
// the table's own number stays visible here while the clamp lives in
// RoundCeiling.
func TestRoundCeilingIsTheModesTable(t *testing.T) {
	if got := roundCeilings[POC]; got != 1 {
		t.Errorf("POC's row = %d; want the single round O3's question is about", got)
	}
	if got := roundCeilings[Hotfix]; got != 1 {
		t.Errorf("Hotfix's row = %d; want the single round O3's question is about", got)
	}
	want := map[Mode]int{POC: 2, Startup: 2, Pro: 3, Critical: widestRoundCeiling, Hotfix: 2}
	for m, n := range want {
		if got := RoundCeiling(m); got != n {
			t.Errorf("RoundCeiling(%s) = %d; want %d", m, got, n)
		}
	}
}

// TestAdversarialCeilingNeverRaises: the adversarial pass may not spend more
// rounds than the review does, at any mode (D726, L2.2). While O3 is open that
// means the same floor of two everywhere; the second loop is the part that
// outlives the answer, since it is the property the first one's constant
// stands for.
func TestAdversarialCeilingNeverRaises(t *testing.T) {
	for _, m := range Modes {
		if got := AdversarialCeiling(m); got > minRoundCeiling {
			t.Errorf("AdversarialCeiling(%s) = %d; want at most minRoundCeiling %d while O3 is open", m, got, minRoundCeiling)
		}
	}
	for _, m := range append(Modes, Mode(""), Mode("poc ")) {
		if got, ceiling := AdversarialCeiling(m), RoundCeiling(m); got > ceiling {
			t.Errorf("AdversarialCeiling(%q) = %d; want at most the review's ceiling %d", m, got, ceiling)
		}
	}
}

// TestRoundCeilingHasARowForEveryMode: every mode of the vocabulary has its own
// row, so a mode added to Modes without one fails here rather than silently
// taking the widest ceiling RoundCeiling gives a mode it cannot place.
func TestRoundCeilingHasARowForEveryMode(t *testing.T) {
	for _, m := range Modes {
		if _, ok := roundCeilings[m]; !ok {
			t.Errorf("roundCeilings has no row for %s", m)
		}
	}
	if len(roundCeilings) != len(Modes) {
		t.Errorf("roundCeilings has %d rows; want one per mode, %d", len(roundCeilings), len(Modes))
	}
}

// TestRoundCeilingRowsAreWithinTheRecipeBound: no row of the table reaches
// above widestRoundCeiling, so no mode buys a run more rounds than the recipe
// itself allows (T3.2). Asserted over the raw rows rather than through
// RoundCeiling, whose two clamps would hide a row the table should not carry:
// a Critical row of twenty would still answer MaxReviewRounds and pass a test
// that reads only RoundCeiling's answer.
func TestRoundCeilingRowsAreWithinTheRecipeBound(t *testing.T) {
	for m, n := range roundCeilings {
		if n < 1 || n > widestRoundCeiling {
			t.Errorf("roundCeilings[%s] = %d; want 1..%d", m, n, widestRoundCeiling)
		}
	}
	if widestRoundCeiling != recipe.MaxReviewRounds {
		t.Errorf("widestRoundCeiling = %d; want recipe.MaxReviewRounds %d", widestRoundCeiling, recipe.MaxReviewRounds)
	}
}

// TestRoundCeilingNeverLowersABelowTheRecipeBound: a mode the table cannot
// place takes the widest ceiling, the fail-closed reading table.go gives a mode
// it cannot read — an unplaced mode lowers nothing.
func TestRoundCeilingNeverLowersABelowTheRecipeBound(t *testing.T) {
	for _, m := range []Mode{"", "Critical", "poc "} {
		if got := RoundCeiling(m); got != widestRoundCeiling {
			t.Errorf("RoundCeiling(%q) = %d; want the widest ceiling %d", m, got, widestRoundCeiling)
		}
	}
	if widestRoundCeiling != recipe.MaxReviewRounds {
		t.Errorf("widestRoundCeiling = %d; want recipe.MaxReviewRounds %d, or the code guard and the recipe bound have drifted",
			widestRoundCeiling, recipe.MaxReviewRounds)
	}
}
