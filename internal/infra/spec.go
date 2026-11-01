// Package infra computes every name, label, env and IAM condition of an
// installation and its repositories, once, from the local config and a
// checkout's fugaro.yaml. Terraform receives them all through the tfvars
// this package writes; the HCL derives nothing (design §8.2).
package infra

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/task"
)

// UserError is a problem with the inputs that the user can fix (exit 1).
type UserError struct{ Err error }

func (e *UserError) Error() string { return e.Err.Error() }
func (e *UserError) Unwrap() error { return e.Err }

func userErr(format string, a ...any) error { return &UserError{Err: fmt.Errorf(format, a...)} }

// Env variables the spec sets beyond the M4 ones.
const (
	// CheckSpecEnv holds the check job's CheckJobSpec, as JSON.
	CheckSpecEnv = "FUGARO_CHECK_SPEC"
	// ComputePricesEnv is <vcpu>,<gib> per second, from the local config's
	// override for the region, so the runner's report agrees with ls.
	ComputePricesEnv = "FUGARO_COMPUTE_PRICES"
	// The project's budget, which the runner reads (runner.SpendFromEnv):
	// the mode, the per-run cap in US dollars, and the price overrides.
	BudgetModeEnv     = runner.BudgetModeEnv
	MaxRunUSDEnv      = runner.MaxRunUSDEnv
	ModelPricesEnv    = runner.ModelPricesEnv
	ModelProvidersEnv = runner.ModelProvidersEnv
	// The ceiling's token cap and allow-list of models, set on every
	// workflow job whatever the mode.
	MaxRunTokensEnv  = runner.MaxRunTokensEnv
	AllowedModelsEnv = runner.AllowedModelsEnv
	// The budget backend (M9b R10): the project's Realtime Database and the
	// restricted web API key (not a secret) that signs a run's token in go to
	// every job whenever the installation has them, whatever the mode (D11,
	// plan task 1.4 step 1); how long the backend may be unreachable before
	// the run halts is set only when the config sets one, and only when the
	// mode is not off.
	RTDBURLEnv        = runner.RTDBURLEnv
	FirebaseAPIKeyEnv = runner.FirebaseAPIKeyEnv
	BudgetGraceEnv    = runner.BudgetGraceEnv
	// CheckoutEnv, set to the workflow's own config.Checkout value on a
	// checkout: clone or checkout: shared workflow's job (never on a baked
	// one, whose spec stays exactly what it was before clone existed), tells
	// the runner's bootstrap to make a blobless clone at start instead of
	// moving a baked-in one, and to install the checkout's mise tools
	// (design generic-tool.md §2, G4; plan Task 17). A shared job needs it
	// for the same reason a clone job does: its image holds the dependency
	// layer alone, so the source can only come from the clone (design
	// forge-authorship-images/shared-images.md §1, §6). The runner reads it,
	// so the constant lives there (runner.CheckoutEnv), the same way the
	// budget variables do.
	CheckoutEnv = runner.CheckoutEnv
	// RemoteEnv, set to the repository's credential-free clone URL on a
	// checkout: clone or checkout: shared workflow's job (and on no other):
	// the job's image carries no checkout, so fugaro exec clones one from
	// this URL — its --remote flag defaults to it (runner.RemoteEnv).
	// Without it the clone job would have no remote to clone at all (review
	// C1).
	RemoteEnv = runner.RemoteEnv
)

// The installation-identity env vars platformEnv and the vertex branch of
// workflow set: the installation's own bucket, backend kind, GCP project,
// Fugaro project name and region, and, for agent.auth: vertex, the same GCP
// project and region again under Vertex's own names. They come from the
// local config too, but changing them means moving the installation
// itself, not editing a provider, a price or a budget cap.
const (
	BucketEnv        = "FUGARO_BUCKET"
	BackendEnv       = "FUGARO_BACKEND"
	GCPProjectEnv    = "FUGARO_GCP_PROJECT"
	ProjectEnv       = "FUGARO_PROJECT"
	RegionEnv        = "FUGARO_REGION"
	VertexRegionEnv  = "CLOUD_ML_REGION"
	VertexProjectEnv = "ANTHROPIC_VERTEX_PROJECT_ID"
)

// PlatformEnv is every key above, together: fugaro upgrade's job-config-copy
// check (internal/cli/upgrade_cloud.go) excludes exactly this set, named
// here from the same constants platformEnv and the vertex branch use, so it
// never has to guess it or drift from them.
var PlatformEnv = []string{BucketEnv, BackendEnv, GCPProjectEnv, ProjectEnv, RegionEnv, VertexRegionEnv, VertexProjectEnv}

// githubGitUser is the HTTPS username of a GitHub App installation token.
const githubGitUser = "x-access-token"

// Inputs are what a repository's spec is computed from.
type Inputs struct {
	LC   *localcfg.Config
	Repo string // owner/name
	Cfg  *config.Config
	// RepoURL is the https clone URL; empty means the provider's usual one.
	RepoURL string
	// FileSetsBaseBranch is whether Cfg's own fugaro.yaml writes
	// git.base_branch itself (config.SetsGitBaseBranch), which is what puts
	// the file's value above the local config's entry and the project's
	// default (config.EffectiveBaseBranch). A resolved config cannot answer
	// it — Resolve fills main in, so a file that writes nothing and one that
	// writes main resolve alike — so a caller that read the file passes the
	// answer, true or false. False is not the same as nil: it says the file
	// names no base and the local entry below it is the spec's, which is what
	// a caller reads head's file for.
	//
	// The field is a pointer because there is a third answer: a caller that
	// did not read the file at all. nil means the file decides — it is the
	// same as FileSetsBaseBranch pointing at true — so such a caller gets
	// Cfg's own resolved value, main included. That is *not* what this spec
	// computed before the field existed, where a non-empty repos.<r>.base_branch
	// entry won over Cfg's value: a caller holding only a resolved config and
	// a local entry that differs must pass false, or the entry is lost.
	FileSetsBaseBranch *bool
	// GitHubAppID overrides the local config's github_app_id.
	GitHubAppID string
	// Installation are the installation root's outputs. Fields left empty
	// take the values Go gives a new installation.
	Installation InstallationOutputs
	// Policy is the published policy node, as the caller read it
	// (internal/cli's policyReadOnce, over budget.rtdb_url): whether a
	// repository is proxied has ONE source, this node's bitbucket_token
	// block (G15), and RepoSpec.BitbucketProxied is read from it and from
	// nothing else.
	//
	// nil means the caller read no node at all: then nothing is proxied and
	// no local project_secrets entry is judged against a document this
	// caller never saw — a command that does not read the node (fugaro
	// image, fugaro doctor, --print-vars) must not refuse one over it. A
	// caller that DID read the node passes what it read, and an empty Policy
	// when the project has published none: "read, and the node admits
	// nothing" is a different answer from "not read", and only the first one
	// refuses a local entry that proxies a repository the node does not.
	Policy *projectpolicy.Policy
}

// InstallationOutputs are the installation root's outputs, as `terraform
// output -json` names them.
type InstallationOutputs struct {
	// ProjectName is the Fugaro project's name; empty in the outputs of an
	// installation applied before M9a.
	ProjectName  string `json:"project_name"`
	RunsBucket   string `json:"runs_bucket"`
	RegistryHost string `json:"registry_host"`
	BaseRegistry string `json:"base_registry"`
	GhcrRegistry string `json:"ghcr_registry"`
	// SharedRegistry is the installation's one shared image registry: the
	// Artifact Registry repository every repository's shared images live in
	// (design docs/forge-authorship-images/shared-images.md §2.6). Empty in
	// the outputs of an installation applied before it existed, which
	// withDefaults fills with the name Go gives a new one.
	SharedRegistry          string   `json:"shared_registry"`
	LegacyRegistry          string   `json:"legacy_registry"`
	SchedulerServiceAccount string   `json:"scheduler_service_account"`
	RoleIDs                 RoleIDs  `json:"role_ids"`
	Launchers               []string `json:"launchers"`
	Operators               []string `json:"operators"`
	LogView                 string   `json:"log_view"`
	RegistryCleanupDryRun   *bool    `json:"registry_cleanup_dry_run"`
	// HistoryServiceAccount and HistoryJob are null without a budget
	// backend: the sweeper's account (which the Firebase root grants its
	// roles) and its Cloud Run job.
	HistoryServiceAccount string `json:"history_service_account"`
	HistoryJob            string `json:"history_job"`
	// CoordinatorServiceAccount and CoordinatorJob are the same pair for the
	// aggregation job (P5, D194): the account the Firebase root grants the
	// FP's database admin to, and the job the schedule starts. Both are null
	// unless the owner opted in (fugaro update --coordinator), which no
	// installation has by default.
	CoordinatorServiceAccount string `json:"coordinator_service_account"`
	CoordinatorJob            string `json:"coordinator_job"`
	// CredentialsService is the installation's credentials service, or null
	// while it has not opted in (D146, D147) — which is every installation
	// today, and the reason nothing here changes until an owner runs C1.20's
	// stage. The repository roots read the same object from the tfvars'
	// installation.credentials_service (RepoInstallation).
	CredentialsService *CredentialsService `json:"credentials_service"`
}

