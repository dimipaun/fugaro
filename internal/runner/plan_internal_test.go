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
			// derived is false: the first_line step runs whatever
			// agent.first_line_review says, which is the case this test is
			// about.
			got := planOf(knobs, false, config.Agent{}, nil, nil, tc.mode)
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
			got := planOf(tc.rcp, false, tc.a, nil, tc.override, tc.mode)
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
	got := planOf(knobs, false, a, nil, intp(recipe.MaxReviewRounds+5), rigor.Critical)
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
	got := planOf(knobs, false, config.Agent{}, nil, nil, rigor.Default)
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

// TestPlanDefaultFollowsAgent: the catalog default is derived from agent.*
// and, since 0.15.0, from the mode's levels — Critical runs review level 3, so
// its adversarial step is in the plan here whatever the recipe's other steps
// do (TestDefaultRecipeSkipsALevelWhoseConditionIsOff is where the level
// itself is read). Critical is also the mode whose ceiling is the whole scale:
// the case is about F6's own precedence, so the mode's ceiling must not bite
// (T3.2).
func TestPlanDefaultFollowsAgent(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a := config.Agent{ReviewRounds: 4, FirstLineReview: config.FirstLineOff, FirstLineRounds: 2}
	level3 := planStep{Kind: recipe.StepAdversarial, Rounds: rigor.AdversarialDefault(rigor.Critical)}
	if got := planOf(def, true, a, nil, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepReview, Rounds: 4, Precedence: 4}, level3,
	}) {
		t.Fatalf("off: %v", got)
	}
	a.FirstLineReview = config.FirstLineOn
	level3.FirstRounds = 2
	if got := planOf(def, true, a, nil, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 4, Precedence: 4, FirstRounds: 2},
		level3,
	}) {
		t.Fatalf("on: %v", got)
	}
	// Not derived (a project or repository recipe named default): its
	// first_line step always runs.
	a.FirstLineReview = config.FirstLineOff
	if got := planOf(def, false, a, nil, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 4, Precedence: 4, FirstRounds: 2},
		level3,
	}) {
		t.Fatalf("not derived: %v", got)
	}
}

func TestPlanPrecedence(t *testing.T) {
	// A 0.6-shaped recipe with pinned rounds (the retired cheap-loop-senior's
	// exact shape): the 0.7.0 catalog's own recipes take the knobs instead
	// (G6), so this is no longer a catalog lookup.
	cheap := parseRecipe(t, "version: 1\nname: cheap-loop-senior\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { max_rounds: 1 }\n")
	a := config.Agent{ReviewRounds: 5, FirstLineReview: config.FirstLineOff, FirstLineRounds: 3}
	// The recipe's own values win over agent.*, and first_line_review: off
	// does not switch off a recipe's first_line step.
	if got := planOf(cheap, false, a, nil, nil, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 1, Precedence: 1, FirstRounds: 2},
	}) {
		t.Fatalf("recipe over agent: %v", got)
	}
	// The task's override wins over both.
	seven := 7
	if got := planOf(cheap, false, a, nil, &seven, rigor.Critical); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 7, Precedence: 7, FirstRounds: 2},
	}) {
		t.Fatalf("override: %v", got)
	}
	solo := parseRecipe(t, "version: 1\nname: claude-solo\nroles:\n  reviewer: coder\nsteps:\n  - review: { max_rounds: 1 }\n")
	if got := planOf(solo, false, a, nil, nil, rigor.Critical); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 1, Precedence: 1}}) {
		t.Fatalf("solo: %v", got)
	}
}

// TestPlanBounceCarriesFirstRounds: a review step with bounce: first_line
// copies the plan's first-line rounds onto itself, and its own Bounce flag,
// so seniorLoop can bounce back without re-deriving anything.
func TestPlanBounceCarriesFirstRounds(t *testing.T) {
	bouncy := parseRecipe(t, "version: 1\nname: bouncy\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { bounce: first_line, max_rounds: 3 }\n")
	a := config.Agent{ReviewRounds: 5, FirstLineRounds: 1}
	got := planOf(bouncy, false, a, nil, nil, rigor.Critical)
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2, Precedence: 2},
		{Kind: recipe.StepReview, Rounds: 3, Precedence: 3, Bounce: true, FirstRounds: 2},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
}

func TestCheckPlan(t *testing.T) {
	ok := []planStep{{Kind: recipe.StepFirstLine, Rounds: 2}, {Kind: recipe.StepReview, Rounds: 1}}
	if err := checkPlan(ok); err != nil {
		t.Fatal(err)
	}
	// Level 3's step ends a plan the same way the senior review does: it is a
	// review round, and the round readiness reads when it ran.
	level3 := []planStep{{Kind: recipe.StepReview, Rounds: 1}, {Kind: recipe.StepAdversarial, Rounds: 2}}
	if err := checkPlan(level3); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]planStep{
		nil,
		{{Kind: recipe.StepFirstLine, Rounds: 2}},
		{{Kind: recipe.StepReview, Rounds: 1}, {Kind: recipe.StepFirstLine, Rounds: 1}},
		{{Kind: recipe.StepReview, Rounds: 0}},
		{{Kind: recipe.StepAdversarial, Rounds: 2}},
		{{Kind: recipe.StepReview, Rounds: 1}, {Kind: recipe.StepAdversarial, Rounds: 0}},
	} {
		if err := checkPlan(bad); err == nil || !strings.Contains(err.Error(), "no trailing review step") {
			t.Errorf("checkPlan(%v) = %v", bad, err)
		}
	}
}
