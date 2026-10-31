package runner_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestHooksDefaultToTheUntaggedBuild: the stage hook's default pair is the one
// an untagged build runs with — no bound of its own and no halt — so the core
// bounds no stage by governance whatever its layers wrote (D522, G10). The
// pair is read the way the stage loop reads it, and it is the real default and
// not a probe: a test that installed the off-switch itself could not tell it
// from a core that had quietly kept a stage bound.
func TestHooksDefaultToTheUntaggedBuild(t *testing.T) {
	f := runner.StageHook()
	if f.Timeout == nil || f.TimedOut == nil {
		t.Fatalf("the default pair is %+v, want both functions", f)
	}
	g := policy.Governance{StageTimeout: policy.StageTimeout{
		Implement: time.Minute, Review: 2 * time.Minute, Fix: 3 * time.Minute}}
	for _, stage := range []string{"implement", "review", "review_first", "fix", "plan", "render", ""} {
		v := runner.StageView{Stage: stage, Governance: g, RunStage: 40 * time.Minute}
		if got := f.Timeout(v); got != 0 {
			t.Errorf("Timeout(%q) = %v, want no bound: this build applies none", stage, got)
		}
	}
	if d := f.TimedOut(runner.StageView{Stage: "implement", Governance: g}); d.Halt {
		t.Errorf("TimedOut = %+v, want no stop", d)
	}
}

