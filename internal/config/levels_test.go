package config

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// levelRecipe is a catalog recipe, parsed as the launcher and the runner
// parse it, so the resolver's tests read the real steps of the real recipes
// rather than a recipe written to agree with them.
func levelRecipe(t *testing.T, name string) *recipe.Recipe {
	t.Helper()
	text, ok := recipe.CatalogText(name)
	if !ok {
		t.Fatalf("catalog recipe %s: not found", name)
	}
	r, ps := recipe.Parse(text)
	if len(ps) > 0 {
		t.Fatalf("catalog recipe %s: %s", name, recipe.ProblemsText(ps))
	}
	return r
}

// levelAgent is the agent every fixture below starts from: two model IDs
// that differ, and level 1 left to agent.first_line_review.
func levelAgent() Agent {
	return Agent{Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"}}
}

func mustWarn(t *testing.T, warns []Warning, level policy.Level, substr string) {
	t.Helper()
	for _, w := range warns {
		if w.Level == level && strings.Contains(w.Message, substr) {
			return
		}
	}
	t.Errorf("warnings = %v; want one about %s holding %q", warns, level, substr)
}

// TestResolveLevelsPrecedence pins the order the resolver reads: the rigor
// mode's own defaults first, then the merged configuration — which is the
// project layer over the repository, and the two are one node here because
// config.Resolve has already merged them key by key (L1.3) — and the task's
// own block last. The owner's floor is applied after all three, and
// TestResolveLevelsFloorRefusesNone is where that is pinned.
func TestResolveLevelsPrecedence(t *testing.T) {
	recipe := levelRecipe(t, "default")

	// Nothing is written anywhere: the mode's own row answers. Pro runs the
	// senior review on the reviewer's model, with no effort and no focus of
	// its own, and runs neither of the other two.
	got, warns, err := ResolveLevels(LevelInputs{
		Config: &Config{Agent: levelAgent()}, Mode: rigor.Pro, Recipe: recipe, CatalogDefault: true,
	})
	if err != nil {
		t.Fatalf("ResolveLevels with nothing set: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings with nothing set = %v; want none", warns)
	}
	if !got.L2.On || got.L2.Model != "claude-opus-5-5" || got.L2.Effort != "" || got.L2.Focus != "" {
		t.Errorf("l2 with nothing set = %+v; want it on, on the reviewer's model, with no effort and no focus", got.L2)
	}
	if got.L1.On || got.L3.On {
		t.Errorf("l1 = %+v, l3 = %+v with nothing set; want both off at pro", got.L1, got.L3)
	}

	// The merged configuration names a model, an effort and a focus for
	// level 2: all three beat the mode's own row.
	agent := levelAgent()
	agent.Levels = ReviewLevels{L2: &LevelSetting{Model: "claude-opus-5-5", Effort: "high", Focus: "watch the migration"}}
	got, _, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro, Recipe: recipe, CatalogDefault: true,
	})
	if err != nil {
		t.Fatalf("ResolveLevels with a level block: %v", err)
	}
	if got.L2.Model != "claude-opus-5-5" || got.L2.Effort != "high" || got.L2.Focus != "watch the migration" {
		t.Errorf("l2 = %+v; want the configuration's model, effort and focus", got.L2)
	}

	// The task restates two of the three and names no model: its two win,
	// and the model stays the configuration's, which is the key-by-key rule
	// every other block follows too.
	got, _, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro, Recipe: recipe, CatalogDefault: true,
		Task: &ReviewLevels{L2: &LevelSetting{Effort: "max", Focus: "check auth boundaries"}},
	})
	if err != nil {
		t.Fatalf("ResolveLevels with a task block: %v", err)
	}
	if got.L2.Model != "claude-opus-5-5" {
		t.Errorf("l2.Model = %q; want the configuration's claude-opus-5-5: the task names no model", got.L2.Model)
	}
	if got.L2.Effort != "max" || got.L2.Focus != "check auth boundaries" {
		t.Errorf("l2 = %+v; want the task's effort and focus", got.L2)
	}

	// A layer's `none` is the level's switch, and it removes the level the
	// layers below it set.
	agent.Levels.L3 = &LevelSetting{Off: true}
	got, _, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Critical, Recipe: recipe, CatalogDefault: true,
	})
	if err != nil {
		t.Fatalf("ResolveLevels with l3 none: %v", err)
	}
	if got.L3.On {
		t.Errorf("l3 = %+v; want it off: a layer wrote none, and no floor requires it", got.L3)
	}
	if !got.L2.On {
		t.Errorf("l2 = %+v; want it on: `none` on l3 says nothing about l2", got.L2)
	}
}

