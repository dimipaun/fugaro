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
// starts allocating decision numbers the owner never gave it. Each of the seven
// tests below is one of those, plus the two structural facts (the questions are
// numbered and optioned; the index states the status, the owner and the commit
// checked against) and one that reads this file rather than the documents (the
// mutation record's count against the list it heads).
//
//  * TestDocsCoordinatorIsWritten -- the three files exist and carry the
//    load-bearing sentences: the status line, the owner, the date, the commit
//    the reconciliation was run against, and the two facts that arrived with
//    the owner's answers of 2026-10-19: all sixteen questions are answered, and
//    none of the answers is an approval.
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
//  * TestDocsCoordinatorRelativeCitationsResolve -- every relative second
//    reference (`:89`, `:14-24`, `:127 notCovered`) resolves against the
//    nearest full-path citation before it in its own paragraph, to a line that
//    exists, and -- where a symbol is named -- to a symbol within a few lines
//    of it.
//  * TestDocsCoordinatorQuestionsAreNumberedAndOptioned -- Q1..Q16, contiguous,
//    each with at least two options, a recommendation and the owner's recorded
//    answer of 2026-10-19.
//  * TestDocsCoordinatorMutationRecordIsCounted -- the mutation record's own
//    count, checked against the list under it. The record is prose and cannot
//    be run, so the one thing about it that can be checked is that it does not
//    claim more mutations than it lists: each mutation is one bullet carrying
//    one arrow, and the header states the total in digits for this test to read
//    and compare. The count had drifted by two -- "twenty-six" over a list of
//    twenty-four -- which is the same silent-overclaim the citation census
//    exists to catch in the document.
//
// Mutation (verified, run and restored) -- 25, one per way these documents can
// rot, each planted and reverted. The count is a digit and not a word, and
// there is only one of it, because TestDocsCoordinatorMutationRecordIsCounted
// reads it and counts the list under it: one arrow per mutation, so a number
// the list does not bear out fails the package like any other count that claims
// more than it sees. Written as a word it drifted -- "twenty-six" over a list
// of twenty-four -- and a word beside a digit would have let the two disagree
// without either looking wrong.
//
//	TestDocsCoordinatorIsWritten
//	  delete the README's status sentence -> fails, naming it;
//	  delete the reconciliation's "The owner answered all sixteen" sentence ->
//	  fails;
//	  delete the README's "All sixteen questions are answered" sentence -> fails;
//	  drop "and none is approved" from the README's answered sentence -> fails:
//	  what is left says the questions were answered, not that answering one
//	  approves nothing;
//	  delete the README's "An answer is not an approval" sentence -> fails;
//	  revert the README's numbers row to "its open items are Q1–Q16" -> fails.
//	TestDocsCoordinatorSpecIsVerbatim
//	  paste a `### Q1 — ` heading into spec.md -> fails;
//	  delete `## 9. Long-running behaviour` -> fails.
//	TestDocsCoordinatorTakesNoDecisionNumber
//	  add a `| **D193** | ... |` row to reconciliation.md -> fails;
//	  drop its "It allocates no decision number" sentence -> fails.
//	TestDocsCoordinatorCitationsExist
//	  rename a cited path to `internal/budget/modelX.go` -> fails, naming it;
//	  cite a nonexistent path in the `path:line Symbol` shape the old regexp
//	  missed (`internal/task/taskNOPE.go:328 NewRunID`) -> fails, naming it.
//	  Run against the old regexp with the census check removed, the same
//	  citation is not reported at all -- that is the hole this shape closed,
//	  and the reason the census is checked first;
//	  restore the old shape-blind regexp -> the census assertion fails,
//	  reporting "217 path-looking tokens and the citation check matched 182";
//	  make the regexp line-bounded again -> fails in the relative check below,
//	  because that loses the anchor `:127 notCovered` resolves against.
//	TestDocsCoordinatorRelativeCitationsResolve
//	  put the literal `:...` back where the F36 line reference is -> fails: a
//	  placeholder is not a line number;
//	  cite `:14024` against `internal/gateway/gateway.go`, a 573-line file ->
//	  fails, past the end of the file;
//	  cite `:1190 Start` for the same -> fails the same way, and does not slice
//	  out of range: the symbol window is only reached for a line that is there;
//	  name `:209 denyWindowGone` -> fails: no such symbol near the line;
//	  write the same reference as a bare `:83 tokenPermissions` after an anchor
//	  that is another file (secretmounts_test.go) -> fails on the symbol. This
//	  is the document defect this check was written for, reproduced: the
//	  paragraph's last full path before the reference was not the file the
//	  reference meant, and nothing could see it;
//	  delete the full-path citation that anchors the D143/D145 bullets ->
//	  fails three times, naming the references with no anchor.
//	TestDocsCoordinatorQuestionsAreNumberedAndOptioned
//	  delete Q9 -> fails on every question after it;
//	  leave Q12 with one option -> fails;
//	  rename Q14's "Recommended" -> fails;
//	  restore the section heading "## 3. Open for the owner (Q1–Q16)" -> fails:
//	  the questions are answered now, and the heading says which;
//	  strip the "Answered 2026-10-19: option (A)" line out of Q7 -> fails,
//	  naming Q7: a question with no answer reads as one that is still open.
//
// The numbers above are a reading of the document at the commit that last
// edited this comment, not a constant, and they have gone stale every time the
// document has grown: the census figures were recorded as 196/163 when the
// document was smaller, then as 214/180 three commits later, and the same
// mutation now reports 217/186. The live readings are the assertion's own
// message and the two log lines -- "N citations checked, against a census of M
// path-looking tokens" and "N relative citations resolved against their
// paragraph's anchor". Anyone re-running a mutation should read the numbers
// there rather than here, and update this comment to whatever they say: a
// number quoted here is a witness that the mutation fires, not a fact about the
// test.
//
// The exception list is the one thing here a mutation cannot prove by itself,
// so it is checked the other way: the test fails when an entry goes unused.
//
// THREE HOLES, FOUND BY REVIEW IN THREE ROUNDS, each closed by the round that
// found it or recorded here as still open. They are worth keeping together
// because they are the same failure -- a check that silently sees less than it
// claims to -- arriving three times.
//
//  1. The symbol named BESIDE a citation was not checked. A sentence reading
//     "`DailyMicros` at `internal/budget/evaluate.go:31`" passed the whole
//     package while DailyMicros is declared in internal/budget/model.go:
//     evaluate.go:31 is ReasonRepoDailyCap. STILL OPEN, and the reason is
//     scope: the symbol sits outside the backticks, so pairing it with a path
//     means reading the sentence. The relative-reference check below does test
//     the symbol where the document names one, and that is the half of this
//     that is mechanical -- the symbol there is inside the reference's own
//     token.
//  2. The extractor's regexp required the closing backtick to follow the path
//     or its `:line` suffix, so the document's dominant `path:line Symbol`
//     shape never matched and nine cited paths were never checked. CLOSED, and
//     the census count is what keeps it closed: a check that silently sees
//     less is the failure mode, and a count is what turns it into a failure.
//  3. That same regexp stopped at a newline, so the three files the document
//     cites in a shape whose symbol wraps were invisible -- and two of them
//     were the anchor a relative reference resolved against. CLOSED; the
//     relative check is what makes it matter, since a lost anchor silently
//     re-points the reference at another file.
//
// A general checker for (1) was prototyped and deliberately not shipped:
// pairing identifiers with citations needs a notion of scope the prose does not
// carry (one parenthetical may cite a single file for a list drawn from three,
// and a paragraph names symbols belonging to other citations in it), and the
// coarse version reported hundreds of false positives -- every symbol in a
// paragraph paired with every path in it. Doing it properly means reading the
// sentences, which is what a reviewer does. The narrow version that IS
// mechanical -- a symbol inside the same backticked token as the line it is
// attached to, in any shape the document uses -- was also tried against the
// whole document and rejected as too loose to trust: three citations name a Go
// selector (`Claims.RB`), a terraform resource type or a slash-joined list of
// constants, none of which appears literally at the line named, so the check
// would have had to accept one of several candidate substrings and would have
// passed almost everything. The relative references below do not have that
// problem: each names exactly one symbol. So this file checks symbols where the
// document's own grammar makes the pairing unambiguous, and says so, rather
// than presenting a fuzzy check as a complete one.

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

