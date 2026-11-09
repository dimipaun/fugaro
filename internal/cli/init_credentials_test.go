package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The credentials service's deploy (docs/credentials-service/default-on.md, the
// owner's ruling of 2026-11-05; plan task DON1). Every `fugaro update` writes
// the installation root's credentials_service block, for every installation, so
// the service exists wherever the CLI has converged: there is no flag and no
// stage to opt in with any more, and the block is part of the state the
// installation stage's own confirmed apply converges.
//
// The rig is an images rig: a released CLI version, the release's credentials
// image published in the fake source under its own "-pinned" tag (so the
// resolve-and-verify step answers with a digest the destination registry
// serves, as the remote repository does), and the installation's budget
// database carrying the marker.

// credentialsRig is the images rig plus the credentials service's own pieces:
// the released image, the budget backend and its marker.
func credentialsRig(t *testing.T) *imagesRig { return credentialsRigAt(t, "1.2.3") }

// credentialsRigAt is credentialsRig for a CLI that is another release: the rig
// whose credentials image is published under version, which is what an upgrade
// BETWEEN two releases is tested with.
func credentialsRigAt(t *testing.T, version string) *imagesRig {
	t.Helper()
	r := newImagesRig(t, version)
	// The released image, and the nightly-immune "-pinned" tag a release
	// publishes beside the plain version tag.
	r.src.publish("dimipaun/fugaro-credentials", version)
	r.src.tags["dimipaun/fugaro-credentials:"+version+"-pinned"] = r.src.tags["dimipaun/fugaro-credentials:"+version]
	// The database the marker is read from: a project config with a budget
	// backend is what `fugaro update --firebase-project` writes. Its
	// firebase_project is the one the credentials service's own environment
	// carries (FUGARO_FIREBASE_PROJECT) and its rtdb_url the one FUGARO_RTDB_URL
	// carries: infra.Credentials takes both from here, because the service reads
	// the installation's own database and has no default.
	r.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+fpID+", rtdb_url: "+r.db.URL+" }\n")
	r.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	return r
}

// credentialsDigest is the digest the fake source published the credentials
// image under: what the resolve-and-verify step returns.
func (r *imagesRig) credentialsDigest() string { return r.credentialsDigestAt("1.2.3") }

// credentialsDigestAt is credentialsDigest for another release.
func (r *imagesRig) credentialsDigestAt(version string) string {
	return r.src.tags["dimipaun/fugaro-credentials:"+version]
}

// credentialsRef is the reference the deploy must write for
// credentials_service.image: the remote repository's path, the version as a
// tag, and the verified digest.
func (r *imagesRig) credentialsRef() string { return r.credentialsRefAt("1.2.3") }

// credentialsRefAt is credentialsRef for another release of the same source.
func (r *imagesRig) credentialsRefAt(version string) string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials:" + version + "@" + r.credentialsDigestAt(version)
}

// oldCredentialsRef is the reference an installation deployed under an EARLIER
// release holds: that release's version in the tag and a digest the rig never
// published — an installation's own record, which is what makes an update have
// to resolve the running release's image rather than read one.
func oldCredentialsRef(version string) string {
	return "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials:" + version + "@sha256:" + strings.Repeat("ab", 32)
}

// verifierServiceAccount is the account §2.2 names: the installation's own verifier
// service account, the one the module creates (verifier.tf, whose address is a
// fact of the configuration because the account ID is a literal,
// infra.VerifierAccountID). It is a service account and never a person: the
// service refuses to start with an address that is both a person and a
// verifier (credsvc/auth.go:574).
func verifierServiceAccount() string {
	return infra.VerifierAccountID + "@" + initProject + ".iam.gserviceaccount.com"
}

// seedCredentialsService writes the installation root's tfvars as a run of an
// earlier release left them: an installation that has the service, on that
// release's credentials image, with the entry's other facts exactly what
// infra.Credentials writes.
func seedCredentialsService(t *testing.T, r *imagesRig, image string) {
	t.Helper()
	seedCredentialsServiceWithVerifiers(t, r, image, nil)
}

// seedCredentialsServiceWithVerifiers is seedCredentialsService for an
// installation whose block already carries a verifier list (CSR14) — the state
// a run of this release leaves behind, and the one a machine whose config is
// silent about the key reads its list from.
func seedCredentialsServiceWithVerifiers(t *testing.T, r *imagesRig, image string, verifiers []string) {
	t.Helper()
	doc := map[string]any{"credentials_service": map[string]any{
		"account_id":       infra.CredentialsAccountID,
		"service":          infra.CredentialsServiceName,
		"image":            image,
		"bundle_secret":    "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358",
		"firebase_project": fpID,
		"rtdb_url":         r.db.URL,
		"job_accounts": []any{map[string]any{
			"provider": "bitbucket", "repo": "acme/sandbox", "workflow": "web"}},
		"url": credentialsServiceURL,
	}}
	if verifiers != nil {
		doc["credentials_service"].(map[string]any)["verifier_accounts"] = verifiers
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.root(), infra.VarsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setCredentialsJobTable replaces the job table the installation's tfvars
// carry, which is the state an installation that converged before this release
// holds: the deploy wrote the table once and no run has ever revised it.
func setCredentialsJobTable(t *testing.T, r *imagesRig, rows []any) {
	t.Helper()
	path := filepath.Join(r.root(), infra.VarsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the installation's tfvars carry no credentials_service to set the job table of: %s", b)
	}
	svc["job_accounts"] = rows
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// credentialsServiceEntry is the credentials_service entry the installation
// root's tfvars carry after a run.
func credentialsServiceEntry(t *testing.T, r *imagesRig) map[string]any {
	t.Helper()
	svc, _ := r.tfvars(t)["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the installation's tfvars carry no credentials_service: %v", r.tfvars(t))
	}
	return svc
}

// credentialsOutputs makes the installation root's `terraform output -json`
// answer carry the credentials service the module publishes (the installation
// root's outputs.tf): the run reads it back for the audience, and the
// repository roots read the same object from the installation's outputs.
//
// The URL is part of it, and it is what the SECOND apply is for
// (fillCredentialsAudience): the apply that creates the service cannot know it
// — Cloud Run assigns it, and Terraform refuses a resource that reads its own
// uri — so the revision carries the module's unassigned placeholder until this
// value comes back and the run applies it.
func credentialsOutputs(t *testing.T, r *imagesRig) {
	t.Helper()
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
	})}
	r.save(t)
}

// credentialsServiceURL is the URL the module's output carries in these tests:
// the shape Cloud Run assigns (the service's name, the project number, the
// region), which is the audience its callers' ID tokens are minted for.
const credentialsServiceURL = "https://fugaro-credentials-123456789012.us-east5.run.app"

// TestUpdateSetsCredentialsServiceOnEveryInstallation is the RED STEP of this
// task. On the tree before it, an installation whose tfvars carried no
// credentials_service got none from `fugaro update`: the block came from the
// owner-run --provision-credentials stage, so this test failed at its first
// assertion with "the installation's tfvars carry no credentials_service".
//
// `fugaro update` is the whole upgrade (CLAUDE.md's HARD RULE): the service
// deploys with it, on every installation, and no second command names it.
func TestUpdateSetsCredentialsServiceOnEveryInstallation(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation with no credentials service failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	// The block is infra.Credentials' own, composed from the installation's
	// local config: the names the module uses, the bundle container the loader
	// also derives, and the two Firebase facts the service is configured with.
	for k, want := range map[string]any{
		"account_id":       infra.CredentialsAccountID,
		"service":          infra.CredentialsServiceName,
		"firebase_project": fpID,
		"rtdb_url":         r.db.URL,
	} {
		if svc[k] != want {
			t.Errorf("credentials_service.%s = %v, want %v: the block is composed from the installation's own config and not from a flag\n%s", k, svc[k], want, s)
		}
	}
	// The job accounts are the installation's own repository jobs: they are
	// what the service admits, and the table it is handed is what binds a
	// caller to a repository.
	jobs, _ := svc["job_accounts"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("credentials_service.job_accounts = %v, want the installation's one repository job\n%s", svc["job_accounts"], s)
	}
	if job, _ := jobs[0].(map[string]any); job["provider"] != "bitbucket" || job["repo"] != "acme/sandbox" || job["workflow"] != "web" {
		t.Errorf("credentials_service.job_accounts[0] = %v, want the installation's own repository, workflow and provider", jobs[0])
	}
	if got := svc["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v, want %s: the very first apply that creates the service runs this release's verified image\n%s", got, r.credentialsRef(), s)
	}
	// The audience: the module's own output carries the URL Cloud Run assigned,
	// and the second apply puts it on the revision. A revision whose audience is
	// not its own URL admits no caller however healthy it looks.
	if got := svc["url"]; got != credentialsServiceURL {
		t.Errorf("credentials_service.url = %v, want %s (the URL the installation's outputs carry): the audience is the service's own URL, never a value this side composes\n%s", got, credentialsServiceURL, s)
	}
	// And the URL reaches the project config in the SAME run, which is what puts
	// it on a launch: `fugaro run` hands a job FUGARO_CREDENTIALS_SERVICE_URL
	// from the selected config's block (run.go's credentialsServiceLaunchEnv),
	// and waiting for the next ordinary update would leave a window in which the
	// service is on and every launch goes out without the address.
	if got := localConfigOf(t, r).CredentialsService; got == nil || got.URL != credentialsServiceURL {
		t.Errorf("the project config records credentials_service = %+v, want the service's URL %s\n%s", got, credentialsServiceURL, s)
	}
}

