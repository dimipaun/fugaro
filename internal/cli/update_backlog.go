package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// update_backlog.go is B2 of docs/orchestrator-graph-estimates: the one pass
// that folds docs/backlog/ into the standing backlog (D2716), read from every
// enrolled repository's default branch (docs/update-from-anywhere U8, D3344).
//
// The folder was a backlog of its own, one Markdown file per item with a close
// flow beside it (D26–D32); the standing backlog is the project's work items
// with no spec, in the orchestrator's backlog node (D2715, D2735), and
// `fugaro backlog add` files into it (B1). D2716 supersedes the folder's
// format: `fugaro update` imports every repository's open items into that node
// ONCE — one item per file, under the file's own id, so a second update does
// not file it again — and the folders stay as history, where a reader still
// finds them and `fugaro backlog index` still regenerates an index (B8).
//
// The step reads no working directory: a repository never updated from a
// checkout still holds unfolded items, and a folder is folded under the
// repository it was read from, whatever machine the update runs on (R5, R20).
// The node is the project's own, so an id two repositories state is refused
// rather than folded under one of them and lost for the other.
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
// `fugaro update` alone, so a person who upgrades has their repositories'
// folders folded by that run or by none at all. Nothing here may fail an
// update, as in the steps beside it (update_budget_mode.go, update.go's
// writeAdminList): a repository the host could not read, a folder that does not
// parse, a database that is not this installation's and a node that moved under
// the run are warnings or lines, and every folder is exactly what the run found.
//
// It asks nothing, and it is silent wherever there is nothing to do: in a
// project whose repositories hold no docs/backlog/ folder, in an installation
// with no database to write into, and on the second and every later update
// whose folders' ids the node still knows.
//
// The step goes one release after L5 ships (O2): it is the fold's deletion, and
// it is a follow-up rather than part of this change.

// backlogOpenStatus is the one of D27's three status words the fold imports: a
// closed or dropped file is history, and the standing backlog has no item for
// it.
const backlogOpenStatus = "open"

