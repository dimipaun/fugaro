package runindex

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// walk runs one reindex pass, as the job's --reindex mode does. A zero limit
// is the pass the job's own timeout bounds; a limit makes it stop where a
// fixture wants it to.
func (e *foldEnv) walk(limit time.Time) (WalkReport, error) {
	e.t.Helper()
	f := e.index()
	f.StartLimit = limit
	return f.Walk(context.Background())
}

// sweep runs one history sweep's index half: the fold, or the walk an
// unfinished reindex is owed.
func (e *foldEnv) sweep(limit time.Time) (SweepReport, error) {
	e.t.Helper()
	f := e.index()
	f.StartLimit = limit
	return f.Sweep(context.Background())
}

// snapshot is every document the index holds, entries and summary, which is
// what "a second pass leaves identical documents" is asked of.
func (e *foldEnv) snapshot() map[string]map[string]any {
	e.t.Helper()
	out := map[string]map[string]any{}
	for _, id := range e.fs.IDs(EntriesCollection) {
		m, _ := e.fs.Value(EntriesCollection, id)
		out[EntriesCollection+"/"+id] = m
	}
	if m, ok := e.fs.Value(SummaryCollection, SummaryDocument); ok {
		out[SummaryCollection+"/"+SummaryDocument] = m
	}
	return out
}

// lease is the lease node as it stands, and nil when nothing holds it.
func (e *foldEnv) lease() map[string]any {
	e.t.Helper()
	v := e.db.Value(PathLease)
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		e.t.Fatalf("lease = %#v", v)
	}
	return m
}

// reindexTwoRuns fills the bucket with one repository's two runs: runTwo is
// newer than runOne, so the walk meets runTwo first.
func (e *foldEnv) reindexTwoRuns() {
	e.t.Helper()
	one := foldNow.Add(-3 * time.Hour)
	e.objects(foldSlug, foldSpec(runOne), foldLaunch(runOne, one.Add(-time.Minute)),
		foldRecord(runOne, runstore.StatusSucceeded, one, foldCost(3.5, 1.0, 0.25, true)))
	two := foldNow.Add(-2 * time.Hour)
	e.objects(foldSlug, foldSpec(runTwo), foldLaunch(runTwo, two.Add(-time.Minute)),
		foldRecord(runTwo, runstore.StatusFailed, two, foldCost(1.0, 0.0, 0.0, false)))
}

// TestReindexIdempotent is the walk's own acceptance (RI5): a pass over the
// whole bucket upserts every run by run id, recomputes the summary from the
// documents and marks the index complete, and a second pass over the same
// bucket leaves every document exactly as it was -- the walk is a statement
// about the bucket, so running it twice says the same thing twice.
func TestReindexIdempotent(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()

	rep, err := e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Held || !rep.Done || rep.Walked != 2 || rep.Unreadable != 0 || rep.Cursor != nil || rep.From != nil {
		t.Fatalf("report = %+v", rep)
	}
	if len(e.warns) != 0 {
		t.Fatalf("warnings = %v", e.warns)
	}
	sum := e.summary()
	if done, ok := sum["complete"].(bool); !ok || !done {
		t.Fatalf("the walk did not mark the index complete: %v", sum)
	}
	if int64Of(t, sum, "runs") != 2 {
		t.Fatalf("summary = %v", sum)
	}

	before := e.snapshot()
	rep, err = e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Done || rep.Walked != 2 {
		t.Fatalf("report = %+v", rep)
	}
	after := e.snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a second pass moved the documents:\n%v\n%v", before, after)
	}
	// The lease is freed, not left behind: a walk that ends holds nothing.
	if l := e.lease(); l != nil {
		t.Fatalf("the walk left its lease behind: %v", l)
	}
}

