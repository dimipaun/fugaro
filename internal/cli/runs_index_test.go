package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// indexFake is the run index a listing reads in these tests: the documents a
// test seeds, the overlay rows D3202 carries beside them, and the summary the
// figure beside a page comes from.
//
// It answers with the contract's own rules (internal/runindex, and
// docs/console-fixes/contract-index.md): a document is kept by the Slugs, Since,
// PR, Mine, Batch, States and PlanTask it matches; Active keeps the runs that
// have not ended, which is an entry whose status is running and an overlay row,
// the overlay being what says a run the fold has not reached is alive; the page
// is the index's order (updated_at descending, then the run id), Limit caps it,
// and After starts it after one run's sort key, resolved from the run's own
// document or from the overlay when the index does not carry it yet (IR1). A run
// id resolved from neither is an error and never an empty page, which is the
// contract's own rule (runindex.Filter.After).
//
// It is a stand-in for the reader RI3 builds, written against the shape a
// listing asks its questions in rather than against Firestore: what is under
// test is which questions a listing asks and what it does with the answers.
type indexFake struct {
	mu      sync.Mutex
	entries []runindex.Entry
	overlay []runindex.Entry
	summary runindex.Summary
	err     error
	// filters is every question a listing asked, in the order it asked them.
	filters []runindex.Filter
}

func (f *indexFake) List(_ context.Context, filter runindex.Filter) (runindex.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	if f.err != nil {
		return runindex.Page{}, f.err
	}
	rows := f.documents()
	if filter.After != "" {
		at, ok := indexAt(rows, filter.After)
		if !ok {
			return runindex.Page{}, fmt.Errorf("no run %s in the index", filter.After)
		}
		rows = slices.DeleteFunc(rows, func(e runindex.Entry) bool { return !indexAfter(e, at) })
	}
	rows = slices.DeleteFunc(rows, func(e runindex.Entry) bool { return !f.keeps(e, filter) })
	slices.SortStableFunc(rows, indexOrder)
	page := runindex.Page{Entries: rows, IndexComplete: f.summary.Complete, FoldedThrough: f.summary.FoldedThrough}
	if filter.Limit > 0 && len(rows) > filter.Limit {
		last := rows[filter.Limit-1]
		page.Entries = rows[:filter.Limit]
		page.Next = &runindex.Cursor{UpdatedAt: &last.UpdatedAt, RunID: last.RunID}
	}
	return page, nil
}

func (f *indexFake) Summary(context.Context) (runindex.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return runindex.Summary{}, f.err
	}
	return f.summary, nil
}

// documents is every run the index knows: the documents it carries, and the
// overlay's rows for the runs it does not -- a run's index document is the
// fold's, and the overlay says only that a run the fold has not reached exists
// (D3202).
func (f *indexFake) documents() []runindex.Entry {
	docs := slices.Clone(f.entries)
	for _, e := range f.overlay {
		if !slices.ContainsFunc(docs, func(d runindex.Entry) bool { return d.RunID == e.RunID }) {
			docs = append(docs, e)
		}
	}
	return docs
}

func (f *indexFake) keeps(e runindex.Entry, filter runindex.Filter) bool {
	if len(filter.Slugs) > 0 && !slices.Contains(filter.Slugs, e.Slug) {
		return false
	}
	if !filter.Since.IsZero() && e.UpdatedAt.Before(filter.Since) {
		return false
	}
	if len(filter.States) > 0 && !slices.Contains(filter.States, e.Status) {
		return false
	}
	if filter.PlanTask != "" && e.PlanTask != filter.PlanTask {
		return false
	}
	if filter.PR != 0 && e.PR != filter.PR {
		return false
	}
	if filter.Mine != "" && e.RequestedBy != filter.Mine {
		return false
	}
	if filter.Batch != "" && e.Batch != filter.Batch {
		return false
	}
	return !filter.Active || e.Status == runstore.StatusRunning || f.isOverlay(e.RunID)
}

func (f *indexFake) isOverlay(id string) bool {
	return slices.ContainsFunc(f.overlay, func(e runindex.Entry) bool { return e.RunID == id })
}

// asked is every question a listing asked, in order.
func (f *indexFake) asked() []runindex.Filter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.filters)
}

// indexOrder is the index's own order: updated_at descending, then the run id
// (runindex.Page).
func indexOrder(a, b runindex.Entry) int {
	if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.RunID, b.RunID)
}

