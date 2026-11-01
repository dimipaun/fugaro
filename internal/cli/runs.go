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
	"github.com/dimipaun/fugaro/internal/pool"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runcache"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// lsWorkers is how many runs' objects are read at once. A run is three
// objects, each a round trip of tens of milliseconds, and a listing covers
// every run in the bucket's window: this width is most of what ls costs.
const lsWorkers = 32

// followWorkers is how many runs' executions are followed at once. That read
// leaves the bucket for the compute backend, whose own requests are not the
// ones lsWorkers is for, and only the runs the executions listing did not
// already answer reach it.
const followWorkers = 8

// maxNameCellRunes bounds a name read back from a run's own result.json
// (an OAuth account or a recipe name, neither validated by this binary
// against its own name rule before display) before it reaches a tabwriter
// cell: clipRunes cuts it, then cellOneLine escapes it, so a hostile 10 KB
// name can't blow up a column's width.
const maxNameCellRunes = 64

type lsOptions struct {
	cloud                    cloudOptions
	repo, since, batch       string
	state                    string
	all, mine, watch, asJSON bool
	interval                 time.Duration
	pr                       int  // only the runs on this pull request, when set
	limit                    int  // only the newest N runs of the window, when > 0
	noCache                  bool // read every object from the bucket, as before the cache existed
}

// lsStates are the run states --state accepts, sorted: runstore's own states
// and the ones runview derives for a run that has not recorded an outcome yet.
// They are the vocabulary a row's `status` carries, never the decorated cell
// statusCell prints -- `failed (preempted)` is --state failed, as a reader
// switching on status has always seen it.
var lsStates = []string{
	string(runstore.StatusCancelled), runview.StatusError, string(runstore.StatusFailed),
	string(runstore.StatusHalted), string(runstore.StatusInfraError), runview.StatusLaunching,
	runview.StatusPending, string(runstore.StatusRunning), string(runstore.StatusSucceeded),
	runview.StatusUnlaunched,
}

// prLookback is runs ls --pr's default --since: run --pr's window
// (followUpLookback), the runs bucket's lifecycle age, so every run the
// bucket still holds is listed.
var prLookback = fmt.Sprintf("%dd", int(followUpLookback/(24*time.Hour)))

// lsDefaultWindow is runs ls's default --since as a span, and the two
// spellings of it are built from that one number: lsDefaultSince is what the
// flag takes (parseSince's "Nd" form) and lsDefaultDays is what the help text
// says ("7 days"). Variables, not constants, for prLookback's reason — they
// are derived — but nothing writes 7 or "7d" a second time. The test guard
// (internal/cli: runid_test.go) reads lsDefaultSince, so change the window
// here and the guard's failure message names the new one.
var (
	lsDefaultWindow = 7 * 24 * time.Hour
	lsDefaultDays   = int(lsDefaultWindow / (24 * time.Hour))
	lsDefaultSince  = fmt.Sprintf("%dd", lsDefaultDays)
)

// lsNow is runs ls's clock: --since's window (lsDefaultSince by default) is
// measured back from it, so which runs a listing shows depends on the day it
// runs.
// Tests replace it to pin that window instead of minting run IDs dated
// against the day the test happens to run (internal/cli: runid_test.go).
var lsNow = time.Now

// lsFilter selects the runs loadRows reads.
type lsFilter struct {
	slugs  []string
	since  time.Time // zero means no bound
	limit  int       // read the newest N runs only, when > 0
	mine   string    // keep runs requested by this identity, when set
	batch  string    // keep runs of this batch, when set
	state  string    // keep runs in this state, when set
	pr     int       // keep runs on this pull request (task or record), when set
	runRef string    // "<slug>/<run-id>": only this run (diagnose)
	warn   io.Writer // where non-fatal problems go; nil discards them
	// cache reads the run objects through the local run cache
	// (internal/runcache), which answers an object from the last listing
	// when its generation is unchanged. Only a listing for display sets it
	// -- runs ls, and the report command through api.LoadRows -- and only
	// for a GCS bucket, which is the only one that reports a generation.
	//
	// Everything else leaves it false, and the reason is the same for all
	// of them: a cached object is what a listing saw, so it may draw a
	// status, never decide. resolvePR (does this PR allow a follow-up?) and
	// the budget's own facts pass (what the rollover records for a day)
	// read the bucket as it is now.
	cache bool
}

