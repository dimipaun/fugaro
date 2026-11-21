package cli

// A plan's whole way through the daemon, at the command (W1): the session
// imports the plan's graph, the person approves the items it wrote, the daemon
// launches the one task nothing waits on, the run leaves a pull request, the
// pull request merges in the host, and the two tasks that waited on it launch
// on a later wake.
//
// Every piece of this is some other task's own test — the import's (G1), the
// approval's (G2, G4), the merge read's (G3), the launch's (E2, E3) — and what
// is tested here is none of them on its own but the wiring between them: the
// daemon's own tick over the nodes the plans live in, the approval landing on
// the node it names, the runs bucket answering the run view, and the real merge
// reader over the label route answering the tick that the pull request has
// merged. Each of those can be right in its own package and still be unwired
// here.
//
// The three tests the plan names are split across two packages and this is the
// boundary: `TestAdoptionMeetsImport` and `TestDiscoveredEdgeEndToEnd` drive
// `orchestrator.Tick` over a plan's own node and live in
// internal/orchestrator/plan_e2e_test.go, and this one drives the released
// command tree, which only this package can reach — the import's own command,
// the person's typed confirmation and the daemon the unit starts are
// internal/cli's, and a test of them cannot sit in a package that `cli` imports.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// planDemo is the plan the folder docs/demo holds: the plan node's name and the
// prefix of every plan task are the folder's own last element (G1), so the
// import, the approval and the node all agree on this one word.
const planDemo = "demo"

// planPR is the pull request A's run leaves: the number the run's own record
// carries and the one the fake host holds.
const planPR = 7

