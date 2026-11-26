package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The run-level half of LV5.2 (docs/rigor-modes/levels-implementation-plan.md,
// levels-reconciliation.md LD8, LF11): the fixture file an agent that stopped
// on a question writes gives a draft whose record carries needs_guidance, the
// status section opens with the questions it asked, and a follow-up that
// answers them is the run it always was. A unit test of Decide cannot reach any
// of the three — the record the run writes, the section it publishes and the
// stage loop that produced both.

// TestGuidanceStopsTheRunAsADraft is the acceptance as a run: a Pro run whose
// own pr.md carries one open question ends a draft with the field set and the
// questions' own reason, however clean its rounds were, and the pull request it
// published is never the ready one. The next run of the same repository — a
// follow-up whose file asks nothing — is ready, which is what says the field is
// the question's and not the level's.
//
// Mutation: drop the askedGuidance case in Decide, and the first run here is
// ready with the flag unset.
func TestGuidanceStopsTheRunAsADraft(t *testing.T) {
	h := newHarness(t, modeCfg(t, "pro", 2, ""), nil)
	rec, err := h.run(t, handBackSteps(handBackPRAsked)...)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || !rec.NeedsGuidance {
		t.Fatalf("outcome = %s, needs_guidance = %v, want a draft that asks for guidance", rec.Outcome, rec.NeedsGuidance)
	}
	if rec.Reason != runner.ReasonOpenQuestions {
		t.Errorf("reason = %q, want %q", rec.Reason, runner.ReasonOpenQuestions)
	}
	body := onlyPR(t, h.provider).Body
	q := strings.Index(body, "**OPEN QUESTIONS**")
	if !strings.Contains(body, "**Draft**") || strings.Contains(body, "**Ready for review**") {
		t.Errorf("the run published a body that is not the draft one:\n%s", body)
	}
	if q < 0 || !strings.Contains(body[q:], "should the flag default to off?") {
		t.Errorf("the status section does not carry the questions the agent asked:\n%s", body)
	}
	// The block opens the section: above the hand-back the level's row says,
	// which is the part a reader who stops at the first lines would miss.
	if hb := strings.Index(body, "**Hand-back** (`"); hb < 0 || hb < q {
		t.Errorf("the questions do not open the section: the hand-back block sits above them:\n%s", body)
	}
}

// TestGuidanceIsNotTheRunnersFallbackBody: the runner's fallback body embeds
// the task, so a brief that spells the heading asks nothing — the questions are
// the agent's own file at the door LV5.1 uses, and a run whose agent left no
// file is the no-file draft with the flag unset.
//
// Mutation: read the questions off the published body instead of handBack(), and
// the run below asks for guidance it never got.
func TestGuidanceIsNotTheRunnersFallbackBody(t *testing.T) {
	const brief = "# Task: add a feature\n\n## Open questions\n- should the flag default to off?\n"
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: brief}
	h := newHarness(t, modeCfg(t, "pro", 2, ""), spec)
	h.noHandBack = true
	rec, err := h.run(t, noAccount(implement("feature")), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(onlyPR(t, h.provider).Body, "## Open questions") {
		t.Fatalf("the pull request does not carry the task's own heading, so this case proves nothing:\n%s", onlyPR(t, h.provider).Body)
	}
	if rec.NeedsGuidance || rec.Outcome != runstore.OutcomeDraft || rec.Reason != runner.ReasonNoHandBack {
		t.Fatalf("outcome = %s (reason %q), needs_guidance = %v; want the no-file draft", rec.Outcome, rec.Reason, rec.NeedsGuidance)
	}
	if strings.Contains(onlyPR(t, h.provider).Body, "**OPEN QUESTIONS**") {
		t.Errorf("the status section shows questions no agent asked:\n%s", onlyPR(t, h.provider).Body)
	}
}

// TestGuidanceOpensTheSectionAtEveryLevel: the questions are nobody's section
// of the hand-back table, so no level's row decides them. Every level a run can
// resolve draws the block, above the hand-back and above the section's own
// blocks, which is what a reader who stops at the first lines meets.
//
// Mutation: draw the block inside the level's hand-back row, and the levels
// whose row carries no such section lose the questions.
func TestGuidanceOpensTheSectionAtEveryLevel(t *testing.T) {
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := newHarness(t, modeCfg(t, level, 2, ""), nil)
			rec, err := h.run(t, handBackSteps(handBackPRAsked)...)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Outcome != runstore.OutcomeDraft || !rec.NeedsGuidance {
				t.Fatalf("outcome = %s, needs_guidance = %v, want the guidance draft", rec.Outcome, rec.NeedsGuidance)
			}
			body := onlyPR(t, h.provider).Body
			q := strings.Index(body, "**OPEN QUESTIONS**")
			if q < 0 {
				t.Fatalf("the status section carries no OPEN QUESTIONS block:\n%s", body)
			}
			if !strings.Contains(body[q:], "should the flag default to off?") {
				t.Errorf("the block does not carry the agent's own question:\n%s", body[q:])
			}
			if hb := strings.Index(body, "**Hand-back** (`"); hb < 0 || hb < q {
				t.Errorf("the questions do not open the section: the hand-back block sits above them:\n%s", body)
			}
		})
	}
}

// TestGuidanceFollowUpIsReady: a follow-up answers the questions and is the run
// it always was — its own file carries the follow-up's sections and asks
// nothing, so the run is ready and its record's flag is unset. The question was
// the run before it, and the field says nothing about the level.
//
// Mutation: carry needs_guidance forward from the chain's previous run, and the
// follow-up below is a draft for a question it answered.
func TestGuidanceFollowUpIsReady(t *testing.T) {
	h := followUpHarness(t, handBackFollowCfg(t, "pro"), nil, commitOnly("feature"), review("ship", 0))
	h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
	h.followUp(t, followID, runID, "")
	rec, err := h.run(t, withAnswer(implement("rename"), handBackFU), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady || rec.Reason != "" || rec.NeedsGuidance {
		t.Fatalf("outcome = %s (reason %q), needs_guidance = %v; want ready without the flag", rec.Outcome, rec.Reason, rec.NeedsGuidance)
	}
}
