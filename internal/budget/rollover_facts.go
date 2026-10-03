package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// BucketFacts reads RunFacts from the runs bucket's result.json files (plan
// H6): compute_usd counts only when compute_estimated, and hours are
// started_at to finished_at. A run with no result.json yet adds nothing; one
// whose record cannot be read (corrupt or oversized, written by the run's own
// account) is skipped with a warning; any other read error fails the pass,
// because a day must never be written from a bucket that could not be read.
type BucketFacts struct {
	Bucket *blob.Bucket
	Warn   func(string)
}

func (b BucketFacts) warnf(format string, a ...any) {
	if b.Warn != nil {
		b.Warn(fmt.Sprintf(format, a...))
	}
}

// Facts lists, for each day of [from, to], the runs started on it.
func (b BucketFacts) Facts(ctx context.Context, from, to int64) (map[int64][]RunFact, map[string]string, error) {
	out := map[int64][]RunFact{}
	repos := map[string]string{}
	slugs, err := runstore.ListSlugs(ctx, b.Bucket)
	if err != nil {
		return nil, nil, err
	}
	// A run ID is minted a little before the run starts: look a day earlier.
	since := time.UnixMilli((from - 1) * 86_400_000).UTC()
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
			rec, err := runstore.Open(b.Bucket, slug, id).ReadRecord(ctx)
			switch {
			case errors.Is(err, runstore.ErrNotFound):
				continue
			case errors.Is(err, runstore.ErrTooLarge) || (err != nil && strings.HasPrefix(err.Error(), "decoding result.json")):
				b.warnf("skipping %s/%s: its result.json cannot be used (%v)", slug, id, err)
				continue
			case err != nil:
				return nil, nil, err
			}
			start := rec.StartedAt
			if start.IsZero() {
				start = minted
			}
			day := Day(start)
			if day < from || day > to {
				continue
			}
			f := RunFact{Slug: slug, Run: id, StartDay: day}
			if c := rec.Cost; c != nil && c.ComputeEstimated {
				f.ComputeUSD, f.ComputeEstimated = c.ComputeUSD, true
			}
			if rec.FinishedAt != nil && rec.FinishedAt.After(start) {
				f.Hours = rec.FinishedAt.Sub(start).Hours()
			}
			out[day] = append(out[day], f)
			if _, ok := repos[slug]; !ok && rec.Repo != "" {
				repos[slug] = rec.Repo
			}
		}
	}
	return out, repos, nil
}