// TestStageTimeoutOfIsTheCoresBound: the export is the stage-to-ceiling
// mapping the core keeps, answered off the governance a stage hook is handed
// so that ee/governance's own Timeout can compute the bound it applies
// (design ee-boundary-moves.md §5.3). Every stage governance bounds has one —
// a review stage through both of its names — and a stage it does not is
// unbounded by governance: an artifact run's plan and render steps, and a
// stage name the runner never asks about, are under the run's own
// timeouts.stage alone.
func TestStageTimeoutOfIsTheCoresBound(t *testing.T) {
	g := policy.Governance{StageTimeout: policy.StageTimeout{
		Implement: time.Minute, Review: 2 * time.Minute, Fix: 3 * time.Minute}}
	for _, tc := range []struct {
		stage string
		want  time.Duration
	}{
		{"implement", time.Minute}, {"review", 2 * time.Minute}, {"review_first", 2 * time.Minute},
		{"fix", 3 * time.Minute}, {"plan", 0}, {"render", 0}, {"", 0},
	} {
		if got := runner.StageTimeoutOf(g, tc.stage); got != tc.want {
			t.Errorf("StageTimeoutOf(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
	// A build whose hook applies nothing still answers no bound per stage
	// through this: what it answers is the governance it was handed, and the
	// zero governance bounds nothing.
	if got := runner.StageTimeoutOf(policy.Governance{}, "implement"); got != 0 {
		t.Errorf("StageTimeoutOf over an empty governance = %v, want no bound", got)
	}
}

// ignoredGovernanceLine is the one run-log line an untagged build writes when
// a run resolved a governance block: a statement about what this build does,
// not a notice about a licence (D522, D523). It is written out here rather
// than read from the seam that writes it, so the wording itself is pinned.
const ignoredGovernanceLine = "this build ignores the governance block"

// TestUntaggedRunnerIgnoresGovernanceWithAWarning: a build whose stage hook is
// the pair an untagged build has after E2.3 decodes a governance: block,
// ignores it and never refuses the run (G2, D522). A stage whose own
// stage_timeout would have stopped it runs on to the run's own
// timeouts.stage and fails there the way any stage that ran out of time does
// — no halt, no checkpoint — and the run log carries exactly one line saying
// the block is ignored, in the build's own words and with no licence notice
// anywhere (D523). The line is written because the run's layers carried a
// block the hook applied nothing from, which is what the next test pins and
// what holds whether or not the policy half resolved anything.
func TestUntaggedRunnerIgnoresGovernanceWithAWarning(t *testing.T) {
	var logs bytes.Buffer
	// The reserve is finalize's own clock, and this test is about where the
	// stage stops: a 5s one lets a loaded machine kill finalize's git and fail
	// the run there instead of at the stage the assertion names.
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 5m, stage: 300ms, verify: 5s, finalize_reserve: 2m }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := newHarness(t, cfg, nil)
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage implement timed out after 300ms" {
		t.Fatalf("record = %+v, want the stage bounded by the run's own timeouts.stage", rec)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("an ignored governance block halted the run: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if n := strings.Count(logs.String(), ignoredGovernanceLine); n != 1 {
		t.Errorf("the run log carries the ignored-block line %d times, want exactly one:\n%s", n, logs.String())
	}
	for _, word := range []string{"licen", "enterprise", "subscription", "paid"} {
		if strings.Contains(strings.ToLower(logs.String()), word) {
			t.Errorf("the run log carries licence wording (%q):\n%s", word, logs.String())
		}
	}
}

// TestUntaggedRunnerWithoutABlockWarnsAboutNothing: the line is about a block
// the run resolved, so a run whose repository writes none ignores nothing and
// says nothing — the stage hook's Timeout answers no bound, which is also what
// the untagged build answers for a stage the block did bound.
func TestUntaggedRunnerWithoutABlockWarnsAboutNothing(t *testing.T) {
	var logs bytes.Buffer
	h := newHarness(t, "", nil)
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v", rec)
	}
	if strings.Contains(logs.String(), ignoredGovernanceLine) {
		t.Errorf("a run with no governance block warned about one:\n%s", logs.String())
	}
}

// TestStageBoundAskedIsAboutTheBlocksNotTheMerge: what the ignored-block line
// is triggered by is the governance the run's own layers carried, never what
// the merge made of them. That is the state both halves of the off-switch make
// at once (E2.3: no shipped defaults and a tighten rule that writes nothing),
// where the same block leaves the effective policy at its zero value — the
// trigger has to answer yes for the block and no for a run that wrote none, or
// a build whose policy half ignores governance too would warn on every run and
// say nothing about the run that was configured.
func TestStageBoundAskedIsAboutTheBlocksNotTheMerge(t *testing.T) {
	blocked := policy.Layer{Governance: policy.Governance{StageTimeout: policy.StageTimeout{Implement: 200 * time.Millisecond}}}
	if !runner.StageBoundAsked([]policy.Layer{{}, {}, blocked}, "implement") {
		t.Error("a layer's stage_timeout did not count as a block asking for a bound")
	}
	if runner.StageBoundAsked([]policy.Layer{{}, {}, {}}, "implement") {
		t.Error("a run whose layers asked for nothing counted as having a block")
	}
	if runner.StageBoundAsked([]policy.Layer{blocked}, "review") {
		t.Error("a bound on implement counted as a bound on review")
	}
}

// TestLateOrDuplicateStageHookIsRefused: the stage hook is installed once, by
// ee/register, so a second SetStage — the way a tagged binary's enforcement
// would be halved after it was set — is refused, and so is a pair the stage
// loop would call into and panic on. The refusals leave the installed pair
// exactly as it was (design §5.5).
func TestLateOrDuplicateStageHookIsRefused(t *testing.T) {
	runner.ResetStageHook(t)
	real := runner.StageFuncs{
		Timeout:  func(runner.StageView) time.Duration { return 0 },
		TimedOut: func(runner.StageView) runner.StageDecision { return runner.StageDecision{} },
	}
	for _, tc := range []struct {
		name string
		f    runner.StageFuncs
	}{
		{"no Timeout", runner.StageFuncs{TimedOut: real.TimedOut}},
		{"no TimedOut", runner.StageFuncs{Timeout: real.Timeout}},
		{"neither", runner.StageFuncs{}},
	} {
		if err := runner.SetStageHook(tc.f); err == nil {
			t.Errorf("a hook with %s was accepted", tc.name)
		}
	}
	if err := runner.SetStageHook(real); err != nil {
		t.Fatalf("the first SetStage was refused: %v", err)
	}
	if err := runner.SetStageHook(real); err == nil {
		t.Error("a second SetStage was accepted, so a tagged build's enforcement can be replaced")
	}
	if got := runner.StageHook(); got.Timeout == nil || got.TimedOut == nil {
		t.Errorf("a refused call changed the installed pair: %+v", got)
	}
}

// TestAStageHookCannotLeaveAHaltReasonTheRunnerCannotRecord: the halt, its
// vocabulary and the words for it stay in the core, so a hook that leaves a
// reason outside the five tripwires fails the stage named instead of writing a
// halt into the record that result.schema.json's enum and haltReasonText
// between them cannot account for (D521, D529). The bound is the hook's own,
// below the run's, so the decision is reached at all.
func TestAStageHookCannotLeaveAHaltReasonTheRunnerCannotRecord(t *testing.T) {
	// finalize runs under the reserve and nothing here is about how long it
	// takes: at 5s a loaded machine kills finalize's own git instead of
	// failing the stage over the hook's reason, which is what this asserts.
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 5m, stage: 300ms, verify: 5s, finalize_reserve: 2m }", "")
	h := newHarness(t, cfg, nil)
	runner.ResetStageHook(t)
	if err := runner.SetStageHook(runner.StageFuncs{
		Timeout: func(runner.StageView) time.Duration { return 200 * time.Millisecond },
		TimedOut: func(runner.StageView) runner.StageDecision {
			return runner.StageDecision{Halt: true, Reason: runstore.HaltReason("invented_stop"), Detail: "time"}
		},
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, blockedStage)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt != nil || rec.Checkpoint != nil {
		t.Fatalf("a reason outside the vocabulary halted the run: halt = %+v, checkpoint = %+v", rec.Halt, rec.Checkpoint)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "invented_stop") {
		t.Fatalf("record = %+v, want the stage failed over the reason the hook left", rec)
	}
}
