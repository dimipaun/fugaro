package orchestrator_test

// The fold of what a finished run reported about the graph (E3, D2709, D2710).
//
// X2 gives the run record what a run learned while it worked: the items its own
// work blocks and the ones it found it needed, each named by a plan task or by
// a path, and the paths a rebase or a push met a conflict on. What these tests
// pin is the daemon's half of it (fold_discovered.go): the three rules the
// plan's acceptance names, each against the state the fence really reads.
//
//   - TestFoldAddsEdgeInFrontOfQueuedItem and TestAddedEdgeUnapprovesWithFact:
//     an entry that names an item which has not launched becomes an edge in
//     front of that item, in the same patch as the outcome that ends the run,
//     and the item waits for a person's approval again with one fact naming the
//     item, the edge and the run (D2740).
//   - TestFoldNeverRemovesOrUnblocks and TestBlocksStayInTheRunsOwnNode:
//     nothing a run reports removes an edge, moves a phase, reaches an item
//     another attempt is going for, or crosses into another node (F44, F51).
//   - TestFoldRefusesCycle and TestFoldRefusesPastMaxDependsOn: an addition the
//     fence would refuse leaves one line in the audit log and the fold of the
//     run's outcome rides on — ValidateState checks no cycle (F42), so a fold
//     that insisted on one would leave a node every tick refuses to read again.
//   - TestNeededBecomesGraphSuspect and TestOneGraphSuspectFactPerRun: what a
//     run needed reorders nothing and leaves at most one fact per run.
//   - TestConflictEdgeOnNextAttempt and TestFoldIgnoresRunningAttempts: a
//     conflict path becomes an edge in front of the item for its next attempt,
//     and never under an attempt that is going (D2710).
//   - TestFoldIsIdempotent: a second cycle over the same report adds nothing.
//
// Everything runs over internal/gcpfake's Realtime Database fake and the tick
// harness in tick_test.go: no live cloud, no clock of this machine, no key and
// no secret.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// foldItem is one item of a fold case: the standard declaration with the
// changes the caller names applied in order and approved after all of them, so
// the digest the approval covers is the declaration as it stands (o6ApprovedBy)
// — what a person read when they approved it. An item whose phase is done is
// left carrying no approval, which is the shape validate.go refuses rather than
// one a test may seed.
func foldItem(id string, phase orchestrator.Phase, approvedAt time.Time, mut ...func(*orchestrator.WorkItem)) orchestrator.WorkItem {
	w := workItemOf(id)
	w.Phase = phase
	for _, m := range mut {
		m(&w)
	}
	if w.Phase.Done() {
		return w
	}
	return o6ApprovedBy(w, approvedAt)
}

// planTask is the plan task an item stands for, which is what a `blocks` or
// `needed` entry naming a plan task is matched against.
func planTask(task string) func(*orchestrator.WorkItem) {
	return func(w *orchestrator.WorkItem) { w.PlanTask = task }
}

// inRepo is the repository an item is of.
func inRepo(repo string) func(*orchestrator.WorkItem) {
	return func(w *orchestrator.WorkItem) { w.Repo = repo }
}

// waitsFor is the items an item depends on.
func waitsFor(deps ...string) func(*orchestrator.WorkItem) {
	return func(w *orchestrator.WorkItem) { w.DependsOn = deps }
}

// foldRun is the item a report is about — its attempt committed and not
// reported, which is o6OpenItem's shape — with the declaration the caller names
// digested into both the item's approval and the intent.
func foldRun(id, runID string, approvedAt, at time.Time, mut ...func(*orchestrator.WorkItem)) (orchestrator.WorkItem, orchestrator.Intent) {
	w := workItemOf(id)
	w.Phase = orchestrator.PhaseRunning
	for _, m := range mut {
		m(&w)
	}
	w = o6ApprovedBy(w, approvedAt)
	w.RunID = runID
	w.Attempts = 1
	it := orchestrator.Intent{
		ID:         orchestrator.IntentID(id, 1),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: approvedAt.UTC(),
		At:         at.UTC(),
	}
	return w, it
}

