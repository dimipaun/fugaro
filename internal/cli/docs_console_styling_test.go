package cli

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TestDocsConsoleStylingDesignIsWritten:
// the design that gives the Electron console a stylesheet exists, carries its
// eleven sections, states in its own first line that nothing follows it, and
// keeps every claim that makes it reviewable — the evidence that the console
// ships no stylesheet at all, the screen inventory every rule is written
// against, the two token sets with their computed ratios, the state rule that
// no state is carried by colour alone, the single-switch theme, the wiring that
// is admitted by the CSP directive already in force, and the owner's six open
// questions. Its red state is this test: the document did not exist.
//
// The document is docs-only work: it changes no `.css`, no Go, no TypeScript
// and no entry of the handler's served-file list, which is why the first string
// below is the status line rather than a mood.
func TestDocsConsoleStylingDesignIsWritten(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The status line, and the rule that nothing follows the document.
		"**Status: design only; nothing is implemented.**",
		"It does not change the CSP",
		"no `.css` file",
		// The sections, in order, as headings: a section that is renamed or
		// dropped must fail here rather than become a citation that dangles.
		"## 1. The problem, and the evidence",
		"## 2. Screen inventory",
		"## 3. The theme system",
		"## 4. Layout",
		"## 5. Wiring (SECURITY-SENSITIVE)",
		"## 6. Tests",
		"## 7. The build plan (Phase 6, UNAPPROVED)",
		"## 8. Open for the owner",
		"## 9. Alternatives considered",
		"## 10. Design brief for the Claude Design canvas",
		"## 11. What this document does not decide",
		// The evidence: no stylesheet, no served entry, no bundler step.
		"SERVED_FILES",
		"No inline style attribute",
		// The theme: two sets, the OS default, one switch, no colour alone.
		"prefers-color-scheme",
		"light-dark(",
		"Never colour alone",
		"KILLED by",
		"NOTIONAL~",
		"(partial)",
		"tabular-nums",
		"forced-colors",
		// The wiring, and the directive that already admits it.
		"is already the directive a stylesheet needs",
		"mediaTypeFor",
		"text/css; charset=utf-8",
		"console.css",
		// The alternatives, priced.
		"adoptedStyleSheets",
		// The plan, the owner, and the number this document does not take.
		"Task 6.2",
		"No D-number is allocated",
		"Q6",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsConsoleStylingCitationsExist: the design's citations are paths a
// reader has to find — the palette the light set is derived from, the site's
// token layer and the contrast check the design's own test mirrors, and the
// documents it is read against. A substring assertion cannot see one of them
// move, so each is stat'ed here as well as required in the text.
//
// **Every citation this test reads is outside the console directory, and that
// is the boundary rather than a choice about value.** The design cites the
// console's own files by their full paths — the protocol handler it does not
// change, the screens its inventory was read from, the bundler that will emit
// the stylesheet, the four unit tests its wiring section extends — and this
// test does not stat them, because `TestConsoleIsOutsideTheGoModule`'s
// allow-list is a ONE-entry exception (Task 0.3 a3: the console contract's own
// docs test) and a second Go file mentioning a path under that directory would
// be a deliberate widening of it. Those citations are checked by the reader the
// design is written for, and the design says so in its own §6.5; the boundary
// is worth more than the stat.
//
// Deliberately not listed either: everything the design proposes and does not
// find — the four theme stylesheets, the token test and the golden-frame
// directory — because a file a design creates is not a file a design cites, and
// stat'ing one would make this test fail on the document doing its job.
func TestDocsConsoleStylingCitationsExist(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The palette the light set is derived from, the site's token layer,
		// and the contrast check the design's own token test is written in the
		// shape of (so the arithmetic is one implementation, not two).
		"website/src/styles/tokens.css",
		"website/src/styles/palette-logo.css",
		"website/test/a11y.mjs",
		// The documents it is read against.
		"docs/design/desktop-console.md",
		"docs/design/desktop-console-v2.md",
		"docs/plans/2026-10-15-desktop-console.md",
		"docs/website/redesign/reconciliation.md",
	} {
		// The document links its neighbours relatively (../plans/…,
		// ../../website/…), so a citation counts in either form.
		rel, err := filepath.Rel("docs/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", path, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", path, cited, err)
		}
	}
}

// TestDocsConsoleStylingAnchorsResolve: every in-page link in the design
// (`](#…)`) resolves to a heading in the design.
//
// The gap this closes is the one the citation test above cannot: a citation test
// stats a *file*, and a dead `#fragment` is not a file. Three of the design's
// anchors were wrong when this test was added — two missing the second hyphen an
// em dash leaves in a GitHub slug (`— the wiring — SECURITY-SENSITIVE` slugs to
// `…the-wiring--security-sensitive`), and one left over from an earlier revision
// of §7.5 naming a heading that had been rewritten — and every one of them
// rendered as a link that went nowhere while the suite stayed green. A document
// whose own table of contents does not resolve is a document a reader has to
// trust rather than follow, and this design's whole shape is cross-references.
//
// The slug rule is GitHub's, as far as this document needs it: lowercase, drop
// everything that is not a letter, a digit, a space, a hyphen or an underscore,
// then spaces to hyphens. That is why an em dash leaves two hyphens behind
// rather than one.
func TestDocsConsoleStylingAnchorsResolve(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)

	slugs := map[string]int{}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(heading, " ") {
			continue
		}
		slug := mdHeadingSlug(heading)
		if slug == "" {
			t.Errorf("%s: the heading %q slugs to the empty string, so nothing can link to it", path, line)
			continue
		}
		slugs[slug]++
	}
	for slug, n := range slugs {
		if n > 1 {
			t.Errorf("%s: %d headings slug to %q: GitHub disambiguates them with a `-1` suffix, so only the first is reachable by the name the document would write", path, n, slug)
		}
	}

	for _, m := range mdAnchorLink.FindAllStringSubmatch(doc, -1) {
		target := m[1]
		// An anchor to another document's section (`../plans/x.md#y`) is that
		// document's business, and this test does not read it.
		if strings.Contains(m[0], ".md#") {
			continue
		}
		if _, ok := slugs[target]; !ok {
			t.Errorf("%s: `%s` resolves to no heading: the link goes nowhere, and no citation test can see it because a fragment is not a file", path, m[0])
		}
	}
}

// mdAnchorLink finds the in-page links of a Markdown document: the URL of a
// `](#…)` link, which is the only form that has to resolve inside the file.
var mdAnchorLink = regexp.MustCompile(`\]\(#([^)\s]+)\)`)

// mdHeadingSlug is GitHub's heading-slug rule, as far as a document of this
// repository's shape needs it: lowercase, keep letters, digits, spaces, hyphens
// and underscores, drop everything else, then turn spaces into hyphens. An
// em dash is dropped and the spaces on either side of it become two hyphens,
// which is the detail the three links this test was written for got wrong.
func mdHeadingSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

