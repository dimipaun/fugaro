package cli

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The four documents of docs/rigor-modes/ — the owner's spec, the
// reconciliation, the plan and the index — and the guard that owns them.
//
// There is no registry test over docs/ in this package: nothing pins the set of
// docs folders and nothing pins a folder's documents on its behalf, so a folder
// that wants its documents pinned writes its own file — this one, the same
// convention docs_windows_cli_test.go states for docs/windows-cli/ and
// docs_in_job_review_test.go for docs/in-job-review/.
//
// The folder is a plan: nothing in it is implemented and every decision in it is
// open. So what a guard can usefully hold is the plan's own shape and the plan's
// own claims about itself — the status line, the numbered findings, decisions,
// items and unverified rows with no gap, the census stating the command beside
// every count, every task carrying the seven things a task must state, and
// the folder's refusal to put a decision in the owner's mouth. Every one of
// those is a sentence a later edit can quietly break, which is what makes them
// worth pinning rather than asserting in prose.
const (
	rigorModesReadme = "docs/rigor-modes/README.md"
	rigorModesSpec   = "docs/rigor-modes/spec.md"
	rigorModesRecon  = "docs/rigor-modes/reconciliation.md"
	rigorModesPlan   = "docs/rigor-modes/implementation-plan.md"

	// rigorModesStatus is the index's status line, word for word: the folder is
	// a plan with nothing implemented and every decision open. It moves only
	// when the work it describes moves, which is why it is asserted rather than
	// left to a reader.
	rigorModesStatus = "**Status: plan only; nothing is implemented; decisions open.**"

	// rigorModesGateHeading is the plan's gate, and rigorModesSlicesHeading the
	// section whose tasks the completeness guard counts.
	rigorModesGateHeading   = "## OWNER APPROVAL GATE"
	rigorModesSlicesHeading = "## 8. The slices"

	// rigorModesCensusHeading is the reconciliation's census, whose rows the
	// census guard reads.
	rigorModesCensusHeading = "## 4. The census, and what it is scoped to"
)

// rigorModesRevision is the commit the reconciliation was read at. A later run
// that re-reads the tree moves it in the same commit, which is how a reader can
// tell how far the document has aged.
const (
	rigorModesRevision = "`1d325928`"
	rigorModesHash     = "1d32592836a089cce2d72ff53e29116a00e1c433"
)

// rigorModesIndexedFiles are the documents the index must name. The index is
// the folder's entry point, so a document it does not name is one a reader
// cannot find.
var rigorModesIndexedFiles = []string{
	"[spec.md](spec.md)",
	"[reconciliation.md](reconciliation.md)",
	"[implementation-plan.md](implementation-plan.md)",
}

// rigorModesClasses are the five words a finding may be classified with
// (reconciliation.md §3). A sixth word is a decision about what the documents
// promise, not a synonym.
var rigorModesClasses = []string{"Wrong", "Exists", "Gap", "Rule", "Open"}

// rigorModesTaskLabels are the seven things every task in the plan must state:
// what it changes, how it is tested, what fails first, how an agent verifies it,
// what it waits for, who reviews it, and what the commit says. A task missing
// one of them is a task an agent cannot pick up.
var rigorModesTaskLabels = []string{
	"**Files.**", "**Tests.**", "**Red step.**", "**Verification.**",
	"**Depends on.**", "**Reviewer.**", "**Commit.**",
}

// rigorModesLabels matches a finding's, a decision's, an item's and an
// unverified row's opening label. All four are numbered from 1 with no gap, and
// a reader who cites "F19" or "D7" has to be able to trust the number.
var (
	rigorModesFindingRE  = regexp.MustCompile(`(?m)^\*\*F([0-9]+) —`)
	rigorModesDecisionRE = regexp.MustCompile(`(?m)^### D([0-9]+) —`)
	rigorModesItemRE     = regexp.MustCompile(`(?m)^\*\*O([0-9]+) —`)
	rigorModesUnverifRE  = regexp.MustCompile(`(?m)^\| U([0-9]+) \|`)
	rigorModesClassRE    = regexp.MustCompile(`Class:\s*\*\*([A-Za-z]+)\*\*`)
	rigorModesTaskRE     = regexp.MustCompile(`(?m)^\*\*(T[0-9]+\.[0-9]+) — `)
	rigorModesSliceRE    = regexp.MustCompile(`(?m)^### Slice ([0-9]+) — `)
)