// foldMerged is an item whose pull request merged: done, with the attempt that
// merged it recorded, which is the only stamp on the node that says when the
// merge came (G3's read writes the phase and nothing else).
func foldMerged(id, runID string, at time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	w := workItemOf(id)
	w.Phase = orchestrator.PhaseDone
	w.RunID = runID
	w.Attempts = 1
	it := orchestrator.Intent{
		ID:         orchestrator.IntentID(id, 1),
		RunID:      runID,
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: at.UTC(),
		At:         at.UTC(),
		Outcome:    orchestrator.OutcomeSucceeded,
	}
	return w, it
}

// foldReport is one finished run's report: it started, it reported, and what it
// reported about the graph is exactly what the caller names.
func foldReport(runID string, outcome orchestrator.Outcome, blocks, needed []runstore.DiscoveredItem, conflicts ...string) orchestrator.RunReport {
	return orchestrator.RunReport{
		RunID:         runID,
		Started:       true,
		Finished:      true,
		Outcome:       outcome,
		Discovered:    runstore.NewDiscovered(blocks, needed),
		ConflictPaths: runstore.NewConflictPaths(conflicts),
	}
}

// namesTask is the entry a run reports for a plan task.
func namesTask(task string) runstore.DiscoveredItem {
	return runstore.DiscoveredItem{PlanTask: task, Reason: "my change lands on the files " + task + " owns"}
}

// namesPath is the entry a run reports for a path.
func namesPath(path string) runstore.DiscoveredItem {
	return runstore.DiscoveredItem{Path: path, Reason: "my change touches " + path}
}

// foldOnly shuts every spawn of the harness's caps. A fold case is about the
// graph and not about launching, and an item a person had approved would
// otherwise start a run in the very cycle the fold is read in. What a launch
// reads of a fold — that an added edge takes the approval away, so the item a
// person approved is not one a run starts under — is pinned with the caps open
// in TestAddedEdgeUnapprovesWithFact.
func foldOnly(h *harness) {
	h.tick.Caps = withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 0 })
}

// foldItemOf is one item of a node as it now stands.
func foldItemOf(t *testing.T, s orchestrator.State, id string) orchestrator.WorkItem {
	t.Helper()
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("the node no longer carries %s", id)
	}
	return w
}

// foldLog is the audit log as the node now carries it, and nil when the fold
// left no line at all.
func foldLog(t *testing.T, h *harness) map[string]any {
	t.Helper()
	v := h.db.Value(mustPath(t, orchestrator.PathAudit))
	if v == nil {
		return nil
	}
	return nodeObject(t, v, "the audit log")
}

// foldRefusal is the one line a refused addition leaves, filed under the item
// whose declaration the edge would have changed: the log derives the id from
// the subject and the transition, so the same refusal about one item is one
// line however often a cycle derives it.
func foldRefusal(t *testing.T, h *harness, subject string) map[string]any {
	t.Helper()
	log := foldLog(t, h)
	if len(log) != 1 {
		t.Fatalf("the log carries %d line(s) %v, and one refused addition leaves one", len(log), logKeys(log))
	}
	entry, ok := log[subject+":graph-refused"].(map[string]any)
	if !ok {
		t.Fatalf("the log carries %v, and the refusal about %q is the line %q", logKeys(log), subject, subject+":graph-refused")
	}
	return entry
}

