package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/batch"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// cloudOptions are the flags every command that talks to the cloud shares.
type cloudOptions struct {
	// config is a project config file; project a project's name;
	// gcpProject a GCP project ID, which must be the selected config's.
	config, project, gcpProject, region string
	// stderr is the command's stderr, for notes; nil is os.Stderr.
	stderr func() io.Writer
	// sharedOK lets a missing local config fall back to the shared config
	// published to the runs bucket. Only selectProject sets it.
	sharedOK bool
	// originDir is the checkout whose origin may select a project config
	// ("" is the working directory's): a command about a checkout other than
	// the working directory's sets it.
	originDir string
}

// addCloudFlags registers --config, --project (a Fugaro project name),
// --gcp-project and --region on cmd.
func addCloudFlags(cmd *cobra.Command, o *cloudOptions) {
	f := cmd.Flags()
	f.StringVar(&o.config, "config", "", "the project config file to use (default: the project's $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml)")
	f.StringVar(&o.project, "project", "", "the Fugaro project to act on (default: the checkout's fugaro.yaml project:, else $FUGARO_PROJECT, else the only project config)")
	f.StringVar(&o.gcpProject, "gcp-project", "", "the GCP project ID; it must be the selected project config's gcp_project")
	f.StringVar(&o.region, "region", "", "GCP region (overrides the project config's)")
	o.stderr = cmd.ErrOrStderr
}

// cloudEnv is what a cloud command works against: the local config, the
// runs bucket and the compute backend.
type cloudEnv struct {
	lc     *localcfg.Config
	bucket *blobx.Bucket
	be     backend.Backend
	gcp    gcp.Options
	// records is the bucket build records are read from, once opened,
	// when it isn't bucket (see recordBucket).
	records *blobx.Bucket
	// noBudgetCheck is --no-budget-check: fugaro run skips the launch
	// pre-check of the budget (nothing else).
	noBudgetCheck bool
	// policy is the project's published policy, read at most once per run
	// through the operator's own credentials (policyReadOnce.read): the launch
	// gate reads it before anything of the launch exists, and the mint
	// decision reads it again on the path where the local budget mode is off.
	// One launch must not be admitted against two different policies.
	policy policyReadOnce
}

// openRecordBucket opens the bucket build records are read from. Tests
// replace it.
var openRecordBucket = blobx.Open

// recordBucket is the bucket ls, image status and the local image check
// read build records from (recordReadURL): the runs bucket, opened on
// first use when a bucket_url names another.
func (e *cloudEnv) recordBucket(ctx context.Context) (*blobx.Bucket, error) {
	u := recordReadURL(e.lc)
	if u == "" || u == e.lc.BucketURL() {
		return e.bucket, nil
	}
	if e.records == nil {
		b, err := openRecordBucket(ctx, u)
		if err != nil {
			return nil, err
		}
		e.records = b
	}
	return e.records, nil
}

// recordReadURL is where build records are read: where builds write them,
// the runs bucket (lc.RecordBucketURL). A bucket_url off GCS (file://,
// mem://) is a local stand-in for the runs bucket, which no build can
// write, and is read in its place.
func recordReadURL(lc *localcfg.Config) string {
	if lc.Bucket != "" {
		if u, err := url.Parse(lc.Bucket); err != nil || u.Scheme != "gs" {
			return lc.Bucket
		}
	}
	return lc.RecordBucketURL()
}

