package runner

// This file is B1.7's runner half: a run whose published policy admits its
// repository with proxy: true, on an installation that has its credentials
// service on, reaches Bitbucket only through the run's own loopback forwarder
// (gateway.GitForward, B1.6), and its job holds no Bitbucket token at all
// (G1, G9).
//
// It asks the three seams that half is made of -- the decision
// (proxiedBitbucket, read once by admissionCheck), the environment the
// forwarder puts in the run's and the agent's git (credentialVars) and the
// endpoint git is actually routed to and the credential is scoped to
// (forwarderRemote) -- rather than running a whole run, for the reason
// git_service_internal_test.go gives for its own half: the run's identity is
// the budget session's token and not an environment value, so the fixtures
// build it the way the run's own policy read does (gitRunIdentity).

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// proxyService is the installation's credentials service as these fixtures
// name it. Nothing is called at it: the forwarder records the URL it was
// started for and sends nothing there until a request arrives, and these
// tests never make one.
const proxyService = "https://creds.example.invalid"

// proxyToken is a Bitbucket token-shaped value that must reach neither the
// run's git environment nor the agent's. It is planted in the job's
// environment the way a job launched before the flip still carries one, and
// it is long enough to be redactable, as every token fixture in this package
// is.
const proxyToken = "bitbucket-proxy-fixture-token-1"

// proxiedRun is a run whose Bitbucket access goes through the forwarder.
// Whether the run is proxied is admissionCheck's to decide (its own test,
// TestProxyFalseUnchanged); this sets the fact the rest of the run reads, and
// the environment that decision was made from.
func proxiedRun(t *testing.T, id *token.Session, edit func(*run)) *run {
	t.Helper()
	return svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{
			providers.EnvCredentialsServiceURL + "=" + proxyService,
			// A metadata server nothing listens on: the forwarder mints the
			// job's ID token per request, and no test here makes one.
			metadataHostEnv + "=127.0.0.1:1",
			providers.EnvBitbucketToken + "=" + proxyToken,
		}
		r.env = []string{"PATH=/usr/bin"}
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		r.proxied = true
		r.ident.tok = id
		if edit != nil {
			edit(r)
		}
	})
}

// pushIdentity is the run's identity as a launcher at or after the proxy mints
// it: it names the branch the run may push, which is the one thing the service
// admits a push for (fb, G6). gitRunIdentity is the older launcher's -- the one
// that predates the claim -- and the tests below need both.
func pushIdentity(t *testing.T) *token.Session {
	t.Helper()
	return gitRunIdentityFor(t, "fugaro/"+svcRunID)
}

// startForward starts the run's forwarder, as bootstrap does, and closes it
// with the run's own cleanup.
func startForward(t *testing.T, r *run) {
	t.Helper()
	if err := r.startProxiedGit(context.Background()); err != nil {
		t.Fatalf("starting the forwarder of a proxied run: %v", err)
	}
	if r.forward == nil {
		t.Fatal("a proxied run started no forwarder")
	}
	t.Cleanup(r.closeForward)
}

