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

// TestReindexFailedPassKeepsCursor is the walk's resumability across a failure
// (F39): a pass over a bucket of a million runs meets a refused read or write
// somewhere, and one that answers by releasing the lease would take the
// position with it -- the lease node IS the cursor -- and the next pass would
// start again from the top of history for ever. What survives is the last run
// the pass upserted, which is the run the failing one comes after.
func TestReindexFailedPassKeepsCursor(t *testing.T) {
	e := newFoldEnv(t)
	// Two runs to walk, newest first: runThree, and the ghost between them is a
	// run directory with no records under it, which the walk reaches and reads
	// nothing from.
	e.objects(foldSlug, foldSpec(runTwo), foldLaunch(runTwo, foldNow.Add(-2*time.Hour)),
		foldRecord(runTwo, runstore.StatusFailed, foldNow.Add(-2*time.Hour), foldCost(1.0, 0.0, 0.0, false)))
	e.bucketWrite(t, foldSlug, runGhost, "note.txt")
	e.objects(foldSlug, foldSpec(runThree), foldLaunch(runThree, foldNow.Add(-4*time.Hour)),
		foldRecord(runThree, runstore.StatusSucceeded, foldNow.Add(-4*time.Hour), foldCost(2.0, 0.0, 0.0, false)))

	// A pass cut off at its newest run: the cursor the failing pass inherits.
	if _, err := e.walk(e.now); err != nil {
		t.Fatal(err)
	}
	if got := e.lease()["cursor"]; got != (&Cursor{Slug: foldSlug, RunID: runTwo}).Encode() {
		t.Fatalf("the fixture left no cursor at runTwo: %v", got)
	}

	// The write of the run after the ghost is refused: the pass reaches the
	// ghost, reaches runThree, and fails there.
	e.fs.DenyNext(1)
	rep, err := e.walk(time.Time{})
	if err == nil {
		t.Fatal("a refused write did not fail the pass")
	}
	if rep.Walked != 0 || rep.Unreadable != 1 {
		t.Fatalf("report = %+v", rep)
	}
	l := e.lease()
	if l == nil {
		t.Fatal("a failed pass freed the lease and took the walk's position with it")
	}
	want := (&Cursor{Slug: foldSlug, RunID: runGhost}).Encode()
	if l["cursor"] != want {
		t.Fatalf("cursor = %v, want the last run the pass reached (%v)", l["cursor"], want)
	}
	if done, ok := e.summary()["complete"].(bool); !ok || done {
		t.Fatalf("a failed pass marked the index complete: %v", e.summary())
	}

	// The pass behind it carries on from there and reaches the end: the run the
	// refused write skipped is the one it writes.
	rep, err = e.walk(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Done || rep.From == nil || rep.From.RunID != runGhost {
		t.Fatalf("the pass after the failure did not resume from the position: %+v", rep)
	}
	if ids := e.fs.IDs(EntriesCollection); len(ids) != 2 {
		t.Fatalf("the resumed pass wrote %v", ids)
	}
	if l := e.lease(); l != nil {
		t.Fatalf("the walk left its lease behind: %v", l)
	}
}

// bucketWrite puts one object under a run's directory without writing any of
// the run's own records: the shape a run leaves when its objects are gone and
// something else under its prefix is not.
func (e *foldEnv) bucketWrite(t *testing.T, slug, run, name string) {
	t.Helper()
	if err := e.bucket.WriteAll(context.Background(), "runs/"+slug+"/"+run+"/"+name, []byte("x"), nil); err != nil {
		t.Fatal(err)
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

// TestReindexResetIsWhatUpdateAndRunsReindexDo is the reset both `fugaro
// update` and `fugaro runs reindex` run before they start anything (RI5, U3):
// the cursor goes, the summary is latched incomplete, and the walk every later
// sweep continues begins at the top -- including on an installation whose index
// has never been folded, where there is no summary to latch and the reset seeds
// one, because a reset is the command that says an index should exist.
func TestReindexResetIsWhatUpdateAndRunsReindexDo(t *testing.T) {
	e := newFoldEnv(t)
	e.reindexTwoRuns()
	// A walk cut off at its first run: a cursor is there to be cleared.
	if _, err := e.walk(e.now); err != nil {
		t.Fatal(err)
	}
	if e.lease()["cursor"] == nil {
		t.Fatalf("the fixture left no cursor: %v", e.lease())
	}
	f := e.index()
	live, err := ResetReindex(context.Background(), f.DB, f.FS, "fugaro-cli", e.now)
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("a lapsed walk was reported as a live one")
	}
	l := e.lease()
	if l["cursor"] != nil {
		t.Fatalf("the reset left the cursor behind: %v", l)
	}
	if l["holder"] != "fugaro-cli" {
		t.Fatalf("the reset's node names nobody: %v", l)
	}
	if done, ok := e.summary()["complete"].(bool); !ok || done {
		t.Fatalf("the reset left the index complete: %v", e.summary())
	}
	// The sweep behind it walks from the top rather than resuming.
	rep, err := e.sweep(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walk == nil || rep.Walk.From != nil || !rep.Walk.Done || rep.Walk.Walked != 2 {
		t.Fatalf("the sweep did not walk from the top: %+v", rep)
	}

	// An installation whose index has never been folded: no summary at all,
	// and the reset is what makes the walk owed rather than the week a fold
	// would reach back.
	fresh := newFoldEnv(t)
	fresh.reindexTwoRuns()
	ff := fresh.index()
	live, err = ResetReindex(context.Background(), ff.DB, ff.FS, "fugaro-cli", fresh.now)
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("an installation with no lease at all was reported as walked")
	}
	sum := fresh.summary()
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("the reset seeded no incomplete summary: %v", sum)
	}
	rep, err = fresh.sweep(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Walk == nil || !rep.Walk.Done || rep.Walk.Walked != 2 {
		t.Fatalf("the seeded reset did not walk: %+v", rep)
	}

	// A walk that IS going: its claim is left exactly as it stands, so the
	// execution walking keeps the lease it is using, and the index is reported
	// as owed a walk.
	going := newFoldEnv(t)
	gf := going.index()
	claim := going.claim(t, gf, "exec-walking")
	live, err = ResetReindex(context.Background(), gf.DB, gf.FS, "fugaro-cli", going.now)
	if err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("a fresh claim was not reported as a walk in flight")
	}
	l = going.lease()
	if l["holder"] != "exec-walking" || l["at"] != claim.At.Format(time.RFC3339Nano) {
		t.Fatalf("the reset stamped over a running writer's claim: %v", l)
	}
	if done, ok := going.summary()["complete"].(bool); !ok || done {
		t.Fatalf("the reset left an index with a walk going complete: %v", going.summary())
	}

	// A fresh claim that is NOT a walk's: the sweep's own fold holds this lease
	// while it folds, and a dead fold's claim stands for the whole TTL. A
	// rebuild asked for must not be turned away by one -- the mark is what makes
	// the next sweep walk -- and the fold's claim is still left alone.
	folded := newFoldEnv(t)
	ffold := folded.index()
	foldClaim := folded.claim(t, ffold, "exec-folding")
	folded.fs.Set(SummaryCollection, SummaryDocument, map[string]any{
		"version": int64(Version), "complete": true, "folded_through": folded.now, "updated_at": folded.now,
	})
	live, err = ResetReindex(context.Background(), ffold.DB, ffold.FS, "fugaro-cli", folded.now)
	if err != nil {
		t.Fatal(err)
	}
	if live {
		t.Fatal("a fold's fresh claim was reported as a walk in flight, so the rebuild never happens")
	}
	if l = folded.lease(); l["holder"] != "exec-folding" || l["at"] != foldClaim.At.Format(time.RFC3339Nano) {
		t.Fatalf("the reset stamped over a folding writer's claim: %v", l)
	}
	if done, ok := folded.summary()["complete"].(bool); !ok || done {
		t.Fatalf("the reset left the index complete: %v", folded.summary())
	}
}

// claim takes the index's writer lease for holder, as a writer does, and
// answers the claim it landed.
func (e *foldEnv) claim(t *testing.T, f *Fold, holder string) LeaseDoc {
	t.Helper()
	l, err := NewLease(f.DB, holder, f.DB.ServerNow)
	if err != nil {
		t.Fatal(err)
	}
	claim, held, err := l.Acquire(context.Background())
	if err != nil || !held {
		t.Fatalf("the fixture took no claim: held=%v err=%v", held, err)
	}
	return claim
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
