package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// accountStagesRun is the least a run needs to answer which stages a
// stage-scoped Claude OAuth account serves (resolveMixedStages): the auth it
// resolved, the models its stages run on, the providers that claim them, and
// the published node. No gateway, no session: the question is the resolution's
// own.
func accountStagesRun(auth string, models config.ModelRoles, providers map[string]config.ModelProvider, stages map[string]string) *run {
	r := &run{
		cfg: &config.Config{Agent: config.Agent{Auth: auth, Models: models}},
		d:   Deps{Providers: providers, Log: slog.New(slog.DiscardHandler)},
	}
	r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
		Version: projectpolicy.Version, ClaudeAccountStages: stages}}
	return r
}

// TestResolveMixedStagesOnlyUnderTheRunsOwnKey: the mix exists to give an
// api-key run a Claude review credential it otherwise has none of, and it is
// that run's alone (docs/design/mixed-credentials.md §11). Under every other
// auth — an unknown one included, which the run judges with the auth it
// resolved — a node naming a stage account resolves to no stage at all: the
// keys keep whatever meaning that auth gives them and no stage of the run
// runs on the account. The refusals those modes carry are config's and the
// launcher's (config.CheckStageAccountMix); the run's own answer is that
// nothing is served.
//
// The mutation this fails on is inverting or dropping the auth arm: a run that
// resolved the keys whatever its auth would put an account's token into a
// stage of a run that must not hold one.
func TestResolveMixedStagesOnlyUnderTheRunsOwnKey(t *testing.T) {
	stages := map[string]string{"review": "team-a"}
	models := config.ModelRoles{Coder: "vendor/model-a", Reviewer: "claude-opus-5"}
	for _, auth := range []string{"oauth", "vertex", config.AuthCascade, ""} {
		r := accountStagesRun(auth, models, nil, stages)
		got, err := r.resolveMixedStages(context.Background())
		if err != nil {
			t.Errorf("auth %q: resolveMixedStages = %v", auth, err)
			continue
		}
		if len(got) != 0 {
			t.Errorf("auth %q: the run resolved stage accounts %v, want none", auth, got)
		}
	}
	// The admitted run does resolve it, so the loop above is not passing
	// because the fixture resolves nothing at all.
	r := accountStagesRun("api-key", models, nil, stages)
	got, err := r.resolveMixedStages(context.Background())
	if err != nil || got["review"] != "team-a" {
		t.Fatalf("the api-key run resolved %v, %v, want the node's account for review", got, err)
	}
}

// TestResolveMixedStagesRefusesAProviderServedStageModel: a stage the account
// serves goes straight to Anthropic on the account's token, so a stage whose
// model a provider route claims would be handed to Anthropic under a model
// Anthropic does not serve — an unknown model at best, and at worst another
// provider's own model ID. It is refused before any model call, the way a
// stage account the launch refuses is (docs/design/mixed-credentials.md §11,
// "A review stage that fails"): never silently sent, and never quietly run on
// the gateway's credential instead.
func TestResolveMixedStagesRefusesAProviderServedStageModel(t *testing.T) {
	providers := map[string]config.ModelProvider{
		"acme": {Kind: config.ModelProviderKind, Models: []string{"vendor/*"}},
	}
	r := accountStagesRun("api-key",
		config.ModelRoles{Coder: "vendor/model-a", Reviewer: "vendor/model-b"},
		providers, map[string]string{"review": "team-a"})
	got, err := r.resolveMixedStages(context.Background())
	if err == nil {
		t.Fatalf("a stage the account serves on a provider-claimed model was accepted: %v", got)
	}
	for _, want := range []string{"review", "vendor/model-b", "acme"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The coder's own provider model is not the stage's: the same providers
	// with a Claude reviewer resolve, so the refusal is about the stage's
	// model and not about the run having a route at all.
	r = accountStagesRun("api-key",
		config.ModelRoles{Coder: "vendor/model-a", Reviewer: "claude-opus-5"},
		providers, map[string]string{"review": "team-a"})
	if got, err := r.resolveMixedStages(context.Background()); err != nil || got["review"] != "team-a" {
		t.Fatalf("a Claude reviewer beside a provider coder resolved %v, %v, want the account for review", got, err)
	}
	// And a node naming nothing resolves nothing, whatever the models are.
	r = accountStagesRun("api-key",
		config.ModelRoles{Coder: "vendor/model-a", Reviewer: "vendor/model-b"}, providers, nil)
	if got, err := r.resolveMixedStages(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("a node naming no stage account resolved %v, %v, want none", got, err)
	}
}

// rtdbMicros is a whole micro-dollar leaf of a fake database, 0 where nothing
// was written.
func rtdbMicros(t *testing.T, db *gcpfake.RTDB, path string) int64 {
	t.Helper()
	switch v := db.Value(path).(type) {
	case nil:
		return 0
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			t.Fatalf("%s = %v", path, v)
		}
		return n
	}
	t.Fatalf("%s = %#v", path, db.Value(path))
	return 0
}

// TestReportNotionalIsTheAccountStagesAlone is Review Focus 9: a mixed run's
// account stage reports its notional dollars to the backend although the
// gateway is on, and its gateway stages do not report anything -- the
// ledger's figures are the ledger's to report.
//
// The gate is per stage and not per run, which is the whole change: written
// against the run (today's `r.gatewayOn()`, true for every mixed run) the
// account stage's figure is dropped and the counters the dashboard and
// `fugaro budget show` read never see it. Inverted the other way -- the
// predicate unnegated -- it returns early for exactly the stage that must
// report. Both are the mutation this test fails on.
func TestReportNotionalIsTheAccountStagesAlone(t *testing.T) {
	const accountUSD = 0.42
	db := gcpfake.NewRTDB(t)
	r := svcRun(t, "api-key", func(r *run) {
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Status: runstore.StatusRunning}
		r.spend = Spend{Mode: "enforce", Prices: pricing.Embedded()}
		r.sess = svcSessionOn(t, db)
	})
	// The stages the account serves, as bootstrap resolves them.
	r.mixedStages = map[string]string{"review": "team-a"}
	today := budget.Day(time.Now())
	runLeaf := budget.PathRun(svcSlug, svcRunID) + "/notional"
	repoLeaf := budget.PathSpendRepo(today, svcSlug) + "/notional"
	globalLeaf := budget.PathSpendGlobal(today) + "/notional"
	leaves := func() [3]int64 {
		return [3]int64{rtdbMicros(t, db, runLeaf), rtdbMicros(t, db, repoLeaf), rtdbMicros(t, db, globalLeaf)}
	}

	// A gateway stage: its dollars are the ledger's, and the run's own
	// bookkeeping takes them from there. Nothing reaches the notional counters.
	r.reportNotional(context.Background(), "implement", agent.Result{CostUSD: 1})
	if got := leaves(); got != [3]int64{0, 0, 0} {
		t.Fatalf("a gateway stage's figure reached the notional leaves %v (day %d): the ledger is what reports a gateway stage", got, today)
	}

	// The account's stage: nobody else reports its dollars, so they land on the
	// run's own leaf and on the repository's and project's counters.
	res := agent.Result{CostUSD: accountUSD,
		ModelUsage: map[string]agent.Usage{"claude-opus-5": {Output: 1000}}}
	r.reportNotional(context.Background(), "review", res)
	want, err := pricing.FromUSD(accountUSD)
	if err != nil {
		t.Fatal(err)
	}
	if got := leaves(); got != [3]int64{int64(want), int64(want), int64(want)} {
		t.Fatalf("the account stage's notional leaves are %v, want %d each", got, want)
	}
}
