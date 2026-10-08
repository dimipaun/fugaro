package watch

import (
	"testing"
	"time"
)

func TestMergeQueuedNoOpWithNothingQueued(t *testing.T) {
	v := fixture()
	got := MergeQueued(v, Config{}, nil, "", t0)
	if len(got.Repos) != len(v.Repos) || got.QueuedNote != "" {
		t.Fatalf("an empty queue must leave the view untouched: %+v", got)
	}
}

func TestMergeQueuedAddsRowToExistingRepo(t *testing.T) {
	v := fixture()
	q := []QueuedRun{{Run: "r-queued1", Slug: "acme__app", Workflow: "web", Recipe: "fast", RequestedBy: "a@b.c", LaunchedAt: t0.Add(-2 * time.Minute)}}
	got := MergeQueued(v, Config{}, q, "", t0)
	var app RepoBlock
	for _, b := range got.Repos {
		if b.Slug == "acme__app" {
			app = b
		}
	}
	if len(app.Runs) != 3 {
		t.Fatalf("want the two live runs plus the queued one, got %d: %+v", len(app.Runs), app.Runs)
	}
	var row RunRow
	found := false
	for _, r := range app.Runs {
		if r.Run == "r-queued1" {
			row, found = r, true
		}
	}
	if !found {
		t.Fatalf("queued run missing: %+v", app.Runs)
	}
	if !row.Queued || row.Stuck || row.Stage != "queued" || row.Title != "web" || row.Recipe != "fast" || row.RequestedBy != "a@b.c" {
		t.Fatalf("row = %+v", row)
	}
	if !row.HasAge || row.Age != 2*time.Minute {
		t.Fatalf("age = %v hasAge=%v", row.Age, row.HasAge)
	}
}

func TestMergeQueuedCreatesRepoBlock(t *testing.T) {
	v := fixture()
	q := []QueuedRun{{Run: "r-new1", Slug: "acme__new", Workflow: "web", LaunchedAt: t0.Add(-time.Minute)}}
	got := MergeQueued(v, Config{RepoNames: map[string]string{"acme__new": "acme/new"}}, q, "", t0)
	var nb *RepoBlock
	for i := range got.Repos {
		if got.Repos[i].Slug == "acme__new" {
			nb = &got.Repos[i]
		}
	}
	if nb == nil {
		t.Fatalf("no block created for the queued-only repository: %+v", got.Repos)
	}
	if nb.Name != "acme/new" || len(nb.Runs) != 1 || nb.Runs[0].Run != "r-new1" {
		t.Fatalf("block = %+v", nb)
	}
	if nb.Bar.Has || nb.Counted != 0 {
		t.Fatalf("a queued-only repository has no spend: %+v", nb)
	}
}

// A run whose registry entry already shows it live must never also appear
// queued: the poll that found the claim and the registry entry can race, and
// the live row always wins.
func TestMergeQueuedSkipsAlreadyLiveRun(t *testing.T) {
	v := fixture() // acme__app already has r-aaaa11 and r-bbbb22 live
	q := []QueuedRun{{Run: "r-aaaa11", Slug: "acme__app", LaunchedAt: t0.Add(-time.Minute)}}
	got := MergeQueued(v, Config{}, q, "", t0)
	for _, b := range got.Repos {
		if b.Slug != "acme__app" {
			continue
		}
		if len(b.Runs) != 2 {
			t.Fatalf("a live run must not duplicate as queued: %+v", b.Runs)
		}
		for _, r := range b.Runs {
			if r.Run == "r-aaaa11" && r.Queued {
				t.Fatalf("the live row was replaced by a queued one: %+v", r)
			}
		}
	}
}

func TestMergeQueuedStuckAfterThreshold(t *testing.T) {
	v := fixture()
	q := []QueuedRun{
		{Run: "r-fresh1", Slug: "acme__app", LaunchedAt: t0.Add(-QueuedStuckAfter + time.Second)},
		{Run: "r-stale1", Slug: "acme__app", LaunchedAt: t0.Add(-QueuedStuckAfter - time.Second)},
	}
	got := MergeQueued(v, Config{}, q, "", t0)
	rows := map[string]RunRow{}
	for _, b := range got.Repos {
		if b.Slug != "acme__app" {
			continue
		}
		for _, r := range b.Runs {
			rows[r.Run] = r
		}
	}
	if rows["r-fresh1"].Stuck {
		t.Fatalf("not yet past the threshold: %+v", rows["r-fresh1"])
	}
	if !rows["r-stale1"].Stuck {
		t.Fatalf("past the threshold: %+v", rows["r-stale1"])
	}
}

func TestMergeQueuedNoteIsSanitisedAndKeptEvenWhenEmpty(t *testing.T) {
	v := fixture()
	got := MergeQueued(v, Config{}, nil, "runs bucket unreachable: \x1b[31mboom", t0)
	if got.QueuedNote == "" || got.QueuedNote == v.QueuedNote {
		t.Fatalf("note not carried: %q", got.QueuedNote)
	}
	for _, c := range got.QueuedNote {
		if c == 0x1b {
			t.Fatalf("an escape reached the note: %q", got.QueuedNote)
		}
	}
}