// TestReindexRecomputesCounts is why the walk exists (F13, D3215): the summary
// is a sum over the entries, so a walk reaching the end of the bucket writes
// the counts the documents themselves give -- not the counts a fold's deltas
// had drifted to, and not the counts the summary carried before the walk.
func TestReindexRecomputesCounts(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()
	// A summary a fold got wrong -- the state a reindex is the repair for.
	e.fs.Set(SummaryCollection, SummaryDocument, map[string]any{
		"version": int64(Version), "complete": false,
		"runs": int64(99), "spent_micros": int64(1), "notional_micros": int64(1),
		"compute_runs": int64(7), "compute_not_estimated": int64(7),
	})

	rep, err := e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Done {
		t.Fatalf("report = %+v", rep)
	}
	sum := e.summary()
	if int64Of(t, sum, "runs") != 2 {
		t.Fatalf("the walk kept the summary's own count: %v", sum)
	}
	if int64Of(t, sum, "spent_micros") != 3_500_000 || int64Of(t, sum, "notional_micros") != 1_000_000 {
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
	if int64Of(t, states, "succeeded") != 1 || int64Of(t, states, "failed") != 1 {
		t.Fatalf("states = %v", states)
	}
	for _, s := range runstore.Statuses {
		if _, ok := states[string(s)]; !ok {
			t.Fatalf("states carries no %s: %v", s, states)
		}
	}
}

// TestReindexStampsWalkStart is the stamp's rule (RI5): the summary carries the
// moment the WALK began, not the moment the pass that finished it ran, so a
// fold after the walk reads every run that changed while the walk was going.
// A stamp taken at the last pass would step over exactly those runs, and
// nothing would ever fold them.
func TestReindexStampsWalkStart(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()
	start := e.now

	// A first pass cut off at its first run: the walk is begun, and the stamp
	// is where it began.
	if _, err := e.walk(e.now); err != nil {
		t.Fatal(err)
	}
	sum := e.summary()
	if at, ok := sum["folded_through"].(time.Time); !ok || !at.Equal(start) {
		t.Fatalf("folded_through = %#v, want the walk's start", sum["folded_through"])
	}
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("a walk that has not finished marked the index complete: %v", sum)
	}

	// A run changes while the walk is going, and the walk finishes much later.
	mid := start.Add(10 * time.Minute)
	e.now = start.Add(time.Hour)
	e.agent(foldSlug, runOne, mid)
	if _, err := e.walk(time.Time{}); err != nil {
		t.Fatal(err)
	}
	sum = e.summary()
	if at, ok := sum["folded_through"].(time.Time); !ok || !at.Equal(start) {
		t.Fatalf("a later pass moved the walk's stamp: %#v, want %s", sum["folded_through"], start)
	}
	if done, ok := sum["complete"].(bool); !ok || !done {
		t.Fatalf("the walk did not mark the index complete: %v", sum)
	}

	// The run that changed mid-walk is folded by the next sweep: it is after
	// the stamp, which is the whole point of stamping the walk's start.
	e.now = start.Add(2 * time.Hour)
	rec := foldRecord(runOne, runstore.StatusSucceeded, start.Add(90*time.Minute), foldCost(9.0, 0.0, 0.0, false))
	e.record(foldSlug, rec)
	rep, err := e.fold()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Folded != 1 {
		t.Fatalf("the run that changed during the walk was not folded: %+v", rep)
	}
	if int64Of(t, e.summary(), "spent_micros") != 9_000_000+1_000_000 {
		t.Fatalf("summary = %v", e.summary())
	}
}