// seedPlanNode writes ops into one plan's own node the way the import does: the
// node a plan's items live in (D2735), applied as a session over the state the
// node starts from.
func seedPlanNode(t *testing.T, f *budgetFixture, plan string, ops ...orchestrator.Op) orchestrator.State {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops},
		orchestrator.Actor{Class: orchestrator.Session, Name: "session", Now: orchestratorTestNow})
	if err != nil {
		t.Fatalf("seeding the plan's node: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(planImportPath(t, plan), json.RawMessage(raw))
	return next
}

// planNodeRead reads one of the daemon's nodes back the way the CLI reads it.
// It takes the database rather than a fixture because this test drives a daemon
// built on the label fixture, and the command tests' own readers
// (storedPlanState, storedOrchestratorState) are the same read over the budget
// fixture this one is not: the document is marshalled back and decoded by the
// rules the format carries, so a node no release would write cannot pin the
// cycle that follows.
func planNodeRead(t *testing.T, db *gcpfake.RTDB, path string) orchestrator.State {
	t.Helper()
	v := db.Value(path)
	if v == nil {
		t.Fatalf("the node %s is not written", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("reading %s back: %v", path, err)
	}
	return s
}

// planItem reads one item of the plan's node, or fails naming what the node
// carries instead: every assertion below is about one task of the imported
// graph, and "there is no such item" is the failure worth naming.
func planItem(t *testing.T, f *labelFixture, id string) orchestrator.WorkItem {
	t.Helper()
	s := planNodeRead(t, f.db, planImportPath(t, planDemo))
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("the node of plan %s carries no item %q: %+v", planDemo, id, s.WorkItems)
	}
	return w
}

// planRunRecord writes one run's record the way the runner leaves it as it ends:
// the outcome and the pull request in one document, which is where the daemon's
// view reads both from (F25). The task and the launch record are the launcher's
// own — it wrote them when it started the run — so this is the one file a test
// writes to say the run is over.
func planRunRecord(t *testing.T, f *labelFixture, runID string, number int) {
	t.Helper()
	if runID == "" {
		t.Fatal("no run to write a record for: the item carries no run id, so nothing was started")
	}
	rec := &runstore.Record{
		Version: 1, RunID: runID, Repo: labelRepo, Status: runstore.StatusSucceeded,
		PR: &runstore.PRRef{Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", labelRepo, number)},
	}
	if err := f.runStore(t, runID).WriteRecord(context.Background(), rec); err != nil {
		t.Fatalf("writing the record of the run %s: %v", runID, err)
	}
}

// planRunEnded ends the execution of the run the daemon started, which is what
// a real installation's platform does when a run's process exits and what the
// fake leaves alone: an execution of the fake runs until a test says otherwise,
// and a run that has ended is not a run the parallel cap counts. The run's own
// identity in the request is `FUGARO_RUN`, the `<slug>/<run id>` the launcher
// sets, so this is the execution of that run and not of whatever started last.
func planRunEnded(t *testing.T, f *labelFixture, runID string) {
	t.Helper()
	for _, call := range f.run.RunRequests() {
		if call.Env["FUGARO_RUN"] == appSlug+"/"+runID && call.Execution != "" {
			f.run.SetState(call.Execution, backend.StateSucceeded)
			return
		}
	}
	t.Fatalf("no execution of the run %s was started, so the run this test ends is not one that ran", runID)
}

// TestPlanRunsEndToEnd is W1's acceptance at the command: a three-node plan
// (A -> B, A -> C) is imported from a checkout, approved with one plan_approve
// through the person's own typed confirmation, and driven by the daemon the unit
// starts. The daemon launches the one task nothing waits on; that run finishes
// with a pull request and the item waits in `review`; the pull request merges in
// the host and the real merge reader — the credentials service's own label route
// — moves the item to `done`; and the two tasks that waited on it launch in that
// same cycle.
//
// Both halves of the wiring between the fold and the merge read are load-bearing,
// and each fails a different way: leave the run view's pull request number unset
// and the item folds straight to `done` on a run whose pull request nobody has
// merged, and build the tick's merge seam from anything but the label route — or
// hold the plan's node out of the tick's node list — and the item waits in
// `review` for ever, with B and C queued behind it. Both are what a plan whose
// merge is unwired does in production, and the second is silent.
func TestPlanRunsEndToEnd(t *testing.T) {
	// The host A's pull request is read through: the run leaves number 7 and it
	// is open when the daemon first asks.
	f := newLabelFixture(t, labelPR(planPR, gitprov.PROpen))
	// The installation's clock, and the one thing this test moves: the merge read
	// is per repository and per cadence (D2742), so the cycle after the merge has
	// to see a later reading than the cycle before it.
	now := orchestratorTestNow
	f.db.SetClock(func() time.Time { return now })
	// The node the daemon drives: the project's state, with no work of its own —
	// a plan's items do not live here (D2735).
	f.seed(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "carry the demo plan through",
	})

	// The session's half: the graph in the checkout becomes the items of the
	// plan's own node, and nothing of the state node.
	t.Chdir(planImportCheckout(t, planImportGraph, planImportPlanMD))
	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if s := planNodeRead(t, f.db, orchestratorPath(t, orchestrator.StateNode)); len(s.WorkItems) != 0 {
		t.Fatalf("the import wrote %d item(s) into the state node, and a plan's items live in the plan's own node (D2735)", len(s.WorkItems))
	}
	imported := planNodeRead(t, f.db, planImportPath(t, planDemo))
	if len(imported.WorkItems) != 3 {
		t.Fatalf("the plan's node holds %d items after the import, and the graph has three tasks", len(imported.WorkItems))
	}

	// The person's half: one approval for the whole graph, typed over the
	// project's own name, landing on the node its list names (G2, D2706).
	asPerson(t)
	typed(t)
	doc := patchFile(t, patchDoc(t, imported.Rev, planApprove(t, imported, planDemo, "A", "B", "C")))
	out, _, err = runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", doc)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	approved := planNodeRead(t, f.db, planImportPath(t, planDemo))
	for _, id := range []string{"A", "B", "C"} {
		if w, ok := approved.Item(id); !ok || w.Approved == nil {
			t.Fatalf("the plan's %s reads %+v after one plan_approve, and the person approved it:\n%s", id, w, out)
		}
	}

	// The daemon, and the two things a person outside it does between its cycles:
	// what a run's own runner reports when it ends, and what a person does to the
	// pull request it left. Everything else — the plan listed each cycle, the
	// fold, the merge read and the launches — is the daemon's own.
	d, dout := f.daemon(t)
	var (
		wakes  int
		merged orchestrator.WorkItem
	)
	d.wake = func(ctx context.Context) error {
		wakes++
		switch wakes {
		case 1:
			// A's run ended with a pull request. The daemon started it, so its id
			// is the one the item carries.
			runID := planItem(t, f, "A").RunID
			planRunEnded(t, f, runID)
			planRunRecord(t, f, runID, planPR)
		case 2:
			// A ended with a pull request open in the host, and this is the half
			// the whole plan turns on: the item waits in `review` — not done, and
			// not failed — and the two tasks that wait on it are exactly as the
			// person approved them, with no run of their own (D2705).
			if w := planItem(t, f, "A"); w.Phase != orchestrator.PhaseReview {
				t.Fatalf("A is %s with its pull request open, and a run that succeeded leaving one waits in `review`:\n%s", w.Phase, dout.String())
			}
			for _, id := range []string{"B", "C"} {
				if w := planItem(t, f, id); w.Phase != orchestrator.PhaseQueued || w.RunID != "" {
					t.Fatalf("%s is %s under the run %q while its own dependency is unmerged, and nothing that waits may start:\n%s", id, w.Phase, w.RunID, dout.String())
				}
			}
			if execs := f.run.Executions(); len(execs) != 1 {
				t.Fatalf("the daemon started %d execution(s) while A's pull request was open, and only A's own run is going", len(execs))
			}
			// The person merges it in the host, and the repository's own cadence
			// moves on so the next cycle reads the host again (D2742).
			f.closePR(t, planPR, gitprov.PRMerged)
			now = now.Add(2 * time.Minute)
		default:
			// This wake is the first one after the cycle that read the merge, and
			// everything the acceptance asks for is already true in it: A is done,
			// and the two tasks that waited on it are started — in that same
			// cycle, which is what puts the merge read before the plan (D2735).
			merged = planItem(t, f, "A")
			for _, id := range []string{"B", "C"} {
				if w := planItem(t, f, id); w.RunID == "" {
					t.Errorf("%s has no run at the first wake after the merge, and the cycle that reads the merge is the cycle that starts the work waiting on it:\n%s", id, dout.String())
				}
			}
			f.cancel()
			return context.Canceled
		}
		return nil
	}
	if err := d.start(f.ctx); err != nil {
		t.Fatalf("the daemon: %v\n%s", err, dout.String())
	}

	// A's own run: the one the daemon launched, whose record left its item in
	// `review` and whose pull request the merge read answered for.
	a := planItem(t, f, "A")
	if a.RunID == "" {
		t.Fatal("A carries no run id: nothing the daemon launched is what this plan is about")
	}
	if a.Phase != orchestrator.PhaseDone {
		t.Errorf("A is %s after its pull request merged, and a merged pull request is what its item waited for (D2705):\n%s", a.Phase, dout.String())
	}
	// The same item, read in the wake the merge cycle ended in: the merge read is
	// what moved it, and not a later cycle nobody watched.
	if merged.Phase != orchestrator.PhaseDone || merged.RunID != a.RunID {
		t.Errorf("the item read as the merge cycle ended is %+v, want the run's item once the merge had landed", merged)
	}

	// B and C: both waited on A, both were approved, and both started in the
	// cycle the merge was read in — which is what makes the merge read a read
	// before the plan is made (D2735).
	runs := map[string]bool{}
	for _, id := range []string{"A", "B", "C"} {
		w := planItem(t, f, id)
		if !slices.Equal(w.DependsOn, planDependsOn(id)) {
			t.Errorf("%s waits for %v, and the graph's own edges say %v", id, w.DependsOn, planDependsOn(id))
		}
		if w.Phase != orchestrator.PhaseDone && w.Phase != orchestrator.PhaseRunning {
			t.Errorf("%s is %s, want `running` for the work the daemon started and `done` for the merged task", id, w.Phase)
		}
		if id != "A" && w.RunID == "" {
			t.Errorf("%s carries no run id: the work the merge unlocked did not start:\n%s", id, dout.String())
		}
		if w.RunID != "" {
			runs[w.RunID] = true
		}
	}
	if len(runs) != 3 {
		t.Errorf("the plan's three tasks name %d run(s), and one task is one run", len(runs))
	}
	if execs := f.run.Executions(); len(execs) != 3 {
		t.Errorf("the daemon started %d execution(s), want one per task of the plan:\n%s", len(execs), dout.String())
	}
	// The plan's items never became the state node's: the whole graph, and the
	// runs that came out of it, belong to the plan's own node (D2735).
	if s := planNodeRead(t, f.db, orchestratorPath(t, orchestrator.StateNode)); len(s.WorkItems) != 0 {
		t.Errorf("the state node carries %+v, and nothing of a plan lands there", s.WorkItems)
	}
}

// planDependsOn is the one edge the test's graph gives a task: A depends on
// nothing, and B and C each depend on A (planImportGraph).
func planDependsOn(id string) []string {
	if id == "A" {
		return nil
	}
	return []string{"A"}
}
