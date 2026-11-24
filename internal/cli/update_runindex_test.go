package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// update_runindex_test.go is RI5's step as the owner meets it: one `fugaro
// update` marks the installation's run index for a rebuild and starts the
// history job's reindex execution, over an installation whose runs are going
// right now.
//
// What the suite has to hold is the whole of the claim: the mark lands, the
// execution is started once in the job's --reindex mode with no timeout
// override, the run in flight is neither waited for nor touched, and the
// fences the steps beside it keep are kept here too.

// reindexConvergeRig is the update door's own fixture with the two things the
// step writes to: the installation's Firestore (the index) and the history job
// it starts. The database is the rig's own fake rather than a second one,
// because the config the update loads -- the project config registerInstallation
// copied out of the rig's -- already names it.
func reindexConvergeRig(t *testing.T) (*convergeRig, *gcpfake.Firestore) {
	t.Helper()
	r := newConvergeRig(t, currentNode())
	// A fresh Firebase project has no Firestore database (newFBRigFor), and
	// the update's own firebase stage creates one where it is missing. The
	// fixture's project is one the update leaves alone, so the database the
	// index lives in is put there for this step to write into.
	r.fs.SetDatabase("us-east5")
	r.run.AddJob(infra.HistoryJob, "1", "2Gi")
	return r, r.fs
}

// TestUpdateStartsReindexWithRunInFlight is RI5's step and the HARD RULE
// together (docs/design/update-in-flight.md): an installation whose runs are
// going is upgraded exactly as an idle one is. The step marks the index --
// which is the half every later sweep continues on its own (F9, F39) -- and
// starts one execution of the history job in --reindex mode, and the run in
// flight is neither waited for nor written to: the walk reads the runs bucket
// and writes the index, and the run's own later writes all still land.
func TestUpdateStartsReindexWithRunInFlight(t *testing.T) {
	r, fs := reindexConvergeRig(t)
	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	// The one line the update prints about it, and the execution it names.
	if !strings.Contains(out, "run index: marked incomplete and reindexing now as ") {
		t.Fatalf("the update says nothing about the reindex it started:\n%s", out)
	}
	// The note about the runs in flight is the door's own: the step adds no
	// second line about them and waits for none of them.
	if line := inFlightLine(t, out); !strings.Contains(line, "1 run in flight") {
		t.Errorf("the update's note does not count the run in flight:\n%s", line)
	}

	// The job was started once, in --reindex mode, with no timeout override.
	var started []gcpfake.RunCall
	for _, c := range r.run.RunRequests() {
		if c.Job == infra.HistoryJob {
			started = append(started, c)
		}
	}
	if len(started) != 1 {
		t.Fatalf("the history job was started %d times, want one: %+v", len(started), r.run.RunRequests())
	}
	if got := strings.Join(started[0].Args, " "); got != "budget history --reindex" {
		t.Errorf("the history job was started with %q, want --reindex", got)
	}
	if started[0].Timeout != "" {
		t.Errorf("the start overrode the job's own timeout: %q", started[0].Timeout)
	}
	if !strings.Contains(out, started[0].Execution) {
		t.Errorf("the update does not name the execution it started:\n%s", out)
	}

	// The mark: the summary is not complete, so the next sweep of the history
	// job walks the index even if this execution never runs.
	sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		t.Fatal("the step left no summary, so no sweep would ever walk")
	}
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("summary = %v", sum)
	}
	l, ok := r.db.Value(runindex.PathLease).(map[string]any)
	if !ok {
		t.Fatalf("the step left no lease node: %#v", r.db.Value(runindex.PathLease))
	}
	if c, ok := l["cursor"]; ok && c != nil && c != "" {
		t.Fatalf("the step left a cursor behind: %#v", l)
	}
	if l["holder"] == "" {
		t.Fatalf("the lease names nobody: %#v", l)
	}

	// And the run in flight goes on: its lease, its ledger, its counters, its
	// record and its review session all still land.
	run.laterWrites(t, r)
}

