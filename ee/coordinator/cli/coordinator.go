// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
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
//     and the pure join in ee/coordinator between them.
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
// deterministic ID and its validation live in ee/coordinator
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
	cloud    fcli.CloudFlags
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
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorReport(cmd *cobra.Command, o *coordinatorReportOptions) error {
	ctx := cmd.Context()
	env, err := fcli.OpenCloud(ctx, &o.cloud)
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
	me, err := env.Config().Me(ctx)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return err
	}
	store, err := openCoordinatorStore(ctx, env.Config())
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
		return coordinatorReportErr(env.Config(), err)
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
func coordinatorReportRepo(env *fcli.Cloud, repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	repos := slices.Sorted(maps.Keys(env.Config().Repos))
	switch len(repos) {
	case 0:
		return "", fcli.UserErr("the local config lists no repository: add one to its repos, or name one with --repo")
	case 1:
		return repos[0], nil
	}
	return "", fcli.UserErr("the local config lists %d repositories; name the one this report is about with --repo", len(repos))
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
		return fcli.UserErr("%v", err)
	case errors.Is(err, rtdb.ErrPermission):
		return fcli.UserErr("project %s's database refused the write: /coordinator is readable by a signed-in reader and writable by nobody through the rules, so a report is written under the database role its caller holds (roles/firebasedatabase.admin, which terraform.budget_admins have); ask an owner to add you, or report through the coordinator's front door once it is deployed (%v)", lc.Name, err)
	}
	return fcli.RemoteErr(err)
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
		return nil, fcli.UserErr("project %s has no Firebase database to report to (budget.rtdb_url is not set in its project config): an operator runs fugaro update --firebase-project %s --name %s", lc.Name, lc.GCPProject, lc.Name)
	}
	db, err := fcli.NewBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
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
	return fcli.ClipRunes(strings.Join(out, ", "), coordinatorMaxCellRunes*2)
}

// fugaro coordinator sweep (docs/coordinator/implementation-plan.md, part P5):
// the aggregation job's entry point — the scheduled Cloud Run job the
// installation root creates, on the pattern the history job runs — and the
// manual invocation of it an owner's live check uses.
//
// It is the one command of the coordinator's that writes, and it writes as the
// coordinator's own account: the job runs as fugaro-coordinator, whose account
// holds roles/firebasedatabase.admin on the Firebase project (D194) because IAM
// cannot express a path. So what keeps it to coordinator/** is the store's own
// check, which every write here goes through — not the rules, which the account
// bypasses, and not IAM, which cannot say "this prefix".
//
// The job has no project config file: Terraform gives it the plain environment
// below, and the account's identity is its only credential. The GitHub read is
// P2's, unchanged: the coordinator's own read-only installation token per
// repository, minted from the App the environment carries.
const (
	// envCoordinatorRepos is the repositories one tick reconciles, as JSON:
	// [{"provider":"github","repo":"owner/name"}, ...]. Terraform writes it
	// from the installation's own repository list.
	envCoordinatorRepos = "FUGARO_COORDINATOR_REPOS"
	// coordinatorSweepTimeout bounds one tick. The job's own timeout is longer
	// (600s), so a pass that runs out of time ends cleanly and the next tick
	// continues rather than the platform killing it mid-write.
	coordinatorSweepTimeout = 8 * time.Minute
)

// coordinatorSweepWiring points the sweep at fakes; tests only.
type coordinatorSweepWiring struct {
	// noAuth sends no credentials to the database, which is the fake's.
	noAuth bool
	// reader is the project-wide read, in place of the one the App credential
	// in the environment builds.
	reader coordinator.Reader
	// bucket opens the runs bucket, in place of "gs://" + FUGARO_RUNS_BUCKET.
	bucket func(ctx context.Context, name string) (*blobx.Bucket, error)
	now    func() time.Time
}

var coordinatorSweepTest *coordinatorSweepWiring

func newCoordinatorSweepCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:    "sweep",
		Short:  "Recompute the coordinator's views (internal)",
		Hidden: true,
		Long: "Recompute every repository's view from what GitHub and the runs bucket say,\n" +
			"write it into the coordinator's state, and mark the repositories it could not\n" +
			"reconcile instead of writing half a view.\n\n" +
			"This is the aggregation job's entry point: a scheduled Cloud Run job runs it\n" +
			"as the coordinator's own account, which holds the database's admin role\n" +
			"because IAM cannot scope a grant to a path. It writes the coordinator's\n" +
			"subtree and nothing else, and that is the store's own check rather than the\n" +
			"database's rules.\n\n" +
			"The job has no project config file: it reads its repositories, its database,\n" +
			"its runs bucket and its GitHub App from the environment Terraform sets on it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), coordinatorSweepTimeout)
			defer cancel()
			return runCoordinatorSweep(ctx, cmd, os.Getenv, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the tick's result as JSON")
	return cmd
}

func runCoordinatorSweep(ctx context.Context, cmd *cobra.Command, getenv func(string) string, asJSON bool) error {
	env := map[string]string{}
	for _, k := range []string{fcli.EnvProject, fcli.EnvFirebaseFP, fcli.EnvRTDBURL, fcli.EnvRunsBucket, envCoordinatorRepos} {
		if env[k] = getenv(k); env[k] == "" {
			return fcli.UserErr("the coordinator's sweep needs %s in its environment (Terraform sets it on the job)", k)
		}
	}
	if strings.ContainsAny(env[fcli.EnvRunsBucket], "/: ") {
		return fcli.UserErr("%s must be a bucket name", fcli.EnvRunsBucket)
	}
	repos, err := coordinatorSweepRepos(env[envCoordinatorRepos])
	if err != nil {
		return err
	}
	test := coordinatorSweepTest
	if err := rtdb.ValidateURL(env[fcli.EnvRTDBURL], test != nil && test.noAuth); err != nil {
		return fcli.UserErr("%s: %v", fcli.EnvRTDBURL, err)
	}
	// The account may hold admin rights on several databases; write only to
	// this Fugaro project's own (plan R-9, the history sweep's own check).
	if err := fcli.CheckDatabaseHost(env[fcli.EnvRTDBURL], env[fcli.EnvFirebaseFP]); err != nil {
		return fcli.UserErr("%v", err)
	}

	var dbAuth rtdb.Auth
	if test != nil && test.noAuth {
		dbAuth = rtdb.Auth{IDToken: func() string { return "" }}
	} else {
		ts, err := google.DefaultTokenSource(ctx, fcli.BudgetScopes()...)
		if err != nil {
			return fcli.RemoteErr(fmt.Errorf("no credentials for the coordinator's database: %w", err))
		}
		dbAuth = rtdb.Auth{Source: ts}
	}
	db, err := rtdb.New(env[fcli.EnvRTDBURL], dbAuth)
	if err != nil {
		return fcli.UserErr("%s: %v", env[fcli.EnvRTDBURL], err)
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, budget.PathProject, &raw)
	if err != nil {
		return fcli.RemoteErr(err)
	}
	var mark string
	if !found || json.Unmarshal(raw, &mark) != nil || mark != env[fcli.EnvProject] {
		return fcli.UserErr("the database at %s is not project %s's budget database (its /fugaro/project does not name the project); nothing was swept", env[fcli.EnvRTDBURL], safetext.OneLine(env[fcli.EnvProject]))
	}

	openBkt := func(ctx context.Context, name string) (*blobx.Bucket, error) { return blobx.Open(ctx, "gs://"+name) }
	if test != nil && test.bucket != nil {
		openBkt = test.bucket
	}
	bucket, err := openBkt(ctx, env[fcli.EnvRunsBucket])
	if err != nil {
		return fcli.RemoteErr(err)
	}
	defer bucket.Close()

	warn := cmd.ErrOrStderr()
	var reader coordinator.Reader
	var tokens *coordinatorTokens
	if test != nil && test.reader != nil {
		reader = test.reader
	} else if reader, tokens, err = coordinatorReader(warn); err != nil {
		return err
	}
	// The tick holds each repository's token for as long as it reads and no
	// longer, as status does: a scheduled job that left them behind would leave
	// one per repository every tick.
	defer tokens.revoke(ctx)
	sw := &coordinator.Sweeper{
		Reader: reader,
		Runs:   coordinatorRunsSource{env: fcli.CloudOverBucket(bucket), warn: warn},
		Store:  coordinator.NewStore(db),
	}
	if test != nil && test.now != nil {
		sw.Now = test.now
	}
	res, err := sw.Sweep(ctx, repos)
	out := cmd.OutOrStdout()
	if asJSON {
		if werr := writeCoordinatorJSON(out, res); werr != nil {
			return werr
		}
	} else if werr := writeCoordinatorSweep(out, res); werr != nil {
		return werr
	}
	// A repository that could not be reconciled is not a failed tick: it is
	// recorded (the stale marker) and the exit code stays 0. A store that
	// refused a write is the sweep's own failure, and it is the job's exit
	// code, which is where an operator reads it.
	return fcli.RemoteErr(err)
}

