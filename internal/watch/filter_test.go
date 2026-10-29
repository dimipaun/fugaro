package watch

import (
	"fmt"
	"strings"
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

// An acknowledgement dismisses any finished run, not only a failure (owner
// spec 2026-10-22, item 4): x means "I have seen this one", and it is the
// same local, per-viewer record the failures have always used. Mutation
// caught: consulting the ack set only in the failure branch.
func TestAckDismissesAnyFinishedRun(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{
		{Run: "ok-young", Finished: true, Age: time.Minute},
		{Run: "ok-acked", Finished: true, Age: time.Minute},
		{Run: "bad-acked", Finished: true, Failed: true, Age: time.Minute},
	}}}}
	f := Filter{Keep: time.Hour, KeepCount: 15, FailedKeep: time.Hour}
	got, hidden := f.Apply(v, func(_, run string) bool { return strings.HasSuffix(run, "acked") })
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "ok-young" {
		t.Fatalf("got %+v, want only the run nobody dismissed", got.Repos[0].Finished)
	}
	if hidden != 2 {
		t.Fatalf("hidden = %d, want both dismissed runs", hidden)
	}
}

// --keep reaches every finished row, not only a success (0.10.2 fix of
// #950): a person who widens the window wants to see the failures and the
// halted runs too, and raising it must never make a failure less visible
// than a success of the same age. Mutation caught: a failure branch that
// reads the 3-minute FailedKeep floor instead of the widened window — the
// bug this test was written red for (a 10m-old failure vanished under
// --keep 24h while a 10m-old success stayed).
func TestKeepWindowAppliesToEveryStatus(t *testing.T) {
	for _, tc := range []struct {
		status string
		failed bool
		age    time.Duration
	}{
		{"success", false, time.Minute},
		{"success", false, 10 * time.Minute},
		{"success", false, 2 * time.Hour},
		{"failed", true, time.Minute},
		{"failed", true, 10 * time.Minute},
		{"failed", true, 2 * time.Hour},
		{"halted", true, time.Minute},
		{"halted", true, 10 * time.Minute},
		{"halted", true, 2 * time.Hour},
	} {
		t.Run(tc.status+"/"+tc.age.String(), func(t *testing.T) {
			r := RunRow{Run: "r1", Finished: true, Failed: tc.failed, Age: tc.age}
			if tc.status == "halted" {
				r.Halted = "budget"
			}
			v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{r}}}}
			f := Filter{Keep: 24 * time.Hour, KeepCount: 15, FailedKeep: FinishedKeep}
			got, hidden := f.Apply(v, nil)
			if len(got.Repos[0].Finished) != 1 || hidden != 0 {
				t.Fatalf("--keep 24h hid the %s run %v old: kept %+v, hidden %d",
					tc.status, tc.age, got.Repos[0].Finished, hidden)
			}
		})
	}
}

// --keep-count counts a failed row like any other finished row (0.10.2):
// with --keep-count 1 only the newest finished row stays, whatever its
// status, so a fresh failure displaces an older success and an older failure
// is displaced by a fresh success. Mutation caught: a failure branch that
// skips the count, which would keep every failure in the window however many
// successes --keep-count allows.
func TestKeepCountAppliesToFailures(t *testing.T) {
	f := Filter{Keep: 24 * time.Hour, KeepCount: 1, FailedKeep: FinishedKeep}
	fresh := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{
		{Run: "failed-now", Finished: true, Failed: true, Age: time.Minute},
		{Run: "ok-earlier", Finished: true, Age: 2 * time.Minute},
	}}}}
	got, hidden := f.Apply(fresh, nil)
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "failed-now" || hidden != 1 {
		t.Fatalf("--keep-count 1 over a fresh failure: kept %+v, hidden %d, want only failed-now",
			got.Repos[0].Finished, hidden)
	}
	old := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{
		{Run: "ok-now", Finished: true, Age: time.Minute},
		{Run: "failed-earlier", Finished: true, Failed: true, Age: 2 * time.Minute},
	}}}}
	got, hidden = f.Apply(old, nil)
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "ok-now" || hidden != 1 {
		t.Fatalf("--keep-count 1 over an older failure: kept %+v, hidden %d, want only ok-now",
			got.Repos[0].Finished, hidden)
	}
}

// FailedKeep's edge is the same strict "younger than" boundary as the
// success rule's Keep (docs/design/generic-tool.md §10.3, docs/plans/...
// Task 6): a failure exactly 24h old, or older, is hidden; one younger is
// kept. TestFailedRunsStayUntilAcked only exercises 10h, well inside the
// window either way, so it cannot tell a strict `<` from a `<=`.
func TestFailedKeepBoundaryIsStrictlyYoungerThan(t *testing.T) {
	f := Filter{Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{
		{Run: "old", Finished: true, Failed: true, Age: 30 * time.Hour},
		{Run: "at-edge", Finished: true, Failed: true, Age: 24 * time.Hour},
		{Run: "young", Finished: true, Failed: true, Age: 23 * time.Hour},
	}}}}
	got, hidden := f.Apply(v, nil)
	if len(got.Repos[0].Finished) != 1 || got.Repos[0].Finished[0].Run != "young" {
		t.Fatalf("got %+v, want only the 23h-old failure", got.Repos[0].Finished)
	}
	if hidden != 2 {
		t.Fatalf("hidden = %d, want 2 (the 30h and the exactly-24h failures)", hidden)
	}
}

// All bypasses both the age check and the ack check for a failure, not just
// one of them: an old, unacknowledged failure and a young, acknowledged one
// must both reappear under --all.
func TestAllBypassesBothFailureRules(t *testing.T) {
	f := Filter{All: true, Keep: 6 * time.Hour, KeepCount: 15, FailedKeep: 24 * time.Hour}
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{
		{Run: "old", Finished: true, Failed: true, Age: 100 * time.Hour},
		{Run: "acked", Finished: true, Failed: true, Age: time.Hour},
	}}}}
	got, hidden := f.Apply(v, func(_, run string) bool { return run == "acked" })
	if len(got.Repos[0].Finished) != 2 || hidden != 0 {
		t.Fatalf("got %+v hidden %d, want both failures kept under --all", got.Repos[0].Finished, hidden)
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

// A block with no runs, no spend and no kill switch is still kept when a
// finished row of its own survived the filter: len(b.Finished) > 0 is its
// own, independent reason to keep a block, not merely incidental to one of
// the others DropEmptyFinishedBlocks also checks.
func TestDropEmptyFinishedBlocksKeepsABlockWithSurvivingFinished(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Finished: []RunRow{{Run: "recent", Age: time.Minute}}}}}
	got := DropEmptyFinishedBlocks(v)
	if len(got.Repos) != 1 || len(got.Repos[0].Finished) != 1 {
		t.Fatalf("got %+v, want the block kept for its surviving finished row", got.Repos)
	}
}
