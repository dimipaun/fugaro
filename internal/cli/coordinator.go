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
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro coordinator status (docs/coordinator/implementation-plan.md, parts
// P1 and P2): the project view, computed and read — the deterministic half of
// Q13's answer, and the first thing in this work a person can look at.
//
// Three properties are the command rather than niceties around it:
//
//   - It reads. There is no store of the coordinator's own, no service, no
//     MCP front door and no model credential anywhere on this path: two
//     readers the tree already has — a gitprov.Provider and the runs bucket —
//     and the pure join in internal/coordinator between them.
//   - It is read-only to GitHub as well (P2, Q3 answered (A)): every
//     repository is read through the coordinator's own installation token,
//     minted per repository with a named read-only permission set and no
//     `write` anywhere (github.CoordinatorTokenPermissions), and cached for
//     the token's life. The provider's own mint — the run's set, which holds
//     two writes — is replaced before the first call.
//   - What it prints about a launcher is a DECLARATION. launch.json's
//     launched_by is a string a launcher writes about itself and nothing
//     verifies (reconciliation finding #11), so the view carries it as
//     declared_by and the table says so on every run of it.

// fugaro coordinator report (docs/coordinator/implementation-plan.md, part
// P4): the report path every orchestrator declares its work through, and the
// coordinator's half of the central orchestrator plan's P9.
//
// It is the spec's §6 "intent signals … sent *before* code exists" with the
// CLI as the transport, and it takes NOTHING BACK: the command writes one
// record and prints it, and it issues no advice, reads no state back and
// compares the declaration against nothing. The record's shape, its
// deterministic ID and its validation live in internal/coordinator
// (Intent, IntentID, Reporter), because `report_intent` (P8) is this same
// writer behind the front door and a second transport must not invent a
// second record.
//
// What it writes is a DECLARATION, never an identity: who declares it is the
// caller's own string — the local config's user:, else git config user.email,
// the string a run's launch.json records (reconciliation finding #11) — and
// nothing here verifies it or authorises anything by it.
//
// The credential is the caller's own: the database client is the budget
// commands' (newBudgetClient, budget.rtdb_url, the person's ADC). The rules
// grant /coordinator a READ and no write to anybody (Q15 A, P3), so what
// admits this write is the database IAM its caller holds —
// roles/firebasedatabase.admin, which terraform.budget_admins have — and a
// refusal says so rather than being reported as an outage.

// coordinatorReportOptions are report's flags.
type coordinatorReportOptions struct {
	cloud    cloudOptions
	repo     string
	branch   string
	release  string
	files    []string
	services []string
	asJSON   bool
}

func newCoordinatorReportCmd() *cobra.Command {
	var o coordinatorReportOptions
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Declare the work you are about to start",
		Long: "Write one intent record for this project's repository: the branch and the\n" +
			"target release the caller declares, and the files and services it is about\n" +
			"to touch, so that work still in flight can be seen before code exists\n" +
			"(spec §6).\n\n" +
			"The record's ID is derived from the caller and the declaration, and from\n" +
			"nothing else: reporting the same declaration twice writes ONE document, and\n" +
			"the second report replaces the first rather than adding to it.\n\n" +
			"The caller is who the local config names (user:), else git config\n" +
			"user.email, and a report nobody can be named for is refused: every record\n" +
			"is somebody's declaration, and one nobody can be named for is not one. That\n" +
			"name is a declaration too — nothing verifies it, and nothing authorises\n" +
			"anything by it (finding #11).\n\n" +
			"This command writes and reads nothing back: it prints the record it wrote\n" +
			"and issues no advice, which is the coordinator's whole posture in v1\n" +
			"(spec §3, advisory and never authoritative).\n\n" +
			"The write goes through the caller's own credentials, on the project's\n" +
			"budget.rtdb_url database. The rules grant /coordinator a read and no write\n" +
			"to anyone, so the write is admitted by the database role its caller holds\n" +
			"(roles/firebasedatabase.admin, which terraform.budget_admins have); a\n" +
			"launcher who is not an admin reports through the coordinator's front door\n" +
			"once it is deployed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorReport(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository this report is about (owner/name; default: the local config's, when it lists one)")
	f.StringVar(&o.branch, "branch", "", "the branch you are about to work on")
	f.StringVar(&o.release, "release", "", "the target release (default: the one the branch names, e.g. release/v0.11.0)")
	f.StringArrayVar(&o.files, "file", nil, "a path you are about to touch (repeatable)")
	f.StringArrayVar(&o.services, "service", nil, "a service you are about to touch (repeatable)")
	f.BoolVar(&o.asJSON, "json", false, "print the record as it was written")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorReport(cmd *cobra.Command, o *coordinatorReportOptions) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	repo, err := coordinatorReportRepo(env, o.repo)
	if err != nil {
		return err
	}
	// The caller, before anything is opened or written: a report nobody can be
	// named for is refused, and the refusal is localcfg.Me's own (it names
	// both places the identity can come from).
	me, err := env.lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	co := checkoutOf(ctx, repo)()
	slug, err := env.repoSlug(repo, func() *config.Config { return co })
	if err != nil {
		return err
	}
	store, err := openCoordinatorStore(ctx, env.lc)
	if err != nil {
		return err
	}
	rec, path, err := (&coordinator.Reporter{Store: store}).Report(ctx, slug, coordinator.Intent{
		DeclaredBy: me,
		Branch:     o.branch,
		Release:    o.release,
		Files:      o.files,
		Services:   o.services,
	})
	if err != nil {
		return coordinatorReportErr(env.lc, err)
	}
	out := cmd.OutOrStdout()
	if o.asJSON {
		return writeCoordinatorJSON(out, rec)
	}
	return writeCoordinatorReport(out, rec, path)
}

