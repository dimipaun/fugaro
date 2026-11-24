package runindex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The two repositories and the runs the reader tests are written over: five
// indexed runs, two of them sharing a sort key, and two the index does not
// carry -- one that finished after the fold and one still going.
const (
	pageSlug  = "aurora-app"
	otherSlug = "birch-api"

	runNewest  = "20261122-110000-dddd"
	runTieHigh = "20261122-100000-cccc"
	runTieLow  = "20261122-100000-bbbb"
	runMid     = "20261122-090000-aaaa"
	runOldest  = "20261122-080000-eeee"
	runFresh   = "20261122-115500-a1b2"
	runLive    = "20261122-115900-c3d4"
)

// indexNow is the moment the reader reads at. It is fixed because what a live
// registry entry's freshness is measured against is the moment of the read,
// and a fixture that seeded a heartbeat has to say what that moment is.
var indexNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// indexAt is a moment of the same UTC day.
func indexAt(h, m int) time.Time { return time.Date(2026, 11, 22, h, m, 0, 0, time.UTC) }

func ptrTime(t time.Time) *time.Time { return &t }

// indexFixture is an installation's index: the Firestore database the history
// job's fold writes and the budget database the overlay reads, both the fakes
// the rest of the tree is tested against.
type indexFixture struct {
	t    *testing.T
	fs   *gcpfake.Firestore
	db   *gcpfake.RTDB
	read Reader
}

func newIndexFixture(t *testing.T) *indexFixture {
	t.Helper()
	f := &indexFixture{t: t, fs: gcpfake.NewFirestore(t), db: gcpfake.NewRTDB(t)}
	f.db.SetClock(func() time.Time { return indexNow })
	client, err := firestore.New(f.fs.URL, "aurora-fp", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "operator"}))
	if err != nil {
		t.Fatal(err)
	}
	live, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return "operator" }})
	if err != nil {
		t.Fatal(err)
	}
	f.read = NewFirestore(client, live, WithClock(func() time.Time { return indexNow }))
	return f
}

// seed writes one run's index document.
func (f *indexFixture) seed(e Entry) {
	f.t.Helper()
	fields := testDocFields(f.t, e)
	// The round trip leaves a moment as the string the wire format spells it
	// with; the document carries the timestamp the fold writes.
	fields["updated_at"] = e.UpdatedAt
	if e.StartedAt != nil {
		fields["started_at"] = *e.StartedAt
	}
	if e.FinishedAt != nil {
		fields["finished_at"] = *e.FinishedAt
	}
	f.fs.Set(entryCollection, e.RunID, fields)
}

// seedSummary writes the installation's summary document.
func (f *indexFixture) seedSummary(s Summary) {
	f.t.Helper()
	fields := testDocFields(f.t, s)
	fields["updated_at"] = s.UpdatedAt
	if s.FoldedThrough != nil {
		fields["folded_through"] = *s.FoldedThrough
	}
	f.fs.Set(summaryCollection, summaryDocID, fields)
}

// seedAgent is a run's own registry entry: a run in flight writes one and
// deletes it as it ends.
func (f *indexFixture) seedAgent(slug, run string, e budget.AgentEntry) {
	f.db.Set(budget.PathAgent(slug, run), e)
}

// seedOutcome is a run's settled outcome, under the UTC day it ended on.
func (f *indexFixture) seedOutcome(slug, run string, status runstore.Status) {
	f.db.Set(budget.PathOutcome(budget.Day(indexNow), slug, run), budget.Outcome{
		Status: string(status), RequestedBy: "dev@example.invalid",
	})
}

func (f *indexFixture) list(filter Filter) Page {
	f.t.Helper()
	page, err := f.read.List(context.Background(), filter)
	if err != nil {
		f.t.Fatalf("List(%+v): %v", filter, err)
	}
	return page
}

