package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
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

// apiKeyLaunchYAML is oauthLaunchYAML with the one word that makes it the other
// auth. The repository, the workflow and the git provider are the same, so the
// pair is what a test about the auth's own condition (D2071) needs.
var apiKeyLaunchYAML = strings.Replace(oauthLaunchYAML, "auth: oauth", "auth: api-key", 1)

// launchWithCredentialsService launches the fixture's run for a checkout
// carrying yaml against an installation whose local config carries the
// service's block with url (empty for an installation that never deployed it,
// where the block is absent altogether) and the per-repository record when
// ready is true, and returns what the backend was handed. It publishes no
// policy node, which is the state of a project that has never published one
// (launchWithCredentialsServiceMode).
//
// The checkout is the test's own, and that is the switch's third condition: it
// is the repository's agent.auth, read from the document the run will read —
// the ref's fugaro.yaml, else this machine's checkout — and never from the
// record, so a repository this launcher cannot read at all is a repository it
// cannot switch on.
func launchWithCredentialsService(t *testing.T, yaml, url string, ready bool) (plain, secret []string) {
	t.Helper()
	return launchWithCredentialsServiceMode(t, yaml, url, ready, "", false)
}

// launchWithCredentialsServiceMode is launchWithCredentialsService with the
// project's PUBLISHED policy node in the test's hand: the node, carrying mode,
// which is what the switch's fourth condition used to be (D2071) and what AB15
// took out of it (D3009) — the fixture keeps the node so a test can pin that
// the answer is the same for every mode it can write, observe and enforce as
// much as the word.
//
// published false is no node at all, which is the state of an installation that
// has never published a policy.
func launchWithCredentialsServiceMode(t *testing.T, yaml, url string, ready bool, mode string, published bool) (plain, secret []string) {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	if url != "" {
		f.appendConfig(t, "credentials_service:\n  url: "+url+"\n")
	}
	if published {
		publishPolicy(t, f, cascadePolicyWithMode(mode))
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
	return withoutServicesStamp(t, rec.env), rec.secretEnv
}

// withoutServicesStamp takes the services stamp out of a launch's plain Env,
// and fails unless it is there exactly once: the launcher writes it on every
// launch, the empty value included, so that an image's own ENV under the same
// name can never answer for the entry the job was deployed with
// (backend.ServicesEnv, servicesLaunchEnv, D1904). The tests that compare a
// plain list to the entries the credentials-service switch produces are about
// those entries; the stamp is the one entry every launch carries, for every
// installation and every repository, and its value is the entry's own list or
// empty — never a secret (the empty value is the common case here, so a test
// that forgot it would otherwise read as "the launch wrote nothing").
func withoutServicesStamp(t *testing.T, plain []string) []string {
	t.Helper()
	out := make([]string, 0, len(plain))
	stamps := 0
	for _, kv := range plain {
		if strings.HasPrefix(kv, backend.ServicesEnv+"=") {
			stamps++
			continue
		}
		out = append(out, kv)
	}
	if stamps != 1 {
		t.Fatalf("the launch's plain Env carries %s %d times (%v): every launch writes it once, empty when the entry declares no service", backend.ServicesEnv, stamps, plain)
	}
	return out
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

// TestLaunchEnvCarriesTheURLForASwitchedAPIKeyRepository is the arm this task
// adds to the switch: a repository whose agent.auth is `api-key` or `oauth` is
// handed the service's address like a cascade one, and only while the project's
// published policy carries a budget mode — the service is reached through the
// gateway, and the gateway exists in `observe` and `enforce` alone (D2071).
//
// The address is NOT inert (F29, R6): with it set the GitHub provider opens
// keyless and the run mints at the service
// (internal/gitprov/providers/providers.go, gated on the very name this entry
// carries), which is the whole of what makes the switch worth turning on for
// these two auths — and the address and the mounts are two answers, one release
// apart (D2072, G17): B2.3 handed the address out while every mount was still
// there, because `api-key` hashes its own key and `oauth` runs on its own
// subscription until the flip hands the serving back, and B2.7's flip is what
// took the two Claude mounts away afterwards (config.ServiceServedAuths;
// internal/infra's TestFilledServiceServedAuthsDropsTheAnthropicMountsOnly and
// TestFilledServiceServedAuthsKeepsProviderKeys are the plan's half of that
// pair — a provider key stays mounted either way, R6, D1634).
//
// A node nobody published, a node that says the word and a node in either live
// mode are one answer here: the fourth condition the launch's answer used to
// carry was the published node's budget mode (D2071), and AB15 took it out
// (D3009) — every run has the gateway the service is reached through, so what
// holds a repository off now is the block, the record and the auth alone. The
// held-off row that is left is the one whose root no apply switched on, which
// is the state ONE `fugaro update` leaves a fresh installation in.
func TestLaunchEnvCarriesTheURLForASwitchedAPIKeyRepository(t *testing.T) {
	want := envCredentialsServiceURL + "=" + launchCredsURL
	for name, tc := range map[string]struct {
		yaml      string
		mode      string
		published bool
		ready     bool
		on        bool
	}{
		"an api-key repository where the budget is observed":          {apiKeyLaunchYAML, config.BudgetObserve, true, true, true},
		"an api-key repository where the budget is enforced":          {apiKeyLaunchYAML, config.BudgetEnforce, true, true, true},
		"an oauth repository where the budget is observed":            {oauthLaunchYAML, config.BudgetObserve, true, true, true},
		"an oauth repository where the budget is enforced":            {oauthLaunchYAML, config.BudgetEnforce, true, true, true},
		"an api-key repository where the budget says the word":        {apiKeyLaunchYAML, config.BudgetOff, true, true, true},
		"an api-key repository on a project that published no policy": {apiKeyLaunchYAML, "", false, true, true},
		"an api-key repository whose root no apply switched on":       {apiKeyLaunchYAML, config.BudgetEnforce, true, false, false},
	} {
		plain, secret := launchWithCredentialsServiceMode(t, tc.yaml, launchCredsURL, tc.ready, tc.mode, tc.published)
		for _, kv := range secret {
			if strings.HasPrefix(kv, envCredentialsServiceURL+"=") {
				t.Errorf("%s: the launch carried %q in its secret entries: the address is the service's public address and the audience the ID token is minted for, and a Batch task refuses a secret entry outright (backend.LaunchSpec.SecretEnv)", name, kv)
			}
		}
		if tc.on {
			if len(plain) != 1 || plain[0] != want {
				t.Errorf("%s: the launch's plain Env = %v, want exactly [%s]: a repository the service serves under a live budget mode is switched on, and its runs mint through the service from here", name, plain, want)
			}
			continue
		}
		if len(plain) != 0 {
			t.Errorf("%s: the launch's plain Env = %v, want none: a project whose budget is `off` (or that published no node), and a root no apply switched on, both leave the repository on its own credentials — the address is what opens the git provider keyless, and the service is reached through a gateway that does not exist there", name, plain)
		}
	}
}

// TestSwitchedAPIKeyGitHubRunMintsThroughTheService is the positive of the pair
// above for the provider this repository uses, and the state F29 is about: the
// GitHub App key of a switched `api-key` repository is held by the service, so
// the job opens the provider keyless and the mint that replaces its own App
// token is the service's (internal/gitprov/github's ServiceHeldKey path, over
// the address this entry names).
//
// The readiness the address is conditioned on is the repository's record, which
// on a real installation is written by an apply whose bundle held the git
// credential this provider needs (recordCredentialsServiceReady): a repository
// whose provider credential is not in the bundle is held off
// (update_switch_test.go's TestUpdateSwitchHoldsOffARepoWhoseGitCredentialIsNotInTheBundle),
// and with every mount kept that costs such a run the address and nothing
// else — it runs on its own App key exactly as it does today (D2072).
func TestSwitchedAPIKeyGitHubRunMintsThroughTheService(t *testing.T) {
	plain, _ := launchWithCredentialsServiceMode(t, apiKeyLaunchYAML, launchCredsURL, true, config.BudgetEnforce, true)
	url, ok := credentialsServiceURLOf(plain)
	if !ok || url != launchCredsURL {
		t.Fatalf("the launch of a switched api-key GitHub repository put %v in the plain Env, want %s=%s: this entry is the whole of the keyless path (internal/gitprov/providers/providers.go gates it on this name), so without it the run mints with the App key mounted on its job instead of at the service", plain, envCredentialsServiceURL, launchCredsURL)
	}

	heldOff, _ := launchWithCredentialsServiceMode(t, apiKeyLaunchYAML, launchCredsURL, false, config.BudgetEnforce, true)
	if len(heldOff) != 0 {
		t.Errorf("a GitHub repository whose entry carries no record launched with %v: the record is written by the apply whose bundle held this provider's credential, so a launch that filled it in from what it can see would open the provider keyless on the strength of a bundle nobody checked", heldOff)
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
// The switch's own refusals are here beside them, because they are the whole
// of what the helper answers with (infra.CredentialsServiceServes): a block
// whose bundle the record does not vouch for, an auth the service cannot serve,
// and a job that is the shared project job. Every one of them hands the job
// nothing, which is the state every repository of a converged installation is
// in until an operator stores a credential in the bundle. The published budget
// mode used to be one of them (D2071) and is none any more (AB15, D3009).
//
// A SHARED PROJECT JOB is judged on its PROFILE's answer (B5.2, D505, D2078),
// which is the answer B5.7's drop was taken on: a job of a profile one
// repository of which is on the switch is handed the address whether or not
// THIS repository's own record says on and whether or not the document was
// readable. Judging it per repository is the state this task's review found —
// a run of such a repository had neither the model mount the profile's drop had
// taken nor an address to fetch one from, and died at its first model call with
// a remedy that could not put the mount back — and the two rows that part the
// two answers are here rather than beside the launch they belong to because
// this is the helper's own table. What a shared job is still not handed is the
// address for an auth the service does not serve, whose runs read nothing of
// the service's and would only lose the App key's path to the git provider, and
// it is never handed one on this machine's tfvars of a repository root: a
// shared job's grant is the installation root's, made from the same records
// wherever that root was applied.
//
// The last rows are what answers where the document cannot be read, and the
// fifth argument's whole job is one of the two: an empty auth is a document the
// launcher could not read (a resolved config always defaults agent.auth, so the
// empty string never comes from a file), and two things stand in for it — the
// repository's own record, and this machine's tfvars of the root that record
// describes (deployedRepoCredentialsService, read at the call site). Either one
// saying on hands the address, because either is written by an apply that
// dropped the job's mounts and granted its account roles/run.invoker. A machine
// that holds neither never applied the root, and that is every console launch:
// the desktop console spawns `fugaro run --repo` from a private empty directory,
// so the document is never readable there and the record is the only answer —
// withholding the address on such a machine starts a `cascade` run whose job
// mounts no model credential and halts at the service's door (D143).
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
		if got := credentialsServiceLaunchEnv(lc, "", "acme/app", config.AuthCascade, "", false, false); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil", name, got)
		}
	}
	got := credentialsServiceLaunchEnv(optedIn, "", "acme/app", config.AuthCascade, "", false, false)
	if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
		t.Errorf("credentialsServiceLaunchEnv = %v, want [%s=%s]", got, envCredentialsServiceURL, launchCredsURL)
	}
	// The auths this release adds (B2.3): an `api-key` or `oauth` repository is
	// handed the same address a cascade one is, and the auth is the whole of what
	// decides beside the block and the record. The mode used to be the fourth
	// condition — the service is reached through the gateway, and the gateway
	// existed in `observe` and `enforce` alone (D2071) — and every run has one
	// now, so the word that was a mode, or no mode at all, is answered exactly as
	// observe is (AB15, D3009). A cascade repository was the only one of the
	// three served before B2.3.
	for _, auth := range []string{config.AuthAPIKey, config.AuthOAuth} {
		for _, mode := range []string{config.BudgetObserve, config.BudgetEnforce, config.BudgetOff, ""} {
			got := credentialsServiceLaunchEnv(optedIn, "", "acme/app", auth, mode, false, false)
			if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
				t.Errorf("credentialsServiceLaunchEnv(auth %s, mode %s) = %v, want [%s=%s]: this release switches such a repository on, whatever its job mounts and whatever its node says (config.ServiceServedAuths keys the mounts, not the address, and the mode is no condition of the switch any more)", auth, mode, got, envCredentialsServiceURL, launchCredsURL)
			}
		}
	}
	for name, tc := range map[string]struct {
		lc       *localcfg.Config
		auth     string
		mode     string
		deployed bool
	}{
		"no record yet": {
			lc:   &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
			auth: config.AuthCascade,
			mode: config.BudgetEnforce,
		},
		"no document, and the root carries no service": {
			lc:       &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
			deployed: false,
		},
		"no service to hand": {lc: &localcfg.Config{}, deployed: true},
	} {
		if got := credentialsServiceLaunchEnv(tc.lc, "", "acme/app", tc.auth, tc.mode, tc.deployed, false); got != nil {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want nil: the switch is the plan's own predicate (infra.CredentialsServiceServes), and a job told where the service is opens its git provider keyless (internal/gitprov/providers/providers.go) for a credential the service cannot hand back", name, got)
		}
	}
	// Both stand-ins, from either side: with no document to read, a root whose
	// tfvars carry the service hands the address — with the record empty, which
	// is the upgrade's state, and with it already written, which is an ordinary
	// launch from outside a checkout — and so does the record alone on a machine
	// that holds no tfvars of the root, which is every console launch. The last
	// of those is the regression this pins: it read only this machine's workdir
	// before, so a teammate's console launch of a switched-on cascade
	// repository got no address and its run halted at the service's door.
	for name, tc := range map[string]struct {
		lc       *localcfg.Config
		deployed bool
	}{
		"a root applied with the service on, no record": {
			lc:       &localcfg.Config{CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL}},
			deployed: true,
		},
		"a root applied with the service on, and recorded": {lc: optedIn, deployed: true},
		"the record alone, on a machine that never applied it": {
			lc: optedIn,
		},
	} {
		got := credentialsServiceLaunchEnv(tc.lc, "", "acme/app", "", "", tc.deployed, false)
		if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
			t.Errorf("%s: credentialsServiceLaunchEnv = %v, want [%s=%s]", name, got, envCredentialsServiceURL, launchCredsURL)
		}
	}
	// The shared job (B5.2): told on its PROFILE's answer, which is the one the
	// drop was taken on (B5.7), and never on this machine's tfvars of a
	// repository root — a shared job's grant is the installation root's, made
	// from that record wherever it was applied. The profile is the config's own
	// (localcfg.Config.Profiles), the same map the deployer reads to compose
	// the job and to decide its mounts, and the repository the launch works on
	// is one of the repositories that profile admits.
	sharedJob := func(repos map[string]localcfg.Repo) *localcfg.Config {
		return &localcfg.Config{
			CredentialsService: &localcfg.CredentialsService{URL: launchCredsURL},
			Repos:              repos,
			Profiles:           map[string]config.JobProfile{"standard": {Provider: "github"}},
		}
	}
	appOn := map[string]localcfg.Repo{"acme/app": {Provider: "github", CredentialsServiceReady: localcfg.RecordSwitch(true)}}
	appOff := map[string]localcfg.Repo{"acme/app": {Provider: "github", CredentialsServiceReady: localcfg.RecordSwitch(false)}}
	// The review's own state: the profile is served because ANOTHER repository
	// of it is on, while the repository this launch works on has no record —
	// and, before the profile's answer was read here, was handed nothing to
	// make a model call with while its mount was already gone.
	appOffLibOn := map[string]localcfg.Repo{
		"acme/app": {Provider: "github", CredentialsServiceReady: localcfg.RecordSwitch(false)},
		"acme/lib": {Provider: "github", CredentialsServiceReady: localcfg.RecordSwitch(true)},
	}
	for name, tc := range map[string]struct {
		lc       *localcfg.Config
		auth     string
		deployed bool
		want     bool
	}{
		"a served profile, the document read": {lc: sharedJob(appOn), auth: config.AuthCascade, want: true},
		"a served profile, this repository's own record off": {
			lc:   sharedJob(appOffLibOn),
			auth: config.AuthAPIKey,
			want: true,
		},
		"a served profile with no readable document": {
			lc:       sharedJob(appOn),
			deployed: true,
			want:     true,
		},
		"a served profile with no readable document and nothing deployed here": {
			lc:   sharedJob(appOn),
			want: true,
		},
		"a served profile, an auth the service cannot serve": {
			lc:   sharedJob(appOn),
			auth: config.DefaultAgentAuth,
		},
		"a profile no repository of which is on the switch": {
			lc:   sharedJob(appOff),
			auth: config.AuthCascade,
		},
		"a profile this config does not declare": {lc: optedIn, auth: config.AuthCascade},
		"no service to hand":                     {lc: &localcfg.Config{}, auth: config.AuthCascade},
	} {
		got := credentialsServiceLaunchEnv(tc.lc, "standard", "acme/app", tc.auth, "", tc.deployed, false)
		if tc.want {
			if len(got) != 1 || got[0] != envCredentialsServiceURL+"="+launchCredsURL {
				t.Errorf("%s: a launch on a shared project job put %v in the execution's plain Env, want [%s=%s]: a profile the service serves reaches it from the account the module granted roles/run.invoker to (deploy/terraform/gcp/modules/project/sa.tf)", name, got, envCredentialsServiceURL, launchCredsURL)
			}
			continue
		}
		if got != nil {
			t.Errorf("%s: a launch on a shared project job put %v in the execution's plain Env, want none: the profile's answer is the drop's own (infra.CredentialsServiceServesProfile and, for the auth, config.ServiceServedAuths), and telling a job where the service is opens its git provider keyless (internal/gitprov/providers/providers.go) for a credential the service cannot hand back", name, got)
		}
	}
	// A review launch is told nothing, whatever the job: the address is part of
	// what the review-only variant is deployed with (internal/infra's
	// reviewVariant), so a per-launch entry would be a value every execution
	// object records and a slip asked for on a path that has never taken one.
	if got := credentialsServiceLaunchEnv(optedIn, "standard", "acme/app", config.AuthCascade, "", false, true); got != nil {
		t.Errorf("a review launch put %v in the execution's plain Env, want none: the variant carries the service's address from its deployment", got)
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

// TestALaunchOutsideACheckoutReadsTheRecordOrTheDeployedRoot is the launch's
// half of the switch where the run's own DOCUMENT cannot be read at all — the
// one state the auth cannot judge. `fugaro run --repo X` from a directory that
// holds no checkout of X leaves launchFileConfig with nothing to read: no ref
// in an object database, no working tree to fall back to, so agent.auth is ""
// and the switch's third condition is unanswerable. Withholding the address
// there starts a `cascade` run whose job mounts no model credential at all —
// the mounts were dropped by the apply that switched it on — so the run halts
// at the service's door (D143), which is what every such launch did before
// this.
//
// Two answers stand in for the document, and either one is enough, because
// both are written by an apply that dropped the job's mounts and granted the
// job's account roles/run.invoker: the repository's own record, written by
// that apply's machine or by the installation-wide backfill that read the
// installation's state, and this machine's tfvars of the root it was applied
// from (deployedRepoCredentialsService, over the file
// seedRepoRootCredentialsService above writes and update_switch_test.go's rig
// produces for real). Reading only the tfvars is what the console's launches
// cannot survive: the desktop console spawns `fugaro run --repo` from a private
// empty directory on every machine, so the record is the only answer a
// teammate's machine has — and a switched-on cascade repository it withholds
// the address from fails at bootstrap with no PR.
//
// The last case is the same launch with neither: no record and no tfvars of
// that root on this machine, so nothing here says its root carries the service
// and the job is handed nothing — the address is also what opens the git
// provider keyless (internal/gitprov/providers/providers.go), and that is the
// fail-closed direction the switch wants.
//
// Mutation (verified, run and restored): pass the record out of the empty-auth
// branch of credentialsServiceLaunchEnv (serves = installed && deployed) and
// the middle case fails with an empty plain list.
func TestALaunchOutsideACheckoutReadsTheRecordOrTheDeployedRoot(t *testing.T) {
	for name, tc := range map[string]struct {
		rootSeeded bool
		recorded   bool
	}{
		"the repository root's own tfvars carry the service": {rootSeeded: true, recorded: true},
		"the record alone, with no tfvars of that root here": {recorded: true},
		"neither the record nor this machine's tfvars":       {},
	} {
		t.Run(name, func(t *testing.T) {
			f := newLaunchBudget(t, "enforce")
			f.appendConfig(t, "credentials_service:\n  url: "+launchCredsURL+"\n")
			if tc.recorded {
				markRepoCredentialsServiceReady(t, f.cloudFixture)
			}
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			// A directory that is no checkout of the repository: nothing for
			// launchFileConfig to read the run's document from.
			t.Chdir(t.TempDir())
			if tc.rootSeeded {
				seedRepoRootCredentialsService(t, appSlug)
			}
			env := memEnv(t, f.cloudFixture)
			rec := &envRecorder{Backend: env.be}
			env.be = rec
			if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
				t.Fatal(err)
			}
			plain := withoutServicesStamp(t, rec.env)
			if !tc.recorded && !tc.rootSeeded {
				if len(plain) != 0 {
					t.Fatalf("a launch with no document, no record and no tfvars of the repository's root put %v in the plain Env, want none: nothing on this machine says that root was applied with the service on, and the address is what opens the git provider keyless for a mint a job account with no invoker grant is refused", plain)
				}
				return
			}
			want := envCredentialsServiceURL + "=" + launchCredsURL
			if len(plain) != 1 || plain[0] != want {
				t.Fatalf("the launch's plain Env = %v, want exactly [%s]: the run's document could not be read, and the repository's record and this machine's tfvars of its root are what stand in for it", plain, want)
			}
		})
	}
}

// The launch-level half of the shared job's switch is in
// project_job_test.go: TestLaunchHandsTheServiceURLToASwitchedProjectJob and
// TestLaunchKeepsAnUnswitchedProjectJobOffTheService. It moved there with B5.2,
// which is the task that hands the address to a shared job a repository's own
// job has been handed since C1.24 — the rule this file's helper table above
// spells for the shared arm, and TestTheCredentialsServiceURLReachesTheJobEnv
// spells for a repository's own job.
