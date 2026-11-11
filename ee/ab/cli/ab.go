// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// This package is `fugaro ab`, the model A/B results surface (D570).
//
// It sits beside the report rather than inside ee/ab because it prints what
// ee/report/ab.go builds from ee/ab's pairs and statistics: a command in
// package ab would have to import ee/report to reach its own rows, and Go
// refuses that cycle. The layout is the one the ee moves use for a feature's
// CLI half (ee/coordinator/cli, ee/report/cli).
package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/ab"
	report "github.com/dimipaun/fugaro/ee/report"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// abDefaultSince is the window --since defaults to. It is the same 30 days
// `fugaro report` reads its own groupings over, so a reader comparing the two
// surfaces is looking at one span. Sampling is off until the owner sets
// ab.rate, and a pair is a rarer event than a run, so a shorter default would
// answer nothing on most projects.
const abDefaultSince = "30d"

// Deps is the core seam this command bounds its read with, reaching it through
// the constructor rather than a package-level variable the core would have to
// publish a setter for (only ee/register may set a seam). NewABCmd supplies the
// core's own cap; a test drives it here.
type Deps struct {
	// MaxRuns bounds the read. A window holding more runs is read to its newest
	// MaxRuns, and the report says so.
	MaxRuns int
}

// NewABCmd is fugaro ab, built with the core's run cap.
func NewABCmd() *cobra.Command { return newABCmd(Deps{MaxRuns: fcli.UserStatsMaxRuns}) }

type abOptions struct {
	cloud       fcli.CloudFlags
	repo, since string
	json        bool
}

