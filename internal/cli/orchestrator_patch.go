package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// maxPatchDocumentBytes bounds one patch document. O0 bounds the ops at
// orchestrator.MaxOps, and this bounds the bytes a caller can hand the command
// before the document is even parsed: a megabyte is far past the largest patch
// the format admits and far short of anything a run should read into memory on
// a person's behalf.
const maxPatchDocumentBytes = 1 << 20

// orchestratorPatchOptions are `fugaro orchestrator patch`'s flags.
type orchestratorPatchOptions struct {
	cloudOptions
	// name is --name: which of the project's orchestrators the document lands
	// on (D3206, D3207). Empty is the project's own.
	name string
	file string
	json bool
}

func newOrchestratorPatchCmd() *cobra.Command {
	var o orchestratorPatchOptions
	cmd := &cobra.Command{
		Use:   "patch",
		Short: "Apply one typed patch document to the orchestrator's state",
		Long: "Apply one patch document (schemas/orchestrator-patch.schema.json) to the\n" +
			"project's orchestrator state, from -f's file or from stdin with -f -. The\n" +
			"document is validated and applied by the state model's own code, and the write\n" +
			"is conditional on the revision this run read, so a patch built from a state\n" +
			"somebody else has since changed is refused rather than landed on top of it.\n\n" +
			"A run acts as a session. The ops only a person may land — approving a work\n" +
			"item, setting the goal, and closing a decision that waits on a person — are\n" +
			"confirmed at a real terminal, where the project's name is typed over the write\n" +
			"that lands: the spec an approval binds, and every op the document carries,\n" +
			"because the whole of it lands as the person who confirms it. Every other\n" +
			"session, --json, a pipe and a coding agent's environment are refused and write\n" +
			"nothing, and no flag covers them.\n" +
			"No flag, environment variable or field of the document makes this command the\n" +
			"daemon or the decision agent: the daemon's bookkeeping is the daemon's.\n\n" +
			"--name lands the document on one of the project's named orchestrators instead of\n" +
			"the project's own: `--name night` writes `<project>~night`'s nodes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorPatch(cmd, &o) },
	}
	f := cmd.Flags()
	addOrchestratorNameFlag(cmd, &o.name)
	f.StringVarP(&o.file, "file", "f", "", "the patch document to apply, or - to read it from stdin")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// orchestratorPatchDoc is the document `patch --json` prints: the revision the
// write landed at and which class of actor made it.
type orchestratorPatchDoc struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Rev     int64  `json:"rev"`
	Actor   string `json:"actor"`
	Ops     int    `json:"ops"`
}

// personOnly reports whether op is an op only a person may land (D1225, G2):
// approving a work item, approving a plan's imported items (D2706, D2717),
// setting the goal, or closing a decision that waits on a person. The state it
// is asked with is the one the patch starts from, because which class a
// decision waits on is a fact about the node as it stands. A decision this run
// cannot find is not one of them: Apply refuses an unknown id before it looks
// at the class, so nothing is written either way.
func personOnly(before orchestrator.State, op orchestrator.Op) bool {
	switch op.Op {
	case orchestrator.OpWorkItemApprove:
		return op.Approve != nil
	case orchestrator.OpPlanApprove:
		return op.PlanApprove != nil
	case orchestrator.OpGoalSet:
		return true
	case orchestrator.OpDecisionResolve:
		if op.Resolve == nil {
			return false
		}
		d, ok := before.Decision(op.Resolve.ID)
		return ok && d.WaitingOn.Class() == orchestrator.Person
	}
	return false
}

// personOnlyOps counts the ops of p that only a person may land: what decides
// whether this run needs the person's typed confirmation at all.
func personOnlyOps(before orchestrator.State, p orchestrator.Patch) int {
	n := 0
	for _, op := range p.Ops {
		if personOnly(before, op) {
			n++
		}
	}
	return n
}

