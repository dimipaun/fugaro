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
		t.Fatalf("the run failed: %v; a round with no parseable verdict is retried once and then recorded, never a blocker", err)
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

// TestNoVerdictReviewLeavesADraftNotReady: a round that never produced a
// verdict is not a blocker and costs no fix round, but it is not a review
// either — the answer it did not give is the only thing that would have shown
// someone read the diff — so the run ends a draft rather than ready. The reason
// and the pull request's status section both name the round (Decide,
// reviewsPart).
func TestNoVerdictReviewLeavesADraftNotReady(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), proseReview, proseReview)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("record = %+v; want a draft: a review with no verdict is not one the run was read by", rec)
	}
	want := "review round 1 returned no parseable verdict after a retry"
	if rec.Reason != want {
		t.Errorf("reason = %q; want %q", rec.Reason, want)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is ready: %+v", pr)
	}
	// Twice, and the count is the point: once as the run's own reason in the
	// status line, once as the review line reviewsPart writes. A senior round
	// rendered as the skipped first-line one would leave a single mention of the
	// reason, and the status section would no longer say why the pull request is
	// a draft.
	if n := strings.Count(pr.Body, want); n != 2 {
		t.Errorf("the status section names the round with no verdict %d times, want 2 (the run's reason and the review line):\n%s", n, pr.Body)
	}
}

// TestReviewRetryIsSkippedOnceTheTokenCapIsHit: the retry is a second stage of
// the same round, and it is not bought once the run has crossed its token cap.
// The dollar cap is the gateway's and this one is the runner's own, so without
// the check the retry ran anyway: one more paid review stage on a run the
// runner had already decided to halt (seniorRound, capReached).
func TestReviewRetryIsSkippedOnceTheTokenCapIsHit(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	impl := withUsage(implement("feature"), agent.Usage{Input: 40}, nil)
	// The round's own answer crosses the cap: 40 + 90 of 100.
	rev := withUsage(proseReview, agent.Usage{Input: 90}, nil)
	rec, err := h.run(t, impl, rev) // a retry would be a third, unscripted call
	if err != nil {
		t.Fatal(err)
	}
	if got := countStage(rec, "review"); got != 1 {
		t.Fatalf("review stages = %d, want 1: a retry after the token cap is crossed is a stage the run does not buy", got)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap {
		t.Fatalf("record = %+v; want the token cap's halt", rec)
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