// TestDocsConsoleStylingPlanTasksAreUnapproved (the console plan's phase 6,
// added 2026-10-19 with the design above):
// the plan carries the styling work in its own numbering and carries it as
// UNAPPROVED. Two things have to hold together and neither is visible from the
// other — the six tasks exist as headings, and the file says in the phase's own
// heading, its summary row and its PR rows that the owner has not approved any
// of them. The wiring task's `SECURITY-SENSITIVE` flag is checked by
// TestPlanRule9ListsEveryFlaggedHeading, which is why this test does not repeat
// rule 9's list; what it adds is that the flag is on a task that *exists* in a
// phase that *says* it is unapproved.
func TestDocsConsoleStylingPlanTasksAreUnapproved(t *testing.T) {
	const path = "docs/plans/2026-10-15-desktop-console.md"
	plan := readDoc(t, path)
	for _, want := range []string{
		// The phase, in the file's numbering, marked unapproved in its heading.
		"## Phase 6 — The console's style — UNAPPROVED",
		// The six tasks. 6.2's heading is the flagged one.
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring — SECURITY-SENSITIVE, independent opus review",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch, the window background and the keyboard",
		"### Task 6.6: The golden frames, re-recorded",
		// The four stylesheets, in the tree the plan promised from before this
		// design existed — the file the tree named and no task created.
		"src/renderer/theme/tokens.css",
		"src/renderer/theme/palette.css",
		"src/renderer/theme/base.css",
		"src/renderer/theme/screens.css",
		// The authority the phase defers to, and the gate it does not pass.
		"docs/design/console-styling.md",
		"These six tasks are `UNAPPROVED`",
		"OWNER APPROVAL GATE",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q: phase 6's tasks are the styling design's build plan, and a task that leaves the plan must leave this test", path, want)
		}
	}

	// The design's six questions, named where a reader of the plan finds them:
	// a question answered by silently dropping it would be a task that starts
	// without the gate it names.
	section := func(start, end string) string {
		i := strings.Index(plan, start)
		if i < 0 {
			t.Fatalf("plan has no %q", start)
		}
		rest := plan[i:]
		if j := strings.Index(rest[len(start):], end); j >= 0 {
			return rest[:len(start)+j]
		}
		return rest
	}("## Phase 6 — The console's style — UNAPPROVED", "\n## Cross-task dependencies")
	for _, q := range []string{"**Q1**", "**Q2**", "**Q3**", "**Q4**", "**Q5**", "**Q6**"} {
		if !strings.Contains(section, q) {
			t.Errorf("%s: phase 6 no longer names %s among the questions the owner decides before it starts", path, q)
		}
	}
	// And the rule that keeps it out of the first release until the owner says
	// otherwise — the design's recommendation, stated in the plan itself.
	if !strings.Contains(plan, "**Phase 6 — the console's style — is not on this list**") {
		t.Errorf("%s: the MVP list no longer says phase 6 is not on it, which is the design's recommendation (its Q6) and would otherwise read as an omission", path)
	}

	// The other two places the file can say a phase is unapproved, because the
	// docstring above claims all three and the heading alone is the one a reader
	// who never scrolls to the tables would check. The plan's own preamble
	// promises a Phase-summary row and the phase's PR rows, so this is also the
	// check that the promise landed.
	if !strings.Contains(plan, "| **6. The console's style — UNAPPROVED** |") {
		t.Errorf("%s: the Phase summary has no row for phase 6 marked UNAPPROVED; the phase is listed in that table like every other one, and a row without the marker is a phase a reader takes as approved", path)
	}
	prRows := mdTableRows(plan, mdPRRowRE)
	if len(prRows) != 5 {
		t.Errorf("%s: the PR groups table holds %d rows for phase 6 (J to J5); the phase is six tasks split into five PRs, and this count is the number the design's §7 states", path, len(prRows))
	}
	for _, row := range prRows {
		if !strings.Contains(row, "UNAPPROVED") {
			t.Errorf("%s: the PR groups row %q does not say UNAPPROVED; every row the phase occupies carries the marker, since a PR group is what a reviewer merges", path, strings.TrimSpace(row))
		}
	}

	// And the tree lines and PR rows are the count the design's §7 prints, so
	// the description and the plan cannot drift apart: the design says four
	// lines for the four stylesheets and five PR rows, and both are counted here
	// rather than trusted.
	themeLines := mdTableRows(plan, mdThemeTreeLineRE)
	if len(themeLines) != 4 {
		t.Errorf("%s: the File Structure tree holds %d theme stylesheet lines, and the design's §7 says four; a tree line and the design's count are the same fact", path, len(themeLines))
	}
	design := readDoc(t, "docs/design/console-styling.md")
	for _, want := range []string{
		"**five** rows in the PR groups table",
		"**four** lines in the File Structure tree",
		"**six** `### Task 6.n:` headings",
	} {
		if !strings.Contains(mdFlat(design), want) {
			t.Errorf("docs/design/console-styling.md: §7 does not say %q; that sentence is how the design tells a reader where the phase landed, and each of its counts is a fact about the plan that this test just counted", want)
		}
	}
}

// mdPRRowRE matches a PR groups row for phase 6 — the phase's rows are the J
// ones, and nothing else in that table starts with a J.
var mdPRRowRE = regexp.MustCompile(`(?m)^\| J\d* \|[^\n]*$`)

// mdThemeTreeLineRE matches a File Structure line for one of the theme
// stylesheets, which are the four files the styling work adds.
var mdThemeTreeLineRE = regexp.MustCompile(`(?m)^ {2}src/renderer/theme/[a-z]+\.css\b[^\n]*$`)

// mdTableRows returns every line of doc the expression matches, with the
// expression's own anchors left on, so a caller can search the row it gets.
func mdTableRows(doc string, re *regexp.Regexp) []string {
	return re.FindAllString(doc, -1)
}

// mdTaskSections cuts a document's task list into one string per task, in task
// order. The markers are each task's heading followed by the marker that ends
// the last one, so the piece between heading n and heading n+1 is task n's whole
// body — scope, files, tests, command and the prose around them.
func mdTaskSections(t *testing.T, path, doc string, marks ...string) []string {
	t.Helper()
	parts := mdCut(t, path, doc, marks...)
	if len(parts) != len(marks)+1 || len(parts) < 8 {
		t.Fatalf("%s: %d pieces for %d markers; this test reads a task list of six tasks plus the marker that ends it", path, len(parts), len(marks))
	}
	return parts[1:len(marks)]
}

// mdClauseStartRE opens one of a task's labelled clauses, in either document's
// shape: the plan writes them as paragraphs (`**Files.** …`), the design as list
// items (`- **Files:** …`).
var mdClauseStartRE = regexp.MustCompile(`^(?:- )?\*\*(Scope|Files|Tests|Command|Verification)\b`)

// mdClause returns the clause of a task's body that opens with the given label —
// `**Files.**`, `**Tests:**` and their kin — with the markdown wrapping flattened
// to single spaces, so a caller can search the sentence rather than the markup
// and does not break when the paragraph is rewrapped. The second result says
// whether the clause is there at all; the callers below require one label and
// take the others as they come, because a task that states no files is a task
// this test would silently stop checking, while a task whose tests are the specs
// it lists (Task 6.6, the recording) has no `**Tests**` clause.
func mdClause(t *testing.T, path, section, label string) (string, bool) {
	t.Helper()
	lines := strings.Split(section, "\n")
	for i := 0; i < len(lines); i++ {
		m := mdClauseStartRE.FindStringSubmatch(lines[i])
		if m == nil || m[1] != label {
			continue
		}
		// The clause runs to the next labelled clause, or to the end of its
		// paragraph. "Paragraph" is spelled differently in the two documents —
		// the plan's clauses are paragraphs at column zero, the design's are
		// list items whose continuations are indented — so the end is a blank
		// line followed by an unindented one, which is the next paragraph in
		// the plan and the next heading in the design.
		body := []string{lines[i]}
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			if mdClauseStartRE.MatchString(line) {
				break
			}
			if strings.TrimSpace(line) == "" {
				if k := mdNextNonBlank(lines, j+1); k < 0 || !strings.HasPrefix(lines[k], " ") {
					break
				}
			}
			body = append(body, line)
		}
		return mdFlat(strings.Join(body, "\n")), true
	}
	return "", false
}

