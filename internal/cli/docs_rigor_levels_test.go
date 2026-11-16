package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The levels addendum of docs/rigor-modes/ — the owner's second spec, the
// reconciliation that reads the tree back, the overlay plan and the index that
// names all three — and the guard that owns them.
//
// The folder's first work has its own guard (docs_rigor_modes_test.go), and this
// is the same convention applied to the second: a folder that wants its
// documents pinned writes its own file, so the two move independently — LV1.2
// restates the 2026-10-20 documents while this file's four stay put. It reuses
// the first guard's helpers (readDoc, flattenDoc, rigorModesNumbered,
// rigorModesTableCells, censusRoot) rather than copying them: the two guards
// read one folder and one repository, and a second spelling of a table-cell
// splitter is a second thing to keep right.
//
// What a guard can usefully hold here is the same shape the first one holds: the
// numbers of every block running from 1 with no gap, every citation resolving,
// every task stating what an agent needs to pick it up, and the owner's spec
// standing byte for byte where the owner wrote it.
const (
	rigorLevelsReadme = "docs/rigor-modes/README.md"
	rigorLevelsSpec   = "docs/rigor-modes/levels-spec.md"
	rigorLevelsRecon  = "docs/rigor-modes/levels-reconciliation.md"
	rigorLevelsPlan   = "docs/rigor-modes/levels-implementation-plan.md"

	// rigorLevelsAddendumHeading is the index's own section for the addendum,
	// which is where a reader finds its three documents: the 2026-10-20 work has
	// a table of its own above, so the addendum's files are checked inside this
	// section rather than anywhere in the file.
	rigorLevelsAddendumHeading = "## The 2026-10-22 addendum: Rigor Levels"

	// rigorLevelsParallelHeading is the plan's own table of what runs when. The
	// level of every task is read from it, and a Depends on line has to point at
	// a task below the one that names it.
	rigorLevelsParallelHeading = "## Parallel execution"
)

// rigorLevelsSpecCommit is the commit the plan names for the spec's blob (LV1.3).
// The owner's second spec was written on its own branch and squash-merged into
// `main` as 54ecb184, so the branch commit itself is not in the repository's
// history; the guard resolves it when it is there and reads the blob back from
// the commit that added the file otherwise. Either way the claim is the same
// one: the spec's bytes have not moved.
const rigorLevelsSpecCommit = "b599f268e"

// rigorLevelsSpecBlob is git's SHA-1 blob ID of docs/rigor-modes/levels-spec.md
// — the blob commit b599f268e recorded, verified against the commit that added
// the file to `main` (54ecb184, whose blob is this one). The spec is the owner's
// document and verbatim, so its content is a fact rather than a text to edit: a
// later task that changes one word of it fails here, and the way past is a new
// commit of the owner's spec, not a new hash.
//
// It is pinned as a hash rather than only read from git so the guard runs in
// every tree, including the copy without `.git` that
// docs_desktop_console_test.go's TestStagedLinuxFilesSurviveTheCopyIn makes; the
// same test reads git's own answer back where the tree is a repository.
const rigorLevelsSpecBlob = "4e37e0d835cb62ace89843c7234a433e2fab20af"

// rigorLevelsIndexedFiles are the documents the addendum's section of the index
// must name. The index is the folder's entry point, so a document it does not
// name is one a reader cannot find.
var rigorLevelsIndexedFiles = []string{
	"[levels-spec.md](levels-spec.md)",
	"[levels-reconciliation.md](levels-reconciliation.md)",
	"[levels-implementation-plan.md](levels-implementation-plan.md)",
}

// rigorLevelsTaskLabels are the seven things every task of the overlay plan has
// to state: what it changes, how it is tested, the command an agent runs, what
// fails first, what it waits for, what its commit says and who reviews it. A
// task missing one is a task an agent cannot pick up.
//
// Six are the plan's bold labels. The Files line is not: the plan writes it as
// the plain word beside its colon or parenthesis ("Files:", "Files (new):"), so
// that one is matched as the word — requiring a bold label there would fail
// every task in the plan, which is a guard against the document rather than for
// it.
var rigorLevelsTaskLabels = []struct {
	what string
	re   *regexp.Regexp
}{
	{"Files", regexp.MustCompile(`\bFiles\s*[:(]`)},
	{"Tests", regexp.MustCompile(`\*\*Tests:\*\*`)},
	{"Command", regexp.MustCompile(`\*\*Command:\*\*`)},
	{"Red step", regexp.MustCompile(`\*\*Red step:\*\*`)},
	{"Depends on", regexp.MustCompile(`\*\*Depends on:\*\*`)},
	{"Commit", regexp.MustCompile(`\*\*Commit:\*\*`)},
	{"Reviewer", regexp.MustCompile(`\*\*Reviewer:\*\*`)},
}

