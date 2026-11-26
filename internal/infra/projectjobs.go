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
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// ProjectJob is one shared project job: one entry of the installation root's
// project_jobs, keyed by its Cloud Run job name (design
// docs/project-centric/spec.md §4.1, plan task 3.2 step 1). The tagged
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
	// CredentialsService is the installation's credentials service, as the
	// module reads it (name and region, no URL: repoCredentialsService), and
	// null for a job whose profile the service does not serve. It is the whole
	// of the switch on this side — the module turns it into the job account's
	// one roles/run.invoker grant on that service (modules/project/sa.tf) and
	// reads it again for the model mounts the same profile gives up
	// (local.is_shared_job_served_model, B5.7) — and, null, it moves nothing at
	// all about the job, which keeps every mount it has and every accessor on
	// them (G9, D147). Absent when null, so the document of a job the service
	// does not serve is byte for byte what it was. See
	// projectJobServesTheCredentialsService for what "served" means here.
	CredentialsService *CredentialsService `json:"credentials_service,omitempty"`
	// CredentialsServiceGrant is the same service for a job that reaches it
	// for every shared job, switched or not: a Bitbucket job clones through it, and
	// a GitHub job's grant must not vanish when a readiness record flips off.
	// Its account keeps every model mount. The module grants on
	// either field and drops mounts on CredentialsService only.
	CredentialsServiceGrant *CredentialsService `json:"credentials_service_grant,omitempty"`
}

// projectJobGitGrant is the service every shared job's account is granted the
// invoker role on whatever the model switch says: the grant follows the
// installation having the service, never a repository's readiness record, which
// a failed bundle read can flip to false and a plan would then delete a grant
// the job still needs. Inert without the switch: the service refuses a caller
// it does not admit (Q6).
func projectJobGitGrant(lc *localcfg.Config) *CredentialsService {
	return installationCredentialsService(lc)
}

// ProjectJobs is the installation's project jobs, by job name: one job and
// one job service account per (profile, provider, account) of the local
// config's profiles (design §4.1), less the names the local config declares a
// key credential — those get no job at all (config.ClaudeAccountTokenNames,
// the rule the per-repository set is built with too). An installation whose
// local config names no profile gets none — the map is nil, the tfvars key is
// left out and the root creates nothing, so an existing installation's plan is
// unchanged.
//
// bucket and vertex are the installation's, computed by Installation, so the
// jobs' bucket condition, FUGARO_* env and Vertex grant are the ones the
// rest of the installation is built with. Nothing here reads a repository:
// the job set is a function of the profiles and the accounts, which is the
// claim design §13 makes about the service-account count and the golden test
// asserts (plan step 6).
func ProjectJobs(lc *localcfg.Config, bucket string, vertex bool) (map[string]ProjectJob, error) {
	return projectJobs(lc, bucket, vertex, nil)
}

// ProjectJobAccount is one point of the shared jobs' account axis: the
// (profile, provider, account) triple a job of ProjectJobs or ProjectReviewJobs
// is deployed for, which that job states in its own entry (ProjectJob's first
// three fields).
//
// It is a value of its own because one rule is about a triple rather than about
// a job: the accounts this installation declares a KEY credential are composed
// into no job at all (ProjectKeyAccountJobs), and the run that composes the
// installation's spec names them so that the carry can keep the job an earlier
// release deployed for one (CarriedProjectJobAccounts).
type ProjectJobAccount struct {
	Profile  string
	Provider string
	Account  string
}

// ProjectKeyAccountJobs is the shared jobs' account axis with the names this
// installation declares a KEY credential left IN: the (profile, provider,
// account) triples ProjectJobs composes no job for.
//
// It is the very filter that drops them, read to NAME what it drops
// (config.ClaudeAccountTokenNames over config.ProjectJobAccounts, the two
// functions the composer applies), because a run that composes a spec has to
// carry the job an earlier release deployed for such a name from the
// installation's own tfvars (CarriedProjectJobAccounts). That job is one the
// installation holds, and a document rewritten without it is the plan of its
// destruction: RefuseProjectJobDeletes stops the whole `fugaro update` on that
// plan, with "restore the profile as it was" as the only remedy it can print —
// and here that remedy does not exist, because putting the name back into the
// profile's claude_accounts is exactly what the filter drops again. The
// installation would stop converging for good (CLAUDE.md's HARD RULE).
//
// The profiles a carry holds whole (projectJobs' carried, which is
// InstallOptions.CarriedJobProfiles) are not named here: every job of such a
// profile, a key credential's included, is the deployed one CarriedProjectJobs
// keeps, so naming its accounts again would be a second answer to one question.
// A profile no shared job serves is not named either, and for the filter's own
// reason: no job of its axis is composed, so none was deployed for a carry to
// keep.
func ProjectKeyAccountJobs(lc *localcfg.Config, carried []string) []ProjectJobAccount {
	var out []ProjectJobAccount
	for name, pr := range lc.Profiles {
		if !SharedJobProvider(pr.Provider) || slices.Contains(carried, name) {
			continue
		}
		axis := config.ProjectJobAccounts(pr)
		kept := config.ClaudeAccountTokenNames(lc.Credentials, axis)
		for _, account := range axis {
			if slices.Contains(kept, account) {
				continue
			}
			out = append(out, ProjectJobAccount{Profile: name, Provider: pr.Provider, Account: account})
		}
	}
	// Sorted, so that the note a run computes is the same list on every run:
	// the order the profiles and the axis arrive in is a map's.
	slices.SortFunc(out, func(a, b ProjectJobAccount) int {
		if c := strings.Compare(a.Profile, b.Profile); c != 0 {
			return c
		}
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return strings.Compare(a.Account, b.Account)
	})
	return out
}

// projectJobImage is the image every shared job of this project runs: the
// project's own answer for a job that names no catalog entry and needs no tag,
// which is what catalog.JobImage gives the empty request (D1900, IS1) — the
// catalog's default entry when the project marks one, and the installation's
// base image when it marks none, which is the image these jobs were deployed
// with before the catalog decided anything.
//
// A profile has no image field (R1), so the catalog's default is the only
// place a project states which image its shared jobs run, and `fugaro update`
// writes one on the pinned base image (D2305) so that a project has an entry
// its jobs run and a place to curate a baked image into. Asking the one
// resolver here rather than spelling the base image is what keeps the
// deployer, the launcher, doctor and the image commands on one answer about
// which image a project's jobs run (F21): the shared job's image and the
// per-repository job's are two answers to the same question, and a second
// spelling of the default in this file is how they would come apart.
//
// The references are this deployer's own, exactly as repoCtx's are: the
// installation's base image stands for both the base and the repository
// reference, because a shared job belongs to no repository and has no derived
// image of its own, and an entry's image is the reference the catalog build
// pushes it to (repoCtx.catalogImage's rule, written once below).
func projectJobImage(lc *localcfg.Config) (string, error) {
	host, err := RegistryHost(lc)
	if err != nil {
		return "", err
	}
	base := lc.BaseImage(config.BaseKind)
	sel, err := catalog.JobImage(catalog.ImageRequest{Catalog: lc.ImageCatalog, Backend: catalog.Backend(lc.Backend())})
	if err != nil {
		return "", userErr("the project's image for its shared jobs: %v", err)
	}
	return sel.Image(catalog.Images{
		Base:    base,
		Repo:    base,
		Catalog: func(e config.Entry) string { return catalogEntryImage(host, gcp.SharedRegistryRepoID(), e) },
	}), nil
}

