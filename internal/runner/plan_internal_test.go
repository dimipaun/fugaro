package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// intp is a task override's review_rounds, which is a pointer because a task
// that sets none must be told apart from one that sets zero.
func intp(n int) *int { return &n }

// planLevels is the resolved levels a plan test hands planOf: the levels the
// test wants to run and nothing else — no model, no effort, no focus, and no
// insertion, since a level whose recipe carries its step is never inserted.
// Which levels a real run resolves is config.ResolveLevels' answer, pinned in
// internal/config; planOf's own business is what the levels do to the plan.
func planLevels(l1, l2, l3 bool) config.Levels {
	return config.Levels{
		L1: config.ResolvedLevel{On: l1},
		L2: config.ResolvedLevel{On: l2},
		L3: config.ResolvedLevel{On: l3},
	}
}

// TestPlanRoundsFollowTheMode: with no number set anywhere — no task
// override, no recipe max_rounds, no agent.* from the repository or the
// project layer — each mode's own row supplies both loops (the owner's ruling
// of 2026-10-31, O3 answered: internal/rigor's DefaultRounds). It is a
// default and not a ceiling, so the two numbers move *with* the mode rather
// than being lowered by it.
//
// Precedence is 0 on both steps: nothing set the number, which is what lets
// the review step read the mode again on every round (reviewRounds).
func TestPlanRoundsFollowTheMode(t *testing.T) {
	knobs := parseRecipe(t, "version: 1\nname: knobs\nsteps:\n  - first_line: {}\n  - review: {}\n")
	for _, tc := range []struct {
		mode       rigor.Mode
		in, senior int
	}{
		{rigor.POC, 1, 1},
		{rigor.Startup, 2, 2},
		{rigor.Pro, 3, 3},
		{rigor.Critical, 3, 5},
		{rigor.Hotfix, 1, 1},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			// Both levels on: the case this test is about is the rounds, and
			// a level that does not run has no rounds at all.
			got := planOf(knobs, planLevels(true, true, false), config.Agent{}, nil, tc.mode)
			want := []planStep{
				{Kind: recipe.StepFirstLine, Rounds: tc.in, Precedence: 0},
				{Kind: recipe.StepReview, Rounds: tc.senior, Precedence: 0, FirstRounds: tc.in},
			}
			if !slices.Equal(got, want) {
				t.Fatalf("plan = %+v, want %+v", got, want)
			}
		})
	}
}

// TestPlanRoundsPrecedence: everything above the mode's default sets another
// number, up or down, and the mode's row then supplies nothing — the task's
// override, the recipe's max_rounds, and agent.* as the repository's own
// fugaro.yaml and the project layer left it (the two merge before planOf, so
// one value arrives here; config.Resolve is where the narrower layer wins).
func TestPlanRoundsPrecedence(t *testing.T) {
	knobs := parseRecipe(t, "version: 1\nname: knobs\nsteps:\n  - first_line: {}\n  - review: {}\n")
	pinned := parseRecipe(t, "version: 1\nname: pinned\nsteps:\n  - first_line: { max_rounds: 3 }\n  - review: { max_rounds: 7 }\n")
	for _, tc := range []struct {
		name     string
		rcp      *recipe.Recipe
		a        config.Agent
		override *int
		mode     rigor.Mode
		wantIn   int
		wantSen  int
	}{
		{name: "a layer above the mode raises both", rcp: knobs, a: config.Agent{FirstLineRounds: 2, ReviewRounds: 4}, mode: rigor.POC, wantIn: 2, wantSen: 4},
		{name: "a layer below the mode lowers both", rcp: knobs, a: config.Agent{FirstLineRounds: 1, ReviewRounds: 1}, mode: rigor.Critical, wantIn: 1, wantSen: 1},
		{name: "the recipe's max_rounds outranks a layer", rcp: pinned, a: config.Agent{FirstLineRounds: 1, ReviewRounds: 1}, mode: rigor.POC, wantIn: 3, wantSen: 7},
		{name: "the override outranks the recipe", rcp: pinned, a: config.Agent{ReviewRounds: 2}, override: intp(9), mode: rigor.Startup, wantIn: 3, wantSen: 9},
		{name: "the override lowers the mode", rcp: knobs, override: intp(1), mode: rigor.Critical, wantIn: 3, wantSen: 1},
		{name: "the override leaves the first line to the mode", rcp: knobs, override: intp(2), mode: rigor.Startup, wantIn: 2, wantSen: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planOf(tc.rcp, planLevels(true, true, false), tc.a, tc.override, tc.mode)
			if len(got) != 2 {
				t.Fatalf("plan = %+v; want both steps", got)
			}
			if got[0].Rounds != tc.wantIn || got[1].Rounds != tc.wantSen {
				t.Fatalf("rounds = %d inner, %d senior; want %d and %d", got[0].Rounds, got[1].Rounds, tc.wantIn, tc.wantSen)
			}
			// The plan keeps the number a layer, the recipe or the task
			// agreed on, and 0 where only the mode supplied it.
			if tc.a.FirstLineRounds == 0 && (tc.rcp.Steps[0].MaxRounds == 0) != (got[0].Precedence == 0) {
				t.Errorf("first-line precedence = %d, want 0 exactly when nothing set the number", got[0].Precedence)
			}
		})
	}
}

