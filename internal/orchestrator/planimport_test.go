package orchestrator_test

// G1: a plan's graph as the work items of the plan's own node (D2704, D2735,
// D2737, D2745, D2746). What these tests hold is the set of promises the import
// makes to the person who approves what it wrote: one item per node with the
// plan's own names on it, a brief that pins the task's section so an approval
// binds its content rather than a pointer (F49), a re-import that changes
// nothing a run is working from and nothing already approved, and a refused
// graph that leaves the node exactly as it was.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// planImportCommit is the commit every brief of the fixture's first import
// pins, and planImportNext is the one a later re-import runs at.
const (
	planImportCommit = "3c1d0e5b7a91f2c4d6e8091a2b3c4d5e6f708192"
	planImportNext   = "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"
)

// planImportDoc is the plan the fixture's graph describes: a title, prose that
// belongs to no task, and one section per node.
const planImportDoc = `# The demo plan

Prose that belongs to no task.

### A: build the thing

Do A, and nothing else.

- **Acceptance**: A builds.

### B: use the thing

Do B, which needs A.

### C: describe the thing

Do C.
`

// planImportNodes are the graph's nodes: A and B are sized and C is not. C is
// the case the default is for — none of the nine graphs committed when this was
// written carries a tier at all, so every node of every one of them arrives
// unsized.
func planImportNodes() []orchestrator.PlanImportNode {
	return []orchestrator.PlanImportNode{
		{ID: "A", Title: "build the thing", Tier: "trivial"},
		{ID: "B", Title: "use the thing", Tier: "large"},
		{ID: "C", Title: "describe the thing"},
	}
}

// planImportInput is one import of that graph, at the older commit.
func planImportInput() orchestrator.PlanImportInput {
	return orchestrator.PlanImportInput{
		Plan:     "demo",
		Repo:     "acme/repo",
		Recipe:   "default",
		Commit:   planImportCommit,
		PlanText: planImportDoc,
		Nodes:    planImportNodes(),
		Edges: []orchestrator.PlanImportEdge{
			{Task: "B", Needs: "A"},
			{Task: "C", Needs: "A"},
		},
	}
}

// planImportState is the plan's node as an import finds it when nothing has
// been written to it: no items at all, at revision 0.
func planImportState() orchestrator.State {
	return orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       "orchestrator-a",
		Role:          orchestrator.RoleOrchestrator,
	}
}

// planImportWorkItem is a plan item as the daemon writes one when it adopts a
// run in flight (D2702): the run's id as the item's id, the launch's plan task,
// and the run's own brief.
func planImportWorkItem(id, planTask string) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID: id, Title: "the run the daemon adopted", Repo: "acme/repo",
		Recipe: "default", RigorTier: "small", Spec: "demo", PlanTask: planTask,
		Brief: "the brief the person approved", Phase: orchestrator.PhaseRunning, RunID: id,
	}
}

// planImportOp is the upsert the patch carries for one item, or false when
// nothing in it writes that item.
func planImportOp(p orchestrator.Patch, id string) (orchestrator.WorkItemOp, bool) {
	for _, op := range p.Ops {
		if op.Op == orchestrator.OpWorkItemUpsert && op.WorkItem != nil && op.WorkItem.ID == id {
			return *op.WorkItem, true
		}
	}
	return orchestrator.WorkItemOp{}, false
}

// applyPlanImport is the whole write: the patch the import built, applied by a
// session, which is the class the command lands as.
func applyPlanImport(t *testing.T, s orchestrator.State, p orchestrator.Patch, now time.Time) orchestrator.State {
	t.Helper()
	return mustApply(t, s, orchestrator.Session, "session:20261116-120000-abcd", now, p.Ops...)
}

