package cli

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gocloud.dev/blob"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_prindex_test.go is D2965's step as the owner meets it: an installation
// at the previous version holds runs and no pull-request index, and ONE `fugaro
// update` — the HARD RULE's whole upgrade — writes the entry of every run that
// names a pull request, over the runs of the last 90 days, create-if-absent.
//
// What the suite has to hold is the whole of the claim: the rows a reader gets
// afterwards are the rows it got before (the index names exactly the runs that
// are on the pull request, and no run is lost or invented), a second update
// writes nothing, a run in flight is neither waited for nor touched, an
// installation with nothing to index hears nothing at all, and no door but
// `fugaro update` writes an entry.

// sevenPR is the pull request the fixture's runs are on.
const sevenPR = 7

// prIndexRig is the update door's own fixture — an installation one release
// behind, whose runs bucket this test can plant runs in and read back.
func prIndexRig(t *testing.T) *convergeRig {
	t.Helper()
	return newConvergeRig(t, currentNode())
}

// prIndexBucket is the installation's runs bucket, opened the way the step
// itself opens it (sharedBucketOpener's URL): the objects planted here are the
// objects the update walks.
func prIndexBucket(t *testing.T, r *convergeRig) *blob.Bucket {
	t.Helper()
	b, err := blobx.Open(t.Context(), r.localConfig(t).BucketURL())
	if err != nil {
		t.Fatalf("opening the rig's runs bucket: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b.Bucket
}

// prIndexViewer is the credential the listings below are made under: the row
// reader drops every run of a repository the caller holds no role in, so a
// listing a test asserts against has to name a member.
const prIndexViewer = "user:test@example.com"

// lsAsViewer makes the fixture's credential a viewer of the project, for the
// `runs ls` below and no other reason. The grants node is planted rather than
// written through the writer because these tests are about the walk and not
// about the roles; an update is free to move the node, which is why every
// listing plants it again.
func lsAsViewer(t *testing.T, r *convergeRig) {
	t.Helper()
	accessRunAs(t, prIndexViewer)
	r.db.Set(roleaccess.PathNode, accessNodeDoc(projectGrant(prIndexViewer, roleaccess.RoleViewer)))
}

// prIndexRuns are the runs the fixture's installation holds: the three states a
// backfill writes an entry for, and the three it must leave alone.
type prIndexRuns struct {
	// root is the first run of the repository, thirty days ago, which opened
	// pull request 7 — its record names it, as a finished run's does.
	root string
	// fu is a follow-up of pull request 7: its TASK names the pull request,
	// which is what makes the entry it gets "followed".
	fu string
	// other opened pull request 8.
	other string
	// plain never launched: it has a task and no record, and names no pull
	// request to index.
	plain string
	// review is a finished review run of pull request 7: its record names the
	// pull request the artifact stage wrote, which is a `reviewed` entry.
	review string
	// running is a run mid-flight in the window: its record is written and its
	// task is not, the order the launcher and the runner write them in.
	running string
}

// prIndexIDs names the fixture's six runs, without planting anything.
func prIndexIDs() prIndexRuns {
	return prIndexRuns{
		root:  runIDAt(30, "090000", "a1a1"),
		fu:    runIDAt(0, "000100", "b2b2"),
		other: runIDAt(0, "000200", "c3c3"),
		plain: runIDAt(0, "000300", "d4d4"),
		// The review is older than prIndexRecent on purpose: it is the run the
		// index is the only way to reach, since neither the last 24 hours nor
		// the live set can name it.
		review:  runIDAt(25, "040000", "e5e5"),
		running: runIDAt(0, "000500", "f6f6"),
	}
}

// plantPRIndexRuns stores the fixture's runs in the installation's runs bucket,
// as the release behind left them: task.json and result.json, and nothing about
// a pull request's index anywhere.
func plantPRIndexRuns(t *testing.T, r *convergeRig) prIndexRuns {
	t.Helper()
	ids := prIndexIDs()
	b := prIndexBucket(t, r)
	// The review run's record names pull request 7 as a finished review's does,
	// so the walk indexes it as `reviewed`: `runs ls --pr 7` prints its row from
	// that number (runs.go's onPR), so an index that could not name it would
	// list less than the scan does (D2964).
	reviewRec := prRecord(ids.review, "", sevenPR, 1)
	reviewRec.Status, reviewRec.Stage, reviewRec.Outcome = runstore.StatusSucceeded, "finalize", runstore.OutcomeNone
	for _, run := range []struct {
		id   string
		spec *task.Spec
		rec  *runstore.Record
	}{
		{ids.root, firstRunSpec(ids.root), prRecord(ids.root, "", sevenPR, 2)},
		{ids.fu, followUpSpec(ids.fu, ids.root, ids.root, sevenPR), prRecord(ids.fu, "", sevenPR, 1)},
		{ids.other, firstRunSpec(ids.other), prRecord(ids.other, "", 8, 5)},
		{ids.plain, firstRunSpec(ids.plain), nil},
		{ids.review, reviewRunSpec(ids.review, sevenPR), reviewRec},
		{ids.running, nil, &runstore.Record{Version: 1, RunID: ids.running, Repo: "acme/app", Workflow: "app",
			Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone}},
	} {
		plantRun(t, b, appSlug, run.id, run.spec, run.rec)
	}
	// The premise, read back through the reader the walk itself uses: a fixture
	// whose runs are not readable would make every "no entry for this one"
	// assertion below true for the wrong reason.
	if _, err := runstore.Open(b, appSlug, ids.running).ReadRecord(t.Context()); err != nil {
		t.Fatalf("the fixture's run in flight has no record (%v): the walk would skip it as a run it cannot place rather than as one it must not touch", err)
	}
	if _, err := runstore.Open(b, appSlug, ids.running).ReadTask(t.Context()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("the fixture's run in flight reads a task (%v): it is the shape of a run that has written its record and not yet its task", err)
	}
	if spec, err := runstore.Open(b, appSlug, ids.review).ReadTask(t.Context()); err != nil || len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != sevenPR {
		t.Fatalf("the fixture's review run reads as %+v (%v), want a review of pull request %d", spec, err, sevenPR)
	}
	return ids
}

// reviewRunSpec is a review run's task: the pull request to read, and none of
// the fields a review task never carries (task.Validate).
func reviewRunSpec(id string, pr int) *task.Spec {
	return &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", ReviewPRs: []int{pr}}
}

// plantRun stores one planted run: its task, its record, or both, the way the
// launcher and the runner write them.
func plantRun(t *testing.T, b *blob.Bucket, slug, id string, spec *task.Spec, rec *runstore.Record) {
	t.Helper()
	s := runstore.Open(b, slug, id)
	if spec != nil {
		if err := s.CreateTask(t.Context(), spec); err != nil {
			t.Fatalf("planting %s's task: %v", id, err)
		}
	}
	if rec != nil {
		if err := s.WriteRecord(t.Context(), rec); err != nil {
			t.Fatalf("planting %s's record: %v", id, err)
		}
	}
}

// prIndexEntries is one pull request's index as a map from run ID to entry, read
// through the reader a later `runs ls --pr N` reads it with, so an object a
// reader refuses fails the test rather than being counted as an entry.
func prIndexEntries(t *testing.T, b *blob.Bucket, slug string, pr int) map[string]runstore.PRIndexEntry {
	t.Helper()
	entries, err := runstore.ListPRIndex(t.Context(), b, slug, pr, func(format string, args ...any) {
		t.Errorf("the index of %s/%d carries an object a reader refuses: "+format, append([]any{slug, pr}, args...)...)
	})
	if err != nil {
		t.Fatalf("listing the index of %s/%d: %v", slug, pr, err)
	}
	out := map[string]runstore.PRIndexEntry{}
	for _, e := range entries {
		out[e.RunID] = e
	}
	return out
}

// indexRunIDs is the runs one pull request's index names, sorted.
func indexRunIDs(entries map[string]runstore.PRIndexEntry) []string {
	out := make([]string, 0, len(entries))
	for id := range entries {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// prIndexKeys is the object names under one prefix of the bucket whose leaf is
// an index entry.
func prIndexKeys(t *testing.T, b *blob.Bucket, prefix string) []string {
	t.Helper()
	var out []string
	for key := range runsTree(t, b, prefix) {
		if strings.Contains(key, "/"+runstore.PRIndexDir+"/") {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

// runsTree is every object under one prefix of the bucket, key to bytes. Taken
// before and after a run it is how a test says "nothing moved" about a whole
// subtree — the entries a step writes included — rather than about the objects
// it happened to think of.
func runsTree(t *testing.T, b *blob.Bucket, prefix string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(t.Context())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("listing %s: %v", prefix, err)
		}
		if obj.IsDir {
			continue
		}
		data, err := b.ReadAll(t.Context(), obj.Key)
		if err != nil {
			t.Fatalf("reading %s: %v", obj.Key, err)
		}
		out[obj.Key] = data
	}
	return out
}

// sameTree fails the test for every object that appeared, vanished or changed
// between two readings of one subtree.
func sameTree(t *testing.T, what string, before, after map[string][]byte) {
	t.Helper()
	for key, was := range before {
		now, ok := after[key]
		switch {
		case !ok:
			t.Errorf("%s removed %s: a step of the update creates what is missing and never takes away what a run holds", what, key)
		case string(now) != string(was):
			t.Errorf("%s rewrote %s:\n  was %s\n  now %s", what, key, was, now)
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			t.Errorf("%s wrote %s, which was not there before", what, key)
		}
	}
}

// lsRunIDs is `runs ls` under the fixture's own credential, as the run IDs it
// lists, sorted. It is what a person reads, so it is what the rows the index has
// to agree with are taken from.
func lsRunIDs(t *testing.T, args ...string) []string {
	t.Helper()
	got, _ := lsJSON(t, args...)
	out := make([]string, 0, len(got.Runs))
	for _, row := range got.Runs {
		out = append(out, row.RunID)
	}
	slices.Sort(out)
	return out
}

// TestUpdateBackfillsPRIndex is the task's own acceptance: an installation at
// the previous version — the runs of its repository in the bucket, no index
// beside them — converges under one `fugaro update` to an index that names
// exactly the runs on a pull request, so the rows `runs ls --pr N` gives are the
// rows it gave before.
//
// The rows are asserted against the listing rather than against the index alone,
// because the listing is what the index is for: this release's reader still
// scans the window (the read that consumes the index is RL3, and it is not in
// this tree), so what this test can hold today — and what the step must not
// break — is that the rows are unchanged by the update, that every run the index
// names is one of those rows (an entry may never add a row), and that the walk
// wrote an entry for exactly the runs whose own records place them on a pull
// request. Every entry is read back through ListPRIndex, which refuses an object
// the run's own records do not agree with (D2963).
//
// The fixture's review run is a row of the listing and an entry of the index:
// the index's vocabulary is the three ways a run comes to be on a pull request
// (RL2W writes `opened` when a run opens one and `followed` when a follow-up
// starts on one, and `reviewed` is the number a review's own artifact stage
// wrote into its record), and `runs ls --pr N` shows a review run whose record
// names the pull request. A walk that skipped it would leave the index listing
// less than the scan does (D2964) — the review of the fixture is twenty-five
// days old, so neither the last 24 hours nor the live set can reach it.
func TestUpdateBackfillsPRIndex(t *testing.T) {
	r := prIndexRig(t)
	ids := plantPRIndexRuns(t, r)
	b := prIndexBucket(t, r)
	// The premise: an installation at the previous version holds runs and no
	// index at all.
	if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
		t.Fatalf("the fixture already holds %v: this test is about the update writing the index", keys)
	}
	lsAsViewer(t, r)
	before := lsRunIDs(t, "--pr", strconv.Itoa(sevenPR), "--repo", "acme/app")
	if got, want := before, slices.Sorted(slices.Values([]string{ids.root, ids.fu, ids.review})); !slices.Equal(got, want) {
		t.Fatalf("the fixture's `runs ls --pr %d` gives %v, want its three runs whose records name it: the acceptance below compares against this listing", sevenPR, got)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// What the run said: the count of entries it created, and nothing else about
	// the index.
	if !strings.Contains(out, "pull-request index: 4 run(s)") {
		t.Errorf("the update does not say how many runs it indexed:\n%s", out)
	}
	// What it wrote: the three runs on pull request 7, each with the kind it
	// came to be on it by — the first run opened it, the follow-up continued it,
	// and the review read it.
	seven := prIndexEntries(t, b, appSlug, sevenPR)
	if got, want := indexRunIDs(seven), slices.Sorted(slices.Values([]string{ids.root, ids.fu, ids.review})); !slices.Equal(got, want) {
		t.Errorf("the index of pull request %d names %v, want the three runs the fixture has on it", sevenPR, got)
	}
	for id, want := range map[string]runstore.PRIndexKind{ids.root: runstore.PRIndexOpened, ids.fu: runstore.PRIndexFollowed, ids.review: runstore.PRIndexReviewed} {
		e := seven[id]
		if e.Kind != want {
			t.Errorf("%s is indexed as %q, want %q", id, e.Kind, want)
		}
		if e.Slug != appSlug || e.PR != sevenPR || e.Version != runstore.PRIndexVersion || e.At.IsZero() {
			t.Errorf("%s is indexed as %+v, want this tree's shape for %s/%d", id, e, appSlug, sevenPR)
		}
	}
	if got := indexRunIDs(prIndexEntries(t, b, appSlug, 8)); !slices.Equal(got, []string{ids.other}) {
		t.Errorf("the index of pull request 8 names %v, want the one run that opened it", got)
	}
	// And what it left alone: no entry for a run that names no pull request at
	// all, and the four objects the walk wrote are the whole of what it wrote.
	for _, id := range []string{ids.plain, ids.running} {
		if e, ok := seven[id]; ok {
			t.Errorf("%s is in the index of pull request %d (%+v) and it names no pull request", id, sevenPR, e)
		}
	}
	want := []string{}
	for _, e := range []struct {
		pr int
		id string
	}{{sevenPR, ids.root}, {sevenPR, ids.fu}, {sevenPR, ids.review}, {8, ids.other}} {
		key, err := runstore.PRIndexPath(appSlug, e.pr, e.id)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, key)
	}
	if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); !slices.Equal(keys, slices.Sorted(slices.Values(want))) {
		t.Errorf("the update wrote %v, want one object per run that names a pull request and (%v) no other", keys, want)
	}

	// The acceptance: the runs `runs ls --pr N` gives afterwards are the runs it
	// gave before, and every run the index names is one of them.
	lsAsViewer(t, r)
	after := lsRunIDs(t, "--pr", strconv.Itoa(sevenPR), "--repo", "acme/app")
	if !slices.Equal(before, after) {
		t.Errorf("runs ls --pr %d gave %v before the update and %v after it: a backfill may add no row and lose none", sevenPR, before, after)
	}
	for _, id := range indexRunIDs(seven) {
		if !slices.Contains(after, id) {
			t.Errorf("the index names %s, which `runs ls --pr %d` does not show (%v): an entry may never add a row", id, sevenPR, after)
		}
	}
}

// TestUpdateBackfillIdempotent: the second `fugaro update` writes nothing for
// what it already indexed. It is the owner's own sentence about the step ("a
// second update writes nothing"), and the reason the walk asks the repository's
// index what it already names before it reads a run: without that, every update
// of every installation would read the records of its whole window to be told,
// entry by entry, that the object it would write is already there.
//
// The second run is given a run that appeared since the first — a run of the
// window that opened a pull request and has no entry — so that the silence is
// not the silence of a step that never ran: the line below is the count of what
// the second update wrote, and it is one, not five. Everything else the bucket
// held is byte for byte what it was, the four entries of the first update
// included (a rewrite of one of them would show here).
func TestUpdateBackfillIdempotent(t *testing.T) {
	r := prIndexRig(t)
	plantPRIndexRuns(t, r)
	b := prIndexBucket(t, r)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
	first := prIndexKeys(t, b, "runs/"+appSlug+"/")
	if len(first) == 0 {
		t.Fatalf("the first update wrote no index at all, so this test would pass over a step that does nothing")
	}

	// A run that appeared between the two updates, as one of the window's runs
	// does on any installation somebody uses. It is planted before the reading
	// below, so the only object the second update may add is its entry.
	appeared := runIDAt(0, "000600", "a7a7")
	plantRun(t, b, appSlug, appeared, firstRunSpec(appeared), prRecord(appeared, "", 9, 1))
	appearedKey, err := runstore.PRIndexPath(appSlug, 9, appeared)
	if err != nil {
		t.Fatal(err)
	}
	before := runsTree(t, b, "runs/"+appSlug+"/")

	// The ordinary command the owner runs again, with none of convergeOn's
	// assertions that the fixture is a previous release: a converged
	// installation is not one. The run's own exit code is not this test's
	// subject — its stages leave this checkout's own uncommitted edit for the
	// person (`git switch -- main`, the images stage re-pinning the base image),
	// which is what an update says about a checkout somebody has changed — and
	// what the tail step wrote is.
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "update")
	if !strings.Contains(out, "pull-request index: 1 run(s)") {
		t.Fatalf("the second update did not index %s, the run that has no entry yet (exit %v, %s): the walk must create what is missing and nothing else\n%s", appeared, err, errOut, out)
	}
	after := runsTree(t, b, "runs/"+appSlug+"/")
	if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); !slices.Equal(keys, slices.Sorted(slices.Values(append(slices.Clone(first), appearedKey)))) {
		t.Errorf("the index holds %v after the second update, want the entries of the first (%v) and %s's alone", keys, first, appeared)
	}
	for key, was := range before {
		now, ok := after[key]
		if !ok {
			t.Errorf("the second update removed %s: it creates what is missing and takes nothing away", key)
			continue
		}
		if string(now) != string(was) {
			t.Errorf("the second update rewrote %s:\n  was %s\n  now %s", key, was, now)
		}
	}
	for key := range after {
		// The run that appeared gets both its index entry and its summary: the
		// summary backfill of the same update (update_summary.go) writes what is
		// missing for it exactly as the index step does.
		if _, ok := before[key]; !ok && key != appearedKey && !strings.HasSuffix(key, "/"+runstore.SummaryName) {
			t.Errorf("the second update wrote %s, want only the entry of the run that appeared (%s)", key, appearedKey)
		}
	}
}

// TestUpdateBackfillQuietWhenNothing: an installation with nothing to write
// hears nothing. The step says one line when it created an entry and is silent
// otherwise — a line about zero entries on every update of every installation is
// noise the update's own account does not need — and the two cases here are the
// two shapes of nothing: a repository with no runs in the window at all, and one
// whose runs name no pull request. The first test in this file is what pins that
// a step which DID write something says so, so the silence below is never the
// silence of a step that did not run.
func TestUpdateBackfillQuietWhenNothing(t *testing.T) {
	t.Run("no runs at all", func(t *testing.T) {
		r := prIndexRig(t)
		b := prIndexBucket(t, r)
		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
		if strings.Contains(out, "pull-request index") {
			t.Errorf("the update says something about the pull-request index over a repository with no runs:\n%s", out)
		}
		if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
			t.Errorf("the update wrote %v with no run to index", keys)
		}
	})
	t.Run("runs that name no pull request", func(t *testing.T) {
		r := prIndexRig(t)
		b := prIndexBucket(t, r)
		ids := prIndexIDs()
		plain, review, running := ids.plain, ids.review, ids.running
		plantRun(t, b, appSlug, plain, firstRunSpec(plain), nil)
		// The review is still going: its task names the pull request it reads
		// and its record names one only at the artifact stage, which is where
		// the review writes its own entry (writeReviewArtifact). There is
		// nothing here for the walk to place it on.
		reviewRec := prRecord(review, "", sevenPR, 1)
		reviewRec.PR = nil
		plantRun(t, b, appSlug, review, reviewRunSpec(review, sevenPR), reviewRec)
		plantRun(t, b, appSlug, running, nil, &runstore.Record{Version: 1, RunID: running, Repo: "acme/app", Workflow: "app",
			Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone})
		// The premise: the walk had runs to read. A recordless run, a review
		// whose record names no pull request yet and a run in flight are the
		// three shapes it finds nothing to write about, and a walk that found
		// none of them would be silent for the wrong reason.
		if _, err := runstore.Open(b, appSlug, plain).ReadTask(t.Context()); err != nil {
			t.Fatalf("the fixture's recordless run has no task either (%v)", err)
		}

		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

		if strings.Contains(out, "pull-request index") {
			t.Errorf("the update says something about the pull-request index with nothing to write:\n%s", out)
		}
		if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
			t.Errorf("the update wrote %v over runs that name no pull request", keys)
		}
	})
}