// CredentialsService is the installation's credentials service (D146): the
// Cloud Run service that alone holds the model credentials. One type serves
// both ends of the contract — the installation root's output of that name and
// the repository root's installation.credentials_service — because both carry
// the same two values.
//
// Null means the installation has not opted in (D147), and null is what every
// reader gates on: with it off, every job keeps its model-secret accessors,
// no job account holds an invoker grant, and no run reaches a service.
type CredentialsService struct {
	// Name is the Cloud Run service's name, Region the region it runs in.
	Name   string `json:"name"`
	Region string `json:"region"`
	// URL is the service's own URL — the audience its callers' Google ID
	// tokens are minted for (C1.14) — and it is the INSTALLATION root's: the
	// module's output carries it (outputs.tf, the service resource's uri) and
	// the init stage writes it into var.credentials_service on the apply
	// after the one that created the service. A repository root reads the
	// first two fields and nothing else, so the object it writes into its own
	// tfvars is built without it (repoCredentialsService).
	URL string `json:"url,omitempty"`
}

// repoCredentialsService is the installation's credentials service as a
// REPOSITORY root reads it: the name and the region the workflow module turns
// into each job account's roles/run.invoker grant. The URL is left behind
// because the module's variable carries no such attribute — an object
// conversion in Terraform silently DROPS an extra attribute rather than
// refusing it, so passing it would read as a value the repository root agreed
// on while nothing used it.
func repoCredentialsService(s *CredentialsService) *CredentialsService {
	if s == nil {
		return nil
	}
	return &CredentialsService{Name: s.Name, Region: s.Region}
}

// RoleIDs are the custom roles' full names, projects/<p>/roles/<id>.
type RoleIDs struct {
	Launcher       string `json:"launcher"`
	JobRunner      string `json:"job_runner"`
	BuildSubmitter string `json:"build_submitter"`
	// TagMover is empty in the outputs of an installation applied before
	// the role existed; fugaro update refuses those.
	TagMover string `json:"tag_mover"`
}

// ServiceAccount is an account to create or adopt.
type ServiceAccount struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
}

// Condition is an IAM condition, compared byte for byte with the live one.
type Condition struct {
	Title      string `json:"title"`
	Expression string `json:"expression"`
}

// WorkflowSpec is one (repository, workflow): its job, job account and
// grants. The tagged fields are the tfvars' repo.workflows.<name>.
type WorkflowSpec struct {
	Job             string            `json:"job"`
	ServiceAccount  ServiceAccount    `json:"service_account"`
	Image           string            `json:"image"` // tagged :latest
	CPU             string            `json:"cpu"`
	Memory          string            `json:"memory"`
	TaskTimeoutS    int               `json:"task_timeout_s"`
	Env             map[string]string `json:"env"`
	SecretEnv       map[string]string `json:"secret_env"` // env var → logical secret name
	BucketCondition Condition         `json:"bucket_condition"`
	// TokenCondition is the job account's read-only grant on its own
	// repository's budget token objects, tokens/<slug>/
	// (gcp.TokenBucketCondition; budget-token-channel.md §7.2, D16).
	TokenCondition Condition `json:"token_condition"`
	Vertex         bool      `json:"vertex"`
	DeployJob      bool      `json:"deploy_job"`

	Name                string            `json:"-"`
	Slug                string            `json:"-"`
	ServiceAccountEmail string            `json:"-"`
	Labels              map[string]string `json:"-"` // on the job
	// SecretIDs are the secret IDs of the logical secrets the job mounts.
	SecretIDs map[string]string `json:"-"`
	// GitSecret is the logical name of the provider credential.
	GitSecret string `json:"-"`
	// BuildSecrets are the logical secrets the workflow's image build
	// mounts: the provider credential and the workflow's secrets, sorted.
	BuildSecrets []string `json:"-"`
	// LegacyImage is the M4 image path in the local config's legacy
	// registry, untagged as M4 printed it; empty without one.
	LegacyImage string `json:"-"`
	// Checkout is the workflow's config.Checkout (baked, clone or shared):
	// clone runs the installation's base image directly (Image is
	// base_images.base, not a derived image), with no per-repository build
	// or daily check; shared runs a shared dependency-layer image and
	// clones the repository's own source at launch (design
	// forge-authorship-images/shared-images.md §6, route A).
	Checkout string `json:"-"`
}

// Registry is the repository's own image registry.
type Registry struct {
	RepositoryID  string `json:"repository_id"`
	CleanupDryRun bool   `json:"cleanup_dry_run"`
}

// CheckSpec is the repository's daily image check job and its schedule.
type CheckSpec struct {
	Job             string `json:"job"`
	Image           string `json:"image"`
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
	Schedule        string `json:"schedule"`
	Paused          bool   `json:"paused"`
	// DeployJob is the readiness gate of the check job, its invoker grant
	// and its Scheduler job.
	DeployJob bool              `json:"deploy_job"`
	Env       map[string]string `json:"env"`
	SecretEnv map[string]string `json:"secret_env"`

	// Workflows are the ones the check covers (rebuild.check: daily).
	Workflows []string `json:"-"`
}

// CheckJobSpec is what the check job reads from CheckSpecEnv.
type CheckJobSpec struct {
	Repo                string   `json:"repo"`
	Provider            string   `json:"provider"`
	RepoURL             string   `json:"repo_url"`
	BaseBranch          string   `json:"base_branch"`
	Workflows           []string `json:"workflows"`
	Registry            string   `json:"registry"` // <host>/<repository ID>
	BuildServiceAccount string   `json:"build_service_account"`
	MachineType         string   `json:"machine_type"`
	BuildRegion         string   `json:"build_region"`
	// BaseImages are the base images of the checked workflows' base kinds
	// (kind -> image): what a rebuild the check submits builds FROM.
	BaseImages map[string]string `json:"base_images"`
	// GitSecretID is the secret ID of the provider credential as Readiness
	// resolved it for this repository (the repository's own copy, or the
	// project-wide one; design docs/design/project-secrets.md §5, §7), so
	// the rebuild the check job submits names the secret the job itself
	// reads. Empty in a spec written before this field existed: the in-job
	// rebuild falls back to the repository's own ID (imagecheck.go).
	GitSecretID string `json:"git_secret_id,omitempty"`
}

// RepoInstallation are the installation values a repository root needs.
type RepoInstallation struct {
	// ProjectName comes from the installation's outputs; the repository
	// root checks every job's FUGARO_PROJECT against it.
	ProjectName             string      `json:"-"`
	RunsBucket              string      `json:"runs_bucket"`
	RegistryHost            string      `json:"registry_host"`
	BaseRegistry            string      `json:"base_registry"`
	GhcrRegistry            string      `json:"ghcr_registry"`
	SharedRegistry          string      `json:"shared_registry"`
	SchedulerServiceAccount string      `json:"scheduler_service_account"`
	RoleIDs                 RepoRoleIDs `json:"role_ids"`
	Launchers               []string    `json:"launchers"`
	Operators               []string    `json:"operators"`
	// CredentialsService is the installation's credentials service, or absent
	// while it has not opted in (D146, D147): the workflow module grants each
	// job account roles/run.invoker on it and drops that job's model-secret
	// accessors (D135, D145).
	CredentialsService *CredentialsService `json:"credentials_service,omitempty"`
}

// RepoRoleIDs are the custom roles a repository root grants.
type RepoRoleIDs struct {
	JobRunner      string `json:"job_runner"`
	BuildSubmitter string `json:"build_submitter"`
	TagMover       string `json:"tag_mover"`
}

