package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// cloudOptions are the flags every command that talks to the cloud shares.
type cloudOptions struct{ config, project, region string }

// addCloudFlags registers --config, --project and --region on cmd.
func addCloudFlags(cmd *cobra.Command, o *cloudOptions) {
	f := cmd.Flags()
	f.StringVar(&o.config, "config", "", "local config file (default $FUGARO_CONFIG, else $XDG_CONFIG_HOME/fugaro/config.yaml, else ~/.config/fugaro/config.yaml)")
	f.StringVar(&o.project, "project", "", "GCP project (overrides the local config)")
	f.StringVar(&o.region, "region", "", "GCP region (overrides the local config)")
}

// cloudEnv is what a cloud command works against: the local config, the
// runs bucket and the compute backend.
type cloudEnv struct {
	lc     *localcfg.Config
	bucket *blobx.Bucket
	be     backend.Backend
	gcp    gcp.Options
}

// prices are the list prices of a region, for cost estimates of cloud runs
// (design §10.1); "" is the region the jobs run in, after --region.
func (e *cloudEnv) prices() runview.PriceBook {
	return func(region string) backend.Prices {
		if region == "" {
			region = e.lc.Region
		}
		return gcp.ListPrices(region)
	}
}

// Close releases the bucket.
func (e *cloudEnv) Close() {
	if e.bucket != nil {
		_ = e.bucket.Close()
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

// openCloud loads the local config (applying --project and --region) and
// connects to the backend and the runs bucket.
func openCloud(ctx context.Context, o cloudOptions) (*cloudEnv, error) {
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return nil, err
	}
	path := o.config
	if path == "" {
		var err error
		if path, err = localcfg.Path(os.Getenv); err != nil {
			return nil, userErr("%v", err)
		}
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		return nil, userErr("%v", err)
	}
	if err := lc.Override(o.project, o.region); err != nil {
		return nil, userErr("--project/--region: %v", err)
	}
	opts := gcp.Options{Project: lc.Project, Region: lc.Region, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager,
		CloudBuild: lc.Endpoints.CloudBuild, NoAuth: lc.Endpoints.NoAuth}}
	be, err := gcp.New(ctx, opts)
	if err != nil {
		return nil, remote(err)
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return nil, remote(err)
	}
	return &cloudEnv{lc: lc, bucket: b, be: be, gcp: opts}, nil
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
// the run's own job in this region (gcp.CheckRunExecution, S-I2). Without
// a readable task there is no job to bind it to, so it is refused too.
func (e *cloudEnv) checkExecution(name, slug string, spec *task.Spec) error {
	if spec == nil || spec.Workflow == "" {
		return fmt.Errorf("its task.json is unreadable or names no workflow, so execution %s can't be tied to the run's job", name)
	}
	return gcp.CheckRunExecution(name, e.lc.Region, slug, spec.Workflow)
}

// parseRunRef is runstore.ParseRef, refusing "." and ".." as a slug:
// path.Join would resolve them outside runs/ (security review S-M7). A bad
// reference is a user error.
func parseRunRef(ref string) (slug, runID string, err error) {
	slug, runID, err = runstore.ParseRef(ref)
	if err == nil && strings.Trim(slug, ".") == "" {
		err = fmt.Errorf("run %q must look like <repo-slug>/<run-id>", ref)
	}
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
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
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
// job-spec. A nested path (group/subgroup/name) is not owner/name.
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
	origin, err := originRepo(ctx)
	if err != nil {
		return nil
	}
	a, err1 := task.CanonicalRepo(origin)
	b, err2 := task.CanonicalRepo(repo)
	if err1 != nil || err2 != nil || a != b {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "fugaro.yaml"))
	if err != nil {
		return nil
	}
	cfg, _ := config.Parse(data)
	return cfg
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
