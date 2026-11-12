// `fugaro runs watch`: the long-run triage, printed (O14c, BF2 slice 1).
//
// A run can outlive its usefulness without failing, and the daemon decides
// what to do about one (O14a's rule, acted on by O14b's seams). That decision
// is otherwise only visible after the fact, in the one line of audit the tick
// leaves once it has already acted. This command prints it first, for every run
// in the window, so a person can read what the daemon would decide before it
// does.
//
// What makes the print worth reading is that it is not a second opinion. The
// rule is orchestrator.Triage and the reading is GatherTriageInput, the same
// two functions the daemon's tick calls, and the threshold is TriageAfter's own
// ceiling for the run's mode — the same expression the cycle builds its targets
// with. Nothing here re-implements a gate, and no number here is this file's
// own.
//
// It is a reading and nothing else, which is the whole of what D1247 asks of
// it: no record, no audit line, no cancel marker, no work item, and no host
// call that changes anything. It carries no flag that acts — no --merge, no
// --apply, no --yes, no --force — so there is no argv that reaches a write from
// here, and the L2 review can check that by mutation rather than by trust.
//
// The one figure this command has to derive for itself is the age. The daemon
// ages a run from the intent it wrote into its own database node, which a
// reader of the runs bucket has no part of and no permission to read. A run ID
// is minted with the run's own start time (task.NewRunID), so the age printed
// here is the run's mint time against now: the closest reading available
// outside the daemon, and the one the document names as age_seconds.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// runsWatchDocVersion is the version of this command's --json document. The
// shape is new here, so it starts at 1, and it keeps the contract rule every
// other runs document keeps (runsShowDocVersion, summaryDocVersion): a member
// is only ever added within a version, and a renamed or re-meant one is a new
// member under a new name.
const runsWatchDocVersion = 1

// watchNotDue is what the print says about a run the rule read nothing about:
// the run has not reached the threshold, so no outcome was taken.
//
// NotDue is the zero TriageKind and prints as the empty string, and an empty
// outcome in a document reads as a field nobody wrote rather than as "not yet".
// So this print has a word of its own for it — the only one of the five that is
// not the rule's — and it is written on every run the rule did not judge.
const watchNotDue = "not-due"

// openWatchProvider is OpenProvider, a seam for the one host this command opens
// (openFollowUpsProvider's pattern): the triage's readings are made with the
// process environment's own credential, the way every read outside a run's own
// job is.
var openWatchProvider = OpenProvider

// runsWatchOptions is runs watch's own flags: the window runs ls lists, and the
// document. There is deliberately no flag beyond these — a flag that acted
// would make an argv from this command a way to merge or cancel a run, which is
// the daemon's to do on evidence and never a person's on a listing.
type runsWatchOptions struct {
	cloud              cloudOptions
	repo, since, batch string
	all, mine, asJSON  bool
}

// watchRow is one run of the window and what the rule answered about it.
type watchRow struct {
	// Run is the whole `<slug>/<run-id>` reference, as every runs document
	// carries it, and Repo the repository the run's task names. "run" carries
	// the pair rather than an ending because the ending is a display of one
	// window and a consumer of the document is not reading this window.
	Run  string `json:"run"`
	Repo string `json:"repo"`
	// AgeSeconds is the run's own mint time against now, which is the reading
	// the print has of how long the run has been going (this file's header).
	AgeSeconds int64 `json:"age_seconds"`
	// Outcome is the rule's kind, or watchNotDue. Reasons are the closed codes
	// behind it in the rule's own order — empty for a run it did not judge, and
	// never null.
	Outcome string   `json:"outcome"`
	Reasons []string `json:"reasons"`
	// Unreadable names the input the rule could not read, which is set exactly
	// when the outcome is recommend-only for that reason.
	Unreadable string `json:"unreadable"`
	// WithoutAPerson is true for the two outcomes taken without asking: a
	// merge-as-is lands work and a restart cancels the run.
	WithoutAPerson bool `json:"without_a_person"`

	// ending is the run's short ending, which the RUN cell of the text table
	// prints (task.DisplayRuns over the window's rows). It is a display of this
	// window alone and is never written to the document, which carries the
	// whole reference above.
	ending string
	// repoText is the REPO cell's text. It is repoCell's own rule — the
	// repository the run's task names, falling back to the slug the run was
	// found under — so a run whose task nobody can read still names a
	// repository in the table, while the document's `repo` keeps the meaning
	// every runs document gives it, which is empty for exactly that run.
	repoText string
}

