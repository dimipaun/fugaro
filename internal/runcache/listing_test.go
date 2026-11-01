package runcache

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The listings these tests read are a fake GCS bucket rather than a driver
// of this package's own: a generation is what a listing has to report and
// what only a GCS-shaped answer carries, so the fake is the smallest thing
// that can be wrong in the way this package guards against.

const (
	testSlug   = "app"
	testBucket = "runs"
)

// testRunIDs are three run IDs, oldest first, from outside any listing
// window a caller would default to.
var testRunIDs = []string{"20261018-090000-0000", "20261018-090100-0001", "20261018-090200-0002"}

// testBucketWith seeds obj's runs into a fake GCS bucket: a task.json and a
// result.json each, the arrangement a run that has not finalized has, plus
// whatever else the test adds.
func testBucketWith(t *testing.T, ids ...string) (*gcpfake.GCS, *blobx.Bucket) {
	t.Helper()
	g := gcpfake.NewGCS(t)
	for _, id := range ids {
		s := runstore.Open(nil, testSlug, id)
		g.Put(testBucket, s.Prefix()+"task.json", []byte(`{"version":1,"run_id":"`+id+`"}`))
		g.Put(testBucket, s.Prefix()+"result.json", []byte(`{"version":1,"run_id":"`+id+`"}`))
	}
	return g, g.Bucket(t, testBucket)
}

// objectKey is the object name of one run's one object.
func objectKey(id, name string) string { return runstore.Open(nil, testSlug, id).Prefix() + name }

// TestListCarriesEveryCachedObjectsGeneration: one listing of a repository
// is where the cache's generations come from, so it has to name every one of
// the three objects a run listing reads, at the generation the bucket holds
// them at -- and a rewritten object has to arrive at a generation of its
// own, since that is the whole of what makes a stale entry safe.
func TestListCarriesEveryCachedObjectsGeneration(t *testing.T) {
	ctx := context.Background()
	g, bucket := testBucketWith(t, testRunIDs...)
	g.Put(testBucket, objectKey(testRunIDs[0], "launch.json"), []byte(`{"version":1}`))

	index := List(ctx, bucket, testSlug, "")
	want := []string{
		objectKey(testRunIDs[0], "result.json"), objectKey(testRunIDs[0], "task.json"),
		objectKey(testRunIDs[0], "launch.json"),
		objectKey(testRunIDs[1], "result.json"), objectKey(testRunIDs[1], "task.json"),
		objectKey(testRunIDs[2], "result.json"), objectKey(testRunIDs[2], "task.json"),
	}
	slices.Sort(want)
	got := slices.Sorted(maps.Keys(index))
	if !slices.Equal(got, want) {
		t.Fatalf("the listing named %v, want the three objects of every run %v", got, want)
	}
	for name, gen := range index {
		if gen == 0 {
			t.Errorf("the listing named %s at generation 0: a zero generation would be answered from whatever the cache holds", name)
		}
	}

	before := index[objectKey(testRunIDs[2], "result.json")]
	g.Put(testBucket, objectKey(testRunIDs[2], "result.json"), []byte(`{"version":1,"status":"failed"}`))
	after := List(ctx, bucket, testSlug, "")[objectKey(testRunIDs[2], "result.json")]
	if after == before {
		t.Errorf("a rewritten object is still at generation %d: a cache keyed on that would serve the status the run no longer has", after)
	}
}

// TestListLeavesOutEverythingElseInTheBucket: the index answers for the
// three objects a listing reads and for nothing else. The claim, the cancel
// marker and anything a future release adds are read on paths that ask the
// bucket -- and a name the index does not carry is a name the reader reads
// from the bucket, never one it calls absent.
func TestListLeavesOutEverythingElseInTheBucket(t *testing.T) {
	ctx := context.Background()
	g, bucket := testBucketWith(t, testRunIDs[0])
	g.Put(testBucket, runstore.Open(nil, testSlug, testRunIDs[0]).ClaimKey(), []byte(`{"holder":"person-1"}`))
	g.Put(testBucket, runstore.Open(nil, testSlug, testRunIDs[0]).Prefix()+"cancel", []byte("2026-10-18T09:00:00Z"))
	g.Put(testBucket, objectKey(testRunIDs[1], "task.json"), []byte(`{"version":1}`))
	// Another repository's runs are under another prefix.
	other := runstore.Open(nil, "other-repo", testRunIDs[0])
	g.Put(testBucket, other.Prefix()+"task.json", []byte(`{"version":1}`))

	index := List(ctx, bucket, testSlug, "")
	for name := range index {
		rest, _ := strings.CutPrefix(name, "runs/"+testSlug+"/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || !cachedNames[parts[1]] {
			t.Errorf("the listing carried %s, which is not one of the three objects a listing caches", name)
		}
	}
	if slices.Contains(slices.Sorted(maps.Keys(index)), other.Prefix()+"task.json") {
		t.Error("the listing of one repository carried another repository's runs")
	}
}