// coordinatorAnsweredMarker is how every one of the sixteen questions records
// the owner's answer of 2026-10-19: the option taken, in place, under the
// question. All sixteen took option (A), the recommended one, so there is a
// single marker to pin -- and pinning it per question is what makes a question
// that quietly loses its answer fail, which is the state the document was in
// until this round.
const coordinatorAnsweredMarker = "**Answered 2026-10-19: option (A)**"

// coordinatorReadmeAnswered and coordinatorReconciledAnswered are the two
// documents' own sentences stating that the owner answered all sixteen
// questions and that answering one is not approving a plan. Each is pinned
// whole, and each is written on a single line in its document so that the pin
// is a sentence rather than a fragment that a rewrap can satisfy.
//
// The README states the second fact twice -- once inside the answered sentence
// ("none is approved") and once as its own sentence -- and both halves are
// pinned, because each is a place a later edit can soften the claim without
// touching the other.
const (
	coordinatorReadmeAnswered     = "**All sixteen questions are answered, and none is approved.**"
	coordinatorReadmeNotApproval  = "**An answer is not an approval**: nothing in this directory has been accepted, approved or given a decision number, no plan exists, and the plan that follows is a separate document with its own gate."
	coordinatorReconciledAnswered = "**The owner answered all sixteen on 2026-10-19, each by taking option (A) — the recommendation — and an answer is not an approval.**"
)

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
		"reconciliation only; nothing is implemented and no plan is approved",
		"**Owner: Dimi Paun. Date: 2026-10-19.**",
		coordinatorCheckedAgainst,
		"**None.** The spec is copied byte for byte",
		"its items were **Q1–Q16**",
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
	// Both documents say, in their own words, two things that only appear as
	// prose and that a later edit can quietly invert: that the owner answered
	// all sixteen questions on 2026-10-19, and that an answer is not an
	// approval. The second is the one that matters -- a reconciliation whose
	// answers start reading as an approved plan is the failure this work's
	// no-D-number rule exists to prevent, and nothing else in this package
	// would see it. A reader who finds the answers described here as a plan
	// should treat that as an error in the other document.
	for path, wants := range map[string][]string{
		coordinatorReadme:     {coordinatorReadmeAnswered, coordinatorReadmeNotApproval},
		coordinatorReconciled: {coordinatorReconciledAnswered},
	} {
		for _, want := range wants {
			if !strings.Contains(readDoc(t, path), want) {
				t.Errorf("%s no longer says both that the owner answered Q1–Q16 and that an answer is not an approval: it lacks %q", path, want)
			}
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
// shape after the path is therefore written as "whatever is not a backtick",
// and coordinatorPathCensusRE below is the independent count that makes a
// regexp which matches nothing a failure rather than a silence.
//
// "Whatever is not a backtick" rather than "whatever is not a backtick or a
// newline" on purpose, and that is a correction of its own: the document cites
// three files in a shape whose symbol wraps onto the next line, as
// `modules/installation/history.tf:70 google_cloud_run_v2_job` does. A citation
// regexp that stopped at the newline did not see those three, which is the same
// silent-shortfall failure the paragraph above describes, one line further in.
// They matter beyond themselves: two of them are the anchor that the relative
// second references (`:223`, `:127 notCovered`) resolve against below, so a
// reader -- or a resolver -- that cannot see the anchor cannot check the
// reference. coordinatorPathCensusRE stays line-bounded, deliberately: it is a
// floor, not an exact count, and a census that stretched across newlines would
// manufacture giant "tokens" wherever the prose has an unpaired backtick.
//
// The extension set is the one this repository's cited files use; a backticked
// token without one (a node path such as `config/policy/shared`, a package
// name, a symbol on its own) is not a citation of a file and is left alone.
// The path character class carries `*` for the two globs the document names
// (`internal/cli/docs_*_test.go`, `docs/design/credentials-service*.md`); both
// are audited exceptions below, not files.
var coordinatorCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b)(?::[0-9]+(?:[-,+][0-9]*)*)?[^`]*`")

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
		if _, ok := coordinatorResolveCitation(path); !ok {
			t.Errorf("%s cites %q, which does not exist (tried %s): a reconciliation whose citations have moved is a reconciliation nobody can check",
				coordinatorReconciled, path, strings.Join(coordinatorTriedCitations(path), ", "))
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

// coordinatorResolveCitation resolves a cited path against the two forms the
// document cites one in: from the repository root, and from
// deploy/terraform/gcp/ (the way SECURITY.md cites the Terraform modules). It
// returns the repository-relative path that exists, and whether one did.
func coordinatorResolveCitation(path string) (string, bool) {
	for _, prefix := range coordinatorCitationPrefixes {
		full := prefix + path
		if _, err := os.Stat("../../" + filepath.FromSlash(full)); err == nil {
			return full, true
		}
	}
	return "", false
}

// coordinatorTriedCitations names every spelling coordinatorResolveCitation
// tried, so a failure says what was looked for rather than only what was not.
func coordinatorTriedCitations(path string) []string {
	tried := make([]string, 0, len(coordinatorCitationPrefixes))
	for _, prefix := range coordinatorCitationPrefixes {
		tried = append(tried, prefix+path)
	}
	return tried
}

// coordinatorBareRefRE finds a relative second reference: a backticked token
// that begins with `:` and carries no path of its own.
//
//	`:89`                  one line
//	`:14-24`               a range
//	`:124,138,159,174`     a list
//	`:127 notCovered`      a line, and the symbol to find beside it
//
// It is line-bounded, where coordinatorCitationRE deliberately is not: this
// shape is short by nature, and a version that crossed newlines would read the
// colon that merely ENDS a citation -- `.../reconciliation.md`:` then a newline
// then `- **D143**` -- as a reference with a whole sentence for a line number.
//
// The colon has to be followed by a non-space, which keeps out the token that
// falls between two adjacent citations in the prose -- the colon and the space
// in "`job`: `path`" -- because that one is punctuation, not a reference.
var coordinatorBareRefRE = regexp.MustCompile("`(:[^ `\n][^`\n]*)`")

// coordinatorLineSpecRE is what may follow a relative reference's colon: a line
// number, or numbers joined by `-` or `,`, with an empty tail allowed for the
// open-ended `:326+` form. Anything else -- `:...`, `:N`, `:later` -- is a
// placeholder that resolves to nothing, and that is the failure this whole
// check exists for: the document carried a literal `:...` in a parenthetical no
// test could see, because a reference with no path in it was skipped by the
// citation check rather than failed by it.
var coordinatorLineSpecRE = regexp.MustCompile(`^[0-9]+(?:[-,+][0-9]*)*$`)

// coordinatorDigitsRE pulls the numbers out of a spec coordinatorLineSpecRE has
// already accepted.
var coordinatorDigitsRE = regexp.MustCompile(`[0-9]+`)

// coordinatorSymbolWindow is how far from a relative reference's line a symbol
// named beside it may sit, in either direction. A doc comment sits a few lines
// above the declaration it belongs to and a call site a line or two either
// side, so the window has to be a few lines wide; it is far too narrow to
// accept a symbol that lives in another part of the file.
const coordinatorSymbolWindow = 5

// coordinatorParagraphSeparatorRE finds the blank line that ends a paragraph.
var coordinatorParagraphSeparatorRE = regexp.MustCompile(`\n[ \t]*\n`)

// coordinatorParagraph returns the blank-line-delimited block of text holding
// offset i. A relative reference is resolved inside one block and no further: a
// bare `:89` belongs to the sentence it sits in, and a search that ran back
// through the document would happily resolve it against a path from three
// findings earlier. A markdown table counts as one block, which is right -- a
// row's own citations are the row's anchor.
func coordinatorParagraph(doc string, i int) (int, int) {
	start, end := 0, len(doc)
	for _, sep := range coordinatorParagraphSeparatorRE.FindAllStringIndex(doc, -1) {
		if sep[1] <= i {
			start = sep[1]
			continue
		}
		if sep[0] >= i {
			end = sep[0]
			break
		}
	}
	return start, end
}

// TestDocsCoordinatorRelativeCitationsResolve: the document's second references
// -- a bare `:89`, `:145`, `:183`, `:209` -- are citations too, and are checked
// like ones. Each resolves against the nearest full-path citation before it in
// its own paragraph; the line it names has to exist in that file; and where the
// reference also names a symbol, the symbol has to be within a few lines of the
// line it is attached to.
//
// This is the check the document needed and did not have. A reference of this
// shape carries no path, so TestDocsCoordinatorCitationsExist skips it
// entirely, and a placeholder line number, a line past the end of the file and
// a symbol the file does not have all read exactly like a correct one. The
// document carried a literal `:...` for exactly that reason.
func TestDocsCoordinatorRelativeCitationsResolve(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	refs := coordinatorBareRefRE.FindAllStringSubmatchIndex(doc, -1)
	if len(refs) == 0 {
		t.Fatal("the document carries no relative reference of the `:N` shape: this test would otherwise check nothing")
	}
	checked := 0
	for _, m := range refs {
		ref := doc[m[2]:m[3]]                       // `:127 notCovered`
		line := 1 + strings.Count(doc[:m[0]], "\n") // where the reference is, for the messages
		start, _ := coordinatorParagraph(doc, m[0])
		// The anchor: the last full-path citation before this reference in the
		// same paragraph. It is allowed to wrap onto the next line, which is
		// why coordinatorCitationRE does not stop at a newline.
		anchor := ""
		for _, c := range coordinatorCitationRE.FindAllStringSubmatchIndex(doc[start:m[0]], -1) {
			anchor = doc[start+c[2] : start+c[3]]
		}
		if anchor == "" {
			t.Errorf("%s:%d cites %q with no full-path citation before it in the same paragraph: a relative reference is resolvable only against an anchor, so a reader has no file to open. Name the path at the reference, or put the reference beside a citation",
				coordinatorReconciled, line, ref)
			continue
		}
		file, ok := coordinatorResolveCitation(anchor)
		if !ok {
			t.Errorf("%s:%d cites %q against %q, which does not exist (tried %s): the anchor has to resolve here too, or the reference resolves to nothing",
				coordinatorReconciled, line, ref, anchor, strings.Join(coordinatorTriedCitations(anchor), ", "))
			continue
		}
		spec, symbol, _ := strings.Cut(strings.TrimPrefix(ref, ":"), " ")
		if !coordinatorLineSpecRE.MatchString(spec) {
			t.Errorf("%s:%d cites %q: %q is not a line number, a range or a list of them. A placeholder that resolves to nothing is a citation no reader can follow, and this document's contract is that every one of them can be",
				coordinatorReconciled, line, ref, spec)
			continue
		}
		body, err := os.ReadFile("../../" + filepath.FromSlash(file))
		if err != nil {
			t.Errorf("%s:%d cites %q against %q, which could not be read: %v", coordinatorReconciled, line, ref, file, err)
			continue
		}
		// The trailing newline every Go file ends with is not a line of it.
		// Splitting without trimming counts one line more than the file has,
		// which makes a reference to that phantom last line resolve to
		// nothing and still pass the check below -- the same "sees less than
		// it claims" shape this file is written against, and the reason the
		// file sizes in the mutation record above are the ones `wc -l` gives.
		lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		checked++
		nums := coordinatorLineNumbers(spec)
		lo, hi, past := nums[0], nums[0], false
		for _, n := range nums {
			if n < lo {
				lo = n
			}
			if n > hi {
				hi = n
			}
			if n < 1 || n > len(lines) {
				past = true
				t.Errorf("%s:%d cites %q against %q, which holds %d lines: line %d is past the end of the file",
					coordinatorReconciled, line, ref, file, len(lines), n)
			}
		}
		// Nothing more to check, and nothing safe to check either: the window
		// below is an index into lines, and a past-the-end line would make it
		// an out-of-range slice.
		if symbol == "" || past {
			continue
		}
		from, to := lo-coordinatorSymbolWindow, hi+coordinatorSymbolWindow
		if from < 1 {
			from = 1
		}
		if to > len(lines) {
			to = len(lines)
		}
		found := false
		for _, l := range lines[from-1 : to] {
			if strings.Contains(l, strings.TrimSuffix(symbol, "()")) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s:%d cites %q against %q, but %q is nowhere in lines %d-%d: the reference names a symbol the file does not have beside the line it is attached to",
				coordinatorReconciled, line, ref, file, symbol, from, to)
		}
	}
	t.Logf("%s: %d relative citations resolved against their paragraph's anchor, of %d found", coordinatorReconciled, checked, len(refs))
}

// coordinatorLineNumbers reads the line numbers out of a spec that
// coordinatorLineSpecRE has already accepted; there is always at least one.
func coordinatorLineNumbers(spec string) []int {
	var out []int
	for _, f := range coordinatorDigitsRE.FindAllString(spec, -1) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
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
	const heading = "## 3. The owner's answers (Q1–Q16)"
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
		// Every question records the owner's answer of 2026-10-19 in place. A
		// question that has lost its answer reads as though it were still open,
		// which is exactly the state this document was in before this round:
		// the answers are the owner's and they are recorded, not implied by the
		// recommendation being the first option.
		if !strings.Contains(body, coordinatorAnsweredMarker) {
			t.Errorf("Q%d records no answer: the owner answered all sixteen on 2026-10-19 and every one of them took option (A), so each question carries %q", n, coordinatorAnsweredMarker)
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
	wantHeading := "## 3. The owner's answers (Q1–Q" + strconv.Itoa(len(bounds)) + ")"
	if !strings.Contains(doc, wantHeading) {
		t.Errorf("%s's questions section is headed %q while it carries %d questions: the heading and the count disagree", coordinatorReconciled, heading, len(bounds))
	}
}

// TestDocsCoordinatorMutationRecordIsCounted reads this file rather than the
// documents, and holds the mutation record to the list under it.
//
// The record is prose about work already done -- it cannot be run, and nothing
// in it proves a mutation ever fired. The one thing about it that IS
// mechanical is its own census: the header states how many mutations were
// planted and reverted, and every one of them appears below as a bullet
// carrying exactly one arrow. Counting the arrows is the same move the
// citation check makes against the document, and it exists for the same
// reason -- a check that claims more than it sees is the failure this whole
// file is about. It caught one: the header read "twenty-six" over a list of
// twenty-four, because two earlier bullets had been replaced by unrelated new
// pins instead of kept.
//
// The count is a single digit ("-- 24,") so this test can read it without a
// word-to-number table, and so there is nothing for a second copy of it to
// disagree with: a word beside a digit would let the two drift apart with
// neither looking wrong, which is the shape of the mistake here.
func TestDocsCoordinatorMutationRecordIsCounted(t *testing.T) {
	src, err := os.ReadFile("docs_coordinator_test.go")
	if err != nil {
		t.Fatalf("reading this test file to check its own mutation record: %v", err)
	}
	text := string(src)

	const open = "// Mutation (verified, run and restored) -- "
	start := strings.Index(text, open)
	if start < 0 {
		t.Fatalf("the mutation record's opening line (%q...) is gone: this test has nothing to check", open)
	}
	// The record runs from that line to the paragraph that disclaims its own
	// numbers; the arrows outside it (the source's own operators, other
	// comments) are not mutations and must not be counted.
	const close = "// The numbers above are a reading"
	end := strings.Index(text[start:], close)
	if end < 0 {
		t.Fatalf("the mutation record's closing paragraph (%q...) is gone, so the list it heads has no end this test can find", close)
	}
	record := text[start : start+end]

	declaredRE := regexp.MustCompile(`^// Mutation \(verified, run and restored\) -- ([0-9]+),`)
	m := declaredRE.FindStringSubmatch(record)
	if m == nil {
		t.Fatalf("the mutation record's opening line must state its count as a digit, in the form %q, so this test can read it", "Mutation (verified, run and restored) -- 24, ...")
	}
	declared, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("the mutation record's stated count %q is not a number: %v", m[1], err)
	}

	listed := strings.Count(record, "->")
	if listed != declared {
		t.Errorf("the mutation record claims %d mutations and lists %d: every mutation below is one bullet carrying one arrow, so the stated count and the list are the same census, and a stated count the list does not bear out is a check claiming more than it sees -- which is exactly how this number drifted from the list once already", declared, listed)
	}
	if declared == 0 {
		t.Error("the mutation record claims no mutations at all: a record of zero planted mutations proves nothing about the checks above it")
	}
}
