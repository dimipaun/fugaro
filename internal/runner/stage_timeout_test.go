package runner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// stageTimeoutCfg is the fixture repository's file with the workflow's
// timeouts replaced and a governance: block appended.
func stageTimeoutCfg(t *testing.T, timeouts, governance string) string {
	t.Helper()
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }", timeouts, 1)
	return cfg + governance
}

// blockedStage is blockUntilDone with a cost figure: the halt's checkpoint
// carries the spend the stage had reached as its estimate, and a zero there
// would not tell a recorded estimate from a missing one.
func blockedStage(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	res, err := blockUntilDone(t, ctx, req)
	res.CostUSD = 1
	return res, err
}

// finishedAtTheBound is implement's work done, whose result the stage returns
// as the bound that ends it fires: the race the runner must read as work done
// rather than as a stop. Which bound it is is the test's own configuration.
func finishedAtTheBound(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	res, err := implement("feature")(t, ctx, req)
	<-ctx.Done()
	return res, err
}

// failedOnItsOwn is an agent that fails its stage with an error of its own,
// well inside every bound the run has.
func failedOnItsOwn(_ *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
	return agent.Result{CostUSD: 1}, errors.New("the agent gave up")
}

// TestStageTimeoutHaltsWithCheckpoint: a stage that reaches its own
// governance.stage_timeout stops the run as a governance halt with a
// checkpoint report — the reason and the detail the runner's own account of
// the bound gives it, the report on the record, and a draft pull request
// saying so — and not as a failed stage (T7.6, D329, G7). The stage ends at
// its own bound, well inside the run's timeouts.stage, and no later stage
// runs.
func TestStageTimeoutHaltsWithCheckpoint(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget || rec.Halt.Scope != "run" || rec.Halt.Detail != "time" {
		t.Fatalf("halt = %+v", rec.Halt)
	}
	if want := "halted: stage_budget: time"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.agent.calls))
	}
	cp := rec.Checkpoint
	if cp == nil || cp.Trigger != string(runstore.HaltStageBudget) || cp.WhyOver == "" || cp.EstimateUSD != 1 {
		t.Fatalf("checkpoint = %+v", cp)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Error("the halt's pull request is ready")
	}
	// The status section the halt wrote mid-stage says the same thing in the
	// vocabulary the record uses, and the report finalize posted carries the
	// runner's words for the stop, the detail it gave it and the reason.
	for _, want := range []string{"**Halted**", "checkpoint `stage\\_budget`", "a stage passed its own budget"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the pull request lacks %q:\n%s", want, pr.Body)
		}
	}
	report := lastComment(t, h)
	for _, want := range []string{"**Halted:** a stage passed its own budget (time)", "halted: stage_budget: time"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
}

// TestStageTimeoutNeverAboveTimeoutsStage: a stage's own timeout only ever
// shortens it. One above the run's timeouts.stage leaves that bound in
// charge, and the stage that reaches it is the failure it has always been —
// no governance stop, no checkpoint — because the run's own time budget is
// not a governance tripwire.
func TestStageTimeoutNeverAboveTimeoutsStage(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 1s, verify: 30s, finalize_reserve: 10s }",
		"governance:\n  stage_timeout: { implement: 5m }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 1s" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage cut by timeouts.stage is a governance stop: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.agent.calls))
	}
	if !onlyPR(t, h.provider).Draft {
		t.Error("a failed run's pull request is not a draft")
	}
}

// TestStageTimeoutStandsWhenTheResultRacesIt: a stage whose own deadline
// fired but which returned a result of its own is a stage that finished, the
// same convention the run's own timeouts.stage has. The run does not stop
// and no checkpoint is written: only a stage the bound actually cut is a
// governance halt, which is what the agent's error says.
func TestStageTimeoutStandsWhenTheResultRacesIt(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 1s }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, finishedAtTheBound, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage that returned a result as its own deadline fired was stopped: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.agent.calls) != 2 {
		t.Fatalf("%d stages ran, want implement and review", len(h.agent.calls))
	}
}

