package cli

// The console v2 design's decisions, pinned (desktop-console-v2 plan, V2.0.1
// and V2.0.2).
//
// The v2 design (docs/design/desktop-console-v2.md) carries eighteen decisions,
// D21 to D38, and the owner answered every one of them on 2026-10-15/16. An
// answer that lives only in a table cell is an answer a later edit can drop:
// these three tests are what makes "answered" a fact about the document rather
// than a claim about it. They are the shape the v1 design's D-table already has
// (TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers in
// docs_desktop_console_test.go), extended by the one thing v2's plan asks for
// and v1's table does not carry: the answer names the task that implements it.
//
// The v1 file is also the ONE Go file allowed to name a path under the
// console's directory (Task 0.3's boundary test); everything here reads docs/
// only.
//
//  * TestDocsDesktopConsoleV2AnswersEveryDecision -- all eighteen are present,
//    each carrying a dated answer, and the four argued in prose are exactly the
//    four that have no table row.
//  * TestDocsDesktopConsoleV2AnswersNameTheTaskThatImplementsThem -- each
//    answer's `Implementation:` clause says what the mapping below says, and
//    every task it names exists in the plan.
//  * TestDocsDesktopConsoleV2CheckNumberReRead -- V2.0.2's record: what
//    `origin/main` carried when the re-read was run, and that Check 62 did not
//    move.
//
// Mutations this file is written to fail on, each run and restored when it was
// added: deleting a decision's table row (test 1, row count and the key set);
// blanking an answer cell (test 1); changing an `Implementation:` clause to name
// a task that does not exist, or deleting the clause (test 2); deleting the
// re-read paragraph, or editing 62 out of it (test 3).

import (
	"regexp"
	"strings"
	"testing"
)

const (
	consoleV2Design = "docs/design/desktop-console-v2.md"
	consoleV2Plan   = "docs/plans/2026-10-15-desktop-console-v2.md"
)

// consoleV2Decisions is the whole set, in order, and it is the pin: eighteen
// numbers, D21 to D38, no nineteenth and none missing.
var consoleV2Decisions = []string{
	"D21", "D22", "D23", "D24", "D25", "D26", "D27", "D28", "D29",
	"D30", "D31", "D32", "D33", "D34", "D35", "D36", "D37", "D38",
}

// consoleV2ProseDecisions are the four the design argues in prose above the
// table "because a cell cannot hold the reasoning". D21 is the one the plan
// singles out ("D21's in its prose paragraph, since it has no row"); the other
// three are D25, D29 and D31.
var consoleV2ProseDecisions = []string{"D21", "D25", "D29", "D31"}

// consoleV2Implementation is V2.0.1's mapping, written once: the decision, and
// the task in the plan whose answer implements it. "" is a decision whose
// answer is a NON-TASK -- the clause in the document must then say so in words
// rather than leave the cell looking unfinished:
//
//   - D24 builds no registry ("not yet, and not as a new identity");
//   - D34's pair is out of the first slice, so V2.5.2 keeps its number and
//     waits on a revisited answer;
//   - D37 is a release placement, carried by the plan's own release paragraph;
//   - D38 is a sequencing rule, carried by the plan's phase order.
//
// The plan's other tasks (V2.0.1, V2.0.2, V2.3.1, V2.4.1, V2.7.1) are not the
// implementation of a decision and are correctly absent here: V2.0.1 and V2.0.2
// are this file, V2.3.1 draws what V2.2.3 computed, V2.4.1 needs D27's read but
// implements the comparison rather than the decision, and V2.7.1 needs nothing
// beyond v1.
var consoleV2Implementation = map[string]string{
	"D21": "V2.5.1",
	"D22": "V2.5.1",
	"D23": "V2.5.1",
	"D24": "",
	"D25": "V2.2.1",
	"D26": "V2.2.3",
	"D27": "V2.1.2",
	"D28": "V2.2.2",
	"D29": "V2.6.1",
	"D30": "V2.5.3",
	"D31": "V2.1.1",
	"D32": "V2.7.2",
	"D33": "V2.7.3",
	"D34": "",
	"D35": "V2.7.2",
	"D36": "V2.8.1",
	"D37": "",
	"D38": "",
}

