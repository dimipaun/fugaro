package runindex

import (
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// folded is the fixture's index folded through 11:45, with one run in it: the
// state a 15-minute writer leaves behind, which is what the overlay exists to
// cover.
func folded(t *testing.T) *indexFixture {
	t.Helper()
	f := newIndexFixture(t)
	f.seedSummary(Summary{
		Version: Version, Complete: true,
		FoldedThrough: ptrTime(indexAt(11, 45)), UpdatedAt: indexAt(11, 45),
		Runs: 1, States: map[string]int64{"succeeded": 1},
	})
	f.seed(Entry{RunID: runMid, Slug: pageSlug, UpdatedAt: indexAt(9, 0), Status: runstore.StatusSucceeded})
	return f
}

// TestOverlayAddsOutcomesSinceFold is RI3's red step: a page built from the
// indexed rows alone is short, and the run that finished minutes ago is in the
// budget database's outcomes rather than in the index (D3202, F9).
func TestOverlayAddsOutcomesSinceFold(t *testing.T) {
	// The indexed rows alone: the fold has not run since the run ended, so
	// nothing in the index knows it exists.
	if got, want := ids(folded(t).list(Filter{})), []string{runMid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the indexed rows = %v, want %v", got, want)
	}

	f := folded(t)
	f.seedOutcome(otherSlug, runFresh, runstore.StatusFailed)
	page := f.list(Filter{})
	// The run is listed, and above the rows the fold has already read: the
	// overlay's sort key is floored at the index's own stamp, because the index
	// carries every run that changed before it.
	if got, want := ids(page), []string{runFresh, runMid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page = %v, want %v", got, want)
	}
	row := rowOf(t, page, runFresh)
	if row.Slug != otherSlug || row.Status != runstore.StatusFailed {
		t.Fatalf("the fresh run = %+v", row)
	}
	if row.UpdatedAt.Before(indexAt(11, 45)) {
		t.Fatalf("the fresh run sorts below the fold's own stamp: %v", row.UpdatedAt)
	}
	// The page's own account of the index is unchanged: the overlay adds rows
	// to it and does not stand in for it.
	if !page.IndexComplete || page.FoldedThrough == nil || !page.FoldedThrough.Equal(indexAt(11, 45)) {
		t.Fatalf("the page's summary = complete %v, folded through %v", page.IndexComplete, page.FoldedThrough)
	}
}

// TestOverlayOnlyAddsRowsTheFilterCanAnswer: an overlay row is a run id, the
// slug the path names, a status and a moment, so a page read per repository or
// per state is answered from it exactly and a page read by a plan task, a pull
// request, an identity or a batch is not answered from it at all.
func TestOverlayOnlyAddsRowsTheFilterCanAnswer(t *testing.T) {
	f := folded(t)
	f.seed(Entry{RunID: runNewest, Slug: pageSlug, UpdatedAt: indexAt(11, 0), Status: runstore.StatusSucceeded,
		PR: 12, PlanTask: "RI3"})
	f.seedOutcome(otherSlug, runFresh, runstore.StatusFailed)
	f.seedOutcome(pageSlug, runOldest, runstore.StatusSucceeded)

	// The two the overlay alone supplies are both listed, above the rows the
	// fold has already read and newest run id first among themselves.
	if got, want := ids(f.list(Filter{})), []string{runFresh, runOldest, runNewest, runMid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page = %v, want %v", got, want)
	}
	for _, c := range []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"one repository", Filter{Slugs: []string{otherSlug}}, []string{runFresh}},
		{"one state", Filter{States: []runstore.Status{runstore.StatusFailed}}, []string{runFresh}},
		{"one plan task", Filter{PlanTask: "RI3"}, []string{runNewest}},
		{"one pull request", Filter{PR: 12}, []string{runNewest}},
	} {
		if got := ids(f.list(c.filter)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestOverlayStatusOnConflict is the merge rule of D3202: the index wins
// unless its own account is unsettled.
func TestOverlayStatusOnConflict(t *testing.T) {
	f := folded(t)
	// The fold read this one while it was going: the outcome is a change it has
	// not read yet, and the overlay is what says the run ended.
	f.seed(Entry{RunID: runNewest, Slug: pageSlug, UpdatedAt: indexAt(11, 0), Status: runstore.StatusRunning})
	// The fold read this one as settled: it is the fold's own reading of the
	// record the run wrote, and an outcome does not override it.
	f.seed(Entry{RunID: runTieLow, Slug: pageSlug, UpdatedAt: indexAt(10, 0), Status: runstore.StatusFailed})
	// The fold read this one with no status at all: an unlaunched run, or one
	// that died before finalize.
	f.seed(Entry{RunID: runOldest, Slug: pageSlug, UpdatedAt: indexAt(9, 30)})
	f.seedOutcome(pageSlug, runNewest, runstore.StatusSucceeded)
	f.seedOutcome(pageSlug, runTieLow, runstore.StatusSucceeded)
	f.seedOutcome(pageSlug, runOldest, runstore.StatusCancelled)
	// A run the index does not carry at all, whose own registry entry says it
	// is going: it is listed as the run its record would be listed as while it
	// goes, which is `running`.
	f.seedAgent(otherSlug, runLive, budget.AgentEntry{
		StartedAt: indexNow.Add(-4 * time.Minute).UnixMilli(),
		UpdatedAt: indexNow.Add(-time.Minute).UnixMilli(),
	})

	page := f.list(Filter{})
	for _, c := range []struct {
		run  string
		want runstore.Status
	}{
		{runNewest, runstore.StatusSucceeded},
		{runTieLow, runstore.StatusFailed},
		{runOldest, runstore.StatusCancelled},
		{runLive, runstore.StatusRunning},
		{runMid, runstore.StatusSucceeded},
	} {
		if got := rowOf(t, page, c.run).Status; got != c.want {
			t.Errorf("%s = %q, want %q", c.run, got, c.want)
		}
	}
	if got, want := rowOf(t, page, runLive).UpdatedAt, indexNow.Add(-time.Minute); !got.Equal(want) {
		t.Errorf("the live run's sort key = %v, want its own heartbeat %v", got, want)
	}

	// The active set is the runs in flight, whatever their age (D3203): the one
	// the registry says is going, and not the one the overlay settled away from
	// the `running` the fold had read.
	if got, want := ids(f.list(Filter{Active: true})), []string{runLive}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the active set = %v, want %v", got, want)
	}
}

// TestOverlaySettlesTheStateAPageWasReadBy: a page read per state is a page
// about the state the run is in, and the merge is what decides it. An entry the
// fold read as going, and an outcome that settled it, is not a row of a page of
// going runs -- and it is a row of the page of the state it ended in, which the
// index does not carry yet either.
func TestOverlaySettlesTheStateAPageWasReadBy(t *testing.T) {
	f := folded(t)
	f.seed(Entry{RunID: runNewest, Slug: pageSlug, UpdatedAt: indexAt(11, 0), Status: runstore.StatusRunning})
	f.seedOutcome(pageSlug, runNewest, runstore.StatusFailed)

	if got := ids(f.list(Filter{States: []runstore.Status{runstore.StatusRunning}})); len(got) != 0 {
		t.Fatalf("the running page = %v, want no rows: the outcome settled the run as failed", got)
	}
	if got, want := ids(f.list(Filter{States: []runstore.Status{runstore.StatusFailed}})), []string{runNewest}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the failed page = %v, want %v: the index has the run as going, and the outcome is what settled it", got, want)
	}
}

