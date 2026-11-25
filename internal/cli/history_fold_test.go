package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// foldFixture is the history job's world with the two things a fold needs
// beside it: a runs bucket that survives from one execution to the next, and
// the run objects of a run to fold.
//
// The bucket is a directory rather than the in-memory fake the other history
// tests use, because each execution of the job closes the bucket it opened
// (history.go, history_rollover.go) and a closed memblob bucket is empty to
// every later read. A file bucket reopens, which is what lets one test run two
// executions of the job over one installation.
type foldFixture struct {
	t *testing.T
	*rolloverFixture
	dir    string
	bucket *blobx.Bucket
}

// The runs the fold tests fold. The ids are the shape task.NewRunID mints.
const (
	foldRunOne = "20261019-100000-aaaa"
	foldRunTwo = "20261019-110000-bbbb"
)

func newFoldFixture(t *testing.T) *foldFixture {
	t.Helper()
	f := &foldFixture{t: t, rolloverFixture: newRolloverFixture(t), dir: t.TempDir()}
	f.bucket = f.openBucket()
	historyTest.bucket = func(ctx context.Context, name string) (*blobx.Bucket, error) { return f.openBucket(), nil }
	// The lease's holder is the execution's name, which is how a lease a person
	// reads names a job run rather than a process.
	t.Setenv("CLOUD_RUN_EXECUTION", "fugaro-history-abc12")
	return f
}

func (f *foldFixture) openBucket() *blobx.Bucket {
	f.t.Helper()
	b, err := fileblob.OpenBucket(f.dir, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return blobx.Wrap(b)
}

// run writes one run's own records into the runs bucket, as the runner does.
func (f *foldFixture) run(id string, status runstore.Status, started time.Time, cost *runstore.Cost) {
	f.t.Helper()
	ctx := context.Background()
	st := runstore.Open(f.bucket.Bucket, appSlug, id)
	spec := &task.Spec{
		Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "# Task: the index folds\n\nthe body", Title: "the index folds",
		PlanTask: "console-fixes/RI4", RequestedBy: "dimi@example.invalid", Batch: "20261019-100000",
	}
	if err := st.WriteTask(ctx, spec); err != nil {
		f.t.Fatal(err)
	}
	launch := &runstore.Launch{
		Version: 1, RunID: id, Backend: "cloud-run", Execution: "projects/p/executions/e",
		Job: f.job, LaunchedBy: "dimi@example.invalid", LaunchedAt: started.Add(-time.Minute), PlanTask: "console-fixes/RI4",
	}
	if err := st.WriteLaunch(ctx, launch); err != nil {
		f.t.Fatal(err)
	}
	fin := started.Add(20 * time.Minute)
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: "acme/app", Status: status, Stage: "finalize",
		StartedAt: started, FinishedAt: &fin, Cost: cost,
		Tier:  &runstore.TierRecord{Tier: tier.Medium, ExpectedUSD: 4, ExpectedMinutes: 30},
		Rigor: &runstore.RigorRecord{Mode: rigor.Pro, Level: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize},
	}
	if err := st.WriteRecord(ctx, rec); err != nil {
		f.t.Fatal(err)
	}
}

func foldCost(model, notional, compute float64, estimated bool) *runstore.Cost {
	return &runstore.Cost{
		ModelUSD: model, ModelNotionalUSD: notional, ComputeUSD: compute, ComputeEstimated: estimated,
		TotalUSD: model - notional + compute, ModelBasis: runstore.BasisAPIList,
		CacheReadTokens: 1200, CacheWriteTokens: 300,
		ModelTokens: map[string]runstore.ModelTokenCounts{"claude-x": {Input: 10, Output: 5}},
	}
}

// bucketKeys lists the runs bucket's object names, which is how a fold that
// wrote a run object would be caught.
func (f *foldFixture) bucketKeys() []string {
	f.t.Helper()
	b := f.openBucket()
	defer b.Close()
	var out []string
	iter := b.List(nil)
	for {
		obj, err := iter.Next(context.Background())
		if err != nil {
			break
		}
		out = append(out, obj.Key)
	}
	return out
}