// consoleV2Table is the design's table read as a table: the decision, and its
// answer cell (the row's LAST cell, so a column inserted before it does not
// break the read).
func consoleV2Table(t *testing.T) map[string]string {
	t.Helper()
	doc := readDoc(t, consoleV2Design)
	rows := docTableAfter(t, consoleV2Design, doc, "| Owner's answer |")
	answers := map[string]string{}
	for _, cells := range rows {
		key := strings.Trim(cells[0], "*")
		if !regexp.MustCompile(`^D3[0-8]$|^D2[0-9]$`).MatchString(key) {
			t.Errorf("%s's decision table carries a row for %q, which is not one of D21-D38", consoleV2Design, cells[0])
			continue
		}
		answers[key] = cells[len(cells)-1]
	}
	return answers
}

// consoleV2Prose returns the prose paragraph of each of the four decisions that
// have no table row, keyed by decision number.
func consoleV2Prose(t *testing.T) map[string]string {
	t.Helper()
	doc := readDoc(t, consoleV2Design)
	paras := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, d := range consoleV2ProseDecisions {
			if strings.HasPrefix(line, "**"+d+" ") {
				paras[d] = line
			}
		}
	}
	return paras
}

// TestDocsDesktopConsoleV2AnswersEveryDecision: eighteen decisions, eighteen
// answers, and the four argued in prose are exactly the four without a row. A
// row deleted, a row added, or an answer blanked fails here by name.
func TestDocsDesktopConsoleV2AnswersEveryDecision(t *testing.T) {
	answers := consoleV2Table(t)
	prose := consoleV2Prose(t)

	if len(consoleV2Decisions) != 18 {
		t.Fatalf("this test's own list carries %d decisions, want the eighteen D21-D38", len(consoleV2Decisions))
	}
	for _, d := range consoleV2Decisions {
		cell, inTable := answers[d]
		para, inProse := prose[d]
		switch {
		case inTable && inProse:
			t.Errorf("%s carries %s twice: a table row and a prose paragraph", consoleV2Design, d)
		case !inTable && !inProse:
			t.Errorf("%s carries no answer for %s: neither a row nor a prose paragraph", consoleV2Design, d)
		case inTable:
			if strings.TrimSpace(strings.Trim(cell, "*")) == "" {
				t.Errorf("%s leaves %s unanswered: its answer cell is %q", consoleV2Design, d, cell)
				continue
			}
			// The dated answer, in the design's own opening: D22's and D34's in
			// the owner's words of 2026-10-16, the rest as answered on
			// 2026-10-15/16.
			if !strings.HasPrefix(cell, "**Owner answered 2026-10-1") {
				t.Errorf("%s's %s answer no longer opens with the owner's dated answer: %q", consoleV2Design, d, cell)
			}
		case inProse:
			if !strings.Contains(para, "Answered 2026-10-1") {
				t.Errorf("%s's %s paragraph no longer carries a dated answer: %q", consoleV2Design, d, para)
			}
		}
	}
	if len(answers) != 18-len(consoleV2ProseDecisions) {
		t.Errorf("%s's decision table carries %d rows, want the fourteen D21-D38 that are not argued in prose", consoleV2Design, len(answers))
	}
	for _, d := range consoleV2ProseDecisions {
		if _, ok := answers[d]; ok {
			t.Errorf("%s gives %s a table row, but the plan reads its answer from the prose paragraph", consoleV2Design, d)
		}
	}
}