// indexAt is the sort key of one run of the index, and false for a run the
// index does not carry.
func indexAt(rows []runindex.Entry, id string) (runindex.Entry, bool) {
	for _, e := range rows {
		if e.RunID == id {
			return e, true
		}
	}
	return runindex.Entry{}, false
}

// indexAfter reports whether e comes after the sort key at.
func indexAfter(e, at runindex.Entry) bool {
	if c := e.UpdatedAt.Compare(at.UpdatedAt); c != 0 {
		return c < 0
	}
	return e.RunID > at.RunID
}

// lsIndex hands every listing in this test the index given: runIndexReader is
// the seam a listing reads the index through (runs.go), and the cleanup puts
// the stub back, so a test that installs no index reads as every listing did
// before this one.
func lsIndex(t *testing.T, idx runindex.Reader) {
	t.Helper()
	old := runIndexReader
	runIndexReader = func(context.Context, *cloudEnv) (runindex.Reader, error) { return idx, nil }
	t.Cleanup(func() { runIndexReader = old })
}

// lsIndexRun seeds one run ago before the listing's own clock -- its run ID is
// minted from that moment and the index carries that moment as the run's sort
// key, so a test's recency is its own and not the wall clock's (recentRunID,
// runid_test.go) -- and returns the run id.
//
// The run is seeded into the fixture's bucket as well as the index, and the
// record is what a row's status is read from: the index is consulted for which
// runs to list, and every row a listing prints is still built from the run
// itself, which is what the two paths have in common.
func lsIndexRun(t *testing.T, f *cloudFixture, idx *indexFake, ago time.Duration, status runstore.Status, suffix string) string {
	t.Helper()
	id := recentRunID(t, ago, suffix)
	idx.entries = append(idx.entries, lsIndexEntry(t, f, id, lsNow().UTC().Add(-ago), status))
	return id
}

// lsIndexEntry seeds one run's task (and its record) and answers the index
// document that names it. A run that is going is seeded launched, with its
// execution in the state that says so: a record alone is a run whose finalize
// never came, which reads as an infra error rather than as a run still working.
func lsIndexEntry(t *testing.T, f *cloudFixture, id string, at time.Time, status runstore.Status) runindex.Entry {
	t.Helper()
	going := status == runstore.StatusRunning
	exec := seedSpec(t, f, firstRunSpec(id), going)
	if going {
		f.run.SetState(exec, backend.StateRunning)
	}
	if status != "" {
		writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Status: status, Execution: exec})
	}
	return runindex.Entry{RunID: id, Slug: appSlug, Repo: "acme/app", UpdatedAt: at, Status: status, Workflow: "web"}
}

// lsIndexFields is --json's document with the two fields saying what the
// listing read about the index, and whether it printed them at all.
type lsIndexFields struct {
	IndexComplete *bool   `json:"index_complete"`
	FoldedThrough *string `json:"folded_through"`
}

func lsIndexDoc(t *testing.T, args ...string) (lsIndexFields, lsOut) {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "ls", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("ls --json: %v (%s)", err, errOut)
	}
	var fields lsIndexFields
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var doc lsOut
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return fields, doc
}

