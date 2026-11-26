package cli

// `fugaro update` folds docs/backlog/ into the standing backlog, once
// (docs/orchestrator-graph-estimates, B2, D2716).
//
// The folder was a backlog of its own, one Markdown file per item (D26–D32);
// from D2716 the project's backlog is the work items with no spec in the
// orchestrator's backlog node (D2715, D2735), and `fugaro update` run in a
// checkout is what imports the folder's open items into it — once, keyed on
// each file's own id, so a second update over the same checkout writes nothing.
// Outside a checkout it does nothing at all.
//
// Both cases run in the update door's own fixture (convergeRig): an
// installation one release behind, in a real checkout of its repository, whose
// database is a hermetic Realtime Database. Nothing here reaches a network, a
// credential or a real installation.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/tier"
)

// backlogItemText is a well-formed item's bytes, from docs_backlog_test.go's
// own backlogItemDoc and the two headings every item's body carries.
func backlogItemText(id, title, status string) string {
	return backlogItemDoc(id, title, status, "## What was left open\n\n"+title+".\n\n## Why\n\nbecause it was.\n")
}

// backlogItemFile writes one item file into root's docs/backlog/ folder, under
// D26's `<id>-<slug>.md` name with the id repeated in the front matter.
func backlogItemFile(t *testing.T, root, id, slug, title, status string) {
	t.Helper()
	dir := filepath.Join(root, backlog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+"-"+slug+".md"), []byte(backlogItemText(id, title, status)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedBacklogNode writes ops into the fixture's backlog node the way a run's
// own patch does — orchestrator.Apply over the empty state — so a command under
// test reads a node a run would have written. It is the state a checkout that
// was already folded is in: the node holds the item the folder's file stands
// for. The actor is the caller's because one of the states below carries a
// phase, and a phase is the daemon's to write (G12).
func seedBacklogNode(t *testing.T, db *gcpfake.RTDB, actor orchestrator.Actor, ops ...orchestrator.Op) {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops}, actor)
	if err != nil {
		t.Fatalf("seeding the backlog node: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	db.Set(orchestratorPath(t, orchestrator.BacklogNode), json.RawMessage(raw))
}

// archivedBacklogOps are the ops that leave an item in the node's archive and
// in no work list, which is where a folded item ends: filed, then finished and
// archived out (OpWorkItemArchive — `fugaro orchestrator patch archive`, and the
// daemon's own pass over a finished item). Both the phase and the archive entry
// are the daemon's to write.
func archivedBacklogOps(id, title string) []orchestrator.Op {
	done := orchestrator.PhaseDone
	item := workItemUpsert(id, title)
	item.WorkItem.Phase = &done
	return []orchestrator.Op{
		item,
		{Op: orchestrator.OpWorkItemArchive, Archive: &orchestrator.ArchiveOp{ID: id, Outcome: string(done)}},
	}
}

// storedBacklogItems reads the fixture's backlog node back the way the CLI
// reads it: the work items it holds, by id.
func storedBacklogItems(t *testing.T, db *gcpfake.RTDB) map[string]orchestrator.WorkItem {
	t.Helper()
	v := db.Value(orchestratorPath(t, orchestrator.BacklogNode))
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("reading the backlog node back: %v", err)
	}
	byID := make(map[string]orchestrator.WorkItem, len(s.WorkItems))
	for _, w := range s.WorkItems {
		byID[w.ID] = w
	}
	return byID
}

// TestUpdateFoldsOpenBacklogOnce is B2's own test, and its red step: a checkout
// whose docs/backlog/ folder still holds items gets them folded into the
// project's backlog node by `fugaro update` alone — one QUEUED, UNAPPROVED item
// per open file, under the file's own id, briefed with the file's text — and
// the run prints one line with the count (D2716, D2715, D2717).
//
// The closed file is the half that says which items the fold is about: a closed
// or dropped item is history, and the standing backlog holds no item for it.
// The node's own items are the other half, and they are what the "once" means
// (D2716): an id the node already carries is one already filed, whether it is
// still in the work list or has been archived out of it, so the run writes
// nothing for either file. The archived one is the half that is easy to miss —
// archiving is where a folded item normally ends, and a skip that read the work
// list alone would refile it as a fresh queued duplicate.
//
// The door is not run a second time here, for the reason TestUpdateIsIdempotent
// gives: this rig's first run onboards the repository, and a second run's
// repository stage stops over exactly that. What a second update folds is a
// property of the node it reads, and the node is what the seed above writes.
func TestUpdateFoldsOpenBacklogOnce(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	backlogItemFile(t, r.root, "20261021-01", "one", "the first thing left open", "open")
	backlogItemFile(t, r.root, "20261021-02", "two", "the second thing left open", "open")
	backlogItemFile(t, r.root, "20261021-03", "three", "a thing already dealt with", "closed")
	backlogItemFile(t, r.root, "20261021-04", "four", "a thing folded here and finished since", "open")
	// The node as a run that already folded this checkout has it: one item still
	// in the work list, and one that finished and was archived out.
	node := []orchestrator.Op{workItemUpsert("20261021-02", "an item somebody already filed")}
	node = append(node, archivedBacklogOps("20261021-04", "a thing folded here and finished since")...)
	seedBacklogNode(t, r.db, orchestrator.Actor{Class: orchestrator.Daemon, Name: "daemon", Now: orchestratorTestNow}, node...)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if !strings.Contains(out, "folded 1") || !strings.Contains(out, backlog.Dir) {
		t.Errorf("the run does not print the count of what it folded, and the folder it folded it from (D2716):\n%s", out)
	}
	if !strings.Contains(out, "unapproved") {
		t.Errorf("the line does not say the folded items are unapproved: a reader who took the fold for a launch waits for runs that never start (D2717):\n%s", out)
	}

	byID := storedBacklogItems(t, r.db)
	if len(byID) != 2 {
		t.Fatalf("the backlog node holds %d work items, want the one the run already held and the one this update folded: %+v", len(byID), byID)
	}
	// The id the node already carried is the file's own, and the item it held is
	// untouched: the fold writes the ids it does not find and nothing else.
	if got := byID["20261021-02"]; got.Title != "an item somebody already filed" {
		t.Errorf("the item the node already held reads %+v, want the title it was seeded with: a file the node has an item for is folded again (D2716)", got)
	}
	if _, ok := byID["20261021-03"]; ok {
		t.Errorf("the closed file was folded: %+v. A closed or dropped item is history, and the standing backlog holds no item for it (D2716)", byID["20261021-03"])
	}
	// An id the node has archived is an id it knows: the item finished and left
	// the work list, and the folder's file is still there. A skip that read the
	// live items alone would file it again, as a queued duplicate of an item the
	// node has already dealt with (D2716).
	if got, ok := byID["20261021-04"]; ok {
		t.Errorf("an item the node had archived was filed again: %+v", got)
	}

	folded, ok := byID["20261021-01"]
	if !ok {
		t.Fatalf("the open file was not folded: the node holds %+v", byID)
	}
	if folded.Title != "the first thing left open" {
		t.Errorf("the folded item's title is %q, want the file's own title", folded.Title)
	}
	if folded.Spec != "" {
		t.Errorf("the folded item carries spec %q: the backlog is the project's work items with NO spec (D2715)", folded.Spec)
	}
	if folded.Phase != orchestrator.PhaseQueued {
		t.Errorf("the folded item's phase is %q, want queued: a folded item is work nobody has started (D2717)", folded.Phase)
	}
	if folded.Approved != nil {
		t.Errorf("the folded item arrives approved (%+v): approval is the person's own word, and a folded item waits for it (D2717)", folded.Approved)
	}
	if folded.Repo != "acme/app" {
		t.Errorf("the folded item's repository is %q, want the checkout's own origin (acme/app)", folded.Repo)
	}
	// The brief is the file's text: the headings the filer wrote are the reason
	// the item exists, and an item briefed with its title alone would lose them.
	if !strings.Contains(folded.Brief, "## Why") {
		t.Errorf("the folded item's brief is %q, want the file's own text after the front matter", folded.Brief)
	}
	if folded.Recipe != recipe.DefaultName {
		t.Errorf("the folded item's recipe is %q, want %q: a folded item is filed with the defaults the person's approval changes", folded.Recipe, recipe.DefaultName)
	}
	if !tier.Known(tier.Tier(folded.RigorTier)) {
		t.Errorf("the folded item's rigor tier is %q, which is not one of %s", folded.RigorTier, tier.List())
	}
	// The items went to the backlog node and to no other node of the subtree
	// (D2735).
	if v := r.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Errorf("the state node holds a document after a fold, and the items belong to the backlog node: %v", v)
	}
}

