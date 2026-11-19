package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// liveWindow is the mixed window the live queries are judged on: a run whose
// execution is going, a run whose launch is in flight, a run that succeeded, a
// run that failed, and a run whose claim is past its bound.
type liveWindow struct{ running, launching, succeeded, failed, stale string }

// seedLiveWindow seeds it into the fixture's own bucket. The runs that ended
// are an hour old, which is outside the claim window: the runs inside it cost
// one claim probe each, that probe being the only way to tell a run being
// launched from one that is not.
func seedLiveWindow(t *testing.T, f *cloudFixture) liveWindow {
	t.Helper()
	w := liveWindow{running: recentRunID(t, time.Hour, "aaaa"), succeeded: recentRunID(t, time.Hour, "cccc"),
		failed: recentRunID(t, time.Hour, "dddd"), stale: recentRunID(t, time.Hour, "eeee"), launching: recentRunID(t, 0, "bbbb")}
	exec := seedLiveRun(t, f, w.running, &runstore.Record{Version: 1, RunID: w.running, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusRunning, Stage: "implement"}, nil)
	f.run.SetState(exec, backend.StateRunning)
	now := lsNow()
	seedLiveRun(t, f, w.launching, nil, &now)
	exec = seedLiveRun(t, f, w.succeeded, &runstore.Record{Version: 1, RunID: w.succeeded, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded}, nil)
	f.run.SetState(exec, backend.StateSucceeded)
	exec = seedLiveRun(t, f, w.failed, &runstore.Record{Version: 1, RunID: w.failed, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusFailed}, nil)
	f.run.SetState(exec, backend.StateFailed)
	stale := now.Add(-runstore.ClaimTTL - time.Minute)
	seedLiveRun(t, f, w.stale, nil, &stale)
	return w
}

// seedLiveRun writes one run of the window — its task, and a launch and a
// record as the case needs — and returns the execution its launch started
// ("" when it was never launched). claimAt, when given, is the moment the run
// holds a launch claim from: a fresh one is a launch in flight, one past
// runstore.ClaimTTL is a launcher that is gone.
func seedLiveRun(t *testing.T, f *cloudFixture, id string, rec *runstore.Record, claimAt *time.Time) string {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if err := s.CreateTask(ctx, firstRunSpec(id)); err != nil {
		t.Fatal(err)
	}
	exec := ""
	if rec != nil {
		// A run the runner got to is a run a launcher started: its launch.json
		// and its record are both there, and the row's execution is the
		// record's.
		exec = startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, id)
		at, err := runstore.RunTime(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: at}); err != nil {
			t.Fatal(err)
		}
		rec.Execution = exec
		if err := s.WriteRecord(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if claimAt != nil {
		ok, existing, err := s.Claim(ctx, "tester", *claimAt)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("seeding %s: the claim is held by %q", id, existing.Holder)
		}
	}
	return exec
}

// scanState is the rows the slow way returns for state: the whole window read
// run by run, joined, and filtered by the same row predicate --state applies.
// It is the reading the live set must match, and the one the two live states
// exist to stop paying for.
func scanState(t *testing.T, f *cloudFixture, state string) []runview.Row {
	t.Helper()
	ctx := context.Background()
	env, err := openCloud(ctx, cloudOptions{stderr: discard})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	now := lsNow()
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{appSlug}, since: now.Add(-lsDefaultWindow), noCache: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(rows, func(r runview.Row) bool { return !rowInState(r, state) })
}