// TestUpdateReindexWithoutAReportedName is the same step when the platform
// started the execution but its operation reported no readable name
// (gcp.ErrStartedUnnamed): the mark landed and the walk is going, so the line
// says so without a name rather than printing "reindexing now as" and stopping.
func TestUpdateReindexWithoutAReportedName(t *testing.T) {
	r, _ := reindexConvergeRig(t)
	r.run.BadRunMetadata = true

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if !strings.Contains(out, "run index: marked incomplete and reindexing now (the platform started the execution but did not report its name), so `fugaro runs ls` is short") {
		t.Fatalf("the update's reindex line does not name the start without an execution name:\n%s", out)
	}
	if strings.Contains(out, "no reindex execution was started") {
		t.Fatalf("a started reindex was reported as one that never started:\n%s", out)
	}
}

// TestUpdateDoesNotReindexACompleteIndex is the first reindex and not one per
// upgrade (RI5): the walk reads every run in the bucket, and an installation
// whose index is complete with no unfinished walk has nothing for it to
// repair. `fugaro update` leaves such an index exactly as it stands -- no
// mark, no lease, no execution -- and `fugaro runs reindex` is the command
// that asks for a rebuild by hand.
func TestUpdateDoesNotReindexACompleteIndex(t *testing.T) {
	r, fs := reindexConvergeRig(t)
	at := time.Now().UTC()
	fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, map[string]any{
		"version": int64(1), "complete": true, "folded_through": at, "updated_at": at,
		"runs": int64(3), "states": map[string]any{"succeeded": int64(3)},
		"spent_micros": int64(0), "notional_micros": int64(0),
		"compute_runs": int64(0), "compute_not_estimated": int64(0),
	})

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if strings.Contains(out, "run index:") {
		t.Fatalf("an update over a complete index said something about the run index:\n%s", out)
	}
	for _, c := range r.run.RunRequests() {
		if c.Job == infra.HistoryJob {
			t.Fatalf("an update over a complete index started the history job: %+v", c)
		}
	}
	if got := r.db.Value(runindex.PathLease); got != nil {
		t.Fatalf("an update over a complete index took the writer lease: %#v", got)
	}
	sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		t.Fatal("the step removed the summary")
	}
	if done, ok := sum["complete"].(bool); !ok || !done {
		t.Fatalf("an update over a complete index latched it incomplete: %v", sum)
	}
	if sum["runs"] != int64(3) {
		t.Fatalf("an update over a complete index rewrote the summary: %v", sum)
	}
}

// TestUpdateReindexesAnIncompleteIndex is the other half of the same rule: the
// index a fold has left incomplete -- or one whose lease still carries an
// unfinished walk -- IS the installation the first reindex is for.
func TestUpdateReindexesAnIncompleteIndex(t *testing.T) {
	r, fs := reindexConvergeRig(t)
	at := time.Now().UTC()
	fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, map[string]any{
		"version": int64(1), "complete": false, "folded_through": at, "updated_at": at,
	})

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if !strings.Contains(out, "run index: marked incomplete and reindexing now as ") {
		t.Fatalf("an update over an incomplete index did not reindex:\n%s", out)
	}
	var started int
	for _, c := range r.run.RunRequests() {
		if c.Job == infra.HistoryJob {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("the history job was started %d times, want one", started)
	}
}

