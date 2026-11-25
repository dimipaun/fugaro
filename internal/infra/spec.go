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
	"github.com/dimipaun/fugaro/internal/catalog"
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
	// RemoteEnv, set to the repository's credential-free clone URL on a job
	// whose image carries no checkout of this repository: a checkout: clone
	// or checkout: shared workflow's job, and a job whose image is a
	// prebuilt catalog entry. fugaro exec clones one from this URL — its
	// --remote flag defaults to it (runner.RemoteEnv) — and without it such
	// a job has no remote to clone at all: a clone job fails bootstrap with
	// "this job has no repository to clone", a prebuilt entry's with "no git
	// checkout and no remote to clone" (#1752, review C1).
	RemoteEnv = runner.RemoteEnv
	// ImageEntryEnv, set to the name of the project image_catalog entry a
	// job's image was deployed from, on a job whose image the catalog chose
	// — prebuilt and built alike — and on no other (docs/image-env,
	// reconciliation F5/F6, D1371). It is the fact that says where the run's
	// image came from: a catalog entry built from a Dockerfile is
	// indistinguishable from a repository's own derived image, so the
	// deploy's knowledge of the entry (WorkflowSpec.CatalogEntry, which is
	// json:"-" and stays in this process) has to travel into the container
	// for the runner to have it. The runner reads it, so the constant lives
	// there (runner.ImageEntryEnv), the way CheckoutEnv and RemoteEnv do.
	ImageEntryEnv = runner.ImageEntryEnv
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

// PlatformEnv is every key above, together: fugaro update's job-config-copy
// check (internal/cli/upgrade_cloud.go) excludes exactly this set, named
// here from the same constants platformEnv and the vertex branch use, so it
// never has to guess it or drift from them.
var PlatformEnv = []string{BucketEnv, BackendEnv, GCPProjectEnv, ProjectEnv, RegionEnv, VertexRegionEnv, VertexProjectEnv}

// JobEnvNames is every environment variable name a deployed job of a workflow
// may carry in its plain environment, both shapes a job comes in together: a
// per-repository workflow job (the template Workflow builds) and a shared
// project job (projectJobEnv, plus the two values that follow from the
// profile). The names are the constants the two builders write, so this list
// cannot drift from either of them without the constants moving first; the
// secrets' own variables are not here, because a secret travels by name under
// secretVariables and never as a plain variable's value.
//
// It exists for the Cloud Batch backend (batch-run-identity F8, BR8). A Cloud
// Run run is handed the deployed job's own environment by the platform: the
// execution's override is merged into it, so nothing has to restate it. A
// Batch job is created per run from nothing, so the launcher reads the
// installation's values under exactly these names and passes them to the
// backend (batch.Options.JobEnv), which writes them into the task's variables
// — the run's whole environment on a VM. internal/infra's
// TestABatchJobCarriesEveryVariableADeployedJobDoes holds the list to a real
// template and the job to the list: a variable the template gains and this
// list does not is a variable a Batch run would not carry. Not to be written
// to.
var JobEnvNames = []string{
	BucketEnv, BackendEnv, GCPProjectEnv, ProjectEnv, RegionEnv,
	VertexRegionEnv, VertexProjectEnv,
	ImageEntryEnv, CheckoutEnv, RemoteEnv,
	providers.EnvGitHubAppID, providers.EnvCredentialsServiceURL,
	ComputePricesEnv,
	BudgetModeEnv, MaxRunTokensEnv, AllowedModelsEnv, MaxRunUSDEnv,
	ModelPricesEnv, RTDBURLEnv, FirebaseAPIKeyEnv, BudgetGraceEnv,
	ModelProvidersEnv, runner.SecretEnvsVar,
}

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
	// Intake is the installation's secrets intake (DS2.2, D1931): the Cloud Run
	// service the dynamic-delivery class writes its per-user and project
	// containers through, or null while the installation has not opted in.
	// Nothing reads it yet — the stage that deploys the intake is DS4.1's — and
	// it is here because DecodeOutputs reads the root's outputs by name (the
	// field is the contract that test pins), so an output with no field is an
	// output this binary cannot report.
	Intake *IntakeService `json:"intake"`
	// VoiceService is the installation's voice hop (P5.3.3): the Cloud Run
	// service a paired phone calls before its speech reaches a provider, or null
	// while the installation has not opted in. Nothing in Go reads its URL —
	// the hop's callers mint their ID tokens for the installation's Firebase
	// project and not for the service's address, which is why
	// CredentialsServiceSpec's block and the module's own variable carry no url
	// member (voice.tf) — and the field is here because DecodeOutputs reads the
	// root's outputs by name (the field is the contract that test pins), so an
	// output with no field is an output this binary cannot report.
	VoiceService *VoiceService `json:"voice_service"`
	// CatalogCheckServiceAccount is the project's catalog check job's account
	// (D1148), its email, or "" on an installation whose project publishes no
	// catalog: the value every repository root reads from its own tfvars
	// (RepoInstallation.CatalogCheckServiceAccount) to grant that account
	// actAs on its build account. Empty in the outputs of an installation
	// applied before the job existed, which is what keeps those repository
	// roots planning exactly what they planned.
	CatalogCheckServiceAccount string `json:"catalog_check_service_account"`
}

// CredentialsService is the installation's credentials service (D146): the
// Cloud Run service that alone holds the model credentials. One type serves
// both ends of the contract — the installation root's output of that name and
// the repository root's installation.credentials_service — because both carry
// the same values.
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
	// after the one that created the service. A repository root reads it not
	// at all, so the object it writes into its own tfvars is built without it
	// (repoCredentialsService).
	URL string `json:"url,omitempty"`
	// Account is the email of the service's own account (D2074), which a
	// repository root grants roles/secretmanager.secretAccessor per mounted
	// workflow secret to — the service reads the container a run asks it for
	// (B3.4). It is written into the repository root's tfvars and never read
	// by this binary: the module turns it into the IAM member.
	//
	// Empty in the outputs of an installation applied before the field
	// existed, and empty is inert there: modules/workflow plans the grant for
	// no account, so a repository root against such an installation plans
	// exactly what it planned, and the next `fugaro update` fills it in.
	Account string `json:"account,omitempty"`
}

// IntakeService is the installation root's `intake` output (DS2.2, D1931): the
// service's name, its region and its own URL, in the same shape
// CredentialsService carries. Null means the installation has not opted in, and
// the URL is what the update stage writes back into var.intake on the apply
// after the one that created the service — Cloud Run assigns the URL, and
// nothing inside the resource can read its own uri. A caller cannot mint an ID
// token for the intake without it.
type IntakeService struct {
	Name   string `json:"name"`
	Region string `json:"region"`
	URL    string `json:"url,omitempty"`
}

// VoiceService is the installation root's `voice_service` output (P5.3.3), in
// the same shape IntakeService carries, and it is the ONE of the three with a
// URL nothing read backs: the hop's callers are phones whose Firebase ID
// tokens are minted for the installation's Firebase project, so there is no
// audience to fill and no second apply for it. The URL is published for the
// operator and for a check that reads an installation back, and no part of
// this binary writes it into a variable.
type VoiceService struct {
	Name   string `json:"name"`
	Region string `json:"region"`
	URL    string `json:"url,omitempty"`
}

// CredentialsServiceServes is the switch: whether a repository's runs may go
// through the installation's credentials service, and therefore whether that
// repository's jobs stop mounting a model credential of their own
// (dropModelMounts) and its launch is handed the service's address
// (internal/cli's credentialsServiceLaunchEnv). All three conditions must
// hold (docs/credentials-service/default-on.md §1.5):
//
//   - installed: the installation has the service's block. It is the
//     precondition rather than a condition — without a service there is
//     nothing to switch to — and each caller asks the source it has: the
//     installation root's outputs where a repository root's spec is built,
//     the local config's own record of them where a launch decides.
//
//   - ready: the bundle holds what THIS repository's runs need the service to
//     hand back — a model credential for the cascade, and, for an `api-key` or
//     `oauth` repository, the credential its auth calls for under EVERY account
//     those runs may go out on, beside the git credential its provider needs
//     (internal/cli's credentialsBundleServes judges it). That is the condition
//     that makes the switch safe: the mount it removes is exactly the credential
//     the service must then hand back, and a `cascade` run has no credential of
//     its own to fall back to (D143). It is read from the per-repository record
//     `fugaro update` writes from the bundle AND the auth its plan was built
//     with — a record of an apply, so it carries the auth that apply was made
//     for, and an owner who applies a root before committing the cascade
//     reads false here until a run of its own applies it again — because this
//     package reads no cloud. The record is read through
//     localcfg.Repo.CredentialsServiceOn: an entry with no record at all and
//     one whose record says false are the same answer to the switch, and they
//     differ only where the config is published (internal/localcfg's
//     MergeShared, which must not let a machine that holds no record erase
//     one another machine wrote).
//
//   - auth: the repository's agent.auth is one the service serves:
//     config.AuthCascade, always, and `api-key` or `oauth` under the mode
//     below (B2.3, D498). It is READ LIVE from the repository's fugaro.yaml
//     at every build of its spec and never stored: a stored copy could outlive
//     an edit to the file, and an `oauth` repository whose mounts were dropped
//     would have no App key to open its git provider with while the service
//     refuses to serve it. `vertex` is served by nothing: its calls go to
//     Vertex AI on the job's own service account.
//
//   - mode: the budget mode the published node carries, and it is a condition
//     for `api-key` and `oauth` alone (D2071): the gateway is what reaches the
//     service, and it exists only in `observe` and `enforce`. A repository on
//     a project whose mode is `off` is held off the switch — mounts kept, no
//     invoker grant — because its run would be handed an address its own
//     runner refuses to use, and its job would be left with a mount gone and
//     nothing to read instead. It is read from Inputs.Policy, the node the
//     caller read: nil means this caller read no node (fugaro image, fugaro
//     doctor, --print-vars), and "" is then the answer — held off, the
//     fail-closed direction — while an empty Policy is the different answer
//     "read, and the project has published no block". Neither is a repository
//     left broken: nothing is dropped for an auth the flip list does not carry
//     (config.ServiceServedAuths), and an auth it does carry is held off here
//     only where this answer is false — a mode off, a root applied with the
//     switch off, no install — and then nothing is dropped either, so a
//     held-off `api-key` repository keeps exactly the mounts it had and its
//     run reads its own key, no service being reached (D2071).
//
//     The cascade is deliberately not held to this condition, and that is what
//     keeps the mode's own rule whole: a `cascade` workflow is REFUSED outright
//     when its effective mode is off (config.CheckCascadeGateway, judged at
//     validate and at launch), because the cascade has no in-job path at all.
//     So a cascade repository whose node says off is one whose runs the launch
//     stops before a job is started, whatever this switch answers, and
//     answering off here would put its model mounts back on a job whose next
//     launch is refused — the mounts and the refusal disagreeing over a state
//     neither of them produced.
//
// It is one function on purpose. The plan a repository root writes and the
// address a launcher puts on its job's execution have to come from the same
// answer: a second implementation would be a second answer, and the state
// where the two disagree is a job whose credential was dropped and whose
// launch was told about no service to get it from.
//
// It is the release-1 bridge's arm and no longer the whole answer: a
// repository the project's routing table serves is on the service whatever
// this function says about its auth, and that arm is
// CredentialsServiceServesChains, read beside it where the switch is decided
// (resolve). This one keeps its meaning and its signature — it answers about
// agent.auth, which is what a repository with no table is still served by
// (O7: the flip list and this question are the owner's to retire, not CS11's).
func CredentialsServiceServes(installed, ready bool, auth, mode string) bool {
	if !installed || !ready {
		return false
	}
	if auth == config.AuthCascade {
		return true
	}
	if auth != config.AuthAPIKey && auth != config.AuthOAuth {
		return false
	}
	return mode == config.BudgetObserve || mode == config.BudgetEnforce
}