// jsonRows is rows as the listing's own JSON, for a failure message a person
// can read.
func jsonRows(t *testing.T, rows []runview.Row) string {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// lsLimitFixture installs the counting runs bucket over the objects the
// fixture's own bucket already holds, so a test reads which keys a listing's
// reads reached. It must come after every seed.
func lsLimitFixture(t *testing.T, f *cloudFixture) *lsWidthDriver {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return lsLimitInstall(t, f, blobx.Wrap(b))
}

// TestLiveSetEqualsScan: --state running and --state launching return the rows
// the scan returns, field for field, on a window that holds a run going, a run
// being launched, two that ended and a claim past its bound.
//
// The clock is pinned so that the two readings are priced at the same instant:
// a live row's compute is the window its execution has been billed for, which
// grows between two readings of the same row by a few milliseconds, and the
// comparison is of the rows and not of when they were taken.
//
// Mutation caught: answering the live states from the live set alone, without
// the read, the join and the filters every other listing goes through — a row
// missing the columns the window read fills, or a status the state asked for
// that the row's own objects do not say.
func TestLiveSetEqualsScan(t *testing.T) {
	defer func(old func() time.Time) { lsNow = old }(lsNow)
	at := time.Now().UTC()
	lsNow = func() time.Time { return at }
	f := newCloudFixture(t)
	w := seedLiveWindow(t, f)
	for _, c := range []struct{ state, want string }{
		{string(runstore.StatusRunning), w.running},
		{runview.StatusLaunching, w.launching},
	} {
		live, _ := lsJSON(t, "--state", c.state)
		if len(live.Runs) != 1 || live.Runs[0].RunID != c.want {
			t.Fatalf("--state %s = %v, want the one run %s", c.state, runIDs(live.Runs), c.want)
		}
		if scan := scanState(t, f, c.state); jsonRows(t, live.Runs) != jsonRows(t, scan) {
			t.Errorf("--state %s = %s, the scan's rows are %s", c.state, jsonRows(t, live.Runs), jsonRows(t, scan))
		}
	}
	// A window that leaves a live run out leaves it out of both readings: the
	// run ID's own mint time bounds the live set the way it bounds the window
	// listing, so a run launched long after it was minted is not a run of a
	// window that does not hold it.
	if got, _ := lsJSON(t, "--state", "running", "--since", "30m"); len(got.Runs) != 0 {
		t.Errorf("--state running --since 30m = %v, want the runs minted in the last 30 minutes", runIDs(got.Runs))
	}
	if got, _ := lsJSON(t, "--state", "launching", "--since", "30m"); len(got.Runs) != 1 || got.Runs[0].RunID != w.launching {
		t.Errorf("--state launching --since 30m = %v, want %s", runIDs(got.Runs), w.launching)
	}
}

// TestLiveSetReadsNoFinishedRun: a live listing reads no object of a run that
// ended, which is what makes it cheap on a repository with a long history. The
// counting bucket is asked which keys the reads reached, and the contrast is
// the scan: a word the scan answers reads every run of the window.
func TestLiveSetReadsNoFinishedRun(t *testing.T) {
	f := newCloudFixture(t)
	w := seedLiveWindow(t, f)
	d := lsLimitFixture(t, f)

	got, _ := lsJSON(t, "--state", "running")
	if len(got.Runs) != 1 || got.Runs[0].RunID != w.running {
		t.Fatalf("--state running = %v, want the one run going", runIDs(got.Runs))
	}
	read := func(keys []string, id string) bool {
		return slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "runs/"+appSlug+"/"+id+"/") })
	}
	live := d.keysRead()
	for _, id := range []string{w.succeeded, w.failed, w.stale} {
		if read(live, id) {
			t.Errorf("--state running read %s's objects: a run that is not going is not in the live set", id)
		}
	}
	if !read(live, w.running) {
		t.Errorf("--state running read none of the running run's objects: %v", live)
	}
	// The run being launched is found by its claim and read from there: that
	// probe is the one object a live listing spends on a run that is not going
	// yet, and it is what makes the launching state exact.
	if !slices.Contains(live, "runs/"+appSlug+"/"+w.launching+"/launching") {
		t.Errorf("--state running did not probe the launch claim of %s: %v", w.launching, live)
	}

	before := len(live)
	if got, _ := lsJSON(t, "--state", "succeeded"); len(got.Runs) != 1 || got.Runs[0].RunID != w.succeeded {
		t.Fatalf("--state succeeded = %v, want the run that succeeded", runIDs(got.Runs))
	}
	scan := d.keysRead()[before:]
	for _, id := range []string{w.succeeded, w.failed} {
		if !read(scan, id) {
			t.Errorf("--state succeeded read none of %s's objects, which its window covers: the scan is the contrast this test is measured against", id)
		}
	}
}