// coordinatorReportRepo is the one repository a report is about: --repo's,
// else the single one the local config lists. There is no --all here and there
// must not be: a declaration is made by one caller about the work it is about
// to start, and a report about every repository at once would be a report
// about none of them.
func coordinatorReportRepo(env *cloudEnv, repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	repos := slices.Sorted(maps.Keys(env.lc.Repos))
	switch len(repos) {
	case 0:
		return "", userErr("the local config lists no repository: add one to its repos, or name one with --repo")
	case 1:
		return repos[0], nil
	}
	return "", userErr("the local config lists %d repositories; name the one this report is about with --repo", len(repos))
}

// coordinatorReportErr classifies a report that failed. The report path's own
// refusals are the caller's (an unnamed caller, a declaration that declares
// nothing), and so is a database that refused the write: /coordinator is
// readable by a signed-in reader and writable by nobody through the rules
// (Q15 A, P3), so what admits the write is the database role its caller holds.
// Everything else — an unreachable database, a bad address — is the host's.
func coordinatorReportErr(lc *localcfg.Config, err error) error {
	switch {
	case errors.Is(err, coordinator.ErrUnnamedCaller), errors.Is(err, coordinator.ErrNothingDeclared):
		return userErr("%v", err)
	case errors.Is(err, rtdb.ErrPermission):
		return userErr("project %s's database refused the write: /coordinator is readable by a signed-in reader and writable by nobody through the rules, so a report is written under the database role its caller holds (roles/firebasedatabase.admin, which terraform.budget_admins have); ask an owner to add you, or report through the coordinator's front door once it is deployed (%v)", lc.Name, err)
	}
	return remote(err)
}

// openCoordinatorStore opens the coordinator's store over the project's own
// database: the same client, the same address and the same credential the
// budget commands open (newBudgetClient, the caller's ADC with the database
// scopes). The store's own check, and not the rules and not IAM, is what keeps
// the writer to the coordinator's subtree (D194), and it mints nothing.
//
// A package variable, so a test replaces exactly the one thing that would
// reach a database.
var openCoordinatorStore = func(ctx context.Context, lc *localcfg.Config) (*coordinator.Store, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, userErr("project %s has no Firebase database to report to (budget.rtdb_url is not set in its project config): an operator runs fugaro update --firebase-project %s --name %s", lc.Name, lc.GCPProject, lc.Name)
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, err
	}
	return coordinator.NewStore(db), nil
}

// writeCoordinatorReport prints the record the command wrote, and the path it
// wrote it to. It is the record and nothing else: no advice, no comparison
// against what is in flight, and no summary — the view (status) is where a
// person reads what collides, and this command is not a second, worse answer
// to that question.
func writeCoordinatorReport(w io.Writer, rec coordinator.Intent, path string) error {
	if _, err := fmt.Fprintf(w, "reported /%s\n", path); err != nil {
		return err
	}
	fmt.Fprintf(w, "  declared by %s at %s\n", coordinatorCell(rec.DeclaredBy), rec.At.Format(time.RFC3339))
	fmt.Fprintf(w, "  branch %s\n", coordinatorCell(rec.Branch))
	fmt.Fprintf(w, "  release %s\n", coordinatorCell(rec.Release))
	fmt.Fprintf(w, "  files %s\n", coordinatorCellList(rec.Files))
	fmt.Fprintf(w, "  services %s\n", coordinatorCellList(rec.Services))
	return nil
}