// TestPlanImportOneItemPerNode is G1's red step: the graph becomes one work item
// per node, under the plan's own names, with the graph's edges as dependencies.
// Before the import existed there was no `--plan` at all, and an item reached a
// node only through a hand-written patch.
//
// Mutation: write the node's title as the item's id, or the graph's node ids
// as the plan tasks, and the assertions below fail; drop the edges and B and C
// arrive depending on nothing.
func TestPlanImportOneItemPerNode(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := planImportState()

	p, ids, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the import refused a graph it is meant to read: %v", err)
	}
	if !slices.Equal(ids, []string{"A", "B", "C"}) {
		t.Fatalf("the import names the items %v, and one item per node in the graph's order is what it is for", ids)
	}
	if len(p.Ops) != 3 || p.BaseRev != s.Rev {
		t.Fatalf("the import built %d ops on revision %d, want 3 on %d", len(p.Ops), p.BaseRev, s.Rev)
	}
	next := applyPlanImport(t, s, p, now)
	if next.Rev != s.Rev+1 {
		t.Errorf("the write left the node at revision %d, want %d", next.Rev, s.Rev+1)
	}

	for _, tc := range []struct {
		id, title, tier string
		deps            []string
	}{
		{id: "A", title: "build the thing", tier: "trivial"},
		{id: "B", title: "use the thing", tier: "large", deps: []string{"A"}},
		// A node the planner did not size is medium, the reading the estimate
		// makes of one (D2722): every graph committed before the tier field
		// existed carries none, so refusing them would leave the import useless
		// on the plans waiting for it.
		{id: "C", title: "describe the thing", tier: "medium", deps: []string{"A"}},
	} {
		w, ok := next.Item(tc.id)
		if !ok {
			t.Fatalf("the import wrote no item %q", tc.id)
		}
		if w.Title != tc.title || w.RigorTier != tc.tier {
			t.Errorf("item %q is %q/%s, want %q/%s", tc.id, w.Title, w.RigorTier, tc.title, tc.tier)
		}
		if w.Repo != "acme/repo" || w.Recipe != "default" {
			t.Errorf("item %q carries %s/%s, and the import's own names are acme/repo/default", tc.id, w.Repo, w.Recipe)
		}
		// The plan's folder is the item's spec and its plan task's first half,
		// which is how the daemon's adoption and this import meet (D2702). The
		// two are separate fields because an item's id admits neither a dot nor
		// a slash (F37) and a plan task is `<plan>/<task>`.
		if w.Spec != "demo" || w.PlanTask != "demo/"+tc.id {
			t.Errorf("item %q is spec %q, plan task %q; want demo and demo/%s", tc.id, w.Spec, w.PlanTask, tc.id)
		}
		if !slices.Equal(w.DependsOn, tc.deps) {
			t.Errorf("item %q depends on %v, want %v (the graph's edges)", tc.id, w.DependsOn, tc.deps)
		}
		if w.Phase != orchestrator.PhaseQueued {
			t.Errorf("item %q arrived in phase %q, and an imported item has been launched by nobody", tc.id, w.Phase)
		}
		if w.Approved != nil {
			t.Errorf("item %q arrived approved by %q, and this import lands as a session: approving is the person's own word (D2706)", tc.id, w.Approved.By)
		}
	}

	// A patch a session builds over its own node's rules is one Apply accepts,
	// which is what the write above proves — and the whole of it is one patch,
	// so a graph that fails halfway writes none of its items.
	if _, err := orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Session, Name: "session", Now: now}); err != nil {
		t.Errorf("a session could not land the import: %v", err)
	}
}

