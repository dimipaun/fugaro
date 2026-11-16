package runner

// This file is S3's own half: a run in the shared Bitbucket job of a project
// job reaches git only through this execution's forwarder, and holds no
// Bitbucket token anywhere it could reach one.
//
// The shared job mounts no Bitbucket token at all (D2302: internal/infra
// deploys a Bitbucket profile no mount and refuses a profile that carries
// one), so what a per-repository proxied run treats as one arm of a policy is
// here the only arm there is: the credential helper installed for the loopback
// endpoint, the insteadOf rewrite that puts every bitbucket.org URL on it, and
// the credentials service behind both. Everything else about the run is the
// per-repository path's, which is why the tests below ask the same seams
// gitproxy_test.go's do -- the decision (proxiedBitbucket), the forwarder, the
// credential URL and the git environment built from them -- and read the
// checkout a run of the job actually opens.
//
// The fixtures build the state the deploy half will create (S1's deployer,
// which no installation has run for a Bitbucket profile yet: I1 flips
// sharedJobProviders), and the job's name is named rather than deployed for the
// reason reviewproxy_internal_test.go gives for its own: what is pinned here is
// the runner's decision about a run that is in this job.

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// sharedBitbucketJob is the shared coding job of the fixture profile: the name
// gcp.ProjectJobName computes for its no-account job, which is the name a
// run's own CLOUD_RUN_JOB carries and the one S1's deployer deploys it under.
func sharedBitbucketJob() string {
	return gcp.ProjectJobName(projectpolicy.DefaultProfileName, gitprov.KindBitbucket, "")
}

// sharedBitbucketRunIn is a coding run in the Cloud Run job job, with the
// installation's credentials service on and a Bitbucket token planted in the
// job's environment the way an earlier release's launcher or an edited job
// definition still carries one: the shared job mounts none, and a run that
// reached for this one would be one the platform meant to withhold
// (TestSharedBitbucketJobHoldsNoToken).
//
// Its identity is the launcher's own token with the branch claim (fb): a
// coding run pushes, and the credentials service admits a push only for the
// branch the identity names (G6).
func sharedBitbucketRunIn(t *testing.T, job string, p *projectpolicy.Policy, found bool, edit func(*run)) *run {
	t.Helper()
	testutil.IsolateGit(t)
	spec := &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main",
		Workflow: identityWorkflow, Task: "Add a feature"}
	r := identityRun(t, job, identitySlug(t, "bitbucket", "acme/app"), spec, p, found)
	r.d.Env = []string{
		providers.EnvCredentialsServiceURL + "=" + proxyService,
		// A metadata server nothing listens on: the forwarder mints the job's
		// ID token per request, and no test here makes one.
		metadataHostEnv + "=127.0.0.1:1",
		providers.EnvBitbucketToken + "=" + proxyToken,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	r.env = []string{"PATH=" + os.Getenv("PATH")}
	r.ident.tok = pushIdentity(t)
	if edit != nil {
		edit(r)
	}
	return r
}

// sharedBitbucketRun is sharedBitbucketRunIn under the fixture policy: the
// block that admits acme/app with proxy: true.
func sharedBitbucketRun(t *testing.T, edit func(*run)) *run {
	t.Helper()
	return sharedBitbucketRunIn(t, sharedBitbucketJob(), reviewProxyPolicy(), true, edit)
}

