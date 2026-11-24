package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// orchestrator_named_callers_test.go is CG1c of docs/console-fixes: every
// command that acts on one orchestrator acts on the one `--name` resolved to
// and on no other (D3206, D3207).
//
// The acceptance is "touched only `<project>~<name>`", and that is two claims
// per caller: the named orchestrator's own nodes carry the write, and the
// project's own orchestrator's nodes are the nodes the command found, byte for
// byte. The second half is the one a flag that is read and then dropped fails,
// so it is the half every case below ends on (onlyTheNamedNode) — and the
// fixture gives the project's own orchestrator something of its own to find,
// one work item and, where a case reads, one of its own items or lines, so a
// caller that fell back to the project's own node would answer with the wrong
// content rather than with nothing at all.
//
// Every node here is written by the state model's own encode path, under the
// actor whose write it is — a session for the state and a plan's items, the
// daemon for the ladder's ledger, which only the daemon may write (G12) — so
// what a command reads is a document a real writer leaves rather than a shape
// this test made up.

// namedCallersNamedItem and namedCallersDefaultItem are the two items every
// fixture here holds: one under the named orchestrator, one under the project's
// own, so the two nodes are told apart by their content and not only by their
// key.
const (
	namedCallersNamedItem   = "night-work"
	namedCallersDefaultItem = "day-work"
)

// namedCallerName is the `--name` every case runs with: the name half of the
// fixture's named orchestrator, `<project>~night` (D3207).
const namedCallerName = "night"

// namedCallersRig is the installation one case acts on: the budget fixture's
// database, the project's own orchestrator's nodes as the case found them, and
// the comparison that holds them to it.
type namedCallersRig struct {
	f *budgetFixture
	// found is every node under the project's own orchestrator's key as the
	// case last looked at them, taken once the case's own fixture is in place
	// and before the command under test runs.
	found any
}

func newNamedCallersRig(t *testing.T) *namedCallersRig {
	t.Helper()
	isolateCache(t)
	f := newOrchestratorFixture(t)
	// The project's own orchestrator's state node, written by the session that
	// files work: every case below is a command that would write the project's
	// own node if `--name` were dropped, and this is the document it would
	// write over.
	seedOrchestratorState(t, f, workItemUpsert(namedCallersDefaultItem, "the project's own work"))
	return &namedCallersRig{f: f}
}

// saw is what the project's own orchestrator's nodes are from here on: the case
// calls it after its own fixture and before the command under test.
func (r *namedCallersRig) saw(t *testing.T) {
	t.Helper()
	r.found = namedCallersNodes(t, r.f, orchestratorTestID)
}

// onlyTheNamedNode is the claim the acceptance is, in its negative half: every
// node the project's own orchestrator has is the node the case found.
func (r *namedCallersRig) onlyTheNamedNode(t *testing.T) {
	t.Helper()
	if r.found == nil {
		t.Fatalf("the case never said which nodes it found, so there is nothing to hold the command to")
	}
	now := namedCallersNodes(t, r.f, orchestratorTestID)
	if !reflect.DeepEqual(r.found, now) {
		t.Errorf("the command wrote the project's own orchestrator, and `--name %s` acts on %s alone (D3206, D3207)\nfound: %v\nnow:   %v",
			namedCallerName, namedOrchestratorID, r.found, now)
	}
}

// namedCallersNodes is one orchestrator's nodes as the database holds them:
// the state, the lease, the audit log and everything else under its key, as one
// value, so a write into any of them is caught and not only a write into the
// one node the case was about.
func namedCallersNodes(t *testing.T, f *budgetFixture, id string) any {
	t.Helper()
	return objectOf(t, orchestratorSubtree(t, f.db))[id]
}