// TestUpdateBackfillLeavesRunInFlight is the HARD RULE's own half of the step
// (docs/design/update-in-flight.md, the row this task adds): the update
// converges while a run of the installation's repository is in flight, the
// backfill writes the entries of the runs that name a pull request, and every
// object a run holds is byte for byte what it was.
//
// The fixture is TestUpdateLeavesARunInFlightRunning's — a real budget session
// and a real run record, started by startRunInFlight — with the runs of the
// release behind planted beside it, so one update is holding both: the runs a
// person can list, which are what the backfill is for, and the run going right
// now, which no step of the update may touch. The entry a run in flight will
// need is written by the run itself when it opens its pull request — the same
// release's runner half — and never by this walk: a run that has written no task
// yet names no pull request this walk could place, and the update is never a
// reason for it to wait.
func TestUpdateBackfillLeavesRunInFlight(t *testing.T) {
	r := prIndexRig(t)
	ids := plantPRIndexRuns(t, r)
	b := prIndexBucket(t, r)
	held := []string{ids.root, ids.fu, ids.other, ids.plain, ids.review, ids.running}
	// The summaries the backfill of the same update writes (update_summary.go)
	// are left out of both readings: this test is about the index step's own
	// reach, and a summary is a derived object no step rewrites once it is
	// there — TestUpdateSummaryLeavesRunInFlight holds that half.
	before := map[string][]byte{}
	for _, id := range held {
		for key, data := range runsTree(t, b, "runs/"+appSlug+"/"+id+"/") {
			if strings.HasSuffix(key, "/"+runstore.SummaryName) {
				continue
			}
			before[key] = data
		}
	}
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// The note about the runs in flight is untouched by this step: the runs are
	// counted by the converge, and the backfill adds no line about them and makes
	// the update wait for none.
	if line := inFlightLine(t, out); !strings.Contains(line, "1 run in flight") {
		t.Errorf("the update's note does not count the run in flight:\n%s", line)
	}
	if !strings.Contains(out, "pull-request index: 4 run(s)") {
		t.Errorf("the update indexed nothing beside the run in flight:\n%s", out)
	}
	// The entries are there, and no run that is still going is among them: the
	// run in flight has written no task, and the run that will name a pull
	// request writes its own entry when it opens it.
	seven := prIndexEntries(t, b, appSlug, sevenPR)
	if got, want := indexRunIDs(seven), slices.Sorted(slices.Values([]string{ids.root, ids.fu, ids.review})); !slices.Equal(got, want) {
		t.Errorf("the index of pull request %d names %v, want the three runs the fixture has on it", sevenPR, got)
	}
	// What every planted run holds is what it held: the step creates the objects
	// that are missing and rewrites nothing, so the whole of every run's own
	// prefix is unchanged.
	after := map[string][]byte{}
	for _, id := range held {
		for key, data := range runsTree(t, b, "runs/"+appSlug+"/"+id+"/") {
			if strings.HasSuffix(key, "/"+runstore.SummaryName) {
				continue
			}
			after[key] = data
		}
	}
	sameTree(t, "the backfill", before, after)
	// And the run in flight goes on: its lease, its ledger, its counters, its
	// record and its review session all still land.
	run.laterWrites(t, r)
}