// ProjectJobImage is projectJobImage for a caller outside this package: the
// one fact the retirement of a repository's whole root asks of S10 (D2305,
// M2's structural precondition). A project whose image does not resolve has no
// shared job a repository could run on, so the retirement would take away the
// only job the repository has -- which is the whole reason the precondition is
// asked of this resolver and of no copy of it.
func ProjectJobImage(lc *localcfg.Config) (string, error) { return projectJobImage(lc) }

// catalogEntryImage is where a catalog entry's image is (D1152): a prebuilt
// entry's own reference, and otherwise the installation's one shared registry,
// one image per entry, named catalog-<entry> with the moving :latest tag a job
// spec names. It is repoCtx.catalogImage's rule and the same function, so that
// the reference a repository's job runs and the reference a shared job runs
// cannot come apart.
func catalogEntryImage(registryHost, sharedRegistry string, e config.Entry) string {
	if e.Source.Kind == config.SourceImage {
		return e.Source.Image
	}
	return registryHost + "/" + sharedRegistry + "/" + catalogImagePrefix + e.Name + ":latest"
}

// projectJobs is ProjectJobs with the named profiles carried: no job is
// composed for them, and the installation's own document carries the job it
// deployed for each instead (CarriedProjectJobs, and internal/cli's
// installSpec, which is the one caller that passes any).
//
// It is the carry of a profile this run cannot compose a job for — the
// default profile `fugaro update` wrote, in a config that has since lost the
// fact that job is composed from (its last GitHub repository moved to
// Bitbucket, so no single App ID is recorded any more). The profile stays the
// policy's: CheckProfiles judges it here with every other profile, and the
// name stays in the published policy, which is where a project whose
// workflows name none reads it — a document that left the profile out of the
// policy whole would refuse those workflows at run start instead of leaving
// the run that stops converging, which is not a trade a carry may make. What
// is left out is the JOB alone, so the plan proposes neither a creation nor a
// destruction of it, and the carried document's job is the one the
// installation already runs.
//
// The App ID (projectJobGitHubAppID) is resolved over the profiles that get a
// job and not over every profile of the config: a carried github profile
// needs no App from the config, because the job that holds its App is not
// composed here — it is the deployed one, and it runs on the ID it was
// deployed with. A profile that is NOT carried and records no ID still
// refuses, as it does today.
func projectJobs(lc *localcfg.Config, bucket string, vertex bool, carried []string) (map[string]ProjectJob, error) {
	if len(lc.Profiles) == 0 {
		return nil, nil
	}
	// The image every job below runs, resolved once: the project's own answer
	// for a job no repository states one for (projectJobImage). A config the
	// resolver refuses — a default entry tagged docker, which needs a VM
	// backend — stops here, where the job's image is written, rather than
	// deploying a job that dies on its first docker command (D1149).
	image, err := projectJobImage(lc)
	if err != nil {
		return nil, err
	}
	isCarried := func(name string) bool { return slices.Contains(carried, name) }
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
	accounts, err := projectpolicy.DeriveAccounts(profiles, lc.ClaudeAccounts, lc.ProjectSecrets, lc.PolicyRepoAccounts())
	if err != nil {
		return nil, userErr("%v", err)
	}
	if err := projectpolicy.CheckProfiles(profiles, accounts); err != nil {
		return nil, userErr("%v", err)
	}
	// The App's ID is resolved once, for the github jobs, and only when one is
	// deployed: one App serves the project (design §4.5), so a job that carries
	// the ID must carry the same one as its neighbours, and a profile of
	// another provider holds another credential (a Bitbucket job reaches git
	// through the credentials service instead). It is resolved only when some
	// github profile gets a shared job at all: a config whose profiles are all
	// Bitbucket ones deploys no github job, so it needs no App and is not
	// refused for lacking one (docs/bitbucket-workspace-token's constraint 5).
	appID := ""
	if slices.ContainsFunc(slices.Collect(maps.Values(lc.Profiles)), func(pr config.JobProfile) bool {
		return pr.Provider == githubProvider
	}) && slices.ContainsFunc(slices.Sorted(maps.Keys(lc.Profiles)), func(name string) bool {
		return !isCarried(name) && lc.Profiles[name].Provider == githubProvider
	}) {
		if appID, err = projectJobGitHubAppID(lc); err != nil {
			return nil, err
		}
	}
	out := make(map[string]ProjectJob, len(lc.Profiles))
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		// A carried profile's job is the installation's own (projectJobs'
		// header): nothing is composed for it, and CheckProfiles above has
		// already judged the profile for the policy that keeps it.
		if isCarried(name) {
			continue
		}
		// A profile of a provider no shared job serves is skipped, not
		// refused: the policy may name one — projectpolicy admits it, a
		// repository's workflow may select it, and the runner only requires a
		// selected profile to be defined — and the installation's other
		// profiles are deployed as they are. No job is deployed for it, which
		// is the fact the launcher reads the same predicate for
		// (SharedJobProvider, and internal/cli's launchProjectJobFor): a
		// repository selecting such a profile keeps its per-repository job
		// rather than being launched onto a job that does not exist.
		//
		// The list is what decides, and every provider it admits is served
		// the same way below: a Bitbucket profile's job mounts no token and
		// requires the credential proxy (D2302), and GitHub's carries the
		// App's ID. It has served both kinds config.Providers names since I1,
		// the line that put the Bitbucket path in service once the sides it
		// waited on had landed (docs/shared-jobs-default).
		if !SharedJobProvider(pr.Provider) {
			continue
		}
		// A served profile that mounts the Bitbucket credential is refused,
		// and the job is not deployed with the mount dropped instead: what an
		// operator wrote is the mount, and a job deployed without it would
		// hold no credential at all while the document says it holds one. The
		// job is shared by every repository the profile admits, and the
		// token's reach is a per-repository list such a job cannot be bounded
		// by (D2302).
		//
		// The rule lives in the policy package
		// (projectpolicy.CheckSharedJobProfile) so that its wording has one
		// home, and the operator's publish asks the same function
		// (internal/cli's refuseAProfileMountingTheBitbucketCredential, since
		// the owner's ruling of 2026-11-14): a config that would deploy this
		// job is refused at the publish, one step from the file that carries
		// the entry. This door is what holds for every node anyway — one
		// published before the ruling, and one `fugaro update`'s policy stage
		// writes after it, which converges an installation whose config already
		// mounts rather than stopping the update.
		if err := projectpolicy.CheckSharedJobProfile("profiles."+name, profiles[name]); err != nil {
			return nil, userErr("%v", err)
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
		} else {
			// The credential proxy, and the job requires it: it holds no git
			// credential of its own (D2302), so
			// providers.EnvCredentialsServiceURL is the only way its runs
			// reach git at all — internal/runner reads exactly this variable
			// to decide that a Bitbucket run is proxied, and a run whose job
			// lacks it halts at its first git command with nothing cloned. It
			// is set from the installation's own record of the service, the
			// local config's credentials_service block, which is written from
			// the installation root's outputs.
			//
			// An installation whose config does not carry the block yet is
			// NOT refused: the run that deploys the service reaches this
			// function before it writes that block (init.go's writeConfig is
			// the last step of that run), so a refusal here would stop the
			// very update that would give the job its address and tell the
			// operator to run the command they are running. The job is
			// deployed without the variable instead, and the address lands on
			// a later apply, once the run that deployed the service has
			// recorded it — an empty URL is the same case read from the
			// document side (CredentialsServiceURLOf: the apply that created
			// the service and cannot know its URL yet).
			//
			// What IS refused is the installation that will not deploy the
			// service at all, because then no apply ever fills the address in:
			// CredentialsInstallationFacts is the question the deploying stage
			// asks before it spends a registry read on the service (the first
			// statement of init_stages.go's deployCredentialsService), and an
			// installation that fails it — no Firebase database, or no
			// repository job for the service to admit — has no service to
			// reach. The sentence below carries that question's own words, so
			// the operator reads the same remedy the update's own line about
			// the service names.
			if lc.CredentialsService != nil && lc.CredentialsService.URL != "" {
				env[providers.EnvCredentialsServiceURL] = lc.CredentialsService.URL
			} else if err := CredentialsInstallationFacts(lc); err != nil {
				return nil, userErr("profiles.%s: a shared %s job holds no git credential of its own and reaches every repository it serves through the installation's credentials service (%s), and this installation cannot deploy one: %v. Deploy the service, or keep this profile's repositories on per-repository jobs",
					name, pr.Provider, providers.EnvCredentialsServiceURL, err)
			}
		}
		secretEnv, err := projectJobSecrets(lc, name, env, pr)
		if err != nil {
			return nil, err
		}
		// One job per account the profile serves (config.ProjectJobAccounts,
		// the one function the deployer, the launcher and the runner compute
		// the axis with): the profile's no-account job, whose name, service
		// account and mounts are byte for byte what this deployer built before
		// accounts existed (D9), plus one per account the profile lists. Two
		// of those values are no Claude account at all — the project's own
		// Anthropic API key (config.AnthropicKeyAccount) and the project's
		// provider key (config.ProviderKeyAccount), which a profile may list
		// and which config.ValidClaudeAccountRef admits there — and each
		// one's job is deployed like any other value of the axis: it mounts no
		// token (projectAccountMount) and its review-only variant is the one
		// the credentials service serves that credential from (D1417, F17).
		// Nothing refuses them: neither value carries a credential of the
		// installation's, so there is no mount to get wrong, and a job this
		// deployer skipped is a review the launcher would ask Cloud Run for
		// and never find. The loop is the axis itself, so the two are
		// deployed without this function naming them: it builds one job per
		// value the axis yields, whichever value that is, less the names the
		// paragraph below filters out of it.
		//
		// Each account's job gets its own copy of the profile's mounts — the
		// token of one account must never ride on another's job — its own copy
		// of the profile's environment (the redaction list below is per job),
		// and its own service account, which is the only thing that may read
		// that account's token (D2).
		//
		// The axis is the profile's own, less the names this installation
		// declares a KEY credential (config.ClaudeAccountTokenNames, the one
		// function WorkflowAccounts applies to the per-repository set): every
		// job this loop builds exists to carry its account's Claude token —
		// projectAccountMount mounts config.ClaudeAccountSecret(account) as
		// CLAUDE_CODE_OAUTH_TOKEN — and a key credential's value is stored under
		// the credential's own derived secret instead (config.CredentialSecret,
		// D1670), so nothing ever writes a version to that container and a job
		// built for the name would mount an empty token: its runs would fail in
		// the credential, and `fugaro secrets ls` would flag a MISSING name no
		// `claude setup-token` step could fill. What such a name keeps is its
		// claude_accounts entry — the reach its allow_data_to bounds, which
		// DeriveAccounts and CheckProfiles judged above and this does not move —
		// and what it does not get is a job. `fugaro accounts add` refuses to
		// put one in a profile's list (accounts_add.go, MC22), so the config
		// that reaches this is one written by hand.
		// The credentials service, when it serves this profile: every job of
		// the profile then carries it, and the module grants each account
		// roles/run.invoker on it. Computed once per profile and not per
		// account, because it is a fact about the repositories a profile
		// serves and not about the credential one of its jobs holds — and
		// through repoCredentialsService, because the module reads the same
		// two values a repository root hands its own module (name and region,
		// the URL left behind: an object conversion in Terraform drops an
		// extra attribute silently).
		served := projectJobServesTheCredentialsService(lc, pr)
		credentials := repoCredentialsService(installationCredentialsService(lc), served)
		for _, account := range config.ClaudeAccountTokenNames(lc.Credentials, config.ProjectJobAccounts(pr)) {
			jobSecrets, err := projectJobMounts(lc, name, pr, account, secretEnv, env, served)
			if err != nil {
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
			sharedJobServedEnv(jobEnv, served)
			jobEnv[runner.SecretEnvsVar] = redactionEnvs(jobSecrets)
			saID := gcp.ProjectServiceAccountID(name, pr.Provider, account)
			if err := addProjectJob(out, ProjectJob{
				Profile:        name,
				Provider:       pr.Provider,
				Account:        account,
				Job:            gcp.ProjectJobName(name, pr.Provider, account),
				ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.ProjectSADisplayName(name, pr.Provider, account)},
				Image:          image,
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
				Vertex:                  vertex,
				CredentialsService:      credentials,
				CredentialsServiceGrant: projectJobGitGrant(lc),
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
//
// Beside the variants the coding set derives, the set holds the implicit
// default review job of each provider ImplicitReviewProviders names, which an
// installation with the credentials service on has whether or not it declares a
// profile (implicitReviewVariant, the owner's requirement of 2026-11-13 for
// github and of 2026-11-14 for bitbucket): a project that publishes no profiles
// deploys no shared coding job, so no variant above covers it, and
// `fugaro review --pr N` would have nothing to run a review on for any of its
// repositories. Each is a job of the project rather than of a profile — the
// credentials service pairs a review by the repository and the policy's
// admissions (credsvc.ReviewJobAccountsFor), not by the job it ran on — so one
// job per provider serves every repository of that provider the service admits.
func ProjectReviewJobs(lc *localcfg.Config, bucket string, vertex bool) (map[string]ProjectJob, error) {
	return projectReviewJobs(lc, bucket, vertex, nil)
}

// projectReviewJobs is ProjectReviewJobs with the named profiles carried: the
// variants are derived from the coding set, so a carried profile derives none
// — its variants are the deployed ones the carry keeps beside its coding job
// (CarriedProjectJobs carries both keys), and a variant composed here for a
// job nothing composes would be a second job of a profile the config cannot
// stand behind.
func projectReviewJobs(lc *localcfg.Config, bucket string, vertex bool, carried []string) (map[string]ProjectJob, error) {
	coding, err := projectJobs(lc, bucket, vertex, carried)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ProjectJob, len(coding)+len(ImplicitReviewProviders))
	for _, j := range coding {
		if err := addReviewVariant(out, coding, reviewVariant(lc, j)); err != nil {
			return nil, err
		}
	}
	// The implicit variant's image, which no coding job of this set carries:
	// a project that declares no profile deploys no coding job for it to
	// inherit one from, and it is a shared job of this project all the same,
	// so it runs the project's own image like every other (S10, D2305). It is
	// resolved where such a variant is really built and not here, so a project
	// that deploys no project job at all asks the resolver nothing.
	for _, provider := range ImplicitReviewProviders {
		v, ok, err := implicitReviewVariant(lc, coding, bucket, vertex, provider)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if err := addReviewVariant(out, coding, v); err != nil {
			return nil, err
		}
	}
	// A set nothing is in is not a set: an installation that deploys neither a
	// coding job nor a variant (no profiles and no credentials service, the
	// shape every installation had before phase 3) leaves the key out of the
	// tfvars and its plan unchanged, which is what the nil map says.
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// addReviewVariant adds one review variant to the variant set, refusing a
// variant whose job name or service account a coding job of the same
// installation already holds.
//
// The variant's name and service account are derived over the same axis as the
// coding set, so an account whose own name ends in the review suffix collides
// with the variant of the account it names: a profile listing acme-review gets
// a coding job named exactly as the review variant of acme, and the two derive
// ONE service account — so the variant would run as a job account that mounts
// the App key, which is the whole of what it exists not to be. addProjectJob's
// check is within a set; this is the same check across the two, and it fails
// closed rather than deploying the variant onto an account that may read a
// secret.
//
// The name rule (config.ValidClaudeAccountRef, over the accounts a policy
// admits) now refuses such an account before this is reached, and this stays
// the last line: it is the only place the two sets are compared, so a config
// that arrives by another road is refused here too.
func addReviewVariant(out map[string]ProjectJob, coding map[string]ProjectJob, v ProjectJob) error {
	for name, c := range coding {
		if c.Job != v.Job && c.ServiceAccount.AccountID != v.ServiceAccount.AccountID {
			continue
		}
		return userErr("profiles.%s: the review variant of the account %q derives the job name %s and the service account %s, and the shared job %s of the account %q already holds them: a Claude account whose name ends in %q is the review variant of the account it names, and the review job would run as an account that mounts the App key. Rename that account (claude_accounts) and run fugaro update again",
			v.Profile, v.Account, v.Job, v.ServiceAccount.AccountID, name, c.Account, credsvc.ReviewVariantSuffix)
	}
	return addProjectJob(out, v)
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
		// The variant fetches the run's read-only token from the service, so
		// its account needs the front-door grant; the service's own class
		// check is what decides what it is served.
		CredentialsServiceGrant: installationCredentialsService(lc),
	}
}

// ImplicitReviewProfile is the profile segment of the installation's implicit
// default review-only jobs (implicitReviewVariant): the ONE review job a project
// has per provider whether or not it declares a profile, deployed for the
// default Claude account. (profile, provider, account) is what the credentials
// service admits a review caller by, so the three segments are the row this
// deployer writes into the service's own table and the one piece 2 of C1
// derives the service's admitted set from, and they are named here rather than
// spelled twice: the account is config.DefaultClaudeAccount, the account a
// review variant's own name spells "default" for (config.ReviewVariantAccount).
//
// It is projectpolicy.DefaultProfileName, the profile a workflow that names
// none runs on: the job serves the repositories of a project that declares no
// profile at all, so it can stand under no other name — and a project whose
// profiles are all Bitbucket ones has no profile that could name it either.
const ImplicitReviewProfile = projectpolicy.DefaultProfileName

// ImplicitReviewProviders are the provider kinds an installation deploys an
// implicit default review-only job for, in the order ProjectReviewJobs builds
// them: the two kinds config.Providers carries.
//
// It is exported because the runner's own placement set has to be the same
// kinds and cannot be computed from this package (internal/infra imports
// internal/runner for runner.SecretEnvsVar, so the arrow cannot be turned
// round): internal/runner's projectjob.go adds one implicit review candidate per
// jobProviderKinds entry, and a test beside it holds the two lists equal — a
// kind on one list and not the other is a job deployed that no run may be placed
// in, or a name the runner places that no installation deployed.
//
// The github job is the owner's requirement of 2026-11-13; the Bitbucket one is
// the owner's word of 2026-11-14 (D2383, D2385). D2383 answered that Bitbucket
// review needs no workspace token in particular — the proxy serves each request
// with the narrowest token covering the repository — so a review job that holds
// no token at all serves a Bitbucket project exactly as it serves a github one.
var ImplicitReviewProviders = []string{githubProvider, bitbucketProvider}

// ImplicitReviewJobName is the name of the implicit default review-only job of
// provider: the variant of the no-account job of ImplicitReviewProfile on it,
// which is the name implicitReviewVariant deploys the job under — reviewVariant
// over the twin it builds there, whose account is the axis's empty one, which
// config.ReviewVariantAccount spells "default".
//
// It is named for the readers that have the installation's own record or config
// in hand and not the job itself: CarriedProjectReviewJobs finds the job by this
// name in the tfvars a run deployed it into, and internal/cli's
// implicitReviewJobFor resolves a review onto it, and it is the same name the
// deployment derives rather than a second spelling of it.
func ImplicitReviewJobName(provider string) string {
	return credsvc.ReviewJobName(ImplicitReviewProfile, provider, config.ReviewVariantAccount(""))
}

// implicitReviewJobTimeout is the task timeout of the implicit default review
// job, and it is the total a repository workflow that names none is given
// (internal/config's own default, 3h, which spec.go's per-repository path adds
// backend.TaskTimeoutSlack to and turns into TaskTimeoutS): the job serves a
// profile no config declares, so nothing states a timeout_class for it, and a
// review on it is bounded exactly as a review of a repository left at the
// default is.
const implicitReviewJobTimeout = 3 * time.Hour

// implicitReviewVariant is the installation's implicit default review-only job
// for provider: the review job a project has with no shared job of its own on
// that provider, so that `fugaro review --pr N` can run on any of its
// repositories with no profile to declare and no policy to publish first (the
// owner's requirement of 2026-11-13 for github, and the owner's word of
// 2026-11-14 for bitbucket). ok is false when the installation deploys none for
// that provider, which is the ordinary answer for most of them.
//
// This is the DEPLOYMENT half of that requirement: the job, its own service
// account and the row the credentials service admits a review caller by are all
// written here. The half that resolves a review onto it is the launcher's
// (internal/cli's implicitReviewJobFor), which reads the job back out of this
// same composition — so the two cannot disagree about which job a review runs
// on, and a config this function deploys no job for is a launch the launcher
// refuses with the installation named rather than a name Cloud Run would answer
// 404 for.
//
// It exists because a review runs on a review-only variant and on nothing else:
// a project that publishes no profiles deploys no shared coding job, so there
// is no coding job to derive a variant from (ProjectReviewJobs' own loop) and no
// variant for a review to run on, whatever a repository's workflow is built
// from. The variant mounts no secret and clones the pull request head itself, so
// it holds nothing per-repository and nothing per-profile: one job per
// (provider, account) serves every repository the credentials service admits.
//
// Three things gate it, and each one is a fact rather than a preference:
//
//   - The credentials service (lc.CredentialsService, the block's presence being
//     the opt-in): a variant mounts nothing at all, so its read-only installation
//     token and its reviewer's model calls both come from the service, and a
//     project without one has nothing for the job to fetch anything from —
//     checkReviewService refuses that launch before it looks for a job.
//   - The provider's own fact, which is what a job of that provider needs to run
//     and no more. A github job runs on providers.EnvGitHubAppID and the module
//     refuses one without it, so the gate is a single recorded Github App ID
//     (recordedGitHubAppID): an installation that records none — or two, which
//     recordedGitHubAppID reports as an error — gets no implicit github job. A
//     Bitbucket job carries no provider variable at all: its git credential is
//     the credentials service's, of whichever type covers the repository (D2383
//     — the job holds no token), and the service is what the address reviewVariant
//     adds names, so its gate is only that the installation has a Bitbucket
//     repository for the job to serve (recordedBitbucketRepo). Neither gate is
//     ever a refusal: this job is deployed for nobody who asked for it, and it
//     must never be the reason an update that converges today stops converging.
//   - No coding job whose review variant IS this job. The gate is the collision
//     itself (the name and the service account addReviewVariant refuses a
//     deployment on), and it is the same predicate for the same reason: every
//     profile's accounts carry config.ProjectJobAccounts' empty segment, so a
//     coding job of this provider holds an entry whose variant is this job's own
//     triple (ImplicitReviewProfile, provider, config.ReviewVariantAccount's
//     "default") when — and only when — the profile it belongs to is
//     ImplicitReviewProfile. A profile under any other name does NOT suppress the
//     job, and that is deliberate rather than an oversight: suppressing it there
//     would take the job out of the set for an installation that already deploys
//     it, and the plan of the next update would carry its delete — which
//     RefuseProjectJobDeletes stops the whole update on, with "restore the
//     profile as it was" as the only way out. Adding a profile is an ordinary
//     act, so the job stays and the profile's own variants are added beside it.
func implicitReviewVariant(lc *localcfg.Config, coding map[string]ProjectJob, bucket string, vertex bool, provider string) (ProjectJob, bool, error) {
	if lc.CredentialsService == nil {
		return ProjectJob{}, false, nil
	}
	env := projectJobEnv(lc, bucket, vertex)
	switch provider {
	case githubProvider:
		appID, err := recordedGitHubAppID(lc)
		if err != nil || appID == "" {
			return ProjectJob{}, false, nil
		}
		env[providers.EnvGitHubAppID] = appID
	case bitbucketProvider:
		if !recordedBitbucketRepo(lc) {
			return ProjectJob{}, false, nil
		}
	default:
		// A kind this deployer deploys no implicit job for: ImplicitReviewProviders
		// is the list, and a provider added to config.Providers is not on it
		// until it is named there.
		return ProjectJob{}, false, nil
	}
	// The project's own image, asked for here and not by the caller: this is
	// the one job of a project that declares no profile, so there is no coding
	// job of the same set to inherit an image from (S10, D2305). It is asked
	// only where a job is really built — a project that deploys none must not
	// meet the resolver's refusal — and it is the only error this function has:
	// every other gate above is a fact the job is not deployed for and never a
	// refusal, because this job is deployed for nobody who asked for it and
	// must never be the reason an update that converges today stops converging.
	image, err := projectJobImage(lc)
	if err != nil {
		return ProjectJob{}, false, err
	}
	// The variant of the no-account job the default profile would have if the
	// project declared one, and a coding job is not deployed for it: reviewVariant
	// is what turns this into the review job, so the four things that make a
	// review-only job one (the suffix's names, the empty mounts, the service's
	// address and no Vertex grant) are stated in one place and not twice. Its
	// bucket and token conditions are the installation's own, which is what the
	// retitling in reviewVariant derives the variant's from.
	saID := gcp.ProjectServiceAccountID(ImplicitReviewProfile, provider, "")
	twin := ProjectJob{
		Profile:        ImplicitReviewProfile,
		Provider:       provider,
		Job:            gcp.ProjectJobName(ImplicitReviewProfile, provider, ""),
		ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.ProjectSADisplayName(ImplicitReviewProfile, provider, "")},
		Image:          image,
		CPU:            strconv.Itoa(config.DefaultCPU),
		Memory:         config.DefaultMemory,
		TaskTimeoutS:   int((implicitReviewJobTimeout + backend.TaskTimeoutSlack) / time.Second),
		Env:            env,
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
	v := reviewVariant(lc, twin)
	for _, c := range coding {
		if t := reviewVariant(lc, c); t.Job == v.Job || t.ServiceAccount.AccountID == v.ServiceAccount.AccountID {
			return ProjectJob{}, false, nil
		}
	}
	return v, true, nil
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

// githubProvider is the provider kind whose jobs carry the project's GitHub
// App ID (config.Providers' spelling, and the value config.JobProfile.Provider
// holds). It is one provider a shared job serves and not the only one: a
// Bitbucket job is built by the same loop and is told apart from this one here,
// because what a job carries differs by the provider it serves — the App's ID
// for github, the credential proxy for one whose git credential the
// installation does not hold.
const githubProvider = "github"

// bitbucketProvider is the other kind config.Providers carries, and the second
// kind ImplicitReviewProviders names. Whether a shared coding job serves it is
// SharedJobProvider's answer and not this constant's: the implicit review-only
// job is deployed for a provider either way, because it mounts no credential at
// all — its git token and its model credential both come from the credentials
// service (D2383) — so a provider no shared coding job serves gets one too.
const bitbucketProvider = "bitbucket"

// projectJobGitHubAppID is the GitHub App ID every github project job carries
// in its environment, resolved from the installation's own configuration
// (owner ruling of 2026-10-17, plan task 3.5 step 4). It is asked for the
// github jobs alone — a job of another provider holds no App key and reaches
// git another way — and it is asked for them only when one is deployed.
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
// declares a github profile before it has onboarded a GitHub repository is in:
// a github shared job runs on the github provider, and
// providers.EnvGitHubAppID is what the runner's github provider is built from,
// so a job deployed without it would halt every run it started. The fix is the
// one the per-repository path asks for at the repository stage ("is on GitHub
// and has no GitHub App ID: pass --github-app-id"): record it, in the local
// config, once.
func projectJobGitHubAppID(lc *localcfg.Config) (string, error) {
	id, err := recordedGitHubAppID(lc)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", userErr("this project's local config declares a github job profile and records no GitHub App ID: its shared project job therefore cannot be deployed. That job runs on the github provider, and the runner builds that provider from %s (the App's ID, which is not a secret), refusing to start without it — so a job deployed without it would halt every run it started. Record the App's ID by onboarding the repository the App serves (fugaro update --repo <owner/name> --github-app-id <id>), which writes it to the local config, then run fugaro update again",
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
			return "", userErr("the local config records two GitHub Apps: %s has %s and %s has %s. A shared project job runs every repository admitted to its profile on one job, and that job holds one App's ID and one github-app-key secret, so a project that uses several Apps cannot share jobs (design docs/project-centric/spec.md §4.5): put the repositories a profile admits on one App, or keep the others on per-repository jobs (a profile is what a repository with checkout: clone selects, so a profile left out of the repository's fugaro.yaml keeps its own job)",
				holder, id, name, r.GitHubAppID)
		}
	}
	return id, nil
}

// recordedBitbucketRepo reports whether the local config records a Bitbucket
// repository, which is the whole of what the implicit Bitbucket review job's
// deployment is gated on.
//
// The job carries no Bitbucket token and no provider variable: its git
// credential is the credentials service's, of whichever type covers the
// repository (D2383 — the proxy serves each request with the narrowest covering
// token), and the job's own environment names the service and nothing else. So
// the fact the deployment needs is not a credential's, which this deployer
// cannot read in any case (the bundle is the operator's, read by the
// installation stage under the operator's own identity), but whether the
// installation has a repository for the job to serve at all. A project whose
// repositories are all GitHub ones gets no Bitbucket job, exactly as a project
// that records no GitHub App ID gets no github one.
//
// The provider is read from the entry, the way recordedGitHubAppID reads it: an
// entry that names none is a repository whose kind only its own fugaro.yaml
// states, and a job deployed on that silence would be a job for a repository
// this deployer cannot name. `fugaro update --repo` writes the field from the
// checkout's own document (init.go's writeRepoConfig), so an onboarded
// repository carries it.
func recordedBitbucketRepo(lc *localcfg.Config) bool {
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if lc.Repos[name].Provider == bitbucketProvider {
			return true
		}
	}
	return false
}

