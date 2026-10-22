package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	return layerClaimGWMode(t, cfg, layer, "enforce", "50", extra, script...)
}

// layerClaimGWMode is layerClaimGW with the budget mode and cap a test
// chooses: the mode is what CheckProviderGateway judges a claimed pin by.
func layerClaimGWMode(t *testing.T, cfg, layer, mode, capUSD string, extra map[string]config.ModelProvider, script ...anthropicfake.Reply) *gw {
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
	// The job's own FUGARO_MODEL_PROVIDERS, round-tripped: it is written per
	// repository, with only the providers that list that repository in
	// allow_data_to, and the run reads its routes back from it (ProvidersEnv,
	// ProvidersFromEnv — what exec.go does). The layer tests go through the
	// cut so they see the table a real job has, not the installation's.
	providers = cutRoutes(t, providers, "acme/app")
	h.deps.Providers = providers
	raw, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		runner.BudgetModeEnv: mode, runner.MaxRunUSDEnv: capUSD, runner.ModelProvidersEnv: string(raw),
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

// claimCfgClaimed is the fixture whose coder and reviewer are both the
// layer's claimed model, with the background pin removed and the first-line
// review off: no pin names a model the job's own route table carries, so the
// claim alone decides that the run is a provider run — (r *run).gatewayRoutes
// registers no route without it, (r *run).stageOnProviderRoute says no stage
// is a provider stage, and config.EffectiveBackground pins no background.
func claimCfgClaimed(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "  first_line_review: off\n")
	cfg = strings.Replace(cfg, "coder: "+sonnet, "coder: "+claimedClaude, 1)
	cfg = strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+claimedClaude, 1)
	cfg = strings.Replace(cfg, ", background: "+haiku, "", 1)
	cfg = strings.Replace(cfg, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	return cfg
}

// claimCfgMixed is the fixture whose coder and background are a Claude model
// that goes direct to Anthropic, and whose reviewer is the layer's claimed
// model: the two kinds of stage side by side in one run.
func claimCfgMixed(t *testing.T) string {
	t.Helper()
	cfg := gwConfig(t, "  first_line_review: off\n")
	cfg = strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+claimedClaude, 1)
	cfg = strings.Replace(cfg, "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
	return cfg
}

// mustLayer is layer text as config.ParseProjectLayer reads it.
func mustLayer(t *testing.T, text string) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte(text), config.LayerAnchor{Project: "aurora", GCPProject: "proj-1234"})
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

