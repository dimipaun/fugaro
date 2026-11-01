package budget_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func putRecord(t *testing.T, b *blobx.Bucket, slug, id string, rec runstore.Record) {
	t.Helper()
	rec.RunID = id
	if err := runstore.Open(b.Bucket, slug, id).WriteRecord(context.Background(), &rec); err != nil {
		t.Fatal(err)
	}
}

func TestBucketFactsAttribution(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	// Minted 23:59:50 on day, started 00:00:10 the next day: the start day counts.
	start := time.Date(2026, 10, 19, 0, 0, 10, 0, time.UTC)
	fin := start.Add(90 * time.Minute)
	c := runstore.NewCost(1.5, 0.25, runstore.BasisAPIList)
	putRecord(t, b, "acme-app", "20261018-235950-aaaa", runstore.Record{Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c})
	// Compute not estimated: hours, no dollars.
	un := runstore.ModelOnlyCost(1, runstore.BasisAPIList)
	putRecord(t, b, "acme-app", "20261018-100000-bbbb", runstore.Record{Repo: "acme/app", StartedAt: time.Date(2026, 10, 18, 10, 0, 5, 0, time.UTC),
		FinishedAt: ptrTo(time.Date(2026, 10, 18, 11, 0, 5, 0, time.UTC)), Cost: &un})
	// Out of range, and not yet recorded.
	putRecord(t, b, "acme-app", "20261001-100000-cccc", runstore.Record{StartedAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)})
	if err := b.WriteAll(context.Background(), "runs/acme-app/20261018-120000-dddd/task.json", []byte("{}"), nil); err != nil {
		t.Fatal(err)
	}
	// A corrupt record is skipped with a warning.
	if err := b.WriteAll(context.Background(), "runs/acme-app/20261018-130000-eeee/result.json", []byte("{nope"), nil); err != nil {
		t.Fatal(err)
	}
	var warns []string
	f := budget.BucketFacts{Bucket: b.Bucket, Warn: func(s string) { warns = append(warns, s) }}
	facts, repos, err := f.Facts(context.Background(), day, day+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[day]) != 1 || facts[day][0].Run != "20261018-100000-bbbb" || facts[day][0].ComputeEstimated || facts[day][0].Hours != 1 {
		t.Fatalf("facts[day] = %+v", facts[day])
	}
	g := facts[day+1]
	if len(g) != 1 || g[0].Run != "20261018-235950-aaaa" || !g[0].ComputeEstimated || g[0].ComputeUSD != 0.25 || g[0].Hours != 1.5 {
		t.Fatalf("facts[day+1] = %+v", g)
	}
	if repos["acme-app"] != "acme/app" || len(warns) != 1 || !strings.Contains(warns[0], "eeee") {
		t.Fatalf("repos %v warns %v", repos, warns)
	}
}

// Slugs narrows the pass to the repositories named: a caller that wants one
// repository's facts reads no other repository's runs, so an unreadable record
// elsewhere is not even looked at.
func TestBucketFactsSlugFilter(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	start := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	fin := start.Add(time.Hour)
	c := runstore.NewCost(1, 0.25, runstore.BasisAPIList)
	putRecord(t, b, "acme-app", "20261018-100000-aaaa", runstore.Record{Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c})
	putRecord(t, b, "acme-other", "20261018-100000-bbbb", runstore.Record{Repo: "acme/other", StartedAt: start, FinishedAt: &fin, Cost: &c})
	if err := b.WriteAll(context.Background(), "runs/acme-other/20261018-110000-cccc/result.json", []byte("{nope"), nil); err != nil {
		t.Fatal(err)
	}
	var warns []string
	f := budget.BucketFacts{Bucket: b.Bucket, Warn: func(s string) { warns = append(warns, s) }, Slugs: []string{"acme-app"}}
	facts, repos, err := f.Facts(context.Background(), day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[day]) != 1 || facts[day][0].Slug != "acme-app" || facts[day][0].ComputeUSD != 0.25 {
		t.Fatalf("facts[day] = %+v", facts[day])
	}
	if len(repos) != 1 || repos["acme-app"] != "acme/app" {
		t.Fatalf("repos = %v", repos)
	}
	if len(warns) != 0 {
		t.Fatalf("a narrowed pass read another repository's runs: %v", warns)
	}
}