// SharedJobProvider reports whether a shared project job serves that git
// provider kind, and it is the one place the answer lives: the deployer
// (ProjectJobs, which skips a profile of a provider it does not serve) and the
// launcher (internal/cli's launchProjectJobFor, which keeps such a repository
// on its per-repository job) both ask it, so the job a launch computes is
// always a job this package deployed.
//
// A skipped profile is not a refused one: the profile is a document the policy
// carries, and skipping it here is what makes "no job is deployed for it" true
// rather than an installation `fugaro update` refuses whole.
//
// Both provider kinds a shared job serves are here since I1
// (docs/shared-jobs-default): the Bitbucket path is built and tested (a job
// that mounts no token and requires the credential proxy, D2302), and the
// tasks of its slice merged behind this list so that a release cut midway
// changed nothing for a Bitbucket project. Admitting a provider here is the
// one-line change that puts its path in service — a provider admitted here is
// served by that path, and this predicate is what tells the launcher the job
// it computed exists.
//
// Every kind of config.Providers is answered — the list is the kinds a
// repository may use — and the test beside this file pins that the two agree,
// so a provider added to that list is not silently given a shared job by a
// forgotten line here.
func SharedJobProvider(provider string) bool {
	return slices.Contains(sharedJobProviders, provider)
}