// namedStatePath and namedPlanPath are the named orchestrator's own nodes,
// built by the package that owns the paths (CG1a).
func namedStatePath(t *testing.T) string {
	t.Helper()
	path, err := orchestrator.PathState(namedOrchestratorID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func namedPlanPath(t *testing.T, plan string) string {
	t.Helper()
	path, err := orchestrator.PathPlan(namedOrchestratorID, plan)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// sessionActor is the actor a state's items and a plan's items are landed as;
// daemonActor is the ladder's, which is the only class that may write a ledger.
func sessionActor() orchestrator.Actor {
	return orchestrator.Actor{Class: orchestrator.Session, Name: "session", Now: orchestratorTestNow}
}

func daemonActor() orchestrator.Actor {
	return orchestrator.Actor{Class: orchestrator.Daemon, Name: "ladder", Now: orchestratorTestNow}
}

// seedNamedNode writes one node of the named orchestrator the way its own
// writer would leave it: O0's Apply, so the document a command reads is held to
// every rule a real one is.
func seedNamedNode(t *testing.T, f *budgetFixture, path string, actor orchestrator.Actor, ops ...orchestrator.Op) orchestrator.State {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       namedOrchestratorID,
		Role:          orchestrator.RoleOrchestrator,
	}
	for len(ops) > 0 {
		n := min(len(ops), orchestrator.MaxOps)
		next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops[:n]}, actor)
		if err != nil {
			t.Fatalf("seeding %s: %v", path, err)
		}
		s, ops = next, ops[n:]
	}
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))
	return s
}

