package runner

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// planStep is one step of the run's loop after implement, with its rounds
// decided.
type planStep struct {
	Kind recipe.StepKind
	// Rounds is the step's rounds: F6's precedence where one of the layers
	// above the mode set a number, the mode's own row (rigor.DefaultRounds,
	// rigor.AdversarialDefault for level 3) where none did. The recipe's
	// bounds are what holds it.
	Rounds int
	// Precedence is the number F6's precedence agreed on, and 0 when nothing
	// set one — the layers above the mode, the recipe's max_rounds and the
	// task's override all having said nothing. The review step keeps it
	// because the mode's row is read again on every round (reviewRounds): a
	// mode a run raises between rounds supplies the rounds from then on, and
	// never reaches back to the ones already spent (D22).
	Precedence int
	// Bounce is review: {bounce: first_line} (recipe.Step.Bounce), copied
	// from the step: the senior review's, or the bounce the resolver inserts
	// level 3 with (O7).
	Bounce bool
	// FirstRounds is the plan's first-line rounds, copied onto the review
	// step when it bounces: 0 when the plan has no first_line step (level 1
	// off, or a recipe with no step for it and no insertion). A bounce with
	// FirstRounds == 0 behaves like a plain review step: there is no cheap
	// loop to bounce back to.
	FirstRounds int
}

// resolveLevels decides the run's three review levels (config.ResolveLevels,
// L3.1) and writes every level that runs into the run's own configuration, so
// the model a level names is the model its stage pins. It runs inside
// resolvePolicy, after applyRouting: the switch rewrites the pins a level is
// read off exactly as it rewrites the role slots, and level 1's answer is the
// coder's model, so the resolution has to see the IDs the switch left.
//
// The launcher resolves the same levels before it writes a task (L3.2), and
// this is the run's own half of it: a task written straight to the runs
// bucket, or replayed from an older one, meets the same refusals here — the
// owner's floor, the allowed_models list, the same-model rule, the
// provider-effort rule and the closed effort set (G1, D721, D725).
//
// A run with no mode resolves nothing: the mode is the resolver's first
// layer, and rigor.Known is false only for a run that never resolved one at
// all (a hand-built one; every real run's bootstrapRigor answers).
func (r *run) resolveLevels() error {
	mode := r.reachedRigor()
	if !rigor.Known(mode) {
		return nil
	}
	// A shadow run resolves against the champion's coder: level 1's own
	// answer is read off the coder's model (Agent.FirstLineOn), and the two
	// arms' stage sets have to be the same one or their records are not
	// comparable (D555). applyLevels resolves against the run's own
	// configuration, which after applyChallenger is the arm's, so the coder
	// is put back for the call and restored after it.
	arm := ""
	if r.shadow {
		arm, r.cfg.Agent.Models.Coder = r.cfg.Agent.Models.Coder, r.championCoder
	}
	levels, warns, err := r.applyLevels(config.LevelInputs{
		Mode:           mode,
		Floor:          r.policy.ReviewFloor(mode),
		Task:           r.spec.ReviewLevels,
		Recipe:         r.rcp,
		CatalogDefault: r.derived,
	})
	if r.shadow {
		r.cfg.Agent.Models.Coder = arm
		if levels.L1.On && levels.L1.Model == r.championCoder {
			// The level's model was the coder's own default, so it is this
			// arm's coder: level 1 runs in the coder's slot, and the arm's
			// coder is the challenger.
			r.setLevelModel(policy.LevelL1, arm)
		}
	}
	for _, w := range warns {
		r.d.Log.Warn("review levels: "+w.Message, "level", string(w.Level))
	}
	if err != nil {
		return fmt.Errorf("review levels: %w", err)
	}
	r.levels = levels
	return nil
}

// stepLevel is which review level a step kind belongs to, false for a kind no
// level owns: a check step is the gate (gate.go), a digest step is the
// runner's own work (digest.go), and a mode: artifact recipe's plan and render
// are artifactHasPlan's.
func stepLevel(k recipe.StepKind) (policy.Level, bool) {
	switch k {
	case recipe.StepFirstLine:
		return policy.LevelL1, true
	case recipe.StepReview:
		return policy.LevelL2, true
	case recipe.StepAdversarial:
		return policy.LevelL3, true
	}
	return "", false
}