// installationCredentialsService is the installation's credentials service as
// THIS package knows it while it builds the installation root's own tfvars: the
// name the root creates it under (CredentialsServiceName, the one literal
// internal/backend/gcp writes into var.credentials_service.service, and the name
// the deployment derives rather than a second spelling of it) and the
// installation's region. The URL is not built here: Cloud Run assigns it, the
// module's variable carries no such attribute, and the deployer never needs it —
// a job is told the address at launch, from the local config's own record of the
// service (internal/cli's credentialsServiceLaunchEnv).
//
// nil for an installation whose local config carries no block, which is the
// opt-in (D147, and the same gate implicitReviewVariant reads): with no service
// there is nothing to grant an account roles/run.invoker on.
func installationCredentialsService(lc *localcfg.Config) *CredentialsService {
	if lc == nil || lc.CredentialsService == nil {
		return nil
	}
	return &CredentialsService{Name: CredentialsServiceName, Region: lc.Region}
}

// projectJobServesTheCredentialsService is the profile's switch: whether this
// profile's shared jobs reach the installation's credentials service, and
// therefore whether the module grants each of their accounts roles/run.invoker
// on it (B5.2, D505, G17) and whether those jobs give up the model credentials
// the service serves their runs (B5.7, config.SharedJobServedClasses): the
// grant arrived one release before the drop, which is G17's order — no mount is
// lost before the service serves that credential to the image the job runs.
//
// Two conditions, and the second is what a profile's auth is on this side of
// the wire. There is no auth in a profile's own declaration — a profile names a
// provider, a size, a timeout and its mounts, and WHICH auth its runs use is a
// fact about each repository's fugaro.yaml — so the deployer reads it where the
// installation records it: localcfg.Repo.CredentialsServiceReady, the record a
// repository's own root is applied under, written as
// CredentialsServiceServes(installed, bundle is ready, that repository's auth)
// (internal/cli's recordCredentialsServiceReady) and backfilled for every entry
// from the installation's own state on each `fugaro update`. A record that says
// on is therefore a repository whose auth is one the service serves AND whose
// root an apply built for it, which is the same pair the launcher judges a
// launch with; a record that says off, or no record at all, is a repository the
// service does not serve.
//
// The installation "has the service" as the block's presence and not as a URL:
// the config records the block on the apply that creates the service, and Cloud
// Run assigns the URL on that same apply, so an entry can hold the block with an
// empty URL for the length of one run — and the grant this predicate drives
// needs the name and the region alone. The launcher's own answer needs the URL
// and withholds the address until it is there, so the two never disagree in the
// direction that matters (a launch told about a service its job has no grant on).
//
// Which repositories are this profile's is the policy's own admission read
// against the config's entries: a repository of the profile's provider — one
// job serves one provider, so a repository of another provider is never
// admitted to it (projectpolicy.Admits) — that the profile's own allow_repos
// admits when it names one, and that the project's enrollment admits, which is
// what the config's own entries are (they are what the published allow_repos is
// seeded from, internal/cli's seedAllowRepos).
//
// One repository the profile serves being on is enough, and it is deliberately
// the wider answer of the two a reader might expect. The narrower one — every
// repository of the profile on — would leave a `cascade` repository of a
// profile that also serves an `oauth` one without the grant its launch needs:
// the launch hands such a repository the service's address (the launcher reads
// ITS record, live), and a job account without the invoker bit fails every one
// of its calls at the front door. A grant on a job whose other repositories are
// not switched is inert: nothing hands those runs the address, and the service
// refuses a caller it does not admit whatever IAM says (Q6).
//
// It is one impure function on purpose — it is the only place the deployer
// reads the installation's own records — and ProjectJobs' "nothing here reads a
// repository" stays true of it: it reads what the config RECORDS about the
// repositories, never a repository's files, and the job set (one job per
// profile, provider and account) does not move with the answer.
func projectJobServesTheCredentialsService(lc *localcfg.Config, pr config.JobProfile) bool {
	return CredentialsServiceServesProfile(lc, pr)
}

