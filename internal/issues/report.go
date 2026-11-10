package issues

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// newWindow is how long an issue counts as new in the terminal Issues section
// (spec §6).
const newWindow = 24 * time.Hour

// noPendingLine is the whole section when it has nothing to say at all
// (spec §6): the section exists to get unfiled issues filed, and a machine with
// nothing to file and nothing its last update fixed gets one line rather than
// empty lists.
const noPendingLine = "No pending issues."

// resolvedHead is the heading of the section's list of fixed issues, and
// sectionUpgradeLead opens the one line that follows it when this machine runs
// a release older than a fix the list holds (F19). Both are the section's own
// wording, pinned by its tests and by the docs.
const (
	resolvedHead       = "Resolved since the last update:"
	sectionUpgradeLead = "Upgrade to "
)

// Versions is the releases the section's Resolved list and its upgrade line
// read (F17, D1754, D1757): the two releases the last update spanned, and the
// release this machine's own binary is.
type Versions struct {
	// Previous is the release the installation was on before the last update
	// (`previous_release`), empty in the first record an installation writes:
	// the window reaches back to the beginning then.
	Previous string
	// Release is the release the last update applied (`release`). An empty one
	// means there is no record to read — an installation that has never updated
	// with a build that writes one — and the list is omitted: without a window
	// there is no answer to which fixes are new.
	Release string
	// Running is this machine's own version (`cli.Version`). The upgrade line
	// appears when the list holds a fix newer than it; a value that is not a
	// release (a dev build) is newer than nothing.
	Running string
}

// Holds reports whether the window holds release: at or below the one the last
// update applied and above the one before it. A release that is not a strict
// X.Y.Z is in no window, and so is every release when there is no record to
// read.
func (v Versions) Holds(release string) bool {
	if v.Release == "" || !updatecheck.AtOrBelow(release, v.Release) {
		return false
	}
	return v.Previous == "" || !updatecheck.AtOrBelow(release, v.Previous)
}

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
	// ResolvedIn is the release that fixed the issue (`resolved_in`, F4): set on
	// a `resolved` row and empty otherwise. The Resolved list is the rows whose
	// value the installation's window holds.
	ResolvedIn string
	// HitBy is who ran into a fixed issue, as one line (`hit by Ana and Jack`,
	// F10), built from the record's reporters by the caller that read them. It
	// is printed on a resolved row and nowhere else.
	HitBy string
}

// Section renders the terminal Issues section (spec §6, D1729): the new,
// pending, recurring and resolved views of the rows, which the CLI prints after
// the digest's own output and never into the committed file — so nothing here
// is bound by the digest's rules and a one-line summary may be named.
//
// now is the caller's clock and fixes the 24-hour window and every age; th is
// the user's setting; v is the releases the last update spanned, which is what
// the Resolved list is a window over and what the upgrade line is judged
// against. The section is a pure function of the four, so the same rows at the
// same instant print the same bytes.
//
// When the section has nothing to say — no pending issue and no fix its window
// holds — the whole section is the one line noPendingLine.
func Section(rows []SectionRow, v Versions, now time.Time, th Thresholds) string {
	var pending, resolved []SectionRow
	for _, r := range rows {
		switch {
		case r.Status == StatusPending:
			pending = append(pending, r)
		case r.Status == StatusResolved && v.Holds(r.ResolvedIn):
			resolved = append(resolved, r)
		}
	}
	if len(pending) == 0 && len(resolved) == 0 {
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
		// A fixed issue is not a bug that keeps happening: it left the list the
		// moment a release fixed it, whichever window it is in.
		if r.Reports > 1 && r.Status != StatusResolved {
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

	// Number order, as every listing of the store is: the window says which
	// fixes are in it, and the numbers say which order a person reads them in.
	sort.Slice(resolved, func(a, b int) bool { return rowKey(resolved[a]) < rowKey(resolved[b]) })

	var blocks []string
	if len(fresh) > 0 {
		blocks = append(blocks, block("New in the last 24 hours:", fresh, func(r SectionRow) string {
			return fmt.Sprintf("- %s (%s) %s", rowName(r), r.Class, summaryText(r))
		}))
	}
	if len(pending) > 0 {
		blocks = append(blocks, block("Pending, oldest first:", pending, func(r SectionRow) string {
			days, waited := waited(r.Created, now)
			line := fmt.Sprintf("- %s waiting %s", rowName(r), waited)
			if days >= th.AfterDays {
				line += ", please file it"
			}
			return fmt.Sprintf("%s: %s [fugaro issue submit %s]", line, summaryText(r), submitRef(r))
		}))
	}
	if len(recurring) > 0 {
		blocks = append(blocks, block("Recurring, most reported first:", recurring, func(r SectionRow) string {
			line := fmt.Sprintf("- %s reported %d times", rowName(r), r.Reports)
			if r.Reports >= th.Recurrence {
				line += ", it keeps happening"
			}
			return line + ": " + summaryText(r)
		}))
	}
	if len(resolved) > 0 {
		blocks = append(blocks, block(resolvedHead, resolved, func(r SectionRow) string {
			line := fmt.Sprintf("- %s fixed in %s", rowName(r), r.ResolvedIn)
			if who := safetext.OneLine(r.HitBy); who != "" {
				line += ", " + who
			}
			return line + ": " + summaryText(r)
		}))
	}
	if nudge := upgradeLine(resolved, v.Running); nudge != "" {
		blocks = append(blocks, nudge+"\n")
	}
	return "## Issues\n\n" + strings.Join(blocks, "\n")
}

// upgradeLine is the section's one line for a machine that is behind, or "":
// `Upgrade to 0.22.0 for these fixes: brew upgrade …, then fugaro update.`
//
// It names the highest release the Resolved list holds — the newest fix the
// person reading is missing — and only when this machine's own release is
// below it (F19). A release that is not a strict X.Y.Z compares as below
// nothing, which is what keeps a dev build from being told to upgrade to what
// it is: the same rule every comparison of a version here follows.
//
// The command is updatecheck's constant and never anything a store supplied:
// the section is read by a person (D1729) and the line is meant to be typed.
func upgradeLine(resolved []SectionRow, running string) string {
	newest := ""
	for _, r := range resolved {
		if newest == "" || updatecheck.Newer(r.ResolvedIn, newest) {
			newest = r.ResolvedIn
		}
	}
	if newest == "" || !updatecheck.Newer(newest, running) {
		return ""
	}
	return fmt.Sprintf("%s%s for these fixes: %s, then fugaro update.", sectionUpgradeLead, newest, updatecheck.BrewCommand)
}

// HitByLine is who ran into one issue, as the one line the section prints on a
// resolved row (`hit by Ana and Jack`, F10), for a caller that has the names
// from the store and only the line to print. It is the line `fugaro update`
// prints for the issues it fixes: both read the same builder, so a person
// cannot be told two different things about one issue.
func HitByLine(names []string) string {
	_, line := hitByLine(names)
	return line
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
