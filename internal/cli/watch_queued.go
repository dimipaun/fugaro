package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/dimipaun/fugaro/internal/task"
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
// its runner starts, and fugaro runs ls shows it pending meanwhile.
const queuedMintMargin = 5 * time.Minute

// finishedLookback bounds how long a run whose result.json exists still
// shows as finished (design generic-tool §10.1): long enough that a run
// taking a while to finish is still found after its registry entry (and
// so its live row) is gone (budget.Session.Finish deletes it once the
// record is written). It governs the scanner's one listing per
// repository (the same listing queued-run detection shares), widened past
// queuedLookback so a finished run's mint time, usually close to when it
// started, still falls inside it.
const finishedLookback = 24 * time.Hour

// queuedStepTimeout bounds each whole-project step of a scan: the project
// marker check and the listing of repositories.
const queuedStepTimeout = 10 * time.Second

// queuedRepoTimeout bounds one repository's part of a scan (its listing
// and the reads of its recent runs): a repository that hangs or fails is
// left out and counted in the note; the others still show.
const queuedRepoTimeout = 10 * time.Second

// queuedWorkers is how many repositories a scan reads at once.
const queuedWorkers = 8

// queuedRunReaders is how many of one repository's runs are read at once,
// inside its deadline: each run costs 3 to 5 sequential reads.
const queuedRunReaders = 6

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
// the launch claim and the cancel marker, the same objects fugaro runs ls reads,
// so it works for anyone who can read the bucket, Cloud Run role or not.
// A var so tests can make it hang.
var openQueueBucket = func(ctx context.Context, lc *localcfg.Config) (*blobx.Bucket, error) {
	return blobx.Open(ctx, lc.BucketURL())
}

// noComputePrices is runview.Join's price book for queued-run detection,
// which never reads an execution (so no compute cost is ever priced): the
// cost in the row it builds is discarded.
func noComputePrices(*backend.Execution) backend.Prices { return backend.Prices{} }

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

// scanWindow bounds what scanRun reports about a run: QueuedSince and
// QueuedMinted keep queued-run detection exactly as narrow as before
// finished runs were added (queuedLookback, queuedMintMargin), while
// FinishedSince is the wider finishedLookback that the scanner's one
// listing per repository is now based on (scan), so a finished run whose
// mint time has aged past the queued margin is still found.
type scanWindow struct {
	QueuedSince, QueuedMinted, FinishedSince time.Time
}

// scanResult is what scanRun found about one run: at most one of Queued
// or Finished is set (IsQueued, IsFinished).
type scanResult struct {
	Queued     watch.QueuedRun
	IsQueued   bool
	Finished   watch.FinishedRun
	IsFinished bool
	// Facts is what the run's own launch.json says about it: the Cloud Run job
	// it names, and who started the run — reported for every run the scan read
	// one for, a live run's included, whose RTDB registry entry carries
	// neither and could not be trusted with either (a launcher writes a job's
	// environment and its own launch.json, never the job's name or the
	// registry). Job is "" when there is no launch.json to read, and
	// LaunchedBy too (a launch.json written before the launcher was recorded).
	Facts watch.RunFacts
}

// scanRun reads one run and says what the scanner should report about it:
// queued, finished, or neither (still running, cancelled before launch, or
// outside the window either way). A corrupt object is an error, so the
// scan notes the run as unreadable.
func scanRun(ctx context.Context, env *cloudEnv, slug, id string, w scanWindow, now time.Time) (scanResult, error) {
	ro, err := readRun(ctx, env, slug, id)
	if err != nil {
		return scanResult{}, err
	}
	in := ro.in
	if in.Problem != "" {
		return scanResult{}, errors.New(in.Problem) // a corrupt object: ls's error row
	}
	// The job and the launcher come from launch.json and are attached whichever
	// row this run produces — queued, finished, or neither (a live run, which
	// this scan still read): one run's launch.json is one run's, and the rows
	// it is stamped onto are the view's, not this function's
	// (watch.MergeFacts).
	facts := watch.RunFacts{}
	if in.Launch != nil {
		facts.Job, facts.LaunchedBy = in.Launch.Job, in.Launch.LaunchedBy
	}
	res, err := scanRow(ctx, env, ro, in, w, now)
	if err != nil {
		return scanResult{}, err
	}
	res.Facts = facts
	return res, nil
}

