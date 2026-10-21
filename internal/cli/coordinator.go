package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/coordinator"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro coordinator status (docs/coordinator/implementation-plan.md, part
// P1): the project view, computed and read — the deterministic half of Q13's
// answer, and the first thing in this work a person can look at.
//
// Three properties are the command rather than niceties around it:
//
//   - It reads. There is no store of the coordinator's own, no service, no
//     MCP front door and no model credential anywhere on this path: two
//     readers the tree already has — a gitprov.Provider and the runs bucket —
//     and the pure join in internal/coordinator between them.
//   - It is about one repository, named. A project-wide view is P2's, and it
//     needs the read-only installation token that part decides; this one
//     reads the way every other read command does.
//   - What it prints about a launcher is a DECLARATION. launch.json's
//     launched_by is a string a launcher writes about itself and nothing
//     verifies (reconciliation finding #11), so the view carries it as
//     declared_by and the table says so on every run of it.

// coordinatorDefaultSince is status's default window, and it is runs ls's:
// the two read the same objects over the same span, and a person comparing
// them must not have to remember that one of them looked further back.
var coordinatorDefaultSince = lsDefaultSince

// coordinatorSince is --since's span as the instant the runs bucket is read
// from: zero for no bound (parseSince's own answer), otherwise that far back
// from lsNow — runs ls's clock, so a test that pins the window pins this
// command's too and the two view the same runs.
func coordinatorSince(window time.Duration) time.Time {
	if window <= 0 {
		return time.Time{}
	}
	return lsNow().Add(-window)
}

// coordinatorDeclaredNote is the line every table carries under it, because
// the column above it is the one column of this view that is not a fact.
const coordinatorDeclaredNote = "declared by is the launcher each run's own launch record wrote about itself\n" +
	"(launch.json's launched_by): a declaration, and never a verified identity\n" +
	"(docs/coordinator/reconciliation.md, finding #11)."

// coordinatorMaxCellRunes bounds one cell of the table. A branch, a path and
// a declaration are all strings somebody else wrote, and none of them may
// widen a column to the width of the terminal.
const coordinatorMaxCellRunes = 60

// coordinatorTruncatedNote is printed under the table when any record it lists
// has a run whose stored file set is short of the one it changed
// (runstore.ChangedFiles). It is a note and not a cell's footnote because the
// truncated set is what the collisions below it were computed over: a reader
// who sees "3+" has to know that the pairs are the ones those runs recorded,
// and not that there are no others.
const coordinatorTruncatedNote = "n+ in FILES is a run whose changed-file set is short of the one it changed\n" +
	"(runstore.ChangedFiles): the paths it lists, and every collision computed from\n" +
	"them, are the ones the run recorded and not necessarily all of them."

// coordinatorOptions are status's flags.
type coordinatorOptions struct {
	cloud  cloudOptions
	repo   string
	since  string
	asJSON bool
}

func newCoordinatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coordinator",
		Short: "The coordinator's project view: what is in flight, and what collides",
		Long: "The coordinator's deterministic half (docs/coordinator/implementation-plan.md).\n" +
			"Nothing here holds a model credential, a service of its own or a store of its\n" +
			"own: these commands read what fugaro already reads.\n\n" +
			"Advisory, never authoritative: a collision is reported and never blocks,\n" +
			"reorders or refuses a merge (spec §3).",
	}
	cmd.AddCommand(newCoordinatorStatusCmd())
	return cmd
}

