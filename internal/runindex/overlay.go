package runindex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The Realtime Database's two nodes the overlay reads (D3202,
// contract-index.md): the live registry, whose entries a run writes itself
// while it is going, and the settled outcomes, one node per finished run under
// the UTC day it finished on. The installation already has both -- they are the
// budget database's own -- so the overlay costs no new store and no new IAM.
const (
	agentsRoot   = "agents"
	outcomesRoot = "outcomes"
)

// overlayFutureSkew is how far ahead of the reader's own clock a heartbeat may
// be and still count as one: a run's machine whose clock is a little fast is
// not a run that has ended. It is the same skew the budget database's crash
// sweep allows, so the overlay and the sweep agree about which nodes are stale.
const overlayFutureSkew = time.Minute

// overlayRow is the whole of what the overlay knows about one run, and every
// field of it is either the RTDB path's own or a liveness fact.
//
// The two nodes carry more than this -- AgentEntry names a repo, a title, a
// prUrl and a requestedBy, and Outcome names a requestedBy -- and none of it is
// read: both nodes are written by the run's own service account, so a title or
// a pull request taken from there would be a value the run chose about itself
// (F9, D3202). The run id and the slug are the path's own and not the node's
// contents, which is why they may come from it.
type overlayRow struct {
	Slug  string
	RunID string
	// Status is the status the overlay can account for: the settled one an
	// outcome node carries, or runstore.StatusRunning for a run the live
	// registry says is in flight -- which is the word the run's own record
	// carries while it goes, so a row built from the registry reads as the row
	// the record would have given. It is empty when the overlay knows of the
	// run and can settle nothing about it, which is a stale registry entry the
	// crash sweep has not reaped yet.
	Status runstore.Status
	// Live is whether the run is in flight, as its own registry entry says: a
	// node under agents/ whose heartbeat is fresh (budget.DefaultStaleAfter,
	// the bound the crash sweep reaps a dead run's node by).
	Live bool
	// At is the latest moment the overlay can account for the run having
	// changed: the registry entry's own heartbeat where there is one, else the
	// start of the UTC day the outcome landed under, and never before the
	// index's fold stamp -- the index carries every run that changed before
	// that stamp, so a run it does not carry changed after it. It is a floor
	// and not a finish time: the overlay is what fills the gap between two
	// folds, not a second history.
	//
	// It is the row's sort key only where the index does not carry the run:
	// where it does, the entry's own sort key places the row, because the
	// overlay supplies a status and a liveness and never a position (D3202).
	At time.Time
}

// entry is the row an overlay-only run is listed with: the run id and the slug
// the RTDB path names, the status the overlay can account for, and the sort key
// above. No title, no pull request, no identity, no cost, no records -- the
// overlay has nothing else to show, and inventing one would be worse than
// showing less (contract-index.md).
func (o overlayRow) entry() Entry {
	return Entry{RunID: o.RunID, Slug: o.Slug, Status: o.Status, UpdatedAt: o.At}
}

// active reports whether the overlay says the run has not ended. A fresh
// registry entry is a run in flight; an outcome is a run that ended, and the
// run's own node is gone by then (budget.Session.Finish deletes it).
func (o overlayRow) active() bool { return o.Live }

