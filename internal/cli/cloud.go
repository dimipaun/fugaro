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
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/backend"
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

// prices are the compute prices of a region, for cost estimates of cloud
// runs (design §10.1): the local config's override, else the list price;
// "" is the region the jobs run in, after --region.
func (e *cloudEnv) prices() runview.PriceBook {
	return func(region string) backend.Prices {
		if region == "" {
			region = e.lc.Region
		}
		if p, ok := e.lc.PriceOverride(region); ok {
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
	be, err := backend.Open(ctx, lc.Backend(), map[string]backend.Opener{
		backend.CloudRun: func(ctx context.Context) (backend.Backend, error) { return gcp.New(ctx, opts) },
	})
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

// budgetScopes are the OAuth scopes the Realtime Database REST API takes
// from a person's Application Default Credentials (plan A2).
var budgetScopes = []string{"https://www.googleapis.com/auth/firebase.database", "https://www.googleapis.com/auth/userinfo.email"}

// openBudgetDB selects the project config (the header goes to stderr, as in
// openCloud) and returns a client of the project's budget database, acting as
// the person (ADC): their IAM roles on the Firebase project decide what the
// database lets them do (viewer reads, admin writes). The project needs
// budget.rtdb_url, which fugaro init --firebase writes. A config that sets
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
		return nil, nil, userErr("project %s has no Firebase budget backend (budget.rtdb_url is not set in its project config): an operator runs fugaro init --firebase <firebase-project-id> --name %s", lc.Name, lc.Name)
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
// objects of run slug (whose task is spec), unless it is an execution of
// the run's own job in this region (gcp.CheckRunExecution): the run's service account can write those
// objects, so they could name another repository's job. Without
// a readable task there is no job to bind it to, so it is refused too.
func (e *cloudEnv) checkExecution(name, slug string, spec *task.Spec) error {
	if spec == nil || spec.Workflow == "" {
		return fmt.Errorf("its task.json is unreadable or names no workflow, so execution %s can't be tied to the run's job", name)
	}
	err := gcp.CheckRunExecution(name, e.lc.Region, slug, spec.Workflow)
	var wr *gcp.WrongRegionError
	if errors.As(err, &wr) {
		return fmt.Errorf("the run's execution is in region %s, not %s; pass --region %s to follow it", wr.Region, wr.Want, wr.Region)
	}
	return err
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

// localRepo is the local config's entry for repo, matched in canonical form.
func (e *cloudEnv) localRepo(repo string) (localcfg.Repo, bool) {
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return localcfg.Repo{}, false
	}
	for name, r := range e.lc.Repos {
		if c, err := task.CanonicalRepo(name); err == nil && c == want {
			return r, true
		}
	}
	return localcfg.Repo{}, false
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

// checkoutParse is checkoutConfig with the parse's problems: nil and no
// problems when there is no such checkout or file, nil and the problems
// when its fugaro.yaml doesn't parse.
func checkoutParse(ctx context.Context, repo string) (*config.Config, []config.Problem) {
	origin, err := originRepo(ctx)
	if err != nil {
		return nil, nil
	}
	a, err1 := task.CanonicalRepo(origin)
	b, err2 := task.CanonicalRepo(repo)
	if err1 != nil || err2 != nil || a != b {
		return nil, nil
	}
	out, err := gitCmd(ctx, ".", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "fugaro.yaml"))
	if err != nil {
		return nil, nil
	}
	return config.Parse(data)
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
