package issues

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/issueresolve"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// resolve.go is the pass `fugaro update` runs once it installed a release
// (F27): it asks GitHub what became of the project's filed issues, marks the
// ones this release fixes `resolved` in the shared store, and answers the
// findings for the update to print.
//
// It is best effort in every direction, because the thing it runs inside is
// the whole upgrade. GitHub unreachable or rate limited, the shared store
// unreadable or unwritable, a record a launcher rewrote into a shape this
// build cannot read, a panic in the middle of the pass: each is one line in
// Result.Skipped and nothing else, so `fugaro update` never fails, prompts or
// waits because of an issue check (F7). And a coding agent's session writes
// nothing at all (F16d): the findings come back for whoever reads the
// session's output and the store is left exactly as it is, because a
// session's writes are the person's own.
//
// The one thing it reads from GitHub is what the reader returns — a state and
// a release version — and no text of GitHub's is ever carried out of here:
// the summary a person sees is the record's own (F16a).

const (
	// passIssues is how many issues one pass asks about (F8). GitHub's
	// anonymous limit is 60 requests an hour for a whole office's address,
	// which `updatecheck`'s daily call shares, so a pass spends a third of it
	// at most — on the issues it has left alone longest, which is what the
	// cursor is for.
	passIssues = 20

	// passBudget bounds a pass in all (F7). Twenty asks at three seconds each
	// is a minute of an update that was asked for nothing of the kind, and
	// the issues behind the first one that times out are asked next time.
	passBudget = 20 * time.Second

	// hitByMax is how many of the people who hit an issue its line names
	// before it counts the rest instead (F10).
	hitByMax = 5
)

// Reader is the one question a pass asks about an issue: what GitHub shows
// about it. *issueresolve.Reader is the one production has; a test answers
// without a network.
type Reader interface {
	Fetch(ctx context.Context, number int) (issueresolve.State, error)
}

// ResolveDeps is what one pass reads and writes through. Every field is required.
type ResolveDeps struct {
	// Bucket is the project's shared store, under its own `issues/` prefix.
	Bucket *blobx.Bucket
	// Reader asks GitHub about one issue.
	Reader Reader
	// Cursor remembers when each number was last asked, so a pass asks the
	// issues it has left alone longest first: an issue that stays open for
	// months must not keep a newer one from ever being reached (F8).
	Cursor *issueresolve.Cursor
	// AgentMarker reports the environment variable that makes this a coding
	// agent's session, "" when it is a person's. It is the one case the pass
	// writes nothing (F16d).
	AgentMarker func() string
	// Now is the clock every transition is stamped with.
	Now func() time.Time
}

// Result is what one pass found and did.
type Result struct {
	// Fixed are the issues the installed release fixes, in number order. Each
	// one is already marked `resolved` in the shared store, unless this is a
	// coding agent's session.
	Fixed []Fixed
	// Quiet is how many issues GitHub closed without fixing them — as not
	// planned, or as a duplicate — and the pass set aside. They are counted
	// and never listed: a closure that fixes nothing is not something an
	// update has to say (F9).
	Quiet int
	// Skipped is why the pass did not do everything it set out to, in one
	// line of this program's own words, or "" when nothing went wrong. It is
	// text for a caller to print or to drop, never an error to handle (F7).
	Skipped string
}

// Fixed is one issue the installed release fixes, as `fugaro update` and the
// daily report print it.
type Fixed struct {
	// Number is the issue's project number.
	Number int
	// Summary is the one-line summary the reporter wrote, as the record holds
	// it. It is another machine's text, for a caller to print through
	// safetext (F16a).
	Summary string
	// Release is the release that shipped the fix: the issue's milestone, a
	// strict X.Y.Z at or below the version that was installed (F4, D1756).
	Release string
	// URL is the GitHub issue the record was filed as.
	URL string
	// HitBy are the people who ran into the issue — the reporter that filed
	// it, then everyone an occurrence names — each one once, through
	// safetext, at most hitByMax of them. A caller that shows them as data
	// uses this.
	HitBy []string
	// HitByLine is the same names as one line: `hit by Ana and Jack`, with
	// `and N more` when there were more than hitByMax of them, and "" when
	// nobody is named. It is built here so that both callers print it the
	// same way (F10).
	HitByLine string
}

