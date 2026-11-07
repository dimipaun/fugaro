package cli

// The CLI output and console dependency graph plan folder, pinned.
//
// docs/cli-output-console-graph/ is a plan-only folder: spec.md (the owner's
// spec, copied verbatim), README.md (the index, the number block, and the two
// answers that shaped the plans), reconciliation.md (the findings F1-F30, the
// headline table, the decisions D1350-D1362 with the owner's answers of
// 2026-11-06, the tensions T1-T6, the slices, the checks and the UNVERIFIED
// rows) and the two plans -- implementation-plan-output.md (CL1-CL12, part A,
// the CLI's own output) and implementation-plan-graph.md (CG1-CG13, part B, the
// console's graph). It adds no code, and no task in it is approved, so nothing
// here pins a command, a flag or an option.
//
// What it does pin is the half of these documents that rots silently:
//
//  * TestDocsCliOutputConsoleGraphDecisionsAreAnswered -- the reconciliation's
//    decision table holds the owner's block and nothing else: the thirteen rows
//    are D1350-D1362, each carries its dated answer, the reserved tail
//    D1363-D1369 has no row, and the one answer that is not the recommendation
//    (D1355) is recorded as that rather than as the recommendation.
//  * TestDocsCliOutputConsoleGraphStatusLines -- the index, the reconciliation
//    and both plans state the folder's status in one line each (plan only,
//    nothing is implemented, no task approved), and the owner's spec states
//    none, because it is his document and not this folder's.
//  * TestDocsCliOutputConsoleGraphCitedPathsExist -- every relative link in the
//    five documents resolves, and every repository path the index and the
//    reconciliation cite in a code span exists. The two plans are read for
//    their links only: their file lists name what their tasks will create, so a
//    path check over them would pin a plan's file list rather than the tree.
//
// Mutation (verified, run and restored), one per way these documents can rot:
//
//	TestDocsCliOutputConsoleGraphStatusLines
//	  change the index's status line to "plan only; nothing is implemented;
//	  the plan is approved" -> fails, naming the file: the line is the folder's
//	  whole claim about itself, and it is what keeps an answered plan from
//	  reading as a plan somebody may build from;
//	TestDocsCliOutputConsoleGraphDecisionsAreAnswered
//	  write a row for D1363 -> fails, naming the number: the reserved tail is
//	  what stops a later task reaching into the next block, and a row there is a
//	  decision the owner did not give;
//	TestDocsCliOutputConsoleGraphCitedPathsExist
//	  cite `internal/report/report.go` in the reconciliation -> fails, naming the
//	  path: the reconciliation's findings are only worth as much as the code
//	  behind them, and a citation that has moved turns a checked finding into an
//	  unchecked one with no other symptom;
//	  link `reconciliation.md#numbers-this-work-holds` from the index -> passes,
//	  and fails when the anchor is left on the target: a link that names a section
//	  of a file that is there is a link that resolves, and reading the anchor as
//	  part of the name reports it as a file that is not, which is a pin that
//	  teaches a later writer to drop the anchor rather than keep the link.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	cocgReadme     = "docs/cli-output-console-graph/README.md"
	cocgSpec       = "docs/cli-output-console-graph/spec.md"
	cocgReconciled = "docs/cli-output-console-graph/reconciliation.md"
	cocgPlanOutput = "docs/cli-output-console-graph/implementation-plan-output.md"
	cocgPlanGraph  = "docs/cli-output-console-graph/implementation-plan-graph.md"
)

// The owner's block, as the reconciliation's number block and the index's own
// row state it: thirteen decisions written and answered on 2026-11-06, seven
// reserved and unused. A decision row outside the written part is a number
// taken without the owner giving it, which is the one failure the reserved tail
// exists to make visible.
const (
	cocgBlockFirst    = 1350
	cocgBlockLast     = 1362
	cocgReservedFirst = 1363
	cocgReservedLast  = 1369

	// cocgAnswerMarker opens the answer cell of every decision row. The date is
	// the whole of the claim -- an answer recorded without the day it was given
	// is an answer a later reader cannot date against the tree it was given for.
	cocgAnswerMarker = "**2026-11-06:"
)

// cocgDocs is every document of the folder, in the index's own order.
func cocgDocs() []string {
	return []string{cocgReadme, cocgSpec, cocgReconciled, cocgPlanOutput, cocgPlanGraph}
}

// cocgDecisionRowRE finds a row of the reconciliation's decision table, which
// is the only place a decision here is answered. The number is read rather than
// the row assumed, so a row added in the reserved tail or above the block is
// seen for what it is.
var cocgDecisionRowRE = regexp.MustCompile(`(?m)^\| \*\*D([0-9]+)\*\* \|`)

