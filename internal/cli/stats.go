package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/stats"
)

// fugaro stats (website plan task W0.3): the owner-run, read-only aggregation
// the caps-and-costs guide publishes its figures from. It is the only command
// whose whole value is what it does *not* print: percentiles per recipe over
// one window of one named installation, and no identifier of any kind
// (reconciliation D96, D97).
//
// Three properties are the command rather than niceties around it, and each
// has a test:
//
//   - One installation, named: --project is required and has no default, the
//     sandbox included, and there is no --all. An aggregation over "every
//     installation this operator can see" is a different, unauthorized
//     question.
//   - Read-only, and only result.json: the run records are read through the
//     store the other read commands use, and the task a run was given is never
//     read at all, so no task text can reach the output.
//   - Refused in a coding agent's session, like every other owner-only path.

// statsDefaultSince is stats runs' default window: the runs bucket's own
// lifecycle age (90 days), so the window holds every record the installation
// still has.
const statsDefaultSince = "90d"

// statsNotAPromise is the disclaimer every published figure travels with
// (reconciliation D97): what a cap is, that a range is a measurement over a
// window rather than a promise, that every figure carries its sample size, and
// that the bottom of every range is one run's own exact figure.
const statsNotAPromise = "A cap is a ceiling, not an estimate: in observe a run counts and logs what it\n" +
	"spent, and in enforce it halts. These figures are percentiles over one window of\n" +
	"one installation's own run records: what those runs did, not a promise about\n" +
	"another run. Every figure prints the number of runs behind it, and the min is\n" +
	"always one run's own exact figure -- as is the median of an odd number of runs,\n" +
	"and the p90 whenever 0.9 times (runs minus one) is a whole number."

type statsOptions struct {
	cloud   cloudOptions
	since   string
	minRuns int
	asJSON  bool
}

// statsDoc is stats runs --json's document. There is no field for a
// repository, run ID, branch, pull request, task, person or commit, and none
// for a mean: the document is every aggregate the command publishes.
type statsDoc struct {
	Project string `json:"project"`
	// Since and Until are the window's own dates (UTC, YYYY-MM-DD).
	Since   string `json:"since"`
	Until   string `json:"until"`
	MinRuns int    `json:"min_runs"`
	// Recipes are the published groups: a group below MinRuns is absent
	// rather than approximated.
	Recipes []statsRecipeDoc `json:"recipes"`
	Totals  statsTotalsDoc   `json:"totals"`
	// NotAPromise is the disclaimer the figures are published with
	// (reconciliation D97).
	NotAPromise string `json:"not_a_promise"`
}

// statsRecipeDoc is one recipe's figures. Runs is the sample size behind every
// one of them.
type statsRecipeDoc struct {
	Recipe              string            `json:"recipe"`
	Runs                int               `json:"runs"`
	ComputeNotEstimated int               `json:"compute_not_estimated"`
	DurationS           stats.Percentiles `json:"duration_s"`
	CostUSD             stats.Percentiles `json:"cost_usd"`
}

// statsTotalsDoc is the accounting behind the groups. Runs is every record a
// figure could be drawn from, so
// sum(recipes[].runs) + runs_below_min_runs + runs_unknown_recipe == runs.
type statsTotalsDoc struct {
	Runs               int `json:"runs"`
	Recipes            int `json:"recipes"`
	RunsBelowMinRuns   int `json:"runs_below_min_runs"`
	RunsUnknownRecipe  int `json:"runs_unknown_recipe"`
	RunsNotEstimated   int `json:"runs_not_estimated"`
	RunsWithoutAFigure int `json:"runs_without_a_figure"`
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Aggregate one installation's run records into figures a page can publish",
	}
	cmd.AddCommand(newStatsRunsCmd())
	return cmd
}

