package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// callDeepseek is a step that makes one call on the provider's model (as
// Claude Code would), records the status and the body the agent saw, and
// writes the body to its transcript as Claude Code does.
func callDeepseek(status *int, body *string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*status, *body = post(t, req, deepseek, 1000)
		fmt.Fprintf(req.Transcript, "{\"type\":\"user\",\"message\":%q}\n", *body)
		return inner(t, ctx, req)
	}
}

// TestProviderRunEndsInPR is the end to end of design m10-multi-model.md: a
// run whose coder is deepseek/deepseek-v4-flash goes through the gateway to
// the provider, which gets the provider's key (and only it), and ends in a
// PR with the cost to the micro in result.json.
func TestProviderRunEndsInPR(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{Cost: 0.0021}.MessageOK(deepseek, use)}, "acme/app")
	prov := g.deps.Providers["openrouter"]
	prov.RouteFeePct = 10
	g.deps.Providers["openrouter"] = prov

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if status != 200 {
		t.Fatalf("the provider call got %d: %s", status, body)
	}
	onlyPR(t, g.provider)

	// The provider got its own key and nothing of the agent's.
	if n := g.compat.Count(); n != 1 {
		t.Fatalf("the provider saw %d calls", n)
	}
	if got := g.compat.Header(0, "Authorization"); got != "Bearer "+providerKey {
		t.Fatalf("the provider saw Authorization %q", got)
	}
	if got := g.compat.Header(0, "X-Api-Key"); got != "" {
		t.Fatalf("the provider saw X-Api-Key %q", got)
	}
	if g.fake.Count() != 0 {
		t.Fatalf("Anthropic saw %d calls of a provider-model run", g.fake.Count())
	}
	// Anthropic's key reaches the agent in no form and the provider's key
	// never does (the agent env is the gateway's token and URL).
	for i, c := range g.agent.calls {
		for _, kv := range c.Env {
			if strings.Contains(kv, providerKey) || strings.Contains(kv, plantedKey) {
				t.Errorf("call %d: a real key is in the agent's env: %s", i, kv)
			}
		}
	}

	// The fee and the attribution are the run's: the model's usage at the
	// run's own rate for it, plus the 10% route fee. The rate is the built-in
	// catalog's for the route's model — the zdr tier's minimum under the
	// default switches — read from the table the run built rather than named
	// here, so the arithmetic under test is the fee, the attribution and the
	// reported figure. The charge is in whole micros, as the engine's is.
	m, ok := g.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the model it routed")
	}
	if m.Catalog == nil || m.Catalog.Tier != "zdr" || m.Catalog.Bound != "min" {
		t.Fatalf("the run's row is not the catalog's zdr minimum: %+v", m.Catalog)
	}
	want := pricing.WithFee(m.Rates.Cost(use), 10).USD()
	if rec.Cost == nil || !near(rec.CostUSD, want) || !near(rec.Cost.RouteBy["openrouter"], want) || !near(rec.Cost.ModelBy[deepseek], want) {
		t.Fatalf("want %v: cost_usd = %v, cost = %+v", want, rec.CostUSD, rec.Cost)
	}
	if !near(rec.Cost.ReportedUSD, 0.0021) {
		t.Fatalf("reported_usd = %v, want the provider's figure, which is never charged", rec.Cost.ReportedUSD)
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || !near(stored.Cost.RouteBy["openrouter"], want) || !near(stored.Cost.ReportedUSD, 0.0021) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the provider key is stored in %v", keys)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Fatal("the log has the provider key")
	}
}

// callClaimed is callDeepseek for the claimed Claude ID: the review stage
// makes one call on the route's claimed model, as Claude Code would, and
// writes the body to its transcript.
func callClaimed(status *int, body *string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		*status, *body = post(t, req, claimedSenior, 4096)
		fmt.Fprintf(req.Transcript, "{\"type\":\"user\",\"message\":%q}\n", *body)
		return inner(t, ctx, req)
	}
}

