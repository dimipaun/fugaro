package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The tier tests that read the shipped expectations moved to ee/governance with
// the policy that carries them (plan task E2.3, D521). What is left here is the
// record's own shape in the build that resolves none of them, which is a
// different question from what the figures are.

// TestRunRecordWithoutATier: a run that carried no tier records no block — the
// field is absent, never a block with an empty tier — so a reader can tell a
// run nobody dispatched an expectation for from one that carries a tier.
func TestRunRecordWithoutATier(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier != nil {
		t.Fatalf("the record of an untiered run carries %+v", rec.Tier)
	}
}

// TestRunRecordCarriesTheZeroExpectationInAnUntaggedBuild: a build that links
// no ee/ features resolves no tier expectation — the figures beside the tier
// name are the ones ee/governance ships, and this build ships none — so a run
// dispatched at --tier small records its tier with a zero expectation beside
// it. That is the record-shape delta this move leaves in the MIT build, and it
// is pinned here because the test that pinned the figures
// (TestRunRecordCarriesTheShippedTierDefault) went with them to ee/governance.
//
// The block is still written, and that is the point: the field's absence means
// "no tier was dispatched", which is a different fact from "an expectation of
// zero". Zero is a figure the published schema allows (result.schema.json
// bounds expected_usd and expected_minutes at 0 and not above), so the record
// is one this build may publish, and nothing reads the figure yet — the
// expected-cost checkpoint is T8.2, and followup keeps the tier name alone.
func TestRunRecordCarriesTheZeroExpectationInAnUntaggedBuild(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main",
		Workflow: "app", Task: "Add a feature", Tier: tier.Small}
	h := newHarness(t, "", spec)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier == nil {
		t.Fatal("the record of a tiered run carries no tier block")
	}
	if rec.Tier.Tier != tier.Small || rec.Tier.ExpectedUSD != 0 || rec.Tier.ExpectedMinutes != 0 {
		t.Errorf("the block = %+v, want the tier the task carried with the zero expectation an untagged build resolves", rec.Tier)
	}
}