// foldFactKeys is a node's facts by key, for a message about the ones it
// carries.
func foldFactKeys(facts map[string]orchestrator.Fact) []string {
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// foldEdgeFact is the one fact D2740 asks for beside an edge the daemon added.
// It is found by its family rather than by a key this test builds: the key is
// the fold's own (a digest of the item), and what a person reads is the line.
func foldEdgeFact(t *testing.T, s orchestrator.State) orchestrator.Fact {
	t.Helper()
	for _, k := range foldFactKeys(s.Facts) {
		if strings.HasPrefix(k, "graph_edge_") {
			return s.Facts[k]
		}
	}
	t.Fatalf("the node carries the facts %v, and D2740 asks for one beside every edge the daemon added", foldFactKeys(s.Facts))
	return orchestrator.Fact{}
}

// TestFoldAddsEdgeInFrontOfQueuedItem is the plan's red step: an item that has
// not launched and is named by what a finished run reported waits for the run's
// own item, in the same patch that ends the run.
func TestFoldAddsEdgeInFrontOfQueuedItem(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	// The run's item stands for no plan task, so the entry below names its
	// target by the task the target stands for.
	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
	}

	rep := h.step()
	got := h.state()
	b := foldItemOf(t, got, "wi-b")
	if !slices.Equal(b.DependsOn, []string{"wi-a"}) {
		t.Errorf("the item the run blocks waits for %v, and the run reported that its work blocks it", b.DependsOn)
	}
	if b.Phase != orchestrator.PhaseQueued {
		t.Errorf("the item is %s, and an added edge is a change to a declaration rather than to a phase", b.Phase)
	}
	if a := foldItemOf(t, got, "wi-a"); a.Phase != orchestrator.PhaseDone {
		t.Errorf("the run's own item is %s after a succeeded run that left no pull request", a.Phase)
	}
	if rep.Folded != 1 {
		t.Errorf("the cycle folded %d finished runs where one reported", rep.Folded)
	}
	if len(h.launcher.asked) != 0 {
		t.Errorf("the cycle started the runs %v, and the item it added an edge to waits for a person now (D2740)", h.launcher.asked)
	}
}

// TestFoldNeverRemovesOrUnblocks is the add-only rule: a report extends a
// declaration, never replaces one, and an item whose attempt is already in
// motion is left exactly as it stands.
func TestFoldNeverRemovesOrUnblocks(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	t.Run("an addition extends the dependencies and moves no phase", func(t *testing.T) {
		merged, mergedIt := foldMerged("wi-c", "20261107-083000-cccc", now.Add(-2*time.Hour))
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"), waitsFor("wi-c"))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, merged, target}, []orchestrator.Intent{it, mergedIt}), now)
		foldOnly(h)
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
		}

		h.step()
		got := h.state()
		b := foldItemOf(t, got, "wi-b")
		if !slices.Equal(b.DependsOn, []string{"wi-c", "wi-a"}) {
			t.Errorf("the dependencies are %v, and the edge the run asked for is added to the ones the item had", b.DependsOn)
		}
		if b.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item is %s, and nothing a run reports moves an item between phases", b.Phase)
		}
		if c := foldItemOf(t, got, "wi-c"); c.Phase != orchestrator.PhaseDone {
			t.Errorf("the merged item is %s after a cycle over a report about another item", c.Phase)
		}
	})

	t.Run("an item an attempt already ran for is left alone", func(t *testing.T) {
		// A failed item under a standing approval is one the plan retries: an
		// edge in front of it would un-approve it and stop a retry that is
		// already in motion, which is the direction D2709 refuses. The entry
		// names it by path — it stands for no plan task — and the footprint is
		// what says which item a path is about.
		failed, failedIt := o6FailedItem("wi-b", approvedAt, now.Add(-20*time.Minute))
		failed.RunID = "20261107-083000-dddd"
		failedIt.RunID = failed.RunID
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))

		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, failed}, []orchestrator.Intent{it, failedIt}), now)
		foldOnly(h)
		h.tick.Footprints = orchestrator.Footprints{"wi-b": plan.NewFootprint([]string{"go.mod"})}
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesPath("go.mod")}, nil),
		}

		h.step()
		got := h.state()
		b := foldItemOf(t, got, "wi-b")
		if len(b.DependsOn) != 0 || b.Phase != orchestrator.PhaseFailed || b.Approved == nil {
			t.Errorf("the item reads back as %+v, and an item whose attempt already ran keeps the declaration it ran under", b)
		}
		if b.RunID != failed.RunID {
			t.Errorf("the item's run id is %q, not %q: nothing here is the fold's to change", b.RunID, failed.RunID)
		}
		if len(got.Facts) != 0 {
			t.Errorf("the node carries the facts %v for an edge the fold did not add", foldFactKeys(got.Facts))
		}
	})
}

