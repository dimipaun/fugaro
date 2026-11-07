package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The credentials service's switch (docs/credentials-service/default-on.md,
// DON8), through the two doors that read it.
//
// Every `fugaro update` writes the installation's credentials_service block
// (DON1), so the block's presence stopped being an opt-in the moment that
// shipped: the same run's apply reaches the repository roots, and the module
// drops a job's model mounts and its account's accessors for every root whose
// `installation.credentials_service` is set (modules/workflow/sa.tf). The
// service's bundle is empty on a fresh installation — `fugaro creds set` is its
// only writer — so a drop gated on the block alone would take every
// repository's model credential away, and a launch gated on it likewise would
// open the git provider keyless (internal/gitprov/providers/providers.go
// returns before it reads appKey), for a credential the service cannot hand
// back. That is the release blocker §1.4 states, and it is what the switch
// exists for: a repository is served only when the installation HAS the block,
// the record `fugaro update` wrote from the bundle says the service holds a
// model credential, and the repository's own agent.auth is one the service
// serves (infra.CredentialsServiceServes).
//
// The tests here are the CLI's half of that — what `fugaro update` records and
// what a launch is handed. The plan's half, which accessors and mounts a
// repository root gives up, is pinned where the spec is built
// (internal/infra/spec_test.go's TestRepoAddressesFollowTheSwitch,
// TestTheDeployAloneDropsNoModelMount and
// TestNoRepositoryRootGainsAServiceTheBundleCannotServe).

// TestTheSwitchTurnsOnForACascadeRepositoryWithAReadyBundle is the one row that
// is on, and it is asserted beside the two rows of its own conditions below
// (the record, the auth), so a launcher that answered with the URL for
// everything fails here.
func TestTheSwitchTurnsOnForACascadeRepositoryWithAReadyBundle(t *testing.T) {
	plain, _ := launchWithCredentialsService(t, cascadeLaunchYAML, launchCredsURL, true)
	want := envCredentialsServiceURL + "=" + launchCredsURL
	if len(plain) != 1 || plain[0] != want {
		t.Fatalf("the launch's plain Env = %v, want exactly [%s]: the installation has the service, the record vouches for its bundle and the repository is a cascade one — the three conditions of infra.CredentialsServiceServes — so the job is what the service is for\n", plain, want)
	}
}

// TestTheSwitchStaysOffForANonCascadeRepositoryWithAReadyBundle: a repository
// whose agent.auth is not the cascade keeps its own model credential, whatever
// the installation and the bundle say. The service has no route that would
// serve an `oauth` or `api-key` job one (internal/credsvc's own kinds), so a
// job told where the service is would lose the credential it runs on and get
// nothing back.
//
// The record is set and the block is there: this test is about the auth
// condition alone, so everything else the switch reads says yes.
func TestTheSwitchStaysOffForANonCascadeRepositoryWithAReadyBundle(t *testing.T) {
	for _, auth := range []string{"oauth", "api-key"} {
		var yaml string
		switch auth {
		case "oauth":
			yaml = oauthLaunchYAML
		default:
			yaml = strings.Replace(oauthLaunchYAML, "auth: oauth", "auth: "+auth, 1)
		}
		plain, secret := launchWithCredentialsService(t, yaml, launchCredsURL, true)
		for _, kv := range append(append([]string{}, plain...), secret...) {
			if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
				t.Errorf("a launch for an agent.auth %s repository of a converged installation carried %q: the service serves config.AuthCascade alone, so the job would drop the model credential it runs on for an address that has nothing to hand it", auth, kv)
			}
		}
	}
}

// TestAnOAuthInstallationStillLaunchesAfterUpdateAlone is the RED STEP of the
// switch, and the case docs/credentials-service/default-on.md §1.4 states as
// the release blocker: the state ONE `fugaro update` leaves a fresh
// installation in.
//
// The run deploys the service, so the block is on the machine — but nothing has
// ever been stored in its bundle (`fugaro creds set` is the only writer), so
// the record the same run writes says the service holds no model credential.
// The launch here is an oauth repository's, the common case, on exactly that
// installation: before the switch, the launcher read the block and put
// FUGARO_CREDENTIALS_SERVICE_URL on the job, which opens the GitHub provider
// keyless (internal/gitprov/providers/providers.go returns before it reads
// appKey) — so every run of every repository of an installation that had merely
// run its update lost the git credential it needs, with no fallback.
//
// Mutation (verified, run and restored): gate credentialsServiceLaunchEnv on
// the block alone (drop the CredentialsServiceServes call in internal/cli/
// run.go) and this fails with the variable set.
func TestAnOAuthInstallationStillLaunchesAfterUpdateAlone(t *testing.T) {
	plain, secret := launchWithCredentialsService(t, oauthLaunchYAML, launchCredsURL, false)
	if len(plain) != 0 {
		t.Fatalf("the launch's plain Env = %v, want none: the installation has the service but its bundle holds no model credential, and a job told where the service is opens its git provider keyless for a mint the service refuses\n", plain)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("the launch carried %q in its secret entries: an installation whose bundle the record does not vouch for must launch exactly what it launched before the service was deployed", kv)
		}
	}
}

