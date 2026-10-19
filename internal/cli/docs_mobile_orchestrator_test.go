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
		"no model credential of its own",
		// The three candidates for the credential.
		"the credentials service",
		"Secret Manager secret mounted",
		"Named OAuth tokens per project",
		// The envelope: the attackers, the MAC, the key's custody, revocation.
		"stolen phone",
		"poisoned",
		"direction",
		"revocation",
		// The 2026-10-19 security review's four blockers: the cloud copy of the
		// pairing key (B1), the compromised resident orchestrator (B2), what a
		// launch authority really reaches (B3), and launches as an attacker's
		// verb (B4).
		"pairing key",
		"E4 — a compromised resident orchestrator",
		"holds the job's own credentials",
		"launch",
		// Who can actually read a secret's value: not the `orch` account alone.
		// The basic Owner/Editor roles carry secretmanager.versions.access and
		// bypass a secret's own policy, which the tree states itself in F3.
		"secretmanager.versions.access",
		"Project Owners/Editors bypass any secret-level policy",
		"reader set",
		// The repository kill node's real path.
		"config/kill/repos/<slug>",
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
		// The number is not claimed: two open pull requests were reported to be
		// pointing at the number that read as free on 2026-10-19, so the design
		// says when a number is taken, not which one.
		"the next free number at merge time",
		"## 9. The alternatives, priced",
		"## 10. Open for the owner",
		"## 11. UNVERIFIED",
		// The hygiene list, where a collision with work in flight is written down.
		"## 12. The hygiene list",
		// Every question, so one cannot be dropped without this failing.
		"**Q1 —", "**Q2 —", "**Q3 —", "**Q4 —", "**Q5 —", "**Q6 —", "**Q7 —",
		// The six the 2026-10-19 review added, mapped to its findings: the cloud
		// key's custody, the phone's launch envelope, a phone-side kill switch,
		// the cloud's merge identity, the pairing's lifetime, and the
		// credentials service's run.invoker.
		"**Q8 —", "**Q9 —", "**Q10 —", "**Q11 —", "**Q12 —", "**Q13 —",
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
		t.Errorf("%s carries a decision-table row: this design may not allocate a decision number, and its questions belong in \"Open for the owner\" as Q1–Q13", orchDesign)
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

// TestDocsMobileOrchestratorCheckNumberIsFree: the design says it takes "the
// next free number at merge time" and writes neither a row nor a section — and
// it must NOT hard-claim a number. 110 was the first free number past every
// reservation on 2026-10-19, but two other pull requests in flight were reported
// to be pointing at it as well, so a design that wrote "Check 110" into its own
// text would be proposing a number two other documents may take. The register
// must also still be free of 110: if it is not, the collision has landed and
// this test's premise is what a later reader needs to see.
//
// The reservation it accounts for: 80–89 are this work's and all ten are named
// in the plan, 90–99 are the credentials service's, and 100–109 are the
// functional spec v0.1's. The mobile README is where that allocation is written
// down, so it is read here too — a design that took a number inside its own
// reserved block would be renumbering a reservation.
func TestDocsMobileOrchestratorCheckNumberIsFree(t *testing.T) {
	const checklist = "docs/gcp-live-checklist.md"
	doc := readDoc(t, orchDesign)
	if !strings.Contains(doc, "the next free number at merge time") {
		t.Errorf("%s does not say which live-check number it would take — and it must say it in those words, not as a claimed number", orchDesign)
	}
	// A hard claim is the failure: the number must not be asserted as this
	// work's. The one place 110 may appear is as the number that READ as free,
	// which the text qualifies.
	if strings.Contains(doc, "**Check 110**") || strings.Contains(doc, "names Check 110") {
		t.Errorf("%s claims Check 110 as its own: the number is the next free one at merge time, because two other pull requests were reported to point at 110 too", orchDesign)
	}
	register := readDoc(t, checklist)
	for _, taken := range []string{"## Check 110", "| 110 |"} {
		if strings.Contains(register, taken) {
			t.Errorf("%s carries %q, so 110 is no longer free: the collision this design's hygiene list predicted has landed and the design's text must be reworded, never the register", checklist, taken)
		}
	}
	readme := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(readme, "80–89") {
		t.Errorf("docs/mobile/README.md no longer records this work's 80–89 reservation: the next free number is only free past it")
	}
}