// mdNextNonBlank is the index of the first line at or after i that is not blank,
// or -1 when the rest of the section is blank.
func mdNextNonBlank(lines []string, i int) int {
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

// mdFlat collapses every run of whitespace to one space. Both documents wrap
// their prose at eighty columns, so a claim worth pinning is a claim that is
// broken across lines; searching the raw text would pin the wrap as well as the
// words, and a rewrap would fail a test about the meaning.
func mdFlat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// mdForwardRefRE matches a named task and the number it carries.
var mdForwardRefRE = regexp.MustCompile(`Task 6\.([1-6])`)

// TestDocsConsoleStylingPhaseOrdering:
// **no task may depend on a later one, and this is the round that found two
// that did.** The plan promised that each of phase 6's six tasks is
// independently mergeable and green on its own, and two of them were not:
//
//   - Task 6.2 landed `THEME_STYLESHEETS` as the whole four-file list while
//     Tasks 6.3 and 6.4 had not yet created two of the four, and the bundler
//     refuses a **listed** file that is missing — so a merge of 6.2 as written
//     left `npm run build` red. The list is the theme directory's whole
//     contents, so it is introduced with what is in the directory (Task 6.1's
//     two files) and grows one entry per task, in the commit that creates the
//     file. The design's §5.2 is where that is stated, and the array literal
//     there is what this test reads.
//   - the main-process window-background assertion was assigned to Task 6.1's
//     `theme-tokens.test.ts` while `backgroundColor` itself arrives in Task
//     6.5's window module. A case asserting a key that does not exist yet is
//     not a case that task owns, so it moved to 6.5.
//
// The general rule the two share is checked directly rather than by name: a
// task's **Files** and **Tests** paragraphs must not name a later task, because
// a paragraph that names one is a paragraph whose subject is that task's work.
// Prose that points forward — "not in this task; Task 6.4 adds that case" — is
// how the plan deliberately explains a deferral and is not a clause this reads.
// What it does read is the pair of paragraphs that say what a task touches and
// what proves it, and for those the rule is absolute.
func TestDocsConsoleStylingPhaseOrdering(t *testing.T) {
	const planPath = "docs/plans/2026-10-15-desktop-console.md"
	const designPath = "docs/design/console-styling.md"
	plan, design := readDoc(t, planPath), readDoc(t, designPath)

	planTasks := mdTaskSections(t, planPath, plan,
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch",
		"### Task 6.6: The golden frames",
		"## Cross-task dependencies")
	designTasks := mdTaskSections(t, designPath, design,
		"### 7.1 Task 6.1",
		"### 7.2 Task 6.2",
		"### 7.3 Task 6.3",
		"### 7.4 Task 6.4",
		"### 7.5 Task 6.5",
		"### 7.6 Task 6.6",
		"### 7.7 The red steps")

	// The general rule, over both documents: Files and Tests never name a task
	// that comes after the one they are in.
	for _, doc := range []struct {
		path  string
		tasks []string
	}{{planPath, planTasks}, {designPath, designTasks}} {
		for i, task := range doc.tasks {
			for _, label := range []string{"Files", "Tests"} {
				clause, ok := mdClause(t, doc.path, task, label)
				if !ok {
					if label == "Files" {
						t.Errorf("%s: task 6.%d's body has no **Files** paragraph; every task states what it touches, and a task that stops stating it is a task this rule stops checking", doc.path, i+1)
					}
					continue
				}
				for _, m := range mdForwardRefRE.FindAllStringSubmatch(clause, -1) {
					later, err := strconv.Atoi(m[1])
					if err != nil {
						t.Fatalf("%s: %q is not a task number: %v", doc.path, m[1], err)
					}
					if later > i+1 {
						t.Errorf("%s: task 6.%d's **%s** paragraph names Task 6.%d, which comes after it; a task's files and its tests have to exist when it lands, so this is a dependency on a later task's work", doc.path, i+1, label, later)
					}
				}
			}
		}
	}

	// And the two findings, pinned by content rather than by the rule above.
	//
	// The list as §5.2 writes it is a prefix of the theme directory, not its
	// whole contents: two names, and the two Tasks 6.3 and 6.4 append.
	if !strings.Contains(mdFlat(design), "The list grows with the files") {
		t.Fatalf("%s: §5.2 no longer says the list grows with the files; without that paragraph the literal below is the only statement of how the list is built, and this test would pass on a snippet that no longer describes the list", designPath)
	}
	var list string
	if i := strings.Index(design, "export const THEME_STYLESHEETS"); i >= 0 {
		if j := strings.Index(design[i:], "])"); j >= 0 {
			list = design[i : i+j]
		}
	}
	if list == "" {
		t.Fatalf("%s: no THEME_STYLESHEETS literal in the document; §5.2 is where the list and its growth are stated", designPath)
	}
	for _, want := range []string{"'tokens.css'", "'palette.css'"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s: the THEME_STYLESHEETS literal does not hold %s; the list is the vocabulary and the values first, and those are the files that exist when it is introduced", designPath, want)
		}
	}
	for _, bad := range []string{"base.css", "screens.css"} {
		if strings.Contains(list, bad) {
			t.Errorf("%s: the THEME_STYLESHEETS literal holds %s, which no task has created yet at the point Task 6.2 lands the list — and the bundler refuses a listed file that is missing, so that merge is a red build rather than an early entry", designPath, bad)
		}
	}
	for _, tc := range []struct {
		task, file string
	}{{"6.3", "base.css"}, {"6.4", "screens.css"}} {
		files, ok := mdClause(t, planPath, planTasks[taskIndex(t, tc.task)], "Files")
		if !ok {
			t.Fatalf("%s: Task %s has no **Files** paragraph", planPath, tc.task)
		}
		for _, want := range []string{tc.file, "THEME_STYLESHEETS"} {
			if !strings.Contains(files, want) {
				t.Errorf("%s: Task %s's **Files** does not name %s; the file and its list entry land in the same commit, because a list that runs ahead of the directory fails the build", planPath, tc.task, want)
			}
		}
	}
	for _, bad := range []string{"base.css", "screens.css"} {
		for _, label := range []string{"Scope", "Files"} {
			if clause, _ := mdClause(t, planPath, planTasks[1], label); strings.Contains(clause, bad) {
				t.Errorf("%s: Task 6.2's **%s** names %s, and that file does not exist until a later task creates it", planPath, label, bad)
			}
		}
	}

	// The window background: the assertion is Task 6.5's, because the literals
	// are, and Task 6.1's paragraphs must not be about them.
	for _, label := range []string{"Scope", "Files", "Tests"} {
		if clause, _ := mdClause(t, planPath, planTasks[0], label); strings.Contains(clause, "backgroundColor") {
			t.Errorf("%s: Task 6.1's **%s** names backgroundColor, which arrives with Task 6.5's consoleWindowOptions; the case that compares the two literals against the two --ground values lands in the task that adds them", planPath, label)
		}
	}
	if clause, ok := mdClause(t, planPath, planTasks[4], "Scope"); !ok || !strings.Contains(clause, "backgroundColor") {
		t.Errorf("%s: Task 6.5's **Scope** does not name backgroundColor; it is the task that adds both literals to the main process's window module, so a reader of the task would not know it owns the assertion", planPath)
	}
	if !strings.Contains(mdFlat(design), "This case is [Task 6.5](#75-task-65--the-theme-switch-the-window-background-and-the-keyboard)'s and not Task 6.1's") {
		t.Errorf("%s: §6.3 does not say the window-background case is Task 6.5's; a design that leaves the owner unnamed is a design a reader can implement in the wrong task", designPath)
	}

	// And §6.5 counts this file, so the count is checked against the file rather
	// than trusted. The revision that fixed the ordering errors added the tenth
	// test and left the section at "nine tests", which is the same defect in the
	// other direction: a reader implementing the phase from the design writes
	// what the design enumerates, and the ordering test was in neither the count
	// nor the list.
	self, err := os.ReadFile("docs_console_styling_test.go")
	if err != nil {
		t.Fatalf("reading this test file to count its cases: %v", err)
	}
	cases := len(regexp.MustCompile(`(?m)^func TestDocsConsoleStyling\w*\(`).FindAll(self, -1))
	if cases == 0 {
		t.Fatalf("no TestDocsConsoleStyling case found in this file, which cannot be right: this test is one")
	}
	word, ok := mdNumberWord(cases)
	if !ok {
		t.Skipf("this file declares %d cases, more than §6.5's prose counts in words; enlarge mdNumberWord", cases)
	}
	if !strings.Contains(mdFlat(design), "with "+word+" tests") {
		t.Errorf("%s: §6.5 does not say this file has %s tests, and this file declares %d; the section's count and its enumerated list are what a reader implements the phase from, so a case that lands without a line there is a case nobody writes", designPath, word, cases)
	}
}

