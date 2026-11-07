package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
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

// addSwitchRepo adds a second repository entry to the rig's local config: the
// switchRepos is the `repos:` line the rigs ship, so a test can rewrite it.
const switchRepos = "  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }"

// addSwitchRepo adds a second repository entry to the rig's local config: what
// a sibling entry does when the door converges acme/sandbox is one of the
// things below asserts.
func addSwitchRepo(t *testing.T, r *initRig, name string) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), switchRepos) {
		t.Fatalf("the rig's local config no longer holds %q:\n%s", switchRepos, b)
	}
	entry := switchRepos + "\n  " + name + ": { provider: bitbucket, base_branch: master, workflows: [web] }"
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(b), switchRepos, entry, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withoutSwitchRepo leaves the rig's local config holding name alone: the
// repository the door converges is then one the config does not hold yet,
// which is what a first onboarding of a repository looks like.
func withoutSwitchRepo(t *testing.T, r *initRig, name string) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), switchRepos) {
		t.Fatalf("the rig's local config no longer holds %q:\n%s", switchRepos, b)
	}
	entry := "  " + name + ": { provider: bitbucket, base_branch: master, workflows: [web] }"
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(b), switchRepos, entry, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repoCredentialsServiceFact is the credentials service as the repository
// root's plan reads it: the installation object in the tfvars the run wrote
// under the repository's own workdir. present is false when the key is not
// there at all, which is what the switch held off writes
// (RepoInstallation.CredentialsService's omitempty).
func repoCredentialsServiceFact(t *testing.T, slug string) (map[string]any, bool) {
	t.Helper()
	wd, err := infra.RepoWorkdir(os.Getenv, initProject, slug)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wd, "gcp", "roots", "repo", infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Installation map[string]json.RawMessage `json:"installation"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	raw, present := doc.Installation["credentials_service"]
	if !present {
		return nil, false
	}
	var svc map[string]any
	if err := json.Unmarshal(raw, &svc); err != nil {
		t.Fatalf("installation.credentials_service = %s: %v", raw, err)
	}
	return svc, true
}

// TestTheInstallationDoorRecordsNoRepositoryItDoesNotApply is the installation
// half of the record, driven end to end: the ordinary `fugaro update`, no
// direct call to the writer.
//
// The bundle holds a model credential and the local config holds TWO
// repositories, and the answer this run must leave for BOTH of them is "not
// recorded" — however ready the bundle is. The record is a statement about a
// grant, and the grant that makes the service usable to a repository is made
// by that repository's OWN root's apply (modules/workflow/sa.tf's
// google_cloud_run_v2_service_iam_member.credentials; the installation root
// grants run.invoker to the operators, the coordinator and the verifier and to
// no job account). This run applies the installation root alone — the
// repository stage is skipped, there being no checkout here — so recording
// either entry would tell a launch of that repository the service is ready
// while its job accounts hold no grant: the launch hands it the service's
// address, which opens the git provider keyless for a mint the service refuses,
// and a `cascade` run has no credential of its own to fall back to (D143).
//
// Mutation (verified, run and restored): move the record back to the
// installation door (call recordCredentialsServiceReady from install(),
// internal/cli/init.go, with every repository the config holds) and this fails
// with both entries recorded.
func TestTheInstallationDoorRecordsNoRepositoryItDoesNotApply(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	addSwitchRepo(t, r.initRig, "acme/other")
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("an ordinary update failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config does not parse: %v", err)
	}
	for _, name := range []string{"acme/sandbox", "acme/other"} {
		if lc.Repos[name].CredentialsServiceReady {
			t.Errorf("the local config records the credentials service as ready for %s after an update that read a bundle holding a model credential, and this run applied no repository root: the record vouches for the roles/run.invoker grant only that root's apply makes, so a launch of %s would be handed the service's address with no grant to knock with — an oauth job whose mounts are still there, whose git provider the address opens keyless, and whose run has no fallback (D143)\n%s", name, name, readFileString(t, r.cfg))
		}
	}
}

// TestAnOptedInInstallationConvergesWithOneUpdate is the UPGRADE path of this
// release, and the state every installation that took the service before it is
// in: the local config holds the service's block (the flag that shipped it
// wrote one), the repository root's tfvars carry installation.credentials_service
// — the apply that dropped that repository's job mounts and granted its job
// account roles/run.invoker — and the entry carries NO record, because the field
// did not exist when all of that happened. The field's absence and a written
// false are the same document (omitempty, deliberately: false is what every
// earlier release's config already said), so nothing on the machine tells this
// installation from one that never opted in.
//
// One ordinary `fugaro update` has to finish that upgrade, which is CLAUDE.md's
// HARD RULE, and that is why the installation door backfills: this release's
// launcher reads the record, so an entry left unrecorded means a `cascade` run
// of that repository is handed no service address while its jobs mount no model
// credential — a run that halts at the service's door (D143). One command, and
// no pass per checkout: the sibling entries are why the backfill sits in this
// door, since the repository stage converges the checkout's own root alone.
//
// The other half of the test is what keeps the backfill evidence and not a
// blanket yes. acme/other's root was applied with the service OFF (the key
// absent, which is what the module writes for a held-off root: its jobs keep
// their mounts and its accounts hold no grant), and this machine holds no
// document at all for acme/third (a sibling never applied here, a first
// onboarding). Recording either ready would hand its launch an address it
// cannot use.
//
// The record's own read — that a true record, a block and a cascade document
// are what a launch hands the URL for — is pinned by
// TestTheSwitchTurnsOnForACascadeRepositoryWithAReadyBundle, and the two doors'
// readings of the bundle by the tests above.
//
// Mutation (verified, run and restored): drop the backfillCredentialsServiceReady
// call from install() (internal/cli/init.go) and this fails on the first
// assertion — acme/sandbox left unrecorded, a repository whose job lost its
// model mounts to an address no launch of it is handed.
func TestAnOptedInInstallationConvergesWithOneUpdate(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	other, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	credentialsOutputs(t, r)
	addSwitchRepo(t, r.initRig, "acme/other")
	addSwitchRepo(t, r.initRig, "acme/third")
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	// The state the previous release left on this machine, one root per
	// repository the config holds: this one applied with the service on, this
	// one with it off, and no document at all for the third.
	seedRepoRootCredentialsService(t, slug)
	seedRepoRootVars(t, other, `{"installation":{}}`)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("an ordinary update failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config does not parse: %v", err)
	}
	if !lc.Repos["acme/sandbox"].CredentialsServiceReady {
		t.Errorf("acme/sandbox carries no record after one ordinary update, though its root was applied with the service on and this run read a bundle holding a model credential: its jobs mount no model credential any more, so a cascade run of it is left with nothing to run on — the record the launcher reads is the only thing that hands it the service's address (D143), and the upgrade owes it without a second command\n%s", readFileString(t, r.cfg))
	}
	for _, name := range []string{"acme/other", "acme/third"} {
		if lc.Repos[name].CredentialsServiceReady {
			t.Errorf("the update recorded the credentials service as ready for %s, whose root was applied with the service OFF (or was never applied on this machine at all): its jobs keep their model mounts and its accounts hold no roles/run.invoker, so its launch would be handed an address the service refuses it for — a record is a statement about a grant, and neither state is evidence of one\n%s", name, readFileString(t, r.cfg))
		}
	}
}

// TestTheRepositoryDoorReadsTheBundleBeforeItPlansItsRoot is the repository
// half, driven end to end from the checkout — the whole `fugaro update --repo`
// command, not a call to writeRepoConfig.
//
// The state is the one the review found: the local config's entry carries no
// record (the field is new, so every existing installation is in it) while the
// bundle holds a model credential and the installation has the service. The
// plan the run makes is read where the plan reads it, the repository root's
// tfvars: the switch is ON, which is only true if the bundle was read and the
// record refreshed before the spec was built. With the read after the plan
// (where the persisted write sits) this run would plan a held-off root — the
// job keeping its model mounts and its account granted no roles/run.invoker —
// while writing the record that hands the next launch the service's address,
// and a launch of that job would be refused by the private service for the
// grant the plan never made.
//
// Mutation (verified, run and restored): move the refresh below `infra.Repo(in)`
// in repoEngine (internal/cli/init.go) and this fails with the key absent.
func TestTheRepositoryDoorReadsTheBundleBeforeItPlansItsRoot(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	r := credentialsRig(t)
	// repoReady is the repository door's own fixture: an applied installation
	// in the state bucket, and the base image the daily image check of the
	// repository's workflow runs from.
	r.repoReady(t)
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	credentialsOutputs(t, r)
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))

	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", "")))
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
	if err != nil {
		t.Fatalf("the run failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	svc, present := repoCredentialsServiceFact(t, slug)
	if !present {
		t.Fatalf("the repository root's tfvars carry no installation.credentials_service, though the installation has the service, the bundle holds a model credential and the repository's agent.auth is the cascade: the run planned a held-off root while the record it writes says the service is ready, so the next launch is handed an address whose invoker grant this plan never made")
	}
	if svc["name"] != infra.CredentialsServiceName {
		t.Errorf("installation.credentials_service = %v, want the installation's own service %q: the URL is left behind because the repository root's variable carries no such attribute (repoCredentialsService)",
			svc, infra.CredentialsServiceName)
	}
}

// TestTheRootIsDeployedForTheAuthTheRunsRead pins the plan's half of the
// switch against the SAME document the launch reads: the fugaro.yaml at the
// default branch, which is the file every run of the repository checks out —
// and not whatever this working tree happens to hold.
//
// The two rows are one uncommitted edit apart in each direction, and each is
// the failure the other document would produce. Over a default branch that says
// cascade, a working tree edited to oauth leaves the launcher (which reads the
// ref) handing the job the service's address: a plan built from the working
// tree would keep that job's model mounts and grant its account no
// roles/run.invoker, so the run is refused by the service with nothing to fall
// back to (D143) — the root has to be deployed for the cascade the runs read.
// Over a default branch that says oauth, a working tree edited to cascade is
// the same break the other way: a plan built from the working tree drops the
// job's model mount while the launcher, reading oauth, withholds the address,
// and the job is left with no model credential at all.
//
// The document is read where the plan reads it, the repository root's tfvars:
// installation.credentials_service is there exactly when the switch is on
// (repoCredentialsService).
//
// Mutation (verified, run and restored): build the spec from the working tree
// again (drop the deploymentConfig call in repoEngine, internal/cli/init.go)
// and both rows fail, each with the answer the other document gives.
func TestTheRootIsDeployedForTheAuthTheRunsRead(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		committed, worktree string
		want                bool
	}{
		"the default branch is cascade and the working tree is edited to oauth": {"cascade", "oauth", true},
		"the default branch is oauth and the working tree is edited to cascade": {"oauth", "cascade", false},
	} {
		r := credentialsRig(t)
		r.repoReady(t)
		r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
		credentialsOutputs(t, r)
		putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
		dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", tc.committed, "aurora", ""))
		// The uncommitted edit: the working tree's file is not the default
		// branch's, which is the state `fugaro update --repo` runs in and the
		// converge's repository stage refuses (init_repo_target.go).
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(checkoutYAML("bitbucket", tc.worktree, "aurora", "")), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Chdir(dir)
		out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
		if err != nil {
			t.Fatalf("%s: the run failed: %v\nstdout:\n%s\nstderr:\n%s", name, err, out, errOut)
		}
		_, present := repoCredentialsServiceFact(t, slug)
		if present != tc.want {
			t.Errorf("%s: the repository root's tfvars carry installation.credentials_service: %v, want %v — the plan is judged on the file the RUN will read, not on this working tree's, or the root deployed here is one a launch parts company with\nstdout:\n%s", name, present, tc.want, out)
		}
	}
}

// recordForRepo runs the repository door's own two halves over the rig's
// installation and returns the config it left: the refresh the spec is built
// from (initRun.recordCredentialsServiceReady — the read of the bundle these
// tests are about) and then the persisted write (initRun.writeRepoConfig),
// which writes the value the plan read.
//
// The ORDER the real door keeps — bundle read and record refreshed before the
// root's spec is built — is pinned by TestTheRepositoryDoorReadsTheBundle-
// BeforeItPlansItsRoot above, which drives the door itself. The outs value is
// empty because the rigs here carry the block in the local config already.
func recordForRepo(t *testing.T, r *initRig) *localcfg.Config {
	t.Helper()
	e := rigEngine(t, r, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(checkoutYAML("bitbucket", "oauth", "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, infra.InstallationOutputs{}, "acme/sandbox")
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

// TestTheRecordIsWrittenAsThePlanReadIt is the record's meaning, at the writer:
// TRUE only where the plan that this same run applies was itself built with the
// switch on, and only on the entry of the repository whose root that plan is.
//
// The bundle is ready in both halves, so nothing here is about the bundle. The
// first half is the ordinary one — the config holds this repository and a
// sibling — and only the converged repository's entry may move: the sibling's
// root was not applied, so its record would vouch for a roles/run.invoker grant
// nobody made, and its launch would be handed the service's address with no
// grant to knock with (D143). The second half is a repository the config does
// not hold yet, which is every first onboarding of a repository on an
// installation whose service is already on: there is no entry for the spec to
// read, so the plan deploys the held-off root — mounts kept, no grant — and the
// record has to say so. Writing the fresh bundle reading instead would be the
// same broken state in the other direction, and this repository would be
// launched onto a service its job cannot call until its next update.
//
// Mutation (verified, run and restored): give writeRepoConfig the fresh reading
// again (CredentialsServiceReady: r.credentialsBundleReady(ctx, lc)) and the
// second half fails with the record set.
func TestTheRecordIsWrittenAsThePlanReadIt(t *testing.T) {
	model := []byte(config.AnthropicSecret + ": " + credsValue(t, "model") + "\n")

	sibling := switchRig(t)
	addSwitchRepo(t, sibling, "acme/other")
	putSwitchBundle(t, sibling, model)
	lc := recordForRepo(t, sibling)
	if !lc.Repos["acme/sandbox"].CredentialsServiceReady {
		t.Errorf("the record for the converged repository is not set over a bundle holding a model credential: the spec was built from this value, so this run deployed the root for a service the launch will not be told about")
	}
	if lc.Repos["acme/other"].CredentialsServiceReady {
		t.Errorf("the record moved for acme/other, whose root this run did not apply: its job accounts hold no roles/run.invoker, so its launch would be handed the service's address — which opens the git provider keyless — and the service would refuse it with no fallback (D143)")
	}

	fresh := switchRig(t)
	withoutSwitchRepo(t, fresh, "acme/other")
	putSwitchBundle(t, fresh, model)
	lc = recordForRepo(t, fresh)
	if lc.Repos["acme/sandbox"].CredentialsServiceReady {
		t.Errorf("the record says the service is ready for a repository the local config did not hold when the spec was built: that spec kept the job's model mounts and granted its account no roles/run.invoker, so this record would hand the next launch an address its job cannot knock with — the one state the switch exists to make impossible")
	}
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
