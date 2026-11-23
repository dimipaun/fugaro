package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/tier"
)

// fugaro backlog (rigor modes, the owner's ruling of 2026-10-20: D26–D32) is
// the project's standing work list, and it is no longer the folder it was
// (docs/orchestrator-graph-estimates, D2715–D2717).
//
// The backlog IS the project's spec-less work items, in the orchestrator's own
// backlog node — `orchestrator/<project>/backlog` (D2735) — so `add` writes a
// `work_item_upsert` with no spec there and prints the id it minted, and `list`
// reads that node together with the spec-less items a session queued in the
// state node. `docs/backlog/` is history (D2716): its items were folded into
// the node once by `fugaro update` (B2), and `index` still renders the folder's
// Markdown index because a history a reader browses should still be
// regenerable.
//
// What an item added here is, exactly: queued and unapproved (D2717). It is
// one node of the graph the daemon launches from, and nothing runs it until a
// person approves it — the approval is the person's own word at a real
// terminal, over the spec the terminal shows them (fugaro orchestrator patch
// today, D1225).

// backlogFolderOptions are the flags `fugaro backlog index` carries: which
// folder the history lives in.
type backlogFolderOptions struct {
	dir string // the folder the item files live in, relative to the working directory
}

// backlogListOptions are `fugaro backlog list`'s flags.
type backlogListOptions struct {
	cloudOptions
	json bool
}

// newBacklogCmd builds `fugaro backlog`.
func newBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "File a backlog item, read the backlog, regenerate its index",
		Long: "Read the project's backlog, file an item into it, and regenerate the index\n" +
			"of the folder it came from.\n\n" +
			"The backlog is the project's work items with no spec: one item per piece of\n" +
			"work nobody has planned yet, in the orchestrator's own backlog node\n" +
			"(orchestrator/<project>/backlog), which is what the daemon launches from and\n" +
			"what a person approves. `add` writes one item there and prints its id; the\n" +
			"item is queued and unapproved, so nothing runs it until a person approves\n" +
			"it; `list` reads the backlog node together with the spec-less items a\n" +
			"session queued in the project's state node.\n\n" +
			"docs/backlog/ is history: the Markdown items that used to be the backlog\n" +
			"were folded into the node once, and the folder stays for a reader. `index`\n" +
			"still regenerates the folder's README.md from the item files beside it, and\n" +
			"`index` is the only command that writes there.\n\n" +
			"add and list read and write the project's database, so both carry the cloud\n" +
			"flags every other command that acts on the project does.",
	}
	cmd.AddCommand(newBacklogAddCmd(), newBacklogIndexCmd(), newBacklogListCmd())
	return cmd
}

// backlogAddOptions are `fugaro backlog add`'s flags.
type backlogAddOptions struct {
	cloudOptions
	title     string
	repo      string
	briefFile string
}

// maxBriefFileBytes bounds the brief file this command reads, before it reads
// it: a brief is at most orchestrator.MaxBriefRunes runes, a few kilobytes of
// UTF-8, so a megabyte is far past any brief and far short of the file a shell
// redirection would hand a command by mistake.
const maxBriefFileBytes = 1 << 20

