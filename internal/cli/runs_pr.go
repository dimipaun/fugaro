package cli

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// prIndexRecent is the window a --pr listing reads beside the pull request's
// index: every run of the last day, whatever the index says of them.
//
// The index is written by the runners that open and follow a pull request, so
// it is silent about the runs that ended before it existed and about a
// follow-up launched by a job image older than it; the union is what keeps
// such a run from hiding (F5, D2964). A day is where the two sides meet: the
// scan of one day measured 3 s on 2026-11-19, and a run still going -- the one
// whose absence would change what the listing prints, since a run going is
// what makes its pull request read live -- is minted well inside it.
const prIndexRecent = 24 * time.Hour

// prRefs is the runs a --pr listing reads: the runs the run index names, the
// runs the pull request's own index in the bucket names, the runs of the last
// prIndexRecent, and the runs that are going now.
//
// It answers no refs at all -- and, for a listing, prints one note -- when
// neither index names the pull request, or when the bucket's index could not be
// read and the run index named nothing either: the caller then reads the window
// exactly as it did before an index existed (D2964, F9). A note is the whole of
// the fallback's cost to the reader: it is a slower listing and not a different
// one, and the note never reaches --json's document. The note is the listing's
// own (lsFilter's indexNote): a caller that reads a pull request's runs to
// decide something falls back in silence, since it is the runs and not the line
// that a launch needs.
//
// The refs are a superset of the runs that end up listed, deliberately: every
// one of them is read and then checked against its own records the way the
// window's runs are (D2963), so a run an index names for the wrong reason buys
// a read and never a row. What the indexes are trusted to do is spend the
// reads, and nothing else.
func prRefs(ctx context.Context, env *cloudEnv, f lsFilter, now time.Time, warnf func(string, ...any)) ([]lsRef, error) {
	// A pull request number means something only in one repository, and the
	// callers refuse a number beside several rather than guessing which one it
	// is. Anything that reaches here with another shape reads the window as
	// every listing did.
	if len(f.slugs) != 1 {
		return nil, nil
	}
	slug := f.slugs[0]
	refs := []lsRef{}
	seen := make(map[lsRef]bool, 8)
	add := func(r lsRef) {
		if seen[r] {
			return
		}
		seen[r] = true
		refs = append(refs, r)
	}
	// The run index's own answer, for a listing: it carries every run of the
	// pull request the index has folded, which is what --pr reads (D3204), and
	// the sources below are what still cover the runs it has not -- history
	// folded before this release, and a run the last fold has not reached (the
	// overlay is the reader's, and it is what makes such a run an entry here).
	//
	// A caller that reads a pull request's runs to decide something keeps the
	// read it had and asks no index: a launch is decided from the runs the
	// bucket and the pull request's own index hold (lsFilter.indexNote), and an
	// index that answered with more is not a decision this task changes.
	indexed := false
	if f.indexNote {
		if idx, err := runIndexReader(ctx, env); err == nil && idx != nil {
			if page, err := idx.List(ctx, runindex.Filter{Slugs: []string{slug}, PR: f.pr, Since: f.since}); err == nil && indexAnswers(page) {
				indexed = true
				for _, e := range page.Entries {
					if r := (lsRef{e.Slug, e.RunID}); r.slug != "" && inWindow(r, f.since) {
						add(r)
					}
				}
			}
		}
	}
	// The pull request's own index in the bucket: the read --pr made before
	// the run index existed, and one the two go on sharing until the runners
	// stop writing it (IR5a, IR5b). An entry naming a run the user's window
	// does not hold is left out, the way the window's own listing leaves it
	// out: the index answers for one pull request, --since for the window, and
	// a run is listed only when both hold it.
	entries, err := runstore.ListPRIndex(ctx, env.bucket.Bucket, slug, f.pr, warnf)
	switch {
	case err != nil:
		// A bucket that refused this one listing of it is not a bucket that
		// cannot answer the query: the scan below reads the window and reports
		// what the bucket says of it. A run index that answered has the pull
		// request already, and the note then says only what could not be read:
		// the rows are the index's, and a line promising the window would be a
		// line about a read this listing is not making.
		if f.indexNote {
			if indexed {
				warnf("note: the pull-request index of %s#%d could not be read: %v\n", slug, f.pr, err)
			} else {
				warnf("note: the pull-request index of %s#%d could not be read: %v; reading every run of the window instead\n", slug, f.pr, err)
			}
		}
		if !indexed {
			return nil, nil
		}
	case len(entries) == 0:
		if !indexed {
			if f.indexNote {
				warnf("note: no pull-request index for %s#%d: reading every run of the window, as it did before the index existed\n", slug, f.pr)
			}
			return nil, nil
		}
	default:
		for _, e := range entries {
			if r := (lsRef{e.Slug, e.RunID}); inWindow(r, f.since) {
				add(r)
			}
		}
	}
	// The recent window is the later of the last day and the user's own
	// --since, so the union never reaches back past the window the listing was
	// asked for.
	from := now.Add(-prIndexRecent)
	if !f.since.IsZero() && f.since.After(from) {
		from = f.since
	}
	ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, from)
	if err != nil {
		return nil, remote(err)
	}
	for _, id := range ids {
		add(lsRef{slug, id})
	}
	// A review of the pull request that is going is the one run of it the
	// index cannot name -- a review neither opens nor follows a pull request --
	// and the fold needs it: while a review is going its task is the only thing
	// that places it on the pull request, and a run going is what makes the
	// pull request read live (runs_prstate.go). The live set is what finds the
	// runs going without reading the history (liveRuns); every run it names off
	// this pull request is read and dropped by the same check as the window's
	// own runs, so what it costs is one backend listing and the handful of runs
	// that are going.
	set, err := liveRuns(ctx, env, f, now)
	if err != nil {
		return nil, err
	}
	for _, r := range set.refs {
		add(r)
	}
	return refs, nil
}
