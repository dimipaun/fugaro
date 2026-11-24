package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_summary.go is RL4's backfill step (docs/runs-ls-speed,
// implementation-plan.md): the run summaries an installation that predates them
// is missing.
//
// A summary (runstore.SummaryName, one object under runs/<slug>/<run>/) is
// written by a run's own job at each stage end, so an installation that
// upgrades holds one only for the runs that START after the upgrade, and
// `runs ls` reads every older run of its window from its three objects --
// task.json, launch.json and result.json -- until this step writes them. The
// step is RL2B's shape (update_prindex.go): it walks each repository's runs of
// the last 90 days once, reads each run's own task and record, and creates the
// summary it would have written, create-if-absent.
//
// Which runs already hold one is one listing of the repository, as the index
// step reads its own (indexedRuns): a converged installation reads no object of
// its runs at all, and an installation converges by reading only the runs that
// are missing one. Reading each run's summary instead would cost one read a run
// of the 90-day window on every update, forever.
//
// Create-if-absent and never a rewrite, for the reason the index's step is
// (the HARD RULE, docs/design/update-in-flight.md): a summary a run wrote is
// the run's own account of itself, and a run in flight rewrites its own at
// every stage end, so a step that overwrote one could clobber a run's own
// newer object with a projection of a record it read a moment earlier. The
// create is conditional in the bucket (runstore.CreateSummary), so the run's
// object wins whichever way the two race, every object a run holds is byte for
// byte as it was, and a second update writes nothing.
//
// Nothing here may fail the update: a summary this run could not write is one
// the next `fugaro update` writes, `runs ls` reads the run's three objects
// until then, and a step of the upgrade is never stopped over a derived hint.
func (e *initEngine) backfillRunSummary(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door and the session first, as in backfillPRIndex: this is the
	// upgrade's step, reached by the same hook the doors share (init_stages.go's
	// outcome), and a coding agent's session under a marker changes nothing
	// outside this machine unless the managed gate is the read that stands in
	// for this run's confirmations (D1016).
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	if e.lc == nil {
		return
	}
	if fakeEndpointsOnGS(e.lc, e.lc.BucketURL()) {
		// The config's endpoints are fakes (tests, emulators): the gs:// bucket
		// is not the one they stand for, and opening it would reach the real
		// network with the developer's credentials.
		return
	}
	b, err := sharedBucketOpener(ctx, e.lc.BucketURL())
	if err != nil {
		r.warn(fmt.Sprintf("the run summaries were not backfilled: the runs bucket %s could not be opened (%s). `runs ls` reads the runs' own objects until an update writes them", safetext.Field(e.lc.BucketURL()), safetext.FieldErr(err)))
		return
	}
	defer b.Close()
	now := time.Now().UTC()
	written, err := backfillRunSummariesIn(ctx, b.Bucket, now, func(format string, args ...any) {
		r.warn("the run summaries: " + fmt.Sprintf(format, args...))
	})
	if err != nil {
		r.warn(fmt.Sprintf("the run summaries were not backfilled: %s. `runs ls` reads the runs' own objects until an update writes them", safetext.FieldErr(err)))
		return
	}
	if written == 0 {
		return
	}
	fmt.Fprintf(r.w, "run summaries: %d run(s) of the last %s now carry one, so `runs ls` reads two objects a row instead of three. Nothing a run holds was rewritten and no run was waited for.\n",
		written, prLookback)
}

// backfillRunSummariesIn is the walk itself: every repository of the bucket,
// its runs of the window, and the summary each run whose record is there is
// missing. It answers how many summaries it created, and the one failure that
// leaves the whole walk undone (the bucket could not be listed at all); a
// repository or a run the walk could not reach is a warning through warnf and
// the walk goes on.
func backfillRunSummariesIn(ctx context.Context, b *blob.Bucket, now time.Time, warnf func(string, ...any)) (int, error) {
	slugs, err := runstore.ListSlugs(ctx, b)
	if err != nil {
		return 0, err
	}
	since := now.Add(-followUpLookback)
	written := 0
	for _, slug := range slugs {
		// What the repository already holds, in one listing, before any run of
		// it is read: a converged installation reads nothing below, and one
		// that is missing summaries reads the runs that are missing one and no
		// other. A summary that is there but unusable counts as there: the
		// object is the run's own, and a create-only write would not replace it
		// anyway.
		summarized, err := summarizedRuns(ctx, b, slug)
		if err != nil {
			warnf("the runs of %s were skipped: their summaries could not be listed (%v)", safetext.Field(slug), safetext.FieldErr(err))
			continue
		}
		ids, err := runstore.ListRunIDs(ctx, b, slug, since)
		if err != nil {
			warnf("the runs of %s were skipped: they could not be listed (%v)", safetext.Field(slug), safetext.FieldErr(err))
			continue
		}
		for _, id := range ids {
			if summarized[id] {
				continue
			}
			s := runstore.Open(b, slug, id)
			rec, err := s.ReadRecord(ctx)
			switch {
			case errors.Is(err, runstore.ErrNotFound):
				// A run that has written no record yet -- one in flight, or
				// one that never started -- has nothing for a summary to stand
				// in for, and a run in flight is never waited for.
				continue
			case err != nil:
				warnf("%s was not summarized: its record could not be read (%v)", safetext.Field(slug+"/"+id), safetext.FieldErr(err))
				continue
			}
			// A run whose record still says it is running is left where it is:
			// it writes its own summary at its next stage end, and the walk
			// never creates an object under a run that is going — the HARD
			// RULE's "changes nothing they hold", read as literally as the
			// index step beside it reads it. A run that ended between its
			// record and its own summary keeps no summary and reads from its
			// three objects, which is the fallback a listing already has.
			if rec.Status == runstore.StatusRunning {
				continue
			}
			var spec *task.Spec
			if sp, err := s.ReadTask(ctx); err == nil {
				spec = sp
			}
			// A run whose task could not be read still gets a summary: the
			// task block is then absent, which is the row the three objects
			// give the same run.
			if err := runstore.CreateSummary(ctx, s, runstore.NewSummary(rec, spec, now)); err != nil {
				if errors.Is(err, runstore.ErrExists) {
					continue // the run wrote its own between the listing above and here
				}
				warnf("%s was not summarized: %v", safetext.Field(slug+"/"+id), safetext.FieldErr(err))
				continue
			}
			written++
		}
	}
	return written, nil
}

// summarizedRuns is the runs of one repository that already hold a summary, in
// one flat listing of it (indexedRuns' shape): every object whose base name is
// the summary's is one run's, and the run is the key's parent segment. It lists
// a repository whole rather than the window, because the summary sits inside
// the run's own directory; what bounds it is the bucket's own aging, which
// retires `runs/` at 90 days -- the same window the walk reads.
func summarizedRuns(ctx context.Context, b *blob.Bucket, slug string) (map[string]bool, error) {
	prefix := "runs/" + slug + "/"
	out := map[string]bool{}
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir || path.Base(obj.Key) != runstore.SummaryName {
			continue
		}
		out[path.Base(path.Dir(obj.Key))] = true
	}
	return out, nil
}