// testDocFields is a value's own fields as the fold writes them: the wire format's
// names, which are the struct's own.
func testDocFields(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func ids(page Page) []string {
	out := make([]string, 0, len(page.Entries))
	for _, e := range page.Entries {
		out = append(out, e.RunID)
	}
	return out
}

// rowOf is one run's row of a page.
func rowOf(t *testing.T, page Page, run string) Entry {
	t.Helper()
	for _, e := range page.Entries {
		if e.RunID == run {
			return e
		}
	}
	t.Fatalf("no row for %s in %v", run, ids(page))
	return Entry{}
}

// seeded is the fixture's index: five runs of two repositories, two of them
// sharing a sort key so the tie-break is pinned, folded through 11:30.
func seeded(t *testing.T) *indexFixture {
	t.Helper()
	f := newIndexFixture(t)
	f.seedSummary(Summary{
		Version: Version, Complete: true,
		FoldedThrough: ptrTime(indexAt(11, 30)), UpdatedAt: indexAt(11, 30),
		Runs: 5, States: map[string]int64{"succeeded": 2, "failed": 1, "unreviewed": 1, "cancelled": 1},
	})
	f.seed(Entry{RunID: runMid, Slug: pageSlug, UpdatedAt: indexAt(9, 0), Status: runstore.StatusSucceeded,
		Title: "the run a person reads", PR: 12, PlanTask: "RI3", RequestedBy: "dev@example.invalid"})
	f.seed(Entry{RunID: runTieLow, Slug: pageSlug, UpdatedAt: indexAt(10, 0), Status: runstore.StatusFailed})
	f.seed(Entry{RunID: runTieHigh, Slug: otherSlug, UpdatedAt: indexAt(10, 0), Status: runstore.StatusUnreviewed})
	f.seed(Entry{RunID: runNewest, Slug: pageSlug, UpdatedAt: indexAt(11, 0), Status: runstore.StatusSucceeded})
	f.seed(Entry{RunID: runOldest, Slug: otherSlug, UpdatedAt: indexAt(8, 0), Status: runstore.StatusCancelled})
	return f
}

func TestFirestoreReaderPages(t *testing.T) {
	f := seeded(t)

	t.Run("newest first, and a tie on the sort key is broken by the run id", func(t *testing.T) {
		page := f.list(Filter{})
		want := []string{runNewest, runTieHigh, runTieLow, runMid, runOldest}
		if got := ids(page); !reflect.DeepEqual(got, want) {
			t.Fatalf("page = %v, want %v", got, want)
		}
		if !page.IndexComplete || page.FoldedThrough == nil || !page.FoldedThrough.Equal(indexAt(11, 30)) {
			t.Fatalf("the page says nothing about the index: complete %v, folded through %v", page.IndexComplete, page.FoldedThrough)
		}
		// The row is the entry the fold wrote, whole: a reader that wants to
		// print a run reads it and needs no second read.
		row := rowOf(t, page, runMid)
		if row.Title != "the run a person reads" || row.PR != 12 || row.PlanTask != "RI3" ||
			row.RequestedBy != "dev@example.invalid" || row.Status != runstore.StatusSucceeded || row.Slug != pageSlug {
			t.Fatalf("the row lost what the entry carried: %+v", row)
		}
	})

	t.Run("a page continues after the run id it was handed", func(t *testing.T) {
		page := f.list(Filter{Limit: 2})
		if got, want := ids(page), []string{runNewest, runTieHigh}; !reflect.DeepEqual(got, want) {
			t.Fatalf("page = %v, want %v", got, want)
		}
		if page.Next == nil || page.Next.RunID != runTieHigh || page.Next.UpdatedAt == nil || !page.Next.UpdatedAt.Equal(indexAt(10, 0)) {
			t.Fatalf("Next = %+v, want the cursor of %s", page.Next, runTieHigh)
		}
		// The tie is the case that matters: the page resumes inside two rows
		// that share a sort key, so the cursor has to be the pair.
		page = f.list(Filter{Limit: 2, After: page.Next.RunID})
		if got, want := ids(page), []string{runTieLow, runMid}; !reflect.DeepEqual(got, want) {
			t.Fatalf("second page = %v, want %v", got, want)
		}
		page = f.list(Filter{Limit: 2, After: page.Next.RunID})
		if got, want := ids(page), []string{runOldest}; !reflect.DeepEqual(got, want) {
			t.Fatalf("third page = %v, want %v", got, want)
		}
		if page.Next != nil {
			t.Fatalf("the last page says there is another one: %+v", page.Next)
		}
	})

	t.Run("paging visits every run once, where the whole list puts it", func(t *testing.T) {
		// A run the fold read as going whose own registry entry is still fresh:
		// the overlay knows it, a one-row page's query does not reach it, and a
		// page that placed it at its heartbeat while the cursor placed it at its
		// entry would list it above runs the next page then skips, and never
		// list them at all.
		f := seeded(t)
		f.seed(Entry{RunID: runMid, Slug: pageSlug, UpdatedAt: indexAt(9, 0), Status: runstore.StatusRunning})
		f.seedAgent(pageSlug, runMid, budget.AgentEntry{
			StartedAt: indexAt(8, 55).UnixMilli(),
			UpdatedAt: indexNow.Add(-time.Minute).UnixMilli(),
		})
		want := ids(f.list(Filter{}))
		if len(want) != 5 {
			t.Fatalf("the whole list = %v, want the five runs", want)
		}
		var got []string
		after := ""
		for page := 0; ; page++ {
			if page > len(want)+1 {
				t.Fatalf("paging never ended: %v", got)
			}
			p := f.list(Filter{Limit: 1, After: after})
			got = append(got, ids(p)...)
			if p.Next == nil {
				break
			}
			after = p.Next.RunID
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("one row at a time = %v, want the whole list %v", got, want)
		}
	})

	t.Run("a page is read per repository and per state", func(t *testing.T) {
		if got, want := ids(f.list(Filter{Slugs: []string{pageSlug}})), []string{runNewest, runTieLow, runMid}; !reflect.DeepEqual(got, want) {
			t.Fatalf("one repository = %v, want %v", got, want)
		}
		if got, want := ids(f.list(Filter{Slugs: []string{pageSlug, otherSlug}, States: []runstore.Status{runstore.StatusSucceeded}})), []string{runNewest, runMid}; !reflect.DeepEqual(got, want) {
			t.Fatalf("one state = %v, want %v", got, want)
		}
		if got, want := ids(f.list(Filter{PR: 12})), []string{runMid}; !reflect.DeepEqual(got, want) {
			t.Fatalf("one pull request = %v, want %v", got, want)
		}
		if got, want := ids(f.list(Filter{PlanTask: "RI3"})), []string{runMid}; !reflect.DeepEqual(got, want) {
			t.Fatalf("one plan task = %v, want %v", got, want)
		}
	})

	t.Run("the window stops the walk", func(t *testing.T) {
		if got, want := ids(f.list(Filter{Since: indexAt(10, 0)})), []string{runNewest, runTieHigh, runTieLow}; !reflect.DeepEqual(got, want) {
			t.Fatalf("since 10:00 = %v, want %v", got, want)
		}
	})

	t.Run("the window is the run's start, not its last write", func(t *testing.T) {
		// A run minted before the window whose own last write is inside it. The
		// window has always meant "runs started within this long" (`runs ls
		// --since`) and the record read windows by the run id's mint time
		// (runstore.ListRunIDs), so this run is outside the window both reads are
		// of -- while an index that windowed and ordered by updated_at would list
		// it, and print rows the record read does not have.
		f := seeded(t)
		f.seed(Entry{RunID: "20261122-093000-f0f0", Slug: pageSlug, UpdatedAt: indexAt(10, 30), Status: runstore.StatusSucceeded})
		if got, want := ids(f.list(Filter{Since: indexAt(10, 0)})), []string{runNewest, runTieHigh, runTieLow}; !reflect.DeepEqual(got, want) {
			t.Fatalf("since 10:00 = %v, want %v", got, want)
		}
	})

	// The parity the window's key exists for: the ids a windowed page holds are
	// the ids the bucket read the index replaces lists for the same window, in
	// the same order. The two reads disagreeing about a straddling run was the
	// finding; the truncation of the two sets is what it also covers.
	t.Run("a window holds what the bucket holds", func(t *testing.T) {
		f := seeded(t)
		f.seed(Entry{RunID: "20261122-093000-f0f0", Slug: pageSlug, UpdatedAt: indexAt(10, 30), Status: runstore.StatusSucceeded})
		bucket := gcpfake.NewGCS(t).Bucket(t, "runs")
		// The runs of that one repository, as the bucket has them: the two the
		// window keeps, the two it does not, and the straddler it does not either.
		for _, id := range []string{runNewest, runTieLow, runMid, "20261122-093000-f0f0"} {
			s := runstore.Open(bucket.Bucket, pageSlug, id)
			if err := s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: id}); err != nil {
				t.Fatal(err)
			}
		}
		since := indexAt(10, 0)
		want, err := runstore.ListRunIDs(context.Background(), bucket.Bucket, pageSlug, since)
		if err != nil {
			t.Fatal(err)
		}
		got := ids(f.list(Filter{Slugs: []string{pageSlug}, Since: since}))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the index's window = %v, the bucket's = %v, want one window", got, want)
		}
	})

	// A window is a moment and a run id names whole seconds, so the two reads
	// meet the question at different granularities: the record read tests the mint
	// time itself (`!t.Before(since)`) and every real window carries a fraction
	// (`runs stats` builds it as now minus a duration). The index's floor is the
	// window's own second rounded up, so a run minted earlier in that second is
	// below it -- the same run the bucket read drops.
	t.Run("a window inside a second holds what the bucket holds", func(t *testing.T) {
		f := seeded(t)
		bucket := gcpfake.NewGCS(t).Bucket(t, "runs")
		for _, id := range []string{runNewest, runTieLow, runMid} {
			s := runstore.Open(bucket.Bucket, pageSlug, id)
			if err := s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: id}); err != nil {
				t.Fatal(err)
			}
		}
		since := indexAt(10, 0).Add(500 * time.Millisecond)
		want, err := runstore.ListRunIDs(context.Background(), bucket.Bucket, pageSlug, since)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, []string{runNewest}) {
			t.Fatalf("the bucket's window = %v, want the runs after %s alone", want, since)
		}
		got := ids(f.list(Filter{Slugs: []string{pageSlug}, Since: since}))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the index's window = %v, the bucket's = %v, want one window", got, want)
		}
		// And the walk stops at the window's own second rounded up, not the second
		// the window starts in: a walk that stopped at the truncated second reads
		// the runs the page's own window filter then drops, and a page cut at them
		// reports a next page over a window that holds nothing more.
		page := f.list(Filter{Slugs: []string{pageSlug}, Since: since, Limit: 1})
		if len(page.Entries) != 1 || page.Next != nil {
			t.Fatalf("a page of one over a window of one = %v with Next %+v, want the one row and no next page", ids(page), page.Next)
		}
	})

	t.Run("the summary is the document, with every state it does not carry", func(t *testing.T) {
		sum, err := f.read.Summary(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if sum.Version != Version || !sum.Complete || sum.Runs != 5 || sum.States["unreviewed"] != 1 {
			t.Fatalf("summary = %+v", sum)
		}
		if len(sum.States) != len(runstore.Statuses) || sum.States[string(runstore.StatusRunning)] != 0 {
			t.Fatalf("a state the document does not carry is missing, not zero: %v", sum.States)
		}
	})

	t.Run("an index that has never folded says so", func(t *testing.T) {
		empty := newIndexFixture(t)
		page := empty.list(Filter{})
		if page.IndexComplete || page.FoldedThrough != nil || len(page.Entries) != 0 {
			t.Fatalf("an index with no summary read as %+v", page)
		}
		sum, err := empty.read.Summary(context.Background())
		if err != nil || sum.Complete || sum.Version != Version || len(sum.States) != len(runstore.Statuses) {
			t.Fatalf("summary = %+v, err = %v", sum, err)
		}
	})

	t.Run("a summary this release cannot read is refused", func(t *testing.T) {
		f := newIndexFixture(t)
		f.seedSummary(Summary{Version: Version + 1, UpdatedAt: indexAt(11, 30)})
		if _, err := f.read.List(context.Background(), Filter{}); err == nil {
			t.Fatal("a summary of another version was read as this one")
		}
	})

	t.Run("paging after a run nothing knows is an error, never the beginning", func(t *testing.T) {
		if _, err := f.read.List(context.Background(), Filter{After: "20261122-000000-0000"}); err == nil {
			t.Fatal("an unknown run id paged from the first row")
		}
	})

	t.Run("a filter the index does not serve is refused", func(t *testing.T) {
		for _, filter := range []Filter{
			{PlanTask: "RI3", States: []runstore.Status{runstore.StatusSucceeded}},
			{PlanTask: "RI3", PR: 12},
			{Active: true, States: []runstore.Status{runstore.StatusSucceeded}},
			{Limit: -1},
		} {
			if _, err := f.read.List(context.Background(), filter); err == nil {
				t.Fatalf("%+v was answered from a page that was read wrong", filter)
			}
		}
	})
}