func (f *foldFixture) entryDoc(id string) map[string]any {
	f.t.Helper()
	doc, ok := f.fs.Value(runindex.EntriesCollection, id)
	if !ok {
		f.t.Fatalf("the sweep left no index document for %s", id)
	}
	return doc
}

func (f *foldFixture) summaryDoc() map[string]any {
	f.t.Helper()
	doc, ok := f.fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		f.t.Fatal("the sweep left no index summary")
	}
	return doc
}

// TestHistorySweepFolds is the sweep's last step: the runs that changed since
// the index's stamp become documents, out of the runs bucket and into
// Firestore, and the summary carries the stamp the next sweep resumes from.
//
// The red step this pins was an installation whose index stayed empty however
// many sweeps ran: the sweep is the index's only incremental writer (D3201),
// so a sweep that does not fold leaves every reader of the index reading the
// overlay alone.
func TestHistorySweepFolds(t *testing.T) {
	f := newFoldFixture(t)
	f.entry(foldRunOne, time.Minute)
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(3.5, 1.0, 0.25, true))
	// A run that is in flight: a task and a launch, no record at all.
	f.entry(foldRunTwo, time.Minute)
	f.runInFlight(foldRunTwo, f.now.Add(-time.Hour))

	before := f.bucketKeys()
	out, _, err := execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "index: 2 folded, 0 unreadable, through "+f.now.Format(time.RFC3339)) {
		t.Fatalf("the sweep does not say what it folded: %q", out)
	}
	// No run object is written: the history account reads the runs bucket and
	// never writes it.
	if after := f.bucketKeys(); len(after) != len(before) {
		t.Fatalf("the fold wrote a run object: %v became %v", before, after)
	}

	one := f.entryDoc(foldRunOne)
	if one["slug"] != appSlug || one["repo"] != "acme/app" || one["run_id"] != foldRunOne {
		t.Fatalf("entry = %v", one)
	}
	if one["status"] != string(runstore.StatusSucceeded) || one["plan_task"] != "console-fixes/RI4" ||
		one["title"] != "the index folds" || one["tier"] != string(tier.Medium) || one["rigor"] != string(rigor.Pro) {
		t.Fatalf("entry = %v", one)
	}
	if rec, ok := one["record"].(map[string]any); !ok {
		t.Fatalf("record = %#v", one["record"])
	} else if _, ok := rec["started_at"].(time.Time); !ok {
		t.Fatalf("record.started_at = %#v, want a timestamp", rec["started_at"])
	}
	// A run that never finalized carries no record and no status: the entry is
	// still written, because the run exists.
	two := f.entryDoc(foldRunTwo)
	if _, ok := two["record"]; ok {
		t.Fatalf("a run in flight carries a record: %v", two)
	}
	if _, ok := two["status"]; ok {
		t.Fatalf("a run in flight carries a status: %v", two)
	}

	sum := f.summaryDoc()
	if sum["runs"] != int64(2) || sum["spent_micros"] != int64(2_500_000) || sum["compute_runs"] != int64(1) {
		t.Fatalf("summary = %v", sum)
	}
	if states, ok := sum["states"].(map[string]any); !ok || states["succeeded"] != int64(1) {
		t.Fatalf("states = %#v", sum["states"])
	}
	if stamp, ok := sum["folded_through"].(time.Time); !ok || !stamp.Equal(f.now) {
		t.Fatalf("folded_through = %#v, want %s", sum["folded_through"], f.now)
	}
	// The lease is freed, so the next execution folds rather than skipping.
	if got := f.db.Value(runindex.PathLease); got != nil {
		t.Fatalf("the sweep left its lease behind: %v", got)
	}
}

