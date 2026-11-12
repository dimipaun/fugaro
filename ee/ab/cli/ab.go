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
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
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
			"this command scores the pairs from those records and from what the repository's\n" +
			"judge runs wrote under their own prefixes: no model call is made here, and the\n" +
			"same records and verdicts always give the same table. A pair at Critical counts\n" +
			"only once a judge has decided it (D561), and every other mode shows the judge's\n" +
			"verdict beside the hard signals it is ranked on.\n\n" +
			"Rows are one per rigor mode x tier x model pair. A pair the arms do not make --\n" +
			"a different start commit, mode, tier or recipe, or an arm that never started --\n" +
			"is in no figure at all. WINS and LOSSES are the challenger's; a TIE decided\n" +
			"nothing and no figure is drawn from one. HALTS is each arm's halt count: a halt\n" +
			"is already a loss, and the count says how often a limit rather than the work\n" +
			"decided a pair. COST is the median pair cost ratio, the challenger's model\n" +
			"dollars over the champion's. JUDGE FAMILY is the family of the model that judged\n" +
			"the group's pairs -- a Claude ID is anthropic, a provider model's family is its\n" +
			"ID's vendor prefix -- and SAME FAMILY says one of them is an arm's own, which\n" +
			"blindness does not remove (D553). UNJUDGED counts the pairs no judge has decided\n" +
			"and TRUNCATED those whose patch was cut short and can never be judged.\n\n" +
			"ADVICE is D565's rule at the mode's exchange rate: the challenger is recommended\n" +
			"when it is clearly better (the lower bound of its win share above a half) and its\n" +
			"median cost ratio is within the mode's rate, or when it is not worse (the upper\n" +
			"bound of its loss share below 0.6) and cheaper; otherwise the champion stays. A\n" +
			fmt.Sprintf("row with fewer than %d decisive pairs says no conclusion rather than\n", ab.MinRuns) +
			"approximating one. Nothing here changes a default: this is a report, not a\n" +
			"switch.\n\n" +
			"No person, repository or task text reaches the output: a row is a mode, a tier and\n" +
			"two coder models, with the family of the model that judged them.\n\n" +
			"--since is how far back the read goes (default 30d: a day count, a Go duration,\n" +
			"or 0 for no bound). --json prints {project, source, since, until, rows[{mode,\n" +
			"tier, champion, challenger, pairs, wins, losses, ties, champion_halts,\n" +
			"challenger_halts, champion_follow_ups, judge_families, judge_same_family,\n" +
			"unjudged, truncated, recommendation{decision, rule, decisive, p, win_share_lo,\n" +
			"win_share_hi, median_cost_ratio, exchange_rate}}], warnings};\n" +
			"median_cost_ratio is null when no pair of the group carried both arms' costs,\n" +
			"and judge_families is empty when no judge has decided a pair of the group.",
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
	// The window as the report shows it and as the read bounds it: --since
	// measured back from now, and no end, since --since is the whole window
	// this command has.
	since := fcli.LsSince(window)
	m := report.ABMeta{Project: lc.Name, Source: "run-records"}
	if !since.IsZero() {
		m.Since = since.UTC().Format(time.RFC3339)
	}
	m.Until = fcli.LsNow().UTC().Format(time.RFC3339)
	rates, rateNote, err := abRateSource(ctx, lc)
	if err != nil {
		return err
	}
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
	objs, _, truncated, err := env.ReadRunRecords(ctx, slugs, since, time.Time{}, d.MaxRuns)
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
	verdicts, unread := readVerdicts(ctx, env, slugs, objs)
	if unread > 0 {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%d of the window's judge runs have a verdicts object this read could not open: the pairs they decided count as unjudged here, so a Critical pair among them is in no figure", unread))
	}
	rep := report.BuildAB(recs, verdicts, rates)
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

