package infra

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// ProjectJob is one shared project job: one entry of the installation root's
// project_jobs, keyed by its Cloud Run job name (design
// docs/design/project-centric.md §4.1, plan task 3.2 step 1). The tagged
// fields are the Terraform module's object type, exactly.
//
// It carries nothing per-repository: a project job serves every repository
// admitted to its profile, so the repository, the ref and the task arrive at
// launch and in task.json (phase 1), and the provider is in the job's own
// name, which no launcher can write.
type ProjectJob struct {
	Profile         string            `json:"profile"`
	Provider        string            `json:"provider"`
	Account         string            `json:"account"`
	Job             string            `json:"job"`
	ServiceAccount  ServiceAccount    `json:"service_account"`
	Image           string            `json:"image"`
	CPU             string            `json:"cpu"`
	Memory          string            `json:"memory"`
	TaskTimeoutS    int               `json:"task_timeout_s"`
	Env             map[string]string `json:"env"`
	SecretEnv       map[string]string `json:"secret_env"` // env var → logical secret name
	BucketCondition Condition         `json:"bucket_condition"`
	// TokenCondition is the project job account's read-only grant on the
	// runs bucket's budget token objects, the whole of tokens/ with no slug
	// (gcp.ProjectTokenBucketCondition): the same widening as
	// BucketCondition, and the same reason — one job serves every repository
	// admitted to its profile, so its token read cannot name one repository's
	// prefix. objectViewer, never objectUser: it must never be able to delete
	// or replace a queued run's token (budget-token-channel.md §7.2, D16).
	TokenCondition Condition `json:"token_condition"`
	Vertex         bool      `json:"vertex"`
}

// ProjectJobs is the installation's project jobs, by job name: one job and
// one job service account per (profile, provider, account) of the local
// config's profiles (design §4.1). An installation whose local config names
// no profile gets none — the map is nil, the tfvars key is left out and the
// root creates nothing, so an existing installation's plan is unchanged.
//
// bucket and vertex are the installation's, computed by Installation, so the
// jobs' bucket condition, FUGARO_* env and Vertex grant are the ones the
// rest of the installation is built with. Nothing here reads a repository:
// the job set is a function of the profiles and the accounts, which is the
// claim design §13 makes about the service-account count and the golden test
// asserts (plan step 6).
func ProjectJobs(lc *localcfg.Config, bucket string, vertex bool) (map[string]ProjectJob, error) {
	if len(lc.Profiles) == 0 {
		return nil, nil
	}
	// The profiles are the policy's own document (config.JobProfile, one
	// declaration), so they are held to the policy's rules here rather than
	// to a second copy of them: `fugaro init` must not deploy a job that
	// `fugaro project policy publish` would refuse, and a profile the nodes
	// refuse describes a job no workflow could ever select.
	//
	// The rules are the policy's own, and so is the accounts block they are
	// judged against: it is derived here from the same config by the same
	// function `fugaro project policy publish` derives it with
	// (projectpolicy.DeriveAccounts), so a profile that names an account
	// nothing defines, or admits a repository its account does not cover, is
	// refused at init with the sentence publish would print — and never
	// deployed as a job whose token is bounded by a list no node states.
	profiles := profilesAsPolicy(lc.Profiles)
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets)
	if err != nil {
		return nil, userErr("%v", err)
	}
	if err := projectpolicy.CheckProfiles(profiles, accounts); err != nil {
		return nil, userErr("%v", err)
	}
	// Bitbucket stays on the per-repository path in phase 3: the owner ruled
	// on 2026-10-15 (probe U1) that a Bitbucket credential cannot be scoped to
	// one project's repositories, so a job every repository of a profile
	// shares must never hold one (plan Task 3.0 step 2, D5 amended). The
	// policy may still name such a profile; no job is deployed for it.
	//
	// It is refused here, before the App's ID is read, rather than inside the
	// loop below, because the two refusals are both true of a config that
	// declares only Bitbucket profiles and records no App ID — and only this
	// one is the reason no job can be deployed. Read the other way round, such
	// a config is told its job profiles have no GitHub App ID and sent to
	// onboard a github repository (the 2026-10-19 review): the message an
	// operator acts on would name a cause they do not have.
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		if lc.Profiles[name].Provider == "bitbucket" {
			return nil, userErr("profiles.%s.provider: a shared job never serves bitbucket in phase 3: a Bitbucket credential cannot be scoped to one project's repositories (the owner's ruling of 2026-10-15), so Bitbucket repositories stay on their per-repository jobs. Remove the profile or make it a github one", name)
		}
	}
	// Every project job is a github one in this phase (the refusal above is
	// why), so the App's ID is resolved once, for all of them: the
	// project has one App (design §4.5), and a job that carries it must carry
	// the same one as its neighbours.
	appID, err := projectJobGitHubAppID(lc)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ProjectJob, len(lc.Profiles))
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		timeout, err := config.ParseDuration(pr.TimeoutClass)
		if err != nil {
			// CheckProfiles holds the spelling; this cannot happen, and a
			// second parse is cheaper than a duplicated rule.
			return nil, userErr("profiles.%s.timeout_class: %v", name, err)
		}
		env := projectJobEnv(lc, bucket, vertex)
		if pr.Provider == githubProvider {
			// The App's ID, non-secret, beside the key the profile mounts as
			// the github-app-key secret (owner ruling of 2026-10-17, plan
			// task 3.5 step 4). The per-repository path sets the same
			// variable from the same field (spec.go's ws.Env), and the
			// runner's github provider refuses to build without it, so both
			// halves are needed for a job to start under any run.
			env[providers.EnvGitHubAppID] = appID
		}
		secretEnv, err := projectJobSecrets(lc, name, env, pr)
		if err != nil {
			return nil, err
		}
		// The account axis: empty until phase 5, because no profile may
		// mount a Claude OAuth token yet — projectpolicy.validProfiles
		// refuses such a profile above, which is the one rule publish, this
		// deployer and both readers of the node share.
		const account = ""
		saID := gcp.ProjectServiceAccountID(name, pr.Provider, account)
		out[gcp.ProjectJobName(name, pr.Provider, account)] = ProjectJob{
			Profile:        name,
			Provider:       pr.Provider,
			Account:        account,
			Job:            gcp.ProjectJobName(name, pr.Provider, account),
			ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.ProjectSADisplayName(name, pr.Provider, account)},
			Image:          lc.BaseImage(config.BaseKind),
			CPU:            strconv.Itoa(pr.CPU),
			Memory:         pr.Memory,
			TaskTimeoutS:   int((timeout + backend.TaskTimeoutSlack) / time.Second),
			Env:            env,
			SecretEnv:      secretEnv,
			BucketCondition: Condition{
				Title:      gcp.BucketConditionTitle(saID),
				Expression: gcp.ProjectBucketCondition(bucket, gcp.JobBucketPrefixes),
			},
			TokenCondition: Condition{
				Title:      gcp.ProjectTokenBucketConditionTitle(saID),
				Expression: gcp.ProjectTokenBucketCondition(bucket),
			},
			Vertex: vertex,
		}
	}
	return out, nil
}

