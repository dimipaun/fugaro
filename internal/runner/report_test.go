package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// toleratedRec is a record whose mode let one finding through: the shape
// T4.2's report reads back.
func toleratedRec(findings []runstore.ToleratedFinding) *runstore.Record {
	return &runstore.Record{
		RunID:   "20261027-120000-abcd",
		Outcome: runstore.OutcomeReady,
		Rigor: &runstore.RigorRecord{
			Mode: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize, Revision: "origin/main",
		},
		Reviews:   []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierSenior, Verdict: "changes", Findings: len(findings)}},
		Tolerated: runstore.NewTolerated(findings),
	}
}

// TestReportListsWhatTheModeTolerated: the report carries the list
// `fugaro diagnose` reads back — the mode the run resolved and one row per
// tolerated finding, with the score the mode's table read.
func TestReportListsWhatTheModeTolerated(t *testing.T) {
	rec := toleratedRec([]runstore.ToleratedFinding{{
		Title: "unbounded retry loop in the fetch", Mode: rigor.Pro,
		Likelihood: 2, Impact: 2, FixCost: 1, Class: rigor.Correctness,
	}})
	got := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)
	for _, want := range []string{
		"**Rigor:** pro, from repo, at finalize, read from origin/main",
		// The words are the block's own, which `fugaro diagnose` prints too:
		// the report contributes the label, never a second wording.
		"**Rigor:** " + rec.Rigor.Line(),
		"#### Tolerated",
		"| Label | Likelihood | Impact | Fix cost | Class | Mode | Finding |",
		"| low | 2 | 2 | 1 | correctness | pro | `unbounded retry loop in the fetch` |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

// TestReportToleratedTotalIsSaidWhenTheTableIsPartial: a table that is not the
// whole list says how many findings the mode really tolerated, rather than
// passing its last row off as the last one. It never says its rows are "the
// first" of the total: the gap is the record's cap only when the list was long
// enough for it, and NewTolerated also drops an entry it cannot read, so the
// rows are some of the total and not the opening of it.
func TestReportToleratedTotalIsSaidWhenTheTableIsPartial(t *testing.T) {
	row := runstore.ToleratedFinding{
		Title: "one finding", Mode: rigor.Pro,
		Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Correctness,
	}
	for _, tc := range []struct {
		name  string
		total int
		want  string
	}{
		{"the record's cap", runstore.MaxTolerated + 7, "107 findings tolerated in all; the record lists 1 of them."},
		{"an entry the record dropped", 2, "2 findings tolerated in all; the record lists 1 of them."},
	} {
		rec := toleratedRec([]runstore.ToleratedFinding{row})
		rec.Tolerated.Total = tc.total
		got := Report(rec, "loc/", nil)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: the partial table does not say so (%q):\n%s", tc.name, tc.want, got)
		}
		if strings.Contains(got, "the first") {
			t.Errorf("%s: the table claims its rows are the first of the total:\n%s", tc.name, got)
		}
	}
	// A list that is the whole answer says nothing about a total.
	rec := toleratedRec([]runstore.ToleratedFinding{row})
	if whole := Report(rec, "loc/", nil); strings.Contains(whole, "tolerated in all") {
		t.Errorf("a complete list names a total:\n%s", whole)
	}
}

// TestReportWithoutToleratedDiffersByTheModeLine: a run that tolerated nothing
// is today's report plus the one line naming the mode it resolved — no
// section, no header, no count.
func TestReportWithoutToleratedDiffersByTheModeLine(t *testing.T) {
	rec := toleratedRec(nil)
	rec.Rigor = nil
	today := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)

	rec.Rigor = &runstore.RigorRecord{Mode: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize}
	got := Report(rec, "runs/acme-app/20261027-120000-abcd/", nil)

	const line = "**Rigor:** pro, from repo, at finalize\n\n"
	if !strings.Contains(got, line) {
		t.Fatalf("the mode line is missing:\n%s", got)
	}
	if stripped := strings.Replace(got, line, "", 1); stripped != today {
		t.Errorf("a run that tolerated nothing changed beyond the mode line:\n--- got ---\n%s\n--- want ---\n%s", stripped, today)
	}
}

// TestReportToleratedTitleStaysInsideItsCell: a title is the reviewer's own
// text, so it can carry a pipe, a backtick run or a line that reads as a
// heading. Each must render inside the cell it belongs to.
func TestReportToleratedTitleStaysInsideItsCell(t *testing.T) {
	rec := toleratedRec([]runstore.ToleratedFinding{{
		Title: "drop the cache | then ``rm -rf``\n# Heading\nand the rest", Mode: rigor.Pro,
		Likelihood: 3, Impact: 3, FixCost: 2, Class: rigor.Performance,
	}})
	got := Report(rec, "loc/", nil)
	if want := "| medium | 3 | 3 | 2 | performance | pro | ```drop the cache \\| then ``rm -rf`` # Heading and the rest``` |"; !strings.Contains(got, want) {
		t.Errorf("the title's row is not one cell holding it:\nwant %q\ngot:\n%s", want, got)
	}
	if strings.Contains(got, "\n# Heading") {
		t.Errorf("a title forged a heading:\n%s", got)
	}
}
