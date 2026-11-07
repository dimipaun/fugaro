package cli

// fugaro runs summary: the run counts and the spend of one window, read from
// the budget database's day nodes (spend, outcomes, runs) and from the spend
// history's daily documents, and from no run object at all. It is the reader
// the console's Project, Coordinator and Spend tabs paint their totals from,
// so it opens neither the runs bucket nor the compute backend: a bucket read
// per run is what makes a listing slow, and a summary has no run to read.
//
// It is a command of its own rather than a field of runs ls --json because the
// two answer different questions from different sources. runs ls reads the
// runs bucket and lists runs; this reads the day counters the caps are
// enforced against and counts a run on the day the budget recorded it. Two
// totals blocks under one name in one document would leave a reader unable to
// tell which basis a figure has, which is what the wire contract forbids.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// summaryDocVersion is the document's version. The document is additive within
// it: a field is added, never retyped or given another meaning (D784).
const summaryDocVersion = 1

// summaryDefaultSince is the command's window: the seven days runs ls lists,
// which is the budget database's own live window (budget.PruneAfter days plus
// today) and what the database can answer without the history at all.
const summaryDefaultSince = "7d"

// summaryMaxDays bounds the window. A summary is per UTC day, so the window's
// days are enumerated, and the spend history is not built for a longer window
// either (report's own bound).
const summaryMaxDays = 3660

// summaryStateOther is the bucket every status outside summaryStates falls in.
const summaryStateOther = "other"

// summaryStates is the state vocabulary of the document's counts, in the order
// the table prints them: the statuses the budget database's outcome nodes
// carry, plus "other" for anything else a node or a document holds (a status
// written by another release). It is closed, so every count is present and a
// consumer reads one without a nil check.
var summaryStates = []string{
	string(runstore.StatusSucceeded), string(runstore.StatusFailed), string(runstore.StatusHalted),
	string(runstore.StatusCancelled), string(runstore.StatusInfraError), summaryStateOther,
}

// summaryOptions are the command's flags.
type summaryOptions struct {
	cloud       cloudOptions
	repo, since string
	asJSON      bool
}

// summaryRepo is one repository's row of the document. Money is integer
// micro-dollars, the unit of both sources: nothing is rounded on the way out.
type summaryRepo struct {
	// Slug is the storage key the sources hold the repository under; Repo is
	// the display name (owner/name) a day record carries, which falls back to
	// the slug for a repository whose name nothing recorded.
	Slug string `json:"slug"`
	Repo string `json:"repo"`
	// Runs counts the runs of the window. States counts them by how they
	// ended, so a run the sources have not settled yet is in Runs and in no
	// state: Runs is at least the sum of States. Neither is ever negative: a
	// source figure below zero is not a count and is added as nothing, so the
	// document the console reads carries the figure the table prints.
	Runs   int64            `json:"runs"`
	States map[string]int64 `json:"states"`
	// SpentMicros is the model money the caps count, NotionalMicros the part
	// of it a subscription paid (counted toward the plan's limits, nobody's
	// bill). The two are never summed.
	SpentMicros    int64 `json:"spent_micros"`
	NotionalMicros int64 `json:"notional_micros"`
	// ComputeMicros is what the runs whose compute was priced came to, and is
	// null when no run's compute is known: "not estimated" is not "free".
	// ComputeRuns says how many runs the figure covers, so a reader can see
	// whether it covers the window's runs or a part of them.
	ComputeMicros *int64 `json:"compute_micros"`
	ComputeRuns   int64  `json:"compute_runs"`
}

// summaryTotals is the same figures over every repository of the window. Its
// counts are derived the same way and are never negative either.
type summaryTotals struct {
	Runs           int64            `json:"runs"`
	States         map[string]int64 `json:"states"`
	SpentMicros    int64            `json:"spent_micros"`
	NotionalMicros int64            `json:"notional_micros"`
	ComputeMicros  *int64           `json:"compute_micros"`
	ComputeRuns    int64            `json:"compute_runs"`
}

