package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
)

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
	if p := h.agent.calls[0].Prompt; strings.Contains(p, "owner has ruled") {
		t.Errorf("the brief claims a ruling nobody gave:\n%s", p)
	}
}
