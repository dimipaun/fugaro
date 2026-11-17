package orchestrator_test

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// planState is a plan's own node as an import leaves it: one item per id, each
// tagged with the plan's folder and carrying the plan task the import wrote.
// The first id is the base state's own item, so a caller names the items it
// wants and gets exactly those.
func planState(t *testing.T, now time.Time, plan string, ids ...string) orchestrator.State {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	for i, id := range ids {
		change := func(o *orchestrator.WorkItemOp) {
			o.Spec = ptr(plan)
			o.PlanTask = ptr(plan + "/" + id)
		}
		if i == 0 {
			s = mustApply(t, s, orchestrator.Session, sessionName, now, upsertOp(id, change))
			continue
		}
		s = mustApply(t, s, orchestrator.Session, sessionName, now, upsertOp(id, func(o *orchestrator.WorkItemOp) {
			o.Title = ptr("the plan's " + id)
			o.Repo = ptr("owner/repo")
			o.Recipe = ptr("standard")
			o.RigorTier = ptr("medium")
			o.Brief = ptr("Do what the plan's " + id + " says.")
			change(o)
		}))
	}
	return s
}

const sessionName = "session:20261117-090000-abcd"

// planItems is the list an import prints and a person approves: the named
// items at the digests they stand at now.
func planItems(t *testing.T, s orchestrator.State, ids ...string) []orchestrator.PlanApproveItem {
	t.Helper()
	items := make([]orchestrator.PlanApproveItem, 0, len(ids))
	for _, id := range ids {
		w, ok := s.Item(id)
		if !ok {
			t.Fatalf("the state carries no item %q", id)
		}
		items = append(items, orchestrator.PlanApproveItem{ID: id, Digest: orchestrator.SpecDigest(w)})
	}
	return items
}

func planApproveOp(node string, items ...orchestrator.PlanApproveItem) orchestrator.Op {
	return orchestrator.Op{Op: orchestrator.OpPlanApprove, PlanApprove: &orchestrator.PlanApproveOp{Node: node, Items: items}}
}

// approvedBy is the approval an item carries, or nil when it carries none.
func approvedBy(t *testing.T, s orchestrator.State, id string) *orchestrator.Approval {
	t.Helper()
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("the state carries no item %q", id)
	}
	return w.Approved
}

// skipSentences is what a plan approval left behind for the items it did not
// approve: the facts a person reads to learn which item stayed behind.
func skipSentences(s orchestrator.State) []string {
	var values []string
	for key, f := range s.Facts {
		if strings.HasPrefix(key, "plan_approve_skipped_") {
			values = append(values, f.Value)
		}
	}
	sort.Strings(values)
	return values
}

// TestPlanApproveApprovesTheListedItems is the op's whole point: one person
// patch is the go for a whole plan, and it is the go for the items the list
// names and no others — an item of the same plan that the list leaves out is
// one the person did not read (D2706, D2738).
func TestPlanApproveApprovesTheListedItems(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1", "wi-2", "wi-3", "wi-4")

	items := planItems(t, s, "wi-1", "wi-2", "wi-3")
	next := mustApply(t, s, orchestrator.Person, "owner", now, planApproveOp(plan, items...))

	for _, it := range items {
		a := approvedBy(t, next, it.ID)
		if a == nil {
			t.Fatalf("%s was listed and carries no approval", it.ID)
		}
		if a.By != "owner" {
			t.Fatalf("the approval of %s names %q", it.ID, a.By)
		}
		if !a.At.Equal(now) {
			t.Fatalf("the approval of %s is stamped %s, and the actor's time is %s", it.ID, a.At, now)
		}
		if a.SpecDigest != it.Digest {
			t.Fatalf("the approval of %s binds %s, and the list carried %s", it.ID, a.SpecDigest, it.Digest)
		}
		if a.Attempts != orchestrator.ApprovalAttempts {
			t.Fatalf("the approval of %s covers %d attempts, and one covers %d", it.ID, a.Attempts, orchestrator.ApprovalAttempts)
		}
		w, _ := next.Item(it.ID)
		if w.SpentApprovalAt != nil {
			t.Fatalf("the approval of %s landed with a spent marker beside it", it.ID)
		}
	}
	if a := approvedBy(t, next, "wi-4"); a != nil {
		t.Fatal("an item of the plan the list left out was approved, and the list is the whole scope of the go")
	}
	if got := skipSentences(next); len(got) != 0 {
		t.Fatalf("an approval that skipped nothing recorded %q", got)
	}
}

