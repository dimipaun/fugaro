package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The docs/mobile-identity-pairing/ folder, pinned, and the eight live checks
// the plan reserves for its own work. This pin is **T0** of
// docs/mobile-identity-pairing/implementation-plan.md, and the plan says what
// it holds: "a docs test pins this folder (documents exist, `spec.md` verbatim,
// decisions numbered D2780-D2799, tasks in order, every cited path exists, no
// secret shape); `docs/gcp-live-checklist.md` carries Checks 730-737 as one
// command each for the owner."
//
// The folder is four documents and no code: the owner's spec (spec.md,
// verbatim), the reconciliation of that spec against this tree
// (reconciliation.md), the index (README.md), the plan that turns the owner's
// answers into ordered tasks (implementation-plan.md), and the graph the plan
// is read through (implementation-plan.graph.json). Nothing here is
// implemented, no task is approved, and **the plan's own tasks are what will
// land the code these documents describe** -- so the citations below are
// checked against the promise each task makes rather than against the tree
// alone: a path the documents cite and the tree does not have is excused only
// while a task's own section names it, and the moment no task does, the guard
// fails. That is the half of this pin the plan calls "every cited path exists".
//
// Each test below is one claim of that sentence, and every one is a sentence a
// later edit can quietly falsify:
//
//   - TestDocsMobileIdentityPairingDocumentsExist -- the four documents and the
//     graph are in the tree, the index names all four, and it keeps the two
//     sentences that say what the folder is: the owner's spec is verbatim, and
//     nothing here is implemented or approved.
//   - TestDocsMobileIdentityPairingSpecIsTheOwnersText -- spec.md is the
//     owner's text: its title, its twelve sections and the date it was written,
//     with none of this folder's apparatus in it -- no decision number, no
//     finding number, no task number, and none of the words the reconciliation
//     and the plan are written in. "The spec stops being verbatim" is a thing a
//     later edit can do silently, which is why it is checked rather than
//     asserted.
//   - TestDocsMobileIdentityPairingDecisionsAreNumbered -- D2780-D2799, each
//     defined once: the ten asked ones as table rows, in order, and the ten
//     resolutions as bullets, each naming the findings it settles.
//   - TestDocsMobileIdentityPairingTasksAreInOrder -- the plan's 43 task
//     headings, in the plan's own order, each with a title, each carrying the
//     fields a task is read by; and the figure the index states (45 tasks) is
//     the count those headings make, with Z1-Z3's one heading holding three.
//   - TestDocsMobileIdentityPairingCitationsResolve -- every repository path
//     the documents cite resolves on disk, and every markdown link resolves
//     relative to the folder, under the promise rule above.
//   - TestDocsMobileIdentityPairingCarriesNoSecretShape -- no line of the four
//     documents carries a credential-shaped literal: a Firebase API key, a PEM
//     header, a signed JWT or a reverse-DNS identity literal. This folder is
//     *about* signing keys, assertions and tokens, so the next person to edit
//     it is the one most likely to paste a real one in.
//   - TestDocsMobileIdentityPairingChecksAreRegistered -- the register
//     paragraph that takes 730-737, and both halves of each check's
//     registration: a summary row and a `## Check 73N:` section, each owner-run,
//     each saying it has not been run, each naming no agent, and each carrying
//     **one command** -- which is what the plan's own acceptance sentence asks
//     for, and what the check is run from.
//
// **What this guard does not hold, said rather than left to be found.** It
// reads this folder's four documents and the check register's own sections for
// 730-737, and it makes no claim about what any *other* folder's documents say:
// a citation into another folder is held to that path resolving and to nothing
// about the sentence there. It pairs no symbol with a file -- the plan and the
// reconciliation name symbols loosely in prose throughout, and pairing an
// identifier with a citation needs a notion of scope the prose does not carry
// (docs/coordinator/implementation-plan.md's P0 says the same of its own pair of
// checks) -- so the code these documents describe is held to its paths and its
// numbering rather than to its names.
const (
	mobileIdentityPairingReadme = "docs/mobile-identity-pairing/README.md"
	mobileIdentityPairingSpec   = "docs/mobile-identity-pairing/spec.md"
	mobileIdentityPairingRecon  = "docs/mobile-identity-pairing/reconciliation.md"
	mobileIdentityPairingPlan   = "docs/mobile-identity-pairing/implementation-plan.md"

	// mobileIdentityPairingGraph is the plan's own graph, as
	// `fugaro plan graph --plan docs/mobile-identity-pairing/implementation-plan.md`
	// reads it. It is in the folder's table and in the census like the documents
	// are, but no test below reads its content: the plan is the authority the
	// graph is derived from, and the graph is checked by the command's own tests.
	mobileIdentityPairingGraph = "docs/mobile-identity-pairing/implementation-plan.graph.json"

	// mobileIdentityPairingPin is this file, by path. T0's own commit is the one
	// that adds it, and it is cited by the plan and the reconciliation like every
	// other path -- so TestDocsMobileIdentityPairingCitationsResolve reads it as
	// a citation that the change making it is the one that resolved, which is the
	// property an excused path loses.
	mobileIdentityPairingPin = "internal/cli/docs_mobile_identity_pairing_test.go"

	// mobileIdentityPairingStatus is the index's statement of what the folder is.
	// Every document here is a plan and nothing else: T0 lands a guard and a
	// register block, which implements none of the behaviour the folder
	// describes, and a reader who lost this line would take the tasks for work
	// that had been done.
	mobileIdentityPairingStatus = "**Status: plan only; nothing is implemented; no task approved.**"

	// mobileIdentityPairingSpecVerbatim is the index's own promise about
	// spec.md, and the sentence TestDocsMobileIdentityPairingSpecIsTheOwnersText
	// makes true rather than takes on its word.
	mobileIdentityPairingSpecVerbatim = "The owner's spec, verbatim. Not edited; no typo was fixed."
)