// TestFoldRefusesCycle pins F42's half of the fence: ValidateState checks no
// cycle, so the fold refuses one itself — with a line, and without stopping the
// fold of the run that ended.
func TestFoldRefusesCycle(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	// The run's item already waits for the item the entry names, so the edge
	// the entry asks for is the other way round and would close a loop.
	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute), waitsFor("wi-b"))
	target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it}), now)
	foldOnly(h)
	h.tick.Audit = mustAudit(t, rtdbClient(t, h.db), clockAt(now))
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
	}

	rep := h.step()
	got := h.state()
	b := foldItemOf(t, got, "wi-b")
	if len(b.DependsOn) != 0 {
		t.Errorf("the item depends on %v after a cycle whose only addition would have closed a loop", b.DependsOn)
	}
	if b.Approved == nil {
		t.Error("a refused addition un-approved an item whose declaration did not change: the fence refused it, so nothing was written")
	}
	if a := foldItemOf(t, got, "wi-a"); a.Phase != orchestrator.PhaseDone {
		t.Errorf("the item is %s, and a refused addition is not a reason to stop folding the run that ended", a.Phase)
	}
	if rep.Folded != 1 {
		t.Errorf("the cycle folded %d finished runs where one reported", rep.Folded)
	}

	entry := foldRefusal(t, h, "wi-b")
	if sub := nodeText(t, entry, "subject"); sub != "wi-b" {
		t.Errorf("the line's subject is %q, and the item whose declaration the edge would have changed is the subject a reader finds it under", sub)
	}
	if tr := nodeText(t, entry, "transition"); tr != "graph-refused" {
		t.Errorf("the line's transition is %q, want graph-refused", tr)
	}
	text := nodeText(t, entry, "text")
	if !strings.Contains(text, "(cycle)") {
		t.Errorf("the line is %q, and the code a reader goes to the node with is cycle", text)
	}
	if !strings.Contains(text, "wi-a -> wi-b") || !strings.Contains(text, runID) {
		t.Errorf("the line is %q, and it has to name the edge that was not added and the run that asked for it", text)
	}
}

// TestFoldRefusesPastMaxDependsOn is the fence's other bound: an item that
// already waits for MaxDependsOn others takes no more, however good the reason,
// and the line says so rather than the tick failing.
func TestFoldRefusesPastMaxDependsOn(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	items := make([]orchestrator.WorkItem, 0, orchestrator.MaxDependsOn+2)
	deps := make([]string, 0, orchestrator.MaxDependsOn)
	for i := 0; i < orchestrator.MaxDependsOn; i++ {
		id := fmt.Sprintf("wi-d%02d", i)
		items = append(items, foldItem(id, orchestrator.PhaseDone, approvedAt))
		deps = append(deps, id)
	}
	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	items = append(items, w, foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"), waitsFor(deps...)))

	h := newHarness(t, tickState(t, now, items, []orchestrator.Intent{it}), now)
	foldOnly(h)
	h.tick.Audit = mustAudit(t, rtdbClient(t, h.db), clockAt(now))
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
	}

	rep := h.step()
	got := h.state()
	b := foldItemOf(t, got, "wi-b")
	if !slices.Equal(b.DependsOn, deps) {
		t.Errorf("the item waits for %d items after a cycle that asked it to wait for %d: a bound the fence holds is not one the fold passes", len(b.DependsOn), len(deps))
	}
	if b.Approved == nil {
		t.Error("a refused addition un-approved an item whose declaration did not change")
	}
	if rep.Folded != 1 {
		t.Errorf("the cycle folded %d finished runs where one reported", rep.Folded)
	}
	if text := nodeText(t, foldRefusal(t, h, "wi-b"), "text"); !strings.Contains(text, "(deps)") {
		t.Errorf("the line is %q, and the code a reader goes to the node with is deps", text)
	}
}

