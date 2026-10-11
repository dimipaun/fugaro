package runner

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// planStep is one step of the run's loop after implement, with its rounds
// decided.
type planStep struct {
	Kind   recipe.StepKind
	Rounds int
	// Bounce is review: {bounce: first_line} (recipe.Step.Bounce), copied
	// from the recipe's review step.
	Bounce bool
	// FirstRounds is the plan's first-line rounds, copied onto the review
	// step when it bounces: 0 when the plan has no first_line step (under
	// derived, with first-line off). A bounce with FirstRounds == 0 behaves
	// like a plain review step: there is no cheap loop to bounce back to.
	FirstRounds int
}

// planOf turns a recipe into the run's plan (docs/design/recipes.md §6).
// derived marks the catalog default, whose first_line step runs only when
// agent.first_line_review turns it on. Rounds come from the task's override,
// else the step's max_rounds, else agent.*.
func planOf(rcp *recipe.Recipe, derived bool, a config.Agent, providers map[string]config.ModelProvider, reviewOverride *int) []planStep {
	var out []planStep
	var firstRounds int
	for _, s := range rcp.Steps {
		switch s.Kind {
		case recipe.StepFirstLine:
			if derived && !a.FirstLineOn(providers) {
				continue
			}
			n := a.FirstLineRounds
			if s.MaxRounds > 0 {
				n = s.MaxRounds
			}
			firstRounds = n
			out = append(out, planStep{Kind: s.Kind, Rounds: n})
		case recipe.StepReview:
			n := a.ReviewRounds
			if s.MaxRounds > 0 {
				n = s.MaxRounds
			}
			if reviewOverride != nil {
				n = *reviewOverride
			}
			out = append(out, planStep{Kind: s.Kind, Rounds: n, Bounce: s.Bounce, FirstRounds: firstRounds})
		}
	}
	return out
}

// checkPlan refuses a plan that does not end with a review step: a run
// never skips the senior review, whatever the recipe says.
func checkPlan(plan []planStep) error {
	if n := len(plan); n == 0 || plan[n-1].Kind != recipe.StepReview || plan[n-1].Rounds < 1 {
		return errors.New("the plan has no trailing review step, so the run would skip the senior review")
	}
	return nil
}