// watchDoc is runs watch --json's document. Every member is written on every
// run, the arrays empty included, so a consumer reads one without a nil check.
type watchDoc struct {
	Version int    `json:"version"`
	Project string `json:"project"`
	// ThresholdSeconds is what a run is judged against: the ladder's ceiling
	// for the default mode, which is the threshold the daemon's own cycle
	// builds its targets with. It is in the document because an outcome is not
	// readable without it — the same recommend-only means something else
	// against a different ceiling.
	ThresholdSeconds int64 `json:"threshold_seconds"`
	// Runs is the window's runs, newest first, in the listing's own order.
	Runs []watchRow `json:"runs"`
	// Warnings are the lines a human print shows above its table: the reads
	// this command could not make, and the runs whose repository it could not
	// resolve. A run whose reading was incomplete is still printed, with the
	// rule's own answer for it.
	Warnings []string `json:"warnings"`
}

func newRunsWatchCmd() *cobra.Command {
	var o runsWatchOptions
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Print the triage the daemon would decide about the window's long runs",
		Long: "Print, for every run in the window, what the daemon's long-run triage would\n" +
			"decide about it: the outcome, the closed codes behind it, the input nobody\n" +
			"could read, and the threshold the decision is taken against.\n\n" +
			"The window is runs ls's: the same --repo, --all, --mine, --since and --batch,\n" +
			"and the same listing behind them. Each run of it is read the way the daemon\n" +
			"reads one — its record from its own storage, its pull request and its checks\n" +
			"from the host the run's repository is on, and the findings artifact it\n" +
			"finalized with — and the outcome is the rule's own, so the print is what the\n" +
			"daemon would decide and not a second opinion about it.\n\n" +
			"This command changes nothing at all. It writes no record, no audit line, no\n" +
			"cancel marker and no work item, it merges nothing, it cancels nothing, and it\n" +
			"has no flag that acts on a run: everything the triage does is the daemon's,\n" +
			"on its own evidence, and this is the reading a person takes before that.\n\n" +
			"An input that cannot be read is never an error: the rule answers\n" +
			"recommend-only, which merges, cancels and restarts nothing, and the print\n" +
			"names the input — record, findings, changed paths or ci — beside the outcome.\n" +
			"A run under the threshold is printed as not-due: the rule read nothing about\n" +
			"it, and that is not one of the four outcomes.\n\n" +
			"The age is the run's own mint time against now. The daemon ages a run from\n" +
			"the attempt it recorded in its own database, which this command does not\n" +
			"read: the two agree to the minute for a run the daemon started, and a run\n" +
			"somebody else started has no attempt for the daemon to age at all.\n\n" +
			"--json prints {version, project, threshold_seconds, runs[], warnings}; runs\n" +
			"carries {run, repo, age_seconds, outcome, reasons[], unreadable,\n" +
			"without_a_person}.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runRunsWatch(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "only this repository (owner/name, or a path inside its checkout)")
	f.BoolVar(&o.all, "all", false, "every repository in the runs bucket")
	f.BoolVar(&o.mine, "mine", false, "only runs you requested")
	f.StringVar(&o.since, "since", lsDefaultSince, "only runs started within this long ("+lsDefaultSince+", 36h, 90m; 0 for all)")
	f.StringVar(&o.batch, "batch", "", "only runs of this batch")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runRunsWatch(cmd *cobra.Command, o *runsWatchOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if o.repo != "" && o.all {
		return userErr("--repo and --all don't go together")
	}
	var err error
	if o.repo, err = repoArg(ctx, o.cloud, o.repo); err != nil {
		return err
	}
	since, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	now := lsNow()
	doc, err := watchRead(ctx, env, o, since, now)
	if err != nil {
		return err
	}
	return writeRunsWatch(cmd.OutOrStdout(), doc, o.asJSON)
}

// watchRead is one read of the window: the runs the flags select, and what the
// rule answers about each of them.
//
// The listing is runs ls's own (lsSlugs, loadRows), so a run this command
// prints is a run a listing shows, in the listing's order and under the same
// flags. noCache is set because a cached object is what a listing saw and a
// decision has to see the bucket.
func watchRead(ctx context.Context, env *cloudEnv, o *runsWatchOptions, since time.Duration, now time.Time) (watchDoc, error) {
	doc := watchDoc{
		Version: runsWatchDocVersion, Project: env.lc.Name,
		ThresholdSeconds: int64(orchestrator.TriageAfter(rigor.Default) / time.Second),
		Runs:             []watchRow{}, Warnings: []string{},
	}
	// The loader's own lines — a corrupt result.json, a run read partially —
	// reach the document and the table above it rather than a stderr nobody
	// reading --json sees. A run's own reading is then printed with them beside
	// it, which is the direction a partial read has to fail in: shown, not
	// dropped.
	var lines strings.Builder
	f := lsFilter{batch: o.batch, warn: &lines, noCache: true}
	var err error
	if f.slugs, err = lsSlugs(ctx, env, &lsOptions{repo: o.repo, all: o.all}, &lines); err != nil {
		return watchDoc{}, err
	}
	if o.mine {
		if f.mine, err = env.lc.Me(ctx); err != nil {
			return watchDoc{}, userErr("--mine: %v", err)
		}
	}
	if since > 0 {
		f.since = now.Add(-since)
	}
	rows, err := loadRows(ctx, env, f, now)
	if err != nil {
		return watchDoc{}, err
	}
	doc.Warnings = append(doc.Warnings, watchLines(lines.String())...)

	warn := func(format string, args ...any) {
		doc.Warnings = append(doc.Warnings, oneLine(fmt.Sprintf(format, args...)))
	}
	kind, open, err := openWatchProvider(os.Environ(), func(w string) { warn("%s", w) })
	if err != nil {
		// A host that cannot be built is not a failure of the print: the rule
		// reads it as CI nobody could read and answers recommend-only, which is
		// the same answer the daemon gives a project whose provider settings
		// are incomplete (orchestratorTriageSeams).
		warn("no code host is configured, so no run's checks are read: %v", err)
		open = nil
	}
	endings := runEndings(rows)
	opened := map[string]gitprov.Provider{}
	for _, row := range rows {
		ref := rowRef(row)
		in, err := watchInput(ctx, env, open, kind, opened, warn, ref, row)
		if err != nil {
			return watchDoc{}, err
		}
		in.Elapsed, in.Threshold = now.Sub(row.Created), orchestrator.TriageAfter(rigor.Default)
		doc.Runs = append(doc.Runs, watchRowOf(row, ref, endings[ref], now, orchestrator.Triage(in)))
	}
	return doc, nil
}

// watchInput is the daemon's own reading of one run, made with the two
// functions the daemon makes it with: the record out of the run's own storage
// and GatherTriageInput, over the host for the run's repository.
//
// The repository is resolved the way the daemon's repoFor resolves it —
// followUpsRepo over the run's launcher-written task and the slug the run was
// found under — and never out of the record's own repo field, which the job
// writes: a job that could name another repository would have this command ask
// a forge about a repository the run never touched (F24, F30).
//
// Every reading that cannot be made leaves the rule's own unreadable input
// rather than an error: a run whose repository this machine cannot resolve is
// read with no host, exactly as the daemon reads it, and the rule answers
// recommend-only with the input named.
func watchInput(ctx context.Context, env *cloudEnv, open gitprov.Opener, kind string, opened map[string]gitprov.Provider, warn func(string, ...any), ref task.Ref, row runview.Row) (orchestrator.TriageInput, error) {
	store := runstore.Open(env.bucket.Bucket, ref.Slug, ref.RunID)
	rec, err := store.ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		rec = nil
	case err != nil && corruptObject(err):
		// A run whose record cannot be read is printed rather than dropped,
		// the way runs ls lists a run whose objects it read partially: the
		// rule's own answer for it is recommend-only with the record named,
		// and a listing that failed whole over one broken object would take
		// the window's other runs with it.
		warn("warning: run %s: its record could not be read: %v", row.Run, err)
		rec = nil
	case err != nil:
		return orchestrator.TriageInput{}, remote(err)
	}
	spec, err := store.ReadTask(ctx)
	if err != nil || spec == nil {
		// The daemon's repoFor reads the task the same way and falls back to
		// the item's repository: a task nobody can read leaves the run's
		// repository to this machine's config, which is the same answer here.
		spec = &task.Spec{}
	}
	repo, err := followUpsRepo(ctx, env, ref.Slug, ref.RunID, spec)
	if err != nil {
		warn("warning: run %s: %v", row.Run, err)
		return GatherTriageInput(ctx, store, nil, rec)
	}
	prov, err := watchProvider(ctx, env, open, kind, opened, warn, repo)
	if err != nil {
		warn("warning: run %s: no host for %s, so its checks are not read: %v", row.Run, repo, err)
		return GatherTriageInput(ctx, store, nil, rec)
	}
	return GatherTriageInput(ctx, store, prov, rec)
}

