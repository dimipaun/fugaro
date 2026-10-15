package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// claimedClaude is the one Claude ID a layer claims: an explicit
// anthropic/<id>, the only shape a claim may take.
const claimedClaude = "anthropic/claude-sonnet-5.5"

// layerWithClaim is a project layer of the fixture's project carrying
// claude_models ("" for a layer with no claim at all).
func layerWithClaim(claims string) string {
	text := "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	if claims != "" {
		text += "claude_models:\n" + claims
	}
	return text
}

// claimCfg is the fixture anchored to the layer's own project and GCP
// project, with the provider's model as the coder, reviewer as the
// reviewer role, the background pinned explicitly (stageOnProviderRoute
// reads it too) and first_line_review left unset, so `auto` decides.
func claimCfg(t *testing.T, reviewer string) string {
	t.Helper()
	cfg := gwConfig(t, "")
	cfg = strings.Replace(cfg, "coder: "+sonnet, "coder: "+deepseek, 1)
	cfg = strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+reviewer, 1)
	cfg = strings.Replace(cfg, "background: "+haiku, "background: "+deepseek, 1)
	cfg = strings.Replace(cfg, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	return cfg
}

// layerClaimGW is a gw run whose job carries the owner's route (deepseek/*
// on openrouter, which may send acme/app code), the mode and cap, and the
// claimed ID's price under model_prices — the owner's ceiling, which a
// project layer cannot carry. The claim itself is only ever in the layer.
func layerClaimGW(t *testing.T, cfg, layer string, extra map[string]config.ModelProvider, script ...anthropicfake.Reply) *gw {
	t.Helper()
	h := newHarnessFiles(t, cfg, layerSpec(layer), nil)
	// The direct Anthropic upstream the run must not reach, and the
	// provider's own fake.
	anth, anthUp := anthropicfake.New(t)
	compat, up := anthropicfake.New(t, script...)
	h.deps.GatewayUpstream = anthUp.URL
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "ANTHROPIC_API_KEY="+plantedKey)
	providers := map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: up.URL, Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"},
	}}
	for name, p := range extra {
		providers[name] = p
	}
	h.deps.Providers = providers
	raw, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		runner.BudgetModeEnv: "enforce", runner.MaxRunUSDEnv: "50", runner.ModelProvidersEnv: string(raw),
		runner.ModelPricesEnv: `{"` + claimedClaude + `": {"input_per_m": 3, "output_per_m": 15}}`,
	}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ov, err := pricing.ParseOverrides(env[runner.ModelPricesEnv])
	if err != nil {
		t.Fatal(err)
	}
	spend.Prices = freshCatalogTable(t, providers, ov)
	h.deps.Spend = spend
	withProviderKey(h)
	return &gw{harness: h, fake: anth, compat: compat, logs: &lockedBuf{}}
}

// callOn makes one call on model through the gateway, as Claude Code would,
// and writes the body to its transcript.
func callOn(model string, inner step) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		status, body := post(t, req, model, 1000)
		if status != 200 {
			t.Errorf("the call on %s got %d: %s", model, status, body)
		}
		fmt.Fprintf(req.Transcript, "{\"type\":\"user\",\"message\":%q}\n", body)
		return inner(t, ctx, req)
	}
}