// coordinatorCellList is one cell holding a list somebody else wrote: "none"
// for a list that declares nothing, and otherwise the values clipped and made
// safe for a terminal line, one by one.
func coordinatorCellList(vals []string) string {
	if len(vals) == 0 {
		return "none"
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, coordinatorCell(v))
	}
	return clipRunes(strings.Join(out, ", "), coordinatorMaxCellRunes*2)
}

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
	all    bool
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
	cmd.AddCommand(newCoordinatorReportCmd())
	return cmd
}

func newCoordinatorStatusCmd() *cobra.Command {
	var o coordinatorOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the work in flight, and where it collides",
		Long: "Join a repository's pull requests with its run records and print the view:\n" +
			"each pull request, the runs behind it, the branch and target release it declares,\n" +
			"the files and services those runs touched, and every pair of work still in\n" +
			"flight that touches the same file or service.\n\n" +
			"One repository by default, every repository the local config lists with --all,\n" +
			"which prints a view per repository and compares nothing across them: a service\n" +
			"is a path's shape in one tree, so two repositories' paths say nothing about\n" +
			"each other.\n\n" +
			"The pull requests listed are the ones the runs name, and that is the whole of\n" +
			"the list this reader can produce: gitprov reads one pull request by number and\n" +
			"has no call that lists a repository's (finding #10), so a pull request no run\n" +
			"names is never asked for. The rule the join is written to — a pull request no\n" +
			"run names is listed as having none, because it is work somebody has open and\n" +
			"dropping it is the one thing this view must not do — is not reached by this\n" +
			"command today.\n\n" +
			"Every repository is read through the coordinator's own credential: an\n" +
			"installation token for that repository, minted from the GitHub App the\n" +
			"environment holds and carrying a named read-only permission set — contents,\n" +
			"issues, metadata and pull requests, all read (Q3 answered (A)) — and cached\n" +
			"for the token's life, so a second read of the same repository mints nothing.\n" +
			"The coordinator reads github only: a bitbucket repository is refused, and the\n" +
			"refusal names the reason, because its credential is one repository's access\n" +
			"token and cannot be widened to a project (reconciliation Q12 (A), finding #30).\n\n" +
			"The launcher of a run is printed as what it is — the string the run's own launch\n" +
			"record wrote about itself — and never as an identity.\n\n" +
			"This command reads and writes nothing of its own: no store of the coordinator's\n" +
			"own, no service, no model credential. It is the read the CLI already performs,\n" +
			"the runs bucket and a pull request read that can only read.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorStatus(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository to read (owner/name; default: the local config's, when it lists one)")
	f.BoolVar(&o.all, "all", false, "read every repository the local config lists, one view each")
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
	repos, err := coordinatorRepos(env, o)
	if err != nil {
		return err
	}
	reader, err := coordinatorReader(warn)
	if err != nil {
		return err
	}
	views := make([]coordinator.View, 0, len(repos))
	for _, repo := range repos {
		view, err := coordinatorViewOf(ctx, env, reader, repo, coordinatorSince(window), warn)
		if err != nil {
			return err
		}
		views = append(views, view)
	}
	out := cmd.OutOrStdout()
	if o.all {
		if o.asJSON {
			return writeCoordinatorJSON(out, coordinator.ProjectView{Repos: views})
		}
		return writeCoordinatorProject(out, views)
	}
	if o.asJSON {
		return writeCoordinatorJSON(out, views[0])
	}
	return writeCoordinatorStatus(out, views[0])
}

// coordinatorRepos is the repositories the view is about: --all's every one
// the local config lists, else --repo's, else the single one there is.
//
// --all with --repo is refused rather than resolved: one names a whole project
// and the other one repository, and a run that guessed which of the two the
// person meant would print a view they did not ask for. The same rule is why
// a config listing more than one repository is refused when neither flag is
// given (P1's rule, unchanged).
func coordinatorRepos(env *cloudEnv, o *coordinatorOptions) ([]string, error) {
	repos := slices.Sorted(maps.Keys(env.lc.Repos))
	switch {
	case o.all && o.repo != "":
		return nil, userErr("--all reads every repository the project lists and --repo names one; give one of the two")
	case o.all:
		if len(repos) == 0 {
			return nil, userErr("the local config lists no repository: add one to its repos")
		}
		return repos, nil
	case o.repo != "":
		return []string{o.repo}, nil
	}
	switch len(repos) {
	case 0:
		return nil, userErr("the local config lists no repository: add one to its repos, or name one with --repo")
	case 1:
		return repos, nil
	}
	return nil, userErr("the local config lists %d repositories; name one with --repo, or read every one with --all", len(repos))
}

