package cli

// The workspaces design folder, pinned.
//
// docs/workspaces/ is a design-only folder: README.md (the index, the owner's
// answers, the reading of the number block and what a reader should not take
// from here), spec.md (the owner's spec recorded as §0, then the design written
// to his answers), reconciliation.md (the findings F1-F31, the sixteen
// decisions D1290-D1305 and the owner's answers of 2026-11-04) and
// implementation-plan.md (the tasks W0-W9). It adds no code and no task in it is
// approved, so nothing here pins a command, a flag or an option.
//
// What it does pin is the half of these documents that rots silently:
//
//  * TestDocsWorkspacesIsWritten -- the four files exist, each carries the
//    folder's status line word for word, the block the decisions live in is
//    claimed whole (D1290-D1309, sixteen written and four reserved), and the
//    plan still gates every task on the security review the coordinator runs.
//  * TestDocsWorkspacesAnswersAreTheOnesInForce -- the answers of 2026-11-04 are
//    in reconciliation.md §4, every one of them is the recommendation (no row
//    says otherwise), and the two questions this round reported instead of
//    deciding are in §8 rather than left as gaps.
//  * TestDocsWorkspacesNamesWhatItDesigns -- the placeholders and the command
//    family the design fixes are the names the documents use, and the security
//    statements the reviews of 2026-11-04 corrected stay corrected: the grant
//    that can lift a member's cap, the read the record's readers are limited
//    to, the issuer pin that lives in the member's own config, and the
//    person class the workspace itself fills.
//  * TestDocsWorkspacesCitationsResolve -- every `path:line` citation resolves to
//    a file of this tree and a line inside it, measured against an independent
//    census so a regexp that stopped matching fails rather than passing
//    vacuously.
//
// The citation resolver is the cloud senior review's (`csrCitationRE`,
// `csrCensusRE`, `csrResolveCitation`, `csrBareNames`), through the hosted MCP
// server's own pattern (`mcpPlainCitationRE`): four docs tests asking one
// question of one tree should not each carry their own, and a second copy is the
// one that drifts.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	wsReadme     = "docs/workspaces/README.md"
	wsSpec       = "docs/workspaces/spec.md"
	wsReconciled = "docs/workspaces/reconciliation.md"
	wsPlan       = "docs/workspaces/implementation-plan.md"

	// wsStatus is the line every file in this folder carries, word for word.
	// It is the folder's whole claim about itself -- the design is answered,
	// the plan is all there is, nothing is implemented and no task is approved
	// -- and a file that dropped it would read as a design somebody may build
	// from, which is the one thing this folder is not.
	wsStatus = "Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved."
)

func wsDocs() []string { return []string{wsReadme, wsSpec, wsReconciled, wsPlan} }

// TestDocsWorkspacesIsWritten: the four files are there, each carries the status
// line, the number block is claimed as a block, and the plan still says what
// every task waits behind.
func TestDocsWorkspacesIsWritten(t *testing.T) {
	for _, path := range wsDocs() {
		flat := flattenDoc(readDoc(t, path))
		if !strings.Contains(flat, wsStatus) {
			t.Errorf("%s does not carry the folder's status line %q: every file of an answered design folder says in one line that the design is answered, that nothing is implemented and that no task is approved, and a file that drops it reads as a design somebody may build from",
				path, wsStatus)
		}
	}

	// The block, claimed whole and used from the front: a reader who greps
	// `D129x` or `D130x` has to find this folder and nothing else, and the four
	// reserved numbers are what stops a later task reaching into the next block.
	// README.md and reconciliation.md are the two files that read the block and
	// both state it the same way.
	for _, path := range []string{wsReadme, wsReconciled} {
		flat := flattenDoc(readDoc(t, path))
		for _, want := range []string{"D1290–D1309", "D1290–D1305", "D1306–D1309"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s never states %q: the block is D1290-D1309, sixteen decisions written and four reserved, and a file that names the block without its reserved tail is a file a later task reads as having room in it",
					path, want)
			}
		}
	}
	if flat := flattenDoc(readDoc(t, wsPlan)); !strings.Contains(flat, "D1290–D1305") {
		t.Errorf("%s does not name the block's written part D1290-D1305: the plan's decision table is what each task is written to", wsPlan)
	}

	plan := flattenDoc(readDoc(t, wsPlan))
	for _, want := range []string{
		// The gate §1 of the plan states before the first task: the independent
		// SECURITY review of spec.md §8, which the coordinator runs.
		"independent SECURITY review is **mandatory before this design merges**",
		"**the coordinator runs it**",
		"**No task is approved**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not state %q: the plan gates every task on the security review and on the owner approving each task by name, so a later edit that dropped either would leave a task looking startable",
				wsPlan, want)
		}
	}
}