// prices are the compute prices one execution is priced with, for cost
// estimates of cloud runs (design §10.1, compute-backend.md §5.2): the local
// config's override, else the list price of the execution's own backend and
// region. The execution tells us both — its Region is the backend's own
// answer, not a parse of a name shape this package would have to know, and
// its MachineType is what makes a run a VM-backed one, whose table is
// Compute Engine's and not Cloud Run's.
//
// An execution no table prices — a machine type or a region the VM table
// does not carry — gets the zero price book, so its row reads "compute not
// estimated" rather than being priced with another platform's figures or
// with $0.00. A nil execution (a run with no execution at all) prices in the
// installation's own region.
//
// warn is how a refusal is said out loud, once per distinct reason: a listing
// whose rows all read "compute not estimated" would otherwise leave the
// operator with nothing to read, which is the quiet failure §5.2 warns about.
// The rows are still listed — a price table's gap is not a reason to fail a
// listing — and nil warn says nothing.
func (e *cloudEnv) prices(warn func(string)) runview.PriceBook {
	var (
		mu   sync.Mutex
		said = map[string]bool{}
	)
	refused := func(err error) {
		if warn == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		msg := err.Error()
		if said[msg] {
			return
		}
		said[msg] = true
		warn("warning: compute is not estimated: " + oneLine(msg) + "\n")
	}
	return func(x *backend.Execution) backend.Prices {
		region := e.lc.Region
		if x != nil && x.Region != "" {
			region = x.Region
		}
		vm := x != nil && x.MachineType != ""
		if p, ok := e.lc.PriceOverride(region); ok {
			if !vm {
				return p
			}
			// The override's rates win, but a VM run is billed for its
			// machine type's quantities and its line keeps the disk/NAT note
			// (batch.ForMachineType, design §5.2, C17).
			p, err := batch.ForMachineType(p, x.MachineType)
			if err != nil {
				refused(err)
				return backend.Prices{}
			}
			return p
		}
		if vm {
			p, err := batch.PricesFor(region, x.MachineType)
			if err != nil {
				refused(err)
				return backend.Prices{}
			}
			return p
		}
		return gcp.ListPrices(region)
	}
}

// Close releases the bucket.
func (e *cloudEnv) Close() {
	if e.bucket != nil {
		_ = e.bucket.Close()
	}
	if e.records != nil {
		_ = e.records.Close()
	}
}

// userErr is a user error (exit 1).
func userErr(format string, args ...any) error {
	return &ExitError{Code: ExitUserError, Err: fmt.Errorf(format, args...)}
}

// remote marks err as a remote failure (exit 2), unless it already carries a code.
func remote(err error) error {
	var ee *ExitError
	if err == nil || errors.As(err, &ee) {
		return err
	}
	return &ExitError{Code: ExitRemoteError, Err: err}
}

// openCloud selects the project config (selectProject: the header goes to
// stderr, --gcp-project and --region are applied) and connects to the
// backend and the runs bucket.
func openCloud(ctx context.Context, o cloudOptions) (*cloudEnv, error) {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return nil, err
	}
	return openCloudFor(ctx, lc)
}

// openCloudFor connects to the backend and the runs bucket of an already
// selected project config.
func openCloudFor(ctx context.Context, lc *localcfg.Config) (*cloudEnv, error) {
	opts := gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager,
		CloudBuild: lc.Endpoints.CloudBuild, NoAuth: lc.Endpoints.NoAuth}}
	be, err := backend.Open(ctx, lc.Backend(), cloudOpeners(lc, opts))
	if err != nil {
		return nil, remote(err)
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return nil, remote(err)
	}
	if err := checkCloudName(ctx, b, lc, os.Getenv, time.Now()); err != nil {
		_ = b.Close()
		return nil, err
	}
	return &cloudEnv{lc: lc, bucket: b, be: be, gcp: opts}, nil
}