// scanRow is what the scan says about in: finished, queued, or neither.
func scanRow(ctx context.Context, env *cloudEnv, ro runObjects, in runview.Input, w scanWindow, now time.Time) (scanResult, error) {
	if in.Record != nil {
		return finishedResult(in, w.FinishedSince, now), nil
	}
	if in.Launch != nil {
		// readRun checks the cancel marker only for a run with no
		// launch.json; a run cancelled while pending has both.
		cancelled, err := ro.store.CancelRequested(ctx)
		if err != nil {
			return scanResult{}, fmt.Errorf("checking %s/%s's cancel marker: %w", in.Slug, in.RunID, err)
		}
		if cancelled {
			return scanResult{}, nil
		}
	}
	return queuedResult(in, w.QueuedSince, w.QueuedMinted, now), nil
}

// finishedResult is run's scanResult when its record (result.json) exists:
// finished when the record is no longer running and ended within
// finishedSince of now, else nothing (still running: its /agents entry
// covers it; or too old for the scanner's lookback).
func finishedResult(in runview.Input, finishedSince, now time.Time) scanResult {
	r := in.Record
	if r.Status == runstore.StatusRunning {
		return scanResult{}
	}
	finishedAt := r.StartedAt
	if r.FinishedAt != nil {
		finishedAt = *r.FinishedAt
	}
	if finishedAt.Before(finishedSince) {
		return scanResult{}
	}
	row := runview.Join(in, noComputePrices, now)
	return scanResult{IsFinished: true, Finished: watch.FinishedRun{
		Run: in.RunID, Slug: in.Slug, Title: runTitle(in.Task), Workflow: row.Workflow,
		Status: row.Status, Outcome: row.Outcome, PRNumber: row.PR, PRURL: row.PRURL,
		LaunchedBy: row.LaunchedBy,
		FinishedAt: finishedAt,
	}}
}

// runTitle is a run's one-line title while it is queued and once it has
// finished: both rows name a run the same way, from the ONE place the text
// comes from (task.Spec.TitleLine) — the title its launcher stored (fugaro
// run --title), else its task prompt's first line. A queued --batch or
// --attempts launch would otherwise fall back to its workflow and read alike
// on every row, the very problem --title exists to fix. It is clipped as the
// runner's own registry entry titles a live run (budget_session.go's
// registryEntry). Unlike that entry, it is not redacted against the run's
// own secrets, which this reader never holds; watch.clean sanitises it for
// display like every other bucket-derived field here.
func runTitle(t *task.Spec) string {
	title := t.TitleLine()
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:80])
	}
	return title
}

// queuedResult is run's scanResult when it has no record yet: queued when
// runview.Join calls it launching or pending (fresh) or unlaunched with a
// claim or lost (stale: runview gave the launch up after
// runstore.ClaimTTL, so the row is stuck) and it is within queuedSince of
// its launch; else nothing. queuedMinted excludes a stale ID outside the
// original queued margin even though the scanner's wider listing (for
// finished runs) surfaced it, so queued detection is unchanged from before
// finished runs were added.
func queuedResult(in runview.Input, queuedSince, queuedMinted, now time.Time) scanResult {
	row := runview.Join(in, noComputePrices, now)
	var stale bool
	switch {
	case row.Status == runview.StatusLaunching, row.Status == runview.StatusPending:
	case row.Status == runview.StatusUnlaunched && in.Claim != nil:
		stale = true
	case row.Status == string(runstore.StatusInfraError) && row.Reason == runview.ReasonLost:
		stale = true
	default:
		return scanResult{}
	}
	at := launchedAt(in, row.Created)
	if at.Before(queuedSince) || row.Created.Before(queuedMinted) {
		return scanResult{}
	}
	return scanResult{IsQueued: true, Queued: watch.QueuedRun{
		Run: in.RunID, Slug: in.Slug, Title: runTitle(in.Task), Workflow: row.Workflow, Recipe: row.Recipe,
		RequestedBy: row.RequestedBy, LaunchedBy: row.LaunchedBy, LaunchedAt: at, Stale: stale,
	}}
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
	// readRun reads one run (scanRun; tests slow it down).
	readRun func(ctx context.Context, env *cloudEnv, slug, id string, w scanWindow, now time.Time) (scanResult, error)

	workers                  int
	repoTimeout, openTimeout time.Duration

	// finishedCache holds the finished runs already read, keyed by
	// "slug/runID": once a run's record is no longer running it never
	// changes again (nothing in this codebase writes result.json after a
	// run has finalized), so a later scan reuses the cached row instead of
	// re-reading task.json, launch.json and result.json for it. The facts
	// the same read found (the job and the launcher, scanResult.Facts) are
	// cached with the row: a cache hit reads no launch.json, so keeping only
	// the row would lose them from every frame after the first scan.
	// cacheMu guards it: scanRepo runs concurrently across repositories and
	// within one repository's run reads. pruneFinishedCache drops entries
	// whose FinishedAt has aged out of finishedLookback, so the cache never
	// grows past what one scan's window could hold.
	cacheMu       sync.Mutex
	finishedCache map[string]cachedFinishedRun

	env *cloudEnv
	// index places the job names the runs' launch.json records, when the
	// command has one (a live watch reads the published policy once; a unit
	// test of the scanner alone has none, and every job is then a bare name).
	index *projectJobIndex
	// facts is the job and launcher of each run the last scan read, by the
	// run's key. It is written at the end of a scan and read by Facts, both on
	// the scanning goroutine, so it needs no lock of its own: what guards it
	// against a reader on another goroutine is queuedSource's own mutex, which
	// the frame's Get holds.
	facts watch.Facts
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
		readRun: scanRun,
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

// cachedFinishedRun is one entry of the finished cache: the row a read
// produced, and the facts (the job and the launcher) the same read found for
// it. The two travel together because a cache hit reads no launch.json.
type cachedFinishedRun struct {
	row   watch.FinishedRun
	facts watch.RunFacts
}

// cachedFinished is key's cached finished run, if any.
func (s *queuedScanner) cachedFinished(key string) (cachedFinishedRun, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	c, ok := s.finishedCache[key]
	return c, ok
}

// cacheFinished remembers c under key, so a later scan does not re-read it.
func (s *queuedScanner) cacheFinished(key string, c cachedFinishedRun) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.finishedCache == nil {
		s.finishedCache = map[string]cachedFinishedRun{}
	}
	s.finishedCache[key] = c
}