// lsRef names one run a listing means to read.
type lsRef struct{ slug, id string }

// lsReaders builds the reader of each repository a listing is about to read:
// one index per repository, from one listing of it, shared by every run of
// that repository.
//
// Nothing is built when the listing did not ask for the cache, which is how
// every caller that is not a display listing -- and --no-cache -- reads the
// bucket exactly as it did before the cache existed. runcache.Open and
// runcache.List answer nil for a cache the CLI cannot place and for a bucket
// that reports no generation per object, and both fall back to the same
// thing: a reader that reads the bucket.
func lsReaders(ctx context.Context, env *cloudEnv, cache bool, refs []lsRef) map[string]runcache.Reader {
	if !cache {
		return nil
	}
	c, err := runcache.Open(os.Getenv, env.lc.Name, cacheBucketID(env))
	if err != nil {
		return nil
	}
	// The listing starts at the oldest run this one means to read, so a
	// repository whose runs are mostly older than the window costs nothing
	// to skip there (runstore.ListRunIDs's own offset rule).
	oldest := map[string]string{}
	for _, r := range refs {
		if from, ok := oldest[r.slug]; !ok || r.id < from {
			oldest[r.slug] = r.id
		}
	}
	readers := make(map[string]runcache.Reader, len(oldest))
	for slug, from := range oldest {
		readers[slug] = runcache.Reader{Cache: c, Index: runcache.List(ctx, env.bucket, slug, from)}
	}
	return readers
}

// cacheBucketID is what the run cache keys a bucket by: the GCS bucket's
// name, so two installations of one project never share entries, and the
// configured URL for the buckets that are not GCS (a local file:// runs
// bucket, which reports no generations and so caches nothing anyway).
func cacheBucketID(env *cloudEnv) string {
	if env.bucket.GCSName != "" {
		return env.bucket.GCSName
	}
	return env.lc.BucketURL()
}

// lsDoc is ls --json's document.
type lsDoc struct {
	Project string         `json:"project"` // the Fugaro project
	Runs    []runview.Row  `json:"runs"`
	Totals  runview.Totals `json:"totals"`
	// Warnings are the lines a human listing prints before its table.
	Warnings []string `json:"warnings"`
}

// newRunsCmd is "fugaro runs": ls lists the runs, and stats aggregates them
// by the person who started each one. The 0.7.0 top-level `fugaro ls`
// tombstone was deleted by #350 in 0.7.5 (design generic-tool §5, G17; the
// owner's 2026-10-14 ruling moved the deletion from 0.8.0 to the next
// release — the one that became 0.7.5, #370; plan Task 29).
func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List and inspect runs",
	}
	cmd.AddCommand(newLsCmd(), newRunsStatsCmd(), newRunsDigestCmd())
	return cmd
}