// TestRunsLsDefaultNewestPlusActive: the default listing is no longer the
// window -- it is the newest lsIndexPage runs by the index's own sort key,
// whatever their age, plus every run that has not ended, whatever its age
// (D3203) -- the active runs first, so that the page's last row is a row of the
// newest-N page and --after it lists what comes next (TestRunsLsDefaultPages-
// CoverEveryRun) -- and the figure beside it is the summary document's, which
// covers the whole index rather than the page (D818).
//
// The listing is asked for with --all because that is the listing whose scope
// is the installation: the fixture's local config lists acme/app, and a listing
// of one repository's rows is figured from its own rows and never from a
// summary that counts every repository (readIndex, and what --repo's own case
// in TestRunsLsTotalsFollowTheScope pins).
//
// Mutation caught: bounding the default read by the window (the listing then
// holds the seven days nothing else), totalling the page instead of the
// summary (a page is still a page, and its own sum is the one figure D818
// forbids beside it), and merging the active runs into the index order (an old
// active run is then the page's last row, and the next --after it skips every
// run between the two).
func TestRunsLsDefaultNewestPlusActive(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)

	// Sixty runs, one a day back: the newest fifty are listed and the ten
	// behind them are not, though every one of them is far outside the seven
	// days the window would have read.
	var newest []string
	for i := range 60 {
		id := lsIndexRun(t, f, idx, time.Duration(i+1)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("%04x", i))
		if i < lsIndexPage {
			newest = append(newest, id)
		}
	}
	// A run going since two hundred days ago: it is the oldest document the
	// index carries and it is listed, which is the half of D3203 the newest-N
	// alone would drop.
	active := lsIndexRun(t, f, idx, 200*24*time.Hour, runstore.StatusRunning, "act1")
	want := append([]string{active}, newest...)

	compute := int64(700000)
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{},
		Runs: 4321, SpentMicros: 1234567, NotionalMicros: 200000, ComputeMicros: &compute, ComputeNotEstimated: 3}

	fields, got := lsIndexDoc(t, "--all")
	if ids := runIDs(got.Runs); !slices.Equal(ids, want) {
		t.Fatalf("runs ls listed %v, want the active run %s and the newest %d by time:\n%v", ids, active, lsIndexPage, want)
	}
	if first := got.Runs[0]; first.Status != string(runstore.StatusRunning) {
		t.Errorf("the first row is listed as %q, want %q: the runs that are still going lead the page", first.Status, runstore.StatusRunning)
	}
	if last := got.Runs[len(got.Runs)-1]; last.RunID != newest[lsIndexPage-1] {
		t.Errorf("the page ends with %s, want %s, the oldest row of the newest %d: the last row is where --after takes the next page from, and a run older than every row of the page would skip the runs between them", last.RunID, newest[lsIndexPage-1], lsIndexPage)
	}
	// The figure is the summary's: neither the page (51 rows) nor the window
	// (nothing) could have produced these numbers.
	if got.Totals.Runs != 4321 {
		t.Errorf("totals count %d runs, want the summary document's 4321: a page is not a total (D818)", got.Totals.Runs)
	}
	if want := budget.Micros(1234567).USD(); got.Totals.ModelUSD != want {
		t.Errorf("totals model_usd = %v, want the summary's %v", got.Totals.ModelUSD, want)
	}
	if want := budget.Micros(200000).USD(); got.Totals.ModelNotionalUSD != want {
		t.Errorf("totals model_notional_usd = %v, want the summary's %v", got.Totals.ModelNotionalUSD, want)
	}
	if want := budget.Micros(700000).USD(); got.Totals.ComputeUSD != want {
		t.Errorf("totals compute_usd = %v, want the summary's %v", got.Totals.ComputeUSD, want)
	}
	if want := got.Totals.ModelUSD + got.Totals.ComputeUSD; got.Totals.TotalUSD != want {
		t.Errorf("totals total_usd = %v, want the billed sum %v", got.Totals.TotalUSD, want)
	}
	if got.Totals.ComputeNotEstimated != 3 {
		t.Errorf("totals compute_not_estimated = %d, want the summary's 3", got.Totals.ComputeNotEstimated)
	}
	if fields.IndexComplete == nil || !*fields.IndexComplete {
		t.Errorf("index_complete = %v, want true: the listing read an index that finished its walk", fields.IndexComplete)
	}
	if fields.FoldedThrough != nil {
		t.Errorf("folded_through = %q, want no field: the summary has folded nothing", *fields.FoldedThrough)
	}
	// The page is the reader's own: the newest lsIndexPage by the index's sort
	// key, plus the active runs on the second question.
	ask := idx.asked()
	if len(ask) != 2 || ask[0].Limit != lsIndexPage || ask[0].Active || !ask[0].Since.IsZero() {
		t.Fatalf("the reader was asked %+v, want the newest %d first and then the active runs", ask, lsIndexPage)
	}
	if ask[1].Active != true || ask[1].Limit != 0 {
		t.Errorf("the second question is %+v, want every active run and no limit: a limit is what \"the newest N\" is", ask[1])
	}
}

