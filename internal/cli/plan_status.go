package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/streams"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/watch"
)

// `fugaro plan status` is the console v2 plan's GV1.6: every plan's graph, joined
// to the runs that are working on it.
//
// Unlike `plan graph`, which is a local read of one artifact, this is a cloud
// read: the state of a task is a run's state, and a run is carried by the run
// index of the selected project — the documents the history job folds, which
// the run's own record and launch are copied into whole (D3201, D3205). So it
// takes the cloud flags `runs ls` takes, and it reads the runs of the
// checkout's own repository — the plans it prints are the checkout's, and a run
// of another repository is not theirs to claim (GV1.6's rule ii; the plan's
// constraint C3).
//
// Two things about it are security-relevant, and both are stated here rather
// than left to the code to imply:
//
//   - **The only run text that reaches the document is the read side's own.**
//     A run's plan task is a hint a job wrote, and it arrives already sanitised
//     and shape-checked by the reader that publishes it (`runview.Join` drops an
//     id the launcher could never have accepted) — this command never reads
//     launch.json itself, and never prints a word of what a job wrote. A stage
//     is job-written too, so it goes out through `safetext.Field` like every
//     other such field (the console contract §9.4), and the plan's own text —
//     an id, a title, a version — is the artifact's, which is repository
//     content and untrusted in the same way a backlog item's title is.
//   - **The walk stays inside the checkout's docs/.** The artifacts are read
//     where they sit, a symlink anywhere under docs/ ends the read rather than
//     being followed, and the count is capped: a checkout that plants a link to
//     another directory is refused instead of having that directory read and
//     printed (the plan's constraint C2, GV2.1's probe).
func newPlanStatusCmd() *cobra.Command {
	var o planStatusOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print every plan's tasks, joined to the runs working on them",
		Long: "Print every plan's graph under docs/, with each task's state from the runs.\n\n" +
			"A task's state joins the graph to the runs the project's index holds:\n" +
			"running with the stage of the run working on it, done when the run's pull\n" +
			"request is merged, cancelled when the task's only run was, blocked while a\n" +
			"task it needs is not done, ready when the work can start (the task a launch\n" +
			"may act on), and not-started otherwise.\n\n" +
			"A run joins a task by the --plan-task its launch recorded, and only when it\n" +
			"is a run of this checkout's own repository: a run of another repository\n" +
			"naming the same task is ignored. The join is display-only — nothing here\n" +
			"makes another plan's task ready.\n\n" +
			"The runs come from the run index, one query per task of the plans here, so\n" +
			"the read costs what the plans hold rather than what the repository's history\n" +
			"holds, and a task's runs are listed however old they are. Only the runs that\n" +
			"join a task of a plan here have their pull requests looked up, and one read\n" +
			"asks about at most 32 pull requests, the newest runs' first.\n\n" +
			"Whether a pull request is merged is asked of your own gh, when it is\n" +
			"installed, and only about a pull request of this checkout's own\n" +
			"repository: a run that recorded another repository's pull request is not\n" +
			"merged here. Where gh cannot answer (a Bitbucket repository, no gh, a\n" +
			"lookup that failed, one past the 32) a task whose run recorded a ready pull\n" +
			"request shows done with the mark \"merge unknown\" rather than nothing.\n\n" +
			"--no-merges does not ask your gh at all: every task whose run recorded a\n" +
			"ready pull request reads the same way, and the document says merges_read is\n" +
			"false. It is the read a screen paints before merge state.\n\n" +
			"Two artifacts that would be one plan id — docs/plans/foo.graph.json and\n" +
			"docs/plans/foo/bar.graph.json both give plans/foo — cannot be joined apart,\n" +
			"so the second is not listed and a warning names both.\n\n" +
			"Merged and cancelled tasks are counted per release and not listed; --all\n" +
			"lists them too. This command writes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPlanStatus(cmd, &o) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.all, "all", false, "list the closed tasks too (merged and cancelled) instead of counting them per release")
	f.BoolVar(&o.noMerges, "no-merges", false, "spawn no gh: every task whose run recorded a ready pull request reads merge unknown")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

type planStatusOptions struct {
	cloud cloudOptions
	all   bool
	// noMerges is --no-merges (D3217 as amended): the read spawns no gh, which
	// is the first paint a console draws before it decorates the graph with
	// merge state. The default keeps the lookups, because `closed`, `mark` and
	// `--all` already mean what a lookup answers to every console installed.
	noMerges bool
	asJSON   bool
}

// The read's own names. planDocsDir is the checkout's docs directory and is the
// only tree the walk enters; planStreamsPath is the release streams catalogue
// (GV1.4), which names the releases and says which have shipped.
const (
	planDocsDir     = "docs"
	planStreamsPath = "docs/release-streams.yaml"
	// planMaxArtifacts bounds one read. A repository with sixty-four plans is
	// past what a screen draws, and the bound is what stops a checkout that
	// fills docs/ with artifacts from being read without end.
	planMaxArtifacts = 64
	// planMaxLookups bounds the gh processes one read spawns: one per distinct
	// pull request, so a plan whose tasks carry many runs costs at most this
	// many lookups, the newest runs' first.
	planMaxLookups = 32
	// planLookupWorkers is how many of those lookups run at once. A lookup is
	// two gh processes against github.com, and GitHub's secondary rate limits
	// discourage concurrent requests, so this read is narrower than the runs
	// bucket's own width (lsWorkers) and stays narrower however wide the
	// bucket reads become.
	planLookupWorkers = 8
)

// planStatusDoc is plan status --json's document.
type planStatusDoc struct {
	// Repo is the checkout's own repository, owner/name, and it is what scopes
	// the join: every run in this document is a run of this repository. "" is
	// an unknown repository — the checkout has no readable origin — and then no
	// run is joined at all, which is why an empty string is not "every
	// repository".
	Repo string `json:"repo"`
	// Plans are the plans the walk found, in path order.
	Plans []planStatusPlan `json:"plans"`
	// Totals are every plan's figures added up, over every task, listed or
	// collapsed: the header line a screen draws before the plans.
	Totals planStatusTotals `json:"totals"`
	// Streams is the release streams catalogue, in the file's own order; `[]`
	// when the file could not be read (the reason is in `warnings`, and a
	// release's name and version are then simply absent).
	Streams []plan.StateStream `json:"streams"`
	// Warnings are the lines a human read prints before its table: a plan whose
	// artifact this build refuses, and a catalogue it could not read. Each is a
	// sentence of ours with the path it is about, never a word a run wrote.
	Warnings []string `json:"warnings"`
	// IndexComplete is whether the run index has ever finished a walk, and
	// FoldedThrough how far it has been folded — absent before the first fold.
	// They are the summary's own two fields and are always read (D3215): every
	// run of every task here comes from the index, so a read whose index is
	// still building lists the runs it has and says so, rather than letting a
	// short list pass for the whole truth.
	IndexComplete bool       `json:"index_complete"`
	FoldedThrough *time.Time `json:"folded_through,omitempty"`
	// MergesRead is present, and false, only under --no-merges (D3217 as
	// amended), and it is a field rather than an absence because a screen has
	// to tell "no merges were read" from "there was no merge to read": with it
	// false every merge state is unknown, `closed` counts no merge and a task
	// whose run recorded a ready pull request carries the "merge unknown" mark.
	MergesRead *bool `json:"merges_read,omitempty"`
}

// planStatusPlan is one plan as this document carries it: the state the join
// answered, and the plan's estimate beside it (S3).
//
// The estimate is a field of its own rather than one of plan.StatePlan's, and
// the state is embedded so the document reads exactly as it did before the
// estimate existed: every key the console already reads stays where it was, at
// the same depth, and the block is additive (S3's own rule, and the reason
// nothing here is a new shape for the rest of the document).
type planStatusPlan struct {
	plan.StatePlan
	// Estimate is what the plan has left to take and what it costs, from the
	// history this read's own invitation to the runs bucket gave it and what
	// the join says about each task (D2722, D2723). It is absent where the plan
	// is an artifact older than the schema that carries a task's tier, which is
	// what a screen shows nothing for — the block's own absence is "no estimate
	// here", and never a zero figure.
	Estimate *plan.Estimate `json:"estimate,omitempty"`
}

// planStatusTotals is the read's own figures over every plan it printed.
type planStatusTotals struct {
	Plans  int `json:"plans"`
	Tasks  int `json:"tasks"`
	Closed int `json:"closed"`
	Open   int `json:"open"`
}

func runPlanStatus(cmd *cobra.Command, o *planStatusOptions) error {
	ctx := cmd.Context()
	errw := cmd.ErrOrStderr()

	doc := planStatusDoc{
		Plans: []planStatusPlan{}, Streams: []plan.StateStream{}, Warnings: []string{},
	}
	// Everything this read could not do but could live without says so in the
	// same two places: stderr, where a rejection goes, and the document, which
	// is what a screen draws.
	warn := func(sentence string) {
		fmt.Fprintf(errw, "warning: %s\n", sentence)
		doc.Warnings = append(doc.Warnings, sentence)
	}

	// The catalogue comes first, and a refusal is a warning rather than the end
	// of the read: it names releases and dates them, and neither is what a
	// task's state is made of. The refusal itself goes to stderr, where every
	// refusal of this tool goes; the document carries our own sentence, because
	// a document is shown on a screen.
	cat, err := streams.Read(planStreamsPath)
	if err != nil {
		fmt.Fprintf(errw, "warning: %s\n", oneLine(err.Error()))
		doc.Warnings = append(doc.Warnings, planStreamsPath+" could not be read: no release is named or dated")
	} else {
		doc.Streams = plan.StreamList(cat)
	}

	// The plans are read before a single run is asked about. Which plans exist
	// and which tasks they carry is what decides which runs the join reads,
	// which pull requests are looked up at all (planMerges), and which runs'
	// records the estimate reads for the work that is already done or going; an
	// artifact this build refuses, and one whose plan id another artifact has
	// already taken, are warnings rather than the end of the read, and neither
	// is a plan the join may read runs for.
	artifacts, err := planArtifacts(planDocsDir)
	if err != nil {
		return err
	}
	plans := make([]plan.PlanRef, 0, len(artifacts))
	paths := make(map[string]string, len(artifacts)) // plan id -> the artifact that took it
	for _, path := range artifacts {
		id := plan.IDFromPath(path)
		d, err := plan.ReadFile(path)
		if err != nil {
			fmt.Fprintf(errw, "warning: %s\n", oneLine(err.Error()))
			doc.Warnings = append(doc.Warnings, planCell(path)+" could not be read: its tasks are not listed")
			continue
		}
		if other, taken := paths[id]; taken {
			// Two artifacts that derive one plan id cannot be joined apart: a run
			// claiming the id would paint a task of both, and one set of figures
			// would be read for two plans. The first in path order keeps the id,
			// and the sentence names both — it is the paths that are wrong, and a
			// reader has to see which two files they are.
			sentence := fmt.Sprintf("%s and %s are both the plan %s; only the first is listed",
				planCell(other), planCell(path), planCell(id))
			fmt.Fprintf(errw, "warning: %s\n", sentence)
			doc.Warnings = append(doc.Warnings, sentence)
			continue
		}
		paths[id] = path
		plans = append(plans, plan.PlanRef{ID: id, Doc: d})
	}

	// The runs of this checkout's own repository. An origin that cannot be read
	// is not a reason to refuse the read: a plan's graph says a great deal on
	// its own, and what is lost is said in a warning. The join itself is
	// fail-closed either way — plan.State joins nothing when the repository is
	// unknown.
	now := time.Now().UTC()
	facts := []plan.RunFact{}
	// The estimate's two inputs from the cloud side: the history the runs
	// carry, and the cap `max_running` defaults to (A6). Both fall back to what
	// this machine runs at when the checkout has no repository to read.
	maxRunning := localcfg.DefaultMaxParallel
	var (
		table   *estimate.Table
		costs   map[string]*runstore.Record
		elapsed map[string]time.Duration
		joined  []plan.RunFact
	)
	if repo, err := originRepo(ctx); err != nil {
		doc.Warnings = append(doc.Warnings, "this checkout's repository could not be read: no run is joined")
	} else {
		doc.Repo = repo
		env, err := openCloud(ctx, o.cloud)
		if err != nil {
			return err
		}
		defer env.Close()
		slug, err := env.repoSlug(repo, checkoutOf(ctx, repo))
		if err != nil {
			return err
		}
		maxRunning = env.lc.MaxParallel
		// The runs come from the index: one query per task of the plans this
		// checkout holds, plus one per pull request the runs of those tasks
		// carry, which is how a follow-up that was given no plan task of its
		// own is joined (plan/state.go's rule iii). No listing of the runs
		// bucket is made at all, so the read costs what the plans hold rather
		// than what the repository's history holds (D3205, F1).
		reader := planIndexReader(env)
		sum, err := reader.Summary(ctx)
		if err != nil {
			return err
		}
		doc.IndexComplete, doc.FoldedThrough = sum.Complete, sum.FoldedThrough
		entries, err := planIndexRuns(ctx, reader, plans, slug)
		if err != nil {
			return err
		}
		facts = planRunFacts(entries)
		costs, elapsed = planRunCosts(entries, now)
		// The join reads the runs that claim one of these plans and no others, and
		// everything spent per run is spent on those — the merge lookup above all,
		// which spawns a process per distinct pull request. Scoping it to the runs
		// the join reads is what keeps a command the console calls to draw a graph
		// from costing what the plans hold rather than what the repository's whole
		// history holds.
		joined = plan.JoinedRuns(plans, facts, doc.Repo)
		table = planIndexTable(sum, warn)
	}

	// The merge lookups are the default read and stay the default read (F37):
	// every console already installed draws `closed`, `mark` and `--all` from
	// them. --no-merges is the first paint of the one that follows, and it is
	// the absence of the lookup rather than a different answer to it.
	var merge plan.MergeCheck
	if o.noMerges {
		unread := false
		doc.MergesRead = &unread
	} else {
		merge = planMerges(ctx, joined, doc.Repo, warn)
	}

	// What every plan's estimate is computed from, gathered once: the history
	// the index's summary answered with, the runs the join read, and the cap
	// and the clock the formula takes.
	in := planEstimateInputs{
		table: table, runs: facts, costs: costs, elapsed: elapsed,
		repo: doc.Repo, maxRunning: maxRunning, now: now,
	}

	for _, p := range plans {
		path := paths[p.ID]
		// --all is asked for whatever the read was given, because the estimate
		// is over every task of the plan and not over the ones a reader asked
		// to see: a done task weighs nothing on the path and costs what it
		// cost, and a collapsed read that lost it would answer for the open
		// work alone. What the flag decides is what is *listed*, and a closed
		// node says so itself (StateNode.Closed), which is the field the flag
		// turns on.
		st, err := plan.State(p.ID, p.Doc, joined, plan.StateOptions{
			Artifact: path, Repo: doc.Repo, All: true, Catalogue: cat, Merge: merge,
		})
		if err != nil {
			fmt.Fprintf(errw, "warning: %s\n", oneLine(err.Error()))
			doc.Warnings = append(doc.Warnings, planCell(path)+" could not be read: its tasks are not listed")
			continue
		}
		est, err := in.of(p, st)
		if err != nil {
			fmt.Fprintf(errw, "warning: %s\n", oneLine(err.Error()))
			doc.Warnings = append(doc.Warnings, planCell(path)+" could not be estimated")
		}
		if !o.all {
			st.Nodes = slices.DeleteFunc(st.Nodes, func(n plan.StateNode) bool { return n.Closed })
		}
		doc.Plans = append(doc.Plans, planStatusPlan{StatePlan: st, Estimate: est})
	}

	doc.Totals = planStatusTotals{Plans: len(doc.Plans)}
	for _, p := range doc.Plans {
		doc.Totals.Tasks += p.Tasks
		doc.Totals.Closed += p.Closed
		doc.Totals.Open += p.Open
	}
	return printPlanStatus(cmd.OutOrStdout(), doc, o.asJSON)
}

// planArtifacts lists the graph artifacts under dir, in path order.
//
// The walk does not follow a symlink and refuses one: a link under docs/ is a
// way for the text a screen shows to be text from somewhere else, and the plan's
// artifacts are read where they sit (C2). Anything else that would be an
// artifact and is not a regular file — a fifo, a socket, a device — is refused
// for the same reason: opening one is a read that may never return, and a
// plan's artifact is a regular file in a checkout. A missing docs/ is no plans
// rather than a refusal — a repository that has none is a repository this read
// has nothing to say about, which is a different statement from a read that was
// stopped.
func planArtifacts(dir string) ([]string, error) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, userErr("%s could not be read: %v", dir, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, userErr("%s is a symlink; plan status reads the checkout's own docs directory and follows no link out of it", dir)
	case !info.IsDir():
		return nil, nil
	}

	var (
		out  []string
		fail error
	)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			fail = userErr("%s is a symlink; plan status reads the graph artifacts where they sit and follows no link", path)
			return filepath.SkipAll
		case d.IsDir(), !strings.HasSuffix(d.Name(), plan.ArtifactSuffix):
			return nil
		case !d.Type().IsRegular():
			fail = userErr("%s is not a regular file; plan status reads a checkout's graph artifacts, and opening anything else is a read that may never end", path)
			return filepath.SkipAll
		}
		out = append(out, path)
		if len(out) > planMaxArtifacts {
			fail = userErr("%s carries more than %d graph artifacts; plan status reads a checkout's plans, and a directory of them this size is not one", dir, planMaxArtifacts)
			return filepath.SkipAll
		}
		return nil
	})
	if fail != nil {
		return nil, fail
	}
	if err != nil {
		return nil, userErr("%s could not be walked: %v", dir, err)
	}
	sort.Strings(out)
	return out, nil
}

