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
//    every citation shape the document uses -- including a shortened reference
//    that names its file by base name alone and carries a line number, which is
//    resolved against the tree and refused when the name fits no file or more
//    than one -- and the check is measured against two independent counts: of
//    the document's path-looking tokens, and of the tokens that attach a line
//    number to a name, the second of which fails on any token no matcher
//    claims.
//  * TestDocsCoordinatorRelativeCitationsResolve -- every relative second
//    reference, in both of its shapes -- `:89`, `:14-24`, `:127 notCovered`,
//    and the symbol-first `sanitizeText:2208` -- resolves against the nearest
//    full-path citation before it in its own paragraph, to a line that exists,
//    and -- where a symbol is named -- to a symbol within a few lines of it.
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
// Mutation (verified, run and restored) -- 34, one per way these documents can
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
//	  put a line past the end of a cited file (`internal/budget/model.go:99999`,
//	  and the same on a second citation of a file cited twice above) -> fails,
//	  naming the line and the file's length, where before this check the path
//	  resolved and the line was never looked at;
//	  cite a nonexistent path in the `path:line Symbol` shape the old regexp
//	  missed (`internal/task/taskNOPE.go:328 NewRunID`) -> fails, naming it.
//	  Run against the old regexp with the census check removed, the same
//	  citation is not reported at all -- that is the hole this shape closed,
//	  and the reason the census is checked first;
//	  restore the old shape-blind regexp -> the census assertion fails,
//	  reporting "217 path-looking tokens and the citation check matched 182";
//	  make the regexp line-bounded again -> fails in the relative check below,
//	  because that loses the anchor `:127 notCovered` resolves against;
//	  put a line past the end of the file a shortened reference's name resolves
//	  to (`apptoken.go:9999`) -> fails, naming that file and its length, where
//	  before this round the token was skipped in silence and no check read it;
//	  shorten a reference to a name no file carries (`nosuchfile.go:89
//	  grantRules`) -> fails, saying no file in the tree is named it;
//	  shorten one to a name six files carry (`main.go:89 grantRules`) -> fails,
//	  listing them: a shortened name is a claim about one file, and the check
//	  will not choose one for it;
//	  name a symbol the resolved file does not have beside the line
//	  (`cover.go:1569 grantRules`) -> fails, naming the window it searched.
//	  The three shortened references the document carries were checked by no
//	  test before this round, and the path census could not see it: they are
//	  path-looking tokens like any other. The distinct-path count could -- it
//	  read 94 then and reads 96 now, which is why both counts are logged.
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
//	  fails three times, naming the references with no anchor;
//	  put the symbol-first reference's line past the end of the file its
//	  paragraph names (`sanitizeText:99999`) -> fails, past the end of the file;
//	  rename the symbol it names (`safeUrlGone:2477`) -> fails: no such symbol
//	  near the line, which is the check the shape had none of before this round;
//	  delete the citation of the console's sanitizer module, which anchors all
//	  three symbol-first references -> fails three times, each one resolving
//	  against the paragraph's previous citation (the console renderer's app
//	  module, 344 lines) and reporting its line past the end of that file: the
//	  anchor is the paragraph's, and a reference that loses its own file
//	  silently inherits another one.
//	TestDocsCoordinatorCitationsExist (the line census)
//	  write a line reference in a shape no matcher knows
//	  (`sanitize-text:2208`, hyphenated) -> fails: the token attaches a line to
//	  a name, the census sees it and no check claims it. This is the assertion
//	  that makes the symbol-first shape's arrival a failure rather than a
//	  silence, which is what it was: three citations, no matcher, and a census
//	  looking for file extensions that could not see them.
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
// mutation now reports 217/182. The live readings are the assertion's own
// message and the two log lines -- "N citation occurrences over P distinct
// paths, against a census of M path-looking tokens" and "N relative citations
// resolved against their paragraph's anchor". Anyone re-running a mutation
// should read the numbers there rather than here, and update this comment to
// whatever they say: a number quoted here is a witness that the mutation fires,
// not a fact about the test.
//
// The first of those two lines prints both citation counts because the census
// is compared with one of them and the line used to print the other: the
// assertion measures occurrences against the census, and the line reported
// distinct paths beside it, so the pair a reader took away was never the pair
// that was compared.
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
//     means reading the sentence. Two checks do test the symbol where the
//     document names one -- the relative references below, and a shortened
//     reference that carries its line (`cover.go:89 grantRules`) in the
//     citation check above -- and that is the half of this that is mechanical:
//     the symbol there is inside the reference's own token.
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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
//
// The line spec is captured as a second group, because the path resolving is
// only half of what a `path:line` citation claims: the check below reads the
// file and holds the number to it, for every occurrence and not only the first
// one per path.
var coordinatorCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b)(:[0-9]+(?:[-,+][0-9]*)*)?[^`]*`")

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
// a path (contain a `/`), or carry a line number that makes it a shortened
// reference to one file in the tree, which coordinatorBareNames resolves and
// refuses to guess at. What is left -- a bare filename with no line, a run
// artifact such as `launch.json` -- is not a claim about a file in this tree.
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
	// The floor for the other half of a citation -- the line, rather than the
	// path. Every token in the document that attaches a line number to a name is
	// one of the two named shapes the checks know: `path:line`, which this test
	// checks below, or `symbol:line`, whose file is the paragraph's anchor and
	// which TestDocsCoordinatorRelativeCitationsResolve checks. A token that is
	// neither is a citation no check reads, which is what the document's
	// `sanitizeText:2208` was: a shape that arrived with the prose and was
	// checked by nothing. The path census above cannot see that shape at all --
	// it looks for a file extension -- so this one exists to see it.
	for _, token := range coordinatorLineCensusRE.FindAllString(doc, -1) {
		if coordinatorCitationRE.MatchString(token) || coordinatorNamedRefRE.MatchString(token) {
			continue
		}
		t.Errorf("%s holds %q, which attaches a line number to a name and is matched by no citation check: the document has grown a shape the checks do not know, so that line is checked by nothing",
			coordinatorReconciled, token)
	}
	// The tree is walked once, so that a shortened reference can be resolved
	// against it rather than skipped.
	bareNames := coordinatorBareNames(t)
	seen := map[string]bool{}
	used := map[string]bool{}
	cites := 0
	for _, m := range matches {
		path := m[1]
		// The line numbers are checked for EVERY occurrence, before the
		// per-path dedup below: one file is cited at several lines, and
		// checking only the first occurrence would leave the rest unread --
		// which is how a citation can name a line past the end of a file and
		// still be reported as checked.
		first := !seen[path]
		seen[path] = true
		// The exceptions are checked first, so that one naming a bare filename
		// (`CLAUDE.md`) is not swallowed by the path test below.
		if reason, ok := coordinatorCitationExceptions[path]; ok {
			if first {
				used[path] = true
				t.Logf("%s cites %q, which is not a file here: %s", coordinatorReconciled, path, reason)
			}
			continue
		}
		// A citation is a path, one of the repository's own root documents, or a
		// shortened second reference that carries the line it means -- `cover.go:89
		// grantRules`, `apptoken.go:174`. The last of those used to be skipped
		// here, and silently: the token names no directory, so the path test
		// below never reached it, and the line number it carries was read by no
		// test at all -- while the document's contract is that every citation
		// shape it uses is checked. A shortened reference is a claim about one
		// file in this tree, so it is resolved against the tree and the check
		// refuses to pick a file for it when the name does not pick one.
		//
		// A bare name with NO line is still left alone: `launch.json` and
		// `fugaro.yaml` name a run artifact and a configuration file, not a file
		// in this tree, and the token carries nothing that could hold them to
		// one.
		if !strings.Contains(path, "/") && !coordinatorRootFiles[path] {
			if m[2] == "" {
				continue
			}
			file, ok := coordinatorResolveBareName(bareNames, path)
			if !ok {
				if first {
					cites++
					t.Errorf("%s cites %q, a shortened reference carrying a line number, and %s: a shortened name is a claim about one file in this tree, and this check will not choose one for it",
						coordinatorReconciled, path, coordinatorBareNameTrouble(bareNames, path))
				}
				continue
			}
			if first {
				cites++
			}
			if !coordinatorCheckCitedLines(t, path, file, m[2]) {
				continue
			}
			if symbol := coordinatorCitedSymbol(m[0], path, m[2]); symbol != "" {
				coordinatorCheckCitedSymbol(t, path, file, m[2], symbol)
			}
			continue
		}
		file, ok := coordinatorResolveCitation(path)
		if !ok {
			if first {
				cites++
				t.Errorf("%s cites %q, which does not exist (tried %s): a reconciliation whose citations have moved is a reconciliation nobody can check",
					coordinatorReconciled, path, strings.Join(coordinatorTriedCitations(path), ", "))
			}
			continue
		}
		if first {
			cites++
		}
		if spec := m[2]; spec != "" {
			coordinatorCheckCitedLines(t, path, file, spec)
		}
	}
	// An exception nobody uses is a hole in this check that no one is watching.
	for path := range coordinatorCitationExceptions {
		if !used[path] {
			t.Errorf("%s no longer cites %q, but coordinatorCitationExceptions still excuses it: delete the entry, or the next citation of that name is unchecked", coordinatorReconciled, path)
		}
	}
	// The numbers this test checked, said out loud, so that the count is
	// visible in the run rather than a fact nobody reads: a later edit that
	// halves it is a line in the log, and the census assertion above is what
	// makes it a failure when it should be.
	//
	// BOTH counts, because the census is measured against one of them and this
	// line used to report the other: the assertion above compares every
	// OCCURRENCE the matcher found with the census, while the number printed
	// here was the DISTINCT PATHS it resolved. A reader taking the live numbers
	// from this line, as the mutation record tells them to, read a pair that
	// was never compared.
	t.Logf("%s: %d citation occurrences over %d distinct paths, against a census of %d path-looking tokens in the document",
		coordinatorReconciled, len(matches), cites, census)
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

// coordinatorBareNames indexes every file in the tree by its base name, so that
// a shortened second reference -- `cover.go:89 grantRules`, `apptoken.go:174` --
// can be resolved to the one file the document means.
//
// The document shortens a name only when the name is unambiguous in the tree,
// and this index is what makes that a checked claim rather than a convention:
// coordinatorResolveBareName below resolves a name only when exactly one file
// carries it. `.git` and `node_modules` are skipped -- neither is this
// repository's own source, and a name matching a vendored copy would be an
// ambiguity that is not one.
func coordinatorBareNames(t *testing.T) map[string][]string {
	t.Helper()
	index := map[string][]string{}
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel("../..", p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		index[filepath.Base(rel)] = append(index[filepath.Base(rel)], rel)
		return nil
	})
	if err != nil {
		t.Fatalf("the repository tree could not be walked, so a shortened reference into it cannot be resolved: %v", err)
	}
	for name := range index {
		sort.Strings(index[name])
	}
	return index
}

