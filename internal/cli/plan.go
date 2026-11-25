package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// `fugaro plan graph` is the console v2 plan's V2.2.1: the read that prints a
// plan's graph artifact.
//
// The artifact is plan-side (design D25): the planner writes it beside the plan
// it describes, as `<plan>.graph.json`, and this command is how anything else
// sees it. The console in particular never opens the file — it spawns this
// command and reads its `--json` document, the way it reads every other figure
// (design D1, §3.4 rule 7) — so the schema is the CLI's to validate and to
// state, and the console's to render.
//
// It is a local read: it opens no bucket, needs no project selected and takes
// no cloud flags, which is why it is not in the flag inventory's `--project`
// set (the console's JSON contract, §8).
func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Read a plan's graph artifact"}
	cmd.AddCommand(newPlanGraphCmd(), newPlanStatusCmd())
	return cmd
}

func newPlanGraphCmd() *cobra.Command {
	var planPath string
	var asJSON bool
	var done []string
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print a plan's graph: its tasks, their dependencies and their footprints",
		Long: "Print the graph artifact written beside a plan.\n\n" +
			"The artifact is `<plan>.graph.json` — for docs/plans/foo.md it is\n" +
			"docs/plans/foo.graph.json. With no --plan, the one graph artifact under\n" +
			"docs/plans/ is read; more than one is refused rather than guessed at.\n\n" +
			"A graph this build does not understand is refused rather than rendered: a\n" +
			"future schema version, a malformed document, an edge to a node that is not\n" +
			"there, or a cycle.\n\n" +
			"When the plan ran the contract-first decoupling pass, the read prints that\n" +
			"pass's own record too: every edge the pass broke with the contract and the\n" +
			"integration task that cover it, what the break bought measured before and\n" +
			"after, and the contract stubs still in the checkout. A plan that broke no\n" +
			"edge prints none of it.\n\n" +
			"The read also answers what the planner cannot: each task's level, whether it\n" +
			"is ready or blocked, and the plan's critical path. A task is ready when every\n" +
			"task it needs is done, so --done names the tasks that are — an id the graph\n" +
			"does not carry is refused rather than ignored. This is a local read and it\n" +
			"writes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// `--done ""` is pflag's empty list, not a done set: it is
			// silently accepted by StringSlice and would otherwise read as
			// "no tasks are done". An empty id is refused on the flag for the
			// same reason an unknown one is (see the long help) -- and the
			// flag has to be *given* as empty, since with no --done at all
			// no done set is a legitimate read.
			if cmd.Flags().Changed("done") && len(done) == 0 {
				return fmt.Errorf("--done names no task; an empty id is refused rather than ignored")
			}
			artifact, err := resolveGraphArtifact(planPath)
			if err != nil {
				return err
			}
			doc, err := plan.ReadFile(artifact)
			if err != nil {
				return err
			}
			graph, err := plan.Analyze(artifact, doc, done)
			if err != nil {
				return err
			}
			est, from, err := planGraphEstimate(cmd.ErrOrStderr(), doc, graph)
			if err != nil {
				return err
			}
			return printPlanGraph(cmd.OutOrStdout(), graph, est, from, asJSON)
		},
	}
	cmd.Flags().StringVar(&planPath, "plan", "", "the plan document the graph sits beside (default: the one graph artifact under docs/plans)")
	cmd.Flags().StringSliceVar(&done, "done", nil, "the tasks whose work is done, by id (comma-separated or repeated); a task is ready when every task it needs is done")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

