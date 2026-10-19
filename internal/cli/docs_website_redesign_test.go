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
// (#572, D188), and the plan says so twice -- §2's constraint 4, "The word ceiling of P4
// goes there too, beside the byte budgets" (§2 is the plan's own constraints; §3 is "The
// parts" and has no numbered constraints), and P4's own section of §3, "**The word ceiling,
// in `website/test/budget.mjs`**" -- while the reconciliation's RF23 puts it the other way
// round ("part **P4** of the plan writes the word ceiling either way"). The check's section
// attributes the gates to no part at all.
//
// The two tests on the entry pin its two halves the way TestWebsiteChecklistHasCheck69aAnd69b
// pins the website plan's 69a and 69b -- the row a reader meets first and the section it
// points at -- because a check that exists as a section but not as a row is a check nobody
// reads first, and a row with no section is a command nobody can follow. The tests beside
// them hold the facts this comment and the section's own wording rest on: the plan's
// attribution of the ceilings to P4, the register's notes about its block of three and about
// the commit each "the number was free" reading was taken at, the "as of main's commit" note
// the three sections open with, and the selectors the check reads its numbers through.
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
	"os"
	"regexp"
	"strings"
	"testing"
)

// checklist70bRow is the row's own prefix, and checklist70bHeading the section's: the two
// halves this file pins, named once so the pair cannot drift apart.
const (
	checklist70bRow     = "| 70b |"
	checklist70bHeading = "## Check 70b:"
	// The redesign's other two checks, whose "as of" note is the same reading of the same
	// page and is pinned with 70b's by TestWebsiteChecklist70NotesNameTheMergedMainCommit.
	checklist70aHeading = "## Check 70a:"
	checklist70cHeading = "## Check 70c:"
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
		// The exemption list, from the file the ceilings' gate imports it from.
		"website/test/lib/exempt.mjs",
		"DOCUMENT_PAGES",
		// The provenance phrases, from the gate that refuses them.
		"website/test/meta.mjs",
		// The ordering gate the position rests on.
		"website/test/fold.mjs",
		// The homepage's own blocks, in the order the page renders them, and the command the
		// page's first `pre` belongs to.
		"website/src/pages/index.astro",
		"AgentChat.astro",
		"ArchitectureDiagram.astro",
		"HomeInstall.astro",
		"HOME_PATHS",
		"home-install-command",
		"chat-watch",
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

// TestWebsiteRedesignPlanAttributesTheWordCeilingToP4 pins the fact the header comment above
// rests on, in the documents' own words *and in the places the comment cites*: the word
// ceilings (D188: 40 words in a page's lead, 250 per page except the three reference pages)
// are part P4's work (#572). The plan says so in §2's constraint 4 -- §2 is "What is new in
// this plan's constraints, on top of the first plan's §3", and its fourth constraint is "One
// source of truth per fact", which puts the ceiling beside the byte budgets -- and again in
// §3, "The parts", in P4's own section, where the gate that enforces it is named. The
// reconciliation's RF23 says it too, where the finding is written.
//
// The sentence is read out of the section and the constraint rather than out of the whole
// document, because a citation names a place: a sentence that moves to another section or
// another constraint is a citation gone stale, and a test that only looks for the text
// anywhere in the file cannot see it. The citation in the comment is checked too -- the
// sentence was cited to §3, which is "The parts" and has no numbered constraints at all, for
// a round of review -- because a comment is source no compiler reads.
func TestWebsiteRedesignPlanAttributesTheWordCeilingToP4(t *testing.T) {
	const planPath = "docs/website/redesign/implementation-plan.md"
	const ceilingSentence = "The word ceiling of P4 goes there too, beside the byte budgets."
	plan := readDoc(t, planPath)

	// §2's constraint 4, and not another of its constraints: the section is cut at the next
	// "## " heading and the constraint at the next numbered item, the way the document
	// numbers them.
	sec2 := docSection(t, planPath, plan, "## 2. What is new in this plan's constraints")
	c4 := strings.Index(sec2, "\n4. **")
	c5 := -1
	if c4 != -1 {
		c5 = strings.Index(sec2[c4+1:], "\n5. **")
	}
	if c4 == -1 || c5 == -1 {
		t.Fatalf("%s's §2 has no constraint 4 followed by a constraint 5: the ceiling's sentence is cited to that constraint", planPath)
	}
	if item := sec2[c4+1 : c4+1+c5]; !strings.Contains(item, ceilingSentence) {
		t.Errorf("%s's §2 constraint 4 does not carry %q: the ceilings are part P4's (#572, D188) and that constraint is the sentence's place", planPath, ceilingSentence)
	}

	// §3, "The parts", and P4's own section of it, where the gate the ceiling lives in is
	// named.
	sec3 := docSection(t, planPath, plan, "## 3. The parts")
	p4 := strings.Index(sec3, "### P4 —")
	if p4 == -1 {
		t.Fatalf("%s's §3 has no P4 section: the ceiling is P4's, and §3's section for the part is where its gate is named", planPath)
	}
	partP4 := sec3[p4:]
	if next := strings.Index(partP4, "\n### "); next != -1 {
		partP4 = partP4[:next]
	}
	if want := "**The word ceiling, in `website/test/budget.mjs`**"; !strings.Contains(partP4, want) {
		t.Errorf("%s's P4 section does not carry %q: the ceiling is enforced in that gate, which is what the comment cites §3 for", planPath, want)
	}

	// The reconciliation, where the finding is written and where the ceiling is P4's either
	// way.
	const recPath = "docs/website/redesign/reconciliation.md"
	if want := "part **P4** of the plan writes the word ceiling either way"; !strings.Contains(strings.Join(strings.Fields(readDoc(t, recPath)), " "), want) {
		t.Errorf("%s does not carry %q: RF23 is the finding this check answers, and it is where the ceiling is attributed to P4 in as many words", recPath, want)
	}

	// And the comment itself, which nothing else reads: it cites the sentence to §2's
	// constraint 4, and this file is the only place that claim is written down. The header is
	// this file's leading comment block -- it opens with the line below and runs to the blank
	// line before `import` -- and the phrase is kept on one line there, so the citation is one
	// string rather than a wrap.
	src, err := os.ReadFile("docs_website_redesign_test.go")
	if err != nil {
		t.Fatal(err)
	}
	header := string(src)
	i := strings.Index(header, "// The website redesign's live check 70b")
	if i == -1 {
		t.Fatalf("this file has no header comment opening %q: the citation this test guards is written there", "// The website redesign's live check 70b")
	}
	header = header[i:]
	if j := strings.Index(header, "\n\n"); j != -1 {
		header = header[:j]
	}
	if !strings.Contains(header, "§2's constraint 4") {
		t.Errorf("this file's header no longer cites the ceiling's sentence to §2's constraint 4: §3 is \"The parts\" and has no numbered constraints, so a header that says §3 cites a place the sentence is not")
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

// highestFreeClaim is the register's "the number was free when ... merged" claim, as the two
// website notes write it: the part's merge and its pull request, the commit the reading was
// taken at, and the highest `## Check N` section the file carried there.
var highestFreeClaim = regexp.MustCompile("was free when (.+?) merged \\(`([0-9a-f]{7,40})`, #([0-9]+)\\): the highest written `## Check N` section in the file that commit merged into, read at `([0-9a-f]{7,40})\\^`, was \\*\\*([0-9a-z]+)\\*\\*")

// TestWebsiteChecklistHighestWrittenClaimsReadAtTheCommitBeforeTheMerge pins the two register
// notes that say a check number "was free when" a part merged -- the Check 69 note's sentence
// for 69, and the Check 70 note's for 70. Each states the highest `## Check N` section the file
// carried at that moment, and such a statement is true of the file *before* the merging commit:
// the merge is what writes the part's own checks. A note that reads the state at the merge
// commit itself gets the count and the reason wrong, which is how this test came to exist --
// the Check 70 note read "the highest written `## Check N` section here was **69b**" as the
// state "when P3 and P6 merged (and when P5 did)", and 70c was in the file at both of those
// commits, written by the first of them.
//
// The shape is what makes the claim checkable rather than remembered: the merge and its pull
// request are named, the reading says which commit it was taken at and that commit is the
// merge's parent, the section the file ended at is named, and the file really carries it. Git
// history is not readable from this test -- CI checks out with `fetch-depth: 2` -- so the pin is
// on the text saying where its fact comes from; the derivation it stands for is
// `git show <merge>^:docs/gcp-live-checklist.md | grep '^## Check'`.
func TestWebsiteChecklistHighestWrittenClaimsReadAtTheCommitBeforeTheMerge(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	flat := strings.Join(strings.Fields(doc), " ")

	found := highestFreeClaim.FindAllStringSubmatch(flat, -1)
	if len(found) < 2 {
		t.Fatalf("%s carries %d dated \"was free when ... merged\" claims, want the two the register's website notes make (Check 69's for 69, Check 70's for 70): a claim that says which section was the highest written one and does not say which commit it read is a claim about today, and today moves", path, len(found))
	}

	// The two readings, in the order the register writes them.
	want := []struct {
		merge, pr, number string
	}{
		{"4b6a45d", "535", "58"},  // W2.2, whose own commit writes 69a and 69b
		{"b9c349a", "575", "69b"}, // P3 and P6, whose own commit writes 70a and 70c
	}
	for i, m := range found {
		part, merge, pr, readAt, number := m[1], m[2], m[3], m[4], m[5]
		if i < len(want) {
			w := want[i]
			if merge != w.merge || pr != w.pr || number != w.number {
				t.Errorf("%s reads the claim about %s as %s's merge (`%s`, #%s) ending at `## Check %s`, want `%s` (#%s) ending at `## Check %s`: the number, the commit and the reading are one fact, so a correction edits all three", path, part, part, merge, pr, number, w.merge, w.pr, w.number)
				continue
			}
		}
		// The reading is the state the merge found, so it is taken at the merge's parent: at
		// the merge itself the part's own checks are already written into the file.
		if readAt != merge {
			t.Errorf("%s reads the highest written section for %s at `%s^`, not at the merge it names, `%s`: the merge commit is what writes the part's own checks, so a reading taken there counts them as written and the number stops looking free", path, part, readAt, merge)
		}
		if !strings.Contains(doc, "## Check "+number+":") {
			t.Errorf("%s says the file ended at `## Check %s` at `%s^`, and carries no `## Check %s:` section: the claim names a section a reader cannot find", path, number, readAt, number)
		}
	}

	// The half the Check 70 note got wrong, pinned where it now stands: what the file ends at
	// after each merge. #575's own commit writes 70a and 70c, so the file ends at 70c there,
	// and it still ends at 70c at #603's -- which is why 70b, P5's own check, is the section
	// written last, by the check-70b work, rather than by P5's merge.
	for _, claim := range []string{
		"so the file ends at `70c` there",
		"it still ends at `70c` at P5's merge (`24ef3aef`, #603)",
	} {
		if !strings.Contains(flat, claim) {
			t.Errorf("%s no longer carries %q: the Check 70 note has to say what the file ends at after the merges that took the number, because 70 was free for P5's check only while 70c was the highest written section", path, claim)
		}
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

// TestWebsiteChecklistCheck70bNamesWhatTheTreeHas pins the identifiers the section's steps
// name to the files that carry them. The section is prose about markup, so it can name a
// class, a constant or a path that no longer exists and still read perfectly well -- which is
// what happened to it: it read the homepage's first command out of `Hero.astro`'s
// `HERO_PATHS` and `hero-install-command` for a round after #623 moved the install paths to
// `HomeInstall.astro`'s `HOME_PATHS` and `.home-install-command` and left the hero with no
// `<pre>` at all. Every name below is checked in the file that carries it, and the two the
// page no longer has are refused, so a step cannot point an owner at markup that is gone.
func TestWebsiteChecklistCheck70bNamesWhatTheTreeHas(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	flat := strings.Join(strings.Fields(docSection(t, path, doc, checklist70bHeading)), " ")

	for _, m := range []struct{ name, file string }{
		{"HOME_PATHS", "website/src/components/HomeInstall.astro"},
		{"home-install-command", "website/src/components/HomeInstall.astro"},
		{"chat-watch", "website/src/components/AgentChat.astro"},
		{"chat-watch-command", "website/src/components/AgentChat.astro"},
		{"HomeInstall", "website/src/pages/index.astro"},
		{"ArchitectureDiagram", "website/src/pages/index.astro"},
		{"DOCUMENT_PAGES", "website/test/lib/exempt.mjs"},
		{"flex-direction: row-reverse", "website/src/styles/base.css"},
	} {
		if !strings.Contains(flat, m.name) {
			t.Errorf("%s's section does not name %q: the step that reads the page has to say what it reads it out of", path, m.name)
		}
		if !strings.Contains(readDoc(t, m.file), m.name) {
			t.Errorf("%s names %q, and %s no longer carries it: the section reads the page out of the tree, so a name the tree does not have is a step an owner cannot run", path, m.name, m.file)
		}
	}

	// The hero's old names, refused: #623 moved the homepage's install paths out of the hero
	// and into their own section, so a step that reads them there reads a page that is gone.
	for _, stale := range []string{"HERO_PATHS", "hero-install-command"} {
		if strings.Contains(flat, stale) {
			t.Errorf("%s's section names %q, which no file in the tree carries: the homepage's install paths are HomeInstall.astro's HOME_PATHS and .home-install-command since #623", path, stale)
		}
	}
}

// methodLines returns the shell lines of the Method's own code block in a check's section.
func methodLines(t *testing.T, heading, section string) []string {
	t.Helper()
	const open = "```bash\n"
	i := strings.Index(section, open)
	if i == -1 {
		t.Fatalf("%s's Method has no ```bash block: the check reads its inputs out of the tree", heading)
	}
	rest := section[i+len(open):]
	end := strings.Index(rest, "\n```")
	if end == -1 {
		t.Fatalf("%s's Method's ```bash block is never closed", heading)
	}
	return strings.Split(rest[:end], "\n")
}

// methodRead reads one Method line the way the shell would: a `grep` or a `sed` whose last
// argument is a file the repository carries and whose quoted argument is the text it looks
// for in that file. It reports ok=false for the lines that are not a read of a file in the
// tree -- the guard, the server, a pipe, or a read of the built artifact under
// `website/dist/`, which the check's own guard is what builds.
func methodRead(line string) (file, pattern string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "grep ") && !strings.HasPrefix(line, "sed ") {
		return "", "", false
	}
	fields := strings.Fields(line)
	file = fields[len(fields)-1]
	if !reMethodFile.MatchString(file) || strings.HasPrefix(file, "website/dist/") {
		return "", "", false
	}
	m := reMethodQuoted.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return file, m[1] + m[2], true
}

var (
	// reMethodFile is a path a Method line can read: one of the tree's own file kinds.
	reMethodFile = regexp.MustCompile(`^[A-Za-z0-9_./-]+\.(mjs|astro|css|js|yaml|json|html|md)$`)
	// reMethodQuoted is the first quoted run of a Method line, either quote style.
	reMethodQuoted = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)
	// reMethodLiteral is a word inside a pattern: the part of a `grep` or `sed` expression
	// that is text to look for rather than regular-expression syntax around it.
	reMethodLiteral = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.-]{2,}`)
)

// TestWebsiteChecklistCheck70bMethodReadsExist reads the section's Method the way a shell
// would and holds every read in it to the file it names: the file is in the tree, and the
// text the line looks for is in that file. This is the check the review asked for after
// `PAGE_EXEMPT` -- an identifier `budget.mjs` has never had, the exemption set being
// `DOCUMENT_PAGES` in `website/test/lib/exempt.mjs` -- was written into a Method that no test
// read, so an owner running the block got an empty grep and a check that silently lost the
// exemption list it was supposed to read.
func TestWebsiteChecklistCheck70bMethodReadsExist(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	lines := methodLines(t, checklist70bHeading, docSection(t, path, doc, checklist70bHeading))

	var read []string
	for _, line := range lines {
		file, pattern, ok := methodRead(line)
		if !ok {
			continue
		}
		read = append(read, file)
		body := readDoc(t, file)
		for _, needle := range reMethodLiteral.FindAllString(pattern, -1) {
			if !strings.Contains(body, needle) {
				t.Errorf("%s's Method reads %q out of %s, which does not carry it: the line would print nothing, and the step below it would read a fact the owner was never shown", checklist70bHeading, needle, file)
			}
		}
	}

	// The files the Method's own claims rest on, each read by one of its lines: the site's
	// facts, the measurement script, the README the ceilings live in, the exemption list and
	// the two gates' own lists, and the page and components the steps look at.
	got := strings.Join(read, "\n")
	for _, want := range []string{
		"website/data/site.yaml",
		"docs/website/redesign/capture-screenshots.mjs",
		"website/README.md",
		"website/test/lib/exempt.mjs",
		"website/test/meta.mjs",
		"website/src/pages/index.astro",
		"website/src/components/HomeInstall.astro",
		"website/src/components/AgentChat.astro",
		"website/src/components/ConsentBanner.astro",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s's Method never reads %s: the step that rests on it is reading the section's memory of the file instead of the file", checklist70bHeading, want)
		}
	}
}

// TestWebsiteChecklist70NotesNameTheMergedMainCommit pins the "as of" note the website
// redesign's three checks carry. The note is what tells an owner which page a section was
// read against, and all three had it wrong: each named `73a0c109`, a main commit from
// before #623 — at which `website/src/components/HomeInstall.astro` does not exist and the
// home page is the hero and the diagram alone — so the page the sections describe (the
// install blocks `HomeInstall.astro`'s, the chat's `fugaro watch` block as the first `pre`
// in `main`) is not the page that commit builds. What they must name is the commit this
// branch merged: the merge `c27064c0`'s second parent, `git rev-parse c27064c0^2`, which
// is `3a233d57`.
//
// What is pinned is the shape rather than the history, because the history is not there to
// read: these tests run against whatever clone they are given (CI checks out with
// `fetch-depth: 2`), so no assertion here can open the commit a sha names. The three
// sections must each carry the note, must name one and the same commit, and must say which
// commit that is ("the commit this branch merged") rather than leaving a sha to stand
// alone — which is the reading a future edit has to repeat from `git rev-parse`. A wrong
// commit named consistently in all three is the one thing this pin cannot refuse, and it
// is why the paragraph above states how the right one is found.
func TestWebsiteChecklist70NotesNameTheMergedMainCommit(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	// `flat` strips the note's backticks along with the emphasis, so the sha is read bare.
	note := regexp.MustCompile("As of main's commit ([0-9a-f]{7,40})")
	type asOf struct{ heading, sha string }
	var notes []asOf
	for _, h := range []string{checklist70aHeading, checklist70bHeading, checklist70cHeading} {
		section := flat(docSection(t, path, doc, h))
		m := note.FindStringSubmatch(section)
		if m == nil {
			t.Errorf("%s carries no \"As of main's commit `…`\" note: the section describes a page, and the note is what says which commit's page it is", h)
			continue
		}
		if !strings.Contains(section, "the commit this branch merged") {
			t.Errorf("%s names %s as the commit it was read against and does not say it is the commit this branch merged: a sha on its own does not say whose page the section describes", h, m[1])
		}
		notes = append(notes, asOf{h, m[1]})
	}
	for i := 1; i < len(notes); i++ {
		if notes[i].sha != notes[0].sha {
			t.Errorf("%s names commit %s and %s names %s: the three notes are one reading of one page, so they name one commit", notes[0].heading, notes[0].sha, notes[i].heading, notes[i].sha)
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
