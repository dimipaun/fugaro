package runner_test

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestPlanRoundsAreRecorded: the record carries the plan the runner runs -- one
// entry per step of the loop after implement, with the rounds planOf resolved
// for it (V1.3) -- so a reader outside the runner can name a review level's cap.
// Nothing else can: the resolution reads the recipe's text, the repository's
// configuration and the rigor mode, and the record keeps none of the three.
//
// The fixture is the one that tells the layers apart: `two-rounds` caps its
// review step at 2 (recipe max_rounds) while the agent block says 3, so a record
// that recorded the config's number instead of the plan's would fail here.
func TestPlanRoundsAreRecorded(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 3, "  first_line_review: off\n"), recipeSpec(embeddedTask("two-rounds", twoRounds)))
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []runstore.PlanStepRecord{{Kind: "review", Rounds: 2}}
	if rec.Plan == nil || !slices.Equal(rec.Plan.Steps, want) {
		t.Fatalf("record's plan = %+v, want %+v (the recipe's two rounds, not the config's three)", rec.Plan, want)
	}
}
