package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// launchCredsURL is the URL an opted-in installation's config carries in
// these tests: the shape Cloud Run assigns (https, a host, no path), which is
// what localcfg's own validation admits.
const launchCredsURL = "https://fugaro-credentials-abcde-uc.a.run.app"

// launchWithCredentialsService launches the fixture's run with lc as the
// installation's config and returns what the backend was handed. It is the
// one place the two subtests below differ.
func launchWithCredentialsService(t *testing.T, lc *localcfg.Config) (plain, secret []string) {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	if lc != nil {
		f.appendConfig(t, "credentials_service:\n  url: "+lc.CredentialsService.URL+"\n")
	}
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		t.Fatal(err)
	}
	return rec.env, rec.secretEnv
}

// TestTheCredentialsServiceURLReachesTheJobEnv: an installation that opted in
// hands the job its service's URL, under the one name the runner and the
// keyless GitHub path read, and that is the whole of the wiring C1.24 is
// (docs/credentials-service, D146/D147). Without it the cascade upstream
// refuses the run at bootstrap however healthy the service is.
//
// The entry is plain and not secret, deliberately: the URL is the service's
// public address and the audience every caller's ID token is minted for, the
// service's own container carries it in plain environment under the same name
// (credentials_serve.go's envCredentialsServiceURL), and Batch refuses
// SecretEnv outright — so a secret entry would make the variable depend on
// which backend a project happens to use.
//
// Mutation (verified, run and restored): drop the lspec.Env line in
// internal/cli/run.go's launch — this fails with an empty plain list.
func TestTheCredentialsServiceURLReachesTheJobEnv(t *testing.T) {
	lc := &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}}
	plain, secret := launchWithCredentialsService(t, lc)
	want := envCredentialsServiceURL + "=" + launchCredsURL
	if len(plain) != 1 || plain[0] != want {
		t.Fatalf("the launch's plain Env = %v, want exactly [%s]", plain, want)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("the credentials service's URL is in the launch's SECRET entries (%q): it is not a secret — it is the service's public address, and Batch refuses a secret entry outright, so an installation on that backend would lose the variable or fail to launch (backend.LaunchSpec.SecretEnv)", kv)
		}
	}
}

// TestAnInstallationWithoutTheServiceSetsNoCredentialsServiceVariable: the
// absence of the block is the opt-in's off position, and off is byte for byte
// what this tree launched before the variable existed — no entry, no empty
// value, nothing for a backend to merge into the job's environment. Every
// installation is in this state until an owner runs `fugaro setup
// --provision-credentials`, so a launcher that put an empty value here would
// change every run in the world; a launcher that put a value here would make
// every job believe it has a credentials service to call.
func TestAnInstallationWithoutTheServiceSetsNoCredentialsServiceVariable(t *testing.T) {
	plain, secret := launchWithCredentialsService(t, nil)
	if len(plain) != 0 {
		t.Fatalf("an installation with no credentials service launched with the plain Env %v, want none: the block's presence is the opt-in, and an installation that has not opted in must launch exactly what it launched before this variable existed", plain)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("an installation with no credentials service launched with %q in its secret entries", kv)
		}
	}
}

// TestCredentialsServiceLaunchEnv: the helper's own edges. A nil config, a
// config with no block and a block with no URL are all "off" — the loader
// refuses the last of those (localcfg's validCredentialsServiceURL), so it can
// only be reached by a Config built in Go, and reading it as "off" is the
// fail-closed direction: the job gets no address rather than an empty one.
//
// A SHARED PROJECT JOB is off whatever the config says, and that is the third
// argument's whole job: the service refuses an account that belongs to one
// (internal/credsvc/auth.go's verifyJob, Global Constraint 6, C3.9), so the
// address would buy such a job nothing — and it would cost it its git
// credentials, because the same variable is what opens the provider keyless
// (internal/gitprov/providers/providers.go) and the mint that replaces the
// key it still mounts would be refused. The shared jobs keep their own
// credentials until C3.9 moves them.
func TestCredentialsServiceLaunchEnv(t *testing.T) {
	optedIn := &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}}
	for name, lc := range map[string]*localcfg.Config{
		"no config":       nil,
		"no block":        {},
		"a block, no url": {CredentialsService: &localcfg.CredentialsService{}},
	} {
		if got := credentialsServiceLaunchEnv(lc, ""); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil", name, got)
		}
	}
	got := credentialsServiceLaunchEnv(optedIn, "")
	if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
		t.Errorf("credentialsServiceLaunchEnv = %v, want [%s=%s]", got, envCredentialsServiceURL, launchCredsURL)
	}
	for _, lc := range []*localcfg.Config{optedIn, nil} {
		if got := credentialsServiceLaunchEnv(lc, "fugaro-standard-github"); got != nil {
			t.Errorf("a launch on the shared project job put %v in the execution's plain Env, want none: the credentials service refuses a shared project job's account whole (internal/credsvc/auth.go's verifyJob), and telling that job where the service is would open its git provider keyless (internal/gitprov/providers/providers.go) for a mint the service refuses — every GitHub run of such a job would break", got)
		}
	}
}

// TestASharedProjectJobIsNotToldAboutTheCredentialsService is the same rule
// through the launch itself, because the helper's third argument is the
// launcher's to get right and a helper test cannot see the call site: this is
// the arm that would break an opted-in installation's shared project jobs.
//
// The fixture is a real shared-job launch (projectJobFixture: the profile, the
// published policy, the job the installation deployed) with the opt-in on, and
// the checkout selects the clone workflow that resolves it — asserted, so the
// test cannot pass by launching the per-repository job instead.
//
// Mutation (verified, run and restored): pass "" for job in run.go's launch
// call — this fails on the credentials entry, and
// TestTheCredentialsServiceURLReachesTheJobEnv still passes, which is why the
// two exist together.
func TestASharedProjectJobIsNotToldAboutTheCredentialsService(t *testing.T) {
	f := newProjectJobFixture(t)
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	projectJobCheckoutYAML(t, projectJobRepoYAML)
	env := memEnv(t, f.cloudFixture)
	rec := &envRecorder{Backend: env.be}
	env.be = rec
	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != f.job {
		t.Fatalf("the launch made %d jobs.run calls (%+v), want one on the shared job %s: without that this test says nothing about a shared project job", len(calls), calls, f.job)
	}
	for _, kv := range append(append([]string{}, rec.env...), rec.secretEnv...) {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("a launch on the shared project job %s carried %q: the service refuses a shared project job's account whole, so this entry is an address such a job can never use — and it is what opens the git provider keyless, for a mint the service refuses", f.job, kv)
		}
	}
}