// patchShard is the node a document is applied to (D2735): the one an op of it
// names, or the state node when no op names one.
//
// Apply is handed one node's document and cannot see the others, which is what
// makes `plan_approve` name the node whose items it approves and what makes an
// item of another node unapprovable by it (itemOfNode, patch_plan.go). So the
// node has to be chosen before Apply runs, and the document itself is what says
// which: `plan_approve` is the only op of the vocabulary that names a node — a
// plan folder, or the standing backlog (D2706, D2717) — and a document with no
// approval op is the state node's, which is every document that was landed
// before the plans had nodes of their own.
//
// A document that would have to land on two nodes is refused rather than
// guessed at, and it is refused whole. One document lands on one node: an
// approval that rode beside an op of another node would half land, and nothing
// would say which half. So an approval travels on its own — a document that
// approves a node's items carries that approval and no other op — and a
// document naming two nodes (two plans, or a plan and the backlog) is refused
// with both named. The names are compared as the paths they build, so a plan
// written in two cases is one node here as it is everywhere else (PathPlan,
// specName).
func patchShard(id string, p orchestrator.Patch) (orchestrator.Shard, error) {
	shard := orchestrator.ShardState
	named, namedPath, others := "", "", 0
	for _, op := range p.Ops {
		if op.Op != orchestrator.OpPlanApprove || op.PlanApprove == nil {
			others++
			continue
		}
		name := op.PlanApprove.Node
		s, path, err := patchShardOf(id, name)
		if err != nil {
			return "", err
		}
		switch {
		case named == "":
			named, namedPath, shard = name, path, s
		case path != namedPath:
			return "", userErr("this document approves the items of two nodes (%s and %s), and one document lands on one node: send one document per node, and nothing was written", safetext.Field(named), safetext.Field(name))
		}
	}
	if named == "" {
		return shard, nil
	}
	if others > 0 {
		return "", userErr("this document approves the items of %s and carries %d other %s beside it: an approval lands on %s's own node and every other op of the vocabulary lands on the state node, and one document lands on one node; send the approval on its own, and nothing was written",
			safetext.Field(named), others, plural(others, "op", "ops"), safetext.Field(named))
	}
	return shard, nil
}

// patchShardOf is one node name as the shard it is and the path that shard
// stands at, inside the orchestrator id the command resolved: the standing
// backlog's, or a plan's. The name is checked here rather than by the node's
// construction so that a document naming something that is neither is refused
// with the fix printed and nothing read — the same refusal Apply gives an
// approval op, one step earlier. The id is the command's own resolution and
// never rebuilt here, so an approval of a named orchestrator's plan lands under
// the id the rest of the run acts on and not under the project's own.
func patchShardOf(id, name string) (orchestrator.Shard, string, error) {
	if name == orchestrator.BacklogNode {
		path, err := orchestrator.PathBacklog(id)
		if err != nil {
			return "", "", userErr("%v: nothing was written", err)
		}
		return orchestrator.ShardBacklog, path, nil
	}
	path, err := orchestrator.PathPlan(id, name)
	if err != nil {
		return "", "", userErr("an approval names %s as the node whose items it approves, and that is not a plan folder or the backlog: %v; nothing was written", safetext.Field(name), err)
	}
	return orchestrator.PlanShard(name), path, nil
}

// itemWords is a work item's digested spec under a head line: every field
// SpecDigest covers and nothing else. That digest is what an approval binds,
// and a person who approves a spec the terminal did not show them has approved
// nothing they read — so the title, the repository, the recipe, the rigor tier,
// the spec, the dependencies and the brief are all here, taken from the state
// the patch leaves behind rather than from the op's own fields, which may be a
// partial declaration over an item that already stands.
//
// The spec is on this list because a session may write it and the person
// approving the item is then attesting to a label name — one no other field on
// the screen implies (T8, docs/forge-labels).
//
// Nothing here is cut, and nothing needs to be: every one of these fields is
// bounded by the state model's own caps (MaxTitleRunes, MaxBriefRunes), and a
// value the terminal shortened is a value the person was not shown.
func itemWords(head string, w orchestrator.WorkItem) []string {
	lines := []string{head}
	if w.Title != "" {
		lines = append(lines, "title: "+oneLine(w.Title))
	}
	where := make([]string, 0, 4)
	for _, f := range []struct{ name, value string }{
		{"repo", w.Repo}, {"recipe", w.Recipe}, {"rigor", w.RigorTier}, {"spec", w.Spec},
	} {
		if f.value != "" {
			where = append(where, f.name+" "+oneLine(f.value))
		}
	}
	if len(where) > 0 {
		lines = append(lines, strings.Join(where, ", "))
	}
	if len(w.DependsOn) > 0 {
		deps := make([]string, 0, len(w.DependsOn))
		for _, d := range w.DependsOn {
			deps = append(deps, safetext.Field(d))
		}
		lines = append(lines, "depends_on "+strings.Join(deps, ", "))
	}
	if w.Brief != "" {
		lines = append(lines, "brief: "+oneLine(w.Brief))
	}
	return lines
}