// githubProvider is the provider kind a shared job serves in phase 3
// (config.Providers' spelling, and the value config.JobProfile.Provider
// holds). Bitbucket is refused above, so it is also the only one a project
// job is ever built for.
const githubProvider = "github"

// projectJobGitHubAppID is the GitHub App ID every project job carries in its
// environment, resolved from the installation's own configuration (owner
// ruling of 2026-10-17, plan task 3.5 step 4).
//
// It is the field each GitHub repository records (localcfg.Repo.GitHubAppID),
// which `fugaro init --repo --github-app-id` writes and the per-repository
// path reads to set the same variable (spec.go's ws.Env) — there is no second
// place an App ID is recorded, and it is not a secret. The key stays the
// github-app-key Secret Manager secret the profile mounts.
//
// One App serves the project (design §4.5: the runner mints a token per
// repository from the project's App), so every GitHub repository the config
// records must name the same ID: the job every repository of a profile runs
// on holds one App's ID and one App key, which is why a project that uses
// several Apps cannot share jobs. Two IDs are refused rather than one of them
// chosen, because either choice deploys a job the other's repositories cannot
// run on — and the failure would be at run time, in the credential, not here.
//
// Nothing recorded at all is refused too, and it is the state a project that
// declares profiles before it has onboarded a GitHub repository is in: a
// shared job runs on the github provider, and providers.EnvGitHubAppID is
// what the runner's github provider is built from, so a job deployed without
// it would halt every run it started. The fix is the one the per-repository
// path asks for at the repository stage ("is on GitHub and has no GitHub App
// ID: pass --github-app-id"): record it, in the local config, once.
func projectJobGitHubAppID(lc *localcfg.Config) (string, error) {
	id, holder := "", ""
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[name]
		if r.Provider != githubProvider || r.GitHubAppID == "" {
			continue
		}
		if id == "" {
			id, holder = r.GitHubAppID, name
			continue
		}
		if id != r.GitHubAppID {
			return "", userErr("the local config records two GitHub Apps: %s has %s and %s has %s. A shared project job runs every repository admitted to its profile on one job, and that job holds one App's ID and one github-app-key secret, so a project that uses several Apps cannot share jobs (design docs/design/project-centric.md §4.5): put the repositories a profile admits on one App, or keep the others on per-repository jobs (a profile is what a repository with checkout: clone selects, so a profile left out of the repository's fugaro.yaml keeps its own job)",
				holder, id, name, r.GitHubAppID)
		}
	}
	if id == "" {
		return "", userErr("this project's local config declares job profiles and records no GitHub App ID: a shared project job therefore cannot be deployed. A project job runs on the github provider, and the runner builds that provider from %s (the App's ID, which is not a secret), refusing to start without it — so a job deployed without it would halt every run it started. Record the App's ID by onboarding the repository the App serves (fugaro init --repo <owner/name> --github-app-id <id>), which writes it to the local config, then run fugaro init again",
			providers.EnvGitHubAppID)
	}
	return id, nil
}

