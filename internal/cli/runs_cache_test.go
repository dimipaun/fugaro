package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// ls reads every run's three objects on every call, and a listing covers the
// same bucket again and again. The local run cache is keyed by object name and
// generation: one listing of the runs bucket carries every object's
// generation, and an object the listing shows at the generation the cache
// already holds is not read a second time.
//
// The bucket these tests read through reports a generation per object, as GCS
// does and as file:// does not, and counts every read that reached it: what
// the cache saves is measured rather than asserted from timings.

// genBucketScheme is the URL scheme the generation bucket is opened with. The
// tests in this package do not run in parallel, so one package-level slot is
// enough (lsLimitBucket's reason).
const genBucketScheme = "genbucket"

var genBucket *genDriver

func init() { blob.DefaultURLMux().RegisterBucket(genBucketScheme, genBucketOpener{}) }

type genBucketOpener struct{}

func (genBucketOpener) OpenBucketURL(context.Context, *url.URL) (*blob.Bucket, error) {
	if genBucket == nil {
		return nil, fmt.Errorf("%s:// opened with no generation bucket installed", genBucketScheme)
	}
	return blob.NewBucket(genBucket), nil
}

// genDriver is a runs bucket whose listings carry each object's generation and
// whose object reads are counted: the objects themselves, the generation each
// one is stored at, and every key a read reached.
type genDriver struct {
	stubBucket
	mu      sync.Mutex
	objects map[string][]byte
	gens    map[string]int64
	reads   []string
}

func (d *genDriver) ErrorCode(err error) gcerrors.ErrorCode {
	if errors.Is(err, errNoObject) {
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}

// ListPaged answers both shapes a listing of a run window takes: the delimited
// one ListRunIDs gathers a slug's run IDs with, and the flat one the cache's
// generations come from, whose entries carry their generation the way a GCS
// listing does. It honours opts.BeforeList's start offset as GCS does, so a
// listing that means to skip older runs is seen skipping them.
func (d *genDriver) ListPaged(_ context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	q := &storage.Query{}
	if opts.BeforeList != nil {
		asFunc := func(i any) bool {
			p, ok := i.(**storage.Query)
			if !ok {
				return false
			}
			*p = q
			return true
		}
		if err := opts.BeforeList(asFunc); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := slices.Sorted(maps.Keys(d.objects))
	var page driver.ListPage
	seen := map[string]struct{}{}
	for _, k := range keys {
		if k < q.StartOffset {
			continue
		}
		rest, ok := strings.CutPrefix(k, opts.Prefix)
		if !ok {
			continue
		}
		if opts.Delimiter != "" {
			i := strings.Index(rest, opts.Delimiter)
			if i < 0 {
				continue
			}
			dir := opts.Prefix + rest[:i+len(opts.Delimiter)]
			if _, dup := seen[dir]; dup {
				continue
			}
			seen[dir] = struct{}{}
			page.Objects = append(page.Objects, &driver.ListObject{Key: dir, IsDir: true})
			continue
		}
		o := &driver.ListObject{Key: k, ModTime: time.Unix(0, d.gens[k]), Size: int64(len(d.objects[k]))}
		o.AsFunc = func(i any) bool {
			p, ok := i.(*storage.ObjectAttrs)
			if !ok {
				return false
			}
			*p = storage.ObjectAttrs{Name: k, Generation: d.gens[k], Size: int64(len(d.objects[k]))}
			return true
		}
		page.Objects = append(page.Objects, o)
	}
	return &page, nil
}

func (d *genDriver) NewRangeReader(_ context.Context, key string, offset, length int64, _ *driver.ReaderOptions) (driver.Reader, error) {
	d.mu.Lock()
	d.reads = append(d.reads, key)
	body, ok := d.objects[key]
	d.mu.Unlock()
	if !ok {
		return nil, errNoObject
	}
	if offset > 0 && offset < int64(len(body)) {
		body = body[offset:]
	}
	if length >= 0 && length < int64(len(body)) {
		body = body[:length]
	}
	return &genReader{Reader: bytes.NewReader(body)}, nil
}

// genReader serves one object's bytes.
type genReader struct{ *bytes.Reader }

func (r *genReader) Close() error { return nil }
func (r *genReader) As(any) bool  { return false }
func (r *genReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{ContentType: "application/json", Size: r.Size()}
}

// set stores body at key, at the generation after the one it held, and answers
// the new generation: a rewrite is a new generation, which is what the cache
// keys on.
func (d *genDriver) set(key string, body []byte) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gens[key]++
	d.objects[key] = body
	return d.gens[key]
}

// keysRead is every object key a read reached since the last forget.
func (d *genDriver) keysRead() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.reads)
}

func (d *genDriver) forget() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads = nil
}

