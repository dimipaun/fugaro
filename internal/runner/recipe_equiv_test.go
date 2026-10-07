package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestDefaultLoopGolden pins the loop of a repository that names no recipe:
// stage order, review tiers, each call's model and the outcome. It was
// written against the hard-coded loop and must pass unchanged on the
// recipe-driven one (docs/design/recipes.md §10, equivalence).
func TestDefaultLoopGolden(t *testing.T) {
	one := 1
	override := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature",
		Overrides: task.Overrides{ReviewRounds: &one}}
	for _, c := range []struct {
		name    string
		rounds  int
		extra   string
		spec    *task.Spec
		steps   []step
		stages  []string
		tiers   []string
		models  []string
		outcome runstore.Outcome
	}{
		{"first line off", 2, "  first_line_review: off\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review", "fix", "review"}, []string{":changes", ":ship"},
			[]string{"coder-model", "senior-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line on", 2, "  first_line_review: on\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("ship", 0)},
			[]string{"implement", "review_first", "fix", "review"}, []string{"first:changes", "senior:ship"},
			[]string{"coder-model", "coder-model", "coder-model", "senior-model"}, runstore.OutcomeReady},
		{"first line hands over on ship", 1, "  first_line_review: on\n  first_line_rounds: 2\n", nil,
			[]step{implement("feature"), review("ship", 0), review("changes", 2)},
			[]string{"implement", "review_first", "review"}, []string{"first:ship", "senior:changes"},
			[]string{"coder-model", "coder-model", "senior-model"}, runstore.OutcomeDraft},
		{"rounds used up", 2, "  first_line_review: off\n", nil,
			[]step{implement("feature"), review("changes", 1), fixStep, review("changes", 1)},
			[]string{"implement", "review", "fix", "review"}, []string{":changes", ":changes"},
			[]string{"coder-model", "senior-model", "coder-model", "senior-model"}, runstore.OutcomeDraft},
		{"task override wins", 3, "  first_line_review: off\n", override,
			[]step{implement("feature"), review("changes", 1)},
			[]string{"implement", "review"}, []string{":changes"},
			[]string{"coder-model", "senior-model"}, runstore.OutcomeDraft},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, firstLineCfg(t, c.rounds, c.extra), c.spec)
			rec, err := h.run(t, c.steps...)
			if err != nil {
				t.Fatal(err)
			}
			if got := stageNames(rec); !eq(got, c.stages...) {
				t.Errorf("stages = %v, want %v", got, c.stages)
			}
			if got := tiers(rec); !eq(got, c.tiers...) {
				t.Errorf("tiers = %v, want %v", got, c.tiers)
			}
			var models []string
			for _, call := range h.agent.calls {
				models = append(models, call.Model)
			}
			if !eq(models, c.models...) {
				t.Errorf("models = %v, want %v", models, c.models)
			}
			if rec.Outcome != c.outcome {
				t.Errorf("outcome = %s, want %s (reason %q)", rec.Outcome, c.outcome, rec.Reason)
			}
		})
	}
}
