package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
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
		Short: "Report what the project spent, by day, week, month, year, repository, model or person",
		Long: "Report model spend from the project's spend history.\n\n" +
			"Finished days are read from the spendDaily documents in the project's Firebase\n" +
			"Firestore (you need roles/datastore.viewer on the Firebase project, which\n" +
			"launchers, operators and budget admins have). Today, yesterday and any day the\n" +
			"daily job has not archived yet are computed live from the budget database and\n" +
			"marked (partial). --since and --until are UTC dates (YYYY-MM-DD); --since also\n" +
			"takes Nd, the last N days up to --until (default 30d); --until defaults to today.\n\n" +
			"--by day|week|month|year groups in time; weeks are ISO weeks in UTC, starting on\n" +
			"Monday (2026-W40). --by repo|model|person groups across the range. Dollars are\n" +
			"exact (integer micro-dollars). Model $, NOTIONAL~ (a subscription's list-price\n" +
			"figure, never billed) and COMPUTE $ are separate columns and never summed;\n" +
			"compute reads n/a when no run's compute was estimated. A person is the stored\n" +
			"requester address; `unknown` is spend with no known requester.\n\n" +
			"--csv writes RFC 4180 with spreadsheet-safe cells (a cell starting with = + - @\n" +
			"is prefixed with '). --json prints {project, source, degraded, by, since, until,\n" +
			"weeks, rows[{key, start, end, partial, model_micros, model_usd, notional_micros,\n" +
			"notional_usd, compute_micros, compute_usd, compute_estimated, run_hours, runs,\n" +
			"calls, tokens}], totals, warnings}; *_usd are exact decimal strings and null\n" +
			"means n/a or not recorded for that breakdown.\n\n" +
			"A project without Firestore history says so and shows run-record totals instead\n" +
			"(from the runs bucket, not budget history).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runReport(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.StringVar(&o.since, "since", reportDefaultSince, "first day: YYYY-MM-DD (UTC) or Nd, the last N days")
	f.StringVar(&o.til, "until", "", "last day, YYYY-MM-DD (UTC; default today)")
	f.StringVar(&o.by, "by", "day", "group by day, week, month, year, repo, model or person")
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
	if n, ok := strings.CutSuffix(since, "d"); ok && !strings.Contains(since, "-") {
		days, err := strconv.Atoi(n)
		if err != nil || days < 1 || days > reportMaxDays {
			return 0, 0, fmt.Errorf("--since %q: use a date (YYYY-MM-DD) or a number of days (30d, 1 to %d)", safetext.Strip(since), reportMaxDays)
		}
		from = to - int64(days) + 1
	} else {
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			return 0, 0, fmt.Errorf("--since %q: use a date (YYYY-MM-DD) or a number of days (30d)", safetext.Strip(since))
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
	var wantSlug string
	if o.repo != "" {
		if wantSlug, err = repoSlugOf(ctx, lc, o.repo); err != nil {
			return err
		}
	}
	m := report.Meta{Project: lc.Name, Source: "firestore", Since: budget.DayDate(from), Un: budget.DayDate(to)}
	warn := func(s string) { m.Warnings = append(m.Warnings, s) }

	recs, why, err := reportHistory(ctx, lc, wantSlug, from, to, today, warn)
	if err != nil {
		return err
	}
	if why != "" {
		return reportDegraded(ctx, cmd, o, lc, dim, m, why, from, to)
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
		return nil, "the project's Firestore database has no history mark (fugaro init --firebase has not set it up)", nil
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
	var recs []report.Rec
	for _, r := range rows {
		day := budget.Day(r.Created.UTC())
		if day < from || day > to {
			continue
		}
		recs = append(recs, report.Rec{DayRecord: recordOfRun(r)})
	}
	rep := report.Build(recs, dim)
	rep.NoHours = true
	return reportEmit(cmd, o, rep, m)
}

func microsOfUSD(v float64) budget.Micros {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return budget.Micros(math.Round(v * 1e6))
}

// recordOfRun is one run as a one-run day record, so the run-record totals
// group like the history does. Subscription model spend is notional.
func recordOfRun(r runview.Row) budget.DayRecord {
	rec := budget.DayRecord{
		Repo: r.Repo, Slug: r.Repo, Date: budget.DayDate(budget.Day(r.Created.UTC())), Runs: 1,
		Outcomes: map[string]int64{}, ByModel: map[string]budget.ModelTotals{}, ByPerson: map[string]budget.PersonTotals{},
	}
	spent, notional := microsOfUSD(r.Cost.ModelUSD), budget.Micros(0)
	if r.Cost.ModelBasis == runstore.BasisSubscription {
		spent, notional = 0, microsOfUSD(r.Cost.ModelUSD)
	}
	rec.SpentMicros, rec.NotionalMicros = spent, notional
	if r.Cost.ComputeEstimated {
		rec.ComputeMicros, rec.ComputeEstimatedRuns = microsOfUSD(r.Cost.ComputeUSD), 1
	}
	for name, usd := range r.Cost.ModelBy {
		if r.Cost.ModelBasis == runstore.BasisSubscription {
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