// The levels folder's numbered blocks, as the documents write them: a finding's
// opening label (LF1), a decision's table row (| **LD1** |), an UNVERIFIED list
// entry (- U1.) and a task's heading (**LV1.1 —), beside the slice headings the
// tasks hang from (### LV1 —). Each runs from 1 with no gap, and a reader who
// cites one has to be able to trust the number.
var (
	rigorLevelsFindingRE  = regexp.MustCompile(`(?m)^\*\*LF([0-9]+) —`)
	rigorLevelsDecisionRE = regexp.MustCompile(`(?m)^\| \*\*LD([0-9]+)\*\* \|`)
	rigorLevelsUnverifRE  = regexp.MustCompile(`(?m)^- U([0-9]+)\.`)
	rigorLevelsSliceRE    = regexp.MustCompile(`(?m)^### LV([0-9]+) —`)
	rigorLevelsTaskRE     = regexp.MustCompile(`(?m)^\*\*LV([0-9]+)\.([0-9]+) — `)
)

// The same blocks as a reader cites them anywhere in a document: "LF3", "LD11",
// "U4", "LV2.1" and the slice a range names ("LV1–LV7"). The optional second
// group is what tells a task citation from a slice one, which matters because
// only the first has to resolve to a task heading.
var (
	rigorLevelsCitedLFRE   = regexp.MustCompile(`LF([0-9]+)`)
	rigorLevelsCitedLDRE   = regexp.MustCompile(`LD([0-9]+)`)
	rigorLevelsCitedURE    = regexp.MustCompile(`\bU([0-9]+)\b`)
	rigorLevelsCitedTaskRE = regexp.MustCompile(`LV([0-9]+)(?:\.([0-9]+))?`)
)

// rigorLevelsTask is one task of the overlay plan: its id and the text of its
// block, which runs from its heading to the next task's heading.
type rigorLevelsTask struct{ id, body string }

// rigorLevelsTasks reads the plan's tasks in order.
func rigorLevelsTasks(t *testing.T, plan string) []rigorLevelsTask {
	t.Helper()
	idx := rigorLevelsTaskRE.FindAllStringSubmatchIndex(plan, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no tasks: a plan with no task is a plan no guard can read", rigorLevelsPlan)
	}
	tasks := make([]rigorLevelsTask, 0, len(idx))
	for n, m := range idx {
		id := "LV" + plan[m[2]:m[3]] + "." + plan[m[4]:m[5]]
		end := len(plan)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		tasks = append(tasks, rigorLevelsTask{id: id, body: plan[m[1]:end]})
	}
	return tasks
}

// rigorLevelsSegment returns the text of one labelled part of a task's block:
// what follows the label up to the next label the plan uses. It is how a guard
// asks a question about a task's Depends on line rather than about the whole
// task, which is the difference between reading a dependency and reading a
// mention of one.
func rigorLevelsSegment(t *testing.T, id, body, label string) string {
	t.Helper()
	i := strings.Index(body, label)
	if i < 0 {
		t.Fatalf("%s states no %s", id, label)
	}
	rest := body[i+len(label):]
	end := len(rest)
	for _, l := range rigorLevelsTaskLabels {
		if loc := l.re.FindStringIndex(rest); loc != nil && loc[0] < end {
			end = loc[0]
		}
	}
	return rest[:end]
}

