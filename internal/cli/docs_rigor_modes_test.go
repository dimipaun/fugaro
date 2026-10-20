package cli

import (
	"os/exec"
	"regexp"
	"runtime"
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

	// rigorModesQuestionsHeading is the spec's own section of open questions, the
	// one the spec guard counts and no document here may answer.
	rigorModesQuestionsHeading = "## Open questions"

	// rigorModesBacklogHeading is the reconciliation's section for the owner's
	// backlog ruling of 2026-10-20, and rigorModesBacklogFolder the folder every
	// document in the set puts the items in. The ruling is a deliverable of that
	// amendment the way the scope ruling was of the one before it, so it is held
	// the same way: by the section and the path a later edit would quietly drop,
	// not by the prose around them.
	rigorModesBacklogHeading = "### 1.3 The backlog ruling"
	rigorModesBacklogFolder  = "docs/backlog/"
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
		// looks for the numbers a work holds. It is 119 rather than 110-118
		// because every one of those is held by an open claim at the base
		// (110 named by the mobile P5.3 design, 111 the setup wizard's, 112
		// the Windows CLI's, 113-118 the voice session's six).
		"**Check 119**",
		// The backlog the owner ruled on, named in the index by the folder its
		// items live in: an index that stops naming it is an index a reader
		// cannot find the backlog in.
		rigorModesBacklogFolder,
	}, rigorModesIndexedFiles...) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s lacks %q", rigorModesReadme, want)
		}
	}

	for _, want := range []string{
		rigorModesStatus,
		"## 3. Findings",
		rigorModesCensusHeading,
		rigorModesBacklogHeading,
		rigorModesBacklogFolder,
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
		rigorModesBacklogFolder,
		"**This gate is not passed, and nothing below is approved.**",
		// The live check this work proposes, named in the plan as well as in the
		// index, because a number claimed in two places is a number to check.
		"Check 119",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q", rigorModesPlan, want)
		}
	}
}

// TestDocsRigorModesSpecIsTheOwners: the spec is the owner's document, and the
// folder does not put words in the owner's mouth. Its four open questions stay
// open — no document here records an answer, which is the marker the sibling
// folder uses ("**Answered 2026-10-19: option 1**") and the one this folder must
// not carry while the status line says the decisions are open — and each of the
// four has a numbered recommendation behind it rather than being left unread.
func TestDocsRigorModesSpecIsTheOwners(t *testing.T) {
	spec := readDoc(t, rigorModesSpec)
	recon := readDoc(t, rigorModesRecon)
	plan := readDoc(t, rigorModesPlan)

	for _, want := range []string{
		"# Fugaro Rigor Modes Spec",
		"Oct 20, 2026 · @Dimi",
		// The five modes, as the owner named them.
		"**POC**", "**Startup**", "**Pro**", "**Critical**", "**Hotfix**",
		rigorModesQuestionsHeading,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", rigorModesSpec, want)
		}
	}
	// The index is looked up and checked before it is sliced: a guard over a
	// heading that has been removed fails, and does not panic on spec[-1:].
	i := strings.Index(spec, rigorModesQuestionsHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q: a document with no question section has no questions to count", rigorModesSpec, rigorModesQuestionsHeading)
	}
	if got := strings.Count(spec[i:], "\n- "); got != 4 {
		t.Errorf("%s: the owner's open questions are %d, want 4", rigorModesSpec, got)
	}

	// No answer is recorded here: not by the sibling folder's marker, and not in
	// words either.
	for _, doc := range []struct{ path, body string }{
		{rigorModesRecon, recon},
		{rigorModesPlan, plan},
	} {
		for _, want := range []string{"**Answered", "the owner answered"} {
			if strings.Contains(doc.body, want) {
				t.Errorf("%s records %q: this folder recommends, and the status line says the decisions are open", doc.path, want)
			}
		}
	}

	// Each of the owner's four questions has a numbered recommendation or item
	// behind it: the scale (D1), the calibration (O1), where the config lives
	// (D5) and who may invoke Hotfix (D12).
	for _, want := range []string{"### D1 —", "**O1 —", "### D5 —", "### D12 —"} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: every one of the owner's open questions has a recommendation behind it", rigorModesRecon, want)
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
// way. The index states "F1–F41", "D1–D25" and "U1–U10", so the ranges it states
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
// every row, and running that command reproduces the count the row states. A
// count without its command is a figure a later reader cannot re-derive, and a
// command that no longer produces its figure is worse than no command at all:
// it is a claim that looks checked. The other 47 rows this repository's folders
// assert in prose; this one is the only place the figure can be re-derived by
// the test itself, so it is re-derived.
//
// The commands are POSIX shell on purpose — `sed`, `grep`, `ls`, `wc` and
// `git grep` — because that is what a reader copies out of the table, so the
// guard runs them the way a reader would, from the repository root.
func TestDocsRigorModesCensusStatesItsCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the census's commands are POSIX shell: sed, grep, ls, wc and git grep")
	}
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	recon := readDoc(t, rigorModesRecon)

	i := strings.Index(recon, rigorModesCensusHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q", rigorModesRecon, rigorModesCensusHeading)
	}
	body := recon[i:]
	if j := strings.Index(body, "\n## 5."); j >= 0 {
		body = body[:j]
	}
	rows, commands, checked := 0, 0, 0
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

		// The row's own figure, and the command that has to produce it.
		want, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			t.Errorf("%s: census row %q states %q as its count, which is not a number", rigorModesRecon, line, cells[1])
			continue
		}
		cmd := rigorModesCommand(cells[2])
		if cmd == "" {
			t.Errorf("%s: census row %q carries no command inside its code span", rigorModesRecon, line)
			continue
		}
		c := exec.Command(sh, "-c", cmd)
		c.Dir = "../.." // the census states its commands run from the repository root
		out, err := c.Output()
		// `grep` and `git grep` exit 1 when they match nothing, which is what a
		// row stating 0 legitimately does; the figure is the output, so only a
		// command that produced none is an error.
		if err != nil && len(out) == 0 {
			t.Errorf("%s: census row %q: running %s: %v", rigorModesRecon, line, cmd, err)
			continue
		}
		got := strings.TrimSpace(string(out))
		checked++
		if got != strconv.Itoa(want) {
			t.Errorf("%s: census row %q says %d, but %s prints %q: the command has to reproduce the figure it sits beside",
				rigorModesRecon, line, want, cmd, got)
		}
	}
	if rows == 0 {
		t.Errorf("%s: the census carries no rows", rigorModesRecon)
	}
	if commands != rows {
		t.Errorf("%s: %d of %d census rows carry a command, want all of them", rigorModesRecon, commands, rows)
	}
	if checked == 0 {
		t.Errorf("%s: no census command was run: the figures are the point", rigorModesRecon)
	}
}