// TestResolveLevelsFloorRefusesNone is G1: no layer below the owner's floor
// can turn a level off. A layer that writes `none` for a level the floor
// requires is refused rather than quietly overruled — the task is written by
// the launcher, so ignoring its `none` would be the launcher deciding how
// much review it gets — and a level the mode's own row leaves off is turned
// on, because the floor is a requirement and the mode's row is not a layer
// saying no.
func TestResolveLevelsFloorRefusesNone(t *testing.T) {
	inputs := func(levels ReviewLevels, task *ReviewLevels) LevelInputs {
		return LevelInputs{
			Config: &Config{Agent: Agent{Models: levelAgent().Models, Levels: levels}},
			Mode:   rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
			Floor: []policy.Level{policy.LevelL2},
			Task:  task,
		}
	}

	_, _, err := ResolveLevels(inputs(ReviewLevels{L2: &LevelSetting{Off: true}}, nil))
	if err == nil {
		t.Fatal("the repository turned l2 off under the owner's floor and ResolveLevels refused nothing")
	}
	for _, want := range []string{"l2", "pro", "floor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	_, _, err = ResolveLevels(inputs(ReviewLevels{}, &ReviewLevels{L2: &LevelSetting{Off: true}}))
	if err == nil {
		t.Fatal("the task turned l2 off under the owner's floor and ResolveLevels refused nothing")
	}

	// No floor, the same `none` is honoured: the refusal above is the
	// floor's doing and not the level's own key being refused outright.
	in := inputs(ReviewLevels{L2: &LevelSetting{Off: true}}, nil)
	in.Floor = nil
	got, _, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with l2 off and no floor: %v", err)
	}
	if got.L2.On {
		t.Errorf("l2 = %+v; want it off: no floor requires it", got.L2)
	}

	// The floor turns on a level the mode's own row leaves off: POC's level
	// 2 is off, and an owner who requires it gets it.
	in = inputs(ReviewLevels{}, nil)
	in.Mode = rigor.POC
	got, _, err = ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels at poc under a floor on l2: %v", err)
	}
	if !got.L2.On {
		t.Errorf("l2 = %+v at poc under a floor on l2; want it on: the floor is applied last", got.L2)
	}
}

// TestResolveLevelsRefusesSameModel is D721: a run never launches with level
// 1 and level 2 on the same exact model ID, and the refusal names both
// levels and the model they share.
func TestResolveLevelsRefusesSameModel(t *testing.T) {
	same := LevelInputs{
		Config: &Config{Agent: Agent{
			Models:          ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "deepseek/deepseek-v4-flash"},
			FirstLineReview: FirstLineOn,
		}},
		Mode:   rigor.Pro,
		Recipe: levelRecipe(t, "standard"),
	}
	_, _, err := ResolveLevels(same)
	if err == nil {
		t.Fatal("a run with l1 and l2 both on deepseek/deepseek-v4-flash launched; want D721's refusal")
	}
	for _, want := range []string{"l1", "l2", "deepseek/deepseek-v4-flash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	// A reviewer on another model is the ordinary case and is not refused.
	other := same
	other.Config = &Config{Agent: Agent{
		Models:          ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "openai/gpt-4o"},
		FirstLineReview: FirstLineOn,
	}}
	if _, _, err := ResolveLevels(other); err != nil {
		t.Errorf("ResolveLevels with l1 and l2 on different models: %v; want no refusal", err)
	}

	// One of the two off is not a refusal either: a level that does not run
	// reviews nothing, and the comparison is between two levels that run.
	// run-digest has no first_line step, so first_line_review decides level
	// 1 there and can turn it off.
	off := same
	off.Config = &Config{Agent: Agent{
		Models:          same.Config.Agent.Models,
		FirstLineReview: FirstLineOff,
	}}
	off.Recipe = levelRecipe(t, "run-digest")
	got, _, err := ResolveLevels(off)
	if err != nil {
		t.Fatalf("ResolveLevels with l1 off: %v", err)
	}
	if got.L1.On {
		t.Errorf("l1 = %+v; want it off: first_line_review is off", got.L1)
	}

	// An installation that names no model at all is the ordinary one, and
	// standard's first_line step runs level 1 whatever first_line_review
	// says: both levels then run the same model — Claude Code's own default —
	// which is not an ID the resolver can compare, and refusing it would
	// refuse every such installation a recipe with a first_line step. The
	// owner ruled on 2026-11-01 that this warns instead, once, and the run
	// goes on.
	unset := LevelInputs{
		Config: &Config{Agent: Agent{FirstLineReview: FirstLineOn}},
		Mode:   rigor.Pro,
		Recipe: levelRecipe(t, "standard"),
	}
	got, warns, err := ResolveLevels(unset)
	if err != nil {
		t.Fatalf("ResolveLevels with no model named anywhere: %v; want a warning and no refusal (owner's ruling, 2026-11-01)", err)
	}
	if !got.L1.On || !got.L2.On || got.L1.Model != "" || got.L2.Model != "" {
		t.Errorf("l1 = %+v, l2 = %+v; want both on with no model of their own", got.L1, got.L2)
	}
	mustWarn(t, warns, policy.LevelL2, "not independent")
	if len(warns) != 1 {
		t.Errorf("warnings = %v; want exactly one: the pair is warned about once", warns)
	}
}