// TestLiveSetOneExecutionList: the two live states make one backend call for
// the executions and read no execution of their own — the listing that found
// the runs is the listing the join uses (U2).
func TestLiveSetOneExecutionList(t *testing.T) {
	f := newCloudFixture(t)
	w := seedLiveWindow(t, f)
	if got, _ := lsJSON(t, "--state", "running"); len(got.Runs) != 1 || got.Runs[0].RunID != w.running {
		t.Fatalf("--state running = %v, want the one run going", runIDs(got.Runs))
	}
	var lists, reads int
	for _, r := range f.run.Requests() {
		if r.Method != "GET" {
			continue
		}
		switch {
		case strings.HasSuffix(r.Path, "/executions"):
			lists++
		case strings.Contains(r.Path, "/executions/"):
			reads++
		}
	}
	if lists != 1 {
		t.Errorf("%d executions listings, want 1: the live set is one call, and the join takes its answer", lists)
	}
	if reads != 0 {
		t.Errorf("%d per-execution reads, want none: the listing that found the run names it its execution", reads)
	}
}

// TestLiveSetMissesARelaunchUnderAnOlderRunID pins the one run the live query
// can miss, so the divergence is a fact a reader can see rather than a hole
// nobody wrote down: a relaunch under an older run ID -- `run --retry`, or a
// re-issue by the resident launcher -- takes a fresh claim on a run minted long
// before, and the claim pass is bounded by the claim window's run IDs, which do
// not reach it. The scan reads it as launching; the live query does not.
//
// The bound is deliberate: a claim says nothing about the run it is on, so
// finding one on an older run ID means probing every run of the window, on
// --state running as well, which is the cost these two states exist to avoid
// (F2, D2960). The opposite test is the window's own mixed fixture, where the
// two readings are equal.
//
// Mutation caught: bounding the claim pass by anything wider (the window's own
// run IDs) while leaving this pinned divergence unrecorded in liveRuns' comment
// and in the --state help.
func TestLiveSetMissesARelaunchUnderAnOlderRunID(t *testing.T) {
	f := newCloudFixture(t)
	id := recentRunID(t, time.Hour, "ffff")
	now := lsNow()
	seedLiveRun(t, f, id, nil, &now)

	scan := scanState(t, f, runview.StatusLaunching)
	if len(scan) != 1 || scan[0].RunID != id {
		t.Fatalf("the scan reads the relaunched run as %v, want the one launching run %s: this test pins the divergence below, so a scan that stopped reading it would leave the pin saying nothing", runIDs(scan), id)
	}
	if got, _ := lsJSON(t, "--state", "launching"); len(got.Runs) != 0 {
		t.Errorf("--state launching = %v, want none: the claim is on a run ID older than the claim window, and this test pins that the live query does not reach it", runIDs(got.Runs))
	}
}

// TestLiveStateWithPRFold: the fold's live word comes from the live set too. A
// run going on a pull request carries `live` on its own row, without reading
// the runs of that pull request that ended (D2967) — and the word --state
// live, which is a state of the pull request and not of a run, still reads the
// window, so the ended run of a live pull request is still listed under it.
func TestLiveStateWithPRFold(t *testing.T) {
	f := newCloudFixture(t)
	root := recentRunID(t, time.Hour, "aaaa")
	fu := recentRunID(t, 0, "bbbb")
	seedSpec(t, f, firstRunSpec(root), false)
	writeRecord(t, f, root, prRecord(root, "", 7, 2))
	exec := seedSpec(t, f, followUpSpec(fu, root, root, 7), true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, fu, &runstore.Record{Version: 1, RunID: fu, Repo: "acme/app", Workflow: "web", Status: runstore.StatusRunning,
		Execution: exec, PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"}})

	got, _ := lsJSON(t, "--state", "running")
	if len(got.Runs) != 1 || got.Runs[0].RunID != fu || got.Runs[0].PRState != prStateLive {
		t.Fatalf("--state running = %s, want the follow-up alone, live", jsonRows(t, got.Runs))
	}
	live, _ := lsJSON(t, "--state", prStateLive)
	if len(live.Runs) != 2 || !slices.Contains(runIDs(live.Runs), root) {
		t.Fatalf("--state live = %s, want the pull request's runs, the one that ended among them", jsonRows(t, live.Runs))
	}
	for _, r := range live.Runs {
		if r.PRState != prStateLive {
			t.Errorf("--state live listed %s with pr_state %q", r.RunID, r.PRState)
		}
	}
}