// levelOn is whether a resolved level runs.
func levelOn(levels config.Levels, l policy.Level) bool { return resolvedLevel(levels, l).On }

// levelStep is stepLevel's inverse: the recipe step a review level runs in, ""
// for a level the plan has no step for.
func levelStep(l policy.Level) recipe.StepKind {
	switch l {
	case policy.LevelL1:
		return recipe.StepFirstLine
	case policy.LevelL2:
		return recipe.StepReview
	case policy.LevelL3:
		return recipe.StepAdversarial
	}
	return ""
}

// recipeHasStep reports whether the recipe carries a step of kind.
func recipeHasStep(rcp *recipe.Recipe, kind recipe.StepKind) bool {
	if rcp == nil || kind == "" {
		return false
	}
	for _, s := range rcp.Steps {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

// planRecipe is the recipe's steps as the run's resolved levels leave them: a
// level that does not run has its step dropped (L2.1's O7), and a level that
// runs and the recipe has no step for is inserted in the level's fixed place —
// level 1 before the review step, level 3 after it with bounce: first_line
// (ResolvedLevel.Inserted).
//
// A recipe that is not the loop is taken as written. A mode: review recipe
// allows exactly one review step and nothing else (recipe.Parse), so level 2's
// step is the one such a recipe carries however the mode's own row reads, and
// there is nowhere for level 1 or level 3 to join; dropping the review step
// instead would leave the plan with no review at all, which checkPlan refuses.
func planRecipe(rcp *recipe.Recipe, levels config.Levels) []recipe.Step {
	if rcp.Mode != recipe.ModeImplement {
		return rcp.Steps
	}
	out := make([]recipe.Step, 0, len(rcp.Steps)+2)
	if levelOn(levels, policy.LevelL1) && resolvedLevel(levels, policy.LevelL1).Inserted {
		out = append(out, recipe.Step{Kind: recipe.StepFirstLine})
	}
	for _, s := range rcp.Steps {
		if lv, ok := stepLevel(s.Kind); ok && !levelOn(levels, lv) {
			continue
		}
		out = append(out, s)
	}
	if levelOn(levels, policy.LevelL3) && resolvedLevel(levels, policy.LevelL3).Inserted {
		out = append(out, recipe.Step{Kind: recipe.StepAdversarial, Bounce: true})
	}
	return out
}

// planOf turns the recipe and the run's resolved levels into the run's plan
// (docs/design/recipes.md §6). Rounds come from the task's override, else the
// step's max_rounds, else agent.*, else the mode's own row (rigor.DefaultRounds,
// and rigor.AdversarialDefault for level 3) — a default the four layers above
// it set another number against, up or down, within the recipe's bounds (the
// owner's ruling of 2026-10-31, which answers O3).
func planOf(rcp *recipe.Recipe, levels config.Levels, a config.Agent, reviewOverride *int, mode rigor.Mode) []planStep {
	defaults := rigor.DefaultRounds(mode)
	var out []planStep
	var firstRounds int
	for _, s := range planRecipe(rcp, levels) {
		switch s.Kind {
		case recipe.StepFirstLine:
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
			// Level 3's number is its own knob and the mode's own default for
			// the step (rigor.AdversarialDefault): the senior review's rounds
			// are not a fallback for it, since a mode's row is two numbers and
			// only one of them is the senior review's.
			//
			// The bound is recipe.MaxReviewRounds, and it is the recipe's own:
			// the parser applies that limit to every step but first_line
			// (recipe.parse), so a max_rounds an adversarial step carries is
			// never above the number setRounds holds it inside. A bound of its
			// own here would have to move with the parser's, and a smaller one
			// would clamp a step the recipe accepted in silence.
			set := a.AdversarialRounds
			if s.MaxRounds > 0 {
				set = s.MaxRounds
			}
			out = append(out, planStep{Kind: s.Kind, Rounds: setRounds(set, rigor.AdversarialDefault(mode), recipe.MaxReviewRounds), Precedence: set, Bounce: s.Bounce, FirstRounds: firstRounds})
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

// adversarialRounds is reviewRounds for level 3's step: the mode's own
// adversarial default where nothing above it set a number, read again on every
// round for the same reason.
func (r *run) adversarialRounds(st planStep) int {
	return setRounds(st.Precedence, rigor.AdversarialDefault(r.reachedRigor()), recipe.MaxReviewRounds)
}

// requiredSteps are the loop steps a plan must carry, in order: the steps
// planRecipe built for the run's levels, with the kinds a planStep can be
// built from. A check step is the gate and a digest step the runner's own
// work, so neither is a step a plan could drop.
func requiredSteps(rcp *recipe.Recipe, levels config.Levels) []recipe.StepKind {
	var out []recipe.StepKind
	for _, s := range planRecipe(rcp, levels) {
		if _, ok := stepLevel(s.Kind); ok {
			out = append(out, s.Kind)
		}
	}
	return out
}

// checkLevelSteps is the contract planRecipe reads and cannot check: a level
// that runs and whose recipe has no step for it must be marked Inserted, or
// planRecipe leaves it out of the plan — and out of requiredSteps with it,
// since that walks planRecipe too — and checkPlan's comparison of the two
// would agree with itself. The level would then be one the launcher's gates
// promised and the run never held, which is what this file's guard exists to
// prevent.
//
// config.levels.inserts sets it for exactly this case, and nothing else in the
// run knows which step a level runs in: a resolution that stopped setting it
// would corrupt the plan in silence.
func checkLevelSteps(rcp *recipe.Recipe, levels config.Levels) error {
	for _, l := range []policy.Level{policy.LevelL1, policy.LevelL2, policy.LevelL3} {
		if !levelOn(levels, l) || resolvedLevel(levels, l).Inserted {
			continue
		}
		if kind := levelStep(l); recipeHasStep(rcp, kind) {
			continue
		}
		return fmt.Errorf("level %s is on and the recipe has no %s step for it, but the level is not marked inserted, so the plan would drop it", levelNames[l], levelStep(l))
	}
	return nil
}

// checkPlan refuses a plan that would silently drop a step the run's resolved
// levels require. The plan is what runs, so a step planRecipe built and planOf
// lost would be a review level the launcher's gates promised and the run never
// held — the outcome the recipe's own review step and the adversarialSince gate
// exist to prevent. It is a guard rather than a rule: planOf walks planRecipe's
// steps itself, and the comparison is what keeps the two from drifting apart.
//
// A plan with no review step at all is refused for the same reason the rule has
// always had — a run never skips the senior review it resolved, and every level
// that ran decided readiness on a round somebody held. A plan whose last step
// runs no rounds is refused because that is the same drop, one step later: the
// step is in the plan and never runs.
func checkPlan(rcp *recipe.Recipe, levels config.Levels, plan []planStep) error {
	if err := checkLevelSteps(rcp, levels); err != nil {
		return err
	}
	required := requiredSteps(rcp, levels)
	if len(required) == 0 {
		return errors.New("the plan has no review step at all: every review level the run resolved is off, so nothing would decide whether the pull request is ready")
	}
	if len(plan) == 0 {
		return fmt.Errorf("the plan has no steps and the run's levels require %d, so the run would skip one of them", len(required))
	}
	if n := len(plan); plan[n-1].Rounds < 1 {
		return fmt.Errorf("the plan's last step (%s) runs no rounds, so the run would skip it", plan[n-1].Kind)
	}
	if len(plan) != len(required) {
		return fmt.Errorf("the plan has %d step(s) and the run's levels require %d, so the run would skip one of them", len(plan), len(required))
	}
	for i, k := range required {
		if plan[i].Kind != k {
			return fmt.Errorf("the plan's step %d is %s where the run's levels require %s, so the run would skip it", i+1, plan[i].Kind, k)
		}
	}
	return nil
}
