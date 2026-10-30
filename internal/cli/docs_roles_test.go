package cli

// The roles design (subscriptions S5.1, forge E0; D446, T16, D627-D629):
// viewer, operator and admin laid over the lists an installation already has,
// with every product decision left for the owner.
//
// What this file is for is the property the review brief names: the document
// *asks*, it does not decide. So the guard that matters is that the decisions
// table is all `open` -- a row the design had quietly settled is exactly the
// failure mode the owner's ruling (D446: a separate design first) exists to
// avoid -- together with the sections that carry those questions and the
// rulings the design is bound by, which it has to name to be checkable at all.
//
// The two absences are the review's own no-go list: no figure (a price, a
// per-seat count) and no dollar amount may appear, because the figures are
// private (D430) and the seat count is D433's pricing rule, not a design input.

import (
	"regexp"
	"strings"
	"testing"
)

const rolesDesignPath = "docs/design/roles.md"

// rolesDecisionRow is one row of the "Decisions for the owner" table: a row
// whose first cell is a decision number. The design holds a block of numbers
// of its own (D680-D719), so the shape is `| D` followed by digits.
var rolesDecisionRow = regexp.MustCompile(`^\|\s*D[0-9]+\s*\|`)

// rolesSeatFigure is a seat count written as a number -- "5 seats", "seat: 5",
// "2 per seat". D433 makes a seat a pricing rule and the estimate D439's, so a
// figure in this document is either a leak of the private figures (D430) or a
// product decision taken without the owner.
var rolesSeatFigure = regexp.MustCompile(`(?i)(\b[0-9]+\s+(per\s+)?seats?\b)|(\bseats?\s*[:=]\s*[0-9]+)`)

// rolesDollarFigure is a currency amount.
var rolesDollarFigure = regexp.MustCompile(`\$[0-9]`)

// TestDocsRolesDesign: `docs/design/roles.md` is the S5.1/E0 design. It has to
// ask rather than answer, so every decision row's status is `open`; it has to
// name the rulings it is bound by, so a reader can check each claim in it
// against its source; and it has to stay out of the commercial figures, which
// are not its subject (D430, D433) and are the subscriptions plan's.
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
		t.Fatalf("%s's decisions table has %d rows: the design's open questions (the vocabulary, the viewer's cloud grant, the minter, the device role, enforcement in the console and the CLI) are what the owner answers, and a table that short has settled them by omission", rolesDesignPath, len(rows))
	}
	for i, row := range rows {
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if status := strings.TrimSpace(cells[len(cells)-1]); status != "open" {
			t.Errorf("%s decisions table row %d says %q, want %q:\n\n%s\n\nEvery product choice in this design is the owner's (D446), so a row the design has answered is the one thing this document may not contain. If the owner has since ruled on it, the answer goes with its ruling in the Settled list and the row leaves the open table.", rolesDesignPath, i+1, status, "open", strings.TrimSpace(row))
		}
	}

	if m := rolesDollarFigure.FindString(doc); m != "" {
		t.Errorf("%s contains a currency amount (%q): the figures are private (D430) and pricing is the subscriptions plan's, not this design's", rolesDesignPath, m)
	}
	if m := rolesSeatFigure.FindString(flat); m != "" {
		t.Errorf("%s contains a seat figure (%q): a seat is D433's pricing rule and the estimate is D439's, so no count belongs in this design (D430)", rolesDesignPath, m)
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
