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
	"net/url"
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
	// The budget backend (M9b R10), on every job whose budget is not off:
	// the project's Realtime Database, the restricted web API key (not a
	// secret) that signs a run's token in, and how long the backend may be
	// unreachable before the run halts (set only when the config sets it).
	RTDBURLEnv        = runner.RTDBURLEnv
	FirebaseAPIKeyEnv = runner.FirebaseAPIKeyEnv
	BudgetGraceEnv    = runner.BudgetGraceEnv
)

// githubGitUser is the HTTPS username of a GitHub App installation token.
const githubGitUser = "x-access-token"

// Inputs are what a repository's spec is computed from.
type Inputs struct {
	LC   *localcfg.Config
	Repo string // owner/name
	Cfg  *config.Config
	// RepoURL is the https clone URL; empty means the provider's usual one.
	RepoURL string
	// GitHubAppID overrides the local config's github_app_id.
	GitHubAppID string
	// Installation are the installation root's outputs. Fields left empty
	// take the values Go gives a new installation.
	Installation InstallationOutputs
}

// InstallationOutputs are the installation root's outputs, as `terraform
// output -json` names them.
type InstallationOutputs struct {
	// ProjectName is the Fugaro project's name; empty in the outputs of an
	// installation applied before M9a.
	ProjectName             string   `json:"project_name"`
	RunsBucket              string   `json:"runs_bucket"`
	RegistryHost            string   `json:"registry_host"`
	BaseRegistry            string   `json:"base_registry"`
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
}

// RoleIDs are the custom roles' full names, projects/<p>/roles/<id>.
type RoleIDs struct {
	Launcher       string `json:"launcher"`
	JobRunner      string `json:"job_runner"`
	BuildSubmitter string `json:"build_submitter"`
	// TagMover is empty in the outputs of an installation applied before
	// the role existed; fugaro init --repo refuses those.
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
	Vertex          bool              `json:"vertex"`
	DeployJob       bool              `json:"deploy_job"`

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
}

// RepoInstallation are the installation values a repository root needs.
type RepoInstallation struct {
	// ProjectName comes from the installation's outputs; the repository
	// root checks every job's FUGARO_PROJECT against it.
	ProjectName             string      `json:"-"`
	RunsBucket              string      `json:"runs_bucket"`
	RegistryHost            string      `json:"registry_host"`
	BaseRegistry            string      `json:"base_registry"`
	SchedulerServiceAccount string      `json:"scheduler_service_account"`
	RoleIDs                 RepoRoleIDs `json:"role_ids"`
	Launchers               []string    `json:"launchers"`
	Operators               []string    `json:"operators"`
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

	c.baseBranch = in.Cfg.Git.BaseBranch
	if hasLocal && local.BaseBranch != "" {
		c.baseBranch = local.BaseBranch
	}
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
	return c, nil
}

// localRepo is the local config's entry for repo, matched as fugaro run
// matches it (owner/name without regard to case).
func localRepo(lc *localcfg.Config, repo string) (localcfg.Repo, bool) {
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return localcfg.Repo{}, false
	}
	for _, k := range slices.Sorted(maps.Keys(lc.Repos)) {
		if c, err := task.CanonicalRepo(k); err == nil && c == want {
			return lc.Repos[k], true
		}
	}
	return localcfg.Repo{}, false
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
func repoURL(provider, repo, given string) (string, error) {
	if given == "" {
		host := map[string]string{gitprov.KindGitHub: "github.com", gitprov.KindBitbucket: "bitbucket.org"}[provider]
		if host == "" {
			return "", userErr("git.provider %q is not bitbucket or github", provider)
		}
		return "https://" + host + "/" + repo + ".git", nil
	}
	u, err := url.Parse(given)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", userErr("the repository URL %s is not an https URL without credentials", gcp.RedactURL(given))
	}
	if gitprov.KindForURL(given) != provider {
		return "", userErr("the repository URL %s is not on %s's host", given, provider)
	}
	// The URL is where the build and the check clone from, so it must be
	// this repository, not only this provider.
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return "", userErr("%v", err)
	}
	if got, err := task.CanonicalRepo(strings.Trim(u.Path, "/")); err != nil || got != want {
		return "", userErr("the repository URL %s is not %s", given, repo)
	}
	return given, nil
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
		"FUGARO_BUCKET":      "gs://" + c.bucket,
		"FUGARO_BACKEND":     backend.CloudRun,
		"FUGARO_GCP_PROJECT": c.lc.GCPProject,
		"FUGARO_PROJECT":     c.inst.ProjectName,
		"FUGARO_REGION":      c.lc.Region,
	}
}

