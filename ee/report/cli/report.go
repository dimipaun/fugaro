// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	report "github.com/dimipaun/fugaro/ee/report"
	"github.com/dimipaun/fugaro/internal/budget"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/firestore"
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

// Deps are the two core seams this command reads a window and bounds its
// by-mode read with. They reach a moved command through its constructor rather
// than a package-level variable the core would have to publish a setter for:
// only ee/register may set a seam (§5.5), and a test drives these through the
// options instead. NewReportCmd supplies the core's own clock and run cap.
type Deps struct {
	// Now is the clock --since and --until resolve against.
	Now func() time.Time
	// MaxRuns bounds the by-mode read. A window holding more is reported to
	// its newest MaxRuns runs, and the report says so.
	MaxRuns int
}

// NewReportCmd is fugaro report, built with the core's clock and run cap.
func NewReportCmd() *cobra.Command {
	return newReportCmd(Deps{Now: fcli.BudgetNow, MaxRuns: fcli.UserStatsMaxRuns})
}

type reportOptions struct {
	cloud            fcli.CloudFlags
	repo, since, til string
	by               string
	json, csv        bool
}

func newReportCmd(d Deps) *cobra.Command {
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
		RunE: func(cmd *cobra.Command, _ []string) error { return runReport(cmd, &o, d) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.StringVar(&o.since, "since", reportDefaultSince, "first day: YYYY-MM-DD (UTC) or Nd, the last N days")
	f.StringVar(&o.til, "until", "", "last day, YYYY-MM-DD (UTC; default today)")
	f.StringVar(&o.by, "by", "day", "group by day, week, month, year, repo, model, person or mode")
	f.BoolVar(&o.json, "json", false, "print machine-readable output")
	f.BoolVar(&o.csv, "csv", false, "print CSV (RFC 4180)")
	fcli.AddCloudFlags(cmd, &o.cloud)
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

func runReport(cmd *cobra.Command, o *reportOptions, d Deps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if o.json && o.csv {
		return fcli.UserErr("--json and --csv don't go together")
	}
	dim, err := report.ParseDim(o.by)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	now := d.Now().UTC()
	today := budget.Day(now)
	from, to, err := reportRange(o.since, o.til, today)
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
	m := report.Meta{Project: lc.Name, Source: "firestore", Since: budget.DayDate(from), Un: budget.DayDate(to)}
	warn := func(s string) { m.Warnings = append(m.Warnings, s) }
	if dim == report.ByMode {
		return reportByMode(ctx, cmd, o, cloud, m, from, to, d)
	}
	var wantSlug string
	if o.repo != "" {
		if wantSlug, err = cloud.RepoSlug(ctx, o.repo); err != nil {
			return err
		}
	}

	recs, why, err := reportHistory(ctx, cloud, wantSlug, from, to, today, warn)
	if err != nil {
		return err
	}
	if why != "" {
		return reportDegraded(ctx, cmd, o, cloud, dim, m, why, from, to, d)
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
func reportHistory(ctx context.Context, cloud *fcli.Cloud, wantSlug string, from, to, today int64, warn func(string)) (recs []report.Rec, why string, err error) {
	lc := cloud.Config()
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, "project " + lc.Name + " has no Firebase budget backend, so it has no spend history", nil
	}
	fs, err := cloud.Firestore(ctx)
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
		return nil, "", cloud.FirestoreErr(err)
	}
	if p, _ := doc.Fields["project"].(string); p != lc.Name {
		return nil, "", fcli.UserErr("the Firestore database of Firebase project %s belongs to project %q, not %q; fix budget.firebase_project in project %s's config", lc.Budget.FirebaseProject, safetext.Strip(p), lc.Name, lc.Name)
	}

	docs, err := fs.Query(ctx, budget.SpendCollection, "date", budget.DayDate(from), budget.DayDate(to))
	if err != nil {
		return nil, "", cloud.FirestoreErr(err)
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
		if lr, ok := reportLive(ctx, cloud, live, warn); ok {
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
func reportLive(ctx context.Context, cloud *fcli.Cloud, days []int64, warn func(string)) (map[int64][]budget.DayRecord, bool) {
	lc := cloud.Config()
	if lc.Budget.RTDBURL == "" {
		warn("no budget database is configured, so days not archived yet are missing")
		return nil, false
	}
	db, err := cloud.BudgetDB(ctx)
	if err != nil {
		warn("live figures for the days not archived yet are unavailable: " + err.Error())
		return nil, false
	}
	facts := fcli.NewLiveFacts(lc, warn, nil)
	defer facts.Close()
	recs, err := budget.LiveDays(ctx, db, facts, days, warn)
	if err != nil {
		warn("live figures for the days not archived yet are unavailable: " + cloud.DBErr(err, false).Error())
		return nil, false
	}
	return recs, true
}

// ------------------------------------------------------------- by mode

// reportModeSourceNote is what the by-mode rows say about where they come from:
// the mode a run resolved is written in the run's own record and in no day
// rollup, so this breakdown reads the runs bucket and never the spend history.
const reportModeSourceNote = "these rows come from run records (the runs bucket), not the Firestore spend history: a run's rigor mode is written in its own record, and every run counts once, on its launch day"

// reportByMode is --by mode: what the project spent in each rigor mode its runs
// resolved, from the run records. It needs neither Firestore nor the budget
// database, so there is no degraded path for it.
func reportByMode(ctx context.Context, cmd *cobra.Command, o *reportOptions, cloud *fcli.Cloud, m report.Meta, from, to int64, d Deps) error {
	m.Source = "run-records"
	m.Warnings = append(m.Warnings, reportModeSourceNote)
	env, err := fcli.OpenFromConfig(ctx, cloud.Config())
	if err != nil {
		return err
	}
	defer env.Close()
	// Every repository the runs bucket holds, not only the ones the local
	// config lists: the history-backed breakdowns cover every repository of
	// the project's Firestore collection, so this one covers the project too.
	// --repo still names one.
	slugs, err := env.LsSlugs(ctx, o.repo, true, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The window's end as an instant, so a run launched after --until is
	// dropped by the reader: --until may be in the past, and a run outside the
	// window must not take a place the cap keeps for a run of it.
	until := time.UnixMilli((to + 1) * 86_400_000).UTC()
	objs, _, truncated, err := env.ReadRunRecords(ctx, slugs, time.UnixMilli(from*86_400_000).UTC(), until, d.MaxRuns)
	if err != nil {
		return err
	}
	if truncated {
		m.Warnings = append(m.Warnings, fmt.Sprintf("the window holds more runs than the %d this report reads: only its newest %d are counted, so a run outside them is in no row", d.MaxRuns, d.MaxRuns))
	}
	recs, corrupt := modeRecords(objs, to)
	rep := report.Build(recs, report.ByMode)
	rep.NoHours = true
	if corrupt > 0 {
		// A record the bucket holds and this read cannot use is money the total
		// is short by, and a reader has to be told rather than left to take the
		// total for the window's whole spend. A run still in flight has written
		// no figure yet, which every window reaching into the last minutes
		// holds: its row is marked like this one's, and it warns nothing.
		m.Warnings = append(m.Warnings, fmt.Sprintf(
			"%d of the window's runs have a result.json this build cannot read: they count in RUNS, and the money is short by whatever they spent", corrupt))
	}
	return reportEmit(cmd, o, rep, m)
}

// modeRecords is the by-mode grouping's input: one Rec per run of the window,
// dated on its launch day, carrying the rigor mode its record resolved and the
// money its record settled. A run this report has no readable record for -- the
// bucket holds a result.json it cannot read, or holds none because the run has
// not written one -- did launch, so it counts once among the runs and adds
// nothing to the money: the record is marked unreadable, so a row made only of
// such runs reads n/a rather than the 0 a reader would take for a measured
// spend. The count returned is of the first kind alone: a record that exists
// and cannot be read is worth warning about, and a run that has not written one
// yet is routine and says nothing about the money a reader can trust.
func modeRecords(objs []fcli.RunRecord, to int64) ([]report.Rec, int) {
	var recs []report.Rec
	var corrupt int
	for _, o := range objs {
		t, err := runstore.RunTime(o.ID())
		if err != nil || budget.Day(t) > to {
			continue
		}
		// Only the fields the by-mode grouping reads: it groups the run's own
		// record, not a repository's day.
		rec := report.Rec{DayRecord: budget.DayRecord{Date: budget.DayDate(budget.Day(t)), Runs: 1}}
		if c := o.Record(); c != nil {
			if c.Rigor != nil {
				rec.Mode = string(c.Rigor.Mode)
			}
			if c.Cost != nil {
				fcli.ApplyCost(&rec.DayRecord, *c.Cost)
			}
		} else {
			rec.Unreadable = true
			if o.RecordCorrupt() {
				corrupt++
			}
		}
		recs = append(recs, rec)
	}
	return recs, corrupt
}

// ------------------------------------------------------------- degraded

// reportDegraded says history is not enabled and offers totals from the run
// records (ls's loader), labelled as such.
func reportDegraded(ctx context.Context, cmd *cobra.Command, o *reportOptions, cloud *fcli.Cloud, dim report.Dim, m report.Meta, why string, from, to int64, d Deps) error {
	m.Degraded, m.Source = true, "run-records"
	m.Notice = "spend history is not enabled: " + why + ". Showing totals from run records (the runs bucket), not budget history: figures are each run's own result, finished and failed runs alike, and have no per-day archive"
	env, err := fcli.OpenFromConfig(ctx, cloud.Config())
	if err != nil {
		return err
	}
	defer env.Close()
	errw := cmd.ErrOrStderr()
	slugs, err := env.LsSlugs(ctx, o.repo, false, errw)
	if err != nil {
		return err
	}
	rows, err := env.LoadRows(ctx, slugs, time.UnixMilli(from*86_400_000).UTC(), errw, d.Now())
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
		recs = append(recs, report.Rec{DayRecord: fcli.RecordOfRun(r)})
	}
	// The compute figures above are estimates, and these rows carry the
	// price table each came from. The report has no per-row column for it
	// (design compute-backend.md §5.2, C17: the figure's shape is Cloud
	// Run's, so the report and the history need no new column), so the
	// source is said once here -- which is where a reader learns that a VM
	// backend's estimate leaves the VM's boot disk and Cloud NAT out.
	for _, src := range fcli.ComputeSources(rows) {
		m.Warnings = append(m.Warnings, "compute_usd is an estimate priced from "+src)
	}
	rep := report.Build(recs, dim)
	rep.NoHours = true
	return reportEmit(cmd, o, rep, m)
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
