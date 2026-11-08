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
			"confirmed at a real terminal, where the project's name is typed and the spec an\n" +
			"approval binds is shown; every other session, --json, a pipe and a coding\n" +
			"agent's environment are refused and write nothing, and --yes never covers them.\n" +
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

// personOnlyOp is one op of a patch that only a person may land, as the
// confirmation shows it.
type personOnlyOp struct {
	Kind   orchestrator.OpKind
	ID     string
	Title  string
	Digest orchestrator.Digest
}

// personOnlyOps names the ops in p that only a person may land (D1225, G2):
// approving a work item, setting the goal, and closing a decision that waits on
// a person. A decision this run cannot find is not one of them: Apply refuses
// an unknown id before it looks at the class, so nothing is written either way.
func personOnlyOps(s orchestrator.State, p orchestrator.Patch) []personOnlyOp {
	var out []personOnlyOp
	for _, op := range p.Ops {
		switch op.Op {
		case orchestrator.OpWorkItemApprove:
			if op.Approve == nil {
				continue
			}
			one := personOnlyOp{Kind: op.Op, ID: op.Approve.ID, Digest: op.Approve.SpecDigest}
			if w, ok := s.Item(op.Approve.ID); ok {
				one.Title = safetext.OneLine(safetext.Clip(w.Title, 80))
			}
			out = append(out, one)
		case orchestrator.OpGoalSet:
			out = append(out, personOnlyOp{Kind: op.Op})
		case orchestrator.OpDecisionResolve:
			if op.Resolve == nil {
				continue
			}
			if d, ok := s.Decision(op.Resolve.ID); ok && d.WaitingOn.Class() == orchestrator.Person {
				out = append(out, personOnlyOp{Kind: op.Op, ID: d.ID})
			}
		}
	}
	return out
}

// personPatchWords is what the confirmation shows: one line per op only a
// person may land, out of names, digests and the stored title this command
// cleaned on the way out.
func personPatchWords(ops []personOnlyOp) string {
	lines := make([]string, 0, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case orchestrator.OpWorkItemApprove:
			what := fmt.Sprintf("approving work item %s", safetext.Field(op.ID))
			if op.Title != "" {
				what += fmt.Sprintf(" (%s)", op.Title)
			}
			lines = append(lines, what+" against spec "+safetext.Field(string(op.Digest)))
		case orchestrator.OpGoalSet:
			lines = append(lines, "setting the goal of the loop")
		case orchestrator.OpDecisionResolve:
			lines = append(lines, fmt.Sprintf("closing decision %s, which waits on a person", safetext.Field(op.ID)))
		default:
			lines = append(lines, string(op.Kind))
		}
	}
	return "this patch does what only a person may do:\n  " + strings.Join(lines, "\n  ")
}

// confirmPersonPatch is the fence on the person's ops (D1225, G2). The typed
// confirmation at a real terminal is the whole of it: a coding agent's
// environment is refused as well, but that check is a mitigation against the
// accident and not a barrier — whoever unsets the marker is outside it — which
// is why nothing here rests on it. --yes never covers this class, and no
// argument reaches it.
func confirmPersonPatch(cmd *cobra.Command, o *orchestratorPatchOptions, lc *localcfg.Config, ops []personOnlyOp) error {
	terminal := stdinIsTerminal(cmd.InOrStdin())
	marker := agentMarker(os.Getenv, terminal)
	what := personPatchWords(ops)
	switch {
	case marker != "":
		return userErr("nothing was written: %s", initflow.AgentRefusal(marker))
	case !initflow.CanConfirm(initflow.Typed, initflow.Conditions{Terminal: terminal, JSON: o.json, Agent: marker}):
		return userErr("%s: %s; nothing was written", initflow.NeedsTerminal("the typed confirmation of this step"), what)
	}
	return confirmTypedStrict(cmd, lc, "a patch that approves a work item, sets the goal or closes a person's decision is the person's own word, and no flag stands in for it", what)
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
	if ops := personOnlyOps(s, p); len(ops) > 0 {
		if err := confirmPersonPatch(cmd, o, lc, ops); err != nil {
			return err
		}
		name, err := authenticatedUser(ctx, lc)
		if err != nil {
			return userErr("an approval is recorded with the person's own name, and this run could not read who its credentials belong to: %v", err)
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
