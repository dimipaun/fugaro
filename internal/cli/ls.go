package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// lsWorkers is how many runs' objects are read at once.
const lsWorkers = 8

type lsOptions struct {
	cloud                    cloudOptions
	repo, since, batch       string
	all, mine, watch, asJSON bool
	interval                 time.Duration
	pr                       int // only the runs on this pull request, when set
}

// prLookback is ls --pr's default --since: the runs bucket's lifecycle
// age, so every run the bucket still holds is listed.
const prLookback = "90d"

// lsFilter selects the runs loadRows reads.
type lsFilter struct {
	slugs  []string
	since  time.Time // zero means no bound
	mine   string    // keep runs requested by this identity, when set
	batch  string    // keep runs of this batch, when set
	pr     int       // keep runs on this pull request (task or record), when set
	runRef string    // "<slug>/<run-id>": only this run (diagnose)
	warn   io.Writer // where non-fatal problems go; nil discards them
}

// lsDoc is ls --json's document.
type lsDoc struct {
	Runs   []runview.Row  `json:"runs"`
	Totals runview.Totals `json:"totals"`
	// Warnings are the lines a human listing prints before its table.
	Warnings []string `json:"warnings"`
}

func newLsCmd() *cobra.Command {
	var o lsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List runs with their status, stage, cost and PR",
		Long: "List runs, newest first, joining the runs bucket with what the backend knows.\n\n" +
			"By default ls shows the last 7 days of the local config's repositories (every\n" +
			"repository in the bucket when the config lists none). A run shows as succeeded\n" +
			"only when its result.json says so; an execution that ended without finalizing\n" +
			"is an infra_error.\n\n" +
			"--pr N lists the runs on pull request N of one repository (the first run and\n" +
			"its follow-ups), over the last " + prLookback + " unless --since says otherwise; the totals\n" +
			"line is then the PR's total cost.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runLs(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.BoolVar(&o.mine, "mine", false, "only runs you requested")
	f.StringVar(&o.since, "since", "7d", "only runs started within this long (7d, 36h, 90m; 0 for all)")
	f.StringVar(&o.batch, "batch", "", "only runs of this batch")
	f.IntVar(&o.pr, "pr", 0, "only runs on this pull request (needs one repository; --since defaults to "+prLookback+")")
	f.BoolVar(&o.watch, "watch", false, "redraw until every run has settled")
	f.DurationVar(&o.interval, "interval", 10*time.Second, "--watch's redraw interval")
	_ = f.MarkHidden("interval")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runLs(cmd *cobra.Command, o *lsOptions) error {
	ctx := cmd.Context()
	if o.repo != "" && o.all {
		return userErr("--repo and --all don't go together")
	}
	prSet := cmd.Flags().Changed("pr")
	if prSet && o.pr <= 0 {
		return userErr("--pr %d: use a pull request number", o.pr)
	}
	if prSet && o.all {
		return userErr("ls --pr needs --repo: a pull request number means something only in one repository")
	}
	if prSet && !cmd.Flags().Changed("since") {
		o.since = prLookback
	}
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	if o.watch && o.interval <= 0 {
		return userErr("--interval must be positive")
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	f := lsFilter{batch: o.batch, pr: o.pr, warn: cmd.ErrOrStderr()}
	if prSet && o.repo == "" && len(env.lc.Repos) != 1 {
		return userErr("ls --pr needs --repo: the local config lists %d repositories", len(env.lc.Repos))
	}
	if f.slugs, err = lsSlugs(ctx, env, o, cmd.ErrOrStderr()); err != nil {
		return err
	}
	if prSet && len(f.slugs) != 1 {
		return userErr("ls --pr needs --repo: %d repositories to list", len(f.slugs))
	}
	if o.mine {
		if f.mine, err = env.lc.Me(ctx); err != nil {
			return userErr("--mine: %v", err)
		}
	}
	out := cmd.OutOrStdout()
	clear := o.watch && !o.asJSON && isTTY(out)
	// The image warnings are read once: they change about daily, and
	// --watch would otherwise read them again on every redraw.
	warnings := imageWarnings(ctx, env, f.slugs, time.Now().UTC())
	for {
		now := time.Now()
		if since > 0 {
			f.since = now.Add(-since)
		}
		rows, err := loadRows(ctx, env, f, now)
		if err != nil {
			return err
		}
		if clear {
			fmt.Fprint(out, "\x1b[H\x1b[2J")
		}
		if err := printRows(out, rows, warnings, now, o.asJSON); err != nil {
			return err
		}
		if !o.watch || allSettled(rows) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.interval):
		}
	}
}

