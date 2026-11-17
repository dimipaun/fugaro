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
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/streams"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/watch"
)

// `fugaro plan status` is the console v2 plan's GV1.6: every plan's graph, joined
// to the runs that are working on it.
//
// Unlike `plan graph`, which is a local read of one artifact, this is a cloud
// read: the state of a task is a run's state, and a run lives in the runs bucket
// of the selected project. So it takes the cloud flags `runs ls` takes, and it
// reads the runs of the checkout's own repository — the plans it prints are the
// checkout's, and a run of another repository is not theirs to claim (GV1.6's
// rule ii; the plan's constraint C3).
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
			"A task's state joins the graph to the runs bucket of the selected project:\n" +
			"running with the stage of the run working on it, done when the run's pull\n" +
			"request is merged, cancelled when the task's only run was, blocked while a\n" +
			"task it needs is not done, ready when the work can start (the task a launch\n" +
			"may act on), and not-started otherwise.\n\n" +
			"A run joins a task by the --plan-task its launch recorded, and only when it\n" +
			"is a run of this checkout's own repository: a run of another repository\n" +
			"naming the same task is ignored. The join is display-only — nothing here\n" +
			"makes another plan's task ready.\n\n" +
			"Every run the bucket still holds (ninety days) is listed. Only the runs that\n" +
			"join a task of a plan here have their pull requests looked up, so the\n" +
			"lookups cost what the plans hold rather than what the repository's history\n" +
			"holds, and one read asks about at most 32 pull requests, the newest runs'\n" +
			"first.\n\n" +
			"Whether a pull request is merged is asked of your own gh, when it is\n" +
			"installed, and only about a pull request of this checkout's own\n" +
			"repository: a run that recorded another repository's pull request is not\n" +
			"merged here. Where gh cannot answer (a Bitbucket repository, no gh, a\n" +
			"lookup that failed, one past the 32) a task whose run recorded a ready pull\n" +
			"request shows done with the mark \"merge unknown\" rather than nothing.\n\n" +
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
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