// mdNumberWord spells a small count the way §6.5's prose does, so the comparison
// is against the sentence rather than against a digit the document never prints.
func mdNumberWord(n int) (string, bool) {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
	if n < 0 || n >= len(words) {
		return "", false
	}
	return words[n], true
}

// mdWordValue reads a small count back out of the prose, so a sentence's word
// and the list beside it can be compared. It is mdNumberWord's inverse and
// covers the same range: zero to twenty.
func mdWordValue(word string) (int, bool) {
	for n := 0; n <= 20; n++ {
		if w, ok := mdNumberWord(n); ok && w == word {
			return n, true
		}
	}
	return 0, false
}

// TestDocsConsoleStylingCountsMatch:
// every count this design prints in prose is checked against the thing it
// counts, because a count is a number and a number in a document rots the way a
// link does — silently, and in whichever direction the sentence beside it needs.
// The revision this test was written from is the evidence: §6.5 said "five that
// read §3.4's numbers" over a list numbered 5 to 10, and a reader counting the
// items after that sentence counted six.
//
// The checks are mechanical on purpose — items, rows and clauses, counted by
// their markers rather than by reading the sentences — because a test that
// agreed with the prose would agree with any prose. What it does not do is check
// counts of things outside the document (§3.4's colours, the eight baselines,
// the console's own test files): those are other cases' subjects, and this one
// is about the document keeping its own arithmetic.
func TestDocsConsoleStylingCountsMatch(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)

	// §6.5's enumeration: the ordered list runs 1..N with no gap, and N is the
	// number the section's opening line says in words.
	section := mdBetween(t, path, doc, "### 6.5 The docs test", "\n---\n\n## 7. ")
	itemNums := regexp.MustCompile(`(?m)^(\d+)\. `).FindAllStringSubmatch(section, -1)
	if len(itemNums) == 0 {
		t.Fatalf("%s: §6.5 has no ordered list; that list is the enumeration the section's count is about", path)
	}
	for i, m := range itemNums {
		if m[1] != strconv.Itoa(i+1) {
			t.Errorf("%s: §6.5's list jumps from %d to %s; the items are numbered consecutively, and a gap is how an item leaves the list without the count noticing", path, i, m[1])
		}
	}
	stated := regexp.MustCompile(`with (\w+) tests`).FindStringSubmatch(mdFlat(section))
	if stated == nil {
		t.Fatalf("%s: §6.5 does not say how many tests this file has; the count and the enumeration are what a reader implements the phase from", path)
	}
	want, ok := mdWordValue(stated[1])
	if !ok {
		t.Fatalf("%s: §6.5's count %q is not a word this test knows", path, stated[1])
	}
	if want != len(itemNums) {
		t.Errorf("%s: §6.5 says %s tests and enumerates %d items; a count that disagrees with the list under it is the defect this case exists for", path, stated[1], len(itemNums))
	}

	// The group inside that list, which is where the count and the enumeration
	// last drifted apart: "Items 5 to 9 — five of the ten — read §3.4's numbers".
	group := regexp.MustCompile(`Items (\d+) to (\d+) — (\w+) of the (\w+) —`).FindStringSubmatch(mdFlat(section))
	if group == nil {
		t.Errorf("%s: §6.5 no longer names the run of items that read §3.4's numbers as a range; without the range a reader counts the items after the sentence and gets the count wrong, which is exactly what happened here", path)
	} else {
		lo, errLo := strconv.Atoi(group[1])
		hi, errHi := strconv.Atoi(group[2])
		size, okSize := mdWordValue(group[3])
		total, okTotal := mdWordValue(group[4])
		switch {
		case errLo != nil || errHi != nil || !okSize || !okTotal:
			t.Errorf("%s: §6.5's group sentence could not be read: %q", path, group[0])
		default:
			if size != hi-lo+1 {
				t.Errorf("%s: §6.5 says %s items and names the range %d to %d, which holds %d", path, group[3], lo, hi, hi-lo+1)
			}
			if total != len(itemNums) {
				t.Errorf("%s: §6.5's group sentence calls the list \"the %s\" and the list holds %d items", path, group[4], len(itemNums))
			}
		}
	}
	// The sentence under the list that names the same group again, in the words
	// a reader remembers: it has to be the group's own word.
	if m := regexp.MustCompile(`these (\w+) read the file`).FindStringSubmatch(mdFlat(section)); m != nil {
		if group == nil || m[1] != group[3] {
			t.Errorf("%s: §6.5 says \"these %s read the file\" and the group it refers to is counted differently above; the two sentences name the same items", path, m[1])
		}
	} else {
		t.Errorf("%s: §6.5 no longer says how many of its cases read the file rather than the tables", path)
	}

	// §7.7's three counts: the table of red steps, the docs test's own mutation
	// list, and the ordering test's. Each is counted by its separators — table
	// rows and semicolons — rather than by trusting the sentence above it.
	reds := mdBetween(t, path, doc, "### 7.7 The red steps", "**And the ")
	rows := 0
	for _, line := range strings.Split(reds, "\n") {
		if strings.HasPrefix(line, "| ") && !strings.Contains(line, "---") && !strings.Contains(line, "Red step |") {
			rows++
		}
	}
	if m := regexp.MustCompile(`The (\w+) that\s*\n?\s*matter most`).FindStringSubmatch(reds); m != nil {
		if n, ok := mdWordValue(m[1]); !ok || n != rows {
			t.Errorf("%s: §7.7 says the %s red steps that matter most are tabulated and the table has %d rows", path, m[1], rows)
		}
	} else {
		t.Errorf("%s: §7.7 no longer says how many red steps it tabulates", path)
	}

	// The two mutation lists: each says a count in words and then lists that many
	// clauses, separated by semicolons. The span is taken raw — from the sentence
	// to the paragraph or the sentence that ends it — because a flattened
	// document has no paragraph ends left, and these lists carry code spans with
	// periods in them ("`Status: draft.`"), which a sentence-splitting regexp
	// would cut in half.
	for _, tc := range []struct {
		sentence string
		countRE  string
		start    string
		end      string
	}{
		{
			sentence: "this document's own docs test was proved against",
			countRE:  `\*\*And the (\w+) this document's own docs test was proved against`,
			start:    "**And the four this document's own docs test was proved against",
			end:      "The seventh is the plan's",
		},
		{
			sentence: "the ordering test's mutations",
			countRE:  `carries (\w+) of its own`,
			start:    "And the ordering test (§6.5's tenth)",
			end:      "\n---\n",
		},
	} {
		span := mdBetween(t, path, doc, tc.start, tc.end)
		m := regexp.MustCompile(tc.countRE).FindStringSubmatch(span)
		if m == nil {
			t.Errorf("%s: §7.7 no longer states the count for %s as a sentence this case can count", path, tc.sentence)
			continue
		}
		n, ok := mdWordValue(m[1])
		if !ok {
			t.Errorf("%s: §7.7's count for %s is %q, not a word this test knows", path, tc.sentence, m[1])
			continue
		}
		after := span[strings.Index(span, m[0])+len(m[0]):]
		if i := strings.Index(after, ":"); i >= 0 {
			after = after[i+1:]
		}
		clauses := len(strings.Split(strings.TrimSpace(after), ";"))
		if clauses != n {
			t.Errorf("%s: §7.7 says %s for %s and lists %d clauses; a count that disagrees with the list beside it is the defect this case exists for", path, m[1], tc.sentence, clauses)
		}
	}

	// And the document's own shape: §6.5's first item counts the section
	// headings, so the count is taken from the headings themselves.
	headings := len(regexp.MustCompile(`(?m)^## `).FindAllString(doc, -1))
	if m := regexp.MustCompile(`this document's (\w+) section headings`).FindStringSubmatch(mdFlat(doc)); m != nil {
		if n, ok := mdWordValue(m[1]); !ok || n != headings {
			t.Errorf("%s: §6.5 says this document has %s section headings and it has %d", path, m[1], headings)
		}
	} else {
		t.Errorf("%s: §6.5's presence case no longer counts this document's section headings", path)
	}
}

