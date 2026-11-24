package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_runindex.go is RI5's step of the upgrade: the run index an
// installation of the release behind has never built, or has stopped building,
// is marked for a rebuild and one execution is started to walk it.
//
// The index is a derived object like the two beside it (update_prindex.go's
// pull-request index, update_backlog.go's standing backlog), so nothing here
// may fail the update: a step that could not mark or start is a step the next
// `fugaro update` runs, `fugaro runs reindex` asks for by hand, and -- because
// the mark is what a walk is judged by -- the next sweep of the history job
// continues on its own (F9, F39).
//
// It runs once and not once per upgrade: an index that is complete with no
// unfinished walk is one this step leaves alone (runindex.WalkOwed), because
// the walk reads every run in the bucket and the upgrade is not the place to
// pay for a rebuild nobody asked for. An installation whose fold is behind, or
// whose index was never built, is the one it starts.
//
// It is two halves, and the first is the one that must land. The mark
// (runindex.ResetReindex): the writer lease loses its cursor and the summary
// carries complete: false, which is the state every later sweep reads as "a
// walk is owed" and walks within its own timeout. The start: one execution of
// the history job in --reindex mode, which walks the whole bucket now rather
// than a sweepful at a time. Only the installation's scheduler account may
// start that job, so the start is attempted and reported and never insisted on
// (O7); the update is not the run that repairs an operator's IAM.
//
// No run in flight is waited for and none is touched: the walk reads the runs
// bucket, writes the index, and gives up the lease when a pass ends, so an
// upgrade reaches an installation whose runs are going exactly as it reaches
// one that is idle (the HARD RULE, docs/design/update-in-flight.md).
func (e *initEngine) reindexRunIndex(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in backfillPRIndex: this is the upgrade's step, and
	// the hook the doors share is the end of the converge (init_stages.go's
	// outcome). `fugaro setup` creates an installation, and an installation it
	// has just created has no history to walk.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// Then the session, the rule the steps beside it keep: this step writes the
	// installation's database and starts a job execution, and a coding agent's
	// session under a marker `--yes`'s help promises it "changes nothing
	// outside this machine". The gate's exception is D1016's — where the marks
	// say the project is managed, the session IS the run that converges the
	// installation.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	if e.lc == nil {
		return
	}
	// The index lives in the installation's Firestore, named by the config's
	// budget.firebase_project and reached with the person's own credentials or,
	// in a no-auth config, at endpoints.firestore. A config that names neither
	// is one whose index this run cannot reach at all -- there is nothing to
	// rebuild and nothing to say -- and it is left alone silently.
	if e.lc.Budget == nil || e.lc.Budget.FirebaseProject == "" || (e.lc.Endpoints.NoAuth && e.lc.Endpoints.Firestore == "") {
		return
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("the run index was not marked for a rebuild: the budget database could not be opened (%s). `fugaro runs ls` reads the index as it stands until an update marks it", safetext.FieldErr(err)))
		return
	}
	// The one ownership question every writer into that database asks
	// (budgetDBUnadopted): a database that is not this installation's is not
	// this step's to mark.
	if why := budgetDBUnadopted(ctx, e.lc, infra.NewDB(db, e.lc.Name, e.lc.GCPProject, ""), "the run index was not marked for a rebuild", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	fs, err := newReportFirestore(ctx, e.lc)
	if err != nil {
		r.warn(fmt.Sprintf("the run index was not marked for a rebuild: %s. `fugaro runs ls` reads the index as it stands until an update marks it", safetext.FieldErr(err)))
		return
	}
	// The first reindex and not a reindex on every upgrade (RI5): an
	// installation whose index is complete and whose lease carries no unfinished
	// walk has nothing to repair, and walking a million runs into an index that
	// already holds them is a cost an upgrade must not add. `fugaro runs reindex`
	// is the command that asks for one by hand.
	owed, err := runindex.WalkOwed(ctx, db, fs)
	if err != nil {
		r.warn(fmt.Sprintf("the run index was not marked for a rebuild: %s. `fugaro runs ls` reads the index as it stands until an update marks it, or `fugaro runs reindex` marks it by hand", safetext.FieldErr(err)))
		return
	}
	if !owed {
		return
	}
	live, err := runindex.ResetReindex(ctx, db, fs, reindexHolder(ctx, e.lc), time.Now().UTC())
	if err != nil {
		r.warn(fmt.Sprintf("the run index was not marked for a rebuild: %s. `fugaro runs ls` reads the index as it stands until an update marks it, or `fugaro runs reindex` marks it by hand", safetext.FieldErr(err)))
		return
	}
	if live {
		fmt.Fprintln(r.w, "run index: a reindex is already walking, so nothing was started; `fugaro runs ls` is short until it reaches the end of history. No run was waited for and none is touched.")
		return
	}
	exec, err := startHistoryReindex(ctx, e.lc)
	if err != nil {
		r.warn(fmt.Sprintf("the run index was marked for a rebuild, but no reindex execution was started (%s). The walk is owed all the same: the next sweep of the history job carries it, and `fugaro runs reindex` asks for it by hand", safetext.FieldErr(err)))
		return
	}
	fmt.Fprintf(r.w, "run index: marked incomplete and reindexing now as %s, so `fugaro runs ls` is short until the walk reaches the end of history. It resumes from its own cursor until then, and no run was waited for: an update reaches an installation whose runs are going exactly as it reaches an idle one.\n", oneLine(exec))
}