// TestUpdateKeepsTheVarsFileWhereTheAudiencePlanReadsIt is the report of
// 2026-11-09 (0.21.1, a managed project, a second attempt): the installation
// step planned, passed the managed gate and applied — the credentials service
// among the changes — the run then wrote the local config and published the
// shared config, and the SAME step stopped on its next plan with
//
//	✖ installation: terraform plan exited 1: Error: Failed to read variables file
//	    Given variables file terraform.tfvars.json does not exist.
//
// That plan is fillCredentialsAudience's (init_stages.go): the module's
// unassigned placeholder can only be replaced once Cloud Run has assigned the
// URL the callers' ID tokens are minted for, so the step writes the URL into
// the installation's tfvars, plans the root again and applies it. It writes
// through the workdir (Workdir.WriteVars, whose path is Workdir.Root plus
// VarsFile) and plans through the terraform pair the workdir was prepared
// with (initRun.terraform builds the two together, tf.TF runs in that same
// root), reading the ONE relative -var-file terraform is given.
//
// So the pair is the whole invariant, and this test is that invariant
// arranged the report's way: the service does not exist yet, the installation
// root's plan creates it, the outputs then carry its URL, and the audience
// step has to rewrite the tfvars and plan a second time. Both plans must run
// in the directory the run's own tfvars are written into — a workdir prepared
// again between the write and the plan takes the root's tree (PrepareWorkdir
// is the only thing that deletes it) and leaves the plan with no variables
// file at all, and a plan run from another root reads another root's tfvars.
//
// The rig is not a managed one, and that is not what the report's failure
// turned on: the run's own step passed the managed gate before it planned
// anything (the gate judges a saved plan, and it touches no workdir), and the
// cover tables admit the module's credentials service by the shape its plan
// writes — the image pin, the environment and the IAM the service requires —
// which the fake's plan for one address deliberately does not spell out. What
// the gate guarantees of this step is untouched: gateStep still judges this
// plan where the project is managed, and no cover table changed.
//
// Mutation (verified, run and restored): insert
// `infra.PrepareWorkdir(e.wd.Dir, "installation")` between the WriteVars and
// the Plan of fillCredentialsAudience — one workdir rebuilt for the same
// directory, which is what init_stages.go's e.setup does at the top of every
// stage — and the run fails with the report's own sentence, this test's
// first assertion:
//
//	installation: terraform plan exited 1: Error: Failed to read variables file
//	Given variables file terraform.tfvars.json does not exist.
func TestUpdateKeepsTheVarsFileWhereTheAudiencePlanReadsIt(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an update that creates the credentials service failed at one of the installation step's own plans: %v\n%s", err, s)
	}
	// The step the report points at ran, and it ran as the second plan: the URL
	// is on the revision only because the module's output came back after the
	// apply that created the service.
	if want := "the credentials service's audience is its own URL: " + credentialsServiceURL; !strings.Contains(s, want) {
		t.Fatalf("the run did not fill the service's audience, so this test is not arranged as the report is: want %q in\n%s", want, s)
	}
	dirs := planDirs(t, r.log)
	if len(dirs) != 2 {
		t.Fatalf("the installation step planned %d times, want 2 (the apply that creates the service, then the audience): %v\n%s", len(dirs), dirs, s)
	}
	for i, dir := range dirs {
		if dir != r.root() {
			t.Errorf("plan %d ran in %s, want %s: the workdir the run writes its %s into and the root its terraform plans in are one directory, or the plan reads variables that are not there (the report's \"Given variables file %s does not exist\")\n%s",
				i+1, dir, r.root(), infra.VarsFile, infra.VarsFile, s)
		}
	}
	// The file the plans read is the run's own, with the URL the step wrote in
	// it: the fake refuses a -var-file that is not there, so the two plans
	// above could not have passed with the write lost.
	svc := credentialsServiceEntry(t, r)
	if got := svc["url"]; got != credentialsServiceURL {
		t.Errorf("credentials_service.url = %v in the installation's %s, want %s\n%s", got, infra.VarsFile, credentialsServiceURL, s)
	}
}

// planDirs are the working directories of the run's own plans, in order: the
// directory terraform reads the relative -var-file=terraform.tfvars.json from.
func planDirs(t *testing.T, log string) []string {
	t.Helper()
	f, err := os.Open(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<24)
	for s.Scan() {
		var c struct {
			Args []string `json:"args"`
			Dir  string   `json:"dir"`
		}
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) > 0 && c.Args[0] == "plan" {
			out = append(out, c.Dir)
		}
	}
	return out
}

// TestUpdateKeepsAnOptedInInstallationsServiceUnchanged: an installation that
// already has the block keeps it. The block is the record of a service that
// exists — its URL is the audience every caller's ID token is minted for, and
// its job table is the installation's own — so the run carries it as it is
// found and moves the ONE member it owns, the image.
//
// Mutation (verified, run and restored): rebuild the block from the spec
// (infra.Credentials) instead of carrying it, and this test fails on the
// recorded URL: the module's unassigned placeholder goes back on the revision
// and the service admits nobody.
func TestUpdateKeepsAnOptedInInstallationsServiceUnchanged(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation that has the service failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	want := map[string]any{
		"account_id":       infra.CredentialsAccountID,
		"service":          infra.CredentialsServiceName,
		"image":            r.credentialsRef(),
		"bundle_secret":    "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358",
		"firebase_project": fpID,
		"rtdb_url":         r.db.URL,
		"job_accounts": []any{map[string]any{
			"provider": "bitbucket", "repo": "acme/sandbox", "workflow": "web"}},
		"url": credentialsServiceURL,
	}
	got, err := json.Marshal(svc)
	if err != nil {
		t.Fatal(err)
	}
	expect, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expect) {
		t.Errorf("credentials_service = %s, want %s: the block is carried byte for byte with the image moved to this release's, and nothing else\n%s", got, expect, s)
	}
}

// seedCheckoutGitSecret stores repo's git credential as `fugaro secrets set`
// would leave it, so a converge run from a checkout of repo finds the value its
// jobs mount and reaches the stages behind the secrets one.
func seedCheckoutGitSecret(t *testing.T, r *imagesRig, repo string) {
	t.Helper()
	slug, err := task.Slug("bitbucket", repo)
	if err != nil {
		t.Fatal(err)
	}
	r.sm.Seed(gcp.SecretID(slug, config.BitbucketSecret), map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: repoLabel(t, slug), gcp.LabelSecret: config.BitbucketSecret,
	}, []byte(credsValue(t, "git")))
}

// TestUpdateRefreshesTheServicesJobTable: the table is the one member of the
// block `fugaro update` composes rather than carries, and the deploy is its
// only other writer — so a repository onboarded after that deploy has to enter
// the table on the next run, and it never did: every later run carried the
// block as it found it, and the service then refuses every call from a job
// account whose row it does not hold (internal/credsvc derives the repository
// from the row) while that repository's own root has already granted the job
// roles/run.invoker on the service. No other command could repair it, which is
// what makes it a HARD RULE failure rather than an inconvenience.
//
// The table is refreshed by UNION: rows the deploy wrote are kept, because a
// dropped row is a refused job while a stale row binds an account no token is
// ever minted for (infra.CredentialsJobAccounts' own comment, and
// infra.RefreshCredentialsServiceJobAccounts). So an installation whose config
// has not changed still converges on the same block, byte for byte, which is
// what TestUpdateKeepsAnOptedInInstallationsServiceUnchanged pins.
//
// Mutation (verified, run and restored): drop the
// infra.RefreshCredentialsServiceJobAccounts call in init_stages.go's
// pinCredentialsImage → this fails on the config's own repository, which the
// table then does not hold.
func TestUpdateRefreshesTheServicesJobTable(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	// The table the deploy left: a repository this machine's config no longer
	// describes, which is what stands in for "the config has moved on since".
	setCredentialsJobTable(t, r, []any{map[string]any{
		"provider": "bitbucket", "repo": "acme/old", "workflow": "web"}})
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation whose job table is stale failed: %v\n%s", err, s)
	}
	got, _ := credentialsServiceEntry(t, r)["job_accounts"].([]any)
	want := []any{
		map[string]any{"provider": "bitbucket", "repo": "acme/old", "workflow": "web"},
		map[string]any{"provider": "bitbucket", "repo": "acme/sandbox", "workflow": "web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("credentials_service.job_accounts = %v, want %v: the config's own repository job enters the table on the next run, and the row the deploy wrote stays — the service refuses a job whose row it does not hold, and nothing else writes this table\n%s", got, want, s)
	}
}

