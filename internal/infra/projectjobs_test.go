package infra

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// projectProfilesLCBlock is the local config block the project-job tests and
// the installation golden are built with: two profiles, both on GitHub, one
// mounting the GitHub App key and the Anthropic key and one the App key alone,
// plus the project-wide containers they mount.
//
// Both are GitHub's because GitHub is the only provider a shared job may serve
// in phase 3: the owner ruled on 2026-10-15 that a Bitbucket credential cannot
// be scoped to one project's repositories, so Bitbucket stays per-repository
// (plan Task 3.0 step 2, D5 amended). Neither mounts a Claude OAuth token:
// those are person-owned (design D2, §7) and are refused on a shared job until
// phase 5 (TestProjectJobsRefuseAClaudeOAuthToken), so every job runs as no
// account and its name's account segment is empty.
const projectProfilesLCBlock = `project_secrets:
  anthropic-api-key: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [github-app-key, anthropic-api-key] }
  heavy: { provider: github, cpu: 8, memory: 32Gi, timeout_class: 24h, secrets: [github-app-key] }
`

const githubAppKeyLCBlock = `project_secrets:
  github-app-key: { allow_data_to: [acme/sandbox] }
`

// projectJobRepoLC is the GitHub repository every project-job config records,
// with the GitHub App ID a shared job is built from (the owner's ruling of
// 2026-10-17, plan task 3.5 step 4). It is one line appended to m4LocalConfig's
// repos block — the way spec_test.go appends its Vertex repository — because
// the block is the last key of that config. A project that declares job
// profiles and records no App ID is refused:
// TestProjectJobsNeedAGitHubAppID.
const projectJobRepoLC = "  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: \"123456789\" }\n"

// projectJobSpec is installationSpecWith for the profiles-bearing config. The
// repository line cannot travel through installationSpecWith's extraLC: it
// belongs inside m4LocalConfig's repos block, and those extras follow the M5
// top-level fields, so a repository put there is a parse error.
func projectJobSpec(t *testing.T) InstallationSpec {
	t.Helper()
	s, err := Installation(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// projectAccountJobSpec is projectJobSpec with the account axis's config
// (projectAccountAxisLC). It is the fixture of
// internal/infra/testdata/installation-accounts.tfvars.json and of the golden
// plan its script makes, the one plan that holds an account job: the token
// mount it carries and the secret grant beside it.
func projectAccountJobSpec(t *testing.T) InstallationSpec {
	t.Helper()
	s, err := Installation(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// threeReposLocalConfig is m4LocalConfig with three repositories instead of
// one, for the claim that the job set does not move with them (design §13).
var threeReposLocalConfig = func() string {
	const one = "repos:\n  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	const three = `repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
  acme/other: { provider: bitbucket, base_branch: master, workflows: [web] }
  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: "123456789" }
`
	if !strings.Contains(m4LocalConfig, one) {
		panic("m4LocalConfig's repos block moved: threeReposLocalConfig is stale")
	}
	return strings.Replace(m4LocalConfig, one, three, 1)
}()

func projectJobsFor(t *testing.T, lc string) map[string]ProjectJob {
	t.Helper()
	c := parseLC(t, lc)
	jobs, err := ProjectJobs(c, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

// prebuiltDefaultImage is the reference catalogDefaultLCBlock's entry carries:
// a prebuilt image, the shape D2305's entry has.
const prebuiltDefaultImage = "ghcr.io/dimipaun/aurora-base:1.0"

// catalogDefaultLCBlock is a catalog whose default entry is a prebuilt image:
// D2305's shape, an entry `fugaro update` writes for a project, which is a
// reference this installation builds nothing from. The entry `update` writes
// is the pinned base image itself, so this fixture's reference is deliberately
// ANOTHER prebuilt image: a job that runs the entry and a job that runs the
// base image have to be told apart by the reference the deployer wrote.
const catalogDefaultLCBlock = `image_catalog:
  entries:
    - name: base
      default: true
      source: { kind: image, image: ` + prebuiltDefaultImage + ` }
`

// TestProjectJobsDeployTheCatalogDefaultImage: a shared job runs the project's
// own image, which is the catalog's default entry (D2305, S10) — the entry the
// project states, and the installation's base image only when it states none.
//
// A profile has no image field (R1), so the catalog's default is the one place
// a project says which image its shared jobs run, and the deployer asks the
// one resolver for it (catalog.JobImage, F21) rather than spelling the base
// image: a shared job that kept the bare base image would run an image the
// project's catalog does not name, and the baked image the spec describes as a
// cache would be one no job of this project ever ran.
func TestProjectJobsDeployTheCatalogDefaultImage(t *testing.T) {
	// m5Additions' two image facts, spelled once here: the base image every
	// repository builds from and the registry host the installation's images
	// live under.
	const baseImage = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc"
	const registryHost = "us-east5-docker.pkg.dev/proj-1234"
	const built = `image_catalog:
  entries:
    - name: team
      default: true
      source: { kind: dockerfile, repo: acme/webapp, path: .fugaro/team.Dockerfile }
`
	for _, tt := range []struct{ name, block, want string }{
		// A prebuilt entry's image is its own reference, exactly as the
		// per-repository path runs it (repoCtx.catalogImage).
		{"a prebuilt default", catalogDefaultLCBlock, prebuiltDefaultImage},
		// An entry this installation builds is the image the catalog build
		// pushes it to: the one shared registry, named catalog-<entry> with the
		// moving :latest tag the job spec names (D1152).
		{"a default this installation builds", built,
			registryHost + "/" + gcp.SharedRegistryRepoID() + "/catalog-team:latest"},
		// A project that marks no default runs the installation's base image,
		// which is what every shared job ran before the catalog decided
		// anything (O4 b).
		{"no default at all", "", baseImage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock+tt.block)
			if len(jobs) != 2 {
				t.Fatalf("%d jobs, want the two the fixture's profiles deploy: %v", len(jobs), slices.Sorted(maps.Keys(jobs)))
			}
			for name, j := range jobs {
				if got := j.Image; got != tt.want {
					t.Errorf("the shared job %s runs %s, want %s: a shared job runs the project's own image, which the catalog's default entry is (D2305)", name, got, tt.want)
				}
			}
		})
	}
}

// TestImplicitReviewJobRunsTheCatalogDefault: the implicit review-only job is
// a shared job of the project like any other, and a project that declares no
// profile has no coding job for it to inherit an image from (S10, D2305). Such
// a project's implicit jobs are the only jobs it has, so the deployer asks the
// resolver for the image itself.
func TestImplicitReviewJobRunsTheCatalogDefault(t *testing.T) {
	jobs := projectReviewJobsFor(t, implicitReviewLC()+catalogDefaultLCBlock)
	if len(jobs) != len(ImplicitReviewProviders) {
		t.Fatalf("review jobs = %v, want one implicit default job per provider %v", sortedJobNames(jobs), ImplicitReviewProviders)
	}
	for name, j := range jobs {
		want := prebuiltDefaultImage
		if j.Image != want {
			t.Errorf("the implicit review job %s runs %s, want the project's default entry %s: it is a shared job of this project, and a project with no profile has no coding job to inherit an image from", name, j.Image, want)
		}
	}
}

// One job and one job service account per (profile, provider, account) — the
// resource set the whole phase rests on (plan task 3.2 step 1).
func TestProjectJobsAreOnePerProfileProviderAccount(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	if len(jobs) != 2 {
		t.Fatalf("%d jobs, want 2: %v", len(jobs), slices.Sorted(maps.Keys(jobs)))
	}
	// No profile may mount a Claude OAuth token yet (phase 5), so every job
	// runs as no account.
	want := map[string]struct{ profile, provider, account string }{
		gcp.ProjectJobName("standard", "github", ""): {"standard", "github", ""},
		gcp.ProjectJobName("heavy", "github", ""):    {"heavy", "github", ""},
	}
	if !slices.Equal(slices.Sorted(maps.Keys(jobs)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("jobs = %v, want %v", slices.Sorted(maps.Keys(jobs)), slices.Sorted(maps.Keys(want)))
	}
	seenSA := map[string]string{}
	for name, j := range jobs {
		w := want[name]
		if j.Profile != w.profile || j.Provider != w.provider || j.Account != w.account || j.Job != name {
			t.Errorf("job %s = %+v, want profile %s, provider %s, account %q", name, j, w.profile, w.provider, w.account)
		}
		// One account per job, and its name is the job's own derived ID: a
		// service account is never shared between jobs (design §4.1).
		if j.ServiceAccount.AccountID != gcp.ProjectServiceAccountID(w.profile, w.provider, w.account) {
			t.Errorf("job %s runs as %s, want %s", name, j.ServiceAccount.AccountID, gcp.ProjectServiceAccountID(w.profile, w.provider, w.account))
		}
		if prev, dup := seenSA[j.ServiceAccount.AccountID]; dup {
			t.Errorf("jobs %s and %s share the service account %s", prev, name, j.ServiceAccount.AccountID)
		}
		seenSA[j.ServiceAccount.AccountID] = name
		if j.ServiceAccount.DisplayName == "" {
			t.Errorf("job %s has no display name", name)
		}
		// The profile's shape, on the one base image clone workflows run
		// (design generic-tool.md §2 G3): nothing here is per-repository.
		if j.Image == "" || strings.Contains(j.Image, "acme") {
			t.Errorf("job %s runs the image %q", name, j.Image)
		}
		if j.BucketCondition.Title != gcp.BucketConditionTitle(j.ServiceAccount.AccountID) {
			t.Errorf("job %s titles its bucket condition %q, want %q", name, j.BucketCondition.Title, gcp.BucketConditionTitle(j.ServiceAccount.AccountID))
		}
		if j.BucketCondition.Expression != gcp.ProjectBucketCondition("fugaro-runs-proj-1234", gcp.JobBucketPrefixes) {
			t.Errorf("job %s grants %q, want the widened condition", name, j.BucketCondition.Expression)
		}
		if j.CPU == "" || j.Memory == "" || j.TaskTimeoutS <= 0 {
			t.Errorf("job %s has no resources: %+v", name, j)
		}
	}
	// The profile's own resources and timeout, not the fixture's defaults.
	standard, heavy := jobs[gcp.ProjectJobName("standard", "github", "")], jobs[gcp.ProjectJobName("heavy", "github", "")]
	if standard.CPU != "2" || standard.Memory != "8Gi" || heavy.CPU != "8" || heavy.Memory != "32Gi" {
		t.Errorf("resources = standard %s/%s, heavy %s/%s", standard.CPU, standard.Memory, heavy.CPU, heavy.Memory)
	}
	if standard.TaskTimeoutS >= heavy.TaskTimeoutS {
		t.Errorf("timeouts = standard %d, heavy %d: 6h must be shorter than 24h", standard.TaskTimeoutS, heavy.TaskTimeoutS)
	}
}

// The job set is a function of the profiles and the accounts, never of the
// repositories (design §13, plan step 6): the repository count moves, the
// jobs and their service accounts do not.
func TestProjectJobsDoNotFollowTheRepositories(t *testing.T) {
	one := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	three := projectJobsFor(t, threeReposLocalConfig+m5Additions+projectProfilesLCBlock)
	if !maps.EqualFunc(one, three, func(a, b ProjectJob) bool { return a.Job == b.Job && a.ServiceAccount == b.ServiceAccount }) {
		t.Errorf("jobs = %v with one repository and %v with three", sortedJobNames(one), sortedJobNames(three))
	}
	// And no repository's name reaches a job at all.
	for name, j := range three {
		if strings.Contains(name, "acme") || strings.Contains(j.ServiceAccount.AccountID, "acme") {
			t.Errorf("job %s is named after a repository", name)
		}
		for k, v := range j.Env {
			if strings.Contains(v, "acme") {
				t.Errorf("job %s sets %s=%s, which is per-repository", name, k, v)
			}
		}
	}
}

func sortedJobNames(jobs map[string]ProjectJob) []string { return slices.Sorted(maps.Keys(jobs)) }

// projectJobRepoReadyLC is projectJobRepoLC with the switch's own record on the
// repository's entry (localcfg.Repo.CredentialsServiceReady, which `fugaro
// update` writes when it applies that repository's root with the service on):
// the deployer's answer to "is this profile's repositories' auth one the
// service serves".
const projectJobRepoReadyLC = "  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: \"123456789\", credentials_service_ready: true }\n"

// TestEmptySharedJobListChangesNothing is the S5 flip's inert state, and G17
// for S5 read on the deployer's side (B5.7, D505, D2078): with no class served
// — the list empty, which is what every release before the flip ships — what
// the switch adds to a shared job is the credentials service and nothing else:
// the job's account gets roles/run.invoker on it
// (deploy/terraform/gcp/modules/project/sa.tf) and the launcher hands its
// executions the address (internal/cli's credentialsServiceLaunchEnv), while
// every mount the job has stays exactly where it was — a job that lost a mount
// here would run with no model credential and no address to fetch one from
// (G17, D143).
//
// The equality is the whole of it: the switched job, with its one new field
// cleared, must be the job the same config builds with the service off. That
// covers the mounts (SecretEnv), the redaction list, the environment, the
// service account, the conditions and the two booleans in one assertion, so a
// later change that quietly drops something while adding the field fails here.
//
// The class list is cleared here and restored afterwards rather than assumed
// empty, so this is the same test in the flip's first commit and its second:
// it is the counterfactual TestFilledSharedJobListDropsTheModelMounts is read
// against (the shape TestEmptyBuildListChangesNothing has for S4).
//
// Mutation (run, restore): have the switch clear the mounts (or set
// SecretEnv = nil) for a switched profile — the equality fails on the mount,
// and the URL-less block stays the same job as before, which is the state that
// must not move either.
func TestEmptySharedJobListChangesNothing(t *testing.T) {
	saved := config.SharedJobServedClasses
	config.SharedJobServedClasses = nil
	t.Cleanup(func() { config.SharedJobServedClasses = saved })

	// The repository line is the first key of the config's repos block, so it
	// goes between m4LocalConfig and the additions that follow it (the same
	// shape projectJobSpec builds).
	tail := m5Additions + projectProfilesLCBlock
	off := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+tail)
	on := projectJobsFor(t, m4LocalConfig+projectJobRepoReadyLC+tail+credentialsServiceLCBlock)
	if got, want := sortedJobNames(on), sortedJobNames(off); !slices.Equal(got, want) {
		t.Fatalf("the switch moved the job set to %v, want %v: one job per (profile, provider, account), whatever its repositories' records say", got, want)
	}
	lc := parseLC(t, m4LocalConfig+projectJobRepoReadyLC+tail+credentialsServiceLCBlock)
	want := &CredentialsService{Name: CredentialsServiceName, Region: lc.Region}
	for name, job := range on {
		before := off[name]
		if job.CredentialsService == nil {
			t.Errorf("the job %s carries no credentials service, want %+v: its profile's repository is switched on, so its account is granted roles/run.invoker on the installation's service and its runs are told where it is", name, *want)
			continue
		}
		if *job.CredentialsService != *want {
			t.Errorf("the job %s carries the credentials service %+v, want %+v: the name is the one the installation root creates it under and the region is the installation's, and the URL is Cloud Run's to assign — a job is told it at launch (internal/cli's credentialsServiceLaunchEnv), never from the tfvars", name, *job.CredentialsService, *want)
		}
		job.CredentialsService, before.CredentialsService = nil, nil
		if !reflect.DeepEqual(job, before) {
			t.Errorf("the job %s = %+v with the service switched on, want the job it is with the service off, %+v: the grant and the address are all this task adds (G17 — the mounts are dropped by B5.7's flip alone)", name, job, before)
		}
	}
	// The same profile with no record, and with no service at all: the second
	// is the installation that never opted in, and the first is the repository
	// whose own root has not been applied with the service on — neither
	// repository's auth is one the service serves, and the job is left exactly
	// as it is.
	for _, tc := range []struct {
		name string
		jobs map[string]ProjectJob
	}{
		{"no record on the repository", projectJobsFor(t, m4LocalConfig+projectJobRepoLC+tail+credentialsServiceLCBlock)},
		{"no service on the installation", off},
	} {
		for name, job := range tc.jobs {
			if job.CredentialsService != nil {
				t.Errorf("%s: the job %s carries the credentials service %+v, want none: the module would grant its account roles/run.invoker on a service that serves none of its runs", tc.name, name, *job.CredentialsService)
			}
		}
	}
}

// TestFilledSharedJobListDropsTheModelMounts is B5.7's flip pinned where it
// holds: the class list is filled, so a profile the credentials service serves
// plans every job of it with no Claude credential at all — the profile's own
// Anthropic key, and, on an account job, the account's own Claude OAuth token —
// while a profile the switch holds off keeps every one of them, before and
// after (G9, D147).
//
// A provider's key is deliberately NOT part of this, and the last half of the
// test is that: nothing serves one yet — the runner reads it from its own
// environment and the gateway routes with the job's own key — so the drop of a
// shared job's provider key is the keys-through-service plan's KS16, whose red
// step is exactly this state (R6, D1634).
//
// The mounts and the redaction list are the whole of the deployer's half: the
// module's accessor list is the same set read through one local
// (deploy/terraform's TestSharedJobServedClassesMatchTheModule), and a variable
// that left the container leaves the redaction list with it, or the run
// publishes the value of a variable it no longer carries.
func TestFilledSharedJobListDropsTheModelMounts(t *testing.T) {
	if !config.SharedJobServesClass(config.SharedJobClassModel) {
		t.Fatalf("config.SharedJobServedClasses = %v and does not carry %q: B5.7's flip is what takes a switched profile's shared job off the model credentials its runs no longer read (D505, D2078)", config.SharedJobServedClasses, config.SharedJobClassModel)
	}
	// The profile's own mounts: the standard profile mounts the App key and the
	// Anthropic key, and only the first of the two survives the flip.
	tail := m5Additions + projectProfilesLCBlock
	off := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+tail+credentialsServiceLCBlock)
	on := projectJobsFor(t, m4LocalConfig+projectJobRepoReadyLC+tail+credentialsServiceLCBlock)
	name := gcp.ProjectJobName("standard", "github", "")
	before, after := off[name], on[name]
	if before.CredentialsService != nil || after.CredentialsService == nil {
		t.Fatalf("the fixtures are not the pair this test is about: the unswitched job carries %v and the switched one %v", before.CredentialsService, after.CredentialsService)
	}
	if _, mounted := before.SecretEnv[config.ReservedSecrets[config.AnthropicSecret]]; !mounted {
		t.Fatalf("the unswitched job mounts %v and not the Anthropic key: the test would be vacuous", before.SecretEnv)
	}
	for env := range after.SecretEnv {
		if config.IsSharedJobServedModelEnv(env) {
			t.Errorf("the switched job mounts %s from %s: the service serves this profile's repositories, so no Claude credential belongs in the container (B5.7, D2078)", env, after.SecretEnv[env])
		}
	}
	if got := after.Env[runner.SecretEnvsVar]; strings.Contains(got, config.ReservedSecrets[config.AnthropicSecret]) {
		t.Errorf("the switched job's %s = %q names a variable it no longer mounts: the redaction list is the mounts, and a stale entry hides a value the run does not carry", runner.SecretEnvsVar, got)
	}
	if got, want := after.Env[runner.SecretEnvsVar], redactionEnvs(after.SecretEnv); got != want {
		t.Errorf("the switched job's %s = %q, want %q: the list is re-derived from the mounts the flip left", runner.SecretEnvsVar, got, want)
	}
	if !maps.Equal(before.SecretEnv, off[name].SecretEnv) {
		t.Errorf("the unswitched job mounts %v and then %v: the flip moves a served profile's job alone (G9)", before.SecretEnv, off[name].SecretEnv)
	}

	// The fact the run's own door is keyed on (C1 (h), CS13): a served job's
	// environment says its credentials are served, which is what stops a run of
	// it reading its own auth or the account its name carries to choose a model
	// credential the container does not hold — and the unswitched job carries
	// no such entry, because it mounts them all.
	if got := before.Env[config.CredentialsServedEnv]; got != "" {
		t.Errorf("the unswitched job's environment carries %s = %q: the fact says the class's mounts are served, and this job keeps every one of them", config.CredentialsServedEnv, got)
	}
	if got := after.Env[config.CredentialsServedEnv]; got != config.ServedValue {
		t.Errorf("the switched job's environment carries %s = %q, want %q: a run of it mounts no model credential, so the credential that serves its calls is the credentials service's answer and never the run's own selection (internal/runner's projectJobServed)", config.CredentialsServedEnv, got, config.ServedValue)
	}

	// The account's own token, which is the mount this class is mostly made of
	// on a shared job: an account job of a served profile carries no
	// CLAUDE_CODE_OAUTH_TOKEN at all, while the same profile's jobs with the
	// switch off carry one each.
	accounts := m5Additions + projectAccountAxisLC
	held := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+accounts+credentialsServiceLCBlock)
	served := projectJobsFor(t, m4LocalConfig+projectJobRepoReadyLC+accounts+credentialsServiceLCBlock)
	token := config.ReservedSecrets[config.ClaudeOAuthToken]
	mounted := 0
	for _, job := range held {
		if _, ok := job.SecretEnv[token]; ok {
			mounted++
		}
	}
	if mounted == 0 {
		t.Fatalf("no job of the unswitched account axis mounts %s: the test would be vacuous", token)
	}
	for jobName, job := range served {
		if logical, ok := job.SecretEnv[token]; ok {
			t.Errorf("the served job %s still mounts %s from %s: the account's token is the credential the service serves its runs' model calls with (B5.1, B5.7)", jobName, token, logical)
		}
	}
	if got, want := sortedJobNames(served), sortedJobNames(held); !slices.Equal(got, want) {
		t.Fatalf("the flip moved the job set to %v, want %v: the class takes mounts away and never a job", got, want)
	}

	// And the one model credential the filled class does NOT take: a provider's
	// key. It is a model credential in Go (config.IsModelSecretEnv) and it is
	// not one here, because nothing serves it yet — the runner reads it from
	// its own environment and the gateway routes the call with the job's own
	// key — so a shared job that gave it up would fail every provider-model run
	// with nothing to fall back on. Its drop is the keys-through-service plan's
	// KS16, whose red step is this state, and this half is what would go red
	// were the provider-key clause reintroduced here (R6, D1634).
	routed := m5Additions + providerProfileLCBlock
	keys := projectJobsFor(t, m4LocalConfig+projectJobRepoReadyLC+routed+credentialsServiceLCBlock)
	job, ok := keys[gcp.ProjectJobName("standard", "github", "")]
	if !ok {
		t.Fatalf("the profile that mounts a provider key deployed no job: jobs = %v", sortedJobNames(keys))
	}
	// The same profile with the switch off, so the two halves of the contrast
	// are one job's set before and after rather than an assertion that could
	// hold of a profile which mounted nothing.
	heldKeys := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+routed+credentialsServiceLCBlock)
	heldJob := heldKeys[gcp.ProjectJobName("standard", "github", "")]
	anthropic := config.ReservedSecrets[config.AnthropicSecret]
	if _, mounted := heldJob.SecretEnv[anthropic]; !mounted {
		t.Fatalf("the unswitched job of the same profile mounts %v and not the Anthropic key: the contrast this half draws would be vacuous", heldJob.SecretEnv)
	}
	keyEnv := config.ProviderKeyEnv("openrouter-api-key")
	keyLogical, keyMounted := job.SecretEnv[keyEnv]
	if !keyMounted {
		t.Errorf("the served job mounts %v and not %s: a provider's key is not served to a shared job yet, so the flip takes every other model credential and leaves this one (B5.7, R6, D1634)", job.SecretEnv, keyEnv)
	} else if keyLogical != "openrouter-api-key" {
		t.Errorf("the served job mounts %s from %s, want the profile's own entry", keyEnv, keyLogical)
	}
	if !strings.Contains(job.Env[runner.SecretEnvsVar], keyEnv) {
		t.Errorf("the served job's %s = %q does not name %s: the redaction list is the mounts, and a mount left in the container is one the run must not publish", runner.SecretEnvsVar, job.Env[runner.SecretEnvsVar], keyEnv)
	}
	if _, dropped := job.SecretEnv[anthropic]; dropped {
		t.Errorf("the served job mounts %s from %s: the named model credentials are the ones the flip takes, and this profile is served (B5.7, D2078)", anthropic, job.SecretEnv[anthropic])
	}
}

