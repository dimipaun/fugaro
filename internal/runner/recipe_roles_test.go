package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func TestRecipeClaudeSolo(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 3, ""), recipeSpec(catalogTask(t, "claude-solo")))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	c := h.agent.calls
	if len(c) != 2 || c[1].Model != "coder-model" || c[1].SessionID == c[0].SessionID || c[1].JSONSchema == "" {
		t.Fatalf("calls = %+v (the coder's model reviews, in a fresh session, with the verdict schema)", c)
	}
}

// Solo still needs a ship: one round that asks for changes is a draft.
func TestRecipeClaudeSoloStillNeedsShip(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 3, ""), recipeSpec(catalogTask(t, "claude-solo")))
	rec, err := h.run(t, implement("feature"), review("changes", 2))
	if err != nil || rec.Outcome != runstore.OutcomeDraft || rec.Reason != "review round 1 still has 2 findings" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// soloGW is the gateway fixture with an opus reviewer, a sonnet coder and
// agent.recipe: claude-solo; extra is appended at the top level.
func soloGW(t *testing.T, recipeLine, extra string) string {
	t.Helper()
	cfg := gwConfig(t, recipeLine)
	cfg = strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+opus, 1)
	return cfg + extra
}

func TestRecipeSoloPinsTheCoder(t *testing.T) {
	reply := anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 10})
	g := newGW(t, soloGW(t, "  recipe: claude-solo\n", ""), "observe", "", reply)
	var st []int
	rec, err := g.run(t, implement("feature"), calling(&st, review("ship", 0), call{sonnet, 100}, call{opus, 100}))
	if err != nil || len(st) != 2 || st[0] != 200 || st[1] != 400 {
		t.Fatalf("rec = %+v, err = %v, statuses = %v (the gateway pins the coder's model for the review stage and refuses the old reviewer's)", rec, err, st)
	}
	if n := strings.Count(g.logs.String(), "agent.models.reviewer ignored: recipe maps reviewer to coder"); n != 1 {
		t.Fatalf("the dropped reviewer model was logged %d times:\n%s", n, g.logs.String())
	}
	c := g.agent.calls
	if c[1].Model != sonnet {
		t.Fatalf("review model = %q, want the coder's %s", c[1].Model, sonnet)
	}
	if got := envValue(c[1].Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != sonnet {
		t.Fatalf("review pin = %q, want the coder's %s", got, sonnet)
	}
}

