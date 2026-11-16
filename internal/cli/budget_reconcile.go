package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro budget reconcile (spend-accuracy SA1.3, D2647): one window of one
// installation's own run records read three ways and put side by side -- what
// the runs were CHARGED (result.json's model_by: the price table's rates plus
// the route fees), what the providers SAID those calls cost (reported_usd, and
// reported_by where the record splits it by serving model), and the BAND the
// price table would have put the reported calls in (table_lo_usd to
// table_hi_usd, SA1.6). Per model, so a charge can be read against the report
// it is about.
//
// It is a reading and nothing else: it writes no counter, moves no cap, asks
// nothing, and reads only the runs bucket through the reader `fugaro report`
// uses (api.go's Cloud.LsSlugs, Cloud.ReadRunRecords and ApplyCost), so the
// two commands agree about a window by construction rather than by two
// implementations of one rule.
//
// Four rules shape what a row says, and each is written down because a record
// cannot always answer the question a reader is asking:
//
//   - The charge is model_by's own split, in whole: a subscription's notional
//     part is inside it, exactly as the record keeps it (P12, D140 leaves the
//     by-model split of a mixed run untellable). The totals say what part of
//     the window's charge was notional, so a reader putting the figure beside
//     an invoice is not left comparing a bill against a plan's usage.
//   - The report is reported_by where the record carries it, which is per
//     serving model (F7). A record written before that field existed carries
//     only the run's reported_usd, which is one figure for the run: it lands
//     on the row of the model the run charged on when there is exactly one,
//     and under `mixed` when the run's charge names several, because no row of
//     several may carry a figure that is all of them.
//   - The band is the run's own figure over the calls its provider reported a
//     cost for, and it travels with the report: one key carries it, or
//     `mixed` does. It is never split between rows -- the record keeps it
//     whole, and dividing it would be this command inventing a per-model band.
//   - `unattributed` -- the charge for calls no stage report carried, which is
//     model_by's own key for them -- is a row of its own and is never folded
//     into a model's.
const (
	// budgetReconcileUnattributed is model_by's key for spend no stage report
	// carried (internal/runner's unattributedModel): a call that settled
	// after its stage's wait ran out.
	budgetReconcileUnattributed = "unattributed"
	// budgetReconcileMixed is the row for a figure the record keeps whole for
	// the run rather than for one model: an old record's reported_usd and its
	// band when the run's charge names several models, and the band of a run
	// whose report names several. It is a bucket of the reading and never a
	// model, so it is named apart from every model_by key.
	budgetReconcileMixed = "mixed"
)

// budgetReconcileMaxRuns is how many runs one reading opens: the same bound
// runs stats reads a window by (UserStatsMaxRuns), and a variable so that a
// test can drive the truncation the output has to own without seeding five
// thousand runs.
var budgetReconcileMaxRuns = UserStatsMaxRuns

type budgetReconcileOptions struct {
	cloud       cloudOptions
	from, to    string
	repo        string
	all, asJSON bool
}