// TestDocsDesktopConsoleV2AnswersNameTheTaskThatImplementsThem is V2.0.1's
// second half: the answer text names the task in the plan that implements it.
// The clause is checked against the mapping above, and every task it names is
// checked against the plan -- so a clause that names a task the plan does not
// carry fails here rather than reading as a promise nobody made.
func TestDocsDesktopConsoleV2AnswersNameTheTaskThatImplementsThem(t *testing.T) {
	plan := readDoc(t, consoleV2Plan)
	answers := consoleV2Table(t)
	prose := consoleV2Prose(t)

	for _, d := range consoleV2Decisions {
		text, ok := answers[d]
		if !ok {
			text = prose[d]
		}
		if text == "" {
			t.Errorf("%s carries no answer text for %s", consoleV2Design, d)
			continue
		}
		// The clause's own subject: the bolded token right after the marker, so
		// a clause may carry a sentence after it (the four non-tasks and D21
		// do) and the subject is still read as one value.
		m := regexp.MustCompile(`\*Implementation: (\*\*[^*]+\*\*)`).FindStringSubmatch(text)
		if m == nil {
			t.Errorf("%s's %s answer names no implementing task: no *Implementation: ...* clause", consoleV2Design, d)
			continue
		}
		want := consoleV2Implementation[d]
		if want == "" {
			if m[1] != "**no task**" {
				t.Errorf("%s's %s answer is a non-task, so its clause must say so in words: it reads %q", consoleV2Design, d, m[1])
			}
			continue
		}
		if got, wantClause := m[1], "**"+want+"**"; got != wantClause {
			t.Errorf("%s's %s answer names %q, want the task that implements it, %q", consoleV2Design, d, got, wantClause)
			continue
		}
		// The task must be one the plan actually carries, read as the plan
		// writes its headings: "**V2.2.3 — readiness, ...".
		if !strings.Contains(plan, "**"+want+" —") {
			t.Errorf("%s's %s answer names %s, which %s does not carry as a task", consoleV2Design, d, want, consoleV2Plan)
		}
	}
}

// TestDocsDesktopConsoleV2CheckNumberReRead is V2.0.2: the re-read of
// `origin/main` is a record in the plan's section 2, it says what the tree now
// carries, and it says that this plan's check did not move. The numbers here
// are what the re-read found on 2026-10-18; a later section landing changes
// them, and the task that finds that out is the one that re-runs the re-read
// and edits this test with it.
func TestDocsDesktopConsoleV2CheckNumberReRead(t *testing.T) {
	plan := readDoc(t, consoleV2Plan)
	// The plan wraps its lines, so the claims are compared against the section
	// read as one run of whitespace-collapsed text: a claim that survives
	// re-wrapping is the claim, not the line breaks.
	flat := strings.Join(strings.Fields(plan), " ")

	for _, want := range []struct {
		why  string
		text string
	}{
		{"the re-read is dated and names the commit it read",
			"**The re-read, done — `origin/main` at `85758e1`, 2026-10-18 (V2.0.2).**"},
		{"the highest section written is 69b, not 47",
			"**What `main` carries now: 34 `## Check` sections — 19, 20–38, 40–47, 49, 50, 52, 58, 69a and 69b — and a summary table whose highest row is `69b`.**"},
		{"the numbers still without a section are named",
			"**39, 48, 51, 53–57 and 59–68 still have no `## Check` section**"},
		{"60 and 61 are still not this plan's",
			"**60 is still the budget-token channel's**"},
		{"and 61 is still the folder export's",
			"**61 is still the monorepo folder export's**"},
		{"the table's own stale row says so",
			"**stale, and superseded by the re-read below: `origin/main` at `85758e1` carries sections through `69b`**"},
		{"the false \"no Check 60 anywhere in docs\" sentence is corrected where it stands",
			"no `Check 60` and no `Check 61` appear anywhere under `docs/`, which **was already false when this plan was written**"},
		{"and the narrower sentence that is true is the one stated",
			"**no `## Check 60` section is written into the checklist**"},
		{"the re-read moves no number",
			"**No number moves.**"},
		{"and this plan's check is 62, before and after",
			"**this plan's check is 62** — which no document has written and which this re-read does not take."},
		{"62 is still reserved to this plan and 63 still held",
			"| **62** | **this plan's** — the one owner-run check in §4 |"},
		{"and 63 is still held rather than spent",
			"| **63** | **this plan's second slot, and it is held rather than spent**"},
	} {
		if !strings.Contains(flat, want.text) {
			t.Errorf("%s no longer carries %s:\n  want %q", consoleV2Plan, want.why, want.text)
		}
	}

	// The two numbers this plan may not write, restated where a task would look
	// for them. The sentence is the plan's own and predates the re-read.
	if !strings.Contains(flat, "**Nothing in this document reserves 60 or 61, and no task may write them.**") {
		t.Errorf("%s no longer forbids writing 60 or 61", consoleV2Plan)
	}
}