// coordinatorResolveBareName resolves a shortened reference's base name against
// the tree index. Exactly one file has to carry the name: a reference is a claim
// about one file, so a name matching two files (or none) is reported by
// coordinatorBareNameTrouble below rather than resolved to a guess.
func coordinatorResolveBareName(index map[string][]string, name string) (string, bool) {
	matches := index[name]
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

// coordinatorBareNameTrouble says why a shortened reference's name did not
// resolve to one file, in the same sentence as the failure: which files carry
// the name, or that none does.
func coordinatorBareNameTrouble(index map[string][]string, name string) string {
	switch matches := index[name]; len(matches) {
	case 0:
		return "no file in this tree is named " + name
	case 1:
		return "the one file named " + name + " is " + matches[0]
	default:
		return strconv.Itoa(len(matches)) + " files are named " + name + " (" + strings.Join(matches, ", ") + ")"
	}
}

// coordinatorCitedSymbol reads the symbol a citation names beside its line: the
// first word after `path:line` inside the citation's own backticks, as
// `cover.go:89 grantRules` names grantRules. A citation with nothing after the
// line names no symbol, and there is nothing to check.
//
// This is the same convention the document's relative references use (`:127
// notCovered`), and it is the only shape in which a path, a line and a symbol
// are paired by grammar rather than by reading the sentence -- which is why the
// symbol is checked here and not beside full-path citations ("THREE HOLES",
// hole 1, above).
func coordinatorCitedSymbol(token, path, spec string) string {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(token, "`"+path+spec), "`"))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// coordinatorFileLines reads a repository file and returns its lines. The
// trailing newline every Go file ends with is not a line of it: splitting
// without trimming counts one more than the file has, which makes a citation
// to that phantom last line resolve to nothing and still pass -- the same
// "sees less than it claims" shape this file is written against.
func coordinatorFileLines(t *testing.T, file string) []string {
	t.Helper()
	body, err := os.ReadFile("../../" + filepath.FromSlash(file))
	if err != nil {
		t.Errorf("%s could not be read, so the citations into it are unchecked: %v", file, err)
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// coordinatorCheckCitedLines checks the line numbers inside a full
// `path:line` citation -- the shape the document uses most, and the one whose
// path was checked while its line never was. The path existing is not the
// claim such a citation makes: `internal/budget/model.go:151,164` says those
// two lines are the ones meant, and a line past the end of the file is a
// citation no reader can follow. It is the same failure the bare-reference
// check below catches, in the other half of the document's citations, and
// finding #26 records a cited line range that was already wrong once.
//
// An open-ended line (`:326+`) is checked for the line it names and no more,
// which is all the `+` claims.
//
// It reports whether every line it was given is a line of the file, because the
// symbol check below indexes into those lines: a caller that goes on to look for
// a symbol beside a line past the end of the file would slice out of range.
func coordinatorCheckCitedLines(t *testing.T, path, file, spec string) bool {
	t.Helper()
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return false
	}
	ok := true
	for _, n := range coordinatorLineNumbers(strings.TrimPrefix(spec, ":")) {
		if n < 1 || n > len(lines) {
			ok = false
			t.Errorf("%s cites %q at %q, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
				coordinatorReconciled, path, spec, file, len(lines), n)
		}
	}
	return ok
}

// coordinatorCheckCitedSymbol checks the symbol a shortened reference names
// beside its line: `cover.go:89 grantRules` claims grantRules is at line 89, and
// a check that read the number and not the name would pass a citation whose name
// had moved to another file. The window is the same one the relative references
// use, and for the same reason.
func coordinatorCheckCitedSymbol(t *testing.T, path, file, spec, symbol string) {
	t.Helper()
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return
	}
	nums := coordinatorLineNumbers(strings.TrimPrefix(spec, ":"))
	lo, hi := nums[0], nums[0]
	for _, n := range nums {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	from, to, found := coordinatorSymbolBeside(lines, lo, hi, symbol)
	if !found {
		t.Errorf("%s cites %q at %q naming %q, but %q is nowhere in lines %d-%d of %s: the shortened reference names a symbol the file does not have beside the line it is attached to",
			coordinatorReconciled, path, spec, symbol, symbol, from, to, file)
	}
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

// coordinatorNamedRefRE finds the document's other second-reference shape, the
// one that names the symbol first and the line second and gives no path at all:
//
//	`sanitizeText:2208`     the symbol, and the line it is at
//	`safeUrl:2477`
//
// It is the mirror of the `:line symbol` shape above, and it resolves the same
// way: the file is the last full-path citation before it in its own paragraph,
// which for these three is the console's sanitizer module. It is line-bounded
// for the same reason -- the token is short by nature -- and the symbol has to
// start with a letter or an underscore, which is what keeps a host and a port
// (`127.0.0.1:0`, cited twice in this document) out of it.
//
// The shape arrived with the document and was matched by nothing: it carries no
// file extension, so the citation matcher and the census both passed over it,
// and it does not begin with a colon, so the `:line` matcher did too. Three
// citations were read by no test at all.
var coordinatorNamedRefRE = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*):([0-9]+(?:[-,+][0-9]*)*)`")

// coordinatorLineCensusRE is the independent count of how many backticked tokens
// in the document attach a line number to a NAME -- a path or a symbol. It is
// the floor for the two named shapes, and it is deliberately dumber than either
// matcher: any token where a name is followed by `:` and a digit, whether what
// follows is a well-formed line spec or not, and whether the name is a path, a
// symbol or neither.
//
// coordinatorPathCensusRE above cannot serve here: it looks for a file
// extension, so `sanitizeText:2208` is invisible to it, and a census that cannot
// see a shape cannot catch the shape going unchecked. This one sees it, and the
// citation test fails on any token it finds that no matcher claims -- which is
// what makes the third shape's arrival a failure rather than a silence.
//
// A name has to start with a letter or an underscore, so a host and a port is
// not a name: `127.0.0.1:0` is cited twice in this document and is not a
// citation of anything.
var coordinatorLineCensusRE = regexp.MustCompile("`[^`\n]*[A-Za-z_][A-Za-z0-9_]*:[0-9][^`\n]*`")

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

// coordinatorSymbolWindow is how far from a cited line a symbol named beside it
// may sit, in either direction. A doc comment sits a few lines above the
// declaration it belongs to and a call site a line or two either side, so the
// window has to be a few lines wide; it is far too narrow to accept a symbol
// that lives in another part of the file.
const coordinatorSymbolWindow = 5

// coordinatorSymbolBeside looks for a symbol in the window of lines a citation
// may mean, and returns the window it looked in and whether it found it. The
// window is why this is not an equality: the symbol named beside a line is
// usually on the line, but a doc comment or a call site puts it a few lines
// away, and both are the citation's own subject.
//
// The caller has established that lo and hi are lines of the file: the window is
// an index into lines, and a past-the-end line would slice out of range.
func coordinatorSymbolBeside(lines []string, lo, hi int, symbol string) (int, int, bool) {
	from, to := lo-coordinatorSymbolWindow, hi+coordinatorSymbolWindow
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	want := strings.TrimSuffix(symbol, "()")
	for _, l := range lines[from-1 : to] {
		if strings.Contains(l, want) {
			return from, to, true
		}
	}
	return from, to, false
}

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
// -- a bare `:89`, `:145`, `:183`, `:209`, and a symbol-first `sanitizeText:2208`
// or `safeUrl:2477` -- are citations too, and are checked like ones. Each
// resolves against the nearest full-path citation before it in its own
// paragraph; the line it names has to exist in that file; and where the
// reference also names a symbol, the symbol has to be within a few lines of the
// line it is attached to.
//
// This is the check the document needed and did not have. A reference of either
// shape carries no path, so TestDocsCoordinatorCitationsExist skips it entirely,
// and a placeholder line number, a line past the end of the file and a symbol
// the file does not have all read exactly like a correct one. The document
// carried a literal `:...` for exactly that reason.
//
// The symbol-first shape arrived later and was read by nothing at all: no
// matcher knew it, and the census could not see it either, because the census
// looks for a file extension. Three citations (`sanitizeText:2208`,
// `sanitizeRefusal:2445`, `safeUrl:2477`) sat in that hole. Both shapes are
// handled here now, and the census below fails on a token that is neither.
func TestDocsCoordinatorRelativeCitationsResolve(t *testing.T) {
	doc := readDoc(t, coordinatorReconciled)
	refs := coordinatorBareRefRE.FindAllStringSubmatchIndex(doc, -1)
	named := coordinatorNamedRefRE.FindAllStringSubmatchIndex(doc, -1)
	if len(refs) == 0 {
		t.Fatal("the document carries no relative reference of the `:N` shape: this test would otherwise check nothing of that shape")
	}
	if len(named) == 0 {
		t.Fatal("the document carries no reference of the `symbol:N` shape: this test would otherwise check nothing of that shape, and coordinatorNamedRefRE would be dead code no test would miss")
	}
	checked := 0
	for _, m := range refs {
		spec, symbol, _ := strings.Cut(strings.TrimPrefix(doc[m[2]:m[3]], ":"), " ")
		if coordinatorCheckRelativeRef(t, doc, m[0], doc[m[2]:m[3]], spec, symbol) {
			checked++
		}
	}
	// The symbol-first shape: `sanitizeText:2208` names the symbol where the
	// other shape names the line, and its file is the paragraph's anchor either
	// way. The token as written is rebuilt from the two groups so that the
	// messages read the same for both shapes.
	for _, m := range named {
		ref := doc[m[2]:m[3]] + ":" + doc[m[4]:m[5]]
		if coordinatorCheckRelativeRef(t, doc, m[0], ref, doc[m[4]:m[5]], doc[m[2]:m[3]]) {
			checked++
		}
	}
	t.Logf("%s: %d relative citations resolved against their paragraph's anchor, of %d found", coordinatorReconciled, checked, len(refs)+len(named))
}

// coordinatorCheckRelativeRef checks one second reference -- either shape -- and
// reports whether it got far enough to count as checked. The two shapes differ
// only in which half of the reference is the symbol: both resolve against the
// last full-path citation before them in their own paragraph, and both are held
// to a line that exists and a symbol that sits beside it.
func coordinatorCheckRelativeRef(t *testing.T, doc string, at int, ref, spec, symbol string) bool {
	t.Helper()
	line := 1 + strings.Count(doc[:at], "\n") // where the reference is, for the messages
	start, _ := coordinatorParagraph(doc, at)
	// The anchor: the last full-path citation before this reference in the same
	// paragraph. It is allowed to wrap onto the next line, which is why
	// coordinatorCitationRE does not stop at a newline.
	anchor := ""
	for _, c := range coordinatorCitationRE.FindAllStringSubmatchIndex(doc[start:at], -1) {
		anchor = doc[start+c[2] : start+c[3]]
	}
	if anchor == "" {
		t.Errorf("%s:%d cites %q with no full-path citation before it in the same paragraph: a relative reference is resolvable only against an anchor, so a reader has no file to open. Name the path at the reference, or put the reference beside a citation",
			coordinatorReconciled, line, ref)
		return false
	}
	file, ok := coordinatorResolveCitation(anchor)
	if !ok {
		t.Errorf("%s:%d cites %q against %q, which does not exist (tried %s): the anchor has to resolve here too, or the reference resolves to nothing",
			coordinatorReconciled, line, ref, anchor, strings.Join(coordinatorTriedCitations(anchor), ", "))
		return false
	}
	if !coordinatorLineSpecRE.MatchString(spec) {
		t.Errorf("%s:%d cites %q: %q is not a line number, a range or a list of them. A placeholder that resolves to nothing is a citation no reader can follow, and this document's contract is that every one of them can be",
			coordinatorReconciled, line, ref, spec)
		return false
	}
	lines := coordinatorFileLines(t, file)
	if lines == nil {
		return false
	}
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
	// Nothing more to check, and nothing safe to check either: the window below
	// is an index into lines, and a past-the-end line would make it an
	// out-of-range slice.
	if symbol == "" || past {
		return true
	}
	from, to, found := coordinatorSymbolBeside(lines, lo, hi, symbol)
	if !found {
		t.Errorf("%s:%d cites %q against %q, but %q is nowhere in lines %d-%d: the reference names a symbol the file does not have beside the line it is attached to",
			coordinatorReconciled, line, ref, file, symbol, from, to)
	}
	return true
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
// The count is a single digit ("-- <N>,") so this test can read it without a
// word-to-number table, and so there is nothing for a second copy of it to
// disagree with: a word beside a digit would let the two drift apart with
// neither looking wrong, which is the shape of the mistake here. Nothing in
// this test quotes the number either -- the form below is built from the same
// constant the parse uses -- because a number written twice in this file is a
// number that can drift, and this file's whole subject is checks that quietly
// stop being true.
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

	declaredRE := regexp.MustCompile(`^` + regexp.QuoteMeta(open) + `([0-9]+),`)
	m := declaredRE.FindStringSubmatch(record)
	if m == nil {
		t.Fatalf("the mutation record's opening line must state its count as a digit, in the form %q, so this test can read it", open+"<N>,")
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