// RepoSpec is one repository. The tagged fields are the tfvars' repo.
type RepoSpec struct {
	Name                 string                  `json:"name"`
	Provider             string                  `json:"provider"`
	Slug                 string                  `json:"slug"`
	Label                string                  `json:"label"`
	Secrets              map[string]string       `json:"secrets"` // logical name → secret ID
	Registry             Registry                `json:"registry"`
	BuildServiceAccount  ServiceAccount          `json:"build_service_account"`
	BuildSecrets         []string                `json:"build_secrets"` // logical names
	BuildBucketCondition Condition               `json:"build_bucket_condition"`
	Check                *CheckSpec              `json:"check"`
	Workflows            map[string]WorkflowSpec `json:"workflows"`
	// BitbucketProxied is the per-repository fact of the flip (D500, D502):
	// this repository's git credential is one the credentials service
	// brokers for it, so each of its jobs gives up the accessor on and the
	// mount of every name in config.ProxiedGitSecretNames (Readiness drops
	// the mount, modules/workflow/sa.tf the accessor), while every other
	// Bitbucket repository — of an installation that opted in or not —
	// keeps both (G9). It is true only when the installation has opted in to
	// the service AND the published node admits this repository with
	// proxy: true (G15, proxiedRepository), and it is written into the
	// tfvars' repo object as the workflow module's bitbucket_proxied input.
	//
	// It is omitted when false, which is what every tfvars written before
	// the input existed means: the module declares bitbucket_proxied
	// optional and false (roots/repo and modules/repo's own repo object), so
	// a plan cannot tell an omitted fact from a false one, and no golden
	// plan moves for a repository nobody proxied.
	BitbucketProxied bool `json:"bitbucket_proxied,omitempty"`

	// ProjectSecretCandidates are the logical names this repository may use
	// project-wide, mapped to the secret ID they would resolve to
	// (gcp.ProjectSecretID). Candidate data only: being here says nothing
	// about whether anything actually resolves there yet -- Readiness
	// decides that against live state, and produces ProjectSecrets.
	// json:"-": this map never reaches tfvars.
	ProjectSecretCandidates map[string]string `json:"-"`
	// ProjectSecrets are the logical names Readiness resolved to the
	// project copy for this repository (repo has no version, project does),
	// mapped to the project secret ID. This is the field that reaches
	// tfvars as repo.project_secrets.
	ProjectSecrets map[string]string `json:"project_secrets,omitempty"`

	// GCPProject is the GCP project ID. It is not in the tfvars under this
	// name: the root's own variable is "project".
	GCPProject   string           `json:"-"`
	Region       string           `json:"-"`
	Installation RepoInstallation `json:"-"`
	GitHubAppID  string           `json:"-"` // GitHub only
	RepoURL      string           `json:"-"`
	BaseBranch   string           `json:"-"`
	GitUser      string           `json:"-"` // the HTTPS username of the provider credential
	// BuildServiceAccountEmail and RegistryPath (<host>/<repository ID>)
	// are where builds run as and push to.
	BuildServiceAccountEmail string `json:"-"`
	RegistryPath             string `json:"-"`
}

// repoCtx is what every workflow of a repository shares.
type repoCtx struct {
	in           Inputs
	lc           *localcfg.Config
	provider     string
	slug, label  string
	bucket       string
	registryHost string
	baseBranch   string
	repoURL      string
	appID        string
	gitSecret    string
	gitUser      string
	inst         InstallationOutputs // with defaults filled in
	// bitbucketProxied is the per-repository fact of the flip, decided once
	// for the repository by resolveBitbucketProxied: RepoSpec.BitbucketProxied
	// is this, and nothing else reads it.
	bitbucketProxied bool
}

var appIDRE = regexp.MustCompile(`^[0-9]{1,20}$`)

func resolve(in Inputs) (*repoCtx, error) {
	if in.LC == nil || in.Cfg == nil {
		return nil, errors.New("infra: the local config and fugaro.yaml are both required")
	}
	lc := in.LC
	c := &repoCtx{in: in, lc: lc, provider: in.Cfg.Git.Provider}
	local, hasLocal := localRepo(lc, in.Repo)
	if hasLocal && local.Provider != "" && local.Provider != c.provider {
		return nil, userErr("the local config says %s is on %s, but its fugaro.yaml says %s; make them agree", in.Repo, local.Provider, c.provider)
	}
	var err error
	if c.slug, err = task.Slug(c.provider, in.Repo); err != nil {
		return nil, userErr("%v", err)
	}
	if c.label, err = gcp.RepoLabel(c.slug); err != nil {
		return nil, userErr("%v", err)
	}
	if c.bucket, err = bucketName(lc); err != nil {
		return nil, userErr("%v", err)
	}
	c.inst = withDefaults(in.Installation, lc, c.bucket)
	if c.inst.ProjectName != lc.Name {
		return nil, userErr("the installation's project name is %s, but the local config's is %s; they must agree (renaming isn't supported)", c.inst.ProjectName, lc.Name)
	}
	if c.inst.RunsBucket != c.bucket {
		return nil, userErr("the installation's runs bucket is %s, but the local config's is %s", c.inst.RunsBucket, c.bucket)
	}
	if c.registryHost, err = registryHost(lc, in.Installation.RegistryHost); err != nil {
		return nil, err
	}
	c.inst.RegistryHost = c.registryHost
	// Role IDs from a stale output of another project's state would grant
	// that project's roles, so they must be this project's.
	prefix := "projects/" + lc.GCPProject + "/roles/"
	for _, r := range []string{c.inst.RoleIDs.Launcher, c.inst.RoleIDs.JobRunner, c.inst.RoleIDs.BuildSubmitter, c.inst.RoleIDs.TagMover} {
		if id, ok := strings.CutPrefix(r, prefix); !ok || id == "" || strings.Contains(id, "/") {
			return nil, userErr("the installation's role %s is not a custom role of project %s", r, lc.GCPProject)
		}
	}

	// The branch this repository's builds and checks are cut from, resolved
	// by the one precedence every layer resolves through: the checkout's
	// fugaro.yaml when the file writes git.base_branch, else the local
	// config's repos.<r>.base_branch, else the project's top-level
	// base_branch, else main. in.Cfg carries the file's value (or the
	// default Resolve filled in), which is why the file's own answer needs
	// in.FileSetsBaseBranch beside it.
	repoBranch := ""
	if hasLocal {
		repoBranch = local.BaseBranch
	}
	fileSets := true
	if in.FileSetsBaseBranch != nil {
		fileSets = *in.FileSetsBaseBranch
	}
	c.baseBranch, _ = config.EffectiveBaseBranch(in.Cfg.Git.BaseBranch, fileSets, repoBranch, lc.BaseBranch)
	if c.repoURL, err = repoURL(c.provider, in.Repo, in.RepoURL); err != nil {
		return nil, err
	}
	c.gitSecret, _ = GitSecret(c.provider)
	switch c.provider {
	case gitprov.KindBitbucket:
		c.gitUser = bitbucket.GitUsername
	case gitprov.KindGitHub:
		c.gitUser = githubGitUser
		c.appID = in.GitHubAppID
		if c.appID == "" && hasLocal {
			c.appID = local.GitHubAppID
		}
		if c.appID == "" {
			return nil, userErr("%s is on GitHub and has no GitHub App ID: pass --github-app-id (it is not a secret)", in.Repo)
		}
		if !appIDRE.MatchString(c.appID) {
			return nil, userErr("GitHub App ID %q is not 1 to 20 digits (--github-app-id)", c.appID)
		}
	default:
		return nil, userErr("git.provider %q is not bitbucket or github", c.provider)
	}
	if err := c.resolveBitbucketProxied(); err != nil {
		return nil, err
	}
	return c, nil
}

// resolveBitbucketProxied decides the per-repository fact of the flip
// (RepoSpec.BitbucketProxied) and refuses a local project_secrets entry that
// answers differently for this repository.
//
// The fact has two halves and it needs both. The installation must have opted
// in to the credentials service (D146, D147): with it off nothing changes,
// whatever the node admits and whatever the operator's entry says (G9). And
// the published node must admit this repository with proxy: true — the node is
// the ONE source of that half (G15), because it is the document the
// credentials service itself reads when a job asks it for the token (G3), so
// the fact this spec is built from and the admission a run is granted come
// from the same place and cannot disagree.
//
// The node is read through the operator's credentials by the caller
// (internal/cli's policyReadOnce, over budget.rtdb_url) and handed in as
// Inputs.Policy: internal/infra holds no client that could read it, and Repo
// is a pure function of its Inputs. nil Policy therefore means the caller read
// no node — fugaro image, fugaro doctor, --print-vars — and then nothing is
// proxied and no entry is judged against a document this caller never saw. An
// empty Policy is the different answer "read, and the project has published
// no block", which proxies nothing either but does judge an entry that says
// otherwise.
//
// The refusal is the flip's one place where the two documents can disagree,
// and it is worth a refusal rather than a precedence rule: the entry is what
// `fugaro project policy publish` derives the node's block from, so a local
// entry that proxies a repository the node does not (or stopped proxying one
// it does) means the next publish from this config would move the fact under a
// running installation — the runs would read one answer and the next update
// another. Both fixes are named, because both are legitimate: publish the
// config the entry is in, or correct the entry to say what the node says.
//
// An entry that is not there is not a disagreement: the node is then the only
// answer, and it is the answer this fact takes. Neither is an entry judged
// while the opt-in is off, or when no node was read.
//
// Nor is a GitHub repository's, and the provider check below is what says so.
// The node's allow_repos is bitbucket-qualified by construction
// (projectpolicy.validBitbucketTokenBlock refuses any other provider, and
// MatchRepo compares segment against segment), so this repository can never be
// admitted by it and the fact is false whatever the node says. The local
// entry, on the other hand, holds bare owner/name slugs, so a GitHub
// repository whose name matches one of them would read as proxied and be
// refused over a Bitbucket credential it does not use — a GitHub repository's
// git credential is the App key, and project_secrets.bitbucket-token is not
// it.
func (c *repoCtx) resolveBitbucketProxied() error {
	if c.provider != gitprov.KindBitbucket || c.inst.CredentialsService == nil || c.in.Policy == nil {
		return nil
	}
	canonical, err := projectpolicy.Canonical(c.provider, c.in.Repo)
	if err != nil {
		// Unreachable: resolve has already put the same pair through
		// task.Slug, which validates the provider kind and the repository with
		// these very functions.
		return userErr("%v", err)
	}
	var nodeProxies bool
	if b := c.in.Policy.BitbucketToken; b != nil {
		nodeProxies = b.Proxy && projectpolicy.AdmitRepo(b.AllowRepos, canonical)
	}
	c.bitbucketProxied = nodeProxies

	entry, hasEntry := c.lc.ProjectSecrets[config.BitbucketSecret]
	if !hasEntry {
		return nil
	}
	localProxies := entry.Proxy && entry.AllowsData(c.in.Repo)
	if localProxies == nodeProxies {
		return nil
	}
	says := func(proxied bool) string {
		if proxied {
			return "admits it with proxy: true"
		}
		return "does not admit it with proxy: true"
	}
	return userErr("the local config's project_secrets.%s entry and the project's published policy disagree about %s: the entry %s, and the published node %s. Whether a repository's git credential is brokered has one source, the published node, because that node is what the credentials service reads when the repository's job asks it for the token — so make the two agree: publish this config with 'fugaro project policy publish', which derives the node's bitbucket_token block from this entry, or correct the entry to say what the node says", config.BitbucketSecret, c.in.Repo, says(localProxies), says(nodeProxies))
}

