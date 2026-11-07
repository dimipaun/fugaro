package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// seedWaitingRoot stores the first run rootID, which opened PR 7, pushed, and
// ended waiting on the owner's decisions (OD1): its review left two findings
// no code change could settle, so the run stopped with the owner-decision halt
// and the two decisions on its record.
func seedWaitingRoot(t *testing.T, f *cloudFixture) {
	t.Helper()
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	rec := prRecord(rootID, "", 7, 1)
	rec.Branch, rec.StartedAt = "fugaro/"+rootID, time.Now().Add(-72*time.Hour)
	rec.Status, rec.Outcome = runstore.StatusHalted, runstore.OutcomeDraft
	rec.Reason = "review is waiting on 2 owner decisions"
	rec.Halt = &runstore.Halt{Reason: runstore.HaltOwnerDecision, Scope: "run", At: time.Now().Add(-72 * time.Hour)}
	rec.OwnerDecisions = runstore.NewOwnerDecisions([]runstore.OwnerDecision{
		{File: "LICENSE.md", Summary: "the licence text needs counsel's clearance"},
		{File: "internal/approval/flow.go", Summary: "the approval flow is a product decision"},
	})
	writeRecord(t, f, rootID, rec)
}

// TestRunPRRefusesAFollowUpWaitingOnTheOwner: a follow-up of a run that stopped
// on owner decisions is refused without a ruling, and the refusal names every
// decision and the flag that carries the answer — the loop that burned the runs
// on PR #1808 cannot start again. Mutation (drop the `&& !ruled` term in
// resolvePR, or hand it false): this launch proceeds and the test fails on the
// missing refusal and on the one execution it started.
func TestRunPRRefusesAFollowUpWaitingOnTheOwner(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "go on")
	if err == nil {
		t.Fatal("the follow-up launched; it must be refused until the owner rules")
	}
	got := err.Error() + errOut
	for _, want := range []string{
		rootID, "PR #7", "2 owner decisions",
		"`LICENSE.md`: the licence text needs counsel's clearance",
		"`internal/approval/flow.go`: the approval flow is a product decision",
		"--owner-ruled <text-or-file>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want none: a refusal starts nothing", n)
	}
}

// TestRunPROwnerRuledLaunchesAndCarriesTheRuling: with the flag, the same
// launch goes through, and the ruling is on the task the job reads (it is what
// the runner appends to the brief verbatim) — the answer, not a note about it.
func TestRunPROwnerRuledLaunchesAndCarriesTheRuling(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	ruling := "Counsel cleared the MIT text; the approval flow stays as it is until Q1, so keep it and say so in the PR."
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", ruling, "go on"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, fuID); spec.OwnerRuled != ruling {
		t.Errorf("task owner_ruled = %q, want the ruling verbatim", spec.OwnerRuled)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("executions = %d, want 1", n)
	}
}

// TestRunPROwnerRuledReadsAFileAndStdin: the flag takes the text or a file
// holding it, or - for stdin, which is how a coordinator passes a ruling it
// already has in hand.
func TestRunPROwnerRuledReadsAFileAndStdin(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	path := filepath.Join(t.TempDir(), "ruling.md")
	if err := os.WriteFile(path, []byte("Cleared by counsel on 2026-11-07.\r\nShip it.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", path, "go on"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, fuID); spec.OwnerRuled != "Cleared by counsel on 2026-11-07.\nShip it." {
		t.Errorf("task owner_ruled = %q, want the file's text", spec.OwnerRuled)
	}

	f2 := newCloudFixture(t)
	seedWaitingRoot(t, f2)
	id := wallRunIDAt(0, "130000", "0a0a")
	if _, _, err := executeStdin(t, "The product decision is: keep the flow.\n", "run", "--repo", "acme/app", "--pr", "7", "--run-id", id, "--owner-ruled", "-", "go on"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f2, id); spec.OwnerRuled != "The product decision is: keep the flow." {
		t.Errorf("task owner_ruled = %q, want stdin's text", spec.OwnerRuled)
	}
}

// TestRunOwnerRuledIsRefusedWhenNothingIsWaiting: --owner-ruled is the answer
// to the decisions a run left the owner, and the reviewers read it as that
// answer (followup.RulingAddendum lists the decisions under it and tells them a
// finding among those is settled). A launch whose previous run left nothing
// waiting has no such decisions, so the text would reach the review levels as
// an instruction from whoever launched it — the one thing a reviewer may not
// take — and the launch is refused before anything is stored or started.
// Mutation (drop the `od == nil &&` guard, or hand it false): this launch
// proceeds and the test fails on the missing refusal and on the execution it
// started.
func TestRunOwnerRuledIsRefusedWhenNothingIsWaiting(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", "ship it", "go on")
	if err == nil {
		t.Fatal("the launch went through; a ruling nothing answers must be refused")
	}
	got := err.Error() + errOut
	for _, want := range []string{rootID, "PR #7", "no decisions to answer", "launch again without it"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want none: a refusal starts nothing", n)
	}
}

// TestRunOwnerRuledIsRefusedWhenTheLastRunsRecordIsGone: whether the previous
// run left the owner anything is in its record, and a record that is not there
// cannot say. The launch is refused rather than trusted: an unreadable record
// is a refusal for the mode and the recipe already, and a ruling read as the
// answer to nothing is the case this whole refusal exists for.
func TestRunOwnerRuledIsRefusedWhenTheLastRunsRecordIsGone(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	failRecordRead(t, runstore.ErrNotFound)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", "ship it", "go on")
	if err == nil {
		t.Fatal("the launch went through; a ruling over a record nobody can read must be refused")
	}
	got := err.Error() + errOut
	for _, want := range []string{rootID, "has no record left", "start a new run instead"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, got)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want none: a refusal starts nothing", n)
	}
}