// newBacklogAddCmd builds `fugaro backlog add`: filing an item by hand (D2717).
//
// The item it writes is what a session's own `work_item_upsert` is: a queued,
// unapproved work item with no spec, in the project's backlog node. It prints
// the id it minted, which is what a later `fugaro backlog list` and a person's
// approval name it by.
func newBacklogAddCmd() *cobra.Command {
	var o backlogAddOptions
	cmd := &cobra.Command{
		Use:   "add",
		Short: "File an item in the project's backlog and print its id",
		Long: "File an item in the project's backlog: a queued, unapproved work item with\n" +
			"no spec, in the orchestrator's backlog node. The command prints the id it\n" +
			"minted, which is the day it was filed (YYYYMMDD) and a two-digit sequence\n" +
			"that day (D26).\n\n" +
			"--title is the one line the item is about and is required; --repo names the\n" +
			"repository it is about, and defaults to this checkout's origin. --brief-file\n" +
			"names a file whose contents are the brief the run is launched with; without\n" +
			"it the brief is the title.\n\n" +
			"Nothing runs the item: it arrives queued and unapproved (D2717), and\n" +
			"approving it is the person's own word at a real terminal, over the spec the\n" +
			"terminal shows them (fugaro orchestrator patch).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogAdd(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.title, "title", "", "the item's title, one line (required)")
	f.StringVar(&o.repo, "repo", "", "the repository the item is about, as owner/name (default: this checkout's origin)")
	f.StringVar(&o.briefFile, "brief-file", "", "a file whose contents are the brief the run is launched with (default: the title)")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// runBacklogAdd writes the item and reports the id it minted.
func runBacklogAdd(cmd *cobra.Command, o *backlogAddOptions) error {
	ctx := cmd.Context()
	title := strings.TrimSpace(o.title)
	if title == "" {
		return userErr("--title is the one line the item is about: fugaro backlog add --title \"what is left open\"")
	}
	if utf8.RuneCountInString(title) > orchestrator.MaxTitleRunes {
		return userErr("--title is over %d runes, which is more than a work item's title holds: shorten it and put the rest in --brief-file", orchestrator.MaxTitleRunes)
	}
	repo := o.repo
	if repo == "" {
		// The default is the checkout's own origin, which is the repository the
		// person is standing in: an item is about a repository, and the one they
		// are in is the one they mean unless they say otherwise.
		got, err := checkoutRepo(ctx, ".")
		if err != nil {
			return userErr("--repo names the repository this item is about, and this checkout has no origin to read it from: pass --repo owner/name (%v)", err)
		}
		repo = got
	}
	brief, err := backlogBrief(o, title)
	if err != nil {
		return err
	}
	// The reporter opens where the run's work does, and the checks above are
	// before it: a consultation that refuses before anything happens keeps no
	// log (D1352, D1354).
	rep, sink := reporterFor(cmd, "backlog-add", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	step := rep.Step("filed")
	// The standing backlog belongs to the project's own orchestrator, and this
	// family takes no --name: a named orchestrator's backlog is filed by naming
	// its node in a patch, and an item filed here is the default one's.
	t, err := openOrchestratorShard(ctx, o.cloudOptions, orchestrator.ShardBacklog, "")
	if err != nil {
		return failStep(rep, step, err)
	}
	lc, node, db := t.lc, t.node, t.db
	// The backlog node lives in the budget database, so this writer asks the one
	// ownership question every writer into it asks (budgetDBUnadopted), before it
	// writes anything.
	if err := requireOwnBudgetDB(ctx, lc, db, "nothing was written"); err != nil {
		return failStep(rep, step, err)
	}
	s, etag, err := node.Load(ctx)
	if err != nil {
		return failStep(rep, step, dbErr(lc, err, false))
	}
	// Every stamp in the document comes from the server's clock (D1230), and the
	// id's day is a stamp: a laptop's own clock is not what says which day an
	// item was filed on.
	now, ok := db.ServerNow()
	if !ok {
		return failStep(rep, step, userErr("the database answered without its own clock, and the day an item is filed is read from it (D1230)"))
	}
	id := backlogItemID(s, now.UTC().Format("20060102"))
	if _, err := backlogUpsert(ctx, node, db, s, etag, []backlogItem{{ID: id, Title: title, Repo: repo, Brief: brief}}); err != nil {
		return failStep(rep, step, backlogWriteErr(lc, s, err))
	}
	step.Done(id)
	// The id is what the command prints, and the line under the step says what the
	// item is until a person acts: a queued, unapproved item is not work the
	// daemon may launch, and a reader who took the write for a launch would wait
	// for a run that never starts (D2717). The reporter's closing line stays the
	// last of the run (D1354), so this one is printed above it.
	fmt.Fprintf(cmd.OutOrStdout(), "%s is queued and unapproved: it runs once a person approves it (fugaro orchestrator patch)\n", safetext.Field(id))
	rep.Summary()
	return nil
}

// backlogBrief is the brief the item is launched with: --brief-file's contents,
// or the title when no file is named — an item is a title with no text only if
// its filer leaves it that way, and the title is the one line they gave.
func backlogBrief(o *backlogAddOptions, title string) (string, error) {
	if o.briefFile == "" {
		return title, nil
	}
	f, err := os.Open(o.briefFile)
	if err != nil {
		return "", userErr("reading the brief %s: %v", safetext.Field(o.briefFile), err)
	}
	defer f.Close()
	data, err := readAllLimited(f, maxBriefFileBytes, fmt.Sprintf("the brief %s", safetext.Field(o.briefFile)))
	if err != nil {
		return "", err
	}
	brief := string(data)
	if strings.TrimSpace(brief) == "" {
		return "", userErr("the brief %s is empty: a work item carries the brief the run is launched with, and an empty one is no brief at all", safetext.Field(o.briefFile))
	}
	if utf8.RuneCountInString(brief) > orchestrator.MaxBriefRunes {
		return "", userErr("the brief %s is over %d runes, which is more than a work item's brief holds", safetext.Field(o.briefFile), orchestrator.MaxBriefRunes)
	}
	return brief, nil
}

// backlogItemID mints the next id for a filing day (D26): the day as YYYYMMDD
// and a two-digit sequence, the first number that day no item of the node
// carries. The ids are read off the node the item is written to and not off the
// retired folder, because the node is what a reader lists.
func backlogItemID(s orchestrator.State, day string) string {
	used := make(map[string]bool, len(s.WorkItems))
	for _, w := range s.WorkItems {
		used[w.ID] = true
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s-%02d", day, n)
		if !used[id] {
			return id
		}
	}
}

// backlogItem is one item a writer files into the project's backlog node: the
// id it is written under, the one line it is about, the repository it is about
// and the brief a run of it is launched with.
type backlogItem struct {
	ID    string
	Title string
	Repo  string
	Brief string
}

// backlogUpsert writes items into the backlog node, skipping every id the node
// already knows, and reports the ids it wrote.
//
// It is the one writer both `fugaro backlog add` (B1) and the fold of
// docs/backlog/ `fugaro update` runs (B2, D2716) go through, because an item is
// one shape whoever files it: a work item with NO spec (D2715), queued and
// unapproved (D2717), in the project's own backlog node (D2735). What the skip
// is for is the fold's "once": an id the node already knows is an item already
// filed, so a second update over the same checkout does not file it again.
//
// "Knows" is the work list AND the archive, and the archive half is what makes
// the "once" hold: an item leaves the work list through OpWorkItemArchive —
// which is how a folded item ends, archived by a person or by the daemon once
// it has finished — and the file it was folded from is still in the checkout, so
// a skip that read the work list alone would refile it the first time it
// finished. The archive is bounded history rather than a record (state.go's
// keptArchived, and shrink drops its oldest entry first when a node is over its
// target), so the ids the skip recognises are the ones the node still remembers,
// and the oldest id it has forgotten is one the fold files a second time.
//
// The recipe and the tier the run is dispatched with are not a filer's to
// invent silently: the state model requires both of every item, so what is
// written is the recipe a project has without configuring one and the smallest
// tier, and the person's approval is what changes either (fugaro orchestrator
// patch).
//
// Every stamp in the document comes from the server's clock (D1230), the whole
// list goes in one patch because that is what the node's conditional write
// takes, and a node that moved under the caller is returned as
// orchestrator.ErrStateMoved rather than as a lost item.
func backlogUpsert(ctx context.Context, node *orchestrator.Node, db *rtdb.Client, s orchestrator.State, etag orchestrator.ETag, items []backlogItem) ([]string, error) {
	// The work list and the archive both: an item that has finished is one the
	// node has already filed, and the file it came from is still in the
	// checkout, so a skip that read the work list alone would file it again.
	have := make(map[string]bool, len(s.WorkItems)+len(s.Archived))
	for _, w := range s.WorkItems {
		have[w.ID] = true
	}
	for _, a := range s.Archived {
		have[a.ID] = true
	}
	var ops []orchestrator.Op
	var wrote []string
	recipeName, tierName := recipe.DefaultName, string(tier.Small)
	for _, it := range items {
		if have[it.ID] {
			continue
		}
		title, repo, brief := it.Title, it.Repo, it.Brief
		ops = append(ops, orchestrator.Op{
			Op: orchestrator.OpWorkItemUpsert,
			WorkItem: &orchestrator.WorkItemOp{
				ID: it.ID, Title: &title, Repo: &repo, Recipe: &recipeName,
				RigorTier: &tierName, Brief: &brief,
			},
		})
		wrote = append(wrote, it.ID)
	}
	if len(ops) == 0 {
		return nil, nil
	}
	now, ok := db.ServerNow()
	if !ok {
		return nil, userErr("the database answered without its own clock, and the stamp an item carries is read from it (D1230)")
	}
	next, err := orchestrator.Apply(s, orchestrator.Patch{BaseRev: s.Rev, Ops: ops}, orchestrator.Actor{Class: orchestrator.Session, Now: now})
	if err != nil {
		return nil, userErr("%v: nothing was written", err)
	}
	if err := node.Save(ctx, next, etag); err != nil {
		return nil, err
	}
	return wrote, nil
}

// backlogWriteErr is what a command says about a failed backlogUpsert: a node
// that moved under the run is the conditional write's own refusal, and anything
// else is the database's, with the exit code a cloud failure carries.
func backlogWriteErr(lc *localcfg.Config, s orchestrator.State, err error) error {
	if errors.Is(err, orchestrator.ErrStateMoved) {
		return userErr("the backlog node moved since this run read it (rev %d), so the item was not written: run the command again", s.Rev)
	}
	return dbErr(lc, err, true)
}

// newBacklogIndexCmd builds `fugaro backlog index`: it writes the folder's
// README.md from the item files beside it.
func newBacklogIndexCmd() *cobra.Command {
	var o backlogFolderOptions
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Regenerate docs/backlog/README.md from the item files beside it",
		Long: "Regenerate the index of docs/backlog/, the folder the backlog was before it\n" +
			"became the project's work items.\n\n" +
			"The index lists one row per item — the id linked to its file, the title,\n" +
			"the level, the kind, the area, the status, who filed it and the date —\n" +
			"sorted by id, with the closed and dropped rows grouped under the open ones,\n" +
			"under a count line (D28). It is written to <dir>/README.md, and it is the\n" +
			"only file this command touches.\n\n" +
			"The document is a pure function of the item files: every date in it comes\n" +
			"from the item that carries it and no clock is read, so regenerating an\n" +
			"unchanged folder rewrites the same bytes and a regeneration in review\n" +
			"diffs only where an item changed (D27). A pull request that adds an item to\n" +
			"the folder runs this in the same commit, and a test in internal/cli fails on\n" +
			"an index that is not current — which is why the file is never merged by\n" +
			"hand. The folder is history: new items go to `fugaro backlog add`, which\n" +
			"writes a work item and no file at all (D2716).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogIndex(cmd, &o) },
	}
	addBacklogDirFlag(cmd, &o)
	return cmd
}

// newBacklogListCmd builds `fugaro backlog list`: it prints the backlog's work
// items, and writes nothing.
func newBacklogListCmd() *cobra.Command {
	var o backlogListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print the project's backlog items",
		Long: "Print the project's backlog: the work items with no spec in the orchestrator's\n" +
			"backlog node, and the spec-less items a session queued in the project's state\n" +
			"node, which are the same list to a reader (D2715).\n\n" +
			"One row per item — its id, phase, the node it lives in, its repository,\n" +
			"whether a person has approved it, and its title — sorted by id, which is the\n" +
			"day the item was filed. --json prints one document instead:\n" +
			"{version, kind, project, waiting, items[{id, title, phase, repo, node,\n" +
			"approved}]}, indented.\n\n" +
			"Nothing is written. Both nodes are read in the project's database, with the\n" +
			"person's own credential and the same cloud flags every command that acts on\n" +
			"the project carries.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBacklogList(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloudOptions)
	return cmd
}

// addBacklogDirFlag adds --dir, the folder the history's item files live in: the
// folder and not the repository root, so the command works wherever it is run
// from and a test can point it at a folder of its own.
func addBacklogDirFlag(cmd *cobra.Command, o *backlogFolderOptions) {
	cmd.Flags().StringVar(&o.dir, "dir", backlog.Dir, "the folder the item files live in")
}

// runBacklogIndex writes the index and reports which file it wrote.
func runBacklogIndex(cmd *cobra.Command, o *backlogFolderOptions) error {
	items, err := backlog.Load(o.dir)
	if err != nil {
		return userErr("%v", err)
	}
	// The reporter opens with the write: reading the folder is what a
	// refused index failed at, and nothing of this run has happened yet
	// (D1352, D1354).
	rep, sink := reporterFor(cmd, "backlog-index", nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	step := rep.Step("wrote")
	path := filepath.Join(o.dir, backlog.IndexName)
	if err := os.WriteFile(path, []byte(backlog.Render(items)), 0o644); err != nil {
		return failStep(rep, step, fmt.Errorf("write %s: %w", path, err))
	}
	step.Done(path)
	rep.Summary()
	return nil
}

// runBacklogList prints the backlog's items, or the document --json asks for.
func runBacklogList(cmd *cobra.Command, o *backlogListOptions) error {
	ctx := cmd.Context()
	lc, db, err := openBudget(ctx, o.cloudOptions)
	if err != nil {
		return err
	}
	// The listing is the project's own orchestrator's: this family takes no
	// --name, so both nodes are the default id's.
	id, err := orchestratorID(lc)
	if err != nil {
		return err
	}
	backlogNode, err := orchestratorNodeShard(db, id, orchestrator.ShardBacklog)
	if err != nil {
		return err
	}
	stateNode, err := orchestratorNodeShard(db, id, orchestrator.ShardState)
	if err != nil {
		return err
	}
	bs, _, err := backlogNode.Load(ctx)
	if err != nil {
		return dbErr(lc, err, false)
	}
	// The state node is read too, and only its spec-less items are listed: a
	// session that queues an item in the main node is filing into the same
	// backlog (D2715's amendment), and an item that carries a spec belongs to the
	// plan that spec names rather than to the backlog.
	ss, _, err := stateNode.Load(ctx)
	if err != nil {
		return dbErr(lc, err, false)
	}
	doc := backlogListDoc{Version: backlogOutputVersion, Kind: "backlog", Project: lc.Name, Items: []backlogRow{}}
	for _, src := range []struct {
		node string
		s    orchestrator.State
	}{{orchestrator.BacklogNode, bs}, {orchestrator.StateNode, ss}} {
		for _, w := range src.s.WorkItems {
			if src.node == orchestrator.StateNode && w.Spec != "" {
				continue
			}
			// Waiting is the unfinished work nobody has approved: a terminal item
			// cannot be approved at all (the state model refuses an approval on a
			// done or cancelled item), and the last few finished items stay in the
			// node's work list, so counting them would print a figure that never
			// clears and a person could do nothing about.
			if w.Approved == nil && !w.Phase.Done() {
				doc.Waiting++
			}
			doc.Items = append(doc.Items, backlogRow{
				ID: w.ID, Title: w.Title, Phase: string(w.Phase), Repo: w.Repo,
				Node: src.node, Approved: w.Approved != nil,
			})
		}
	}
	sort.Slice(doc.Items, func(i, j int) bool { return doc.Items[i].ID < doc.Items[j].ID })

	w := cmd.OutOrStdout()
	if o.json {
		return writeJSON(w, doc)
	}
	if len(doc.Items) == 0 {
		fmt.Fprintln(w, "no items: neither the backlog node nor the project's state node holds a work item with no spec")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPHASE\tNODE\tREPO\tAPPROVED\tTITLE")
	for _, r := range doc.Items {
		approved := "no"
		if r.Approved {
			approved = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			safetext.Field(r.ID), safetext.Field(r.Phase), safetext.Field(r.Node),
			safetext.Field(r.Repo), approved, safetext.Field(r.Title))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if doc.Waiting > 0 {
		fmt.Fprintf(w, "%d waiting for a person's approval: nothing runs until one is approved, and a finished item is not waiting on anybody (D2717)\n", doc.Waiting)
	}
	return nil
}

// backlogOutputVersion is the version of the document `fugaro backlog list
// --json` prints: the description of it in the console's JSON contract is this
// version's. A field never changes meaning across it — a new one is additive,
// and a renamed one is a new version.
const backlogOutputVersion = 1

// backlogListDoc is `fugaro backlog list --json`: the backlog's work items as
// data. The console's JSON contract describes it in its backlog section, and
// reads this file's symbols rather than a line number.
type backlogListDoc struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	// Waiting is the count of the items no person has approved and no phase has
	// finished: what a screen shows as the work waiting on the person, since an
	// unapproved item is one nothing will run (D2717) and a done or cancelled one
	// can never be approved.
	Waiting int          `json:"waiting"`
	Items   []backlogRow `json:"items"`
}

// backlogRow is one item of the listing. `node` is which node of the
// orchestrator subtree the row was read from — the backlog node, or the state
// node's spec-less items — and `approved` is whether a person has approved the
// spec the item stands for.
type backlogRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Phase    string `json:"phase"`
	Repo     string `json:"repo"`
	Node     string `json:"node"`
	Approved bool   `json:"approved"`
}