// planIndexRuns asks the index for the runs of the plans this checkout holds:
// one query per plan task, and then one per pull request the runs of those
// tasks carry.
//
// The second pass is the join's rule for a follow-up (plan/state.go's rule
// iii): a follow-up records no plan task of its own, so the only thing that
// places it on a task is the pull request it was opened on, which is the
// number the run it follows recorded. It is a second pass rather than a
// query-per-PR-of-history because the pull requests asked about are the ones
// the plans' own runs name, so the read still costs what the plans hold.
//
// Every query is scoped to the checkout's own repository, and the runs two
// queries both answer with are read once: an entry is one run, and joining it
// twice would count its cost twice.
func planIndexRuns(ctx context.Context, reader runindex.Reader, plans []plan.PlanRef, slug string) ([]runindex.Entry, error) {
	keys := []string{}
	for _, p := range plans {
		for _, n := range p.Doc.Nodes {
			// A node whose id cannot be the half of a plan task a launcher
			// writes was never launched under one, so no entry carries it and
			// the query is not worth making (task.PlanTaskProblem is the one
			// shape rule, applied by the launcher on write).
			if key := p.ID + "/" + n.ID; task.PlanTaskProblem(key) == "" {
				keys = append(keys, key)
			}
		}
	}
	entries, err := planIndexList(ctx, reader, keys, func(key string) runindex.Filter {
		return runindex.Filter{Slugs: []string{slug}, PlanTask: key}
	})
	if err != nil {
		return nil, err
	}
	prs, seen := []int{}, map[int]bool{}
	for _, e := range entries {
		if n := planEntryPR(e); n != 0 && !seen[n] {
			seen[n] = true
			prs = append(prs, n)
		}
	}
	following, err := planIndexList(ctx, reader, prs, func(n int) runindex.Filter {
		return runindex.Filter{Slugs: []string{slug}, PR: n}
	})
	if err != nil {
		return nil, err
	}
	out := make([]runindex.Entry, 0, len(entries)+len(following))
	byID := map[string]bool{}
	for _, e := range append(entries, following...) {
		if !byID[e.RunID] {
			byID[e.RunID] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// planIndexList makes the queries one at a time and answers them in the order
// the keys were given, so the join reads the same runs in the same order
// whatever order the index answers in. One query is one page of the index (the
// reader pages internally: Filter.Limit is 0, every match), and the queries are
// made at planIndexWorkers at once, because each is a network round trip and
// there are as many of them as the plans hold tasks.
//
// An error is returned as it is read: a join built from the queries that
// happened to succeed would print a short list, which is the one thing a
// reader of the index may not do.
func planIndexList[K any](ctx context.Context, reader runindex.Reader, keys []K, filter func(K) runindex.Filter) ([]runindex.Entry, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	type answer struct {
		page runindex.Page
		err  error
	}
	answers := make([]answer, len(keys))
	parallelN(len(keys), planIndexWorkers, func(i int) {
		answers[i].page, answers[i].err = reader.List(ctx, filter(keys[i]))
	})
	out := []runindex.Entry{}
	for i, a := range answers {
		if a.err != nil {
			return nil, fmt.Errorf("reading the run index for %v: %w", keys[i], a.err)
		}
		out = append(out, a.page.Entries...)
	}
	return out, nil
}

// planEntryPR is the pull request number a run's entry carries, the way the
// join reads it (plan/prOf): the record's own number when it recorded one, the
// run's otherwise.
func planEntryPR(e runindex.Entry) int {
	if e.Record != nil && e.Record.PR != nil && e.Record.PR.Number > 0 {
		return e.Record.PR.Number
	}
	return e.PR
}

// planRunFacts turns the entries the index answered with into the facts the
// join reads. It is the whole seam between the two: everything a job wrote is
// either already sanitised by the index's own reader (the plan task, which is
// shape-checked where it is folded in) or sanitised here (the stage), and
// nothing else crosses.
func planRunFacts(entries []runindex.Entry) []plan.RunFact {
	out := make([]plan.RunFact, 0, len(entries))
	for _, e := range entries {
		f := plan.RunFact{
			RunID: e.RunID, Repo: e.Repo, Status: string(e.Status), PR: e.PR,
			PlanTask: planEntryTask(e),
		}
		if e.Record != nil {
			f.Stage = safetext.Field(e.Record.Stage)
			f.Outcome = string(e.Record.Outcome)
			if e.Record.PR != nil {
				f.RecordPR, f.PRURL = e.Record.PR.Number, e.Record.PR.URL
			}
		}
		out = append(out, f)
	}
	return out
}

// planEntryTask is the plan task an entry may be joined by, and "" for one the
// launcher could never have written. The id is display text a job wrote into
// its own launch record (runview's plan_task rule, D4): it is shape-checked
// here exactly as the row reader checks it, so a claim of another shape — one
// carrying a line break, one that is not a plan task at all — joins nothing.
func planEntryTask(e runindex.Entry) string {
	if task.PlanTaskProblem(e.PlanTask) != "" {
		return ""
	}
	return safetext.Field(e.PlanTask)
}

// planMerges asks gh about every pull request the joined runs carry, once each,
// and answers the join's lookup from the answers.
//
// It is handed the runs the join reads (plan.JoinedRuns) and no others: a
// lookup spawns a process, and asking about every pull request of a repository's
// whole run history would make a command the console calls to draw a graph cost
// whatever the history costs. The answers are not cached across invocations
// either — watch's cache lives in this process, and this command is one process
// — so what bounds the spawns is this set and the dedup below.
//
// It goes through internal/watch rather than spawning gh here, and that is the
// point of it: the URL is matched against watch's own anchored GitHub pattern
// before anything is spawned, the argv is built from the parsed owner, repo and
// number rather than from the URL, the host is pinned, and the process group,
// the environment and the bounded buffers are the ones `watch` already uses. A
// second spawn path here would be a second answer to "which URLs may be handed
// to a subprocess".
//
// Nothing is refused when gh cannot answer: a lookup with no answer is exactly
// the state the join shows as "merge unknown".
//
// A run's pr_url is job-written, so a GitHub URL is asked about only when it
// names the checkout's own repository: a job that pointed its record at some
// other merged pull request would otherwise mark its own task done. Such a URL
// answers "known, not merged", so it neither closes the task nor earns the
// "merge unknown" mark. And the spawns are capped at planMaxLookups, newest
// runs first: what is past the cap has no answer, which the read shows as
// "merge unknown" and says in a warning.
func planMerges(ctx context.Context, facts []plan.RunFact, repo string, warn func(string)) plan.MergeCheck {
	type answer struct{ merged, known bool }
	byURL := map[string]answer{}
	ordered := slices.Clone(facts)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].RunID > ordered[j].RunID })
	urls := []string{}
	seen := map[string]bool{}
	skipped := 0
	for _, f := range ordered {
		if f.PRURL == "" || seen[f.PRURL] {
			continue
		}
		seen[f.PRURL] = true
		if prRepo, ok := watch.PRRepo(f.PRURL); ok && !planSameRepo(prRepo, repo) {
			byURL[f.PRURL] = answer{merged: false, known: true}
			continue
		}
		if len(urls) == planMaxLookups {
			skipped++
			continue
		}
		urls = append(urls, f.PRURL)
	}
	if skipped > 0 {
		warn(fmt.Sprintf("%d more pull requests than the %d this read looks up: their tasks show merge unknown", skipped, planMaxLookups))
	}
	answers := make([]answer, len(urls))
	pr := watch.NewPRState(watch.PRStateOptions{})
	parallelN(len(urls), planLookupWorkers, func(i int) {
		m, k := pr.MergeNow(ctx, urls[i])
		answers[i] = answer{merged: m, known: k}
	})
	for i, u := range urls {
		byURL[u] = answers[i]
	}
	return func(url string) (bool, bool) {
		a, ok := byURL[url]
		if !ok {
			return false, false
		}
		return a.merged, a.known
	}
}

