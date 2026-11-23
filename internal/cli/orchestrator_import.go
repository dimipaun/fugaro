package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// orchestratorImportOptions are `fugaro orchestrator import`'s flags.
type orchestratorImportOptions struct {
	cloudOptions
	// name is --name: which of the project's orchestrators the document is
	// imported into (D3206, D3207). Empty is the project's own.
	name string
	file string
	plan string
	json bool
}

func newOrchestratorImportCmd() *cobra.Command {
	var o orchestratorImportOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Load a plan's graph, or the one-time session dump, into the orchestrator",
		Long: "Load work into the orchestrator from one of two documents, never both.\n\n" +
			"--plan <folder> imports a plan: it reads <folder>/implementation-plan.graph.json\n" +
			"with fugaro plan's own reader and writes the plan's node (orchestrator <id>/\n" +
			"plans/<plan>) one work item per node of the graph — the task's id, the plan's\n" +
			"name as its spec, <plan>/<task> as its plan task, the graph's edges as its\n" +
			"dependencies, and a brief that pins the plan's commit and the SHA-256 of the\n" +
			"task's own section (D2704, D2737). A re-import writes only the items nothing\n" +
			"has launched yet and keeps an unchanged section's brief, so an approval a\n" +
			"person already gave still stands (D2745); a graph this build refuses writes\n" +
			"nothing at all. It lands as a session, which is who runs it — the session in\n" +
			"the checkout the plan merged in — so it approves nothing: the items arrive\n" +
			"unapproved, and --json prints them with the digests approving them takes.\n\n" +
			"-f loads the one-time session dump (the spec's Migration), from its file or\n" +
			"from stdin with -f -. The dump is one patch document\n" +
			"(schemas/orchestrator-patch.schema.json) a session wrote out, and it is read\n" +
			"by the same validator and bounded by the same caps as fugaro orchestrator\n" +
			"patch: it is the one document in this loop written by a model, and everything\n" +
			"it carries is data, never an instruction.\n\n" +
			"The dump lands as the person who runs this, because the migration is theirs\n" +
			"to make, and it lands nothing a session may not land: approving a work\n" +
			"item, setting the goal and closing a decision that waits on anyone but a\n" +
			"session are the person's own word, given at a real terminal over the spec\n" +
			"they read (fugaro orchestrator patch). So no dump approves work and no dump\n" +
			"closes a decision of the person's — not even one the dump raises and closes\n" +
			"itself — and every work item the dump declares arrives unapproved.\n\n" +
			"--name imports into one of the project's named orchestrators instead of the\n" +
			"project's own: `--name night` writes `<project>~night`'s plan and state nodes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorImport(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.StringVarP(&o.file, "file", "f", "", "the session dump to import, or - to read it from stdin")
	f.StringVar(&o.plan, "plan", "", "the plan to import, as its folder under docs/ (docs/<plan>): reads its implementation-plan.graph.json into the plan's node")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// importedUnapproved counts the work items the dump declares that stand
// unapproved in the state the import wrote. It reads the state the write left
// rather than the dump's own ops, so what the run reports is what the node
// holds: an item the dump declares that was already approved, and stays
// approved, is not reported as one the person still has to approve, and a dump
// whose approval was somehow honoured reports nothing.
func importedUnapproved(next orchestrator.State, p orchestrator.Patch) int {
	seen := map[string]bool{}
	n := 0
	for _, op := range p.Ops {
		if op.Op != orchestrator.OpWorkItemUpsert || op.WorkItem == nil || seen[op.WorkItem.ID] {
			continue
		}
		seen[op.WorkItem.ID] = true
		if w, ok := next.Item(op.WorkItem.ID); ok && w.Approved == nil {
			n++
		}
	}
	return n
}

func runOrchestratorImport(cmd *cobra.Command, o *orchestratorImportOptions) error {
	switch {
	case o.plan != "" && o.file != "":
		return userErr("pass --plan (a plan's graph) or -f (a session dump), not both: they load two different documents")
	case o.plan != "":
		return runOrchestratorPlanImport(cmd, o)
	case o.file == "":
		return userErr("nothing to import: pass --plan <folder> to import a plan's graph, or -f <file> to load a session dump")
	}
	return runOrchestratorDumpImport(cmd, o)
}

// orchestratorPlanImportDoc is the document `fugaro orchestrator import --plan
// --json` prints: what the import wrote, and the plan's items with the digests
// the approval of them takes (D2706).
//
// The items are every node of the graph in the graph's own order, each with the
// id the item stands under in the node and the digest of the spec the plan
// declared — so the list is what `plan_approve` takes whether the import wrote
// the item now or found it standing, and whether it was linked to an adopted
// item's id or not. `ops` is what this run wrote, and an idempotent re-import
// reports zero of them beside the same list.
type orchestratorPlanImportDoc struct {
	Version int                       `json:"version"`
	Kind    string                    `json:"kind"`
	ID      string                    `json:"id"`
	Plan    string                    `json:"plan"`
	Commit  string                    `json:"commit"`
	Rev     int64                     `json:"rev"`
	Ops     int                       `json:"ops"`
	Items   []orchestratorPlanItemDoc `json:"items"`
}

// orchestratorPlanItemDoc is one item of the plan: the item's id in the node,
// and the digest of the spec it stands for.
type orchestratorPlanItemDoc struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// runOrchestratorPlanImport is `--plan`: the graph a session reads in its
// checkout, written as one typed patch into the plan's own node (D2704, D2735).
//
// Three reads make the pins in every brief true, and they are the reason this
// is not a one-line import. The graph is read off disk, where the plan folder
// is; the commit is the checkout's HEAD; and the plan's own text is read *at
// that commit* with `git show` rather than off disk, so a section whose hash
// goes into a brief is a section that is in the commit named beside it even
// when the working tree has moved on. What the person then approves is a
// section of the plan as the commit pinned it (F49, D2737).
//
// It lands as a session and never as a person: the party that runs it is the
// session in the checkout the plan merged in, and nothing it writes approves
// anything — an approval is the person's own word, given later over the items
// this prints (D2706).
func runOrchestratorPlanImport(cmd *cobra.Command, o *orchestratorImportOptions) error {
	ctx := cmd.Context()
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return userErr("importing a plan reads it out of a checkout: run this from one (git rev-parse --show-toplevel failed: %v)", err)
	}
	folder := o.plan
	if !filepath.IsAbs(folder) {
		// --plan is a plan folder, and a plan folder is named from the
		// repository root: docs/<plan> reads the same from a subdirectory as it
		// does from the root, and the path every brief pins is the same one.
		folder = filepath.Join(root, folder)
	}
	doc, err := plan.ReadFile(filepath.Join(folder, "implementation-plan.graph.json"))
	if err != nil {
		return userErr("%v: nothing was written", err)
	}
	rel, err := filepath.Rel(root, folder)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return userErr("the plan folder %s is not inside the checkout %s, and a plan's name is where it sits under docs/: nothing was written", safetext.Field(folder), safetext.Field(root))
	}
	rel = filepath.ToSlash(rel)
	name := plan.IDFromPath(rel + "/implementation-plan.graph.json")
	if strings.Contains(name, "/") {
		// A plan's name is one path element: a plan in a folder of its own,
		// docs/<plan>. The name becomes the node's property and the items'
		// spec, and IDFromPath answers with a path for the artifacts that sit
		// in the plan documents' own directory — which is a plan whose graph
		// this import cannot place rather than one to name after a guess.
		return userErr("the plan sits at %s, and a plan this command imports is a folder of its own under docs/ (docs/<plan>): a plan below that has no single name to key its node and its items by, and nothing was written", safetext.Field(rel))
	}
	// Where the plan sits and which document it is are both fixed by the brief,
	// so both are checked here rather than trusted. Every brief this import
	// writes sends a run to docs/<plan>/implementation-plan.md, with the plan's
	// name folded the way the node's own path folds it (specName, R3), and the
	// section it hashes is read from the document the graph names. The two have
	// to be one file: a folder outside docs/, one whose case the fold changes,
	// or a graph naming another document in its folder would put a digest over
	// one file in a brief that sends the run to another, which is the approval
	// F49 and D2737 exist to make impossible.
	if want := plan.DocsDir + "/" + task.ASCIILower(name); rel != want {
		return userErr("the plan sits at %s, and a plan this command imports sits at %s/<plan> with the plan's name folded (%s): the brief of every item it writes names that path, and a plan anywhere else would carry a digest over a document the run never reads; nothing was written", safetext.Field(rel), safetext.Field(plan.DocsDir), safetext.Field(want))
	}
	if doc.Plan != rel+"/implementation-plan.md" {
		return userErr("the graph names the plan document %s, and the brief of every item it writes sends a run to %s/implementation-plan.md: the plan, its graph and its briefs are one document, and nothing was written", safetext.Field(doc.Plan), safetext.Field(rel))
	}
	commit, err := gitRead(ctx, ".", "rev-parse", "HEAD")
	if err != nil {
		return userErr("reading the checkout's HEAD, which is the commit every brief pins: %v; nothing was written", err)
	}
	text, err := gitCmd(ctx, ".", "show", commit+":"+filepath.ToSlash(doc.Plan)).Output()
	if err != nil {
		return userErr("reading %s at HEAD (%s), which is the plan every brief pins and whose sections the briefs hash: %v; nothing was written", safetext.Field(doc.Plan), safetext.Field(commit), err)
	}
	repo, err := originRepo(ctx)
	if err != nil {
		return err
	}
	in := orchestrator.PlanImportInput{
		Plan:     name,
		Repo:     repo,
		Recipe:   recipe.DefaultName,
		Commit:   commit,
		PlanText: string(text),
		Nodes:    make([]orchestrator.PlanImportNode, 0, len(doc.Nodes)),
		Edges:    make([]orchestrator.PlanImportEdge, 0, len(doc.Edges)),
	}
	for _, n := range doc.Nodes {
		// Every node is an ordinary item, the contract and integration kinds
		// included (D2704): what the kind says is how the planner broke the
		// work up, and a run reads its task from the plan's own section.
		t := ""
		if n.Tier != nil {
			t = string(*n.Tier)
		}
		in.Nodes = append(in.Nodes, orchestrator.PlanImportNode{ID: n.ID, Title: n.Title, Tier: t})
	}
	for _, e := range doc.Edges {
		in.Edges = append(in.Edges, orchestrator.PlanImportEdge{Task: e.Task, Needs: e.Needs})
	}

	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
		return err
	}
	id, err := orchestratorIDFor(lc, o.name)
	if err != nil {
		return err
	}
	node, err := orchestratorNodeShard(db, id, orchestrator.PlanShard(name))
	if err != nil {
		return err
	}
	s, etag, err := node.Load(ctx)
	if err != nil {
		return dbErr(lc, err, false)
	}
	now, ok := db.ServerNow()
	if !ok {
		return userErr("the database answered without its own clock, and every stamp this import writes comes from it (D1230)")
	}
	p, ids, err := orchestrator.PlanImportPatch(s, in)
	if err != nil {
		return userErr("%v: nothing was written", err)
	}
	next := s
	if len(p.Ops) > 0 {
		next, err = orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Session, Name: "import", Now: now})
		if err != nil {
			if errors.Is(err, orchestrator.ErrStaleRev) {
				return userErr("%v: read the node again with %s and run this import once more", err, orchestratorStateCommand(o.name))
			}
			return userErr("%v: nothing was written", err)
		}
		if err := node.Save(ctx, next, etag); err != nil {
			if errors.Is(err, orchestrator.ErrStateMoved) {
				return userErr("the plan's node moved since this run read it (rev %d), so the graph was not imported: run it once more", s.Rev)
			}
			return dbErr(lc, err, true)
		}
	}
	items, unapproved := planImportItems(next, ids)
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorPlanImportDoc{
			Version: orchestratorOutputVersion, Kind: "plan_import", ID: id,
			Plan: name, Commit: commit, Rev: next.Rev, Ops: len(p.Ops), Items: items,
		})
	}
	if len(p.Ops) == 0 {
		fmt.Fprintf(w, "nothing to write: %s's node already carries the graph's %d %s at commit %s (orchestrator %s is at rev %d)\n",
			safetext.Field(name), len(ids), plural(len(ids), "task", "tasks"), safetext.Field(commit), safetext.Field(id), next.Rev)
	} else {
		fmt.Fprintf(w, "imported %d %s into %s's node as a session: orchestrator %s is at rev %d\n",
			len(p.Ops), plural(len(p.Ops), "op", "ops"), safetext.Field(name), safetext.Field(id), next.Rev)
	}
	if unapproved > 0 {
		fmt.Fprintf(w, "%d of the plan's %d %s unapproved: approving them is your own word at a real terminal, and --json prints the {id, digest} list an approval takes\n",
			unapproved, len(ids), plural(len(ids), "task", "tasks"))
	}
	return nil
}

