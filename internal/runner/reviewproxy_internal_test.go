package runner

// This file is the review variant's half of B1.7 (owner direction
// 2026-11-13): `fugaro review --pr N` works on Bitbucket too, and the
// review-only job mounts no secret at all, so its git and REST access to
// Bitbucket goes through the credentials service's proxy, which holds the
// bundle's bitbucket-token server-side. What stopped it was the runner's own
// decision: proxiedBitbucket refused every project job (D505), and the review
// variant is a project job. These tests pin the class rule that replaced that
// refusal, the bootstrap the per-repository path already had -- the forwarder,
// the credential URL and the git environment built from them -- and the two
// things the review job must never carry: a token and a push.
//
// They ask the seams rather than running a whole run, for gitproxy_test.go's
// reason: the run's identity is the budget session's token and not an
// environment value, so the fixtures build it the way the run's own policy
// read does, and a whole review run through the proxy would need a live
// credentials service to forward to.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// reviewProxyPolicy is the published node these tests run under: one profile
// serving Bitbucket, and the bitbucket_token block admitting the fixture's
// repository with proxy: true -- the one source of proxiedness (G15). The
// same node serves the coding test below, so the two differ in the run's
// class alone.
func reviewProxyPolicy() *projectpolicy.Policy {
	return &projectpolicy.Policy{
		Version:        projectpolicy.Version,
		BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/app"}, Proxy: true},
		Profiles: map[string]projectpolicy.Profile{
			projectpolicy.DefaultProfileName: {JobProfile: config.JobProfile{Provider: gitprov.KindBitbucket}},
		},
	}
}

// reviewProxyJob is the review-only variant of the fixture profile's job: the
// name gcp.ProjectJobName computes for the profile's no-account job with
// config.ReviewVariantAccount on the account segment, which is the name
// internal/infra deploys the variant under and the one the run's own
// CLOUD_RUN_JOB carries.
func reviewProxyJob() string {
	return gcp.ProjectJobName(projectpolicy.DefaultProfileName, gitprov.KindBitbucket, reviewVariantAccount(""))
}

