package runstore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/task"
)

// The resolver's clock: every case below is a fixed date, so the seven days
// D1702 bounds an ending lookup by are a fact of the test and not of the day
// it runs on.
var (
	resolveNow   = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	resolveSince = resolveNow.AddDate(0, 0, -7)
)

// activeOf is a live feed holding refs.
func activeOf(refs ...task.Ref) func(context.Context) ([]task.Ref, error) {
	return func(context.Context) ([]task.Ref, error) { return refs, nil }
}

// A unique active match wins over a finished run whose ID ends the same way
// (D1702): the feed is read first, and the window is not consulted while it
// has an answer.
func TestResolveEndingActiveBeatsRecent(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20261108-120000-3bb2"), "20261108-120000-3bb2")
	seed(t, Open(b, "acme-app", "20261107-010101-3bb2"), "20261107-010101-3bb2")
	active := activeOf(task.Ref{Slug: "acme-app", RunID: "20261108-120000-3bb2"})

	got, err := ResolveEnding(ctx, b, active, "3bb2", resolveSince)
	if err != nil || len(got) != 1 {
		t.Fatalf("ResolveEnding = %v, %v", got, err)
	}
	if m := got[0]; m != (Match{Slug: "acme-app", RunID: "20261108-120000-3bb2", Active: true}) {
		t.Fatalf("ResolveEnding = %+v, want the active run", m)
	}
	// Without the feed the two runs of the window collide: that is the
	// ambiguity the active answer spares the caller.
	if _, err := ResolveEnding(ctx, b, nil, "3bb2", resolveSince); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ResolveEnding without the feed = %v, want ErrAmbiguous", err)
	}
}

// The window is the caller's: an ending resolves against the runs minted at
// or after since (D1702), across every repository, and a run older than it is
// not found by an ending however unique that ending is (F16) — its full ID is
// its way in.
func TestResolveEndingWindow(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20261106-090000-aaaa"), "20261106-090000-aaaa")
	seed(t, Open(b, "acme-web", "20261105-090000-bbbb"), "20261105-090000-bbbb")
	seed(t, Open(b, "acme-app", "20261031-090000-cccc"), "20261031-090000-cccc") // 8 days before now

	got, err := ResolveEnding(ctx, b, nil, "aaaa", resolveSince)
	if err != nil || !slices.Equal(got, []Match{{Slug: "acme-app", RunID: "20261106-090000-aaaa"}}) {
		t.Fatalf("ResolveEnding = %v, %v", got, err)
	}
	if got, err := ResolveEnding(ctx, b, nil, "bbbb", resolveSince); err != nil || len(got) != 1 || got[0].Slug != "acme-web" {
		t.Fatalf("ResolveEnding in another repository = %v, %v", got, err)
	}
	if _, err := ResolveEnding(ctx, b, nil, "cccc", resolveSince); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveEnding of an 8-day-old run = %v, want ErrNotFound", err)
	}
	// The same run by its full ID: the window bounds an ending, never a run.
	if got, err := ResolveEnding(ctx, b, nil, "20261031-090000-cccc", resolveSince); err != nil || len(got) != 1 {
		t.Fatalf("ResolveEnding of the full ID = %v, %v", got, err)
	}
}

// An ending shorter than the short form itself is refused, in runes (D1701):
// four characters is the shortest one any surface displays, and the shortest
// one a lookup accepts.
func TestResolveEndingTooShort(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20261108-115248-3bb2"), "20261108-115248-3bb2")

	for _, ending := range []string{"", "b", "bb", "3bb", "テキス"} {
		if _, err := ResolveEnding(ctx, b, nil, ending, resolveSince); !errors.Is(err, ErrTooShort) {
			t.Errorf("ResolveEnding(%q) = %v, want ErrTooShort", ending, err)
		}
	}
	if _, err := ResolveEnding(ctx, b, nil, "3bb2", resolveSince); err != nil {
		t.Fatalf("ResolveEnding(3bb2) = %v", err)
	}
	if _, err := ResolveEnding(ctx, b, nil, "テキスト", resolveSince); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveEnding(テキスト) = %v, want ErrNotFound (four runes is not too short)", err)
	}
}

// Two runs whose IDs end the same way are never resolved by picking one
// (D1700): the resolver answers ErrAmbiguous and returns the candidates
// beside it, newest first, the order `runs ls` shows them in.
func TestResolveEndingAmbiguousAcrossRepos(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20261108-115248-3bb2"), "20261108-115248-3bb2")
	seed(t, Open(b, "acme-web", "20261107-010101-3bb2"), "20261107-010101-3bb2")

	got, err := ResolveEnding(ctx, b, nil, "3bb2", resolveSince)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ResolveEnding = %v, %v, want ErrAmbiguous", got, err)
	}
	want := []Match{
		{Slug: "acme-app", RunID: "20261108-115248-3bb2"},
		{Slug: "acme-web", RunID: "20261107-010101-3bb2"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ResolveEnding = %v, want %v", got, want)
	}
	// Two active matches are ambiguous too, and both are marked active.
	active := activeOf(
		task.Ref{Slug: "acme-app", RunID: "20261108-115248-3bb2"},
		task.Ref{Slug: "acme-web", RunID: "20261107-010101-3bb2"},
	)
	got, err = ResolveEnding(ctx, b, active, "3bb2", resolveSince)
	if !errors.Is(err, ErrAmbiguous) || !slices.Equal(got, []Match{
		{Slug: "acme-app", RunID: "20261108-115248-3bb2", Active: true},
		{Slug: "acme-web", RunID: "20261107-010101-3bb2", Active: true},
	}) {
		t.Fatalf("ResolveEnding with two active = %v, %v", got, err)
	}
}

// A full run ID and `<slug>/<run-id>` keep Locate's rules and Locate's
// errors: they are not endings, and a lookup of one must not depend on the
// live feed being readable (D1705).
func TestResolveEndingFullIDUnchanged(t *testing.T) {
	b := memblob.OpenBucket(nil)
	seed(t, Open(b, "acme-app", "20261108-115248-3bb2"), "20261108-115248-3bb2")
	failing := func(context.Context) ([]task.Ref, error) { return nil, errors.New("feed is down") }

	for _, ref := range []string{"20261108-115248-3bb2", "acme-app/20261108-115248-3bb2"} {
		slug, runID, err := Locate(ctx, b, ref)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ResolveEnding(ctx, b, failing, ref, resolveSince)
		if err != nil || !slices.Equal(got, []Match{{Slug: slug, RunID: runID}}) {
			t.Fatalf("ResolveEnding(%q) = %v, %v; want %s/%s", ref, got, err, slug, runID)
		}
	}
	if _, err := ResolveEnding(ctx, b, failing, "acme-app/20261108-115248-ffff", resolveSince); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveEnding of a missing ref = %v, want ErrNotFound", err)
	}
	if _, err := ResolveEnding(ctx, b, failing, "20260101-000000-ffff", resolveSince); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveEnding of a missing ID = %v, want ErrNotFound", err)
	}
	if _, err := ResolveEnding(ctx, b, failing, "acme-app/nope", resolveSince); err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrTooShort) {
		t.Fatalf("ResolveEnding of a malformed ref = %v, want Locate's refusal", err)
	}
	seed(t, Open(b, "acme-web", "20261108-115248-3bb2"), "20261108-115248-3bb2")
	if _, err := ResolveEnding(ctx, b, failing, "20261108-115248-3bb2", resolveSince); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ResolveEnding of an ID in two repositories = %v, want Locate's ErrAmbiguous", err)
	}
}
