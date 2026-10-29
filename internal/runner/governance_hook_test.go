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

// TestHooksDefaultToTodaysBehaviour: the stage hook's default pair is what the
// runner did before it existed — the stage's own governance.stage_timeout, and
// the stage_budget halt by time when it fires — so the seam adds no behaviour
// of its own (G10). The pair is read the way the stage loop reads it.
func TestHooksDefaultToTodaysBehaviour(t *testing.T) {
	f := runner.StageHook()
	if f.Timeout == nil || f.TimedOut == nil {
		t.Fatalf("the default pair is %+v, want both functions", f)
	}
	g := policy.Governance{StageTimeout: policy.StageTimeout{
		Implement: time.Minute, Review: 2 * time.Minute, Fix: 3 * time.Minute}}
	for _, tc := range []struct {
		stage string
		want  time.Duration
	}{
		{"implement", time.Minute}, {"review", 2 * time.Minute},
		{"review_first", 2 * time.Minute}, {"fix", 3 * time.Minute},
		{"plan", 0}, {"render", 0}, {"", 0},
	} {
		v := runner.StageView{Stage: tc.stage, Governance: g, RunStage: 40 * time.Minute}
		if got := f.Timeout(v); got != tc.want {
			t.Errorf("Timeout(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
	if d := f.TimedOut(runner.StageView{Stage: "implement", Governance: g}); !d.Halt ||
		d.Reason != runstore.HaltStageBudget || d.Detail != "time" {
		t.Errorf("TimedOut = %+v, want the stage_budget halt by time", d)
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
// anywhere (D523).
func TestUntaggedRunnerIgnoresGovernanceWithAWarning(t *testing.T) {
	var logs bytes.Buffer
	cfg := stageTimeoutCfg(t,
		"timeouts: { total: 1m, stage: 300ms, verify: 5s, finalize_reserve: 5s }",
		"governance:\n  stage_timeout: { implement: 200ms }\n")
	h := newHarness(t, cfg, nil)
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	runner.SetCore(t, h.deps.Log)
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
// says nothing: the stage hook's Timeout answers no bound, which is also what
// the untagged build answers for a stage the block did bound.
func TestUntaggedRunnerWithoutABlockWarnsAboutNothing(t *testing.T) {
	var logs bytes.Buffer
	h := newHarness(t, "", nil)
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	runner.SetCore(t, h.deps.Log)
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