// TestAnOnboardingConvergeNamesTheNewRepositoriesJobs: the run that onboards a
// repository is the run that grants its jobs roles/run.invoker on the
// credentials service, so its jobs have to enter the table in that same run —
// the service admits the callers its table holds and refuses every other
// (internal/credsvc/auth.go), and a `cascade` run has no credential of its own
// to fall back to (D143).
//
// The table is composed out of the local config, and the stage order is what
// makes this a whole-command test rather than a unit one: the installation
// stage (third) applies the root and writes the tfvars the table is in, while a
// repository joins the config in the repository stage (ninth) — so the entry
// has to be there before the first stage plans (makeOnboardingRepoEntry, called
// from converge), and it has to carry the workflows, which is what a row is
// derived from. Without it this run's table names the deploy's stale row and
// the config's other repository alone, and the checkout's own jobs are refused
// by the service they were just switched onto until a later update.
//
// The workflow is the checkout's own (app, from its fugaro.yaml), not the
// `web` of the config's other entries: the row is the job the repository's
// root deploys, and that job is named by the repository's document.
//
// Mutation (verified, run and restored): drop the makeOnboardingRepoEntry call
// from init_stages.go's converge and this fails with the table holding
// [acme/old, acme/other] — no row for the repository the run onboarded. Drop
// only the Workflows of the entry it writes and it fails the same way: the row
// is derived per workflow, so an entry with none is a repository whose jobs the
// service still refuses.
func TestAnOnboardingConvergeNamesTheNewRepositoriesJobs(t *testing.T) {
	r := credentialsRig(t)
	r.repoReady(t)
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	// The table the deploy left: a repository this machine's config no longer
	// describes, which is what stands in for "the config has moved on since".
	setCredentialsJobTable(t, r, []any{map[string]any{
		"provider": "bitbucket", "repo": "acme/old", "workflow": "web"}})
	// The bundle holds a model credential, so the repository this run onboards
	// is the state the finding is about: its root is applied with the switch on
	// and its jobs are granted the invoker.
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	// The checkout's repository is NOT in the local config: this is its first
	// onboarding, and the checkout is where its provider comes from.
	withoutSwitchRepo(t, r.initRig, "acme/other")
	// Its git credential is stored, so the converge reaches the repository
	// stage rather than stopping at the secrets one — the stage order is the
	// whole point of this test, and a run that stopped earlier would prove
	// nothing about the table the installation apply wrote.
	seedCheckoutGitSecret(t, r, "acme/sandbox")
	// The run goes to its end: the layer stage and the checkout stage's cloud
	// step read the project's bucket, and the rig's endpoints are fakes, so
	// bucket_url names a directory this machine can read (the layer rigs'
	// own pattern) rather than a gs:// bucket nothing serves.
	bucket := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bucket, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	r.appendConfig(t, "bucket_url: file://"+bucket+"\n")
	r.save(t)
	// The build records live in the runs bucket, which is the same fake
	// problem: the checkout's cloud step writes one there.
	records := t.TempDir()
	openRecords := openRecordBucket
	t.Cleanup(func() { openRecordBucket = openRecords })
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+records) }

	// The workflow runs the base image directly (checkout: clone), so the
	// checkout stage's cloud step has no build to submit: what this test is
	// about is the installation apply and the table it writes, and a billable
	// build of a fixture's own app would only stand between the two.
	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", cloneCheckoutYAML("bitbucket", "cascade", "aurora")))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("the run that onboards the checkout's repository failed: %v\n%s", err, s)
	}
	got, _ := credentialsServiceEntry(t, r)["job_accounts"].([]any)
	want := []any{
		map[string]any{"provider": "bitbucket", "repo": "acme/old", "workflow": "web"},
		map[string]any{"provider": "bitbucket", "repo": "acme/other", "workflow": "web"},
		map[string]any{"provider": "bitbucket", "repo": "acme/sandbox", "workflow": "app"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("credentials_service.job_accounts = %v, want %v: the repository this run onboarded is in the table the same run's installation apply wrote, beside the rows the deploy left and the config's other repository — the service refuses every call from a job account whose row it does not hold, while that job's own root has already been granted roles/run.invoker on it, and the repository's cascade runs then halt with nothing to fall back to (D143)\n%s", got, want, s)
	}
}

// TestUpdateCarriesTheVerifierAccount: the block's verifier_accounts is the
// verifier account §2.2 names — a service account — and never a person's
// address. The list is the effective one (localcfg's keep rule: the owner's own
// when the config states one, the published node's otherwise), and it is
// written into the block on every installation, which is what makes §4.3's
// dependency hold: an installation that deployed with an empty verifier list
// would have CSR10's launcher reader refuse every brief.
//
// Mutation (verified, run and restored): write a person's address in place of
// the account, and this test fails — and the service would not start at all,
// because NewAuthenticator refuses an address that is both a person and a
// verifier (internal/credsvc/auth.go:574).
func TestUpdateCarriesTheVerifierAccount(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	// The node a previous `fugaro update` published, naming the installation's
	// verifier account: this machine's config is silent about the key, so the
	// node is where the list is (C3.9g2, D1075).
	publishedPolicyForTest(t, r, map[string]any{
		"version":           projectpolicy.Version,
		"verifier_accounts": []any{verifierServiceAccount()},
	})
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation deploying the service failed: %v\n%s", err, s)
	}
	got, _ := credentialsServiceEntry(t, r)["verifier_accounts"].([]any)
	if len(got) != 1 || got[0] != verifierServiceAccount() {
		t.Fatalf("credentials_service.verifier_accounts = %v, want the published node's list [%s]\n%s", credentialsServiceEntry(t, r)["verifier_accounts"], verifierServiceAccount(), s)
	}
	for _, a := range got {
		if addr, _ := a.(string); strings.HasPrefix(addr, "user:") || strings.Contains(addr, "@example.com") {
			t.Errorf("credentials_service.verifier_accounts names %q, a person: the verifier is a dedicated service account, and the service refuses to start with an address that is both (credsvc/auth.go:574)", addr)
		}
	}

	// And with nothing anywhere naming a verifier, the block carries no member
	// at all: that is the state the module's own default is for — the address of
	// the verifier account the same apply creates (verifier.tf) — which is a
	// service account too, and the run records it in the config so the next
	// update's block names it.
	plain := credentialsRig(t)
	plain.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, plain)
	out, errOut, err = executeStdin(t, "", "update", "--yes")
	s = out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation with no verifier named failed: %v\n%s", err, s)
	}
	if got, ok := credentialsServiceEntry(t, plain)["verifier_accounts"]; ok {
		t.Errorf("credentials_service.verifier_accounts = %v on an installation whose config and node both name nobody, want the member absent: an absent member is the module's variable null, which is the state its default — the verifier account this apply creates — is for\n%s", got, s)
	}
	if got := verifierListOf(localConfigOf(t, plain)); !sameList(got, []string{verifierServiceAccount()}) {
		t.Errorf("the local config names verifier_accounts = %v, want [%s]: the address is a fact of the configuration (infra.VerifierAccountID), which is what lets one update create the account and name it\n%s", got, verifierServiceAccount(), s)
	}
}

// TestProvisionCredentialsFlagIsGone: the flag is REMOVED, not aliased (the
// owner's ruling of 2026-11-05, §3 DON1). A no-op flag whose name and --help
// text say "opt in" is a lie in the one surface a user reads to learn what a
// command does, and `fugaro update` is the whole upgrade, so there is nothing
// for the flag to do. The refusal names the command that deploys the service
// now, because the error is what a user who read an older document sees.
func TestProvisionCredentialsFlagIsGone(t *testing.T) {
	for door, build := range doorCommands {
		cmd := build()
		if f := cmd.Flags().Lookup(retiredCredentialsFlag); f != nil {
			t.Errorf("fugaro %s still declares --%s: it is removed, not aliased", door, retiredCredentialsFlag)
		}
		for _, text := range []string{cmd.Flags().FlagUsages(), cmd.Long} {
			if strings.Contains(text, retiredCredentialsFlag) {
				t.Errorf("fugaro %s's help offers --%s; the service is deployed by every fugaro update", door, retiredCredentialsFlag)
			}
		}
	}
	for _, door := range []string{"setup", "update"} {
		firstRunEnv(t)
		out, errOut, err := executeStdin(t, "", door, "--"+retiredCredentialsFlag)
		s := out + errOut + ErrorText(err)
		if err == nil {
			t.Fatalf("fugaro %s --%s exited 0:\n%s", door, retiredCredentialsFlag, s)
		}
		if got := ExitCode(err); got != ExitUserError {
			t.Errorf("fugaro %s --%s: exit %d, want %d", door, retiredCredentialsFlag, got, ExitUserError)
		}
		if !strings.Contains(s, "--"+retiredCredentialsFlag) {
			t.Errorf("fugaro %s's refusal does not name the flag that is gone:\n%s", door, s)
		}
		if !strings.Contains(s, "fugaro update") {
			t.Errorf("fugaro %s's refusal does not name the command that deploys the service now:\n%s", door, s)
		}
		if strings.Contains(s, "unknown flag") {
			t.Errorf("fugaro %s's refusal is pflag's own sentence rather than the removal's:\n%s", door, s)
		}
	}
}

// TestUpdatePinsTheServiceImageByDigest: the block's image is the released
// image pinned BY DIGEST — the reference the signature was verified for and the
// digest was read back through the installation's remote repository at, never
// the tag alone. The module's own validation refuses a tag at plan time, so a
// deploy that wrote one would be refused by its own plan; the reason is not
// tidiness: the service runs as the account that reads every credential in the
// bundle, while every operator of the project holds
// roles/artifactregistry.writer on the registry the image comes from.
//
// The tag-only form is refused before anything is written, which the
// development-build case below shows: there is no released image to resolve, so
// there is no reference to write at all.
func TestUpdatePinsTheServiceImageByDigest(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	image, _ := credentialsServiceEntry(t, r)["image"].(string)
	if image != r.credentialsRef() {
		t.Errorf("credentials_service.image = %q, want %q (the verified digest read back through the remote repository)", image, r.credentialsRef())
	}
	if !strings.HasSuffix(image, "@"+r.credentialsDigest()) {
		t.Errorf("the image is not pinned by the verified digest: %q", image)
	}
	if r.arCalls == 0 {
		t.Error("the digest was written without being read back through the remote repository")
	}
	// Two applies of a first deploy, and they are the audience's whole story:
	// the installation stage's own, which creates the service — its revision
	// then carries the module's unassigned placeholder, because the URL does not
	// exist until that apply returns — and fillCredentialsAudience's, which puts
	// the module's own output on it. Both happen before the stage reports done,
	// so an installation this run calls converged is one that serves.
	if apps := r.ran(t, "apply"); len(apps) != 2 {
		t.Errorf("applies = %v, want the installation root twice (the installation stage's, and the audience's)", apps)
	}

	// A development build has no released image, so there is nothing to resolve
	// and nothing to write: the deploy is skipped rather than failed, because a
	// build from a checkout cannot break the rest of its own upgrade over an
	// image only a release publishes (CLAUDE.md's HARD RULE), and nothing is left
	// stale — the next released run deploys the service.
	dev := newImagesRig(t, "dev")
	dev.setPlanAt(t, "installation", change("module.installation.google_storage_bucket.runs", "update"))
	dev.appendConfig(t, "budget: { mode: observe, per_run_usd: 5, firebase_project: "+fpID+", rtdb_url: "+dev.db.URL+" }\n")
	dev.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	out, errOut, err = executeStdin(t, "", "update", "--yes")
	s = out + errOut
	if err != nil {
		t.Fatalf("a development build's update failed instead of deploying nothing: %v\n%s", err, s)
	}
	if _, ok := dev.tfvars(t)["credentials_service"]; ok {
		t.Error("a development build wrote credentials_service")
	}
	if dev.resolveCalls != 0 {
		t.Errorf("a development build resolved %d image(s): it has no released image to resolve", dev.resolveCalls)
	}
}