// TestRunsLsAfterRunID: --after stays a run id, which is what the console's
// allow-list checks, and the reader resolves it to that run's own sort key: the
// listing starts after the run, and the figure beside the page does not move
// (D818).
func TestRunsLsAfterRunID(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	var ids []string
	for i := range 5 {
		ids = append(ids, lsIndexRun(t, f, idx, time.Duration(i+1)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("a%03x", i)))
	}
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{}, Runs: 99}

	_, got := lsIndexDoc(t, "--after", ids[1])
	if want := ids[2:]; !slices.Equal(runIDs(got.Runs), want) {
		t.Fatalf("runs ls --after %s listed %v, want %v: the page starts after the run itself", ids[1], runIDs(got.Runs), want)
	}
	if got.Totals.Runs != 99 {
		t.Errorf("totals count %d runs, want the summary's 99: paging moves the page and not the total", got.Totals.Runs)
	}
	ask := idx.asked()
	if len(ask) != 2 {
		t.Fatalf("the reader was asked %+v, want the page and the active runs", ask)
	}
	for _, a := range ask {
		if a.After != ids[1] {
			t.Errorf("the reader was asked After %q, want the run id %s: --after is a run id and not a cursor (IR1)", a.After, ids[1])
		}
	}
}

// TestRunsLsAfterOverlayRow: a run that finished after the last fold has no
// index document yet -- the overlay is what carries it (D3202) -- and its sort
// key comes from the overlay, so a page after it is a page after a run the
// index does not hold.
//
// Mutation caught: resolving --after from the index documents alone, which
// fails the read for the one run id a fresh listing just printed.
func TestRunsLsAfterOverlayRow(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	var ids []string
	for i := range 3 {
		ids = append(ids, lsIndexRun(t, f, idx, time.Duration(i+2)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("b%03x", i)))
	}
	// A run going since a day ago, which the fold has not reached: the bucket
	// holds it, the index has no document of it, and the overlay names it.
	over := recentRunID(t, 24*time.Hour, "over")
	idx.overlay = []runindex.Entry{lsIndexEntry(t, f, over, lsNow().UTC().Add(-24*time.Hour), runstore.StatusRunning)}

	// The run the cursor names is a row of the listing's own page, which is
	// what a caller that pages hands back.
	_, all := lsIndexDoc(t)
	if want := append([]string{over}, ids...); !slices.Equal(runIDs(all.Runs), want) {
		t.Fatalf("runs ls listed %v, want the overlay's run %s first:\n%v", runIDs(all.Runs), over, want)
	}
	_, got := lsIndexDoc(t, "--after", over)
	if !slices.Equal(runIDs(got.Runs), ids) {
		t.Fatalf("runs ls --after %s listed %v, want %v: the sort key comes from the overlay", over, runIDs(got.Runs), ids)
	}
}

// TestRunsLsSinceWholeWindow: --since bounds the read by the window and not by
// history, so every run of the window is listed -- more than a default page --
// and the figure beside them is the window's own rows and never the summary's,
// which is what those flags have always meant (D3203, D818).
func TestRunsLsSinceWholeWindow(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	var want []string
	for i := range 60 {
		want = append(want, lsIndexRun(t, f, idx, time.Duration(i+1)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("c%03x", i)))
	}
	outside := lsIndexRun(t, f, idx, 200*24*time.Hour, runstore.StatusSucceeded, "old0")
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{}, Runs: 4321}

	fields, got := lsIndexDoc(t, "--since", "100d")
	if !slices.Equal(runIDs(got.Runs), want) {
		t.Fatalf("runs ls --since 100d listed %d rows, want the window's %d:\n%v", len(got.Runs), len(want), runIDs(got.Runs))
	}
	if slices.Contains(runIDs(got.Runs), outside) {
		t.Errorf("runs ls --since 100d listed %s, a run of 200 days ago: the window is the bound", outside)
	}
	if got.Totals.Runs != len(want) {
		t.Errorf("totals count %d runs, want the window's %d and not the summary's 4321: a bounded listing totals its own rows (D818)", got.Totals.Runs, len(want))
	}
	if fields.IndexComplete == nil || !*fields.IndexComplete {
		t.Errorf("index_complete = %v, want true", fields.IndexComplete)
	}
	// The window is the query's bound and the page is every row it matched:
	// the reader is asked for no limit, and the page is cut here so that the
	// figure beside it stays the window's.
	ask := idx.asked()
	if len(ask) != 1 {
		t.Fatalf("the reader was asked %+v, want one question: the window bounds the read", ask)
	}
	if ask[0].Limit != 0 || ask[0].Active {
		t.Errorf("the question is %+v, want the whole window with no limit", ask[0])
	}
	if want := lsNow().UTC().Add(-100 * 24 * time.Hour); ask[0].Since.After(want.Add(time.Minute)) || ask[0].Since.Before(want.Add(-time.Minute)) {
		t.Errorf("the question's Since = %s, want about %s", ask[0].Since, want)
	}
}

