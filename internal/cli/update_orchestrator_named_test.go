package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// update_orchestrator_named_test.go is CG2 of docs/console-fixes: `fugaro
// update` needs no orchestrator migration, a named orchestrator's included, and
// it is the whole upgrade over an installation that holds them while a run is
// in flight (D3218 as amended, F35, F36).
//
// The state and the lease are decoded strictly, so a `project`, `name` or
// `driver` field this release wrote into either would be a node every member
// still on the release behind — and every resident daemon whose image the
// update has not rebuilt — refuses as its own. D3218 as first written would
// have had the update write exactly those; as amended the update writes none of
// them, the named orchestrators this plan adds are keyed by their id alone, and
// the driver node beside the state is the lease holder's to write and nobody
// else's. What makes that a tested claim rather than a sentence is this file:
// the subtree as the release behind left it, after the update, with the one
// write the update does make into it — the fold of the checkout's docs/backlog/
// folder into the standing backlog (D2716, update_backlog.go) — named, held to
// the release behind's own decoder, and shown to be the whole of it.
//
// The fixture's checkout holds an open backlog item for exactly that reason: a
// checkout with no docs/backlog/ folds nothing, and a subtree that came out
// unchanged would then say nothing about the write this release's node shapes
// reach the release behind through.

// namedOrchestratorID is one named orchestrator of the fixture's project: the
// project's name, the separator and a slug (D3207). The project's own name is
// the default orchestrator's id, which is what `fugaro update` installs the
// resident daemon's unit for, and it is the id beside it here.
var namedOrchestratorID = orchestratorTestID + orchestrator.NameSeparator + "night"

// prevStateSchemaPath is the release behind's own strict decoder for an
// orchestrator node's document, frozen where internal/orchestrator's
// compatibility test keeps it: the state, the standing backlog's node and a
// plan's are one document (D2735), so the shape the release behind reads the
// state by is the shape it reads the backlog node by — the node `fugaro update`
// writes (F35).
const prevStateSchemaPath = "../orchestrator/testdata/prev-release/state.schema.json"

