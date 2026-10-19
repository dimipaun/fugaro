package cli

// The coordinator agent's docs package, pinned.
//
// docs/coordinator/ holds the owner's spec for a per-project coordinator agent
// (spec.md, verbatim), the reconciliation of that spec against the code
// (reconciliation.md), and the index (README.md). The work is reconciliation
// only: no Go, no TypeScript, no Terraform, no rules change, and no plan until
// the owner answers the reconciliation's questions.
//
// The reconciliation's whole value is that every claim in it is checkable, and
// the three things a later edit can silently break are: the spec stops being
// verbatim, a citation drifts to a path that no longer exists, or the work
// starts allocating decision numbers the owner never gave it. Each of the five
// tests below is one of those, plus the two structural facts (the questions are
// numbered and optioned; the index states the status, the owner and the commit
// checked against).
//
//  * TestDocsCoordinatorIsWritten -- the three files exist and carry the
//    load-bearing sentences: the status line, the owner, the date, the commit
//    the reconciliation was run against, and the fact that no question has been
//    answered.
//  * TestDocsCoordinatorSpecIsVerbatim -- spec.md carries the spec's own title,
//    status and eleven sections, and carries none of the reconciliation's
//    apparatus: the two documents have not been mixed.
//  * TestDocsCoordinatorTakesNoDecisionNumber -- neither document allocates a
//    decision number, and both say so.
//  * TestDocsCoordinatorCitationsExist -- every repository path reconciliation.md
//    cites resolves on disk, in either the full form or the
//    deploy/terraform/gcp/-relative form the Terraform modules are cited in, in
//    every citation shape the document uses, and the check is measured against
//    an independent count of the document's path-looking tokens.
//  * TestDocsCoordinatorQuestionsAreNumberedAndOptioned -- Q1..Q16, contiguous,
//    each with at least two options and a recommendation.
//
// Mutation (verified, run and restored) -- thirteen, one per way these documents
// can rot, each planted and reverted:
//
//	TestDocsCoordinatorIsWritten
//	  delete the README's status sentence -> fails, naming it;
//	  delete the reconciliation's "All sixteen are still OPEN" sentence -> fails;
//	  delete the README's "Q1–Q16 are all unanswered" -> fails.
//	TestDocsCoordinatorSpecIsVerbatim
//	  paste a `### Q1 — ` heading into spec.md -> fails;
//	  delete `## 9. Long-running behaviour` -> fails.
//	TestDocsCoordinatorTakesNoDecisionNumber
//	  add a `| **D193** | ... |` row to reconciliation.md -> fails;
//	  drop its "It allocates no decision number" sentence -> fails.
//	TestDocsCoordinatorCitationsExist
//	  rename a cited path to `internal/budget/modelX.go` -> fails, naming it;
//	  cite a nonexistent path in the `path:line Symbol` shape the old regexp
//	  missed (`internal/task/taskNOPE.go:328 NewRunID`) -> fails, naming it,
//	  where before this file's fix it passed;
//	  restore the old shape-blind regexp -> the census assertion fails,
//	  reporting "214 path-looking tokens and the citation check matched 180".
//	TestDocsCoordinatorQuestionsAreNumberedAndOptioned
//	  delete Q9 -> fails on every question after it;
//	  leave Q12 with one option -> fails;
//	  rename Q14's "Recommended" -> fails.
//
// The numbers above are a reading of the document at the commit that last
// edited this comment, not a constant, and they went stale once already: the
// census figures were recorded as 196/163 when the document was smaller, and
// three commits of edits later the same mutation reports 214/180. Anyone
// re-running a mutation here should
// read the count from the assertion's own message and from the check's log line
// ("N citations checked, against a census of M path-looking tokens") rather
// than from this comment, and update the comment to whatever it says. A number
// quoted here is a witness that the mutation fires, not a fact about the test.
//
// The exception list is the one thing here a mutation cannot prove by itself,
// so it is checked the other way: the test fails when an entry goes unused.
//
// KNOWN BLIND SPOT, found by review: TestDocsCoordinatorCitationsExist checks
// that a cited PATH resolves, not that the SYMBOL named beside it is declared
// at the cited line. A sentence reading "`DailyMicros` at
// `internal/budget/evaluate.go:31`" passed the whole package while DailyMicros
// is declared in internal/budget/model.go -- evaluate.go:31 is
// ReasonRepoDailyCap. The claim the reconciliation makes about itself ("every
// path and symbol was read at that commit") is therefore only as strong as the
// reading; this test does not back it. A second review round found the other
// half of the same problem in the extractor itself: the regexp required the
// closing backtick to follow the path or its `:line` suffix, so the document's
// `path:line Symbol` shape never matched and nine cited paths were never
// checked. Both are why the census count is here: a check that silently sees
// less is the failure mode, and a count is what turns it into a failure.
//
// A checker for that was prototyped and deliberately not shipped: pairing
// identifiers with citations needs a notion of scope the prose does not carry
// (one parenthetical may cite a single file for a list drawn from three, and a
// paragraph names symbols belonging to other citations in it), and the coarse
// version reported hundreds of false positives -- every symbol in a paragraph
// paired with every path in it. Doing it properly means reading the sentences,
// which is what a reviewer does. Until then this is a partial check, and it is
// documented as one rather than presented as a complete one.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	coordinatorReadme     = "docs/coordinator/README.md"
	coordinatorSpec       = "docs/coordinator/spec.md"
	coordinatorReconciled = "docs/coordinator/reconciliation.md"
)

