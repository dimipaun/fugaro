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
			"nothing, and --yes never covers them.\n" +
			"No flag, environment variable or field of the document makes this command the\n" +
			"daemon or the decision agent: the daemon's bookkeeping is the daemon's.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorPatch(cmd, &o) },
	}
	f := cmd.Flags()
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
// approving a work item, setting the goal, or closing a decision that waits on
// a person. The state it is asked with is the one the patch starts from,
// because which class a decision waits on is a fact about the node as it
// stands. A decision this run cannot find is not one of them: Apply refuses an
// unknown id before it looks at the class, so nothing is written either way.
func personOnly(before orchestrator.State, op orchestrator.Op) bool {
	switch op.Op {
	case orchestrator.OpWorkItemApprove:
		return op.Approve != nil
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

// itemWords is a work item's digested spec under a head line: every field
// SpecDigest covers and nothing else. That digest is what an approval binds,
// and a person who approves a spec the terminal did not show them has approved
// nothing they read — so the title, the repository, the recipe, the rigor tier,
// the dependencies and the brief are all here, taken from the state the patch
// leaves behind rather than from the op's own fields, which may be a partial
// declaration over an item that already stands.
//
// Nothing here is cut, and nothing needs to be: every one of these fields is
// bounded by the state model's own caps (MaxTitleRunes, MaxBriefRunes), and a
// value the terminal shortened is a value the person was not shown.
func itemWords(head string, w orchestrator.WorkItem) []string {
	lines := []string{head}
	if w.Title != "" {
		lines = append(lines, "title: "+oneLine(w.Title))
	}
	where := make([]string, 0, 3)
	for _, f := range []struct{ name, value string }{
		{"repo", w.Repo}, {"recipe", w.Recipe}, {"rigor", w.RigorTier},
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
func confirmPersonPatch(cmd *cobra.Command, o *orchestratorPatchOptions, lc *localcfg.Config, before, after orchestrator.State, p orchestrator.Patch) error {
	terminal := stdinIsTerminal(cmd.InOrStdin())
	marker := agentMarker(os.Getenv, terminal)
	what := patchWords(before, after, p)
	switch {
	case marker != "":
		return userErr("nothing was written: %s", initflow.AgentRefusal(marker))
	case !initflow.CanConfirm(initflow.Typed, initflow.Conditions{Terminal: terminal, JSON: o.json, Agent: marker}):
		return userErr("%s: %s; nothing was written", initflow.NeedsTerminal("the typed confirmation of this step"), what)
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
	lc, node, db, err := openOrchestrator(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	// The state node is written into the budget database, so this writer asks
	// the one ownership question every writer into it asks (budgetDBUnadopted),
	// before it reads anything: a patch is not a read of somebody else's
	// installation that happens to have no effect.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
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
			return userErr("%v: read the state again with fugaro orchestrator state and build the patch from the revision it prints", err)
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
			return userErr("the state node moved since this run read it (rev %d), so the patch was not written: read the state again with fugaro orchestrator state and build the patch from the revision it prints", s.Rev)
		}
		return dbErr(lc, err, true)
	}
	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, orchestratorPatchDoc{
			Version: orchestratorOutputVersion, Kind: "patch", ID: lc.Name,
			Rev: next.Rev, Actor: string(actor.Class), Ops: len(p.Ops),
		})
	}
	fmt.Fprintf(w, "wrote %d %s as a %s: orchestrator %s is at rev %d\n",
		len(p.Ops), plural(len(p.Ops), "op", "ops"), actor.Class, safetext.Field(lc.Name), next.Rev)
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