// coordinatorSweepRepos parses envCoordinatorRepos into the repositories one
// tick reconciles. The slug is derived here rather than carried, because it is
// a function of the provider and the name (task.Slug) and the runs bucket's
// tree is keyed by it: a second spelling in the environment would be a second
// thing to keep in step with that tree.
func coordinatorSweepRepos(raw string) ([]coordinator.Repo, error) {
	var in []struct {
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fcli.UserErr("%s is not the JSON list of the repositories this job reconciles (%v)", envCoordinatorRepos, err)
	}
	out := make([]coordinator.Repo, 0, len(in))
	for _, r := range in {
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			return nil, fcli.UserErr("%s: %v", envCoordinatorRepos, err)
		}
		out = append(out, coordinator.Repo{Provider: r.Provider, Name: r.Repo, Slug: slug})
	}
	return out, nil
}

// coordinatorRunsSource is the runs bucket as the sweep's RunsReader: the
// command's own read (coordinatorRuns), so the sweep and the view cannot
// disagree about what a repository's runs are.
//
// The window is the sweep's (coordinator.RunsWindow, 30 days), wider than
// status's: a tick reconciles the repository, and a view computed from the
// last 36 hours of runs would forget every pull request older than that. It
// is still a window, because a tick that read every run a repository ever had
// would grow with the repository's history every 15 minutes.
type coordinatorRunsSource struct {
	env  *fcli.Cloud
	warn io.Writer
}

func (s coordinatorRunsSource) Runs(ctx context.Context, repo coordinator.Repo, since time.Time) ([]coordinator.Run, error) {
	return coordinatorRuns(ctx, s.env, repo.Slug, since, s.warn)
}

// writeCoordinatorSweep prints one line per repository and the tick's own
// count. It is a report of what was written, and never advice: the view
// (status) is where a person reads what is in flight and what collides.
func writeCoordinatorSweep(w io.Writer, res coordinator.Result) error {
	for _, r := range res.Repos {
		if r.Stale {
			fmt.Fprintf(w, "%s: stale: %s\n", safetext.OneLine(r.Repo), safetext.OneLine(r.Reason))
			continue
		}
		fmt.Fprintf(w, "%s: %d records, %d in flight, %d collisions, %d pull requests read, %d unread, %d records pruned\n",
			safetext.OneLine(r.Repo), r.Records, r.InFlight, r.Collisions, r.Read, r.Unread, r.Pruned)
		if r.Omitted > 0 {
			fmt.Fprintf(w, "%s: %d records left out of the view (its bound is %d)\n", safetext.OneLine(r.Repo), r.Omitted, coordinator.MaxViewRecords)
		}
		if r.Undecodable > 0 {
			fmt.Fprintf(w, "%s: %d documents did not decode and were stepped over\n", safetext.OneLine(r.Repo), r.Undecodable)
		}
	}
	fmt.Fprintf(w, "swept %d repositories: %d reconciled, %d stale\n",
		len(res.Repos), len(res.Repos)-res.StaleRepos(), res.StaleRepos())
	if res.Stopped != "" {
		fmt.Fprintf(w, "the tick stopped early on %s; the next one starts with %s\n", res.Stopped, safetext.OneLine(res.Next))
	}
	return nil
}

