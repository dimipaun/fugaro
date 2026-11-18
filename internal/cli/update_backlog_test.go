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

// backlogItemFile writes one item file into root's docs/backlog/ folder, in the
// format backlog.Parse reads: D26's `<id>-<slug>.md` name with the id repeated
// in the front matter, D27's eight fields, and the two headings every item's
// body carries.
func backlogItemFile(t *testing.T, root, id, slug, title, status string) {
	t.Helper()
	dir := filepath.Join(root, backlog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\n" +
		"id: " + id + "\n" +
		"title: " + title + "\n" +
		"mode: none\n" +
		"area: repo\n" +
		"status: " + status + "\n" +
		"filed-by: unknown\n" +
		"filed-in: hand\n" +
		"date: 2026-10-21\n" +
		"---\n\n" +
		"## What was left open\n\n" + title + ".\n\n" +
		"## Why\n\nbecause it was.\n"
	if err := os.WriteFile(filepath.Join(dir, id+"-"+slug+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedBacklogNode writes items into the fixture's backlog node the way a
// session's own patch does — orchestrator.Apply over the empty state — so a
// command under test reads a node a run would have written. It is the state a
// checkout that was already folded is in: the node holds the item the folder's
// file stands for.
func seedBacklogNode(t *testing.T, db *gcpfake.RTDB, ops ...orchestrator.Op) {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID,
		Role:          orchestrator.RoleOrchestrator,
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops},
		orchestrator.Actor{Class: orchestrator.Session, Name: "session", Now: orchestratorTestNow})
	if err != nil {
		t.Fatalf("seeding the backlog node: %v", err)
	}
	raw, err := orchestrator.EncodeState(next)
	if err != nil {
		t.Fatal(err)
	}
	db.Set(orchestratorPath(t, orchestrator.BacklogNode), json.RawMessage(raw))
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
// The node's own item is the other half, and it is what the "once" means
// (D2716): the id the node already carries is one already filed, so the run
// writes nothing for that file and leaves the item it found exactly as it was.
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
	seedBacklogNode(t, r.db, workItemUpsert("20261021-02", "an item somebody already filed"))

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

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
