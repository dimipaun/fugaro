package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
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

// mobileOrchestratorNeverRuleRE reads one rule of section 6: its number and the
// sentence the design states in bold.
var mobileOrchestratorNeverRuleRE = regexp.MustCompile(`(?m)^([0-9]+)\. \*\*(.+?)\*\*`)

// TestOrchestratorNeverRulesAreTheDesignsEleven (mobile plan Task P5.1c):
// section 6 of the design of record is the never-list the resident
// orchestrator's code answers with, and internal/orchestrator/never.go is that
// list as refusals. The two are compared rule by rule and sentence by sentence,
// so a rule added, renumbered or reworded on one side fails here rather than
// drifts: the code half is what a path meets, the document half is what a
// reviewer reads, and neither is the other's copy.
func TestOrchestratorNeverRulesAreTheDesignsEleven(t *testing.T) {
	const path = "docs/mobile/design/always-on-orchestrator.md"
	list := docSection(t, path, readDoc(t, path), "## 6. What it must never do")
	rules := orchestrator.NeverRules()
	written := mobileOrchestratorNeverRuleRE.FindAllStringSubmatch(list, -1)
	if len(written) != len(rules) {
		t.Fatalf("%s's never-list states %d rules and internal/orchestrator/never.go carries %d: the two are one list",
			path, len(written), len(rules))
	}
	for i, m := range written {
		rule := rules[i]
		if want := strconv.Itoa(rule.Number); m[1] != want {
			t.Errorf("%s's rule %d is numbered %s in the code: the numbering is the design's", path, i+1, want)
		}
		if m[2] != rule.Never {
			t.Errorf("%s's rule %s reads %q and the code's Rule.Never reads %q: a reworded rule is reworded in both places",
				path, m[1], m[2], rule.Never)
		}
	}
}

// mobileOrchestratorChecks are the four numbers the register text of Task
// P5.1h took, in the order the register writes them. The design named 110 and
// claimed none (TestDocsMobileOrchestratorClaimsNoCheckNumber); the number the
// task takes is the first one free past every claim at the time it is written,
// which is 660, and each thing the task's Files line names gets its own number
// rather than a sub-letter -- the shape the mobile-auth allocation of
// 2026-11-14 used for its eight (Checks 620 to 627).
var mobileOrchestratorChecks = []string{"660", "661", "662", "663"}

