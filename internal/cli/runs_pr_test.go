package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// openPRIndexBucket is the fixture's runs bucket, open.
func openPRIndexBucket(t *testing.T, f *cloudFixture) *blob.Bucket {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// seedPRIndexEntry writes one run's index entry, the object the runner that
// opened or followed the pull request writes for itself.
func seedPRIndexEntry(t *testing.T, f *cloudFixture, id string, pr int, kind runstore.PRIndexKind) {
	t.Helper()
	e := runstore.PRIndexEntry{RunID: id, Slug: appSlug, PR: pr, Kind: kind, At: time.Now().UTC()}
	if err := runstore.PutPRIndex(context.Background(), openPRIndexBucket(t, f), e); err != nil {
		t.Fatal(err)
	}
}

// dropPRIndex removes every entry of pull request pr's index: the runs stay,
// and nothing names them, which is what an installation that predates the
// index looks like. It fails when there was nothing to remove, so a test that
// means to compare the two readings cannot pass by comparing one of them with
// itself.
func dropPRIndex(t *testing.T, f *cloudFixture, pr int) {
	t.Helper()
	ctx := context.Background()
	b := openPRIndexBucket(t, f)
	prefix := "runs/" + appSlug + "/" + runstore.PRIndexDir + "/" + strconv.Itoa(pr) + "/"
	removed := 0
	for it := b.List(&blob.ListOptions{Prefix: prefix}); ; {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if obj.IsDir {
			continue
		}
		if err := b.Delete(ctx, obj.Key); err != nil {
			t.Fatal(err)
		}
		removed++
	}
	if removed == 0 {
		t.Fatalf("nothing under %s: this test's fallback would be no fallback at all", prefix)
	}
}

// seedIndexedReview seeds a review of pull request pr that is going now: its
// task names the pull request it reviews, which is the only thing that places
// it there while it is going — a review's record carries one only at the
// artifact stage, which is what ends it — and its record and execution are the
// running ones a review in flight has.
func seedIndexedReview(t *testing.T, f *cloudFixture, id string, pr int) {
	t.Helper()
	ctx := context.Background()
	b := openPRIndexBucket(t, f)
	s := runstore.Open(b, appSlug, id)
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "review the pull request",
		ReviewPRs: []int{pr}, RequestedBy: "someone@example.com"}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
	at, err := runstore.RunTime(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: at}); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Status: runstore.StatusRunning,
		Stage: "review", Execution: exec}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	f.run.SetState(exec, backend.StateRunning)
}

// indexedWindow is seedPRRuns' window with the index an installation that has
// been through `fugaro update` holds for it: every run of either pull request
// named by its own entry, a review of pull request 7 going now, which no entry
// names, and a finished review of it older than the recent window, which only
// an entry can name.
type indexedWindow struct {
	prRuns
	review     string // the review of PR 7 that is going now
	reviewDone string // the review of PR 7 that ended, 25 days ago
}

func seedIndexedWindow(t *testing.T, f *cloudFixture) indexedWindow {
	t.Helper()
	w := indexedWindow{prRuns: seedPRRuns(t, f)}
	seedPRIndexEntry(t, f, w.root, 7, runstore.PRIndexOpened)
	seedPRIndexEntry(t, f, w.fu1, 7, runstore.PRIndexFollowed)
	seedPRIndexEntry(t, f, w.fu2, 7, runstore.PRIndexFollowed)
	seedPRIndexEntry(t, f, w.other, 8, runstore.PRIndexOpened)
	w.review = runIDAt(0, "000500", "ffff")
	seedIndexedReview(t, f, w.review, 7)
	w.reviewDone = runIDAt(25, "120000", "1234")
	seedFinishedReview(t, f, w.reviewDone, 7)
	seedPRIndexEntry(t, f, w.reviewDone, 7, runstore.PRIndexReviewed)
	return w
}

// seedFinishedReview seeds a review of pull request pr that ended: its record
// carries the number the artifact stage wrote, which is what makes its row one
// `runs ls --pr N` prints, and its task names the pull request it reviewed the
// way every review's does.
func seedFinishedReview(t *testing.T, f *cloudFixture, id string, pr int) {
	t.Helper()
	spec := firstRunSpec(id)
	spec.Mode = recipe.ModeReview
	spec.ReviewPRs = []int{pr}
	seedSpec(t, f, spec, false)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReviewed, Stage: "review",
		PR: &runstore.PRRef{Number: pr}})
}

