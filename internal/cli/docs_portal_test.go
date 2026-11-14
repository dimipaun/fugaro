package cli

// The portal docs folder, pinned.
//
// docs/portal/ is a design folder: spec.md (the owner's spec, verbatim),
// reconciliation.md (findings F1–F43 against `origin/main`, the decisions the
// owner answered on 2026-11-12 with D2207 amended on 2026-11-13, the open items
// and the UNVERIFIED list),
// implementation-plan.md (slices P0–P9, the parallel levels, the critical path,
// the shared-files table and the checks the plan takes) and README.md, the
// index. It writes no command and no flag, so nothing here pins one: what it
// pins is the arithmetic a reader plans from, which is the half of these
// documents that rots without anyone noticing.
//
//  * TestDocsPortalReservesItsNumberRanges -- the two blocks the folder claims
//    for itself (D2180–D2229, Checks 570–579), stated in the documents a reader
//    meets them in, and the checks table inside the block: the numbers written
//    and the tail reserved are the block and nothing else, in order, with no
//    number left out.
//  * TestDocsPortalDecisionRowsCarryAnAnswer -- every decision row is one of
//    D2180–D2207, exactly once, and carries the owner's answer with its date.
//    The folder's whole claim is that the questions are answered; a row with an
//    empty answer cell is a question the plan is still waiting on, and it reads
//    as settled to anyone scanning the table.
//  * TestDocsPortalLevelsTableMatchesTheDependsOnLines -- the level table is the
//    plan's schedule, so it is read against the Depends-on line of every task it
//    places: a level strictly after every task that task waits on, every task in
//    exactly one level, no level skipped.
//  * TestDocsPortalPinMovesTheCensusRow -- writing this file is itself one more
//    `internal/cli/docs_*_test.go`, and docs/rigor-modes/reconciliation.md's row
//    counts them.
//
// Mutations (each verified, run and restored): renumber the last decision row to
// D2230 (fails the block half); blank D2194's answer cell (fails the answer
// half); move P9.3's token from level 7 to level 3 (fails the level half, since
// P5.1 sits at 6); write "577–579 are reserved." (fails the checks half, which
// leaves 576 unclaimed); drop this file's name from the rigor-modes amendment
// sentence (fails the census half).

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const portalDir = "docs/portal/"

// The blocks the folder takes, and the part of each it says is used. They are
// constants rather than numbers read out of the documents so that a renumbering
// has to be written down twice -- in the document and in this pin -- which is
// what makes it a decision instead of a drift.
const (
	portalDecisionLow       = 2180
	portalDecisionHigh      = 2229
	portalDecisionsUsedHigh = 2207
	portalCheckLow          = 570
	portalCheckHigh         = 579
)

// portalDecisionRowRE matches a row of the reconciliation's decisions table,
// `| D2180 | question | recommendation | alternative | answer |`.
var portalDecisionRowRE = regexp.MustCompile(`^\| (D[0-9]{4}) \|`)

// portalTaskHeadingRE matches the plan's task heading, `- **P1.0 The API …**`.
// The bullet and not a section is the unit, because that is where the plan
// writes a task's own Depends-on line.
var portalTaskHeadingRE = regexp.MustCompile(`(?m)^- \*\*(P[0-9]\.[0-9]+)\b`)

// portalTaskTokenRE finds a plan task token ("P3.6", "P9.1") wherever a task is
// named by one: the level table's cells and the Depends-on lines both spell
// them this way, and it is the same spelling on both sides that lets one be
// read against the other.
var portalTaskTokenRE = regexp.MustCompile(`\bP[0-9]\.[0-9]+\b`)

// portalLevelRowRE matches a row of the plan's level table, `| 3 | P1.7, P3.3 |`.
// The plan's other tables open a row with a number too (the checks table's
// `| 570 |`), which is why each is read inside the table its header bounds.
var portalLevelRowRE = regexp.MustCompile(`^\| ([0-9]+) \| (.+) \|$`)

// portalCheckRowRE matches a row of the plan's checks table, `| 570 | … |`.
var portalCheckRowRE = regexp.MustCompile(`^\| ([0-9]+) \|`)

// portalReservedRE reads the plan's "576–579 are reserved." sentence.
var portalReservedRE = regexp.MustCompile(`([0-9]{3,4})–([0-9]{3,4}) are reserved`)

