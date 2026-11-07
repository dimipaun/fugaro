package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
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

// repoStateGrantAddress is where an applied repository root's state keeps the
// invoker grant the switch turned on made: modules/workflow/sa.tf's
// google_cloud_run_v2_service_iam_member.credentials, reached the way the root
// reaches its workflow module. The suffix is what
// infra.RepoCredentialsServiceApplied matches on.
const repoStateGrantAddress = `module.repo.module.workflow["web"].google_cloud_run_v2_service_iam_member.credentials[0]`

// repoStateJobAddress is one of the root's own resources, which a held-off
// root's state holds too: what makes seedRepoStateHeldOff a state and not an
// empty document.
const repoStateJobAddress = `module.repo.module.workflow["web"].google_cloud_run_v2_job.workflow`

// seedRepoStateGrant writes a repository root's state object in the
// installation's state bucket as the apply of a root the switch is ON for
// leaves it: the invoker grant is in the state, which is the fact the
// upgrade's backfill reads (appliedRepoCredentialsService).
func seedRepoStateGrant(t *testing.T, r *initRig, slug string) {
	t.Helper()
	seedRepoState(t, r, slug, repoStateGrantAddress)
}

// seedRepoStateHeldOff writes the same object for a root applied with the
// switch OFF: the root's own resources are there and the invoker grant is not,
// which is what the module plans for a held-off root.
func seedRepoStateHeldOff(t *testing.T, r *initRig, slug string) {
	t.Helper()
	seedRepoState(t, r, slug, repoStateJobAddress)
}