// summaryDoc is fugaro runs summary --json's document. Every member is written
// on every run, the arrays empty included, so a consumer reads one without a
// nil check.
type summaryDoc struct {
	V       int    `json:"v"`
	Project string `json:"project"`
	// Since and Until are the window's own UTC dates (YYYY-MM-DD). Since is
	// always a date: a summary is per day, so a window with no lower bound is
	// refused rather than answered.
	Since string `json:"since"`
	Until string `json:"until"`
	// Repositories is one row per repository either source named, sorted by
	// slug; Totals is the same figures over all of them.
	Repositories []summaryRepo `json:"repositories"`
	Totals       summaryTotals `json:"totals"`
	// DaysUnknown names the window's UTC days that neither source answered
	// for. Their runs and their spend are unknown and are counted nowhere:
	// zero would be a figure, and nothing here is one.
	DaysUnknown []string `json:"days_unknown"`
	// Notes are the lines a human table prints beside its table: what the
	// figures count, and what the read could not cover.
	Notes []string `json:"notes"`
}

func newRunsSummaryCmd() *cobra.Command {
	var o summaryOptions
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Count the window's runs and their spend, without reading a run object",
		Long: "Count the runs of a window by repository and by state, and total what they\n" +
			"spent, from the budget database and the spend history alone.\n\n" +
			"The figures come from the day counters the caps are enforced against: the\n" +
			"budget database's spend, outcomes and runs nodes for the days it still holds,\n" +
			"and the spend history's daily documents for the days the rollover has\n" +
			"archived. No run object is read, so no run's task, result or transcript is\n" +
			"opened and the runs bucket is never listed: this is the read a listing cannot\n" +
			"be, and it answers in about a second over the database's own window.\n\n" +
			"A run is counted on the UTC day the database recorded its spend, which is the\n" +
			"day the rollover files it under, so the window and the counts agree with the\n" +
			"budget and not necessarily with runs ls, whose rows are the runs bucket's.\n" +
			"Runs that have not settled are counted in runs and in no state. SPENT is the\n" +
			"model money the caps count; NOTIONAL is the part a subscription paid, which\n" +
			"counts toward the plan's limits and is nobody's bill. The two are separate and\n" +
			"are never summed. COMPUTE is what the runs whose compute was priced came to,\n" +
			"and is n/a when no run's compute is known: this read opens no bucket, so a live\n" +
			"day's compute is never estimated here, and 'not estimated' is never shown as\n" +
			"free.\n\n" +
			"An unknown day is named, never counted as zero. The budget database holds the\n" +
			"days it has not pruned (today and the " + strconv.Itoa(budget.PruneAfter) + " before it); every older day is\n" +
			"the spend history's to answer, and a project whose history cannot answer at all\n" +
			"-- no Firebase backend, no database, no history mark -- names the days it\n" +
			"cannot know in days_unknown and counts nothing on them. A day inside the\n" +
			"database's window with no node is a day nothing ran on, which is known.\n\n" +
			"A window is a span of UTC days ending today: --since 7d is today and the six\n" +
			"days before it. The whole history is not a window, so --since 0 is refused.\n\n" +
			"--json prints {v, project, since, until, repositories[], totals, days_unknown,\n" +
			"notes}; repositories and totals carry {slug, repo, runs, states{}, spent_micros,\n" +
			"notional_micros, compute_micros, compute_runs}, and states carries all of\n" +
			"succeeded, failed, halted, cancelled, infra_error and other.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRunsSummary(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.StringVar(&o.since, "since", summaryDefaultSince, "the window of UTC days ending today (7d, 36h, 90m; a summary is per day, so 0 is refused)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runRunsSummary(cmd *cobra.Command, o *summaryOptions) error {
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	if since <= 0 {
		return userErr("--since %s: a summary is per UTC day, so the window needs a lower bound; pass --since 7d, or --since 365d for the last year", safetext.Strip(o.since))
	}
	days := int(since / (24 * time.Hour))
	if days < 1 {
		days = 1 // a window shorter than a day is today
	}
	if days > summaryMaxDays {
		return userErr("--since %s: the window is %d days, and a summary covers at most %d", safetext.Strip(o.since), days, summaryMaxDays)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if o.repo, err = repoArg(ctx, o.repo); err != nil {
		return err
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
	today := budget.Day(budgetNow().UTC())
	to := today
	from := to - int64(days) + 1
	read, err := readSummary(ctx, lc, wantSlug, from, to, today, func(s string) {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+safetext.Strip(s))
	})
	if err != nil {
		return err
	}
	return writeSummary(cmd.OutOrStdout(), summaryDocument(lc.Name, from, to, read), o.asJSON)
}

// summaryRead is one read of the window: every day record either source
// answered with, the days neither did, and the lines saying what the read
// could not cover.
type summaryRead struct {
	recs []budget.DayRecord
	// days are the window's days neither source answered for, ascending.
	days []int64
	// answered is how many days of the window a source answered for.
	answered int
	notes    []string
}

// readSummary reads the window from the two sources: the spend history first
// (the archived days, and the mark that says the database is this project's),
// then the budget database for the days it still holds that the history has
// not finalized.
func readSummary(ctx context.Context, lc *localcfg.Config, wantSlug string, from, to, today int64, warn func(string)) (summaryRead, error) {
	out := summaryRead{}
	history, final, historyOK, historyWhy, err := summaryHistory(ctx, lc, from, to, warn)
	if err != nil {
		return out, err
	}
	// The days the database still holds: today and the budget.PruneAfter days
	// before it. A day outside them may have been pruned, so it is the
	// history's alone to answer for.
	liveStart := max(from, today-budget.PruneAfter)
	live, liveErr := map[int64][]budget.DayRecord{}, error(nil)
	if liveStart <= to {
		var days []int64
		for d := liveStart; d <= to; d++ {
			if !final[d] {
				days = append(days, d)
			}
		}
		if len(days) > 0 {
			live, liveErr = summaryLive(ctx, lc, days, warn)
		}
	}
	liveWhy := ""
	if liveErr != nil {
		live, liveWhy = map[int64][]budget.DayRecord{}, liveErr.Error()
	}
	for d := from; d <= to; d++ {
		switch {
		case len(live[d]) > 0: // fresher than a document of the same day
			out.recs, out.answered = append(out.recs, live[d]...), out.answered+1
		case len(history[d]) > 0:
			out.recs, out.answered = append(out.recs, history[d]...), out.answered+1
		case d >= liveStart && liveErr == nil:
			out.answered++ // nothing ran on it: the database is not pruned inside its window
		case d < liveStart && historyOK:
			out.answered++ // nothing ran on it: the rollover archives the days it wrote a document for
		default:
			out.days = append(out.days, d)
		}
	}
	if wantSlug != "" {
		out.recs = slices.DeleteFunc(out.recs, func(r budget.DayRecord) bool { return r.Slug != wantSlug })
	}
	if out.answered == 0 {
		// Nothing at all is known about the window, and a document of zeros
		// would read as "nothing ran".
		if liveErr != nil {
			return out, liveErr
		}
		return out, userErr("nothing of the window could be read: %s", historyWhy)
	}
	out.notes = summaryNotes(from, to, out.days, historyWhy, liveWhy)
	return out, nil
}

// summaryHistory reads the archived days: the spend history's daily documents
// of the window, and the days it has finalized. ok is false, with why saying
// so, when the history cannot answer at all -- no Firebase backend, no
// database, no history mark -- which is a state a project may be in and not a
// failure; a failure of a history that is there is an error.
//
// It is the same read as fugaro report's history half, which lives in ee/ and
// cannot be reached from here (D444). The two must agree, so the figures here
// are pinned against the same window in the tests.
func summaryHistory(ctx context.Context, lc *localcfg.Config, from, to int64, warn func(string)) (recs map[int64][]budget.DayRecord, final map[int64]bool, ok bool, why string, err error) {
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, nil, false, "project " + lc.Name + " has no Firebase budget backend, so it has no spend history", nil
	}
	fs, err := newReportFirestore(ctx, lc)
	if err != nil {
		return nil, nil, false, "", err
	}
	// The mark first: the database must be this project's history.
	doc, err := fs.Get(ctx, budget.MetaCollection, budget.MarkDocument)
	switch {
	case errors.Is(err, firestore.ErrNoDatabase):
		return nil, nil, false, "the project's Firebase project has no Firestore database", nil
	case errors.Is(err, firestore.ErrNotFound):
		return nil, nil, false, "the project's Firestore database has no history mark (fugaro update --firebase-project has not set it up)", nil
	case err != nil:
		return nil, nil, false, "", firestoreErr(lc, err)
	}
	if p, _ := doc.Fields["project"].(string); p != lc.Name {
		return nil, nil, false, "", userErr("the Firestore database of Firebase project %s belongs to project %q, not %q; fix budget.firebase_project in project %s's config",
			lc.Budget.FirebaseProject, safetext.Strip(p), lc.Name, lc.Name)
	}
	docs, err := fs.Query(ctx, budget.SpendCollection, "date", budget.DayDate(from), budget.DayDate(to))
	if err != nil {
		return nil, nil, false, "", firestoreErr(lc, err)
	}
	recs, final = map[int64][]budget.DayRecord{}, map[int64]bool{}
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
		recs[day] = append(recs[day], r)
		if r.Final {
			final[day] = true
		}
	}
	if bad > 0 {
		warn(fmt.Sprintf("%d spend-history document(s) could not be read and are left out of the totals", bad))
	}
	return recs, final, true, "", nil
}