// TestClaimedClaudeSeniorEndToEnd is Task 6 step 1: the whole run, on a
// provider whose models name the Claude ID explicitly and with the owner's
// model_prices row for it in the ceiling. The coder is the route's deepseek
// model and the reviewer is the claimed senior, and both stages make their
// call through the gateway: the coder's on the provider's model and the
// reviewer's on the claimed Claude one. Both calls reach the PROVIDER's fake
// and Anthropic's sees none, each with the provider's key and never the
// Anthropic one -- which is mounted, because this run's background pin is
// still Claude's own haiku (NeedsAnthropicKey), so the two keys are both
// there and the route still decides. The run ends in a PR.
//
// The claimed senior is a provider route in every respect (P10): its stage
// pins the model as Claude Code's opus role and carries the provider route's
// variable pair, in the process environment and in the managed settings file
// -- the same treatment the coder's stage gets (TestProviderStageDisablesThinkingVars),
// so nothing about the review stage says "direct Anthropic".
func TestClaimedClaudeSeniorEndToEnd(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := claimedRun(t, claimedPinned(t), "enforce", "50",
		[]anthropicfake.Reply{
			anthropicfake.Compat{}.MessageOK(deepseek, use),
			anthropicfake.Compat{}.MessageOK(claimedSenior, use),
		}, "acme/app")

	var coderStatus, reviewStatus int
	var coderBody, reviewBody string
	var coderManaged, reviewerManaged map[string]string
	rec, err := g.run(t,
		readManagedDuring(g.harness, &coderManaged, callDeepseek(&coderStatus, &coderBody, implement("feature"))),
		readManagedDuring(g.harness, &reviewerManaged, callClaimed(&reviewStatus, &reviewBody, review("ship", 0))))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if coderStatus != 200 || reviewStatus != 200 {
		t.Fatalf("statuses = %d, %d\ncoder: %s\nreviewer: %s", coderStatus, reviewStatus, coderBody, reviewBody)
	}
	onlyPR(t, g.provider)

	// Both stages' calls went to the provider; Anthropic's fake saw nothing at
	// all, so the claimed Claude call did not go direct.
	if n := g.compat.Count(); n != 2 {
		t.Fatalf("the provider saw %d calls, want the coder's and the reviewer's: %+v", n, g.compat.Seen())
	}
	if n := g.fake.Count(); n != 0 {
		t.Fatalf("Anthropic saw %d calls of a run whose every pin it does not serve", n)
	}
	// The route carried the claimed model: the provider's second request is
	// the reviewer's, on the claimed ID.
	if body := string(g.compat.Body(1)); !strings.Contains(body, `"model":"`+claimedSenior+`"`) {
		t.Fatalf("the provider's second call is not the claimed senior's: %s", body)
	}
	// The provider saw its own key and nothing of the Anthropic one.
	for i := 0; i < 2; i++ {
		if got := g.compat.Header(i, "Authorization"); got != "Bearer "+providerKey {
			t.Errorf("provider call %d: Authorization = %q", i, got)
		}
		if got := g.compat.Header(i, "X-Api-Key"); got != "" {
			t.Errorf("provider call %d: the Anthropic key's header is set: %q", i, got)
		}
		if strings.Contains(string(g.compat.Body(i)), plantedKey) {
			t.Errorf("provider call %d: the body carries the Anthropic key", i)
		}
	}
	// Neither key is in either stage's environment: the agent holds the
	// gateway's own token and URL, whatever route its call takes.
	for i, c := range g.agent.calls {
		for _, kv := range c.Env {
			if strings.Contains(kv, providerKey) || strings.Contains(kv, plantedKey) {
				t.Errorf("stage %d: a real key is in the agent's env: %s", i, kv)
			}
		}
	}

	// The claimed senior's stage is a provider stage exactly like the coder's.
	reviewer := g.agent.calls[1]
	if got := envValue(reviewer.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != claimedSenior {
		t.Errorf("the review stage pins %q as its model, want the claimed ID", got)
	}
	for k, v := range map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "MAX_THINKING_TOKENS": "0"} {
		if got := envValue(reviewer.Env, k); got != v {
			t.Errorf("review stage (claimed provider route), process env: %s = %q, want %q", k, got, v)
		}
		if got, ok := reviewerManaged[k]; !ok || got != v {
			t.Errorf("review stage (claimed provider route), managed settings env: %s = %q (present %v), want %q", k, got, ok, v)
		}
	}
	if got := envValue(g.agent.calls[0].Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != deepseek {
		t.Errorf("the coder stage pins %q, want the route's coder model", got)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Error("the log carries the provider key")
	}
}

// TestClaimedClaudeSeniorCharge is Task 6 step 3: the claimed senior's call
// is charged at the claimed model's own rates -- the owner's model_prices row
// for `anthropic/claude-sonnet-5.5` -- with the route's fee on top, and no
// direct-path Claude rate appears anywhere in the run's figures.
//
// The override's 3/15 is deliberately not the direct row's rates for the same
// family (claude-sonnet-5-5 is $2/$10 per million tokens, internal/pricing/table.go),
// so a run that charged the reviewer at the direct-path rate lands on a
// different number and this test says so: the two charges are computed here
// and asserted to differ before the one that must win is asserted.
func TestClaimedClaudeSeniorCharge(t *testing.T) {
	coderUse := pricing.Usage{Input: 1000, Output: 500}
	reviewUse := pricing.Usage{Input: 2000, Output: 1000}
	g := claimedRun(t, claimedPinned(t), "enforce", "50",
		[]anthropicfake.Reply{
			anthropicfake.Compat{}.MessageOK(deepseek, coderUse),
			anthropicfake.Compat{}.MessageOK(claimedSenior, reviewUse),
		}, "acme/app")
	prov := g.deps.Providers["openrouter"]
	prov.RouteFeePct = 10
	g.deps.Providers["openrouter"] = prov

	// The run's table prices the claimed ID from the owner's override, parsed
	// by the shipped path: no catalog row prices the ID (P11), so the
	// override is the only price it has, and the price table keys it whatever
	// the routes say (P4).
	ov, err := pricing.ParseOverrides(claimPricesJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimRates, ok := ov[claimedSenior]
	if !ok {
		t.Fatalf("the fixture's override has no row for %s: %v", claimedSenior, ov)
	}
	row, ok := g.deps.Spend.Prices.Lookup(claimedSenior)
	if !ok || row.Rates != claimRates {
		t.Fatalf("the run's table prices the claimed ID as %+v (found %v), want the owner's %+v", row.Rates, ok, claimRates)
	}
	direct, ok := pricing.Embedded().Lookup(sonnet)
	if !ok {
		t.Fatalf("the built-in table has no direct row for %s", sonnet)
	}
	if direct.Rates.Cost(reviewUse) == claimRates.Cost(reviewUse) {
		t.Fatalf("the direct row and the override charge the same for %+v: this test proves nothing", reviewUse)
	}

	var coderStatus, reviewStatus int
	var coderBody, reviewBody string
	rec, err := g.run(t,
		callDeepseek(&coderStatus, &coderBody, implement("feature")),
		callClaimed(&reviewStatus, &reviewBody, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if coderStatus != 200 || reviewStatus != 200 {
		t.Fatalf("statuses = %d, %d: %s %s", coderStatus, reviewStatus, coderBody, reviewBody)
	}

	// The claimed senior's charge is its own rates plus the route's fee, and
	// it is not what the direct Claude row would have charged.
	if rec.Cost == nil {
		t.Fatalf("the run recorded no cost: %+v", rec)
	}
	wantReview := pricing.WithFee(claimRates.Cost(reviewUse), 10)
	wantDirect := pricing.WithFee(direct.Rates.Cost(reviewUse), 10)
	if wantReview == wantDirect {
		t.Fatal("the fee makes the two charges coincide: this test proves nothing")
	}
	if got := micros(t, rec.Cost.ModelBy[claimedSenior]); got != wantReview {
		t.Errorf("the claimed senior was charged %v µ$, want %v (the owner's rates plus the fee); the direct Claude rate would have charged %v",
			got, wantReview, wantDirect)
	}
	// The coder's own row comes from the built-in catalog, read here from the
	// table the run built rather than named, so what is under test is the
	// claimed model's rate and not the catalog's.
	coderRow, ok := g.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the coder's model")
	}
	wantCoder := pricing.WithFee(coderRow.Rates.Cost(coderUse), 10)
	if got := micros(t, rec.Cost.ModelBy[deepseek]); got != wantCoder {
		t.Errorf("the coder was charged %v µ$, want %v", got, wantCoder)
	}
	// The two models' charges are the run's whole model cost and the whole of
	// the route's; nothing else was charged, so no direct-path Claude figure
	// is hiding in the totals.
	if len(rec.Cost.ModelBy) != 2 {
		t.Errorf("the run's figures name %v, want exactly the coder's model and the claimed ID", chargedNames(rec.Cost))
	}
	if got := rec.Cost.ModelBy[sonnet]; got != 0 {
		t.Errorf("a direct Claude rate is in the run's figures: %s = %v USD", sonnet, got)
	}
	if got := micros(t, rec.Cost.ModelBy[claimedSenior]); got != wantReview {
		t.Errorf("model_by[%s] = %v µ$, want %v", claimedSenior, got, wantReview)
	}
	if got, want := micros(t, rec.Cost.RouteBy["openrouter"]), wantReview+wantCoder; got != want {
		t.Errorf("route_by = %v µ$, want the coder's charge plus the claimed senior's (%v)", got, want)
	}
	if got := micros(t, rec.Cost.ModelUSD); got != wantReview+wantCoder {
		t.Errorf("model_usd = %v µ$, want %v", got, wantReview+wantCoder)
	}
	if !near(rec.CostUSD, (wantReview + wantCoder).USD()) {
		t.Errorf("cost_usd = %v, want %v", rec.CostUSD, (wantReview + wantCoder).USD())
	}
	// The stored record carries the same figures.
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || micros(t, stored.Cost.ModelBy[claimedSenior]) != wantReview || stored.Cost.ModelBy[sonnet] != 0 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

// callModel is a step that makes one call on model as Claude Code would, and
// fails the test if the gateway did not serve it: the statuses are asserted
// where a test needs them (callDeepseek, callClaimed), and a stage that only
// has to spend does not.
func callModel(model string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if status, body := post(t, req, model, 4096); status != 200 {
			t.Errorf("the call on %s got %d: %s", model, status, body)
		}
		return inner(t, ctx, req)
	}
}

// TestClaimedClaudeSeniorRecipes is Task 6 step 4: it pins P9's second half by
// naming the recipe. `auto` decides the derived recipe's first line, and a
// claimed Claude senior turns it off -- FirstLineOn under auto asks for a coder
// a provider serves *and a reviewer none does* -- so the derived recipe runs
// implement, review. A recipe the catalog names itself is not derived, and its
// own first_line step runs whatever auto would have said: the catalog's
// `standard` runs implement, review_first, review, the cheap step on the
// coder's model, and its `review: {bounce: first_line}` sends a rejection back
// into that step. Both subtests use `first_line_review: auto`, so the recipe
// name is the only difference between them.
func TestClaimedClaudeSeniorRecipes(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	// reply answers the nth call as the provider would: the coder's model for
	// the implement, the fix and every first-line review, the claimed Claude ID
	// for the senior ones.
	reply := func(model string) anthropicfake.Reply {
		return anthropicfake.Compat{}.MessageOK(model, use)
	}

	t.Run("the derived recipe runs no first line", func(t *testing.T) {
		g := claimedRun(t, claimedRecipe(t, "  first_line_review: auto\n  recipe: "+recipe.DefaultName+"\n"),
			"enforce", "50", []anthropicfake.Reply{reply(deepseek), reply(claimedSenior)}, "acme/app")
		var coderStatus, reviewStatus int
		var coderBody, reviewBody string
		rec, err := g.run(t,
			callDeepseek(&coderStatus, &coderBody, implement("feature")),
			callClaimed(&reviewStatus, &reviewBody, review("ship", 0)))
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if coderStatus != 200 || reviewStatus != 200 {
			t.Fatalf("statuses = %d, %d: %s %s", coderStatus, reviewStatus, coderBody, reviewBody)
		}
		if got := stageNames(rec); !eq(got, "implement", "review") {
			t.Fatalf("stages = %v, want no first-line step: auto is off once a provider serves the reviewer", got)
		}
		for _, r := range rec.Reviews {
			if r.Tier == runstore.TierFirst {
				t.Errorf("a first-line review ran under the derived recipe: %+v", r)
			}
		}
		onlyPR(t, g.provider)
	})

	t.Run("a non-derived recipe keeps its own first line, and its bounce", func(t *testing.T) {
		// The catalog's standard: first_line, then review: {bounce: first_line}.
		g := claimedRun(t, claimedRecipe(t, "  first_line_review: auto\n  recipe: standard\n"),
			"enforce", "50", []anthropicfake.Reply{
				reply(deepseek),      // implement
				reply(deepseek),      // review_first
				reply(deepseek),      // fix
				reply(claimedSenior), // review, round 1: a rejection
				reply(deepseek),      // fix
				reply(deepseek),      // review_first, the bounce
				reply(claimedSenior), // review, round 2
			}, "acme/app")
		rec, err := g.run(t,
			callModel(deepseek, implement("feature")),
			callModel(deepseek, review("changes", 1)), // review_first round 1
			callModel(deepseek, fixStep),
			callModel(claimedSenior, review("changes", 1)), // review round 1
			callModel(deepseek, fixStep),
			callModel(deepseek, review("ship", 0)),      // review_first, the bounce
			callModel(claimedSenior, review("ship", 0))) // review round 2
		if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review", "fix", "review_first", "review") {
			t.Fatalf("stages = %v", got)
		}
		if got := tiers(rec); !eq(got, "first:changes", "senior:changes", "first:ship", "senior:ship") {
			t.Fatalf("reviews = %v", got)
		}
		if rs := rec.Reviews; rs[2].Bounce != 1 || rs[2].Tier != runstore.TierFirst {
			t.Fatalf("the bounced first-line review = %+v, want Bounce 1, Tier first", rs[2])
		}
		// The first-line step runs on the coder's model, the senior on the
		// claimed ID: the recipe's step, not the reviewer's model.
		wantModels := []string{deepseek, deepseek, deepseek, claimedSenior, deepseek, deepseek, claimedSenior}
		for i, c := range g.agent.calls {
			if c.Model != wantModels[i] {
				t.Errorf("stage %d ran on %s, want %s", i, c.Model, wantModels[i])
			}
		}
		// Every call went to the provider: the claimed senior's reviews too.
		if n := g.compat.Count(); n != 7 {
			t.Fatalf("the provider saw %d calls, want 7", n)
		}
		if n := g.fake.Count(); n != 0 {
			t.Fatalf("Anthropic saw %d calls", n)
		}
		onlyPR(t, g.provider)
	})
}

// chargedNames is the models the run's figures name, in a fixed order, for a
// failure message (the map's order is not one).
func chargedNames(c *runstore.Cost) []string {
	out := slices.Sorted(maps.Keys(c.ModelBy))
	return out
}

// readManagedDuring wraps a step to snapshot the managed settings file's
// "env" (what Claude Code would actually merge in, taking precedence over
// any repository settings.json's own "env") into *dst while that stage runs,
// before the next stage's write replaces it.
func readManagedDuring(h *harness, dst *map[string]string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		env, _, _ := settingsEnv(t, h)
		*dst = env
		return inner(t, ctx, req)
	}
}

// TestProviderStageDisablesThinkingVars: a provider route refuses
// context_management and the extended-thinking additions Claude Code sends
// once thinking is on (forward.go's routedKeys; Check 25 of
// docs/gcp-live-checklist.md). providerPinned's coder is deepseek (a
// provider model) and its reviewer is Claude: the coder stage must see
// CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 and MAX_THINKING_TOKENS=0 exactly,
// both in the agent's process environment and in the managed settings file's
// "env" (the anti-override mechanism: Claude Code merges a settings file's
// own "env" over the process environment, and managed settings beat every
// other settings file, so only being in the managed file stops a repository's
// own .claude/settings.json from reinstating thinking). The review stage,
// entirely on Claude, must see neither, in either place, so its request
// shape stays exactly what it was.
func TestProviderStageDisablesThinkingVars(t *testing.T) {
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.MessageOK(deepseek, pricing.Usage{Input: 100, Output: 50})}, "acme/app")
	var status int
	var body string
	var coderManaged, reviewerManaged map[string]string
	rec, err := g.run(t,
		readManagedDuring(g.harness, &coderManaged, callDeepseek(&status, &body, implement("feature"))),
		readManagedDuring(g.harness, &reviewerManaged, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(g.agent.calls) != 2 {
		t.Fatalf("stages = %d, want 2: %+v", len(g.agent.calls), g.agent.calls)
	}
	coder, reviewer := g.agent.calls[0], g.agent.calls[1]
	want := map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "MAX_THINKING_TOKENS": "0"}
	for k, v := range want {
		if got := envValue(coder.Env, k); got != v {
			t.Errorf("coder stage (provider), process env: %s = %q, want %q", k, got, v)
		}
		if got, ok := coderManaged[k]; !ok || got != v {
			t.Errorf("coder stage (provider), managed settings env: %s = %q (present %v), want %q", k, got, ok, v)
		}
		if got := envValue(reviewer.Env, k); got != "" {
			t.Errorf("reviewer stage (Claude), process env: %s = %q, want unset", k, got)
		}
		if got, ok := reviewerManaged[k]; ok {
			t.Errorf("reviewer stage (Claude), managed settings env: %s = %q, want absent", k, got)
		}
	}
}

