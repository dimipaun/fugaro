package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
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

// TestPolicyKeyPathGovernance: a governance key is reported under the
// governance: block of fugaro.yaml, which is where it is written, and not
// under budget:, which is where the cost and model policy lives. Both readers
// of the path — the ignored-key warning a run logs (policyRecord) and the
// problem fugaro validate annotates (internal/cli/validate.go) — name a line
// of the file, so a governance key sent to budget.<key> would send its
// operator to a block that cannot carry it.
//
// A tier's own key is one of those governance keys: the merge spells it
// "tiers.<tier>.<key>" (policy.TierKey, T8.1), which is a fugaro.yaml path
// only once governance. is in front of it. The tier loop below walks the
// vocabulary, so a tier added to internal/tier cannot silently fall through
// to budget.tiers.<tier>.<key>.
func TestPolicyKeyPathGovernance(t *testing.T) {
	for _, key := range []string{
		policy.KeyReviewRatio, policy.KeyReviewFloorCalls, policy.KeyMaxSubagents,
		policy.KeyStageBudgetImplement, policy.KeyStageBudgetReview, policy.KeyStageBudgetFix,
		policy.KeyStageTokensImplement, policy.KeyStageTokensReview, policy.KeyStageTokensFix,
		policy.KeyStageTimeoutImplement, policy.KeyStageTimeoutReview, policy.KeyStageTimeoutFix,
	} {
		if got := PolicyKeyPath(key); got != "governance."+key {
			t.Errorf("PolicyKeyPath(%q) = %q, want governance.%s", key, got, key)
		}
	}
	for _, tt := range tier.Tiers {
		for _, key := range []string{policy.KeyExpectedUSD, policy.KeyExpectedMinutes, policy.KeyMaxSubagents} {
			want := "governance." + policy.TierKey(tt, key)
			if got := PolicyKeyPath(policy.TierKey(tt, key)); got != want {
				t.Errorf("PolicyKeyPath(%q) = %q, want %q", policy.TierKey(tt, key), got, want)
			}
		}
	}
	for _, c := range []struct{ key, want string }{
		{policy.KeyPerRunUSD, "budget.per_run_usd"},
		{policy.KeyMode, "budget.mode"},
		{policy.KeyAllowedModels, "budget.allowed_models"},
		{policy.KeyMaxRunTokens, "agent.max_run_tokens"},
		{policy.KeyMaxOutputCoder, "agent.max_output_tokens.coder"},
		{policy.KeyMaxOutputReviewer, "agent.max_output_tokens.reviewer"},
		// The triage switch (D1854): the node's own member is the repository's
		// agent.triage, so a key left to the fall-through would report the
		// ignored value under budget.triage, a key no file has, and the
		// operator it is meant to send to agent.triage would look in a block
		// that cannot carry it.
		{policy.KeyTriage, "agent.triage"},
	} {
		if got := PolicyKeyPath(c.key); got != c.want {
			t.Errorf("PolicyKeyPath(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestPolicyKeyPathCraft: a craft key is reported under the agent.crafts block
// of fugaro.yaml, which is where a craft's access is written, and not under
// budget., where neither a craft nor a credential lives. The merge spells the
// block's keys under crafts. (policy.CraftAccessKey, CraftCredentialKey,
// CraftPoolKey), and both readers of the path name a line of the file — the
// ignored-key warning a run logs and the problem `fugaro validate` annotates —
// so a craft key left to the fall-through would send an operator to a block
// that cannot carry it. The crafts are walked rather than spelled, the way the
// tier loop above is: a craft added to internal/config cannot silently fall
// through to budget.crafts.<craft>.access.
func TestPolicyKeyPathCraft(t *testing.T) {
	for _, key := range []string{
		policy.CraftCredentialKey("openrouter-main"),
		policy.CraftPoolKey("claude"),
		policy.CraftPoolMembersKey("claude"),
		policy.CraftPoolStrategyKey("claude"),
	} {
		if got, want := PolicyKeyPath(key), "agent."+key; got != want {
			t.Errorf("PolicyKeyPath(%q) = %q, want %q", key, got, want)
		}
	}
	for _, name := range config.CraftNames {
		key := policy.CraftAccessKey(name)
		if got, want := PolicyKeyPath(key), "agent."+key; got != want {
			t.Errorf("PolicyKeyPath(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestPolicyRecordCraftIgnoredNamesALayer: every entry of the record's
// ignored[] names a layer, the craft merge's own included. A credential, a
// pool or a craft's access a later layer may not introduce (F12, D1628) has no
// effective value, but it still answers to the project's own layer — the one
// that creates credentials and pools — and policyRecord copies the merge's
// source verbatim into result.json, whose schema pins the field to the four
// layer names (schemas/result.schema.json, schemas_test's enum table). A merge
// that answered "" here would write a record the repository's own validator
// refuses.
//
// A pool's two keys answer differently: their effective value is the one in
// force, so a strategy the project's pool leaves unnamed is recorded as the
// failover it is (poolStrategyText) — the reader of a record tells a looser
// value from a name nothing makes available by that field alone, and a
// strategy that lost to the default would otherwise read as a credential the
// project does not define.
func TestPolicyRecordCraftIgnoredNamesALayer(t *testing.T) {
	project := policy.Layer{Craft: policy.CraftLayer{
		Credentials: []string{"openrouter-main"},
		Pools:       map[string]policy.CraftPool{"claude": {Members: []string{"openrouter-main"}}},
		Access:      map[string]string{config.CraftCoder: "openrouter-main"},
	}}
	file := policy.Layer{Craft: policy.CraftLayer{
		Credentials: []string{"repo-key"},
		Pools: map[string]policy.CraftPool{
			"repo-pool": {Members: []string{"openrouter-main"}},
			"claude":    {Strategy: policy.PoolStrategyRoundRobin},
		},
		Access: map[string]string{config.CraftCoder: "repo-key",
			config.CraftSeniorReview: "repo-pool"},
	}}
	rec := policyRecord(policy.Merge(project, file), 0)
	if rec == nil || len(rec.Ignored) == 0 {
		t.Fatalf("the repository's refused craft values were not recorded: %+v", rec)
	}
	layers := []string{policy.SourceCeiling, policy.SourcePolicy, policy.SourceDefaultBranch, policy.SourceBranch}
	for _, ig := range rec.Ignored {
		if !slices.Contains(layers, ig.Source) {
			t.Errorf("ignored %s: source %q is not one of %v, so result.json would fail its own schema", ig.Key, ig.Source, layers)
		}
	}
	// The strategy the project's pool never named, refused: the entry names the
	// layer that answers for it and carries failover, the value the run would
	// use.
	strategy := slices.IndexFunc(rec.Ignored, func(ig runstore.PolicyIgnored) bool {
		return ig.Key == policy.CraftPoolStrategyKey("claude")
	})
	if strategy < 0 {
		t.Fatalf("the refused strategy was not recorded: %+v", rec.Ignored)
	}
	if ig := rec.Ignored[strategy]; ig.Source != policy.SourceCeiling || ig.Effective != policy.PoolStrategyFailover {
		t.Errorf("ignored %s = %+v, want the project's own failover", ig.Key, ig)
	}
}

// TestRunnerReadsReviewFloorFromNode is D722's job half, and the trust
// boundary it closes (G1): the floor is read from the owner-only policy node
// — the one object the launcher cannot write — and from nowhere else, so a
// hostile task or project layer cannot turn review off below the owner's
// line. The read is the node read resolvePolicy already makes; the floor
// rides the ceiling layer from there (policyCeiling) and the merge unions it
// into the effective policy, which is what L3.1's resolver will consume.
func TestRunnerReadsReviewFloorFromNode(t *testing.T) {
	node := &projectpolicy.Policy{ReviewFloor: map[rigor.Mode][]policy.Level{
		rigor.Pro:      {policy.LevelL2},
		rigor.Critical: {policy.LevelL2, policy.LevelL3},
	}}
	r := &run{
		cfg:  &config.Config{},
		spec: &task.Spec{},
		rec:  &runstore.Record{},
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
	}
	r.projPolicy = policyRead{done: true, policy: node, found: true}
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := r.policy.ReviewFloor(rigor.Pro), []policy.Level{policy.LevelL2}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor(pro) = %v, want %v: the floor the node carries must reach the effective policy", got, want)
	}
	if got, want := r.policy.ReviewFloor(rigor.Critical), []policy.Level{policy.LevelL2, policy.LevelL3}; !slices.Equal(got, want) {
		t.Errorf("ReviewFloor(critical) = %v, want %v", got, want)
	}
	if got := r.policy.ReviewFloor(rigor.POC); got != nil {
		t.Errorf("ReviewFloor(poc) = %v, want nil: the node sets no floor for that mode", got)
	}

	// A run whose project has no policy node at all — no floor to read — has
	// no floor, which is the state every installation is in until an owner
	// publishes one.
	none := &run{
		cfg:  &config.Config{},
		spec: &task.Spec{},
		rec:  &runstore.Record{},
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
	}
	none.projPolicy = policyRead{done: true, found: false}
	if err := none.resolvePolicy(context.Background(), none.cfg); err != nil {
		t.Fatal(err)
	}
	if got := none.policy.ReviewFloor(rigor.Pro); got != nil {
		t.Errorf("ReviewFloor(pro) = %v with no policy node, want nil", got)
	}
}

// TestPolicyCeilingCarriesTheBinariesList is D986's read half, beside the
// floor's: the binary allow rule is the owner-only policy node's, the run
// reads it at bootstrap and puts it on the ceiling layer (policyCeiling), and
// the merge is what lets a layer under the ceiling remove an admitted path and
// never add one. The node's own decoder is the plan's AR0 and is not here yet
// (projectpolicy.Binaries), so the policies below are built by hand — which is
// the state this read has until that task lands: nothing populates the field,
// and every run is the node-with-no-block case. The layers under it are the
// ones a launcher writes, so the node's list is the only list there is — and a
// node that names none, or no node at all, admits no binary, which is the
// behaviour of every installation that has published no binaries block.
func TestPolicyCeilingCarriesTheBinariesList(t *testing.T) {
	newRun := func(p *projectpolicy.Policy, found bool) *run {
		r := &run{
			cfg:  &config.Config{},
			spec: &task.Spec{},
			rec:  &runstore.Record{},
			d:    Deps{Log: slog.New(slog.DiscardHandler)},
		}
		r.projPolicy = policyRead{done: true, policy: p, found: found}
		return r
	}
	const brand, site = "brand/*.png", "website/public/**"
	node := &projectpolicy.Policy{Binaries: &projectpolicy.Binaries{Allow: []string{brand, site}, MaxBytes: 1048576}}
	r := newRun(node, true)
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := r.policy.BinariesAllow, []string{brand, site}; !slices.Equal(got, want) {
		t.Errorf("BinariesAllow = %v, want %v: the list the node carries must reach the effective policy", got, want)
	}
	if r.policy.BinariesMaxBytes != 1048576 {
		t.Errorf("BinariesMaxBytes = %d, want the node's 1048576", r.policy.BinariesMaxBytes)
	}
	// The node is the layer the record must name, not the job environment,
	// which can carry no binaries at all.
	if got := r.policy.Sources[policy.KeyBinariesAllow]; got != policy.SourcePolicy {
		t.Errorf("sources[%s] = %q, want %q", policy.KeyBinariesAllow, got, policy.SourcePolicy)
	}

	for _, tt := range []struct {
		name  string
		p     *projectpolicy.Policy
		found bool
	}{
		{"a node with no binaries block", &projectpolicy.Policy{}, true},
		{"no policy node at all", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRun(tt.p, tt.found)
			if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
				t.Fatal(err)
			}
			if r.policy.BinariesAllow != nil || r.policy.BinariesMaxBytes != 0 {
				t.Errorf("the effective policy admits binaries: %v, %d", r.policy.BinariesAllow, r.policy.BinariesMaxBytes)
			}
		})
	}

	// The empty list the node wrote is the node's list, and result.json has to
	// say so: `binaries: {allow: []}` is D987's spelling for a list the owner
	// cleared, and the merge reads it as the first list there is — which, left
	// unsourced, would name the job environment, the one layer that can carry
	// no binaries at all (fugaro.yaml has no such key).
	cleared := newRun(&projectpolicy.Policy{Binaries: &projectpolicy.Binaries{Allow: []string{}}}, true)
	if err := cleared.resolvePolicy(context.Background(), cleared.cfg); err != nil {
		t.Fatal(err)
	}
	if got := cleared.policy.BinariesAllow; got == nil || len(got) != 0 {
		t.Errorf("BinariesAllow = %v, want the empty list the node wrote", got)
	}
	if got := cleared.policy.Sources[policy.KeyBinariesAllow]; got != policy.SourcePolicy {
		t.Errorf("sources[%s] = %q, want %q: the node wrote the empty list, so the environment cannot be its source",
			policy.KeyBinariesAllow, got, policy.SourcePolicy)
	}
	if rec := policyRecord(cleared.policy, 0); rec == nil || rec.Sources[policy.KeyBinariesAllow] != policy.SourcePolicy {
		t.Errorf("result.json's policy.sources = %+v, want %s = %q", rec, policy.KeyBinariesAllow, policy.SourcePolicy)
	}
}

// ref is a pointer to v: the four chain limits a task carries are pointers,
// and a nil one is a dimension the launch left unbounded (task.Overrides).
func ref[T any](v T) *T { return &v }

// chainAround writes a chain of two runs into bucket — the one that began it,
// whose stored task is the chain's own limit and whose record is what it
// spent (D1041, D1046), and the one this test resolves the policy of, linked
// to it — and returns the run, ready for resolvePolicy. Its store is the
// bucket, which is the whole point of the read: one record at a time, by
// name (F21).
func chainAround(t *testing.T, bucket *blob.Bucket) *run {
	t.Helper()
	ctx := context.Background()
	const root = "20261001-090000-abcd"
	rs := runstore.Open(bucket, svcSlug, root)
	if err := rs.WriteTask(ctx, &task.Spec{Version: 1, RunID: root, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Begin the work", Overrides: task.Overrides{MaxReviewRounds: ref(6), MaxTime: "1h"}}); err != nil {
		t.Fatal(err)
	}
	if err := rs.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: root, Repo: "acme/app", Status: runstore.StatusSucceeded,
		Reviews: []runstore.ReviewSummary{{Round: 1}, {Round: 2}, {Round: 3}, {Round: 4}, {Round: 5}},
		Stages:  []runstore.StageTiming{{Name: "implement", DurationS: 2400}}}); err != nil {
		t.Fatal(err)
	}
	spec := &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Carry on",
		Branch: "fugaro/" + root, PR: 1, PreviousRun: root,
		Overrides: task.Overrides{MaxReviewRounds: ref(9), MaxTime: "3h"}}
	r := &run{cfg: &config.Config{}, spec: spec, rec: &runstore.Record{},
		d: Deps{Store: runstore.Open(bucket, svcSlug, svcRunID), Log: slog.New(slog.DiscardHandler), Now: time.Now}}
	r.projPolicy = policyRead{done: true}
	return r
}

// TestPolicyChainRemainingCannotLoosen: the chain is the merge's last layer,
// so a hand-written task's looser figure cannot survive it (D1044, D1047).
// The task asks for 9 review rounds and a 3-hour wall clock, the run that
// began the chain was launched with 6 rounds and 1 hour, and its record says
// it spent 5 rounds and 40 minutes, so what is left is 1 round and 20 minutes
// — and neither the task's 9 nor its 3 hours is what the run carries.
func TestPolicyChainRemainingCannotLoosen(t *testing.T) {
	bucket := memblob.OpenBucket(nil)
	defer bucket.Close()
	r := chainAround(t, bucket)
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatal(err)
	}
	if got := r.policy.MaxReviewRounds; got != 1 {
		t.Errorf("max_review_rounds = %d, want the chain's 1 (its 6 less the 5 spent), not the task's 9", got)
	}
	if got := r.policy.MaxRunTime; got != 20*time.Minute {
		t.Errorf("max_run_time = %v, want the chain's 20m (its 1h less the 40m spent), not the task's 3h", got)
	}
	for _, key := range []string{policy.KeyMaxReviewRounds, policy.KeyMaxRunTime} {
		if got := r.policy.Sources[key]; got != policy.SourceChain {
			t.Errorf("sources[%s] = %q, want %q", key, got, policy.SourceChain)
		}
	}
}

// TestChainRecordsAreReadByNameNotListed: the chain is followed one record at
// a time, by name (F21): the job's account holds storage.objects.get on its
// own prefix and no list, so a bucket that answers every listing with 403
// still resolves the chain, and the fake's log of the calls shows not one of
// them was a listing.
func TestChainRecordsAreReadByNameNotListed(t *testing.T) {
	const runsBucket = "fugaro-runs-proj-1234"
	g := gcpfake.NewGCS(t)
	bucket := g.Bucket(t, runsBucket).Bucket
	g.FailListings(runsBucket)
	r := chainAround(t, bucket)
	before := len(g.Requests())
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatal(err)
	}
	if got := r.policy.MaxReviewRounds; got != 1 {
		t.Fatalf("max_review_rounds = %d, want the chain's 1: the records behind the link were not read", got)
	}
	reqs := g.Requests()
	for _, req := range reqs[min(before, len(reqs)):] {
		if strings.Contains(req.Query, "prefix=") || strings.Contains(req.Query, "delimiter=") {
			t.Errorf("the chain was listed: %s %s?%s", req.Method, req.Path, req.Query)
		}
	}
}
