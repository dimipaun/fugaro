package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// intp is a task override's review_rounds, which is a pointer because a task
// that sets none must be told apart from one that sets zero.
func intp(n int) *int { return &n }

// TestPlanRoundCeilingPrecedence: the mode's ceiling sits *under* F6's
// precedence rather than beside it (T3.2, D7b). The precedence is asserted
// through planOf's own answer — the task's override, else the repository's
// agent.review_rounds, else the recipe's max_rounds — and every mode's ceiling
// only lowers it: a cheaper mode spends fewer rounds than the precedence agreed
// on, a mode whose ceiling does not bite leaves the precedence's answer alone,
// and a repository that configured no rigor keeps today's rounds whatever mode
// it resolved (D19's ruling).
func TestPlanRoundCeilingPrecedence(t *testing.T) {
	// A review step pinning six rounds, so the recipe outranks agent.* and the
	// precedence is asserted rather than restated.
	six := parseRecipe(t, "version: 1\nname: six\nsteps:\n  - review: { max_rounds: 6 }\n")
	a := config.Agent{ReviewRounds: 5}
	for _, tc := range []struct {
		name       string
		override   *int
		mode       rigor.Mode
		configured bool
		wantRounds int
		wantPrec   int
	}{
		{"the recipe outranks agent.*", nil, rigor.Critical, true, 6, 6},
		{"a cheaper mode spends fewer", nil, rigor.Pro, true, 3, 6},
		{"the override outranks the recipe", intp(7), rigor.Critical, true, 7, 7},
		{"the ceiling sits under the override", intp(7), rigor.POC, true, 2, 7},
		{"hotfix's one round is clamped to two", nil, rigor.Hotfix, true, 2, 6},
		{"critical does not lower", intp(2), rigor.Critical, true, 2, 2},
		{"an unconfigured repository keeps its rounds", nil, rigor.Pro, false, 6, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planOf(six, false, a, nil, tc.override, tc.mode, tc.configured)
			want := []planStep{{Kind: recipe.StepReview, Rounds: tc.wantRounds, Precedence: tc.wantPrec}}
			if !slices.Equal(got, want) {
				t.Fatalf("plan = %+v, want %+v", got, want)
			}
		})
	}
	// The first-line step keeps its own bound: D7 names no first-line ceiling,
	// so the mode touches the review step's rounds and nothing else.
	first := parseRecipe(t, "version: 1\nname: first\nsteps:\n  - first_line: { max_rounds: 3 }\n  - review: { max_rounds: 6 }\n")
	got := planOf(first, false, a, nil, nil, rigor.POC, true)
	if got[0].Rounds != 3 || got[0].Precedence != 3 {
		t.Fatalf("the first-line step = %+v; want its own three rounds, whatever the mode", got[0])
	}
}

// TestPlanRoundCeilingIsNeverAboveTheRecipeBound: no mode's ceiling reaches
// above MaxReviewRounds, which is the most a review step may carry at all
// (T3.2). The ceiling is a lower bound only — a mode cannot buy a run more
// rounds than the recipe itself allows.
func TestPlanRoundCeilingIsNeverAboveTheRecipeBound(t *testing.T) {
	for _, m := range rigor.Modes {
		if got := reviewRoundCeiling(m, true); got > recipe.MaxReviewRounds {
			t.Errorf("reviewRoundCeiling(%s) = %d; want at most %d", m, got, recipe.MaxReviewRounds)
		}
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

func TestPlanDefaultFollowsAgent(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a := config.Agent{ReviewRounds: 4, FirstLineReview: config.FirstLineOff, FirstLineRounds: 2}
	if got := planOf(def, true, a, nil, nil); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 4}}) {
		t.Fatalf("off: %v", got)
	}
	a.FirstLineReview = config.FirstLineOn
	if got := planOf(def, true, a, nil, nil); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 4, FirstRounds: 2},
	}) {
		t.Fatalf("on: %v", got)
	}
	// Not derived (a project or repository recipe named default): its
	// first_line step always runs.
	a.FirstLineReview = config.FirstLineOff
	if got := planOf(def, false, a, nil, nil); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 4, FirstRounds: 2},
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
	if got := planOf(cheap, false, a, nil, nil); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 1, FirstRounds: 2},
	}) {
		t.Fatalf("recipe over agent: %v", got)
	}
	// The task's override wins over both.
	seven := 7
	if got := planOf(cheap, false, a, nil, &seven); !slices.Equal(got, []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 7, FirstRounds: 2},
	}) {
		t.Fatalf("override: %v", got)
	}
	solo := parseRecipe(t, "version: 1\nname: claude-solo\nroles:\n  reviewer: coder\nsteps:\n  - review: { max_rounds: 1 }\n")
	if got := planOf(solo, false, a, nil, nil); !slices.Equal(got, []planStep{{Kind: recipe.StepReview, Rounds: 1}}) {
		t.Fatalf("solo: %v", got)
	}
}

// TestPlanBounceCarriesFirstRounds: a review step with bounce: first_line
// copies the plan's first-line rounds onto itself, and its own Bounce flag,
// so seniorLoop can bounce back without re-deriving anything.
func TestPlanBounceCarriesFirstRounds(t *testing.T) {
	bouncy := parseRecipe(t, "version: 1\nname: bouncy\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { bounce: first_line, max_rounds: 3 }\n")
	a := config.Agent{ReviewRounds: 5, FirstLineRounds: 1}
	got := planOf(bouncy, false, a, nil, nil)
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 2},
		{Kind: recipe.StepReview, Rounds: 3, Bounce: true, FirstRounds: 2},
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
	for _, bad := range [][]planStep{
		nil,
		{{Kind: recipe.StepFirstLine, Rounds: 2}},
		{{Kind: recipe.StepReview, Rounds: 1}, {Kind: recipe.StepFirstLine, Rounds: 1}},
		{{Kind: recipe.StepReview, Rounds: 0}},
	} {
		if err := checkPlan(bad); err == nil || !strings.Contains(err.Error(), "no trailing review step") {
			t.Errorf("checkPlan(%v) = %v", bad, err)
		}
	}
}