// Resolve runs one pass over the project's filed issues and answers what the
// installed release fixes. installed is the release the update just brought
// the installation to: an issue counts as fixed when GitHub shows it closed
// as completed with a milestone at or below it (F4, D1756), and one whose
// milestone is above it is left for a later update.
//
// The error is always nil. Every way a pass can fail is Result.Skipped — one
// line, this program's own words — because the caller is `fugaro update`,
// which may not fail over an issue check (F7); the second result is part of
// the shape so a caller reads it like every other store call and can tell
// "found nothing" from "did not run" without parsing a sentence.
func Resolve(ctx context.Context, deps ResolveDeps, installed string) (res Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, passBudget)
	defer cancel()

	// A panic below is one line in Skipped and not an update that stops half
	// way (F7): the pass reads a store every launcher can write and decodes
	// answers from GitHub, and neither is allowed to take the upgrade down
	// with it.
	defer func() {
		if recover() != nil {
			res.note("the issue check stopped unexpectedly")
		}
	}()

	now := deps.Now()
	rows, err := ListShared(ctx, deps.Bucket)
	if err != nil {
		res.note("the project's issues could not be read")
		return res, nil
	}

	// Only a filed issue is a question for GitHub: `pending` was never filed,
	// and `dismissed` and `resolved` are where somebody — or this pass, on
	// another machine — already put it. A record whose link is not this
	// project's issue is not one to ask about either (F28): the store is
	// writable by every launcher, and the number the request is built from
	// has to be one this project filed.
	var numbers []int
	rowAt := make(map[int]SharedRow, len(rows))
	for _, row := range rows {
		if row.Status != StatusSubmitted || !projectIssueURL(row.URL) {
			continue
		}
		numbers = append(numbers, row.Number)
		rowAt[row.Number] = row
	}
	if len(numbers) == 0 {
		return res, nil
	}
	ordered := deps.Cursor.Order(numbers)
	if len(ordered) > passIssues {
		ordered = ordered[:passIssues]
	}
	var asked []int
	defer func() {
		if len(asked) == 0 {
			return
		}
		// Best effort: a cursor that cannot be written costs the next pass the
		// same asks in the same order, and nothing else (its own rule).
		_ = deps.Cursor.Mark(asked, now)
	}()

	for _, number := range ordered {
		asked = append(asked, number)
		state, ferr := deps.Reader.Fetch(ctx, number)
		if ferr != nil {
			res.note(skipNote(ferr))
			// An address GitHub will not answer for is an address it will not
			// answer for the next issue either, so the pass ends rather than
			// spending what is left of its budget on answers it already knows
			// (F8). Any other error is one issue's, and the issues behind it
			// are still asked about.
			if errors.Is(ferr, issueresolve.ErrRateLimited) || errors.Is(ferr, issueresolve.ErrTransport) {
				break
			}
			continue
		}
		switch s := state.(type) {
		case issueresolve.Open, issueresolve.NoVerdict:
			// Nothing has changed for the issue, or GitHub decided nothing
			// about it — a pull request, a close reason this reader does not
			// know, a close as completed with no release milestone. It is left
			// exactly as it is, and its number is in asked, which is what
			// keeps a pass from asking about it again before the issues
			// behind it (F8, F15).
		case issueresolve.ClosedNotPlanned:
			res.quiet(ctx, deps, number, ClosedNotPlanned, now)
		case issueresolve.ClosedDuplicate:
			res.quiet(ctx, deps, number, ClosedDuplicate, now)
		case issueresolve.ClosedCompleted:
			if updatecheck.AtOrBelow(s.Milestone, installed) {
				res.fix(ctx, deps, rowAt[number], s.Milestone, now)
			}
		}
	}
	// The order the issues were asked about is the cursor's, which is about
	// what this machine asked last and not about the issues: a caller prints
	// these, so they come back in number order, and two machines with
	// different cursors print the same list.
	sort.Slice(res.Fixed, func(a, b int) bool { return res.Fixed[a].Number < res.Fixed[b].Number })
	return res, nil
}