// summaryLive computes the days the database still holds with the database's
// own rollup, the same reader the rollover and fugaro report use. facts are
// nil: this read opens no runs bucket, so no run's compute is priced here and
// a live day's compute reads as not estimated rather than as nothing.
func summaryLive(ctx context.Context, lc *localcfg.Config, days []int64, warn func(string)) (map[int64][]budget.DayRecord, error) {
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	// The database must be this project's own, the check every other reader of
	// it makes: a budget.rtdb_url pointing at another project's database would
	// have this read print that project's counts and spend under this project's
	// name, which is a wrong answer rather than a missing one.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, err
	}
	recs, err := budget.LiveDays(ctx, db, nil, days, warn)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	return recs, nil
}

// summaryNotes is the document's own account of itself: what the figures
// count, and what the read could not cover.
func summaryNotes(from, to int64, unknown []int64, historyWhy, liveWhy string) []string {
	notes := []string{
		"the figures are the budget database's own day counters and the spend history's daily documents; no run object was read",
		"a run is counted on the UTC day the budget recorded its spend, so a count can differ from runs ls, whose rows are the runs bucket's",
	}
	if historyWhy != "" {
		notes = append(notes, "the spend history answered nothing ("+historyWhy+"), so the days older than the database's window are unknown")
	}
	if liveWhy != "" {
		notes = append(notes, "the budget database answered nothing ("+liveWhy+"), so the days it still holds are unknown")
	}
	if len(unknown) > 0 {
		notes = append(notes, fmt.Sprintf("%d of the window's %d days (%s to %s) were answered by neither source and are counted nowhere: %s to %s",
			len(unknown), to-from+1, budget.DayDate(from), budget.DayDate(to),
			budget.DayDate(unknown[0]), budget.DayDate(unknown[len(unknown)-1])))
	}
	return notes
}