// TestReindexResumesFromCursor is the walk's resumability (F39): a pass that
// reaches its bound stops at the run it is on and leaves its position in the
// lease, so the next pass carries on from there instead of starting over --
// and no second execution is started to do it (F9).
func TestReindexResumesFromCursor(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()

	rep, err := e.walk(e.now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done || rep.Walked != 1 || rep.Cursor == nil {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Cursor.Slug != foldSlug || rep.Cursor.RunID != runTwo {
		t.Fatalf("cursor = %+v, want the newest run", rep.Cursor)
	}
	if ids := e.fs.IDs(EntriesCollection); len(ids) != 1 {
		t.Fatalf("a bounded pass wrote %v", ids)
	}
	l := e.lease()
	if l == nil {
		t.Fatal("a bounded pass freed the lease with the walk unfinished")
	}
	if l["cursor"] != rep.Cursor.Encode() {
		t.Fatalf("lease = %v, want the cursor in it", l)
	}
	// The lease carries an expiry in the past: no execution holds it, so the
	// next sweep takes it and continues rather than waiting out a TTL.
	expires, ok := l["expires_at"].(string)
	if !ok {
		t.Fatalf("lease = %v", l)
	}
	at, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Before(time.Now()) {
		t.Fatalf("the stopped lease is still fresh: %v", l)
	}
	if done, ok := e.summary()["complete"].(bool); !ok || done {
		t.Fatalf("an unfinished walk marked the index complete: %v", e.summary())
	}

	// The next pass continues from the cursor and finishes.
	rep, err = e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Done || rep.Walked != 1 || rep.From == nil || rep.From.RunID != runTwo {
		t.Fatalf("report = %+v", rep)
	}
	if ids := e.fs.IDs(EntriesCollection); len(ids) != 2 {
		t.Fatalf("the resumed pass wrote %v", ids)
	}
	sum := e.summary()
	if int64Of(t, sum, "runs") != 2 {
		t.Fatalf("summary = %v", sum)
	}
	if done, ok := sum["complete"].(bool); !ok || !done {
		t.Fatalf("the resumed walk did not mark the index complete: %v", sum)
	}
	if l := e.lease(); l != nil {
		t.Fatalf("the walk left its lease behind: %v", l)
	}
}

// TestSweepContinuesWalk is what makes the walk resumable without a second
// execution (F9, F39): the history job's own sweep, with no flag and no
// override, carries an unfinished walk further -- from the cursor the lease
// left, and from the top when a pass died before it could leave one, which the
// summary's incompleteness is there to remember.
func TestSweepContinuesWalk(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()

	// A pass cut off at its first run.
	if _, err := e.walk(e.now); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(15 * time.Minute)
	rep, err := e.sweep(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walk == nil || !rep.Walk.Done || rep.Walk.From == nil {
		t.Fatalf("the sweep did not continue the walk: %+v", rep)
	}
	if rep.Held || rep.Folded != 0 {
		t.Fatalf("a sweep that walked reported a fold: %+v", rep)
	}
	if int64Of(t, e.summary(), "runs") != 2 {
		t.Fatalf("summary = %v", e.summary())
	}

	// A pass that died before it could leave a cursor: the summary's
	// incompleteness is the marker, and the next sweep walks from the top.
	e.fs.Set(SummaryCollection, SummaryDocument, map[string]any{
		"version": int64(Version), "complete": false,
		"folded_through": e.now, "runs": int64(2),
	})
	rep, err = e.sweep(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walk == nil || rep.Walk.From != nil || !rep.Walk.Done || rep.Walk.Walked != 2 {
		t.Fatalf("the sweep did not restart the walk: %+v", rep)
	}

	// A complete index with nothing changed is a fold, not a walk.
	rep, err = e.sweep(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walk != nil || rep.Held {
		t.Fatalf("a complete index was walked: %+v", rep)
	}
}

// TestDeadLeaseLapses is the expiry's rule: a walk whose execution died leaves
// a claim no one will ever release, and the next pass must take it over -- from
// the position it left, which is the part of the walk that is not lost.
func TestDeadLeaseLapses(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()
	// A walk cut off at its first run, whose execution then died: the claim is
	// still there and still names its holder, and its expiry is long past.
	if _, err := e.walk(e.now); err != nil {
		t.Fatal(err)
	}
	cursor := e.lease()["cursor"]
	e.db.Set(PathLease, map[string]any{
		"holder":     "exec-dead",
		"at":         time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano),
		"expires_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano),
		"cursor":     cursor,
	})

	rep, err := e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Held {
		t.Fatalf("a lapsed claim stopped the walk: %+v", rep)
	}
	if rep.From == nil || string(rep.From.Encode()) != cursor {
		t.Fatalf("the walk did not resume the dead one's position: %+v", rep)
	}
	if !rep.Done {
		t.Fatalf("report = %+v", rep)
	}
	if int64Of(t, e.summary(), "runs") != 2 {
		t.Fatalf("summary = %v", e.summary())
	}
	if l := e.lease(); l != nil {
		t.Fatalf("the walk left its lease behind: %v", l)
	}
}
