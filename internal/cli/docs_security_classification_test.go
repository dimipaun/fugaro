package cli

// Security classification (docs/security-classification/): the folder in which
// the owner asks that each project say what counts as security-sensitive for
// its own situation -- a project-level component and an optional repo-level
// one, over the engine's floor -- and the plan (SC1-SC11) that would build it:
// the floor and the union merge, `fugaro security paths`, doctor's line, the
// runner's escalation and the two config keys.
//
// SC2 starts this file, and what it holds is what the folder claims about
// itself rather than about the code it plans: the ask is the owner's words
// verbatim, the status line names what has landed and leaves the decisions
// open, the twenty decisions D1500–D1519 are still recommendations with an
// empty owner's-answer cell, the number block sits above every claim the folder
// read, and every task names its files, its red step and its reviewer tier.
// SC1's and SC4's code has landed on the tree since the folder was written (the
// plan's own first tasks), and this file pins none of its behavior -- the
// engine's package is pinned by its own tests, and what would otherwise go
// unchecked here is the folder: an ask reworded out of the owner's words, a
// status line still reading as the brief asked for it while the tree has moved,
// a decision cell that quietly acquired an answer, a number taken beside one
// already claimed, or a task that lost the files an agent picks it up by. SC6
// adds one more: the live register's section for this folder's own check names
// only commands the tree carries, because a section that writes its own steps
// off as unrunnable sends a reader to the half of the check that spends money.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const secClassDir = "docs/security-classification/"

// secClassDocs are the four documents the folder ships, in the order README.md
// lists them. The list is written here rather than derived from the directory
// so that a document that disappears -- or a fifth that arrives without the
// index naming it -- fails instead of quietly shrinking the folder.
var secClassDocs = []string{"README.md", "spec.md", "reconciliation.md", "implementation-plan.md"}

// secClassAsQuote strips the `> ` marker markdown puts in front of every line
// of a blockquote. The ask is one blockquote, so its sentences wrap with a `> `
// in the middle of them, and a phrase spanning a wrap is a phrase a plain
// flattenDoc cannot see.
func secClassAsQuote(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimPrefix(line, "> "), ">")
	}
	return strings.Join(lines, "\n")
}

// secClassAskGroups are the seven question groups the brief's ask carries, each
// by the words it opens with. They are pinned by their opening words rather
// than by a count of "(" markers, because seven is what reconciliation.md's §1
// headline table answers one for one: an ask reworded down to six is a question
// the table still answers and the brief no longer asks.
var secClassAskGroups = []string{
	"(1) the shape: a project-layer key",
	"(2) WHO consumes the answer and WHEN: the runner",
	"(3) how an owner edits it:",
	"(4) what a repo-level key does when the project has none and vice versa",
	"(5) the interaction with the needs_owner work (OD1) and the skills (OD2)",
	"(6) the smallest useful first slice and what ships dormant",
	"(7) migration, schemas and the closed decoders that must learn the new field",
}

// TestDocsSecurityClassificationIsWritten: the folder is the four documents its
// index names, one of them per thing a reader comes for -- what was asked, what
// the tree says back, and what would be built -- and the index says whose ask
// it is. A folder pin starts here: the other three tests below read documents
// this one holds in place.
func TestDocsSecurityClassificationIsWritten(t *testing.T) {
	index := flattenDoc(readDoc(t, secClassDir+"README.md"))
	for _, name := range secClassDocs {
		if _, err := os.Stat("../../" + secClassDir + name); err != nil {
			t.Errorf("%s%s is not in the tree: %v", secClassDir, name, err)
			continue
		}
		// The index's table lists the other three; the index does not link
		// itself, and a reader holding it has already found it.
		if name != "README.md" && !strings.Contains(index, "("+name+")") {
			t.Errorf("%s does not link %s: the index's table is where a reader finds the folder's documents", secClassDir+"README.md", name)
		}
	}
	for _, want := range []string{
		"**Asked by:** the owner, 2026-11-07, as task **SC0**",
		"**Spec owner:** the owner, 2026-11-07.",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("%s lacks %q: whose ask this is and who owns the spec are the two facts the rest of the folder is read against", secClassDir+"README.md", want)
		}
	}
}