// The alias resolves before the allow-list: the opus reviewer is not on the
// list, but under claude-solo the reviewer runs sonnet, which is.
func TestRecipeSoloAllowListSeesCoderModel(t *testing.T) {
	list := "budget:\n  allowed_models: [" + sonnet + ", " + haiku + "]\n"
	g := newGW(t, soloGW(t, "", list), "observe", "")
	refusedBeforeAnyCall(t, g, "agent.models.reviewer")
	g = newGW(t, soloGW(t, "  recipe: claude-solo\n", list), "observe", "")
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// The alias cannot launder a coder that is off the list.
func TestRecipeSoloCannotEscapeAllowList(t *testing.T) {
	cfg := strings.Replace(soloGW(t, "  recipe: claude-solo\n", "budget:\n  allowed_models: ["+sonnet+", "+haiku+"]\n"),
		"coder: "+sonnet, "coder: "+opus, 1)
	refusedBeforeAnyCall(t, newGW(t, cfg, "observe", ""), "agent.models.coder")
}

// coderIsReviewerRecipe is a custom repo recipe with roles: {coder:
// reviewer} (G9, premium's "coder on the reviewer's model"): one senior
// review, no first line.
const coderIsReviewerRecipe = "version: 1\nname: coder-reviewer\nroles:\n  coder: reviewer\nsteps:\n  - review: { max_rounds: 1 }\n"

// TestCoderIsReviewerPinsTheReviewerModel: under roles: {coder: reviewer},
// the implement stage's gateway pin is the reviewer's model (and the old
// coder's is refused, mirroring TestRecipeSoloPinsTheCoder's check of the
// review stage under reviewer: coder); the review stage's own pin, already
// the reviewer's, is unchanged.
func TestCoderIsReviewerPinsTheReviewerModel(t *testing.T) {
	files := map[string]string{".fugaro/recipes/coder-reviewer.yaml": coderIsReviewerRecipe}
	reply := anthropicfake.MessageOK(opus, pricing.Usage{Output: 10})
	g := newGWFiles(t, soloGW(t, "  recipe: coder-reviewer\n", ""), "observe", "", files, reply)
	var st []int
	// The implement stage fails once the refused probe runs (same as a
	// refused call during any pinned stage), so the run's final outcome is
	// not checked here, exactly as TestRecipeSoloPinsTheCoder does not check
	// it for the review stage.
	_, err := g.run(t, calling(&st, implement("feature"), call{opus, 100}, call{sonnet, 100}))
	if err != nil || len(st) != 2 || st[0] != 200 || st[1] != 400 {
		t.Fatalf("err = %v, statuses = %v (the gateway pins the reviewer's model for the implement stage and refuses the old coder's)", err, st)
	}
	if n := strings.Count(g.logs.String(), "agent.models.coder ignored: recipe maps coder to reviewer"); n != 1 {
		t.Fatalf("the dropped coder model was logged %d times:\n%s", n, g.logs.String())
	}
	c := g.agent.calls
	if c[0].Model != opus {
		t.Fatalf("implement model = %q, want the reviewer's %s", c[0].Model, opus)
	}
	if got := envValue(c[0].Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != opus {
		t.Fatalf("implement pin = %q, want the reviewer's %s", got, opus)
	}

	// The review stage's own pin is unchanged: a plain run (no gateway
	// probing) still sends it to the reviewer's model, distinct from the
	// coder's.
	h := newHarnessFiles(t, firstLineCfg(t, 3, "  recipe: coder-reviewer\n"), nil, files)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	c = h.agent.calls
	if len(c) != 2 || c[0].Model != "senior-model" || c[1].Model != "senior-model" {
		t.Fatalf("calls = %+v (both implement and review run on the reviewer's model)", c)
	}
}

// The reviewer takes the coder's per-call output limit too (D8): under solo a
// 3,000-token review call passes the coder's 4,096 limit; without the recipe
// the reviewer's own limit of 100 refuses it.
func TestRecipeSoloTakesTheCodersOutputLimit(t *testing.T) {
	limits := func(t *testing.T, recipeLine string) string {
		cfg := gwConfig(t, recipeLine)
		out := strings.Replace(cfg, "max_output_tokens: { coder: 4096, reviewer: 4096 }", "max_output_tokens: { coder: 4096, reviewer: 100 }", 1)
		if out == cfg {
			t.Fatal("fixture has no max_output_tokens line")
		}
		return out
	}
	reply := anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 10})
	var st []int
	g := newGW(t, limits(t, "  recipe: claude-solo\n"), "observe", "", reply)
	rec, err := g.run(t, implement("feature"), calling(&st, review("ship", 0), call{sonnet, 3000}))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(st) != 1 || st[0] != 200 {
		t.Fatalf("rec = %+v, err = %v, statuses = %v", rec, err, st)
	}
	st = nil
	g = newGW(t, limits(t, ""), "observe", "", reply)
	if _, err = g.run(t, implement("feature"), calling(&st, review("ship", 0), call{sonnet, 3000})); err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0] != 400 {
		t.Fatalf("without the recipe the reviewer's own limit applies: statuses = %v", st)
	}
}

// The coder takes the reviewer's per-call output limit too, the mirror of
// TestRecipeSoloTakesTheCodersOutputLimit: under roles: {coder: reviewer} a
// 3,000-token implement call passes the reviewer's 4,096 limit; without the
// recipe the coder's own limit of 100 refuses it.
func TestCoderReviewerTakesTheReviewersOutputLimit(t *testing.T) {
	files := map[string]string{".fugaro/recipes/coder-reviewer.yaml": coderIsReviewerRecipe}
	limits := func(t *testing.T, recipeLine string) string {
		cfg := gwConfig(t, recipeLine)
		out := strings.Replace(cfg, "max_output_tokens: { coder: 4096, reviewer: 4096 }", "max_output_tokens: { coder: 100, reviewer: 4096 }", 1)
		if out == cfg {
			t.Fatal("fixture has no max_output_tokens line")
		}
		return out
	}
	reply := anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 10})
	var st []int
	g := newGWFiles(t, limits(t, "  recipe: coder-reviewer\n"), "observe", "", files, reply)
	rec, err := g.run(t, calling(&st, implement("feature"), call{sonnet, 3000}), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(st) != 1 || st[0] != 200 {
		t.Fatalf("rec = %+v, err = %v, statuses = %v", rec, err, st)
	}
	st = nil
	g = newGWFiles(t, limits(t, ""), "observe", "", files, reply)
	if _, err = g.run(t, calling(&st, implement("feature"), call{sonnet, 3000})); err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 || st[0] != 400 {
		t.Fatalf("without the recipe the coder's own limit applies: statuses = %v", st)
	}
}
