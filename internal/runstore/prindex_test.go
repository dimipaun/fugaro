package runstore

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// prIndexBucket is a fake GCS runs bucket: the index is written create-only,
// and a generation is what the fake carries that a mem:// bucket does not.
func prIndexBucket(t *testing.T) (*gcpfake.GCS, *blob.Bucket) {
	t.Helper()
	g := gcpfake.NewGCS(t)
	return g, g.Bucket(t, "runs").Bucket
}

// TestEntryPathIsPerRun: one object per run, so two runs of one pull request
// are two objects and neither write can replace the other (D2962).
func TestEntryPathIsPerRun(t *testing.T) {
	const (
		slug = "acme-app"
		id   = "20261119-130000-abcd"
	)
	got, err := PRIndexPath(slug, 42, id)
	if err != nil || got != "runs/acme-app/pr-index/42/20261119-130000-abcd.json" {
		t.Fatalf("PRIndexPath = %q, %v", got, err)
	}
	other, err := PRIndexPath(slug, 42, "20261119-140000-beef")
	if err != nil || other == got {
		t.Fatalf("another run of the same pull request = %q, %v: one object per pull request would be overwritable whole", other, err)
	}
	otherPR, err := PRIndexPath(slug, 43, id)
	if err != nil || otherPR == got {
		t.Fatalf("the same run on another pull request = %q, %v", otherPR, err)
	}

	_, b := prIndexBucket(t)
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	if err := PutPRIndex(ctx, b, PRIndexEntry{RunID: id, Slug: slug, PR: 42, Kind: PRIndexOpened, At: at}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Exists(ctx, got); err != nil || !ok {
		t.Fatalf("the write did not land on PRIndexPath's name: %v, %v", ok, err)
	}

	for _, bad := range []struct {
		slug string
		pr   int
		run  string
	}{
		{"", 42, id}, {"..", 42, id}, {"Acme-App", 42, id}, {"acme/app", 42, id}, {"acme-app/", 42, id},
		{slug, 0, id}, {slug, -1, id},
		{slug, 42, ""}, {slug, 42, "not-a-run"}, {slug, 42, "../" + id}, {slug, 42, id + "/x"},
	} {
		if p, err := PRIndexPath(bad.slug, bad.pr, bad.run); err == nil {
			t.Errorf("PRIndexPath(%q, %d, %q) = %q, want a refusal: every segment becomes an object name", bad.slug, bad.pr, bad.run, p)
		}
	}
}

// TestPutIsCreateIfAbsent: the object is the run's own and it is written
// once; a second write of the same run is a no-op and never an overwrite
// (D2962).
func TestPutIsCreateIfAbsent(t *testing.T) {
	const (
		slug = "acme-app"
		id   = "20261119-130000-abcd"
	)
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	_, b := prIndexBucket(t)
	first := PRIndexEntry{RunID: id, Slug: slug, PR: 42, Kind: PRIndexOpened, At: at}
	if err := PutPRIndex(ctx, b, first); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Kind, second.At = PRIndexFollowed, at.Add(time.Hour)
	if err := PutPRIndex(ctx, b, second); err != nil {
		t.Fatalf("a second write of one run's entry = %v, want a no-op", err)
	}
	key, err := PRIndexPath(slug, 42, id)
	if err != nil {
		t.Fatal(err)
	}
	stored := readPRIndexObject(t, b, key)
	if stored.Kind != PRIndexOpened || !stored.At.Equal(at) {
		t.Fatalf("the second write replaced the first: %+v", stored)
	}
	if stored.Version != PRIndexVersion {
		t.Fatalf("the writer stored version %d, want %d", stored.Version, PRIndexVersion)
	}

	// Another run of the same pull request writes its own object, and the
	// first run's object is untouched.
	otherID := "20261119-140000-beef"
	if err := PutPRIndex(ctx, b, PRIndexEntry{RunID: otherID, Slug: slug, PR: 42, Kind: PRIndexFollowed, At: at}); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := PRIndexPath(slug, 42, otherID)
	if ok, err := b.Exists(ctx, otherKey); err != nil || !ok {
		t.Fatalf("the second run's entry is not there: %v, %v", ok, err)
	}
	if stored := readPRIndexObject(t, b, key); stored.RunID != id {
		t.Fatalf("another run's write changed this run's entry: %+v", stored)
	}

	for _, bad := range []PRIndexEntry{
		{RunID: id, Slug: slug, PR: 42, Kind: "other", At: at},
		{RunID: id, Slug: slug, PR: 42, Kind: "", At: at},
		{RunID: id, Slug: slug, PR: 42, Kind: PRIndexOpened},
		{RunID: id, Slug: slug, PR: 0, Kind: PRIndexOpened, At: at},
		{RunID: id, Slug: "", PR: 42, Kind: PRIndexOpened, At: at},
		{RunID: "nope", Slug: slug, PR: 42, Kind: PRIndexOpened, At: at},
	} {
		if err := PutPRIndex(ctx, b, bad); err == nil {
			t.Errorf("PutPRIndex(%+v) = nil, want a refusal: an entry that could not be read back must not be written", bad)
		}
	}
}

// readPRIndexObject reads one index object back the way a reader would.
func readPRIndexObject(t *testing.T, b *blob.Bucket, key string) PRIndexEntry {
	t.Helper()
	data, _, err := blobx.Wrap(b).Read(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var e PRIndexEntry
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// TestListPRIndexByNumber: one listing of runs/<slug>/pr-index/<N>/, newest
// run first, and nothing of another pull request.
func TestListPRIndexByNumber(t *testing.T) {
	const slug = "acme-app"
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	_, b := prIndexBucket(t)
	older, newer := "20261119-100000-aaaa", "20261119-110000-bbbb"
	for _, e := range []PRIndexEntry{
		{RunID: older, Slug: slug, PR: 42, Kind: PRIndexOpened, At: at},
		{RunID: newer, Slug: slug, PR: 42, Kind: PRIndexFollowed, At: at.Add(time.Hour)},
		{RunID: "20261119-120000-cccc", Slug: slug, PR: 7, Kind: PRIndexOpened, At: at},
	} {
		if err := PutPRIndex(ctx, b, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ListPRIndex(ctx, b, slug, 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RunID != newer || got[1].RunID != older {
		t.Fatalf("ListPRIndex(42) = %+v, want %s then %s", got, newer, older)
	}
	if got[0].Kind != PRIndexFollowed || got[0].PR != 42 || got[0].Slug != slug {
		t.Fatalf("the entry lost a member on the way back: %+v", got[0])
	}

	seven, err := ListPRIndex(ctx, b, slug, 7, nil)
	if err != nil || len(seven) != 1 || seven[0].RunID != "20261119-120000-cccc" {
		t.Fatalf("ListPRIndex(7) = %+v, %v", seven, err)
	}
	none, err := ListPRIndex(ctx, b, slug, 99, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("ListPRIndex(99) = %+v, %v, want nothing and no error", none, err)
	}
	if _, err := ListPRIndex(ctx, b, slug, 0, nil); err == nil {
		t.Error("ListPRIndex(0) = nil, want a refusal")
	}
	if _, err := ListPRIndex(ctx, b, "..", 42, nil); err == nil {
		t.Error(`ListPRIndex("..") = nil, want a refusal`)
	}
}

// TestBadEntrySkipped: the object is one the untrusted job account writes, so
// a reader decodes it strictly and checks it against where it was found, and
// skips what it cannot vouch for with a warning rather than failing the query
// (D2963, F4).
func TestBadEntrySkipped(t *testing.T) {
	const slug = "acme-app"
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	good := PRIndexEntry{RunID: "20261119-090000-aaaa", Slug: slug, PR: 42, Kind: PRIndexOpened, At: at}
	g, b := prIndexBucket(t)
	if err := PutPRIndex(ctx, b, good); err != nil {
		t.Fatal(err)
	}

	const prefix = "runs/acme-app/pr-index/42/"
	put := func(name, doc string) { g.Put("runs", prefix+name, []byte(doc)) }
	entry := func(run, kind, at string) string {
		return fmt.Sprintf(`{"version":1,"run_id":%q,"slug":"acme-app","pr":42,"kind":%q,"at":%q}`, run, kind, at)
	}
	put("20261119-100000-bbbb.json", "{not json")
	put("20261119-110000-cccc.json", entry("20261119-110000-dddd", "opened", "2026-11-19T11:00:00Z"))
	put("20261119-120000-dddd.json", `{"version":2,"run_id":"20261119-120000-dddd","slug":"acme-app","pr":42,"kind":"opened","at":"2026-11-19T12:00:00Z"}`)
	put("20261119-130000-eeee.json", entry("20261119-130000-eeee", "other", "2026-11-19T13:00:00Z"))
	put("20261119-140000-ffff.json", `{"version":1,"run_id":"20261119-140000-ffff","slug":"acme-app","pr":42,"kind":"opened","at":"2026-11-19T14:00:00Z","extra":1}`)
	put("20261119-150000-aaaa.json", `{"version":1,"run_id":"20261119-150000-aaaa","slug":"acme-app","pr":42,"kind":"opened"}`)
	put("20261119-160000-bbbb.json", `{"version":1,"run_id":"20261119-160000-bbbb","slug":"other-app","pr":42,"kind":"opened","at":"2026-11-19T16:00:00Z"}`)
	put("not-a-run.json", entry("not-a-run", "opened", "2026-11-19T17:00:00Z"))
	put("nested/20261119-180000-aaaa.json", entry("20261119-180000-aaaa", "opened", "2026-11-19T18:00:00Z"))

	var warnings []string
	got, err := ListPRIndex(ctx, b, slug, 42, func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) })
	if err != nil {
		t.Fatalf("a bad object failed the query: %v", err)
	}
	if len(got) != 1 || got[0].RunID != good.RunID {
		t.Fatalf("ListPRIndex = %+v, want only the good entry %s", got, good.RunID)
	}
	if len(warnings) != 9 {
		t.Fatalf("skipped %d objects, warnings = %v, want 9", len(warnings), warnings)
	}
	if _, err := ListPRIndex(ctx, b, slug, 42, nil); err != nil {
		t.Fatalf("a nil warnf = %v, want silence rather than a panic", err)
	}
}

// TestReadersSkipPRIndex: pr-index/ is a sibling of the run prefixes, not a
// run, so every reader that walks runs/<slug>/ must skip it (F7).
func TestReadersSkipPRIndex(t *testing.T) {
	const slug = "acme-app"
	run := "20260927-090000-bbbb"
	at := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	_, bucket := prIndexBucket(t)
	seed(t, Open(bucket, slug, run), run)
	for _, e := range []PRIndexEntry{
		{RunID: run, Slug: slug, PR: 42, Kind: PRIndexOpened, At: at},
		{RunID: "20260927-100000-cccc", Slug: slug, PR: 7, Kind: PRIndexFollowed, At: at},
	} {
		if err := PutPRIndex(ctx, bucket, e); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := ListRunIDs(ctx, bucket, slug, time.Time{})
	if err != nil || !slices.Equal(ids, []string{run}) {
		t.Fatalf("ListRunIDs = %v, %v: the index is not a run", ids, err)
	}
	slugs, err := ListSlugs(ctx, bucket)
	if err != nil || !slices.Equal(slugs, []string{slug}) {
		t.Fatalf("ListSlugs = %v, %v", slugs, err)
	}
	if got, _, err := Locate(ctx, bucket, run); err != nil || got != slug {
		t.Fatalf("Locate = %q, %v", got, err)
	}
	// The windowed listing (an offset that starts at a run ID) skips it too.
	if ids, err := ListRunIDs(ctx, bucket, slug, at.Add(-time.Hour)); err != nil || !slices.Equal(ids, []string{run}) {
		t.Fatalf("the windowed listing = %v, %v", ids, err)
	}

	// A repository that holds only index objects holds no runs at all.
	_, only := prIndexBucket(t)
	if err := PutPRIndex(ctx, only, PRIndexEntry{RunID: run, Slug: slug, PR: 42, Kind: PRIndexOpened, At: at}); err != nil {
		t.Fatal(err)
	}
	if ids, err := ListRunIDs(ctx, only, slug, time.Time{}); err != nil || len(ids) != 0 {
		t.Fatalf("a repository of index objects listed runs: %v, %v", ids, err)
	}
}