// runInFlight writes a run that has a task and a launch and no result yet.
func (f *foldFixture) runInFlight(id string, started time.Time) {
	f.t.Helper()
	ctx := context.Background()
	st := runstore.Open(f.bucket.Bucket, appSlug, id)
	spec := &task.Spec{
		Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web",
		Task: "# Task: the index folds\n\nthe body", Title: "the index folds",
		PlanTask: "console-fixes/RI4", RequestedBy: "dimi@example.invalid", Batch: "20261019-100000",
	}
	if err := st.WriteTask(ctx, spec); err != nil {
		f.t.Fatal(err)
	}
	launch := &runstore.Launch{
		Version: 1, RunID: id, Backend: "cloud-run", Execution: "projects/p/executions/e",
		Job: f.job, LaunchedBy: "dimi@example.invalid", LaunchedAt: started, PlanTask: "console-fixes/RI4",
	}
	if err := st.WriteLaunch(ctx, launch); err != nil {
		f.t.Fatal(err)
	}
}

// TestRolloverKeepsDaysAfterFold is the two executions of one installation:
// the sweep that folds, and the rollover that reads what it left. The rollover
// moves days out of the database and then DELETES their nodes, and a day the
// fold has not walked is one whose runs would be missing from the index for
// good -- the fold finds what changed through outcomes/<day>/ and agents/, and
// a deleted day is not there to find. So it prunes only the days before the
// stamp, keeps the days at or after it, and says so.
//
// The second half is the same pass over a stamp that has stopped moving: a
// stamp further behind than the days this database keeps is a gap no future
// fold can close, and the pass that reads it latches the index incomplete so
// every reader of a short list says so.
func TestRolloverKeepsDaysAfterFold(t *testing.T) {
	f := newFoldFixture(t)
	today := budget.Day(f.now)
	// A run that ended ten days ago, folded by the sweep of that day.
	old := today - 10
	f.entry(foldRunOne, time.Minute)
	f.seed(old, foldRunOne, 1_000_000)
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(1.0, 0.0, 0.1, true))

	if out, _, err := execute(t, "budget", "history", "--sweep"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "index: 1 folded") {
		t.Fatalf("the sweep did not fold: %q", out)
	}
	stamp := f.summaryDoc()["folded_through"]
	if at, ok := stamp.(time.Time); !ok || !at.Equal(f.now) {
		t.Fatalf("folded_through = %#v", stamp)
	}

	// Ten days later, the rollover: the stamp is now behind the days this
	// database keeps, so the day the fold read is prunable while a day after
	// the stamp -- old enough to prune by age -- is not.
	f.now = f.now.AddDate(0, 0, 10)
	kept := today + 1
	f.seed(kept, "20261021-100000-cccc", 500_000)

	out, stderr, err := execute(t, "budget", "history", "--rollover")
	if err != nil {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
	if f.db.Value("spend/"+budget.DayKey(old)) != nil {
		t.Errorf("the day the fold read was kept: %q", out)
	}
	if f.db.Value("spend/"+budget.DayKey(kept)) == nil {
		t.Errorf("a day after the fold's stamp was pruned: %q", out)
	}
	if !strings.Contains(out, budget.DayDate(kept)+": ") || !strings.Contains(out, "waits for the fold") {
		t.Errorf("the pass does not say which day it kept for the fold: %q", out)
	}
	// The stamp is older than PruneAfter, which is the gap no fold can close:
	// the index is marked incomplete and the warning names the reindex.
	if !strings.Contains(stderr, "not been folded since") || !strings.Contains(stderr, "reindex") {
		t.Errorf("a stale fold warns about nothing: %q", stderr)
	}
	if got := f.summaryDoc()["complete"]; got != false {
		t.Errorf("a stale fold left the index complete: %v", got)
	}
}