// mobileOrchestratorCheckRE reads a `## Check N` heading as the number it
// carries, the way docs_check600_test.go's own reading does.
var mobileOrchestratorCheckRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// TestDocsMobileOrchestratorChecksAreRegistered: the four checks P5.1h writes
// are registered the register's own way -- a summary row and a `## Check N:`
// section each, the two halves of one statement -- both halves say the check is
// the owner's and has not been run, neither names an installation but a sandbox
// one, and every step is the owner's: no agent runs any. The register-wide
// backstop that would assert the pairing is deliberately absent
// (TestChecklistChecksAreGuarded), so the work that takes the numbers carries
// the assertion for them, and this is that assertion.
func TestDocsMobileOrchestratorChecksAreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, number := range mobileOrchestratorChecks {
		row := checkRow(register, number)
		if row == "" {
			t.Fatalf("%s has no `| %s |` row: Check %s's section is written below it, and the table is where a reader finds it", liveChecksDoc, number, number)
		}
		for _, want := range []string{
			"OWNER-RUN, NOT RUN.",
			"no agent runs any step of it",
			"never `belong`",
			`See "Check ` + number + `" below`,
			// The work and the reading that took the number: a row that stopped
			// naming either is a number a reader cannot check against the
			// register's own numbering paragraph.
			"P5.1h",
			"2026-11-16",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("Check %s's row does not say %q: the row is half the registration, and the other half is the section it points at", number, want)
			}
		}

		section := strings.ToLower(flattenDoc(checkSection(t, register, number)))
		for _, want := range []string{
			"**not run.**",
			// The register's own rule for an owner-run check, and the sentence
			// the four sections carry between them.
			"no agent runs any step of it",
			"never `belong`",
			"p5.1h",
			"mobile/implementation-plan.md",
			"mobile/design/always-on-orchestrator.md",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and how to run it", number, want)
			}
		}
	}

	// What each section settles, read off the sentence that carries it: a
	// section that drifted back to another subject -- or to the design's own
	// draft, which described a service this plan has not built -- would send its
	// owner to read the wrong thing out of a real project.
	for _, want := range []struct {
		number  string
		phrases []string
	}{
		{"660", []string{"minscale", "min-instances = 0", "empty mailbox", "scheduler"}},
		{"661", []string{"with the laptop shut", "job execution", "internal/cli/mailbox.go", "p4.1"}},
		{"662", []string{"destroyed", "2b", "reads a secret value"}},
		{"663", []string{"var.launchers", "roles/firebasedatabase.viewer", "testiampermissions"}},
	} {
		section := strings.ToLower(flattenDoc(checkSection(t, register, want.number)))
		for _, phrase := range want.phrases {
			if !strings.Contains(section, phrase) {
				t.Errorf("Check %s's section no longer says %q: it is what that number was taken to measure, and the task's Files line is what names the four", want.number, phrase)
			}
		}
	}

	// Check 663's storage probes are readings of the `orch` account's own grants,
	// so every `gcloud storage` command in its block has to run as that account:
	// without the flag the command reads your own identity, whose refusal of the
	// bucket root proves nothing (Check 68's rule) and whose writes succeed
	// whatever the account holds -- a false failure, an object no restore step
	// names, and a positive control that controls for nothing. The block, and not
	// the whole section, is what is read: the prose below it names the restore
	// command without repeating the flag.
	section663 := checkSection(t, register, "663")
	block := ""
	if open := strings.Index(section663, "```sh"); open >= 0 {
		if rest := section663[open+len("```sh"):]; strings.Contains(rest, "```") {
			block = rest[:strings.Index(rest, "```")]
		}
	}
	if block == "" {
		t.Fatalf("%s: Check 663's section carries no ```sh command block, and the check is run from it", liveChecksDoc)
	}
	storage := 0
	for _, line := range strings.Split(block, "\n") {
		if !strings.Contains(line, "gcloud storage") {
			continue
		}
		storage++
		if !strings.Contains(line, "--impersonate-service-account=<orch-account>") {
			t.Errorf("Check 663's `%s` does not carry `--impersonate-service-account=<orch-account>`: the storage probes read the `orch` account's own grants, and a denial for your own identity proves nothing", strings.TrimSpace(line))
		}
	}
	if storage < 4 {
		t.Errorf("Check 663's block carries %d `gcloud storage` commands, want at least four: the two writes (the `runs/` positive control and the refused root write) and the two listings", storage)
	}

	// The four are appended where the register's written sections end, in
	// ascending order: the register writes a work's sections after every section
	// already there, and the numbering paragraph's reading -- the number free
	// past every claim -- is a statement about a tree in which nothing was
	// written above them.
	first := strings.Index(register, "\n## Check "+mobileOrchestratorChecks[0]+":")
	if first < 0 {
		t.Fatalf("%s has no `## Check %s:` section", liveChecksDoc, mobileOrchestratorChecks[0])
	}
	highest := 0
	for _, m := range mobileOrchestratorCheckRE.FindAllStringSubmatch(register[:first], -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	if highest == 0 {
		t.Fatalf("%s carries no `## Check` section above Check %s: this assertion would otherwise pass whatever the numbering paragraph says", liveChecksDoc, mobileOrchestratorChecks[0])
	}
	if highest >= 660 {
		t.Errorf("Check 660 is written below a section numbered %d: the number is the first free past every claim, and a higher section already in the file makes that reading wrong", highest)
	}
	prev := first
	for _, number := range mobileOrchestratorChecks[1:] {
		at := strings.Index(register, "\n## Check "+number+":")
		if at < 0 {
			t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, number)
		}
		if at < prev {
			t.Errorf("%s writes Check %s above an older one of the same block: the four are one appended block, and a reader takes a number written out of order for a live one", liveChecksDoc, number)
		}
		prev = at
	}

	// The numbering paragraph above the table is the record of the reading, and
	// it is what a later work reads the register's claims from: it has to name
	// the four, the number taken, and the design's 110 as named and unwritten
	// rather than as this work's.
	claim := strings.Index(register, "Checks 660 to 663 are the always-on orchestrator's")
	table := strings.Index(register, "\n| 660 |")
	if claim < 0 || table < 0 {
		t.Fatalf("%s: the claim paragraph (%d) and the row (%d) are both part of the registration", liveChecksDoc, claim, table)
	}
	if claim > table {
		t.Errorf("the 660–663 claim paragraph is below the table's first row: the paragraph is the record of the number taken and reads with the register's other numbering paragraphs, above the table")
	}
	para := flattenDoc(register[claim:table])
	for _, want := range []string{
		"660 is the first free number past every claim",
		"660–663",
		"110 is not this work's number",
		"110 stays named and unwritten",
		"no agent runs any step of any of them",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("%s's 660–663 claim paragraph does not say %q: the paragraph is the reading the four numbers rest on, and a later work takes the register's claims from it", liveChecksDoc, want)
		}
	}
}
