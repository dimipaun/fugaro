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
// remote the clone, every fetch and the push go to (proxiedRemote, and the
// admission that has to accept it when a retried execution finds the checkout
// the first one made) -- rather than running a whole run, for the reason
// git_service_internal_test.go gives for its own half: the run's identity is
// the budget session's token and not an environment value, so the fixtures
// build it the way the run's own policy read does (gitRunIdentity).

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
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
	r.credURL = gitops.CredentialURL(r.proxiedRemote("https://bitbucket.org/acme/app.git"))
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

// TestProxiedRemoteRewrite: a proxied run clones, fetches and pushes through
// its own forwarder, and it is the run that writes that remote rather than the
// launcher (proxiedRemote). So the endpoint it writes has to be one its own
// admission accepts: a retried execution of the same job finds the checkout
// the first execution made, whose origin is the endpoint, and the check that
// binds a remote to this run's repository would otherwise refuse it.
//
// The endpoint is admitted for this run's repository and no other, and only
// for a proxied run: it is a remote the runner itself wrote, and everything
// else about the check is unchanged.
func TestProxiedRemoteRewrite(t *testing.T) {
	r := proxiedRun(t, pushIdentity(t), nil)
	startForward(t, r)
	const launcherRemote = "https://bitbucket.org/acme/app.git"
	got := r.proxiedRemote(launcherRemote)
	if want := strings.TrimSuffix(r.forward.URL(), "/") + "/acme/app.git"; got != want {
		t.Fatalf("the proxied remote is %q, want %q", got, want)
	}
	if strings.Contains(got, "bitbucket.org") {
		t.Errorf("the proxied remote names Bitbucket itself (%q): the run talks to the forwarder, and to Bitbucket only through the service", got)
	}
	slug, err := task.Slug(gitprov.KindBitbucket, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.remoteAdmissionErr(got, gitprov.KindBitbucket, "acme/app", slug); err != nil {
		t.Errorf("the run's own forwarder endpoint was refused by its own admission: %v", err)
	}
	other := strings.TrimSuffix(r.forward.URL(), "/") + "/acme/other.git"
	if err := r.remoteAdmissionErr(other, gitprov.KindBitbucket, "acme/app", slug); err == nil {
		t.Error("the endpoint naming another repository was admitted: the endpoint is the runner's, and the repository it names is still the launcher's to bind")
	}
	// A run that is not proxied has no such endpoint, and the admission says
	// so: the rewrite is the proxied run's own, and nothing else may hand it
	// in.
	plain := proxiedRun(t, gitRunIdentity(t), func(r *run) { r.proxied = false })
	if err := plain.remoteAdmissionErr(got, gitprov.KindBitbucket, "acme/app", slug); err == nil {
		t.Error("a run that is not proxied admitted the forwarder's endpoint")
	}
	if plain.proxiedRemote(launcherRemote) != launcherRemote {
		t.Error("a run that is not proxied rewrote its remote")
	}
}

// TestForwarderRemoteRepoUnreadable: forwarderRemoteRepo reads the endpoint
// and only the endpoint -- an http URL on a loopback address, under the
// proxy route's prefix, naming an owner/name. Everything else is somebody
// else's remote, whatever it looks like.
func TestForwarderRemoteRepoUnreadable(t *testing.T) {
	for _, tc := range []struct{ name, remote string }{
		{"Bitbucket itself", "https://bitbucket.org/acme/app.git"},
		{"a loopback host that is not this route", "http://127.0.0.1:9/acme/app.git"},
		{"a route with no repository", "http://127.0.0.1:9" + gateway.GitProxyPrefix},
		{"a route with only an owner", "http://127.0.0.1:9" + gateway.GitProxyPrefix + "acme"},
		{"a remote that is not a URL", "git@bitbucket.org:acme/app.git"},
		{"a host that merely begins with 127.0.0.1", "http://127.0.0.1.evil.example:9" + gateway.GitProxyPrefix + "acme/app.git"},
	} {
		if named, ok := forwarderRemoteRepo(tc.remote); ok {
			t.Errorf("%s (%q) was read as the forwarder's endpoint for %q", tc.name, tc.remote, named)
		}
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
	for _, want := range []string{"acme/app", "proxy: true", proxiedSince, "names none"} {
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
// artifact run, whose deliverable goes to the runs bucket.
func TestProxiedReviewRunNeedsNoBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*run)
	}{
		{"review", func(r *run) { r.review = true }},
		{"artifact", func(r *run) { r.artifact = true }},
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
		if got := r.proxiedRemote(remote); got != remote {
			t.Errorf("proxiedRemote(%q) = %q, want it unchanged", remote, got)
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