// TestFilledSharedJobListKeepsTheAppKey is the other half of B5.7's flip, and
// it is G22 read as a test: the GitHub App's private key is
// config.ServiceHeldSecretNames' name and not this list's, so filling the
// shared-job class list leaves the App key's mount and the accessor that goes
// with it exactly where they were. A shared job that gave the key up before the
// runner stopped reading FUGARO_GITHUB_APP_PRIVATE_KEY is a job whose every
// GitHub run dies in the credential — the reason that list is still empty
// (#907, C2.3).
func TestFilledSharedJobListKeepsTheAppKey(t *testing.T) {
	if !config.SharedJobServesClass(config.SharedJobClassModel) {
		t.Fatalf("config.SharedJobServedClasses = %v and does not carry %q: this is B5.7's filled state, and the empty list keeps everything by definition", config.SharedJobServedClasses, config.SharedJobClassModel)
	}
	if config.IsServiceHeldSecretName(config.GitHubAppKeySecret) {
		t.Fatalf("config.ServiceHeldSecretNames = %v names the App key: its drop is that list's own flip (#907), and this test is about the App key staying while only the model class leaves (G22)", config.ServiceHeldSecretNames)
	}
	tail := m5Additions + projectProfilesLCBlock
	on := projectJobsFor(t, m4LocalConfig+projectJobRepoReadyLC+tail+credentialsServiceLCBlock)
	key := config.ReservedSecrets[config.GitHubAppKeySecret]
	for jobName, job := range on {
		logical, mounted := job.SecretEnv[key]
		if !mounted {
			t.Errorf("the served job %s mounts no %s (%v): the App key is served by no list this release fills, so its accessor and its mount stay together (G22, #907)", jobName, key, job.SecretEnv)
			continue
		}
		if logical != config.GitHubAppKeySecret {
			t.Errorf("the served job %s mounts %s from %s, want %s", jobName, key, logical, config.GitHubAppKeySecret)
		}
		if got := job.Env[runner.SecretEnvsVar]; !strings.Contains(got, key) {
			t.Errorf("the served job %s = %q does not name %s: the mount is still there, and a mounted secret left off the redaction list is one the run would print", jobName, runner.SecretEnvsVar, key)
		}
	}
}

// TestTheProjectJobsSwitchFollowsTheRecordsOfTheRun pins the re-derivation a
// run owes the records it writes itself: the switch is read from the local
// config's per-repository records, and two writers move them INSIDE a run that
// applies the installation root — the upgrade backfill at the end of the
// installation stage and the repository stage's own record write. A set derived
// once, when the run's options were read, is behind those records from the
// moment either has written, and the apply that follows would deploy a shared
// job whose account the launcher has already been told to hand the service's
// address — refused at the front door, with no fallback (D143).
//
// The record that has to move the answer is the repository's own kind: the
// second config below is the first with ONE field added to the repository line
// (projectJobRepoReadyLC), which is exactly what `recordCredentialsServiceReady`
// writes, and every job of the profile that admits that repository has to gain
// the service — and nothing else about it (the equality is
// TestEmptySharedJobListChangesNothing's own).
//
// Mutations (run, restore): have RefreshProjectJobCredentialsService return its
// input and the staleness answer is false and the jobs stay off; have it clear
// the field instead of setting it and the equality fails.
func TestTheProjectJobsSwitchFollowsTheRecordsOfTheRun(t *testing.T) {
	tail := m5Additions + projectProfilesLCBlock
	off := m4LocalConfig + projectJobRepoLC + tail + credentialsServiceLCBlock
	on := m4LocalConfig + projectJobRepoReadyLC + tail + credentialsServiceLCBlock
	before, after := parseLC(t, off), parseLC(t, on)
	if off == on {
		t.Fatal("the two fixtures are the same document: this test is about the one record that differs between them")
	}
	jobs := projectJobsFor(t, off)
	if !ProjectJobsCredentialsServiceStale(jobs, after) {
		t.Fatalf("the set derived before the record moved is not reported stale, though the config now serves the profile's repository: the run's last apply of the installation root is then skipped, and the grant lands only on a second fugaro update")
	}
	refreshed := RefreshProjectJobCredentialsService(jobs, after)
	if ProjectJobsCredentialsServiceStale(refreshed, after) {
		t.Errorf("the re-derived set is still stale: the answer has to be a fixed point of the config it was derived from, or every later stage plans the root again")
	}
	if ProjectJobsCredentialsServiceStale(jobs, before) {
		t.Errorf("a set derived from the records as they stand is reported stale: it is, on a run where nothing moved, exactly what the config derives")
	}
	want := &CredentialsService{Name: CredentialsServiceName, Region: after.Region}
	if got, wantNames := sortedJobNames(refreshed), sortedJobNames(jobs); !slices.Equal(got, wantNames) {
		t.Fatalf("the re-derivation moved the job set to %v, want %v: one job per (profile, provider, account), whatever its repositories' records say", got, wantNames)
	}
	for name, job := range refreshed {
		if job.CredentialsService == nil || *job.CredentialsService != *want {
			t.Errorf("after the record moved, the job %s carries %v, want %+v: its profile admits the repository whose record says the service is ready", name, job.CredentialsService, *want)
			continue
		}
	}
	// And the re-derivation is the derivation, whole: the refreshed set is what
	// the config as it stands composes from scratch, mounts and redaction list
	// included. The switch is the condition the class list's drop is read behind
	// (projectJobMounts), so a set where the switch moved and the mounts did not
	// is a job carrying neither the model credential an On record had taken away
	// nor an address to fetch one from — every run of it would die at its first
	// model call (G17, D143).
	if fresh := projectJobsFor(t, on); !reflect.DeepEqual(refreshed, fresh) {
		for name, job := range refreshed {
			if !reflect.DeepEqual(job, fresh[name]) {
				t.Errorf("the re-derived job %s = %+v, want the job the same config composes from scratch, %+v: the switch and the mounts it decides move together (G17)", name, job, fresh[name])
			}
		}
	}
	// A job whose profile the config no longer declares is answered off: there
	// is no profile left for a repository to be served through. The config
	// records the repository whose record is on, so only the missing profile
	// can be what moves the answer.
	undeclared := parseLC(t, m4LocalConfig+projectJobRepoReadyLC+m5Additions+credentialsServiceLCBlock)
	for name, job := range RefreshProjectJobCredentialsService(refreshed, undeclared) {
		if job.CredentialsService != nil {
			t.Errorf("the job %s carries the credentials service %+v, though the config declares no profile %q at all: the set is the door's, and a profile a person has removed serves nothing", name, *job.CredentialsService, job.Profile)
		}
	}
}

// The job's env is what the installation knows and nothing per-repository
// (plan task 3.2 step 1): the four identity variables, the mode the
// installation's own config names (AB5, F20), plus the budget backend's
// address and web key when the installation has them. The provider
// is never one of them — a launcher overrides any variable — and neither is
// anything a repository's own config decides.
func TestProjectJobEnvIsTheInstallationsAlone(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	// The App's ID is the one non-installation variable a job carries, and it
	// is not per-repository: one App serves the project (owner ruling of
	// 2026-10-17, plan task 3.5 step 4), so it is resolved once and is the
	// same in every job. runner.SecretEnvsVar is the second, and it is the
	// list of the variables the job mounts secrets as — the profile's mounts,
	// which a repository's own config does not decide (a profile is what a
	// repository selects), computed per job from that job's own SecretEnv and
	// pinned by TestProjectJobRegistersItsMountedSecretsForRedaction.
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, RTDBURLEnv, FirebaseAPIKeyEnv, BudgetModeEnv, providers.EnvGitHubAppID, runner.SecretEnvsVar}
	for name, j := range jobs {
		keys := slices.Sorted(maps.Keys(j.Env))
		for _, k := range keys {
			if !slices.Contains(allowed, k) {
				t.Errorf("job %s sets %s, which is not one of the installation's variables", name, k)
			}
		}
		if j.Env[ProjectEnv] != "aurora" || j.Env[GCPProjectEnv] != "proj-1234" || j.Env[RegionEnv] != "us-east5" || j.Env[BucketEnv] != "gs://fugaro-runs-proj-1234" {
			t.Errorf("job %s identity = %v", name, j.Env)
		}
		// The provider is not an env var: run.jobs.runWithOverrides lets a
		// launcher write any variable, and the provider decides which
		// repository the runner may clone and which credential it holds.
		for k, v := range j.Env {
			if strings.Contains(strings.ToUpper(k), "PROVIDER") || v == j.Provider {
				t.Errorf("job %s carries the provider in %s=%s", name, k, v)
			}
		}
		// The secret mounts are the profile's, by variable name.
		wantSecrets := map[string]string{}
		switch j.Profile {
		case "standard":
			wantSecrets = map[string]string{"FUGARO_GITHUB_APP_PRIVATE_KEY": "github-app-key", "ANTHROPIC_API_KEY": "anthropic-api-key"}
		case "heavy":
			wantSecrets = map[string]string{"FUGARO_GITHUB_APP_PRIVATE_KEY": "github-app-key"}
		}
		if !maps.Equal(j.SecretEnv, wantSecrets) {
			t.Errorf("job %s mounts %v, want %v", name, j.SecretEnv, wantSecrets)
		}
		for k := range j.SecretEnv {
			if _, dup := j.Env[k]; dup {
				t.Errorf("job %s sets %s plainly and as a secret", name, k)
			}
		}
	}

	// With a budget backend the two backend variables are there, and they
	// are the only addition: a job that cannot read the project's policy
	// would run on the environment ceiling with the policy unenforced (D11).
	withBudget := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock+`
budget: { rtdb_url: https://aurora-default-rtdb.firebaseio.com, firebase_api_key: AIzaSyA1234567890abcdefghijklmnopqrstu }
`)
	for name, j := range withBudget {
		if j.Env[RTDBURLEnv] != "https://aurora-default-rtdb.firebaseio.com" || j.Env[FirebaseAPIKeyEnv] != "AIzaSyA1234567890abcdefghijklmnopqrstu" {
			t.Errorf("job %s backend env = %v", name, j.Env)
		}
		if len(j.Env) != 9 {
			t.Errorf("job %s sets %d variables, want the four identity ones, the mode, the two backend ones, the App's ID and the redaction list: %v", name, len(j.Env), j.Env)
		}
		if j.Env[providers.EnvGitHubAppID] != "123456789" {
			t.Errorf("job %s carries %s=%q, want the App ID the local config records", name, providers.EnvGitHubAppID, j.Env[providers.EnvGitHubAppID])
		}
	}
}

// A project that declares profiles and records no GitHub App ID is refused,
// rather than deployed without one (owner ruling of 2026-10-17, plan task 3.5
// step 4). The refusal is not pedantry: a shared job serves the github
// provider, and internal/gitprov/providers builds that provider from
// FUGARO_GITHUB_APP_ID, refusing to start without it — so the job would deploy
// cleanly and then halt every run it started, at run time, in the credential,
// where nothing points back at the local config.
//
// The state is reachable: a project that writes its profiles before it has
// onboarded a GitHub repository has exactly this local config, and that is why
// the message names the command that fixes it.
func TestProjectJobsNeedAGitHubAppID(t *testing.T) {
	// m4LocalConfig's own repository is a Bitbucket one, so nothing in this
	// config records an App ID.
	lc := parseLC(t, m4LocalConfig+m5Additions+projectProfilesLCBlock)
	if _, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false); err == nil {
		t.Fatal("ProjectJobs deployed shared jobs for a config that records no GitHub App ID")
	} else {
		for _, want := range []string{providers.EnvGitHubAppID, "--github-app-id"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one naming %q", err, want)
			}
		}
	}

	// The same profiles with one GitHub repository that records the ID: the
	// jobs are built, and every one of them carries it.
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	if len(jobs) == 0 {
		t.Fatal("no jobs for a config that records an App ID")
	}
	for name, j := range jobs {
		if j.Env[providers.EnvGitHubAppID] != "123456789" {
			t.Errorf("job %s carries %s=%q, want 123456789", name, providers.EnvGitHubAppID, j.Env[providers.EnvGitHubAppID])
		}
	}

	// A repository that records the ID but is not GitHub's does not count:
	// the field is the App's, and only a github repository has an App.
	bitbucket := strings.Replace(projectJobRepoLC, "provider: github", "provider: bitbucket", 1)
	if _, err := ProjectJobs(parseLC(t, m4LocalConfig+bitbucket+m5Additions+projectProfilesLCBlock), "fugaro-runs-proj-1234", false); err == nil {
		t.Error("ProjectJobs took the App ID from a repository that is not on GitHub")
	}
}

// Two GitHub Apps in one local config are refused rather than one of them
// chosen (design §4.5: the runner mints a token per repository from the
// project's App, and a profile's repositories all run on one shared job). A
// job built from either ID is wrong for the other's repositories, and the
// failure would be at run time, in the credential — so the deploy stops and
// says which two repositories disagree.
//
// Two repositories on the SAME App are the normal shape, and they are the
// control: the refusal is about disagreement, not about the number of
// repositories.
func TestProjectJobsRefuseTwoGitHubApps(t *testing.T) {
	second := "  acme/other: { provider: github, base_branch: main, workflows: [web], github_app_id: \"987654321\" }\n"
	_, err := ProjectJobs(parseLC(t, m4LocalConfig+projectJobRepoLC+second+m5Additions+projectProfilesLCBlock), "fugaro-runs-proj-1234", false)
	if err == nil {
		t.Fatal("ProjectJobs deployed shared jobs for a config that records two GitHub Apps")
	}
	for _, want := range []string{"acme/webapp", "123456789", "acme/other", "987654321"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one naming %q", err, want)
		}
	}

	agreed := "  acme/other: { provider: github, base_branch: main, workflows: [web], github_app_id: \"123456789\" }\n"
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+agreed+m5Additions+projectProfilesLCBlock)
	for name, j := range jobs {
		if j.Env[providers.EnvGitHubAppID] != "123456789" {
			t.Errorf("job %s carries %s=%q, want the one App both repositories record", name, providers.EnvGitHubAppID, j.Env[providers.EnvGitHubAppID])
		}
	}
}

// A project job on Vertex carries the two Vertex names, not only the grant (a
// review finding): the job's Vertex flag becomes roles/aiplatform.user on its
// service account, and the Google auth libraries read the project and region
// under Vertex's own names — ANTHROPIC_VERTEX_PROJECT_ID and CLOUD_ML_REGION —
// rather than GOOGLE_CLOUD_PROJECT, so a Vertex-auth run in a job without them
// is refused for want of a project while holding a grant it cannot use. The
// per-repository path sets exactly these two from exactly these values
// (spec.go's `if ws.Vertex`), and platformEnv's own comment already counts
// them as installation identity: a project job on Vertex is the same
// installation, so it names them the same way.
//
// Mutation (run, restored): drop the `if vertex` block from projectJobEnv. The
// Vertex branches below fail; the rest of the file is unaffected, because the
// non-Vertex branches assert the names are *absent*.
func TestProjectJobsOnVertexCarryTheVertexEnvironment(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	if !slices.Contains(PlatformEnv, VertexRegionEnv) || !slices.Contains(PlatformEnv, VertexProjectEnv) {
		t.Fatalf("PlatformEnv = %v, want it to name Vertex's two variables: it is the list upgrade copies over, and the list these jobs are held to", PlatformEnv)
	}
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("%d jobs, want 2", len(jobs))
	}
	// The four identity variables, the mode the installation's config names,
	// Vertex's two and the redaction list — no budget backend in this fixture,
	// so nothing else may appear.
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, BudgetModeEnv, VertexRegionEnv, VertexProjectEnv, providers.EnvGitHubAppID, runner.SecretEnvsVar}
	for name, j := range jobs {
		if !j.Vertex {
			t.Errorf("job %s is on Vertex: grant and environment disagree", name)
		}
		if j.Env[VertexRegionEnv] != "us-east5" || j.Env[VertexProjectEnv] != "proj-1234" {
			t.Errorf("job %s env = %v, want %s=us-east5 and %s=proj-1234 (the local config's region and GCP project)", name, j.Env, VertexRegionEnv, VertexProjectEnv)
		}
		if j.Env[VertexRegionEnv] != lc.Region || j.Env[VertexProjectEnv] != lc.GCPProject {
			t.Errorf("job %s vertex env is not the installation's own region and project: %v", name, j.Env)
		}
		for _, k := range slices.Sorted(maps.Keys(j.Env)) {
			if !slices.Contains(allowed, k) {
				t.Errorf("job %s sets %s, which is not one of the installation's variables", name, k)
			}
		}
	}

	// The other way round: an installation that does not use Vertex does not
	// advertise it. A job carrying ANTHROPIC_VERTEX_PROJECT_ID without the
	// grant would send runs at Vertex with a token that cannot reach it.
	off, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	for name, j := range off {
		if j.Vertex {
			t.Errorf("job %s is not on Vertex: grant and environment disagree", name)
		}
		if _, dup := j.Env[VertexRegionEnv]; dup {
			t.Errorf("job %s sets %s off Vertex: %v", name, VertexRegionEnv, j.Env)
		}
		if _, dup := j.Env[VertexProjectEnv]; dup {
			t.Errorf("job %s sets %s off Vertex: %v", name, VertexProjectEnv, j.Env)
		}
	}
}