// genRuns seeds n runs -- a task.json and a result.json each, as lsBucketRuns
// does -- into a bucket whose listings report generations.
func genRuns(t *testing.T, n int) (*genDriver, []string) {
	t.Helper()
	b, ids := lsBucketRuns(t, n)
	return genObjects(t, b.Bucket), ids
}

// genObjects is a counting generation bucket holding b's objects byte for
// byte, so what a test's command reads is runstore's own objects and never a
// second fixture's copy of them.
func genObjects(t *testing.T, b *blob.Bucket) *genDriver {
	t.Helper()
	d := &genDriver{objects: map[string][]byte{}, gens: map[string]int64{}}
	ctx := context.Background()
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
		d.set(obj.Key, body)
	}
	return d
}

// installGenBucket serves d from the genbucket:// URL and points the
// fixture's config at it, so the real command drives the bucket whose reads
// the test counts.
func installGenBucket(t *testing.T, f *cloudFixture, d *genDriver) {
	t.Helper()
	genBucket = d
	t.Cleanup(func() { genBucket = nil })
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(f.bucket)) {
		t.Fatalf("%s does not name the fixture's runs bucket %q", path, f.bucket)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(data, []byte(f.bucket), []byte(genBucketScheme+"://runs")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// genObjectsOf is genObjects over the runs the fixture already holds, however
// they were seeded: the same reader then reads a bucket whose listings carry
// generations, which is what lets a warmed cache answer it if anything
// consults it.
func genObjectsOf(t *testing.T, f *cloudFixture) *genDriver {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return genObjects(t, b)
}

// resultKey is the object name of one run's result.json.
func resultKey(t *testing.T, id string) string {
	t.Helper()
	return runstore.Open(nil, appSlug, id).Prefix() + "result.json"
}

// TestRunsLsSecondCallReadsOnlyChangedGenerations: a listing of the same
// window reads nothing the second time when no object changed, and only the
// objects whose generation changed when one did -- a rewritten result.json
// above all, which the runner writes again and again during a run, so a cache
// that assumed the objects never change would serve a run's earlier status
// forever.
func TestRunsLsSecondCallReadsOnlyChangedGenerations(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	d, ids := genRuns(t, 3)
	env := envOn(t, f, blobx.Wrap(blob.NewBucket(d)))
	list := func(cached bool) []string {
		t.Helper()
		rows, err := loadRows(ctx, env, lsFilter{slugs: []string{appSlug}, noCache: !cached}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, r.RunID+" "+r.Status)
		}
		return got
	}

	first := list(true)
	if len(first) != len(ids) {
		t.Fatalf("the first listing read %d runs, want %d", len(first), len(ids))
	}
	if got := d.keysRead(); len(got) == 0 {
		t.Fatal("the first listing read no object at all: the fixture is not being read")
	}
	d.forget()
	if got := list(true); !slices.Equal(got, first) {
		t.Fatalf("the second listing answered %v, want %v: the rows are the same whichever read served them", got, first)
	}
	if got := d.keysRead(); len(got) != 0 {
		t.Errorf("the second listing read %v: every object of the window is unchanged, so the cache answers all of them", got)
	}

	key := resultKey(t, ids[0])
	rec, err := json.Marshal(&runstore.Record{Version: 1, RunID: ids[0], Repo: "acme/app", Workflow: "web", Status: runstore.StatusFailed})
	if err != nil {
		t.Fatal(err)
	}
	d.set(key, rec)
	d.forget()
	rows := list(true)
	if got := d.keysRead(); !slices.Equal(got, []string{key}) {
		t.Errorf("the third listing read %v, want only %s: the rewritten record is a new generation and its own run's objects are unchanged", got, key)
	}
	if !slices.Contains(rows, ids[0]+" "+string(runstore.StatusFailed)) {
		t.Errorf("the third listing answered %v, want run %s failed: the record the bucket now holds is the one a listing shows", rows, ids[0])
	}
}

// TestRunsLsNoCacheReadsEveryObject: the cache changes only speed. A listing
// that was not given it reads every object of the window again -- the warm
// cache of the listing before it is never consulted -- and answers the same
// rows. That is what --no-cache asks for, and what a caller that deletes the
// cache directory gets.
func TestRunsLsNoCacheReadsEveryObject(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	d, ids := genRuns(t, 3)
	env := envOn(t, f, blobx.Wrap(blob.NewBucket(d)))
	list := func(cached bool) []string {
		t.Helper()
		rows, err := loadRows(ctx, env, lsFilter{slugs: []string{appSlug}, noCache: !cached}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, r.RunID+" "+r.Status)
		}
		return got
	}

	warm := list(true)
	d.forget()
	if got := list(false); !slices.Equal(got, warm) {
		t.Fatalf("the uncached listing answered %v, want %v: a cache is speed, never an answer", got, warm)
	}
	read := d.keysRead()
	for _, id := range ids {
		for _, name := range []string{"task.json", "result.json"} {
			key := runstore.Open(nil, appSlug, id).Prefix() + name
			if !slices.Contains(read, key) {
				t.Errorf("the uncached listing did not read %s: without the cache every object of the window is read again", key)
			}
		}
	}
}