// pruneFinishedCache drops every cached entry whose FinishedAt is before
// finishedSince: once a scan's lookback has moved past a finished run, it
// drops out of the finished list on its own (finishedResult), so keeping
// it cached would only grow the cache forever.
func (s *queuedScanner) pruneFinishedCache(finishedSince time.Time) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for key, c := range s.finishedCache {
		if c.row.FinishedAt.Before(finishedSince) {
			delete(s.finishedCache, key)
		}
	}
}

// repoScan is one repository's part of a scan.
type repoScan struct {
	queued     []watch.QueuedRun
	finished   []watch.FinishedRun
	facts      watch.Facts // the job and launcher of each run read, by the run's key
	unreadable int         // runs left out (failed reads)
	runErr     error       // the first of them's error
	cutOff     int         // runs not read before the repository's deadline
	total      int         // runs listed
	err        error       // the repository could not be listed: rows is nil
}

// scanRepo reads slug's queued and finished runs under its own deadline:
// one listing of the run IDs minted since listMinted (w.FinishedSince's
// wider window, so a finished run's mint time still falls inside it), then
// the reads of those runs not already in the finished cache, up to
// queuedRunReaders at once. When the deadline fires mid-way the rows
// already read are kept and the runs not read are counted in cutOff (a
// cache hit never counts against it: it costs no read at all).
func (s *queuedScanner) scanRepo(ctx context.Context, env *cloudEnv, slug string, listMinted time.Time, w scanWindow, now time.Time) repoScan {
	rctx, cancel := context.WithTimeout(ctx, s.repoTimeout)
	defer cancel()
	ids, err := s.listIDs(rctx, env.bucket.Bucket, slug, listMinted)
	if err != nil {
		return repoScan{err: err}
	}
	type result struct {
		res  scanResult
		err  error
		done bool // the read finished before the deadline
	}
	results := make([]result, len(ids))
	var toRead []int
	for i, id := range ids {
		if c, ok := s.cachedFinished(slug + "/" + id); ok {
			results[i] = result{scanResult{IsFinished: true, Finished: c.row, Facts: c.facts}, nil, true}
			continue
		}
		toRead = append(toRead, i)
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(queuedRunReaders, len(toRead)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				res, err := s.readRun(rctx, env, slug, ids[i], w, now)
				if rctx.Err() != nil {
					continue // cut off: the result is not trusted
				}
				if res.IsFinished {
					s.cacheFinished(slug+"/"+ids[i], cachedFinishedRun{row: res.Finished, facts: res.Facts})
				}
				results[i] = result{res, err, true}
			}
		}()
	}
