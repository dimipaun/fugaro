package runner

import (
	"errors"
	"slices"

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
// agent.first_line_review turns it on and whose adversarial step runs only
// where the mode's levels run review level 3. Rounds come from the task's
// override, else the step's max_rounds, else agent.*, else the mode's own row
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
		// dropped. Every other step the parser accepts is planned here, so
		// the loop runs exactly what checkPlan has just read.
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
		case recipe.StepAdversarial:
			// Level 3 has no agent.* knob of its own yet (L1.3/L4.4 add one),
			// so the step's own max_rounds or the mode's senior row is what it
			// spends (rigor.AdversarialDefault). Only the derived default
			// follows the level: a recipe that writes the step is taken as
			// written, exactly as a recipe's own first_line step is, and the
			// catalog default carries it for every mode that runs it.
			if derived && !adversarialLevelOn(mode) {
				continue
			}
			set := s.MaxRounds
			out = append(out, planStep{Kind: s.Kind, Rounds: setRounds(set, rigor.AdversarialDefault(mode), recipe.MaxReviewRounds), Precedence: set, Bounce: s.Bounce, FirstRounds: firstRounds})
		}
	}
	return out
}

// adversarialLevelOn reports whether review level 3 is on for mode m, which is
// what the derived default's adversarial step follows: the mode's own row of
// the levels table (rigor.DefaultLevels) — Critical runs it, every other mode
// leaves it off. The layers above that row (the task's review_levels, the
// owner's floor) are the level resolver's, L3.1, and the runner acts on the
// whole resolution in L4.2 (docs/review-levels-ab-jobmax/implementation-plan.md);
// this is the row below them, and it is the one a repository that configured
// no level at all gets.
//
// A mode outside the table runs the level rather than dropping it: the runner
// resolves a mode through rigor.Resolve, which answers only known ones, and a
// level this code cannot place must not be the one silently skipped.
func adversarialLevelOn(mode rigor.Mode) bool {
	ls, err := rigor.DefaultLevels(mode)
	return err != nil || ls.L3 == rigor.LevelOn
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

// checkPlan refuses a plan that does not end with a review step: the senior
// review, or level 3's adversarial pass after it (recipe.Parse allows no other
// order). A run never skips the senior review, whatever the recipe says.
//
// It reads the plan and not the recipe, because the plan is what the loop
// runs: the derived default carries the adversarial step at every mode and
// plans it only where the mode's levels run it, so a recipe that has the step
// and a plan that leaves it out is a run of the levels the mode resolved
// rather than a step silently dropped.
func checkPlan(plan []planStep) error {
	senior := slices.ContainsFunc(plan, func(st planStep) bool { return st.Kind == recipe.StepReview && st.Rounds >= 1 })
	n := len(plan)
	if n == 0 || !senior || plan[n-1].Rounds < 1 ||
		(plan[n-1].Kind != recipe.StepReview && plan[n-1].Kind != recipe.StepAdversarial) {
		return errors.New("the plan has no trailing review step, so the run would skip the senior review")
	}
	return nil
}