// TestFoldIgnoresRunningAttempts is the other half of the fence: an item an
// attempt is going for is left alone, because what a new edge would change is
// the declaration that attempt was launched under.
func TestFoldIgnoresRunningAttempts(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"
	const going = "20261107-090500-eeee"

	t.Run("an item whose own run is going keeps its declaration", func(t *testing.T) {
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		target, targetIt := foldRun("wi-b", going, approvedAt, now.Add(-20*time.Minute), planTask("plan-a/T2"))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it, targetIt}), now)
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
			{RunID: going, Started: true},
		}

		h.step()
		b := foldItemOf(t, h.state(), "wi-b")
		if len(b.DependsOn) != 0 {
			t.Errorf("the item waits for %v, and its attempt is going: the edge is not added under a run that is in flight", b.DependsOn)
		}
		if b.Phase != orchestrator.PhaseRunning || b.RunID != going || b.Approved == nil {
			t.Errorf("the item reads back as %+v, and nothing here was the fold's to change", b)
		}
	})

	t.Run("a conflict edge waits for an item no attempt is going for", func(t *testing.T) {
		// A second attempt was committed for the item while the first run's
		// report was still coming: the conflict the report carries is recorded
		// as a suspicion and the edge is not added under the attempt that is
		// going (D2710).
		const later = "20261107-091000-ffff"
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		merged, mergedIt := foldMerged("wi-m", "20261107-085500-aaaa", now.Add(-20*time.Minute))
		second := orchestrator.Intent{
			ID:         orchestrator.IntentID("wi-a", 2),
			RunID:      later,
			SpecDigest: orchestrator.SpecDigest(w),
			ApprovalAt: approvedAt.UTC(),
			At:         now.Add(-5 * time.Minute).UTC(),
		}
		w.RunID = later
		w.Attempts = 2
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, merged}, []orchestrator.Intent{it, second, mergedIt}), now)
		foldOnly(h)
		h.tick.Footprints = orchestrator.Footprints{"wi-m": plan.NewFootprint([]string{"internal/orchestrator/**"})}
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeFailed, nil, nil, "internal/orchestrator/tick.go"),
			{RunID: later, Started: true},
		}

		h.step()
		got := h.state()
		a := foldItemOf(t, got, "wi-a")
		if len(a.DependsOn) != 0 {
			t.Errorf("the item waits for %v, and a second attempt for it is going", a.DependsOn)
		}
		if _, ok := got.Facts["graph_suspect_"+runID]; !ok {
			t.Errorf("the node carries the facts %v, and a conflict the daemon cannot place an edge for is still what the run reported", foldFactKeys(got.Facts))
		}
	})
}

// TestNeededBecomesGraphSuspect is D2709's note: what a run needed is one fact
// and nothing else — no edge, no phase, no reordering — even when the node
// really does carry the item the run named.
func TestNeededBecomesGraphSuspect(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	named := foldItem("wi-t", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T9"), waitsFor("wi-wait"))
	waiting := foldItem("wi-wait", orchestrator.PhaseQueued, approvedAt)
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, named, waiting}, []orchestrator.Intent{it}), now)
	foldOnly(h)
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, nil,
			[]runstore.DiscoveredItem{namesTask("plan-a/T9"), namesPath("internal/orchestrator/tick.go")}),
	}

	h.step()
	got := h.state()
	if len(got.Facts) != 1 {
		t.Fatalf("the node carries the facts %v, and a run that reported what it needed leaves one note", foldFactKeys(got.Facts))
	}
	fact, ok := got.Facts["graph_suspect_"+runID]
	if !ok {
		t.Fatalf("the node carries the facts %v, and the note is filed under the run that reported it", foldFactKeys(got.Facts))
	}
	if fact.By != orchestrator.Daemon {
		t.Errorf("the note is from %q, and the fold is the daemon's step", fact.By)
	}
	for _, name := range []string{runID, "wi-a", "plan-a/T9", "internal/orchestrator/tick.go"} {
		if !strings.Contains(fact.Value, name) {
			t.Errorf("the note reads %q, and it has to name the run, the item it was for and what the run said it needed (%q)", fact.Value, name)
		}
	}
	t9 := foldItemOf(t, got, "wi-t")
	if !slices.Equal(t9.DependsOn, []string{"wi-wait"}) {
		t.Errorf("the item the run said it needed waits for %v, and what a run needed reorders nothing", t9.DependsOn)
	}
	if t9.Approved == nil {
		t.Error("the note un-approved the item the run named, and a note is not a change to a declaration")
	}
}

