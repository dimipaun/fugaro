package watch

import (
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
	got := MergeFinished(v, fin, now)
	b := got.Repos[0]
	if len(b.Runs) != 1 || len(b.Finished) != 2 {
		t.Fatalf("runs %d finished %d", len(b.Runs), len(b.Finished))
	}
	if b.Finished[0].Run != "r-new" || !b.Finished[0].Failed || b.Finished[1].PRURL != "https://github.com/o/r/pull/7" {
		t.Fatalf("finished rows = %+v", b.Finished)
	}
}
