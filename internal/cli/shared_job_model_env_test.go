package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
)

// sharedJobCoder is the coder the project layer below puts on the fixture's
// repository: a model only a provider route serves, which is the pin a shared
// project job had no way to resolve (check-580: the run ended at bootstrap
// with "the models are not approved for this repository ... no provider
// serves it").
const sharedJobCoder = "deepseek/deepseek-v4-flash"

// sharedJobClaude is the layer's default model, and its reviewer is
// claude-opus-5: the pair testProjectLayerAgent carries, because a layer whose
// first-line and senior review would run one model is refused (D721's guard).
const sharedJobClaude = "claude-sonnet-5-5"

// sharedJobProviderConfig is the project's provider table and its price
// override as an operator's local config carries them. Two routes, one serving
// both of the fixture's repositories and one serving only acme/other, so a
// launch of acme/app must carry the first ALONE, with its allow_data_to cut to
// the repository the run is for (runner.ProvidersEnv's filter): the routes
// belong on the execution and not in a job every repository of the profile
// shares.
const sharedJobProviderConfig = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: [acme/app, acme/other]
  moonshot:
    kind: anthropic-compat
    base_url: https://api.moonshot.example/v1
    auth: bearer
    secret: moonshot-api-key
    models: ["moonshotai/*"]
    allow_data_to: [acme/other]
model_prices:
  ` + sharedJobCoder + `: { input_per_m: 0.28, output_per_m: 0.42 }
`

// sharedJobModelLayer is the project layer that puts the provider's model on
// the fixture's coder: a repository's own fugaro.yaml may name no model at all
// since S1 (D3237), so the project layer is where a run reads the pin — the
// same place an installation states the coder a check like 580 was about.
const sharedJobModelLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  agent: { auth: api-key, model: ` + sharedJobClaude + `, models: { coder: ` + sharedJobCoder + `, reviewer: claude-opus-5, background: claude-haiku-4-5 } }
`

// TestSharedJobLaunchCarriesTheProjectsModels is check 580's fix, at the
// launcher: a launch that resolved a shared project job hands its execution
// the project's model routes, its price overrides and its model allow-list,
// filtered per repository — the values a per-repository job carries in its own
// environment (internal/infra's budgetEnv and runner.ProvidersEnv) and a
// shared job cannot, where one repository's table would become every
// repository's.
//
// The test then reads those values back through the runner's own readers, which
// is what makes it a statement about bootstrap rather than about a string:
// checkProviderModels is exactly CheckProviderPolicy and CheckProviderGateway
// over the run's routes, its price table and its pins, and before this change
// the run on a shared job was refused by the first of them with "no provider
// serves it".
//
// Red against main, where the execution carried none of the three and the
// readers answered with no routes at all.
func TestSharedJobLaunchCarriesTheProjectsModels(t *testing.T) {
	f := newProjectJobFixtureMode(t, "enforce")
	isolateCache(t)
	f.appendConfig(t, sharedJobProviderConfig)
	// The allowed-models list is the fixture's own budget block: appendConfig
	// replaces that block whole when the yaml names one (localcfg refuses a
	// second budget key), so the list is written into the block it has.
	f.rewriteConfig(t, "budget: { mode: enforce, per_run_usd: 5, ",
		"budget: { mode: enforce, per_run_usd: 5, allowed_models: ["+sharedJobClaude+", "+sharedJobCoder+"], ")
	publishedLayer(t, f.cloudFixture, sharedJobModelLayer)
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("a launch of a provider-served coder on the installation's shared job: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != f.job {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the shared job %s: without that this test says nothing about a shared project job", len(calls), calls, f.job)
	}
	// The container's environment: the job's own deploy-time entries with this
	// execution's overrides over them. The fake's project job carries no mode
	// (AddProjectJob seeds the budget backend's two variables alone), and the
	// mode is the job's own entry and never a per-execution one, so it is
	// stated here as internal/infra's projectJobEnv deploys it.
	env := maps.Clone(calls[0].Env)
	env[runner.BudgetModeEnv] = "enforce"
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	routes, err := runner.ProvidersFromEnv(lookup)
	if err != nil {
		t.Fatalf("the execution's own environment is one the runner refuses: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("the execution carries the routes %v, want openrouter alone: the run's own environment is what says which provider serves its models, and a shared job's deployment carries none", slices.Sorted(maps.Keys(routes)))
	}
	openrouter := routes["openrouter"]
	if want := []string{"acme/app"}; !slices.Equal(openrouter.AllowDataTo, want) {
		t.Errorf("the execution's openrouter route allows data to %v, want %v: the owner's allow_data_to is a per-repository fact, and the route the run carries is cut to the repository it is for (runner.ProvidersEnv)", openrouter.AllowDataTo, want)
	}
	if want := []string{"deepseek/*"}; !slices.Equal(openrouter.Models, want) {
		t.Errorf("the execution's openrouter route serves %v, want %v", openrouter.Models, want)
	}

	ov, err := pricing.ParseOverrides(env[runner.ModelPricesEnv])
	if err != nil {
		t.Fatalf("the execution's %s is not one the runner parses: %v", runner.ModelPricesEnv, err)
	}
	rates, ok := ov[sharedJobCoder]
	if !ok || rates.InputPerM != 0.28 || rates.OutputPerM != 0.42 {
		t.Errorf("the execution prices %s at %+v (present %v), want the project config's 0.28/0.42: without the project's overrides a shared job charges the built-in catalog's guess, or nothing at all", sharedJobCoder, rates, ok)
	}

	spend, err := runner.SpendFromEnv(lookup)
	if err != nil {
		t.Fatalf("the execution's own environment is one the runner refuses: %v", err)
	}
	if want := []string{sharedJobClaude, sharedJobCoder}; !slices.Equal(spend.AllowedModels, want) {
		t.Errorf("the execution's allow-list is %v, want %v", spend.AllowedModels, want)
	}

	// Bootstrap's model approval, over the environment the execution carries:
	// the two checks checkProviderModels runs, with the run's own pins. The
	// agent is the layer's document as the run resolves it — the provider's
	// model for the coder, the layer's own for the reviewer — and the
	// allow-list passed to CheckProviderPolicy is the FILE's, which the
	// checkout sets none of.
	agent := config.Agent{Auth: "api-key", Model: sharedJobClaude,
		Models: config.ModelRoles{Coder: sharedJobCoder, Reviewer: "claude-opus-5", Background: "claude-haiku-4-5"}}
	if ps := config.CheckProviderPolicy(agent, nil, "acme/app", routes, spend.Prices); len(ps) > 0 {
		t.Errorf("the run is refused at bootstrap for its models: %v", ps)
	}
	if ps := config.CheckProviderGateway(agent, "", routes, "enforce"); len(ps) > 0 {
		t.Errorf("the run is refused at bootstrap for its provider models: %v", ps)
	}
}
