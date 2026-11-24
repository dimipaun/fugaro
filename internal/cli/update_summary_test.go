package cli

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_summary_test.go is RL4's backfill step as the owner meets it (D2966,
// docs/runs-ls-speed/implementation-plan.md): an installation at the release
// that predates the run summary holds runs and no summary beside them, and ONE
// `fugaro update` — the HARD RULE's whole upgrade — writes the summary each
// run's own objects project to, so `runs ls` reads two objects a row instead
// of three.
//
// What the suite holds is the whole of the claim: every run whose record is
// there converges in one update to a summary that is the projection of that
// run's own task and record (the same object the runner writes), the rows a
// reader gets afterwards are the rows it got before, a second update writes
// nothing and says nothing, a run in flight is neither waited for nor touched —
// it writes its own summary at its next stage end — and no door but `fugaro
// update` writes a summary.

// summaryRuns are the runs the fixture's installation holds: the two shapes the
// backfill writes a summary for, and the two it must leave alone.
type summaryRuns struct {
	// a and b are finished runs of the window, each with a task and a record.
	a, b string
	// plain never launched: a task and no record, so there is nothing for a
	// summary to stand in for.
	plain string
	// running is a run mid-flight: a record that still says running, the shape
	// the walk must skip rather than write an object under.
	running string
}

// summaryIDs names the fixture's four runs, without planting anything.
func summaryIDs() summaryRuns {
	return summaryRuns{
		a:       runIDAt(0, "001000", "aa11"),
		b:       runIDAt(0, "001100", "bb22"),
		plain:   runIDAt(0, "001200", "cc33"),
		running: runIDAt(0, "001300", "dd44"),
	}
}

// summaryRec is a finished run's record: the fields a row is built from set to
// something no zero value would give them, so a summary that dropped one reads
// as a difference.
func summaryRec(id string) *runstore.Record {
	c := runstore.NewCost(1.0, 0.5, runstore.BasisAPIList)
	return &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady,
		Branch: "fugaro/" + id, PushedHead: strings.Repeat("a", 40), CostUSD: 1.0, Cost: &c}
}

// plantSummaryRuns stores the fixture's runs in the installation's runs bucket,
// as the release that predates the summary left them: task.json and result.json,
// and no summary beside them.
func plantSummaryRuns(t *testing.T, b *blob.Bucket) summaryRuns {
	t.Helper()
	ids := summaryIDs()
	plantRun(t, b, appSlug, ids.a, firstRunSpec(ids.a), summaryRec(ids.a))
	plantRun(t, b, appSlug, ids.b, firstRunSpec(ids.b), summaryRec(ids.b))
	plantRun(t, b, appSlug, ids.plain, firstRunSpec(ids.plain), nil)
	plantRun(t, b, appSlug, ids.running, nil, &runstore.Record{Version: 1, RunID: ids.running, Repo: "acme/app", Workflow: "app",
		Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone})
	// The premise, read back through the reader the walk itself uses: the two
	// finished runs have both objects, the recordless one has a task alone, and
	// the run in flight has a record that still says running.
	for _, id := range []string{ids.a, ids.b} {
		if _, err := runstore.Open(b, appSlug, id).ReadRecord(t.Context()); err != nil {
			t.Fatalf("the fixture has no record for %s (%v), so there is nothing to project", id, err)
		}
	}
	if rec, err := runstore.Open(b, appSlug, ids.running).ReadRecord(t.Context()); err != nil || rec.Status != runstore.StatusRunning {
		t.Fatalf("the fixture's run in flight reads %+v (%v), want a record that still says running", rec, err)
	}
	return ids
}

