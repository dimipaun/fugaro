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
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pool"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runcache"
	"github.com/dimipaun/fugaro/internal/runindex"
	"github.com/dimipaun/fugaro/internal/runner"
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
	groupBy                  string
	all, mine, watch, asJSON bool
	stream                   bool // with --json: one line per run, then the line that ends the feed
	debt                     bool // only the runs filed from a triage deferral, when set
	interval                 time.Duration
	pr                       int    // only the runs on this pull request, when set
	limit                    int    // print the newest N rows of the listing, when > 0
	after                    string // start the listing after this run, when set
	noCache                  bool   // read every object from the bucket, as before the cache existed
}

// lsStates are the run states --state accepts, sorted: runstore's own states
// and the ones runview derives for a run that has not recorded an outcome yet.
// They are the vocabulary a row's `status` carries, never the decorated cell
// statusCell prints -- `failed (preempted)` is --state failed, as a reader
// switching on status has always seen it.
//
// `paused` is the run's own status word (R15) and belongs here with the
// others: a run that suspended itself is a state a reader asks a listing for,
// and it is a scan rather than a live query, because a pause ends the run's
// execution and releases its lease (R12, runs_live.go). It is also the state
// of the pull request such a run is on, and it is deliberately not one of the
// three in lsPRStates -- that list's own note says why.
var lsStates = []string{
	string(runstore.StatusCancelled), runview.StatusError, string(runstore.StatusFailed),
	string(runstore.StatusHalted), string(runstore.StatusInfraError), runview.StatusLaunching,
	string(runstore.StatusPaused), runview.StatusPending, string(runstore.StatusRunning),
	string(runstore.StatusSucceeded), runview.StatusUnlaunched, string(runstore.StatusUnreviewed),
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
	mine   string    // keep runs requested by this identity, when set
	batch  string    // keep runs of this batch, when set
	state  string    // keep runs in this state, when set
	debt   bool      // keep runs whose task is marked debt, when set
	pr     int       // keep runs on this pull request (task or record), when set
	runRef string    // "<slug>/<run-id>": only this run (diagnose)
	warn   io.Writer // where non-fatal problems go; nil discards them
	// noCache reads every object from the bucket, as every listing did
	// before the run cache existed. A listing reads through the cache by
	// default -- runs ls, and the report command through api.LoadRows, the
	// one helper both go through -- because that is what the cache is for:
	// the same window listed again and again.
	//
	// It is set where a cached object would answer more than a display
	// number. resolvePR asks whether this pull request allows a follow-up,
	// which is a launch, and the budget's own facts pass (what the rollover
	// records for a day, internal/budget/rollover_facts.go) is a budget
	// check that never goes through this loader at all. A cached object is
	// what a listing saw, and a decision has to see the bucket.
	//
	// runs ls --no-cache sets it too: the whole cache is an optimisation,
	// so ignoring it must cost nothing but time.
	noCache bool
	// indexNote asks for the two lines that say why a --pr read fell back to
	// the window: the index could not be read, or the pull request has no
	// entry in it (runs_pr.go, D2964).
	//
	// They are a listing's, and the listing is the only reader that sets it.
	// The person watching `runs ls --pr N` is the one who reads why it is
	// slow, while a caller that reads a pull request's runs to decide
	// something -- the launcher resolving the spec a review is handed
	// (reviewspec.go's launchSpecForPR), a follow-up's own resolvePR -- is
	// not reading a listing, and a line about this implementation's shortcut
	// is not something a launch prints. The read is the same either way: the
	// note is a note, never a different set of runs (RL3's acceptance, which
	// keeps those callers on the index).
	indexNote bool
}

// lsRef names one run a listing means to read.
type lsRef struct{ slug, id string }