// planImportItems reads the plan's items back out of the node the import left,
// in the graph's own order: each one's digest — what an approval binds — and
// how many of them stand unapproved.
//
// It reads the state rather than the ops, so the list is the same one whether
// the import wrote an item, found it standing, or linked to an item the daemon
// had already adopted: what a person approves is the item in the node, and that
// is what the digest is taken from.
func planImportItems(s orchestrator.State, ids []string) ([]orchestratorPlanItemDoc, int) {
	items := make([]orchestratorPlanItemDoc, 0, len(ids))
	unapproved := 0
	for _, id := range ids {
		w, ok := s.Item(id)
		if !ok {
			continue
		}
		if w.Approved == nil {
			unapproved++
		}
		items = append(items, orchestratorPlanItemDoc{ID: id, Digest: string(orchestrator.SpecDigest(w))})
	}
	return items, unapproved
}

// runOrchestratorDumpImport is `-f`: the one-time session dump.
func runOrchestratorDumpImport(cmd *cobra.Command, o *orchestratorImportOptions) error {
	ctx := cmd.Context()
	// The dump is a patch document and is read as one, by patch's own reader:
	// the byte cap that stops a document nobody could have read is the same cap,
	// and half a dump is not a dump.
	raw, err := readPatchDocument(cmd, o.file)
	if err != nil {
		return err
	}
	p, err := orchestrator.DecodePatch(raw)
	if err != nil {
		return userErr("the dump is not a document this release reads: %v", err)
	}
	t, err := openOrchestrator(ctx, o.cloudOptions, o.name)
	if err != nil {
		return err
	}
	if err := requireOwnBudgetDB(ctx, t.lc, t.db, "nothing was written"); err != nil {
		return err
	}
	s, etag, err := t.node.Load(ctx)
	if err != nil {
		return dbErr(t.lc, err, false)
	}
	now, ok := t.db.ServerNow()
	if !ok {
		return userErr("the database answered without its own clock, and every stamp this import writes comes from it (D1230)")
	}
	// The person's own ops are refused before the person's name is looked up,
	// because nothing about this run may land them: what an approval, the goal
	// and a decision the session does not own need is the person's word at a
	// terminal over the spec they read, and a dump is model output.
	//
	// The question is put to the state model rather than to a list of op names
	// kept here: the document is folded as a session would fold it, and what a
	// session may not land is the model's own answer. A scan of the dump's op
	// names against the state read before it would miss a decision the dump
	// raises and closes in the same document — the decision is not in the state
	// yet when the scan runs, so its class reads as nobody's and the resolve
	// looks like any other — and the fold is the same code the write below
	// runs, so the two cannot disagree.
	if _, err := orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Session, Now: now}); errors.Is(err, orchestrator.ErrActorNotAllowed) {
		return userErr("nothing was written: a dump is a session's own record and lands nothing a session may not land — approving a work item, setting the goal and closing a decision that waits on anyone but a session are the person's own word at a real terminal over the spec they read (fugaro orchestrator patch): %v", err)
	}
	name, err := authenticatedUser(ctx, t.lc)
	if err != nil {
		return userErr("the dump lands as the person whose migration it is, and this run could not read who its credentials belong to: %v", err)
	}
	next, err := orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Person, Name: name, Now: now})
	if err != nil {
		if errors.Is(err, orchestrator.ErrStaleRev) {
			return userErr("%v: the dump carries the revision it was built from, and it has to be the node's — read it with %s and write the dump again from the revision it prints", err, orchestratorStateCommand(o.name))
		}
		return userErr("%v: nothing was written", err)
	}
	if err := t.node.Save(ctx, next, etag); err != nil {
		if errors.Is(err, orchestrator.ErrStateMoved) {
			return userErr("the state node moved since this run read it (rev %d), so the dump was not written: read the state again with %s and write the dump from the revision it prints", s.Rev, orchestratorStateCommand(o.name))
		}
		return dbErr(t.lc, err, true)
	}
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorPatchDoc{
			Version: orchestratorOutputVersion, Kind: "patch", ID: t.id,
			Rev: next.Rev, Actor: string(orchestrator.Person), Ops: len(p.Ops),
		})
	}
	fmt.Fprintf(w, "imported %d %s as a %s: orchestrator %s is at rev %d\n",
		len(p.Ops), plural(len(p.Ops), "op", "ops"), orchestrator.Person, safetext.Field(t.id), next.Rev)
	if n := importedUnapproved(next, p); n > 0 {
		fmt.Fprintf(w, "%d work %s the dump declares arrived unapproved: approving one is your own word at a real terminal (fugaro orchestrator patch)\n",
			n, plural(n, "item", "items"))
	}
	return nil
}
