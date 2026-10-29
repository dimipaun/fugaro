package cli

import (
	"context"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runview"
)

// This file is the one exported surface the core offers the commands the owner
// moves out of it (design ee-boundary-moves.md §10). A moved command lives in
// another package, so it cannot reach cloudEnv, cloudOptions or the helpers
// beside them; it opens a cloud through OpenCloud and reads what it needs from
// the opaque *Cloud handle. The list is closed: a command that needs something
// outside it adds the helper HERE, where it is readable, rather than reaching
// into the core from across the boundary. Every function below is a thin
// wrapper over the core's own implementation, so the two cannot drift.

// CloudFlags are the flags every command that talks to the cloud shares,
// opaque so that a moved command cannot read the project selection or the
// header behind them.
type CloudFlags struct{ o cloudOptions }

// AddCloudFlags registers --config, --project, --gcp-project and --region on
// cmd.
func AddCloudFlags(cmd *cobra.Command, f *CloudFlags) {
	addCloudFlags(cmd, &f.o)
}

// Cloud is an opened cloud environment: the resolved project config, the runs
// bucket and the compute backend, opaque so that a moved command cannot reach
// the endpoints or the opener behind them.
type Cloud struct{ env *cloudEnv }

// OpenCloud selects the project config and connects to the backend and the
// runs bucket.
func OpenCloud(ctx context.Context, f *CloudFlags) (*Cloud, error) {
	env, err := openCloud(ctx, f.o)
	if err != nil {
		return nil, err
	}
	return &Cloud{env: env}, nil
}

// OpenFromConfig connects to the backend and the runs bucket of an already
// selected project config.
func OpenFromConfig(ctx context.Context, lc *localcfg.Config) (*Cloud, error) {
	env, err := openCloudFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	return &Cloud{env: env}, nil
}

// Config is the project config this cloud was opened with.
func (c *Cloud) Config() *localcfg.Config { return c.env.lc }

// CloudOverBucket is a cloud over a runs bucket alone, with no project config
// and no backend: it is what the coordinator's sweep job needs, since that job
// runs with no config file at all — Terraform gives it FUGARO_* environment
// variables and it opens the runs bucket itself. Config answers nil on it, and
// RunsBucket, ReadRun, RepoProvider and Close are the only methods that mean
// anything: everything else on this type reads the project config the sweep
// does not have.
func CloudOverBucket(b *blobx.Bucket) *Cloud { return &Cloud{env: &cloudEnv{bucket: b}} }

// NoBudgetCheck is --no-budget-check: the launch's own switch for skipping the
// budget pre-check.
func (c *Cloud) NoBudgetCheck() bool { return c.env.noBudgetCheck }

// RepoSlug is repo's slug, as run and ls take it.
func (c *Cloud) RepoSlug(ctx context.Context, repo string) (string, error) {
	return repoSlugOf(ctx, c.env.lc, repo)
}

// BudgetDB is the project's budget database, checked to be the database of the
// project the cloud was opened with: a config whose budget.rtdb_url points at
// another project's database is refused, exactly as it is on the paths that
// open one in the core.
func (c *Cloud) BudgetDB(ctx context.Context) (*rtdb.Client, error) {
	lc, db, err := budgetDBFor(ctx, c.env.lc)
	if err != nil {
		return nil, err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return nil, err
	}
	return db, nil
}

// DBErr classifies an error of a database call: a refusal is a user error
// (exit 1), everything else, an outage included, a remote one (exit 2).
func (c *Cloud) DBErr(err error, write bool) error { return dbErr(c.env.lc, err, write) }

// Firestore is the client of the project's spend history. A project with no
// Firebase backend is refused here rather than dereferenced: the core's own
// reader checks the same two fields before it reaches for one, and a project
// that never ran `fugaro update --firebase-project` has no history to read.
func (c *Cloud) Firestore(ctx context.Context) (*firestore.Client, error) {
	lc := c.env.lc
	if lc.Budget == nil || lc.Budget.FirebaseProject == "" {
		return nil, userErr("project %s has no Firebase budget backend (budget.firebase_project is not set in its project config), so it has no spend history: an operator runs fugaro update --firebase-project %s --name %s", lc.Name, lc.GCPProject, lc.Name)
	}
	return newReportFirestore(ctx, lc)
}

// FirestoreErr classifies a Firestore read failure: a refusal names the role.
func (c *Cloud) FirestoreErr(err error) error { return firestoreErr(c.env.lc, err) }

// UserErr is a user error: ExitCode maps it to exit 1.
func UserErr(format string, args ...any) error { return userErr(format, args...) }

// RemoteErr marks err as a remote failure: ExitCode maps it to exit 2.
func RemoteErr(err error) error { return remote(err) }

// ClipRunes is s cut to n runes.
func ClipRunes(s string, n int) string { return clipRunes(s, n) }

