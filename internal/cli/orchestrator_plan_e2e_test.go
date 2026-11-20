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
// the node it names, and the real merge reader over the label route answering
// the tick that the pull request has merged. Each of those is a thing that can
// be right in its own package and still be unwired here.

import (
	"encoding/json"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

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