// decisionWords is a decision as the confirmation shows it, from the state the
// patch leaves behind: the question, the class it waits on, and — once it is
// closed — the answer that lands.
func decisionWords(d orchestrator.Decision) string {
	what := safetext.Field(d.ID)
	if d.WaitingOn != "" {
		what += fmt.Sprintf(" waits on a %s", oneLine(string(d.WaitingOn)))
	}
	if d.Question != "" {
		what += ": " + oneLine(d.Question)
	}
	if d.Answer != "" {
		what += " — with the answer: " + oneLine(d.Answer)
	}
	return what
}

// opWords is one op of the patch as the confirmation shows it: one line, or the
// line and the spec under it for a work item.
func opWords(after orchestrator.State, op orchestrator.Op) []string {
	switch op.Op {
	case orchestrator.OpWorkItemUpsert:
		if op.WorkItem == nil {
			return []string{"writing a work item"}
		}
		w, ok := after.Item(op.WorkItem.ID)
		if !ok {
			return []string{"writing work item " + safetext.Field(op.WorkItem.ID)}
		}
		return itemWords("writing work item "+safetext.Field(w.ID)+":", w)
	case orchestrator.OpWorkItemApprove:
		if op.Approve == nil {
			return []string{"approving a work item"}
		}
		head := fmt.Sprintf("approving work item %s against spec %s:", safetext.Field(op.Approve.ID), safetext.Field(string(op.Approve.SpecDigest)))
		w, ok := after.Item(op.Approve.ID)
		if !ok {
			return []string{head}
		}
		return itemWords(head, w)
	case orchestrator.OpPlanApprove:
		if op.PlanApprove == nil {
			return []string{"approving a plan's items"}
		}
		lines := []string{"approving the items of " + safetext.Field(op.PlanApprove.Node) + " the list names:"}
		for _, it := range op.PlanApprove.Items {
			// Every item of the list is shown the way a single approval shows
			// one: the digest the list carries, and the spec that digest binds
			// as the patch leaves it. A list of ids and digests the person
			// never saw is not an approval of anything they read (D2706,
			// D2738), which is what the op's own id list exists for.
			head := fmt.Sprintf("  %s against spec %s:", safetext.Field(it.ID), safetext.Field(string(it.Digest)))
			w, ok := after.Item(it.ID)
			if !ok {
				lines = append(lines, head)
				continue
			}
			sub := itemWords(head, w)
			lines = append(lines, sub[0])
			for _, l := range sub[1:] {
				lines = append(lines, "  "+l)
			}
		}
		return lines
	case orchestrator.OpWorkItemArchive:
		if op.Archive == nil {
			return []string{"archiving a work item"}
		}
		what := "archiving work item " + safetext.Field(op.Archive.ID)
		if w, ok := after.Item(op.Archive.ID); ok && w.Title != "" {
			what += " (" + oneLine(w.Title) + ")"
		}
		return []string{what}
	case orchestrator.OpDecisionAdd:
		if op.Decision == nil {
			return []string{"raising a decision"}
		}
		d, ok := after.Decision(op.Decision.ID)
		if !ok {
			return []string{"raising decision " + safetext.Field(op.Decision.ID)}
		}
		return []string{"raising decision " + decisionWords(d)}
	case orchestrator.OpDecisionResolve:
		if op.Resolve == nil {
			return []string{"closing a decision"}
		}
		d, ok := after.Decision(op.Resolve.ID)
		if !ok {
			return []string{"closing decision " + safetext.Field(op.Resolve.ID)}
		}
		return []string{"closing decision " + decisionWords(d)}
	case orchestrator.OpFactSet:
		if op.Fact == nil {
			return []string{"setting a fact"}
		}
		value := op.Fact.Value
		if f, ok := after.Facts[op.Fact.Key]; ok {
			value = f.Value
		}
		return []string{fmt.Sprintf("setting fact %s = %s", safetext.Field(op.Fact.Key), oneLine(value))}
	case orchestrator.OpFactDelete:
		if op.Fact == nil {
			return []string{"deleting a fact"}
		}
		return []string{"deleting fact " + safetext.Field(op.Fact.Key)}
	case orchestrator.OpGoalSet:
		return []string{"setting the goal of the loop to: " + oneLine(after.Goal)}
	}
	return []string{string(op.Op)}
}

