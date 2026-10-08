package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"gocloud.dev/blob"

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
// background, each step under its own deadline.

// queuedPollEvery is how often the live screen re-scans the runs bucket
// for queued runs, plus up to queuedPollJitter so that many open watches
// do not list in step: much slower than the RTDB ticks (15 s) that drive
// the rest of the view, since every scan costs a list call per repository.
const (
	queuedPollEvery  = 60 * time.Second
	queuedPollJitter = 5 * time.Second
)

// queuedNextPoll is the wait before the next scan. A var so tests can
// shrink it.
var queuedNextPoll = func() time.Duration {
	return queuedPollEvery + rand.N(queuedPollJitter)
}

// queuedOnceWait is how long watch --once waits for the first scan before
// printing without queued rows (and with a note). A var so tests can
// shrink it.
var queuedOnceWait = 3 * time.Second

// queuedLookback bounds how long a run shows queued: until this long after
// its launch claim or launch.json (by their own timestamps). A launch older
// than runstore.ClaimTTL that never got a record stays listed as stuck
// until it leaves this window.
const queuedLookback = 30 * time.Minute

// queuedMintMargin is how much older than the lookback a run ID (its mint
// time) may be and still be listed: the ID is minted just before the claim
// and the launch, so the bucket listing starts at now − queuedLookback −
// queuedMintMargin. A run launched later than that after its ID was minted
// (fugaro run --retry of an old stored task) is not found: it shows once
// its runner starts, and fugaro ls shows it pending meanwhile.
const queuedMintMargin = 5 * time.Minute

// queuedStepTimeout bounds each whole-project step of a scan: the project
// marker check and the listing of repositories.
const queuedStepTimeout = 10 * time.Second

// queuedRepoTimeout bounds one repository's part of a scan (its listing
// and the reads of its recent runs): a repository that hangs or fails is
// left out and counted in the note; the others still show.
const queuedRepoTimeout = 10 * time.Second

// queuedWorkers is how many repositories a scan reads at once.
const queuedWorkers = 8

// queuedOpenTimeout bounds how long one scan waits for the bucket to open
// (credential discovery and the marker check). An open still running past
// it is not restarted: the next scan waits for the same one.
const queuedOpenTimeout = 20 * time.Second

// queuedOpenBackoffMin and queuedOpenBackoffMax bound the wait before the
// bucket is opened again after an open failed: it doubles from the first
// to the second with every failure in a row, so a missing grant or
// credential costs one discovery every few minutes, not one per poll.
const (
	queuedOpenBackoffMin = time.Minute
	queuedOpenBackoffMax = 5 * time.Minute
)

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

// errText is err for a note: a deadline says which one, any other error
// is sanitised (bucket and object text is not ours).
func errText(err error, within time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("the runs bucket did not answer within %s", within)
	}
	return safetext.Strip(err.Error())
}

// queuedNote is a failed scan's err as watch's one-line degrade note.
func queuedNote(err error) string {
	return "queued runs unavailable: " + errText(err, queuedStepTimeout)
}

// queuedScanner reads queued runs from the runs bucket, keeping one bucket
// handle across scans (opened, and its project marker checked, on the
// first scan that succeeds at it). Its methods are called from one
// goroutine at a time.
type queuedScanner struct {
	lc   *localcfg.Config
	repo string
	open func(context.Context, *localcfg.Config) (*blobx.Bucket, error)
	// slugs lists the repositories to scan; listIDs a repository's run IDs
	// minted since a time. Seams for tests.
	slugs   func(context.Context, *cloudEnv) ([]string, error)
	listIDs func(context.Context, *blob.Bucket, string, time.Time) ([]string, error)
	now     func() time.Time

	workers                  int
	repoTimeout, openTimeout time.Duration

	env *cloudEnv
	// opening is the open in flight, nil when none.
	opening chan openResult
	// openFails counts opens failed in a row; until nextOpen no new open
	// starts and a scan fails at once with openErr.
	openFails int
	nextOpen  time.Time
	openErr   error
}

type openResult struct {
	b   *blobx.Bucket
	err error
}

func newQueuedScanner(lc *localcfg.Config, repo string) *queuedScanner {
	return &queuedScanner{
		lc: lc, repo: repo, open: openQueueBucket,
		slugs: func(ctx context.Context, env *cloudEnv) ([]string, error) {
			return lsSlugs(ctx, env, &lsOptions{repo: repo}, io.Discard)
		},
		listIDs: runstore.ListRunIDs,
		now:     time.Now,
		workers: queuedWorkers, repoTimeout: queuedRepoTimeout, openTimeout: queuedOpenTimeout,
	}
}

// Close releases the bucket handle, never blocking: an open still in
// flight is closed by its own goroutine once it ends.
func (s *queuedScanner) Close() {
	if s.env != nil {
		s.env.Close()
		s.env = nil
	}
	if ch := s.opening; ch != nil {
		s.opening = nil
		go func() {
			if r := <-ch; r.b != nil {
				_ = r.b.Close()
			}
		}()
	}
}