// quiet records an issue GitHub closed without fixing it: `dismissed`, with
// the reason GitHub gave (D1752), counted rather than listed (F9).
//
// The write is the store's, so a coding agent's session makes none: the
// finding is still counted and the record is left where it is (F16d).
func (r *Result) quiet(ctx context.Context, deps ResolveDeps, number int, reason ClosedAs, now time.Time) {
	r.Quiet++
	if deps.AgentMarker() != "" {
		return
	}
	if err := SetClosed(ctx, deps.Bucket, number, reason, 0, now); err != nil {
		r.note("one issue closed on GitHub could not be set aside")
	}
}

// fix records an issue the installed release fixes: it is marked `resolved`
// with the release that shipped the fix (the milestone, F4), and the finding
// is returned for a caller to print.
//
// The write is the store's and the finding is not. A coding agent's session
// writes nothing (F16d), and a write that fails leaves the finding as the
// finding it is: the issue stays `submitted` and the next pass meets it
// again.
func (r *Result) fix(ctx context.Context, deps ResolveDeps, row SharedRow, release string, now time.Time) {
	names, err := Reporters(ctx, deps.Bucket, row.Number)
	if err != nil {
		// The fix is known and the names are not: the line is printed without
		// them rather than the finding being dropped.
		r.note("the people who hit one issue could not be read")
	}
	hitBy, line := hitByLine(names)
	if deps.AgentMarker() == "" {
		if err := SetResolved(ctx, deps.Bucket, row.Number, release, 0, now); err != nil {
			r.note("one issue this release fixes could not be marked resolved")
		}
	}
	r.Fixed = append(r.Fixed, Fixed{
		Number:    row.Number,
		Summary:   row.Summary,
		Release:   release,
		URL:       row.URL,
		HitBy:     hitBy,
		HitByLine: line,
	})
}

// note records why the pass did not do everything it set out to, once: the
// text is one line a person reads, and the first thing that went wrong is
// what explains the ones behind it.
func (r *Result) note(why string) {
	if r.Skipped == "" {
		r.Skipped = why
	}
}

// skipNote is why the pass stopped or left one issue alone, in this program's
// own words: the error a reader returned carries whatever the transport said,
// and the line a person reads is written here rather than repeated from it.
func skipNote(err error) string {
	switch {
	case errors.Is(err, issueresolve.ErrRateLimited):
		return "GitHub's anonymous rate limit is reached"
	case errors.Is(err, issueresolve.ErrTransport):
		return "GitHub could not be reached"
	}
	return "one issue could not be checked"
}

// hitByLine is the one place the `hit by Ana and Jack` line is built (F10):
// the reporter that filed the issue and everyone an occurrence names, each
// one through safetext — they are strings every launcher can write — each one
// once, the first hitByMax named and the rest counted. The names come back
// with the line, for a caller that shows them as data.
func hitByLine(names []string) (hitBy []string, line string) {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = safetext.OneLine(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		hitBy = append(hitBy, name)
	}
	if len(hitBy) == 0 {
		return nil, ""
	}
	unique := hitBy
	if len(hitBy) > hitByMax {
		hitBy = hitBy[:hitByMax]
	}
	if rest := len(unique) - len(hitBy); rest > 0 {
		// The names read as a list and a count rather than as a sentence: a
		// line that said `A, B, C, D and E and 2 more` would say "and" twice.
		line = "hit by " + strings.Join(hitBy, ", ") + fmt.Sprintf(" and %d more", rest)
	} else {
		line = "hit by " + joinNames(hitBy)
	}
	return hitBy, line
}

// joinNames joins names the way a sentence does: `Ana`, `Ana and Jack`,
// `Ana, Jack and Jill`.
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