// providerReviewerPinned is providerPinned with the reviewer on the route's
// model too, so both roles are provider-served. That is the case `auto` needs
// to leave the derived recipe without a first line: FirstLineOn asks for a
// coder a provider serves *and a reviewer no provider serves*, so with a
// Claude coder the line is off for the coder's own sake and a fixture would
// stay green even with the reviewer clause deleted. The background stays
// Claude's haiku, which gwConfig pins explicitly: an unset one would become
// the coder's provider-served model (config.EffectiveBackground) and the
// review stage would carry the provider's variables for a reason this test is
// not about (Review Focus 10).
func providerReviewerPinned(t *testing.T) string {
	t.Helper()
	cfg := providerFirstLine(t, "")
	out := strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+deepseek, 1)
	if out == cfg {
		t.Fatal("the fixture has no reviewer line")
	}
	return out
}

// TestProviderReviewerSkipsTheFirstLine is the runner half of the
// mixed-credentials plan's Task 2: a provider on both the coder and the
// reviewer, `first_line_review: auto` (left to its default), and the derived
// catalog recipe -- so the first line is off and the run is implement, review.
// The senior review's Tier is "" rather than senior: it is a senior review in
// a run that had no first line (runstore), which is exactly what `auto` just
// made this run, so the assertion is Tier != runstore.TierFirst and never
// Tier == runstore.TierSenior. Both roles provider-served is the point of the
// fixture; the reviewer's stage is a provider stage in every respect, thinking
// off, and the second half of the test is the contrast that says so.
func TestProviderReviewerSkipsTheFirstLine(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerReviewerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{
			anthropicfake.Compat{}.MessageOK(deepseek, use),
			anthropicfake.Compat{}.MessageOK(deepseek, use),
		}, "acme/app")
	var coderStatus, reviewStatus int
	var coderBody, reviewBody string
	var reviewerManaged map[string]string
	rec, err := g.run(t,
		callDeepseek(&coderStatus, &coderBody, implement("feature")),
		readManagedDuring(g.harness, &reviewerManaged, callDeepseek(&reviewStatus, &reviewBody, review("ship", 0))))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if coderStatus != 200 || reviewStatus != 200 {
		t.Fatalf("statuses = %d, %d: %s %s", coderStatus, reviewStatus, coderBody, reviewBody)
	}
	onlyPR(t, g.provider)

	// The derived recipe has no first-line step under `auto` here.
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v, want no first-line step: auto is off once a provider serves the reviewer too", got)
	}
	if len(rec.Reviews) != 1 {
		t.Fatalf("reviews = %+v, want the senior review alone", rec.Reviews)
	}
	for _, r := range rec.Reviews {
		if r.Tier == runstore.TierFirst {
			t.Errorf("a first-line review ran under a provider-served reviewer: %+v", r)
		}
	}
	if got := rec.Reviews[0].Verdict; got != "ship" {
		t.Errorf("the senior review's verdict = %q, want ship", got)
	}

	// Both stages' calls went to the provider, so the review stage is a
	// provider stage: Anthropic's fake saw nothing at all.
	if n := g.compat.Count(); n != 2 {
		t.Fatalf("the provider saw %d calls, want the coder's and the reviewer's: %+v", n, g.compat.Seen())
	}
	if n := g.fake.Count(); n != 0 {
		t.Fatalf("Anthropic saw %d calls of a run whose every pin a provider serves", n)
	}
	reviewer := g.agent.calls[1]
	if got := envValue(reviewer.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != deepseek {
		t.Errorf("the review stage pins %q, want the provider's model", got)
	}
	for k, v := range map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "MAX_THINKING_TOKENS": "0"} {
		if got := envValue(reviewer.Env, k); got != v {
			t.Errorf("reviewer stage (provider route), process env: %s = %q, want %q", k, got, v)
		}
		if got, ok := reviewerManaged[k]; !ok || got != v {
			t.Errorf("reviewer stage (provider route), managed settings env: %s = %q (present %v), want %q", k, got, ok, v)
		}
	}

	// The contrast, in one short run: the same provider coder with a Claude
	// reviewer carries neither variable -- the stage is not a provider stage
	// -- so the assertions above are about the reviewer's own model and not
	// about the run. providerPinned's first line is off, so the reviewer is
	// the run's second and last stage.
	g = providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	var claudeManaged map[string]string
	var coderStatus2 int
	var coderBody2 string
	rec, err = g.run(t,
		callDeepseek(&coderStatus2, &coderBody2, implement("feature")),
		readManagedDuring(g.harness, &claudeManaged, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(g.agent.calls) != 2 {
		t.Fatalf("stages = %d, want 2: %+v", len(g.agent.calls), g.agent.calls)
	}
	claudeReviewer := g.agent.calls[1]
	if got := envValue(claudeReviewer.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != sonnet {
		t.Errorf("the Claude reviewer's stage pins %q, want its own model", got)
	}
	for k := range map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1", "MAX_THINKING_TOKENS": "0"} {
		if got := envValue(claudeReviewer.Env, k); got != "" {
			t.Errorf("reviewer stage (Claude), process env: %s = %q, want unset", k, got)
		}
		if got, ok := claudeManaged[k]; ok {
			t.Errorf("reviewer stage (Claude), managed settings env: %s = %q, want absent", k, got)
		}
	}
}

// Provider models are real dollars and observe never refuses a call, so a
// provider model is refused at bootstrap in observe (and off) mode, naming the
// rule and the command to change the mode; enforce runs, and a Claude-only run
// is fine in observe.
func TestProviderModelNeedsEnforce(t *testing.T) {
	g := providerRun(t, providerPinned(t), "observe", "", nil, "acme/app")
	refusedBeforeAnyCall(t, g, "budget.mode enforce", "fugaro budget set --global --mode enforce", "deepseek/deepseek-v4-flash")
	g = providerRun(t, providerPinned(t), "off", "", nil, "acme/app")
	refusedBeforeAnyCall(t, g, "budget gateway", "budget.mode to enforce")
	use := pricing.Usage{Input: 1000, Output: 500}
	g = providerRun(t, providerPinned(t), "enforce", "5", []anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	var status int
	var body string
	if rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("enforce: rec = %+v, err = %v", rec, err)
	}
	if strings.Contains(g.logs.String(), "provider_observe") {
		t.Error("the moot observe warning is back")
	}
	g = newGW(t, gwConfig(t, ""), "observe", "", capScript(1)...)
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatalf("a Claude-only run in observe: %v", err)
	}
}