// TestPlanRoundsClampToTheRecipeBounds: whatever supplies the number, it
// lands inside the recipe's own bounds — the first line at most
// recipe.MaxFirstLineRounds, the senior review at most
// recipe.MaxReviewRounds — so a value no run should carry cannot reach the
// loop. Configuration refuses these values at load (config.Validate,
// task.Validate), which is why the clamp is asserted against planOf directly.
func TestPlanRoundsClampToTheRecipeBounds(t *testing.T) {
	knobs := parseRecipe(t, "version: 1\nname: knobs\nsteps:\n  - first_line: {}\n  - review: {}\n")
	a := config.Agent{FirstLineRounds: recipe.MaxFirstLineRounds + 4, ReviewRounds: recipe.MaxReviewRounds + 3}
	got := planOf(knobs, planLevels(true, true, false), a, intp(recipe.MaxReviewRounds+5), rigor.Critical)
	if got[0].Rounds != recipe.MaxFirstLineRounds {
		t.Errorf("first-line rounds = %d; want the recipe's bound %d", got[0].Rounds, recipe.MaxFirstLineRounds)
	}
	if got[1].Rounds != recipe.MaxReviewRounds {
		t.Errorf("senior rounds = %d; want the recipe's bound %d", got[1].Rounds, recipe.MaxReviewRounds)
	}
}

// TestPlanReviewRoundsFollowTheReachedMode: when nothing set the senior
// review's number, it is read again from the mode the run has reached — not
// fixed with the plan — because D22 resolves the mode again after each round
// that changed the tree, and the ruling makes that mode supply the number. A
// number a layer set does not move: the mode supplies it only where nothing
// else did.
func TestPlanReviewRoundsFollowTheReachedMode(t *testing.T) {
	r := &run{rec: &runstore.Record{Rigor: &runstore.RigorRecord{Mode: rigor.POC}}}
	unset := planStep{Kind: recipe.StepReview, Rounds: 1, Precedence: 0}
	if got := r.reviewRounds(unset); got != 1 {
		t.Fatalf("rounds = %d at POC; want the mode's one", got)
	}
	r.rec.Rigor.Mode = rigor.Critical
	if got := r.reviewRounds(unset); got != 5 {
		t.Fatalf("rounds = %d after the mode rose to critical; want the five that remain", got)
	}
	set := planStep{Kind: recipe.StepReview, Rounds: 2, Precedence: 2}
	if got := r.reviewRounds(set); got != 2 {
		t.Fatalf("rounds = %d; want the number a layer set, whatever the mode", got)
	}
}

// TestPlanUnconfiguredRepositoryTakesTheModesRounds: a repository that wrote
// no rigor: block and sits under no project layer's mode resolves Pro (D19),
// and Pro's row is now what its runs spend — the one behaviour this ruling
// changes for a repository that opted into nothing. It is the same reading
// D19 already gives Pro's threshold table (internal/rigor's filing policy),
// and it is stated here because the alternative — silence meaning the rounds
// every mode ran before the table existed — is the one thing the ruling
// refuses.
func TestPlanUnconfiguredRepositoryTakesTheModesRounds(t *testing.T) {
	knobs := parseRecipe(t, "version: 1\nname: knobs\nsteps:\n  - first_line: {}\n  - review: {}\n")
	got := planOf(knobs, planLevels(true, true, false), config.Agent{}, nil, rigor.Default)
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 3, Precedence: 0},
		{Kind: recipe.StepReview, Rounds: 3, Precedence: 0, FirstRounds: 3},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v, want Pro's row %+v", got, want)
	}
}