// localRepo is the local config's entry for repo, matched as fugaro run
// matches it (localcfg.Config.RepoFor, the one lookup every reader of
// repos.<r> resolves through).
func localRepo(lc *localcfg.Config, repo string) (localcfg.Repo, bool) {
	return lc.RepoFor(repo)
}

// bucketName is the runs bucket's name, which IAM conditions need: the
// one image builds record in (lc.RecordBucketURL), so the specs, the
// first build and readiness name the same bucket.
func bucketName(lc *localcfg.Config) (string, error) {
	if name := lc.RunsBucketName(); name != "" {
		return name, nil
	}
	return "", errors.New("the local config's bucket_url is not a gs:// bucket")
}

var registryHostRE = regexp.MustCompile(`^([a-z]+-[a-z]+[0-9]+)-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])$`)

// RegistryHost is the installation's registry host for lc: its
// registry_host, else the one a new installation creates, checked to be
// lc's project.
func RegistryHost(lc *localcfg.Config) (string, error) { return registryHost(lc, "") }

// registryHost is <region>-docker.pkg.dev/<project>, the prefix of every
// image registry: the local config's, else the installation's output,
// else the one the installation creates. Images are pushed there with the
// project's credentials, so a host naming another project is refused.
func registryHost(lc *localcfg.Config, output string) (string, error) {
	host := lc.RegistryHost
	switch {
	case host != "" && output != "" && host != output:
		return "", userErr("the local config's registry_host %s is not the installation's %s", host, output)
	case host == "":
		host = output
	}
	if host == "" {
		host = gcp.DefaultRegistryHost(lc.Region, lc.GCPProject)
	}
	m := registryHostRE.FindStringSubmatch(host)
	if m == nil {
		return "", userErr("registry host %q is not <region>-docker.pkg.dev/<project>", host)
	}
	if m[2] != lc.GCPProject {
		return "", userErr("registry host %s names project %s, not %s", host, m[2], lc.GCPProject)
	}
	return host, nil
}

// repoURL is the https clone URL: the given one, which must be on the
// provider's host and carry no credentials, else the provider's usual one.
//
// The rules are repourl.CloneURL's, and only the error shape is this
// package's: the deployer and the runner (a project job's clone URL, plan task
// 3.3 step 3) must give the same answer, so a URL one of them refuses cannot
// be one the other accepts. A per-repository job's FUGARO_REMOTE is written
// from here (jobCheckoutEnv) and a project job's URL is computed there.
func repoURL(provider, repo, given string) (string, error) {
	u, err := repourl.CloneURL(provider, repo, given)
	if err != nil {
		return "", userErr("%v", err)
	}
	return u, nil
}

// withDefaults fills in the outputs the installation has not reported
// with the names Go gives a new installation.
func withDefaults(o InstallationOutputs, lc *localcfg.Config, bucket string) InstallationOutputs {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	role := func(id string) string { return "projects/" + lc.GCPProject + "/roles/" + id }
	def(&o.ProjectName, lc.Name)
	def(&o.RunsBucket, bucket)
	def(&o.BaseRegistry, BaseRegistry)
	def(&o.GhcrRegistry, GhcrRegistry)
	def(&o.SharedRegistry, gcp.SharedRegistryRepoID())
	def(&o.SchedulerServiceAccount, serviceAccountEmail(SchedulerServiceAccountID, lc.GCPProject))
	def(&o.RoleIDs.Launcher, role(RoleLauncher))
	def(&o.RoleIDs.JobRunner, role(RoleJobRunner))
	def(&o.RoleIDs.BuildSubmitter, role(RoleBuildSubmitter))
	def(&o.RoleIDs.TagMover, role(RoleTagMover))
	if o.Launchers == nil {
		o.Launchers = slices.Clone(lc.Terraform.Launchers)
	}
	if o.Operators == nil {
		o.Operators = slices.Clone(lc.Terraform.Operators)
	}
	if o.Launchers == nil {
		o.Launchers = []string{}
	}
	if o.Operators == nil {
		o.Operators = []string{}
	}
	if o.RegistryCleanupDryRun == nil {
		t := true
		o.RegistryCleanupDryRun = &t
	}
	return o
}

func serviceAccountEmail(id, project string) string {
	return id + "@" + project + ".iam.gserviceaccount.com"
}

// platformEnv is the M4 env every job and the check job get.
func (c *repoCtx) platformEnv() map[string]string {
	return map[string]string{
		BucketEnv:     "gs://" + c.bucket,
		BackendEnv:    backend.CloudRun,
		GCPProjectEnv: c.lc.GCPProject,
		ProjectEnv:    c.inst.ProjectName,
		RegionEnv:     c.lc.Region,
	}
}

func (c *repoCtx) registryPath() string { return c.registryHost + "/" + gcp.RegistryRepoID(c.slug) }

// Workflow is the spec of one workflow of the repository, its default
// Claude account (config.DefaultClaudeAccount). Repo builds one WorkflowSpec
// per account a repository may choose among (docs/design/named-oauth-tokens.md
// §4); this exported helper only ever builds the default one, which is what
// every caller outside this package wants.
func Workflow(in Inputs, name string) (WorkflowSpec, error) {
	c, err := resolve(in)
	if err != nil {
		return WorkflowSpec{}, err
	}
	return c.workflow(name, config.DefaultClaudeAccount)
}

// GitSecret is the logical name of the git credential of provider (the
// secret every job mounts), and false for a provider that is neither
// Bitbucket nor GitHub. The one place that names them.
//
// Bitbucket's is the per-repository one of the two: a repository access token
// is issued for one repository and works for no other, so it is never stored
// project-wide by default (config.PerRepositorySecret, the owner's ruling of
// 2026-10-15) -- an operator who holds one workspace or project access token
// instead stores that one for the project through the allow-list (D461/D464).
// GitHub's App key is a project's, mounted by every repository the App serves,
// and its logical name is config.GitHubAppKeySecret — the same constant
// config.IsServiceHeldSecretEnv and the workflow module's
// service_held_secret_names answer "is this the App key?" with. Review finding
// F3: this function wrote both the App key's name and Bitbucket's out as
// literals, so a rename in config would have left it returning a secret no job
// mounts; the internal/cli code that spelled the App key out — doctor.go's
// check, secrets.go's multilineSecret, githubapp.go's project-wide note,
// init.go's App pre-check and project_enroll.go's --github-app-key read — now
// names the constant too. What still spells the literal does so on purpose: the
// flag's own name, the sentences a user reads ("fugaro secrets set
// github-app-key …"), the tests that pin the name as the wire value, and
// gcp/build.go's example of the ID this builds.
func GitSecret(provider string) (string, bool) {
	switch provider {
	case gitprov.KindBitbucket:
		return config.BitbucketSecret, true
	case gitprov.KindGitHub:
		return config.GitHubAppKeySecret, true
	}
	return "", false
}

// SecretMount is one secret a workflow's job mounts: the logical name (what
// fugaro secrets set takes) and the variable it becomes.
type SecretMount struct{ Logical, Env string }

