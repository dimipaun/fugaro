package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDocsMobileOrchestratorDesignIsWritten (mobile plan Task P5.1a, approved
// by the owner on 2026-11-14): the design of record for phase 2 hosting — the
// always-on orchestrator — exists, says plainly that it is design only, that
// the approval of the task builds nothing and that the draft it re-grounds was
// closed and never merged, and carries the sections the brief names: the
// proposal, the hard envelope, revocation, the answers of 2026-10-19, the
// unverified table and the hygiene list. Its red state is this test: the
// document did not exist, and the only copy of it was the draft on closed
// PR #632.
func TestDocsMobileOrchestratorDesignIsWritten(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		// The status line, the task it is, and the draft it is built from.
		"design only; nothing is implemented",
		"design of record",
		"Task **P5.1**",
		"**P5.1a**",
		"P5.1a–P5.1h",
		"PR #632, which is closed and was never merged",
		"approved P5.1 on 2026-11-14 and the plan split",
		// The structure the brief names: the proposal, the envelope, revocation,
		// the two question blocks and the two trailing tables.
		"## 4. The proposal",
		"### 4.5 The hard envelope: a stolen phone, a stolen account, a poisoned message, and a compromised orchestrator",
		"### 4.7 Revocation",
		"## 10. Answered by the owner on 2026-10-19",
		"### 10.1 Answered by the owner on 2026-11-14 — the plan's eleven questions",
		"## 11. UNVERIFIED",
		"## 12. The hygiene list: what goes stale on merge, and what collides",
		// The attacker this round's review added, and the two things the design
		// refuses to oversell.
		"**E4 — a compromised resident orchestrator.**",
		"third copy",
		"signJwt",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	// Every question the design put to the owner carries its answer in place,
	// and none is left open. The answers are the ones of 2026-10-19.
	for _, q := range []string{"Q1", "Q2", "Q3", "Q4", "Q5", "Q6", "Q7", "Q8", "Q9", "Q10", "Q11", "Q12", "Q13"} {
		if !strings.Contains(flat, "**"+q+" — ") {
			t.Errorf("%s does not carry %s as its own question", path, q)
		}
	}
	if !strings.Contains(flat, "**Answered 2026-10-19: the recommended option**") {
		t.Errorf("%s records no answer of 2026-10-19 for its questions", path)
	}
	if strings.Contains(flat, "**Open for the owner**") {
		t.Errorf("%s still has an %q block, though every question was answered on 2026-10-19", path, "Open for the owner")
	}
	// The plan's own eleven questions are stated with the recommendations this
	// design is built on — the four the task brief names are load-bearing.
	for _, q := range []string{
		"P5.1-Q1", "P5.1-Q2", "P5.1-Q3", "P5.1-Q4", "P5.1-Q5", "P5.1-Q6",
		"P5.1-Q7", "P5.1-Q8", "P5.1-Q9", "P5.1-Q10", "P5.1-Q11",
	} {
		if !strings.Contains(flat, "**"+q+"**") {
			t.Errorf("%s does not state the plan's %s", path, q)
		}
	}
	for _, want := range []string{
		// P5.1-Q2 — the third copy of the pairing key, and whose note it is.
		"note is P5.1e's to write, not this document's",
		"Global Constraint 4",
		// P5.1-Q3 — the budget must be on.
		"on and enforcing",
		// P5.1-Q4 — no free text in the first phase-2 release.
		"refuses free text",
		// P5.1-Q5/Q11 — no merge in the cloud, and the launch envelope's values.
		"no merge in the cloud",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks the load-bearing ruling %q", path, want)
		}
	}
}

// TestDocsMobileOrchestratorIsReGroundedAgainstTheTree: the design's job is to
// keep PR #632's text except where it states this tree. The three claims the
// task brief names — no rules generation 3 and no device node, `fugaro pair`
// planned and not built, and #601 an open pull request — are the ones the draft
// got wrong, and each is corrected here rather than silently. A re-grounding
// that dropped one of them would still pass the test above.
func TestDocsMobileOrchestratorIsReGroundedAgainstTheTree(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		// The rules generation and the device node.
		"Generation 3 is in the tree",
		"config/admin/devices",
		"merged as **#601**",
		"Generation 5",
		// `fugaro pair` is built.
		"internal/cli/pair.go",
		"newPairRevokeCmd",
		"merged as **#2572**",
		// The Go half of the MAC, and the residual test's new name.
		"internal/mailbox/mac.go",
		"VerifyMAC",
		"TestMailboxResidualMintedTokenNeedsTheDeviceNode",
		// The credentials service, which landed after the draft.
		"internal/credsvc/",
		"RUN 2026-10-21: PASSED",
		// The budget-token seal, which shipped.
		"both halves shipped in 0.9.0",
		// The account axis, which is built.
		"internal/projectpolicy/accounts.go",
		// The refusal's real text, not the draft's invented command.
		"brew upgrade dimipaun/tap/fugaro",
		"policy-rules-skew",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks the re-grounding %q", path, want)
		}
	}
	// The renamed test may appear only as a correction. Every line that carries
	// the old symbol must say it is the draft's — the citation is by symbol so
	// that the rename is a one-line fix, and a line that presents the old name
	// as the current one is the false claim the re-grounding exists to remove.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "TestMailboxResidualMintedTokenPassesUntilPhase6") {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "draft") {
			t.Errorf("%s names TestMailboxResidualMintedTokenPassesUntilPhase6 outside a correction to the draft: %q", path, strings.TrimSpace(line))
		}
	}
	// The refusal the draft invented is not quoted as the tree's: `fugaro
	// upgrade` is not a command, and a reader who pastes it gets an error. It
	// may appear only where the document is describing what the draft quoted.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "'fugaro upgrade'") {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "quot") {
			t.Errorf("%s quotes 'fugaro upgrade' outside a correction of the draft: %q", path, strings.TrimSpace(line))
		}
	}
}