// TestProxiedBitbucketAgentEnvHasNoToken: a proxied run's git environment
// carries no credential at all. The helper is installed against the
// forwarder's own endpoint with an empty token (G1: the job holds none, and
// it is the credentials service that puts one on the wire), the rewrite that
// sends every bitbucket.org URL there goes in with it (so the agent's own git
// commands reach Bitbucket only through the service), and the token the
// environment still plants reaches neither the run's git nor the agent's own
// environment.
//
// Mutation (run, restored): hand auth.Token to gitops.CredentialVars in
// credentialVars' proxied arm -- the planted token is in the git environment
// and this test fails on it.
func TestProxiedBitbucketAgentEnvHasNoToken(t *testing.T) {
	r := proxiedRun(t, pushIdentity(t), nil)
	startForward(t, r)
	r.credURL = gitops.CredentialURL(r.forwarderRemote("https://bitbucket.org/acme/app.git"))
	if r.credURL == "" || !strings.HasPrefix(r.credURL, "http://127.0.0.1:") {
		t.Fatalf("the proxied remote's credential URL is %q, want the loopback forwarder's", r.credURL)
	}
	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the workflow"); err != nil {
		t.Fatalf("opening the provider of a proxied run: %v", err)
	}
	if r.auth.Token != "" {
		t.Errorf("the proxied provider handed the run the token %q: the job holds none (G1)", r.auth.Token)
	}
	// A token the run's auth somehow carries reaches the helper all the same:
	// the helper's token is empty because this run has none to give it, not
	// because the auth it was handed happened to be. (The plant is what an
	// older path, or the mount the flip has not dropped yet, could leave
	// behind -- and the flip is what removes the last of them.)
	r.auth = gitprov.GitAuth{Username: "x-token-auth", Token: proxyToken}
	vars, err := r.authVars()
	if err != nil {
		t.Fatal(err)
	}
	if vars["FUGARO_GIT_TOKEN"] != "" {
		t.Errorf("the git environment carries the token %q, want none", vars["FUGARO_GIT_TOKEN"])
	}
	if want := "credential." + r.credURL + ".helper"; vars["GIT_CONFIG_KEY_0"] != want || vars["GIT_CONFIG_KEY_1"] != want {
		t.Errorf("the credential helper is installed for %q and %q, want %q: it must be scoped to the endpoint git talks to", vars["GIT_CONFIG_KEY_0"], vars["GIT_CONFIG_KEY_1"], want)
	}
	if want := "3"; vars["GIT_CONFIG_COUNT"] != want {
		t.Errorf("GIT_CONFIG_COUNT is %q, want %q: the helper's two entries and the rewrite", vars["GIT_CONFIG_COUNT"], want)
	}
	if key, want := vars["GIT_CONFIG_KEY_2"], "url."+r.forward.URL()+".insteadOf"; key != want {
		t.Errorf("the rewrite key is %q, want %q", key, want)
	}
	if got, want := vars["GIT_CONFIG_VALUE_2"], "https://bitbucket.org/"; got != want {
		t.Errorf("the rewrite covers %q, want %q", got, want)
	}
	for _, kv := range r.env {
		if strings.Contains(kv, proxyToken) {
			t.Errorf("the agent's environment carries the Bitbucket token: %s", kv)
		}
	}
	// The agent's own environment is where a credential costs the most, and
	// it is the one this run's vars reach (r.env, set by the fixture).
	if !slices.Contains(r.env, "FUGARO_GIT_TOKEN=") {
		t.Errorf("the agent's environment does not carry the credential helper's variables: %q", r.env)
	}
}

// TestProxiedFetchFollowsThisExecutionsForwarder: what a proxied run's git
// actually reaches is decided by the insteadOf rewrite its environment carries
// -- git opens the URL the checkout records, and every Bitbucket URL is
// rewritten to THIS execution's listener -- so a checkout an earlier execution
// left behind is fetched through the forwarder of the run that is running now,
// whatever port it drew, and the endpoint is never written into the checkout.
//
// Real git, the run's own credentialVars and the real forwarder against the
// fake service, because the claim is about what git does with the two: a
// remote whose port an earlier execution drew, or a rewrite that is missing or
// aimed at the wrong endpoint, is a fetch that reaches neither this listener
// nor the service, and it fails here naming the URL it tried.
//
// Mutation (run, restored): hand the launcher's remote to gitops in place of
// the rewrite -- the fetch goes to https://bitbucket.org/ and the service is
// sent nothing.
func TestProxiedFetchFollowsThisExecutionsForwarder(t *testing.T) {
	svc := newGitServiceFake(t, time.Now())
	meta := gitMetadataFake(t, gitServiceIDToken)
	r := proxiedRun(t, pushIdentity(t), func(r *run) {
		r.d.Env = []string{
			providers.EnvCredentialsServiceURL + "=" + svc.srv.URL,
			metadataHostEnv + "=" + strings.TrimPrefix(meta.URL, "http://"),
		}
	})
	startForward(t, r)
	// The bootstrap's own wiring: the credential is scoped to the endpoint,
	// and the remote stays the URL a launcher writes for this repository --
	// which is what an earlier execution's checkout carries as its origin.
	const launcherRemote = "https://bitbucket.org/acme/app.git"
	r.credURL = gitops.CredentialURL(r.forwarderRemote(launcherRemote))
	if !strings.HasPrefix(r.credURL, "http://127.0.0.1:") {
		t.Fatalf("the credential is scoped to %q, want this execution's loopback endpoint", r.credURL)
	}
	vars, err := r.authVars()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := vars["GIT_CONFIG_VALUE_2"], bitbucketOrigin; got != want {
		t.Fatalf("the git environment rewrites %q, want %q: without it git opens the remote as it is spelled", got, want)
	}
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	testutil.Git(t, dir, "remote", "add", "origin", launcherRemote)
	repo, err := gitops.Open(dir, gitops.WithVars(nil, vars))
	if err != nil {
		t.Fatalf("opening the checkout: %v", err)
	}
	// The fetch fails: the fake service is not a git server. What it must not
	// do is fail without reaching the service, which is what this asserts.
	if err := repo.FetchBase(context.Background(), "main"); err == nil {
		t.Error("a fetch from a service that is not a git server succeeded")
	}
	calls := svc.mintCalls()
	if len(calls) == 0 {
		t.Fatal("the fetch never reached the installation's credentials service: the run's git did not follow the rewrite")
	}
	if want := gateway.GitProxyPrefix + "acme/app.git/info/refs"; calls[0].path != want {
		t.Errorf("the service was asked for %q, want %q", calls[0].path, want)
	}
	if calls[0].auth != "Bearer "+gitServiceIDToken {
		t.Errorf("the fetch reached the service with %q, want this job's own identity", calls[0].auth)
	}
}

