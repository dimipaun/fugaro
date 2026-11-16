package digest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The Escalation section of a report (docs/outer-loop-escalation, D2034, E12):
// what the ladder did to this repository's work items, over the 24 hours and
// the 7 days ending with the period.
//
// It is the one part of a report that is not built from the period's run
// records. The ladder's ledger is the orchestrator's (E7): rung, reason code,
// counters, times and a head SHA, one item at a time, and the reader that can
// reach it is not the one that reads a bucket of records. So the section is
// built from two inputs the caller has and this package does not go looking
// for: the ledger's escalation blocks (EscalationItem) and the same repository's
// run records Build was handed, which carry the chain's cost and hours.
//
// Three of this package's rules hold here as everywhere:
//
//   - No text a record wrote, and no secret (G8). An item is a rung, a reason
//     code, a count, an instant and a pull-request number; the ledger holds no
//     prompt, no finding and no diff, and nothing here renders one. The one
//     sentence the section writes is built from the figures below (the
//     threshold suggestion), never from an input.
//
//   - Deterministic figures (G3). Nothing here reads a clock: both windows end
//     where the period closes (Period.EndsAt), every list is walked in a
//     defined order, and a tie in the median is broken by the arithmetic.
//
//   - "A regression" is a revert and nothing else (O1, F14). The records hold
//     no way to trace a regression to a merge, so the section reports the one
//     measurable proxy: a revert of the merge within 7 days. A follow-up, a
//     reopen, a failing check and a person's complaint are none of them a
//     regression here, and the section says so where it prints the figure.

// EscalationDayHours and EscalationWeekDays are the section's two windows,
// both ending where the period closes: the spec's "the last 24 hours and a
// rolling 7 days".
const (
	EscalationDayHours = 24
	EscalationWeekDays = 7
)

// EscalationRevertWindow is how long after a merge a revert still counts as a
// regression: the spec's "reverted within 7 days" (O1). A revert later than
// this is a revert like any other and not this merge's regression.
const EscalationRevertWindow = 7 * 24 * time.Hour

// MaxLongestOpen is how many open pull requests the section names: the
// longest-running ones are what a person acts on, and the list is a table in a
// report read in a pull request.
const MaxLongestOpen = 5

// EscalationStep is one move on the ladder, as the ledger recorded it: the
// rung entered, why, and when. From, the head SHA and the item are not here —
// the section counts rungs and reasons and prints no head (G8).
type EscalationStep struct {
	To     escalation.Rung
	Reason escalation.Reason
	At     time.Time
}

// EscalationFollowUp is one follow-up a rung 3 merge filed, as the reader
// found it: how urgent the judge called it (D2040) and whether it has been
// closed. It is a word and a bool, never the follow-up's text.
type EscalationFollowUp struct {
	Importance runstore.MergeJudgeImportance
	Closed     bool
}

// EscalationItem is one work item's ladder history, as the reader of the
// ledger built it: the escalation block the orchestrator wrote for it (E7),
// the pull request its chain opened, and what only the host can say about that
// pull request — whether it merged, and whether a revert named it.
//
// Repo is what makes the digest per repository (E12): the ledger is read per
// orchestrator and an installation has several, so an item names the
// repository it belongs to and EscalationSection keeps the ones its report is
// about.
type EscalationItem struct {
	// Repo is the repository the item's pull request is against.
	Repo string
	// ID is the work item's own id, which names it in the ledger. It is not
	// printed: a person reads pull requests here, and the section names no
	// title, no branch and no run (G1).
	ID string
	// Rung is the highest rung the item entered. A rung outside the vocabulary
	// — an item that entered none, or a ledger this build cannot read — is
	// counted and printed as unplaced rather than read as a rung (G5).
	Rung escalation.Rung
	// EnteredAt is when it entered that rung, and OuterAttempts is how many of
	// the ladder's own runs it has spent (E7).
	EnteredAt     time.Time
	OuterAttempts int
	// Transitions is the ladder's history for the item, oldest first.
	Transitions []EscalationStep
	// PR is the pull request the item's chain opened, and the key the chain's
	// records are found by: a record whose PR is this number is a run of this
	// item. 0 means the item has no pull request, which is an item the ladder
	// has not pushed.
	PR int
	// MergedAt is when that pull request merged, and nil while it has not. It
	// is the host's answer and not a rung: rung 3 is where a merge is decided,
	// and a rung 3 that the gate refused waits for a person (D2030, D2037).
	MergedAt *time.Time
	// RevertedAt is when a revert of that merge appeared, and nil when none
	// did. It is the host's answer — a revert pull request naming the merge —
	// and it is the whole of the regression proxy (O1).
	RevertedAt *time.Time
	// FollowUps are the follow-ups the item's rung 3 merge filed.
	FollowUps []EscalationFollowUp
	// Open is true while the pull request is neither merged nor closed, which
	// is what the longest-open list is built from.
	Open bool
}

