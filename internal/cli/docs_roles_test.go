package cli

// The roles design (subscriptions S5.1, forge E0; D446, T16, D627-D629):
// viewer, operator and admin laid over the lists an installation already has,
// with every product decision left for the owner.
//
// What this file is for is the property the review brief names: the document
// *asks*, and answers only where the owner has ruled. So the guard that matters
// is that every decision row carries an answer the owner gave -- a row the
// design quietly settled itself is exactly the failure mode D446 (a separate
// design first) exists to avoid, and a row that lost the answer the owner gave
// is a task nobody may start -- together with the sections that carry those
// questions, the rulings the design is bound by, and the two rows the roles-
// scopes work's D1580 answer supersedes (F26).
//
// The two absences are the review's own no-go list: no figure (a price, a
// per-seat count) and no dollar amount may appear, because the figures are
// private (D430) and the seat count is D433's pricing rule, not a design input.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const rolesDesignPath = "docs/design/roles.md"

// rolesDecisionRow is one row of the "Decisions for the owner" table: a row
// whose first cell is a decision number. The design holds a block of numbers
// of its own (D680-D719), so the shape is `| D` followed by digits.
var rolesDecisionRow = regexp.MustCompile(`^\|\s*D[0-9]+\s*\|`)

// rolesDecisionNumber reads a decision row's number off its first cell, which
// is what the two superseded rows below are picked by.
var rolesDecisionNumber = regexp.MustCompile(`^\|\s*(D[0-9]+)\s*\|`)

// rolesAnsweredToday is the status every row the owner answered carries: the
// date of the ruling and the owner's "all as recommended" of 2026-10-30.
const rolesAnsweredToday = "answered 2026-10-30, as recommended"

// rolesSupersededRows are the two answers the roles-scopes work's D1580
// reverses (F26): D690(a) said the CLI does not rank its own commands, D691(a)
// that the console does not know a role, and the owner's 2026-11-07 answer of
// D1580 replaces both. They are named here rather than counted, because which
// two were reversed is the claim: a third row quietly marked superseded, or one
// of these two losing its mark, is a reader sent to the wrong answer.
var rolesSupersededRows = []string{"D690", "D691"}

// rolesSeatFigure is a seat count written as a number -- "5 seats", "seat: 5",
// "2 per seat". D433 makes a seat a pricing rule and the estimate D439's, so a
// figure in this document is either a leak of the private figures (D430) or a
// product decision taken without the owner.
var rolesSeatFigure = regexp.MustCompile(`(?i)(\b[0-9]+\s+(per\s+)?seats?\b)|(\bseats?\s*[:=]\s*[0-9]+)`)

// rolesDollarFigure is a currency amount.
var rolesDollarFigure = regexp.MustCompile(`\$[0-9]`)

