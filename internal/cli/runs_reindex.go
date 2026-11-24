package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// runs_reindex.go is RI5's caller: `fugaro runs reindex`, the rebuild of the
// run index a person asks for.
//
// It does two things, in this order, and the order is the whole of it. First
// the reset (runindex.ResetReindex): the walk is marked owed -- the lease
// loses its cursor and the summary carries complete: false -- so that every
// later sweep of the history job continues the walk on its own, with no
// execution started and no grant asked for (F9, F39). Then, when no execution
// is already walking, one execution is started in the job's --reindex mode,
// which is the pass that actually reads the bucket now rather than at the next
// sweep.
//
// The second half is the one that may not be allowed, and it does not have to
// be: only the scheduler account holds run.invoker on the history job, so a
// caller who may not start it -- a member, an operator whose role does not
// cover the job -- gets one line saying so and the reset stands (O7). What the
// command guarantees is the reset; what it tries is the execution.
//
// The fence is update's own for the steps that start the job (U3): the reset
// and the start both write the installation's cloud, so a coding agent's
// session is refused rather than obeyed.

// reindexJobArgs is how the history job is asked for a walk: the container's
// command line, overridden at the start (RI5). It is the same invocation the
// job's own image runs, and nothing else is overridden -- no timeout, because
// the job's own is what the pass leaves its cursor against (Fold.StartLimit).
var reindexJobArgs = []string{"budget", "history", "--reindex"}

// newRunsReindexCmd is "fugaro runs reindex".
func newRunsReindexCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the run index from the runs bucket",
		Long: "Rebuild the run index: mark it incomplete, then walk every run in the runs\n" +
			"bucket into it and recompute the summary (RI5).\n\n" +
			"The mark is written first and is the part that always lands: from it, every\n" +
			"later sweep of the history job continues the walk on its own until it reaches\n" +
			"the end of history, with no execution started by the job and no timeout\n" +
			"overridden. Then one execution is started in the job's --reindex mode, which\n" +
			"is the pass that walks now; a caller who may not start the job -- only the\n" +
			"installation's own scheduler account may -- gets one line saying so, and the\n" +
			"walk is carried by the next sweep instead.\n\n" +
			"The walk is resumable: a pass that runs out of time leaves its position\n" +
			"beside the writer lease, and the next pass continues from there rather than\n" +
			"starting over. A walk that is already going is left alone: its own fresh\n" +
			"claim is what says so, and nothing is started.\n\n" +
			"A reindex asks nothing of any run: a run in flight is neither waited for nor\n" +
			"touched, because the walk reads the runs bucket and never writes a run's own\n" +
			"objects.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runRunsReindex(cmd, &o) },
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// runRunsReindex is the command's own body: the guard, the reset, and the one
// execution the reset may be followed by.
func runRunsReindex(cmd *cobra.Command, o *cloudOptions) error {
	// The fence (U3): what this writes is the installation's index and what it
	// starts is a job execution, so it is run by a person at their own
	// terminal, exactly as the update's own job-starting step is.
	if m := agentMarker(os.Getenv, processTerminal()); m != "" {
		return userErr("nothing was reindexed: fugaro runs reindex marks the installation's run index for a rebuild and starts the history job's reindex execution, so it is run by a person at their own terminal: %s", initflow.AgentRefusal(m))
	}
	ctx := cmd.Context()
	// The budget database and the config alone: the reset writes the database's
	// lease and the summary document, and the execution is started through the
	// backend. Nothing here reads the runs bucket, so none is opened.
	lc, db, err := openBudgetDB(ctx, *o)
	if err != nil {
		return err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "the index was not marked for a rebuild", selfCommand()+" update"); why != "" {
		return userErr("%v", oneLine(why))
	}
	out := cmd.OutOrStdout()
	live, err := reindexReset(ctx, lc, db, reindexHolder(ctx, lc), time.Now().UTC())
	if err != nil {
		return remote(err)
	}
	if live {
		fmt.Fprintln(out, "index: a reindex is already walking, so nothing was started: its lease is held and the walk it is on is the one the index is owed. `fugaro runs ls` is short until it reaches the end of history")
		return nil
	}
	fmt.Fprintln(out, "index: marked incomplete, so every sweep of the history job walks it until one reaches the end of history")
	exec, err := startHistoryReindex(ctx, lc)
	if err != nil {
		// The caller may not start the job (O7), or the job is not deployed:
		// neither is a reason to fail, because the reset above already did what
		// the command is for -- the walk is owed, and the sweep that meets the
		// incomplete summary carries it.
		fmt.Fprintf(out, "index: no execution was started (%s). The reindex stands all the same: the next sweep of the history job walks the index\n", oneLine(err.Error()))
		return nil
	}
	fmt.Fprintf(out, "index: reindexing now%s, resuming from its own cursor until it reaches the end of history\n", reindexExecClause(exec))
	return nil
}

// reindexReset is the reset both `fugaro runs reindex` and `fugaro update`'s
// step run before they start anything (RI5, U3): the cursor goes and the
// summary is latched incomplete, so the walk every later sweep continues
// begins at the top of history.
func reindexReset(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, holder string, now time.Time) (bool, error) {
	fs, err := newReportFirestore(ctx, lc)
	if err != nil {
		return false, err
	}
	return runindex.ResetReindex(ctx, db, fs, holder, now)
}

// reindexHolder is the name the reset leaves on the lease: the person's own
// identity, which is what the contract asks a reset to carry (the CLI's own
// identity rather than an execution's name, contract-index.md's lease table).
// A person who cannot be named -- no `user:` and no git identity -- falls back
// to the command's own name rather than refusing: the holder is a label a
// person reads, and what a walk is judged by is the expiry beside it.
func reindexHolder(ctx context.Context, lc *localcfg.Config) string {
	if me, err := lc.Me(ctx); err == nil && me != "" {
		return me
	}
	return "fugaro-runs-reindex"
}

// startHistoryReindex starts one execution of the installation's history job
// in --reindex mode, and answers its name: "" when the platform started the
// execution but reported no readable name for it (gcp.ErrStartedUnnamed), which
// the callers' line says (reindexExecClause) rather than leaving "reindexing
// now as" with nothing after it. A non-nil error means nothing was started.
//
// It is the gcp backend's own job start rather than a launch: there is no run
// and nothing to record, so the project config's backend endpoint is built
// here from the config the command already selected.
func startHistoryReindex(ctx context.Context, lc *localcfg.Config) (string, error) {
	be, err := gcp.New(ctx, gcp.Options{
		GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView,
		Endpoints: gcp.Endpoints{Run: lc.Endpoints.Run, NoAuth: lc.Endpoints.NoAuth},
	})
	if err != nil {
		return "", err
	}
	name, err := be.StartJob(ctx, infra.HistoryJob, reindexJobArgs)
	if errors.Is(err, gcp.ErrStartedUnnamed) {
		// The execution is running; only its name could not be read.
		return "", nil
	}
	return name, err
}

// reindexExecClause is the clause both callers print after "reindexing now":
// the execution's own name, or -- when the platform started the execution but
// reported no readable one -- a note that says so, because "reindexing now as"
// with nothing after it names no execution at all.
func reindexExecClause(name string) string {
	if name == "" {
		return " (the platform started the execution but did not report its name)"
	}
	return " as " + oneLine(name)
}
