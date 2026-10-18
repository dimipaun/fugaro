package infra

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	lc := parseLC(t, m4LocalConfig+m5Additions+projectProfilesLCBlock)
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
	// The four identity variables plus Vertex's two — no budget backend in
	// this fixture, so nothing else may appear.
	allowed := []string{ProjectEnv, GCPProjectEnv, RegionEnv, BucketEnv, VertexRegionEnv, VertexProjectEnv}
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
		lc := parseLC(t, m4LocalConfig+m5Additions+c.lc)
		want := projectpolicy.CheckProfiles(profilesAsPolicy(lc.Profiles))
		if want == nil {
			t.Fatalf("%s: the policy admits the profile, so nothing below is about the layer that refuses it", c.name)
		}
		_, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
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
	jobs := projectJobsFor(t, m4LocalConfig+m5Additions+`profiles:
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
	// profiles.
	if jobs := projectJobsFor(t, m4LocalConfig+m5Additions+`profiles:
  standard: { provider: bitbucket, cpu: 2, memory: 8Gi, timeout_class: 6h }
`); len(jobs) != 0 {
		t.Errorf("jobs = %v, want none: no shared job serves Bitbucket", slices.Sorted(maps.Keys(jobs)))
	}
	// The token is the other half of the ruling and is still refused: a shared
	// job that serves GitHub may not mount a credential that reaches every
	// repository of a Bitbucket workspace (ProjectJobs' own check, which a
	// skip does not reach).
	if _, err := ProjectJobs(parseLC(t, m4LocalConfig+m5Additions+`project_secrets:
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
// built with: a profile mounting no Claude token (an api-key profile, whose
// job's account segment is empty), the only shape a shared job deploys in
// phase 3 — a profile mounting a Claude account's token is refused by
// projectJobSecrets until phase 5 (plan task 3.2's fix round).
const projectAccountLCBlock = `profiles:
  apikey: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h }
  standard: { provider: github, cpu: 1, memory: 2Gi, timeout_class: 1h }
`

// The account axis is computed on two sides, from two different documents: the
// deployer resolves it while it deploys (always empty in phase 3, since no
// profile may mount a Claude token), and the runner recomputes it at run start
// from the published policy's profile, which carries the secret names and
// nothing else (config.ClaudeAccountOfSecrets, plan task 3.3 step 1). They
// must agree: a job whose name the runner would not recompute is a job every
// run of that profile halts on, because the run cannot place the name it is
// in and adopts no identity from it.
//
// What is asserted is the deployed *name* — the key the runner matches its
// CLOUD_RUN_JOB against — and not only the field beside it, because the name is
// what a run has to reproduce.
func TestProjectJobAccountIsWhatTheRunnerRecomputes(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+m5Additions+projectAccountLCBlock)
	jobs, err := ProjectJobs(lc, "fugaro-runs-proj-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		gcp.ProjectJobName("apikey", "github", ""):   "",
		gcp.ProjectJobName("standard", "github", ""): "",
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
