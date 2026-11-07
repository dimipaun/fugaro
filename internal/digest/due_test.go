package digest

import (
	"slices"
	"testing"
	"time"
)

// The due computation's own cases. Its reader is elsewhere -- a report run's
// step (internal/runner), and the CLI's due signal (D962, F6, F17) is the one
// the move into this package is for -- so what is pinned here is the calendar
// itself: the seven closed days, the week on a Monday, the month on the 1st
// and the year on 1 January. A reader that disagreed with this file would be a
// second implementation of "due", which is what the move exists to prevent.

// TestDuePeriodsIsTheClosedWindow: a report is due for every closed day the
// catch-up covers, newest first, whether or not the repository ran anything: a
// day with no runs still gets its file, which is how a reader tells "no runs
// that day" from "no report run that day" (G5). The clock is a Wednesday past
// the first of the month, so the window is the seven days alone and this test
// says nothing about the week or the month.
//
// Mutation (count from today rather than yesterday, or cover six days): the
// list is not the seven days before the clock, in that order.
func TestDuePeriodsIsTheClosedWindow(t *testing.T) {
	// The window is the constant the launcher's own manifest is sized around
	// (internal/cli's digestManifestWindow covers these days plus the month a
	// monthly report needs), so a value that moved silently would leave a
	// report run short of records it is due to read.
	if DueCatchUpDays != 7 {
		t.Errorf("DueCatchUpDays = %d, want the seven closed days a report run catches up", DueCatchUpDays)
	}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	want := []Period{
		{Dim: ByDay, Key: "2026-10-20", Start: "2026-10-20", End: "2026-10-20"},
		{Dim: ByDay, Key: "2026-10-19", Start: "2026-10-19", End: "2026-10-19"},
		{Dim: ByDay, Key: "2026-10-18", Start: "2026-10-18", End: "2026-10-18"},
		{Dim: ByDay, Key: "2026-10-17", Start: "2026-10-17", End: "2026-10-17"},
		{Dim: ByDay, Key: "2026-10-16", Start: "2026-10-16", End: "2026-10-16"},
		{Dim: ByDay, Key: "2026-10-15", Start: "2026-10-15", End: "2026-10-15"},
		{Dim: ByDay, Key: "2026-10-14", Start: "2026-10-14", End: "2026-10-14"},
	}
	if got := DuePeriods(now); !slices.Equal(got, want) {
		t.Errorf("DuePeriods(%s) = %+v, want the seven closed days %+v", now.Format("2006-01-02"), got, want)
	}
}

// TestDuePeriodsAddsTheWeekOnAMonday: the week that closed with yesterday is
// due on a Monday, beside the seven days it covers -- so a weekly report is
// written the day after its own Sunday, and the days inside it are written on
// the same run.
//
// Mutation (key the week by today, or ask for a Sunday rather than a Monday):
// the week's key is the days' own Monday or the week is missing, and the
// report run's committed tree is not the tree this file says is due.
func TestDuePeriodsAddsTheWeekOnAMonday(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	want := []Period{
		{Dim: ByDay, Key: "2026-10-18", Start: "2026-10-18", End: "2026-10-18"},
		{Dim: ByDay, Key: "2026-10-17", Start: "2026-10-17", End: "2026-10-17"},
		{Dim: ByDay, Key: "2026-10-16", Start: "2026-10-16", End: "2026-10-16"},
		{Dim: ByDay, Key: "2026-10-15", Start: "2026-10-15", End: "2026-10-15"},
		{Dim: ByDay, Key: "2026-10-14", Start: "2026-10-14", End: "2026-10-14"},
		{Dim: ByDay, Key: "2026-10-13", Start: "2026-10-13", End: "2026-10-13"},
		{Dim: ByDay, Key: "2026-10-12", Start: "2026-10-12", End: "2026-10-12"},
		{Dim: ByWeek, Key: "2026-W42", Start: "2026-10-12", End: "2026-10-18"},
	}
	if got := DuePeriods(now); !slices.Equal(got, want) {
		t.Errorf("DuePeriods(%s) = %+v, want the seven closed days and the week that closed with yesterday %+v", now.Format("2006-01-02"), got, want)
	}
}