// CredentialsServiceServesProfile is that same answer for a reader outside the
// deployer, and there is exactly one: the launcher, which judges a launch on a
// shared project job with the answer its drop was taken on
// (internal/cli's credentialsServiceLaunchEnv, B5.7). The drop is the
// profile's — a shared job is ONE job for every repository a profile serves,
// so one switched repository takes the model mounts off every repository's
// runs on it — and the address each of those runs is handed has to be the
// profile's for the same reason: judged per repository, a repository of a
// served profile whose own record says off (or whose document the launching
// machine cannot read at all) would be handed no address and would have no
// mount to fall back on either, and its run would die at its first model call
// (G17, D143). So this is the deployer's function, exported, and never a
// second spelling of it: the two answers are one answer, and a profile the
// switch holds off keeps every mount and every accessor on both sides (G9,
// D147).
//
// The launcher asks it of the same two facts the deployer does — the config's
// per-repository records and the profile's own definition — and of no third
// one: a launch on a shared job is judged by the profile even where this
// machine cannot read the repository's document at all, which is a state the
// per-repository path does need a document for. What it does beside this answer
// is not a second one: the installation's URL has to be there to hand anything
// at all, and a run whose auth the service does not serve is told nothing
// whatever the profile says, because it reads none of the service's credentials
// and the address would move its git traffic onto a mint it does not need.
func CredentialsServiceServesProfile(lc *localcfg.Config, pr config.JobProfile) bool {
	if lc == nil || lc.CredentialsService == nil {
		return false
	}
	for _, key := range slices.Sorted(maps.Keys(lc.Repos)) {
		entry := lc.Repos[key]
		if entry.Provider != pr.Provider || !entry.CredentialsServiceOn() {
			continue
		}
		canonical, err := projectpolicy.Canonical(entry.Provider, key)
		if err != nil {
			continue
		}
		if len(pr.AllowRepos) > 0 && !projectpolicy.AdmitRepo(pr.AllowRepos, canonical) {
			continue
		}
		return true
	}
	return false
}