func newLsCmd() *cobra.Command {
	var o lsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List runs with their status, stage, cost and PR",
		Long: "List runs, newest first, joining the runs bucket with what the backend knows.\n\n" +
			"By default ls shows the last " + strconv.Itoa(lsDefaultDays) + " days of the local config's repositories (every\n" +
			"repository in the bucket when the config lists none). A run shows as succeeded\n" +
			"only when its result.json says so; an execution that ended without finalizing\n" +
			"is an infra_error.\n\n" +
			"--limit N reads the objects of the newest N runs alone, so a run left out\n" +
			"costs nothing to leave out; the N are the newest of every repository\n" +
			"listed, and --mine, --batch, --state and --pr then narrow fewer rows than\n" +
			"that, never more. 0, the default, reads every run of the window.\n\n" +
			"--state STATE lists only runs in that state, matched against the status --json\n" +
			"carries: the `halted (run_cap)` cell is --state halted, and --help lists the\n" +
			"states the flag takes. A run's state is in its result.json, so this filter\n" +
			"reads every run it filters -- it narrows the listing, never the reads.\n\n" +
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
	f.StringVar(&o.since, "since", lsDefaultSince, "only runs started within this long ("+lsDefaultSince+", 36h, 90m; 0 for all)")
	f.StringVar(&o.batch, "batch", "", "only runs of this batch")
	f.StringVar(&o.state, "state", "", "only runs in this state ("+strings.Join(lsStates, ", ")+")")
	f.IntVar(&o.limit, "limit", 0, "read and list only the newest N runs (0, the default, is every run of the window)")
	f.IntVar(&o.pr, "pr", 0, "only runs on this pull request (needs one repository; --since defaults to "+prLookback+")")
	f.BoolVar(&o.watch, "watch", false, "redraw until every run has settled")
	f.DurationVar(&o.interval, "interval", 10*time.Second, "--watch's redraw interval")
	_ = f.MarkHidden("interval")
	f.BoolVar(&o.noCache, "no-cache", false, "read every run object from the bucket instead of the local run cache")
	_ = f.MarkHidden("no-cache")
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
		return userErr("runs ls --pr needs --repo: a pull request number means something only in one repository")
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
	if o.limit < 0 {
		return userErr("--limit %d: use a count, or 0 for every run of the window", o.limit)
	}
	if o.state != "" && !slices.Contains(lsStates, o.state) {
		return userErr("--state %q: use one of %s", o.state, strings.Join(lsStates, ", "))
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	f := lsFilter{batch: o.batch, state: o.state, limit: o.limit, pr: o.pr, warn: cmd.ErrOrStderr(), cache: !o.noCache}
	if prSet && o.repo == "" && len(env.lc.Repos) != 1 {
		return userErr("runs ls --pr needs --repo: the local config lists %d repositories", len(env.lc.Repos))
	}
	if f.slugs, err = lsSlugs(ctx, env, o, cmd.ErrOrStderr()); err != nil {
		return err
	}
	if prSet && len(f.slugs) != 1 {
		return userErr("runs ls --pr needs --repo: %d repositories to list", len(f.slugs))
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
		now := lsNow()
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
		if err := printRows(out, env.lc.Name, rows, warnings, now, o.asJSON); err != nil {
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

// loadRows reads the runs f selects -- the newest f.limit of them when it is
// set, which bounds the reads and not only the rows -- joins each with its
// execution, and returns the rows newest first. A run whose launch.json or
// result.json is corrupt, or names an execution that isn't the run's, is an
// error row with a warning. Any other failed read but absence fails the whole
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
	var refs []lsRef
	if f.runRef != "" {
		slug, id, err := parseRunRef(f.runRef)
		if err != nil {
			return nil, err
		}
		refs = append(refs, lsRef{slug, id})
	} else {
		for _, slug := range f.slugs {
			ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, f.since)
			if err != nil {
				return nil, remote(err)
			}
			for _, id := range ids {
				refs = append(refs, lsRef{slug, id})
			}
		}
		// A run ID starts with its mint time, so ordering the refs by ID puts
		// the newest runs first before any object is read and --limit then
		// reads the newest N runs' objects and nothing older. The order is
		// across the repositories listed, not within each, because the N are
		// the newest runs of the listing. Without a limit the refs keep the
		// order they were gathered in, which is what the warnings a listing
		// prints are read in.
		if f.limit > 0 {
			slices.SortStableFunc(refs, func(a, b lsRef) int {
				if c := strings.Compare(b.id, a.id); c != 0 {
					return c
				}
				return strings.Compare(a.slug, b.slug)
			})
			refs = refs[:min(len(refs), f.limit)]
		}
	}

	// The bucket's objects, for every run, through the run cache when this
	// listing asked for it. One flat listing per repository carries every
	// run's three objects' generations, so the runs the previous listing
	// already read are answered locally and the reads that leave the machine
	// are the objects that changed.
	readers := lsReaders(ctx, env, f.cache, refs)
	runs := make([]runObjects, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		runs[i], errs[i] = readRunVia(ctx, env, readers[refs[i].slug], refs[i].slug, refs[i].id)
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}
	// A run left out warns only when it might be on the PR: its record
	// is unreadable and its task doesn't place it on another one.
	for _, r := range runs {
		if r.warning != "" && (f.pr == 0 || onPR(r.in, f.pr) || r.mayBeOnPR()) {
			fmt.Fprint(warn, multiLine(r.warning))
		}
	}
	if f.pr != 0 {
		runs = slices.DeleteFunc(runs, func(r runObjects) bool { return !onPR(r.in, f.pr) })
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

	var mu sync.Mutex // guards warn
	warnf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(warn, multiLine(fmt.Sprintf(format, args...)))
	}
	// The price book warns through warnf too: a VM run whose region or
	// machine type the price table does not carry is listed with compute not
	// estimated, and this is where the operator reads why (design
	// compute-backend.md §5.2).
	prices := env.prices(func(s string) { warnf("%s", s) })
	rows := make([]runview.Row, len(runs))
	errs = make([]error, len(runs))
	parallelN(len(runs), followWorkers, func(i int) {
		if errs[i] = followExecution(ctx, env, &runs[i], execs, warnf); errs[i] == nil {
			rows[i] = runview.Join(runs[i].in, prices, now)
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}

	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return (f.mine != "" && r.RequestedBy != f.mine) || (f.batch != "" && r.Batch != f.batch) ||
			(f.state != "" && string(r.Status) != f.state)
	})
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Run, b.Run)
	})
	return rows, nil
}