// profilesAsPolicy is the local config's profiles as the policy carries them
// (projectpolicy.Profile embeds config.JobProfile), so ProjectJobs checks
// them with the policy's own rules rather than with a copy of them.
func profilesAsPolicy(profiles map[string]config.JobProfile) map[string]projectpolicy.Profile {
	out := make(map[string]projectpolicy.Profile, len(profiles))
	for name, pr := range profiles {
		out[name] = projectpolicy.Profile{JobProfile: pr}
	}
	return out
}

// projectJobEnv is a project job's plain environment: the installation's
// identity and the budget backend's address and web key, and nothing else.
//
// It is deliberately *not* budgetEnv, which sets per-repository values too
// (the token ceiling, the model allow-list, the price overrides and the
// deploy-time mode come from the repository's own config and its policy): a
// project job is shared, so those ride the execution (plan task 3.3) instead
// of sitting in the job, where one repository's ceiling would become every
// repository's. What stays is what belongs to the installation (D11, plan
// task 1.4 step 1): a job deployed without the two backend variables cannot
// read the project's policy at all.
//
// vertex is the installation's own Vertex setting — the same one that becomes
// the job's Vertex grant, and the same one a per-repository workflow reads at
// spec.go's own `if ws.Vertex` — so a job granted roles/aiplatform.user also
// carries the project and region under Vertex's names. A profile's Vertex
// flag is an installation-wide fact, not a per-repository one, so it belongs
// here rather than on the execution: the Google auth libraries read
// ANTHROPIC_VERTEX_PROJECT_ID and CLOUD_ML_REGION (they do not read
// GOOGLE_CLOUD_PROJECT for this), so without them every Vertex-auth run in a
// shared job — the default installation's runs — would be refused for want of
// a project, with the grant it was given unused.
func projectJobEnv(lc *localcfg.Config, bucket string, vertex bool) map[string]string {
	env := map[string]string{
		ProjectEnv:    lc.Name,
		GCPProjectEnv: lc.GCPProject,
		RegionEnv:     lc.Region,
		BucketEnv:     "gs://" + bucket,
	}
	if vertex {
		env[VertexRegionEnv] = lc.Region
		env[VertexProjectEnv] = lc.GCPProject
	}
	if b := lc.Budget; b != nil {
		if b.RTDBURL != "" {
			env[RTDBURLEnv] = b.RTDBURL
		}
		if b.FirebaseAPIKey != "" {
			env[FirebaseAPIKeyEnv] = b.FirebaseAPIKey
		}
	}
	return env
}

