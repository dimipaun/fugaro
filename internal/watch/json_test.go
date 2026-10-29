package watch

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
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

// watch --json carries a live run's draft pull request number (task GV2.5 of
// the console dependency graph plan): a draft PR opens early in a run, so the
// row the feed draws while the run is still working carries the number, and the
// console's graph screen does not have to wait for the run to finish (or for a
// `plan status` refresh) to show it.
//
// It is the number the run's own record holds (runview.Row.RecordPR, the value
// `runs ls --json` prints as record_pr): the run writes the registry entry's
// prUrl from that same record the moment its draft PR opens
// (internal/runner/prflow.go's notePR, and checkPullRequest for a follow-up),
// and the entry is what a live row is built from — so no new read is made for
// it. The number comes out of that URL through this package's own anchored
// GitHub pattern (ghPRURL, internal/watch/prstate.go), the reading MergeNow and
// PRRepo do before they build gh's argv, where the number is already an int
// (prRef.number): the URL is structure here, not text to scrape.
//
// Mutation (leaving the field off the live row's JSONRun): the first assertion
// fails, the document carrying no pr_number at all.
func TestLiveRunCarriesDraftPR(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme__app", Name: "acme/app", Runs: []RunRow{
			runRow("acme__app", "r-draft", budget.AgentEntry{Repo: "acme/app", Stage: "implement",
				PRURL: "https://github.com/acme/app/pull/412"}, t0),
		}}}}
	doc := BuildJSON("aurora", v)
	if len(doc.Runs) != 1 {
		t.Fatalf("runs = %+v, want the one live row", doc.Runs)
	}
	if doc.Runs[0].PRNumber != 412 {
		t.Fatalf("live pr_number = %d, want the registry prUrl's 412", doc.Runs[0].PRNumber)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"pr_number":412`) {
		t.Fatalf("the live document carries no pr_number:\n%s", data)
	}
}

// TestLiveRunWithoutPRHasNoField: a live run that has recorded no pull request
// carries no `pr_number` key at all — neither a run that has opened none nor
// one whose registry prUrl is not a GitHub pull request URL, which this
// package's pattern reads as no number rather than as a guess. Absent means
// "none recorded", never `0`: a repository numbers its pull requests from 1, so
// a zero would be a number no run can have, and the console reads the two
// differently (`optionalInt` maps absent to `null`).
func TestLiveRunWithoutPRHasNoField(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-03", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Repos: []RepoBlock{{Slug: "acme__app", Name: "acme/app", Runs: []RunRow{
			runRow("acme__app", "r-none", budget.AgentEntry{Repo: "acme/app", Stage: "implement"}, t0),
			runRow("acme__app", "r-bitbucket", budget.AgentEntry{Repo: "acme/app", Stage: "implement",
				PRURL: "https://bitbucket.org/acme/app/pull-requests/9"}, t0),
		}}}}
	doc := BuildJSON("aurora", v)
	if len(doc.Runs) != 2 {
		t.Fatalf("runs = %+v, want both live rows", doc.Runs)
	}
	for _, r := range doc.Runs {
		if r.PRNumber != 0 {
			t.Fatalf("%s pr_number = %d, want none", r.Run, r.PRNumber)
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pr_number") {
		t.Fatalf("a live run with no pull request carries the key:\n%s", data)
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

// watch --json carries leased_usd on the total and on every repository row:
// counted minus spent, floored at zero, always present so a reader never has
// to tell "nothing is leased" from "this document does not say" (the console
// reads the key, and a zero there is a measured zero).
//
// Mutation (dropping the floor): a state no normal write makes -- counted
// below spent -- would put a negative figure on the wire, and the second case
// fails. Mutation (setting only the total's): the repository case fails.
func TestWatchJSONCarriesLeasedUSD(t *testing.T) {
	v := View{Now: t0, Day: "2026-10-29", Mode: "enforce", Conn: Connection{Kind: ConnLive},
		Project: ProjectLine{Counted: 47_310_000, Spent: 47_110_000},
		Repos: []RepoBlock{
			{Slug: "acme__app", Name: "acme/app", Counted: 47_310_000, Spent: 47_110_000},
			// counted below spent: never a negative figure.
			{Slug: "acme__under", Name: "acme/under", Counted: 1_000_000, Spent: 2_000_000},
		}}
	doc := BuildJSON("aurora", v)
	if doc.Total.LeasedUSD != 0.2 {
		t.Errorf("total.leased_usd = %v, want 0.20 (counted 47.31 - spent 47.11)", doc.Total.LeasedUSD)
	}
	if got := doc.Repos[0].LeasedUSD; got != 0.2 {
		t.Errorf("repos[0].leased_usd = %v, want 0.20", got)
	}
	if got := doc.Repos[1].LeasedUSD; got != 0 {
		t.Errorf("repos[1].leased_usd = %v, want 0 (counted below spent is floored, never negative)", got)
	}

	// No leases at all -- nothing reserved, nothing spent -- is a measured 0,
	// and the key is on the wire either way: it has no omitempty.
	data, err := json.Marshal(BuildJSON("aurora", View{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"leased_usd":0`) {
		t.Fatalf("a document with nothing leased does not carry leased_usd:0:\n%s", data)
	}
}