// summaryAgg is one repository's figures while the window's records are folded
// into it.
type summaryAgg struct {
	repo        string
	runs        int64
	states      map[string]int64
	spent       budget.Micros
	notional    budget.Micros
	compute     budget.Micros
	computeRuns int64
}

func newSummaryAgg() *summaryAgg {
	a := &summaryAgg{states: map[string]int64{}}
	for _, s := range summaryStates {
		a.states[s] = 0
	}
	return a
}

// summaryDocument folds the window's day records into the document. The days
// arrive one at a time and a repository has one record per day, so the days are
// added together here.
func summaryDocument(project string, from, to int64, read summaryRead) summaryDoc {
	bySlug := map[string]*summaryAgg{}
	for _, r := range read.recs {
		a := bySlug[r.Slug]
		if a == nil {
			a = newSummaryAgg()
			bySlug[r.Slug] = a
		}
		if a.repo == "" {
			a.repo = r.Repo
		}
		a.runs += max(r.Runs, 0)
		for k, n := range r.Outcomes {
			// A count below zero is not a count -- a window's nodes can
			// disagree -- so it counts as nothing, as the money is added
			// (summaryAdd) and as the database's rollup clamps its counters.
			a.states[summaryState(k)] += max(n, 0)
		}
		a.spent, a.notional = summaryAdd(a.spent, r.SpentMicros), summaryAdd(a.notional, r.NotionalMicros)
		if r.ComputeEstimatedRuns > 0 {
			a.compute, a.computeRuns = summaryAdd(a.compute, r.ComputeMicros), a.computeRuns+r.ComputeEstimatedRuns
		}
	}
	doc := summaryDoc{
		V: summaryDocVersion, Project: project, Since: budget.DayDate(from), Until: budget.DayDate(to),
		Repositories: []summaryRepo{}, DaysUnknown: []string{}, Notes: read.notes,
	}
	slugs := make([]string, 0, len(bySlug))
	for slug := range bySlug {
		slugs = append(slugs, slug)
	}
	slices.Sort(slugs)
	total := newSummaryAgg()
	for _, slug := range slugs {
		a := bySlug[slug]
		repo := summaryRepo{Slug: slug, Repo: a.repo, Runs: a.runs, States: a.states,
			SpentMicros: int64(a.spent), NotionalMicros: int64(a.notional),
			ComputeMicros: summaryCompute(a.compute, a.computeRuns, a.runs), ComputeRuns: a.computeRuns}
		if repo.Repo == "" {
			repo.Repo = slug
		}
		doc.Repositories = append(doc.Repositories, repo)
		total.runs += a.runs
		for s, n := range a.states {
			total.states[s] += max(n, 0)
		}
		total.spent, total.notional = summaryAdd(total.spent, a.spent), summaryAdd(total.notional, a.notional)
		total.compute, total.computeRuns = summaryAdd(total.compute, a.compute), total.computeRuns+a.computeRuns
	}
	doc.Totals = summaryTotals{Runs: total.runs, States: total.states,
		SpentMicros: int64(total.spent), NotionalMicros: int64(total.notional),
		ComputeMicros: summaryCompute(total.compute, total.computeRuns, total.runs), ComputeRuns: total.computeRuns}
	for _, d := range read.days {
		doc.DaysUnknown = append(doc.DaysUnknown, budget.DayDate(d))
	}
	return doc
}