// TestEditingTheAuthModeTakesEffectWithoutAnotherUpdate: the auth is read from
// the document the RUN will read and never from a copy stored when the record
// was written, so a repository that switches to the cascade — or away from it —
// is judged by the file it has now.
//
// It matters because `fugaro update` is the whole upgrade (CLAUDE.md's HARD
// RULE): an installation whose owner edits agent.auth must not need a second
// command before the change is honoured. One fixture, one local config and one
// record, two launches: the first on a checkout of the cascade, the second on a
// checkout of the same repository with agent.auth: oauth. Nothing between them
// writes the config.
//
// Mutation (verified, run and restored): store agent.auth on the record at the
// write and read it at the launch (pass the recorded auth into
// credentialsServiceLaunchEnv's third argument instead of the checkout's), and
// this fails on the second launch — the oauth checkout is still handed the
// service's address.
func TestEditingTheAuthModeTakesEffectWithoutAnotherUpdate(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	launch := func(yaml, runID string) []string {
		t.Helper()
		projectJobCheckoutYAML(t, yaml)
		env := memEnv(t, f.cloudFixture)
		rec := &envRecorder{Backend: env.be}
		env.be = rec
		if _, err := launchRun(context.Background(), env, appSlug, raceSpecAt(t, env, runID), time.Now()); err != nil {
			t.Fatal(err)
		}
		return rec.env
	}
	markRepoCredentialsServiceReady(t, f.cloudFixture)

	on := launch(cascadeLaunchYAML, "20261107-100000-ca5c")
	if want := envCredentialsServiceURL + "=" + launchCredsURL; len(on) != 1 || on[0] != want {
		t.Fatalf("the cascade checkout launched with %v, want [%s]: the fixture is the switch on, or the second launch below says nothing", on, want)
	}
	off := launch(oauthLaunchYAML, "20261107-100000-0a5c")
	for _, kv := range off {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Errorf("after the repository's fugaro.yaml was edited to agent.auth: oauth, the launch carried %q: the auth the switch is judged with is the one the run will read from that file, so an owner's edit takes effect without another update", kv)
		}
	}
	if off := readFileString(t, os.Getenv("FUGARO_CONFIG")); !strings.Contains(off, "credentials_service_ready: true") {
		t.Errorf("the local config no longer carries the record the fixture wrote, so the second launch's answer is not the auth's:\n%s", off)
	}
}

// switchRig is the rig the record's two tests write through: an installation
// whose local config carries the credentials service's block (so the run has a
// service to read a bundle of) and a real Secret Manager fake behind the
// endpoints the config already names.
func switchRig(t *testing.T) *initRig {
	t.Helper()
	r := newInitRig(t)
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	return r
}

// putSwitchBundle stores one version of the service's bundle document, as
// `fugaro creds set` writes it, and lets the fake hand the value back: its
// access call fails the test until a test allows it. A nil doc is the container
// with no version at all — the state a fresh deploy leaves, since nothing but
// that command ever writes one.
func putSwitchBundle(t *testing.T, r *initRig, doc []byte) {
	t.Helper()
	r.sm.Seed(credsvc.BundleSecretID(initProjectName), map[string]string{gcp.LabelManaged: gcp.ManagedValue}, doc)
	r.sm.AllowAccess()
}