func newBudgetReconcileCmd() *cobra.Command {
	var o budgetReconcileOptions
	cmd := &cobra.Command{
		Use:   "reconcile --from YYYY-MM-DD --to YYYY-MM-DD (--repo owner/name | --all)",
		Short: "Put the runs' charged, reported and table spend side by side, per model",
		Long: "Read one window of the project's run records and print, per model, what the\n" +
			"runs were charged, what the providers reported their calls cost, and the band\n" +
			"the price table would have put those calls in.\n\n" +
			"--from and --to are UTC dates (YYYY-MM-DD) and both are inclusive; the window\n" +
			"is the one runs ls and report read, so a run is in it on the day the bucket's\n" +
			"run ID places its start. --repo names one repository and --all every\n" +
			"repository of the runs bucket; one of the two is required, because a reading\n" +
			"that silently covered less, or more, than the reader meant is how a\n" +
			"reconciliation comes out plausible and wrong.\n\n" +
			"The figures are the run records' own, and nothing is priced here:\n\n" +
			"  CHARGE is the run's model_by, the price table's rates plus the route fees,\n" +
			"  as the runs recorded it, split by serving model. A subscription's notional\n" +
			"  part is inside it; the totals say how much, and no provider bills that part.\n" +
			"  REPORT is what the providers said their calls cost (their own figures, no\n" +
			"  route fee in them), and WITH REPORT is how many of the row's runs carried\n" +
			"  one. A record written before reported_by existed carries one figure for the\n" +
			"  whole run: it lands on the model's row when the run charged on exactly one\n" +
			"  model, and under `mixed` when it charged on several.\n" +
			"  BAND is what the price table would have charged the calls a provider\n" +
			"  reported a cost for, from the cheapest end of the selected tier to the\n" +
			"  dearest. It is the run's own figure and is never split between models: a\n" +
			"  run whose report names several models carries its band under `mixed`.\n" +
			"  TOKENS are the run's counts by model (model_tokens), with the cache reads\n" +
			"  and writes apart. A zero is a count: it is what the record says, and a\n" +
			"  record written before the field existed has none.\n" +
			"  TIERS are the service tiers the run's calls came back at that the price\n" +
			"  table has no multiplier for, which explain a dearer figure; the record\n" +
			"  keeps them per run, so a run that touched two models lists them on both.\n\n" +
			"`unattributed` is a row of its own: it is the charge for calls no stage\n" +
			"report carried, which model_by keys apart from the models, and it is never\n" +
			"folded into one. `mixed` holds what a run recorded for itself rather than for\n" +
			"one of its models.\n\n" +
			"The totals are printed with the route fee and without it: the fee is a\n" +
			"percentage the project config adds to every charge on a provider route\n" +
			"(route_fee_pct), the providers report the model's own price and no fee, so the\n" +
			"report is read against the charge without it. The window's runs that carried\n" +
			"no usable record, and the settled calls no provider reported a cost for, are\n" +
			"counted and printed: they are spend the totals are short by, and a reader is\n" +
			"told rather than left to take the figures for the window's whole.\n\n" +
			"The read is bounded to the newest 5000 runs of the window, newest first; a\n" +
			"window holding more says so, in the output and in the --json document's\n" +
			"truncated. It reads only: no counter, cap or kill switch is read or written,\n" +
			"and the budget database is not reached at all.\n\n" +
			"--json prints {project, from, to, repo, all, max_runs, truncated, models[],\n" +
			"totals, warnings}; a model row carries {model, charge_usd, report_usd,\n" +
			"band_lo_usd, band_hi_usd, input_tokens, output_tokens, cache_read_tokens,\n" +
			"cache_write_tokens, runs, runs_with_report, service_tiers}, and totals carries\n" +
			"the window's own counts and sums, charge_without_fee_usd among them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runBudgetReconcile(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.from, "from", "", "first day of the window, inclusive (YYYY-MM-DD, UTC)")
	f.StringVar(&o.to, "to", "", "last day of the window, inclusive (YYYY-MM-DD, UTC)")
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name, or a path inside its checkout)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	for _, name := range []string{"from", "to"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

// budgetReconcileModel is one row of the reading: one model's charge, report,
// band, tokens and service tiers over the window.
type budgetReconcileModel struct {
	Model            string  `json:"model"`
	ChargeUSD        float64 `json:"charge_usd"`
	ReportUSD        float64 `json:"report_usd"`
	BandLoUSD        float64 `json:"band_lo_usd"`
	BandHiUSD        float64 `json:"band_hi_usd"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	// Runs is the runs that touched this row -- charged on it, reported on
	// it, or recorded tokens for it -- and RunsWithReport the runs whose
	// provider report landed on it.
	Runs           int      `json:"runs"`
	RunsWithReport int      `json:"runs_with_report"`
	ServiceTiers   []string `json:"service_tiers,omitempty"`
}

// budgetReconcileTotals is the window's own figures, over every row.
type budgetReconcileTotals struct {
	// Runs is the runs that carried a cost breakdown; NoRecord the runs whose
	// result.json is not there (a run still going, or one that died before it
	// wrote one), RecordCorrupt the runs whose result.json is there and this
	// read could not use, NoCost the runs whose record carries no cost
	// breakdown at all, and Unlaunched the runs that never started.
	Runs          int `json:"runs"`
	NoRecord      int `json:"no_record"`
	RecordCorrupt int `json:"record_corrupt"`
	NoCost        int `json:"no_cost"`
	Unlaunched    int `json:"unlaunched"`
	// RunsWithReport is how many of Runs carried a provider report.
	RunsWithReport int `json:"runs_with_report"`
	// ChargeUSD is the rows' charge, the window's model figures as recorded;
	// ChargeWithoutFeeUSD is the same with the route fee taken back out, which
	// is what the providers themselves charged; NotionalUSD is the part of
	// ChargeUSD a subscription paid, which no provider bills.
	ChargeUSD           float64 `json:"charge_usd"`
	ChargeWithoutFeeUSD float64 `json:"charge_without_fee_usd"`
	NotionalUSD         float64 `json:"notional_usd"`
	// ReportUSD is what the providers said the window's calls cost, BandLoUSD
	// and BandHiUSD the table's band over the calls they reported a cost for,
	// and UnreportedCalls the window's settled calls whose response carried
	// no provider cost: how much of the band, and of the report, is actually
	// over.
	ReportUSD        float64 `json:"report_usd"`
	BandLoUSD        float64 `json:"band_lo_usd"`
	BandHiUSD        float64 `json:"band_hi_usd"`
	UnreportedCalls  int     `json:"unreported_calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
}

// budgetReconcileDoc is `budget reconcile --json`'s document: the window, the
// rows and the totals, so a reader can put the figures under a script without
// reading the table.
type budgetReconcileDoc struct {
	Project string `json:"project"`
	From    string `json:"from"`
	To      string `json:"to"`
	// Repo is the one repository --repo named, "" with --all.
	Repo string `json:"repo,omitempty"`
	All  bool   `json:"all"`
	// MaxRuns and Truncated say how much of the window the figures are over:
	// Truncated means the window held more runs than MaxRuns, and that only
	// its newest MaxRuns were read.
	MaxRuns   int                    `json:"max_runs"`
	Truncated bool                   `json:"truncated"`
	Models    []budgetReconcileModel `json:"models"`
	Totals    budgetReconcileTotals  `json:"totals"`
	// Warnings are what the read could not use: the run bound, a record the
	// bucket holds and this read could not take apart.
	Warnings []string `json:"warnings,omitempty"`
}

// reconcileRun is one run as the reading takes it apart: where its report and
// its band land, and the route fee its charges carry.
type reconcileRun struct {
	cost *runstore.Cost
	// reportBy is the run's report per row, and bandRow the one row that
	// carries the run's band ("" when it has none). They are what the rules
	// above decide, worked out once so that the rows and the totals cannot
	// disagree about where a figure went.
	reportBy map[string]budget.Micros
	bandRow  string
	// reported is whether the run carried a provider report at all: a band
	// with no report is a record this reading shows rather than a run of the
	// share below.
	reported bool
	// feePaid is the route fee inside the run's charges: what taking the
	// providers' own price back out of them removes.
	feePaid budget.Micros
}

func runBudgetReconcile(cmd *cobra.Command, o *budgetReconcileOptions) error {
	if o.repo != "" && o.all {
		return userErr("--repo and --all don't go together")
	}
	if o.repo == "" && !o.all {
		return userErr("name the window's repositories: --repo owner/name for one, or --all for every repository in the runs bucket")
	}
	from, to, err := reconcileWindow(o.from, o.to)
	if err != nil {
		return err
	}
	if o.repo, err = repoArg(cmd.Context(), o.cloud, o.repo); err != nil {
		return err
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	cloud := &Cloud{env: env}
	slugs, err := cloud.LsSlugs(ctx, o.repo, o.all, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The window's end as an instant, so a run launched after --to is dropped
	// by the reader rather than taking a place the bound keeps for a run of
	// the window.
	until := time.UnixMilli((to + 1) * 86_400_000).UTC()
	objs, unlaunched, truncated, err := cloud.ReadRunRecords(ctx, slugs, time.UnixMilli(from*86_400_000).UTC(), until, budgetReconcileMaxRuns)
	if err != nil {
		return err
	}

	doc := budgetReconcileDoc{
		Project: env.lc.Name, From: o.from, To: o.to, Repo: o.repo, All: o.all,
		MaxRuns: budgetReconcileMaxRuns, Truncated: truncated,
	}
	if truncated {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("the window holds more runs than the %d this reading opens: only its newest %d are counted, so a run outside them is in no row", budgetReconcileMaxRuns, budgetReconcileMaxRuns))
	}
	runs := make([]reconcileRun, 0, len(objs))
	for _, obj := range objs {
		rec := obj.Record()
		switch {
		case obj.RecordCorrupt():
			doc.Totals.RecordCorrupt++
		case rec == nil:
			doc.Totals.NoRecord++
		case rec.Cost == nil:
			doc.Totals.NoCost++
		default:
			runs = append(runs, reconcileRead(rec.Cost, env.lc))
		}
	}
	if doc.Totals.RecordCorrupt > 0 {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("%d of the window's runs hold a result.json this reading could not use: their spend is in no row, so the totals are short by it", doc.Totals.RecordCorrupt))
	}
	doc.Models, doc.Totals = reconcileRows(runs, doc.Totals)
	doc.Totals.Unlaunched = unlaunched
	return writeReconcile(cmd.OutOrStdout(), cmd.ErrOrStderr(), doc, o.asJSON)
}

// reconcileWindow reads --from and --to as UTC days, refusing a window that
// ends before it starts rather than answering about an empty one.
func reconcileWindow(from, to string) (fromDay, toDay int64, err error) {
	parse := func(name, value string) (int64, error) {
		t, err := time.Parse("2006-01-02", value)
		if err != nil {
			return 0, userErr("--%s %q is not a date (YYYY-MM-DD, UTC)", name, safetext.Strip(value))
		}
		return budget.Day(t), nil
	}
	if fromDay, err = parse("from", from); err != nil {
		return 0, 0, err
	}
	if toDay, err = parse("to", to); err != nil {
		return 0, 0, err
	}
	if toDay < fromDay {
		return 0, 0, userErr("--to %s is before --from %s: the window is empty", budget.DayDate(toDay), budget.DayDate(fromDay))
	}
	return fromDay, toDay, nil
}

// reconcileRead takes one run's cost apart: where its report and its band land,
// and what route fee its charges carry.
func reconcileRead(c *runstore.Cost, lc *localcfg.Config) reconcileRun {
	r := reconcileRun{cost: c}
	// The fee is the providers' routes' own: route_by's "unattributed" key is
	// spend no stage report carried, which may be a Claude call's and carries
	// no fee this reading could know, so it is left out (diagnose routeOf
	// reads the same key the same way).
	for route, usd := range c.RouteBy {
		if route == budgetReconcileUnattributed {
			continue
		}
		m := microsOfUSD(usd)
		r.feePaid += m - pricing.WithoutFee(m, lc.Providers[route].RouteFeePct)
	}
	r.reportBy, r.bandRow = reconcileReport(c)
	r.reported = len(c.ReportedBy) > 0 || c.ReportedUSD > 0
	return r
}

// reconcileReport decides the row or rows a run's report and band are read on:
// reported_by where the record carries it, else the run's whole reported_usd on
// the model it charged on when there is exactly one.
func reconcileReport(c *runstore.Cost) (reportBy map[string]budget.Micros, bandRow string) {
	if len(c.ReportedBy) > 0 {
		reportBy = make(map[string]budget.Micros, len(c.ReportedBy))
		for model, usd := range c.ReportedBy {
			reportBy[model] = microsOfUSD(usd)
		}
		if len(c.ReportedBy) == 1 {
			for model := range c.ReportedBy {
				return reportBy, model
			}
		}
		// The band is the run's own figure over every call it reported a
		// cost for, and no row of several may carry all of it.
		return reportBy, budgetReconcileMixed
	}
	if c.ReportedUSD <= 0 && c.TableLoUSD <= 0 && c.TableHiUSD <= 0 {
		return nil, ""
	}
	row := reconcileChargedModel(c)
	if c.ReportedUSD <= 0 {
		// A band with no report is a record that cannot be whole (SA1.6's
		// band is over reported calls), and it is shown rather than dropped.
		return nil, row
	}
	return map[string]budget.Micros{row: microsOfUSD(c.ReportedUSD)}, row
}

// reconcileChargedModel is the one model a run's charge names, and
// budgetReconcileMixed when it names none or several: the row an old record's
// whole-run report belongs on.
func reconcileChargedModel(c *runstore.Cost) string {
	var only string
	for model, usd := range c.ModelBy {
		if usd == 0 {
			continue
		}
		if only != "" {
			return budgetReconcileMixed
		}
		only = model
	}
	if only == "" {
		return budgetReconcileMixed
	}
	return only
}

// reconcileAccum is one row being built.
type reconcileAccum struct {
	charge, report, bandLo, bandHi budget.Micros
	tokens                         runstore.ModelTokenCounts
	runs, runsWithReport           int
	tiers                          map[string]bool
}

// reconcileRows folds the window's runs into one row per model -- plus
// `unattributed` and `mixed` where a run recorded them -- and the totals over
// them.
func reconcileRows(runs []reconcileRun, totals budgetReconcileTotals) ([]budgetReconcileModel, budgetReconcileTotals) {
	rows := map[string]*reconcileAccum{}
	row := func(name string) *reconcileAccum {
		a, ok := rows[name]
		if !ok {
			a = &reconcileAccum{tiers: map[string]bool{}}
			rows[name] = a
		}
		return a
	}
	var charge, report, bandLo, bandHi, feePaid, notional budget.Micros
	for _, r := range runs {
		c := r.cost
		totals.Runs++
		if r.reported {
			totals.RunsWithReport++
		}
		totals.UnreportedCalls += c.UnreportedCalls
		notional += microsOfUSD(c.ModelSubscriptionUSD())
		feePaid += r.feePaid

		// touched is the rows this run is one run of, so that a model the run
		// only recorded tokens for still counts as a run of that row.
		touched := map[string]bool{}
		for model, usd := range c.ModelBy {
			usd := microsOfUSD(usd)
			if usd == 0 {
				continue
			}
			row(model).charge += usd
			charge += usd
			touched[model] = true
		}
		for model, m := range r.reportBy {
			row(model).report += m
			report += m
			touched[model] = true
		}
		if r.bandRow != "" {
			lo, hi := microsOfUSD(c.TableLoUSD), microsOfUSD(c.TableHiUSD)
			a := row(r.bandRow)
			a.bandLo, a.bandHi = a.bandLo+lo, a.bandHi+hi
			bandLo, bandHi = bandLo+lo, bandHi+hi
			touched[r.bandRow] = true
		}
		for model, t := range c.ModelTokens {
			a := row(model)
			a.tokens.Input += t.Input
			a.tokens.Output += t.Output
			a.tokens.CacheRead += t.CacheRead
			a.tokens.CacheWrite5m += t.CacheWrite5m
			a.tokens.CacheWrite1h += t.CacheWrite1h
			touched[model] = true
		}
		for _, tier := range c.ServiceTiers {
			for model := range touched {
				row(model).tiers[tier] = true
			}
		}
		for model := range touched {
			rows[model].runs++
		}
		for model := range r.reportBy {
			rows[model].runsWithReport++
		}
	}
	out := make([]budgetReconcileModel, 0, len(rows))
	for _, model := range reconcileOrder(rows) {
		a := rows[model]
		out = append(out, budgetReconcileModel{
			Model:            model,
			ChargeUSD:        a.charge.USD(),
			ReportUSD:        a.report.USD(),
			BandLoUSD:        a.bandLo.USD(),
			BandHiUSD:        a.bandHi.USD(),
			InputTokens:      a.tokens.Input,
			OutputTokens:     a.tokens.Output,
			CacheReadTokens:  a.tokens.CacheRead,
			CacheWriteTokens: a.tokens.CacheWrite5m + a.tokens.CacheWrite1h,
			Runs:             a.runs,
			RunsWithReport:   a.runsWithReport,
			ServiceTiers:     slices.Sorted(maps.Keys(a.tiers)),
		})
	}
	totals.ChargeUSD = charge.USD()
	totals.ChargeWithoutFeeUSD = max(charge-feePaid, 0).USD()
	totals.NotionalUSD = notional.USD()
	totals.ReportUSD = report.USD()
	totals.BandLoUSD, totals.BandHiUSD = bandLo.USD(), bandHi.USD()
	for _, m := range out {
		totals.InputTokens += m.InputTokens
		totals.OutputTokens += m.OutputTokens
		totals.CacheReadTokens += m.CacheReadTokens
		totals.CacheWriteTokens += m.CacheWriteTokens
	}
	return out, totals
}

// reconcileOrder is the rows' order: the models by charge, dearest first and
// by name where they tie, and the reading's own two buckets after every model
// -- `unattributed` then `mixed` -- so a model is never read beside a bucket as
// though the two were the same kind of thing.
func reconcileOrder(rows map[string]*reconcileAccum) []string {
	keys := slices.Sorted(maps.Keys(rows))
	slices.SortStableFunc(keys, func(a, b string) int {
		rank := func(s string) int {
			switch s {
			case budgetReconcileUnattributed:
				return 1
			case budgetReconcileMixed:
				return 2
			}
			return 0
		}
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		switch ca, cb := rows[a].charge, rows[b].charge; {
		case ca > cb:
			return -1
		case cb > ca:
			return 1
		}
		return 0
	})
	return keys
}

// writeReconcile prints the reading as its table with its summary lines, or as
// one JSON document; warn takes what the read could not use, so a person
// reading the table sees it and a consumer reads it in the document.
func writeReconcile(w, warn io.Writer, doc budgetReconcileDoc, asJSON bool) error {
	for _, line := range doc.Warnings {
		fmt.Fprintln(warn, "warning: "+safetext.Strip(line))
	}
	if asJSON {
		return writeJSON(w, doc)
	}
	scope := "every repository in the runs bucket"
	if doc.Repo != "" {
		scope = doc.Repo
	}
	window := fmt.Sprintf("%s to %s, %s", doc.From, doc.To, scope)
	if doc.Truncated {
		window += fmt.Sprintf(", truncated to the newest %d runs", doc.MaxRuns)
	}
	fmt.Fprintf(w, "project: %s\n", doc.Project)
	fmt.Fprintf(w, "window:  %s\n", window)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tCHARGE\tREPORT\tBAND\tIN\tOUT\tCACHE R\tCACHE W\tRUNS\tWITH REPORT\tTIERS")
	for _, m := range doc.Models {
		fmt.Fprintln(tw, reconcileLine(m))
	}
	fmt.Fprintln(tw, reconcileTotalLine(doc.Totals))
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, line := range reconcileSummary(doc.Totals) {
		fmt.Fprintln(w, line)
	}
	return nil
}

// reconcileLine is one row of the table. A dash is "the record carries
// nothing here", which is not the same answer as a zero.
func reconcileLine(m budgetReconcileModel) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s",
		cellOneLine(clipRunes(m.Model, maxNameCellRunes)), money(microsOfUSD(m.ChargeUSD)),
		reconcileMoney(microsOfUSD(m.ReportUSD)), reconcileBand(microsOfUSD(m.BandLoUSD), microsOfUSD(m.BandHiUSD)),
		m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWriteTokens,
		m.Runs, m.RunsWithReport, strings.Join(m.ServiceTiers, ", "))
}