func newStatsRunsCmd() *cobra.Command {
	var o statsOptions
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Publish per-recipe duration and cost percentiles over a window, and nothing else",
		Long: "Aggregate one named installation's own run records into the figures the\n" +
			"caps-and-costs guide publishes: the minimum, the median and the 90th percentile\n" +
			"of a run's wall-clock duration and of its total cost, grouped by recipe.\n\n" +
			"The records are read read-only, over --since's window, from the installation\n" +
			"--project names -- and result.json alone is read: the task a run was given is\n" +
			"never read, so no task text can reach the output. Nothing a reader cannot look\n" +
			"up is printed: the groups are recipe names and the window, every figure is a\n" +
			"number or a count, and no field carries a repository, run ID, branch, pull\n" +
			"request, task, person or commit.\n\n" +
			"--project is required and has no default, the sandbox included: an aggregation is\n" +
			"about one named installation, and this command never guesses one. There is no\n" +
			"--all. The command is refused in a coding agent's session: the records are an\n" +
			"installation's own data, and reading them is the owner's step.\n\n" +
			"A recipe with fewer than --min-runs runs in the window is not published at all --\n" +
			"a range from four runs reads as a promise precisely because it is a range -- and\n" +
			"a run whose recipe this binary does not ship, or whose record carries no finished\n" +
			"duration or no cost breakdown, or whose result.json cannot be read at all, is\n" +
			"counted and not grouped: one bad object never makes the figures unproducible.\n\n" +
			"--json prints {project, since, until, min_runs, recipes[{recipe, runs,\n" +
			"compute_not_estimated, duration_s{min, median, p90}, cost_usd{min, median, p90}}],\n" +
			"totals{runs, recipes, runs_below_min_runs, runs_unknown_recipe, runs_not_estimated,\n" +
			"runs_without_a_figure}, not_a_promise}.\n\n" +
			"The closing line is the disclaimer the figures travel with: a cap is a ceiling\n" +
			"rather than an estimate, a range is what one window of one installation's runs did\n" +
			"rather than a promise, and every figure prints the number of runs behind it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runStatsRuns(cmd, &o) },
	}
	f := cmd.Flags()
	addCloudFlags(cmd, &o.cloud)
	f.Lookup("project").Usage = "the installation whose run records are read (required: this command never guesses one, and the sandbox is named like any other)"
	_ = cmd.MarkFlagRequired("project")
	f.StringVar(&o.since, "since", statsDefaultSince, "the window: Nd days back from today, or a Go duration (90d)")
	f.IntVar(&o.minRuns, "min-runs", stats.MinRuns, "the least runs a recipe may carry a range from (a thinner group is not published)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runStatsRuns(cmd *cobra.Command, o *statsOptions) error {
	// The records are data about an installation of the owner's own, so this
	// is refused under the marker every owner-only path uses, before anything
	// is read.
	if m := agentMarker(os.Getenv); m != "" {
		return userErr("fugaro stats runs reads one installation's run records, so it is refused in a coding agent's session (%s is set): run it in your own terminal window", m)
	}
	if strings.TrimSpace(o.cloud.project) == "" {
		return userErr("--project: name the installation to aggregate (this command never guesses one, the sandbox included)")
	}
	if o.minRuns < 1 {
		return userErr("--min-runs %d: use at least 1", o.minRuns)
	}
	if o.minRuns < stats.MinRuns {
		// Lowering the threshold is the operator's call (D97), and the figures
		// are printed as asked for -- but a range from so few runs is one run's
		// own figures with a range drawn around them, so the command says so
		// rather than letting it read as a distribution.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: --min-runs %d is below the %d a range is meant to be read with: a range from so few runs is mostly one run's own figures, and every figure prints its sample size\n",
			o.minRuns, stats.MinRuns)
	}
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	until := time.Now().UTC()
	var from time.Time
	if since > 0 {
		from = until.Add(-since)
	}
	runs, err := readStatsRuns(ctx, env, from)
	if err != nil {
		return err
	}
	res := stats.Aggregate(runs, o.minRuns)
	return writeStats(cmd.OutOrStdout(), env.lc.Name, from, until, o.since, o.minRuns, res, o.asJSON)
}

