package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/watch"
)

// putQueued writes task.json and launch.json for slug/run, launched at.
func putQueued(t *testing.T, put func(key string, data []byte), slug, run string, at time.Time) {
	t.Helper()
	launch, err := json.Marshal(map[string]any{
		"version": 1, "run_id": run, "backend": "cloud-run", "job": "j",
		"execution": "projects/p/locations/r/jobs/j/executions/e-" + run, "launched_at": at.UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	put("runs/"+slug+"/"+run+"/task.json", []byte(`{"version":1,"run_id":"`+run+`","repo":"acme/app","ref":"main","task":"do it","overrides":{}}`))
	put("runs/"+slug+"/"+run+"/launch.json", launch)
}

// openedScanner is a scanner whose bucket is already open on b.
func openedScanner(b *blob.Bucket) *queuedScanner {
	lc := &localcfg.Config{}
	sc := newQueuedScanner(lc, "")
	sc.env = &cloudEnv{lc: lc, bucket: &blobx.Bucket{Bucket: b}}
	return sc
}

func queuedRunIDs(rows []watch.QueuedRun) []string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.Slug+"/"+r.Run)
	}
	slices.Sort(ids)
	return ids
}

// A scan costs one listing of the repositories plus one small listing per
// repository, however many runs each repository has ever had: 60
// repositories with 5000 old runs each cost 61 list calls, and the runs
// launched within the lookback are still found.
func TestQueuedScanListCallsIndependentOfHistory(t *testing.T) {
	const repos, history = 60, 5000
	g := gcpfake.NewGCS(t)
	put := func(key string, data []byte) { g.Put("runs", key, data) }
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)
	for r := range repos {
		slug := fmt.Sprintf("acme-r%02d", r)
		for i := range history {
			put("runs/"+slug+"/"+old.Add(time.Duration(i)*time.Hour).Format("20060102-150405")+"-0000/task.json", []byte("{}"))
		}
	}
	recent := now.Add(-2 * time.Minute)
	putQueued(t, put, "acme-r00", recent.Format("20060102-150405")+"-aaaa", recent)
	putQueued(t, put, "acme-r59", recent.Format("20060102-150405")+"-bbbb", recent)

	sc := openedScanner(g.Bucket(t, "runs").Bucket)
	before := g.ListCalls()
	rows, note, err := sc.scan(context.Background())
	if err != nil || note != "" {
		t.Fatalf("scan: %v, note %q", err, note)
	}
	if got := len(queuedRunIDs(rows)); got != 2 {
		t.Fatalf("want the 2 recent runs queued, got %v", queuedRunIDs(rows))
	}
	if n := g.ListCalls() - before; n != 1+repos {
		t.Fatalf("one scan made %d list calls, want %d (one per repository plus the repository listing)", n, 1+repos)
	}
}

// One repository that hangs past its own deadline is left out and counted
// in the note; the others are read in parallel and still show.
func TestQueuedScanHungRepoKeepsOthers(t *testing.T) {
	b := memblob.OpenBucket(nil)
	ctx := context.Background()
	put := func(key string, data []byte) {
		if err := b.WriteAll(ctx, key, data, nil); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Minute)
	id := at.UTC().Format("20060102-150405")
	putQueued(t, put, "acme-a", id+"-aaaa", at)
	putQueued(t, put, "acme-hung", id+"-bbbb", at)
	putQueued(t, put, "acme-c", id+"-cccc", at)

	sc := openedScanner(b)
	sc.repoTimeout = 200 * time.Millisecond
	sc.slugs = func(context.Context, *cloudEnv) ([]string, error) {
		return []string{"acme-a", "acme-hung", "acme-c"}, nil
	}
	var hung, overlapped atomic.Bool
	sc.listIDs = func(ctx context.Context, b *blob.Bucket, slug string, since time.Time) ([]string, error) {
		if slug == "acme-hung" {
			hung.Store(true)
			<-ctx.Done()
			hung.Store(false)
			return nil, ctx.Err()
		}
		if slug == "acme-c" {
			deadline := time.Now().Add(time.Second) // give the hung one time to start
			for !hung.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			overlapped.Store(hung.Load())
		}
		return runstore.ListRunIDs(ctx, b, slug, since)
	}
	start := time.Now()
	rows, note, err := sc.scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !overlapped.Load() {
		t.Fatal("repositories were not read in parallel: acme-c waited for acme-hung")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the scan took %s: the hung repository's deadline did not hold", took)
	}
	if got := queuedRunIDs(rows); !slices.Equal(got, []string{"acme-a/" + id + "-aaaa", "acme-c/" + id + "-cccc"}) {
		t.Fatalf("rows = %v", got)
	}
	if !strings.Contains(note, "1 of 3 repositories not read (acme-hung: the runs bucket did not answer within 200ms)") {
		t.Fatalf("note = %q", note)
	}
}

