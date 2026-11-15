package runner

// The core half of plan T7.2: the gateway stage-limit hook's default and the
// wiring from the config the run resolved to the hook. The limits themselves
// are ee/governance's and are tested there, against the gateway.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// stopAllLimit is a stage limit that stops the first call of every stage, with
// the sentence the builder that made it was handed: the wiring test's own
// limit, which is how the figures a run resolved become visible in a refusal
// without an ee/ build.
type stopAllLimit struct{ detail string }

func (l stopAllLimit) Begin(gateway.StageLimitView) {}

func (l stopAllLimit) Stop(gateway.StageLimitView) gateway.StageStop {
	return gateway.StageStop{Reason: string(runstore.HaltStageBudget), Detail: l.detail}
}

// TestStageLimitHookDefaultsToNil is the core default of the fourth seam
// (T7.2): a build that installs no builder hands the gateway no stage limit at
// all, which is exactly the gateway of today — the run's spend cap, the
// lease's sub-cap and the stage's pinned models and no per-stage budget. The
// two refusals beside it are what keeps a tagged binary's limits from being
// replaced or switched off (design ee-boundary-moves.md §5.5).
func TestStageLimitHookDefaultsToNil(t *testing.T) {
	ResetGatewayStageLimitHook(t)
	r := &run{spec: &task.Spec{Repo: "acme/app"}, spend: Spend{Mode: "observe", Cap: 20_000_000},
		d: Deps{Log: slog.New(slog.DiscardHandler)}}

	if got := r.gatewayStageLimit(); got != nil {
		t.Fatalf("a build that installed no builder handed the gateway %#v, want nil: an untagged build enforces no per-stage budget (D522)", got)
	}
	if err := SetGatewayStageLimitHook(nil); err == nil {
		t.Error("a nil builder was accepted: a seam set to one would mean \"this build enforces nothing\", which is what not calling the setter already says")
	}
	installed := func(g policy.Governance, tt tier.Tier, cap pricing.Micros) gateway.StageLimit {
		return stopAllLimit{detail: "installed"}
	}
	if err := SetGatewayStageLimitHook(installed); err != nil {
		t.Fatalf("the first SetGatewayStageLimit was refused: %v", err)
	}
	if err := SetGatewayStageLimitHook(installed); err == nil {
		t.Error("a second SetGatewayStageLimit was accepted: a builder that can be replaced after ee/register set one is a way to drop a tagged binary's stage budgets")
	}
	if got := r.gatewayStageLimit(); got == nil {
		t.Fatal("the installed builder did not reach the run: the gateway would enforce no stage budget")
	}
}

// TestStageLimitsWiredFromConfig is the wiring half (T7.2): the governance the
// run's own layers resolved, the tier the task was dispatched at and the run's
// per-run cap reach the gateway's stage limit, and the limit the run's
// startGateway installs is the one its calls are judged by. The limits
// themselves are ee/governance's; what is pinned here is that a run's
// configuration gets to them at all.
func TestStageLimitsWiredFromConfig(t *testing.T) {
	ResetGatewayStageLimitHook(t)
	var (
		calls  int
		gotGov policy.Governance
		gotT   tier.Tier
		gotCap pricing.Micros
	)
	SetGatewayStageLimitHook(func(g policy.Governance, tt tier.Tier, cap pricing.Micros) gateway.StageLimit {
		calls++
		gotGov, gotT, gotCap = g, tt, cap
		return stopAllLimit{detail: fmt.Sprintf("stage budget: review_ratio=%d max_subagents=%d tier=%s cap_micros=%d",
			g.ReviewRatio, g.MaxSubagents, tt, int64(cap))}
	})

	r := svcServedRunWith(t, func() (http.Header, pricing.Usage) {
		return http.Header{gateway.HeaderTier: []string{gateway.TierA}}, pricing.Usage{Input: 1000, Output: 3}
	}, func(r *run) {
		r.rec = &runstore.Record{Version: 1, RunID: svcRunID, Repo: "acme/app", Status: runstore.StatusRunning}
		r.spec = &task.Spec{Repo: "acme/app", Tier: tier.Small}
		r.d.Spend = Spend{Mode: "observe", Cap: 20_000_000, Prices: pricing.Embedded()}
		// The repository's own governance: block, as the run's file layer
		// carries it. This test binary links no ee/ package, so the merge
		// tightens no governance key and the block alone resolves to zero —
		// what a tagged build resolves from it is planted below.
		r.cfg.Layer = &config.ProjectLayer{Governance: &config.Governance{ReviewRatio: 3, ReviewFloorCalls: 4, MaxSubagents: 2}}
		// A project that turns the cascade on, which a cascade run is admitted
		// by (O2) and whose allow-list admits the model the call below names.
		r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
			Routing: &config.Routing{Claude: &config.RoutingClaude{Via: config.RouteCascade}},
			Budget:  projectpolicy.Budget{Mode: policy.ModeObserve, AllowedModels: []string{"claude-sonnet-5-5"}},
		}}
	})
	if err := r.resolvePolicy(context.Background(), r.cfg); err != nil {
		t.Fatal(err)
	}
	// The governance a *tagged* build resolves from that block is ee/'s: it is
	// the policy hook's Tighten that merges the keys, and this binary's core
	// default tightens nothing (D522), so the block above came back as zero.
	// What is under test here is the hand-off and not the merge, so the value
	// a tagged build would have resolved is planted on the run's own policy,
	// which is where startGateway reads it from.
	r.policy.Governance = policy.Governance{ReviewRatio: 3, ReviewFloorCalls: 4, MaxSubagents: 2}
	// The session is the identity a cascade run is admitted by: the service
	// verifies this run by the identity the budget backend admitted it under.
	r.sess = svcSession(t)
	startServedGateway(t, r)

	code, body := svcPost(t, r)
	if code != http.StatusForbidden {
		t.Fatalf("the stage's call was answered %d: %s\nwant 403 with the limit's own sentence", code, body)
	}
	if calls != 1 {
		t.Fatalf("the installed builder ran %d times for one run, want once: the limits are the run's, resolved once at start", calls)
	}
	if gotGov.ReviewRatio != 3 || gotGov.ReviewFloorCalls != 4 || gotGov.MaxSubagents != 2 {
		t.Errorf("the builder was handed %+v, want the governance the run's project layer resolved (review_ratio 3, review_floor_calls 4, max_subagents 2)", gotGov)
	}
	if gotT != tier.Small {
		t.Errorf("the builder was handed tier %q, want the task's own %q", gotT, tier.Small)
	}
	if gotCap != r.spend.Cap || gotCap <= 0 {
		t.Errorf("the builder was handed cap %d, want the run's own %d (a stage budget is a fraction of it, so the two have to be the same figure)", gotCap, r.spend.Cap)
	}
	// The run reads its own sentence back out of the refusal the agent got,
	// which is the whole of what the wiring has to make true: the figures the
	// run resolved are the figures its calls are judged by.
	for _, want := range []string{"review_ratio=3", "tier=small", fmt.Sprintf("cap_micros=%d", int64(r.spend.Cap))} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal body does not carry %q: %s", want, body)
		}
	}
}