// A profile may mount only project-wide containers: a name the installation
// does not store project-wide, or one nothing configures at all, is refused
// rather than granted (a grant on a secret that is not created is a plan that
// names nothing, and a repository's own secret cannot be mounted on a job
// every repository it admits runs).
func TestProjectJobSecretsMustBeProjectWide(t *testing.T) {
	// The same profiles without the containers the installation creates.
	bare := strings.Replace(projectProfilesLCBlock, `project_secrets:
  anthropic-api-key: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
`, "", 1)
	for _, c := range []struct{ name, lc, want string }{
		{"no container at all", m4LocalConfig + projectJobRepoLC + m5Additions + bare, "not stored project-wide"},
	} {
		_, err := ProjectJobs(parseLC(t, c.lc), "fugaro-runs-proj-1234", false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %q", c.name, err, c.want)
		}
	}
	// A name that is neither reserved nor a key is refused by the mount
	// resolver itself, and a local config now DOES reach it with a workflow
	// secret's name: projectSecretContainers derives a container for the
	// entry (W2), so the check above passes and this one is what refuses the
	// name. It still refuses — the resolver maps a reserved name and a
	// configured provider's key and nothing else — which is the fence D1266
	// rests on; TestProfileRefusesAProjectWorkflowSecret pins the same path
	// end to end, through ProjectJobs.
	workflowSecret := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectWorkflowSecretBlockLC)
	if _, ok := projectSecretContainers(workflowSecret)["sandbox-probe"]; !ok {
		t.Errorf("projectSecretContainers derives no container for a workflow_secrets entry: W2 derives one, and it is why the resolver below is the check that sees the name at all")
	}
	if env, ok := projectSecretEnv(workflowSecret, "sandbox-probe"); ok {
		t.Errorf("a workflow's own secret resolves to the variable %s", env)
	}

	// A provider's key is project-wide by construction (projectSecretContainers),
	// and it is mounted as the runner's provider variable.
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+providerLC+`profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [openrouter-api-key] }
`)
	j, ok := jobs[gcp.ProjectJobName("standard", "github", "")]
	if !ok {
		t.Fatalf("jobs = %v", sortedJobNames(jobs))
	}
	if j.SecretEnv["FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY"] != "openrouter-api-key" {
		t.Errorf("secret env = %v", j.SecretEnv)
	}
	if j.Account != "" {
		t.Errorf("account = %q: a profile that mounts no Claude token runs as no account", j.Account)
	}
}

// projectWorkflowSecretBlockLC is W5's fixture: one project workflow secret
// whose entry lists this installation's one GitHub repository, so
// projectSecretContainers derives its container.
//
// sandbox-probe is the name the tree's own fixture repository declares
// (deploy/sandbox/fugaro.yaml), so this speaks about a workflow secret a
// repository really has rather than one invented for the test.
const projectWorkflowSecretBlockLC = `workflow_secrets:
  sandbox-probe: { allow_data_to: [acme/webapp] }
`

// projectWorkflowSecretProfileLC is that block plus the profile D1266
// refuses: a shared job whose secrets list the name.
const projectWorkflowSecretProfileLC = projectWorkflowSecretBlockLC + `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [sandbox-probe] }
`

// TestProfileRefusesAProjectWorkflowSecret is W5's red step, and its subject
// is D1266: a profile is shared by every repository admitted to it, so a
// project workflow secret — whose whole gate is one entry's allow_data_to, a
// per-repository list — may not be mounted on a shared job. The refusal is
// the deployer's own and already in the tree: this task writes no line of it.
//
// The test pins the exact path the name takes today, and both halves of it:
// projectSecretContainers derives a container from the entry (W2), so
// projectJobSecrets' "not stored project-wide" check passes, and
// projectSecretEnv — which maps a key of config.ReservedSecrets and a
// configured provider's own secret, and nothing else — is the check that
// refuses it. Asserting the sentence rather than the bare fact of an error is
// what makes this fail if either half is widened: teach projectSecretEnv a
// workflow secret's variable and ProjectJobs deploys the job (D1266's
// rejected alternative, the one that would mount one value on every
// repository the profile admits); stop deriving the container and a different
// refusal fires.
//
// The rule cannot live in projectpolicy instead: CheckProfiles takes the
// policy's profiles and accounts and never the local config, so where the
// entry is absent — a teammate's machine, a CI checkout, a reader of the
// published node — the rule would be asked anyway and the profile admitted,
// which is a refusal that fails open and reads as enforced (F9).
func TestProfileRefusesAProjectWorkflowSecret(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectWorkflowSecretProfileLC)
	if _, ok := projectSecretContainers(lc)["sandbox-probe"]; !ok {
		t.Fatalf("projectSecretContainers has no container for sandbox-probe: the entry's non-empty allow_data_to derives one (D1263), and without it the refusal below is the container check's rather than the fence's")
	}
	_, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err == nil {
		t.Fatalf("ProjectJobs deployed a job for a profile mounting the project workflow secret sandbox-probe: a profile is shared by every repository admitted to it, and the entry's allow-list is not one a shared job can honour")
	}
	want := `profiles.standard.secrets[0]: "sandbox-probe" is neither a secret Fugaro reserves nor the key of a configured model provider`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want the entry's own refusal %q", err, want)
	}
	if env, ok := projectSecretEnv(lc, "sandbox-probe"); ok {
		t.Errorf("projectSecretEnv(sandbox-probe) = %q, true; want refused: this function is what the fence reads, and a workflow secret is neither a reserved name nor a configured provider's key", env)
	}
}

// TestProfileStillAcceptsAProviderKey is the other side of the fence: it
// refuses by what a name IS — a reserved name or a configured provider's
// secret — and not by where its container came from, so the widening W2 added
// to projectSecretContainers does not cost the mounts a shared job
// legitimately has today.
func TestProfileStillAcceptsAProviderKey(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+providerLC+`profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [openrouter-api-key] }
`)
	j, ok := jobs[gcp.ProjectJobName("standard", "github", "")]
	if !ok {
		t.Fatalf("jobs = %v, want the standard profile's job", sortedJobNames(jobs))
	}
	if j.SecretEnv["FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY"] != "openrouter-api-key" {
		t.Errorf("secret env = %v, want the provider's own key mounted as its provider variable", j.SecretEnv)
	}
}

// TestProfileStillAcceptsAReservedName is the same guard for the other kind
// projectSecretEnv maps: a platform-reserved name is still mounted on a
// shared job, so the fence refuses a workflow secret without costing the
// names the tree already reserves.
func TestProfileStillAcceptsAReservedName(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	j, ok := jobs[gcp.ProjectJobName("standard", "github", "")]
	if !ok {
		t.Fatalf("jobs = %v, want the standard profile's job", sortedJobNames(jobs))
	}
	if j.SecretEnv["ANTHROPIC_API_KEY"] != "anthropic-api-key" {
		t.Errorf("secret env = %v, want the reserved name mounted as its own variable", j.SecretEnv)
	}
}

// A Claude OAuth token is person-owned (design D2 and §7: D2 sweeps API keys
// and the git credential into project scope, never claude-oauth-token), and
// its allow_data_to — the repositories that may use the account — is a list a
// shared job cannot honour, since every repository the profile admits runs on
// the same job. So until phase 5 decides how an account reaches a shared job,
// a profile that mounts any Claude account's token is refused, the default
// account's and a named account's alike, rather than mounted with its
// allow-list silently ignored.
//
// The refusal is pinned to the LAYER that makes it, not to a phrase both
// layers could produce: the deployer's refusal is projectpolicy's rule
// (ProjectJobs runs CheckProfiles over the local config before it builds
// anything), so what this asserts is that the error is that function's own,
// word for word. A second refusal copy inside this package — which an earlier
// revision carried, unreachable behind the first — cannot satisfy it.
func TestProjectJobsRefuseAClaudeOAuthToken(t *testing.T) {
	for _, c := range []struct{ name, lc string }{
		{"the default account", `project_secrets:
  claude-oauth-token: { allow_data_to: [acme/sandbox] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [claude-oauth-token] }
`},
		{"a named account", claudeAccountsLC + `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [claude-oauth-token-acme-personal] }
`},
	} {
		lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+c.lc)
		// The accounts block the deployer judges the profiles against is
		// derived from the same config, exactly as ProjectJobs derives it.
		accounts, err := projectpolicy.DeriveAccounts(profilesAsPolicy(lc.Profiles), lc.ClaudeAccounts, lc.ProjectSecrets, nil)
		if err != nil {
			t.Fatalf("%s: DeriveAccounts: %v", c.name, err)
		}
		want := projectpolicy.CheckProfiles(profilesAsPolicy(lc.Profiles), accounts)
		if want == nil {
			t.Fatalf("%s: the policy admits the profile, so nothing below is about the layer that refuses it", c.name)
		}
		_, err = ProjectJobs(lc, "fugaro-runs-proj-1234", false)
		if err == nil {
			t.Fatalf("%s: ProjectJobs deployed a job for a profile mounting a Claude OAuth token", c.name)
		}
		if err.Error() != want.Error() {
			t.Errorf("%s: err = %v, want the policy's own refusal %v", c.name, err, want)
		}
		if !strings.Contains(err.Error(), "person-owned") || !strings.Contains(err.Error(), "phase 5") {
			t.Errorf("%s: err = %v, want a refusal naming the token person-owned until phase 5", c.name, err)
		}
	}
}

// A profile of a provider the shared-job list does not hold is skipped and not
// refused, which is the whole of this test: the policy layer admits such a
// profile (projectpolicy.validProfiles, and TestProfileAdmitsOnlyItsOwnProvider),
// a repository's workflow may select it, and the runner only requires a selected
// profile to be defined — so an installation whose local config names one must
// deploy, and the profile simply gets no job. Refusing the installation whole,
// as this function used to, made `fugaro update` fail on a document `fugaro
// project policy publish` accepts.
//
// The list serves every kind config.Providers names since I1 (github and
// bitbucket — TestSharedJobProviderIsTheProvidersList is the shipped list's own
// test), so no profile reaches this path today and the test holds the list back
// to github to walk it. It is live code all the same: the skip is what keeps the
// next provider kind inert until its whole path is built, which is exactly the
// reason I1's own change waited on the sides of docs/shared-jobs-default.
//
// What a shared job never does, whatever provider the profile names, is mount
// the Bitbucket token, which the last case below refuses under the held list
// too.
//
// Mutation (verified, run and restored): refuse instead of skipping (return
// nil, userErr(...) for !SharedJobProvider) — the first case stops with that
// refusal instead of deploying the GitHub profile beside it. Mutation: drop the
// skip (projectJobs), and the first case stops on the Bitbucket profile's own
// refusal of this fixture, which records no credentials service: the skip is
// what keeps a profile the list does not hold out of the rest of the composer,
// and never what hides it from the policy's rules.
func TestProjectJobsSkipBitbucketProfiles(t *testing.T) {
	admitSharedJobProviders(t, "github")
	// A project with repositories on both providers defines both profiles
	// (the plan's own example, standard and standard-bitbucket): the GitHub
	// one is deployed, the Bitbucket one is not, and the call succeeds.
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+`profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
  standard-bitbucket: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`)
	if got := slices.Sorted(maps.Keys(jobs)); !slices.Equal(got, []string{gcp.ProjectJobName("standard", "github", "")}) {
		t.Errorf("jobs = %v, want the GitHub profile's alone: a shared job never serves a provider the list does not hold", got)
	}
	// The Bitbucket profile is still held to the policy's rules — CheckProfiles
	// runs over every profile before any of them is skipped, so a skip is not
	// a way to deploy an installation around a profile the policy would refuse.
	// The Bitbucket profile here is valid, so nothing is refused; the
	// assertion is that the GitHub one beside it is deployed in full.
	for _, j := range jobs {
		if j.Provider != "github" {
			t.Errorf("a job was deployed for the %s provider: %+v", j.Provider, j)
		}
	}
	// A local config naming the Bitbucket profile alone deploys nothing at all,
	// rather than refusing: the map is empty, the tfvars key is left out and
	// the root creates nothing, exactly as for an installation with no
	// profiles. The config records no GitHub repository and so no GitHub App
	// ID (m4LocalConfig's repository is a Bitbucket one): the App ID is
	// required only when some profile gets a shared job, and none does here
	// (TestProjectJobsNeedAGitHubAppID is the GitHub side of that).
	if jobs := projectJobsFor(t, m4LocalConfig+m5Additions+`profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`); len(jobs) != 0 {
		t.Errorf("jobs = %v, want none: no shared job serves Bitbucket", slices.Sorted(maps.Keys(jobs)))
	}
	// The token is the other half of the ruling and is still refused: a shared
	// job that serves GitHub may not mount a credential that reaches every
	// repository of a Bitbucket workspace. The sentence is the policy's own
	// (projectpolicy.validBitbucketProfile), applied to every profile before
	// any job is built — so a skip is not a way around it.
	if _, err := ProjectJobs(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+`project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [bitbucket-token] }
`), "fugaro-runs-proj-1234", false); err == nil ||
		!strings.Contains(err.Error(), `profiles.standard.secrets[0]: "bitbucket-token" is the Bitbucket credential`) {
		t.Errorf("err = %v, want the Bitbucket token refused on a GitHub profile", err)
	}
}

// admitSharedJobProviders holds the shared-job provider list to the kinds
// named, for the duration of one test. The list is what decides whether a
// profile is served, and the skip path — a profile of a provider the list does
// not hold, skipped rather than refused — is reachable only with the list held
// back: the release serves both kinds config.Providers names since I1
// (SharedJobProvider), and a test of a served provider reads the list as it
// ships.
func admitSharedJobProviders(t *testing.T, kinds ...string) {
	t.Helper()
	orig := sharedJobProviders
	sharedJobProviders = kinds
	t.Cleanup(func() { sharedJobProviders = orig })
}

// bitbucketProjectLC is the local config of a project whose repositories and
// profiles are all Bitbucket's: m4LocalConfig's own repository (acme/sandbox on
// bitbucket), the credentials service switched on — which a shared Bitbucket
// job's runs reach git through — and one Bitbucket profile that mounts nothing.
const bitbucketProjectLC = `profiles:
  standard: { provider: bitbucket, cpu: 4, memory: 16Gi, timeout_class: 12h }
`

// TestProjectJobsDeployABitbucketProfile is S1 of docs/shared-jobs-default and
// the deployer half of docs/bitbucket-workspace-token's T2.2 (D2302): a shared
// project job serves a Bitbucket profile, and it serves it the only way such a
// job may — with no Bitbucket credential mounted and the credential proxy
// required instead, because the job is shared by every repository its profile
// admits and the token's reach is a per-repository list it cannot honour.
//
// The provider is served by the list as it ships since I1, and that is the red
// step this test used to fail at: it replaces the list no longer, so the job
// below is composed by the code under it — remove "bitbucket" from
// sharedJobProviders (the pre-I1 state) and the first call deploys nothing.
func TestProjectJobsDeployABitbucketProfile(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions+credentialsServiceLCBlock+bitbucketProjectLC)
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	name := gcp.ProjectJobName("standard", "bitbucket", "")
	if !slices.Equal(sortedJobNames(jobs), []string{name}) {
		t.Fatalf("jobs = %v, want the profile's one job %s: a shared job serves every provider kind SharedJobProvider holds", sortedJobNames(jobs), name)
	}
	j := jobs[name]
	if j.Profile != "standard" || j.Provider != "bitbucket" || j.Account != "" {
		t.Errorf("job = (%s, %s, account %q), want (standard, bitbucket, \"\")", j.Profile, j.Provider, j.Account)
	}
	// It holds no credential of the provider it serves: no bitbucket-token,
	// which is D2302's whole point, and no github-app-key either, which belongs
	// to a job that clones GitHub repositories.
	if len(j.SecretEnv) != 0 {
		t.Errorf("the Bitbucket job mounts %v, want nothing: a shared Bitbucket job holds no token at all (D2302)", j.SecretEnv)
	}
	// It requires the proxy instead. The credentials service's address on the
	// job is what makes its runs' git traffic proxied — internal/runner reads
	// exactly this variable to decide that a Bitbucket run is proxied at all —
	// and a job deployed without it halts before anything is cloned.
	if got := j.Env[providers.EnvCredentialsServiceURL]; got != lc.CredentialsService.URL {
		t.Errorf("%s = %q, want %q: a shared Bitbucket job reaches git through the installation's credentials service, and this is how a run learns where it is", providers.EnvCredentialsServiceURL, got, lc.CredentialsService.URL)
	}
	if _, ok := j.Env[providers.EnvGitHubAppID]; ok {
		t.Errorf("the Bitbucket job carries %s: the App's ID is a github job's, resolved from the project's github repositories", providers.EnvGitHubAppID)
	}
	if j.CPU != "4" || j.Memory != "16Gi" {
		t.Errorf("job = %s cpu, %s memory, want the profile's own 4 and 16Gi", j.CPU, j.Memory)
	}
	timeout, err := config.ParseDuration("12h")
	if err != nil {
		t.Fatal(err)
	}
	if want := int((timeout + backend.TaskTimeoutSlack) / time.Second); j.TaskTimeoutS != want {
		t.Errorf("task timeout = %ds, want %ds", j.TaskTimeoutS, want)
	}
	// An installation that will not deploy a credentials service cannot give
	// the job the proxy it needs and has no later apply to fill one in, and
	// the profile is refused rather than deployed as a job whose every run
	// stops at its first git command. This config names no Firebase backend,
	// which is what CredentialsInstallationFacts refuses, so the sentence the
	// operator reads carries that stage's own remedy.
	if _, err := ProjectJobs(parseLC(t, m4LocalConfig+m5Additions+bitbucketProjectLC), "fugaro-runs-proj-1234", false); err == nil {
		t.Error("a shared Bitbucket job was deployed on an installation that will not deploy a credentials service")
	} else {
		for _, want := range []string{providers.EnvCredentialsServiceURL, "firebase_project", "fugaro update --firebase-project"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one naming %q", err, want)
			}
		}
	}
}

// TestProjectJobsBitbucketJobConvergesOnTheServiceURL is the second half of
// D2302's rule about the proxy: the job requires the credentials service's
// address, and an installation that does not record it yet is still deployed a
// job — the run that deploys the service is the one whose config cannot carry
// the block yet (init.go's writeConfig, that run's last step), and a refusal
// here would stop the very update that gives the job its address, under a
// sentence telling the operator to run the command they are running.
//
// What converges is the pair, which is what this test walks: the run that
// creates the service deploys the job without the variable, records the block,
// and the next apply reads that record and puts the address on the job.
func TestProjectJobsBitbucketJobConvergesOnTheServiceURL(t *testing.T) {
	const before = m4LocalConfig + m5Additions + credentialsBudgetLC + bitbucketProjectLC
	name := gcp.ProjectJobName("standard", "bitbucket", "")
	jobs, err := ProjectJobs(parseLC(t, before), "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatalf("the run that deploys the credentials service was refused: %v", err)
	}
	if !slices.Equal(sortedJobNames(jobs), []string{name}) {
		t.Fatalf("jobs = %v, want the profile's one job %s", sortedJobNames(jobs), name)
	}
	if got, ok := jobs[name].Env[providers.EnvCredentialsServiceURL]; ok {
		t.Errorf("%s = %q, want it absent: this installation records no credentials service yet, and the address is what the apply after this one adds", providers.EnvCredentialsServiceURL, got)
	}
	// The same config as the deploy run's own stages read it:
	// credentialsServiceOptedIn's presence-only block, whose empty url is the
	// state of every reader between the apply that created the service and the
	// one that recorded its URL.
	optedIn := *parseLC(t, before)
	optedIn.CredentialsService = &localcfg.CredentialsService{}
	if jobs, err = ProjectJobs(&optedIn, "fugaro-runs-proj-1234", false); err != nil {
		t.Fatalf("the deploy run's own presence-only block was refused: %v", err)
	}
	if got, ok := jobs[name].Env[providers.EnvCredentialsServiceURL]; ok {
		t.Errorf("%s = %q, want it absent: an empty url is a service whose address the apply has not recorded yet", providers.EnvCredentialsServiceURL, got)
	}
	after := parseLC(t, before+credentialsServiceLCBlock)
	jobs, err = ProjectJobs(after, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := jobs[name].Env[providers.EnvCredentialsServiceURL]; got != after.CredentialsService.URL {
		t.Errorf("%s = %q, want %q: the address the run that deployed the service recorded reaches the job on the next apply", providers.EnvCredentialsServiceURL, got, after.CredentialsService.URL)
	}
}

// TestProjectJobsRefuseAProfileThatMountsTheBitbucketToken: D2302 refuses a
// shared job for a profile that mounts the Bitbucket credential, rather than
// deploying one without the mount. The two are not the same act: the mount is
// what an operator wrote, and a job deployed with it silently dropped would
// hold no credential at all while the document says it holds one.
//
// It is the opposite of projectpolicy.SharedBitbucketProfile, which is the
// PER-REPOSITORY Bitbucket job's test (the bitbucket provider AND the mount),
// and this test asserts the two rules point apart — the hazard
// docs/shared-jobs-default/contract.md names, since that predicate is the name
// a side is likeliest to reach for and reaching for it here would deploy a
// tokenless shared job for exactly the profiles D2302 refuses one.
func TestProjectJobsRefuseAProfileThatMountsTheBitbucketToken(t *testing.T) {
	const mounting = m4LocalConfig + m5Additions + credentialsServiceLCBlock + `project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [bitbucket-token], allow_repos: [bitbucket/acme/sandbox] }
`
	jobs, err := ProjectJobs(parseLC(t, mounting), "fugaro-runs-proj-1234", false)
	if err == nil {
		t.Fatalf("jobs = %v, want the profile refused: a shared Bitbucket job mounts no token (D2302)", sortedJobNames(jobs))
	}
	for _, want := range []string{"profiles.standard.secrets[0]", config.BitbucketSecret, "credentials service"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one naming %q", err, want)
		}
	}
	// The mount is judged before the proxy is looked for, so the sentence an
	// operator reads is about the entry they wrote and not about the
	// installation: the same config with no credentials service at all is
	// still refused for the mount.
	withoutService := parseLC(t, strings.Replace(mounting, credentialsServiceLCBlock, "", 1))
	if _, err := ProjectJobs(withoutService, "fugaro-runs-proj-1234", false); err == nil {
		t.Error("a profile mounting the Bitbucket credential was admitted")
	} else if !strings.Contains(err.Error(), config.BitbucketSecret) {
		t.Errorf("err = %v, want the mount refused here rather than the missing service", err)
	}
	// The per-repository predicate is still what it was (D473, pinned by
	// projectpolicy's own test), and this is the pair: it answers true for the
	// one profile a shared job must be refused for.
	if !projectpolicy.SharedBitbucketProfile(parseLC(t, mounting).Profiles["standard"]) {
		t.Error("SharedBitbucketProfile no longer answers true for a bitbucket profile mounting the token: the per-repository predicate and the shared-job rule are meant to point apart, and a side that reached for the predicate would deploy a tokenless shared job for exactly this profile")
	}
}

