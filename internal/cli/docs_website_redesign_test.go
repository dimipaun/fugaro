package cli

// The website redesign's live check 70b, the gist above the fold (website redesign plan
// docs/website/redesign/implementation-plan.md §3 and §8, part P5; decision D187;
// reconciliation findings RF23 and RF5).
//
// 70b is the third of the redesign's three owner-run live checks and the one part P5 owed
// the register: P5 merged in #603 and shipped the ordering gate (website/test/fold.mjs)
// and its own revision of the word ceiling's lead reading (website/test/budget.mjs, which
// now measures the lead as the region between the `h1` and the first `h2`), but not the
// check that measures the fold itself, which the register's note under "Check 70 is the
// website redesign's" recorded as not in the file yet. The ceilings themselves are P4's
// (#572, D188), and the plan says so twice -- §3's constraint 4, "The word ceiling of P4
// goes there too, beside the byte budgets", and P4's own section of §3, "**The word
// ceiling, in `website/test/budget.mjs`**" -- while the reconciliation's RF23 puts it the
// other way round ("part **P4** of the plan writes the word ceiling either way"). The
// check's section attributes the gates to no part at all.
//
// The two tests on the entry pin its two halves the way TestWebsiteChecklistHasCheck69aAnd69b
// pins the website plan's 69a and 69b -- the row a reader meets first and the section it
// points at -- because a check that exists as a section but not as a row is a check nobody
// reads first, and a row with no section is a command nobody can follow. The three tests
// beside them hold the facts this comment and the section's own wording rest on: the plan's
// attribution of the ceilings to P4, the register's note about its block of three, and the
// selectors the check reads its numbers through.
//
// What the pair holds 70b to, beyond the shape every entry in the register has:
//
//   - **D187's answer is the check's shape.** The fold is a word ceiling plus one
//     owner-run measurement, because this repository's gates are deliberately
//     browser-free and no hermetic test can see a viewport. So the section reads its
//     numbers out of the tree -- the measurement script's keys and viewports, the
//     ceilings out of the README the gate reads, the provenance phrases out of the
//     gate's own list -- rather than carrying numbers that can go stale, and it names
//     the decision and the finding it answers.
//   - **It is the owner's and no agent's.** A check written as something an agent runs
//     is a check that will be run by an agent, and this one measures a first screen in
//     a browser this repository does not drive.
//   - **It changes nothing.** The section asks for no install into the tree and runs no
//     `fugaro` command: D187 kept the site's dependency set (the capture tooling's
//     browser is installed out of tree), and the fold is a fact about a page rather than
//     about a project.

import (
	"strings"
	"testing"
)

// checklist70bRow is the row's own prefix, and checklist70bHeading the section's: the two
// halves this file pins, named once so the pair cannot drift apart.
const (
	checklist70bRow     = "| 70b |"
	checklist70bHeading = "## Check 70b:"
)

// TestWebsiteChecklistHasCheck70bRow pins the summary table's half: one row, in the
// register's own place for it (after 70a and before 70c, because the numbers are read in
// order), saying who runs the check, that no agent does, and pointing at the section.
func TestWebsiteChecklistHasCheck70bRow(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	var row string
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, checklist70bRow) {
			continue
		}
		if row != "" {
			t.Errorf("%s has more than one %q row: one check, one row", path, "70b")
		}
		row = line
	}
	if row == "" {
		// Not a Fatalf: the section is checked either way, so the failure names both
		// halves of what is missing at once.
		t.Errorf("the summary table has no %q row: the check is in the register's index or it is not in the register", "70b")
	} else {
		for _, want := range []string{
			"USER-RUN, NOT RUN",
			"run by you",
			"No agent runs any step",
			`See "Check 70b" below`,
			"P5",   // the part that owes the check
			"D187", // the decision that made it the owner's rather than a gate's
			"RF23", // the finding it answers
			"fold",
			"40 words",
			"250 words",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("the %q row does not carry %q: %s", "70b", want, row)
			}
		}
	}

	// The register's order, in the table: a row out of order is a reader following the
	// numbers into the wrong place.
	at := func(n string) int { return strings.Index(doc, "| "+n+" |") }
	for _, pair := range [][2]string{{"70a", "70b"}, {"70b", "70c"}} {
		if at(pair[0]) == -1 || at(pair[1]) == -1 || at(pair[0]) > at(pair[1]) {
			t.Errorf("the summary table's rows are not in order: %s does not come before %s", pair[0], pair[1])
		}
	}
}