// cutRoutes is a job's own route table: runner.ProvidersEnv for repo, read
// back through runner.ProvidersFromEnv, so a test sees exactly the providers
// (and the allow_data_to) the deployed job carries.
func cutRoutes(t *testing.T, providers map[string]config.ModelProvider, repo string) map[string]config.ModelProvider {
	t.Helper()
	raw, err := runner.ProvidersEnv(providers, repo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.ProvidersFromEnv(func(k string) (string, bool) {
		return raw, k == runner.ModelProvidersEnv
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
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

// A claim is a route's worth of routing on its own: a run whose pins all name
// the claimed model is a provider run, with the route registered, both stages
// provider stages and the background the coder's own claimed model. No pin
// names anything the job's own table carries, so nothing but the merge can
// put this run on the provider. Mutations that make this fail, one at a time:
// (r *run).gatewayRoutes' `used` map built from r.d.Providers (no route is
// registered, and both calls reach Anthropic), (r *run).stageOnProviderRoute
// reading r.d.Providers (no stage carries agent.ProviderRouteVars), and
// (r *run).backgroundModel reading r.d.Providers through
// config.EffectiveBackground (the background pin becomes empty and CheckPins
// refuses the run).
func TestLayerClaimAloneMakesTheRunAProviderRun(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfgClaimed(t), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(claimedClaude, use), anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	rec, err := g.run(t, callOn(claimedClaude, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	onlyPR(t, g.provider)
	if n := g.compat.Count(); n != 2 {
		t.Fatalf("the provider saw %d calls, want both stages", n)
	}
	if g.fake.Count() != 0 {
		t.Fatalf("Anthropic saw %d calls: the claim did not put the run on the route", g.fake.Count())
	}
	if rec.Cost == nil || rec.Cost.ModelBy[claimedClaude] <= 0 || rec.Cost.RouteBy["openrouter"] <= 0 {
		t.Fatalf("cost = %+v", rec.Cost)
	}
	for i, call := range g.agent.calls {
		if got := envValue(call.Env, "MAX_THINKING_TOKENS"); got != "0" {
			t.Fatalf("stage %d is not a provider stage: MAX_THINKING_TOKENS = %q", i, got)
		}
		if got := envValue(call.Env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != claimedClaude {
			t.Fatalf("stage %d: ANTHROPIC_DEFAULT_HAIKU_MODEL = %q, want the claimed coder", i, got)
		}
	}
}

// The claim routes the reviewer's calls and leaves a Claude coder's stage
// alone: agent.ProviderRouteVars are for a stage the gateway would send to a
// provider, so a direct model's stage never carries them (P10). Mutation that
// makes this fail: (r *run).stageOnProviderRoute reading r.d.Providers, which
// is where the claim is not — the reviewer's stage then loses the provider's
// variables while the coder's stage is untouched.
func TestLayerClaimLeavesADirectCoderStageAlone(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfgMixed(t), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	// The direct upstream answers the coder's own call.
	g.fake.Script = []anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(sonnet, use)}
	rec, err := g.run(t, callOn(sonnet, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if g.fake.Count() != 1 || g.compat.Count() != 1 {
		t.Fatalf("Anthropic saw %d calls and the provider %d, want the coder's and the reviewer's", g.fake.Count(), g.compat.Count())
	}
	if len(g.agent.calls) != 2 {
		t.Fatalf("stages = %d", len(g.agent.calls))
	}
	coder, reviewer := g.agent.calls[0], g.agent.calls[1]
	if got := envValue(coder.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != sonnet {
		t.Fatalf("the coder's stage was pointed at %q", got)
	}
	for _, v := range []string{"MAX_THINKING_TOKENS", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"} {
		if got := envValue(coder.Env, v); got != "" {
			t.Errorf("the direct coder's stage carries %s = %q", v, got)
		}
	}
	if got := envValue(reviewer.Env, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != claimedClaude {
		t.Fatalf("the reviewer's stage was pointed at %q", got)
	}
	for _, v := range []string{"MAX_THINKING_TOKENS", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"} {
		if got := envValue(reviewer.Env, v); got == "" {
			t.Errorf("the claimed reviewer's stage does not carry %s", v)
		}
	}
}

// A claimed pin is a model a provider serves, and the credential rule is
// asked of it exactly as of any other provider model: an oauth token is a
// Claude subscription credential and never goes to another provider.
// Mutation that makes this fail: checkProviderModels calling
// config.CheckProviderAuth with r.d.Providers instead of r.modelProviders()
// (the claimed model is not in the job's own table, so the refusal is never
// written and the run starts).
func TestLayerClaimedPinUnderOauthIsRefused(t *testing.T) {
	cfg := strings.Replace(claimCfgMixed(t), "auth: api-key", "auth: oauth", 1)
	g := layerClaimGW(t, cfg, layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil)
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	refusedBeforeAnyCall(t, g, "agent.models.reviewer", "served by provider openrouter",
		"an oauth token is a Claude subscription credential")
}

// The mode rule is asked of a claimed pin too: observe never refuses a call
// and a provider's calls are real dollars, so a claimed pin needs the budget
// gateway enforcing. Mutation that makes this fail: checkProviderModels
// calling config.CheckProviderGateway with r.d.Providers instead of
// r.modelProviders() (no problem is written, and the observe run starts).
func TestLayerClaimedPinUnderObserveIsRefused(t *testing.T) {
	g := layerClaimGWMode(t, claimCfgMixed(t), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), "observe", "50", nil)
	// The refusal names the model, not the path: checkProviderModels reports
	// CheckProviderGateway's message alone (ps[0].Message).
	refusedBeforeAnyCall(t, g, claimedClaude, "is served by a provider, which needs budget.mode enforce")
}

// The environment the run routes by is the one written for the repository,
// and the claim — which only the layer carries — is not in it: the run
// merges the claim on top of that table, never into it. That is why removing
// the claim from the layer puts the refusal back on the next launch, with no
// stale environment to step around. (The other half — that `fugaro update
// --repo` writes no claim, whatever the layer says — is
// internal/infra's TestTheJobProvidersEnvNeverCarriesALayerClaim, where that
// table is written; this asserts the input side, the run's own Deps.)
func TestLayerClaimIsNeverInTheEnvironment(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n"), nil,
		anthropicfake.Compat{}.MessageOK(deepseek, use), anthropicfake.Compat{}.MessageOK(claimedClaude, use))
	rec, err := g.run(t, callOn(deepseek, implement("feature")), callOn(claimedClaude, review("ship", 0)))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// The table the run started from — the toolchain's own environment,
	// captured before the merge — has no claimed model in it. That the
	// *deployed job's* FUGARO_MODEL_PROVIDERS carries none either is pinned
	// where it is written:
	// internal/infra's TestTheJobProvidersEnvNeverCarriesALayerClaim (this
	// test cannot see that variable, only this fixture's deps).
	raw, err := json.Marshal(g.deps.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "anthropic/") {
		t.Fatalf("the environment carries the claim: %s", raw)
	}
	// And the merge is what the run routed with, so the claimed model did
	// reach the provider.
	merged, ps := config.ApplyClaudeClaims(g.deps.Providers, config.ClaimsOnRoutes(g.deps.Providers, config.ClaudeClaimsOf(mustLayer(t, layerWithClaim("  openrouter: [\""+claimedClaude+"\"]\n")))))
	if len(ps) != 0 || len(merged["openrouter"].Models) != 2 {
		t.Fatalf("the merged table = %v (problems %v)", merged["openrouter"].Models, ps)
	}
	if !slices.Contains(merged["openrouter"].Models, claimedClaude) {
		t.Fatalf("the merged table does not carry the claim: %v", merged["openrouter"].Models)
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

// A layer is installation-wide; a job's FUGARO_MODEL_PROVIDERS is written
// per repository, with only the providers that list that repository in
// allow_data_to. So a claim naming a provider this job does not carry is a
// route *this* repository does not have — never a config error, which the
// run cannot tell apart from a name nothing defines (both are simply absent
// from its table). Refusing here would break every repository the owner did
// not list for that provider, including the ones that never name the claimed
// model, while `fugaro validate` — which judges the installation config's
// whole route table — calls the same layer valid. The refusal for a name the
// installation does not define lives there and in `fugaro config publish`,
// where that whole table is known (TestPublishRefusesAClaimForAnUndefinedProvider).
func TestLayerClaimForARouteThisRepositoryDoesNotHave(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	// The owner's second route: qwen/*, allowed for acme/other only, so
	// acme/app's job does not carry it at all.
	other := config.ModelProvider{Kind: config.ModelProviderKind, BaseURL: "https://other.example/api",
		Auth: "bearer", Secret: "other-api-key", Models: []string{"qwen/*"}, AllowDataTo: []string{"acme/other"}}
	for _, name := range []string{"other", "nosuch"} {
		g := layerClaimGW(t, claimCfg(t, deepseek), layerWithClaim("  "+name+": [\""+claimedClaude+"\"]\n"),
			map[string]config.ModelProvider{"other": other},
			anthropicfake.Compat{}.MessageOK(deepseek, use), anthropicfake.Compat{}.MessageOK(deepseek, use))
		if _, ok := g.deps.Providers[name]; ok {
			t.Fatalf("the fixture's job carries %s: the cut is not the one under test", name)
		}
		rec, err := g.run(t, callOn(deepseek, implement("feature")), callOn(deepseek, review("ship", 0)))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("claiming %s: rec = %+v, err = %v", name, rec, err)
		}
		// The claim changed nothing: the run's routes are the job's own, and
		// the claimed model was never named by any pin.
		if g.compat.Count() != 2 || g.fake.Count() != 0 {
			t.Fatalf("claiming %s: the provider saw %d calls, Anthropic %d", name, g.compat.Count(), g.fake.Count())
		}
	}
	// A pin that names the claimed model is refused by the ordinary rule —
	// the route that claims it is not one this repository has — and never
	// with a message about the layer's key.
	g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim("  other: [\""+claimedClaude+"\"]\n"),
		map[string]config.ModelProvider{"other": other})
	refusedBeforeAnyCall(t, g, "no provider serves it", "agent.models.reviewer")
}

// Review Focus 3: the claim is judged by CheckClaudeClaim, the same rule a
// route entry is judged by, so a class of Claude models can never be claimed
// from the layer either.
func TestLayerClaimMustBeExplicit(t *testing.T) {
	// 101 characters: past the bound a route entry is held to
	// (config.CheckModelPattern). A claim is appended to the named route's own
	// models, so an over-long one would make the gateway refuse to start every
	// run whose job carries that route -- the run must be refused at the layer
	// instead, before any call.
	long := "anthropic/" + strings.Repeat("c", 91)
	if len(long) != 101 {
		t.Fatalf("the fixture ID is %d characters, want 101", len(long))
	}
	for _, claims := range []string{
		"  openrouter: [\"anthropic/*\"]\n",
		"  openrouter: [\"claude-sonnet-5-5\"]\n",
		"  openrouter: []\n",
		"  openrouter: [\"" + long + "\"]\n",
	} {
		g := layerClaimGW(t, claimCfg(t, claimedClaude), layerWithClaim(claims), nil)
		rec, err := g.run(t)
		// The reason must be the *layer* being invalid, not the pin the claim
		// would have served: the unclaimed-model refusal's own sentence names
		// "the project layer's claude_models" too, so matching on that
		// substring alone would pass for a run refused for any other reason.
		if err == nil || rec.Status != runstore.StatusInfraError ||
			!strings.Contains(rec.Reason, "the task's project layer") || !strings.Contains(rec.Reason, "is invalid") ||
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
