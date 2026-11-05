package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// accountStagesRun is the least a run needs to answer which stages a
// stage-scoped Claude OAuth account serves (resolveMixedStages): the agent it
// resolved (its auth, the models its stages run on, the levels that pin them),
// the providers that claim them, the mode the run resolved, and the published
// node. No gateway, no session: the question is the resolution's own.
func accountStagesRun(agent config.Agent, providers map[string]config.ModelProvider, mode string, p *projectpolicy.Policy) *run {
	r := &run{
		cfg:   &config.Config{Agent: agent},
		d:     Deps{Providers: providers, Log: slog.New(slog.DiscardHandler)},
		spend: Spend{Mode: mode},
	}
	if p != nil {
		p.Version = projectpolicy.Version
		r.projPolicy = policyRead{done: true, found: true, policy: p}
	}
	return r
}

// mixedStagesNode is a published node that declares the mix one way or the
// other: the stage accounts named and the project's default, either of which
// may be absent.
func mixedStagesNode(stages map[string]string, def string) *projectpolicy.Policy {
	return &projectpolicy.Policy{ClaudeAccountStages: stages, DefaultClaudeAccount: def}
}

// mixAccountModels is the agent of the run the ruling admits: a provider-served
// coder, an Anthropic reviewer, and the two review levels pinning their own
// models — the shape every rung of the mix is judged on.
func mixAccountModels(coder, reviewer string) config.Agent {
	return config.Agent{
		Auth:   "api-key",
		Models: config.ModelRoles{Coder: coder, Reviewer: reviewer},
	}
}