// lsSlugs are the repositories ls reads: --repo's, --all's (every slug in
// the bucket), else the local config's, else every slug.
func lsSlugs(ctx context.Context, env *cloudEnv, o *lsOptions, warn io.Writer) ([]string, error) {
	if o.repo != "" {
		slug, err := env.repoSlug(o.repo, checkoutOf(ctx, o.repo))
		if err != nil {
			return nil, err
		}
		return []string{slug}, nil
	}
	if !o.all && len(env.lc.Repos) > 0 {
		var slugs []string
		for name, r := range env.lc.Repos {
			// An entry without a provider takes it from this checkout's
			// fugaro.yaml when the checkout is that repository (§5.4).
			checkout := func() *config.Config { return nil }
			if r.Provider == "" {
				checkout = checkoutOf(ctx, name)
			}
			slug, err := env.repoSlug(name, checkout)
			if err != nil {
				fmt.Fprintf(warn, "warning: skipping %s: %v\n", name, err)
				continue
			}
			slugs = append(slugs, slug)
		}
		slices.Sort(slugs)
		return slices.Compact(slugs), nil
	}
	slugs, err := runstore.ListSlugs(ctx, env.bucket.Bucket)
	if err != nil {
		return nil, remote(err)
	}
	return slugs, nil
}

// loadRows reads every run f selects, joins it with its execution, and
// returns the rows newest first. A run whose launch.json or result.json is
// corrupt, or names an execution that isn't the run's, is an error row
// with a warning. Any other failed read but absence fails the whole
// listing: ls never shows a status guessed from a partial read.
//
// It reads every run's objects first, keeps the runs f.pr selects, and
// only then asks the backend about executions, so a PR's listing costs
// no backend call for the runs off it.
func loadRows(ctx context.Context, env *cloudEnv, f lsFilter, now time.Time) ([]runview.Row, error) {
	warn := f.warn
	if warn == nil {
		warn = io.Discard
	}
	type ref struct{ slug, id string }
	var refs []ref
	if f.runRef != "" {
		slug, id, err := parseRunRef(f.runRef)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref{slug, id})
	} else {
		for _, slug := range f.slugs {
			ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, f.since)
			if err != nil {
				return nil, remote(err)
			}
			for _, id := range ids {
				refs = append(refs, ref{slug, id})
			}
		}
	}

	// The bucket's objects, for every run.
	runs := make([]runObjects, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		runs[i], errs[i] = readRun(ctx, env, refs[i].slug, refs[i].id)
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}
	if f.pr != 0 {
		runs = slices.DeleteFunc(runs, func(r runObjects) bool { return !onPR(r.in, f.pr) })
	}
	for _, r := range runs {
		if r.warning != "" {
			fmt.Fprint(warn, multiLine(r.warning))
		}
	}
	if len(runs) == 0 {
		return []runview.Row{}, nil
	}

	// One listing covers most runs' executions; it reaches an hour further
	// back than the runs, since a run ID is minted before its launch only
	// by moments, but clocks differ.
	execs := map[string]backend.Execution{}
	if f.runRef == "" && slices.ContainsFunc(runs, func(r runObjects) bool { return r.execution != "" }) {
		lf := backend.ListFilter{}
		if !f.since.IsZero() {
			lf.Since = f.since.Add(-time.Hour)
		}
		list, err := env.be.List(ctx, lf)
		if err != nil {
			return nil, remote(fmt.Errorf("listing executions: %w", err))
		}
		for _, e := range list {
			if id, ok := backend.ParseExecution(e.Name); ok {
				execs[id.Key()] = e
			}
		}
	}

	prices := env.prices()
	rows := make([]runview.Row, len(runs))
	errs = make([]error, len(runs))
	var mu sync.Mutex // guards warn
	warnf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(warn, multiLine(fmt.Sprintf(format, args...)))
	}
	parallel(len(runs), func(i int) {
		if errs[i] = followExecution(ctx, env, &runs[i], execs, warnf); errs[i] == nil {
			rows[i] = runview.Join(runs[i].in, prices, now)
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}

	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return (f.mine != "" && r.RequestedBy != f.mine) || (f.batch != "" && r.Batch != f.batch)
	})
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Run, b.Run)
	})
	return rows, nil
}

