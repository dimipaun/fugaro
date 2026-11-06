package cli

// The hosted MCP server's design folder, pinned.
//
// docs/mcp-server/ is a design-only folder: README.md (the index, the reading
// of the number block and what a reader should not take from it), spec.md (the
// owner's spec copied verbatim as §0, then the design written to his answers),
// reconciliation.md (the findings F1-F32, the fifteen decisions D920-D934 and
// the answers of 2026-11-04, and the answer of 2026-11-05, D935) and
// implementation-plan.md (the tasks M1-M10). It
// adds no code and no task in it is approved, so nothing here pins a command, a
// flag or an option.
//
// What it does pin is the half of these documents that rots silently:
//
//  * TestDocsMCPServerIsWritten -- the four files exist, each carries the
//    folder's status line word for word, and the plan states the two gates
//    every task waits behind.
//  * TestDocsMCPServerAnswersAreTheOnesInForce -- the answers of 2026-11-04 and
//    the answer of 2026-11-05 are dated in reconciliation.md §4, every one of
//    them is the recommendation (no
//    row says otherwise), D922's restatement is recorded rather than the answer
//    it replaced, and the "D922/D933 tension" the first answer created is gone
//    from every file.
//  * TestDocsMCPServerNamesWhatItDesigns -- the design's tool is `spawn_task`
//    where §0's verbatim copy still says `submit_task`, the subtree is
//    `mcpauth`, and the launch path is `fugaro run`'s own.
//  * TestDocsMCPServerCitationsResolve -- every `path:line` citation resolves to
//    a file of this tree and a line inside it, measured against an independent
//    census so a regexp that stopped matching fails rather than passing
//    vacuously.
//
// The citation resolver is the cloud senior review's (`csrCitationRE`,
// `csrCensusRE`, `csrResolveCitation`, `csrBareNames`), through the
// config-management folder's own pattern: three docs tests asking one question
// of one tree should not each carry their own, and a second copy is the one that
// drifts.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// mcpPlainCitationRE finds the shape README.md writes its citations in: a path
// and a line with no backticks around them. csrCitationRE cannot see those, and
// the README is where this folder's number block lives -- so without this the
// citation check passes on README vacuously, which is the one failure mode the
// census cannot catch when a whole file is written in a shape the regexp does
// not know.
// The optional backtick after the extension is the README's own house style: it
// closes the code span before the colon, writing the path in code and the line
// beside it, so a pattern that demanded the two be backticked together would
// still see none of the README's citations.
var mcpPlainCitationRE = regexp.MustCompile("[A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:go|md|json|ts|tf|tmpl|yaml|yml)`?:[0-9]+")

const (
	mcpReadme     = "docs/mcp-server/README.md"
	mcpSpec       = "docs/mcp-server/spec.md"
	mcpReconciled = "docs/mcp-server/reconciliation.md"
	mcpPlan       = "docs/mcp-server/implementation-plan.md"

	// mcpStatus is the line every file in this folder carries, word for word.
	// It is the folder's whole claim about itself -- the design is answered,
	// the plan is all there is, nothing is implemented and no task is approved
	// -- and a file that dropped it would read as a design somebody may build
	// from, which is the one thing this folder is not.
	mcpStatus = "Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved."
)

func mcpDocs() []string { return []string{mcpReadme, mcpSpec, mcpReconciled, mcpPlan} }

// TestDocsMCPServerIsWritten: the four files are there, each carries the status
// line, and the plan still says what every task in it waits behind.
func TestDocsMCPServerIsWritten(t *testing.T) {
	for _, path := range mcpDocs() {
		flat := flattenDoc(readDoc(t, path))
		if !strings.Contains(flat, mcpStatus) {
			t.Errorf("%s does not carry the folder's status line %q: every file of an answered design folder says in one line that the design is answered, that nothing is implemented and that no task is approved, and a file that drops it reads as a design somebody may build from",
				path, mcpStatus)
		}
	}

	plan := flattenDoc(readDoc(t, mcpPlan))
	for _, want := range []string{
		// The two gates §3 of the plan states before the first task: the
		// review the coordinator runs, and the licence nobody has confirmed.
		"independent SECURITY review of [spec.md](spec.md) §8",
		"**run by the coordinator**",
		"D921's unconfirmed licence",
		"the five whose marker says **mandatory** are M3, M4, M5, M7 and M9",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not state %q: the plan gates every task on the security review and on the licence, and it names which tasks are marked mandatory, so a later edit that dropped either would leave a task looking startable",
				mcpPlan, want)
		}
	}
}