// TestDocsRigorModesIsWritten: the folder exists with its index and its three
// documents, the index carries the status line word for word and names the other
// three files, and each document carries the structure the brief asked for. Its
// red state is this test: the folder's guard did not exist until this file did.
func TestDocsRigorModesIsWritten(t *testing.T) {
	readme := readDoc(t, rigorModesReadme)
	spec := readDoc(t, rigorModesSpec)
	recon := readDoc(t, rigorModesRecon)
	plan := readDoc(t, rigorModesPlan)

	for _, want := range append([]string{
		rigorModesStatus,
		"**Owner:** the Fugaro owner",
		rigorModesRevision,
		rigorModesHash,
		// The claim that this folder decides nothing for the owner.
		"**Not an approval.**",
		// The check number this work proposes, in the index, where a reader
		// looks for the numbers a work holds.
		"**Check 113**",
	}, rigorModesIndexedFiles...) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", rigorModesReadme, want)
		}
	}

	for _, want := range []string{
		// The owner's spec, verbatim and unannotated.
		"Oct 20, 2026 · @Dimi",
		"## Open questions",
		"Fugaro Rigor Modes Spec",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", rigorModesSpec, want)
		}
	}
	// The spec is the owner's; the folder does not add to it. Its open questions
	// stay four, and no document in the folder claims any of them answered.
	if got := strings.Count(spec[strings.Index(spec, "## Open questions"):], "\n- "); got != 4 {
		t.Errorf("%s: the owner's open questions are %d, want 4", rigorModesSpec, got)
	}

	for _, want := range []string{
		rigorModesStatus,
		"## 3. Findings",
		rigorModesCensusHeading,
		"## 5. Decisions for the owner",
		"## 9. UNVERIFIED",
		"**None of these is taken.**",
		// The revision, in both forms, so a later reader can compare a re-read
		// against the commit this document was written at.
		rigorModesRevision,
		rigorModesHash,
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q", rigorModesRecon, want)
		}
	}

	for _, want := range []string{
		rigorModesStatus,
		rigorModesSlicesHeading,
		rigorModesGateHeading,
		"**This gate is not passed, and nothing below is approved.**",
		// The live check this work proposes, named in the plan as well as in the
		// index, because a number claimed in two places is a number to check.
		"Check 113",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", rigorModesPlan, want)
		}
	}
}

// rigorModesNumbered checks one series of **N — labels** runs 1..n with no gap
// and no repeat, and reports the count. It is this file's own helper rather than
// the one docs_windows_cli_test.go defines, so neither folder's guard depends on
// the other's file.
func rigorModesNumbered(t *testing.T, path, doc, what string, re *regexp.Regexp) []int {
	t.Helper()
	var nums []int
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: %s label %q does not carry a number", path, what, m[0])
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no %s", path, what)
	}
	for i, n := range nums {
		if n != i+1 {
			t.Fatalf("%s: %s are %v, want 1..%d with no gap: a citation has to mean one thing", path, what, nums, len(nums))
		}
	}
	return nums
}

// TestDocsRigorModesFindingsDecisionsAndItemsAreNumbered: every finding carries
// a number and one of the five class words, every decision and every item is
// numbered from 1 with no gap, and the UNVERIFIED table is numbered the same
// way. The index states "F1–F34", "D1–D19" and "U1–U8", so the ranges it states
// are exactly the ranges the documents carry.
func TestDocsRigorModesFindingsDecisionsAndItemsAreNumbered(t *testing.T) {
	recon := readDoc(t, rigorModesRecon)

	findings := rigorModesNumbered(t, rigorModesRecon, recon, "findings", rigorModesFindingRE)
	decisions := rigorModesNumbered(t, rigorModesRecon, recon, "decisions", rigorModesDecisionRE)
	items := rigorModesNumbered(t, rigorModesRecon, recon, "items", rigorModesItemRE)
	unverified := rigorModesNumbered(t, rigorModesRecon, recon, "UNVERIFIED rows", rigorModesUnverifRE)

	// Every finding carries its class, and the class is one of the five words.
	// The section is split on the finding labels so a class cannot be read as
	// belonging to the neighbour.
	chunks := rigorModesFindingRE.Split(recon, -1)[1:]
	if len(chunks) != len(findings) {
		t.Fatalf("%s: %d findings, %d chunks", rigorModesRecon, len(findings), len(chunks))
	}
	counted := map[string]int{}
	for i, chunk := range chunks {
		m := rigorModesClassRE.FindStringSubmatch(chunk)
		if m == nil {
			t.Errorf("%s: F%d carries no 'Class: **X**'", rigorModesRecon, findings[i])
			continue
		}
		if !slices.Contains(rigorModesClasses, m[1]) {
			t.Errorf("%s: F%d is classified %q, want one of %v", rigorModesRecon, findings[i], m[1], rigorModesClasses)
			continue
		}
		counted[m[1]]++
	}
	for _, c := range rigorModesClasses {
		if counted[c] == 0 {
			t.Errorf("%s classifies no finding as %s: every one of the five words is in use", rigorModesRecon, c)
		}
	}

	// The index's own ranges, checked against the counts rather than trusted,
	// and the plan's citations of the same ranges.
	readme := readDoc(t, rigorModesReadme)
	for _, want := range []string{
		"**D1–D" + strconv.Itoa(len(decisions)) + "**",
		"**F1–F" + strconv.Itoa(len(findings)) + "**",
		"**U1–U" + strconv.Itoa(len(unverified)) + "**",
		"**O1–O" + strconv.Itoa(len(items)) + "**",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not state %q: the index's ranges are checked against the documents' own counts", rigorModesReadme, want)
		}
	}
	plan := readDoc(t, rigorModesPlan)
	for _, want := range []string{
		"F1–F" + strconv.Itoa(len(findings)),
		"D1–D" + strconv.Itoa(len(decisions)),
		"U1–U" + strconv.Itoa(len(unverified)),
		"O1–O" + strconv.Itoa(len(items)),
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not cite %q", rigorModesPlan, want)
		}
	}
}

