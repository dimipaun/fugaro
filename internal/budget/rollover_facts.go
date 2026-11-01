package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/pool"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// factsWorkers is how many runs' result.json are read at once. One read is a
// round trip of tens of milliseconds and a day's pass covers hundreds of runs,
// so this width is most of what the pass costs.
const factsWorkers = 32

// BucketFacts reads RunFacts from the runs bucket's result.json files (plan
// H6): compute_usd counts only when compute_estimated, and hours are
// started_at to finished_at. A run with no result.json yet adds nothing; one
// whose record cannot be read (corrupt or oversized, written by the run's own
// account) is skipped with a warning; any other read error fails the pass,
// because a day must never be written from a bucket that could not be read.
type BucketFacts struct {
	Bucket *blob.Bucket
	Warn   func(string)
	// Slugs, when set, restricts the pass to these repositories: the bucket
	// holds every project's runs, and a caller that wants a handful of facts
	// should not pay for a run listing per repository it will never read.
	// Empty means every repository in the bucket.
	Slugs []string
	// Backend is the compute backend this installation launches runs on, by
	// name (backend.CloudRun, backend.CloudBatch). It decides how far before
	// the window's first day the listing starts, because the gap between a
	// run's mint and its record is that backend's launch latency and not a
	// constant (backend.MintSkewMargin). Empty is a caller that cannot name
	// the backend, which takes the longest bound.
	Backend string
}

func (b BucketFacts) warnf(format string, a ...any) {
	if b.Warn != nil {
		b.Warn(fmt.Sprintf(format, a...))
	}
}

// Facts lists, for each day of [from, to], the runs started on it. The runs
// are listed by the mint time their IDs carry — from the backend's own
// look-back before the window's first day (backend.MintSkewMargin), since a run
// minted before the window can be one that started inside it — and each is
// counted on the day its record's started_at names, which for a run minted
// inside the window can be a day outside it. The
// run records are read factsWorkers at a time; the walk over them is still one
// pass in the order the runs were listed, so a day's facts and the failures
// that reach the caller are what reading them one at a time gave, with these
// exceptions, both of them about a pass that stops early rather than about
// what it returns:
//
//   - Every repository is listed before any record is read, so a repository
//     whose listing fails is reported ahead of an earlier repository's record
//     that failed to read.
//   - A record read the bucket refused ends the pass, so the reads it had in
//     flight are cancelled rather than waited out and the later ones are never
//     made. The failure reported is the one that ended the pass: a read that
//     was cancelled has no failure of its own to report, where reading them
//     one at a time would have given it one.
func (b BucketFacts) Facts(ctx context.Context, from, to int64) (map[int64][]RunFact, map[string]string, error) {
	out := map[int64][]RunFact{}
	repos := map[string]string{}
	slugs, err := runstore.ListSlugs(ctx, b.Bucket)
	if err != nil {
		return nil, nil, err
	}
	if len(b.Slugs) > 0 {
		want := make(map[string]struct{}, len(b.Slugs))
		for _, s := range b.Slugs {
			want[s] = struct{}{}
		}
		keep := make([]string, 0, len(b.Slugs))
		for _, slug := range slugs {
			if _, ok := want[slug]; ok {
				keep = append(keep, slug)
			}
		}
		slugs = keep
	}
	// A run ID is minted before the run starts, by however long the backend
	// takes to start it: look back by that gap and no further
	// (backend.MintSkewMargin).
	since := time.UnixMilli(from*86_400_000 - int64(backend.MintSkewMargin(b.Backend)/time.Millisecond)).UTC()
	type ref struct {
		slug, id string
		minted   time.Time
	}
	var refs []ref
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, since)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range ids { // newest first: the newest record names the repository
			minted, err := runstore.RunTime(id)
			if err != nil || Day(minted) > to {
				continue
			}
			refs = append(refs, ref{slug: slug, id: id, minted: minted})
		}
	}
	// Every record is read before any of them is used, and each goroutine
	// writes only its own index, so the pass below is the same walk whichever
	// read finishes first.
	//
	// A read that fails the pass ends it: the reads in flight and the ones
	// still to come are cancelled rather than waited out, so a bucket that
	// refused a read does not cost the rest of the window's round trips
	// before the caller hears about it.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	recs := make([]*runstore.Record, len(refs))
	errs := make([]error, len(refs))
	pool.Run(len(refs), factsWorkers, func(i int) {
		recs[i], errs[i] = runstore.Open(b.Bucket, refs[i].slug, refs[i].id).ReadRecord(runCtx)
		switch {
		case errs[i] == nil:
		case errors.Is(errs[i], context.Canceled) && ctx.Err() == nil:
			// This read was stopped by the pass's own cancel, not by the
			// bucket: the failure that cancelled it is the one reported, and
			// this read's slot stays as empty as one never made.
			recs[i], errs[i] = nil, nil
		case fatalRead(errs[i]):
			stop()
		}
	})
	for i, r := range refs {
		rec, err := recs[i], errs[i]
		switch {
		case err == nil && rec == nil:
			continue // cancelled: the read that ended the pass is the failure reported, and this slot holds nothing
		case errors.Is(err, runstore.ErrNotFound):
			continue
		case err != nil && !fatalRead(err):
			b.warnf("skipping %s/%s: its result.json cannot be used (%v)", r.slug, r.id, err)
			continue
		case err != nil:
			return nil, nil, err
		}
		start := rec.StartedAt
		if start.IsZero() {
			start = r.minted
		}
		day := Day(start)
		if day < from || day > to {
			continue
		}
		f := RunFact{Slug: r.slug, Run: r.id, StartDay: day}
		if c := rec.Cost; c != nil && c.ComputeEstimated {
			f.ComputeUSD, f.ComputeEstimated = c.ComputeUSD, true
		}
		if rec.FinishedAt != nil && rec.FinishedAt.After(start) {
			f.Hours = rec.FinishedAt.Sub(start).Hours()
		}
		out[day] = append(out[day], f)
		if _, ok := repos[r.slug]; !ok && rec.Repo != "" {
			repos[r.slug] = rec.Repo
		}
	}
	return out, repos, nil
}

// fatalRead reports whether a failed result.json read must fail the whole
// pass: a run with no result.json yet adds nothing, and a record nobody can
// use (corrupt, or past the read cap) is one run's problem and is warned
// about. Anything else — the bucket refused the read — is the day's.
func fatalRead(err error) bool {
	return err != nil && !errors.Is(err, runstore.ErrNotFound) &&
		!errors.Is(err, runstore.ErrTooLarge) && !strings.HasPrefix(err.Error(), "decoding result.json")
}