// rigorModesCommand is the shell command inside a census row's code span, with
// the table's own escaping undone: a markdown cell cannot carry a bare `|`, so
// a command that pipes writes `\|`, and what runs is the `|` a reader copies.
func rigorModesCommand(cell string) string {
	cell = strings.TrimSpace(cell)
	i := strings.Index(cell, "`")
	j := strings.LastIndex(cell, "`")
	if i < 0 || j <= i {
		return ""
	}
	return strings.ReplaceAll(cell[i+1:j], `\|`, "|")
}

// rigorModesCitedTaskRE matches a citation of one task, as the documents write
// it ("plan T3.2", "T8.1's"). It is deliberately the whole id and not a range:
// a range ("T3.1-T3.5") is checked endpoint by endpoint, because a range whose
// far end does not exist is the same broken citation.
var rigorModesCitedTaskRE = regexp.MustCompile(`T([0-9]+)\.([0-9]+)`)

// TestDocsRigorModesEveryCitedTaskExists: every `T<n>.<m>` the reconciliation
// and the index cite resolves to a task heading in the plan. This is the guard
// the review asked for and the folder did not have: a document that cites a
// task that does not exist is a document whose reader cannot find what it
// points at, and a plan is edited by renumbering far more often than by
// rewriting. It is a check over the *citation*, not over the prose around it, so
// a reworded sentence cannot hide a dangling reference.
func TestDocsRigorModesEveryCitedTaskExists(t *testing.T) {
	plan := readDoc(t, rigorModesPlan)
	exists := map[string]bool{}
	for _, m := range rigorModesTaskRE.FindAllStringSubmatch(plan, -1) {
		exists[m[1]] = true
	}
	if len(exists) == 0 {
		t.Fatalf("%s lists no tasks to cite", rigorModesPlan)
	}

	cited := 0
	for _, path := range []string{rigorModesRecon, rigorModesReadme, rigorModesPlan} {
		doc := readDoc(t, path)
		for _, m := range rigorModesCitedTaskRE.FindAllStringSubmatch(doc, -1) {
			id := "T" + m[1] + "." + m[2]
			cited++
			if !exists[id] {
				t.Errorf("%s cites %s, which is no task in %s: a citation has to resolve to a heading", path, id, rigorModesPlan)
			}
		}
	}
	if cited == 0 {
		t.Fatalf("no document cites a task: the plan and the reconciliation are meant to point at each other")
	}
}

// rigorModesBlockRangeRE matches a document stating a whole block's range as
// this folder writes one: the prefix, a 1, the dash, and the last number, with
// the prefix repeated or not — "F1–F41" and "D1–D25". A range is a summary
// claim, and it is the one number in these documents a reader takes without
// counting, so a range left behind by an edit ("D1–D19" after D20 landed) is a
// claim the folder no longer has.
var rigorModesBlockRangeRE = regexp.MustCompile(`([FDOU])1 ?[–-] ?(?:[FDOU])?([0-9]+)`)

// rigorModesAnyNumberRE matches one citation of a block's number anywhere it is
// written ("F19", "D7", "U10", "O3"), which is how the documents point at each
// other.
var rigorModesAnyNumberRE = regexp.MustCompile(`([FDOU])([0-9]+)`)