// The parallel pass's own tests. Facts now reads many runs' result.json at
// once, which must change nothing about what it returns: these hold the
// reads' width, the order of what comes out, and the handling of a record
// that cannot be read.

// factsDriver is the runs bucket these tests drive: the objects a test seeds,
// a count of how many object reads are in flight at once, and a way to fail
// one read or report it larger than its bytes.
type factsDriver struct {
	driver.Bucket // nil: only the methods below are reached
	mu            sync.Mutex
	objects       map[string][]byte
	sizes         map[string]int64
	fail          map[string]error
	hold          map[string]bool // reads that wait for the pass's own cancel, as a real driver's do
	cancelled     int             // reads the pass's cancel turned back
	gaveUp        int             // reads that waited for a cancel that never came
	read          int             // object reads that reached the object: what a pass pays for
	wait          time.Duration   // how long a read holds the bucket open waiting for the declared width
	want          int             // the width a read waits for; 0 reads without waiting
	open          int
	peak          int
	reached       bool          // open has been want at least once
	width         chan struct{} // closed the first time want reads are in flight at once
	widthOnce     sync.Once
}

// errNoSuchObject is the driver's missing object. ErrorCode reports it as
// gcerrors.NotFound, which is what runstore reads as "this run has no
// result.json".
var errNoSuchObject = errors.New("no such object")

func newFactsDriver() *factsDriver {
	return &factsDriver{
		objects: map[string][]byte{}, sizes: map[string]int64{}, fail: map[string]error{}, hold: map[string]bool{},
		width: make(chan struct{}),
	}
}

func (d *factsDriver) ErrorCode(err error) gcerrors.ErrorCode {
	if errors.Is(err, errNoSuchObject) {
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}

func (d *factsDriver) As(any) bool             { return false }
func (d *factsDriver) ErrorAs(error, any) bool { return false }
func (d *factsDriver) Close() error            { return nil }

// peakOpen is the most object reads that were ever in flight at once.
func (d *factsDriver) peakOpen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.peak
}

// reads is how many object reads have reached an object so far: one per run's
// result.json the pass read. A look-back that lists runs the window then drops
// is read here and nowhere else.
func (d *factsDriver) reads() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.read
}

func (d *factsDriver) ListPaged(_ context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	d.mu.Lock()
	keys := make([]string, 0, len(d.objects))
	for k := range d.objects {
		keys = append(keys, k)
	}
	d.mu.Unlock()
	slices.Sort(keys)
	var page driver.ListPage
	seen := map[string]struct{}{}
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, opts.Prefix)
		if !ok {
			continue
		}
		if i := strings.Index(rest, opts.Delimiter); opts.Delimiter != "" && i >= 0 {
			dir := opts.Prefix + rest[:i+len(opts.Delimiter)]
			if _, dup := seen[dir]; dup {
				continue
			}
			seen[dir] = struct{}{}
			page.Objects = append(page.Objects, &driver.ListObject{Key: dir, IsDir: true})
			continue
		}
		page.Objects = append(page.Objects, &driver.ListObject{Key: k})
	}
	return &page, nil
}

