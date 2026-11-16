package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The fixture's two items, and the runs their ledgers name. The ids are run id
// shaped, because the state refuses anything else as a run id (`task.ValidRunID`).
const (
	escalationItemID  = "wi-1"
	escalationOtherID = "wi-2"
	escalationIdleID  = "wi-3"
	escalationRunID   = "20261109-080000-0001"
	escalationGoneRun = "20261109-093000-0002"
)

// escalationItemOp is one work item as the ladder leaves it: the declaration,
// the run it is on and its ledger. The ledger is the daemon's alone to write
// (G12), so the actor seeding one is the daemon and an op carrying an
// escalation from any other class would be refused.
func escalationItemOp(id, runID string, e orchestrator.Escalation) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
		ID: id, Title: strPtr("work that stalled"), Repo: strPtr("acme/repo"), Recipe: strPtr("code"),
		RigorTier: strPtr("small"), Brief: strPtr("make the change " + id),
		RunID: strPtr(runID), Escalation: &e,
	}}
}

// escalationLedger is one item's ledger: the rung it is at, the ladder runs it
// has spent and its transitions, oldest first. The time it entered the rung is
// the last transition's own, which is what makes the block one thing rather
// than two (`validateEscalation`).
func escalationLedger(rung escalation.Rung, ran int, trs ...orchestrator.Transition) orchestrator.Escalation {
	return orchestrator.Escalation{Rung: rung, EnteredAt: trs[len(trs)-1].At, OuterAttempts: ran, Transitions: trs}
}

// seedEscalationState writes the node the ladder's own write leaves: O0's Apply
// as the daemon, so the ledger is held to every rule a real one is.
func seedEscalationState(t *testing.T, f *budgetFixture, ops ...orchestrator.Op) orchestrator.State {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
	}
	for len(ops) > 0 {
		n := min(len(ops), orchestrator.MaxOps)
		next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops[:n]},
			orchestrator.Actor{Class: orchestrator.Daemon, Name: "ladder", Now: orchestratorTestNow})
		if err != nil {
			t.Fatalf("seeding the ladder's ledger: %v", err)
		}
		s, ops = next, ops[n:]
	}
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(orchestratorPath(t, orchestrator.StateNode), json.RawMessage(raw))
	return s
}

// escalationRun writes one run of a work item's own recipe into the runs
// bucket, as the runner leaves it: task.json is what makes the run locatable
// (runstore.Locate) and result.json is where the item's cost and its start come
// from. A run the item's own recipe never wrote is the only thing missing from
// escalationGoneRun below: an item whose run record is not in the bucket.
func escalationRun(t *testing.T, f *budgetFixture, id string, started time.Time, billedUSD float64) {
	t.Helper()
	dir := filepath.Join(f.runsDir(), "runs", appSlug, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]any{
		"task.json": map[string]any{"version": 1, "run_id": id, "repo": "acme/repo", "brief": "make the change"},
		"result.json": map[string]any{
			"version": 1, "run_id": id, "repo": "acme/repo", "status": "succeeded", "stage": "finalize",
			"started_at": started.UTC().Format(time.RFC3339),
			"cost_usd":   billedUSD,
			"cost":       map[string]any{"total_usd": billedUSD, "model_usd": billedUSD, "estimate": false},
		},
	} {
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// escalationDocMatchesSchema holds one `--json` document against
// schemas/escalation.schema.json, the contract a consumer reads it by.
func escalationDocMatchesSchema(t *testing.T, out string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "escalation.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/escalation.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/escalation.schema.json: %v\n%s", err, out)
	}
}

// escalationLsItem is one row of `escalation ls --json`, read as a consumer
// reads it: the two figures are pointers because null and zero are different
// facts, and a test that could not tell them apart would pass on either.
type escalationLsItem struct {
	ID            string               `json:"id"`
	Repo          string               `json:"repo"`
	Rung          string               `json:"rung"`
	Reason        string               `json:"reason"`
	EnteredAt     string               `json:"entered_at"`
	AgeSeconds    *int64               `json:"age_seconds"`
	OuterAttempts int                  `json:"outer_attempts"`
	CostUSD       *float64             `json:"cost_usd"`
	Transitions   []escalationLsChange `json:"transitions"`
}

type escalationLsChange struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Reason  string `json:"reason"`
	At      string `json:"at"`
	HeadSHA string `json:"head_sha"`
}

