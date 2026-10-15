package runner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The default branch is fetched and read once per run: a later call gets
// the first answer, so the project check and the policy see the same file
// and the agent can't move it between them.
func TestDefaultBranchFileReadOnce(t *testing.T) {
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	remote := testutil.NewRemote(t, files)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "w"), remote, gitops.WithVars(gitops.IdentityEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{repo: repo}
	ctx := context.Background()
	first, def, err := r.defaultBranchFile(ctx)
	if err != nil || def != "main" || string(first) != files["fugaro.yaml"] {
		t.Fatalf("%q, %q, %v", first, def, err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"fugaro.yaml": files["fugaro.yaml"] + "# moved\n"})
	testutil.Git(t, seed, "commit", "--quiet", "-am", "move")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	second, _, err := r.defaultBranchFile(ctx)
	if err != nil || string(second) != string(first) {
		t.Fatalf("second read differs: %q, %v", second, err)
	}
}

// A tag (or local branch) named like the remote-tracking ref must not
// shadow the default branch's file.
func TestDefaultBranchFileNotShadowedByTag(t *testing.T) {
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	remote := testutil.NewRemote(t, files)
	dir := filepath.Join(t.TempDir(), "w")
	repo, err := gitops.OpenOrClone(context.Background(), dir, remote, gitops.WithVars(gitops.IdentityEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "checkout", "--quiet", "-b", "evil")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "# evil\n"})
	testutil.Git(t, dir, "commit", "--quiet", "-am", "evil")
	testutil.Git(t, dir, "tag", "origin/main")
	r := &run{repo: repo}
	got, _, err := r.defaultBranchFile(context.Background())
	if err != nil || string(got) != files["fugaro.yaml"] {
		t.Fatalf("got %q, %v", got, err)
	}
}

// FileLayer is the one construction of a fugaro.yaml's policy that the run
// and `fugaro validate` share: every policy key is in it.
func TestFileLayerCarriesEveryPolicyKey(t *testing.T) {
	cfg, ps := config.Parse([]byte("version: 1\nproject: x\ngit: { provider: github }\nagent:\n  max_run_tokens: 7\n  max_output_tokens: { coder: 11, reviewer: 13 }\n" +
		"budget: { mode: observe, per_run_usd: 3, per_day_usd: 9, allowed_models: [claude-haiku-4-5] }\nworkflows:\n  s: { commands: { build: make, test: make } }\n"))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	want := policy.Layer{Mode: "observe", PerRunUSD: 3, PerDayUSD: 9, MaxRunTokens: 7, MaxOutputCoder: 11, MaxOutputReviewer: 13, AllowedModels: []string{"claude-haiku-4-5"}}
	if got := FileLayer(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("FileLayer = %+v, want %+v", got, want)
	}
}

// result.json's policy object carries the day cap, and a record without one
// has no per_day_usd key.
func TestPolicyRecordCarriesPerDay(t *testing.T) {
	rec := policyRecord(policy.Effective{Layer: policy.Layer{PerDayUSD: 7.5},
		Sources: map[string]string{policy.KeyPerDayUSD: policy.SourceDefaultBranch}}, 0)
	if rec == nil || rec.Effective.PerDayUSD != 7.5 || rec.Sources["per_day_usd"] != "default-branch" {
		t.Fatalf("record = %+v", rec)
	}
	raw, err := json.Marshal(rec)
	if err != nil || !strings.Contains(string(raw), `"per_day_usd":7.5`) {
		t.Fatalf("json = %s, %v", raw, err)
	}
	other := policyRecord(policy.Effective{Layer: policy.Layer{PerRunUSD: 2}, Sources: map[string]string{policy.KeyPerRunUSD: policy.SourceCeiling}}, 0)
	if raw, _ := json.Marshal(other); strings.Contains(string(raw), "per_day_usd") {
		t.Fatalf("json = %s", raw)
	}
}

// (code review, medium) The policy node's model_prices replace the job
// environment's, and they must do it on the run's own table, not on the
// embedded one: pricing.Embedded().With(o) drops every row a
// catalog-eligible route claims (and every route entry with them), so a
// model `fugaro validate` and `fugaro budget prices` price from the
// catalogue becomes unpriced in a run whose node carries any model_prices
// at all — CheckPins then refuses a pin the owner has, and the gateway
// refuses the call it should charge.
//
// The catalogue here is the shipped one and the route is the shape
// providerRun builds (an openrouter route claiming deepseek/*), written
// inline because this is the table the run builds, not a run.
//
// Mutation (run, restore): return pricing.Embedded().With(o) from
// policyPrices again and the catalogue model is not priced by the node's
// table (the fixture check above the assertion is what says the row is
// really the catalogue's, not an override).
func TestPolicyPricesKeepTheCatalogRows(t *testing.T) {
	const catalogModel, embeddedModel = "deepseek/deepseek-v4-flash", "claude-sonnet-5-5"
	providers := map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
	}}
	r := &run{d: Deps{Providers: providers}}
	// The job's own table (no node prices): the catalogue prices the model.
	env, err := r.priceTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env.Lookup(catalogModel); !ok {
		t.Fatalf("fixture: the route's model %s is not priced by the shipped catalogue", catalogModel)
	}
	// The node carries prices for a different model, an embedded one.
	in, out := 20.0, 10.0
	pp := &projectpolicy.Policy{Prices: map[string]projectpolicy.Price{
		embeddedModel: {InputPerM: &in, OutputPerM: &out},
	}}
	got, err := r.policyPrices(pp)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Lookup(catalogModel); !ok {
		t.Errorf("the node's price table has no row for %s: the catalogue's rows and routes were dropped, so the run cannot price a model the owner pinned", catalogModel)
	}
	if m, ok := got.Lookup(embeddedModel); !ok || m.Rates.InputPerM != in || m.Rates.OutputPerM != out {
		t.Errorf("%s row = %+v, %v: the node's own override must still apply", embeddedModel, m, ok)
	}
	// A node with no model_prices at all is the job's own table.
	empty, err := r.policyPrices(&projectpolicy.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.Lookup(catalogModel); !ok {
		t.Error("a node without prices must leave the run on the job's own table")
	}
}
