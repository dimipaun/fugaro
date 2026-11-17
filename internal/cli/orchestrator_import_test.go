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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/testutil"
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

// TestImportDumpJSONIsThePatchDocument: `--json` on the dump path prints
// `orchestrator patch --json`'s own document, because the dump is a patch
// document and the write that landed is the same write. It is additive — the
// prose path is untouched — and it is here so the flag means one thing on both
// paths rather than printing prose over a dump.
func TestImportDumpJSONIsThePatchDocument(t *testing.T) {
	newOrchestratorFixture(t)
	asPerson(t)
	path := patchFile(t, patchDoc(t, 0, workItemUpsert("web-fix", "fix the web build")))

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "-f", path, "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Rev   int64  `json:"rev"`
		Actor string `json:"actor"`
		Ops   int    `json:"ops"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON: %v\n%s", err, out)
	}
	if doc.Kind != "patch" || doc.ID != orchestratorTestID || doc.Actor != string(orchestrator.Person) || doc.Ops != 1 || doc.Rev != 1 {
		t.Errorf("the dump's document is %+v, and the dump lands as the person, one op, at rev 1", doc)
	}
}

// G1: `--plan`, a plan's graph imported into the plan's own node (docs/
// orchestrator-graph-estimates, D2704, D2735, D2737). The unit's own tests
// (internal/orchestrator) hold what one import writes; these hold what the
// command around it does — where it reads the graph and the plan from, that a
// graph it refuses leaves the node untouched, and that --json prints the
// document an approval of the plan's items takes.

// planImportGraph is the fixture's graph: three tasks, A sized trivial, B
// large and C unsized, with B and C waiting on A.
const planImportGraph = `{
  "schema_version": 3,
  "plan": "docs/demo/implementation-plan.md",
  "nodes": [
    {"id": "A", "title": "build the thing", "run": null, "version": null, "tier": "trivial",
     "footprint": {"prediction": true, "paths": ["internal/a.go"]}},
    {"id": "B", "title": "use the thing", "run": null, "version": null, "tier": "large",
     "footprint": {"prediction": true, "paths": ["internal/b.go"]}},
    {"id": "C", "title": "describe the thing", "run": null, "version": null,
     "footprint": {"prediction": true, "paths": ["internal/c.go"]}}
  ],
  "edges": [
    {"task": "B", "needs": "A", "reason": "one file: both edit internal/b.go"},
    {"task": "C", "needs": "A", "reason": "one file: both edit internal/a.go"}
  ]
}
`

// planImportDottedGraph is a graph whose task is numbered the way two of the
// committed plans number theirs, with a dot: the id rule an item id goes by
// admits none (F37), so the import has no item to write for this node.
const planImportDottedGraph = `{
  "schema_version": 3,
  "plan": "docs/demo/implementation-plan.md",
  "nodes": [
    {"id": "SA2.1", "title": "the task numbered with a dot", "run": null, "version": null,
     "footprint": {"prediction": true, "paths": ["internal/a.go"]}}
  ],
  "edges": []
}
`

// planImportSectionlessGraph is a graph naming a task the plan it points at does
// not describe: there is no section to hash, so no brief for it is writeable.
const planImportSectionlessGraph = `{
  "schema_version": 3,
  "plan": "docs/demo/implementation-plan.md",
  "nodes": [
    {"id": "Z", "title": "the task the plan never mentions", "run": null, "version": null,
     "footprint": {"prediction": true, "paths": ["internal/z.go"]}}
  ],
  "edges": []
}
`

// planImportPlanMD is the plan those tasks are described in: one section each,
// which is what a brief pins the hash of.
const planImportPlanMD = `# The demo plan

Prose that belongs to no task.

### A: build the thing

Do A, and nothing else.

- **Acceptance**: A builds.

### B: use the thing

Do B, which needs A.

### C: describe the thing

Do C.
`

// planImportSectionA is A's section as a reader of the plan sees it, written out
// here so the digest the brief carries is checked against a reader's own
// arithmetic rather than the code that produced it.
const planImportSectionA = "### A: build the thing\n\nDo A, and nothing else.\n\n- **Acceptance**: A builds.\n"

// planImportCheckout writes a checkout holding the plan and its graph, with an
// origin remote naming the repository the items are for, and returns its path.
// The command reads the graph off disk and the plan's text at HEAD, so the two
// have to be a real checkout with a real commit for any of this to mean
// anything: a fixture with the files and no history would test neither pin.
func planImportCheckout(t *testing.T, graph, planMD string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main")
	testutil.WriteFiles(t, dir, map[string]string{
		"docs/demo/implementation-plan.graph.json": graph,
		"docs/demo/implementation-plan.md":         planMD,
	})
	// The URL is read and never reached: the import takes the repository's
	// owner/name from origin and asks the network for nothing.
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "the plan and its graph")
	return dir
}

// planImportPath is the plan node's path, built by the package that owns it.
func planImportPath(t *testing.T, plan string) string {
	t.Helper()
	path, err := orchestrator.PathPlan(orchestratorTestID, plan)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// storedPlanState reads a plan's node back the way the CLI reads it.
func storedPlanState(t *testing.T, f *budgetFixture, plan string) orchestrator.State {
	t.Helper()
	v := f.db.Value(planImportPath(t, plan))
	if v == nil {
		t.Fatalf("the node of plan %s is not written", plan)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s, err := orchestrator.DecodeState(raw)
	if err != nil {
		t.Fatalf("reading the plan's node back: %v", err)
	}
	return s
}

// planImportDocDigest is the digest of one task's section, computed the way the
// brief says a run computes it: the SHA-256 of the section's text.
func planImportDocDigest(t *testing.T, section string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(section))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestOrchestratorImportPlanWritesTheGraph is G1 at the command: the graph under
// <folder>/implementation-plan.graph.json becomes the items of the plan's own
// node, and nothing at all of the state node.
//
// Mutation: write into the state node rather than the plan's, and the last
// assertion fails; take the plan name from the graph's node ids rather than the
// folder, and the plan task and the node's path disagree.
func TestOrchestratorImportPlanWritesTheGraph(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportGraph, planImportPlanMD)
	t.Chdir(dir)
	commit := testutil.Git(t, dir, "rev-parse", "HEAD")

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"imported 3 ops into demo's node as a session", "3 of the plan's 3 tasks unapproved"} {
		if !strings.Contains(out, want) {
			t.Errorf("the import does not say %q:\n%s", want, out)
		}
	}
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Error("the import wrote the state node: a plan's items live in the plan's own node (D2735)")
	}

	s := storedPlanState(t, f, "demo")
	if len(s.WorkItems) != 3 {
		t.Fatalf("the plan's node holds %d work items, want one per node of the graph", len(s.WorkItems))
	}
	wantDigest := planImportDocDigest(t, planImportSectionA)
	for _, tc := range []struct {
		id, title, tier string
		deps            []string
	}{
		{id: "A", title: "build the thing", tier: "trivial"},
		{id: "B", title: "use the thing", tier: "large", deps: []string{"A"}},
		{id: "C", title: "describe the thing", tier: "medium", deps: []string{"A"}},
	} {
		w, ok := s.Item(tc.id)
		if !ok {
			t.Fatalf("the graph's task %q was not imported", tc.id)
		}
		if w.Title != tc.title || w.RigorTier != tc.tier || w.Spec != "demo" || w.PlanTask != "demo/"+tc.id {
			t.Errorf("item %q is %q/%s spec %q task %q", tc.id, w.Title, w.RigorTier, w.Spec, w.PlanTask)
		}
		if w.Repo != "acme/app" {
			t.Errorf("item %q is for %q, and origin names acme/app", tc.id, w.Repo)
		}
		if !slices.Equal(w.DependsOn, tc.deps) {
			t.Errorf("item %q depends on %v, want %v", tc.id, w.DependsOn, tc.deps)
		}
		if w.Phase != orchestrator.PhaseQueued || w.Approved != nil {
			t.Errorf("item %q arrived %s and approved by %v, and the import approves nothing", tc.id, w.Phase, w.Approved)
		}
	}
	a, _ := s.Item("A")
	if !strings.Contains(a.Brief, "docs/demo/implementation-plan.md") {
		t.Errorf("A's brief does not name the plan document: %q", a.Brief)
	}
	if !strings.Contains(a.Brief, commit) {
		t.Errorf("A's brief does not pin the checkout's HEAD (%s): %q", commit, a.Brief)
	}
	if !strings.Contains(a.Brief, wantDigest) {
		t.Errorf("A's brief does not carry the SHA-256 of its section (%s): %q", wantDigest, a.Brief)
	}
	if !strings.HasPrefix(a.Brief, "# Task: demo A: build the thing\n") {
		t.Errorf("A's brief does not open with the task's own line: %q", a.Brief)
	}
}

// TestOrchestratorImportPlanReadsThePlanAtTheCommit: the section a brief hashes
// is the section at the commit the brief pins, not whatever the working tree
// holds. A plan edited but not committed is a plan no approval can bind — the
// run reads the plan at the commit — so hashing the working tree would put a
// digest in the brief that the run then refuses to match (F49).
func TestOrchestratorImportPlanReadsThePlanAtTheCommit(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportGraph, planImportPlanMD)
	t.Chdir(dir)
	edited := strings.Replace(planImportPlanMD, "Do A, and nothing else.", "Do A differently, in a way nobody has committed.", 1)
	if err := os.WriteFile(filepath.Join(dir, "docs/demo/implementation-plan.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo"); err != nil {
		t.Fatalf("the import refused a checkout with an uncommitted plan edit: %v", err)
	}
	a, _ := storedPlanState(t, f, "demo").Item("A")
	if !strings.Contains(a.Brief, planImportDocDigest(t, planImportSectionA)) {
		t.Errorf("the brief does not pin the committed section: %q", a.Brief)
	}
	if strings.Contains(a.Brief, planImportDocDigest(t, strings.Replace(planImportSectionA, "Do A, and nothing else.", "Do A differently, in a way nobody has committed.", 1))) {
		t.Error("the brief pinned the section the working tree holds, which no commit carries")
	}
}

// TestOrchestratorImportPlanJSONIsTheDocumentTheApprovalTakes: --json prints the
// items with the digests `plan_approve` takes (D2706), which is the whole reason
// a session can hand the plan over without approving anything itself. The
// document is held to schemas/orchestrator-output.schema.json, the contract its
// readers go by.
func TestOrchestratorImportPlanJSONIsTheDocumentTheApprovalTakes(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportGraph, planImportPlanMD)
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	commit := testutil.Git(t, dir, "rev-parse", "HEAD")

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The schema is read from the checkout this test came from, so the
	// document is held to the contract beside the code rather than to nothing.
	t.Chdir(here)
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Plan    string `json:"plan"`
		Commit  string `json:"commit"`
		Rev     int64  `json:"rev"`
		Ops     int    `json:"ops"`
		Items   []struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON: %v\n%s", err, out)
	}
	if doc.Kind != "plan_import" || doc.ID != orchestratorTestID || doc.Plan != "demo" || doc.Commit != commit {
		t.Errorf("the document is %+v, and it should report the plan import of demo at %s", doc, commit)
	}
	if doc.Ops != 3 || doc.Rev != 1 {
		t.Errorf("the document reports %d ops at rev %d, want 3 at 1", doc.Ops, doc.Rev)
	}
	s := storedPlanState(t, f, "demo")
	seen := map[string]bool{}
	for _, it := range doc.Items {
		w, ok := s.Item(it.ID)
		if !ok {
			t.Fatalf("the document names the item %q, which the node does not hold", it.ID)
		}
		// The digest is the one an approval binds: what the state model itself
		// computes over the item, and not a value the command derived its own
		// way.
		if it.Digest != string(orchestrator.SpecDigest(w)) {
			t.Errorf("the document's digest for %q is %s, and the item hashes to %s", it.ID, it.Digest, orchestrator.SpecDigest(w))
		}
		if w.Approved != nil {
			t.Errorf("the item %q is approved, and the import approves nothing", it.ID)
		}
		seen[it.ID] = true
	}
	for _, id := range []string{"A", "B", "C"} {
		if !seen[id] {
			t.Errorf("the document does not name the item %q", id)
		}
	}
}

// TestOrchestratorImportPlanReimportWritesNothing: running it again over the
// node it wrote changes nothing at all — not a revision, not a digest — which is
// what leaves the approvals a person gave over the plan's items standing
// (D2745). The command says so rather than reporting a write it did not make.
func TestOrchestratorImportPlanReimportWritesNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportGraph, planImportPlanMD)
	t.Chdir(dir)
	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo"); err != nil {
		t.Fatalf("%v", err)
	}
	before := storedPlanState(t, f, "demo")
	digests := map[string]string{}
	for _, w := range before.WorkItems {
		digests[w.ID] = string(orchestrator.SpecDigest(w))
	}

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to write") {
		t.Errorf("the second import does not say it wrote nothing:\n%s", out)
	}
	after := storedPlanState(t, f, "demo")
	if after.Rev != before.Rev {
		t.Errorf("the second import moved the node from rev %d to %d, and it wrote no op", before.Rev, after.Rev)
	}
	for _, w := range after.WorkItems {
		if digests[w.ID] != string(orchestrator.SpecDigest(w)) {
			t.Errorf("the item %q changed under a re-import that wrote nothing", w.ID)
		}
	}
}

// TestOrchestratorImportPlanRefusedGraphWritesNothing: a graph this build cannot
// honour is refused whole, and the plan's node is left exactly as it was — not
// created, not partly written. This is G1's own acceptance sentence, and the
// graph here is one of the two committed plans whose task ids carry dots (F37).
func TestOrchestratorImportPlanRefusedGraphWritesNothing(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportDottedGraph, planImportPlanMD)
	t.Chdir(dir)

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo")
	if err == nil {
		t.Fatalf("a graph whose task ids carry dots was imported:\n%s", out)
	}
	if !strings.Contains(err.Error(), "F37") {
		t.Errorf("the refusal does not say why the id cannot be one: %v", err)
	}
	if v := f.db.Value(planImportPath(t, "demo")); v != nil {
		t.Error("the refused graph wrote the plan's node")
	}
	if v := f.db.Value(orchestratorPath(t, orchestrator.StateNode)); v != nil {
		t.Error("the refused graph wrote the state node")
	}

	// The same graph with a task the plan describes in no section is refused
	// too: a brief pins the section's hash, and there is none to pin.
	dir = planImportCheckout(t, planImportSectionlessGraph, planImportPlanMD)
	t.Chdir(dir)
	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo"); err == nil {
		t.Error("a graph with a task the plan does not describe was imported")
	} else if !strings.Contains(err.Error(), "describes no section") {
		t.Errorf("the refusal does not say the plan describes no such section: %v", err)
	}
}

// TestOrchestratorImportRefusesTwoDocumentsAtOnce, and the bare run: `import`
// loads one document or the other, and it says which flags there are rather than
// picking one. A bare run reads nothing at all — the closed stdin of the
// no-prompt harness is no document — which is why the refusal is made before any
// read.
func TestOrchestratorImportRefusesTwoDocumentsAtOnce(t *testing.T) {
	f := newOrchestratorFixture(t)
	dir := planImportCheckout(t, planImportGraph, planImportPlanMD)
	t.Chdir(dir)
	path := patchFile(t, patchDoc(t, 0, workItemUpsert("web-fix", "fix the web build")))

	if _, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo", "-f", path); err == nil {
		t.Error("the import accepted a plan and a dump in one run")
	} else if !strings.Contains(err.Error(), "not both") {
		t.Errorf("the refusal does not say two documents were given: %v", err)
	}
	out, _, err := runOrchestrator(t, "", "orchestrator", "import")
	if err == nil {
		t.Fatalf("a bare import read something:\n%s", out)
	}
	for _, want := range []string{"--plan", "-f"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if v := f.db.Value(planImportPath(t, "demo")); v != nil {
		t.Error("a refused run wrote the plan's node")
	}
}

// TestOrchestratorImportPlanNeedsACheckout: the graph is read from the plan
// folder and the plan itself from git, so a run with no checkout to read is
// refused rather than importing items with no pinned commit in them.
func TestOrchestratorImportPlanNeedsACheckout(t *testing.T) {
	newOrchestratorFixture(t)
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	_, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/demo")
	if err == nil {
		t.Fatal("the import ran outside a checkout")
	}
	if !strings.Contains(err.Error(), "checkout") {
		t.Errorf("the refusal does not say a checkout is needed: %v", err)
	}
}

// TestOrchestratorImportPlanRefusesAPlanBelowDocs: a plan this command imports
// is a folder of its own under docs/, because its name is the node's property
// and the items' spec. A graph deeper than that — the plan documents in
// docs/plans/ are named by date and title — has no single name to key them by, so
// it is refused with the reason rather than keyed by a guess.
func TestOrchestratorImportPlanRefusesAPlanBelowDocs(t *testing.T) {
	f := newOrchestratorFixture(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main")
	testutil.WriteFiles(t, dir, map[string]string{
		"docs/plans/2026-01-01-the-demo/implementation-plan.graph.json": strings.Replace(planImportGraph, "docs/demo/", "docs/plans/2026-01-01-the-demo/", 1),
		"docs/plans/2026-01-01-the-demo/implementation-plan.md":         planImportPlanMD,
	})
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/app.git")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "a plan below docs")
	t.Chdir(dir)

	out, _, err := runOrchestrator(t, "", "orchestrator", "import", "--plan", "docs/plans/2026-01-01-the-demo")
	if err == nil {
		t.Fatalf("a plan below docs/ was imported:\n%s", out)
	}
	if !strings.Contains(err.Error(), "folder of its own under docs/") {
		t.Errorf("the refusal does not say where a plan this command imports sits: %v", err)
	}
	if v := f.db.Value(planImportPath(t, "plans")); v != nil {
		t.Error("the refused plan wrote a node")
	}
}