// connect returns the bucket handle, opening it if need be. The open runs
// on its own goroutine with ctx (the handle outlives this scan, and its
// credentials keep the context they were found with), and this waits for
// it at most s.openTimeout and never past ctx. A failed open is retried
// only after a backoff (queuedOpenBackoffMin doubling to
// queuedOpenBackoffMax); until then connect fails at once with its error.
func (s *queuedScanner) connect(ctx context.Context) (*cloudEnv, error) {
	if s.env != nil {
		return s.env, nil
	}
	if s.opening == nil {
		if s.openErr != nil && s.now().Before(s.nextOpen) {
			return nil, s.openErr
		}
		ch := make(chan openResult, 1)
		s.opening = ch
		go func() {
			b, err := s.open(ctx, s.lc)
			if err != nil {
				ch <- openResult{err: remote(err)}
				return
			}
			cctx, cancel := context.WithTimeout(ctx, queuedStepTimeout)
			err = checkCloudName(cctx, b, s.lc, os.Getenv, time.Now())
			cancel()
			if err != nil {
				_ = b.Close()
				ch <- openResult{err: err}
				return
			}
			ch <- openResult{b: b}
		}()
	}
	t := time.NewTimer(s.openTimeout)
	defer t.Stop()
	select {
	case r := <-s.opening:
		s.opening = nil
		if r.err != nil {
			s.openFails++
			wait := queuedOpenBackoffMax
			if s.openFails <= 8 {
				wait = min(queuedOpenBackoffMin<<(s.openFails-1), queuedOpenBackoffMax)
			}
			s.nextOpen, s.openErr = s.now().Add(wait), r.err
			return nil, r.err
		}
		s.openFails, s.openErr = 0, nil
		s.env = &cloudEnv{lc: s.lc, bucket: r.b}
		return s.env, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, fmt.Errorf("opening the runs bucket did not finish within %s", s.openTimeout)
	}
}

// repoScan is one repository's part of a scan.
type repoScan struct {
	rows       []watch.QueuedRun
	unreadable int   // runs left out
	runErr     error // the first of them's error
	err        error // the repository could not be read: rows is nil
}

// scanRepo reads slug's queued runs under its own deadline: one listing of
// the run IDs minted since minted, then the reads of those runs.
func (s *queuedScanner) scanRepo(ctx context.Context, env *cloudEnv, slug string, minted, since, now time.Time) repoScan {
	rctx, cancel := context.WithTimeout(ctx, s.repoTimeout)
	defer cancel()
	ids, err := s.listIDs(rctx, env.bucket.Bucket, slug, minted)
	if err != nil {
		return repoScan{err: err}
	}
	var r repoScan
	for _, id := range ids {
		q, ok, err := queuedFromRun(rctx, env, slug, id, since, now)
		if rctx.Err() != nil {
			return repoScan{err: rctx.Err()}
		}
		switch {
		case err != nil:
			r.unreadable++
			if r.runErr == nil {
				r.runErr = err
			}
		case ok:
			r.rows = append(r.rows, q)
		}
	}
	return r
}

// scan is one read of the runs bucket. err means nothing could be read
// (rows is nil then); note, when not "", says some repositories or runs
// could not be read and are left out of rows. Repositories are read in
// parallel (s.workers at a time), each under its own deadline.
func (s *queuedScanner) scan(ctx context.Context) (rows []watch.QueuedRun, note string, err error) {
	env, err := s.connect(ctx)
	if err != nil {
		return nil, "", err
	}
	lctx, cancel := context.WithTimeout(ctx, queuedStepTimeout)
	slugs, err := s.slugs(lctx, env)
	cancel()
	if err != nil {
		return nil, "", err
	}
	now := s.now().UTC()
	since := now.Add(-queuedLookback)
	minted := since.Add(-queuedMintMargin)

	results := make([]repoScan, len(slugs))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(s.workers, len(slugs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = s.scanRepo(ctx, env, slugs[i], minted, since, now)
			}
		}()
	}
feed:
	for i := range slugs {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, "", ctx.Err() // shutting down
	}

	var parts []string
	unreadRepos, unreadRuns := 0, 0
	var repoErr, runErr string
	for i, r := range results {
		if r.err != nil {
			if unreadRepos == 0 {
				repoErr = slugs[i] + ": " + errText(r.err, s.repoTimeout)
			}
			unreadRepos++
			continue
		}
		rows = append(rows, r.rows...)
		if r.unreadable > 0 && unreadRuns == 0 {
			runErr = errText(r.runErr, s.repoTimeout)
		}
		unreadRuns += r.unreadable
	}
	if unreadRepos > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d repositories not read (%s)", unreadRepos, len(slugs), repoErr))
	}
	if unreadRuns > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be read and are not shown (%s)", unreadRuns, runErr))
	}
	if len(parts) > 0 {
		note = "queued runs: " + strings.Join(parts, "; ")
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

// startQueuedSource starts scanning at once, then every queuedNextPoll()
// until ctx ends. The caller's Wait, after ctx is cancelled, returns
// promptly: every step of a scan stops when ctx ends, and an open in
// flight is left to finish on its own goroutine.
func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string) *queuedSource {
	sc := newQueuedScanner(lc, repo)
	return runQueuedSource(ctx, sc.scan, sc.Close)
}

// runQueuedSource polls scan on its own goroutine until ctx ends, then
// calls done.
func runQueuedSource(ctx context.Context, scan func(context.Context) ([]watch.QueuedRun, string, error), done func()) *queuedSource {
	qs := &queuedSource{scan: scan}
	qs.wg.Add(1)
	go func() {
		defer qs.wg.Done()
		defer done()
		for {
			qs.refresh(ctx)
			t := time.NewTimer(queuedNextPoll())
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
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