// parallel calls fn(0) … fn(n-1) on up to lsWorkers goroutines, for work that
// reads the runs bucket.
func parallel(n int, fn func(i int)) {
	parallelN(n, lsWorkers, fn)
}

// parallelN is parallel with a bound of its own. A caller whose work reaches
// something other than the runs bucket passes its width here rather than
// growing lsWorkers, which is what a bucket read costs: two gh processes at
// github.com at once are what a host's secondary rate limits are about, and a
// read that fans out over repositories multiplies its width by the width of
// the read inside it.
func parallelN(n, workers int, fn func(i int)) {
	pool.Run(n, workers, fn)
}

// onPR reports whether a run is on pull request pr: its task continues
// it, or its record names it. An error row whose task says so is kept.
func onPR(in runview.Input, pr int) bool {
	return (in.Task != nil && in.Task.PR == pr) || (in.Record != nil && in.Record.PR != nil && in.Record.PR.Number == pr)
}

// mayBeOnPR reports whether a run onPR leaves out could still be on a
// pull request: its launch.json or result.json is unreadable, so its
// record wasn't read, and no task says it follows another PR.
func (r runObjects) mayBeOnPR() bool {
	return r.unreadRecord && (r.in.Task == nil || r.in.Task.PR == 0)
}

// runObjects is what the bucket holds about one run, before its
// execution is joined.
type runObjects struct {
	in        runview.Input
	store     *runstore.Store
	execution string // the execution to follow; "" when there is none
	warning   string // printed only when the run is listed (or may be)
	// unreadRecord means a corrupt object stopped the reads before the
	// record could say which pull request the run is on.
	unreadRecord bool
}

// readRun reads what the bucket knows about one run; it makes no backend
// call. Every object comes from the bucket: a caller that wants the run
// cache passes its own reader to readRunVia.
func readRun(ctx context.Context, env *cloudEnv, slug, id string) (runObjects, error) {
	return readRunVia(ctx, env, runcache.Reader{}, slug, id)
}

