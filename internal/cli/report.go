package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro report (plan M9d task 6): what the project spent, by day, week,
// month, year, repository, model or person. Finished days come from the
// spendDaily documents in Firestore (read as the person: roles/datastore.viewer
// on the Firebase project), today and any day not archived yet are computed
// live from the Realtime Database with the same Rollup the daily job uses and
// are marked (partial). Without Firestore history it falls back to run-record
// totals, clearly labelled as such.

const (
	reportDefaultSince = "30d"
	// reportMaxDays bounds a range; a query reads a document per repo per day.
	reportMaxDays = 3660
)

type reportOptions struct {
	cloud            cloudOptions
	repo, since, til string
	by               string
	json, csv        bool
}

func newReportCmd() *cobra.Command {
	var o reportOptions
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Report what the project spent, by day, week, month, year, repository, model, person or mode",
		Long: "Report model spend from the project's spend history.\n\n" +
			"Finished days are read from the spendDaily documents in the project's Firebase\n" +
			"Firestore (you need roles/datastore.viewer on the Firebase project, which\n" +
			"launchers, operators and budget admins have). Today, yesterday and any day the\n" +
			"daily job has not archived yet are computed live from the budget database and\n" +
			"marked (partial). --since and --until are UTC dates (YYYY-MM-DD); --since also\n" +
			"takes Nd, the last N days up to --until (default 30d); --until defaults to today.\n\n" +
			"--by day|week|month|year groups in time; weeks are ISO weeks in UTC, starting on\n" +
			"Monday (2026-W40). --by repo|model|person groups across the range, and --by mode\n" +
			"groups by the rigor mode each run resolved (poc, startup, pro, critical, hotfix,\n" +
			"and none for a run whose record carries no mode, or carries one this version does\n" +
			"not know), read from the run records and not from this history: a mode row made\n" +
			"only of runs whose records could not be read is marked unreadable and reads n/a,\n" +
			"never 0. Dollars are exact (integer micro-dollars). Model $, NOTIONAL~ (a\n" +
			"subscription's list-price figure, never billed) and COMPUTE $ are separate\n" +
			"columns and never summed; compute reads n/a when no run's compute was estimated.\n" +
			"A person is the stored requester address; `unknown` is spend, and runs, with no\n" +
			"requester recorded. By model, a subscription model's dollars are notional\n" +
			"(priced from its tokens, so they can differ slightly from the run's recorded\n" +
			"notional total).\n\n" +
			"--csv writes RFC 4180 with spreadsheet-safe cells (a cell starting with = + - @\n" +
			"is prefixed with '). --json prints {project, source, degraded, by, since, until,\n" +
			"weeks, rows[{key, start, end, partial, unreadable, model_micros, model_usd,\n" +
			"notional_micros, notional_usd, compute_micros, compute_usd, compute_estimated,\n" +
			"run_hours, runs, calls, tokens}], totals, warnings}; *_usd are exact decimal\n" +
			"strings and null means n/a or not recorded for that breakdown (an unreadable\n" +
			"row carries null money).\n\n" +
			"A project without Firestore history says so and shows run-record totals instead\n" +
			"(from the runs bucket, not budget history).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runReport(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.StringVar(&o.since, "since", reportDefaultSince, "first day: YYYY-MM-DD (UTC) or Nd, the last N days")
	f.StringVar(&o.til, "until", "", "last day, YYYY-MM-DD (UTC; default today)")
	f.StringVar(&o.by, "by", "day", "group by day, week, month, year, repo, model, person or mode")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	f.BoolVar(&o.csv, "csv", false, "print CSV (RFC 4180)")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// reportRange parses --since/--until against today (a UTC day number).
func reportRange(since, until string, today int64) (from, to int64, err error) {
	to = today
	if until != "" {
		t, err := time.Parse("2006-01-02", until)
		if err != nil {
			return 0, 0, fmt.Errorf("--until %q: use a date, YYYY-MM-DD", safetext.Strip(until))
		}
		to = min(budget.Day(t), today)
	}
	n, unit := since, int64(0)
	if v, ok := strings.CutSuffix(since, "d"); ok {
		n, unit = v, 1
	} else if v, ok := strings.CutSuffix(since, "w"); ok {
		n, unit = v, 7
	}
	if unit > 0 && !strings.Contains(since, "-") {
		count, err := strconv.Atoi(n)
		days := int64(count) * unit
		if err != nil || count < 1 || days > reportMaxDays {
			return 0, 0, fmt.Errorf("--since %q: use a date (YYYY-MM-DD), a number of days (30d) or weeks (12w), at most %d days", safetext.Strip(since), reportMaxDays)
		}
		from = to - days + 1
	} else {
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			return 0, 0, fmt.Errorf("--since %q: use a date (YYYY-MM-DD) a number of days (30d) or weeks (12w)", safetext.Strip(since))
		}
		from = budget.Day(t)
	}
	if from > to {
		return 0, 0, fmt.Errorf("--since %s is after --until %s", budget.DayDate(from), budget.DayDate(to))
	}
	if to-from+1 > reportMaxDays {
		return 0, 0, fmt.Errorf("the range is longer than %d days", reportMaxDays)
	}
	return from, to, nil
}

