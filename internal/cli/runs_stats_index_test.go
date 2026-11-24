package cli

// IR6 of docs/console-fixes: `runs stats` -- the Spend screen's read -- takes
// its rows from the run index, one page of entries for its window, and reads no
// run object at all (F32).
//
// Three things are pinned here. The read itself: with an index that has finished
// a walk, the figures are its entries' and the bucket the command is pointed at
// is never touched -- the GCS fake counts every request, so the read that made
// none is the test, and the bucket's own run carries figures the index does not,
// so a command that fell back to the records would publish them. The page's
// other kind of row, which is the overlay's: a run the index has not folded yet
// carries no launch and no record and still started, so it is in a row and not
// in the count of runs that never launched. And the document: `index_complete`
// says which of the two reads produced the figures, false while the index has
// never finished a walk (the package's stub, which is what a caller that has
// wired nothing reads through) and true when the rows are the index's.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/userstats"
)

// statsIndex is the run index these tests read through: the entries a test
// hands it, answered the way the contract's Reader answers them -- the window's
// entries, newest first, cut at the filter's limit, with the cursor that says
// the page was cut -- and a summary whose `complete` the test decides.
type statsIndex struct {
	entries []runindex.Entry
	// complete is the summary's own `complete`, and the page's IndexComplete
	// beside it: false is an index that has never finished a walk.
	complete bool
	// lists counts the pages asked for, so a test can tell a read that asked the
	// index from one that never did.
	lists int
}

func (s *statsIndex) List(_ context.Context, f runindex.Filter) (runindex.Page, error) {
	s.lists++
	kept := []runindex.Entry{}
	for _, e := range s.entries {
		if len(f.Slugs) > 0 && !slices.Contains(f.Slugs, e.Slug) {
			continue
		}
		if !f.Since.IsZero() && e.UpdatedAt.Before(f.Since) {
			continue
		}
		kept = append(kept, e)
	}
	slices.SortFunc(kept, func(a, b runindex.Entry) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.RunID, a.RunID)
	})
	page := runindex.Page{Entries: kept, IndexComplete: s.complete}
	if f.Limit > 0 && len(kept) > f.Limit {
		page.Entries = kept[:f.Limit]
		last := page.Entries[len(page.Entries)-1]
		page.Next = &runindex.Cursor{UpdatedAt: &last.UpdatedAt, RunID: last.RunID}
	}
	return page, nil
}

func (s *statsIndex) Summary(context.Context) (runindex.Summary, error) {
	return runindex.Summary{Version: runindex.Version, Complete: s.complete, States: map[string]int64{}}, nil
}

// statsIndexInstall points the command's index read at idx for one test.
func statsIndexInstall(t *testing.T, idx runindex.Reader) {
	t.Helper()
	prev := runIndexReader
	runIndexReader = func(context.Context, *cloudEnv) (runindex.Reader, error) { return idx, nil }
	t.Cleanup(func() { runIndexReader = prev })
}

