package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// launchCredsURL is the URL an opted-in installation's config carries in
// these tests: the shape Cloud Run assigns (https, a host, no path), which is
// what localcfg's own validation admits.
const launchCredsURL = "https://fugaro-credentials-abcde-uc.a.run.app"

// markRepoCredentialsServiceReady writes the record `fugaro update` leaves on
// a repository's local-config entry (init.go's writeRepoConfig) into the
// fixture's config: the half of the switch that only an apply can answer
// (infra.CredentialsServiceServes) — this repository's root was applied with
// the service on — where the block and the live agent.auth are read where the
// launch is. The fixture's repos block names acme/app with no record, which is
// what a machine that never ran the update holds.
func markRepoCredentialsServiceReady(t *testing.T, f *cloudFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const repo = "  acme/app: { provider: github, base_branch: main, workflows: [web]"
	if !strings.Contains(string(data), repo) {
		t.Fatalf("the fixture's local config no longer holds %q:\n%s", repo, data)
	}
	ready := strings.Replace(string(data), repo, repo+", credentials_service_ready: true", 1)
	if err := os.WriteFile(path, []byte(ready), 0o600); err != nil {
		t.Fatal(err)
	}
}

// oauthLaunchYAML is cascadeLaunchYAML's shape for a repository the service
// cannot serve: the same workflow, git provider and project, with agent.auth
// `oauth`. The auth, and nothing else about the repository, is what the switch
// reads (infra.CredentialsServiceServes), so the pair is what a test about that
// one condition needs.
const oauthLaunchYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: oauth
workflows:
  web: { commands: { build: sh build.sh, test: sh test.sh } }