// startSharedBitbucketJob is the rest of the proxied bootstrap in bootstrap's
// own order: the forwarder (startProxiedGit), the remote the run computes for
// its project job (cloneURL), the credential URL built from that remote and the
// forwarder (forwarderRemote), and the provider opened through the service
// (openProvider, which is providers.Proxied for a proxied run).
func startSharedBitbucketJob(t *testing.T, r *run) *run {
	t.Helper()
	ctx := context.Background()
	if err := r.startProxiedGit(ctx); err != nil {
		t.Fatalf("starting the shared Bitbucket job's forwarder: %v", err)
	}
	if r.forward == nil {
		t.Fatal("a run in the shared Bitbucket job started no forwarder")
	}
	t.Cleanup(r.closeForward)
	if !strings.Contains(r.forward.URL(), "127.0.0.1:") {
		t.Errorf("the forwarder listens on %q, want the loopback address", r.forward.URL())
	}
	remote, err := r.cloneURL(ctx)
	if err != nil {
		t.Fatalf("computing the shared job's clone URL: %v", err)
	}
	if want := "https://bitbucket.org/acme/app.git"; remote != want {
		t.Fatalf("the shared job's clone URL is %q, want %q", remote, want)
	}
	r.credURL = gitops.CredentialURL(r.forwarderRemote(remote))
	if !strings.HasPrefix(r.credURL, "http://127.0.0.1:") {
		t.Fatalf("the proxied remote's credential URL is %q, want the loopback forwarder's", r.credURL)
	}
	if err := r.openProvider(ctx, gitprov.KindBitbucket, "the project job "+sharedBitbucketJob()); err != nil {
		t.Fatalf("opening the shared job's Bitbucket provider: %v", err)
	}
	return r
}

// TestSharedBitbucketJobUsesTheForwarder is S3's acceptance: a coding run in
// the shared Bitbucket job is a proxied run, with exactly the bootstrap the
// per-repository path builds -- the loopback forwarder, the clone URL the
// launcher wrote, the credential scoped to the endpoint, the tokenless provider
// and the git environment built from the two -- and nothing about the job
// changes what the run's git does.
//
// The job model decides nothing here (proxiedBitbucket reads no class since
// S3): what makes this run proxied is the published block and the job's own
// environment, and the two jobs of a profile differ in the class their names
// pin and the account each one's name spells, never in whether the proxy is
// on. checkJobClass is the door that holds a run to its own job.
//
// Mutation (run, restored): require the review class in proxiedBitbucket --
// r.proxied is false, startProxiedGit halts (sharedJobNeedsTheProxy) and this
// test fails on the first assertion.
func TestSharedBitbucketJobUsesTheForwarder(t *testing.T) {
	r := admitProjectProxyRun(t, sharedBitbucketRun(t, nil), "")
	if !r.proxied {
		t.Fatal("a coding run in the shared Bitbucket job was not proxied: the job mounts no Bitbucket token (D2302), so the service's proxy is the only thing it can clone with")
	}
	startSharedBitbucketJob(t, r)
	if r.provider == nil || r.providerKind != gitprov.KindBitbucket {
		t.Fatalf("the shared job's provider is %T/%q, want a Bitbucket provider", r.provider, r.providerKind)
	}
	// The provider the service hands a proxied run holds no token (G1).
	if r.auth.Token != "" {
		t.Errorf("the shared job's provider handed the run the token %q, want none", r.auth.Token)
	}
	// This is the environment openCheckout hands the clone, and it is the
	// proxied one: the helper is installed for the loopback endpoint alone
	// (both of gitops.CredentialVars' entries, so git is asked for the
	// rewritten URL), and the third entry rewrites every Bitbucket URL to
	// this execution's listener -- which is what makes this run's clone, its
	// fetches and its push reach bitbucket.org only through the service.
	vars, err := r.authVars()
	if err != nil {
		t.Fatalf("building the shared job's git credentials: %v", err)
	}
	if got := vars["FUGARO_GIT_TOKEN"]; got != "" {
		t.Errorf("the shared job's git environment carries FUGARO_GIT_TOKEN=%q, want none", got)
	}
	if want := "credential." + r.credURL + ".helper"; vars["GIT_CONFIG_KEY_0"] != want || vars["GIT_CONFIG_KEY_1"] != want {
		t.Errorf("the credential helper is installed for %q and %q, want %q twice: it must be scoped to the endpoint git talks to",
			vars["GIT_CONFIG_KEY_0"], vars["GIT_CONFIG_KEY_1"], want)
	}
	if got := vars["GIT_CONFIG_COUNT"]; got != "3" {
		t.Errorf("the proxied git environment installs %s config entries, want 3 (the helper twice, the rewrite once)", got)
	}
	if key, want := vars["GIT_CONFIG_KEY_2"], "url."+r.forward.URL()+".insteadOf"; key != want {
		t.Errorf("the rewrite key is %q, want %q", key, want)
	}
	if got := vars["GIT_CONFIG_VALUE_2"]; got != bitbucketOrigin {
		t.Errorf("the rewrite sends %q to the forwarder, want every %s URL", got, bitbucketOrigin)
	}
	// A coding run's agent does push for it, so its own environment is the
	// proxied one too -- the same helper and rewrite, and the same absence of
	// a token.
	if err := r.refreshGitAuth(context.Background(), bootstrapAuthMinValid); err != nil {
		t.Fatalf("refreshing the shared job's git credentials: %v", err)
	}
	if got := proxyEnvValue(r.env, "FUGARO_GIT_TOKEN"); got != "" {
		t.Errorf("the shared job's agent environment carries FUGARO_GIT_TOKEN=%q, want the helper's empty token", got)
	}
	if got := proxyEnvValue(r.env, "GIT_CONFIG_VALUE_2"); got != bitbucketOrigin {
		t.Errorf("the shared job's agent environment rewrites %q, want %q: the agent's own git commands have to reach Bitbucket through the service too", got, bitbucketOrigin)
	}
}

