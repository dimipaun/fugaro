package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// seedWaiting records the decisions the run before the follow-up left for the
// owner. A ruling is an answer, so it is only ever launched over a run that
// left something to answer (internal/cli refuses the rest), and the reviewer's
// brief lists the decisions under it (followup.RulingAddendum).
func seedWaiting(t *testing.T, h *fuHarness, ds ...runstore.OwnerDecision) {
	t.Helper()
	ctx := context.Background()
	store := runstore.Open(h.bucket, repoSlug, runID)
	rec, err := store.ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec.OwnerDecisions = runstore.NewOwnerDecisions(ds)
	if err := store.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// TestFollowUpCarriesTheOwnersRuling: the ruling a follow-up was launched with
// (task.Spec.OwnerRuled, fugaro run --owner-ruled) reaches the implement
// prompt verbatim — it is the answer to the findings the run before it stopped
// on, and a brief that paraphrased it would be answering for the owner — and
// it is echoed onto the run's record, so a reader sees what the run was told.
func TestFollowUpCarriesTheOwnersRuling(t *testing.T) {
	h := followUpHarness(t, "", nil)
	h.followUp(t, followID, runID, "Tidy up.")
	const ruling = "Counsel cleared the MIT text as it stands. Keep the approval flow; product will revisit it next quarter."
	spec := &task.Spec{Version: 1, RunID: followID, Repo: "acme/app", Ref: "main", Workflow: h.first.Workflow, Task: "Tidy up.",
		Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID, OwnerRuled: ruling}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil {
		t.Fatalf("the follow-up failed: %v", err)
	}
	if len(h.agent.calls) == 0 || !strings.Contains(h.agent.calls[0].Prompt, ruling) {
		t.Fatalf("the implement brief does not carry the ruling verbatim:\n%s", h.agent.calls[0].Prompt)
	}
	if rec.OwnerRuled != ruling {
		t.Errorf("record owner_ruled = %q, want the ruling verbatim", rec.OwnerRuled)
	}
}

// TestTheOwnersRulingReachesEveryReviewLevel: the reviewer reads in a session
// of its own, so the ruling the implement brief carries is not in front of it —
// and a reviewer that does not know the owner answered meets the same finding
// the run was halted on, marks it needs_owner again, and stops the follow-up in
// the same place with the next one refused: exactly the loop the ruling was
// launched to end. Every review level therefore reads the ruling, fenced as the
// launch's text with the decisions it answers listed under it, and with the one
// thing it changes about the reviewer's own labels (a finding among those
// decisions is not needs_owner): the cheap first line, the senior review, and
// the adversarial review.
//
// The three levels share the one review brief the run builds (runner.go), so
// this is the prompt each of them is handed; a case that read only the senior
// call could not tell a ruling added to that brief from one added to the senior
// loop alone.
func TestTheOwnersRulingReachesEveryReviewLevel(t *testing.T) {
	cfg := followUpYAML(t, "")
	cfg = strings.Replace(cfg, "  review_rounds: 2\n",
		"  review_rounds: 2\n  first_line_review: on\n  first_line_rounds: 1\n  models: { coder: coder-model, reviewer: senior-model }\n", 1)
	cfg = strings.Replace(cfg, "agent:\n", "rigor:\n  default: critical\nagent:\n", 1)
	// The first run reviews all three levels too (it is the same plan), and
	// must push: a follow-up needs a pull request of its own to continue.
	h := followUpHarness(t, cfg, nil,
		implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	seedWaiting(t, h, runstore.OwnerDecision{File: "LICENSE.md", Summary: "the licence text needs counsel's clearance"})
	h.followUp(t, followID, runID, "Tidy up.")
	const ruling = "Counsel cleared the MIT text as it stands. Keep the approval flow; product will revisit it next quarter."
	spec := &task.Spec{Version: 1, RunID: followID, Repo: "acme/app", Ref: "main", Workflow: h.first.Workflow, Task: "Tidy up.",
		Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID, OwnerRuled: ruling}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"), review("ship", 0), review("ship", 0), review("ship", 0))
	if err != nil {
		t.Fatalf("the follow-up failed: %v", err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v, want one call per level after implement", got)
	}
	for i, level := range []string{"the first line", "the senior review", "the adversarial review"} {
		prompt := h.agent.calls[i+1].Prompt
		if !strings.Contains(prompt, ruling) {
			t.Errorf("%s's prompt does not carry the ruling verbatim:\n%s", level, prompt)
		}
		// The ruling is quoted as the launch's text, in a block of its own, and
		// the decisions it answers are listed under it: that list is what keeps
		// the reviewer's silence about a finding to the ones the owner answered.
		if !strings.Contains(prompt, "`LICENSE.md`: the licence text needs counsel's clearance") {
			t.Errorf("%s's prompt does not list the decision the ruling answers:\n%s", level, prompt)
		}
		if !strings.Contains(prompt, "never mark it needs_owner") {
			t.Errorf("%s's prompt does not say a finding the ruling settles is not needs_owner:\n%s", level, prompt)
		}
		if !strings.Contains(prompt, "<<<fugaro-owner-ruling-") || !strings.Contains(prompt, "<<<end-fugaro-owner-ruling-") {
			t.Errorf("%s's prompt does not fence the ruling off as the launch's text:\n%s", level, prompt)
		}
	}
}

// TestFollowUpWithoutARulingSaysNothingAboutOne: the section is the ruling's
// own, and a run launched without one carries no trace of it — an empty
// heading, like an empty field, would read as an owner who said nothing rather
// than as a launch that asked nothing.
func TestFollowUpWithoutARulingSaysNothingAboutOne(t *testing.T) {
	h := followUpHarness(t, "", nil)
	h.followUp(t, followID, runID, "Tidy up.")
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil {
		t.Fatalf("the follow-up failed: %v", err)
	}
	if rec.OwnerRuled != "" {
		t.Errorf("record owner_ruled = %q, want none", rec.OwnerRuled)
	}
	for i, c := range h.agent.calls {
		// The implement brief's ruling section, and the review briefs' addendum:
		// neither has anything to say when the launch carries no ruling.
		for _, claim := range []string{"owner has ruled", "This follow-up was launched to answer the decisions"} {
			if strings.Contains(c.Prompt, claim) {
				t.Errorf("call %d claims a ruling nobody gave (%q):\n%s", i+1, claim, c.Prompt)
			}
		}
	}
}