// A provider that reports more than 10% above the charge is logged once at
// the end of the run (warn level, no key); a report below the charge is not.
// The charge is compared without the route fee, which the provider doesn't
// report. The reported figures are relative to the charge the run's own table
// makes for the usage, read from that table (Task 4: the catalog prices the
// route's model, at a fraction of the placeholder row this test was written
// against), so the case under test is the 10% threshold and not a number.
func TestReportedAboveChargeWarnsOnce(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g0 := providerRun(t, providerPinned(t), "enforce", "50", nil, "acme/app")
	m, ok := g0.deps.Spend.Prices.Lookup(deepseek)
	if !ok {
		t.Fatal("the run's table has no row for the model it routed")
	}
	bare := m.Rates.Cost(use).USD()
	for _, c := range []struct {
		cost float64
		fee  float64
		warn int
	}{
		{bare * 3, 0, 1}, {bare * 1.05, 0, 0}, {bare * 0.5, 0, 0},
		{bare * 1.2, 10, 1}, {bare * 1.05, 10, 0},
	} {
		g := providerRun(t, providerPinned(t), "enforce", "50", []anthropicfake.Reply{anthropicfake.Compat{Cost: c.cost}.MessageOK(deepseek, use)}, "acme/app")
		prov := g.deps.Providers["openrouter"]
		prov.RouteFeePct = c.fee
		g.deps.Providers["openrouter"] = prov
		var status int
		var body string
		if rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if n := strings.Count(g.logs.String(), "code=reported_gap"); n != c.warn {
			t.Errorf("reported %v: %d warnings, want %d:\n%s", c.cost, n, c.warn, g.logs.String())
		}
		if strings.Contains(g.logs.String(), providerKey) {
			t.Error("the log has the provider key")
		}
	}
}