// TestPlanImportBriefPinsCommitAndSectionHash is D2737's fence: the brief names
// the plan document, the task's section and the commit, and carries the SHA-256
// of that section — so what the person approves is the text of the task rather
// than a pointer that may move under the approval (F49).
//
// The hash is computed here from the section written out by hand, so the rule
// the import hashes by — the heading's line through the last line before the
// next heading of the same or a higher level, trailing blank lines dropped — is
// pinned by a reader that does not share the code that implements it.
func TestPlanImportBriefPinsCommitAndSectionHash(t *testing.T) {
	sectionA := "### A: build the thing\n\nDo A, and nothing else.\n\n- **Acceptance**: A builds.\n"
	sum := sha256.Sum256([]byte(sectionA))
	digest := "sha256:" + hex.EncodeToString(sum[:])

	got, ok := orchestrator.PlanSectionDigest(planImportDoc, "A")
	if !ok {
		t.Fatal("the plan's own section for A was not found")
	}
	if got != digest {
		t.Errorf("the section's digest is %s, and the section a reader sees hashes to %s", got, digest)
	}

	want := "# Task: demo A: build the thing\n" +
		`Read your task from docs/demo/implementation-plan.md, section "### A:", at commit ` + planImportCommit +
		", whose SHA-256 is " + digest + ": read the plan at that commit and refuse rather than work from a section whose hash differs.\n"
	if brief := orchestrator.PlanBrief("demo", "A", "build the thing", planImportCommit, digest); brief != want {
		t.Errorf("the import's brief is\n%q\nwant\n%q", brief, want)
	}

	// And the item the import writes carries that brief, not one built from
	// anything else: the node's title, the plan's folder and the two pins.
	p, _, err := orchestrator.PlanImportPatch(planImportState(), planImportInput())
	if err != nil {
		t.Fatalf("the import refused the fixture's graph: %v", err)
	}
	op, ok := planImportOp(p, "A")
	if !ok || op.Brief == nil {
		t.Fatalf("the import wrote no brief for A: %+v", op)
	}
	if *op.Brief != want {
		t.Errorf("the item's brief is\n%q\nwant\n%q", *op.Brief, want)
	}

	// The heading has to be the task's own: `### AB:` is AB's section and not
	// A's, and a plan that describes a task in no heading at all has no section
	// to hash — which is a refusal in the import, not an empty brief.
	if _, ok := orchestrator.PlanSectionDigest("### AB: not A\n\nText.\n", "A"); ok {
		t.Error("a heading naming another task was read as A's section")
	}
	if _, ok := orchestrator.PlanSectionDigest(planImportDoc, "D"); ok {
		t.Error("the plan describes no D, and the digest was answered anyway")
	}
	if _, ok := orchestrator.PlanSectionDigest("### E\n\nText.\n", "E"); !ok {
		t.Error("a heading that is the id and nothing else is that task's section")
	}
}

// TestPlanImportLinksAdoptedItemAndRewritesEdges is D2746: the daemon adopts a
// run in flight as an item keyed by `<plan>/<task>` before any session imports
// the plan, and the import meets that item rather than writing a second one
// beside it — so the node holds one item per task, and every edge the graph
// draws to the node names the id the item really has. Writing the graph's own
// task id would be a dependency on an item no node holds, which the validator
// refuses (F56).
func TestPlanImportLinksAdoptedItemAndRewritesEdges(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := planImportState()
	adopted := planImportWorkItem("20261116-110000-abcd", "demo/A")
	adopted.Adopted = true
	s.WorkItems = []orchestrator.WorkItem{adopted}

	p, ids, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the import refused a graph whose task the daemon had adopted: %v", err)
	}
	if !slices.Equal(ids, []string{adopted.ID, "B", "C"}) {
		t.Fatalf("the import names the items %v, and A's item is the one the daemon adopted (%s)", ids, adopted.ID)
	}
	if _, ok := planImportOp(p, adopted.ID); ok {
		t.Error("the import wrote an op over the adopted item: what a run in flight is working from is not a session's to rewrite (D2704)")
	}
	for _, id := range []string{"B", "C"} {
		op, ok := planImportOp(p, id)
		if !ok || op.DependsOn == nil {
			t.Fatalf("the import wrote no dependencies for %q: %+v", id, op)
		}
		if !slices.Equal(*op.DependsOn, []string{adopted.ID}) {
			t.Errorf("%q depends on %v, and the graph's edge names A — which is the adopted item %s (D2746)", id, *op.DependsOn, adopted.ID)
		}
	}
	next := applyPlanImport(t, s, p, now)
	if _, ok := next.Item("A"); ok {
		t.Error("the node carries an item under the graph's task id as well as the adopted one: one task is one item (D2746)")
	}
	if w, _ := next.Item(adopted.ID); w.Title != adopted.Title || w.Brief != adopted.Brief {
		t.Errorf("the adopted item was rewritten: %+v", w)
	}
}