// EscalationRungFigures is one rung's line: how many of the window's pull
// requests are at it, and how long the ones that merged took.
type EscalationRungFigures struct {
	Rung escalation.Rung
	// PRs is how many items reached exactly this rung.
	PRs int
	// Merged is how many of them merged, and MedianToMerge is the median of
	// their time to merge — the chain's first run's start to the merge. The
	// median is unknown rather than zero when no merged item has a readable
	// chain start (G5): a figure nobody can compute is not a fast merge.
	Merged        int
	MedianToMerge time.Duration
	MedianKnown   bool
}

// EscalationFigures is one window's figures.
type EscalationFigures struct {
	// Rungs is every rung, R0 first, so the table has the same rows whatever
	// the window holds.
	Rungs []EscalationRungFigures
	// Unplaced is how many of the window's items carry no rung this build
	// knows. They are counted and printed apart, never dropped: an item the
	// section cannot place is a ledger its reader must look at (G5).
	Unplaced int
	// Rung3Merges is how many rung 3 items merged: the merges the ladder
	// decided, which is what the regression proxy is measured against.
	Rung3Merges int
	// Reverts is how many of those merges a revert named within
	// EscalationRevertWindow — the regression proxy, and nothing else (O1).
	Reverts int
	// FollowUps is how many follow-ups the window's rung 3 merges filed, and
	// FollowUpsClosed how many of them have been closed.
	FollowUps       int
	FollowUpsClosed int
	// Churn is how many ladder transitions the churn comparator caused
	// (escalation.ReasonChurnNew and ReasonChurnSame): a reviewer raising new
	// minor findings, or the same finding over and over (E9).
	Churn int
	// PastR1 is the hours the window's items spent on the ladder past rung 1 —
	// the chain's first run's start to its merge, or to the window's end while
	// it is open — and PastR1Cost is those chains' billed dollars (G9).
	PastR1     time.Duration
	PastR1Cost budget.Micros
	// PastR1Items is how many items the two figures above are about.
	PastR1Items int
}

// EscalationOpenPR is one pull request the section names as long-running: its
// number, its rung, and how long it has been open.
type EscalationOpenPR struct {
	PR   int
	Rung escalation.Rung
	Age  time.Duration
}

// Escalation is a report's Escalation section: the ledger's figures for one
// repository, over the two windows, the longest-running open pull requests,
// and the threshold suggestion as one sentence.
type Escalation struct {
	// Repo is the repository the section is about.
	Repo string
	// Items is how many of the repository's items the ledger held at all,
	// whatever window they fall in.
	Items int
	// Day and Week are the two windows, the 24 hours and the 7 days ending
	// where the period closes.
	Day  EscalationFigures
	Week EscalationFigures
	// Longest is the open pull requests, oldest first, at most MaxLongestOpen
	// of them.
	Longest []EscalationOpenPR
	// Suggestion is the section's threshold suggestion, as text built from the
	// figures above and nothing else (spec "The report suggests threshold
	// changes"). A person reads it and edits the local configuration; nothing
	// applies it.
	Suggestion string
}

