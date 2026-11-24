package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_backlog.go is B2 of docs/orchestrator-graph-estimates: the one pass
// that folds docs/backlog/ into the standing backlog (D2716).
//
// The folder was a backlog of its own, one Markdown file per item with a close
// flow beside it (D26–D32); the standing backlog is the project's work items
// with no spec, in the orchestrator's backlog node (D2715, D2735), and
// `fugaro backlog add` files into it (B1). D2716 supersedes the folder's
// format: `fugaro update`, run in a checkout, imports the folder's open items
// into that node ONCE — one item per file, under the file's own id, so a second
// update over the same checkout does not file it again — and the folder stays
// as history, where a reader still finds it and `fugaro backlog index` still
// regenerates its index (B8).
//
// "Once" is as far as the node remembers (backlogUpsert's skip): the ids in the
// backlog shard's work list, and the ids in the bounded archive it keeps, which
// is where a folded item goes once it has finished. The ordinary case is
// therefore silent for good — the archive is not pruned while a node is inside
// its size target — and the one that is not is an item folded long ago, finished
// and since aged out of that archive: the folder's file is still there, the node
// no longer holds the id, and a later update files it a second time, queued and
// unapproved like the first, so a person still sees it before anything runs.
// Closing that hole takes a mark on the node that retention cannot trim, and a
// node field is a decision for the owner rather than a step this change takes.
//
// Why the import is a step of this door: the HARD RULE says the upgrade is
// `fugaro update` alone, so a person who upgrades and runs it in a checkout has
// their folder folded by that run or by none at all. Nothing here may fail an
// update, as in the steps beside it (update_budget_mode.go, update.go's
// writeAdminList): a folder that could not be read, a checkout with no origin
// to name, a database that is not this installation's and a node that moved
// under the run are warnings, and the folder is exactly what the run found.
//
// It asks nothing, and it is silent wherever there is nothing to do: outside a
// checkout, in a checkout with no docs/backlog/ folder, in an installation with
// no database to write into, and on the second and every later update over a
// checkout whose ids the node still knows.

// backlogOpenStatus is the one of D27's three status words the fold imports: a
// closed or dropped file is history, and the standing backlog has no item for
// it.
const backlogOpenStatus = "open"