`

// launchWithCredentialsService launches the fixture's run for a checkout
// carrying yaml against an installation whose local config carries the
// service's block with url (empty for an installation that never deployed it,
// where the block is absent altogether) and the per-repository record when
// ready is true, and returns what the backend was handed.
//
// The checkout is the test's own, and that is the switch's third condition: it
// is the repository's agent.auth, read from the document the run will read —
// the ref's fugaro.yaml, else this machine's checkout — and never from the
// record, so a repository this launcher cannot read at all is a repository it
// cannot switch on.
func launchWithCredentialsService(t *testing.T, yaml, url string, ready bool) (plain, secret []string) {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	if url != "" {
		f.appendConfig(t, "credentials_service:\n  url: "+url+"\n")
	}
	projectJobCheckoutYAML(t, yaml)
	if ready {
		markRepoCredentialsServiceReady(t, f.cloudFixture)
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
	plain, secret := launchWithCredentialsService(t, cascadeLaunchYAML, launchCredsURL, true)
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
// absence of the block is the off position, and off is byte for byte what this
// tree launched before the variable existed — no entry, no empty value, nothing
// for a backend to merge into the job's environment. Every `fugaro update`
// writes the block now (docs/credentials-service/default-on.md), so the
// installations in this state are the ones a run could not deploy it on: a
// build from a checkout, which has no released image to pin, and a config with
// no Firebase backend for the service to read. A launcher that put an empty
// value here would change those runs for no reason; a launcher that put a value
// here would make every job believe it has a credentials service to call.
func TestAnInstallationWithoutTheServiceSetsNoCredentialsServiceVariable(t *testing.T) {
	plain, secret := launchWithCredentialsService(t, cascadeLaunchYAML, "", false)
	if len(plain) != 0 {
		t.Fatalf("an installation with no credentials service launched with the plain Env %v, want none: the block's presence is the opt-in, and an installation that has not opted in must launch exactly what it launched before this variable existed", plain)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("an installation with no credentials service launched with %q in its secret entries", kv)
		}
	}
}

// TestACascadeRepositoryAppliedHeldOffIsNotHandedTheService is the launch's
// half of the oauth→cascade direction (update_switch_test.go's
// TestTheRecordFollowsTheAuthTheRunsRead is the record's), and it is the state
// the record's writer left behind until the review of 2026-11-07: the block is
// there, the checkout is a cascade one, and the entry carries NO record because
// the only apply of that root was made while the document still said oauth —
// the natural adoption order, the cascade committed afterwards.
//
// The launch reads the auth live (TestEditingTheAuthModeTakesEffectWithout-
// AnotherUpdate pins that it must), so the record is the whole of what stands
// between this launch and a job handed an address whose invoker grant no apply
// ever made: the run's provider call opens GitHub keyless and the service
// refuses the mint, and a `cascade` run has nothing of its own to fall back to
// (D143). Nothing is handed until a run of its own applies the root with the
// switch on, which is what records true.
func TestACascadeRepositoryAppliedHeldOffIsNotHandedTheService(t *testing.T) {
	plain, secret := launchWithCredentialsService(t, cascadeLaunchYAML, launchCredsURL, false)
	if len(plain) != 0 {
		t.Fatalf("a cascade repository whose entry carries no record launched with the plain Env %v, want none: the record is the half of the switch only an apply of that root can answer, so a launch that filled it in from what it can see would hand the job an address whose roles/run.invoker grant no apply made — the job opens its git provider keyless for a mint the service refuses\n", plain)
	}
	for _, kv := range secret {
		if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
			t.Fatalf("the launch carried %q in its secret entries: an installation whose bundle no run has recorded must launch exactly what it launched before the service was deployed", kv)
		}
	}
}

// TestCredentialsServiceLaunchEnv: the helper's own edges. A nil config, a
// config with no block and a block with no URL are all "off" — the loader
// refuses the last of those (localcfg's validCredentialsServiceURL), so it can
// only be reached by a Config built in Go, and reading it as "off" is the
// fail-closed direction: the job gets no address rather than an empty one.
//
// The switch's own two refusals are here beside them, because they are the
// whole of what the helper answers with this release
// (infra.CredentialsServiceServes): a block whose bundle the record does not
// vouch for, and a repository whose agent.auth the service cannot serve. Both
// hand the job nothing, which is the state every repository of a converged
// installation is in until an operator stores a credential in the bundle —
// and, for an `oauth` or `api-key` repository, the state it stays in, because
// the service has no route that would serve it one.
//
// A SHARED PROJECT JOB is off whatever the config says, and that is the job
// argument's whole job: the service refuses an account that belongs to one
// (internal/credsvc/auth.go's verifyJob, Global Constraint 6, C3.9), so the
// address would buy such a job nothing — and it would cost it its git
// credentials, because the same variable is what opens the provider keyless
// (internal/gitprov/providers/providers.go) and the mint that replaces the
// key it still mounts would be refused. The shared jobs keep their own
// credentials until C3.9 moves them.
//
// The last row is the DEPLOYED ROOT's own answer, and it is the fifth
// argument's whole job: an empty auth is a document the launcher could not read
// (a resolved config always defaults agent.auth, so the empty string never
// comes from a file), and the repository root's own last apply is what the job
// runs under then — the tfvars that apply was built from, read at the call site
// by deployedRepoCredentialsService. It replaces the record and the auth
// together, so a root applied with the service off hands nothing even over a
// record that says ready, and a root applied with it on hands the address over
// a record that says nothing — which is the state an installation that opted in
// under an earlier release is in until `fugaro update` backfills it.
func TestCredentialsServiceLaunchEnv(t *testing.T) {
	optedIn := &localcfg.Config{
		CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL},
		Repos:              map[string]localcfg.Repo{"acme/app": {CredentialsServiceReady: localcfg.RecordSwitch(true)}},
	}
	for name, lc := range map[string]*localcfg.Config{
		"no config":       nil,
		"no block":        {},
		"a block, no url": {CredentialsService: &localcfg.CredentialsService{}},
	} {
		if got := credentialsServiceLaunchEnv(lc, "", "acme/app", config.AuthCascade, false); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil", name, got)
		}
	}
	got := credentialsServiceLaunchEnv(optedIn, "", "acme/app", config.AuthCascade, false)
	if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
		t.Errorf("credentialsServiceLaunchEnv = %v, want [%s=%s]", got, envCredentialsServiceURL, launchCredsURL)
	}
	for name, tc := range map[string]struct {
		lc       *localcfg.Config
		auth     string
		deployed bool
	}{
		"no record yet": {
			lc:   &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
			auth: config.AuthCascade,
		},
		"a repository the service cannot serve":    {lc: optedIn, auth: "oauth"},
		"no document, and no root of its own here": {lc: optedIn},
		"no document, and the root carries no service": {
			lc:       &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
			deployed: false,
		},
		"no service to hand": {lc: &localcfg.Config{}, deployed: true},
	} {
		if got := credentialsServiceLaunchEnv(tc.lc, "", "acme/app", tc.auth, tc.deployed); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil: the switch is the plan's own predicate (infra.CredentialsServiceServes), and a job told where the service is opens its git provider keyless (internal/gitprov/providers/providers.go) for a credential the service cannot hand back", name, got)
		}
	}
	// The deployed root's own answer, both ways: with no document to read, a
	// root whose tfvars carry the service hands the address — with the record
	// empty, which is the upgrade's state, and with it already written, which
	// is an ordinary launch from outside a checkout — and withholding it there
	// starts a run whose job mounts no model credential and halts (D143).
	for name, lc := range map[string]*localcfg.Config{
		"a root applied with the service on, no record":    {CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
		"a root applied with the service on, and recorded": optedIn,
	} {
		got := credentialsServiceLaunchEnv(lc, "", "acme/app", "", true)
		if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want [%s=%s]", name, got, envCredentialsServiceURL, launchCredsURL)
		}
	}
	for _, lc := range []*localcfg.Config{optedIn, nil} {
		if got := credentialsServiceLaunchEnv(lc, "fugaro-standard-github", "acme/app", config.AuthCascade, false); got != nil {
			t.Errorf("a launch on the shared project job put %v in the execution's plain Env, want none: the credentials service refuses a shared project job's account whole (internal/credsvc/auth.go's verifyJob), and telling that job where the service is would open its git provider keyless (internal/gitprov/providers/providers.go) for a mint the service refuses — every GitHub run of such a job would break", got)
		}
	}
}

// seedRepoRootCredentialsService writes the repository root's tfvars — the
// document the repository door leaves in this machine's workdir for it — the
// way that door writes it for a root it deploys with the switch on:
// installation.credentials_service present and not null. The URL is left out
// deliberately, as the repository root's own copy leaves it
// (infra.RepoCredentialsService), because nothing about the ROOT's answer
// depends on it. update_switch_test.go's repoCredentialsServiceFact reads the
// same file back out of a real `fugaro update --repo`.
func seedRepoRootCredentialsService(t *testing.T, slug string) {
	t.Helper()
	seedRepoRootVars(t, slug, `{"installation":{"credentials_service":{"name":"fugaro-credentials","region":"us-east5"}}}`)
}

// seedRepoRootVars writes a repository root's tfvars, verbatim, where the last
// apply of that root left them, in THIS machine's workdir: what a launch
// outside a checkout reads the deployed root's answer from
// (deployedRepoCredentialsService), and a document the upgrade's backfill
// deliberately does NOT read any more (the review of 2026-11-07's fourth
// round: it asked about roots another machine applies, and those write no
// document here — appliedRepoCredentialsService reads the installation's own
// state instead).
func seedRepoRootVars(t *testing.T, slug, doc string) {
	t.Helper()
	dir, err := infra.RepoWorkdir(os.Getenv, initProject, slug)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gcp", "roots", "repo", infra.VarsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestALaunchOutsideACheckoutReadsTheDeployedRoot is the launch's half of the
// switch where the run's own DOCUMENT cannot be read at all — the one state
// neither the record nor the auth can judge. `fugaro run --repo X` from a
// directory that holds no checkout of X leaves launchFileConfig with nothing to
// read: no ref in an object database, no working tree to fall back to, so
// agent.auth is "" and the switch's third condition is unanswerable. Withholding
// the address there starts a `cascade` run whose job mounts no model credential
// at all — the mounts were dropped by the apply that switched it on — so the
// run halts at the service's door (D143), which is what every such launch did
// before this.
//
// What answers in the document's place is the repository root's own tfvars: the
// document the last apply of that root was built from, which carries the
// service exactly when that apply dropped the job's mounts and granted its
// account roles/run.invoker (deployedRepoCredentialsService, over the file
// seedRepoRootCredentialsService above writes and update_switch_test.go's rig
// produces for real).
//
// The second half is the same launch with no tfvars of that root on this
// machine: nothing here says its root carries the service, so the job is handed
// nothing — the address is also what opens the git provider keyless
// (internal/gitprov/providers/providers.go), and the entry's record is not the
// evidence this branch reads: it is written by an apply of this machine's, and
// what this launch is missing is the document such an apply would have left.
//
// Mutation (verified, run and restored): drop the deployedRepoCredentialsService
// call at run.go's call site (pass false) and the first half fails with an
// empty plain list.
func TestALaunchOutsideACheckoutReadsTheDeployedRoot(t *testing.T) {
	for name, seeded := range map[string]bool{
		"the repository root's own tfvars carry the service": true,
		"no tfvars of that root on this machine":             false,
	} {
		t.Run(name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
			markRepoCredentialsServiceReady(t, f.cloudFixture)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			// A directory that is no checkout of the repository: nothing for
			// launchFileConfig to read the run's document from.
			t.Chdir(t.TempDir())
			if seeded {
				seedRepoRootCredentialsService(t, appSlug)
			}
			env := memEnv(t, f.cloudFixture)
			rec := &envRecorder{Backend: env.be}
			env.be = rec
			if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
				t.Fatal(err)
			}
			want := envCredentialsServiceURL + "=" + launchCredsURL
			if !seeded {
				if len(rec.env) != 0 {
					t.Fatalf("a launch with no document and no tfvars of the repository's root put %v in the plain Env, want none: nothing on this machine says that root was applied with the service on, and the address is what opens the git provider keyless for a mint a job account with no invoker grant is refused", rec.env)
				}
				return
			}
			if len(rec.env) != 1 || rec.env[0] != want {
				t.Fatalf("the launch's plain Env = %v, want exactly [%s]: the run's document could not be read, and the repository root's own apply is what the job's containers are in", rec.env, want)
			}
		})
	}
}

// TestASharedProjectJobIsNotToldAboutTheCredentialsService is the same rule
// through the launch itself, because the helper's job argument is the
// launcher's to get right and a helper test cannot see the call site: this is
// the arm that would break an opted-in installation's shared project jobs.
//
// The fixture is a real shared-job launch (projectJobFixture: the profile, the
// published policy, the job the installation deployed) with the opt-in on, and
// the checkout selects the clone workflow that resolves it — asserted, so the
// test cannot pass by launching the per-repository job instead. The repository
// is switched on (a cascade checkout, the record written): what this test is
// about is the job's own kind, so everything else the switch reads says yes and
// the shared job is still handed nothing.
//
// Mutation (verified, run and restored): pass "" for job in run.go's launch
// call — this fails on the credentials entry, and
// TestTheCredentialsServiceURLReachesTheJobEnv still passes, which is why the
// two exist together.
func TestASharedProjectJobIsNotToldAboutTheCredentialsService(t *testing.T) {
	f := newProjectJobFixture(t)
	f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
	projectJobCheckoutYAML(t, projectJobCascadeRepoYAML)
	markRepoCredentialsServiceReady(t, f.cloudFixture)
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