// TestPRIndexEqualsScan: --pr N reads the index, and returns exactly the rows
// the scan returns for it, field for field, on a window that holds a pull
// request with an old run and two of today, a second pull request, a review of
// the first one going, and a review of it that ended 25 days ago.
//
// The finished review is the row only an entry can name: it is older than the
// recent window and it is not going, so the index is the one thing that can
// tell a --pr listing it exists -- and its row is a row the scan prints, since
// the artifact stage writes the pull request into the review's record (onPR).
// The entry is seeded rather than written by the runner, which is the writer's
// own test (internal/runner's TestReviewWritesThePullRequestIndexEntry) — the
// walk that writes the same entry for an installation behind is
// TestBackfillIndexEqualsScan's.
//
// The scan is the same listing with the index emptied: the runs are all there,
// which is what the installation this sped up looks like. The clock is pinned
// so the two readings are priced at the same instant, and a running row's
// compute is the window its execution has been billed for.
//
// Mutation caught: reading the index and printing what it names without the
// runs of the recent window and without the review going on the pull request —
// a row missing because an older image wrote no entry, or a whole pull request
// reading stalled while a review of it is running.
func TestPRIndexEqualsScan(t *testing.T) {
	defer func(old func() time.Time) { lsNow = old }(lsNow)
	at := time.Now().UTC()
	lsNow = func() time.Time { return at }
	f := newCloudFixture(t)
	w := seedIndexedWindow(t, f)

	indexed, errOut := lsJSON(t, "--pr", "7")
	if want := []string{w.fu2, w.fu1, w.reviewDone, w.root}; !slices.Equal(runIDs(indexed.Runs), want) {
		t.Fatalf("ls --pr 7 = %v, want %v (stderr %q)", runIDs(indexed.Runs), want, errOut)
	}
	if strings.Contains(errOut, "pull-request index") {
		t.Errorf("stderr = %q, want no fallback note: the pull request has an index", errOut)
	}
	for _, r := range indexed.Runs {
		if r.PRState != prStateLive {
			t.Fatalf("ls --pr 7 listed %s as %q, want live: the fold reads the review going on the pull request, which the index does not name", r.RunID, r.PRState)
		}
	}

	dropPRIndex(t, f, 7)
	scan, scanErr := lsJSON(t, "--pr", "7")
	if !strings.Contains(scanErr, "no pull-request index") {
		t.Errorf("stderr = %q, want the one line saying the listing fell back to the window", scanErr)
	}
	if jsonRows(t, indexed.Runs) != jsonRows(t, scan.Runs) {
		t.Errorf("the index reads %s, the scan %s", jsonRows(t, indexed.Runs), jsonRows(t, scan.Runs))
	}
	if a, b := jsonDoc(t, indexed.Totals), jsonDoc(t, scan.Totals); a != b {
		t.Errorf("the index totals %s, the scan %s", a, b)
	}
}

// TestPRIndexLiveReviewOlderThanTheRecentWindow: the live set is the third
// thing a --pr listing reads, and it is the only one of the three that can
// reach a review of the pull request that is going and was minted more than a
// day ago.
//
// The index cannot name it — a review opens and follows no pull request, so the
// number it reviews is in its record only once its artifact stage writes it,
// which is what ends it — and the recent window cannot reach it, its run ID
// being 25 days old. The live set finds it from the execution that is still
// running, and its row is what makes the pull request read live
// (markPRStates): without it the pull request reads settled while a review of
// it is going, and the review's own objects are never opened.
//
// Mutation caught: reading the index and the recent window and leaving the live
// set out — prRefs' loop over set.refs deleted.
func TestPRIndexLiveReviewOlderThanTheRecentWindow(t *testing.T) {
	f := newCloudFixture(t)
	root := runIDAt(30, "090000", "aaaa")
	review := runIDAt(25, "120000", "bbbb")
	seedSpec(t, f, firstRunSpec(root), false)
	writeRecord(t, f, root, prRecord(root, "", 7, 1))
	seedPRIndexEntry(t, f, root, 7, runstore.PRIndexOpened)
	seedIndexedReview(t, f, review, 7)
	d := lsLimitFixture(t, f)

	got, errOut := lsJSON(t, "--pr", "7")
	if ids := runIDs(got.Runs); !slices.Equal(ids, []string{root}) {
		t.Fatalf("ls --pr 7 = %v, want %s alone: a review going is read for the fold and never printed (stderr %q)", ids, root, errOut)
	}
	if got.Runs[0].PRState != prStateLive {
		t.Errorf("pull request 7 reads %q, want live: the review of it is going, and the live set is the only thing that names it", got.Runs[0].PRState)
	}
	if read := d.keysRead(); !slices.Contains(read, "runs/"+appSlug+"/"+review+"/task.json") {
		t.Errorf("the review going on pull request 7 was not read (%v): the runs of the live set are read beside the index's and the window's", read)
	}
}