// TestDuePeriodsAddsTheMonthAndYearOnTheFirst: the coarser periods are due the
// day after they end -- the month on the 1st and the year on 1 January -- and
// both are keyed by that last day, so the report that is due on 1 January is
// the December that closed and not the month just begun. A 1st that is a
// Monday closes a week and a month at once, and both are due.
//
// Mutation (key the month or the year by today, or drop the year on 1
// January): the first-of-January case loses the year, or the month's key is
// the month that has just begun rather than the one that closed.
func TestDuePeriodsAddsTheMonthAndYearOnTheFirst(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want []Period
	}{
		{
			name: "the month that closed",
			now:  time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
			want: []Period{
				{Dim: ByDay, Key: "2026-10-31", Start: "2026-10-31", End: "2026-10-31"},
				{Dim: ByDay, Key: "2026-10-30", Start: "2026-10-30", End: "2026-10-30"},
				{Dim: ByDay, Key: "2026-10-29", Start: "2026-10-29", End: "2026-10-29"},
				{Dim: ByDay, Key: "2026-10-28", Start: "2026-10-28", End: "2026-10-28"},
				{Dim: ByDay, Key: "2026-10-27", Start: "2026-10-27", End: "2026-10-27"},
				{Dim: ByDay, Key: "2026-10-26", Start: "2026-10-26", End: "2026-10-26"},
				{Dim: ByDay, Key: "2026-10-25", Start: "2026-10-25", End: "2026-10-25"},
				{Dim: ByMonth, Key: "2026-10", Start: "2026-10-01", End: "2026-10-31"},
			},
		},
		{
			name: "the month and the year that closed",
			now:  time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC),
			want: []Period{
				{Dim: ByDay, Key: "2026-12-31", Start: "2026-12-31", End: "2026-12-31"},
				{Dim: ByDay, Key: "2026-12-30", Start: "2026-12-30", End: "2026-12-30"},
				{Dim: ByDay, Key: "2026-12-29", Start: "2026-12-29", End: "2026-12-29"},
				{Dim: ByDay, Key: "2026-12-28", Start: "2026-12-28", End: "2026-12-28"},
				{Dim: ByDay, Key: "2026-12-27", Start: "2026-12-27", End: "2026-12-27"},
				{Dim: ByDay, Key: "2026-12-26", Start: "2026-12-26", End: "2026-12-26"},
				{Dim: ByDay, Key: "2026-12-25", Start: "2026-12-25", End: "2026-12-25"},
				{Dim: ByMonth, Key: "2026-12", Start: "2026-12-01", End: "2026-12-31"},
				{Dim: ByYear, Key: "2026", Start: "2026-01-01", End: "2026-12-31"},
			},
		},
		{
			name: "a 1st that is a Monday closes a week and a month at once",
			now:  time.Date(2027, 2, 1, 12, 0, 0, 0, time.UTC),
			want: []Period{
				{Dim: ByDay, Key: "2027-01-31", Start: "2027-01-31", End: "2027-01-31"},
				{Dim: ByDay, Key: "2027-01-30", Start: "2027-01-30", End: "2027-01-30"},
				{Dim: ByDay, Key: "2027-01-29", Start: "2027-01-29", End: "2027-01-29"},
				{Dim: ByDay, Key: "2027-01-28", Start: "2027-01-28", End: "2027-01-28"},
				{Dim: ByDay, Key: "2027-01-27", Start: "2027-01-27", End: "2027-01-27"},
				{Dim: ByDay, Key: "2027-01-26", Start: "2027-01-26", End: "2027-01-26"},
				{Dim: ByDay, Key: "2027-01-25", Start: "2027-01-25", End: "2027-01-25"},
				{Dim: ByWeek, Key: "2027-W04", Start: "2027-01-25", End: "2027-01-31"},
				{Dim: ByMonth, Key: "2027-01", Start: "2027-01-01", End: "2027-01-31"},
			},
		},
	} {
		if got := DuePeriods(tc.now); !slices.Equal(got, tc.want) {
			t.Errorf("%s: DuePeriods(%s) = %+v, want %+v", tc.name, tc.now.Format("2006-01-02"), got, tc.want)
		}
	}
}

// TestDuePeriodsSkipsADateItCannotName: every date here is one this package
// formatted itself, and the layout still has to be able to name it -- its
// report shapes carry a four-digit year (layout.go's reportShapes). A clock
// past 9999 is where that bites: the newest closed day formats as a
// five-digit year, NewPeriod refuses it, and the days behind it are reported
// as usual rather than the refusal being written into a report of its own. The
// clock is the 2nd of January, so neither the week nor the month is due at it.
//
// Mutation (append the period NewPeriod returned despite its error): an
// unnameable period with an empty key is added where the newest day belongs,
// and a writer of this layout would be handed a period no path can name.
func TestDuePeriodsSkipsADateItCannotName(t *testing.T) {
	now := time.Date(10000, time.January, 2, 12, 0, 0, 0, time.UTC)
	want := []Period{
		{Dim: ByDay, Key: "9999-12-31", Start: "9999-12-31", End: "9999-12-31"},
		{Dim: ByDay, Key: "9999-12-30", Start: "9999-12-30", End: "9999-12-30"},
		{Dim: ByDay, Key: "9999-12-29", Start: "9999-12-29", End: "9999-12-29"},
		{Dim: ByDay, Key: "9999-12-28", Start: "9999-12-28", End: "9999-12-28"},
		{Dim: ByDay, Key: "9999-12-27", Start: "9999-12-27", End: "9999-12-27"},
		{Dim: ByDay, Key: "9999-12-26", Start: "9999-12-26", End: "9999-12-26"},
	}
	if got := DuePeriods(now); !slices.Equal(got, want) {
		t.Errorf("DuePeriods(%s) = %+v, want the six closed days the layout can name and not the unnameable %s", now.Format("2006-01-02"), got, "10000-01-01")
	}
}