// RefreshProjectJobCredentialsService re-derives every job's
// credentials_service — and nothing else about it — from the config as it
// stands now, and returns the set carrying that answer.
//
// It exists because the answer is not a fact of the run's inputs alone: it is
// read off the local config's per-repository records
// (projectJobServesTheCredentialsService), and those records MOVE inside a run
// that applies the installation root. Two writers do that, and both are later
// than the moment the run's options were read: the upgrade backfill, which
// writes the entries the field did not exist for at the end of the installation
// stage (internal/cli's install), and the repository stage, which writes the
// entry of the checkout it converges before it plans that checkout's root.
//
// A set derived once is therefore behind the records the moment either has
// written, and the apply that follows would deploy the shared job of a profile
// the records have just switched with no roles/run.invoker grant on the
// service — while the launcher, which reads the same records live, hands that
// job the service's address. The service refuses a caller without the grant at
// its front door and a cascade run has nothing to fall back to (D143), so the
// two have to converge in ONE run (CLAUDE.md's HARD RULE): the callers
// re-derive immediately before the tfvars that plan the root.
//
// The set's KEYS never move — this is the same jobs, one per (profile,
// provider, account) — so every field a job is more than its switch stays what
// the first derivation built. A job whose profile the config no longer declares
// is answered off: there is no profile left for a repository to be served
// through.
//
// What moves with the switch is the mounts it takes away, and they are
// re-derived beside it (refreshProjectJobMounts, B5.7's flip): the switch is
// the same condition the class list's drop is read behind, so a set where the
// switch moved and the mounts did not is a job carrying neither the model
// credential an On record had taken away nor an address to fetch one from —
// a job whose every run dies at its first model call (G17, D143). The
// re-derivation is exactly the derivation: a refreshed set equals the set the
// same config composes from scratch (TestTheProjectJobsSwitchFollowsTheRecordsOfTheRun).
func RefreshProjectJobCredentialsService(jobs map[string]ProjectJob, lc *localcfg.Config) map[string]ProjectJob {
	if len(jobs) == 0 || lc == nil {
		return jobs
	}
	out := maps.Clone(jobs)
	for name, job := range out {
		pr, declared := lc.Profiles[job.Profile]
		served := declared && projectJobServesTheCredentialsService(lc, pr)
		job.CredentialsService = repoCredentialsService(installationCredentialsService(lc), served)
		job.CredentialsServiceGrant = projectJobGitGrant(lc)
		if declared {
			job = refreshProjectJobMounts(job, pr, lc, served)
		}
		out[name] = job
	}
	return out
}