// secClassStatusParagraph is the index's status line: the one paragraph that
// opens with **Status:**, which the brief asked for and spec.md keeps in its
// own words. It is the paragraph in the folder that goes stale on its own,
// because it is a claim about the tree rather than about the ask.
func secClassStatusParagraph(t *testing.T) string {
	t.Helper()
	raw := readDoc(t, secClassDir+"README.md")
	for _, para := range strings.Split(raw, "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(para), "**Status:") {
			return flattenDoc(para)
		}
	}
	t.Fatalf("%s carries no paragraph opening with %q: the brief asks for a status line, and it is the paragraph a reader checks against the tree", secClassDir+"README.md", "**Status:")
	return ""
}

// TestDocsSecurityClassificationStatusMatchesTheTree: the index's status line
// is read against the tree, not against the brief. The brief's own words for
// it -- "plan only; nothing implemented; no task approved" -- are kept where
// they belong, inside spec.md (TestDocsSecurityClassificationSpecIsTheAskVerbatim),
// and while the plan's tasks are landing the line may not still read that way:
// SC1's package is in the tree, so a line that says nothing is implemented is a
// folder contradicting the code it cites. What it must carry is the other half,
// the one that does not move when a task lands -- that no decision is answered,
// which is what the twenty cells in §8 are.
func TestDocsSecurityClassificationStatusMatchesTheTree(t *testing.T) {
	status := secClassStatusParagraph(t)
	if _, err := os.Stat("../../internal/orchestrator/sensitivity/floor.go"); err == nil {
		for _, stale := range []string{"plan only", "nothing implemented", "no task approved"} {
			if strings.Contains(status, stale) {
				t.Errorf("%s's status line still says %q while SC1's package is in the tree: the line is a claim about the tree, and the brief's own words for it are kept in spec.md", secClassDir+"README.md", stale)
			}
		}
	}
	for _, want := range []string{"SC1", "SC2", "empty owner's-answer cell"} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's status line does not carry %q: it names what has landed and leaves the twenty decisions unanswered, and a line that drops either reads as a folder with nothing in the tree or one whose decisions are settled", secClassDir+"README.md", want)
		}
	}
}

// TestDocsSecurityClassificationSpecIsTheAskVerbatim: spec.md is the owner's
// ask, and the folder's own rule is that it stays his words -- README.md calls
// it "his words ... verbatim", and reconciliation.md keeps its answers outside
// it. So this reads the file the way that claim is broken: the ask is a
// blockquote attributed to him by date, the seven question groups are all in it
// with the words they open with, and the brief's own constraints (DOCS ONLY,
// the status line it asks for, "Do not decide anything silently") are inside
// the ask rather than paraphrased above it.
func TestDocsSecurityClassificationSpecIsTheAskVerbatim(t *testing.T) {
	raw := readDoc(t, secClassDir+"spec.md")
	ask := flattenDoc(secClassAsQuote(raw))
	if !strings.HasPrefix(strings.TrimSpace(raw), "# ") || !strings.Contains(raw, "The ask, verbatim:") {
		t.Errorf("%s no longer says %q: the file's whole claim is that the block under that line is the ask, not a description of it", secClassDir+"spec.md", "The ask, verbatim:")
	}
	if !strings.Contains(ask, "Owner 2026-11-07:") {
		t.Errorf("%s does not attribute the ask to the owner and the date: an ask quoted without its author is a paragraph a reader cannot weigh", secClassDir+"spec.md")
	}
	for _, group := range secClassAskGroups {
		if !strings.Contains(ask, group) {
			t.Errorf("%s's ask does not carry the question group opening %q: the seven groups are what the reconciliation answers one for one, and a group reworded away is a question the brief no longer asks", secClassDir+"spec.md", group)
		}
	}
	for _, want := range []string{
		"is the spec's owner: the block below is his, verbatim, and it is the only text in this folder he wrote",
		"we have to find a way to allow each project using Fugaro to determine what counts as security sensitive for their own situation",
		"It should have a project level component, and an optional repo-level one.",
		// The brief's own instructions, which are the run's and not the
		// design's (spec.md says so itself): a spec that dropped them would
		// read as a product ask with no fence around it.
		"DOCS ONLY, no code",
		"status line: 'plan only; nothing implemented; no task approved'",
		"Do not decide anything silently",
		"every owner decision is a D row with a recommendation",
	} {
		if !strings.Contains(ask, want) {
			t.Errorf("%s lacks %q: the file is the brief verbatim, so a fact the brief states and the file does not is the owner's ask edited after the fact", secClassDir+"spec.md", want)
		}
	}
}