// readRunVia is readRun through rd, which may answer an object from the
// local run cache. rd's index decides absence as well as content, so a
// reader built from a listing reads nothing for a run whose objects the
// listing did not see.
func readRunVia(ctx context.Context, env *cloudEnv, rd runcache.Reader, slug, id string) (runObjects, error) {
	s := runstore.Open(env.bucket.Bucket, slug, id)
	r := runObjects{in: runview.Input{Slug: slug, RunID: id}, store: s}
	in := &r.in
	problem := func(p string) {
		in.Problem = p
		r.warning = fmt.Sprintf("warning: run %s/%s: %s\n", slug, id, p)
	}
	raw, err := rd.Read(ctx, s, "task.json")
	switch {
	case err != nil:
		return r, err
	case raw != nil:
		in.Task, _ = task.Parse(raw) // unparseable: the row says task.json unreadable
	}
	// A corrupt object is this run's error row, with a warning; a read that
	// fails otherwise (the bucket, the network) still fails the listing.
	for _, read := range []struct {
		name string
		do   func() error
	}{
		{"launch.json", func() (err error) { in.Launch, err = readLaunch(ctx, s, rd); return err }},
		{"result.json", func() (err error) { in.Record, err = readRecord(ctx, s, rd); return err }},
	} {
		if err := read.do(); corruptObject(err) {
			problem(read.name + " is unreadable: " + err.Error())
			r.unreadRecord = true
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

// readLaunch reads a run's launch.json through rd: the bytes, decoded
// exactly as runstore.ReadLaunch decodes them, so an object the bucket made
// unreadable is the same error either way. A nil launch and no error means
// the run was never launched.
func readLaunch(ctx context.Context, s *runstore.Store, rd runcache.Reader) (*runstore.Launch, error) {
	raw, err := rd.Read(ctx, s, "launch.json")
	if err != nil || raw == nil {
		return nil, err
	}
	var l runstore.Launch
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("decoding launch.json: %w", err)
	}
	return &l, nil
}

// readRecord reads a run's result.json through rd, as runstore.ReadRecord
// decodes it. A nil record and no error means the run has not finalized.
func readRecord(ctx context.Context, s *runstore.Store, rd runcache.Reader) (*runstore.Record, error) {
	raw, err := rd.Read(ctx, s, "result.json")
	if err != nil || raw == nil {
		return nil, err
	}
	var rec runstore.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("decoding result.json: %w", err)
	}
	return &rec, nil
}

// followExecution joins r with its execution, from execs (the listing)
// or, when the listing lacks it, from the backend.
func followExecution(ctx context.Context, env *cloudEnv, r *runObjects, execs map[string]backend.Execution, warnf func(string, ...any)) error {
	in, s, name := &r.in, r.store, r.execution
	if name == "" || in.Problem != "" {
		return nil
	}
	// The run's service account can write both objects: follow only the
	// execution the run's own FUGARO_RUN names, which is what tells two runs
	// of one shared job apart (checkExecution). The listing's own entry saves
	// the read; a run whose execution is outside the listing's window is read
	// back on its own. A read that fails is this run's own row, not the
	// listing's: one run's execution failing to read must not hide the other
	// runs' rows, as a corrupt object of one run does not.
	eid, _ := backend.ParseExecution(name)
	var hint *backend.Execution
	if e, ok := execs[eid.Key()]; ok {
		hint = &e
	}
	x, err := env.checkExecution(ctx, hint, name, in.Slug, in.RunID)
	if err != nil {
		in.Problem = "not following its execution: " + err.Error()
		warnf("warning: run %s/%s: %s\n", in.Slug, in.RunID, in.Problem)
		return nil
	}
	in.Exec = x
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

// maxSinceDays is the largest day count whose duration fits in int64
// nanoseconds: time.Duration(days)*24*time.Hour silently wraps to a negative
// window past it, so a larger count is refused rather than read as "no runs".
// The desktop console's runs-window schema holds the same bound.
const maxSinceDays = 106751

// parseSince parses --since: a positive number of days (7d), a positive Go
// duration (36h, 90m), or 0 for no bound.
func parseSince(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 1 || days > maxSinceDays {
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
func printRows(w io.Writer, project string, rows []runview.Row, warnings []string, now time.Time, asJSON bool) error {
	tot := runview.Sum(rows)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(lsDoc{Project: project, Runs: rows, Totals: tot, Warnings: warnings})
	}
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	showRecipe := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.Recipe != "" && r.Recipe != recipe.DefaultName })
	showLayer := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.ProjectLayer != nil })
	showAccount := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.OAuthAccount != "" && r.OAuthAccount != "default" })
	// The artifact column appears only when some listed run has artifacts:
	// every run that opens a pull request has none, and its columns do not
	// change (design artifact-runs.md §9).
	showArtifacts := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.ArtifactCount > 0 })
	// The changed-files column appears only when some listed run's record
	// carries the set (design D27): a run from before the field, or one that
	// never reached finalize, has none and does not widen every other run's
	// columns for it.
	showFiles := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.ChangedFiles != nil })
	// The mode column appears only when some listed run's record carries a
	// rigor block (docs/rigor-modes D24): a run from before the field, or one
	// that died before bootstrap, has none and does not widen every other
	// run's columns for it.
	showMode := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.Rigor != nil })
	header := "RUN\tSTATUS\tSTAGE\tAGE\tCOST\tPR\tBY"
	if showRecipe {
		header += "\tRECIPE"
	}
	if showLayer {
		header += "\tLAYER"
	}
	if showAccount {
		header += "\tACCOUNT"
	}
	if showArtifacts {
		header += "\tARTIFACTS"
	}
	if showFiles {
		header += "\tFILES"
	}
	if showMode {
		header += "\tMODE"
	}
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		cost := fmt.Sprintf("$%.2f", r.Cost.TotalUSD)
		if r.Cost.ModelSubscriptionUSD() > 0 {
			// The model spend -- or, in a mixed run, part of it -- was
			// notional, and the figure beside this marker leaves it out.
			cost += "~"
		}
		if !r.Cost.ComputeEstimated {
			cost += ", compute not estimated" // never "free" (design §10.1)
		}
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s", oneLine(r.Run), oneLine(statusCell(r)), oneLine(r.Stage), age(now.Sub(r.Created)), cost, prColumn(r), launcherCell(r))
		if showRecipe {
			name := r.Recipe
			if name == "" {
				name = recipe.DefaultName
			}
			line += "\t" + cellOneLine(clipRunes(name, maxNameCellRunes))
		}
		if showLayer {
			line += "\t" + layerCell(r)
		}
		if showAccount {
			name := r.OAuthAccount
			if name == "" {
				name = "default"
			}
			line += "\t" + cellOneLine(clipRunes(name, maxNameCellRunes))
		}
		if showArtifacts {
			line += "\t" + artifactCell(r)
		}
		if showFiles {
			line += "\t" + changedFilesCell(r)
		}
		if showMode {
			line += "\t" + modeCell(r)
		}
		fmt.Fprintln(tw, line)
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

