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
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
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
	// to a second copy of them: `fugaro update` must not deploy a job that
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
	// Every project job is a github one in this phase (a Bitbucket profile is
	// skipped below), so the App's ID is resolved once, for all of them: the
	// project has one App (design §4.5), and a job that carries it must carry
	// the same one as its neighbours. It is resolved only when some profile
	// gets a shared job at all: a config whose profiles are all Bitbucket ones
	// deploys no job, so it needs no App and is not refused for lacking one.
	appID := ""
	if slices.ContainsFunc(slices.Collect(maps.Values(lc.Profiles)), func(pr config.JobProfile) bool { return SharedJobProvider(pr.Provider) }) {
		if appID, err = projectJobGitHubAppID(lc); err != nil {
			return nil, err
		}
	}
	out := make(map[string]ProjectJob, len(lc.Profiles))
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		// Bitbucket stays on the per-repository path in phase 3: the owner
		// ruled on 2026-10-15 (probe U1) that a Bitbucket credential cannot
		// be scoped to one project's repositories, so a job every repository
		// of a profile shares must never hold one (plan Task 3.0 step 2, D5
		// amended). A Bitbucket profile is therefore skipped, not refused: the
		// policy may name one — projectpolicy admits it, a Bitbucket
		// repository's workflow may select it, and the runner only requires a
		// selected profile to be defined — and the installation's other
		// profiles are deployed as they are. No job is deployed for it, which
		// is the fact the launcher reads the same predicate for
		// (SharedJobProvider, and internal/cli's launchProjectJobFor): a
		// repository selecting such a profile keeps its per-repository job
		// rather than being launched onto a job that does not exist.
		if !SharedJobProvider(pr.Provider) {
			continue
		}
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
		// One job per account the profile serves (config.ProjectJobAccounts,
		// the one function the deployer, the launcher and the runner compute
		// the axis with): the profile's no-account job, whose name, service
		// account and mounts are byte for byte what this deployer built before
		// accounts existed (D9), plus one per account the profile lists.
		//
		// Each account's job gets its own copy of the profile's mounts — the
		// token of one account must never ride on another's job — its own copy
		// of the profile's environment (the redaction list below is per job),
		// and its own service account, which is the only thing that may read
		// that account's token (D2).
		for _, account := range config.ProjectJobAccounts(pr) {
			jobSecrets := maps.Clone(secretEnv)
			if err := projectAccountMount(lc, name, pr, account, jobSecrets, env); err != nil {
				return nil, err
			}
			// The variables this job mounts secrets as, for the runner's
			// redactor (runner.SecretEnvsVar, the variable the per-repository
			// path sets at spec.go's own secret block). It is computed per job
			// and from the job's own mounts, after the account's token is on
			// them: the jobs of one profile differ — each account's job mounts
			// that account's token, and the no-account job mounts the default
			// account's exactly when the profile lists it (D5) — so a list
			// computed once for the profile would leave whichever job it was
			// not computed from with a token the run publishes unredacted. The
			// env map itself is per job for the same reason: it is the same map
			// the runner is handed, and the jobs beside this one must not carry
			// this job's list.
			jobEnv := maps.Clone(env)
			jobEnv[runner.SecretEnvsVar] = redactionEnvs(jobSecrets)
			saID := gcp.ProjectServiceAccountID(name, pr.Provider, account)
			if err := addProjectJob(out, ProjectJob{
				Profile:        name,
				Provider:       pr.Provider,
				Account:        account,
				Job:            gcp.ProjectJobName(name, pr.Provider, account),
				ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.ProjectSADisplayName(name, pr.Provider, account)},
				Image:          lc.BaseImage(config.BaseKind),
				CPU:            strconv.Itoa(pr.CPU),
				Memory:         pr.Memory,
				TaskTimeoutS:   int((timeout + backend.TaskTimeoutSlack) / time.Second),
				Env:            jobEnv,
				SecretEnv:      jobSecrets,
				BucketCondition: Condition{
					Title:      gcp.BucketConditionTitle(saID),
					Expression: gcp.ProjectBucketCondition(bucket, gcp.JobBucketPrefixes),
				},
				TokenCondition: Condition{
					Title:      gcp.ProjectTokenBucketConditionTitle(saID),
					Expression: gcp.ProjectTokenBucketCondition(bucket),
				},
				Vertex: vertex,
			}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// ProjectReviewJobs is the installation's review-only job variants, by job
// name: one per (profile, provider, account) the coding set deploys (design
// docs/cloud-senior-review/reconciliation.md's CSR7 entry, D883, D893(c) and
// the owner's ruling O1 of 2026-11-01). They are the jobs a project's own
// senior review runs on, so the container that runs a test from the head under
// review (D885) is never the container a coding run holds its credentials in.
//
// It is a set of its own rather than entries in ProjectJobs', and that is load
// bearing: the coding set is `config.ProjectJobAccounts`' axis read once (the
// one function the deployer, the launcher and the runner compute it with), and
// every consumer of that set — the credentials service's admitted accounts
// (credsvc.ReviewJobAccountsFor derives the variants FROM the axis, not from a
// second job set), doctor's census, the golden plan's count of `module.project`
// instances — would see a doubled axis if the variants were in it. The root
// deploys this map as a module instance of its own, with the installation's
// secrets deliberately NOT handed in (roots/installation/main.tf): a variant
// that is not offered a container cannot mount one.
//
// What a variant mounts is nothing (O1): no github-app-key, no review-signing
// key and no Claude OAuth token either. The block is D892(a)'s ruling of the
// same day — the credentials service signs, so no key is in the container — and
// the reason is one property of the job, not three: a variant runs untrusted
// code with the job's service account, so every credential in its environment
// is a credential that code can read (and, on the shared OS user, one whose
// /proc residual it can read).
//
// What it is handed instead is the credentials service's address
// (providers.EnvCredentialsServiceURL, D893(c)), the way a per-repository
// launch hands it to a repository's own job. Its git provider is therefore the
// keyless one (internal/gitprov/providers reads the variable), which fetches the
// read-only installation token from the service by name when it is needed and
// refreshes it through the Token(ctx, minValid) contract; the request carries
// the App's ID alone and the repository and permissions are the service's
// decision (credsvc/github.go). Nothing is handed in — no per-launch secret is
// created and no execution's environment carries a token value — and an
// installation that has opted into no service gets no variant URL, exactly as a
// repository job gets none: the variable's presence is the opt-in
// (credentialsServiceLaunchEnv's rule, read here off the same local config).
//
// A variant's name, service account and account segment are
// credsvc.ReviewJobName's and credsvc.ReviewServiceAccountID's, over the axis
// spelled the way the policy speaks it: the one spelling the service admits a
// caller by. Its mounts, resources and timeout are its coding twin's — it
// serves the same profile, over the same repositories, and it reaches the same
// buckets (F15 records that reach: a review job writes one run's record and
// artifact, and the whole of runs/ is more than that; the owner accepted it on
// 2026-10-17 and narrowing it is a bucket-layout change). The one environment
// entry it drops is the coding job's redaction list (runner.SecretEnvsVar):
// there are no mounts for it to name, and an empty list beside no secret would
// claim a redactor had something to hide.
//
// It holds no Vertex grant either (the owner's ruling of 2026-11-04): the
// design's grant list is the two conditions, the account's own bucket reach
// and nothing else, and under O1 the reviewer's model calls are served by the
// credentials service, so roles/aiplatform.user on this account would be a
// reach no task of the design names. reviewVariant says what stays in its
// environment.
func ProjectReviewJobs(lc *localcfg.Config, bucket string, vertex bool) (map[string]ProjectJob, error) {
	coding, err := ProjectJobs(lc, bucket, vertex)
	if err != nil {
		return nil, err
	}
	if len(coding) == 0 {
		return nil, nil
	}
	out := make(map[string]ProjectJob, len(coding))
	for _, j := range coding {
		v := reviewVariant(lc, j)
		// The variant's name and service account are derived over the same
		// axis as the coding set, so an account whose own name ends in the
		// review suffix collides with the variant of the account it names: a
		// profile listing acme-review gets a coding job named exactly as the
		// review variant of acme, and the two derive ONE service account — so
		// the variant would run as a job account that mounts the App key,
		// which is the whole of what it exists not to be. addProjectJob's
		// check is within a set; this is the same check across the two, and it
		// fails closed rather than deploying the variant onto an account that
		// may read a secret.
		//
		// The name rule (config.ValidClaudeAccountRef, over the accounts a
		// policy admits) now refuses such an account before this is reached,
		// and this stays the last line: it is the only place the two sets are
		// compared, so a config that arrives by another road is refused here
		// too.
		for name, c := range coding {
			if c.Job != v.Job && c.ServiceAccount.AccountID != v.ServiceAccount.AccountID {
				continue
			}
			return nil, userErr("profiles.%s: the review variant of the account %q derives the job name %s and the service account %s, and the shared job %s of the account %q already holds them: a Claude account whose name ends in %q is the review variant of the account it names, and the review job would run as an account that mounts the App key. Rename that account (claude_accounts) and run fugaro update again",
				j.Profile, v.Account, v.Job, v.ServiceAccount.AccountID, name, c.Account, credsvc.ReviewVariantSuffix)
		}
		if err := addProjectJob(out, v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ReviewVariantAccount is config.ReviewVariantAccount, the mapping the
// deployer here, the launcher (internal/cli's project job lookup) and the
// runner that places a review run in its variant all derive the variant's names
// over. It is kept as a name here because the deployer reads it in the
// vocabulary of the jobs it builds, and it is the same function, never a second
// spelling: a second one would name a job nobody deployed or a service account
// nobody created.
func ReviewVariantAccount(account string) string { return config.ReviewVariantAccount(account) }

// reviewVariant is one coding job's review-only twin. j is the coding job the
// deployer built for that (profile, provider, account), so the variant inherits
// everything about the triple — its provider, its resources, its timeout, its
// bucket and token conditions and its environment — and differs from it in
// exactly four things: the name and service account carry the review suffix (so
// no account of the deployment is ever shared with a coding job), the mounts
// are empty, the environment is the credentials service's address, and the
// Vertex grant is not carried (the owner's ruling of 2026-11-04: the design's
// grant list names no aiplatform role, and under O1 the reviewer's model calls
// go through the credentials service).
func reviewVariant(lc *localcfg.Config, j ProjectJob) ProjectJob {
	// The account segment is the policy's spelling, not the axis's: the axis
	// writes the default account as the empty string (config.ProjectJobAccounts)
	// and the review job holds no token of its own to join, so the mapping
	// config.ProjectJobTokenAccount makes for the token-carrying entries is made
	// here for the variant's name — the same mapping, and the same account name,
	// credsvc.ReviewJobAccountsFor computes the admitted set with.
	account := ReviewVariantAccount(j.Account)
	saID := credsvc.ReviewServiceAccountID(j.Profile, j.Provider, account)
	env := maps.Clone(j.Env)
	delete(env, runner.SecretEnvsVar)
	if cs := lc.CredentialsService; cs != nil && cs.URL != "" {
		env[providers.EnvCredentialsServiceURL] = cs.URL
	}
	return ProjectJob{
		Profile:        j.Profile,
		Provider:       j.Provider,
		Account:        account,
		Job:            credsvc.ReviewJobName(j.Profile, j.Provider, account),
		ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.ProjectSADisplayName(j.Profile, j.Provider, account+credsvc.ReviewVariantSuffix)},
		Image:          j.Image,
		CPU:            j.CPU,
		Memory:         j.Memory,
		TaskTimeoutS:   j.TaskTimeoutS,
		Env:            env,
		SecretEnv:      map[string]string{},
		BucketCondition: Condition{
			Title:      gcp.BucketConditionTitle(saID),
			Expression: j.BucketCondition.Expression,
		},
		TokenCondition: Condition{
			Title:      gcp.ProjectTokenBucketConditionTitle(saID),
			Expression: j.TokenCondition.Expression,
		},
		// No Vertex grant, whatever the coding job's: the module's one
		// aiplatform member is what this field drives (modules/project/sa.tf),
		// and the design's grant list for a review variant does not name it.
		// The two Vertex names in the environment above stay, because they are
		// not a grant: an agent on `agent.auth: vertex` refuses to start
		// without CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID
		// (internal/agent/env.go), and a variant that cannot start is not a
		// narrower variant.
		Vertex: false,
	}
}

// addProjectJob adds j to the job set, refusing a job whose name or service
// account another (profile, provider, account) already holds. Both are derived
// with a short hash (gcp.ProjectJobName, gcp.ProjectServiceAccountID), so a
// collision is improbable rather than impossible, and a silent replacement in
// this map would deploy one account's job in another's place, or give two
// jobs one service account that may read both accounts' tokens.
func addProjectJob(out map[string]ProjectJob, j ProjectJob) error {
	for _, o := range out {
		if o.Job == j.Job || o.ServiceAccount.AccountID == j.ServiceAccount.AccountID {
			return userErr("profiles.%s: the shared job of (%s, %s, account %q) derives the job name %s and the service account %s, and the job of (%s, %s, account %q) already holds %s and %s: two jobs must never share either. Rename one profile or account and run fugaro update again",
				j.Profile, j.Profile, j.Provider, j.Account, j.Job, j.ServiceAccount.AccountID,
				o.Profile, o.Provider, o.Account, o.Job, o.ServiceAccount.AccountID)
		}
	}
	out[j.Job] = j
	return nil
}

// githubProvider is the provider kind a shared job serves in phase 3
// (config.Providers' spelling, and the value config.JobProfile.Provider
// holds). A Bitbucket profile is skipped above, so it is also the only one a project
// job is ever built for.
const githubProvider = "github"

// projectJobGitHubAppID is the GitHub App ID every project job carries in its
// environment, resolved from the installation's own configuration (owner
// ruling of 2026-10-17, plan task 3.5 step 4).
//
// It is the field each GitHub repository records (localcfg.Repo.GitHubAppID),
// which `fugaro update --repo --github-app-id` writes and the per-repository
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
	id, err := recordedGitHubAppID(lc)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", userErr("this project's local config declares job profiles and records no GitHub App ID: a shared project job therefore cannot be deployed. A project job runs on the github provider, and the runner builds that provider from %s (the App's ID, which is not a secret), refusing to start without it — so a job deployed without it would halt every run it started. Record the App's ID by onboarding the repository the App serves (fugaro update --repo <owner/name> --github-app-id <id>), which writes it to the local config, then run fugaro update again",
			providers.EnvGitHubAppID)
	}
	return id, nil
}