func runReport(cmd *cobra.Command, o *reportOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if o.json && o.csv {
		return userErr("--json and --csv don't go together")
	}
	dim, err := report.ParseDim(o.by)
	if err != nil {
		return userErr("%v", err)
	}
	now := budgetNow().UTC()
	today := budget.Day(now)
	from, to, err := reportRange(o.since, o.til, today)
	if err != nil {
		return userErr("%v", err)
	}
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o.cloud) // the project header goes to stderr
	if err != nil {
		return err
	}
	m := report.Meta{Project: lc.Name, Source: "firestore", Since: budget.DayDate(from), Un: budget.DayDate(to)}
	warn := func(s string) { m.Warnings = append(m.Warnings, s) }
	if dim == report.ByMode {
		return reportByMode(ctx, cmd, o, lc, m, from, to)
	}
	var wantSlug string
	if o.repo != "" {
		if wantSlug, err = repoSlugOf(ctx, lc, o.repo); err != nil {
			return err
		}
	}

	recs, why, err := reportHistory(ctx, lc, wantSlug, from, to, today, warn)
	if err != nil {
		return err
	}
	if why != "" {
		return reportDegraded(ctx, cmd, o, lc, dim, m, why, from, to)
	}
	// The history path knows a day's compute is an estimate (the records
	// count the runs it was estimated for) but not the price table each came
	// from: the daily rollup stores the figure, not its source (design C17),
	// so the source is named no further than that here.
	if daysWithCompute(recs) {
		m.Warnings = append(m.Warnings, reportComputeEstimateNote)
	}
	return reportEmit(cmd, o, report.Build(recs, dim), m)
}

func reportEmit(cmd *cobra.Command, o *reportOptions, rep report.Report, m report.Meta) error {
	out := cmd.OutOrStdout()
	switch {
	case o.json:
		return report.JSON(out, rep, m)
	case o.csv:
		for _, w := range m.Warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+safetext.Strip(w))
		}
		if m.Degraded {
			fmt.Fprintln(cmd.ErrOrStderr(), safetext.Strip(m.Notice))
		}
		return report.CSV(out, rep)
	}
	return report.Table(out, rep, m)
}