// launcherCell is a row's BY cell: who started the run, in the short form a
// column has room for (the part before the @ of an address, the whole value
// for anything else — a service account name, a CI identity), or "-" for a
// run whose launch.json recorded no launcher. Several people start runs on
// one project at once, so the column is always there, and the dash is a fact
// ("nobody recorded"), never a placeholder for one.
//
// The launcher is untrusted text a launcher wrote, so it is clipped and
// escaped like every other name column, whether or not it arrived through
// runview.Join's own sanitising.
func launcherCell(r runview.Row) string {
	by := runview.LauncherShort(r.LaunchedBy)
	if by == "" {
		return "-"
	}
	return cellOneLine(clipRunes(by, maxNameCellRunes))
}

// statusCell is a row's status; a halted run says which limit halted it, and a
// row whose status was derived at read time says what was derived.
//
// The decoration is display only, exactly as the halt reason is: the column
// reads `failed (preempted)` while `status` stays `failed` and the fact that
// identified the preemption sits beside it in `derived`, so a reader that
// switches on status -- or a script that parses it -- sees the vocabulary it
// always saw. A row is either halted or derived, never both: a record that
// halted is a finalized record, and a finalized record is never derived.
func statusCell(r runview.Row) string {
	if r.Status == string(runstore.StatusHalted) && r.Halt != nil {
		return r.Status + " (" + string(r.Halt.Reason) + ")"
	}
	if r.Derived != "" {
		return r.Status + " (" + r.Derived + ")"
	}
	return r.Status
}

