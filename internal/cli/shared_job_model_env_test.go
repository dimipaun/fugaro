package cli

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runner"
)

// sharedJobCoder is the coder the project layer below puts on the fixture's
// repository: a model only a provider route serves — the pin a shared project
// job had no way to resolve (check 580: the run ended at bootstrap with "the
// models are not approved for this repository ... no provider serves it").
const sharedJobCoder = "deepseek/deepseek-v4-flash"

// sharedJobClaude is the layer's default model, and its reviewer is
// claude-opus-5: the pair testProjectLayerAgent carries, because a layer whose
// first-line and senior review would run one model is refused (D721's guard).
const sharedJobClaude = "claude-sonnet-5-5"

// sharedJobProviderConfig is the project's provider table and its price
// override as an operator's local config carries them — the block `fugaro
// project policy publish` copies into the policy node whole.
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

// TestSharedJobLaunchWritesNoModelEnvironment: a launch on a shared project
// job hands its execution no model environment. The routes, the prices and the
// allow-list such a run goes by are the owner's published policy's, and the run
// reads them there (internal/runner's resolvePolicy); a launcher's own config
// is not the owner of any of the three. It must not be: a route names the host
// a repository's code goes to and the secret that carries its key, and a price
// is what a call is charged at against the owner's cap, so a launcher that
// supplied either would send the profile's provider key and the repository's
// code to a host of its own choosing and could price its way around the
// ceiling.
//
// The job is the one that cannot carry them either: it is shared by every
// repository of its profile, so one repository's table in its definition would
// become every repository's (internal/infra's projectJobEnv).
func TestSharedJobLaunchWritesNoModelEnvironment(t *testing.T) {
	f := newProjectJobFixtureMode(t, "enforce")
	isolateCache(t)
	f.appendConfig(t, sharedJobProviderConfig)
	publishedLayer(t, f.cloudFixture, sharedJobModelLayer)
	projectJobCheckoutYAML(t, projectJobAnchoredRepoYAML)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090000-aaaa", "Add a feature"); err != nil {
		t.Fatalf("a launch of a provider-served coder on the installation's shared job: %v", err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != f.job {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the shared job %s: without that this test says nothing about a shared project job", len(calls), calls, f.job)
	}
	// The execution's own environment — the override a launch sets on the
	// execution, which is where the routes, the prices and the allow-list
	// would have been written.
	for _, name := range []string{runner.ModelProvidersEnv, runner.ModelPricesEnv, runner.AllowedModelsEnv} {
		if v, ok := calls[0].Env[name]; ok {
			t.Errorf("the launch wrote %s=%q on the execution: the run takes the project's models from the published policy, which is the one object a launcher cannot write", name, v)
		}
	}
}