func (d *factsDriver) NewRangeReader(ctx context.Context, key string, offset, length int64, _ *driver.ReaderOptions) (driver.Reader, error) {
	d.mu.Lock()
	err := d.fail[key]
	body, ok := d.objects[key]
	size := d.sizes[key]
	hold := d.hold[key]
	d.mu.Unlock()
	if hold {
		// A read that answers the context it was given, which is what a real
		// driver's does: a pass that cancelled the reads this failure left in
		// flight gets them back at once, and one that waits them out is what
		// the timeout below names.
		select {
		case <-ctx.Done():
			d.mu.Lock()
			d.cancelled++
			d.mu.Unlock()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			d.mu.Lock()
			d.gaveUp++
			d.mu.Unlock()
			return nil, errors.New("the read waited out a bucket that had already refused another")
		}
	}
	switch {
	case err != nil:
		return nil, err
	case !ok:
		return nil, errNoSuchObject
	}
	if size == 0 {
		size = int64(len(body))
	}
	d.mu.Lock()
	d.read++
	d.open++
	if d.open > d.peak {
		d.peak = d.open
	}
	widened := d.open >= d.want && !d.reached
	d.reached = d.reached || widened
	waiting := !d.reached && d.wait > 0
	d.mu.Unlock()
	switch {
	case widened:
		d.widthOnce.Do(func() { close(d.width) })
	case waiting:
		// A reader holds the bucket open until the pass has as many reads in
		// flight as the test declares, rather than for a fixed moment: what is
		// being measured is the pass's own width, and a loaded machine may take
		// a while to put a read on the CPU. A pass that widens past the
		// declared width closes d.width and every read goes through at once; a
		// pass at the declared width, which is the one under test, waits this
		// out, so the assertion below reads the width the pass settled at
		// rather than the scheduler's promptness.
		select {
		case <-d.width:
		case <-time.After(d.wait):
		}
	}
	d.mu.Lock()
	d.open--
	d.mu.Unlock()
	if offset > 0 && offset < int64(len(body)) {
		body = body[offset:]
	}
	if length >= 0 && length < int64(len(body)) {
		body = body[:length]
	}
	return &factsReader{Reader: bytes.NewReader(body), attrs: &driver.ReaderAttributes{
		ContentType: "application/json", Size: size,
	}}, nil
}

type factsReader struct {
	*bytes.Reader
	attrs *driver.ReaderAttributes
}

func (r *factsReader) Attributes() *driver.ReaderAttributes { return r.attrs }
func (r *factsReader) Close() error                         { return nil }
func (r *factsReader) As(any) bool                          { return false }

// factsRun is one run the two buckets hold.
type factsRun struct {
	slug, id string
	rec      runstore.Record
}