// TestResolveLevelsInsertsMissingStep is O7 as ruled: the rigor mode wins, and
// an enabled level whose step the recipe lacks is inserted in its fixed place
// — level 1 before the review step, level 3 after it with bounce: first_line,
// and both take their knobs from agent.*. The resolver only says so; the
// runner that holds the recipe inserts it (L4.2).
func TestResolveLevelsInsertsMissingStep(t *testing.T) {
	// run-digest carries a digest and a review step and no first_line, and
	// it maps no roles, so a first-line review the repository turns on is an
	// insertion rather than a collision with level 2.
	agent := Agent{
		Models:          ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "openai/gpt-4o"},
		FirstLineReview: FirstLineOn,
	}
	got, _, err := ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro, Recipe: levelRecipe(t, "run-digest"),
	})
	if err != nil {
		t.Fatalf("ResolveLevels on run-digest: %v", err)
	}
	if !got.L1.On || !got.L1.Inserted {
		t.Errorf("l1 = %+v; want it on and inserted: run-digest has no first_line step (O7)", got.L1)
	}
	if got.L2.Inserted {
		t.Errorf("l2 = %+v; want Inserted false: level 2's step is the review step every loop recipe carries", got.L2)
	}
	if got.L3.On || got.L3.Inserted {
		t.Errorf("l3 = %+v at pro; want it off and not inserted", got.L3)
	}

	// Critical turns level 3 on, and standard has no adversarial step.
	got, _, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Critical, Recipe: levelRecipe(t, "standard"),
	})
	if err != nil {
		t.Fatalf("ResolveLevels on standard at critical: %v", err)
	}
	if !got.L3.On || !got.L3.Inserted {
		t.Errorf("l3 = %+v; want it on and inserted: standard has no adversarial step (O7)", got.L3)
	}
	if !got.L1.On || got.L1.Inserted {
		t.Errorf("l1 = %+v; want it on and not inserted: standard's first_line step always runs", got.L1)
	}

	// The catalog's own default already carries the step first_line_review
	// turns on, so there is nothing to insert.
	got, _, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
	})
	if err != nil {
		t.Fatalf("ResolveLevels on the catalog default: %v", err)
	}
	if !got.L1.On || got.L1.Inserted {
		t.Errorf("l1 = %+v; want it on and not inserted: the catalog default carries the step", got.L1)
	}
}