// seedRepoState writes one repository root's state object, holding the given
// addresses, where whichever machine applied that root put it: the
// installation's own copy in the state bucket, which every checkout and every
// machine shares and which is therefore the evidence the upgrade's backfill
// reads. A repository nothing seeds has no object at all — a root never applied
// anywhere. The document is marshalled rather than written as a literal,
// because an address carries quoted module keys of its own
// (repoStateGrantAddress).
func seedRepoState(t *testing.T, r *initRig, slug string, addresses ...string) {
	t.Helper()
	res := make([]map[string]any, 0, len(addresses))
	for _, a := range addresses {
		res = append(res, map[string]any{"address": a})
	}
	doc, err := json.Marshal(map[string]any{
		"format_version": "1.0",
		"values":         map[string]any{"root_module": map[string]any{"child_modules": []map[string]any{{"resources": res}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.stateBucket()
	r.gcs.Put(initStateBucket, infra.RepoStatePrefix(slug)+"/default.tfstate", doc)
}

// repoWorkdirExists reports whether this machine holds a repository root's
// workdir — the per-machine copy of what a run handed Terraform, which is the
// document an upgrade may NOT have to rely on.
func repoWorkdirExists(t *testing.T, slug string) bool {
	t.Helper()
	wd, err := infra.RepoWorkdir(os.Getenv, initProject, slug)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(wd)
	return err == nil
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
		if lc.Repos[name].CredentialsServiceOn() {
			t.Errorf("the local config records the credentials service as ready for %s after an update that read a bundle holding a model credential, and this run applied no repository root: the record vouches for the roles/run.invoker grant only that root's apply makes, so a launch of %s would be handed the service's address with no grant to knock with — an oauth job whose mounts are still there, whose git provider the address opens keyless, and whose run has no fallback (D143)\n%s", name, name, readFileString(t, r.cfg))
		}
	}
}

// TestAnOptedInInstallationConvergesWithOneUpdate is the UPGRADE path of this
// release, and the state every installation that took the service before it is
// in: the local config holds the service's block (the flag that shipped it
// wrote one), the repository root was applied with the service on — its job
// mounts dropped, its job account granted roles/run.invoker — and the entry
// carries NO record, because the field did not exist when all of that happened.
// A NO record and a written false are two different documents since the review
// of 2026-11-07's fourth round (localcfg.Repo.CredentialsServiceReady is a
// pointer), and it is the no-record one that tells this installation from one
// that never opted in: a recorded false is a door's judgement of that root and
// the backfill leaves it alone.
//
// One ordinary `fugaro update` has to finish that upgrade, which is CLAUDE.md's
// HARD RULE, and that is why the installation door backfills: this release's
// launcher reads the record, so an entry left unrecorded means a `cascade` run
// of that repository is handed no service address while its jobs mount no model
// credential — a run that halts at the service's door (D143). One command, and
// no pass per checkout: the sibling entries are why the backfill sits in this
// door, since the repository stage converges the checkout's own root alone.
//
// The evidence is the installation's OWN state and not this machine's workdir,
// which is the other thing the review's fourth round found: the workdir
// (<state>/fugaro/terraform/<project>/repos/<slug>) is per machine and per
// checkout, so an installation whose roots a teammate applied — or that this
// same person applied from another clone — has no document here at all, and a
// backfill reading it would converge one checkout per `fugaro update`, the
// operator step the HARD RULE forbids. The state bucket is the installation's
// one copy, written by whichever machine ran the apply (seedRepoStateGrant
// below writes it, and NO workdir exists anywhere in this test).
//
// The other half of the test is what keeps the backfill evidence and not a
// blanket yes. acme/other's root was applied with the service OFF (its state
// holds the root's own resources and no invoker grant, which is what the module
// plans for a held-off root: its jobs keep their mounts and its accounts hold no
// grant), and acme/third was never applied anywhere (no state object at all).
// Recording either ready would hand its launch an address it cannot use.
//
// The record's own read — that a true record, a block and a cascade document
// are what a launch hands the URL for — is pinned by
// TestTheSwitchTurnsOnForACascadeRepositoryWithAReadyBundle, and the two doors'
// readings of the bundle by the tests above.
//
// Two mutations, both verified, run and restored. Drop the
// backfillCredentialsServiceReady call from install() (internal/cli/init.go)
// and this fails on the first assertion — acme/sandbox left unrecorded, a
// repository whose job lost its model mounts to an address no launch of it is
// handed. Make the backfill a blanket yes (record every entry the config holds,
// which is what the first draft of this work order did) and it fails on the
// second — acme/other and acme/third recorded over roots whose jobs kept their
// mounts. Read the workdir's tfvars instead of the state
// (deployedRepoCredentialsService, the pre-fix reader; seed nothing into the
// state bucket) and it fails on the FIRST assertion too, which is the fourth
// round's finding.
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
	// The state the previous release left on the installation — the one object
	// per root that every machine and every checkout shares: this one applied
	// with the service on, this one with it off, and nothing at all for the
	// third (a repository never applied anywhere, a first onboarding).
	seedRepoStateGrant(t, r.initRig, slug)
	seedRepoStateHeldOff(t, r.initRig, other)
	if repoWorkdirExists(t, slug) {
		t.Fatalf("this machine holds a workdir for %s, so the test is not the state the review's fourth round found: the upgrade has to converge from the installation's own state, without a document this machine happens to have written", slug)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("an ordinary update failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config does not parse: %v", err)
	}
	if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("acme/sandbox carries no record after one ordinary update, though its root was applied with the service on and this run read a bundle holding a model credential: its jobs mount no model credential any more, so a cascade run of it is left with nothing to run on — the record the launcher reads is the only thing that hands it the service's address (D143), and the upgrade owes it without a second command\n%s", readFileString(t, r.cfg))
	}
	for _, name := range []string{"acme/other", "acme/third"} {
		if lc.Repos[name].CredentialsServiceOn() {
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
// BeforeItPlansItsRoot above, which drives the door itself.
//
// auth is the repository's agent.auth as the door passes it, cfg.Agent.Auth of
// the document a RUN reads (deploymentConfig): the third condition of the
// switch, which the record carries with the bundle's readiness. The fixture is
// the same string in the checkout's fugaro.yaml, since a door that judged one
// document and planned the root from another is what
// TestTheRootIsDeployedForTheAuthTheRunsRead pins.
//
// switchOutputs is the installation root's own outputs as the repository door
// reads them, and it is where the local config's block comes FROM (init.go's
// install writes the config's block out of these): a rig carrying the block in
// its config carries it here too, or the two say different things about the
// installation, which is the state the door below is judged against.
func switchOutputs() infra.InstallationOutputs {
	return infra.InstallationOutputs{CredentialsService: &infra.CredentialsService{
		Name:   infra.CredentialsServiceName,
		Region: "us-east5",
		URL:    credentialsServiceURL,
	}}
}

func recordForRepo(t *testing.T, r *initRig, auth string) *localcfg.Config {
	return recordForRepoOutputs(t, r, auth, switchOutputs())
}

// recordForRepoOutputs is recordForRepo over outputs a test chooses, which is
// what pins the record's first condition: the installation the SPEC is built
// for is the one these outputs describe.
func recordForRepoOutputs(t *testing.T, r *initRig, auth string, outs infra.InstallationOutputs) *localcfg.Config {
	t.Helper()
	e := rigEngine(t, r, &initOptions{yes: true})
	cfg, problems := config.Parse([]byte(checkoutYAML("bitbucket", auth, "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, outs, "acme/sandbox", cfg.Git.Provider, cfg.Agent.Auth)
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
// installation whose service is already on: the door makes that entry BEFORE
// the spec reads it (recordCredentialsServiceReady, the review of 2026-11-07's
// fourth round), so this run plans the root for the service and records the
// same answer every later run does — one run, and not the two an entry created
// only after the apply needed.
//
// Mutations, both verified, run and restored. Give writeRepoConfig the fresh
// reading again (CredentialsServiceReady: r.credentialsBundleReady(ctx, lc))
// and the first half fails over a read the spec never saw. Create the entry in
// the write after the apply only (drop the creation from
// recordCredentialsServiceReady) and the second half fails with the record
// false — the held-off root the first `fugaro update --repo` of a repository
// then deploys, which is the finding.
func TestTheRecordIsWrittenAsThePlanReadIt(t *testing.T) {
	model := []byte(config.AnthropicSecret + ": " + credsValue(t, "model") + "\n")

	sibling := switchRig(t)
	addSwitchRepo(t, sibling, "acme/other")
	putSwitchBundle(t, sibling, model)
	lc := recordForRepo(t, sibling, config.AuthCascade)
	if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("the record for the converged repository is not set over a bundle holding a model credential: the spec was built from this value, so this run deployed the root for a service the launch will not be told about")
	}
	if lc.Repos["acme/other"].CredentialsServiceOn() {
		t.Errorf("the record moved for acme/other, whose root this run did not apply: its job accounts hold no roles/run.invoker, so its launch would be handed the service's address — which opens the git provider keyless — and the service would refuse it with no fallback (D143)")
	}

	fresh := switchRig(t)
	withoutSwitchRepo(t, fresh, "acme/other")
	putSwitchBundle(t, fresh, model)
	lc = recordForRepo(t, fresh, config.AuthCascade)
	entry, ok := lc.Repos["acme/sandbox"]
	if !ok {
		t.Fatalf("the door left no entry for a repository it onboarded: the local config is what the next run reads that repository's provider and record from\n%s", readFileString(t, fresh.cfg))
	}
	if entry.Provider != "bitbucket" {
		t.Errorf("the entry the onboarding created carries provider %q, want bitbucket: the shell the door makes before the spec reads it has the checkout's own provider and nothing else", entry.Provider)
	}
	if !entry.CredentialsServiceOn() {
		t.Errorf("the record is false for a repository the local config did not hold when the door ran, though the installation has the service, the bundle holds a model credential and the repository's agent.auth is the cascade: the entry the door makes BEFORE infra.Repo reads it is what makes this run plan the switched-on root, and a record false here is the held-off root — mounts kept, no invoker grant — whose next launch withholds the address, leaving a `cascade` run with nothing to run on until a second, unannounced `fugaro update --repo` in the same checkout (D143)")
	}
}

// TestAFirstOnboardingConvergesInOneRun is the finding above through the whole
// command: `fugaro update --repo --plan-only` in the checkout of a repository
// the local config does not hold yet, on a converged installation. The root it
// plans must be the switched-on one — installation.credentials_service in the
// tfvars the plan was built from — without any earlier run having onboarded the
// repository and without a second one after it.
//
// The configuration is the adoption order the switch exists for and the one a
// cascade repository is onboarded in: the installation has the service, the
// bundle holds a model credential, and the repository's own document says
// agent.auth: cascade. On main one run sufficed for this, and it still does.
//
// Mutation (verified, run and restored): create the entry in the write after
// the apply only (drop the creation from recordCredentialsServiceReady,
// internal/cli/init_credentials.go) and the root is planned held off — the fact
// is absent — because the spec, built before that write, read no entry at all.
func TestAFirstOnboardingConvergesInOneRun(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	r := credentialsRig(t)
	r.repoReady(t)
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	credentialsOutputs(t, r)
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	// The repository is NOT in the local config: this is its first onboarding,
	// and the checkout is where its provider and its document come from.
	withoutSwitchRepo(t, r.initRig, "acme/other")

	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", "")))
	out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
	if err != nil {
		t.Fatalf("the run failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if _, present := repoCredentialsServiceFact(t, slug); !present {
		t.Errorf("the repository root's tfvars carry no installation.credentials_service on the run that onboarded the repository, though the installation has the service, the bundle holds a model credential and the checkout says agent.auth: cascade: the plan was built before the entry existed, so this run deployed the held-off root — the job's model mounts kept and its account granted no roles/run.invoker — and the launch that follows withholds the service's address, leaving the cascade run with no model credential at all (D143)\nstdout:\n%s", out)
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
	if got := recordForRepo(t, r, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); got {
		t.Errorf("the record says the service is ready for a bundle with no version at all: `fugaro creds set` is the bundle's only writer, so this is the state every installation is in the moment its update deploys the service")
	}

	// The bundle holds a credential, and it is not a model one: the git
	// credential the proxy forwards is a kind the cascade must never reach
	// (internal/credsvc's KindGit), so it buys the repositories nothing.
	git := switchRig(t)
	putSwitchBundle(t, git, []byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
	if got := recordForRepo(t, git, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); got {
		t.Errorf("the record says the service is ready for a bundle whose only entry is a git credential: the service serves model credentials (KindModel), and a job switched onto it would have nothing to run on")
	}

	// And a model credential is the yes: the record is what the plan and the
	// launch read, so this is the half that turns the switch on.
	model := switchRig(t)
	putSwitchBundle(t, model, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	if got := recordForRepo(t, model, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); !got {
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
	if got := recordForRepo(t, r, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); !got {
		t.Fatalf("the fixture is not the state this test starts from: the record does not say the bundle's model credential makes the service ready")
	}

	// The bundle emptied in the sense the service answers: a version that
	// parses and holds no model credential. The record is a value the run
	// writes like any other, so it goes back to false — which is what puts the
	// repository's own mounts back on the next apply.
	r.sm.Seed(credsvc.BundleSecretID(initProjectName), map[string]string{gcp.LabelManaged: gcp.ManagedValue},
		[]byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
	next := recordForRepo(t, r, config.AuthCascade)
	if got := next.Repos["acme/sandbox"].CredentialsServiceOn(); got {
		t.Errorf("the record still says the service is ready after the bundle stopped holding a model credential: every run of this repository would be launched against a service that cannot serve it, while its job's own model mounts stay dropped")
	}
	// The switch-off is written down rather than dropped, and it has to be: the
	// record is a tri-state (localcfg.Repo.CredentialsServiceReady), where "no
	// record" says no machine has judged this root and `false` says an apply ran
	// with the switch off. Every reader of the switch reads the two the same
	// way; the published config must not, because it is a union whose local
	// entry wins a clash (MergeShared/mergeRepoRecords), so a dropped false
	// would let this machine's next publish clear a teammate's true — the
	// seventh review round's first finding.
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "credentials_service_ready: false") {
		t.Errorf("the local config does not carry the switch going off for this repository, and this file is the one `fugaro update` publishes for the installation: the switch-off has to travel with it, or the machine that applied the root with the service off looks like one that never judged it:\n%s", raw)
	}
}

// TestTheRecordFollowsTheAuthTheRunsRead is the third condition of the switch
// at the record, and the direction the review of 2026-11-07 found missing: the
// record's writer answered from the bundle alone, so an entry was recorded
// ready for a repository the plan that same run made was NOT built to be served
// by — a held-off root, whose job keeps its model mounts and whose account is
// granted no roles/run.invoker.
//
// The state is the natural adoption order: the owner fills the bundle, runs
// `fugaro update --repo` while the checkout still says `agent.auth: oauth`, and
// commits the cascade afterwards. On bare readiness that run recorded true; the
// next launch reads the cascade from the document (TestEditingTheAuthMode-
// TakesEffectWithoutAnotherUpdate pins that it must), sees a record that
// vouches for the service, and hands the job an address whose grant this plan
// never made. The provider then opens GitHub keyless for a mint the private
// service refuses, and a `cascade` run has no credential of its own to fall
// back to (D143) — the run fails as infra_error, with no PR, until somebody
// runs another `fugaro update --repo`. That is the broken state the switch
// exists to make impossible, and it is reachable without a second command in
// any order but the lucky one.
//
// Both halves run over ONE rig and ONE ready bundle, so nothing here is about
// the bundle: the value that moves is the auth, and the record has to move with
// it. The false half is the direction no test covered — an oauth apply records
// `false`, the job keeps the mounts that apply kept, and the run works until
// the cascade is committed and applied, which is the run that records true.
//
// Mutation (verified, run and restored): record the bundle reading alone
// (entry.CredentialsServiceReady = ready) and the second half fails with the
// record set over a root the plan held off.
func TestTheRecordFollowsTheAuthTheRunsRead(t *testing.T) {
	r := switchRig(t)
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))

	if got := recordForRepo(t, r, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); !got {
		t.Fatalf("the fixture is not the state the oauth half moves away from: over a bundle holding a model credential and a repository whose agent.auth the service serves, the record has to say the service is ready — that is the plan this run applied")
	}
	if got := recordForRepo(t, r, "oauth").Repos["acme/sandbox"].CredentialsServiceOn(); got {
		t.Errorf("the record says the service is ready for a repository whose agent.auth is oauth, over the bundle the half above recorded: the plan this same run applied kept the job's model mounts and granted its account no roles/run.invoker (the module drops both for the roots the switch is on for), so the record hands the next launch — which reads the auth these runs read — an address the service refuses for the grant nobody made. A cascade committed afterwards must be applied by a run of its own before the service is of any use to this repository")
	}
	// The record went back rather than staying where the first half left it,
	// and it is written as `false` rather than dropped: the field is a
	// tri-state, and only the door that applies this root writes it, so the
	// switch-off has to reach the other machines through the published union
	// (MergeShared/mergeRepoRecords) instead of looking like a root nobody
	// judged.
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "credentials_service_ready: false") {
		t.Errorf("the local config does not carry the record going back after the oauth apply, and this file is the one `fugaro update` publishes for the installation:\n%s", raw)
	}
}

// TestAnOptedOutInstallationRecordsNoSwitch is the record's first condition,
// and the state that makes it matter: the local config still carries the
// service's block and its URL — an owner who takes the service out leaves them
// there, and the config is rewritten only by the next ordinary update
// (docs/credentials-service/runbook.md §9) — while the installation root's
// OUTPUTS carry no service at all, which is what the spec reads installed from
// (infra.Inputs.Installation, withDefaults never filling it from the config).
// `fugaro update --repo`, which applies no installation root, is the door that
// meets this state.
//
// The plan that door builds is held off: mounts kept, no roles/run.invoker. A
// record written from the bundle's reading alone would say ready anyway, and
// the next launch — which reads the config's block — would hand the job the
// address of a service that no longer exists, opening the git provider keyless
// for a mint nothing answers while the job still mounts the credential it
// needed (D143). The record is the plan's own first condition, so it says what
// the plan did.
//
// Both halves run over ONE rig and ONE ready bundle: what moves is the
// installation's outputs, which is the whole of the first condition.
//
// Mutation (verified, run and restored): pass true for installed again
// (infra.CredentialsServiceServes(true, ready, auth)) and the second half fails
// with the record set over a root the plan held off.
func TestAnOptedOutInstallationRecordsNoSwitch(t *testing.T) {
	r := switchRig(t)
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))

	if got := recordForRepo(t, r, config.AuthCascade).Repos["acme/sandbox"].CredentialsServiceOn(); !got {
		t.Fatalf("the fixture is not the state the opt-out half moves away from: over an installation whose outputs carry the service, a readable bundle and a cascade document, the record has to say the service is ready — that is the plan this run applied")
	}
	if got := recordForRepoOutputs(t, r, config.AuthCascade, infra.InstallationOutputs{}).Repos["acme/sandbox"].CredentialsServiceOn(); got {
		t.Errorf("the record says the service is ready on an installation whose outputs carry no service: the spec this same run built read installed from those outputs, so the root was applied held off — mounts kept, no roles/run.invoker — while the record would hand the next launch an address for a service that is not there, which opens the git provider keyless for a mint nothing answers (D143). The config's stale block is not evidence that a root was applied with the service on")
	}
	// Recorded `false` and not dropped, for the tri-state's reason: the door that
	// applied this root judged it, and the switch-off travels in the published
	// union (MergeShared/mergeRepoRecords) while an absent record says no machine
	// has judged it at all.
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "credentials_service_ready: false") {
		t.Errorf("the local config does not carry the record after the installation's service was taken out, and this file is the one `fugaro update` publishes for the installation:\n%s", raw)
	}
}

// TestAnUnreadableBundleLeavesTheRecordAlone is the other half of what a
// reading is: a read that FAILED is not the answer "the bundle holds no model
// credential", and writing that answer is not a local choice — the record
// travels in the local config `fugaro update` publishes
// (internal/localcfg/shared.go), so one refused read on one machine would
// switch the service off for every teammate's `fugaro run`, whose jobs had
// their model mounts dropped by the apply that switched them on and have
// nothing of their own left to run on. Every one of those runs halts at the
// service's door (D143) until somebody whose identity CAN read the bundle runs
// `fugaro update` again.
//
// The failure is the one an operator meets after a role change or a rotation:
// the container is there and this identity may not list its versions — the
// fake's 403, armed on the one list the door makes (FailVersionsAfter counts
// the lists the fake has served, so the first call below has to make one
// before the refusal can be aimed at the second). The record starts at the
// value a person's own run wrote — the switch is ON, and the mounted state of
// that repository's jobs is what makes the false so expensive — and it is
// still there after the refused run, in the file the next publish reads.
//
// Mutation (verified, run and restored): answer the failed read with a write
// (return false, true from unreadable, the pre-fix code) and this fails with
// the record cleared and the key gone from the config.
func TestAnUnreadableBundleLeavesTheRecordAlone(t *testing.T) {
	r := switchRig(t)
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	markSwitchRepoReady(t, r, "acme/sandbox")
	if got := recordForRepo(t, r, config.AuthCascade); !got.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Fatalf("the fixture is not the state this test starts from: over a readable bundle holding a model credential, the record has to be written")
	}
	r.sm.FailVersionsAfter = r.sm.VersionsListCalls()

	lc := recordForRepo(t, r, config.AuthCascade)
	if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("the record was cleared by a run that read nothing: the bundle's version list answered 403, and an error is an answer about this machine's identity and not about the model credentials the bundle holds")
	}
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "credentials_service_ready: true") {
		t.Errorf("the local config no longer carries the record after a read that failed, and this file is the one `fugaro update` publishes for the installation: every other machine reads the cleared switch, withholds the service's address from a `cascade` run whose job mounts no model credential, and halts at the service's door (D143)\n%s", raw)
	}
}

// TestAFailedReadIsNotAnsweredByTheRunsSecondDoor is the other way into the
// write above, and the one the reading's own memo opens: ONE run asks the
// bundle twice — the installation door's backfill for the repositories a
// previous release applied (init.go's install), then the repository stage's
// record for the root this run converges (init.go's repoEngine) — and the
// second ask has to read the first one's ANSWER rather than its memo. A
// memo that remembered only the reading would hand the second door a false
// with record=true, which is the write the first door had just refused: a
// false no reading produced, into the entry the repository stage persists and
// `fugaro update` publishes for the installation.
//
// The state is the upgrade's, with the two entries the two doors ask about.
// acme/sandbox carries the record a person's own run wrote — the switch is ON
// for it, and its job's model mounts are gone, which is what makes the false
// so expensive — and acme/other is the repository the backfill exists for: the
// installation's state holds its root's invoker grant and its entry carries no
// record. The bundle read fails on the version list, the refusal of the role
// change or rotation above.
//
// The bundle is read once in the whole run, which is what the two calls here
// are: the second one is the repository door's own call on the same engine, and
// the file is written by the door's own write.
//
// Mutation (verified, run and restored): memoize the reading without its record
// flag (the pre-fix `if r.credsRead { return r.credsReady, true }`) and this
// fails with the record cleared for acme/sandbox, in the entry and in the file.
func TestAFailedReadIsNotAnsweredByTheRunsSecondDoor(t *testing.T) {
	r := switchRig(t)
	addSwitchRepo(t, r, "acme/other")
	putSwitchBundle(t, r, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	if got := recordForRepo(t, r, config.AuthCascade); !got.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Fatalf("the fixture is not the state this test starts from: over a readable bundle holding a model credential, the record has to be written")
	}
	sibling, err := task.Slug("bitbucket", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	seedRepoStateGrant(t, r, sibling)
	r.sm.FailVersionsAfter = r.sm.VersionsListCalls()

	e := rigEngine(t, r, &initOptions{yes: true})
	c, err := newInitClients(t.Context(), e.lc)
	if err != nil {
		t.Fatalf("the rig's clients do not build: %v", err)
	}
	e.r.backfillCredentialsServiceReady(t.Context(), c, initStateBucket, e.lc, switchOutputs())
	if !e.lc.Repos["acme/sandbox"].CredentialsServiceOn() || e.lc.Repos["acme/other"].CredentialsServiceOn() {
		t.Fatalf("the run's first door did not leave the records this test judges the second door against (sandbox %v, other %v): the backfill reads the bundle once and writes nothing from a read that failed",
			e.lc.Repos["acme/sandbox"].CredentialsServiceOn(), e.lc.Repos["acme/other"].CredentialsServiceOn())
	}
	e.r.recordCredentialsServiceReady(t.Context(), e.lc, switchOutputs(), "acme/sandbox", "bitbucket", config.AuthCascade)
	spec := infra.RepoSpec{Name: "acme/sandbox", Provider: "bitbucket", BaseBranch: "master"}
	cfg, problems := config.Parse([]byte(checkoutYAML("bitbucket", config.AuthCascade, "aurora", "")))
	if cfg == nil {
		t.Fatalf("the fixture's fugaro.yaml does not parse: %v", problems)
	}
	if err := e.r.writeRepoConfig(t.Context(), e.lc, spec, cfg, r.cfg, e.old); err != nil {
		t.Fatalf("the repository stage's config write failed: %v", err)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the project config does not parse: %v", err)
	}
	if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("the run's second door cleared the record of a repository whose bundle was never read: the backfill's failed read is not a reading, and a false written from it switches the service off for every machine of this installation — the launches that then withhold the address start `cascade` runs whose jobs have no model mount left to fall back on (D143)")
	}
	if raw := readFileString(t, r.cfg); !strings.Contains(raw, "credentials_service_ready: true") {
		t.Errorf("the local config no longer carries the record after a run that read nothing, and this file is the one `fugaro update` publishes for the installation:\n%s", raw)
	}
}

// markSwitchRepoReady writes the record a person's OWN run leaves on a
// repository's entry: it read a bundle that held a model credential, so the
// entry the next run reads already answers the switch.
func markSwitchRepoReady(t *testing.T, r *initRig, name string) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := "  " + name + ": {"
	if !strings.Contains(string(b), entry) {
		t.Fatalf("the rig's local config holds no entry for %s:\n%s", name, b)
	}
	ready := strings.Replace(string(b), entry, entry+" credentials_service_ready: true,", 1)
	if err := os.WriteFile(r.cfg, []byte(ready), 0o600); err != nil {
		t.Fatal(err)
	}
	if lc, err := localcfg.Load(r.cfg); err != nil || !lc.Repos[name].CredentialsServiceOn() {
		t.Fatalf("the record the helper wrote does not read back (err %v): the fixture is not the state this test starts from", err)
	}
}

// TestTheBundleIsNeverOpenedInAnAgentsSessionOrARunsProcess is the fence the
// record's writer needed and did not have: EVERY command that touches this
// installation's secrets refuses a coding agent's session and a run's own
// process before its first call (`fugaro creds ls/set`, credsOwnerOnly), and
// the bundle credentialBundleReady reads is the widest of them — the one
// document holding every model credential the service issues.
//
// The run is the whole repository door, `fugaro update --repo --plan-only`,
// which is how the hole was found: with CLAUDECODE set it read the bundle,
// answered from it and recorded the answer, so an agent's session could switch
// a repository off — or on — for a credential it may not read, and could do it
// to an installation whose bundle it had just opened. The two doors are one
// fence: the repository door (recordCredentialsServiceReady, whose reading is
// what the spec below it is built from) and the installation door's backfill
// (backfillCredentialsServiceReady) read through the same function.
//
// Every marker is looped over, the agent-set ones and the IDE extension's
// alike, and so is every marker a Fugaro run's own process carries, because the
// sentence a refusal prints and the variable it names are read by a person: a
// fence that moved off one of them fails here.
//
// The fixture is chosen so the two answers are far apart. The bundle holds a
// git credential and no model one, and the fake is NOT allowed to serve its
// payload (r.sm.Seed without AllowAccess): a run that reads the document fails
// the test twice over, once by opening the version list and once at the access
// call, which the fake reports. The entry carries the record a person's own run
// wrote — the bundle DID hold a model credential then — so the pre-fix run
// answered false from a document it had just read, planned the repository's
// root held off, and would have written that false over the person's record.
//
// The plan is what this door's --plan-only run leaves to read: it stops before
// the local config is written, so the record ITSELF is asked of the writer in
// TestTheRecordIsKeptUnderAnAgentOrRunMarker, which drives the two calls this
// door makes without the apply in between.
//
// Mutation (verified, run and restored): drop the two marker checks from
// credentialsBundleReady (internal/cli/init_credentials.go) and every marker
// here fails, on the bundle's version list and on the key absent from the
// root's tfvars.
func TestTheBundleIsNeverOpenedInAnAgentsSessionOrARunsProcess(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	markers := append(append([]string{}, agentenv.Markers...), managedFlagRunMarkers...)
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			r := credentialsRig(t)
			r.repoReady(t)
			r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
			credentialsOutputs(t, r)
			markSwitchRepoReady(t, r.initRig, "acme/sandbox")
			bundle := credsvc.BundleSecretID(initProjectName)
			r.sm.Seed(bundle, map[string]string{gcp.LabelManaged: gcp.ManagedValue},
				[]byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
			read, listed := r.sm.Accessed(), r.sm.VersionsListedFor(bundle)

			t.Setenv(marker, "1")
			t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "cascade", "aurora", "")))
			out, errOut, err := executeStdin(t, "", "update", "--repo", "--plan-only")
			if err != nil {
				t.Fatalf("the run failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}

			if got := r.sm.Accessed(); len(got) != len(read) {
				t.Errorf("the run read %v, and the bundle's own document is the one secret a coding agent's session and a run's process may not read (credsOwnerOnly, internal/cli/creds.go): the bundle holds every model credential the service issues\nstdout:\n%s", got[len(read):], out)
			}
			if got := r.sm.VersionsListedFor(bundle); got != listed {
				t.Errorf("the run listed the bundle's %d version(s) where it listed %d before, and that list is the first call a store over the container makes: nothing under this marker may open the bundle's store at all, whatever it would find there\nstdout:\n%s", got, listed, out)
			}
			if _, present := repoCredentialsServiceFact(t, slug); !present {
				t.Errorf("the repository root was planned held off, though the entry's record says the service is ready and this run read no bundle that could have said otherwise: the plan has to be the one the record already answers, or this run's apply takes the repository's model mounts away and grants it nothing in their place\nstdout:\n%s", out)
			}
		})
	}
}

