package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
)

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
	cheap := catalogRecipe(t, "cheap-loop-senior") // first_line 2, review 1
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
	solo := catalogRecipe(t, "claude-solo")
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