func printPlanStatus(w io.Writer, doc planStatusDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	for _, line := range doc.Warnings {
		fmt.Fprintf(w, "warning: %s\n", line)
	}
	if len(doc.Streams) > 0 {
		parts := make([]string, 0, len(doc.Streams))
		for _, s := range doc.Streams {
			parts = append(parts, fmt.Sprintf("%s %s (next %s)", planCell(s.Name), planCell(s.Current), planCell(s.Next)))
		}
		fmt.Fprintf(w, "streams: %s\n", strings.Join(parts, ", "))
	}
	// The one line a short list has to carry (D3215): the index has never
	// finished a walk, so the runs behind these tasks are the ones it holds and
	// a task it has not reached reads as unjoined rather than as the truth.
	if !doc.IndexComplete {
		fmt.Fprintln(w, "index: still building; the runs joined here are the ones folded so far")
	}
	if len(doc.Plans) == 0 {
		fmt.Fprintf(w, "no plan found under %s\n", planDocsDir)
		return nil
	}
	for i, p := range doc.Plans {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printPlanStatusPlan(w, p)
	}
	fmt.Fprintf(w, "\n%d %s, %d %s: %d open, %d closed\n",
		doc.Totals.Plans, planWord(doc.Totals.Plans, "plan", "plans"),
		doc.Totals.Tasks, planWord(doc.Totals.Tasks, "task", "tasks"),
		doc.Totals.Open, doc.Totals.Closed)
	return nil
}

