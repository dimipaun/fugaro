package cli

import (
	"context"
	"errors"
	"fmt"
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
// docs/design/watch-queued.md): never the backend, and never a new object.
// Only someone who can read the runs bucket (a launcher or an operator)
// sees them; a plain Firebase Viewer always gets the one-line "queued runs
// unavailable" note instead (gcp-setup.md "the runs bucket's Viewer
// access"). Every bucket read happens off the render path, in the
// background, bounded by queuedScanTimeout.

// queuedPollInterval is how often the live screen re-scans the runs bucket
// for queued runs: a GCS list per repository, much slower than the RTDB
// ticks that drive the rest of the view. A var so tests can shrink it.
var queuedPollInterval = 15 * time.Second

// queuedOnceWait is how long watch --once waits for the first scan before
// printing without queued rows (and with a note). A var so tests can
// shrink it.
var queuedOnceWait = 3 * time.Second

// queuedLookback bounds which runs a scan considers: those whose launch
// claim or launch.json was written within it (by the object's own time, so
// a --retry of an old run ID counts). A launch older than
// runstore.ClaimTTL that never got a record stays listed as stuck until it
// leaves this window.
const queuedLookback = 30 * time.Minute

// queuedScanTimeout bounds one whole scan (marker check, listings, reads):
// a bucket that hangs costs a note, never a frozen screen.
const queuedScanTimeout = 10 * time.Second

// queuedDropAfter is how many scans in a row may fail before the last
// good queued rows are dropped: one or two failures keep them (a hiccup
// should not blank the screen), each with the note saying they are from
// an earlier read; a bucket that stays unreadable shows the note only.
const queuedDropAfter = 3

// openQueueBucket opens just the runs bucket, never the compute backend:
// queued-run detection only ever reads task.json, launch.json, result.json,
// the launch claim and the cancel marker, the same objects fugaro ls reads,
// so it works for anyone who can read the bucket, Cloud Run role or not.
// A var so tests can make it hang.
var openQueueBucket = func(ctx context.Context, lc *localcfg.Config) (*blobx.Bucket, error) {
	return blobx.Open(ctx, lc.BucketURL())
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

// queuedFromRun reads one run and says whether watch shows it queued, and
// how. A run is queued when it has a launch claim or launch.json, no
// record (result.json), no cancel marker and no corrupt object, and
// runview.Join calls it launching or pending (fresh) or unlaunched with a
// claim or lost (stale: runview gave the launch up after
// runstore.ClaimTTL, so the row is stuck). A run with a record is never
// queued: it started (it is running, finished, or lost after starting).
// A corrupt object is an error, so the scan notes the run as unreadable.
func queuedFromRun(ctx context.Context, env *cloudEnv, slug, id string, since, now time.Time) (watch.QueuedRun, bool, error) {
	ro, err := readRun(ctx, env, slug, id)
	if err != nil {
		return watch.QueuedRun{}, false, err
	}
	in := ro.in
	if in.Problem != "" {
		return watch.QueuedRun{}, false, errors.New(in.Problem) // a corrupt object: ls's error row
	}
	if in.Record != nil {
		// Started: Join would not call it queued either; this only saves
		// the cancel-marker read.
		return watch.QueuedRun{}, false, nil
	}
	if in.Launch != nil {
		// readRun checks the cancel marker only for a run with no
		// launch.json; a run cancelled while pending has both.
		cancelled, err := ro.store.CancelRequested(ctx)
		if err != nil {
			return watch.QueuedRun{}, false, fmt.Errorf("checking %s/%s's cancel marker: %w", slug, id, err)
		}
		if cancelled {
			return watch.QueuedRun{}, false, nil
		}
	}
	row := runview.Join(in, noComputePrices, now)
	var stale bool
	switch {
	case row.Status == runview.StatusLaunching, row.Status == runview.StatusPending:
	case row.Status == runview.StatusUnlaunched && in.Claim != nil:
		stale = true
	case row.Status == string(runstore.StatusInfraError) && row.Reason == runview.ReasonLost:
		stale = true
	default:
		return watch.QueuedRun{}, false, nil
	}
	at := launchedAt(in, row.Created)
	if at.Before(since) {
		return watch.QueuedRun{}, false, nil
	}
	return watch.QueuedRun{
		Run: id, Slug: slug, Workflow: row.Workflow, Recipe: row.Recipe, RequestedBy: row.RequestedBy,
		LaunchedAt: at, Stale: stale,
	}, true, nil
}

// queuedNote is err as watch's one-line degrade note.
func queuedNote(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("queued runs unavailable: the runs bucket did not answer within %s", queuedScanTimeout)
	}
	return "queued runs unavailable: " + safetext.Strip(err.Error())
}

// queuedScanner reads queued runs from the runs bucket, keeping one bucket
// handle across scans (opened, and its project marker checked, on the
// first scan that succeeds at it).
type queuedScanner struct {
	lc   *localcfg.Config
	repo string
	open func(context.Context, *localcfg.Config) (*blobx.Bucket, error)
	env  *cloudEnv
}

func newQueuedScanner(lc *localcfg.Config, repo string) *queuedScanner {
	return &queuedScanner{lc: lc, repo: repo, open: openQueueBucket}
}

func (s *queuedScanner) Close() {
	if s.env != nil {
		s.env.Close()
		s.env = nil
	}
}

// scan is one read of the runs bucket. err means nothing could be read
// (rows is nil then); note, when not "", says some runs or repositories
// could not be read and are left out of rows.
func (s *queuedScanner) scan(ctx context.Context) (rows []watch.QueuedRun, note string, err error) {
	if s.env == nil {
		// The handle outlives this scan, so it is opened with ctx (its
		// credentials keep the context they were found with), not with
		// the scan's timeout.
		b, err := s.open(ctx, s.lc)
		if err != nil {
			return nil, "", remote(err)
		}
		cctx, cancel := context.WithTimeout(ctx, queuedScanTimeout)
		err = checkCloudName(cctx, b, s.lc, os.Getenv, time.Now())
		cancel()
		if err != nil {
			_ = b.Close()
			return nil, "", err
		}
		s.env = &cloudEnv{lc: s.lc, bucket: b}
	}
	sctx, cancel := context.WithTimeout(ctx, queuedScanTimeout)
	defer cancel()
	slugs, err := lsSlugs(sctx, s.env, &lsOptions{repo: s.repo}, io.Discard)
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	since := now.Add(-queuedLookback)
	skipped := 0
	var first error
	skip := func(err error) {
		skipped++
		if first == nil {
			first = err
		}
	}
	for _, slug := range slugs {
		ids, err := runstore.RecentLaunches(sctx, s.env.bucket.Bucket, slug, since)
		if sctx.Err() != nil {
			return nil, "", sctx.Err()
		}
		if err != nil {
			skip(err)
			continue
		}
		for _, id := range ids {
			q, ok, err := queuedFromRun(sctx, s.env, slug, id, since, now)
			if sctx.Err() != nil {
				return nil, "", sctx.Err()
			}
			if err != nil {
				skip(err)
				continue
			}
			if ok {
				rows = append(rows, q)
			}
		}
	}
	if skipped > 0 {
		note = fmt.Sprintf("queued runs: %d could not be read and are not shown (%s)", skipped, safetext.Strip(first.Error()))
	}
	return rows, note, nil
}

// fetchQueuedOnce is watch --once's read of the queued rows: one scan in
// the background, waited for at most queuedOnceWait; past that it returns
// no rows and a note, and the scan is abandoned.
func fetchQueuedOnce(ctx context.Context, lc *localcfg.Config, repo string) ([]watch.QueuedRun, string) {
	type result struct {
		rows []watch.QueuedRun
		note string
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc := newQueuedScanner(lc, repo)
	done := make(chan result, 1)
	go func() {
		defer sc.Close()
		rows, note, err := sc.scan(ctx)
		if err != nil {
			note = queuedNote(err)
		}
		done <- result{rows, note}
	}()
	select {
	case r := <-done:
		return r.rows, r.note
	case <-time.After(queuedOnceWait):
		return nil, fmt.Sprintf("queued runs unavailable: the runs bucket did not answer within %s", queuedOnceWait)
	}
}

// queuedSource polls the runs bucket for queued runs on its own goroutine
// and clock, and caches the latest result for Get, which the render loops
// call on every frame: no bucket read ever happens on the render path, so
// a slow or unreachable bucket never stalls the RTDB-driven view, and the
// first frame never waits for the first scan.
type queuedSource struct {
	scan func(context.Context) ([]watch.QueuedRun, string, error)
	wg   sync.WaitGroup

	mu       sync.Mutex
	rows     []watch.QueuedRun
	note     string
	failures int // scans failed in a row
}

// startQueuedSource starts scanning at once, then every queuedPollInterval
// until ctx ends. The caller's Wait, after ctx is cancelled, blocks until
// the goroutine has stopped (at most queuedScanTimeout, a scan in flight
// when ctx ends returns at once).
func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string) *queuedSource {
	sc := newQueuedScanner(lc, repo)
	qs := &queuedSource{scan: sc.scan}
	interval := queuedPollInterval
	qs.wg.Add(1)
	go func() {
		defer qs.wg.Done()
		defer sc.Close()
		qs.refresh(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				qs.refresh(ctx)
			}
		}
	}()
	return qs
}

// Wait blocks until the background poll loop has stopped (ctx must already
// be done, or this never returns).
func (qs *queuedSource) Wait() { qs.wg.Wait() }

func (qs *queuedSource) refresh(ctx context.Context) {
	rows, note, err := qs.scan(ctx)
	if ctx.Err() != nil {
		return // shutting down: nothing will render it
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if err == nil {
		qs.rows, qs.note, qs.failures = rows, note, 0
		return
	}
	qs.failures++
	qs.note = queuedNote(err)
	if qs.failures >= queuedDropAfter {
		qs.rows = nil
	} else if len(qs.rows) > 0 {
		qs.note += " (the queued rows shown are from an earlier read)"
	}
}

// Get is the latest queued rows and note, safe to call on every frame: it
// never blocks on the network.
func (qs *queuedSource) Get() ([]watch.QueuedRun, string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.rows, qs.note
}