// watchProvider is the host for one repository, opened at most once per run of
// this command: the credential a real provider mints is cached inside it, and a
// provider opened per run would mint one per run and hold none.
//
// The kind comes from the project config's own answer for the repository, as
// the daemon's provider reads it, and the opener is the one this command built
// once from its environment.
func watchProvider(ctx context.Context, env *cloudEnv, open gitprov.Opener, kind string, opened map[string]gitprov.Provider, warn func(string, ...any), repo string) (gitprov.Provider, error) {
	if repo == "" {
		return nil, errors.New("no repository to open a provider for")
	}
	if p, ok := opened[repo]; ok {
		return p, nil
	}
	if open == nil {
		return nil, errors.New("no code host is configured")
	}
	if k := env.repoProvider(repo, nil); k != "" {
		kind = k
	}
	p, _, err := open(ctx, kind, repo)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("no provider for %s", repo)
	}
	opened[repo] = p
	return p, nil
}

// watchRowOf is one run of the window as the document carries it. The outcome's
// codes are copied in the rule's own order and are checked against the closed
// vocabulary first, so a code nothing can place is never written to a document
// a reader outside this binary consumes.
func watchRowOf(row runview.Row, ref task.Ref, ending string, now time.Time, out orchestrator.TriageOutcome) watchRow {
	wr := watchRow{
		Run: ref.Slug + "/" + ref.RunID, Repo: row.Repo, AgeSeconds: int64(now.Sub(row.Created) / time.Second),
		Outcome: watchOutcome(out), Reasons: []string{}, Unreadable: string(out.Unreadable),
		WithoutAPerson: out.WithoutAPerson, ending: ending,
		repoText: cellOneLine(clipRunes(repoCell(row), maxNameCellRunes)),
	}
	for _, r := range out.Reasons {
		if !orchestrator.KnownTriageReason(r) {
			continue
		}
		wr.Reasons = append(wr.Reasons, string(r))
	}
	return wr
}