func (c *repoCtx) registryPath() string { return c.registryHost + "/" + gcp.RegistryRepoID(c.slug) }

// Workflow is the spec of one workflow of the repository.
func Workflow(in Inputs, name string) (WorkflowSpec, error) {
	c, err := resolve(in)
	if err != nil {
		return WorkflowSpec{}, err
	}
	return c.workflow(name)
}

// GitSecret is the logical name of the git credential of provider (the
// secret every job mounts), and false for a provider that is neither
// Bitbucket nor GitHub. The one place that names them.
func GitSecret(provider string) (string, bool) {
	switch provider {
	case gitprov.KindBitbucket:
		return "bitbucket-token", true
	case gitprov.KindGitHub:
		return "github-app-key", true
	}
	return "", false
}

// SecretMount is one secret a workflow's job mounts: the logical name (what
// fugaro secrets set takes) and the variable it becomes.
type SecretMount struct{ Logical, Env string }

// SecretMounts is what the job of workflow name (w) mounts, in order: the
// git provider's credential (gitSecret), the one Claude credential agent.auth
// names (none for vertex), the keys of the model providers the owner's local
// config allows repo to send code to (only with api-key: a run does not mix
// credentials), and the workflow's own secrets. It is the one place that
// decides it, for the job's spec and for the secrets init asks for.
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
func SecretMounts(gitSecret string, cfg *config.Config, name string, w config.Workflow, lc *localcfg.Config, repo string) ([]SecretMount, error) {
	out := []SecretMount{{gitSecret, config.ReservedSecrets[gitSecret]}}
	switch cfg.Agent.Auth {
	case "oauth":
		out = append(out, SecretMount{"claude-oauth-token", config.ReservedSecrets["claude-oauth-token"]})
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
		out = append(out, SecretMount{s.Name, s.Env})
	}
	return out, nil
}

