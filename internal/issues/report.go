package issues

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// newWindow is how long an issue counts as new in the terminal Issues section
// (spec §6).
const newWindow = 24 * time.Hour

// noPendingLine is the whole section when no issue is pending (spec §6): the
// section exists to get unfiled issues filed, and a machine with nothing to
// file gets one line rather than three empty lists.
const noPendingLine = "No pending issues."

// Thresholds are where the section's wording firms up (spec §6, T-IS15): a
// pending issue that has waited AfterDays or more, and an issue reported
// Recurrence times or more, print in the firmer wording. Both are the user's
// setting (`issues.nudge_after_days`, `issues.nudge_recurrence`), which
// localcfg's accessors answer with the defaults for a file that sets neither.
type Thresholds struct {
	// AfterDays is the age, in whole days, at which a pending issue's line
	// firms up.
	AfterDays int
	// Recurrence is the number of times an issue has been reported at which
	// its line firms up; duplicates merged from other reporters count (F21).
	Recurrence int
}

// SectionRow is one issue as the section prints it: the display data a listing
// of the shared store hands over, and nothing else. It is its own type rather
// than the record, so the section prints a listing it never read and cannot
// carry a reporter, a failed command or a path into a terminal.
type SectionRow struct {
	// Number is the project number, zero for an issue that has not been
	// uploaded yet (D1723); LocalID names it then.
	Number int
	// LocalID is `local-<run stem>`, the name a local issue is printed and
	// submitted by (F23).
	LocalID string
	// Class is the bucket the issue is saved in.
	Class Class
	// Status is where the issue stands; only a `pending` row is nudged.
	Status Status
	// Summary is the one-line summary the reporter wrote. It is text from
	// another machine, so it is printed through safetext (F6).
	Summary string
	// Created is when the issue was first reported.
	Created time.Time
	// Reports is how many times the issue has been reported: the first report
	// and every duplicate merged into it, which is what makes it recurring
	// (D1724, F21).
	Reports int
}

// Section renders the terminal Issues section (spec §6, D1729): the new,
// pending and recurring views of the rows, which the CLI prints after the
// digest's own output and never into the committed file — so nothing here is
// bound by the digest's rules and a one-line summary may be named.
//
// now is the caller's clock and fixes the 24-hour window and every age; th is
// the user's setting. The section is a pure function of the three, so the same
// rows at the same instant print the same bytes.
//
// When no row is pending the whole section is the one line noPendingLine.
func Section(rows []SectionRow, now time.Time, th Thresholds) string {
	var pending []SectionRow
	for _, r := range rows {
		if r.Status == StatusPending {
			pending = append(pending, r)
		}
	}
	if len(pending) == 0 {
		return noPendingLine + "\n"
	}
	// Oldest first (spec §6), and by name where two times are equal, so the
	// order is total and the section is the same bytes for the same rows.
	sort.Slice(pending, func(a, b int) bool {
		if !pending[a].Created.Equal(pending[b].Created) {
			return pending[a].Created.Before(pending[b].Created)
		}
		return rowKey(pending[a]) < rowKey(pending[b])
	})

	var fresh []SectionRow
	for _, r := range rows {
		if now.Sub(r.Created) <= newWindow {
			fresh = append(fresh, r)
		}
	}
	sort.Slice(fresh, func(a, b int) bool {
		if !fresh[a].Created.Equal(fresh[b].Created) {
			return fresh[a].Created.After(fresh[b].Created)
		}
		return rowKey(fresh[a]) < rowKey(fresh[b])
	})

	var recurring []SectionRow
	for _, r := range rows {
		if r.Reports > 1 {
			recurring = append(recurring, r)
		}
	}
	// Most reported first, which is what "how many times they have happened"
	// orders by, with the same name tie-break.
	sort.Slice(recurring, func(a, b int) bool {
		if recurring[a].Reports != recurring[b].Reports {
			return recurring[a].Reports > recurring[b].Reports
		}
		return rowKey(recurring[a]) < rowKey(recurring[b])
	})

	var blocks []string
	if len(fresh) > 0 {
		blocks = append(blocks, block("New in the last 24 hours:", fresh, func(r SectionRow) string {
			return fmt.Sprintf("- %s (%s) %s", rowName(r), r.Class, summaryText(r))
		}))
	}
	blocks = append(blocks, block("Pending, oldest first:", pending, func(r SectionRow) string {
		days, waited := waited(r.Created, now)
		line := fmt.Sprintf("- %s waiting %s", rowName(r), waited)
		if days >= th.AfterDays {
			line += ", please file it"
		}
		return fmt.Sprintf("%s: %s [fugaro issue submit %s]", line, summaryText(r), submitRef(r))
	}))
	if len(recurring) > 0 {
		blocks = append(blocks, block("Recurring, most reported first:", recurring, func(r SectionRow) string {
			line := fmt.Sprintf("- %s reported %d times", rowName(r), r.Reports)
			if r.Reports >= th.Recurrence {
				line += ", it keeps happening"
			}
			return line + ": " + summaryText(r)
		}))
	}
	return "## Issues\n\n" + strings.Join(blocks, "\n")
}

// block is one list of the section: its heading line and one line per row,
// each ending in a newline, so joining two blocks with a newline separates
// them with the blank line Markdown wants.
func block(head string, rows []SectionRow, line func(SectionRow) string) string {
	var b strings.Builder
	b.WriteString(head + "\n")
	for _, r := range rows {
		b.WriteString(line(r) + "\n")
	}
	return b.String()
}

// rowName is the row as the section names it: `#0007` once it has a number and
// the local ID before that (D1723, F23). The name is data from the shared
// store like any other, so it is printed through safetext.
func rowName(r SectionRow) string {
	if r.Number > 0 {
		return fmt.Sprintf("#%04d", r.Number)
	}
	return safetext.OneLine(r.LocalID)
}

// rowKey is the name the sort compares: the number zero-padded, so numbers
// order by value and not by their printed width, or the local ID, which sorts
// after every number and so never collides with one.
func rowKey(r SectionRow) string {
	if r.Number > 0 {
		return fmt.Sprintf("#%09d", r.Number)
	}
	return r.LocalID
}

// submitRef is the argument `fugaro issue submit` takes for the row: the
// number, or the local ID while the issue has none (F23).
func submitRef(r SectionRow) string {
	if r.Number > 0 {
		return fmt.Sprintf("%d", r.Number)
	}
	return safetext.OneLine(r.LocalID)
}

// summaryText is the row's summary as one safe line of a terminal.
func summaryText(r SectionRow) string { return safetext.OneLine(r.Summary) }

// waited is how long the issue has waited: the whole days since it was
// reported, and the same age as the section prints it. A record whose creation
// time is ahead of the clock — a hand-edited or a steered file — reads as less
// than a day rather than as a negative one.
func waited(created, now time.Time) (days int, text string) {
	days = int(now.Sub(created).Hours() / 24)
	switch {
	case days < 1:
		return days, "less than a day"
	case days == 1:
		return days, "1 day"
	default:
		return days, fmt.Sprintf("%d days", days)
	}
}
