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
	"time"

	"github.com/dimipaun/fugaro/internal/failure"
)

// TestDocsDesignDocPinsTheHostOfThePRStateLookup is the review finding on the
// amendment drifting from the code: c8e230d pinned the host in the argv gh is
// given (--repo github.com/<owner>/<repo>, so a forwarded GH_HOST cannot move
// the question to another GitHub), and the amendment that documents that call —
// normative here, the code comments cite docs/design as the authority — still
// showed the hostless form, in the very sentence that lists the call's
// guarantees. A reader implementing from the spec would drop the host and
// reintroduce exactly the confusion the fix removed.
//
// The check is the real argv rather than a literal: watch is run against a fake
// gh over a github.com row, the repository gh was asked for is read out of it,
// and the doc has to name that same host in the same argument. So the doc and
// the code cannot drift apart in either direction, and the host is not written
// twice here for one of them to rot.
func TestDocsDesignDocPinsTheHostOfThePRStateLookup(t *testing.T) {
	// This test is about the argv gh was given, not about how fast a loaded
	// machine can fork and exec it: it owns the lookup's budget (and the
	// --once wait, which must outlast it) so a slow exec is never read as a
	// lookup that answered nothing.
	longPRStateBudget(t)
	f := newBudgetFixture(t, "")
	seedWatch(f, "mine")
	run := runID(10*time.Minute, "ff01")
	f.writeTask(t, appSlug, run, "")
	f.writeFinishedResult(t, appSlug, run, "succeeded", "ready", 42, "https://github.com/acme/app/pull/42", 10*time.Minute)
	calls := fakeWatchGH(t, "echo OPEN\n")

	plain, _, err := execute(t, "watch", "--once", "--plain")
	if err != nil {
		t.Fatal(err)
	}
	argv := ghWatchCalls(t, calls)
	if len(argv) != 1 {
		t.Fatalf("calls = %v, want one lookup of the ready row\nframe:\n%s", argv, plain)
	}
	repo := repoArgument(t, argv[0])
	// The property the doc has to carry: the repository gh is asked for is
	// named host-first. A bare owner/repo is resolved against gh's default
	// host, which GH_HOST sets — the bug the pin exists to prevent.
	parts := strings.Split(repo, "/")
	if len(parts) != 3 || !strings.Contains(parts[0], ".") {
		t.Fatalf("gh was asked for %q: the repository must carry its host (github.com/acme/app), not resolve against GH_HOST", repo)
	}

	doc := readDoc(t, "docs/design/generic-tool.md")
	line := docLine(doc, "gh pr view")
	if line == "" {
		t.Fatalf("the design doc no longer documents the gh call:\n%s", docLineish(doc))
	}
	// The doc's owner and repository are placeholders; the host is not, and it
	// is the host the code pins.
	want := "--repo " + parts[0] + "/<owner>/<repo>"
	if !strings.Contains(line, want) {
		t.Errorf("the amendment does not show the pinned call: want %q in the sentence that documents it:\n%s", want, line)
	}
	if !strings.Contains(line, "GH_HOST") {
		t.Errorf("the amendment gives the pinned host without saying what it is pinned against (GH_HOST):\n%s", line)
	}
}

// repoArgument is the value of gh's --repo in one recorded argv line, "" when
// the line has none.
func repoArgument(t *testing.T, call string) string {
	t.Helper()
	fields := strings.Fields(call)
	for i, a := range fields {
		switch {
		case a == "--repo" && i+1 < len(fields):
			return fields[i+1]
		case strings.HasPrefix(a, "--repo="):
			return strings.TrimPrefix(a, "--repo=")
		}
	}
	t.Fatalf("gh was run without --repo: %q", call)
	return ""
}

