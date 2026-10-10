package watch

import (
	"testing"
	"time"
)

// --json is the machine view and stays unfiltered (design generic-tool
// §10.3): it gains "finished" (every finished row) and "ready" (just the
// ones whose outcome is ready) arrays, regardless of age or count.
func TestBuildJSONIncludesFinishedAndReady(t *testing.T) {
	v := View{Repos: []RepoBlock{{Slug: "a", Name: "acme/app"}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-ready", Slug: "a", Title: "Fix the bug", Status: "succeeded", Outcome: "ready",
			PRURL: "https://github.com/acme/app/pull/7", PRNumber: 7, FinishedAt: t0.Add(-30 * 24 * time.Hour)},
		{Run: "r-failed", Slug: "a", Title: "Half-done", Status: "failed", FinishedAt: t0.Add(-time.Hour)},
	}, t0)
	doc := BuildJSON("aurora", v)
	if len(doc.Finished) != 2 {
		t.Fatalf("finished = %+v, want both rows even though one is 30 days old", doc.Finished)
	}
	if len(doc.Ready) != 1 || doc.Ready[0].Run != "r-ready" || doc.Ready[0].PRURL != "https://github.com/acme/app/pull/7" {
		t.Fatalf("ready = %+v, want only r-ready", doc.Ready)
	}
	for _, f := range doc.Finished {
		if f.Run == "r-failed" && !f.Failed {
			t.Fatalf("r-failed not flagged failed: %+v", f)
		}
	}
}