// wsDecisionRE finds a decision number as the documents write it, so a count is
// of that decision and not of a longer number that starts the same way.
var wsDecisionRE = regexp.MustCompile(`\bD(?:129[0-9]|130[0-9])\b`)

// TestDocsWorkspacesAnswersAreTheOnesInForce: the answers of 2026-11-04 are in
// reconciliation.md §4, every one of them is the recommendation, and the two
// questions this round reported instead of deciding are in §8.
//
// §8 is the section the design's own text points at four times -- twice in
// spec.md §5, twice in §10 -- for the two questions a security review raised
// that no task may settle. A reference to a section that does not exist is the
// silent failure this pins: the prose would read as if the trade had been
// recorded somewhere, and the reader would stop looking.
func TestDocsWorkspacesAnswersAreTheOnesInForce(t *testing.T) {
	rec := readDoc(t, wsReconciled)
	if !strings.Contains(rec, "## 4. The decisions") {
		t.Fatalf("%s does not carry the decisions section: the sixteen decisions were answered on 2026-11-04 and the record of that is the section a reader looks for first", wsReconciled)
	}
	if !strings.Contains(rec, "## 8. Owner decisions this round did not take") {
		t.Fatalf("%s does not carry §8: spec.md §5 and §10 point at it four times for the two questions this round reported rather than decided, so a missing section leaves four references pointing at nothing", wsReconciled)
	}

	// Every decision is written in §4 as its own bullet, question and answer
	// together, and again in README.md's answers table -- so a decision whose
	// answer went missing leaves one of the two files short and a reader of the
	// other with no way to tell.
	counted := map[string]int{}
	for _, d := range wsDecisionRE.FindAllString(rec, -1) {
		counted[d]++
	}
	readme := readDoc(t, wsReadme)
	for i := 1290; i <= 1305; i++ {
		d := "D" + strconv.Itoa(i)
		if counted[d] == 0 {
			t.Errorf("%s never names %s: §4 writes each of the sixteen decisions as its own bullet, and a decision missing from it is one the owner answered and the record dropped", wsReconciled, d)
		}
		if !strings.Contains(readme, "**"+d+"**") {
			t.Errorf("%s does not carry %s in its answers table: the table is where a reader finds the answer without reading the question, and a row that went missing is a decision that reads as unanswered", wsReadme, d)
		}
	}
	// And the four reserved numbers carry no decision: a bullet for one of them
	// would be a number this work took without the owner giving it.
	for i := 1306; i <= 1309; i++ {
		if d := "**D" + strconv.Itoa(i) + " —"; strings.Contains(rec, d) {
			t.Errorf("%s writes a decision for %s, which is reserved and unused: the block's written part is D1290-D1305", wsReconciled, d)
		}
	}

	// The answers in force are all the recommendation.
	for _, path := range wsDocs() {
		doc := readDoc(t, path)
		if strings.Contains(doc, "Not the recommendation") {
			t.Errorf("%s still marks an answer as not the recommendation: every answer of 2026-11-04 took it", path)
		}
	}

	// §8 names both questions, and the design's own text points at it rather
	// than deciding either one.
	for _, want := range []string{
		"O1 — Where the advisory headroom lives",
		"O2 — Whether the workspace's cap is metered at the proxy instead of rolled up",
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("%s does not state %q: the headroom's location decides which data each side sees and the cap's basis is D1301's answer, so neither is a task's to settle and both have to be recorded where the design points",
				wsReconciled, want)
		}
	}
	spec := readDoc(t, wsSpec)
	if n := strings.Count(spec, "(reconciliation.md) §8"); n < 4 {
		t.Errorf("%s points at reconciliation.md §8 %d times and the design makes that reference four times (twice in §5, twice in §10): a reference that went missing is a question the design reads as having decided", wsSpec, n)
	}
}

