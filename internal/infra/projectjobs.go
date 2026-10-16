package infra

import (
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
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
	Vertex          bool              `json:"vertex"`
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
	if err := projectpolicy.CheckProfiles(profilesAsPolicy(lc.Profiles)); err != nil {
		return nil, userErr("%v", err)
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
		env := projectJobEnv(lc, bucket)
		secretEnv, account, err := projectJobSecrets(lc, name, env, pr)
		if err != nil {
			return nil, err
		}
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
			Vertex: vertex,
		}
	}
	return out, nil
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
func projectJobEnv(lc *localcfg.Config, bucket string) map[string]string {
	env := map[string]string{
		ProjectEnv:    lc.Name,
		GCPProjectEnv: lc.GCPProject,
		RegionEnv:     lc.Region,
		BucketEnv:     "gs://" + bucket,
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

// projectJobSecrets resolves a profile's secrets into a project job's mounts
// and the Claude account the job runs as: the plain env, the secret env
// (variable → project-wide secret container, the shape the workflow module's
// secret_env has) and the account axis of the job's name.
//
// A profile is shared by every repository admitted to it, so it may mount
// only project-wide containers — a name with no container is refused rather
// than granted, because the grant would name a secret the installation does
// not create (design docs/design/project-secrets.md §4). That is also why
// nothing per-repository can be mounted here: a repository's own secret is
// reachable only by a job that repository alone runs.
//
// The account is the Claude OAuth account whose token the profile mounts
// (config.ClaudeAccountBySecret), and it is the account axis of the job's
// name: one job and one service account per (profile, provider, account)
// exists so that each account's token is readable only by the runs using
// that account (design §7's reason for the axis). A profile that mounts no
// Claude token — an api-key or Vertex installation — runs as no account, and
// its job's account segment is empty.
//
// A job holds one identity, and the account can never be ambiguous: every
// Claude account's token is mounted as the one variable the runner reads
// (CLAUDE_CODE_OAUTH_TOKEN), so a profile naming two accounts fails the
// duplicate-variable check below — the account axis has exactly one value or
// the profile is refused.
func projectJobSecrets(lc *localcfg.Config, profile string, env map[string]string, pr config.JobProfile) (secretEnv map[string]string, account string, err error) {
	containers := projectSecretContainers(lc)
	secretEnv = map[string]string{}
	for i, name := range pr.Secrets {
		if _, ok := containers[name]; !ok {
			return nil, "", userErr("profiles.%s.secrets[%d]: %q is not stored project-wide, so a shared job cannot mount it: a profile is shared by every repository it admits, and only a project-wide secret has one copy. Store it with project_secrets in the local config", profile, i, name)
		}
		v, ok := projectSecretEnv(lc, name)
		if !ok {
			return nil, "", userErr("profiles.%s.secrets[%d]: %q is neither a secret Fugaro reserves nor the key of a configured model provider or Claude account", profile, i, name)
		}
		if _, dup := secretEnv[v]; dup {
			return nil, "", userErr("profiles.%s.secrets[%d]: the variable %s is mounted twice", profile, i, v)
		}
		secretEnv[v] = name
		if acc, isClaude := config.ClaudeAccountBySecret(lc.ClaudeAccounts, name); isClaude {
			account = acc
		}
	}
	for k := range secretEnv {
		if _, dup := env[k]; dup {
			return nil, "", userErr("profiles.%s: the secret variable %s collides with a variable the platform sets", profile, k)
		}
	}
	return secretEnv, account, nil
}

// projectSecretEnv is the variable a project-wide secret is mounted as: the
// reserved name's own variable, a Claude account's token variable (every
// account's token is CLAUDE_CODE_OAUTH_TOKEN, the one the runner reads) or a
// model provider's key variable. ok is false for a name that is none of
// those, whatever its shape.
func projectSecretEnv(lc *localcfg.Config, name string) (string, bool) {
	if v, ok := config.ReservedSecrets[name]; ok {
		return v, true
	}
	if _, ok := config.ClaudeAccountBySecret(lc.ClaudeAccounts, name); ok {
		return config.ReservedSecrets["claude-oauth-token"], true
	}
	if _, ok := config.ProviderBySecret(lc.Providers, name); ok {
		return config.ProviderKeyEnv(name), true
	}
	return "", false
}