// mdBetween returns the part of doc that starts at start, the marker included,
// and stops before end. Both markers must be there: a missing one is a document
// this test can no longer read, and a silent empty slice would make every check
// over it vacuous. The markers are raw — newlines and all — because the callers
// count what is between them, and a flattened copy has no line ends left to
// count.
func mdBetween(t *testing.T, path, doc, start, end string) string {
	t.Helper()
	i := strings.Index(doc, start)
	if i < 0 {
		t.Fatalf("%s: %q is not in the document; this test reads what follows it", path, start)
	}
	rest := doc[i:]
	j := strings.Index(rest[len(start):], end)
	if j < 0 {
		t.Fatalf("%s: %q does not end before %q, so this test cannot tell where the section stops", path, start, end)
	}
	return rest[:len(start)+j]
}

// taskIndex maps a task's name to its position among phase 6's six tasks, so a
// case can name the task it is about rather than an index into the cut.
func taskIndex(t *testing.T, task string) int {
	t.Helper()
	for i, name := range []string{"6.1", "6.2", "6.3", "6.4", "6.5", "6.6"} {
		if name == task {
			return i
		}
	}
	t.Fatalf("taskIndex: %q is not one of phase 6's six tasks", task)
	return 0
}

// --- the computed numbers of §3.4 -----------------------------------------

//
// The design's §3.4 is a table of numbers, and a number in a document rots the
// way a link does — silently, and in the direction of whatever the sentence
// beside it needs. The first revision of the design is the evidence: it
// asserted that the five state colours were "≥ 60° apart from each other in
// both sets", and that was false in both sets (the dark set's `--warn`/`--error`
// pair was 37.0° apart, its `--halted`/`--stale` pair 55.0°), while every
// contrast figure beside it was right.
//
// So the four tests below recompute §3.4 from the literal values in §3.2 and
// §3.3 with WCAG 2.1's relative-luminance definition, the same arithmetic
// `website/test/a11y.mjs` uses; assert the requirement §3.4 states for each
// token's role; and check §3.2's accounting of which tokens are new against the
// website's palette file rather than against the sentence. **A literal changed
// in §3.2 or §3.3, and the number printed beside it left alone, fails here** —
// which is the red step for all three findings of the review this test was
// written against.
//
// The arithmetic is written out rather than shared, because it is four lines
// and the point is that the test and the document agree on the formula. It is
// deliberately not the console's own token test (§6.3): that one is TypeScript
// on the other side of TestConsoleIsOutsideTheGoModule's one-entry allow-list,
// which is what keeps a Go file from reaching in there at all — the same
// boundary that makes this file cite the console's files as text and never stat
// them. A path under that directory written into a comment here is enough to
// fail that test, which is how this paragraph came to be rewritten.

// mdRGB parses a six-digit sRGB hex literal into its three channel values.
func mdRGB(t *testing.T, hex string) [3]float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("not a six-digit hex literal: %q", hex)
	}
	var out [3]float64
	for i := range out {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("not a six-digit hex literal: %q", hex)
		}
		out[i] = float64(v)
	}
	return out
}

