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
	g := newGW(t, soloGW(t, "  recipe: claude-solo\n", ""), "observe", "")
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
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