// summaryAdd adds two micro-dollar figures: a negative figure counts as
// nothing, and a sum past the largest figure the integer holds saturates there
// rather than wrapping into a negative one a reader would take for a real
// figure. The budget database's own rollup sums its counters the same way
// (budget's satAdd).
func summaryAdd(a, b budget.Micros) budget.Micros {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// summaryCompute is a row's compute figure: null when the row has runs and no
// run's compute was priced -- "not estimated" is not "free" -- the sum
// otherwise. A row with no run at all is zero, which is a fact: nothing ran.
func summaryCompute(compute budget.Micros, computeRuns, runs int64) *int64 {
	if computeRuns == 0 && runs > 0 {
		return nil
	}
	v := int64(compute)
	return &v
}

// summaryState maps an outcome status onto the document's closed vocabulary:
// anything outside it is "other", so a status another release wrote is visible
// rather than dropped or given a key of its own.
func summaryState(status string) string {
	if status != summaryStateOther && slices.Contains(summaryStates, status) {
		return status
	}
	return summaryStateOther
}

func writeSummary(w io.Writer, doc summaryDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	fmt.Fprintf(w, "project: %s\n", doc.Project)
	fmt.Fprintf(w, "window:  %s to %s\n", doc.Since, doc.Until)
	for _, line := range doc.Notes {
		fmt.Fprintln(w, multiLine(line))
	}
	fmt.Fprintln(w)
	if len(doc.Repositories) == 0 {
		// A window nothing ran in is said plainly, not shown as an empty table
		// a reader could take for a failure.
		fmt.Fprintln(w, "no run and no spend in this window")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "REPOSITORY\tRUNS"
	for _, s := range summaryStates {
		header += "\t" + strings.ToUpper(strings.ReplaceAll(s, "_", " "))
	}
	fmt.Fprintln(tw, header+"\tSPENT\tNOTIONAL\tCOMPUTE")
	line := func(name string, r summaryRepo) string {
		cells := []string{cellOneLine(clipRunes(name, maxNameCellRunes)), strconv.FormatInt(r.Runs, 10)}
		for _, s := range summaryStates {
			cells = append(cells, strconv.FormatInt(r.States[s], 10))
		}
		return strings.Join(append(cells, money(budget.Micros(r.SpentMicros)),
			money(budget.Micros(r.NotionalMicros)), summaryComputeCell(r)), "\t")
	}
	for _, r := range doc.Repositories {
		fmt.Fprintln(tw, line(r.Repo, r))
	}
	fmt.Fprintln(tw, line("TOTAL", summaryRepo{States: doc.Totals.States, Runs: doc.Totals.Runs,
		SpentMicros: doc.Totals.SpentMicros, NotionalMicros: doc.Totals.NotionalMicros,
		ComputeMicros: doc.Totals.ComputeMicros}))
	return tw.Flush()
}

// summaryComputeCell is a row's compute cell: "n/a" when no run's compute is
// known, since "$0.00" would be a figure the read does not have.
func summaryComputeCell(r summaryRepo) string {
	if r.ComputeMicros == nil {
		return "n/a"
	}
	return money(budget.Micros(*r.ComputeMicros))
}