// secClassDecisionRowRE matches one row of reconciliation.md §8's decisions
// table: its first cell is a decision number in bold. It is anchored on the
// closing pipe, so a number cited inside another row's prose is not read as a
// row of its own.
var secClassDecisionRowRE = regexp.MustCompile(`(?m)^\| \*\*D([0-9]{4})\*\* \|`)

// secClassDecisions are the folder's decision numbers in the order §8 writes
// them. The list is here rather than derived so that a row that disappears, or
// two that swap places, is a failure and not a smaller table.
var secClassDecisions = []string{
	"1500", "1501", "1502", "1503", "1504", "1505", "1506", "1507", "1508", "1509",
	"1510", "1511", "1512", "1513", "1514", "1515", "1516", "1517", "1518", "1519",
}

// TestDocsSecurityClassificationDecisionsAreUnanswered: the twenty decisions
// exist, in order, and every one of their owner's-answer cells is still empty.
// This is the folder's load-bearing claim about itself -- README.md's status
// line and §8's own preamble both say no decision is answered -- and it is the
// one a later edit breaks silently: filling a cell looks like an improvement
// and turns a recommendation into a ruling the owner never gave.
//
// The cells are read from the last cell of each row rather than by a fixed
// column count: D1512's recommendation carries a code span with pipe
// characters in it (`floor|project|repo`), so the row splits into more cells
// than the table has columns, and the owner's-answer cell is the last one
// either way.
func TestDocsSecurityClassificationDecisionsAreUnanswered(t *testing.T) {
	const path = secClassDir + "reconciliation.md"
	raw := readDoc(t, path)
	if !strings.Contains(raw, "## 8. Decisions D1500–D1519") {
		t.Fatalf("%s has no %q section: §8 is where the folder's decisions and their owner's-answer cells live", path, "## 8. Decisions D1500–D1519")
	}
	if !strings.Contains(flattenDoc(raw), "| # | Question | Recommendation (and why) | Alternative | Its cost | Owner's answer |") {
		t.Errorf("%s' decisions table no longer carries its six-column header: the last column is the owner's answer, and a row read without it is a recommendation mistaken for a ruling", path)
	}

	var rows []string
	for _, line := range strings.Split(raw, "\n") {
		if m := secClassDecisionRowRE.FindStringSubmatch(line); m != nil {
			rows = append(rows, m[1])
		}
	}
	// Fatal rather than reported: the loop below reads the table by position, so
	// a table of the wrong length is the precondition of every check after it.
	if len(rows) != len(secClassDecisions) {
		t.Fatalf("%s writes %d decision rows and the folder's block is %d: D1500–D1519 is the block this work took, and a row outside it is a number nobody allocated", path, len(rows), len(secClassDecisions))
	}
	for i, want := range secClassDecisions {
		if rows[i] != want {
			t.Errorf("%s' decision rows are not D1500–D1519 in order: row %d is %q, want D%s", path, i+1, rows[i], want)
			break
		}
	}
	empty := 0
	for _, line := range strings.Split(raw, "\n") {
		if secClassDecisionRowRE.FindStringSubmatch(line) == nil {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) < 6 {
			t.Errorf("%s: the row %q has %d cells, want at least 6 (`# | Question | Recommendation (and why) | Alternative | Its cost | Owner's answer`)", path, strings.TrimSpace(line[:min(len(line), 40)]), len(cells))
			continue
		}
		if answer := strings.TrimSpace(cells[len(cells)-1]); answer != "*(empty)*" {
			t.Errorf("%s: the owner's-answer cell of the row %.30q... reads %q, and this folder's status is that no decision is answered: a filled cell is a ruling the owner did not give", path, strings.TrimSpace(line), answer)
			continue
		}
		empty++
	}
	if empty != len(secClassDecisions) {
		t.Errorf("%s carries %d empty owner's-answer cells and the block has %d decisions", path, empty, len(secClassDecisions))
	}
	if !strings.Contains(flattenDoc(raw), "Each row is a question the ask raises, **a recommendation**, the alternative, that alternative's cost, and **an empty owner's-answer cell**") {
		t.Errorf("%s no longer states what a row is: the sentence is the folder's own description of the table above", path)
	}
}