// TestStaleFoldMarksIncomplete is the mark itself, at the two ends of the
// rule: a stamp the days this database keeps can no longer reach sets
// complete: false -- what `fugaro doctor` reads (RI6) and what the reindex
// (RI5) clears -- and a stamp inside the window leaves the summary alone.
func TestStaleFoldMarksIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name   string
		behind int64
		want   bool
	}{
		{"a stamp further behind than the days the database keeps", budget.PruneAfter + 1, false},
		{"a stamp inside the window", budget.PruneAfter, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFoldFixture(t)
			// The index's summary, as a fold would have left it.
			stamp := f.now.AddDate(0, 0, -int(tc.behind))
			f.fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, map[string]any{
				"version": int64(1), "complete": true, "folded_through": stamp, "updated_at": stamp,
				"runs": int64(1), "states": map[string]any{"succeeded": int64(1)},
				"spent_micros": int64(0), "notional_micros": int64(0),
				"compute_runs": int64(0), "compute_not_estimated": int64(0),
			})
			_, stderr, err := execute(t, "budget", "history", "--rollover")
			if err != nil {
				t.Fatalf("err = %v, stderr = %q", err, stderr)
			}
			sum := f.summaryDoc()
			if got, ok := sum["complete"].(bool); !ok || got != tc.want {
				t.Fatalf("complete = %#v, want %v (stderr = %q)", sum["complete"], tc.want, stderr)
			}
			if stale := strings.Contains(stderr, "not been folded since"); stale != !tc.want {
				t.Fatalf("warned = %v, want %v: %q", stale, !tc.want, stderr)
			}
			// The fold's own stamp is not the rollover's to move: the reindex
			// reads it to know where the walk resumes.
			if at, ok := sum["folded_through"].(time.Time); !ok || !at.Equal(stamp) {
				t.Fatalf("the rollover moved the stamp: %#v", sum["folded_through"])
			}
		})
	}
}

// executeHistoryCtx runs the command tree with a context of its own, which is
// how a test hands the history job a deadline the process's clock cannot reach:
// the command derives its own eight-minute timeout from this context, and
// context.WithTimeout keeps the shorter one, so a test's deadline is the
// execution's.
func executeHistoryCtx(t *testing.T, ctx context.Context, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetContext(ctx)
	cmd.SetArgs(NormalizeArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestHistoryReindexWalksTheBucket is the job's --reindex mode end to end
// (RI5): the flag the history job is started with walks every run in the runs
// bucket into the index, recomputes the summary from what it wrote and says
// what it did. It is the one mode whose whole job is the walk -- a sweep folds
// only what changed and a rollover moves days -- and the pass an operator
// watches after `fugaro runs reindex` or `fugaro update` starts it.
func TestHistoryReindexWalksTheBucket(t *testing.T) {
	f := newFoldFixture(t)
	f.entry(foldRunOne, time.Minute)
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(3.5, 1.0, 0.25, true))
	// A run that is in flight: a task and a launch, no record at all. The walk
	// takes it too, because the walk reads the bucket and not the registry.
	f.entry(foldRunTwo, time.Minute)
	f.runInFlight(foldRunTwo, f.now.Add(-time.Hour))

	before := f.bucketKeys()
	out, _, err := execute(t, "budget", "history", "--reindex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "index: 2 walked, 0 unreadable, through "+f.now.Format(time.RFC3339)+" (complete)") {
		t.Fatalf("the reindex does not say what it walked: %q", out)
	}
	// The walk reads the runs bucket and never writes it.
	if after := f.bucketKeys(); len(after) != len(before) {
		t.Fatalf("the reindex wrote a run object: %v became %v", before, after)
	}
	for _, id := range []string{foldRunOne, foldRunTwo} {
		if _, ok := f.fs.Value(runindex.EntriesCollection, id); !ok {
			t.Fatalf("the reindex left %s out of the index: %v", id, f.fs.IDs(runindex.EntriesCollection))
		}
	}
	if sum := f.summaryDoc(); sum["complete"] != true || sum["runs"] != int64(2) {
		t.Fatalf("summary = %v", sum)
	}
	// A completed walk frees the lease, so the next sweep folds rather than
	// walking behind it.
	if got := f.db.Value(runindex.PathLease); got != nil {
		t.Fatalf("the reindex left its lease behind: %v", got)
	}
}

// TestHistoryReindexIsBoundedByTheExecutionDeadline pins the wiring the mode
// exists for: a pass is bounded by the execution's own deadline -- the job's
// eight minutes, or the shorter one a caller hands it -- minus reindexReserve,
// so a walk that reaches the bound stops at the run it is on and leaves its
// position with the lease for the next sweep to continue, rather than being
// killed by the platform with nothing to resume from (F39, RI5).
//
// The deadline here is thirty seconds out and the reserve is forty-five, so the
// bound is already behind the walk when it reaches the first run: the pass
// walks exactly that one and stops. The second execution is the resume -- a
// plain --sweep finds the cursor and carries the walk to the end.
func TestHistoryReindexIsBoundedByTheExecutionDeadline(t *testing.T) {
	f := newFoldFixture(t)
	// The walk's clock is the process's, so the execution's deadline is in the
	// future for the fakes' HTTP calls and still inside the walk's own window.
	f.now = time.Now().UTC()
	f.entry(foldRunOne, time.Minute)
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(3.5, 1.0, 0.25, true))
	f.entry(foldRunTwo, time.Minute)
	f.run(foldRunTwo, runstore.StatusSucceeded, f.now.Add(-2*time.Hour), foldCost(1.0, 0.0, 0.1, true))

	ctx, cancel := context.WithDeadline(context.Background(), f.now.Add(30*time.Second))
	defer cancel()
	out, _, err := executeHistoryCtx(t, ctx, "budget", "history", "--reindex")
	if err != nil {
		t.Fatal(err)
	}
	// The runs are walked newest first, so the pass stops on the newer one.
	want := "index: 1 walked, 0 unreadable, through " + f.now.Format(time.RFC3339) + " (unfinished: the next sweep continues from there)"
	if !strings.Contains(out, want) {
		t.Fatalf("a pass cut by the execution deadline does not say so: %q, want %q", out, want)
	}
	// The position is left with the lease rather than thrown away with it.
	l, ok := f.db.Value(runindex.PathLease).(map[string]any)
	if !ok {
		t.Fatalf("the unfinished walk left no lease: %#v", f.db.Value(runindex.PathLease))
	}
	if cursor := (runindex.Cursor{Slug: appSlug, RunID: foldRunTwo}).Encode(); l["cursor"] != cursor {
		t.Fatalf("the unfinished walk left cursor %#v, want the run it stopped on (%s)", l["cursor"], foldRunTwo)
	}

	// The next sweep continues from the cursor instead of starting over.
	out, _, err = execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "index: 1 walked, 0 unreadable, through "+f.now.Format(time.RFC3339)+" (complete)") {
		t.Fatalf("the sweep did not continue the walk: %q", out)
	}
	if sum := f.summaryDoc(); sum["complete"] != true || sum["runs"] != int64(2) {
		t.Fatalf("summary = %v", sum)
	}
	if got := f.db.Value(runindex.PathLease); got != nil {
		t.Fatalf("the completed walk left its lease behind: %v", got)
	}
}