// TestDocsMobileOrchestratorLinksResolve: every relative markdown link in the
// design resolves from its own directory. The document is written against a
// tree that has moved, so its citations are the part most likely to point at a
// file that no longer exists under that name.
func TestDocsMobileOrchestratorLinksResolve(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	doc := readDoc(t, path)
	targets := markdownLinkTargets(doc)
	if len(targets) == 0 {
		t.Fatalf("%s carries no markdown link at all", path)
	}
	for _, target := range targets {
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join("../../docs/mobile/design", target)); err != nil {
			t.Errorf("%s links %q, which does not resolve from docs/mobile/design/: %v", path, target, err)
		}
	}
}

// TestDocsMobileOrchestratorIsIndexedAndPlanned: the README indexes the design
// with a row that says what it is, and the plan's Task P5.1a entry names the
// three files this change touches — the design, this pin and the README. A
// design that is landed but unindexed is one a reader of the directory does not
// find, and a plan whose Files line still names the draft's PR is one a later
// implementer builds from the wrong copy.
func TestDocsMobileOrchestratorIsIndexedAndPlanned(t *testing.T) {
	const design = "design/always-on-orchestrator.md"

	index := readDoc(t, "docs/mobile/README.md")
	if !strings.Contains(index, design) {
		t.Errorf("docs/mobile/README.md does not index %s", design)
	}
	// The index row is the directory's one-line statement of what the design is,
	// so the status it carries is the claim a reader takes away: it is design
	// only, it is P5.1a's, and it was re-grounded from a closed pull request.
	for _, want := range []string{
		"always-on orchestrator",
		"P5.1a",
		"design only",
		"PR #632",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("docs/mobile/README.md's %s row lacks %q", design, want)
		}
	}

	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	heading := "### Task P5.1a:"
	start := strings.Index(plan, heading)
	if start < 0 {
		t.Fatalf("docs/mobile/implementation-plan.md has no %q entry", heading)
	}
	rest := plan[start+len(heading):]
	end := strings.Index(rest, "\n### Task ")
	if end < 0 {
		t.Fatalf("%s's entry runs to the end of the plan: no following task", heading)
	}
	entry := rest[:end]
	for _, want := range []string{
		"docs/mobile/design/always-on-orchestrator.md",
		"internal/cli/docs_mobile_orchestrator_test.go",
		"docs/mobile/README.md",
		"approved 2026-11-14",
	} {
		if !strings.Contains(heading+entry, want) {
			t.Errorf("docs/mobile/implementation-plan.md's P5.1a entry lacks %q", want)
		}
	}
}

// TestDocsMobileOrchestratorClaimsNoCheckNumber: the design names the number a
// live check would take and claims none — the register records 110 as claimed
// by an open pull request, whose own rule is that whichever of two open claims
// merges second renumbers itself, and the plan hands the number to nobody. The
// pin holds both halves: the design claims nothing, and the register carries no
// `## Check 110` section and no `| 110 |` row, because the task that writes it
// is P5.1h and this change is P5.1a.
func TestDocsMobileOrchestratorClaimsNoCheckNumber(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)
	for _, want := range []string{
		"claims no number",
		"writes no check",
		"110 is claimed by an open pull request",
		"the first number free past every claim",
		"P5.1h",
		// The numbers the plan names, and the one this design leans on.
		"Check 84",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}

	register := readDoc(t, "docs/gcp-live-checklist.md")
	if strings.Contains(register, "## Check 110") {
		t.Errorf("docs/gcp-live-checklist.md carries a `## Check 110` section, which is P5.1h's to write and not this change's")
	}
	if strings.Contains(register, "| 110 |") {
		t.Errorf("docs/gcp-live-checklist.md carries a `| 110 |` row, which is P5.1h's to write and not this change's")
	}
}

// TestDocsMobileOrchestratorNeverListIsWhole: the design describes a
// security-sensitive service, and its never-list is the part a later
// implementer turns into code (P5.1c). All eleven rules must be there, and the
// two the review added — the device node the orchestrator may not write, and
// the installation module's `var.launchers` list it may never join — are the
// ones whose absence would be a widening rather than an omission.
func TestDocsMobileOrchestratorNeverListIsWhole(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	doc := readDoc(t, path)
	heading := "## 6. What it must never do"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s has no %q section", path, heading)
	}
	rest := doc[start+len(heading):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		t.Fatalf("%s's never-list runs to the end of the document", path)
	}
	list := rest[:end]
	for n := 1; n <= 11; n++ {
		if !strings.Contains(list, "\n"+strconv.Itoa(n)+". **") {
			t.Errorf("%s's never-list has no rule %d", path, n)
		}
	}
	for _, want := range []string{
		"never write `config/admin/devices/**`",
		"Never joined to `var.launchers`",
		"roles/firebasedatabase.viewer",
		"Never read a secret value",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("%s's never-list lacks %q", path, want)
		}
	}
}