// PaidCommandNames are the command names whose code the owner moves out of the
// core. A reader, not a seam: the commands a build holds are its own business,
// so there is no setter and a caller cannot change the list the stubs are built
// from (design docs/design/ee-boundary-moves.md §9, D536).
//
// The core's skill checks read it to tell a name the released binary answers to
// — the core command today, a registration after the move, the stub in a build
// carrying none of that code — from a name no build has, and to hand a paid
// name's flags to the tagged tree, where the command that carries them is
// (ee/plugincheck). It answers a copy, so a caller that appends to it changes
// nothing.
func PaidCommandNames() []string { return slices.Clone(paidCommandNames) }

// RunsBucket is the bucket the project's runs are stored in.
func (c *Cloud) RunsBucket() *blobx.Bucket { return c.env.bucket }

// RepoProvider is repo's git provider kind, as Cloud.RepoSlug resolves it: the
// local config's entry first, then the checkout's fugaro.yaml.
func (c *Cloud) RepoProvider(repo string, co *config.Config) string {
	return c.env.repoProvider(repo, co)
}

// Close releases the cloud's bucket and its build-record bucket.
func (c *Cloud) Close() { c.env.Close() }

// RunObjects is what the runs bucket holds about one run, before its execution
// is joined, opaque so that a moved command reads what the coordinator's sweep
// reads and nothing else.
type RunObjects struct{ o runObjects }

// ReadRun reads what the bucket knows about one run; it makes no backend call.
func (c *Cloud) ReadRun(ctx context.Context, slug, id string) (*RunObjects, error) {
	o, err := readRun(ctx, c.env, slug, id)
	if err != nil {
		return nil, err
	}
	return &RunObjects{o: o}, nil
}

// Input is the run's own objects, as the other views of one run read them.
func (r *RunObjects) Input() runview.Input { return r.o.in }

// Warning is what the read could not make sense of, for the command's stderr.
func (r *RunObjects) Warning() string { return r.o.warning }

// CheckoutOf is a memoized lookup of the working directory's fugaro.yaml, for
// repo only: what the commands that name a repository read it through.
func CheckoutOf(ctx context.Context, repo string) func() *config.Config {
	return checkoutOf(ctx, repo)
}

// ParseSince parses a --since value: a positive number of days (7d), a
// positive Go duration (36h, 90m), or 0 for no bound.
func ParseSince(s string) (time.Duration, error) { return parseSince(s) }

// LsDefaultSince is the default window of a listing, which the coordinator's
// own default --since is.
func LsDefaultSince() string { return lsDefaultSince }

// LsNow is the clock a --since window is measured back from: a fixed one is
// how the listing's own tests pin that window, and the coordinator's do too.
func LsNow() time.Time { return lsNow() }

// Parallel calls fn(0) … fn(n-1) on up to the listing's own bound of
// goroutines.
func Parallel(n int, fn func(i int)) { parallel(n, fn) }

// OpenProvider turns the process environment into the project's code-host
// settings, the way a launch with no --provider override does: the kind to
// open and how to open it, with warn receiving the adapters' warnings.
func OpenProvider(env []string, warn func(string)) (string, gitprov.Opener, error) {
	return providerOptions(execOptions{}, env, warn)
}

// NewBudgetClient is the client of the project's database at url, acting as
// the person (ADC with the budget scopes); noAuth (fakes only) sends no
// credentials. The URL is validated first.
func NewBudgetClient(ctx context.Context, url string, noAuth bool) (*rtdb.Client, error) {
	return newBudgetClient(ctx, url, noAuth)
}

// BudgetScopes are the OAuth scopes a database token is minted with. It
// answers a copy, so a caller cannot change what the core mints for the next
// one.
func BudgetScopes() []string { return slices.Clone(budgetScopes) }

// CheckDatabaseHost refuses a database URL that is not the named Firebase
// project's own: the sweep writes only to this Fugaro project's database.
func CheckDatabaseHost(rawURL, firebaseProject string) error {
	return checkDatabaseHost(rawURL, firebaseProject)
}

const (
	// EnvProject is the project's name, which the sweep job reads out of the
	// environment Terraform sets on it.
	EnvProject = envProject
	// EnvFirebaseFP is the Firebase project the database has to belong to.
	EnvFirebaseFP = envFirebaseFP
	// EnvRTDBURL is the database to sweep.
	EnvRTDBURL = envRTDBURL
	// EnvRunsBucket is the bucket the runs are read from.
	EnvRunsBucket = envRunsBucket
)

// ReportIntentTimeout bounds one launch's declaration, as the core's own
// declarer bounds it: the declaration is advisory, and a database that is slow
// or unreachable must cost the launch a few seconds rather than the hold it is
// working inside.
const ReportIntentTimeout = reportIntentTimeout