// TestStageTimeoutStandsWhenTheRunBoundIsReached: the run's own timeouts.stage
// has the same convention, and the stage's own bound is read the same way — a
// stage that returns a result as the bound fires is a stage that finished, so
// the run does not fail it and the loop goes on to the next stage.
func TestStageTimeoutStandsWhenTheRunBoundIsReached(t *testing.T) {
	h := newHarness(t, stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 1s, verify: 5s, finalize_reserve: 5s }", ""), nil)
	rec, err := h.run(t, finishedAtTheBound, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage that returned a result as the run's own bound fired was stopped: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("record = %+v", rec)
	}
	if len(h.agent.calls) != 2 {
		t.Fatalf("%d stages ran, want implement and review", len(h.agent.calls))
	}
}

// TestStageTimeoutKeepsAnAgentFailureOfItsOwn: an agent that fails its stage
// with an error of its own, its own deadline never reached, keeps that
// failure. The bound did not fire, so the stage is not a stop: the record
// names the stage's own error and no checkpoint is written.
func TestStageTimeoutKeepsAnAgentFailureOfItsOwn(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 1s }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, failedOnItsOwn)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement failed: the agent gave up" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a failure of the agent's own was read as the stage's timeout: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
}

// TestStageTimeoutIsNotTheRunsOwnBound: the run's own bound can cut a stage
// before the stage's own does — here the finalize reserve leaves the stage
// less time than its governance.stage_timeout asks for — and a stage cut by
// the run's own bound is the failure it has always been, whatever a stage
// timeout would have said had it been reached (T7.6). Reporting it as a stop
// would blame a tripwire that never fired and draft a pull request over a
// run that simply ran out of its own time.
func TestStageTimeoutIsNotTheRunsOwnBound(t *testing.T) {
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 30s, stage: 10s, verify: 5s, finalize_reserve: 27s }",
		"governance:\n  stage_timeout: { implement: 9s }\n")
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "time budget exhausted") {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a stage cut by the run's own bound is a governance stop: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.agent.calls))
	}
}

// TestStageTimeoutLooserInTheRepositoryIsRecorded: a stage timeout is one key
// of the one policy merge, so a repository file that writes a looser value
// than the project layer's is not applied and is not silently dropped either:
// the effective bound stays the layer's, and result.json carries the ignored
// value under the key it was written as (T7.1, D331, docs/project-layer.md).
func TestStageTimeoutLooserInTheRepositoryIsRecorded(t *testing.T) {
	layer := runnerLayer + "governance: { stage_timeout: { implement: 200ms } }\n"
	repo := layeredRepo + "governance: { stage_timeout: { implement: 5m } }\n"
	h := newHarness(t, repo, layerSpec(layer))
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget {
		t.Fatalf("record = %+v, halt = %+v", rec, rec.Halt)
	}
	pol := rec.Policy
	if pol == nil || pol.Sources[policy.KeyStageTimeoutImplement] != policy.SourceCeiling {
		t.Fatalf("policy = %+v, want the layer's value as the ceiling's", pol)
	}
	var found bool
	for _, ig := range pol.Ignored {
		if ig.Key == policy.KeyStageTimeoutImplement {
			found = true
			if ig.Value != "5m0s" || ig.Effective != "200ms" || ig.From != policy.SourceBranch {
				t.Errorf("ignored = %+v", ig)
			}
		}
	}
	if !found {
		t.Fatalf("the looser value in the repository's file was dropped without being recorded: %+v", pol.Ignored)
	}
}

// TestStageTimeoutFromTheProjectLayer: the project layer's governance block is
// the ceiling a stage's own timeout is read from, and the one an operator sets
// for every repository of a project (T7.1, T7.4). A run whose layer sets it,
// with nothing in the repository's own file, is bounded by it.
func TestStageTimeoutFromTheProjectLayer(t *testing.T) {
	layer := runnerLayer + "governance: { stage_timeout: { implement: 200ms } }\n"
	h := newHarness(t, layeredRepo, layerSpec(layer))
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget ||
		rec.Halt.Detail != "time" || rec.Checkpoint == nil {
		t.Fatalf("record = %+v, halt = %+v, checkpoint = %+v", rec, rec.Halt, rec.Checkpoint)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d stages ran, want only implement", len(h.agent.calls))
	}
}