// Review Focus 9, the mutation that matters: the layer's claim is merged in
// (*run).resolveConfig, before planOf asks FirstLineOn about the reviewer.
// A merge any later (next to checkProviderModels, say) leaves the reviewer
// unclaimed here, and the derived recipe keeps its cheap first line.
func TestLayerClaimUnderAutoSkipsTheFirstLine(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(deepseek, use), anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	rec, err := g.run(t, callOn(deepseek, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v", got)
	}
	// The tier of a senior review in a run that never had a first line is
	// "", not senior (runner.go's StepFirstLine branch is the only place
	// TierSenior is set), so the assertion is "no first-line entry".
	for _, r := range rec.Reviews {
		if r.Tier == runstore.TierFirst {
			t.Fatalf("a first-line review ran: %+v", rec.Reviews)
		}
	}
	if g.fake.Count() != 0 {
		t.Fatalf("Anthropic saw %d calls of a layer-claimed run", g.fake.Count())
	}
}

// The claim reaches the routes: the reviewer's call goes to the provider,
// and the reviewer's stage is a provider stage.
func TestLayerClaimEnablesTheRoute(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(deepseek, use), anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	rec, err := g.run(t, callOn(deepseek, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	onlyPR(t, g.provider)
	if n := g.compat.Count(); n != 2 {
		t.Fatalf("the provider saw %d calls, want the coder's and the reviewer's", n)
	}
	if g.fake.Count() != 0 {
		t.Fatalf("Anthropic saw %d calls", g.fake.Count())
	}
	if body := string(g.compat.Body(1)); !strings.Contains(body, claimedClaude) {
		t.Fatalf("the reviewer's call did not name the claimed model: %s", body)
	}
	if rec.Cost == nil || rec.Cost.ModelBy[claimedClaude] <= 0 || rec.Cost.RouteBy["openrouter"] <= 0 {
		t.Fatalf("cost = %+v", rec.Cost)
	}
	// The reviewer's stage carries the gateway's pins for the claimed
	// model and the provider's own variables (P10).
	last := g.agent.calls[len(g.agent.calls)-1]
	if got := envValue(last.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != claimedClaude {
		t.Fatalf("the reviewer's stage was pointed at %q", got)
	}
	if got := envValue(last.Env, "MAX_THINKING_TOKENS"); got != "0" {
		t.Fatalf("MAX_THINKING_TOKENS = %q on the reviewer's stage", got)
	}
}

// The claim is never in the environment: Deps.Providers is what
// FUGARO_MODEL_PROVIDERS decoded to — the local config's routes, written
// once per repository by `fugaro init --repo` — and the claim, which only
// the layer carries, is not in it. That is also why removing the claim from
// the layer puts the refusal back on the next launch.
func TestLayerClaimIsNeverInTheEnvironment(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(deepseek, use), anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	rec, err := g.run(t, callOn(deepseek, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	raw, err := runner.ProvidersEnv(g.deps.Providers, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "anthropic/") {
		t.Fatalf("the environment carries the claim: %s", raw)
	}
	// The run itself routed the claimed model all the same.
	if g.compat.Count() != 2 {
		t.Fatalf("the provider saw %d calls", g.compat.Count())
	}
}

// The same fixture with the claim removed from the layer: the refusal is
// back, before any call, naming the pin.
func TestLayerClaimRemovedPutsTheRefusalBack(t *testing.T) {
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim(""), nil)
	refusedBeforeAnyCall(t, g, "no provider serves it", "agent.models.reviewer")
}

// A layer that did not apply (it names another project) changes nothing:
// the claim is not merged and the run is refused exactly as without it.
func TestLayerClaimNotAppliedChangesNothing(t *testing.T) {
	other := "version: 1\nproject: other\ngcp_project: proj-1234\nclaude_models:\n  openrouter: [\"" + claimedClaude + "\"]\n"
	g := layerClaimGW(t, claimCfg(t, claimedClaude), other, nil)
	g.deps.Project = ""
	rec, err := g.run(t)
	if err == nil || rec.ProjectLayer == nil || rec.ProjectLayer.Applied ||
		!strings.Contains(rec.Reason, "no provider serves it") || g.fake.Count() != 0 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// P8: a claim may only add to a route the owner defined; an unknown name
// fails bootstrap, naming the key, before any call.
func TestLayerClaimUnknownProviderRefused(t *testing.T) {
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  nosuch: [\""+claimedClaude+"\"]\n"), nil)
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError ||
		!strings.Contains(rec.Reason, "claude_models.nosuch") || !strings.Contains(rec.Reason, "does not define") ||
		len(g.agent.calls) != 0 || g.fake.Count() != 0 || g.compat.Count() != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(g.agent.calls))
	}
}

// Review Focus 3: the claim is judged by CheckClaudeClaim, the same rule a
// route entry is judged by, so a class of Claude models can never be claimed
// from the layer either.
func TestLayerClaimMustBeExplicit(t *testing.T) {
	for _, claims := range []string{
		"  openrouter: [\"anthropic/*\"]\n",
		"  openrouter: [\"claude-sonnet-5-5\"]\n",
		"  openrouter: []\n",
	} {
		g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim(claims), nil)
		rec, err := g.run(t)
		if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "claude_models") ||
			len(g.agent.calls) != 0 {
			t.Fatalf("claims %q: rec = %+v, err = %v", claims, rec, err)
		}
	}
}

// A claim that collides with another route's pattern is a config problem,
// not a gateway that refuses to start.
func TestLayerClaimOverlapRefused(t *testing.T) {
	thief := config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: "https://thief.example/api",
		Auth: "bearer", Secret: "thief-api-key", Models: []string{claimedClaude}, AllowDataTo: []string{"acme/app"}}
	g := layerClaimGW(t, claimCfg(t, deepseek), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"),
		map[string]config.ModelProvider{"thief": thief})
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "overlaps") ||
		len(g.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