// TestWebsiteChecklistHasCheck70bSection pins the section's half: the banner naming the
// part, the decision and the finding; the Method's reads out of the tree; a step list whose
// every step ends in a FACT line; the pass, fail and paste-back blocks; and the closing
// sentence that says the check changes nothing and no agent runs it. It also refuses the
// two things this check may not ask for, so a later edit cannot quietly turn it into a
// different check.
func TestWebsiteChecklistHasCheck70bSection(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, checklist70bHeading)

	// The register wraps its lines, so the phrases are looked for in the section read as
	// one run of whitespace-collapsed text: a phrase that survives re-wrapping is the
	// phrase, not the line breaks around it.
	flat := strings.Join(strings.Fields(section), " ")

	for _, want := range []string{
		// Who runs it, and that no agent does. The section's own closing sentence is
		// the register's lowercase form ("no agent runs any step of it"), which is
		// what 70a and 70c close on; the row above carries the capitalised one.
		"NOT RUN",
		"run by you",
		"no agent runs any step",
		// The part, the decision and the findings it answers.
		"part P5",
		"D187",
		"D188",
		"RF23",
		"RF5",
		// The Method: what it reads out of the tree rather than remembers.
		"website/dist/index.html",
		"website/data/site.yaml",
		"docs/website/redesign/capture-screenshots.mjs",
		"h1Bottom",
		"leadBottom",
		"firstCommandBottom",
		"390 × 844",
		"1280 × 900",
		// The word ceilings, from the site's own gate.
		"website/test/budget.mjs",
		"40 words",
		"250 words",
		"/docs/",
		"/security/",
		"/changelog/",
		// The provenance phrases, from the gate that refuses them.
		"website/test/meta.mjs",
		// The ordering gate the position rests on.
		"website/test/fold.mjs",
		// The banner that is over the first screen on a first visit.
		"ConsentBanner.astro",
		"position: fixed",
		// The blocks every entry in the register ends with.
		"FACT:",
		"Passes when:",
		"Fails when:",
		"Paste back:",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not carry %q: a live check says who runs it, what to read and look at, when it passes and what failing looks like (plan §4)", checklist70bHeading, want)
		}
	}

	// Every numbered step ends in its own FACT line: the register's steps are the numbers
	// the owner pastes back, and a step that reads something without saying what it read
	// is a step nobody can report on. The steps are the lines that start "N. **" at the
	// section's own level, and each one's text runs to the next such line.
	steps := 0
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		if !isNumberedStep(line) {
			continue
		}
		steps++
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if isNumberedStep(lines[j]) || strings.HasPrefix(lines[j], "**Passes when:") {
				end = j
				break
			}
		}
		if !strings.Contains(strings.Join(lines[i:end], "\n"), "FACT:") {
			t.Errorf("%s's step %q carries no FACT line: the step has to say which number the owner pastes back", checklist70bHeading, strings.TrimSpace(line))
		}
	}
	if steps == 0 {
		t.Errorf("%s carries no numbered step: the check is a list a person runs", checklist70bHeading)
	}

	// The two things this check may not ask for. D187 kept the site's dependency set (the
	// capture tooling's browser is installed out of tree, so the fold costs the tree
	// nothing), and the check is about a page rather than about a project: a step that
	// installed a dependency or ran a `fugaro` command would be a different check, and a
	// different bill.
	for _, refuse := range []string{"npm install", "fugaro run"} {
		if strings.Contains(flat, refuse) {
			t.Errorf("%s carries %q: this check changes nothing -- it installs nothing into the tree and runs no `fugaro` command -- and a step that needs either belongs to a check that says so (D187)", checklist70bHeading, refuse)
		}
	}

	// The section's place in the register, beside the rows' own order.
	at := func(heading string) int { return strings.Index(doc, heading) }
	for _, pair := range [][2]string{{"## Check 70a:", "## Check 70b:"}, {"## Check 70b:", "## Check 70c:"}} {
		if at(pair[0]) == -1 || at(pair[1]) == -1 || at(pair[0]) > at(pair[1]) {
			t.Errorf("the register's sections are not in order: %s does not come before %s", pair[0], pair[1])
		}
	}
}

// TestWebsiteRedesignPlanAttributesTheWordCeilingToP4 pins the fact this file's header
// comment rests on, in the documents' own words: the word ceilings (D188: 40 words in a
// page's lead, 250 per page except the three reference pages) are part P4's work (#572), and
// the plan says so twice -- §3's constraint 4 and P4's own section of §3 -- while the
// reconciliation's RF23 says it where the finding is written. The comment cites those
// sentences, so this holds the citation to what is actually there: a document that
// re-attributes the ceiling fails here rather than leaving a comment quietly wrong about
// whose work the ceiling was.
func TestWebsiteRedesignPlanAttributesTheWordCeilingToP4(t *testing.T) {
	for _, m := range []struct{ path, want string }{
		{"docs/website/redesign/implementation-plan.md", "The word ceiling of P4 goes there too, beside the byte budgets."},
		{"docs/website/redesign/implementation-plan.md", "**The word ceiling, in `website/test/budget.mjs`**"},
		{"docs/website/redesign/reconciliation.md", "part **P4** of the plan writes the word ceiling either way"},
	} {
		flat := strings.Join(strings.Fields(readDoc(t, m.path)), " ")
		if !strings.Contains(flat, m.want) {
			t.Errorf("%s does not carry %q: the ceilings are part P4's (#572, D188), and this is the sentence the check's pin cites for that", m.path, m.want)
		}
	}
}

