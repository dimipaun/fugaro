package digest

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// These pins are the literals internal/report's own code answers, written from
// one run of it so this copy can be held to the original without importing it
// (internal/report moves to ee/report; the digest must build without it).

func TestParseDimAcceptsTheVocabulary(t *testing.T) {
	for _, want := range []Dim{ByDay, ByWeek, ByMonth, ByYear, ByRepo, ByModel, ByPerson, ByMode} {
		got, err := ParseDim(string(want))
		if err != nil || got != want {
			t.Errorf("ParseDim(%q) = %q, %v; want %q", want, got, err, want)
		}
	}
	_, err := ParseDim("weekly")
	if err == nil {
		t.Fatal("ParseDim accepted a word outside the vocabulary")
	}
	const want = `--by "weekly": use day, week, month, year, repo, model, person or mode`
	if err.Error() != want {
		t.Errorf("the refusal = %q, want %q", err.Error(), want)
	}
}

func TestIsPeriodAnswersTheFourPeriods(t *testing.T) {
	for _, tc := range []struct {
		dim  Dim
		want bool
	}{
		{ByDay, true}, {ByWeek, true}, {ByMonth, true}, {ByYear, true},
		{ByRepo, false}, {ByModel, false}, {ByPerson, false}, {ByMode, false},
	} {
		if got := tc.dim.IsPeriod(); got != tc.want {
			t.Errorf("IsPeriod(%s) = %v, want %v", tc.dim, got, tc.want)
		}
	}
}

func TestPeriodKeyAnswersThePeriods(t *testing.T) {
	for _, tc := range []struct {
		dim              Dim
		date, key, start string
	}{
		{ByDay, "2026-10-21", "2026-10-21", "2026-10-21"},
		{ByWeek, "2026-10-21", "2026-W43", "2026-10-19"},
		{ByMonth, "2026-10-21", "2026-10", "2026-10-01"},
		{ByYear, "2026-10-21", "2026", "2026-01-01"},
		// The ISO-week year boundary: both of these days are 2026-W53.
		{ByWeek, "2026-12-31", "2026-W53", "2026-12-28"},
		{ByWeek, "2027-01-01", "2026-W53", "2026-12-28"},
	} {
		key, start, err := PeriodKey(tc.dim, tc.date)
		if err != nil || key != tc.key || start != tc.start {
			t.Errorf("PeriodKey(%s, %s) = %q, %q, %v; want %q, %q", tc.dim, tc.date, key, start, err, tc.key, tc.start)
		}
	}

	_, _, err := PeriodKey(ByRepo, "2026-10-21")
	if err == nil || err.Error() != "repo is not a period" {
		t.Errorf("PeriodKey(ByRepo) = %v, want the refusal that names it", err)
	}
	if _, _, err := PeriodKey(ByDay, "not-a-date"); err == nil || err.Error() != `bad date "not-a-date"` {
		t.Errorf("PeriodKey on a non-date = %v, want the refusal that names it", err)
	}
}

func TestDollarRenderingPins(t *testing.T) {
	for _, tc := range []struct {
		m         budget.Micros
		usd6, usd string
	}{
		{0, "0.000000", "$0.00"},
		{1, "0.000001", "$0.000001"},
		{1234, "0.001234", "$0.001234"},
		{1000000, "1.000000", "$1.00"},
		{123456789012, "123456.789012", "$123456.789012"},
		{-2500000, "-2.500000", "$-2.50"},
	} {
		if got := USD6(tc.m); got != tc.usd6 {
			t.Errorf("USD6(%d) = %q, want %q", int64(tc.m), got, tc.usd6)
		}
		if got := USD(tc.m); got != tc.usd {
			t.Errorf("USD(%d) = %q, want %q", int64(tc.m), got, tc.usd)
		}
	}
}