// TestPlanApproveRefusedFromSessionAndDaemon is the fence the op stands on. A
// session can read an item and compute its digest, so nothing but the class of
// the actor keeps an agent from approving a plan it wrote itself.
func TestPlanApproveRefusedFromSessionAndDaemon(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1", "wi-2")
	items := planItems(t, s, "wi-1", "wi-2")

	for _, tc := range []struct {
		name  string
		class orchestrator.Class
		who   string
	}{
		{"a session", orchestrator.Session, sessionName},
		{"the daemon", orchestrator.Daemon, "orchestrator-a"},
		{"a decision agent", orchestrator.DecisionAgent, "decision:20261117-090000-abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := applyAs(t, s, tc.class, tc.who, now, planApproveOp(plan, items...)); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
				t.Fatalf("a %s approved a plan's items: err = %v, want %v", tc.class, err, orchestrator.ErrActorNotAllowed)
			}
		})
	}

	// A person's approval names the person: an approval nobody signed is one
	// the record cannot attribute.
	if _, err := applyAs(t, s, orchestrator.Person, "", now, planApproveOp(plan, items...)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("an unsigned approval landed: err = %v, want %v", err, orchestrator.ErrInvalid)
	}
	for _, id := range []string{"wi-1", "wi-2"} {
		if a := approvedBy(t, s, id); a != nil {
			t.Fatalf("%s carries an approval after a refused patch", id)
		}
	}
}

// TestPlanApproveRefusedForAnEmptyNodeOrList is the two patches the op cannot
// read as an approval: one that names no node, and one that approves nothing.
// Neither is a refusal to widen — an approval of nothing is not a weaker
// approval, it is a patch that says nothing about what the person read.
func TestPlanApproveRefusedForAnEmptyNodeOrList(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1")
	items := planItems(t, s, "wi-1")

	for _, tc := range []struct {
		name string
		op   orchestrator.Op
	}{
		{"no node", planApproveOp("", items...)},
		{"no items", planApproveOp(plan)},
		{"a node that is not a plan folder", planApproveOp("docs/"+plan, items...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := applyAs(t, s, orchestrator.Person, "owner", now, tc.op); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Fatalf("the patch landed: err = %v, want %v", err, orchestrator.ErrInvalid)
			}
		})
	}
}

// TestPlanApproveIgnoresItemsTaggedLater is D2706's amendment, and the reason
// the op carries ids at all. An op that named a plan and approved everything
// tagged with it would approve an item a session tagged with the plan's name
// after the import — an item nobody read. The list is what the person read, so
// an item that joined the plan afterwards stays unapproved however well it
// matches.
func TestPlanApproveIgnoresItemsTaggedLater(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1")
	read := planItems(t, s, "wi-1")

	// The list the person read is taken, and only then does a session tag
	// another item with the plan's name — a backlog item the import never saw.
	tagged := mustApply(t, s, orchestrator.Session, sessionName, now,
		upsertOp("wi-2", func(o *orchestrator.WorkItemOp) {
			o.Title = ptr("the task the plan grew afterwards")
			o.Repo = ptr("owner/repo")
			o.Recipe = ptr("standard")
			o.RigorTier = ptr("medium")
			o.Brief = ptr("Do what the plan's wi-2 says.")
			o.Spec = ptr(plan)
			o.PlanTask = ptr(plan + "/wi-2")
		}))

	next := mustApply(t, tagged, orchestrator.Person, "owner", now, planApproveOp(plan, read...))
	if a := approvedBy(t, next, "wi-1"); a == nil {
		t.Fatal("the item the list named carries no approval, and the list is what the person read")
	}
	if a := approvedBy(t, next, "wi-2"); a != nil {
		t.Fatal("an item tagged with the plan after the list was read was approved by it")
	}
}