// TestTheRecordIsKeptUnderARunsProcessMarker is the other half of that door's
// fence, at the writer: what the entry carries before the run it still carries
// after it, whatever the bundle would have answered.
//
// The run above cannot show this by itself — --plan-only stops before the local
// config is written — so this one drives the door's own two calls, the record's
// refresh and the config write, over the same rig and the same bundle
// (recordForRepo, which is the pair every record test here reads). The record
// starts true, the bundle holds no model credential, and under a run marker the
// false the bundle would answer is not a reading and is not written: the switch
// is a value a READING may move, in either direction, and a process that may
// not open the document has no reading to move it with.
//
// The markers are the run ones, because an agent's session never reaches this
// write at all: the repository stage refuses to apply in one (initflow's
// AgentError, "a coding agent's session is present…) before the config is
// written, so what it may not move is the plan, which is what the test above
// pins.
//
// Mutation (verified, run and restored): drop the two marker checks from
// credentialsBundleReady and every marker here fails, the record cleared to
// false for a bundle this run had no business reading.
func TestTheRecordIsKeptUnderARunsProcessMarker(t *testing.T) {
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := switchRig(t)
			markSwitchRepoReady(t, r, "acme/sandbox")
			bundle := credsvc.BundleSecretID(initProjectName)
			r.sm.Seed(bundle, map[string]string{gcp.LabelManaged: gcp.ManagedValue},
				[]byte(config.BitbucketSecret+": "+credsValue(t, "git")+"\n"))
			t.Setenv(marker, "1")

			lc := recordForRepo(t, r, config.AuthCascade)
			if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
				t.Errorf("the record a person's own run wrote was cleared by a run that read no bundle: the bundle holds no model credential, and that is an answer only a reading may give — written here, it switches the repository off on the word of a process that may not look at the credential it is judging, and the person's next run has nothing to correct it from\n%s", readFileString(t, r.cfg))
			}
			if got := r.sm.Accessed(); len(got) != 0 {
				t.Errorf("the run read %v, and the bundle's document is the one secret a coding agent's session and a run's process may not read (credsOwnerOnly, internal/cli/creds.go): the bundle holds every model credential the service issues", got)
			}
			if got := r.sm.VersionsListedFor(bundle); got != 0 {
				t.Errorf("the run listed the bundle's versions %d time(s), and that list is the first call a store over the container makes: nothing under this marker may open it, whatever it would find there", got)
			}
		})
	}
}

