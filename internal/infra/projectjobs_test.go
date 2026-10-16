package infra

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// projectProfilesLCBlock is the local config block the project-job tests and
// the installation golden are built with: two profiles on two providers, one
// mounting a Claude account's token (so it runs as the default account) and
// one mounting none (so it runs as no account — an api-key job, whose account
// segment is empty), plus the project-wide containers they mount.
//
// The two providers are the point of the provider axis (finding 4): one job
// serves one provider, so a profile per provider is two jobs.
const projectProfilesLCBlock = `project_secrets:
  bitbucket-token: { allow_data_to: [acme/sandbox] }
  claude-oauth-token: { allow_data_to: [acme/sandbox] }
  github-app-key: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [bitbucket-token, claude-oauth-token] }
  heavy: { provider: github, cpu: 8, memory: 32Gi, timeout_class: 24h, secrets: [github-app-key] }
`

const githubAppKeyLCBlock = `project_secrets:
  github-app-key: { allow_data_to: [acme/sandbox] }
`

// threeReposLocalConfig is m4LocalConfig with three repositories instead of
// one, for the claim that the job set does not move with them (design §13).
var threeReposLocalConfig = func() string {
	const one = "repos:\n  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }\n"
	const three = `repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
  acme/other: { provider: bitbucket, base_branch: master, workflows: [web] }
  acme/webapp: { provider: github, base_branch: main, workflows: [web] }
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
	jobs := projectJobsFor(t, m4LocalConfig+m5Additions+projectProfilesLCBlock)
	if len(jobs) != 2 {
		t.Fatalf("%d jobs, want 2: %v", len(jobs), slices.Sorted(maps.Keys(jobs)))
	}
	// The account axis is the Claude account the profile mounts its token
	// for; a profile that mounts none runs as no account.
	want := map[string]struct{ profile, provider, account string }{
		gcp.ProjectJobName("standard", "bitbucket", "default"): {"standard", "bitbucket", "default"},
		gcp.ProjectJobName("heavy", "github", ""):              {"heavy", "github", ""},
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
	standard, heavy := jobs[gcp.ProjectJobName("standard", "bitbucket", "default")], jobs[gcp.ProjectJobName("heavy", "github", "")]
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
	one := projectJobsFor(t, m4LocalConfig+m5Additions+projectProfilesLCBlock)
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
	jobs := projectJobsFor(t, m4LocalConfig+m5Additions+projectProfilesLCBlock)
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, RTDBURLEnv, FirebaseAPIKeyEnv}
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
			wantSecrets = map[string]string{"FUGARO_BITBUCKET_TOKEN": "bitbucket-token", "CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token"}
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
	withBudget := projectJobsFor(t, m4LocalConfig+m5Additions+projectProfilesLCBlock+`
budget: { rtdb_url: https://aurora-default-rtdb.firebaseio.com, firebase_api_key: AIzaSyA1234567890abcdefghijklmnopqrstu }
`)
	for name, j := range withBudget {
		if j.Env[RTDBURLEnv] != "https://aurora-default-rtdb.firebaseio.com" || j.Env[FirebaseAPIKeyEnv] != "AIzaSyA1234567890abcdefghijklmnopqrstu" {
			t.Errorf("job %s backend env = %v", name, j.Env)
		}
		if len(j.Env) != 6 {
			t.Errorf("job %s sets %d variables, want the four identity ones and the two backend ones: %v", name, len(j.Env), j.Env)
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
  bitbucket-token: { allow_data_to: [acme/sandbox] }
  claude-oauth-token: { allow_data_to: [acme/sandbox] }
  github-app-key: { allow_data_to: [acme/webapp] }
`, "", 1)
	for _, c := range []struct{ name, lc, want string }{
		{"no container at all", m4LocalConfig + m5Additions + bare, "not stored project-wide"},
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
	if env, ok := projectSecretEnv(parseLC(t, m4LocalConfig+m5Additions), "acme-workflow-secret"); ok {
		t.Errorf("a workflow's own secret resolves to the variable %s", env)
	}

	// A provider's key is project-wide by construction (projectSecretContainers),
	// and it is mounted as the runner's provider variable.
	jobs := projectJobsFor(t, m4LocalConfig+m5Additions+providerLC+`profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [openrouter-api-key] }
`)
	j, ok := jobs[gcp.ProjectJobName("standard", "bitbucket", "")]
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

// The account axis: a profile runs as the Claude account whose token it
// mounts, and a job holds one identity, so two accounts on one profile are
// refused rather than silently run as one of them.
func TestProjectJobRunsAsTheAccountItMounts(t *testing.T) {
	lc := m4LocalConfig + m5Additions + claudeAccountsLC + `profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [claude-oauth-token-acme-personal] }
  heavy: { provider: bitbucket, cpu: 8, memory: 16Gi, timeout_class: 24h, secrets: [claude-oauth-token-acme-personal, claude-oauth-token-acme-overflow] }
`
	_, err := ProjectJobs(parseLC(t, lc), "fugaro-runs-proj-1234", false)
	// Two Claude accounts on one profile collide on the single variable the
	// runner reads, so the job's account can never be one of two.
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN is mounted twice") {
		t.Fatalf("err = %v, want a refusal of two accounts on one profile", err)
	}
	jobs := projectJobsFor(t, m4LocalConfig+m5Additions+claudeAccountsLC+`profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [claude-oauth-token-acme-personal] }
`)
	j, ok := jobs[gcp.ProjectJobName("standard", "bitbucket", "acme-personal")]
	if !ok {
		t.Fatalf("jobs = %v, want one named for the account acme-personal", sortedJobNames(jobs))
	}
	if j.Account != "acme-personal" || j.SecretEnv["CLAUDE_CODE_OAUTH_TOKEN"] != "claude-oauth-token-acme-personal" {
		t.Errorf("job = %+v", j)
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
  heavy: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`, "standard must be defined"},
		{"resources Cloud Run refuses", `profiles:
  standard: { provider: bitbucket, cpu: 8, memory: 1Gi, timeout_class: 6h }
`, "at least"},
		{"a timeout that is not one", `profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: "soon" }
`, "not a duration"},
		{"a pattern of another provider", `profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, allow_repos: [github/acme/web] }
`, "this profile serves bitbucket"},
	} {
		_, err := ProjectJobs(parseLC(t, m4LocalConfig+m5Additions+c.profiles), "fugaro-runs-proj-1234", false)
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
	jobs, err := ProjectJobs(parseLC(t, m4LocalConfig+m5Additions), "fugaro-runs-proj-1234", false)
	if err != nil || jobs != nil {
		t.Errorf("ProjectJobs = %v, %v; want none", jobs, err)
	}
}

// projectAccountLCBlock is the local config block the recomputation test is
// built with: the three shapes a profile's account axis has — a profile
// mounting the reserved token (the default account), one mounting a named
// account's token, and one mounting no Claude token at all (an api-key
// profile, whose job's account segment is empty) — plus the containers they
// mount.
const projectAccountLCBlock = `claude_accounts:
  acme-personal:
    allow_data_to: ["acme/sandbox"]
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/sandbox] }
profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h, secrets: [claude-oauth-token] }
  personal: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [claude-oauth-token-acme-personal] }
  apikey: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h }