// TestDocsRolesDesign: `docs/design/roles.md` is the S5.1/E0 design. It has to
// record the owner's answer to each decision row and nothing it decided itself;
// it has to mark the two rows the roles-scopes work's D1580 answer supersedes,
// with D446's narrowing beside them (F26); it has to name the rulings it is
// bound by, so a reader can check each claim in it against its source; and it
// has to stay out of the commercial figures, which are not its subject (D430,
// D433) and are the subscriptions plan's.
func TestDocsRolesDesign(t *testing.T) {
	doc := readDoc(t, rolesDesignPath)
	flat := flattenDoc(doc)

	// The rulings the design is bound by: the roles design itself (D446), its
	// licence placement (T16), the interim over the lists (D627), the sign-in
	// shape (D628) and suspension (D629).
	for _, want := range []string{"D446", "T16", "D627", "D628", "D629"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not cite %s: the rulings the design is bound by are what a reader checks its claims against, and a claim whose ruling is unnamed cannot be checked at all", rolesDesignPath, want)
		}
	}

	// The two sections by name, at whatever heading level and numbering the
	// document gives them: neither is what the guard is about. rolesSection
	// fails the test itself when the heading is missing.
	for _, want := range []string{"Decisions for the owner", "Out of scope"} {
		if body := rolesSection(t, doc, want); strings.TrimSpace(body) == "" {
			t.Errorf("%s's %q section is empty: the heading is there and the section is not, which is what a guard anchored to the whole document would miss", rolesDesignPath, want)
		}
	}

	rows := rolesDecisionRows(rolesSection(t, doc, "Decisions for the owner"))
	if len(rows) < 10 {
		t.Fatalf("%s's decisions table has %d rows: the design's questions (the vocabulary, the viewer's cloud grant, the minter, the device role, enforcement in the console and the CLI) are what the owner answered on 2026-10-30, and a table that short has lost them", rolesDesignPath, len(rows))
	}
	superseded := map[string]bool{}
	for _, id := range rolesSupersededRows {
		superseded[id] = true
	}
	seenSuperseded := map[string]bool{}
	for i, row := range rows {
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if len(cells) != 6 {
			t.Errorf("%s decisions table row %d has %d cells, want 6 (`# | Question | Options | Recommendation | Cost of each | Status`): a row of another shape puts a different cell where the answer is", rolesDesignPath, i+1, len(cells))
			continue
		}
		status := strings.TrimSpace(cells[5])
		id := rolesDecisionNumber.FindStringSubmatch(row)
		if id == nil {
			t.Errorf("%s decisions table row %d has no decision number: %q", rolesDesignPath, i+1, strings.TrimSpace(row))
			continue
		}
		if superseded[id[1]] {
			seenSuperseded[id[1]] = true
			if !strings.Contains(status, "superseded by D1580 (2026-11-07)") {
				t.Errorf("%s's row for %s says %q: the owner's 2026-11-07 answer of D1580 reverses it (F26, docs/roles-scopes/reconciliation.md), and a row that keeps its old answer without the supersession sends a reader to a rule the build does not follow", rolesDesignPath, id[1], status)
			}
			continue
		}
		if status != rolesAnsweredToday {
			t.Errorf("%s's row for %s says %q, want %q:\n\n%s\n\nEvery product choice in this design is the owner's (D446): the owner answered them all on 2026-10-30, and a row whose status is neither that answer nor one of the two D1580 supersedes is a decision whose state this document does not record.", rolesDesignPath, id[1], status, rolesAnsweredToday, strings.TrimSpace(row))
		}
	}
	for _, id := range rolesSupersededRows {
		if !seenSuperseded[id] {
			t.Errorf("%s carries no row for %s marked superseded by D1580: two answers of 2026-10-30 are reversed by the owner's 2026-11-07 answer of D1580, and this file names them", rolesDesignPath, id)
		}
	}
	if !strings.Contains(flat, "and D446 is narrowed with them") {
		t.Errorf("%s does not state that D446 is narrowed beside the two supersessions: D1580 puts the role check in the CLI and the console where D446 said the cloud enforced the roles, and the narrowing has to be readable where the supersession is recorded", rolesDesignPath)
	}

	if m := rolesDollarFigure.FindString(doc); m != "" {
		t.Errorf("%s contains a currency amount (%q): the figures are private (D430) and pricing is the subscriptions plan's, not this design's", rolesDesignPath, m)
	}
	if m := rolesSeatFigure.FindString(flat); m != "" {
		t.Errorf("%s contains a seat figure (%q): a seat is D433's pricing rule and the estimate is D439's, so no count belongs in this design (D430)", rolesDesignPath, m)
	}
}