// EscalationSection builds repo's Escalation section from the ledger's items
// and the repository's run records, over the 24 hours and the 7 days ending
// where p closes. It returns nil when the period has no end to anchor its
// windows at (a Period built by hand) or when the ledger holds no item of the
// repository at all: a section of zeroes is a figure nobody measured, and the
// report says nothing rather than printing one (G5).
//
// recs are the same records Build was handed — one repository's, in any order —
// and the chain's hours and cost are read from them. An item whose pull request
// has no record is still counted (the ledger is what the section is about), and
// its chain's hours and cost are simply unknown.
func EscalationSection(repo string, items []EscalationItem, recs []runstore.Record, p Period) *Escalation {
	end, ok := p.EndsAt()
	if !ok {
		return nil
	}
	day := end.Add(-EscalationDayHours * time.Hour)
	week := end.Add(-EscalationWeekDays * 24 * time.Hour)

	chains := chainStarts(recs)
	e := &Escalation{Repo: repo}
	dayItems, weekItems := []EscalationItem{}, []EscalationItem{}
	for _, it := range items {
		if it.Repo != repo {
			continue
		}
		e.Items++
		at := lastActivity(it)
		if !at.Before(day) && at.Before(end) {
			dayItems = append(dayItems, it)
		}
		if !at.Before(week) && at.Before(end) {
			weekItems = append(weekItems, it)
		}
	}
	if e.Items == 0 {
		return nil
	}
	e.Day = figures(dayItems, chains, day, end)
	e.Week = figures(weekItems, chains, week, end)
	e.Longest = longestOpen(items, repo, end)
	e.Suggestion = suggestion(e.Week)
	return e
}

// chainFacts is what a run record says about one pull request's chain: the
// start of its first run, which is where the item's hours are measured from,
// and what the chain billed.
type chainFacts struct {
	start   time.Time
	started bool
	billed  budget.Micros
}

// chainStarts is the records grouped by pull request: the earliest start of
// each chain and what the chain billed. A record with no pull request, or one
// whose start is the zero time, is in no chain: a run that opened nothing is
// not a rung's run.
func chainStarts(recs []runstore.Record) map[int]chainFacts {
	out := map[int]chainFacts{}
	for i := range recs {
		rec := &recs[i]
		if rec.PR == nil || rec.PR.Number < 1 || rec.StartedAt.IsZero() {
			continue
		}
		f := out[rec.PR.Number]
		if !f.started || rec.StartedAt.Before(f.start) {
			f.start = rec.StartedAt
		}
		f.started = true
		f.billed = satAdd(f.billed, billed(rec))
		out[rec.PR.Number] = f
	}
	return out
}

// figures is one window's figures: the items whose last ladder activity fell
// in [from, end).
func figures(items []EscalationItem, chains map[int]chainFacts, from, end time.Time) EscalationFigures {
	f := EscalationFigures{Rungs: rungRows()}
	byRung := map[escalation.Rung][]time.Duration{}
	merged := map[escalation.Rung]int{}
	for _, it := range items {
		if !escalation.KnownRung(it.Rung) {
			f.Unplaced++
			continue
		}
		for i := range f.Rungs {
			if f.Rungs[i].Rung == it.Rung {
				f.Rungs[i].PRs++
				break
			}
		}
		ch, known := chains[it.PR]
		if it.MergedAt != nil && known {
			merged[it.Rung]++
			byRung[it.Rung] = append(byRung[it.Rung], it.MergedAt.Sub(ch.start))
		}
		if it.Rung == escalation.R3 {
			if it.MergedAt != nil {
				f.Rung3Merges++
				f.FollowUps += len(it.FollowUps)
				for _, fu := range it.FollowUps {
					if fu.Closed {
						f.FollowUpsClosed++
					}
				}
				if it.RevertedAt != nil && it.RevertedAt.After(*it.MergedAt) &&
					it.RevertedAt.Sub(*it.MergedAt) <= EscalationRevertWindow {
					f.Reverts++
				}
			}
		}
		for _, tr := range it.Transitions {
			if tr.Reason == escalation.ReasonChurnNew || tr.Reason == escalation.ReasonChurnSame {
				f.Churn++
			}
		}
		if it.Rung.Rank() >= escalation.R2.Rank() {
			f.PastR1Items++
			f.PastR1Cost = satAdd(f.PastR1Cost, ch.billed)
			f.PastR1 += pastR1Hours(it, ch, known, end)
		}
	}
	for i := range f.Rungs {
		f.Rungs[i].Merged = merged[f.Rungs[i].Rung]
		if d, ok := medianDuration(byRung[f.Rungs[i].Rung]); ok {
			f.Rungs[i].MedianToMerge, f.Rungs[i].MedianKnown = d, true
		}
	}
	return f
}