type escalationLsDocOut struct {
	Version int                `json:"version"`
	Kind    string             `json:"kind"`
	Project string             `json:"project"`
	Now     string             `json:"now"`
	Items   []escalationLsItem `json:"items"`
}

// TestEscalationLsJSONSchema: `escalation ls --json` is the versioned listing
// of the ladder's ledger, and it is what the schema says it is. The listing
// holds the items the ladder has acted on and no others, the reason of each is
// its last transition's, and the two figures the ledger does not hold — the age
// and the cost — come from the item's own run records: an item whose record
// this machine cannot read has neither, written as null and never as a zero,
// which is the one figure a person would act on wrongly (D1049, D1051).
//
// Mutation (drop the item's own runs from the read, or write a zero for an
// unreadable one): the age and the cost of wi-1 turn into 0 and 0.00, or wi-2
// claims $0.00 rather than null, and this test fails on the row that changed.
func TestEscalationLsJSONSchema(t *testing.T) {
	f := newOrchestratorFixture(t)
	escalationRun(t, f, escalationRunID, orchestratorTestNow.Add(-2*time.Hour), 1.25)
	seedEscalationState(t, f,
		escalationItemOp(escalationItemID, escalationRunID, escalationLedger(escalation.R2, 2,
			orchestrator.Transition{To: escalation.R0, Reason: escalation.ReasonRebase, At: orchestratorTestNow.Add(-3 * time.Hour), HeadSHA: "0f1e2d3"},
			orchestrator.Transition{From: escalation.R0, To: escalation.R2, Reason: escalation.ReasonAttempts, At: orchestratorTestNow.Add(-1 * time.Hour)},
		)),
		escalationItemOp(escalationOtherID, escalationGoneRun, escalationLedger(escalation.R1, 1,
			orchestrator.Transition{To: escalation.R1, Reason: escalation.ReasonAge, At: orchestratorTestNow.Add(-30 * time.Minute)},
		)),
		// An item the ladder has never acted on: the listing is of the ladder,
		// so it is not a row.
		orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
			ID: escalationIdleID, Title: strPtr("work nobody escalated"), Repo: strPtr("acme/repo"),
			Recipe: strPtr("code"), RigorTier: strPtr("small"), Brief: strPtr("make the change"),
		}},
	)

	out, _, err := runOrchestrator(t, "", "escalation", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	escalationDocMatchesSchema(t, out)
	var doc escalationLsDocOut
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Version != escalationDocVersion || doc.Kind != "ls" || doc.Project != orchestratorTestID {
		t.Errorf("the document says version %d, kind %q, project %q", doc.Version, doc.Kind, doc.Project)
	}
	if want := orchestratorTestNow.Format(time.RFC3339); doc.Now != want {
		t.Errorf("now = %q, want the database's own clock %q (D1230)", doc.Now, want)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("the listing holds %d items, want the two the ladder has acted on:\n%s", len(doc.Items), out)
	}
	if doc.Items[0].ID != escalationItemID || doc.Items[1].ID != escalationOtherID {
		t.Errorf("the rows are %s then %s, want them by id", doc.Items[0].ID, doc.Items[1].ID)
	}

	one := doc.Items[0]
	if one.Rung != string(escalation.R2) || one.Reason != string(escalation.ReasonAttempts) {
		t.Errorf("wi-1 is at %s for %q, want %s for %q", one.Rung, one.Reason, escalation.R2, escalation.ReasonAttempts)
	}
	if want := orchestratorTestNow.Add(-1 * time.Hour).Format(time.RFC3339); one.EnteredAt != want {
		t.Errorf("wi-1 entered its rung at %q, want the last transition's own time %q", one.EnteredAt, want)
	}
	if one.AgeSeconds == nil || *one.AgeSeconds != 2*60*60 {
		t.Errorf("wi-1's age is %v seconds, want the 2h between its run's start and the server's now", one.AgeSeconds)
	}
	if one.CostUSD == nil || *one.CostUSD != 1.25 {
		t.Errorf("wi-1's cost is %v, want the $1.25 its own run record billed", one.CostUSD)
	}
	if one.OuterAttempts != 2 {
		t.Errorf("wi-1 counts %d ladder runs, want the 2 the ledger holds", one.OuterAttempts)
	}
	if len(one.Transitions) != 2 {
		t.Fatalf("wi-1 carries %d transitions, want both:\n%s", len(one.Transitions), out)
	}
	// The first transition left the zero rung, which the document spells by
	// leaving the member out rather than by writing an empty one.
	if one.Transitions[0].From != "" || one.Transitions[1].From != string(escalation.R0) {
		t.Errorf("the chain is %q then %q, want an absent rung then R0", one.Transitions[0].From, one.Transitions[1].From)
	}

	other := doc.Items[1]
	if other.AgeSeconds != nil || other.CostUSD != nil {
		t.Errorf("wi-2 carries age %v and cost %v, and no record of its runs could be read, so both are null (D1049, D1051)", other.AgeSeconds, other.CostUSD)
	}

	// The table says the same thing in words: an unknown figure is said rather
	// than shown as a zero, which would read as an item that cost nothing.
	text, _, err := runOrchestrator(t, "", "escalation", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	for _, want := range []string{"ITEM", "REPO", "RUNG", "REASON", "AGE", "ATTEMPTS", "COST", "wi-1", "R2", "attempts", "$1.25", "unknown"} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing does not name %q:\n%s", want, text)
		}
	}
}