`

// The account axis is computed on two sides, from two different documents: the
// deployer resolves it while it deploys, from the local config's
// claude_accounts (projectJobSecrets, over a secret the installation stores
// project-wide), and the runner recomputes it at run start from the published
// policy's profile, which carries the secret names and nothing else
// (config.ClaudeAccountOfSecrets, plan task 3.3 step 1). They must be one rule
// (the function says so in its own comment): a job whose name the runner would
// not recompute is a job every run of that profile halts on, because the run
// cannot place the name it is in and adopts no identity from it.
//
// What is asserted is the deployed *name* — the key the runner matches its
// CLOUD_RUN_JOB against — and not only the field beside it, because the name is
// what a run has to reproduce.
//
// Mutation (run, restored): change either side's reading of the shape — drop
// the "-<name>" suffix in ClaudeAccountOfSecrets, or resolve the account from
// the profile's name in projectJobSecrets. This test says so here rather than
// at a halt on every run of the profile.
func TestProjectJobAccountIsWhatTheRunnerRecomputes(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions+projectAccountLCBlock)
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		gcp.ProjectJobName("standard", "bitbucket", "default"):    "default",
		gcp.ProjectJobName("personal", "github", "acme-personal"): "acme-personal",
		gcp.ProjectJobName("apikey", "github", ""):                "",
	}
	if !slices.Equal(slices.Sorted(maps.Keys(jobs)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("jobs = %v, want %v", slices.Sorted(maps.Keys(jobs)), slices.Sorted(maps.Keys(want)))
	}
	for name, account := range want {
		j := jobs[name]
		if j.Account != account {
			t.Errorf("job %s runs as %q, want %q", name, j.Account, account)
		}
		// The runner's recomputation, from the profile's secret names alone.
		got, _ := config.ClaudeAccountOfSecrets(lc.Profiles[j.Profile].Secrets)
		if got != account {
			t.Errorf("the profile %s (%v) recomputes to the account %q, and its job is named for %q: the run would not find the job it is in",
				j.Profile, lc.Profiles[j.Profile].Secrets, got, account)
		}
		if recomputed := gcp.ProjectJobName(j.Profile, j.Provider, got); recomputed != name {
			t.Errorf("the runner computes the job name %s for the profile %s, and the deployed one is %s", recomputed, j.Profile, name)
		}
	}
}
