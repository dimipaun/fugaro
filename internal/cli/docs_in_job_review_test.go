package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The two documents this work added, and the index that names them. There is
// no test in this package that walks docs/ or pins the set of docs folders
// (each docs package is pinned by its own file, like this one), so the folder
// and its file set are pinned here.
const (
	inJobReviewREADME  = "docs/in-job-review/README.md"
	inJobReviewSpec    = "docs/in-job-review/spec.md"
	inJobReviewRecon   = "docs/in-job-review/reconciliation.md"
	inJobReviewStatus  = "**Status: reconciliation only; no code is implemented; the plan waits for the owner's answers.**"
	inJobReviewSubject = "Fugaro: In-Job Review Spec"
)

// TestDocsInJobReviewIsWritten: the folder exists with its index and both
// documents, the index carries the brief's status line word for word and
// names the other two files, and the reconciliation carries the structure the
// brief asked for — a headline table, findings classified Wrong / Exists /
// Gap / Rule / Open and numbered from #1 without a gap, a section "Open for
// the owner", and an UNVERIFIED table. Its red state is this test: the folder
// did not exist.
func TestDocsInJobReviewIsWritten(t *testing.T) {
	readme := readDoc(t, inJobReviewREADME)
	spec := readDoc(t, inJobReviewSpec)
	recon := readDoc(t, inJobReviewRecon)

	for _, want := range []string{
		inJobReviewStatus,
		// The owner and the dates, as the brief states them.
		"**Owner:** Dimi Paun",
		"2026-10-18",
		"2026-10-19",
		// The index names both documents it indexes.
		"[spec.md](spec.md)",
		"[reconciliation.md](reconciliation.md)",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", inJobReviewREADME, want)
		}
	}

	if !strings.Contains(spec, inJobReviewSubject) {
		t.Errorf("%s is not the owner's in-job review spec", inJobReviewSpec)
	}

	for _, want := range []string{
		// The headline table and its columns.
		"| Spec concept | What exists, and under what name | New? |",
		// Every section the brief asked for.
		"## 2. Findings",
		"## 4. Open for the owner",
		"## 6. UNVERIFIED",
		// The revision this was read at, so a later run can tell when it ages.
		"Checked against:** `origin/main` at `82ba600",
		"2026-10-19",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", inJobReviewRecon, want)
		}
	}

	// The findings: numbered from #1 with no gap, and each classified with one
	// of the five words. A skipped number or a word outside the set fails. The
	// class word opens the parenthesis and may be followed by more text (some
	// findings say why they are classified as they are).
	label := regexp.MustCompile(`(?m)^\*\*#([0-9]+) \(([A-Za-z]+)`)
	known := map[string]bool{"Wrong": true, "Exists": true, "Gap": true, "Rule": true, "Open": true}
	classes := map[string]int{}
	nums := []int{}
	for _, m := range label.FindAllStringSubmatch(recon, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("finding label %q does not carry a number", m[0])
		}
		nums = append(nums, n)
		if !known[m[2]] {
			t.Errorf("finding #%d is classified %q: every finding carries one of Wrong, Exists, Gap, Rule, Open", n, m[2])
			continue
		}
		classes[m[2]]++
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no finding of the form **#N (Class).**", inJobReviewRecon)
	}
	for i, n := range nums {
		if n != i+1 {
			t.Fatalf("finding #%d is out of order: findings are %v (want #1..#%d with no gap)", n, nums, len(nums))
		}
	}
	for _, c := range []string{"Wrong", "Exists", "Gap", "Rule", "Open"} {
		if classes[c] == 0 {
			t.Errorf("%s classifies no finding as %s: every finding carries one of Wrong, Exists, Gap, Rule, Open", inJobReviewRecon, c)
		}
	}
	// The questions: numbered from Q1 with no gap, so "Open for the owner" is a
	// list a reader can count, and the index's own "Q1–Q17" is true.
	question := regexp.MustCompile(`(?m)^\*\*Q([0-9]+)\.`)
	qnums := []int{}
	for _, m := range question.FindAllStringSubmatch(recon, -1) {
		n, _ := strconv.Atoi(m[1])
		qnums = append(qnums, n)
	}
	if len(qnums) == 0 {
		t.Fatalf("%s states no question of the form **QN.**", inJobReviewRecon)
	}
	for i, n := range qnums {
		if n != i+1 {
			t.Fatalf("question Q%d is out of order: questions are %v (want Q1..Q%d with no gap)", n, qnums, len(qnums))
		}
	}
	if !strings.Contains(readme, "**Q1–Q"+strconv.Itoa(len(qnums))+"**") {
		t.Errorf("%s does not state the range Q1–Q%d the reconciliation actually carries (%d questions)",
			inJobReviewREADME, len(qnums), len(qnums))
	}
	if !strings.Contains(readme, "**#1–#"+strconv.Itoa(len(nums))+"**") {
		t.Errorf("%s does not state the range #1–#%d the reconciliation actually carries (%d findings)",
			inJobReviewREADME, len(nums), len(nums))
	}
}