// coordinatorViewOf reads one repository's view: its runs out of the bucket,
// the pull requests those runs name out of the host through the project's
// reader, and the join between them.
//
// Every repository is read, including one whose runs name no pull request: the
// reader's own refusal of a project it does not serve is about what the project
// holds and not about whether it has work in flight, so a bitbucket repository
// is refused the same way whether or not it has runs.
func coordinatorViewOf(ctx context.Context, env *cloudEnv, reader coordinator.Reader, repo string, since time.Time, warn io.Writer) (coordinator.View, error) {
	co := checkoutOf(ctx, repo)()
	slug, err := env.repoSlug(repo, func() *config.Config { return co })
	if err != nil {
		return coordinator.View{}, err
	}
	runs, err := coordinatorRuns(ctx, env, slug, since, warn)
	if err != nil {
		return coordinator.View{}, err
	}
	prs, err := reader.PullRequests(ctx, env.repoProvider(repo, co), repo, coordinatorNumbers(runs))
	if err != nil {
		// The refusal of a project this read does not serve is the
		// configuration's (reconciliation Q12 (A), finding #30). Everything else
		// is the host's — unless the reader kept the class of what failed, which
		// it does for the mint's own refusal of a missing App credential
		// (coordinator's redactedError): remote() marks a failure exit 2 only
		// when it does not already carry a code, so a user error the reader
		// rewrote the text of is still a user error here.
		if errors.Is(err, coordinator.ErrBitbucket) {
			return coordinator.View{}, userErr("%v", err)
		}
		return coordinator.View{}, remote(err)
	}
	return coordinator.Join(repo, prs, runs), nil
}

// coordinatorNumbers is the pull requests the runs name, each once, in the
// order the runs list them.
//
// gitprov.Provider reads one pull request by number and has no call that lists
// a repository's (reconciliation finding #10), so this is the whole of what a
// reader can be asked for however it reads them: each run's task or record
// names the pull request it is on, and a pull request no run names is never
// asked for.
func coordinatorNumbers(runs []coordinator.Run) []int {
	var numbers []int
	for _, run := range runs {
		if run.PR != 0 && !slices.Contains(numbers, run.PR) {
			numbers = append(numbers, run.PR)
		}
	}
	return numbers
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

// coordinatorReader is the project-wide read the command uses: P2's reader,
// with the read-only mint, the provider opener and the redaction wired into
// it (D193's answer of 2026-10-21: one read-only installation token per
// repository, cached for its life).
//
// The wiring is here and not at the call site so that it is one place to read
// and one place to change, and so that a test replaces exactly the two things
// that would reach GitHub — the opener and the mint — and leaves the cache,
// the refusals and the redaction under test.
//
// agent.Redact is the redactor: the same one the runner hands the agent, so a
// credential cannot reach a failure by a path this command forgot about.
func coordinatorReader(warn io.Writer) (coordinator.Reader, error) {
	open, err := openCoordinatorProvider(os.Environ(), func(s string) { fmt.Fprintln(warn, multiLine(s)) })
	if err != nil {
		return nil, userErr("%v", err)
	}
	// The mint runs on the first call a repository's provider makes, and that
	// call happens only for a repository whose runs name a pull request: this
	// read needs no App credential to look at a project with nothing in it, and
	// nothing here reads one to find that out.
	return &coordinator.Project{Mint: coordinatorMint, Open: open, Redact: agent.Redact}, nil
}

// coordinatorAppCredential is the App credential the coordinator's mint needs:
// the App's ID, and its private key PEM read the way the opener reads it
// (providers.AppKey, so the mint and the opener cannot disagree about where the
// key is). Each is named when it is missing, and the failure is the user's own
// rather than the host's.
func coordinatorAppCredential() (appID, pemData string, err error) {
	appID = os.Getenv(providers.EnvGitHubAppID)
	if appID == "" {
		return "", "", userErr("%s is not set: the project-wide read mints the coordinator's own token and needs the GitHub App's ID (see docs/git-providers.md)", providers.EnvGitHubAppID)
	}
	pemData, err = providers.AppKey(os.Environ())
	if err != nil {
		return "", "", userErr("%v", err)
	}
	return appID, pemData, nil
}

// openCoordinatorProvider opens the git providers the view is read through. It
// is exec.go's own providerOptions, called with no --provider override, so the
// coordinator opens a provider exactly the way a run does: providers.FromEnv,
// on the App credential the environment carries. Tests replace it.
//
// What it returns is the coordinator's own ProviderOpener, and the difference
// from gitprov.Opener is the whole of P2's credential decision: the source it
// is handed is installed on the provider before its first call, so every
// installation token the read then uses is the coordinator's read-only one and
// never the run's set, which holds two writes.
//
// The App credential is asked for before the provider is opened, and that is
// the second half of the decision. providers.FromEnv reports a variable it does
// not find as a plain error, and a failure through the host is a remote one
// (exit 2) — so without this check a person who simply has not exported the
// App's ID would be told the host failed and would retry it, when the refusal
// is theirs to fix and the same condition is already a user error where the
// mint asks for the credential (coordinatorAppCredential, exec 1). Asking here
// is also what makes that wording reachable: the opener refuses before the mint
// is reached, so the mint's own message could never be the one a person saw.
// It costs a repository whose runs name a pull request the same two values read
// a second time, and nothing at all for one without: Project.PullRequests
// returns before opening when the runs name no pull request, so a project with
// nothing in it is still read with no App credential anywhere.
var openCoordinatorProvider = func(env []string, warn func(string)) (coordinator.ProviderOpener, error) {
	_, open, err := providerOptions(execOptions{}, env, warn)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
		if _, _, err := coordinatorAppCredential(); err != nil {
			return nil, nil, err
		}
		p, secrets, err := open(ctx, gitprov.KindGitHub, repo)
		if err != nil {
			return nil, secrets, err
		}
		if err := installCoordinatorSource(p, src); err != nil {
			return nil, secrets, err
		}
		return p, secrets, nil
	}, nil
}