// pastR1Hours is how long one item's chain ran: its first run's start to its
// merge, or to the window's end while the pull request is still open. It is
// zero when the chain has no readable start (G5).
func pastR1Hours(it EscalationItem, ch chainFacts, known bool, end time.Time) time.Duration {
	if !known {
		return 0
	}
	to := end
	if it.MergedAt != nil && it.MergedAt.Before(end) {
		to = *it.MergedAt
	}
	if !to.After(ch.start) {
		return 0
	}
	return to.Sub(ch.start)
}

// lastActivity is when the item was last on the ladder: its merge when it
// merged after its last transition, its newest transition otherwise, and its
// entry when it has neither. It is what places an item in a window, and it
// reads no clock.
func lastActivity(it EscalationItem) time.Time {
	at := it.EnteredAt
	for _, tr := range it.Transitions {
		if tr.At.After(at) {
			at = tr.At
		}
	}
	if it.MergedAt != nil && it.MergedAt.After(at) {
		at = *it.MergedAt
	}
	return at
}

// longestOpen is the repository's open pull requests, oldest first, at most
// MaxLongestOpen of them. Age is measured to the instant the period closes,
// from the chain's first run's start, so a report of a closed period is the
// same report later.
func longestOpen(items []EscalationItem, repo string, end time.Time) []EscalationOpenPR {
	var open []EscalationOpenPR
	for _, it := range items {
		if it.Repo != repo || !it.Open || it.MergedAt != nil || it.PR < 1 {
			continue
		}
		age := time.Duration(0)
		for _, tr := range it.Transitions {
			if tr.At.Before(end) && end.Sub(tr.At) > age {
				age = end.Sub(tr.At)
			}
		}
		if it.EnteredAt.Before(end) && end.Sub(it.EnteredAt) > age {
			age = end.Sub(it.EnteredAt)
		}
		open = append(open, EscalationOpenPR{PR: it.PR, Rung: it.Rung, Age: age})
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].Age != open[j].Age {
			return open[i].Age > open[j].Age
		}
		return open[i].PR < open[j].PR
	})
	if len(open) > MaxLongestOpen {
		open = open[:MaxLongestOpen]
	}
	return open
}

// suggestion is the section's threshold advice, built from the window's
// figures alone: a rung 3 merge with no revert is the evidence the thresholds
// can come down, a revert is the evidence they should go up, and a window with
// no rung 3 merge is no evidence either way. It is text in a report; a person
// edits the local configuration, and nothing applies it (spec "The report
// suggests threshold changes").
func suggestion(w EscalationFigures) string {
	switch {
	case w.Rung3Merges == 0:
		return "no change suggested: no rung 3 merge in the last 7 days, so there is nothing to measure the thresholds against."
	case w.Reverts == 0:
		return fmt.Sprintf("lower the thresholds: %d rung 3 %s in the last 7 days and no revert within 7 days (O1's proxy).",
			w.Rung3Merges, plural(w.Rung3Merges, "merge", "merges"))
	case w.Reverts == w.Rung3Merges:
		return fmt.Sprintf("raise the thresholds: every one of the last 7 days' %d rung 3 %s was reverted within 7 days (O1's proxy).",
			w.Rung3Merges, plural(w.Rung3Merges, "merge", "merges"))
	default:
		return fmt.Sprintf("raise the thresholds: %d of the last 7 days' %d rung 3 %s %s reverted within 7 days (O1's proxy).",
			w.Reverts, w.Rung3Merges, plural(w.Rung3Merges, "merge", "merges"),
			plural(w.Reverts, "was", "were"))
	}
}

// rungRows is the rung table's rows, R0 first: the same rows in every window,
// so two windows of one report cannot disagree about which rungs exist.
func rungRows() []EscalationRungFigures {
	rows := make([]EscalationRungFigures, 0, len(escalation.Rungs))
	for _, r := range escalation.Rungs {
		rows = append(rows, EscalationRungFigures{Rung: r})
	}
	return rows
}