// coordinatorCheckedAgainst is the commit the reconciliation was run against:
// the release commit of v0.8.6, which is `origin/main` when this package was
// written. It is a separate constant because the test asserts the documents
// name it, not that HEAD is it -- the reconciliation is a dated reading of one
// commit, and a later commit does not make it wrong.
const coordinatorCheckedAgainst = "c6009b5f"

// coordinatorSections are the spec's own section headings, in order: the pin
// that spec.md is the document the owner wrote and not a summary of it.
var coordinatorSections = []string{
	"## 1. Problem",
	"## 2. Goal",
	"## 3. Principles",
	"## 4. Deployment",
	"## 5. State",
	"## 6. Inputs",
	"## 7. Interface: MCP front door, Firestore behind",
	"## 8. Models",
	"## 9. Long-running behaviour",
	"## 10. Console integration",
	"## 11. Open questions",
}

// TestDocsCoordinatorIsWritten: the package exists, and the index states the
// four facts a reader has to have before they trust anything in it -- that
// nothing is implemented, who owns it, when it was written, and which commit it
// was checked against.
func TestDocsCoordinatorIsWritten(t *testing.T) {
	for _, path := range []string{coordinatorReadme, coordinatorSpec, coordinatorReconciled} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s does not exist: %v", path, err)
		}
	}
	readme := readDoc(t, coordinatorReadme)
	for _, want := range []string{
		"reconciliation only; nothing is implemented; the plan waits for the owner's answers",
		"**Owner: Dimi Paun. Date: 2026-10-19.**",
		coordinatorCheckedAgainst,
		"**None.** The spec is copied byte for byte",
		"its open items are **Q1–Q16**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", coordinatorReadme, want)
		}
	}
	// The reconciliation states the commit and the date itself, not only the
	// index: it is the document a reader cites, and it has to stand alone.
	reconciled := readDoc(t, coordinatorReconciled)
	if !strings.Contains(reconciled, coordinatorCheckedAgainst) {
		t.Errorf("%s never names the commit it was checked against (%s)", coordinatorReconciled, coordinatorCheckedAgainst)
	}
	if !strings.Contains(reconciled, "2026-10-19") {
		t.Errorf("%s never states the date it was written", coordinatorReconciled)
	}
	// Both documents say, in their own words, that no question has been
	// answered. This is the one fact in the package that no other check can
	// see and that a later edit can quietly invert -- a reconciliation whose
	// recommendations start reading as rulings is the failure this work's
	// no-D-number rule exists to prevent, and it would arrive as prose rather
	// than as a table row.
	for path, want := range map[string]string{
		coordinatorReadme:     "Q1–Q16 are all unanswered",
		coordinatorReconciled: "**All sixteen are still OPEN.** The owner has answered none of them.",
	} {
		if !strings.Contains(readDoc(t, path), want) {
			t.Errorf("%s no longer says the questions are unanswered: it lacks %q. The owner has not answered any of Q1–Q16, and a document that stops saying so reads as though they were decided", path, want)
		}
	}
}

