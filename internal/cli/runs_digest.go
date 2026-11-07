package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// fugaro runs digest -- one closed period's report of one repository, written
// into that repository's own docs tree.
//
// It is the terminal half of D320: a report run (T3.2) produces the same
// files in the container, and this command produces them from a person's
// terminal, from the same internal/digest. Four properties shape it, and each
// has a test:
//
//   - One repository. The report is of this repository and of no other: a
//     record whose own repository is another is not read into it, and there
//     is no --all (G2, D322). On a project job the account reaches every
//     repository's runs/, so the record is untrusted input and that check is
//     the guard.
//   - Closed periods. A run counts in the period it finished (G4), so a
//     period holding today is still receiving runs and is refused.
//   - Determinism. The same records and the same folder give byte-identical
//     files, the index included (G3), so a regeneration in review diffs only
//     where a period changed.
//   - The folder is untrusted. A report is a file a person may edit by hand,
//     so the ones the pattern rules read are re-checked by shape, and a file
//     that fails is named and skipped, never fatal.

// digestNow is runs digest's clock: which period is closed, and which one a
// call without --date reports, depend on the day it runs. Tests replace it, as
// they replace lsNow.
var digestNow = time.Now

// digestMaxRuns bounds the read, as runs stats bounds its own listing: the
// newest this many runs, so a listing past it is reported over its newest part
// and says so. It is a variable for the same reason digestNow is -- a test
// reaches the warning without a bucket of five thousand runs -- and its value
// is the bound runs stats publishes.
var digestMaxRuns = userStatsMaxRuns

type digestOptions struct {
	cloud       cloudOptions
	period      string
	date        string
	repo        string
	dir         string
	stdout      bool
	asJSON      bool
	submitDraft bool
	due         bool
}

// digestDueDocVersion is runs digest --due --json's document version. Members
// are only ever added within a version (D784), so the schema in schemas/
// pins this number and a reused member name is a new name under a new version.
const digestDueDocVersion = 1

// digestDueDoc is runs digest --due --json's document: the periods that have
// closed with no report under Dir, newest first, and the newest report the
// folder holds. Due is always an array and Newest is null rather than absent,
// so every member is there on every document and a consumer branches on a
// value rather than on whether a member it needs was written.
type digestDueDoc struct {
	Version int              `json:"version"`
	Dir     string           `json:"dir"`
	Due     []digestDueRow   `json:"due"`
	Newest  *digestDueReport `json:"newest"`
}

// digestDueRow is one period that is due: the dimension its key belongs to,
// and the key itself, which is what a caller hands back to --date or greps the
// folder for.
type digestDueRow struct {
	Dim digest.Dim `json:"dim"`
	Key string     `json:"key"`
}

// digestDueReport is the newest report the folder holds: the period it covers,
// and how many whole days old that period is at the clock the reading ran
// with.
type digestDueReport struct {
	Key     string     `json:"key"`
	Dim     digest.Dim `json:"dim"`
	DaysAgo int        `json:"days_ago"`
}

// digestDoc is runs digest --json's document. The summary block and the
// patterns are internal/digest's own types, so a consumer reads the same
// shapes the committed file carries.
type digestDoc struct {
	// Repo is the repository reported (owner/name), and Period the key of the
	// period: the file's own key, and the name a reader greps for.
	Repo   string `json:"repo"`
	Period string `json:"period"`
	// Summary is the fenced block the report carries, and Patterns the fixed
	// rules (D326) read over it and the committed reports before it.
	Summary  digest.Summary   `json:"summary"`
	Patterns []digest.Pattern `json:"patterns,omitempty"`
	// Skipped names the files under --dir this call did not read as reports --
	// the ones it could not read and the ones whose name is not D321's -- by
	// their own path, in path order: never anything they hold (G1).
	Skipped []string `json:"skipped,omitempty"`
}

// digestRow is one report file under --dir: the period its own path names,
// and the summary block it carries when that can be read at all.
type digestRow struct {
	dim     digest.Dim
	key     string
	rel     string // the file's path under --dir, D321's <YYYY>/<key>.md when it is a row and no shape of this command's when it is skipped
	summary digest.Summary
	read    bool
}