// SecretMounts is what the job of workflow name, for Claude account token
// (w) mounts, in order: the git provider's credential (gitSecret), the one
// Claude credential agent.auth names (none for vertex; token's own derived
// secret, config.ClaudeAccountSecret, for oauth), the keys of the model
// providers the owner's local config allows repo to send code to (only with
// api-key: a run does not mix credentials), and the workflow's own secrets.
// It is the one place that decides it, for the job's spec and for the
// secrets init asks for.
//
// A provider's key is the runner's, for its gateway, and only a repository
// the owner allowed to send code there gets it, as a variable the agent's
// environment never carries.
//
// Limitation: every workflow of an allowed repository gets the key, whether
// or not its pins name that provider's models. The spec sees only the
// checked-in agent block (shared by all workflows), but the models a run
// uses come from the branch's fugaro.yaml and a task's --model override,
// neither known when the job is deployed; gating on the visible pins would
// make a run that pins a provider model later fail for a missing key. The
// key reaches only the runner, never the agent, and the repository is
// already one the owner allowed to send code to the provider.
func SecretMounts(gitSecret string, cfg *config.Config, name, token string, w config.Workflow, lc *localcfg.Config, repo string) ([]SecretMount, error) {
	out := []SecretMount{{gitSecret, config.ReservedSecrets[gitSecret]}}
	switch cfg.Agent.Auth {
	case "cascade":
		// The cascade mounts no model credential at all (D143, D146): the
		// credentials service hands the run one at call time, through the
		// in-runner gateway and the run's own identity (C1.19), so there is
		// no tier-A token and no tier-B key in this job's environment — and
		// the job's account holds no accessor on either
		// (modules/workflow/sa.tf, D145). Stated as a case of its own rather
		// than left to fall past the switch: the empty body is the decision.
		// TestCascadeMountsNoOAuthToken holds it.
	case "oauth":
		out = append(out, SecretMount{config.ClaudeAccountSecret(token), config.ReservedSecrets["claude-oauth-token"]})
	case "api-key":
		out = append(out, SecretMount{"anthropic-api-key", config.ReservedSecrets["anthropic-api-key"]})
		for _, pn := range slices.Sorted(maps.Keys(lc.Providers)) {
			if p := lc.Providers[pn]; p.AllowsData(repo) {
				out = append(out, SecretMount{p.Secret, p.SecretEnv()})
			}
		}
	}
	for _, s := range w.Secrets {
		if p, ok := config.ProviderBySecret(lc.Providers, s.Name); ok {
			return nil, userErr("workflow %s: secret %s is the key of model provider %s, which only the owner's local config may use", name, s.Name, p)
		}
		if p, ok := config.ReservedProviderSecret(lc.Providers, s.Name); ok {
			// The conventional name of a configured provider's key, and not
			// that provider's own .secret (a name that IS its key took the
			// branch above). No workflow may mount it: the name belongs to
			// that provider's key (config.ReservedProviderSecret), so a value
			// stored under it would be read as the provider's credential the
			// day the key is renamed to the convention, and `fugaro secrets
			// set` refuses to store one — the job would mount a name that can
			// have no version. Refused here rather than left to the readiness
			// check, which would blame a missing secret. The same rule is
			// config.CheckWorkflowProviderSecrets's, which `fugaro validate`
			// calls; the two refusals must name the same secrets, and
			// TestTheSpecAndValidateRefuseTheSameWorkflowSecretNames (this
			// package) is what holds them to it.
			return nil, userErr("workflow %s: secret %s is the name reserved for model provider %s's key (the convention names a provider's key after its entry), so no workflow may declare it", name, s.Name, p)
		}
		out = append(out, SecretMount{s.Name, s.Env})
	}
	return out, nil
}

// WorkflowAccounts are the Claude accounts each of repo's workflows gets a
// job for: under oauth every account config.AllowedClaudeAccounts allows repo
// (the default first), and the default alone under every other auth. RepoSpec
// deploys one job per (workflow, account) from it, and RequiredSecrets reads
// the same list, so the two cannot disagree about which tokens are mounted.
func WorkflowAccounts(cfg *config.Config, lc *localcfg.Config, repo string) []string {
	if cfg.Agent.Auth == "oauth" {
		return config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)
	}
	return []string{config.DefaultClaudeAccount}
}

// RequiredSecrets is every logical secret name the jobs of repo's workflows
// mount and cannot go without, sorted: the union of SecretMounts over each
// workflow of cfg and each account WorkflowAccounts gives it, with the git
// credential of cfg's provider (GitSecret), less the optional mounts
// (OptionalSecret) a job runs without, as readiness treats them. It is what
// `fugaro secrets ls` checks for MISSING, so that column flags exactly the
// names a deployed job needs and never one no job of this repository reads. A provider GitSecret does not
// know, or a workflow SecretMounts refuses, is an error, not a guess.
func RequiredSecrets(cfg *config.Config, lc *localcfg.Config, repo string) ([]string, error) {
	git, ok := GitSecret(cfg.Git.Provider)
	if !ok {
		return nil, userErr("git.provider %q is not bitbucket or github", cfg.Git.Provider)
	}
	set := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		for _, tok := range WorkflowAccounts(cfg, lc, repo) {
			ms, err := SecretMounts(git, cfg, name, tok, cfg.Workflows[name], lc, repo)
			if err != nil {
				return nil, err
			}
			for _, m := range ms {
				if !OptionalSecret(m.Logical) {
					set[m.Logical] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(set)), nil
}

// workflow is the spec of (name, token): fugaro.yaml's logical workflow
// commands, base, resources and timeouts, for the Cloud Run job of the
// variant gcp.WorkflowVariant(name, token) (identity for the repository's
// default account, so an installation with no named accounts gets a job,
// account and labels byte-identical to before this field existed, design
// docs/design/named-oauth-tokens.md §5). Only the job's own identity (Job,
// ServiceAccount, the fugaro_workflow label) and its mounted Claude secret
// (SecretMounts) vary by token; the image, commands and resources are the
// one fugaro.yaml workflow entry's, shared by every variant (they are built
// once, not once per account).
func (c *repoCtx) workflow(name, token string) (WorkflowSpec, error) {
	w, ok := c.in.Cfg.Workflows[name]
	if !ok {
		return WorkflowSpec{}, userErr("fugaro.yaml has no workflow %q", name)
	}
	if issues := gcp.CheckResources(w.Resources); len(issues) > 0 {
		return WorkflowSpec{}, userErr("workflow %s: resources.%s: %s", name, issues[0].Field, issues[0].Message)
	}
	lc, slug := c.lc, c.slug
	variant := gcp.WorkflowVariant(name, token)
	saID := gcp.ServiceAccountID(slug, variant)
	// checkout: clone runs the installation's base image directly (design
	// generic-tool.md §2, G3): there is no per-repository derived image, so
	// the job's image is base_images.base, not a :latest tag in the
	// repository's own registry. Every token variant of the workflow runs
	// the same image. A checkout: shared workflow keeps the derived tag:
	// the image its job runs is a shared dependency-layer digest from the
	// lock, which only the operator's machine writes onto the job (design
	// forge-authorship-images/shared-images.md §6), and no spec can know it.
	// Until C8b1's refresh writes that digest, the two markers below make a
	// shared job clone-shaped while this tag — the per-repository image
	// readiness.go's non-clone branch asks the operator to build — is what
	// it runs; C8b1 owns replacing both.
	image := gcp.ImageName(c.registryPath(), slug, name) + ":latest"
	if w.Checkout == config.CheckoutClone {
		image = lc.BaseImage(config.BaseKind)
	}
	ws := WorkflowSpec{
		Job:            gcp.JobName(slug, variant),
		ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.JobSADisplayName(slug, variant)},
		Image:          image,
		Checkout:       w.Checkout,
		CPU:            strconv.Itoa(w.Resources.CPU),
		Memory:         w.Resources.Memory,
		TaskTimeoutS:   int((w.Timeouts.Total.Duration + backend.TaskTimeoutSlack) / time.Second),
		Env:            c.platformEnv(),
		SecretEnv:      map[string]string{},
		BucketCondition: Condition{
			Title:      gcp.BucketConditionTitle(saID),
			Expression: gcp.BucketCondition(c.bucket, gcp.JobBucketPrefixes, slug),
		},
		TokenCondition: Condition{
			Title:      gcp.TokenBucketConditionTitle(saID),
			Expression: gcp.TokenBucketCondition(c.bucket, slug),
		},
		Vertex:    c.in.Cfg.Agent.Auth == "vertex",
		DeployJob: true,

		Name:                name,
		Slug:                slug,
		ServiceAccountEmail: serviceAccountEmail(saID, lc.GCPProject),
		Labels:              map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: c.label, gcp.LabelWorkflow: variant},
		SecretIDs:           map[string]string{},
		GitSecret:           c.gitSecret,
	}
	if lc.Registry != "" {
		ws.LegacyImage = gcp.ImageName(lc.Registry, slug, name)
	}
	// One writer for the job's clone shape: a job whose image holds no
	// repository's source — clone, which runs the installation's base image,
	// and shared, which runs the dependency layer alone — carries the two
	// markers the run needs to fetch the source itself, and the value is the
	// workflow's own kind, which is what the runner switches on.
	if w.Checkout == config.CheckoutClone || w.Checkout == config.CheckoutShared {
		ws.Env[CheckoutEnv] = w.Checkout
		// Where the run clones from: the repository's own URL, which
		// c.repoURL already checked is https, credential-free and this
		// repository's (see repoURL).
		ws.Env[RemoteEnv] = c.repoURL
	}
	if ws.Vertex {
		ws.Env[VertexRegionEnv] = lc.Region
		ws.Env[VertexProjectEnv] = lc.GCPProject
	}
	if c.appID != "" {
		ws.Env[providers.EnvGitHubAppID] = c.appID
	}
	if p, ok := lc.PriceOverride(lc.Region); ok {
		ws.Env[ComputePricesEnv] = formatPrice(p.VCPUSecondUSD) + "," + formatPrice(p.GiBSecondUSD)
	}
	if err := budgetEnv(lc, ws.Env); err != nil {
		return WorkflowSpec{}, userErr("%v", err)
	}
	// Only the providers this repository may send code to are named; the
	// keys are mounted below.
	pv, err := runner.ProvidersEnv(lc.Providers, c.in.Repo)
	if err != nil {
		return WorkflowSpec{}, userErr("%v", err)
	}
	if pv != "" {
		ws.Env[ModelProvidersEnv] = pv
	}

	var collisions []string
	mounts, err := SecretMounts(c.gitSecret, c.in.Cfg, name, token, w, lc, c.in.Repo)
	if err != nil {
		return WorkflowSpec{}, err
	}
	for _, m := range mounts {
		if _, dup := ws.SecretEnv[m.Env]; dup {
			collisions = append(collisions, m.Env)
		}
		ws.SecretEnv[m.Env] = m.Logical
		ws.SecretIDs[m.Logical] = gcp.SecretID(slug, m.Logical)
	}
	// An installation that has opted in to the credentials service holds
	// every model credential itself (D146, D147), so its jobs mount none of
	// them: modules/workflow/sa.tf drops each job account's accessor, and a
	// mount left behind would fail the job's own deploy. The containers stay
	// declared — see dropModelMounts — so nothing here plans a secret's
	// destruction.
	if c.inst.CredentialsService != nil {
		dropModelMounts(&ws)
	}
	ws.BuildSecrets = []string{c.gitSecret}
	for _, s := range w.Secrets {
		ws.BuildSecrets = append(ws.BuildSecrets, s.Name)
	}
	slices.Sort(ws.BuildSecrets)
	ws.BuildSecrets = slices.Compact(ws.BuildSecrets)
	// Every variable a secret is mounted as, so the runner can register
	// them all for redaction before bootstrap.
	ws.Env[runner.SecretEnvsVar] = redactionEnvs(ws.SecretEnv)
	for env := range ws.SecretEnv {
		if _, dup := ws.Env[env]; dup {
			collisions = append(collisions, env)
		}
	}
	if len(collisions) > 0 {
		slices.Sort(collisions)
		return WorkflowSpec{}, userErr("workflow %s: secret env %s collides with a variable the platform sets", name, strings.Join(slices.Compact(collisions), ", "))
	}
	return ws, nil
}