var (
	// secClassBlockRE matches the block the index's numbers table states,
	// "**D1500–D1519**". The second number carries its own prefix, which is
	// optional here the way it is in the rigor-modes folder's own range guard.
	secClassBlockRE = regexp.MustCompile(`\*\*D([0-9]{4})[–-](?:D)?([0-9]{4})\*\*`)
	// secClassReservedRE matches §9's reading of the reserved ceiling the block
	// was measured against.
	secClassReservedRE = regexp.MustCompile(`the reserved ceiling is D([0-9]{4})`)
	// secClassCheckRE matches the live check §9 claims, and the highest number
	// §9 read as cited on `main` when it claimed it.
	secClassCheckRE = regexp.MustCompile(`This work claims \*\*Check ([0-9]{3})\*\*`)
	secClassCitedRE = regexp.MustCompile("the highest number \\*\\*cited\\*\\* on `main` is \\*\\*([0-9]{3})\\*\\*")
	// secClassD15xxRE matches any decision number in the 15xx range, which is
	// the range this work's own block sits in.
	secClassD15xxRE = regexp.MustCompile(`\bD(15[0-9]{2})\b`)
)

// TestDocsSecurityClassificationNumberBlockIsAboveEveryClaim: the numbers the
// folder takes are above the ones it read, and it takes nothing beside them.
// §9 is the whole of the claim -- "370 is therefore the first number above
// every claim and every reserve on `main`" -- and the index states it for a
// reader who lands there first, so both are read and the arithmetic is checked
// rather than restated: the block starts immediately above the reserved ceiling
// §9 read, and the check is above the highest number §9 read as cited.
//
// The reading is of the letters and not of the tree, on purpose. Later folders
// have taken higher blocks since (docs/roles-scopes holds D1580–D1599 and
// Checks 400–409), so a test that re-derived "the highest number anywhere" and
// compared it to 370 would fail on every later folder rather than on this one;
// what stays this folder's claim is that its block was above every claim at the
// reading it names, and what is checkable here is that no number in the range
// wanders outside the block it says it reserved nothing above.
func TestDocsSecurityClassificationNumberBlockIsAboveEveryClaim(t *testing.T) {
	index := flattenDoc(readDoc(t, secClassDir+"README.md"))
	recon := flattenDoc(readDoc(t, secClassDir+"reconciliation.md"))

	for _, want := range []struct{ path, doc, phrase string }{
		{secClassDir + "README.md", index, "**this work's block**, taken above every number written or reserved on `main` and **reserving nothing above it**"},
		{secClassDir + "README.md", index, "**Verified free**: no `D15xx` appears anywhere on `main` or in any local ref"},
		{secClassDir + "README.md", index, "every one of them has an empty owner's-answer cell"},
		{secClassDir + "README.md", index, "with 371–379 available"},
		{secClassDir + "reconciliation.md", recon, "This work takes **D1500–D1519**, and reserves nothing above it."},
		{secClassDir + "reconciliation.md", recon, "The highest D number actually written on `main` is D1489"},
		{secClassDir + "reconciliation.md", recon, "D1490–D1499 appear only in that work's own reserve row"},
		{secClassDir + "reconciliation.md", recon, "The highest written section is **291**"},
		{secClassDir + "reconciliation.md", recon, "the highest number **cited** on `main` is **360**"},
		{secClassDir + "reconciliation.md", recon, "370 is therefore the first number above every claim and every reserve on `main`"},
	} {
		if !strings.Contains(want.doc, want.phrase) {
			t.Errorf("%s lacks %q: the block is a claim about other works' numbers, and the reading that justifies it is the whole of the evidence a reader can re-run", want.path, want.phrase)
		}
	}

	m := secClassBlockRE.FindStringSubmatch(index)
	if m == nil {
		t.Fatalf("%s states no `**Dnnnn–Dnnnn**` block in its numbers table: a work that cannot say which block it took cannot be checked against its siblings'", secClassDir+"README.md")
	}
	start, _ := strconv.Atoi(m[1])
	end, _ := strconv.Atoi(m[2])
	if start != 1500 || end != 1519 {
		t.Errorf("%s states the block D%d–D%d, and this folder's decisions are D1500–D1519: the index's row and §8's table have to be the same block", secClassDir+"README.md", start, end)
	}
	rm := secClassReservedRE.FindStringSubmatch(recon)
	if rm == nil {
		t.Fatalf("%s no longer states the reserved ceiling its block was measured against", secClassDir+"reconciliation.md")
	}
	ceiling, _ := strconv.Atoi(rm[1])
	if start != ceiling+1 {
		t.Errorf("%s takes the block from D%d and §9 reads the reserved ceiling as D%d: the block is taken above the reserve rather than inside it, which is what the sentence beside the reading claims", secClassDir+"README.md", start, ceiling)
	}

	cm := secClassCheckRE.FindStringSubmatch(recon)
	im := secClassCitedRE.FindStringSubmatch(recon)
	if cm == nil || im == nil {
		t.Fatalf("%s no longer states the check it claims (%v) or the highest number it read as cited (%v)", secClassDir+"reconciliation.md", cm != nil, im != nil)
	}
	claim, _ := strconv.Atoi(cm[1])
	cited, _ := strconv.Atoi(im[1])
	if claim <= cited {
		t.Errorf("%s claims Check %d while reading Check %d as the highest cited on `main`: a claimed number at or below a cited one is a number two works both hold", secClassDir+"reconciliation.md", claim, cited)
	}

	// "Reserving nothing above it", read out of the folder rather than trusted:
	// a D15xx number outside D1500–D1519 is a decision this work took without
	// saying so, which is the one thing a reader of a number block has to be
	// able to rule out.
	for _, name := range secClassDocs {
		path := secClassDir + name
		for _, stray := range secClassD15xxRE.FindAllStringSubmatch(readDoc(t, path), -1) {
			n, _ := strconv.Atoi(stray[1])
			if n < start || n > end {
				t.Errorf("%s cites D%d, outside the block D%d–D%d the folder says it took and reserved nothing above: a number this work did not allocate is a number whose owner nobody can find", path, n, start, end)
			}
		}
	}
}