// summaryKeys is the summary objects under one prefix of the bucket, sorted.
func summaryKeys(t *testing.T, b *blob.Bucket, prefix string) []string {
	t.Helper()
	var out []string
	for key := range runsTree(t, b, prefix) {
		if strings.HasSuffix(key, "/"+runstore.SummaryName) {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

// wantRunSummary is the summary the backfill would have written for a run: the
// record's and the task's own projection, through the same NewSummary the runner
// writes with. Its At is zeroed, because the walk's clock is its own and the
// summary names the moment it was written; the store-stamped slug is zeroed too,
// since the wire object carries it and this fixture's projection does not.
func wantRunSummary(t *testing.T, b *blob.Bucket, id string) *runstore.Summary {
	t.Helper()
	s := runstore.Open(b, appSlug, id)
	rec, err := s.ReadRecord(t.Context())
	if err != nil {
		t.Fatalf("reading %s's record: %v", id, err)
	}
	var spec *task.Spec
	if sp, err := s.ReadTask(t.Context()); err == nil {
		spec = sp
	}
	return runstore.NewSummary(rec, spec, time.Time{})
}

// assertRunSummary reads a run's summary back through the reader a listing reads
// it with and holds it to the projection of the run's own objects, field for
// field: this is the "the summary it would have written" the task asks for, and
// a member the projection dropped, renamed or retyped is a difference here.
func assertRunSummary(t *testing.T, b *blob.Bucket, id string) {
	t.Helper()
	got, err := runstore.Open(b, appSlug, id).ReadSummary(t.Context())
	if err != nil {
		t.Fatalf("run %s has no readable summary after the update: %v", id, err)
	}
	want := wantRunSummary(t, b, id)
	got.At = time.Time{}
	want.At = time.Time{}
	got.Slug = ""
	want.Slug = ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run %s's summary is not the projection of its own objects:\n  got  %+v\n  want %+v", id, got, want)
	}
}

// TestUpdateBackfillsRunSummaries is the task's own acceptance: an installation
// at the release that predates the summary — the runs of its repository in the
// bucket, no summary beside them — converges under one `fugaro update` to a
// summary for every run whose own record is there, so `runs ls` reads two
// objects a row instead of three and shows the same rows it showed before.
func TestUpdateBackfillsRunSummaries(t *testing.T) {
	r := prIndexRig(t)
	b := prIndexBucket(t, r)
	ids := plantSummaryRuns(t, b)
	// The premise: an installation at the previous release holds no summary at
	// all.
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
		t.Fatalf("the fixture already holds %v: this test is about the update writing the summaries", keys)
	}
	lsAsViewer(t, r)
	before := lsRunIDs(t, "--since", "0", "--repo", "acme/app")

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// What the run said: the count of summaries it created. A run in flight and
	// a run with no record are the two it must not write one for, so it wrote
	// two.
	if !strings.Contains(out, "run summaries: 2 run(s)") {
		t.Errorf("the update does not say how many runs it summarized:\n%s", out)
	}
	// What it wrote: the summary the run's own objects project to, for the two
	// runs whose records are there, and exactly those two objects.
	assertRunSummary(t, b, ids.a)
	assertRunSummary(t, b, ids.b)
	want := []string{}
	for _, id := range []string{ids.a, ids.b} {
		want = append(want, "runs/"+appSlug+"/"+id+"/"+runstore.SummaryName)
	}
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); !slices.Equal(keys, want) {
		t.Errorf("the update wrote %v, want one summary for each run whose record is there (%v) and no other", keys, want)
	}

	// The acceptance: the runs `runs ls` gives afterwards are the runs it gave
	// before — the summary is read in place of the run's task and record, so a
	// field it dropped or moved would be a row that changed here.
	lsAsViewer(t, r)
	after := lsRunIDs(t, "--since", "0", "--repo", "acme/app")
	if !slices.Equal(before, after) {
		t.Errorf("runs ls gave %v before the update and %v after it: a backfill moves no row", before, after)
	}
}

// TestUpdateSummaryIdempotent: the second `fugaro update` writes nothing and
// says nothing for what already has a summary. It is the owner's own sentence
// about the step ("a second update writes nothing"), and it is the reason the
// walk reads a run's summary before it reads the run's task and record: without
// that, every update of every installation would read the whole window to be
// told, run by run, that the object it would write is already there.
//
// The recipe is the index step's (TestUpdateBackfillIdempotent): the second
// update is given a run that appeared since the first, so that the silence is
// not the silence of a step that never ran — the count line is the one run it
// summarized — and everything else the bucket held is byte for byte what it was.
func TestUpdateSummaryIdempotent(t *testing.T) {
	r := prIndexRig(t)
	b := prIndexBucket(t, r)
	plantSummaryRuns(t, b)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); len(keys) == 0 {
		t.Fatalf("the first update wrote no summary at all, so this test would pass over a step that does nothing")
	}

	// A run that appeared between the two updates, as one of the window's runs
	// does on any installation somebody uses.
	appeared := runIDAt(0, "001400", "ee55")
	plantRun(t, b, appSlug, appeared, firstRunSpec(appeared), summaryRec(appeared))
	appearedKey := "runs/" + appSlug + "/" + appeared + "/" + runstore.SummaryName
	before := runsTree(t, b, "runs/"+appSlug+"/")

	// The ordinary command the owner runs again (its exit code is not this
	// test's subject: the stages leave this checkout's own uncommitted edit for
	// the person, which is what an update says about a checkout somebody has
	// changed).
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "update")
	if !strings.Contains(out, "run summaries: 1 run(s)") {
		t.Fatalf("the second update did not summarize %s, the run that has no summary yet (exit %v, %s): the walk must write what is missing and nothing else\n%s", appeared, err, errOut, out)
	}
	after := runsTree(t, b, "runs/"+appSlug+"/")
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); !slices.Contains(keys, appearedKey) {
		t.Errorf("the second update wrote %v, want %s's summary among them", keys, appearedKey)
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
		if _, ok := before[key]; !ok && key != appearedKey {
			t.Errorf("the second update wrote %s, want only the summary of the run that appeared (%s)", key, appearedKey)
		}
	}
}