// readStatsRuns reads every run record in the window and maps each to the
// narrow view internal/stats aggregates. The window is applied to the run IDs
// the bucket lists, exactly as runs ls --since does, and only result.json is
// read: a run whose task.json or transcript would tell a reader more is read
// no further.
//
// A record that cannot be used is one run's problem, not the window's, exactly
// as runs ls and budget preview treat it: a run that has not finalized has no
// record, and a corrupt or oversized one is that run's own (corruptObject).
// Both are counted as a record with no figure and the figures are still
// published -- one bad object must not make them unproducible.
//
// A read failure that is neither -- permission denied, say -- fails the whole
// command, and the failure says only that a record could not be read, with its
// place in the window. The run it was, the object it was, and the error under
// it are identifiers of an installation's own data, and this command's whole
// value is that none of them is printed.
func readStatsRuns(ctx context.Context, env *cloudEnv, since time.Time) ([]stats.Run, error) {
	slugs, err := runstore.ListSlugs(ctx, env.bucket.Bucket)
	if err != nil {
		return nil, remote(errors.New("the runs bucket could not be listed"))
	}
	var refs []statsRef
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, since)
		if err != nil {
			// dirs' own error names the prefix, and the prefix names the
			// repository: this one is not printed either.
			return nil, remote(errors.New("a repository's runs could not be listed"))
		}
		for _, id := range ids {
			refs = append(refs, statsRef{slug: slug, id: id})
		}
	}
	// One window can hold every run of every repository in the bucket, so the
	// records are read on the worker pool runs ls and budget preview share.
	recs := make([]*runstore.Record, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		recs[i], errs[i] = runstore.Open(env.bucket.Bucket, refs[i].slug, refs[i].id).ReadRecord(ctx)
	})
	runs := make([]stats.Run, 0, len(refs))
	for i := range refs {
		switch err := errs[i]; {
		case err == nil:
			runs = append(runs, statsRun(recs[i]))
		case errors.Is(err, runstore.ErrNotFound), corruptObject(err):
			runs = append(runs, stats.Run{NoFigure: true})
		default:
			return nil, remote(fmt.Errorf("a run record could not be read (%d of %d)", i+1, len(refs)))
		}
	}
	return runs, nil
}

// statsRef is one run to read: the bucket directory it sits in, and its ID.
type statsRef struct{ slug, id string }

// statsRun is the narrow view of one record: a recipe and where it came from,
// a duration, a cost and whether the compute part is an estimate. Nothing that
// identifies the run, its repository or its pull request is carried across, so
// no field of the published aggregate can hold one -- this function is where
// the record's repository, run ID, branch, pull request and account are left
// behind.
func statsRun(rec *runstore.Record) stats.Run {
	r := stats.Run{NoFigure: true}
	// The recipe is the group key, and it is read from the record's recipe
	// rather than from anything that names the run. A record from before
	// 0.5.0 has none, and internal/stats groups only a catalog recipe: an
	// empty name, and a repository's own recipe that reuses a catalog name,
	// are both dropped as unknown.
	if rec.Recipe != nil {
		r.Recipe, r.RecipeSource = rec.Recipe.Name, rec.Recipe.Source
	}
	if rec.StartedAt.IsZero() || rec.FinishedAt == nil || rec.Cost == nil {
		return r
	}
	d := rec.FinishedAt.Sub(rec.StartedAt)
	// A figure that cannot be one is no figure: a duration that ran backwards,
	// a negative cost, or a value past any cost. Two of them in one window are
	// enough to make the interpolation produce an infinity, and --json then
	// fails outright rather than printing a range.
	if d < 0 || !statsFigure(d.Seconds()) || !statsFigure(rec.Cost.TotalUSD) {
		return r
	}
	r.NoFigure = false
	r.DurationS = d.Seconds()
	r.CostUSD = rec.Cost.TotalUSD
	r.ComputeNotEstimated = !rec.Cost.ComputeEstimated
	return r
}