// claudeAccountModels is mixAccountModels with an Anthropic model on the coding
// stage too: no provider route serves the coder, so the gateway holds no
// provider key for a Claude account to sit beside.
func claudeAccountModels(reviewer string) config.Agent {
	return mixAccountModels("claude-sonnet-5", reviewer)
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
	for _, auth := range []string{"oauth", "vertex", config.AuthCascade, ""} {
		r := accountStagesRun(mixAccountModels("vendor/model-a", "claude-opus-5"), nil, policy.ModeEnforce,
			mixedStagesNode(stages, ""))
		r.cfg.Agent.Auth = auth
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
	r := accountStagesRun(mixAccountModels("vendor/model-a", "claude-opus-5"), nil, policy.ModeEnforce,
		mixedStagesNode(stages, ""))
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
	r := accountStagesRun(mixAccountModels("vendor/model-a", "vendor/model-b"), providers, policy.ModeEnforce,
		mixedStagesNode(map[string]string{"review": "team-a"}, ""))
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
	r = accountStagesRun(mixAccountModels("vendor/model-a", "claude-opus-5"), providers, policy.ModeEnforce,
		mixedStagesNode(map[string]string{"review": "team-a"}, ""))
	if got, err := r.resolveMixedStages(context.Background()); err != nil || got["review"] != "team-a" {
		t.Fatalf("a Claude reviewer beside a provider coder resolved %v, %v, want the account for review", got, err)
	}
	// And a node naming nothing resolves nothing, whatever the models are.
	r = accountStagesRun(mixAccountModels("vendor/model-a", "vendor/model-b"), providers, policy.ModeEnforce,
		mixedStagesNode(nil, ""))
	if got, err := r.resolveMixedStages(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("a node naming no stage account resolved %v, %v, want none", got, err)
	}
}

// providerRoutes is the one route every rung-4 fixture's coder is pinned to:
// a provider the owner allowed every repository to send code to, which is what
// "the coder is served by a provider route this repository may use" means.
func providerRoutes() map[string]config.ModelProvider {
	return map[string]config.ModelProvider{
		"acme": {Kind: config.ModelProviderKind, Models: []string{"vendor/*"}, AllowDataTo: []string{"*"}},
	}
}

// pinnedAgent is the agent of a run whose two review levels pin their own
// models: the levels a stage's own model is read off (stageModel), for the one
// condition rung 4 attaches to a stage.
func pinnedAgent(coder, l2, l3 string) config.Agent {
	a := mixAccountModels(coder, "claude-opus-5")
	a.Levels.L2 = &config.LevelSetting{Model: l2}
	a.Levels.L3 = &config.LevelSetting{Model: l3}
	return a
}

// TestResolveMixedStagesFollowsRungFour is the runner's half of the owner's
// ruling of 2026-11-05 (docs/design/mixed-credentials.md §11): the project's
// default_claude_account opts an api-key run into the mix for the two stages of
// the allow-list, under the conditions the launch's mixedStageAccount applies —
// an Anthropic model on that stage, a coder a provider route this repository
// may use, and a mode the run resolves as enforce — and under no other.
//
// The runner has to make the same judgement the launch did: it reads the
// published node the launch read the local config *from*, and a rung the launch
// admits but the runner does not is a run whose review silently goes out on the
// gateway credential — the very fallback §11 refuses — while a rung the runner
// admits and the launch does not is a job with no token for the account.
func TestResolveMixedStagesFollowsRungFour(t *testing.T) {
	claude, vendor := "claude-opus-5", "vendor/model-b"
	for _, tc := range []struct {
		name   string
		agent  config.Agent
		mode   string
		def    string
		stages map[string]string
		want   map[string]string
	}{
		{
			name:  "the default names the mix's account",
			agent: pinnedAgent("vendor/model-a", claude, claude), mode: policy.ModeEnforce,
			def:  "team-a",
			want: map[string]string{"review": "team-a", "adversarial": "team-a"},
		},
		{
			name:  "a stage whose own model is not Anthropic takes no account",
			agent: pinnedAgent("vendor/model-a", vendor, claude), mode: policy.ModeEnforce,
			def:  "team-a",
			want: map[string]string{"adversarial": "team-a"},
		},
		{
			name:  "the sentinel default declares no mix",
			agent: pinnedAgent("vendor/model-a", claude, claude), mode: policy.ModeEnforce,
			def:  config.DefaultClaudeAccount,
			want: nil,
		},
		{
			name:  "a coder no provider route serves has no provider key to mix",
			agent: pinnedAgent("claude-sonnet-5", claude, claude), mode: policy.ModeEnforce,
			def:  "team-a",
			want: nil,
		},
		{
			name:  "a mode that routes no call to a provider",
			agent: pinnedAgent("vendor/model-a", claude, claude), mode: policy.ModeObserve,
			def:  "team-a",
			want: nil,
		},
		{
			name:  "rung 2 is served as written and wins over the default",
			agent: pinnedAgent("vendor/model-a", claude, vendor), mode: policy.ModeEnforce,
			def: "team-a", stages: map[string]string{"review": "team-b"},
			want: map[string]string{"review": "team-b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := accountStagesRun(tc.agent, providerRoutes(), tc.mode, mixedStagesNode(tc.stages, tc.def))
			got, err := r.resolveMixedStages(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("resolveMixedStages = %v, want %v", got, tc.want)
			}
		})
	}
	// The one-account rule (design §11, "never a second provider's key"): a
	// project default that names a *different* account from the one the other
	// stage's own key names is a second credential, and it is refused rather
	// than honoured — with both rungs naming the same account it is the same
	// run the ruling admits, which is the control.
	r := accountStagesRun(pinnedAgent("vendor/model-a", claude, claude), providerRoutes(), policy.ModeEnforce,
		mixedStagesNode(map[string]string{"review": "team-b"}, "team-a"))
	got, err := r.resolveMixedStages(context.Background())
	if err == nil {
		t.Fatalf("a project default and a stage key naming two accounts were accepted: %v", got)
	}
	for _, want := range []string{"review", "team-b", "team-a"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	r = accountStagesRun(pinnedAgent("vendor/model-a", claude, claude), providerRoutes(), policy.ModeEnforce,
		mixedStagesNode(map[string]string{"review": "team-a"}, "team-a"))
	if got, err := r.resolveMixedStages(context.Background()); err != nil || got["review"] != "team-a" || got["adversarial"] != "team-a" {
		t.Fatalf("one account named by both rungs resolved %v, %v, want the two stages on it", got, err)
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

// TestReportNotionalStaysForOauthRuns is the other half of that gate, and the
// one it is easiest to take away while adding the account stage's. An oauth
// run has no gateway at all (GatewayOn: an oauth token never goes through
// one), so every stage of it is Claude Code's own, every figure is notional,
// and every stage reports -- exactly as it did before a mixed run existed.
//
// The mutation this fails on is the gate narrowed to the account stage alone
// (`!r.stageDirect(name)`), under which no stage of an oauth run reports
// anything: the run's, the repository's and the project's notional counters
// stay at zero for the subscription's whole spend, in silence. r.spend.Mode is
// "enforce" here to make the point that the mode is not what silences a stage:
// the gateway is, and only for the stages it serves.
func TestReportNotionalStaysForOauthRuns(t *testing.T) {
	const stageUSD = 0.42
	db := gcpfake.NewRTDB(t)
	r := svcRun(t, "oauth", func(r *run) {
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Status: runstore.StatusRunning}
		r.spend = Spend{Mode: "enforce", Prices: pricing.Embedded()}
		r.sess = svcSessionOn(t, db)
	})
	today := budget.Day(time.Now())
	r.reportNotional(context.Background(), "implement", agent.Result{
		CostUSD:    stageUSD,
		ModelUsage: map[string]agent.Usage{"claude-opus-5": {Output: 1000}},
	})
	want, err := pricing.FromUSD(stageUSD)
	if err != nil {
		t.Fatal(err)
	}
	got := [3]int64{
		rtdbMicros(t, db, budget.PathRun(svcSlug, svcRunID)+"/notional"),
		rtdbMicros(t, db, budget.PathSpendRepo(today, svcSlug)+"/notional"),
		rtdbMicros(t, db, budget.PathSpendGlobal(today)+"/notional"),
	}
	if got != [3]int64{int64(want), int64(want), int64(want)} {
		t.Fatalf("an oauth run's stage left its notional counters at %v, want %d each (day %d): the subscription paid the whole run", got, want, today)
	}
}

// TestAccountStageCostKeepsEarlierDollarsWithNoGateway pins the run with no
// gateway (budget.mode off, a Claude coder, a stage account on the review): the
// ledger's figures are zero there, so a figure written from them alone would
// replace what the implement stage had already cost.
func TestAccountStageCostKeepsEarlierDollarsWithNoGateway(t *testing.T) {
	r := svcRun(t, "api-key", func(r *run) {
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Status: runstore.StatusRunning, CostUSD: 1}
		r.gw = nil
	})
	r.mixedStages = map[string]string{"review": "team-a"}
	r.recordAccountStageCost("review", 0.42)
	if got := r.rec.CostUSD; got < 1.419 || got > 1.421 {
		t.Fatalf("cost_usd = %v after a $1.00 stage and a $0.42 account stage, want 1.42", got)
	}
}
