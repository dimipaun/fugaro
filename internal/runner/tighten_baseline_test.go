package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// R20's baseline as the run itself reads it (docs/repo-override-rulings, RT2's
// leftover). The two keys no mode row holds are agent.max_budget_usd, whose
// baseline is Fugaro's own config.DefaultMaxBudgetUSD, and
// agent.review_stop_fraction, whose baseline is
// config.DefaultReviewStopFraction (reconciliation.md R20): a value the project
// layer says nothing about is judged against those, and one that does not
// tighten is dropped, so a repository's own figure is not always the one its
// runs read.
//
// The cases below assert that through the run and not through the helpers: the
// stage cap every call is launched with (runner.go, yield.go), the checkpoint
// stage's own call (checkpoint_stage.go) and the share bootstrap hands the
// Budget (runner.go). A test that recomputed config.TightenUSD or
// config.TightenStopFraction would pin its own arithmetic and leave every reader
// free to stop reading the helper.

// silentProject re-homes the harness's run on a project layer that prices it and
// says nothing else: the models a run behind a gateway must name (CheckPins) and
// the mode the fixture's rigor: block moved to (testutil.FixturePins), with no
// tighten-only agent key stated — what a silent project is (R20, spec §5.2).
//
// The harness's own layer is never silent: withPins states the keys the
// fixture's file writes (testutil.SanctionAgent) so that a case about what a
// number does reads the number it wrote, and a case about the baseline has to
// write the layer itself. cfg is the file the harness was built with, which is
// the one its layer was lifted from.
func silentProject(t *testing.T, h *harness, cfg string) {
	t.Helper()
	_, layer := testutil.FixturePins(cfg, config.LiftedKeyPaths())
	if layer == "" {
		t.Fatal("the fixture lifted no key, so there is no layer to leave silent")
	}
	spec, err := h.store.ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec.ProjectLayer = &task.ProjectLayer{SHA256: config.LayerSum([]byte(layer)), YAML: layer}
	h.setSpec(t, spec)
}

// runAndBudget runs the harness's fixture and hands back the Budget the run's
// own bootstrap built, with the clock the test moves: the stop rule is asked at
// the instants below and never at the wall clock's.
func runAndBudget(t *testing.T, h *harness) (*runner.Budget, *testClock) {
	t.Helper()
	clk := newClock()
	h.deps.Now = clk.Now
	var handle *runner.RunHandle
	runner.OnRun(t, func(rh *runner.RunHandle) { handle = rh })
	// The mode's own row supplies the round count where the file's is unchecked
	// (R20): the loop may run two review rounds, so both are scripted.
	if _, err := h.run(t, implement("feature"), review("ship", 0), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if handle == nil {
		t.Fatal("the run never started")
	}
	b := handle.Budget()
	return &b, clk
}

// TestTheRunsBudgetTakesTheBaseline: the stop rule's time half as the run really
// reads it (R20, RT2). A repository's share the project layer says nothing about
// is judged against config.DefaultReviewStopFraction, so a file that wrote a
// smaller share has its runs review to the baseline — and a share the layer
// states stands, which is the second case.
func TestTheRunsBudgetTakesTheBaseline(t *testing.T) {
	cfg := modeCfg(t, "startup", 1, "  review_stop_fraction: 0.05\n")

	t.Run("a silent project's run stops at the baseline", func(t *testing.T) {
		h := newHarness(t, cfg, nil)
		silentProject(t, h, cfg)
		b, clk := runAndBudget(t, h)
		if b.StopFraction != 0 {
			t.Errorf("the run's StopFraction = %v, want 0: a share the project says nothing about is read as unset, and stopAt supplies the baseline (R20)", b.StopFraction)
		}
		clk.advance(time.Duration(0.05 * float64(b.Total)))
		if b.PastStop() {
			t.Errorf("the loop stops starting rounds %v into a %v budget, which is the file's own share: the run takes the baseline's", time.Duration(0.05*float64(b.Total)), b.Total)
		}
		clk.advance(time.Duration(0.75 * float64(b.Total)))
		if !b.PastStop() {
			t.Errorf("the loop is still starting rounds %v into a %v budget, want the baseline's share", time.Duration(0.8*float64(b.Total)), b.Total)
		}
	})

	t.Run("a share the layer states stands", func(t *testing.T) {
		// The harness's own layer states the file's keys (withPins), so this is
		// the same file read by a project that answered: nothing is dropped.
		h := newHarness(t, cfg, nil)
		b, clk := runAndBudget(t, h)
		if b.StopFraction != 0.05 {
			t.Errorf("the run's StopFraction = %v, want the layer's own 0.05", b.StopFraction)
		}
		clk.advance(time.Duration(0.05 * float64(b.Total)))
		if !b.PastStop() {
			t.Errorf("the loop is still starting rounds %v into a %v budget, want the layer's own share", time.Duration(0.05*float64(b.Total)), b.Total)
		}
	})
}

// checkpointCall is the checkpoint stage's work: the call is answered without
// posting, since nothing here is about the answer — the request the agent was
// handed is what the case reads, off the harness's own record of the calls.
func checkpointCall(_ *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
	return agent.Result{}, nil
}

// TestTheCheckpointCallsBudgetTakesTheBaseline is the same rule at the other
// call site the run has for it: the checkpoint stage's own call, launched with
// the run's agent.max_budget_usd (checkpoint_stage.go). A repository's figure
// above config.DefaultMaxBudgetUSD under a project that says nothing about the
// key is dropped for the baseline there too, so the call a stopped run makes is
// priced at 25 and not at the 100 the file wrote — and so is the stopped stage's
// own call, which reads the same figure (yield.go).
func TestTheCheckpointCallsBudgetTakesTheBaseline(t *testing.T) {
	cfg := gwConfig(t, "  max_budget_usd: 100\n")
	h := newGW(t, cfg, "enforce", "100", out4k())
	silentProject(t, h.harness, cfg)
	runner.ResetGatewayStageLimitHook(t)
	if err := runner.SetGatewayStageLimitHook(func(policy.Governance, tier.Tier, pricing.Micros) gateway.StageLimit {
		return stageStopLimit{detail: "implement stage budget"}
	}); err != nil {
		t.Fatal(err)
	}
	runner.ResetStageHook(t)
	no := func(runner.StageView) runner.StageDecision { return runner.StageDecision{} }
	if err := runner.SetStageHook(runner.StageFuncs{
		Timeout:  func(runner.StageView) time.Duration { return 0 },
		TimedOut: no,
		Stopped: func(runner.StageView) runner.StageDecision {
			return runner.StageDecision{Halt: true, Reason: runstore.HaltStageBudget, Detail: "implement stage budget", Checkpoint: true}
		},
		Before: no,
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, stoppedStage, checkpointCall)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Checkpoint == nil {
		t.Fatalf("record = %+v, want the halt and the checkpoint report the stop left", rec)
	}
	calls := h.agent.calls
	if len(calls) != 2 {
		t.Fatalf("%d stage calls, want implement and the checkpoint stage", len(calls))
	}
	for i, stage := range []string{"the stopped stage", "the checkpoint stage"} {
		if got := calls[i].MaxBudgetUSD; got != config.DefaultMaxBudgetUSD {
			t.Errorf("%s ran with --max-budget-usd %v, want the baseline %v (R20)", stage, got, config.DefaultMaxBudgetUSD)
		}
	}
}