// TestDocsMobileOrchestratorAttributionsAreRight: the citations that name a
// finding or a phrase have to name the one that actually says it. Three of them
// were wrong once: the "this repository never runs interactively" sentence was
// attributed to docs/design/voice.md's F16 (which is a different finding — "no
// fugaro voice, and no local socket"), the register's "first number free past
// every reservation above it" was quoted for 58 as well as 61 (58's sentence
// drops "free"), and the laptop credential's rules bypass was hung on
// internal/infra/firebasedb.go's dbErr, which says only that the database
// refused you. The register's own phrases are asserted here too, so the
// document's claim about the register cannot go stale silently.
func TestDocsMobileOrchestratorAttributionsAreRight(t *testing.T) {
	doc := readDoc(t, orchDesign)
	for _, wrong := range []string{
		"voice.md`'s F16",
		"`dbErr` says so in as many words",
		"`dbErr`)", // the bypass is D113b's; dbErr never states one
	} {
		if strings.Contains(doc, wrong) {
			t.Errorf("%s still carries %q: the citation names a finding or a symbol that does not say it", orchDesign, wrong)
		}
	}
	if !strings.Contains(doc, "**F12**") {
		t.Errorf("%s no longer cites docs/design/voice.md's F12, which is where \"this repository never runs interactively\" is written", orchDesign)
	}
	if !strings.Contains(doc, "drops *free*") {
		t.Errorf("%s quotes the register's numbering phrase without marking 58's variant", orchDesign)
	}
	register := readDoc(t, "docs/gcp-live-checklist.md")
	for _, phrase := range []string{
		"the first number free past every reservation above it",
		"the first number past every reservation",
	} {
		if !strings.Contains(register, phrase) {
			t.Errorf("docs/gcp-live-checklist.md no longer writes %q: %s's claim about the register has gone stale", phrase, orchDesign)
		}
	}
}

// TestDocsMobileOrchestratorKillPathMatchesTheTree: the design names the
// repository kill node as the tree writes it — `config/kill/repos/<slug>`
// (internal/budget/model.go's PathKillRepo, and the rules' KILLS_OFF macro
// reads root.child('config/kill/repos').child($slug)) — and never as the shorter
// `config/kill/<slug>` a reader would guess, which is a node nothing writes and
// the rules never consult. A design that tells an implementer to watch the wrong
// node is a design that tells them the kill switch is off when it is on.
func TestDocsMobileOrchestratorKillPathMatchesTheTree(t *testing.T) {
	doc := readDoc(t, orchDesign)
	if strings.Contains(doc, "config/kill/<slug>") {
		t.Errorf("%s writes the repository kill node as config/kill/<slug>: the tree's node is config/kill/repos/<slug> (PathKillRepo), and a reader following this document would watch a node nothing writes", orchDesign)
	}
	if !strings.Contains(doc, "config/kill/repos/<slug>") {
		t.Errorf("%s no longer names the repository kill node at all", orchDesign)
	}
	// The claim is the tree's, so the tree is asked.
	model := readDoc(t, "internal/budget/model.go")
	if !strings.Contains(model, `"config/kill/repos/"`) {
		t.Errorf("internal/budget/model.go no longer builds PathKillRepo from \"config/kill/repos/\": %s's citation has gone stale", orchDesign)
	}
	rules := readDoc(t, "internal/budget/rules/rules.go")
	if !strings.Contains(rules, "config/kill/repos") {
		t.Errorf("internal/budget/rules/rules.go's KILLS_OFF no longer reads config/kill/repos: %s's citation has gone stale", orchDesign)
	}
}

// orchSlug is the anchor a heading carries in a rendered Markdown document:
// lowercased, with everything that is not a letter, a digit, a space or a
// hyphen dropped and spaces turned into hyphens. It is the rule the document's
// own `](#…)` links depend on.
func orchSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// TestDocsMobileOrchestratorAnchorsResolve: every in-document link points at a
// heading that exists. The document cross-references itself heavily — the
// envelope (§4.5) and the questions are cited from a dozen places — and a
// heading whose text is edited without its links is a document whose
// cross-references silently stop working. That happened once: the §4.5 heading
// gained ", and a compromised orchestrator" while thirteen links kept the old
// anchor.
func TestDocsMobileOrchestratorAnchorsResolve(t *testing.T) {
	doc := readDoc(t, orchDesign)
	slugs := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		body, ok := strings.CutPrefix(line, "#")
		if !ok {
			continue
		}
		for strings.HasPrefix(body, "#") {
			body = body[1:]
		}
		body = strings.TrimSpace(body)
		if body == "" || body == strings.TrimSpace(line) {
			continue
		}
		slugs[orchSlug(body)] = true
	}
	// A document with no headings would make this test vacuous.
	if len(slugs) < 10 {
		t.Fatalf("%s has %d headings: the anchor check needs the document's real headings", orchDesign, len(slugs))
	}
	for i := 0; ; {
		j := strings.Index(doc[i:], "](#")
		if j < 0 {
			break
		}
		start := i + j + 3
		end := strings.Index(doc[start:], ")")
		if end < 0 {
			t.Fatalf("%s has an unterminated in-document link at offset %d", orchDesign, start)
		}
		slug := doc[start : start+end]
		if !slugs[slug] {
			t.Errorf("%s links to #%s, which no heading in the file produces: the heading moved or was reworded and the link was not", orchDesign, slug)
		}
		i = start + end
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
	for _, want := range []string{"UNSCHEDULED", "SECURITY-SENSITIVE", "design/always-on-orchestrator.md", "the next free number at merge time"} {
		if !strings.Contains(entry, want) {
			t.Errorf("Task P5.1's entry no longer says %q:\n\n%s", want, entry)
		}
	}
}