// auditLine appends one line to an id's audit log, the way that id's daemon
// does: the log of the orchestrator a read is about has something in it, and
// the other one's has something a read of the wrong node would find.
func auditLine(t *testing.T, f *budgetFixture, id, subject, transition string) {
	t.Helper()
	db, err := newBudgetClient(t.Context(), f.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := orchestrator.NewAudit(db, id, func() (time.Time, bool) { return orchestratorTestNow, true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Append(t.Context(), orchestrator.Entry{Subject: subject, Transition: transition}); err != nil {
		t.Fatal(err)
	}
}

// TestNamedCallersTouchOnlyTheirNode is CG1c's own test: one case per caller of
// the resolver, each running the command with `--name night` and checking that
// what it read and wrote is `<project>~night`'s and that the project's own
// orchestrator's nodes are untouched.
//
// Mutation: drop the name from any one of them — openOrchestrator, the patch
// runner's resolved id, the plan import's shard, the dump import's target, the
// escalation view's id — and that case's positive half fails; keep it for the
// read but build the node from the default id and both halves fail.
func TestNamedCallersTouchOnlyTheirNode(t *testing.T) {
	t.Run("state", func(t *testing.T) {
		r := newNamedCallersRig(t)
		named := seedNamedNode(t, r.f, namedStatePath(t), sessionActor(),
			workItemUpsert(namedCallersNamedItem, "the named orchestrator's work"))
		r.saw(t)

		out, _, err := runOrchestrator(t, "", "orchestrator", "state", "--json", "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`orchestrator state --name %s`: %v\n%s", namedCallerName, err, out)
		}
		orchestratorDocMatchesSchema(t, out)
		var doc struct {
			Kind  string `json:"kind"`
			ID    string `json:"id"`
			Rev   *int64 `json:"rev"`
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ID != namedOrchestratorID {
			t.Errorf("the document names the orchestrator %q, and `--name %s` reads %s", doc.ID, namedCallerName, namedOrchestratorID)
		}
		if doc.Rev == nil || *doc.Rev != named.Rev {
			t.Errorf("the document reads rev %v, and %s's own node is at rev %d", doc.Rev, namedOrchestratorID, named.Rev)
		}
		ids := make([]string, 0, len(doc.Items))
		for _, it := range doc.Items {
			ids = append(ids, it.ID)
		}
		if len(ids) != 1 || ids[0] != namedCallersNamedItem {
			t.Errorf("the document lists %v, and the named orchestrator's node holds %s alone", ids, namedCallersNamedItem)
		}

		// The same read with no flag is the project's own node: the flag moved
		// the read, and not the fixture.
		out, _, err = runOrchestrator(t, "", "orchestrator", "state", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ID != orchestratorTestID {
			t.Errorf("`orchestrator state --json` with no --name names %q, and it reads the project's own orchestrator", doc.ID)
		}

		// --brief --offline reads the file that id's own daemon writes (D1243):
		// with no cache yet the refusal is about that id's file, and never a
		// fallback to the project's own node.
		out, _, err = runOrchestrator(t, "", "orchestrator", "state", "--brief", "--offline", "--name", namedCallerName)
		if err == nil {
			t.Fatalf("--brief --offline with no cache returned success:\n%s", out)
		}
		if !strings.Contains(err.Error(), namedOrchestratorID) {
			t.Errorf("the refusal does not name %s's own cache file: %v", namedOrchestratorID, err)
		}
		seedBriefCache(t, namedOrchestratorID, orchestratorTestNow.Add(-5*time.Minute))
		out, _, err = runOrchestrator(t, "", "orchestrator", "state", "--brief", "--offline", "--name", namedCallerName)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, "orchestrator "+namedOrchestratorID+": the brief the daemon cached") {
			t.Errorf("the offline print does not carry %s's own cached brief, which is the file its daemon writes:\n%s", namedOrchestratorID, out)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("events", func(t *testing.T) {
		r := newNamedCallersRig(t)
		auditLine(t, r.f, orchestratorTestID, namedCallersDefaultItem, "approved")
		auditLine(t, r.f, namedOrchestratorID, namedCallersNamedItem, "launched")
		r.saw(t)

		read := func(args ...string) orchestratorEventsDoc {
			t.Helper()
			out, _, err := runOrchestrator(t, "", append([]string{"orchestrator", "events", "--json"}, args...)...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			orchestratorDocMatchesSchema(t, out)
			var doc orchestratorEventsDoc
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			return doc
		}

		doc := read("--name", namedCallerName)
		if doc.ID != namedOrchestratorID {
			t.Errorf("the document names the orchestrator %q, and `--name %s` reads %s", doc.ID, namedCallerName, namedOrchestratorID)
		}
		if len(doc.Events) != 1 || doc.Events[0].Subject != namedCallersNamedItem {
			t.Errorf("the log read is %v, and %s's own log holds one line about %s", doc.Events, namedOrchestratorID, namedCallersNamedItem)
		}
		// Without the flag it is the project's own log, which is the other line:
		// the two logs are told apart by what they hold.
		if doc := read(); doc.ID != orchestratorTestID || len(doc.Events) != 1 || doc.Events[0].Subject != namedCallersDefaultItem {
			t.Errorf("`orchestrator events --json` with no --name read %q holding %v, and the project's own log holds one line about %s",
				doc.ID, doc.Events, namedCallersDefaultItem)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("patch", func(t *testing.T) {
		r := newNamedCallersRig(t)
		before := seedNamedNode(t, r.f, namedStatePath(t), sessionActor(),
			workItemUpsert(namedCallersNamedItem, "the named orchestrator's work"))
		r.saw(t)
		path := patchFile(t, patchDoc(t, before.Rev, workItemUpsert("night-work-2", "one more piece of the night's work")))

		out, _, err := runOrchestrator(t, "", "orchestrator", "patch", "-f", path, "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`orchestrator patch --name %s`: %v\n%s", namedCallerName, err, out)
		}
		if !strings.Contains(out, namedOrchestratorID) {
			t.Errorf("the write does not name the orchestrator it wrote, %s:\n%s", namedOrchestratorID, out)
		}
		after := planNodeRead(t, r.f.db, namedStatePath(t))
		if after.Rev != before.Rev+1 {
			t.Errorf("the named orchestrator's node is at rev %d, and the patch wrote one op over rev %d", after.Rev, before.Rev)
		}
		if _, ok := after.Item("night-work-2"); !ok {
			t.Errorf("the patch did not land in %s's own node, which holds %v", namedOrchestratorID, after.WorkItems)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("patch approves a plan's node", func(t *testing.T) {
		r := newNamedCallersRig(t)
		asPerson(t)
		typed(t)
		const plan = "graph-import"
		before := seedNamedNode(t, r.f, namedPlanPath(t, plan), sessionActor(),
			planItemUpsert("plan-a", plan, "the plan's first task"))
		r.saw(t)
		path := patchFile(t, patchDoc(t, before.Rev, planApprove(t, before, plan, "plan-a")))

		// The approval is the person's own word, so the patch is confirmed at a
		// terminal the way a real one is: the project's own name, which is what
		// the confirmation asks for whatever orchestrator the patch is about.
		out, _, err := runOrchestrator(t, orchestratorTestID+"\n", "orchestrator", "patch", "-f", path, "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`orchestrator patch --name %s`: %v\n%s", namedCallerName, err, out)
		}
		if !strings.Contains(out, namedOrchestratorID) {
			t.Errorf("the write does not name the orchestrator it wrote, %s:\n%s", namedOrchestratorID, out)
		}
		after := planNodeRead(t, r.f.db, namedPlanPath(t, plan))
		w, ok := after.Item("plan-a")
		if !ok || w.Approved == nil || w.Approved.By != orchestratorPerson {
			t.Errorf("plan-a carries %+v, and the confirmed patch approves it in %s's own plan node", w.Approved, namedOrchestratorID)
		}
		// The shard is the id's as well as the node's: an approval whose list
		// named the plan but whose paths were built from the default id would
		// land here instead.
		if v := r.f.db.Value(planImportPath(t, plan)); v != nil {
			t.Errorf("the approval wrote %s, which is the project's own orchestrator's node for that plan: %v", planImportPath(t, plan), v)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("import -f", func(t *testing.T) {
		r := newNamedCallersRig(t)
		asPerson(t)
		path := patchFile(t, patchDoc(t, 0, workItemUpsert(namedCallersNamedItem, "the named orchestrator's work")))
		r.saw(t)

		out, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", path, "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`orchestrator import -f --name %s`: %v\n%s", namedCallerName, err, out)
		}
		if !strings.Contains(out, namedOrchestratorID) {
			t.Errorf("the import does not name the orchestrator it wrote, %s:\n%s", namedOrchestratorID, out)
		}
		after := planNodeRead(t, r.f.db, namedStatePath(t))
		if after.Rev != 1 {
			t.Errorf("the named orchestrator's node is at rev %d, and the dump wrote one op over the absent node's rev 0", after.Rev)
		}
		if _, ok := after.Item(namedCallersNamedItem); !ok {
			t.Errorf("the dump did not land in %s's own node, which holds %v", namedOrchestratorID, after.WorkItems)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("import --plan", func(t *testing.T) {
		r := newNamedCallersRig(t)
		t.Chdir(planImportCheckout(t, planImportGraph, planImportPlanMD))
		r.saw(t)

		out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo", "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`orchestrator import --plan --name %s`: %v\n%s", namedCallerName, err, out)
		}
		if !strings.Contains(out, namedOrchestratorID) {
			t.Errorf("the import does not name the orchestrator it wrote, %s:\n%s", namedOrchestratorID, out)
		}
		imported := planNodeRead(t, r.f.db, namedPlanPath(t, "demo"))
		if len(imported.WorkItems) != 3 {
			t.Errorf("the plan's node under %s holds %d work items, and the graph has 3 nodes", namedOrchestratorID, len(imported.WorkItems))
		}
		for _, w := range imported.WorkItems {
			if w.Spec != "demo" {
				t.Errorf("the item %s carries spec %q, and the plan it was imported from is demo", w.ID, w.Spec)
			}
		}
		// The plan's node is the id's too: an import that resolved the plan but
		// built its node from the default id would land here.
		if v := r.f.db.Value(planImportPath(t, "demo")); v != nil {
			t.Errorf("the import wrote %s, which is the project's own orchestrator's node for that plan: %v", planImportPath(t, "demo"), v)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("escalation", func(t *testing.T) {
		r := newNamedCallersRig(t)
		escalationRun(t, r.f, escalationRunID, orchestratorTestNow.Add(-2*time.Hour), 1.25)
		seedNamedNode(t, r.f, namedStatePath(t), daemonActor(),
			escalationItemOp(namedCallersNamedItem, escalationRunID, escalationLedger(escalation.R2, 2,
				orchestrator.Transition{To: escalation.R0, Reason: escalation.ReasonRebase, At: orchestratorTestNow.Add(-3 * time.Hour), HeadSHA: "0f1e2d3"},
				orchestrator.Transition{From: escalation.R0, To: escalation.R2, Reason: escalation.ReasonAttempts, At: orchestratorTestNow.Add(-1 * time.Hour)},
			)))
		// The project's own orchestrator has an escalated item of its own, at a
		// different rung: a read of the wrong node would list that one, and the
		// rung it is at would say so.
		seedEscalationState(t, r.f, escalationItemOp(namedCallersDefaultItem, escalationRunID, escalationLedger(escalation.R0, 0,
			orchestrator.Transition{To: escalation.R0, Reason: escalation.ReasonRebase, At: orchestratorTestNow.Add(-4 * time.Hour)},
		)))
		r.saw(t)

		out, _, err := runOrchestrator(t, "", "escalation", "ls", "--json", "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`escalation ls --name %s`: %v\n%s", namedCallerName, err, out)
		}
		escalationDocMatchesSchema(t, out)
		var ls escalationLsDoc
		if err := json.Unmarshal([]byte(out), &ls); err != nil {
			t.Fatal(err)
		}
		if ls.Project != namedOrchestratorID {
			t.Errorf("the document names the project %q, and `--name %s` reads %s", ls.Project, namedCallerName, namedOrchestratorID)
		}
		if len(ls.Items) != 1 || ls.Items[0].ID != namedCallersNamedItem {
			t.Fatalf("the ladder listed %v, and %s's own node holds one escalated item, %s", ls.Items, namedOrchestratorID, namedCallersNamedItem)
		}
		if ls.Items[0].Rung != escalation.R2 || ls.Items[0].CostUSD == nil || *ls.Items[0].CostUSD != 1.25 {
			t.Errorf("the row is %+v, and the named item's own ledger is at rung %s with one run that billed $1.25", ls.Items[0], escalation.R2)
		}

		out, _, err = runOrchestrator(t, "", "escalation", "show", namedCallersNamedItem, "--json", "--name", namedCallerName)
		if err != nil {
			t.Fatalf("`escalation show --name %s`: %v\n%s", namedCallerName, err, out)
		}
		escalationDocMatchesSchema(t, out)
		var show escalationShowDoc
		if err := json.Unmarshal([]byte(out), &show); err != nil {
			t.Fatal(err)
		}
		if show.Project != namedOrchestratorID || show.Item.ID != namedCallersNamedItem {
			t.Errorf("show read %q's item %q, and `--name %s` reads %s", show.Project, show.Item.ID, namedCallerName, namedOrchestratorID)
		}
		// The project's own orchestrator's item is not one of the named
		// orchestrator's items, and the refusal says which of the two an item
		// has to be in to be read at all: the read is the named node's.
		_, _, err = runOrchestrator(t, "", "escalation", "show", namedCallersDefaultItem, "--name", namedCallerName)
		if err == nil {
			t.Fatalf("`escalation show %s --name %s` read the project's own orchestrator's item", namedCallersDefaultItem, namedCallerName)
		}
		if !strings.Contains(err.Error(), "no work item") {
			t.Errorf("the refusal does not say the item is not in the named orchestrator: %v", err)
		}
		// And with no flag the ladder is the project's own: its own item, at its
		// own rung.
		out, _, err = runOrchestrator(t, "", "escalation", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if err := json.Unmarshal([]byte(out), &ls); err != nil {
			t.Fatal(err)
		}
		if ls.Project != orchestratorTestID || len(ls.Items) != 1 || ls.Items[0].ID != namedCallersDefaultItem || ls.Items[0].Rung != escalation.R0 {
			t.Errorf("`escalation ls --json` with no --name read %q holding %v, and the project's own orchestrator holds %s at rung %s",
				ls.Project, ls.Items, namedCallersDefaultItem, escalation.R0)
		}

		r.onlyTheNamedNode(t)
	})

	t.Run("run", func(t *testing.T) {
		r := newNamedCallersRig(t)
		r.saw(t)

		out, _, err := runOrchestrator(t, "", "orchestrator", "run", "--name", namedCallerName, "--once")
		if err != nil {
			t.Fatalf("`orchestrator run --name %s --once`: %v\n%s", namedCallerName, err, out)
		}
		if !strings.Contains(out, namedOrchestratorID) {
			t.Errorf("the run does not name the orchestrator it drove, %s:\n%s", namedOrchestratorID, out)
		}

		r.onlyTheNamedNode(t)
	})
}
