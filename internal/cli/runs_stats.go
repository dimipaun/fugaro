package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/userstats"
)

// fugaro runs stats --by user: spend and jobs, per person, over one window of
// one installation's own run records.
//
// The figures are derived from the RUN RECORDS -- each run's launch.json
// launched_by, and its settled cost and outcome in result.json -- and from
// nothing else: no budget counter is read, no rule changes, and this command
// adds no write path of any kind.
//
// Three properties shape it, and each has a test:
//
//   - The identity is the one the run itself recorded. A follow-up therefore
//     belongs to whoever launched the follow-up, an automated launch (a
//     service account, a CI identity) is a group of its own, and a run that
//     recorded nobody is 'unknown', never guessed from its task or its
//     record.
//   - A caller sees every user only when the installation's own lists place
//     them as an operator; otherwise they see their own row alone, unless
//     they ask for the whole table with --all-users. The identity the view is
//     resolved for and the view itself are printed, so a table never leaves a
//     reader guessing whose numbers they are looking at.
//   - The read is bounded. A window can hold every run of every repository in
//     the bucket, so at most --max-runs records are read per call, newest
//     first, and a truncated read says so in the output rather than quietly
//     publishing figures over a subtree of the window.

// userStatsDefaultSince is runs stats' default window: the same seven days
// runs ls lists by default.
const userStatsDefaultSince = "7d"

// userStatsMaxRuns is how many runs one call reads. It bounds the read rather
// than the window: a window holding more is truncated to its newest
// userStatsMaxRuns runs, and the output says so. Raising it is the caller's
// choice (--max-runs) and costs one object read per run.
const userStatsMaxRuns = 5000

// userStatsByUser is the only breakdown this command publishes. --by names
// it, so that the day a second one exists it is a value here and not a
// second command.
const userStatsByUser = "user"

type userStatsOptions struct {
	cloud                 cloudOptions
	by, repo, since       string
	all, allUsers, asJSON bool
	maxRuns               int
}

// userStatsDoc is runs stats --json's document.
type userStatsDoc struct {
	Project string `json:"project"`
	// By is the breakdown asked for ("user").
	By string `json:"by"`
	// View is which rows the caller is shown: "all" (every user of the
	// installation), or "own" (their own row alone). You is the identity the
	// view was resolved for, in the same domain a row's key is -- sanitised,
	// one printable line, capped at userstats.MaxIdentityRunes, so a consumer
	// can match it against rows[].user. It is "" when the caller asked for
	// every user with --all-users and the CLI could not tell who they are,
	// which is unavailable rather than a person. Operator says whether the
	// installation's own lists place You as one.
	View     string `json:"view"`
	You      string `json:"you"`
	Operator bool   `json:"operator"`
	// Since and Until are the window's own dates (UTC, YYYY-MM-DD); Since is
	// "" when the window is unbounded (--since 0).
	Since string `json:"since"`
	Until string `json:"until"`
	// MaxRuns and Truncated say how much of the window the figures are over:
	// Truncated means the window held more runs than MaxRuns and only its
	// newest MaxRuns were read.
	MaxRuns   int  `json:"max_runs"`
	Truncated bool `json:"truncated"`
	// Rows are the groups, one per (user, currency); Totals one per currency
	// with an empty user. Dollars are never added across currencies.
	Rows   []userstats.Row `json:"rows"`
	Totals []userstats.Row `json:"totals"`
	// Unlaunched counts the runs of the window that were neither launched nor
	// recorded -- unlaunched, cancelled before launch, or a stale claim. They
	// never started, so no figure above counts them.
	Unlaunched int `json:"unlaunched"`
	// Notes are the lines a human table prints before and after its table:
	// whose numbers these are, what the money counts, and what the read left
	// out.
	Notes []string `json:"notes"`
}