// mobileIdentityPairingDocuments is the folder's four documents: what
// TestDocsMobileIdentityPairingDocumentsExist holds to the tree, what the
// census reads and what the secret-shape scan walks. The index lists them in
// its own table; this is the list the checks below walk, and a document added
// to the folder and not to it is a document nothing pins.
var mobileIdentityPairingDocuments = []string{
	mobileIdentityPairingReadme,
	mobileIdentityPairingSpec,
	mobileIdentityPairingRecon,
	mobileIdentityPairingPlan,
}

// TestDocsMobileIdentityPairingDocumentsExist: the folder is in the tree, and
// the index still describes it as what it is. The index is where a reader
// starts, so it names every other file in the folder -- a file it drops is a
// file nobody opens -- and it carries the two sentences that keep the folder
// from being read as work: that nothing is implemented and no task is approved,
// and that the owner's spec is reproduced rather than paraphrased.
//
// Mutation (verified, run and restored): delete a document and this test fails
// naming it; delete the status line from the index and it fails naming the
// sentence the plan pins; drop a row from the index's table and it fails naming
// the file that stopped being linked.
func TestDocsMobileIdentityPairingDocumentsExist(t *testing.T) {
	for _, doc := range append(slices.Clone(mobileIdentityPairingDocuments), mobileIdentityPairingGraph) {
		if _, err := os.Stat("../../" + doc); err != nil {
			t.Errorf("%s is not in the tree: %v", doc, err)
		}
	}
	readme := readDoc(t, mobileIdentityPairingReadme)
	if !strings.Contains(readme, mobileIdentityPairingStatus) {
		t.Errorf("%s no longer carries the status line %q: the folder says of itself what it is, and a reader who lost that line would take the plan for code", mobileIdentityPairingReadme, mobileIdentityPairingStatus)
	}
	if !strings.Contains(readme, mobileIdentityPairingSpecVerbatim) {
		t.Errorf("%s no longer says of spec.md, %q: the index is where a reader learns that the document beside it is the owner's text, and a promise dropped is a promise the guard below is not read against", mobileIdentityPairingReadme, mobileIdentityPairingSpecVerbatim)
	}
	for _, name := range []string{"spec.md", "reconciliation.md", "implementation-plan.md", "implementation-plan.graph.json"} {
		if !strings.Contains(readme, "("+name+")") {
			t.Errorf("%s no longer links %s: the index is the one place a reader is sent from, and a file it drops is a file nobody opens", mobileIdentityPairingReadme, name)
		}
	}
}

// mobileIdentityPairingSpecHeadings is spec.md's headings, line for line: its
// title and the twelve sections under it, the one subsection included. The
// assertion is the whole list rather than a membership test, so a heading added,
// removed, renamed or reordered fails.
var mobileIdentityPairingSpecHeadings = []string{
	"# Fugaro Mobile App — Identity, Pairing and Text Interface Spec",
	"## Context and principles",
	"## Why QR-only pairing is not enough",
	"## Identity providers in Fugaro-Prod",
	"## Multiple identities per app",
	"## Pairing flow",
	"### Why a workspace-bound assertion instead of the raw ID token",
	"## Multi-device continuity",
	"## Job controls",
	"## Text interface",
	"## Context gauge and usage meter",
	"## Resolved decisions",
	"## Corner cases",
}

var mobileIdentityPairingSpecHeadingRE = regexp.MustCompile(`(?m)^#{1,6} .*$`)

// mobileIdentityPairingSpecDate is the line the spec dates itself with, which
// is the whole of its provenance: the folder has no signature block, and the
// index sends a reader to the reconciliation for the day each decision was
// answered.
const mobileIdentityPairingSpecDate = "November 2026."

// mobileIdentityPairingSpecForeignRE are the folder's own numbering schemes: a
// decision number, a finding number, a task number, an open item and a check.
// The spec the owner wrote has none of them, and one appearing in it is the two
// documents mixed.
var mobileIdentityPairingSpecForeignRE = []*regexp.Regexp{
	regexp.MustCompile(`\bD\d+\b`),
	regexp.MustCompile(`\bF\d+\b`),
	regexp.MustCompile(`\b[OPQU]\d{1,2}\b`),
	regexp.MustCompile(`\bCheck \d+\b`),
}

// mobileIdentityPairingSpecForeignWords is the folder's own vocabulary for its
// apparatus. These are words the reconciliation and the plan cannot be written
// without, so one of them in the owner's text is an edit to the text rather than
// a copy of it.
var mobileIdentityPairingSpecForeignWords = []string{
	"Recommendation",
	"Alternative",
	"reconcil",
	"implementation-plan",
	"implementation plan",
	"Finding",
	"UNVERIFIED",
}

