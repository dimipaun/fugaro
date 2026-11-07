package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/dimipaun/fugaro/internal/digest"
)

// doctorReviewsDueID and doctorReviewsOldID are doctor's reviews-due checks
// (D963, docs/periodic-reviews/implementation-plan.md R2): the two states of
// one reading, and two IDs rather than one because a class is decided by an ID
// alone (doctorClassOf) and the plan puts the two in different classes. A
// closed period with no report is work the reader has to do
// (doctorClassActionable); an old newest report is the owner's cadence, and
// the line names the command for each choice (doctorClassDecision, where
// doctorDecisionIDs carries the ID). One ID would put one of them in the other
// block, and a line filed under "yours to decide" is read as a choice when the
// cadence has lapsed.
const (
	doctorReviewsDueID = "reviews-due"
	doctorReviewsOldID = "reviews-due-old"
)

// doctorReviewsCheck is doctor's reviews-due line: the checkout's own report
// folder, read the way `fugaro runs digest --due` reads it (R1, over R0's one
// due computation) and reported only where the cadence shows a gap.
//
// Two lines and otherwise silence:
//
//   - a weekly, monthly or yearly period that has closed with no report is the
//     warning: the cadence lapsed. It is only ever about a dimension the folder
//     already holds a report of, because a warning is an actionable line that
//     fails doctor --strict, and the dimension a repository does not report is
//     not a cadence it keeps: a monthly repository would be told off every
//     Monday for the week, a weekly one every 1st for the month, a daily one
//     every Monday. The seven closed days DuePeriods carries are left out for
//     the same reason, and they are the catch-up's own window besides -- a
//     missed day is what the next report run fills in, so the days a repository
//     fell behind on are the second line, not this one.
//   - the newest report being more than one period of its own dimension old is
//     the info line: nothing coarse has closed since it, and the report a
//     reader would expect next is late. That state is valid -- the cadence is
//     the user's (D965) -- so the line is a decision and names both commands.
//
// Silence is the common state and it is two states: a checkout that holds no
// report folder has never reviewed, and doctor does not nag a practice nobody
// adopted (the setup path is where that is said, once), and a checkout that is
// up to date has nothing to be told.
//
// Offline, like the other checkout checks and unlike the installation's: it
// reads this checkout's own folder and opens no project, no credential and no
// bucket, so it answers in a checkout that has never been set up and it runs
// in a coding agent's session. The folder is untrusted input, read by R1's own
// reader, so a file whose name is not D321's shape is no report at all.
func doctorReviewsCheck(root string, now time.Time) *doctorCheck {
	dir := filepath.Join(root, filepath.FromSlash(digest.Dir))
	// A folder nobody can read is answered as a folder that holds nothing:
	// this is the checkout's own tree, and doctor's other reads of it are the
	// same best effort (doctorLayerChecks). It is not a cloud read that failed,
	// which is the state doctor counts rather than passes over.
	folder, err := digestReportFiles(dir, "", nil)
	if err != nil || len(folder.rows) == 0 {
		return nil
	}
	if p, ok := doctorReviewsClosed(digest.DuePeriods(now), folder.rows); ok {
		return &doctorCheck{ID: doctorReviewsDueID, Severity: "warning",
			Problem: fmt.Sprintf("%s closed and this checkout holds no report for it, so the reviews have lapsed", p.Key+" ("+string(p.Dim)+")"),
			Fix:     fmt.Sprintf("run `fugaro runs digest --period %s --date %s`", p.Dim, p.Start)}
	}
	newest := digestNewestReport(folder.rows, now)
	if newest == nil || !doctorReviewsOld(newest, now) {
		return nil
	}
	return &doctorCheck{ID: doctorReviewsOldID, Severity: "info",
		Problem: fmt.Sprintf("the newest report under %s is %s, %s", digest.Dir, digestPeriodLabel(newest), digestDaysOld(newest.DaysAgo)),
		Fix:     fmt.Sprintf("keep the cadence with `fugaro runs digest --period %s`, or see what is due with `fugaro runs digest --due`", newest.Dim)}
}

// doctorReviewsClosed is the most recently closed week, month or year of due
// that the folder holds no report for, among the dimensions the folder keeps at
// all. The days and the dimensions the folder has never reported are passed
// over for the reason the check's own comment gives, and the newest is the one
// named: a state with more than one closed period missing (the 1st of January
// closes a month and a year) is answered by the one that closed last, and the
// command in the fix lists the rest.
func doctorReviewsClosed(due []digest.Period, rows []digest.Row) (digest.Period, bool) {
	var newest digest.Period
	found := false
	for _, p := range due {
		if p.Dim == digest.ByDay || digestHasReport(rows, p) || !doctorReviewsKeeps(rows, p.Dim) {
			continue
		}
		if found && !digestNewerPeriod(p, newest) {
			continue
		}
		newest, found = p, true
	}
	return newest, found
}

// doctorReviewsKeeps is whether the folder holds a report of this dimension at
// all: the cadence the repository shows, which is the only cadence doctor may
// hold it to. A folder of day reports is a repository that reports days, and
// the week it never wrote is not a week it lapsed on.
func doctorReviewsKeeps(rows []digest.Row, dim digest.Dim) bool {
	for _, r := range rows {
		if r.Dim == dim {
			return true
		}
	}
	return false
}

// doctorReviewsOld is whether the newest report is more than one period of its
// own dimension old: the days between the period's last day and now are more
// than the period's own length. Whole days on both sides, so a report of
// yesterday is not "more than a day old" at any hour of today.
func doctorReviewsOld(newest *digestDueReport, now time.Time) bool {
	p, err := digest.PeriodOfKey(newest.Dim, newest.Key)
	if err != nil {
		// A row's own path named its period, so this cannot happen; a report
		// without a period is no age to measure.
		return false
	}
	start, err := time.Parse("2006-01-02", p.Start)
	if err != nil {
		return false
	}
	end, err := time.Parse("2006-01-02", p.End)
	if err != nil {
		return false
	}
	length := int(end.Sub(start).Hours()/24) + 1
	return digestDaysAgo(p.End, now) > length
}
