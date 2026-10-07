package runner

import (
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// planStep is one step of the run's loop after implement, with its rounds
// decided.
type planStep struct {
	Kind   recipe.StepKind
	Rounds int
}

// planOf turns a recipe into the run's plan (docs/design/recipes.md §6).
// derived marks the catalog default, whose first_line step runs only when
// agent.first_line_review turns it on. Rounds come from the task's override,
// else the step's max_rounds, else agent.*.
func planOf(rcp *recipe.Recipe, derived bool, a config.Agent, providers map[string]config.ModelProvider, reviewOverride *int) []planStep {
	var out []planStep
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
			out = append(out, planStep{Kind: s.Kind, Rounds: n})
		case recipe.StepReview:
			n := a.ReviewRounds
			if s.MaxRounds > 0 {
				n = s.MaxRounds
			}
			if reviewOverride != nil {
				n = *reviewOverride
			}
			out = append(out, planStep{Kind: s.Kind, Rounds: n})
		}
	}
	return out
}