// TestDocsMobileIdentityPairingSpecIsTheOwnersText: spec.md is the owner's
// spec, copied alone -- which is what the index promises and what the
// reconciliation's whole reading rests on, since it disagrees with the owner's
// words rather than with a paraphrase. The ways it stops being a copy are
// silent: a heading reworded, a decision number pasted in from the
// reconciliation, a paragraph annotated in place.
//
// Mutation (verified, run and restored): insert a decision number into the spec
// (or reword a heading, or delete the date line) and this test fails naming it.
func TestDocsMobileIdentityPairingSpecIsTheOwnersText(t *testing.T) {
	spec := readDoc(t, mobileIdentityPairingSpec)
	if got := mobileIdentityPairingSpecHeadingRE.FindAllString(spec, -1); !slices.Equal(got, mobileIdentityPairingSpecHeadings) {
		t.Errorf("%s's headings are %q; the owner's spec is its title and the twelve sections under it, and a heading that moved or multiplied means the document was edited rather than copied", mobileIdentityPairingSpec, got)
	}
	if !strings.Contains(spec, mobileIdentityPairingSpecDate) {
		t.Errorf("%s no longer carries %q: the spec is the owner's text, its date included, and that line is the whole of its provenance", mobileIdentityPairingSpec, mobileIdentityPairingSpecDate)
	}
	for _, re := range mobileIdentityPairingSpecForeignRE {
		if got := re.FindString(spec); got != "" {
			t.Errorf("%s carries %q, which is this folder's own numbering rather than the owner's words: the spec is reproduced alone, and a number pasted into it is the reconciliation leaking into the text it disagrees with", mobileIdentityPairingSpec, got)
		}
	}
	for _, word := range mobileIdentityPairingSpecForeignWords {
		if strings.Contains(spec, word) {
			t.Errorf("%s carries %q, which is this folder's own vocabulary: the spec is the text as it was written, with nothing of ours in it", mobileIdentityPairingSpec, word)
		}
	}
}

// mobileIdentityPairingDecisionRowRE matches a decision the reconciliation
// *asks*: a table row of section "Decisions", `| **D2780** | … |`.
var mobileIdentityPairingDecisionRowRE = regexp.MustCompile(`(?m)^\| \*\*(D\d+)\*\* \|`)

// mobileIdentityPairingResolutionRE matches a decision the reconciliation
// *resolves*: a bullet of the same section, `- **D2790** (F5). …`. The findings
// in the parentheses are the spec text the decision settles, and they are what
// makes the section a reconciliation rather than a list of answers.
var mobileIdentityPairingResolutionRE = regexp.MustCompile(`(?m)^- \*\*(D\d+)\*\* \((F[^)]*)\)\.`)