// factsBuckets writes runs into a plain bucket and hands back that bucket with
// a counting one holding the same objects byte for byte: the sequential
// reference reads one, the parallel pass the other, so a difference between
// the two results is the reads and never the data.
func factsBuckets(t *testing.T, runs []factsRun) (*blob.Bucket, *factsDriver) {
	t.Helper()
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	for _, r := range runs {
		putRecord(t, b, r.slug, r.id, r.rec)
	}
	d := newFactsDriver()
	for it := b.List(nil); ; {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := b.ReadAll(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		d.objects[obj.Key] = body
	}
	return b.Bucket, d
}

// sequentialFacts is Facts as it read before the reads were parallel: the same
// loop, one result.json at a time, against the same bucket. It is the
// reference the parallel pass has to equal, kept in the test so the comparison
// is with what the command did and not with itself. It lists a whole day back,
// the look-back the pass had before it was trimmed to mintSkewMargin.
func sequentialFacts(t *testing.T, b budget.BucketFacts, from, to int64) (map[int64][]budget.RunFact, map[string]string) {
	t.Helper()
	return sequentialFactsLookBack(t, b, from, to, 24*time.Hour)
}

// sequentialFactsLookBack is sequentialFacts with the listing's look-back
// under the test's control, for a test that compares two of them: the pass
// starts mintSkewMargin before the window's first day, where it used to start
// a whole day before it.
func sequentialFactsLookBack(t *testing.T, b budget.BucketFacts, from, to int64, lookBack time.Duration) (map[int64][]budget.RunFact, map[string]string) {
	t.Helper()
	ctx := context.Background()
	out := map[int64][]budget.RunFact{}
	repos := map[string]string{}
	slugs, err := runstore.ListSlugs(ctx, b.Bucket)
	if err != nil {
		t.Fatal(err)
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
	since := time.UnixMilli(from*86_400_000 - int64(lookBack/time.Millisecond)).UTC()
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, since)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			minted, err := runstore.RunTime(id)
			if err != nil || budget.Day(minted) > to {
				continue
			}
			rec, err := runstore.Open(b.Bucket, slug, id).ReadRecord(ctx)
			switch {
			case errors.Is(err, runstore.ErrNotFound):
				continue
			case errors.Is(err, runstore.ErrTooLarge) || (err != nil && strings.HasPrefix(err.Error(), "decoding result.json")):
				if b.Warn != nil {
					b.Warn(fmt.Sprintf("skipping %s/%s: its result.json cannot be used (%v)", slug, id, err))
				}
				continue
			case err != nil:
				t.Fatal(err)
			}
			start := rec.StartedAt
			if start.IsZero() {
				start = minted
			}
			day := budget.Day(start)
			if day < from || day > to {
				continue
			}
			f := budget.RunFact{Slug: slug, Run: id, StartDay: day}
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
	return out, repos
}

// TestFactsParallelEqualsSequential: the parallel pass returns what reading
// one at a time did, and it reads within factsWorkers at once. The bound is
// the half of that the facts cannot show: a pass with no bound returns the
// same days and opens one socket per run of the bucket's whole window.
// Mutations caught: reading one at a time (a peak of 1), and dropping the
// bound (a peak of every ref the fixture holds).
func TestFactsParallelEqualsSequential(t *testing.T) {
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	var runs []factsRun
	for _, slug := range []string{"acme-app", "acme-other", "acme-third"} {
		for d := int64(-1); d <= 2; d++ {
			at := time.UnixMilli((day + d) * 86_400_000).UTC()
			for i := range 12 {
				minted := at.Add(time.Duration(9+i) * time.Hour)
				rec := runstore.Record{Repo: "acme/" + slug}
				switch i % 4 {
				case 0:
					// Started the next day, and this far in the day it is the
					// day after the one the window ends on: it is read and
					// then dropped, which the two passes must agree on too.
					rec.StartedAt = minted.Add(5 * time.Hour)
				case 1:
					// No start time at all: the mint time stands in for it.
				default:
					rec.StartedAt = minted.Add(time.Duration(i) * time.Minute)
				}
				if i%3 == 0 {
					c := runstore.NewCost(1, float64(i)/4, runstore.BasisAPIList)
					rec.Cost = &c
				} else if i%3 == 1 {
					m := runstore.ModelOnlyCost(1, runstore.BasisAPIList)
					rec.Cost = &m
				}
				if i%2 == 0 {
					fin := rec.StartedAt.Add(time.Duration(i+1) * 30 * time.Minute)
					rec.FinishedAt = &fin
				}
				runs = append(runs, factsRun{slug: slug, id: minted.Format("20060102-150405") + fmt.Sprintf("-%04x", i), rec: rec})
			}
		}
	}
	plain, d := factsBuckets(t, runs)
	// factsWorkers is what the assertion below pins, and this package's tests
	// are external to the one that declares it: 32 is that number, written
	// once here so a pass that widens past it is caught. The driver holds a
	// read until one more than the bound is in flight, so a wide pass is
	// measured widening and a pass at the bound waits this out instead.
	const factsBound = 32
	d.want = factsBound + 1
	d.wait = 250 * time.Millisecond
	from, to := day-1, day+1

	var seqWarns []string
	want, wantRepos := sequentialFacts(t, budget.BucketFacts{Bucket: plain, Warn: func(s string) { seqWarns = append(seqWarns, s) }}, from, to)

	counted := blob.NewBucket(d)
	var mu sync.Mutex
	var parWarns []string
	got, gotRepos, err := budget.BucketFacts{Bucket: counted, Warn: func(s string) {
		mu.Lock()
		defer mu.Unlock()
		parWarns = append(parWarns, s)
	}}.Facts(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if peak := d.peakOpen(); peak <= 1 || peak > factsBound {
		t.Fatalf("result.json was read %d at a time, want more than one and at most %d (factsWorkers): the pass reads them in parallel and within its bound", peak, factsBound)
	}
	if len(want) != 3 || len(want[from]) == 0 || len(want[day]) == 0 || len(want[to]) == 0 {
		t.Fatalf("the fixture put %d days of facts in the result: this test would compare too little", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the parallel pass differs from reading one at a time:\n%+v\nwant\n%+v", got, want)
	}
	if !reflect.DeepEqual(gotRepos, wantRepos) {
		t.Fatalf("repos = %v, want %v", gotRepos, wantRepos)
	}
	if len(parWarns) != len(seqWarns) {
		t.Fatalf("warnings = %v, want %v", parWarns, seqWarns)
	}
}

func TestFactsCorruptOversizedMissingRecordSameAsBefore(t *testing.T) {
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	start := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	fin := start.Add(time.Hour)
	c := runstore.NewCost(1, 0.25, runstore.BasisAPIList)
	_, d := factsBuckets(t, []factsRun{{
		slug: "acme-app", id: "20261018-100000-aaaa",
		rec: runstore.Record{Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c},
	}})
	// The three records that cannot be used, beside the good one: a
	// result.json that is not JSON, one the bucket reports past the cap, and a
	// run whose directory holds no result.json at all.
	d.objects["runs/acme-app/20261018-110000-bbbb/result.json"] = []byte("{nope")
	d.objects["runs/acme-app/20261018-120000-cccc/result.json"] = []byte(`{"version":1}`)
	d.sizes["runs/acme-app/20261018-120000-cccc/result.json"] = runstore.MaxRecordBytes + 1
	d.objects["runs/acme-app/20261018-130000-dddd/task.json"] = []byte("{}")

	var warns []string
	f := budget.BucketFacts{Bucket: blob.NewBucket(d), Warn: func(s string) { warns = append(warns, s) }}
	facts, _, err := f.Facts(context.Background(), day, day)
	if err != nil {
		t.Fatal(err)
	}
	want := []budget.RunFact{{Slug: "acme-app", Run: "20261018-100000-aaaa", StartDay: day, ComputeUSD: 0.25, ComputeEstimated: true, Hours: 1}}
	if !reflect.DeepEqual(facts[day], want) {
		t.Fatalf("facts[day] = %+v, want only the record that can be read", facts[day])
	}
	// Newest first, which is the order the runs are read in: cccc before bbbb.
	if len(warns) != 2 || !strings.Contains(warns[0], "cccc") || !strings.Contains(warns[1], "bbbb") {
		t.Fatalf("warnings = %v, want the oversized record then the corrupt one", warns)
	}
	if strings.Contains(strings.Join(warns, "\n"), "dddd") {
		t.Errorf("a run with no result.json warned: %v", warns)
	}
	seq, _ := sequentialFacts(t, budget.BucketFacts{Bucket: blob.NewBucket(d)}, day, day)
	if !reflect.DeepEqual(facts, seq) {
		t.Fatalf("the parallel pass differs from reading one at a time:\n%+v\nwant\n%+v", facts, seq)
	}
}

func TestFactsFailedReadFailsThePass(t *testing.T) {
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	start := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	c := runstore.NewCost(1, 0.25, runstore.BasisAPIList)
	rec := runstore.Record{Repo: "acme/app", StartedAt: start, Cost: &c}
	_, d := factsBuckets(t, []factsRun{
		{slug: "acme-app", id: "20261018-100000-aaaa", rec: rec},
		{slug: "acme-app", id: "20261018-120000-bbbb", rec: rec},
	})
	refused := errors.New("the runs bucket refused the read")
	d.fail["runs/acme-app/20261018-100000-aaaa/result.json"] = refused
	d.fail["runs/acme-app/20261018-120000-bbbb/result.json"] = refused

	facts, repos, err := budget.BucketFacts{Bucket: blob.NewBucket(d)}.Facts(context.Background(), day, day)
	if err == nil {
		t.Fatal("a bucket read that failed did not fail the pass: a day must never be written from a bucket that could not be read")
	}
	if facts != nil || repos != nil {
		t.Errorf("a failed pass returned facts %v and repos %v", facts, repos)
	}
	// The runs are read newest first, so bbbb is the first failure and its
	// message is the one the caller sees, whatever order the reads finished
	// in.
	if !strings.Contains(err.Error(), "20261018-120000-bbbb/result.json") || strings.Contains(err.Error(), "aaaa") {
		t.Fatalf("error = %v, want the first failure in read order", err)
	}
}

// TestFactsCancelsTheReadsAFailureLeavesInFlight: a record read the bucket
// refused ends the pass then, rather than after every read the pass had in
// flight has come back. The driver's reads answer the context they are given,
// as a real one's do, so a pass that waited them out is the one that stalls on
// the first refusal of a window that has hundreds of runs in it.
// Mutation caught: reading without the pass's own cancel, which leaves every
// other read to its own timeout.
func TestFactsCancelsTheReadsAFailureLeavesInFlight(t *testing.T) {
	day := budget.Day(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	const runs = 8
	var seeds []factsRun
	for i := range runs {
		// Newest first, so the first read the pass makes is the one that fails.
		seeds = append(seeds, factsRun{
			slug: "acme-app", id: fmt.Sprintf("20261018-%02d0000-%04x", 17-i, i),
			rec: runstore.Record{Repo: "acme/app", StartedAt: time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)},
		})
	}
	_, d := factsBuckets(t, seeds)
	refused := errors.New("the runs bucket refused the read")
	d.fail["runs/acme-app/"+seeds[0].id+"/result.json"] = refused
	for _, s := range seeds[1:] {
		d.hold["runs/acme-app/"+s.id+"/result.json"] = true
	}

	facts, repos, err := budget.BucketFacts{Bucket: blob.NewBucket(d)}.Facts(context.Background(), day, day)
	if !errors.Is(err, refused) {
		t.Fatalf("error = %v, want the read the bucket refused", err)
	}
	if facts != nil || repos != nil {
		t.Errorf("a failed pass returned facts %v and repos %v", facts, repos)
	}
	d.mu.Lock()
	cancelled, gaveUp := d.cancelled, d.gaveUp
	d.mu.Unlock()
	if cancelled != runs-1 {
		t.Errorf("%d of the %d reads the failure left in flight were cancelled, want all of them", cancelled, runs-1)
	}
	if gaveUp != 0 {
		t.Errorf("%d reads waited out a bucket that had already refused one: the pass ends when the read that failed does", gaveUp)
	}
}

// TestFactsLookBackIsMinutesNotADay: the pass lists a run by its mint time, so
// how far before the window's first day it reaches back is the gap between a
// run's mint and its record — minutes. A look-back of the whole day before the
// window, which is what the pass did, listed every run minted that day and the
// day filter dropped every one of them that did not start inside the window:
// on a report that discarded a quarter of its reads.
// Mutation caught: listing from the day before the window, which reads the
// record minted an hour before it below.
func TestFactsLookBackIsMinutesNotADay(t *testing.T) {
	day := budget.Day(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	// Minted an hour before the window's first day, started on the day before
	// it: the old look-back read this record and the day filter dropped it.
	far := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	// Minted a minute before the window's first day, started ten seconds into
	// it: this is the run the look-back exists for.
	near := time.Date(2026, 10, 18, 23, 59, 0, 0, time.UTC)
	farID, nearID := far.Format("20060102-150405")+"-aaaa", near.Format("20060102-150405")+"-bbbb"
	_, d := factsBuckets(t, []factsRun{
		{slug: "acme-app", id: farID, rec: runstore.Record{Repo: "acme/app", StartedAt: far.Add(20 * time.Minute)}},
		{slug: "acme-app", id: nearID, rec: runstore.Record{Repo: "acme/app", StartedAt: time.Date(2026, 10, 19, 0, 0, 10, 0, time.UTC)}},
	})
	facts, _, err := budget.BucketFacts{Bucket: blob.NewBucket(d)}.Facts(context.Background(), day, day)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.reads(); got != 1 {
		t.Errorf("the pass read %d records: the run minted an hour before the window was read only to be dropped by the day filter, so the look-back is minutes and not the day before", got)
	}
	want := []budget.RunFact{{Slug: "acme-app", Run: nearID, StartDay: day}}
	if !reflect.DeepEqual(facts[day], want) {
		t.Fatalf("facts[day] = %+v, want %+v", facts[day], want)
	}
}

// TestFactsRunMintedJustBeforeMidnightCountedInItsDay: the run's own day is
// the day its record started on, so a run minted seconds before midnight
// belongs to the day after its ID's — and the mint-skew look-back is what
// reaches it at all, since its ID is minted before the window opens.
func TestFactsRunMintedJustBeforeMidnightCountedInItsDay(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	// Minted 23:59:50 the day before, started ten seconds into the day.
	putRecord(t, b, "acme-app", "20261018-235950-aaaa", runstore.Record{
		Repo: "acme/app", StartedAt: time.Date(2026, 10, 19, 0, 0, 10, 0, time.UTC),
	})
	// Minted 23:59:50 on the day and never started: the mint time stands in
	// for a start, so it counts on the day it was minted.
	putRecord(t, b, "acme-app", "20261019-235950-bbbb", runstore.Record{Repo: "acme/app"})

	facts, _, err := budget.BucketFacts{Bucket: b.Bucket}.Facts(context.Background(), day, day)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range facts[day] {
		got = append(got, f.Run)
	}
	if want := []string{"20261019-235950-bbbb", "20261018-235950-aaaa"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("facts[day] = %v, want %v", got, want)
	}
	if len(facts[day-1]) != 0 {
		t.Errorf("facts[day-1] = %+v: the run minted the day before started on the day", facts[day-1])
	}
}

// TestFactsRunMintedJustAfterMidnightCountedNextDay: the other side of the same
// midnight. The window opens a day before its first live day, and the run
// minted seconds after the next midnight — the ID's own day is the window's
// next day — counts on that day, the day its record started on, which the
// minutes look-back reaches without any margin at all.
func TestFactsRunMintedJustAfterMidnightCountedNextDay(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)) // the first live day
	// Minted ten seconds into the window's first day, started ten seconds
	// later: the ID's day is the day it counts on.
	putRecord(t, b, "acme-app", "20261018-000010-aaaa", runstore.Record{Repo: "acme/app",
		StartedAt: time.Date(2026, 10, 18, 0, 0, 20, 0, time.UTC)})
	// Minted ten seconds into the window's next day, started ten seconds
	// later: the day the window is about, and the one the run counts on.
	putRecord(t, b, "acme-app", "20261019-000010-bbbb", runstore.Record{Repo: "acme/app",
		StartedAt: time.Date(2026, 10, 19, 0, 0, 20, 0, time.UTC)})
	// Minted at the far end of the day and never started: its mint day is the
	// day it counts on, and a day past the window is still dropped.
	putRecord(t, b, "acme-app", "20261020-235950-cccc", runstore.Record{Repo: "acme/app"})

	facts, _, err := budget.BucketFacts{Bucket: b.Bucket}.Facts(context.Background(), day-1, day)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range facts[day] {
		got = append(got, f.Run)
	}
	if want := []string{"20261019-000010-bbbb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("facts[day] = %v, want %v", got, want)
	}
	if len(facts[day-1]) != 1 || facts[day-1][0].Run != "20261018-000010-aaaa" {
		t.Errorf("facts[day-1] = %+v: the run minted just after the window's midnight counts on the day it started, not on the day after it", facts[day-1])
	}
}

// TestFactsSkewedRecordWithinMarginCounted: the ID is minted by the launcher's
// clock and the record's started_at written by the container's, so the two
// stamps disagree by the skew between them. A run whose two stamps sit on
// either side of midnight by less than the margin is counted on the day its
// record names, whichever side of its mint that is: attribution is the
// record's, and the margin is what keeps the run listed.
func TestFactsSkewedRecordWithinMarginCounted(t *testing.T) {
	b := blobx.Wrap(memblob.OpenBucket(nil))
	day := budget.Day(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	// The launcher's clock ahead: the ID says the day, the record says the day
	// before it.
	putRecord(t, b, "acme-app", "20261019-000005-aaaa", runstore.Record{Repo: "acme/app",
		StartedAt: time.Date(2026, 10, 18, 23, 59, 40, 0, time.UTC)})
	// The launcher's clock behind: the ID says the day before, the record says
	// the day.
	putRecord(t, b, "acme-app", "20261018-235940-bbbb", runstore.Record{Repo: "acme/app",
		StartedAt: time.Date(2026, 10, 19, 0, 0, 5, 0, time.UTC)})

	facts, _, err := budget.BucketFacts{Bucket: b.Bucket}.Facts(context.Background(), day-1, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts[day-1]) != 1 || facts[day-1][0].Run != "20261019-000005-aaaa" {
		t.Errorf("facts[day-1] = %+v, want the run whose record is on the day before, not the one whose ID is", facts[day-1])
	}
	if len(facts[day]) != 1 || facts[day][0].Run != "20261018-235940-bbbb" {
		t.Errorf("facts[day] = %+v, want the run whose record is on the day", facts[day])
	}
}

// TestFactsFinishedDaysUnchangedByTheShorterLookBack: a report's finished days
// are the days before it, and their rows are what they were when the pass
// listed a whole day back — the same runs, in the same order, on the same
// days, with the same repository names. The runs minted on the day before the
// window, which the day filter dropped then, are the reads this change
// removes: the reference reads them and the pass does not.
func TestFactsFinishedDaysUnchangedByTheShorterLookBack(t *testing.T) {
	windowLast := budget.Day(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	from, to := windowLast-4, windowLast
	var runs []factsRun
	for d := from - 1; d <= to+1; d++ {
		midnight := time.UnixMilli(d * 86_400_000).UTC()
		for i := range 3 {
			start := midnight.Add(time.Duration(8+4*i) * time.Hour)
			minted := start.Add(-time.Duration(3+i) * time.Minute)
			c := runstore.NewCost(1, float64(i+1)/4, runstore.BasisAPIList)
			fin := start.Add(time.Hour)
			runs = append(runs, factsRun{slug: "acme-app", id: minted.Format("20060102-150405") + fmt.Sprintf("-%04x", i),
				rec: runstore.Record{Repo: "acme/app", StartedAt: start, FinishedAt: &fin, Cost: &c}})
		}
	}
	// Minted seconds before the window opens and started seconds into it: the
	// run the look-back exists for, on the one night the two rules differ.
	boundaryMint := time.UnixMilli(from * 86_400_000).UTC().Add(-10 * time.Second)
	runs = append(runs, factsRun{slug: "acme-app", id: boundaryMint.Format("20060102-150405") + "-fe01",
		rec: runstore.Record{Repo: "acme/app", StartedAt: time.UnixMilli(from * 86_400_000).UTC().Add(10 * time.Second)}})

	_, d := factsBuckets(t, runs)
	// The reference is the pass as it read before this change: the same walk,
	// listing a whole day before the window.
	want, wantRepos := sequentialFactsLookBack(t, budget.BucketFacts{Bucket: blob.NewBucket(d)}, from, to, 24*time.Hour)
	refReads := d.reads()
	if len(want) != 5 || len(want[from]) != 4 {
		t.Fatalf("the fixture put %d days of facts in the result (%d runs on the first): this test would compare too little", len(want), len(want[from]))
	}
	got, gotRepos, err := budget.BucketFacts{Bucket: blob.NewBucket(d)}.Facts(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if parReads := d.reads() - refReads; parReads >= refReads {
		t.Errorf("the pass read %d records where the look-back of a whole day read %d: the day before the window was read only to be dropped", parReads, refReads)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the finished days differ from what the day-long look-back returned:\n%+v\nwant\n%+v", got, want)
	}
	if !reflect.DeepEqual(gotRepos, wantRepos) {
		t.Fatalf("repos = %v, want %v", gotRepos, wantRepos)
	}
}