// reviewProxyRun is a run in the review-only variant of the fixture profile's
// Bitbucket job, with the installation's credentials service on and a
// Bitbucket token planted in the job's environment the way a job launched
// before a flip still carries one: the review job mounts none, and a run that
// reached for this one would be one the platform meant to withhold (G1).
//
// Its job identity is adopted the way admissionCheck adopts one -- the
// platform's own report of the execution, matched against the names the
// published policy computes -- so the class door (checkJobClass) has run
// before anything here is asked, exactly as it has in a real run.
func reviewProxyRun(t *testing.T, mode string, job string) *run {
	t.Helper()
	testutil.IsolateGit(t)
	spec := &task.Spec{Version: 1, RunID: "20261113-022425-abcd", Repo: "acme/app", Ref: "main",
		Workflow: identityWorkflow, Mode: mode, ReviewPRs: []int{7}, Task: "Review the change"}
	r := identityRun(t, job, identitySlug(t, "bitbucket", "acme/app"), spec, reviewProxyPolicy(), true)
	r.d.Env = []string{
		providers.EnvCredentialsServiceURL + "=" + proxyService,
		// A metadata server nothing listens on: the forwarder mints the job's
		// ID token per request, and no test here makes one.
		metadataHostEnv + "=127.0.0.1:1",
		providers.EnvBitbucketToken + "=" + proxyToken,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	// r.review is bootstrap's own marker for the same task (len(spec.ReviewPRs)
	// > 0, runner.go), read by the two seams a proxied run leans on: the
	// branch-claim exemption in startProxiedGit and the guard that keeps git
	// credentials out of a review agent's environment (refreshGitAuth).
	r.review = true
	// The run's own identity, which the forwarder presents to the service as
	// the caller it is (D143): without one it refuses a request before it asks
	// the job's metadata server for the ID token it also attaches, which is the
	// step the push test counts to tell a request that left from one that did
	// not.
	r.ident.tok = gitRunIdentity(t)
	return r
}

// admitProjectProxyRun is the run's admission as admissionCheck makes it: the
// identity decision first (which is where the class is held to the job), then
// the one read of the published node that decides proxiedness. It returns the
// run with r.proxied set, and fails the test if the identity is not the
// account's job it was launched into.
func admitProjectProxyRun(t *testing.T, r *run, wantAccount string) *run {
	t.Helper()
	profile, provider, account, err := r.jobIdentity(context.Background())
	if err != nil {
		t.Fatalf("the fixture's own run was not admitted to its job: %v", err)
	}
	if account != wantAccount || provider != gitprov.KindBitbucket || profile != projectpolicy.DefaultProfileName {
		t.Fatalf("jobIdentity = (%q, %q, %q), want (%q, %q, %q)",
			profile, provider, account, projectpolicy.DefaultProfileName, gitprov.KindBitbucket, wantAccount)
	}
	p, found, err := r.projectPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.proxied = r.proxiedBitbucket(p, found, true, provider, r.spec.Repo)
	return r
}

// startReviewProxyRun is the rest of the proxied bootstrap: the forwarder
// (startProxiedGit, which bootstrap starts before anything talks to
// Bitbucket), the remote the run computes for its project job (cloneURL), the
// credential URL built from that remote and the forwarder (forwarderRemote),
// and the provider opened through the service (openProvider, which is
// providers.Proxied when the run is proxied).
func startReviewProxyRun(t *testing.T, r *run) *run {
	t.Helper()
	ctx := context.Background()
	if err := r.startProxiedGit(ctx); err != nil {
		t.Fatalf("starting the review variant's forwarder: %v", err)
	}
	if r.forward == nil {
		t.Fatal("a proxied review run started no forwarder")
	}
	t.Cleanup(r.closeForward)
	remote, err := r.cloneURL(ctx)
	if err != nil {
		t.Fatalf("computing the review variant's clone URL: %v", err)
	}
	if want := "https://bitbucket.org/acme/app.git"; remote != want {
		t.Fatalf("the review variant's clone URL is %q, want %q", remote, want)
	}
	r.credURL = gitops.CredentialURL(r.forwarderRemote(remote))
	if !strings.HasPrefix(r.credURL, "http://127.0.0.1:") {
		t.Fatalf("the proxied review remote's credential URL is %q, want the loopback forwarder's", r.credURL)
	}
	if err := r.openProvider(ctx, gitprov.KindBitbucket, "the project job "+reviewProxyJob()); err != nil {
		t.Fatalf("opening the review variant's Bitbucket provider: %v", err)
	}
	return r
}

// proxyEnvValue is envValue for this package's own fixtures (runner_test.go's
// is the external package's).
func proxyEnvValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// proxyEnvSet is proxyEnvValue's writer: the job's environment with key set to
// value, for a test that has to name the metadata server the forwarder mints
// the job's identity at.
func proxyEnvSet(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if _, ok := strings.CutPrefix(kv, key+"="); !ok {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}

// TestReviewVariantOnAProjectJobBootstrapsTheBitbucketProxy is the decision
// this task changed: a review run in the review-only variant of a Bitbucket
// profile's project job opens its repository through the credentials
// service's proxy, with the same bootstrap a per-repository proxied run has --
// the forwarder, the credential URL scoped to it, the tokenless provider and
// the git environment built from the two.
//
// The class is what admits it and nothing else: the job is one the published
// policy computes, the run's own marker says review, and checkJobClass is the
// door that held the two to each other before this decision was made (a
// coding run in the same job, or a review run in the shared one, halts in
// jobIdentity and never reaches the proxy).
//
// Mutation (run, restored): drop the reviewClassRun clause in proxiedBitbucket
// and let every project job through. This test still passes and
// TestCodingRunOnAProjectJobIsNotProxied fails -- the class rule is the whole
// of what separates the two.
func TestReviewVariantOnAProjectJobBootstrapsTheBitbucketProxy(t *testing.T) {
	r := admitProjectProxyRun(t, reviewProxyRun(t, recipe.ModeReview, reviewProxyJob()), reviewVariantAccount(""))
	if !r.proxied {
		t.Fatal("a review run in the review-only variant of a project job was not proxied: its container holds no Bitbucket token, so it has nothing to clone with")
	}
	startReviewProxyRun(t, r)
	if r.provider == nil || r.providerKind != gitprov.KindBitbucket {
		t.Fatalf("the proxied review run's provider is %T/%q, want a Bitbucket provider", r.provider, r.providerKind)
	}
	// The provider the service hands a proxied run holds no token (G1), and
	// the helper installed for the loopback endpoint carries none either: the
	// job's own token -- planted in its environment above -- reaches neither.
	if r.auth.Token != "" {
		t.Errorf("the proxied review run's provider handed it the token %q", r.auth.Token)
	}
	vars, err := r.credentialVars(r.auth)
	if err != nil {
		t.Fatalf("building the proxied review run's git credentials: %v", err)
	}
	if got := vars["FUGARO_GIT_TOKEN"]; got != "" {
		t.Errorf("the review job's git environment carries FUGARO_GIT_TOKEN=%q, want none", got)
	}
	if got := vars["GIT_CONFIG_COUNT"]; got != "3" {
		t.Errorf("the proxied git environment installs %s config entries, want 3 (the helper twice, the rewrite once)", got)
	}
	if got := vars["GIT_CONFIG_VALUE_2"]; got != bitbucketOrigin {
		t.Errorf("the rewrite sends %q to the forwarder, want every %s URL", got, bitbucketOrigin)
	}
	for k, v := range vars {
		if strings.Contains(v, proxyToken) {
			t.Errorf("the git environment's %s carries the job's Bitbucket token", k)
		}
	}
}

// TestCodingRunOnAProjectJobIsNotProxied is the boundary this task did not
// cross: a shared coding run on a project job keeps today's answer (D505's
// sentence -- a project job serves every repository of its profile and holds
// its own token, so a proxied repository is never launched onto one, and the
// service admitting a shared coding caller is B5.3). The same policy, the same
// repository, the same installation: only the class differs.
//
// Mutation (run, restored): drop the reviewClassRun clause in proxiedBitbucket
// (or invert it), and this test fails: the coding run is proxied and starts a
// forwarder it must not have.
func TestCodingRunOnAProjectJobIsNotProxied(t *testing.T) {
	job := gcp.ProjectJobName(projectpolicy.DefaultProfileName, gitprov.KindBitbucket, "")
	r := admitProjectProxyRun(t, reviewProxyRun(t, "", job), "")
	if r.proxied {
		t.Fatal("a shared coding run on a project job was proxied: the proxy serving a shared job is B5.3, and today's answer is that a project job holds its own token")
	}
	if err := r.startProxiedGit(context.Background()); err != nil {
		t.Fatalf("a run that is not proxied refused to start: %v", err)
	}
	if r.forward != nil {
		t.Fatal("a coding run on a project job started a Bitbucket forwarder")
	}
}

// TestReviewVariantProxyCarriesNoBitbucketToken: the review job mounts no
// secret, and nothing of this run may manufacture one. The job's environment
// plants a Bitbucket token anyway -- the shape of a job launched before the
// flip, or one whose definition a launcher edited -- and it reaches neither
// the git environment the runner builds, nor the agent's own environment, nor
// anything the run logs.
//
// Mutation (run, restored): open the provider with providers.FromEnv instead
// of providers.Proxied, or drop the `&& !r.review` guard in refreshGitAuth.
// The planted token is then the run's git credential (FUGARO_GIT_TOKEN) and,
// in the second case, the agent's (the review agent's environment is the one
// this test reads), and every assertion below fails.
func TestReviewVariantProxyCarriesNoBitbucketToken(t *testing.T) {
	var logs strings.Builder
	r := admitProjectProxyRun(t, reviewProxyRun(t, recipe.ModeReview, reviewProxyJob()), reviewVariantAccount(""))
	r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	// The agent's environment is the one the run builds for it, and a review
	// run's is built with no git credentials at all (refreshGitAuth).
	r.env = []string{"PATH=" + os.Getenv("PATH")}
	startReviewProxyRun(t, r)
	if err := r.refreshGitAuth(context.Background(), bootstrapAuthMinValid); err != nil {
		t.Fatalf("refreshing the proxied review run's git credentials: %v", err)
	}
	vars, err := r.credentialVars(r.auth)
	if err != nil {
		t.Fatal(err)
	}
	env := gitops.WithVars(gitops.IdentityEnvFor(r.identity), vars)
	// The run's own git environment is the proxied one: a helper for the
	// loopback endpoint (which is why GIT_CONFIG_COUNT is 3) carrying no
	// credential at all -- the empty token is what "git needs no credentials"
	// is spelled with, and the service authenticates the request on the wire.
	for _, k := range []string{"FUGARO_GIT_TOKEN", "FUGARO_GIT_USERNAME", "GH_TOKEN"} {
		if got := proxyEnvValue(env, k); got != "" {
			t.Errorf("the review job's git environment carries %s=%q", k, got)
		}
	}
	for _, k := range []string{"FUGARO_GIT_TOKEN", "FUGARO_GIT_USERNAME", "GIT_CONFIG_COUNT", "GH_TOKEN"} {
		if got := proxyEnvValue(r.env, k); got != "" {
			t.Errorf("the review agent's environment carries %s=%q", k, got)
		}
	}
	for _, e := range append(append([]string{}, env...), r.env...) {
		if strings.Contains(e, proxyToken) {
			t.Errorf("the planted Bitbucket token reached an environment: %s", e)
		}
	}
	if strings.Contains(logs.String(), proxyToken) {
		t.Errorf("the planted Bitbucket token reached the run's log: %s", logs.String())
	}
}

// TestReviewVariantProxyPushIsRefusedLocally: the review variant's checkout is
// the pull request's head, detached (reviewmode's CheckoutDetached), and the
// only git environment this run ever builds is the proxied one -- a credential
// helper for the loopback endpoint carrying no token, and the rewrite that
// sends every Bitbucket URL there. A push attempted from that checkout, the
// thing a reviewed head's own tooling or the agent would try, is refused by
// git itself before any transport: a detached HEAD has no branch to push
// (push.default is git's own "simple", which this run's environment does not
// change).
//
// "Before it leaves" is the job's own metadata server, which is where the
// forwarder mints the identity it presents to the service before forwarding
// anything: a request that reached the forwarder would ask it, so a counter of
// zero is a request that never left the checkout. The stderr assertion is the
// other half -- git's own refusal, and no transport failure behind it.
//
// The service's own refusal is the second door and is deliberately not what
// this test leans on: a proxied run's identity names the branch it may push
// (fb), and a review run's names none, so the proxy would refuse a push the
// identity does not claim (B1.7's own tests cover that half).
//
// Mutation (run, restored): check the head out on a branch instead of
// detached, and the push leaves -- the forwarder is asked for the job's
// identity and this test fails on both the counter and the stderr text.
func TestReviewVariantProxyPushIsRefusedLocally(t *testing.T) {
	var metaCalls atomic.Int64
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metaCalls.Add(1)
		_, _ = io.WriteString(w, "metadata-id-token")
	}))
	t.Cleanup(meta.Close)
	r := reviewProxyRun(t, recipe.ModeReview, reviewProxyJob())
	r.d.Env = proxyEnvSet(r.d.Env, metadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
	startReviewProxyRun(t, admitProjectProxyRun(t, r, reviewVariantAccount("")))

	work := filepath.Join(t.TempDir(), "work")
	remote := testutil.NewRemote(t, map[string]string{"a.txt": "a\n"})
	testutil.Git(t, filepath.Dir(work), "clone", "--quiet", remote, work)
	// The review checkout: the pull request's head, detached, with the origin
	// the run computed for its project job (cloneURL).
	testutil.Git(t, work, "checkout", "--quiet", "--detach", "HEAD")
	testutil.Git(t, work, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	vars, err := r.credentialVars(r.auth)
	if err != nil {
		t.Fatal(err)
	}
	// The environment the run's own git commands are built with
	// (openCheckout), which is what a push from this checkout would use.
	env := gitops.WithVars(gitops.IdentityEnvFor(r.identity), vars)
	env = append(env, "PATH="+os.Getenv("PATH"), "HOME="+t.TempDir(), "LC_ALL=C")
	cmd := exec.Command("git", "push")
	cmd.Dir = work
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("a push from the review variant's checkout succeeded")
	}
	got := stderr.String()
	if !strings.Contains(got, "not currently on a branch") {
		t.Errorf("the push was not refused by git's own detached-HEAD rule: %q", got)
	}
	if strings.Contains(got, "unable to access") || strings.Contains(got, "Could not resolve host") {
		t.Errorf("the push reached the transport before it was refused: %q", got)
	}
	if n := metaCalls.Load(); n != 0 {
		t.Errorf("the forwarder asked the job's metadata server %d times: the push left the checkout", n)
	}
}
