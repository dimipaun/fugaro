package infra

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
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

// The job's env is what the installation knows and nothing per-repository
// (plan task 3.2 step 1): the four identity variables, plus the budget
// backend's address and web key when the installation has them. The provider
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
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, RTDBURLEnv, FirebaseAPIKeyEnv, providers.EnvGitHubAppID, runner.SecretEnvsVar}
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
		if len(j.Env) != 8 {
			t.Errorf("job %s sets %d variables, want the four identity ones, the two backend ones, the App's ID and the redaction list: %v", name, len(j.Env), j.Env)
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
	// The four identity variables plus Vertex's two and the redaction list — no
	// budget backend in this fixture, so nothing else may appear.
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, VertexRegionEnv, VertexProjectEnv, providers.EnvGitHubAppID, runner.SecretEnvsVar}
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
	// resolver itself. No local config can reach it — projectSecretContainers
	// only ever produces names of the three kinds below — so this is the
	// backstop that keeps that true if either list changes.
	if env, ok := projectSecretEnv(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions), "acme-workflow-secret"); ok {
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
		accounts, err := projectpolicy.DeriveAccounts(profilesAsPolicy(lc.Profiles), lc.ClaudeAccounts, lc.ProjectSecrets)
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

// Bitbucket stays on the per-repository path in phase 3 (the owner's ruling
// of 2026-10-15 on probe U1: a Bitbucket credential cannot be scoped to one
// project's repositories; plan Task 3.0 step 2, D5 amended). So a shared job
// never serves Bitbucket and never mounts the Bitbucket token, whatever
// provider the profile names.
//
// A Bitbucket profile is skipped and not refused, which is the whole of this
// test: the policy layer admits such a profile (projectpolicy.validProfiles,
// and TestProfileAdmitsOnlyItsOwnProvider), a Bitbucket repository's workflow
// may select it, and the runner only requires a selected profile to be
// defined — so an installation whose local config names one must deploy, and
// the profile simply gets no job. Refusing the installation whole, as this
// function used to, made `fugaro init` fail on a document `fugaro project
// policy publish` accepts.
//
// Mutation (verified, run and restored): put the refusal back (return nil,
// userErr(...) for !SharedJobProvider) — the first case below stops with an
// error instead of deploying the GitHub profile. Mutation: drop the
// SharedJobProvider skip — the first case deploys a job for the Bitbucket
// profile, which no credential may serve.
func TestProjectJobsSkipBitbucketProfiles(t *testing.T) {
	// A project with repositories on both providers defines both profiles
	// (the plan's own example, standard and standard-bitbucket): the GitHub
	// one is deployed, the Bitbucket one is not, and the call succeeds.
	jobs := projectJobsFor(t, m4LocalConfig+projectJobRepoLC+m5Additions+`profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h }
  standard-bitbucket: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`)
	if got := slices.Sorted(maps.Keys(jobs)); !slices.Equal(got, []string{gcp.ProjectJobName("standard", "github", "")}) {
		t.Errorf("jobs = %v, want the GitHub profile's alone: a shared job never serves Bitbucket", got)
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
	// repository of a Bitbucket workspace (ProjectJobs' own check, which a
	// skip does not reach).
	if _, err := ProjectJobs(parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+`project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
profiles:
  standard: { provider: github, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [bitbucket-token] }
`), "fugaro-runs-proj-1234", false); err == nil ||
		!strings.Contains(err.Error(), `profiles.standard.secrets[0]: "bitbucket-token" is the Bitbucket credential`) {
		t.Errorf("err = %v, want the Bitbucket token refused on a GitHub profile", err)
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
	for _, kind := range config.Providers {
		want := kind == "github"
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
			err := projectAccountMount(lc, "standard", lc.Profiles["standard"], c.account, map[string]string{}, map[string]string{})
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
// *value* except two places, each named below with what it is. The Claude
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
// that package has to be added deliberately.
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
// Mutation (run, restore): read the account's token in the deployer or the
// launcher (any `sm.Access(...)` outside the files below) — the scan fails
// with the file that did it.
func TestNoSecretValueReadUnderInternal(t *testing.T) {
	// The files allowed to name a secret-value read, and what each one is.
	allowed := map[string]string{
		"backend/gcp/secrets.go": "the Secret Manager client: Access is the GitHub App key pre-check's one read, Set writes values",
		"cli/creds.go":           "C1.25's owner-run `fugaro creds set`/`creds ls` over the same ONE bundle container (Q6): set reads the document it is about to rewrite",
		"cli/githubapp.go":       "the GitHub App key pre-check of init and doctor",
		"credsvc/bundle.go":      "the credentials service's ONE bundle container (Q6): the service attaches the credential upstream, and it is the only Fugaro process that ever holds a model credential's value",
	}
	// The reads, by the API each one is spelled with: the google-api client's
	// Versions.Access, the cloud.google.com/go client's AccessSecretVersion,
	// and a secretmanager.SecretPayload built or read.
	reads := []string{".Access(", "AccessSecretVersion", "SecretPayload"}
	var found []string
	root := ".." // internal/
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
		for _, r := range reads {
			if bytes.Contains(b, []byte(r)) {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				found = append(found, filepath.ToSlash(rel))
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if _, ok := allowed[f]; !ok {
			t.Errorf("internal/%s names a secret-value read (%v), and only these files may: %v. A Claude OAuth token reaches a run by the platform's mount, never through Fugaro", f, reads, slices.Sorted(maps.Keys(allowed)))
		}
	}
	// And every allowed file is still one, so a removal is noticed too.
	for f := range allowed {
		if !slices.Contains(found, f) {
			t.Errorf("internal/%s no longer reads a secret value, and it is on the allow-list for %s: update the list rather than letting it drift", f, allowed[f])
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
func TestProjectJobDeletesAreRefusedUntilRetirementExists(t *testing.T) {
	job := `module.project["` + gcp.ProjectJobName("standard", "github", "") + `"].google_cloud_run_v2_job.this`
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
	// Anything else the module holds may still be replaced with
	// --allow-delete (a grant whose condition changed, say), and a plan that
	// deletes no project job is untouched.
	for _, addr := range []string{
		`module.project["` + gcp.ProjectJobName("standard", "github", "") + `"].google_storage_bucket_iam_member.job`,
		"module.installation.google_cloud_run_v2_job.history[0]",
	} {
		if err := RefuseProjectJobDeletes(planDeleting(addr)); err != nil {
			t.Errorf("deleting %s: %v", addr, err)
		}
	}
	if hints := DeleteHints(planDeleting(job), nil); len(hints) != 1 || !strings.Contains(hints[0], "profiles") {
		t.Errorf("hints = %v, want one naming the local config's profiles", hints)
	}
}

// TestProjectAccountMountIsTheOneSeam: the Claude token reaches a shared job
// through projectAccountMount and nowhere else in the deployer (design §4.6:
// the security service replaces that one function). No other function of
// projectjobs.go may name an account's secret or the token's variable, and
// ProjectJobs must call it: a mount written anywhere else is a second carrier
// the swap would leave behind.
//
// Mutation (run, restore): inline the mount in ProjectJobs' loop (the same
// secret_env, by another path) — every behavioural test still passes, and this
// one fails naming ProjectJobs.
func TestProjectAccountMountIsTheOneSeam(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "projectjobs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
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
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "projectAccountMount" && fn.Name.Name == "ProjectJobs" {
					calls = true
				}
			}
			return true
		})
	}
	if !calls {
		t.Error("ProjectJobs does not call projectAccountMount: the account jobs' token reaches them by some other path")
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
	err := projectAccountMount(lc, "standard", lc.Profiles["standard"], "acme-personal", secretEnv, map[string]string{})
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