func newABCmd(d Deps) *cobra.Command {
	var o abOptions
	cmd := &cobra.Command{
		Use:   "ab",
		Short: "Report model A/B results: pairs, wins, cost and the recommendation per group",
		Long: "Report model A/B results from the runs the launcher sampled.\n\n" +
			"A sampled task launches a champion (the run the developer sees) and a\n" +
			"challenger (the same task, at the same commit, on another coder model, run as a\n" +
			"shadow that pushes nothing and opens nothing). Both records carry the pair, and\n" +
			"this command scores the pairs from those records alone: no model call, no\n" +
			"judgement, and the same records always give the same table.\n\n" +
			"Rows are one per rigor mode x tier x model pair. A pair the arms do not make --\n" +
			"a different start commit, mode, tier or recipe, or an arm that never started --\n" +
			"is in no figure at all. WINS and LOSSES are the challenger's; a TIE decided\n" +
			"nothing and no figure is drawn from one. HALTS is each arm's halt count: a halt\n" +
			"is already a loss, and the count says how often a limit rather than the work\n" +
			"decided a pair. COST is the median pair cost ratio, the challenger's model\n" +
			"dollars over the champion's.\n\n" +
			"ADVICE is D565's rule at the mode's exchange rate: the challenger is recommended\n" +
			"when it is clearly better (the lower bound of its win share above a half) and its\n" +
			"median cost ratio is within the mode's rate, or when it is not worse (the upper\n" +
			"bound of its loss share below 0.6) and cheaper; otherwise the champion stays. A\n" +
			fmt.Sprintf("row with fewer than %d decisive pairs says no conclusion rather than\n", ab.MinRuns) +
			"approximating one. Nothing here changes a default: this is a report, not a\n" +
			"switch.\n\n" +
			"No person, repository or task text reaches the output: a row is a mode, a tier and\n" +
			"two coder models.\n\n" +
			"--since is how far back the read goes (default 30d: a day count, a Go duration,\n" +
			"or 0 for no bound). --json prints {project, source, since, until, rows[{mode,\n" +
			"tier, champion, challenger, pairs, wins, losses, ties, champion_halts,\n" +
			"challenger_halts, champion_follow_ups, recommendation{decision, rule, decisive,\n" +
			"p, win_share_lo, win_share_hi, median_cost_ratio, exchange_rate}}], warnings};\n" +
			"median_cost_ratio is null when no pair of the group carried both arms' costs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runAB(cmd, &o, d) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.StringVar(&o.since, "since", abDefaultSince, "how far back to read: Nd, a Go duration, or 0 for no bound")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runAB(cmd *cobra.Command, o *abOptions, d Deps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	window, err := fcli.ParseSince(o.since)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	if err := fcli.RefuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	cloud, err := fcli.SelectProject(ctx, &o.cloud) // the project header goes to stderr
	if err != nil {
		return err
	}
	lc := cloud.Config()
	m := report.ABMeta{Project: lc.Name, Source: "run-records"}
	// The window's ends as instants, and the later end left open: --since is
	// the whole window this command has, so a pair launched after the read
	// began is in the next report rather than half in this one.
	if since := fcli.LsSince(window); !since.IsZero() {
		m.Since = since.UTC().Format(time.RFC3339)
	}
	m.Until = fcli.LsNow().UTC().Format(time.RFC3339)
	rates, rateNote := abRateSource(lc.Name)
	if rateNote != "" {
		m.Warnings = append(m.Warnings, rateNote)
	}
	env, err := fcli.OpenFromConfig(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	// Every repository the runs bucket holds, not only the ones the local
	// config lists: a pair's two runs are in one repository's prefix, and the
	// project's whole sample is what a recommendation is read over. --repo
	// still names one.
	slugs, err := env.LsSlugs(ctx, o.repo, true, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	objs, _, truncated, err := env.ReadRunRecords(ctx, slugs, fcli.LsSince(window), time.Time{}, d.MaxRuns)
	if err != nil {
		return err
	}
	if truncated {
		m.Warnings = append(m.Warnings, fmt.Sprintf("the window holds more runs than the %d this read opens: only its newest %d are counted, so a pair outside them is in no row", d.MaxRuns, d.MaxRuns))
	}
	recs, corrupt := abRecords(objs)
	if corrupt > 0 {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%d of the window's runs have a result.json this build cannot read: an arm without a record is in no pair, so the pair it belongs to is in no figure", corrupt))
	}
	rep := report.BuildAB(recs, rates)
	if o.json {
		return report.ABJSON(cmd.OutOrStdout(), rep, m)
	}
	return report.ABTable(cmd.OutOrStdout(), rep, m)
}

// abRecords is the pair read's input: every run of the window whose record this
// build can read. A run that has written no record yet -- one still in flight --
// is no arm of a pair and warns nothing, while a record the bucket holds and
// this read cannot use is counted for the warning: the pair it belongs to has
// an arm R2 refuses, and a reader has to know the sample is short by it.
func abRecords(objs []fcli.RunRecord) ([]runstore.Record, int) {
	recs := make([]runstore.Record, 0, len(objs))
	corrupt := 0
	for _, o := range objs {
		rec := o.Record()
		if rec == nil {
			if o.RecordCorrupt() {
				corrupt++
			}
			continue
		}
		recs = append(recs, *rec)
	}
	return recs, corrupt
}

// abRateSource is the exchange rate table the recommendation is read at (D565)
// and the line saying where it came from, empty when the project's own layer
// supplied it.
//
// The table is the project layer's ab.exchange_rate, with every mode it leaves
// out -- and every mode of a machine that has read no layer -- filled in from
// the figure Fugaro ships. A report never fetches the layer itself, so the copy
// the last read of it cached on this machine is what a read-only surface can
// reach; a machine that has read none is the ordinary case on a fresh checkout
// rather than a fault, and the note is what keeps the advice honest about it
// instead of silently reporting another project's rates.
func abRateSource(project string) (map[rigor.Mode]float64, string) {
	entry, ok := localcfg.LoadLayerCache(os.Getenv, project)
	if !ok {
		return nil, "no project layer is cached on this machine, so every mode's exchange rate is the figure Fugaro ships (run a command that reads the layer, such as `fugaro models ls`, to report the project's own ab.exchange_rate)"
	}
	layer, problems := config.ParseProjectLayer([]byte(entry.YAML), config.LayerAnchor{Project: project, GCPProject: entry.GCPProject})
	if layer == nil || len(problems) > 0 {
		return nil, "the project layer cached on this machine could not be read, so every mode's exchange rate is the figure Fugaro ships"
	}
	return layer.AB.ExchangeRates(), ""
}