// readVerdicts is what the window's judge runs have decided, keyed by pair ID
// (ab.JudgeVerdicts), and how many of their objects this read could not open.
//
// The verdicts are read from where the judge runs wrote them and nowhere else
// (D559): a judge run is one of the records this read already holds, so finding
// one costs no listing, and its own prefix is the only place its object lives.
// The record does not say which repository prefix that is — a read across
// repositories holds records and their slugs apart — so the slug is derived
// from the record's repository and the slugs the read covered, the way the run
// itself derives it (runner.ProviderForSlug). Its run ID is the listing's own
// and never the record's: the record's is text out of result.json, and it
// becomes part of an object name (runstore.Open), so a record declaring another
// run's ID would open another prefix's object — where the listing's ID is the
// directory the bucket actually holds the run under.
//
// A judge run whose object is there and cannot be opened or believed is counted
// rather than fatal: the figures it would have decided are read as unjudged,
// which the report says out loud, and one bad object never costs a reader the
// whole table — the reading ab's own launcher makes is the opposite one, since
// judging the same pairs again would write a second verdict for each. An object
// that is simply absent is not counted: a judge run that wrote none decided
// nothing (JudgeVerdicts).
func readVerdicts(ctx context.Context, cloud *fcli.Cloud, slugs []string, objs []fcli.RunRecord) (map[string]ab.Verdict, int) {
	verdicts := map[string]ab.Verdict{}
	unread := 0
	for _, o := range objs {
		rec := o.Record()
		if rec == nil || !ab.IsJudgeRun(rec) {
			continue
		}
		slug := slugOfRecord(*rec, slugs)
		if slug == "" {
			unread++
			continue
		}
		got, err := ab.JudgeVerdicts(ctx, cloud.RunsBucket(), slug, o.ID())
		if err != nil {
			unread++
			continue
		}
		// Two objects can name one pair: A5.2 hands a pair whose judge call
		// failed to the next run (skipCallFailed), so a later run's answer
		// stands beside an earlier run's skip. A judgement therefore wins over a
		// skip whatever order the records were read in, and a second judgement —
		// which no launcher here makes (D569) — leaves the first standing
		// rather than making a figure depend on the listing's order.
		for pairID, v := range got {
			seen, ok := verdicts[pairID]
			if !ok || (!seen.Judged() && v.Judged()) {
				verdicts[pairID] = v
			}
		}
	}
	return verdicts, unread
}

// slugOfRecord is the storage slug of the repository a record ran in: the one
// slug of the read that names that repository, "" for a repository none of them
// does. A slug is a hash of the provider kind and the repository, so exactly
// one of a read's slugs can match a given record, and a record whose repository
// no slug names is one whose judge runs this read cannot find.
func slugOfRecord(rec runstore.Record, slugs []string) string {
	for _, slug := range slugs {
		if _, ok := runner.ProviderForSlug(slug, rec.Repo); ok {
			return slug
		}
	}
	return ""
}

// abRateSource is the exchange rate table the recommendation is read at (D565)
// and the line saying where it came from, empty when the project's own layer
// supplied it.
//
// The table is the project layer's ab.exchange_rate, with every mode it leaves
// out -- and every mode of a project that has published no layer -- filled in
// from the figure Fugaro ships.
//
// The layer is read the way a launch reads it (fcli.ProjectLayerOf: the
// bucket's object, with a cached copy standing in only when the bucket cannot
// be reached at all and that copy is inside the offline limit). Reading the
// bucket rather than the cache file is what makes a layer published after this
// machine last read one decide the ADVICE column: the cache holds whatever the
// last read of the layer left there, however old, and a project that raised its
// ab.exchange_rate would otherwise keep being judged at the old one.
//
// The note keeps the advice honest where the layer is not the bucket's own
// object -- a cached copy and its age, or why no layer applies -- instead of
// silently reading the advice at another project's rates.
func abRateSource(ctx context.Context, lc *localcfg.Config) (map[rigor.Mode]float64, string, error) {
	layer, note, err := fcli.ProjectLayerOf(ctx, lc)
	if err != nil {
		return nil, "", err
	}
	switch {
	case layer == nil && note == "":
		return nil, "this project has published no project layer, so every mode's exchange rate is the figure Fugaro ships", nil
	case layer == nil:
		return nil, note + "; with no layer of its own, every mode's exchange rate is the figure Fugaro ships", nil
	case note != "":
		return layer.AB.ExchangeRates(), note + " (the advice and the exchange rates below are that copy's)", nil
	}
	return layer.AB.ExchangeRates(), "", nil
}