// refreshProjectJobMounts re-derives one job's mounts — and the redaction list
// that mirrors them — from the config as it stands, and returns the job
// carrying them.
//
// A config that no longer resolves the profile's own mounts leaves the job
// exactly as it was: a mount that stays is a mount the job's account may still
// read, while a derivation that guessed in the other direction would leave a
// container nothing mounts and a credential nothing carries. The error itself
// is the deployer's to print, and it has already printed it — the config that
// reaches here was composed by ProjectJobs, which refuses one it cannot resolve
// (projectJobSecrets) before any job exists to refresh.
//
// The environment handed back is the job's own, less the redaction list, which
// IS the mounts (the same pair ProjectJobs writes): a stale list would leave a
// run publishing the value of a variable it no longer carries, or hiding one it
// does.
func refreshProjectJobMounts(job ProjectJob, pr config.JobProfile, lc *localcfg.Config, served bool) ProjectJob {
	env := maps.Clone(job.Env)
	delete(env, runner.SecretEnvsVar)
	profileSecrets, err := projectJobSecrets(lc, job.Profile, env, pr)
	if err != nil {
		return job
	}
	mounts, err := projectJobMounts(lc, job.Profile, pr, job.Account, profileSecrets, env, served)
	if err != nil {
		return job
	}
	env[runner.SecretEnvsVar] = redactionEnvs(mounts)
	sharedJobServedEnv(env, served)
	job.SecretEnv = mounts
	job.Env = env
	return job
}

// ProjectJobsCredentialsServiceStale reports whether the switch on these jobs
// answers differently from what the config now derives — whether a set derived
// before a record moved is still what a run holds, and the grant the records ask
// for has therefore not been applied.
//
// It is the credentials stage's own question (internal/cli): that stage is the
// run's last apply of the installation root, so it is where a grant a mid-run
// record write made necessary either lands or waits for a second `fugaro
// update` — which is the command the owner is not asked to run twice.
func ProjectJobsCredentialsServiceStale(jobs map[string]ProjectJob, lc *localcfg.Config) bool {
	refreshed := RefreshProjectJobCredentialsService(jobs, lc)
	for name, job := range jobs {
		if credentialsServiceKey(job.CredentialsService) != credentialsServiceKey(refreshed[name].CredentialsService) {
			return true
		}
	}
	return false
}

// credentialsServiceKey is the switch as one comparable value: the two fields
// the module reads off it, or "" for a job the service does not serve.
func credentialsServiceKey(s *CredentialsService) string {
	if s == nil {
		return ""
	}
	return s.Name + "\x00" + s.Region
}

// sharedJobProviders are the provider kinds a shared project job serves
// (SharedJobProvider). It is a list rather than a comparison so that the test
// can walk it, and it holds kinds of config.Providers. Bitbucket joined it in
// I1, once the credentials service paired a shared job (S2), the runner ran
// one (S3) and the launcher chose one (S4) — a provider admitted before those
// landed would deploy jobs nothing could run on.
var sharedJobProviders = []string{"github", "bitbucket"}

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
// environment: its identity, the budget backend's address and web key, and the
// mode the installation's own config names, and nothing else. ProjectJobs adds
// the two values that follow from the profile and the job — the GitHub App's
// ID (non-secret, owner ruling of 2026-10-17) and runner.SecretEnvsVar, the
// list of variables that job's secrets are mounted as.
//
// It is deliberately *not* budgetEnv, which sets per-repository values too
// (the model routes, the price overrides and the model allow-list, each cut to
// one repository): a project job is shared, so those are the published
// policy's, and a run reads them from the node (internal/runner's
// resolvePolicy) rather than from its own environment, which a launcher would
// otherwise be able to write — a route there names the host a repository's
// code and the profile's provider key go to, and a price is the cap's own
// arithmetic. The token ceiling and the cap are the node's the same way. What
// stays is what belongs to the installation (D11, plan
// task 1.4 step 1): a job deployed without the two backend variables cannot
// read the project's policy at all, and the mode it names is the
// installation's own — the fallback for a run whose policy node is absent
// (AB5, F20, P3), while the mode a run really runs under is the published
// policy's, which is stricter.
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
	// A shared job names its mode too (AB5, F20): no launcher puts one on the
	// execution, so a job without the entry takes its mode from the published
	// policy alone, and an older image reads a missing entry as off — the mode
	// this release removed. The mode named here is the installation's, the same
	// one its own config gives its jobs: a repository admitted to the profile
	// cannot put its own on a job every repository of the profile shares.
	env[BudgetModeEnv] = policy.Mode(lc.BudgetMode()).Effective()
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
// not create (design docs/project-secrets/spec.md §4). That is also why
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
// The Bitbucket token is judged before this function is reached, and not here.
// Both halves of it are rules about the profile and not about its mounts: a
// profile that mounts it and serves another provider is refused by
// validBitbucketProfile, which CheckProfiles applies to every profile before a
// job is built, and a profile a shared job serves that mounts it is refused by
// projectpolicy.CheckSharedJobProfile, which ProjectJobs asks of every profile
// the shared-job list admits. A third copy here could never be reached — the
// two rules above name every profile that carries the entry — and an earlier
// revision's was exactly that: dead, and read as live by anyone who found it.
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