// TestHistoryReindexSaysWhoHoldsTheLease is printWalk's third answer, and the
// one only --reindex can reach: a sweep that finds a fresh claim reports it at
// the sweep and never walks, while --reindex asks for the walk itself and is
// told who is in the way. An operator who starts a reindex and reads nothing
// has no way to tell a walk that found nothing from one that never ran, so the
// pass names the holder and exits cleanly, having written nothing.
func TestHistoryReindexSaysWhoHoldsTheLease(t *testing.T) {
	f := newFoldFixture(t)
	// The database's clock is the fixture's, so the claim below is fresh when
	// the pass judges it rather than lapsed by the process's own clock.
	f.db.SetClock(func() time.Time { return f.now })
	at := f.now.UTC()
	f.db.Set(runindex.PathLease, map[string]any{
		"holder":     "fugaro-history-xyz12",
		"at":         at.Format(time.RFC3339Nano),
		"expires_at": at.Add(runindex.LeaseTTL).Format(time.RFC3339Nano),
	})
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(3.5, 1.0, 0.25, true))

	out, _, err := execute(t, "budget", "history", "--reindex")
	if err != nil {
		t.Fatalf("a reindex that reached another writer is not a failure: %v", err)
	}
	if !strings.Contains(out, "index: not walked: fugaro-history-xyz12 holds the writer lease") {
		t.Fatalf("the reindex does not say who holds the lease: %q", out)
	}
	// Nothing was read into the index and nothing was written: the holder's
	// walk is untouched.
	if _, ok := f.fs.Value(runindex.EntriesCollection, foldRunOne); ok {
		t.Fatal("a reindex wrote while another writer held the lease")
	}
	if _, ok := f.fs.Value(runindex.SummaryCollection, runindex.SummaryDocument); ok {
		t.Fatal("a reindex wrote the summary while another writer held the lease")
	}
	if got := f.db.Value(runindex.PathLease); got == nil {
		t.Fatal("the reindex freed a lease it never took")
	}
}

