package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mobile plan's Task P5.1 ("Phase 2 hosting — the always-on orchestrator")
// asks for a design that answers D115 and says what managed-projects D55–D78
// changes for it. This test is that design's pin, in the shape
// docs_budget_token_channel_test.go uses for a design doc: the file exists,
// carries the load-bearing sentences and the questions the owner has to answer,
// cites paths that are in the tree, allocates no decision number, does not take
// a live-check number another document holds, and is indexed by the mobile
// README and named by the task it belongs to.
//
// Its red state is a missing or gutted document: docs/mobile/design/ did not
// exist before this change, and every assertion below fails on the file that
// was there — none.

const orchDesign = "docs/mobile/design/always-on-orchestrator.md"

// TestDocsMobileOrchestratorDesignIsWritten: the design exists, says it builds
// nothing, answers the questions P5.1 names, and asks the owner the questions
// it may not answer itself.
func TestDocsMobileOrchestratorDesignIsWritten(t *testing.T) {
	doc := readDoc(t, orchDesign)
	for _, want := range []string{
		// What it is, and that it is not built.
		"# Phase 2 hosting: the always-on orchestrator",
		"design only; nothing is implemented",
		"Task P5.1",
		"D115",
		// The brief's own premise, corrected rather than worked around.
		"## 1. Corrections to the brief",
		"generation 3",
		"PR #601",
		// Where it runs, and the shape of the thing.
		"min-instances = 0",
		"Cloud Scheduler",
		"One Cloud Run service per installation",
		// The identity and the never-list.
		"## 6. What it must never do",
		"No IAM",
		"managed apply",
		"never a model credential in the resident service",
		// The three candidates for the credential.
		"the credentials service",
		"Secret Manager secret mounted",
		"Named OAuth tokens per project",
		// The envelope: the three attackers, the MAC, revocation.
		"stolen phone",
		"poisoned",
		"direction",
		"revocation",
		// Approvals with the phone as the only human.
		"spoken-confirmation",
		"Confirm targets",
		// Failure modes and cost.
		"### 4.9 Failure modes",
		"launch claim",
		"at-least-once",
		// The threat model, the checks, the alternatives, the questions.
		"## 5. The threat model, stated once",
		"## 7. The checks it would need",
		"Check 110",
		"## 9. The alternatives, priced",
		"## 10. Open for the owner",
		"## 11. UNVERIFIED",
		// Every question, so one cannot be dropped without this failing.
		"**Q1 —", "**Q2 —", "**Q3 —", "**Q4 —", "**Q5 —", "**Q6 —", "**Q7 —",
		// And the sentence that keeps it out of the owner's numbering.
		"No decision number is allocated",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", orchDesign, want)
		}
	}
}

// TestDocsMobileOrchestratorTakesNoDecisionNumber: this work may not allocate a
// decision number — the mobile block is D110–D129 and the functional spec's is
// D160–D179 — so the design must not carry a decision table of its own. A `|** D`
// row is what a new decision looks like in every document in this tree, so its
// presence here is the failure.
func TestDocsMobileOrchestratorTakesNoDecisionNumber(t *testing.T) {
	doc := readDoc(t, orchDesign)
	if strings.Contains(doc, "\n| **D") {
		t.Errorf("%s carries a decision-table row: this design may not allocate a decision number, and its questions belong in \"Open for the owner\" as Q1–Q7", orchDesign)
	}
	if !strings.Contains(doc, "allocates no decision number") {
		t.Errorf("%s does not say in its own words that it allocates no decision number", orchDesign)
	}
}

