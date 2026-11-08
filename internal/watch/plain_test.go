package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/task"
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
	short := func(id string) string { return task.ShortRun(id) } // the frame's own form of a run id
	for _, want := range []string{short("r-ready"), "READY FOR REVIEW", short("r-failed"), "FAILED", "Ready for your review (1)", "https://github.com/acme/app/pull/7", "Fix the bug"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	// Only the ready run appears in the review section, not the failure.
	section := got[strings.Index(got, "Ready for your review"):]
	if strings.Contains(section, short("r-failed")) {
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

// shortRunView is one repository with an active run and a finished one: the
// two tables of one block, which are the rows the collision rule works over.
func shortRunView(active, finished string) View {
	v := View{Now: t0, Repos: []RepoBlock{{Slug: "a", Name: "acme/app", Runs: []RunRow{
		mkRun(active, "Fix the flaky login test", "coding", nil),
	}}}}
	return MergeFinished(v, Config{}, []FinishedRun{
		{Run: finished, Slug: "a", Title: "Speed up the parser", Status: "succeeded", FinishedAt: t0.Add(-time.Minute)},
	}, t0)
}

// A run's id in the plain frame is the same short form the interactive table
// shows — its last four characters — and never the whole id (spec.md §1, F3):
// the leading characters are the launch stamp, which every run started in the
// same minute shares, so a column of them tells two rows apart no better than
// a column of one. Both tables read the same way, the active one and the
// finished one.
func TestPlainShortRun(t *testing.T) {
	got := plainFrame(t, shortRunView("20261023-021512-abcd", "20261024-030000-ef01"))
	for _, want := range []string{"abcd", "ef01", "Fix the flaky login test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the plain frame is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "20261023") || strings.Contains(got, "20261024") {
		t.Fatalf("the plain frame prints a launch stamp:\n%s", got)
	}
}

// Two runs whose ids share their last four characters are shown with an
// ending extended one character at a time until the two differ (spec.md §3,
// task.DisplayRuns; the extension crosses the dash), and the extension covers
// a block's two tables at once: an active row and a finished one are printed
// together on this frame, so a collision between them widens both cells.
func TestPlainCollision(t *testing.T) {
	got := plainFrame(t, shortRunView("20261108-115248-3bb2", "20261107-010101-3bb2"))
	for _, want := range []string{"8-3bb2", "1-3bb2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the colliding ending %q is not in the plain frame:\n%s", want, got)
		}
	}
	if strings.Contains(got, "20261108") || strings.Contains(got, "20261107") {
		t.Fatalf("the plain frame prints a launch stamp:\n%s", got)
	}
}

// TestRenderPlainWarnsAtEightyPercent: the plain frames have no colour at all,
// so the cap figure they print must carry the 80% warning itself (R5), and the
// threshold is the ratio, not the rounded figure: 79.9% draws "(80%)" and no
// mark.
func TestRenderPlainWarnsAtEightyPercent(t *testing.T) {
	view := func(pct float64) View {
		return View{
			Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
			Project: ProjectLine{Bar: Bar{Has: true, Used: 40 * usd, Cap: 50 * usd, Percent: pct}, Runs: 1},
		}
	}
	plain := func(pct float64, ascii bool) string {
		var b strings.Builder
		if err := RenderPlain(&b, "aurora", view(pct), nil, PlainOptions{ASCII: ascii}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if got := plain(79.9, false); !strings.Contains(got, "counted $40.00 of $50.00 (80%)") || strings.Contains(got, "⚠") {
		t.Fatalf("79.9%% of the cap must print no warning:\n%s", got)
	}
	if got := plain(80, false); !strings.Contains(got, "counted $40.00 of $50.00 (80% ⚠)") {
		t.Fatalf("80%% of the cap drew no warning:\n%s", got)
	}
	if got := plain(80, true); !strings.Contains(got, "counted $40.00 of $50.00 (80% !)") {
		t.Fatalf("the ascii warning is missing:\n%s", got)
	}
}
