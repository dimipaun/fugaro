package cli

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The money path: a daemon that dies mid-launch spends once (O8, D1231, F11).
//
// Every case here is the same shape: a crash is staged where the daemon really
// dies — the intent is committed by the tick's own starter, the task may be
// stored, the run may be started — and then a real tick runs over the state the
// crash left, with the real launcher, the real run store and the fake compute
// platform. What is pinned is that the recovery starts the attempt the state
// recorded and never a second run: the run id is the state's, so the launcher
// is asked for a run that may already exist, and `launchRun`'s own claim is
// what answers that.

// crashLauncher is the daemon dying where the test says: the real launcher's
// own steps run up to the crash point and the start does not happen.
type crashLauncher struct {
	real *orchestratorLauncher
	// stored is true for a crash after the task was stored and before the
	// start; false for one right after the intent, before anything was stored.
	stored bool
}

var errDaemonDied = errors.New("the daemon died here")

func (c crashLauncher) Launch(ctx context.Context, spec orchestrator.Spec, runID string) (string, error) {
	if c.stored {
		if _, _, err := c.real.prepare(ctx, spec, runID); err != nil {
			return "", err
		}
	}
	return "", errDaemonDied
}

// crashState is the node a crash left: one approved item, and whether the
// attempt it planned was already committed.
type crashState struct {
	item     orchestrator.WorkItem
	runID    string
	intentAt time.Time
}

// TestCrashBetweenIntentAndLaunchMakesOneRun: a daemon that dies between the
// intent and the launch, between the task and the start, or right after the
// start leaves one run and one attempt behind — never two, and never an item
// stuck running with no run under it. Each crash is followed by one real tick
// past the claim window, which is the tick a restarted daemon runs.
func TestCrashBetweenIntentAndLaunchMakesOneRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		// stored is true when the crash happened after task.json was written.
		stored bool
		// started is true when the crash happened after the run was started.
		started bool
	}{
		{name: "after the intent"},
		{name: "after the task and before the start", stored: true},
		{name: "after the start", stored: true, started: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, d, cs := crashDaemon(t, tc.stored, tc.started)

			// The state the crash left: the attempt is recorded under its own
			// run id, and the item names that run — an item running with no run
			// behind it is the one thing a crash must not leave, and the
			// recovery below is checked for exactly that.
			if cs.item.RunID != cs.runID {
				t.Fatalf("the state records run %q and the crash recorded %q", cs.item.RunID, cs.runID)
			}

			f.recover(t, d, cs)

			execs := f.run.Executions()
			if len(execs) != 1 {
				t.Fatalf("the recovery started %d execution(s), want exactly one: %v", len(execs), execs)
			}
			// One run for the recorded id, and its launch is on record: the
			// item is waiting on a run that exists rather than on one nobody
			// started.
			if _, err := f.runStore(t, cs.runID).ReadLaunch(context.Background()); err != nil {
				t.Fatalf("the run the state recorded was not started: %v", err)
			}
			after := f.item(t, cs.item.ID)
			if after.Phase == orchestrator.PhaseRunning && after.RunID != cs.runID {
				t.Errorf("the item is running under %q, and the crash recorded %q", after.RunID, cs.runID)
			}
			if after.RunID != cs.runID {
				t.Errorf("the item no longer names the run the attempt was recorded under: %q", after.RunID)
			}
			if after.Phase == orchestrator.PhaseBlocked {
				t.Errorf("the recovery abandoned an attempt that was only ever waiting for its run: %q", after.Note)
			}
		})
	}
}

// TestCrashAfterTaskJSONBeforeTheStartStillStartsOnce is the middle crash on
// its own, because it is the one that could spend twice: the task exists, so a
// second launch would either store a second task or start a second execution.
// It stores neither — the task the crash left is the task the attempt carries
// — and the run that starts is the one the state recorded.
func TestCrashAfterTaskJSONBeforeTheStartStillStartsOnce(t *testing.T) {
	f, d, cs := crashDaemon(t, true, false)
	s := f.runStore(t, cs.runID)
	before, err := s.ReadTask(context.Background())
	if err != nil {
		t.Fatalf("the task the crash stored: %v", err)
	}

	f.recover(t, d, cs)

	after, err := s.ReadTask(context.Background())
	if err != nil {
		t.Fatalf("the task after the recovery: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("the recovery rewrote the task the crash left:\nafter  %+v\nbefore %+v", after, before)
	}
	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the recovery started %d execution(s), want exactly one: %v", len(execs), execs)
	}
	if _, err := s.ReadLaunch(context.Background()); err != nil {
		t.Fatalf("the recovery did not start the run: %v", err)
	}
}

// TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent: a person edited the
// item after the crash and before the task existed, so the spec the intent was
// committed for is not the item's any more. Nothing may start under it: the
// attempt is abandoned, the item is blocked with the reason on it, and no run
// is started under the run id the intent recorded.
func TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent(t *testing.T) {
	f, d, cs := crashDaemon(t, false, false)
	edited := f.item(t, cs.item.ID)
	edited.Brief = "A different thing entirely, since the attempt was committed."
	f.putItem(t, edited)

	f.recover(t, d, cs)

	after := f.item(t, cs.item.ID)
	if after.Phase != orchestrator.PhaseBlocked {
		t.Fatalf("the item is %s after an attempt whose spec moved under it, want blocked: %+v", after.Phase, after)
	}
	if !strings.Contains(after.Note, "the item's spec is not the one the intent was committed for") {
		t.Errorf("the item does not say why it was abandoned: %q", after.Note)
	}
	it := f.intent(t, orchestrator.IntentID(cs.item.ID, 1))
	if it.Outcome != orchestrator.OutcomeAbandoned {
		t.Errorf("the abandoned attempt carries the outcome %q, want abandoned", it.Outcome)
	}
	if execs := f.run.Executions(); len(execs) != 0 {
		t.Errorf("an attempt whose spec moved started %d execution(s): %v", len(execs), execs)
	}
	if _, err := f.runStore(t, cs.runID).ReadLaunch(context.Background()); err == nil {
		t.Error("a run was started under the abandoned attempt's run id")
	}
}

// TestRecoveryBeforeTaskJSONOfASpentRetryStartsOnce: the crash is a retry's, so
// the approval it spent is gone from the item and only the spent marker names
// it. The attempt is still the item's own open one, so the recovery re-issues
// it — a person's approval is not needed for an attempt already committed —
// and the run is started once under the run id the intent recorded.
func TestRecoveryBeforeTaskJSONOfASpentRetryStartsOnce(t *testing.T) {
	f, d, cs := crashDaemon(t, false, false)
	// What the retry's own write left: the approval is spent, the marker names
	// it, the attempt count is the retry's, and the intent was committed under
	// the spent approval before the daemon died.
	first := f.item(t, cs.item.ID)
	approvalAt := first.Approved.At
	retry := first
	retry.Approved = nil
	retry.SpentApprovalAt = &approvalAt
	retry.Attempts = 2
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{retry},
		Intents: []orchestrator.Intent{
			{ID: orchestrator.IntentID(retry.ID, 1), RunID: "20261109-101500-0001", SpecDigest: specDigest(retry), ApprovalAt: approvalAt, At: cs.intentAt.Add(-time.Hour), Outcome: orchestrator.OutcomeFailed},
			{ID: orchestrator.IntentID(retry.ID, 2), RunID: cs.runID, SpecDigest: specDigest(retry), ApprovalAt: approvalAt, At: cs.intentAt},
		},
	})

	f.recover(t, d, cs)

	if execs := f.run.Executions(); len(execs) != 1 {
		t.Fatalf("the recovery of a crashed retry started %d execution(s), want exactly one: %v", len(execs), execs)
	}
	if _, err := f.runStore(t, cs.runID).ReadLaunch(context.Background()); err != nil {
		t.Fatalf("the crashed retry was not started: %v", err)
	}
	it := f.intent(t, orchestrator.IntentID(retry.ID, 2))
	if it.Outcome != "" {
		t.Errorf("the attempt was closed with %q where it should be waiting on the run it started", it.Outcome)
	}
}

// crash stages one daemon that dies at the point the caller names and returns
// the fixture, the daemon and what the node records afterwards: the item, the
// run id the attempt was recorded under, and when the intent was stamped.
//
// The crash is the real one: the tick commits the intent through its own
// starter and then asks a launcher that dies where a crash would, so what the
// recovery reads is what a killed daemon really leaves.
func crashDaemon(t *testing.T, stored, started bool) (*daemonFixture, *orchestratorDaemon, crashState) {
	t.Helper()
	f := newDaemonFixture(t)
	d, _ := f.running(t, "")
	now := f.serverNow(t)
	f.now = now
	item := approvedItem("wi-1", now)
	f.putState(t, orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora", Role: orchestrator.RoleOrchestrator,
		Goal: "keep the work moving", WorkItems: []orchestrator.WorkItem{item},
	})
	launcher := f.launcher(t)
	switch {
	case started:
		d.tick.Launcher = launcher
	default:
		d.tick.Launcher = crashLauncher{real: launcher, stored: stored}
	}
	// The crash itself, and the state that survives it.
	if _, err := d.tick.Step(context.Background()); !errors.Is(err, errDaemonDied) && err != nil {
		t.Fatalf("the staged crash reported %v", err)
	}
	state := f.state(t)
	it := f.intent(t, orchestrator.IntentID(item.ID, 1))
	if it.RunID == "" {
		t.Fatalf("the crash left no attempt recorded: %+v", state)
	}
	if !started && stored {
		if _, err := f.runStore(t, it.RunID).ReadTask(context.Background()); err != nil {
			t.Fatalf("the crash was staged after the task and left none: %v", err)
		}
	}
	return f, d, crashState{item: f.item(t, item.ID), runID: it.RunID, intentAt: it.At}
}