// TestDocsWorkspacesNamesWhatItDesigns: the names the design fixes are the names
// the documents use, and the security statements the review corrected are the
// corrected ones.
//
// The corrected-statement blocks are the ones this test exists for, and there
// have been two review rounds of them. The first found the design claiming the
// workspace's database grant could not lift a member's cap -- the opposite of
// what the code says -- and claiming the sign-in half's dependency was an open
// pull request after it had merged. The second found three statements the
// folder's own sections contradicted: that a member reads the workspace's
// record, that the issuer pin existed anywhere but in a plan's prose, and that
// the member-side grants were applied by somebody. The third took a fourth: the
// person class, which the folder had derived from the members' own lists -- a
// channel no grant supports, since a member's launcher/operator list is that
// member's own file and the workspace's service reads no member -- so the class
// is the workspace owner's and no member's person is admitted on any route.
// Every one of them is a statement a reader acts on, and every one had a
// plausible-sounding wrong version in the folder.
func TestDocsWorkspacesNamesWhatItDesigns(t *testing.T) {
	// The placeholders: a real organisation's example was not recovered, and a
	// design that reused `workspace-a` would be read as being about the
	// Bitbucket workspace name and the settings-commands project placeholder.
	// The reconciliation names the *projects* and not the workspace, which is
	// why it is not in this list.
	for _, path := range []string{wsReadme, wsSpec, wsPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "workspace-alpha") {
			t.Errorf("%s never names `workspace-alpha`: it is this folder's workspace placeholder, and the string it replaced means two other things in this repository", path)
		}
	}
	for _, path := range []string{wsReadme, wsSpec, wsPlan} {
		flat := flattenDoc(readDoc(t, path))
		for _, want := range []string{"owner/repo-a", "owner/repo-b"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s does not name %q: repository names are placeholders here, and a document that dropped one is a document that named a real repository", path, want)
			}
		}
	}

	// The command family and the wire names, which the CLI change and the
	// schemas have to agree with.
	for _, want := range []string{"fugaro workspace", "fugaro budget kill", "workspaces/<name>"} {
		if flat := flattenDoc(readDoc(t, wsSpec)); !strings.Contains(flat, want) {
			t.Errorf("%s does not name %q: it is a name this design fixes and a task builds against", wsSpec, want)
		}
	}

	// The corrected security statements, in the words the correction uses.
	spec := flattenDoc(readDoc(t, wsSpec))
	for _, want := range []string{
		"**the workspace service's grant can lift a member's cap**",
		"which IAM can't enforce",
		"IAM bypasses them",
		"**landed on `main` on 2026-11-04**",
		`It is not true that "the only thing that changes is the admitted set"`,
		"**A `stale` member fails closed.**",
		// The three the security re-read of 2026-11-04 added, each of which a
		// reader acts on and each of which the folder had a plausible wrong
		// version of.
		"`email_verified` is required to be true",
		"**the issuer the member's own config pins**",
		"**It is read by the workspace's own service and by the workspace owner's CLI, and by nothing else.**",
		"admits **no member's person**",
		// Whose the class is. The folder's first two rounds read it as the
		// members' people -- derived from their own lists, then admitted route by
		// route -- and no grant gives the workspace's service a way to learn a
		// member's people, so a reader who finds the derived version acts on an
		// admission the design cannot make.
		"it is the **workspace owner's** people",
		"**the workspace's own figures are the workspace owner's to read**",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s does not state %q: the review of 2026-11-04 corrected this, and the wrong version is the one a reader would otherwise act on", wsSpec, want)
		}
	}

	// The plan's own half: the two-sided membership and the tasks that own the
	// pin and the member's grants. A pin that lives only in the spec is a pin no
	// task builds, and a grant no task applies is a grant that was never made.
	plan := flattenDoc(readDoc(t, wsPlan))
	for _, want := range []string{
		"**Membership takes both sides.**",
		"TestExchangeVerifiesTheMembersPinnedIssuerNotTheRecords",
		"TestMemberUpdateBindsTheWorkspaceGrantsToThePinnedAccount",
		"TestWorkspaceServiceRefusesAMembersPersonTheWorkspaceWideRead",
		// W6 is the task that owns the class, so the pin is stated there too and
		// not only in the spec: a refusal no task states is a refusal no test holds.
		"it is the **workspace owner's** people",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not carry %q: the pin and the member-side grants are design statements until a task owns them, and this is where a task is named", wsPlan, want)
		}
	}

	// The claim the review refused, which must not come back in any file.
	for _, path := range wsDocs() {
		flat := flattenDoc(readDoc(t, path))
		for _, banned := range []string{
			"The one thing that changes is that the admitted set",
			// A member reading the workspace's own record: the section that
			// said it and the sections that denied it cannot both stand, and
			// the denial is the one the grants support.
			"member reads when it needs to know who else is in the group",
			"member's exchange reads for the member list",
		} {
			if strings.Contains(flat, banned) {
				t.Errorf("%s still claims %q: the folder's own sections say a member holds no grant on the workspace's project and reads no node of its record", path, banned)
			}
		}
	}
}