// statsIndexMintTime is the moment a run id names, which is what the fold
// writes as an entry's updated_at when the run's record and launch carry none:
// a run that was claimed and never launched has no other moment, and an entry
// with no sort key would fall outside every window (contract-index.md, "The run
// document").
func statsIndexMintTime(t *testing.T, id string) time.Time {
	t.Helper()
	at, err := runstore.RunTime(id)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// statsIndexEntry is one index entry for a run of the fixture's window: the
// fields this command reads, and nothing else.
func statsIndexEntry(id, launchedBy string, updated time.Time, rec *runstore.Record) runindex.Entry {
	return runindex.Entry{
		RunID:     id,
		Slug:      appSlug,
		UpdatedAt: updated,
		Launch:    &runstore.Launch{Version: 1, RunID: id, LaunchedBy: launchedBy, LaunchedAt: updated},
		Record:    rec,
	}
}

// seedBucketRun writes one run's two records into the env's own bucket: what a
// read of the records finds there.
func seedBucketRun(t *testing.T, env *cloudEnv, id, launchedBy string, rec *runstore.Record) {
	t.Helper()
	ctx := context.Background()
	s := runstore.Open(env.bucket.Bucket, appSlug, id)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, LaunchedBy: launchedBy, LaunchedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		return
	}
	rec.RunID = id
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// bucketRequests renders the requests a bucket served, so a failure says what
// the read spent: the listing (`/o`, the endpoint with no object after it) and
// the object reads (`/o/<name>`), which are the two things a run-object read
// costs.
func bucketRequests(reqs []gcpfake.Request) []string {
	var out []string
	for _, r := range reqs {
		out = append(out, r.Method+" "+r.Path+"?"+r.Query)
	}
	return out
}

// TestRunsStatsReadsIndexNoObjects is IR6's own red step: the rows come from the
// index's entries in the window, and the read makes no request to the runs
// bucket at all. Run against the read the command made before this task, the
// same call lists the bucket and reads two objects per run -- the request list
// below is the assertion, and it is empty.
func TestRunsStatsReadsIndexNoObjects(t *testing.T) {
	f := newCloudFixture(t)
	gcs := gcpfake.NewGCS(t)
	env := envOn(t, f, gcs.Bucket(t, "runs"))
	ctx := context.Background()
	day := time.Now().UTC().Truncate(time.Second)
	// The bucket: a run of the same window whose figures are NOT the ones the
	// index carries, so a read that fell back to the records would publish it.
	seedBucketRun(t, env, userStatsID(day, 0), "bucket@example.com", userRec(runstore.StatusFailed, 9.00, runstore.BasisAPIList))
	idx := &statsIndex{complete: true, entries: []runindex.Entry{
		statsIndexEntry(userStatsID(day, 1), "sam@example.com", day.Add(-time.Minute), userRec(runstore.StatusSucceeded, 2.00, runstore.BasisAPIList)),
		statsIndexEntry(userStatsID(day, 2), "dana@example.com", day, userRec(runstore.StatusSucceeded, 3.00, runstore.BasisAPIList)),
	}}
	statsIndexInstall(t, idx)

	before := len(gcs.Requests())
	read, err := readUserStatsRuns(ctx, env, []string{appSlug}, day.Add(-7*24*time.Hour), userStatsMaxRuns)
	if err != nil {
		t.Fatal(err)
	}
	if got := bucketRequests(gcs.Requests()[before:]); len(got) != 0 {
		t.Errorf("the read made %d request(s) to the runs bucket, want none: the rows are the index's (F32)\n%s",
			len(got), strings.Join(got, "\n"))
	}
	if idx.lists != 1 {
		t.Errorf("the index was asked for %d page(s), want 1", idx.lists)
	}
	if !read.indexComplete {
		t.Error("index_complete = false over an index that has finished a walk: the rows are its entries'")
	}
	if read.truncated || read.unlaunched != 0 {
		t.Errorf("truncated = %v, unlaunched = %d, want false and 0 over two entries", read.truncated, read.unlaunched)
	}
	// The rows are the entries' own figures, and the bucket's run is in none of
	// them: the same window, read from the index.
	res := userstats.Aggregate(userStatsRuns(read.runs))
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %+v, want the index's two runs", res.Rows)
	}
	dana := userStatsRowFor(t, res.Rows, "dana@example.com")
	if dana.Runs != 1 || dana.CountedUSD != 3.00 {
		t.Errorf("dana = %+v, want the entry's own run and money", dana)
	}
	for _, r := range res.Rows {
		if r.User == "bucket@example.com" {
			t.Errorf("the bucket's run is in the figures: %+v", res.Rows)
		}
	}

	// The page's own continuation is the truncation: cut to one entry, the read
	// says the window holds more, and still reads no object.
	before = len(gcs.Requests())
	cut, err := readUserStatsRuns(ctx, env, []string{appSlug}, day.Add(-7*24*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := bucketRequests(gcs.Requests()[before:]); len(got) != 0 {
		t.Errorf("the cut read made %d request(s) to the runs bucket, want none\n%s", len(got), strings.Join(got, "\n"))
	}
	if !cut.truncated {
		t.Error("truncated = false over a page the reader cut: the window holds more than --max-runs")
	}
	if len(cut.runs) != 1 || cut.runs[0].run.User != "dana@example.com" {
		t.Errorf("the cut read is over %+v, want the newest entry alone", cut.runs)
	}
}

// TestRunsStatsCountsAnOverlayRowAsARunNotAsUnlaunched is the page's other kind
// of row, the overlay's: a run the index has not folded yet -- in flight, or
// finished inside the fold gap -- is listed with its run id, its slug and its
// status alone (contract-index.md, "The overlay"), so it carries no launch and
// no record and still DID start. It is therefore not one of the window's
// unlaunched runs, and it is in a row: the unknown one with no record, which is
// where the record read puts a started run whose launch nobody can read
// (TestRunsStatsUnreadableLaunchIsUnknownNotUnlaunched). The entry the fold
// wrote with neither record and no status is the one that never started, and it
// is in this window on the sort key the fold gives it -- its own run id's mint
// time, `updated_at`'s last fallback -- which is the key such a run has and the
// only reason the branch below is reachable at all.
func TestRunsStatsCountsAnOverlayRowAsARunNotAsUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	env := envOn(t, f, gcpfake.NewGCS(t).Bucket(t, "runs"))
	day := time.Now().UTC().Truncate(time.Second)
	overlayOnly, neverLaunched := userStatsID(day, 1), userStatsID(day, 2)
	statsIndexInstall(t, &statsIndex{complete: true, entries: []runindex.Entry{
		// The overlay's row: the run id and the slug are the path's own, the
		// status is the overlay's, and there is nothing else to carry.
		{RunID: overlayOnly, Slug: appSlug, UpdatedAt: day, Status: runstore.StatusRunning},
		// The fold's row for a run that was claimed and never launched: a
		// task.json and nothing else, so nothing settled a status for it and its
		// sort key is the moment its run id names.
		{RunID: neverLaunched, Slug: appSlug, UpdatedAt: statsIndexMintTime(t, neverLaunched)},
	}})

	read, err := readUserStatsRuns(context.Background(), env, []string{appSlug}, day.Add(-7*24*time.Hour), userStatsMaxRuns)
	if err != nil {
		t.Fatal(err)
	}
	if read.unlaunched != 1 {
		t.Errorf("unlaunched = %d, want 1: only the entry that settled no status never started", read.unlaunched)
	}
	res := userstats.Aggregate(userStatsRuns(read.runs))
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v, want the overlay's run in the unknown row alone", res.Rows)
	}
	if got := res.Rows[0]; got.User != userstats.UnknownUser || got.Runs != 1 || got.NoRecord != 1 {
		t.Errorf("the overlay's row = %+v, want one run under %q with no record: it started, and its launcher "+
			"and its money are what the page cannot say", got, userstats.UnknownUser)
	}
}