// genFixture seeds n runs into the counting generation bucket, serves the
// project marker from it (the name check every cloud command makes reads the
// bucket, and that read is not the one under test) and points the fixture's
// config at it, so the real command drives the bucket whose reads the test
// counts.
func genFixture(t *testing.T, f *cloudFixture, n int) (*genDriver, []string) {
	t.Helper()
	d, ids := genRuns(t, n)
	marker, err := os.ReadFile(f.markerPath())
	if err != nil {
		t.Fatal(err)
	}
	d.set(infra.ProjectMarkerObject, marker)
	installGenBucket(t, f, d)
	return d, ids
}

// runReads is every run object read that reached the bucket since the last
// forget, leaving out what a listing reads off the runs bucket besides a
// run: the project marker the name check reads, the image build records.
func (d *genDriver) runReads() []string {
	return slices.DeleteFunc(d.keysRead(), func(k string) bool { return !strings.HasPrefix(k, "runs/") })
}

// TestRunsLsCommandSecondCallReadsOnlyChangedGenerations is the red test's
// command-level half: the same caching, driven through `runs ls` itself over
// a bucket whose listings carry generations, rather than through loadRows.
// It is what pins the reader the command builds -- an ls that never asks for
// the cache would still pass the loader-level test.
func TestRunsLsCommandSecondCallReadsOnlyChangedGenerations(t *testing.T) {
	f := newCloudFixture(t)
	d, ids := genFixture(t, f, 3)

	first, _ := lsJSON(t, "--since", "0")
	if len(first.Runs) != len(ids) {
		t.Fatalf("ls listed %d runs, want %d", len(first.Runs), len(ids))
	}
	if got := d.runReads(); len(got) == 0 {
		t.Fatal("ls read no run object: the fixture is not the bucket the command read")
	}
	d.forget()
	second, _ := lsJSON(t, "--since", "0")
	if !slices.Equal(runIDs(second.Runs), runIDs(first.Runs)) {
		t.Fatalf("the second ls listed %v, want %v", runIDs(second.Runs), runIDs(first.Runs))
	}
	if got := d.runReads(); len(got) != 0 {
		t.Errorf("the second ls read %v: ls serves an unchanged run from the cache", got)
	}

	d.forget()
	third, _ := lsJSON(t, "--since", "0", "--no-cache")
	if !slices.Equal(runIDs(third.Runs), runIDs(first.Runs)) {
		t.Fatalf("ls --no-cache listed %v, want %v", runIDs(third.Runs), runIDs(first.Runs))
	}
	if got := d.runReads(); len(got) == 0 {
		t.Error("ls --no-cache read no run object: the flag has to read the bucket, cache or no cache")
	}
}