// TestDocsRolesDesignCitations: the role-model section's four line citations
// into internal/access/access.go and the two into internal/cli/pair.go are
// resolved, not pinned to a literal.
//
// It is the drift a literal cannot see that this guard is for: RS2 grew that
// file's package doc by six lines, all four citations named a line that then
// said something else, and no test covered them. The access.go citations here
// are the closed set's `const (` block and the three declarations the sentence
// names beside it, so each one is read out of the source and the document has to
// name it. mobileauth.go:80 is that file's own and is not this section's.
//
// The pair.go citations were the same drift the other way round: RS8 moved
// pairInheritedRole off the two IAM lists and onto the grants node, the line the
// two sentences named became pairQR.Project, and the sentences went on
// describing the rule RS8 had replaced. Both are read out of pair.go and both
// are COUNTED, because one re-pointed citation and one stale one would satisfy a
// check that only asks whether the document contains the resolved line.
func TestDocsRolesDesignCitations(t *testing.T) {
	doc := readDoc(t, rolesDesignPath)
	src := strings.Split(readDoc(t, "internal/access/access.go"), "\n")

	lineOf := func(needle string) int {
		for i, l := range src {
			if strings.Contains(l, needle) {
				return i + 1
			}
		}
		t.Fatalf("internal/access/access.go no longer carries %q, so %s's citation has nothing to resolve against", needle, rolesDesignPath)
		return 0
	}

	// The closed set is the `const (` block the sentence's three names are in,
	// which is the line the citation names: walk back from RoleViewer's own
	// declaration to the block that opens it.
	closedSet := lineOf(`RoleViewer Role = "viewer"`)
	for closedSet > 0 && strings.TrimSpace(src[closedSet-1]) != "const (" {
		closedSet--
	}
	if closedSet == 0 {
		t.Fatalf("internal/access/access.go declares RoleViewer outside a `const (` block, so the citation %s makes for the closed set has no line to name", rolesDesignPath)
	}

	for _, c := range []struct {
		cite string
		what string
	}{
		{"`internal/access/access.go:" + strconv.Itoa(closedSet) + "`", "the closed set"},
		{"(`:" + strconv.Itoa(lineOf("func ParseRole")) + "`)", "ParseRole"},
		{"(`:" + strconv.Itoa(lineOf("func (r Role) Rank()")) + "`)", "Rank"},
		{"(`:" + strconv.Itoa(lineOf("func (r Role) AtLeast(")) + "`)", "AtLeast"},
	} {
		if !strings.Contains(doc, c.cite) {
			t.Errorf("%s does not cite %s at %s, where internal/access/access.go declares it: the sentence points at whatever now sits on the lines it names, which is how these four drifted from 60/82/99/112 when that file grew", rolesDesignPath, c.what, c.cite)
		}
	}

	pairSrc := strings.Split(readDoc(t, "internal/cli/pair.go"), "\n")
	pairLine := 0
	for i, l := range pairSrc {
		if strings.Contains(l, "func pairInheritedRole(") {
			pairLine = i + 1
			break
		}
	}
	if pairLine == 0 {
		t.Fatalf("internal/cli/pair.go no longer declares pairInheritedRole, so %s's two citations of it have nothing to resolve against: the role a device inherits is the sentence they are there for", rolesDesignPath)
	}
	pairCite := "`internal/cli/pair.go:" + strconv.Itoa(pairLine) + "`"
	if n := strings.Count(doc, pairCite); n != 2 {
		t.Errorf("%s cites %s %d times, want 2 (the session's role and the vocabulary paragraph): a citation left at the old line names whatever pairInheritedRole left behind", rolesDesignPath, pairCite, n)
	}
}

// rolesSection returns the body of the section some heading names `name`. The
// heading's own line is found by name rather than spelled out, so the guard
// does not care what level or numbering the document gives the section, and
// the cut is docSection's (docs_providers_test.go), which is the one helper in
// this package for reading a section out of a document.
func rolesSection(t *testing.T, doc, name string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if headingLevel(line) > 0 && strings.Contains(line, name) {
			return docSection(t, rolesDesignPath, doc, line)
		}
	}
	t.Fatalf("%s has no heading naming the %q section", rolesDesignPath, name)
	return ""
}

// rolesDecisionRows picks the decisions table's rows out of a section: a row
// whose first cell is a decision number.
func rolesDecisionRows(section string) []string {
	var rows []string
	for _, line := range strings.Split(section, "\n") {
		if rolesDecisionRow.MatchString(line) {
			rows = append(rows, line)
		}
	}
	return rows
}