// TestDocsCliOutputConsoleGraphDecisionsAreAnswered: the block the owner gave
// this work is written, all of it is answered, and the table does not run past
// it.
//
// It is pinned because the folder's whole status rests on the difference
// between a recommendation and an answer: these thirteen rows are what the two
// plans are written to, and a row that drifted back into a recommendation, or a
// fourteenth row in the reserved tail, would leave a reader acting on a
// decision nobody gave.
func TestDocsCliOutputConsoleGraphDecisionsAreAnswered(t *testing.T) {
	// The block, claimed whole in the two documents that read it: the index's
	// numbers row and the reconciliation's number block. A file that named the
	// block without its reserved tail would read as having room in it.
	for _, path := range []string{cocgReadme, cocgReconciled} {
		flat := flattenDoc(readDoc(t, path))
		for _, want := range []string{"D1350-D1369", "D1350-D1362", "D1363-D1369"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s never states %q: the block is D1350-D1369, thirteen decisions written and answered and seven reserved, and a file that names it without its reserved tail is a file a later task reads as having room in it",
					path, want)
			}
		}
	}

	rec := readDoc(t, cocgReconciled)
	rows := map[int]string{}
	for _, line := range strings.Split(rec, "\n") {
		m := cocgDecisionRowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: decision row %q does not carry a number", cocgReconciled, line)
		}
		if _, dup := rows[n]; dup {
			t.Errorf("%s writes two rows for D%d: a decision answered twice is a reader with two answers", cocgReconciled, n)
		}
		rows[n] = line
	}
	if len(rows) == 0 {
		t.Fatalf("%s writes no decision row: the block is what the plans are written to", cocgReconciled)
	}

	for n, row := range rows {
		if n < cocgBlockFirst || n > cocgBlockLast {
			t.Errorf("%s answers D%d: the owner's block is D%d-D%d and D%d-D%d are reserved and unused, so a row outside the written part is a decision the owner did not give",
				cocgReconciled, n, cocgBlockFirst, cocgBlockLast, cocgReservedFirst, cocgReservedLast)
			continue
		}
		if !strings.Contains(row, cocgAnswerMarker) {
			t.Errorf("%s: D%d carries no dated owner answer (%q): every one of the thirteen was answered on 2026-11-06, and a row that reads as a recommendation is a decision a reader acts on without one",
				cocgReconciled, n, cocgAnswerMarker)
		}
	}
	for n := cocgBlockFirst; n <= cocgBlockLast; n++ {
		if _, ok := rows[n]; !ok {
			t.Errorf("%s writes no row for D%d: the written part of the block is D%d-D%d with no gap, and a decision missing from it is one the owner answered and the record dropped",
				cocgReconciled, n, cocgBlockFirst, cocgBlockLast)
		}
	}

	// D1355 is the one answer that was not the reconciliation's recommendation,
	// and it is the one a later edit is most likely to soften back: the owner
	// took `termenv` as a direct dependency, which is a supply-chain decision
	// with a reason of its own, and both the row and the index's section have to
	// go on saying so.
	if row, ok := rows[1355]; ok && !strings.Contains(row, "NOT THE RECOMMENDATION") {
		t.Errorf("%s: D1355 does not record that its answer was not the recommendation: the owner promoted `termenv` to a direct dependency against the row's own recommendation, and an answer recorded as the recommendation is a record the owner cannot read",
			cocgReconciled)
	}
	readme := flattenDoc(readDoc(t, cocgReadme))
	for _, want := range []string{"Answers that were not the recommendation", "**D1355:**", "`termenv` becomes a direct dependency"} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not state %q: the index is where a reader meets the block first, and the one answer that was not the recommendation is the one they cannot take from the table alone",
				cocgReadme, want)
		}
	}
}

// cocgStatusRE finds a document's status line: the paragraph the folder's files
// open with. It is matched at the start of a line so a sentence that merely
// mentions the status is not read as one.
var cocgStatusRE = regexp.MustCompile(`(?m)^\*\*Status: .*$`)

// cocgNoTaskApprovedRE matches the clause the status line ends on. The index
// and the reconciliation write "no task approved" and the two plans write "no
// task is approved"; the two spellings are one claim, and it is the claim that
// keeps an answered, planned folder from reading as one somebody may build from.
var cocgNoTaskApprovedRE = regexp.MustCompile(`no task (is )?approved`)