// TestDocsCoordinatorSpecIsVerbatim: spec.md is the owner's document and
// nothing else. It carries the spec's own title, its status line and all eleven
// of its sections, in order -- and it carries none of the reconciliation's
// apparatus. A reconciliation paragraph pasted into the spec, or a section
// dropped from it, would leave the next reader unable to tell which of the two
// documents is the owner's.
func TestDocsCoordinatorSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, coordinatorSpec)
	for _, want := range []string{
		"# Fugaro Coordinator Agent — Design Spec",
		"*Status: draft, Oct 2026*",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", coordinatorSpec, want)
		}
	}
	// In order, not merely present: a section moved is a section edited.
	at := 0
	for _, heading := range coordinatorSections {
		i := strings.Index(spec[at:], heading)
		if i < 0 {
			t.Errorf("%s lacks the section heading %q, or carries it out of order", coordinatorSpec, heading)
			continue
		}
		at += i + len(heading)
	}
	// The reconciliation's own vocabulary, which must never appear in the
	// owner's text: a finding number, a question heading, a classification.
	for _, notWant := range []string{
		"### Q1 —", "**Wrong / Exists", "UNVERIFIED", "Finding #", "reconciliation.md",
	} {
		if strings.Contains(spec, notWant) {
			t.Errorf("%s carries %q, which belongs to the reconciliation: the spec is the owner's text verbatim", coordinatorSpec, notWant)
		}
	}
}

// coordinatorAllocationRowRE matches a decision-allocation table row: a row
// whose FIRST cell is exactly one decision number, `| **D193** | ... |`. It is
// deliberately anchored on the closing `|` of the first cell, so the index's
// range rows (`| **D1–D192** |`, `| **D193 onward** |`) are not matches: those
// report the allocation, they do not take a number from it.
var coordinatorAllocationRowRE = regexp.MustCompile(`(?m)^\|\s*\*{0,2}D[0-9]+[a-z]?\*{0,2}\s*\|`)

// TestDocsCoordinatorTakesNoDecisionNumber: this work allocates no decision
// number. Its open items are questions Q1-Q16, and the owner's answer to one is
// what would turn it into a decision -- which is exactly what the spec's own
// "the plan waits for the owner's answers" means. A D-number appearing here
// would take a number out of the block the repository's allocation records
// (D193 onward is the next free one) without the owner having given it.
func TestDocsCoordinatorTakesNoDecisionNumber(t *testing.T) {
	for _, path := range []string{coordinatorReadme, coordinatorReconciled} {
		doc := readDoc(t, path)
		if m := coordinatorAllocationRowRE.FindString(doc); m != "" {
			t.Errorf("%s allocates a decision number: %q. This work's open items are Q1-Q16; the next free D block is D193 onward and is not this work's to take", path, strings.TrimSpace(m))
		}
	}
	// Both documents have to SAY it, not merely avoid it: a reader who finds
	// Q-numbers and no D-numbers needs the reason in front of them.
	for _, tc := range []struct{ path, want string }{
		{coordinatorReadme, "This work takes **no D-number**"},
		{coordinatorReconciled, "It allocates no decision number"},
	} {
		if !strings.Contains(readDoc(t, tc.path), tc.want) {
			t.Errorf("%s does not say that this work allocates no decision number: it lacks %q", tc.path, tc.want)
		}
	}
}