// TestDocsMCPServerAnswersAreTheOnesInForce: the answers of 2026-11-04 are in
// reconciliation.md §4, dated, and the folder is written to the ones in force.
//
// This is the test the folder needed and did not have. D922 was answered one way
// and restated the same day, and between the two the design said the opposite of
// what it says now in four files at once -- so what is pinned here is not just
// that the answers are present but that the folder is not carrying the replaced
// one: no row calls any answer "not the recommendation", and the tension the
// replaced answer created is gone.
func TestDocsMCPServerAnswersAreTheOnesInForce(t *testing.T) {
	rec := readDoc(t, mcpReconciled)
	if !strings.Contains(rec, "### The owner's answers, 2026-11-04") {
		t.Fatalf("%s does not carry the answers section: the fifteen decisions were answered on 2026-11-04 and the record of that is the section a reader looks for first", mcpReconciled)
	}

	// Every decision is written at least twice in the reconciliation -- once as
	// the question put to the owner and once as the answer -- because a decision
	// whose answer went missing would leave the question table reading as if it
	// were still open.
	for i := 920; i <= 934; i++ {
		d := fmt.Sprintf("**D%d**", i)
		if n := strings.Count(rec, d); n < 2 {
			t.Errorf("%s names %s %d times and the folder writes each decision twice (the question of §4's first table and its answer in §4's second): the answer's row is missing or the question's is",
				mcpReconciled, d, n)
		}
	}

	// The answers in force are all the recommendation, and D922's replaced
	// answer is recorded as replaced rather than as the answer.
	for _, path := range mcpDocs() {
		doc := readDoc(t, path)
		if strings.Contains(doc, "Not the recommendation") || strings.Contains(doc, "**not the recommendation** is a place") {
			t.Errorf("%s still marks an answer as not the recommendation: every answer of 2026-11-04 took it, and the one that did not was restated the same day rather than left standing", path)
		}
		if strings.Contains(doc, "D922/D933 tension") {
			t.Errorf("%s still carries the D922/D933 tension text: D922's restatement closed the subtree, so the database rule and D933's tool rule agree and the sentence that said otherwise has to be gone from every file rather than left beside its replacement", path)
		}
		if !strings.Contains(doc, ".read: false") || !strings.Contains(doc, ".write: false") {
			t.Errorf("%s does not state the closed rule: D922's answer in force is `.read: false` and `.write: false`, and a file that states the subtree without them is a file a builder could read as leaving it open", path)
		}
	}

	// The restatement itself is recorded, in the two files that carry the
	// answer, so a reader of either cannot conclude that the first answer stands.
	for _, path := range []string{mcpReconciled, mcpSpec, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "restated") {
			t.Errorf("%s does not record that D922's answer was restated the same day: the folder's earlier record was the other answer, and a reader who finds only the current one cannot tell that it moved", path)
		}
	}

	// The answer of 2026-11-05 (D935), from the independent review of the plan's
	// M3, is recorded the way the fifteen before it are: a section of its own in
	// the reconciliation, the decision written twice -- the question of §4's
	// first table and the answer under it -- and the two files that carry the
	// design naming it, because a reader of the plan or the index alone has to
	// find the same answer the reconciliation records.
	if !strings.Contains(rec, "### The answer of 2026-11-05") {
		t.Fatalf("%s does not carry the answer of 2026-11-05: the review of the plan's M3 found that the closed subtree is still reachable by everyone the installation lists (F32), the owner answered it the same day, and that answer is what the plan's M2, M3, M4, M5 and M10 are written to", mcpReconciled)
	}
	if n := strings.Count(rec, "**D935**"); n < 2 {
		t.Errorf("%s names **D935** %d times and the folder writes each decision twice (the question of §4's first table and its answer in §4's second): the answer's row is missing or the question's",
			mcpReconciled, n)
	}
	for _, path := range []string{mcpReadme, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "D935") {
			t.Errorf("%s never names D935: the token records leave the project's database, and the index and the plan are what a reader meets first, so an amendment that reached the reconciliation alone would leave both describing a store the records have left", path)
		}
	}

	// And the claim the review overturned is gone rather than left standing
	// beside its replacement: the closed subtree was written as the one place
	// nothing but the coordinator's database grant reaches, which is exactly
	// what F32 disproved.
	for _, path := range mcpDocs() {
		if flat := flattenDoc(readDoc(t, path)); strings.Contains(flat, "the one place in this design where nothing but the coordinator's database admin grant reaches the data") {
			t.Errorf("%s still says the closed subtree is the one place nothing but the coordinator's database grant reaches: it sits in the project's shared database, whose viewer and admin grants are project-level and cover every node in it (F32, D935), so the sentence states the opposite of the answer in force", path)
		}
	}

	// The store's name, and the reach the second review of this work removed.
	// Section 8 and M4 first named the recommended bucket under the managed
	// prefix `fmgd-`, on the claim that no project-level storage binding exists
	// -- and both halves are gone: the managed deployer's conditioned role is
	// bound at project level on that prefix (deployer.tf:379, :142) and every
	// operator can act as the deployer (deployer.tf:501), so a prefixed name put
	// the store inside the reach it exists to leave. What is pinned is the name
	// that replaces it, the sentence that says why, and the reading check 8
	// gains -- so a later edit that quietly restored the prefix fails here.
	plan := flattenDoc(readDoc(t, mcpPlan))
	if !strings.Contains(plan, "fugaro-mcpauth-<gcp-project>") {
		t.Errorf("%s does not name the store's bucket %q: §8 and M4 recommend a bucket of its own and the name is the whole of what keeps the managed deployer's conditioned role off it", mcpPlan, "fugaro-mcpauth-<gcp-project>")
	}
	if !strings.Contains(plan, "`fmgd-` is the managed deployer's fence") {
		t.Errorf("%s does not say why the store's name is outside the managed prefix: the deployer's conditioned storage role is a project-level binding on `fmgd-` (deployer.tf:379) and every operator can act as the deployer (deployer.tf:501), so the reason is the correction rather than a detail", mcpPlan)
	}
	for _, path := range mcpDocs() {
		flat := flattenDoc(readDoc(t, path))
		if strings.Contains(flat, "fmgd-<gcp-project>-mcpauth") {
			t.Errorf("%s still names the store's bucket under the managed prefix `fmgd-<gcp-project>-mcpauth`: the managed deployer's conditioned storage role reaches every bucket carrying that prefix and every operator can act as the deployer, so a store so named is readable and rewritable by both (the second review of this work, 2026-11-05)", path)
		}
		if strings.Contains(flat, "No project-level storage binding exists anywhere") {
			t.Errorf("%s still says no project-level storage binding exists anywhere: the managed deployer's `fugaroDeployerStorage` is one, conditioned on `fmgd-` (deployer.tf:379), and the claim is what made the store's name wrong", path)
		}
	}
	if !strings.Contains(flattenDoc(readDoc(t, mcpReconciled)), "impersonate the deployer as an operator") {
		t.Errorf("%s: check 8 does not carry the operator-impersonating-the-deployer reading: the store's name keeps that identity off it, and only a live project can show the name did what it was chosen to do", mcpReconciled)
	}
}