func newCoordinatorStatusCmd() *cobra.Command {
	var o coordinatorOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show one repository's work in flight, and where it collides",
		Long: "Join one repository's pull requests with its run records and print the view:\n" +
			"each pull request, the runs behind it, the branch and target release it declares,\n" +
			"the files and services those runs touched, and every pair of work still in\n" +
			"flight that touches the same file or service.\n\n" +
			"The pull requests listed are the ones this repository's runs name, and that is\n" +
			"the whole of the list this reader can produce: gitprov reads one pull request\n" +
			"by number and has no call that lists a repository's (finding #10), so a pull\n" +
			"request no run names is never asked for. The rule the join is written to — a\n" +
			"pull request no run names is listed as having none, because it is work\n" +
			"somebody has open and dropping it is the one thing this view must not do — is\n" +
			"not reached by this command today, and P2 is the part that reads widely\n" +
			"enough to reach it.\n\n" +
			"The launcher of a run is printed as what it is — the string the run's own launch\n" +
			"record wrote about itself — and never as an identity.\n\n" +
			"This command reads and writes nothing: no store of the coordinator's own, no\n" +
			"service, no credential of its own. It is the per-repository read the CLI already\n" +
			"performs with the repository's own provider credential, and the runs bucket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorStatus(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository to read (owner/name; default: the local config's, when it lists one)")
	f.StringVar(&o.since, "since", coordinatorDefaultSince, "only runs started within this long ("+coordinatorDefaultSince+", 36h, 90m; 0 for all)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorStatus(cmd *cobra.Command, o *coordinatorOptions) error {
	ctx := cmd.Context()
	window, err := parseSince(o.since)
	if err != nil {
		return userErr("%v", err)
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	warn := cmd.ErrOrStderr()
	repo, slug, kind, err := coordinatorTarget(ctx, env, o.repo)
	if err != nil {
		return err
	}
	runs, err := coordinatorRuns(ctx, env, slug, coordinatorSince(window), warn)
	if err != nil {
		return err
	}
	prs, err := coordinatorPullRequests(ctx, repo, kind, runs, warn)
	if err != nil {
		return err
	}
	view := coordinator.Join(repo, prs, runs)
	if o.asJSON {
		return writeCoordinatorJSON(cmd.OutOrStdout(), view)
	}
	return writeCoordinatorStatus(cmd.OutOrStdout(), view)
}

// coordinatorTarget is the repository the view is about and how it is read:
// --repo's, else the local config's when it lists exactly one, and the git
// provider kind repoSlug resolved (the config's entry and the checkout's
// fugaro.yaml must agree, and repoSlug is the one rule that refuses them
// disagreeing).
func coordinatorTarget(ctx context.Context, env *cloudEnv, flag string) (repo, slug, kind string, err error) {
	if flag == "" {
		repos := slices.Sorted(maps.Keys(env.lc.Repos))
		switch len(repos) {
		case 0:
			return "", "", "", userErr("the local config lists no repository: add one to its repos, or name one with --repo")
		case 1:
			flag = repos[0]
		default:
			return "", "", "", userErr("the local config lists %d repositories; name one with --repo", len(repos))
		}
	}
	co := checkoutOf(ctx, flag)()
	slug, err = env.repoSlug(flag, func() *config.Config { return co })
	if err != nil {
		return "", "", "", err
	}
	return flag, slug, env.repoProvider(flag, co), nil
}

// coordinatorRuns reads the runs the view holds: every run of slug minted
// within the window, newest first (runstore.ListRunIDs), read the way the
// other read commands read them (readRun) and reduced to what the view needs.
//
// A run whose object is unreadable is still a run: readRun's warning goes to
// the command's stderr, and what could be read is what the view shows, so an
// unreadable result.json does not make a pull request disappear.
func coordinatorRuns(ctx context.Context, env *cloudEnv, slug string, since time.Time, warn io.Writer) ([]coordinator.Run, error) {
	ids, err := runstore.ListRunIDs(ctx, env.bucket.Bucket, slug, since)
	if err != nil {
		return nil, remote(err)
	}
	objs := make([]runObjects, len(ids))
	errs := make([]error, len(ids))
	parallel(len(ids), func(i int) {
		objs[i], errs[i] = readRun(ctx, env, slug, ids[i])
	})
	if err := errors.Join(errs...); err != nil {
		return nil, remote(err)
	}
	out := make([]coordinator.Run, 0, len(objs))
	for _, o := range objs {
		if o.warning != "" {
			fmt.Fprint(warn, multiLine(o.warning))
		}
		out = append(out, coordinatorRun(o.in))
	}
	return out, nil
}

// coordinatorRun is one run as the view holds it, out of the objects the
// bucket has: the pull request it is on, its branch and base branch, what its
// own record says about it, and the paths it changed.
//
// Two rules here are the view's and not the store's. The pull request is the
// record's, else the task's — the same order runview.Row uses, so the two
// views of one run never disagree about which pull request it is on — and a
// run is terminal only when its record says a terminal status: a run whose
// record is missing or unreadable is still in flight, which is the direction
// that reports work rather than hiding it.
func coordinatorRun(in runview.Input) coordinator.Run {
	run := coordinator.Run{ID: in.RunID, Files: []string{}}
	if in.Task != nil {
		run.PR, run.Branch = in.Task.PR, in.Task.Branch
	}
	if in.Launch != nil {
		// The one field of this view that is sanitised here rather than by
		// the renderer, as runview.Row.LaunchedBy's own comment says of the
		// same string: a launcher writes it, so it can carry escapes,
		// newlines or 10 KB of text, and both documents this row appears in
		// are rendered as they are.
		run.DeclaredBy = safetext.Field(in.Launch.LaunchedBy)
	}
	if r := in.Record; r != nil {
		if r.PR != nil && r.PR.Number != 0 {
			run.PR = r.PR.Number
		}
		run.Status, run.Outcome = string(r.Status), string(r.Outcome)
		run.Terminal = r.Status != "" && r.Status != runstore.StatusRunning
		if r.Branch != "" {
			run.Branch = r.Branch
		}
		run.BaseBranch = r.BaseBranch
		if cf := r.ChangedFiles.Bounded(); cf != nil {
			run.Files, run.FilesTruncated = cf.Paths, cf.Truncated
		}
	}
	return run
}

// openCoordinatorProvider opens the git provider the view is read through. It
// is exec.go's own providerOptions, called with no --provider override, so the
// coordinator opens a provider exactly the way a run does: providers.FromEnv,
// on the App credential the environment carries. Tests replace it.
var openCoordinatorProvider = func(env []string, warn func(string)) (gitprov.Opener, error) {
	_, open, err := providerOptions(execOptions{}, env, warn)
	return open, err
}

// coordinatorPRWorkers bounds the pull-request reads that are in flight at
// once. The numbers are a handful and each read is one round trip, so the
// bound is about what the host and the installation token will take rather
// than about this process: it is small, and smaller than lsWorkers, because
// the same App credential also mints the token behind every one of them.
const coordinatorPRWorkers = 4

// coordinatorPullRequests reads the pull requests the runs name, a few at a
// time.
//
// gitprov.Provider reads one pull request by number and has no call that lists
// a repository's (reconciliation finding #10), so P1's pull requests are the
// ones the runs bucket declares: each run's task or record names the pull
// request it is on, and each of those numbers is read from the host. That is
// what "one repository's view" means here, and it is deliberately the whole of
// it — P2 replaces this reader with the project-wide one, and the join is
// written against the pull requests and not against this reader, so that swap
// changes the implementation rather than the shape (D193).
//
// The reads are concurrent and the view is not: each answer is written to its
// own slot, so the pull requests are in the order the runs named them however
// the goroutines interleave, and the join's own sort is fed the same slice in
// the same order on every run. Both adapters are safe to call this way — the
// GitHub one serialises its token and identity lookups and the Bitbucket one
// its identity lookup, and both read through httpjson on one http.Client.
//
// A read that fails fails the command. A view missing the pull requests the
// host would not answer for would be a partial view presented as a whole one,
// which is the failure mode this command exists to avoid; the failures are
// collected by number, so the error names the first number that could not be
// read whatever finished first.
//
// The credential values the opener returns are used for nothing but redaction:
// no failure this command prints may carry one (agent.Redact; the opener's own
// values, never a GitAuth token, are all it can hold).
func coordinatorPullRequests(ctx context.Context, repo, kind string, runs []coordinator.Run, warn io.Writer) ([]coordinator.PullRequest, error) {
	var numbers []int
	for _, run := range runs {
		if run.PR != 0 && !slices.Contains(numbers, run.PR) {
			numbers = append(numbers, run.PR)
		}
	}
	if len(numbers) == 0 {
		return nil, nil
	}
	open, err := openCoordinatorProvider(os.Environ(), func(s string) { fmt.Fprintln(warn, multiLine(s)) })
	if err != nil {
		return nil, userErr("%v", err)
	}
	p, secrets, err := open(ctx, kind, repo)
	if err != nil {
		return nil, remote(errors.New(agent.Redact(err.Error(), secrets)))
	}
	out := make([]coordinator.PullRequest, len(numbers))
	errs := make([]error, len(numbers))
	parallelN(len(numbers), coordinatorPRWorkers, func(i int) {
		info, err := p.PullRequest(ctx, numbers[i])
		if err != nil {
			errs[i] = fmt.Errorf("reading pull request %d of %s: %s", numbers[i], repo, agent.Redact(err.Error(), secrets))
			return
		}
		out[i] = coordinator.PullRequest{
			Number: info.Number,
			URL:    info.URL,
			Title:  info.Title,
			Branch: info.SourceBranch,
			Draft:  info.Draft,
			State:  string(info.State),
		}
	})
	for _, err := range errs {
		if err != nil {
			return nil, remote(err)
		}
	}
	return out, nil
}

// writeCoordinatorJSON prints the view itself: the document is the data, and
// every field of it is described by the type that carries it.
func writeCoordinatorJSON(w io.Writer, v coordinator.View) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// writeCoordinatorStatus prints the view as a table, then the collisions.
func writeCoordinatorStatus(w io.Writer, v coordinator.View) error {
	fmt.Fprintf(w, "%s: %d records, %d in flight\n", cellOneLine(v.Repo), len(v.Records), v.InFlight())
	if len(v.Records) == 0 {
		fmt.Fprintln(w, "no work in flight, and no pull request the runs bucket knows about")
		fmt.Fprintf(w, "\n%s\n", coordinatorDeclaredNote)
		return nil
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WORK\tBRANCH\tRELEASE\tRUNS\tFILES\tSTATE\tDECLARED BY")
	truncated := false
	for _, r := range v.Records {
		state := "landed"
		if r.InFlight {
			state = "in flight"
		}
		runs := "none"
		if len(r.Runs) > 0 {
			runs = strconv.Itoa(len(r.Runs))
		}
		files := strconv.Itoa(len(r.Files))
		if recordFilesTruncated(r) {
			files += "+"
			truncated = true
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			cellOneLine(r.Label()),
			coordinatorCell(r.Branch),
			coordinatorCell(r.Release),
			runs,
			files,
			state,
			coordinatorCell(declaredBy(r)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if truncated {
		fmt.Fprintf(w, "\n%s\n", coordinatorTruncatedNote)
	}
	fmt.Fprintln(w)
	if len(v.Collisions) == 0 {
		fmt.Fprintln(w, "no collisions")
	} else {
		fmt.Fprintln(w, "COLLISIONS")
		for _, c := range v.Collisions {
			var what []string
			if len(c.Files) > 0 {
				what = append(what, strings.Join(c.Files, ", "))
			}
			if len(c.Services) > 0 {
				what = append(what, "service "+strings.Join(c.Services, ", "))
			}
			fmt.Fprintf(w, "%s and %s: %s\n", cellOneLine(c.A), cellOneLine(c.B),
				cellOneLine(clipRunes(strings.Join(what, "; "), coordinatorMaxCellRunes*2)))
		}
	}
	fmt.Fprintf(w, "\n%s\n", coordinatorDeclaredNote)
	return nil
}

// recordFilesTruncated is whether any run behind the record stored a file set
// short of the one it changed. The record's FILES cell then reads "n+", and
// the table carries the note that says what the "+" means: the collisions the
// view reports are computed over the paths the runs recorded, and a truncated
// set is not the whole one.
func recordFilesTruncated(r coordinator.Record) bool {
	for _, run := range r.Runs {
		if run.FilesTruncated {
			return true
		}
	}
	return false
}

// declaredBy is the launcher of the record's newest run — the view lists a
// record's runs newest first — and "" when it has none.
func declaredBy(r coordinator.Record) string {
	if len(r.Runs) == 0 {
		return ""
	}
	return r.Runs[0].DeclaredBy
}

// coordinatorCell is one cell of the table: "-" for a value the view does not
// have, and otherwise the value clipped and made safe for a terminal cell. A
// branch, a release and a declaration are all strings somebody else wrote.
func coordinatorCell(s string) string {
	if s == "" {
		return "-"
	}
	return cellOneLine(clipRunes(s, coordinatorMaxCellRunes))
}