// CredentialsServiceAddress answers whether this repository's root hands its
// job accounts the installation's credentials service — the block in the
// tfvars, which modules/workflow/sa.tf turns into each job account's
// roles/run.invoker grant, and which the launcher puts on the execution as the
// service's address — while CredentialsServiceServes above answers whether the
// service serves this repository's CREDENTIALS (D3326 as amended by F17).
//
// The two come apart for exactly one repository, and that is the whole of what
// this function exists for: a `vertex` one. A vertex repository's runs read no
// MODEL credential of the service's — the model calls go out on the job's own
// Google credentials through Vertex AI — so the address cannot move its model
// path (internal/runner's serviceSwitched, which admits `api-key` and `oauth`
// alone, F17) and the block carries no drop with it: model_secrets_served stays
// `none` for it (ModelSecretsServed), and the module's own drop is gated on the
// serves fact this function's second answer travels beside
// (RepoInstallation.CredentialsServiceServes). What the address IS for is the
// workflow secrets its runner fetches from the service at bootstrap
// (internal/runner's fetchWorkflowSecrets, D3326).
//
// Its GIT credential the address does move, and that is the owner's ruling of
// 2026-11-24: the variable is the one every git path reads, so a job handed it
// opens its provider with no App key of its own and mints its installation
// tokens here (internal/gitprov/providers). A `vertex` repository gets the
// address without the switch's vouch, so the state where its bundle cannot
// serve that credential is the one to catch: the launch refuses it with the fix
// named (internal/cli's gitCredentialGate), and no root takes a mount away on
// this arm, so nothing is stranded by the refusal. The same is true of the
// Bitbucket proxy — internal/cli's proxiedRepo and internal/runner's
// proxiedBitbucket both read the address off the execution rather than this
// function's second answer, so the Bitbucket repository whose published node
// admits it with `proxy: true` is a proxied launch from here on, its git
// traffic through the service and its job holding no token of its own (B1.13).
// An installation whose node proxies none of its vertex repositories is
// untouched, and which repositories a node proxies is the operator's own
// publication.
//
// It is handed whenever the installation HAS the service — and not only on a
// record's vouch, which is the switch's second condition — because there is
// nothing for the record to vouch for: the record says a root was applied with
// the service serving this repository's credentials, which is false for a
// vertex repository by construction, and a vertex run's fetch fails closed at
// the service if no root has granted it a call yet (D143, internal/runner). So
// a vertex repository is the one repository whose address follows the
// installation's own block rather than the per-repository switch, and `ready`
// is read for the other arms alone.
//
// It is one function, read by both the spec the root plans from and the
// address the launcher puts on the execution, for CredentialsServiceServes'
// reason: a second implementation would be a second answer, and the state
// where the two disagree is a job granted a call the launcher never told it to
// make, or one told to make a call its account holds no grant for.
//
// A repository the switch HOLDS OFF and that is not a vertex one answers false
// here as it does there: an `api-key` or `oauth` repository on a mode-off
// project, or one whose root was applied while its record said off, is handed
// nothing — telling it where the service is would move its model path onto a
// service it keeps its own mount instead of reading (F17, D2071).
func CredentialsServiceAddress(installed, ready bool, auth, mode string) bool {
	if CredentialsServiceServes(installed, ready, auth, mode) {
		return true
	}
	return installed && auth == "vertex"
}

// ModelSecretsServed answers which model secrets the credentials service
// serves to a job — the value of the repository root's model_secrets_served
// input, and the one thing modules/workflow/sa.tf keys the model mount and
// accessor drop on (config.ModelSecretServedAll, ...Anthropic, ...None).
//
// It is a SECOND answer rather than a clause of the switch because the two
// come apart across releases, and that was D2072's first step: from the
// release that hands out the address (B2.3) a switched `api-key` or `oauth`
// repository's runs reached the service while its job went on MOUNTING the
// credential — config.ServiceServedAuths was empty then — so an older
// launcher or an older job image, which reads the mount and knows nothing of
// the service, still worked (G17, D2072). The flip (B2.7) filled the list,
// which is what makes this answer `anthropic` for those two: only the two
// named model credentials go, and a provider's key stays with the job. The
// two answers stay apart because they decide different things — the switch is
// the address and the accessor, this is the mount — and because the cascade,
// which has no in-job credential to read at all, takes every mount away with
// the URL.
//
// `serves` is CredentialsServiceServes' answer for this repository, and the
// three cases are:
//
//   - `none`: the switch is off for this repository. Nothing is served, so
//     every mount stays and the module drops no accessor.
//   - `all`: a repository whose every call the service makes with a credential
//     of its own — the cascade's job since the first release, whose runner
//     reads every model credential from the service, and since CS11 a
//     repository the project's routing table serves, whose run's gateway holds
//     no key at all and whose chain names the source per call (R2). That is
//     also the answer this branch shipped before the input existed, and the
//     module's own default for a root applied with an older tfvars.
//   - `anthropic`: an auth in config.ServiceServedAuths — the service hands
//     back the Claude OAuth token and the Anthropic key, and only those two:
//     a provider's key is forwarded, not attached, by the route that spends it
//     (R6, D1634), so its mount is the run's own and stays.
//
// `serves` gates the table, and the order matters: `serves` is the switch's
// answer, which CredentialsServiceServesChains holds to installed-and-ready
// just as CredentialsServiceServes does, so `plan.Serves` on a repository of an
// installation with no service — or one whose root has not recorded readiness —
// is a table this run is not told about. Reading the table first would drop the
// job's model mounts there and leave it with CredentialsService nil beside them,
// which is exactly the state the switch exists to prevent: no mount and no
// address is a run that reaches no credential at all.
//
// So a repository the table serves AND the switch reaches is `all` however its
// file names its auth — including a `vertex` one, which no arm below answers for
// and which has no credential to mount either way — and a repository the table
// does not serve is the bridge's, `none` or `anthropic` by the list above.
// `plan` is ChainPlanFor's answer for this repository.
func ModelSecretsServed(plan ChainPlan, serves bool, auth string) string {
	if !serves {
		return config.ModelSecretServedNone
	}
	if plan.Serves {
		return config.ModelSecretServedAll
	}
	if auth == config.AuthCascade {
		return config.ModelSecretServedAll
	}
	if slices.Contains(config.ServiceServedAuths, auth) {
		return config.ModelSecretServedAnthropic
	}
	return config.ModelSecretServedNone
}