// statsMaxFigure is the largest figure this command can publish. Rounding a
// cost to the cent multiplies it by 100 on the way, so a larger one -- 1e308
// is a value a bad record can carry -- would round to an infinity and --json
// would fail rather than print a range.
const statsMaxFigure = math.MaxFloat64 / 100

// statsFigure reports whether v can be one of the figures published: a cost or
// a duration is never negative, never infinite, never NaN, and never so large
// that publishing it rounds to an infinity.
func statsFigure(v float64) bool {
	return v >= 0 && v <= statsMaxFigure && !math.IsInf(v, 0) && !math.IsNaN(v)
}

// writeStats prints the figures as a table with their disclaimer, or as one
// JSON document. sinceLabel is --since as the operator wrote it, so the window
// reads back as asked for rather than as a duration's own spelling.
func writeStats(w io.Writer, project string, from, until time.Time, sinceLabel string, minRuns int, res stats.Result, asJSON bool) error {
	doc := statsDoc{
		Project: project,
		Since:   from.Format("2006-01-02"),
		Until:   until.Format("2006-01-02"),
		MinRuns: minRuns,
		Recipes: []statsRecipeDoc{},
		Totals: statsTotalsDoc{
			Runs:               res.Runs,
			Recipes:            len(res.Groups),
			RunsBelowMinRuns:   res.RunsBelowMinRuns,
			RunsUnknownRecipe:  res.RunsUnknownRecipe,
			RunsNotEstimated:   res.ComputeNotEstimated,
			RunsWithoutAFigure: res.RunsWithoutFigure,
		},
		NotAPromise: statsNotAPromise,
	}
	if from.IsZero() {
		doc.Since = ""
	}
	for _, g := range res.Groups {
		doc.Recipes = append(doc.Recipes, statsRecipeDoc{
			Recipe:              g.Recipe,
			Runs:                g.Runs,
			ComputeNotEstimated: g.ComputeNotEstimated,
			DurationS:           g.DurationS,
			CostUSD:             g.CostUSD,
		})
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}

	fmt.Fprintf(w, "project: %s\n", project)
	window := "the whole bucket"
	if !from.IsZero() {
		window = fmt.Sprintf("%s to %s (%s)", doc.Since, doc.Until, sinceLabel)
	}
	fmt.Fprintf(w, "window:  %s, at least %d %s per recipe\n\n", window, minRuns, plural(minRuns, "run", "runs"))
	if len(res.Groups) == 0 {
		fmt.Fprintf(w, "no recipe reached %d %s in this window: no range is published\n", minRuns, plural(minRuns, "run", "runs"))
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RECIPE\tRUNS\tDURATION S (min median p90)\tCOST USD (min median p90)\tCOMPUTE NOT ESTIMATED")
		for _, g := range res.Groups {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\n", oneLine(g.Recipe), g.Runs,
				statsTriple(g.DurationS, "%.0f"), statsTriple(g.CostUSD, "%.2f"), g.ComputeNotEstimated)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "\n%d runs with a figure in %d %s: %d in recipes below %d runs, %d whose recipe this binary does not ship, %d without a figure\n",
		res.Runs, len(res.Groups), plural(len(res.Groups), "recipe", "recipes"),
		res.RunsBelowMinRuns, minRuns, res.RunsUnknownRecipe, res.RunsWithoutFigure)
	_, err := fmt.Fprintln(w, "\n"+statsNotAPromise)
	return err
}

// statsTriple prints one measure's three figures.
func statsTriple(p stats.Percentiles, format string) string {
	return fmt.Sprintf(format+" "+format+" "+format, p.Min, p.Median, p.P90)
}