// TestDocsInJobReviewSpecIsVerbatim: spec.md is the owner's text of 2026-10-18,
// unchanged — the brief's rule is that only obvious typos may be fixed, and
// none needed fixing, so the title, the status line and every section heading
// are pinned here. A later edit that "improves" the spec, drops a section or
// reorders one fails, which is the point: the spec is the owner's, and the
// document that may change is the reconciliation.
func TestDocsInJobReviewSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, inJobReviewSpec)
	for _, want := range []string{
		"# " + inJobReviewSubject,
		"Status: Draft · Oct 18, 2026 · Owner: Dimi Paun",
		"## 1. Problem",
		"## 2. Goal",
		"## 3. Job flow",
		"## 4. Review plans",
		"## 5. Running reviews",
		"## 6. Findings and verdict",
		"## 7. Review-only jobs",
		"## 8. Rebase and stale base",
		"## 9. Timeouts and time budget",
		"## 10. Calibration",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q: the spec is the owner's text, unchanged", inJobReviewSpec, want)
		}
	}
	// The brief's own marker must not have been copied in with the text.
	for _, absent := range []string{"=== SPEC TEXT ===", "=== END OF SPEC TEXT ==="} {
		if strings.Contains(spec, absent) {
			t.Errorf("%s carries the brief's own marker %q: only the spec text between the markers belongs here", inJobReviewSpec, absent)
		}
	}
	// The reconciliation is the document that states the spec was not changed.
	recon := readDoc(t, inJobReviewRecon)
	if !strings.Contains(strings.ToLower(recon), "no typo needed fixing") &&
		!strings.Contains(strings.ToLower(recon), "nothing was changed") &&
		!strings.Contains(strings.ToLower(recon), "verbatim") {
		t.Errorf("%s never states that %s is the owner's text unchanged", inJobReviewRecon, inJobReviewSpec)
	}
}

// TestDocsInJobReviewStatesTheBriefCorrections: two claims the brief this work
// was written from makes are wrong about this tree, and both corrections are
// load-bearing — the first decides what a "Rule" finding can cite, the second
// decides whether the spec's section 8 is an assumption or a question for the
// owner. Each must be stated in the reconciliation and summarised in the
// index, so a later reader cannot act on the brief's version by accident.
func TestDocsInJobReviewStatesTheBriefCorrections(t *testing.T) {
	recon := readDoc(t, inJobReviewRecon)
	readme := readDoc(t, inJobReviewREADME)
	if _, err := os.Stat("../../CLAUDE.md"); err == nil {
		t.Errorf("CLAUDE.md now exists: %s's correction that this repository has none is stale, and the Rule class must say what it cites",
			inJobReviewRecon)
	}
	if _, err := os.Stat("../../.claude/rules"); err == nil {
		t.Errorf(".claude/rules now exists: %s's correction that this repository has none is stale", inJobReviewRecon)
	}
	// What each document must carry, as the substance of each correction and
	// not merely a mention of the symbol: a document that says the runner never
	// force-pushes while quoting --force-with-lease in an example must fail.
	for _, doc := range []struct {
		path string
		text string
		want []string
	}{
		{inJobReviewRecon, recon, []string{
			// Correction 1: no CLAUDE.md, no .claude/rules here.
			"CLAUDE.md",
			".claude/rules",
			// Correction 2: the runner does force-push its own run branch, with
			// a lease; checkpoint pushes are the fast-forward-only ones; the
			// base branch is never pushed at all.
			"does** force-push",
			"force-with-lease",
			"fast-forward only",
			"base branch is never pushed",
			"PushFastForward",
		}},
		{inJobReviewREADME, readme, []string{
			"CLAUDE.md",
			".claude/rules",
			"force-with-lease",
			"fast-forward only",
			"base branch is never pushed",
			"PushFastForward",
		}},
	} {
		for _, want := range doc.want {
			if !strings.Contains(doc.text, want) {
				t.Errorf("%s lacks %q: the brief's wrong version of this fact must not be left standing alone", doc.path, want)
			}
		}
	}
}

// TestDocsInJobReviewAllocatesNoDecisionsOrChecks: the index's own "numbers
// this work holds" table says no D-number is allocated and no check number is
// claimed, because every finding that needs the owner is a Q. This asserts the
// claim rather than trusting it: no decision-row label (**D123**) appears in
// either document, the table says so in words, and docs/gcp-live-checklist.md
// was not touched to claim a number.
func TestDocsInJobReviewAllocatesNoDecisionsOrChecks(t *testing.T) {
	decisionRow := regexp.MustCompile(`(?m)\*\*D[0-9]+\*\*`)
	for _, path := range []string{inJobReviewRecon, inJobReviewREADME} {
		if m := decisionRow.FindAllString(readDoc(t, path), -1); len(m) > 0 {
			t.Errorf("%s carries decision labels %v: this work allocates no D-number, so every finding that needs the owner is a Q",
				path, m)
		}
	}
	readme := readDoc(t, inJobReviewREADME)
	for _, want := range []string{
		"| **D-numbers** | **none allocated.**",
		"| **Check numbers** | **none claimed.**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q: the numbers this work holds must say it holds none", inJobReviewREADME, want)
		}
	}
}