// portalTable returns the data rows of the markdown table whose header line is
// header, in the order the document writes them, with the header and its rule
// dropped. Every table in the folder ends at a blank line, which is where this
// stops: reading on to the next `|` would swallow whatever prose follows, and a
// checks row read as a level row is a schedule made of the wrong numbers.
func portalTable(t *testing.T, path, doc, header string) []string {
	t.Helper()
	i := strings.Index(doc, header)
	if i < 0 {
		t.Fatalf("%s has no %q table: update this test's markers", path, header)
	}
	rest := doc[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	lines := strings.Split(rest, "\n")
	if len(lines) < 3 {
		t.Fatalf("%s's %q table has %d lines, too few for a header, a rule and a row: update this test's markers", path, header, len(lines))
	}
	return lines[2:]
}

// portalDecision is one row of the reconciliation's decisions table.
type portalDecision struct {
	number  string
	columns []string // the five cells, trimmed, in the table's order
}

// portalDecisions reads the decisions table, in the order it is written. A row
// that lost a cell is reported here rather than read: the answer is the cell
// this table exists for, and a row whose columns shifted would hand a reader the
// recommendation in its place.
func portalDecisions(t *testing.T) []portalDecision {
	t.Helper()
	const path = portalDir + "reconciliation.md"
	var decisions []portalDecision
	for _, line := range strings.Split(readDoc(t, path), "\n") {
		if !portalDecisionRowRE.MatchString(line) {
			continue
		}
		parts := strings.Split(strings.TrimSpace(line), "|")
		// `| a | b | c | d | e |` splits into the five cells with an empty field
		// on each side of them.
		if len(parts) != 7 {
			t.Errorf("%s's row %q has %d cells, want 5: a decision is a question, a recommendation, an alternative and the owner's answer, and a row that lost one reads as a question nobody asked", path, line, len(parts)-2)
			continue
		}
		columns := make([]string, 0, 5)
		for _, cell := range parts[1:6] {
			columns = append(columns, strings.TrimSpace(cell))
		}
		decisions = append(decisions, portalDecision{number: columns[0], columns: columns})
	}
	if len(decisions) == 0 {
		t.Fatalf("%s's decisions table has no `| DNNNN |` row: the table is where the folder's answers are, and a scan that finds none reads nothing", path)
	}
	return decisions
}

// portalTaskEntry is one task of the plan: its token ("P1.0") and the text of
// its bullet, which is where its Files, Tests, Command and Depends-on lines are.
type portalTaskEntry struct {
	task  string
	entry string
}

// portalTaskEntries reads every task the plan writes, in document order. The
// bullet, and not a section, is the unit: the plan writes one task per bullet
// and its Depends-on line inside that bullet, so the next heading is where an
// entry ends.
func portalTaskEntries(t *testing.T, path, plan string) []portalTaskEntry {
	t.Helper()
	locs := portalTaskHeadingRE.FindAllStringSubmatchIndex(plan, -1)
	if len(locs) == 0 {
		t.Fatalf("%s writes no `- **P0.0 …**` task heading: the plan's tasks are bullets, and a scan that finds none reads nothing", path)
	}
	var entries []portalTaskEntry
	seen := make(map[string]bool, len(locs))
	for i, loc := range locs {
		task := plan[loc[2]:loc[3]]
		if seen[task] {
			t.Errorf("%s writes the task heading %s twice: a task with two entries is two Files lists, and the level table can place only one of them", path, task)
		}
		seen[task] = true
		end := len(plan)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		entries = append(entries, portalTaskEntry{task: task, entry: plan[loc[0]:end]})
	}
	return entries
}

// portalDependsOn reads the plan tasks a task's Depends-on line names. The
// sentence runs to the Commit line every task of slices P1–P9 carries; a task
// with no such line at all is reported, because the level table places it and a
// task the plan never says when to start is one a reader schedules by guessing.
func portalDependsOn(t *testing.T, path, task, entry string) []string {
	t.Helper()
	i := strings.Index(entry, "Depends on:")
	if i < 0 {
		t.Errorf("%s's %s entry carries no `Depends on:` line: the level table places it, and the line is what that placement is read against", path, task)
		return nil
	}
	rest := entry[i+len("Depends on:"):]
	if j := strings.Index(rest, "Commit:"); j >= 0 {
		rest = rest[:j]
	}
	return portalTaskTokenRE.FindAllString(rest, -1)
}

// TestDocsPortalReservesItsNumberRanges: the folder takes two blocks and says so
// in each document a reader can meet them in, because every later task of the
// plan numbers itself inside them: the decisions in the reconciliation's header,
// the checks in its own and the plan's. A block that stopped being stated is a
// block the next work is free to take a number from, and the collision surfaces
// as two different questions wearing one number.
//
// The half with teeth is the checks table: the plan's rows are the numbers it
// wrote and the sentence under them is the tail it reserved, and the two have to
// be the whole block, in order, with no number left out -- a gap is a check the
// folder claims and nobody wrote.
func TestDocsPortalReservesItsNumberRanges(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{portalDir + "reconciliation.md", []string{
			"numbers **D2180–D2229** (D2180–D2207 used) and **Checks 570–579**",
		}},
		{portalDir + "README.md", []string{
			"Numbers taken: **D2180–D2229** (D2180–D2207 used), **Checks 570–579** (570–575 used)",
		}},
		{portalDir + "implementation-plan.md", []string{
			"## Checks this plan takes (570–579)",
			"576–579 are reserved.",
		}},
	} {
		flat := flattenDoc(readDoc(t, tc.path))
		for _, want := range tc.want {
			if !strings.Contains(flat, want) {
				t.Errorf("%s does not state %q: the blocks are what every task of this plan numbers itself inside, and a document that stopped saying them leaves the next work free to take a number that is already spoken for", tc.path, want)
			}
		}
	}

	const planPath = portalDir + "implementation-plan.md"
	plan := readDoc(t, planPath)
	var written []int
	for _, row := range portalTable(t, planPath, plan, "| Check | Measures |") {
		m := portalCheckRowRE.FindStringSubmatch(row)
		if m == nil {
			t.Errorf("%s's checks table carries the row %q, which is not `| NNN | … |`: update this test's markers", planPath, row)
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s's checks table row %q does not start with a number: %v", planPath, row, err)
		}
		written = append(written, n)
	}
	if len(written) == 0 {
		t.Fatalf("%s's checks table writes no check: the plan's own section says it takes a block of them", planPath)
	}
	for i, n := range written {
		if want := portalCheckLow + i; n != want {
			t.Errorf("%s's checks table writes check %d where the block's own order wants %d: the checks are one block written in number order, so a gap or a swap is a number a later task reads as free", planPath, n, want)
		}
	}
	reserved := portalReservedRE.FindStringSubmatch(flattenDoc(plan))
	if reserved == nil {
		t.Fatalf("%s no longer says which checks of the block it reserves: the numbers it writes are the used part, and the tail is the rest of the block", planPath)
	}
	lo, _ := strconv.Atoi(reserved[1])
	hi, _ := strconv.Atoi(reserved[2])
	if last := written[len(written)-1]; lo != last+1 {
		t.Errorf("%s writes checks up to %d and reserves from %d: the block is %d–%d with no number left out, and a gap between the two halves is a check the folder claims and nobody wrote", planPath, last, lo, portalCheckLow, portalCheckHigh)
	}
	if hi != portalCheckHigh {
		t.Errorf("%s reserves up to %d where the block ends at %d: the reserved tail is the rest of the block, and a tail that stops short leaves the numbers above it unclaimed", planPath, hi, portalCheckHigh)
	}
}