// reportHistory reads the day records. why is non-empty when history is not
// enabled (the degraded mode).
func reportHistory(ctx context.Context, lc *localcfg.Config, wantSlug string, from, to, today int64, warn func(string)) (recs []report.Rec, why string, err error) {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, "project " + lc.Name + " has no Firebase budget backend, so it has no spend history", nil
	}
	fs, err := newReportFirestore(ctx, lc)
	if err != nil {
		return nil, "", err
	}
	// The mark first: the database must be this project's history.
	doc, err := fs.Get(ctx, budget.MetaCollection, budget.MarkDocument)
	switch {
	case errors.Is(err, firestore.ErrNoDatabase):
		return nil, "the project's Firebase project has no Firestore database", nil
	case errors.Is(err, firestore.ErrNotFound):
		return nil, "the project's Firestore database has no history mark (fugaro update --firebase-project has not set it up)", nil
	case err != nil:
		return nil, "", firestoreErr(lc, err)
	}
	if p, _ := doc.Fields["project"].(string); p != lc.Name {
		return nil, "", userErr("the Firestore database of Firebase project %s belongs to project %q, not %q; fix budget.firebase_project in project %s's config", lc.Budget.FirebaseProject, safetext.Strip(p), lc.Name, lc.Name)
	}

	docs, err := fs.Query(ctx, budget.SpendCollection, "date", budget.DayDate(from), budget.DayDate(to))
	if err != nil {
		return nil, "", firestoreErr(lc, err)
	}
	byDay := map[int64][]report.Rec{}
	final := map[int64]bool{}
	bad := 0
	for _, d := range docs {
		r, err := budget.FromFields(d.Fields)
		if err != nil {
			bad++
			continue
		}
		t, err := time.Parse("2006-01-02", r.Date)
		if err != nil || budget.Day(t) < from || budget.Day(t) > to {
			bad++
			continue
		}
		day := budget.Day(t)
		if wantSlug == "" || r.Slug == wantSlug {
			byDay[day] = append(byDay[day], report.Rec{DayRecord: r, Partial: !r.Final})
		}
		if r.Final {
			final[day] = true
		}
	}
	if bad > 0 {
		warn(fmt.Sprintf("%d history document(s) could not be read and are left out of the totals", bad))
	}

	// Live days: not final, still in the database's window.
	var live []int64
	for d := max(from, today-budget.PruneAfter-1); d <= to; d++ {
		if !final[d] {
			live = append(live, d)
		}
	}
	if len(live) > 0 {
		if lr, ok := reportLive(ctx, lc, live, warn); ok {
			for _, d := range live {
				if _, has := lr[d]; has {
					delete(byDay, d) // fresher than a provisional document
					for _, r := range lr[d] {
						if wantSlug == "" || r.Slug == wantSlug {
							byDay[d] = append(byDay[d], report.Rec{DayRecord: r, Partial: true})
						}
					}
				}
			}
		} else {
			for d, rs := range byDay {
				if !final[d] {
					for i := range rs {
						rs[i].Partial = true
					}
				}
			}
		}
	}
	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	for _, d := range days {
		recs = append(recs, byDay[d]...)
	}
	return recs, "", nil
}

// reportLive computes the live days from the budget database; ok is false
// (with a warning) when it cannot.
func reportLive(ctx context.Context, lc *localcfg.Config, days []int64, warn func(string)) (map[int64][]budget.DayRecord, bool) {
	if lc.Budget.RTDBURL == "" {
		warn("no budget database is configured, so days not archived yet are missing")
		return nil, false
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err == nil {
		err = checkDBProject(ctx, db, lc)
	}
	if err != nil {
		warn("live figures for the days not archived yet are unavailable: " + err.Error())
		return nil, false
	}
	facts := &lazyFacts{lc: lc, warn: warn}
	defer facts.close()
	recs, err := budget.LiveDays(ctx, db, facts, days, warn)
	if err != nil {
		warn("live figures for the days not archived yet are unavailable: " + dbErr(lc, err, false).Error())
		return nil, false
	}
	return recs, true
}

// lazyFacts opens the runs bucket only when there is something to read.
type lazyFacts struct {
	lc   *localcfg.Config
	warn func(string)
	env  *cloudEnv
}

func (l *lazyFacts) Facts(ctx context.Context, from, to int64) (map[int64][]budget.RunFact, map[string]string, error) {
	env, err := openCloudFor(ctx, l.lc)
	if err != nil {
		return nil, nil, err
	}
	l.env = env
	return budget.BucketFacts{Bucket: env.bucket.Bucket, Warn: l.warn}.Facts(ctx, from, to)
}

func (l *lazyFacts) close() {
	if l.env != nil {
		l.env.Close()
	}
}

func newReportFirestore(ctx context.Context, lc *localcfg.Config) (*firestore.Client, error) {
	var src oauth2.TokenSource
	if lc.Endpoints.NoAuth {
		if lc.Endpoints.Firestore == "" {
			return nil, userErr("no_auth is set but endpoints.firestore is empty: refusing to reach the real Firestore without credentials")
		}
		src = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})
	} else {
		ts, err := google.DefaultTokenSource(ctx, cloudPlatform)
		if err != nil {
			return nil, userErr("no Google credentials to reach the spend history: run gcloud auth application-default login (%v)", err)
		}
		src = ts
	}
	fs, err := firestore.New(lc.Endpoints.Firestore, lc.Budget.FirebaseProject, src)
	if err != nil {
		return nil, userErr("%v", err)
	}
	return fs, nil
}