// TestDocsMCPServerNamesWhatItDesigns: the names the design fixes are the names
// the documents use.
//
// §0 is the owner's spec copied verbatim, so it keeps the tool name he wrote
// there while the design around it uses the one he answered. That is the one
// place the two disagree on purpose, and a reader who met the disagreement
// without the note would think a tool was missing.
func TestDocsMCPServerNamesWhatItDesigns(t *testing.T) {
	spec := readDoc(t, mcpSpec)
	if !strings.Contains(spec, "- **submit_task**: start a task") {
		t.Errorf("%s no longer carries §0 verbatim: the owner's spec is copied word for word, and %q is how it names the tool", mcpSpec, "submit_task")
	}
	if !strings.Contains(spec, "`spawn_task`") {
		t.Errorf("%s never names `spawn_task`: D931's answer of 2026-11-04 gives the tool that name, and the design's own sections have to use it", mcpSpec)
	}

	for _, path := range mcpDocs() {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "mcpauth") {
			t.Errorf("%s does not name `mcpauth`: D922's answer gives the token records' subtree that name, and four documents that each describe the subtree have to call it the same thing", path)
		}
	}
	// README.md is the index and names the tool only in passing; the three
	// documents that state the design are the ones that have to use his name for
	// it.
	for _, path := range []string{mcpSpec, mcpReconciled, mcpPlan} {
		if flat := flattenDoc(readDoc(t, path)); !strings.Contains(flat, "spawn_task") {
			t.Errorf("%s never names `spawn_task`: D931's answer of 2026-11-04 gives the tool that name, and a document that still calls it `submit_task` is written to an answer that was replaced", path)
		}
	}

	// The launch path is `fugaro run`'s own, named by the two things it reuses:
	// the claim that makes a submit idempotent, and the pre-check that carries
	// the kill switch.
	for _, want := range []string{"internal/runstore/launch.go:112", "internal/budget/precheck.go:58"} {
		if flat := flattenDoc(readDoc(t, mcpSpec)); !strings.Contains(flat, want) {
			t.Errorf("%s does not cite %q: D931's single launch path is the design's answer to a front door that could otherwise spend money a person's launch cannot, and the citation is what makes the claim checkable", mcpSpec, want)
		}
	}
}

// TestDocsMCPServerCitationsResolve: every file and line these documents cite
// exists. A reconciliation is only worth as much as the code behind it, and a
// citation whose line moved turns a checked finding into an unchecked one with
// no other symptom.
func TestDocsMCPServerCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	for _, path := range mcpDocs() {
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
			mcpCheckCitation(t, path, m[1], m[2], bare)
		}
		for _, tok := range mcpPlainCitationRE.FindAllString(doc, -1) {
			cut := strings.LastIndex(tok, ":")
			mcpCheckCitation(t, path, strings.TrimSuffix(tok[:cut], "`"), tok[cut:], bare)
		}
		if len(matches)+len(mcpPlainCitationRE.FindAllString(doc, -1)) == 0 {
			t.Errorf("%s cites no file and line at all: every document in this folder stands on the code it was reconciled against, so a file the check finds no citation in is a file the check has gone blind on", path)
		}
		t.Logf("%s: %d backticked citations carrying a line, against a census of %d path-looking tokens with one, and %d written without backticks",
			path, len(matches), census, len(mcpPlainCitationRE.FindAllString(doc, -1)))
	}
}

// mcpCheckCitation resolves one cited file and line to a line of this tree.
func mcpCheckCitation(t *testing.T, doc, cited, lineRef string, bare map[string][]string) {
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
