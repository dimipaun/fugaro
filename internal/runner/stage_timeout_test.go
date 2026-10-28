package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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

// blockedStage is a stage that runs until its context ends, the way a real
// agent does: only a bound of the runner's stops it.
func blockedStage(_ *testing.T, ctx context.Context, _ agent.Request) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{CostUSD: 1}, ctx.Err()
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

// TestStageTimeoutFromTheProjectLayer: the project layer's governance block is
// one of the two surfaces a stage's own timeout is read from, and the one an
// operator sets for every repository of a project (T7.1, T7.4). A run whose
// layer sets it, with nothing in the repository's own file, is bounded by it.
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