// rigorLevelsParallelLevels reads the plan's parallel execution table into the
// level each task runs at. It is the table a Depends on line is checked against,
// so it is read rather than restated: a table that stops naming a task fails
// TestDocsRigorLevelsEveryDependencyIsEarlier rather than silently excusing it.
func rigorLevelsParallelLevels(t *testing.T, plan string) map[string]int {
	t.Helper()
	i := strings.Index(plan, rigorLevelsParallelHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q: the table is where a task's level is read from", rigorLevelsPlan, rigorLevelsParallelHeading)
	}
	body := plan[i:]
	if j := strings.Index(body[1:], "\n## "); j >= 0 {
		body = body[:j+1]
	}
	levels := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := rigorModesTableCells(line)
		if len(cells) < 2 {
			continue
		}
		level, err := strconv.Atoi(strings.TrimSpace(cells[0]))
		if err != nil {
			continue // the header row and the separator, which name no level
		}
		for _, m := range rigorLevelsCitedTaskRE.FindAllStringSubmatch(cells[1], -1) {
			if m[2] == "" {
				continue
			}
			id := m[0]
			if prev, ok := levels[id]; ok {
				t.Errorf("%s: the parallel execution table puts %s at level %d and level %d: a task runs at one level", rigorLevelsPlan, id, prev, level)
				continue
			}
			levels[id] = level
		}
	}
	if len(levels) == 0 {
		t.Fatalf("%s: the parallel execution table names no task", rigorLevelsPlan)
	}
	return levels
}

// rigorLevelsSpecBlobInGit returns git's blob ID of the spec at the commit that
// owns it — the commit the plan names when it is in this clone, and otherwise the
// commit that added the file — or "" when the tree cannot answer: no repository,
// no git, or a checkout too shallow to reach either. The caller reads that as
// "not checked here" rather than as a failure, the way the modes census skips its
// git rows in the copy-in tree.
func rigorLevelsSpecBlobInGit(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(censusRoot, ".git")); err != nil {
		return ""
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	run := func(args ...string) string {
		out, err := exec.Command(git, append([]string{"-C", censusRoot}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	commit := rigorLevelsSpecCommit
	if run("rev-parse", "--verify", "--quiet", commit+"^{commit}") == "" {
		commit = ""
		if log := run("log", "--diff-filter=A", "--format=%H", "--", rigorLevelsSpec); log != "" {
			commit = strings.Split(log, "\n")[0]
		}
	}
	if commit == "" {
		return ""
	}
	return run("rev-parse", "--verify", "--quiet", commit+":"+rigorLevelsSpec)
}

// rigorLevelsBlobID is git's SHA-1 object ID of a blob holding data, computed
// from the bytes rather than read out of a repository, so the pin below holds in
// a tree that has no `.git` at all.
func rigorLevelsBlobID(data string) string {
	sum := sha1.Sum([]byte("blob " + strconv.Itoa(len(data)) + "\x00" + data))
	return hex.EncodeToString(sum[:])
}

// TestDocsRigorLevelsSpecIsTheOwners: the levels spec is the owner's second
// document, and the owner's answers to it are recorded beside it rather than in
// it. The spec is verbatim, so it carries no answer marker and no answer text,
// and every LD decision in the reconciliation carries the owner's answer, dated
// 2026-10-22. This is the twin of TestDocsRigorModesSpecIsTheOwners, which holds
// the same rule for the first spec: an answer pasted into a spec makes the spec
// this folder's text, and the reconciliation is where a reader is meant to find
// it.
func TestDocsRigorLevelsSpecIsTheOwners(t *testing.T) {
	spec := readDoc(t, rigorLevelsSpec)
	recon := readDoc(t, rigorLevelsRecon)
	plan := readDoc(t, rigorLevelsPlan)

	for _, want := range []string{
		"# Fugaro Rigor Levels — Spec (owner, 2026-10-22 night)",
		// The default the spec names, and the five levels it names it among: the
		// vocabulary every task of the overlay plan is written in.
		"Default when nothing is specified: **`pro`**",
		"### 2.1 POC", "### 2.2 Startup", "### 2.3 Pro", "### 2.4 Critical", "### 2.5 Hotfix",
		"## 4. Promotion", "## 5. Review Matching", "## 6. Artefacts Summary",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q", rigorLevelsSpec, want)
		}
	}
	for _, want := range []string{"`poc`", "`startup`", "`pro`", "`critical`", "`hotfix`"} {
		if !strings.Contains(spec, want) {
			t.Errorf("%s lacks %q: the five levels are the spec's own vocabulary", rigorLevelsSpec, want)
		}
	}

	// The spec stays the owner's document: every answer is recorded in the
	// reconciliation, and one pasted into the spec would make it this folder's
	// text. The marker checked for is the decision label, not the date, because
	// the spec's own title carries the date it was written.
	for _, want := range []string{"**Answered", "**LD", "the owner answered"} {
		if strings.Contains(spec, want) {
			t.Errorf("%s records %q: the spec is verbatim, and the answers live in %s", rigorLevelsSpec, want, rigorLevelsRecon)
		}
	}

	// Every decision carries the owner's answer in its own row, and the answer is
	// the date the owner gave it: a decision left as a recommendation is the one
	// inconsistency this folder exists to avoid.
	idx := rigorLevelsDecisionRE.FindAllStringSubmatchIndex(recon, -1)
	if len(idx) == 0 {
		t.Fatalf("%s lists no decisions to answer", rigorLevelsRecon)
	}
	for n, m := range idx {
		id := recon[m[2]:m[3]]
		end := len(recon)
		if n+1 < len(idx) {
			end = idx[n+1][0]
		}
		row, _, _ := strings.Cut(recon[m[0]:end], "\n")
		if !strings.Contains(row, "2026-10-22") {
			t.Errorf("%s: LD%s carries no answer dated 2026-10-22: the owner answered every decision of the addendum on that day", rigorLevelsRecon, id)
		}
	}

	// The reconciliation says where it wins, and the plan says what it builds
	// from: the spec as amended by the reconciliation. A reader who finds only
	// one of the two has found half the design.
	for _, want := range []string{"this document wins", "[levels-spec.md](levels-spec.md)"} {
		if !strings.Contains(recon, want) {
			t.Errorf("%s lacks %q: the reconciliation is the document that reads the spec against the tree", rigorLevelsRecon, want)
		}
	}
	for _, want := range []string{"[levels-spec.md](levels-spec.md)", "[levels-reconciliation.md](levels-reconciliation.md)"} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not name %q: the plan builds the spec as the reconciliation amends it", rigorLevelsPlan, want)
		}
	}
}

