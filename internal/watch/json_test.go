package watch

import (
	"encoding/json"
	"strings"
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

// watch --json carries a run's plan task id, on the live rows and on the
// finished ones: it is the join hint the console's graph screen matches a
// run to a node of a plan graph by (task GV1.2 of the console dependency
// graph plan, decision GD5).
//
// It comes the same way the launcher does — from the run's own launch.json,
// read by the scan and stamped onto the rows by MergeFacts — and is
// sanitised the same way, because it is text a launcher wrote. A run whose
// launch record has none carries no key at all: absent means "not
// recorded", never an empty id.
//
// Mutation (leaving PlanTask off RunFacts or off MergeFacts): a live run
// that has one shows nothing, and the first assertion fails; mutation
// (leaving it off jsonFinished): only the finished row's fails.
func TestWatchJSONPlanTask(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme__app", Name: "acme/app", Runs: []RunRow{
			mkRun("r-live", "Ship W1.3", "implement", nil),
			mkRun("r-other", "Unjoined work", "implement", nil),
		}}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-done", Slug: "acme__app", Title: "Old work", Status: "succeeded", Outcome: "ready",
			FinishedAt: t0.Add(-time.Hour)},
	}, t0)
	v = MergeFacts(v, Facts{
		{Slug: "acme__app", Run: "r-live"}:  {PlanTask: "setup-wizard/W1.3"},
		{Slug: "acme__app", Run: "r-done"}:  {PlanTask: "plans/2026-10-15-desktop-console-v2/GV1.2"},
		{Slug: "acme__app", Run: "r-other"}: {},
	})
	doc := BuildJSON("aurora", v)
	if len(doc.Runs) != 2 || len(doc.Finished) != 1 {
		t.Fatalf("runs = %d, finished = %d", len(doc.Runs), len(doc.Finished))
	}
	live := map[string]string{}
	for _, r := range doc.Runs {
		live[r.Run] = r.PlanTask
	}
	if live["r-live"] != "setup-wizard/W1.3" {
		t.Fatalf("r-live plan_task = %q, want the launch.json's", live["r-live"])
	}
	if live["r-other"] != "" {
		t.Fatalf("r-other plan_task = %q, want none", live["r-other"])
	}
	if doc.Finished[0].PlanTask != "plans/2026-10-15-desktop-console-v2/GV1.2" {
		t.Fatalf("finished plan_task = %q", doc.Finished[0].PlanTask)
	}
	if len(doc.Ready) != 1 || doc.Ready[0].PlanTask != doc.Finished[0].PlanTask {
		t.Fatalf("ready plan_task = %+v, want the finished row's", doc.Ready)
	}
	// The key is absent, never "", on a row whose launch record has none.
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"plan_task"`) != 3 {
		t.Fatalf("plan_task keys = %d, want 3 (the two joined rows and the ready row):\n%s",
			strings.Count(string(data), `"plan_task"`), data)
	}
}

// TestMergeFactsDropsAPlanTaskThatIsNotOne: launch.json is writable by the
// run's own service account, so MergeFacts keeps a plan task only when it
// passes the launcher's shape check (task.PlanTaskProblem), as runview.Join
// does for runs ls --json: a traversal, spaces or 500 characters reach
// neither a live row nor a finished one.
//
// Mutation caught: sanitising and capping only.
func TestMergeFactsDropsAPlanTaskThatIsNotOne(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme__app", Name: "acme/app", Runs: []RunRow{
			mkRun("r-dots", "a", "implement", nil),
			mkRun("r-spaces", "b", "implement", nil),
			mkRun("r-long", "c", "implement", nil),
		}}}}
	v = MergeFinished(v, Config{}, []FinishedRun{
		{Run: "r-done", Slug: "acme__app", Title: "Old work", Status: "succeeded", Outcome: "ready",
			FinishedAt: t0.Add(-time.Hour)},
	}, t0)
	v = MergeFacts(v, Facts{
		{Slug: "acme__app", Run: "r-dots"}:   {PlanTask: "../x"},
		{Slug: "acme__app", Run: "r-spaces"}: {PlanTask: "setup wizard/W 1.3"},
		{Slug: "acme__app", Run: "r-long"}:   {PlanTask: "plan/" + strings.Repeat("x", 495)},
		{Slug: "acme__app", Run: "r-done"}:   {PlanTask: "../../etc/x"},
	})
	data, err := json.Marshal(BuildJSON("aurora", v))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"plan_task"`) {
		t.Fatalf("a misshapen plan task reached watch --json:\n%s", data)
	}
}
