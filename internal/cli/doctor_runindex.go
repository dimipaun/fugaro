package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// doctor_runindex.go is RI6 (F9): doctor's line about a run index that is not
// keeping up, so that a person who reads a short list learns why it is short.
//
// It reads the installation's summary document -- runIndexMeta/summary, the
// one document the index keeps per installation (runindex.Summary) -- with the
// operator's own credentials, through the same Firestore client the spend
// history is read with, rather than through the Reader the commands compile
// against. Two reasons. The Reader cannot say the index is absent: an
// installation with no index database answers "incomplete" through it
// (emptyIndexReader, runindex_wire.go), and an installation whose index has
// never been built is not one with an incomplete index -- there is nothing to
// rebuild, and `fugaro update` builds the first one. And the fact the line is
// decided on is the document's own: a Reader merges the RTDB overlay in and so
// answers a different question (D3202).
//
// A document that is absent, or a summary that is complete with a stamp inside
// the budget database's own retention, is silence -- and that is the state of
// every installation this release reaches, since nothing writes the summary
// until the history job's fold and reindex land (RI4, RI5). It is why the fix
// may name `fugaro runs reindex` while that command is newer than this file:
// the line cannot be reached before the release that fills the summary, and the
// release that fills it is the one that ships the command.
//
// A read that did not answer is an info line carrying the unread marker, never
// silence: whether the index is complete is then unknown, and "unknown" is not
// "nothing to say" (doctorClassOf). The finding itself is info and not a
// warning -- an index still building is the ordinary state of a young
// installation, not something wrong -- so it never fails `--strict`, and it is
// in doctorActionableInfoIDs because the one thing to do about it is a command.

// runIndexIncompleteCheckID is doctor's line about the run index: one line,
// whatever state the summary is in.
const runIndexIncompleteCheckID = "run-index-incomplete"

// runIndexFix is the one thing a reader does about an index that is not keeping
// up: the rebuild, which walks every run in the runs bucket and recomputes the
// summary from the index (RI5).
const runIndexFix = "run `fugaro runs reindex`: it walks every run in the runs bucket and rebuilds the index and its summary"

// doctorRunIndexChecks reads the index's summary and reports the one state F9
// asks for: a summary that is not complete, or one whose last fold is older
// than the days the budget database keeps.
func doctorRunIndexChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		// No Firebase project, no Firestore database for the index to live in:
		// the state of every installation that has not run
		// `fugaro update --firebase-project`, and the same silence
		// doctorPolicyChecks keeps for the same reason.
		return nil
	}
	fs, err := newReportFirestore(ctx, lc)
	if err != nil {
		return runIndexUnread(err)
	}
	doc, err := fs.Get(ctx, runindex.SummaryCollection, runindex.SummaryDocument)
	switch {
	case errors.Is(err, firestore.ErrNotFound):
		// A missing document is not an incomplete index, and this arm covers a
		// missing database too (firestore.ErrNoDatabase wraps ErrNotFound): no
		// walk has written a summary yet, so there is nothing to rebuild, and a
		// line here would send a reader to rebuild what is not there.
		return nil
	case err != nil:
		return runIndexUnread(err)
	}
	// The document is decoded into the contract's own Summary (C-INDEX), so the
	// field names are the ones the writer put there. No version is asked of it:
	// the two facts this line reads are the document's own and a field never
	// changes meaning, so a summary a newer history job wrote is read rather
	// than refused.
	fields, err := json.Marshal(doc.Fields)
	if err != nil {
		return runIndexUnread(err)
	}
	var s runindex.Summary
	if err := json.Unmarshal(fields, &s); err != nil {
		return runIndexUnread(fmt.Errorf("the summary document is not one this Fugaro reads: %w", err))
	}
	now := budgetNow()
	if s.Complete && !runIndexFoldStale(s, now) {
		return nil
	}
	return []doctorCheck{{ID: runIndexIncompleteCheckID, Severity: "info",
		Problem: runIndexProblem(s, now), Fix: runIndexFix}}
}

// runIndexFoldStale reports whether the index's last fold is older than the
// budget database's own retention, by the rollover's own comparison
// (budget.PruneAfter, the age in days beyond which a day may leave the
// database). A stamp that old is what the rollover turns into `complete:
// false` and what makes the next sweep restart the walk (RI4), and doctor reads
// it as well because the summary is written by a sweep on its own clock: the
// stamp can be older than the retention before the sweep that reacts to it.
//
// A summary with no stamp is not stale: it has never folded, and the arm that
// reports that is `complete` being false.
func runIndexFoldStale(s runindex.Summary, now time.Time) bool {
	return s.FoldedThrough != nil && budget.Day(now)-budget.Day(*s.FoldedThrough) > budget.PruneAfter
}

// runIndexProblem is the state in the check's own words, with the two figures
// the reader has nowhere else: how much the index holds, and where its last
// fold reached. The first sentence is the one the default view prints, so it
// carries the consequence -- a listing that reads the index is short -- and the
// rest stays for --all and --json.
func runIndexProblem(s runindex.Summary, now time.Time) string {
	if !runIndexFoldStale(s, now) {
		return fmt.Sprintf("the run index is incomplete, so `fugaro runs ls`, `plan status` and `graph` show fewer runs than the runs bucket holds. "+
			"It holds %d runs, and the walk that builds it has not reached the end of history", s.Runs)
	}
	last := fmt.Sprintf("its last fold reached %s, %s ago, older than the %d days the budget database keeps",
		s.FoldedThrough.UTC().Format("2006-01-02"), ageDays(now.Sub(*s.FoldedThrough)), budget.PruneAfter)
	if s.Complete {
		return fmt.Sprintf("the run index has stopped keeping up with history: %s. "+
			"It holds %d runs, and the runs that changed since the fold are missing from it", last, s.Runs)
	}
	return fmt.Sprintf("the run index is incomplete and has stopped keeping up with history: %s. "+
		"It holds %d runs, and the runs that changed since the fold are missing from it", last, s.Runs)
}

// runIndexUnread is the check for a summary doctor could not read: an unknown
// rather than a pass, so it carries the unread marker and the class counts it
// (doctorClassOf). It is info and never a warning for the same reason the
// finding is: nothing about the installation is known to be wrong.
func runIndexUnread(err error) []doctorCheck {
	return []doctorCheck{{ID: runIndexIncompleteCheckID, Severity: "info", unread: true,
		Problem: fmt.Sprintf("whether the run index is complete could not be checked (%v), so whether a listing reads the whole history is unknown rather than fine", err),
		Fix:     "check that the credentials doctor uses can read the project's Firestore, then run doctor again"}}
}