// recordedGitHubAppID is the one GitHub App ID the local config records, and
// the refusal of a config that records two. It is projectJobGitHubAppID's
// resolution without that function's refusal of "none": the coordinator's
// aggregation job carries the ID too (Coordinator), but a project whose
// repositories are all on bitbucket simply has no job to deploy rather than a
// config to fix, and it is the caller that decides which of the two answers it
// is looking at. Two IDs stay refused for both callers: either job holds one
// App's ID and one App key, so a project that uses two Apps cannot run one.
func recordedGitHubAppID(lc *localcfg.Config) (string, error) {
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
	return id, nil
}

// SharedJobProvider reports whether a shared project job serves that git
// provider kind in phase 3, and it is the one place the answer lives: the
// deployer (ProjectJobs, which skips a profile of a provider it does not
// serve) and the launcher (internal/cli's launchProjectJobFor, which keeps
// such a repository on its per-repository job) both ask it, so the job a
// launch computes is always a job this package deployed.
//
// It is github alone, and that is the owner's ruling of 2026-10-15 on probe
// U1 rather than a gap: a Bitbucket credential cannot be scoped to one
// project's repositories, and a shared job holds one credential for every
// repository it serves, so Bitbucket stays on the per-repository path (plan
// Task 3.0 step 2, D5 as amended, and docs/project-layer.md's "Bitbucket
// repositories never reach one"). Nothing about a Bitbucket profile is
// refused: the profile is a document the policy carries, and skipping it here
// is what makes "no job is deployed for it" true rather than an installation
// `fugaro update` refuses whole.
//
// Every kind of config.Providers is answered — the list is the kinds a
// repository may use — and the test beside this file pins that the two agree,
// so a provider added to that list is not silently given a shared job by a
// forgotten line here.
func SharedJobProvider(provider string) bool {
	return slices.Contains(sharedJobProviders, provider)
}