// firestoreErr classifies a Firestore read failure: a refusal names the role.
func firestoreErr(lc *localcfg.Config, err error) error {
	if errors.Is(err, firestore.ErrPermission) {
		return userErr("project %s's spend history refused the read: you need the Cloud Datastore Viewer role (roles/datastore.viewer) on its Firebase project %s, plus the Service Usage Consumer role (roles/serviceusage.serviceUsageConsumer); GCP owners and editors have both; gcloud auth application-default login may be needed (%s)",
			lc.Name, safetext.Strip(lc.Budget.FirebaseProject), safetext.Strip(err.Error()))
	}
	return remote(fmt.Errorf("reading the spend history: %s", safetext.Strip(err.Error())))
}

// ------------------------------------------------------------- by mode

// reportMaxRuns is how many runs the by-mode read opens. It bounds the read
// rather than the window: a window holding more is reported to its newest
// reportMaxRuns runs, and the report says so. It is a variable so a test can
// drive the truncation without planting thousands of runs.
var reportMaxRuns = userStatsMaxRuns

// reportModeSourceNote is what the by-mode rows say about where they come from:
// the mode a run resolved is written in the run's own record and in no day
// rollup, so this breakdown reads the runs bucket and never the spend history.
const reportModeSourceNote = "these rows come from run records (the runs bucket), not the Firestore spend history: a run's rigor mode is written in its own record, and every run counts once, on its launch day"