// coordinatorDefaultSince is status's default window, and it is runs ls's:
// the two read the same objects over the same span, and a person comparing
// them must not have to remember that one of them looked further back.
var coordinatorDefaultSince = fcli.LsDefaultSince()

// coordinatorSince is --since's span as the instant the runs bucket is read
// from: zero for no bound (parseSince's own answer), otherwise that far back
// from lsNow — runs ls's clock, so a test that pins the window pins this
// command's too and the two view the same runs.
func coordinatorSince(window time.Duration) time.Time {
	if window <= 0 {
		return time.Time{}
	}
	return fcli.LsNow().Add(-window)
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
	cloud     fcli.CloudFlags
	repo      string
	all       bool
	since     string
	asJSON    bool
	fromStore bool
}

// NewCoordinatorCmd is the coordinator's command tree: report, sweep and
// status. Exported because ee/register is what adds it to the released tree
// (design docs/design/ee-boundary-moves.md §5.1).
func NewCoordinatorCmd() *cobra.Command {
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
	cmd.AddCommand(newCoordinatorSweepCmd())
	cmd.AddCommand(newCoordinatorShowCmd())
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
			"environment holds and carrying a named read-only permission set — pull\n" +
			"requests and metadata, the two reads this view makes (Q3 answered (A)) —\n" +
			"and cached for the token's life, so a second read of the same repository\n" +
			"mints nothing. Every token the command minted is revoked when it is done,\n" +
			"so the credential lives for the read and no longer.\n" +
			"With FUGARO_CREDENTIALS_SERVICE_URL set, the installation's credentials\n" +
			"service holds the App's key and this command holds none, so it refuses (exit 1)\n" +
			"before it reads the App's ID or key: the service mints a run's token, and this\n" +
			"read is not a run.\n" +
			"Exit codes: 1 for a missing or malformed App credential (or the refusal above),\n" +
			"2 for a failure of the host.\n" +
			"The coordinator reads github only: a bitbucket repository is refused, and the\n" +
			"refusal names the reason, because its credential is one repository's access\n" +
			"token and cannot be widened to a project (reconciliation Q12 (A), finding #30).\n\n" +
			"--from-store is a different read of the same view (D364): it prints what the\n" +
			"sweep already stored, coordinator/repos/<slug>/ in the project's database,\n" +
			"through the caller's own credentials — roles/firebasedatabase.viewer is what\n" +
			"admits it — and with the same --repo and --all selection as above. It opens no\n" +
			"host and holds no App ID, no App key and no Bitbucket credential: everything a\n" +
			"stored view says about a pull request is what the sweep read once. It is the\n" +
			"read a console makes, and it works with FUGARO_CREDENTIALS_SERVICE_URL set,\n" +
			"where the read above refuses.\n" +
			"With --json, one repository is printed as it is stored: {\"repo\":\n" +
			"\"owner/name\", \"stored\": true, \"view\": {the view document}, \"stale\": {the\n" +
			"sweep's marker, as stored}}. A repository the coordinator has never\n" +
			"reconciled is \"stored\": false, and the document then holds the runs half of\n" +
			"the view — the runs bucket alone, with the pull requests those runs name\n" +
			"listed and no host state for them — so a console reads a project whose\n" +
			"coordinator is off with the same argv. --since bounds that half, and a\n" +
			"stored view is printed as the sweep wrote it. What decides between the two\n" +
			"is the database and never the local config: a teammate's config is the\n" +
			"published shared one, and it does not carry the coordinator's switch at all.\n" +
			"The stale marker is printed exactly as stored, with no age computed from it:\n" +
			"the stored view carries no time by design (internal/coordinator/sweep.go).\n\n" +
			"The launcher of a run is printed as what it is — the string the run's own launch\n" +
			"record wrote about itself — and never as an identity.\n\n" +
			"This command writes nothing of its own: no service, no model credential, and no\n" +
			"store it maintains. Without --from-store it reads nothing of the coordinator's\n" +
			"either — it is the read the CLI already performs, the runs bucket and a pull\n" +
			"request read that can only read — while --from-store reads the view the sweep\n" +
			"stored and nothing else.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.fromStore {
				return runCoordinatorStatusFromStore(cmd, &o)
			}
			return runCoordinatorStatus(cmd, &o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository to read (owner/name; default: the local config's, when it lists one)")
	f.BoolVar(&o.all, "all", false, "read every repository the local config lists, one view each")
	f.StringVar(&o.since, "since", coordinatorDefaultSince, "only runs started within this long ("+coordinatorDefaultSince+", 36h, 90m; 0 for all)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	f.BoolVar(&o.fromStore, "from-store", false, "print the view the sweep stored, with no host read and no key (falls back to the runs alone when none is stored)")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorStatus(cmd *cobra.Command, o *coordinatorOptions) error {
	ctx := cmd.Context()
	window, err := fcli.ParseSince(o.since)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	warn := cmd.ErrOrStderr()
	repos, err := coordinatorRepos(env, o)
	if err != nil {
		return err
	}
	reader, tokens, err := coordinatorReader(warn)
	if err != nil {
		return err
	}
	// Every token this read minted is taken back when the command is done, on
	// every return and whatever happened on the way: the credential lives for
	// the read and no longer (github.RevokeInstallationToken). A command that
	// failed on its second repository has still left the first one's token
	// behind, and that is the one nothing else would ever take back.
	defer tokens.revoke(ctx)

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
func coordinatorRepos(env *fcli.Cloud, o *coordinatorOptions) ([]string, error) {
	repos := slices.Sorted(maps.Keys(env.Config().Repos))
	switch {
	case o.all && o.repo != "":
		return nil, fcli.UserErr("--all reads every repository the project lists and --repo names one; give one of the two")
	case o.all:
		if len(repos) == 0 {
			return nil, fcli.UserErr("the local config lists no repository: add one to its repos")
		}
		return repos, nil
	case o.repo != "":
		return []string{o.repo}, nil
	}
	switch len(repos) {
	case 0:
		return nil, fcli.UserErr("the local config lists no repository: add one to its repos, or name one with --repo")
	case 1:
		return repos, nil
	}
	return nil, fcli.UserErr("the local config lists %d repositories; name one with --repo, or read every one with --all", len(repos))
}

// coordinatorViewOf reads one repository's view: its runs out of the bucket,
// the pull requests those runs name out of the host through the project's
// reader, and the join between them.
//
// Every repository is read, including one whose runs name no pull request: the
// reader's own refusal of a project it does not serve is about what the project
// holds and not about whether it has work in flight, so a bitbucket repository
// is refused the same way whether or not it has runs.
func coordinatorViewOf(ctx context.Context, env *fcli.Cloud, reader coordinator.Reader, repo string, since time.Time, warn io.Writer) (coordinator.View, error) {
	co := fcli.CheckoutOf(ctx, repo)()
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return coordinator.View{}, err
	}
	runs, err := coordinatorRuns(ctx, env, slug, since, warn)
	if err != nil {
		return coordinator.View{}, err
	}
	prs, err := reader.PullRequests(ctx, env.RepoProvider(repo, co), repo, coordinator.Numbers(runs))
	if err != nil {
		// The refusal of a project this read does not serve is the
		// configuration's (reconciliation Q12 (A), finding #30). Everything else
		// is the host's — unless the reader kept the class of what failed, which
		// it does for the mint's own refusal of a missing App credential
		// (coordinator's redactedError): fcli.RemoteErr() marks a failure exit 2 only
		// when it does not already carry a code, so a user error the reader
		// rewrote the text of is still a user error here.
		if errors.Is(err, coordinator.ErrBitbucket) {
			return coordinator.View{}, fcli.UserErr("%v", err)
		}
		return coordinator.View{}, fcli.RemoteErr(err)
	}
	return coordinator.Join(repo, prs, runs), nil
}

// coordinatorRuns reads the runs the view holds: every run of slug minted
// within the window, newest first (runstore.ListRunIDs), read the way the
// other read commands read them (readRun) and reduced to what the view needs.
//
// A run whose object is unreadable is still a run: readRun's warning goes to
// the command's stderr, and what could be read is what the view shows, so an
// unreadable result.json does not make a pull request disappear.
func coordinatorRuns(ctx context.Context, env *fcli.Cloud, slug string, since time.Time, warn io.Writer) ([]coordinator.Run, error) {
	ids, err := runstore.ListRunIDs(ctx, env.RunsBucket().Bucket, slug, since)
	if err != nil {
		return nil, fcli.RemoteErr(err)
	}
	objs := make([]*fcli.RunObjects, len(ids))
	errs := make([]error, len(ids))
	fcli.Parallel(len(ids), func(i int) {
		objs[i], errs[i] = env.ReadRun(ctx, slug, ids[i])
	})
	if err := errors.Join(errs...); err != nil {
		return nil, fcli.RemoteErr(err)
	}
	out := make([]coordinator.Run, 0, len(objs))
	for _, o := range objs {
		if o.Warning() != "" {
			fmt.Fprint(warn, safetext.MultiLine(o.Warning()))
		}
		out = append(out, coordinatorRun(o.Input()))
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
// and one place to change, and so that a test replaces exactly the three things
// that would reach GitHub — the opener, the mint and the revoke — and leaves
// the cache, the refusals and the redaction under test.
//
// agent.Redact is the redactor: the same one the runner hands the agent, so a
// credential cannot reach a failure by a path this command forgot about.
//
// The second return is the record of what this read minted, which the caller
// revokes when the command is done (coordinatorTokens). It is returned rather
// than deferred here because "the command is done" is the caller's fact and not
// the reader's: the reader has no end of its own.
func coordinatorReader(warn io.Writer) (coordinator.Reader, *coordinatorTokens, error) {
	open, err := openCoordinatorProvider(os.Environ(), func(s string) { fmt.Fprintln(warn, safetext.MultiLine(s)) })
	if err != nil {
		return nil, nil, fcli.UserErr("%v", err)
	}
	// The mint runs on the first call a repository's provider makes, and that
	// call happens only for a repository whose runs name a pull request: this
	// read needs no App credential to look at a project with nothing in it, and
	// nothing here reads one to find that out.
	tokens := &coordinatorTokens{}
	return &coordinator.Project{Mint: tokens.mint, Open: open, Redact: agent.Redact}, tokens, nil
}

// coordinatorTokens remembers the installation tokens one status run minted, so
// that the run can take them back when it is done. GitHub revokes an
// installation token at DELETE /installation/token, and this command's whole
// posture is that it holds a credential for as long as it reads and no longer:
// the tokens are minted per repository, cached for the read, and handed back at
// the end.
//
// They are kept in MINT ORDER, which is also the order they are revoked in. A
// set would take them back in whatever order Go walked it, so the same command
// over the same project would send a different sequence every run — and the
// fixture a test replays is strictly ordered, which is how that would be found
// the expensive way.
type coordinatorTokens struct {
	mu     sync.Mutex
	tokens []string
}

// mint is the TokenSource the reader is given: the command's own mint, with the
// token kept for the revoke at the end.
func (t *coordinatorTokens) mint(ctx context.Context, repo string) (string, time.Time, error) {
	token, expires, err := coordinatorMint(ctx, repo)
	if err != nil {
		return "", time.Time{}, err
	}
	t.mu.Lock()
	t.tokens = append(t.tokens, token)
	t.mu.Unlock()
	return token, expires, nil
}

// coordinatorRevokeTimeout bounds the revoke as a whole. It is the bound the
// launch's own report at the end of a run takes (internal/cli/run.go): a few
// short calls to one host, which answers in milliseconds when it answers at
// all, and a host that answers none of them must not hold the process after the
// command has finished its work.
const coordinatorRevokeTimeout = 30 * time.Second

// revoke takes back every token this run minted, and returns nothing: it is
// best-effort by contract. A revoke that failed leaves a token to expire on its
// own within the hour, and the read that used it has already succeeded or
// already failed for a reason of its own — so there is no failure to report, no
// exit code to change, and nothing to print. The token reaches neither an error
// nor a log line here, which is the rule this whole path keeps.
//
// The context is the command's, detached and bounded. Detached, because the one
// moment a credential is most likely to be left behind is the moment the person
// interrupts the read: they have still been handed a token per repository, and
// those are the ones nobody would ever take back. Bounded, because the command
// is over and nothing here may hold it.
//
// The mutex is held for the copy and not for the calls: revoking is a network
// round trip per token, and the only other holder is a mint that has already
// returned its answer.
//
// A nil record is a read that minted through no record of this command's (the
// sweep's injected reader), so there is nothing of its to take back.
func (t *coordinatorTokens) revoke(ctx context.Context) {
	if t == nil {
		return
	}
	t.mu.Lock()
	tokens := slices.Clone(t.tokens)
	t.mu.Unlock()
	if len(tokens) == 0 {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinatorRevokeTimeout)
	defer cancel()
	for _, token := range tokens {
		if rctx.Err() != nil {
			// Out of budget: what is left expires on its own within the hour,
			// which is where a revoke that failed already leaves it.
			return
		}
		_ = coordinatorRevoke(rctx, token)
	}
}

// coordinatorGitHubAPIURL is the one GitHub API root every call this command
// makes with the App's credential goes to: the JWT the mint signs, the
// installation tokens minted with it, the read those authenticate and the
// revoke that takes them back. It is pinned, not read from the environment
// (D474): FUGARO_GITHUB_API_URL is the run's own redirect, and a variable
// anything that launches this job can set must not decide where the App's key
// is spent — a root of a launcher's choosing would receive the App credential
// itself. Tests point it at a hermetic host; production code has no way to
// change it.
var coordinatorGitHubAPIURL = github.DefaultBaseURL

// coordinatorProviderEnv is the environment a provider is opened with: the one
// this command was handed, with the GitHub API root replaced by the pinned one,
// so the redirect the process may carry for the run's own sake cannot decide
// where the App credential's calls go (coordinatorGitHubAPIURL).
func coordinatorProviderEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, providers.EnvGitHubAPIURL+"=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, providers.EnvGitHubAPIURL+"="+coordinatorGitHubAPIURL)
}

// coordinatorRevoke revokes one installation token this command minted. It is a
// package variable so that a test can watch it — or refuse it — without a
// network, and its own call reads the same pinned API root the mint does, so
// the two cannot be pointed at different hosts.
var coordinatorRevoke = func(ctx context.Context, token string) error {
	return github.RevokeInstallationToken(ctx, coordinatorGitHubAPIURL, token, nil)
}

// coordinatorAppCredential is the App credential the coordinator's mint needs:
// the App's ID, and its private key read the way the opener reads it
// (providers.AppKey, so the mint and the opener cannot disagree about where the
// key is) and parsed into the key the mint signs with. Each failure names what
// is missing or malformed, and every one of them is the user's own rather than
// the host's.
//
// The parse is here, and not left to the caller that mints, because this
// function is what BOTH halves of the command ask for the credential: a key
// that is there and is not a key has to be the caller's failure from either,
// and a parse left to providers.FromEnv would answer the same environment
// twice — exit 1 through the mint, which parses the key itself, and exit 2
// through the opener, whose plain error travels as a failure through the host.
// A person holding a key that is not a key would be told GitHub failed and
// would retry it.
func coordinatorAppCredential() (appID string, key *rsa.PrivateKey, pemData string, err error) {
	// With the installation's credentials service on, the App's key is the
	// service's (C2.2, D145) and this process holds none: reading one here would
	// be reaching for a key the platform meant to withhold, which is the rule
	// providers.FromEnv keeps for the run path and the one the coordinator's own
	// mint has to keep too. The service mints a RUN's token and this read is not
	// a run, so there is nothing to ask it for either: the command is refused
	// rather than served the wrong credential. A key that is somehow in the
	// environment anyway changes nothing, exactly as it changes nothing there.
	if os.Getenv(providers.EnvCredentialsServiceURL) != "" {
		return "", nil, "", fcli.UserErr("%s is set, so the installation's credentials service holds the GitHub App's key and this command holds none: the project-wide read mints its own read-only token from the App's key, and the service mints a run's. Run the read where the key is", providers.EnvCredentialsServiceURL)
	}
	appID = os.Getenv(providers.EnvGitHubAppID)
	if appID == "" {
		return "", nil, "", fcli.UserErr("%s is not set: the project-wide read mints the coordinator's own token and needs the GitHub App's ID (see docs/git-providers.md)", providers.EnvGitHubAppID)
	}
	pemData, err = providers.AppKey(os.Environ())
	if err != nil {
		return "", nil, "", fcli.UserErr("%v", err)
	}
	key, err = github.ParsePrivateKey([]byte(pemData))
	if err != nil {
		return "", nil, "", fcli.UserErr("%v", err)
	}
	return appID, key, pemData, nil
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
// App's ID, or whose key is not a key, would be told the host failed and would
// retry it, when the refusal is theirs to fix and the same condition is already
// a user error where the mint asks for the credential
// (coordinatorAppCredential, exit 1). Asking here is also what makes that
// wording reachable: the opener refuses before the mint is reached, so the
// mint's own message could never be the one a person saw.
// It costs a repository whose runs name a pull request the same credential read
// a second time, and nothing at all for one without: Project.PullRequests
// returns before opening when the runs name no pull request, so a project with
// nothing in it is still read with no App credential anywhere.
var openCoordinatorProvider = func(env []string, warn func(string)) (coordinator.ProviderOpener, error) {
	_, open, err := fcli.OpenProvider(coordinatorProviderEnv(env), warn)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, repo string, src gitprov.TokenSource) (gitprov.Provider, []string, error) {
		if _, _, _, err := coordinatorAppCredential(); err != nil {
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
// repository: github.CoordinatorTokenPermissions — pull requests and metadata,
// the two reads the project-wide reader makes — through
// github.MintInstallationToken, with the App credential the environment
// carries. One repository at a time (D193(A)): the token is scoped to the
// repository it was minted for, and a second repository mints its own.
//
// The credential — the App's ID and its key — is read through
// coordinatorAppCredential, which reads and parses it the same way the opener
// does, so where the key comes from and what counts as a key are one rule. A
// failure this returns carries no key: what it reports is redacted with the PEM
// it read (agent.Redact), because the mint's own error text can quote what it
// was handed. Tests replace it, so a read never reaches GitHub.
var coordinatorMint = func(ctx context.Context, repo string) (string, time.Time, error) {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return "", time.Time{}, fcli.UserErr("repository %q must look like owner/name", repo)
	}
	appID, key, pemData, err := coordinatorAppCredential()
	if err != nil {
		return "", time.Time{}, err
	}
	token, expires, err := github.MintInstallationToken(ctx, github.Options{
		Owner:      owner,
		Repo:       name,
		AppID:      appID,
		PrivateKey: key,
		BaseURL:    coordinatorGitHubAPIURL,
	}, github.CoordinatorTokenPermissions())
	if err != nil {
		return "", time.Time{}, fcli.RemoteErr(errors.New(agent.Redact(err.Error(), []string{pemData})))
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
	fmt.Fprintf(w, "%s: %d records, %d in flight\n", safetext.TableCell(v.Repo), len(v.Records), v.InFlight())
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
			safetext.TableCell(r.Label()),
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
			fmt.Fprintf(w, "%s and %s: %s\n", safetext.TableCell(c.A), safetext.TableCell(c.B),
				safetext.TableCell(fcli.ClipRunes(strings.Join(what, "; "), coordinatorMaxCellRunes*2)))
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
	return safetext.TableCell(fcli.ClipRunes(s, coordinatorMaxCellRunes))
}