// A repository whose listing fails is left out and counted; the rows of
// the others are kept, never discarded with it.
func TestQueuedScanRepoErrorKeepsPartialResults(t *testing.T) {
	b := memblob.OpenBucket(nil)
	ctx := context.Background()
	put := func(key string, data []byte) {
		if err := b.WriteAll(ctx, key, data, nil); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Minute)
	id := at.UTC().Format("20060102-150405") + "-aaaa"
	putQueued(t, put, "acme-a", id, at)

	sc := openedScanner(b)
	sc.slugs = func(context.Context, *cloudEnv) ([]string, error) { return []string{"acme-a", "acme-broken"}, nil }
	sc.listIDs = func(ctx context.Context, b *blob.Bucket, slug string, since time.Time) ([]string, error) {
		if slug == "acme-broken" {
			return nil, errors.New("boom\x1b[2J")
		}
		return runstore.ListRunIDs(ctx, b, slug, since)
	}
	rows, note, err := sc.scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := queuedRunIDs(rows); !slices.Equal(got, []string{"acme-a/" + id}) {
		t.Fatalf("rows = %v", got)
	}
	if !strings.Contains(note, "1 of 2 repositories not read (acme-broken: boom") || strings.Contains(note, "\x1b") {
		t.Fatalf("note = %q", note)
	}
}

// The queued source polls once a minute, with a little jitter, well apart
// from the live view's 15 s ticks.
func TestQueuedPollCadence(t *testing.T) {
	if queuedPollEvery != 60*time.Second || queuedPollJitter <= 0 || queuedPollJitter > 10*time.Second {
		t.Fatalf("poll every %s + up to %s", queuedPollEvery, queuedPollJitter)
	}
	seen := map[time.Duration]bool{}
	for range 200 {
		d := queuedNextPoll()
		if d < queuedPollEvery || d >= queuedPollEvery+queuedPollJitter {
			t.Fatalf("next poll in %s", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatal("no jitter")
	}
}

// A bucket that fails to open is opened again only after a backoff that
// doubles up to queuedOpenBackoffMax, not on every poll; each scan in
// between fails at once with the open's error.
func TestQueuedScanOpenBackoff(t *testing.T) {
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	var opens atomic.Int32
	sc := newQueuedScanner(&localcfg.Config{}, "")
	sc.now = func() time.Time { return clock }
	sc.open = func(context.Context, *localcfg.Config) (*blobx.Bucket, error) {
		opens.Add(1)
		return nil, errors.New("no credentials")
	}
	var at []int
	for poll := range 21 { // one poll a minute for 20 minutes
		before := opens.Load()
		_, _, err := sc.scan(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no credentials") {
			t.Fatalf("poll %d: err = %v", poll, err)
		}
		if opens.Load() > before {
			at = append(at, poll)
		}
		clock = clock.Add(time.Minute)
	}
	// Opens at 0, then after 1, 2, 4, 5 (capped), 5 minutes.
	if want := []int{0, 1, 3, 7, 12, 17}; !slices.Equal(at, want) {
		t.Fatalf("opened at minutes %v, want %v", at, want)
	}
}

// Quitting while the bucket open is blocked returns at once, and leaves no
// goroutine behind once the open ends: whether the open honours its
// context or ignores it until it finishes on its own.
func TestQueuedSourceQuitWhileOpenBlocked(t *testing.T) {
	for _, honours := range []bool{true, false} {
		t.Run(fmt.Sprintf("honoursCtx=%v", honours), func(t *testing.T) {
			base := runtime.NumGoroutine()
			release := make(chan struct{})
			started := make(chan struct{})
			sc := newQueuedScanner(&localcfg.Config{}, "")
			sc.open = func(ctx context.Context, _ *localcfg.Config) (*blobx.Bucket, error) {
				close(started)
				if honours {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				<-release
				return nil, errors.New("too late")
			}
			ctx, cancel := context.WithCancel(context.Background())
			qs := runQueuedSource(ctx, sc.scan, sc.Close)
			<-started
			cancel()
			done := make(chan struct{})
			go func() { qs.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Wait blocked on the open")
			}
			close(release)
			deadline := time.Now().Add(5 * time.Second)
			for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > base {
				t.Fatalf("%d goroutines left behind", n-base)
			}
		})
	}
}
