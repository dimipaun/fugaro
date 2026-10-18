package cli

// The website redesign's live check 70b, the gist above the fold (website redesign plan
// docs/website/redesign/implementation-plan.md §3 and §8, part P5; decision D187;
// reconciliation findings RF23 and RF5).
//
// 70b is the third of the redesign's three owner-run live checks and the one part P5 owed
// the register: P5 merged in #603 and shipped the ordering gate (website/test/fold.mjs)
// and the word ceilings (website/test/budget.mjs) but not the check that measures the fold
// itself, which the register's note under "Check 70 is the website redesign's" recorded as
// not in the file yet. These two tests pin the two halves of the entry the way
// TestWebsiteChecklistHasCheck69aAnd69b pins the website plan's 69a and 69b -- the row a
// reader meets first and the section it points at -- because a check that exists as a
// section but not as a row is a check nobody reads first, and a row with no section is a
// command nobody can follow.
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

// isNumberedStep reports whether a checklist line opens a numbered step of a Method
// ("1. **At 390 × 844, …"), which is how every live check in the register writes its
// steps.
func isNumberedStep(line string) bool {
	if len(line) < 5 || line[1] != '.' || line[2] != ' ' {
		return false
	}
	return line[0] >= '1' && line[0] <= '9'
}