// redactionEnvs is the value of runner.SecretEnvsVar for a job that mounts
// secretEnv: every variable that job mounts a secret as, sorted and
// comma-joined, which the runner splits and registers with its redactor before
// bootstrap (internal/runner's addMountedSecrets).
//
// It is a function of the mounts and of nothing else, and every job that has
// mounts computes it from its own map — the workflow specs, the check job, and
// a project job's, whose jobs of one profile differ from each other (each
// account's job mounts that account's token, or none). A hand-written list
// beside the mounts is the one thing this cannot be: a mount added without it
// is a secret the run publishes the moment it prints its environment, and
// SECURITY.md's claim that the redactor masks what a run publishes rests on the
// two being the same list.
func redactionEnvs(secretEnv map[string]string) string {
	return strings.Join(slices.Sorted(maps.Keys(secretEnv)), ",")
}

// budgetEnv adds the project's budget to a workflow job's env: the token
// cap and the allow-list, the cap when there is one and the price overrides
// when there are any, whatever the mode (they are the owner's ceiling, which
// a repository's committed policy is clamped to), the mode itself when it is
// not off, and the backend's address and web key whenever the installation
// has them (D11: a run reads the project's policy with them). The check job
// calls no model and gets none of it.
func budgetEnv(lc *localcfg.Config, env map[string]string) error {
	if b := lc.Budget; b != nil {
		if b.MaxRunTokens > 0 {
			env[MaxRunTokensEnv] = strconv.FormatInt(b.MaxRunTokens, 10)
		}
		if b.AllowedModels != nil {
			env[AllowedModelsEnv] = strings.Join(b.AllowedModels, ",")
		}
		if b.PerRunUSD > 0 {
			env[MaxRunUSDEnv] = formatPrice(b.PerRunUSD)
		}
	}
	o, err := lc.Overrides()
	if err != nil {
		return err
	}
	prices, err := o.Env()
	if err != nil {
		return err
	}
	if prices != "" {
		env[ModelPricesEnv] = prices
	}
	// The backend's address and its web key go to every job whenever the
	// installation has them, whatever the mode (D11, plan task 1.4 step 1):
	// the mode is policy now, and a run reads the project's policy with these
	// two — the token exchange needs the key, not only the URL. A job
	// deployed without them cannot read a policy at all, and cannot tell that
	// apart from a project that published none, so it would run on the
	// environment ceiling with the policy unenforced; Readiness's Firebase
	// gate is what refuses that once a policy exists.
	if b := lc.Budget; b != nil {
		if b.RTDBURL != "" {
			env[RTDBURLEnv] = b.RTDBURL
		}
		if b.FirebaseAPIKey != "" {
			env[FirebaseAPIKeyEnv] = b.FirebaseAPIKey
		}
	}
	// The mode stays the deploy-time mode: it is the fallback for the case
	// where no policy exists (P3), and the grace only matters to a session.
	if mode := lc.BudgetMode(); mode != localcfg.BudgetOff {
		env[BudgetModeEnv] = mode
		if b := lc.Budget; b != nil && b.Grace > 0 {
			env[BudgetGraceEnv] = b.Grace.String()
		}
	}
	return nil
}