// TestResolvePRReadsTheBucketNotTheCache: whether a pull request may be followed
// up is a launch and not a number to draw, so the window resolvePR reads comes
// from the bucket even when a listing has just warmed the cache with the very
// same objects -- the noCache the follow-up passes. The bucket here reports
// generations, so a warmed cache would answer that read if anything consulted
// it, and launch.json is the object that measures it: it is one of the three a
// listing reads and the one nothing on the follow-up path reads for itself
// (checkRoot reads the root's result.json, readTask its task.json), so a
// follow-up answered from the cache leaves it unread.
func TestResolvePRReadsTheBucketNotTheCache(t *testing.T) {
	ctx := context.Background()
	f := newCloudFixture(t)
	exec := seedSpec(t, f, firstRunSpec(rootID), true)
	rec := prRecord(rootID, exec, 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	d := genObjectsOf(t, f)
	env := envOn(t, f, blobx.Wrap(blob.NewBucket(d)))
	window := lsFilter{slugs: []string{appSlug}, since: time.Now().Add(-followUpLookback)}

	// A listing of the same window first: the cache now holds every object of
	// the PR's run at the generation the bucket reported.
	if rows, err := loadRows(ctx, env, window, time.Now()); err != nil || len(rows) != 1 {
		t.Fatalf("the listing before the follow-up = %d rows, %v; want the PR's one run", len(rows), err)
	}
	if got := d.runReads(); len(got) == 0 {
		t.Fatal("the listing read no run object: the fixture is not the bucket the read reaches")
	}
	d.forget()

	if _, err := resolvePR(ctx, env, "acme/app", appSlug, 7, "", nil); err != nil {
		t.Fatal(err)
	}
	read := d.runReads()
	for _, name := range []string{"task.json", "launch.json", "result.json"} {
		key := runstore.Open(nil, appSlug, rootID).Prefix() + name
		if !slices.Contains(read, key) {
			t.Errorf("resolvePR read %v, want %s from the bucket: a warm cache is not what decides whether a pull request allows a follow-up", read, key)
		}
	}
}

// TestRunsLsOversizedObjectIsThisRunsRow: an object past its read cap is
// ErrTooLarge, and a listing draws that as the run's own unreadable object
// rather than as a failure of the listing (corruptObject). The cache must
// not change which of the two it is: an object the bucket refused is never
// cached, so the listing reads it again, and the row is the one a listing
// without a cache draws -- the same rows, the same warnings, warm or cold,
// cached or not.
func TestRunsLsOversizedObjectIsThisRunsRow(t *testing.T) {
	f := newCloudFixture(t)
	d, ids := genFixture(t, f, 3)
	d.set(resultKey(t, ids[0]), bytes.Repeat([]byte("x"), blobx.MaxReadBytes+1))

	// row is what the listing says of the run whose result.json the bucket
	// refused: the run is listed, with its own reason, and it is the run that
	// failed rather than the listing.
	row := func(t *testing.T, got lsOut) string {
		t.Helper()
		for _, r := range got.Runs {
			if r.RunID != ids[0] {
				continue
			}
			if r.Status != runview.StatusError || !strings.Contains(r.Reason, "result.json is unreadable") {
				t.Fatalf("the run whose result.json the bucket refused reads %q / %q, want the run's own unreadable object", r.Status, r.Reason)
			}
			return r.Status + ": " + r.Reason
		}
		t.Fatalf("the listing left out run %s: the listing failed rather than the run", ids[0])
		return ""
	}
	// warn is the one warning line the listing printed for that run: the
	// corrupt object is the run's, and no other run's.
	warn := func(t *testing.T, stderr string) string {
		t.Helper()
		var named []string
		for _, line := range strings.Split(stderr, "\n") {
			if strings.Contains(line, "result.json is unreadable") {
				named = append(named, line)
			}
		}
		if len(named) != 1 || !strings.Contains(named[0], ids[0]+":") {
			t.Fatalf("stderr = %q, want one warning naming %s and no other run", stderr, ids[0])
		}
		return named[0]
	}

	first, firstErr := lsJSON(t, "--since", "0")
	if len(first.Runs) != len(ids) {
		t.Fatalf("ls listed %d of %d runs: an object the bucket refused is that run's row, not the listing's", len(first.Runs), len(ids))
	}
	wantRow, wantWarn := row(t, first), warn(t, firstErr)

	// The warm cache answers the other runs, and the refused object is read
	// again -- a read that failed cached nothing -- so neither the row nor
	// the warning can change because a listing ran before it.
	d.forget()
	second, secondErr := lsJSON(t, "--since", "0")
	if !slices.Equal(runIDs(second.Runs), runIDs(first.Runs)) || row(t, second) != wantRow || warn(t, secondErr) != wantWarn {
		t.Errorf("the warm listing answered %v, want %v, with the same row and warning:\n%q\n%q", runIDs(second.Runs), runIDs(first.Runs), secondErr, firstErr)
	}
	if got := d.runReads(); !slices.Contains(got, resultKey(t, ids[0])) {
		t.Errorf("the warm listing read %v, want the refused object read again", got)
	}

	d.forget()
	third, thirdErr := lsJSON(t, "--since", "0", "--no-cache")
	if !slices.Equal(runIDs(third.Runs), runIDs(first.Runs)) || row(t, third) != wantRow || warn(t, thirdErr) != wantWarn {
		t.Errorf("ls --no-cache answered %v, want %v, with the same row and warning:\n%q\n%q", runIDs(third.Runs), runIDs(first.Runs), thirdErr, firstErr)
	}
}

// TestRunsLsNoCacheFlagIsHidden: --no-cache is an escape hatch, not a knob to
// document -- the cache changes only speed, and a listing that needed the
// flag would be reporting the cache's budget rather than the bucket's. It is
// hidden, so the generated CLI reference (website/content/cli-reference.md,
// internal/clidoc/cliref) and the tests that pin it do not carry it; the
// drift test fails on a visible flag nothing regenerated the reference from,
// which is why this one pins the hidden flag here.
func TestRunsLsNoCacheFlagIsHidden(t *testing.T) {
	flag := newLsCmd().Flags().Lookup("no-cache")
	if flag == nil {
		t.Fatal("runs ls has no --no-cache flag")
	}
	if !flag.Hidden {
		t.Error("--no-cache is not hidden: the generated CLI reference would be out of date")
	}
}