// secClassTaskHeadingRE matches the heading that opens a task in the plan's §4,
// "### SC7. The repository key, `security` (F3, ...)". The block it opens runs
// to the next heading, which is how the three labels below are read as that
// task's own rather than as the next task's.
var secClassTaskHeadingRE = regexp.MustCompile(`(?m)^### (SC[0-9]+)\. `)

// secClassPathRE matches a backticked repository path, which is how every task
// names the files it touches. It requires the slash, so a backticked symbol
// (`Match`, `layerTopKeys`) is not read as a file.
var secClassPathRE = regexp.MustCompile("`[A-Za-z0-9_][A-Za-z0-9_./-]*/[A-Za-z0-9_./-]+`")

// secClassTasks are the plan's tasks in the order §4 writes them. The list is
// here rather than derived so that a task dropped, or two swapped, is a failure
// and not a shorter plan -- §1's levels and §5's critical path both count on
// these eleven.
var secClassTasks = []string{"SC1", "SC2", "SC3", "SC4", "SC5", "SC6", "SC7", "SC8", "SC9", "SC10", "SC11"}

// TestDocsSecurityClassificationPlanStatesEveryTasksFilesRedStepAndTier: every
// task states the three things an agent picks one up by -- the files it touches,
// the test that fails first, and the tier that reviews it -- and names at least
// one file, because a "Files:" label with no path under it is a task whose size
// nobody can check. This is the folder's own rule rather than a preference: its
// header says every task names at most six paths, and the shared-files table
// that proves no level has two writers of one file is read off exactly these
// lists.
func TestDocsSecurityClassificationPlanStatesEveryTasksFilesRedStepAndTier(t *testing.T) {
	path := secClassDir + "implementation-plan.md"
	lines := strings.Split(readDoc(t, path), "\n")
	type task struct {
		id    string
		block string
	}
	var tasks []task
	for i, line := range lines {
		m := secClassTaskHeadingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") || strings.HasPrefix(lines[j], "### ") {
				end = j
				break
			}
		}
		tasks = append(tasks, task{m[1], strings.Join(lines[i:end], "\n")})
	}
	if len(tasks) != len(secClassTasks) {
		t.Fatalf("%s writes %d `### SCn.` task headings and the plan is %d tasks: §1's levels, §5's critical path and the shared-files table are all counts of these", path, len(tasks), len(secClassTasks))
	}
	for i, want := range secClassTasks {
		if tasks[i].id != want {
			t.Fatalf("%s's %dth task is %s, want %s: the tasks are numbered in the order the plan builds them", path, i+1, tasks[i].id, want)
		}
	}
	for _, tk := range tasks {
		block := flattenDoc(tk.block)
		for _, label := range []string{"**Files:**", "**Red step:**", "**Reviewer tier:**"} {
			if !strings.Contains(block, label) {
				t.Errorf("%s's %s states no %s: the files it touches, the test that fails first and the tier that reviews it are what the next agent starts from", path, tk.id, label)
			}
		}
		if !secClassPathRE.MatchString(tk.block) {
			t.Errorf("%s's %s names no repository path: a task whose file list is empty is a task whose size and whose shared-file conflicts nobody can read", path, tk.id)
		}
		for _, label := range []string{"**Depends on:**", "**Commit:**"} {
			if !strings.Contains(block, label) {
				t.Errorf("%s's %s states no %s: the plan's own header makes all of these part of a task", path, tk.id, label)
			}
		}
	}
}