// parallel calls fn(0) … fn(n-1) on up to lsWorkers goroutines.
func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(lsWorkers, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

// onPR reports whether a run is on pull request pr: its task continues
// it, or its record names it. An error row whose task says so is kept.
func onPR(in runview.Input, pr int) bool {
	return (in.Task != nil && in.Task.PR == pr) || (in.Record != nil && in.Record.PR != nil && in.Record.PR.Number == pr)
}

// runObjects is what the bucket holds about one run, before its
// execution is joined.
type runObjects struct {
	in        runview.Input
	store     *runstore.Store
	execution string // the execution to follow; "" when there is none
	warning   string // printed only when the run is listed
}

// readRun reads what the bucket knows about one run; it makes no backend
// call.
func readRun(ctx context.Context, env *cloudEnv, slug, id string) (runObjects, error) {
	s := runstore.Open(env.bucket.Bucket, slug, id)
	r := runObjects{in: runview.Input{Slug: slug, RunID: id}, store: s}
	in := &r.in
	problem := func(p string) {
		in.Problem = p
		r.warning = fmt.Sprintf("warning: run %s/%s: %s\n", slug, id, p)
	}
	raw, err := s.ReadFile(ctx, "task.json")
	switch {
	case err == nil:
		in.Task, _ = task.Parse(raw) // unparseable: the row says task.json unreadable
	case !errors.Is(err, runstore.ErrNotFound):
		return r, err
	}
	// A corrupt object is this run's error row, with a warning; a read that
	// fails otherwise (the bucket, the network) still fails the listing.
	for _, read := range []struct {
		name string
		do   func() error
	}{
		{"launch.json", func() (err error) { in.Launch, err = absent(s.ReadLaunch(ctx)); return err }},
		{"result.json", func() (err error) { in.Record, err = absent(s.ReadRecord(ctx)); return err }},
	} {
		if err := read.do(); corruptObject(err) {
			problem(read.name + " is unreadable: " + err.Error())
			return r, nil
		} else if err != nil {
			return r, err
		}
	}
	if in.Launch == nil && in.Record == nil {
		if in.CancelMarker, err = s.CancelRequested(ctx); err != nil {
			return r, fmt.Errorf("checking %s/%s's cancel marker: %w", slug, id, err)
		}
		if in.Claim, err = absent(s.ReadClaim(ctx)); corruptObject(err) {
			problem("the launch claim is unreadable: " + err.Error())
		} else if err != nil {
			return r, err
		}
		return r, nil
	}
	// The record names the execution that owns the run; launch.json may
	// name a duplicate after a double launch.
	if in.Record != nil {
		r.execution = in.Record.Execution
	}
	if r.execution == "" && in.Launch != nil {
		r.execution = in.Launch.Execution
	}
	return r, nil
}

// followExecution joins r with its execution, from execs (the listing)
// or, when the listing lacks it, from the backend.
func followExecution(ctx context.Context, env *cloudEnv, r *runObjects, execs map[string]backend.Execution, warnf func(string, ...any)) error {
	in, s, name := &r.in, r.store, r.execution
	if name == "" || in.Problem != "" {
		return nil
	}
	// The run's service account can write both objects: follow only an
	// execution of the run's own job.
	if err := env.checkExecution(name, in.Slug, in.Task); err != nil {
		in.Problem = "not following its execution: " + err.Error()
		warnf("warning: run %s/%s: %s\n", in.Slug, in.RunID, in.Problem)
		return nil
	}
	eid, _ := backend.ParseExecution(name)
	if e, ok := execs[eid.Key()]; ok {
		in.Exec = &e
		return nil
	}
	// Outside the listing's window, or not listed yet: ask for it alone.
	e, err := env.be.Execution(ctx, name)
	switch {
	case err == nil:
		in.Exec = &e
	case !errors.Is(err, backend.ErrNotFound):
		return fmt.Errorf("run %s/%s: %w", in.Slug, in.RunID, err)
	}
	// The record was read before the execution: a runner that wrote its
	// final record and exited in between would look like one that died
	// without finalizing. Once the execution has ended (or is gone), read
	// the record again, so it is as new as the execution.
	if in.Record != nil && in.Record.Status == runstore.StatusRunning && (in.Exec == nil || in.Exec.State.Terminal()) {
		if in.Record, err = absent(s.ReadRecord(ctx)); err != nil {
			return err
		}
	}
	return nil
}

// corruptObject reports whether err is a run object that can't be used:
// undecodable, or past its read cap (runstore.ErrTooLarge,
// blobx.ErrTooLarge). That is the run's problem, not the listing's.
func corruptObject(err error) bool {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, runstore.ErrTooLarge) || errors.Is(err, blobx.ErrTooLarge)
}