// TestResolveLevelsWarnsCoderAndFamily pins the two warnings about level 2's
// model: it is the coder's own, or it shares level 1's vendor family. Neither
// is a refusal — the run goes on — and ModelFamily is what the second one
// reads.
func TestResolveLevelsWarnsCoderAndFamily(t *testing.T) {
	// Level 1 is off, so the coder's model at level 2 is a warning rather
	// than D721's refusal.
	_, warns, err := ResolveLevels(LevelInputs{
		Config: &Config{Agent: Agent{
			Models:          ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "deepseek/deepseek-v4-flash"},
			FirstLineReview: FirstLineOff,
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
	})
	if err != nil {
		t.Fatalf("ResolveLevels with the coder's model at l2: %v", err)
	}
	mustWarn(t, warns, policy.LevelL2, "coder's own model")

	// Two different Claude IDs are one family, so level 2 reviews a model
	// whose blind spots level 1's model shares.
	_, warns, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: Agent{
			Models:          ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
			FirstLineReview: FirstLineOn,
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "standard"),
	})
	if err != nil {
		t.Fatalf("ResolveLevels with two Claude models: %v", err)
	}
	mustWarn(t, warns, policy.LevelL2, "anthropic")

	// Two vendors share no family, and nothing is warned.
	_, warns, err = ResolveLevels(LevelInputs{
		Config: &Config{Agent: Agent{
			Models:          ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "openai/gpt-4o"},
			FirstLineReview: FirstLineOn,
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "standard"),
	})
	if err != nil {
		t.Fatalf("ResolveLevels with two vendors: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; want none: two vendors share no family", warns)
	}

	for model, want := range map[string]string{
		"claude-sonnet-5-5":           "anthropic",
		"claude-haiku-4-5@20251001":   "anthropic",
		"anthropic/claude-sonnet-5.5": "anthropic",
		"deepseek/deepseek-v4-flash":  "deepseek",
		"sonnet":                      "",
		"":                            "",
	} {
		if got := ModelFamily(model); got != want {
			t.Errorf("ModelFamily(%q) = %q; want %q", model, got, want)
		}
	}
}

// TestResolveLevelsRefusesProviderEffort is D725: an effort set for a model a
// provider serves is refused, because those stages run with thinking off
// (MAX_THINKING_TOKENS=0) and the setting would look applied and do nothing.
func TestResolveLevelsRefusesProviderEffort(t *testing.T) {
	providers := map[string]ModelProvider{"provider-a": {Models: []string{"deepseek/*"}}}
	agent := Agent{
		Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "deepseek/deepseek-v4-flash"},
		Levels: ReviewLevels{L2: &LevelSetting{Effort: "high"}},
	}
	in := LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro,
		Recipe: levelRecipe(t, "default"), CatalogDefault: true,
		Providers: providers,
	}
	_, _, err := ResolveLevels(in)
	if err == nil {
		t.Fatal("l2 ran with an effort on a provider-served model; want D725's refusal")
	}
	for _, want := range []string{"l2.effort", "high", "provider-a", "deepseek/deepseek-v4-flash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	// The same effort on a model no provider claims is applied, not refused.
	in.Config = &Config{Agent: Agent{
		Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
		Levels: ReviewLevels{L2: &LevelSetting{Effort: "high"}},
	}}
	got, warns, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with an effort on a Claude model: %v", err)
	}
	if got.L2.Effort != "high" {
		t.Errorf("l2.Effort = %q; want the written high", got.L2.Effort)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; want none: claude-opus-5-5 sends an effort", warns)
	}
}