func newRunsStatsCmd() *cobra.Command {
	var o userStatsOptions
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Aggregate run records by the person who started each run",
		Long: "Count the runs one installation's records hold, and what they cost, grouped by\n" +
			"the person who started each one: how many jobs an identity began, how they ended,\n" +
			"what they spent, and a total row.\n\n" +
			"The numbers come from the run records, not from the budget database: each run's\n" +
			"launch.json carries the launcher it recorded at launch, and its result.json the\n" +
			"outcome and the settled cost. Nothing else is read, no counter is consulted, and\n" +
			"nothing is written.\n\n" +
			"Attribution, as the records themselves state it:\n\n" +
			"  - A follow-up (fugaro run --pr) belongs to the launcher of that follow-up: the\n" +
			"    identity is the one the follow-up's own launch recorded.\n" +
			"  - An automated launch -- a service account, a CI runner -- appears as its own\n" +
			"    identity, exactly as it recorded itself; it is never folded into a person's.\n" +
			"  - A run whose launch recorded no launcher is grouped as 'unknown'. It is never\n" +
			"    guessed from the run's task, its record or the account it ran under.\n" +
			"  - Dollars are never added across currencies: a row is one user and one currency,\n" +
			"    and a total covers one currency. Every cost recorded today is in USD.\n" +
			"  - The window is UTC days like the rest of the CLI. A run is counted on the day\n" +
			"    the bucket's run IDs place its start, so the window and the runs in it agree\n" +
			"    with runs ls.\n\n" +
			"The money is model dollars, counted the way the budget screens count them. COUNTED\n" +
			"$ is the model spend that counts toward the caps (a run on an API key or Vertex),\n" +
			"and NOTIONAL $ the model spend of a run on a subscription (agent.auth oauth),\n" +
			"which counts toward the plan's limits and is no one's bill: they are separate\n" +
			"columns and are never summed. Compute is not attributed to a person: no run's\n" +
			"compute is, and the budget database's own person view leaves it out too. AVG\n" +
			"$/RUN is COUNTED $ over COUNTED RUNS, the runs that settled a real-dollar cost;\n" +
			"a notional run, a run with no readable record and a record from before the cost\n" +
			"breakdown existed are none of them averaged in as a run that cost nothing.\n\n" +
			"Who sees whom. An operator of the installation -- the identity the local config's\n" +
			"user: (or git config user.email) names, listed in its terraform.operators, the\n" +
			"list fugaro doctor's runs-bucket-iam check reads -- sees every user. Anyone else\n" +
			"sees their own row alone, and --all-users asks for the whole table. An\n" +
			"installation that names no operators (one from before the 0.7.0 hardening, say)\n" +
			"places nobody, and the view then stays private: the default is never the wider\n" +
			"one. Whose numbers a table shows is printed above it.\n\n" +
			"A private view is the caller's own row. A caller whose identity cannot be told at\n" +
			"all -- the local config names nobody, and git config user.email answers nothing --\n" +
			"is refused, never quietly widened: the error says so, and names --all-users. An\n" +
			"identity with no key of its own because nothing printable is left of it (a pasted\n" +
			"zero-width character, say) is shown no row instead: the table is empty, and its\n" +
			"view line says the CLI cannot tell who you are. A row keyed 'unknown' is every run\n" +
			"of the window that recorded no launcher -- never one person's -- so it is not shown\n" +
			"as the caller's own either, and a note says so.\n\n" +
			"The launcher text is what a launcher wrote and is read for nothing else: it is\n" +
			"sanitised and size-capped before it is printed, and it authorises and admits\n" +
			"nothing. Two identities that differ only in what the sanitising drops -- an\n" +
			"invisible character a paste left in one of them, say -- or only past the cap are\n" +
			"one key and one row: the row is the key, not the person. A private view is not\n" +
			"read off that key. It holds the runs whose launch recorded your identity itself,\n" +
			"compared as runs ls --mine compares it, so someone whose identity merely\n" +
			"sanitises to the same text is never counted in it and your own row can be\n" +
			"smaller than that key's row in the full table.\n\n" +
			"A window can hold every run of every repository in the bucket, so the read is\n" +
			"bounded: at most --max-runs records are read per call, newest first, and a window\n" +
			"holding more says so in the output rather than publishing figures over a part of\n" +
			"itself silently. The bound is on the read, not on a person: the newest --max-runs\n" +
			"runs are taken before any view is applied, so a private view can be over fewer of\n" +
			"the caller's own runs than --max-runs suggests. A run with neither a launch nor a\n" +
			"record -- never launched, cancelled before launch, or a stale claim -- never\n" +
			"started and is counted in no figure. The count of those is the window's, over\n" +
			"everyone's runs, and is no more the caller's own than the truncation is.\n\n" +
			"--json prints {project, by, view, you, operator, since, until, max_runs,\n" +
			"truncated, rows[], totals[], unlaunched, notes}; rows and totals carry {user,\n" +
			"currency, runs, succeeded, failed, halted, infra_error, other, no_record,\n" +
			"counted_usd, notional_usd, counted_runs, avg_counted_usd}, and the counts add up:\n" +
			"runs == succeeded + failed + halted + infra_error + other + no_record.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRunsStats(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.by, "by", userStatsByUser, "what to break the figures down by (user)")
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.StringVar(&o.since, "since", userStatsDefaultSince, "only runs started within this long (7d, 36h, 90m; 0 for all)")
	f.IntVar(&o.maxRuns, "max-runs", userStatsMaxRuns, "read at most this many runs, newest first (a bigger window is truncated and says so)")
	f.BoolVar(&o.allUsers, "all-users", false, "show every user's row, not only your own (the table names whose numbers it shows)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runRunsStats(cmd *cobra.Command, o *userStatsOptions) error {
	if o.by != userStatsByUser {
		return userErr("--by %q: use %s, the only breakdown this command publishes", safetext.Strip(o.by), userStatsByUser)
	}
	if o.repo != "" && o.all {
		return userErr("--repo and --all don't go together")
	}
	if o.maxRuns < 1 {
		return userErr("--max-runs %d: use at least 1", o.maxRuns)
	}
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	// Who the caller is, and how the installation's own lists place them.
	// Both are read the way the rest of the CLI reads them: lc.Me is the
	// identity `fugaro run` records and `runs ls --mine` matches, and the
	// lists are the ones fugaro doctor's bucket check reads (roles.go).
	me, meErr := env.lc.Me(ctx)
	// The identity is emitted the way every other identity in this document
	// is: through IdentityOf, so it is one printable line, bounded, and the
	// same value a row's key is. git config user.email is read out of the
	// checkout the caller happens to stand in, so this text is no more
	// trustworthy than a launcher's -- and a consumer can match `you` against
	// rows[].user only if both are in the key's own domain. It stays "" when
	// the CLI cannot tell who the caller is, which is "unavailable", not a
	// person named "unknown" -- including when the identity it did resolve is
	// nothing but invisible characters (userstats.CallerKey).
	you := userstats.CallerKey(me)
	operator := operatorOf(env.lc, me)
	view := userStatsViewOwn
	if operator || o.allUsers {
		view = userStatsViewAll
	}
	if view == userStatsViewOwn && me == "" {
		return userErr("cannot tell who you are (%v), and this view shows your own row alone: set user: in the local config, or pass --all-users to see every user's rows",
			safetext.Strip(meErr.Error()))
	}

	slugs, err := lsSlugs(ctx, env, &lsOptions{repo: o.repo, all: o.all}, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	until := time.Now().UTC()
	var from time.Time
	if since > 0 {
		from = until.Add(-since)
	}
	read, err := readUserStatsRuns(ctx, env, slugs, from, o.maxRuns)
	if err != nil {
		return err
	}
	if view == userStatsViewOwn {
		// Matched on the identity the run recorded, exactly as runs ls --mine
		// matches: launch.json's launched_by is the requester the launch was
		// made for (run.go), so "your own runs" has one meaning in this CLI and
		// this is it.
		//
		// Not on the row's key. A key is sanitised text two people can share --
		// identities that differ only in characters the sanitising drops, or
		// only past the cap, are one row -- and a shared key is not a person:
		// showing its row under "your own runs only" would credit the caller
		// with a stranger's runs, which is the mistake the unattributed key's
		// guard below removes in the other direction. The row a private view
		// does show is keyed by `you`, since every run in it recorded this
		// identity and its key is IdentityOf of it.
		//
		// Two identities are shown nothing at all: one with nothing printable
		// in it (`you` is ""), and one whose text reads as the unattributed
		// key itself. The row under that key holds every run of the window that
		// recorded no launcher, so it is nobody's own figures.
		own := you
		if own == userstats.UnknownUser {
			own = ""
		}
		read.runs = slices.DeleteFunc(read.runs, func(r userStatsRecorded) bool { return own == "" || r.recorded != me })
	}
	res := userstats.Aggregate(userStatsRuns(read.runs))
	return writeUserStats(cmd.OutOrStdout(), userStatsDoc{
		Project:    env.lc.Name,
		By:         userStatsByUser,
		View:       view,
		You:        you,
		Operator:   operator,
		Since:      dateOrEmpty(from),
		Until:      until.Format("2006-01-02"),
		MaxRuns:    o.maxRuns,
		Truncated:  read.truncated,
		Rows:       res.Rows,
		Totals:     res.Totals,
		Unlaunched: read.unlaunched,
	}, o.since, o.asJSON)
}

// The two views a caller may be shown. They are the ruling's own two, and
// neither is a guess: userStatsViewOwn is the default whenever the
// installation's lists do not place the caller as an operator.
const (
	userStatsViewAll = "all"
	userStatsViewOwn = "own"
)

// userStatsRecorded is one run as the window read it: the aggregation's own
// view of it, and the launcher text its launch.json recorded -- the raw text,
// which is what a caller's private view is filtered by and what the
// aggregation never receives. Two of them can be one row and are still two
// launchers.
type userStatsRecorded struct {
	run      userstats.Run
	recorded string
}

// userStatsRuns is the aggregation's view of the window's runs.
func userStatsRuns(seen []userStatsRecorded) []userstats.Run {
	runs := make([]userstats.Run, len(seen))
	for i, s := range seen {
		runs[i] = s.run
	}
	return runs
}

// userStatsRead is one bounded read of the window's run records.
type userStatsRead struct {
	// runs are the records the window held, mapped to the aggregation's own
	// view of a run.
	runs []userStatsRecorded
	// unlaunched counts the runs of the window that were neither launched
	// nor recorded: they never started and no figure counts them.
	unlaunched int
	// truncated means the window held more runs than the read was allowed, so
	// only its newest max were read.
	truncated bool
}

// readUserStatsRuns reads the window's runs the way runs ls reads them -- the
// runs bucket's listing, one directory per repository, then one object per
// run -- and maps each to the narrow view internal/userstats aggregates.
//
// The read is bounded: at most max runs, newest first, across every
// repository. The listing is already the window's, so the cap decides what a
// window too large to read whole publishes, and the caller says so rather
// than leaving a reader to think the figures cover the whole window.
//
// Only launch.json and result.json are read, and neither the task nor the
// transcript: the launcher is launch.json's own record of itself, and the
// attribution rules say a run with no launcher recorded is 'unknown' -- a
// task's requester is not the run's launcher, so reading the task could only
// put a guess where the rule asks for the fact.
//
// A record that cannot be used is one run's problem, not the window's, as in
// runs ls: a corrupt launch.json leaves that run's launcher unknown, and a
// corrupt or missing result.json leaves its outcome and its cost unknown. One
// bad object never makes the figures unproducible, and neither is ever read
// as a zero.
func readUserStatsRuns(ctx context.Context, env *cloudEnv, slugs []string, since time.Time, max int) (userStatsRead, error) {
	type ref struct{ slug, id string }
	var refs []ref
	for _, slug := range slugs {
		ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, since)
		if err != nil {
			return userStatsRead{}, remote(err)
		}
		for _, id := range ids {
			refs = append(refs, ref{slug, id})
		}
	}
	// A run ID starts with its UTC mint time, so an ID sorts by when the run
	// started: newest first is the window's newest runs, whichever
	// repositories they came from.
	slices.SortFunc(refs, func(a, b ref) int {
		if c := strings.Compare(b.id, a.id); c != 0 {
			return c
		}
		return strings.Compare(b.slug, a.slug)
	})
	var read userStatsRead
	if len(refs) > max {
		refs, read.truncated = refs[:max], true
	}

	type objects struct {
		launch *runstore.Launch
		record *runstore.Record
	}
	objs := make([]objects, len(refs))
	errs := make([]error, len(refs))
	parallel(len(refs), func(i int) {
		s := runstore.Open(env.bucket.Bucket, refs[i].slug, refs[i].id)
		// A corrupt launch record is a launcher nobody recorded; a corrupt
		// result record is an outcome and a cost nobody can read. Neither is
		// another run's problem, and neither is read as a zero.
		l, err := absent(s.ReadLaunch(ctx))
		switch {
		case err == nil:
			objs[i].launch = l
		case corruptObject(err):
		default:
			errs[i] = err
			return
		}
		r, err := absent(s.ReadRecord(ctx))
		switch {
		case err == nil:
			objs[i].record = r
		case corruptObject(err):
		default:
			errs[i] = err
		}
	})
	if err := errors.Join(errs...); err != nil {
		return userStatsRead{}, remote(fmt.Errorf("reading the window's run records: %w", err))
	}
	for _, o := range objs {
		switch {
		case o.launch == nil && o.record == nil:
			// No launch and no record: the run never started, and no
			// launcher is on record either. It is counted, and in no figure.
			read.unlaunched++
		default:
			read.runs = append(read.runs, userStatsRun(o.launch, o.record))
		}
	}
	return read, nil
}

// userStatsRun is the aggregation's view of one run's two objects, and the
// launcher text the run recorded beside it.
//
// The identity is read from launch.json alone: a run whose task names a
// requester but whose launch recorded nobody is 'unknown', because the rule
// is the launcher the run recorded and never a value inferred from beside it.
//
// A record that settled no cost breakdown leaves the money unknown rather
// than zero: a record from before the breakdown existed may still carry a
// model figure, and it cannot say whether that figure counted toward a cap or
// was a subscription's notional one, which is exactly the difference this
// command publishes. It is counted among the runs and in no money column.
func userStatsRun(l *runstore.Launch, rec *runstore.Record) userStatsRecorded {
	recorded := launcherRecorded(l)
	r := userstats.Run{User: userstats.IdentityOf(recorded)}
	if rec == nil {
		r.NoRecord = true
		return userStatsRecorded{run: r, recorded: recorded}
	}
	r.Status = string(rec.Status)
	if rec.Cost != nil {
		switch rec.Cost.ModelBasis {
		case runstore.BasisSubscription:
			r.NotionalUSD = rec.Cost.ModelUSD
		default:
			r.CountedUSD, r.CountedCost = rec.Cost.ModelUSD, true
		}
	}
	return userStatsRecorded{run: r, recorded: recorded}
}

// launcherRecorded is what a run's launch.json says about who started it, ""
// when there is no launch record.
func launcherRecorded(l *runstore.Launch) string {
	if l == nil {
		return ""
	}
	return l.LaunchedBy
}

// writeUserStats prints the groups as a table with its notes, or as one JSON
// document.
func writeUserStats(w io.Writer, doc userStatsDoc, sinceLabel string, asJSON bool) error {
	doc.Rows, doc.Totals = userStatsRows(doc.Rows), userStatsRows(doc.Totals)
	doc.Notes = userStatsNotes(doc)
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	fmt.Fprintf(w, "project: %s\n", doc.Project)
	window := "the whole bucket"
	if doc.Since != "" {
		window = fmt.Sprintf("%s to %s (%s)", doc.Since, doc.Until, sinceLabel)
		if doc.Truncated {
			window += fmt.Sprintf(", truncated to the newest %d runs", doc.MaxRuns)
		}
	} else if doc.Truncated {
		window = fmt.Sprintf("the whole bucket, truncated to the newest %d runs", doc.MaxRuns)
	}
	fmt.Fprintf(w, "window:  %s\n", window)
	fmt.Fprintf(w, "by:      %s\n", doc.By)
	for _, line := range doc.Notes {
		fmt.Fprintln(w, multiLine(line))
	}
	fmt.Fprintln(w)
	if len(doc.Rows) == 0 {
		// A window with nobody in it is said plainly, not shown as an empty
		// table a reader could take for a failure.
		who := "anyone this view covers"
		if doc.View == userStatsViewOwn {
			who = "you"
		}
		fmt.Fprintf(w, "no run in this window was started by %s\n", who)
		return nil
	}
	// The currency column appears only when the rows carry more than one:
	// every cost recorded today is in the one currency, and a column that is
	// always the same word is not a fact a reader needs. When there is more
	// than one, it is the column that keeps the money columns from reading as
	// one sum.
	several := false
	for _, r := range doc.Rows {
		several = several || r.Currency != doc.Rows[0].Currency
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "USER"
	if several {
		header += "\tCUR"
	}
	header += "\tRUNS\tSUCCEEDED\tFAILED\tHALTED\tINFRA ERROR\tOTHER\tNO RECORD\tCOUNTED $\tNOTIONAL $\tCOUNTED RUNS\tAVG $/RUN"
	fmt.Fprintln(tw, header)
	line := func(r userstats.Row) string {
		s := cellOneLine(clipRunes(r.User, maxNameCellRunes))
		if several {
			s += "\t" + cellOneLine(clipRunes(r.Currency, maxNameCellRunes))
		}
		return fmt.Sprintf("%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t$%.2f\t$%.2f\t%d\t%s",
			s, r.Runs, r.Succeeded, r.Failed, r.Halted, r.InfraError, r.Other, r.NoRecord,
			r.CountedUSD, r.NotionalUSD, r.CountedRuns, userStatsAverage(r))
	}
	for _, r := range doc.Rows {
		fmt.Fprintln(tw, line(r))
	}
	for _, t := range doc.Totals {
		// One total per currency, and the row says which: with more than one
		// currency in the table, "TOTAL" alone would be the one line a reader
		// could take for a sum across them.
		name := "TOTAL"
		if several {
			name = "TOTAL " + t.Currency
		}
		t.User = name
		fmt.Fprintln(tw, line(t))
	}
	return tw.Flush()
}

// userStatsAverage is a group's average counted cost per counted run, or "-"
// when there is no run to average: "$0.00 a run" would be a figure, and the
// group has none.
func userStatsAverage(r userstats.Row) string {
	if r.CountedRuns == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", r.AvgCountedUSD)
}

// userStatsNotes are the lines the table (and the document) carries about
// itself: whose numbers these are, what the money counts, and what the read
// left out. They are built from the document alone, so the human and the
// machine output say exactly the same things.
func userStatsNotes(doc userStatsDoc) []string {
	var notes []string
	who := "the CLI cannot tell who you are"
	if doc.You != "" {
		// You is already IdentityOf's value, so this and the document's own
		// field are one text; oneLine is the second belt on the line the note
		// is interpolated into, not a second rendering of a different value.
		who = "you are " + oneLine(doc.You)
	}
	if doc.View == userStatsViewAll {
		// The full table names whose numbers it shows: everyone's, said in
		// words, with the reason this caller is shown them.
		why := "--all-users asked for it"
		if doc.Operator {
			why = "this installation's operators list names you"
		}
		notes = append(notes, fmt.Sprintf("view:    every user of this installation (%s; %s): the table below is everyone's numbers, each row named", who, why))
	} else {
		notes = append(notes, fmt.Sprintf("view:    your own runs only (%s, and this installation's operators list does not name you): --all-users shows every user's rows", who))
	}
	if doc.View == userStatsViewOwn && doc.You == userstats.UnknownUser {
		// The one identity that is a key and still not a person's: this
		// caller's own text reads as the word the unattributed runs are
		// grouped by, so no row is shown as theirs. Said, because a table with
		// no rows under "your own runs only" otherwise reads as a window this
		// caller started nothing in.
		notes = append(notes, fmt.Sprintf("you:     your identity reads as %q, the key a run that recorded no launcher is grouped under: no row below is shown as yours, because a row under that key is not only yours", userstats.UnknownUser))
	}
	notes = append(notes, "money:   model dollars, as the budget screens count them: COUNTED counts toward the caps, NOTIONAL (agent.auth oauth) toward none. They are never summed, and compute is attributed to no person")
	if doc.Truncated {
		notes = append(notes, fmt.Sprintf("read:    the window holds more runs than the %d this call reads: no figure below is over more than its newest %d, and none is over the whole window", doc.MaxRuns, doc.MaxRuns))
	}
	if doc.Unlaunched > 0 {
		notes = append(notes, fmt.Sprintf("read:    %d %s in the window %s never launched (no launch and no record) and %s counted in no figure",
			doc.Unlaunched, plural(doc.Unlaunched, "run", "runs"), wasWere(doc.Unlaunched), wasWere(doc.Unlaunched)))
	}
	return notes
}

// wasWere is "was" or "were", for a count.
func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// userStatsRows is rows, or the empty list for nil, so an empty window prints
// [] in the document rather than null (the same rule nonNil keeps for the
// string lists other documents carry).
func userStatsRows(rows []userstats.Row) []userstats.Row {
	if rows == nil {
		return []userstats.Row{}
	}
	return rows
}

// dateOrEmpty is t as the UTC date the rest of the CLI prints, "" for the
// zero time (an unbounded window).
func dateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
