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

// TestValidateKeepsTheProviderReasonBesideALayerClaim is the review finding on
// the table the *non-routing* rules are handed. When the layer carries any
// claim at all, layerRoutes now answers with the run's own routes — the
// operator's providers filtered by allow_data_to — and validate.go gave that
// same map to budgetProblems and providerProblems, although the filter is the
// routing rules' own reading. providerProblems judges a pin by asking which
// entry claims it and whether *that* entry's allow_data_to names this
// repository; a table the filter has already dropped the entries from can
// never answer the second question, so a pin vendorx serves for another
// repository became "is not a Claude model and no provider serves it: providers
// are set by the owner in the project config" — the operator is sent to write a
// provider that already exists, instead of to add this repository to its
// allow_data_to.
//
// The claim here is unrelated: it names a route this repository does carry
// (openrouter), and the pin (zz/m1) is vendorx's, which this repository may not
// use. With no claim at all the message was already right, and it must stay
// that way: the claim-blind table validate reads is the owner's whole config.
//
// Mutation that makes this fail: hand providerProblems (and budgetProblems) the
// filtered merge, i.e. the table layerRoutes returns for the routing rules.
func TestValidateKeepsTheProviderReasonBesideALayerClaim(t *testing.T) {
	// vendorx serves zz/* for acme/app alone; the checkout is acme/other, and
	// the file pins zz/m1. openrouter is the route the layer claims a Claude
	// model for, which is all the finding needs: a layer with a claim.
	checkout := func(t *testing.T, layerClaims string) (string, string, error) {
		t.Helper()
		f := newCloudFixture(t)
		isolateCache(t)
		f.appendConfig(t, "providers:\n"+
			"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [\"acme/other\"]\n"+
			"  vendorx:\n    kind: anthropic-compat\n    base_url: https://vendorx.example\n    auth: bearer\n    secret: vendorx-key\n    models: [\"zz/*\"]\n    allow_data_to: [\"acme/app\"]\n"+
			"model_prices:\n  \"zz/m1\": { input_per_m: 3, output_per_m: 15 }\n")
		repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 2 }\n" +
			"agent:\n  auth: api-key\n  model: zz/m1\n  models: { reviewer: zz/m1, background: zz/m1 }\n" +
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
		layerCheckout(t, f, repo)
		publishedLayer(t, f, testProjectLayer+layerClaims)
		out, errOut, err := execute(t, "validate")
		return out, errOut, err
	}
	// The fix the operator is sent to: list this repository for the provider
	// that serves the pin. Every role names the model, so the sentence is the
	// same for each of them.
	const want = "`zz/m1` is served by provider vendorx, whose allow_data_to does not name `acme/other`"
	for _, tc := range []struct {
		name   string
		claims string
	}{
		{"a layer with a claim for another route", "claude_models:\n  openrouter: [\"anthropic/claude-haiku-4.5\"]\n"},
		{"a layer with no claim at all", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := checkout(t, tc.claims)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("a pin whose provider may not serve this repository: exit %d\n%s%s", ExitCode(err), out, errOut)
			}
			if !strings.Contains(out, want) {
				t.Fatalf("the refusal does not name the provider that serves the pin:\n%s%s", out, errOut)
			}
			if strings.Contains(out, "no provider serves it") {
				t.Errorf("the operator is sent to add a provider that exists:\n%s", out)
			}
		})
	}
}

// TestValidatePointsAtAllowDataToBesideALayerClaim is the same reading on the
// other sentence that names an entry's allow_data_to: the switch's own list,
// where config.CheckRouting judges a model over the run's table and reads a
// route this repository may not use as "no provider entry claims it"
// (switchListFilteredClause). That clause resolves the model over the
// operator's own table and the run's, so routingProblems must hand it the
// operator's — the table the caller reads for the routing rules is the run's
// own, and over it both resolutions agree and the clause is never written: the
// operator is sent to write a route entry that is there.
//
// The layer carries a claim for exactly the entry this repository may not use,
// which is what makes the two tables differ: openrouter serves acme/sandbox,
// the checkout is acme/other (layerCheckout), and the claim is dropped for this
// repository like every other route it does not carry (config.ClaimsOnRoutes).
// The pins are another Claude model, so the switch's own list is the whole of
// the question.
//
// Mutation that makes this fail: pass routingProblems' own table to the clause
// (switchListFilteredClause(sw, m, repo, routes, carried)), i.e. read the
// operator's routes from the table that was already filtered.
func TestValidatePointsAtAllowDataToBesideALayerClaim(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	f.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5 }\n"+
		"providers:\n"+
		"  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n    auth: bearer\n    secret: openrouter-api-key\n    models: [\"anthropic/claude-sonnet-5.5\"]\n    allow_data_to: [\"acme/sandbox\"]\n"+
		"routing:\n  claude:\n    via: openrouter\n    models: [claude-sonnet-5-5]\n")
	repo := "version: 1\nproject: aurora\ngcp_project: proj-1234\nbudget: { mode: enforce, per_run_usd: 5 }\n" +
		"agent:\n  auth: api-key\n  model: claude-opus-5-5\n  models: { background: claude-haiku-4-5 }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	layerCheckout(t, f, repo)
	publishedLayer(t, f, testProjectLayer+"claude_models:\n  openrouter: [\"anthropic/claude-haiku-4.5\"]\n")
	out, errOut, err := execute(t, "validate")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a switch no run of this repository can carry out is a halt: exit %d\n%s%s", ExitCode(err), out, errOut)
	}
	// The rule's own sentence, then the clause that names the entry the
	// operator has to add this repository to.
	if !strings.Contains(out, "no provider entry claims it") {
		t.Fatalf("the rule's own sentence is gone:\n%s", out)
	}
	for _, want := range []string{"openrouter", "allow_data_to", `"acme/sandbox"`, "acme/other", "fugaro update --repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("the line does not name %q, so the operator is sent to write an entry that is there:\n%s", want, out)
		}
	}
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