// reconcileTotalLine is the table's own sum, so a reader can see the rows add
// up to the figures the summary lines state.
func reconcileTotalLine(t budgetReconcileTotals) string {
	return fmt.Sprintf("TOTAL\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t",
		money(microsOfUSD(t.ChargeUSD)), reconcileMoney(microsOfUSD(t.ReportUSD)),
		reconcileBand(microsOfUSD(t.BandLoUSD), microsOfUSD(t.BandHiUSD)),
		t.InputTokens, t.OutputTokens, t.CacheReadTokens, t.CacheWriteTokens,
		t.Runs, t.RunsWithReport)
}

func reconcileMoney(m budget.Micros) string {
	if m == 0 {
		return "-"
	}
	return money(m)
}

func reconcileBand(lo, hi budget.Micros) string {
	if lo == 0 && hi == 0 {
		return "-"
	}
	return money(lo) + "-" + money(hi)
}

// reconcileSummary is what the table cannot say by itself: what the window's
// runs did and did not record, what the fee is worth, and how much of the
// charge a subscription paid.
func reconcileSummary(t budgetReconcileTotals) []string {
	out := []string{
		fmt.Sprintf("runs:     %d with a cost breakdown, %d with a record and none, %d with no readable record, %d that never started",
			t.Runs, t.NoCost, t.NoRecord+t.RecordCorrupt, t.Unlaunched),
		fmt.Sprintf("charge:   %s with the route fee, %s without it (what the providers themselves charged)",
			money(microsOfUSD(t.ChargeUSD)), money(microsOfUSD(t.ChargeWithoutFeeUSD))),
	}
	if t.ReportUSD > 0 {
		line := fmt.Sprintf("report:   %s reported by the providers over %d of %d runs",
			money(microsOfUSD(t.ReportUSD)), t.RunsWithReport, t.Runs)
		// A record written before SA1.6 carries no band, and a window of them
		// says so by saying nothing rather than by printing a dash beside the
		// figure it would be read against.
		if t.BandLoUSD > 0 || t.BandHiUSD > 0 {
			line += fmt.Sprintf("; the table band over those calls is %s", reconcileBand(microsOfUSD(t.BandLoUSD), microsOfUSD(t.BandHiUSD)))
		}
		out = append(out, line)
	} else {
		out = append(out, fmt.Sprintf("report:   the providers reported no call of this window (of %d runs with a cost breakdown)", t.Runs))
	}
	if t.UnreportedCalls > 0 {
		out = append(out, fmt.Sprintf("calls:    %d settled calls carried no provider cost, so the report and the band are short of them", t.UnreportedCalls))
	}
	if t.NotionalUSD > 0 {
		out = append(out, fmt.Sprintf("notional: %s of the charge was a subscription's plan, which the provider reports and no invoice bills", money(microsOfUSD(t.NotionalUSD))))
	}
	return out
}
