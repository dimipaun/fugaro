package digest

import (
	"strings"
	"testing"
)

// TestFileBodyCarriesPatternsForEveryHead pins the two writers' one shared rule
// (internal/cli's `fugaro runs digest` and internal/runner's digest step both
// build a period's file here): a period's file ends with the pattern rules, the
// yearly one included. A year's head is built from its months rather than from
// records, so a body that appended the rules on one path only would leave the
// two writers' yearly files different for the same rows -- which no test of one
// writer alone can see.
func TestFileBodyCarriesPatternsForEveryHead(t *testing.T) {
	year, err := NewPeriod(ByYear, "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	rows := []Row{{
		Dim: ByMonth, Key: "2026-01", Rel: "2026/2026-01.md", Read: true,
		Summary: Summary{Period: "2026-01", Start: "2026-01-01", End: "2026-01-31", Runs: 3, Billed: "1.000000", Notional: "0.000000", Review: "0.000000"},
	}}
	day, err := NewPeriod(ByDay, "2026-02-01")
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

// TestPeriodOfKeyIsPeriodKeysInverse: every key PeriodKey writes is a period
// for PeriodOfKey, whatever the dimension, and it is the period the key came
// from -- the start and the end included, which is what a reader over a folder
// of files needs and cannot get from the key alone. The week cases are the
// interesting ones: a week's Monday is in another year for the one week a year
// spans the boundary, and 2026 has a 53rd.
func TestPeriodOfKeyIsPeriodKeysInverse(t *testing.T) {
	for _, tc := range []struct {
		dim  Dim
		date string
	}{
		{ByDay, "2026-10-25"},
		{ByWeek, "2026-10-21"},
		{ByMonth, "2026-10-21"},
		{ByYear, "2026-10-21"},
		{ByWeek, "2026-12-31"},
		{ByWeek, "2027-01-01"},
		{ByWeek, "2027-01-04"},
		{ByDay, "2026-01-01"},
		{ByMonth, "2027-01-01"},
		{ByYear, "2027-01-01"},
	} {
		want, err := NewPeriod(tc.dim, tc.date)
		if err != nil {
			t.Fatalf("NewPeriod(%s, %s): %v", tc.dim, tc.date, err)
		}
		got, err := PeriodOfKey(tc.dim, want.Key)
		if err != nil {
			t.Errorf("PeriodOfKey(%s, %q) refused a key PeriodKey wrote: %v", tc.dim, want.Key, err)
			continue
		}
		if got != want {
			t.Errorf("PeriodOfKey(%s, %q) = %+v, want the period of %s: %+v", tc.dim, want.Key, got, tc.date, want)
		}
	}
}

// TestPeriodOfKeyRefusesWhatTheLayoutCannotName: a key is a period only in the
// dimension its own shape gives, and only when it names one. A reader walking a
// hand-written folder gets an error rather than a period this package made up.
func TestPeriodOfKeyRefusesWhatTheLayoutCannotName(t *testing.T) {
	for _, tc := range []struct {
		name string
		dim  Dim
		key  string
	}{
		{"a day key read as a week", ByWeek, "2026-10-25"},
		{"a week key read as a day", ByDay, "2026-W43"},
		{"a month key read as a week", ByWeek, "2026-10"},
		{"a year key read as a month", ByMonth, "2026"},
		{"a dimension that is no period", ByRepo, "acme/app"},
		{"a week the year does not have", ByWeek, "2026-W54"},
		{"a week with no zero padding", ByWeek, "2026-W1"},
		{"a thirteenth month", ByMonth, "2026-13"},
		{"a month that is not a month", ByMonth, "2026-00"},
		{"30 February", ByDay, "2026-02-30"},
		{"a day that is not a date", ByDay, "2026-1-1"},
		{"nothing at all", ByDay, ""},
	} {
		if p, err := PeriodOfKey(tc.dim, tc.key); err == nil {
			t.Errorf("%s: PeriodOfKey(%s, %q) = %+v, and the layout names no such period", tc.name, tc.dim, tc.key, p)
		}
	}
}