// reportByMode is --by mode: what the project spent in each rigor mode its runs
// resolved, from the run records. It needs neither Firestore nor the budget
// database, so there is no degraded path for it.
func reportByMode(ctx context.Context, cmd *cobra.Command, o *reportOptions, lc *localcfg.Config, m report.Meta, from, to int64) error {
	m.Source = "run-records"
	m.Warnings = append(m.Warnings, reportModeSourceNote)
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	// Every repository the runs bucket holds, not only the ones the local
	// config lists: the history-backed breakdowns cover every repository of
	// the project's Firestore collection, so this one covers the project too.
	// --repo still names one.
	slugs, err := lsSlugs(ctx, env, &lsOptions{repo: o.repo, all: true}, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The window's end as an instant, so a run launched after --until is
	// dropped by the reader: --until may be in the past, and a run outside the
	// window must not take a place the cap keeps for a run of it.
	until := time.UnixMilli((to + 1) * 86_400_000).UTC()
	objs, _, truncated, err := readRunObjects(ctx, env, slugs, time.UnixMilli(from*86_400_000).UTC(), until, reportMaxRuns)
	if err != nil {
		return err
	}
	if truncated {
		m.Warnings = append(m.Warnings, fmt.Sprintf("the window holds more runs than the %d this report reads: only its newest %d are counted, so a run outside them is in no row", reportMaxRuns, reportMaxRuns))
	}
	recs, unread := modeRecords(objs, to)
	rep := report.Build(recs, report.ByMode)
	rep.NoHours = true
	if unread.total > 0 {
		// A total that sums unreadable rows is short, and a reader has to be
		// told rather than left to take it for the window's whole spend.
		m.Warnings = append(m.Warnings, fmt.Sprintf(
			"%d of the window's runs have no record this report can read (%d hold a result.json this build cannot read, %d have none in the bucket): they count in RUNS, and the money is short by whatever they spent",
			unread.total, unread.corrupt, unread.total-unread.corrupt))
	}
	return reportEmit(cmd, o, rep, m)
}

// modeUnreadable counts the window's runs with no record this report can read:
// total is every one of them, corrupt the ones whose result.json the bucket
// holds and this read could not use. The rest have none in the bucket, which is
// a different fact about the run and the same fact about the money.
type modeUnreadable struct{ total, corrupt int }

// modeRecords is the by-mode grouping's input: one Rec per run of the window,
// dated on its launch day, carrying the rigor mode its record resolved and the
// money its record settled. A run this report has no readable record for -- the
// bucket holds a result.json it cannot read, or holds none because the run has
// not written one -- did launch, so it counts once among the runs and adds
// nothing to the money: the record is marked unreadable, so a row made only of
// such runs reads n/a rather than the 0 a reader would take for a measured
// spend. The two are counted apart so the warning can say which they were.
func modeRecords(objs []runObject, to int64) ([]report.Rec, modeUnreadable) {
	var recs []report.Rec
	var unread modeUnreadable
	for _, o := range objs {
		t, err := runstore.RunTime(o.id)
		if err != nil || budget.Day(t) > to {
			continue
		}
		// Only the fields the by-mode grouping reads: it groups the run's own
		// record, not a repository's day.
		rec := report.Rec{DayRecord: budget.DayRecord{Date: budget.DayDate(budget.Day(t)), Runs: 1}}
		if c := o.record; c != nil {
			if c.Rigor != nil {
				rec.Mode = string(c.Rigor.Mode)
			}
			if c.Cost != nil {
				applyCost(&rec.DayRecord, *c.Cost)
			}
		} else {
			rec.Unreadable = true
			unread.total++
			if o.recordCorrupt {
				unread.corrupt++
			}
		}
		recs = append(recs, rec)
	}
	return recs, unread
}

// ------------------------------------------------------------- degraded

// reportDegraded says history is not enabled and offers totals from the run
// records (ls's loader), labelled as such.
func reportDegraded(ctx context.Context, cmd *cobra.Command, o *reportOptions, lc *localcfg.Config, dim report.Dim, m report.Meta, why string, from, to int64) error {
	m.Degraded, m.Source = true, "run-records"
	m.Notice = "spend history is not enabled: " + why + ". Showing totals from run records (the runs bucket), not budget history: figures are each run's own result, finished and failed runs alike, and have no per-day archive"
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return err
	}
	defer env.Close()
	errw := cmd.ErrOrStderr()
	f := lsFilter{warn: errw, since: time.UnixMilli(from * 86_400_000).UTC()}
	if f.slugs, err = lsSlugs(ctx, env, &lsOptions{repo: o.repo}, errw); err != nil {
		return err
	}
	rows, err := loadRows(ctx, env, f, budgetNow())
	if err != nil {
		return err
	}
	if dim == report.ByModel {
		m.Warnings = append(m.Warnings, "run records do not carry a per-model split: --by model shows nothing here")
	}
	// Only the runs the report shows: a source line for a day outside --since
	// and --until would name a table no figure in this report came from.
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		day := budget.Day(r.Created.UTC())
		return day < from || day > to
	})
	var recs []report.Rec
	for _, r := range rows {
		recs = append(recs, report.Rec{DayRecord: recordOfRun(r)})
	}
	// The compute figures above are estimates, and these rows carry the
	// price table each came from. The report has no per-row column for it
	// (design compute-backend.md §5.2, C17: the figure's shape is Cloud
	// Run's, so the report and the history need no new column), so the
	// source is said once here -- which is where a reader learns that a VM
	// backend's estimate leaves the VM's boot disk and Cloud NAT out.
	for _, src := range computeSources(rows) {
		m.Warnings = append(m.Warnings, "compute_usd is an estimate priced from "+src)
	}
	rep := report.Build(recs, dim)
	rep.NoHours = true
	return reportEmit(cmd, o, rep, m)
}

