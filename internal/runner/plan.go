package runner

import (
	"errors"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// planStep is one step of the run's loop after implement, with its rounds
// decided.
type planStep struct {
	Kind recipe.StepKind
	// Rounds is the step's rounds after the mode's ceiling (T3.2): F6's
	// precedence under rigor.RoundCeiling of the mode the run had reached when
	// the plan was derived.
	Rounds int
	// Precedence is F6's value before the mode's ceiling. The review step
	// keeps it because the ceiling is read again on every round
	// (reviewRounds): a mode a run raises between rounds raises the ceiling
	// for the rounds that remain, and those rounds are spent under this value
	// under the new ceiling, never under the rounds already gone (D22).
	Precedence int
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
// else the step's max_rounds, else agent.*, spent under the mode's ceiling
// (T3.2).
func planOf(rcp *recipe.Recipe, derived bool, a config.Agent, providers map[string]config.ModelProvider, reviewOverride *int, mode rigor.Mode, configured bool) []planStep {
	ceil := reviewRoundCeiling(mode, configured)
	var out []planStep
	var firstRounds int
	for _, s := range rcp.Steps {
		// A check step is the gate (checkGateSteps), and an artifact
		// recipe's plan and render are artifactHasPlan's; neither is a
		// planStep. A digest step has no arm here either, and cannot reach
		// one: it is the runner's own work, and bootstrap refuses a recipe
		// holding it (UsesDigest) before planOf is called, so no plan is
		// ever built with the step silently dropped.
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
			out = append(out, planStep{Kind: s.Kind, Rounds: n, Precedence: n})
		case recipe.StepReview:
			n := a.ReviewRounds
			if s.MaxRounds > 0 {
				n = s.MaxRounds
			}
			if reviewOverride != nil {
				n = *reviewOverride
			}
			out = append(out, planStep{Kind: s.Kind, Rounds: min(n, ceil), Precedence: n, Bounce: s.Bounce, FirstRounds: firstRounds})
		}
	}
	return out
}

// reviewRoundCeiling is the ceiling the mode puts on a review step's rounds
// (T3.2, D7b): rigor.RoundCeiling of the mode, under the recipe's own bound —
// a mode lowers the rounds F6 agrees on and never reaches above MaxReviewRounds.
//
// The repository's own rigor: block is the gate, the same
// rigor.Config.Configured the findings policy reads (outcome.go): a repository
// that says nothing about rigor is reviewed in exactly the rounds it was, which
// T3.2 asks for and which keeps the ceiling out of a repository that never
// opted into a mode. It is a gate this task carries, not one this folder's
// decisions name — D19 resolves Pro for an unconfigured repository, whose row
// would lower such a repository's rounds.
func reviewRoundCeiling(mode rigor.Mode, configured bool) int {
	if !configured {
		return recipe.MaxReviewRounds
	}
	return min(rigor.RoundCeiling(mode), recipe.MaxReviewRounds)
}

// reviewRounds is the bound the next round is spent under (T3.2): F6's
// precedence under the ceiling of the mode the run has reached *now*. It is
// read on every round rather than fixed with the plan, because D22 resolves the
// mode again after each round that changed the tree: a mode a run raises
// between rounds buys the rounds that remain and never reaches back to the ones
// already spent.
func (r *run) reviewRounds(st planStep) int {
	return min(st.Precedence, reviewRoundCeiling(r.reachedRigor(), r.rigorCfg.Configured()))
}

// checkPlan refuses a plan that does not end with a review step: a run
// never skips the senior review, whatever the recipe says.
func checkPlan(plan []planStep) error {
	if n := len(plan); n == 0 || plan[n-1].Kind != recipe.StepReview || plan[n-1].Rounds < 1 {
		return errors.New("the plan has no trailing review step, so the run would skip the senior review")
	}
	return nil
}