// TestReimportKeepsCommitOfUnchangedSection is D2745, and it is F55's whole
// point: `SpecDigest` hashes the brief, so a re-import at a newer commit that
// rewrote every brief would revoke every approval the plan had. A section whose
// hash did not change keeps the brief it was given — the older commit included
// — and so keeps the approval given over it; a section that did change gets the
// new commit and needs approving again.
//
// Mutation: always write the brief with the incoming commit and A's digest
// moves, its approval is cleared, and the person is asked to approve the same
// text a second time.
func TestReimportKeepsCommitOfUnchangedSection(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := planImportState()
	p, _, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the first import refused the fixture's graph: %v", err)
	}
	s = applyPlanImport(t, s, p, now)
	a, ok := s.Item("A")
	if !ok {
		t.Fatal("the first import wrote no item A")
	}
	s = mustApply(t, s, orchestrator.Person, "owner", now, approveOp("A", orchestrator.SpecDigest(a)))
	approved := s.WorkItems[slices.IndexFunc(s.WorkItems, func(w orchestrator.WorkItem) bool { return w.ID == "A" })]

	// The plan moved on: a new commit, another task, and B's section edited.
	in := planImportInput()
	in.Commit = planImportNext
	in.PlanText = strings.Replace(planImportDoc, "Do B, which needs A.", "Do B, which needs A, and a little more.", 1) +
		"\n### D: the task that arrived\n\nDo D.\n"
	in.Nodes = append(in.Nodes, orchestrator.PlanImportNode{ID: "D", Title: "the task that arrived", Tier: "small"})

	p, _, err = orchestrator.PlanImportPatch(s, in)
	if err != nil {
		t.Fatalf("the re-import refused the plan it moved to: %v", err)
	}
	if op, ok := planImportOp(p, "A"); ok {
		t.Errorf("the re-import rewrote A, whose section did not change (D2745): %+v", op)
	}
	next := applyPlanImport(t, s, p, now)
	a, _ = next.Item("A")
	if a.Brief != approved.Brief {
		t.Errorf("A's brief is now\n%q\nand it was\n%q", a.Brief, approved.Brief)
	}
	if !strings.Contains(a.Brief, planImportCommit) {
		t.Errorf("A's brief no longer pins the commit it was approved at (%s): %q", planImportCommit, a.Brief)
	}
	if a.Approved == nil {
		t.Fatal("the re-import cleared A's approval, and its section was not touched (F55)")
	}
	if a.Approved.SpecDigest != orchestrator.SpecDigest(a) {
		t.Errorf("A stands approved at %s and its spec now hashes to %s", a.Approved.SpecDigest, orchestrator.SpecDigest(a))
	}
	// The other half of the rule: a section that did change is a new brief at
	// the new commit, so its approval — if it had one — would not carry over.
	b, _ := next.Item("B")
	if !strings.Contains(b.Brief, planImportNext) || strings.Contains(b.Brief, planImportCommit) {
		t.Errorf("B's section changed and its brief is %q, which should pin the newer commit (%s)", b.Brief, planImportNext)
	}
	if d, ok := next.Item("D"); !ok || !strings.Contains(d.Brief, planImportNext) {
		t.Errorf("the task the plan gained was not imported: %+v", d)
	}
}

// TestPlanImportIsIdempotent: importing the same graph over the state it wrote
// lands nothing at all. It is not only tidiness — an op that rewrites an item
// with the same values takes the revision on and re-hashes the spec, and a
// patch that changes nothing is one the format refuses to apply, so the caller
// has to know there is nothing to write rather than hand it an empty patch.
//
// Mutation: compare the ops against nothing rather than against the item as it
// stands, and the second import builds three ops, `Apply` refuses the patch,
// and the command fails on a graph it had already imported.
func TestPlanImportIsIdempotent(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := planImportState()
	p, ids, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the first import refused the fixture's graph: %v", err)
	}
	s = applyPlanImport(t, s, p, now)
	before := make([]orchestrator.WorkItem, len(s.WorkItems))
	copy(before, s.WorkItems)

	p, again, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the second import refused the graph the first one read: %v", err)
	}
	if len(p.Ops) != 0 {
		t.Fatalf("the second import built %d ops over the state it had just written", len(p.Ops))
	}
	if !slices.Equal(ids, again) {
		t.Errorf("the two imports name the items %v and %v", ids, again)
	}
	if err := orchestrator.ValidatePatch(p); err == nil {
		t.Error("an empty patch passed the format's own validator, and the caller is what has to refuse to write it")
	} else if !errors.Is(err, orchestrator.ErrInvalid) {
		t.Errorf("the empty patch was refused with %v, want the format's own invalid-op error", err)
	}
	if !reflect.DeepEqual(before, s.WorkItems) {
		t.Error("the second import changed the state without writing anything")
	}
}