// An installation that cannot carry the service deploys nothing, and the
// update still converges everything else. The two refusals infra.Credentials
// makes are the installation's own state: no Firebase backend to read (its
// budget block names no project or database, so the service would have no
// database to read and no configuration at all), and no repository job for the
// service to admit (the service refuses a table that binds no run to a
// repository, credsvc/auth.go's NewAuthenticator).
//
// This is the HARD RULE read at the deploy: a state that cannot carry the
// service must not turn the one upgrade command into a failure, and the line
// the run prints is what tells the owner which half is missing.
func TestUpdateWithoutAFirebaseBackendDeploysNothing(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_storage_bucket.runs", "update"))
	// The config without the budget block: an installation that has not run
	// `fugaro update --firebase-project` yet.
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "budget: {") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an installation with no Firebase backend could not be updated: %v\n%s", err, s)
	}
	if !strings.Contains(s, "the credentials service is not deployed") {
		t.Errorf("the run said nothing about the service it did not deploy:\n%s", s)
	}
	if _, ok := r.tfvars(t)["credentials_service"]; ok {
		t.Errorf("an installation with no Firebase backend was given credentials_service: %v", r.tfvars(t)["credentials_service"])
	}
}

// verifierAccountsEnv is the entry the installation module renders for the
// verifier list, at the key infra.Credentials writes it under. The two rows
// below are the ones the golden fixture uses, because they are the two shapes
// a verifier really has: a person's address, and the coordinator's own service
// account.
var verifierAccountsEnv = []string{"person-1@example.com", "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"}

// publishedPolicyForTest writes a policy node at the path the published policy
// lives at, with the generation marker of the deployed rules beside it: a node
// exists only under rules that carry it (projectpolicy.Read answers "no policy
// published" below PolicyRulesVersion, and the rules deploy writes the
// marker), so a rig that seeded one without the other would be an installation
// that cannot be.
func publishedPolicyForTest(t *testing.T, r *imagesRig, node map[string]any) {
	t.Helper()
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	r.db.Set(projectpolicy.PathShared(), node)
}

// publishedVerifiersForTest writes a policy node carrying the verifier list at
// the path the published policy lives at, which is what a previous `fugaro
// update` left there.
func publishedVerifiersForTest(t *testing.T, r *imagesRig) {
	t.Helper()
	publishedPolicyForTest(t, r, map[string]any{
		"version":           projectpolicy.Version,
		"verifier_accounts": verifierAccountsEnv,
	})
}

// TestCredentialsServiceKeepsThePublishedVerifierList: the verifier list is
// the one value the block takes from the PUBLISHED node rather than from this
// machine's configuration (C3.9g2, D1075). A second operator's config states
// none — it was written before the key existed — and the node is where the
// owner's list is, so a deploy that read the configuration alone would render
// the service's variable null and empty a verify route another machine set up.
//
// Mutation (verified, run and restored): pass nil to infra.Credentials and
// never let prepare write the member, and this test fails on the block's list.
func TestCredentialsServiceKeepsThePublishedVerifierList(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	publishedVerifiersForTest(t, r)
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	svc := credentialsServiceEntry(t, r)
	got, _ := svc["verifier_accounts"].([]any)
	if len(got) != len(verifierAccountsEnv) {
		t.Fatalf("credentials_service.verifier_accounts = %v, want the published node's list %v", svc["verifier_accounts"], verifierAccountsEnv)
	}
	for i, want := range verifierAccountsEnv {
		if got[i] != want {
			t.Errorf("verifier_accounts[%d] = %v, want %v", i, got[i], want)
		}
	}
}

// TestCredentialsServiceClearsTheListOnAnExplicitEmptyOne: writing an empty
// list is how the owner clears the route, and it is the only spelling that
// does — deleting the key keeps the node's list, which the test above pins.
//
// The entry is written as an empty ARRAY and never dropped, and that is the
// whole difference between the owner's clear and the module's default: an
// absent member is the module's variable null, which is the state the default
// — the verifier account this installation creates — is for (verifier.tftest
// .hcl pins null → the account alone). A clear that dropped the key would
// therefore hand the service exactly the caller the owner switched off
// (CSR14).
func TestCredentialsServiceClearsTheListOnAnExplicitEmptyOne(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "create"))
	credentialsOutputs(t, r)
	publishedVerifiersForTest(t, r)
	r.appendConfig(t, "verifier_accounts: []\n")
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	svc := credentialsServiceEntry(t, r)
	got, ok := svc["verifier_accounts"]
	if !ok {
		t.Fatalf("credentials_service.verifier_accounts is absent on a written empty list, want []: an absent member is the module's variable null, which is the state the module's default (the verifier account this installation creates) is for, so the clear would admit the caller the owner switched off")
	}
	if arr, ok := got.([]any); !ok || len(arr) != 0 {
		t.Errorf("credentials_service.verifier_accounts = %v on a written empty list, want [] (nobody)", got)
	}
}

// credsRefreshYAML is acme/app's fugaro.yaml with the one value the drift
// note's credentials clause is about: agent.auth: cascade, the mode whose
// credential comes from the installation's credentials service at call time
// (C1.1 offers it, C1.19 refuses a launch without the service).
const credsRefreshYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: cascade }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// upgradeRigWithCascade is upgradeRigWithGitHubAppID's rig for a checkout of
// that file: jobConfigCopyNote calls infra.Workflow and openCloudFor
// directly, unstubbed, so it needs a real repository entry and a runs bucket
// with the installation's project marker.
func upgradeRigWithCascade(t *testing.T) *anchorModeRig {
	t.Helper()
	fakeTerraform(t)
	upgradeEnv(t, "0.5.1")
	r := newAnchorModeRig(t, credsRefreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	runs := t.TempDir()
	r.appendConfig(t, "bucket_url: file://"+runs+"\n")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project.json"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestUpgradeNoteNamesTheStage (plan C1.20): a version upgrade is where the
// operator of a cascade repository learns that two OWNER-RUN steps stand
// between the file they just edited and a run that works — the note names
// both, in the order they have to run, and never suggests that upgrading
// does any of it.
//
// The clause is orthogonal to the drift the rest of the note is about, so it
// is owed even when every deployed job's env copy matches (the case here),
// and that is what makes it its own reason to speak: without it the whole
// note would stay silent for exactly the operators who need it.
//
// Mutation (verified, run and restored): make jobConfigCopyCredentialsNote
// return "": this test fails, finding no clause at all.
func TestUpgradeNoteNamesTheStage(t *testing.T) {
	r := upgradeRigWithCascade(t)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"note: the deployed job's environment matches the local project config",
		"agent.auth: cascade",
		"must mount no model credential at all",
		"fugaro update --firebase-project",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q:\n%s", want, out)
		}
	}
	// The service is the identity the repository jobs' model credentials move
	// to, so the note must not read as something an upgrade does on its own.
	if strings.Contains(out, "agent.auth: oauth") {
		t.Errorf("the note reports the wrong auth mode:\n%s", out)
	}
}

// The clause is silent for every other auth: a repository whose jobs mount
// their own credential has nothing to be told about the service, and a note
// that named it anyway would be noise the operator learns to skip.
func TestUpgradeNoteCredentialsClauseIsSilentWithoutCascade(t *testing.T) {
	for _, auth := range []string{"", "oauth", "api-key"} {
		cfg := &config.Config{}
		cfg.Agent.Auth = auth
		if got := jobConfigCopyCredentialsNote(cfg); got != "" {
			t.Errorf("the clause speaks for agent.auth %q: %q", auth, got)
		}
	}
	cfg := &config.Config{}
	cfg.Agent.Auth = config.AuthCascade
	if got := jobConfigCopyCredentialsNote(cfg); !strings.Contains(got, "fugaro update --firebase-project") || !strings.Contains(got, "fugaro update --repo .") {
		t.Errorf("the clause does not name the steps for %s: %q", config.AuthCascade, got)
	}
}

// --print-vars reads the workdir and no cloud, so it carries the block the
// workdir holds as it is found — verifier_accounts included
// (CarriedCredentialsServiceAsFound): the print is what a person reads to see
// what their installation holds, and a document that dropped the owner's list
// would disagree with the installation it stands for.
func TestPrintVarsCarriesTheCredentialsService(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsServiceWithVerifiers(t, r, oldCredentialsRef("1.2.2"), verifierAccountsEnv)
	out, errOut, err := executeStdin(t, "", "update", "--yes", "--print-vars")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--print-vars did not print a document: %v\n%s", err, out)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("--print-vars printed no credentials_service for an installation that holds one:\n%s", out)
	}
	if svc["image"] != oldCredentialsRef("1.2.2") {
		t.Errorf("--print-vars printed image %v, want the workdir's own %s: this path reads no cloud, so it has no release to pin", svc["image"], oldCredentialsRef("1.2.2"))
	}
	got, _ := svc["verifier_accounts"].([]any)
	if len(got) != len(verifierAccountsEnv) {
		t.Errorf("--print-vars printed verifier_accounts = %v, want the workdir's list %v as it is found", svc["verifier_accounts"], verifierAccountsEnv)
	}
}