// TestDocsPortalDecisionRowsCarryAnAnswer: the reconciliation's table is what a
// reader checks to see whether a question is settled, and the folder's whole
// argument rests on every one of them being answered by the owner. An empty
// answer cell is a question the plan is still waiting on that reads as settled,
// and a cell without the date it was answered on cannot be told from one written
// later, so both are pinned here.
//
// The block's used part is checked from the rows rather than from the header:
// the header says D2180–D2207 are used, and a number it claims with no row under
// it -- or a row in the reserved tail -- is the header and the table disagreeing
// about how many questions were answered.
func TestDocsPortalDecisionRowsCarryAnAnswer(t *testing.T) {
	const path = portalDir + "reconciliation.md"
	decisions := portalDecisions(t)
	seen := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		n, err := strconv.Atoi(strings.TrimPrefix(d.number, "D"))
		if err != nil {
			t.Fatalf("%s's decision row %q does not carry a number: %v", path, d.number, err)
		}
		if n < portalDecisionLow || n > portalDecisionsUsedHigh {
			t.Errorf("%s writes decision %s, outside the used part of the block (D%d–D%d; D%d–D%d is reserved): a question numbered in the tail is one the folder says it has not asked", path, d.number, portalDecisionLow, portalDecisionsUsedHigh, portalDecisionsUsedHigh+1, portalDecisionHigh)
		}
		if seen[d.number] {
			t.Errorf("%s writes decision %s twice: two rows for one number are two questions a reader counts as one", path, d.number)
		}
		seen[d.number] = true
		answer := d.columns[4]
		if answer == "" {
			t.Errorf("decision %s's answer cell is empty: the folder's claim is that the owner answered every question, and a blank cell reads as one still waiting -- which is a task nobody may start", d.number)
			continue
		}
		if !strings.HasPrefix(answer, "**2026-11-12") {
			t.Errorf("decision %s's answer cell reads %q, which does not open with the date the owner answered on: an answer without its date cannot be told from one written later", d.number, answer)
		}
	}
	for n := portalDecisionLow; n <= portalDecisionsUsedHigh; n++ {
		if number := "D" + strconv.Itoa(n); !seen[number] {
			t.Errorf("%s has no %s row: the header says D%d–D%d are used, and a number the block claims with no question under it is an answer a reader counts twice", path, number, portalDecisionLow, portalDecisionsUsedHigh)
		}
	}
}