// TestSharedJobProviderIsTheProvidersList: SharedJobProvider is the one place
// "a shared project job serves this provider" is decided, and both sides of it
// ask — the deployer skips a profile it does not serve and the launcher keeps
// such a repository on its per-repository job — so the two can never disagree
// about which jobs exist. Every kind a repository may use is answered, and the
// served list holds kinds of config.Providers: a provider added to that list is
// not silently given a shared job by a forgotten line here, which is the
// mutation this test is for.
func TestSharedJobProviderIsTheProvidersList(t *testing.T) {
	// Both kinds a repository may use are served since I1: the slice that
	// built the Bitbucket path merged behind this list (a job that mounts no
	// token, requires the credential proxy, D2302, and is proven end to end),
	// and I1 is the line that put it in service. A provider kind is admitted
	// here only with its whole path built — the job the deployer builds, the
	// credential the job does not hold and the launcher that chooses the job —
	// which is why the expectation is written out rather than derived: a third
	// kind added to config.Providers must fail here, not be silently served.
	served := []string{"github", "bitbucket"}
	for _, kind := range config.Providers {
		want := slices.Contains(served, kind)
		if got := SharedJobProvider(kind); got != want {
			t.Errorf("SharedJobProvider(%q) = %v, want %v", kind, got, want)
		}
	}
	if SharedJobProvider("") || SharedJobProvider("fake") || SharedJobProvider("gitlab") {
		t.Error("SharedJobProvider answers true for a kind that is not a provider: a run with no provider, the tests' fake kind and a host nobody serves must all keep their per-repository job")
	}
	if len(sharedJobProviders) == 0 {
		t.Fatal("no provider is served, so every profile is skipped and no project job is ever deployed")
	}
	for _, kind := range sharedJobProviders {
		if !slices.Contains(config.Providers, kind) {
			t.Errorf("sharedJobProviders names %q, which is not a provider kind of config.Providers (%v)", kind, config.Providers)
		}
	}
}

// A profile the policy would refuse is refused here too, with the policy's
// own rules (projectpolicy.CheckProfiles): init must not deploy a job that
// `fugaro project policy publish` would refuse, because nobody could ever
// select it.
func TestProjectJobsRefuseAProfileThePolicyWouldRefuse(t *testing.T) {
	for _, c := range []struct{ name, profiles, want string }{
		{"no provider", `profiles:
  standard: { cpu: 2, memory: 8Gi, timeout_class: 6h }
`, "must name the provider"},
		{"no standard profile", `profiles:
  heavy: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
`, "standard must be defined"},
		{"resources Cloud Run refuses", `profiles:
  standard: { provider: github, cpu: 8, memory: 1Gi, timeout_class: 6h }
`, "at least"},
		{"a timeout that is not one", `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: "soon" }
`, "not a duration"},
		{"a pattern of another provider", `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [bitbucket/acme/web] }
`, "this profile serves github"},
	} {
		_, err := ProjectJobs(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+c.profiles), "fugaro-runs-proj-1234", false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %q", c.name, err, c.want)
		}
	}
}

// An installation whose local config names no profile creates none: the
// tfvars key is left out, so its plan is what it was before profiles existed.
func TestInstallationWithoutProfilesWritesNoProjectJobs(t *testing.T) {
	data, err := InstallationVars(installationSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["project_jobs"]; ok {
		t.Errorf("an installation with no profiles writes project_jobs:\n%s", data)
	}
	jobs, err := ProjectJobs(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions), "fugaro-runs-proj-1234", false)
	if err != nil || jobs != nil {
		t.Errorf("ProjectJobs = %v, %v; want none", jobs, err)
	}
}

// projectAccountLCBlock is the local config block the recomputation test is
// built with: a profile serving no Claude account (an api-key profile, whose
// job's account segment is empty) and one serving a named account and the
// default one, with the project-wide copies both need.
const projectAccountLCBlock = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  apikey: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [github-app-key] }
  standard: { provider: github, cpu: 1, memory: 2Gi, timeout_class: 1h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal, default], secrets: [github-app-key] }
`

// The account axis is computed on two sides, from two different documents: the
// deployer computes it while it deploys (config.ProjectJobAccounts of the
// profile it is building jobs for), and the runner recomputes it at run start
// from the published policy's profile, which carries the same field
// (projectJobCandidates, plan task 3.3 step 1). They must agree: a job whose
// name the runner would not recompute is a job every run of that profile halts
// on, because the run cannot place the name it is in and adopts no identity
// from it.
//
// What is asserted is the deployed *name* — the key the runner matches its
// CLOUD_RUN_JOB against — and not only the field beside it, because the name is
// what a run has to reproduce.
func TestProjectJobAccountIsWhatTheRunnerRecomputes(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountLCBlock)
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	// The deployer's set: one job per (profile, provider, account), the
	// no-account job of each profile included. Written out rather than
	// computed here, because what the runner recomputes is exactly the list
	// this literal is: the axis config.ProjectJobAccounts returns is asserted
	// against the field below.
	want := []string{
		gcp.ProjectJobName("apikey", "github", ""),
		gcp.ProjectJobName("standard", "github", ""),
		gcp.ProjectJobName("standard", "github", "acme-personal"),
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(jobs)); !slices.Equal(got, want) {
		t.Fatalf("jobs = %v, want the names the runner recomputes, %v", got, want)
	}
	for _, name := range want {
		j := jobs[name]
		// The account is one the profile's own field holds: the runner
		// recomputes the name from that field alone, so an account the axis
		// does not carry is a job the run would never place.
		if !slices.Contains(config.ProjectJobAccounts(lc.Profiles[j.Profile]), j.Account) {
			t.Errorf("the job %s runs as the account %q, which the profile %s does not serve (config.ProjectJobAccounts = %v): the run in it would recompute a name the deployer never built",
				name, j.Account, j.Profile, config.ProjectJobAccounts(lc.Profiles[j.Profile]))
		}
		if recomputed := gcp.ProjectJobName(j.Profile, j.Provider, j.Account); recomputed != name {
			t.Errorf("the runner computes the job name %s for the job the deployer built as %s (profile %s, account %q)", recomputed, name, j.Profile, j.Account)
		}
	}
}

// projectAccountAxisLC is the account axis's fixture: one named account and
// the default one, both stored project-wide for the repository the config
// records, and one profile serving them both beside the no-account job every
// profile keeps. github-app-key rides along, so an account job's secret_env
// holds a secret that is not a token: the account's token is added beside the
// profile's own mounts, never instead of them.
//
// The profile writes its own allow_repos because it lists accounts (D3): a
// profile that serves accounts must name the repositories it admits exactly,
// and every account it serves must cover them.
const projectAccountAxisLC = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal, default], secrets: [github-app-key] }
`

// providerProfileLCBlock is the fixture a profile that mounts a PROVIDER's key
// needs: one provider whose secret is its own key, admitted to this repository
// by its own allow_data_to (a provider's key is not a project_secrets entry —
// that block holds the reserved names alone, config.ValidateProjectSecrets) —
// and one profile mounting the App key, the Anthropic key and that provider's
// key. It is what TestFilledSharedJobListDropsTheModelMounts reads to pin the
// one model credential B5.7's flip leaves standing in a served profile's job,
// beside the named one it takes: nothing serves a provider's key yet, so its
// drop is the keys-through-service plan's KS16 (R6, D1634).
const providerProfileLCBlock = `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
    allow_data_to: ["acme/webapp"]
project_secrets:
  anthropic-api-key: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [github-app-key, anthropic-api-key, openrouter-api-key] }
`

// TestProjectJobAccountAxis: a profile that serves Claude accounts gets one
// job per account beside the no-account job every profile keeps (design §4.1,
// D5 and D9). The axis is the profile's own claude_accounts field, computed by
// config.ProjectJobAccounts — the one function the deployer, the launcher and
// the runner share — and every entry names a job and a job service account of
// its own, so no two accounts ever share one.
//
// Mutation (run, restore): build the no-account job alone (the pre-task code,
// `const account = ""`), which leaves the account's job undeployed while the
// launcher targets it and the runner recomputes its name.
func TestProjectJobAccountAxis(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	want := map[string]string{
		gcp.ProjectJobName("standard", "github", ""):              "",
		gcp.ProjectJobName("standard", "github", "acme-personal"): "acme-personal",
	}
	if !slices.Equal(sortedJobNames(jobs), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("jobs = %v, want one per (profile, provider, account) of %v", sortedJobNames(jobs), want)
	}
	seenSA := map[string]string{}
	for name, account := range want {
		j := jobs[name]
		if j.Profile != "standard" || j.Provider != "github" || j.Account != account || j.Job != name {
			t.Errorf("job %s = profile %s, provider %s, account %q, job %q; want standard, github, %q, %s",
				name, j.Profile, j.Provider, j.Account, j.Job, account, name)
		}
		if got := gcp.ProjectServiceAccountID("standard", "github", account); j.ServiceAccount.AccountID != got {
			t.Errorf("job %s runs as %s, want %s", name, j.ServiceAccount.AccountID, got)
		}
		if prev, dup := seenSA[j.ServiceAccount.AccountID]; dup {
			t.Errorf("jobs %s and %s share the service account %s: the whole point of the account axis is that one job's credential is readable by that job alone", prev, name, j.ServiceAccount.AccountID)
		}
		seenSA[j.ServiceAccount.AccountID] = name
	}
}

// keyCredentialAccountAxisLC is the account axis's fixture with a KEY
// credential beside a token account: acme-key is a name `fugaro accounts add
// --type openrouter` declares (MC22, D1670), whose value is stored under the
// credential's own derived secret (config.CredentialSecret) and never under
// acme-key's claude-oauth-token container, and acme-personal is the account
// whose token a job of this profile really holds. The profile lists both,
// because a key credential's claude_accounts entry keeps the meaning it has
// for such a name — the list of repositories the key may be sent to — and only
// the JOB is what the name must not get (config.ClaudeAccountTokenNames).
const keyCredentialAccountAxisLC = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
  acme-key: { allow_data_to: [acme/webapp] }
credentials:
  - { name: acme-key, type: openrouter }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal, acme-key], secrets: [github-app-key] }
`

// TestProjectJobsLeaveAKeyCredentialOut is MC22 (D1670) at the project job
// set, and it is the same rule TestAKeyCredentialDeploysNoJobMountingAnEmptyToken
// pins on the per-repository set: a name the local config declares a KEY
// credential gets no job, because every job of the axis exists to carry an
// account's Claude token — projectAccountMount mounts
// config.ClaudeAccountSecret(acme-key) as CLAUDE_CODE_OAUTH_TOKEN — and
// nothing ever writes a version to that container for such a name. A job built
// for it would mount an empty token, the run launched on it would fail in the
// credential, and `fugaro secrets ls` would flag a MISSING name no
// `claude setup-token` step could fill. The rule is the one WorkflowAccounts
// applies (config.ClaudeAccountTokenNames), reused rather than restated: the
// name's claude_accounts entry keeps the reach it bounds, and only the job
// goes.
//
// The token account beside it is the control: the rule is about the
// credential's TYPE and not about the profile's list, so acme-personal's job,
// its own job service account and its token mount stay exactly where they
// were — and the no-account job every profile keeps stays with them (D9).
//
// Mutation (run, restore): iterate config.ProjectJobAccounts(pr) alone (the
// pre-fix code), which gives the key account a job and a job service account
// again, both mounting the empty token.
func TestProjectJobsLeaveAKeyCredentialOut(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+keyCredentialAccountAxisLC)
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	key := gcp.ProjectJobName("standard", "github", "acme-key")
	keySA := gcp.ProjectServiceAccountID("standard", "github", "acme-key")
	token := config.ClaudeAccountSecret("acme-key")
	for name, j := range jobs {
		if name == key || j.Job == key || j.Account == "acme-key" {
			t.Errorf("the job set holds %s (profile %s, provider %s, account %q): a name declared a key credential gets no job, and every job of this axis mounts %s as %s — a container nothing writes a version to for such a name (its value is %s)",
				name, j.Profile, j.Provider, j.Account, token, config.ReservedSecrets[config.ClaudeOAuthToken], config.CredentialSecret("acme-key"))
		}
		if j.ServiceAccount.AccountID == keySA {
			t.Errorf("the job %s runs as %s, the job service account of acme-key: the secretAccessor member follows the mount, so an account that mounts nothing must have no service account either", name, keySA)
		}
		if _, mounted := j.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; mounted && j.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]] == token {
			t.Errorf("the job %s mounts %s as %s: no job mounts a key credential's empty token container", name, token, config.ReservedSecrets[config.ClaudeOAuthToken])
		}
	}
	// The set is not emptied by the rule: the profile's no-account job and its
	// one token account's job are the two it deploys.
	want := []string{
		gcp.ProjectJobName("standard", "github", ""),
		gcp.ProjectJobName("standard", "github", "acme-personal"),
	}
	if got := sortedJobNames(jobs); !slices.Equal(got, want) {
		t.Fatalf("jobs = %v, want %v: the profile's no-account job and acme-personal's — the token account the profile serves", got, want)
	}
	personal := jobs[gcp.ProjectJobName("standard", "github", "acme-personal")]
	if got := personal.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; got != config.ClaudeAccountSecret("acme-personal") {
		t.Errorf("acme-personal's job mounts %s = %q, want %q", config.ReservedSecrets[config.ClaudeOAuthToken], got, config.ClaudeAccountSecret("acme-personal"))
	}
	if got := gcp.ProjectServiceAccountID("standard", "github", "acme-personal"); personal.ServiceAccount.AccountID != got {
		t.Errorf("acme-personal's job runs as %s, want %s", personal.ServiceAccount.AccountID, got)
	}
	// And the account stays where it was, in the block a publish derives from
	// the same config: a key credential keeps its claude_accounts entry, whose
	// allow_data_to is the reach its key may be sent to (D1670) — the rule here
	// drops a job and never the account.
	accounts, err := projectpolicy.DeriveAccounts(profilesAsPolicy(lc.Profiles), lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := accounts["acme-key"]; !ok {
		t.Errorf("the accounts block lost acme-key: %v — a key credential keeps its claude_accounts entry, whose allow_data_to is the reach its key may be sent to", slices.Sorted(maps.Keys(accounts)))
	}
}

// TestProjectJobNoAccountJobUnchanged: a profile taking an account on does not
// move the job it already had. The no-account job's name, service account,
// env, resources and mounts are what they were before the profile listed
// anything (D9) — which is why a rollout adds jobs and replaces none, and why
// RefuseProjectJobDeletes never fires on it.
//
// Mutation (run, restore): derive the job's name from the profile's mounts or
// from its accounts (projectStem with any account but the empty one), which
// renames every existing job the moment its profile gains an account.
func TestProjectJobNoAccountJobUnchanged(t *testing.T) {
	const before = `project_secrets:
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [github-app-key] }
`
	// The same profile, serving one named account: allow_repos and
	// claude_accounts are the only lines that differ, and neither is part of
	// the job a profile deploys.
	const after = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal], secrets: [github-app-key] }
`
	name := gcp.ProjectJobName("standard", "github", "")
	one := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+before)
	two := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+after)
	if len(one) != 1 || one[name].Account != "" {
		t.Fatalf("the profile with no account deployed %v, want the no-account job %s alone", sortedJobNames(one), name)
	}
	if !reflect.DeepEqual(one[name], two[name]) {
		t.Errorf("the no-account job changed when the profile took an account on:\nbefore %+v\nafter  %+v", one[name], two[name])
	}
	if _, ok := two[gcp.ProjectJobName("standard", "github", "acme-personal")]; !ok {
		t.Errorf("the profile serving acme-personal deployed %v, want its account job beside the no-account one", sortedJobNames(two))
	}
}

// TestAccountJobMountsOnlyItsOwnToken is this task's security property on the
// deployer's side: each account's token is named in exactly one job's
// secret_env — the job of that account — and in no other job's. The grant
// follows the mount (modules/project/sa.tf's
// google_secret_manager_secret_iam_member.job iterates the job's own
// secret_env), so the one job service account that may read an account's token
// is that account's job's: one token, one job, one service account.
//
// Mutation (run, restore): have projectAccountMount mount every account the
// profile lists on every account job (the plan's red step) — each token then
// rides on its neighbour's job, and with it its secretAccessor member.
func TestAccountJobMountsOnlyItsOwnToken(t *testing.T) {
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	v := config.ReservedSecrets[config.ClaudeOAuthToken]
	noAccount := gcp.ProjectJobName("standard", "github", "")
	account := gcp.ProjectJobName("standard", "github", "acme-personal")
	want := map[string]string{
		config.ClaudeAccountSecret(config.DefaultClaudeAccount): noAccount,
		config.ClaudeAccountSecret("acme-personal"):             account,
	}
	holders := map[string][]string{}
	for name, j := range jobs {
		for env, secret := range j.SecretEnv {
			if config.ClaudeOAuthSecret(secret) {
				holders[secret] = append(holders[secret], name+" as "+env)
			}
		}
	}
	for secret, job := range want {
		if got := holders[secret]; !slices.Equal(got, []string{job + " as " + v}) {
			t.Errorf("the token %s is mounted on %v, want on the job %s alone (as %s): the secretAccessor member follows secret_env, so a second job naming it is a second service account that can read it",
				secret, got, job, v)
		}
	}
	for secret, got := range holders {
		if _, ok := want[secret]; !ok {
			t.Errorf("the token %s is mounted on %v, and no account of this installation is %q", secret, got, secret)
		}
	}
	// Each job's token variable names its own account's secret, and the
	// profile's own mount rides beside it rather than being replaced by it.
	for name, wantSecret := range map[string]string{noAccount: config.ClaudeAccountSecret(config.DefaultClaudeAccount), account: config.ClaudeAccountSecret("acme-personal")} {
		j := jobs[name]
		if got := j.SecretEnv[v]; got != wantSecret {
			t.Errorf("job %s mounts %s = %q, want %q", name, v, got, wantSecret)
		}
		if got := j.SecretEnv[config.ReservedSecrets["github-app-key"]]; got != "github-app-key" {
			t.Errorf("job %s mounts github-app-key = %q, want the profile's own mount beside its account's token", name, got)
		}
	}
}

// TestProjectJobRegistersItsMountedSecretsForRedaction: every variable a
// project job mounts a secret as is named in that job's own
// runner.SecretEnvsVar, which is what the runner registers with its redactor
// before bootstrap (internal/runner's addMountedSecrets).
//
// It is the project path's half of what the per-repository path has done since
// the redactor existed (internal/infra/spec.go sets the variable there), and it
// is load-bearing for an account job whatever the run's agent.auth is: the
// token is in the job's environment, so an api-key or Vertex run of a profile
// that lists the default account holds a credential the agent can read and the
// run can print. SECURITY.md states the redactor masks what Fugaro publishes of
// such a run; without this variable it does not.
//
// One source, per job: the list is the job's own SecretEnv keys — the same map
// the module turns into secret_key_refs — and never a second list kept beside
// it, so a mount added or dropped for one account cannot leave the redaction
// list behind. The account jobs of one profile differ (each mounts its own
// token, or none), which is why the list is computed per job and not once per
// profile.
//
// Mutation (run, restore): drop the assignment in ProjectJobs (the pre-fix
// code), which leaves every project job with no FUGARO_SECRET_ENVS at all, or
// set it once from the profile's mounts before the loop, which gives the
// account job a list that does not name its own token.
func TestProjectJobRegistersItsMountedSecretsForRedaction(t *testing.T) {
	// The account axis's own fixture: the profile serves the named account and
	// the default one, so both jobs mount a token beside the profile's mount.
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	v := config.ReservedSecrets[config.ClaudeOAuthToken]
	for name, j := range jobs {
		want := strings.Join(slices.Sorted(maps.Keys(j.SecretEnv)), ",")
		if got := j.Env[runner.SecretEnvsVar]; got != want {
			t.Errorf("job %s sets %s = %q, want %q: the variables it mounts secrets as are what the runner redacts", name, runner.SecretEnvsVar, got, want)
		}
		if got := j.Env[runner.SecretEnvsVar]; got == "" || !strings.Contains(got, v) {
			t.Errorf("job %s sets %s = %q, which does not name %s, the variable its token is mounted as: a run in this job that printed its environment would publish the account's token", name, runner.SecretEnvsVar, got, v)
		}
		if _, dup := j.SecretEnv[runner.SecretEnvsVar]; dup {
			t.Errorf("job %s mounts a secret as %s, which is the redaction list's own variable", name, runner.SecretEnvsVar)
		}
	}

	// And the list is the job's own: a profile that serves a named account
	// without the default one mounts no token on its no-account job, so that
	// job's list names the profile's mount alone while the account job's names
	// both. The profile is the account axis's with `default` dropped.
	const namedOnly = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal], secrets: [github-app-key] }