// TestDocsWorkspacesCitationsResolve: every file and line these documents cite
// exists. A reconciliation is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom -- which is what happened to this folder's `credentials.tf`
// citations, five of them one or more lines off.
func TestDocsWorkspacesCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range wsDocs() {
		doc := readDoc(t, path)
		// The census first, and independently: how many path-looking tokens
		// carrying a line the document holds at all. The check below has to see
		// at least that many, so a regexp that stopped matching a shape the
		// prose uses fails here rather than quietly checking less.
		census := len(csrCensusRE.FindAllString(doc, -1))
		matches := csrCitationRE.FindAllStringSubmatch(doc, -1)
		if len(matches) < census {
			t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape csrCitationRE no longer knows, so those citations are unchecked",
				path, census, len(matches))
		}
		for _, m := range matches {
			wsCheckCitation(t, path, m[1], m[2], bare)
		}
		plain := mcpPlainCitationRE.FindAllString(doc, -1)
		for _, tok := range plain {
			cut := strings.LastIndex(tok, ":")
			wsCheckCitation(t, path, strings.TrimSuffix(tok[:cut], "`"), tok[cut:], bare)
		}
		if len(matches)+len(plain) == 0 {
			t.Errorf("%s cites no file and line at all: every document in this folder stands on the code it was reconciled against, so a file the check finds no citation in is a file the check has gone blind on", path)
		}
		t.Logf("%s: %d backticked citations carrying a line, against a census of %d path-looking tokens with one, and %d written without backticks",
			path, len(matches), census, len(plain))
	}
}

// wsCheckCitation resolves one cited file and line to a line of this tree.
func wsCheckCitation(t *testing.T, doc, cited, lineRef string, bare map[string][]string) {
	t.Helper()
	file, ok := csrResolveCitation(cited, bare)
	if !ok {
		t.Errorf("%s cites %q, which is not a file in this tree: a reconciliation whose citations have moved is a reconciliation nobody can check", doc, cited)
		return
	}
	line, err := strconv.Atoi(strings.TrimPrefix(lineRef, ":"))
	if err != nil {
		t.Errorf("%s cites %q%s, whose line is not a number: %v", doc, cited, lineRef, err)
		return
	}
	body, err := os.ReadFile("../../" + filepath.FromSlash(file))
	if err != nil {
		t.Errorf("%s cites %q, which could not be read, so the line is unchecked: %v", doc, cited, err)
		return
	}
	// The trailing newline every file ends with is not a line of it: counting it
	// makes a citation to the phantom last line resolve and still pass.
	lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
	if line < 1 || line > lines {
		t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there",
			doc, cited, lineRef, file, lines, line)
	}
}