// TestDocsInJobReviewCitationsExist: every load-bearing citation in the two
// documents is a path a later implementer has to find — the symbols the
// findings rest on, the file that carries each rule the spec runs into, and
// the documents the reconciliation is read against. A substring assertion
// cannot see one of them move, so each is stat'ed here as well as required in
// the text.
//
// The list is the documents' own citations, so one that leaves the text must
// leave this test too (the test says so when it fails). Every path was read on
// `main` on 2026-10-19 and is in this tree.
func TestDocsInJobReviewCitationsExist(t *testing.T) {
	both := readDoc(t, inJobReviewREADME) + readDoc(t, inJobReviewRecon)
	for _, cited := range []string{
		// The loop the spec's section 3 describes, as it exists.
		"internal/recipe",
		"internal/runner/plan.go",
		"internal/runner/runner.go",
		"internal/runner/prompts.go",
		"internal/runner/outcome.go",
		"internal/runner/budget.go",
		"internal/runner/lockcache.go",
		// The verdict contract the spec's section 6 changes.
		"internal/runner/verdict.go",
		"internal/runstore/runstore.go",
		"internal/runstore/cost.go",
		"internal/runner/cost.go",
		"internal/runner/policy.go",
		"schemas/result.schema.json",
		// The reconciliation also cites the console's own json-contract.md and
		// its shared schema module (finding #26: the console reads the CLI's
		// JSON, never result.json, and keys its documents by `shape`). Those two
		// are deliberately NOT stat'ed here, and the path is not even spelled
		// out in this comment: this is a .go file of the module, and
		// TestConsoleIsOutsideTheGoModule's consoleBoundaryAllowList is a
		// one-entry exception (the console contract's own docs test) that a
		// second entry may only widen by argument. That boundary scans a Go
		// file's text, comments included, but not the markdown of docs/, so the
		// pin stops at the module's edge and the claim is carried by the prose.
		"internal/watch/plain.go",
		"internal/watch/render.go",
		"internal/watch/finished.go",
		"internal/watch/filter.go",
		// Where a review plan would have to ride, and what it costs to add.
		"internal/task",
		"internal/cli/recipes_skew.go",
		"internal/config/scope.go",
		"internal/config/docs_scope_test.go",
		"internal/config/defaults.go",
		"internal/config/validate.go",
		"internal/projectpolicy",
		// Models, credentials and the spend they carry.
		"internal/config/providers.go",
		"internal/config/routing.go",
		"internal/runner/gateway.go",
		"internal/runner/providerkey.go",
		"internal/gateway/route.go",
		"internal/pricing/table.go",
		"internal/budget/lease.go",
		"internal/budget/model.go",
		"internal/cli/budget_preview.go",
		"internal/infra/spec.go",
		"internal/localcfg",
		"fugaro.yaml",
		// Rebasing, pushing and the follow-up the spec's section 8 changes.
		"internal/gitops/gitops.go",
		"internal/gitops/checkpoint.go",
		"internal/gitops/rejected.go",
		"internal/runner/followup.go",
		"internal/cli/followup.go",
		"internal/followup",
		"deploy/terraform/gcp/modules/workflow/job.tf",
		"deploy/terraform/gcp/modules/project/job.tf",
		// What a reviewer is, and what it is not.
		"internal/agent/agent.go",
		"internal/agent/settings.go",
		"internal/agent/redact.go",
		"internal/runner/reviewmode.go",
		"internal/backend/gcp/limits.go",
		"internal/backend/gcp/run.go",
		"internal/blobx",
		// Reading it back: the CLI surfaces the spec's verdict would reach.
		"internal/cli/run.go",
		"internal/cli/diagnose.go",
		"internal/cli/root.go",
		"internal/cli/exec.go",
		// The documents it is read against.
		"SECURITY.md",
		"docs/recipes.md",
		"docs/dogfooding.md",
		"docs/git-providers.md",
		"docs/multi-model.md",
		"docs/gcp-live-checklist.md",
		"docs/design/recipes.md",
		"docs/design/generic-tool.md",
		"docs/design/mixed-credentials.md",
		"docs/design/project-centric.md",
		"docs/design/artifact-runs.md",
		"docs/design/named-oauth-tokens.md",
		"docs/design/budget-token-channel.md",
		"docs/plans/2026-10-14-oauth-reviewer-stage.md",
		"docs/credentials-service/README.md",
		"docs/credentials-service/reconciliation.md",
		"docs/credentials-service/implementation-plan.md",
	} {
		// The documents link their neighbours relatively (../recipes.md,
		// ../../SECURITY.md), so a citation counts in either form.
		rel, err := filepath.Rel("docs/in-job-review", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(both, cited) && !strings.Contains(both, rel) {
			t.Errorf("docs/in-job-review no longer cites %s: this list is the documents' own citations, so one that has left the text must leave this test", cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("docs/in-job-review cites %s, which is not in the tree: %v", cited, err)
		}
	}
}
