package cli

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/watch"
)

// `fugaro watch` shows queued runs (launched, or about to be, but with no
// registry entry yet) from the same runs bucket ls reads (design
// docs/design/watch-queued.md): never the backend, and never a new object,
// so a person with only the Firebase Viewer role sometimes can and sometimes
// can't read them, depending on whether they also hold a launcher's or
// operator's grant on the bucket (gcp-setup.md "the runs bucket's Viewer
// access"). Either way watch degrades to a one-line note instead of failing.

// queuedPollInterval is how often the live screen re-scans the runs bucket
// for queued runs: a GCS list per repository, much slower than the RTDB
// ticks that drive the rest of the view. A var so tests can shrink it.
var queuedPollInterval = 15 * time.Second

// queuedLookback bounds how far back watch looks for a queued run: well
// past runstore.ClaimTTL, since a claim or launch.json older than that has
// already become unlaunched (claim only) or lost (launch.json), so scanning
// further back would only cost more GCS list calls for nothing.
const queuedLookback = 30 * time.Minute

// openQueueBucket opens just the runs bucket, never the compute backend:
// queued-run detection only ever reads task.json, launch.json, result.json
// and the launch claim, the same objects fugaro ls reads, so it still works
// for a viewer who can read the bucket but holds no Cloud Run role.
func openQueueBucket(ctx context.Context, lc *localcfg.Config) (*cloudEnv, error) {
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return nil, remote(err)
	}
	if err := checkCloudName(ctx, b, lc, os.Getenv, time.Now()); err != nil {
		_ = b.Close()
		return nil, err
	}
	return &cloudEnv{lc: lc, bucket: b}, nil
}

// noComputePrices is runview.Join's price book for queued-run detection,
// which never reads an execution (so no compute cost is ever priced): the
// cost in the row it builds is discarded.
func noComputePrices(string) backend.Prices { return backend.Prices{} }

// launchedAt is when a run entered its claimed or launched state: the
// claim's or launch.json's own timestamp, else the run id's mint time.
func launchedAt(in runview.Input, created time.Time) time.Time {
	switch {
	case in.Launch != nil && !in.Launch.LaunchedAt.IsZero():
		return in.Launch.LaunchedAt
	case in.Claim != nil && !in.Claim.At.IsZero():
		return in.Claim.At
	default:
		return created
	}
}

// queuedRuns reads every run of slugs minted at or after since and keeps the
// ones runview.Join calls launching or pending from the bucket alone (no
// execution is ever looked up, so a run already running with a healthy
// record is never misread as queued: Join falls back to its "running" case
// whenever a record says so). It makes no backend call, so it costs no
// Cloud Run permission.
func queuedRuns(ctx context.Context, env *cloudEnv, slugs []string, since time.Time) ([]watch.QueuedRun, error) {
	var out []watch.QueuedRun
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, since)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			ro, err := readRun(ctx, env, slug, id)
			if err != nil {
				return nil, err
			}
			if ro.in.Problem != "" {
				continue // corrupt object: not shown as queued, same as ls's own warning path
			}
			row := runview.Join(ro.in, noComputePrices, time.Now().UTC())
			if row.Status != runview.StatusLaunching && row.Status != runview.StatusPending {
				continue
			}
			out = append(out, watch.QueuedRun{
				Run: id, Slug: slug, Workflow: row.Workflow, Recipe: row.Recipe, RequestedBy: row.RequestedBy,
				LaunchedAt: launchedAt(ro.in, row.Created),
			})
		}
	}
	return out, nil
}

// queuedNote is err as watch's one-line degrade note.
func queuedNote(err error) string {
	return "queued runs unavailable: " + safetext.Strip(err.Error())
}

// fetchQueued is one synchronous read of the runs bucket for watch's queued
// rows, scoped to repo ("" for every repository lsSlugs would list). note is
// a one-line, already-sanitised reason when the bucket couldn't be read at
// all; rows is nil then.
func fetchQueued(ctx context.Context, lc *localcfg.Config, repo string, since time.Time) (rows []watch.QueuedRun, note string) {
	env, err := openQueueBucket(ctx, lc)
	if err != nil {
		return nil, queuedNote(err)
	}
	defer env.Close()
	slugs, err := lsSlugs(ctx, env, &lsOptions{repo: repo}, io.Discard)
	if err != nil {
		return nil, queuedNote(err)
	}
	rows, err = queuedRuns(ctx, env, slugs, since)
	if err != nil {
		return nil, queuedNote(err)
	}
	return rows, ""
}

// queuedSource polls the runs bucket for queued runs on its own clock and
// caches the latest result for Get, which the render loops call on every
// frame: the bucket read happens only every queuedPollInterval, so a slow or
// unreachable bucket never stalls the RTDB-driven view. A poll that fails
// keeps the last good rows (a hiccup should not blank what was just shown)
// while still surfacing the note.
type queuedSource struct {
	lc   *localcfg.Config
	repo string
	wg   sync.WaitGroup

	mu   sync.Mutex
	rows []watch.QueuedRun
	note string
}

// startQueuedSource does one synchronous read, then keeps refreshing it on
// its own ticker until ctx ends. The caller's Wait, after ctx is cancelled,
// blocks until the goroutine has actually stopped: without it, a caller that
// returns right after cancelling could race a later mutation of
// queuedPollInterval (tests only; cancellation is instant otherwise).
func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string) *queuedSource {
	qs := &queuedSource{lc: lc, repo: repo}
	qs.refresh(ctx)
	qs.wg.Add(1)
	go func() { defer qs.wg.Done(); qs.loop(ctx) }()
	return qs
}

// Wait blocks until the background poll loop has stopped (ctx must already
// be done, or this never returns).
func (qs *queuedSource) Wait() { qs.wg.Wait() }

func (qs *queuedSource) loop(ctx context.Context) {
	t := time.NewTicker(queuedPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			qs.refresh(ctx)
		}
	}
}

func (qs *queuedSource) refresh(ctx context.Context) {
	rows, note := fetchQueued(ctx, qs.lc, qs.repo, time.Now().Add(-queuedLookback))
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if note != "" {
		qs.note = note
		return
	}
	qs.rows, qs.note = rows, ""
}

// Get is the latest queued rows and note, safe to call on every frame: it
// never blocks on the network.
func (qs *queuedSource) Get() ([]watch.QueuedRun, string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.rows, qs.note
}