// TestPlanApproveIgnoresAnotherNodesItems is the node half of the scope. Apply
// is handed one node's document and cannot see the others, so the node an op
// names is checked against the items themselves: a list that carries another
// node's item does not approve it, and does not fail the patch either — the
// items that are the node's still go.
func TestPlanApproveIgnoresAnotherNodesItems(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1", "wi-2")
	// wi-3 is a backlog item and wi-4 belongs to another plan: both are on the
	// node's document and neither is one this approval is about.
	s = mustApply(t, s, orchestrator.Session, sessionName, now,
		upsertOp("wi-3", func(o *orchestrator.WorkItemOp) {
			o.Title = ptr("a ticket from the backlog")
			o.Repo = ptr("owner/repo")
			o.Recipe = ptr("standard")
			o.RigorTier = ptr("medium")
			o.Brief = ptr("Do what the ticket says.")
		}))
	s = mustApply(t, s, orchestrator.Session, sessionName, now,
		upsertOp("wi-4", func(o *orchestrator.WorkItemOp) {
			o.Title = ptr("another plan's task")
			o.Repo = ptr("owner/repo")
			o.Recipe = ptr("standard")
			o.RigorTier = ptr("medium")
			o.Brief = ptr("Do what the other plan says.")
			o.Spec = ptr("spend-accuracy")
		}))

	items := planItems(t, s, "wi-1", "wi-2", "wi-3", "wi-4")
	next := mustApply(t, s, orchestrator.Person, "owner", now, planApproveOp(plan, items...))

	for _, id := range []string{"wi-1", "wi-2"} {
		if a := approvedBy(t, next, id); a == nil {
			t.Fatalf("%s is the plan's own item and carries no approval", id)
		}
	}
	for _, id := range []string{"wi-3", "wi-4"} {
		if a := approvedBy(t, next, id); a != nil {
			t.Fatalf("%s is not an item of %s and the approval landed on it", id, plan)
		}
	}
}

// TestPlanApproveSkipsChangedDigest is the one case the op reports on. An
// approval binds the spec that was read (applyApprove's rule), so an item whose
// spec moved between the read and the patch is skipped rather than approved —
// and the person learns which one, because the nineteen that did not move must
// not be held up by the one that did.
func TestPlanApproveSkipsChangedDigest(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	s := planState(t, now, plan, "wi-1", "wi-2", "wi-3")
	read := planItems(t, s, "wi-1", "wi-2", "wi-3")

	// The person read the list; before the patch lands, the plan is re-imported
	// and wi-2's brief changes, so its digest is no longer the one on the list.
	moved := mustApply(t, s, orchestrator.Session, sessionName, now,
		upsertOp("wi-2", func(o *orchestrator.WorkItemOp) { o.Brief = ptr("Do what the plan's wi-2 says now.") }))

	next := mustApply(t, moved, orchestrator.Person, "owner", now, planApproveOp(plan, read...))
	for _, id := range []string{"wi-1", "wi-3"} {
		if a := approvedBy(t, next, id); a == nil {
			t.Fatalf("%s did not move and carries no approval", id)
		}
	}
	if a := approvedBy(t, next, "wi-2"); a != nil {
		t.Fatal("an item whose spec moved since it was read was approved at the digest it no longer has")
	}
	w, _ := next.Item("wi-2")
	if w.Brief != "Do what the plan's wi-2 says now." {
		t.Fatalf("the skip rewrote the item: its brief is %q", w.Brief)
	}

	got := skipSentences(next)
	if len(got) != 1 {
		t.Fatalf("the approval left %d sentences about skipped items, want one: %q", len(got), got)
	}
	if !strings.Contains(got[0], "wi-2") {
		t.Fatalf("the sentence a person reads does not name the item it is about: %q", got[0])
	}
	for _, f := range next.Facts {
		if strings.Contains(f.Value, "wi-1") || strings.Contains(f.Value, "wi-3") {
			t.Fatalf("an item that landed is named in a skip sentence: %q", f.Value)
		}
	}
}