// The upgrade's own step (0.15.0). An installation that has the service runs
// the credentials image of the release that pinned it and no other, and until
// this task NOTHING moved it: the writer ran only under
// --provision-credentials — before this task, that is. An installation that
// opted in under an earlier release ran that release's credentials image and no
// other, and NOTHING moved it: an ordinary run rewrote the tfvars from a spec
// that never carries the key and carried the PREVIOUS document's entry forward
// verbatim (infra.CarriedCredentialsService, init.go's prepare) — image and
// all. So an installation that opted in under an earlier release stayed on that
// release's image for ever, and every later release's behavior that the image
// decides (0.15.0's: the process serves HTTP/2 cleartext, so the module names
// the container's port h2c and Check 190's large push stops being cut at the
// HTTP/1 body limit) never reached it. CLAUDE.md's hard rule is that the
// upgrade carries its own migration and `fugaro update` is the whole of it.
//
// The deploy of the block itself is on every installation now (this task), so
// this is the carry half: an installation that HAS the block keeps every fact
// of it and only the image moves.
//
// Mutation (verified, run and restored): return from pinCredentialsImage before
// it resolves (the carry this test exists to replace): the tfvars keep 1.2.2's
// image and this test fails.
func TestUpdateRepinsTheCredentialsImageOnAnOrdinaryUpdate(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation that has the service failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	if got := svc["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v, want %s: an ordinary update left the installation on the image of the release it opted in under, so the release's own process never reaches it\n%s", got, r.credentialsRef(), s)
	}
	// The digest is the resolve-and-verify step's own and not a value this run
	// composed: the release's "-pinned" tag, its signature, and the read-back
	// through the remote repository all happened, once for the run.
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("resolve calls %d, read-backs %d, want one of each: the image is pinned only through the step that verifies the signature of the digest it names", r.resolveCalls, r.arCalls)
	}
	// Everything else the entry holds is what the earlier run wrote: the
	// re-pin is a change of the ONE value, not a rebuild of the service's
	// facts.
	for k, want := range map[string]any{
		"account_id": infra.CredentialsAccountID, "service": infra.CredentialsServiceName,
		"firebase_project": fpID, "url": credentialsServiceURL,
		"bundle_secret": "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358",
	} {
		if svc[k] != want {
			t.Errorf("credentials_service.%s = %v, want %v: the re-pin rewrote a fact of the service it does not own", k, svc[k], want)
		}
	}
	// One apply, and no audience fill: the block already carries the URL the
	// module's own output does, which is every run after the one that deployed
	// the service (prepare carries it forward), so there is nothing for the
	// second apply to change.
	if apps := r.ran(t, "apply"); len(apps) != 1 {
		t.Errorf("applies = %v, want the installation root's alone", apps)
	}

	// The next ordinary update is the converged one, and it asks the registry
	// nothing: the reference already names this release, so there is no image
	// to move and no reason to reach the network — which is also why an owner
	// whose registry is briefly unreachable updates as they did before this
	// migration existed.
	if out, errOut, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("the update after the re-pin failed: %v\n%s%s", err, out, errOut)
	}
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("the converged update resolved %d image(s) and read %d back, want the first run's one and one: the re-pin is needed once per release and not once per run", r.resolveCalls, r.arCalls)
	}
	if got := credentialsServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("the converged update's image = %v, want %s", got, r.credentialsRef())
	}
}

// update --firebase-project re-pins too. The flag moves the installation's
// first apply into the Firebase stage (the installation stage skips: "applied
// as the first step of the firebase backend"), so newFirebaseStage is where an
// opted-in owner's only re-pin happens on this path — and the hard rule's one
// command has to converge them here as it does without the flag, or the
// installation with the Firebase backend stays on the previous release's
// credentials image and the h2c port this release's module names is never what
// the service runs.
func TestUpdateFirebaseProjectRepinsTheCredentialsImage(t *testing.T) {
	r := credentialsRig(t)
	// The mark the door wrote when the installation was set up: the Firebase
	// stage refuses a database that holds data and is not this installation's,
	// and an installation that can be updated is one it already wrote.
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	// The Firestore database the installation was set up with: the rig starts
	// without one (a fresh Firebase project has none), and the stage asks the
	// owner for a location and creates it when it finds none — which is a setup
	// question, not the update this test is about.
	r.fs.SetDatabase("us-east5")
	// The installation root's outputs as this path reads them: what
	// credentialsOutputs answers with, AND the history account the Firebase
	// stage reads back. credentialsOutputs replaces the rig's script rather
	// than adding to it, and this is the one test that needs both objects from
	// the same run, so the two are composed here.
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service":     map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
		"history_service_account": historyAccount,
		"history_job":             nil,
	})}
	r.save(t)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID, "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("update --firebase-project of an opted-in installation failed: %v\n%s", err, s)
	}
	if got := credentialsServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v, want %s: update --firebase-project skipped the re-pin, so an installation whose first apply is the Firebase stage never reaches this release's image\n%s", got, r.credentialsRef(), s)
	}
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("resolve calls %d, read-backs %d, want one of each: the image is pinned only through the step that verifies the signature of the digest it names, once for the run", r.resolveCalls, r.arCalls)
	}
}

// TestTheFirebaseProjectDoorBackfillsTheRecordToo: the same door owes this
// upgrade's per-repository record, because it applies the installation root
// itself and runs no installation stage — so it never reaches install(), which
// is the other call site of the backfill (internal/cli/init.go).
//
// An installation that took the credentials service before this release and
// whose FIRST post-upgrade command is `fugaro update --firebase-project <id>`
// is the state: the earlier apply already dropped its repositories' model
// mounts and granted their accounts roles/run.invoker, so a record left for
// the next plain update is a `cascade` run launched with no model credential
// of its own and no address to call (D143) — the second command CLAUDE.md's
// HARD RULE forbids.
//
// Mutation (verified, run and restored): drop the
// backfillCredentialsServiceReady call from initFirebase and this fails with
// acme/sandbox carrying no record after the flag's run.
func TestTheFirebaseProjectDoorBackfillsTheRecordToo(t *testing.T) {
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	r := credentialsRig(t)
	r.db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.fs.SetDatabase("us-east5")
	r.appendConfig(t, "credentials_service: { url: "+credentialsServiceURL+" }\n")
	// The installation's own outputs, as the Firebase stage reads them back: the
	// credentials block this door re-pins AND the history account the stage
	// needs (the composed script is TestUpdateFirebaseProjectRepinsTheCredentialsImage's).
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service":     map[string]any{"name": infra.CredentialsServiceName, "region": "us-east5", "url": credentialsServiceURL},
		"history_service_account": historyAccount,
		"history_job":             nil,
	})}
	r.save(t)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	// The bundle holds a model credential, and the installation's own state
	// says this repository's root was applied with the service on: the two
	// things the backfill judges an entry by.
	putSwitchBundle(t, r.initRig, []byte(config.AnthropicSecret+": "+credsValue(t, "model")+"\n"))
	seedRepoStateGrant(t, r.initRig, slug)

	out, errOut, err := executeStdin(t, "", "update", "--firebase-project", fpID, "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("update --firebase-project failed: %v\n%s", err, s)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the local config does not parse: %v", err)
	}
	if !lc.Repos["acme/sandbox"].CredentialsServiceOn() {
		t.Errorf("acme/sandbox carries no record after `fugaro update --firebase-project`, though its root was applied with the service on and this run read a bundle holding a model credential: this door applies the installation root itself and runs no installation stage, so the backfill install() makes is the one it owes too — without it the upgrade's record waits for a second, plain update, and a cascade run in between is launched with no model mount of its own and no address to call (D143)\n%s", s)
	}
}

// The upgrade between the two releases this one follows: ONE `fugaro update`
// takes an installation that opted in under the previous release to this
// release's credentials image — the one whose process serves HTTP/2 cleartext,
// which is the release the module's own local counts as the first h2c-serving
// one and the tftest fixtures name as the pair (the run that asserts the port
// is named h2c, and the run before it that asserts no port at all).
//
// This is the migration CLAUDE.md's hard rule is about, and it is one run: the
// owner upgrades the CLI and runs the one documented command. The entry's other
// facts converge too — a re-pin that rebuilt them from the flag-less spec would
// drop the audience the service's callers mint their ID tokens for, which
// admits nobody.
func TestInstallationAtThePreviousVersionConvergesInOneUpdateCredentials(t *testing.T) {
	r := credentialsRigAt(t, "0.15.0")
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("0.14.2"))
	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("one update from the previous release failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	if got, want := svc["image"], r.credentialsRefAt("0.15.0"); got != want {
		t.Errorf("credentials_service.image = %v, want %s: one update from the previous release must reach this release's verified image, or the installation keeps serving the previous release's process and the h2c port the module names for this one is never what it runs\n%s", got, want, s)
	}
	if svc["url"] != credentialsServiceURL || svc["rtdb_url"] != r.db.URL {
		t.Errorf("the converged entry lost a fact the previous release wrote (%v): the re-pin changes the image and nothing else", svc)
	}
	if r.resolveCalls != 1 {
		t.Errorf("resolve calls %d, want 1: one update is one resolve, and it is the release's own signed image the run pins", r.resolveCalls)
	}
}

