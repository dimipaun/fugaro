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