// resolveGraphArtifact turns --plan into the artifact to read. A plan names its
// own artifact (plan.ArtifactPath); with no plan given, the checkout's
// docs/plans/ directory is searched, and **more than one artifact is a refusal
// rather than a pick** — a console pointed at a checkout with two plans must be
// told which, not handed whichever one a directory listing happened to put
// first.
func resolveGraphArtifact(planPath string) (string, error) {
	if planPath != "" {
		return plan.ArtifactPath(planPath), nil
	}
	matches, err := filepath.Glob(filepath.Join("docs", "plans", "*"+plan.ArtifactSuffix))
	if err != nil {
		return "", fmt.Errorf("searching docs/plans for a graph artifact: %w", err)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no graph artifact under docs/plans; pass --plan <plan document>, whose graph sits beside it as <plan>%s", plan.ArtifactSuffix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("docs/plans carries %d graph artifacts; pass --plan to say which:\n  %s",
			len(matches), strings.Join(matches, "\n  "))
	}
}

func printPlanGraph(w io.Writer, graph plan.Graph, est *plan.Estimate, from string, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(graph)
	}
	fmt.Fprintf(w, "plan %s (schema version %d, %d %s)\n",
		pluginwire.Printable(graph.Plan), graph.SchemaVersion, graph.Levels, planWord(graph.Levels, "level", "levels"))
	if len(graph.Nodes) == 0 {
		fmt.Fprintln(w, "this graph carries no tasks")
		return nil
	}
	needs := make(map[string][]string, len(graph.Nodes))
	for _, e := range graph.Edges {
		needs[e.Task] = append(needs[e.Task], e.Needs)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tLEVEL\tSTATE\tVERSION\tRUN\tNEEDS\tFOOTPRINT")
	for _, n := range graph.Nodes {
		version, run := "-", "-"
		if n.Version != nil {
			version = *n.Version
		}
		if n.Run != nil {
			run = *n.Run
		}
		deps := "-"
		if len(needs[n.ID]) > 0 {
			deps = strings.Join(needs[n.ID], ",")
		}
		// The footprint column says what the paths are as well as which: a
		// prediction, never a measurement (D28). The count is of the patterns
		// the planner wrote, and the word is the label.
		fp := "predicted: " + strings.Join(n.Footprint.Paths, " ")
		if len(n.Footprint.Paths) == 0 {
			fp = "predicted: none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			planCell(n.ID), planCell(n.Title), n.Level, planState(n), planCell(version), planCell(run),
			planCell(deps), planCell(fp))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// The read's own answers, in the same words the document uses, so a
	// terminal shows what a console would draw.
	path := make([]string, 0, len(graph.CriticalPath.Tasks))
	for _, id := range graph.CriticalPath.Tasks {
		path = append(path, planCell(id))
	}
	fmt.Fprintf(w, "critical path (%d %s): %s\n",
		graph.CriticalPath.Length, planWord(graph.CriticalPath.Length, "task", "tasks"), strings.Join(path, " -> "))
	if graph.Bottleneck != nil {
		fmt.Fprintf(w, "bottleneck: %s\n", planCell(*graph.Bottleneck))
	}
	// What the plan has left to take and what it costs (S3, D2714 as amended).
	// It is the same line `plan status` prints, and `from` says which of the
	// cache and the seeds answered it, since this read may not reach the
	// network.
	if est != nil {
		fmt.Fprintln(w, planEstimateLine(*est, from))
	}
	if graph.Gain != nil {
		if err := printPlanDecoupling(w, graph); err != nil {
			return err
		}
	}
	return nil
}

// printPlanDecoupling prints what the contract-first pass did: every edge it
// broke with the contract that broke it and the integration task that covers
// it, what the break bought measured before and after, and the contract stubs
// still in the checkout. The figures are the read's own (decouple.go), which is
// why they are printed and not asked of the planner: the terminal says what the
// --json document carries.
//
// It is printed only when the read computed a gain, which is exactly when the
// document recorded a break, so a v2 artifact and a v3 one whose plan kept
// every edge print the lines they always did.
func printPlanDecoupling(w io.Writer, graph plan.Graph) error {
	fmt.Fprintln(w, "DECOUPLING")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tNEEDED\tCONTRACT\tINTEGRATION\tREASON")
	for _, d := range graph.Decoupling {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			planCell(d.Was.Task), planCell(d.Was.Needs), planCell(d.Contract),
			planCell(d.Integration), planCell(d.Reason))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	gain := *graph.Gain
	verdict := "no gain"
	if gain.Improves {
		verdict = "improves"
	}
	fmt.Fprintf(w, "gain: %d broken, %d kept, %d added; before %d %s, weight %s; after %d %s, weight %s; %s\n",
		gain.Broken, gain.Kept, gain.AddedTasks,
		gain.Before.Tasks, planWord(gain.Before.Tasks, "task", "tasks"), planWeight(gain.Before.Weight),
		gain.After.Tasks, planWord(gain.After.Tasks, "task", "tasks"), planWeight(gain.After.Weight), verdict)
	var remaining []string
	for _, n := range graph.Nodes {
		for _, stub := range n.StubsPresent {
			remaining = append(remaining, planCell(n.ID)+": "+planCell(stub))
		}
	}
	if len(remaining) > 0 {
		fmt.Fprintf(w, "stubs remaining: %s\n", strings.Join(remaining, ", "))
	}
	return nil
}

// planWeight is a path's weight as a person reads it: the shortest form that
// says the number exactly, so a size of one prints as "1" rather than "1.0".
func planWeight(weight float64) string { return strconv.FormatFloat(weight, 'f', -1, 64) }

// planState is a task's one word: done, ready or blocked. The three fields are
// the document's (V2.2.3, design F7) and this reads them in the order that
// makes the word unique -- a task is done, ready or blocked, never two.
func planState(n plan.NodeState) string {
	switch {
	case n.Done:
		return "done"
	case n.Ready:
		return "ready"
	case n.Blocked:
		return "blocked"
	}
	return "-"
}

// planWord is singular or plural for a count, so the terminal does not say
// "1 tasks".
func planWord(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// planCell is one cell of the human table: Printable's escaping and length
// cap, and then no tab — Printable keeps one, and in a tabwriter row a tab
// from the artifact would open a column of its own.
func planCell(s string) string { return cellOneLine(pluginwire.Printable(s)) }

// planGraphEstimate is this read's estimate: what the graph has left to take
// and what it costs, from the history this machine last cached and the seeds
// (D2714 as amended).
//
// The history is not read here and never will be: this command may not reach
// the network, so the table is the one `fugaro plan status` wrote, read back
// from the same path that command writes it to (estimate.CachePath), and a
// machine whose cache is not there yet leaves every tier its seed minutes and
// no cost at all — the state every repository starts in (D2712). A cache this
// release cannot read is a warning rather than a refusal: the graph is still
// printable offline, and the estimate says which of the two answered it.
//
// The estimate is a schema-3 answer. `tier` is a field of that schema (D2711),
// so an older artifact has no tier to estimate a task from and prints no line
// at all — the v2 document is a contract a console reads byte for byte, and
// this read adds nothing to it.
//
// The tasks `--done` names are the ones the estimate weighs as finished: that
// is the recalibration the flag buys, and it is the same done set the table a
// reader just read was drawn with.
func planGraphEstimate(warn io.Writer, doc plan.Document, graph plan.Graph) (*plan.Estimate, string, error) {
	if doc.SchemaVersion < plan.SchemaVersion {
		return nil, "", nil
	}
	table, from := planCachedTable(warn)
	state := make(map[string]plan.NodeProgress, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.Done {
			state[n.ID] = plan.NodeProgress{Done: true}
		}
	}
	// The mode is Fugaro's own default (D19): this command reads one artifact
	// and takes no project, so there is no repository to resolve a rigor for,
	// and Pro is what a repository that configures nothing runs at.
	est, err := plan.EstimatePlan(doc, table, state, localcfg.DefaultMaxParallel, rigor.Default, time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	return &est, from, nil
}

// planCachedTable is the history this machine holds, with the phrase the line
// says it used. It is the whole of this read's own offline half: a path that
// cannot be placed and a file that cannot be read are the seeds again, said in
// one sentence of ours rather than as a refusal.
//
// The cap the estimate is computed at is localcfg.DefaultMaxParallel: this
// command takes no project, so the person's own `max_parallel` — the figure
// `max_running` defaults to (A6) — is not here to read, and the default is what
// a checkout with no project layer runs at.
func planCachedTable(warn io.Writer) (*estimate.Table, string) {
	path, err := estimate.CachePath(os.Getenv, runtime.GOOS)
	switch {
	case err != nil:
		fmt.Fprintf(warn, "warning: %s\n", oneLine(err.Error()))
		return nil, "from the seeds: this machine has no place to cache history"
	}
	table, err := estimate.ReadCache(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "from the seeds: no history is cached on this machine yet"
	case err != nil:
		fmt.Fprintf(warn, "warning: %s\n", oneLine(err.Error()))
		return nil, "from the seeds: the cached history could not be read"
	}
	return table, "from the cached history of " + table.WrittenAt.UTC().Format("2006-01-02 15:04 MST")
}

// planEstimateLine is the one line both reads print: when the plan is ready and
// what it costs, the count of tasks it is over, and the sample each task's
// figure came from (D2714's "history for 9, seeds for 3").
//
// from is where the table came from, and it is "" for the read whose history is
// the index's own — `plan status` — and a phrase for the offline one, which has
// to say which of the cache and the seeds answered it.
//
// The two ranges are printed as a reader reads a number: whole minutes and
// cents, so a seed of 90 minutes prints "45–180 min" rather than
// "45.00000000000001". A cost the plan cannot state prints D2723's own words
// instead of a range: the tasks underneath are counted, because a figure that
// added up the ones it knew would read as the plan's whole cost.
func planEstimateLine(est plan.Estimate, from string) string {
	cost := fmt.Sprintf("no history yet for %d %s", est.CostUnknownTasks,
		planWord(est.CostUnknownTasks, "task", "tasks"))
	if est.CostUSD != nil {
		cost = fmt.Sprintf("$%s–$%s", planDollars(est.CostUSD.Low), planDollars(est.CostUSD.High))
	}
	parts := []string{fmt.Sprintf("%d %s", len(est.Nodes), planWord(len(est.Nodes), "task", "tasks"))}
	if bases := planBases(est.Nodes); bases != "" {
		parts = append(parts, bases)
	}
	if from != "" {
		parts = append(parts, from)
	}
	return fmt.Sprintf("ready in %d–%d min, %s (%s)",
		planMinutes(est.ReadyInMinutes.Low), planMinutes(est.ReadyInMinutes.High), cost,
		strings.Join(parts, "; "))
}

// planBases counts the plan's tasks by the sample their figure came from, in
// the order the estimate reaches for those samples: the history, the tier, the
// seed, and the plan's own assumed tier for a task it gave none. A sample no
// task was answered from is left out rather than printed as a zero.
func planBases(nodes []plan.EstimateNode) string {
	words := []struct {
		basis estimate.Basis
		word  string
	}{
		{estimate.BasisHistory, "history"},
		{estimate.BasisTier, "tiers"},
		{estimate.BasisSeed, "seeds"},
		{plan.BasisAssumed, "assumed"},
	}
	counts := map[estimate.Basis]int{}
	for _, n := range nodes {
		counts[n.Basis]++
	}
	var parts []string
	for _, w := range words {
		if counts[w.basis] > 0 {
			parts = append(parts, fmt.Sprintf("%s for %d", w.word, counts[w.basis]))
		}
	}
	return strings.Join(parts, ", ")
}

// planMinutes and planDollars are one end of a range as a person reads it:
// whole minutes and whole cents, which is all the precision an estimate has.
// The number is spelled in its shortest exact form, so a range of three
// dollars prints "$3–$5" rather than "$3.00".
func planMinutes(v float64) int { return int(math.Round(math.Max(0, v))) }

func planDollars(v float64) string {
	return strconv.FormatFloat(math.Round(math.Max(0, v)*100)/100, 'f', -1, 64)
}