// lsReaders builds the reader of every run a listing is about to read: one
// index per repository, from one listing of it, and the runs that index can
// answer for share it.
//
// Nothing is built for a listing that asked not to be cached (lsFilter's
// noCache), and that is how a decision and --no-cache read the bucket
// exactly as they did before the cache existed: the reader of every run is
// then the zero Reader, which reads the bucket. runcache.Open and
// runcache.List answer nil for a cache the CLI cannot place and for a bucket
// that reports no generation per object, and a nil cache or a nil index falls
// back to the same thing.
func lsReaders(ctx context.Context, env *cloudEnv, cached bool, refs []lsRef) map[lsRef]runcache.Reader {
	if !cached {
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
	readers := make(map[lsRef]runcache.Reader, len(refs))
	for slug, from := range oldest {
		index := runcache.List(ctx, env.bucket, slug, from)
		named := indexRuns(index, slug)
		for _, r := range refs {
			if r.slug != slug {
				continue
			}
			// A run the index names not one object of is a run it cannot
			// answer for (indexRuns): its objects are read from the bucket.
			if index != nil && !named[r.id] {
				readers[r] = runcache.Reader{}
				continue
			}
			readers[r] = runcache.Reader{Cache: c, Index: index}
		}
	}
	return readers
}

// indexRuns is the runs whose prefix the index names at least one object of,
// by run ID.
//
// The run IDs of a window and the generations the cache keys its entries by
// come from two listings, and a run written between them is named by the
// earlier one and by no object of the later one. On a bucket that is strongly
// consistent only one order of the two is possible -- the flat listing is the
// later, so it sees at least what the delimited listing saw -- but an index
// that names no object of a run cannot answer for it either way: an unlisted
// key reads as absent, and the run would be drawn with no task and no record
// rather than as the run the bucket holds.
func indexRuns(index map[string]int64, slug string) map[string]bool {
	named := make(map[string]bool, len(index))
	prefix := "runs/" + slug + "/"
	for key := range index {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if id, _, ok := strings.Cut(rest, "/"); ok && id != "" {
			named[id] = true
		}
	}
	return named
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
	// Groups is --group-by's block: one row per failure class of the window,
	// beside the rows rather than instead of them. It is omitted whenever no
	// grouping was asked for, and omitempty is what makes that safe: the
	// degraded watch document embeds this type (watchDegradedDoc) and the
	// phone refuses a key it does not know, so a block printed by a listing
	// that never grouped would break the app's degraded screen (F14, D1458).
	Groups []lsGroup `json:"groups,omitempty"`
	// IndexComplete and FoldedThrough are the run index's own two facts about
	// itself, carried by a listing whose rows came from it: whether a walk has
	// ever reached the end of history (D3215) and the moment the index is
	// folded through, which is how fresh the rows are.
	//
	// They are absent from a listing the window answered -- every listing of
	// this release, which reads the index's stub, and every listing a flag the
	// index does not serve puts on the window -- because a listing that read no
	// index has nothing to say about one. Both are pointers for the reason
	// Groups is a slice: the degraded watch document embeds this type and the
	// phone refuses a key it does not know, so a field a listing did not read
	// must not reach the document it writes (F14, D1458).
	IndexComplete *bool      `json:"index_complete,omitempty"`
	FoldedThrough *time.Time `json:"folded_through,omitempty"`
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
	cmd.AddCommand(newLsCmd(), newRunsStatsCmd(), newRunsSummaryCmd(), newRunsDigestCmd(), newRunsReportsCmd(), newRunsShowCmd(), newRunsWatchCmd(), newRunsReindexCmd())
	return cmd
}

func newLsCmd() *cobra.Command {
	var o lsOptions
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List runs with their status, stage, cost and PR",
		Long: "List runs, newest first -- created descending, then run descending --\n" +
			"joining the runs bucket with what the backend knows.\n\n" +
			"By default ls lists the newest " + strconv.Itoa(lsIndexPage) + " runs by time plus every run that is\n" +
			"still going, whatever its age, read from the installation's run index rather\n" +
			"than by scanning the bucket: no listing of the bucket's objects is made on\n" +
			"that path, so the listing costs a handful of requests however much history\n" +
			"there is. Its totals are then the index's own summary -- every run the\n" +
			"installation's index carries, not the page -- which is the figure of a\n" +
			"listing that covers the installation: one narrowed to some of its\n" +
			"repositories (--repo, or a local config that lists them) totals the rows\n" +
			"it read, as a windowed listing does. While the index is still building,\n" +
			"the listing says so in one line above its table, and --json carries\n" +
			"index_complete and folded_through.\n\n" +
			"A run shows as succeeded only when its result.json says so; an execution that\n" +
			"ended without finalizing is an infra_error.\n\n" +
			"--since W bounds a window instead of a count: every run of it is listed,\n" +
			"bounded by the window and not by history, and its totals are those rows'.\n" +
			"--mine and --batch bound one the same way. A listing the index cannot answer\n" +
			"reads the bucket's own window instead, and that window is the last\n" +
			strconv.Itoa(lsDefaultDays) + " days of the local config's repositories (every repository in the\n" +
			"bucket when the config lists none). An index that could not be read says so\n" +
			"in one note; an index that has folded nothing yet -- a fresh installation,\n" +
			"before its first fold -- reads the window in silence and carries no index\n" +
			"fact either way, which is the state every installation is in until then.\n\n" +
			"--limit N prints the newest N rows of the listing and shortens nothing else:\n" +
			"the totals line, in the text listing and in --json's totals, is the listing's\n" +
			"own -- the index's summary by default, and the rows of the window with --since\n" +
			"-- because a page is not a total. 0, the default, prints every row of it -- a\n" +
			"default listing's own page (the newest " + strconv.Itoa(lsIndexPage) + " plus the runs still going), or the\n" +
			"whole window when --since, --mine or --batch bounds the listing instead.\n\n" +
			"--after RUN-ID starts the listing after that run: the rows strictly after it\n" +
			"in the listing's order, so the same argv with the last id of a page is the\n" +
			"next page. The id is the run_id --json carries; one the listing does not\n" +
			"hold is refused rather than answered with an empty page, which reads as no\n" +
			"more runs. Neither flag goes with --watch, which redraws the whole listing.\n\n" +
			"--state STATE lists only the runs in that state, one of two vocabularies.\n" +
			"The first is the run's own state, matched against the status --json carries:\n" +
			"the `halted (run_cap)` cell is --state halted. The second is the state of\n" +
			"the pull request the run went to: live (a run is going on it), ready (no\n" +
			"run is going and it waits only on a merge) or stalled (it had a run, no run\n" +
			"is going, and it is not ready). That state is the pull request's and not a\n" +
			"row's: the listing groups the runs it read by the pull request they carry,\n" +
			"takes the newest one of each that says what the pull request is -- the run\n" +
			"whose outcome is ready or draft, so a rebase or a review that left it as\n" +
			"it found it does not decide -- and keeps the rows of the pull requests in\n" +
			"the state asked for; --json carries it as `pr_state` on every row that has\n" +
			"a pull request, and leaves it out of the rows that have none. It is the\n" +
			"last state a run recorded of the pull request and not the pull request's\n" +
			"state now: nothing here calls the forge, so a pull request merged or\n" +
			"closed after the run that left it ready still reads ready, and one\n" +
			"closed after a draft still reads stalled. A review run's pull request is\n" +
			"the one its task reviews, which its record names only when the review\n" +
			"ends: while it is going, its row reads that pull request's state though\n" +
			"its pr is still empty. A run's own state is in its result.json, so this\n" +
			"filter reads every run it filters -- it narrows the listing, never the\n" +
			"reads. running and launching are the exception: a run that is going, and a\n" +
			"run whose launch is in flight, are answered from the live set -- the\n" +
			"executions the backend is still running, and the claims a launch in flight\n" +
			"holds -- so those two read only the runs that set names, never every run of\n" +
			"the window (D2960). One run launching can miss: a relaunch under an older\n" +
			"run ID (run --retry, or a re-issue by the resident launcher) holds a fresh\n" +
			"claim on a run older than the claim window, which is what bounds the\n" +
			"listing those claims are found by, so a scan shows that run and this query\n" +
			"does not.\n\n" +
			"The fourth state of a pull request, orphan (no run ever produced or follows\n" +
			"it), is one no listing of runs can name: such a pull request has no run and\n" +
			"so no row here, and telling one from every other would take a call to the\n" +
			"forge, which a listing of runs does not make -- --state orphan is refused\n" +
			"for that reason rather than answered with an empty listing.\n\n" +
			"--debt lists only the runs filed from a triage deferral: a task whose own spec\n" +
			"is marked debt, which is work a run's review left behind rather than work\n" +
			"somebody asked for. Like --state it reads the runs it filters, since the\n" +
			"mark is in the run's task.json; a run whose task.json is unreadable is\n" +
			"left out rather than guessed in, because nothing there says it is one.\n\n" +
			"--group-by failure_class adds a block to the listing: one row per class of the\n" +
			"closed vocabulary, with the runs of the window that ended in it, their share\n" +
			"of the window's runs, and what they cost. It groups the rows the listing has\n" +
			"already read and reads nothing new, and it moves nothing: the rows and the\n" +
			"totals are the ones without it, and --json gains the `groups` block beside\n" +
			"them. A page of a grouped listing is a page's total, so --group-by doesn't go\n" +
			"with --limit, --after or --watch.\n\n" +
			"--pr N lists the runs on pull request N of one repository (the first run and\n" +
			"its follow-ups), over the last " + prLookback + " unless --since says otherwise; the totals\n" +
			"line is then the PR's total cost.\n\n" +
			"--stream, with --json, prints the listing as newline-delimited JSON instead of\n" +
			"as one document: a line per run, each carrying one element of --json's runs\n" +
			"array unchanged under `row`, then a line saying how many rows the feed\n" +
			"carried (`end`). A consumer renders a run as its line arrives rather than\n" +
			"holding the whole document, and a feed with no end line is a read that was\n" +
			"interrupted. A listing that stopped after its first line writes an `error`\n" +
			"line with the sentence the command would have printed, and exits non-zero.\n" +
			"The rows of a page are the page's, so --limit and --after mean here what\n" +
			"they mean without it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runLs(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name, or a path inside its checkout)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.BoolVar(&o.mine, "mine", false, "only runs you requested")
	f.StringVar(&o.since, "since", lsDefaultSince, "only runs started within this long ("+lsDefaultSince+", 36h, 90m; 0 for all)")
	f.StringVar(&o.batch, "batch", "", "only runs of this batch")
	f.StringVar(&o.state, "state", "", "only runs in this state, a run's or its pull request's ("+strings.Join(lsStateWords(), ", ")+")")
	f.StringVar(&o.groupBy, "group-by", "", "group the listing by one field ("+strings.Join(lsGroupings, ", ")+")")
	f.BoolVar(&o.debt, "debt", false, "only runs filed from a triage deferral (a task marked debt)")
	f.IntVar(&o.limit, "limit", 0, "print only the newest N rows of the listing (0, the default, is every row of it: a default listing's page, a bounded one's whole window; the totals stay the listing's)")
	f.StringVar(&o.after, "after", "", "print only the rows after this run id in the listing")
	f.IntVar(&o.pr, "pr", 0, "only runs on this pull request (needs one repository; --since defaults to "+prLookback+")")
	f.BoolVar(&o.watch, "watch", false, "redraw until every run has settled")
	f.DurationVar(&o.interval, "interval", 10*time.Second, "--watch's redraw interval")
	_ = f.MarkHidden("interval")
	f.BoolVar(&o.noCache, "no-cache", false, "read every run object from the bucket instead of the local run cache")
	_ = f.MarkHidden("no-cache")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.stream, "stream", false, "with --json: one line per run, then a line saying how many there were")
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
	var err error
	if o.repo, err = repoArg(ctx, o.cloud, o.repo); err != nil {
		return err
	}
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	if o.watch && o.interval <= 0 {
		return userErr("--interval must be positive")
	}
	if o.limit < 0 {
		return userErr("--limit %d: use a count, or 0 for every row of the listing", o.limit)
	}
	// The flags are read as typed rather than by value, the way --pr is: a
	// reader who wrote either beside --watch asked for a page of a listing
	// that redraws, and that is the combination being refused.
	if o.watch && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("after")) {
		return userErr("--watch redraws the whole listing until every run has settled: --limit and --after don't go with it")
	}
	if o.state == prStateOrphan {
		return userErr("--state orphan: no run produced or follows such a pull request, so no listing of runs holds a row of it -- only a forge can name one")
	}
	if o.state != "" && !slices.Contains(lsStateWords(), o.state) {
		return userErr("--state %q: use one of %s", o.state, strings.Join(lsStateWords(), ", "))
	}
	if o.groupBy != "" && !slices.Contains(lsGroupings, o.groupBy) {
		return userErr("--group-by %q: use one of %s", o.groupBy, strings.Join(lsGroupings, ", "))
	}
	// A grouped listing is a reading of the whole window -- the totals and the
	// shares are the window's -- so a page of it or a redraw of it would be a
	// figure computed over something else.
	if o.groupBy != "" && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("after")) {
		return userErr("--group-by reads the whole window: a page of a grouped listing is a page's total, so --limit and --after don't go with it")
	}
	if o.groupBy != "" && o.watch {
		return userErr("--group-by reads the whole window: --watch redraws the listing until every run has settled, so the two don't go together")
	}
	// The feed is JSON lines -- one element of --json's runs array per line --
	// so --stream without --json would be a listing in a shape the flag does
	// not write, and the two combinations below are documents the feed has no
	// line for: a redraw of the listing, and a block beside the rows.
	if o.stream && !o.asJSON {
		return userErr("--stream needs --json: the feed is JSON lines, one per run of the document's runs array")
	}
	if o.stream && o.watch {
		return userErr("--stream prints the listing once: --watch redraws it until every run has settled, so the two don't go together")
	}
	if o.stream && o.groupBy != "" {
		return userErr("--stream has no line for --group-by's block: the feed carries the document's runs, and the block is a reading of the whole window")
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	f := lsFilter{batch: o.batch, state: o.state, debt: o.debt, pr: o.pr, warn: cmd.ErrOrStderr(), noCache: o.noCache, indexNote: true}
	if prSet && o.repo == "" && len(env.lc.Repos) != 1 {
		return userErr("runs ls --pr needs --repo: the local config lists %d repositories", len(env.lc.Repos))
	}
	// The flags are read as typed rather than by value (--since's default is a
	// window, and a --since that was typed is a bound the listing keeps), and
	// they are what tells the two listings apart: one the run index answers and
	// one that reads the bucket.
	sinceSet, limitSet := cmd.Flags().Changed("since"), cmd.Flags().Changed("limit")
	indexed := indexListing(o)
	// The repositories the local config lists, resolved once: the index reads an
	// empty set as every repository of the installation and the window reads one
	// as none, so both are handed the same list rather than resolving it twice.
	conf := configSlugs(ctx, env, cmd.ErrOrStderr())
	var warnings []string
	if indexed {
		var about []string
		if f.slugs, about, err = lsIndexSlugs(ctx, env, o, conf); err != nil {
			return err
		}
		warnings = imageWarnings(ctx, env, about, time.Now().UTC())
	} else {
		if f.slugs, err = lsSlugsOf(ctx, env, o, conf, cmd.ErrOrStderr()); err != nil {
			return err
		}
		warnings = imageWarnings(ctx, env, f.slugs, time.Now().UTC())
	}
	if prSet && len(f.slugs) != 1 {
		return userErr("runs ls --pr needs --repo: %d repositories to list", len(f.slugs))
	}
	if o.mine {
		if f.mine, err = env.lc.Me(ctx); err != nil {
			return userErr("--mine: %v", err)
		}
	}
	gate, err := accessRowsGate(ctx, env)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	clear := o.watch && !o.asJSON && isTTY(out)
	for {
		now := lsNow()
		if since > 0 {
			f.since = now.Add(-since)
		}
		read, err := readListing(ctx, env, gate, f, o, conf, sinceSet, limitSet, now)
		if err != nil {
			return err
		}
		if clear {
			fmt.Fprint(out, "\x1b[H\x1b[2J")
		}
		if o.stream {
			err = streamRows(out, read.page, read.index)
		} else if o.groupBy != "" {
			err = printGroupsWithTotals(out, env.lc.Name, read.page, read.tot, warnings, now, o.asJSON)
		} else {
			err = printRowsWithTotals(out, env.lc.Name, read.page, read.tot, read.index, warnings, now, o.asJSON)
		}
		if err != nil {
			return err
		}
		if !o.watch || allSettled(read.rows) {
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
// the bucket), else the local config's, else every slug. It is lsSlugsOf with
// the config's own list resolved here, which is what a caller that only wants
// the list hands it.
func lsSlugs(ctx context.Context, env *cloudEnv, o *lsOptions, warn io.Writer) ([]string, error) {
	return lsSlugsOf(ctx, env, o, configSlugs(ctx, env, warn), warn)
}

// lsSlugsOf is lsSlugs with the config's own list given: conf is what
// configSlugs answered, and a listing that resolves it once hands the same list
// to the index and to the window rather than resolving it twice (runLs).
func lsSlugsOf(ctx context.Context, env *cloudEnv, o *lsOptions, conf []string, warn io.Writer) ([]string, error) {
	if o.repo != "" {
		slug, err := env.repoSlug(o.repo, checkoutOf(ctx, o.repo))
		if err != nil {
			return nil, err
		}
		return []string{slug}, nil
	}
	if !o.all && len(conf) > 0 {
		return conf, nil
	}
	slugs, err := runstore.ListSlugs(ctx, env.bucket.Bucket)
	if err != nil {
		return nil, remote(err)
	}
	return slugs, nil
}

// lsIndexSlugs are the repositories a listing asks the run index for, and the
// repositories its image warnings are about: the two are not always one set.
//
// The ask is the one the index's own filter takes, where no repository at all
// means every repository of the installation -- which is what --all means, and
// what a local config that lists none means -- so a listing that reads the
// index resolves its repositories without the one listing of the runs bucket
// that finding out every slug costs (D3203). The warnings are about the
// configured repositories, since those are the ones whose image this checkout
// knows the workflow of (imageWarnings).
func lsIndexSlugs(ctx context.Context, env *cloudEnv, o *lsOptions, conf []string) (ask, about []string, err error) {
	if o.repo != "" {
		slug, err := env.repoSlug(o.repo, checkoutOf(ctx, o.repo))
		if err != nil {
			return nil, nil, err
		}
		return []string{slug}, []string{slug}, nil
	}
	if o.all {
		return nil, conf, nil
	}
	return conf, conf, nil
}

// configSlugs are the slugs of the repositories the local config lists,
// resolved locally: a name that cannot be resolved is skipped with a warning
// rather than listed under a name this checkout cannot vouch for.
func configSlugs(ctx context.Context, env *cloudEnv, warn io.Writer) []string {
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
	return slices.Compact(slugs)
}

// windowRefs is every run of the window f names, per repository: one listing
// per slug, which is the listing every other path narrows -- the live set by
// the executions that are still running, --pr by the pull request's index.
// What it answers is what a listing read before either of those paths existed.
func windowRefs(ctx context.Context, env *cloudEnv, f lsFilter) ([]lsRef, error) {
	var refs []lsRef
	for _, slug := range f.slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, f.since)
		if err != nil {
			return nil, remote(err)
		}
		for _, id := range ids {
			refs = append(refs, lsRef{slug, id})
		}
	}
	return refs, nil
}

// loadRows reads the runs f selects, joins each with its execution, and
// returns the rows newest first. It reads the whole window whatever the
// listing goes on to print: the totals cover every row it read, so a page is
// never the sum beside it (D818). A run whose launch.json or
// result.json is corrupt, or names an execution that isn't the run's, is an
// error row with a warning. Any other failed read but absence fails the whole
// listing: ls never shows a status guessed from a partial read.
//
// Which runs it reads is the filter's: every run of the window, the live set
// for the two live states, one run for a run reference, and for f.pr the runs
// the pull request's index names, the runs of the last day and the runs that
// are going (prRefs) -- falling back to the window when the index has nothing
// to say about the pull request.
//
// Each run is read objects first -- but the two f.pr cannot need, for a
// run whose own task names another pull request (taskOnOtherPR) -- and keeps the
// runs f.pr selects, and the review run going on that pull request, which the
// fold needs and the listing does not print, and only then asks the backend
// about executions, so a PR's listing costs no backend call for the runs off
// it. What --pr and --debt leave out is dropped after the fold and never
// before it: a state is the pull request's, read from every run of it the
// listing read, so the rows they print carry the state their pull request has
// and not the state of the selection.
func loadRows(ctx context.Context, env *cloudEnv, f lsFilter, now time.Time) ([]runview.Row, error) {
	warnf := lsWarnf(f.warn)
	refs, live, err := selectRefs(ctx, env, f, now, warnf)
	if err != nil {
		return nil, err
	}
	return readRefRows(ctx, env, f, refs, live, now, warnf)
}

// lsWarnf is the writer a listing's notes and warnings go through: they are
// one line each, and the reads that write them are parallel, so the writer is
// serialised. A nil writer discards them, which is what a caller reading rows
// for a decision rather than for a person hands it.
func lsWarnf(w io.Writer) func(string, ...any) {
	if w == nil {
		w = io.Discard
	}
	var mu sync.Mutex // guards w
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, multiLine(fmt.Sprintf(format, args...)))
	}
}