// TestUpdateOutsideACheckoutConvergesTheReviewAdmissionGate is the convergence
// test CLAUDE.md's HARD RULE requires for checkReviewAdmission, and the fourth
// review round's finding 2: the gate refuses a review launch while the
// installation's own record of its credentials service predates
// reviewAdmissionSince, so the one documented command has to move THAT record —
// and it has to do it here, in the case the gate was first written wrong about.
//
// The installation is the one between `brew upgrade` and `fugaro update`: an
// older service image in the tfvars, and a base image older than this release
// too. The base image is the trap. `fugaro update` re-pins it only in a run
// that handles that kind at all — imagesStage.kinds names `base` for --base, a
// checkout's workflows, or a catalog build, and for nothing else — so the
// common update, run from a directory that is not a repository, leaves it
// exactly where it was. A gate read off base_images.base therefore refuses
// every review on an installation this same update has already converged, and
// its remedy ("Run fugaro update") changes nothing: the owner is sent round a
// loop with no exit. The service image is the record every update moves, and
// this test is both halves: refused before the update, admitted after it, with
// the base pin asserted to be unmoved, which is what makes the second half
// evidence about the judge rather than about the rig.
func TestUpdateOutsideACheckoutConvergesTheReviewAdmissionGate(t *testing.T) {
	r := credentialsRigAt(t, reviewAdmissionSince)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("0.20.3"))
	// The base pin the last in-checkout update left, at a release this one
	// postdates: r.Chdir(t.TempDir()) in the rig puts this run outside any
	// checkout, so the images stage names no base kind and the pin stays.
	staleBase := r.pinnedBase(config.BaseKind, "0.20.3")
	r.appendConfig(t, "base_images: {base: "+staleBase+"}\n")

	if rel, _, ok := installationServiceRelease(localConfigOf(t, r)); !ok || rel != "0.20.3" {
		t.Fatalf("the seeded installation records service release %q (ok %v), want 0.20.3: this test's first half is only about a service that predates the class", rel, ok)
	}
	if _, err := checkReviewAdmissionWarn(localConfigOf(t, r)); err == nil {
		t.Fatal("a review launch from an installation whose service predates the class was admitted before the update")
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("one ordinary update of an installation whose service predates the class failed: %v\n%s", err, s)
	}

	rel, image, ok := installationServiceRelease(localConfigOf(t, r))
	if !ok || rel != reviewAdmissionSince {
		t.Errorf("the installation records service release %q (ok %v, image %s) after one ordinary update, want %s: the update has to move the record the admission gate judges, or the refusal's own remedy leaves the launch refused\n%s", rel, ok, image, reviewAdmissionSince, s)
	} else if want := r.credentialsRefAt(reviewAdmissionSince); image != want {
		t.Errorf("the installation records the service image %s after one ordinary update, want %s", image, want)
	}
	if warn, err := checkReviewAdmissionWarn(localConfigOf(t, r)); err != nil {
		t.Errorf("a review launch is still refused after one ordinary update: %v (warning %q): `fugaro update` is the whole upgrade, and a gate whose remedy does not clear it is an operator step nobody was told to run", err, warn)
	}
	if got := localConfigOf(t, r).BaseImages[config.BaseKind]; got != staleBase {
		t.Errorf("base_images.base = %s after the update, want the pin it started from (%s): the gate must not be read off a record an update outside a checkout never writes", got, staleBase)
	}
}

// The re-pin fails, and the opt-in survives it (the review's finding 1). The
// resolve reaches cosign and the registry, so it fails for reasons that have
// nothing to do with the installation: no cosign on PATH, a wrong-identity
// signature, a registry blip. What must NOT happen is the trap the workdir
// order sets: PrepareWorkdir rebuilds the installation's tree (deleting the
// tfvars) before the installation stage runs, so a run that errors between
// that rebuild and prepare's rewrite leaves NO tfvars file at all — and the
// next run reads that as "never opted in", plans the service, its account, its
// grant and its bundle away, and the bundle container's prevent_destroy
// refuses the plan. The installation could then never be updated again without
// a manual repair: a transient registry failure would have cost the owner an
// operator step, and CLAUDE.md's hard rule says the upgrade leaves none.
//
// The refusal is the other half of the same finding, and the reason this test
// exists rather than a softer one: the resolve failing must FAIL the run, not
// be swallowed into "the image did not move, carry on" (a silent return nil
// here applies the installation with the previous release's image and reports
// the service as moved), and never a digest nothing verified.
func TestUpdateWithAFailedCredentialsRepinKeepsTheOptIn(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	seeded, err := os.ReadFile(filepath.Join(r.root(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	r.resolveErr = errors.New("the signature of dimipaun/fugaro-credentials:1.2.3-pinned did not verify")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err == nil {
		t.Fatalf("an update whose credentials image could not be resolved reported success: the installation is applied on the previous release's image while the run says it moved the service\n%s", s)
	}
	if !strings.Contains(err.Error(), "did not verify") {
		t.Errorf("the run failed with %v, want the resolve's own refusal — an update that cannot reach the registry cannot report the service as moved either\n%s", err, s)
	}
	// The write-back is not a half-built document an aborted run left behind:
	// nothing removes a workdir on the error path (the next run's PrepareWorkdir
	// is what replaces the tree), so these bytes are what the installation is
	// left holding, and what every reader of this file reads next — prepare and
	// --print-vars carry the keys this run's spec never holds forward FROM them,
	// and installationFenceRecorded reads the managed deployer's fence out of
	// them. They are the earlier run's document entire, so the file and the
	// snapshot the next run takes agree, and no clean-up step is owed here: a
	// deletion would be the finding this test exists for.
	after, err := os.ReadFile(filepath.Join(r.root(), infra.VarsFile))
	if err != nil {
		t.Fatalf("the failed update left no tfvars to read back: %v\n%s", err, s)
	}
	if string(after) != string(seeded) {
		t.Errorf("the failed update left tfvars that are not the document it started from: %s, want %s\n%s", after, seeded, s)
	}
	// The opt-in is where it was, image and all: the run wrote the document
	// the carry is about to use back before it resolved, so the file the next
	// run reads carries the service.
	doc := credentialsVarsOnDisk(t, r)
	if doc == nil {
		t.Fatalf("the failed update left the installation with no tfvars at all: the next run carries no credentials_service forward, plans the service's removal, and the bundle container's prevent_destroy refuses that plan — the installation could then never be updated again\n%s", s)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("the failed update left tfvars carrying no credentials_service: the next run reads that as never having opted in\n%s", s)
	}
	if svc["image"] != oldCredentialsRef("1.2.2") {
		t.Errorf("credentials_service.image = %v, want the previous pin %s kept: nothing was applied, so nothing may say the service moved", svc["image"], oldCredentialsRef("1.2.2"))
	}

	// The next update, this time with the registry reachable, still finds the
	// opt-in and converges — the whole point: one transient failure costs one
	// retry and not an operator step.
	r.resolveErr = nil
	if out, errOut, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("the update after the failed re-pin did not converge: %v\n%s%s", err, out, errOut)
	}
	if got := credentialsServiceEntry(t, r)["image"]; got != r.credentialsRef() {
		t.Errorf("credentials_service.image = %v after the retry, want %s: the opt-in the failed run kept is what the retry re-pinned", got, r.credentialsRef())
	}
}

// An installation whose recorded image names a release NEWER than this CLI's is
// left alone (the review's finding 2). Two machines of one installation are
// upgraded at different times, so the older CLI meets a service that a newer
// release's update already moved: the release in the reference is this
// release's future, and re-pinning it "down" to the version the older binary
// knows would put the previous process back — and the two machines would flip
// the service back and forth on every update, each one undoing the other. It
// is the base image's own rule (init_images.go's baseIsCurrent, which compares
// with newer and never moves a pin back).
func TestUpdateKeepsANewerRecordedCredentialsImage(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	ahead := oldCredentialsRef("1.2.4")
	seedCredentialsService(t, r, ahead)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an update whose installation already runs a later release's image failed: %v\n%s", err, s)
	}
	if got := credentialsServiceEntry(t, r)["image"]; got != ahead {
		t.Errorf("credentials_service.image = %v, want %s untouched: a CLI older than the release the installation runs must not pin the service back to its own\n%s", got, ahead, s)
	}
	if r.resolveCalls != 0 || r.arCalls != 0 {
		t.Errorf("the update resolved %d image(s) and read %d back, want none: there is nothing to move when the recorded release is ahead of this binary's", r.resolveCalls, r.arCalls)
	}
}

// An image that names NO release is replaced by this release's (the review of
// the fix round): the "never move it down" rule compares two releases, and a
// reference that names none — an operator's hand-written digest, pinned by
// digest so the module's validation admits it, and naming no version at all —
// has nothing to compare. It is the case the operators line describes ("an
// operator's hand-written image digest is replaced by the release image"), and
// the re-pin must reach it rather than read it as "a release newer than mine".
//
// The release in the rig is 0.15.0, which is what makes this test the one that
// catches the trap: newer(a, b) splits both sides on "." and atoi's every part,
// so an empty operand reads as one zero part and compares EQUAL to a release
// whose major is 0 — every release this program has cut so far. At 1.2.3 the
// comparison happens to answer "newer" for the wrong reason, which is why the
// rig at what will be the current release is the one that pins this.
func TestUpdateReplacesACredentialsImageThatNamesNoRelease(t *testing.T) {
	r := credentialsRigAt(t, "0.15.0")
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seeded := "us-east5-docker.pkg.dev/" + initProject + "/" + infra.GhcrRegistry +
		"/dimipaun/fugaro-credentials@sha256:" + strings.Repeat("ab", 32)
	seedCredentialsService(t, r, seeded)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an update of an installation whose image names no release failed: %v\n%s", err, s)
	}
	if got, want := credentialsServiceEntry(t, r)["image"], r.credentialsRefAt("0.15.0"); got != want {
		t.Errorf("credentials_service.image = %v, want %s: a reference that names no release is the release this installation runs nobody knows, and leaving it there is an installation stuck on an operator's hand-written digest\n%s", got, want, s)
	}
	if r.resolveCalls != 1 || r.arCalls != 1 {
		t.Errorf("resolve calls %d, read-backs %d, want one of each: the replacement image is the release's, and it is written only through the step that verified it", r.resolveCalls, r.arCalls)
	}
}