// TestUpdateBackfillWritesNothingOutsideTheUpdateDoor: the step is `fugaro
// update`'s, and it keeps the fences the steps beside it keep (update.go's
// writeAdminList, update_marker_version.go's convergeMarkerVersion): `fugaro
// setup` converges the same installation through the same hook (init_stages.go's
// outcome), `--plan-only` writes nothing at all, and a coding agent's session
// under a marker changes nothing outside this machine unless the managed gate is
// the read that stands in for this run's confirmations (D1016).
//
// What is asserted is whether the runs bucket is reached at all, which is the
// difference between "the step decided not to write" and "the step wrote nothing
// here": every case below has a bucket it could write an entry into, and the
// three fenced ones never open it.
func TestUpdateBackfillWritesNothingOutsideTheUpdateDoor(t *testing.T) {
	cases := []struct {
		name  string
		fence func(t *testing.T, e *initEngine)
		// opened is whether the step is expected to reach the runs bucket: the
		// three fenced cases never open it, the two admitted ones do — and write
		// nothing, the bucket being empty.
		opened bool
	}{
		{"fugaro setup", func(_ *testing.T, e *initEngine) { e.r.o.door = setupDoor.name }, false},
		{"--plan-only", func(_ *testing.T, e *initEngine) { e.r.o.planOnly = true }, false},
		{"a coding agent's session", func(t *testing.T, _ *initEngine) { agentSession(t) }, false},
		{"a coding agent's session in a managed project", func(t *testing.T, e *initEngine) {
			agentSession(t)
			e.r.gate = &initGate{managed: true}
		}, true},
		{"the update door at the owner's terminal", func(*testing.T, *initEngine) {}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _, opened := prIndexStepEngine(t)
			c.fence(t, e)
			e.backfillPRIndex(context.Background())
			if *opened != c.opened {
				t.Errorf("the step opened the runs bucket: %v, want %v: what keeps it out of one is the door it is reached by, the preview it runs under and the session it is in", *opened, c.opened)
			}
		})
	}
}

// prIndexStepEngine is the step's fixture without the call: the engine, where
// the step writes what it prints, and whether it opened the runs bucket. A test
// that has to say something about the run the step is reached by rather than
// about the step alone — the door, the preview, the session, the gate — takes
// the engine, sets that, and calls backfillPRIndex itself.
func prIndexStepEngine(t *testing.T) (e *initEngine, out *strings.Builder, opened *bool) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		RunsBucket: "fugaro-runs-" + initProject,
		Bucket:     "file://" + t.TempDir(),
		Budget:     &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints:  localcfg.Endpoints{NoAuth: true},
	}
	touched := false
	prev := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		touched = true
		return blobx.Open(ctx, url)
	}
	t.Cleanup(func() { sharedBucketOpener = prev })

	buf := &strings.Builder{}
	return &initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}, w: buf}, lc: lc}, buf, &touched
}