// TestDocsCliOutputConsoleGraphStatusLines: the folder's status is stated in
// one line in every document but the owner's spec, and it says all three
// things: the folder is a plan, nothing is implemented, and no task is
// approved.
//
// The spec is the exception on purpose. It is the owner's document, copied
// verbatim and committed alone, so the state of this folder is stated beside it
// rather than in it -- and a status line that appeared in the spec would be this
// folder's text inside his.
func TestDocsCliOutputConsoleGraphStatusLines(t *testing.T) {
	for _, path := range []string{cocgReadme, cocgReconciled, cocgPlanOutput, cocgPlanGraph} {
		lines := cocgStatusRE.FindAllString(readDoc(t, path), -1)
		if len(lines) != 1 {
			t.Errorf("%s carries %d status lines, want exactly one: the folder's state is one sentence, and a document with none or two leaves a reader to guess which is current",
				path, len(lines))
			continue
		}
		line := strings.ToLower(lines[0])
		for _, want := range []string{"plan only;", "nothing is implemented;"} {
			if !strings.Contains(line, want) {
				t.Errorf("%s' status line does not say %q (%q): the folder is a plan and nothing in it is built, which is what a reader has to know before trusting a task list",
					path, want, lines[0])
			}
		}
		if !cocgNoTaskApprovedRE.MatchString(line) {
			t.Errorf("%s' status line does not say no task is approved (%q): answering a decision approved none of these tasks, and a status line that dropped the clause would read as a go to build",
				path, lines[0])
		}
	}

	if cocgStatusRE.MatchString(readDoc(t, cocgSpec)) {
		t.Errorf("%s carries a status line: the spec is the owner's document, copied verbatim, and this folder's state is stated beside it rather than written into it",
			cocgSpec)
	}
}

// cocgLinkRE finds a relative markdown link target, anchor and all: the anchor
// is stripped where the target is resolved, since it is the file that has to
// exist. It is the shape a document cites one of its own files in, and the shape
// that reads as a path in all five documents, which is why it is checked in all
// five while the code-span paths below are checked where they mean something.
var cocgLinkRE = regexp.MustCompile(`\]\(([^)]+)\)`)

// cocgPathRE finds a repository path in a code span: a full path from the
// repository root, with a source or document extension. A shortened name
// (`spawn.ts`, resolved against a directory named earlier in the sentence), a
// bare file name, a package or module path and a directory all fail this
// pattern on purpose -- none of them can be resolved against the root, and
// guessing which directory a short name belongs to is how a citation check
// starts reporting paths that are not there.
var cocgPathRE = regexp.MustCompile("`((?:docs|internal|ee|console|website|plugin|mobile|shared|images|schemas|scripts|deploy|cmd)/[A-Za-z0-9_./-]*\\.(?:go|md|ts|tsx|json|yaml|yml|tf|css|mjs|sh))`")

// TestDocsCliOutputConsoleGraphCitedPathsExist: the paths these documents point
// at are paths of this tree.
//
// Every relative link in all five documents is resolved, because a link is the
// one cross-reference shape every document here uses and a broken one is a
// reader sent nowhere. The code-span paths are read in the index and the
// reconciliation only: those two describe the tree as it stands, and the two
// plans name in the same shape the files their tasks will create, so a check
// over them would pin a plan's file list rather than the tree.
func TestDocsCliOutputConsoleGraphCitedPathsExist(t *testing.T) {
	for _, path := range cocgDocs() {
		doc := readDoc(t, path)
		links := 0
		for _, m := range cocgLinkRE.FindAllStringSubmatch(doc, -1) {
			target := strings.TrimSpace(m[1])
			if target == "" || strings.Contains(target, "://") {
				continue // an external reference is not a path of this tree
			}
			links++
			// A target may carry an anchor (`reconciliation.md#t7`), and it is the
			// file that has to be there: stat'ing the anchor along with it reports a
			// link that is there as a file that is not. A target that is nothing but
			// an anchor points inside the document it is written in and names no path
			// of this tree at all, so it is counted as a link and resolved no further.
			file, _, _ := strings.Cut(target, "#")
			if file == "" {
				continue
			}
			resolved := filepath.Join("..", "..", filepath.FromSlash(filepath.Dir(path)), filepath.FromSlash(file))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q, which is not a file of this tree: a link that resolves to nothing is a reader sent to a document that is not there", path, target)
			}
		}
		// The floor is what catches a regexp that has stopped matching the
		// documents' shapes: a file the check finds no link in is a file it has
		// gone blind on. The owner's spec is the one document that legitimately
		// has none -- it is his text, copied verbatim, and it cites nothing of
		// this folder -- so it is read for its links without being held to one.
		if links == 0 && path != cocgSpec {
			t.Errorf("%s links to nothing: every document of this folder points at its neighbours, so a file the check finds no link in is a file the check has gone blind on", path)
		}
	}

	for _, path := range []string{cocgReadme, cocgReconciled} {
		doc := readDoc(t, path)
		matches := cocgPathRE.FindAllStringSubmatch(doc, -1)
		if len(matches) == 0 {
			t.Errorf("%s cites no repository path in a code span: the reconciliation is only worth as much as the code behind it, so a document the check finds no citation in is a document whose findings went unchecked", path)
		}
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(m[1]))); err != nil {
				t.Errorf("%s cites %q, which is not a file of this tree: a reconciliation whose citations have moved is a reconciliation nobody can check", path, m[1])
			}
		}
		t.Logf("%s: %d repository paths cited in a code span", path, len(matches))
	}
}