// patchWords is the whole patch as the person reads it before typing their
// project's name. Every op of the document is listed, in the order it is
// applied: the typed confirmation raises the whole patch to Person, so the ops
// that ride along with a person's own — a decision closed on behalf of the
// daemon or a session, an upsert, a fact, an archive — land under the person's
// authority too, and an op that lands that way without being shown is one the
// person never agreed to. The ops that need the person's own word are marked.
//
// Everything printed comes from the state the patch leaves behind (after) and
// never from the state it starts at: what the confirmation attests to is the
// text that lands. So a goal_set shows the goal, an approval shows every field
// its digest binds — which a work_item_upsert in the same document may have
// rewritten, and does so before the approval is applied — and a decision shows
// the question and the answer it is closed with.
func patchWords(before, after orchestrator.State, p orchestrator.Patch) string {
	lines := make([]string, 0, len(p.Ops))
	for _, op := range p.Ops {
		block := opWords(after, op)
		if personOnly(before, op) {
			block[0] = "[person-only] " + block[0]
		}
		lines = append(lines, block...)
	}
	// The ops that ride along land as the person as well (there is one actor per
	// document), so the head says so: the markers name the ops that need the
	// person's own word, not the only ones this confirmation covers.
	return "this patch lands as a person, and every op below is applied as you —" +
		" the ones marked [person-only] are ops only a person may land:\n  " +
		strings.Join(lines, "\n  ")
}

// confirmPersonPatch is the fence on the person's ops (D1225, G2). The typed
// confirmation at a real terminal is the whole of it: a coding agent's
// environment is refused as well, but that check is a mitigation against the
// accident and not a barrier — whoever unsets the marker is outside it — which
// is why nothing here rests on it. --yes never covers this class, and no
// argument reaches it. The state it is given as after is the one the patch would
// write, which at this point exists in memory alone: nothing reaches the node
// before this returns and the conditional write runs.
//
// The issues an approval names are read back here, after that fence has
// answered and before the person is asked to type (G4, D2741): an item the
// ingest wrote carries the issue's id and a hash of its body and never the body
// itself (D2720), so the body the person is approving has to be fetched and put
// on the screen. A run with no terminal, a --json run and a coding agent's
// session spawn nothing at all, and the read is refused — with the fix printed
// and nothing written — when the issue no longer hashes to what the item pins.
func confirmPersonPatch(cmd *cobra.Command, o *orchestratorPatchOptions, lc *localcfg.Config, before, after orchestrator.State, p orchestrator.Patch) error {
	terminal := stdinIsTerminal(cmd.InOrStdin())
	marker := agentMarker(os.Getenv, terminal)
	what := patchWords(before, after, p)
	switch {
	case marker != "":
		return userErr("nothing was written: %s", initflow.AgentRefusal(marker))
	case !initflow.CanConfirmRun(initflow.Conditions{Terminal: terminal, JSON: o.json, Agent: marker}):
		return userErr("%s: %s; nothing was written", initflow.NeedsTerminal("the typed confirmation of this step"), what)
	}
	issues, err := approvedIssues(cmd.Context(), after, p)
	if err != nil {
		return err
	}
	if issues != "" {
		fmt.Fprint(cmd.OutOrStdout(), issues)
	}
	return confirmTypedStrict(cmd, lc, "a patch that approves a work item, sets the goal or closes a person's decision is the person's own word, and no flag stands in for it: everything this document carries lands with it", what)
}

