package cli

// O11's import, the one-time session dump (docs/stateless-orchestrator, spec
// "Migration"). The dump is the first document in this loop a model writes, so
// these tests pin the three things that make it safe to read: it goes through
// the same validator and the same caps as `patch`, an oversize dump is refused
// rather than cut, and no dump approves work — approving a work item, setting
// the goal and closing a person's decision are the person's own word at a
// terminal, so a dump carrying one is refused whole rather than applied.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// emptyOrchestratorState is the node a one-time import lands on: nobody has
// written it yet, which is what Node.Load reads an absent node as, at revision
// 0 — the revision the dump carries.
func emptyOrchestratorState() orchestrator.State {
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
	}
}

// TestImportNeverMarksAnItemApproved is O11's red step, and the fence on the
// migration's one dangerous op: a dump that approves work is refused whole,
// because the approval a dump could carry is the model's and the approval the
// loop acts on is a person's, given at a terminal over the spec they read. A
// dump that declares the same item without the approval lands, and what it
// declares arrives unapproved — which is what the command prints, so a person
// knows the migration left them the approving to do.
func TestImportNeverMarksAnItemApproved(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	s := emptyOrchestratorState()
	up := workItemUpsert("web-fix", "fix the web build")
	approve := orchestrator.Op{Op: orchestrator.OpWorkItemApprove, Approve: &orchestrator.ApproveOp{
		ID: "web-fix", SpecDigest: digestAfter(t, s, up, "web-fix"),
	}}
	path := patchFile(t, patchDoc(t, s.Rev, up, approve))

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", path)
	if err == nil {
		t.Fatalf("a dump that approves a work item was imported:\n%s", out)
	}
	for _, want := range []string{"work_item_approve", "only a person may land"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Fatal("the refused dump wrote the state node: a document refused for one op lands none of the others")
	}

	// The same dump without the approval: it lands, and every item it declares
	// is unapproved.
	path = patchFile(t, patchDoc(t, s.Rev, up))
	out, _, err = runOrchestrator(t, "", "orchestrator", "import", "-f", path)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	w, ok := storedOrchestratorState(t, f).Item("web-fix")
	if !ok {
		t.Fatal("the import wrote no item")
	}
	if w.Approved != nil {
		t.Errorf("the item arrived approved by %q, and a dump cannot approve work", w.Approved.By)
	}
	if !strings.Contains(out, "1 work item the dump declares arrived unapproved") {
		t.Errorf("the import does not say the item it declared is unapproved:\n%s", out)
	}
}

// TestImportRunsTheSameValidatorAsPatch: the dump is read by `patch`'s own
// decoder, validator and apply — the reconciliation's "validate it with the
// same code, cap it" — so every document one command refuses, the other refuses
// with the validator's own reason. A second reader of the format written for
// the import is how a document lands through one door that the other refuses.
func TestImportRunsTheSameValidatorAsPatch(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	for _, tc := range []struct{ name, doc, reason string }{
		{
			"an op this release does not have",
			`{"base_rev":0,"ops":[{"op":"summary_replace","goal":{"goal":"the whole session"}}]}`,
			`"summary_replace" is not an op this release has`,
		},
		{
			"a field this release does not model",
			`{"base_rev":0,"summary":"what the session tracked","ops":[{"op":"fact_set","fact":{"key":"stage","value":"migration"}}]}`,
			"does not model",
		},
		{
			"a work item field only the daemon writes",
			`{"base_rev":0,"ops":[{"op":"work_item_upsert","work_item":{"id":"web-fix","title":"fix the web build","phase":"running"}}]}`,
			"may not write a work item's phase",
		},
	} {
		path := patchFile(t, tc.doc)
		_, _, patchErr := runOrchestrator(t, "", "orchestrator", "patch", "-f", path)
		out, _, importErr := runOrchestrator(t, "", "orchestrator", "import", "-f", path)
		if patchErr == nil {
			t.Errorf("%s: patch accepted a document the format refuses", tc.name)
		}
		if importErr == nil {
			t.Fatalf("%s: import accepted a document the format refuses:\n%s", tc.name, out)
		}
		for cmd, err := range map[string]error{"patch": patchErr, "import": importErr} {
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("%s: %s refused with %v, and the validator's own reason is %q", tc.name, cmd, err, tc.reason)
			}
		}
	}
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Error("a refused import wrote the state node")
	}
}

// TestImportRefusesAnOversizeDump: the caps bound the dump in bytes before it
// is parsed and in ops when it is, and both refusals leave the node alone. The
// byte cap is the one that matters for a document a model wrote: the format
// clips free text rather than refusing it, so a megabyte of it would otherwise
// land as a clipped value nobody read.
func TestImportRefusesAnOversizeDump(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	up := workItemUpsert("web-fix", "fix the web build")
	up.WorkItem.Brief = strPtr(strings.Repeat("x", maxPatchDocumentBytes))
	path := patchFile(t, patchDoc(t, 0, up))

	_, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", path)
	if err == nil {
		t.Fatal("a dump over the byte cap was imported")
	}
	if want := fmt.Sprintf("over %d bytes", maxPatchDocumentBytes); !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name the cap (%q): %v", want, err)
	}

	// Over the op cap, which is O0's: a dump is at most MaxOps long, and the
	// count is refused before an op of it is looked at.
	ops := make([]orchestrator.Op, 0, orchestrator.MaxOps+1)
	for i := 0; i <= orchestrator.MaxOps; i++ {
		ops = append(ops, workItemUpsert("item-"+orchNum(i), "the item "+orchNum(i)))
	}
	path = patchFile(t, patchDoc(t, 0, ops...))
	_, _, err = runOrchestrator(t, "", "orchestrator", "import", "-f", path)
	if err == nil {
		t.Fatal("a dump over the op cap was imported")
	}
	if want := fmt.Sprintf("at most %d", orchestrator.MaxOps); !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name the cap (%q): %v", want, err)
	}

	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Error("a refused dump wrote the state node")
	}
}