func (c *repoCtx) workflow(name string) (WorkflowSpec, error) {
	w, ok := c.in.Cfg.Workflows[name]
	if !ok {
		return WorkflowSpec{}, userErr("fugaro.yaml has no workflow %q", name)
	}
	if issues := gcp.CheckResources(w.Resources); len(issues) > 0 {
		return WorkflowSpec{}, userErr("workflow %s: resources.%s: %s", name, issues[0].Field, issues[0].Message)
	}
	lc, slug := c.lc, c.slug
	saID := gcp.ServiceAccountID(slug, name)
	ws := WorkflowSpec{
		Job:            gcp.JobName(slug, name),
		ServiceAccount: ServiceAccount{AccountID: saID, DisplayName: gcp.JobSADisplayName(slug, name)},
		Image:          gcp.ImageName(c.registryPath(), slug, name) + ":latest",
		CPU:            strconv.Itoa(w.Resources.CPU),
		Memory:         w.Resources.Memory,
		TaskTimeoutS:   int((w.Timeouts.Total.Duration + backend.TaskTimeoutSlack) / time.Second),
		Env:            c.platformEnv(),
		SecretEnv:      map[string]string{},
		BucketCondition: Condition{
			Title:      gcp.BucketConditionTitle(saID),
			Expression: gcp.BucketCondition(c.bucket, gcp.JobBucketPrefixes, slug),
		},
		Vertex:    c.in.Cfg.Agent.Auth == "vertex",
		DeployJob: true,

		Name:                name,
		Slug:                slug,
		ServiceAccountEmail: serviceAccountEmail(saID, lc.GCPProject),
		Labels:              map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: c.label, gcp.LabelWorkflow: name},
		SecretIDs:           map[string]string{},
		GitSecret:           c.gitSecret,
	}
	if lc.Registry != "" {
		ws.LegacyImage = gcp.ImageName(lc.Registry, slug, name)
	}
	if ws.Vertex {
		ws.Env["CLOUD_ML_REGION"] = lc.Region
		ws.Env["ANTHROPIC_VERTEX_PROJECT_ID"] = lc.GCPProject
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
	mounts, err := SecretMounts(c.gitSecret, c.in.Cfg, name, w, lc, c.in.Repo)
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
	ws.BuildSecrets = []string{c.gitSecret}
	for _, s := range w.Secrets {
		ws.BuildSecrets = append(ws.BuildSecrets, s.Name)
	}
	slices.Sort(ws.BuildSecrets)
	ws.BuildSecrets = slices.Compact(ws.BuildSecrets)
	// Every variable a secret is mounted as, so the runner can register
	// them all for redaction before bootstrap.
	ws.Env[runner.SecretEnvsVar] = strings.Join(slices.Sorted(maps.Keys(ws.SecretEnv)), ",")
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

// budgetEnv adds the project's budget to a workflow job's env: the token
// cap and the allow-list, the cap when there is one and the price overrides
// when there are any, whatever the mode (they are the owner's ceiling, which
// a repository's committed policy is clamped to), and the mode itself when it
// is not off. The check job calls no model and gets none of it.
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
	if mode := lc.BudgetMode(); mode != localcfg.BudgetOff {
		env[BudgetModeEnv] = mode
		// Budget off never touches the backend, so no job learns of it.
		if b := lc.Budget; b != nil {
			if b.RTDBURL != "" {
				env[RTDBURLEnv] = b.RTDBURL
			}
			if b.FirebaseAPIKey != "" {
				env[FirebaseAPIKeyEnv] = b.FirebaseAPIKey
			}
			if b.Grace > 0 {
				env[BudgetGraceEnv] = b.Grace.String()
			}
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
			SchedulerServiceAccount: c.inst.SchedulerServiceAccount,
			RoleIDs:                 RepoRoleIDs{JobRunner: c.inst.RoleIDs.JobRunner, BuildSubmitter: c.inst.RoleIDs.BuildSubmitter, TagMover: c.inst.RoleIDs.TagMover},
			Launchers:               c.inst.Launchers,
			Operators:               c.inst.Operators,
		},
		GitHubAppID:              c.appID,
		RepoURL:                  c.repoURL,
		BaseBranch:               c.baseBranch,
		GitUser:                  c.gitUser,
		BuildServiceAccountEmail: serviceAccountEmail(buildID, lc.GCPProject),
		RegistryPath:             c.registryPath(),
	}
	var checked []string
	for _, name := range slices.Sorted(maps.Keys(in.Cfg.Workflows)) {
		ws, err := c.workflow(name)
		if err != nil {
			return RepoSpec{}, err
		}
		rs.Workflows[name] = ws
		maps.Copy(rs.Secrets, ws.SecretIDs)
		rs.BuildSecrets = append(rs.BuildSecrets, ws.BuildSecrets...)
		if in.Cfg.Workflows[name].Rebuild.Defaults().Check != "off" {
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
	return rs, nil
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
// images (kind -> image) that isn't in the installation's base registry,
// in kind order. The build accounts can read only that registry, so every
// build from such an image would fail at its pull; the check job's own pull
// would still work, which hides it until then.
func BaseImageWarnings(bases map[string]string, outs InstallationOutputs) []string {
	registry := outs.RegistryHost + "/" + outs.BaseRegistry
	var ws []string
	for _, kind := range slices.Sorted(maps.Keys(bases)) {
		if ref := bases[kind]; ref != "" && !strings.HasPrefix(ref, registry+"/") {
			ws = append(ws, fmt.Sprintf("the local config's base_images.%s %s is not in the installation's base registry %s, the only one the build accounts can read, so the image builds would fail at its pull: push the base image to %s and set base_images.%s to it",
				kind, ref, registry, registry, kind))
		}
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
		kind := c.in.Cfg.Workflows[name].Base
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
		Image:           bases[c.in.Cfg.Workflows[checked[0]].Base],
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

// Schedule is the check's daily cron schedule: a minute between 05:00 and
// 06:59 UTC derived from the slug, so repositories don't all build at once.
func Schedule(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	n := binary.BigEndian.Uint16(sum[:2]) % 120
	return fmt.Sprintf("%d %d * * *", n%60, 5+n/60)
}
