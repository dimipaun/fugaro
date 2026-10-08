package runstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

func seed(t *testing.T, s *Store, id string) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/x", Ref: "main", Task: "x"}
	if err := s.CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

func TestListAndLocate(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	ids := []string{"20260925-100000-aaaa", "20260927-090000-bbbb", "20260927-110000-cccc"}
	for _, id := range ids {
		seed(t, Open(b, "acme-app", id), id)
	}
	seed(t, Open(b, "acme-web", "20260927-120000-dddd"), "20260927-120000-dddd")
	// Not a run: a stray object must not break listing.
	_ = b.WriteAll(ctx, "runs/acme-app/notes.txt", []byte("x"), nil)

	slugs, err := ListSlugs(ctx, b)
	if err != nil || !slices.Equal(slugs, []string{"acme-app", "acme-web"}) {
		t.Fatalf("ListSlugs = %v, %v", slugs, err)
	}
	since := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	got, err := ListRunIDs(ctx, b, "acme-app", since)
	if err != nil || !slices.Equal(got, []string{"20260927-110000-cccc", "20260927-090000-bbbb"}) {
		t.Fatalf("ListRunIDs = %v, %v", got, err)
	}
	if slug, id, err := Locate(ctx, b, "20260927-120000-dddd"); err != nil || slug != "acme-web" || id != "20260927-120000-dddd" {
		t.Fatalf("Locate bare = %s %s %v", slug, id, err)
	}
	if slug, _, err := Locate(ctx, b, "acme-app/20260925-100000-aaaa"); err != nil || slug != "acme-app" {
		t.Fatalf("Locate ref = %s %v", slug, err)
	}
	if _, _, err := Locate(ctx, b, "20260101-000000-ffff"); err == nil || !strings.Contains(err.Error(), "no run") {
		t.Fatalf("Locate missing = %v", err)
	}
	seed(t, Open(b, "acme-web", "20260927-110000-cccc"), "20260927-110000-cccc")
	if _, _, err := Locate(ctx, b, "20260927-110000-cccc"); !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "acme-app/20260927-110000-cccc") {
		t.Fatalf("Locate ambiguous = %v", err)
	}
	if rt, err := RunTime("20260927-110000-cccc"); err != nil || !rt.Equal(time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("RunTime = %v %v", rt, err)
	}
}

// A remote failure while checking a run is not "no run": it must not look
// like a user error.
func TestLocateRemoteErrorIsNotNotFound(t *testing.T) {
	b := blob.NewBucket(&fakeBucket{attrErr: errFakeRemote})
	_, _, err := Locate(context.Background(), b, "acme-app/20260925-100000-aaaa")
	if err == nil || errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), "no run") || !strings.Contains(err.Error(), "runs bucket") {
		t.Fatalf("Locate remote error = %v", err)
	}
	_, _, err = Locate(context.Background(), memblob.OpenBucket(nil), "acme-app/20260925-100000-aaaa")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "no run") {
		t.Fatalf("Locate missing = %v", err)
	}
}

func TestListRunIDsRejectsBadSlug(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20260927-090000-bbbb"), "20260927-090000-bbbb")
	for _, bad := range []string{"", ".", "..", "Acme-App", "acme/app", "acme-app/"} {
		if _, err := ListRunIDs(context.Background(), b, bad, time.Time{}); err == nil || !strings.Contains(err.Error(), "slug") {
			t.Errorf("ListRunIDs(%q) = %v", bad, err)
		}
	}
	if got, err := ListRunIDs(context.Background(), b, "acme", time.Time{}); err != nil || len(got) != 0 {
		t.Errorf("ListRunIDs(acme) = %v, %v", got, err)
	}
}

// A run ID's mint time is its UTC prefix, so since is a start offset that
// spans midnight: the runs minted just before it are left out, those just
// after (on either day) are listed, on GCS (server-side offset) and on a
// bucket that ignores the offset (memblob) alike.
func TestListRunIDsSinceAcrossMidnight(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	buckets := map[string]*blob.Bucket{"memblob": memblob.OpenBucket(nil), "gcs": g.Bucket(t, "runs").Bucket}
	for name, b := range buckets {
		for _, id := range []string{
			"20261006-235900-aaaa", "20261007-235459-aaaa", // before since
			"20261007-235500-bbbb", "20261007-235959-cccc", "20261008-000000-dddd", "20261008-001000-eeee",
		} {
			if err := b.WriteAll(ctx, "runs/acme-app/"+id+"/task.json", []byte("{}"), nil); err != nil {
				t.Fatal(err)
			}
		}
		since := time.Date(2026, 10, 7, 23, 55, 0, 0, time.UTC)
		want := []string{"20261008-001000-eeee", "20261008-000000-dddd", "20261007-235959-cccc", "20261007-235500-bbbb"}
		// any zone, earlier or later than UTC: IDs are UTC
		for _, off := range []int{-7, 7} {
			got, err := ListRunIDs(ctx, b, "acme-app", since.In(time.FixedZone("x", off*3600)))
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("%s (UTC%+d): ListRunIDs = %v, %v; want %v", name, off, got, err, want)
			}
		}
	}
}

// On GCS a since listing starts at the offset server side: it costs one
// list call however many older runs the repository has, where the full
// listing pages through all of them.
func TestListRunIDsSinceCostIndependentOfHistory(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	b := g.Bucket(t, "runs").Bucket
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5000 {
		g.Put("runs", "runs/acme-app/"+old.Add(time.Duration(i)*time.Minute).Format("20060102-150405")+"-0000/task.json", []byte("{}"))
	}
	g.Put("runs", "runs/acme-app/20261007-120000-ffff/task.json", []byte("{}"))
	before := g.ListCalls()
	got, err := ListRunIDs(ctx, b, "acme-app", time.Date(2026, 10, 7, 11, 30, 0, 0, time.UTC))
	if err != nil || !slices.Equal(got, []string{"20261007-120000-ffff"}) {
		t.Fatalf("ListRunIDs = %v, %v", got, err)
	}
	if n := g.ListCalls() - before; n != 1 {
		t.Fatalf("a since listing made %d list calls, want 1", n)
	}
	before = g.ListCalls()
	if all, err := ListRunIDs(ctx, b, "acme-app", time.Time{}); err != nil || len(all) != 5001 {
		t.Fatalf("full listing: %d ids, %v", len(all), err)
	}
	if n := g.ListCalls() - before; n < 5 {
		t.Fatalf("the full listing made %d list calls; the fake must page like GCS", n)
	}
}