func newRunsDigestCmd() *cobra.Command {
	var o digestOptions
	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Write a closed period's report of this repository into its own docs tree",
		Long: "Write one closed period's report of this repository, from its run records, into\n" +
			"the repository's own docs tree: <dir>/<YYYY>/<key>.md, plus the index\n" +
			"<dir>/README.md regenerated from the files there (D321).\n\n" +
			"The report is internal/digest's: the period's runs, the flagged ones and the\n" +
			"dearest of the rest, the stage figures, the summary block a later report reads,\n" +
			"and the fixed pattern rules (D326) read over that block and the committed ones.\n" +
			"A yearly report has no runs of its own to read -- the records are kept 90 days\n" +
			"(F24) -- so it is that year's monthly reports added up, and the report says how\n" +
			"many of the twelve it added up, so a partial year is not read as a whole one.\n\n" +
			"--date names any day inside the period. Without it the most recent closed period\n" +
			"is reported: the day, week, month or year before the one today falls in. A period\n" +
			"holding today is still receiving runs -- a run counts in the period it finished,\n" +
			"in UTC (G4) -- so it is refused and nothing is written for it, and so is a period\n" +
			"that has not begun yet.\n\n" +
			"The report is of this repository and of no other (G2, D322): --repo names it, and\n" +
			"without --repo it is the checkout's own origin. There is no --all. A record under\n" +
			"the repository's prefix whose own repository is another is not read into the\n" +
			"report, and no report names another repository.\n\n" +
			"Nothing in a report is a person or a task's own words (G1, D323): the flags,\n" +
			"recipes and figures are internal/digest's own closed vocabulary, and a report is\n" +
			"committed into a repository that may be public. Nothing here refuses in a coding\n" +
			"agent's session, which is what makes the scheduled report run possible.\n\n" +
			"The committed reports under --dir are the ones the pattern rules read, and each is\n" +
			"re-checked by shape before it is used: a report a person edited by hand, or a\n" +
			"markdown file whose name is not <YYYY>/<key>.md, is named on stderr by its own path\n" +
			"and left out of the patterns and of the index's figures, and the report being\n" +
			"written goes on. Nothing else in the folder is read, and the folder is never edited\n" +
			"except for the report and the index.\n\n" +
			"The read is the repository's runs bucket listing, newest first, capped at the bound\n" +
			"runs stats uses, and a listing longer than that says so on stderr. The listing is not\n" +
			"narrowed to the period: a run counts in the period it finished (G4), so a run that\n" +
			"started before the period and finished inside it is one of its runs. The bucket's\n" +
			"lifecycle age keeps the listing small, and F24's retention means a period older than\n" +
			"it has no records left -- which is why a yearly report rolls the committed monthly\n" +
			"ones up rather than reading records.\n\n" +
			"--stdout prints the report instead of writing it, and --json prints the summary\n" +
			"block and the patterns as {repo, period, summary, patterns[], skipped[]}. Neither\n" +
			"writes a file, so a caller can read a report before committing it.\n\n" +
			"--due is the cadence's own reading: it names the periods that have closed with no\n" +
			"report under --dir, newest first -- the period that ended most recently is the\n" +
			"first line, and of two that ended on the same day the longer one comes first --\n" +
			"and how old the newest report it found is. It reads that folder and nothing else --\n" +
			"no project, no credential, no bucket -- so it answers in a checkout that has never\n" +
			"been set up, and it writes nothing: --period, --date, --repo, --stdout and\n" +
			"--submit-draft have nothing to act on beside it and are refused by name. --json\n" +
			"prints it as {version, dir, due[], newest}.\n\n" +
			"--submit-draft prints neither: it prints a redacted learning draft a person can\n" +
			"file as a \"Skill learning\" issue, and it sends nothing. The draft leaves the\n" +
			"repository, so it carries no repository name and no run ID -- a period other\n" +
			"than a year renumbers its runs run-1, run-2, ... (a yearly draft rolls the\n" +
			"months up and has no run rows) -- and it ends with the one command that opens\n" +
			"the issue form, gh issue create --web -R " + updatecheck.Repo + ", which the\n" +
			"person runs themselves: the form lives in Fugaro's repository and -R is what\n" +
			"reaches it from the checkout this command read, and the person picks the\n" +
			"\"Skill learning\" form in the browser, because --web ignores --template. --dir\n" +
			"still names the folder the committed reports the pattern rules read live in;\n" +
			"nothing is written there, so --stdout and --json are refused beside it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRunsDigest(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.period, "period", string(digest.ByDay), "the period to report: day, week, month or year")
	f.StringVar(&o.date, "date", "", "any day inside the period (YYYY-MM-DD); default: the most recent closed period")
	f.StringVar(&o.repo, "repo", "", "the repository to report (owner/name, or a path inside its checkout); default: this checkout's origin")
	f.StringVar(&o.dir, "dir", digest.Dir, "the folder the reports live in, relative to the working directory")
	f.BoolVar(&o.stdout, "stdout", false, "print the report instead of writing it")
	f.BoolVar(&o.asJSON, "json", false, "print the summary block and the patterns as JSON")
	f.BoolVar(&o.submitDraft, "submit-draft", false, "print a redacted learning draft for a \"Skill learning\" issue; sends nothing")
	f.BoolVar(&o.due, "due", false, "print the closed periods this checkout has no report for; reads the folder and nothing else")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runRunsDigest(cmd *cobra.Command, o *digestOptions) error {
	if o.due {
		return runRunsDigestDue(cmd, o)
	}
	if o.stdout && o.asJSON {
		return userErr("--stdout and --json don't go together")
	}
	if o.submitDraft && o.stdout {
		return userErr("--submit-draft prints the draft instead of the report, so --stdout has nothing to print")
	}
	if o.submitDraft && o.asJSON {
		return userErr("--submit-draft prints a learning draft, not JSON; drop --json")
	}
	dim, err := digestDim(o.period)
	if err != nil {
		return err
	}
	p, err := digestPeriod(dim, o.date, digestNow())
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	repo, slug, err := digestRepo(ctx, env, o.cloud, o.repo)
	if err != nil {
		return err
	}

	// The period this call writes replaces its own file, so a committed file of
	// that period is not named as skipped: naming it would send a caller to
	// look for a file this call is about to write over.
	replaced := ""
	if !o.stdout && !o.asJSON && !o.submitDraft {
		replaced = p.Key
	}
	// The committed reports, read once: they are the pattern rules' history,
	// the months a yearly report rolls up, and the rows the index lists.
	folder, err := digestReportFiles(o.dir, replaced, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	rows := folder.rows

	var (
		cur    digest.Summary
		head   []byte
		months []digest.Summary
		// selected are the report's selected run rows, which --submit-draft
		// renumbers: a yearly report rolls its months up instead and has none.
		selected []digest.Run
	)
	if p.Dim == digest.ByYear {
		months = digest.MonthsOf(rows, p.Key)
		if len(months) == 0 {
			return userErr("no monthly report of %s is committed under %s, so there is no year to roll up: a yearly report is its twelve monthly ones added up, because the records are kept 90 days",
				p.Key, safetext.Strip(o.dir))
		}
		cur = digest.RollUp(p.Key, months)
		if cur.Period == "" {
			return userErr("the monthly reports of %s under %s are not ones this command wrote, so there is no year to roll up",
				p.Key, safetext.Strip(o.dir))
		}
		head = digest.YearMarkdown(p, months, cur)
	} else {
		recs, truncated, err := digestRecords(ctx, env, slug, repo)
		if err != nil {
			return err
		}
		if truncated {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: this repository's runs bucket listing holds more than %d runs; the report reads its newest %d, so an older period is reported over fewer of its runs\n",
				digestMaxRuns, digestMaxRuns)
		}
		d := digest.Build(recs, p)
		cur = d.Summary()
		head = digest.Markdown(d)
		selected = d.Runs
	}
	patterns := digest.Patterns(digest.Prev(rows, p), cur)
	body := digest.FileBody(head, rows, p, cur)

	doc := digestDoc{Repo: repo, Period: p.Key, Summary: cur, Patterns: patterns, Skipped: folder.skipped}
	switch {
	case o.submitDraft:
		_, err := cmd.OutOrStdout().Write(digestSubmitDraft(cur, selected, patterns))
		return err
	case o.stdout:
		_, err := cmd.OutOrStdout().Write(body)
		return err
	case o.asJSON:
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}

	path := filepath.Join(o.dir, p.Key[:4], p.Key+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	indexPath := filepath.Join(o.dir, digest.IndexName)
	index := digest.IndexMarkdown(digest.WithRow(rows, p, cur))
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", indexPath, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\nwrote %s\n", path, indexPath)
	return nil
}

// digestDim is --period as the time grouping it names.
func digestDim(period string) (digest.Dim, error) {
	dim, err := digest.ParseDim(period)
	if err != nil || !dim.IsPeriod() {
		return "", userErr("--period %q: use day, week, month or year", safetext.Strip(period))
	}
	return dim, nil
}

// digestPeriod is the period to report: the one --date falls in, or the most
// recent closed one without it. A period holding today is still open -- its
// runs are still arriving, and a run counts in the period it finished (G4) --
// so it is refused, and nothing is written for it.
func digestPeriod(dim digest.Dim, date string, now time.Time) (digest.Period, error) {
	today := now.UTC()
	if date == "" {
		// The day before the current period's first day is in the period
		// before it, whichever grouping this is: yesterday, the last day of
		// the previous week, month or year.
		cur, err := digest.NewPeriod(dim, today.Format("2006-01-02"))
		if err != nil {
			return digest.Period{}, userErr("--period %s: %v", dim, err)
		}
		start, err := time.Parse("2006-01-02", cur.Start)
		if err != nil {
			return digest.Period{}, fmt.Errorf("the period %s starts at %q, which is not a date", cur.Key, cur.Start)
		}
		date = start.AddDate(0, 0, -1).Format("2006-01-02")
	}
	p, err := digest.NewPeriod(dim, date)
	if err != nil {
		return digest.Period{}, userErr("--date %q: use a day (YYYY-MM-DD)", safetext.Strip(date))
	}
	if p.Holds(today) {
		return digest.Period{}, userErr("period %s is open: it holds %s, and its runs are still arriving. Report a closed period, or wait until %s has ended",
			p.Key, today.Format("2006-01-02"), p.Key)
	}
	// A period that starts after today has received no finished run at all: it
	// is not a closed period with no runs, it is not yet a period.
	if p.Start > today.Format("2006-01-02") {
		return digest.Period{}, userErr("period %s has not begun: it starts %s, after today (%s), so no run has finished in it. Report a closed period, or wait until %s has begun",
			p.Key, p.Start, today.Format("2006-01-02"), p.Key)
	}
	return p, nil
}

// digestRepo is the one repository this report is of (G2): --repo, or the
// checkout's own origin, as fugaro run resolves it. It returns both the
// owner/name a record's own repo field is compared against and the slug the
// runs bucket keeps that repository's runs under.
func digestRepo(ctx context.Context, env *cloudEnv, o cloudOptions, repo string) (string, string, error) {
	var err error
	if repo, err = repoArg(ctx, o, repo); err != nil {
		return "", "", err
	}
	if repo == "" {
		if repo, err = originRepo(ctx); err != nil {
			return "", "", err
		}
	}
	if _, err := task.CanonicalRepo(repo); err != nil {
		return "", "", userErr("--repo: %v", err)
	}
	slug, err := env.repoSlug(repo, checkoutOf(ctx, repo))
	if err != nil {
		return "", "", err
	}
	return repo, slug, nil
}

// digestRecords reads the repository's runs the way runs ls reads them -- the
// runs bucket's listing under this repository's own prefix, then one
// result.json per run -- newest first and bounded at runs stats' bound.
//
// The listing is not narrowed to the period. A run counts in the period it
// finished (G4), so a run that started before the period and finished inside
// it is one of its runs, and a listing taken from the period's own first day
// would leave it out of this report and out of the next one too. The bucket's
// lifecycle age (runs_days) keeps the whole listing small, and a listing
// longer than the bound is truncated to its newest runs and says so, rather
// than quietly reporting a period over part of itself.
//
// A run with no result.json has not finished, so it is in no period (G4), and
// a record nobody can read is one run's problem rather than the report's, as
// in runs ls: it is left out rather than read as a zero. A record whose own
// repository is not the one being reported is not this repository's run, and
// is dropped here, before anything of it is built into the report (G2).
func digestRecords(ctx context.Context, env *cloudEnv, slug, repo string) ([]runstore.Record, bool, error) {
	ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, time.Time{})
	if err != nil {
		return nil, false, remote(err)
	}
	truncated := len(ids) > digestMaxRuns
	if truncated {
		ids = ids[:digestMaxRuns]
	}
	recs := make([]runstore.Record, len(ids))
	found := make([]bool, len(ids))
	errs := make([]error, len(ids))
	parallel(len(ids), func(i int) {
		rec, err := absent(runstore.Open(env.bucket.Bucket, slug, ids[i]).ReadRecord(ctx))
		switch {
		case err == nil && rec != nil:
			recs[i], found[i] = *rec, true
		case err == nil:
			// No result.json: the run has not finished, so it is in no
			// period.
		case corruptObject(err):
			// A record nobody can read is one run's problem, not the
			// report's.
		default:
			errs[i] = err
		}
	})
	if err := errors.Join(errs...); err != nil {
		return nil, false, remote(fmt.Errorf("reading this repository's run records: %w", err))
	}
	out := make([]runstore.Record, 0, len(ids))
	for i, ok := range found {
		if ok && digestSameRepo(recs[i].Repo, repo) {
			out = append(out, recs[i])
		}
	}
	return out, truncated, nil
}