// selectRefs is the runs a filter names, and the live set when it named one of
// the two live states: every run of the window, the live set's refs, one run
// for a run reference, and for f.pr the runs the pull request's index names,
// the runs of the last day and the runs that are going (prRefs) -- falling back
// to the window when the index has nothing to say about the pull request.
func selectRefs(ctx context.Context, env *cloudEnv, f lsFilter, now time.Time, warnf func(string, ...any)) ([]lsRef, *liveSet, error) {
	var refs []lsRef
	// live is set when --state named one of the two live states: the refs are
	// then the live set, and its executions are the join's (liveRuns).
	var live *liveSet
	switch {
	case f.runRef != "":
		slug, id, err := parseRunRef(f.runRef)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, lsRef{slug, id})
	case liveState(f.state):
		set, err := liveRuns(ctx, env, f, now)
		if err != nil {
			return nil, nil, err
		}
		live, refs = &set, set.refs
	case f.pr != 0:
		// --pr reads the pull request's index, the runs of the last day and the
		// runs that are going; only what those name is read, and every run of
		// them is checked against its own records below (D2962, D2963, D2964).
		// A pull request the index does not know -- everything that ran before
		// it, an installation that has not been through an update -- reads the
		// window as it always did, and prRefs says so in one line on stderr.
		r, err := prRefs(ctx, env, f, now, warnf)
		if err != nil {
			return nil, nil, err
		}
		if r == nil {
			if r, err = windowRefs(ctx, env, f); err != nil {
				return nil, nil, err
			}
		}
		refs = r
	default:
		r, err := windowRefs(ctx, env, f)
		if err != nil {
			return nil, nil, err
		}
		refs = r
	}
	return refs, live, nil
}