// formatPrice is the shortest decimal that parses back to v.
func formatPrice(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Repo is the spec of the repository and all of its workflows.
func Repo(in Inputs) (RepoSpec, error) {
	c, err := resolve(in)
	if err != nil {
		return RepoSpec{}, err
	}
	lc, slug := c.lc, c.slug
	buildID := gcp.BuildServiceAccountID(slug)
	rs := RepoSpec{
		Name:                in.Repo,
		Provider:            c.provider,
		Slug:                slug,
		Label:               c.label,
		Secrets:             map[string]string{},
		Registry:            Registry{RepositoryID: gcp.RegistryRepoID(slug), CleanupDryRun: *c.inst.RegistryCleanupDryRun},
		BuildServiceAccount: ServiceAccount{AccountID: buildID, DisplayName: gcp.BuildSADisplayName(slug)},
		BuildSecrets:        []string{},
		BuildBucketCondition: Condition{
			Title:      gcp.BucketConditionTitle(buildID),
			Expression: gcp.BucketCondition(c.bucket, gcp.BuildBucketPrefixes, slug),
		},
		Workflows: map[string]WorkflowSpec{},

		GCPProject: lc.GCPProject,
		Region:     lc.Region,
		Installation: RepoInstallation{
			ProjectName:             c.inst.ProjectName,
			RunsBucket:              c.bucket,
			RegistryHost:            c.registryHost,
			BaseRegistry:            c.inst.BaseRegistry,
			GhcrRegistry:            c.inst.GhcrRegistry,
			SharedRegistry:          c.inst.SharedRegistry,
			SchedulerServiceAccount: c.inst.SchedulerServiceAccount,
			RoleIDs:                 RepoRoleIDs{JobRunner: c.inst.RoleIDs.JobRunner, BuildSubmitter: c.inst.RoleIDs.BuildSubmitter, TagMover: c.inst.RoleIDs.TagMover},
			Launchers:               c.inst.Launchers,
			Operators:               c.inst.Operators,
			CredentialsService:      repoCredentialsService(c.inst.CredentialsService),
		},
		BitbucketProxied: c.bitbucketProxied,

		GitHubAppID:              c.appID,
		RepoURL:                  c.repoURL,
		BaseBranch:               c.baseBranch,
		GitUser:                  c.gitUser,
		BuildServiceAccountEmail: serviceAccountEmail(buildID, lc.GCPProject),
		RegistryPath:             c.registryPath(),
	}
	var checked []string
	for _, name := range slices.Sorted(maps.Keys(in.Cfg.Workflows)) {
		// One WorkflowSpec per Claude account this repository may choose for
		// an oauth workflow (design docs/design/named-oauth-tokens.md §4 T3):
		// config.AllowedClaudeAccounts is the one place that decides the
		// set, so a name not on it never gets a key here at all (the hard
		// backstop of the allow-list, design §6b.3). Every other auth kind
		// has exactly the default account, unchanged from before this field
		// existed.
		for _, tok := range WorkflowAccounts(in.Cfg, lc, in.Repo) {
			ws, err := c.workflow(name, tok)
			if err != nil {
				return RepoSpec{}, err
			}
			key := gcp.WorkflowVariant(name, tok)
			rs.Workflows[key] = ws
			maps.Copy(rs.Secrets, ws.SecretIDs)
			rs.BuildSecrets = append(rs.BuildSecrets, ws.BuildSecrets...)
		}
		// The daily check covers the workflow's image, the same image
		// across every token variant: once per logical workflow, not once
		// per variant. A checkout: clone workflow has no derived image to
		// rebuild, so the daily check never covers it, whatever its
		// rebuild.check says.
		if in.Cfg.Workflows[name].Checkout != config.CheckoutClone && in.Cfg.Workflows[name].Rebuild.Defaults().Check != "off" {
			checked = append(checked, name)
		}
	}
	slices.Sort(rs.BuildSecrets)
	rs.BuildSecrets = slices.Compact(rs.BuildSecrets)
	if len(checked) > 0 {
		if rs.Check, err = c.check(rs, checked); err != nil {
			return RepoSpec{}, err
		}
	}
	rs.ProjectSecretCandidates = c.projectSecretCandidates(rs)
	return rs, nil
}

// projectSecretCandidates are the logical names this repository is eligible
// to use project-wide, mapped to the project secret ID each would resolve
// to: every name its workflows (and the check job) mount that
// ProjectSecretCandidate accepts. Being here says nothing about whether the
// name actually resolves to the project copy -- Readiness decides that
// against live state (design docs/design/project-secrets.md §5).
func (c *repoCtx) projectSecretCandidates(rs RepoSpec) map[string]string {
	out := map[string]string{}
	add := func(logical string) {
		if id, ok := ProjectSecretCandidate(c.lc, logical, c.in.Repo); ok {
			out[logical] = id
		}
	}
	for _, key := range slices.Sorted(maps.Keys(rs.Workflows)) {
		for _, logical := range uniqueSecrets(rs.Workflows[key]) {
			add(logical)
		}
	}
	if rs.Check != nil {
		for _, logical := range slices.Compact(slices.Sorted(maps.Values(rs.Check.SecretEnv))) {
			add(logical)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ProjectSecretCandidate is the one place that answers "may this repository
// use logical project-wide, and what ID would that be": the name is an
// entry of project_secrets that lists repo, or a reserved secret D2 swept
// into project scope with no project_secrets entry at all, or a named Claude
// account's own derived secret whose account lists repo, or a model
// provider's own key whose provider lists repo -- AllowsData uniformly
// across the last three, so §3a's no-list/["*"]-means-any-repository rule
// applies to a provider's key exactly as it does everywhere else. ok is
// false, id "" otherwise. It is exported because fugaro update's
// secrets stage (before any RepoSpec exists) needs the identical answer.
func ProjectSecretCandidate(lc *localcfg.Config, logical, repo string) (id string, ok bool) {
	if lc == nil || logical == "" {
		return "", false
	}
	eligible := config.AllowedForProjectSecret(lc.ProjectSecrets, logical, repo)
	if !eligible && config.ProjectScopeByDefault(logical) {
		// D2 (plan Task 2.1 step 2, finding 13): a reserved name the platform
		// itself mounts is eligible project-wide with no per-name list, which
		// is the case this function refused before -- only an entry of
		// project_secrets reached it. An *entry* still decides: an owner who
		// wrote project_secrets.<name>.allow_data_to said who may read it, and
		// "absent or empty means nobody" stays true of a list someone wrote.
		// Without one, the sweep applies, and every repository may read it --
		// the same reach the CLI's default store announces, which is what
		// makes "stored project-wide" mean what a person reads it to mean.
		if _, listed := lc.ProjectSecrets[logical]; !listed {
			eligible = true
		}
	}
	if !eligible {
		// ClaudeAccountBySecret always resolves the bare claude-oauth-token
		// to DefaultClaudeAccount, which is never a key of the map
		// (ValidateClaudeAccounts refuses it), so the default account's own
		// secret is eligible only through project_secrets above -- the
		// documented shape (design §3).
		if name, found := config.ClaudeAccountBySecret(lc.ClaudeAccounts, logical); found {
			eligible = lc.ClaudeAccounts[name].AllowsData(repo)
		}
	}
	if !eligible {
		if name, found := config.ProviderBySecret(lc.Providers, logical); found {
			eligible = lc.Providers[name].AllowsData(repo)
		}
	}
	if !eligible {
		return "", false
	}
	return gcp.ProjectSecretID(lc.Name, logical), true
}

// MountSecretID is the one place that answers "which secret ID does this
// repository's job mount for logical": the project's copy when Readiness
// resolved the name to it (ProjectSecrets, written by resolveProject from the
// one predicate, infra.ProjectSecretCandidate, which is D2's
// config.ProjectScopeByDefault), the repository's own container otherwise, and
// "" when neither holds the name -- a name no job mounts at all, or an
// optional secret Readiness dropped for having no version anywhere.
//
// It reads the decision, it does not take it: the two maps are exactly the two
// the repository module's Terraform merge reads
// (deploy/terraform/gcp/modules/repo/workflows.tf's local.secrets prefers
// var.repo.project_secrets over the repository's own container, which is what
// makes a project-scope key mount on the repository's job at all). A caller
// that spelled that precedence itself could point a job at a secret the plan
// never mounted, so the readers of it ask here instead of repeating it.
//
// The phase-3 hand-off (plan Task 2.2 step 3): a repository has a job of its
// own until phase 3. When it stops having one, the same resolution feeds a
// profile's mount list instead, and the repository copy's precedence survives
// only on the per-repository path -- so that move lands here, not in each
// caller.
func (rs RepoSpec) MountSecretID(logical string) string {
	if id := rs.ProjectSecrets[logical]; id != "" {
		return id
	}
	return rs.Secrets[logical]
}

// UsesVertex reports whether any of the repository's workflows
// authenticates its agent through Vertex AI.
func (rs RepoSpec) UsesVertex() bool {
	for _, ws := range rs.Workflows {
		if ws.Vertex {
			return true
		}
	}
	return false
}

// BaseImageWarnings are the warnings for each of the local config's base
// images (kind -> image) that is in neither of the installation's own image
// repositories, in kind order: the base registry (the copy an earlier
// release made) or the remote (pull-through) repository over ghcr.io (this
// release's pin). The build accounts can read those two, so a build from
// anything else would fail at its pull; the check job's own pull would
// still work, which hides it until then.
//
// It is deliberately not a gate: the build account can read both, so this
// is a readability hint and a pointer at where a base image belongs, and
// the gate is cover.go's shape test and managedVersion.
func BaseImageWarnings(bases map[string]string, outs InstallationOutputs) []string {
	registries := []string{outs.RegistryHost + "/" + outs.BaseRegistry, outs.RegistryHost + "/" + outs.GhcrRegistry}
	var ws []string
	for _, kind := range slices.Sorted(maps.Keys(bases)) {
		ref := bases[kind]
		if ref == "" || slices.ContainsFunc(registries, func(r string) bool { return strings.HasPrefix(ref, r+"/") }) {
			continue
		}
		ws = append(ws, fmt.Sprintf("the local config's base_images.%s %s is in neither of the installation's image repositories (%s), the only ones the build accounts can read, so the image builds would fail at its pull: push the base image to %s and set base_images.%s to it",
			kind, ref, strings.Join(registries, ", "), registries[0], kind))
	}
	return ws
}

// check is the daily image check of the workflows in checked. It runs as
// the build account, from the base image, so it holds the provider
// credential and nothing else.
func (c *repoCtx) check(rs RepoSpec, checked []string) (*CheckSpec, error) {
	lc := c.lc
	// The check job runs from the base image of its first workflow's kind,
	// and passes the job the base of each kind a rebuild of its workflows
	// builds FROM.
	bases := map[string]string{}
	var missing []string
	for _, name := range checked {
		kind := c.in.Cfg.Workflows[name].BaseKind()
		if ref := lc.BaseImage(kind); ref != "" {
			bases[kind] = ref
		} else if !slices.Contains(missing, kind) {
			missing = append(missing, kind)
		}
	}
	if len(missing) > 0 {
		return nil, userErr("the local config has no base_images entry for %s, which the daily image check of %s needs: set base_images.<kind> in the local config to the base image of that kind in the installation's base registry",
			strings.Join(missing, ", "), rs.Name)
	}
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			return nil, userErr("%v", err)
		}
		region = r
	}
	spec, err := json.Marshal(CheckJobSpec{
		Repo: rs.Name, Provider: c.provider, RepoURL: c.repoURL, BaseBranch: c.baseBranch,
		Workflows: checked, Registry: rs.RegistryPath, BuildServiceAccount: rs.BuildServiceAccountEmail,
		MachineType: lc.Build.MachineType, BuildRegion: lc.BuildRegion(), BaseImages: bases,
	})
	if err != nil {
		return nil, err
	}
	gitEnv := config.ReservedSecrets[c.gitSecret]
	env := c.platformEnv()
	env[CheckSpecEnv] = string(spec)
	if c.appID != "" {
		env[providers.EnvGitHubAppID] = c.appID
	}
	env[runner.SecretEnvsVar] = gitEnv
	return &CheckSpec{
		Job:             gcp.CheckJobName(c.slug),
		Image:           bases[c.in.Cfg.Workflows[checked[0]].BaseKind()],
		SchedulerJob:    gcp.SchedulerJobName(c.slug),
		SchedulerRegion: region,
		Schedule:        Schedule(c.slug),
		Paused:          true,
		DeployJob:       true,
		Env:             env,
		SecretEnv:       map[string]string{gitEnv: c.gitSecret},
		Workflows:       checked,
	}, nil
}

// Addresses are the Terraform addresses of every resource instance the
// repository root (deploy/terraform/gcp/roots/repo -> modules/repo and
// modules/workflow) declares for rs, sorted. It is deterministic from the
// spec alone -- the same spec always yields the same set -- which is what
// makes Retirements (retire.go) a set difference.
//
// It mirrors the modules' own for_each, count and deploy gates, not their
// prose: a resource a module does not declare for this spec is not an
// address here either, because an address claimed but not produced would
// make Retirements call a live resource retired. In particular the jobs
// (google_cloud_run_v2_job.this[0], the check job and the launcher grants)
// exist only when the spec's DeployJob says so, exactly as
// `count = var.workflow.deploy_job ? 1 : 0` and check.tf's
// `local.deploy_check` do; Readiness never turns DeployJob off for a job
// that already exists, so an address the state holds is never dropped this
// way.
//
// Keys are quoted with strconv.Quote, as newImport does, and for the same
// reason (config.SecretNameRE and config.WorkflowNameRE restrict every key
// to [a-z0-9-], where strconv.Quote and HCL's quoting agree).
//
// It is a hand-maintained mirror, so it is pinned rather than trusted:
// internal/infra/testdata/repo_root_addresses.golden holds the address list
// of a real `terraform plan` of the real root, and
// TestRepoAddressesMatchTheRealPlanGolden fails when a module gains a
// resource this function does not name (Review Focus 8 of
// docs/plans/2026-10-14-config-transitions.md).
func (rs RepoSpec) Addresses() []string {
	const root = "module.repo."
	q := strconv.Quote
	var out []string
	add := func(s string) { out = append(out, s) }
	operators := rs.Installation.Operators
	// The roots read the tfvars' installation.launchers, which RepoVars
	// writes as the launchers and the operators together (tfvars.go's
	// launchersWithOperators): an operator launches too.
	launchers := launchersWithOperators(rs.Installation.Launchers, operators)

	// modules/repo/secrets.tf
	for _, logical := range slices.Sorted(maps.Keys(rs.Secrets)) {
		add(root + "google_secret_manager_secret.this[" + q(logical) + "]")
	}
	// The operator grants are for_each over setproduct(secret, operator),
	// with the role appended to the key.
	for _, logical := range slices.Sorted(maps.Keys(rs.Secrets)) {
		for _, member := range operators {
			add(root + "google_secret_manager_secret_iam_member.operator[" + q(logical+" "+member+" secretVersionAdder") + "]")
			add(root + "google_secret_manager_secret_iam_member.operator[" + q(logical+" "+member+" viewer") + "]")
		}
	}
	// modules/repo/registry.tf
	add(root + "google_artifact_registry_repository.images")
	for _, member := range operators {
		add(root + "google_artifact_registry_repository_iam_member.operator_images[" + q(member) + "]")
	}
	// The installation's shared image registry (design
	// docs/forge-authorship-images/shared-images.md §2.6, §11): this
	// repository's build account writes it, and each of its job accounts
	// reads it. Both grants are declared without a gate, so they are
	// addresses of every repository root.
	add(root + "google_artifact_registry_repository_iam_member.build_shared")
	for _, key := range slices.Sorted(maps.Keys(rs.Workflows)) {
		add(root + "google_artifact_registry_repository_iam_member.job_shared[" + q(key) + "]")
	}
	// modules/repo/build.tf
	add(root + "google_service_account.build")
	for _, logical := range rs.BuildSecrets {
		add(root + "google_secret_manager_secret_iam_member.build[" + q(logical) + "]")
	}
	add(root + "google_artifact_registry_repository_iam_member.build_images")
	add(root + "google_artifact_registry_repository_iam_member.build_tag_mover")
	add(root + "google_artifact_registry_repository_iam_member.build_base")
	// #362's remote repository: the build's reader grant on the ghcr.io
	// mirror, declared without a gate (build.tf), so it is an address of
	// every repository root.
	add(root + "google_artifact_registry_repository_iam_member.build_ghcr")
	add(root + `google_project_iam_member.build["log_writer"]`)
	add(root + `google_project_iam_member.build["build_submitter"]`)
	add(root + "google_service_account_iam_member.build_self")
	for _, member := range operators {
		add(root + "google_service_account_iam_member.operator_build[" + q(member) + "]")
	}
	add(root + "google_storage_bucket_iam_member.build")
	// modules/repo/check.tf
	if rs.Check != nil && rs.Check.DeployJob {
		add(root + "google_cloud_run_v2_job.check[0]")
		add(root + "google_cloud_run_v2_job_iam_member.check_invoker[0]")
		add(root + "google_cloud_scheduler_job.check[0]")
	}
	// modules/repo/workflows.tf -> modules/workflow
	for _, key := range slices.Sorted(maps.Keys(rs.Workflows)) {
		ws := rs.Workflows[key]
		p := "module.repo.module.workflow[" + q(key) + "]."
		add(p + "google_service_account.job")
		for _, member := range operators {
			add(p + "google_service_account_iam_member.operator[" + q(member) + "]")
		}
		add(p + "google_storage_bucket_iam_member.job")
		add(p + "google_storage_bucket_iam_member.job_tokens")
		for _, logical := range uniqueSecrets(ws) {
			// The credentials the service holds and the job has given up are
			// its to hold once the installation has opted in (D145):
			// modules/workflow/sa.tf drops the job's accessor on each — the
			// model credentials by C1.18, the GitHub App's private key by C2.3
			// — and the job's mount goes with it (Readiness drops it, and the
			// module's job_mounts repeats the rule), so the mirror must not
			// name a grant the spec no longer produces — it would read as one
			// that survives a retirement. The App key's half is inert until
			// config.ServiceHeldSecretNames names it (C2.2's release, review
			// finding F1).
			if rs.Installation.CredentialsService != nil && mountsServiceHeldSecret(ws, logical, rs.BitbucketProxied) {
				continue
			}
			add(p + "google_secret_manager_secret_iam_member.job[" + q(logical) + "]")
		}
		if rs.Installation.CredentialsService != nil {
			// The job account's roles/run.invoker on the service (D135).
			add(p + "google_cloud_run_v2_service_iam_member.credentials[0]")
		}
		if ws.Vertex {
			add(p + "google_project_iam_member.vertex[0]")
		}
		if ws.DeployJob {
			add(p + "google_cloud_run_v2_job.this[0]")
			for _, member := range launchers {
				add(p + "google_cloud_run_v2_job_iam_member.launcher[" + q(member) + "]")
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// mountsServiceHeldSecret reports whether the logical secret name is one
// whose credential the credentials service holds and the job's own account has
// given up for ws: every secret_env entry naming it carries such a credential
// (config.IsServiceHeldSecretEnv — a model credential by its variable, a name
// config.ServiceHeldSecretNames lists by its name), so the service holds it
// once an installation opts in (D145, D146) and the job's account keeps no
// accessor on it (C1.18, C2.3). The App key's half of that list is empty until
// C2.2's release (review finding F1), so this answers false for it today and
// the job keeps the accessor it needs to read the key.
//
// proxied is the repository's own fact (RepoSpec.BitbucketProxied), and it is
// the second way a mount comes to be held: a name config.ProxiedGitSecretNames
// lists is the credentials service's for a repository the published node
// admits with proxy: true (B1.8, D500), and for that repository alone — the
// same name in another repository's spec, proxied false, keeps its accessor
// (G9). It is a parameter rather than a read of the spec so that this function
// stays the one statement of the module's rule, and so that the rule is
// visible where it is called.
//
// It is the same question modules/workflow/sa.tf's job_secret_names asks, one
// mount at a time, and it is written the same way — a name survives if ANY
// entry that mounts it is not one the service holds — so the mirror
// (RepoSpec.Addresses) and the Terraform cannot disagree about which grants
// the spec produces. One secret mounted under two variables, one of them a
// credential the service holds, is a shape config already refuses
// (ReservedSecrets, ProviderBySecret), and this answers the same way either
// way. The proxied clause is the module's is_proxied_git, folded in: it reads
// the logical name and the repository's fact and never the variable, so it is
// the same answer for every entry naming the name.
func mountsServiceHeldSecret(ws WorkflowSpec, name string, proxied bool) bool {
	proxiedGit := proxied && slices.Contains(config.ProxiedGitSecretNames, name)
	held := false
	for env, logical := range ws.SecretEnv {
		if logical != name {
			continue
		}
		if !config.IsServiceHeldSecretEnv(env, name) && !proxiedGit {
			return false
		}
		held = true
	}
	return held
}

// Schedule is the check's daily cron schedule: a minute between 05:00 and
// 06:59 UTC derived from the slug, so repositories don't all build at once.
func Schedule(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	n := binary.BigEndian.Uint16(sum[:2]) % 120
	return fmt.Sprintf("%d %d * * *", n%60, 5+n/60)
}