// compilePrevStateSchema reads and compiles the frozen schema. It is called
// before the fixture chdirs into its checkout (newConvergeRig), because the
// path above is this package's own.
func compilePrevStateSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(prevStateSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatalf("%s has no $id", prevStateSchemaPath)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// TestUpdateNamedMigrationWithRunInFlight: an installation one release behind,
// holding the default orchestrator's nodes and a named one's, updated while a
// run of its own repository is in flight. The update converges, the run goes
// on, and the orchestrator subtree it found is the subtree it leaves with one
// node added: the standing backlog's, folded from the checkout's folder
// (D2716). No node the release behind left moves, no node appears under the
// named orchestrator, and the one the update wrote is a document the release
// behind reads.
//
// Mutation: have the update write the driver node, a `project` or `name` field
// into a state, or a node keyed by anything but the id, and the subtree
// assertion below fails; internal/orchestrator's TestPreviousReleaseReadsNewNodes
// is the other half, that the node such a write produces is one the release
// behind refuses.
func TestUpdateNamedMigrationWithRunInFlight(t *testing.T) {
	// The release behind's decoder, before the fixture chdirs into its checkout.
	prevState := compilePrevStateSchema(t)

	r := newConvergeRig(t, v3Node(nil))
	// The deployed rules are the release behind's own document, not a stand-in:
	// this installation is one release old in every way the update reads.
	r.db.SetRules(previousRulesDocument(t))

	// The checkout's open item, which is what makes the update write anything
	// into the subtree at all: docs/backlog/ is folded into the standing backlog
	// by the update alone (D2716), and the fold is the one write into an
	// orchestrator node that `fugaro update` makes.
	backlogItemFile(t, r.root, "20261021-01", "one", "the first thing left open", "open")

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

	out := convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "repo-plugin-pin"})

	// The note is the owner's requirement of 2026-11-09, read over an
	// installation that also holds a named orchestrator's nodes: the update
	// waits for no run and changes nothing one holds.
	line := inFlightLine(t, out)
	for _, want := range []string{"1 run in flight", "waits for none", "changes nothing they hold"} {
		if !strings.Contains(line, want) {
			t.Errorf("the update's note does not say %q over an installation holding a named orchestrator's nodes: %q", want, line)
		}
	}

	// What it wrote to the subtree: the backlog node, and nothing else. The fold
	// is the update's one write into an orchestrator node (D2716) and it is the
	// default orchestrator's — the project's own name, because the folder is the
	// checkout's and a checkout names one project — so the named orchestrator
	// gains no node at all.
	after := orchestratorSubtree(t, r.db)
	beforeTree, afterTree := objectOf(t, before), objectOf(t, after)
	beforeDefault, _ := beforeTree[orchestratorTestID].(map[string]any)
	afterDefault, _ := afterTree[orchestratorTestID].(map[string]any)
	if beforeDefault == nil || afterDefault == nil {
		t.Fatalf("the subtree does not hold %s's nodes, so nothing below is a reading of them:\nbefore: %s\nafter:  %s", orchestratorTestID, before, after)
	}
	if _, ok := beforeDefault[orchestrator.BacklogNode]; ok {
		t.Fatalf("the fixture holds a backlog node already, so the node below is not the update's own write:\n%s", before)
	}
	if _, ok := afterDefault[orchestrator.BacklogNode]; !ok {
		t.Fatalf("fugaro update folded nothing into %s's backlog node, so the subtree below is unchanged for a reason other than the one it claims: the checkout's %s holds an open item, and the fold is the update's one write into an orchestrator node (D2716)\n%s", orchestratorTestID, backlog.Dir, after)
	}
	// The document it wrote is one the release behind reads. The backlog node is
	// a state document through and through (D2735), so the release behind's
	// strict decoder for the state is its decoder for this node too, and a field
	// this release added to either is refused here exactly as it is there (F35).
	// This is the update's own write path, which is the one a field added to the
	// state would reach a release behind through first.
	folded := storedNode(t, r.db, updateOrchestratorPath(t, orchestratorTestID, orchestrator.BacklogNode))
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(folded))
	if err != nil {
		t.Fatalf("the backlog node this update wrote is not JSON: %v\n%s", err, folded)
	}
	obj, ok := inst.(map[string]any)
	if !ok {
		t.Fatalf("the backlog node this update wrote is not a JSON object:\n%s", folded)
	}
	// The database stores no empty object, so the `facts` the writer emits as an
	// empty map is absent from the node as the tree holds it — the state node a
	// daemon writes is stored the same way, and `facts` is the state's one map.
	// The release behind's reader refuses a field it does not model and reads a
	// missing one as the zero value, so restoring the empty object is the
	// document the writer emitted and cannot hide an unknown field, which is the
	// whole of what is asserted here (F35).
	if _, has := obj["facts"]; !has {
		obj["facts"] = map[string]any{}
	}
	if err := prevState.Validate(inst); err != nil {
		t.Errorf("the release behind refuses the backlog node this update wrote — the one node `fugaro update` writes into an orchestrator subtree — so a member still on it, or a resident daemon whose image the update has not rebuilt, refuses the project's own backlog (F35): %v\n%s", err, folded)
	}
	// Take the fold's own node out and the two subtrees are one document: the
	// subtree the update leaves is the subtree it found with that one node
	// added, so every node the release behind left is there byte for byte and
	// none this release writes has appeared beside it — the driver node
	// included, which is the lease holder's write and not the update's. The
	// claim is about what the update does write, not about an update that
	// writes nothing: the fold's node is that write (D2716), and D3218 as
	// amended is that no other node under `orchestrator/` moves for a release
	// behind to refuse (F35).
	delete(afterDefault, orchestrator.BacklogNode)
	if !reflect.DeepEqual(beforeTree, afterTree) {
		t.Errorf("fugaro update changed an orchestrator node other than the one it folds, %s's backlog node. The subtree it leaves is the subtree it found with that one node added, and D3218 as amended is that no second node of it moves: a member still on the release behind, or a resident daemon whose image it has not rebuilt, refuses a node that carries a field it does not model (F35)\nbefore: %s\nafter:  %s",
			orchestratorTestID, before, after)
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
// node's name — what PathState, PathLease, PathDriver and PathBacklog build.
func updateOrchestratorPath(t *testing.T, id, node string) string {
	t.Helper()
	return orchestrator.SubtreeRoot + "/" + budget.Key(id) + "/" + node
}

// orchestratorSubtree is everything the database holds under `orchestrator/`,
// as one document: the reading the assertions above diff before and after the
// update, and take the fold's own node out of.
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

// storedNode is one node as the database holds it: the fake's tree read back
// and marshalled, so the assertion above holds the document a reader of the
// release behind would be handed rather than the value this test meant to
// write.
func storedNode(t *testing.T, db *gcpfake.RTDB, path string) []byte {
	t.Helper()
	v := db.Value(path)
	if v == nil {
		t.Fatalf("no node at %s", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// objectOf is a subtree as the tree the database holds, for the assertions
// about which of its nodes moved.
func objectOf(t *testing.T, doc string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(doc), &obj); err != nil {
		t.Fatalf("the subtree is not a JSON object: %v", err)
	}
	return obj
}