// TestDocsRigorModesCensusStatesItsCommands: the census carries a command in
// every row, and the section states the scope the counts are defined over. A
// count without its command is a figure a later reader cannot re-derive, which
// is the rule the documents state for themselves.
func TestDocsRigorModesCensusStatesItsCommands(t *testing.T) {
	recon := readDoc(t, rigorModesRecon)

	i := strings.Index(recon, rigorModesCensusHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q", rigorModesRecon, rigorModesCensusHeading)
	}
	body := recon[i:]
	if j := strings.Index(body, "\n## 5."); j >= 0 {
		body = body[:j]
	}
	rows, commands := 0, 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := rigorModesTableCells(line)
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == "Construct" {
			continue // the header
		}
		if rigorModesAllRule(cells) {
			continue // the separator
		}
		if len(cells) != 3 {
			t.Errorf("%s: census row %q has %d cells, want 3 (Construct | Count | Command)", rigorModesRecon, line, len(cells))
			continue
		}
		rows++
		if strings.Count(cells[2], "`") < 2 {
			t.Errorf("%s: census row %q carries no command: where a count is volatile state the command, not the figure", rigorModesRecon, line)
			continue
		}
		commands++
	}
	if rows == 0 {
		t.Errorf("%s: the census carries no rows", rigorModesRecon)
	}
	if commands != rows {
		t.Errorf("%s: %d of %d census rows carry a command, want all of them", rigorModesRecon, commands, rows)
	}
}

// rigorModesRuleRE matches a markdown table's separator cell ("---", ":--", …).
var rigorModesRuleRE = regexp.MustCompile(`^:?-+:?$`)

// rigorModesAllRule reports whether every cell is a separator, which is the row
// under a markdown table's header and not a row of data.
func rigorModesAllRule(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !rigorModesRuleRE.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

// rigorModesTableCells splits a markdown table row into cells, keeping an
// escaped pipe (`\|`, which a command inside a code span needs) inside its cell.
func rigorModesTableCells(line string) []string {
	const esc = "\x00"
	line = strings.ReplaceAll(line, `\|`, esc)
	line = strings.Trim(strings.TrimSpace(line), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.ReplaceAll(cells[i], esc, `\|`)
	}
	return cells
}

// TestDocsRigorModesEveryTaskIsComplete: every task in the plan states the seven
// things a task has to state, and the slices are numbered from 0 with no gap.
// This is the folder's own reading of the brief's requirement that each part
// carry its files, tests, a verification an agent can run and the reviewer that
// applies — a task missing one is a task nobody can start.
func TestDocsRigorModesEveryTaskIsComplete(t *testing.T) {
	plan := readDoc(t, rigorModesPlan)

	if i := strings.Index(plan, rigorModesSlicesHeading); i < 0 {
		t.Fatalf("%s lacks %q", rigorModesPlan, rigorModesSlicesHeading)
	}

	idx := rigorModesTaskRE.FindAllStringSubmatchIndex(plan, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no tasks", rigorModesPlan)
	}
	for n, m := range idx {
		id := plan[m[2]:m[3]]
		end := len(plan)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		body := plan[m[1]:end]
		for _, label := range rigorModesTaskLabels {
			if !strings.Contains(body, label) {
				t.Errorf("%s: %s states no %s", rigorModesPlan, id, label)
			}
		}
	}

	// The slices, numbered from 0 with no gap (a slice is a part of a plan, so
	// its numbering starts at zero, unlike a finding's), and every task
	// belonging to one: a task written before the first slice heading is a task
	// in no slice.
	var nums []int
	for _, m := range rigorModesSliceRE.FindAllStringSubmatch(plan, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: slice label %q does not carry a number", rigorModesPlan, m[0])
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		t.Fatalf("%s states no slices", rigorModesPlan)
	}
	for i, n := range nums {
		if n != i {
			t.Fatalf("%s: slices are %v, want 0..%d with no gap", rigorModesPlan, nums, len(nums)-1)
		}
	}
	if idx[0][0] < strings.Index(plan, rigorModesSlicesHeading) {
		t.Errorf("%s: a task is written above %q", rigorModesPlan, rigorModesSlicesHeading)
	}
}