// repoCredentialsService is the installation's credentials service as a
// REPOSITORY root reads it: the name, the region and the account the workflow
// module turns into each job account's roles/run.invoker grant, the
// per-container secretAccessor the service's own account gets (D2074), or nil
// when this repository is handed no address at all
// (CredentialsServiceAddress). The URL is left behind because the module's
// variable carries no such attribute — an object conversion in Terraform
// silently DROPS an extra attribute rather than refusing it, so passing it
// would read as a value the repository root agreed on while nothing used it.
//
// The account travels with an empty value when the installation's outputs
// predate it, and that is deliberate: the module plans no grant for an empty
// account, so the release that added the field changes no repository root's
// plan until the installation itself is applied (outputs.tf carries it from
// the service's own resource, so the next apply of that root does).
//
// nil is the whole of the first flip, and for the repositories it holds off it
// still needs no Terraform change: a repository the switch does not serve and
// that is no vertex one is handed no address (CredentialsServiceAddress), so
// it plans exactly what every repository planned before the service existed —
// its job accounts keep the accessors and the mounts, and none of them is
// granted roles/run.invoker.
//
// The block and the DROPS it used to carry are one value each since IS9
// (D3326): the block is the address (an invoker grant and the launcher's
// FUGARO_CREDENTIALS_SERVICE_URL), and RepoInstallation.CredentialsServiceServes
// is the drop, because a vertex repository is handed the first without the
// second — the module reads both, and the two answers travel together so a
// plan cannot take an accessor away from a job whose credential the service
// does not serve.
func repoCredentialsService(s *CredentialsService, address bool) *CredentialsService {
	if s == nil || !address {
		return nil
	}
	return &CredentialsService{Name: s.Name, Region: s.Region, Account: s.Account}
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
	// Vertex is whether this job's account is planned the Vertex permission
	// (modules/workflow/sa.tf's one aiplatform member), and it is the fact this
	// spec's own address set is read from, so the grant and the address cannot
	// disagree. It is the repository reaching Vertex AND the project's mode
	// allowing the grant (VertexGrantForJob): under `enforce` no run in flight
	// can be using it, so release 1 plans none, and the two Vertex names in Env
	// go with it — an agent on `agent.auth: vertex` reads them, and that run is
	// the one the gateway refuses first.
	Vertex    bool `json:"vertex"`
	DeployJob bool `json:"deploy_job"`
	// StageAccount is whether this is a mixed stage account's own variant job
	// (mixedStageAccountJob): the job a run's `review`, `adversarial` and
	// `triage` stages go out on when an api-key repository mixes a Claude
	// account with the provider key its coder's stages keep (B0's one mix,
	// docs/mixed-credentials/spec.md §11). Those stages reach Anthropic on
	// that account's token through the RUNNER and never through the run's
	// gateway, so the credentials service has no call of theirs to serve and
	// B2.7's flip leaves this job's Claude OAuth mount and its accessor alone
	// where it takes the rest away (dropModelMounts, modules/workflow/sa.tf's
	// anthropic arm). False for every other job of the repository, the base
	// variant included.
	StageAccount bool `json:"stage_account"`
	// VaultSecretNames are the logical names of the workflow's OWN secrets
	// whose values the credentials service fetches for this job (B3.8, S3,
	// config.VaultFetchedSecretClasses): sorted, and empty while no class of
	// secrets is fetched or while this repository is one the switch holds off.
	//
	// The names are here because the module needs them and cannot derive them:
	// the job mounts none of them, so var.workflow.secret_env no longer names
	// them, and the credentials service still has to be able to READ each one's
	// container — it is the service, and not the job, that fetches the value now
	// (D2074's grant, B3.4's fetch). The containers stay declared and the job's
	// own accessors on them go (modules/workflow/sa.tf drops both through this
	// list, and internal/infra's dropVaultMounts takes the mounts away here).
	VaultSecretNames []string `json:"vault_secret_names,omitempty"`

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
	// Warnings are what the image selection has to say about this
	// workflow's job without refusing it (D1146): a pinned catalog entry
	// that does not cover the tags the job needs is used anyway, and this
	// is the sentence a caller prints to say so. They are json:"-" because
	// nothing outside the process that built this spec reads them.
	Warnings []string `json:"-"`
	// CatalogEntry is the project image catalog's entry the job's image
	// comes from, "" when the workflow runs no catalog entry. CatalogPrebuilt
	// is whether that entry's source is a prebuilt reference
	// (config.SourceImage): Image is then the entry's own reference, which no
	// Fugaro build writes and no shape can recognise (isCatalogImage), so
	// readiness asks this rather than the image.
	CatalogEntry    string `json:"-"`
	CatalogPrebuilt bool   `json:"-"`
	// CatalogContext is whether that entry's source names a build context.
	// It carries the fact and not the path, because the one reader that has
	// it is readiness's skew gate (imageContextShipped): the release that
	// first reads a context is the release that flips that gate, and until
	// then every job whose image comes from such an entry is held back.
	CatalogContext bool `json:"-"`
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
	// project-wide one; design docs/project-secrets/spec.md §5, §7), so
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
	// while it has not opted in, and while this repository is handed no
	// address at all (D146, D147, CredentialsServiceAddress): the workflow
	// module grants each job account roles/run.invoker on it — that grant and
	// the launcher's FUGARO_CREDENTIALS_SERVICE_URL are the whole of what this
	// member means — and modules/repo/build.tf grants the build account the
	// same on it (B4.4).
	CredentialsService *CredentialsService `json:"credentials_service,omitempty"`
	// CredentialsServiceServes is whether that service serves THIS
	// repository's credentials (CredentialsServiceServes), which is the half
	// of the switch the module's drop is keyed on: the job's model-secret
	// accessors and mounts go, and the service's own account gets a
	// secretAccessor on each secret the job mounts (D135, D145, D2074), while
	// this is true.
	//
	// It is a pointer and omitted, and that is what keeps every tfvars written
	// before this input existed planning exactly what it planned: the module
	// declares it optional and TRUE, because a root applied then carried the
	// block only for a repository the switch served and planned every drop
	// with it. Written, it is the answer for the block beside it — false for
	// the one repository handed the address without being served, a `vertex`
	// one (D3326 as amended by F17) — and it is written whenever the block is,
	// so a plan can never read the module's default where Go had an answer.
	CredentialsServiceServes *bool `json:"credentials_service_serves,omitempty"`
	// CatalogCheckServiceAccount is the project's catalog check job's account
	// (D1148), its email, from the installation's outputs, or "" on an
	// installation whose project publishes no catalog. It is what
	// modules/repo/catalogcheck.tf grants actAs on this repository's build
	// account to, so a build of this repository's catalog entry runs as this
	// repository. Omitted from the tfvars when empty, so a repository root
	// planned against an installation with no job writes exactly what it
	// wrote before the grant existed.
	CatalogCheckServiceAccount string `json:"catalog_check_service_account,omitempty"`
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

	// Shared is whether a deployed profile admits this repository (M6,
	// docs/shared-jobs-default): the repository runs on a shared project job
	// instead of the per-repository jobs its own root deploys, which is the
	// structural precondition the owner's answer to D2303 puts in place of a
	// retire gate, and the fact M7 reads to stop applying that root.
	//
	// A deployed profile is one the published node defines for a provider a
	// shared job is deployed for (SharedJobProvider, the same predicate the
	// deployer skips a profile by and the launcher refuses a shared job with),
	// and it admits the repository when projectpolicy.Admits says so — the
	// profile's own provider, the project's allow_repos and the profile's own
	// list, all three. A repository no profile admits is unmarked, and so is
	// one whose provider no profile of the node serves. The profiles are
	// walked in name order and the first admission answers, so a node answers
	// the same way on every read.
	//
	// The admission is the whole rule, deliberately: this is a record of what
	// the project's policy says about the repository, not a second copy of the
	// launcher's decision about a run. Which job a workflow lands on is
	// launchProjectJobFor's (the workflow's own job_profile, its checkout mode
	// while a shared job cannot hold it), and a mark computed from that would
	// have to guess at a run that has not happened. What reads it asks the
	// rest of its own question (M7: the repository's workflows, the images
	// stage, the warning).
	//
	// It changes no Terraform input and no behaviour on its own: it is
	// json:"-" the way the spec's other in-process facts are
	// (ProjectSecretCandidates, AccountProfiles), so the repository root plans
	// exactly what it planned before the field existed and M6 merges before M7
	// and M2 without a root applying differently. nil Policy — a caller that
	// read no node (fugaro image, fugaro doctor, --print-vars) — marks
	// nothing, because a node it never read deploys no profile it could be
	// admitted to; an empty Policy is the different answer "read, and the
	// project published no profile", and it marks nothing either.
	Shared bool `json:"-"`

	// ModelSecretsServed is which model secrets the credentials service serves
	// to this repository's jobs (config.ModelSecretServedAll, ...Anthropic,
	// ...None), written into the tfvars' repo object as the workflow module's
	// model_secrets_served input. It is ModelSecretsServed's answer for this
	// repository — the switch's verdict for it and its agent.auth — and the
	// module drops a model mount's ACCESSOR on exactly the secrets the value
	// names, which is the other half of the drop Go makes here
	// (dropModelMounts): a job account without an accessor on a secret its
	// container still mounts is a job Cloud Run refuses to create.
	//
	// It is the flip's value rather than the switch's (D2072): `none` for a
	// repository the switch holds off, `all` for the cascade's job, and
	// `anthropic` — the two named model credentials and no provider key — for
	// an auth config.ServiceServedAuths holds. So a switched `api-key` or
	// `oauth` repository plans the two Claude mounts and their accessors away
	// while every provider key it mounts stays, and its runs reach the service
	// for the credential the two mounts gave up (G17, R6, D1634).
	//
	// It is omitted when empty, and the module's default is then `all`: the
	// value this branch shipped before the input existed, which is what a root
	// applied with an older tfvars means, and what a spec built without going
	// through Repo plans. Every spec Repo builds carries one of the three
	// values, so no plan of this release leaves the question to the default.
	ModelSecretsServed string `json:"model_secrets_served,omitempty"`

	// ReachesVertex is whether this repository's runs reach Vertex at all: its
	// file's `agent.auth: vertex`, or a chain of the project's table that may
	// serve it naming Vertex (ChainPlan.Vertex — a repository the service
	// reaches Vertex for, which names no Vertex in its own file at all).
	// UsesVertex is this, and it is what `fugaro update` records into the local
	// config as repos.<repo>.vertex and what the installation enables the
	// Vertex AI API from.
	//
	// It is a fact of its own because the GRANT comes apart from it: under
	// `budget.mode: enforce` release 1 plans no job's account the Vertex
	// permission (VertexGrantForJob) while the API must stay on and the
	// credentials service must keep its own permission, the service being what
	// calls Vertex on its behalf (D3174). It is json:"-" the way the spec's
	// other in-process facts are: no Terraform input carries it, and the
	// repository root plans exactly what it planned before this field existed.
	ReachesVertex bool `json:"-"`

	// ProjectSecretCandidates are the logical names this repository may use
	// project-wide, mapped to the secret ID they would resolve to
	// (gcp.ProjectSecretID). Candidate data only: being here says nothing
	// about whether anything actually resolves there yet -- Readiness
	// decides that against live state, and produces ProjectSecrets.
	// json:"-": this map never reaches tfvars.
	ProjectSecretCandidates map[string]string `json:"-"`
	// ProjectSecretNodeOnly names the entries of ProjectSecretCandidates that
	// only the published node admits: this machine's local config holds no
	// entry and no list that makes the name project-wide for this repository,
	// so the node's own block is the whole answer (SD17, G15).
	//
	// The distinction decides the command that stores such a name, and that is
	// the only reason this map exists: `fugaro secrets set <name>` answers its
	// scope from the local config alone (internal/cli's projectScopeName), so
	// for a name only the node admits it refuses --project-wide and its bare
	// form writes the repository's own copy -- the copy that then wins over
	// the project's forever, the silent flip project-wide secrets exist to
	// avoid (design docs/project-secrets/spec.md §5). What such a name is
	// given instead is the run's own door, whose secrets stage stores the
	// value in the container its own node-read decision picks: SecretFix's
	// node-only branch, and initsecrets' Left, which spells the same line.
	// json:"-": this map never reaches tfvars.
	ProjectSecretNodeOnly map[string]bool `json:"-"`
	// ProjectSecrets are the logical names Readiness resolved to the
	// project copy for this repository (repo has no version, project does),
	// mapped to the project secret ID. This is the field that reaches
	// tfvars as repo.project_secrets.
	ProjectSecrets map[string]string `json:"project_secrets,omitempty"`

	// DefaultAccountUnused says no run of this project's configuration holds
	// the unsuffixed default Claude account (localcfg's
	// RunsOnDefaultClaudeAccount): every account a launch picks is a named one,
	// so the bare claude-oauth-token is a token nothing mounts and its absence
	// is not a repository's missing secret. Readiness drops the mount of that
	// name from every job and SecretCommands prints no step for it, because a
	// step would send the owner to mint a token for an account their own
	// configuration never chooses — the one repository copy no project-wide
	// copy can replace (the bare name is deliberately outside D2's sweep). The
	// job itself is unchanged and still deployed: an explicit
	// `fugaro run --token default` targets it, and the account's own allow-list
	// is what refuses that launch, not a job that is not there. json:"-": no
	// Terraform input reads it.
	DefaultAccountUnused bool `json:"-"`

	// AccountProfiles names, for each named Claude account this repository's
	// jobs mount, the profile whose claude_accounts already lists it
	// (config.ClaudeAccountProfile: the first in name order, and no entry when
	// no profile serves the account). SecretCommands prints `fugaro accounts
	// add <name>` for such a token, and that command has to say which profile
	// gains the name: it refuses to pick one in a project with several
	// (accountsAddProfile), and with exactly one it would add the account to
	// that profile and deploy a job — a change storing the token never made.
	// Naming the profile that already serves the account leaves that half of
	// the command a no-op. A fact of the project's local config, carried here
	// because the step's command is the one thing that needs it. json:"-":
	// this map never reaches tfvars.
	AccountProfiles map[string]string `json:"-"`

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
	// credentialsServiceOn is the per-repository fact of the credentials
	// service's switch, decided once for the repository by resolve: its root
	// hands the workflow module the service to grant an invoker on and its
	// launch is handed the service's address, or neither. Which model mounts
	// the switch takes away is a SECOND answer (ModelSecretsServed), read
	// beside it wherever a mount is dropped.
	credentialsServiceOn bool
	// credentialsServiceAddressed is CredentialsServiceAddress's answer for
	// this repository: whether its root hands the workflow module the service
	// at all (RepoInstallation.CredentialsService, and the build account's own
	// invoker grant with it). It is TRUE for every repository
	// credentialsServiceOn is, and for a `vertex` one of an installation that
	// has the service, whose runs reach it for their workflow secrets and no
	// credential of theirs (D3326, F17). credentialsServiceOn stays the
	// narrower answer and is the one every mount drop reads; the two are read
	// apart and travel together into the tfvars.
	credentialsServiceAddressed bool
	// budgetMode is the mode the published node carries, read once by resolve
	// from Inputs.Policy: the switch's fourth condition for an `api-key` or
	// `oauth` repository (D2071), and the fact VertexGrantForJob reads to take
	// a job's Vertex permission away on an enforcing project. It is "" where
	// the caller read no node.
	budgetMode string
	// chain is what the project's routing table decides for this repository,
	// read once by resolve (ChainPlanFor): whether a chain of it may serve this
	// repository — which is what puts its runs on the credentials service
	// beside the switch's own answer, CredentialsServiceServesChains — whether
	// it reaches Vertex, and whether the project publishes a table at all.
	chain ChainPlan
	// providerKeysServed and stageAccountServed are R12's two facts as this
	// repository's entry in the local config records them, read once by
	// resolve: the owner's own `fugaro update` judged each of them and wrote
	// the answer there, and this root's plan hands the fact to the job from
	// that record (KS19, F31). They are the RECORD's values and not a second
	// judgement — the bundle, the deployed service's release and the published
	// allow-list are not readable here (internal/infra reads no cloud), which
	// is exactly why the record exists.
	//
	// Neither is honoured unless the switch is on, and that is a condition
	// rather than the record's own: a fact that says the service serves this
	// repository's provider keys is only usable where the job was planned to
	// reach the service at all (an address, an invoker grant), so a config
	// hand-edited to state the fact with the switch off — no run writes that
	// state, but a file is a file — plans no fact and the job keeps the key it
	// mounts, which is the direction every reader of these records fails in.
	providerKeysServed, stageAccountServed bool
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
	if in.Policy != nil {
		c.budgetMode = in.Policy.Budget.Mode
	}
	c.chain = ChainPlanFor(lc, in.Repo)
	installed, ready := c.inst.CredentialsService != nil, local.CredentialsServiceOn()
	// Two arms, one answer: the release-1 bridge, which reads agent.auth, and
	// the table, which does not (ChainPlanFor). A repository the table serves
	// is on the service whatever its file names — and a repository the bridge
	// would serve keeps its answer, so an installation whose table names no
	// chain for it moves nothing (MG1's per-repository migration).
	c.credentialsServiceOn = CredentialsServiceServes(installed, ready, in.Cfg.Agent.Auth, c.budgetMode) ||
		CredentialsServiceServesChains(installed, ready, c.chain)
	// The ADDRESS is a third arm beside the two: wherever the service serves
	// this repository's credentials the job is also told where the service is,
	// and a `vertex` repository is told even when nothing serves it (D3326 —
	// its runner fetches its workflow secrets through the address). Read as the
	// serves answer OR the vertex arm rather than as a second predicate, so the
	// launcher reading the same function cannot name a service this plan did
	// not grant the account.
	c.credentialsServiceAddressed = c.credentialsServiceOn ||
		CredentialsServiceAddress(installed, ready, in.Cfg.Agent.Auth, c.budgetMode)
	c.providerKeysServed = local.ProviderKeysServedOn()
	c.stageAccountServed = local.StageAccountServedOn()
	return c, nil
}