// TestDocsRigorLevelsSpecIsByteIdenticalToItsCommit: the spec's bytes are the
// ones the owner wrote. It is the one document of this folder that no task may
// edit — the plan amends it by pointing at the reconciliation, never by
// rewriting the spec — so the strongest thing a guard can hold is that not one
// word of it has moved since the commit that owns it.
//
// The mutation that proves it: change one word of levels-spec.md and this test
// fails, on the blob ID below and not on anything else in the suite.
func TestDocsRigorLevelsSpecIsByteIdenticalToItsCommit(t *testing.T) {
	spec := readDoc(t, rigorLevelsSpec)
	if got := rigorLevelsBlobID(spec); got != rigorLevelsSpecBlob {
		t.Errorf("%s is blob %s, and the spec the owner wrote is %s: the spec is verbatim, and a task that changes it amends it through %s instead",
			rigorLevelsSpec, got, rigorLevelsSpecBlob, rigorLevelsRecon)
	}
	// And the pin is git's own answer, where this tree is a repository that can
	// give one: a hash that no longer matches what git records for the commit is
	// a hash that was moved to fit an edited file.
	if got := rigorLevelsSpecBlobInGit(t); got == "" {
		t.Logf("%s is not the root of a git work tree, or git cannot reach the commit that owns %s: the blob ID above was checked against the bytes only", censusRoot, rigorLevelsSpec)
	} else if got != rigorLevelsSpecBlob {
		t.Errorf("git records blob %s for %s at the commit that owns it, and this file pins %s: the two have to be the same blob",
			got, rigorLevelsSpec, rigorLevelsSpecBlob)
	}
}

// TestDocsRigorLevelsReadmeIndexesTheAddendum: the index names all three
// documents of the addendum, in the section that is theirs. The 2026-10-20 work
// has its own table above, so a levels document named only somewhere else in the
// file is not indexed for the work it belongs to.
func TestDocsRigorLevelsReadmeIndexesTheAddendum(t *testing.T) {
	readme := readDoc(t, rigorLevelsReadme)
	i := strings.Index(readme, rigorLevelsAddendumHeading)
	if i < 0 {
		t.Fatalf("%s lacks %q: the addendum's own section is where a reader finds its three documents", rigorLevelsReadme, rigorLevelsAddendumHeading)
	}
	for _, want := range rigorLevelsIndexedFiles {
		if !strings.Contains(readme[i:], want) {
			t.Errorf("%s's addendum section does not name %q: a document the index does not name is one a reader cannot find", rigorLevelsReadme, want)
		}
	}
}