// recordForRepo runs the repository stage's own write (initRun.writeRepoConfig,
// init.go:3671) over the rig's installation and returns the config it left: the
// ONE place the record is written, and therefore the place the bundle is read.
func recordForRepo(t *testing.T, r *initRig) *localcfg.Config {
	t.Helper()
	e := rigEngine(t, r, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(checkoutYAML("bitbucket", "oauth", "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	spec := infra.RepoSpec{Name: "acme/sandbox", Provider: "bitbucket", BaseBranch: "master"}
	if err := e.r.writeRepoConfig(t.Context(), e.lc, spec, cfg, r.cfg, e.old); err != nil {
		t.Fatalf("the repository stage's config write failed: %v", err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the project config does not parse: %v", err)
	}
	return lc
}

// TestTheSwitchIsOffUntilTheBundleHoldsAModelCredential is the readiness half
// of the switch at the one place the bundle is read: the record `fugaro update`
// writes for a repository.
//
// The bundle is the service's OWN loader's read — the same store `fugaro creds
// set` writes through, the same newest version and the same credsvc.ParseBundle
// the running service parses with — so a container with no version at all (a
// fresh deploy, this test's first half) and a document that parses with no
// model credential in it (its second half) are one answer: not ready. Anything
// else would switch a repository onto a service that has nothing to hand it.
//
// Mutation (verified, run and restored): answer credentialsBundleReady with
// true when the container exists (drop the ParseBundle read of the entries) and
// this fails on the git-only document.
func TestTheSwitchIsOffUntilTheBundleHoldsAModelCredential(t *testing.T) {
	// The fresh deploy: the container exists — the same apply that deploys the
	// service creates it — and no version has ever been written.
	r := switchRig(t)
	putSwitchBundle(t, r, nil)
	if got := recordForRepo(t, r).Repos["acme/sandbox"].CredentialsServiceReady; got {
		t.Errorf("the record says the service is ready for a bundle with no version at all: `fugaro creds set` is the bundle's only writer, so this is the state every installation is in the moment its update deploys the service")
	}

	// The bundle holds a credential, and it is not a model one: the git
	// credential the proxy forwards is a kind the cascade must never reach
	// (internal/credsvc's KindGit), so it buys the repositories nothing.
	git := switchRig(t)
	putSwitchBundle(t, git, []byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
	if got := recordForRepo(t, git).Repos["acme/sandbox"].CredentialsServiceReady; got {
		t.Errorf("the record says the service is ready for a bundle whose only entry is a git credential: the service serves model credentials (KindModel), and a job switched onto it would have nothing to run on")
	}

	// And a model credential is the yes: the record is what the plan and the
	// launch read, so this is the half that turns the switch on.
	model := switchRig(t)
	putSwitchBundle(t, model, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	if got := recordForRepo(t, model).Repos["acme/sandbox"].CredentialsServiceReady; !got {
		t.Errorf("the record does not say the service is ready for a bundle holding a model credential: no repository of this installation would ever be switched on")
	}
}

// TestTheRecordIsWrittenFalseWhenTheBundleIsEmptied is the other direction, and
// it is the fail-safe one: `fugaro creds set` can disable or replace the
// bundle, and an installation whose bundle no longer holds a model credential
// must put the repository back on its own mounts on the next run. A record
// written only when it is true would leave the repository switched on for ever,
// on a job whose mounts the module already dropped.
//
// The state is ASSERTED rather than assumed: the same rig writes the record
// twice, first over a bundle holding a model credential and then over the same
// container holding one that does not, so the second write is a change of a
// value that was there.
//
// Mutation (verified, run and restored): write the record only when
// credentialsBundleReady answers true (skip the write otherwise) and this fails
// on the second half, the record still saying true.
func TestTheRecordIsWrittenFalseWhenTheBundleIsEmptied(t *testing.T) {
	r := switchRig(t)
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	if got := recordForRepo(t, r).Repos["acme/sandbox"].CredentialsServiceReady; !got {
		t.Fatalf("the fixture is not the state this test starts from: the record does not say the bundle's model credential makes the service ready")
	}

	// The bundle emptied in the sense the service answers: a version that
	// parses and holds no model credential. The record is a value the run
	// writes like any other, so it goes back to false — which is what puts the
	// repository's own mounts back on the next apply.
	r.sm.Seed(credsvc.BundleSecretID(initProjectName), map[string]string{gcp.LabelManaged: gcp.ManagedValue},
		[]byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
	next := recordForRepo(t, r)
	if got := next.Repos["acme/sandbox"].CredentialsServiceReady; got {
		t.Errorf("the record still says the service is ready after the bundle stopped holding a model credential: every run of this repository would be launched against a service that cannot serve it, while its job's own model mounts stay dropped")
	}
	// The member is absent rather than `false` (localcfg.Repo's omitempty):
	// both decode as false, and the absent one is what keeps an installation
	// nothing is ready for byte for byte what it was before the release.
	if raw := readFileString(t, r.cfg); strings.Contains(raw, "credentials_service_ready") {
		t.Errorf("the local config still carries the key after the record went false:\n%s", raw)
	}
}