// A coding agent's session pins nothing (the review's finding 3). The engine
// stages are PLANNED in such a session rather than skipped, so an ordinary
// `fugaro update` from one does reach the installation stage — and without
// this the re-pin there would run cosign and the registry with the operator's
// own ADC and write the resolved image into the workdir's tfvars. That is the
// resolve and the write the credentials stage's own agent refusal exists to
// keep out of a session (TestInitCredentialsStageRefusedInAgent: "no digest
// resolved, no tfvars written"), and a plan-only run that did them would have
// the operator's credentials spent on a registry read the owner never asked
// for.
//
// The installation stage really ran: the tfvars this test reads are the ones
// prepare wrote, which no earlier stage of an agent's run reaches.
func TestUpdateInAnAgentSessionPinsNoCredentialsImage(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	seedCredentialsService(t, r, oldCredentialsRef("1.2.2"))
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil && !strings.Contains(s, "left for you") {
		t.Fatalf("an ordinary update in a coding agent's session failed with something other than the steps it leaves for the user: %v\n%s", err, s)
	}
	if r.resolveCalls != 0 || r.arCalls != 0 {
		t.Errorf("an agent's session resolved %d image(s) and read %d back: the session plans the installation, so a re-pin there spends the operator's ADC on cosign and the registry for a run that applies nothing", r.resolveCalls, r.arCalls)
	}
	if got := credentialsServiceEntry(t, r)["image"]; got != oldCredentialsRef("1.2.2") {
		t.Errorf("credentials_service.image = %v, want the recorded %s: nothing of the re-pin may run in an agent's session\n%s", got, oldCredentialsRef("1.2.2"), s)
	}
}

// credentialsVarsOnDisk is the installation root's tfvars as the workdir
// holds them now, or nil when the workdir holds none — the difference the
// failed re-pin turns on, which is why this read is not init_test.go's
// tfvars (that one fails the test when the file is missing).
func credentialsVarsOnDisk(t *testing.T, r *imagesRig) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root(), infra.VarsFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the workdir's %s is not a document: %v\n%s", infra.VarsFile, err, b)
	}
	return doc
}

// TestOrdinaryUpdateAdmitsTheVerifierAccountInOneRun is the convergence test
// CLAUDE.md's HARD RULE requires (CSR14): an installation at the PREVIOUS
// version, with the credentials service already on, takes ONE ordinary
// `fugaro update` — the one documented command, with no flag — to the state
// this release ships, and both halves have to hold at the end of that run.
//
// The installation is seeded the way an earlier release left it: the block is
// in the tfvars and carries no verifier_accounts member at all (the member is
// this release's), the local config states no verifier and the published node
// names none. On the tree this test was written against, one ordinary update
// creates the verifier account and admits it for NOBODY: the module renders
// the variable null (today's expression is `null ? [] : [...]`), and the
// policy node the same run publishes names no verifier either — so the
// launcher's verify call and the owner's own check are both refused until a
// second run nobody was told to make.
//
// Half one is the service's own list, and it is asserted as the state the
// module's default is FOR: the block the run leaves carries no member, which
// is what makes the module render the account this same apply creates
// (verifier.tftest.hcl pins null → the account alone). Half two is the node
// the same run published, which is what CSR15's reader and the owner's doctor
// read.
//
// Half two is the half that needs the run's ORDER to be right, and this test
// holds it: the policy stage's plan is made in the read-only preview pass —
// before any stage applies — while the address reaches this machine's config
// from the installation stage's own write, so a stage that kept that plan would
// publish the node the config stated BEFORE it named the verifier and refuse
// its own write one line later ("the published policy was written but still
// does not carry what the local config states"). The rules are seeded CURRENT
// for exactly this reason: a rules stage that deploys drops the stale read on
// its own (init_rules_stage.go), and an installation at the previous version
// whose release changed no rule deploys nothing, so the seeding is what makes
// this a test of the run's order rather than of the rules stage's forget.
// Mutation (verified, run and restored): drop the forget the local-config write
// makes (init.go's writeConfig) and this test fails with that sentence.
func TestOrdinaryUpdateAdmitsTheVerifierAccountInOneRun(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	credentialsOutputs(t, r)
	// The database is this installation's, marked (the ownership check every
	// policy write is behind), and it holds the node the PREVIOUS release
	// published — a node with no verifier member, which is the state this
	// installation is in: nothing anywhere names a verifier, so the module's
	// own account is the default and this run's publish is what names it.
	r.db.Set(budget.PathMark, auroraMark())
	r.db.Set(projectpolicy.PathShared(), map[string]any{"version": projectpolicy.Version})
	// The rules this installation is already read under: the generation this
	// binary deploys AND the document it generates, which is what a project at
	// the previous version holds when the release between them changed no rule.
	// Both halves are seeded, and not the marker alone, because the difference
	// is the whole point of this test: a rules stage that deploys is a stage
	// that drops the policy stage's cached read (init_rules_stage.go), and a
	// run whose rules are current deploys nothing — so the publish below is the
	// policy stage's own read of the config THIS run wrote, made after the
	// installation stage wrote it, and not a plan the rules stage happened to
	// re-make.
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r.db.SetRules(gen)
	r.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	seedCredentialsService(t, r, r.credentialsRef())
	// The literal, as the module writes it (verifier.tf's account_id) and as
	// internal/infra spells it (VerifierAccountID): the address is a fact of the
	// configuration and not of the apply, which is the whole reason one run can
	// both create the account and name it.
	want := "fugaro-verifier@" + initProject + ".iam.gserviceaccount.com"

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("one ordinary update from the previous release failed: %v\n%s", err, s)
	}

	svc := credentialsServiceEntry(t, r)
	if got, ok := svc["verifier_accounts"]; ok {
		t.Errorf("credentials_service.verifier_accounts = %v after one ordinary update, want no member at all: the member's absence is the state the module's default is for, and the default is the account this apply creates\n%s", got, s)
	}

	node, _ := r.db.Value(projectpolicy.PathShared()).(map[string]any)
	got, _ := node["verifier_accounts"].([]any)
	if len(got) != 1 || got[0] != want {
		t.Errorf("the published policy node names verifier_accounts = %v, want [%s]: the service admits the account and the node every reader sees has to name it in the same run, or the launcher's verify call is refused until a second update nobody was told to make\n%s", node["verifier_accounts"], want, s)
	}
}

// TestOrdinaryInitAfterTheOptInNamesTheVerifierAccount is the config writer's
// half of the HARD RULE, and the reason the address is COMPUTED rather than
// read back from an output.
//
// The installation has opted in — the block is in the tfvars, seeded the way
// an earlier release left it, which is the state every opted-in installation
// is in — while the local config is silent about the list and the published
// node names none. The effective list is nil, and that nil is the gate: it is
// the one state the module's default is for (the account this apply creates),
// so it is the one state in which a run writes a name.
//
// The address is a fact of the configuration and not of the apply (the
// module's account_id is a literal, infra.VerifierAccountID), and that is what
// lets ONE ordinary update both create the account and name it: the module's
// output carries the service's name, region and URL and no verifier list at
// all, so nothing in it could name one, and the config is named anyway.
//
// Mutation (verified, run and restored): write the address from a flag or a
// config value — nothing in either names it in this rig — and this test fails;
// take it from the module's output and it fails on the rig whose outputs carry
// no service.
func TestOrdinaryInitAfterTheOptInNamesTheVerifierAccount(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation")
	seedCredentialsService(t, r, r.credentialsRef())
	// The node a previous release published names no verifier: the key is this
	// release's, so every opted-in installation is in this state.
	publishedPolicyForTest(t, r, map[string]any{"version": projectpolicy.Version})
	if got := verifierListOf(localConfigOf(t, r)); got != nil {
		t.Fatalf("the rig's local config already names a verifier (%v): the gate fires only while nothing anywhere names one", got)
	}
	credentialsOutputs(t, r)
	want := "fugaro-verifier@" + initProject + ".iam.gserviceaccount.com"

	out, errOut, err := executeStdin(t, "", "setup", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary init of an opted-in installation failed: %v\n%s", err, s)
	}
	got := verifierListOf(localConfigOf(t, r))
	if len(got) != 1 || got[0] != want {
		t.Errorf("the local config names verifier_accounts = %v after one ordinary update, want [%s]: the account is the default an opted-in installation gets without typing anything, and a run that does not name it leaves the service admitting nobody the module's default is for\n%s", got, want, s)
	}
}

// TestCredentialsServiceKeepsTheOwnersVerifierList is the keep rule at the
// writer that can lose the list: the update's own config write, where the
// tree's sibling TestCredentialsServiceKeepsThePublishedVerifierList holds the
// credentials stage's.
//
// Both directions are here because either one alone is a bug. A config that
// WRITES a list keeps it — the owner's `[]` included, which is their clear —
// and a config that is SILENT while the published node holds the owner's list
// writes nothing, so an update on a second operator's machine leaves that
// node's list as it was.
//
// Mutation (verified, run and restored): gate the write on the local field
// instead of the effective list (localcfg.VerifierAccountsEffective) and the
// silent row fails — a machine whose config merely predates the key replaces
// the owner's list with the default, and the service then refuses the verifier
// the owner named.
func TestCredentialsServiceKeepsTheOwnersVerifierList(t *testing.T) {
	owner := "person-1@example.com"
	rows := []struct {
		name     string
		config   string   // the line the local config states, "" for silent
		node     []string // the list the published node holds, nil for none
		want     []string // the config's list after the run, nil for none
		wantNode []string // the node's list after the run, nil for nobody
	}{
		{
			name:     "a written list is kept",
			config:   "verifier_accounts: [" + owner + "]\n",
			want:     []string{owner},
			wantNode: []string{owner},
		},
		{
			name:     "a written empty list is the owner's clear",
			config:   "verifier_accounts: []\n",
			want:     []string{},
			wantNode: nil,
		},
		{
			name:     "a silent config keeps the node's list",
			node:     verifierAccountsEnv,
			want:     nil,
			wantNode: verifierAccountsEnv,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := credentialsRig(t)
			r.setPlanAt(t, "installation")
			credentialsOutputs(t, r)
			// The database is this installation's, marked (the ownership
			// check every policy write is behind), and the node is the one a
			// previous release published.
			r.db.Set(budget.PathMark, auroraMark())
			node := map[string]any{"version": projectpolicy.Version}
			if row.node != nil {
				node["verifier_accounts"] = row.node
			}
			publishedPolicyForTest(t, r, node)
			if row.config != "" {
				r.appendConfig(t, row.config)
			}
			typedThroughYes(t)
			fakeTerminal(t)
			if out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--yes"); err != nil {
				t.Fatalf("%v\n%s%s", err, out, errOut)
			}
			lc := localConfigOf(t, r)
			if !sameList(verifierListOf(lc), row.want) {
				t.Errorf("the local config names verifier_accounts = %v, want %v: the file's list wins when it states one (the owner's clear included), and a file that states none keeps the node's\n%s", verifierListOf(lc), row.want, readFileString(t, r.cfg))
			}
			if got := nodeVerifiers(t, r); !sameList(got, row.wantNode) {
				t.Errorf("the published node names verifier_accounts = %v, want %v: the list the service is handed and the list every reader of the node sees are the same one\n%s", got, row.wantNode, readFileString(t, r.cfg))
			}
		})
	}
}

