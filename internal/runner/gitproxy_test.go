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
	"maps"
	"os"
	"path/filepath"
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
// proxy: true -- and without the installation's service on, or for a
// repository the block does not admit -- nothing about the run changes. The
// decision is admissionCheck's (proxiedBitbucket), and it is the one place it
// is made, so a table over it is the table over which runs are proxied.
//
// The job model is deliberately not a column: a shared project job of a
// Bitbucket profile is proxied like any other Bitbucket job (S3), and the two
// things that decide it are the block and the job's environment. What a
// project job changes is what a refusal means -- it holds no token to fall
// back to -- and that is TestSharedBitbucketJobHaltsWhenTheProxyRefusesARepository
// (shared_bitbucket_test.go).
func TestProxyFalseUnchanged(t *testing.T) {
	block := func(proxy bool, repos ...string) *projectpolicy.Policy {
		return &projectpolicy.Policy{BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: repos, Proxy: proxy}}
	}
	for _, tc := range []struct {
		name  string
		p     *projectpolicy.Policy
		found bool
		kind  string
		repo  string
		env   []string
		want  bool
	}{
		{name: "the block admits the repository with proxy: true", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}, want: true},
		{name: "a wildcard admits it", p: block(true, "bitbucket/acme/*"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}, want: true},
		{name: "proxy: false", p: block(false, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "no block", p: &projectpolicy.Policy{}, found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "no policy at all", found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "the node was not found", p: block(true, "bitbucket/acme/app"), kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "another repository", p: block(true, "bitbucket/acme/other"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "another provider", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindGitHub, repo: "acme/app", env: []string{providers.EnvCredentialsServiceURL + "=" + proxyService}},
		{name: "the installation has no service", p: block(true, "bitbucket/acme/app"), found: true, kind: gitprov.KindBitbucket, repo: "acme/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := proxiedRun(t, gitRunIdentity(t), func(r *run) { r.d.Env = tc.env; r.proxied = false })
			if got := r.proxiedBitbucket(tc.p, tc.found, tc.kind, tc.repo); got != tc.want {
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

// nestedOrigin is the URL a launcher writes for this repository: the clone's
// configured origin, and what a relative submodule URL is resolved against.
const nestedOrigin = "https://bitbucket.org/acme/app.git"

// nestedClone is the checkout a clone job's bootstrap has right after
// openCheckout: a blobless, --no-checkout clone of a remote whose main branch
// declares urls in .gitmodules (gitops' CloneBlobless, which openCheckout picks
// for a job with no baked checkout), with the configured origin the launcher's
// URL.
//
// The clone's own remote stays reachable through a url.<path>.insteadOf rule —
// the shape a proxied run's environment installs for its own forwarder
// (credentialVars) — so a checkout made from it below fetches what it checks
// out with no network, while everything this package reads and writes stays the
// launcher's URL. Nothing here writes a .gitmodules by hand: the file the route
// is read from is the one the checkout writes.
func nestedClone(t *testing.T, urls map[string]string) *gitops.Repo {
	t.Helper()
	testutil.IsolateGit(t)
	files := map[string]string{"README.md": "hello\n"}
	if len(urls) > 0 {
		var gm strings.Builder
		for _, name := range slices.Sorted(maps.Keys(urls)) {
			gm.WriteString("[submodule \"" + name + "\"]\n\tpath = " + name + "\n\turl = " + urls[name] + "\n")
		}
		files[".gitmodules"] = gm.String()
	}
	remote := testutil.NewRemote(t, files)
	repo, err := gitops.CloneBlobless(context.Background(), filepath.Join(t.TempDir(), "work"), remote, "main", gitops.IdentityEnv())
	if err != nil {
		t.Fatalf("cloning: %v", err)
	}
	testutil.Git(t, repo.Dir, "config", "url."+remote+".insteadOf", nestedOrigin)
	testutil.Git(t, repo.Dir, "remote", "set-url", "origin", nestedOrigin)
	return repo
}

// nestedRun is a proxied run over that clone, with allow as the published
// block's allow_repos and its forwarder started: the three things N2 joins —
// the checkout's own .gitmodules, this execution's forwarder and the policy the
// coverage question is answered from — are the real ones.
//
// Nothing is checked out yet, as nothing is on a clone job at that point in
// bootstrap: the tests below make the checkout themselves, in bootstrap's own
// order.
func nestedRun(t *testing.T, urls map[string]string, allow []string) (*run, *gitops.Repo) {
	t.Helper()
	repo := nestedClone(t, urls)
	r := proxiedRun(t, pushIdentity(t), func(r *run) {
		r.repo = repo
		r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
			Version:        projectpolicy.Version,
			BitbucketToken: &projectpolicy.BitbucketToken{AllowRepos: allow, Proxy: true},
		}}
	})
	startForward(t, r)
	return r, repo
}

// nestedCheckoutOut is the checkout the default arm makes (CheckoutNewBranch,
// which is where a --no-checkout clone gets its tree). It is what makes the
// .gitmodules the submodule half reads exist: a run that read the submodules
// before it was reading a file the clone had not written yet, which is the bug
// this fixture is here to catch.
func nestedCheckoutOut(t *testing.T, repo *gitops.Repo) {
	t.Helper()
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/"+svcRunID); err != nil {
		t.Fatalf("checking out the clone's branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".gitmodules")); err != nil {
		t.Fatalf("the checkout left no .gitmodules to read: %v", err)
	}
}

// TestSubmoduleURLsUseTheForwarder: a proxied run's nested fetches ride its
// own forwarder, in the checkout's configuration and never in the tracked
// file. Each URL is the endpoint for the repository the submodule *is* — a
// relative one resolved against the checkout's own remote, as git resolves it
// — because that is the repository the credentials service checks the block
// for.
//
// The tree is a clone job's: a --no-checkout clone, then the checkout
// CheckoutNewBranch makes, and the route is written after it, where bootstrap
// writes it. Written before the checkout instead, there is no .gitmodules in
// the working tree at all, and this test fails on the missing
// submodule.lib.url.
//
// Mutation (run, restored): drop the PointSubmoduleAt call from
// routeNestedFetches -- every submodule keeps the URL .gitmodules declares and
// this fails on it.
func TestSubmoduleURLsUseTheForwarder(t *testing.T) {
	r, repo := nestedRun(t, map[string]string{
		"lib":        "../lib.git",
		"vendor/dep": "https://bitbucket.org/acme/dep.git",
	}, []string{"bitbucket/acme/*"})
	ctx := context.Background()
	if err := r.routeLFSEndpoint(ctx); err != nil {
		t.Fatalf("pointing the LFS endpoint before the checkout: %v", err)
	}
	nestedCheckoutOut(t, repo)
	if err := r.routeNestedFetches(ctx); err != nil {
		t.Fatalf("routing the nested fetches of a checkout the block covers: %v", err)
	}
	endpoint := strings.TrimSuffix(r.forward.URL(), "/")
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		t.Fatalf("the endpoint is %q, want this execution's loopback listener", endpoint)
	}
	for name, want := range map[string]string{
		"lib":        endpoint + "/acme/lib.git",
		"vendor/dep": endpoint + "/acme/dep.git",
	} {
		if got := testutil.Git(t, repo.Dir, "config", "--get", "submodule."+name+".url"); got != want {
			t.Errorf("submodule.%s.url = %q, want %q", name, got, want)
		}
	}
	// The origin stays the URL the launcher wrote: the route is this
	// execution's, and an endpoint written into the checkout outlives the
	// listener it names.
	if got := testutil.Git(t, repo.Dir, "config", "--get", "remote.origin.url"); got != nestedOrigin {
		t.Errorf("the checkout's origin is %q, want the launcher's remote", got)
	}
	// And none of it is a change the run would commit.
	if got := testutil.Git(t, repo.Dir, "status", "--porcelain"); got != "" {
		t.Errorf("routing the nested fetches changed the checkout: git status reports %q", got)
	}
}

// TestLFSEndpointUsesTheForwarder: git-lfs reads lfs.url in preference to the
// endpoint it derives from the remote, so pointing it at this execution's
// forwarder is what puts an LFS object's fetch on the same narrowed route as
// the run's own. The URL rewrite in credentialVars would not do it -- lfs.url
// is a URL git-lfs composes the batch request from, not one the run's git
// opens.
//
// It is written before the checkout, and the clone has no tree yet: the
// checkout is itself a fetch, and the smudge filters the image installed run
// over the files it writes.
//
// Mutation (run, restored): drop the PointLFSAt call from routeLFSEndpoint --
// no lfs.url is written, git-lfs derives bitbucket.org from the remote again,
// and this fails on the empty read.
func TestLFSEndpointUsesTheForwarder(t *testing.T) {
	r, repo := nestedRun(t, nil, []string{"bitbucket/acme/*"})
	// The clone is --no-checkout: a file of the revision is not in the working
	// tree until something checks it out, which is why the endpoint cannot wait
	// for the checkout either.
	if _, err := os.Stat(filepath.Join(repo.Dir, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("the clone already has its tree: %v", err)
	}
	if err := r.routeLFSEndpoint(context.Background()); err != nil {
		t.Fatalf("pointing the LFS endpoint of a checkout the block covers: %v", err)
	}
	want := strings.TrimSuffix(r.forward.URL(), "/") + "/acme/app.git/info/lfs"
	if got := testutil.Git(t, repo.Dir, "config", "--get", "lfs.url"); got != want {
		t.Errorf("lfs.url = %q, want the forwarder's endpoint %q", got, want)
	}
}

// TestUncoveredSubmoduleNamesTheRepo: a submodule whose repository the
// published block does not cover stops the run before the agent starts, and
// the refusal names it the way the policy spells it, with the one command that
// widens the list. It is the run's own sentence rather than the service's
// class (G7): nothing has been fetched yet, and the operator's fix is the same
// list the service would have refused on.
//
// It reads the checkout the run makes, and the checkout has to be there: the
// same call before it finds no .gitmodules, no submodule at all, and no
// refusal (measured: routing first makes this fail on err = nil while the
// other test fails on the missing submodule.lib.url).
//
// Mutation (run, restored): answer the coverage question with the run's own
// repository, or skip it -- the submodule is routed, the service refuses it
// one stage later, and this fails on err = nil.
func TestUncoveredSubmoduleNamesTheRepo(t *testing.T) {
	r, repo := nestedRun(t, map[string]string{"lib": "../lib.git"}, []string{"bitbucket/acme/app"})
	ctx := context.Background()
	if err := r.routeLFSEndpoint(ctx); err != nil {
		t.Fatalf("pointing the LFS endpoint: %v", err)
	}
	nestedCheckoutOut(t, repo)
	err := r.routeNestedFetches(ctx)
	if err == nil {
		t.Fatal("a submodule outside the run's Bitbucket access was routed to the forwarder")
	}
	for _, want := range []string{
		"the submodule lib",
		"bitbucket/acme/lib",
		"fugaro secrets allow " + config.BitbucketSecret + " acme/lib",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// No submodule route was written: the run stopped on the first submodule
	// it read. (The LFS endpoint is the earlier half, written before the
	// checkout, and is not what this refusal is about.)
	if got := testutil.Git(t, repo.Dir, "config", "--list"); strings.Contains(got, "submodule.") {
		t.Errorf("a run that refused the checkout's submodules wrote a route into it:\n%s", got)
	}
}

// TestForeignSubmoduleIsLeftUnrouted: a submodule on another host -- a public
// GitHub one inside a Bitbucket repository, which is a shape repositories do
// carry -- is left exactly as .gitmodules declares it, and the run goes on.
// The insteadOf rewrite the run's environment carries names this execution's
// forwarder for bitbucket.org alone, so git fetches that submodule anonymously,
// exactly as it would without this run; refusing it instead would stop every
// proxied run of a repository that declares one, which the plan's acceptance
// does not ask for (it owes a refusal for a Bitbucket repository the run's
// access does not cover, and that is the sibling below).
//
// Mutation (run, restored): refuse a remote whose host is not bitbucket.org's
// in nestedEndpoint -- the run stops on this repository's submodule and this
// fails on the refusal.
func TestForeignSubmoduleIsLeftUnrouted(t *testing.T) {
	r, repo := nestedRun(t, map[string]string{
		"lib":     "../lib.git",
		"foreign": "https://github.com/other/thing.git",
	}, []string{"bitbucket/acme/*"})
	ctx := context.Background()
	if err := r.routeLFSEndpoint(ctx); err != nil {
		t.Fatalf("pointing the LFS endpoint: %v", err)
	}
	nestedCheckoutOut(t, repo)
	if err := r.routeNestedFetches(ctx); err != nil {
		t.Fatalf("routing the nested fetches of a checkout carrying another host's submodule: %v", err)
	}
	// The Bitbucket sibling is routed, so a run that routed nothing at all is
	// not what this passes on.
	want := strings.TrimSuffix(r.forward.URL(), "/") + "/acme/lib.git"
	if got := testutil.Git(t, repo.Dir, "config", "--get", "submodule.lib.url"); got != want {
		t.Errorf("submodule.lib.url = %q, want the forwarder's endpoint %q", got, want)
	}
	// And the foreign one is not: no URL of this execution's is written for it,
	// which is what leaves git fetching it from github.com as the repository
	// declared.
	if got := testutil.Git(t, repo.Dir, "config", "--list"); strings.Contains(got, "submodule.foreign.url") {
		t.Errorf("a submodule on another host was routed:\n%s", got)
	}
}

// TestLFSEndpointStaysOffWithoutAForwarder: a run with no forwarder -- every
// per-repository job, which holds its own Bitbucket token -- points nothing at
// all. No lfs.url is written, so git-lfs keeps deriving the endpoint the job's
// own configuration gives it, and the filters a `git lfs install` left in the
// image stay exactly as they are: turning them off for every run that is not
// proxied would break the LFS content of the runs that are not.
//
// Mutation (run, restored): drop the r.forward == nil arm of routedOrigin --
// this run writes routes to a listener that does not exist, and the policy it
// has no answer for (this fixture's read is "not found") stops it on a refusal
// that is not about the run.
func TestLFSEndpointStaysOffWithoutAForwarder(t *testing.T) {
	repo := nestedClone(t, map[string]string{"lib": "../lib.git"})
	r := proxiedRun(t, pushIdentity(t), func(r *run) {
		r.repo = repo
		// A read that found nothing: a run that is not proxied needs no
		// policy, and must not reach for one to route nothing.
		r.projPolicy = policyRead{done: true}
	})
	if r.forward != nil {
		t.Fatal("the fixture started a forwarder: this is the arm without one")
	}
	ctx := context.Background()
	if err := r.routeLFSEndpoint(ctx); err != nil {
		t.Fatalf("pointing the LFS endpoint of a run with no forwarder: %v", err)
	}
	nestedCheckoutOut(t, repo)
	if err := r.routeNestedFetches(ctx); err != nil {
		t.Fatalf("routing the nested fetches of a run with no forwarder: %v", err)
	}
	if got := testutil.Git(t, repo.Dir, "config", "--list"); strings.Contains(got, "submodule.") || strings.Contains(got, "lfs.url") {
		t.Errorf("a run with no forwarder wrote a route into its checkout:\n%s", got)
	}
}

// TestPushBindingReadsTheCloneNotItsEnvironment: the push binding holds a fresh
// clone (openCheckout, CF1, F39), and what it reads is the checkout's own
// configuration and not the environment the clone was made with. That
// environment carries this run's own route to the forwarder -- every
// bitbucket.org URL rewritten onto this execution's loopback listener
// (credentialVars) -- and git applies that rewrite to the origin it answers
// with: read through it, a fresh clone of a proxied run's own repository answers
// with an http endpoint, which the admission refuses, so no proxied run that has
// to clone can bootstrap at all (the review finding on this change).
//
// Real git, a proxied run's own forwarder and the run's own credentialVars,
// because what the rewrite does to `git remote get-url origin` is the whole
// claim: the two reads below differ in that environment and in nothing else.
//
// Mutation (run, restored): hand the clone itself (strip(repo)) to
// checkBakedCheckoutOrigin in place of heldCheckout. The refusal the third check
// below asserts against comes back instead of nil and the run stops before it
// clones anything.
func TestPushBindingReadsTheCloneNotItsEnvironment(t *testing.T) {
	r := admitProjectProxyRun(t, sharedBitbucketRun(t, nil), "")
	startSharedBitbucketJob(t, r)
	ctx := context.Background()
	remote, err := r.cloneURL(ctx)
	if err != nil {
		t.Fatalf("the remote a proxied run clones from: %v", err)
	}
	vars, err := r.authVars()
	if err != nil {
		t.Fatalf("building the git environment of a proxied run: %v", err)
	}
	slug, err := task.Slug(gitprov.KindBitbucket, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	testutil.Git(t, dir, "remote", "add", "origin", remote)
	// The clone openCheckout opens: a checkout of the launcher's remote, opened
	// on the environment the clone was made with (the run's own vars, the
	// identity included).
	clone, err := gitops.Open(dir, gitops.WithVars(gitops.IdentityEnvFor(r.identity), vars))
	if err != nil {
		t.Fatalf("opening the fresh clone: %v", err)
	}
	throughTheEnv, err := clone.OriginURL(ctx)
	if err != nil {
		t.Fatalf("reading the clone's origin through the environment it was cloned with: %v", err)
	}
	if !strings.Contains(throughTheEnv, "127.0.0.1") {
		t.Fatalf("the clone's origin through its own environment is %q, want the forwarder this run's vars route it to: this test's premise is gone", throughTheEnv)
	}
	if err := remoteAdmissionErr(throughTheEnv, gitprov.KindBitbucket, "acme/app", slug); err == nil {
		t.Fatal("the run's admission admits the forwarder's own endpoint: this test's premise is gone")
	}
	held, err := r.heldCheckout(clone)
	if err != nil {
		t.Fatalf("opening the fresh clone on its own configuration: %v", err)
	}
	if err := r.checkBakedCheckoutOrigin(ctx, gitprov.KindBitbucket, held); err != nil {
		t.Fatalf("the binding refused a fresh clone of this run's own repository: %v", err)
	}
}