// digestSameRepo is whether a record's own repository is the one being
// reported. A record is untrusted input -- any run under the prefix can write
// one -- and its repo field is a word it chose, so a value that is not a
// repository name at all is not this repository's run either (G2).
func digestSameRepo(recRepo, repo string) bool {
	got, err := task.CanonicalRepo(recRepo)
	if err != nil {
		return false
	}
	want, err := task.CanonicalRepo(repo)
	return err == nil && got == want
}

// digestFolder is the reports a folder holds: one row per report file, and the
// files that are there and are not reports this command wrote, each by its own
// path under --dir (G1).
type digestFolder struct {
	rows    []digest.Row
	skipped []string
}

// digestReportFiles reads the report files under dir, oldest period first: one
// row per file whose name is D321's shape, carrying the summary block it holds
// when that can be read.
//
// A report is a file a person may edit by hand, so nothing here is trusted: a
// file whose block does not decode, whose block is not one this report writes,
// or whose block names a period other than the one its own path names, is a
// row with no summary. It is named on warn by its path -- a name this command
// checked against its own shape, never anything the file holds (G1) -- and the
// report being written goes on. A markdown file whose name is not D321's shape
// is a report renamed out of the layout: it is no row at all, and it is named
// the same way, so a report does not leave the index without a word.
//
// replaced is the period this call writes, if it writes one: that period's own
// file is about to be overwritten, so a stale one is not named as skipped.
func digestReportFiles(dir, replaced string, warn io.Writer) (*digestFolder, error) {
	years, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return &digestFolder{}, nil
	}
	if err != nil {
		return nil, userErr("reading %s: %v", safetext.Strip(dir), err)
	}
	folder := &digestFolder{}
	// A skipped file's name is the person's, and a person's file name can carry
	// a control character or an escape sequence: it is printed to a terminal and
	// carried in --json, so it is made one line of text in both places (G1).
	skip := func(rel string) {
		name := safetext.OneLine(rel)
		folder.skipped = append(folder.skipped, name)
		if warn != nil {
			fmt.Fprintf(warn, "warning: %s is not a report this command wrote; it is left out of the patterns and of the index's figures\n", name)
		}
	}
	for _, y := range years {
		if !y.IsDir() {
			if y.Name() != digest.IndexName {
				folder.add(dir, y.Name(), replaced, skip)
			}
			continue
		}
		if !digest.IsYearDir(y.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, y.Name()))
		if err != nil {
			return nil, userErr("reading %s: %v", filepath.Join(safetext.Strip(dir), y.Name()), err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			folder.add(dir, y.Name()+"/"+e.Name(), replaced, skip)
		}
	}
	sort.Slice(folder.rows, func(i, j int) bool { return folder.rows[i].Key < folder.rows[j].Key })
	sort.Strings(folder.skipped)
	return folder, nil
}

