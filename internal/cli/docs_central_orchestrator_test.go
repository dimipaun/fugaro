package cli

// The central-orchestrator docs package, pinned -- part P0 of this folder's plan, and
// nothing else. docs/central-orchestrator/ holds the owner's spec for central orchestrators
// (spec.md, verbatim, committed alone), the reconciliation of that spec against the tree at
// origin/main 2f8b4dca (reconciliation.md), the index (README.md) and the plan
// (implementation-plan.md). P0 is the guard test that owns the folder and its citations; it
// is the one piece of Go this work has written, and it implements no part of the orchestrator.
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
//    that an answer is not an approval, and the sentence that says P0 is the only part of the
//    plan that has landed.
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
// implemented", and P0 landing is the one thing that would make that sentence false. The
// sentence keeps the phrase and states its own exception, so neither half can be dropped
// silently.
const centralOrchReadmeStatus = "**Status: plan only; no code is implemented, except the guard test P0 adds (`internal/cli/docs_central_orchestrator_test.go`, the one file that owns this folder) — every part after P0 is unbuilt, and no feature code exists.**"

// centralOrchReadmeAnswered, centralOrchReadmeApproval, centralOrchReconciledAnswered and
// centralOrchReconciledRegister are the sentences the owner's answers of 2026-10-20 put into
// the two documents. Each is pinned whole, because each is a place a later edit can soften the
// claim without touching the others: that six decisions are answered, that answering one is
// not approving anything, and that the register reading was corrected rather than quoted.
const (
	centralOrchReadmeAnswered        = "**Six of the twenty-six decisions are answered — D1, D2, D3, D6, D12 and D19, each as its recommendation, by the owner on 2026-10-20 — and D4–D5, D7–D11, D13–D18 and D20–D26 are still open**"
	centralOrchReadmeApproval        = "**Six decisions — D1, D2, D3, D6, D12 and D19 — were answered by the owner on 2026-10-20, each as the recommendation the plan was written to, and an answer is not an approval of anything**"
	centralOrchReadmeP0Only          = "no part of the plan has been accepted except P0 (the guard test)"
	centralOrchReconciledAnswered    = "**Six of the twenty-six decisions — D1, D2, D3, D6, D12 and D19 — were answered by the owner on 2026-10-20, each as its recommendation, and an answer is not an approval**"
	centralOrchReconciledStatus      = "**Status: reconciliation only; nothing is implemented and no plan is approved.**"
	centralOrchReconciledRegister    = "**The register, read by name against `main`.**"
	centralOrchReconciledNextFree    = "**The next free block is D221 onward, not D193 onward.**"
	centralOrchReadmeOwner           = "**Owner: Dimi Paun. Date: 2026-10-20. Base: `main` at `2f8b4dca`**"
	centralOrchPlanStatus            = "P0 — the guard test that pins this folder and its citations, `internal/cli/docs_central_orchestrator_test.go` — has landed"
	centralOrchReadmeNoCheck         = "**not claimed and not written.**"
	centralOrchReadmeRegisterReading = "**the highest D-number written anywhere under `docs/` is D220 and the next free block is D221 onward**"
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

// centralOrchMarkdownLinkRE finds an inline markdown link's target, which is a citation too.
var centralOrchMarkdownLinkRE = regexp.MustCompile(`\]\(\.\.?/[^)]+\)`)

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
		"the status line, with its exception stated in the same sentence": centralOrchReadmeStatus,
		"the sentence naming the six answered decisions":                  centralOrchReadmeAnswered,
		"the sentence that answering one is not an approval":              centralOrchReadmeApproval,
		"the sentence that only P0 is accepted":                           centralOrchReadmeP0Only,
		"the owner, the date and the commit checked against":              centralOrchReadmeOwner,
		"the corrected register reading":                                  centralOrchReadmeRegisterReading,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %s: %q", centralOrchReadme, what, want)
		}
	}
	if strings.Contains(readme, "Status: plan only; no feature code is implemented") {
		t.Errorf("%s still carries the pre-P0 status wording: P0 has landed, so the sentence has to say which Go exists and which does not", centralOrchReadme)
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
		t.Errorf("%s lacks the sentence recording that P0, and only P0, has landed: %q", centralOrchPlan, centralOrchPlanStatus)
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
	doc      string // the file that spelling resolves to
	claim    string // the claim the citation makes
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
	{"spec.md", "docs/central-orchestrator/spec.md", "# Central orchestrators — the reconciliation **Status: reconciliation only; nothing is implemented and no plan is app…", "# Fugaro Central Orchestrator Spec", "", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "**Where this document disagrees with `spec.md`, it wins.** No Go, no TypeScript, no Terraform, no rules change and no…", "## Resolved questions", "", ""},
	{"internal/cli/docs_central_orchestrator_test.go", "internal/cli/docs_central_orchestrator_test.go", "**Where this document disagrees with `spec.md`, it wins.** No Go, no TypeScript, no Terraform, no rules change and no…", "const centralOrchCheckedAgainst = \"2f8b4dca\"", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "Two sources are **not on `main`** and are cited with that stated: the always-on orchestrator design (**PR #632**, bra…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "Where a citation names a document rather than a symbol, it names the heading or the phrase the sentence turns on, for…", "**Every block is cited by content and not by line**", "", ""},
	{"internal/cli/docs_artifact_runs_test.go", "internal/cli/docs_artifact_runs_test.go", "In particular it writes **no guard test**: the folder-per-work convention in this tree is a guard package beside the …", "func TestArtifactRunsDesignSecurityText(t *testing.T) {", "", ""},
	{"internal/cli/docs_windows_cli_test.go", "internal/cli/docs_windows_cli_test.go", "In particular it writes **no guard test**: the folder-per-work convention in this tree is a guard package beside the …", "func TestDocsWindowsCLIIsWritten(t *testing.T) {", "", ""},
	{"internal/cli/docs_base_test.go", "internal/cli/docs_base_test.go", "Nothing else needs registering: there is no docs-folder registry (`internal/clidoc/` holds the CLI reference generato…", "func readDoc(t *testing.T, path string) string {", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "- **C2 — \"Firebase\" is two stores in this tree, and the spec's word covers both.** The budget database is the **Realt…", "func (c *Client) Get(ctx context.Context, path string, out any) (found bool, err error) {", "", ""},
	{"internal/rtdb/stream.go", "internal/rtdb/stream.go", "- **C2 — \"Firebase\" is two stores in this tree, and the spec's word covers both.** The budget database is the **Realt…", "func (c *Client) Stream(ctx context.Context, path string) <-chan Event {", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "Firestore is a separate, hand-written REST client (`internal/firestore/client.go` `Get`, `Patch`, `Query`, `Delete`) …", "func (c *Client) Get(ctx context.Context, coll, id string) (*Doc, error) {", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "Firestore is a separate, hand-written REST client (`internal/firestore/client.go` `Get`, `Patch`, `Query`, `Delete`) …", "type Roller struct {", "", ""},
	{"README.md", "docs/credentials-service/README.md", "- **C3 — \"the security service\" is a document, not a service.** `docs/credentials-service/` says it twice: *\"plan onl…", "**Status: plan only; nothing is implemented; decisions answered", "", ""},
	{"spec.md", "docs/credentials-service/spec.md", "the phone client backend) go in sibling services that call it\"* (`spec.md`, \"Shape\" and \"Boundary\").", "## Security service (credential proxy)", "", ""},
	{"README.md", "docs/coordinator/README.md", "- **C4 — and so is \"the coordinator\".** `docs/coordinator/` is *\"reconciliation only; nothing is implemented and no p…", "**Status: reconciliation only; nothing is implemented and no plan is approved.**", "", ""},
	{"spec.md", "docs/coordinator/spec.md", "- **C4 — and so is \"the coordinator\".** `docs/coordinator/` is *\"reconciliation only; nothing is implemented and no p…", "- **Advisory, not authoritative.**", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "The mobile spec is `docs/mobile/functional-spec-v0.1.md`, the rulings that already fixed the confusion are D165, D166…", "# Fugaro Mobile App — Functional Spec (v0.1)", "", ""},
	{"docs/mobile/functional-spec-reconciliation.md", "docs/mobile/functional-spec-reconciliation.md", "The mobile spec is `docs/mobile/functional-spec-v0.1.md`, the rulings that already fixed the confusion are D165, D166…", "**G12 — \"Anyone can authenticate with Firebase\" is false for a Fugaro installation. Wrong.**", "", ""},
	{"docs/rigor-modes/spec.md", "docs/rigor-modes/spec.md", "It does not: `docs/rigor-modes/spec.md`, `reconciliation.md`, `implementation-plan.md` and `README.md` (PR #773) cont…", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"reconciliation.md", "docs/rigor-modes/spec.md", "It does not: `docs/rigor-modes/spec.md`, `reconciliation.md`, `implementation-plan.md` and `README.md` (PR #773) cont…", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"implementation-plan.md", "docs/rigor-modes/spec.md", "It does not: `docs/rigor-modes/spec.md`, `reconciliation.md`, `implementation-plan.md` and `README.md` (PR #773) cont…", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"README.md", "docs/rigor-modes/spec.md", "It does not: `docs/rigor-modes/spec.md`, `reconciliation.md`, `implementation-plan.md` and `README.md` (PR #773) cont…", "", "the document lives only in open PR #773 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/gateway/gateway.go", "internal/gateway/gateway.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "func Start(ctx context.Context, o Options) (*Server, error) {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "type Holder struct {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "func PathCapsRepo(slug string) string { return \"config/caps/repos/\" + seg(\"slug\", slug) }", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "The spec's concepts against what the repository has | The spec says | What the tree has, at `2f8b4dca` | Verdict | | …", "Auto-merge. Fugaro never merges.", "", ""},
	{"internal/gitprov/gitprov.go", "internal/gitprov/gitprov.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "type Provider interface {", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "const VERB_VALUES = Object.freeze([...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS]);", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "type Action struct {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/infra/identityplatform.go", "internal/infra/identityplatform.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "func PublicSignUp(raw []byte) ([]string, error) {", "", ""},
	{"deploy/terraform/gcp/modules/firebase/firebase.tf", "deploy/terraform/gcp/modules/firebase/firebase.tf", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "service = \"identitytoolkit.googleapis.com\"", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "# Push notifications to the phone — P5.2", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "type SessionMeta struct {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "FirestoreMetaCollection = \"meta\"", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "// Operations: Get, Patch (an upsert whose updateMask lists every field it", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "func Args(req Request) []string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "Fugaro never merges.\"* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comme…", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"task.json", "task.json", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Store) CreateTask(ctx context.Context, spec *task.Spec) error {", "", ""},
	{"launch.json", "launch.json", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Store) WriteLaunch(ctx context.Context, l *Launch) error {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "type Claim struct {", "", ""},
	{"result.json", "result.json", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Store) CreateRecord(ctx context.Context, r *Record) error {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func ReplaceRecordIf(ctx context.Context, b *blobx.Bucket, slug, runID string, rec *Record, v RecordVersion) error {", "", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Store) RequestCancel(ctx context.Context) error {", "", ""},
	{"session/session.json", "session/session.json", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"comments.json", "comments.json", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runner/followup.go", "internal/runner/followup.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "var snap followup.Snapshot", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func Acquire(ctx context.Context, b *blobx.Bucket, key string, h Holder, now time.Time) (*Lock, error) {", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Session) Start(ctx context.Context, entry AgentEntry) error {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Session) grant(ctx context.Context, need Micros) (Micros, error) {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Session) Finish(ctx context.Context, status string) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "res, err := budget.SetKill(ctx, db, path, kill, me, o.reason, budget.KillHooks{", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "type Roller struct {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "**Where a run's state lives today** | Where | Object / node | Written by | What it is | | --- | --- | --- | --- | | R…", "FirestoreMetaCollection = \"meta\"", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "The nearest is the launch claim: one per run, held for `runstore.ClaimTTL` (10 minutes), judged stale against the **h…", "func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "The nearest is the launch claim: one per run, held for `runstore.ClaimTTL` (10 minutes), judged stale against the **h…", "func releaseClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "If the assumption fails, the result is a double launch, which the runner's duplicate-execution check contains.\"* The …", "func claimHolder() string {", "", ""},
	{"internal/runview/runview.go", "internal/runview/runview.go", "`internal/runview/runview.go`'s `Row.LaunchedBy` is explicitly untrusted display text; the budget token's `RB` claim …", "// LaunchedBy is who started the run, as the run's own launch.json", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "`internal/runview/runview.go`'s `Row.LaunchedBy` is explicitly untrusted display text; the budget token's `RB` claim …", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "`internal/mailbox/message.go` defines `Message`, `Action`, `Confirmation`, `Heartbeat` and the validators `ValidateMe…", "const KindMessage = \"message\"", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "The only conversation storage is per run (`internal/runstore/session.go`), accompanied by `internal/agent/session.go`…", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"internal/agent/session.go", "internal/agent/session.go", "The only conversation storage is per run (`internal/runstore/session.go`), accompanied by `internal/agent/session.go`…", "func SessionDir(home, workDir string) string {", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "The only conversation storage is per run (`internal/runstore/session.go`), accompanied by `internal/agent/session.go`…", "func NewSessionID() string {", "", ""},
	{"internal/runner/session.go", "internal/runner/session.go", "The only conversation storage is per run (`internal/runstore/session.go`), accompanied by `internal/agent/session.go`…", "func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored {", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "**A liveness heartbeat.** Exists, for a run, in RTDB: `internal/budget/registry.go`'s `Session.Start` / `heartbeat` /…", "func (s *Session) Start(ctx context.Context, entry AgentEntry) error {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "**A liveness heartbeat.** Exists, for a run, in RTDB: `internal/budget/registry.go`'s `Session.Start` / `heartbeat` /…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "**The second half of the invariant already exists.** Two orchestrators cannot both be the same run's *runner*: `inter…", "func (r *run) claimRecord(ctx context.Context) error {", "", ""},
	{"result.json", "result.json", "**The second half of the invariant already exists.** Two orchestrators cannot both be the same run's *runner*: `inter…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runner/lockcache.go", "internal/runner/lockcache.go", "**The second half of the invariant already exists.** Two orchestrators cannot both be the same run's *runner*: `inter…", "var ErrDuplicateExecution = errors.New(\"another execution already owns this run\")", "", ""},
	{"internal/backend/codec.go", "internal/backend/codec.go", "**The second half of the invariant already exists.** Two orchestrators cannot both be the same run's *runner*: `inter…", "func SameExecution(a, b string) bool {", "", ""},
	{"modules/project/job.tf", "deploy/terraform/gcp/modules/project/job.tf", "— Wrong.** Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job` (`modules/project…", "resource \"google_cloud_run_v2_job\" \"this\" {", "", ""},
	{"modules/workflow/job.tf", "deploy/terraform/gcp/modules/workflow/job.tf", "— Wrong.** Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job` (`modules/project…", "resource \"google_cloud_run_v2_job\" \"this\" {", "", ""},
	{"modules/repo/check.tf", "deploy/terraform/gcp/modules/repo/check.tf", "— Wrong.** Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job` (`modules/project…", "resource \"google_cloud_run_v2_job\" \"check\" {", "", ""},
	{"modules/installation/history.tf", "deploy/terraform/gcp/modules/installation/history.tf", "— Wrong.** Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job` (`modules/project…", "resource \"google_cloud_run_v2_job\" \"history\" {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "A repo-wide search for `google_cloud_run_v2_service` finds no resource, only cost-rule rows (`internal/infra/tf/manag…", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"internal/managed/stateaccounts.go", "internal/managed/stateaccounts.go", "A repo-wide search for `google_cloud_run_v2_service` finds no resource, only cost-rule rows (`internal/infra/tf/manag…", "func AccountsOfState(data []byte) ([]StateResource, error) {", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "The deciding symbol is `internal/infra/tf/cover.go`'s `allowedTypes`: it lists `google_cloud_run_v2_job` and `google_…", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/cli/init_review.go", "internal/cli/init_review.go", "The deciding symbol is `internal/infra/tf/cover.go`'s `allowedTypes`: it lists `google_cloud_run_v2_job` and `google_…", "func (r *initRun) notCovered(p *tf.Plan, imports []tf.ImportKey) string {", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "`managedTypes` in `internal/infra/tf/managed_cover.go` is the narrower list for managed projects — **14 entries to `a…", "var managedTypes = map[string]bool{", "", ""},
	{"internal/gateway/gateway.go", "internal/gateway/gateway.go", "— Exists.** `internal/gateway/gateway.go`'s `Start` (a package-level function, not a method) listens on `127.0.0.1:0`…", "func Start(ctx context.Context, o Options) (*Server, error) {", "", ""},
	{"mobile/src/demo/safety.test.js", "mobile/src/demo/safety.test.js", "— Gap.** No WebSocket client or server exists in the tree; the only `websocket` strings are test fences (`mobile/src/…", "const real = { fetch: globalThis.fetch, XMLHttpRequest: globalThis.XMLHttpRequest, WebSocket: globalThis.WebSocket };", "", ""},
	{"·/test/unit/spawnlog.test.ts", "", "— Gap.** No WebSocket client or server exists in the tree; the only `websocket` strings are test fences (`mobile/src/…", "expect(writer).not.toMatch(/fetch\\(|https?:\\/\\/|new URL\\(|WebSocket|net\\.|request\\(/)", "", ""},
	{"internal/rtdb/stream.go", "internal/rtdb/stream.go", "The RTDB client's realtime transport is outbound SSE (`internal/rtdb/stream.go`'s `Stream` and `Event`), and the cons…", "func (c *Client) Stream(ctx context.Context, path string) <-chan Event {", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "— Exists.** `internal/infra/tf/managed_cost.go`'s tables read `google_cloud_run_v2_service` with its `template.0.scal…", "path:  []any{\"template\", 0, \"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"deploy/terraform/gcp/modules/compute/variables.tf", "deploy/terraform/gcp/modules/compute/variables.tf", "— Rule.** `deploy/terraform/gcp/modules/compute/variables.tf`'s `launchers` / `operators` validations require `user:`…", "variable \"launchers\" {", "", ""},
	{"internal/cli/doctor_launcher.go", "internal/cli/doctor_launcher.go", "— Rule.** `deploy/terraform/gcp/modules/compute/variables.tf`'s `launchers` / `operators` validations require `user:`…", "func publicCanStart(p *runapi.GoogleIamV1Policy, project string) bool {", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "`internal/cli/init_members.go`'s `defaultMembers` derives members from `authenticatedUser`, which requires a **verifi…", "func (e *initEngine) defaultMembers(ctx context.Context) error {", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "— Wrong.** `internal/runstore/launch.go`'s `Claim` (`Holder`, `At`) is the launch claim: it exists to make *launching…", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "The branch lock (`internal/lock/lock.go` `Holder{RunID, Execution, ExpiresAt}`) is per branch, not per job-shepherd.", "type Holder struct {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "The budget's \"lease\" (`internal/budget/lease.go` `leaseSource.Grant`) is an amount of money.", "func (l *leaseSource) Grant(ctx context.Context, need Micros) (Micros, error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "— Exists.** `internal/cli/run.go`'s `takeClaim` reads a held claim **once**, judges staleness on that content, and ta…", "func takeClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string, now time.Time) (won bool, other runstore.Claim, err error) {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "`internal/lock/lock.go`'s `Takeover` / `ErrHolderChanged` does the same for the branch lock and adds the one signal a…", "var beforeTakeoverDelete func()", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "— Rule.** `internal/rtdb/client.go`'s `GetETag` (an `X-Firebase-ETag` read) and `PutIfMatch` (`If-Match`, returning `…", "func (c *Client) GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error) {", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "— Exists / Open.** `internal/rtdb/client.go`'s `ServerNow` is *\"the database server's current time: the Date header o…", "func (c *Client) ServerNow() (time.Time, bool) {", "", ""},
	{"internal/budget/session.go", "internal/budget/session.go", "Its users are the budget day and the watch view (`internal/budget/session.go` `Session.now` — *\"now is the database's…", "func (s *Session) now() time.Time {", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "Its users are the budget day and the watch view (`internal/budget/session.go` `Session.now` — *\"now is the database's…", "func (s *Supervisor) now() time.Time {", "", ""},
	{"internal/blobx/blobx.go", "internal/blobx/blobx.go", "— Gap.** The only fencing primitive is the object generation: `internal/blobx/blobx.go`'s `Create`, `ReplaceIf` (*\"ov…", "func (b *Bucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "— Gap.** The only fencing primitive is the object generation: `internal/blobx/blobx.go`'s `Create`, `ReplaceIf` (*\"ov…", "type RecordVersion struct {", "", ""},
	{"result.json", "result.json", "— Gap.** The only fencing primitive is the object generation: `internal/blobx/blobx.go`'s `Create`, `ReplaceIf` (*\"ov…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "— Exists.** `internal/budget/registry.go`'s `Session.heartbeat` patches the registry entry; `internal/budget/sweep.go…", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "— Exists.** `internal/budget/registry.go`'s `Session.heartbeat` patches the registry entry; `internal/budget/sweep.go…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"shared/lease/index.js", "shared/lease/index.js", "— Exists.** `runstore.ClaimTTL` is 10 minutes; the mailbox's consumer lease in the mobile design is 10 minutes with a…", "//   Store.Claim(ctx, holder, at)             -> takeLease(state, { holder, at })", "", ""},
	{"internal/cli/run_attach.go", "internal/cli/run_attach.go", "— Gap.** `internal/cli/run_attach.go`'s `attachLaunch`, `attachInputs`, `attachJudge` attach **files** (`--attach`), …", "func attachLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, prefix string, paths []string, kind string, clone bool, warn io.Writer) (config.Inputs, error) {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "— Gap.** `internal/cli/run_attach.go`'s `attachLaunch`, `attachInputs`, `attachJudge` attach **files** (`--attach`), …", "func existingLaunch(ctx context.Context, env *cloudEnv, s *runstore.Store, spec *task.Spec) (*runstore.Launch, error) {", "", ""},
	{"launch.json", "launch.json", "— Gap.** `internal/cli/run_attach.go`'s `attachLaunch`, `attachInputs`, `attachJudge` attach **files** (`--attach`), …", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "— Exists.** `internal/agent/agent.go`'s `Request.SessionID` / `Resume` (the CLI passes `--resume <id>`), `internal/ru…", "func NewSessionID() string {", "", ""},
	{"internal/runner/session.go", "internal/runner/session.go", "— Exists.** `internal/agent/agent.go`'s `Request.SessionID` / `Resume` (the CLI passes `--resume <id>`), `internal/ru…", "func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored {", "", ""},
	{"internal/followup/prompt.go", "internal/followup/prompt.go", "— Exists.** `internal/agent/agent.go`'s `Request.SessionID` / `Resume` (the CLI passes `--resume <id>`), `internal/ru…", "Resumed        bool     // the previous run's session was resumed", "", ""},
	{"internal/runstore/session.go", "internal/runstore/session.go", "— Exists.** `internal/agent/agent.go`'s `Request.SessionID` / `Resume` (the CLI passes `--resume <id>`), `internal/ru…", "func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {", "", ""},
	{"internal/runview/runview.go", "internal/runview/runview.go", "— Gap.** `internal/runview/runview.go`'s `Row.LaunchedBy` is what `fugaro ls` and the console show, and `internal/cli…", "// LaunchedBy is who started the run, as the run's own launch.json", "", ""},
	{"internal/cli/launch_budget.go", "internal/cli/launch_budget.go", "— Gap.** `internal/runview/runview.go`'s `Row.LaunchedBy` is what `fugaro ls` and the console show, and `internal/cli…", "func (e *cloudEnv) mintBudgetToken(ctx context.Context, slug string, spec *task.Spec, job *launchProjectJob) (key []byte, err error) {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "— Rule.** `internal/lock/lock.go`'s `Stale` needs more than a timer: `holder.Execution != \"\"` **and** `executionTermi…", "func Stale(holder Holder, executionTerminal bool) bool {", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "— Rule.** `internal/lock/lock.go`'s `Stale` needs more than a timer: `holder.Execution != \"\"` **and** `executionTermi…", "var errOwnerUnknown = errors.New(\"could not tell whether another execution owns this run\")", "", ""},
	{"internal/budget/registry.go", "internal/budget/registry.go", "A lease has no such oracle: the only signal is that heartbeats stopped (`internal/budget/registry.go`'s `Session.hear…", "func (s *Session) heartbeat(ctx context.Context) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "A lease has no such oracle: the only signal is that heartbeats stopped (`internal/budget/registry.go`'s `Session.hear…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"result.json", "result.json", "— Exists.** `result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` / `Record`, g…", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "— Exists.** `result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` / `Record`, g…", "func (s *Store) CreateRecord(ctx context.Context, r *Record) error {", "", ""},
	{"internal/runstore/replace.go", "internal/runstore/replace.go", "— Exists.** `result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` / `Record`, g…", "func ReplaceRecordIf(ctx context.Context, b *blobx.Bucket, slug, runID string, rec *Record, v RecordVersion) error {", "", ""},
	{"internal/backend/codec.go", "internal/backend/codec.go", "— Exists.** `result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` / `Record`, g…", "func SameExecution(a, b string) bool {", "", ""},
	{"internal/runner/runner.go", "internal/runner/runner.go", "— Exists.** `result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` / `Record`, g…", "func (r *run) claimRecord(ctx context.Context) error {", "", ""},
	{"internal/runstore/runstore.go", "internal/runstore/runstore.go", "The one lever from outside is the cancel marker — `internal/runstore/runstore.go`'s `RequestCancel` / `CancelRequeste…", "func (s *Store) RequestCancel(ctx context.Context) error {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "— Wrong.** `internal/budget/model.go`'s `Caps` reads `/config`: `GlobalCaps`, `DefaultCaps`, `RepoCaps` and `Limits`,…", "func PathCapsRepo(slug string) string { return \"config/caps/repos/\" + seg(\"slug\", slug) }", "", ""},
	{"internal/budget/evaluate.go", "internal/budget/evaluate.go", "`internal/budget/evaluate.go`'s `Evaluate` is *\"the pure mirror of the database rules\"* and `internal/budget/precheck…", "func Evaluate(st State, w Write, caps Caps, kills Kills, now time.Time) Decision {", "", ""},
	{"internal/budget/precheck.go", "internal/budget/precheck.go", "`internal/budget/evaluate.go`'s `Evaluate` is *\"the pure mirror of the database rules\"* and `internal/budget/precheck…", "type PrecheckInput struct {", "", ""},
	{"internal/budget/evaluate.go", "internal/budget/evaluate.go", "The one per-identity cap is the local file's committed `per_day_usd` (`internal/budget/evaluate.go` `Write.PolicyCap`…", "type Write struct {", "", ""},
	{"internal/budget/token/token.go", "internal/budget/token/token.go", "— Exists.** `internal/budget/token/token.go`'s `Claims`, `UID(slug, run)` = `r~<slug>~<run>`, `ErrRevoked` (*\"identit…", "type Claims struct {", "", ""},
	{"internal/budget/token/mint.go", "internal/budget/token/mint.go", "— Exists.** `internal/budget/token/token.go`'s `Claims`, `UID(slug, run)` = `r~<slug>~<run>`, `ErrRevoked` (*\"identit…", "func Mint(ctx context.Context, signer Signer, c Claims) (string, error) {", "", ""},
	{"internal/budget/token/seal.go", "internal/budget/token/seal.go", "— Exists.** `internal/budget/token/token.go`'s `Claims`, `UID(slug, run)` = `r~<slug>~<run>`, `ErrRevoked` (*\"identit…", "func Sealed(data []byte) bool { return len(data) >= minEnvelope && data[0] == EnvelopeVersion }", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "— Exists.** `internal/budget/token/token.go`'s `Claims`, `UID(slug, run)` = `r~<slug>~<run>`, `ErrRevoked` (*\"identit…", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"SECURITY.md", "SECURITY.md", "— Rule.** `SECURITY.md` states what a launcher can reach, that a run is an untrusted agent, that *\"/proc/<pid>/enviro…", "/proc/<pid>/environ", "", ""},
	{"docs/design/budget-token-channel.md", "docs/design/budget-token-channel.md", "`docs/design/budget-token-channel.md` is the design.", "# The budget token's channel", "", ""},
	{"README.md", "docs/credentials-service/README.md", "— Gap.** `docs/credentials-service/` is plan-only (`README.md`), its D134 gives a run the *existing* budget identity,…", "**Status: plan only; nothing is implemented; decisions answered", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "`docs/design/project-centric.md` §7's \"OAuth tokens (phase 5)\" is the one place a **reference** is described — *\"A re…", "OAuth tokens (phase 5)", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "`docs/design/project-centric.md` §7's \"OAuth tokens (phase 5)\" is the one place a **reference** is described — *\"A re…", "a stopgap", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "— Wrong.** `docs/design/v1.md` §2, non-goals: *\"Auto-merge.", "Auto-merge. Fugaro never merges.", "", ""},
	{"internal/gitprov/gitprov.go", "internal/gitprov/gitprov.go", "Fugaro never merges.\"*; the provider interface `internal/gitprov/gitprov.go`'s `Provider` exposes `EnsurePR`, `ApplyR…", "type Provider interface {", "", ""},
	{"internal/cli/root.go", "internal/cli/root.go", "Fugaro never merges.\"*; the provider interface `internal/gitprov/gitprov.go`'s `Provider` exposes `EnsurePR`, `ApplyR…", "root.AddCommand(", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "— Exists (schema) / Gap.** `shared/confirm/index.js`'s `initialState` / `step` / `readBack` and `DESTRUCTIVE_VERBS`, …", "* initialState builds the empty machine.", "", ""},
	{"internal/budget/rules/rules.json.tmpl", "internal/budget/rules/rules.json.tmpl", "— Exists (schema) / Gap.** `shared/confirm/index.js`'s `initialState` / `step` / `readBack` and `DESTRUCTIVE_VERBS`, …", "\"head_sha\": { \".validate\": \"(newData.isString() && newData.val().length > 0) && SHAOK\" },", "", "the template writes the mailbox subtree twice -- under the inbox and under the outbox -- and the action node's head_sha validation is byte-identical in both"},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "— Exists (schema) / Gap.** `shared/confirm/index.js`'s `initialState` / `step` / `readBack` and `DESTRUCTIVE_VERBS`, …", "type Action struct {", "", ""},
	{"internal/cli/project_cmd.go", "internal/cli/project_cmd.go", "The consumer is plan task P6.7 and `fugaro pair` is P6.6, both unbuilt (`internal/cli/project_cmd.go`: *\"'fugaro pair…", "'fugaro pair' is plan task P6.6 and is not built", "", ""},
	{"shared/mailbox-schema/mac.js", "shared/mailbox-schema/mac.js", "The consumer is plan task P6.7 and `fugaro pair` is P6.6, both unbuilt (`internal/cli/project_cmd.go`: *\"'fugaro pair…", "* macOf is the tag a record carries: HMAC-SHA-256 over canonicalBytes(record),", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "— Rule.** `internal/budget/rules/rules.go`'s `userClaim` requires `auth.token.mu == auth.uid` **and** `root.child('co…", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/followup/trust.go", "internal/followup/trust.go", "— Rule.** `internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up — *\"an author liste…", "type Trust struct {", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "— Rule.** `internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up — *\"an author liste…", "// Trusted are the account IDs whose PR comments a follow-up acts on:", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "— Rule.** `internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up — *\"an author liste…", "// Labels and Reviewers are applied only when a pull request is made ready:", "", ""},
	{"internal/gitprov/github/github.go", "internal/gitprov/github/github.go", "— Rule.** `internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up — *\"an author liste…", "func (p *Provider) ApplyReady(ctx context.Context, number int, reviewers, labels []string) error {", "", ""},
	{"internal/config/config.go", "internal/config/config.go", "— Rule.** `internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up — *\"an author liste…", "return claimed(providers, a.ModelFor(RoleCoder)) && !claimed(providers, a.ModelFor(RoleReviewer))", "", ""},
	{"SECURITY.md", "SECURITY.md", "— Exists.** `SECURITY.md`'s paragraph on `mode: review` (`review-only`, `pick`): trusted-author-only, by the author's…", "**`mode: review` (`review-only`, `pick`) is trusted-author-only**", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "— Exists / Open.** `docs/mobile/functional-spec-v0.1.md` §4.1 *\"Adding a project: QR pairing\"* carries *\"Only someone…", "Adding a project: QR pairing", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "— Exists / Open.** `docs/mobile/functional-spec-v0.1.md` §4.1 *\"Adding a project: QR pairing\"* carries *\"Only someone…", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"mobile/package.json", "mobile/package.json", "— Gap.** `mobile/package.json`'s dependencies contain no Firebase SDK and no networking library; `mobile/src/feed/ind…", "\"dependencies\": {", "", ""},
	{"mobile/src/feed/index.js", "mobile/src/feed/index.js", "— Gap.** `mobile/package.json`'s dependencies contain no Firebase SDK and no networking library; `mobile/src/feed/ind…", "//   * **A port.** There is no database client in this directory, and there must", "", ""},
	{"mobile/src/feed/reader.js", "mobile/src/feed/reader.js", "There is no database client in this directory, and there must not be\"*; `mobile/src/feed/reader.js`'s `FeedReader` ta…", "export class FeedReader {", "", ""},
	{"mobile/src/feed/paths.js", "mobile/src/feed/paths.js", "There is no database client in this directory, and there must not be\"*; `mobile/src/feed/reader.js`'s `FeedReader` ta…", "export const FAMILIES = Object.freeze(['config/caps', 'config/kill', 'spend', 'mailbox']);", "", ""},
	{"mobile/src/mailbox/client.js", "mobile/src/mailbox/client.js", "There is no database client in this directory, and there must not be\"*; `mobile/src/feed/reader.js`'s `FeedReader` ta…", "const error = new TypeError('createMailboxClient: the account tuple is not usable');", "", ""},
	{"mobile/src/demo/safety.test.js", "mobile/src/demo/safety.test.js", "There is no database client in this directory, and there must not be\"*; `mobile/src/feed/reader.js`'s `FeedReader` ta…", "const RULES = [", "", ""},
	{"·/src/main/protocol.ts", "", "— Rule.** the console renderer's CSP module's `CONTENT_SECURITY_POLICY` carries `connect-src 'none'` — *\"the renderer ne…", "* third directive of `CONTENT_SECURITY_POLICY` below, so a `<link", "", ""},
	{"·/src/main/ipc.ts", "", "— Rule.** the console renderer's CSP module's `CONTENT_SECURITY_POLICY` carries `connect-src 'none'` — *\"the renderer ne…", "const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)", "", ""},
	{"·/src/preload/index.ts", "", "— Rule.** the console renderer's CSP module's `CONTENT_SECURITY_POLICY` carries `connect-src 'none'` — *\"the renderer ne…", "const names: readonly ExposedName[] = Object.freeze(EXPOSED_OPERATIONS.map(([name]) => name))", "", ""},
	{"·/src/main/spawn.ts", "", "— Rule.** the console renderer's CSP module's `CONTENT_SECURITY_POLICY` carries `connect-src 'none'` — *\"the renderer ne…", "const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === operation)", "", ""},
	{"docs/design/desktop-console.md", "docs/design/desktop-console.md", "`docs/design/desktop-console.md` §5.1 rule 3 and rule 6 are the design's statement of both.", "### 5.1 The hard rules", "", ""},
	{"·/docs/json-contract.md", "", "*Consequence:* the console can reach a WebSocket only by a deliberate change to a load-bearing property — or through …", "line-delimited JSON — one of exactly two line-delimited documents the console", "", ""},
	{"internal/cli/watch.go", "internal/cli/watch.go", "— Exists.** `internal/cli/watch.go`'s `newWatchCmd` / `runWatch` / `watchStream` stream four RTDB paths through `inte…", "func newWatchCmd() *cobra.Command {", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "— Exists.** `internal/cli/watch.go`'s `newWatchCmd` / `runWatch` / `watchStream` stream four RTDB paths through `inte…", "type Supervisor struct {", "", ""},
	{"internal/cli/watch_queued.go", "internal/cli/watch_queued.go", "— Exists.** `internal/cli/watch.go`'s `newWatchCmd` / `runWatch` / `watchStream` stream four RTDB paths through `inte…", "func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string, index *projectJobIndex) *queuedSource {", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "— Wrong.** `internal/cli/init_firebase.go`'s `refusePublicSignUp` — the CLI's own refusal, reading the configuration …", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/infra/identityplatform.go", "internal/infra/identityplatform.go", "— Wrong.** `internal/cli/init_firebase.go`'s `refusePublicSignUp` — the CLI's own refusal, reading the configuration …", "func PublicSignUp(raw []byte) ([]string, error) {", "", ""},
	{"deploy/terraform/gcp/modules/firebase/firebase.tf", "deploy/terraform/gcp/modules/firebase/firebase.tf", "— Wrong.** `internal/cli/init_firebase.go`'s `refusePublicSignUp` — the CLI's own refusal, reading the configuration …", "# The web API key a job signs in with: signInWithCustomToken and the refresh", "", ""},
	{"docs/mobile/functional-spec-reconciliation.md", "docs/mobile/functional-spec-reconciliation.md", "— Wrong.** `internal/cli/init_firebase.go`'s `refusePublicSignUp` — the CLI's own refusal, reading the configuration …", "**G12 — \"Anyone can authenticate with Firebase\" is false for a Fugaro installation. Wrong.**", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "— Exists (partial).** `internal/cli/init_members.go`'s `authenticatedUser` / `userEmail` call `https://openidconnect.…", "var authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "— Gap.** Mobile D121: *\"no push in v1\"*; `docs/mobile/design/push-notifications.md` is *\"design only; nothing is impl…", "design only; nothing is implemented", "", ""},
	{"modules/installation/history.tf", "deploy/terraform/gcp/modules/installation/history.tf", "— Gap.** Mobile D121: *\"no push in v1\"*; `docs/mobile/design/push-notifications.md` is *\"design only; nothing is impl…", "location = google_cloud_run_v2_job.history[0].location", "", ""},
	{"docs/credentials-service/reconciliation.md", "docs/credentials-service/reconciliation.md", "`docs/credentials-service/reconciliation.md`'s D141 defers alerts *\"once a notification channel and phase 5's owner r…", "| **D141** | **`broken` and alerts** (F19)", "", ""},
	{"internal/agent/agent.go", "internal/agent/agent.go", "— Exists / Gap.** `internal/agent/agent.go`'s `Args` emits `-p --output-format stream-json --verbose --dangerously-sk…", "func Args(req Request) []string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "— Exists / Gap.** `internal/agent/agent.go`'s `Args` emits `-p --output-format stream-json --verbose --dangerously-sk…", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "Nobody will answer questions\"*; `internal/agent/settings.go`'s `WriteManagedSettings` adds only a `permissions.deny` …", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "— Rule.** `internal/mailbox/message.go`'s `Message`, `Kinds`, `Action`, `Confirmation`, `Heartbeat`, `Agent`, `Valida…", "const KindMessage = \"message\"", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "The working implementation is JavaScript plus RTDB rules: `shared/mailbox/`, `shared/mailbox-schema/`, `shared/prompt…", "case \"MACOK\":", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "`docs/mobile/implementation-plan.md`'s P6.7 owns the Go verifier and executor.", "### Task P6.7: The consumer's enforcement of the D128 amendment — SECURITY-SENSITIVE", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "— Exists (doc).** `docs/mobile/functional-spec-v0.1.md` §3.2 *\"Voice prompts from agents\"*, \"Queueing policy\": *\"Defa…", "Voice prompts from agents", "", ""},
	{"internal/localcfg/localcfg.go", "internal/localcfg/localcfg.go", "`internal/localcfg/localcfg.go`'s `BurnAlertUSDPerHour` is a **display** threshold passed to `internal/watch/build.go…", "// BurnAlertUSDPerHour is the spend rate above which watch marks a", "", ""},
	{"internal/watch/build.go", "internal/watch/build.go", "`internal/localcfg/localcfg.go`'s `BurnAlertUSDPerHour` is a **display** threshold passed to `internal/watch/build.go…", "// BurnAlertPerHour is watch.burn_alert_usd_per_hour in micro-dollars;", "", ""},
	{"docs/design/desktop-console-v2.md", "docs/design/desktop-console-v2.md", "`internal/localcfg/localcfg.go`'s `BurnAlertUSDPerHour` is a **display** threshold passed to `internal/watch/build.go…", "cap alerts have data and no threshold", "", ""},
	{"internal/cli/followup.go", "internal/cli/followup.go", "— Exists.** `internal/cli/followup.go`'s `resolvePR` / `prSpec` / `storedFollowUp` / `recheckFollowUp`, `internal/fol…", "func resolvePR(ctx context.Context, env *cloudEnv, repo, slug string, pr int, exclude string, warn io.Writer) (*prChain, error) {", "", ""},
	{"internal/followup/trust.go", "internal/followup/trust.go", "— Exists.** `internal/cli/followup.go`'s `resolvePR` / `prSpec` / `storedFollowUp` / `recheckFollowUp`, `internal/fol…", "type Trust struct {", "", ""},
	{"internal/followup/select.go", "internal/followup/select.go", "— Exists.** `internal/cli/followup.go`'s `resolvePR` / `prSpec` / `storedFollowUp` / `recheckFollowUp`, `internal/fol…", "type Selection struct {", "", ""},
	{"internal/followup/prompt.go", "internal/followup/prompt.go", "— Exists.** `internal/cli/followup.go`'s `resolvePR` / `prSpec` / `storedFollowUp` / `recheckFollowUp`, `internal/fol…", "func ImplementPrompt(d PromptData, sel Selection) string {", "", ""},
	{"internal/config/reservedenv_test.go", "internal/config/reservedenv_test.go", "— Gap.** There is no Sentry **integration** anywhere in the tree — the only two Sentry strings are both in tests and …", "for _, env := range []string{\"NPM_TOKEN\", \"SENTRY_DSN\", \"MAPS_API_KEY\", \"PATHWAY_KEY\", \"HOMEBREW_TOKEN\"} {", "", ""},
	{"internal/budget/rules/rules.json.tmpl", "internal/budget/rules/rules.json.tmpl", "The nearest once-only primitives are the rules' create-if-absent outcome (`internal/budget/rules/rules.json.tmpl`, `!…", "\".write\": \"PAIRING && newData.exists() && !data.exists()\",", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "The nearest once-only primitives are the rules' create-if-absent outcome (`internal/budget/rules/rules.json.tmpl`, `!…", "func (s *Sweeper) remove(ctx context.Context, slug, run string, e AgentEntry, now time.Time) error {", "", ""},
	{"internal/budget/token/object.go", "internal/budget/token/object.go", "The nearest once-only primitives are the rules' create-if-absent outcome (`internal/budget/rules/rules.json.tmpl`, `!…", "func PutObject(ctx context.Context, b *blobx.Bucket, slug, run, tok string, key []byte) error {", "", ""},
	{"internal/budget/model.go", "internal/budget/model.go", "— Gap.** `internal/budget/model.go` has `GlobalCaps`, `DefaultCaps`, `RepoCaps` and `Limits` only; no record distingu…", "type GlobalCaps struct {", "", ""},
	{"schemas/fugaro.schema.json", "schemas/fugaro.schema.json", "`schemas/fugaro.schema.json`'s `budget` block has `mode`, `per_run_usd`, `per_day_usd`, `allowed_models` and nothing …", "\"description\": \"A model per stage role; a role left out uses agent.model. With a budget on, each must be an explicit model ID that has a price.\",", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "— Exists.** `internal/budget/kill.go`'s `SetKill` (*\"the one writer of a switch\"*), `checkKills`, `watchKill`, on `bu…", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"SECURITY.md", "SECURITY.md", "— Exists (partial).** `SECURITY.md` says *\"a run is already the untrusted party\"* and, for review mode, that *\"a trus…", "a run is already the untrusted party", "", ""},
	{"internal/safetext/safetext.go", "internal/safetext/safetext.go", "`internal/safetext/safetext.go`'s `OneLine`, `MultiLine`, `Text`, `Field`, `FieldList` sanitise **display**, not a pr…", "func OneLine(s string) string { return Text(s, false) }", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "*Consequence:* *\"clearly marked as data in the agent's context\"* needs a convention the tree does not have, and it mu…", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "— Exists (partial).** `internal/agent/settings.go`'s `WriteManagedSettings` writes a managed settings file whose only…", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "— Wrong.** `docs/design/project-centric.md` §4.1 and `SECURITY.md`'s accepted D4/D13 paragraphs: one job per (profile…", "### 4.1 One job and one job service account per (profile, account), for clone workflows", "", ""},
	{"SECURITY.md", "SECURITY.md", "— Wrong.** `docs/design/project-centric.md` §4.1 and `SECURITY.md`'s accepted D4/D13 paragraphs: one job per (profile…", "- **Runs of one repository can see each other, and the 0.7.0 bucket IAM work does not change that.**", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "`docs/project-claude-accounts.md` §10: *\"every run of an account's job holds that account's token, whatever the run d…", "## 10. The interim carrier, and what changes later", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "— Exists.** `docs/design/v1.md` §2 (#22); the rigor modes work's D13 (*\"Fugaro opens PRs and never deploys\"*) and D11…", "## 2. Goals and non-goals", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "— Rule.** `internal/infra/firestore.go`'s `FirestoreRules` is `allow read, write: if false;` — *\"nothing is readable …", "const FirestoreRules = `rules_version = '2';", "", ""},
	{"internal/firestore/client.go", "internal/firestore/client.go", "— Gap.** `internal/firestore/client.go`'s surface is `Get`, `Patch` (with `MustExist`, `MustNotExist`, `IfUpdateTime`…", "func (c *Client) Get(ctx context.Context, coll, id string) (*Doc, error) {", "", ""},
	{"docs/git-providers.md", "docs/git-providers.md", "— Gap.** Fugaro reaches Git through a job's git credential and the GitHub App (`internal/gitprov/`, `docs/git-provide…", "Fugaro opens pull requests on **Bitbucket Cloud** and **GitHub**.", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "— Gap.** `docs/coordinator/README.md` (*\"reconciliation only; nothing is implemented and no plan is approved\"*), `doc…", "reconciliation only; nothing is implemented and no plan is approved", "", ""},
	{"docs/coordinator/spec.md", "docs/coordinator/spec.md", "— Gap.** `docs/coordinator/README.md` (*\"reconciliation only; nothing is implemented and no plan is approved\"*), `doc…", "- **Advisory, not authoritative.**", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "— Open.** `docs/mobile/design/always-on-orchestrator.md` (PR #632; **not on `main`**) designs *\"One Cloud Run service…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/mobile/reconciliation.md", "docs/mobile/reconciliation.md", "— Rule.** `docs/mobile/reconciliation.md`'s D115 (owner's answer 2026-10-17): *\"decide phase 2 later — after phase 1 …", "| **D115** | **Phase 2 hosting: who holds the model credential?**", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "`docs/mobile/implementation-plan.md`'s P5.1 is **UNSCHEDULED** and **SECURITY-SENSITIVE** *\"because it holds a model …", "### Task P5.1: Phase 2 hosting — the always-on orchestrator — UNSCHEDULED", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "— Rule.** `docs/design/v1.md` §1: a Fugaro **project** and an **installation** are the same deployment unit, as the c…", "## 1. Decisions at a glance", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "— Exists.** `docs/coordinator/README.md` and `docs/coordinator/reconciliation.md` (its #29 and its Related list) cite…", "- `docs/design/always-on-orchestrator.md` — the close sibling: one resident service per", "", ""},
	{"docs/coordinator/reconciliation.md", "docs/coordinator/reconciliation.md", "— Exists.** `docs/coordinator/README.md` and `docs/coordinator/reconciliation.md` (its #29 and its Related list) cite…", "**#29 — The close sibling is the always-on orchestrator (mobile P5.1, PR #632), and it makes two choices that contradict this spec. — Open.**", "", ""},
	{"docs/design/always-on-orchestrator.md", "docs/design/always-on-orchestrator.md", "— Exists.** `docs/coordinator/README.md` and `docs/coordinator/reconciliation.md` (its #29 and its Related list) cite…", "", "this path is the stale citation finding #55 records: the document is not at this path on main (PR #632 places it under docs/mobile/design/), so there is no line here to hold", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "PR #632 places it at `docs/mobile/design/always-on-orchestrator.md`; `docs/design/` on `main` has no such file.", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/windows-cli/README.md", "docs/windows-cli/README.md", "— Exists.** The console's macOS and Linux builds ship for 0.10.0 (`docs/windows-cli/README.md`'s context, and the rel…", "0.10.0) because its data source is the CLI and the CLI has no Windows build,", "", ""},
	{"docs/credentials-service/README.md", "docs/credentials-service/README.md", "— Exists.** The console's macOS and Linux builds ship for 0.10.0 (`docs/windows-cli/README.md`'s context, and the rel…", "the first product slice", "", ""},
	{"fugaro-mobile-app-spec.md", "fugaro-mobile-app-spec.md", "The exact edits the mobile spec needs — listed, and not made The spec's Follow-up bullet asks for `fugaro-mobile-app-…", "", "the file is not a file in this repository and exists nowhere: correction C5 records that the brief's bullet names a file the tree does not have, so there is no line here to hold", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "The file it means is `docs/mobile/functional-spec-v0.1.md`.", "### 4.1 Adding a project: QR pairing", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "**§4.2 \"Authentication and authorisation\", the sentence** *\"Because anyone can authenticate with Firebase, each proje…", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "**§4.2, the sentence** *\"Firebase security rules deny access to anyone not on the allowlist, regardless of successful…", "const userClaim = \"(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())\"", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "The alternative is the runs bucket next to `locks/` and `launching` (`internal/lock/lock.go`, `internal/runstore/laun…", "newGen, err := l.b.ReplaceIf(ctx, l.key, body, l.gen, l.body)", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "The alternative is the runs bucket next to `locks/` and `launching` (`internal/lock/lock.go`, `internal/runstore/laun…", "func (s *Store) ClaimKey() string { return s.prefix + \"launching\" }", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "The grace period is `renewed_at + grace` where `renewed_at` is written **by the writer's own clock** and the reader's…", "func (c *Client) ServerNow() (time.Time, bool) {", "", ""},
	{"internal/budget/sweep.go", "internal/budget/sweep.go", "The alternative — reuse the stale-heartbeat sweep and let the *liveness* record decide (`internal/budget/sweep.go` `D…", "// DefaultStaleAfter is how long an entry may go without a heartbeat", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "*Sub-question for the owner:* whether the lease is per **job** (the run) or per **PR** (the spec says *\"two orchestra…", "type Holder struct {", "", ""},
	{"internal/cli/run.go", "internal/cli/run.go", "### D3 — Clock skew, and who may hold a lease **Recommendation: the lease's expiry is judged on the database's clock;…", "func claimHolder() string {", "", ""},
	{"internal/runner/prompts.go", "internal/runner/prompts.go", "Alert content is untrusted **text**: it is passed to the job as an input under a documented data boundary, never as i…", "func SystemPrompt(d PromptData, instructions string) string {", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "Alert content is untrusted **text**: it is passed to the job as an input under a documented data boundary, never as i…", "func WriteManagedSettings(path string, env map[string]string, denyWeb bool) error {", "", ""},
	{"docs/project-claude-accounts.md", "docs/project-claude-accounts.md", "**Cost:** one job per profile per alert class, and the honest statement that the account, not the fix, is the blast r…", "## 10. The interim carrier, and what changes later", "", ""},
	{"docs/design/desktop-console.md", "docs/design/desktop-console.md", "**Alternative:** a second local socket owned by the CLI (`docs/design/desktop-console.md` §3.3's Option C, *\"a local …", "a local read-only API served by the CLI", "", ""},
	{"internal/cli/init_firebase.go", "internal/cli/init_firebase.go", "Then answer three sub-questions explicitly: (a) whether a project may accept Google or Microsoft SSO at all — the tre…", "func refusePublicSignUp(raw []byte, fp string) error {", "", ""},
	{"internal/cli/init_members.go", "internal/cli/init_members.go", "Then answer three sub-questions explicitly: (a) whether a project may accept Google or Microsoft SSO at all — the tre…", "var authenticatedUser = func(ctx context.Context, lc *localcfg.Config) (string, error) {", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "### D7 — The shared session and the action locks **Recommendation: one shared session per central orchestrator, as th…", "type Action struct {", "", ""},
	{"shared/confirm/index.js", "shared/confirm/index.js", "### D7 — The shared session and the action locks **Recommendation: one shared session per central orchestrator, as th…", "const VERB_VALUES = Object.freeze([...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS]);", "", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "Concretely: each action carries `verb`, `target`, and for a merge a `head_sha`, and the executor re-reads the target …", "func actionField(value any) checked[*Action] {", "", ""},
	{"shared/mailbox-schema/mac.js", "shared/mailbox-schema/mac.js", "### D9 — The signed callback contract **Recommendation: HMAC-SHA-256 over a canonical byte string, in the shape the m…", "* macOf is the tag a record carries: HMAC-SHA-256 over canonicalBytes(record),", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "### D10 — De-duplication, rate limit, the automated budget, and the circuit breaker **Recommendation: key an alert jo…", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
	{"internal/infra/firestore.go", "internal/infra/firestore.go", "### D11 — Session memory: what is stored, where, and when it may be deleted **Recommendation: the shared conversation…", "const FirestoreRules = `rules_version = '2';", "", ""},
	{"internal/budget/rollover.go", "internal/budget/rollover.go", "**Alternative:** keep the session in the RTDB, where the rules and the identity already are — cost: the RTDB is a bud…", "type Roller struct {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "**Alternative:** make `add|rm|ls` local-config commands that only shape a Terraform plan — cost: it does not match ho…", "func newBudgetCmd() *cobra.Command {", "", ""},
	{"internal/cli/budget.go", "internal/cli/budget.go", "Why: it is how every other project-level thing works (`internal/cli/budget.go`, `internal/projectpolicy/`), and it is…", "func newBudgetCmd() *cobra.Command {", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "### D13 — Cost and scale-to-zero **Recommendation: one service, `min-instances = 0`, request-based billing, a wake pa…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/coordinator/spec.md", "docs/coordinator/spec.md", "### D14 — How this relates to the coordinator **Recommendation: the orchestrator reports to the coordinator as a **wr…", "- **Advisory, not authoritative.**", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "### D15 — How this relates to the local orchestrator, and who sees a lost lease **Recommendation: the local orchestra…", "type Supervisor struct {", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "### D18 — The merge authority **Recommendation: the central orchestrator does not merge, and its \"action lock\" set dr…", "## 2. Goals and non-goals", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "### D19 — Per installation or per project, and one service per orchestrator **Recommendation: follow the tree's unit …", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "### D19 — Per installation or per project, and one service per orchestrator **Recommendation: follow the tree's unit …", "var managedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cost.go", "internal/infra/tf/managed_cost.go", "### D19 — Per installation or per project, and one service per orchestrator **Recommendation: follow the tree's unit …", "path:  []any{\"scaling\", 0, \"min_instance_count\"},", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: **one Cloud Run service per insta…", "var allowedTypes = map[string]bool{", "", ""},
	{"internal/infra/tf/managed_cover.go", "internal/infra/tf/managed_cover.go", "**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: **one Cloud Run service per insta…", "var managedTypes = map[string]bool{", "", ""},
	{"docs/mobile/design/push-notifications.md", "docs/mobile/design/push-notifications.md", "### D21 — Notifications **Recommendation: v1 uses only channels that exist or are already designed — a marker in the …", "# Push notifications to the phone — P5.2", "", ""},
	{"docs/mobile/functional-spec-v0.1.md", "docs/mobile/functional-spec-v0.1.md", "### D22 — The mobile spec's edits **Recommendation: a separate, small pull request applies §4's six (+1) edits to `do…", "## 4. Pairing and access", "", ""},
	{"internal/cli/docs_mobile_plan_test.go", "internal/cli/docs_mobile_plan_test.go", "### D22 — The mobile spec's edits **Recommendation: a separate, small pull request applies §4's six (+1) edits to `do…", "func TestDocsMobilePlanPhase6IsApproved(t *testing.T) {", "", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "The register is `docs/coordinator/README.md`'s \"The numbers this work holds\": D1–D192 are taken and **D193 onward is …", "The numbers this work holds", "", ""},
	{"docs/design/voice.md", "docs/design/voice.md", "### D24 — The live-check number **Recommendation: this work claims **no** check number now, and states its owner-run …", "the phase that ships the check writes them", "", ""},
	{"budget/sweep.go", "budget/sweep.go", "| # | Question | Recommendation (first) | Alternative, and its cost | Answer, 2026-10-20 | | --- | --- | --- | --- | …", "", "a run artifact written by the agent's container under runs/<slug>/<run>/, not a file in this repository, so there is no line here for an anchor to be held", ""},
	{"docs/coordinator/README.md", "docs/coordinator/README.md", "**The register, read by name against `main`.** The register is `docs/coordinator/README.md`'s \"The numbers this work …", "The numbers this work holds", "", ""},
	{"README.md", "docs/mobile/voice-session/README.md", "**That is no longer the tree's state.** Reading the tree at `main` now: - `docs/mobile/voice-session/` — merged on `m…", "| **D201–D220** | this work's decisions, all written and **all open**.", "", ""},
	{"reconciliation.md", "docs/mobile/voice-session/reconciliation.md", "**That is no longer the tree's state.** Reading the tree at `main` now: - `docs/mobile/voice-session/` — merged on `m…", "tree. So this work starts at **D201** and leaves D193–D200 alone rather than", "", ""},
	{"docs/design/compute-backend.md", "docs/design/compute-backend.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "# A Docker-capable compute backend: workloads on Compute Engine VMs", "", ""},
	{"docs/mobile/README.md", "docs/mobile/README.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "Checks 70–79 are unused and deliberately not taken", "", ""},
	{"docs/mobile/implementation-plan.md", "docs/mobile/implementation-plan.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "| **6 — APPROVED 2026-10-18 (functional spec v0.1)**", "", ""},
	{"docs/mobile/README.md", "docs/mobile/README.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "**100–104 proposed**", "", ""},
	{"docs/mobile/design/voice-evaluation-switch.md", "docs/mobile/design/voice-evaluation-switch.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "## 7. The register: providers, as read on 2026-10-19", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "The register's claims, read name by name for this document: | Numbers | Whose | State | | --- | --- | --- | | 1–18 | …", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"docs/design/always-on-orchestrator.md", "docs/design/always-on-orchestrator.md", "- **O2 — a stale citation this work would land on top of.** [docs/coordinator/README.md](../coordinator/README.md) an…", "", "this path is the stale citation finding #55 records: the document is not at this path on main (PR #632 places it under docs/mobile/design/), so there is no line here to hold", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "PR #632 places that document at `docs/mobile/design/always-on-orchestrator.md`, and `docs/design/` on `main` has no s…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "Whoever writes that pull request also has to decide whether the spec's Follow-up bullet is corrected in `spec.md` — *…", "- **Follow-up:** update fugaro-mobile-app-spec.md so QR pairing is described as discovery only, with Firebase Auth as the identity layer.", "", ""},
	{"spec.md", "docs/central-orchestrator/spec.md", "Whoever writes that pull request also has to decide whether the spec's Follow-up bullet is corrected in `spec.md` — *…", "Oct 20, 2026 · @Dimi", "", ""},
	{"docs/mobile/design/voice-evaluation-switch.md", "docs/mobile/design/voice-evaluation-switch.md", "- **O4 — Check 110 is claimed twice and written by nobody.** The voice evaluation switch (`docs/mobile/design/voice-e…", "## 7. The register: providers, as read on 2026-10-19", "", ""},
	{"internal/infra/tf/cover.go", "internal/infra/tf/cover.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "var allowedTypes = map[string]bool{", "", ""},
	{"docs/credentials-service/reconciliation.md", "docs/credentials-service/reconciliation.md", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "| U5 | Cloud Run service limits: request timeout", "", ""},
	{"internal/rtdb/client.go", "internal/rtdb/client.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "func (c *Client) PutIfMatch(ctx context.Context, path, etag string, v any) error {", "", ""},
	{"internal/budget/rules/rules.go", "internal/budget/rules/rules.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "t, err := template.New(\"rules\").Option(\"missingkey=error\").Parse(source)", "", ""},
	{"internal/projectpolicy/version.go", "internal/projectpolicy/version.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "const PolicyRulesVersion = 1", "", ""},
	{"·/test/unit/window.test.ts", "", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "\"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'\"", "", ""},
	{"internal/agent/settings.go", "internal/agent/settings.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "var deniedWebTools = []string{\"WebSearch\", \"WebFetch\"}", "", ""},
	{"docs/design/project-centric.md", "docs/design/project-centric.md", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "### 4.1 One job and one job service account per (profile, account), for clone workflows", "", ""},
	{"internal/watch/source.go", "internal/watch/source.go", "UNVERIFIED | # | What is unverified | Why | What would settle it | | --- | --- | --- | --- | | U1 | That a Cloud Run …", "type Supervisor struct {", "", ""},
	{"docs/design/v1.md", "docs/design/v1.md", "Glossary - **Installation / project.** The same deployment unit: one GCP project with one runs bucket, one Firebase p…", "## 1. Decisions at a glance", "", ""},
	{"internal/runstore/launch.go", "internal/runstore/launch.go", "Not the launch claim (`internal/runstore/launch.go` `Claim`), not the branch lock (`internal/lock/lock.go` `Holder`),…", "type Claim struct {", "", ""},
	{"internal/lock/lock.go", "internal/lock/lock.go", "Not the launch claim (`internal/runstore/launch.go` `Claim`), not the branch lock (`internal/lock/lock.go` `Holder`),…", "type Holder struct {", "", ""},
	{"internal/budget/lease.go", "internal/budget/lease.go", "Not the launch claim (`internal/runstore/launch.go` `Claim`), not the branch lock (`internal/lock/lock.go` `Holder`),…", "type leaseSource struct{ s *Session }", "", ""},
	{"docs/mobile/design/always-on-orchestrator.md", "docs/mobile/design/always-on-orchestrator.md", "- **Resident service.** The always-on cloud process: in PR #632's design the consumer plus the launcher, holding no m…", "", "the design lives only in open PR #632 and is not on main, so there is no line here for an anchor to be held to", ""},
	{"internal/mailbox/message.go", "internal/mailbox/message.go", "- **Prompt.** A question, permission or decision an agent raises, tied to a job; the mailbox's `prompt`/`answer` kind…", "// Kinds, Actions and Directions are the module's closed vocabularies, in the", "", ""},
	{"internal/budget/kill.go", "internal/budget/kill.go", "- **Breaker.** The stored state that stops automated launches after a rate-limit or budget event, cleared by a human …", "func SetKill(ctx context.Context, db KillDB, path string, on bool, by, reason string, h KillHooks) (SetResult, error) {", "", ""},
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
			if _, err := os.Stat("../../" + filepath.FromSlash(a.doc)); err == nil {
				t.Errorf("%s is excused as %q, and the file is now in this tree: the excuse has expired, so the citation needs an anchor of the content it means", a.doc, a.excused)
			}
			continue
		}
		at, ok := centralOrchLineOf(t, a.doc, a.anchor, "the citation of "+a.token, a.repeated)
		if ok {
			t.Logf("%s: %s is at line %d of %s", a.token, a.claim, at, a.doc)
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
	// own directory, the way the reader's browser resolves it.
	for _, token := range centralOrchMarkdownLinkRE.FindAllString(doc, -1) {
		target := strings.TrimSuffix(strings.TrimPrefix(token, "]("), ")")
		if _, err := os.Stat(filepath.Join("../../docs/central-orchestrator", filepath.FromSlash(target))); err != nil {
			t.Errorf("%s links to %s and no such file or directory is beside it in this tree: a markdown link is a citation too, and it is followed by a reader the same way, so a link that 404s is a citation no reader can check", centralOrchReconciled, target)
		}
	}
	t.Logf("%s: %d citations claimed by the matcher over %d path-looking tokens, %d markdown links",
		centralOrchReconciled, len(matches), total, len(centralOrchMarkdownLinkRE.FindAllString(doc, -1)))
}

// centralOrchCitationsAbsent are the citations that name something this tree does not have,
// each with the reason the document itself gives. Nothing else may fail to resolve.
var centralOrchCitationsAbsent = map[string]string{
	"docs/mobile/design/always-on-orchestrator.md": "PR #632's always-on orchestrator design, which is not on main",
	"docs/design/always-on-orchestrator.md":        "the stale path finding #55 records; PR #632 places the document under docs/mobile/design/",
	"docs/rigor-modes/spec.md":                     "PR #773's rigor-mode documents, which are not on main",
	"fugaro-mobile-app-spec.md":                    "the file correction C5 records as not existing anywhere",
	"result.json":                                  "a run artifact, written under runs/<slug>/<run>/",
	"launch.json":                                  "a run artifact, written under runs/<slug>/<run>/",
	"task.json":                                    "a run artifact, written under runs/<slug>/<run>/",
	"comments.json":                                "a run artifact, written under runs/<slug>/<run>/",
	"session/session.json":                         "a run artifact, written under runs/<slug>/<run>/",
	"README.md":                                    "cited bare, and every README.md this document means is named by an anchor entry",
	"spec.md":                                      "cited bare, and every spec.md this document means is named by an anchor entry",
	"reconciliation.md":                            "cited bare, and every reconciliation.md this document means is named by an anchor entry",
	"implementation-plan.md":                       "cited bare, and every implementation-plan.md this document means is named by an anchor entry",
	"budget/sweep.go":                              "the second, shortened citation of internal/budget/sweep.go in the same row",
}

// centralOrchCitationAbsent reports whether a citation names something this tree does not have.
func centralOrchCitationAbsent(path string) bool {
	_, ok := centralOrchCitationsAbsent[path]
	return ok
}