// TestForwarderRemoteIsTheCredentialScope: the endpoint the credential is
// scoped to is this run's own listener, and it is not the remote. The remote a
// proxied run leaves in a checkout is the launcher's Bitbucket URL, so nothing
// this run writes carries a port that dies with it.
func TestForwarderRemoteIsTheCredentialScope(t *testing.T) {
	r := proxiedRun(t, pushIdentity(t), nil)
	startForward(t, r)
	const launcherRemote = "https://bitbucket.org/acme/app.git"
	got := r.forwarderRemote(launcherRemote)
	if want := strings.TrimSuffix(r.forward.URL(), "/") + "/acme/app.git"; got != want {
		t.Fatalf("the endpoint is %q, want %q", got, want)
	}
	if strings.Contains(got, "bitbucket.org") {
		t.Errorf("the endpoint names Bitbucket itself (%q): the credential is scoped to the loopback listener", got)
	}
	// The admission is the one every other run makes: the launcher's remote
	// for this repository, and nothing about it changes for a proxied run.
	slug, err := task.Slug(gitprov.KindBitbucket, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteAdmissionErr(launcherRemote, gitprov.KindBitbucket, "acme/app", slug); err != nil {
		t.Errorf("a proxied run's own remote was refused by the run's admission: %v", err)
	}
	if plain := r.forwarderRemote(launcherRemote); plain == launcherRemote {
		t.Error("the endpoint is the launcher's remote: a proxied run's git credential would be scoped to Bitbucket itself")
	}
}

// TestForwarderEndpointIsNeverARemote: no remote this run reads is admitted
// when it names the forwarder. The listener has a port of its own in every
// execution, so a checkout carrying one is pinned to a port that dies with the
// execution that wrote it, and git reaches the remote a checkout records rather
// than the one a caller hands it: admitting it would be a fetch, and a push,
// aimed at an address nobody is listening on. Nothing writes one any more --
// the endpoint is derived per execution, from the environment alone
// (forwarderRemote) -- and the rows below are the addresses it could be spelled
// on, the loopback ones included: the earlier shape of this code admitted
// them, and a checkout an earlier execution left kept the dead port.
func TestForwarderEndpointIsNeverARemote(t *testing.T) {
	slug, err := task.Slug(gitprov.KindBitbucket, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, remote string }{
		{"a loopback endpoint naming this run's repository", "http://127.0.0.1:9" + gateway.GitProxyPrefix + "acme/app.git"},
		{"the IPv6 loopback spelling of one", "http://[::1]:9" + gateway.GitProxyPrefix + "acme/app.git"},
		{"an address that is not loopback", "http://10.0.0.5:9" + gateway.GitProxyPrefix + "acme/app.git"},
		{"a host that merely begins with 127.0.0.1", "http://127.0.0.1.evil.example:9" + gateway.GitProxyPrefix + "acme/app.git"},
	} {
		if err := remoteAdmissionErr(tc.remote, gitprov.KindBitbucket, "acme/app", slug); err == nil {
			t.Errorf("%s (%q) was admitted as this run's remote", tc.name, tc.remote)
		}
	}
	if err := remoteAdmissionErr("https://bitbucket.org/acme/app.git", gitprov.KindBitbucket, "acme/app", slug); err != nil {
		t.Errorf("the repository's own Bitbucket remote was refused: %v", err)
	}
}

// TestProxiedRefusesIdentityWithoutBranch is O3's plan default, the runner's
// half: the credentials service admits a push only for the branch the run's
// identity names (fb, G6), so a run that pushes cannot do its work with an
// identity that names none, and it stops at bootstrap -- before the clone --
// rather than at its first push, where the service's refusal names nothing an
// operator can act on. Only a launcher older than the proxy mints one of
// those, and the refusal says so.
//
// Mutation (run, restored): drop the branchClaim check from startProxiedGit --
// the refusal is gone and this test fails on err = nil.
func TestProxiedRefusesIdentityWithoutBranch(t *testing.T) {
	r := proxiedRun(t, gitRunIdentity(t), nil)
	if got := r.branchClaim(); got != "" {
		t.Fatalf("the fixture's identity names the branch %q, want none", got)
	}
	err := r.startProxiedGit(context.Background())
	if err == nil {
		t.Fatal("a proxied push run with an identity that names no branch was admitted: the service refuses every push of it, one stage later and after the clone")
	}
	for _, want := range []string{"acme/app", "proxy: true", branchClaimSince, "names none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if r.forward != nil {
		t.Error("a refused run started a forwarder: nothing of this run is to reach the service")
	}
}

// TestProxiedReviewRunNeedsNoBranch: a review run pushes nothing and opens
// nothing, so its identity names no branch and none is required of it (B1.4
// admits it with no fb, and refuses every push of it). The same is true of an
// artifact run, whose deliverable goes to the runs bucket, and of a challenger
// arm, which publishes nothing at all (D554) — the arm is refused further down
// this same path, where openProvider asks its provider for a read-only token
// and Bitbucket has none to mint (A2.1), so the fb refusal must not be what
// speaks first for it.
func TestProxiedReviewRunNeedsNoBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*run)
	}{
		{"review", func(r *run) { r.review = true }},
		{"artifact", func(r *run) { r.artifact = true }},
		{"a challenger arm", func(r *run) { r.shadow = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := proxiedRun(t, gitRunIdentity(t), tc.edit)
			startForward(t, r)
			if !strings.Contains(r.forward.URL(), "127.0.0.1:") {
				t.Errorf("the forwarder listens on %q, want the loopback address", r.forward.URL())
			}
			// The close is the once-and-once the run's cleanup makes: a
			// second one is not a listener stopped under a request still in
			// flight.
			r.closeForward()
			r.closeForward()
		})
	}
}