// add is one file the folder holds, read as a report: a file of D321's shape
// is a row whatever it holds, and a markdown file of any other name is named
// and left out. A file that is neither -- an image, a .gitkeep -- is no report
// this command could have written, and is passed over in silence.
func (f *digestFolder) add(dir, rel, replaced string, skip func(string)) {
	dim, key, ok := digest.ReportName(rel)
	if !ok {
		if strings.HasSuffix(rel, ".md") {
			skip(rel)
		}
		return
	}
	row := digest.Row{Dim: dim, Key: key, Rel: rel}
	switch s, read := digest.ReadRow(filepath.Join(dir, filepath.FromSlash(rel)), key); {
	case read:
		row.Summary, row.Read = s, true
	case key == replaced:
		// The report this call writes replaces this file, so it is not named
		// as one the call skipped.
	default:
		skip(rel)
	}
	f.rows = append(f.rows, row)
}

// runRunsDigestDue is `runs digest --due`: the cadence's own reading (D962,
// spec §6). It reads the committed report folder, prints the periods that have
// closed with no report and the age of the newest report it found, and stops
// there: no project config is selected, no credential is opened and no bucket
// is reached, so it answers in a checkout that has never been set up and it is
// safe in a coding agent's session.
//
// The periods are digest.DuePeriods(now), the one computation a report run's
// own step reads (R0), so what a person is told is missing and what a report
// run fills in cannot drift apart.
func runRunsDigestDue(cmd *cobra.Command, o *digestOptions) error {
	// --due prints no report and writes nothing, so a flag that selects a
	// report -- its period, its date, its repository, its destination or its
	// draft -- has nothing to act on here. Each is refused by name rather than
	// silently ignored, and by Changed rather than by value, so `--period day`
	// carrying its own default is refused too: the caller asked for something
	// this read does not do.
	for _, f := range []struct{ name, why string }{
		{"period", "--due reports every period that is due, so --period has nothing to select"},
		{"date", "--due asks today's clock which periods have closed, so --date has nothing to select"},
		{"repo", "--due reads the folder in this checkout, so --repo has nothing to name"},
		{"stdout", "--due prints no report, so --stdout has nothing to print"},
		{"submit-draft", "--due prints no draft; the draft is of a report this call does not write"},
	} {
		if cmd.Flags().Changed(f.name) {
			return userErr("--%s: %s", f.name, f.why)
		}
	}

	// An empty --dir names no folder, and this reading is of a folder: it is
	// refused rather than read as the default one, because a caller who typed
	// the flag asked about the folder they named and about no other.
	if o.dir == "" {
		return userErr("--dir: name the folder the reports are read from, or leave the flag out to read %s", digest.Dir)
	}

	// The folder is read the way the report writer reads it, so "a report is
	// here" means the same file in both: D321's shape, and a file whose name
	// is not that shape is no report at all. A row that exists but whose block
	// cannot be read still counts as the report it is named for -- the period
	// has a file -- which is why nothing here needs the summaries. Nothing is
	// warned about and nothing is written; the folder is untrusted input to
	// this read, never output.
	folder, err := digestReportFiles(o.dir, "", nil)
	if err != nil {
		return err
	}
	now := digestNow().UTC()
	periods := make([]digest.Period, 0)
	for _, p := range digest.DuePeriods(now) {
		if !digestHasReport(folder.rows, p) {
			periods = append(periods, p)
		}
	}
	// DuePeriods lists the catch-up's days and then the week, month or year
	// that closed, so a coarser period is not necessarily last: the list is
	// ordered here, by the day each period ended.
	sort.Slice(periods, func(i, j int) bool { return digestNewerPeriod(periods[i], periods[j]) })
	due := make([]digestDueRow, 0, len(periods))
	for _, p := range periods {
		due = append(due, digestDueRow{Dim: p.Dim, Key: p.Key})
	}
	newest := digestNewestReport(folder.rows, now)

	if o.asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(digestDueDoc{Version: digestDueDocVersion, Dir: o.dir, Due: due, Newest: newest})
	}
	writeRunsDigestDue(cmd.OutOrStdout(), o.dir, due, newest)
	return nil
}