// TestListStartsAtTheOldestRunTheCallerMeansToRead: the offset a listing is
// asked for is a run ID, and a run that sorts before it costs nothing to
// skip. The runs before the offset are the ones the caller has already
// decided not to read, so leaving them out of the index costs no read -- and
// including them would cost the listing the whole history of the repository.
func TestListStartsAtTheOldestRunTheCallerMeansToRead(t *testing.T) {
	ctx := context.Background()
	_, bucket := testBucketWith(t, testRunIDs...)

	index := List(ctx, bucket, testSlug, testRunIDs[1])
	if slices.Contains(slices.Sorted(maps.Keys(index)), objectKey(testRunIDs[0], "task.json")) {
		t.Errorf("a listing from %s carried %s, which is older", testRunIDs[1], testRunIDs[0])
	}
	for _, id := range testRunIDs[1:] {
		if _, ok := index[objectKey(id, "task.json")]; !ok {
			t.Errorf("a listing from %s left out %s, which is at or after it", testRunIDs[1], id)
		}
	}
}

// TestListIsNoIndexForABucketThatReportsNoGeneration: only GCS answers a
// listing with a generation per object. A local or in-memory bucket does
// not, and the answer there is no index at all -- the caller reads every
// object, exactly as it did before this package existed -- rather than an
// index whose entries are all at generation 0.
func TestListIsNoIndexForABucketThatReportsNoGeneration(t *testing.T) {
	ctx := context.Background()
	b, ids := memBucketWith(t, 2)
	if index := List(ctx, b, testSlug, ""); index != nil {
		t.Errorf("a bucket that reports no generation produced %v, want no index", index)
	}
	if index := List(ctx, nil, testSlug, ""); index != nil {
		t.Errorf("a listing of no bucket produced %v, want no index", index)
	}
	// An index that names no object is the same answer: a caller whose own
	// listing of the same window found runs would read every one of them as
	// absent and draw an empty listing, which is worse than reading the
	// bucket.
	empty := gcpfake.NewGCS(t).Bucket(t, testBucket)
	if index := List(ctx, empty, testSlug, ""); index != nil {
		t.Errorf("a listing that found nothing produced %v, want no index", index)
	}
	// The objects are there either way: it is the generations that are not.
	if _, err := b.Exists(ctx, objectKey(ids[0], "task.json")); err != nil {
		t.Fatal(err)
	}
}

