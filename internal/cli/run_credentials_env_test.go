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
func TestCredentialsServiceLaunchEnv(t *testing.T) {
	for name, lc := range map[string]*localcfg.Config{
		"no config":       nil,
		"no block":        {},
		"a block, no url": {CredentialsService: &localcfg.CredentialsService{}},
	} {
		if got := credentialsServiceLaunchEnv(lc); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil", name, got)
		}
	}
	got := credentialsServiceLaunchEnv(&localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}})
	if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
		t.Errorf("credentialsServiceLaunchEnv = %v, want [%s=%s]", got, envCredentialsServiceURL, launchCredsURL)
	}
}