func catalogRecipe(t *testing.T, name string) *recipe.Recipe {
	t.Helper()
	text, ok := recipe.CatalogText(name)
	if !ok {
		t.Fatalf("no catalog recipe %s", name)
	}
	r, ps := recipe.Parse(text)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return r
}

// parseRecipe parses an inline recipe text, failing the test on any problem.
func parseRecipe(t *testing.T, text string) *recipe.Recipe {
	t.Helper()
	r, ps := recipe.Parse([]byte(text))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return r
}

// TestPlanStepsFollowTheLevels: the plan carries a review step exactly for the
// levels that run — a level that is off has its step dropped (L2.1's O7), and a
// level that is on and whose recipe has no step for it is inserted in the
// level's fixed place: level 1 before the review step, level 3 after it with
// bounce: first_line. Which levels a run resolves is config.ResolveLevels'
// answer (the catalog default's own first_line rule among them); what the plan
// does with them is here.
func TestPlanStepsFollowTheLevels(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a := config.Agent{ReviewRounds: 4, FirstLineRounds: 2}
	// Level 1 off: the catalog default's first_line step goes with it, and the
	// review step's own rounds are the ones a layer set.
	if got := planOf(def, planLevels(false, true, false), a, nil, rigor.Critical); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 4, Precedence: 4}}) {
		t.Fatalf("level 1 off: %v", got)
	}
	if got := planOf(def, planLevels(true, true, false), a, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 4, Precedence: 4, FirstRounds: 2},
	}) {
		t.Fatalf("level 1 on: %v", got)
	}
	// Level 3 on with no step for it: the plan gains one after the review, and
	// it bounces to the cheap loop the first_line step leaves (O7).
	withL3 := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 4, Precedence: 4, FirstRounds: 2},
		{Kind: recipe.StepAdversarial, Rounds: rigor.AdversarialDefault(rigor.Critical), Bounce: true, FirstRounds: 2},
	}
	if got := planOf(def, config.Levels{
		L1: config.ResolvedLevel{On: true},
		L2: config.ResolvedLevel{On: true},
		L3: config.ResolvedLevel{On: true, Inserted: true},
	}, a, nil, rigor.Critical); !slices.Equal(got, withL3) {
		t.Fatalf("level 3 inserted: %v, want %v", got, withL3)
	}
	// Level 3 on with no first line under it: the bounce has nothing to bounce
	// to, so the step carries no first-line rounds and behaves as a plain
	// review (planStep.FirstRounds).
	noCheap := planOf(def, config.Levels{
		L2: config.ResolvedLevel{On: true},
		L3: config.ResolvedLevel{On: true, Inserted: true},
	}, a, nil, rigor.Critical)
	if n := len(noCheap); n != 2 || noCheap[1].Kind != recipe.StepAdversarial || noCheap[1].FirstRounds != 0 {
		t.Fatalf("level 3 with no first line: %v", noCheap)
	}
	// A recipe that is not the loop is taken as written: a mode: review
	// recipe's one review step is planned whatever its own level's row says,
	// because there is nowhere for a level to join and dropping it would leave
	// the plan with no review at all.
	reviewOnly := parseRecipe(t, "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n")
	if got := planOf(reviewOnly, config.Levels{}, a, nil, rigor.Critical); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 1, Precedence: 1}}) {
		t.Fatalf("mode: review: %v", got)
	}
}

func TestPlanPrecedence(t *testing.T) {
	// A 0.6-shaped recipe with pinned rounds (the retired cheap-loop-senior's
	// exact shape): the 0.7.0 catalog's own recipes take the knobs instead
	// (G6), so this is no longer a catalog lookup.
	cheap := parseRecipe(t, "version: 1\nname: cheap-loop-senior\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { max_rounds: 1 }\n")
	a := config.Agent{ReviewRounds: 5, FirstLineReview: config.FirstLineOff, FirstLineRounds: 3}
	// The recipe's own values win over agent.*, and a recipe's first_line step
	// is planned for the level that runs it: first_line_review is the
	// resolver's answer for a level 1 nothing else names, not a step's.
	if got := planOf(cheap, planLevels(true, true, false), a, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 1, Precedence: 1, FirstRounds: 2},
	}) {
		t.Fatalf("recipe over agent: %v", got)
	}
	// The task's override wins over both.
	seven := 7
	if got := planOf(cheap, planLevels(true, true, false), a, &seven, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 7, Precedence: 7, FirstRounds: 2},
	}) {
		t.Fatalf("override: %v", got)
	}
	solo := parseRecipe(t, "version: 1\nname: claude-solo\nroles:\n  reviewer: coder\nsteps:\n  - review: { max_rounds: 1 }\n")
	if got := planOf(solo, planLevels(false, true, false), a, nil, rigor.Critical); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 1, Precedence: 1}}) {
		t.Fatalf("solo: %v", got)
	}
}

