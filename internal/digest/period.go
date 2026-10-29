package digest

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// This file is the digest's own copy of the period vocabulary and the dollar
// rendering internal/report carries: the digest is MIT and must keep building
// with ee/ deleted (D444, T11), and internal/report moves to ee/report. The
// bodies, the values and the error wording are internal/report's, kept in step
// by period_test.go's pins; nothing else of a report is copied here.

// Dim is what a digest period groups by.
type Dim string

const (
	ByDay    Dim = "day"
	ByWeek   Dim = "week"
	ByMonth  Dim = "month"
	ByYear   Dim = "year"
	ByRepo   Dim = "repo"
	ByModel  Dim = "model"
	ByPerson Dim = "person"
	ByMode   Dim = "mode"
)

// ParseDim parses --by.
func ParseDim(s string) (Dim, error) {
	switch d := Dim(s); d {
	case ByDay, ByWeek, ByMonth, ByYear, ByRepo, ByModel, ByPerson, ByMode:
		return d, nil
	}
	return "", fmt.Errorf("--by %q: use day, week, month, year, repo, model, person or mode", s)
}

// IsPeriod says whether d groups by time.
func (d Dim) IsPeriod() bool { return d == ByDay || d == ByWeek || d == ByMonth || d == ByYear }

// PeriodKey is the key and the first day of date's period for a time grouping.
func PeriodKey(d Dim, date string) (key, start string, err error) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", fmt.Errorf("bad date %q", date)
	}
	switch d {
	case ByDay:
		return date, date, nil
	case ByWeek:
		y, w := t.ISOWeek()
		off := (int(t.Weekday()) + 6) % 7 // Monday = 0
		return fmt.Sprintf("%04d-W%02d", y, w), t.AddDate(0, 0, -off).Format("2006-01-02"), nil
	case ByMonth:
		return t.Format("2006-01"), t.Format("2006-01") + "-01", nil
	case ByYear:
		return t.Format("2006"), t.Format("2006") + "-01-01", nil
	}
	return "", "", fmt.Errorf("%s is not a period", d)
}

// USD6 is m as dollars with exactly six decimals, from the integer.
func USD6(m budget.Micros) string {
	if m < 0 {
		return "-" + USD6(-m)
	}
	return fmt.Sprintf("%d.%06d", int64(m)/1_000_000, int64(m)%1_000_000)
}

// USD is m for people: at least cents, up to six decimals when there are
// some, exact.
func USD(m budget.Micros) string {
	s := USD6(m)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	return "$" + s
}