// TestDocsSecurityClassificationCensusRowMovedForItsPin: docs/rigor-modes/
// reconciliation.md's row counts the folders that ship one of these pins, and
// its own text says a folder that writes one moves the row in its own commit --
// "a reader who finds the row stale has found a merge that did not". This
// folder writes one, so the move is checked here as well as by
// TestDocsRigorModesCensusStatesItsCommands and the three pins that re-check
// the same row (docs_config_management_test.go, docs_repo_image_removal_test.go
// and docs_managed_agent_converge_test.go).
func TestDocsSecurityClassificationCensusRowMovedForItsPin(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	// The count the row states, read out of the row itself rather than assumed:
	// the columns are `| construct | count | command |`.
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	if stated := strings.TrimSpace(cols[2]); stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files: the row is a count a reader re-runs, so a folder that added a pin moves it", path, stated, len(pins))
	}
	// Read flattened rather than against the row's own line breaks: the
	// sentence wraps wherever the edit was made, and a guard that only knows
	// the unwrapped spelling misses the amendment the moment someone re-wraps
	// the paragraph it sits in.
	if !strings.Contains(flattenDoc(doc), "the security-classification folder's pin, moved the table's row") {
		t.Errorf("%s does not carry the amendment sentence naming this folder's pin: the row's own text says a folder that writes one moves the row in its own commit, and an amendment the row does not record is the merge that did not", path)
	}
}