// changedFilesCell is a row's recorded changed-path count (design D27): the
// count of paths the record holds, and "N/M" when the record was truncated at
// M paths, so a reader can see that the set it holds is not the whole set.
// "-" for a run that recorded none — an unknown answer, never a zero.
func changedFilesCell(r runview.Row) string {
	cf := r.ChangedFiles
	if cf == nil {
		return "-"
	}
	if cf.Truncated {
		return fmt.Sprintf("%d/%d", len(cf.Paths), cf.Total)
	}
	return strconv.Itoa(len(cf.Paths))
}

// validSHA256 reports whether s is a sha256 hex digest: exactly 64 lowercase
// hex characters. The record a sha comes from is launcher-writable and
// json.Unmarshal (runstore.ReadRecordVersion) never checks it against the
// schema, so it can be any text; shortSHA must reject it before slicing, or
// a multibyte rune cut mid-byte would corrupt the terminal output.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// shortSHA is a sha256 hex digest shortened for display; a string that
// isn't 64 lowercase hex characters shows as "(invalid)" instead.
func shortSHA(s string) string {
	if !validSHA256(s) {
		return "(invalid)"
	}
	return s[:12]
}

// validGeneration reports whether g could be a real layer generation: a
// publisher only ever increments from 0, so negative means the record
// wasn't written by one (same untrusted-record concern as validSHA256).
func validGeneration(g int64) bool { return g >= 0 }

// modeCell is a row's rigor mode and where it came from (docs/rigor-modes
// D24): the mode, the layer that decided it, and the strictest area rule that
// matched when one did, with a "+N" for the rest D23 also recorded. "-" for a
// run whose record carries no block this reader can show: it resolved none,
// which is not the same fact as resolving the built-in default.
//
// runs ls is a table, so the cell is one short line; `fugaro diagnose` prints
// the whole block (runstore.RigorRecord.Line). Both are built from the
// record's own block,
// which is job-writable: the block reaches the row through
// runstore.RigorRecord.Bounded, the name through the same clip every other
// name cell uses, and the finished cell through cellOneLine.
func modeCell(r runview.Row) string {
	rg := r.Rigor
	if rg == nil {
		return "-"
	}
	cell := string(rg.Mode) + " (" + string(rg.From)
	if len(rg.Areas) > 0 {
		cell += " " + clipRunes(rg.Areas[0].Name, maxNameCellRunes)
		if n := len(rg.Areas) - 1; n > 0 {
			cell += fmt.Sprintf(" +%d", n)
		}
	}
	return cellOneLine(cell + ")")
}

// layerCell is a run's project layer in ls: its generation, "-" for none
// or for a record whose generation isn't trustworthy.
func layerCell(r runview.Row) string {
	pl := r.ProjectLayer
	switch {
	case pl == nil || !validGeneration(pl.Generation):
		return "-"
	case !pl.Applied:
		return fmt.Sprintf("gen %d (not applied)", pl.Generation)
	}
	return fmt.Sprintf("gen %d", pl.Generation)
}

// prColumn is a row's pull request: "#N <url>", "#N" while the URL is
// unknown (a follow-up before it starts), else the URL alone; then a
// "(draft)" marker while the PR is, or may be left, a draft: the early draft
// of a running first run, an outcome of draft, a draft whose run died.
func prColumn(r runview.Row) string {
	var col string
	switch {
	case r.PR > 0 && r.PRURL != "":
		col = fmt.Sprintf("#%d %s", r.PR, oneLine(r.PRURL))
	case r.PR > 0:
		col = fmt.Sprintf("#%d", r.PR)
	default:
		col = oneLine(r.PRURL)
	}
	switch {
	case col == "":
	case r.StaleDraft:
		col += " (draft, stale)"
	case r.Outcome == string(runstore.OutcomeDraft), r.Status == string(runstore.StatusRunning) && !r.FollowUp && r.RecordPR > 0:
		col += " (draft)"
	}
	return col
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