// TestSweepDuringReindexStillSweeps is RI5's edge with the rest of the job: a
// reindex holding the index's writer lease stops the fold and nothing else. The
// crash sweep is the sweep's own work and runs anyway (D12) -- a registry entry
// a dead run left behind is removed exactly as it is when no walk is going --
// and the rollover is a separate execution that never read this lease at all.
//
// What the sweep may not do is fold: the summary is a sum over the entries, and
// a fold beside a walk adds the same run's delta to a figure both of them read.
func TestSweepDuringReindexStillSweeps(t *testing.T) {
	f := newFoldFixture(t)
	// The fixture's clock is the database's too, so a claim written from it is
	// judged the way the sweep judges one.
	f.db.SetClock(func() time.Time { return f.now })

	// A crashed run for the crash sweep: a registry entry with a dead
	// heartbeat, and no execution of the platform's to say otherwise.
	crashed := "20261002-090000-aaaa"
	f.db.Set("agents/"+appSlug+"/"+crashed, map[string]any{
		"repo": "acme/app", "workflow": "web", "requestedBy": "dimi@example.invalid",
		"startedAt": f.now.Add(-time.Hour).UnixMilli(), "updatedAt": f.now.Add(-time.Hour).UnixMilli(),
	})
	// A run the fold would write if it ran: its registry heartbeat is fresh,
	// so the crash sweep keeps it, and its own records are in the bucket.
	f.db.Set("agents/"+appSlug+"/"+foldRunOne, map[string]any{
		"repo": "acme/app", "workflow": "web", "requestedBy": "dimi@example.invalid",
		"startedAt": f.now.Add(-time.Minute).UnixMilli(), "updatedAt": f.now.Add(-time.Minute).UnixMilli(),
	})
	f.run(foldRunOne, runstore.StatusSucceeded, f.now.Add(-3*time.Hour), foldCost(3.5, 1.0, 0.25, true))

	// The walk in flight: a fresh claim, naming the execution holding it, with
	// the position it has reached.
	at := f.now.UTC()
	f.db.Set(runindex.PathLease, map[string]any{
		"holder":     "fugaro-history-xyz12",
		"at":         at.Format(time.RFC3339Nano),
		"expires_at": at.Add(runindex.LeaseTTL).Format(time.RFC3339Nano),
		"cursor":     runindex.Cursor{Slug: appSlug, RunID: foldRunTwo}.Encode(),
	})

	out, stderr, err := execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
	// The crash sweep ran: the dead run's registry entry is gone.
	if got := f.db.Value("agents/" + appSlug + "/" + crashed); got != nil {
		t.Fatalf("a reindex stopped the crash sweep: %v", got)
	}
	if !strings.Contains(out, "sweep: 1 removed") {
		t.Fatalf("the sweep did not remove the crashed run: %q", out)
	}
	// The fold did not: the summary and the entries are exactly as they were,
	// and the sweep says who holds the lease.
	if !strings.Contains(out, "index: not folded: fugaro-history-xyz12 holds the writer lease") {
		t.Fatalf("the sweep does not say the fold was skipped: %q", out)
	}
	if _, ok := f.fs.Value(runindex.EntriesCollection, foldRunOne); ok {
		t.Fatal("the sweep folded a run while a reindex held the lease")
	}
	if _, ok := f.fs.Value(runindex.SummaryCollection, runindex.SummaryDocument); ok {
		t.Fatal("the sweep wrote the summary while a reindex held the lease")
	}
	if _, ok := f.fs.Value(runindex.EntriesCollection, foldRunTwo); ok {
		t.Fatal("the sweep walked the cursor a reindex holds")
	}
	// The claim is left exactly as it stands: the execution walking it keeps
	// the lease it is using.
	if got := f.db.Value(runindex.PathLease); got == nil {
		t.Fatal("the sweep freed the lease a reindex holds")
	}
}