// sharedJobProviders are the provider kinds a shared project job serves
// (SharedJobProvider). It is a list rather than a comparison so that the test
// can walk it, and it holds kinds of config.Providers.
var sharedJobProviders = []string{"github"}

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

// projectJobEnv is the installation's half of a project job's plain
// environment: its identity and the budget backend's address and web key, and
// nothing else. ProjectJobs adds the two values that follow from the profile
// and the job — the GitHub App's ID (non-secret, owner ruling of 2026-10-17)
// and runner.SecretEnvsVar, the list of variables that job's secrets are
// mounted as.
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
// job is built: a Claude token is never something an operator lists among a
// profile's mounts (design project-named-oauth-tokens §4.1, D4), and that rule
// stays in force now that a profile can serve accounts. The permission is the
// profile's claude_accounts field, and the token reaches an account job
// through projectAccountMount alone, never through this function.
// ProjectJobs runs projectpolicy.CheckProfiles first, so that rule is the one
// this function's callers hit — the same rule `fugaro project policy publish`
// holds a profile to, which is the point of it being there rather than here
// (an earlier revision repeated the refusal below, where it could never be
// reached).
//
// The Bitbucket token is the one this function refuses itself, because
// Bitbucket stays per-repository in phase 3 (see ProjectJobs) and that is not
// a rule about profiles a policy would have an opinion on. It is reached on a
// profile of a provider a shared job serves — a GitHub profile mounting it,
// which would hand a job every repository of a Bitbucket workspace the token
// reaches. A Bitbucket profile mounting it never gets here: ProjectJobs skips
// such a profile before this call, and no job of it exists to hold anything.
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