// watchOutcome is the rule's kind as the document spells it, with this print's
// own word for the run it read nothing about.
func watchOutcome(out orchestrator.TriageOutcome) string {
	if !out.Due() {
		return watchNotDue
	}
	return string(out.Kind)
}

// watchLines is what a writer collected as lines: the loader's own warnings,
// each line of its own.
func watchLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// watchCell is a row's TRIAGE cell: the outcome, the codes behind it, and the
// input nobody could read. It is composed the way the tick composes its own
// audit line — the outcome, a colon, the codes — so a person reading this
// before the daemon acts and the log afterwards read the same words for the
// same decision.
func watchCell(r watchRow) string {
	if r.Outcome == watchNotDue {
		return watchNotDue
	}
	line := r.Outcome + ": " + strings.Join(r.Reasons, " ")
	if r.Unreadable != "" {
		line += " (unreadable: " + r.Unreadable + ")"
	}
	return line
}

func writeRunsWatch(w io.Writer, doc watchDoc, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	for _, line := range doc.Warnings {
		fmt.Fprintln(w, multiLine(line))
	}
	threshold := time.Duration(doc.ThresholdSeconds) * time.Second
	if len(doc.Runs) == 0 {
		// A window nothing ran in is said plainly, not shown as an empty table
		// a reader could take for a failure.
		_, err := fmt.Fprintf(w, "no run in this window · judged after %s\n", age(threshold))
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tREPO\tAGE\tTRIAGE")
	for _, r := range doc.Runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", oneLine(r.ending), r.repoText,
			age(time.Duration(r.AgeSeconds)*time.Second), oneLine(watchCell(r)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	noun := "runs"
	if len(doc.Runs) == 1 {
		noun = "run"
	}
	_, err := fmt.Fprintf(w, "%s %s · judged after %s\n", strconv.Itoa(len(doc.Runs)), noun, age(threshold))
	return err
}