// TestBackgroundDefaultsToCoder: with a provider coder and no background
// model, Claude Code's haiku role is pinned to the coder's model, so no
// hidden Claude call happens; a background model that is set is kept, and a
// Claude coder is untouched (its background is still required by the budget).
func TestBackgroundDefaultsToCoder(t *testing.T) {
	noBackground := strings.Replace(providerPinned(t), ", background: "+haiku, "", 1)
	if noBackground == providerPinned(t) {
		t.Fatal("fixture has no background")
	}
	g := providerRun(t, noBackground, "enforce", "50", nil, "acme/app")
	if rec, err := g.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for i, c := range g.agent.calls {
		for _, k := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL"} {
			if got := envValue(c.Env, k); i == 0 && got != deepseek {
				t.Errorf("stage %d: %s = %q, want the coder's model", i, k, got)
			}
		}
	}

	// An explicit background wins.
	g = providerRun(t, providerPinned(t), "enforce", "50", nil, "acme/app")
	if _, err := g.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if got := envValue(g.agent.calls[0].Env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != haiku {
		t.Errorf("an explicit background became %q", got)
	}

	// A Claude coder with no background is refused by the budget, as before.
	g = newGW(t, strings.Replace(gwConfig(t, ""), ", background: "+haiku, "", 1), "observe", "")
	if rec, err := g.run(t); err == nil || !strings.Contains(rec.Reason, "agent.models.background") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestProviderModelNeedsTheGateway: without a budget there is no gateway, and
// a call on a provider model would go to Anthropic: refused, before any call.
func TestProviderModelNeedsTheGateway(t *testing.T) {
	g := providerRun(t, providerPinned(t), "off", "", nil, "acme/app")
	refusedBeforeAnyCall(t, g, "gateway", "budget.mode")
}

// TestMissingProviderKeyIsInfraError: the key is not mounted, so the run does
// not start (no agent), naming the variable and never a value.
func TestMissingProviderKeyIsInfraError(t *testing.T) {
	g := providerRun(t, providerPinned(t), "enforce", "50", nil, "acme/app")
	g.deps.Env = slices.DeleteFunc(g.deps.Env, func(kv string) bool { return strings.HasPrefix(kv, orProvider.SecretEnv()+"=") })
	refusedBeforeAnyCall(t, g, "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY", "not mounted")
}

// TestHaltOnProviderRun: the cap holds a provider run as it does a Claude
// one: a call whose reservation does not fit is refused at the gateway and
// never forwarded, and the run halts. The cap is small because the route's
// model is priced by the built-in catalog at the zdr tier's minimum, a
// fraction of the placeholder row it replaced (Task 4): the reservation for
// a 1,000-token reply is a few hundred micro-dollars, so $0.00001 is under
// it while staying above the smallest cap the budget takes.
func TestHaltOnProviderRun(t *testing.T) {
	g := providerRun(t, providerPinned(t), "enforce", "0.00001", nil, "acme/app")
	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")))
	if err != nil {
		t.Fatal(err)
	}
	if status != 403 || g.compat.Count() != 0 {
		t.Fatalf("status = %d, provider calls = %d", status, g.compat.Count())
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("record = %+v", rec)
	}
}

// TestUpstreamErrorEchoingKeyIsRedacted: a provider that echoes the key it
// was sent in a 401 must not put it in the agent's hands, the transcript,
// result.json, the report, the PR or the logs; neither may a fake claude that
// echoes the key itself. (The gateway half is in internal/gateway.)
func TestUpstreamErrorEchoingKeyIsRedacted(t *testing.T) {
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Error(401, "authentication_error", "invalid api key "+providerKey+" (bearer "+providerKey+")")}, "acme/app")
	var status int
	var body string
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Stderr.Write([]byte("claude: " + providerKey + "\n"))
		res, err := implement("feature")(t, ctx, req)
		res.Text = "done " + providerKey
		return res, err
	}
	rec, err := g.run(t, callDeepseek(&status, &body, leak), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if status != 401 || strings.Contains(body, providerKey) {
		t.Fatalf("the agent saw %d %s", status, body)
	}
	if g.compat.Count() != 1 {
		t.Fatalf("the provider saw %d calls", g.compat.Count())
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v", rec)
	}
	if got, _ := json.Marshal(onlyPR(t, g.provider)); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the PR has the key: %s", got)
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the key is stored in %v", keys)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the record has the key: %s", got)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Fatal("the log has the key")
	}
}