// rigorModesForeignNumber is a number above this folder's own blocks that a
// document here may still cite, because it belongs to a register the document
// names: the coordinator's allocation (D192), the free range its row states
// (D193), the two sibling folders' D-blocks (docs/windows-cli/'s D1–D16 and
// docs/setup-wizard/'s D1–D12), and the block the folder's own O7 reads out of
// the register — docs/mobile/voice-session/'s D201–D220, the numbers around them
// that voice-session left alone (D200, written nowhere here), and the next free
// block at this folder's base (D221), which O7 recommends by name.
//
// Each is allowed only where the document names its owner, because "D16" beside
// no name is a citation of a decision this folder does not have. The register's
// figures are quoted with the anchor they were read at (docs/coordinator/README.md's
// row says "at `c6009b5f`"), which is why the block above is read from this
// folder's base commit rather than trusted as current: at `1d325928` the highest
// number written anywhere in docs/ is D220, not D192.
var rigorModesForeignNumber = []struct {
	prefix, owner string
	end           int
}{
	{"D", "owner's allocation", 192},
	{"D", "D193 onward", 193},
	{"D", "docs/windows-cli/", 16},
	{"D", "docs/setup-wizard/", 12},
	{"D", "docs/mobile/voice-session/", 200},
	{"D", "docs/mobile/voice-session/", 201},
	{"D", "docs/mobile/voice-session/", 219},
	{"D", "docs/mobile/voice-session/", 220},
	{"D", "next free block", 221},
}

// TestDocsRigorModesEveryBlockRangeIsCurrent: every block range a document in
// this folder states either ends where the block does (counted from the
// reconciliation rather than trusted), or is explained: a *part* of a block cited
// beside a section reference (§1.1's table cites "D1–D4" as the decisions behind
// the score), or a range this folder quotes from somewhere else, whose owner the
// document names. Everything else is a claim about how far a block reaches that
// the block disagrees with — the shape an edit leaves behind, because a block is
// extended by appending to it and the table that summarises it is elsewhere.
//
// It also holds that no document cites a number a block does not have: a citation
// of "D26" is a pointer to a decision nobody wrote.
func TestDocsRigorModesEveryBlockRangeIsCurrent(t *testing.T) {
	recon := readDoc(t, rigorModesRecon)
	counts := map[string]int{
		"F": len(rigorModesNumbered(t, rigorModesRecon, recon, "findings", rigorModesFindingRE)),
		"D": len(rigorModesNumbered(t, rigorModesRecon, recon, "decisions", rigorModesDecisionRE)),
		"O": len(rigorModesNumbered(t, rigorModesRecon, recon, "items", rigorModesItemRE)),
		"U": len(rigorModesNumbered(t, rigorModesRecon, recon, "UNVERIFIED rows", rigorModesUnverifRE)),
	}
	foreign := func(prefix string, n int, doc string) bool {
		// The owner is looked for across the document's own line breaks: a
		// markdown paragraph wraps, and "the next free\nblock" still names the
		// register the number comes from.
		doc = strings.ReplaceAll(doc, "\n", " ")
		for _, f := range rigorModesForeignNumber {
			if f.prefix == prefix && f.end == n && strings.Contains(doc, f.owner) {
				return true
			}
		}
		return false
	}

	ranges, cites := 0, 0
	for _, path := range []string{rigorModesReadme, rigorModesRecon, rigorModesPlan} {
		doc := readDoc(t, path)
		for i, line := range strings.Split(doc, "\n") {
			for _, loc := range rigorModesBlockRangeRE.FindAllStringSubmatchIndex(line, -1) {
				m := rigorModesBlockRangeRE.FindStringSubmatch(line[loc[0]:loc[1]])
				end, err := strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("%s: unreadable range end %q", path, m[2])
				}
				ranges++
				// A *part* of a block is cited beside the section it belongs
				// to ("§5, D1–D4"): the § has to come before the range, or a
				// line that merely mentions a section somewhere would excuse
				// any range on it.
				if end == counts[m[1]] || foreign(m[1], end, doc) || strings.Contains(line[:loc[0]], "§") {
					continue
				}
				t.Errorf("%s line %d states %s1–%s%d, and the %s block reaches %d: a range has to end where the block does, cite a part of it beside a section reference, or name whose register it quotes",
					path, i+1, m[1], m[1], end, m[1], counts[m[1]])
			}
			for _, m := range rigorModesAnyNumberRE.FindAllStringSubmatch(line, -1) {
				n, err := strconv.Atoi(m[2])
				if err != nil {
					t.Fatalf("%s: unreadable citation %q", path, m[2])
				}
				cites++
				if n <= counts[m[1]] || foreign(m[1], n, doc) {
					continue
				}
				t.Errorf("%s line %d cites %s%d, and the %s block reaches %d: a citation has to resolve to a number the block has",
					path, i+1, m[1], n, m[1], counts[m[1]])
			}
		}
	}
	if ranges == 0 || cites == 0 {
		t.Fatalf("no document states a block range or cites a block number: the guard's own subjects are missing")
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