// projectJobMounts is ONE job's own mount set: the profile's mounts
// (projectJobSecrets), plus the account's token (projectAccountMount), less
// everything the credentials service serves this profile's repositories
// (config.SharedJobServedClasses, B5.7).
//
// It is a function of its own because the answer depends on the switch, and
// the switch moves INSIDE a run: the set has to be re-derivable from the
// config as it stands then, which is what RefreshProjectJobCredentialsService
// does with it — a job whose switch went off keeps the mounts an On record had
// taken away, or it runs with neither a credential nor an address to fetch one
// from (G17, D143).
//
// served is the profile's own switch, read once per profile by the caller
// (projectJobServesTheCredentialsService) and handed to both halves. Both are
// needed to move a mount: the class list alone would take the credential from a
// profile the service does not serve, and the switch alone from a profile whose
// class is not listed yet — which is the same pair of clauses the module's
// local.is_shared_job_served_model spells, and they have to answer the same per
// variable, or a job keeps an accessor it never uses or loses one its container
// still mounts (C2.3). A class the list does not carry leaves every mount
// exactly where it is, whatever the switch says, so an empty list is a set
// derived as it was derived before the flip (D147).
func projectJobMounts(lc *localcfg.Config, profile string, pr config.JobProfile, account string, secretEnv, env map[string]string, served bool) (map[string]string, error) {
	mounts := maps.Clone(secretEnv)
	if served {
		for v := range mounts {
			if config.IsSharedJobServedModelEnv(v) {
				delete(mounts, v)
			}
		}
	}
	if err := projectAccountMount(lc, profile, pr, account, mounts, env, served); err != nil {
		return nil, err
	}
	return mounts, nil
}

// sharedJobServedEnv records C1's fact "this job's credentials are served" on a
// shared job's own environment (config.CredentialsServedEnv, C1 (h)), or clears
// it, from the profile's switch — and it is the same pair of clauses the mounts
// move by, which is the point of it being one function.
//
// The runner's door on the credential a shared job holds reads this fact and
// nothing else about that credential (internal/runner's projectJobServed): a
// run of a served job chooses no model credential from agent.auth or from the
// account its name carries, because the container holds none — which credential
// serves a call is the credentials service's answer, walked from the project's
// routing table (R2). So a job whose fact is set while it still mounts the
// account's token is a run whose account nobody checks, and a job whose mounts
// are gone without the fact is a run reading its own auth for a credential the
// container does not hold: the two move together (G17, D143), and this is where
// they are held to it.
//
// served is the profile's own switch, and the second clause is the fail-closed
// one rather than a restatement of the first: the mounts move per variable by
// the class list (IsSharedJobServedModelEnv). With that list empty — the inert
// state D147's "with it off nothing changes" is — a switched profile keeps
// every mount, and the fact must not be set.
func sharedJobServedEnv(env map[string]string, served bool) {
	if served && config.SharedJobServesClass(config.SharedJobClassModel) {
		env[config.CredentialsServedEnv] = config.ServedValue
		return
	}
	delete(env, config.CredentialsServedEnv)
}

// projectAccountMount adds the one secret_env entry that delivers an account
// job's Claude OAuth token: CLAUDE_CODE_OAUTH_TOKEN → the account's own
// logical secret (config.ClaudeAccountSecret). It is the carrier the
// per-repository path already uses (internal/infra's SecretMounts, `case
// "oauth"`), so the project level has no second delivery path to maintain
// (design project-named-oauth-tokens §4.3, D1).
//
// It is deliberately ONE function, and it is the seam the security service
// replaces (§4.6). The credentials proxy has landed (B5.1, B5.2) and this is
// where its flip arrives: served — the profile's own switch, B5.7's condition —
// stops the entry being added, and nothing else moves: not the job's name (a
// function of
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
// The account axis's non-account values (config.AnthropicKeyAccount, the
// project's own Anthropic API key, and config.ProviderKeyAccount, the project's
// provider key) are entries of config.ProjectJobAccounts like any other, and
// the arm below is where they are answered: their jobs mount no token, because
// each credential is held outside the installation — the platform's own key,
// and the credentials service's provider key — and the container lookup this
// function is otherwise made of would answer them with
// claude_accounts.<value>.allow_data_to, a key config.ValidateClaudeAccounts
// refuses.
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
func projectAccountMount(lc *localcfg.Config, profile string, pr config.JobProfile, account string, secretEnv, env map[string]string, served bool) error {
	// The axis's non-account values are the project's own Anthropic API key and
	// the project's provider key (config.AnthropicKeyAccount,
	// config.ProviderKeyAccount): each names a credential held outside the
	// installation's own mounts, so its job mounts no token and the container
	// lookup below is not its question — the value's logical name has no
	// container to find, and the lookup's answer would prescribe a
	// claude_accounts key config.ValidateClaudeAccounts refuses. The arm stands
	// ahead of the lookup for that reason, as it did when these values were
	// refused outright: the lookup's answer is a remedy nobody can carry out.
	// Each one's review-only variant mounts nothing at all (reviewVariant),
	// which is what makes the values deployable: the class's calls are the
	// credentials service's, and the credentials it serves them with never ride
	// a job (D1417, O1).
	if config.AxisNonAccountValue(account) {
		return nil
	}
	if account == "" && !slices.Contains(pr.ClaudeAccounts, config.DefaultClaudeAccount) {
		return nil
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
	// The S5 flip (B5.7): a profile the credentials service serves mounts no
	// model credential, and this one is its own account's — the run's model
	// calls are the service's for this profile's repositories (B5.1, B5.2), so
	// the container this function used to name is the service's to read. The
	// arm stands after the checks above and before the assignment, so a profile
	// whose account has no project-wide copy is still refused here rather than
	// deployed onto a container the service could not read either.
	//
	// It is a no-op while the class list is empty, which is the state this
	// function is in before the flip's second commit, and for an account the
	// class does not cover (a profile's own mounts are projectJobMounts'
	// half of the same rule).
	if served && config.IsSharedJobServedModelEnv(v) {
		return nil
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