// TestFirstLineErrorEchoingKeyIsRedacted: a first-line stage that fails with
// an error (and stderr) carrying the provider key is skipped with a log line,
// and the key reaches no log, record or stored object.
func TestFirstLineErrorEchoingKeyIsRedacted(t *testing.T) {
	g := providerRun(t, providerFirstLine(t, "on"), "enforce", "50", nil, "acme/app")
	broken := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Stderr.Write([]byte("claude: " + providerKey + "\n"))
		return agent.Result{}, errors.New("upstream said: bad key " + providerKey)
	}
	rec, err := g.run(t, implement("feature"), broken, review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := tiers(rec); !eq(got, "first:none", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if !strings.Contains(g.logs.String(), "the first-line review failed") {
		t.Fatal("the skip was not logged")
	}
	for _, l := range strings.Split(g.logs.String(), "\n") {
		if strings.Contains(l, providerKey) {
			t.Fatalf("the log has the key: %s", l)
		}
	}
	if keys := objectsContaining(t, g.harness, providerKey); len(keys) > 0 {
		t.Fatalf("the key is stored in %v", keys)
	}
	if got, _ := json.Marshal(rec); bytes.Contains(got, []byte(providerKey)) {
		t.Fatalf("the record has the key: %s", got)
	}
}

// TestGatewayRoutesMatchTheConfig: every model the config claims
// (ProviderFor) is routed by the gateway to that provider and no other, and a
// model the config does not claim goes to no provider: the routes are built
// from the config's patterns (runner.GatewayRoute), so they cannot drift.
// Each model is sent through a real gateway to one fake upstream per
// provider.
func TestGatewayRoutesMatchTheConfig(t *testing.T) {
	patterns := []string{"deepseek/*", "exact/model-1", "z-ai/glm-4.*", "x/y*"}
	models := []string{
		"deepseek/deepseek-v4-flash", "deepseek/a", "deepseek-x/a", "exact/model-1", "exact/model-10", "exact/model",
		"z-ai/glm-4.", "z-ai/glm-4.6", "z-ai/glm-5", "x/y", "x/yz", "x/w",
	}
	providers := map[string]config.ModelProvider{}
	fakes := map[string]*anthropicfake.Fake{}
	var routes []gateway.Route
	for i, pat := range patterns {
		name := fmt.Sprintf("p%d", i)
		fk, up := anthropicfake.New(t, replies(len(models))...)
		fakes[name] = fk
		providers[name] = config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: up.URL, Auth: "bearer", Secret: "key-" + name, Models: []string{pat}, RouteFeePct: 1}
		routes = append(routes, runner.GatewayRoute(name, providers[name], func() (string, error) { return "provider-key-0123", nil }))
	}
	if err := config.ValidateModelProviders(providers); len(err) > 0 {
		t.Fatal(err)
	}
	prices := map[string]pricing.Rates{}
	for _, m := range models {
		prices[m] = pricing.Rates{InputPerM: 1, OutputPerM: 1}
	}
	table, err := pricing.Embedded().With(prices)
	if err != nil {
		t.Fatal(err)
	}
	anth, anthUp := anthropicfake.New(t, replies(len(models))...)
	srv, err := gateway.Start(context.Background(), gateway.Options{
		Upstream: gateway.Upstream{Kind: "anthropic", BaseURL: anthUp.URL, APIKey: plantedKey},
		Routes:   routes, Prices: table, Mode: gateway.Observe,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	for _, m := range models {
		srv.BeginStage(gateway.Stage{Name: "implement", Model: m})
		before := map[string]int{}
		for n, fk := range fakes {
			before[n] = fk.Count()
		}
		beforeAnth := anth.Count()
		body := fmt.Sprintf(`{"model":%q,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, m)
		postBody(t, srv.URL()+"/v1/messages", srv.Token(), body)
		srv.EndStage()
		got := ""
		for n, fk := range fakes {
			if fk.Count() > before[n] {
				got += n
			}
		}
		want, claimed := "", false
		if name, _, ok := config.ProviderFor(providers, m); ok {
			want, claimed = name, true
		}
		if got != want {
			t.Errorf("%q: the config claims it for %q (%v), the gateway routed it to %q", m, want, claimed, got)
		}
		if !claimed && anth.Count() == beforeAnth {
			t.Errorf("%q: not claimed, yet it reached nobody", m)
		}
	}
}

// replies are n small replies, for a fake that is asked more than once.
func replies(n int) []anthropicfake.Reply {
	out := make([]anthropicfake.Reply, n)
	for i := range out {
		out[i] = anthropicfake.MessageOK("m", pricing.Usage{Output: 1})
	}
	return out
}

// TestProviderRunStartsWithNoAnthropicKey (design project-centric §6.1, review
// Medium 1): a run whose coder is a provider's needs no Anthropic key at all.
// The gateway starts without one, the agent's environment is built without
// one, and the call reaches the provider with the provider's key -- the
// counterpart of TestBuildEnvNoGatekeeperNeedsTheKeyActionably, which covers
// the one case where the key is still not optional (no gateway).
func TestProviderRunStartsWithNoAnthropicKey(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	// No key stored anywhere: the env the job runs with has none.
	g.deps.Env = filterEnv(g.deps.Env, "ANTHROPIC_API_KEY")

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if status != 200 {
		t.Fatalf("the provider call got %d: %s", status, body)
	}
	if n := g.compat.Count(); n != 1 {
		t.Errorf("the provider saw %d calls", n)
	}
	if g.fake.Count() != 0 {
		t.Errorf("Anthropic saw %d calls of a provider-only run with no key", g.fake.Count())
	}
	// The agent still gets an ANTHROPIC_API_KEY: the gateway's own token, the
	// stand-in that keeps Claude Code pointed at the gateway with no stored
	// key (and no provider key anywhere near it).
	for i, c := range g.agent.calls {
		v := envValue(c.Env, "ANTHROPIC_API_KEY")
		if v == "" {
			t.Errorf("call %d: the agent's ANTHROPIC_API_KEY is empty", i)
		}
		if strings.Contains(envValue(c.Env, "ANTHROPIC_BASE_URL"), "openrouter") || v == providerKey {
			t.Errorf("call %d: the agent holds a real key or a direct provider URL: %v", i, c.Env)
		}
	}
}

// TestResultJSONCacheTokenTotals (P12): the run's prompt-cache read and write
// token totals reach result.json's cost, summed from the gateway's own ledger
// of its calls' usage — the figure design §3's prompt-caching risk is read
// from. They are token counts, so nothing prices them.
func TestResultJSONCacheTokenTotals(t *testing.T) {
	use := pricing.Usage{Input: 1000, CacheWrite5m: 30, CacheWrite1h: 5, CacheRead: 400, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Both cache TTLs are cache writes: the run wrote 35 tokens to the cache.
	if rec.Cost == nil || rec.Cost.CacheReadTokens != 400 || rec.Cost.CacheWriteTokens != 35 {
		t.Fatalf("cost = %+v, want 400 cache reads and 35 cache writes", rec.Cost)
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || stored.Cost.CacheReadTokens != 400 || stored.Cost.CacheWriteTokens != 35 {
		t.Fatalf("stored cost = %+v, %v", stored.Cost, err)
	}
	raw, err := json.Marshal(rec.Cost)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cache_read_tokens":400`, `"cache_write_tokens":35`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("result.json's cost lacks %s: %s", want, raw)
		}
	}
}