// TestPlanBounceCarriesFirstRounds: a review step with bounce: first_line
// copies the plan's first-line rounds onto itself, and its own Bounce flag,
// so seniorLoop can bounce back without re-deriving anything.
func TestPlanBounceCarriesFirstRounds(t *testing.T) {
	bouncy := parseRecipe(t, "version: 1\nname: bouncy\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { bounce: first_line, max_rounds: 3 }\n")
	a := config.Agent{ReviewRounds: 5, FirstLineRounds: 1}
	got := planOf(bouncy, planLevels(true, true, false), a, nil, rigor.Critical)
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 3, Precedence: 3, Bounce: true, FirstRounds: 2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
}

// TestPlanAdversarialRounds: the adversarial step's rounds are its own knob
// and the mode's own adversarial default (rigor.AdversarialDefault), never the
// senior review's number: a mode's row is two numbers and only one of them is
// the senior review's (the owner's ruling of 2026-10-31, which answers O3).
func TestPlanAdversarialRounds(t *testing.T) {
	level3 := parseRecipe(t, "version: 1\nname: level3\nsteps:\n  - review: {}\n  - adversarial: {}\n")
	got := planOf(level3, planLevels(false, true, true), config.Agent{}, nil, rigor.Critical)
	if n := len(got); n != 2 || got[1].Kind != recipe.StepAdversarial {
		t.Fatalf("plan = %+v", got)
	}
	if got[1].Rounds != rigor.AdversarialDefault(rigor.Critical) || got[1].Precedence != 0 {
		t.Fatalf("adversarial rounds = %d (precedence %d); want the mode's %d, with nothing above it having set one",
			got[1].Rounds, got[1].Precedence, rigor.AdversarialDefault(rigor.Critical))
	}
	// A number a layer set is the step's, and the mode's row then supplies
	// nothing — so the step does not read the senior review's knob.
	pinned := parseRecipe(t, "version: 1\nname: pinned\nsteps:\n  - review: {}\n  - adversarial: { max_rounds: 2 }\n")
	a := config.Agent{ReviewRounds: 4, AdversarialRounds: 3}
	if got := planOf(pinned, planLevels(false, true, true), a, nil, rigor.Critical); got[1].Rounds != 2 || got[1].Precedence != 2 {
		t.Fatalf("recipe over agent: %+v", got)
	}
	plain := parseRecipe(t, "version: 1\nname: plain3\nsteps:\n  - review: {}\n  - adversarial: {}\n")
	if got := planOf(plain, planLevels(false, true, true), a, nil, rigor.Critical); got[1].Rounds != 3 || got[1].Precedence != 3 {
		t.Fatalf("agent.adversarial_rounds: %+v", got)
	}
}

// TestCheckPlanLevelSteps is planRecipe's own contract, which checkPlan has to
// state because planRecipe cannot: a level that runs and whose recipe has no
// step for it must be marked Inserted, or planRecipe leaves it out of the plan
// *and* out of requiredSteps, and the comparison below agrees with itself while
// the level the launcher's gates promised never runs. config.ResolveLevels sets
// the mark for exactly that case, so a resolution that stopped setting it is
// the failure this pins.
func TestCheckPlanLevelSteps(t *testing.T) {
	plain := parseRecipe(t, "version: 1\nname: plain\nsteps:\n  - first_line: {}\n  - review: {}\n")
	// Level 3 on, its step absent from the recipe and no mark: refused, and the
	// refusal names the level rather than the plan's shape, which is the same
	// plan either way.
	unmarked := planLevels(true, true, true)
	err := checkPlan(plain, unmarked, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "level 3") || !strings.Contains(err.Error(), "adversarial") {
		t.Fatalf("checkPlan(level 3 on, no adversarial step, not inserted) = %v, want the level named as dropped", err)
	}
	// The same levels with the mark the resolver sets: planRecipe inserts the
	// step in level 3's fixed place, and the plan it built is accepted.
	inserted := unmarked
	inserted.L3 = config.ResolvedLevel{On: true, Inserted: true}
	plan := planOf(plain, inserted, config.Agent{}, nil, rigor.Critical)
	if len(plan) != 3 || plan[2].Kind != recipe.StepAdversarial || !plan[2].Bounce {
		t.Fatalf("planOf(inserted level 3) = %+v, want the adversarial step inserted after the review step, with bounce", plan)
	}
	if err := checkPlan(plain, inserted, plan); err != nil {
		t.Fatalf("checkPlan(inserted level 3) = %v, want it accepted: planRecipe inserts the step", err)
	}
}