// coordinatorCitationRE finds a backtick-quoted repository path, in EVERY shape
// the reconciliation cites one in:
//
//	`internal/budget/model.go:1-2`         path, line range
//	`SECURITY.md:40`                      path and one line
//	`internal/task/task.go:328 NewRunID`  path, line, and the symbol there
//	`internal/cli/init.go:1494 installRoot`
//	the console's `main/spawn.ts:326+`        a line and an open end
//	`internal/budget/model.go:151,164`    a list of lines
//	`internal/pricing/table.go Embedded()` a path and a symbol, no line
//	`internal/budget/rules/rules.json.tmpl` a path alone
//
// The shapes after the path are the document's dominant ones and the reason
// this regexp is written the way it is. An earlier version required the closing
// backtick to follow the path (or its `:line` suffix) immediately, which is
// true of two of those shapes and false of the rest: `path:line Symbol` never
// matched, so nine cited paths were never checked at all, and no test could
// see it because the check simply found fewer citations and said nothing. The
// shape after the path is therefore written as "whatever is not a backtick or
// a newline", and coordinatorPathCensusRE below is the independent count that
// makes a regexp which matches nothing a failure rather than a silence.
//
// The extension set is the one this repository's cited files use; a backticked
// token without one (a node path such as `config/policy/shared`, a package
// name, a symbol on its own) is not a citation of a file and is left alone.
// The path character class carries `*` for the two globs the document names
// (`internal/cli/docs_*_test.go`, `docs/design/credentials-service*.md`); both
// are audited exceptions below, not files.
var coordinatorCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b)(?::[0-9]+(?:[-,+][0-9]*)*)?[^`\\n]*`")

// coordinatorPathCensusRE is the independent count of how many path-looking
// tokens the document holds, and it is deliberately dumber than the citation
// regexp: any backticked token that carries a known extension anywhere in it,
// whether the path starts the token or not, and whatever follows it. It is the
// floor the citation check is measured against, so that a regexp which has
// stopped matching the document's shapes fails the test instead of passing it
// vacuously -- the failure mode that hid the `path:line Symbol` gap.
//
// Two regexps rather than one on purpose: a census computed with the same
// regexp as the check would shrink with it and could never catch it.
var coordinatorPathCensusRE = regexp.MustCompile("`[^`\\n]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b[^`\\n]*`")

// coordinatorCitationPrefixes are the directories a citation may be relative to
// besides the repository root. The Terraform modules are cited the way
// SECURITY.md cites them -- `modules/project/`, not the whole
// `deploy/terraform/gcp/` path -- so the test accepts both forms rather than
// forcing one spelling on the prose.
var coordinatorCitationPrefixes = []string{"", "deploy/terraform/gcp/"}

// coordinatorRootFiles are the repository's own top-level documents, which a
// citation may name with no directory at all. Everything else has to look like
// a path (contain a `/`), so that a bare filename in the prose -- a run
// artifact such as `launch.json`, or a shortened second reference to a file
// already cited in full -- is not mistaken for a citation of a file.
var coordinatorRootFiles = map[string]bool{"SECURITY.md": true, "README.md": true, "CONTRIBUTING.md": true}

// coordinatorCitationExceptions are citations that deliberately name something
// that is not a file in this repository at this commit, each with the reason.
// Every one is an audited case, not a blanket skip, and the test fails if an
// entry here goes unused: an exception that outlives the sentence it was
// written for is a hole in the check.
var coordinatorCitationExceptions = map[string]string{
	"CLAUDE.md":                             "§0 records that there is NO root CLAUDE.md; naming the absent file is the point of the sentence",
	"docs/design/always-on-orchestrator.md": "the always-on orchestrator design, in open PR #632 and not on main; §2 #29 says so",
	"docs/design/console-styling.md":        "the console styling design, in open PR #634 and not on main; U7 says so",
	"docs/design/credentials-service*.md":   "§0.3 quotes the brief's own glob, which names no file: the documents are at docs/credentials-service/, and the point of the sentence is that docs/design/ has no such subdirectory",
	"docs/releases/vX.Y.Z.md":               "the naming pattern of the release notes, not one file",
	"internal/cli/docs_*_test.go":           "§0.4 names the pattern of the docs packages' pinning tests -- `*.go` is not a filename -- as the shape this file's tests were written from",
}

// TestDocsCoordinatorCitationsExist: every repository path reconciliation.md
// cites resolves. This is the test that keeps a reconciliation honest: its
// claims are only worth as much as the files behind them, and a path that moved
// turns a checked finding into an unchecked one without any other symptom.
func TestDocsCoordinatorCitationsExist(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	// The census first, and independently: how many path-looking tokens the
	// document holds at all. The citation check below has to see at least that
	// many, so a regexp that stopped matching a shape the prose uses fails
	// rather than quietly checking less.
	census := len(coordinatorPathCensusRE.FindAllString(doc, -1))
	matches := coordinatorCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) < census {
		t.Fatalf("%s holds %d path-looking tokens and the citation check matched %d of them: the document cites a shape coordinatorCitationRE no longer knows, so those citations are unchecked",
			coordinatorReconciled, census, len(matches))
	}
	seen := map[string]bool{}
	used := map[string]bool{}
	cites := 0
	for _, m := range matches {
		path := m[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		// The exceptions are checked first, so that one naming a bare filename
		// (`CLAUDE.md`) is not swallowed by the path test below.
		if reason, ok := coordinatorCitationExceptions[path]; ok {
			used[path] = true
			t.Logf("%s cites %q, which is not a file here: %s", coordinatorReconciled, path, reason)
			continue
		}
		// A citation is a path, or one of the repository's own root documents.
		// A bare filename is a run artifact or a shortened second reference,
		// and is not a claim about a file in this tree.
		if !strings.Contains(path, "/") && !coordinatorRootFiles[path] {
			continue
		}
		cites++
		var tried []string
		ok := false
		for _, prefix := range coordinatorCitationPrefixes {
			full := prefix + path
			tried = append(tried, full)
			if _, err := os.Stat("../../" + filepath.FromSlash(full)); err == nil {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%s cites %q, which does not exist (tried %s): a reconciliation whose citations have moved is a reconciliation nobody can check",
				coordinatorReconciled, path, strings.Join(tried, ", "))
		}
	}
	// An exception nobody uses is a hole in this check that no one is watching.
	for path := range coordinatorCitationExceptions {
		if !used[path] {
			t.Errorf("%s no longer cites %q, but coordinatorCitationExceptions still excuses it: delete the entry, or the next citation of that name is unchecked", coordinatorReconciled, path)
		}
	}
	// The number this test checked, said out loud, so that the count is
	// visible in the run rather than a fact nobody reads: a later edit that
	// halves it is a line in the log, and the census assertion above is what
	// makes it a failure when it should be.
	t.Logf("%s: %d citations checked, against a census of %d path-looking tokens in the document", coordinatorReconciled, cites, census)
}

// coordinatorQuestionRE finds a question heading in the reconciliation's
// "Open for the owner" section: `### Q7 — Event intake: ...`.
var coordinatorQuestionRE = regexp.MustCompile(`(?m)^### Q([0-9]+) — `)

// TestDocsCoordinatorQuestionsAreNumberedAndOptioned: the reconciliation's
// questions are Q1..Q16, contiguous and in order, and each one gives the owner a
// real choice -- at least two options, and a recommendation among them. A
// question with one option is a decision wearing a question's clothes, which is
// the thing this work's "no D-number" rule exists to prevent.
func TestDocsCoordinatorQuestionsAreNumberedAndOptioned(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	const heading = "## 3. Open for the owner (Q1–Q16)"
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s has no %q section", coordinatorReconciled, heading)
	}
	section, _, _ := strings.Cut(doc[start+len(heading):], "\n## ")
	bounds := coordinatorQuestionRE.FindAllStringSubmatchIndex(section, -1)
	if len(bounds) == 0 {
		t.Fatal("the questions section carries no `### Q<n> — ` heading: this test would otherwise check nothing")
	}
	for i, b := range bounds {
		n, err := strconv.Atoi(section[b[2]:b[3]])
		if err != nil {
			t.Fatalf("question heading %q does not carry a number: %v", section[b[0]:b[1]], err)
		}
		if n != i+1 {
			t.Errorf("the questions run %d, %d, ...: question %d is out of order or missing", i, n, i+1)
		}
		end := len(section)
		if i+1 < len(bounds) {
			end = bounds[i+1][0]
		}
		body := section[b[0]:end]
		if options := strings.Count(body, "- **("); options < 2 {
			t.Errorf("Q%d offers %d option(s): every question here gives the owner at least two, or it is a decision and not a question", n, options)
		}
		if !strings.Contains(body, "Recommended") {
			t.Errorf("Q%d names no recommendation: the owner is owed the recommended option and why", n)
		}
	}
	if len(bounds) != 16 {
		t.Errorf("the questions section carries %d questions, and this work's index says Q1–Q16", len(bounds))
	}
	// The index's count and the section's count are the same fact written
	// twice; a later edit that adds a question has to update both.
	readme := readDoc(t, coordinatorReadme)
	if !strings.Contains(readme, "Q1–Q16") {
		t.Errorf("%s no longer says the questions are Q1–Q16, while the section carries %d of them", coordinatorReadme, len(bounds))
	}
	// And the section heading itself names the range, so the two cannot drift
	// apart without one of them looking wrong.
	wantHeading := "## 3. Open for the owner (Q1–Q" + strconv.Itoa(len(bounds)) + ")"
	if !strings.Contains(doc, wantHeading) {
		t.Errorf("%s's questions section is headed %q while it carries %d questions: the heading and the count disagree", coordinatorReconciled, heading, len(bounds))
	}
}