// docLine is the first line of doc containing want, "" when none does.
func docLine(doc, want string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

// fcDoc is the failure-class design. Its pin lives in this file rather than in
// a docs test file of its own because the tree's docs pin census counts the
// internal/cli/docs_*_test.go files, so a folder of its own would move a number
// the design promises not to move (D1462).
const fcDoc = "docs/design/failure-classes.md"

// fcClassRowRE is one row of the design's vocabulary table: a class name in
// backticks in the first cell. A snake_case name is the only shape that matches
// -- the table's other cells hold prose, and the other tables in the document
// open with a path or a word that is not one.
var fcClassRowRE = regexp.MustCompile("^\\| `([a-z][a-z_]+)` \\| ")

// TestDocsFailureClassDesignPinsTheVocabulary: the design says how far its
// plan has got -- FC1 built, the rest not -- its vocabulary is closed, the
// count it states is the vocabulary's own, and every claim a reader would act
// on -- one writer, the source of the table it is built on, the report, the
// legacy reader, the unchanged version, the census it promises not to move --
// is in it, in the words this test names.
//
// It is pinned because the document's whole value is that a reader can check
// it: a vocabulary that grew a twentieth value in prose while the table still
// said nineteen, or a design that quietly stopped saying which format versions
// move, is a document two implementers read differently.
func TestDocsFailureClassDesignPinsTheVocabulary(t *testing.T) {
	doc := readDoc(t, fcDoc)
	flat := flattenDoc(doc)

	// The state of the document first: a reader who skims it for a decision
	// must find the owner's answer, and a reader arriving after FC1 must find
	// that the vocabulary it describes is in the tree -- a status line that
	// reads as an open question while the code is on `main` is the defect
	// TestDocsProjectWorkflowSecretsSaysTheBuildLanded fails a document for.
	for _, want := range []string{
		"**Status: a design, and its FC1 has shipped; the rest of §10 is not built.**",
		// The design's own decisions stay the owner's: the plan's decisions being
		// answered is not the design's D1–D9 and O1–O6 being answered, which is
		// what README.md's and the plan's own notes about this document say.
		"the design's own decisions — §8 D1–D9 and §9 O1–O6 — stay his",
		// §10's own status, in the shape the coordinator plan's parts use: a
		// line above the tasks saying how far the plan has got.
		"**Status: a plan, approved to build on 2026-11-07; FC1 is built and FC2–FC9 are not started.**",
		"## 9. Open items (the owner's)",
		"## 11. What this design does not do",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer says %q: the document's status, its open items and the line between what it designs and what it does not are what a reader acts on", fcDoc, want)
		}
	}
	// The wording the flip replaced is banned rather than merely absent, as it
	// is in the design folder it was flipped in: a status line that reads as
	// an open question is one a reader does not act on, so a revert of the
	// flip has to fail here rather than read plausibly.
	for _, gone := range []string{"nothing is built", "no decision below is answered", "no task is approved"} {
		if strings.Contains(flat, gone) {
			t.Errorf("%s still says %q: FC1 -- the package, the record's field and the schema's enum -- is on `main`, and the status lines are where a reader checks the document against the build", fcDoc, gone)
		}
	}

	// The closed vocabulary: every class is a row of the table, the table holds
	// exactly the classes, in the order the design lists them, and a name in the
	// table is one of the classes.
	//
	// The classes are read from internal/failure rather than written out here: it
	// is the one slice the schema's enum and the report's table are read from, so
	// the document is held to the code that implements it -- and a class added to
	// one of the two alone is a design table, a schema enum and a report that
	// disagree about a vocabulary they all claim is closed.
	classes := failure.Classes()
	if len(classes) == 0 {
		t.Fatalf("internal/failure's Classes() is empty: the design's vocabulary table would be checked against nothing")
	}
	var rows []string
	for _, line := range strings.Split(doc, "\n") {
		if m := fcClassRowRE.FindStringSubmatch(line); m != nil {
			rows = append(rows, m[1])
		}
	}
	if len(rows) != len(classes) {
		t.Errorf("%s's vocabulary table holds %d class rows, want %d: the vocabulary is closed, so a row added or lost beside it is a change to the runner, the schema and the report at once", fcDoc, len(rows), len(classes))
	}
	for _, class := range classes {
		if !slices.ContainsFunc(rows, func(row string) bool { return row == string(class) }) {
			t.Errorf("%s's vocabulary table has no row for `%s`: the class is one internal/failure's Classes() carries, so the runner would write it and the report would print it", fcDoc, class)
		}
	}
	for _, name := range rows {
		if !slices.ContainsFunc(classes, func(c failure.Class) bool { return string(c) == name }) {
			t.Errorf("%s's vocabulary table holds `%s`, which is not one of the vocabulary's classes: a class the schema's enum does not carry is a value the record cannot hold", fcDoc, name)
		}
	}
	want := make([]string, len(classes))
	for i, c := range classes {
		want[i] = string(c)
	}
	if !slices.Equal(rows, want) {
		t.Errorf("%s's vocabulary table lists %v, internal/failure's Classes() lists %v: the order is the document's own -- the report prints the classes in it -- so a class inserted in one list alone is two renderings of one vocabulary", fcDoc, rows, want)
	}
	// The count the sentence states is read from the vocabulary, not pinned as a
	// word of its own: a class added to Classes(), the schema's enum, the
	// property table and this table leaves the word that counts them behind, and
	// the sentence would claim a set that no longer exists.
	counted := fmt.Sprintf("**%s values, and the set is closed in Go, in the schema and in the digest**", fcCountWord(len(classes)))
	if !strings.Contains(flat, counted) {
		t.Errorf("%s no longer says %q: the sentence is the design's own count of the vocabulary, and internal/failure's Classes() holds %d", fcDoc, counted, len(classes))
	}

	// The claims the implementation tasks rest on, each one a thing a reader
	// would otherwise take from the prose and get wrong.
	for _, want := range []string{
		// §3's own name for the slice, in the sentence that calls it the one
		// place the vocabulary is written: FC1 is what moved the design onto
		// this name, and a design that went back to naming a symbol the tree
		// does not carry would be a pin holding a name to nothing.
		"one slice the tests read (`failure.Classes()`, `internal/failure/failure.go`, built by FC1)",
		// Where the classes come from: a listing's own row, not a script.
		"`fugaro runs ls --since 7d --json`",
		// The field, and the sentence that it is additive.
		"`failure_class`",
		"stays `const 1`",
		// The one writer, named by the signature the task writes.
		"func (r *run) recordEnd(status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string)",
		"is the only",
		// The report: the grouping, the committed table, the two rules.
		"`fugaro runs ls --group-by failure_class`",
		"`FailuresTable`",
		"`failure_class_high`",
		"`failure_class_new`",
		// The legacy reader, and that its answer is never written back.
		"## 5. Legacy records: read from the reason, never written back",
		"func ClassOf(rec *runstore.Record) failure.Class",
		// The classifier over the agent's own message, and its boundary.
		"`stageFailureClass`",
		"never stored, quoted or returned",
		// HARD RULE: a field addition owes no operator step.
		"the whole upgrade is `fugaro update`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer says %q: an implementation run reads this document for exactly this, and the tasks in its section 10 are written against it", fcDoc, want)
		}
	}

	// The two censuses the design promises not to move, checked against the
	// tree rather than taken on the document's word: the backlog's own index,
	// and the count of docs test files that the pin's host is chosen to keep.
	backlog := flattenDoc(readDoc(t, "docs/backlog/README.md"))
	census := fcCensusRE.FindString(backlog)
	if census == "" {
		t.Fatalf("docs/backlog/README.md carries no census line: the design's promise that it files no item is checked against one")
	}
	// The design bolds the count itself, so the index's own period sits
	// outside the emphasis.
	if !strings.Contains(flat, "**"+strings.TrimSuffix(census, ".")+"**") {
		t.Errorf("%s no longer quotes the backlog's census (%q) as the number it leaves alone: the design files no item, and a document that keeps its promise by not mentioning it is a document whose promise nobody checks", fcDoc, census)
	}
	if !strings.Contains(flat, "internal/cli/docs_pr_state_test.go") || !strings.Contains(flat, "TestDocsRigorModesReconciliationCountsTheConfigManagementPin") {
		t.Errorf("%s no longer names the file its own pin lives in and the test that holds the docs-pin census it must not move", fcDoc)
	}
}