// TestConflictEdgeOnNextAttempt is D2710's placement: the path a run met a
// conflict on becomes an edge in front of the run's own item for its next
// attempt, with the two facts a reader needs to make sense of it.
func TestConflictEdgeOnNextAttempt(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	merged, mergedIt := foldMerged("wi-m", "20261107-085500-aaaa", now.Add(-20*time.Minute))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, merged}, []orchestrator.Intent{it, mergedIt}), now)
	// The plan's own graph is what says which item a path is about (F33), and
	// the item merged while the attempt was going.
	h.tick.Footprints = orchestrator.Footprints{"wi-m": plan.NewFootprint([]string{"internal/orchestrator/**"})}
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeFailed, nil, nil, "internal/orchestrator/tick.go"),
	}

	rep := h.step()
	got := h.state()
	a := foldItemOf(t, got, "wi-a")
	if !slices.Equal(a.DependsOn, []string{"wi-m"}) {
		t.Errorf("the item waits for %v, and the run met a conflict on a file the merged item's footprint covers", a.DependsOn)
	}
	if a.Phase != orchestrator.PhaseFailed {
		t.Errorf("the item is %s after a failed run, and an added edge is not a phase", a.Phase)
	}
	if a.Approved != nil {
		t.Error("the item the daemon added an edge to still carries the approval of the spec it no longer is (D2740)")
	}
	if rep.Carried != 0 || len(h.launcher.asked) != 0 {
		t.Errorf("the cycle carried %d action(s) and started %v: the retry a person's approval covered does not fire on a declaration the daemon changed", rep.Carried, h.launcher.asked)
	}

	edge := foldEdgeFact(t, got)
	if edge.By != orchestrator.Daemon {
		t.Errorf("the fact is from %q, and the fold is the daemon's step", edge.By)
	}
	for _, name := range []string{"wi-a", "wi-m", runID} {
		if !strings.Contains(edge.Value, name) {
			t.Errorf("the fact reads %q, and D2740 asks it to name the item, the edge and the run (%q)", edge.Value, name)
		}
	}
	fact, ok := got.Facts["graph_suspect_"+runID]
	if !ok {
		t.Fatalf("the node carries the facts %v, and a conflict leaves the run's own note as well", foldFactKeys(got.Facts))
	}
	for _, name := range []string{"internal/orchestrator/tick.go", "wi-m"} {
		if !strings.Contains(fact.Value, name) {
			t.Errorf("the note reads %q, and it has to name the path the run met and the item the daemon read it as (%q)", fact.Value, name)
		}
	}
}