// TestSharedBitbucketJobHoldsNoToken: the shared job mounts no Bitbucket token,
// and nothing of this run may manufacture one. The job's environment plants one
// anyway -- the shape of a job launched before the flip, or one whose definition
// a launcher edited -- and it reaches neither the git environment the run
// builds, nor the coding agent's own environment, nor anything the run logs.
//
// Mutation (run, restored): open the provider with providers.FromEnv instead of
// providers.Proxied, or hand auth.Token to gitops.CredentialVars in
// credentialVars' proxied arm. The planted token is then a git credential, and
// every assertion below fails.
func TestSharedBitbucketJobHoldsNoToken(t *testing.T) {
	var logs strings.Builder
	r := admitProjectProxyRun(t, sharedBitbucketRun(t, func(r *run) {
		r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	}), "")
	startSharedBitbucketJob(t, r)
	if err := r.refreshGitAuth(context.Background(), bootstrapAuthMinValid); err != nil {
		t.Fatalf("refreshing the shared job's git credentials: %v", err)
	}
	vars, err := r.authVars()
	if err != nil {
		t.Fatal(err)
	}
	// The run's git environment is the proxied one: a helper for the loopback
	// endpoint carrying no credential at all, and the rewrite beside it.
	env := gitops.WithVars(gitops.IdentityEnvFor(r.identity), vars)
	for _, k := range []string{"FUGARO_GIT_TOKEN", "FUGARO_GIT_USERNAME", "GH_TOKEN"} {
		if got := proxyEnvValue(env, k); got != "" {
			t.Errorf("the shared job's git environment carries %s=%q", k, got)
		}
	}
	for _, k := range []string{"FUGARO_GIT_TOKEN", "FUGARO_GIT_USERNAME", "GH_TOKEN"} {
		if got := proxyEnvValue(r.env, k); got != "" {
			t.Errorf("the shared job's agent environment carries %s=%q", k, got)
		}
	}
	for _, e := range append(append([]string{}, env...), r.env...) {
		if strings.Contains(e, proxyToken) {
			t.Errorf("the planted Bitbucket token reached an environment: %s", e)
		}
	}
	for _, e := range r.d.Env {
		if strings.Contains(e, providers.EnvBitbucketToken) && !strings.HasSuffix(e, "="+proxyToken) {
			t.Errorf("the plant itself is not the job's environment entry it was: %s", e)
		}
	}
	if strings.Contains(logs.String(), proxyToken) {
		t.Errorf("the planted Bitbucket token reached the run's log: %s", logs.String())
	}
}

