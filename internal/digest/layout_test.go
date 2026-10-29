package digest

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/report"
)

// TestFileBodyCarriesPatternsForEveryHead pins the two writers' one shared rule
// (internal/cli's `fugaro runs digest` and internal/runner's digest step both
// build a period's file here): a period's file ends with the pattern rules, the
// yearly one included. A year's head is built from its months rather than from
// records, so a body that appended the rules on one path only would leave the
// two writers' yearly files different for the same rows -- which no test of one
// writer alone can see.
func TestFileBodyCarriesPatternsForEveryHead(t *testing.T) {
	year, err := NewPeriod(report.ByYear, "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	rows := []Row{{
		Dim: report.ByMonth, Key: "2026-01", Rel: "2026/2026-01.md", Read: true,
		Summary: Summary{Period: "2026-01", Start: "2026-01-01", End: "2026-01-31", Runs: 3, Billed: "1.000000", Notional: "0.000000", Review: "0.000000"},
	}}
	day, err := NewPeriod(report.ByDay, "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		head []byte
		p    Period
		cur  Summary
	}{
		{
			name: "a year built from its months",
			head: YearMarkdown(year, MonthsOf(rows, year.Key), RollUp(year.Key, MonthsOf(rows, year.Key))),
			p:    year,
			cur:  RollUp(year.Key, MonthsOf(rows, year.Key)),
		},
		{
			name: "a day built from records",
			head: Markdown(Digest{Period: day}),
			p:    day,
			cur:  Summary{Period: day.Key},
		},
	} {
		body := FileBody(tc.head, rows, tc.p, tc.cur)
		if !strings.Contains(string(body), "## Patterns\n") {
			t.Errorf("%s: the file carries no Patterns section:\n%s", tc.name, body)
		}
		if !strings.HasPrefix(string(body), string(tc.head)) {
			t.Errorf("%s: the file does not begin with the head its writer built:\n%s", tc.name, body)
		}
	}
}