// cloudOpeners is the opener map backend.Open picks from: one entry per
// backend name this build can open, each constructing its backend from the
// same project config. backend.Open's contract is kept on both sides:
// lc.Backend() is backend.CloudRun when the config names none, and a name with
// no entry here fails closed, naming the entries there are, rather than
// falling back to Cloud Run.
//
// The Batch entry is registered and never selected: internal/localcfg refuses
// `backend: cloud-batch` until the task that turns it on (design
// compute-backend.md §8.1), so no installation reaches it. It is wired here
// and not in a test because the seam's second implementation has to be opened
// from an installation's own facts -- its run account, its network, its
// recorded socket gid -- and this is the only place that holds them.
func cloudOpeners(lc *localcfg.Config, runOpts gcp.Options) map[string]backend.Opener {
	return map[string]backend.Opener{
		backend.CloudRun: func(ctx context.Context) (backend.Backend, error) { return gcp.New(ctx, runOpts) },
		backend.CloudBatch: func(ctx context.Context) (backend.Backend, error) {
			return batch.New(ctx, batchOptions(lc))
		},
	}
}

// batchOptions is the Cloud Batch backend's Options, from the project config:
// the installation's own identity, and the batch block (localcfg.Batch) for
// everything a run's VM and job are built and placed from.
//
// A field left out of the block stays empty here and is then the backend's own
// default or its refusal -- the machine type, the boot disk size and the socket
// path have defaults; the image, the run account, the network and the
// subnetwork are refused by batch.New, which is what a block that is missing one
// of them should do (an installation is provisioned by Task 9, not guessed).
func batchOptions(lc *localcfg.Config) batch.Options {
	o := batch.Options{
		GCPProject:    lc.GCPProject,
		Region:        lc.Region,
		FugaroProject: lc.Name,
		Timeouts:      batchTimeouts{lc: lc},
		Endpoints: batch.Endpoints{
			Batch: lc.Endpoints.Batch, Logging: lc.Endpoints.Logging, NoAuth: lc.Endpoints.NoAuth,
		},
		LogView: lc.LogView,
	}
	if b := lc.Batch; b != nil {
		o.Image, o.MachineType, o.BootImage = b.Image, b.MachineType, b.BootImage
		o.Network, o.Subnetwork, o.ServiceAccount = b.Network, b.Subnetwork, b.ServiceAccount
		o.Secrets = b.Secrets
		o.Docker = batch.Docker{SocketGID: b.SocketGID}
	}
	return o
}

// batchTimeouts is the Batch backend's Timeouts, read from the project config.
// On Cloud Run the workflow timeouts are read back from the deployed job; on
// Batch there is no deployed job to read (a job is created per run and is
// single-use), so they are a configuration read (design §2.3).
//
// What this holds is the installation's own bound, localcfg.BatchTaskTimeout --
// backend.MaxRunTime unless the operator wrote batch.task_timeout -- and it is
// deliberately one value for every workflow rather than a guess per workflow:
// the failure directions are not symmetric, and a bound shorter than a
// workflow's would truncate a run that was doing nothing wrong. A launch that
// carries its own total (--total-timeout, LaunchSpec.Timeout) is unaffected
// either way: the backend uses that one first, and this is only what a launch
// that names none falls back to. The workflow's own timeouts.total, read per
// workflow from the project layer, is what replaces this when the enable task
// lands.
type batchTimeouts struct{ lc *localcfg.Config }

// TaskTimeout is the installation's task timeout. A workflow it cannot answer
// for (one this installation's config does not carry) is answered for with the
// same bound rather than with ErrNotFound: the bound is the installation's, and
// refusing a workflow whose own timeout is unknown would keep a run from
// starting over a question the installation has already answered
// conservatively.
func (t batchTimeouts) TaskTimeout(slug, workflow string) (time.Duration, error) {
	return t.lc.BatchTaskTimeout(), nil
}

// LongestTaskTimeout is the same bound: every workflow gets it, so it is the
// longest one too. It bounds how far back an active-only List looks.
func (t batchTimeouts) LongestTaskTimeout() (time.Duration, error) {
	return t.lc.BatchTaskTimeout(), nil
}