// computeSources is the distinct price-table source lines the rows' compute
// estimates were priced from, sorted, for the report's own source line. Rows
// priced with a table that does not name itself (Cloud Run's list price, and
// every run recorded before this existed) contribute nothing, so the report
// of an installation that never ran on a VM reads exactly as it did.
func computeSources(rows []runview.Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if src := r.Cost.ComputeSource; src != "" && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	sort.Strings(out)
	return out
}

// reportComputeEstimateNote is the one line the history-backed report says
// about its COMPUTE column. The history keeps the figure, not the table it
// came from, so a reader of an installation that ran on a VM would otherwise
// never learn that the column leaves the VM's boot disk and Cloud NAT out
// (design compute-backend.md §5.2, C17) -- the run-record path below says the
// same thing with the table it can name.
const reportComputeEstimateNote = "COMPUTE $ is an estimate, priced from each run's own price table: a VM run's figure leaves the VM's boot disk and Cloud NAT out"

// daysWithCompute reports whether any record in the report priced compute for
// a run. An installation that never estimated compute gets no note, so its
// report reads exactly as it did before this existed.
func daysWithCompute(recs []report.Rec) bool {
	for _, r := range recs {
		if r.ComputeEstimatedRuns > 0 {
			return true
		}
	}
	return false
}

// costLines is the cost block diagnose prints: the cost line, and under it
// the compute estimate's own source line when it has one (a VM run's price
// table, design compute-backend.md §5.2). One line per line, since oneLine
// would turn the break between them into a "?" and the two lines are not one
// sentence.
func costLines(c runstore.Cost) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(runner.CostLine(c), "**", ""), "\n") {
		out = append(out, oneLine(ln))
	}
	return out
}

// microsOfUSD converts a USD figure to the history's micro-dollars, treating
// a figure that is not a positive number as zero.
func microsOfUSD(v float64) budget.Micros {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return budget.Micros(math.Round(v * 1e6))
}

// applyCost writes a run's cost into a one-run day record: the two halves of
// the model figure (P12, D140), so a mixed cascade run's tier-A dollars are
// notional in the history and a subscription run's whole figure still is. One
// split for both readers of a run record -- the run-record totals and the
// by-mode rows -- so the two divide a mixed run the same way.
func applyCost(rec *budget.DayRecord, c runstore.Cost) {
	rec.SpentMicros = microsOfUSD(c.ModelMoneyUSD())
	rec.NotionalMicros = microsOfUSD(c.ModelSubscriptionUSD())
	if c.ComputeEstimated {
		rec.ComputeMicros, rec.ComputeEstimatedRuns = microsOfUSD(c.ComputeUSD), 1
	}
}

// recordOfRun is one run as a one-run day record, so the run-record totals
// group like the history does.
func recordOfRun(r runview.Row) budget.DayRecord {
	rec := budget.DayRecord{
		Repo: r.Repo, Slug: r.Repo, Date: budget.DayDate(budget.Day(r.Created.UTC())), Runs: 1,
		Outcomes: map[string]int64{}, ByModel: map[string]budget.ModelTotals{}, ByPerson: map[string]budget.PersonTotals{},
	}
	applyCost(&rec, r.Cost)
	spent, notional := rec.SpentMicros, rec.NotionalMicros
	// The by-model split of a run with a notional part is of its whole model
	// figure and cannot be told apart per model, so it is left out rather
	// than counted as money (a subscription run always had none of it here).
	// The split is asked of the notional part, never of the basis (P12,
	// D140): a cascade run the plan served in full is api-list and billed
	// nothing.
	for name, usd := range r.Cost.ModelBy {
		if r.Cost.AllNotional() || notional > 0 {
			continue
		}
		rec.ByModel[budget.Key(name)] = budget.ModelTotals{Micros: microsOfUSD(usd)}
	}
	who := r.RequestedBy
	if strings.TrimSpace(who) == "" {
		who = budget.UnknownPerson
	}
	rec.ByPerson[budget.Key(who)] = budget.PersonTotals{Micros: spent, NotionalMicros: notional, Runs: 1}
	return rec
}