feed:
	for _, i := range toRead {
		select {
		case next <- i:
		case <-rctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()

	r := repoScan{total: len(ids)}
	for i, res := range results {
		if res.done && res.err == nil && (res.res.Facts.Job != "" || res.res.Facts.LaunchedBy != "") {
			if r.facts == nil {
				r.facts = watch.Facts{}
			}
			// watch.RunKeyOf applies the same normalisation the rows carry
			// (the wire slug key, the cleaned run id), so the reader that
			// stamps the view never reproduces either rule.
			r.facts[watch.RunKeyOf(slug, ids[i])] = s.factsOf(res.res.Facts)
		}
		switch {
		case !res.done:
			r.cutOff++
		case res.err != nil:
			r.unreadable++
			if r.runErr == nil {
				r.runErr = res.err
			}
		case res.res.IsQueued:
			r.queued = append(r.queued, res.res.Queued)
		case res.res.IsFinished:
			r.finished = append(r.finished, res.res.Finished)
		}
	}
	if ctx.Err() != nil {
		return repoScan{err: ctx.Err()} // shutting down
	}
	return r
}

// scan is one read of the runs bucket. err means nothing could be read
// (queued and finished are nil then); note, when not "", says some
// repositories or runs could not be read and are left out. Repositories are
// read in parallel (s.workers at a time), each under its own deadline.
func (s *queuedScanner) scan(ctx context.Context) (queued []watch.QueuedRun, finished []watch.FinishedRun, note string, err error) {
	env, err := s.connect(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	lctx, cancel := context.WithTimeout(ctx, queuedStepTimeout)
	slugs, err := s.slugs(lctx, env)
	cancel()
	if err != nil {
		return nil, nil, "", err
	}
	now := s.now().UTC()
	w := scanWindow{
		QueuedSince:   now.Add(-queuedLookback),
		FinishedSince: now.Add(-finishedLookback),
	}
	w.QueuedMinted = w.QueuedSince.Add(-queuedMintMargin)
	// The one listing per repository covers both queued and finished
	// detection, so it uses the wider of the two windows.
	listMinted := w.FinishedSince.Add(-queuedMintMargin)
	s.pruneFinishedCache(w.FinishedSince)

	results := make([]repoScan, len(slugs))
	repoJobs := make(chan int)
	var wg sync.WaitGroup
	for range min(s.workers, len(slugs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range repoJobs {
				results[i] = s.scanRepo(ctx, env, slugs[i], listMinted, w, now)
			}
		}()
	}
feed:
	for i := range slugs {
		select {
		case repoJobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(repoJobs)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, nil, "", ctx.Err() // shutting down
	}

	jobs := watch.Facts{}
	var parts []string
	unreadRepos, unreadRuns := 0, 0
	var repoErr, runErr, cutOff string
	cutRepos := 0
	for i, r := range results {
		if r.err != nil {
			if unreadRepos == 0 {
				repoErr = slugs[i] + ": " + errText(r.err, s.repoTimeout)
			}
			unreadRepos++
			continue
		}
		queued = append(queued, r.queued...)
		finished = append(finished, r.finished...)
		maps.Copy(jobs, r.facts)
		if r.cutOff > 0 {
			if cutRepos == 0 {
				cutOff = fmt.Sprintf("%s: %d of %d runs not read (%s)", slugs[i], r.cutOff, r.total, errText(context.DeadlineExceeded, s.repoTimeout))
			}
			cutRepos++
		}
		if r.unreadable > 0 && unreadRuns == 0 {
			runErr = errText(r.runErr, s.repoTimeout)
		}
		unreadRuns += r.unreadable
	}
	if unreadRepos > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d repositories not read (%s)", unreadRepos, len(slugs), repoErr))
	}
	if cutRepos > 0 {
		more := ""
		if cutRepos > 1 {
			more = fmt.Sprintf(" and %d more repositories", cutRepos-1)
		}
		parts = append(parts, "runs left unread, the rows read are shown: "+cutOff+more)
	}
	if unreadRuns > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be read and are not shown (%s)", unreadRuns, runErr))
	}
	if len(parts) > 0 {
		note = "runs: " + strings.Join(parts, "; ")
	}
	s.facts = jobs
	return queued, finished, note, nil
}

// factsOf is what a scan read about one run, as the facts a frame stamps onto
// the row it draws: the job name launch.json recorded, placed against the
// published policy (s.index.ref — never parsed), and the launcher launch.json
// recorded. The launcher is carried as it was read; watch.MergeFacts sanitises
// it for display like every other bucket-derived string.
func (s *queuedScanner) factsOf(f watch.RunFacts) watch.RunFacts {
	return watch.RunFacts{JobRef: s.index.ref(f.Job), LaunchedBy: f.LaunchedBy}
}

// Facts is the job and launcher of each run the last scan read, by the run's
// key (plan task 3.5 step 2, and who started a run). It is what a frame stamps
// onto its rows (watch.MergeFacts): neither a live row's job nor its launcher
// is in any registry entry, so the bucket's launch.json — read by the scan for
// every run it lists — is where they come from.
//
// Call it on the goroutine that ran the scan (queuedSource.refresh does): the
// map is the scan's own and is never mutated after it returns.
func (s *queuedScanner) Facts() watch.Facts { return s.facts }

// fetchQueuedOnce is watch --once's read of the queued and finished rows:
// one scan in the background, waited for at most queuedOnceWait; past that
// it returns no rows and a note, and the scan is abandoned.
func fetchQueuedOnce(ctx context.Context, lc *localcfg.Config, repo string, index *projectJobIndex) ([]watch.QueuedRun, []watch.FinishedRun, watch.Facts, string) {
	type result struct {
		queued   []watch.QueuedRun
		finished []watch.FinishedRun
		facts    watch.Facts
		note     string
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sc := newQueuedScanner(lc, repo)
	sc.index = index
	done := make(chan result, 1)
	go func() {
		defer sc.Close()
		queued, finished, note, err := sc.scan(ctx)
		if err != nil {
			note = queuedNote(err)
		}
		done <- result{queued, finished, sc.Facts(), note}
	}()
	select {
	case r := <-done:
		return r.queued, r.finished, r.facts, r.note
	case <-time.After(queuedOnceWait):
		return nil, nil, nil, fmt.Sprintf("queued runs unavailable: the runs bucket did not answer within %s", queuedOnceWait)
	}
}

// queuedSource polls the runs bucket for queued and finished runs on its
// own goroutine and clock, and caches the latest result for Get, which the
// render loops call on every frame: no bucket read ever happens on the
// render path, so a slow or unreachable bucket never stalls the
// RTDB-driven view, and the first frame never waits for the first scan.
type queuedSource struct {
	sc *queuedScanner
	// scan is the scan to run: sc's own, unless a test sets one to drive the
	// failure handling below without a bucket behind it.
	scan func(context.Context) ([]watch.QueuedRun, []watch.FinishedRun, string, error)
	wg   sync.WaitGroup

	mu       sync.Mutex
	rows     []watch.QueuedRun
	finished []watch.FinishedRun
	// facts is the job and launcher of each run the last good scan read,
	// resolved against the published policy (watch.MergeFacts stamps them
	// onto the rows the frame draws).
	facts    watch.Facts
	note     string
	failures int // scans failed in a row
}

// startQueuedSource starts scanning at once, then every queuedNextPoll()
// until ctx ends. The caller's Wait, after ctx is cancelled, returns
// promptly: every step of a scan stops when ctx ends, and an open in
// flight is left to finish on its own goroutine.
func startQueuedSource(ctx context.Context, lc *localcfg.Config, repo string, index *projectJobIndex) *queuedSource {
	sc := newQueuedScanner(lc, repo)
	sc.index = index
	return runQueuedSource(ctx, sc)
}

// runQueuedSource polls sc on its own goroutine until ctx ends, then closes
// it.
func runQueuedSource(ctx context.Context, sc *queuedScanner) *queuedSource {
	qs := &queuedSource{sc: sc}
	qs.wg.Add(1)
	go func() {
		defer qs.wg.Done()
		defer sc.Close()
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
	scan := qs.scan
	if scan == nil {
		scan = qs.sc.scan
	}
	rows, finished, note, err := scan(ctx)
	if ctx.Err() != nil {
		return // shutting down: nothing will render it
	}
	var facts watch.Facts
	if qs.sc != nil {
		facts = qs.sc.Facts()
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if err == nil {
		qs.rows, qs.finished, qs.facts, qs.note, qs.failures = rows, finished, facts, note, 0
		return
	}
	qs.failures++
	qs.note = queuedNote(err)
	if qs.failures >= queuedDropAfter {
		qs.rows, qs.finished = nil, nil
		qs.facts = nil
	} else if len(qs.rows) > 0 || len(qs.finished) > 0 {
		qs.note += " (the queued rows shown are from an earlier read)"
	}
}

// Get is the latest queued and finished rows and note, safe to call on
// every frame: it never blocks on the network.
func (qs *queuedSource) Get() ([]watch.QueuedRun, []watch.FinishedRun, string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.rows, qs.finished, qs.note
}

// Facts is the last good scan's facts, by the run's key. Safe to call on every
// frame, like Get, and empty until the first scan lands.
func (qs *queuedSource) Facts() watch.Facts {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.facts
}