// absent turns runstore.ErrNotFound into a nil value and no error.
func absent[T any](v *T, err error) (*T, error) {
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

// parseSince parses --since: a positive number of days (7d), a positive Go
// duration (36h, 90m), or 0 for no bound.
func parseSince(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 1 {
			return 0, fmt.Errorf("--since %q: use a positive number of days (7d) or a Go duration (36h)", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--since %q: use a positive number of days (7d) or a Go duration (36h)", s)
	}
	return d, nil
}

func allSettled(rows []runview.Row) bool {
	for _, r := range rows {
		if !r.Settled {
			return false
		}
	}
	return true
}

// printRows prints rows as a table with a totals line, or as one JSON
// document. The warnings come before the table, or go into the document.
func printRows(w io.Writer, rows []runview.Row, warnings []string, now time.Time, asJSON bool) error {
	tot := runview.Sum(rows)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(lsDoc{Runs: rows, Totals: tot, Warnings: warnings})
	}
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tSTATUS\tSTAGE\tAGE\tCOST\tPR")
	for _, r := range rows {
		cost := fmt.Sprintf("$%.2f", r.Cost.TotalUSD)
		if r.Cost.ModelBasis == runstore.BasisSubscription && r.Cost.ModelUSD > 0 {
			cost += "~" // the model spend was notional
		}
		if !r.Cost.ComputeEstimated {
			cost += ", compute not estimated" // never "free" (design §10.1)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(r.Run), oneLine(r.Status), oneLine(r.Stage), age(now.Sub(r.Created)), cost, prColumn(r))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	noun := "runs"
	if tot.Runs == 1 {
		noun = "run"
	}
	line := fmt.Sprintf("%d %s · ≈ $%.2f billed (model $%.2f + compute $%.2f)", tot.Runs, noun, tot.TotalUSD, tot.ModelUSD, tot.ComputeUSD)
	if tot.ModelNotionalUSD > 0 {
		line += fmt.Sprintf(" · $%.2f model notional (subscription)", tot.ModelNotionalUSD)
	}
	if n := tot.ComputeNotEstimated; n > 0 {
		noun := "runs"
		if n == 1 {
			noun = "run"
		}
		line += fmt.Sprintf(" · compute not estimated for %d %s", n, noun)
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// prColumn is a row's pull request: "#N <url>", "#N" while the URL is
// unknown (a follow-up before it starts), else the URL alone.
func prColumn(r runview.Row) string {
	switch {
	case r.PR > 0 && r.PRURL != "":
		return fmt.Sprintf("#%d %s", r.PR, oneLine(r.PRURL))
	case r.PR > 0:
		return fmt.Sprintf("#%d", r.PR)
	}
	return oneLine(r.PRURL)
}

// age rounds d to minutes, hours or days.
func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(0, int(d/time.Minute)))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// isTTY reports whether w is a terminal.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