`
	appKey := config.ReservedSecrets["github-app-key"]
	one := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+namedOnly)
	want := map[string]string{
		gcp.ProjectJobName("standard", "github", ""):              appKey,
		gcp.ProjectJobName("standard", "github", "acme-personal"): v + "," + appKey,
	}
	if len(one) != len(want) {
		t.Fatalf("the profile listed acme-personal alone and deployed %v, want its two jobs", sortedJobNames(one))
	}
	for name, w := range want {
		if got := one[name].Env[runner.SecretEnvsVar]; got != w {
			t.Errorf("job %s sets %s = %q, want %q", name, runner.SecretEnvsVar, got, w)
		}
	}
}

// TestDefaultAccountOnNoAccountJob: the default account is not a job of its
// own (D5). A profile that lists it serves it on the no-account job every
// profile keeps, so listing it adds no job and moves no name — and no job of
// any profile is named for it, which is the shape the runner must never
// compute (the account segment of that job is empty).
//
// Mutation (run, restore): give the default account an entry of its own in
// config.ProjectJobAccounts, which deploys a second job whose name spells
// "default" while the launcher and the runner keep targeting the no-account
// one.
func TestDefaultAccountOnNoAccountJob(t *testing.T) {
	const lc = `project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [default], secrets: [github-app-key] }
`
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+lc)
	name := gcp.ProjectJobName("standard", "github", "")
	if !slices.Equal(sortedJobNames(jobs), []string{name}) {
		t.Fatalf("a profile listing the default account deployed %v, want its no-account job %s alone", sortedJobNames(jobs), name)
	}
	if got := jobs[name].Account; got != "" {
		t.Errorf("the no-account job runs as %q, want the empty account", got)
	}
	if got := jobs[name].SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; got != config.ClaudeAccountSecret(config.DefaultClaudeAccount) {
		t.Errorf("the no-account job mounts %s = %q, want the default account's token %q", config.ReservedSecrets[config.ClaudeOAuthToken], got, config.ClaudeAccountSecret(config.DefaultClaudeAccount))
	}
	for jobName := range jobs {
		if strings.Contains(jobName, "-"+config.DefaultClaudeAccount+"-") {
			t.Errorf("the job %s is named for the default account, and the default account rides the no-account job", jobName)
		}
	}
	// The axis says the same thing: the default account is spelled as the
	// empty one, once.
	if got := config.ProjectJobAccounts(config.JobProfile{ClaudeAccounts: []string{config.DefaultClaudeAccount}}); !slices.Equal(got, []string{""}) {
		t.Errorf("config.ProjectJobAccounts(default) = %v, want the empty account alone", got)
	}
}

// TestAccountJobRefusedWithoutContainer: projectAccountMount refuses to name a
// container the installation does not create. A job whose secret_env names a
// secret nothing creates plans nothing and then fails at run time, in the
// credential, which is the one place a shared job must not fail.
//
// The configs here never reach ProjectJobs: publish and init derive the
// accounts block through projectpolicy.DeriveAccounts, which refuses an
// account no repository may use before any job is built. This pins the
// backstop itself — the rule that keeps the mount and the container from
// drifting apart — and it names the block the operator has to edit, which
// differs by account: claude_accounts for a named one, project_secrets for the
// default account (whose name is reserved in claude_accounts).
//
// Mutation (run, restore): drop the container check, which mounts a token the
// root creates no container for.
func TestAccountJobRefusedWithoutContainer(t *testing.T) {
	for _, c := range []struct {
		name, lc, account, block string
		// drop is the project-wide entry taken back out of the parsed config
		// after it validated, for the case whose config the local config's own
		// rules refuse to read at all (a profile listing the default account
		// with no project-wide copy of its token): the drift this backstop
		// catches is an entry that existed when the profile was written and is
		// gone by the time the jobs are built, which is a config no reader
		// sees whole.
		drop string
	}{
		{
			"a named account nobody may use",
			`claude_accounts:
  acme-personal: {}
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal] }
`,
			"acme-personal", "claude_accounts.acme-personal.allow_data_to", "",
		},
		{
			"the default account whose project-wide copy is gone",
			`project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [default] }
`,
			"", "project_secrets.claude-oauth-token.allow_data_to", config.ClaudeOAuthToken,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+c.lc)
			if c.drop != "" {
				delete(lc.ProjectSecrets, c.drop)
			}
			err := projectAccountMount(lc, "standard", lc.Profiles["standard"], c.account, map[string]string{}, map[string]string{}, false)
			if err == nil {
				t.Fatalf("the account %q was mounted with no project-wide copy to mount", c.account)
			}
			for _, want := range []string{c.block, config.ClaudeAccountSecret(c.account)} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// anthropicKeyProfileLC is the sentinel's own fixture: a profile that lists
// the project's own Anthropic API key and no Claude account at all, with the
// key's project-wide copy reaching the one repository the config records.
// claude_accounts is not written — that is the installation this task exists
// for (D1422): no Claude account anywhere, and its reviews still have a job.
//
// The profile writes its own allow_repos because it lists an account (D3), and
// the sentinel's published list is derived from exactly that list, kept where
// project_secrets.anthropic-api-key reaches the repositories
// (projectpolicy.anthropicKeySlugs) — so the key's own reach is written here
// too, or DeriveAccounts refuses the empty list it would publish.
const anthropicKeyProfileLC = `project_secrets:
  anthropic-api-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [` + config.AnthropicKeyAccount + `] }
`

// TestReviewJobIsDeployedForAProjectWithNoClaudeAccount is this task's
// installation-shape half (F17, D1417, D1422): an installation that publishes
// the sentinel and no Claude account deploys a job whose account segment is the
// sentinel — mounting no Claude token, because the value names a credential the
// platform already holds (projectAccountMount) — and, beside it, the
// review-only variant the credentials service serves the key from. The
// service's own account set is derived from the same axis
// (credsvc.ReviewJobAccountsFor reads config.ProjectJobAccounts), so the two
// sides are checked against each other here rather than against a second copy
// of the naming rule: the variant the deployer built IS the account the service
// admits.
//
// A profile of a provider the shared-job list does not hold is the second case
// and it is a contrast, not a refusal: the test holds the list back to github
// for it, no shared job is deployed for such a profile, so no variant is
// either, and an installation whose only profile is one of those has no review
// job at all. That is why this test reads both sets off the config rather than
// only the sentinel's coding job. (The release serves bitbucket since I1, so an
// installation that can deploy the credentials service gets that job and its
// variant — TestReviewJobVariantFollowsTheProfiles is the positive half.)
//
// Mutation (run, restore): keep AK4's refusal (return the userErr ProjectJobs
// carried for this profile) — the GitHub case has no jobs to read; or drop the
// sentinel's arm from projectAccountMount — the container lookup prescribes
// claude_accounts.<sentinel>.allow_data_to, a key config.ValidateClaudeAccounts
// refuses.
func TestReviewJobIsDeployedForAProjectWithNoClaudeAccount(t *testing.T) {
	admitSharedJobProviders(t, "github")
	for name, tc := range map[string]struct {
		provider, allowRepos, keyReach string
		serves                         bool
	}{
		"a shared-job profile with no Claude account":               {"github", "github/acme/webapp", "acme/webapp", true},
		"a Bitbucket profile the held-back list deploys no job for": {"bitbucket", "bitbucket/acme/sandbox", "acme/sandbox", false},
	} {
		t.Run(name, func(t *testing.T) {
			lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+`project_secrets:
  anthropic-api-key: { allow_data_to: [`+tc.keyReach+`] }
profiles:
  standard: { provider: `+tc.provider+`, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [`+tc.allowRepos+`], claude_accounts: [`+config.AnthropicKeyAccount+`] }
`)
			coding, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
			if err != nil {
				t.Fatalf("an installation whose only account-axis value is the project's own Anthropic API key was refused: %v", err)
			}
			review, err := ProjectReviewJobs(lc, "fugaro-runs-proj-1234", false)
			if err != nil {
				t.Fatalf("ProjectReviewJobs: %v", err)
			}
			if !tc.serves {
				if len(coding) != 0 || len(review) != 0 {
					t.Fatalf("a %s profile deployed %v and %v: no shared job is deployed for one, so no variant is either", tc.provider, sortedJobNames(coding), sortedJobNames(review))
				}
				return
			}
			// The axis is two values here — the profile's no-account job and the
			// sentinel's — and the variant set is one per coding job.
			wantCoding := []string{
				gcp.ProjectJobName("standard", "github", ""),
				gcp.ProjectJobName("standard", "github", config.AnthropicKeyAccount),
			}
			slices.Sort(wantCoding)
			if !slices.Equal(sortedJobNames(coding), wantCoding) {
				t.Fatalf("coding jobs = %v, want the no-account job and the sentinel's, %v", sortedJobNames(coding), wantCoding)
			}
			wantReview := reviewWantNames()
			slices.Sort(wantReview)
			if !slices.Equal(sortedJobNames(review), wantReview) {
				t.Fatalf("review jobs = %v, want one per coding job, %v", sortedJobNames(review), wantReview)
			}
			token := config.ReservedSecrets[config.ClaudeOAuthToken]
			sentinelJob := coding[gcp.ProjectJobName("standard", "github", config.AnthropicKeyAccount)]
			if sentinelJob.Account != config.AnthropicKeyAccount {
				t.Errorf("the job of the project's own key carries the account segment %q, want %q", sentinelJob.Account, config.AnthropicKeyAccount)
			}
			if _, mounted := sentinelJob.SecretEnv[token]; mounted {
				t.Errorf("the job of the project's own key mounts %s: the sentinel names no Claude account, so there is no token of its own to mount", token)
			}
			if got, want := sentinelJob.ServiceAccount.AccountID, gcp.ProjectServiceAccountID("standard", "github", config.AnthropicKeyAccount); got != want {
				t.Errorf("the job of the project's own key runs as %s, want %s", got, want)
			}
			variant := review[credsvc.ReviewJobName("standard", "github", config.AnthropicKeyAccount)]
			if variant.Account != config.AnthropicKeyAccount {
				t.Errorf("the sentinel's review variant carries the account segment %q, want %q", variant.Account, config.AnthropicKeyAccount)
			}
			if len(variant.SecretEnv) != 0 {
				t.Errorf("the sentinel's review variant mounts %v: a review variant mounts nothing at all (O1)", variant.SecretEnv)
			}
			if _, ok := variant.Env[runner.SecretEnvsVar]; ok {
				t.Errorf("the sentinel's review variant carries %s with no mount to name", runner.SecretEnvsVar)
			}
			if got, want := variant.ServiceAccount.AccountID, credsvc.ReviewServiceAccountID("standard", "github", config.AnthropicKeyAccount); got != want {
				t.Errorf("the sentinel's review variant runs as %s, want %s — the name credsvc.ReviewJobAccountsFor derives the admitted account from", got, want)
			}
			// The service's half, over the same installation: the policy node
			// publish derives from this local config, judged by the one function
			// that turns the axis into admitted accounts.
			accounts, err := projectpolicy.DeriveAccounts(profilesAsPolicy(lc.Profiles), lc.ClaudeAccounts, lc.ProjectSecrets, nil)
			if err != nil {
				t.Fatalf("DeriveAccounts over the test's own config: %v", err)
			}
			p := &projectpolicy.Policy{Version: projectpolicy.Version, Profiles: profilesAsPolicy(lc.Profiles), Accounts: accounts}
			admitted := credsvc.ReviewJobAccountsFor(p, "proj-1234")
			var want []credsvc.ReviewJobAccount
			for _, a := range config.ProjectJobAccounts(lc.Profiles["standard"]) {
				account := infraReviewAccount(a)
				want = append(want, credsvc.ReviewJobAccount{
					Email:   credsvc.ReviewServiceAccountID("standard", "github", account) + "@proj-1234.iam.gserviceaccount.com",
					Account: account,
				})
			}
			if !reflect.DeepEqual(admitted, want) {
				t.Fatalf("ReviewJobAccountsFor = %v, want the variants the deployer built, %v", admitted, want)
			}
			// The sentinel's own entry, said by name: the key is served only where
			// the service admits the caller, and this is the row that admits the
			// sentinel's variant.
			var found bool
			for _, a := range admitted {
				if a.Account == config.AnthropicKeyAccount {
					found = true
				}
			}
			if !found {
				t.Errorf("the service admits no account for the project's own key: %v", admitted)
			}
			if _, ok := accounts[config.AnthropicKeyAccount]; !ok {
				t.Errorf("the derived accounts block has no %s entry: the sentinel's list is what bounds which repositories the key may serve", config.AnthropicKeyAccount)
			}
		})
	}
	// The module's job.account validation admits the sentinel as a literal
	// because HCL cannot import the Go constant (modules/project/variables.tf).
	// Nothing else holds the two spellings together: a Terraform validation a
	// Go constant moved out from under would refuse every plan of an
	// installation whose only account-axis value is the key.
	t.Run("the module admits the sentinel's literal", func(t *testing.T) {
		b, err := fs.ReadFile(terraform.FS, "gcp/modules/project/variables.tf")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `var.job.account == "`+config.AnthropicKeyAccount+`"`) {
			t.Errorf("deploy/terraform/gcp/modules/project/variables.tf does not admit config.AnthropicKeyAccount (%q) as a job.account value: an installation publishing the sentinel would deploy no job", config.AnthropicKeyAccount)
		}
	})
}

// providerKeyProfileLC is the provider key's own fixture (AK6a): a profile that
// lists the account axis's second non-account value and no Claude account at
// all. There is no project_secrets entry for it and there can be none — the
// credential is the credentials service's, held once for the project — so the
// profile's own allow_repos is the whole bound of the list the policy publishes
// for it (projectpolicy's nonAccountSlugs).
const providerKeyProfileLC = `profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [` + config.ProviderKeyAccount + `] }
`

// TestProviderKeyJobMountsNothing is the second non-account value's mount half:
// the job whose account segment is the provider key carries no Claude token,
// and nothing about the value can put one there. The value names a credential
// the credentials service holds for the project (AK6b), never an account of the
// installation's, so there is no token of its own for
// config.ProjectJobTokenAccount to answer with and no container for
// projectAccountMount to mount — the answer both the launcher's gate and the
// runner's re-check are built on (a job holding no token is one an OAuth run
// must not land on). Its review-only variant mounts nothing at all, which is
// what makes the value deployable.
//
// Mutation (run, restore): let the value fall through projectAccountMount's
// lookup — the container lookup answers claude_accounts.provider-key--project
// .allow_data_to, a key config.ValidateClaudeAccounts refuses, so the operator
// is sent to a remedy nobody can carry out and the installation is refused
// where it should deploy; and drop the value's arm in ProjectJobTokenAccount —
// the answer becomes ("", false) only by the profile's list, and a profile that
// did list it as an account would mount a token the job has not got.
func TestProviderKeyJobMountsNothing(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+providerKeyProfileLC)
	pr := lc.Profiles["standard"]
	if got, serves := config.ProjectJobTokenAccount(pr, config.ProviderKeyAccount); serves || got != "" {
		t.Fatalf("ProjectJobTokenAccount(standard, %s) = (%q, %v), want (\"\", false): the value names the credentials service's credential, not an account whose token a job holds", config.ProviderKeyAccount, got, serves)
	}
	secretEnv, env := map[string]string{}, map[string]string{}
	if err := projectAccountMount(lc, "standard", pr, config.ProviderKeyAccount, secretEnv, env, false); err != nil {
		t.Fatalf("the provider key's job was refused a mount: %v — the credential is the service's, so there is no project-wide copy of the value's own to demand", err)
	}
	if len(secretEnv) != 0 || len(env) != 0 {
		t.Fatalf("the provider key's job mounts %v/%v, want nothing: no Claude token reaches a job whose account segment is the value", secretEnv, env)
	}
	coding, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatalf("an installation whose only account-axis value is the provider key was refused: %v", err)
	}
	job := coding[gcp.ProjectJobName("standard", "github", config.ProviderKeyAccount)]
	if job.Account != config.ProviderKeyAccount {
		t.Fatalf("the provider key's job carries the account segment %q, want %q", job.Account, config.ProviderKeyAccount)
	}
	token := config.ReservedSecrets[config.ClaudeOAuthToken]
	if _, mounted := job.SecretEnv[token]; mounted {
		t.Errorf("the provider key's job mounts %s: the value names no account of the installation's, so there is no token of its own to mount", token)
	}
	review, err := ProjectReviewJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatalf("ProjectReviewJobs: %v", err)
	}
	variant := review[credsvc.ReviewJobName("standard", "github", config.ReviewVariantAccount(config.ProviderKeyAccount))]
	if variant.Account != config.ProviderKeyAccount {
		t.Errorf("the provider key's review variant carries the account segment %q, want %q", variant.Account, config.ProviderKeyAccount)
	}
	if len(variant.SecretEnv) != 0 {
		t.Errorf("the provider key's review variant mounts %v: a review variant mounts nothing at all (O1)", variant.SecretEnv)
	}

	// The module's job.account validation admits the value's literal because
	// HCL cannot import the Go constant (modules/project/variables.tf), and
	// nothing else holds the two spellings together.
	t.Run("the module admits the provider key's literal", func(t *testing.T) {
		b, err := fs.ReadFile(terraform.FS, "gcp/modules/project/variables.tf")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `var.job.account == "`+config.ProviderKeyAccount+`"`) {
			t.Errorf("deploy/terraform/gcp/modules/project/variables.tf does not admit config.ProviderKeyAccount (%q) as a job.account value: an installation publishing the value would deploy no job", config.ProviderKeyAccount)
		}
	})
}

// infraReviewAccount is the axis value as the admission speaks it, which is the
// spelling the variant's own names carry: the axis writes the default account as
// "" (config.ProjectJobAccounts) and the policy spells it
// config.DefaultClaudeAccount.
func infraReviewAccount(account string) string {
	return config.ReviewVariantAccount(account)
}

// reviewWantNames is the review variant of every job the sentinel's fixture
// deploys, by the one derivation both sides use.
func reviewWantNames() []string {
	var out []string
	for _, account := range []string{"", config.AnthropicKeyAccount} {
		out = append(out, credsvc.ReviewJobName("standard", "github", infraReviewAccount(account)))
	}
	return out
}

// TestJobNameIndependentOfSecretEnv: a job's name and its service account are
// functions of its (profile, provider, account) triple and of no mount, which
// is what makes the credentials service a clean swap: it removes one
// secret_env entry and nothing is renamed, replaced or deleted (design §4.6).
//
// Mutation (run, restore): fold a secret name into projectStem, which moves
// every job the moment a profile's mounts change.
func TestJobNameIndependentOfSecretEnv(t *testing.T) {
	// The same profile and the same accounts, mounted differently: the second
	// config mounts no github-app-key at all, so the two deploy jobs whose
	// secret_env differs and whose names must not.
	const bare = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal, default] }
`
	one := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	two := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+bare)
	if !slices.Equal(sortedJobNames(one), sortedJobNames(two)) {
		t.Fatalf("the jobs moved with the mounts: %v with github-app-key mounted, %v without", sortedJobNames(one), sortedJobNames(two))
	}
	for name, a := range one {
		b := two[name]
		if a.ServiceAccount != b.ServiceAccount {
			t.Errorf("the job %s runs as %s with github-app-key mounted and as %s without", name, a.ServiceAccount.AccountID, b.ServiceAccount.AccountID)
		}
		if _, mounted := a.SecretEnv[config.ReservedSecrets["github-app-key"]]; !mounted {
			t.Errorf("the fixture no longer mounts github-app-key, so the two jobs' secret_env no longer differ")
		}
		if _, mounted := b.SecretEnv[config.ReservedSecrets["github-app-key"]]; mounted {
			t.Errorf("the second fixture mounts github-app-key too, so this test compares nothing")
		}
		// No secret's name is part of the job's name or its service account:
		// removing the token entry later cannot move either.
		for _, secret := range a.SecretEnv {
			if strings.Contains(name, secret) || strings.Contains(a.ServiceAccount.AccountID, secret) {
				t.Errorf("the job %s (%s) is named after the secret %s it mounts", name, a.ServiceAccount.AccountID, secret)
			}
		}
	}
}