// TestFoldIsIdempotent is the level-triggered rule: a second cycle over the
// same report changes nothing, and an edge that is already there is left as it
// stands rather than added twice.
func TestFoldIsIdempotent(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	t.Run("a second cycle over one report writes nothing", func(t *testing.T) {
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it}), now)
		foldOnly(h)
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
		}

		h.step()
		first := h.state()
		rev := h.rev()
		second := h.step()
		if second.Folded != 0 {
			t.Errorf("the second cycle folded %d runs, and the attempt it folded had reported", second.Folded)
		}
		if got := h.rev(); got != rev {
			t.Errorf("the second cycle wrote the node (rev %d -> %d), and nothing about it changed", rev, got)
		}
		if b := foldItemOf(t, h.state(), "wi-b"); !slices.Equal(b.DependsOn, []string{"wi-a"}) {
			t.Errorf("the item waits for %v, and the edge is listed once", b.DependsOn)
		}
		if len(h.state().Facts) != len(first.Facts) {
			t.Errorf("the node carries the facts %v after a second cycle, and it carried %v after the first", foldFactKeys(h.state().Facts), foldFactKeys(first.Facts))
		}
	})

	t.Run("an edge the item already has is not added again", func(t *testing.T) {
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"), waitsFor("wi-a"))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it}), now)
		foldOnly(h)
		h.tick.Audit = mustAudit(t, rtdbClient(t, h.db), clockAt(now))
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
		}

		h.step()
		got := h.state()
		b := foldItemOf(t, got, "wi-b")
		if !slices.Equal(b.DependsOn, []string{"wi-a"}) {
			t.Errorf("the item waits for %v, and the edge the run asked for is one it already had", b.DependsOn)
		}
		if b.Approved == nil {
			t.Error("an edge the item already had un-approved it: nothing about its declaration changed")
		}
		if len(got.Facts) != 0 {
			t.Errorf("the node carries the facts %v for an edge that was already there", foldFactKeys(got.Facts))
		}
		if log := foldLog(t, h); len(log) != 0 {
			t.Errorf("the log carries %v, and an edge that was already there is not a refusal", logKeys(log))
		}
	})
}

// TestBlocksStayInTheRunsOwnNode is F44 and D2709's round-2 correction: an
// entry reaches the node the run's own item is in — that plan's node, or, for a
// run with no plan at all, the items of its own repository in that node — and
// nothing else.
func TestBlocksStayInTheRunsOwnNode(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	t.Run("a run of a plan reaches that plan's node and no other", func(t *testing.T) {
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute), planTask("plan-a/T1"))
		mine := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"))
		elsewhere := foldItem("wi-other", orchestrator.PhaseQueued, approvedAt, planTask("plan-b/T9"))

		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{elsewhere}, nil), now)
		foldOnly(h)
		planNode := h.withShard(t, orchestrator.PlanShard("plan-a"), tickState(t, now, []orchestrator.WorkItem{w, mine}, []orchestrator.Intent{it}))
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded,
				[]runstore.DiscoveredItem{namesTask("plan-a/T2"), namesTask("plan-b/T9")}, nil),
		}

		h.step()
		if b := foldItemOf(t, shardState(t, planNode), "wi-b"); !slices.Equal(b.DependsOn, []string{"wi-a"}) {
			t.Errorf("the item of the run's own plan waits for %v, and the run reported that it blocks it", b.DependsOn)
		}
		if o := foldItemOf(t, h.state(), "wi-other"); len(o.DependsOn) != 0 {
			t.Errorf("an item of another plan's node waits for %v, and a dependency never crosses nodes (F44)", o.DependsOn)
		}
	})

	t.Run("a run with no plan reaches its own repository's items", func(t *testing.T) {
		// The run's item stands for no plan task, so the entry names its target
		// by path, and the two candidates are the same file's footprint in two
		// repositories: what a path like go.mod may reach is the run's own
		// repository and nothing else (D2709, F51).
		w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
		same := foldItem("wi-same", orchestrator.PhaseQueued, approvedAt)
		other := foldItem("wi-diff", orchestrator.PhaseQueued, approvedAt, inRepo("owner/other"))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, same, other}, []orchestrator.Intent{it}), now)
		foldOnly(h)
		h.tick.Footprints = orchestrator.Footprints{
			"wi-same": plan.NewFootprint([]string{"go.mod"}),
			"wi-diff": plan.NewFootprint([]string{"go.mod"}),
		}
		h.reader.reports = []orchestrator.RunReport{
			foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesPath("go.mod")}, nil),
		}

		h.step()
		got := h.state()
		if s := foldItemOf(t, got, "wi-same"); !slices.Equal(s.DependsOn, []string{"wi-a"}) {
			t.Errorf("the item of the run's own repository waits for %v, and the run reported that it blocks it", s.DependsOn)
		}
		if d := foldItemOf(t, got, "wi-diff"); len(d.DependsOn) != 0 {
			t.Errorf("an item of another repository waits for %v, and one path may not reach every item of a project", d.DependsOn)
		}
	})
}