// foldBacklog is the step: it reads the checkout's docs/backlog/ folder, writes
// one queued, unapproved item per open file into the project's backlog node,
// and prints one line with the count it folded.
func (e *initEngine) foldBacklog(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door and the plan first, as in raiseBudgetMode and writeAdminList:
	// this is the upgrade's step, and --plan-only changes nothing outside this
	// machine. `--check` is not a converge at all (update_check.go), so it
	// reaches no engine and no step.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	// Then the session, for the rule the steps beside it keep (writeAdminList):
	// what this writes is the project's own work list, and a coding agent's
	// session is not the act that files work into it — `--yes`'s help says as
	// much, that under one of these markers the door "changes nothing outside
	// this machine, --yes included". The gate is the one exception (D1016):
	// where the marks say the project is managed, the gate's own read stands in
	// for the confirmations this run would have asked for, so the session IS the
	// run that converges the installation and the fold is part of what it
	// converges, exactly as the admin list is.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		return
	}
	// The folder is the checkout's, and only a checkout has one: `fugaro update`
	// also converges an installation from anywhere, and a run standing outside a
	// checkout has no docs/backlog/ to import. pluginwire.Locate answers the
	// question every checkout-aware step asks, and it refuses the home
	// directory, where `.git` in a stray folder is not a checkout of this
	// project's.
	loc, ok := pluginwire.Locate(".")
	if !ok {
		return
	}
	dir := filepath.Join(loc.Root, backlog.Dir)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	// The folder is read whole or not at all (backlog.Load): an item file that
	// breaks the format is a warning naming it rather than an import of the
	// files that happened to parse, because the folder is what the person wrote
	// and half of it is not the state to fold.
	files, err := backlog.Load(dir)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: %s", backlog.Dir, safetext.FieldErr(err)))
		return
	}
	// Every item an import writes names the repository it is about, and the
	// folder is the checkout's own: the origin remote is what the fold names,
	// which is the same default `fugaro backlog add` reads off it. A checkout
	// whose origin git cannot read, or one that is not owner/name, is said
	// rather than folded with an empty repository — the state model refuses an
	// item with no repository, and an item filed against the wrong one is work
	// sent to the wrong job.
	repo, err := checkoutRepo(ctx, loc.Root)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: %s", backlog.Dir, safetext.FieldErr(err)))
		return
	}
	items := backlogFoldItems(files, repo)
	if len(items) == 0 {
		return
	}
	// A run that never opened the cloud, or a project with no shared backend at
	// all, has no node to fold into: the line about that installation is
	// doctor's (policy-published), and `fugaro update --firebase-project` — the
	// command that gives it a backend — is where this step next runs.
	if e.lc == nil || e.lc.Budget == nil || e.lc.Budget.RTDBURL == "" {
		return
	}
	db, err := newBudgetClient(ctx, e.lc.Budget.RTDBURL, e.lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: the database it would be written to could not be opened (%s)", backlog.Dir, safetext.FieldErr(err)))
		return
	}
	// The one ownership question every reader and writer of that database asks
	// (budgetDBUnadopted), before anything is read out of it: a database that is
	// not this installation's is not where this project's backlog lives, and an
	// import there would file this folder into somebody else's work list. Both
	// of the predicate's answers mean the same thing here — the folder is left
	// as it is — so the string form is the whole of it and the error, which a
	// command turns into an exit code, is not: nothing in an update may fail
	// over this step.
	if why := budgetDBUnadopted(ctx, e.lc, infra.NewDB(db, e.lc.Name, e.lc.GCPProject, ""), backlog.Dir+" was not folded into the backlog", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	// The fold is the project's own orchestrator's backlog: `fugaro update` knows
	// no names, and the node it writes is the one the login unit it installs
	// drives.
	id, err := orchestratorID(e.lc)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: %s", backlog.Dir, safetext.FieldErr(err)))
		return
	}
	node, err := orchestratorNodeShard(db, id, orchestrator.ShardBacklog)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: %s", backlog.Dir, safetext.FieldErr(err)))
		return
	}
	// One patch carries at most orchestrator.MaxOps ops, so a folder holding
	// more open items than that is folded over as many patches, each read and
	// saved under the ETag it read. The node is the same one a daemon is writing
	// to, and a fold that held one ETag across a few hundred items would lose to
	// it; what is already in the node is skipped by backlogUpsert, so the second
	// batch of a rerun still consumes its slice and every loop ends.
	written := 0
	for off := 0; off < len(items); off += orchestrator.MaxOps {
		s, etag, err := node.Load(ctx)
		if err != nil {
			r.warn(fmt.Sprintf("%s was not folded into the backlog: %s%s", backlog.Dir, safetext.FieldErr(dbErr(e.lc, err, false)), foldWroteNote(written, len(items))))
			return
		}
		batch := items[off:min(off+orchestrator.MaxOps, len(items))]
		wrote, err := backlogUpsert(ctx, node, db, s, etag, batch)
		if err != nil {
			r.warn(fmt.Sprintf("%s was not folded into the backlog: %s%s", backlog.Dir, safetext.FieldErr(backlogWriteErr(e.lc, s, err)), foldWroteNote(written, len(items))))
			return
		}
		written += len(wrote)
	}
	if written == 0 {
		return
	}
	// One line, and only where the fold wrote something: the second update over
	// this checkout writes nothing and says nothing, exactly as the raise of the
	// budget mode says nothing over a mode already raised. The line carries the
	// count (D2716) and the one fact a reader needs — these items are not work
	// that is running, they are work waiting on a person (D2717).
	fmt.Fprintf(r.w, "backlog: folded %d open item(s) from %s into the standing backlog, queued and unapproved: each runs once a person approves it (fugaro orchestrator patch)\n", written, backlog.Dir)
}

// foldWroteNote says how much of a folder an interrupted fold had already
// written, so the warning about it is not read as "nothing happened" by a
// person looking at a node that just gained items. Empty when nothing was
// written.
func foldWroteNote(written, total int) string {
	if written == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d of %d item(s) were written before it stopped, and the next update folds in the rest)", written, total)
}

// backlogFoldItems is the folder's open items as the items to write: the file's
// own id and title, the checkout's repository, and the file's body as the brief
// a run of the item is launched with.
//
// The body is what the item stands for — the folder's `## What was left open`
// and `## Why` are the text the filer wrote and the whole reason the item
// exists — so an item folded from a file is briefed with it rather than with
// its title alone, which is the brief a filing with no text gets (backlogBrief).
// The state model's own cap still bounds it, as it bounds every brief the state
// holds (orchestrator.MaxBriefRunes).
//
// The folder's `level`, `kind`, `area`, `filed-by`, `filed-in` and `date` are
// not carried: none of them is a work item's field, an item's rigor is its
// rigor_tier (D2711), and the record of which run filed what is the folder's own
// file, which the fold leaves where it is. The two fields the state model
// requires of every item and that no item here declares — its recipe and its
// rigor tier — are the ones the shared writer fills in (backlogUpsert), the same
// defaults `fugaro backlog add` files with, and a person's approval is what
// changes either.
func backlogFoldItems(files []backlog.Item, repo string) []backlogItem {
	var out []backlogItem
	for _, f := range files {
		if f.Status != backlogOpenStatus {
			continue
		}
		out = append(out, backlogItem{
			ID: f.ID, Title: f.Title, Repo: repo, Brief: strings.TrimSpace(f.Body),
		})
	}
	return out
}
