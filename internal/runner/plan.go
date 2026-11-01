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
	// Rounds is the step's rounds: F6's precedence where one of the layers
	// above the mode set a number, the mode's own row (rigor.DefaultRounds)
	// where none did. The recipe's bounds are what holds it.
	Rounds int
	// Precedence is the number F6's precedence agreed on, and 0 when nothing
	// set one — the layers above the mode, the recipe's max_rounds and the
	// task's override all having said nothing. The review step keeps it
	// because the mode's row is read again on every round (reviewRounds): a
	// mode a run raises between rounds supplies the rounds from then on, and
	// never reaches back to the ones already spent (D22).
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
// else the step's max_rounds, else agent.*, else the mode's own row
// (rigor.DefaultRounds) — a default the four layers above it set another
// number against, up or down, within the recipe's bounds (the owner's ruling
// of 2026-10-31, which answers O3).
func planOf(rcp *recipe.Recipe, derived bool, a config.Agent, providers map[string]config.ModelProvider, reviewOverride *int, mode rigor.Mode) []planStep {
	defaults := rigor.DefaultRounds(mode)
	var out []planStep
	var firstRounds int
	for _, s := range rcp.Steps {
		// A check step is the gate (checkGateSteps), and an artifact
		// recipe's plan and render are artifactHasPlan's; neither is a
		// planStep. A digest step has no arm here either: it is the runner's
		// own work, dispatched in agentLoop in place of implement
		// (digest.go), so no plan is ever built with the step silently
		// dropped.
		switch s.Kind {
		case recipe.StepFirstLine:
			if derived && !a.FirstLineOn(providers) {
				continue
			}
			set := a.FirstLineRounds
			if s.MaxRounds > 0 {
				set = s.MaxRounds
			}
			n := setRounds(set, defaults.Inner, recipe.MaxFirstLineRounds)
			firstRounds = n
			out = append(out, planStep{Kind: s.Kind, Rounds: n, Precedence: set})
		case recipe.StepReview:
			set := a.ReviewRounds
			if s.MaxRounds > 0 {
				set = s.MaxRounds
			}
			if reviewOverride != nil {
				set = *reviewOverride
			}
			out = append(out, planStep{Kind: s.Kind, Rounds: setRounds(set, defaults.Senior, recipe.MaxReviewRounds), Precedence: set, Bounce: s.Bounce, FirstRounds: firstRounds})
		}
	}
	return out
}

// setRounds is one step's rounds: the number a layer above the mode set, or
// the mode's own row where none did, held inside the recipe's bound. 0 is the
// layers' "unset" — config.Validate refuses 0 nowhere but reads it as "the mode
// supplies this", and recipe.Step.MaxRounds uses 0 the same way — so a step
// whose set is 0 takes the mode's number and never runs no rounds at all, which
// checkPlan would refuse.
func setRounds(set, modeDefault, bound int) int {
	if set == 0 {
		set = modeDefault
	}
	return min(set, bound)
}

// reviewRounds is the rounds the next senior round is spent under: the number a
// layer, the recipe or the task set, or — where none of them did — the senior
// row of the mode the run has reached *now*. It is read on every round rather
// than fixed with the plan, because D22 resolves the mode again after each
// round that changed the tree: a mode a run raises between rounds supplies the
// rounds that remain and never reaches back to the ones already spent.
func (r *run) reviewRounds(st planStep) int {
	return setRounds(st.Precedence, rigor.DefaultRounds(r.reachedRigor()).Senior, recipe.MaxReviewRounds)
}

// checkPlan refuses a plan that does not end with a review step: a run
// never skips the senior review, whatever the recipe says.
//
// It refuses the adversarial step too, and that refusal is load-bearing
// rather than a guard: planOf has no arm for the step, so a recipe carrying
// one would otherwise be planned without it and the run would open an
// ordinary pull request with review level 3 never run — the outcome the
// launcher's adversarialSince gate promises cannot happen. The step is
// level 3's, and the runner that runs it is a later release's; until then
// the only way one reaches this runner is a task written by hand or
// replayed, and the run ends here instead (L4.2 replaces this with the step
// itself).
func checkPlan(rcp *recipe.Recipe, plan []planStep) error {
	if n := len(plan); n == 0 || plan[n-1].Kind != recipe.StepReview || plan[n-1].Rounds < 1 {
		return errors.New("the plan has no trailing review step, so the run would skip the senior review")
	}
	if recipe.UsesAdversarial(rcp) {
		return errors.New("the adversarial step is not run by this runner (review level 3 ships in a newer fugaro), so the run would skip it")
	}
	return nil
}