// readRefRows reads the runs refs names, joins each with its execution, and
// returns the rows newest first. It reads the whole window whatever the listing
// goes on to print: the totals cover every row it read, so a page is never the
// sum beside it (D818). A run whose launch.json or result.json is corrupt, or
// names an execution that isn't the run's, is an error row with a warning. Any
// other failed read but absence fails the whole listing: ls never shows a
// status guessed from a partial read. live is the set of a live listing, whose
// executions are the join's, and nil otherwise.
//
// The order of what it returns is the listing's own -- created descending, then
// the run reference descending. refs is what a listing asked for and comes back
// in that order either way, but a page the run index answered is a position in
// the index's order rather than in this one (readIndex), so its caller keeps
// the rows in the order they came in: --after resolves a run id to that index
// sort key, and rows printed in another order would drop the runs between two
// pages.
func readRefRows(ctx context.Context, env *cloudEnv, f lsFilter, refs []lsRef, live *liveSet, now time.Time, warnf func(string, ...any)) ([]runview.Row, error) {
	// The bucket's objects, for every run, through the run cache when this
	// listing asked for it. One flat listing per repository carries every
	// run's cached objects' generations, so the runs the previous listing
	// already read are answered locally and the reads that leave the machine
	// are the objects that changed.
	//
	// A live listing reads the bucket: an index is built from a listing of
	// every object of the repository, which is the cost the live set was read
	// to avoid, and a repository with a long history is exactly where a live
	// run would be answered from a cache it took a full listing to build.
	//
	// The same answer decides whether a run may be read from its summary: a
	// summary belongs to the run cache -- it is the listing's own shortcut
	// over the run's objects, and a run cache that has been through an update
	// holds it from the same listing the three generations came from -- and a
	// listing that asked for the bucket as it is now (--no-cache, every
	// caller that decides rather than displays: a follow-up gate, a review
	// scope, a budget read) reads the run's three objects, as it did before
	// there were summaries.
	useCache := live == nil && !f.noCache
	readers := lsReaders(ctx, env, useCache, refs)
	runs := make([]runObjects, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		runs[i], errs[i] = readRunVia(ctx, env, readers[refs[i]], refs[i].slug, refs[i].id, f.pr, useCache)
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}
	// A run left out warns only when it might be on the PR: its record
	// is unreadable and its task doesn't place it on another one.
	for _, r := range runs {
		if r.warning != "" && (f.pr == 0 || onPR(r.in, f.pr) || r.mayBeOnPR()) {
			warnf("%s", r.warning)
		}
	}
	if f.pr != 0 {
		// The pull request's own runs, and the review run going on it: while a
		// review is going its task is the only thing that places it on a pull
		// request (its record names one only at the artifact stage, see the
		// fold below), and the state of a pull request is read from every run
		// of it, so a review going is what makes it live. The review row is
		// kept for the fold alone and dropped before the listing prints: --pr
		// lists the pull request's own runs, and a review of it is not one of
		// them (stateOnly). Keeping it here rather than joining the whole
		// window is what leaves --pr costing no backend call for the runs off
		// the pull request.
		kept := runs[:0]
		for _, r := range runs {
			switch {
			case onPR(r.in, f.pr):
				kept = append(kept, r)
			case reviewPR(r.in.Task) == f.pr:
				r.stateOnly = true
				kept = append(kept, r)
			}
		}
		runs = kept
	}
	if len(runs) == 0 {
		return []runview.Row{}, nil
	}

	// One listing covers most runs' executions; it reaches an hour further
	// back than the runs, since a run ID is minted before its launch only
	// by moments, but clocks differ.
	//
	// A live listing made that call already, and the runs it read are the runs
	// that listing found: its answer is the join's, so the two states cost one
	// backend call and never two (U2).
	execs := map[string]backend.Execution{}
	// listed is set only when the listing below is the whole of the runs it is
	// joined with -- every one of them inside its floor: an execution it lacks
	// is then one the platform no longer holds, which is what a read of it
	// would answer, one call per run, for every run whose execution Cloud Run
	// has already deleted.
	listed := false
	switch {
	case live != nil:
		execs = live.execs
	case f.runRef == "" && slices.ContainsFunc(runs, func(r runObjects) bool { return r.execution != "" }):
		// The floor is the oldest run joined and not the flags' own --since:
		// the page of an index-answered listing is the newest runs plus every
		// active one whatever its age (D3203), so a floor taken from --since
		// leaves an older run's live execution out of a listing that is then
		// trusted as the whole of them, and that run joins with no execution at
		// all -- an infra_error where the execution says running, a preempted
		// run stripped of what the execution derived.
		lf := backend.ListFilter{}
		lf.Since = executionsSince(runs)
		list, err := env.be.List(ctx, lf)
		if err != nil {
			return nil, remote(fmt.Errorf("listing executions: %w", err))
		}
		for _, e := range list {
			if id, ok := backend.ParseExecution(e.Name); ok {
				execs[id.Key()] = e
			}
		}
		listed = true
	}

	// The price book warns through warnf too: a VM run whose region or
	// machine type the price table does not carry is listed with compute not
	// estimated, and this is where the operator reads why (design
	// compute-backend.md §5.2).
	prices := env.prices(func(s string) { warnf("%s", s) })
	rows := make([]runview.Row, len(runs))
	errs = make([]error, len(runs))
	parallelN(len(runs), followWorkers, func(i int) {
		if errs[i] = followExecution(ctx, env, &runs[i], execs, listed, warnf); errs[i] == nil {
			rows[i] = runview.Join(runs[i].in, prices, now)
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}

	// The pull request each review run of the window is on, by run id: a
	// review run's record carries no pull request until the artifact stage
	// writes one, which is what ends the review
	// (internal/runner/reviewartifact.go), so while the review is going its
	// task is the only thing that says which pull request it is on, and its
	// artifact is one verdict about one head (reviewPR).
	reviewing := map[string]int{}
	for i := range rows {
		if pr := reviewPR(runs[i].in.Task); pr != 0 {
			reviewing[rows[i].RunID] = pr
		}
	}
	// The run that resumed each paused run of the window, by the paused run's
	// key: the resumer's own task names the paused run under `resumes`
	// (task.Spec.Resumes, contract-pause R17), and the paused run's chain
	// cannot — ResolveChain walks backwards, so a paused run's chain holds its
	// ancestors and never the run the orchestrator launched to pick it up.
	// Built before the filters below narrow the rows, like the states beside
	// it: a resumer is often a run those filters drop, and the paused row it
	// names still reads who resumed it. The first in the listing's own order
	// wins, which is the newest launch (newerRow) — two runs naming one pause
	// is a state the resume guards exist to prevent, and the listing must not
	// flip between two answers on consecutive reads.
	resumers := map[string]string{}
	for i := range rows {
		if t := runs[i].in.Task; t != nil && t.Resumes != "" {
			key := pauseRowKey(rowRef(rows[i]).Slug, t.Resumes)
			if _, ok := resumers[key]; !ok {
				resumers[key] = rows[i].RunID
			}
		}
	}
	// The state of each pull request the window holds, set before the filters
	// below pick the page: a state is the pull request's, read from every run
	// of it the listing read, so it is the same on every row of a page as on
	// the rows the filters below dropped. The pause of a paused row is stamped
	// here too, and from the same lookup: the state of its pull request and the
	// instant its own row shows are two questions about one node, and the
	// lookup reads it once (pauseFactsFor).
	pauses := pauseFactsFor(ctx, env, warnf)
	markPRStates(rows, reviewing, pauses)
	markPauses(rows, resumers, pauses)
	// A review run --pr kept for the fold alone is not a row it prints: the
	// state every row of the pull request carries is read from it, and the
	// review itself is not one of the pull request's own runs. rows is runs
	// joined one for one, so the mark travels by index.
	if f.pr != 0 {
		kept := rows[:0]
		for i, r := range rows {
			if !runs[i].stateOnly {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	// The debt filter reads the task and nothing else, and it is applied after
	// the fold rather than beside the reads because the mark is
	// task.Spec.Debt, which no row carries: the runs it leaves out are still
	// runs of the pull requests its rows carry, so their state is read from
	// them too. A run whose task.json is unreadable has no task to say it is
	// one -- it is left out rather than guessed into a listing of the work
	// nobody asked for.
	if f.debt {
		kept := rows[:0]
		for i, r := range rows {
			if runs[i].in.Task != nil && runs[i].in.Task.Debt {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	rows = slices.DeleteFunc(rows, func(r runview.Row) bool {
		return (f.mine != "" && r.RequestedBy != f.mine) || (f.batch != "" && r.Batch != f.batch) ||
			(f.state != "" && !rowInState(r, f.state))
	})
	// The listing's order, which --after's cursor is defined by: created
	// descending, then the run reference descending. Two runs minted in the
	// same second -- which is what the reference's own mint time leaves
	// unordered -- are ordered by that reference, so the order is total and
	// the row a cursor stops at is the row a reader sees.
	slices.SortStableFunc(rows, func(a, b runview.Row) int {
		if c := b.Created.Compare(a.Created); c != 0 {
			return c
		}
		return strings.Compare(b.Run, a.Run)
	})
	return rows, nil
}

// pageRows is the page a listing prints out of the rows it read: the rows
// strictly after the cursor in the listing's own order, then the newest limit
// of them. The totals are the caller's to take over the whole window, never
// over what this returns.
//
// A cursor the window does not hold is refused rather than answered with an
// empty page. The two are not the same answer: an empty page reads as "no more
// runs", and a caller that walked off the end of the window by mistake -- a
// filter that dropped the cursor's run, a --since that cut it off -- would
// page nothing for ever.
func pageRows(rows []runview.Row, after string, limit int) ([]runview.Row, error) {
	if after != "" {
		i := slices.IndexFunc(rows, func(r runview.Row) bool { return r.RunID == after })
		if i < 0 {
			return nil, userErr("--after %s: no run in this listing has that id", after)
		}
		rows = rows[i+1:]
	}
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows, nil
}

// lsIndexPage is how many runs of the default listing the index is asked for
// beside the active ones: the newest lsIndexPage by the index's own sort key,
// whatever their age, plus every run that has not ended (D3203). It is what
// makes the default listing's reads bounded by a number and not by history --
// 100x the history costs the same handful of requests, and every row read costs
// its own objects (I-INDEX's TestIndexedReadsConstantRequests).
const lsIndexPage = 50

// indexAnswers reports whether a page came from an index that can be believed.
//
// An index that names no run, has folded nothing and has never finished a walk
// is an index with nothing to answer with: that is the state internal/runindex's
// stub answers in, and the state an installation is in before its first reindex
// has folded anything. A listing that took it as the truth would print an empty
// list where the bucket holds runs -- the one thing the index's own contract
// forbids -- so it reads the window instead, as every listing did before the
// index existed. Every other answer is the index's, including a folded index
// whose page is empty because the filter matched nothing.
func indexAnswers(p runindex.Page) bool {
	return len(p.Entries) > 0 || p.FoldedThrough != nil || p.IndexComplete
}

// indexListing reports whether this listing is one the run index answers: it is
// the default listing -- the newest lsIndexPage by time plus every active run
// (D3203) -- a listing bounded by --since, --mine or --batch, and every listing
// that prints a page of one of them (--limit, --after, --stream).
//
// The flags below keep the window read, and each for its own reason: --state
// and --debt filter on facts no entry carries (the state a pull request has,
// which is read from every run of it, and the debt mark, which is in the task),
// --group-by reads a whole window rather than a page of one, and --watch
// redraws until every run it read has settled, which a page of an index is not
// a reading of. Their rows are the rows they always were.
func indexListing(o *lsOptions) bool {
	return !o.watch && o.state == "" && !o.debt && o.groupBy == "" && o.pr == 0
}

// lsIndexState is what a listing prints about the index it read: whether a walk
// has ever reached the end of history (D3215) and the moment the index is
// folded through, both as the page it read carried them. It is nil for a
// listing the window answered, and the two are then absent from --json's
// document and unmentioned in the text listing: a listing that read no index
// has nothing to say about one.
type lsIndexState struct {
	complete      bool
	foldedThrough *time.Time
}

// lsRead is what a listing holds after it has read: every row it knows about --
// the figure beside the page and the pull request states its rows carry are
// read from all of them, never from the page alone (D818) -- the page it prints,
// that figure, and what the index said of itself.
type lsRead struct {
	rows  []runview.Row
	page  []runview.Row
	tot   runview.Totals
	index *lsIndexState
}

// readListing reads the runs a listing prints: from the run index when this
// listing is one it answers (indexListing) and the index has something to say
// (indexAnswers), and from the window otherwise -- the flags above, and every
// answer of an index that has nothing in it or could not be read.
//
// The rows come back with the access gate applied (D1586) and the page already
// cut, which is the one thing the two paths do differently: the window's page
// is cut here, by the cursor and the limit, while a page the index answered is
// the reader's own -- its cursor is a position in the index's order
// (runindex.Filter.After), which the rows a listing read do not carry.
func readListing(ctx context.Context, env *cloudEnv, gate accessRowGate, f lsFilter, o *lsOptions, conf []string, sinceSet, limitSet bool, now time.Time) (lsRead, error) {
	indexed := indexListing(o)
	if indexed {
		if idx, err := runIndexReader(ctx, env); err != nil {
			return lsRead{}, remote(err)
		} else if idx != nil {
			if read, ok := readIndex(ctx, env, idx, f, o, sinceSet, limitSet, now); ok {
				return gateRead(gate, read, f.warn), nil
			}
		}
		// The window reads the repositories its own way: the index asked for
		// every repository of the installation with an empty set, and a window
		// that read that as no repository at all would list nothing, so the
		// fallback resolves them as every listing always has.
		var err error
		if f.slugs, err = lsSlugsOf(ctx, env, o, conf, f.warn); err != nil {
			return lsRead{}, err
		}
	}
	rows, err := loadRows(ctx, env, f, now)
	if err != nil {
		return lsRead{}, err
	}
	rows, dropped := gate.listed(rows)
	noteDropped(f.warn, dropped)
	page, err := pageRows(rows, o.after, o.limit)
	if err != nil {
		return lsRead{}, err
	}
	return lsRead{rows: rows, page: page, tot: runview.Sum(rows)}, nil
}

// gateRead is read with the access gate applied to it: the rows a credential
// holds no role in are dropped from what the listing knows and from the page it
// prints, and what that left out is said in one line (D1586's read half).
//
// A figure the gate has changed is recomputed from the rows it left: the
// summary document counts every repository of the installation, so a listing
// that dropped a repository's rows and kept the summary's total would report
// the spend of repositories the reader holds no role in.
func gateRead(gate accessRowGate, read lsRead, warn io.Writer) lsRead {
	var dropped int
	read.rows, dropped = gate.listed(read.rows)
	if read.page != nil {
		read.page, _ = gate.listed(read.page)
	}
	if dropped > 0 {
		read.tot = runview.Sum(read.rows)
	}
	noteDropped(warn, dropped)
	return read
}

// noteDropped says what the access gate left out of a listing. It prints
// nothing when it left out nothing, which is every listing of a credential that
// holds a role in every repository it read.
func noteDropped(warn io.Writer, dropped int) {
	if dropped > 0 {
		lsNotef(warn, "note: %d of the runs read are of repositories this credential holds no role in, and are not listed here: the roles come from the credential (finding F21), and '%s user ls --repo <repo>' prints what it holds there\n", dropped, selfCommand())
	}
}

// lsNotef writes one of a listing's notes, with the treatment of embedded
// newlines every other line of a listing gets.
func lsNotef(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprint(w, multiLine(fmt.Sprintf(format, args...)))
}

// readIndex answers a listing from the run index, and reports false when the
// listing is the window's after all: an index that has nothing to answer with
// (indexAnswers) or a reader that failed. A failed read is said in one line and
// answered the same way, because a listing that printed a short page as the
// truth is exactly what the index's contract forbids (D3215) -- and the window
// is what every listing read before the index existed.
//
// Which shape it asks for is the listing's own. The default listing -- nothing
// bounds it -- is the newest lsIndexPage by the index's sort key plus every
// active run, whatever its age, and the figure beside it is the summary's: the
// whole installation's, because a page of the newest runs is not a total
// (D3203, D818). A listing that bounds itself -- --since, --mine, --batch --
// reads every run it bounds and totals those rows, which is what those flags
// have always meant, and the page is cut here so that the figure beside it
// stays the window's and never the page's.
//
// The summary is the figure only for a listing that covers the whole
// installation: one narrowed to some of its repositories (--repo, or a local
// config that lists them) totals its own rows, because the summary counts every
// repository's runs and spend and a listing of one repository's rows beside the
// installation's figure would report money the reader did not ask for (D1586's
// read half, and the same rule the window has always kept).
func readIndex(ctx context.Context, env *cloudEnv, idx runindex.Reader, f lsFilter, o *lsOptions, sinceSet, limitSet bool, now time.Time) (lsRead, bool) {
	bounded := sinceSet || f.mine != "" || f.batch != ""
	filter := runindex.Filter{Slugs: f.slugs, Since: f.since, Mine: f.mine, Batch: f.batch, After: o.after}
	if !bounded {
		// The listing that bounds nothing is the newest lsIndexPage by the
		// index's sort key, whatever its age (D3203), so it asks for no window:
		// the default --since is the window a listing that reads the bucket is
		// bounded by (lsDefaultWindow), and it is not this read's bound. An
		// explicit --limit that names a count is that many rows instead.
		//
		// --limit 0 names none, and it keeps the page's bound rather than
		// dropping it: every row of this listing is the reader's own page, and
		// a limit of none is not a reason to ask the index for every match --
		// that would read every run's objects over all history, one run at a
		// time and with no run cache, which is the read this path exists to
		// remove and which the bucket path never does either (a limit cuts the
		// rows it printed, and the window still bounded the read).
		filter.Since = time.Time{}
		filter.Limit = lsIndexPage
		if limitSet && o.limit > 0 {
			filter.Limit = o.limit
		}
	}
	entries, state, ok := readIndexEntries(ctx, idx, filter, f.warn)
	if !ok {
		return lsRead{}, false
	}
	if !bounded && o.after == "" {
		// The active runs, whatever their age: a run that has not ended is in
		// the listing however far back it started, which is the half of D3203
		// the newest-limit alone would drop. It is the same filter without the
		// limit, since a limit is what "the newest N" is and not what "every
		// active run" is.
		//
		// The first page alone asks. Every active run is on it whatever its age,
		// so a page after one is paging a listing that has already listed them
		// all: asking again would put an old active run at the end of the page
		// the cursor comes from, and the next --after would then start after a
		// run older than every other row of the page, leaving the runs between
		// the two unlisted for good (mergeIndexEntries, and D3203's own "newest
		// N plus the active" is a page of the listing rather than a rule about
		// every page of it).
		//
		// The question is an addition and not a second opinion: the rows the
		// first one answered are a listing either way, so an index with nothing
		// to say about the active runs is a listing with no run going rather
		// than one that reads the window (an index that has folded runs and
		// written no summary is exactly that). A reader that failed is a read
		// that did not happen, and half a listing is not a listing: the window
		// answers it whole, as it does for a failure of the first question.
		active := filter
		active.Active, active.Limit = true, 0
		more, _, answered, err := readIndexPage(ctx, idx, active)
		if err != nil {
			noteIndexUnreadable(f.warn, err)
			return lsRead{}, false
		}
		if answered {
			entries = mergeIndexEntries(entries, more.Entries)
		}
	}
	refs := make([]lsRef, 0, len(entries))
	for _, e := range entries {
		if e.Slug != "" && e.RunID != "" {
			refs = append(refs, lsRef{e.Slug, e.RunID})
		}
	}
	// The runs the index named are read one by one, with no run cache: the
	// cache is keyed by a flat listing of every object of a repository
	// (runcache.List), and that listing is exactly what this path exists to
	// avoid -- the index has already said which runs to read, so asking the
	// bucket for the generations of all of them would be reading the window
	// again by another name.
	f.noCache = true
	rows, err := readRefRows(ctx, env, f, refs, nil, now, lsWarnf(f.warn))
	if err != nil {
		return lsRead{}, false
	}
	// The order is the index's own, which its --after cursor is a position in.
	// The rows come back in it already -- refs is in that order and the fold
	// keeps it -- but only here is the order a fact of this read rather than of
	// the reader: a page printed in another order would drop the runs between
	// two pages, since --after resolves a run id to the index's sort key.
	at := make(map[string]int, len(entries))
	for i, e := range entries {
		at[e.RunID] = i
	}
	slices.SortStableFunc(rows, func(a, b runview.Row) int { return at[a.RunID] - at[b.RunID] })
	read := lsRead{rows: rows, page: rows, tot: runview.Sum(rows), index: &state}
	if bounded && o.limit > 0 && o.limit < len(read.page) {
		// The page is cut here rather than asked for in the filter, so that the
		// figure beside it stays the window's and never the page's (D818). The
		// default listing needs no cut: its page is the reader's own -- the
		// newest runs it was asked for, plus every active run beside them.
		read.page = read.page[:o.limit]
	}
	if !bounded && len(f.slugs) == 0 {
		// The listing that asks the index for no repository at all is the one
		// that covers the installation, which is what the summary document is
		// the figure of. One that names its repositories is a listing of them
		// and keeps the figure its rows give it.
		tot, ok := readIndexTotals(ctx, idx, f.warn)
		if !ok {
			return lsRead{}, false
		}
		read.tot = tot
	}
	return read, true
}

// readIndexEntries is one page of the index, and what the page says about the
// index itself (runindex.Page). It reports false when the index has nothing to
// answer with, or when the reader failed, having said so in one line: every
// caller then reads the window as it did before the index existed.
func readIndexEntries(ctx context.Context, idx runindex.Reader, filter runindex.Filter, warn io.Writer) ([]runindex.Entry, lsIndexState, bool) {
	page, state, answered, err := readIndexPage(ctx, idx, filter)
	if err != nil {
		noteIndexUnreadable(warn, err)
		return nil, lsIndexState{}, false
	}
	if !answered {
		return nil, lsIndexState{}, false
	}
	return page.Entries, state, true
}

// readIndexPage is one question put to the index and nothing decided about it:
// the page, what the page said about the index itself, and whether the index
// had anything to answer with at all (indexAnswers). False there is an index
// that has folded nothing and finished no walk -- the stub's shape, and an
// installation before its first fold -- and never an index whose filter matched
// nothing, which answers with the summary's own state beside an empty page
// (readIndexEntries and readIndex's second question read it differently, and
// that is why the two are told apart here rather than collapsed into one).
func readIndexPage(ctx context.Context, idx runindex.Reader, filter runindex.Filter) (runindex.Page, lsIndexState, bool, error) {
	page, err := idx.List(ctx, filter)
	if err != nil {
		return runindex.Page{}, lsIndexState{}, false, err
	}
	if !indexAnswers(page) {
		return runindex.Page{}, lsIndexState{}, false, nil
	}
	return page, lsIndexState{complete: page.IndexComplete, foldedThrough: page.FoldedThrough}, true, nil
}

// noteIndexUnreadable is the one line a listing says when the index could not
// be read: it is about to read the window, as every listing did before the
// index existed.
func noteIndexUnreadable(warn io.Writer, err error) {
	lsNotef(warn, "note: the run index could not be read: %v; listing the window instead, as it did before the index existed\n", oneLine(err.Error()))
}

// readIndexTotals is the figure beside a listing the index answered: the
// summary document's, which covers every run the installation's index carries
// and not the page (D3203). A summary that cannot be read is a listing that
// reads the window instead -- the figure is not optional, and a page printed
// with no figure beside it is the one thing a reader would notice.
func readIndexTotals(ctx context.Context, idx runindex.Reader, warn io.Writer) (runview.Totals, bool) {
	summary, err := idx.Summary(ctx)
	if err != nil {
		lsNotef(warn, "note: the run index's summary could not be read: %v; listing the window instead, as it did before the index existed\n", oneLine(err.Error()))
		return runview.Totals{}, false
	}
	return lsSummaryTotals(summary), true
}

// mergeIndexEntries is the default listing's page: the runs that are still
// going and are not part of the newest page first, then the newest runs, each
// part in the index's own order -- updated_at descending, then the run id
// (runindex.Page), which is the order a cursor is a position in.
//
// The active runs lead because the last row of the page is the row --after
// takes the next page from, and it has to be a row inside the newest-N page: an
// active run older than every row of that page sorts after all of them, so a
// listing that printed it last would hand on a cursor two hundred days old and
// the runs between the two would never be listed. The runs that are still going
// are the ones a reader must not miss, whichever page they are on, and the
// first page is the one that carries them all.
func mergeIndexEntries(newest, active []runindex.Entry) []runindex.Entry {
	seen := make(map[string]bool, len(newest)+len(active))
	newestOnly := make([]runindex.Entry, 0, len(newest))
	for _, e := range newest {
		if e.RunID == "" || seen[e.RunID] {
			continue
		}
		seen[e.RunID] = true
		newestOnly = append(newestOnly, e)
	}
	extra := make([]runindex.Entry, 0, len(active))
	for _, e := range active {
		if e.RunID == "" || seen[e.RunID] {
			continue
		}
		seen[e.RunID] = true
		extra = append(extra, e)
	}
	slices.SortStableFunc(newestOnly, indexEntryOrder)
	slices.SortStableFunc(extra, indexEntryOrder)
	out := make([]runindex.Entry, 0, len(newestOnly)+len(extra))
	out = append(out, extra...)
	out = append(out, newestOnly...)
	return out
}

// indexEntryOrder is the index's own order: updated_at descending, then the run
// id (runindex.Page). Both halves of a page are one slice of entries a reader
// answered, and the order is put back here rather than trusted, since --after
// resolves a run id to that sort key and a page in another order would drop the
// runs between two pages.
func indexEntryOrder(a, b runindex.Entry) int {
	if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.RunID, b.RunID)
}

// lsSummaryTotals is the figure beside a listing the summary answered, in the
// shape a row's cost block sums to. The money is integer micro-dollars in the
// document -- the unit the fold moves and `runs summary` publishes -- and a
// float here, so the conversion is in one place. ComputeMicros absent is "not
// estimated" and not zero (design §10.1): the figure prints as $0.00 either way,
// and ComputeNotEstimated is what says which runs it covers.
func lsSummaryTotals(s runindex.Summary) runview.Totals {
	tot := runview.Totals{
		Runs:                int(s.Runs),
		ModelUSD:            budget.Micros(s.SpentMicros).USD(),
		ModelNotionalUSD:    budget.Micros(s.NotionalMicros).USD(),
		ComputeNotEstimated: int(s.ComputeNotEstimated),
	}
	if s.ComputeMicros != nil {
		tot.ComputeUSD = budget.Micros(*s.ComputeMicros).USD()
	}
	tot.TotalUSD = tot.ModelUSD + tot.ComputeUSD
	return tot
}

// lsIndexBuildingLine is the one line a text listing prints while the index is
// still building (D3215): the rows are the ones the index has folded and the
// ones its overlay carries, and a listing that printed them without saying so
// would read as the whole history. The moment it is folded through is in the
// line when the index has one, since that is what says how much of the recent
// past the rows may be missing.
func lsIndexBuildingLine(index *lsIndexState) string {
	line := "note: the run index is still building: this listing is not the whole history yet"
	if index.foldedThrough != nil {
		line += ", and the index is folded through " + index.foldedThrough.UTC().Format(time.RFC3339)
	}
	return line
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

// reviewPR is the pull request a review run reviews: a review names it in
// Spec.ReviewPRs, and only a review of exactly one is on a single pull request
// -- a review of several is on none of them (continuesPRChain reads a review
// launch the same way, chain_continues.go). 0 for any other run.
func reviewPR(t *task.Spec) int {
	if t == nil || len(t.ReviewPRs) != 1 {
		return 0
	}
	return t.ReviewPRs[0]
}

// mayBeOnPR reports whether a run onPR leaves out could still be on a
// pull request: its launch.json or result.json is unreadable, so its
// record wasn't read, and no task says it follows another PR.
func (r runObjects) mayBeOnPR() bool {
	return r.unreadRecord && (r.in.Task == nil || r.in.Task.PR == 0)
}

// taskOnOtherPR reports whether a run's task alone places it on a pull request
// other than pr: pr is the one a --pr listing asked for, and the task names a
// different one.
//
// Nothing else about the run can move it back onto pr. task.Validate writes a
// spec's pr, branch and previous_run together and refuses them beside
// review_prs, so a task that names a pull request is a follow-up; and a
// follow-up's record names the pull request its own task does and never
// another, because the runner records that number from the task, and only once
// the pull request answered with the task's own branch
// (internal/runner/followup.go's checkPullRequest). A run on some other pull
// request is therefore one --pr leaves out whichever way it is read, and one
// no warning of its can reach a --pr listing either (mayBeOnPR is false for a
// task that names a pull request).
func taskOnOtherPR(t *task.Spec, pr int) bool {
	return pr != 0 && t != nil && t.PR != 0 && t.PR != pr
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
	// summarized means the run's task and record came from its own summary
	// object, so a later re-read of the record goes back to the summary and
	// never to a result.json the rest of the row was not built from.
	summarized bool
	// stateOnly means --pr kept the run for the fold alone: a review run in
	// flight on the pull request is a run of it, and not a row --pr lists.
	stateOnly bool
}

// readRun reads what the bucket knows about one run; it makes no backend
// call. Every object comes from the bucket: a caller that wants the run
// cache passes its own reader to readRunVia. It reads the run's three
// objects and never its summary: a caller of this one wants the run itself
// (a digest, an estimate, a gate), not the row a listing prints.
func readRun(ctx context.Context, env *cloudEnv, slug, id string) (runObjects, error) {
	return readRunVia(ctx, env, runcache.Reader{}, slug, id, 0, false)
}

// readRunVia is readRun through rd, which may answer an object from the
// local run cache. rd's index decides absence as well as content, so a
// reader built from a listing reads nothing for a run whose objects the
// listing did not see.
//
// pr is the pull request a --pr listing asked for, 0 for a listing that asked
// for none: a run whose task names a different pull request is left there, its
// launch.json and result.json unread, because neither can place it on the pull
// request the caller is listing (taskOnOtherPR).
//
// sum lets the run's own summary.json stand in for its task.json and its
// result.json, which is what `runs ls` wants and what the rest do not: the
// summary carries what a row shows and nothing else, so a caller that reads
// the run for anything more -- its stages, its verify records, its reports --
// passes false. A summary this reader cannot vouch for (absent, unreadable,
// another version, another run) is no error and no row: the three objects
// answer instead, which is every run written before summaries existed.
func readRunVia(ctx context.Context, env *cloudEnv, rd runcache.Reader, slug, id string, pr int, sum bool) (runObjects, error) {
	s := runstore.Open(env.bucket.Bucket, slug, id)
	r := runObjects{in: runview.Input{Slug: slug, RunID: id}, store: s}
	in := &r.in
	problem := func(p string) {
		in.Problem = p
		// The warning is a person's line on stderr, so it names the run by its
		// ending like every other human surface (spec.md §1, D1705), with the
		// slug beside it: the warning may name a run the listing below does not
		// show at all (--pr, a window the run fell out of).
		r.warning = fmt.Sprintf("warning: run %s/%s: %s\n", slug, task.ShortRun(id), p)
	}
	var err error
	if sum {
		ok, err := readSummaryVia(ctx, s, rd, in)
		if err != nil {
			return r, err
		}
		r.summarized = ok
	}
	if taskOnOtherPR(in.Task, pr) {
		// On the summary path this is the two objects' own answer too: the
		// summary carries the task's pull request and the record's, which is
		// all that places a run on a pull request (taskOnOtherPR).
		return r, nil
	}
	if !r.summarized {
		raw, err := rd.Read(ctx, s, "task.json")
		switch {
		case err != nil:
			return r, err
		case raw != nil:
			in.Task, _ = task.Parse(raw) // unparseable: the row says task.json unreadable
		}
		if taskOnOtherPR(in.Task, pr) {
			return r, nil
		}
	}
	// A corrupt object is this run's error row, with a warning; a read that
	// fails otherwise (the bucket, the network) still fails the listing. On
	// the summary path launch.json is the only one of the two left to read --
	// a launcher wrote it, and a summary may never stand in for what a
	// launcher wrote -- and its corruption drops the record with it, so the
	// row is the one the three objects give for the same object.
	for _, read := range []struct {
		name string
		do   func() error
	}{
		{"launch.json", func() (err error) { in.Launch, err = readLaunch(ctx, s, rd); return err }},
		{"result.json", func() (err error) {
			if r.summarized {
				return nil
			}
			in.Record, err = readRecord(ctx, s, rd)
			return err
		}},
	} {
		if err := read.do(); corruptObject(err) {
			problem(read.name + " is unreadable: " + err.Error())
			r.unreadRecord = true
			in.Record = nil
			return r, nil
		} else if err != nil {
			return r, err
		}
	}
	if in.Launch == nil && in.Record == nil {
		if in.CancelMarker, err = s.CancelRequested(ctx); err != nil {
			return r, fmt.Errorf("checking %s/%s's cancel marker: %w", slug, task.ShortRun(id), err)
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

// readSummaryVia reads a run's summary object through rd and fills in.Task and
// in.Record from it, so the run's task.json and result.json are not read: two
// objects per run instead of three, which is what the summary is for.
//
// ok is false when there is nothing here to read the run from, and the caller
// reads the three objects instead. Every one of those is a fallback rather
// than an error, because the object is a shortcut and a shortcut may not break
// the listing it was meant to speed up: the run has no summary (every run
// written before this object existed), or the summary is unreadable or names
// another version, another run, or no record at all (runstore.DecodeSummary's
// own checks). An error is returned only for what would fail the listing
// anyway -- the bucket or the network.
func readSummaryVia(ctx context.Context, s *runstore.Store, rd runcache.Reader, in *runview.Input) (bool, error) {
	raw, err := rd.Read(ctx, s, runstore.SummaryName)
	switch {
	case corruptObject(err):
		// A corrupt summary is not this run's error row, unlike a corrupt
		// task.json or result.json: those are the run's own objects and the
		// listing says so, while this one is only a shortcut over them.
		return false, nil
	case err != nil:
		return false, err
	case raw == nil:
		return false, nil
	}
	sum, err := runstore.DecodeSummary(raw, s.Slug(), s.RunID())
	if err != nil {
		return false, nil
	}
	// Task may be nil here -- a run whose task could not be read -- which is
	// the row task.json would give for the same run.
	in.Task, in.Record = sum.Spec(), sum.AsRecord()
	return true, nil
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

// executionsSince is the floor a listing of these runs' executions is read
// from: an hour before the oldest run among them, since a run ID is minted
// before its launch only by moments (readRefRows). Zero -- no floor, the
// listing read whole -- when a run ID carries no moment, which is still a
// superset of the runs joined and so still a listing none of them falls outside.
func executionsSince(runs []runObjects) time.Time {
	oldest := time.Time{}
	for _, r := range runs {
		at, err := runstore.RunTime(r.in.RunID)
		if err != nil {
			return time.Time{}
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	if oldest.IsZero() {
		return time.Time{}
	}
	return oldest.Add(-time.Hour)
}

// followExecution joins r with its execution, from execs (the listing)
// or, when the listing lacks it, from the backend. A listing that is the whole
// of the runs joined with it (listed) is the platform's answer for every one of
// them, so a run it lacks has no execution and none is read: a read would be one
// request per run, which on a long history is thousands against a quota the
// launches share.
func followExecution(ctx context.Context, env *cloudEnv, r *runObjects, execs map[string]backend.Execution, listed bool, warnf func(string, ...any)) error {
	in, s, name := &r.in, r.store, r.execution
	if name == "" || in.Problem != "" {
		return nil
	}
	// The run's service account can write both objects: follow only the
	// execution the run's own FUGARO_RUN names, which is what tells two runs
	// of one shared job apart (checkExecution). The listing's own entry saves
	// the read; a run the listing does not hold -- a listing that is not the
	// whole of the runs joined, or one a live read made -- is read back on its
	// own. A read that fails is this run's own row, not the
	// listing's: one run's execution failing to read must not hide the other
	// runs' rows, as a corrupt object of one run does not.
	eid, _ := backend.ParseExecution(name)
	var hint *backend.Execution
	if e, ok := execs[eid.Key()]; ok {
		hint = &e
	}
	var (
		x   *backend.Execution
		err error
	)
	if hint != nil || !listed {
		x, err = env.checkExecution(ctx, hint, name, in.Slug, in.RunID)
	} else {
		err = env.checkExecutionName(name)
	}
	if err != nil {
		in.Problem = "not following its execution: " + err.Error()
		warnf("warning: run %s/%s: %s\n", in.Slug, task.ShortRun(in.RunID), in.Problem)
		return nil
	}
	in.Exec = x
	// The record was read before the execution: a runner that wrote its
	// final record and exited in between would look like one that died
	// without finalizing. Once the execution has ended (or is gone), read
	// the record again, so it is as new as the execution.
	//
	// The record is re-read from where this run's record came from: a row
	// built from the summary re-reads the summary, because reading result.json
	// here would answer a record the rest of the row was never built from --
	// and the summary is written beside the record, at the same moments, so it
	// is as new as it. Two summaries fall back to result.json instead: one that
	// has gone or cannot be vouched for (the shortcut is not there any more),
	// and one that still says running while the execution has ended (the
	// shortcut is there and behind the record). Either way the row is the run's
	// own record rather than a run shown as one nothing wrote -- a nil record
	// here reads as a run the backend lost.
	if in.Record != nil && in.Record.Status == runstore.StatusRunning && (in.Exec == nil || in.Exec.State.Terminal()) {
		if r.summarized {
			// A summary the reader cannot vouch for is the same fallback the
			// first read gives it (readSummaryVia): the shortcut is not there,
			// and the run's own object answers. Only a read that failed is
			// this listing's error, as it is on the first read.
			sum, serr := absent(s.ReadSummary(ctx))
			if serr != nil && !corruptObject(serr) {
				return serr
			}
			// A summary that still says running, while the execution has
			// ended, is one behind the record it stands in for: the summary
			// write is best-effort and follows the record write (runner.save),
			// so a run that ended in between leaves a valid summary of a run
			// still going. Trusting it here would show a succeeded run as
			// infra_error, "execution ended without finalizing (succeeded)" --
			// a row wrong for good -- so the run's own record answers instead,
			// exactly as when the summary has gone: result.json is the object
			// this one stands in for and the one that carries the ending.
			if sum != nil && sum.Record != nil && sum.Record.Status != runstore.StatusRunning {
				in.Record = sum.AsRecord()
				return nil
			}
		}
		if in.Record, err = absent(s.ReadRecord(ctx)); err != nil {
			return err
		}
	}
	return nil
}

// corruptObject reports whether err is a run object that can't be used:
// undecodable, or past its read cap (runstore.ErrTooLarge,
// blobx.ErrTooLarge). That is the run's problem, not the listing's.
// runstore.ErrCorrupt is the same verdict from a reader that decodes the
// object itself (runstore.ReadRecord, ReadSummary): the object is there and
// does not decode, which is not a read that failed.
func corruptObject(err error) bool {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, runstore.ErrTooLarge) || errors.Is(err, blobx.ErrTooLarge) ||
		errors.Is(err, runstore.ErrCorrupt)
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
// document. The warnings come before the table, or go into the document. The
// totals are the rows' own; a caller printing a page of a wider listing hands
// that listing's totals to printRowsWithTotals instead.
func printRows(w io.Writer, project string, rows []runview.Row, warnings []string, now time.Time, asJSON bool) error {
	return printRowsWithTotals(w, project, rows, runview.Sum(rows), nil, warnings, now, asJSON)
}

// printRowsWithTotals is printRows with the totals given rather than summed
// from the rows: a listing that printed a page still totals the window it read
// (D818), so the number beside a page is never the page's own. index is what
// the listing read about the run index, and is nil for a listing the window
// answered (lsIndexState).
func printRowsWithTotals(w io.Writer, project string, rows []runview.Row, tot runview.Totals, index *lsIndexState, warnings []string, now time.Time, asJSON bool) error {
	return printDoc(w, project, rows, tot, nil, index, warnings, now, asJSON)
}

// printGroupsWithTotals is printRowsWithTotals with --group-by's block: the
// same rows under the same totals -- neither moves -- and the window's failure
// classes printed under them, or carried beside them in the document. It is
// the only caller that hands groups to printDoc, and the only thing --group-by
// changes about a listing. Its read is the window's, so it has no index state
// to print: a grouped listing is one the index does not answer (indexListing).
func printGroupsWithTotals(w io.Writer, project string, rows []runview.Row, tot runview.Totals, warnings []string, now time.Time, asJSON bool) error {
	return printDoc(w, project, rows, tot, groupRows(rows), nil, warnings, now, asJSON)
}

// printDoc prints rows as a table with a totals line, or as one JSON document,
// with groups -- nil unless the listing was grouped -- as the block beside
// them, and index -- nil unless the rows came from the run index -- as the two
// fields saying what the index said of itself, and one line above the table
// while it is still building (D3215).
func printDoc(w io.Writer, project string, rows []runview.Row, tot runview.Totals, groups []lsGroup, index *lsIndexState, warnings []string, now time.Time, asJSON bool) error {
	if asJSON {
		doc := lsDoc{Project: project, Runs: rows, Totals: tot, Groups: groups, Warnings: warnings}
		if index != nil {
			complete := index.complete
			doc.IndexComplete, doc.FoldedThrough = &complete, index.foldedThrough
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
	if index != nil && !index.complete {
		fmt.Fprintln(w, lsIndexBuildingLine(index))
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
	// The resume column appears only when some listed run ended paused
	// (contract-pause R15, D3195): a listing whose window holds no paused run
	// does not widen every other run's columns for a cell it would leave `-`
	// on every row of the page.
	showResume := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.Status == string(runstore.StatusPaused) })
	// RUN is the ending a person reads, says and types back (spec.md §1), and
	// the repository has the column beside it: together the two are what
	// Row.Run carries as `<slug>/<run-id>` and what --json keeps whole (D1705).
	header := "RUN\tREPO\tSTATUS\tSTAGE\tAGE\tCOST\tPR\tBY"
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
	if showResume {
		header += "\tRESUME"
	}
	fmt.Fprintln(tw, header)
	endings := runEndings(rows)
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
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s", oneLine(endings[rowRef(r)]), repoCell(r), oneLine(statusCell(r)), oneLine(r.Stage), age(now.Sub(r.Created)), cost, prColumn(r), launcherCell(r))
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
		if showResume {
			line += "\t" + pauseCell(r)
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
	if _, err := fmt.Fprintln(w, line); err != nil {
		return err
	}
	if err := printOwnerWaiting(w, rows); err != nil {
		return err
	}
	if groups == nil {
		return nil
	}
	return printGroupsTable(w, groups)
}

// printOwnerWaiting prints, under the table, what each listed run is waiting
// for the repository's owner to decide (OD1), in the same words the pull
// request's own status section uses: the run, the count its own reason names,
// and one line per decision with the file and the reviewer's sentence. It
// prints nothing when no listed run is waiting, which is every ordinary
// listing. The sentences are untrusted text — a reviewer wrote them — so they
// are clipped and escaped like every other cell.
func printOwnerWaiting(w io.Writer, rows []runview.Row) error {
	waiting := 0
	for _, r := range rows {
		if r.OwnerDecisions != nil {
			waiting++
		}
	}
	if waiting == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Waiting on you"); err != nil {
		return err
	}
	// The same rule over the same rows the table above printed, so a run is
	// named by one ending in both places (task.DisplayRuns, F15).
	endings := runEndings(rows)
	for _, r := range rows {
		od := r.OwnerDecisions
		if od == nil {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = "review is waiting on " + runner.Plural(od.Total, "owner decision")
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", oneLine(endings[rowRef(r)]), oneLine(reason)); err != nil {
			return err
		}
		for _, d := range od.Decisions {
			summary := oneLine(clipRunes(d.Summary, maxOwnerDecisionRunes))
			line := "    - " + summary
			if d.File != "" {
				line = "    - `" + oneLine(d.File) + "`: " + summary
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if n := od.Total - len(od.Decisions); n > 0 {
			if _, err := fmt.Fprintf(w, "    and %s more; the run's record lists %d of them\n", runner.Plural(n, "decision"), len(od.Decisions)); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxOwnerDecisionRunes bounds one decision's sentence in a listing, the way
// maxNameCellRunes bounds a name column: the record already caps it at
// runstore.MaxOwnerDecisionText, and a table line that carried the whole cap
// for every decision of every run would bury the rows above it.
const maxOwnerDecisionRunes = 120

// rowRef is the pair a row's RUN cell is computed over (task.Ref): the two
// halves Row.Run carries, `<slug>/<run-id>` — the repository's storage slug
// and the run's full ID. It reads them out of Run rather than off RunID,
// because Run is the pair the listing is built on (F8) and a row assembled
// without its ID still names the run it is.
func rowRef(r runview.Row) task.Ref {
	slug, id, ok := strings.Cut(r.Run, "/")
	if !ok {
		return task.Ref{Slug: r.Repo, RunID: r.Run}
	}
	return task.Ref{Slug: slug, RunID: id}
}

// runEndings is what each of rows is shown by: the shortest ending of its run
// ID that no other listed run's ID also ends with (task.DisplayRuns, spec.md
// §3, D1706). The endings are computed over exactly the rows passed in — the
// rule holds over what is shown together, so one run may be shown as `3bb2` in
// one listing and as `8-3bb2` in the next (F15).
func runEndings(rows []runview.Row) map[task.Ref]string {
	refs := make([]task.Ref, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, rowRef(r))
	}
	return task.DisplayRuns(refs)
}

// repoCell is a row's REPO cell: the repository the run's task names, in the
// column the RUN cell gave up when it became the ending. A run whose task.json
// could not be read names no repository in the task — but its reference still
// carries the repository's slug, which runview.Join builds into Row.Run as
// `<slug>/<run-id>` from the ref the listing is built on, and rowRef reads it
// back out. So the cell falls back to that slug rather than going blank: the
// row is listed either way, the RUN cell no longer carries the slug (it is the
// ending now), and a blank cell would leave the row naming no repository at
// all, which the whole-ID RUN cell used to do for it.
func repoCell(r runview.Row) string {
	name := r.Repo
	if name == "" {
		name = rowRef(r).Slug
	}
	if name == "" {
		return "-"
	}
	return cellOneLine(clipRunes(name, maxNameCellRunes))
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

// pauseCell is a paused run's resume fact for the listing's RESUME column
// (contract-pause R15, D3195): the instant the orchestrator will resume its
// chain at, in RFC 3339 UTC like the document's other instants, and the run
// that resumed it, shortened the way a run id is spelled in a cell
// (task.ShortRun). It is the two facts `internal/watch`'s plain frame draws for
// a paused finished row, spelled for this table.
//
// `-` for a run that is not paused, and for a paused run whose pause this read
// could not see at all: a node the lookup could not read leaves the instant
// unknown and a listing that holds no resumer leaves the resumer unknown, and
// an unknown answer is never rendered as an empty one (the rule the changed-
// files and mode cells beside it follow). A paused run the reader can place
// always has at least one of the two, since the node names the instant it was
// written with.
func pauseCell(r runview.Row) string {
	// The question is asked by an equality with the word it is about — is this
	// run paused? — rather than by negating it: a status read is always an
	// equality with the value being asked about, so a status a later release
	// adds is never swept into an answer it does not belong to (the rule
	// TestRunsListsReadTheStatusWord holds this file's reads to).
	if r.Status == string(runstore.StatusPaused) {
		var parts []string
		if r.ResumeAt != nil {
			parts = append(parts, r.ResumeAt.UTC().Format(time.RFC3339))
		}
		if r.ResumedBy != "" {
			parts = append(parts, "by "+task.ShortRun(r.ResumedBy))
		}
		if len(parts) > 0 {
			return cellOneLine(strings.Join(parts, " "))
		}
	}
	return "-"
}

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
	cell := string(rg.LevelOf()) + " (" + string(rg.From)
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