// mdLinear is one sRGB channel's linear value, the transfer function WCAG 2.1
// defines relative luminance over.
func mdLinear(c float64) float64 {
	c /= 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// mdLum is WCAG 2.1's relative luminance of a six-digit hex literal.
func mdLum(t *testing.T, hex string) float64 {
	t.Helper()
	c := mdRGB(t, hex)
	return 0.2126*mdLinear(c[0]) + 0.7152*mdLinear(c[1]) + 0.0722*mdLinear(c[2])
}

// mdRatio is the WCAG contrast ratio between two literals, lighter one first:
// (L1+0.05)/(L2+0.05).
func mdRatio(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := mdLum(t, a), mdLum(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// mdHue is the HSL hue of a six-digit sRGB hex literal, in degrees. A neutral
// has no hue and returns 0; the document never asks for one, because the one
// neutral in either set is `--surface #FFFFFF`, which is only ever a surface.
func mdHue(t *testing.T, hex string) float64 {
	t.Helper()
	c := mdRGB(t, hex)
	r, g, b := c[0]/255, c[1]/255, c[2]/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	d := max - min
	if d == 0 {
		return 0
	}
	var h float64
	switch max {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h
}

// mdHueGap is the circular distance between two hues in degrees — the smaller
// of |Δh| and 360−|Δh| — which is the distance §3.4's pair tables are about.
func mdHueGap(t *testing.T, a, b string) float64 {
	t.Helper()
	d := math.Abs(mdHue(t, a) - mdHue(t, b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

// mdSeparatedArm names the arm of §3.4's disjunction a pair takes — "hue" when
// the two hues are at least 50° apart, "luminance" when they are not but the
// luminance contrast between them is at least 3:1 — and whether the pair takes
// either arm at all. Both thresholds are inclusive: the rule is written "at
// least 50°", "at least 3:1".
func mdSeparatedArm(gap, contrast float64) (arm string, separated bool) {
	if gap >= 50 {
		return "hue", true
	}
	if contrast >= 3 {
		return "luminance", true
	}
	return "", false
}

// mdCut splits s at the given markers, which must appear in that order, and
// returns the pieces between them (one more piece than markers). A marker that
// is missing or out of order fails rather than silently slicing the wrong
// region: these markers are the sub-headings and table captions of one section,
// and a section that has been reordered is exactly what this would otherwise
// misread.
func mdCut(t *testing.T, path, s string, marks ...string) []string {
	t.Helper()
	out := make([]string, 0, len(marks)+1)
	off := 0
	for _, m := range marks {
		i := strings.Index(s[off:], m)
		if i < 0 {
			t.Fatalf("%s: %q is missing, or out of order, in the section being read", path, m)
		}
		out = append(out, s[off:off+i])
		off += i + len(m)
	}
	return append(out, s[off:])
}

// The four table shapes §3.2–§3.4 are written in: a value-set row, a
// three-surface contrast row, the decorative `--border` row (whose ratios are
// deliberately not bold, because they are deliberately not admissible), the
// ink-on-fill row, and a state-pair row.
var (
	mdTokenRowRE   = regexp.MustCompile("(?m)^\\| `(--[a-z-]+)` \\| `(#[0-9A-F]{6})` \\|")
	mdSurfaceRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\|")
	mdDecorativeRowRE = regexp.MustCompile(
		"(?m)^\\| `(--border)` \\(decorative\\) \\| ([0-9.]+) \\| ([0-9.]+) \\| ([0-9.]+) \\|")
	mdFillRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\|")
	mdPairRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` / `(--[a-z-]+)` \\| ([0-9.]+)° \\| ([0-9.]+) \\| ([a-z]+) \\|")
)

// mdTokens reads a value set out of its §3.2/§3.3 table: the rows that pair a
// token name with its opaque six-digit literal. A name declared twice with two
// different values fails, because the rest of these tests would otherwise pick
// whichever row came last and be right by accident.
func mdTokens(t *testing.T, path, section string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range mdTokenRowRE.FindAllStringSubmatch(section, -1) {
		if prev, dup := out[m[1]]; dup && prev != m[2] {
			t.Errorf("%s: %s is declared twice with different values (%s and %s)", path, m[1], prev, m[2])
		}
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("%s: no token rows in the section being read; its table was renamed or reshaped and this test needs updating", path)
	}
	return out
}

// mdStylingDoc returns the document and its four load-bearing regions: the two
// value sets and §3.4's tables. Every test below starts here rather than
// re-finding the same markers.
func mdStylingDoc(t *testing.T) (doc string, dark, light map[string]string, contrast string) {
	t.Helper()
	const path = "docs/design/console-styling.md"
	doc = readDoc(t, path)
	theme := mdCut(t, path, doc,
		"### 3.2 The dark value set",
		"### 3.3 The light value set",
		"### 3.4 Contrast, computed",
		"### 3.5 Never colour alone")
	return doc, mdTokens(t, path, theme[1]), mdTokens(t, path, theme[2]), theme[3]
}

// mdSurfaceOrder is the three painted backgrounds every token is measured
// against, in the column order §3.4 prints them.
var mdSurfaceOrder = []string{"--ground", "--surface", "--surface-alt"}

// mdStateOrder is the five state colours, in the order §3.4's pair tables list
// them: the order the table is written in, so a pair dropped from it is a pair
// the completeness check below can name.
var mdStateOrder = []string{"--ok", "--warn", "--error", "--halted", "--stale"}

// TestDocsConsoleStylingContrastComputes: every ratio §3.4 prints is the ratio
// the literal values in §3.2 and §3.3 compute, to two decimals; every token
// clears the requirement its role carries (4.5:1 for body text, 3:1 for a
// control's boundary); and `--border` is still below 3:1, because §3.4 prints
// it as decorative and a hairline that cleared 3:1 would be a boundary the
// design should have named as one.
func TestDocsConsoleStylingContrastComputes(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc, dark, light, contrast := mdStylingDoc(t)

	parts := mdCut(t, path, contrast,
		"**Dark**", "**Light**", "**A label on a filled control**",
		"**State separation, computed.**",
		"**Dark: the ten pairs of state colours**",
		"**Light: the ten pairs of state colours**",
		"Circular hue distance")
	darkSurfaces, lightSurfaces, inkTable := parts[1], parts[2], parts[3]

	// The two surface tables, printed ratio by printed ratio.
	for _, tc := range []struct {
		set, name, table string
		tokens           map[string]string
	}{
		{"the dark set", "dark", darkSurfaces, dark},
		{"the light set", "light", lightSurfaces, light},
	} {
		rows := mdSurfaceRowRE.FindAllStringSubmatch(tc.table, -1)
		if len(rows) != 10 {
			t.Fatalf("%s: §3.4's %s table has %d token rows, want 10 (every token but the four that are only ever text on a fill)", path, tc.name, len(rows))
		}
		for _, m := range rows {
			name := m[1]
			hex, declared := tc.tokens[name]
			if !declared {
				t.Errorf("%s: §3.4's %s table prints %s, which §3.2/§3.3 does not declare", path, tc.name, name)
				continue
			}
			for i, surface := range mdSurfaceOrder {
				got, err := strconv.ParseFloat(m[2+i], 64)
				if err != nil {
					t.Fatalf("%s: %s on %s: %v", path, name, surface, err)
				}
				if want := mdRatio(t, hex, tc.tokens[surface]); math.Abs(got-want) > 0.005 {
					t.Errorf("%s: §3.4 prints %s on %s as %s:1, and the literal %s computes %.2f:1", path, name, surface, m[2+i], hex, want)
				}
			}
		}

		// The decorative row, printed unbolded and checked all the same.
		dec := mdDecorativeRowRE.FindStringSubmatch(tc.table)
		if dec == nil {
			t.Fatalf("%s: §3.4's %s table no longer carries a `--border (decorative)` row", path, tc.name)
		}
		for i, surface := range mdSurfaceOrder {
			got, err := strconv.ParseFloat(dec[2+i], 64)
			if err != nil {
				t.Fatalf("%s: --border on %s: %v", path, surface, err)
			}
			want := mdRatio(t, tc.tokens["--border"], tc.tokens[surface])
			if math.Abs(got-want) > 0.005 {
				t.Errorf("%s: §3.4 prints --border on %s as %s:1, and the literal computes %.2f:1", path, surface, dec[2+i], want)
			}
			if want >= 3 {
				t.Errorf("%s: the %s set's --border on %s is %.2f:1; §3.4 prints it as decorative, and a border that clears 3:1 is a control's boundary and must be said to be one", path, tc.name, surface, want)
			}
		}

		// The requirements, from the computed ratios rather than the printed
		// ones: a hex moved in §3.2 or §3.3 fails here even when the table has
		// been updated to match it.
		for _, token := range []string{"--text", "--text-muted", "--accent-ink", "--accent",
			"--ok", "--warn", "--error", "--halted", "--stale"} {
			for _, surface := range mdSurfaceOrder {
				if r := mdRatio(t, tc.tokens[token], tc.tokens[surface]); r < 4.5 {
					t.Errorf("%s: the %s's %s on %s is %.2f:1, and 4.5:1 is what body-size text needs", path, tc.name, token, surface, r)
				}
			}
		}
		for _, token := range []string{"--accent", "--border-strong"} {
			for _, surface := range mdSurfaceOrder {
				if r := mdRatio(t, tc.tokens[token], tc.tokens[surface]); r < 3 {
					t.Errorf("%s: the %s's %s on %s is %.2f:1, and 3:1 is what a boundary marking a control's extent needs", path, tc.name, token, surface, r)
				}
			}
		}
	}

	// The ink-on-fill table: the ink is the dark set's `--ground` and the light
	// set's `--surface`, which is what §3.4 says it is, and every fill clears
	// 4.5:1 against it because a label on a fill is text.
	fills := mdFillRowRE.FindAllStringSubmatch(inkTable, -1)
	if len(fills) != 6 {
		t.Fatalf("%s: §3.4's ink-on-fill table has %d rows, want 6 (the accent and the five states)", path, len(fills))
	}
	for _, m := range fills {
		name := m[1]
		for i, tc := range []struct {
			set  map[string]string
			ink  string
			name string
		}{
			{dark, dark["--ground"], "`--ground`"},
			{light, light["--surface"], "`--surface`"},
		} {
			got, err := strconv.ParseFloat(m[2+i], 64)
			if err != nil {
				t.Fatalf("%s: --ink-on-fill on %s: %v", path, name, err)
			}
			want := mdRatio(t, tc.ink, tc.set[name])
			if math.Abs(got-want) > 0.005 {
				t.Errorf("%s: §3.4 prints the light/dark ink on %s as %s:1, and %s on it computes %.2f:1", path, name, m[2+i], tc.ink, want)
			}
			if want < 4.5 {
				t.Errorf("%s: --ink-on-fill on --%s is %.2f:1, and a label is text", path, strings.TrimPrefix(name, "--"), want)
			}
		}
	}

	// The rule §3.4 states, pinned to the two numbers this test computes with:
	// the rule and the arithmetic cannot drift apart silently.
	if !strings.Contains(doc, "at least 50° apart in hue, _or_ at least 3:1") {
		t.Errorf("%s: §3.4 no longer states the 50°-or-3:1 rule this test checks the state colours against", path)
	}
}

// TestDocsConsoleStylingStateSeparation: the ten pairs of state colours in each
// set, as §3.4 prints them, are the ten pairs the literals make — the hue
// distance to a tenth of a degree, the luminance contrast to two decimals — and
// every one of them satisfies the rule the section states. This is the claim the
// first revision of the design got wrong, and the reason it is a table of
// computed numbers now rather than a sentence.
//
// The rule is a disjunction and this enforces it as one: **50° of hue, or 3:1 of
// luminance contrast where hue cannot separate the pair.** So the case fails on a
// pair that misses both, and on a row that claims the wrong arm, but it does not
// demand 3:1 of a pair that already clears 50° — the design does not state that,
// the ten present pairs do not meet it, and a test that pinned it would refuse a
// future token taking the documented fallback. The message it fails with is
// still the section's own: change the token, not the rule.
func TestDocsConsoleStylingStateSeparation(t *testing.T) {
	const path = "docs/design/console-styling.md"
	_, dark, light, contrast := mdStylingDoc(t)
	parts := mdCut(t, path, contrast,
		"**Dark**", "**Light**", "**A label on a filled control**",
		"**State separation, computed.**",
		"**Dark: the ten pairs of state colours**",
		"**Light: the ten pairs of state colours**",
		"Circular hue distance")
	for _, tc := range []struct {
		set   map[string]string
		name  string
		table string
	}{
		{dark, "dark", parts[5]},
		{light, "light", parts[6]},
	} {
		rows := mdPairRowRE.FindAllStringSubmatch(tc.table, -1)
		if len(rows) != 10 {
			t.Fatalf("%s: the %s set's pair table has %d rows, want 10: the five state colours have ten pairs, and a pair dropped from the table is a pair this test would stop checking", path, tc.name, len(rows))
		}
		seen := map[string]bool{}
		for _, m := range rows {
			a, b := m[1], m[2]
			if _, ok := tc.set[a]; !ok {
				t.Errorf("%s: the %s pair table names %s, which §3.2/§3.3 does not declare", path, tc.name, a)
			}
			if _, ok := tc.set[b]; !ok {
				t.Errorf("%s: the %s pair table names %s, which §3.2/§3.3 does not declare", path, tc.name, b)
			}
			seen[a+" "+b] = true

			gap := mdHueGap(t, tc.set[a], tc.set[b])
			if got, err := strconv.ParseFloat(m[3], 64); err != nil {
				t.Fatalf("%s: %s/%s: %v", path, a, b, err)
			} else if math.Abs(got-gap) > 0.05 {
				t.Errorf("%s: the %s pair table prints %s / %s as %.1f° apart, and the literals compute %.1f°", path, tc.name, a, b, got, gap)
			}

			contrast := mdRatio(t, tc.set[a], tc.set[b])
			if got, err := strconv.ParseFloat(m[4], 64); err != nil {
				t.Fatalf("%s: %s/%s: %v", path, a, b, err)
			} else if math.Abs(got-contrast) > 0.005 {
				t.Errorf("%s: the %s pair table prints %s / %s at %s:1 luminance contrast, and the literals compute %.2f:1", path, tc.name, a, b, m[4], contrast)
			}

			// §3.4's rule is a disjunction — 50° of hue, **or** 3:1 of luminance
			// contrast where hue cannot separate the pair — so this accepts the
			// documented fallback rather than pinning one arm of it. A pair that
			// fails both is the one thing the rule forbids, and a pair that
			// clears the first need not clear the second: none of the ten does
			// today, and demanding it would be a rule the design does not state.
			// The decision itself is mdSeparatedArm, which the case below tests
			// on its own — no pair in the document takes the fallback, so a
			// mutation of the document alone could not reach that arm.
			arm, separated := mdSeparatedArm(gap, contrast)
			if !separated {
				t.Errorf("%s: the %s set's %s and %s are %.1f° apart and %.2f:1 apart; §3.4's rule needs one of the two — 50° of hue, or 3:1 of luminance as the fallback — so change the token, not the rule", path, tc.name, a, b, gap, contrast)
			}
			// And the row says which arm it took, so a fallback pair cannot be
			// tabulated as if hue had separated it.
			if separated && m[5] != arm {
				t.Errorf("%s: the %s pair table says %s / %s are separated by %q, and the literals say %q (%.1f° of hue, %.2f:1 of luminance)", path, tc.name, a, b, m[5], arm, gap, contrast)
			}
		}
		for i, a := range mdStateOrder {
			for _, b := range mdStateOrder[i+1:] {
				if !seen[a+" "+b] {
					t.Errorf("%s: the %s pair table omits %s / %s", path, tc.name, a, b)
				}
			}
		}
	}
}

// TestDocsConsoleStylingSeparationArms: the two arms of §3.4's rule, on their
// own. The document's own ten pairs per set all take the hue arm — every state
// colour has to clear 4.5:1 as text on all three surfaces, which puts them all
// within a narrow luminance band, so no pair of the five present colours is 3:1
// apart in luminance — and so no edit to the document alone can exercise the
// fallback branch. Testing the decision directly is what makes it a tested
// branch rather than dead code, and pins both thresholds as inclusive, which is
// how §3.4 writes them.
func TestDocsConsoleStylingSeparationArms(t *testing.T) {
	for _, tc := range []struct {
		gap, contrast float64
		arm           string
		separated     bool
		why           string
	}{
		{120, 1.1, "hue", true, "hue alone separates these"},
		{50, 1.1, "hue", true, "the rule is \"at least 50°\", so 50° clears it"},
		{49.9, 3.2, "luminance", true, "under 50° of hue, but 3:1 is what the fallback needs and this pair is over it"},
		{49.9, 3, "luminance", true, "the rule is \"at least 3:1\", so exactly 3:1 clears it"},
		{49.9, 2.99, "", false, "under both thresholds, which is the one thing §3.4 forbids"},
		{0, 1, "", false, "identical colours are the far end of the same failure"},
		{0, 21, "luminance", true, "nil hue separation is the case the fallback exists for"},
	} {
		arm, separated := mdSeparatedArm(tc.gap, tc.contrast)
		if separated != tc.separated || arm != tc.arm {
			t.Errorf("%.1f° of hue and %.2f:1 of luminance: mdSeparatedArm says (%q, %v), and §3.4 says (%q, %v) — %s", tc.gap, tc.contrast, arm, separated, tc.arm, tc.separated, tc.why)
		}
	}
}

// TestDocsConsoleStylingTokenProvenance: §3.2's accounting of which tokens are
// new is checked against the website's palette file, not against the sentence.
// The first revision said "the six tokens the console needs added: the second
// surface, the control border and the five state colours" and named the wrong
// six — `--warn` is the website's, so there are seven values reused, one reused
// under another name and six new. Reading palette-logo.css is what makes the
// words "reused" and "new" checkable at all; without it they are a claim about
// a file nothing in this repository reads.
func TestDocsConsoleStylingTokenProvenance(t *testing.T) {
	_, dark, _, _ := mdStylingDoc(t)
	site := readDoc(t, "website/src/styles/palette-logo.css")

	// The seven the console copies from the website, byte for byte.
	for name, hex := range map[string]string{
		"--ground":     "#0A0E16",
		"--surface":    "#121A26",
		"--text":       "#E6EEF5",
		"--text-muted": "#9FB2C4",
		"--accent":     "#17A8E6",
		"--border":     "#24303F",
		"--warn":       "#F5A524",
	} {
		if dark[name] != hex {
			t.Errorf("%s: §3.2 declares %s as %q, want %q, the website's value", "docs/design/console-styling.md", name, dark[name], hex)
		}
		if !strings.Contains(site, name+": "+hex+";") {
			t.Errorf("palette-logo.css no longer declares %s: %s; §3.2 reuses it byte for byte, and that reuse is the claim this test checks", name, hex)
		}
	}

	// The one the console reuses under another name: the website names the
	// accent-as-text tone for its one ground, and the console's two grounds make
	// that name wrong here.
	if ink := dark["--accent-ink"]; !strings.Contains(site, "--accent-on-dark: "+ink+";") {
		t.Errorf("palette-logo.css's --accent-on-dark is not %s, the value §3.2's --accent-ink reuses under its new name", ink)
	}

	// The six the console invents: none of their literals may already be in the
	// website palette, or §3.2's word "new" is wrong about that one.
	for _, name := range []string{"--surface-alt", "--border-strong", "--ok", "--error", "--halted", "--stale"} {
		hex := dark[name]
		if hex == "" {
			t.Errorf("docs/design/console-styling.md: §3.2 declares no %s", name)
			continue
		}
		if strings.Contains(site, hex) {
			t.Errorf("docs/design/console-styling.md: §3.2 calls %s (%s) new, and palette-logo.css already carries that literal", name, hex)
		}
	}

	// And the count the accounting adds up to, so a fifteenth token cannot be
	// added to the table without the sentence being revisited.
	if n := len(dark); n != 14 {
		t.Errorf("docs/design/console-styling.md: §3.2 declares %d tokens; the section's accounting is seven reused, one renamed and six new, which is fourteen", n)
	}
}

// TestDocsConsoleStylingAccentOnALightGround: §3.3's correction is a claim
// about two different backgrounds, and both are computed here. `#17A8E6` is
// 2.70:1 on **white** — the figure reconciliation §5.3 records, and the figure
// the first revision of §3.3 attributed to "a light ground" in general — and
// 2.49:1 on this set's own `--ground` `#F2F6FA`, which is the number this
// design actually has to answer for, and 2.31:1 on `--surface-alt`. The prose
// is pinned to the two figures and to the tone the design names in their place,
// because the whole finding was that the right number was in the wrong place.
func TestDocsConsoleStylingAccentOnALightGround(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc, dark, light, _ := mdStylingDoc(t)

	accent := dark["--accent"]
	if accent != "#17A8E6" {
		t.Fatalf("%s: §3.2's --accent is %q; this test is about the website's #17A8E6 and the light grounds it cannot be text on", path, accent)
	}
	if got := mdRatio(t, accent, "#FFFFFF"); math.Abs(got-2.70) > 0.005 {
		t.Errorf("#17A8E6 on white computes %.2f:1, want 2.70:1, the figure reconciliation §5.3 records for white", got)
	}
	if got := mdRatio(t, accent, light["--ground"]); math.Abs(got-2.49) > 0.005 {
		t.Errorf("#17A8E6 on the light set's own --ground %s computes %.2f:1, want 2.49:1, the figure §3.3 now states", light["--ground"], got)
	}

	// The accent itself, on a light ground, is text; and the design says so.
	for _, surface := range mdSurfaceOrder {
		if r := mdRatio(t, light["--accent"], light[surface]); r < 4.5 {
			t.Errorf("%s: the light set's --accent on %s is %.2f:1, and §3.3's own rule is that the light accent may be text on all three surfaces", path, surface, r)
		}
	}

	for _, want := range []string{
		// Each figure beside the background it belongs to.
		"`#17A8E6` on **white** is\n**2.70:1**",
		"`#F2F6FA` the same value is **2.49:1**",
		// And the token the light set names for accent text, since the accent
		// itself cannot be it.
		"**`--accent-ink` `#085A82`** is nevertheless the tone for accent text",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	if ink := light["--accent-ink"]; ink != "#085A82" {
		t.Errorf("%s: the light set's --accent-ink is %q, want #085A82, the tone §3.3 names for accent text on a light ground", path, ink)
	}
}
