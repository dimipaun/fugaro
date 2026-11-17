package orchestrator_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The resident pass's read of the node's own launch requests (mobile plan task
// P5.1d). The launch itself is internal/cli's one path — the launcher header
// there, F11/D931/T2 — so what these tests hold is the SPLIT:
// orchestrator.ResidentRequests is the launch requests the node's record
// already carries (the re-issues), and it is what a pass hands to the starter
// the daemon's cycle already uses.

// residentRun is the run id these tests launch: one shape of the ids
// internal/task mints, and the same one the intent of a re-issued attempt
// carries.
const residentRun = "20261117-050000-abcd"

// TestResidentRequestsCarryOutANodesOwnReissue is the read end to end and the
// half of the design that says what it is for: the attempt is already on the
// node — a person's patch committed it — and what is left is to start it. The
// starter a resident pass holds carries a launcher and NOTHING else, which is
// the same sentence as "this process writes no node", and the run id is the one
// the intent recorded and never a second one.
func TestResidentRequestsCarryOutANodesOwnReissue(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := o6OpenItem("wi-1", residentRun, approvedAt, now.Add(-orchestrator.ClaimTTL))
	s := o6Node(t, now, w)
	s.Intents = []orchestrator.Intent{it}

	requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, o6Caps(), now)
	if len(others) != 0 {
		t.Fatalf("the node's own attempt was read as %+v beside the re-issue: an attempt committed and not started is a launch request", others)
	}
	a := onlyAction(t, requests)
	if a.Kind != orchestrator.ActionReissue || a.RunID != residentRun || a.IntentID != it.ID {
		t.Fatalf("the pass reads %+v, not a re-issue of %q under %q", a, it.ID, residentRun)
	}

	l := &o6Launcher{}
	if _, err := (orchestrator.Starter{Launcher: l}).Start(context.Background(), s, a, now); err != nil {
		t.Fatalf("the node's own re-issue was refused: %v", err)
	}
	if !slices.Equal(l.asked, []string{residentRun}) {
		t.Fatalf("the launcher was asked for %v, and the node records the attempt under %q: a re-issue is the same launch and never a second one", l.asked, residentRun)
	}
	if len(l.specs) != 1 || l.specs[0] != orchestrator.SpecOf(w) {
		t.Errorf("the launcher was handed %+v, and the node holds %+v: what a launcher starts is the spec the state carries", l.specs, orchestrator.SpecOf(w))
	}

	// And the identity of a start is the state's record: a launcher that comes
	// back with another run id is a run the state does not carry.
	against := &o6Launcher{wrongID: "20261117-050000-ffff"}
	if _, err := (orchestrator.Starter{Launcher: against}).Start(context.Background(), s, a, now); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a launcher that answered %q for the attempt recorded as %q was accepted: %v", against.wrongID, residentRun, err)
	}
}

// TestResidentRequestsAreTheNodesOwnLaunchRequests is the split a resident pass
// acts on: the launch requests the node's own record already carries are the
// re-issue's, everything else a plan derives is a write's — and the ceilings a
// pass cannot read do not bound a re-issue, because it starts the attempt that
// was already committed and counted. The budget switch is the exception, and it
// is not a ceiling: it stops the re-issues too, which is the one thing a pass
// has to read rather than assume.
func TestResidentRequestsAreTheNodesOwnLaunchRequests(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	w, it := o6OpenItem("wi-1", residentRun, approvedAt, now.Add(-time.Minute))
	s := o6Node(t, now, w)
	s.Intents = []orchestrator.Intent{it}

	// Inside the claim window the attempt is the platform's: a launch beside it
	// may still be starting, so there is nothing for this pass to read.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, o6Caps(), now); len(requests)+len(others) != 0 {
		t.Fatalf("the pass read %+v / %+v inside the attempt's claim window", requests, others)
	}

	// Past the window the request is the re-issue, and the ceilings a pass
	// cannot read as anything but zero do not bound it: what recovery re-issues
	// was committed and counted already. The SWITCH is not one of those zeros —
	// the field's zero value reads as OFF, which is the permissive direction and
	// the reason Caps' doc states the rule the other way round ("a switch it
	// could not read is on") — so every call below writes it: a pass hands in
	// what it read, and a switch it could not read is handed in as true.
	later := now.Add(orchestrator.ClaimTTL)
	requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: false}, later)
	a := onlyAction(t, requests)
	if a.Kind != orchestrator.ActionReissue || a.RunID != residentRun || a.IntentID != it.ID {
		t.Fatalf("the pass read %+v under the zero ceilings, not the re-issue of %q", a, it.ID)
	}
	if len(others) != 0 {
		t.Fatalf("the pass left %+v beside the node's own re-issue", others)
	}

	// And the switch DOES bound it, which is the one rung no ceiling reaches:
	// planNode's first line turns the whole node off, recovery included, so what
	// a pass reads under a switch that is on is nothing at all — and that is the
	// value a pass whose read FAILED hands in, which is why a `fugaro budget
	// kill` stops the re-issues and not only the first launches.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: true}, later); len(requests)+len(others) != 0 {
		t.Fatalf("the pass planned %+v / %+v under a switch that is on, and the switch stops every request a pass reads", requests, others)
	}

	// A run that started is in flight however long it takes, and no window
	// makes it a second launch.
	if requests, others := orchestrator.ResidentRequests(s, orchestrator.RunView{Runs: []orchestrator.Run{{RunID: residentRun, Started: true}}}, o6Caps(), later); len(requests)+len(others) != 0 {
		t.Fatalf("the pass planned %+v / %+v for a run that had started", requests, others)
	}

	// An item a person approved and nothing has started yet is a WRITE: the
	// first launch is the attempt's own recording, and the ceilings a pass
	// cannot read — every one of them zero, with the switch read as off — plan
	// none of it.
	fresh := o6Node(t, now, o6Item("wi-2", orchestrator.PhaseQueued, approvedAt))
	requests, others = orchestrator.ResidentRequests(fresh, orchestrator.RunView{}, o6Caps(), now)
	if a := onlyAction(t, others); a.Kind != orchestrator.ActionLaunch {
		t.Fatalf("the plan left %+v for an approved item, not the first launch", a)
	}
	if len(requests) != 0 {
		t.Fatalf("the pass read %+v as a launch request of the node, and nothing committed that attempt", requests)
	}
	if requests, others := orchestrator.ResidentRequests(fresh, orchestrator.RunView{}, orchestrator.Caps{KillSwitch: false}, now); len(requests)+len(others) != 0 {
		t.Fatalf("the zero ceilings planned %+v / %+v, and a zero cap is no room at all", requests, others)
	}

	// An attempt whose spec a person edited after the intent was committed is
	// an abandon, which is a write as well: it closes the intent rather than
	// starting anything.
	edited := o6Edit(t, s, "wi-1", "A different brief.", now)
	requests, others = orchestrator.ResidentRequests(edited, orchestrator.RunView{}, o6Caps(), later)
	if a := onlyAction(t, others); a.Kind != orchestrator.ActionAbandon {
		t.Fatalf("the plan left %+v for an edited attempt, not the abandon", a)
	}
	if len(requests) != 0 {
		t.Fatalf("the pass read %+v as a launch request, and nothing may start under the abandoned intent", requests)
	}
}