// secClassCheck370Section returns Check 370's section of the live register, cut
// at the next `## ` heading so a check written after it cannot widen what is
// read. The number is the plan's §6, which leaves the register's row and this
// section to SC6, so the guard for what the section tells a reader to run lives
// here with the rest of what the folder claims.
func secClassCheck370Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check 370:")
	if i < 0 {
		t.Fatalf("%s has no `## Check 370:` section: the register's row points at steps that have to be there", liveChecksDoc)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// secClassCheck370Commands are the `fugaro ...` invocations the section's fenced
// blocks carry, each cut back to the command words a shell resolves -- those
// before the first flag or `<placeholder>` -- and de-duplicated in the order
// they appear. Reading the fences rather than the prose is the point: the prose
// names commands in backticks, and only a fenced line is a step a reader runs.
func secClassCheck370Commands(section string) [][]string {
	var cmds [][]string
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "fugaro" {
			continue
		}
		var words []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") || strings.HasPrefix(f, "<") {
				break
			}
			words = append(words, f)
		}
		if len(words) == 0 {
			continue
		}
		if key := strings.Join(words, " "); !seen[key] {
			seen[key] = true
			cmds = append(cmds, words)
		}
	}
	return cmds
}

// TestDocsSecurityClassificationCheck370NamesOnlyCommandsTheTreeCarries guards
// the drift the merge that brought SC3 in produced. Check 370 was written while
// `fugaro security paths` was a task on the plan and not yet a command; it said
// so in three places, and it went on saying so after the command landed on the
// tree -- which tells a reader that the free, offline half of the check cannot
// be run at all and leaves only the half that spends money. So: every command
// the section's blocks name has to resolve in this tree's own command tree, all
// the way down to its last word, and the reader command has to be one of them.
func TestDocsSecurityClassificationCheck370NamesOnlyCommandsTheTreeCarries(t *testing.T) {
	section := secClassCheck370Section(t, readDoc(t, liveChecksDoc))
	cmds := secClassCheck370Commands(section)
	if len(cmds) == 0 {
		t.Fatalf("Check 370's blocks name no `fugaro` command, so this case proves nothing: the section is the steps a person runs")
	}
	root := NewRootCmd()
	reader := false
	for _, c := range cmds {
		// The leftover args are the telling half. Find answers a word it cannot
		// descend through by handing back the command it ran out of names at,
		// with no error: `["security", "pathes"]` returns `security` and
		// `["pathes"]`. So `err != nil || sub == root` alone passes for a
		// renamed or dropped subcommand -- the drift this case is here for,
		// since the command it names is two words deep.
		if sub, rest, err := root.Find(c); err != nil || sub == root || len(rest) > 0 {
			t.Errorf("Check 370 names `fugaro %s`, which this tree does not carry: a step a reader cannot run is a part of the check written off as unrunnable", strings.Join(c, " "))
		}
		if strings.Join(c, " ") == "security paths" {
			reader = true
		}
	}
	if !reader {
		t.Errorf("Check 370 does not name `fugaro security paths` among the steps it tells a reader to run: parts 1 and 3 of the check are that command, and a section that drops it leaves only the part that spends money")
	}
}