// TestOrdinaryUpdateCarriesTheOwnersClearIntoTheBlock is the carried-block
// direction of the same rule: an installation whose local config writes
// `verifier_accounts: []` and whose carried block has no such member — the
// state every opted-in installation is in, because the member is new — takes
// ONE ordinary `fugaro update` to a block whose member is `[]` and a node that
// names nobody, so the service admits nobody and the node every reader sees
// agrees with it.
//
// On the tree this test was written against the member stays absent, and an
// absent member is the module's variable null: the module's default — the
// verifier account this installation creates — is then admitted on a route the
// owner switched off.
//
// Mutation (verified, run and restored): carry the block verbatim and leave
// the member absent, and this test fails on the block's member.
func TestOrdinaryUpdateCarriesTheOwnersClearIntoTheBlock(t *testing.T) {
	r := credentialsRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	// The node the owner's clear is about: it holds a list, which the clear
	// has to reach — a node that kept it would leave the reader and the
	// service disagreeing about who may verify.
	r.db.Set(budget.PathMark, auroraMark())
	publishedPolicyForTest(t, r, map[string]any{
		"version":           projectpolicy.Version,
		"verifier_accounts": []any{"person-1@example.com"},
	})
	seedCredentialsService(t, r, r.credentialsRef())
	r.appendConfig(t, "verifier_accounts: []\n")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err != nil {
		t.Fatalf("an ordinary update of an installation whose owner cleared the list failed: %v\n%s", err, s)
	}
	svc := credentialsServiceEntry(t, r)
	got, ok := svc["verifier_accounts"]
	if !ok {
		t.Fatalf("the carried block has no verifier_accounts member, want []: an absent member is the module's variable null, which is the state the module's default is for, so the service would admit the verifier account the owner cleared\n%s", s)
	}
	if arr, ok := got.([]any); !ok || len(arr) != 0 {
		t.Errorf("credentials_service.verifier_accounts = %v after one ordinary update, want [] (nobody): the carried block has to carry the owner's clear, or the service admits a caller the node does not name\n%s", got, s)
	}
	if got := nodeVerifiers(t, r); got != nil {
		t.Errorf("the published node names verifier_accounts = %v after the owner's clear, want nobody: the node is where CSR15's reader and the credentials service's own list come from, and the two have to agree\n%s", got, s)
	}
}

// localConfigOf is the project config the rig's runs wrote, parsed: the file
// these tests assert on, and the one the update's write is about.
func localConfigOf(t *testing.T, r *imagesRig) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the project config does not parse: %v", err)
	}
	return lc
}

// nodeVerifiers is the verifier list the published policy node holds, nil for
// the member that names nobody: an absent member and an empty list are one
// thing there (policyConfigDrift's jsonText folds them), and nil is how both
// are read here.
func nodeVerifiers(t *testing.T, r *imagesRig) []string {
	t.Helper()
	node, _ := r.db.Value(projectpolicy.PathShared()).(map[string]any)
	raw, _ := node["verifier_accounts"].([]any)
	var out []string
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// verifierListOf reads the verifier list out of a config the rig wrote: the
// field is a pointer so that the owner's written empty list survives the write
// (omitempty would drop it), and the deref is what tells the two apart — nil is
// a file that says nothing about the list, an empty slice is the clear.
func verifierListOf(c *localcfg.Config) []string {
	if c.VerifierAccounts == nil {
		return nil
	}
	return *c.VerifierAccounts
}

// sameList compares two verifier lists the way these tests need: nil is
// "names nobody", and it is NOT the same as a present empty list — that
// distinction is the whole of the owner's clear, because an absent member is
// the module's variable null, which is the state the module's default is for.
func sameList(got, want []string) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	return slices.Equal(got, want)
}

// TestPrintVarsKeepsTheCarriedVerifierList is --print-vars on an installation
// whose workdir carries a verifier list and whose local config says nothing
// about the key: the printed document has to carry the list. It is a
// read-only look at the installation as it STANDS, so the alternative is not
// a cloud read (this path makes none by design, which is why it cannot answer
// with the effective list) but the config's own, which is nil here — and a
// nil third argument to infra.CarriedCredentialsService DELETES the member,
// so the printed document named nobody while the same machine's `fugaro
// update` writes the owner's list into the same block.
//
// Mutation (verified, run and restored): pass lc.VerifierAccountsEffective(nil)
// to CarriedCredentialsService instead of the as-found carry
// (CarriedCredentialsServiceAsFound): this test fails, the member gone from
// the printed document.
func TestPrintVarsKeepsTheCarriedVerifierList(t *testing.T) {
	owner := "person-1@example.com"
	r := credentialsRig(t)
	seedCredentialsServiceWithVerifiers(t, r, r.credentialsRef(), []string{owner})
	if got := verifierListOf(localConfigOf(t, r)); got != nil {
		t.Fatalf("the rig's local config already names a verifier (%v): this test is about the machine whose config is silent", got)
	}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "setup", "--print-vars")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--print-vars did not print one JSON object: %v\n%s", err, out)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		t.Fatalf("--print-vars dropped the opt-in the workdir carries:\n%s", out)
	}
	raw, ok := svc["verifier_accounts"].([]any)
	if !ok {
		t.Fatalf("--print-vars printed no verifier_accounts member (credentials_service = %v), want [%s]: an absent member is the module's variable null, so the printed document admits the module's default where the installation names the owner's list\n%s", svc, owner, out)
	}
	var got []string
	for _, v := range raw {
		s, _ := v.(string)
		got = append(got, s)
	}
	if !sameList(got, []string{owner}) {
		t.Errorf("--print-vars printed verifier_accounts = %v, want [%s]: the printed variables are the installation as it stands, and the list the block carries is the one its service is admitted by\n%s", got, owner, out)
	}
}

// TestAFailedPolicyReadKeepsTheVerifierList is the read that must NOT be
// swallowed. The published node is one this Fugaro cannot read at all — a
// version a NEWER binary published, the one skew no stage of this run repairs
// — and the installation's block carries the owner's list, with the machine's
// own config silent about the key.
//
// Answering that read with "nothing anywhere names a verifier" is not the
// tolerant path this run needs; it is a WRITE. prepare deletes the block's
// member (the module's default then admits the installation's own
// account) and writeConfig writes that address into the local config, which
// the same run's policy stage publishes over the owner's list: an installation
// whose node is merely newer than this CLI ends up with its published verifier
// replaced, permanently, by the installation's own account.
//
// So the run stops, and it stops at the read: the sentence names the verifier
// list it could not read, and the config it would have written is untouched.
//
// Mutation (verified, run and restored): swallow the error the way the
// original tolerance did (`if err != nil || p == nil { return the config's own }`)
// and this test fails on both assertions — the run goes on to write the
// address into the local config, and what stops it afterwards is the
// repository stage's own refusal, which names the node and not the list it
// silently replaced.
func TestAFailedPolicyReadKeepsTheVerifierList(t *testing.T) {
	owner := "person-1@example.com"
	r := credentialsRig(t)
	r.setPlanAt(t, "installation")
	credentialsOutputs(t, r)
	// The node a NEWER Fugaro published: this binary reads the document, sees a
	// version above its own, and has no sentence for its members.
	r.db.Set(budget.PathMark, auroraMark())
	publishedPolicyForTest(t, r, map[string]any{
		"version":           projectpolicy.Version + 1,
		"verifier_accounts": []any{owner},
	})
	seedCredentialsServiceWithVerifiers(t, r, r.credentialsRef(), []string{owner})
	r.save(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	s := out + errOut
	if err == nil {
		t.Fatalf("an update whose policy node is newer than this binary succeeded, want the read refused: the run cannot tell what the node names, so it must not answer for it\n%s", s)
	}
	if !strings.Contains(s, "the verifier accounts this installation's document must carry could not be read") {
		t.Errorf("the run stopped, but not at the verifier read: want a refusal naming the verifier accounts it could not read\n%s", s)
	}
	if got := verifierListOf(localConfigOf(t, r)); got != nil {
		t.Errorf("the local config names verifier_accounts = %v after a run whose policy read failed, want the file as it was (silent): writing the installation's own address here publishes it over the owner's list\n%s", got, readFileString(t, r.cfg))
	}
}

// cloneCheckoutYAML is checkoutYAML for a repository whose one workflow runs
// the base image directly: no build, so a converge that reaches the checkout
// stage's cloud step has nothing billable to submit.
func cloneCheckoutYAML(provider, auth, project string) string {
	return "version: 1\nproject: " + project + "\ngit: { provider: " + provider + " }\nagent: { auth: " + auth + " }\nworkflows:\n  app: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"
}
