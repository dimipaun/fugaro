package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gocloud.dev/blob"

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
}

func (b BucketFacts) warnf(format string, a ...any) {
	if b.Warn != nil {
		b.Warn(fmt.Sprintf(format, a...))
	}
}

// Facts lists, for each day of [from, to], the runs started on it. The run
// records are read factsWorkers at a time; the walk over them is still one
// pass in the order the runs were listed, so a day's facts and the failures
// that reach the caller are what reading them one at a time gave.
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
	// A run ID is minted a little before the run starts: look a day earlier.
	since := time.UnixMilli((from - 1) * 86_400_000).UTC()
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
	recs := make([]*runstore.Record, len(refs))
	errs := make([]error, len(refs))
	readsAtOnce(len(refs), func(i int) {
		recs[i], errs[i] = runstore.Open(b.Bucket, refs[i].slug, refs[i].id).ReadRecord(ctx)
	})
	for i, r := range refs {
		rec, err := recs[i], errs[i]
		switch {
		case errors.Is(err, runstore.ErrNotFound):
			continue
		case errors.Is(err, runstore.ErrTooLarge) || (err != nil && strings.HasPrefix(err.Error(), "decoding result.json")):
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

// readsAtOnce runs fn(0) … fn(n-1) on at most factsWorkers goroutines.
func readsAtOnce(n int, fn func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(factsWorkers, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}