// TestBackfillIndexEqualsScan: the index `fugaro update` writes is read by
// `runs ls --pr N` and gives the rows the scan gives, field for field — with
// every entry written by the walk and none seeded by hand, which is the
// difference between this test and TestPRIndexEqualsScan's.
//
// The finished review is the row that matters: it is 25 days old and it is not
// going, so neither the recent window nor the live set can name it, and its row
// is a row the scan prints, the number the artifact stage wrote into its record
// being what puts it on the pull request (runs.go's onPR). The walk reads the
// same two objects the row reader reads and writes it as `reviewed`; a walk
// that left review runs out would leave the index listing less than the scan
// does (D2964).
//
// Mutation caught: prIndexOfRun answering 0 for every review run — the index
// then lists four rows where the scan lists five.
func TestBackfillIndexEqualsScan(t *testing.T) {
	defer func(old func() time.Time) { lsNow = old }(lsNow)
	at := time.Now().UTC()
	lsNow = func() time.Time { return at }
	f := newCloudFixture(t)
	w := seedPRRuns(t, f)
	review := runIDAt(25, "120000", "1234")
	seedFinishedReview(t, f, review, 7)

	b := openPRIndexBucket(t, f)
	if n, err := backfillPRIndexIn(context.Background(), b, at, nil); err != nil || n != 5 {
		t.Fatalf("the backfill wrote %d entries (%v), want the five runs whose records name a pull request, the review among them", n, err)
	}

	indexed, errOut := lsJSON(t, "--pr", "7")
	if strings.Contains(errOut, "pull-request index") {
		t.Errorf("stderr = %q, want no fallback note: the walk wrote the index", errOut)
	}
	if want := []string{w.fu2, w.fu1, review, w.root}; !slices.Equal(runIDs(indexed.Runs), want) {
		t.Fatalf("ls --pr 7 = %v, want %v: the index the walk wrote lists the review's row, as the scan prints it", runIDs(indexed.Runs), want)
	}

	// The same listing with the index emptied: the scan, which is what the
	// index has to agree with.
	dropPRIndex(t, f, 7)
	scan, scanErr := lsJSON(t, "--pr", "7")
	if !strings.Contains(scanErr, "no pull-request index") {
		t.Errorf("stderr = %q, want the one line saying the listing fell back to the window", scanErr)
	}
	if jsonRows(t, indexed.Runs) != jsonRows(t, scan.Runs) {
		t.Errorf("the index reads %s, the scan %s", jsonRows(t, indexed.Runs), jsonRows(t, scan.Runs))
	}
	if a, b := jsonDoc(t, indexed.Totals), jsonDoc(t, scan.Totals); a != b {
		t.Errorf("the index totals %s, the scan %s", a, b)
	}
}