// TestDocsRigorLevelsNumbersRunFromOne: every block of the addendum is numbered
// from 1 with no gap — the findings (LF), the decisions (LD), the UNVERIFIED
// rows (U), the slices (LV) and each slice's tasks (LVn.m). A citation has to
// mean one thing, and a gap is what a reader citing "LD7" cannot see.
func TestDocsRigorLevelsNumbersRunFromOne(t *testing.T) {
	recon := readDoc(t, rigorLevelsRecon)
	plan := readDoc(t, rigorLevelsPlan)

	rigorModesNumbered(t, rigorLevelsRecon, recon, "findings", rigorLevelsFindingRE)
	rigorModesNumbered(t, rigorLevelsRecon, recon, "decisions", rigorLevelsDecisionRE)
	rigorModesNumbered(t, rigorLevelsRecon, recon, "UNVERIFIED rows", rigorLevelsUnverifRE)
	rigorModesNumbered(t, rigorLevelsPlan, plan, "slices", rigorLevelsSliceRE)

	perSlice := map[int][]int{}
	var order []int
	for _, m := range rigorLevelsTaskRE.FindAllStringSubmatch(plan, -1) {
		slice, task := m[1], m[2]
		s, err := strconv.Atoi(slice)
		if err != nil {
			t.Fatalf("%s: slice label %q does not carry a number", rigorLevelsPlan, m[0])
		}
		n, err := strconv.Atoi(task)
		if err != nil {
			t.Fatalf("%s: task label %q does not carry a number", rigorLevelsPlan, m[0])
		}
		if _, ok := perSlice[s]; !ok {
			order = append(order, s)
		}
		perSlice[s] = append(perSlice[s], n)
	}
	if len(perSlice) == 0 {
		t.Fatalf("%s lists no tasks", rigorLevelsPlan)
	}
	for _, s := range order {
		nums := perSlice[s]
		for i, n := range nums {
			if n != i+1 {
				t.Fatalf("%s: LV%d's tasks are %v, want 1..%d with no gap: a citation has to mean one thing", rigorLevelsPlan, s, nums, len(nums))
			}
		}
	}
}

// TestDocsRigorLevelsEveryCitationResolves: every LF, LD, LV and U number a
// levels document cites resolves to a number its block has. A document that
// cites a finding that does not exist is a document whose reader cannot find
// what it points at, and a plan is edited by renumbering far more often than by
// rewriting.
//
// The U block is checked over the reconciliation alone, because it is the only
// one of the four the index does not share with the 2026-10-20 work: the U1, U2
// and U12 the index carries are that folder's UNVERIFIED rows, which
// docs_rigor_modes_test.go holds, and reading them against this addendum's four
// would fail a document that is right.
func TestDocsRigorLevelsEveryCitationResolves(t *testing.T) {
	recon := readDoc(t, rigorLevelsRecon)
	plan := readDoc(t, rigorLevelsPlan)

	findings := len(rigorLevelsFindingRE.FindAllString(recon, -1))
	decisions := len(rigorLevelsDecisionRE.FindAllString(recon, -1))
	unverified := len(rigorLevelsUnverifRE.FindAllString(recon, -1))
	if findings == 0 || decisions == 0 || unverified == 0 {
		t.Fatalf("%s states no findings, decisions or UNVERIFIED rows to cite", rigorLevelsRecon)
	}

	tasks := map[string]bool{}
	for _, m := range rigorLevelsTaskRE.FindAllStringSubmatch(plan, -1) {
		tasks["LV"+m[1]+"."+m[2]] = true
	}
	sliceSet := map[string]bool{}
	for _, m := range rigorLevelsSliceRE.FindAllStringSubmatch(plan, -1) {
		sliceSet["LV"+m[1]] = true
	}
	if len(tasks) == 0 || len(sliceSet) == 0 {
		t.Fatalf("%s lists no tasks or slices to cite", rigorLevelsPlan)
	}

	cites := 0
	for _, path := range []string{rigorLevelsReadme, rigorLevelsRecon, rigorLevelsPlan} {
		doc := readDoc(t, path)
		for _, m := range rigorLevelsCitedLFRE.FindAllStringSubmatch(doc, -1) {
			cites++
			if n, _ := strconv.Atoi(m[1]); n < 1 || n > findings {
				t.Errorf("%s cites %s, and the LF block reaches %d: a citation has to resolve to a number the block has", path, m[0], findings)
			}
		}
		for _, m := range rigorLevelsCitedLDRE.FindAllStringSubmatch(doc, -1) {
			cites++
			if n, _ := strconv.Atoi(m[1]); n < 1 || n > decisions {
				t.Errorf("%s cites %s, and the LD block reaches %d: a citation has to resolve to a number the block has", path, m[0], decisions)
			}
		}
		for _, m := range rigorLevelsCitedTaskRE.FindAllStringSubmatch(doc, -1) {
			cites++
			switch {
			case m[2] != "" && !tasks[m[0]]:
				t.Errorf("%s cites %s, which is no task heading of %s: a citation has to resolve to a heading", path, m[0], rigorLevelsPlan)
			case m[2] == "" && !sliceSet[m[0]]:
				t.Errorf("%s cites %s, which is no slice heading of %s", path, m[0], rigorLevelsPlan)
			}
		}
	}
	for _, m := range rigorLevelsCitedURE.FindAllStringSubmatch(recon, -1) {
		cites++
		if n, _ := strconv.Atoi(m[1]); n < 1 || n > unverified {
			t.Errorf("%s cites %s, and the UNVERIFIED list reaches U%d: a citation has to resolve to a number the block has", rigorLevelsRecon, m[0], unverified)
		}
	}
	if cites == 0 {
		t.Fatalf("no levels document cites a number: the three are meant to point at each other")
	}
}

