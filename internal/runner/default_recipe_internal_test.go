package runner

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// deepseekRoute is a job's providers block claiming the deepseek family and
// nothing else: the route that decides whether the coder's model is served by
// a provider (Deps.Providers).
func deepseekRoute() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api/v1",
		Auth: "bearer", Secret: "route-key", Models: []string{"deepseek/*"},
	}}
}

// providerCoderAgent is the repository shape the derived first line is on for:
// a provider serves the coder's model and none serves the reviewer's, which is
// what config.Agent.FirstLineOn's `auto` reads.
func providerCoderAgent() (config.Agent, map[string]config.ModelProvider) {
	a := config.Agent{Models: config.ModelRoles{Coder: "deepseek/deepseek-v4.1-flash", Reviewer: "claude-opus-5"}}
	return a, deepseekRoute()
}

// claudeOnlyAgent is a repository whose coder and reviewer are both Claude
// pins: no route claims either, so the derived first line is off.
func claudeOnlyAgent() (config.Agent, map[string]config.ModelProvider) {
	a := config.Agent{Models: config.ModelRoles{Coder: "claude-sonnet-5", Reviewer: "claude-opus-5"}}
	return a, deepseekRoute()
}

// planKinds is the plan's step kinds, in the order the loop would run them.
func planKinds(plan []planStep) []recipe.StepKind {
	out := make([]recipe.StepKind, 0, len(plan))
	for _, st := range plan {
		out = append(out, st.Kind)
	}
	return out
}

// TestDefaultRecipeRunsThreeLevelsWhenEachIsOn: the catalog `default` is the
// loop a repository gets when nothing names a recipe, and it now carries the
// three review levels the design names (docs/recipes.md §2): the first-line
// loop (level 1, the coder's own model, on for a repository a provider serves
// the coder of and does not serve the reviewer of), the senior review (level
// 2, the round readiness reads) and the adversarial pass after it (level 3, on
// at Critical — the levels row this runner reads).
//
// Red before the change: the default's steps were first_line and review, so
// the plan ended at the senior review and level 3 was never planned, whatever
// the mode.
func TestDefaultRecipeRunsThreeLevelsWhenEachIsOn(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a, providers := providerCoderAgent()
	got := planOf(def, true, a, providers, nil, rigor.Critical)
	// Critical's own row supplies every number (rigor.DefaultRounds: 3 inner,
	// 5 senior; rigor.AdversarialDefault is the senior row), so no step carries
	// a precedence and each takes the mode's number as the plan is built.
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 3},
		{Kind: recipe.StepReview, Rounds: 5, FirstRounds: 3},
		{Kind: recipe.StepAdversarial, Rounds: 5, FirstRounds: 3},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
}

// TestDefaultRecipeSkipsALevelWhoseConditionIsOff: each level of the derived
// default has its own condition, and the plan carries only the levels it is
// on. The senior review's case is the one that is not a skip: level 2 is the
// round readiness reads, and this runner refuses a plan without one
// (checkPlan), so the derived default keeps it at every mode — including POC,
// whose own levels row turns it off. Acting on that row (level 1 deciding
// readiness instead) is the level resolver's and the runner's L3.1/L4.2
// (docs/review-levels-ab-jobmax/implementation-plan.md), not the recipe's.
func TestDefaultRecipeSkipsALevelWhoseConditionIsOff(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)

	t.Run("level 1: first_line_review off", func(t *testing.T) {
		a, providers := providerCoderAgent()
		a.FirstLineReview = config.FirstLineOff
		got := planOf(def, true, a, providers, nil, rigor.Critical)
		want := []recipe.StepKind{recipe.StepReview, recipe.StepAdversarial}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})

	t.Run("level 2: no mode leaves the plan without a senior review", func(t *testing.T) {
		a, providers := providerCoderAgent()
		got := planOf(def, true, a, providers, nil, rigor.POC)
		want := []recipe.StepKind{recipe.StepFirstLine, recipe.StepReview}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})

	t.Run("level 3: the mode's row leaves it off", func(t *testing.T) {
		a, providers := providerCoderAgent()
		got := planOf(def, true, a, providers, nil, rigor.Pro)
		want := []recipe.StepKind{recipe.StepFirstLine, recipe.StepReview}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})
}

// TestDefaultRecipeUnchangedForAClaudeOnlyRepository: a repository whose coder
// no provider serves gets no inner loop — `auto` leaves the first line off —
// and the loop it does get is the one it had before 0.15.0, the senior review
// on the mode's rounds. At a mode whose levels turn level 3 on, the same
// repository gets level 3 and nothing else new: what the default gained is the
// level, never a stage its models cannot run.
func TestDefaultRecipeUnchangedForAClaudeOnlyRepository(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a, providers := claudeOnlyAgent()

	got := planOf(def, true, a, providers, nil, rigor.Pro)
	if want := []planStep{{Kind: recipe.StepReview, Rounds: 3}}; !slices.Equal(got, want) {
		t.Fatalf("Pro plan = %+v, want %+v", got, want)
	}
	got = planOf(def, true, a, providers, nil, rigor.Critical)
	if want := []planStep{{Kind: recipe.StepReview, Rounds: 5}, {Kind: recipe.StepAdversarial, Rounds: 5}}; !slices.Equal(got, want) {
		t.Fatalf("Critical plan = %+v, want %+v", got, want)
	}
}