// TestPRFallbackNoteIsTheListings: the fallback's one line is a listing's
// (D2964, F9). The person watching `runs ls --pr N` reads why it is slow; a
// caller that reads a pull request's runs to decide something — the launcher
// resolving the spec a review is handed (reviewspec.go's launchSpecForPR) — is
// not reading a listing, and a line about this implementation's shortcut has no
// business on a launch's own output. TestLaunchSpec* pins the same from the
// launcher's side.
//
// Mutation caught: writing the note for every read of a pull request's runs,
// which puts a line about the index on `fugaro review --pr N`'s output.
func TestPRFallbackNoteIsTheListings(t *testing.T) {
	f := newCloudFixture(t)
	root := runIDAt(30, "090000", "aaaa")
	seedSpec(t, f, firstRunSpec(root), false)
	writeRecord(t, f, root, prRecord(root, "", 7, 1))

	// The listing says so, once.
	_, errOut := lsJSON(t, "--pr", "7")
	if n := strings.Count(errOut, "no pull-request index"); n != 1 {
		t.Errorf("stderr = %q, want the listing's one line saying it read the window", errOut)
	}
	// The read that decides hears nothing about the index.
	env := fileEnv(t, f)
	var buf bytes.Buffer
	if _, err := loadRows(t.Context(), env, lsFilter{slugs: []string{appSlug}, pr: 7, warn: &buf, noCache: true}, time.Now()); err != nil {
		t.Fatalf("reading pull request 7's runs: %v", err)
	}
	if s := buf.String(); strings.Contains(s, "pull-request index") {
		t.Errorf("a read that is not a listing was told about the index: %q", s)
	}
}

