package cli

// `fugaro backlog add` files a WORK ITEM (docs/orchestrator-graph-estimates, B1,
// D2715–D2717): a `work_item_upsert` with no spec, written to the orchestrator's
// own backlog node — `orchestrator/<project>/backlog` (D2735) — queued and
// unapproved, so nothing runs it until a person approves it. The id it mints is
// what it prints, and the folder it used to write a Markdown file into is
// history (D2716): its red step is exactly that, a command that wrote a file
// and now writes a node.
//
// Every case runs in newOrchestratorFixture's world: the project config and the
// hermetic Realtime Database, whose clock is pinned to 2026-11-08, so the day an
// id carries is the fixture's and not the machine's. Nothing here reaches a
// network, a credential or a real installation.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/tier"
)

// addArgs is the least an item needs: the one line it is about and the
// repository it is about. --repo is given rather than derived in every case but
// the one that tests the derivation, because otherwise the run would read the
// origin of whatever checkout the tests happen to run in.
func addArgs(extra ...string) []string {
	return append([]string{
		"backlog", "add", "--repo", "acme/app",
		"--title", "Review round 2 left 3 findings open",
	}, extra...)
}

// addDay is the day the fixture's clock answers with and the day an id minted in
// this file carries (orchestratorTestNow's own day).
const addDay = "20261108"

// storedBacklogState reads the fixture's backlog node back the way the CLI reads
// it. A node nobody wrote is this orchestrator's empty state, exactly as Load
// answers, so a case about "nothing was written" reads it too.
func storedBacklogState(t *testing.T, f *budgetFixture) orchestrator.State {
	t.Helper()
	v := f.db.Value(orchestratorPath(t, orchestrator.BacklogNode))
	if v == nil {
		return orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: orchestratorTestID, Role: orchestrator.RoleOrchestrator}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("reading the backlog node back: %v", err)
	}
	return s
}

// TestBacklogAddWritesUnapprovedItem is B1's own test, and its red step: filing
// an item writes a work item with no spec to the backlog node, queued and
// unapproved (D2717), prints the id it minted, and writes no file into
// docs/backlog/ — the folder is history (D2716).
func TestBacklogAddWritesUnapprovedItem(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	out, _, err := execute(t, addArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := addDay + "-01"
	if !strings.Contains(out, want) {
		t.Errorf("fugaro backlog add printed %q, want the id it minted (%s)", out, want)
	}
	if !strings.Contains(out, "unapproved") {
		t.Errorf("the command does not say the item is unapproved: a reader who took the write for a launch waits for a run that never starts (D2717):\n%s", out)
	}

	s := storedBacklogState(t, f)
	if len(s.WorkItems) != 1 {
		t.Fatalf("the backlog node holds %d work items, want the one this run filed: %+v", len(s.WorkItems), s.WorkItems)
	}
	w := s.WorkItems[0]
	if w.ID != want {
		t.Errorf("the item's id is %q, want %q (the fixture's day and the day's first sequence, D26)", w.ID, want)
	}
	if w.Spec != "" {
		t.Errorf("the item carries spec %q: the backlog is the project's work items with NO spec (D2715)", w.Spec)
	}
	if w.Phase != orchestrator.PhaseQueued {
		t.Errorf("the item's phase is %q, want queued: a filed item is work nobody has started (D2717)", w.Phase)
	}
	if w.Approved != nil {
		t.Errorf("the item arrives approved (%+v): approval is the person's own word, and a filed item waits for it (D2717)", w.Approved)
	}
	if w.Title != "Review round 2 left 3 findings open" || w.Repo != "acme/app" {
		t.Errorf("the item is %q in %q, want the title and the repository the command was given", w.Title, w.Repo)
	}
	// The brief is what the run is launched with, and it is never empty: the
	// title is the brief a filer who writes no file gives.
	if w.Brief == "" {
		t.Errorf("the item carries no brief: a work item is a declaration the daemon launches a run from (D2717)")
	}
	// The two fields the state model requires of every item and this command has
	// no flag for are written with the defaults the item stands for until a person
	// changes them.
	if w.Recipe != recipe.DefaultName {
		t.Errorf("the item's recipe is %q, want %q", w.Recipe, recipe.DefaultName)
	}
	if !tier.Known(tier.Tier(w.RigorTier)) {
		t.Errorf("the item's rigor tier is %q, which is not one of %s", w.RigorTier, tier.List())
	}
	// The state node was not written: the item went to the backlog node and to no
	// other node of the subtree (D2735).
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Errorf("the state node holds a document after a filing, and the item belongs to the backlog node: %v", v)
	}
}

