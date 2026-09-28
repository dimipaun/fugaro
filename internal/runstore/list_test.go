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