// TestUpdateSummaryQuietWhenNothing: an installation with nothing to write hears
// nothing. The step says one line when it created a summary and is silent
// otherwise — a line about zero summaries on every update of every installation
// is noise the update's own account does not need — and the two cases here are
// the shapes of nothing: a repository with no runs at all, and one whose runs
// have no record for a summary to stand in for (a run in flight, and one that
// never launched). The first test in this file pins that a step which DID write
// something says so, so the silence below is never the silence of a step that
// did not run.
func TestUpdateSummaryQuietWhenNothing(t *testing.T) {
	t.Run("no runs at all", func(t *testing.T) {
		r := prIndexRig(t)
		b := prIndexBucket(t, r)
		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})
		if strings.Contains(out, "run summaries") {
			t.Errorf("the update says something about the run summaries over a repository with no runs:\n%s", out)
		}
		if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
			t.Errorf("the update wrote %v with no run to summarize", keys)
		}
	})
	t.Run("no run with a record to project", func(t *testing.T) {
		r := prIndexRig(t)
		b := prIndexBucket(t, r)
		ids := summaryIDs()
		plantRun(t, b, appSlug, ids.plain, firstRunSpec(ids.plain), nil)
		plantRun(t, b, appSlug, ids.running, nil, &runstore.Record{Version: 1, RunID: ids.running, Repo: "acme/app", Workflow: "app",
			Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone})
		// The premise: the walk had runs to read. A recordless run and a run in
		// flight are the two shapes it finds nothing to write about, and a walk
		// that found neither would be silent for the wrong reason.
		if _, err := runstore.Open(b, appSlug, ids.plain).ReadTask(t.Context()); err != nil {
			t.Fatalf("the fixture's recordless run has no task either (%v)", err)
		}

		out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

		if strings.Contains(out, "run summaries") {
			t.Errorf("the update says something about the run summaries with nothing to write:\n%s", out)
		}
		if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
			t.Errorf("the update wrote %v over runs whose records are not there to project", keys)
		}
	})
}

// TestUpdateSummaryLeavesRunInFlight is the HARD RULE's own half of the step
// (docs/design/update-in-flight.md, the row this task adds): the update
// converges while a run of the installation's repository is in flight, the
// backfill writes the summaries of the runs that have finished, and every object
// the run in flight holds is byte for byte what it was — including the summary
// it does not have yet: the run writes its own at its next stage end, and the
// walk never creates an object under a run that is going.
//
// The fixture is TestUpdateLeavesARunInFlightRunning's — a real budget session
// and a real run record, started by startRunInFlight — with the runs of the
// release behind planted beside it, so one update is holding both the runs a
// person can list, which are what the backfill is for, and the run going right
// now, which no step of the update may touch.
func TestUpdateSummaryLeavesRunInFlight(t *testing.T) {
	r := prIndexRig(t)
	b := prIndexBucket(t, r)
	ids := plantSummaryRuns(t, b)
	// The premise: the runs of the release behind hold no summary yet, so the
	// update has something to write beside the run in flight.
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); len(keys) != 0 {
		t.Fatalf("the fixture already holds %v, so this test would pass over a walk that never ran", keys)
	}
	run := startRunInFlight(t, r)
	// What each planted run holds before the update. The summary the walk is
	// about to write beside the finished runs is left out of the comparison --
	// it is the one object that is meant to appear, and summaryKeys holds it to
	// exactly the two runs above -- so what the two trees below compare is
	// everything else: no object rewritten, and no summary under a run that is
	// going or one that never launched.
	held := []string{ids.a, ids.b, ids.plain, ids.running}
	planted := func() map[string][]byte {
		out := map[string][]byte{}
		for _, id := range held {
			for key, data := range runsTree(t, b, "runs/"+appSlug+"/"+id+"/") {
				if strings.HasSuffix(key, "/"+runstore.SummaryName) {
					continue
				}
				out[key] = data
			}
		}
		return out
	}
	before := planted()

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// The note about the runs in flight is untouched by this step, and the
	// backfill still summarized the finished runs beside it.
	if line := inFlightLine(t, out); !strings.Contains(line, "1 run in flight") {
		t.Errorf("the update's note does not count the run in flight:\n%s", line)
	}
	if !strings.Contains(out, "run summaries: 2 run(s)") {
		t.Errorf("the update summarized nothing beside the run in flight:\n%s", out)
	}
	if keys := summaryKeys(t, b, "runs/"+appSlug+"/"); !slices.Equal(keys, []string{
		"runs/" + appSlug + "/" + ids.a + "/" + runstore.SummaryName,
		"runs/" + appSlug + "/" + ids.b + "/" + runstore.SummaryName,
	}) {
		t.Errorf("the update wrote %v, want a summary for the two finished runs alone", keys)
	}
	// What every planted run holds is what it held: no summary appeared under a
	// run that is going or one that never launched, and nothing was rewritten.
	sameTree(t, "the summary backfill", before, planted())
	// And the run in flight goes on: its lease, its ledger, its counters, its
	// record and its review session all still land.
	run.laterWrites(t, r)
}