// TestUpdateStandsWhenTheReindexJobStartIsRefused is O7's other caller: the
// update's step writes the mark first and only then attempts the start, and
// only the installation's scheduler account holds run.invoker on the history
// job, so a caller without it is refused by the platform. The refusal must not
// fail the update: the mark already landed, the walk is owed, and the step says
// so in a warning rather than insisting on the start it may not make.
func TestUpdateStandsWhenTheReindexJobStartIsRefused(t *testing.T) {
	r, fs := reindexConvergeRig(t)
	r.run.FailRunWith = 403

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if !strings.Contains(out, "but no reindex execution was started") || !strings.Contains(out, "The walk is owed all the same") {
		t.Fatalf("the update does not report the refused start and the mark that stands:\n%s", out)
	}
	// The mark landed before the start was tried, which is what makes the
	// refusal harmless: the summary is incomplete and the lease names a holder
	// with no cursor, so the next sweep of the history job walks the index.
	sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		t.Fatal("the step left no summary, so no sweep would walk")
	}
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("summary = %v", sum)
	}
	l, ok := r.db.Value(runindex.PathLease).(map[string]any)
	if !ok {
		t.Fatalf("the step left no lease node: %#v", r.db.Value(runindex.PathLease))
	}
	if c, ok := l["cursor"]; ok && c != nil && c != "" {
		t.Fatalf("the step left a cursor behind: %#v", l)
	}
}

// TestUpdateReindexWritesNothingOutsideTheUpdateDoor: the step is `fugaro
// update`'s, and it keeps the fences the steps beside it keep (update.go's
// writeAdminList, update_prindex.go's backfillPRIndex): `fugaro setup`
// converges the same installation through the same hook (init_stages.go's
// outcome), `--plan-only` writes nothing at all, and a coding agent's session
// under a marker changes nothing outside this machine unless the managed gate
// is the read that stands in for this run's confirmations (D1016).
//
// What is asserted is whether the step reached the installation's cloud at
// all — the database it opens and the job it starts — which is the difference
// between "the step decided not to write" and "the step had nothing to write".
func TestUpdateReindexWritesNothingOutsideTheUpdateDoor(t *testing.T) {
	cases := []struct {
		name  string
		fence func(t *testing.T, e *initEngine)
		// reached is whether the step is expected to touch the cloud: the three
		// fenced cases never build a client, the two admitted ones do.
		reached bool
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
			e, _, db, job := reindexStepEngine(t)
			c.fence(t, e)
			e.reindexRunIndex(context.Background())
			// A step that got past its fences writes the lease and starts the
			// job; one the fences stop does neither, so the two readings
			// together are whether it reached the installation's cloud at all.
			reached := db.Value(runindex.PathLease) != nil || len(job.RunRequests()) > 0
			if reached != c.reached {
				t.Errorf("the step reached the installation's cloud: %v, want %v: what keeps it out of one is the door it is reached by, the preview it runs under and the session it is in", reached, c.reached)
			}
		})
	}
}

// reindexStepEngine is the step's fixture without the call: the engine, where
// the step writes what it prints, the installation's budget database and its
// history job — the two things a step that got past its fences writes to and
// starts. A test that has to say something about the run the step is reached by
// rather than about the step alone — the door, the preview, the session, the
// gate — takes the engine, sets that, and calls reindexRunIndex itself.
//
// The database is the fake the harness adopts (the project name and the mark
// every writer asks for) and the job is the Run fake, so a step that got past
// its fences finds a database it may write and a job it may start, and says
// what it did rather than failing on a missing endpoint.
func reindexStepEngine(t *testing.T) (e *initEngine, out *strings.Builder, db *gcpfake.RTDB, job *gcpfake.Run) {
	t.Helper()
	db = gcpfake.NewRTDB(t)
	adoptBudgetFake(db)
	job = gcpfake.NewRun(t)
	job.Project, job.Region = initProject, "us-east5"
	job.AddJob(infra.HistoryJob, "1", "2Gi")
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		RunsBucket: "fugaro-runs-" + initProject,
		Bucket:     "file://" + t.TempDir(),
		Budget:     &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints:  localcfg.Endpoints{NoAuth: true, Firestore: gcpfake.NewFirestore(t).URL, Run: job.URL + "/"},
	}
	out = &strings.Builder{}
	e = &initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}, w: out}, lc: lc}
	return e, out, db, job
}