// projectAccountMount adds the one secret_env entry that delivers an account
// job's Claude OAuth token: CLAUDE_CODE_OAUTH_TOKEN → the account's own
// logical secret (config.ClaudeAccountSecret). It is the carrier the
// per-repository path already uses (internal/infra's SecretMounts, `case
// "oauth"`), so the project level has no second delivery path to maintain
// (design project-named-oauth-tokens §4.3, D1).
//
// It is deliberately ONE function, and it is the seam the security service
// replaces (§4.6). When the credentials proxy lands, this function stops
// adding the entry and nothing else moves: not the job's name (a function of
// the (profile, provider, account) triple, gcp.ProjectJobName, and of no
// mount), not its profile, not the policy's accounts block, and not the grant
// — modules/project/sa.tf's google_secret_manager_secret_iam_member.job
// iterates the job's own secret_env, so the account job's secretAccessor
// member on its own account's secret goes with this entry and with nothing
// else. A second place that mounted the token would be a second thing to
// remove, and a second thing to get wrong in the meantime.
//
// account is one entry of config.ProjectJobAccounts, so the empty one is the
// profile's no-account job: it carries the default account's token exactly
// when the profile lists "default" (D5), which is the same rule that function
// states — a profile that lists no account mounts no Claude token anywhere.
//
// The mount is refused, rather than granted, when the installation creates no
// container for it: a job whose secret_env names a container the root does not
// create plans nothing, and the job then fails at run time, in the credential.
// The configs that reach here are already refused earlier — publish and init
// derive the accounts block through projectpolicy.DeriveAccounts, which
// requires the account's own list (claude_accounts.<name>.allow_data_to, or
// project_secrets.<claude-oauth-token>.allow_data_to for the default account)
// to name at least one repository, and a non-empty list there is exactly what
// projectSecretContainers turns into a container — so this is the backstop
// that keeps the two rules from drifting apart, and the one that fires if
// either stops holding.
func projectAccountMount(lc *localcfg.Config, profile string, pr config.JobProfile, account string, secretEnv, env map[string]string) error {
	if account == "" && !slices.Contains(pr.ClaudeAccounts, config.DefaultClaudeAccount) {
		return nil
	}
	// The project's own Anthropic API key is not a Claude account, and this
	// deployer serves it no job: its credential is the project's own copy, held
	// by the platform and attached to a review caller's request by the
	// credentials service, so there is no token for a mount to add and no
	// variant of the account axis to build. The refusal has to be this arm's,
	// ahead of the container lookup, because that lookup's answer for this
	// value is claude_accounts.anthropic-api-key--project.allow_data_to — a key
	// ValidateClaudeAccounts refuses, so it is a remedy nobody can carry out.
	// Taking the key off the profile is the move that works, and the route that
	// deploys a job for it replaces this arm (the api-key review plan's AK5).
	if account == config.AnthropicKeyAccount {
		return userErr("profiles.%s.claude_accounts: %q is the project's own Anthropic API key, not a Claude account, and no job serves it: its credential is the project's own copy, held by the platform, so no shared job mounts a token for it and no review variant is deployed for it in this release. Take the key off profiles.%s.claude_accounts and run fugaro update again — a repository that reads the key for its own model calls mounts it from the project's own copy, which a profile does not have to name",
			profile, account, profile)
	}
	logical := config.ClaudeAccountSecret(account)
	if _, ok := projectSecretContainers(lc)[logical]; !ok {
		// The block the operator edits differs by account: the default
		// account is not definable in claude_accounts (its name is reserved
		// there), so its list — and so its container — comes from the
		// project_secrets entry that already decides whether a project-wide
		// copy of claude-oauth-token exists at all (design §4.2, D6).
		source, shown := "claude_accounts."+account+".allow_data_to", account
		if account == "" {
			source, shown = "project_secrets."+logical+".allow_data_to", config.DefaultClaudeAccount
		}
		return userErr("profiles.%s.claude_accounts: the account %q has no project-wide copy in this installation, so no shared job can mount its token: a job's secret_env names a container of the installation's project-wide secrets, and this installation creates none for %s. Give the account a non-empty list — %s, whose repositories are also what the account's policy list is derived from — and run fugaro update again",
			profile, shown, logical, source)
	}
	v := config.ReservedSecrets[config.ClaudeOAuthToken]
	if _, dup := env[v]; dup {
		return userErr("profiles.%s: the secret variable %s collides with a variable the platform sets", profile, v)
	}
	// Never over another mount of the same variable: the profile rules refuse
	// a Claude token among a profile's secrets, and a silent overwrite here
	// would swap one credential for another if that rule ever moved.
	if prev, dup := secretEnv[v]; dup {
		return userErr("profiles.%s: the secret variable %s is already mounted from %s, so the account's token cannot be mounted as it", profile, v, prev)
	}
	secretEnv[v] = logical
	return nil
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
// "project", one instance per job name, and as "project_review" for the
// review-only variants of CSR7). The variant is the same module over the same
// axis, so its job is the same resource under the second instance's address and
// is refused the same way.
var projectJobAddress = regexp.MustCompile(`^module\.project(?:_review)?\["[^"]+"\]\.google_cloud_run_v2_job\.this$`)

// RefuseProjectJobDeletes refuses a plan that deletes or replaces a project
// job, whatever --allow-delete names, and a review-only variant with it: the
// variant is the same module instance under the same axis, it is protected
// from deletion the same way, and a plan that removes the coding job of a
// triple removes its variant too (the variant is derived from the coding set,
// so the profile that disappears takes both). Retiring a project job is
// deferred: the module protects its job from deletion and, unlike the workflow
// module (modules/workflow/job.tf's allow_job_delete, internal/infra/retire.go),
// has no switch to lift it. A profile removed, or one whose provider or secrets
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