// TestProxyFalseUnchanged is G9's other half: without the published block's
// proxy: true -- and without the installation's service on, or on a project
// job, or for a repository the block does not admit -- nothing about the run
// changes. The decision is admissionCheck's (proxiedBitbucket), and it is the
// one place it is made, so a table over it is the table over which runs are
// proxied.
func TestProxyFalseUnchanged(t *testing.T) {
	block := func(proxy bool, repos ...string) *projectpolicy.Policy {
		return &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: repos, Proxy: proxy}}
	}
	for _, tc := range []struct {
		name    string
		p       *projectpolicy.Policy
		found   bool
		project bool
		kind    string
		repo    string
		env     []string
		want    bool
	}{
		{name: "the block admits the repository with proxy: true", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}, want: true},
		{name: "a wildcard admits it", p: block(true, "bitbucket/acme/*"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}, want: true},
		{name: "proxy: false", p: block(false, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "no block", p: &projectpolicy.Policy{}, found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "no policy at all", found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "the node was not found", p: block(true, "bitbucket/acme/app"), kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "another repository", p: block(true, "bitbucket/acme/other"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "a project job", p: block(true, "bitbucket/acme/app"), found: true, project: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "another provider", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindGitHub, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "the installation has no service", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := proxiedRun(t, gitRunIdentity(t), func(r *run) { r.d.Env = tc.env; r.proxied = false })
			if got := r.proxiedBitbucket(tc.p, tc.found, tc.project, tc.kind, tc.repo); got != tc.want {
				t.Fatalf("proxiedBitbucket = %v, want %v", got, tc.want)
			}
		})
	}
	// And the run that is not proxied is the run that always was: no
	// forwarder, the remote the launcher wrote, the job's own token in the
	// git environment, and the provider Deps hands over.
	t.Run("the run itself", func(t *testing.T) {
		env := []string{providers.EnvBitbucketToken + "=" + proxyToken}
		r := svcRun(t, "api-key", func(r *run) {
			r.d.Env = env
			r.d.OpenProvider = providers.FromEnv(env, nil, nil)
			r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		})
		if err := r.startProxiedGit(context.Background()); err != nil {
			t.Fatalf("a run that is not proxied refused to start: %v", err)
		}
		if r.forward != nil {
			t.Fatal("a run that is not proxied started a forwarder")
		}
		const remote = "https://bitbucket.org/acme/app.git"
		if got := r.forwarderRemote(remote); got != remote {
			t.Errorf("forwarderRemote(%q) = %q, want it unchanged", remote, got)
		}
		if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the workflow"); err != nil {
			t.Fatalf("opening the provider of a run that is not proxied: %v", err)
		}
		if r.auth.Token != proxyToken {
			t.Errorf("the run's git token is %q, want the job's own Bitbucket token", r.auth.Token)
		}
		r.credURL = gitops.CredentialURL(remote)
		vars, err := r.authVars()
		if err != nil {
			t.Fatal(err)
		}
		if vars["FUGARO_GIT_TOKEN"] != proxyToken {
			t.Errorf("the git environment carries %q, want the job's own Bitbucket token", vars["FUGARO_GIT_TOKEN"])
		}
		if _, ok := vars["GIT_CONFIG_KEY_2"]; ok {
			t.Error("a run that is not proxied carries the proxied run's URL rewrite")
		}
		if want := "2"; vars["GIT_CONFIG_COUNT"] != want {
			t.Errorf("GIT_CONFIG_COUNT is %q, want %q (the helper's two entries alone)", vars["GIT_CONFIG_COUNT"], want)
		}
	})
}
