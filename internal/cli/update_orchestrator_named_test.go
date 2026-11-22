package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// update_orchestrator_named_test.go is CG2 of docs/console-fixes: `fugaro
// update` writes nothing to an orchestrator node, a named orchestrator's
// included, and it is the whole upgrade over an installation that holds them
// while a run is in flight (D3218 as amended, F35, F36).
//
// The state and the lease are decoded strictly, so a `project`, `name` or
// `driver` field this release wrote into either would be a node every member
// still on the release behind — and every resident daemon whose image the
// update has not rebuilt — refuses as its own. D3218 as first written would
// have had the update write exactly those; as amended the update writes none of
// them, the named orchestrators this plan adds are keyed by their id alone, and
// the driver node beside the state is the lease holder's to write and nobody
// else's. What makes that a tested claim rather than a sentence is this file:
// the subtree as the release behind left it, byte for byte, after the update.

// namedOrchestratorID is one named orchestrator of the fixture's project: the
// project's name, the separator and a slug (D3207). The project's own name is
// the default orchestrator's id, which is what `fugaro update` installs the
// resident daemon's unit for, and it is the id beside it here.
var namedOrchestratorID = orchestratorTestID + orchestrator.NameSeparator + "night"

// TestUpdateNamedMigrationWithRunInFlight: an installation one release behind,
// holding the default orchestrator's nodes and a named one's, updated while a
// run of its own repository is in flight. The update converges, the run goes
// on, and the orchestrator subtree it found is the subtree it leaves — there is
// no migration to run, which is the whole of what D3218 as amended asks of it.
//
// Mutation: have the update write the driver node, a `project` or `name` field
// into a state, or a node keyed by anything but the id, and the subtree
// assertion below fails; internal/orchestrator's TestPreviousReleaseReadsNewNodes
// is the other half, that the node such a write produces is one the release
// behind refuses.
func TestUpdateNamedMigrationWithRunInFlight(t *testing.T) {
	r := newConvergeRig(t, v3Node(nil))
	// The deployed rules are the release behind's own document, not a stand-in:
	// this installation is one release old in every way the update reads.
	r.db.SetRules(previousRulesDocument(t))

	// The nodes the release behind left, both orchestrators of the project: the
	// state, the lease, and the driver node beside them.
	for _, id := range []string{orchestratorTestID, namedOrchestratorID} {
		seedUpdateOrchestratorNodes(t, r.db, id)
	}
	before := orchestratorSubtree(t, r.db)
	if !strings.Contains(before, namedOrchestratorID) {
		t.Fatalf("the fixture holds no node of the named orchestrator, so nothing below is a reading of one:\n%s", before)
	}

	run := startRunInFlight(t, r)

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	// The note is the owner's requirement of 2026-11-09, read over an
	// installation that also holds a named orchestrator's nodes: the update
	// waits for no run and changes nothing one holds.
	line := inFlightLine(t, out)
	for _, want := range []string{"1 run in flight", "waits for none", "changes nothing they hold"} {
		if !strings.Contains(line, want) {
			t.Errorf("the update's note does not say %q over an installation holding a named orchestrator's nodes: %q", want, line)
		}
	}

	// What it wrote to the subtree: nothing. Every node the release behind left
	// is there byte for byte, and none this release writes has appeared — the
	// driver node included, which is the lease holder's write and not the
	// update's.
	after := orchestratorSubtree(t, r.db)
	if !infra.SameJSON([]byte(before), []byte(after)) {
		t.Errorf("fugaro update changed the orchestrator subtree. D3218 as amended is that it writes nothing to an orchestrator node: a member still on the release behind, or a resident daemon whose image it has not rebuilt, refuses a node that carries a field it does not model (F35)\nbefore: %s\nafter:  %s",
			before, after)
	}

	// And the run the update found in flight goes on: its lease, its ledger,
	// its counters, its outcome and its record all still land.
	run.laterWrites(t, r)
}

// seedUpdateOrchestratorNodes writes one orchestrator's three nodes as the
// release behind leaves them.
//
// The state and the driver go in through this release's own shapes, and the
// lease as the daemon writes it. The named id's paths are built here rather
// than by PathState, PathLease and PathDriver, which refuse a named id until
// CG1a widens the id rule (F36); an id is a key of the subtree and nothing
// escapes it (D3207), so the two spellings are one path.
func seedUpdateOrchestratorNodes(t *testing.T, db *gcpfake.RTDB, id string) {
	t.Helper()
	state, err := json.Marshal(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       id,
		Role:          orchestrator.RoleOrchestrator,
	})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := json.Marshal(orchestrator.Driver{Principal: orchestratorTestPrincipal, At: orchestratorTestNow})
	if err != nil {
		t.Fatal(err)
	}
	db.Set(updateOrchestratorPath(t, id, orchestrator.StateNode), json.RawMessage(state))
	db.Set(updateOrchestratorPath(t, id, orchestrator.DriverNode), json.RawMessage(driver))
	db.Set(updateOrchestratorPath(t, id, orchestrator.LeaseNode), map[string]any{
		"holder": "20261122-080654-abcd",
		"at":     orchestratorTestNow.UTC().Format(time.RFC3339),
	})
}

// orchestratorTestPrincipal is the credential the fixture's orchestrators are
// driven by, in the address spelling the driver node carries (F18).
const orchestratorTestPrincipal = "owner@example.invalid"

// updateOrchestratorPath is the path of one node of an orchestrator id: the
// subtree's own root, the id escaped with the tree's one escaper, and the
// node's name — what PathState, PathLease and PathDriver build.
func updateOrchestratorPath(t *testing.T, id, node string) string {
	t.Helper()
	return orchestrator.SubtreeRoot + "/" + budget.Key(id) + "/" + node
}

// orchestratorSubtree is everything the database holds under `orchestrator/`,
// as one document: the reading the assertion above diffs before and after the
// update.
func orchestratorSubtree(t *testing.T, db *gcpfake.RTDB) string {
	t.Helper()
	v := db.Value(orchestrator.SubtreeRoot)
	if v == nil {
		return "{}"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
