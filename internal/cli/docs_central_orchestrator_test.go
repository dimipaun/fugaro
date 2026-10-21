package cli

// The central-orchestrator docs package, pinned -- part P0 of this folder's plan, and
// nothing else. docs/central-orchestrator/ holds the owner's spec for central orchestrators
// (spec.md, verbatim, committed alone), the reconciliation of that spec against the tree at
// origin/main 2f8b4dca (reconciliation.md), the index (README.md) and the plan
// (implementation-plan.md). P0 is the guard test that owns the folder and its citations; it
// is the piece of Go P0 wrote, and it implements no part of the orchestrator. P1's admission
// of the service type and P3's lease have landed since, and the sentences pinned below name
// them: this file's own premise is "P0 and nothing else", so a part that lands has to be
// written into the pins here or the pins quietly stop being true.
//
// The reconciliation's whole value is that every claim in it is checkable, and the four things
// a later edit can silently break are: the spec stops being verbatim, a citation drifts to a
// path that no longer exists, a citation into a document other works edit drifts off the
// sentence it meant, or a symbol this work anchors on is renamed under it. Each of the tests
// below is one of those, plus the two structural facts the owner's six answers of 2026-10-20
// put into the three documents (the decisions are answered, and answering one approves
// nothing) and the register reading corrected in this same pull request.
//
//  * TestDocsCentralOrchestratorIsWritten -- the four files exist and carry the load-bearing
//    sentences: the index's status line, the owner, the date, the commit the reconciliation
//    was run against, the fact that six decisions are answered (D1, D2, D3, D6, D12, D19) and
//    that an answer is not an approval, and the sentences that say which parts of the plan
//    have landed (P0's guard test, P1's admission and P3's lease) and which have not.
//  * TestDocsCentralOrchestratorSpecIsVerbatim -- spec.md carries the owner's own title, byline
//    and nineteen sections in order, carries none of the reconciliation's apparatus, and hashes
//    to the byte-for-byte pin: an edit here is an edit to the owner's text and has to be
//    deliberate.
//  * TestDocsCentralOrchestratorNumbersTableIsAsStated -- the index's numbers table and the
//    plan's section 6 table are read against each other and against the tree: the six answered
//    decisions are the six the owner named and no others, the twenty open ones are the rest,
//    the index claims no live-check number, and the register reading is the corrected one
//    (D220 written, D221 onward free), not the stale D1-D192/D193-onward one.
//  * TestDocsCentralOrchestratorCitationsExist -- every repository path reconciliation.md cites
//    resolves on disk, in either the full form or the deploy/terraform/gcp/-relative form the
//    Terraform modules are cited in; no citation carries a line number (the whole point of this
//    folder's convention); and the check is measured against an independent count of the
//    document's path-looking tokens, so a citation in a shape no matcher knows fails rather
//    than going unread.
//  * TestDocsCentralOrchestratorCitationsAreAnchoredByContent -- every citation that names a
//    file in this repository is held to the line of that file the citation turns on: the
//    declaration of the symbol it names, or the sentence of the document it means. The table is
//    ONE ENTRY PER CITATION and is counted against the citations token by token, so a claim
//    added against a file that already has an anchor fails until it has one of its own. The
//    citations that name something this tree does not have are excused BY NAME AND REASON in
//    the table itself -- PR #632's always-on orchestrator design, PR #773's rigor-mode
//    documents, the stale docs/design/always-on-orchestrator.md path finding #55 records, the
//    fugaro-mobile-app-spec.md correction C5 records, and the run artifacts (result.json and
//    friends) -- and each excuse is checked the only way an excuse can be: the file it names
//    must still be absent from this tree, so the day the pull request merges the test fails and
//    demands a real anchor rather than quietly holding nothing.
//
// One thing the table cannot write down is a path under the console's directory: no Go file in
// this package may carry one (docs_console_boundary_test.go holds the ONE-entry allow list of
// files that may, and docs_voice_module_test.go's precedent is that not even a comment may name
// one). Seven of the reconciliation's citations name console files, so those entries name the
// citation by the tail of its token, and the test resolves the tail back to the path through
// consoleDirName -- the boundary test's own constant -- and requires the document to cite that
// tail exactly once. Every one of the seven is anchored on a line of the console file, so the
// boundary rule costs the citations nothing.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	centralOrchReadme     = "docs/central-orchestrator/README.md"
	centralOrchSpec       = "docs/central-orchestrator/spec.md"
	centralOrchReconciled = "docs/central-orchestrator/reconciliation.md"
	centralOrchPlan       = "docs/central-orchestrator/implementation-plan.md"
)

// centralOrchCheckedAgainst is the commit the reconciliation was run against: the base
// 2f8b4dca the branch sits on. It is a separate constant because the tests assert the
// documents name it, not that HEAD is it -- the reconciliation is a dated reading of one
// commit, and a later commit does not make it wrong.
const centralOrchCheckedAgainst = "2f8b4dca"

// centralOrchSpecSHA256 is the byte-for-byte pin on the owner's spec, taken when P0 landed.
// The README promises spec.md is the owner's text copied byte for byte, so an edit here is an
// edit to something this work does not own: fix it by restoring the owner's text, and only the
// owner can change the pin.
const centralOrchSpecSHA256 = "2f56725b63459806985226145656dea35d61fe36f26990116857ec014733a01d"

// centralOrchAnsweredMarker is how each of the six answered decisions records the owner's
// answer of 2026-10-20 under its own heading. Pinning it per decision is what makes a decision
// that quietly loses its answer fail, which is the state the other twenty are in by design.
const centralOrchAnsweredMarker = "**Answered 2026-10-20 — the recommendation, as written above.**"

// centralOrchReadmeStatus is the index's status sentence, and it is the one place the two
// halves of P0's premise meet: the plan's P0 says the README carries "plan only; no code is
// implemented", and a part landing is what makes that sentence false. The sentence keeps the
// phrase and states its own exception, so neither half can be dropped silently -- and it was
// moved twice: once by the leftovers pull request after P3 landed, when the exception it named
// (P0's guard test, and nothing else) stopped being the whole of what is in the tree, and once
// by P1, whose admission of the Cloud Run service type is the third part. The exception it names
// now is the guard test, P1's admission and P3's lease; a part that lands without moving this
// sentence leaves the index saying no feature code exists while the part sits beside it, which
// is the state this pin exists to prevent.
const centralOrchReadmeStatus = "**Status: plan only; no part but P0 is approved, and the code in the tree is P0's guard test (`internal/cli/docs_central_orchestrator_test.go`, the one file that owns this folder), P1's admission of the Cloud Run service type (`internal/infra/tf/cover.go`, where every service is refused by name until P4 writes the checks one owes) and P3's lease (the `internal/lease` package and the rules' `orchestrators` family, landed by #782, whose model is written out at the end of P3 in `implementation-plan.md`) — every other part is unbuilt.**"

// centralOrchReadmeStaleStatus are the status sentences the index has carried and must not
// carry again: the pre-P0 one, which said no code existed at all, and P0's own, which said the
// guard test was the only code that did. Each is checked whole rather than by the phrase
// "plan only; no code is implemented" on its own, because that phrase is a *prefix* of P0's
// sentence: a Contains over it would fire on the wording P0 legitimately wrote and never on
// the pre-P0 sentence it replaced, which is the regression this check is for.
var centralOrchReadmeStaleStatus = []string{
	"**Status: plan only; no code is implemented.**",
	"**Status: plan only; no code is implemented, except the guard test P0 adds",
}

// centralOrchReadmeAnswered, centralOrchReadmeApproval, centralOrchReconciledAnswered and
// centralOrchReconciledRegister are the sentences the owner's answers of 2026-10-20 put into
// the two documents. Each is pinned whole, because each is a place a later edit can soften the
// claim without touching the others: that six decisions are answered, that answering one is
// not approving anything, and that the register reading was corrected rather than quoted.
const (
	centralOrchReadmeAnswered        = "**Six of the twenty-six decisions are answered — D1, D2, D3, D6, D12 and D19, each as its recommendation, by the owner on 2026-10-20 — and D4–D5, D7–D11, D13–D18 and D20–D26 are still open**"
	centralOrchReadmeApproval        = "**Six decisions — D1, D2, D3, D6, D12 and D19 — were answered by the owner on 2026-10-20, each as the recommendation the plan was written to, and an answer is not an approval of anything**"
	centralOrchReadmeLandedOnly      = "no part of the plan has been accepted except the three that have landed (P0's guard test, P1's admission of the service type and P3's lease)"
	centralOrchReconciledAnswered    = "**Six of the twenty-six decisions — D1, D2, D3, D6, D12 and D19 — were answered by the owner on 2026-10-20, each as its recommendation, and an answer is not an approval**"
	centralOrchReconciledStatus      = "**Status: reconciliation only; nothing is implemented and no plan is approved.**"
	centralOrchReconciledRegister    = "**The register, read by name against `main`.**"
	centralOrchReconciledNextFree    = "**The next free block is D221 onward, not D193 onward.**"
	centralOrchReadmeOwner           = "**Owner: Dimi Paun. Date: 2026-10-20. Base: `main` at `2f8b4dca`**"
	centralOrchPlanStatus            = "P0 — the guard test that pins this folder and its citations, `internal/cli/docs_central_orchestrator_test.go` — has landed, and so have **P1** (the Cloud Run service type admitted to the Terraform cover, `internal/infra/tf/cover.go` and `internal/infra/tf/managed_cover.go`, where every service is refused by name until P4 writes the checks one owes) and **P3** (`internal/lease`, the rules' `orchestrators` family and `RulesVersion` 4, landed by #782"
	centralOrchReadmeNoCheck         = "**not claimed and not written.**"
	centralOrchReadmeRegisterReading = "**the highest D-number written anywhere under `docs/` is D220 and the next free block is D221 onward**"
	// The three sentences that say P0 has landed, one per document. Each was stale until the
	// pull request that landed P0: C1 of the reconciliation said the work "writes **no guard
	// test**", the plan's P0 said "This folder has none", and the index still described
	// reconciliation section 6 as "Open for the owner" after that section grew an answer
	// column. The negatives below are the check that would have caught each of them: the
	// present-tense claim and the retired section title must not come back.
	centralOrchReconciledC1        = "**That pull request is the one that landed P0**"
	centralOrchReconciledStale     = "In particular it writes **no guard test**"
	centralOrchPlanP0Landed        = "it has since landed"
	centralOrchPlanP0Stale         = "This folder has none, because"
	centralOrchReadmeSection6      = "the same decisions tabled **for the owner — the answered six, and the open twenty**"
	centralOrchReadmeSection6Stale = "the same decisions tabled as **Open for the owner**"
)

// centralOrchSpecSections are the spec's own headings, in order: the pin that spec.md is the
// document the owner wrote and not a summary of it. The headings are the owner's, including
// the ones a tidier document would have renamed.
var centralOrchSpecSections = []string{
	"# Fugaro Central Orchestrator Spec",
	"## Purpose",
	"## Roles (terminology)",
	"## Use cases",
	"## Optionality and cost",
	"## Job ownership",
	"## Handoff",
	"## Access control",
	"## Management commands",
	"## Interaction channel",
	"## Multi-user access",
	"## Shared session",
	"## Agent prompts and escalation",
	"## Agent submitter callbacks",
	"## Alert storm protection",
	"## Prompt injection safeguards",
	"## Session memory",
	"## Submission auth",
	"## Client identity: pairing vs. authentication",
	"## Resolved questions",
}

// centralOrchSpecByline is the line the owner signs the spec with, pinned separately from the
// headings because it is the document's own statement of who wrote it and when.
const centralOrchSpecByline = "Oct 20, 2026 · @Dimi"