// fcCountWord is how the design's sentence spells a count: capitalised, because
// the sentence opens with it. The vocabulary is small enough that a table of
// words says it, and a count past the table falls back to digits -- a spelling
// the sentence does not use, so a vocabulary that outgrew it fails loudly
// rather than agreeing with itself.
func fcCountWord(n int) string {
	words := []string{"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine",
		"Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen",
		"Eighteen", "Nineteen", "Twenty"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// fcCensusRE is the backlog index's own census, which the design quotes and
// leaves alone: a count of items in each state, whatever the numbers are.
var fcCensusRE = regexp.MustCompile(`[0-9]+ items: [0-9]+ open, [0-9]+ closed, [0-9]+ dropped\.`)

// fcStatusWriterRE is one statement that assigns a terminal status to a
// record: the shape §1 of the design counts. The character class around the
// assignment is what keeps a comparison out of the census -- `rec.Status ==
// runstore.StatusRunning` and `rec.Status != runstore.StatusSucceeded` are
// both reads, and the first draft of the design's list was wrong precisely
// because it was made by eye rather than by this.
var fcStatusWriterRE = regexp.MustCompile(`\brec\.Status[^=!]*= runstore\.Status`)

// TestDocsFailureClassCensusMatchesTheRunner: the design says how many
// statements write a run's terminal status, in how many functions and files,
// and those three numbers are the census of the tree rather than a memory of
// it.
//
// The document's counts were wrong when it was written -- it said fifteen
// places and listed eighteen functions, while fourteen statements and a
// fourteen-helper list followed two paragraphs later -- and a design whose
// entire argument is "these fifteen writers become one" is worth nothing if
// the number is a recollection. Counting here is what makes the claim checkable
// in both directions: the runner gaining a writer makes the design and this pin
// fail together, which is the moment somebody has to decide whether the new
// statement goes through recordEnd.
func TestDocsFailureClassCensusMatchesTheRunner(t *testing.T) {
	paths, err := filepath.Glob("../../internal/runner/*.go")
	if err != nil {
		t.Fatal(err)
	}
	statements := 0
	files := map[string]bool{}
	functions := map[string]bool{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !fcStatusWriterRE.MatchString(line) {
				continue
			}
			statements++
			files[filepath.Base(path)] = true
			if fn := fcEnclosingFunc(lines[:i+1]); fn != "" {
				functions[fn] = true
			}
		}
	}
	if statements == 0 {
		t.Fatalf("no statement of internal/runner assigns a runstore.Status to a record: the census the design rests on found nothing, so this check passes vacuously")
	}
	want := fmt.Sprintf("written in %d statements, in %d functions across %d files", statements, len(functions), len(files))
	doc := flattenDoc(readDoc(t, fcDoc))
	if !strings.Contains(doc, want) {
		t.Errorf("%s no longer says %q: the design's first paragraph is a census of the runner's terminal-status writers, and %d of them in %d functions across %d files (%s) is what the tree holds",
			fcDoc, want, statements, len(functions), len(files), strings.Join(fcNames(files), ", "))
	}
}

// fcNames is a set's members, sorted, for a failure message a reader can act on.
func fcNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// fcEnclosingFunc is the name of the function the lines end inside: the last
// top-level declaration above them, "" when there is none. It is how the
// census counts functions rather than statements -- a count of statements alone
// would not notice a writer that moved into a helper.
func fcEnclosingFunc(upto []string) string {
	for i := len(upto) - 1; i >= 0; i-- {
		if !strings.HasPrefix(upto[i], "func ") {
			continue
		}
		rest := strings.TrimPrefix(upto[i], "func ")
		if strings.HasPrefix(rest, "(") {
			if cut := strings.Index(rest, ")"); cut >= 0 {
				rest = rest[cut+1:]
			}
		}
		rest = strings.TrimSpace(rest)
		if cut := strings.Index(rest, "("); cut >= 0 {
			rest = rest[:cut]
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// TestDocsFailureClassDesignCitationsResolve: every file and line the design
// cites exists, and the shortened names it uses resolve to exactly one file --
// both halves of it, the `path:line` citations and the `:NNN` references that
// name no file and mean the paragraph's nearest preceding one.
//
// It is the same check the other design documents carry, and it earned its keep
// on this one before it was written: the draft cited `:62` for `summaryStates`
// in a paragraph whose nearest preceding full citation was `userstats.go:198`,
// so a reader following the paragraph's own anchor landed in the history
// census. A citation nobody can resolve is a claim nobody can check.
func TestDocsFailureClassDesignCitationsResolve(t *testing.T) {
	bare := csrBareNames(t)
	doc := readDoc(t, fcDoc)
	// The census first, and independently: how many path-looking tokens with a
	// line the document holds at all. The check below has to see at least that
	// many, so a shape the prose uses and the regexp does not know fails here
	// rather than going unchecked.
	census := len(csrCensusRE.FindAllString(doc, -1))
	matches := csrCitationRE.FindAllStringSubmatch(doc, -1)
	if len(matches) < census {
		t.Fatalf("%s holds %d path-looking tokens carrying a line and the citation check matched %d of them: the document cites a shape csrCitationRE no longer knows, so those citations are unchecked", fcDoc, census, len(matches))
	}
	for _, m := range matches {
		file, ok := csrResolveCitation(m[1], bare)
		if !ok {
			t.Errorf("%s cites %q, which is not one file of this tree: a shortened name resolves only when exactly one file carries it", fcDoc, m[1])
			continue
		}
		line, err := strconv.Atoi(strings.TrimPrefix(m[2], ":"))
		if err != nil {
			t.Errorf("%s cites %q%s, whose line is not a number: %v", fcDoc, m[1], m[2], err)
			continue
		}
		body, err := os.ReadFile("../../" + filepath.FromSlash(file))
		if err != nil {
			t.Errorf("%s cites %q, which could not be read, so the line is unchecked: %v", fcDoc, m[1], err)
			continue
		}
		// The trailing newline every file ends with is not a line of it.
		lines := len(strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"))
		if line < 1 || line > lines {
			t.Errorf("%s cites %q%s, but %s holds %d lines: line %d is past the end of the file, so the citation names a line that is not there", fcDoc, m[1], m[2], file, lines, line)
		}
	}
	t.Logf("%s: %d citations carrying a line, against a census of %d path-looking tokens with one", fcDoc, len(matches), census)

	// The other half: a `:NNN` in this document names no file, so its meaning
	// is the paragraph's nearest preceding citation -- the shape a rewrap or a
	// reordered sentence silently repoints. Zero would mean the document
	// stopped using the shape and this half checked nothing.
	refs := csrCheckRelativeCitations(t, fcDoc, doc, bare)
	if refs == 0 {
		t.Errorf("%s holds no `:NNN` reference for the relative half of this check to resolve: the document cites lines that way, and a check that finds none is a check that passes vacuously", fcDoc)
	}
	t.Logf("%s: %d relative references resolved against their paragraph's anchor", fcDoc, refs)
}

// docLineish names the lines that mention gh, so a failure to find the sentence
// under test can be read: the doc may have been reworded, not wrong.
func docLineish(doc string) string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "gh ") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