// TestNoSecretValueReadUnderInternal: no code under internal/ reads a secret
// *value* except the places named below, each with what it is. The Claude
// OAuth token reaches a run by the platform's own mount and never through
// Fugaro's deployer or launcher: the deployer names the secret, the module
// mounts it, and neither of those two ever holds the value.
//
// The credentials service's bundle loader is the second exception and it is a
// different kind of thing: the service's whole job is to hold the credential
// and attach it to a call the run makes (C1.11, Slice 2 of the
// credentials-service plan — the service replaces the mount), so it reads ONE
// container's values and it is the only Fugaro process that does. What this
// test keeps is that the read stays there: a read added on the deployer's or
// the launcher's path — the path this package owns — is the failure it is for,
// and `internal/credsvc/bundle.go` is on the list so that a second reader in
// that package has to be added deliberately: `internal/credsvc/dynamicsecrets.go`
// is that second reader, the per-secret containers a person rotated through the
// intake (D1931), read by exact name and never by listing, with a read that
// failed refusing the call rather than serving the bundle's older value (D143).
//
// The third is the same container again, read by the other end of the
// credentials service: `fugaro creds set` is the bundle's only writer, and a
// read-modify-write cannot be written without the read (C1.25 of the same
// plan: it reads the `latest` version, sets one entry, writes a new version,
// and re-reads to catch a concurrent writer). It is owner-run, refused in a
// coding agent's session and in a run's process, and its value never leaves
// the process — but it is a read, so it is on the list by name rather than
// left to be discovered.
//
// The fourth is the one read under internal/ whose value is Fugaro's own
// secret rather than a credential a run is served (mobile plan task P5.1e):
// `internal/mailbox/keychain/cloud.go` reads the installation's pairing-key
// container, because the tag over a paired phone's messages is a symmetric
// HMAC and the consumer that verifies one holds the key the phone signs with
// (the mobile orchestrator design's section 4.2) — the third copy of that key,
// in the installation's own project. It reads the installation's OWN container
// and every version of it, since the uid a pairing's key belongs to is inside
// the version's payload: the whole set is what a pass verifies with. What it
// never does is write a secret's value into a log, an error or a refusal, and
// SECURITY.md, D111 and Global Constraint 4 carry the note that records the
// widening in the same change.
//
// The fifth is a provider's key rather than a credential of this tree's own
// (mobile plan tasks P5.3.3/P5.3.5, the voice design's section 4.4 point 4):
// `internal/voicesvc/provider.go` reads the installation's provider-key
// container, because the voice hop is what presents the key at a third party
// when it forwards a phone's utterance — that is the whole point of the hop
// (the device holds no provider credential, the owner's ruling of 2026-10-19).
// The document is one JSON object of register names to keys, cached for five
// minutes (providerKeyTTL), and the container is the hop's own: not the
// credentials bundle, not a repository's or a job's secret. It is read by the
// ONE Fugaro process behind an endpoint anyone on the internet may call, so
// the file's own rule is stricter than the list's: no value of it reaches a
// log line, an error, a refusal or a response, and what a call is logged as is
// internal/voicesvc's callLog shape — no byte of any header but the two Claude
// Code IDs, and no byte of any body. SECURITY.md's voice section states the
// container, the grant and the blast radius this read creates.
//
// Mutation (run, restore): read the account's token in the deployer or the
// launcher (any `sm.Access(...)` outside the files below) — the scan fails
// with the file that did it. The other spellings a read can take are held by
// TestAPayloadReadInTheIntakesAdapterFailsTheScan.
func TestNoSecretValueReadUnderInternal(t *testing.T) {
	unlisted, stale := secretValueReadFindings(goFilesUnderInternal(t))
	for _, f := range unlisted {
		t.Errorf("internal/%s names a secret-value read (%v), and only these files may: %v. A Claude OAuth token reaches a run by the platform's mount, never through Fugaro", f, secretValueReadNames, slices.Sorted(maps.Keys(secretValueReadAllowed)))
	}
	// And every allowed file is still one, so a removal is noticed too.
	for _, f := range stale {
		t.Errorf("internal/%s no longer reads a secret value, and it is on the allow-list for %s: update the list rather than letting it drift", f, secretValueReadAllowed[f])
	}
}

// secretValueReadAllowed are the files allowed to name a secret-value read, and
// what each one is.
var secretValueReadAllowed = map[string]string{
	"backend/gcp/secrets.go":    "the Secret Manager client: Access is the GitHub App key pre-check's one read, Set writes values",
	"cli/creds.go":              "C1.25's owner-run `fugaro creds set`/`creds ls` over the same ONE bundle container (Q6): set reads the document it is about to rewrite",
	"cli/githubapp.go":          "the GitHub App key pre-check of init and doctor",
	"cli/workspace_probe.go":    "the workspace credential probe (workspace-projects-users WU22, D2820/D2825): `workspace project status --check-credentials` reads a member project's own model key and its project-owned Claude accounts into memory in the OWNER's own terminal, and spends each value on one minimal authenticated call — which is what makes a credential's validity a fact rather than a mark. Its fence is the coding agent's session, refused before anything is read (refuseKeyReadAt) — and that fence alone, not the run-process one (managedFlagRunMarker): a run's own process carries no agent marker, and D2820 keeps the command in the owner's terminal. It reads no personal credential (D2825): a name under the person's own prefix is skipped before any read. No value of it reaches a print, a log, an error or a refusal, and the probe's own answer carries the provider's status and never the value",
	"credsvc/bundle.go":         "the credentials service's ONE bundle container (Q6): the service attaches the credential upstream, and it is the only Fugaro process that ever holds a model credential's value",
	"credsvc/dynamicsecrets.go": "the per-secret containers of the dynamic-delivery grammar (D1931): a credential a person rotated is read ahead of the bundle's entry for it (R6), by exact name and never by listing, and a read that fails refuses the call rather than serving the bundle's older value (D143)",
	"mailbox/keychain/cloud.go": "the installation's own pairing-key container (mobile plan task P5.1e, the mobile orchestrator design's section 4.2): a paired phone's tag is a symmetric HMAC, so the consumer that verifies one holds the key the phone signs with, and the version's own payload carries which device it is for (P5.1e's third copy, revoked by `fugaro pair revoke` destroying the version)",
	"voicesvc/provider.go":      "the installation's provider-key container (mobile plan tasks P5.3.3/P5.3.5, the voice design's section 4.4 point 4): the hop presents a provider's key at the provider when it forwards a phone's utterance, one JSON object of register names to keys cached for five minutes (providerKeyTTL). That container is the hop's own and never the credentials bundle's, a job's or a repository's, and the process behind it is the one endpoint anyone on the internet may call, so no value of it reaches a log, an error, a refusal or a response, and a read that fails refuses the call rather than presenting an empty key (ErrKeyUnavailable)",
}

// secretValueReadNames are the reads, by the API each one is spelled with: the
// google-api client's Versions.Access, the cloud.google.com/go client's
// AccessSecretVersion, and the REST form — a version is read by a `:access` on
// its path, which is how a client that spells the call out itself reads one.
var secretValueReadNames = []string{".Access(", "AccessSecretVersion", ":access\""}

// secretValueWrittenPayload is the ONE naming of the payload type that is not a
// read: AddSecretVersionRequest's own field, which is how every writer in this
// tree spells it (backend/gcp/secrets.go, cli/creds.go, and the intake's own
// adapter). The type was a read token of its own until the intake landed, and
// that is the sensitivity a read inside the intake's adapter needs: the file is
// a writer, deploy/terraform's reader guard holds it only as an exception
// (E22), and this scan is the one guard left that can catch a read added to it.
// So the write form is skipped and every other naming of the type is a read —
// a payload a version's value arrives in is one however the file came by it.
var secretValueWrittenPayload = regexp.MustCompile(`Payload:\s*&secretmanager\.SecretPayload\{`)

// namesASecretValueRead reports whether a file's source names a read of a
// secret's value.
func namesASecretValueRead(src string) bool {
	for _, name := range secretValueReadNames {
		if strings.Contains(src, name) {
			return true
		}
	}
	return strings.Contains(secretValueWrittenPayload.ReplaceAllString(src, ""), "SecretPayload")
}

// goSource is one Go file the scan reads: its path under internal/ and its
// source, so the guard can be run over a file that is not on disk.
type goSource struct {
	rel string
	src string
}

// goFilesUnderInternal is every non-test Go file under internal/, which is the
// scope the guard names.
func goFilesUnderInternal(t *testing.T) []goSource {
	t.Helper()
	const root = ".." // internal/
	var files []goSource
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, goSource{rel: filepath.ToSlash(rel), src: string(b)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// secretValueReadFindings is the guard's whole answer over a set of files: the
// ones that name a read with no exception row, and the rows whose file no
// longer names one.
func secretValueReadFindings(files []goSource) (unlisted, stale []string) {
	found := map[string]bool{}
	for _, f := range files {
		if !namesASecretValueRead(f.src) {
			continue
		}
		found[f.rel] = true
		if _, ok := secretValueReadAllowed[f.rel]; !ok {
			unlisted = append(unlisted, f.rel)
		}
	}
	for f := range secretValueReadAllowed {
		if !found[f] {
			stale = append(stale, f)
		}
	}
	slices.Sort(unlisted)
	slices.Sort(stale)
	return unlisted, stale
}

// TestAPayloadReadInTheIntakesAdapterFailsTheScan holds the reader that no
// other guard can: internal/intake/gcp.go is on deploy/terraform's reader list
// as a writer (E22), so a read added inside it is invisible to that scan, and
// this one has no row for the file to excuse it. Each case is a spelling such a
// read could take, put into the real adapter's source and asked of the real
// guard over the real tree — the write form the adapter actually has is the
// run's own control, since it must stay invisible for the file to need no row
// at all.
func TestAPayloadReadInTheIntakesAdapterFailsTheScan(t *testing.T) {
	const rel = "intake/gcp.go"
	files := goFilesUnderInternal(t)
	if unlisted, stale := secretValueReadFindings(files); len(unlisted) != 0 || len(stale) != 0 {
		t.Fatalf("the tree already fails this guard (unlisted %v, stale %v), so a mutation here would prove nothing", unlisted, stale)
	}
	var real string
	for _, f := range files {
		if f.rel == rel {
			real = f.src
		}
	}
	if real == "" {
		t.Fatalf("%s is not a file this guard reads, so this test is not about the guard's blind spot", rel)
	}
	for _, tc := range []struct{ what, read string }{
		{"a version read decoded into the payload type", `
var payload secretmanager.SecretPayload
if err := json.Unmarshal(body, &payload); err != nil {
	return "", err
}
return string(payload.Data), nil
`},
		{"a version read over REST", `
resp, err := http.Get(s.svc.BasePath + "/secrets/" + id + "/versions/latest:access")
if err != nil {
	return "", err
}
defer resp.Body.Close()
`},
		{"a version read through the cloud client", `
got, err := c.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: id})
if err != nil {
	return "", err
}
return string(got.Payload.Data), nil
`},
	} {
		mutated := slices.Clone(files)
		for i := range mutated {
			if mutated[i].rel == rel {
				mutated[i].src = real + "\nfunc mutated(id string) (string, error) {\n" + tc.read + "}\n"
			}
		}
		unlisted, stale := secretValueReadFindings(mutated)
		if len(unlisted) != 1 || unlisted[0] != rel || len(stale) != 0 {
			t.Errorf("%s in internal/%s: the guard reports unlisted %v and stale %v, want just %s", tc.what, rel, unlisted, stale, rel)
		}
	}
}

// Retiring a project job is deferred (plan task 3.2's fix round, review item
// 4): the module protects the job from deletion and has no allow_job_delete,
// unlike the workflow module. A profile removed, or one whose secrets change
// its account and so its job's name, plans the old module instance's delete;
// the guard refuses it without --allow-delete, and with it the apply would
// destroy the launchers' grants on the job and then fail on the deletion
// protection. So the delete of a project job is refused before the apply
// whatever --allow-delete names, with the way back and the reason.
//
// The review-only variant of a project job is refused with it (the owner's
// ruling of 2026-11-04): it is the same module instance over the same axis
// (CSR7), protected from deletion the same way, and a profile that disappears
// takes the coding job and its variant in one plan — so a variant the guard
// did not name would be the one project job an apply could still destroy.
//
// Mutation (run, restore): drop the `(?:_review)?` from projectJobAddress, or
// the "module.project_review[" prefix from installation.go's hints — the
// variant's branches below fail (the refusal does not fire, or the delete
// comes back with no hint at all).
func TestProjectJobDeletesAreRefusedUntilRetirementExists(t *testing.T) {
	coding := `module.project["` + gcp.ProjectJobName("standard", "github", "") + `"].google_cloud_run_v2_job.this`
	variant := `module.project_review["` + credsvc.ReviewJobName("standard", "github", config.DefaultClaudeAccount) + `"].google_cloud_run_v2_job.this`
	for _, job := range []string{coding, variant} {
		for _, acts := range [][]string{{"delete"}, {"delete", "create"}, {"create", "delete"}} {
			p := &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: job, Change: tf.Change{Actions: acts}}}}
			err := RefuseProjectJobDeletes(p)
			if err == nil {
				t.Fatalf("%v of %s is not refused", acts, job)
			}
			for _, want := range []string{job, "retiring a project job is not supported yet", "deletion protection", "restore the profile"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v: err = %v, want it to name %q", acts, err, want)
				}
			}
		}
		if hints := DeleteHints(planDeleting(job), nil); len(hints) != 1 || !strings.Contains(hints[0], "profiles") {
			t.Errorf("deleting %s: hints = %v, want one naming the local config's profiles", job, hints)
		}
	}
	// Anything else the module holds may still be replaced with
	// --allow-delete (a grant whose condition changed, say), and a plan that
	// deletes no project job is untouched.
	for _, addr := range []string{
		`module.project["` + gcp.ProjectJobName("standard", "github", "") + `"].google_storage_bucket_iam_member.job`,
		`module.project_review["` + credsvc.ReviewJobName("standard", "github", config.DefaultClaudeAccount) + `"].google_storage_bucket_iam_member.job`,
		"module.installation.google_cloud_run_v2_job.history[0]",
	} {
		if err := RefuseProjectJobDeletes(planDeleting(addr)); err != nil {
			t.Errorf("deleting %s: %v", addr, err)
		}
	}
}

// TestProjectAccountMountIsTheOneSeam: the Claude token reaches a shared job
// through projectAccountMount and nowhere else in the deployer (design §4.6:
// the security service replaces that one function). No other function of
// projectjobs.go may name an account's secret or the token's variable, and the
// function that composes the jobs must call it: a mount written anywhere else is
// a second carrier the swap would leave behind.
//
// The composition is projectJobs, which is where the jobs are built and what
// the exported ProjectJobs delegates to (it is the form that carries a
// profile's job, init_policy_migrate.go's installSpec): the seam is the
// composition, not the name of the wrapper around it. The call need not be
// direct — B5.7's fill moved it one function along, into projectJobMounts,
// which is the one place the mounts and the drop are decided together — so what
// is asserted is that a path of local calls reaches projectAccountMount from
// projectJobs, which is the same claim the direct call made: no other carrier
// exists on the way the jobs are composed.
//
// Mutation (run, restore): inline the mount in projectJobs' loop (the same
// secret_env, by another path) — every behavioural test still passes, and this
// one fails naming the path.
func TestProjectAccountMountIsTheOneSeam(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "projectjobs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "config" &&
					(x.Sel.Name == "ClaudeAccountSecret" || x.Sel.Name == "ClaudeOAuthToken") && fn.Name.Name != "projectAccountMount" {
					t.Errorf("%s: %s names config.%s: the Claude token's mount belongs to projectAccountMount alone", fset.Position(x.Pos()), fn.Name.Name, x.Sel.Name)
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name != fn.Name.Name {
					calls[fn.Name.Name] = append(calls[fn.Name.Name], id.Name)
				}
			}
			return true
		})
	}
	seen := map[string]bool{"projectJobs": true}
	for frontier := []string{"projectJobs"}; len(frontier) > 0; {
		var next []string
		for _, fn := range frontier {
			for _, callee := range calls[fn] {
				if seen[callee] {
					continue
				}
				seen[callee] = true
				next = append(next, callee)
			}
		}
		frontier = next
	}
	if !seen["projectAccountMount"] {
		t.Error("no path of calls from projectJobs reaches projectAccountMount: the account jobs' token reaches them by some other path")
	}
}

// TestProjectAccountMountRefusesAnOccupiedVariable: the mount never
// overwrites a CLAUDE_CODE_OAUTH_TOKEN entry already in the job's secret_env.
// The profile rules refuse a Claude token among a profile's secrets, so this
// cannot be reached through ProjectJobs today; a silent overwrite would still
// replace one credential with another without a word if that rule ever moved.
//
// Mutation (run, restore): drop the occupied check — the mount replaces the
// planted entry and this fails.
func TestProjectAccountMountRefusesAnOccupiedVariable(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	v := config.ReservedSecrets[config.ClaudeOAuthToken]
	secretEnv := map[string]string{v: "github-app-key"}
	err := projectAccountMount(lc, "standard", lc.Profiles["standard"], "acme-personal", secretEnv, map[string]string{}, false)
	if err == nil {
		t.Fatalf("the mount replaced an existing %s entry: secret_env = %v", v, secretEnv)
	}
	if secretEnv[v] != "github-app-key" {
		t.Errorf("a refused mount still wrote secret_env: %v", secretEnv)
	}
}