// recover is the restarted daemon's tick: the real launcher, and a clock past
// the claim window so recovery acts on the attempt the crash left. The lease is
// beaten first because the clock moved, and a lease stamped before it is one
// Node.Save refuses.
func (f *daemonFixture) recover(t *testing.T, d *orchestratorDaemon, cs crashState) {
	t.Helper()
	ctx := context.Background()
	f.db.SetClock(func() time.Time { return f.now.Add(orchestrator.ClaimTTL + time.Minute) })
	if err := d.lease.Beat(ctx); err != nil {
		t.Fatalf("beating the lease again after the clock moved: %v", err)
	}
	d.tick.Launcher = f.launcher(t)
	rep, err := d.tick.Step(ctx)
	if err != nil {
		t.Fatalf("the tick after the crash: %v", err)
	}
	if rep.Outcome == orchestrator.CycleEmpty && rep.Carried == 0 {
		t.Fatalf("the recovery did nothing at all over the attempt the crash left: %+v", rep)
	}
}

// approvedItem is one work item a person has approved: the state every launch
// in this file starts from.
func approvedItem(id string, at time.Time) orchestrator.WorkItem {
	w := orchestrator.WorkItem{
		ID: id, Title: "land the daemon", Repo: "acme/app", Recipe: "standard", RigorTier: "medium",
		Brief: "Start the run the attempt records.", Phase: orchestrator.PhaseQueued,
	}
	w.Approved = &orchestrator.Approval{By: "owner", At: at.Add(-time.Minute), SpecDigest: specDigest(w), Attempts: orchestrator.ApprovalAttempts}
	return w
}

// specDigest is the digest an approval's own item carries: what the item
// carries is what a person approved, and an approval that pins another one is
// one no attempt may start under.
func specDigest(w orchestrator.WorkItem) orchestrator.Digest { return orchestrator.SpecDigest(w) }

// serverNow is the fixture database's clock as a client reads it, which is the
// only clock a stamp under the state node comes from (D1230).
func (f *daemonFixture) serverNow(t *testing.T) time.Time {
	t.Helper()
	_, db, err := openBudget(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now, ok := db.ServerNow()
	if !ok {
		t.Fatal("the fixture database has no server time")
	}
	return now.UTC().Truncate(time.Millisecond)
}

// putState writes one state at the node the way this release would: encoded
// and validated, so a test's own state is held to the same rules a daemon's is.
func (f *daemonFixture) putState(t *testing.T, s orchestrator.State) {
	t.Helper()
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the state this test seeded is not one a daemon could read: %v", err)
	}
	f.db.Set(f.statePath(t), json.RawMessage(raw))
}

// state is the node as it stands.
func (f *daemonFixture) state(t *testing.T) orchestrator.State {
	t.Helper()
	lc, db, err := openBudget(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = lc
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: "aurora", Role: orchestrator.RoleOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the state node: %v", err)
	}
	return s
}

// item is one item of the node, and a failure when it is not there at all.
func (f *daemonFixture) item(t *testing.T, id string) orchestrator.WorkItem {
	t.Helper()
	w, ok := f.state(t).Item(id)
	if !ok {
		t.Fatalf("the node carries no work item %q", id)
	}
	return w
}

// intent is one intent of the node, and a failure when it is not there at all.
func (f *daemonFixture) intent(t *testing.T, id string) orchestrator.Intent {
	t.Helper()
	for _, it := range f.state(t).Intents {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("the node carries no intent %q", id)
	return orchestrator.Intent{}
}

// putItem replaces one item at the node, leaving the rest of the state alone.
func (f *daemonFixture) putItem(t *testing.T, w orchestrator.WorkItem) {
	t.Helper()
	s := f.state(t)
	s.WorkItems = replaceItem(s.WorkItems, w)
	f.putState(t, s)
}

func replaceItem(items []orchestrator.WorkItem, w orchestrator.WorkItem) []orchestrator.WorkItem {
	out := make([]orchestrator.WorkItem, 0, len(items))
	for _, it := range items {
		if it.ID == w.ID {
			out = append(out, w)
			continue
		}
		out = append(out, it)
	}
	return out
}