// TestOneGraphSuspectFactPerRun is D2709's correction after review round 2: a
// run leaves one note however many entries it carries, and two runs leave two.
func TestOneGraphSuspectFactPerRun(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"
	const otherRun = "20261107-090500-beef"

	a, itA := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	b, itB := foldRun("wi-b", otherRun, approvedAt, now.Add(-20*time.Minute))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{a, b}, []orchestrator.Intent{itA, itB}), now)
	foldOnly(h)
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, nil, []runstore.DiscoveredItem{
			namesTask("plan-b/T1"), namesTask("plan-b/T2"), namesPath("go.mod"),
		}),
		foldReport(otherRun, orchestrator.OutcomeFailed, nil, []runstore.DiscoveredItem{namesPath("docs/status.md")}),
	}

	h.step()
	got := h.state()
	if len(got.Facts) != 2 {
		t.Fatalf("the node carries the facts %v, and one run leaves one note however many entries it carries", foldFactKeys(got.Facts))
	}
	for _, run := range []string{runID, otherRun} {
		fact, ok := got.Facts["graph_suspect_"+run]
		if !ok {
			t.Fatalf("the node carries the facts %v, and the run %q leaves one of its own", foldFactKeys(got.Facts), run)
		}
		if !strings.Contains(fact.Value, run) {
			t.Errorf("the note reads %q, and it has to name the run it is about (%q)", fact.Value, run)
		}
	}
	if first := got.Facts["graph_suspect_"+runID].Value; !strings.Contains(first, "3 item(s)") {
		t.Errorf("the note reads %q, and how many entries the run needed is the part a person reads", first)
	}
}

// TestAddedEdgeUnapprovesWithFact is D2740's whole mechanism with the caps open:
// the edge makes the declaration another one, the approval goes with the one it
// covered, the fact says which item, which edge and which run, and the item a
// person approved is not one a run starts under.
func TestAddedEdgeUnapprovesWithFact(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const runID = "20261107-090000-abcd"

	w, it := foldRun("wi-a", runID, approvedAt, now.Add(-30*time.Minute))
	target := foldItem("wi-b", orchestrator.PhaseQueued, approvedAt, planTask("plan-a/T2"))
	approved := orchestrator.SpecDigest(target)
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w, target}, []orchestrator.Intent{it}), now)
	h.reader.reports = []orchestrator.RunReport{
		foldReport(runID, orchestrator.OutcomeSucceeded, []runstore.DiscoveredItem{namesTask("plan-a/T2")}, nil),
	}

	rep := h.step()
	got := h.state()
	b := foldItemOf(t, got, "wi-b")
	if b.Approved != nil {
		t.Error("the item the daemon added an edge to still carries the approval of a spec it no longer is: depends_on is digested and the person approved the declaration they read (D2740)")
	}
	if orchestrator.SpecDigest(b) == approved {
		t.Error("the declaration is the one the person approved, so there is nothing for them to read and approve again")
	}
	if rep.Carried != 0 || len(h.launcher.asked) != 0 {
		t.Errorf("the cycle carried %d action(s) and started %v, and an item the fold un-approved is not one a run starts under", rep.Carried, h.launcher.asked)
	}

	edge := foldEdgeFact(t, got)
	for _, name := range []string{"wi-b", "wi-a", runID} {
		if !strings.Contains(edge.Value, name) {
			t.Errorf("the fact reads %q, and it has to name the item, the edge and the run that caused it (%q)", edge.Value, name)
		}
	}
	if !strings.Contains(edge.Value, "approval") {
		t.Errorf("the fact reads %q, and what a person has to do about it is the point of the note", edge.Value)
	}
}