// digestHasReport is whether the folder holds a report of this period. The
// dimension is compared as well as the key: a row's dimension comes from the
// shape of the file's name, and only a file of this period's own shape is this
// period's report.
func digestHasReport(rows []digest.Row, p digest.Period) bool {
	for _, r := range rows {
		if r.Key == p.Key && r.Dim == p.Dim {
			return true
		}
	}
	return false
}

// digestNewestReport is the report the folder holds that covers the latest
// period. Rows are in key order rather than period order, so the last row is
// not the newest report, and the end of a period is the package's own -- a
// week's last day is not a day a key spells.
func digestNewestReport(rows []digest.Row, now time.Time) *digestDueReport {
	var newest digest.Period
	found := false
	for _, r := range rows {
		p, err := digest.PeriodOfKey(r.Dim, r.Key)
		if err != nil {
			// A row's own path named its period, so this cannot happen; a file
			// that somehow reached a row without one is no period to report.
			continue
		}
		if found && !digestNewerPeriod(p, newest) {
			continue
		}
		newest, found = p, true
	}
	if !found {
		return nil
	}
	return &digestDueReport{Key: newest.Key, Dim: newest.Dim, DaysAgo: digestDaysAgo(newest.End, now)}
}

// digestNewerPeriod is whether a is the newer of two periods: the one that
// ended later, and the longer one where both ended on the same day (a day and
// the week that closes with it, a day and the month it ends). It orders the
// due list and picks the newest report, so a rule read in one place is not a
// rule the other reads differently.
func digestNewerPeriod(a, b digest.Period) bool {
	if a.End != b.End {
		return a.End > b.End
	}
	return digestPeriodRank(a.Dim) > digestPeriodRank(b.Dim)
}