// TestUpdateSummaryWritesNothingOutsideTheUpdateDoor: the step is `fugaro
// update`'s, and it keeps the fences the steps beside it keep (update.go's
// writeAdminList, update_prindex.go's backfillPRIndex): `fugaro setup` converges
// the same installation through the same hook (init_stages.go's outcome),
// `--plan-only` writes nothing at all, and a coding agent's session under a
// marker changes nothing outside this machine unless the managed gate is the
// read that stands in for this run's confirmations (D1016).
//
// What is asserted is whether the runs bucket is reached at all: every case
// below has a bucket it could write a summary into, and the three fenced ones
// never open it.
func TestUpdateSummaryWritesNothingOutsideTheUpdateDoor(t *testing.T) {
	cases := []struct {
		name   string
		fence  func(t *testing.T, e *initEngine)
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
			e.backfillRunSummary(context.Background())
			if *opened != c.opened {
				t.Errorf("the step opened the runs bucket: %v, want %v: what keeps it out of one is the door it is reached by, the preview it runs under and the session it is in", *opened, c.opened)
			}
		})
	}
}

// TestUpdateSummaryConvergedHistoryReadsNoRun: an installation that has
// converged keeps converging on every later `fugaro update`, and the price of
// that must not be one object read a run. Which runs hold a summary comes from
// ONE listing of the repository (indexedRuns' shape), so a history of a
// thousand runs that all hold one is read by the walk without opening a single
// run; only the runs a summary is missing are read, and each of those is read
// for its record and its task.
//
// Mutations caught: going back to a ReadSummary a run (the driver counts one
// read a run of the window on every update, forever), and a listing that reads
// the runs it walks rather than the summary objects under them.
func TestUpdateSummaryConvergedHistoryReadsNoRun(t *testing.T) {
	const history = 1000
	b, ids := lsBucketRuns(t, history)
	ctx := context.Background()
	for _, id := range ids[:history-1] {
		s := runstore.Open(b.Bucket, appSlug, id)
		rec, err := s.ReadRecord(ctx)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.ReadTask(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := runstore.WriteSummary(ctx, s, runstore.NewSummary(rec, spec, time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	missing := ids[history-1]
	d := &lsWidthDriver{objects: map[string][]byte{}, width: make(chan struct{})}
	lsDriverObjects(t, d, b)

	written, err := backfillRunSummariesIn(ctx, blob.NewBucket(d), time.Now().UTC(), func(string, ...any) {})
	if err != nil {
		t.Fatalf("the walk failed over a converged history: %v", err)
	}
	if written != 1 {
		t.Errorf("the walk wrote %d summaries, want the one run of %d that holds none", written, history)
	}
	want := []string{lsRunObjectKey(missing, "result.json"), lsRunObjectKey(missing, "task.json")}
	if got := d.keysRead(); !slices.Equal(got, want) {
		t.Errorf("the walk read %v, want %v and nothing else: a run that already holds a summary is not read, and a converged installation reads no run at all", got, want)
	}
	if _, ok := d.objects[lsRunObjectKey(missing, runstore.SummaryName)]; !ok {
		t.Errorf("the walk read the one run that holds no summary and wrote none for it")
	}
}
