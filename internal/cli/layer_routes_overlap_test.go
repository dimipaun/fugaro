package cli

import (
	"strings"
	"testing"
)

// TestValidateJudgesALayerClaimAgainstTheRoutesTheRepositoryHas is the
// review's false-halt case, one commit for `layerRoutes` merging the layer's
// claims over the *unfiltered* lc.Providers while the runner merges them over
// the routes the repository actually has (runner.resolveConfig:
// config.ClaimsOnRoutes over the job's own FUGARO_MODEL_PROVIDERS, which
// runner.ProvidersEnv writes with only the entries whose allow_data_to names
// the repository).
//
// A claim that lands on a route this repository carries changes that route's
// models, and the merge's overlap rule then compares it with every *other*
// entry of the table it was handed. Over the whole installation config that
// includes entries the repository's runs never carry, so a claim colliding
// only with one of those halts validate while the run resolves the very same
// claim happily: 208 such false halts in the reviewer's matrix.
//
// Mutation that makes this fail: hand layerRoutes lc.Providers (drop the
// runner.ProvidersFor filter), or merge without config.ClaimsOnRoutes.
func TestValidateJudgesALayerClaimAgainstTheRoutesTheRepositoryHas(t *testing.T) {
	const id = "anthropic/claude-sonnet-5.5"
	// checkout writes the owner's routes: openrouter serves acme/other (the
	// fixture checkout's repository), vendorx serves acme/app only, and both
	// claim the ID the layer is about to add to openrouter. The layer claims
	// it for openrouter, exactly as a layer does for a route it means to
	// extend.
	checkout := func(t *testing.T, vendorAllows string) (string, string, error) {
		t.Helper()
		f := newCloudFixture(t)
		isolateCache(t)
		f.appendConfig(t, "providers:\n"+
			"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
			"  vendorx:\n    kind: anthropic-compat\n    base_url: https://vendorx.example\n    auth: bearer\n    secret: vendorx-key\n    models: [\""+id+"\"]\n    allow_data_to: [\""+vendorAllows+"\"]\n"+
			"model_prices:\n  \""+id+"\": { input_per_m: 3, output_per_m: 15 }\n")
		repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
			"agent:\n  auth: api-key\n  model: claude-sonnet-5-5\n  models: { reviewer: claude-sonnet-5-5, background: claude-sonnet-5-5 }\n" +
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
		layerCheckout(t, f, repo)
		publishedLayer(t, f, testProjectLayer+"claude_models:\n  openrouter: [\""+id+"\"]\n")
		out, errOut, err := execute(t, "validate")
		return out, errOut, err
	}
	t.Run("a route this repository does not carry cannot collide", func(t *testing.T) {
		// vendorx serves acme/app: it is in the installation config and in no
		// run of this repository's, so the run's merge has one entry
		// claiming the ID, and so must validate's.
		out, errOut, err := checkout(t, "acme/app")
		if err != nil || !strings.Contains(out, "is valid") {
			t.Fatalf("the claim collides only with a route this repository does not carry: exit %d\n%s%s", ExitCode(err), out, errOut)
		}
	})
	t.Run("a route this repository does carry still collides", func(t *testing.T) {
		// The control: the same claim, with vendorx serving this repository
		// too. The run's own merge refuses this (its routes hold both
		// entries), so validate must.
		out, errOut, err := checkout(t, "acme/other")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a claim that collides with a route this repository carries: exit %d\n%s%s", ExitCode(err), out, errOut)
		}
		if !strings.Contains(out, "overlaps") {
			t.Errorf("the refusal is not the overlap rule's:\n%s", out)
		}
	})
}

// A layer with no claude_models at all is the state every repository is in
// before an operator claims anything: the routes are the owner's own,
// untouched, whatever repository they are filtered for. It is the control
// for the filter above — a filter that dropped entries unconditionally, or a
// merge that copied the table and lost one, would fail here.
func TestValidateALayerWithNoClaimsLeavesTheRoutesAsTheyAre(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	f.appendConfig(t, "providers:\n"+
		"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
		"model_prices:\n  \"deepseek/deepseek-v4.1-flash\": { input_per_m: 3, output_per_m: 15 }\n")
	repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
		"agent:\n  auth: api-key\n  model: deepseek/deepseek-v4.1-flash\n  models: { reviewer: deepseek/deepseek-v4.1-flash, background: deepseek/deepseek-v4.1-flash }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	layerCheckout(t, f, repo)
	publishedLayer(t, f, testProjectLayer)
	if out, errOut, err := execute(t, "validate"); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("a layer with no claims: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
}