// TestEscalationShowTransitions: `escalation show ITEM` prints one item's
// ledger and every transition it took, oldest first, with the head each was
// decided at — and the document is the same thing in the schema's shape. An
// item the node does not hold and one no rung has touched are both refused:
// neither has a ledger, and a command that printed an empty one would tell a
// person the ladder had looked at something it never looked at.
//
// Mutation (reverse the transitions, or drop the head): the rows come out
// newest first, or the rung 0 row loses the head it is spent by, and this test
// fails naming the row.
func TestEscalationShowTransitions(t *testing.T) {
	f := newOrchestratorFixture(t)
	seedEscalationState(t, f,
		escalationItemOp(escalationItemID, escalationRunID, escalationLedger(escalation.R3, 3,
			orchestrator.Transition{To: escalation.R0, Reason: escalation.ReasonRebase, At: orchestratorTestNow.Add(-4 * time.Hour), HeadSHA: "0f1e2d3"},
			orchestrator.Transition{From: escalation.R0, To: escalation.R2, Reason: escalation.ReasonAttempts, At: orchestratorTestNow.Add(-3 * time.Hour)},
			orchestrator.Transition{From: escalation.R2, To: escalation.R3, Reason: escalation.ReasonR2Findings, At: orchestratorTestNow.Add(-2 * time.Hour)},
		)),
	)

	out, _, err := runOrchestrator(t, "", "escalation", "show", escalationItemID, "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	escalationDocMatchesSchema(t, out)
	var doc struct {
		Version int              `json:"version"`
		Kind    string           `json:"kind"`
		Project string           `json:"project"`
		Item    escalationLsItem `json:"item"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Kind != "show" || doc.Project != orchestratorTestID || doc.Item.ID != escalationItemID {
		t.Errorf("the document is kind %q of %q for %q", doc.Kind, doc.Project, doc.Item.ID)
	}
	if doc.Item.Rung != string(escalation.R3) || doc.Item.Reason != string(escalation.ReasonR2Findings) {
		t.Errorf("the item is at %s for %q, want R3 for the reason its last transition carries", doc.Item.Rung, doc.Item.Reason)
	}
	if doc.Item.OuterAttempts != 3 {
		t.Errorf("the item counts %d ladder runs, want the 3 the ledger holds", doc.Item.OuterAttempts)
	}
	if len(doc.Item.Transitions) != 3 {
		t.Fatalf("the item carries %d transitions, want all three:\n%s", len(doc.Item.Transitions), out)
	}
	want := []escalationLsChange{
		{To: string(escalation.R0), Reason: string(escalation.ReasonRebase), At: orchestratorTestNow.Add(-4 * time.Hour).Format(time.RFC3339), HeadSHA: "0f1e2d3"},
		{From: string(escalation.R0), To: string(escalation.R2), Reason: string(escalation.ReasonAttempts), At: orchestratorTestNow.Add(-3 * time.Hour).Format(time.RFC3339)},
		{From: string(escalation.R2), To: string(escalation.R3), Reason: string(escalation.ReasonR2Findings), At: orchestratorTestNow.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	if !reflect.DeepEqual(doc.Item.Transitions, want) {
		t.Errorf("the transitions are\n%+v\nwant, oldest first,\n%+v", doc.Item.Transitions, want)
	}

	text, _, err := runOrchestrator(t, "", "escalation", "show", escalationItemID)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if !strings.Contains(text, "rung R3") || !strings.Contains(text, "reason r2_findings") {
		t.Errorf("the header does not state the rung and its reason:\n%s", text)
	}
	// Every transition is one row, oldest first, and the rung 0 row carries the
	// head it was spent by: it is what says which heads have had their rebase.
	if !strings.Contains(text, "0f1e2d3") {
		t.Errorf("no row names the head rung 0 was decided at, and it is what says which heads have had their rebase:\n%s", text)
	}
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(line, "2026-11-08T") {
			rows++
		}
	}
	if rows != 3 {
		t.Errorf("the table carries %d transition rows, want three:\n%s", rows, text)
	}

	for _, tc := range []struct{ name, id, want string }{
		{"an item the node does not hold", "wi-nobody", "no work item wi-nobody"},
		{"an item no rung has touched", escalationIdleID, "has never entered the escalation ladder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.id == escalationIdleID {
				// The item the node holds and the ladder has not touched: named,
				// so the refusal is about the ladder rather than about the id.
				seedEscalationState(t, f, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{
					ID: escalationIdleID, Title: strPtr("work nobody escalated"), Repo: strPtr("acme/repo"),
					Recipe: strPtr("code"), RigorTier: strPtr("small"), Brief: strPtr("make the change"),
				}})
			}
			out, _, err := runOrchestrator(t, "", "escalation", "show", tc.id)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, want it to name %q", err, tc.want)
			}
			if out != "" {
				t.Errorf("a refusal printed a document:\n%s", out)
			}
		})
	}
}

// TestEscalationReadsNoWrite: the ladder's ledger is the daemon's alone to
// write, and this command has no write to make. The proof is the fake's own:
// the state node refuses every write that would touch it, and both subcommands
// still read and print — a command that wrote anything at the node could not
// finish on a node that refuses writes. The node and its siblings are the same
// bytes afterwards.
//
// Mutation (write the ledger back through a Node rather than reading it
// through ReadState, or stamp anything at the node): the denied write fails the
// run, and the item the command prints is not the one the read found.
func TestEscalationReadsNoWrite(t *testing.T) {
	f := newOrchestratorFixture(t)
	escalationRun(t, f, escalationRunID, orchestratorTestNow.Add(-2*time.Hour), 3.50)
	seedEscalationState(t, f,
		escalationItemOp(escalationItemID, escalationRunID, escalationLedger(escalation.R2, 2,
			orchestrator.Transition{To: escalation.R0, Reason: escalation.ReasonRebase, At: orchestratorTestNow.Add(-3 * time.Hour), HeadSHA: "0f1e2d3"},
			orchestrator.Transition{From: escalation.R0, To: escalation.R2, Reason: escalation.ReasonAttempts, At: orchestratorTestNow.Add(-1 * time.Hour)},
		)),
	)
	before := escalationNodeBytes(t, f, orchestratorPath(t, orchestrator.StateNode))
	// Deny closes the node to writes and leaves every read alone, so a command
	// that read and printed is proof it wrote nothing here.
	f.db.Deny(orchestratorPath(t, orchestrator.StateNode))

	for _, args := range [][]string{
		{"escalation", "ls"},
		{"escalation", "ls", "--json"},
		{"escalation", "show", escalationItemID},
		{"escalation", "show", escalationItemID, "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, _, err := runOrchestrator(t, "", args...)
			if err != nil {
				t.Fatalf("%v wrote to the state node, which refuses writes: %v\n%s", args, err, out)
			}
			if !strings.Contains(out, escalationItemID) {
				t.Errorf("%v printed no item:\n%s", args, out)
			}
		})
	}

	if after := escalationNodeBytes(t, f, orchestratorPath(t, orchestrator.StateNode)); !bytes.Equal(before, after) {
		t.Errorf("the state node changed:\nbefore %s\nafter  %s", before, after)
	}
	// The ladder's own audit node is the daemon's log of what it did, and a
	// reading command leaves no entry in it.
	if v := f.db.Value(orchestratorPath(t, orchestrator.AuditNode)); v != nil {
		t.Errorf("the audit node holds %v, and this command only reads", v)
	}
}

// escalationNodeBytes is one node as it stands, marshalled the way the fake holds it, so
// two readings of it compare by value rather than by encoding.
func escalationNodeBytes(t *testing.T, f *budgetFixture, path string) []byte {
	t.Helper()
	raw, err := json.Marshal(f.db.Value(path))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