// TestDocsMobileOrchestratorCitationsExist: the design's load-bearing citations
// are paths a later implementer has to find — the rules template and its macros,
// the claim and the mint, the credential mounts, the lease and the MAC, the
// module that would carry the service. A substring assertion cannot see one of
// them move, so each is stat'ed here as well as required in the text.
func TestDocsMobileOrchestratorCitationsExist(t *testing.T) {
	doc := readDoc(t, orchDesign)
	for _, cited := range []string{
		// The mailbox's other side, and the tag that fences it.
		"shared/mailbox-schema/index.js",
		"shared/mailbox-schema/mac.js",
		"shared/mailbox/index.js",
		"shared/lease/index.js",
		"shared/presence/index.js",
		"shared/confirm/index.js",
		"shared/README.md",
		// The rules the consumer writes through, and their generation.
		"internal/budget/rules/rules.go",
		"internal/budget/rules/rules.json.tmpl",
		"internal/projectpolicy/version.go",
		// The launch path: the claim, the mint, the take, the override.
		"internal/runstore/launch.go",
		"internal/cli/run.go",
		"internal/cli/launch_budget.go",
		"internal/cli/cloud.go",
		"internal/budget/token/object.go",
		"internal/runner/projectpolicy.go",
		"internal/runner/budget_session.go",
		"internal/backend/backend.go",
		// The envelope: caps, lease, kill switch.
		"internal/budget/evaluate.go",
		"internal/budget/lease.go",
		"internal/budget/kill.go",
		"internal/budget/model.go",
		// The credential mounts, and the accounts that are deploy-time only.
		"internal/infra/spec.go",
		"internal/config/claude_accounts.go",
		"deploy/terraform/gcp/modules/workflow/job.tf",
		"deploy/terraform/gcp/modules/workflow/sa.tf",
		// The identity the design would reuse, and where it lives.
		"deploy/terraform/gcp/modules/firebase/iam.tf",
		"deploy/terraform/gcp/modules/installation/iam.tf",
		"deploy/terraform/gcp/modules/installation/scheduler.tf",
		"internal/backend/gcp/names.go",
		// What it may never reach.
		"internal/cli/allow_delete.go",
		"internal/cli/root.go",
		"internal/infra/firebasedb.go",
		"internal/cli/doctor.go",
		// The documents it is read against.
		"SECURITY.md",
		"docs/gcp-live-checklist.md",
		"docs/plans/2026-09-27-m4-gcp-backend.md",
		"docs/design/budget-token-channel.md",
		"docs/design/managed-projects.md",
		"docs/design/named-oauth-tokens.md",
		"docs/design/voice.md",
		"docs/credentials-service/README.md",
		"docs/mobile/implementation-plan.md",
		"docs/mobile/reconciliation.md",
		"docs/mobile/functional-spec-reconciliation.md",
	} {
		// The document links its neighbours relatively (../implementation-plan.md,
		// ../../design/…, ../../../SECURITY.md), so a citation counts in either form.
		rel, err := filepath.Rel("docs/mobile/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", orchDesign, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", orchDesign, cited, err)
		}
	}
}

// TestDocsMobileOrchestratorCheckNumberIsFree: the design names the next free
// live-check number — 110, the first past every reservation the register
// records — and writes neither its row nor its section. A `## Check 110` section
// or a `| 110 |` row in the register means another document has taken the
// number, and this design would be proposing a taken one; the design must then
// be renumbered, never the register.
//
// The reservation it accounts for: 80–89 are this work's and all ten are named
// in the plan, 90–99 are the credentials service's, and 100–109 are the
// functional spec v0.1's. The mobile README is where that allocation is written
// down, so it is read here too — a design that took a number inside its own
// reserved block would be renumbering a reservation.
func TestDocsMobileOrchestratorCheckNumberIsFree(t *testing.T) {
	const checklist = "docs/gcp-live-checklist.md"
	doc := readDoc(t, orchDesign)
	if !strings.Contains(doc, "Check 110") {
		t.Errorf("%s does not name the live-check number it would need", orchDesign)
	}
	register := readDoc(t, checklist)
	for _, taken := range []string{"## Check 110", "| 110 |"} {
		if strings.Contains(register, taken) {
			t.Errorf("%s carries %q, so Check 110 is no longer free and %s must propose the next number", checklist, taken, orchDesign)
		}
	}
	readme := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(readme, "80–89") {
		t.Errorf("docs/mobile/README.md no longer records this work's 80–89 reservation: Check 110 is only free past it")
	}
}

// TestDocsMobileOrchestratorIsIndexedAndPlanned: the mobile README lists the
// design with its neighbours, and the plan's Task P5.1 names the file and stays
// UNSCHEDULED. Both are the point of the document rather than its decoration:
// an unindexed design is one a later reader does not find, and a task entry that
// stopped saying UNSCHEDULED would be scheduling work the owner has not
// approved.
func TestDocsMobileOrchestratorIsIndexedAndPlanned(t *testing.T) {
	readme := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(readme, "design/always-on-orchestrator.md") {
		t.Errorf("docs/mobile/README.md does not index %s", orchDesign)
	}
	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	if !strings.Contains(plan, "design/always-on-orchestrator.md") {
		t.Errorf("docs/mobile/implementation-plan.md never names %s", orchDesign)
	}
	// The task entry, not the whole file: the heading, its Files line and its
	// Reviewer tier are what this change edited.
	var entry string
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "### Task P5.1:") {
			entry = line
			continue
		}
		if entry != "" {
			if strings.HasPrefix(line, "### ") {
				break
			}
			entry += "\n" + line
		}
	}
	if entry == "" {
		t.Fatalf("docs/mobile/implementation-plan.md has no P5.1 task entry")
	}
	for _, want := range []string{"UNSCHEDULED", "SECURITY-SENSITIVE", "design/always-on-orchestrator.md", "Check 110"} {
		if !strings.Contains(entry, want) {
			t.Errorf("Task P5.1's entry no longer says %q:\n\n%s", want, entry)
		}
	}
}