// launchBackend is the backend name a launch record carries: launch.json's
// backend field and the run's FUGARO_BACKEND. It is read from the seam --
// the codec of the backend the launch actually ran on -- and never written as
// a constant, because a record that names the wrong backend is a record
// fugaro logs, fugaro cancel, fugaro diagnose and the reaper all misread
// (design §2.5). A backend with no codec (none this build opens has none)
// records "" rather than a name that would be wrong.
func launchBackend(be backend.Backend) string {
	if c := be.Codec(); c != nil {
		return c.Backend()
	}
	return ""
}

// budgetScopes are the OAuth scopes the Realtime Database REST API takes
// from a person's Application Default Credentials (plan A2).
var budgetScopes = []string{"https://www.googleapis.com/auth/firebase.database", "https://www.googleapis.com/auth/userinfo.email"}

// openBudgetDB selects the project config (the header goes to stderr, as in
// openCloud) and returns a client of the project's budget database, acting as
// the person (ADC): their IAM roles on the Firebase project decide what the
// database lets them do (viewer reads, admin writes). The project needs
// budget.rtdb_url, which fugaro update --firebase-project writes. A config that sets
// endpoints.no_auth (fakes only) sends no credentials.
func openBudgetDB(ctx context.Context, o cloudOptions) (*localcfg.Config, *rtdb.Client, error) {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, nil, err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	return budgetDBFor(ctx, lc)
}

// budgetDBFor is openBudgetDB's second half, for a config already selected.
func budgetDBFor(ctx context.Context, lc *localcfg.Config) (*localcfg.Config, *rtdb.Client, error) {
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, nil, userErr("project %s has no Firebase budget backend (budget.rtdb_url is not set in its project config): an operator runs fugaro update --firebase-project %s --name %s", lc.Name, firebaseProjectIDWord(lc.GCPProject), lc.Name)
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, nil, err
	}
	return lc, db, nil
}

// newBudgetClient is the client of the budget database at url, acting as the
// person (ADC with the budget scopes); noAuth (fakes only) sends no
// credentials. The URL is validated first.
func newBudgetClient(ctx context.Context, url string, noAuth bool) (*rtdb.Client, error) {
	if err := rtdb.ValidateURL(url, noAuth); err != nil {
		return nil, userErr("budget.rtdb_url: %v", err)
	}
	auth := rtdb.Auth{IDToken: func() string { return "" }}
	if !noAuth {
		ts, err := google.DefaultTokenSource(ctx, budgetScopes...)
		if err != nil {
			return nil, userErr("no Google credentials to reach the budget database: run gcloud auth application-default login (%v)", err)
		}
		auth = rtdb.Auth{Source: ts}
	}
	var opts []rtdb.Option
	if budgetTransport != nil {
		opts = append(opts, rtdb.WithHTTPClient(&http.Client{Transport: budgetTransport}))
	}
	db, err := rtdb.New(url, auth, opts...)
	if err != nil {
		return nil, userErr("budget.rtdb_url: %v", err)
	}
	return db, nil
}

// locateRun resolves "<slug>/<run-id>" or a bare run ID to a run in the
// bucket. A malformed reference, an unknown run or an ambiguous bare ID is
// a user error; anything else is remote.
func locateRun(ctx context.Context, env *cloudEnv, ref string) (slug, runID string, err error) {
	if strings.Contains(ref, "/") {
		if _, _, err := parseRunRef(ref); err != nil {
			return "", "", err
		}
	} else if _, err := runstore.RunTime(ref); err != nil {
		return "", "", userErr("%q is neither <repo-slug>/<run-id> nor a run ID", ref)
	}
	slug, runID, err = runstore.Locate(ctx, env.bucket.Bucket, ref)
	switch {
	case errors.Is(err, runstore.ErrNotFound), errors.Is(err, runstore.ErrAmbiguous):
		return "", "", userErr("%v", err)
	case err != nil:
		return "", "", remote(err)
	}
	return slug, runID, nil
}