// medianDuration is the median of ds, and whether there is one: an empty list
// has no median at all (G5). An even count is the arithmetic mean of the two
// middles, which is exact in the nanoseconds the times carry.
func medianDuration(ds []time.Duration) (time.Duration, bool) {
	if len(ds) == 0 {
		return 0, false
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid], true
	}
	return (sorted[mid-1] + sorted[mid]) / 2, true
}

// plural picks a word for n, so a one-item figure does not read as a many-item
// one.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// escalationMarkdown is the section as the report prints it, or "" when the
// report carries none. Every cell is a figure, a rung of the closed
// vocabulary, a pull-request number or a duration; the one sentence is the
// suggestion, which this package built from those figures (G1, G8).
func escalationMarkdown(e *Escalation) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Escalation\n\n")
	fmt.Fprintf(&b, "The escalation ladder's ledger for this repository, over the 24 hours and the 7 days ending with this period: %d work %s in all. "+
		"A rung is where the ladder left an item; \"rung 3 merges\" is what it merged there; \"reverts\" is the regression proxy (O1), a revert of such a merge within 7 days and nothing else; \"n/a\" is a figure nobody could measure (G5).\n\n",
		e.Items, plural(e.Items, "item", "items"))
	windows := []struct {
		name string
		f    EscalationFigures
	}{{"24h", e.Day}, {"7d", e.Week}}
	b.WriteString("| window | items | R0 | R1 | R2 | R3 | R4 | unplaced | rung 3 merges | reverts | follow-ups | closed | churn | hours past R1 | cost past R1 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, w := range windows {
		counts := map[escalation.Rung]int{}
		items := 0
		for _, r := range w.f.Rungs {
			counts[r.Rung] = r.PRs
			items += r.PRs
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %s | %s |\n",
			w.name, items, counts[escalation.R0], counts[escalation.R1], counts[escalation.R2],
			counts[escalation.R3], counts[escalation.R4], w.f.Unplaced, w.f.Rung3Merges, w.f.Reverts,
			w.f.FollowUps, w.f.FollowUpsClosed, w.f.Churn,
			hours(w.f.PastR1, w.f.PastR1Items), USD(w.f.PastR1Cost))
	}
	// The median time to merge, per rung, in a table of its own: it is a
	// duration per rung and not a count of items, so as extra columns of the
	// table above the header would carry the five rung names twice.
	b.WriteString("\n| window | R0 | R1 | R2 | R3 | R4 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, w := range windows {
		med := map[escalation.Rung]string{}
		for _, r := range w.f.Rungs {
			med[r.Rung] = medianToMerge(r)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", w.name,
			med[escalation.R0], med[escalation.R1], med[escalation.R2], med[escalation.R3], med[escalation.R4])
	}
	b.WriteString("\n")
	if len(e.Longest) > 0 {
		b.WriteString("Longest open: ")
		parts := make([]string, 0, len(e.Longest))
		for _, o := range e.Longest {
			rung := string(o.Rung)
			if !escalation.KnownRung(o.Rung) {
				rung = "unplaced"
			}
			parts = append(parts, fmt.Sprintf("#%d (%s, %s)", o.PR, rung, hours(o.Age, 1)))
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString(".\n\n")
	}
	fmt.Fprintf(&b, "Thresholds: %s\n", e.Suggestion)
	return b.String()
}

// hours is a duration as whole hours and minutes, or "n/a" when nobody could
// measure it (G5): a figure of zero items is not a figure of no hours.
func hours(d time.Duration, n int) string {
	if n == 0 {
		return "n/a"
	}
	total := int64(d / time.Minute)
	return fmt.Sprintf("%dh%02dm", total/60, total%60)
}

// medianToMerge is a rung's median time to merge as the table prints it, or
// "n/a" where no merged item of the rung had a readable chain start (G5): a
// median nobody could compute is not a fast merge.
func medianToMerge(f EscalationRungFigures) string {
	if !f.MedianKnown {
		return "n/a"
	}
	return hours(f.MedianToMerge, 1)
}