// TestAddProjectJobRefusesACollision: two (profile, provider, account) triples
// that derive the same job name or the same service account ID are refused,
// never one silently replacing the other in the job set. Each derivation
// carries a short hash (12 hex for a job, 8 for an account), so a collision is
// improbable, not impossible; a replaced entry would deploy one account's job
// under another's name, or two jobs as one account that reads both tokens.
//
// Mutation (run, restore): drop either check in addProjectJob — the job set
// keeps the second entry and this fails.
func TestAddProjectJobRefusesACollision(t *testing.T) {
	a := ProjectJob{Profile: "p", Account: "", Job: "fugaro-p-x", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-1"}}
	sameJob := ProjectJob{Profile: "p", Account: "acme-work", Job: "fugaro-p-x", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-2"}}
	sameSA := ProjectJob{Profile: "p", Account: "acme-home", Job: "fugaro-p-y", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-1"}}
	for _, b := range []ProjectJob{sameJob, sameSA} {
		out := map[string]ProjectJob{}
		if err := addProjectJob(out, a); err != nil {
			t.Fatal(err)
		}
		if err := addProjectJob(out, b); err == nil {
			t.Errorf("the account %q's job (%s, %s) was added beside %s (%s): a collision must be refused", b.Account, b.Job, b.ServiceAccount.AccountID, a.Job, a.ServiceAccount.AccountID)
		}
		if out[a.Job].Account != a.Account || len(out) != 1 {
			t.Errorf("the job set moved on a refused add: %v", out)
		}
	}
}

// projectReviewJobsFor is ProjectReviewJobs over a local config, for the tests
// of the review-only variant (CSR7).
func projectReviewJobsFor(t *testing.T, lc string) map[string]ProjectJob {
	t.Helper()
	c := parseLC(t, lc)
	jobs, err := ProjectReviewJobs(c, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

// reviewAccount is the account segment the review variant's own names carry:
// the policy's spelling of the account, which the axis writes as the empty
// string for the default account (config.ProjectJobTokenAccount's mapping, the
// one credsvc.ReviewJobAccountsFor makes too).
func reviewAccount(account string) string {
	if account == "" {
		return config.DefaultClaudeAccount
	}
	return account
}

// TestReviewJobVariantMountsNoAppKey is CSR7's red step: the review-only
// variant of a project job mounts no github-app-key, while the coding job of
// the same (profile, provider, account) triple keeps the key it has always
// mounted. The variant is the container that runs a test from the head under
// review (design docs/cloud-senior-review D885), so an App key in its
// environment is a credential the code under review can read.
//
// Mutation (run, restore): add github-app-key to the variant's secret_env —
// this test fails, and so does TestReviewJobVariantMountsNoSecretAtAll.
func TestReviewJobVariantMountsNoAppKey(t *testing.T) {
	coding := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	review := projectReviewJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	if len(review) == 0 {
		t.Fatalf("no review variant was built for an installation whose profiles serve accounts: there is no variant, and the only job the axis knows mounts the key")
	}
	var mounted int
	for _, j := range coding {
		if j.SecretEnv[config.ReservedSecrets[config.GitHubAppKeySecret]] == config.GitHubAppKeySecret {
			mounted++
		}
	}
	if mounted != len(coding) {
		t.Fatalf("%d of %d coding jobs mount the App key: this test's contrast is gone (the golden and the Terraform test hold that mount)", mounted, len(coding))
	}
	for name, j := range review {
		for env, secret := range j.SecretEnv {
			if secret == config.GitHubAppKeySecret {
				t.Errorf("the review variant %s mounts the App key as %s: the variant runs the head under review, so the App's private key must not be in its environment", name, env)
			}
		}
	}
}

// TestReviewJobVariantServiceAccountHasNoAppKeyAccessor is the grant half of
// the same property: the variant's service account holds no secretAccessor on
// the App key's container. modules/project/sa.tf grants one member per value of
// a job's secret_env (google_secret_manager_secret_iam_member.job iterates it),
// so the account's grant list is the variant's own secret_env — empty — and
// never the coding job's, whose account is a different service account
// entirely.
//
// Mutation (run, restore): mount the key on the variant (the entry that makes
// sa.tf grant the accessor) — this test fails.
func TestReviewJobVariantServiceAccountHasNoAppKeyAccessor(t *testing.T) {
	coding := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	review := projectReviewJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	if len(review) != len(coding) {
		t.Fatalf("the review variants are %d and the coding jobs %d: the variant is one job per coding job", len(review), len(coding))
	}
	codingSA := map[string]string{}
	for _, j := range coding {
		codingSA[j.ServiceAccount.AccountID] = j.Job
	}
	for name, j := range review {
		// The grant list: what sa.tf's for_each would iterate.
		for _, secret := range j.SecretEnv {
			if secret == config.GitHubAppKeySecret {
				t.Errorf("the review variant %s names %s in its grant list: its service account %s would hold secretAccessor on the App key", name, secret, j.ServiceAccount.AccountID)
			}
		}
		if prev, shared := codingSA[j.ServiceAccount.AccountID]; shared {
			t.Errorf("the review variant %s runs as %s, which is the coding job %s's account: the variant's grant list would then be that job's", name, j.ServiceAccount.AccountID, prev)
		}
	}
}

// TestReviewJobVariantMountsNoSecretAtAll is O1's property (the owner's ruling
// of 2026-11-01) and D892(a)'s: the variant mounts nothing at all — no App key,
// no review-signing key and no CLAUDE_CODE_OAUTH_TOKEN either — so a process
// that reads /proc in the container finds nothing of the person's, and the
// read-only installation token is fetched from the credentials service rather
// than handed in (D893(c)).
//
// The mount is also the whole of the grant (sa.tf iterates it) and the whole of
// the redaction list (runner.SecretEnvsVar, computed from the job's own
// mounts), so an empty secret_env is the variant's environment carrying no
// secret-shaped variable at all.
//
// Mutation (run, restore): add the signing key or the account's Claude token
// back to the variant's secret_env (the two shapes earlier drafts of the
// design had) — this test fails on whichever was added.
func TestReviewJobVariantMountsNoSecretAtAll(t *testing.T) {
	review := projectReviewJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	if len(review) == 0 {
		t.Fatal("no review variant was built, so this pins nothing")
	}
	for name, j := range review {
		if len(j.SecretEnv) != 0 {
			t.Errorf("the review variant %s mounts %v: under O1 it mounts no secret at all, and its account's grant list is its mounts", name, j.SecretEnv)
		}
		if got, ok := j.Env[runner.SecretEnvsVar]; ok {
			t.Errorf("the review variant %s carries %s = %q: the redaction list names the variables its secrets are mounted as, and it mounts none", name, runner.SecretEnvsVar, got)
		}
		for _, v := range slices.Collect(maps.Values(config.ReservedSecrets)) {
			if _, ok := j.Env[v]; ok {
				t.Errorf("the review variant %s carries %s, a reserved secret's variable, as plain environment: the variant mounts nothing", name, v)
			}
		}
		if _, ok := j.Env[config.ClaudeAccountSecret(config.DefaultClaudeAccount)]; ok {
			t.Errorf("the review variant %s carries a Claude token's variable in its environment", name)
		}
		if _, ok := j.Env[config.ClaudeAccountSecret("acme-personal")]; ok {
			t.Errorf("the review variant %s carries a named account's token variable in its environment", name)
		}
	}
}

// TestReviewJobVariantCarriesNoVertexGrant: an installation built on Vertex
// grants its coding jobs' accounts roles/aiplatform.user, and the review
// variant of those same jobs carries no such grant (the owner's ruling of
// 2026-11-04). The design's grant list for a review variant is its two bucket
// conditions and its own account and nothing else (spec §2, plan CSR7), and
// under O1 the reviewer's model calls are served by the credentials service,
// so an aiplatform member on this account is a reach no task of the design
// names. modules/project/sa.tf's one vertex member is what the flag drives, so
// the entry is the whole of the grant.
//
// The two Vertex names stay in the variant's plain environment: they are not a
// grant, and an agent on `agent.auth: vertex` refuses to start without
// CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID (internal/agent/env.go) — a
// variant that cannot start is not a narrower variant. What it does not hold is
// the grant that would let a Vertex call leave the container.
//
// Mutation (run, restore): give the variant its coding job's flag (Vertex:
// j.Vertex) — every variant below fails.
func TestReviewJobVariantCarriesNoVertexGrant(t *testing.T) {
	const lc = m4LocalConfig + projectJobRepoLC + m5Additions + projectAccountAxisLC
	c := parseLC(t, lc)
	coding, err := ProjectJobs(c, "fugaro-runs-proj-1234", true)
	if err != nil {
		t.Fatal(err)
	}
	review, err := ProjectReviewJobs(c, "fugaro-runs-proj-1234", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(coding) == 0 || len(review) != len(coding) {
		t.Fatalf("the fixture built %d coding jobs and %d review variants: this test's contrast is gone", len(coding), len(review))
	}
	for name, j := range coding {
		if !j.Vertex {
			t.Fatalf("the coding job %s is not on Vertex although the installation is: this test's contrast is gone", name)
		}
	}
	for name, v := range review {
		if v.Vertex {
			t.Errorf("the review variant %s carries its coding job's Vertex flag: the module would grant the account roles/aiplatform.user, which the design's grant list does not name", name)
		}
		if v.Env[VertexRegionEnv] != c.Region || v.Env[VertexProjectEnv] != c.GCPProject {
			t.Errorf("the review variant %s carries %s = %q and %s = %q, want the installation's own region and project: the names are what an agent on agent.auth: vertex needs to start, and they are not the grant", name, VertexRegionEnv, v.Env[VertexRegionEnv], VertexProjectEnv, v.Env[VertexProjectEnv])
		}
	}
}

// TestSharedProjectJobKeepsItsAppKey is this task's other half: adding the
// variant does not narrow an existing job. Every coding job of the axis still
// mounts the App key exactly as it did, under the same service account and with
// the same conditions, so an installation that converges deploys a second job
// beside its jobs and moves none of them.
//
// Mutation (run, restore): drop the App key from the shared job (or build the
// variant in place of it instead of beside it) — this test and the Terraform
// test used by CSR7 both fail.
func TestSharedProjectJobKeepsItsAppKey(t *testing.T) {
	coding := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectAccountAxisLC)
	axis := []string{"", "acme-personal"}
	if len(coding) != len(axis) {
		t.Fatalf("the fixture deployed %v, want one coding job per account of %v", sortedJobNames(coding), axis)
	}
	for _, account := range axis {
		saID := gcp.ProjectServiceAccountID("standard", "github", account)
		name := gcp.ProjectJobName("standard", "github", account)
		j, ok := coding[name]
		if !ok {
			t.Fatalf("the fixture deployed %v, want %s among them", sortedJobNames(coding), name)
		}
		if got := j.SecretEnv[config.ReservedSecrets[config.GitHubAppKeySecret]]; got != config.GitHubAppKeySecret {
			t.Errorf("the shared job %s mounts github-app-key = %q, want %q: this task adds a variant, it does not narrow an existing job", name, got, config.GitHubAppKeySecret)
		}
		if j.ServiceAccount.AccountID != saID {
			t.Errorf("the shared job %s runs as %s, want %s", name, j.ServiceAccount.AccountID, saID)
		}
		if want := gcp.BucketConditionTitle(saID); j.BucketCondition.Title != want {
			t.Errorf("the shared job %s has the bucket condition title %q, want %q", name, j.BucketCondition.Title, want)
		}
		if want := gcp.ProjectTokenBucketConditionTitle(saID); j.TokenCondition.Title != want {
			t.Errorf("the shared job %s has the token condition title %q, want %q", name, j.TokenCondition.Title, want)
		}
	}
}

// TestProjectJobAccountsKnowsTheVariant: the review variant is derived from the
// account axis the launcher, the deployer and the runner all compute the job
// set with (config.ProjectJobAccounts), and never added to it. Every account
// the axis names for a profile gets one variant, named the way
// credsvc.ReviewJobName spells it over the policy's own account name — which is
// what makes the launcher's lookup, the deployer's job set and the runner's
// recomputation agree on one name — and the coding job of the same triple stays
// where it was.
//
// Mutation (run, restore): spell the variant's account segment from the axis's
// own value (the empty string for the default account) or add the variants to
// config.ProjectJobAccounts — the first stops matching credsvc's names (and so
// the service admits an account nobody deployed), the second gives the coding
// jobs a second entry and every consumer that walks the axis a doubled set.
func TestProjectJobAccountsKnowsTheVariant(t *testing.T) {
	const lc = m4LocalConfig + projectJobRepoLC + m5Additions + projectAccountAxisLC
	coding := projectJobsFor(t, lc)
	review := projectReviewJobsFor(t, lc)
	if len(review) != len(coding) {
		t.Fatalf("the review variants are %v and the coding jobs %v: one variant per coding job", sortedJobNames(review), sortedJobNames(coding))
	}
	seen := map[string]string{}
	for name, j := range coding {
		wantName := credsvc.ReviewJobName(j.Profile, j.Provider, reviewAccount(j.Account))
		wantSA := credsvc.ReviewServiceAccountID(j.Profile, j.Provider, reviewAccount(j.Account))
		v, ok := review[wantName]
		if !ok {
			t.Errorf("the coding job %s has no review variant: the review set is %v, want %s among them (credsvc.ReviewJobName over the same triple)", name, sortedJobNames(review), wantName)
			continue
		}
		if v.Job != wantName || v.Profile != j.Profile || v.Provider != j.Provider || reviewAccount(v.Account) != reviewAccount(j.Account) {
			t.Errorf("the review variant %s = %+v, want the coding job %s's own triple under the review suffix", wantName, v, name)
		}
		if v.ServiceAccount.AccountID != wantSA {
			t.Errorf("the review variant %s runs as %s, want %s (credsvc.ReviewServiceAccountID)", wantName, v.ServiceAccount.AccountID, wantSA)
		}
		if prev, dup := seen[wantName]; dup {
			t.Errorf("the review variants of %s and %s are one job: %s", prev, name, wantName)
		}
		seen[wantName] = name
	}
}

// projectReviewSuffixAccountLC is projectAccountAxisLC's shape with one account
// whose own name ends in the review suffix, which is what makes a coding job's
// name and service account the review variant of another account's job.
const projectReviewSuffixAccountLC = `claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
  acme-personal-review: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/webapp], claude_accounts: [acme-personal, acme-personal-review], secrets: [github-app-key] }
`

// TestReviewJobVariantRefusesAnAccountCarryingTheSuffix: the variant's names
// are derived over the same axis as the coding set, so an account named after
// another account's review variant derives the same job name AND the same
// service account as that variant — and the variant would then run as the
// account of a coding job that mounts the App key, which is the one thing the
// review job's own account must never be (O1). The two sets are checked
// against each other and the deployment stops rather than deploying it: a
// coding job and its review variant may not share an account, and this is the
// shape in which a name can make them.
//
// The check is on the deployment, not on the variant alone: it is the pair
// that collides, and a profile with one of the two names is a perfectly good
// installation — an account called acme-personal-review deploys its own job
// and no variant of acme-personal exists to collide with it.
//
// Two lines refuse the name, and this test takes both: the local config's own
// rule (config.ValidateClaudeAccounts, which localcfg.Parse runs) and the
// published policy's, which is what the deployer builds the job sets from — so
// `fugaro update` stops on the name before it plans a variant, and a config
// that never met the loader's rule stops here.
//
// ProjectReviewJobs' cross-set check is the third and the last, and the name
// rules now stand in front of it: it is kept because it is the only place the
// two sets are compared, and it fails closed if a config ever reaches the
// deployer by another road.
//
// Mutation (run, restore): drop the name rule from
// config.ValidClaudeAccountRef — the policy admits the name, ProjectReviewJobs
// builds the variant of acme-personal onto acme-personal-review's own job name
// and service account, and this test, which expects the refusal, fails.
func TestReviewJobVariantRefusesAnAccountCarryingTheSuffix(t *testing.T) {
	const lc = m4LocalConfig + projectJobRepoLC + m5Additions + projectReviewSuffixAccountLC
	c, err := localcfg.Parse([]byte(lc))
	if err == nil || !strings.Contains(err.Error(), config.ReviewVariantSuffix) {
		t.Fatalf("the local config naming acme-personal-review parsed: err = %v, want the account name rule's refusal", err)
	}
	if _, err := ProjectReviewJobs(c, "fugaro-runs-proj-1234", false); err == nil {
		t.Fatal("the review variants were built although acme-personal-review holds the job name and the service account of acme-personal's variant")
	} else if !strings.Contains(err.Error(), config.ReviewVariantSuffix) {
		// The policy names the rule and the field, never the account it read
		// (the no-echo rule), so the suffix is what the refusal is asked for.
		t.Errorf("the refusal does not name the suffix the account carries: %v", err)
	}
	if _, err := ProjectJobs(c, "fugaro-runs-proj-1234", false); err == nil {
		t.Error("the coding jobs were built although the policy refuses the account name acme-personal-review")
	}
}

// TestReviewJobVariantFollowsTheProfiles: an installation whose profiles a
// shared job cannot serve gets no variant, and one that has not opted into the
// credentials service gets none at all — not even the implicit default job,
// whose whole purpose a project without the service has nothing to serve
// (TestImplicitReviewJobIsDeployedWithoutProfiles). The review job is a project
// job with a suffix, so it follows the same predicate the coding set follows
// (infra.SharedJobProvider): a variant for a profile the list does not hold
// would be a job every consumer of the axis looks for and no deployment
// creates, and a profile the list serves has one of its own — Bitbucket's since
// I1, which is the positive half below.
func TestReviewJobVariantFollowsTheProfiles(t *testing.T) {
	if got := projectReviewJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions); got != nil {
		t.Errorf("an installation with no profiles and no credentials service built the review variants %v, want none", sortedJobNames(got))
	}
	bitbucket := `profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`
	served := m4LocalConfig + projectJobRepoLC + m5Additions + credentialsServiceLCBlock + bitbucket
	want := credsvc.ReviewJobName("standard", "bitbucket", infraReviewAccount(""))
	if got := sortedJobNames(projectReviewJobsFor(t, served)); !slices.Contains(got, want) {
		t.Errorf("a Bitbucket installation with the credentials service on built the review variants %v, want the profile's own %s beside the installation's implicit job: the list serves bitbucket since I1, and the variants follow the coding set",
			got, want)
	}
	admitSharedJobProviders(t, "github")
	if got := projectReviewJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+bitbucket); len(got) != 0 {
		t.Errorf("a Bitbucket profile built the review variants %v, want none: no shared job serves a provider the list does not hold", sortedJobNames(got))
	}
}

// credentialsServiceLCBlock is the local config block that records the
// installation's credentials service: the block's PRESENCE is the opt-in
// (localcfg.CredentialsService), and its url is both the address a review-only
// job fetches its installation token and its model calls from and the audience
// its callers' ID tokens are minted for.
const credentialsServiceLCBlock = "credentials_service: { url: https://fugaro-credentials-123456789012.us-east5.run.app }\n"

// implicitReviewBaseLC is the installation the tests below are about, with the
// credentials service NOT yet on it: a github project that declares no profile
// at all, carrying the Firebase backend the credentials service's own two facts
// come from (credentialsBudgetLC), so that one fixture answers both halves of
// the claim — whether the job is deployed, and whether the row naming it
// reaches the service.
func implicitReviewBaseLC() string {
	return m4LocalConfig + projectJobRepoLC + m5Additions + credentialsBudgetLC
}

// implicitReviewLC is implicitReviewBaseLC with the credentials service on:
// the owner's requirement of 2026-11-13's project — github, the service on, and
// no profiles block at all, which is the state `fugaro review --pr N` had
// nothing to run on in.
func implicitReviewLC() string { return implicitReviewBaseLC() + credentialsServiceLCBlock }

// implicitReviewRow is the row of the credentials service's review table the
// implicit default review job occupies, as this deployment writes it: the
// (profile, provider, account) triple the service admits a review caller by.
// It is named once here because piece 2 of C1 derives the set
// credsvc.ReviewJobAccountsFor admits from the same three values — the row is
// named on both sides rather than spelled twice, which is what makes the two
// equal by construction and what its own test (internal/credsvc) reads this for.
func implicitReviewRow() CredentialsReviewJob {
	return implicitReviewRowOf(githubProvider)
}

// implicitReviewRowOf is that row for one provider, which is the axis the set
// of them varies on (ImplicitReviewProviders).
func implicitReviewRowOf(provider string) CredentialsReviewJob {
	return CredentialsReviewJob{Profile: ImplicitReviewProfile, Provider: provider, Account: config.DefaultClaudeAccount}
}

// implicitReviewRows is one row per implicit default review job an installation
// deploys, in ImplicitReviewProviders' order: the whole review table of an
// installation that declares no profile.
func implicitReviewRows() []CredentialsReviewJob {
	out := make([]CredentialsReviewJob, 0, len(ImplicitReviewProviders))
	for _, provider := range ImplicitReviewProviders {
		out = append(out, implicitReviewRowOf(provider))
	}
	return out
}

// TestImplicitReviewJobIsDeployedWithoutProfiles is the deployment half of the
// owner's requirement of 2026-11-13: `fugaro review --pr N` must run on a
// project with no profile to declare and no policy to publish first. A review
// runs on a review-only variant and never on a shared coding job, and a project
// with no profiles has no coding job to derive a variant from — so the variant
// has to be a job of the project itself: ONE review-only job for the default
// account on each provider the installation deploys one for
// (ImplicitReviewProviders), beside no coding job at all.
//
// Both providers are here for the same reason and one of them is a finding of
// plan task RJ8's (F6): no shared job ever serves Bitbucket (design §4.5, D5 as
// amended), so a Bitbucket repository's review has no variant to resolve
// without this job, and a Bitbucket repository with no profile is the case that
// has nothing else to reach. The config below records one repository of each
// provider (m4LocalConfig's acme/sandbox is the Bitbucket entry), so one
// fixture answers for both.
//
// What this test asserts is the deployment: each job, its own service account
// and the row the credentials service admits a review caller by. The half that
// resolves a review onto one follows (C1's piece 3 for GitHub, plan task RJ8
// for Bitbucket): internal/cli's reviewJobFor resolves the implicit job of the
// repository's own provider, and refuses when the config composes none.
//
// Mutations (each run and restored): drop the credentials-service gate and an
// installation that has not opted in deploys a review job whose every call
// fails; give it a mount (or the coding job's redaction list) and an
// installation with no profile mounts a secret in the container that runs the
// code under review; drop the provider's own gate (recordedGitHubAppID,
// recordedBitbucketRepo) and this fixture's providers are deployed for
// installations that carry neither the App nor a repository of them.
func TestImplicitReviewJobIsDeployedWithoutProfiles(t *testing.T) {
	if got := projectJobsFor(t, implicitReviewLC()); got != nil {
		t.Fatalf("a project with no profiles deployed the coding jobs %v, want none: the implicit job is what its repositories are reviewed on, and a coding job here would be a second answer", sortedJobNames(got))
	}
	if got := projectReviewJobsFor(t, implicitReviewBaseLC()); got != nil {
		t.Errorf("a project with no credentials service built the review jobs %v, want none", sortedJobNames(got))
	}
	lc := parseLC(t, implicitReviewLC())
	jobs, err := ProjectReviewJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != len(ImplicitReviewProviders) {
		t.Fatalf("review jobs = %v, want one implicit default job per provider %v", sortedJobNames(jobs), ImplicitReviewProviders)
	}
	w := defaultWorkflow(t)
	for _, row := range implicitReviewRows() {
		want := credsvc.ReviewJobName(row.Profile, row.Provider, row.Account)
		j, ok := jobs[want]
		if !ok {
			t.Errorf("the review set %v has no job named %s", sortedJobNames(jobs), want)
			continue
		}
		if j.Profile != row.Profile || j.Provider != row.Provider || j.Account != row.Account {
			t.Errorf("the implicit review job is (%s, %s, account %q), want %v: the credentials service admits a review caller by that triple", j.Profile, j.Provider, j.Account, row)
		}
		if got, want := j.ServiceAccount.AccountID, credsvc.ReviewServiceAccountID(row.Profile, row.Provider, row.Account); got != want {
			t.Errorf("the %s implicit review job runs as %s, want %s: the variant's own service account is what makes it a job that mounts nothing", row.Provider, got, want)
		}
		// It mounts nothing at all, and it names no redaction list either: the
		// environment it runs the code under review in carries the credentials
		// service's address and no credential of the installation's.
		if len(j.SecretEnv) != 0 {
			t.Errorf("the %s implicit review job mounts %v, want no secret: a variant runs untrusted code as the job's service account, so every credential in its environment is one that code can read", row.Provider, j.SecretEnv)
		}
		if _, ok := j.Env[runner.SecretEnvsVar]; ok {
			t.Errorf("the %s implicit review job carries %s, want no redaction list: there are no mounts for it to name", row.Provider, runner.SecretEnvsVar)
		}
		if got, want := j.Env[providers.EnvCredentialsServiceURL], lc.CredentialsService.URL; got != want {
			t.Errorf("the %s implicit review job carries %s = %q, want %q: the service is where its read-only token and its model calls come from", row.Provider, providers.EnvCredentialsServiceURL, got, want)
		}
		// The App ID belongs to the github job and to no other: the module
		// refuses a github job without it (its own precondition), and a
		// Bitbucket job that carried one would hand a container that clones
		// through the proxy a key it has no use for.
		appID := j.Env[providers.EnvGitHubAppID]
		if row.Provider == githubProvider && appID != "123456789" {
			t.Errorf("the implicit review job carries %s = %q, want the App ID the config records: its provider refuses to start without it", providers.EnvGitHubAppID, appID)
		}
		if row.Provider != githubProvider && appID != "" {
			t.Errorf("the %s implicit review job carries %s = %q, want none: only the github job's token is issued for an App", row.Provider, providers.EnvGitHubAppID, appID)
		}
		// The runner's own image, and the resources a repository workflow that
		// names none is given: the job serves a profile no config declares, so
		// nothing states a profile's cpu, memory or timeout_class for it.
		if got, want := j.Image, lc.BaseImage(config.BaseKind); got != want {
			t.Errorf("the %s implicit review job runs the image %s, want the installation's base image %s", row.Provider, got, want)
		}
		if got, want := j.CPU, strconv.Itoa(w.Resources.CPU); got != want {
			t.Errorf("the %s implicit review job's cpu is %s, want the default workflow's %s", row.Provider, got, want)
		}
		if got, want := j.Memory, w.Resources.Memory; got != want {
			t.Errorf("the %s implicit review job's memory is %s, want the default workflow's %s", row.Provider, got, want)
		}
		// Its task timeout is the default workflow's total plus the backend's
		// slack, which is how a per-repository job's is computed (spec.go's
		// TaskTimeoutS): a deployer that carried its own copy of the number would
		// put this job on a clock the default repository job no longer runs on.
		if got, want := j.TaskTimeoutS, int((w.Timeouts.Total.Duration+backend.TaskTimeoutSlack)/time.Second); got != want {
			t.Errorf("the %s implicit review job's task timeout is %ds, want the default workflow's total plus the slack, %ds", row.Provider, got, want)
		}
		if j.Vertex {
			t.Errorf("the %s implicit review job carries a Vertex grant: under O1 the reviewer's model calls go through the credentials service, and no task of the design names roles/aiplatform.user for it", row.Provider)
		}
		// Its name is the project stem's, which is what keeps it from ever being
		// a per-repository job's: the jobs beside it are fugaro-<slug>-<workflow>
		// (gcp.JobName), one per repository, and no repository's job can hold a
		// service account a review runs on.
		for _, repo := range []string{"acme/webapp", "acme/sandbox"} {
			slug, err := task.Slug(row.Provider, repo)
			if err != nil {
				t.Fatal(err)
			}
			if j.Job == gcp.JobName(slug, "web") || j.ServiceAccount.AccountID == gcp.ServiceAccountID(slug, "web") {
				t.Errorf("the %s implicit review job (%s, %s) is the job or the service account of %s's per-repository job: a review would run in a container built for the repository's own workflow", row.Provider, j.Job, j.ServiceAccount.AccountID, repo)
			}
		}
	}
}

// defaultWorkflow is the workflow a repository that names no resources and no
// timeouts resolves to, read out of internal/config rather than restated: the
// implicit review job is given exactly what such a workflow's jobs are given
// (config's own cpu, memory and total, which spec.go's per-repository path adds
// backend.TaskTimeoutSlack to), so a change to the default fails the test above
// instead of leaving the implicit review job on resources of its own.
func defaultWorkflow(t *testing.T) config.Workflow {
	t.Helper()
	c, ps := config.Parse([]byte(`version: 1
project: acme
gcp_project: acme-fugaro
git: { provider: github }
workflows: { web: { commands: { build: "true", test: "true" } } }
`))
	if len(ps) > 0 {
		t.Fatalf("the minimal repository config is not one config.Parse accepts: %v", ps)
	}
	w, ok := c.Workflows["web"]
	if !ok {
		t.Fatalf("the minimal config resolved no web workflow: %v", slices.Sorted(maps.Keys(c.Workflows)))
	}
	return w
}

// TestProfilesKeepTheirOwnReviewVariants: an installation's review set is its
// coding set's own twins, and beside them the implicit job of each provider no
// profile of it derives one for — here the Bitbucket one, which a Bitbucket
// repository's review has no other source of at all (no shared job serves the
// provider, so no profile's coding job is ever deployed for it). The github
// implicit job is the one that does derive: this installation's standard
// profile IS the profile the implicit job is deployed under, so its no-account
// coding job derives exactly the implicit job's name and service account, and
// the implicit emission is suppressed for the reason implicitReviewVariant's
// own gate gives — the instance is already there, under that key — and not
// because the installation has a github coding job at all
// (TestAddingAProfileKeepsTheImplicitReviewJob is about that distinction, and
// about what getting it wrong costs).
//
// Mutations (each run and restored): make the implicit emission unconditional —
// drop the collision check in implicitReviewVariant — and this fails: the
// project gets a second review job for the standard profile's default account,
// which the deployer refuses as a collision rather than deploying two jobs that
// share a name. Drop the credentials-service gate and the Bitbucket job is
// deployed on an installation that has not opted in.
func TestProfilesKeepTheirOwnReviewVariants(t *testing.T) {
	profiles := m4LocalConfig + projectJobRepoLC + m5Additions + projectProfilesLCBlock
	if coding := projectJobsFor(t, profiles); len(coding) != 2 {
		t.Fatalf("the fixture's coding set is %v, want the two profile jobs it is about", sortedJobNames(coding))
	}
	before := projectReviewJobsFor(t, profiles)
	after := projectReviewJobsFor(t, profiles+credentialsServiceLCBlock)
	// The credentials service is what the implicit jobs are gated on, so they
	// are the whole of the difference it makes to this set — one per provider
	// the profiles leave underived.
	added := map[string]bool{}
	for name := range after {
		if _, ok := before[name]; !ok {
			added[name] = true
		}
	}
	if want := credsvc.ReviewJobName(ImplicitReviewProfile, bitbucketProvider, config.DefaultClaudeAccount); len(added) != 1 || !added[want] {
		t.Fatalf("the credentials service added %v to %v, want the Bitbucket implicit job %s alone: the standard profile's no-account job derives the github implicit job's own name and service account, so that one is already deployed", added, sortedJobNames(before), want)
	}
	if len(after) != 3 {
		t.Fatalf("the review set is %v, want the two variants of the two profiles and the Bitbucket implicit job", sortedJobNames(after))
	}
	// For the jobs this set already had, the only difference the credentials
	// service makes is inside their environment: the address a review fetches
	// from.
	for name, j := range after {
		b, ok := before[name]
		if !ok {
			continue
		}
		if got, want := j.Env[providers.EnvCredentialsServiceURL], "https://fugaro-credentials-123456789012.us-east5.run.app"; got != want {
			t.Errorf("%s carries %s = %q, want %q", name, providers.EnvCredentialsServiceURL, got, want)
		}
		if j.CredentialsServiceGrant == nil {
			t.Errorf("%s carries no invoker grant on the credentials service, which its token fetch needs", name)
		}
		j.CredentialsServiceGrant = nil
		delete(b.Env, providers.EnvCredentialsServiceURL)
		delete(j.Env, providers.EnvCredentialsServiceURL)
		if !reflect.DeepEqual(b, j) {
			t.Errorf("the review job %s is not what this installation deployed before the credentials service:\ngot  %+v\nwant %+v", name, j, b)
		}
	}
}

// implicitReviewProfileLCBlock is the profiles a person adds to an installation
// that declares none: the default profile on the provider no shared job serves,
// and one github profile under a name that is not the implicit job's own. It is
// the shape the transition below is about — a github coding job exists, and
// nothing in the set derives the implicit review job's name or account.
const implicitReviewProfileLCBlock = `profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
  fast: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
`

// TestAddingAProfileKeepsTheImplicitReviewJob: a profile added to an
// installation that declares none does not take the implicit review job away
// with it.
//
// The transition is what makes this a requirement rather than a preference:
// implicitReviewVariant is the job's only source (ProjectReviewJobs), so a gate
// that suppresses it for the wrong reason drops the key from
// project_review_jobs, the next plan carries the delete of the module instance
// that holds it, and the whole `fugaro update` is refused by
// RefuseProjectJobDeletes — whose remedy is "restore the profile as it was",
// that is, never add it. So the gate is the collision (the name and the service
// account) and not the provider: the profile added below is a github one under
// another name, its own variants are added beside the implicit job, and the job
// keeps its key, its name and its account.
//
// The second half is the profile that does derive it. Named ImplicitReviewProfile,
// its no-account variant IS the implicit job — one name, one service account,
// derived over one triple — so the set holds the same key either way and the
// plan updates that module instance in place, which is what an installation
// that already deploys the job needs.
func TestAddingAProfileKeepsTheImplicitReviewJob(t *testing.T) {
	name := credsvc.ReviewJobName(ImplicitReviewProfile, githubProvider, config.DefaultClaudeAccount)
	before := projectReviewJobsFor(t, implicitReviewLC())
	j, ok := before[name]
	if !ok {
		t.Fatalf("the profile-less installation deploys %v, want the implicit review job %s", sortedJobNames(before), name)
	}
	after := projectReviewJobsFor(t, implicitReviewLC()+implicitReviewProfileLCBlock)
	kept, ok := after[name]
	if !ok {
		t.Fatalf("adding the github profile `fast` took the implicit review job %s away: the review set is now %v, and the next plan carries the delete of the job the installation already deployed", name, sortedJobNames(after))
	}
	if kept.Job != j.Job || kept.ServiceAccount.AccountID != j.ServiceAccount.AccountID {
		t.Errorf("the review job %s is %s as the account %s, want the job %s the installation already deploys as %s: another name or account is a replacement, and RefuseProjectJobDeletes stops the update on it",
			name, kept.Job, kept.ServiceAccount.AccountID, j.Job, j.ServiceAccount.AccountID)
	}
	// The added profile's own variant is beside it, which is the whole of what
	// adding the profile does to this set.
	if want := credsvc.ReviewJobName("fast", githubProvider, config.DefaultClaudeAccount); after[want].Job != want {
		t.Errorf("the added profile's own review variant %s is not in %v", want, sortedJobNames(after))
	}
	// And the profile the implicit job is deployed under derives the same key
	// rather than a second one: same name, same service account, so nothing
	// about this installation's plan changes when a person adds it.
	standard := projectReviewJobsFor(t, implicitReviewLC()+`profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
`)
	derived, ok := standard[name]
	if !ok {
		t.Fatalf("the profile named %s derives %v, want the implicit job's own key %s", ImplicitReviewProfile, sortedJobNames(standard), name)
	}
	if derived.ServiceAccount.AccountID != j.ServiceAccount.AccountID || derived.Job != j.Job {
		t.Errorf("the profile named %s derives %s as the account %s, want the implicit job's own %s as %s", ImplicitReviewProfile, derived.Job, derived.ServiceAccount.AccountID, j.Job, j.ServiceAccount.AccountID)
	}
}

// TestTheDeployedImplicitReviewJobIsTheJobTheRunnerPlaces binds the two
// spellings of the implicit review job to each other: the name this package
// deploys (ImplicitReviewJobName, the key of project_review_jobs) and the job
// the runner places a run in it by (runner.ProjectJobOfPolicy over the
// published policy of a project that declares no profile).
//
// Nothing else holds them together, and drift is silent. The runner cannot
// import this package (this package imports the runner for SecretEnvsVar) and
// this package cannot call the runner's candidate, which is unexported, so the
// triple is spelled twice: here as ImplicitReviewProfile, githubProvider and
// config.ReviewVariantAccount(""), there as projectpolicy.DefaultProfileName,
// gitprov.KindGitHub and config.ReviewVariantAccount(""). A profile renamed, a
// provider swapped or an account spelled differently in one place and not the
// other leaves the two computing different names, and the consequence is every
// run of the deployed job halting in the runner's cannotPlaceJob — the
// container is then a job the published policy does not compute. The runner's
// own tests cannot see it: each compares its candidate against a third copy of
// the same name.
//
// The policy is the one an installation that declares no profile publishes
// (implicitReviewLC: no `profiles` block at all), which is the only shape this
// job is deployed for and the one shape that makes it a project's only job.
//
// Mutations (each run and restored): spell the account differently in the
// runner's implicitReviewCandidates, or stand the deployed job under another
// profile (ImplicitReviewProfile), and this fails with the runner placing no
// project job of the name the installation deploys.
func TestTheDeployedImplicitReviewJobIsTheJobTheRunnerPlaces(t *testing.T) {
	name := ImplicitReviewJobName(githubProvider)
	deployed := projectReviewJobsFor(t, implicitReviewLC())
	if _, ok := deployed[name]; !ok {
		t.Fatalf("the profile-less installation deploys %v, want the implicit review job %s: this test is about the name it is deployed under", sortedJobNames(deployed), name)
	}
	profile, provider, account, ok := runner.ProjectJobOfPolicy(&projectpolicy.Policy{Version: projectpolicy.Version}, name)
	if !ok {
		t.Fatalf("the runner places a run in no project job named %s, and this installation deploys it: every run of that job stops in cannotPlaceJob, because its container is a job the published policy does not compute", name)
	}
	wantAccount := config.DefaultClaudeAccount + config.ReviewVariantSuffix
	if profile != ImplicitReviewProfile || provider != githubProvider || account != wantAccount {
		t.Errorf("the runner places %s as the job of (%s, %s, %s), want (%s, %s, %s): the profile-less policy's only project job is the one this installation deploys, under the provider and the account this job's own name is computed from",
			name, profile, provider, account, ImplicitReviewProfile, githubProvider, wantAccount)
	}
}

// TestImplicitReviewJobNeedsARecordedAppID: an installation with the credentials
// service on that records no single github App ID gets no implicit GITHUB job,
// and gets no error either.
//
// Both halves are the guard and not a nicety. The module refuses a github job
// whose FUGARO_GITHUB_APP_ID is not 1 to 20 digits
// (deploy/terraform/gcp/modules/project/job.tf), so a job planned without one
// stops the apply — an installation that converges today, whose repositories are
// Bitbucket ones and whose runs need no App at all, would stop converging, which
// is the one outcome this deployment must never cause. An installation that
// records TWO App IDs is the same case by the other road: recordedGitHubAppID
// reports it as an error for a project that declares profiles, and here it is a
// skip, because the job is deployed for nobody who asked for it and must not
// become a refusal of its own.
//
// The Bitbucket job is beside it in both fixtures on purpose: the two providers'
// gates are independent, and an installation of Bitbucket repositories is
// exactly the one whose github job is skipped (plan task RJ8, finding F6 — that
// installation still needs a review job, and it is the other one).
func TestImplicitReviewJobNeedsARecordedAppID(t *testing.T) {
	one := implicitReviewLC()
	noApp := strings.Replace(one, `, github_app_id: "123456789"`, "", 1)
	twoApps := strings.Replace(one, projectJobRepoLC,
		projectJobRepoLC+`  acme/other: { provider: github, base_branch: main, workflows: [web], github_app_id: "987654321" }`+"\n", 1)
	if noApp == one || twoApps == one {
		t.Fatal("the two fixtures below did not change: this test would read the installation that records one App ID and deploys the job")
	}
	bitbucket := credsvc.ReviewJobName(ImplicitReviewProfile, bitbucketProvider, config.DefaultClaudeAccount)
	for _, c := range []struct{ what, lc string }{
		{"an installation that records no App ID", noApp},
		{"an installation that records two App IDs", twoApps},
	} {
		jobs, err := ProjectReviewJobs(parseLC(t, c.lc), "fugaro-runs-proj-1234", false)
		if err != nil {
			t.Errorf("%s was refused: %v", c.what, err)
		}
		if _, ok := jobs[ImplicitReviewJobName(githubProvider)]; ok {
			t.Errorf("%s deployed the github review job %s, want none: the module refuses a github job without the App ID, so the job would stop the apply of an installation that converges today", c.what, ImplicitReviewJobName(githubProvider))
		}
		if _, ok := jobs[bitbucket]; !ok {
			t.Errorf("%s deployed %v, want the Bitbucket review job %s: the two providers' gates are independent, and no shared job serves Bitbucket at all", c.what, sortedJobNames(jobs), bitbucket)
		}
	}
}

// credentialsFixtureImage is the image the fixtures below give the credentials
// service: a release pinned by digest, as Credentials insists (a tag is
// mutable), and a digest of no release at all.
const credentialsFixtureImage = "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"

// TestInstallationReviewTfvarsGolden writes the tfvars
// internal/infra/testdata/installation-review.tfvars.json and the golden plan
// scripts/gen-golden-plans.sh makes of it: the installation the implicit
// default review jobs are deployed into — the credentials service ON, no
// profiles block at all, so no shared coding job and nothing for
// `fugaro review --pr N` to have run on — and one review job per provider it
// records a repository of (the fixture's acme/webapp is the github one and
// acme/sandbox the Bitbucket one), carrying no mount and their own service
// accounts.
//
// It is built the way `fugaro update` builds it and not by hand: the
// installation stage's own document (InstallationVars over Installation), with
// the credentials service written into it by the read-modify-write that turns
// the service on (SetCredentialsService) — the block is the deploy's own and
// Installation() deliberately leaves it out. So the fixture cannot claim
// something the deployment would not write, and the golden plan of the
// installation root is planned over exactly the document a run hands Terraform.
//
// Run with -update to rewrite; a change here is a change to what Terraform is
// given.
func TestInstallationReviewTfvarsGolden(t *testing.T) {
	lc := parseLC(t, implicitReviewLC())
	spec, err := Installation(lc, InstallOptions{BudgetBackend: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.ProjectJobs) != 0 {
		t.Fatalf("the fixture's installation deploys the coding jobs %v, want none: the whole point of the implicit review job is a project that publishes no profile", sortedJobNames(spec.ProjectJobs))
	}
	if len(spec.ProjectReviewJobs) != len(ImplicitReviewProviders) {
		t.Fatalf("the fixture's installation deploys the review jobs %v, want the implicit default job of each of %v", sortedJobNames(spec.ProjectReviewJobs), ImplicitReviewProviders)
	}
	svc, err := Credentials(lc, credentialsFixtureImage, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.URL = lc.CredentialsService.URL
	vars, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	vars, err = SetCredentialsService(vars, svc)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "installation-review.tfvars.json", vars)
}

// TestAddReviewVariantRefusesACollision: a review variant that derives a job
// name or a service account a coding job of the same installation already holds
// is refused, never added beside it. The two sets are derived over the same
// axis (the variant's account segment is the coding job's plus the review
// suffix), so this is the one crossing the review suffix is a boundary of: a
// coding job that holds the variant's name would be a review running as an
// account that mounts the App key.
//
// Mutation (run, restore): drop the cross-set check in addReviewVariant — the
// variant lands in the set beside the job that holds its name.
func TestAddReviewVariantRefusesACollision(t *testing.T) {
	coding := map[string]ProjectJob{"fugaro-p-x": {Profile: "standard", Provider: "github", Account: "default-review",
		Job: "fugaro-p-x", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-1"}}}
	for _, b := range []ProjectJob{
		{Profile: "standard", Provider: "github", Account: "default", Job: "fugaro-p-x", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-2"}},
		{Profile: "standard", Provider: "github", Account: "default", Job: "fugaro-p-y", ServiceAccount: ServiceAccount{AccountID: "fugaro-p-sa-1"}},
	} {
		out := map[string]ProjectJob{}
		if err := addReviewVariant(out, coding, b); err == nil {
			t.Errorf("the variant (%s, %s) was added beside the coding job (%s, %s): a collision must be refused", b.Job, b.ServiceAccount.AccountID, "fugaro-p-x", "fugaro-p-sa-1")
		}
		if len(out) != 0 {
			t.Errorf("the review set moved on a refused add: %v", sortedJobNames(out))
		}
	}
}

func TestSharedBitbucketJobsAreGrantedTheServiceWithTheSwitchOff(t *testing.T) {
	lc := m4LocalConfig + projectJobRepoLC + m5Additions + projectProfilesLCBlock + credentialsServiceLCBlock
	jobs := projectJobsFor(t, lc)
	if len(jobs) == 0 {
		t.Fatal("no shared job derived")
	}
	for name, j := range jobs {
		if j.CredentialsService != nil {
			t.Fatalf("%s is served with no repository switched on", name)
		}
		if got, want := j.CredentialsServiceGrant != nil, j.Provider != githubProvider; got != want {
			t.Errorf("%s (%s): invoker grant = %v, want %v", name, j.Provider, got, want)
		}
	}
}