// projectJobSecrets resolves a profile's secrets into a project job's secret
// env (variable → project-wide secret container, the shape the workflow
// module's secret_env has); env is the job's plain env, which no secret may
// shadow.
//
// A profile is shared by every repository admitted to it, so it may mount
// only project-wide containers — a name with no container is refused rather
// than granted, because the grant would name a secret the installation does
// not create (design docs/design/project-secrets.md §4). That is also why
// nothing per-repository can be mounted here: a repository's own secret is
// reachable only by a job that repository alone runs.
//
// Two rules refuse a secret, and they are held at two different places on
// purpose. A Claude OAuth token — the default account's claude-oauth-token and
// a named account's alike — is refused by the profile's own rule, before any
// job is built: a Claude account is person-owned (design D2 and §7: D2 sweeps
// API keys and the git credential into project scope, never
// claude-oauth-token) and its allow_data_to is a per-repository list a shared
// job cannot honour, since every repository the profile admits runs on the
// same job. ProjectJobs runs projectpolicy.CheckProfiles first, so that rule
// is the one this function's callers hit — the same rule `fugaro project
// policy publish` holds a profile to, which is the point of it being there
// rather than here (an earlier revision repeated the refusal below, where it
// could never be reached). Phase 5 decides how an account reaches a shared
// job; until then the account axis of the job's name is always empty, and the
// field stays so that phase does not rename every job.
//
// The Bitbucket token is the one this function refuses itself, because
// Bitbucket stays per-repository in phase 3 (see ProjectJobs) and that is not
// a rule about profiles a policy would have an opinion on.
//
// What a shared job does not honour for the secrets it does mount is the
// project_secrets entry's allow_data_to: the entry decides that the container
// exists (projectSecretContainers), and the profile decides who mounts it.
// That is D2's ruling — a project-scope key reaches every repository on the
// profiles that mount it — not an oversight.
func projectJobSecrets(lc *localcfg.Config, profile string, env map[string]string, pr config.JobProfile) (map[string]string, error) {
	containers := projectSecretContainers(lc)
	secretEnv := map[string]string{}
	for i, name := range pr.Secrets {
		if name == "bitbucket-token" {
			return nil, userErr("profiles.%s.secrets[%d]: %q is the Bitbucket credential, and a shared job never holds one in phase 3: it cannot be scoped to one project's repositories (the owner's ruling of 2026-10-15)", profile, i, name)
		}
		if _, ok := containers[name]; !ok {
			return nil, userErr("profiles.%s.secrets[%d]: %q is not stored project-wide, so a shared job cannot mount it: a profile is shared by every repository it admits, and only a project-wide secret has one copy. Store it with project_secrets in the local config", profile, i, name)
		}
		v, ok := projectSecretEnv(lc, name)
		if !ok {
			return nil, userErr("profiles.%s.secrets[%d]: %q is neither a secret Fugaro reserves nor the key of a configured model provider", profile, i, name)
		}
		if _, dup := secretEnv[v]; dup {
			return nil, userErr("profiles.%s.secrets[%d]: the variable %s is mounted twice", profile, i, v)
		}
		secretEnv[v] = name
	}
	for k := range secretEnv {
		if _, dup := env[k]; dup {
			return nil, userErr("profiles.%s: the secret variable %s collides with a variable the platform sets", profile, k)
		}
	}
	return secretEnv, nil
}

// projectSecretEnv is the variable a project-wide secret is mounted as: the
// reserved name's own variable or a model provider's key variable. ok is
// false for a name that is neither, whatever its shape. A Claude account's
// token never reaches it: projectpolicy.validProfiles refuses such a profile
// before ProjectJobs builds anything.
func projectSecretEnv(lc *localcfg.Config, name string) (string, bool) {
	if v, ok := config.ReservedSecrets[name]; ok {
		return v, true
	}
	if _, ok := config.ProviderBySecret(lc.Providers, name); ok {
		return config.ProviderKeyEnv(name), true
	}
	return "", false
}

// projectJobAddress is the address of a project job's Cloud Run job in the
// installation root (roots/installation/main.tf calls modules/project as
// "project", one instance per job name).
var projectJobAddress = regexp.MustCompile(`^module\.project\["[^"]+"\]\.google_cloud_run_v2_job\.this$`)

// RefuseProjectJobDeletes refuses a plan that deletes or replaces a project
// job, whatever --allow-delete names. Retiring a project job is deferred: the
// module protects its job from deletion and, unlike the workflow module
// (modules/workflow/job.tf's allow_job_delete, internal/infra/retire.go), has
// no switch to lift it. A profile removed, or one whose provider or secrets
// change and so its job's name, plans the old instance's delete, and an apply
// let through would destroy the launchers' grants on the job and then fail on
// the deletion protection, leaving the job unlaunchable. So the plan is
// stopped before the apply, with the way back.
func RefuseProjectJobDeletes(p *tf.Plan) error {
	if p == nil {
		return nil
	}
	var refused []string
	for _, rc := range p.ResourceChanges {
		if projectJobAddress.MatchString(rc.Address) && slices.Contains(rc.Change.Actions, "delete") {
			refused = append(refused, fmt.Sprintf("%s (%s)", rc.Address, strings.Join(rc.Change.Actions, ", ")))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return userErr("the plan would delete or replace %d project job(s), and retiring a project job is not supported yet: the job has deletion protection and nothing lifts it, so the apply would remove its launchers' grants and then fail. Nothing was applied; restore the profile as it was in the local config (its name, provider and secrets):\n  %s",
		len(refused), strings.Join(refused, "\n  "))
}
