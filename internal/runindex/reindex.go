package runindex

import (
	"context"
	"errors"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// reindex.go is RI5: the walk that builds the index from the runs bucket, and
// the two ways it is reached.
//
// The fold (RI4) is incremental and reaches back only as far as its discovery
// can see -- a week of outcomes and the runs that are alive -- so an
// installation whose index is empty of everything older, or whose stamp has
// fallen further behind than the days the database keeps, is one only a walk
// over the whole bucket repairs. `fugaro update` starts it once after its
// apply and `fugaro runs reindex` starts it again (U3); the job's own
// --reindex mode is the pass.
//
// The walk is resumable rather than long (F39). A pass upserts the runs it
// reaches and leaves its position in the lease, so a sweep that finds an
// unfinished cursor carries on from there: the job starts no execution of
// itself (F9), no timeout is overridden, and a pass that dies costs the work
// of one pass rather than the whole walk.
//
// Each pass is one writer, under the same lease the fold takes, because the
// summary is a sum over the entries: two passes folding one run would each add
// its own delta, and a figure a reader cannot tell from a right one is worse
// than a slow one.

// WalkReport is what one reindex pass did.
type WalkReport struct {
	// Held is true when another writer's claim was fresh: nothing was read and
	// nothing was written, and Holder names who holds it.
	Held   bool
	Holder string
	// From is the position the pass resumed from, and nil for a pass that began
	// at the top of history.
	From *Cursor
	// Walked is how many runs the pass upserted, and Unreadable how many of the
	// runs it reached carried nothing to write (a run whose objects are gone,
	// which is a warning rather than a failure).
	Walked     int
	Unreadable int
	// Done is true when the pass reached the end of the bucket: the summary is
	// then recomputed from the index documents and marked complete (D3215, F13).
	Done bool
	// Cursor is where the pass stopped, when it did not reach the end: the
	// position the next pass continues from, written into the lease.
	Cursor *Cursor
	// Through is the stamp the summary carries after the pass: the moment the
	// walk began -- not the moment this pass ran -- so the next fold reads every
	// run that changed while the walk was going.
	Through time.Time
	// Restarted is true when the pass found a cursor it could not read and began
	// again from the top of history, which it says rather than hides
	// (DecodeCursor fails closed for exactly this caller).
	Restarted bool
	// Estimate says whether the summary's estimate table was rebuilt.
	Estimate bool
}

// SweepReport is what one sweep did to the index (RI5): the fold it ran, or
// the reindex walk it continued.
type SweepReport struct {
	FoldReport
	// Walk is set when this pass walked a reindex rather than folding. The
	// embedded fold report is zero then -- Folded, Unreadable and Through are
	// the walk's, in here.
	Walk *WalkReport
}

// Sweep is what the history job's sweep does to the index: it folds what
// changed, or continues an unfinished reindex walk, whichever the index is
// owed.
//
// A walk is owed while the summary says the index is not complete (D3215) or
// while the lease carries a cursor (F39), and a fresh claim by another writer
// decides both: while a reindex holds the lease, every other sweep prints who
// holds it and skips the index, and its own work -- the crash sweep, the push
// queue -- is untouched (D12).
func (f *Fold) Sweep(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	if f.DB == nil || f.FS == nil || f.Bucket == nil {
		return rep, errors.New("runindex: the sweep needs the budget database, a Firestore client and the runs bucket")
	}
	cur, found, err := f.lease(ctx)
	if err != nil {
		return rep, err
	}
	srv, ok := f.DB.ServerNow()
	if !ok {
		return rep, errors.New("runindex: the database's clock was not seen (no Date header), so a claim cannot be judged fresh or lapsed; nothing was folded")
	}
	if found && cur.ExpiresAt.After(srv) {
		rep.Held, rep.Holder = true, cur.Holder
		return rep, nil
	}
	if cur.Cursor != "" {
		w, err := f.Walk(ctx)
		rep.Walk = &w
		return rep, err
	}
	sum, found, err := summaryDoc(ctx, f.FS)
	if err != nil {
		return rep, err
	}
	if found && !sum.Complete {
		w, err := f.Walk(ctx)
		rep.Walk = &w
		return rep, err
	}
	fr, err := f.Fold(ctx)
	rep.FoldReport = fr
	return rep, err
}

// Walk runs (or continues) the reindex: every run of the bucket, upserted into
// the index by run id, under the writer lease. It is the job's --reindex mode
// and what `fugaro runs reindex` starts (RI5).
//
// The stamp is the walk's own start and never the pass's, so a fold that runs
// after the walk reads every run that changed while it was going -- the runs a
// first pass upserted before their own records moved included, which a stamp
// taken at the last pass would step over and lose. A pass that does not reach
// the end leaves its cursor in the lease and the walk to the next sweep; a
// pass that does recomputes the summary from the documents and sets complete.
//
// A pass that fails leaves that same position behind rather than freeing the
// node, which is the difference between a walk of a million runs that survives
// a refused write and one that starts again from the top of history every time
// (F39).
func (f *Fold) Walk(ctx context.Context) (WalkReport, error) {
	var rep WalkReport
	if f.DB == nil || f.FS == nil || f.Bucket == nil {
		return rep, errors.New("runindex: the reindex needs the budget database, a Firestore client and the runs bucket")
	}
	lease, err := NewLease(f.DB, f.Holder, f.DB.ServerNow)
	if err != nil {
		return rep, err
	}
	claim, held, err := lease.Acquire(ctx)
	if err != nil {
		return rep, err
	}
	if !held {
		rep.Held, rep.Holder = true, claim.Holder
		return rep, nil
	}
	// A pass that stops leaves the node in place with its cursor and a lapsed
	// expiry (Lease.Stop), so the release must not then delete it: the walk
	// would lose the position it just saved.
	var stopped bool
	defer func() {
		if stopped {
			return
		}
		if err := lease.Release(ctx, claim.At); err != nil {
			f.warnf("the index lease could not be released: %v", err)
		}
	}()

	var cursor *Cursor
	if claim.Cursor != "" {
		c, err := DecodeCursor(claim.Cursor)
		if err != nil {
			rep.Restarted = true
			f.warnf("the reindex cursor could not be read (%v): the walk begins again from the top of history", err)
		} else {
			cursor = &c
		}
	}
	rep.From = cursor

	sum, _, err := summaryDoc(ctx, f.FS)
	if err != nil {
		return rep, err
	}
	start := f.now()
	if cursor != nil && sum.FoldedThrough != nil {
		// The walk the cursor belongs to has already said where it began: this
		// pass reads the same window, and only the pass that begins a walk
		// moves the stamp.
		start = sum.FoldedThrough.UTC()
	}
	if cursor == nil {
		if err := f.beginWalk(ctx, start); err != nil {
			return rep, err
		}
	}
	rep.Through = start

	last, done, err := f.walk(ctx, cursor, &rep)
	if err != nil {
		// A pass that failed mid-walk must not take the walk's position with it:
		// the position is the lease node's, and a release deletes the node. What
		// is saved is the last run this pass upserted -- everything up to it is
		// in the index and everything after it is not -- or, when the pass
		// reached no run at all, the position it inherited. Only a pass that has
		// nothing to save at all falls through to the release, which is the node
		// it took.
		pos := last
		if pos == nil {
			pos = cursor
		}
		if pos != nil {
			stopped = true
			rep.Cursor = pos
			if serr := lease.Stop(ctx, claim.At, pos.Encode()); serr != nil {
				f.warnf("the walk's position could not be left with the lease, so the next pass begins at the top of history: %v", serr)
			}
		}
		return rep, err
	}
	if !done {
		// last is nil only for a bucket with no run at all, which the pass then
		// reached the end of: an unfinished walk always has a position.
		if last == nil {
			return rep, errors.New("runindex: the walk stopped before it reached a run, so it has no position to resume from")
		}
		rep.Cursor = last
		stopped = true
		if err := lease.Stop(ctx, claim.At, last.Encode()); err != nil {
			return rep, err
		}
		return rep, nil
	}
	rep.Done = true
	if err := f.complete(ctx, start, sum, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// walk is the pass itself: the bucket's repositories in order, their runs
// newest first, and the cursor's own position skipped. It answers the position
// it ended on, and whether it reached the end of the bucket.
//
// The position comes back on a failure too, and it is the last run the pass
// upserted: the run that failed, and every run after it, is not in the index,
// so a caller that saved the failing run's position would step over it for
// ever. A failure before the first write answers nothing at all, which is what
// tells the caller to keep the position it inherited (Walk).
func (f *Fold) walk(ctx context.Context, cursor *Cursor, rep *WalkReport) (*Cursor, bool, error) {
	slugs, err := runstore.ListSlugs(ctx, f.Bucket)
	if err != nil {
		return nil, false, err
	}
	var last *Cursor
	for _, slug := range slugs {
		if cursor != nil && slug < cursor.Slug {
			continue // a repository the walk has already passed
		}
		ids, err := runstore.ListRunIDs(ctx, f.Bucket, slug, time.Time{})
		if err != nil {
			return last, false, err
		}
		for _, id := range ids {
			if cursor != nil && slug == cursor.Slug && id >= cursor.RunID {
				continue // the runs the walk has already upserted
			}
			entry, err := f.read(ctx, runKey{slug: slug, run: id})
			if err != nil {
				return last, false, err
			}
			switch {
			case entry == nil:
				rep.Unreadable++
			default:
				if err := f.write(ctx, entry); err != nil {
					return last, false, err
				}
				rep.Walked++
			}
			last = &Cursor{Slug: slug, RunID: id}
			// The bound is checked after the run and not before it, so a pass
			// that stops leaves a position to resume from even when it was cut
			// off at its first check: an unfinished walk with no cursor would
			// be a walk the next sweep folds over instead of continuing.
			if f.pastLimit() {
				return last, false, nil
			}
		}
	}
	return last, true, nil
}

// beginWalk latches the index as a walk's: the summary carries the walk's start
// as its stamp and is not complete, which is what tells every later sweep that
// a walk is owed (D3215, F39).
//
// It is written at the FIRST pass and before anything is walked, so a pass that
// dies -- killed at its timeout, a container that goes away -- leaves the fact
// where the next sweep finds it, even when it left no cursor at all.
func (f *Fold) beginWalk(ctx context.Context, start time.Time) error {
	_, err := f.FS.Patch(ctx, SummaryCollection, SummaryDocument, map[string]any{
		"version":        int64(Version),
		"complete":       false,
		"folded_through": start,
		"updated_at":     f.now(),
	})
	return err
}

// complete recomputes the summary from the index documents and writes it whole:
// the walk reached the end of the bucket, so every run it holds is in the index
// and the counts are a sum over what is there rather than a running total any
// pass could have got wrong (F13, D3215).
func (f *Fold) complete(ctx context.Context, start time.Time, was Summary, rep *WalkReport) error {
	sum, err := f.recompute(ctx)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"version":               int64(Version),
		"complete":              true,
		"folded_through":        start,
		"updated_at":            f.now(),
		"runs":                  sum.Runs,
		"states":                sum.States,
		"spent_micros":          sum.SpentMicros,
		"notional_micros":       sum.NotionalMicros,
		"compute_runs":          sum.ComputeRuns,
		"compute_not_estimated": sum.ComputeNotEstimated,
	}
	var clear []string
	if sum.ComputeMicros != nil {
		fields["compute_micros"] = *sum.ComputeMicros
	} else {
		clear = append(clear, "compute_micros")
	}
	if stale(was.Estimate, f.now()) {
		table, err := f.estimate(ctx, f.now())
		if err != nil {
			// The estimate table is a distribution that moves slowly; the counts
			// and the completeness are what a reader of the summary needs, and a
			// walk that folded a million runs must not be failed over a table.
			f.warnf("the estimate table could not be rebuilt: %v (the summary keeps the one it has)", err)
		} else {
			fields["estimate"] = table
			rep.Estimate = true
		}
	}
	opts := []firestore.PatchOption{}
	if len(clear) > 0 {
		opts = append(opts, firestore.Clear(clear...))
	}
	encoded, err := docFields(fields)
	if err != nil {
		return err
	}
	_, err = f.FS.Patch(ctx, SummaryCollection, SummaryDocument, encoded, opts...)
	return err
}

// recompute sums the summary over the index document: one read of every entry
// the index holds, in pages whose count is bounded by the index's own size and
// never by the walk's.
func (f *Fold) recompute(ctx context.Context) (Summary, error) {
	sum := Summary{Version: Version, States: map[string]int64{}}
	for _, s := range runstore.Statuses {
		sum.States[string(s)] = 0
	}
	var (
		compute int64
		last    *firestore.Doc
	)
	for {
		docs, err := f.FS.RunQuery(ctx, firestore.Query{
			Collection: EntriesCollection,
			Limit:      pageSize,
			StartAfter: last,
		})
		if err != nil {
			return Summary{}, err
		}
		for i := range docs {
			e, err := decodeEntry(docs[i])
			if err != nil {
				return Summary{}, err
			}
			sum.Runs++
			if e.Status != "" {
				sum.States[string(e.Status)]++
			}
			g := figures(e.Record)
			sum.SpentMicros += g.spent
			sum.NotionalMicros += g.notional
			sum.ComputeRuns += g.computeRuns
			sum.ComputeNotEstimated += g.notEstimated
			compute += g.compute
		}
		if len(docs) < pageSize {
			break
		}
		last = &docs[len(docs)-1]
	}
	if sum.ComputeRuns > 0 {
		sum.ComputeMicros = &compute
	}
	return sum, nil
}

// pastLimit reports whether a pass has run past the moment it may start work
// until (Fold.StartLimit): the job's own timeout, minus what is left to write
// the position the next pass resumes from.
func (f *Fold) pastLimit() bool {
	return !f.StartLimit.IsZero() && !f.now().Before(f.StartLimit)
}

// lease reads the lease node as it stands, without claiming it: what a sweep
// asks before it decides to fold, walk or do neither.
func (f *Fold) lease(ctx context.Context) (LeaseDoc, bool, error) {
	var cur LeaseDoc
	found, err := f.DB.Get(ctx, PathLease, &cur)
	if err != nil {
		return LeaseDoc{}, false, err
	}
	return cur, found, nil
}

// ResetReindex puts the index back to "a walk is owed, from the top": the
// cursor is gone from the lease node and the summary carries complete: false
// (D3215, F39), so every later sweep walks until one reaches the end and the
// walk begins at the top of history rather than resuming a position an
// operator has decided against (RI5, U3).
//
// It is the first half of both `fugaro update`'s step and `fugaro runs reindex`,
// and it is what makes them work when the caller may not start the job at all
// (O7): the walk the reset asks for is one every sweep of the history job
// continues on its own, with no grant and no execution started.
//
// The mark is written whether or not a writer holds the lease, and live is true
// only for a walk that is already going: the claim carries an unfinished walk's
// cursor, or the summary was incomplete before this reset. A fresh claim on its
// own is not a walk -- the sweep's own fold holds this lease for as long as a
// fold takes, and a claim a dead fold left stands for the lease's whole TTL --
// and a rebuild asked for must not be turned away by one: the caller starts the
// execution, which finds the claim in its way and leaves the walk to the next
// sweep, and the mark is what makes that sweep walk.
//
// A summary that is not there at all is seeded: an installation whose index has
// never been folded has no `complete` field to latch, and the reset is exactly
// the command that says an index should exist, so the document it asks for is
// written here rather than left for the next fold (which would fold the week
// its discovery can see and call that the whole of history).
func ResetReindex(ctx context.Context, db *rtdb.Client, st Store, holder string, now time.Time) (live bool, err error) {
	if db == nil || st == nil {
		return false, errors.New("runindex: a reindex reset needs the budget database and a Firestore client")
	}
	// What the summary said before the mark, which is half of what tells a walk
	// in flight from a fold: a walk that is going has already latched the index
	// incomplete (Walk.beginWalk).
	sum, found, err := summaryDoc(ctx, st)
	if err != nil {
		return false, err
	}
	owed := !found || !sum.Complete
	lease, err := NewLease(db, holder, db.ServerNow)
	if err != nil {
		return false, err
	}
	claim, held, err := lease.Reset(ctx)
	if err != nil {
		return false, err
	}
	if err := MarkIncomplete(ctx, st, now); err != nil {
		return false, err
	}
	if !found {
		if _, err := st.Patch(ctx, SummaryCollection, SummaryDocument, map[string]any{
			"version": int64(Version), "complete": false, "updated_at": now.UTC(),
		}); err != nil {
			return false, err
		}
	}
	if !held {
		return false, nil
	}
	return claim.Cursor != "" || owed, nil
}

// WalkOwed reports whether the index is owed a walk: the summary says it is not
// complete -- or is not there at all -- or the lease carries the cursor of an
// unfinished walk (F39, D3215).
//
// It is what `fugaro update` asks before it starts the first reindex (RI5), so
// an installation whose index is built and whose stamp is current is left
// exactly as it is rather than walked over again: the walk reads every run in
// the bucket, and an upgrade must not cost that on an installation that has
// nothing to repair. `fugaro runs reindex` does not ask: a person who runs the
// command wants the walk.
func WalkOwed(ctx context.Context, db *rtdb.Client, st Store) (bool, error) {
	if db == nil || st == nil {
		return false, errors.New("runindex: whether a walk is owed needs the budget database and a Firestore client")
	}
	var cur LeaseDoc
	found, err := db.Get(ctx, PathLease, &cur)
	if err != nil {
		return false, err
	}
	if found && cur.Cursor != "" {
		return true, nil
	}
	sum, found, err := summaryDoc(ctx, st)
	if err != nil {
		return false, err
	}
	return !found || !sum.Complete, nil
}