// jsonDoc is v as JSON, for a failure message that shows two documents apart.
func jsonDoc(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestForgedEntryAddsNoRow: the index is a hint and never a source of rows
// (D2963). A run on a shared job can write any object under its slug, its own
// pull request's entries among them, so an entry that names a run of another
// pull request is read — the read is what the entry buys — and the run's own
// task places it on that other pull request, so nothing of it is listed.
//
// Mutation caught: trusting the index for the rows, which would list a pull
// request's runs to a reader who asked for another one's.
func TestForgedEntryAddsNoRow(t *testing.T) {
	f := newCloudFixture(t)
	on := runIDAt(30, "090000", "aaaa")  // pull request 7, 30 days ago: only the index reaches it
	off := runIDAt(30, "090100", "bbbb") // pull request 8, as old
	seedSpec(t, f, firstRunSpec(on), false)
	writeRecord(t, f, on, prRecord(on, "", 7, 1))
	seedSpec(t, f, firstRunSpec(off), false)
	writeRecord(t, f, off, prRecord(off, "", 8, 1))
	seedPRIndexEntry(t, f, on, 7, runstore.PRIndexOpened)
	// The forged entry: run off's own task names pull request 8, whatever this
	// says.
	seedPRIndexEntry(t, f, off, 7, runstore.PRIndexOpened)
	d := lsLimitFixture(t, f)

	got, _ := lsJSON(t, "--pr", "7")
	if ids := runIDs(got.Runs); !slices.Equal(ids, []string{on}) {
		t.Fatalf("ls --pr 7 = %v, want %s alone: an entry adds a read, never a row", ids, on)
	}
	read := d.keysRead()
	if !slices.Contains(read, "runs/"+appSlug+"/"+off+"/task.json") {
		t.Errorf("the entry's run was not read (%v): every run the index names is checked against its own records", read)
	}
}

// TestRecentRunMissingFromIndexStillShows: an index is silent about the runs
// that ended before it existed, and about a follow-up launched by a job image
// older than it. Such a run is inside the union's recent window, so it is read
// and listed all the same (F5, D2964).
func TestRecentRunMissingFromIndexStillShows(t *testing.T) {
	f := newCloudFixture(t)
	old := runIDAt(30, "090000", "aaaa")
	recent := runIDAt(0, "000100", "bbbb")
	seedSpec(t, f, firstRunSpec(old), false)
	writeRecord(t, f, old, prRecord(old, "", 7, 2))
	seedPRIndexEntry(t, f, old, 7, runstore.PRIndexOpened)
	// The follow-up of today, and no entry names it.
	seedSpec(t, f, followUpSpec(recent, old, old, 7), false)
	fu := prRecord(recent, "", 7, 1)
	fu.Branch, fu.FollowUp = "fugaro/"+old, &runstore.FollowUp{PR: 7, PreviousRun: old}
	writeRecord(t, f, recent, fu)

	got, _ := lsJSON(t, "--pr", "7")
	if want := []string{recent, old}; !slices.Equal(runIDs(got.Runs), want) {
		t.Fatalf("ls --pr 7 = %v, want %v: the recent run is listed though no entry names it", runIDs(got.Runs), want)
	}
}

// TestNoIndexFallsBackToScan: a pull request with no entry at all reads the
// window exactly as the command did before the index existed, and says so in
// one line on stderr (D2964, F9).
func TestNoIndexFallsBackToScan(t *testing.T) {
	f := newCloudFixture(t)
	w := seedPRRuns(t, f)
	scan, errOut := lsJSON(t, "--pr", "7")
	if want := []string{w.fu2, w.fu1, w.root}; !slices.Equal(runIDs(scan.Runs), want) {
		t.Fatalf("ls --pr 7 = %v, want %v (stderr %q)", runIDs(scan.Runs), want, errOut)
	}
	if n := strings.Count(errOut, "no pull-request index"); n != 1 {
		t.Errorf("stderr = %q, want one line saying the listing fell back to the window", errOut)
	}
	// The same rows once every run of the pull request has an entry: the
	// fallback is slower, never different.
	seedPRIndexEntry(t, f, w.root, 7, runstore.PRIndexOpened)
	seedPRIndexEntry(t, f, w.fu1, 7, runstore.PRIndexFollowed)
	seedPRIndexEntry(t, f, w.fu2, 7, runstore.PRIndexFollowed)
	indexed, indexedErr := lsJSON(t, "--pr", "7")
	if jsonRows(t, scan.Runs) != jsonRows(t, indexed.Runs) {
		t.Errorf("the scan reads %s, the index %s", jsonRows(t, scan.Runs), jsonRows(t, indexed.Runs))
	}
	if strings.Contains(indexedErr, "no pull-request index") {
		t.Errorf("stderr = %q, want no fallback note once the index holds the pull request", indexedErr)
	}
}

// TestNoCacheUsesIndex: --no-cache is about the local run cache and not about
// the index: a listing that bypasses the cache still reads the runs the index
// names and still skips the rest of the window, which is what makes the flag
// cost time and nothing else.
//
// Mutation caught: letting --no-cache turn the index off as well, which
// silently puts the 23-second scan back behind a flag a reader passes to be
// sure of what they see.
func TestNoCacheUsesIndex(t *testing.T) {
	f := newCloudFixture(t)
	on := runIDAt(30, "090000", "aaaa")
	off := runIDAt(30, "090100", "bbbb")
	seedSpec(t, f, firstRunSpec(on), false)
	writeRecord(t, f, on, prRecord(on, "", 7, 1))
	seedSpec(t, f, firstRunSpec(off), false)
	writeRecord(t, f, off, prRecord(off, "", 8, 1))
	seedPRIndexEntry(t, f, on, 7, runstore.PRIndexOpened)
	d := lsLimitFixture(t, f)

	got, _ := lsJSON(t, "--pr", "7", "--no-cache")
	if ids := runIDs(got.Runs); !slices.Equal(ids, []string{on}) {
		t.Fatalf("ls --pr 7 --no-cache = %v, want %s alone", ids, on)
	}
	read := d.keysRead()
	if !slices.Contains(read, "runs/"+appSlug+"/"+on+"/result.json") {
		t.Errorf("--no-cache read no object of the run (%v): it reads the bucket, and the index is not the cache", read)
	}
	offPrefix := "runs/" + appSlug + "/" + off + "/"
	if slices.ContainsFunc(read, func(k string) bool { return strings.HasPrefix(k, offPrefix) }) {
		t.Errorf("--no-cache read the run of another pull request (%v): it bypasses the run cache and still uses the index", read)
	}
}

// TestPRReadsOnlyNamedRuns: --pr N reads the runs the index names, the runs of
// the last day and the runs that are going — and no object of a run that is
// none of those, however much of the window it is in. That is the whole of the
// speed: the 30-day-old run of another pull request is what the scan reads
// before it filters, and this listing never opens it.
//
// The run of the pull request that the index names is 30 days old on purpose:
// the recent window cannot reach it, so a listing that read it, read it
// through the index.
func TestPRReadsOnlyNamedRuns(t *testing.T) {
	f := newCloudFixture(t)
	on := runIDAt(30, "090000", "aaaa")
	other := runIDAt(30, "090100", "bbbb")
	plain := runIDAt(30, "090200", "cccc")
	seedSpec(t, f, firstRunSpec(on), false)
	writeRecord(t, f, on, prRecord(on, "", 7, 1))
	seedSpec(t, f, firstRunSpec(other), false)
	writeRecord(t, f, other, prRecord(other, "", 8, 1))
	seedSpec(t, f, firstRunSpec(plain), false)
	writeRecord(t, f, plain, prRecord(plain, "", 0, 1))
	seedPRIndexEntry(t, f, on, 7, runstore.PRIndexOpened)
	d := lsLimitFixture(t, f)

	got, _ := lsJSON(t, "--pr", "7")
	if ids := runIDs(got.Runs); !slices.Equal(ids, []string{on}) {
		t.Fatalf("ls --pr 7 = %v, want %s alone", ids, on)
	}
	read := d.keysRead()
	if !slices.Contains(read, "runs/"+appSlug+"/"+on+"/task.json") {
		t.Errorf("--pr 7 read none of the run its index names: %v", read)
	}
	for _, id := range []string{other, plain} {
		prefix := "runs/" + appSlug + "/" + id + "/"
		if slices.ContainsFunc(read, func(k string) bool { return strings.HasPrefix(k, prefix) }) {
			t.Errorf("--pr 7 read %s: a run off the pull request is what the scan reads and this listing must not (%v)", id, read)
		}
	}
}

// TestPRFallbackNoteNotInJSON: the fallback's one line is a note for the person
// watching the command run and never part of the document — a reader that
// parses --json gets the rows and the warnings it always got, and nothing
// about how the listing reached them (F9).
func TestPRFallbackNoteNotInJSON(t *testing.T) {
	f := newCloudFixture(t)
	seedPRRuns(t, f) // no index: an installation before it
	out, errOut, err := execute(t, "runs", "ls", "--json", "--pr", "7")
	if err != nil {
		t.Fatalf("ls --pr 7 --json: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, "no pull-request index") {
		t.Fatalf("stderr = %q, want the one line saying the listing fell back to the window", errOut)
	}
	if strings.Contains(out, "pull-request index") {
		t.Errorf("the fallback note reached the document:\n%s", out)
	}
	var doc struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, w := range doc.Warnings {
		if strings.Contains(w, "pull-request index") {
			t.Errorf("the document's warnings carry the note: %q", w)
		}
	}
	// The fallback is the whole window read, which is what the warning about
	// the run whose result.json is unreadable has always shown.
	if !strings.Contains(errOut, "result.json is unreadable") {
		t.Errorf("stderr = %q, want the run read by the window and warned about", errOut)
	}
}

// TestLaunchPRReadsTheRunIndex: the launch gate reads the run index, which is
// what lets `fugaro run --pr N` find the run that opened the pull request now
// that the runners write no entry of their own (IR5a, D3204).
//
// The pull request's index in the bucket is the shape a `--pr` launch leaves
// behind: the launcher's own entry for a follow-up started on it, and none for
// the opener. It is non-empty, so the window fallback never fires, and the run
// that pushed is in neither it nor the recent window -- a gate reading the
// bucket alone refuses the launch of a pull request whose own opener is right
// there in the index.
//
// Mutation caught: gating prRefs' run index read on lsFilter.indexNote, which
// is what left the launch deciding from the bucket's index alone.
func TestLaunchPRReadsTheRunIndex(t *testing.T) {
	f := newCloudFixture(t)
	idx := &indexFake{}
	lsIndex(t, idx)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	// A follow-up of the pull request that never pushed, with the launcher's
	// entry for it and no entry for the opener.
	stalled := wallRunIDAt(3, "100000", "beef")
	seedFollowUp(t, f, stalled, rootID, rootID, time.Now().Add(-72*time.Hour), runstore.StatusFailed, false)
	seedPRIndexEntry(t, f, stalled, 7, runstore.PRIndexFollowed)
	idx.entries = append(idx.entries, runindex.Entry{RunID: rootID, Slug: appSlug, Repo: "acme/app",
		UpdatedAt: time.Now().UTC().Add(-72 * time.Hour), Status: runstore.StatusFailed, Workflow: "web", PR: 7})

	args := []string{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--json", "also rename x"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatalf("the launch of PR 7's follow-up was refused (%v): the index names the opener %s, which the bucket's index does not hold", err, rootID)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the follow-up started", n)
	}
}
