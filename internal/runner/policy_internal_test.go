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
	"github.com/dimipaun/fugaro/internal/pricing"
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

// The policy's model_prices is the owner's override list, and the table it is
// applied to is the run's own (r.priceTable → config.PriceTable over the job's
// FUGARO_MODEL_PROVIDERS routes and the built-in catalog). Building it as
// pricing.Embedded().With(o) instead does more than replace the model_prices
// key: it drops every row a catalog-eligible route claims, and an override
// naming such a model materializes it with its limits cleared, so the gateway
// falls back to DefaultMaxOutputTokens (1e6) and stops refusing a max_tokens
// above the model's real maximum.
//
// This is the strict half of the pair TestPolicyCeilingKeepsTheJobsRoutesAndCatalog
// covers (that one's limit check passes when an override kept ContextTokens and
// cleared MaxOutputTokens, which is exactly the fallback above): what this
// asserts is that MaxOutputTokens itself is the catalog's.
func TestPolicyPricesBuildOnTheRunsOwnTable(t *testing.T) {
	const ds = "deepseek/deepseek-v4-flash"
	cat, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := cat.Models[ds]
	if !ok || entry.MaxOutputTokens <= 0 {
		t.Fatalf("the catalog prices %s with no output limit: %+v", ds, entry)
	}
	// A route whose base URL the catalog may price, claiming the model.
	r := &run{d: Deps{Providers: map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api/v1",
		Auth: "bearer", Secret: "openrouter-api-key", Models: []string{"deepseek/*"},
	}}}}
	price := func(in, out float64) projectpolicy.Price {
		return projectpolicy.Price{InputPerM: &in, OutputPerM: &out}
	}
	// priced is the ceiling's table for a node carrying one model_prices entry,
	// read through the same call the run's bootstrap makes.
	priced := func(id string, p projectpolicy.Price) *pricing.Table {
		t.Helper()
		_, spend, _, err := r.policyCeiling(&projectpolicy.Policy{Prices: map[string]projectpolicy.Price{id: p}}, envCeiling(Spend{}, ""), Spend{})
		if err != nil {
			t.Fatalf("the policy's model_prices was refused: %v", err)
		}
		if spend.Prices == nil {
			t.Fatal("the ceiling carries no price table")
		}
		return spend.Prices
	}
	// An override for a model no route claims: the route's own row — the one
	// the run's pin is priced from — must survive it. Without that row the run
	// is refused at bootstrap ("the models can't be priced under the budget").
	if m, ok := priced("claude-sonnet-5-5", price(1, 2)).Lookup(ds); !ok {
		t.Fatal("the policy's model_prices dropped the job's own route-priced row, so nothing can price a run pinned to it")
	} else if m.MaxOutputTokens != entry.MaxOutputTokens {
		t.Errorf("%s: the policy's list changed the model's output maximum to %d, want the catalog's %d", ds, m.MaxOutputTokens, entry.MaxOutputTokens)
	}
	// An override naming the route's model: the override's rates win, and the
	// file's limits stay, so the ceiling on max_tokens is still the model's.
	m, ok := priced(ds, price(3, 4)).Lookup(ds)
	if !ok {
		t.Fatalf("the owner's override left %s with no row at all", ds)
	}
	if m.MaxOutputTokens != entry.MaxOutputTokens {
		t.Errorf("%s: the owner's override cleared the model's output maximum (%d, want %d), so the gateway would fall back to %d and stop refusing an oversized max_tokens",
			ds, m.MaxOutputTokens, entry.MaxOutputTokens, pricing.DefaultMaxOutputTokens)
	}
	if m.Rates.OutputPerM != 4 || m.Rates.InputPerM != 3 {
		t.Errorf("%s: the owner's override did not apply: %+v", ds, m.Rates)
	}
}