// TestRunsStatsDocumentSaysWhichReadItIs is the document half: index_complete is
// true when the rows are the index's page, and false while the index has never
// finished a walk -- when the run records are read instead, over the same window
// and with the same figures, which is the read this command made before the
// index existed.
func TestRunsStatsDocumentSaysWhichReadItIs(t *testing.T) {
	f := newCloudFixture(t)
	userStatsPlace(t, f, "someone@example.com", nil, []string{"user:someone@example.com"})
	day := time.Now().UTC()
	seedUserRun(t, f, userStatsID(day, 0), "bucket@example.com", userRec(runstore.StatusSucceeded, 1.00, runstore.BasisAPIList))

	// The package's own stub is wired -- an index that has never finished a walk
	// -- so the records are read and the document says which read it is.
	fallback := runsStats(t)
	if fallback.IndexComplete {
		t.Error("index_complete = true with an index that has never finished a walk: the records are what this read is over")
	}
	if len(fallback.Rows) != 1 || fallback.Rows[0].User != "bucket@example.com" || fallback.Rows[0].CountedUSD != 1.00 {
		t.Errorf("rows = %+v, want the records' own run and money", fallback.Rows)
	}

	// The index answers: the rows are its entries, and the document says so.
	statsIndexInstall(t, &statsIndex{complete: true, entries: []runindex.Entry{
		statsIndexEntry(userStatsID(day, 1), "index@example.com", day, userRec(runstore.StatusSucceeded, 4.00, runstore.BasisAPIList)),
	}})
	indexed := runsStats(t)
	if !indexed.IndexComplete {
		t.Error("index_complete = false over an index that has finished a walk")
	}
	if len(indexed.Rows) != 1 || indexed.Rows[0].User != "index@example.com" || indexed.Rows[0].CountedUSD != 4.00 {
		t.Errorf("rows = %+v, want the index's own run and money", indexed.Rows)
	}
	for _, r := range indexed.Rows {
		if r.User == "bucket@example.com" {
			t.Errorf("the bucket's run is in a read taken from the index: %+v", indexed.Rows)
		}
	}
}

// userStatsRowFor is the row one user's key carries in an aggregate, failing
// when the figures hold none.
func userStatsRowFor(t *testing.T, rows []userstats.Row, user string) userstats.Row {
	t.Helper()
	for _, r := range rows {
		if r.User == user {
			return r
		}
	}
	t.Fatalf("no row for %q in %+v", user, rows)
	return userstats.Row{}
}