// TestBakedFetchGoesThroughTheForwarder settles U3: a checkout the image baked
// -- one at the run's work directory, whose origin is the URL a launcher wrote
// for this repository -- is fetched through THIS execution's forwarder like any
// other, because what git follows is not the remote but the insteadOf rewrite
// the run's git environment carries. bootstrap's own base-branch fetch
// (defaultBranchFile, which reads fugaro.yaml at origin/<default branch>) is
// the fetch every run of a baked job makes before the agent starts, and S9
// resets and verifies this same checkout before it.
//
// Real git, the run's own credentialVars and the real forwarder against the
// fake service, for TestProxiedFetchFollowsThisExecutionsForwarder's reason:
// the claim is about what git does with the two. The fetch fails -- the fake
// service is not a git server -- and what it must not do is fail without
// reaching the service, which is what this asserts.
//
// Mutation (run, restored): drop the rewrite from credentialVars' proxied arm,
// and the fetch goes to https://bitbucket.org/ -- a host nothing in this test
// listens on -- and the service is sent nothing.
func TestBakedFetchGoesThroughTheForwarder(t *testing.T) {
	svc := newGitServiceFake(t, time.Now())
	meta := gitMetadataFake(t, gitServiceIDToken)
	r := admitProjectProxyRun(t, sharedBitbucketRun(t, func(r *run) {
		r.d.WorkDir = t.TempDir()
		r.d.Env = proxyEnvSet(r.d.Env, metadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
		r.d.Env = proxyEnvSet(r.d.Env, providers.EnvCredentialsServiceURL, svc.srv.URL)
	}), "")
	startSharedBitbucketJob(t, r)
	// The baked checkout: a repository already at the work directory, with the
	// origin the launcher wrote -- the state S9's reset and verify are handed
	// on a shared image.
	source := testutil.NewRemote(t, map[string]string{"a.txt": "a\n"})
	testutil.Git(t, t.TempDir(), "clone", "--quiet", source, r.d.WorkDir)
	testutil.Git(t, r.d.WorkDir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	// The checkout is opened the way openCheckout opens it: the run's own
	// identity and the proxied credentialVars.
	cloneVars, err := r.authVars()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := gitops.Open(r.d.WorkDir, gitops.WithVars(gitops.IdentityEnvFor(r.identity), cloneVars))
	if err != nil {
		t.Fatalf("opening the baked checkout: %v", err)
	}
	if err := repo.FetchBase(context.Background(), "main"); err == nil {
		t.Error("a fetch from a service that is not a git server succeeded")
	}
	calls := svc.mintCalls()
	if len(calls) == 0 {
		t.Fatal("the baked fetch never reached the installation's credentials service: the run's git did not follow the rewrite")
	}
	if want := gateway.GitProxyPrefix + "acme/app.git/info/refs"; calls[0].path != want {
		t.Errorf("the service was asked for %q, want %q", calls[0].path, want)
	}
	if calls[0].auth != "Bearer "+gitServiceIDToken {
		t.Errorf("the baked fetch reached the service with %q, want this job's own identity", calls[0].auth)
	}
	if want := sharedBitbucketJob(); calls[0].job != want {
		t.Errorf("the baked fetch named the job %q, want %q", calls[0].job, want)
	}
}

// TestSharedBitbucketJobHaltsWhenTheProxyRefusesARepository is S3's last
// acceptance clause: a run in the shared Bitbucket job that the installation's
// credential proxy does not serve halts, with one sentence naming the
// repository it is for and the fix, before any git credential exists and before
// anything is cloned -- instead of going on to clone with no credential and
// stopping at git's own "could not read Username for 'https://bitbucket.org'",
// which names neither the repository, nor the switch that is off, nor the list
// that is short.
//
// The halt decides nothing itself: proxiedBitbucket already answered from the
// same two facts the credentials service reads (the published block, and the
// job's own environment), and this sentence is what a run does about that
// answer. A refused repository is a policy edit and never a retry (contract.md,
// "A refusal as retryable"): the operator widens the block or the profile's
// allow_repos, publishes again, and the run is launched once more.
//
// Mutation (run, restored): drop the sharedJobNeedsTheProxy call from
// startProxiedGit -- a run the proxy does not serve starts nothing, clones with
// no credential, and this test fails on err = nil.
func TestSharedBitbucketJobHaltsWhenTheProxyRefusesARepository(t *testing.T) {
	profile := func(p *projectpolicy.Policy) *projectpolicy.Policy {
		if p.Profiles == nil {
			p.Profiles = map[string]projectpolicy.Profile{}
		}
		p.Profiles[projectpolicy.DefaultProfileName] = projectpolicy.Profile{JobProfile: config.JobProfile{Provider: gitprov.KindBitbucket}}
		return p
	}
	for _, tc := range []struct {
		name  string
		p     *projectpolicy.Policy
		env   []string
		wants []string
	}{
		{
			name:  "the project has published no block",
			p:     profile(&projectpolicy.Policy{Version: projectpolicy.Version}),
			wants: []string{"acme/app", sharedBitbucketJob(), "bitbucket_token block is absent", "fugaro secrets allow " + config.BitbucketSecret},
		},
		{
			name:  "the block carries proxy: false",
			p:     profile(&projectpolicy.Policy{Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/app"}}}),
			wants: []string{"acme/app", sharedBitbucketJob(), "proxy: false", "--proxy=true"},
		},
		{
			name: "the job names no credentials service",
			p:    profile(&projectpolicy.Policy{Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/app"}, Proxy: true}}),
			// The environment without the service's URL: a job an update has
			// not reached yet.
			env:   []string{providers.EnvBitbucketToken + "=" + proxyToken, "PATH=" + os.Getenv("PATH")},
			wants: []string{"acme/app", sharedBitbucketJob(), "names no credentials service", "fugaro update"},
		},
		{
			name:  "the block does not admit the repository",
			p:     profile(&projectpolicy.Policy{Version: projectpolicy.Version, BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/other"}, Proxy: true}}),
			wants: []string{"acme/app", sharedBitbucketJob(), "allow_repos", "bitbucket/acme/other", "fugaro secrets allow " + config.BitbucketSecret + " acme/app"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sharedBitbucketRunIn(t, sharedBitbucketJob(), tc.p, true, func(r *run) {
				if tc.env != nil {
					r.d.Env = tc.env
				}
			})
			admitProjectProxyRun(t, r, "")
			if r.proxied {
				t.Fatalf("the run is proxied under a block that %s", tc.name)
			}
			err := r.startProxiedGit(context.Background())
			if err == nil {
				t.Fatal("a run in the shared Bitbucket job whose repository the proxy does not serve started as if it had a credential")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
			if !strings.Contains(err.Error(), "before any git credential") {
				t.Errorf("the halt does not say where the run stopped: %v", err)
			}
			if r.forward != nil {
				t.Error("a refused run started a forwarder: nothing of this run is to reach the service")
			}
			if strings.Contains(err.Error(), proxyToken) {
				t.Errorf("the halt carries the job's planted Bitbucket token: %v", err)
			}
		})
	}
	// The halt is the shared Bitbucket job's alone: a per-repository job holds
	// its own mounted token, and a project job of another provider is not this
	// rule's at all. Both are the unproxied runs B1.7 leaves exactly as they
	// were (G9).
	t.Run("a per-repository job", func(t *testing.T) {
		r := sharedBitbucketRunIn(t, "", reviewProxyPolicy(), true, nil)
		if err := r.startProxiedGit(context.Background()); err != nil {
			t.Fatalf("a per-repository run that is not proxied refused to start: %v", err)
		}
		if r.forward != nil {
			t.Error("a per-repository run that is not proxied started a forwarder")
		}
	})
	t.Run("a project job of another provider", func(t *testing.T) {
		github := &projectpolicy.Policy{Version: projectpolicy.Version,
			Profiles: map[string]projectpolicy.Profile{
				projectpolicy.DefaultProfileName: {JobProfile: config.JobProfile{Provider: gitprov.KindGitHub}},
			}}
		r := sharedBitbucketRunIn(t, gcp.ProjectJobName(projectpolicy.DefaultProfileName, gitprov.KindGitHub, ""), github, true, nil)
		if _, _, _, err := r.jobIdentity(context.Background()); err != nil {
			t.Fatalf("the fixture's own run was not admitted to its job: %v", err)
		}
		if err := r.startProxiedGit(context.Background()); err != nil {
			t.Fatalf("a github project job refused to start over a Bitbucket block: %v", err)
		}
		if r.forward != nil {
			t.Error("a github project job started the Bitbucket forwarder")
		}
	})
}