// TestCheckPlan pins the two things the run's levels require of its plan: no
// plan skips the senior review, and no plan drops a step a resolved level
// needs. planOf walks planRecipe's steps itself, so this guard is a guard
// rather than the rule — the comparison is what keeps the two from drifting
// apart, and the plan is what runs.
func TestCheckPlan(t *testing.T) {
	plain := parseRecipe(t, "version: 1\nname: plain\nsteps:\n  - first_line: {}\n  - review: {}\n")
	both := planLevels(true, true, false)
	ok := []planStep{{Kind: recipe.StepFirstLine, Rounds: 2}, {Kind: recipe.StepReview, Rounds: 1}}
	if err := checkPlan(plain, both, ok); err != nil {
		t.Fatal(err)
	}
	// No plan skips the senior review: a run whose levels hold one and whose
	// plan does not carry it would end with nothing deciding readiness.
	for _, bad := range [][]planStep{
		nil,
		{{Kind: recipe.StepFirstLine, Rounds: 2}},
		{{Kind: recipe.StepReview, Rounds: 1}, {Kind: recipe.StepFirstLine, Rounds: 1}},
	} {
		if err := checkPlan(plain, both, bad); err == nil || !strings.Contains(err.Error(), "skip") {
			t.Errorf("checkPlan(%v) = %v, want the review step's absence refused", bad, err)
		}
	}
	// A plan that would drop any other step the levels require is refused the
	// same way: the launcher's gates promised the level and the run would never
	// hold it. The steps have to be the required ones, in their order.
	if err := checkPlan(plain, both, []planStep{{Kind: recipe.StepFirstLine, Rounds: 2}, {Kind: recipe.StepAdversarial, Rounds: 1}}); err == nil || !strings.Contains(err.Error(), "skip") {
		t.Errorf("checkPlan(a step the levels do not require) = %v, want it refused", err)
	}
	// The plan's last step running no rounds is the same drop one step later:
	// the step is in the plan and never runs.
	if err := checkPlan(plain, both, []planStep{{Kind: recipe.StepFirstLine, Rounds: 2}, {Kind: recipe.StepReview}}); err == nil {
		t.Error("a trailing review step with no rounds was planned")
	}
	// A run whose levels leave no review step at all decides nothing, so the
	// plan is refused whatever it holds.
	if err := checkPlan(plain, config.Levels{}, nil); err == nil || !strings.Contains(err.Error(), "no review step at all") {
		t.Errorf("checkPlan(all levels off) = %v, want it refused", err)
	}
	// The plan follows the levels rather than the recipe: level 3 on is the
	// adversarial step, planned after the review and required there.
	level3 := parseRecipe(t, "version: 1\nname: level3\nsteps:\n  - first_line: {}\n  - review: {}\n  - adversarial: {}\n")
	withL3 := planLevels(true, true, true)
	plan3 := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 1},
		{Kind: recipe.StepAdversarial, Rounds: 5},
	}
	if err := checkPlan(level3, withL3, plan3); err != nil {
		t.Fatal(err)
	}
	// The same recipe without level 3's step is a plan that drops it, and a
	// plan that dropped the review step for the adversarial one is refused for
	// the same reason.
	for _, bad := range [][]planStep{
		ok,
		{{Kind: recipe.StepFirstLine, Rounds: 2}, {Kind: recipe.StepAdversarial, Rounds: 5}},
	} {
		if err := checkPlan(level3, withL3, bad); err == nil || !strings.Contains(err.Error(), "skip") {
			t.Errorf("checkPlan(%v) = %v, want the dropped step refused", bad, err)
		}
	}
}
