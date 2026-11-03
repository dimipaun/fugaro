package runner

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
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

// levelsOf resolves a run's three review levels the way the run's bootstrap
// does (config.ResolveLevels, L4.2): the mode's own row, the configuration's
// agent.levels, the task's review_levels and the owner's floor, in that
// precedence. Every case below builds its plan from this answer rather than
// from a hand-written Levels, so what it pins is the plan the levels a run
// really has produce.
func levelsOf(t *testing.T, rcp *recipe.Recipe, derived bool, mode rigor.Mode, a config.Agent, providers map[string]config.ModelProvider, floor []policy.Level, task *config.ReviewLevels) config.Levels {
	t.Helper()
	levels, _, err := config.ResolveLevels(config.LevelInputs{
		Config: &config.Config{Agent: a}, Mode: mode, Floor: floor, Task: task,
		Recipe: rcp, CatalogDefault: derived, Providers: providers,
	})
	if err != nil {
		t.Fatalf("resolving the levels of a %s run: %v", mode, err)
	}
	return levels
}

// TestDefaultRecipeRunsThreeLevelsWhenEachIsOn: the catalog `default` is the
// loop a repository gets when nothing names a recipe, and it carries the three
// review levels the design names (docs/recipes.md §2): the first-line loop
// (level 1, the coder's own model, on for a repository a provider serves the
// coder of and does not serve the reviewer of), the senior review (level 2,
// the round readiness reads) and the adversarial pass after it (level 3, on at
// Critical — the levels row this runner reads).
//
// Red before the change: the default's steps were first_line and review, so
// the plan ran level 3 only as the step O7 inserts for a level a recipe has
// none for, never as the step the recipe itself writes — the bounce it runs
// was O7's, not the catalog's.
func TestDefaultRecipeRunsThreeLevelsWhenEachIsOn(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a, providers := providerCoderAgent()
	got := planOf(def, levelsOf(t, def, true, rigor.Critical, a, providers, nil, nil), a, nil, rigor.Critical)
	// Critical's own row supplies every number (rigor.DefaultRounds: 3 inner,
	// 5 senior; rigor.AdversarialDefault is the senior row), so no step carries
	// a precedence and each takes the mode's number as the plan is built. The
	// level 3 step is the recipe's own and bounces to the cheap loop, which is
	// the catalog's ruling (O7): a rejection runs the fix, then another
	// first-line loop, then the next adversarial round.
	want := []planStep{
		{Kind: recipe.StepFirstLine, Rounds: 3},
		{Kind: recipe.StepReview, Rounds: 5, FirstRounds: 3},
		{Kind: recipe.StepAdversarial, Rounds: 5, Bounce: true, FirstRounds: 3},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
}

// TestDefaultRecipeSkipsALevelWhoseConditionIsOff: each level of the derived
// default has its own condition, and the plan carries only the levels it is
// on (L2.1's O7, planRecipe).
func TestDefaultRecipeSkipsALevelWhoseConditionIsOff(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)

	t.Run("level 1: first_line_review off", func(t *testing.T) {
		a, providers := providerCoderAgent()
		a.FirstLineReview = config.FirstLineOff
		got := planOf(def, levelsOf(t, def, true, rigor.Critical, a, providers, nil, nil), a, nil, rigor.Critical)
		want := []recipe.StepKind{recipe.StepReview, recipe.StepAdversarial}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})

	t.Run("level 3: the mode's row leaves it off", func(t *testing.T) {
		a, providers := providerCoderAgent()
		got := planOf(def, levelsOf(t, def, true, rigor.Pro, a, providers, nil, nil), a, nil, rigor.Pro)
		want := []recipe.StepKind{recipe.StepFirstLine, recipe.StepReview}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})

	t.Run("level 2: POC's row leaves it off and the first line decides readiness", func(t *testing.T) {
		a, providers := providerCoderAgent()
		got := planOf(def, levelsOf(t, def, true, rigor.POC, a, providers, nil, nil), a, nil, rigor.POC)
		want := []recipe.StepKind{recipe.StepFirstLine}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})
}

// TestDefaultRecipeUnchangedForAClaudeOnlyRepository: a repository whose coder
// no provider serves gets no inner loop — `auto` leaves the first line off —
// and the loop it does get is the one it had before the step shipped, the
// senior review on the mode's rounds. At a mode whose levels turn level 3 on,
// the same repository gets level 3 and nothing else new: what the default
// gained is the level, never a stage its models cannot run.
func TestDefaultRecipeUnchangedForAClaudeOnlyRepository(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a, providers := claudeOnlyAgent()

	got := planOf(def, levelsOf(t, def, true, rigor.Pro, a, providers, nil, nil), a, nil, rigor.Pro)
	if want := []planStep{{Kind: recipe.StepReview, Rounds: 3}}; !slices.Equal(got, want) {
		t.Fatalf("Pro plan = %+v, want %+v", got, want)
	}
	got = planOf(def, levelsOf(t, def, true, rigor.Critical, a, providers, nil, nil), a, nil, rigor.Critical)
	// The level 3 step is the recipe's own and bounces; with the first line
	// off there is no cheap loop for the bounce to run, so its FirstRounds is
	// 0 and a rejection runs the fix alone.
	if want := []planStep{{Kind: recipe.StepReview, Rounds: 5}, {Kind: recipe.StepAdversarial, Rounds: 5, Bounce: true}}; !slices.Equal(got, want) {
		t.Fatalf("Critical plan = %+v, want %+v", got, want)
	}
}