// checklist70Note opens the register's paragraph about the redesign's check 70, the paragraph
// whose own sentence says what the block of three adds up to.
const checklist70Note = "**Check 70 is the website redesign's"

// TestWebsiteChecklist70NoteDoesNotClaimCheck69IsComplete pins the one sentence in the
// register's note about Check 70 that was wrong. The note closes by saying what its block of
// three adds up to, and the paragraph above it -- the one about Check 69 -- says in as many
// words that 69c "is not in this file yet". A sentence reading "so the block 69a-69c arrived
// in is complete" therefore contradicted the register sixteen lines above it; the true claim
// is about the block *this* paragraph proposes, which is the one all three of whose sections
// are in the file. The test holds both halves: the claim about 70, and the refusal of the
// claim about 69.
func TestWebsiteChecklist70NoteDoesNotClaimCheck69IsComplete(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	i := strings.Index(doc, checklist70Note)
	if i == -1 {
		t.Fatalf("%s has no %q paragraph: the register's note about check 70 is where the redesign's sub-lettering is explained", path, checklist70Note)
	}
	note := doc[i:]
	if j := strings.Index(note, "\n\n"); j != -1 {
		note = note[:j]
	}
	flat := strings.Join(strings.Fields(note), " ")

	// The completeness the paragraph may claim is its own: 70a, 70b and 70c are written below
	// by P3, P5 and P6, and the register's own sections have to hold them for that to be true.
	for _, want := range []string{"**70a**", "**70b**", "**70c**", "**P3**", "**P5**", "**P6**", "Check 69"} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s's note about check 70 does not carry %q: the paragraph says which parts wrote which of the three checks, and what the block adds up to", path, want)
		}
	}
	for _, want := range []string{"## Check 70a:", "## Check 70b:", "## Check 70c:"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s has no %q section: the note says the block is written below, so the register has to hold it", path, want)
		}
	}

	// And it may not claim that the block Check 69 arrived in is complete: 69c is Task W2.3's
	// and is not in this file, which the paragraph above the note says. The sentence is kept
	// here as it was written, so the wording cannot drift back to it.
	if strings.Contains(flat, "69a–69c arrived in is complete") {
		t.Errorf("%s's note about check 70 says the block Check 69 arrived in is complete: it is not -- 69c is not in this file yet, as the paragraph above the note says", path)
	}
}

// TestWebsiteChecklistCheck70bReadsTheScriptsOwnSelectors pins the check's measurements to the
// script's own. The three keys the section reads -- `h1Bottom`, `leadBottom` and
// `firstCommandBottom` -- are defined in docs/website/redesign/capture-screenshots.mjs through
// one selector each, and the section says its two numbers are read "the way the script reads
// them", so it has to read them through those selectors. The trap this guards is a look-alike:
// the script measures the document's first `h1`, and `main h1` is a different element that
// happens to be the same one on today's pages -- a check written against `main h1` would pass
// while measuring an element the script does not.
func TestWebsiteChecklistCheck70bReadsTheScriptsOwnSelectors(t *testing.T) {
	const scriptPath = "docs/website/redesign/capture-screenshots.mjs"
	const path = "docs/gcp-live-checklist.md"
	script := readDoc(t, scriptPath)
	doc := readDoc(t, path)
	flat := strings.Join(strings.Fields(docSection(t, path, doc, checklist70bHeading)), " ")

	for _, m := range []struct{ key, sel string }{
		{"h1Bottom", "'h1'"},                 // the script's `const h1 = q('h1')`
		{"leadBottom", "'main p'"},           // q('main p')
		{"firstCommandBottom", "'main pre'"}, // q('main pre')
	} {
		if !strings.Contains(script, "q("+m.sel+")") {
			t.Errorf("%s no longer measures %s with q(%s): the check's Method reads the script's keys out of it, so the key and the selector move together", scriptPath, m.key, m.sel)
		}
		if !strings.Contains(flat, "document.querySelector("+m.sel+")") {
			t.Errorf("%s's section does not read %s with document.querySelector(%s), the script's own selector: the check reads its numbers \"the way the script reads them\"", path, m.key, m.sel)
		}
	}

	// The look-alike, refused: the script's `h1` is the document's first, not the one inside
	// `main`, and the check may not quietly substitute one for the other.
	if strings.Contains(flat, "document.querySelector('main h1')") {
		t.Errorf("%s's section reads the h1 with document.querySelector('main h1'): the script reads the document's first `h1` (%s), and a check that measures a different element is not the script's measurement", path, scriptPath)
	}
}

// isNumberedStep reports whether a checklist line opens a numbered step of a Method
// ("1. **At 390 × 844, …"), which is how every live check in the register writes its
// steps.
func isNumberedStep(line string) bool {
	if len(line) < 5 || line[1] != '.' || line[2] != ' ' {
		return false
	}
	return line[0] >= '1' && line[0] <= '9'
}
