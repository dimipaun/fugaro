package cli

// RA10 of docs/review-against-task, the launcher's half: `fugaro review --pr N`
// drives a pull request a run opened all the way to the task the job is handed.
//
// Everything between the two ends is real here — the command, the runs bucket
// the launcher resolves the root run out of, the branch its rows recorded, the
// stored task it reads, the image gate that decides whether the field may be
// written at all, and the task.json the job will read. The runner half is
// internal/runner's own file: the two files are one end-to-end proof split
// where the two packages meet, at the task the launcher writes.

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// reviewAgainstFollowUp is a second run on the pull request: a follow-up's own
// text is what the pull request was last asked to do, and the launcher quotes
// it beside the root's task (R1).
const reviewAgainstFollowUp = "20261116-100000-bbbb"

// TestReviewAgainstTaskEndToEnd is the launcher's half: a review launched by
// hand on a pull request a run opened carries that run's task, the latest
// follow-up's own text and the plan task the root names into the task.json the
// review job reads.
//
// The launch is the command a person runs — --pr and the account — and nothing
// else: the spec is not handed in with --spec, so what the job is checked
// against is what the bucket says the pull request was written for.
func TestReviewAgainstTaskEndToEnd(t *testing.T) {
	f := newReviewFixture(t)
	// The fixture's own base image is as old as the policy node it has to read,
	// and a review launched onto it has its spec dropped (reviewSpecSkew,
	// D2933) — here the field is the subject, so the installation is put on a
	// release that carries it.
	setBaseImage(t, reviewSpecSince)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	plan := firstRunSpec(rootID)
	plan.Task = "Rename the gadget\nand update its docs"
	plan.PlanTask = "review-against-task/RA10"
	seedLaunchRootSpec(t, f.cloudFixture, plan, 7, time.Now().Add(-72*time.Hour))
	seedLaunchFollowUp(t, f.cloudFixture, reviewAgainstFollowUp, rootID, "Also rename the widget", time.Now().Add(-48*time.Hour))

	if _, _, err := reviewCLI(t, "--pr", "7"); err != nil {
		t.Fatalf("a review of PR #7: %v", err)
	}
	slug, id, err := parseRunRef(storedRunRef(t, f.cloudFixture))
	if err != nil {
		t.Fatal(err)
	}
	spec := reviewTaskSpec(t, f.cloudFixture, slug, id)
	if spec.Mode != recipe.ModeReview || len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("the stored task is mode %q on %v, want the review of PR #7", spec.Mode, spec.ReviewPRs)
	}
	rs := spec.ReviewSpec
	if rs == nil {
		t.Fatal("the launched review carries no review_spec: the job would judge the code alone")
	}
	if rs.Source != task.ReviewSpecRun || rs.Run != rootID {
		t.Fatalf("review spec = %+v, want the run that opened the pull request (%s)", rs, rootID)
	}
	if rs.Text != plan.Task {
		t.Fatalf("review spec text = %q, want the task the run was asked for (%q)", rs.Text, plan.Task)
	}
	if rs.Followup != "Also rename the widget" {
		t.Fatalf("review spec follow-up = %q, want the latest follow-up's text", rs.Followup)
	}
	if rs.PlanTask != plan.PlanTask {
		t.Fatalf("review spec plan task = %q, want the root task's (%q)", rs.PlanTask, plan.PlanTask)
	}
	if problem := task.ReviewSpecProblem(rs); problem != "" {
		t.Fatalf("the spec the launcher wrote does not hold its own shape: %s", problem)
	}
}
