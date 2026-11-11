package cli

// O11's import, the one-time session dump (docs/stateless-orchestrator, spec
// "Migration"). The dump is the first document in this loop a model writes, so
// these tests pin the three things that make it safe to read: it goes through
// the same validator and the same caps as `patch`, an oversize dump is refused
// rather than cut, and no dump approves work — approving a work item, setting
// the goal and closing a person's decision are the person's own word at a
// terminal, so a dump carrying one is refused whole rather than applied, and a
// decision a dump raises itself is judged with the rest of what it carries, not
// against the state it found.

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
	for _, want := range []string{"may not approve a work item", "nothing was written"} {
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

// TestImportRefusesADecisionItRaisesAndCloses: the same document, not the same
// node. A dump whose first op raises a decision waiting on a person and whose
// next op answers it is refused whole, because the fence is the state model's
// own — the dump is folded as a session folds it, so the decision is in the
// state by the time its resolve is judged. Judging a resolve against the state
// read before the dump, and then writing the dump as a person, would see no
// decision at all, let the resolve through and land a person's decision closed
// with the person nowhere near a terminal — and the how-to, the release note and
// this command's own help all promise that document is refused.
//
// The class is the line, not the op: a decision waiting on a session is the
// session's to close, so the same shape waiting on a session lands, closed and
// recorded as the session's answer.
func TestImportRefusesADecisionItRaisesAndCloses(t *testing.T) {
	f := newOrchestratorFixture(t)
	asPerson(t)
	s := emptyOrchestratorState()
	add := personDecision("pick-a-repo")
	resolve := orchestrator.Op{Op: orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: "pick-a-repo", Answer: "the web one"}}

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", patchFile(t, patchDoc(t, s.Rev, add, resolve)))
	if err == nil {
		t.Fatalf("a dump that raises a person's decision and closes it in the same document landed:\n%s", out)
	}
	for _, want := range []string{"nothing was written", "waits on a person"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Fatal("the refused dump wrote the state node: the decision is raised by an op the same dump carries, and neither it nor the resolve lands")
	}

	// The same dump with the resolve dropped: the decision lands, open, waiting
	// on the person the import left it to.
	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", patchFile(t, patchDoc(t, s.Rev, add))); err != nil {
		t.Fatalf("%v", err)
	}
	d, ok := storedOrchestratorState(t, f).Decision("pick-a-repo")
	if !ok {
		t.Fatal("the import wrote no decision")
	}
	if d.Resolved() {
		t.Errorf("the decision arrived closed by %q, and a dump cannot close the person's question", d.By)
	}
	if d.WaitingOn != orchestrator.OnPerson {
		t.Errorf("the decision waits on %q, and the dump said a person", d.WaitingOn)
	}

	// A decision waiting on the session is the session's own, so the same
	// document shape lands it closed: the class is the line, not the op. It is
	// recorded as the person's answer, because the dump lands as whoever ran it.
	mine := orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
		ID: "which-branch", Question: "which branch does the sweep watch?", WaitingOn: orchestrator.OnSession,
	}}
	answer := orchestrator.Op{Op: orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: "which-branch", Answer: "the sessions branch"}}
	path := patchFile(t, patchDoc(t, storedOrchestratorState(t, f).Rev, mine, answer))
	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", path); err != nil {
		t.Fatalf("a dump that closes the session's own decision was refused: %v", err)
	}
	d, ok = storedOrchestratorState(t, f).Decision("which-branch")
	if !ok || !d.Resolved() || d.By != orchestrator.Person {
		t.Errorf("the session's own decision did not land closed: %+v", d)
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