func printPlanStatusPlan(w io.Writer, p planStatusPlan) {
	stream := ""
	if p.Stream != nil {
		stream = ", stream " + planCell(*p.Stream)
	}
	fmt.Fprintf(w, "%s (%s%s)\n", planCell(p.Artifact), planCell(p.ID), stream)
	// What the plan has left to take and what it costs, over every task of it
	// — the ones collapsed below included — because the figure is the plan's
	// and not the listing's.
	if p.Estimate != nil {
		fmt.Fprintf(w, "  %s\n", planEstimateLine(*p.Estimate, ""))
	}
	if len(p.Releases) > 0 {
		parts := make([]string, 0, len(p.Releases))
		for _, r := range p.Releases {
			box := fmt.Sprintf("%s: %d %s, %d done, %d closed, %d open",
				releaseLabel(r), r.Total, planWord(r.Total, "task", "tasks"), r.Done, r.Closed, r.Open)
			parts = append(parts, box)
		}
		fmt.Fprintf(w, "  releases: %s\n", strings.Join(parts, "; "))
	}
	if len(p.Nodes) == 0 {
		fmt.Fprintf(w, "  no task to list (%d closed)\n", p.Closed)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ID\tSTATE\tLEVEL\tCRITICAL\tRELEASE\tRUN\tSTAGE\tPR\tTITLE")
	for _, n := range p.Nodes {
		crit := "-"
		if n.Critical {
			crit = "yes"
		}
		state := n.State
		if n.Mark != "" {
			state += " (" + n.Mark + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			planCell(n.ID), planCell(state), n.Level, crit,
			planCell(planDash(n.Release)), planCell(planDash(n.Run)), planCell(planDash(n.Stage)),
			planCell(planDash(planNumber(n.PRNumber))), planCell(n.Title))
	}
	_ = tw.Flush()
}

// releaseLabel is a release box's heading: its version, the name it spends and,
// when the stream has already shipped it, the word for that. The word is on the
// heading rather than implied by a colour, because "this release is out" is the
// one thing about a box that changes what a reader does with it.
func releaseLabel(r plan.StateRelease) string {
	label := planCell(r.Version)
	if r.Name != "" {
		label += " " + planCell(r.Name)
	}
	if r.Finished {
		label += " (shipped)"
	}
	return label
}

// planIndexWorkers is how many index queries one read makes at once. Each is a
// network round trip of its own and there are as many of them as the plans hold
// tasks, so they are made together; the width is this command's own rather than
// a listing's, because it is one graph's join and not a scan.
const planIndexWorkers = 8

// planIndexTable is the history the estimate is drawn from: the table the
// index's summary carries, which the history job's fold keeps over every run of
// the installation (F15, D3201). It is read rather than built here, and that is
// the whole of what changed about this read's history — it used to be the
// window's records, up to planEstimateRuns of them, read and cached on this
// machine.
//
// The summary's table is also left in this machine's cache, while the file
// there is older, and that is not a courtesy: `plan graph` and the
// orchestrator's brief read that file offline (planCachedTable,
// orchestratorBriefTable) and this command is its only writer, so a copy that
// stopped being refreshed here would leave both of them estimating from the day
// this read stopped folding history. It is written when the file is missing or
// older than the fold that produced the summary, so a console polling this read
// once a minute does not rewrite it once a minute.
//
// A summary no walk has folded yet carries no table, and then this machine's
// cache answers, and the seeds answer a machine that holds none: the fallback
// `plan graph` makes, for the same reason (D2714 as amended). The cache is not
// held to a freshness bound here as it was while this command filled it itself:
// it is only reached before the index's first fold, and a figure the machine
// already holds beats the seeds it would otherwise fall to.
func planIndexTable(sum runindex.Summary, warn func(string)) *estimate.Table {
	path, err := estimate.CachePath(os.Getenv, runtime.GOOS)
	if err != nil {
		warn("this machine's cache directory could not be placed: " + oneLine(err.Error()))
		return sum.Estimate
	}
	if sum.Estimate == nil {
		cached, err := estimate.ReadCache(path)
		if err != nil {
			return nil
		}
		return cached
	}
	if cached, err := estimate.ReadCache(path); err == nil && !cached.WrittenAt.Before(sum.Estimate.WrittenAt) {
		return sum.Estimate
	}
	if err := sum.Estimate.WriteCache(path); err != nil {
		warn("the estimate's cached history could not be written: " + oneLine(err.Error()))
	}
	return sum.Estimate
}

// planRunCosts is what each run the index answered with has cost so far and how
// long it has been going: the two figures a task of a plan is answered with
// beyond its state (D2723). Both come off the entry, which carries the run's
// own record and window whole, so neither costs a second read of the runs
// bucket — the read this command used to make for them (F1).
//
// A record with no cost block and a run that never started are the same absence
// here as they were on the row: the estimate counts that task's cost as unknown
// rather than as free, and measures no minutes for it.
func planRunCosts(entries []runindex.Entry, now time.Time) (map[string]*runstore.Record, map[string]time.Duration) {
	costs := make(map[string]*runstore.Record, len(entries))
	elapsed := make(map[string]time.Duration, len(entries))
	for _, e := range entries {
		if e.Record != nil {
			costs[e.RunID] = e.Record
		}
		if e.StartedAt != nil {
			elapsed[e.RunID] = now.Sub(*e.StartedAt)
		}
	}
	return costs, elapsed
}

// planEstimateInputs is what one plan's estimate needs beyond the plan itself:
// the history, the runs the join read and what each of them cost and has been
// going, the repository the join is scoped to, and the cap and the clock the
// formula takes.
type planEstimateInputs struct {
	table      *estimate.Table
	runs       []plan.RunFact
	costs      map[string]*runstore.Record
	elapsed    map[string]time.Duration
	repo       string
	maxRunning int
	now        time.Time
}

// of is one plan's estimate: what it has left to take and what it costs, from
// the history this read has just read and what the join says about each task
// (D2722, D2723).
//
// It is the same read `fugaro plan graph` makes, from the same shape of inputs,
// and the two callers differ in exactly the one thing D2714 as amended says
// they do: this one read the history, and that one reads the cache back.
//
// An artifact older than the schema that carries a task's tier is not estimated
// at all: there is no tier in it to estimate a task from, and the estimate is a
// schema-3 answer (D2711) — so the block is absent, which is what a screen
// already reads as "no estimate here".
func (in planEstimateInputs) of(p plan.PlanRef, st plan.StatePlan) (*plan.Estimate, error) {
	if p.Doc.SchemaVersion < plan.SchemaVersion {
		return nil, nil
	}
	// Fugaro's own default mode, for `plan graph`'s reason: a plan's tasks are
	// launched from a repository whose rigor this read does not resolve.
	est, err := plan.EstimatePlan(p.Doc, in.table, in.progress(p.ID, p.Doc, st.Nodes), in.maxRunning, rigor.Default, in.now)
	if err != nil {
		return nil, err
	}
	return &est, nil
}

// progress is what this read knows about each task of a plan beyond its graph:
// whether its work is done or going, how long the run working on it has been
// going and what that run has cost so far (D2722, D2723).
//
// Two readings meet here, and each is the one that already knows the answer.
// Whether a task is done or running is the join's own (plan.State: a confirmed
// merge first, then a live run, then a cancellation) and is taken as it stands
// — a second reading of the same facts here would be a second answer to a
// question the table above the line already prints. The run beside it is the
// latest one that claims the task, found by the join's own rule (i): the plan's
// id, a slash, and a task of this plan, in the checkout's own repository. It is
// found here rather than read off the StateNode because that carries four
// characters of a run id (task.ShortRun), and four characters name a run to a
// person and to nobody else.
//
// A task the join calls cancelled is neither done nor running, and the estimate
// counts it as work that has not started — the reading that keeps a cancelled
// task's minutes in the plan's answer rather than dropping the work it was for.
func (in planEstimateInputs) progress(planID string, doc plan.Document, nodes []plan.StateNode) map[string]plan.NodeProgress {
	ids := make(map[string]bool, len(doc.Nodes))
	for _, n := range doc.Nodes {
		ids[n.ID] = true
	}
	latest := map[string]plan.RunFact{}
	for _, r := range in.runs {
		if !planSameRepo(r.Repo, in.repo) {
			continue
		}
		id := planClaim(planID, r.PlanTask, ids)
		if id == "" {
			continue
		}
		if cur, ok := latest[id]; !ok || r.RunID > cur.RunID {
			latest[id] = r
		}
	}
	out := make(map[string]plan.NodeProgress, len(nodes))
	for _, n := range nodes {
		done, running := n.State == plan.StateDone, n.State == plan.StateRunning
		if !done && !running {
			continue
		}
		p := plan.NodeProgress{Done: done, Running: running}
		if r, ok := latest[n.ID]; ok {
			if elapsed, started := in.elapsed[r.RunID]; started {
				p.ElapsedMinutes = elapsed.Minutes()
			}
			// A record with no cost block is no figure (a result.json from
			// before the breakdown existed), and the plan's total then reads
			// unknown rather than counting that task as free (D2723).
			if rec := in.costs[r.RunID]; rec != nil && rec.Cost != nil {
				cost := rec.Cost.TotalUSD
				p.CostUSD = &cost
			}
		}
		out[n.ID] = p
	}
	return out
}

// planClaim is the task of this plan a run's plan_task claims, and "" for a
// claim that joins nothing. It is the join's own rule, read here for the one
// field the join does not hand back (the runs a task's state was made of): the
// claim splits at its last slash, because a plan id is a path under docs/ and
// may hold slashes of its own, and the task it names has to be one this graph
// carries.
func planClaim(planID, planTask string, ids map[string]bool) string {
	i := strings.LastIndexByte(planTask, '/')
	if i <= 0 || i == len(planTask)-1 || planTask[:i] != planID {
		return ""
	}
	if taskID := planTask[i+1:]; ids[taskID] {
		return taskID
	}
	return ""
}

// planSameRepo compares two repositories the way the join does
// (task.CanonicalRepo: the ASCII fold, a trailing .git trimmed). An unknown
// repository matches nothing.
func planSameRepo(a, b string) bool {
	ca, errA := task.CanonicalRepo(a)
	cb, errB := task.CanonicalRepo(b)
	return errA == nil && errB == nil && ca == cb
}

// planDash is a cell for a value a task may not have: a dash is what the human
// tables of this tool print for an absent one, so a column never looks like a
// value that is there.
func planDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// planNumber is a number as the same shape of cell.
func planNumber(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}
