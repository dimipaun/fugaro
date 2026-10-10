package watch

import (
	"fmt"
	"testing"
	"time"
)

func TestFilterStricterOfAgeAndCount(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	_ = now // Apply takes no explicit "now": Age is already computed on the rows.
	var fin []RunRow
	for i := 0; i < 20; i++ { // 20 successes, one every 10 minutes
		fin = append(fin, RunRow{Run: fmt.Sprintf("r%02d", i), Finished: true, Age: time.Duration(i) * 10 * time.Minute})
	}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: fin}}}
	got, hidden := Filter{Keep: 6 * time.Hour, KeepCount: 15}.Apply(v, nil)
	if n := len(got.Repos[0].Finished); n != 15 || hidden != 5 {
		t.Fatalf("kept %d hid %d, want 15 and 5 (count is stricter)", n, hidden)
	}
	got, _ = Filter{Keep: time.Hour, KeepCount: 15}.Apply(v, nil)
	if n := len(got.Repos[0].Finished); n != 6 { // ages 0..50 min
		t.Fatalf("kept %d, want 6 (age is stricter)", n)
	}
	got, hidden = Filter{All: true, Keep: time.Hour, KeepCount: 1}.Apply(v, nil)
	if len(got.Repos[0].Finished) != 20 || hidden != 0 {
		t.Fatal("--all filtered")
	}
}

func TestFailedRunsStayUntilAcked(t *testing.T) {
	failed := RunRow{Run: "f", Finished: true, Failed: true, Age: 10 * time.Hour}
	ok := RunRow{Run: "s", Finished: true, Age: 10 * time.Hour}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{failed, ok}}}}
	f := Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}
	got, _ := f.Apply(v, func(string, string) bool { return false })
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "f" {
		t.Fatalf("got %+v, want only the failure", got.Repos[0].Finished)
	}
	got, _ = f.Apply(v, func(_, run string) bool { return run == "f" })
	if len(got.Repos[0].Finished) != 0 {
		t.Fatal("an acknowledged failure stayed")
	}
}

// ReadyForReview lists only "ready" rows, across repositories, newest first;
// a "draft" outcome (passed review on a draft whose test failed, design
// generic-tool §10.1) never appears there even though it is still a
// finished row.
func TestReadyForReviewListsReadyNewestFirst(t *testing.T) {
	v := View{Repos: []RepoBlock{
		{Slug: "a", Finished: []RunRow{
			{Run: "old", Outcome: "ready", StartedAt: 1000, PRURL: "https://github.com/acme/app/pull/1"},
			{Run: "draft", Outcome: "draft", StartedAt: 3000, PRURL: "https://github.com/acme/app/pull/2"},
		}},
		{Slug: "b", Finished: []RunRow{
			{Run: "new", Outcome: "ready", StartedAt: 2000, PRURL: "https://github.com/acme/lib/pull/9"},
		}},
	}}
	got := ReadyForReview(v)
	if len(got) != 2 || got[0].Run != "new" || got[1].Run != "old" {
		t.Fatalf("got %+v, want new then old (draft excluded)", got)
	}
}

// A block created only to show finished rows (no running or queued run, no
// spend, no kill switch) disappears once every finished row it showed is
// filtered out, the way Build never shows an empty block today.
func TestDropEmptyMergedBlockOnceFilteredAway(t *testing.T) {
	v := View{Repos: []RepoBlock{
		{Slug: "a", Finished: []RunRow{{Run: "old", Age: 48 * time.Hour}}},
		{Slug: "b", Runs: []RunRow{{Run: "live"}}, Finished: []RunRow{{Run: "old2", Age: 48 * time.Hour}}},
	}}
	got, _ := Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}.Apply(v, nil)
	got = DropEmptyFinishedBlocks(got)
	if len(got.Repos) != 1 || got.Repos[0].Slug != "b" {
		t.Fatalf("got %+v, want only the block that still has a live run", got.Repos)
	}
}
