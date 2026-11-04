package cli

import (
	"fmt"
	"strings"
	"testing"
)

// The pin for docs/run-without-checkout/, the design for launching a run for a
// repository the launcher has no checkout of. Nothing pins a docs folder on its
// behalf in this package (docs_in_job_review_test.go states the convention), so
// the folder that wants its documents held writes its own file — this one.
//
// What it holds is the state the owner's answers of 2026-11-04 put the design
// in, and the one answer that is not the design's own recommendation: the read
// is made through the installation's credentials service, as the operator
// themselves, so the launcher holds no App key and reads no environment
// variable. That is the claim a later edit is most likely to soften by
// accident — the recommendation the owner declined is the one the prose around
// it was first written for — so it is pinned in the spec and in the plan, not
// only in the reconciliation.
//
// What it deliberately does not assert: anything about the code. No task of the
// design is approved and nothing here is implemented, so there is no behaviour
// to hold; the claims are about four documents.
const (
	runWithoutCheckoutREADME     = "docs/run-without-checkout/README.md"
	runWithoutCheckoutSpec       = "docs/run-without-checkout/spec.md"
	runWithoutCheckoutRecon      = "docs/run-without-checkout/reconciliation.md"
	runWithoutCheckoutImplPlan   = "docs/run-without-checkout/implementation-plan.md"
	runWithoutCheckoutStatus     = "**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**"
	runWithoutCheckoutAnswered   = "2026-11-04"
	runWithoutCheckoutDeviation  = "not the recommendation"
	runWithoutCheckoutNoKey      = "holds no App key"
	runWithoutCheckoutNoEnv      = "reads no environment variable"
	runWithoutCheckoutDecisions  = 14 // D901-D914, each with a row of its own
	runWithoutCheckoutDecisionAt = 901
)

// TestDocsRunWithoutCheckoutIsAnsweredAndPlanOnly: every one of the four
// documents carries the answered status line word for word — the state a later
// round must not quietly reopen by editing one of them back to "design only" —
// and the index names the other three.
func TestDocsRunWithoutCheckoutIsAnsweredAndPlanOnly(t *testing.T) {
	readme := readDoc(t, runWithoutCheckoutREADME)
	for _, path := range []string{
		runWithoutCheckoutREADME, runWithoutCheckoutSpec,
		runWithoutCheckoutRecon, runWithoutCheckoutImplPlan,
	} {
		if doc := readDoc(t, path); !strings.Contains(doc, runWithoutCheckoutStatus) {
			t.Errorf("%s lacks the answered status line %q", path, runWithoutCheckoutStatus)
		}
	}
	for _, want := range []string{
		"[spec.md](spec.md)", "[reconciliation.md](reconciliation.md)", "[implementation-plan.md](implementation-plan.md)",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not index %s", runWithoutCheckoutREADME, want)
		}
	}
}

// TestDocsRunWithoutCheckoutRecordsTheOwnerAnswer: the owner's answer to D902 is
// recorded, dated, and marked as the deviation it is, in the three documents
// that carry prose about the credential — and the answer's substance (no App
// key in the launcher, no environment variable read) is in the spec and the plan
// and not only in the reconciliation's decision table. An edit that reinstates
// the recommended App-key read would leave the status line and the answers'
// dates in place, which is why the substance is what is checked here.
func TestDocsRunWithoutCheckoutRecordsTheOwnerAnswer(t *testing.T) {
	for _, path := range []string{runWithoutCheckoutREADME, runWithoutCheckoutRecon, runWithoutCheckoutSpec, runWithoutCheckoutImplPlan} {
		doc := readDoc(t, path)
		if !strings.Contains(doc, runWithoutCheckoutAnswered) {
			t.Errorf("%s records no answer of %s", path, runWithoutCheckoutAnswered)
		}
	}
	recon := flattenDoc(readDoc(t, runWithoutCheckoutRecon))
	if !strings.Contains(recon, "**Not the recommendation. Read through the credentials service's person class.**") {
		t.Errorf("%s does not mark D902's answer as the deviation it is", runWithoutCheckoutRecon)
	}
	for _, path := range []string{runWithoutCheckoutREADME, runWithoutCheckoutImplPlan} {
		if !strings.Contains(flattenDoc(readDoc(t, path)), runWithoutCheckoutDeviation) {
			t.Errorf("%s records D902's answer without saying it is %s", path, runWithoutCheckoutDeviation)
		}
	}
	for _, path := range []string{runWithoutCheckoutREADME, runWithoutCheckoutSpec, runWithoutCheckoutImplPlan} {
		doc := flattenDoc(readDoc(t, path))
		for _, want := range []string{runWithoutCheckoutNoKey, runWithoutCheckoutNoEnv} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s never says the launcher %s", path, want)
			}
		}
	}
}

// TestDocsRunWithoutCheckoutHoldsItsDecisionBlock: every decision of the block
// the README claims — D901 to D914, each with a row of its own — is in the
// reconciliation, and no document cites a number past the block's end as this
// work's own. The block is the design's claim on the register, so a row dropped
// in an edit (or a decision renumbered into a neighbour's block) fails here
// rather than in the next design that reads the register.
func TestDocsRunWithoutCheckoutHoldsItsDecisionBlock(t *testing.T) {
	recon := readDoc(t, runWithoutCheckoutRecon)
	for i := 0; i < runWithoutCheckoutDecisions; i++ {
		want := fmt.Sprintf("| **D%d** |", runWithoutCheckoutDecisionAt+i)
		if !strings.Contains(recon, want) {
			t.Errorf("%s has no row for decision %s", runWithoutCheckoutRecon, want)
		}
	}
	if !strings.Contains(readDoc(t, runWithoutCheckoutREADME), "**D901–D919**") {
		t.Errorf("%s no longer states the block it holds", runWithoutCheckoutREADME)
	}
}

// TestDocsRunWithoutCheckoutPlanNamesItsOwnTasks: the task names every other
// document cites are the ones the plan actually writes. A review of the first
// round found the index claiming tasks the plan did not contain, so the plan's
// headings are read for the names and the index's sentence is checked against
// them rather than trusted.
func TestDocsRunWithoutCheckoutPlanNamesItsOwnTasks(t *testing.T) {
	plan := readDoc(t, runWithoutCheckoutImplPlan)
	for _, want := range []string{
		"## A1.", "## A2.", "## A3.", "## A4.", "## B1.", "## B2.", "## C1.", "## D1.",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s has no %q heading", runWithoutCheckoutImplPlan, want)
		}
	}
	for _, absent := range []string{"## A5.", "## B3.", "## C2.", "## D2."} {
		if strings.Contains(plan, absent) {
			t.Errorf("%s grew a %q task: the index and the review round's count name A1-A4, B1-B2, C1 and D1", runWithoutCheckoutImplPlan, absent)
		}
	}
	readme := readDoc(t, runWithoutCheckoutREADME)
	if !strings.Contains(readme, "Tasks A1–A4, B1–B2, C1 and D1") {
		t.Errorf("%s no longer names the plan's tasks as the plan writes them", runWithoutCheckoutREADME)
	}
}
