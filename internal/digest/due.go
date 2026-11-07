package digest

import "time"

// This file is "which periods has this repository closed and not reported":
// the one implementation of the due computation, which a report run's own step
// reads and which the CLI's due signal (D962, F6, F17) was moved here for. It
// is here rather than in the runner because internal/runner is a package a CLI
// read must not import into -- it would drag the whole runner into the CLI's
// dependency graph -- and because two implementations of "due" that drifted
// apart would let a person's reading and a report run's disagree about which
// periods are missing (docs/periodic-reviews/implementation-plan.md, R0).

// DueCatchUpDays is how many closed days a report is due for at once. The
// report run is launched by the user's own routine and Fugaro schedules none
// (D965), so a missed day must be filled by the next run: the launcher's
// manifest window covers these days plus the month a monthly report needs
// (internal/cli's digestManifestWindow).
const DueCatchUpDays = 7

// DuePeriods is the periods a report is due for at now: the seven closed days
// the catch-up covers, and the week, month or year that closed since the last
// one. A day with no run of the repository still gets its file, which is how a
// reader tells "no runs that day" from "no report run that day" (G5).
//
// The coarser periods are due on the day after they ended: the week on a
// Monday (yesterday is its Sunday), the month on the 1st, and the year on 1
// January. A 1st that is a Monday closes a week and a month at once, and both
// are due.
//
// Nothing here reads a clock: now is the caller's, the day the report run or
// the person believes it is. The result is newest first, and an empty window
// is an empty slice rather than nil, so a reader that renders it prints no
// periods rather than a null.
func DuePeriods(now time.Time) []Period {
	today := now.UTC()
	type due struct {
		dim  Dim
		date string
	}
	var dues []due
	for i := 1; i <= DueCatchUpDays; i++ {
		dues = append(dues, due{ByDay, today.AddDate(0, 0, -i).Format("2006-01-02")})
	}
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	if today.Weekday() == time.Monday {
		dues = append(dues, due{ByWeek, yesterday})
	}
	if today.Day() == 1 {
		dues = append(dues, due{ByMonth, yesterday})
		if today.Month() == time.January {
			dues = append(dues, due{ByYear, yesterday})
		}
	}
	out := make([]Period, 0, len(dues))
	for _, d := range dues {
		// A date this computation formatted is a date this package's own
		// clock reads; one the layout cannot name is a period no report is
		// written for, so it is left out rather than handed on with an empty
		// key.
		p, err := NewPeriod(d.dim, d.date)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}