// TestRunsLsPRFromIndex: --pr reads the run index (D3204). The run listed here
// is named by nothing else: it is thirty days old, so the recent window cannot
// reach it, and the pull request's own index in the bucket is empty, so a
// listing that did not read the run index would print nothing at all -- and
// would say so in the fallback's line.
func TestRunsLsPRFromIndex(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	id := runIDAt(30, "090000", "aaaa")
	seedSpec(t, f, firstRunSpec(id), false)
	writeRecord(t, f, id, prRecord(id, "", 7, 1))
	idx.entries = append(idx.entries, runindex.Entry{RunID: id, Slug: appSlug, Repo: "acme/app", Workflow: "web",
		UpdatedAt: lsNow().UTC().AddDate(0, 0, -30), Status: runstore.StatusFailed, PR: 7})

	_, got := lsIndexDoc(t, "--pr", "7", "--since", "0")
	if want := []string{id}; !slices.Equal(runIDs(got.Runs), want) {
		t.Fatalf("runs ls --pr 7 listed %v, want %v: the run index names the pull request (D3204)", runIDs(got.Runs), want)
	}
	ask := idx.asked()
	if len(ask) != 1 || ask[0].PR != 7 || !slices.Equal(ask[0].Slugs, []string{appSlug}) {
		t.Fatalf("the reader was asked %+v, want one question for pull request 7 of %s", ask, appSlug)
	}
}

// TestRunsLsSaysIndexBuilding: while the index is still building, the text
// listing prints one line saying so -- the rows are what the index has folded
// and the listing is not the whole history (D3215) -- and --json carries the
// two facts as fields rather than as a line. An index that has finished its
// walk prints neither.
func TestRunsLsSaysIndexBuilding(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	id := lsIndexRun(t, f, idx, 24*time.Hour, runstore.StatusSucceeded, "d000")
	folded := time.Date(2026, 11, 22, 4, 15, 0, 0, time.UTC)
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: false, FoldedThrough: &folded,
		States: map[string]int64{}, Runs: 1}

	out, errOut, err := execute(t, "runs", "ls")
	if err != nil {
		t.Fatalf("runs ls: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "still building") {
		t.Errorf("the listing does not say the index is still building:\n%s", out)
	}
	if !strings.Contains(out, folded.Format(time.RFC3339)) {
		t.Errorf("the listing does not say how far the index is folded through (%s):\n%s", folded.Format(time.RFC3339), out)
	}
	// The RUN cell is the run's ending, the last four characters of its id.
	if !strings.Contains(out, "d000") {
		t.Errorf("the listing does not carry the run the index folded:\n%s", out)
	}
	if strings.Contains(errOut, "still building") {
		t.Errorf("the line went to stderr: it is the listing's own, and a person reads it with the rows (%q)", errOut)
	}
	fields, got := lsIndexDoc(t)
	if fields.IndexComplete == nil || *fields.IndexComplete {
		t.Errorf("index_complete = %v, want false: no walk has reached the end of history", fields.IndexComplete)
	}
	if fields.FoldedThrough == nil || *fields.FoldedThrough != folded.Format(time.RFC3339) {
		t.Errorf("folded_through = %v, want %q", fields.FoldedThrough, folded.Format(time.RFC3339))
	}
	if !slices.Equal(runIDs(got.Runs), []string{id}) {
		t.Errorf("the document lists %v, want %s", runIDs(got.Runs), id)
	}
	if strings.Contains(strings.Join(got.Warnings, "\n"), "still building") {
		t.Errorf("the line reached the document's warnings (%v): it is printed with the rows and is not a warning", got.Warnings)
	}

	// A walk that reached the end prints nothing: the line is about an index
	// that has not, and a listing that printed it either way would make it
	// noise.
	idx.summary.Complete = true
	out, errOut, err = execute(t, "runs", "ls")
	if err != nil {
		t.Fatalf("runs ls: %v (%s)", err, errOut)
	}
	if strings.Contains(out, "still building") {
		t.Errorf("a finished index still prints the line:\n%s", out)
	}
}