// TestDefaultRecipeHonoursTheTaskAndTheFloor: the levels a run resolves are
// not the mode's row alone — the task's review_levels sits above it and the
// owner's floor above both (config.ResolveLevels, L3.1) — and the derived
// default's level 3 follows the whole answer rather than the row underneath
// it. A task that turns level 3 off at Critical drops the step; the owner's
// floor, which is a requirement and not a veto, puts it back at Pro, where the
// mode's own row leaves it off.
func TestDefaultRecipeHonoursTheTaskAndTheFloor(t *testing.T) {
	def := catalogRecipe(t, recipe.DefaultName)
	a, providers := providerCoderAgent()

	t.Run("the task's l3: none at Critical", func(t *testing.T) {
		task := &config.ReviewLevels{L3: &config.LevelSetting{Off: true}}
		got := planOf(def, levelsOf(t, def, true, rigor.Critical, a, providers, nil, task), a, nil, rigor.Critical)
		want := []recipe.StepKind{recipe.StepFirstLine, recipe.StepReview}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
	})

	t.Run("the owner's floor requiring L3 at Pro", func(t *testing.T) {
		floor := []policy.Level{policy.LevelL3}
		got := planOf(def, levelsOf(t, def, true, rigor.Pro, a, providers, floor, nil), a, nil, rigor.Pro)
		want := []recipe.StepKind{recipe.StepFirstLine, recipe.StepReview, recipe.StepAdversarial}
		if kinds := planKinds(got); !slices.Equal(kinds, want) {
			t.Fatalf("plan = %v, want %v", kinds, want)
		}
		// The floor's level runs the mode's own adversarial number, not
		// Critical's: the row the plan reads is the mode the run reached.
		if n := got[len(got)-1].Rounds; n != rigor.AdversarialDefault(rigor.Pro) {
			t.Errorf("level 3 rounds = %d, want %d", n, rigor.AdversarialDefault(rigor.Pro))
		}
	})

	// The floor refuses a level turned off under it rather than quietly
	// keeping it on (G1), so there is no plan to build: the resolution is the
	// task's to refuse, at the launcher and again here.
	t.Run("the owner's floor refuses l3: none", func(t *testing.T) {
		task := &config.ReviewLevels{L3: &config.LevelSetting{Off: true}}
		_, _, err := config.ResolveLevels(config.LevelInputs{
			Config: &config.Config{Agent: a}, Mode: rigor.Critical, Floor: []policy.Level{policy.LevelL3}, Task: task,
			Recipe: def, CatalogDefault: true, Providers: providers,
		})
		if err == nil {
			t.Fatal("a task turning off a level the owner's floor requires resolved without a refusal")
		}
	})
}

// TestPlanAdversarialRoundsComeFromOneKnob: level 3's rounds have one knob and
// two defaults under it — agent.adversarial_rounds (L1.3), else the step's own
// max_rounds, else the mode's own row (rigor.AdversarialDefault) — and never
// the senior review's number: a mode's row is two numbers and only one of them
// is the senior review's (the owner's ruling of 2026-10-31, which answers O3).
func TestPlanAdversarialRoundsComeFromOneKnob(t *testing.T) {
	a, providers := providerCoderAgent()
	a.AdversarialRounds = 4

	t.Run("the knob, where nothing else sets a number", func(t *testing.T) {
		def := catalogRecipe(t, recipe.DefaultName)
		got := planOf(def, levelsOf(t, def, true, rigor.Critical, a, providers, nil, nil), a, nil, rigor.Critical)
		last := got[len(got)-1]
		if last.Kind != recipe.StepAdversarial || last.Rounds != 4 || last.Precedence != 4 {
			t.Fatalf("level 3 = %+v, want the knob's 4 rounds", last)
		}
	})

	t.Run("the step's own max_rounds wins over the knob", func(t *testing.T) {
		written := parseRecipe(t, "version: 1\nname: written\nsteps:\n  - review: {}\n  - adversarial: { max_rounds: 2 }\n")
		got := planOf(written, levelsOf(t, written, false, rigor.Critical, a, providers, nil, nil), a, nil, rigor.Critical)
		if last := got[len(got)-1]; last.Rounds != 2 || last.Precedence != 2 {
			t.Fatalf("level 3 = %+v, want the step's own 2 rounds", last)
		}
	})

	t.Run("the mode's row, where neither is set", func(t *testing.T) {
		def := catalogRecipe(t, recipe.DefaultName)
		bare, bareProviders := providerCoderAgent()
		got := planOf(def, levelsOf(t, def, true, rigor.Critical, bare, bareProviders, nil, nil), bare, nil, rigor.Critical)
		if last := got[len(got)-1]; last.Rounds != rigor.AdversarialDefault(rigor.Critical) || last.Precedence != 0 {
			t.Fatalf("level 3 = %+v, want the mode's own row", last)
		}
	})
}