// TestPlanImportLeavesLaunchedItems is D2704's other half: a re-import updates
// the items nothing has launched yet and touches no other. An item out of
// `queued`, one carrying a run id and one an attempt has been committed for are
// all launched — the third is the case that matters, because a run that failed
// puts its item back in `queued` and only the attempt records that it ever ran.
//
// Mutation: judge "launched" by the phase alone, and the failed item's brief is
// rewritten under a run whose approval covered the old one.
func TestPlanImportLeavesLaunchedItems(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	running := planImportWorkItem("20261116-110000-abcd", "demo/A")
	spent := orchestrator.WorkItem{
		ID: "C", Title: "the title the failed run worked from", Repo: "acme/repo",
		Recipe: "default", RigorTier: "small", Spec: "demo", PlanTask: "demo/C",
		Brief: "the brief its attempt was committed under", Phase: orchestrator.PhaseQueued,
		RunID: "20261116-110001-abcd", Attempts: 1,
	}
	s := planImportState()
	s.WorkItems = []orchestrator.WorkItem{running, spent}
	s.Intents = []orchestrator.Intent{{
		ID: orchestrator.IntentID("C", 1), RunID: spent.RunID,
		SpecDigest: orchestrator.SpecDigest(spent), ApprovalAt: now.Add(-time.Hour), At: now.Add(-time.Hour),
		Outcome: orchestrator.OutcomeFailed,
	}}

	p, ids, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the import refused a node with a launched item in it: %v", err)
	}
	if !slices.Equal(ids, []string{running.ID, "B", spent.ID}) {
		t.Fatalf("the import names the items %v, want the graph's nodes against the items that stand", ids)
	}
	for _, id := range []string{running.ID, spent.ID} {
		if op, ok := planImportOp(p, id); ok {
			t.Errorf("the import wrote an op over the launched item %q (D2704): %+v", id, op)
		}
	}
	if _, ok := planImportOp(p, "B"); !ok {
		t.Error("the import wrote nothing for B, which nothing has launched")
	}
	next := applyPlanImport(t, s, p, now)
	for _, want := range []orchestrator.WorkItem{running, spent} {
		w, ok := next.Item(want.ID)
		if !ok {
			t.Fatalf("the item %q disappeared", want.ID)
		}
		if w.Title != want.Title || w.Brief != want.Brief || w.Phase != want.Phase || w.RunID != want.RunID {
			t.Errorf("the import rewrote the launched item %q: %+v, want %+v", want.ID, w, want)
		}
	}
	// And what the import did write is the graph's own task, not a second item
	// beside the one the run holds.
	if _, ok := next.Item("A"); ok {
		t.Error("the node holds an item for A as well as the running run's item")
	}
}

