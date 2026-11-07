package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// ownerFinding is a finding whose ONLY remedies are a decision that is not the
// run's to make (OD1): the reviewer set needs_owner on it, so no fix round is
// spent on it and the run stops waiting for the owner.
func ownerFinding(file, summary string) map[string]any {
	return map[string]any{
		"severity": "high", "file": file, "summary": summary, "needs_owner": true,
		"likelihood": 2, "impact": 4, "fix_cost": 3, "class": "maintainability",
	}
}

// fixableFinding is an ordinary scored finding, which the run may fix.
func fixableFinding(file, summary string) map[string]any {
	return map[string]any{
		"severity": "high", "file": file, "summary": summary,
		"likelihood": 3, "impact": 4, "fix_cost": 2, "class": "correctness",
	}
}

// TestOwnerDecisionStopsTheLoopWithoutAFixRound: a verdict whose every finding
// is needs_owner buys no fix round and no further review round — no code change
// can settle it — and the run ends saying what it is waiting for, with the
// pull request left a draft. This is the check the OD1 mutation removes.
func TestOwnerDecisionStopsTheLoopWithoutAFixRound(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"),
		reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")))
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := countStage(rec, "fix"); got != 0 {
		t.Fatalf("fix stages = %d, want 0: an owner decision is not handed to a fixer", got)
	}
	if got := countStage(rec, "review"); got != 1 {
		t.Fatalf("review stages = %d, want 1: a round whose every finding is the owner's stops the loop at once", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %q, want a draft", rec.Outcome)
	}
	want := "review is waiting on 1 owner decision"
	if rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltOwnerDecision {
		t.Errorf("halt = %+v, want the owner-decision halt", rec.Halt)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 {
		t.Fatalf("owner decisions = %+v, want the one the round left", rec.OwnerDecisions)
	}
	if d := rec.OwnerDecisions.Decisions[0]; d.File != "LICENSE.md" || !strings.Contains(d.Summary, "counsel") {
		t.Errorf("decision = %+v, want the file and the reviewer's own sentence", d)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
	// The status block says what the run is waiting for, under its own line,
	// with the file and the reviewer's own sentence: a reader of the pull
	// request must not have to open the transcript to learn what to decide.
	for _, want := range []string{"**Waiting on you**", "`LICENSE.md`", "the licence text needs counsel's clearance"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the status section does not carry %q:\n%s", want, pr.Body)
		}
	}
}

// TestMixedVerdictFixesTheFixableOneThenStops: a round carrying one fixable
// finding and one the owner must settle fixes the first and never the second,
// and the next round's all-owner verdict is where the loop stops.
func TestMixedVerdictFixesTheFixableOneThenStops(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"),
		reviewScored("changes",
			fixableFinding("ledger.go", "a partial write is not rolled back"),
			ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")),
		fixStep,
		reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")))
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
		t.Fatalf("stages = %v, want the fixable finding fixed and no second fix", got)
	}
	// The fixer is handed the fixable finding and never the owner's: the
	// owner's finding is the one thing in the round that no code change can
	// settle, and a brief that carried it would spend the round on it.
	fix := h.agent.calls[2]
	if !strings.Contains(fix.Prompt, "a partial write is not rolled back") {
		t.Errorf("the fix round's brief does not carry the fixable finding:\n%s", fix.Prompt)
	}
	if strings.Contains(fix.Prompt, "counsel") {
		t.Errorf("the fix round's brief carries the owner's finding:\n%s", fix.Prompt)
	}
	want := "review is waiting on 1 owner decision"
	if rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltOwnerDecision {
		t.Errorf("halt = %+v, want the owner-decision halt", rec.Halt)
	}
}

// TestOwnerDecisionIsHonoredAtFixCostOne: a reviewer that sets needs_owner on
// a finding the table would call cheap is still taken at its word — the run
// stops and lists it — because the flag is the reviewer's answer about who can
// settle the finding and not a second score.
func TestOwnerDecisionIsHonoredAtFixCostOne(t *testing.T) {
	h := newHarness(t, "", nil)
	cheap := ownerFinding("internal/approval/flow.go", "the approval flow is a product decision")
	cheap["fix_cost"] = 1
	rec, err := h.run(t, implement("feature"), reviewScored("changes", cheap))
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := countStage(rec, "fix"); got != 0 {
		t.Fatalf("fix stages = %d, want 0: fix_cost 1 does not override the reviewer's needs_owner", got)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 ||
		rec.OwnerDecisions.Decisions[0].File != "internal/approval/flow.go" {
		t.Fatalf("owner decisions = %+v, want the cheap finding listed", rec.OwnerDecisions)
	}
}
