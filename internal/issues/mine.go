package issues

import (
	"sort"
	"time"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// hideAfter is how long a finished issue stays in the list: one resolved or
// dismissed more than this long ago is hidden unless --all (F13).
const hideAfter = 30 * 24 * time.Hour

// Display is what a row says about where its issue stands. It is the record's
// own status plus the two views that are not record values: `local` for an
// issue this machine has not uploaded, and `regression` for one this user has
// hit again after a release fixed it (F2). It is derived here and nowhere
// else, so the printer and the snapshot cannot disagree about a row.
type Display string

const (
	// DisplayLocal has no project number yet: it is this machine's alone.
	DisplayLocal Display = "local"
	// DisplayPending is in the shared store and not filed yet.
	DisplayPending Display = "pending"
	// DisplaySubmitted is filed on GitHub.
	DisplaySubmitted Display = "submitted"
	// DisplayResolved was fixed by a release.
	DisplayResolved Display = "resolved"
	// DisplayDismissed was set aside.
	DisplayDismissed Display = "dismissed"
	// DisplayRegression is a resolved issue this user has hit again.
	DisplayRegression Display = "regression"
)

// Coverage is the answer to "is this machine on the version that fixes the
// row's issue" (F5, D1782): `covered` when the CLI and the installation
// record are both at or above the fixing release, `behind` when either is not
// — the one case that prints the `fugaro update` nudge — `cli_only` when the
// installation's release is unknown and the CLI alone decides, which the row
// says out loud (O4), and `unknown` when a version cannot be compared at all.
// `none` is every row the question does not apply to.
type Coverage string

const (
	// CoverageNone is a row with no fixing release to compare against.
	CoverageNone Coverage = "none"
	// CoverageCovered is both versions at or above the fixing release.
	CoverageCovered Coverage = "covered"
	// CoverageCLIOnly is the CLI at or above it with the installation
	// unchecked.
	CoverageCLIOnly Coverage = "cli_only"
	// CoverageBehind is the CLI or the installation below it.
	CoverageBehind Coverage = "behind"
	// CoverageUnknown is a version that is not a release, or none at all.
	CoverageUnknown Coverage = "unknown"
)

// MineStatus is the shared record's status as a row reads it: the record's
// four values, `resolved` among them, which the local file's own three do not
// have — only the shared store says an issue is fixed (F13).
type MineStatus string

const (
	MineStatusPending   MineStatus = "pending"
	MineStatusSubmitted MineStatus = "submitted"
	MineStatusResolved  MineStatus = "resolved"
	MineStatusDismissed MineStatus = "dismissed"
)

// MineLocal is one of this machine's own issue files, as a row reads it: the
// fields a row shows, and not the record. It is deliberately not Issue, so
// that a row can carry the time a transition stamped — a dismissal that never
// reached the shared store still has to age out (F4) — without the row model
// being coupled to the record's format.
type MineLocal struct {
	// Number is the project number the shared store gave the issue, zero
	// while it is still this machine's alone and LocalID names it.
	Number  int
	LocalID string
	Summary string
	// Status is the local file's own, so it is never `resolved`: the shared
	// record is the truth once an issue is uploaded.
	Status Status
	// The four dates are the file's own; a nil one is a transition that has
	// not happened, and a row with none is never hidden (F4).
	HitAt       *time.Time
	SubmittedAt *time.Time
	ResolvedAt  *time.Time
	DismissedAt *time.Time
	URL         string
	ResolvedIn  string
	// Regression says the user's own hit was flagged a regression against
	// ResolvedIn when the store matched this file to an issue (F25 there).
	Regression bool
}

// MineSharedIssue is one issue the shared store says this user is a reporter
// on: the creator of the record, or the reporter of a duplicate merged into
// someone else's issue (F7). The reader derives every field of it, so that
// this stays a pure function of its argument: HitAt is this user's own hit and
// OthersHit a count of other people, never anyone's name (F15).
type MineSharedIssue struct {
	Number      int
	Summary     string
	Status      MineStatus
	HitAt       *time.Time
	SubmittedAt *time.Time
	ResolvedAt  *time.Time
	DismissedAt *time.Time
	URL         string
	ResolvedIn  string
	// Regression is this user's own flagged occurrence, never another
	// reporter's (F14).
	Regression bool
	// OthersHit counts the other people who hit the issue, this user never
	// among them and no name ever attached.
	OthersHit int
}

// MineShared is one shared read's whole answer: the user's issues, the time
// the store was read at — "as of", where the spec asked for GitHub's
// "last checked" (F6) — and whether the listing was cut short. Offline there
// is no answer at all and a snapshot stands in for it (F9).
type MineShared struct {
	AsOf      time.Time
	Issues    []MineSharedIssue
	Truncated bool
}

// MineInput is everything the rows are built from: the two sources of issues,
// this machine's two versions, the clock and the `--all` flag. None of it is
// read by BuildRows, which is why the ordering, the hiding and the version
// comparisons can be pinned without a store, a file or a network.
type MineInput struct {
	// Local is this machine's own issue files, already the ones that belong
	// to the project `mine` is running in (F8b).
	Local []MineLocal
	// Shared is the last read of the shared store; the zero value is a run
	// that had none.
	Shared MineShared
	// Version is this binary's own version and Installed the release the
	// installation record names, "" when it could not be read (F5).
	Version   string
	Installed string
	Now       time.Time
	// All keeps the rows the 30-day rule would hide (F13).
	All bool
}

// MineRow is one line of `mine`: the display data of one issue, with no
// command and nothing of another reporter's in it (F8, F9, F15).
type MineRow struct {
	// Number is the project number, zero for a row that is still this
	// machine's alone and named by LocalID.
	Number  int
	LocalID string
	Summary string
	Display Display
	// The four dates are the row's own times, the hit time included; a nil
	// one is a transition that has not happened.
	HitAt       *time.Time
	SubmittedAt *time.Time
	ResolvedAt  *time.Time
	DismissedAt *time.Time
	URL         string
	ResolvedIn  string
	Coverage    Coverage
	// OthersHit counts other people, never this user (F15).
	OthersHit int
	// AsOf is when the row's state was last known: the shared read's time for
	// a row the store answered for, and this run's clock for one only the
	// local files have (F9).
	AsOf time.Time
}

// The five groups of D1783, in the order they print: a regression first, then
// an issue a release fixed and this machine has not been updated to, then the
// issues still waiting on someone (this machine's own, and the team's pending
// ones), then the filed ones, and last the finished ones — resolved and
// dismissed alike.
const (
	groupRegression = iota
	groupBehind
	groupWaiting
	groupFiled
	groupFinished
)

// group is the row's place in that order.
func (r MineRow) group() int {
	switch r.Display {
	case DisplayRegression:
		return groupRegression
	case DisplayResolved:
		if r.Coverage == CoverageBehind {
			return groupBehind
		}
		return groupFinished
	case DisplayLocal, DisplayPending:
		return groupWaiting
	case DisplaySubmitted:
		return groupFiled
	}
	// A status this build does not know is not one to sort by a rule it was
	// never given: it goes last, where the row is still shown and still read.
	return groupFinished
}

// BuildRows turns the two sources into the rows `mine` prints, ordered by the
// five groups of D1783 with the newest or the oldest first as each group's
// question needs (F13), and with the rows the 30-day rule hides left out, and
// says how many those were.
//
// An issue both sources have is one row: once an issue is uploaded the shared
// record is the truth, and this machine's file is only its copy (F13), so the
// shared view's status, dates and URL win.
func BuildRows(in MineInput) (rows []MineRow, hidden int) {
	rows = make([]MineRow, 0, len(in.Shared.Issues)+len(in.Local))
	uploaded := make(map[int]bool, len(in.Shared.Issues))
	for _, s := range in.Shared.Issues {
		rows = append(rows, in.sharedRow(s))
		uploaded[s.Number] = true
	}
	for _, l := range in.Local {
		if l.Number != 0 && uploaded[l.Number] {
			continue
		}
		rows = append(rows, in.localRow(l))
	}
	sortRows(rows)
	if in.All {
		return rows, 0
	}
	kept := rows[:0]
	for _, r := range rows {
		if r.agedOut(in.Now) {
			hidden++
			continue
		}
		kept = append(kept, r)
	}
	return kept, hidden
}

// sharedRow is one issue as the shared store answered for it.
func (in MineInput) sharedRow(s MineSharedIssue) MineRow {
	display := s.display()
	return MineRow{
		Number:      s.Number,
		Summary:     s.Summary,
		Display:     display,
		HitAt:       s.HitAt,
		SubmittedAt: s.SubmittedAt,
		ResolvedAt:  s.ResolvedAt,
		DismissedAt: s.DismissedAt,
		URL:         s.URL,
		ResolvedIn:  s.ResolvedIn,
		Coverage:    in.coverage(display, s.ResolvedIn),
		OthersHit:   s.OthersHit,
		AsOf:        in.Shared.AsOf,
	}
}

// localRow is one issue only this machine has. Its as-of is this run's clock,
// because the file is the state that was just read: nothing here is a stale
// answer from somewhere else.
func (in MineInput) localRow(l MineLocal) MineRow {
	display := l.display()
	row := MineRow{
		Number:      l.Number,
		LocalID:     l.LocalID,
		Summary:     l.Summary,
		Display:     display,
		HitAt:       l.HitAt,
		SubmittedAt: l.SubmittedAt,
		ResolvedAt:  l.ResolvedAt,
		DismissedAt: l.DismissedAt,
		URL:         l.URL,
		ResolvedIn:  l.ResolvedIn,
		Coverage:    in.coverage(display, l.ResolvedIn),
		AsOf:        in.Now,
	}
	if row.Number != 0 {
		// A row is named by its number once it has one: the local name is
		// this machine's own business, and the snapshot and the JSON hold one
		// name per row.
		row.LocalID = ""
	}
	return row
}

// display is F2's rule for a shared record: the user's own flagged occurrence
// on a resolved issue makes a regression, everything else is the record's own
// status.
func (s MineSharedIssue) display() Display {
	if s.Regression && s.Status == MineStatusResolved {
		return DisplayRegression
	}
	switch s.Status {
	case MineStatusPending:
		return DisplayPending
	case MineStatusSubmitted:
		return DisplaySubmitted
	case MineStatusResolved:
		return DisplayResolved
	case MineStatusDismissed:
		return DisplayDismissed
	}
	return Display(s.Status)
}

// display is F2's rule for a local file: an issue dismissed here is dismissed
// even before it is uploaded, an issue with no number is local whatever it is
// waiting for, and a flagged file is a regression against the release the
// store matched it to.
func (l MineLocal) display() Display {
	if l.Regression && l.ResolvedIn != "" {
		return DisplayRegression
	}
	switch l.Status {
	case StatusDismissed:
		return DisplayDismissed
	case StatusSubmitted:
		return DisplaySubmitted
	}
	if l.Number == 0 {
		return DisplayLocal
	}
	return Display(l.Status)
}

// coverage answers F5 for a row that has a fixing release to compare: a
// resolved row, and a regression row, which by its own definition is at or
// above that release. Every other row is `none`: there is nothing it could be
// behind.
//
// A fixing release or a CLI that is not a release cannot be compared, so the
// answer is `unknown` — a dev build can never be updated to anything (D1736),
// and a record whose `resolved_in` is not a release names no version to check
// (F11). A release the installation record does not name is `cli_only`: the
// CLI alone decides, and the row says the installation was not checked (O4).
// An installation release that cannot be parsed is not known to be at or
// above, which is `behind` — the nudge is `fugaro update`, the one command
// that would also repair the record (F17).
func (in MineInput) coverage(display Display, resolvedIn string) Coverage {
	if display != DisplayResolved && display != DisplayRegression {
		return CoverageNone
	}
	if _, ok := updatecheck.CanonicalRelease(resolvedIn); !ok {
		return CoverageUnknown
	}
	if !updatecheck.Valid(in.Version) {
		return CoverageUnknown
	}
	if !updatecheck.AtOrBelow(resolvedIn, in.Version) {
		return CoverageBehind
	}
	if in.Installed == "" {
		return CoverageCLIOnly
	}
	if !updatecheck.AtOrBelow(resolvedIn, in.Installed) {
		return CoverageBehind
	}
	return CoverageCovered
}

// agedOut reports whether the row has fallen out of the list. Only a finished
// row is ever hidden, measured from the change that put it there, and a row
// whose file carries no such time is never hidden: an issue of unknown age is
// not one to drop (F4, F13).
func (r MineRow) agedOut(now time.Time) bool {
	if r.group() != groupFinished {
		return false
	}
	at, ok := r.finishedAt()
	return ok && now.Sub(at) > hideAfter
}

// finishedAt is when the row reached the state it is in now: the later of the
// two terminal stamps, which is the one its own transition wrote.
func (r MineRow) finishedAt() (time.Time, bool) {
	var at time.Time
	ok := false
	for _, t := range []*time.Time{r.ResolvedAt, r.DismissedAt} {
		if t != nil && (!ok || t.After(at)) {
			at, ok = *t, true
		}
	}
	return at, ok
}

// sortRows puts the rows in the order they print: the groups of D1783, and
// inside a group the newest or the oldest first as that group's question
// needs. Two rows the same instant apart are ordered by number, so the same
// input always prints the same list.
func sortRows(rows []MineRow) {
	sort.Slice(rows, func(a, b int) bool { return rows[a].before(rows[b]) })
}

// before is sortRows' rule for one pair of rows.
func (r MineRow) before(o MineRow) bool {
	group, other := r.group(), o.group()
	if group != other {
		return group < other
	}
	switch group {
	case groupRegression, groupBehind, groupFinished:
		// The newest status change first: a row that just moved is the one to
		// look at, and a regression has no stamp of its own, so it sorts by
		// the hit that flagged it.
		if !r.changedAt().Equal(o.changedAt()) {
			return r.changedAt().After(o.changedAt())
		}
	default:
		// The oldest hit first: the longest wait is the loudest.
		if !timeOrZero(r.HitAt).Equal(timeOrZero(o.HitAt)) {
			return timeOrZero(r.HitAt).Before(timeOrZero(o.HitAt))
		}
	}
	if r.Number != o.Number {
		return r.Number < o.Number
	}
	return r.LocalID < o.LocalID
}

// changedAt is the row's last status change: the latest of the times it
// carries, the hit time included.
func (r MineRow) changedAt() time.Time {
	at := timeOrZero(r.HitAt)
	for _, t := range []*time.Time{r.ResolvedAt, r.DismissedAt} {
		if t != nil && t.After(at) {
			at = *t
		}
	}
	return at
}

// timeOrZero is a date that may be absent, as a time sorting can compare: a
// row with no such time is the oldest of its group rather than a panic.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
