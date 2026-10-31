package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// proseReview is a review stage that answered, but not with a verdict: the
// transcript a reviewer leaves when it writes its reasoning and never the JSON
// the stage asked for. Nothing in it parses (verdict.go reads the structured
// output and then the fenced blocks), which is the round this file is about.
func proseReview(t *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
	return agent.Result{Text: "I read the diff and I am not sure: the change looks fine to me, but I did not settle on a verdict.", CostUSD: 0.5}, nil
}

// TestUnparseableReviewIsRetriedOnce: a round whose answer carries no
// parseable verdict is asked once more, in a session of its own, and no fix
// round is spent on it: a reviewer that said nothing asked for nothing, so a
// phantom blocker must not drive the coder (verdict.go, seniorLoop).
func TestUnparseableReviewIsRetriedOnce(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), proseReview, proseReview)
	if err != nil {
		t.Fatalf("the run failed: %v; a round with no parseable verdict is retried and then tolerated, never failed", err)
	}
	if got := countStage(rec, "review"); got != 2 {
		t.Fatalf("review stages = %d, want 2: the round, and its one retry", got)
	}
	if got := countStage(rec, "fix"); got != 0 {
		t.Fatalf("fix stages = %d, want 0: a round with no verdict has no findings to fix", got)
	}
	if len(rec.Reviews) != 1 {
		t.Fatalf("reviews = %+v, want the one round the retry was spent on", rec.Reviews)
	}
	if got := rec.Reviews[0]; got.Verdict != "none" || got.Findings != 0 {
		t.Errorf("review = %+v, want the `none` a review with no verdict is recorded as", got)
	}
}

// TestUnparseableReviewIsToleratedNotABlocker: a run whose review round never
// produced a verdict is otherwise complete, so it ships: the outcome is ready
// rather than a draft over a blocker nobody wrote, the record's reason says the
// round was tolerated, and the pull request's status section says which round
// and why (reviewsPart, Decide).
func TestUnparseableReviewIsToleratedNotABlocker(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), proseReview, proseReview)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("record = %+v; want a ready run: a review with no verdict is not a finding", rec)
	}
	if !strings.Contains(rec.Reason, "tolerated") || !strings.Contains(rec.Reason, "no parseable verdict") {
		t.Errorf("reason = %q; want it to name the round with no verdict and that the run tolerated it", rec.Reason)
	}
	pr := onlyPR(t, h.provider)
	if !strings.Contains(pr.Body, "review round 1 returned no parseable verdict: tolerated") {
		t.Errorf("the status section does not say the round was tolerated:\n%s", pr.Body)
	}
	if pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
}

// TestRealBlockerStillFailsTheRun: the tolerance is for a round that produced
// nothing. A round that produced a finding the run's mode does not let through
// still decides readiness, so the pull request is a draft naming the round —
// the retry path must not have softened that (Decide).
func TestRealBlockerStillFailsTheRun(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "review_rounds: 2", "review_rounds: 1", 1)
	h := newHarness(t, cfg, nil)
	rec, err := h.run(t, implement("feature"), review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Status != runstore.StatusFailed {
		t.Fatalf("record = %+v; want a draft: the round left a finding the run tolerates nothing for", rec)
	}
	if rec.Reason != "review round 1 still has 1 finding" {
		t.Errorf("reason = %q; want the finding's count, not a tolerated verdict", rec.Reason)
	}
	if pr := onlyPR(t, h.provider); !pr.Draft {
		t.Errorf("the pull request is ready: %+v", pr)
	}
}
