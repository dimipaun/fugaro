package watch

import (
	"strings"
	"testing"
	"time"
)

// RenderPlain shows a repository's finished runs (status, PR, age) and, at
// the end, the "Ready for your review" section (design generic-tool §10.1
// and §10.3): the same information the interactive screen shows, in the
// non-interactive text frame.
func TestRenderPlainShowsFinishedAndReady(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Name: "acme/app"}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-ready", Slug: "a", Title: "Fix the bug", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/7", PRNumber: 7, FinishedAt: t0.Add(-time.Hour)},
		{Run: "r-failed", Slug: "a", Title: "Half-done", Status: "failed", FinishedAt: t0.Add(-2 * time.Hour)},
	}, t0)
	var b strings.Builder
	if err := RenderPlain(&b, "aurora", v, ReadyRowsOf(v), PlainOptions{}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"r-ready", "READY FOR REVIEW", "r-failed", "FAILED", "Ready for your review (1)", "https://github.com/acme/app/pull/7", "Fix the bug"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	// Only the ready run appears in the review section, not the failure.
	section := got[strings.Index(got, "Ready for your review"):]
	if strings.Contains(section, "r-failed") {
		t.Fatalf("the failure must not be in the review section:\n%s", section)
	}
}

// A ready PR stays in "Ready for your review" even once the recency filter
// (or an empty v with no repositories left at all) has dropped its own
// finished row from the table above: ready is resolved by the caller before
// filtering (design generic-tool §10.1), and RenderPlain only draws the
// resolved list it is given, never recomputing it from v.
func TestRenderPlainReadySurvivesAFilteredView(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Name: "acme/app"}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-ready", Slug: "a", Title: "Fix the bug", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/7", PRNumber: 7, FinishedAt: t0.Add(-30 * time.Hour)},
	}, t0)
	ready := ReadyRowsOf(v) // resolved before the filter would hide the row
	filtered, _ := Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}.Apply(v, nil)
	filtered = DropEmptyFinishedBlocks(filtered)
	if len(filtered.Repos) != 0 {
		t.Fatalf("the fixture should filter down to no repositories at all: %+v", filtered.Repos)
	}
	var b strings.Builder
	if err := RenderPlain(&b, "aurora", filtered, ready, PlainOptions{}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "Ready for your review (1)") || !strings.Contains(got, "https://github.com/acme/app/pull/7") {
		t.Fatalf("a 30h-old ready PR must still be listed:\n%s", got)
	}
}