// TestPlanApproveSkipsFinishedItems: an item that is already done or cancelled
// takes no approval — validateItem refuses one on a finished item — so a list
// read before its items landed leaves the finished ones alone and lands the
// rest. The list is what a saved patch or an import's own `--json` output
// carries, so re-running it after some of the plan merged is the ordinary way
// to meet this, and one finished item must not take the whole patch down.
func TestPlanApproveSkipsFinishedItems(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	plan := "orchestrator-graph-estimates"
	runID := "20261117-090000-abcd"
	s := planState(t, now, plan, "wi-1", "wi-2", "wi-3")
	read := planItems(t, s, "wi-1", "wi-2", "wi-3")

	// wi-2 has landed and wi-3 was cancelled since the list was read: both runs
	// are over.
	finish := func(id string, phase orchestrator.Phase) {
		t.Helper()
		s = mustApply(t, s, orchestrator.Daemon, "orchestrator-a", now,
			phaseOp(id, ptr(orchestrator.PhaseRunning), &runID))
		s = mustApply(t, s, orchestrator.Daemon, "orchestrator-a", now,
			phaseOp(id, ptr(phase), nil))
	}
	finish("wi-2", orchestrator.PhaseDone)
	finish("wi-3", orchestrator.PhaseCancelled)

	next := mustApply(t, s, orchestrator.Person, "owner", now, planApproveOp(plan, read...))
	if a := approvedBy(t, next, "wi-1"); a == nil {
		t.Fatal("the one item that is not finished carries no approval")
	}
	for _, id := range []string{"wi-2", "wi-3"} {
		if a := approvedBy(t, next, id); a != nil {
			t.Fatalf("the finished item %s carries an approval, and a document with one is one this release does not read back", id)
		}
	}
	if got := skipSentences(next); len(got) != 0 {
		t.Fatalf("a finished item was recorded as something the person must act on: %q", got)
	}
}

// TestPlanApproveOnBacklogNode is D2717: the standing backlog is the other node
// this op approves, and its items are the ones carrying no spec at all (D2715).
// The node is what the fence reads, so a backlog approval leaves a plan's items
// alone exactly as a plan's approval leaves the backlog's.
func TestPlanApproveOnBacklogNode(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	s := planState(t, now, "orchestrator-graph-estimates", "wi-1")
	// wi-2 is the backlog's: a ticket with no plan behind it.
	s = mustApply(t, s, orchestrator.Session, sessionName, now,
		upsertOp("wi-2", func(o *orchestrator.WorkItemOp) {
			o.Title = ptr("a ticket from the backlog")
			o.Repo = ptr("owner/repo")
			o.Recipe = ptr("standard")
			o.RigorTier = ptr("medium")
			o.Brief = ptr("Do what the ticket says.")
		}))

	items := planItems(t, s, "wi-1", "wi-2")
	next := mustApply(t, s, orchestrator.Person, "owner", now, planApproveOp(orchestrator.BacklogNode, items...))

	if a := approvedBy(t, next, "wi-2"); a == nil {
		t.Fatal("the backlog's own item carries no approval")
	}
	if a := approvedBy(t, next, "wi-1"); a != nil {
		t.Fatal("a plan's item was approved by an approval of the backlog")
	}
	if got := skipSentences(next); len(got) != 0 {
		t.Fatalf("an approval of the backlog recorded %q about an item of another node", got)
	}
}