// TestPlanImportRefusedGraphWritesNothing: every refusal is made before a patch
// exists, and the patch a refusal returns is empty — so a caller that writes
// what it was handed writes exactly nothing, which is the whole of what "a
// refused graph writes nothing" means (G1's acceptance).
//
// The refusals are the graph shapes that cannot be honoured, each of which has
// a plan in the repository behind it or a rule of the state model: a task
// numbered with a dot (F37 — the graph's ids are the planner's, and `idRE`
// allows none), a task the plan describes in no section (D2737 pins a hash, and
// there is none to pin), a task id an item of the node already holds for
// another plan's task, an edge naming a task that is not a node, a graph whose
// patch would be longer than a patch may be, and the names a node's path is
// built from.
func TestPlanImportRefusedGraphWritesNothing(t *testing.T) {
	dotted := planImportInput()
	dotted.Nodes = []orchestrator.PlanImportNode{{ID: "SA2.1", Title: "the dotted task", Tier: "small"}}

	undescribed := planImportInput()
	undescribed.Nodes = append(undescribed.Nodes, orchestrator.PlanImportNode{ID: "D", Title: "no section", Tier: "small"})

	edges := planImportInput()
	edges.Edges = []orchestrator.PlanImportEdge{{Task: "B", Needs: "Z"}}

	selfDep := planImportInput()
	selfDep.Edges = []orchestrator.PlanImportEdge{{Task: "B", Needs: "B"}}

	big := planImportInput()
	big.Nodes = nil
	big.Edges = nil
	for i := 0; i <= orchestrator.MaxOps; i++ {
		id := fmt.Sprintf("T%d", i)
		big.Nodes = append(big.Nodes, orchestrator.PlanImportNode{ID: id, Title: "task " + id, Tier: "small"})
		big.PlanText += "\n### " + id + "\n\nDo it.\n"
	}

	noPlan := planImportInput()
	noPlan.Plan = "docs/demo"

	badRepo := planImportInput()
	badRepo.Repo = "not-a-repo"

	badCommit := planImportInput()
	badCommit.Commit = "HEAD"

	// A node whose id another plan's item already holds: the import writes into
	// the plan's own node and an item's id is unique inside it, so an id taken
	// by an item that is not this plan's is a collision the import refuses
	// rather than a name to write over.
	colliding := planImportState()
	colliding.WorkItems = []orchestrator.WorkItem{{
		ID: "A", Title: "another plan's task", Repo: "acme/repo", Recipe: "default",
		RigorTier: "small", Spec: "other", PlanTask: "other/A",
		Brief: "another plan's brief", Phase: orchestrator.PhaseQueued,
	}}

	ambiguous := planImportState()
	ambiguous.WorkItems = []orchestrator.WorkItem{
		planImportWorkItem("20261116-110000-abcd", "demo/A"),
		{ID: "A", Title: "a second item for the same task", Repo: "acme/repo", Recipe: "default",
			RigorTier: "small", Spec: "demo", PlanTask: "demo/A", Brief: "a brief", Phase: orchestrator.PhaseQueued},
	}

	for _, tc := range []struct {
		name  string
		state orchestrator.State
		in    orchestrator.PlanImportInput
		why   string
	}{
		{"a task numbered with a dot", planImportState(), dotted, "not a work item id"},
		{"a task the plan does not describe", planImportState(), undescribed, "describes no section"},
		{"a plan folder that is not one name", planImportState(), noPlan, "not a plan folder name"},
		{"a repository that is not owner/name", planImportState(), badRepo, "not owner/name"},
		{"a commit that is not a git object", planImportState(), badCommit, "not a git object name"},
		{"an edge to a task that is not a node", planImportState(), edges, "not a node of the graph"},
		{"a node that depends on itself", planImportState(), selfDep, "depends on itself"},
		{"a graph longer than a patch", planImportState(), big, "at most"},
		{"a task id another plan's item holds", colliding, planImportInput(), "is not this plan's"},
		{"two items for one plan task", ambiguous, planImportInput(), "one work item"},
	} {
		p, ids, err := orchestrator.PlanImportPatch(tc.state, tc.in)
		if err == nil {
			t.Errorf("%s: the import accepted the graph and built %d ops", tc.name, len(p.Ops))
			continue
		}
		if !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: the refusal is %v, and it should say %q", tc.name, err, tc.why)
		}
		if !errors.Is(err, orchestrator.ErrInvalid) && !errors.Is(err, orchestrator.ErrTooLarge) {
			t.Errorf("%s: the refusal is %v, and it is neither invalid input nor an oversize graph", tc.name, err)
		}
		if len(p.Ops) != 0 || p.BaseRev != 0 {
			t.Errorf("%s: the refusal returned a patch of %d ops at revision %d, and a refused graph writes nothing", tc.name, len(p.Ops), p.BaseRev)
		}
		if ids != nil {
			t.Errorf("%s: the refusal returned the ids %v beside it", tc.name, ids)
		}
	}
}