// memBucketWith is the same fixture on an in-memory bucket.
func memBucketWith(t *testing.T, n int) (*blobx.Bucket, []string) {
	t.Helper()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	ids := testRunIDs[:n]
	for _, id := range ids {
		if err := runstore.Open(b.Bucket, testSlug, id).PutFile(context.Background(), "task.json", []byte(`{"version":1}`), "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	return b, ids
}

// TestReaderServesAnUnchangedObjectWithoutARead is what the whole cache is
// for, and it is measured rather than timed: once the object is cached, the
// fake is told to fail the next read of it, and the reader answers anyway.
func TestReaderServesAnUnchangedObjectWithoutARead(t *testing.T) {
	ctx := context.Background()
	id := testRunIDs[0]
	key := objectKey(id, "result.json")
	g, bucket := testBucketWith(t, id)
	want := []byte(`{"version":1,"run_id":"` + id + `"}`)
	c, _ := testCache(t, t.TempDir(), "aurora", testBucket)
	s := runstore.Open(bucket.Bucket, testSlug, id)
	rd := Reader{Cache: c, Index: List(ctx, bucket, testSlug, id)}

	if got, err := rd.Read(ctx, s, "result.json"); err != nil || string(got) != string(want) {
		t.Fatalf("the first read = %q, %v; want the object", got, err)
	}

	// The next read of this object fails at the bucket. The cached read
	// above is the only one that was allowed to happen.
	g.FailObjectGetsAfter(testBucket, key, 0)
	if got, err := rd.Read(ctx, s, "result.json"); err != nil || string(got) != string(want) {
		t.Errorf("the second read = %q, %v; want the cached bytes: an unchanged object is answered from the cache", got, err)
	}
	// And the same read without the cache does reach the bucket: blobx
	// answers a refused read as an absent object, so the decision path sees
	// nothing rather than a status it did not read.
	got, err := Reader{}.Read(ctx, s, "result.json")
	if err != nil || got != nil {
		t.Errorf("the uncached read = %q, %v; want the refused read to reach the bucket and read as absent", got, err)
	}
}

// TestAChangedGenerationIsReadAgain: the runner rewrites result.json as a
// run progresses, so the object a listing reads a second time is very often
// not the object it read the first time. A generation the cache does not
// hold is a miss, and the read that follows is what the cache is brought to.
func TestAChangedGenerationIsReadAgain(t *testing.T) {
	ctx := context.Background()
	id := testRunIDs[0]
	key := objectKey(id, "result.json")
	g, bucket := testBucketWith(t, id)
	c, _ := testCache(t, t.TempDir(), "aurora", testBucket)
	s := runstore.Open(bucket.Bucket, testSlug, id)

	index := List(ctx, bucket, testSlug, id)
	if _, err := (Reader{Cache: c, Index: index}).Read(ctx, s, "result.json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(key, index[key]); !ok {
		t.Fatalf("the read left nothing cached for %s at generation %d", key, index[key])
	}

	// The run writes its record again: a new generation, and the entry the
	// first read left no longer answers for it.
	rec := []byte(`{"version":1,"run_id":"` + id + `","status":"failed"}`)
	g.Put(testBucket, key, rec)
	rd := Reader{Cache: c, Index: List(ctx, bucket, testSlug, id)}
	if rd.Index[key] == index[key] {
		t.Fatal("the rewrite did not change the generation, so the test proves nothing")
	}
	got, err := rd.Read(ctx, s, "result.json")
	if err != nil || !bytes.Equal(got, rec) {
		t.Fatalf("the read after a rewrite = %q, %v; want the record the bucket holds now", got, err)
	}
	if _, ok := c.Get(key, index[key]); ok {
		t.Error("the cache still answers at the generation the rewrite replaced: it holds one answer per object, not a history")
	}
}

// TestReaderIsNeverTrustedForADecision: the zero Reader reads the bucket
// whatever is in the cache, which is what every caller that is not listing
// for display passes -- a launch, a gate, a budget read. A cache that
// answered one of those would decide on a snapshot of a listing that has
// already run.
func TestReaderIsNeverTrustedForADecision(t *testing.T) {
	ctx := context.Background()
	id := testRunIDs[0]
	_, bucket := testBucketWith(t, id)
	c, _ := testCache(t, t.TempDir(), "aurora", testBucket)
	s := runstore.Open(bucket.Bucket, testSlug, id)

	// The cache holds a stale answer for the object, at a generation the
	// listing no longer reports, and a zero Reader must not touch it.
	c.Put(objectKey(id, "result.json"), 1, []byte(`{"version":1,"status":"running"}`))
	if got, err := (Reader{}).Read(ctx, s, "result.json"); err != nil || strings.Contains(string(got), "running") {
		t.Fatalf("a zero Reader answered %q, %v; want the bucket's own bytes", got, err)
	}
	// The cache is still a cache: an indexed reader ignores the same stale
	// entry and reads the bucket.
	rd := Reader{Cache: c, Index: List(ctx, bucket, testSlug, id)}
	if got, err := rd.Read(ctx, s, "result.json"); err != nil || strings.Contains(string(got), "running") {
		t.Fatalf("an indexed reader answered the stale entry %q, %v", got, err)
	}

	// An object the listing did not see is absent without a read: this run
	// was written but never launched, which is the case the bucket cannot
	// answer cheaply and the listing can.
	if got, err := rd.Read(ctx, s, "launch.json"); err != nil || got != nil {
		t.Errorf("a run that never launched read as %q, %v", got, err)
	}
}