// digestPeriodRank orders the four period dimensions from the shortest to the
// longest, which is how two periods that ended on the same day are ordered:
// the year before the month, the month before the week, the week before the
// day. Anything that is not a period is a day's length here, and no period of
// another dimension reaches this: DuePeriods writes the four and the layout
// names the four.
func digestPeriodRank(dim digest.Dim) int {
	switch dim {
	case digest.ByWeek:
		return 1
	case digest.ByMonth:
		return 2
	case digest.ByYear:
		return 3
	}
	return 0
}

// digestDaysAgo is how many whole days have passed between a period's last day
// and now. A period that has not ended yet -- a hand-written file named for a
// period in the future -- is 0 days old rather than a negative age.
func digestDaysAgo(end string, now time.Time) int {
	last, err := time.Parse("2006-01-02", end)
	if err != nil {
		return 0
	}
	days := int(now.Sub(last) / (24 * time.Hour))
	if days < 0 {
		return 0
	}
	return days
}

// writeRunsDigestDue prints the reading for a person: the periods that are
// due -- the list, or the one line that says there are none -- and the newest
// report with its age. Every line names the folder it is about, so a caller
// reading a log knows which checkout answered.
func writeRunsDigestDue(w io.Writer, dir string, due []digestDueRow, newest *digestDueReport) {
	name := safetext.Strip(dir)
	if len(due) == 0 {
		// One line, and it still says how old the newest report is: a caller
		// that runs this on a schedule reads one line either way.
		if newest == nil {
			fmt.Fprintf(w, "nothing is due under %s: the folder holds no report\n", name)
			return
		}
		fmt.Fprintf(w, "nothing is due under %s: the newest report is %s, %s\n", name, digestPeriodLabel(newest), digestDaysOld(newest.DaysAgo))
		return
	}
	if len(due) == 1 {
		fmt.Fprintf(w, "1 period is due under %s:\n", name)
	} else {
		fmt.Fprintf(w, "%d periods are due under %s:\n", len(due), name)
	}
	for _, d := range due {
		fmt.Fprintf(w, "  %s (%s)\n", d.Key, d.Dim)
	}
	if newest == nil {
		fmt.Fprintln(w, "the folder holds no report")
		return
	}
	fmt.Fprintf(w, "newest report: %s, %s\n", digestPeriodLabel(newest), digestDaysOld(newest.DaysAgo))
}

// digestPeriodLabel is a report's period as a person reads it: the key and the
// dimension it belongs to.
func digestPeriodLabel(r *digestDueReport) string {
	return fmt.Sprintf("%s (%s)", r.Key, r.Dim)
}

// digestDaysOld is n whole days as a person reads it.
func digestDaysOld(n int) string {
	if n == 1 {
		return "1 day old"
	}
	return fmt.Sprintf("%d days old", n)
}