// credentialsServiceServesFact is RepoInstallation.CredentialsServiceServes as
// this repository's tfvars carry it: this repository's own answer
// (credentialsServiceOn) whenever the block above it is written, and nil
// whenever it is not — because a tfvars with no block names no service the
// drops could be about, and the module reads the member only through the block.
//
// Writing the answer beside the block rather than leaving it to the module's
// default is what makes the two halves one document: the default is TRUE
// (every tfvars written before this input existed meant "the block is the
// switch"), so an omitted member beside a written block would plan the drop
// for the one repository handed the address without being served (D3326).
func (c *repoCtx) credentialsServiceServesFact() *bool {
	if c.inst.CredentialsService == nil || !c.credentialsServiceAddressed {
		return nil
	}
	serves := c.credentialsServiceOn
	return &serves
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
	localProxies := entry.Proxying() && entry.AllowsData(c.in.Repo)
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

// sharedRepo is RepoSpec.Shared's answer for this repository, which SharedRepo
// computes: the node is read through the operator's credentials by the caller
// and handed in as Inputs.Policy, exactly as the proxy's half is
// (resolveBitbucketProxied, same reason: internal/infra holds no client that
// could read one and Repo is a pure function of its Inputs).
//
// A repository with a workflow whose tools live in its own derived image
// (checkout other than clone) keeps its per-repository jobs until the shared
// job carries that image (SharedJobBaked), because fugaro run launches that
// workflow on the per-repository job: enrolling the repository as shared would
// leave update creating no job for the launch to find.
func (c *repoCtx) sharedRepo() bool {
	if !SharedJobBaked && c.in.Cfg != nil {
		for _, w := range c.in.Cfg.Workflows {
			if w.Checkout != config.CheckoutClone {
				return false
			}
		}
	}
	return SharedRepo(c.in.Policy, c.provider, c.in.Repo)
}

// SharedJobBaked is whether a shared project job carries a baked workflow's
// tools; S10 flips it in the change that deploys the catalog default image.
// It is a variable so S4's and S10's tests admit the workflow the way
// production will.
var SharedJobBaked = false

// buildServes is whether the credentials service serves the secrets of class
// to this repository's BUILDS (B4.9, the S4 flip; D2076, D2077): the class
// list says which classes are served at all (config.BuildServedSecretClasses)
// and credentialsServiceOn says whether this repository is one the switch
// serves — the same two halves every other flip here reads, and the same
// answer modules/repo/build.tf's local.build_mounts_served and
// local.check_mount_served compute from the class list it spells out and the
// per-repository block Go writes into the tfvars.
//
// Go's half of the flip is the tfvars: the build account's accessor list
// (WorkflowSpec.BuildSecrets, which RepoSpec gathers) empties with the class
// "build", and the check job's mount empties with either class — the job runs
// as the build account, so the accessor its mount is read under is the one
// the class "build" takes away (C2.3). internal/backend/gcp's BuildRequest
// reads the same list for the request's own mounts, beside the service
// address the build paths in internal/cli put on the spec.
func (c *repoCtx) buildServes(class string) bool {
	return c.credentialsServiceOn && config.BuildServesClass(class)
}

// buildMountsServed is buildServes for the one answer both of the build's own
// clauses read: whether the build's mounts — the request's availableSecrets,
// the account's accessor list and the build step's own secretEnv — leave this
// repository. They go with the class "build" alone, the class of a workflow's
// own build secrets, because a request that kept any of them would need the
// accessor the class list takes away (D2077).
func (c *repoCtx) buildMountsServed() bool { return c.buildServes(config.BuildSecretClassBuild) }

// checkMountServed is whether the daily check job gives its own mount up: it
// runs as the build account and mounts the repository's git credential (the
// class "git"), and that credential's accessor is the build account's, so
// either class takes the mount away (C2.3) — the job then fetches the
// credential there instead, which is what the service's address in its
// environment is for (B4.5, checkGitCredential).
func (c *repoCtx) checkMountServed() bool {
	return c.buildServes(config.BuildSecretClassGit) || c.buildServes(config.BuildSecretClassBuild)
}

// SharedRepo is RepoSpec.Shared's answer for a repository whose spec a caller
// does not build: whether a deployed profile of the published node admits it.
// RepoSpec.Shared's own doc comment is the whole rule; this is that walk for
// the callers that hold the node and the repository's name but no Inputs to
// build a spec with — the images stage and the refresh behind it, which decide
// by it whether a checkout still converges per-repository images (M7,
// docs/shared-jobs-default).
//
// A nil policy marks nothing, exactly as it does there: a caller that read no
// node has read no profile the repository could be admitted to. An error is
// never a refusal either, and the two it can carry are both unreachable from
// the callers here: a name no provider could address (which task.Slug refuses
// long before) and Admits' own error, which is a name the node itself defines.
func SharedRepo(p *projectpolicy.Policy, provider, repo string) bool {
	if p == nil {
		return false
	}
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return false
	}
	for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
		if !SharedJobProvider(p.Profiles[name].Provider) {
			continue
		}
		if ok, err := p.Admits(name, canonical); err == nil && ok {
			return true
		}
	}
	return false
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
		o.Launchers = slices.Clone(lc.Terraform.Operators)
	}
	if o.Operators == nil {
		o.Operators = slices.Clone(lc.Terraform.Admins)
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
	return platformEnvFor(c.lc, c.inst.ProjectName, c.bucket)
}

// platformEnvFor is platformEnv's five variables for the jobs that have no
// repoCtx: the installation's own names, its Fugaro project's name and the
// runs bucket its record lives in. The project's catalog check job gets these
// too (CatalogCheckSpec.Env), and they have to be the same bytes the
// repository check job's env carries — the module's own preconditions compare
// two of them against its variables.
func platformEnvFor(lc *localcfg.Config, projectName, bucket string) map[string]string {
	return map[string]string{
		BucketEnv:     "gs://" + bucket,
		BackendEnv:    backend.CloudRun,
		GCPProjectEnv: lc.GCPProject,
		ProjectEnv:    projectName,
		RegionEnv:     lc.Region,
	}
}

func (c *repoCtx) registryPath() string { return c.registryHost + "/" + gcp.RegistryRepoID(c.slug) }

// catalogImage is where a catalog entry's image is (D1152): the
// installation's one shared registry, one image per entry, named
// catalog-<entry> with the moving :latest tag the job spec names. An entry
// whose source is an image the project builds elsewhere is not built by the
// catalog at all and is used exactly as it is written.
//
// It is built here rather than read from the build half of this package
// (PIC4), which does not exist yet; when it lands, this is the reader the two
// halves must agree through, so the name is written once.
func (c *repoCtx) catalogImage(e config.Entry) string {
	return catalogEntryImage(c.registryHost, c.inst.SharedRegistry, e)
}

// effectiveCatalog is the catalog the repository's specs are computed from:
// the local configuration's when it states one, the published project node's
// otherwise (localcfg's keep rule, D1140), and nil when the project has
// neither.
func (c *repoCtx) effectiveCatalog() *config.Catalog {
	var published *config.Catalog
	if c.in.Policy != nil {
		published = c.in.Policy.ImageCatalog
	}
	return c.lc.ImageCatalogEffective(published)
}

// effectiveImageMap is the repo-to-image map the repository's specs are
// computed from: the local configuration's when it states one, the published
// project node's otherwise (localcfg's keep rule, D1906), and nil when the
// project maps none.
func (c *repoCtx) effectiveImageMap() map[string]string {
	var published map[string]string
	if c.in.Policy != nil {
		published = c.in.Policy.ImageMap
	}
	return c.lc.ImageMapEffective(published)
}

// imageRequest is the question this repository's workflows are resolved with:
// the project's effective catalog and map, this repository, the workflow and
// the installation's backend.
//
// The backend is the installation's own, read from the config and not written
// as a constant: the docker rule is a rule about the backend the job will run
// on (D1149), and an installation that names cloud-batch is judged by the rule
// for the backend it actually runs on rather than by Cloud Run's.
func (c *repoCtx) imageRequest(w config.Workflow) catalog.ImageRequest {
	return catalog.ImageRequest{
		Catalog:   c.effectiveCatalog(),
		ImageMap:  c.effectiveImageMap(),
		Canonical: c.in.Repo,
		Workflow:  w,
		Backend:   catalog.Backend(c.lc.Backend()),
	}
}

// jobImage is the image one workflow's job runs, resolved by the one resolver
// (catalog.JobImage, D1900, IS1). It is called where the job spec is built —
// deploy time — because the job's image is fixed once the job exists (F7): a
// job that started on an image that cannot do its work would die mid-run
// instead of being refused, and the refusals the resolver raises are
// conditions of the project's configuration rather than of one run.
//
// Which workflows reach this at all is the same resolver the launcher, doctor
// and the image commands ask, so no two of them can disagree about which jobs
// the project's images are (F21).
//
// An entry it answers with is then held to D1191: a baked job's image has to
// hold this repository's checkout, which only an entry a Fugaro build writes
// can promise, so a prebuilt one is refused here rather than deployed. The
// resolver answers with an entry only for a baked checkout — a clone or shared
// job's image is the installation's own — so that rule is asked of every entry
// it answers with, whatever the workflow's checkout kind was spelled as.
func (c *repoCtx) jobImage(name string, w config.Workflow) (catalog.Selection, error) {
	sel, err := catalog.JobImage(c.imageRequest(w))
	if err != nil {
		return catalog.Selection{}, userErr("workflow %s: %w", name, err)
	}
	if sel.Source == catalog.SourceCatalog && !catalog.BuildsImage(sel.Entry) {
		return catalog.Selection{}, unbuiltOnBaked(name, sel.Entry)
	}
	return sel, nil
}

// unbuiltOnBaked is D1191's refusal, raised where the job spec is built and not
// by the job (F14). It names the entry, what it is built from and the two ways
// out, because that is the choice the person reading it is making: run a
// clone-shaped job, which clones the repository at launch instead of carrying
// it, or give the workflow an entry this installation can build. The clone half
// says which keys it costs — a clone workflow may carry neither image_name: nor
// needs: (internal/config's own refusal) — and the entry half says the workflow
// has to be pointed at the entry, because a pin or the project's default would
// keep choosing the one being refused.
//
// The refusal is the design's premise (reconciliation §D1191), and not a claim
// about one job: what such a job does is decided by the same writer that marks
// its checkout kind, and this tree's writer gives the remote to clone and shared
// only, so a baked job whose entry is prebuilt holds no checkout and no remote
// and its first minute fails at bootstrap (gitops.OpenOrClone: "has no git
// checkout and no remote to clone"). A tree that also writes the remote for a
// prebuilt entry makes the same shape run by cloning at launch. Either way what
// the refusal costs is not that job's: fugaro setup and fugaro update --repo
// build the repository's job specs through this path, so a repository whose
// workflows carry such a pin meets this sentence there and edits the workflow or
// the catalog.
func unbuiltOnBaked(workflow string, e config.Entry) error {
	return userErr("workflow %s: the catalog entry %s has source kind %s, which Fugaro builds no image from, so its image holds no checkout of this repository — and a checkout: %s job runs an image that holds one; use checkout: %s, which cannot carry image_name: or needs:, or point the workflow at a catalog entry Fugaro builds (fugaro image add <name> --dockerfile <owner/name:path> --tags <tags>)",
		workflow, e.Name, e.Source.Kind, config.CheckoutBaked, config.CheckoutClone)
}