// TestBacklogAddWritesNoFile: the folder is history (D2716), so the command
// takes no --dir and writes nothing there. It is the other half of the red step:
// the command that used to write `docs/backlog/<id>-<slug>.md` now writes a node
// and a node only.
func TestBacklogAddWritesNoFile(t *testing.T) {
	isolateState(t)
	newOrchestratorFixture(t)
	dir := filepath.Join(t.TempDir(), "backlog")
	_, _, err := execute(t, addArgs("--dir", dir)...)
	if err == nil {
		t.Fatal("fugaro backlog add took --dir: the folder is history and the command writes no file into it (D2716)")
	}
	if !strings.Contains(err.Error(), "dir") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}
	if entries, rerr := os.ReadDir(dir); rerr == nil && len(entries) != 0 {
		t.Errorf("the command wrote %d files into %s", len(entries), dir)
	}
}

// TestBacklogAddAllocatesTheDaysNextID: a second filing the same day takes the
// next sequence — the ids are read off the node the item is written to (D26) —
// so two items filed a moment apart are two ids rather than one overwritten.
func TestBacklogAddAllocatesTheDaysNextID(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	if _, _, err := execute(t, addArgs()...); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, addArgs("--title", "Hotfix left the migration unrun")...)
	if err != nil {
		t.Fatal(err)
	}
	want := addDay + "-02"
	if !strings.Contains(out, want) {
		t.Errorf("the second filing printed %q, want the day's next sequence (%s, D26)", out, want)
	}
	s := storedBacklogState(t, f)
	if len(s.WorkItems) != 2 {
		t.Fatalf("the backlog node holds %d work items, want the two this run filed", len(s.WorkItems))
	}
}

// TestBacklogAddBriefFileCarriesTheBrief: --brief-file's contents are the brief
// the run is launched with, which is what makes a filed item more than its
// title; without the flag the title is the brief.
func TestBacklogAddBriefFileCarriesTheBrief(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	brief := filepath.Join(t.TempDir(), "brief.md")
	body := "# What to do\n\nread the round's findings and close them out\n"
	if err := os.WriteFile(brief, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, addArgs("--brief-file", brief)...); err != nil {
		t.Fatal(err)
	}
	s := storedBacklogState(t, f)
	if len(s.WorkItems) != 1 {
		t.Fatalf("the backlog node holds %d work items, want one", len(s.WorkItems))
	}
	if !strings.Contains(s.WorkItems[0].Brief, "read the round's findings") {
		t.Errorf("the item's brief is %q, want the file's contents", s.WorkItems[0].Brief)
	}
}

// TestBacklogAddDefaultsTheRepositoryToTheCheckout: --repo is optional, and what
// it defaults to is the checkout's own origin — the repository the person is
// standing in — the same default `fugaro run --repo` has.
func TestBacklogAddDefaultsTheRepositoryToTheCheckout(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	t.Chdir(dir)
	if _, _, err := execute(t, "backlog", "add", "--title", "a title"); err != nil {
		t.Fatal(err)
	}
	s := storedBacklogState(t, f)
	if len(s.WorkItems) != 1 {
		t.Fatalf("the backlog node holds %d work items, want one", len(s.WorkItems))
	}
	if s.WorkItems[0].Repo != "acme/app" {
		t.Errorf("the item's repository is %q, want acme/app, the checkout's own origin", s.WorkItems[0].Repo)
	}
}

// TestBacklogAddRefusesWhatItCannotWrite: the refusals this command owns, all of
// them before anything is written — no title, a title over the state model's
// cap, a brief file that is not there, an empty one and one over the cap, and no
// repository to default to. A refusal writes nothing to the node.
func TestBacklogAddRefusesWhatItCannotWrite(t *testing.T) {
	isolateState(t)
	f := newOrchestratorFixture(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(dir, "long.md")
	if err := os.WriteFile(long, []byte(strings.Repeat("a", orchestrator.MaxBriefRunes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no title", []string{"backlog", "add", "--repo", "acme/app"}, "--title"},
		{"a title over the cap", addArgs("--title", strings.Repeat("t", orchestrator.MaxTitleRunes+1)), "title"},
		{"a brief file that is not there", addArgs("--brief-file", filepath.Join(dir, "nothing.md")), "brief"},
		{"an empty brief file", addArgs("--brief-file", empty), "empty"},
		{"a brief over the cap", addArgs("--brief-file", long), "brief"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatal("the filing was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %q: %v", tc.want, err)
			}
			if s := storedBacklogState(t, f); len(s.WorkItems) != 0 {
				t.Errorf("a refused filing wrote %d work items to the backlog node", len(s.WorkItems))
			}
		})
	}

	// And with no repository to read a default from, the refusal names the flag
	// rather than writing an item with an empty repository, which the state model
	// would refuse a step later with a sentence about owner/name.
	t.Chdir(t.TempDir())
	if _, _, err := execute(t, "backlog", "add", "--title", "a title"); err == nil {
		t.Fatal("a filing outside a checkout and with no --repo was accepted")
	} else if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("the refusal does not name --repo: %v", err)
	}
}
