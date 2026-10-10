package watch

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMergeFinishedAddsRowsBelowRunning(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	v := View{Repos: []RepoBlock{{Slug: "o-r", Runs: []RunRow{{Run: "r-live", StartedAt: now.Add(-time.Minute).UnixMilli()}}}}}
	fin := []FinishedRun{
		{Run: "r-old", Slug: "o-r", Status: "succeeded", Outcome: "ready", PRURL: "https://github.com/o/r/pull/7", PRNumber: 7, FinishedAt: now.Add(-2 * time.Hour)},
		{Run: "r-new", Slug: "o-r", Status: "failed", Outcome: "draft", FinishedAt: now.Add(-time.Hour)},
		{Run: "r-live", Slug: "o-r", Status: "succeeded", FinishedAt: now}, // still live in /agents: skipped
	}
	got := MergeFinished(v, Config{}, fin, now)
	b := got.Repos[0]
	if len(b.Runs) != 1 || len(b.Finished) != 2 {
		t.Fatalf("runs %d finished %d", len(b.Runs), len(b.Finished))
	}
	if b.Finished[0].Run != "r-new" || !b.Finished[0].Failed || b.Finished[1].PRURL != "https://github.com/o/r/pull/7" {
		t.Fatalf("finished rows = %+v", b.Finished)
	}
}

// A finished-only repository (no live run, spend or kill switch of its own)
// gets its name from the local config, exactly as MergeQueued's equivalent
// path does (TestMergeQueuedCreatesRepoBlock), and is sorted into place
// among the other blocks (lessRepoBlock), not merely appended.
func TestMergeFinishedCreatesNamedRepoBlockInSortedOrder(t *testing.T) {
	v := fixture() // acme__lib (killed), acme__app, acme__docs, in that order
	fin := []FinishedRun{{Run: "r-new1", Slug: "acme__aaa", Status: "succeeded", FinishedAt: t0.Add(-time.Hour)}}
	got := MergeFinished(v, Config{RepoNames: map[string]string{"acme__aaa": "acme/aaa"}}, fin, t0)

	var names []string
	for _, b := range got.Repos {
		names = append(names, b.Slug)
	}
	// Blocks keep a stable order by name, not spend (design generic-tool
	// G23): killed first, then "acme/aaa" before "acme/app" before "acme/docs".
	want := []string{"acme__lib", "acme__aaa", "acme__app", "acme__docs"}
	if !slices.Equal(names, want) {
		t.Fatalf("repo order = %v, want %v", names, want)
	}
	var nb *RepoBlock
	for i := range got.Repos {
		if got.Repos[i].Slug == "acme__aaa" {
			nb = &got.Repos[i]
		}
	}
	if nb == nil || nb.Name != "acme/aaa" || len(nb.Finished) != 1 {
		t.Fatalf("block = %+v", nb)
	}
}

// Failed matches the design's definition (generic-tool §10.3): failed,
// halted and infra_error are failures; succeeded and cancelled are not.
func TestFinishedRowFailedMatchesDesignStatuses(t *testing.T) {
	now := time.Now()
	want := map[string]bool{
		"succeeded": false, "cancelled": false,
		"failed": true, "halted": true, "infra_error": true,
	}
	for status, wantFailed := range want {
		f := FinishedRun{Run: "r1", Slug: "o-r", Status: status, FinishedAt: now}
		if row := finishedRow(f, now); row.Failed != wantFailed {
			t.Fatalf("status %q: Failed = %v, want %v", status, row.Failed, wantFailed)
		}
	}
}

// A hostile title (an escape sequence and a bidi override, neither ours to
// trust: result.json's title comes from the bucket) never reaches the row
// with its escape or override byte intact; the plain text around it stays.
func TestFinishedRowSanitisesHostileTitle(t *testing.T) {
	f := FinishedRun{Run: "r1", Slug: "o-r", Title: "pwned\x1b]0;x\a‮evil", Status: "succeeded", FinishedAt: time.Now()}
	row := finishedRow(f, time.Now())
	for _, bad := range []string{"\x1b", "\a", "‮"} {
		if strings.Contains(row.Title, bad) {
			t.Fatalf("title leaked a hostile byte: %q", row.Title)
		}
	}
	if !strings.Contains(row.Title, "pwned") || !strings.Contains(row.Title, "evil") {
		t.Fatalf("the plain text around the hostile bytes must survive: %q", row.Title)
	}
}

// cleanURL refuses anything but a short https:// URL: neither a
// javascript: URL nor an https URL over 200 bytes is ever shown.
func TestCleanURLRefusesNonHTTPSAndOverlongURL(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "https://" + strings.Repeat("a", 200)} {
		if got := cleanURL(u); got != "" {
			t.Fatalf("cleanURL(%q) = %q, want refused", u, got)
		}
	}
}

// A finished run's age is never negative, even if its FinishedAt is after
// now (clock skew between the writer and the reader).
func TestFinishedRowAgeNeverNegative(t *testing.T) {
	now := time.Now()
	f := FinishedRun{Run: "r1", Slug: "o-r", Status: "succeeded", FinishedAt: now.Add(time.Hour)}
	if row := finishedRow(f, now); row.Age != 0 {
		t.Fatalf("age = %s, want 0 (never negative)", row.Age)
	}
}