type planStatusOptions struct {
	cloud  cloudOptions
	all    bool
	asJSON bool
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
		rows   []runview.Row
		table  *estimate.Table
		recs   map[string]*runstore.Record
		joined []plan.RunFact
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
		// The listing is bounded by the runs bucket's own age, the window
		// `run --pr` reads with (followUpLookback is that lifecycle): a join
		// wants every run the bucket still holds — an older one has been
		// deleted with its run and can never join a task — and nothing beyond
		// it, so the read costs what the bucket holds rather than everything
		// the repository has ever launched.
		rows, err = loadRows(ctx, env, lsFilter{
			slugs: []string{slug}, since: now.Add(-followUpLookback), warn: errw,
		}, now)
		if err != nil {
			return err
		}
		facts = planRunFacts(rows)
		// The join reads the runs that claim one of these plans and no others, and
		// everything spent per run is spent on those — the merge lookup above all,
		// which spawns a process per distinct pull request. Scoping it to the runs
		// the join reads is what keeps a command the console calls to draw a graph
		// from costing what the plans hold rather than what the repository's whole
		// history holds.
		joined = plan.JoinedRuns(plans, facts, doc.Repo)
		table = planEstimateTable(ctx, env, slug, now, warn)
		recs = planRunRecords(ctx, env, slug, joined, warn)
	}

	merge := planMerges(ctx, joined, doc.Repo, warn)

	// What every plan's estimate is computed from, gathered once: the history
	// the runs bucket just answered with, the rows the join read, and the cap
	// and the clock the formula takes.
	in := planEstimateInputs{
		table: table, rows: rows, recs: recs, repo: doc.Repo, maxRunning: maxRunning, now: now,
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

// planRunFacts turns the rows the runs read produced into the facts the join
// reads. It is the whole seam between the two: everything a job wrote is either
// already sanitised by the reader (plan_task) or sanitised here (the stage), and
// nothing else crosses.
func planRunFacts(rows []runview.Row) []plan.RunFact {
	out := make([]plan.RunFact, 0, len(rows))
	for _, r := range rows {
		out = append(out, plan.RunFact{
			RunID: r.RunID, Repo: r.Repo, PlanTask: r.PlanTask,
			Status: r.Status, Stage: safetext.Field(r.Stage), Outcome: string(r.Outcome),
			PR: r.PR, RecordPR: r.RecordPR, PRURL: r.PRURL,
		})
	}
	return out
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

// planEstimateRuns bounds the history the estimate is drawn from. A plan's
// figures are per tier and per rigor, so the sample is the runs behind those
// groups rather than the runs behind one plan, and the window is the runs
// bucket's own (followUpLookback, as the join's listing is). It is the same
// bound `runs stats` reads its window with, for the same reason: past it a read
// is one nobody asked for.
const planEstimateRuns = 5000

// planEstimateRefresh is how old the cached history may be before this read
// pays for the window's records again. It is what keeps a command the console
// polls about once a minute from re-reading the window's objects on every poll:
// the table is a distribution over tiers and modes, which moves as runs finish
// rather than as a reader looks, while everything else this estimate prints —
// which task is done or going, how long its run has been going, and what that
// run has cost — is as fresh as the join beside it.
const planEstimateRefresh = 6 * time.Hour

// planEstimateTable is the history the estimate is drawn from: the table an
// earlier read of this machine cached, while it is younger than
// planEstimateRefresh, and the window's records read and cached again when it
// is not (D2714 as amended).
//
// The window is the runs bucket's own (followUpLookback, as the join's listing
// is) and the read is bounded (planEstimateRuns), because a plan's figures are
// per tier and per rigor: the sample is the runs behind those groups rather
// than the runs behind one plan, and past the bound it is a read nobody asked
// for. The records are read here as records (readRunObjects, the one reader
// behind runs stats and report) rather than off the listing's rows, which
// carry neither the tier nor the finish time a sample is grouped by —
// runview.Row is deliberately narrower than a record.
//
// A history that cannot be read is a warning and not the end of the read: the
// estimate then answers every tier its seed minutes with no cost at all
// (D2712), and the line says which sample answered each task. A cache that
// cannot be read is the same window read again, which is what heals it; one
// that cannot be written costs the offline reads the seeds, which is what they
// fall back to on a machine that has never run this command.
func planEstimateTable(ctx context.Context, env *cloudEnv, slug string, now time.Time, warn func(string)) *estimate.Table {
	path, err := estimate.CachePath(os.Getenv, runtime.GOOS)
	if err != nil {
		warn("this machine's cache directory could not be placed: " + oneLine(err.Error()))
		path = ""
	} else if cached, err := estimate.ReadCache(path); err == nil && now.Sub(cached.WrittenAt) < planEstimateRefresh {
		return cached
	}
	objs, _, _, err := readRunObjects(ctx, env, []string{slug}, now.Add(-followUpLookback), time.Time{}, planEstimateRuns)
	if err != nil {
		warn("this repository's run history could not be read: the estimate is its tiers' seed minutes alone")
		return nil
	}
	records := make([]runstore.Record, 0, len(objs))
	for _, o := range objs {
		if o.record != nil {
			records = append(records, *o.record)
		}
	}
	table := estimate.Build(records, now)
	if path != "" {
		if err := table.WriteCache(path); err != nil {
			warn("the estimate's cached history could not be written: " + oneLine(err.Error()))
		}
	}
	return table
}

// planRunRecords reads the record of every run the join is about — the runs
// that claim a task of one of this checkout's plans, and no others. It is what
// tells a done or running task what its work has actually cost (D2723), and
// without it the plan's total could not be a figure at all.
//
// They are read through the run cache the listing reads its own rows with
// (lsReaders, readRunVia): the join has just read these runs' objects, so a
// poll of this command reads them off this machine rather than out of the
// bucket again — and the set is the runs the plans claim, not the window's. A
// record that cannot be read is one task's cost unknown, which the estimate
// counts and the line prints; the read says so once rather than dropping it in
// silence.
func planRunRecords(ctx context.Context, env *cloudEnv, slug string, joined []plan.RunFact, warn func(string)) map[string]*runstore.Record {
	refs := make([]lsRef, 0, len(joined))
	for _, f := range joined {
		refs = append(refs, lsRef{slug: slug, id: f.RunID})
	}
	if len(refs) == 0 {
		return nil
	}
	readers := lsReaders(ctx, env, true, refs)
	recs := make(map[string]*runstore.Record, len(refs))
	var unread int
	for _, r := range refs {
		run, err := readRunVia(ctx, env, readers[r], r.slug, r.id, 0)
		switch {
		case err != nil:
			unread++
		case run.in.Record != nil:
			recs[r.id] = run.in.Record
		}
	}
	if unread > 0 {
		warn(fmt.Sprintf("the runs this plan's tasks carry could not all be read, %d of %d: a task of them the estimate found no cost for counts as unknown",
			unread, len(refs)))
	}
	return recs
}

// planEstimateInputs is what one plan's estimate needs beyond the plan itself:
// the history, the runs the join read and what their records cost, the
// repository the join is scoped to, and the cap and the clock the formula
// takes.
type planEstimateInputs struct {
	table      *estimate.Table
	rows       []runview.Row
	recs       map[string]*runstore.Record
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
	latest := map[string]runview.Row{}
	for _, r := range in.rows {
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
			if elapsed, started := r.Elapsed(in.now); started {
				p.ElapsedMinutes = elapsed.Minutes()
			}
			// A record with no cost block is no figure (a result.json from
			// before the breakdown existed), and the plan's total then reads
			// unknown rather than counting that task as free (D2723).
			if rec := in.recs[r.RunID]; rec != nil && rec.Cost != nil {
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