// installCoordinatorSource hands src to p as the source of every installation
// token it uses. It is the whole of P2's credential decision at the point where
// it is enforced: from here on every call this provider makes is authenticated
// with the coordinator's own read-only token and never with the run's set,
// which holds two writes.
//
// A provider that cannot be handed a source is a refusal and never a fallback:
// such a provider would mint the run's set on its first call, and the point of
// the credential is that this read cannot reach a permission it does not need
// (Q3 answered (A)). It is a function of its own so that both halves are
// testable where they live: the install, and the refusal.
func installCoordinatorSource(p gitprov.Provider, src gitprov.TokenSource) error {
	setter, ok := p.(gitprov.ServiceTokenSetter)
	if !ok {
		return fmt.Errorf("a %s provider cannot be given the coordinator's read-only token source", gitprov.KindGitHub)
	}
	setter.UseServiceTokens(src)
	return nil
}

// coordinatorMint mints the coordinator's installation token for one
// repository: github.CoordinatorTokenPermissions — contents, issues, metadata
// and pull requests, every one of them read — through
// github.MintInstallationToken, with the App credential the environment
// carries. One repository at a time (D193(A)): the token is scoped to the
// repository it was minted for, and a second repository mints its own.
//
// The credential — the App's ID and its key PEM — is read through
// coordinatorAppCredential, which reads it the same way the opener does, so
// where the key comes from is one rule. A failure this returns carries no key:
// what it reports is redacted with the PEM it read (agent.Redact), because the
// mint's own error text can quote what it was handed. Tests replace it, so a
// read never reaches GitHub.
var coordinatorMint = func(ctx context.Context, repo string) (string, time.Time, error) {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return "", time.Time{}, userErr("repository %q must look like owner/name", repo)
	}
	appID, pemData, err := coordinatorAppCredential()
	if err != nil {
		return "", time.Time{}, err
	}
	key, err := github.ParsePrivateKey([]byte(pemData))
	if err != nil {
		return "", time.Time{}, userErr("%v", err)
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner:      owner,
		Repo:       name,
		AppID:      appID,
		PrivateKey: key,
		BaseURL:    os.Getenv(providers.EnvGitHubAPIURL),
	}, github.CoordinatorTokenPermissions())
	if err != nil {
		return "", time.Time{}, remote(errors.New(agent.Redact(err.Error(), []string{pemData})))
	}
	return token, expires, nil
}

// writeCoordinatorJSON prints a view document as it is: the document is the
// data — one repository's coordinator.View, or the project's
// coordinator.ProjectView — and every field of it is described by the type
// that carries it.
func writeCoordinatorJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// writeCoordinatorProject prints every repository's view, one after another,
// each in the table writeCoordinatorStatus draws.
func writeCoordinatorProject(w io.Writer, views []coordinator.View) error {
	for i, v := range views {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := writeCoordinatorStatus(w, v); err != nil {
			return err
		}
	}
	return nil
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
