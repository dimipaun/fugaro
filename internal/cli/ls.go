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
}

// lsFilter selects the runs loadRows reads.
type lsFilter struct {
	slugs  []string
	since  time.Time // zero means no bound
	mine   string    // keep runs requested by this identity, when set
	batch  string    // keep runs of this batch, when set
	runRef string    // "<slug>/<run-id>": only this run (diagnose)
	warn   io.Writer // where non-fatal problems go; nil discards them
}

// lsDoc is ls --json's document.
type lsDoc struct {
	Runs   []runview.Row  `json:"runs"`
	Totals runview.Totals `json:"totals"`
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
			"is an infra_error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runLs(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.BoolVar(&o.mine, "mine", false, "only runs you requested")
	f.StringVar(&o.since, "since", "7d", "only runs started within this long (7d, 36h, 90m; 0 for all)")
	f.StringVar(&o.batch, "batch", "", "only runs of this batch")
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

	f := lsFilter{batch: o.batch, warn: cmd.ErrOrStderr()}
	if f.slugs, err = lsSlugs(ctx, env, o, cmd.ErrOrStderr()); err != nil {
		return err
	}
	if o.mine {
		if f.mine, err = env.lc.Me(ctx); err != nil {
			return userErr("--mine: %v", err)
		}
	}
	out := cmd.OutOrStdout()
	clear := o.watch && !o.asJSON && isTTY(out)
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
		if err := printRows(out, rows, now, o.asJSON); err != nil {
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
	if len(refs) == 0 {
		return []runview.Row{}, nil
	}

	// One listing covers most runs' executions; it reaches an hour further
	// back than the runs, since a run ID is minted before its launch only
	// by moments, but clocks differ.
	execs := map[string]backend.Execution{}
	if f.runRef == "" {
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
	rows := make([]runview.Row, len(refs))
	errs := make([]error, len(refs))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex // guards warn
		next = make(chan int)
	)
	warnf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(warn, multiLine(fmt.Sprintf(format, args...)))
	}
	for range min(lsWorkers, len(refs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				in, err := readRun(ctx, env, refs[i].slug, refs[i].id, execs, warnf)
				if err != nil {
					errs[i] = err
					continue
				}
				rows[i] = runview.Join(in, prices, now)
			}
		}()
	}
	for i := range refs {
		next <- i
	}
	close(next)
	wg.Wait()
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

// readRun gathers what the bucket and the backend know about one run.
func readRun(ctx context.Context, env *cloudEnv, slug, id string, execs map[string]backend.Execution, warnf func(string, ...any)) (runview.Input, error) {
	s := runstore.Open(env.bucket.Bucket, slug, id)
	in := runview.Input{Slug: slug, RunID: id}
	raw, err := s.ReadFile(ctx, "task.json")
	switch {
	case err == nil:
		in.Task, _ = task.Parse(raw) // unparseable: the row says task.json unreadable
	case !errors.Is(err, runstore.ErrNotFound):
		return in, err
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
			in.Problem = read.name + " is unreadable: " + err.Error()
			warnf("warning: run %s/%s: %s\n", slug, id, in.Problem)
			return in, nil
		} else if err != nil {
			return in, err
		}
	}
	if in.Launch == nil && in.Record == nil {
		if in.CancelMarker, err = s.CancelRequested(ctx); err != nil {
			return in, fmt.Errorf("checking %s/%s's cancel marker: %w", slug, id, err)
		}
		if in.Claim, err = absent(s.ReadClaim(ctx)); err != nil {
			return in, err
		}
		return in, nil
	}
	// The record names the execution that owns the run; launch.json may
	// name a duplicate after a double launch (N-2).
	name := ""
	if in.Record != nil {
		name = in.Record.Execution
	}
	if name == "" && in.Launch != nil {
		name = in.Launch.Execution
	}
	if name == "" {
		return in, nil
	}
	// The run's service account can write both objects: follow only an
	// execution of the run's own job (S-I2).
	if err := env.checkExecution(name, slug, in.Task); err != nil {
		in.Problem = "not following its execution: " + err.Error()
		warnf("warning: run %s/%s: %s\n", slug, id, in.Problem)
		return in, nil
	}
	eid, _ := backend.ParseExecution(name)
	if e, ok := execs[eid.Key()]; ok {
		in.Exec = &e
		return in, nil
	}
	// Outside the listing's window, or not listed yet: ask for it alone.
	e, err := env.be.Execution(ctx, name)
	switch {
	case err == nil:
		in.Exec = &e
	case !errors.Is(err, backend.ErrNotFound):
		return in, fmt.Errorf("run %s/%s: %w", slug, id, err)
	}
	// The record was read before the execution: a runner that wrote its
	// final record and exited in between would look like one that died
	// without finalizing. Once the execution has ended (or is gone), read
	// the record again, so it is as new as the execution (C-M1).
	if in.Record != nil && in.Record.Status == runstore.StatusRunning && (in.Exec == nil || in.Exec.State.Terminal()) {
		if in.Record, err = absent(s.ReadRecord(ctx)); err != nil {
			return in, err
		}
	}
	return in, nil
}

// corruptObject reports whether err is a run object that was read but
// can't be decoded: that run's problem, not the listing's.
func corruptObject(err error) bool {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF)
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
// document.
func printRows(w io.Writer, rows []runview.Row, now time.Time, asJSON bool) error {
	tot := runview.Sum(rows)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(lsDoc{Runs: rows, Totals: tot})
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(r.Run), oneLine(r.Status), oneLine(r.Stage), age(now.Sub(r.Created)), cost, oneLine(r.PRURL))
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