// Workflow is the spec of one workflow of the repository, its default
// Claude account (config.DefaultClaudeAccount). Repo builds one WorkflowSpec
// per account a repository may choose among (docs/named-oauth-tokens/spec.md
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
// config.IsServiceHeldSecretName and the workflow module's
// service_held_secret_names answer "is this the App key?" with (the model
// credentials' own half of that question is config.IsServiceHeldModelEnv). Review finding
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
// Claude credential agent.auth names — none for vertex, token's own derived
// secret (config.ClaudeAccountSecret) for oauth, and for an api-key job the
// stage account's token when a stage key named one — the keys of the model
// providers the owner's local config allows repo to send code to (only with
// api-key, and under the credential's own container where the installation
// declares a runner-path key credential of that provider's name,
// runnerKeyCredential), and the workflow's own secrets. It is the one place
// that decides it, for the job's spec and for the secrets init asks for.
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
			p := lc.Providers[pn]
			if !p.AllowsData(repo) {
				continue
			}
			// A runner-path key credential of the provider's own name is that
			// provider's key (D1670, MC23): the value the credential's own
			// declaration bounds is stored under the credential's derived secret
			// name, and this mount is the half of the run that carries it to the
			// call. The provider's own .secret is superseded and not mounted
			// beside it: one variable carries one key, and a second container for
			// it would be a copy no run reads.
			logical := p.Secret
			if s, ok := runnerKeyCredential(lc, pn, repo); ok {
				logical = s
			}
			out = append(out, SecretMount{logical, p.SecretEnv()})
		}
		// The one mix the owner's B0 ruling admits: the stage account's own
		// job mounts its token (mixedStageAccounts). That mount is the
		// exception B2.7's flip has to carry: it is the STAGE's credential and
		// not the run's, so the service never serves it and the mount stays
		// (mixedStageAccountJob, dropModelMounts).
		if mixedStageAccountJob(cfg, lc, repo, token) {
			out = append(out, SecretMount{config.ClaudeAccountSecret(token), config.ReservedSecrets["claude-oauth-token"]})
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

// runnerKeyCredential is the container holding the key of the runner-path key
// credential that serves provider pn for repo, when this installation declares
// one (D1670, MC23). The provider of that name is the one a pool member of the
// name is served by (config.PoolMemberServesModel, whose openrouter member
// indexes providers by its own name), so the credential's declaration and the
// provider's entry are the two halves of one wiring, and this function is where
// they meet.
//
// The container is the credential's OWN derived secret name, which is what
// `fugaro accounts add <name> --type openrouter` writes the value into: a
// provider's .secret may not name it (config.ValidateModelProviders refuses the
// name there, because the name is derived from a credential and never chosen),
// so the mount is built from the credential's declaration rather than from the
// provider entry's own field.
//
// It is bounded the way the credential is: claude_accounts.<name>.allow_data_to
// names the repositories its key may be sent to, and a repository the list does
// not name gets the provider's own .secret mounted as before. A credential of
// the service's path (anthropic) is not one of these — the credentials service
// serves it out of the bundle and this job mounts nothing for it — and a name
// this config declares no key credential for is the ordinary provider key.
func runnerKeyCredential(lc *localcfg.Config, provider, repo string) (string, bool) {
	secret := config.CredentialSecret(provider)
	if _, ok := runnerKeyCredentialName(lc, secret); !ok || !lc.ClaudeAccounts[provider].AllowsData(repo) {
		return "", false
	}
	return secret, true
}

// RunnerKeyCredential is runnerKeyCredential for a reader outside this package:
// the container the runner-path key credential of provider's own name holds the
// key in, for the repositories claude_accounts.<provider>.allow_data_to names.
//
// The reader is internal/cli's copy of the installation's model keys into the
// credentials service's bundle (KS12): which container a repository's runs are
// served a provider's key from is this function's answer and nobody else's, and
// an entry the copy wrote holding a key the repository's own job does not mount
// is a key no released run is served (F27, R6). It is exported rather than
// spelled a second time there so the two cannot disagree.
func RunnerKeyCredential(lc *localcfg.Config, provider, repo string) (string, bool) {
	return runnerKeyCredential(lc, provider, repo)
}

// runnerKeyCredentialName is the credential a logical secret is the derived
// container of, when this installation declares it as a RUNNER-path key
// credential (D1670, MC23). It is the one place that reads the half off a
// container name, so the two rules that meet on it cannot disagree: the mount
// SecretMounts builds for a provider of that name, and the project-secret
// candidate ProjectSecretCandidateFromNode answers for -- a name the first
// mounts and the second refuses is a mount Readiness can never resolve, which
// is a job it refuses to deploy (or, for a job that exists, one whose mount is
// dropped) over a key the operator did store.
//
// The serving path is the clause that keeps the other half out: a
// service-served credential (anthropic) is read from the credentials service's
// bundle and mounted nowhere, so its container is no job's secret and no
// candidate; and a name in the namespace with no such credential declared
// belongs to no mount either.
func runnerKeyCredentialName(lc *localcfg.Config, secret string) (string, bool) {
	name, ok := config.CredentialBySecret(secret)
	if !ok {
		return "", false
	}
	cred, declared := config.CredentialNamed(lc.Credentials, name)
	if !declared || !config.KeyCredentialType(cred.Type) || config.CredentialServingPath(cred.Type) != config.CredentialPathRunner {
		return "", false
	}
	return name, true
}

// WorkflowAccounts are the Claude accounts each of repo's workflows gets a
// job for: under oauth every account config.AllowedClaudeAccounts allows repo
// (the default first), and mixedStageAccounts' accounts beside the default
// under api-key, and the default alone under every other auth. RepoSpec
// deploys one job per (workflow, account) from it, and RequiredSecrets reads
// the same list, so the two cannot disagree about which tokens are mounted.
//
// A name the local config declares a KEY credential is not one of them
// (config.ClaudeAccountTokenNames), and the reason is the mount rather than the
// name: every job of this set exists to carry an account's Claude token,
// config.ClaudeAccountSecret(name) as CLAUDE_CODE_OAUTH_TOKEN
// (SecretMounts), and a key credential's value is stored under its own derived
// name instead (config.CredentialSecret, D1670) — no run ever writes a version
// to the account's token container for such a name, so a job built for it
// would mount an empty token, `fugaro secrets ls` would flag a MISSING secret
// no `claude setup-token` step could fill (SecretCommands), and a run launched
// on the variant would fail in the credential. The name's claude_accounts
// entry keeps the meaning it has for a key credential — the bound on where the
// key may be sent — and what carries the key to a call is the serving half of
// its type, never a Claude account job of its own (D1670, MC23).
func WorkflowAccounts(cfg *config.Config, lc *localcfg.Config, repo string) []string {
	switch cfg.Agent.Auth {
	case "oauth":
		return config.ClaudeAccountTokenNames(lc.Credentials, config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo))
	case "api-key":
		out := []string{config.DefaultClaudeAccount}
		for _, name := range mixedStageAccounts(cfg, lc, repo) {
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
		return config.ClaudeAccountTokenNames(lc.Credentials, out)
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
// docs/named-oauth-tokens/spec.md §5). Only the job's own identity (Job,
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
	// The job's image is the resolver's answer (catalog.JobImage, D1900), told
	// in this repository's own references: the installation's base image for a
	// checkout: clone job, which has no per-repository derived image at all
	// (design generic-tool.md §2, G3) — the same image for every token variant
	// of the workflow; the shared dependency-layer image for a checkout:
	// shared job, whose digest only the operator's machine writes onto the job
	// (design forge-authorship-images/shared-images.md §6, C8b1) and which is
	// therefore the derived tag here until that refresh exists, the per-
	// repository image readiness.go's non-clone branch asks the operator to
	// build; a catalog entry for a job the project's images decide; and the
	// repository's own derived tag for a project that has no image for the job
	// yet (O4 b).
	sel, err := c.jobImage(name, w)
	if err != nil {
		return WorkflowSpec{}, err
	}
	image := sel.Image(catalog.Images{
		Base:    lc.BaseImage(config.BaseKind),
		Repo:    gcp.ImageName(c.registryPath(), slug, name) + ":latest",
		Catalog: c.catalogImage,
	})
	warnings, entry := sel.Warnings, sel.Entry
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
		Vertex:    c.reachesVertex() && VertexGrantForJob(c.budgetMode),
		DeployJob: true,

		Name:                name,
		Slug:                slug,
		ServiceAccountEmail: serviceAccountEmail(saID, lc.GCPProject),
		Labels:              map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: c.label, gcp.LabelWorkflow: variant},
		SecretIDs:           map[string]string{},
		GitSecret:           c.gitSecret,
		Warnings:            warnings,
		CatalogEntry:        entry.Name,
		CatalogPrebuilt:     entry.Name != "" && entry.Source.Kind == config.SourceImage,
		CatalogContext:      entry.Name != "" && entry.Source.Context != "",
	}
	if lc.Registry != "" {
		ws.LegacyImage = gcp.ImageName(lc.Registry, slug, name)
	}
	// The image's origin, written for the run on EVERY workflow job: the
	// catalog entry this job's image came from, and empty on a job the
	// catalog did not choose (a repository-built image, a checkout: clone or
	// shared job, whose entry is empty, because the resolver answers those
	// with the base and shared layers and no entry). The
	// key is written even when empty because the runner reads os.Environ,
	// which includes a derived image's own ENV: a repository Dockerfile that
	// sets FUGARO_IMAGE_ENTRY would otherwise run with a stamp the deploy
	// never wrote, whereas a key the job sets overrides whatever the image
	// holds. A run reads an empty stamp as "no entry" and passes none of its
	// image's own variables, which is the fail-closed direction; a job
	// deployed before this variable existed behaves as it did, and stays open
	// to that forgery, until its next `fugaro update --repo .` redeploys it:
	// the repository stage, which a plain `fugaro update` does not run
	// (docs/image-env, reconciliation D1371).
	ws.Env[ImageEntryEnv] = entry.Name
	// One writer for the job's clone shape: a job whose image holds no
	// checkout of this repository carries the markers the run needs to fetch
	// the source itself, and the value of the kind marker is the workflow's
	// own kind, which is what the runner switches on.
	//
	// The two markers are not one set. FUGARO_CHECKOUT goes on the two kinds
	// whose image is not built from this repository at all — clone, which
	// runs the installation's base image, and shared, which runs the
	// dependency layer alone — and on no other, because it is what makes the
	// run install the checkout's own mise tools and clone blobless.
	// FUGARO_REMOTE goes on those two and on a job whose image is a prebuilt
	// catalog entry, which is a baked workflow: its image is the operator's
	// own reference, shared by every repository that names the entry, so it
	// can carry no repository's checkout and the run has to clone one (#1752).
	// It is not a clone kind: the entry's image holds the tools it was built
	// with, so the run leaves the checkout kind alone and makes a whole
	// clone — the history a follow-up and merge-base need — through
	// gitops.OpenOrClone.
	if w.Checkout == config.CheckoutClone || w.Checkout == config.CheckoutShared {
		ws.Env[CheckoutEnv] = w.Checkout
	}
	if w.Checkout == config.CheckoutClone || w.Checkout == config.CheckoutShared || ws.CatalogPrebuilt {
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
	// A repository the credentials service serves is handed its model
	// credential by the service instead of by its own job (D146, D147), so the
	// job mounts none of the mounts the service serves: modules/workflow/sa.tf
	// drops each job account's accessor, and a mount left behind would fail the
	// job's own deploy. The containers stay declared — see dropModelMounts —
	// so nothing here plans a secret's destruction.
	//
	// Which model mounts go is ModelSecretsServed's answer and not the
	// switch's alone (D2072): the cascade's job gives up every one of them,
	// while an `api-key` or `oauth` repository whose runs reach the service
	// gives up the two Claude mounts and keeps every provider key — the flip
	// (B2.7) is what filled the list that makes that arm reachable, and the
	// arm itself is why the two answers are apart: an `api-key` run still
	// forwards a provider call on its own mounted key (R6, D1634). The value
	// is the same one the tfvars carry (RepoSpec.ModelSecretsServed), so the
	// mount this drops and the accessor the module takes away are one answer.
	//
	// And a mixed stage account's own job is the one job the `anthropic` arm
	// does not reach whole (WorkflowSpec.StageAccount, set just below and read
	// by the drop, the address mirror and the module alike): its Claude OAuth
	// mount is the STAGE's credential, read by the runner for the three stages
	// that go direct to Anthropic rather than through the run's gateway, so the
	// service never serves it and the mount stays — with its accessor, which
	// sa.tf exempts by the same field (B0's one mix,
	// docs/mixed-credentials/spec.md §11).
	//
	// c.credentialsServiceOn and not the block's presence: the installation
	// having a service says nothing about THIS repository. A repository
	// whose agent.auth the service does not serve, or one whose bundle
	// cannot hand its credential back, would be left with no model
	// credential at all — which is the whole of what this gate is for
	// (CredentialsServiceServes).
	ws.StageAccount = mixedStageAccountJob(c.in.Cfg, c.lc, c.in.Repo, token)
	// R12's two facts, from the record the owner's own `fugaro update` wrote
	// for this repository (KS19, F31, C1's names): this job's provider calls
	// may be sent through the service rather than on the key it mounts, and
	// the mixed variant's second account is one the service selects like any
	// named account, so the runner holds no token for it. They are planned and
	// never left to the run to guess, because the two things they are about —
	// the bundle's entries, the deployed service's release and the published
	// allow-list — are the owner's own read and are not in this process.
	//
	// A false fact is not planned at all and not planned as "0": a run reads a
	// fact by the entry's presence and its value together (config.ServedValue),
	// so an absent entry is the one way "not served" is said, and a job planned
	// by an installation that never wrote the record reads exactly as it does
	// today (an absent entry, R12's "any other run uses its mounted key as
	// today").
	//
	// Both are hung off the switch, which is the state they were judged in: a
	// fact is reported to a run by the job it is launched on, and a job with no
	// switch has no service address and no invoker grant, so a fact planned over
	// it would send a provider call to a service that refuses the caller.
	//
	// Whether the FACTS are true is what a released `fugaro update` decided
	// (R12's three conditions and, for the second, the release the service must
	// be at), and release A's `ready` is deliberately not widened by them
	// (F30): the mounts this spec drops below are the switch's answer alone.
	if c.credentialsServiceOn {
		if c.providerKeysServed {
			ws.Env[config.ProviderKeysServedEnv] = config.ServedValue
		}
		if c.stageAccountServed {
			ws.Env[config.StageAccountServedEnv] = config.ServedValue
		}
	}
	if served := ModelSecretsServed(c.chain, c.credentialsServiceOn, c.in.Cfg.Agent.Auth); served != config.ModelSecretServedNone {
		dropModelMounts(&ws, served)
	}
	// And the workflow's OWN secrets go the same way for a repository the
	// service serves once the class list names them (B3.8, S3): the runner
	// fetches their values from the service at bootstrap (B3.4) instead of
	// reading them from its own environment, so the job mounts none of them and
	// the module drops the matching accessor — while the containers stay
	// declared, because the service is what reads them now (D2074's grant,
	// B3.1's record, dropVaultMounts).
	//
	// The class list is the switch (config.VaultFetchedSecretClasses, which has
	// carried VaultSecretClassWorkflow since the flip's second commit; with it
	// empty this call is a no-op), and it is the same list
	// modules/workflow/sa.tf's own clause reads, held to it by that module's
	// guard test. Read beside dropModelMounts because it is the same rule — a
	// mount and an accessor are one answer — for a second class of secret.
	if c.credentialsServiceOn {
		dropVaultMounts(&ws, w, func(name string) bool {
			// The repository's eligibility to use the name project-wide, which
			// is ProjectSecretCandidates' own predicate for this name
			// (ProjectSecretCandidateFromNode, with the two facts only this
			// context has) and so the same answer ignoredRepoSecret and
			// B3.1's record read: a name this says no to is one D2313 leaves
			// storeless here, with no row for the service to serve.
			_, ok := ProjectSecretCandidateFromNode(c.lc, name, c.in.Repo, c.provider, c.in.Policy)
			return ok
		})
	}
	// The build account's accessor list goes with the service (B4.9, D2076,
	// D2077): a build of a served repository names none of these secrets — its
	// credential step fetches the git credential and the workflow's own build
	// secrets there instead (B4.5) — so the list is empty and
	// modules/repo/build.tf drops the accessor with it. It is the same list
	// modules/repo/build.tf reads through local.build_mounts_served, and
	// internal/backend/gcp's BuildRequest reads the same class for the request.
	if !c.buildMountsServed() {
		ws.BuildSecrets = []string{c.gitSecret}
		for _, s := range w.Secrets {
			ws.BuildSecrets = append(ws.BuildSecrets, s.Name)
		}
		slices.Sort(ws.BuildSecrets)
		ws.BuildSecrets = slices.Compact(ws.BuildSecrets)
	}
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
	//
	// The mode written is the one the config *says*, so a config that writes
	// none leaves the job without an entry, as does the word that was a mode
	// until this release — the shape the release behind gave every job of every
	// installation. The local config answers observe for a mode it does not
	// write now (AB2, D3010, localcfg.BudgetMode), and taking that answer here
	// would put a mode entry on every job of every installation that exists,
	// which is AB5's move to make, on purpose and with the readiness note that
	// goes with it ("budgetEnv writes FUGARO_BUDGET_MODE and the grace for every
	// job, observe when unset", F13). AB5 drops this read.
	if b := lc.Budget; b != nil && b.Mode != "" && b.Mode != localcfg.BudgetOff {
		env[BudgetModeEnv] = b.Mode
		if b.Grace > 0 {
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
			ProjectName:                c.inst.ProjectName,
			RunsBucket:                 c.bucket,
			RegistryHost:               c.registryHost,
			BaseRegistry:               c.inst.BaseRegistry,
			GhcrRegistry:               c.inst.GhcrRegistry,
			SharedRegistry:             c.inst.SharedRegistry,
			SchedulerServiceAccount:    c.inst.SchedulerServiceAccount,
			RoleIDs:                    RepoRoleIDs{JobRunner: c.inst.RoleIDs.JobRunner, BuildSubmitter: c.inst.RoleIDs.BuildSubmitter, TagMover: c.inst.RoleIDs.TagMover},
			Launchers:                  c.inst.Launchers,
			Operators:                  c.inst.Operators,
			CredentialsService:         repoCredentialsService(c.inst.CredentialsService, c.credentialsServiceAddressed),
			CredentialsServiceServes:   c.credentialsServiceServesFact(),
			CatalogCheckServiceAccount: c.inst.CatalogCheckServiceAccount,
		},
		BitbucketProxied:     c.bitbucketProxied,
		Shared:               c.sharedRepo(),
		DefaultAccountUnused: !lc.RunsOnDefaultClaudeAccount(),
		// The same answer workflow() dropped the mounts with, read from the
		// same two facts, so the module's accessor drop and this spec's mount
		// drop cannot disagree: a job whose account lost an accessor on a
		// secret its container still mounts is a job Cloud Run refuses to
		// create.
		ModelSecretsServed: ModelSecretsServed(c.chain, c.credentialsServiceOn, in.Cfg.Agent.Auth),
		ReachesVertex:      c.reachesVertex(),

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
		// an oauth workflow (design docs/named-oauth-tokens/spec.md §4 T3):
		// config.AllowedClaudeAccounts is the one place that decides the
		// set, so a name not on it never gets a key here at all (the hard
		// backstop of the allow-list, design §6b.3). Every other auth kind
		// has exactly the default account, except an api-key repository whose
		// stage keys, or whose project default, declare the one mix the
		// ruling admits: that repository gets the account's own variant
		// beside the default, and nothing else changes (WorkflowAccounts).
		// catalogImage is what jobImage answered for the workflow's job,
		// read back from the spec it built rather than asked again: the
		// check below and the job's own image must never disagree about
		// which jobs are the catalog's.
		catalogImage := false
		for _, tok := range WorkflowAccounts(in.Cfg, lc, in.Repo) {
			ws, err := c.workflow(name, tok)
			if err != nil {
				return RepoSpec{}, err
			}
			key := gcp.WorkflowVariant(name, tok)
			rs.Workflows[key] = ws
			maps.Copy(rs.Secrets, ws.SecretIDs)
			rs.BuildSecrets = append(rs.BuildSecrets, ws.BuildSecrets...)
			catalogImage = ws.CatalogEntry != ""
		}
		// The daily check covers the workflow's image, the same image
		// across every token variant: once per logical workflow, not once
		// per variant. A checkout: clone workflow has no derived image to
		// rebuild, so the daily check never covers it, whatever its
		// rebuild.check says. Neither does a workflow a catalog entry gives
		// its image to: the check rebuilds the repository's own derived
		// tag, which that job never runs — a prebuilt entry's image is
		// pushed by the operator, and a built one belongs to the catalog's
		// own check — so a repository whose every workflow runs an entry
		// needs no base image at all.
		w := in.Cfg.Workflows[name]
		if w.Checkout != config.CheckoutClone && !catalogImage && w.Rebuild.Defaults().Check != "off" {
			checked = append(checked, name)
		}
	}
	slices.Sort(rs.BuildSecrets)
	rs.BuildSecrets = slices.Compact(rs.BuildSecrets)
	// The profile that already serves every named account the workflows above
	// resolved a token for: SecretCommands' `accounts add` step names it
	// (RepoSpec.AccountProfiles).
	rs.AccountProfiles = map[string]string{}
	for logical := range rs.Secrets {
		if name, ok := strings.CutPrefix(logical, config.ClaudeOAuthTokenPrefix); ok {
			if profile := config.ClaudeAccountProfile(lc.Profiles, name); profile != "" {
				rs.AccountProfiles[name] = profile
			}
		}
	}
	if len(checked) > 0 {
		if rs.Check, err = c.check(rs, checked); err != nil {
			return RepoSpec{}, err
		}
	}
	rs.ProjectSecretCandidates, rs.ProjectSecretNodeOnly = c.projectSecretCandidates(rs)
	return rs, nil
}

// projectSecretCandidates are the logical names this repository is eligible
// to use project-wide, mapped to the project secret ID each would resolve
// to: every name its workflows (and the check job) mount that
// ProjectSecretCandidate accepts. Being here says nothing about whether the
// name actually resolves to the project copy -- Readiness decides that
// against live state (design docs/project-secrets/spec.md §5).
//
// The second answer is the subset only the node admits (RepoSpec's
// ProjectSecretNodeOnly), asked of each name only once the local config's own
// answer is no: a name the local config already answers for keeps that answer,
// and the node is asked about no other.
//
// It reads the two facts this context holds and no caller of the predicate
// alone has -- the repository's provider and the published node -- so the
// container a job mounts and the container update --repo's secrets stage
// stores into are one answer and cannot disagree (SD17).
func (c *repoCtx) projectSecretCandidates(rs RepoSpec) (map[string]string, map[string]bool) {
	out := map[string]string{}
	var nodeOnly map[string]bool
	add := func(logical string) {
		if id, ok := ProjectSecretCandidate(c.lc, logical, c.in.Repo); ok {
			out[logical] = id
			return
		}
		id, ok := ProjectSecretCandidateFromNode(c.lc, logical, c.in.Repo, c.provider, c.in.Policy)
		if !ok {
			return
		}
		out[logical] = id
		if nodeOnly == nil {
			nodeOnly = map[string]bool{}
		}
		nodeOnly[logical] = true
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
		return nil, nil
	}
	return out, nodeOnly
}

// ProjectSecretCandidate is the one place that answers "may this repository
// use logical project-wide, and what ID would that be": the name is an
// entry of project_secrets that lists repo, or a reserved secret D2 swept
// into project scope with no project_secrets entry at all, or a named Claude
// account's own derived secret whose account lists repo, or a model
// provider's own key whose provider lists repo -- AllowsData uniformly
// across the last three, so §3a's no-list/["*"]-means-any-repository rule
// applies to a provider's key exactly as it does everywhere else -- or,
// last, an entry of workflow_secrets that lists repo, the one source with no
// default at all (D1262). ok is false, id "" otherwise. It is exported
// because fugaro update's secrets stage (before any RepoSpec exists) needs
// the identical answer.
//
// It is ProjectSecretCandidateFromNode with no published node and no
// provider in hand, which is the answer a caller that read no node and knows
// no provider is asking for.
func ProjectSecretCandidate(lc *localcfg.Config, logical, repo string) (id string, ok bool) {
	return ProjectSecretCandidateFromNode(lc, logical, repo, "", nil)
}

// ProjectSecretCandidateFromNode is ProjectSecretCandidate with the two facts
// only the caller has: the repository's git provider, and the project's
// published policy node. It adds one source, the node's bitbucket_token block,
// and it is the read SD17 answers: a machine whose local config holds no
// project_secrets.bitbucket-token entry no longer needs the owner to copy that
// entry to it, because the allow-list the owner published is what admits the
// repository here.
//
// The source is narrow on purpose, and each of its four conditions is a rule
// of its own:
//
//   - bitbucket_token is the only block of the node that is derived from
//     project_secrets (projectpolicy.DeriveBitbucketToken), so it is the only
//     name the node can answer for at all. Every other name's allow-list lives
//     in a local file and nowhere else, and a name the node does not carry
//     must not be read as one that admits nobody -- the local sources above
//     keep the last word on those.
//   - the provider must be bitbucket. The node's allow_repos is
//     bitbucket-qualified by construction (projectpolicy.validBitbucketTokenBlock
//     refuses any other provider), and the canonical form built below is what
//     AdmitRepo compares segment against segment -- so a GitHub repository
//     whose owner/name a Bitbucket entry happens to match would otherwise read
//     as admitted to a credential it does not use. resolveBitbucketProxied
//     guards the same way and for the same reason.
//   - the local config must hold no entry for the name. An entry is the
//     owner's own decision and the file is its source of truth (D854), so a
//     machine that carries one keeps the answer it carries -- including the
//     entry that names another repository, which is not a disagreement to
//     resolve but the answer. This is resolveBitbucketProxied's rule read on
//     the allow-list: an entry that is not there is not a disagreement, and
//     the node is then the only answer.
//   - the node must admit the repository. An empty allow_repos admits nothing
//     (projectpolicy.AdmitRepo), which is the deny-all a published block with
//     an empty list states.
//
// A nil node is a caller that read none -- the local-only callers, and
// update --repo on a machine whose config names no database address -- and
// then nothing here is added, which is the answer every caller gave before
// this function existed.
func ProjectSecretCandidateFromNode(lc *localcfg.Config, logical, repo, provider string, p *projectpolicy.Policy) (id string, ok bool) {
	if lc == nil || logical == "" {
		return "", false
	}
	eligible := config.AllowedForProjectSecret(lc.ProjectSecrets, logical, repo)
	_, listed := lc.ProjectSecrets[logical]
	swept := config.ProjectScopeByDefault(logical)
	if !eligible && swept {
		// D2 (plan Task 2.1 step 2, finding 13): a reserved name the platform
		// itself mounts is eligible project-wide with no per-name list, which
		// is the case this function refused before -- only an entry of
		// project_secrets reached it. An *entry* still decides: an owner who
		// wrote project_secrets.<name>.allow_data_to said who may read it, and
		// "absent or empty means nobody" stays true of a list someone wrote.
		// Without one, the sweep applies, and every repository may read it --
		// the same reach the CLI's default store announces, which is what
		// makes "stored project-wide" mean what a person reads it to mean.
		if !listed {
			eligible = true
		}
	}
	// ClaudeAccountBySecret always resolves the bare claude-oauth-token
	// to DefaultClaudeAccount, which is never a key of the map
	// (ValidateClaudeAccounts refuses it), so the default account's own
	// secret is eligible only through project_secrets above -- the
	// documented shape (design §3).
	account, namedAccount := config.ClaudeAccountBySecret(lc.ClaudeAccounts, logical)
	if !eligible && namedAccount {
		eligible = lc.ClaudeAccounts[account].AllowsData(repo)
	}
	modelProvider, namedProvider := config.ProviderBySecret(lc.Providers, logical)
	if !eligible && namedProvider {
		eligible = lc.Providers[modelProvider].AllowsData(repo)
	}
	// A runner-path key credential's own container is the fifth source, with a
	// named account's shape: the credential is a declaration of this
	// installation's config, its key is what the job mounts for the provider of
	// its name (SecretMounts), and claude_accounts.<name>.allow_data_to is the
	// reach the key may be sent to -- the field `fugaro accounts add --type
	// openrouter` writes before the value is stored under that container
	// (D1670, MC23). The branch is what makes the mount resolvable at all: the
	// container is neither project_secrets nor an account's token nor a
	// provider's .secret, so without it Readiness finds no copy of a name a job
	// mounts, and the command it names for that state is not one the CLI accepts
	// for this namespace.
	//
	// The serving path is what keeps the other half out: a service-served
	// credential's container is read from the credentials service's bundle and
	// mounted nowhere, so it is no job's secret and no candidate here.
	keyCredential, namedKeyCredential := runnerKeyCredentialName(lc, logical)
	if !eligible && namedKeyCredential {
		eligible = lc.ClaudeAccounts[keyCredential].AllowsData(repo)
	}
	claimed := listed || swept || namedAccount || namedProvider || namedKeyCredential
	if !eligible && !claimed {
		// workflow_secrets is the fourth and last source, asked only for a
		// name none of the others claims -- the order is the gate, so a
		// hand-written entry cannot widen a name the sweep, an account or a
		// provider already answers for (D1262). Unlike the sweep this block
		// has no default: an entry is the whole gate, so a name with none
		// mounts nowhere.
		eligible = config.AllowedForWorkflowSecret(lc.WorkflowSecrets, logical, repo)
	}
	if !eligible && projectSecretFromNode(lc, logical, repo, provider, p) {
		eligible = true
	}
	if !eligible {
		return "", false
	}
	return gcp.ProjectSecretID(lc.Name, logical), true
}

// projectSecretFromNode is the published node's half of the answer, and it is
// asked only for the one name the node carries a project_secrets-derived block
// for. The four conditions are ProjectSecretCandidateFromNode's, in its order.
func projectSecretFromNode(lc *localcfg.Config, logical, repo, provider string, p *projectpolicy.Policy) bool {
	if logical != config.BitbucketSecret || p == nil || p.BitbucketToken == nil {
		return false
	}
	if provider != gitprov.KindBitbucket {
		return false
	}
	if _, listed := lc.ProjectSecrets[logical]; listed {
		return false
	}
	canonical, err := projectpolicy.Canonical(provider, repo)
	if err != nil {
		return false
	}
	return projectpolicy.AdmitRepo(p.BitbucketToken.AllowRepos, canonical)
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

// UsesVertex reports whether this repository's runs reach Vertex AI at all,
// which is the fact the installation plans the Vertex AI API and the
// credentials service's own permission from (ChainsNameVertex, and the record
// `fugaro update` writes into the local config's repos.<repo>.vertex).
//
// It is ReachesVertex and deliberately NOT "some workflow's job holds the
// grant": since CS11 a job's Vertex grant is planned away on an enforcing
// project while the API has to stay enabled for the credentials service, so a
// reader that answered from ws.Vertex would disable the API and plan the
// service no permission exactly where the service is the only thing left that
// calls Vertex.
func (rs RepoSpec) UsesVertex() bool { return rs.ReachesVertex }

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
	// A repository the service serves gives the mount up (B4.9, D2076): the
	// job runs as the build account, whose accessor on the credential goes
	// with the build's own mounts, and it fetches the credential from the
	// service instead (B4.5's checkGitCredential, which reads exactly this
	// address off its environment) — so the environment names the service,
	// because a job that mounts nothing and names no service holds no
	// credential at all, and it mounts nothing, so it has no variable to
	// register for redaction either.
	//
	// The address is the local config's own, the same one every job of a
	// switched repository is launched with (internal/cli's
	// credentialsServiceLaunchEnv) and the same block the module's
	// run.invoker grant is planned from: an installation that deploys the
	// service records its address when it does.
	secretEnv := map[string]string{gitEnv: c.gitSecret}
	if c.checkMountServed() {
		secretEnv = map[string]string{}
		if cs := lc.CredentialsService; cs != nil && cs.URL != "" {
			env[providers.EnvCredentialsServiceURL] = cs.URL
		}
		env[runner.SecretEnvsVar] = redactionEnvs(secretEnv)
	}
	return &CheckSpec{
		Job:             gcp.CheckJobName(c.slug),
		Image:           bases[c.in.Cfg.Workflows[checked[0]].BaseKind()],
		SchedulerJob:    gcp.SchedulerJobName(c.slug),
		SchedulerRegion: region,
		Schedule:        Schedule(c.slug),
		Paused:          true,
		// The job's shape, and not the decision to deploy it: that is
		// Readiness's (readiness.go), which after D1900 answers the live
		// cloud -- a repository whose cloud holds no check job is given none
		// (IS2), one whose cloud holds one keeps it, because the state holds
		// its three addresses and a spec that stopped producing them would
		// plan a retirement (retire.go), not a quiet release. True here is
		// what keeps that fail-safe: every ungated reader -- Addresses() on
		// the fixture specs, `fugaro update --repo --print-vars` -- still
		// describes the job, so the only way to lose it is Readiness saying
		// so about the cloud.
		DeployJob: true,
		Env:       env,
		SecretEnv: secretEnv,
		Workflows: checked,
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
// docs/config-transitions/implementation-plan.md).
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
	// repository's build account writes it and holds the installation's tag
	// mover on it — moving a tag needs artifactregistry.tags.delete, which
	// the writer role lacks, and a catalog build (D1152) moves both latest
	// and its content tag there — and each of its job accounts reads it.
	// Every grant is declared without a gate, so all three are addresses of
	// every repository root.
	add(root + "google_artifact_registry_repository_iam_member.build_shared")
	add(root + "google_artifact_registry_repository_iam_member.build_shared_tag_mover")
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
	// The build account's own roles/run.invoker on the installation's
	// credentials service (B4.4, modules/repo/build.tf), on the same switch
	// the build's own drop is (D135, B4.9): an installation that has not opted
	// in plans no resource, and neither does the one repository handed the
	// address without being served — a `vertex` one, whose builds read their
	// git credential and their build secrets from the accessors they keep, so
	// the grant would be a call nothing of its makes (D3326, "any grant beyond
	// the ones a task names").
	if rs.credentialsServiceServes() {
		add(root + "google_cloud_run_v2_service_iam_member.build_credentials[0]")
	}
	for _, member := range operators {
		add(root + "google_service_account_iam_member.operator_build[" + q(member) + "]")
	}
	add(root + "google_storage_bucket_iam_member.build")
	// The build account's read of the copied catalog definitions (PIC4,
	// gcp.CatalogReadCondition): declared without a gate, so an address of
	// every repository root, like the build account's own grant above.
	add(root + "google_storage_bucket_iam_member.catalog_read")
	// The project's catalog check job's actAs on this repository's build
	// account (D1148, modules/repo/catalogcheck.tf), gated on the
	// installation having such a job at all: its account arrives in this
	// root's tfvars, and an installation that publishes no catalog sends
	// none, so the resource is not planned and its address is not one this
	// spec has.
	if rs.Installation.CatalogCheckServiceAccount != "" {
		add(root + "google_service_account_iam_member.catalog_check_build[0]")
	}
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
			if rs.credentialsServiceServes() && mountsServiceHeldSecret(ws, logical, rs.BitbucketProxied, rs.ModelSecretsServed) {
				continue
			}
			add(p + "google_secret_manager_secret_iam_member.job[" + q(logical) + "]")
		}
		// And the service's own accessor, one per container this workflow's
		// job MOUNTS (D2074) — a set of its own, and not the job's grants
		// read backwards: a mount the job keeps an accessor on is one the
		// service may still be asked for (the module's set is the mounts), and
		// a mount the job has given up is one the service is not granted.
		//
		// Plus the containers no job mounts any more and the service fetches
		// for it instead (B3.8, WorkflowSpec.VaultSecretNames): the module's
		// service_secret_names is its job_mounts together with that list, so
		// the mirror reads both. Its job_secret_names half needs nothing here
		// — the names left ws.SecretEnv when the drop took their mounts, so
		// uniqueSecrets below no longer names them.
		serviceNames := uniqueSecrets(ws)
		serviceNames = append(serviceNames, ws.VaultSecretNames...)
		slices.Sort(serviceNames)
		for _, logical := range slices.Compact(serviceNames) {
			if serviceReadsSecret(rs.Installation.CredentialsService, rs.credentialsServiceServes(), logical, rs.BitbucketProxied) {
				add(p + "google_secret_manager_secret_iam_member.service[" + q(logical) + "]")
			}
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
// (config.IsServiceHeldModelEnv — the model variable, read for the arm this
// root plans — or a name config.IsServiceHeldSecretName holds for every job),
// so the service holds it once an installation opts in (D145, D146) and the
// job's account keeps no accessor on it (C1.18, C2.3). The App key's name is
// not in that list until C2.2's release (review finding F1), so this answers
// false for it today and the job keeps the accessor it needs to read the key.
//
// served is RepoSpec.ModelSecretsServed, read for the same reason it is read
// by the drop this mirrors (dropModelMounts): the arm decides which model
// mounts the service serves this repository, and with it which accessors the
// plan keeps. In the `anthropic` arm a mount that survives the drop — the
// provider keys, and a mixed stage account's own Claude OAuth token — is a
// mount the job's account keeps a grant on, so a mirror that answered "held"
// for every model variable would report a live accessor as one the plan
// retires (R6, D1634; B0's mix, docs/mixed-credentials/spec.md §11).
//
// proxied is the repository's own fact (RepoSpec.BitbucketProxied), and it is
// the second way a mount comes to be held: a name config.ProxiedGitSecretNames
// lists is the credentials service's for a repository the published node
// admits with proxy: true (B1.8, B1.13 part two, D500), and for that
// repository alone — the same name in another repository's spec, proxied
// false, keeps its accessor (G9). It is a parameter rather than a read of the
// spec so that this function stays the one statement of the module's rule, and
// so that the rule is visible where it is called.
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
func mountsServiceHeldSecret(ws WorkflowSpec, name string, proxied bool, served string) bool {
	proxiedGit := proxied && slices.Contains(config.ProxiedGitSecretNames, name)
	held := false
	for env, logical := range ws.SecretEnv {
		if logical != name {
			continue
		}
		if !config.IsServiceHeldModelEnv(env, served, ws.StageAccount) && !config.IsServiceHeldSecretName(name) && !proxiedGit {
			return false
		}
		held = true
	}
	return held
}

// DropsBitbucketToken reports whether this root's plan takes the Bitbucket
// token away from the repository's jobs: the job's mount of it (Readiness's
// drop, and the module's job_mounts with it) and its account's secretAccessor
// on it (the mirror in Addresses, and modules/workflow/sa.tf beside it). It is
// a drop, so it reads credentialsServiceServes and not the block alone: a
// `vertex` repository handed the address keeps its token.
//
// It is Readiness's own two conditions, stated once for a caller that has to
// decide BEFORE a plan runs: `fugaro update` refuses the drop while the
// credentials service's bundle holds no token to fetch instead (B1.13 part
// two, the owner's ruling O8, internal/cli's update_proxied.go), and a second
// statement of the condition there would be a second answer about one plan —
// the state where the gate answers false and the apply drops the mount is
// exactly the one the ruling forbids. The name is read from
// config.ProxiedGitSecretNames, so a list that stopped naming the token
// answers false here as it drops nothing there.
//
// The drop is pinned where the plan makes it (spec_test.go's
// TestFilledProxiedListDropsMountAndAccessor, its empty-list counterfactual
// TestEmptyProxiedListKeepsTheMountAndAccessor and
// TestFilledProxiedListKeepsNonProxiedRepositories), and
// TestDropsBitbucketTokenIsTheDropThePlanMakes holds this answer to that drop
// on the same fixtures, so the two cannot part company without a test failing.
func (rs RepoSpec) DropsBitbucketToken() bool {
	return rs.credentialsServiceServes() && rs.BitbucketProxied &&
		slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret)
}

// credentialsServiceServes reports whether this root's plan carries the
// service's DROP for this repository (RepoInstallation.CredentialsServiceServes,
// written by credentialsServiceServesFact): the block is there AND the answer
// beside it says the service serves this repository's credentials — which is
// what modules/workflow/sa.tf's local.service_serves and
// modules/repo/build.tf's two locals read out of var.installation, both of them
// defaulting an ABSENT member to true for the same reason (a tfvars written
// before the member existed carried the block only for a repository the switch
// served). It is the mirror of a drop, so every predicate below that mirrors a
// drop reads it; the ones that mirror an ADDRESS read the block alone.
func (rs RepoSpec) credentialsServiceServes() bool {
	if rs.Installation.CredentialsService == nil {
		return false
	}
	return rs.Installation.CredentialsServiceServes == nil || *rs.Installation.CredentialsServiceServes
}

// BuildMountsServed reports whether this root's plan takes the BUILD's own
// mounts away: the build account's accessor list (RepoSpec.BuildSecrets, which
// Addresses turns into one google_secret_manager_secret_iam_member.build per
// name, and modules/repo/build.tf's for_each reads beside it) and, with it, the
// request's availableSecrets and the build step's own secrets
// (internal/backend/gcp's BuildRequest, D2077).
//
// It is buildServes(config.BuildSecretClassBuild) for a caller that has to
// decide from the finished spec rather than while building it — the guard's own
// allowance for the flip's accessor deletes (BuildAccessorAllowDelete) — and it
// is stated from the tfvars' side of the same switch: the block is there and
// the fact beside it says the service serves this repository
// (credentialsServiceServes, which is what modules/repo/build.tf's
// local.build_mounts_served reads out of var.installation), so this answer and
// the module's for_each are one answer about one plan.
//
// It is the class "build" alone and not either class: the check job's own mount
// leaves with the class "git" as well (check.tf's local.check_mount_served), and
// that mount is a container's secret_key_ref and no IAM member, so no plan
// carries a delete of it for a guard to weigh.
func (rs RepoSpec) BuildMountsServed() bool {
	return rs.credentialsServiceServes() && config.BuildServesClass(config.BuildSecretClassBuild)
}

// serviceReadsSecret reports whether the credentials service's own account is
// granted this logical secret's container in this repository's root (D2074,
// B3.3): the module's service_secret_names is the values of its job_mounts
// together with the names the job no longer mounts and the service fetches for
// it (B3.8, vault_secret_names; the caller passes both), so the test is "does
// this job still MOUNT the secret, or is it one the service fetches for this
// job" — every mounted secret except the two whose mount the module's own drop
// takes away once the service is on, by logical name (the GitHub App's private
// key, C2.3) or for this repository alone (a proxied repository's git
// credential, B1.13, G9/G15).
// config.ServiceHeldSecretNames is the same list the module's literal is held
// to (deploy/terraform's TestServiceHeldSecretsMatchTheCredentialsService).
//
// It is a different question from mountsServiceHeldSecret, and deliberately:
// that one answers "may the JOB still read it", which the served model
// credentials also say no to (their accessor goes at C1.18, their mount at
// C1.20), while their MOUNT is still on the container this release, and a
// container the job mounts is one the service may be asked for. So a model
// credential of a repository the service serves is a grant here until Go's
// own dropModelMounts takes the mount out of the spec — which is exactly the
// tfvars the module reads.
//
// svc nil, or with no account in it, is a repository handed no address and an
// installation applied before the account existed: no grant at all, which is
// what makes this release plan nothing new on such an installation. `serves`
// is credentialsServiceServes, the fact the module's own service_secret_names
// reads: a repository handed the address without being served — a `vertex` one
// — is granted nothing here, because the service is handed no container of
// that job's (D3326, F17).
func serviceReadsSecret(svc *CredentialsService, serves bool, name string, proxied bool) bool {
	if svc == nil || svc.Account == "" || !serves {
		return false
	}
	if slices.Contains(config.ServiceHeldSecretNames, name) {
		return false
	}
	return !proxied || !slices.Contains(config.ProxiedGitSecretNames, name)
}

// Schedule is the check's daily cron schedule: a minute between 05:00 and
// 06:59 UTC derived from the slug, so repositories don't all build at once.
func Schedule(slug string) string {
	sum := sha256.Sum256([]byte(slug))
	n := binary.BigEndian.Uint16(sum[:2]) % 120
	return fmt.Sprintf("%d %d * * *", n%60, 5+n/60)
}

// mixedStageAccounts are the Claude accounts an api-key repository's runs mix
// with their provider key, held to config.AllowedClaudeAccounts the way every
// other account name is, deduplicated and in the stages' own order: the
// account each of config.MixedAccountStages' stage keys names, and the
// project's default_claude_account where that key is what asks for the mix
// (mixedDefaultAccount). The stages of the mix — the senior review, the
// adversarial review and the triage that weighs their findings — may name one
// account and no other stage may name any
// (docs/mixed-credentials/spec.md §11): that is the one mix the owner's B0
// ruling admits, the coder's own stages keeping the provider key the gateway
// holds.
//
// An account is a job of its own (gcp.WorkflowVariant), so an api-key
// repository that mixes nothing keeps the single job it had: the run that
// holds a Claude account is the run whose stage keys, or whose project
// default, asked for one, and its review is the stage that goes direct to
// Anthropic. The default account's own spelling is the one name that is not a
// job of its own: its variant is the base job, which is why a stage key naming
// it adds nothing here.
//
// The allow-list is what keeps the mount to what the owner admitted: a token's
// mount is this repository's runs reading that account's credential, and only
// the account's own allow_data_to says whether this repository may put its
// code where that token is. An account the list does not carry is not an entry
// here — `fugaro validate` and the launch refuse the key — so the operator
// hears about it rather than a job quietly appearing without the token.
//
// A stage key may hold an alias (docs/account-aliases, D2091), and the name
// read here is the account it resolves to: the job set is one of the readers
// that decide which jobs exist, and both the node the runner reads it off and
// the task carry the account (the publish resolves, `configAccountValues`), so
// a key read by its own alias would leave the account with no job at all while
// the node still sends runs to it — `fugaro update` would retire the variant,
// the executions on it included. The resolution is the one step every reader
// takes and never a walk (config.ResolveAccountAlias); a name that is no alias
// is read unchanged.
func mixedStageAccounts(cfg *config.Config, lc *localcfg.Config, repo string) []string {
	if lc == nil {
		return nil
	}
	allowed := config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo)
	var out []string
	for _, stage := range config.MixedAccountStages {
		name, _ := lc.ResolveAccount(lc.ClaudeAccountStages[stage].ClaudeAccount)
		if name == "" || !slices.Contains(allowed, name) || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	if def := mixedDefaultAccount(cfg, lc, allowed); def != "" && !slices.Contains(out, def) {
		out = append(out, def)
	}
	return out
}

// MixedStageAccounts is mixedStageAccounts for the readers outside this
// package that have to ask whether a repository mixes a stage account at all:
// internal/cli's recordCredentialsServiceReady decides R12's second fact with
// it — "the record names a stage account" — and it must read the same set the
// job set is built from, or a record could say the fact true for an account no
// job of the repository exists for, and withhold it where one does (KS9, KS19).
func MixedStageAccounts(cfg *config.Config, lc *localcfg.Config, repo string) []string {
	return mixedStageAccounts(cfg, lc, repo)
}

// mixedStageAccountJob is whether token is one of the repository's mixed stage
// accounts: the job that account gets is its OWN variant (gcp.WorkflowVariant),
// the one a run of this repository is launched on when the mix's stages go out
// on it (mixedRunAccount, internal/cli), and the only job whose Claude OAuth
// mount is a credential of the stage's rather than the run's.
//
// The mix is api-key's alone (resolveMixedStages, internal/runner; B0's ruling
// of 2026-11-04, docs/mixed-credentials/spec.md §11), which is why the auth
// is read here: a name a stage key of an oauth repository names is not one of
// these jobs — under oauth the key keeps the meaning the accounts design gives
// it, and the account's token is the run's own, served by the service like
// every other (B2.7).
//
// It is one function so that the mount and the drop reading it cannot
// disagree: SecretMounts mounts the token on that job alone, and workflow
// hands the same answer to dropModelMounts and to the tfvars the module reads
// (WorkflowSpec.StageAccount), so the one mount the flip leaves and the
// account's accessor on it go and stay together — Cloud Run refuses to create
// a job whose account keeps an accessor a mount does not, or the reverse
// (C1.20).
func mixedStageAccountJob(cfg *config.Config, lc *localcfg.Config, repo, token string) bool {
	if cfg.Agent.Auth != config.AuthAPIKey {
		return false
	}
	return slices.Contains(mixedStageAccounts(cfg, lc, repo), token)
}

// mixedDefaultAccount is rung 4's account for an api-key repository: the
// project's default_claude_account where the owner's ruling of 2026-11-05
// admits it as one of this repository's mixed accounts, "" otherwise
// (docs/mixed-credentials/spec.md §11). It is the job set's half of the rung
// the launch (mixedStageAccount, internal/cli/run.go) and the run
// (r.mixedDefaultAccount, internal/runner) resolve, so a run that may hold the
// account's token is a run a job exists for — and a repository the ruling
// refuses gets neither, rather than a job whose token no run may read.
//
// The ruling's conditions, as the job set can read them:
//
//   - the key names an account of its own: "" and the unsuffixed-default
//     sentinel (config.DefaultClaudeAccount) are the same answer, because an
//     api-key job's base variant carries no OAuth token of its own;
//   - the name is one this repository may use (config.AllowedClaudeAccounts):
//     the mount is this repository's runs reading that account's credential,
//     and the published policy carries no allow-list for a run to check, so
//     the filter here is the only place it is applied;
//   - no stage key of the mix names a *different* account: the run holds one
//     Claude account and no second (config.CheckStageAccountMix, which the
//     launch and `fugaro validate` apply), so where a stage key names another
//     account the default is not an account of this repository's mix and no
//     job is built for it;
//   - one of the mix's stages runs an Anthropic model (config.MixedStageModel,
//     config.IsClaudeModel): a project that sets the key and reviews on
//     another vendor has asked for no mix, and its key keeps the other meaning
//     it has — the account an oauth run gives to every stage.
//
// The coder's provider route and the budget mode are deliberately not read
// here. They are conditions of the run, not of the job set: they decide
// whether a run of this repository may hold the token at all, and they change
// with a branch's fugaro.yaml or a task's override that no deploy can see.
// Reading them here would refuse a job for a configuration the next commit
// makes admissible, and build one the current branch cannot use; the run
// refuses it where it resolves (r.mixedDefaultAccount) with the reason.
//
// The key is resolved through the file's aliases first, for mixedStageAccounts'
// reason: the default may be written as an alias (D2091), and the account the
// node and the task carry is the one it stands for.
func mixedDefaultAccount(cfg *config.Config, lc *localcfg.Config, allowed []string) string {
	if cfg == nil {
		return ""
	}
	def, _ := lc.ResolveAccount(lc.DefaultClaudeAccount)
	if def == "" || def == config.DefaultClaudeAccount || !slices.Contains(allowed, def) {
		return ""
	}
	for _, stage := range config.MixedAccountStages {
		if name, _ := lc.ResolveAccount(lc.ClaudeAccountStages[stage].ClaudeAccount); name != "" && name != def {
			return ""
		}
	}
	for _, stage := range config.MixedAccountStages {
		if lc.ClaudeAccountStages[stage].ClaudeAccount != "" {
			continue
		}
		if config.IsClaudeModel(config.MixedStageModel(cfg.Agent, stage)) {
			return def
		}
	}
	return ""
}