// TestDocsRigorLevelsEveryTaskIsComplete: every task of the overlay plan states
// the seven things a task has to state — its files, its tests, the command an
// agent runs, its red step, what it waits for, its commit and its reviewer. A
// task missing one is a task nobody can start, and the one this guard was
// written for is the Tests line: a task whose tests are described inside its
// Command reads as complete to a person and as incomplete to an agent.
func TestDocsRigorLevelsEveryTaskIsComplete(t *testing.T) {
	plan := readDoc(t, rigorLevelsPlan)
	for _, task := range rigorLevelsTasks(t, plan) {
		for _, label := range rigorLevelsTaskLabels {
			if !label.re.MatchString(task.body) {
				t.Errorf("%s: %s states no %s: a task an agent cannot pick up is a task the plan only looks finished", rigorLevelsPlan, task.id, label.what)
			}
		}
	}
}

// TestDocsRigorLevelsEveryDependencyIsEarlier: every task named in a Depends on
// line is a task of the plan, sits at a lower level of the parallel execution
// table than the task that waits for it, and is in that table at all — as is the
// task that names it. The table is what a run's ordering is read from, so a
// dependency pointing at a later level, at a task that does not exist, or at one
// the table dropped is a plan that cannot be picked up in the order it states.
func TestDocsRigorLevelsEveryDependencyIsEarlier(t *testing.T) {
	plan := readDoc(t, rigorLevelsPlan)
	levels := rigorLevelsParallelLevels(t, plan)

	seen := map[string]bool{}
	for _, task := range rigorLevelsTasks(t, plan) {
		level, ok := levels[task.id]
		if !ok {
			t.Errorf("%s: %s is no task of the parallel execution table: a task outside it has no level for a Depends on line to sit below", rigorLevelsPlan, task.id)
			continue
		}
		seen[task.id] = true
		depends := rigorLevelsSegment(t, task.id, task.body, "**Depends on:**")
		for _, m := range rigorLevelsCitedTaskRE.FindAllStringSubmatch(depends, -1) {
			if m[2] == "" {
				t.Errorf("%s: %s depends on the slice %s: a Depends on line names a task", rigorLevelsPlan, task.id, m[0])
				continue
			}
			dep, ok := levels[m[0]]
			if !ok {
				t.Errorf("%s: %s depends on %s, which is no task of the parallel execution table", rigorLevelsPlan, task.id, m[0])
				continue
			}
			if dep >= level {
				t.Errorf("%s: %s sits at level %d and depends on %s at level %d: a dependency has to sit at a lower level of the table", rigorLevelsPlan, task.id, level, m[0], dep)
			}
		}
	}
	named := make([]string, 0, len(levels))
	for id := range levels {
		named = append(named, id)
	}
	slices.Sort(named)
	for _, id := range named {
		if !seen[id] {
			t.Errorf("%s: the parallel execution table names %s, which is no task heading", rigorLevelsPlan, id)
		}
	}
}
