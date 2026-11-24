package runindex

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

const (
	foldSlug = "acme-widgets"
	foldRepo = "acme/widgets"
	// The run ids are the shape task.NewRunID mints, which is what the
	// entry's own sort key falls back to when a run's records carry no
	// moment.
	runOne = "20261120-093000-a1b2"
	runTwo = "20261120-101500-c3d4"
	// runThree is older than both, and runGhost sits between them: a run
	// directory a walk reaches and reads nothing from (RI5's failure test).
	runThree = "20261120-080000-eeee"
	runGhost = "20261120-090000-ffff"
)

var foldNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// foldEnv is one fold's world: the two fakes, the runs bucket, and the fold's
// own clock.
type foldEnv struct {
	t      *testing.T
	db     *gcpfake.RTDB
	fs     *gcpfake.Firestore
	bucket *blob.Bucket
	now    time.Time
	warns  []string
}

func newFoldEnv(t *testing.T) *foldEnv {
	t.Helper()
	return &foldEnv{t: t, db: gcpfake.NewRTDB(t), fs: gcpfake.NewFirestore(t), bucket: memblob.OpenBucket(nil), now: foldNow}
}

// index is the folder one pass runs as: the lease's holder is the execution's
// name and the wall clock is the fixture's.
func (e *foldEnv) index() *Fold {
	e.t.Helper()
	c, err := rtdb.New(e.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		e.t.Fatal(err)
	}
	fs, err := firestore.New(e.fs.URL, "aurora-fp", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok-SECRET"}))
	if err != nil {
		e.t.Fatal(err)
	}
	return &Fold{DB: c, FS: fs, Bucket: e.bucket, Holder: "exec-1", Now: func() time.Time { return e.now },
		Warn: func(s string) { e.warns = append(e.warns, s) }}
}

// fold runs one fold, as the sweep does.
func (e *foldEnv) fold() (FoldReport, error) {
	e.t.Helper()
	return e.index().Fold(context.Background())
}

// reader is the real Reader over the fixture's two fakes, for a test that reads
// back what a fold wrote.
func (e *foldEnv) reader() Reader {
	e.t.Helper()
	client, err := firestore.New(e.fs.URL, "aurora-fp", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok-SECRET"}))
	if err != nil {
		e.t.Fatal(err)
	}
	live, err := rtdb.New(e.db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		e.t.Fatal(err)
	}
	return NewFirestore(client, live, WithClock(func() time.Time { return e.now }))
}

// agent is a run's registry heartbeat: what a live run writes, and the fold's
// second discovery path.
func (e *foldEnv) agent(slug, run string, at time.Time) {
	e.db.Set("agents/"+slug+"/"+run, map[string]any{"repo": foldRepo, "workflow": "go", "updatedAt": at.UnixMilli()})
}

// outcome is a run's settled outcome under the day it ended on: the fold's
// first discovery path.
func (e *foldEnv) outcome(slug, run string, day int64, status string) {
	e.db.Set("outcomes/"+budget.DayKey(day)+"/"+slug+"/"+run,
		map[string]any{"status": status, "requestedBy": "dev@example.invalid"})
}

// record rewrites a run's result.json: a run whose record changes is a run the
// fold reads again.
func (e *foldEnv) record(slug string, rec *runstore.Record) {
	e.t.Helper()
	if err := runstore.Open(e.bucket, slug, rec.RunID).WriteRecord(context.Background(), rec); err != nil {
		e.t.Fatal(err)
	}
}

// objects writes a run's own records into the runs bucket.
func (e *foldEnv) objects(slug string, spec *task.Spec, launch *runstore.Launch, rec *runstore.Record) {
	e.t.Helper()
	st := runstore.Open(e.bucket, slug, spec.RunID)
	ctx := context.Background()
	if err := st.WriteTask(ctx, spec); err != nil {
		e.t.Fatal(err)
	}
	if launch != nil {
		if err := st.WriteLaunch(ctx, launch); err != nil {
			e.t.Fatal(err)
		}
	}
	if rec != nil {
		if err := st.WriteRecord(ctx, rec); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *foldEnv) doc(run string) map[string]any {
	e.t.Helper()
	m, ok := e.fs.Value(EntriesCollection, run)
	if !ok {
		e.t.Fatalf("the index holds no document for %s", run)
	}
	return m
}

func (e *foldEnv) summary() map[string]any {
	e.t.Helper()
	m, ok := e.fs.Value(SummaryCollection, SummaryDocument)
	if !ok {
		e.t.Fatal("the index holds no summary")
	}
	return m
}

// keys lists the runs bucket's object names, which is how a fold that wrote a
// run object would be caught.
func (e *foldEnv) keys() []string {
	e.t.Helper()
	var out []string
	iter := e.bucket.List(nil)
	for {
		obj, err := iter.Next(context.Background())
		if err != nil {
			break
		}
		out = append(out, obj.Key)
	}
	sort.Strings(out)
	return out
}

func int64Of(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("%s is absent from %v", key, m)
	}
	i, ok := v.(int64)
	if !ok {
		t.Fatalf("%s = %#v, want an integer", key, v)
	}
	return i
}

func foldSpec(run string) *task.Spec {
	return &task.Spec{
		Version: 1, RunID: run, Repo: foldRepo, Ref: "main", Workflow: "go",
		Task: "# Task: the index folds\n\nthe body", Title: "the index folds",
		PlanTask: "console-fixes/RI4", RequestedBy: "dev@example.invalid", Batch: "20261120-093000",
	}
}

func foldLaunch(run string, at time.Time) *runstore.Launch {
	return &runstore.Launch{
		Version: 1, RunID: run, Backend: "cloud-run", Execution: "projects/p/executions/e",
		Job: "fugaro-run-acme-widgets", LaunchedBy: "dev@example.invalid", LaunchedAt: at, PlanTask: "console-fixes/RI4",
	}
}

// foldCost is a mixed run's cost block: money and notional, compute priced, and
// an integer cache total -- the kinds the document has to carry as kinds.
func foldCost(model, notional, compute float64, estimated bool) *runstore.Cost {
	return &runstore.Cost{
		ModelUSD: model, ModelNotionalUSD: notional, ComputeUSD: compute, ComputeEstimated: estimated,
		TotalUSD: model - notional + compute, ModelBasis: runstore.BasisAPIList,
		CacheReadTokens: 1200, CacheWriteTokens: 300,
		ModelTokens: map[string]runstore.ModelTokenCounts{"claude-x": {Input: 10, Output: 5}},
	}
}

func foldRecord(run string, status runstore.Status, started time.Time, cost *runstore.Cost) *runstore.Record {
	fin := started.Add(20 * time.Minute)
	return &runstore.Record{
		Version: 1, RunID: run, Repo: foldRepo, Status: status, Stage: "finalize",
		StartedAt: started, FinishedAt: &fin, Cost: cost,
		Tier:  &runstore.TierRecord{Tier: tier.Medium, ExpectedUSD: 4, ExpectedMinutes: 30},
		Rigor: &runstore.RigorRecord{Mode: rigor.Pro, Level: rigor.Pro, From: rigor.FromRepo, At: runstore.RigorAtFinalize},
	}
}

// TestFoldUpsertsChangedRuns is the fold's own acceptance: the runs that
// changed since the stamp become documents, every field of one taken from the
// run's records, and the document carries the kinds the contract promises --
// a timestamp as a timestamp, an integer as an integer -- because a document
// whose figures are doubles is one no reader can sum and no query can bound.
//
// The second half is what the stamp is for: a fold with nothing new to read
// writes no document and still moves the stamp, so the window it re-reads
// shrinks instead of growing with the index.
func TestFoldUpsertsChangedRuns(t *testing.T) {
	e := newFoldEnv(t)
	started := foldNow.Add(-3 * time.Hour)
	e.agent(foldSlug, runOne, started)
	e.outcome(foldSlug, runOne, budget.Day(started), "succeeded")
	e.objects(foldSlug, foldSpec(runOne), foldLaunch(runOne, started.Add(-time.Minute)),
		foldRecord(runOne, runstore.StatusSucceeded, started, foldCost(3.5, 1.0, 0.25, true)))
	// A run that is in flight: a task and a launch, no record at all.
	e.agent(foldSlug, runTwo, foldNow.Add(-2*time.Minute))
	e.objects(foldSlug, foldSpec(runTwo), foldLaunch(runTwo, foldNow.Add(-3*time.Minute)), nil)

	before := e.keys()
	rep, err := e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Folded != 2 || rep.Unreadable != 0 || rep.Held {
		t.Fatalf("report = %+v", rep)
	}
	if !rep.Through.Equal(foldNow) {
		t.Fatalf("the stamp is %s, want %s", rep.Through, foldNow)
	}
	if len(e.warns) != 0 {
		t.Fatalf("warnings = %v", e.warns)
	}
	if after := e.keys(); len(after) != len(before) {
		t.Fatalf("the fold wrote a run object: %v became %v", before, after)
	}

	one := e.doc(runOne)
	if one["slug"] != foldSlug || one["repo"] != foldRepo || one["run_id"] != runOne {
		t.Fatalf("entry = %v", one)
	}
	if one["status"] != string(runstore.StatusSucceeded) || one["plan_task"] != "console-fixes/RI4" ||
		one["title"] != "the index folds" || one["workflow"] != "go" ||
		one["requested_by"] != "dev@example.invalid" || one["launched_by"] != "dev@example.invalid" ||
		one["batch"] != "20261120-093000" || one["tier"] != string(tier.Medium) || one["rigor"] != string(rigor.Pro) {
		t.Fatalf("entry = %v", one)
	}
	finished, ok := one["updated_at"].(time.Time)
	if !ok || !finished.Equal(started.Add(20*time.Minute)) {
		t.Fatalf("updated_at = %#v, want the record's finish", one["updated_at"])
	}
	// The record and the launch are whole, and the kinds inside them are the
	// service's: this is what makes `record.cost.model_usd` a double a reader
	// can sum and `record.cost.cache_read_tokens` an integer a query can
	// bound.
	rec, ok := one["record"].(map[string]any)
	if !ok {
		t.Fatalf("record = %#v", one["record"])
	}
	if _, ok := one["launch"].(map[string]any); !ok {
		t.Fatalf("launch = %#v", one["launch"])
	}
	if _, ok := rec["started_at"].(time.Time); !ok {
		t.Fatalf("record.started_at = %#v, want a timestamp", rec["started_at"])
	}
	cost, ok := rec["cost"].(map[string]any)
	if !ok {
		t.Fatalf("record.cost = %#v", rec["cost"])
	}
	if v, ok := cost["model_usd"].(float64); !ok || v != 3.5 {
		t.Fatalf("record.cost.model_usd = %#v, want a double", cost["model_usd"])
	}
	if v, ok := cost["cache_read_tokens"].(int64); !ok || v != 1200 {
		t.Fatalf("record.cost.cache_read_tokens = %#v, want an integer", cost["cache_read_tokens"])
	}
	if v, ok := cost["model_tokens"].(map[string]any); !ok {
		t.Fatalf("record.cost.model_tokens = %#v", cost["model_tokens"])
	} else if m, ok := v["claude-x"].(map[string]any); !ok {
		t.Fatalf("record.cost.model_tokens.claude-x = %#v", v["claude-x"])
	} else if n, ok := m["input"].(int64); !ok || n != 10 {
		t.Fatalf("record.cost.model_tokens.claude-x.input = %#v, want an integer", m["input"])
	}

	// The run with no record: no status, no record, and the launch's own
	// moment as the sort key.
	two := e.doc(runTwo)
	if _, ok := two["record"]; ok {
		t.Fatalf("a run that never finalized carries a record: %v", two)
	}
	if _, ok := two["status"]; ok {
		t.Fatalf("a run that never finalized carries a status: %v", two)
	}
	if at, ok := two["updated_at"].(time.Time); !ok || !at.Equal(foldNow.Add(-3*time.Minute)) {
		t.Fatalf("updated_at = %#v, want the launch's moment", two["updated_at"])
	}

	// The summary: two runs, one settled state, the model figure split in
	// micro-dollars, and the compute figure with its own counts.
	sum := e.summary()
	if int64Of(t, sum, "runs") != 2 {
		t.Fatalf("summary = %v", sum)
	}
	if int64Of(t, sum, "spent_micros") != 2_500_000 || int64Of(t, sum, "notional_micros") != 1_000_000 {
		t.Fatalf("summary = %v", sum)
	}
	if int64Of(t, sum, "compute_micros") != 250_000 || int64Of(t, sum, "compute_runs") != 1 ||
		int64Of(t, sum, "compute_not_estimated") != 1 {
		t.Fatalf("summary = %v", sum)
	}
	states, ok := sum["states"].(map[string]any)
	if !ok {
		t.Fatalf("states = %#v", sum["states"])
	}
	for _, s := range runstore.Statuses {
		if _, ok := states[string(s)]; !ok {
			t.Fatalf("states carries no %s: %v", s, states)
		}
	}
	if int64Of(t, states, "succeeded") != 1 {
		t.Fatalf("states = %v", states)
	}
	if stamp, ok := sum["folded_through"].(time.Time); !ok || !stamp.Equal(foldNow) {
		t.Fatalf("folded_through = %#v", sum["folded_through"])
	}

	// The next pass reads the same window again -- a day's outcomes are always
	// inside it, which is what folds a run whose outcome landed after the
	// stamp -- and the run it folds again moves nothing: the entry is what it
	// was and the summary's figures are where they were.
	e.now = foldNow.Add(15 * time.Minute)
	entryBefore := e.doc(runOne)
	rep, err = e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Held {
		t.Fatalf("report = %+v", rep)
	}
	if after := e.doc(runOne); !reflect.DeepEqual(entryBefore, after) {
		t.Fatalf("a re-fold changed the entry:\n%v\n%v", entryBefore, after)
	}
	sum = e.summary()
	if int64Of(t, sum, "runs") != 2 || int64Of(t, sum, "spent_micros") != 2_500_000 ||
		int64Of(t, sum, "compute_runs") != 1 {
		t.Fatalf("a re-fold moved the summary: %v", sum)
	}
	if stamp, ok := sum["folded_through"].(time.Time); !ok || !stamp.Equal(e.now) {
		t.Fatalf("the stamp did not advance: %#v", sum["folded_through"])
	}
}

// TestFoldStampsAClaimedNeverLaunchedRunWithItsMintTime is the sort key's last
// fallback, the one case where the entry takes no moment from a record at all:
// a run a launcher claimed and no execution ever picked up, so the bucket holds
// a task.json and nothing else. The run's own id names the moment it was minted
// (`runstore.RunTime`), and the entry is stamped with it -- an entry with no
// sort key falls outside every `--since` window, and `runs stats`'s
// `unlaunched`, the window's count of runs that never started, is counted off a
// page of entries, so such a run would be on none of them and the figure would
// be zero while the record read counts them (contract-index.md, the
// `updated_at` row).
//
// The fold is handed the run the way the reindex walk hands it one (RI5); what
// the test pins is the document that comes out of it, and that the reader
// places that document in the window the run was minted in and in no later one.
func TestFoldStampsAClaimedNeverLaunchedRunWithItsMintTime(t *testing.T) {
	e := newFoldEnv(t)
	run := "20261120-110000-e5f6"
	minted, err := runstore.RunTime(run)
	if err != nil {
		t.Fatal(err)
	}
	e.objects(foldSlug, foldSpec(run), nil, nil)
	f := e.index()
	entry, err := f.read(context.Background(), runKey{slug: foldSlug, run: run})
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("the fold found nothing to write for a run whose task.json is there: the run exists")
	}
	if entry.Status != "" || entry.Launch != nil || entry.Record != nil {
		t.Fatalf("entry = %+v, want a run that launched nothing and settled nothing", entry)
	}
	if !entry.UpdatedAt.Equal(minted) {
		t.Fatalf("updated_at = %s, want the run id's mint time %s: a run that never launched has no record and no launch to take a moment from",
			entry.UpdatedAt, minted)
	}
	if err := f.write(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	// The read end: the entry is placed by that key, so the window it was
	// minted in carries it and a window that opens after it does not.
	r := e.reader()
	inside, err := r.List(context.Background(), Filter{Slugs: []string{foldSlug}, Since: minted.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(inside); !reflect.DeepEqual(got, []string{run}) {
		t.Fatalf("the window that opens before the run was minted lists %v, want the run alone: it is on no other read's page either", got)
	}
	if row := rowOf(t, inside, run); row.Status != "" || !row.UpdatedAt.Equal(minted) {
		t.Fatalf("the row = %+v, want the run settled nothing and placed by its mint time", row)
	}
	after, err := r.List(context.Background(), Filter{Slugs: []string{foldSlug}, Since: minted.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(after); len(got) != 0 {
		t.Fatalf("a window that opens after the run was minted lists %v, want nothing", got)
	}
}

// TestFoldAppliesCountDeltas is the summary's own rule: a fold moves the
// counts and the money by the run's own state change, so a run that ends
// differently is counted once and not twice, and a run whose compute stops
// being known leaves the summary with no compute figure rather than with a
// stale one.
func TestFoldAppliesCountDeltas(t *testing.T) {
	e := newFoldEnv(t)
	started := foldNow.Add(-3 * time.Hour)
	e.agent(foldSlug, runOne, started)
	e.objects(foldSlug, foldSpec(runOne), foldLaunch(runOne, started.Add(-time.Minute)),
		foldRecord(runOne, runstore.StatusSucceeded, started, foldCost(3.5, 1.0, 0.25, true)))
	if _, err := e.fold(); err != nil {
		t.Fatal(err)
	}

	// The same run ends differently: failed, dearer, and its compute is no
	// longer priced.
	e.now = foldNow.Add(30 * time.Minute)
	started = e.now.Add(-time.Hour)
	e.agent(foldSlug, runOne, e.now)
	e.record(foldSlug, foldRecord(runOne, runstore.StatusFailed, started, foldCost(5.0, 0.0, 0, false)))
	rep, err := e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Folded != 1 {
		t.Fatalf("report = %+v", rep)
	}
	sum := e.summary()
	if int64Of(t, sum, "runs") != 1 {
		t.Fatalf("the run was counted twice: %v", sum)
	}
	if int64Of(t, sum, "spent_micros") != 5_000_000 || int64Of(t, sum, "notional_micros") != 0 {
		t.Fatalf("summary = %v", sum)
	}
	if _, ok := sum["compute_micros"]; ok {
		t.Fatalf("a summary whose runs price no compute carries one: %v", sum)
	}
	if int64Of(t, sum, "compute_runs") != 0 || int64Of(t, sum, "compute_not_estimated") != 1 {
		t.Fatalf("summary = %v", sum)
	}
	states := sum["states"].(map[string]any)
	if int64Of(t, states, "failed") != 1 || int64Of(t, states, "succeeded") != 0 {
		t.Fatalf("states = %v", states)
	}

	// A second run arrives: the counts add up rather than replace.
	e.now = foldNow.Add(45 * time.Minute)
	started = e.now.Add(-time.Hour)
	e.agent(foldSlug, runTwo, e.now)
	e.objects(foldSlug, foldSpec(runTwo), foldLaunch(runTwo, started.Add(-time.Minute)),
		foldRecord(runTwo, runstore.StatusSucceeded, started, foldCost(1.0, 0.0, 0.5, true)))
	if _, err := e.fold(); err != nil {
		t.Fatal(err)
	}
	sum = e.summary()
	if int64Of(t, sum, "runs") != 2 || int64Of(t, sum, "spent_micros") != 6_000_000 ||
		int64Of(t, sum, "compute_micros") != 500_000 || int64Of(t, sum, "compute_runs") != 1 {
		t.Fatalf("summary = %v", sum)
	}
	states = sum["states"].(map[string]any)
	if int64Of(t, states, "failed") != 1 || int64Of(t, states, "succeeded") != 1 {
		t.Fatalf("states = %v", states)
	}
}

// TestFoldLeaseRefusesSecondWriter is the one-writer rule the summary's deltas
// depend on: a fold that finds another writer's fresh claim reads nothing,
// writes nothing and says who holds it, and a claim that has lapsed is taken
// over rather than left to stop the index for ever.
func TestFoldLeaseRefusesSecondWriter(t *testing.T) {
	e := newFoldEnv(t)
	started := foldNow.Add(-time.Hour)
	e.agent(foldSlug, runOne, started)
	e.outcome(foldSlug, runOne, budget.Day(started), "succeeded")
	e.objects(foldSlug, foldSpec(runOne), foldLaunch(runOne, started.Add(-time.Minute)),
		foldRecord(runOne, runstore.StatusSucceeded, started, foldCost(1.0, 0.0, 0.1, true)))
	e.db.Set(PathLease, map[string]any{
		"holder":     "exec-2",
		"at":         time.Now().UTC().Format(time.RFC3339Nano),
		"expires_at": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano),
	})

	rep, err := e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Held || rep.Holder != "exec-2" {
		t.Fatalf("report = %+v", rep)
	}
	if ids := e.fs.IDs(EntriesCollection); len(ids) != 0 {
		t.Fatalf("a fold that does not hold the lease wrote %v", ids)
	}
	if _, ok := e.fs.Value(SummaryCollection, SummaryDocument); ok {
		t.Fatal("a fold that does not hold the lease wrote the summary")
	}

	// The claim lapses: the fold takes it over, folds, and frees it.
	e.db.Set(PathLease, map[string]any{
		"holder":     "exec-2",
		"at":         time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano),
		"expires_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano),
	})
	rep, err = e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Held || rep.Folded != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if e.db.Value(PathLease) != nil {
		t.Fatalf("the fold left its lease behind: %v", e.db.Value(PathLease))
	}
}

// TestFoldLeavesTheStampOnFailure is the failure rule: a fold that cannot
// write its summary leaves the stamp where it was, so the next sweep reads the
// same window rather than stepping over the runs this one could not fold.
func TestFoldLeavesTheStampOnFailure(t *testing.T) {
	e := newFoldEnv(t)
	started := foldNow.Add(-time.Hour)
	e.agent(foldSlug, runOne, started)
	e.objects(foldSlug, foldSpec(runOne), foldLaunch(runOne, started.Add(-time.Minute)),
		foldRecord(runOne, runstore.StatusSucceeded, started, foldCost(1.0, 0.0, 0.1, true)))
	if _, err := e.fold(); err != nil {
		t.Fatal(err)
	}
	stamp := e.summary()["folded_through"]

	// The database refuses the writes of the next pass.
	e.now = foldNow.Add(15 * time.Minute)
	e.agent(foldSlug, runTwo, e.now)
	e.objects(foldSlug, foldSpec(runTwo), foldLaunch(runTwo, e.now.Add(-time.Minute)),
		foldRecord(runTwo, runstore.StatusSucceeded, e.now.Add(-time.Minute), foldCost(1.0, 0.0, 0.1, true)))
	e.fs.DenyNext(1)
	if _, err := e.fold(); err == nil {
		t.Fatal("the fold reported success while the database refused its writes")
	}
	if got := e.summary()["folded_through"]; got != stamp {
		t.Fatalf("a failed fold moved the stamp: %v became %v", stamp, got)
	}
}

// TestFoldProjectsTheTaskBranch is run 1a21's deferred review finding, on the
// writing side. indexRun rebuilt the coordinator view from the entry's two
// records alone, so a run whose record is absent or unreadable lost the branch
// coordinatorRun falls back to the task for. The fold is where that fallback is
// preserved: the entry projects the branch runview.Join joins (the record's,
// else the task's), so a reader of the index draws the same branch the bucket
// read draws. A record-less run is the case that makes the projection
// load-bearing, and the one asserted here.
func TestFoldProjectsTheTaskBranch(t *testing.T) {
	e := newFoldEnv(t)
	branch := "fugaro/" + runTwo
	spec := &task.Spec{
		Version: 1, RunID: runTwo, Repo: foldRepo, Ref: "main", Workflow: "go",
		Task: "# Task: a run in flight\n\nthe body", Title: "a run in flight",
		Branch: branch, PR: 41, PreviousRun: runOne,
	}
	// A task and a launch, no record at all: the state whose branch nothing
	// else in the entry can answer for.
	e.agent(foldSlug, runTwo, foldNow.Add(-2*time.Minute))
	e.objects(foldSlug, spec, foldLaunch(runTwo, foldNow.Add(-3*time.Minute)), nil)

	if _, err := e.fold(); err != nil {
		t.Fatal(err)
	}
	two := e.doc(runTwo)
	if got := two["branch"]; got != branch {
		t.Fatalf("branch = %#v, want the task's %s", got, branch)
	}
	if _, ok := two["record"]; ok {
		t.Fatalf("a run that never finalized carries a record: %v", two)
	}
}