// TestRunOwnerRuledNeedsAPullRequest: the ruling answers findings a run on a
// pull request left, so a launch with no --pr is refused rather than stored on
// a task nothing will read it from.
func TestRunOwnerRuledNeedsAPullRequest(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--owner-ruled", "yes", "do it"); err == nil || !strings.Contains(err.Error(), "--owner-ruled needs --pr") {
		t.Fatalf("err = %v, want the --pr refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("executions = %d, want none", n)
	}
}

// TestRunOwnerRuledIsRefusedOnAReviewRecipe: the ruling is the owner's answer
// to findings a run on the pull request left, and it is a follow-up's field —
// the runner appends it to that follow-up's brief, and task.Validate refuses
// owner_ruled on a spec with no branch, pr and previous_run. A mode: review
// recipe builds exactly such a spec (it reviews a pull request rather than
// continuing one), so the launch is refused rather than started with the
// owner's answer dropped: the way --review-run is refused for the same reason.
func TestRunOwnerRuledIsRefusedOnAReviewRecipe(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	seedWaitingRoot(t, f)
	wantRefused(t, f, "--owner-ruled answers the findings a run on a pull request left, and --recipe review-only reviews a pull request instead of fixing one",
		"run", "--repo", "acme/app", "--pr", "7", "--recipe", "review-only", "--owner-ruled", "keep the flow")
}

// TestRunOwnerRuledRefusesAValueNoRulingCanBe: the flag's value is the ruling
// itself, and a ruling no brief may carry is refused in the launcher's own
// words rather than by the run at bootstrap, after the launch was paid for —
// the rules are the runner's (task.ValidateOwnerRuled, which task.Spec.Validate
// applies to the same field), so what this accepts is never what the run
// refuses. A value that is blank once trimmed, and one over
// task.MaxOwnerRuledBytes, are each exit 1 with a message naming the flag, and
// neither starts anything: the refusals sit before the cloud call, so a launch
// that would otherwise be accepted (the previous run is waiting, and --pr 7 is
// a follow-up of it) leaves no execution behind.
func TestRunOwnerRuledRefusesAValueNoRulingCanBe(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	cases := []struct{ name, id, value, want string }{
		{"blank", "20261007-150000-abcd", "   ",
			"--owner-ruled is empty: pass the owner's ruling, a file holding it, or - for stdin"},
		{"over the bound", "20261007-150000-abce", strings.Repeat("x", task.MaxOwnerRuledBytes+1),
			fmt.Sprintf("--owner-ruled is %d bytes; a ruling must be at most %d",
				task.MaxOwnerRuledBytes+1, task.MaxOwnerRuledBytes)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", c.id,
				"--owner-ruled", c.value, "go on")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want exit 1 naming %q", err, c.want)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Fatalf("executions = %d, want none: a refusal starts nothing", n)
			}
		})
	}
}

// TestRunPRFollowUpAfterARulingIsNotRefused: the next follow-up of that pull
// request finds a previous run that is not waiting on anything (the ruled run
// pushed on), so it launches with no flag at all — the refusal is about the
// decisions still open, not about the pull request for ever.
func TestRunPRFollowUpAfterARulingIsNotRefused(t *testing.T) {
	f := newCloudFixture(t)
	seedWaitingRoot(t, f)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", "ship it", "go on"); err != nil {
		t.Fatal(err)
	}
	// The ruled run's own record, as the runner writes it: no block, no halt.
	rec := prRecord(fuID, "", 7, 1)
	rec.Branch, rec.StartedAt = "fugaro/"+rootID, time.Now()
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: rootID}
	writeRecord(t, f, fuID, rec)
	next := wallRunIDAt(0, "140000", "1b1b")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", next, "keep going"); err != nil {
		t.Fatalf("the second follow-up must launch with no ruling: %v", err)
	}
	// A repeated --run-id of the same launch is the same task, ruling and
	// all: it reports the launch it already made rather than storing a second
	// one. A DIFFERENT ruling under the same id is a different task, and is
	// refused the way every other difference is.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", "ship it", "go on"); err != nil {
		t.Fatalf("repeating the same launch must be idempotent: %v", err)
	}
	// A launch that gives no ruling makes no claim about one, exactly as a
	// launch that gives no --title makes no claim about the title: the stored
	// ruling stands and the repeat is the same task, not a different one.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "go on"); err != nil {
		t.Fatalf("a repeat without --owner-ruled is the same task, not a different one: %v", err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--owner-ruled", "hold on", "go on"); err == nil || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("a repeated --run-id with a different ruling must be refused, got %v", err)
	}
}