// TestRunsLsStreamFromIndex: the Live tab's feed reads the index the default
// listing reads -- the same page, the same rows, one per line -- so what a
// consumer draws while a listing arrives is what the listing would have
// printed whole.
func TestRunsLsStreamFromIndex(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	var want []string
	for i := range 55 {
		id := lsIndexRun(t, f, idx, time.Duration(i+1)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("e%03x", i))
		if i < lsIndexPage {
			want = append(want, id)
		}
	}
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{}, Runs: 55}

	out, errOut, err := execute(t, "runs", "ls", "--json", "--stream")
	if err != nil {
		t.Fatalf("runs ls --stream: %v (%s)", err, errOut)
	}
	feed := readFeed(t, out)
	if feed.end == nil || feed.end.Count != len(feed.rows) {
		t.Fatalf("the feed ended with %+v over %d rows, want an end line counting them", feed.end, len(feed.rows))
	}
	var ids []string
	for i, raw := range feed.rows {
		var row struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("row %d is not a run: %v: %s", i+1, err, raw)
		}
		ids = append(ids, row.RunID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("the feed carried %v, want the newest %d the index named:\n%v", ids, lsIndexPage, want)
	}
	ask := idx.asked()
	if len(ask) != 2 || ask[0].Limit != lsIndexPage {
		t.Errorf("the feed's reader was asked %+v, want the default listing's own questions", ask)
	}
}

// TestRunsLsDefaultMakesNoListing: the default path is bounded by the index and
// not by a walk of the bucket. Every run it prints is read -- an index names
// runs, and the row a person reads is built from the run itself -- and no
// listing is made, which is the difference between a read priced by a page and
// one priced by a repository's history.
//
// Mutation caught: leaving the run cache on for an indexed listing (its
// generation index is one flat listing of the whole repository) and reading the
// window beside the index.
func TestRunsLsDefaultMakesNoListing(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	var ids []string
	for i := range 3 {
		ids = append(ids, lsIndexRun(t, f, idx, time.Duration(i+1)*24*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("f%03x", i)))
	}
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{}, Runs: 3}
	d := lsLimitFixture(t, f)

	_, got := lsIndexDoc(t)
	if !slices.Equal(runIDs(got.Runs), ids) {
		t.Fatalf("runs ls listed %v, want %v", runIDs(got.Runs), ids)
	}
	if n := d.listingCount(); n != 0 {
		t.Errorf("the default listing walked the bucket %d times, want none: every run it prints was named by the index", n)
	}
	for _, id := range ids {
		if key := "runs/" + appSlug + "/" + id + "/task.json"; !slices.Contains(d.keysRead(), key) {
			t.Errorf("the listing read no %s: the rows are built from the runs, not from the documents (%v)", key, d.keysRead())
		}
	}
}

// TestRunsLsIndexUnreadableFallsBackToTheWindow: an index that cannot be read
// leaves the listing reading the window, as it did before the index existed,
// with one line on stderr saying so -- a listing never prints a short page as
// the truth, and an index that answered nothing is the case that rule is for
// (D3215).
//
// The document is byte for byte the one the same listing prints with no index
// at all: the fallback is slower, never different.
func TestRunsLsIndexUnreadableFallsBackToTheWindow(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{err: errIndexUnreadable}
	lsIndex(t, idx)
	var ids []string
	for i := range 2 {
		ids = append(ids, lsIndexRun(t, f, idx, time.Duration(i+1)*time.Hour, runstore.StatusSucceeded, fmt.Sprintf("a1%02x", i)))
	}
	idx.summary = runindex.Summary{Version: runindex.Version, Complete: true, States: map[string]int64{}, Runs: 2}

	out, errOut, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatalf("runs ls --json: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "the run index could not be read") {
		t.Errorf("stderr = %q, want the one line saying the index could not be read", errOut)
	}
	var got lsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !slices.Equal(runIDs(got.Runs), ids) {
		t.Fatalf("the fallback listed %v, want the window's %v", runIDs(got.Runs), ids)
	}
	if got.Totals.Runs != 2 {
		t.Errorf("the fallback totals %d runs, want the window's 2 and not the summary's", got.Totals.Runs)
	}
	if strings.Contains(out, "index_complete") {
		t.Errorf("the fallback's document carries an index fact: the listing read no index\n%s", out)
	}

	lsIndex(t, runindex.NewStub())
	plain, _, err := execute(t, "runs", "ls", "--json")
	if err != nil {
		t.Fatalf("runs ls --json: %v", err)
	}
	if plain != out {
		t.Errorf("the fallback reads:\n%s\nwant the listing with no index at all:\n%s", out, plain)
	}
}

// errIndexUnreadable is a reader that could not answer: what a listing meets
// with the service down.
var errIndexUnreadable = errors.New("the run index is unreadable")