// centralOrchApparatus are strings none of which may appear in spec.md: the reconciliation's
// apparatus (findings, decisions, its own section headings) and this package's own vocabulary.
// A spec that has picked one up is no longer the owner's text.
var centralOrchApparatus = []string{"**#1 —", "D1 —", "**Answered 2026-10-20", "## 3. Findings", "reconciliation", "guarded here"}

// centralOrchCitationRE finds a citation: a backticked token ending in a source or document
// extension. The path is the capture and the rest of the token is kept so the line-number
// rule can read it. It deliberately matches tokens with a leading directory, and every bare
// name too: a bare README.md is as much a citation as docs/coordinator/README.md is, and the
// anchor table is what says which document a bare name meant.
var centralOrchCitationRE = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./*-]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf)\\b)([^`]*)`")

// centralOrchCensusRE finds every path-looking token the document writes in backticks, whether
// or not the citation matcher knows its shape. The census exists so that a citation written in
// a shape nobody taught the matcher to read fails instead of disappearing, and it counts more
// than the matcher does on purpose: it does not care how the token starts, so a backticked
// `[name](target)` link or a bare `../foo/bar.md` is counted here and has to be accounted for
// by the citation matcher or by the markdown-link check below.
var centralOrchCensusRE = regexp.MustCompile("[`]([^`\\s]*\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf))\\b")

// centralOrchLineNumberRE finds a citation that carries a line number, in any of the shapes
// the tree's other documents use. This folder cites by content and by symbol and never by
// line, and that is the rule this refuses.
var centralOrchLineNumberRE = regexp.MustCompile("`[A-Za-z0-9_./*-]+\\.(?:go|md|ts|json|js|tmpl|yaml|yml|tf):[0-9]")

// centralOrchCitationPrefixes are the forms a Terraform module path may be written in: the
// repository-relative one, and the deploy/terraform/gcp/-relative one the reconciliation uses
// for modules. Both name the same file.
var centralOrchCitationPrefixes = []string{"", "deploy/terraform/gcp/"}

// centralOrchMarkdownLinkRE finds an inline markdown link's target, which is a citation too. It
// matches a bare relative target as well as a ./ or ../ one: this folder links to its own
// siblings by name -- the reconciliation links to implementation-plan.md, the plan to spec.md --
// and a matcher that knew only the ./-prefixed form left those links, which are the ones a
// reader follows most often, unread. What it deliberately does not match is a target carrying a
// colon: an absolute URL (https:, mailto:) is not a file in this tree and is not this check's to
// resolve, and a target that is only a fragment is skipped at the check rather than here.
var centralOrchMarkdownLinkRE = regexp.MustCompile(`\]\(([^):]+)\)`)

// centralOrchConsoleTail is how the anchor table names a citation into the console's directory:
// by the tail of its token, with this marker in front, rather than by the path itself. No Go
// file in this package may carry that path -- docs_console_boundary_test.go's
// consoleBoundaryAllowList is a ONE-entry exception whose own sub-test refuses a second, and
// docs_voice_module_test.go records the precedent that not even a comment may name a path under
// it -- so a table that had to spell the path could not hold these citations at all. It names
// them by their tail instead, and centralOrchConsoleDoc turns that tail back into the path the
// document writes, using consoleDirName, the constant the boundary test itself declares.
const centralOrchConsoleTail = "·"

// centralOrchConsoleDoc resolves a tail entry to the path the reconciliation spells.
func centralOrchConsoleDoc(tail string) string {
	return consoleDirName + tail
}

// centralOrchResolve turns one anchor entry into the citation token the document spells and the
// file that token names. A plain entry is its own answer. A tail entry is resolved against the
// document: exactly one of its citations may end with the tail, and anything else -- none, or
// two -- is a citation the entry no longer holds, which is a failure rather than a warning.
func centralOrchResolve(t *testing.T, entries []centralOrchContentAnchor, cited map[string]int) ([]centralOrchContentAnchor, []string) {
	t.Helper()
	resolved := make([]centralOrchContentAnchor, 0, len(entries))
	tokens := make([]string, 0, len(entries))
	for _, a := range entries {
		if !strings.HasPrefix(a.token, centralOrchConsoleTail) {
			resolved = append(resolved, a)
			tokens = append(tokens, a.token)
			continue
		}
		tail := strings.TrimPrefix(a.token, centralOrchConsoleTail)
		at := ""
		for token := range cited {
			if strings.HasSuffix(token, tail) {
				if at != "" {
					t.Errorf("centralOrchContentAnchors holds %s and %s and the document cites both %s and %s: a tail entry has to name one citation, so write the entries out in full or make the tails tell them apart", a.token, centralOrchConsoleDoc(tail), at, token)
				}
				at = token
			}
		}
		if at == "" {
			t.Errorf("centralOrchContentAnchors holds %s and %s cites no citation ending in it: the entry names a citation the document has stopped making", a.token, centralOrchReconciled)
			continue
		}
		resolved = append(resolved, centralOrchContentAnchor{
			token: at, doc: centralOrchConsoleDoc(tail), claim: a.claim, anchor: a.anchor, repeated: a.repeated, excused: a.excused,
		})
		tokens = append(tokens, at)
	}
	return resolved, tokens
}

func centralOrchFileLines(t *testing.T, file string) []string {
	t.Helper()
	body, err := os.ReadFile("../../" + filepath.FromSlash(file))
	if err != nil {
		t.Errorf("%s could not be read, so the citations into it are unchecked: %v", file, err)
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// centralOrchLineOf returns the one line of file carrying anchor, and whether there was
// exactly one. An anchor that names two lines is a citation two readers would follow to
// different places, and an anchor that names none is a citation no reader can follow.
func centralOrchLineOf(t *testing.T, file, anchor, what, repeated string) (int, bool) {
	t.Helper()
	lines := centralOrchFileLines(t, file)
	if lines == nil {
		return 0, false
	}
	at, count := -1, 0
	for i, line := range lines {
		if !strings.Contains(line, anchor) {
			continue
		}
		count++
		if at < 0 {
			at = i
			continue
		}
		if repeated == "" {
			t.Errorf("%s carries %q on line %d and again on line %d, and that is the anchor %s is read from: an anchor that names two lines is a citation two readers would follow to different places, so this entry needs one that names the line it means, or a repeated reason saying why the phrase is legitimately carried twice",
				file, anchor, at+1, i+1, what)
			return 0, false
		}
	}
	if repeated != "" && count < 2 {
		t.Errorf("%s carries %q on %s, and the entry excuses that with %q: the reason says the phrase is written more than once, and a reason the file has stopped bearing is a weaker entry than the one it replaced, so either the anchor moves back to the line that carries it or the reason goes",
			file, anchor, centralOrchCount(count, "line", "lines"), repeated)
		return 0, false
	}
	if at < 0 {
		t.Errorf("%s carries no line with %q, and that is the anchor %s is read from: a citation by content is a claim that the file still carries the phrase or the symbol it names, and a phrase the file does not carry is a citation no reader can follow",
			file, anchor, what)
		return 0, false
	}
	return at + 1, true
}

// TestDocsCentralOrchestratorIsWritten: the package exists, and the index and the
// reconciliation state the facts a reader has to have before they trust anything in it -- what
// is implemented (nothing but this guard), who owns it, when it was written, which commit it
// was checked against, which decisions the owner has answered and that answering one approves
// nothing.
func TestDocsCentralOrchestratorIsWritten(t *testing.T) {
	for _, path := range []string{centralOrchReadme, centralOrchSpec, centralOrchReconciled, centralOrchPlan} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is missing: this folder's documents are the whole of the work, and a guard over them cannot run without them: %v", path, err)
		}
	}
	readme := flattenDoc(readDoc(t, centralOrchReadme))
	for what, want := range map[string]string{
		"the status line, naming part by part what has landed": centralOrchReadmeStatus,
		"the sentence naming the six answered decisions":       centralOrchReadmeAnswered,
		"the sentence that answering one is not an approval":   centralOrchReadmeApproval,
		"the sentence naming the three parts accepted":         centralOrchReadmeLandedOnly,
		"the owner, the date and the commit checked against":   centralOrchReadmeOwner,
		"the corrected register reading":                       centralOrchReadmeRegisterReading,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %s: %q", centralOrchReadme, what, want)
		}
	}
	for _, stale := range centralOrchReadmeStaleStatus {
		if strings.Contains(readme, stale) {
			t.Errorf("%s still carries a stale status wording: %q. P1 and P3 have landed since it was written, so the sentence has to say which Go exists and which does not", centralOrchReadme, stale)
		}
	}
	reconciled := flattenDoc(readDoc(t, centralOrchReconciled))
	for what, want := range map[string]string{
		"its own status line":                        centralOrchReconciledStatus,
		"the sentence naming the six answered":       centralOrchReconciledAnswered,
		"the heading of the corrected register read": centralOrchReconciledRegister,
		"the sentence naming the next free block":    centralOrchReconciledNextFree,
	} {
		if !strings.Contains(reconciled, want) {
			t.Errorf("%s lacks %s: %q", centralOrchReconciled, what, want)
		}
	}
	plan := flattenDoc(readDoc(t, centralOrchPlan))
	if !strings.Contains(plan, centralOrchPlanStatus) {
		t.Errorf("%s lacks the sentence recording which parts have landed, P0 first: %q", centralOrchPlan, centralOrchPlanStatus)
	}
	// The three sentences that went stale the day P0 landed, and the three that replaced
	// them: a document that still says the guard is unwritten contradicts the guard standing
	// beside it, and a reader who finds one has no way to tell which of the two is current.
	for _, c := range []struct{ doc, file, stale, now string }{
		{centralOrchReconciled, "correction C1", centralOrchReconciledStale, centralOrchReconciledC1},
		{centralOrchPlan, "P0's scope", centralOrchPlanP0Stale, centralOrchPlanP0Landed},
		{centralOrchReadme, "the index row for reconciliation.md", centralOrchReadmeSection6Stale, centralOrchReadmeSection6},
	} {
		body := flattenDoc(readDoc(t, c.doc))
		if strings.Contains(body, c.stale) {
			t.Errorf("%s' %s still carries %q: P0 has landed and section 6 has an answer column, so this sentence describes a state that has changed", c.doc, c.file, c.stale)
		}
		if !strings.Contains(body, c.now) {
			t.Errorf("%s' %s lacks %q: it is the sentence that replaced the stale one, and without it the staleness comes back unnoticed", c.doc, c.file, c.now)
		}
	}
}

// TestDocsCentralOrchestratorSpecIsVerbatim: spec.md is the owner's text, byte for byte, and
// none of the reconciliation's apparatus has been mixed into it.
func TestDocsCentralOrchestratorSpecIsVerbatim(t *testing.T) {
	spec := readDoc(t, centralOrchSpec)
	sum := sha256.Sum256([]byte(spec))
	if got := hex.EncodeToString(sum[:]); got != centralOrchSpecSHA256 {
		before := centralOrchSpecSHA256[:12]
		t.Errorf("%s is no longer the text committed verbatim (sha256 %s..., want %s...): the README promises it is byte-for-byte the owner's, so an edit here needs the owner, not this test updated", centralOrchSpec, got[:12], before)
	}
	if !strings.HasPrefix(spec, "# Fugaro Central Orchestrator Spec\n") {
		t.Errorf("%s does not open with the owner's title: a document that opens on something else is not the text the owner wrote", centralOrchSpec)
	}
	if !strings.Contains(spec, centralOrchSpecByline) {
		t.Errorf("%s lacks the owner's byline %q", centralOrchSpec, centralOrchSpecByline)
	}
	at := -1
	lines := strings.Split(spec, "\n")
	for _, heading := range centralOrchSpecSections {
		// The heading has to be a line of its own, which is why this is not a
		// substring search: the spec's prose names several of these sections, and
		// a heading that survives only as a mention in a sentence is a heading
		// the document has lost.
		next := -1
		for i, line := range lines {
			if line == heading {
				next = i
				break
			}
		}
		if next < 0 {
			t.Errorf("%s lacks the section %q: the spec's own headings are part of the text, and a missing one means the document was edited rather than copied", centralOrchSpec, heading)
			continue
		}
		if next < at {
			t.Errorf("%s carries %q out of the owner's order: the sections run Purpose through Resolved questions, and reordering them is an edit to the spec", centralOrchSpec, heading)
		}
		at = next
	}
	for _, apparatus := range centralOrchApparatus {
		if strings.Contains(spec, apparatus) {
			t.Errorf("%s carries %q, which is the reconciliation's apparatus and not the owner's text: the two documents have been mixed", centralOrchSpec, apparatus)
		}
	}
}

// centralOrchAnsweredDecisions are the six the owner answered on 2026-10-20, each as its
// recommendation, and centralOrchOpenDecisions are the twenty that are still open. Both sets
// are written out in full rather than counted, because the claim under test is not "six
// rows say answered" but "these six, and no others, are the ones the owner ruled on".
var centralOrchAnsweredDecisions = []string{"D1", "D2", "D3", "D6", "D12", "D19"}

var centralOrchOpenDecisions = []string{
	"D4", "D5", "D7", "D8", "D9", "D10", "D11", "D13", "D14", "D15", "D16", "D17", "D18",
	"D20", "D21", "D22", "D23", "D24", "D25", "D26",
}

// centralOrchDecisionRowRE finds a decision row of the plan's section 6 table, and
// centralOrchDNumberRE pulls the decision numbers out of such a row.
var (
	centralOrchDecisionRowRE = regexp.MustCompile(`(?m)^\|\s*\*\*(D[0-9]+(?:, ?D[0-9]+)*)\*\*.*$`)
	centralOrchDNumberRE     = regexp.MustCompile(`D([0-9]+)`)
)

// TestDocsCentralOrchestratorNumbersTableIsAsStated: the index's numbers table and the plan's
// section 6 table are checked against each other and against the register's corrected reading.
//
// This is the check the two tables needed and did not have. The index's D-row and no-check row
// are prose no test read, and the plan's section 6 table gained an answer column in the same
// pull request that answered the decisions -- a column nothing else reads. Four assertions hold
// them:
//
//   - the plan's answered rows name exactly the six answered decisions and the open rows exactly
//     the other twenty, so a decision answered without being moved to the answered column fails,
//     and so does a column that answers one the owner did not;
//   - the reconciliation carries the answered marker under each of the six and no other decision
//     heading, which is the half of the record that lives in the reconciliation;
//   - the index states the same split in words; and
//   - the register reading in the index and in the reconciliation is the corrected one (D220
//     written, D221 onward free), not the stale D1-D192 / D193-onward reading the register's own
//     table still carries.
func TestDocsCentralOrchestratorNumbersTableIsAsStated(t *testing.T) {
	plan := readDoc(t, centralOrchPlan)
	answered := map[string]bool{}
	open := map[string]bool{}
	rows := 0
	for _, m := range centralOrchDecisionRowRE.FindAllStringSubmatch(plan, -1) {
		rows++
		line := m[0]
		// The numbers come from the row's own decision cell, never from the rest of the
		// row: a row may name another decision in its prose -- D12's row says "`attach`
		// only after D5" -- and a scan of the whole line would read that D5 as answered.
		nums := centralOrchDNumberRE.FindAllStringSubmatch(m[1], -1)
		switch {
		case strings.Contains(line, "**ANSWERED — the recommendation**"):
			for _, n := range nums {
				answered["D"+n[1]] = true
			}
		case strings.Contains(line, "**open**"):
			for _, n := range nums {
				open["D"+n[1]] = true
			}
		default:
			t.Errorf("%s' section 6 carries the decision row %q with neither an answer nor an open state: an answer column with a cell that says neither is a decision whose state no reader can tell", centralOrchPlan, strings.TrimSpace(line))
		}
	}
	if rows == 0 {
		t.Fatalf("%s has no decision row at all: this test would otherwise check nothing", centralOrchPlan)
	}
	for _, d := range centralOrchAnsweredDecisions {
		if !answered[d] {
			t.Errorf("%s' section 6 does not answer %s: the owner answered it on 2026-10-20, and a table that drops the answer puts the decision back among the open ones", centralOrchPlan, d)
		}
		if open[d] {
			t.Errorf("%s' section 6 lists %s as both answered and open", centralOrchPlan, d)
		}
	}
	for _, d := range centralOrchOpenDecisions {
		if !open[d] {
			t.Errorf("%s' section 6 does not list %s as open: it is one of the twenty the owner has not ruled on, and a table that loses it reads as answered", centralOrchPlan, d)
		}
		if answered[d] {
			t.Errorf("%s' section 6 lists %s as answered: the owner answered six, and this is not one of them", centralOrchPlan, d)
		}
	}
	if len(answered) != len(centralOrchAnsweredDecisions) {
		t.Errorf("%s' section 6 answers %d decisions and the owner answered %d: the extra one is a decision this work made up an answer for", centralOrchPlan, len(answered), len(centralOrchAnsweredDecisions))
	}
	if len(open) != len(centralOrchOpenDecisions) {
		t.Errorf("%s' section 6 lists %d open decisions, and %d are open: the difference is a decision silently accounted for", centralOrchPlan, len(open), len(centralOrchOpenDecisions))
	}

	reconciled := readDoc(t, centralOrchReconciled)
	// The reconciliation records each answer under its own decision heading. Walk the
	// decision headings and check the six carry the marker and the twenty do not.
	headingRE := regexp.MustCompile(`(?m)^### (D[0-9]+) — `)
	found := 0
	for _, m := range headingRE.FindAllStringSubmatchIndex(reconciled, -1) {
		d := reconciled[m[2]:m[3]]
		end := len(reconciled)
		if next := strings.Index(reconciled[m[1]:], "\n### "); next >= 0 {
			end = m[1] + next
		}
		body := reconciled[m[1]:end]
		found++
		has := strings.Contains(body, centralOrchAnsweredMarker)
		want := false
		for _, a := range centralOrchAnsweredDecisions {
			if a == d {
				want = true
			}
		}
		if has != want {
			if want {
				t.Errorf("%s' section 5 gives %s no %q line: the owner answered it on 2026-10-20, and a decision that loses its answer reads as one still open", centralOrchReconciled, d, centralOrchAnsweredMarker)
			} else {
				t.Errorf("%s' section 5 gives %s an answer line, and the owner did not answer it: twenty decisions are open by design, and answering one here invents a ruling", centralOrchReconciled, d)
			}
		}
	}
	if found < len(centralOrchAnsweredDecisions)+len(centralOrchOpenDecisions) {
		t.Errorf("%s carries %d decision headings and the reconciliation tables %d decisions: the rest are decisions the question set lost", centralOrchReconciled, found, len(centralOrchAnsweredDecisions)+len(centralOrchOpenDecisions))
	}

	readme := flattenDoc(readDoc(t, centralOrchReadme))
	for what, want := range map[string]string{
		"the six/twenty split":                          centralOrchReadmeAnswered,
		"the statement that it claims no check":         centralOrchReadmeNoCheck,
		"the corrected register reading":                centralOrchReadmeRegisterReading,
		"the statement that answering approves nothing": centralOrchReadmeApproval,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %s: %q", centralOrchReadme, what, want)
		}
	}
	if strings.Contains(readme, "D1–D192 are taken") && !strings.Contains(readme, "its own table is stale as read against `main`") {
		t.Errorf("%s states the register's D1–D192 reading without saying the register is stale: the reading this work corrected is quoted as fact, which is the one thing the correction exists to prevent", centralOrchReadme)
	}
}

// centralOrchContentAnchor is one citation of reconciliation.md held to the line of the file it
// names that carries the claim: anchor is the text of that line, written exactly as the file
// writes it, and the line number is not written down anywhere -- it is found, which is what
// makes inserting a paragraph above it somebody else's change to make.
//
// Token is the citation exactly as the reconciliation spells it, and doc is what that spelling
// resolves to: the two differ only where the document writes a bare name (README.md, spec.md,
// reconciliation.md, implementation-plan.md), where the sentence, not the string, is what says
// which document is meant. Holding the token is what makes the table's coverage countable: the
// test below groups the document's citations and these entries by token and requires the two
// counts to be equal, so a citation added anywhere fails until it has an entry of its own.
//
// Excused is set instead of anchor where the citation names something this tree does not have,
// and it carries the reason: PR #632's always-on orchestrator design, PR #773's rigor-mode
// documents, the stale docs/design/always-on-orchestrator.md path finding #55 records, the
// fugaro-mobile-app-spec.md that correction C5 records, and the run artifacts a run writes under
// runs/<slug>/<run>/. The excuse is audited rather than trusted: the test requires an excused
// doc to be absent from this tree, so the day the pull request merges the entry fails and has to
// become an anchor.
// Repeated is set where the phrase genuinely is carried by more than one line of the file, and
// it carries the reason. It has one member: `internal/budget/rules/rules.json.tmpl` writes the
// mailbox subtree twice -- once under the inbox and once under the outbox -- with the action
// node, and the verb and head_sha validations inside it, byte-identical in both. The entry still
// has to name a line the file carries, and the reason is audited rather than trusted: the test
// requires a repeated anchor to be carried by more than one line, so a reason that has stopped
// being true fails rather than quietly weakening the entry.
type centralOrchContentAnchor struct {
	token    string // the citation as the reconciliation spells it
	doc      string // the file that spelling resolves to; empty for a console tail
	claim    string // the finding or section of the reconciliation the citation sits in
	anchor   string // the text of one line of doc that carries the claim
	excused  string // why no anchor is possible, when the file is not in this tree
	repeated string // why that phrase is legitimately carried by more than one line
}

// centralOrchContentAnchors holds every citation in reconciliation.md that names a file:
// ONE ENTRY PER CITATION, in the order the citations appear, so a document cited six times
// appears six times. That is deliberate, and it is the difference between this table and a list
// of files: the claim is that every citation is held, and a file cited six times with one anchor
// behind it is one citation held and five that are not.
var centralOrchContentAnchors = []centralOrchContentAnchor{
	{"spec.md", "docs/central-orchestrator/spec.md", "the document's status block, above its first section", "# Fugaro Central Orchestrator Spec", "", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "the document's status block, above its first section", "## Resolved questions", "", ""},
	// This file cites itself, and an entry for a citation into this file has one extra
	// constraint: its anchor text is written into this table as well, so a line without a
	// double quote in it would be carried twice -- once where it lives and once here -- and
	// read as an anchor that names two lines. Both self-entries anchor on a line carrying a
	// `"`, which this table writes escaped, so the anchor names exactly the line it means.
	{"internal/cli/docs_central_orchestrator_test.go", "internal/cli/docs_central_orchestrator_test.go", "the document's status block, above its first section", "const centralOrchCheckedAgainst = \"2f8b4dca\"", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "the document's status block, above its first section", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "the document's status block, above its first section", "**Every block is cited by content and not by line**", "", ""},
	{"internal/cli/docs_artifact_runs_test.go", "internal/cli/docs_artifact_runs_test.go", "0. Corrections to the brief this work was written from", "func TestArtifactRunsDesignSecurityText(t *testing.T) {", "", ""},
	{"internal/cli/docs_windows_cli_test.go", "internal/cli/docs_windows_cli_test.go", "0. Corrections to the brief this work was written from", "func TestDocsWindowsCLIIsWritten(t *testing.T) {", "", ""},
	{"internal/cli/docs_central_orchestrator_test.go", "internal/cli/docs_central_orchestrator_test.go", "0. Corrections to the brief this work was written from", "const centralOrchSpecSHA256 = \"2f56725b63459806985226145656dea35d61fe36f26990116857ec014733a01d\"", "", ""},
	{"internal/cli/docs_base_test.go", "internal/cli/docs_base_test.go", "0. Corrections to the brief this work was written from", "func readDoc(t *testing.T, path string) string {", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "0. Corrections to the brief this work was written from", "func (c *Client) Get(ctx context.Context, path string, out any) (found bool, err error) {", "", ""},
	{"internal/rtdb/stream.go", "internal/rtdb/stream.go", "0. Corrections to the brief this work was written from", "func (c *Client) Stream(ctx context.Context, path string) <-chan Event {", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "0. Corrections to the brief this work was written from", "func (c *Client) Get(ctx context.Context, coll, id string) (*Doc, error) {", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "0. Corrections to the brief this work was written from", "type Roller struct {", "", ""},
	{"README.md", "docs/credentials-service/README.md", "0. Corrections to the brief this work was written from", "**Status: plan only; nothing is implemented; decisions answered", "", ""},
	{"spec.md", "docs/credentials-service/spec.md", "0. Corrections to the brief this work was written from", "## Security service (credential proxy)", "", ""},
	{"README.md", "docs/coordinator/README.md", "0. Corrections to the brief this work was written from", "**Status: reconciliation only; nothing is implemented and no plan is approved.**", "", ""},
	{"spec.md", "docs/coordinator/spec.md", "0. Corrections to the brief this work was written from", "- **Advisory, not authoritative.**", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "0. Corrections to the brief this work was written from", "# Fugaro Mobile App — Functional Spec (v0.1)", "", ""},
	{"docs/mobile/functional-spec-reconciliation.md", "docs/mobile/functional-spec-reconciliation.md", "0. Corrections to the brief this work was written from", "**G12 — \"Anyone can authenticate with Firebase\" is false for a Fugaro installation. Wrong.**", "", ""},
	{"docs/rigor-modes/spec.md", "docs/rigor-modes/spec.md", "0. Corrections to the brief this work was written from", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"reconciliation.md", "docs/rigor-modes/reconciliation.md", "0. Corrections to the brief this work was written from", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"implementation-plan.md", "docs/rigor-modes/implementation-plan.md", "0. Corrections to the brief this work was written from", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"README.md", "docs/rigor-modes/README.md", "0. Corrections to the brief this work was written from", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "1. The spec's concepts against what the repository has", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/gateway/gateway.go", "internal/gateway/gateway.go", "1. The spec's concepts against what the repository has", "func Start(ctx context.Context, o Options) (*Server, error) {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "1. The spec's concepts against what the repository has", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "1. The spec's concepts against what the repository has", "type Holder struct {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "1. The spec's concepts against what the repository has", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "1. The spec's concepts against what the repository has", "func PathCapsRepo(slug string) string { return \"config/caps/repos/\" + seg(\"slug\", slug) }", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "1. The spec's concepts against what the repository has", "Auto-merge. Fugaro never merges.", "", ""},
	{"internal/gitprov/gitprov.go", "internal/gitprov/gitprov.go", "1. The spec's concepts against what the repository has", "type Provider interface {", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "1. The spec's concepts against what the repository has", "const VERB_VALUES = Object.freeze([...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS]);", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "1. The spec's concepts against what the repository has", "type Action struct {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "1. The spec's concepts against what the repository has", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "1. The spec's concepts against what the repository has", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/infra/identityplatform.go", "internal/infra/identityplatform.go", "1. The spec's concepts against what the repository has", "func PublicSignUp(raw []byte) ([]string, error) {", "", ""},
	{"deploy/terraform/gcp/modules/firebase/firebase.tf", "deploy/terraform/gcp/modules/firebase/firebase.tf", "1. The spec's concepts against what the repository has", "service = \"identitytoolkit.googleapis.com\"", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "1. The spec's concepts against what the repository has", "# Push notifications to the phone — P5.2", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "1. The spec's concepts against what the repository has", "type SessionMeta struct {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "1. The spec's concepts against what the repository has", "FirestoreMetaCollection = \"meta\"", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "1. The spec's concepts against what the repository has", "// Operations: Get, Patch (an upsert whose updateMask lists every field it", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "1. The spec's concepts against what the repository has", "func Args(req Request) []string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "1. The spec's concepts against what the repository has", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "1. The spec's concepts against what the repository has", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"task.json", "task.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "2. The spec's own open item, answered from the code", "func (s *Store) CreateTask(ctx context.Context, spec *task.Spec) error {", "", ""},
	{"launch.json", "launch.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "2. The spec's own open item, answered from the code", "func (s *Store) WriteLaunch(ctx context.Context, l *Launch) error {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "2. The spec's own open item, answered from the code", "type Claim struct {", "", ""},
	{"result.json", "result.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "2. The spec's own open item, answered from the code", "func (s *Store) CreateRecord(ctx context.Context, r *Record) error {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "2. The spec's own open item, answered from the code", "func ReplaceRecordIf(ctx context.Context, b *blobx.Bucket, slug, runID string, rec *Record, v RecordVersion) error {", "", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "2. The spec's own open item, answered from the code", "func (s *Store) RequestCancel(ctx context.Context) error {", "", ""},
	{"session/session.json", "session/session.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "2. The spec's own open item, answered from the code", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"comments.json", "comments.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runner/followup.go", "internal/runner/followup.go", "2. The spec's own open item, answered from the code", "var snap followup.Snapshot", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "2. The spec's own open item, answered from the code", "func Acquire(ctx context.Context, b *blobx.Bucket, key string, h Holder, now time.Time) (*Lock, error) {", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "2. The spec's own open item, answered from the code", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "2. The spec's own open item, answered from the code", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "2. The spec's own open item, answered from the code", "func (s *Session) Start(ctx context.Context, entry AgentEntry) error {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "2. The spec's own open item, answered from the code", "func (s *Session) grant(ctx context.Context, need Micros) (Micros, error) {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "2. The spec's own open item, answered from the code", "func (s *Session) Finish(ctx context.Context, status string) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "2. The spec's own open item, answered from the code", "func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "2. The spec's own open item, answered from the code", "res, err := budget.SetKill(ctx, db, path, kill, me, o.reason, budget.KillHooks{", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "2. The spec's own open item, answered from the code", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "2. The spec's own open item, answered from the code", "type Roller struct {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "2. The spec's own open item, answered from the code", "FirestoreMetaCollection = \"meta\"", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "2. The spec's own open item, answered from the code", "func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "2. The spec's own open item, answered from the code", "func releaseClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "2. The spec's own open item, answered from the code", "func claimHolder() string {", "", ""},
	{"internal/runview/runview.go", "internal/runview/runview.go", "2. The spec's own open item, answered from the code", "// LaunchedBy is who started the run, as the run's own launch.json", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "2. The spec's own open item, answered from the code", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "2. The spec's own open item, answered from the code", "const KindMessage = \"message\"", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "2. The spec's own open item, answered from the code", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"internal/agent/session.go", "internal/agent/session.go", "2. The spec's own open item, answered from the code", "func SessionDir(home, workDir string) string {", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "2. The spec's own open item, answered from the code", "func NewSessionID() string {", "", ""},
	{"internal/runner/session.go", "internal/runner/session.go", "2. The spec's own open item, answered from the code", "func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "2. The spec's own open item, answered from the code", "func (s *Session) Start(ctx context.Context, entry AgentEntry) error {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "2. The spec's own open item, answered from the code", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "2. The spec's own open item, answered from the code", "func (r *run) claimRecord(ctx context.Context) error {", "", ""},
	{"result.json", "result.json", "2. The spec's own open item, answered from the code", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runner/lockcache.go", "internal/runner/lockcache.go", "2. The spec's own open item, answered from the code", "var ErrDuplicateExecution = errors.New(\"another execution already owns this run\")", "", ""},
	{"internal/backend/codec.go", "internal/backend/codec.go", "2. The spec's own open item, answered from the code", "func SameExecution(a, b string) bool {", "", ""},
	{"modules/project/job.tf", "deploy/terraform/gcp/modules/project/job.tf", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "resource \"google_cloud_run_v2_job\" \"this\" {", "", ""},
	{"modules/workflow/job.tf", "deploy/terraform/gcp/modules/workflow/job.tf", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "resource \"google_cloud_run_v2_job\" \"this\" {", "", ""},
	{"modules/repo/check.tf", "deploy/terraform/gcp/modules/repo/check.tf", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "resource \"google_cloud_run_v2_job\" \"check\" {", "", ""},
	{"modules/installation/history.tf", "deploy/terraform/gcp/modules/installation/history.tf", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "resource \"google_cloud_run_v2_job\" \"history\" {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"internal/managed/stateaccounts.go", "internal/managed/stateaccounts.go", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "func AccountsOfState(data []byte) ([]StateResource, error) {", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/cli/init_review.go", "internal/cli/init_review.go", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "func (r *initRun) notCovered(p *tf.Plan, imports []tf.ImportKey) string {", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "#1 — There is no Cloud Run *service* in this repository, and the installation's own resource…", "var managedTypes = map[string]bool{", "", ""},
	{"internal/gateway/gateway.go", "internal/gateway/gateway.go", "#2 — The CLI's only HTTP surface is a loopback listener inside a job. — Exists.", "func Start(ctx context.Context, o Options) (*Server, error) {", "", ""},
	{"mobile/src/demo/safety.test.js", "mobile/src/demo/safety.test.js", "#3 — There is no WebSocket anywhere, in either direction. — Gap.", "const real = { fetch: globalThis.fetch, XMLHttpRequest: globalThis.XMLHttpRequest, WebSocket: globalThis.WebSocket };", "", ""},
	{"·/test/unit/spawnlog.test.ts", "", "#3 — There is no WebSocket anywhere, in either direction. — Gap.", "expect(writer).not.toMatch(/fetch\\(|https?:\\/\\/|new URL\\(|WebSocket|net\\.|request\\(/)", "", ""},
	{"internal/rtdb/stream.go", "internal/rtdb/stream.go", "#3 — There is no WebSocket anywhere, in either direction. — Gap.", "func (c *Client) Stream(ctx context.Context, path string) <-chan Event {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "#4 — The cost gate already understands a service's scale shape. — Exists.", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"deploy/terraform/gcp/modules/compute/variables.tf", "deploy/terraform/gcp/modules/compute/variables.tf", "#5 — The trust boundary is persons and groups, and reach-based membership is refused. — Rule.", "variable \"launchers\" {", "", ""},
	{"internal/cli/doctor_launcher.go", "internal/cli/doctor_launcher.go", "#5 — The trust boundary is persons and groups, and reach-based membership is refused. — Rule.", "func publicCanStart(p *runapi.GoogleIamV1Policy, project string) bool {", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "#5 — The trust boundary is persons and groups, and reach-based membership is refused. — Rule.", "func (e *initEngine) defaultMembers(ctx context.Context) error {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "#6 — \"The orchestrator holds a lease in Firebase\" has no referent: there is no lease record…", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "#6 — \"The orchestrator holds a lease in Firebase\" has no referent: there is no lease record…", "type Holder struct {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "#6 — \"The orchestrator holds a lease in Firebase\" has no referent: there is no lease record…", "func (l *leaseSource) Grant(ctx context.Context, need Micros) (Micros, error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "#7 — The takeover protocol the spec needs already exists in two places, and both are…", "func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "#7 — The takeover protocol the spec needs already exists in two places, and both are…", "var beforeTakeoverDelete func()", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "#8 — `internal/rtdb` has conditional writes but **no transaction**. — Rule.", "func (c *Client) GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error) {", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "#9 — The RTDB client knows the database's clock, and the ownership paths do not use it.…", "func (c *Client) ServerNow() (time.Time, bool) {", "", ""},
	{"internal/budget/session.go", "internal/budget/session.go", "#9 — The RTDB client knows the database's clock, and the ownership paths do not use it.…", "func (s *Session) now() time.Time {", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "#9 — The RTDB client knows the database's clock, and the ownership paths do not use it.…", "func (s *Supervisor) now() time.Time {", "", ""},
	{"internal/blobx/blobx.go", "internal/blobx/blobx.go", "#10 — There is no fencing token, no epoch and no generation field on any ownership record.…", "func (b *Bucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "#10 — There is no fencing token, no epoch and no generation field on any ownership record.…", "type RecordVersion struct {", "", ""},
	{"result.json", "result.json", "#10 — There is no fencing token, no epoch and no generation field on any ownership record.…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "#11 — The existing heartbeat is a liveness record with an independent sweeper, and its clock…", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "#11 — The existing heartbeat is a liveness record with an independent sweeper, and its clock…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"shared/lease/index.js", "shared/lease/index.js", "#12 — The spec's grace period and the heartbeat interval are the only clock inputs, and 10…", "//   Store.Claim(ctx, holder, at)             -> takeLease(state, { holder, at })", "", ""},
	{"internal/cli/run_attach.go", "internal/cli/run_attach.go", "#13 — A run cannot be attached to, and cannot be resumed live. — Gap.", "func attachLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, prefix string, paths []string, kind string, clone bool, warn io.Writer) (config.Inputs, error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "#13 — A run cannot be attached to, and cannot be resumed live. — Gap.", "func existingLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec) (*runstore.Launch, error) {", "", ""},
	{"launch.json", "launch.json", "#13 — A run cannot be attached to, and cannot be resumed live. — Gap.", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "#14 — What does exist is a conversation that can be resumed, and it is stored per run. — Exists.", "func NewSessionID() string {", "", ""},
	{"internal/runner/session.go", "internal/runner/session.go", "#14 — What does exist is a conversation that can be resumed, and it is stored per run. — Exists.", "func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored {", "", ""},
	{"internal/followup/prompt.go", "internal/followup/prompt.go", "#14 — What does exist is a conversation that can be resumed, and it is stored per run. — Exists.", "Resumed        bool     // the previous run's session was resumed", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "#14 — What does exist is a conversation that can be resumed, and it is stored per run. — Exists.", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"internal/runview/runview.go", "internal/runview/runview.go", "#15 — Nothing records who took a job over, and the one attribution the tree carries is display…", "// LaunchedBy is who started the run, as the run's own launch.json", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "#15 — Nothing records who took a job over, and the one attribution the tree carries is display…", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "#16 — A lease's staleness signal is weaker than the branch lock's, and that is a property of…", "func Stale(holder Holder, executionTerminal bool) bool {", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "#16 — A lease's staleness signal is weaker than the branch lock's, and that is a property of…", "var errOwnerUnknown = errors.New(\"could not tell whether another execution owns this run\")", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "#16 — A lease's staleness signal is weaker than the branch lock's, and that is a property of…", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "#16 — A lease's staleness signal is weaker than the branch lock's, and that is a property of…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"result.json", "result.json", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "func (s *Store) CreateRecord(ctx context.Context, r *Record) error {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "func ReplaceRecordIf(ctx context.Context, b *blobx.Bucket, slug, runID string, rec *Record, v RecordVersion) error {", "", ""},
	{"internal/backend/codec.go", "internal/backend/codec.go", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "func SameExecution(a, b string) bool {", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "func (r *run) claimRecord(ctx context.Context) error {", "", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job…", "func (s *Store) RequestCancel(ctx context.Context) error {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "#18 — No cap is keyed to a person or a service identity. — Wrong.", "func PathCapsRepo(slug string) string { return \"config/caps/repos/\" + seg(\"slug\", slug) }", "", ""},
	{"internal/budget/evaluate.go", "internal/budget/evaluate.go", "#18 — No cap is keyed to a person or a service identity. — Wrong.", "func Evaluate(st State, w Write, caps Caps, kills Kills, now time.Time) Decision {", "", ""},
	{"internal/budget/precheck.go", "internal/budget/precheck.go", "#18 — No cap is keyed to a person or a service identity. — Wrong.", "type PrecheckInput struct {", "", ""},
	{"internal/budget/evaluate.go", "internal/budget/evaluate.go", "#18 — No cap is keyed to a person or a service identity. — Wrong.", "type Write struct {", "", ""},
	{"internal/budget/token/token.go", "internal/budget/token/token.go", "#19 — A run's Firebase identity is its own uid, minted per run and revoked when it ends.…", "type Claims struct {", "", ""},
	{"internal/budget/token/mint.go", "internal/budget/token/mint.go", "#19 — A run's Firebase identity is its own uid, minted per run and revoked when it ends.…", "func Mint(ctx context.Context, signer Signer, c Claims) (string, error) {", "", ""},
	{"internal/budget/token/seal.go", "internal/budget/token/seal.go", "#19 — A run's Firebase identity is its own uid, minted per run and revoked when it ends.…", "func Sealed(data []byte) bool { return len(data) >= minEnvelope && data[0] == EnvelopeVersion }", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "#19 — A run's Firebase identity is its own uid, minted per run and revoked when it ends.…", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"SECURITY.md", "SECURITY.md", "#20 — The seal's own limits are documented, and they bound what a service may be given. — Rule.", "/proc/<pid>/environ", "", ""},
	{"docs/design/budget-token-channel.md", "docs/design/budget-token-channel.md", "#20 — The seal's own limits are documented, and they bound what a service may be given. — Rule.", "# The budget token's channel", "", ""},
	{"README.md", "docs/credentials-service/README.md", "#21 — Every credential path the spec names runs through unbuilt work. — Gap.", "**Status: plan only; nothing is implemented; decisions answered", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "#21 — Every credential path the spec names runs through unbuilt work. — Gap.", "OAuth tokens (phase 5)", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "#21 — Every credential path the spec names runs through unbuilt work. — Gap.", "a stopgap", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "#22 — Fugaro never merges, and the spec's orchestrator does. — Wrong.", "Auto-merge. Fugaro never merges.", "", ""},
	{"internal/gitprov/gitprov.go", "internal/gitprov/gitprov.go", "#22 — Fugaro never merges, and the spec's orchestrator does. — Wrong.", "type Provider interface {", "", ""},
	{"internal/cli/root.go", "internal/cli/root.go", "#22 — Fugaro never merges, and the spec's orchestrator does. — Wrong.", "root.AddCommand(", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it…", "* initialState builds the empty machine.", "", ""},
	{"internal/budget/rules/rules.json.tmpl", "internal/budget/rules/rules.json.tmpl", "#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it…", "\"head_sha\": { \".validate\": \"(newData.isString() && newData.val().length > 0) && SHAOK\" },", "", "the template writes the mailbox subtree twice -- under the inbox and under the outbox -- and the action node's head_sha validation is byte-identical in both"},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it…", "type Action struct {", "", ""},
	{"internal/cli/project_cmd.go", "internal/cli/project_cmd.go", "#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it…", "'fugaro pair' is plan task P6.6 and is not built", "", ""},
	{"shared/mailbox-schema/mac.js", "shared/mailbox-schema/mac.js", "#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it…", "* macOf is the tag a record carries: HMAC-SHA-256 over canonicalBytes(record),", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "#24 — The rules' actor model is the device node, and it is what makes a merge write possible at…", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/followup/trust.go", "internal/followup/trust.go", "#25 — \"Reviewer\" exists three times in the tree and none of them is a merge authority. — Rule.", "type Trust struct {", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "#25 — \"Reviewer\" exists three times in the tree and none of them is a merge authority. — Rule.", "// Trusted are the account IDs whose PR comments a follow-up acts on:", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "#25 — \"Reviewer\" exists three times in the tree and none of them is a merge authority. — Rule.", "// Labels and Reviewers are applied only when a pull request is made ready:", "", ""},
	{"internal/gitprov/github/github.go", "internal/gitprov/github/github.go", "#25 — \"Reviewer\" exists three times in the tree and none of them is a merge authority. — Rule.", "func (p *Provider) ApplyReady(ctx context.Context, number int, reviewers, labels []string) error {", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "#25 — \"Reviewer\" exists three times in the tree and none of them is a merge authority. — Rule.", "return claimed(providers, a.ModelFor(RoleCoder)) && !claimed(providers, a.ModelFor(RoleReviewer))", "", ""},
	{"SECURITY.md", "SECURITY.md", "#26 — The review-only mode already carries the tree's strongest author gate, and it is not a…", "**`mode: review` (`review-only`, `pick`) is trusted-author-only**", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "#27 — QR pairing is already ruled discovery-only in the mobile work; the *spec* that says so is…", "Adding a project: QR pairing", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "#27 — QR pairing is already ruled discovery-only in the mobile work; the *spec* that says so is…", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"mobile/package.json", "mobile/package.json", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "\"dependencies\": {", "", ""},
	{"mobile/src/feed/index.js", "mobile/src/feed/index.js", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "//   * **A port.** There is no database client in this directory, and there must", "", ""},
	{"mobile/src/feed/reader.js", "mobile/src/feed/reader.js", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "export class FeedReader {", "", ""},
	{"mobile/src/feed/paths.js", "mobile/src/feed/paths.js", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "export const FAMILIES = Object.freeze(['config/caps', 'config/kill', 'spend', 'mailbox']);", "", ""},
	{"mobile/src/mailbox/client.js", "mobile/src/mailbox/client.js", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "const error = new TypeError('createMailboxClient: the account tuple is not usable');", "", ""},
	{"mobile/src/demo/safety.test.js", "mobile/src/demo/safety.test.js", "#28 — The phone app has no server component, no HTTP and no socket, and reads the database…", "const RULES = [", "", ""},
	{"·/src/main/protocol.ts", "", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "* third directive of `CONTENT_SECURITY_POLICY` below, so a `<link", "", ""},
	{"·/src/main/ipc.ts", "", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)", "", ""},
	{"·/src/preload/index.ts", "", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "const names: readonly ExposedName[] = Object.freeze(EXPOSED_OPERATIONS.map(([name]) => name))", "", ""},
	{"·/src/main/spawn.ts", "", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === operation)", "", ""},
	{"docs/design/desktop-console.md", "docs/design/desktop-console.md", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "### 5.1 The hard rules", "", ""},
	{"·/docs/json-contract.md", "", "#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.", "line-delimited JSON — one of exactly two line-delimited documents the console", "", ""},
	{"internal/cli/watch.go", "internal/cli/watch.go", "#30 — The console's data plane is `fugaro watch --json`, and watch is read-only apart from an…", "func newWatchCmd() *cobra.Command {", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "#30 — The console's data plane is `fugaro watch --json`, and watch is read-only apart from an…", "type Supervisor struct {", "", ""},
	{"internal/cli/watch_queued.go", "internal/cli/watch_queued.go", "#30 — The console's data plane is `fugaro watch --json`, and watch is read-only apart from an…", "func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string, index *projectJobIndex) *queuedSource {", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "#32 — No public sign-in provider exists, and the platform refuses to enable one. — Wrong.", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/infra/identityplatform.go", "internal/infra/identityplatform.go", "#32 — No public sign-in provider exists, and the platform refuses to enable one. — Wrong.", "func PublicSignUp(raw []byte) ([]string, error) {", "", ""},
	{"deploy/terraform/gcp/modules/firebase/firebase.tf", "deploy/terraform/gcp/modules/firebase/firebase.tf", "#32 — No public sign-in provider exists, and the platform refuses to enable one. — Wrong.", "# The web API key a job signs in with: signInWithCustomToken and the refresh", "", ""},
	{"docs/mobile/functional-spec-reconciliation.md", "docs/mobile/functional-spec-reconciliation.md", "#32 — No public sign-in provider exists, and the platform refuses to enable one. — Wrong.", "**G12 — \"Anyone can authenticate with Firebase\" is false for a Fugaro installation. Wrong.**", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "#33 — Email verification is checked in exactly one place, and it is the CLI's own ADC path.…", "var authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "#34 — Push is deferred, and \"Fugaro's own notifications\" has no sender. — Gap.", "design only; nothing is implemented", "", ""},
	{"modules/installation/history.tf", "deploy/terraform/gcp/modules/installation/history.tf", "#34 — Push is deferred, and \"Fugaro's own notifications\" has no sender. — Gap.", "location = google_cloud_run_v2_job.history[0].location", "", ""},
	{"docs/credentials-service/reconciliation.md", "docs/credentials-service/reconciliation.md", "#34 — Push is deferred, and \"Fugaro's own notifications\" has no sender. — Gap.", "| **D141** | **`broken` and alerts** (F19)", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "#35 — A run is headless and cannot ask anything. — Exists / Gap.", "func Args(req Request) []string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "#35 — A run is headless and cannot ask anything. — Exists / Gap.", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "#35 — A run is headless and cannot ask anything. — Exists / Gap.", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "#36 — The Go mailbox is a schema with no importer, and it is the tree's only prompt/answer…", "const KindMessage = \"message\"", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "#36 — The Go mailbox is a schema with no importer, and it is the tree's only prompt/answer…", "case \"MACOK\":", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "#36 — The Go mailbox is a schema with no importer, and it is the tree's only prompt/answer…", "### Task P6.7: The consumer's enforcement of the D128 amendment — SECURITY-SENSITIVE", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "#38 — The prompt-queue policy the tree has is the mobile functional spec's, and it is a design.…", "Voice prompts from agents", "", ""},
	{"internal/localcfg/localcfg.go", "internal/localcfg/localcfg.go", "#39 — Nothing in the tree escalates on a timer, and rigor mode is not a timer source. — Wrong.", "// BurnAlertUSDPerHour is the spend rate above which watch marks a", "", ""},
	{"internal/watch/build.go", "internal/watch/build.go", "#39 — Nothing in the tree escalates on a timer, and rigor mode is not a timer source. — Wrong.", "// BurnAlertPerHour is watch.burn_alert_usd_per_hour in micro-dollars;", "", ""},
	{"docs/design/desktop-console-v2.md", "docs/design/desktop-console-v2.md", "#39 — Nothing in the tree escalates on a timer, and rigor mode is not a timer source. — Wrong.", "cap alerts have data and no threshold", "", ""},
	{"internal/cli/followup.go", "internal/cli/followup.go", "#40 — What does escalate today is a person reading a PR: the trusted-comment follow-up.…", "func resolvePR(ctx context.Context, env *cloudEnv, repo, slug string, pr int, exclude string, warn io.Writer) (*prChain, error) {", "", ""},
	{"internal/followup/trust.go", "internal/followup/trust.go", "#40 — What does escalate today is a person reading a PR: the trusted-comment follow-up.…", "type Trust struct {", "", ""},
	{"internal/followup/select.go", "internal/followup/select.go", "#40 — What does escalate today is a person reading a PR: the trusted-comment follow-up.…", "type Selection struct {", "", ""},
	{"internal/followup/prompt.go", "internal/followup/prompt.go", "#40 — What does escalate today is a person reading a PR: the trusted-comment follow-up.…", "func ImplementPrompt(d PromptData, sel Selection) string {", "", ""},
	{"internal/config/reservedenv_test.go", "internal/config/reservedenv_test.go", "#41 — No alert ingest exists: no webhook, no fingerprint, no de-duplication, no rate limit, no…", "for _, env := range []string{\"NPM_TOKEN\", \"SENTRY_DSN\", \"MAPS_API_KEY\", \"PATHWAY_KEY\", \"HOMEBREW_TOKEN\"} {", "", ""},
	{"internal/budget/rules/rules.json.tmpl", "internal/budget/rules/rules.json.tmpl", "#41 — No alert ingest exists: no webhook, no fingerprint, no de-duplication, no rate limit, no…", "\".write\": \"PAIRING && newData.exists() && !data.exists()\",", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "#41 — No alert ingest exists: no webhook, no fingerprint, no de-duplication, no rate limit, no…", "func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "#41 — No alert ingest exists: no webhook, no fingerprint, no de-duplication, no rate limit, no…", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "#42 — There is no automated-jobs budget, and no record carries a trigger kind. — Gap.", "type GlobalCaps struct {", "", ""},
	{"schemas/fugaro.schema.json", "schemas/fugaro.schema.json", "#42 — There is no automated-jobs budget, and no record carries a trigger kind. — Gap.", "\"description\": \"A model per stage role; a role left out uses agent.model. With a budget on, each must be an explicit model ID that has a price.\",", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "#43 — The kill switch is the only fleet-wide stop, and it is a human's. — Exists.", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"SECURITY.md", "SECURITY.md", "#44 — The runner already treats a run as untrusted, but it does not mark untrusted *input*.…", "a run is already the untrusted party", "", ""},
	{"internal/safetext/safetext.go", "internal/safetext/safetext.go", "#44 — The runner already treats a run as untrusted, but it does not mark untrusted *input*.…", "func OneLine(s string) string { return Text(s, false) }", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "#44 — The runner already treats a run as untrusted, but it does not mark untrusted *input*.…", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "#45 — A \"restricted toolset\" has one precedent, and it is narrow. — Exists (partial).", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "#46 — \"They see only the secrets the fix needs\" is not expressible today: secrets follow the…", "### 4.1 One job and one job service account per (profile, account), for clone workflows", "", ""},
	{"SECURITY.md", "SECURITY.md", "#46 — \"They see only the secrets the fix needs\" is not expressible today: secrets follow the…", "- **Runs of one repository can see each other, and the 0.7.0 bucket IAM work does not change that.**", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "#46 — \"They see only the secrets the fix needs\" is not expressible today: secrets follow the…", "## 10. The interim carrier, and what changes later", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "#47 — \"No auto-merge, even in hotfix\" is consistent with the tree and with the rigor modes…", "## 2. Goals and non-goals", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "#48 — Firestore is a server-side store: no client SDK may read or write it. — Rule.", "const FirestoreRules = `rules_version = '2';", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "#49 — Firestore has no TTL, no transactions and no listeners in this tree. — Gap.", "func (c *Client) Get(ctx context.Context, coll, id string) (*Doc, error) {", "", ""},
	{"docs/git-providers.md", "docs/git-providers.md", "#50 — \"Archive to the project's Git repo\" has no committer that is not a run. — Gap.", "Fugaro opens pull requests on **Bitbucket Cloud** and **GitHub**.", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "#51 — The coordinator is advisory and unbuilt, and this spec gives it a job it has no way to…", "reconciliation only; nothing is implemented and no plan is approved", "", ""},
	{"docs/coordinator/spec.md", "docs/coordinator/spec.md", "#51 — The coordinator is advisory and unbuilt, and this spec gives it a job it has no way to…", "- **Advisory, not authoritative.**", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "#52 — The always-on orchestrator design (PR #632) is the close sibling, and it disagrees with…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/mobile/reconciliation.md", "docs/mobile/reconciliation.md", "#53 — Mobile D115's conditions for phase 2 are already written, and this spec must meet them or…", "| **D115** | **Phase 2 hosting: who holds the model credential?**", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "#53 — Mobile D115's conditions for phase 2 are already written, and this spec must meet them or…", "### Task P5.1: Phase 2 hosting — the always-on orchestrator — UNSCHEDULED", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "#54 — The unit differs: per project in this spec, per installation in PR #632 — and the two are…", "## 1. Decisions at a glance", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "#55 — A stale citation that this work would land on top of. — Exists.", "- `docs/design/always-on-orchestrator.md` — the close sibling: one resident service per", "", ""},
	{"docs/coordinator/reconciliation.md", "docs/coordinator/reconciliation.md", "#55 — A stale citation that this work would land on top of. — Exists.", "**#29 — The close sibling is the always-on orchestrator (mobile P5.1, PR #632), and it makes two choices that contradict this spec. — Open.**", "", ""},
	{"docs/design/always-on-orchestrator.md", "docs/design/always-on-orchestrator.md", "#55 — A stale citation that this work would land on top of. — Exists.", "", "this path is the stale citation finding #55 records: the document is not at this path on main (PR #632 places it under docs/mobile/design/), so there is no line here to hold", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "#55 — A stale citation that this work would land on top of. — Exists.", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/windows-cli/README.md", "docs/windows-cli/README.md", "#56 — 0.10.0 is full. — Exists.", "0.10.0) because its data source is the CLI and the CLI has no Windows build,", "", ""},
	{"docs/credentials-service/README.md", "docs/credentials-service/README.md", "#56 — 0.10.0 is full. — Exists.", "the first product slice", "", ""},
	{"fugaro-mobile-app-spec.md", "fugaro-mobile-app-spec.md", "4. The exact edits the mobile spec needs — listed, and not made", "", "the file is not a file in this repository and exists nowhere: correction C5 records that the brief's bullet names a file the tree does not have, so there is no line here to hold", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "4. The exact edits the mobile spec needs — listed, and not made", "### 4.1 Adding a project: QR pairing", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "4. The exact edits the mobile spec needs — listed, and not made", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "4. The exact edits the mobile spec needs — listed, and not made", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "5. Decisions D1–D26", "newGen, err := l.b.ReplaceIf(ctx, l.key, body, l.gen, l.body)", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "5. Decisions D1–D26", "func (s *Store) ClaimKey() string { return s.prefix + \"launching\" }", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "5. Decisions D1–D26", "func (c *Client) ServerNow() (time.Time, bool) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "5. Decisions D1–D26", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "5. Decisions D1–D26", "type Holder struct {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "5. Decisions D1–D26", "func claimHolder() string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "5. Decisions D1–D26", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "5. Decisions D1–D26", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "5. Decisions D1–D26", "## 10. The interim carrier, and what changes later", "", ""},
	{"docs/design/desktop-console.md", "docs/design/desktop-console.md", "5. Decisions D1–D26", "a local read-only API served by the CLI", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "5. Decisions D1–D26", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "5. Decisions D1–D26", "var authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "5. Decisions D1–D26", "type Action struct {", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "5. Decisions D1–D26", "const VERB_VALUES = Object.freeze([...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS]);", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "5. Decisions D1–D26", "func actionField(value any) checked[*Action] {", "", ""},
	{"shared/mailbox-schema/mac.js", "shared/mailbox-schema/mac.js", "5. Decisions D1–D26", "* macOf is the tag a record carries: HMAC-SHA-256 over canonicalBytes(record),", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "5. Decisions D1–D26", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "5. Decisions D1–D26", "const FirestoreRules = `rules_version = '2';", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "5. Decisions D1–D26", "type Roller struct {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "5. Decisions D1–D26", "func newBudgetCmd() *cobra.Command {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "5. Decisions D1–D26", "func newBudgetCmd() *cobra.Command {", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "5. Decisions D1–D26", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/coordinator/spec.md", "docs/coordinator/spec.md", "5. Decisions D1–D26", "- **Advisory, not authoritative.**", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "5. Decisions D1–D26", "type Supervisor struct {", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "5. Decisions D1–D26", "## 2. Goals and non-goals", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "5. Decisions D1–D26", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "5. Decisions D1–D26", "var managedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "5. Decisions D1–D26", "path:  []any{\"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "5. Decisions D1–D26", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "5. Decisions D1–D26", "var managedTypes = map[string]bool{", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "5. Decisions D1–D26", "# Push notifications to the phone — P5.2", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "5. Decisions D1–D26", "## 4. Pairing and access", "", ""},
	{"internal/cli/docs_mobile_plan_test.go", "internal/cli/docs_mobile_plan_test.go", "5. Decisions D1–D26", "func TestDocsMobilePlanPhase6IsApproved(t *testing.T) {", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "5. Decisions D1–D26", "The numbers this work holds", "", ""},
	{"docs/design/voice.md", "docs/design/voice.md", "5. Decisions D1–D26", "the phase that ships the check writes them", "", ""},
	{"budget/sweep.go", "internal/budget/sweep.go", "6. For the owner — the answered six, and the open twenty", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	// D19's correction (P1, 2026-10-21) cites notCovered a second time: the sentence it corrects
	// claimed the admitted type asks a typed confirmation of its own, and this delegation — the
	// one line that decides which plans ask one — is what says the type does not.
	{"internal/cli/init_review.go", "internal/cli/init_review.go", "D19 — Per installation or per project, and one service per orchestrator", "func (r *initRun) notCovered(p *tf.Plan, imports []tf.ImportKey) string {", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "7. The numbers this work would hold", "The numbers this work holds", "", ""},
	{"README.md", "docs/mobile/voice-session/README.md", "7. The numbers this work would hold", "| **D201–D220** | this work's decisions, all written and **all open**.", "", ""},
	{"reconciliation.md", "docs/mobile/voice-session/reconciliation.md", "7. The numbers this work would hold", "tree. So this work starts at **D201** and leaves D193–D200 alone rather than", "", ""},
	{"docs/design/compute-backend.md", "docs/design/compute-backend.md", "7. The numbers this work would hold", "# A Docker-capable compute backend: workloads on Compute Engine VMs", "", ""},
	{"docs/mobile/README.md", "docs/mobile/README.md", "7. The numbers this work would hold", "Checks 70–79 are unused and deliberately not taken", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "7. The numbers this work would hold", "| **6 — APPROVED 2026-10-18 (functional spec v0.1)**", "", ""},
	{"docs/mobile/README.md", "docs/mobile/README.md", "7. The numbers this work would hold", "**100–104 proposed**", "", ""},
	{"docs/mobile/design/voice-evaluation-switch.md", "docs/mobile/design/voice-evaluation-switch.md", "7. The numbers this work would hold", "## 7. The register: providers, as read on 2026-10-19", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "7. The numbers this work would hold", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/design/always-on-orchestrator.md", "docs/design/always-on-orchestrator.md", "8. Items found while planning, kept here so they are not lost", "", "this path is the stale citation finding #55 records: the document is not at this path on main (PR #632 places it under docs/mobile/design/), so there is no line here to hold", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "8. Items found while planning, kept here so they are not lost", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "8. Items found while planning, kept here so they are not lost", "- **Follow-up:** update fugaro-mobile-app-spec.md so QR pairing is described as discovery only, with Firebase Auth as the identity layer.", "", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "8. Items found while planning, kept here so they are not lost", "Oct 20, 2026 · @Dimi", "", ""},
	{"docs/mobile/design/voice-evaluation-switch.md", "docs/mobile/design/voice-evaluation-switch.md", "8. Items found while planning, kept here so they are not lost", "## 7. The register: providers, as read on 2026-10-19", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "U1 — That a Cloud Run **service** can be admitted to the installation's Terraform without breaking `fugaro setup`'s confirmation model", "var allowedTypes = map[string]bool{", "", ""},
	{"docs/credentials-service/reconciliation.md", "docs/credentials-service/reconciliation.md", "U2 — WebSocket support through the Cloud Run front end (upgrade headers, idle timeout, maximum connection duration)", "| U5 | Cloud Run service limits: request timeout", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "U3 — RTDB `If-Match`/ETag behaviour under concurrent writers at the rate a lease implies (one heartbeat a minute per job, plus takeovers)", "func (c *Client) PutIfMatch(ctx context.Context, path, etag string, v any) error {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "U4 — Whether the RTDB rules can express the lease's generation check without a `RulesVersion` bump colliding with another work's", "t, err := template.New(\"rules\").Option(\"missingkey=error\").Parse(source)", "", ""},
	{"internal/projectpolicy/version.go", "internal/projectpolicy/version.go", "U4 — Whether the RTDB rules can express the lease's generation check without a `RulesVersion` bump colliding with another work's", "const PolicyRulesVersion = 1", "", ""},
	{"·/test/unit/window.test.ts", "", "U7 — Whether the console can reach the orchestrator without weakening `connect-src 'none'` in a way the design accepts", "\"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'\"", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "U8 — Whether a job's agent can be given a *narrower* toolset than `internal/agent/settings.go`'s `deniedWebTools` allows, without `--dangerously-skip-permissions` becoming per-tool", "var deniedWebTools = []string{\"WebSearch\", \"WebFetch\"}", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "U10 — Whether an alert job on a dedicated profile actually narrows secrets as D4 assumes", "### 4.1 One job and one job service account per (profile, account), for clone workflows", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "U13 — Whether `fugaro watch`'s stream can carry the lease without a second stream or a rules change", "type Supervisor struct {", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "10. Glossary", "## 1. Decisions at a glance", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "10. Glossary", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "10. Glossary", "type Holder struct {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "10. Glossary", "type leaseSource struct{ s *Session }", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "10. Glossary", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "10. Glossary", "// Kinds, Actions and Directions are the module's closed vocabularies, in the", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "10. Glossary", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
}

// TestDocsCentralOrchestratorCitationsAreAnchoredByContent: every citation in the
// reconciliation that names a file in this repository is held to the line it turns on, and the
// citations that name something this tree does not have are excused by name and reason.
//
// Three assertions hold the rule:
//
//   - no citation carries a line number, so the next edit cannot put one back and get a check
//     that reads whether the line exists and never what it says;
//   - every citation token in the document is named by exactly as many entries as the document
//     cites it, so a claim added against a file that already has an anchor fails until it has
//     one of its own -- the case a document-level check lets through; and
//   - each entry's anchor is carried by exactly one line of the file it names, so the claim and
//     the line behind it are the same fact, and a file that stops carrying the symbol or the
//     sentence fails.
func TestDocsCentralOrchestratorCitationsAreAnchoredByContent(t *testing.T) {
	doc := readDoc(t, centralOrchReconciled)
	matches := centralOrchCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) == 0 {
		t.Fatalf("%s cites no file at all: this test would otherwise check nothing", centralOrchReconciled)
	}
	for _, m := range matches {
		if centralOrchLineNumberRE.MatchString(m[0]) {
			t.Errorf("%s carries the citation %q with a line number: this folder cites by content and by symbol and never by line, because a line number into a document other works add to is stale the moment one of them inserts a paragraph above it, and nothing here would notice", centralOrchReconciled, m[0])
		}
	}

	cited := map[string]int{}
	for _, m := range matches {
		cited[m[1]]++
	}
	anchors, _ := centralOrchResolve(t, centralOrchContentAnchors, cited)
	held := map[string]int{}
	for _, a := range anchors {
		held[a.token]++
		if a.excused != "" {
			if !strings.Contains(a.excused, "PR #") && !strings.Contains(a.excused, "not a file in this repository") {
				t.Errorf("the entry for %s is excused with %q, which names neither the pull request the file lives in nor a run artifact: an excuse has to say what it is excusing", a.token, a.excused)
			}
			// The reason is written once, in centralOrchCitationsAbsent, and the entry
			// quotes it: a table entry whose reason has drifted from the declaration is
			// an excuse that declaration does not make, and that is exactly how the D2
			// row's shortened `budget/sweep.go` -- a file in this tree -- came to be
			// excused as a run artifact while the check read only the phrase.
			reason, declared := centralOrchCitationsAbsent[a.doc]
			switch {
			case !declared:
				t.Errorf("the entry for %s is excused to %s, and centralOrchCitationsAbsent does not carry that path: an excuse has to be declared where every other one is, or it is a reason this test wrote for itself", a.token, a.doc)
			case reason != a.excused:
				t.Errorf("the entry for %s is excused with %q and centralOrchCitationsAbsent gives %s the reason %q: the two have to be the same sentence, or the table excuses something the declaration does not", a.token, a.excused, a.doc, reason)
			}
			if _, err := os.Stat("../../" + filepath.FromSlash(a.doc)); err == nil {
				t.Errorf("%s is excused as %q, and the file is now in this tree: the excuse has expired, so the citation needs an anchor of the content it means", a.doc, a.excused)
			}
			continue
		}
		at, ok := centralOrchLineOf(t, a.doc, a.anchor, "the citation of "+a.token, a.repeated)
		if ok {
			t.Logf("%s is anchored at line %d of %s -- %s", a.token, at, a.doc, a.claim)
		}
	}
	for token, n := range cited {
		switch got := held[token]; {
		case got == 0:
			t.Errorf("%s cites %s %s and centralOrchContentAnchors holds none: the citation names a file and no longer says where in it, so nothing holds it to the line it turns on", centralOrchReconciled, token, centralOrchCount(n, "time", "times"))
		case got < n:
			t.Errorf("%s cites %s %s and centralOrchContentAnchors holds %s: one anchor holds one citation, so a citation with none behind it is read by nothing", centralOrchReconciled, token, centralOrchCount(n, "time", "times"), centralOrchCount(got, "anchor", "anchors"))
		case got > n:
			t.Errorf("centralOrchContentAnchors holds %s for %s and %s cites it %s: delete the entries no citation is left for, or the next citation that lands on that token is checked against a line the file has stopped carrying", centralOrchCount(got, "anchor", "anchors"), token, centralOrchReconciled, centralOrchCount(n, "time", "times"))
		}
	}
	for token := range held {
		if _, ok := cited[token]; !ok {
			t.Errorf("centralOrchContentAnchors holds an entry for %s and %s never cites it: a pin with no citation behind it is a claim nothing in the document makes", token, centralOrchReconciled)
		}
	}
	t.Logf("%s: %d citations held by %d content anchors, none of them by a line number", centralOrchReconciled, len(matches), len(anchors))
}

// centralOrchCount renders a count with its singular or plural noun, so the failure messages
// read as sentences rather than as "(2 time)".
func centralOrchCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// The four shapes the reconciliation labels a citation's position with, tried against the line
// the citation sits on and then upwards: a section heading, a subsection heading, a finding's
// own bold heading, and a decision or UNVERIFIED table row (whose label is its id and its first
// cell, "D19 — Per installation or per project …").
var (
	centralOrchClaimSectionRE  = regexp.MustCompile(`^## (.+)$`)
	centralOrchClaimSubheadRE  = regexp.MustCompile(`^### (.+)$`)
	centralOrchClaimFindingRE  = regexp.MustCompile(`^\*\*#\d+ — .+\*\*$`)
	centralOrchClaimTableRowRE = regexp.MustCompile(`^\| \*{0,2}([A-Z]{1,2}\d+)\*{0,2} \| ([^|]+)\|`)
)

// centralOrchStatusClaim is the claim an entry carries for a citation above the document's first
// section: there is no heading to read, and the status block is where such a citation sits.
const centralOrchStatusClaim = "the document's status block, above its first section"

// centralOrchClaimAt reads the labels a citation at line at (0-based) can honestly be claimed
// under: the innermost label at or above it, and the section it is in. Two, not one, because
// this table's claims are written at both levels — a citation inside a finding is claimed by the
// finding ("#17 — A run's lifecycle …") and one inside a section's prose by the section
// ("7. The numbers this work would hold") — and both are true of it. When neither exists the
// citation is in the status block.
func centralOrchClaimAt(lines []string, at int) []string {
	inner, section := "", ""
	for i := at; i >= 0; i-- {
		line := strings.TrimRight(lines[i], " \t")
		if inner == "" {
			switch {
			case centralOrchClaimSubheadRE.MatchString(line):
				inner = centralOrchClaimSubheadRE.FindStringSubmatch(line)[1]
			case centralOrchClaimSectionRE.MatchString(line):
				inner = centralOrchClaimSectionRE.FindStringSubmatch(line)[1]
			case centralOrchClaimFindingRE.MatchString(line):
				inner = strings.TrimSuffix(strings.TrimPrefix(line, "**"), "**")
			case centralOrchClaimTableRowRE.MatchString(line):
				m := centralOrchClaimTableRowRE.FindStringSubmatch(line)
				inner = m[1] + " — " + strings.TrimSpace(strings.ReplaceAll(m[2], "**", ""))
			}
		}
		if section == "" && centralOrchClaimSectionRE.MatchString(line) {
			section = centralOrchClaimSectionRE.FindStringSubmatch(line)[1]
		}
		if inner != "" && section != "" {
			break
		}
	}
	claims := make([]string, 0, 2)
	for _, c := range []string{inner, section} {
		// The section is its own innermost label when the citation sits directly under it, and
		// naming it twice in a failure message reads as two labels.
		if c != "" && (len(claims) == 0 || claims[len(claims)-1] != c) {
			claims = append(claims, c)
		}
	}
	if len(claims) == 0 {
		return []string{centralOrchStatusClaim}
	}
	return claims
}

// centralOrchClaimMatches reports whether a claim field can be read as the label it was written
// from. A claim is often the label truncated with an ellipsis ("#1 — There is no Cloud Run
// *service* in this repository, and the installation's own resource…"), so a claim that is a
// prefix of the label is the same claim; emphasis is not part of either, and neither is the
// ellipsis.
func centralOrchClaimMatches(claim, label string) bool {
	norm := func(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "**", "")) }
	claim, label = norm(claim), norm(label)
	if strings.HasSuffix(claim, "…") {
		claim = strings.TrimSpace(strings.TrimSuffix(claim, "…"))
	}
	return label == claim || strings.HasPrefix(label, claim)
}

// TestDocsCentralOrchestratorClaimsMatchWhereTheySit: each entry's claim is the label the
// citation is *actually under*, read from the document, and not a free-text note beside the
// anchor.
//
// The other three assertions in this guard read the entry's token, its doc and its anchor, and
// leave the claim as a comment: an entry can say it holds a citation inside finding #17 while
// the citation sits in the glossary, and every one of them still passes. That is what the
// generation-4 lease's entries did — seven of them, for the glossary's "Lease" bullet, each
// claiming the U13 row three sections above, because they were written by copying the rows next
// to them. The claim is the one field that says *why* the line behind the anchor is the right
// one, so it is the field that has to be read.
//
// The pairing is per token and in document order: the table holds one entry per citation, so the
// Nth citation of a token in the document is the Nth entry for it. (It is per token rather than
// across the table because the table is not strictly in document order — a later edit inserted
// entries beside the ones they were copied from.)
func TestDocsCentralOrchestratorClaimsMatchWhereTheySit(t *testing.T) {
	doc := readDoc(t, centralOrchReconciled)
	lines := strings.Split(doc, "\n")
	at := map[string][]int{}
	cited := map[string]int{}
	for _, m := range centralOrchCitationRE.FindAllStringSubmatchIndex(doc, -1) {
		token := doc[m[2]:m[3]]
		at[token] = append(at[token], strings.Count(doc[:m[2]], "\n"))
		cited[token]++
	}
	anchors, _ := centralOrchResolve(t, centralOrchContentAnchors, cited)
	byToken := map[string][]centralOrchContentAnchor{}
	for _, a := range anchors {
		byToken[a.token] = append(byToken[a.token], a)
	}
	checked := 0
	for token, entries := range byToken {
		sites := at[token]
		if len(sites) != len(entries) {
			// The count assertions in the test above are the ones that report this, in the
			// terms the table's own rule is written in; here it only means the pairing cannot
			// be made, so this check says nothing rather than guessing at one.
			t.Logf("%s: %s is cited %s and held by %s, so the claim of each is not paired here", centralOrchReconciled, token, centralOrchCount(len(sites), "time", "times"), centralOrchCount(len(entries), "anchor", "anchors"))
			continue
		}
		for i, a := range entries {
			claims := centralOrchClaimAt(lines, sites[i])
			ok := false
			for _, c := range claims {
				if centralOrchClaimMatches(a.claim, c) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("centralOrchContentAnchors claims the citation of %s at line %d of %s sits under %q, and the document labels it %s: a claim that is not where the citation sits is a note beside the anchor rather than the reason the line behind it is the right one, so either the claim is wrong or the anchor is holding a different citation than the entry means",
					a.token, sites[i]+1, centralOrchReconciled, a.claim, strings.Join(quoteAll(claims), " or "))
			}
			checked++
		}
	}
	t.Logf("%s: %d claims read against the label the citation is under", centralOrchReconciled, checked)
}

// quoteAll renders each label in quotes, for a failure message that names every label the
// citation could honestly be claimed under.
func quoteAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, strconv.Quote(s))
	}
	return out
}

// TestDocsCentralOrchestratorCitationsExist: every repository path reconciliation.md cites
// resolves on disk, and the check is measured against an independent count of the document's
// path-looking tokens.
//
// The census is the half that keeps the citation check honest: a citation written in a shape no
// matcher knows is not a failure of the matcher, it is a citation nothing reads, and the only
// way to see it is to count the path-looking tokens and require the check to account for all of
// them. The markdown links are counted too, because a link is a citation.
//
// The paths that do not resolve are the ones the document itself says are not on this tree --
// the open pull requests' documents and the stale path of finding #55 -- plus the run artifacts
// a run writes under runs/<slug>/<run>/. They are named in centralOrchCitationsAbsent with the
// reason, and the test requires each of them to still be absent, so the list cannot quietly
// outlive its reasons.
func TestDocsCentralOrchestratorCitationsExist(t *testing.T) {
	doc := readDoc(t, centralOrchReconciled)
	matches := centralOrchCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) == 0 {
		t.Fatalf("%s cites no file at all", centralOrchReconciled)
	}
	for _, m := range matches {
		path := m[1]
		if centralOrchCitationAbsent(path) {
			continue
		}
		found := ""
		for _, prefix := range centralOrchCitationPrefixes {
			if _, err := os.Stat("../../" + filepath.FromSlash(prefix+path)); err == nil {
				found = prefix + path
				break
			}
		}
		if found == "" {
			t.Errorf("%s cites %s and no such file is in this tree, in either the repository-relative or the deploy/terraform/gcp/-relative form: a citation into a file that is not here is a claim no reader can check, and if it is deliberate it belongs in centralOrchCitationsAbsent with its reason", centralOrchReconciled, path)
		}
	}
	// The census: every path-looking token, against the citations the matcher claimed. It is
	// counted per token and by occurrence, not as a set and not by total, because the claim is
	// that the matcher read each one of them: a count that only compared totals would let a
	// matcher that reads one phrase twice and misses two others pass.
	census := map[string]int{}
	for _, m := range centralOrchCensusRE.FindAllStringSubmatch(doc, -1) {
		census[m[1]]++
	}
	cited := map[string]int{}
	for _, m := range matches {
		cited[m[1]]++
	}
	for token, n := range census {
		if cited[token] < n {
			t.Errorf("%s writes %s %s and the citation matcher read %s: a citation in a shape the matcher does not know is a citation nothing checks, so teach the matcher the shape rather than leaving it unread", centralOrchReconciled, token, centralOrchCount(n, "time", "times"), centralOrchCount(cited[token], "of them", "of them"))
		}
	}
	total := 0
	for _, n := range census {
		total += n
	}
	if total != len(matches) {
		t.Errorf("%s has %d path-looking tokens and the citation matcher claimed %d: a citation is written in a shape the matcher does not know, so nothing checks it -- teach the matcher the shape rather than leaving it unread", centralOrchReconciled, total, len(matches))
	}
	// A markdown link is a citation too, and its target is resolved against this document's
	// own directory, the way the reader's browser resolves it. The check is the folder's, not
	// this file's: TestDocsCentralOrchestratorLinksResolve runs it over all four documents,
	// because the reconciliation is not the only one that links to its siblings.
	t.Logf("%s: %d citations claimed by the matcher over %d path-looking tokens, %d markdown links",
		centralOrchReconciled, len(matches), total, len(centralOrchMarkdownLinkRE.FindAllString(doc, -1)))
}

// TestDocsCentralOrchestratorLinksResolve: every inline markdown link in the folder resolves
// against the directory it is written in. A link is a citation the reader follows by clicking,
// so a target that is not there is a citation no reader can check -- and a bare sibling name
// ("implementation-plan.md") is the shape this folder uses most, which is why the matcher reads
// it and not only the ./-prefixed form. A target that is a fragment alone is an anchor into the
// document it is written in, and a fragment on a file target is resolved by the browser after
// the file loads: neither is a path to stat, so the fragment is cut before the file is.
func TestDocsCentralOrchestratorLinksResolve(t *testing.T) {
	for _, path := range []string{centralOrchReadme, centralOrchSpec, centralOrchReconciled, centralOrchPlan} {
		doc := readDoc(t, path)
		dir := filepath.Dir(filepath.Join("../../", path))
		for _, m := range centralOrchMarkdownLinkRE.FindAllStringSubmatch(doc, -1) {
			target := strings.TrimSpace(strings.SplitN(m[1], "#", 2)[0])
			if target == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links to %s and no such file or directory is beside it in this tree: a markdown link is a citation too, and it is followed by a reader the same way, so a link that 404s is a citation no reader can check", path, target)
			}
		}
	}
}

// centralOrchCitationsAbsent are the citations that name something this tree does not have,
// each with the reason the document itself gives. Nothing else may fail to resolve.
var centralOrchCitationsAbsent = map[string]string{
	// The documents that live only in an open pull request, and the stale path finding #55
	// records. These are the ones the anchor table excuses, and its excuse has to be this
	// sentence, word for word: the reason is written once, here, and quoted there.
	"docs/mobile/design/always-on-orchestrator.md": "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to",
	"docs/design/always-on-orchestrator.md":        "this path is the stale citation finding #55 records: the document is not at this path on main (PR #632 places it under docs/mobile/design/), so there is no line here to hold",
	"docs/rigor-modes/spec.md":                     "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to",
	"docs/rigor-modes/reconciliation.md":           "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to",
	"docs/rigor-modes/implementation-plan.md":      "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to",
	"docs/rigor-modes/README.md":                   "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to",
	"fugaro-mobile-app-spec.md":                    "the file is not a file in this repository and exists nowhere: correction C5 records that the brief's bullet names a file the tree does not have, so there is no line here to hold",
	// The run artifacts a run writes under runs/<slug>/<run>/.
	"result.json":          "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held",
	"launch.json":          "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held",
	"task.json":            "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held",
	"comments.json":        "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held",
	"session/session.json": "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held",
	// The bare names. Each occurrence is resolved by the anchor table to the document its
	// sentence means, so these are paths the *existence* test must not stat rather than
	// citations nothing holds; the reason says which.
	"README.md":              "cited bare, and the anchor table resolves every occurrence to the README.md its sentence means",
	"spec.md":                "cited bare, and the anchor table resolves every occurrence to the spec.md its sentence means",
	"reconciliation.md":      "cited bare, and the anchor table resolves every occurrence to the reconciliation.md its sentence means",
	"implementation-plan.md": "cited bare, and the anchor table resolves every occurrence to the implementation-plan.md its sentence means",
	// Not an excuse: the D2 row writes the module's name twice, and the shortened second
	// citation is anchored on internal/budget/sweep.go like any other. It is here because the
	// short spelling is not a path on disk, and the existence test must not stat it.
	"budget/sweep.go": "the D2 row's shortened second citation of internal/budget/sweep.go, which is in this tree and is anchored on that file in centralOrchContentAnchors",
}

// centralOrchCitationAbsent reports whether a citation names something this tree does not have.
func centralOrchCitationAbsent(path string) bool {
	_, ok := centralOrchCitationsAbsent[path]
	return ok
}