// TestDocsPortalLevelsTableMatchesTheDependsOnLines: the level table is the
// plan's schedule -- what runs together, and in what order the rest follows --
// so a level that contradicts a Depends-on line is a schedule an agent cannot
// keep, and it is the table an agent reads rather than the task bullets. Three
// halves: every task of slices P1–P9 is placed exactly once and the P0 tasks are
// not placed at all (they are owner-run, outside the levels), a task's level is
// strictly after the level of every task its own Depends-on line names, and the
// levels are written 0 upward with none skipped.
func TestDocsPortalLevelsTableMatchesTheDependsOnLines(t *testing.T) {
	const path = portalDir + "implementation-plan.md"
	plan := readDoc(t, path)

	levelOf := make(map[string]int)
	var placed []string
	var levels []int
	for _, row := range portalTable(t, path, plan, "| Level | Tasks (run together) |") {
		m := portalLevelRowRE.FindStringSubmatch(row)
		if m == nil {
			t.Errorf("%s's level table carries the row %q, which is not `| N | P… |`: update this test's markers", path, row)
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s's level table row %q does not start with a level number: %v", path, row, err)
		}
		levels = append(levels, n)
		tasks := portalTaskTokenRE.FindAllString(m[2], -1)
		if len(tasks) == 0 {
			t.Errorf("%s's level %d row names no task: a level with nothing to run is a row a reader counts as work", path, n)
		}
		for _, task := range tasks {
			if was, ok := levelOf[task]; ok {
				t.Errorf("%s files %s at level %d and again at level %d: a task runs once, and two rows for one task are two schedules a reader cannot both keep", path, task, was, n)
				continue
			}
			levelOf[task] = n
			placed = append(placed, task)
		}
	}
	if len(levelOf) == 0 {
		t.Fatalf("%s's level table places no task: the table is the plan's schedule, and a scan that finds none reads nothing", path)
	}
	for i, n := range levels {
		if n != i {
			t.Errorf("%s's level table writes level %d as its %dth row: the levels are 0 upward with none empty and none out of order, so a skipped or reordered level is a hole in the schedule", path, n, i)
		}
	}

	written := make(map[string]bool)
	for _, e := range portalTaskEntries(t, path, plan) {
		written[e.task] = true
		level, isPlaced := levelOf[e.task]
		if strings.HasPrefix(e.task, "P0.") {
			if isPlaced {
				t.Errorf("%s files %s at level %d, and the plan runs the P0 tasks outside the levels: they are the owner's, and a level that names one is work an agent reads as its own", path, e.task, level)
			}
			continue
		}
		if !isPlaced {
			t.Errorf("%s writes %s and the level table places it nowhere: every task of slices P1–P9 runs at a level, and one left out is a task a reader of the table never starts", path, e.task)
			continue
		}
		for _, dep := range portalDependsOn(t, path, e.task, e.entry) {
			depLevel, ok := levelOf[dep]
			if !ok {
				t.Errorf("%s's %s depends on %s, which the level table does not place: a dependency with no level is either a task left out of the table or a name that does not exist, and both leave the schedule unreadable", path, e.task, dep)
				continue
			}
			if depLevel >= level {
				t.Errorf("%s's %s sits at level %d and %s, which its Depends-on line names, sits at level %d: a task cannot run in the same level as what it waits on, and this table is what an agent reads to decide what runs together", path, e.task, level, dep, depLevel)
			}
		}
	}
	for _, task := range placed {
		if !written[task] {
			t.Errorf("%s's level table places %s, which the plan writes no task for: a level naming a task that does not exist is work an agent is sent to do", path, task)
		}
	}
}

// TestDocsPortalPinMovesTheCensusRow: writing this file is itself one more
// `internal/cli/docs_*_test.go`, and docs/rigor-modes/reconciliation.md's row
// counts them. That row's own text says the folder which writes a pin moves it
// in its own commit and names itself in the sentence beside it, so both halves
// are checked: the figure is re-counted from the tree rather than trusted, and
// the amendment sentence has to name this file -- a row that moved with no
// sentence is the merge the row calls the one that did not.
func TestDocsPortalPinMovesTheCensusRow(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	// The row is `| construct | count | command |`, and the count is the third
	// cell of the `|`-split.
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	if stated := strings.TrimSpace(cols[2]); stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files: the row is a count a reader re-runs, so the folder that adds a pin moves it", path, stated, len(pins))
	}
	const amendment = "Amended 2026-11-13: `internal/cli/docs_portal_test.go`, the portal folder's pin"
	if !strings.Contains(flattenDoc(doc), amendment) {
		t.Errorf("%s does not carry %q: the row's own text says a folder that writes a pin moves the row in its own commit and says so beside it, and a row that moved without the sentence is the merge the row calls the one that did not", path, amendment)
	}
}
