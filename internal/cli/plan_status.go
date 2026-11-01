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
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/plan"
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
	Plans []plan.StatePlan `json:"plans"`
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
		Plans: []plan.StatePlan{}, Streams: []plan.StateStream{}, Warnings: []string{},
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

	// The runs of this checkout's own repository. An origin that cannot be read
	// is not a reason to refuse the read: a plan's graph says a great deal on
	// its own, and what is lost is said in a warning. The join itself is
	// fail-closed either way — plan.State joins nothing when the repository is
	// unknown.
	facts := []plan.RunFact{}
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
		// The listing is bounded by the runs bucket's own age, the window
		// `run --pr` reads with (followUpLookback is that lifecycle): a join
		// wants every run the bucket still holds — an older one has been
		// deleted with its run and can never join a task — and nothing beyond
		// it, so the read costs what the bucket holds rather than everything
		// the repository has ever launched.
		now := time.Now().UTC()
		rows, err := loadRows(ctx, env, lsFilter{
			slugs: []string{slug}, since: now.Add(-followUpLookback), warn: errw,
		}, now)
		if err != nil {
			return err
		}
		facts = planRunFacts(rows)
	}

	// The plans are read before a single run is asked about. Which plans exist
	// and which tasks they carry is what decides which runs the join reads, and
	// therefore which pull requests are looked up at all (planMerges); an
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
		id := planIDFromPath(path)
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

	// The join reads the runs that claim one of these plans and no others, and
	// everything spent per run is spent on those — the merge lookup above all,
	// which spawns a process per distinct pull request. Scoping it to the runs
	// the join reads is what keeps a command the console calls to draw a graph
	// from costing what the plans hold rather than what the repository's whole
	// history holds.
	joined := plan.JoinedRuns(plans, facts, doc.Repo)
	merge := planMerges(ctx, joined, doc.Repo, func(sentence string) {
		fmt.Fprintf(errw, "warning: %s\n", sentence)
		doc.Warnings = append(doc.Warnings, sentence)
	})

	for _, p := range plans {
		path := paths[p.ID]
		st, err := plan.State(p.ID, p.Doc, joined, plan.StateOptions{
			Artifact: path, Repo: doc.Repo, All: o.all, Catalogue: cat, Merge: merge,
		})
		if err != nil {
			fmt.Fprintf(errw, "warning: %s\n", oneLine(err.Error()))
			doc.Warnings = append(doc.Warnings, planCell(path)+" could not be read: its tasks are not listed")
			continue
		}
		doc.Plans = append(doc.Plans, st)
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

// planIDFromPath is the join's plan id for an artifact under docs/ (GV1.6's
// rule i): the id a launch writes before the slash in --plan-task.
//
// For an artifact directly under docs/ it is the file's stem, for
// docs/plans/<stem>.graph.json it is "plans/<stem>" — the plan documents in
// docs/plans/ are named by their date and title rather than by a directory — and
// for anything deeper it is the directory's path under docs/. The id is where
// the artifact sits, so moving a plan renames it, which is the trade rule (i)
// makes: a path is something every reader can compute the same way, and nothing
// has to be registered anywhere for a plan to have a name.
func planIDFromPath(path string) string {
	rel := strings.TrimPrefix(filepath.ToSlash(path), planDocsDir+"/")
	stem := strings.TrimSuffix(rel, plan.ArtifactSuffix)
	dir, file := filepath.Split(stem)
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	switch dir {
	case "", ".":
		return file
	case "plans":
		return "plans/" + file
	}
	return dir
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

func printPlanStatusPlan(w io.Writer, p plan.StatePlan) {
	stream := ""
	if p.Stream != nil {
		stream = ", stream " + planCell(*p.Stream)
	}
	fmt.Fprintf(w, "%s (%s%s)\n", planCell(p.Artifact), planCell(p.ID), stream)
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