// mobileIdentityPairingDecisionsSection returns the reconciliation's
// "## Decisions" section, where both shapes are written. Bounding it is the
// point: the numbers are cited all over the rest of the document ("Amended by
// O7", "task P8"), and a census read from the whole file would count those.
func mobileIdentityPairingDecisionsSection(t *testing.T, recon string) string {
	t.Helper()
	const heading = "## Decisions"
	i := strings.Index(recon, heading)
	if i < 0 {
		t.Fatalf("%s has no %q section: the decisions D2780-D2799 are written there, in the two shapes this test reads", mobileIdentityPairingRecon, heading)
	}
	section := recon[i+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsMobileIdentityPairingDecisionsAreNumbered: D2780-D2799 are defined
// once each -- the ten the owner was asked as rows, in order, and the ten
// resolved as bullets, each naming the findings it settles -- and no other
// decision number is defined anywhere in the section.
//
// A decision number is an allocation the owner made, so a number that moves or
// a gap that opens is not a formatting slip: it is this work claiming a number
// nobody gave it, or dropping one it was given. The rows are read in order
// because the asked ten are one table; the resolutions are read as a set,
// because the reconciliation writes them in the order it settled them (D2799
// before D2798) and a guard that demanded ascending numbers there would fail on
// the document as written.
//
// Mutation (verified, run and restored): renumber one decision (D2789 to
// D2790) and this test fails naming the number and the position; delete a
// resolution's finding list and it fails naming the decision that stopped
// saying which specification text it settles.
func TestDocsMobileIdentityPairingDecisionsAreNumbered(t *testing.T) {
	section := mobileIdentityPairingDecisionsSection(t, readDoc(t, mobileIdentityPairingRecon))
	rows := mobileIdentityPairingDecisionRowRE.FindAllStringSubmatch(section, -1)
	if len(rows) != 10 {
		t.Fatalf("%s's Decisions section carries %d decision rows, and D2780-D2789 are the ten the owner was asked: the rows, or the section, is what changed", mobileIdentityPairingRecon, len(rows))
	}
	for i, m := range rows {
		want := fmt.Sprintf("D%d", 2780+i)
		if m[1] != want {
			t.Errorf("%s's decision row %d is %s, where D2780-D2789 in order means %s: the number is an allocation the owner made, and one that moved is an allocation this work did not receive", mobileIdentityPairingRecon, i+1, m[1], want)
		}
	}
	resolutions := mobileIdentityPairingResolutionRE.FindAllStringSubmatch(section, -1)
	if len(resolutions) != 10 {
		t.Fatalf("%s's Decisions section carries %d resolution bullets, and D2790-D2799 are the ten the owner's answers resolved: the bullets, or the section, is what changed", mobileIdentityPairingRecon, len(resolutions))
	}
	seen := map[string]bool{}
	for _, m := range resolutions {
		if seen[m[1]] {
			t.Errorf("%s defines %s twice: a number is one decision, and a second definition is an answer that was rewritten rather than amended", mobileIdentityPairingRecon, m[1])
		}
		seen[m[1]] = true
	}
	for i := 2790; i <= 2799; i++ {
		number := fmt.Sprintf("D%d", i)
		if !seen[number] {
			t.Errorf("%s defines no %s: D2780-D2799 is the block this folder took, and a gap in the resolutions is a decision the owner answered that the document no longer records", mobileIdentityPairingRecon, number)
		}
	}
}

// mobileIdentityPairingTaskRE matches a task heading: `### T0: the pin test and
// the live checks`. The name is the id the folder's own text cites the task by.
var mobileIdentityPairingTaskRE = regexp.MustCompile(`(?m)^### ([A-Za-z0-9-]+): (.+)$`)

// mobileIdentityPairingTask is one task: its heading's id and title, and the
// text under it to the next heading -- which is the whole of what the task
// promises, its Files line included.
type mobileIdentityPairingTask struct{ name, title, body string }

// mobileIdentityPairingTasks reads the plan's tasks: the "## Slices and tasks"
// section, from each `### ` heading to the next. The section after it
// ("## Parallel execution") is prose about the tasks and defines none, so the
// slice stops there -- a task named in a table below is not a task the plan
// writes, and this census is of the plan's own definitions.
func mobileIdentityPairingTasks(t *testing.T, plan string) []mobileIdentityPairingTask {
	t.Helper()
	const heading = "## Slices and tasks"
	i := strings.Index(plan, heading)
	if i < 0 {
		t.Fatalf("%s has no %q section: the tasks are written there, and this census reads the headings, not the parallel-execution table", mobileIdentityPairingPlan, heading)
	}
	rest := plan[i+len(heading):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		t.Fatalf("%s's task section has no heading after it: the plan writes one, and without it this census would read the whole document", mobileIdentityPairingPlan)
	}
	section := rest[:end]
	heads := mobileIdentityPairingTaskRE.FindAllStringSubmatchIndex(section, -1)
	var out []mobileIdentityPairingTask
	for j, m := range heads {
		stop := len(section)
		if j+1 < len(heads) {
			stop = heads[j+1][0]
		}
		out = append(out, mobileIdentityPairingTask{
			name:  section[m[2]:m[3]],
			title: section[m[4]:m[5]],
			body:  section[m[1]:stop],
		})
	}
	return out
}

// mobileIdentityPairingTaskNamed returns the task with this heading id, failing
// when the plan no longer writes one. What a task's own section says is read
// through this, so a heading that was renamed fails here rather than quietly
// passing an empty body to the check below.
func mobileIdentityPairingTaskNamed(t *testing.T, tasks []mobileIdentityPairingTask, name string) mobileIdentityPairingTask {
	t.Helper()
	for _, task := range tasks {
		if task.name == name {
			return task
		}
	}
	t.Fatalf("%s writes no %s heading: it is one of the tasks in the order this guard pins, and a test reading its section has nothing to read", mobileIdentityPairingPlan, name)
	return mobileIdentityPairingTask{}
}

// mobileIdentityPairingTaskOrder is the plan's task order, as the plan writes
// it: the slices' order, which is the order the plan's own "Slices and tasks"
// and its levels table argue for. It is held here rather than derived, because
// the order is the thing being pinned: a task renumbered or moved turns the
// dependency order into a list a reader picks from.
var mobileIdentityPairingTaskOrder = []string{
	"DA", "T0", "C1", "C2", "P0", "M0", "Z0",
	"A1", "A2", "A3", "A4", "P1", "P8",
	"P2", "P3", "P4", "P5", "P6", "P7", "P9",
	"K1", "K2", "M1", "M2", "M3", "K3",
	"J1", "J2", "J3", "J4", "J5r", "J5a", "J5b", "J5c", "J6",
	"X1", "X2", "X3", "Z1-Z3", "I1", "I2", "D1", "UP1",
}

// mobileIdentityPairingTaskFields is what every task is read by, whatever shape
// its section takes: the plan writes most tasks as one bullet per field, and
// Z1-Z3 -- three tasks under one heading -- as one bullet per task with the
// fields inline. The census below asks for the labels, so both shapes pass and a
// section that dropped its red step does not: an acceptance with no failing
// state beside it is the half of the task's discipline an implementer is left to
// invent.
var mobileIdentityPairingTaskFields = []string{
	"Acceptance:", "Files:", "Tests:", "Command:", "Red step:", "Depends on:", "Commit:", "Reviewer tier:",
}

// mobileIdentityPairingTaskCount is the figure the index states -- "45 tasks
// and two anticipated contract changes" -- and it is what the headings make:
// 42 headings of one task each, and Z1-Z3's single heading holding three.
const mobileIdentityPairingTaskCount = 45

// TestDocsMobileIdentityPairingTasksAreInOrder: the plan's 43 task headings are
// the pinned order, each carries a title, each carries the fields a task is read
// by, and the count they make is the count the index states.
//
// Mutation (verified, run and restored): delete a task heading and this test
// fails naming the position; reorder two and it fails naming both; delete a
// task's Red step and it fails naming the field; change the index's 45 to 44
// and it fails with the count the headings make.
func TestDocsMobileIdentityPairingTasksAreInOrder(t *testing.T) {
	tasks := mobileIdentityPairingTasks(t, readDoc(t, mobileIdentityPairingPlan))
	if len(tasks) != len(mobileIdentityPairingTaskOrder) {
		t.Fatalf("%s writes %d task headings, and the plan's own order is %d: the headings, or the plan, is what changed", mobileIdentityPairingPlan, len(tasks), len(mobileIdentityPairingTaskOrder))
	}
	for i, task := range tasks {
		want := mobileIdentityPairingTaskOrder[i]
		if task.name != want {
			t.Errorf("%s's task %d is %s, where the plan's order means %s: the tasks are a dependency order, and one that moved is a reader planning work the tree cannot carry yet", mobileIdentityPairingPlan, i+1, task.name, want)
		}
		if strings.TrimSpace(task.title) == "" {
			t.Errorf("%s's %s has no title after its colon: the heading is what `fugaro plan graph` and this folder's text name the task by, and one with no title is a task a reader cannot find", mobileIdentityPairingPlan, task.name)
		}
		for _, field := range mobileIdentityPairingTaskFields {
			if !strings.Contains(task.body, field) {
				t.Errorf("%s's %s carries no %q: every task states its acceptance, its files, its tests, its command, its red step, its dependencies, its commit and its reviewer tier, and a field dropped is the one an implementer has to invent", mobileIdentityPairingPlan, task.name, field)
			}
		}
	}
	// Z1-Z3 is the one heading that holds more than one task: the context
	// module's three, which the plan writes as three bullets because they are one
	// module waiting on one spike. Both halves are named here so the arithmetic
	// below cannot pass by accident: a plan that split the heading into three
	// would fail the order list above, and one that dropped two of its bullets
	// would fail this.
	z := mobileIdentityPairingTaskNamed(t, tasks, "Z1-Z3")
	for _, want := range []string{"**Z1 context gauge.**", "**Z2 compaction.**", "**Z3 usage meter.**"} {
		if !strings.Contains(z.body, want) {
			t.Errorf("%s's Z1-Z3 section carries no %q: the heading holds three tasks, each with its own acceptance and commit, and a bullet dropped is an allocation the count below still pays for", mobileIdentityPairingPlan, want)
		}
	}
	if got := len(tasks) - 1 + 3; got != mobileIdentityPairingTaskCount {
		t.Errorf("%s's headings make %d tasks (%d headings, one of them holding three) and the index states %d: the two are the same figure, and the one that moved is the one a reader counts against the wrong document", mobileIdentityPairingPlan, got, len(tasks), mobileIdentityPairingTaskCount)
	}
	if !strings.Contains(readDoc(t, mobileIdentityPairingReadme), fmt.Sprintf("%d tasks", mobileIdentityPairingTaskCount)) {
		t.Errorf("%s no longer states the %d tasks the plan writes: the index is where the folder's size is read, and a figure dropped leaves the plan's own count unstated", mobileIdentityPairingReadme, mobileIdentityPairingTaskCount)
	}
}

// mobileIdentityPairingCitationRE matches a backticked token. The folder writes
// a path as `internal/cli/pair.go`, a directory as `ee/identitysvc` and a
// package directory as `internal/mobilesession`.
var mobileIdentityPairingCitationRE = regexp.MustCompile("`([^`\n]+)`")

// mobileIdentityPairingProseChars are the characters a backticked token carries
// when it is prose rather than a citation: whitespace (a command line, a
// sentence), a bar, a hedge, a glob or a bracket. They are what keeps
// `fugaro pair --email` and a Realtime Database node path with a wildcard in it
// out of the census.
const mobileIdentityPairingProseChars = " \t|—*?[]{}<>"

// mobileIdentityPairingCitedPaths is every repository path a document cites: a
// backticked token, with no prose characters in it, that contains a `/` and
// whose first segment is a top-level directory of this repository (the walk
// mobileAuthTopLevelDirs makes, reused rather than written again: it is the same
// claim about the same tree). The token is used as written -- a trailing slash is
// a spelling of a directory and is stripped, but a line number (`rules.go:433`)
// is not: it is a citation that does not resolve.
func mobileIdentityPairingCitedPaths(doc string, dirs map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mobileIdentityPairingCitationRE.FindAllStringSubmatch(doc, -1) {
		token := m[1]
		if strings.ContainsAny(token, mobileIdentityPairingProseChars) {
			continue
		}
		if !strings.Contains(token, "/") {
			continue
		}
		if !dirs[strings.SplitN(token, "/", 2)[0]] {
			continue
		}
		token = strings.TrimSuffix(token, "/")
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	slices.Sort(out)
	return out
}

// mobileIdentityPairingLinkRE matches a markdown link target. The index and the
// plan cite their neighbours that way (`../mobile-auth/spec.md`,
// `implementation-plan.graph.json`), and a link is a citation however it is
// written.
var mobileIdentityPairingLinkRE = regexp.MustCompile(`\]\(([^)#\s]+)\)`)

// mobileIdentityPairingPromisedBy returns the plan task whose own section names
// path, or "" when no task does. This is the excuse rule the census runs on, and
// it is derived rather than listed on purpose: the plan's Files lines are the
// promise that a path will exist, so a cited path that is not in the tree is
// acceptable exactly while a task says it will be -- and the day a document
// starts citing a path no task promises, the census fails and names it, which is
// the pull request that would otherwise have landed a dead link. A hand-kept
// list of a hundred forthcoming paths would rot a task at a time; this one
// empties itself as the tasks land, because a path that exists is never asked
// for a promise.
func mobileIdentityPairingPromisedBy(tasks []mobileIdentityPairingTask, path string) string {
	for _, task := range tasks {
		if strings.Contains(task.body, path) {
			return task.name
		}
	}
	return ""
}

// TestDocsMobileIdentityPairingCitationsResolve: every repository path the four
// documents cite is either in the tree or named by a plan task that will land
// it, and every markdown link resolves relative to the folder. A citation is how
// these documents carry their whole value -- the reconciliation is a reading of
// the tree, the plan is a set of tasks against it, and the checks are run on
// what the tasks build -- so a path nobody can open is a claim nobody can check.
//
// Mutation (verified, run and restored): name a path the tree does not have and
// no task promises (`internal/cli/no-such-file.go`) and this test fails naming
// it and the document that cites it; append a line number to a citation
// (`rules.go:433`) and it fails on the same token; add a link to a document that
// is not there and it fails naming the link; delete the Files line that promises
// one of the excused paths and it fails naming the path that lost its promise.
func TestDocsMobileIdentityPairingCitationsResolve(t *testing.T) {
	dirs := mobileAuthTopLevelDirs(t)
	tasks := mobileIdentityPairingTasks(t, readDoc(t, mobileIdentityPairingPlan))
	// The floor is per document, and it is what keeps a filter that stopped
	// matching from making every assertion below vacuous: a census that found no
	// paths would pass every rule it has. The index and the spec cite no path in
	// backticks at all -- their neighbours are links, which the second half
	// reads -- so the two floors are the two documents that cite the tree.
	for _, doc := range []struct {
		path string
		min  int
	}{{mobileIdentityPairingRecon, 40}, {mobileIdentityPairingPlan, 150}} {
		paths := mobileIdentityPairingCitedPaths(readDoc(t, doc.path), dirs)
		if len(paths) < doc.min {
			t.Fatalf("%s: mobileIdentityPairingCitedPaths found %d paths, and this document cites at least %d: the filter, not the document, is what broke", doc.path, len(paths), doc.min)
		}
		for _, p := range paths {
			if _, err := os.Stat("../../" + p); err == nil {
				continue
			}
			if owner := mobileIdentityPairingPromisedBy(tasks, p); owner != "" {
				continue
			}
			t.Errorf("%s cites %s, which is not in the tree and which no task of the plan names: a path a reader cannot open is a dead end, and a path these documents expect to exist must be promised by the Files line of the task that lands it", doc.path, p)
		}
	}
	// This file is the one path in the census whose promise T0's own commit
	// spends, so it is asserted rather than left to the loop above: it exists,
	// and it resolves for that reason rather than because a task still promises
	// to write it.
	if _, err := os.Stat("../../" + mobileIdentityPairingPin); err != nil {
		t.Errorf("%s is not in the tree: %v -- it is the path T0's own commit lands, and the census reads it like any other citation", mobileIdentityPairingPin, err)
	}

	// The links, which are how the folder cites its neighbours: resolved against
	// the document's own directory, with a fragment dropped (an anchor is a claim
	// about the target's headings, and a guard for that is a different pin).
	// The graph is generated and the spec is the owner's text copied whole, so
	// the two of them are read for resolution and not for the presence of a
	// link: spec.md has none at all, which is one of the things that makes it the
	// owner's document rather than one of ours. The three that cite their
	// neighbours must keep doing so -- a folder whose documents stopped linking
	// each other is a folder a reader has to guess their way around.
	for _, doc := range append(slices.Clone(mobileIdentityPairingDocuments), mobileIdentityPairingGraph) {
		text := readDoc(t, doc)
		links := mobileIdentityPairingLinkRE.FindAllStringSubmatch(text, -1)
		if doc != mobileIdentityPairingGraph && doc != mobileIdentityPairingSpec && len(links) == 0 {
			t.Errorf("%s carries no link at all: the folder's own three documents cite each other and the tree, and one with no link is a document whose reader was not sent anywhere", doc)
		}
		for _, m := range links {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join("../../", filepath.Dir(doc), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links %s, which is not there: %v", doc, target, err)
			}
		}
	}
}

// mobileIdentityPairingSecretShapes are the four shapes a credential would take
// in these documents. The plan names the first three by kind -- this folder is
// about signing keys, assertions and session tokens -- and the fourth is the
// shape of the app's store identity, which D125 keeps out of this repository.
// A signed JWT is the interesting one: this folder's whole subject is tokens,
// and a real one pasted into a citation or a fixture would be a live bearer
// credential in a public repository.
var mobileIdentityPairingSecretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a Firebase API key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{10,}`)},
	{"a PEM header", regexp.MustCompile(`-----BEGIN[A-Z ]*-----`)},
	{"a signed JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)},
	{"a reverse-DNS identity literal", regexp.MustCompile(`\b(?:com|org|net|io|ai|dev|co)\.[A-Za-z0-9][A-Za-z0-9.-]*`)},
}

// mobileIdentityPairingSecretShape returns the name of the first shape line
// carries, or "" if it carries none.
func mobileIdentityPairingSecretShape(line string) string {
	for _, s := range mobileIdentityPairingSecretShapes {
		if s.re.MatchString(line) {
			return s.name
		}
	}
	return ""
}

// TestDocsMobileIdentityPairingCarriesNoSecretShape: no line of the four
// documents carries a credential-shaped literal. The plan says why this one is
// unusual: the folder is *about* a signing key, a JWKS, an assertion and a
// refresh token, so the next person to edit it is the one most likely to paste
// a real one in -- and a document is where a value pasted once stays.
//
// The scan is walked with planted values first, so a green run means the walk
// can fail, and with strings the documents really carry, so a check that fails
// on everything fails here rather than passing quietly.
//
// Mutation (verified, run and restored): paste an `AIza…`-shaped string or a
// signed JWT into the reconciliation and this test fails naming the document and
// the line.
func TestDocsMobileIdentityPairingCarriesNoSecretShape(t *testing.T) {
	for _, planted := range []struct{ line, shape string }{
		{"    api_key: AIzaSyA0123456789abcdefghijklmnopqrstu", "a Firebase API key"},
		{"-----BEGIN PRIVATE KEY-----", "a PEM header"},
		{"  assertion: eyJhbGciOiJFZERTQSJ9.eyJhdWQiOiJhY21lIn0.sig", "a signed JWT"},
		{"  ios.bundleIdentifier: com.fugaro.app", "a reverse-DNS identity literal"},
	} {
		if got := mobileIdentityPairingSecretShape(planted.line); got != planted.shape {
			t.Fatalf("%q is reported as %q, and it is %s: the scan is what broke, not the documents", planted.line, got, planted.shape)
		}
	}
	// The plan writes the key's shape as `AIza…`, whose ellipsis is not key
	// material, and the documents name real things that are not identities. A
	// scan that flagged these would be turned off rather than fixed.
	for _, clean := range []string{
		"shape (`AIza…`), and the plan names no such literal anywhere",
		"the assertion is an EdDSA JWT with `iss`, `aud`, `sub`, `email` and `jti`",
		"`mobile/app.json` carries no `ios.bundleIdentifier`",
		"the keys are published at a JWKS URL (`https://…/jwks.json`)",
	} {
		if got := mobileIdentityPairingSecretShape(clean); got != "" {
			t.Errorf("%q is reported as %s: the scan must tell a document talking about a shape from the shape itself", clean, got)
		}
	}
	for _, doc := range mobileIdentityPairingDocuments {
		for i, line := range strings.Split(readDoc(t, doc), "\n") {
			if got := mobileIdentityPairingSecretShape(line); got != "" {
				t.Errorf("%s:%d carries %s: this folder is about signing keys, assertions and tokens, and a value pasted here is a value in a public repository", doc, i+1, got)
			}
		}
	}
}

// mobileIdentityPairingCensusRowRE reads the rigor-modes census' own row of the
// `internal/cli` docs pins, capturing its count: the label, the number and the
// row's own `ls internal/cli/docs_*_test.go | wc -l` command. The count is read
// and not matched as text, so the assertion below holds it to a floor rather
// than to one value -- the row is a running total every folder that adds a pin
// moves, and an equality here goes stale on the next such merge.
var mobileIdentityPairingCensusRowRE = regexp.MustCompile("\\| Docs folders that ship their own `internal/cli/docs_\\*_test\\.go` pin \\| ([0-9]+) \\| `ls internal/cli/docs_\\*_test\\.go \\\\?\\| wc -l` \\|")

// TestDocsMobileIdentityPairingMovesTheCensusRow: this file is one more
// `internal/cli/docs_*_test.go` pin, so it moves the running total of them in
// docs/rigor-modes/reconciliation.md, whose own rule is that the folder which
// writes one moves the row in the same commit and names itself in the sentence
// beside it. A row that moved without a sentence is the merge that row calls the
// one that did not, and every pin in this package is held to it the same way.
//
// The count is read as "at least 150", the figure this commit moved it from, and
// not pinned to 151: the row is a total of other folders' guards, and an equality
// would fail on the next pin rather than on this one. The count is not computed
// here -- running `ls` would make this test not hermetic -- because the row's own
// command is the check the owner runs.
func TestDocsMobileIdentityPairingMovesTheCensusRow(t *testing.T) {
	const recon = "docs/rigor-modes/reconciliation.md"
	census := flattenDoc(readDoc(t, recon))
	row := mobileIdentityPairingCensusRowRE.FindStringSubmatch(census)
	if row == nil {
		t.Fatalf("%s carries no `Docs folders that ship their own `internal/cli/docs_*_test.go` pin` row with a count in it and the `ls internal/cli/docs_*_test.go | wc -l` command beside it: the row is a running total of other folders' guards, and the folder that adds one is the folder that moves it", recon)
	}
	if count, err := strconv.Atoi(row[1]); err != nil || count < 150 {
		t.Errorf("%s's `internal/cli/docs_*_test.go` pin row reads %q, want a number at least 150: this file's commit moved the row, the running total never falls, and a row below this one is a total that lost the guards of a folder it counted", recon, row[1])
	}
	if !strings.Contains(census, "Amended 2026-11-19: `internal/cli/docs_mobile_identity_pairing_test.go`, the mobile-identity-pairing folder's pin") {
		t.Errorf("%s does not name this pin as the amendment that moved its `internal/cli/docs_*_test.go` count: the row is a total of other folders' guards, and a folder that writes one says so in its own commit", recon)
	}
}

// mobileIdentityPairingChecks are the eight live checks T0 writes, with the
// plan task each one is read against and the phrases its section has to keep.
// The numbers are the block the folder's reconciliation took -- "checks 730-739
// (730-737 used)" -- and each check belongs to the task whose behaviour it
// measures: the owner runs it, and the section says so.
var mobileIdentityPairingChecks = []struct {
	number   string
	owner    string
	phrases  []string
	subject  string
	rowSays  string
	commands int
}{
	{
		number:  "730",
		owner:   "A3",
		subject: "the identity project, the signing key and the JWKS",
		phrases: []string{"identity project", "protection level", "jwks"},
		rowSays: "the identity project",
	},
	{
		number:  "731",
		owner:   "A3",
		subject: "the provider consoles and what the identity project accepts",
		phrases: []string{"signinwithidp", "provider", "u8", "u9"},
		rowSays: "the provider",
	},
	{
		number:  "732",
		owner:   "Z0",
		subject: "the operator's Claude Code session",
		phrases: []string{"context", "compact", "u1", "u2", "u3"},
		rowSays: "claude code session",
	},
	{
		number:  "733",
		owner:   "M0",
		subject: "two real phones",
		phrases: []string{"iphone", "android", "keychain", "u4", "u5", "u7", "u10"},
		rowSays: "two real phones",
	},
	{
		number:  "734",
		owner:   "M0",
		subject: "the store builds and the share extension",
		phrases: []string{"share extension", "store", "declaration"},
		rowSays: "store build",
	},
	{
		number:  "735",
		owner:   "UP1",
		subject: "the live sequence after `fugaro update`",
		phrases: []string{"fugaro update", "refresh", "revoke", "restore", "u11", "u12"},
		rowSays: "fugaro update",
	},
	{
		number:  "736",
		owner:   "A3",
		subject: "the quotas at the stated scale",
		phrases: []string{"quota", "u6"},
		rowSays: "quota",
	},
	{
		number:  "737",
		owner:   "A4",
		subject: "the root key project and the first signed key set",
		phrases: []string{"root key", "key set", "cannot sign", "o7", "o15"},
		rowSays: "root key",
	},
}

// mobileIdentityPairingCommandBlock returns the body of a check section's ONE
// ```sh block, and fails when the section carries none or more than one. The
// plan asks for one command each, and the block is where a reader copies it
// from: a second block is a second command by another name.
func mobileIdentityPairingCommandBlock(t *testing.T, section, number string) string {
	t.Helper()
	const fence = "```sh"
	if !strings.Contains(section, fence) {
		t.Fatalf("Check %s's section carries no %s block: the check is run from its command, and a section without one has no steps at all", number, fence)
	}
	if strings.Count(section, fence) != 1 {
		t.Fatalf("Check %s's section carries %d %s blocks, and the plan asks for one command each: the check is one command the owner runs", number, strings.Count(section, fence), fence)
	}
	rest := section[strings.Index(section, fence)+len(fence):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("Check %s's %s block is never closed", number, fence)
	}
	return rest[:end]
}

// TestDocsMobileIdentityPairingChecksAreRegistered: Checks 730-737 are in the
// register twice over -- a summary row and a `## Check 73N:` section -- each
// owner-run, each saying it has not been run, each saying no agent runs any step
// of it, each keeping itself off the `belong` installation, and each carrying
// **one command**: the plan's acceptance sentence for T0 asks for exactly that,
// and a check written as a procedure rather than a command is the hand work the
// register's own convention replaced.
//
// Mutation (verified, run and restored): delete one check's section and this
// test fails naming the number; add a second command to a block and it fails
// naming the block; drop "no agent runs any step of it" and it fails naming the
// section; remove the register paragraph and it fails with the reading it
// records.
func TestDocsMobileIdentityPairingChecksAreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	// The register paragraph, which is where the numbers' own reading is
	// recorded: the block is claimed past every claim above it, and a paragraph
	// that lost that sentence would leave the register unable to say why 730 is
	// this work's.
	const claim = "**Checks 730 to 737 are the mobile-identity-pairing work's"
	i := strings.Index(register, claim)
	if i < 0 {
		t.Fatalf("%s carries no %q paragraph: the eight checks are claimed there, and the register's rule is that the paragraph that takes a block records the reading it took", liveChecksDoc, claim)
	}
	paragraph := register[i:]
	if end := strings.Index(paragraph, "\n\n"); end >= 0 {
		paragraph = paragraph[:end]
	}
	paragraph = flattenDoc(paragraph)
	for _, want := range []string{
		// The reading itself, in the register's own words.
		"the first free number past every claim",
		"claimed, not reserved",
		// The claims the reading steps over, so a later reader can re-run it.
		"700", "703", "710", "712", "720", "729",
		// What the block is: eight owner-run checks, one command each, and a
		// paragraph that says who may run them.
		"one command",
		"no agent runs any step",
		"never `belong`",
		"NOT RUN",
	} {
		if !strings.Contains(paragraph, want) {
			t.Errorf("%s's Checks 730-737 paragraph does not say %q: the paragraph is the whole of what this block's numbers mean, and a reading that lost a clause is a reader who cannot re-run it", liveChecksDoc, want)
		}
	}

	for _, check := range mobileIdentityPairingChecks {
		row := checkRow(register, check.number)
		if row == "" {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check.number, check.number)
			continue
		}
		flatRow := strings.ToLower(flattenDoc(row))
		for _, want := range []string{"not run", "no agent runs any step", "never `belong`", check.rowSays} {
			if !strings.Contains(flatRow, want) {
				t.Errorf("Check %s's row does not carry %q: the row is the whole check's registration, and a reader takes a row that lost one of these for a check that was run, by someone, somewhere", check.number, want)
			}
		}

		section := checkSection(t, register, check.number)
		// Flattened and folded, because the register's prose wraps: a pin that
		// missed a phrase across a line break would be a pin on the line breaks.
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"never `belong`",
			"mobile-identity-pairing/implementation-plan.md",
			"one command",
			check.owner,
		} {
			if !strings.Contains(flat, strings.ToLower(want)) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles, whose task it measures and how to run it", check.number, want)
			}
		}
		for _, phrase := range check.phrases {
			if !strings.Contains(flat, strings.ToLower(phrase)) {
				t.Errorf("Check %s's section no longer says %q: that phrase is what the number was taken to measure, and a check that stopped measuring it is a number a reader trusts for nothing", check.number, phrase)
			}
		}

		// One command, and the block's own shape: no line but the command, and
		// the blanks and comments a reader needs to follow it.
		block := mobileIdentityPairingCommandBlock(t, section, check.number)
		var commands []string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			commands = append(commands, line)
		}
		if len(commands) != 1 {
			t.Errorf("Check %s's command block carries %d command lines, and the plan asks for one command each: %q", check.number, len(commands), commands)
		}
		// The checks read; none of them writes. A command that creates, deletes
		// or applies something is the one thing this block may not hold: the
		// register's checks are run by the owner against an installation, and a
		// step that changes it is the owner's to type deliberately.
		if len(commands) == 1 {
			for _, verb := range []string{" apply", " delete ", "gcloud projects create", "fugaro setup", "terraform"} {
				if strings.Contains(" "+commands[0], verb) {
					t.Errorf("Check %s's command carries %q: these checks read what a deploy left behind, and a command that writes is not a check", check.number, strings.TrimSpace(verb))
				}
			}
		}
	}
}