// TestResolveLevelsRefusesModelOutsideAllowList: a level may name a model
// only if the run's effective allowed_models admits it, and nil is no list at
// all — which allows every model — while an empty list allows none.
func TestResolveLevelsRefusesModelOutsideAllowList(t *testing.T) {
	agent := Agent{
		Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
		Levels: ReviewLevels{L2: &LevelSetting{Model: "claude-opus-5-5"}},
	}
	in := LevelInputs{
		Config: &Config{Agent: agent}, Mode: rigor.Pro,
		Recipe: levelRecipe(t, "default"), CatalogDefault: true,
		AllowedModels: []string{"claude-sonnet-5-5"},
	}
	_, _, err := ResolveLevels(in)
	if err == nil {
		t.Fatal("l2 named a model outside allowed_models and ResolveLevels refused nothing")
	}
	for _, want := range []string{"l2.model", "claude-opus-5-5", "allowed_models"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	// The model on the list is admitted.
	in.Config.Agent.Levels.L2.Model = "claude-sonnet-5-5"
	if _, _, err := ResolveLevels(in); err != nil {
		t.Errorf("ResolveLevels with l2 on an allowed model: %v", err)
	}

	// nil is no list: every model is allowed.
	in.Config.Agent.Levels.L2.Model = "claude-opus-5-5"
	in.AllowedModels = nil
	if _, _, err := ResolveLevels(in); err != nil {
		t.Errorf("ResolveLevels with no allow-list: %v", err)
	}

	// An empty non-nil list allows none, exactly as
	// policy.Effective.HasAllowList reads it.
	in.AllowedModels = []string{}
	_, _, err = ResolveLevels(in)
	if err == nil {
		t.Fatal("an empty allowed_models admitted a model")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the refusal %q does not say the list is empty", err)
	}
}

// TestResolveLevelsSoloUnaffected: a configuration whose reviewer is the
// coder's model resolves at every mode on the recipes that map the reviewer
// to the coder (solo, lint-fix) and on the catalog's own default, which
// follows agent.first_line_review. None of them reaches D721, because none of
// them runs level 1 and level 2 together — the refusal needs the step, and
// these recipes either have none or run it only when nothing turns it on.
func TestResolveLevelsSoloUnaffected(t *testing.T) {
	for _, tc := range []struct {
		what           string
		recipe         string
		catalogDefault bool
	}{
		{"solo", "solo", false},
		{"lint-fix", "lint-fix", false},
		{"the catalog default with the coder as the reviewer", "default", true},
	} {
		for _, mode := range rigor.Modes {
			got, _, err := ResolveLevels(LevelInputs{
				Config: &Config{Agent: Agent{Models: ModelRoles{
					Coder: "deepseek/deepseek-v4-flash", Reviewer: "deepseek/deepseek-v4-flash",
				}}},
				Mode: mode, Recipe: levelRecipe(t, tc.recipe), CatalogDefault: tc.catalogDefault,
			})
			if err != nil {
				t.Errorf("%s at %s: %v; want no refusal (the recipe maps the reviewer to the coder, or follows first_line_review)", tc.what, mode, err)
				continue
			}
			if got.L1.On && got.L2.On {
				t.Errorf("%s at %s resolved l1 and l2 both on: %+v, %+v", tc.what, mode, got.L1, got.L2)
			}
		}
	}
}

// TestResolveLevelsRefusesFirstLineRecipeWithCoderEqualsReviewer is O11: a
// recipe carrying first_line: {} always runs level 1 (docs/recipes.md §2), so
// a configuration whose reviewer is the coder's own model has level 1 and
// level 2 on one exact model ID there, and D721 refuses it. The catalog's own
// default is the one recipe that follows agent.first_line_review, so the same
// configuration is fine on it.
func TestResolveLevelsRefusesFirstLineRecipeWithCoderEqualsReviewer(t *testing.T) {
	for _, name := range []string{"standard", "test-and-fix"} {
		_, _, err := ResolveLevels(LevelInputs{
			Config: &Config{Agent: Agent{Models: ModelRoles{
				Coder: "deepseek/deepseek-v4-flash", Reviewer: "deepseek/deepseek-v4-flash",
			}}},
			Mode: rigor.Pro, Recipe: levelRecipe(t, name),
		})
		if err == nil {
			t.Errorf("%s: a coder = reviewer configuration resolved with no refusal; want D721's", name)
			continue
		}
		for _, want := range []string{"l1", "l2"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal %q does not name %q", name, err, want)
			}
		}
	}

	if _, _, err := ResolveLevels(LevelInputs{
		Config: &Config{Agent: Agent{Models: ModelRoles{
			Coder: "deepseek/deepseek-v4-flash", Reviewer: "deepseek/deepseek-v4-flash",
		}}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
	}); err != nil {
		t.Errorf("the catalog default: %v; want no refusal: it follows first_line_review (O11)", err)
	}
}

// TestResolveLevelsReviewOnlyHasLevel2Alone: a mode: review recipe allows
// exactly one review step and nothing else, so a report run has level 2 alone
// at every rigor mode. A level something else asks for there cannot be
// dropped — the mode's own row is about the loop, which a report run has
// none of, but a layer's block and the owner's floor are read, and a level
// that cannot run is refused rather than silently left out (G1).
func TestResolveLevelsReviewOnlyHasLevel2Alone(t *testing.T) {
	in := LevelInputs{
		Config: &Config{Agent: levelAgent()}, Mode: rigor.Critical,
		Recipe: levelRecipe(t, "review-only"),
	}
	got, _, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels on review-only at critical: %v", err)
	}
	if !got.L2.On {
		t.Errorf("l2 = %+v; want it on: the recipe's one review step is level 2", got.L2)
	}
	if got.L1.On || got.L3.On {
		t.Errorf("l1 = %+v, l3 = %+v; want both off: the recipe allows one review step and nothing else", got.L1, got.L3)
	}

	// A layer that turns level 3 on is refused: the step cannot exist there.
	in.Config = &Config{Agent: Agent{Models: levelAgent().Models, Levels: ReviewLevels{L3: &LevelSetting{Focus: "auth boundaries"}}}}
	if _, _, err := ResolveLevels(in); err == nil {
		t.Error("review-only ran with a layer's l3 on and ResolveLevels refused nothing")
	}

	// An owner's floor that requires level 1 is refused too, whether or not
	// a layer says anything about it.
	in.Config = &Config{Agent: levelAgent()}
	in.Floor = []policy.Level{policy.LevelL1}
	_, _, err = ResolveLevels(in)
	if err == nil {
		t.Fatal("review-only ran under a floor requiring l1 and ResolveLevels refused nothing")
	}
	for _, want := range []string{"l1", "floor", "review-only"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	// A layer turning one of them off asks for nothing and is not refused.
	in.Config = &Config{Agent: Agent{Models: levelAgent().Models, Levels: ReviewLevels{L3: &LevelSetting{Off: true}}}}
	in.Floor = nil
	if _, _, err := ResolveLevels(in); err != nil {
		t.Errorf("review-only with l3 written none: %v; want no refusal", err)
	}
}

// TestResolveLevelsWarnsEffortIgnoredModel: claude-haiku-4-5 sends no effort
// by any mechanism, so an effort written for it is a warning and not a
// refusal — the run goes on, and the level's setting does nothing.
func TestResolveLevelsWarnsEffortIgnoredModel(t *testing.T) {
	in := LevelInputs{
		Config: &Config{Agent: Agent{
			Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
			Levels: ReviewLevels{L2: &LevelSetting{Model: "claude-haiku-4-5", Effort: "high"}},
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
	}
	_, warns, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with an effort on l2's haiku: %v", err)
	}
	mustWarn(t, warns, policy.LevelL2, "does nothing")

	// No effort, nothing to warn about: the model is still the same one.
	in.Config.Agent.Levels.L2.Effort = ""
	if _, warns, err := ResolveLevels(in); err != nil {
		t.Fatalf("ResolveLevels with no effort on l2's haiku: %v", err)
	} else if len(warns) != 0 {
		t.Errorf("warnings = %v; want none: no effort is set", warns)
	}
}

// TestResolveLevelsTaskBlockBeatsTheConfigsNone pins the order of the two
// layers the resolver reads for a level's on/off: the task's block is the one
// above the merged configuration's, so a task that names a key of a level
// turns it on over a configuration that wrote `none`, and a task that writes
// `none` turns off a level the configuration set. It is the order
// config.LayerCopyKey gives the layers, moved one level up.
func TestResolveLevelsTaskBlockBeatsTheConfigsNone(t *testing.T) {
	in := LevelInputs{
		Config: &Config{Agent: Agent{
			Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
			Levels: ReviewLevels{L2: &LevelSetting{Off: true}},
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
		Task: &ReviewLevels{L2: &LevelSetting{Model: "openai/gpt-4o"}},
	}
	got, _, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with the task naming l2 over the configuration's none: %v", err)
	}
	if !got.L2.On || got.L2.Model != "openai/gpt-4o" {
		t.Errorf("l2 = %+v; want it on the task's openai/gpt-4o: the task is the layer above the configuration", got.L2)
	}

	// The other direction: the configuration names the level and the task
	// writes none, which is the last word on whether the level runs.
	in.Config = &Config{Agent: Agent{
		Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
		Levels: ReviewLevels{L2: &LevelSetting{Model: "openai/gpt-4o"}},
	}}
	in.Task = &ReviewLevels{L2: &LevelSetting{Off: true}}
	got, _, err = ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with the task writing l2 none over the configuration's block: %v", err)
	}
	if got.L2.On {
		t.Errorf("l2 = %+v; want it off: the task wrote none and no floor requires the level", got.L2)
	}
}

// TestResolveLevelsOffLevelCarriesNoSettings: the resolver reads a level's
// model, effort and focus before it knows whether the level runs, so a level
// that then turns out to be off must come back with none of the three. A
// caller that read a model off an off level would put it in a stage the level
// does not run — and the settings are the reason the block is refused at
// decode when `none: true` sits beside one, so a block that carries both must
// not be the way past that rule either.
func TestResolveLevelsOffLevelCarriesNoSettings(t *testing.T) {
	in := LevelInputs{
		Config: &Config{Agent: Agent{
			Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
		Task: &ReviewLevels{L2: &LevelSetting{
			Off: true, Model: "openai/gpt-4o", Effort: "max", Focus: "auth boundaries",
		}},
	}
	got, warns, err := ResolveLevels(in)
	if err != nil {
		t.Fatalf("ResolveLevels with l2 off beside its settings: %v", err)
	}
	if got.L2 != (ResolvedLevel{}) {
		t.Errorf("l2 = %+v; want the zero ResolvedLevel: a level that does not run has no model, effort or focus", got.L2)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; want none: an off level has no model to warn about", warns)
	}
}

// TestResolveLevelsErrorReturnsNoLevels: a refusal must not hand back the
// levels it was in the middle of resolving. A caller that acted on them would
// launch the very run that was refused — the same-model pair, the effort on a
// provider route, the model outside allowed_models — and every one of these
// has at least one level already set when the refusal is raised.
func TestResolveLevelsErrorReturnsNoLevels(t *testing.T) {
	models := ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"}
	for _, tc := range []struct {
		what string
		in   LevelInputs
	}{
		{"two levels on one model", LevelInputs{
			Config: &Config{Agent: Agent{Models: ModelRoles{Coder: "openai/gpt-4o", Reviewer: "openai/gpt-4o"}}},
			Mode:   rigor.Pro, Recipe: levelRecipe(t, "standard"),
		}},
		{"an effort on a provider-served model", LevelInputs{
			Config: &Config{Agent: Agent{Models: models, Levels: ReviewLevels{L2: &LevelSetting{Model: "deepseek/deepseek-v4-flash", Effort: "high"}}}},
			Mode:   rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
			Providers: map[string]ModelProvider{"provider-a": {Models: []string{"deepseek/*"}}},
		}},
		{"l2 on a model outside allowed_models", LevelInputs{
			Config: &Config{Agent: Agent{Models: models}},
			Mode:   rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
			AllowedModels: []string{"openai/gpt-4o"},
		}},
	} {
		got, _, err := ResolveLevels(tc.in)
		if err == nil {
			t.Errorf("%s: ResolveLevels refused nothing", tc.what)
			continue
		}
		if got != (Levels{}) {
			t.Errorf("%s: levels = %+v beside the refusal %v; want the zero Levels, which no caller can act on", tc.what, got, err)
		}
	}
}

// TestResolveLevelsRefusesEffortOutsideTheSet: the effort's set is closed, and
// the resolver refuses a value outside it as well as the decoder does
// (LevelSetting.validate). It is the same refusal for the same reason — Claude
// Code takes a value it does not know without a word, sends the model's own
// default, and the run looks like it did what the setting asked — and the
// resolver is the check a task that never went through the decoder meets, at
// the launcher and at the runner's bootstrap (L3.2, L4.2).
func TestResolveLevelsRefusesEffortOutsideTheSet(t *testing.T) {
	in := LevelInputs{
		Config: &Config{Agent: Agent{
			Models: ModelRoles{Coder: "claude-sonnet-5-5", Reviewer: "claude-opus-5-5"},
		}},
		Mode: rigor.Pro, Recipe: levelRecipe(t, "default"), CatalogDefault: true,
		Task: &ReviewLevels{L2: &LevelSetting{Effort: "auto"}},
	}
	_, _, err := ResolveLevels(in)
	if err == nil {
		t.Fatal(`l2 ran with the effort "auto"; want it refused: the set is closed and Claude Code would send the model's default without a word`)
	}
	for _, want := range []string{"l2.effort", `"auto"`, "low, medium, high, xhigh, max"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}

	// Each value of the set is admitted, so what is refused is the value and
	// not an effort at level 2.
	for _, effort := range Efforts {
		in.Task = &ReviewLevels{L2: &LevelSetting{Effort: effort}}
		if _, _, err := ResolveLevels(in); err != nil {
			t.Errorf("ResolveLevels with l2.effort %q: %v; want it admitted", effort, err)
		}
	}
}