// TestUpdateOutsideCheckoutFoldsNothing: the fold is the checkout's, and a run
// that is not standing in one folds nothing at all (D2716) — not the folder the
// run happened to find, not a line about it, not a document at the node. The
// items are in the fixture's checkout, where a folder's items belong, and the
// run is started from a directory that is no checkout.
//
// The run is the door's own and not convergeOn's helper, and that is the
// difference this test is about: from outside a checkout the checkout half of
// the upgrade has nothing to converge — its images and its pin are that
// checkout's — so the run converges the installation and reports those steps as
// not-this-run's, which the sibling test's assertions (doctor clean, the pin
// gone) would read as the fixture being wrong. What is asserted here is what
// the fold owns: it printed no line and wrote nothing, while the run itself
// finished — the release it converged to is named in the output, so a run that
// stopped early fails this test rather than passing it by saying nothing.
func TestUpdateOutsideCheckoutFoldsNothing(t *testing.T) {
	r := newConvergeRig(t, v7Node())
	backlogItemFile(t, r.root, "20261021-01", "one", "the first thing left open", "open")
	backlogItemFile(t, r.root, "20261021-02", "two", "the second thing left open", "open")

	t.Chdir(t.TempDir())
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "aurora\n", "update")
	if err != nil || !strings.Contains(out, convergeRelease) {
		t.Fatalf("the run did not finish (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if strings.Contains(out, "folded") {
		t.Errorf("a run that is not in a checkout folded something:\n%s", out)
	}
	if v := r.db.Value(orchestratorPath(t, orchestrator.BacklogNode)); v != nil {
		t.Errorf("a run that is not in a checkout wrote the backlog node: %v", v)
	}
}