// foldBacklog is the step: it reads every enrolled repository's docs/backlog/
// folder at its default branch, writes one queued, unapproved item per open
// file into the project's backlog node, and prints one line with the count it
// folded.
func (e *initEngine) foldBacklog(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in raiseBudgetMode and writeAdminList: this is the
	// upgrade's step. `--check` is not a converge at all (update_check.go), so
	// it reaches no engine and no step. --plan-only does reach it, and reads
	// like any other run: what it says it would fold is a line, and the write
	// is what it does not do (spec §Interactions).
	if o.doorName() != updateDoor.name {
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
	// The project's repositories, each read at its own default branch with its
	// docs/backlog/ folder (R5, D3344): a repository no machine ever updated
	// from a checkout still holds unfolded items, and a folder is folded under
	// the repository it was read from whatever machine the run is on. A
	// repository whose host cannot list the tree is unread, and its line says
	// why; the folder is not there for it and nothing is assumed about it.
	views, _ := e.repos(ctx)
	// Every folder is read before anything is planned, because an id two
	// repositories state is refused with both of them in hand (R5).
	type folder struct {
		repo  string
		items []backlog.Item
	}
	var (
		folders []folder
		// unfolded is the line of a repository the fold did not fold from: one
		// it could not read, or one whose folder it refused whole.
		unfolded = map[string]initflow.RepoLine{}
		// holders is an id to the repositories whose folders state it.
		holders = map[string][]string{}
	)
	// A repository the fold did not fold from is also warned about, beside the
	// line it records: the lines are the output members' to print (U13), and
	// until they do, a step that left a repository's folder unfolded without a
	// word would be a silent skip, which is what main's fold warned about and
	// what this step keeps (R21, spec §Guiding principles). The warning comes out
	// when U13 prints the lines.
	unfoldedWarn := func(repo, why string) {
		r.warn(fmt.Sprintf("%s: %s was not folded into the backlog: %s", backlogRepoList([]string{repo}), backlog.Dir, why))
	}
	for _, v := range views {
		if !v.Read() {
			unfolded[v.Repo] = newRepoLine(v.Repo, initflow.RepoUnread, v.Unread)
			unfoldedWarn(v.Repo, v.Unread)
			continue
		}
		// The folder is read whole or not at all (backlog.LoadFiles): an item
		// file that breaks the format is a line naming it rather than an import
		// of the files that happened to parse, because the folder is what the
		// person wrote and half of it is not the state to fold.
		items, err := backlog.LoadFiles(v.Files)
		if err != nil {
			unfolded[v.Repo] = newRepoLine(v.Repo, initflow.RepoNeedsYou,
				fmt.Sprintf("its %s was not folded: %s", backlog.Dir, safetext.FieldErr(err)))
			unfoldedWarn(v.Repo, safetext.FieldErr(err).Error())
			continue
		}
		open := backlogOpenItems(items)
		if len(open) == 0 {
			continue
		}
		folders = append(folders, folder{repo: v.Repo, items: open})
		for _, it := range open {
			holders[it.ID] = append(holders[it.ID], v.Repo)
		}
	}
	// An id two repositories state is refused, and neither item is folded: the
	// node is the project's own, so one id is one work item whoever filed it,
	// and folding it under either repository would lose the other's (R5). One
	// line names both, and the fix — a rename on the branch that would merge
	// second — is the folder's own guard (D26).
	for _, f := range folders {
		for _, it := range f.items {
			if repos := holders[it.ID]; len(repos) > 1 && repos[0] == f.repo {
				r.warn(fmt.Sprintf("%s: id %s is in %s, so neither item was folded until one file is renamed (D26)",
					backlog.Dir, it.ID, backlogRepoList(repos)))
			}
		}
	}
	// The items to write: each read repository's open items minus the refused
	// ids, under the repository the file was read from and never a field of it
	// (R20) — the folder states no repository, and an item filed against the
	// wrong one is work sent to the wrong job.
	var (
		items    []backlogItem
		itemRepo = map[string]string{}
	)
	for _, f := range folders {
		keep := make([]backlog.Item, 0, len(f.items))
		for _, it := range f.items {
			if len(holders[it.ID]) > 1 {
				continue
			}
			keep = append(keep, it)
		}
		if len(keep) == 0 {
			continue
		}
		items = append(items, backlogFoldItems(keep, f.repo)...)
		for _, it := range keep {
			itemRepo[it.ID] = f.repo
		}
	}
	// The step's lines, in the list's canonical order: a repository it did not
	// fold from, and a repository it folded n items of -- or would, under
	// --plan-only, which is the state that says nothing was written.
	foldState, foldDetail := initflow.RepoDone, func(n int) string { return fmt.Sprintf("folded %d open item(s)", n) }
	if o.planOnly {
		foldState = initflow.RepoSkipped
		foldDetail = func(n int) string { return fmt.Sprintf("would fold %d open item(s); --plan-only writes nothing", n) }
	}
	report := func(counts map[string]int) {
		var lines []initflow.RepoLine
		for _, v := range views {
			if l, ok := unfolded[v.Repo]; ok {
				lines = append(lines, l)
				continue
			}
			if n := counts[v.Repo]; n > 0 {
				lines = append(lines, newRepoLine(v.Repo, foldState, foldDetail(n)))
			}
		}
		if len(lines) > 0 {
			e.reportRepos("backlog", lines)
		}
	}
	// Nothing open to fold anywhere: the step says which repositories it could
	// not read or fold, and nothing else.
	if len(items) == 0 {
		report(nil)
		return
	}
	// A run that never opened the cloud, or a project with no shared backend at
	// all, has no node to fold into: the line about that installation is
	// doctor's (policy-published), and `fugaro update --firebase-project` — the
	// command that gives it a backend — is where this step next runs. The four
	// failures below this point are not a repository's: the step's fold of every
	// repository it could is what the warning they print is about, and none of
	// them is a line about one repository.
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
	// The node as it stands, read once and written by nothing here: an id the
	// node already files under ANOTHER repository is the collision the folders
	// state among themselves, one update later — an item folded earlier from a
	// checkout, or from a repository whose file has since been deleted — so it is
	// refused with the same line and neither item is folded (R5). Filing it under
	// the source as well would leave the node holding two items of one id, and the
	// one that already runs would lose the repository this file was read from.
	s, _, err := node.Load(ctx)
	if err != nil {
		r.warn(fmt.Sprintf("%s was not folded into the backlog: %s", backlog.Dir, safetext.FieldErr(dbErr(e.lc, err, false))))
		return
	}
	items = backlogNotFiledElsewhere(r.warn, s, items, itemRepo)
	// Under --plan-only the reads happen and the write does not: the count it
	// would fold is what the step is here to say (spec §Interactions). It is the
	// count the real update would WRITE and not the number of open files, which
	// is the number the node decides (backlogUpsert's skip, D2716): a plan-only
	// run over folders a fold has already filed says nothing, exactly as the
	// update behind it would write nothing. The node read above is the whole of
	// what this branch reads, and no write follows it.
	if o.planOnly {
		would := map[string]int{}
		for _, it := range backlogUnknownItems(s, items) {
			would[itemRepo[it.ID]]++
		}
		report(would)
		n := 0
		for _, c := range would {
			n += c
		}
		if n == 0 {
			return
		}
		fmt.Fprintf(r.w, "backlog: would fold %d open item(s) from every repository's %s into the standing backlog, queued and unapproved: each runs once a person approves it (fugaro orchestrator patch)\n", n, backlog.Dir)
		return
	}
	// One patch carries at most orchestrator.MaxOps ops, so a few hundred open
	// items are folded over as many patches, each read and saved under the ETag
	// it read. The node is the same one a daemon is writing to, and a fold that
	// held one ETag across a few hundred items would lose to it; what is already
	// in the node is skipped by backlogUpsert, so the second batch of a rerun
	// still consumes its slice and every loop ends.
	written := map[string]int{}
	total := func() int {
		n := 0
		for _, c := range written {
			n += c
		}
		return n
	}
	for off := 0; off < len(items); off += orchestrator.MaxOps {
		s, etag, err := node.Load(ctx)
		if err != nil {
			r.warn(fmt.Sprintf("%s was not folded into the backlog: %s%s", backlog.Dir, safetext.FieldErr(dbErr(e.lc, err, false)), foldWroteNote(total(), len(items))))
			report(written)
			return
		}
		batch := items[off:min(off+orchestrator.MaxOps, len(items))]
		wrote, err := backlogUpsert(ctx, node, db, s, etag, batch)
		if err != nil {
			r.warn(fmt.Sprintf("%s was not folded into the backlog: %s%s", backlog.Dir, safetext.FieldErr(backlogWriteErr(e.lc, s, err)), foldWroteNote(total(), len(items))))
			report(written)
			return
		}
		for _, id := range wrote {
			written[itemRepo[id]]++
		}
	}
	report(written)
	folded := total()
	if folded == 0 {
		return
	}
	// One line, and only where the fold wrote something: a second update over
	// folders whose ids the node still knows writes nothing and says nothing,
	// exactly as the raise of the budget mode says nothing over a mode already
	// raised. The line carries the count (D2716) and the one fact a reader needs
	// — these items are not work that is running, they are work waiting on a
	// person (D2717).
	fmt.Fprintf(r.w, "backlog: folded %d open item(s) from every repository's %s into the standing backlog, queued and unapproved: each runs once a person approves it (fugaro orchestrator patch)\n", folded, backlog.Dir)
}

// backlogOpenItems is the folder's items the fold is about: a closed or dropped
// item is history, and the standing backlog has no item for it (D2716).
func backlogOpenItems(items []backlog.Item) []backlog.Item {
	var out []backlog.Item
	for _, it := range items {
		if it.Status == backlogOpenStatus {
			out = append(out, it)
		}
	}
	return out
}

// backlogNotFiledElsewhere is a fold's items minus the ones the node already
// files under another repository, each with the line the folders' own collision
// gets: one id is one work item whoever filed it, so a fold that wrote this one
// too would leave the node holding two items of one id, and the repository the
// file was read from would be the one lost (R5). The id the node holds under the
// SAME repository is not a collision — it is the skip that keeps a fold from
// filing an item twice (D2716) — and the archive is left out of the comparison
// because an archived entry carries no repository at all: an id the node has
// finished and let age out of its work list is therefore compared with nothing
// and folded again with no line, a hole that is a gap rather than a decision
// this step takes (the comment above foldBacklog states what closing it needs).
func backlogNotFiledElsewhere(warn func(string), s orchestrator.State, items []backlogItem, itemRepo map[string]string) []backlogItem {
	var out []backlogItem
	for _, it := range items {
		repo := itemRepo[it.ID]
		other := backlogRepoOf(s, it.ID, repo)
		if other == "" {
			out = append(out, it)
			continue
		}
		a, b := repo, other
		if repoSortKey(a) > repoSortKey(b) {
			a, b = b, a
		}
		warn(fmt.Sprintf("%s: id %s is in %s, so neither item was folded until one file is renamed (D26)",
			backlog.Dir, it.ID, backlogRepoList([]string{a, b})))
	}
	return out
}

// backlogRepoList is repositories as one of the fold's warnings names them.
// Each name goes through the two passes newRepoLine gives a line's repository
// (F27, R12): they come out of a checkout's own git config, so they are
// repository-controlled text, and nothing of that may reach the terminal or the
// --json warnings array. The list is bounded the way every listing this program
// puts inside one of its own sentences is (safetext.FieldListLine) and says how
// many names it left out, because how many repositories state one id is not
// this program's to decide.
func backlogRepoList(repos []string) string {
	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, pluginwire.Printable(oneLine(repo)))
	}
	return safetext.FieldListLine(names)
}

// backlogRepoOf is the repository the node already files an id under, when that
// is not the repository the file was read from. "" is the ordinary answer: the
// node holds no item of that id, or holds it under this same repository.
func backlogRepoOf(s orchestrator.State, id, repo string) string {
	for _, w := range s.WorkItems {
		if w.ID == id && repoSortKey(w.Repo) != repoSortKey(repo) {
			return w.Repo
		}
	}
	return ""
}

// backlogUnknownItems is the items of a fold the node does not already hold,
// which is what backlogUpsert would write: the same skip, asked before the
// write, so what a --plan-only run counts is what an update would do.
func backlogUnknownItems(s orchestrator.State, items []backlogItem) []backlogItem {
	have := backlogKnown(s)
	var out []backlogItem
	for _, it := range items {
		if !have[it.ID] {
			out = append(out, it)
		}
	}
	return out
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