// TestTheBackfillIsNotWrittenInAnAgentsSessionOrARunsProcess is the same fence
// at the installation door, which is the other reader of the bundle: the
// ordinary `fugaro update`, whose backfill
// (backfillCredentialsServiceReady, internal/cli/init_credentials.go) is what
// converges an installation that took the service before this release
// (TestAnOptedInInstallationConvergesWithOneUpdate is that path in full).
//
// The state is the one the backfill exists for: the repository root's own
// tfvars say the service is on, the entry carries no record, and the bundle
// holds a model credential — so the run that may read it records the entry, and
// the run under a marker leaves it exactly as it found it. The fake is not
// allowed to serve the payload here either, so a run that reached the document
// fails this test twice over.
//
// The markers are the RUN ones alone, and that is the reachable set at this
// door: an agent's session never gets here, because the installation step
// refuses to apply in one before this door runs at all (initflow's AgentError,
// "a coding agent's session is present…, so nothing is applied"), so the run
// that can read this bundle with the installation door's identity is a Fugaro
// run's own process. The agent markers are pinned where an agent CAN drive this
// code — the repository door above, whose plan reads the bundle before its
// apply is ever reached.
//
// Left unrecorded is the correct answer and not a lost convergence: the record
// is the person's, the entry carries none, and the person's own next `fugaro
// update` writes it — the upgrade still takes one command, and not one an agent
// has to run.
//
// Mutation (verified, run and restored): drop the two marker checks from
// credentialsBundleReady and this fails with the entry recorded and the
// bundle's payload read.
func TestTheBackfillIsNotWrittenInAnAgentsSessionOrARunsProcess(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := credentialsRig(t)
			r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
			r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
			credentialsOutputs(t, r)
			bundle := credsvc.BundleSecretID(initProjectName)
			r.sm.Seed(bundle, map[string]string{gcp.LabelManaged: gcp.ManagedValue},
				[]byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
			seedRepoRootCredentialsService(t, slug)

			t.Setenv(marker, "1")
			out, errOut, err := executeStdin(t, "", "update", "--yes")
			if err != nil {
				t.Fatalf("the run failed: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
			}

			if got := r.sm.Accessed(); len(got) != 0 {
				t.Errorf("the run read %v, and the backfill's bundle holds every model credential the service issues: an agent's session reads it the way credsOwnerOnly refuses to (`fugaro creds ls`, internal/cli/creds.go)\nstdout:\n%s", got, out)
			}
			if got := r.sm.VersionsListedFor(bundle); got != 0 {
				t.Errorf("the run listed the bundle's versions %d time(s), which is the first call a store over the container makes: the backfill reads the bundle to answer the switch, and no answer under this marker may be written down\nstdout:\n%s", got, out)
			}
			lc, err := localcfg.Load(r.cfg)
			if err != nil {
				t.Fatalf("the local config does not parse: %v", err)
			}
			if lc.Repos["acme/sandbox"].CredentialsServiceOn() {
				t.Errorf("the backfill wrote the switch's record for acme/sandbox from a bundle this run may not read: the entry keeps the record the person's own run left on it, which here is none — and the person's next `fugaro update` is what converges it, so nothing is lost by this run leaving it alone\n%s", readFileString(t, r.cfg))
			}
		})
	}
}
