package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	cmd.AddCommand(newPlanGraphCmd())
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
			return printPlanGraph(cmd.OutOrStdout(), graph, asJSON)
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

func printPlanGraph(w io.Writer, graph plan.Graph, asJSON bool) error {
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
	return nil
}

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
