package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// orchestratorImportOptions are `fugaro orchestrator import`'s flags.
type orchestratorImportOptions struct {
	cloudOptions
	file string
}

func newOrchestratorImportCmd() *cobra.Command {
	var o orchestratorImportOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Load the one-time session dump into the orchestrator's state",
		Long: "Load the one-time session dump (the spec's Migration) into the project's\n" +
			"orchestrator state, from -f's file or from stdin with -f -. The dump is one\n" +
			"patch document (schemas/orchestrator-patch.schema.json) a session wrote out,\n" +
			"and it is read by the same validator and bounded by the same caps as\n" +
			"fugaro orchestrator patch: it is the one document in this loop written by a\n" +
			"model, and everything it carries is data, never an instruction.\n\n" +
			"The dump lands as the person who runs this, because the migration is theirs\n" +
			"to make, and it lands nothing a session may not land: approving a work\n" +
			"item, setting the goal and closing a decision that waits on anyone but a\n" +
			"session are the person's own word, given at a real terminal over the spec\n" +
			"they read (fugaro orchestrator patch). So no dump approves work and no dump\n" +
			"closes a decision of the person's — not even one the dump raises and closes\n" +
			"itself — and every work item the dump declares arrives unapproved.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runOrchestratorImport(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVarP(&o.file, "file", "f", "", "the session dump to import, or - to read it from stdin")
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
	lc, node, db, err := openOrchestrator(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
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
	name, err := authenticatedUser(ctx, lc)
	if err != nil {
		return userErr("the dump lands as the person whose migration it is, and this run could not read who its credentials belong to: %v", err)
	}
	next, err := orchestrator.Apply(s, p, orchestrator.Actor{Class: orchestrator.Person, Name: name, Now: now})
	if err != nil {
		if errors.Is(err, orchestrator.ErrStaleRev) {
			return userErr("%v: the dump carries the revision it was built from, and it has to be the node's — read it with fugaro orchestrator state and write the dump again from the revision it prints", err)
		}
		return userErr("%v: nothing was written", err)
	}
	if err := node.Save(ctx, next, etag); err != nil {
		if errors.Is(err, orchestrator.ErrStateMoved) {
			return userErr("the state node moved since this run read it (rev %d), so the dump was not written: read the state again with fugaro orchestrator state and write the dump from the revision it prints", s.Rev)
		}
		return dbErr(lc, err, true)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "imported %d %s as a %s: orchestrator %s is at rev %d\n",
		len(p.Ops), plural(len(p.Ops), "op", "ops"), orchestrator.Person, safetext.Field(lc.Name), next.Rev)
	if n := importedUnapproved(next, p); n > 0 {
		fmt.Fprintf(w, "%d work %s the dump declares arrived unapproved: approving one is your own word at a real terminal (fugaro orchestrator patch)\n",
			n, plural(n, "item", "items"))
	}
	return nil
}