func runOrchestratorPatch(cmd *cobra.Command, o *orchestratorPatchOptions) error {
	ctx := cmd.Context()
	raw, err := readPatchDocument(cmd, o.file)
	if err != nil {
		return err
	}
	p, err := orchestrator.DecodePatch(raw)
	if err != nil {
		return userErr("the patch document is not one this release reads: %v", err)
	}
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	// The nodes of one orchestrator are written into the budget database, so
	// this writer asks the one ownership question every writer into it asks
	// (budgetDBUnadopted), before it reads anything: a patch is not a read of
	// somebody else's installation that happens to have no effect.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
		return err
	}
	// The id is resolved once, before the shard is chosen: the shard's paths are
	// built from it (patchShardOf), the node is built from it, and it is what the
	// run prints, so all three are one answer to which orchestrator this is.
	id, err := orchestratorIDFor(lc, o.name)
	if err != nil {
		return err
	}
	shard, err := patchShard(id, p)
	if err != nil {
		return err
	}
	node, err := orchestratorNodeShard(db, id, shard)
	if err != nil {
		return err
	}
	s, etag, err := node.Load(ctx)
	if err != nil {
		return dbErr(lc, err, false)
	}
	// Every stamp in the document comes from the server's clock (D1230): a
	// laptop's own clock is what the order of the log and the lease must not
	// depend on, and it is not what an approval is dated with either.
	now, ok := db.ServerNow()
	if !ok {
		return userErr("the database answered without its own clock, and every stamp this patch writes comes from it (D1230)")
	}
	actor := orchestrator.Actor{Class: orchestrator.Session, Now: now}
	personOps := personOnlyOps(s, p)
	if personOps > 0 {
		// Who the person is comes before the confirmation, because the state the
		// patch would write is what the confirmation shows and an approval cannot
		// be applied without the name it is recorded with.
		name, err := authenticatedUser(ctx, lc)
		if err != nil {
			return userErr("a patch that approves a work item, sets the goal or closes a person's decision is recorded with the person's own name, and this run could not read who its credentials belong to: %v", err)
		}
		actor.Class, actor.Name = orchestrator.Person, name
	}
	next, err := orchestrator.Apply(s, p, actor)
	if err != nil {
		if errors.Is(err, orchestrator.ErrStaleRev) {
			return userErr("%v: read the state again with %s and build the patch from the revision it prints", err, orchestratorStateCommand(o.name))
		}
		return userErr("%v: nothing was written", err)
	}
	// The confirmation comes after the patch is built and before it is written,
	// so the person reads the state this patch leaves behind rather than the one
	// it starts from. Apply works on a copy: up to the write below, nothing of
	// it exists outside this run's memory.
	if personOps > 0 {
		if err := confirmPersonPatch(cmd, o, lc, s, next, p); err != nil {
			return err
		}
	}
	if err := node.Save(ctx, next, etag); err != nil {
		if errors.Is(err, orchestrator.ErrStateMoved) {
			return userErr("the state node moved since this run read it (rev %d), so the patch was not written: read the state again with %s and build the patch from the revision it prints", s.Rev, orchestratorStateCommand(o.name))
		}
		return dbErr(lc, err, true)
	}
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorPatchDoc{
			Version: orchestratorOutputVersion, Kind: "patch", ID: id,
			Rev: next.Rev, Actor: string(actor.Class), Ops: len(p.Ops),
		})
	}
	fmt.Fprintf(w, "wrote %d %s as a %s: orchestrator %s is at rev %d\n",
		len(p.Ops), plural(len(p.Ops), "op", "ops"), actor.Class, safetext.Field(id), next.Rev)
	return nil
}

// readPatchDocument reads the patch from -f's file, or from stdin for "-".
func readPatchDocument(cmd *cobra.Command, file string) ([]byte, error) {
	switch file {
	case "":
		return nil, userErr("a patch document is what this command applies: pass -f <file>, or -f - to read it from stdin")
	case "-":
		return readAllLimited(cmd.InOrStdin(), maxPatchDocumentBytes, "the patch document on stdin")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, userErr("reading the patch document %s: %v", safetext.Field(file), err)
	}
	defer f.Close()
	return readAllLimited(f, maxPatchDocumentBytes, fmt.Sprintf("the patch document %s", safetext.Field(file)))
}

// readAllLimited reads r whole and refuses what is over the limit rather than
// cutting it: half a document is not a patch, and the format's own validation
// would report the cut rather than the size.
func readAllLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, userErr("reading %s: %v", what, err)
	}
	if int64(len(data)) > limit {
		return nil, userErr("%s is over %d bytes, which is more than the %d ops a patch holds can take", what, limit, orchestrator.MaxOps)
	}
	return data, nil
}