// readOverlay reads the RTDB's live set and the outcome days at or after the
// index's fold stamp, and merges them by run id -- the key the index is keyed
// by, and the only join the two stores have (D3202).
//
// The read is bounded by the fold's own freshness and never by history: one
// request for the live set and one per UTC day from the stamp to today, at most
// budget.PruneAfter of them, because the rollover deletes an outcome day older
// than that and a stamp older than it sets the index incomplete (RI4, RI5).
//
// A node key that budget.Key did not write is an error rather than a row to
// skip: it is a node nothing in this tree wrote, and reading past it would drop
// a run from the page without saying so.
func readOverlay(ctx context.Context, db *rtdb.Client, foldedThrough *time.Time, now time.Time) (map[string]overlayRow, error) {
	if db == nil {
		return nil, errors.New("runindex: the overlay needs a Realtime Database client")
	}
	now = now.UTC()
	var live map[string]map[string]budget.AgentEntry
	if _, err := db.Get(ctx, agentsRoot, &live); err != nil {
		return nil, fmt.Errorf("runindex: reading the live set: %w", err)
	}
	out := make(map[string]overlayRow, len(live))
	for slugKey, runs := range live {
		slug, err := budget.Unkey(slugKey)
		if err != nil {
			return nil, fmt.Errorf("runindex: the live set holds a slug key this tree did not write: %w", err)
		}
		for runKey, e := range runs {
			run, err := budget.Unkey(runKey)
			if err != nil {
				return nil, fmt.Errorf("runindex: the live set of %s holds a run key this tree did not write: %w", slug, err)
			}
			at := heartbeat(e)
			if at.IsZero() {
				// Neither a heartbeat nor a start: the entry says nothing about
				// when the run changed, so it is a row no page can place and a
				// status no read may claim. The run's own records are where it
				// is listed.
				continue
			}
			row := overlayRow{Slug: slug, RunID: run, At: at}
			if row.Live = alive(e, now); row.Live {
				row.Status = runstore.StatusRunning
			}
			out[run] = row
		}
	}
	for _, day := range overlayDays(foldedThrough, now) {
		var days map[string]map[string]budget.Outcome
		found, err := db.Get(ctx, outcomesRoot+"/"+budget.DayKey(day), &days)
		if err != nil {
			return nil, fmt.Errorf("runindex: reading the outcomes of %s: %w", budget.DayDate(day), err)
		}
		if !found {
			continue
		}
		for slugKey, runs := range days {
			slug, err := budget.Unkey(slugKey)
			if err != nil {
				return nil, fmt.Errorf("runindex: the outcomes of %s hold a slug key this tree did not write: %w", budget.DayDate(day), err)
			}
			for runKey, o := range runs {
				run, err := budget.Unkey(runKey)
				if err != nil {
					return nil, fmt.Errorf("runindex: the outcomes of %s under %s hold a run key this tree did not write: %w", budget.DayDate(day), slug, err)
				}
				row := out[run]
				if row.RunID == "" {
					row.Slug, row.RunID = slug, run
				}
				// The outcome is the run's ending, so it settles the row: it
				// wins over a registry entry that is still there, which a run
				// deletes as it finishes and a failed delete can leave behind.
				if s := outcomeStatus(o.Status); s != "" {
					row.Status, row.Live = s, false
				}
				row.At = later(row.At, dayStart(day))
				out[run] = row
			}
		}
	}
	// The stamp is the floor under every row's sort key: the index carries
	// every run that changed before it, so a run the overlay knows and the
	// index does not changed after it. It is also what keeps a run that
	// finished minutes ago above the rows the fold has already read, which is
	// the whole reason the overlay is read at all (F9).
	for id, row := range out {
		if foldedThrough != nil {
			row.At = later(row.At, *foldedThrough)
		}
		out[id] = row
	}
	return out, nil
}

// overlayDays is the UTC days the overlay reads: every day from the index's
// fold stamp to today, and today alone when there is no stamp. An index that
// has never folded says so (Summary.Complete is false) and every reader prints
// that it is still building; its history is the fold's to read, not the
// overlay's, whose own bound is the last minutes of activity (F9).
func overlayDays(foldedThrough *time.Time, now time.Time) []int64 {
	today := budget.Day(now)
	first := today
	if foldedThrough != nil {
		if d := budget.Day(*foldedThrough); d < first {
			first = d
		}
	}
	if today-first > budget.PruneAfter {
		first = today - budget.PruneAfter
	}
	days := make([]int64, 0, today-first+1)
	for d := first; d <= today; d++ {
		days = append(days, d)
	}
	return days
}

// dayStart is the first instant of a UTC epoch day.
func dayStart(day int64) time.Time { return time.UnixMilli(day * 86_400_000).UTC() }

// heartbeat is the moment a registry entry last reported itself, from its
// heartbeat where it has one and its start otherwise, as the crash sweep reads
// the same two fields.
func heartbeat(e budget.AgentEntry) time.Time {
	hb := e.UpdatedAt
	if hb == 0 {
		hb = e.StartedAt
	}
	if hb <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(hb).UTC()
}

// alive reports whether a registry entry says its run is in flight. Everything
// in the entry is written by the run's own service account, so the one thing
// that can keep it here is a fresh heartbeat: budget.DefaultStaleAfter is the
// bound the crash sweep reaps a dead run's node by, and the skew above is the
// one that sweep allows.
func alive(e budget.AgentEntry, now time.Time) bool {
	at := heartbeat(e)
	if at.IsZero() {
		return false
	}
	age := now.Sub(at)
	return age < budget.DefaultStaleAfter && age > -overlayFutureSkew
}

// outcomeStatus is the settled status an outcome node carries, or "" for a word
// that settles nothing: "none" is a run that recorded no outcome at all, and
// the empty status is a node nothing wrote a status into. The vocabulary is
// runstore's own, less `running` -- the rules refuse that word in an outcome,
// because a run writes its outcome as it ends.
func outcomeStatus(s string) runstore.Status {
	if s == "" || s == "none" {
		return ""
	}
	for _, known := range runstore.Statuses {
		if string(known) == s && known.Terminal() {
			return known
		}
	}
	return ""
}

// later is the later of two moments, treating the zero time as "no moment at
// all": an overlay row is placed by the newest thing the overlay can account
// for, and a row it can account for nothing about is not placed at all.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