// TestOverlayNeverSuppliesPRorIdentity is F9: both overlay nodes are written by
// the run's own service account, so nothing they say about the run is read --
// only that it exists, whether it is going, and how it ended.
func TestOverlayNeverSuppliesPRorIdentity(t *testing.T) {
	f := folded(t)
	// Everything a row could show, in the run's own registry entry, and a
	// requester in the outcome.
	f.seedAgent(otherSlug, runLive, budget.AgentEntry{
		Repo: "acme/other", Workflow: "fix", Title: "a title the run chose",
		Stage: "implement", PRURL: "https://example.invalid/pr/99",
		RequestedBy: "dev@example.invalid", Action: "ran a command",
		StartedAt: indexNow.Add(-4 * time.Minute).UnixMilli(),
		UpdatedAt: indexNow.Add(-time.Minute).UnixMilli(),
	})
	f.seedOutcome(pageSlug, runFresh, runstore.StatusSucceeded)

	page := f.list(Filter{})
	for _, c := range []struct {
		run  string
		want Entry
	}{
		// The whole row: the run id and the slug the path names, the status the
		// overlay can account for, and nothing else.
		{runLive, Entry{RunID: runLive, Slug: otherSlug, Status: runstore.StatusRunning}},
		{runFresh, Entry{RunID: runFresh, Slug: pageSlug, Status: runstore.StatusSucceeded}},
	} {
		got := rowOf(t, page, c.run)
		got.UpdatedAt = time.Time{} // the overlay's own moment, asserted below
		if got != c.want {
			t.Errorf("the row for %s is %+v, want %+v", c.run, got, c.want)
		}
	}
	if got, want := rowOf(t, page, runLive).UpdatedAt, indexNow.Add(-time.Minute); !got.Equal(want) {
		t.Errorf("the live run's sort key = %v, want its own heartbeat %v", got, want)
	}
	if got, want := rowOf(t, page, runFresh).UpdatedAt, indexAt(11, 45); !got.Equal(want) {
		t.Errorf("the fresh run's sort key = %v, want the fold's own stamp %v", got, want)
	}
}

// TestOverlayReadsTheDaysSinceTheFoldAndNoMore pins the bound the overlay
// inherits: the rollover prunes an outcome day only after the fold has read it,
// so the overlay reads from the stamp to today and never further back than
// PruneAfter (RI4, RI5).
func TestOverlayReadsTheDaysSinceTheFoldAndNoMore(t *testing.T) {
	f := newIndexFixture(t)
	stale := indexNow.AddDate(0, 0, -budget.PruneAfter-3)
	f.seedSummary(Summary{
		Version: Version, Complete: false, FoldedThrough: ptrTime(stale),
		UpdatedAt: indexNow, States: map[string]int64{},
	})
	f.seedOutcome(otherSlug, runFresh, runstore.StatusFailed)
	f.db.Set(budget.PathOutcome(budget.Day(stale), pageSlug, runOldest), budget.Outcome{
		Status: string(runstore.StatusSucceeded), RequestedBy: "dev@example.invalid",
	})
	if got, want := ids(f.list(Filter{})), []string{runFresh}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page = %v, want %v: a day the overlay does not read was listed", got, want)
	}
	days := overlayDays(ptrTime(stale), indexNow)
	if len(days) != budget.PruneAfter+1 || days[0] != budget.Day(indexNow)-budget.PruneAfter || days[len(days)-1] != budget.Day(indexNow) {
		t.Fatalf("days = %v, want the last %d up to today", days, budget.PruneAfter+1)
	}
}
