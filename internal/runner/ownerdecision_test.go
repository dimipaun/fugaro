package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
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

// TestShippedVerdictWithAnOwnerDecisionWaitsOnThePR: a round that ships while
// carrying a finding only the owner can settle is not a round that may go
// ready. The decision is recorded from the deciding round whatever the verdict
// was, and Decide's gate sits above every verdict — so the run ends a draft
// with the question on it, even though the loop itself never stopped on it and
// no halt was recorded (OD1).
func TestShippedVerdictWithAnOwnerDecisionWaitsOnThePR(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"),
		reviewScored("ship", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")))
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %q, want a draft: a decision the owner has not answered is not a shipped round", rec.Outcome)
	}
	if want := "review is waiting on 1 owner decision"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if rec.Halt != nil {
		t.Errorf("halt = %+v, want none: the loop did not stop on the decision, Decide did", rec.Halt)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 {
		t.Fatalf("owner decisions = %+v, want the one the shipped round carried", rec.OwnerDecisions)
	}
	if d := rec.OwnerDecisions.Decisions[0]; d.File != "LICENSE.md" || !strings.Contains(d.Summary, "counsel") {
		t.Errorf("decision = %+v, want the file and the reviewer's own sentence", d)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
	for _, want := range []string{"**Waiting on you**", "`LICENSE.md`", "the licence text needs counsel's clearance"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the status section does not carry %q:\n%s", want, pr.Body)
		}
	}
}

// TestMixedLastRoundListsTheOwnersDecisionOnTheDraft: a last round that mixes a
// fixable finding with one only the owner can settle fixes what it can and
// leaves the run a draft waiting on the rest — the owner's part is listed, not
// dropped because the round was not all theirs (OD1).
func TestMixedLastRoundListsTheOwnersDecisionOnTheDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	mixed := reviewScored("changes",
		fixableFinding("ledger.go", "a partial write is not rolled back"),
		ownerFinding("LICENSE.md", "the licence text needs counsel's clearance"))
	rec, err := h.run(t, implement("feature"), mixed, fixStep, mixed)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
		t.Fatalf("stages = %v, want both rounds with the one fix between them", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %q, want a draft", rec.Outcome)
	}
	if want := "review is waiting on 1 owner decision"; rec.Reason != want {
		t.Errorf("reason = %q, want %q: the owner's part is what the draft waits on", rec.Reason, want)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 ||
		rec.OwnerDecisions.Decisions[0].File != "LICENSE.md" {
		t.Fatalf("owner decisions = %+v, want the mixed round's owner finding listed", rec.OwnerDecisions)
	}
	pr := onlyPR(t, h.provider)
	for _, want := range []string{"**Waiting on you**", "`LICENSE.md`"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the status section does not carry %q:\n%s", want, pr.Body)
		}
	}
}

// TestARunHaltedForItsCapRecordsNoOwnerDecisions: the decisions are recorded
// for the run that ends waiting on them, never over a stop that is the run's
// own — a cap the run crossed is why it ended, and a run whose review happened
// to leave an owner's finding beside that is not a run waiting for a person
// (OD1).
func TestARunHaltedForItsCapRecordsNoOwnerDecisions(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	rec, err := h.run(t,
		withUsage(implement("feature"), agent.Usage{Input: 40, Output: 20}, nil),
		withUsage(reviewScored("changes", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")),
			agent.Usage{Input: 30, Output: 20}, nil))
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap {
		t.Fatalf("halt = %+v, want the token cap the round crossed", rec.Halt)
	}
	if rec.OwnerDecisions != nil {
		t.Errorf("owner decisions = %+v, want none on a run that stopped for its own cap", rec.OwnerDecisions)
	}
	if body := onlyPR(t, h.provider).Body; strings.Contains(body, "Waiting on you") {
		t.Errorf("the status section asks the owner for a decision the run did not stop on:\n%s", body)
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

// TestShippedSeniorRoundWithAnOwnerDecisionSurvivesLevel3: level 3 overwrites
// the round readiness is decided on, and the decisions a run owes the owner are
// not that round's alone. A senior review that shipped while carrying a finding
// only the owner can settle, followed by a clean adversarial round, is still a
// run waiting for the owner: with only the deciding round read, the pull
// request went READY with the question recorded nowhere — nothing listed it and
// the follow-up that answers it was launched blind over it (OD1).
func TestShippedSeniorRoundWithAnOwnerDecisionSurvivesLevel3(t *testing.T) {
	h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0), // review_first
		reviewScored("ship", ownerFinding("LICENSE.md", "the licence text needs counsel's clearance")), // review: the senior level ships carrying it
		review("ship", 0), // adversarial, clean
	)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v, want one round at every level", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s (reason %q), want a draft: level 3 shipping does not answer what level 2 left the owner", rec.Outcome, rec.Reason)
	}
	if want := "review is waiting on 1 owner decision"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if rec.Halt != nil {
		t.Errorf("halt = %+v, want none: the loop ran to its own end, Decide kept the pull request a draft", rec.Halt)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 ||
		rec.OwnerDecisions.Decisions[0].File != "LICENSE.md" {
		t.Fatalf("owner decisions = %+v, want the senior round's finding listed", rec.OwnerDecisions)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
	for _, want := range []string{"**Waiting on you**", "`LICENSE.md`", "the licence text needs counsel's clearance"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("the status section does not carry %q:\n%s", want, pr.Body)
		}
	}
}

// TestMixedSeniorRoundOutOfRoundsStillLeavesTheBlock: the same loss from the
// other side. A senior round that ran out of rounds with one fixable finding
// beside the owner's, followed by level 3 shipping clean, leaves the pull
// request a draft because level 2's last verdict is not a ship — and that
// draft has to carry the question, or the owner is never asked and the
// follow-up that answers it is not refused (OD1).
func TestMixedSeniorRoundOutOfRoundsStillLeavesTheBlock(t *testing.T) {
	h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
	mixed := reviewScored("changes",
		fixableFinding("ledger.go", "a partial write is not rolled back"),
		ownerFinding("LICENSE.md", "the licence text needs counsel's clearance"))
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0), // review_first
		mixed,             // review, round 1
		fixStep,
		review("ship", 0), // review_first, the bounce
		mixed,             // review, round 2: review_rounds is 2, so the level is out of rounds
		review("ship", 0), // adversarial, clean
	)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s, want a draft: level 2's last round did not ship", rec.Outcome)
	}
	if rec.OwnerDecisions == nil || len(rec.OwnerDecisions.Decisions) != 1 ||
		rec.OwnerDecisions.Decisions[0].File != "LICENSE.md" {
		t.Fatalf("owner decisions = %+v, want the last senior round's owner finding listed", rec.OwnerDecisions)
	}
	if want := "review is waiting on 1 owner decision"; rec.Reason != want {
		t.Errorf("reason = %q, want %q: the question is what the draft waits on, not only the held-back level", rec.Reason, want)
	}
	if body := onlyPR(t, h.provider).Body; !strings.Contains(body, "`LICENSE.md`") {
		t.Errorf("the status section does not carry the pending decision:\n%s", body)
	}
}