// checkExecution refuses name, an execution name read from the bucket
// objects of run slug/<runID>, unless it is that run's own execution: the
// FUGARO_RUN the execution itself reports is the run's (gcp.CheckRunExecution).
//
// What it used to bind the two by was the execution's job
// (JobName(slug, WorkflowVariant(spec.Workflow, spec.OAuthToken))). Under the
// project-centric model one job is shared by every repository admitted to a
// profile (design §4.1), so the job says nothing about which run an
// execution is: two runs of one job — two --run-id repeats, or two
// repositories on one profile — would each be followable by the other's
// objects, and the run's service account can write its own (plan task 3.4
// step 2).
//
// The name is checked first, on its own, because it needs no request and
// because a name in another region must be refused with the region to pass
// (--region) rather than as a missing execution. x is the execution, when the
// caller already has it (a listing's own entry), and nil makes this read it
// back; an execution the backend does not have is not a refusal, since a run
// whose execution has been deleted (Cloud Run keeps them 30 days) can be
// neither followed nor made to act on anyone else's — cancel's own tolerance
// of a vanished execution depends on it. What comes back is that execution,
// nil when there is none, so a caller that needs it (ls) does not read it
// twice.
//
// The task.json is not read at all: the run the execution must be is the one
// the caller asked about, which the caller knows from the reference it
// resolved (and not from an object the run itself can write).
func (e *cloudEnv) checkExecution(ctx context.Context, x *backend.Execution, name, slug, runID string) (*backend.Execution, error) {
	// The name is parsed by the codec of the backend this command selected,
	// never by Cloud Run's own: the two backends spell a run's identity
	// differently (eight fields with an execution in them against six with
	// none), so a project whose backend is not Cloud Run would otherwise be
	// checked against the wrong shape (design compute-backend.md §2.5, §8.1
	// Task 8, C12). This is ParseInRegion rather than ValidateRun: the job
	// binding a name would need is the run's own, and it is checked below by
	// gcp.CheckRunExecution, which reads the FUGARO_RUN the execution itself
	// reports (internal/backend/codec.go's ValidateRun has no production
	// caller for that reason).
	if _, err := e.be.Codec().ParseInRegion(name, e.lc.Region); err != nil {
		var wr *backend.WrongRegionError
		if errors.As(err, &wr) {
			return nil, fmt.Errorf("the run's execution is in region %s, not %s; pass --region %s to follow it", wr.Region, wr.Want, wr.Region)
		}
		return nil, err
	}
	if x == nil {
		got, err := e.be.Execution(ctx, name)
		switch {
		case errors.Is(err, backend.ErrNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
		x = &got
	}
	if err := gcp.CheckRunExecution(*x, slug, runID); err != nil {
		return nil, err
	}
	return x, nil
}

// jobWorkflow is gcp.WorkflowVariant(workflow, token), refusing instead of
// panicking when workflow is not a name WorkflowVariant accepts. Unlike
// OAuthToken (config.ClaudeAccountNameRE, enforced by task.Spec.Validate on
// every task.Parse), task.Spec's Workflow field is never shape-checked:
// config.WorkflowNameRE only constrains fugaro.yaml's own workflow: keys, not
// what reaches task.Spec.Workflow, which an unvalidated --workflow flag, a
// stored task a different process wrote, or bucket data a launcher wrote
// directly, can all carry unchanged. Every caller here (a fresh launch, and
// checkExecution's correlation of ls/logs/diagnose/--retry/follow-up) must
// treat "not a workflow name" as "no job can be derived from it", never as a
// reason to crash.
func jobWorkflow(workflow, token string) (variant string, ok bool) {
	defer func() {
		if recover() != nil {
			variant, ok = "", false
		}
	}()
	return gcp.WorkflowVariant(workflow, token), true
}

// parseRunRef is runstore.ParseRef, with a bad reference as a user error.
// ParseRef takes only task.Slug's alphabet, so "." and "..", which
// path.Join would resolve outside runs/, never parse.
func parseRunRef(ref string) (slug, runID string, err error) {
	slug, runID, err = runstore.ParseRef(ref)
	if err != nil {
		return "", "", userErr("%v", err)
	}
	return slug, runID, nil
}

// localRepo is the local config's entry for repo, matched in canonical form
// (localcfg.Config.RepoFor, which `fugaro config list --json` reads its
// base_branch column through for the same rows: the branch that document shows
// has to be the branch this launch is cut from).
func (e *cloudEnv) localRepo(repo string) (localcfg.Repo, bool) {
	return e.lc.RepoFor(repo)
}

// repoSlug is repo's storage slug. Its git provider kind comes from the
// local config's entry for repo and from the checkout's fugaro.yaml (only
// when the checkout's origin is repo; see checkoutOf). Either may be
// missing, but when both are set they must agree: the slug depends on it.
// It never comes from the origin host.
func (e *cloudEnv) repoSlug(repo string, checkout func() *config.Config) (string, error) {
	local := ""
	if r, ok := e.localRepo(repo); ok {
		local = r.Provider
	}
	fromCheckout := ""
	if c := checkout(); c != nil {
		fromCheckout = c.Git.Provider
	}
	provider := local
	switch {
	case local != "" && fromCheckout != "" && local != fromCheckout:
		return "", userErr("the local config says %s is on %s, but this checkout's fugaro.yaml says %s; make them agree", repo, local, fromCheckout)
	case local == "":
		provider = fromCheckout
	}
	if provider == "" {
		return "", userErr("cannot tell the git provider of %s: set provider in its repos entry of the local config, or run from its checkout", repo)
	}
	slug, err := task.Slug(provider, repo)
	if err != nil {
		return "", userErr("%v", err)
	}
	return slug, nil
}

// originRepo is owner/name of the checkout's origin remote.
func originRepo(ctx context.Context) (string, error) {
	out, err := gitCmd(ctx, ".", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", userErr("no --repo, and no origin remote here to take it from")
	}
	origin := strings.TrimSpace(string(out))
	repo, ok := repoFromOrigin(origin)
	if !ok {
		return "", userErr("origin %s does not name owner/name; pass --repo", gcp.RedactURL(image.HTTPSOrigin(origin)))
	}
	return repo, nil
}

// repoFromOrigin is the owner/name an origin remote URL (https, ssh or
// scp-like) names: the one rule for run, ls, secrets, image build and
// init. A nested path (group/subgroup/name) is not owner/name.
func repoFromOrigin(origin string) (string, bool) {
	u, err := url.Parse(image.HTTPSOrigin(origin))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	repo := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if _, _, ok := gitprov.SplitRepo(repo); !ok {
		return "", false
	}
	return repo, true
}

// checkoutConfig is the fugaro.yaml of the checkout in the working
// directory, when that checkout's origin is repo. Anything else (no
// checkout, another repository, no or an unparseable fugaro.yaml) is nil:
// the caller then asks for the flag it was after.
func checkoutConfig(ctx context.Context, repo string) *config.Config {
	cfg, _ := checkoutParse(ctx, repo)
	return cfg
}

// refuseSharedCheckout refuses a launch from repo's checkout when its
// fugaro.yaml (or the project layer it resolves over) declares checkout:
// shared before the release that builds shared images. A launch reads that
// file leniently, so without this the refused file just reads as no file and
// the launch fails on whatever it then lacks (the git provider, the
// workflow), or starts a job the runner refuses at bootstrap.
func refuseSharedCheckout(ctx context.Context, repo string) error {
	_, problems := checkoutParse(ctx, repo)
	for _, p := range problems {
		if strings.Contains(p.Message, config.SharedCheckoutRefusal) {
			return userErr("fugaro.yaml: %s (see fugaro validate)", p)
		}
	}
	return nil
}

// checkoutRoot is the working directory's checkout root when its origin is
// repo, and "" otherwise.
func checkoutRoot(ctx context.Context, repo string) string {
	origin, err := originRepo(ctx)
	if err != nil {
		return ""
	}
	a, err1 := task.CanonicalRepo(origin)
	b, err2 := task.CanonicalRepo(repo)
	if err1 != nil || err2 != nil || a != b {
		return ""
	}
	out, err := gitCmd(ctx, ".", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkoutParseCacheKey is the context key withCheckoutParseCache installs.
type checkoutParseCacheKey struct{}

// checkoutParseResult is what checkoutParse returns, cached whole: a nil
// cfg with no problems means "no such checkout or file", which is worth
// remembering too (a repeat of the same failed git/file lookup).
type checkoutParseResult struct {
	cfg      *config.Config
	problems []config.Problem
}

// checkoutParseCache memoizes two distinct reads of a checkout's
// fugaro.yaml by root, for one command invocation: the working tree's
// (checkoutParse, below) and the default branch's, read from git
// (defaultBranchParse, init_repo_target.go) — different bytes at the same
// root, so they get separate maps and never share a key. A third map,
// layer, is keyed the same way and shared with run.go's embedProjectLayer
// (Task 11): whichever of the two resolves the checkout's project layer
// first in a command leaves it here for the other, scoped (see
// layerCacheScope) so a launch never inherits an answer that was resolved
// under a different, or absent, project selection.
//
// checkoutParse: run.go's launch path calls it (through checkoutConfig) up
// to three times for the same spec.Repo, launch_budget.go once more, and
// secrets.go a fourth time when its own first read came back nil — each
// one, uncached, re-reads fugaro.yaml from disk and resolves the project
// layer again (a bucket read, Task 10). The one thing besides root that
// could vary call to call is layerOptions, but every path through
// checkoutParse hands parseCheckoutFugaroYAML the same fixed
// layerOptions{Lenient: true}, so root alone is key enough here.
//
// defaultBranchParse: the converge reads the default branch's fugaro.yaml
// up to three times in one run (once while gathering inputs for the
// anchor hint, once more in the repository stage, once in the secrets
// stage), every time with the same selectedProjectConfig(ctx) too.
type checkoutParseCache struct {
	mu            sync.Mutex
	workingTree   map[string]checkoutParseResult
	defaultBranch map[string]defaultBranchResult
	layer         map[string]layerCacheEntry
}

// layerCacheEntry is a cached project-layer resolution together with the
// scope it was resolved under.
type layerCacheEntry struct {
	layer foundLayer
	scope layerCacheScope
}

// layerCacheScope is the part of findLayer's input, besides the checkout's
// own bytes, that decides what it does: which local project config (if
// any) is selected. findLayer's bucket URL comes from lc when lc names the
// same project and GCP project the checkout does, and a nil lc forces a
// lenient call offline (findLayer, "offline is forced"). checkoutParse
// resolves against selectedProjectConfig(ctx) (the ambient selection: a
// committed project, FUGARO_PROJECT, FUGARO_CONFIG...); embedProjectLayer
// resolves against env.lc (the one openCloud actually selected for this
// command, which may differ: --project, --config and --gcp-project all
// select env.lc without changing what selectedProjectConfig(ctx) sees).
// Two resolutions only ever share a cache entry when their scopes are
// equal, so a strict launch (decision L16) never reuses a lenient
// resolution made under a different, or absent, project selection — which
// would otherwise also smuggle in decision L13's offline exception for a
// nil lc (a stale local cache standing in for a bucket read that a
// differently-scoped strict call would have made for real).
type layerCacheScope struct {
	selected         bool
	name, gcpProject string
	bucketURL        string
}

func layerScopeOf(lc *localcfg.Config) layerCacheScope {
	if lc == nil {
		return layerCacheScope{}
	}
	return layerCacheScope{selected: true, name: lc.Name, gcpProject: lc.GCPProject, bucketURL: lc.BucketURL()}
}

// withCheckoutParseCache installs an empty checkoutParseCache, root.go's
// PersistentPreRun does this once per command so every checkoutParse and
// defaultBranchParse call within the same invocation shares it; a context
// without one (a direct unit test, or any call before the command tree
// installs it) makes both work exactly as before, uncached.
func withCheckoutParseCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, checkoutParseCacheKey{},
		&checkoutParseCache{workingTree: map[string]checkoutParseResult{}, defaultBranch: map[string]defaultBranchResult{}, layer: map[string]layerCacheEntry{}})
}

// cachedProjectLayer is the project layer checkoutParse (or embedProjectLayer
// itself) already resolved for root in this command under lc's exact scope
// (layerCacheScope), if any: only ever a determinate one (never Unknown —
// see cacheProjectLayer), and only ever one resolved under the same
// project selection lc names, so reusing it is exactly as safe as lc's own
// caller resolving it fresh.
func cachedProjectLayer(ctx context.Context, root string, lc *localcfg.Config) (foundLayer, bool) {
	cache, _ := ctx.Value(checkoutParseCacheKey{}).(*checkoutParseCache)
	if cache == nil {
		return foundLayer{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	e, ok := cache.layer[root]
	if !ok || e.scope != layerScopeOf(lc) {
		return foundLayer{}, false
	}
	return e.layer, true
}

// cacheProjectLayer records fl as root's project layer for the rest of this
// command, scoped to lc (the project config it was resolved against), when
// fl is a determinate answer (never Unknown: a lenient caller's "could not
// tell" is never reused by a strict one). cachedProjectLayer only ever
// returns an entry whose scope matches the caller's own lc.
func cacheProjectLayer(ctx context.Context, root string, lc *localcfg.Config, fl foundLayer) {
	if fl.Unknown {
		return
	}
	cache, _ := ctx.Value(checkoutParseCacheKey{}).(*checkoutParseCache)
	if cache == nil {
		return
	}
	cache.mu.Lock()
	cache.layer[root] = layerCacheEntry{layer: fl, scope: layerScopeOf(lc)}
	cache.mu.Unlock()
}

// checkoutParse is checkoutConfig with the parse's problems: nil and no
// problems when there is no such checkout or file, nil and the problems
// when its fugaro.yaml doesn't parse.
func checkoutParse(ctx context.Context, repo string) (*config.Config, []config.Problem) {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return nil, nil
	}
	cache, _ := ctx.Value(checkoutParseCacheKey{}).(*checkoutParseCache)
	if cache != nil {
		cache.mu.Lock()
		r, ok := cache.workingTree[root]
		cache.mu.Unlock()
		if ok {
			return r.cfg, r.problems
		}
	}
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	var result checkoutParseResult
	if err == nil {
		lc := selectedProjectConfig(ctx)
		rf, rerr := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
		if rerr != nil {
			result.problems = []config.Problem{{Path: "project layer", Message: oneLineCLI(rerr.Error())}}
		} else {
			result.cfg, result.problems = rf.Cfg, rf.Problems
			cacheProjectLayer(ctx, root, lc, rf.Layer)
		}
	}
	if cache != nil {
		cache.mu.Lock()
		cache.workingTree[root] = result
		cache.mu.Unlock()
	}
	return result.cfg, result.problems
}

// refuseHTTP2Debug refuses to talk to Google while GODEBUG holds
// http2debug: Go then prints HTTP/2 requests, headers (the OAuth bearer
// token) and, at 2, frames of the body (a secret being stored) to stderr.
// GODEBUG is read before main runs, so it can't be unset in time; every
// command that opens the cloud refuses instead, with a fixed message.
func refuseHTTP2Debug(getenv func(string) string) error {
	if strings.Contains(getenv("GODEBUG"), "http2debug") {
		return userErr("GODEBUG sets http2debug, which makes Go print HTTP requests, credentials and secrets included, to stderr; unset it (or drop http2debug) and rerun")
	}
	return nil
}
