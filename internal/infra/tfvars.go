package infra

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

// The installation's singletons. The installation module checks their
// shapes; only these constants name them.
const (
	SchedulerServiceAccountID = "fugaro-scheduler"
	RoleLauncher              = "fugaroLauncher"
	RoleJobRunner             = "fugaroJobRunner"
	RoleBuildSubmitter        = "fugaroBuildSubmitter"
	RoleTagMover              = "fugaroTagMover"
	BaseRegistry              = "fugaro-base"
	// GhcrRegistry is the remote (pull-through) repository over ghcr.io:
	// the base image's pin goes through it (design §3.1).
	GhcrRegistry   = "fugaro-ghcr"
	LegacyRegistry = "fugaro"
	LogBucket      = "fugaro"
	LogView        = "fugaro-runs"
	LogSink        = "fugaro-jobs"
	LogExclusion   = "fugaro-jobs-from-default"
	// LogBucketDescription is the log bucket's ownership mark: a log
	// bucket carries no labels, so discovery adopts a bucket of that name
	// only when it carries this description.
	LogBucketDescription = "Fugaro job logs (managed by fugaro)"
)

// The runs bucket's lifecycle, in days: the rules the bootstrap set, so an
// adopted bucket's lifecycle doesn't change, and the one the stored reports
// get (R12's fifth rule, E3).
const (
	runsDays            = 90
	cacheCustomTimeDays = 30
	cacheAgeDays        = 180
	// reportsDays is 18 months in whole days, as the other members are: it is
	// the retention of reports/ and it is the Go half of the module's own rule
	// (bucket.tf), which is why an installation's plan holds both. runs/ keeps
	// its 90 (F24, untouched): a yearly report rolls the twelve stored monthly
	// ones up, so the reports have to outlive the records by more than a year.
	reportsDays = 548
)

// InstallOptions are the installation's inputs that come from flags.
type InstallOptions struct {
	// StateBucket defaults to the local config's, else fugaro-tfstate-<project>.
	StateBucket string
	// EnableVertex enables the Vertex AI API; so does any repository the
	// local config records as using it, and any chain of the project's own
	// routing table that names Vertex (ChainsNameVertex) — the service calls
	// Vertex on the installation's own identity, so a table that names Vertex
	// needs the API whether or not a repository of it records one.
	EnableVertex bool
	// SkipAPIs leaves the project's APIs to be managed elsewhere.
	SkipAPIs bool
	// Launchers and Operators are IAM members; nil means the local config's.
	Launchers, Operators []string
	// Viewers are IAM members too, and nil is empty: no local config key
	// carries a viewer list, so an installation names one only when a run
	// derives it from the grants node (roles.md §4, RS4). An installation
	// that names none plans exactly what it planned before the viewer's
	// role existed — the module's role and grants are gated on the list.
	Viewers []string
	Budget  *Budget
	// AlertEmail defaults to the local config's; empty means no alert.
	AlertEmail string
	// RegistryCleanup is dry-run (the default), on or off.
	RegistryCleanup     string
	AdoptLegacyRegistry bool
	// NoLogIsolation turns log isolation off; it is on (the module's
	// default) otherwise.
	NoLogIsolation bool
	// BudgetBackend gives the installation the history account and, once
	// the Firebase root's outputs are known and its image exists, the
	// history job and its sweep Scheduler job. It is set by --firebase-project and
	// by a project config that records a Firebase project.
	BudgetBackend bool
	// ManagedDeployerScoped opts the installation in to the managed
	// deployer's seven per-service roles and their seven conditioned
	// bindings (the module's managed_deployer_scoped). init sets it for an
	// installation that uses managed projects — the run that creates one
	// with --create-project, and every later run against one,
	// read from the project's creation fact (init's installRoot) — so the
	// key follows the installation and not the run. What it does not settle
	// is whether the fence works: Check 68 has not been run
	// (deployer.tf's header, docs/managed-deployer-fence/spec.md).
	ManagedDeployerScoped bool
	// Coordinator is the owner's opt-in to the coordinator's aggregation job
	// (P5, D194): with BudgetBackend, the installation declares the
	// coordinator's account, its read of the runs bucket and, once it can,
	// the job and its schedule. It is off unless fugaro update --coordinator
	// or a local config that records the opt-in sets it, so an installation
	// that never asked plans exactly what it planned before the job existed.
	Coordinator bool
	// CarriedJobProfiles are the profiles whose shared job this run does not
	// compose (projectJobs' header): the installation's own document carries
	// the job it deployed for each, whole, so the plan proposes no change to
	// it. It is internal/cli's installSpec that sets it, and only for a
	// default profile the config can no longer compose a job for.
	CarriedJobProfiles []string
	// MCPServer is the owner's SECOND opt-in, and the one that makes the
	// installation reachable from the network (D920, D934): the coordinator's
	// front door, its own service account, its uploaded image and D920's
	// public invoker binding. It needs Coordinator beside it — the front door
	// is the coordinator's own service and serves the state the coordinator
	// writes, so an installation that has not declared the coordinator has no
	// state for one to answer about. Nothing in the CLI sets this yet: fugaro
	// update carries no --mcp-server flag, so the field is reachable only from
	// a caller that builds the options itself, and the release that turns the
	// front door on adds the flag. An installation that never asked writes no
	// mcp_server key at all, so its plan is exactly what it planned before the
	// front door existed — including the installation that opted into the
	// coordinator for its read-only view alone.
	MCPServer bool
	// ReportsPaused and CatalogCheckPaused are the project layer's jobs:
	// block (R13): whether each of the installation's two scheduled jobs is
	// stopped. nil means the layer wrote nothing — no jobs: block, no block
	// for that job, no paused: key, or no layer to read at all — and the
	// default is then the job's own: paused for the reports job, running for
	// the catalog check, whose nightly rebuild needs no operator edit (D3444).
	// They are pointers for that reason and no other: the zero value of a bool
	// says the opposite of one of the two, so a caller that built the options
	// itself would state the wrong answer for that job by saying nothing.
	// ReportsPaused is rendered by R7, the reports job's own plan;
	// CatalogCheckPaused is rendered by CatalogCheck.
	ReportsPaused, CatalogCheckPaused *bool
}

// jobPaused is one paused option's value, with the job's own default where the
// option names none. The default lives here, once, because "the layer said
// nothing" and "the layer said false" are one bool apart and which of them a
// job takes is a decision about that job: the reports job deploys stopped, the
// catalog check runs (D3444).
func jobPaused(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Budget is an optional budget on the project, in whole units of currency.
type Budget struct {
	BillingAccount string `json:"billing_account"`
	Amount         int64  `json:"amount"`
	CurrencyCode   string `json:"currency_code"`
}

// InstallationSpec is the installation root's tfvars: exactly its
// variables.
type InstallationSpec struct {
	Project string `json:"project"`
	// FugaroProject is the Fugaro project's name (not the GCP ID, which is
	// Project: Terraform's own variable keeps its name).
	FugaroProject   string            `json:"fugaro_project"`
	Region          string            `json:"region"`
	RunsBucket      string            `json:"runs_bucket"`
	StateBucket     string            `json:"state_bucket"`
	Names           InstallationNames `json:"names"`
	BucketLifecycle BucketLifecycle   `json:"bucket_lifecycle"`
	EnableVertex    bool              `json:"enable_vertex"`
	ManageAPIs      bool              `json:"manage_apis"`
	Launchers       []string          `json:"launchers"`
	Operators       []string          `json:"operators"`
	// LauncherAccounts is the launcher class's list (D1938, F25) as the two
	// services that admit a launcher read it (FUGARO_LAUNCHER_ACCOUNTS): the
	// bare addresses a Google ID token's email claim holds. It is derived by
	// launchers.go from the merged launchers list above and never written by
	// hand, so the class and the invoker grants name the same people (F31).
	// An installation whose roster names no launcher writes no key at all: the
	// module's own default is then the empty class, which is what an
	// installation that never had one already planned.
	LauncherAccounts []string `json:"launcher_accounts,omitempty"`
	// Viewers are the installation's read-only people (roles.md §4, RS4):
	// they read every run, its logs and the project's inventory, and hold no
	// write and no launch. It is left out of the tfvars when empty, so an
	// installation that names no viewer hands Terraform the document it
	// handed it before the list existed.
	Viewers []string `json:"viewers,omitempty"`
	// LauncherBucketCondition limits the launchers' write grant on the runs
	// bucket to runs/, tokens/ and issues/ (gcp.LauncherBucketCondition).
	LauncherBucketCondition Condition       `json:"launcher_bucket_condition"`
	Budget                  *Budget         `json:"budget"`
	AlertEmail              *string         `json:"alert_email"`
	RegistryCleanup         RegistryCleanup `json:"registry_cleanup"`
	AdoptLegacyRegistry     bool            `json:"adopt_legacy_registry"`
	// LogBucketDescription is the log bucket's ownership mark.
	LogBucketDescription string `json:"log_bucket_description"`
	// LogBucketLocation is where the run log bucket is. Installation() writes
	// the mapping's answer (place.For), which is what a NEW installation's
	// bucket is created in; DiscoverInstallation gives it the location it read
	// for a bucket that exists, because a bucket's location is permanent and
	// nothing that exists is ever given the mapping's answer (R5, F10). The
	// module ignores the field on a bucket it manages, so the value a plan
	// carries can never move one.
	LogBucketLocation string `json:"log_bucket_location"`
	// LogIsolation is nil for the module's default, which is on; only
	// --no-log-isolation and the rollback turn it off.
	LogIsolation *bool `json:"log_isolation,omitempty"`
	// EnableBudget and History give the installation the history account
	// and, with History.DeployJob, the sweeper's job (M9b); both are left
	// out of an installation without a budget backend. Coordinator is the
	// aggregation job and the writer account beside them (P5, D194), left
	// out the same way and also whenever the owner has not opted in
	// (InstallOptions.Coordinator).
	EnableBudget bool             `json:"enable_budget,omitempty"`
	History      *HistorySpec     `json:"history,omitempty"`
	Coordinator  *CoordinatorSpec `json:"coordinator,omitempty"`
	// Licence is the licence renewal job (K4.2): the scheduled
	// `fugaro licence renew --job`, its own account and the empty container
	// the renewal secret is written into. It hangs off the coordinator's
	// opt-in rather than one of its own — its image IS the coordinator's, so
	// an installation that has not opted in has no tagged image to run — and
	// internal/cli's licenceJob drops the block again where this plan does not
	// deploy the coordinator's job, which is the same fact
	// modules/installation/licence.tf reads from local.deploy_coordinator.
	Licence *LicenceSpec `json:"licence,omitempty"`
	// MCPServer is the coordinator's front door (D920, D934), left out of an
	// installation the owner has not turned it on for: the module creates the
	// account, the service and the public invoker binding only for a variable
	// that is there, so leaving it out plans exactly the installation that
	// existed before the front door did (InstallOptions.MCPServer).
	MCPServer *MCPServerSpec `json:"mcp_server,omitempty"`
	// ProjectSecrets are the installation module's project-wide secret
	// containers (logical name -> gcp.ProjectSecretID), which fugaro update
	// creates or adopts; their values are never set by Terraform (design
	// docs/project-secrets/spec.md §4). Installation() fills it from the
	// local config's eligibility; DiscoverInstallation then unions in every
	// correctly-labelled container that already exists, so a config edit
	// can never shrink the module's for_each (N3). A workflow-secret name is
	// no longer eligible (IS6/D3324, projectSecretContainers); a live container
	// for one the record does not mark copied stays in the union.
	ProjectSecrets map[string]string `json:"project_secrets,omitempty"`
	// OwnGitSecrets is set by LiveGitSecrets and read only by
	// WithPlannedProjectSecrets; it never reaches tfvars.
	OwnGitSecrets map[string]bool `json:"-"`
	// ProjectJobs are the installation's shared project jobs (design
	// project-centric §4.1): one Cloud Run job and one job service account
	// per (profile, provider, account), by job name. The key is left out of
	// an installation whose local config names no profile, so the plan of
	// one that had none is unchanged (infra.ProjectJobs).
	ProjectJobs map[string]ProjectJob `json:"project_jobs,omitempty"`
	// CarriedProjectJobs names the profiles whose shared job this spec does not
	// compose, because the local config cannot compose one for them right now,
	// while the installation already holds the job an earlier run deployed:
	// prepare keeps each of them, whole, from the installation's own tfvars
	// (infra.CarriedProjectJobs). It is a working note of the run and not a
	// variable — the root has no such input — so it never reaches the document.
	CarriedProjectJobs []string `json:"-"`
	// CarriedBudget is the alert the installation already holds, as the run
	// read it out of the cloud: the amount and the currency from the project's
	// creation fact, the billing account from the alert's own resource in the
	// installation root's state (R5 and R7 of docs/managed-flag-ergonomics).
	// prepare writes it in where this spec carries no budget of its own, so a
	// run given no budget flags plans no change to the alert; Budget above is
	// the variable the module takes, and this is a working note of the run, so
	// it never reaches the document as itself.
	CarriedBudget *Budget `json:"-"`
	// CarriedProjectJobAccounts are the points of the account axis this spec
	// composes no job for, because the local config declares a KEY credential
	// under the name (ProjectKeyAccountJobs, the filter's own answer), while
	// the installation may already hold the job an earlier release deployed
	// for one: prepare keeps each of them, whole, from the installation's own
	// tfvars (infra.CarriedProjectJobAccounts). A working note like the field
	// above, and never a variable of the root's.
	CarriedProjectJobAccounts []ProjectJobAccount `json:"-"`
	// ProjectReviewJobs are the review-only variants of those jobs, by job
	// name: one per (profile, provider, account) the coding set deploys, with
	// an empty secret_env, deployed by the root as a module instance of its own
	// with no secrets handed in (design docs/cloud-senior-review, CSR7). Left
	// out — like ProjectJobs, and for the same reason — of an installation
	// whose local config names no profile.
	ProjectReviewJobs map[string]ProjectJob `json:"project_review_jobs,omitempty"`
	// ManagedDeployerScoped is the module's managed_deployer_scoped: left out
	// unless set, so an installation that does not opt in writes the same
	// tfvars as before the variable existed and plans without the seven
	// per-service deployer roles and their seven conditioned bindings
	// (InstallOptions.ManagedDeployerScoped).
	ManagedDeployerScoped bool `json:"managed_deployer_scoped,omitempty"`
	// CredentialsService is the installation's credentials service (D146,
	// D147), left out until `fugaro update` deploys it (DON1, the owner's
	// ruling of 2026-11-05) — which every run does for every installation that
	// can carry the service at all. Left out rather than written as null, so an
	// installation that cannot carry one (no Firebase backend to read, no
	// repository job to admit) writes exactly the tfvars it wrote before the
	// variable existed, and the module creates nothing for an absent one.
	CredentialsService *CredentialsServiceSpec `json:"credentials_service,omitempty"`
	// CatalogCheck is the project's catalog check job (D1148), left out on an
	// installation whose project publishes no catalog and on one whose catalog
	// names no entry a repository of it can build. The module creates the job,
	// its account, its six grants and its schedule only for an installation
	// that writes this, so leaving it out plans exactly what the installation
	// planned before the job existed.
	CatalogCheck *CatalogCheckSpec `json:"catalog_check,omitempty"`
	// Reports is the project's reports job (E4), left out on an installation
	// this run cannot deploy it for: one that records no base image for the
	// job to run FROM. The module creates the job, its account, its two
	// preconditioned grants and its schedule for an installation that writes
	// this — paused, unless the layer's jobs.reports.paused says otherwise —
	// so leaving it out plans exactly what the installation planned before
	// the job existed. It is deliberately NOT gated on the project's catalog
	// or on any repository: the job reports every repository of the project
	// into the project's own bucket, and its assignment travels in
	// ReportsJobDocEnv.
	Reports *ReportsJobSpec `json:"reports,omitempty"`
	// PeopleMinter is the launcher-does-not-mint switch (LM11, LM12, LM14):
	// the root's var.people_minter, which keeps the installation's
	// orchestrator_token_minter row while it is on. It is a POINTER because
	// the key's absence is the answer this run has before the grant moves
	// (the root's default, on) and only `false` is ever written: a plain bool
	// with omitempty could not write it at all, and one without would put
	// `people_minter = true` into every document this build writes, moving
	// the tfvars of every installation that never turned it off. nil on every
	// run that is not the one moving the grant (LM14's stage); every later run
	// carries the key forward into the document (prepare's carry,
	// CarriedPeopleMinter).
	PeopleMinter *bool `json:"people_minter,omitempty"`
}

// CredentialsServiceSpec is the installation root's var.credentials_service
// (D146): the service's own account and Cloud Run service, the image it runs,
// and the ONE bundle container its account reads. It is not the outputs'
// CredentialsService (spec.go), which is what a repository root reads back:
// that object carries the name and the region, and this one carries what the
// module needs to create them.
//
// The whole opt-in is this one object: the module's five resources are gated
// on it, and one accessor grant on the one bundle container is its whole reach
// in Secret Manager (Q6, Global Constraint 12). Nothing else about the
// installation changes; what changes in the repository roots is that their
// jobs stop mounting a model credential (C1.20, internal/infra/spec.go's
// dropModelMounts).
type CredentialsServiceSpec struct {
	AccountID string `json:"account_id"`
	Service   string `json:"service"`
	Image     string `json:"image"`
	// BundleSecret is the bundle container's Secret Manager ID, which the
	// loader derives from the project the same way (gcp.ProjectSecretID of
	// the logical name CredentialsBundleName): the module creates the
	// container under this ID and grants the account on it, and the service
	// reads it back by its own derivation, so the two never drift.
	BundleSecret string `json:"bundle_secret"`
	// FirebaseProject and RTDBURL are the installation's Firebase project —
	// the one a run's identity is verified against — and the Realtime
	// Database it reads (C1.14's FUGARO_FIREBASE_PROJECT and FUGARO_RTDB_URL).
	// Both are the local config's own budget block, written by `fugaro update
	// --firebase` from the Firebase root's outputs, so the service and every
	// run's launcher name the same database.
	FirebaseProject string `json:"firebase_project"`
	RTDBURL         string `json:"rtdb_url"`
	// JobAccounts are the installation's per-repository jobs (CredentialsJob),
	// the triples the service derives the job service accounts it binds a run
	// to from (credsvc.JobAccountsFor over task.Slug and gcp.ServiceAccountID).
	// Emails are deliberately not carried: an account's name and a
	// repository's slug are both hashes of these three fields, so a list of
	// emails here would be a second derivation that can drift from the
	// accounts the deployer creates.
	JobAccounts []CredentialsJob `json:"job_accounts"`
	// ReviewJobAccounts are the installation's REVIEW-ONLY job variants
	// (CredentialsReviewJob): the review class's admitted set, which is a set
	// of its own and not a row of JobAccounts above — a review caller is
	// admitted to one account's model credential and to nothing else
	// (credsvc's ClassReview), so a deployment that named only the coding
	// table would refuse every review launch with the fail-closed sentence
	// internal/cli's credentialsServiceConfig writes for an empty set.
	//
	// The rows carry the triple and never the account's email, for the reason
	// JobAccounts carries its own three fields: a review job's service account
	// is a hash of (profile, provider, account)
	// (credsvc.ReviewServiceAccountID), so a list of addresses here would be a
	// second derivation that can drift from the accounts the deployer creates.
	// Account is the value of the account axis the variant's calls are served
	// by, which the service cannot derive from the triple alone: it is the published
	// policy's own spelling (config.ProjectJobAccounts' mapping, never empty),
	// and a revision reads no policy at start-up (D143).
	//
	// It is left out of an installation that deploys no review job at all — no
	// profile, and no credentials service (CredentialsReviewJobAccounts) — which
	// is an installation whose service admits no review caller: the fail-closed
	// state, and not an error, because the class is inert until a job of it
	// exists (unlike JobAccounts, which the service refuses an empty version
	// of). An installation with no profile and the service on is NOT that
	// state: it deploys the ONE implicit default review job and carries that
	// job's row, which is what the owner's requirement of 2026-11-13 asks for
	// (ProjectReviewJobs' implicitReviewVariant).
	ReviewJobAccounts []CredentialsReviewJob `json:"review_job_accounts,omitempty"`
	// VerifierAccounts are the identities that may ask the service whether a
	// verdict's signature is its own (C3.9g1, D1068): the installation's own
	// verifier account when nobody names one, and the owner's list when they
	// do. The value is the published policy node's list, not this machine's
	// opinion of it: the owner writes it in the local config, `fugaro update`'s
	// policy stage publishes it, and the same run's prepare reads it back and
	// hands it here (localcfg's keep rule is what makes a config that states
	// none keep the node's).
	//
	// It is THREE-STATE, and a pointer is the only spelling of it, because
	// `omitempty` on a slice cannot tell two of the three apart:
	//
	//   - nil: the key is omitted, which is the state the module's default is
	//     for — it renders the address of the verifier account the same apply
	//     creates (CSR14, verifier.tf) — and an installation that names no
	//     verifier anywhere gets that account without typing anything.
	//   - a pointer to an empty list: `[]` is written, the owner switching the
	//     route off. Omitted instead, the module's default would be admitted
	//     on a route the owner cleared, which is the one thing `[]` is for.
	//   - a pointer to the owner's list: written as it is. The account is never
	//     ADDED to it: a union would admit an identity `[]` cannot remove.
	VerifierAccounts *[]string `json:"verifier_accounts,omitempty"`
	// SharedJobAccounts are the installation's SHARED project jobs
	// SharedJobAccounts are the installation's SHARED project jobs
	// (CredentialsSharedJob): the shared coding class's admitted set (B5.1),
	// one row per (profile, provider, account) the deployer creates a shared
	// job for, plus the repositories that profile serves. It is neither of the
	// two tables above and it is not a row of either: a shared job is ONE job
	// for every repository a profile serves, so its account is admitted to the
	// repositories of its profile and to nothing else, and a repository that
	// runs on a per-repository job (a JobAccounts row) is admitted to its own
	// repository alone.
	//
	// The rows carry the triple and never the account's email, for the reason
	// the two tables above do (credsvc.SharedJobAccountFor over
	// gcp.ProjectServiceAccountID): a shared job's service account is a hash of
	// (profile, provider, account), and a list of addresses here would be a
	// second derivation that can drift from the accounts the deployer creates.
	// Account is the axis's OWN spelling, in which the empty string is the
	// profile's no-account job (config.ProjectJobAccounts), and it is NOT
	// ReviewVariantAccount's mapping: this class's caller is the coding job,
	// which the axis spells as it is.
	//
	// Repos is the profile's own allow_repos, or — a profile that names none —
	// the repositories this installation's own config records for the profile's
	// provider (sharedJobRepos), and it is REQUIRED and may be empty: an empty
	// Repos is the deny-all profile and not a refusal, while a row the service
	// read as deny-all because the member was missing would be a job it refuses
	// every call of (internal/cli's credentialsServiceSharedJobAccounts refuses
	// the document rather than guess).
	//
	// It is left out of an installation that deploys no shared job at all — no
	// profile on a provider a shared job serves — which is an installation
	// whose service admits no shared caller: the fail-closed state, and not an
	// error, since the class is inert until a job of it exists. It is the
	// review table's rule and not JobAccounts' one, for the same reason.
	SharedJobAccounts []CredentialsSharedJob `json:"shared_job_accounts,omitempty"`
	// SignerAccount is the runs' signer account the service signs a run's
	// identity as (launcher-does-not-mint plan tasks LM12 and LM13; D3121):
	// the account the installation's Firebase root creates, as the bare
	// address this process reads (FUGARO_SIGNER_ACCOUNT). It is derived from
	// the local config's own budget block — the Firebase project the same
	// root published — and it is written whenever that block is, because the
	// service is deployed only into an installation that has one
	// (firebaseBackend), and an installation whose budget backend exists has
	// had that root applied: the signer is created there and not by the
	// credentials service. Leaving it out is the state of an installation
	// this release has not converged: the service starts with the entry
	// absent and serves no identity route at all (I1's contract), rather
	// than naming an account nothing created.
	SignerAccount string `json:"signer_account,omitempty"`
	// RosterServiceAccounts are the roster's `serviceAccount:` entries as
	// bare addresses (LM12, LM13; D3123): the class a person's list cannot
	// hold, because such a caller presents the account's own Google ID token
	// rather than a person's, and — in the module — the list the service's
	// invoker grant is made for. launchers.go derives it from the same
	// merged list the launcher class comes from, so the accounts IAM admits
	// to the service and the ones the process admits are one derivation, and
	// a `group:` entry is on neither (launcherServiceAccounts says why).
	RosterServiceAccounts []string `json:"roster_service_accounts,omitempty"`
	// ImplicitAdmins are the installing machine's implicit admins as bare
	// addresses (LM12, LM13; F29, R16 arm 1): the identity the launch gate
	// admits whatever the roles node says, handed to the service so that the
	// issuer's scope check gives the gate's answer and an owner is not
	// refused a launch that works today. It is read from the machine's own
	// terraform.budget_admins, through the same normalisation the gate reads
	// it with (implicitAdmins).
	ImplicitAdmins []string `json:"implicit_admins,omitempty"`
	// URL is the service's OWN URL, the audience its callers' Google ID
	// tokens are minted for. It is empty on the apply that CREATES the
	// service: Cloud Run assigns the URL, nothing in the module may read the
	// service's own uri from inside its resource (Terraform refuses a
	// self-referential block), and the revision then carries the module's
	// unassigned placeholder instead. The init stage reads the real URL back
	// from the module's own output (infra.CredentialsService.URL) and writes
	// it here in a second apply, which is why it is optional and why an
	// ordinary run of init carries it forward rather than dropping it
	// (CredentialsServiceURLOf).
	URL string `json:"url,omitempty"`
}

// IntakeSpec is the installation root's var.intake (D1931, DS2.2, DS4.1): the
// secrets intake's own account and Cloud Run service, and the image it runs.
// It is not the outputs' IntakeService (spec.go), which is what a reader gets
// back: that object carries the name and the region, and this one carries what
// the module needs to create them.
//
// One release, one pin, two services: the intake is a subcommand of the
// released binary (`fugaro intake serve`, internal/cli), so its image is the
// same digest-pinned reference the credentials service's block carries and no
// resolve of its own — the module's own container sets the args that pick the
// subcommand and no command of its own, which is how the one image serves both.
//
// The block is written by `fugaro update`'s own installation stage
// (internal/cli's ensureIntakeBlock), never composed from the local config the
// way CredentialsServiceSpec is: the intake reads no bundle and no database —
// its account holds two conditioned roles and one object read — so the
// installation facts the credentials service refuses over are not its, and
// every run of init rewrites the root's tfvars from a spec that carries this
// key none, which is why prepare carries it forward (CarriedIntake).
type IntakeSpec struct {
	AccountID string `json:"account_id"`
	Service   string `json:"service"`
	Image     string `json:"image"`
	// URL is the service's OWN URL, the audience every caller's Google ID token
	// is minted for — the one thing that binds such a token to THIS service. It
	// is empty on the apply that CREATES the service, for the credentials
	// service's reason: Cloud Run assigns the URL, Terraform refuses an
	// expression that reads the service's own uri from inside its resource, and
	// the revision then carries the module's unassigned placeholder (an
	// .invalid host no ID token is ever minted for) until the same stage reads
	// the URL back from the module's own output and applies it.
	URL string `json:"url,omitempty"`
	// People is the intake's SECOND class list (WC3, R5, D3143): the people of
	// a workspace's roster, as the bare addresses a Google ID token's email
	// claim holds (infra.IntakePeople). It is a member of the INTAKE's own
	// block and never of `launchers`, which is the workspace project's
	// credentials service invoker set as well (F10), and it is empty — no key
	// at all — for every installation that is not a workspace's project.
	//
	// `fugaro update` renders it from the workspace's roster on the owner's own
	// run (init_stages.go's renderIntakePeople), so a person the roster gains
	// waits for that one command, which binds the invoke grant beside it
	// (D2971).
	People []string `json:"people,omitempty"`
}

// VoiceServiceSpec is the installation root's var.voice_service (P5.3.3): the
// voice hop's own account and Cloud Run service, and the image it runs.
//
// One release, one pin, four services: the hop is a subcommand of the
// released binary (`voice serve`, internal/cli), so its image is the same
// digest-pinned reference the credentials service's block and the intake's
// carry and no resolve of its own — the module's own container sets the args
// that pick the subcommand and no command of its own, which is how the one
// image serves all four.
//
// There is deliberately no URL member, and that is the one real difference
// from both of them: the hop's callers are phones, whose ID tokens are minted
// for the installation's Firebase project and not for the service's own
// address, so nothing reads a URL back from the module and no second apply
// exists for it. What the process is CONFIGURED with — the Firebase project it
// verifies against and the database whose device nodes and policy it reads —
// is not here either: the module reads credentials_service's own two members
// (voice.tf), because the same run writes both blocks from one config and a
// second copy of one fact could be pulled apart by a hand edit.
//
// The block is the hop's OWN opt-in, and it is the only one there is: nothing
// composes it from the local config the way CredentialsServiceSpec is, no
// command writes it and no flag asks for it — an owner who wants the hop writes
// the three members above into the installation root's tfvars, and the module's
// variable is null until they do, which is no service, no account and no
// invoker binding at all. Every run of init rewrites the root's tfvars from a
// spec that carries this key none, so what keeps an opted-in installation's
// block is prepare's carry (CarriedVoiceService); what moves the image inside
// it is the credentials-service stage's own move (internal/cli's
// moveCredentialsImage), which re-pins a block that is already there and never
// writes one.
type VoiceServiceSpec struct {
	AccountID string `json:"account_id"`
	Service   string `json:"service"`
	Image     string `json:"image"`
}

// CredentialsJob is one job of the installation as the credentials service
// names it: the git provider, the repository and the workflow, where
// workflow is the JOB's own name — a repository that runs one workflow under
// a named Claude account has two jobs of it, the plain workflow and the
// account's variant (gcp.WorkflowVariant), and each has its own service
// account and so its own row here.
type CredentialsJob struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	Workflow string `json:"workflow"`
}

// CredentialsReviewJob is one review-only variant of the installation's
// project jobs as the credentials service's environment carries it: the
// (profile, provider, account) triple the variant's job and service account
// are derived from (credsvc.ReviewJobName, credsvc.ReviewServiceAccountID),
// where account is the value of the account axis the policy's admissions
// spell — never empty, because the axis spells the profile's no-account job as
// the empty string and the admissions spell it config.DefaultClaudeAccount
// (config.ReviewVariantAccount), which is the value the service's own
// ReviewJobAccount.Account holds and the pairing resolves a call by. It is an
// account's name or, since AK4, config.AnthropicKeyAccount, the sentinel that
// names the project's own Anthropic API key and is not an account.
//
// It is the review class's own row rather than a ProjectJob: what the service
// admits a review caller AS is the triple and nothing else, and the variant's
// image, resources, mounts and grants are the deployment's business (they are
// the coding job's own twin, internal/infra/projectjobs.go's reviewVariant).
type CredentialsReviewJob struct {
	Profile  string `json:"profile"`
	Provider string `json:"provider"`
	Account  string `json:"account"`
}

// CredentialsSharedJob is one SHARED project job of the installation as the
// credentials service's environment carries it (B5.1): the
// (profile, provider, account) triple the job and its service account are
// derived from (gcp.ProjectServiceAccountID), plus the repositories that
// profile serves.
//
// It is the review row's shape one axis over and it is not that row: Account
// is the axis' own spelling (the empty string is the profile's no-account job,
// config.ProjectJobAccounts) and never ReviewVariantAccount's mapping, because
// the caller this row admits holds the CODING job — the same job the launcher
// launches a repository of an admitted profile onto (internal/cli's
// launchProjectJobFor). Repos is what the call's admission is read through
// (credsvc's ClassShared over projectpolicy.AdmitRepo): the repositories the
// profile serves, as patterns, and an empty list is the profile that serves
// none — a row whose callers are all refused.
type CredentialsSharedJob struct {
	Profile  string   `json:"profile"`
	Provider string   `json:"provider"`
	Account  string   `json:"account"`
	Repos    []string `json:"repos"`
}

// credentialsSharedJobKey is the shared table's row identity, the same rule
// credentialsReviewJobKey states for the review table: two rows are one row
// exactly when they name one job. Repos is deliberately NOT part of it — the
// repositories a profile serves are a fact ABOUT the job and not part of
// naming it, so a profile whose list moved is the same row with another
// value, which is what lets the union below update it in place.
func credentialsSharedJobKey(j CredentialsSharedJob) string {
	return j.Profile + "\x00" + j.Provider + "\x00" + j.Account
}

// The credentials service's singletons. The account is what reads the bundle,
// and nothing else is granted actAs on it (modules/installation/credentials.tf);
// the Cloud Run service's name is the name every job's invoker grant and the
// repository roots' CredentialsService output carry.
const (
	CredentialsAccountID = "fugaro-credentials"
	// VerifierAccountID is the verifier service account (CSR14, D1068): the
	// installation's own caller of the credentials service's verify route,
	// created by the same apply that turns the service on
	// (modules/installation/verifier.tf, whose account_id is this literal) and
	// granted nothing of a run's — no bucket, no secret, no project role. Its
	// whole reach is the invoker bit on that one service, and the operators'
	// impersonation of it.
	//
	// It is a constant here because the module's account ID is a LITERAL, which
	// is what makes the address a fact of the configuration rather than of the
	// apply: `<id>@<project>.iam.gserviceaccount.com`, computable before
	// anything is planned. That is what lets ONE `fugaro update` write the
	// address into the owner's local config — and so into the published policy
	// node — and have the same apply's service admit it, with no output to read
	// back and no second run (CLAUDE.md's HARD RULE). internal/infra/tf's
	// VerifierAccountID is the same literal on the cover's side.
	VerifierAccountID = "fugaro-verifier"
	// CredentialsServiceName is the Cloud Run service's name. It may start
	// with fugaro-: that prefix is reserved by the *job* listings (HistoryJob),
	// and a Cloud Run service is not one of them.
	CredentialsServiceName = "fugaro-credentials"
	// CredentialsBundleName is the logical name of the one bundle secret —
	// the document every credential the service holds lives in (Q6). It is a
	// name of the platform's: no workflow may declare it and no `fugaro
	// secrets set` stores a value under it, because the service's document is
	// written by `fugaro creds set NAME` alone (C1.11, C1.25).
	CredentialsBundleName = "creds-bundle"
	// IntakeAccountID and IntakeServiceName are the secrets intake's own two
	// names (D1931, DS2.2): the account that alone creates, versions and
	// deletes the dynamic containers, and the Cloud Run service that runs it.
	// The service's name is what a caller derives the intake's URL from
	// (internal/cli's secretsIntakeServiceURL) and what the launcher invoker
	// grants are made on, so it is one literal and not a per-installation
	// choice. internal/infra/tf's IntakeAccountID is the same literal on the
	// cover's side, which is what lets the cover hold the account the plan
	// creates for it.
	IntakeAccountID   = "fugaro-intake"
	IntakeServiceName = "fugaro-intake"
	// VoiceAccountID and VoiceServiceName are the voice hop's own two names
	// (P5.3.3): the account that verifies a phone's ID token and reads the one
	// database, and the Cloud Run service that runs it. internal/infra/tf's
	// VoiceAccountID is the same literal on the cover's side, which is what lets
	// the cover hold the account the plan creates for it, and the module's
	// variable accepts no other (variables.tf): an account ID is a fact of the
	// configuration rather than of the apply, the way VerifierAccountID is.
	//
	// The SERVICE's name is the installation's own and only the default here:
	// the module's variable takes any Cloud Run name, so an installation that
	// wrote another one keeps it — the name is a force-new attribute, and a run
	// that composed this constant over it would plan a replace the guard
	// refuses (internal/cli's keepVoiceHop reads the name back out of the
	// module's own output, VoiceService.Name).
	//
	// Nothing in Go reads an address back for it: the one service of the
	// module's four whose callers mint their ID tokens for the Firebase project
	// rather than for the service's own URL (VoiceServiceSpec), so the hop is
	// the one that carries no url member.
	//
	// fugaro- is not reserved against these: that prefix is reserved by the
	// *job* listings (HistoryJob), and a Cloud Run service is not one of them
	// (CredentialsServiceName's own note).
	VoiceAccountID   = "fugaro-voice"
	VoiceServiceName = "fugaro-voice"
)

// credsImageRE is the image form the module's own validation admits
// (deploy/terraform/gcp/modules/installation/variables.tf): an image
// reference ending in an @sha256: digest of its own, never a bare tag. A tag
// is refused at plan time for a security reason rather than a preference —
// the service runs as the account that reads every credential in the bundle,
// and every operator holds roles/artifactregistry.writer on the registry the
// image comes from, so a mutable tag would let an operator push the code that
// the next owner apply runs as that account (D145: operators add, never
// read). Refused here too, so the stage that fills it fails before it writes
// a tfvars rather than in the plan.
var credsImageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]*@sha256:[0-9a-f]{64}$`)

// credsImageReleaseRE is the release an image reference names, and it is the
// module's own pattern for the same question
// (deploy/terraform/gcp/modules/installation/credentials.tf's
// credentials_image_release, which reads this same ":<version>@sha256:" out of
// the reference to decide whether the process inside it serves h2c). The two
// must move together: a reference whose version one side can read and the
// other cannot is an installation whose image and port disagree.
var credsImageReleaseRE = regexp.MustCompile(`:([0-9]+\.[0-9]+\.[0-9]+)@sha256:`)

// Credentials is the credentials service's spec for lc: the names the module
// uses, the bundle container the loader also derives, the image the caller
// verified, and the four facts the process is configured with (C1.14) — the
// Firebase project, the database, the job table and, once it is known, the
// service's own URL (which the stage fills on the apply after the one that
// creates the service; it is left empty here).
//
// The image must be pinned by digest (credsImageRE) — this is the check the
// stage's own test walks the tag form through, and it is the same rule the
// Terraform variable states, so a tfvars this function returns is never one
// the plan refuses for its image.
//
// The Firebase facts come from the local config's budget block, which `fugaro
// update --firebase-project` wrote from the Firebase root's outputs: the service reads
// the installation's own database, so an installation whose config names none
// cannot be configured at all — the service's own startup refuses a database
// URL that is missing or is not this installation's, and it builds that client
// before it serves anything, so the block would be a revision that never comes
// up. Both halves are refused here by name rather than written empty, which is
// what the revision would refuse by.
//
// publishedVerifiers is the verifier list the published policy node carries,
// which the caller reads and this writer cannot: the list reaches the service
// by two paths (the node, and the variable this function fills), and the rule
// that joins them is localcfg's — the configuration's own list when it states
// one, the node's otherwise — so a second operator's config, written before
// the key existed, does not render the variable null and empty a route another
// machine set up (C3.9g1, D1075).
func Credentials(lc *localcfg.Config, image string, publishedVerifiers []string) (CredentialsServiceSpec, error) {
	if !credsImageRE.MatchString(image) {
		return CredentialsServiceSpec{}, userErr("the credentials service's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits): a tag is mutable, and this service reads every model credential the installation holds", image)
	}
	b, jobs, err := credentialsInstallation(lc)
	if err != nil {
		return CredentialsServiceSpec{}, err
	}
	reviews, err := CredentialsReviewJobAccounts(lc)
	if err != nil {
		return CredentialsServiceSpec{}, err
	}
	shared, err := CredentialsSharedJobAccounts(lc)
	if err != nil {
		return CredentialsServiceSpec{}, err
	}
	signer, roster, admins := credentialsServiceIdentity(lc, b)
	return CredentialsServiceSpec{
		AccountID:         CredentialsAccountID,
		Service:           CredentialsServiceName,
		Image:             image,
		BundleSecret:      gcp.ProjectSecretID(lc.Name, CredentialsBundleName),
		FirebaseProject:   b.FirebaseProject,
		RTDBURL:           b.RTDBURL,
		JobAccounts:       jobs,
		ReviewJobAccounts: reviews,
		SharedJobAccounts: shared,
		// The one rule, applied here as the publisher applies it: the owner's
		// list when the config states one — an explicit empty list included,
		// which clears the route — and the node's otherwise. The pointer is
		// what keeps a written `[]` from being read as "nobody said" (the
		// field's own comment): nil here is the state the module's default is
		// for, and the account it creates is admitted.
		VerifierAccounts: verifierAccountsField(lc.VerifierAccountsEffective(publishedVerifiers)),
		// The identity half's three names (LM12's variables, D3121, D3123,
		// F29/R16), through the one derivation the carry reads too
		// (credentialsServiceIdentity), so the run that deploys the service and
		// every run after it configure it the same way.
		SignerAccount:         signer,
		RosterServiceAccounts: roster,
		ImplicitAdmins:        admins,
	}, nil
}

// credentialsServiceIdentity is the block's three identity names — the runs'
// signer, the roster's service accounts and the implicit admins (LM12's
// variables, D3121, D3123, F29/R16) — as one derivation, read by both writers
// of the block: the deploy that composes it (Credentials) and the carry that
// brings an installation's own block up to date
// (RefreshCredentialsServiceIdentity). It is one function so the two cannot
// disagree about what the service is configured with, which is the property
// every class list of this service has.
//
// b is the budget block the signer's project comes from: the signer is the
// Firebase root's own account, in the project that root creates it in, and the
// root has been applied by the time the local config names the project. The
// roster is the same merged launcher list the installation root's class is
// derived from, read one member class over (launcherServiceAccounts), and the
// implicit admins are the gate's own list, read the way the gate reads it
// (implicitAdmins).
func credentialsServiceIdentity(lc *localcfg.Config, b *localcfg.Budget) (string, []string, []string) {
	return serviceAccountEmail(SignerAccountID, b.FirebaseProject),
		launcherServiceAccounts(launchersWithOperators(lc.Terraform.Operators, lc.Terraform.Admins)),
		implicitAdmins(lc.Terraform.BudgetAdmins)
}

// implicitAdmins is the machine's implicit admins as the bare addresses the
// service compares a caller's Google ID token against (LM13; F29, R16 arm 1),
// and it is the LAUNCH GATE's own list: the `user:` entries of the local
// config's terraform.budget_admins, normalised by access.Member — the
// function the gate reads them with (internal/cli's userImplicitAdmins),
// whose member spelling is this list with `user:` in front of each address.
// Handing the service the same people is what makes the issuer's scope check
// give the gate's answer: an owner whose own address is on the list is
// admitted to a launch the gate admits today, and one the config does not
// name is refused in both places.
//
// A `group:` or `serviceAccount:` entry is not here and is not on the gate's
// list either: neither is a person the roles node can place, and the implicit
// class exists for the person whose grants are not written down yet. An
// address that does not parse is dropped, exactly as the gate drops it.
func implicitAdmins(members []string) []string {
	out := []string{}
	for _, m := range members {
		address, ok := strings.CutPrefix(m, "user:")
		if !ok {
			continue
		}
		member, err := access.Member(address)
		if err != nil {
			continue
		}
		if email, ok := strings.CutPrefix(member, "user:"); ok {
			out = append(out, email)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// CredentialsInstallationFacts is Credentials' own question about the
// installation, asked on its own: does this project's config name everything
// the service needs to exist in it at all — the Firebase database it reads,
// and at least one repository job it may admit? It is what a caller asks
// BEFORE it resolves the service's image, so a run whose installation cannot
// carry the service spends no registry read, no signature check and no pull
// on one; Credentials asks it too (credentialsInstallation, below), so the
// two can never disagree about an installation.
func CredentialsInstallationFacts(lc *localcfg.Config) error {
	_, _, err := credentialsInstallation(lc)
	return err
}

// credentialsInstallation is the budget block and the job table Credentials
// builds the service from, and the refusals an installation that names
// neither gets: the service reads the installation's own database and admits
// its repository jobs, so a project with no database named and a project
// whose table binds no run to a repository are installations the service
// cannot be deployed into (its own Authenticator refuses a table of none).
func credentialsInstallation(lc *localcfg.Config) (*localcfg.Budget, []CredentialsJob, error) {
	b, err := firebaseBackend(lc, "the credentials service")
	if err != nil {
		return nil, nil, err
	}
	jobs, err := CredentialsJobAccounts(lc)
	if err != nil {
		return nil, nil, err
	}
	return b, jobs, nil
}

// firebaseBackend is the Firebase project and database an installation's own
// cloud services read, and the two refusals a project that names neither gets.
// what names the thing asking, so the sentence an owner reads is about what
// they turned on: the credentials service reads the installation's own
// database (Credentials), and so does the resident orchestrator
// (infra.Orchestrator in orchestrator.go).
//
// The database's URL must be a Realtime Database URL: both readers build a
// client for it before they serve anything, so an installation whose config
// names a host that is not one is an installation whose service never comes up.
func firebaseBackend(lc *localcfg.Config, what string) (*localcfg.Budget, error) {
	b := lc.Budget
	if b == nil || b.FirebaseProject == "" || b.RTDBURL == "" {
		return nil, userErr("%s reads the installation's Firebase database, and project %s's config names no Firebase project and database (budget.firebase_project and budget.rtdb_url): run 'fugaro update --firebase-project <gcp-project-id>' first, which writes both from the Firebase root's outputs", what, lc.Name)
	}
	if err := rtdb.ValidateURL(b.RTDBURL, true); err != nil {
		return nil, userErr("budget.rtdb_url is not a Firebase Realtime Database URL: it must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app, with no credentials, path, query or fragment (a loopback address, which is the emulator, is admitted here). Run 'fugaro update --firebase-project <gcp-project-id>' to write it from the Firebase root's outputs")
	}
	return b, nil
}

// verifierAccountsField spells an effective verifier list as the three-state
// field CredentialsServiceSpec.VerifierAccounts. A nil list stays nil: nobody
// anywhere names a verifier, so the key is omitted and the module's own
// account is the default. Any list that was WRITTEN becomes a pointer to it,
// an empty one included, so `[]` reaches the module as `[]` and the owner's
// clear is never read as their silence.
func verifierAccountsField(effective []string) *[]string {
	if effective == nil {
		return nil
	}
	return &effective
}

// CredentialsJobAccounts is every job of the installation that may call the
// credentials service, as the triples the module's var.credentials_service
// carries: one row per repository, workflow and Claude account the repository
// may run it under.
//
// It is the same set of accounts the repository roots grant roles/run.invoker
// to (modules/workflow/sa.tf creates one job account per workflow and per
// account its own root allows), derived the same way but from the LOCAL
// config alone: the installation's tfvars are written by the installation
// stage, which never reads a repository's fugaro.yaml. So the account axis is
// config.AllowedClaudeAccounts — the accounts a repository's allow_data_to
// names — which is exactly WorkflowAccounts's list for an oauth repository and
// a superset of it for one whose agent authenticates with an API key. A
// superset is the fail-closed direction here: a row for an account a
// repository does not create binds an account no token is ever minted for,
// while a MISSING row is a job that holds an invoker grant and is refused by
// the service (the account is derived from the row, and the row is where the
// service learns which repository a caller belongs to).
//
// A repository whose provider the local config does not name is refused
// rather than skipped: without it neither the account's name nor the row is
// derivable, and a table that quietly left that repository's jobs out would
// deploy a service that refuses every one of their calls. doctor.go's
// projectSecretAccountOwners skips such a repository, which is right for a
// check that lists who owns a secret and wrong for the table a running
// service authenticates against.
//
// The rows are sorted (provider, repo, workflow) and carry no duplicates.
func CredentialsJobAccounts(lc *localcfg.Config) ([]CredentialsJob, error) {
	var out []CredentialsJob
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		r := lc.Repos[repo]
		if _, err := task.Slug(r.Provider, repo); err != nil {
			return nil, userErr("the credentials service binds a caller to its repository through the git provider and the repository name, and the local config does not say which provider %s is on: add repos.%s.provider to it (or fix the repository's name) and run this stage again", repo, repo)
		}
		seen := map[string]bool{}
		for _, w := range r.Workflows {
			for _, acct := range config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo) {
				// The default account IS the workflow's own job: its variant
				// is the workflow name itself (gcp.WorkflowVariant).
				v := gcp.WorkflowVariant(w, acct)
				if seen[v] {
					continue
				}
				seen[v] = true
				out = append(out, CredentialsJob{Provider: r.Provider, Repo: repo, Workflow: v})
			}
		}
	}
	if len(out) == 0 {
		return nil, userErr("the installation has no repository job to bind the credentials service to (the local config names no repository with a workflow): the service refuses a table that binds no run to a repository, so an installation with no jobs cannot have it")
	}
	slices.SortFunc(out, func(a, b CredentialsJob) int { return strings.Compare(credentialsJobKey(a), credentialsJobKey(b)) })
	return out, nil
}

// credentialsJobKey is one row's sort key and its identity: the three fields
// the service derives a caller's job from, joined so that two rows are the same
// row exactly when they name the same job. The separator is a NUL, which no
// provider, repository or variant can hold.
func credentialsJobKey(j CredentialsJob) string {
	return j.Provider + "\x00" + j.Repo + "\x00" + j.Workflow
}

// credentialsReviewJobKey is the review table's row identity, the same rule
// credentialsJobKey states for the coding table: two rows are one row exactly
// when they name one variant, and no profile, provider or account can hold the
// NUL separator.
func credentialsReviewJobKey(j CredentialsReviewJob) string {
	return j.Profile + "\x00" + j.Provider + "\x00" + j.Account
}

// RefreshCredentialsServiceJobAccounts returns vars with the credentials
// service's three job tables — job_accounts, review_job_accounts and
// shared_job_accounts — brought up to date with the installation the local
// config names now, and every other member of the block — the image, the URL,
// the bundle container, the database and the verifier list — exactly what it
// was.
//
// The three tables are the members of the block this side composes out of the
// local config (CredentialsJobAccounts over lc.Repos, CredentialsReviewJobAccounts
// and CredentialsSharedJobAccounts over lc.Profiles), and the deploy is their only other writer: Credentials
// fills them in on the run that CREATES the block, and every later run carries
// the block as it is found (CarriedCredentialsService). A repository onboarded
// after that deploy is therefore in the table of nobody — its root still grants
// its job roles/run.invoker on the service (modules/workflow/sa.tf), and the
// running service then refuses every call from an account whose row it does not
// hold, which is the failure CredentialsJobAccounts' own comment calls the one
// that must not happen. The review table is the same hazard one step along the
// axis: a profile, a provider or a Claude account added to the config after the
// deploy is a review variant the service's table does not name, and that
// variant's calls are refused exactly as a repository's would be. No other
// command could put either row there: `fugaro update` is the whole upgrade
// (CLAUDE.md's HARD RULE), so the refresh is part of it or it is nowhere.
//
// The rows found in the document are UNIONED with the derived ones rather than
// replaced by them, which is the fail-closed direction CredentialsJobAccounts
// already states: a row for a job the config no longer names binds an account
// no token is ever minted for, while a row a refresh DROPPED is a job that
// holds an invoker grant and is refused. It is also what keeps an installation
// whose config has not changed byte for byte what it was, which is what a
// carried block owes its readers. The review and shared tables are read and
// unioned the same way, with the one difference their own comments give: an
// installation that names no profile — or no profile on a provider a shared job
// serves — derives no row of them at all, so the empty table written here is
// the class's fail-closed state and not the refusal an empty job_accounts is.
// The shared table's ONE other difference is that a row's repositories are not
// part of its identity, so a row the config still derives is taken whole from
// the config rather than kept as found (see the union below).
//
// A config that cannot describe a table at all — a named repository whose
// provider it does not state, no repository with a workflow, or a profile whose
// review variants cannot be derived — is refused rather than written around,
// and so is a document whose rows this program cannot read: the caller then
// keeps the block it found. Refreshing from a config that is missing a
// repository is the removal the union is there to prevent, and dropping a
// member is worse still.
func RefreshCredentialsServiceJobAccounts(vars []byte, lc *localcfg.Config) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no credentials_service to refresh the job table of")
	}
	var found []CredentialsJob
	if raw, ok := svc["job_accounts"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &found); err != nil {
			return nil, fmt.Errorf("the installation's credentials_service.job_accounts is not a list of provider, repo and workflow rows: %w", err)
		}
	}
	jobs, err := CredentialsJobAccounts(lc)
	if err != nil {
		return nil, err
	}
	merged := found
	seen := make(map[string]bool, len(found)+len(jobs))
	for _, j := range found {
		seen[credentialsJobKey(j)] = true
	}
	for _, j := range jobs {
		if seen[credentialsJobKey(j)] {
			continue
		}
		seen[credentialsJobKey(j)] = true
		merged = append(merged, j)
	}
	slices.SortFunc(merged, func(a, b CredentialsJob) int { return strings.Compare(credentialsJobKey(a), credentialsJobKey(b)) })
	svc["job_accounts"] = merged
	// The review table, read and unioned by the same rules and with the same
	// refusal for a document it cannot read: the two tables are the two members
	// this side composes, and a run that refreshed one of them and dropped the
	// other would leave the class the service admits behind the deployment that
	// creates it.
	var foundReview []CredentialsReviewJob
	if raw, ok := svc["review_job_accounts"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &foundReview); err != nil {
			return nil, fmt.Errorf("the installation's credentials_service.review_job_accounts is not a list of profile, provider and account rows: %w", err)
		}
	}
	reviews, err := CredentialsReviewJobAccounts(lc)
	if err != nil {
		return nil, err
	}
	mergedReview := make([]CredentialsReviewJob, 0, len(foundReview)+len(reviews))
	mergedReview = append(mergedReview, foundReview...)
	seenReview := make(map[string]bool, len(foundReview)+len(reviews))
	for _, r := range foundReview {
		seenReview[credentialsReviewJobKey(r)] = true
	}
	for _, r := range reviews {
		if seenReview[credentialsReviewJobKey(r)] {
			continue
		}
		seenReview[credentialsReviewJobKey(r)] = true
		mergedReview = append(mergedReview, r)
	}
	// An empty union leaves the member exactly as the document carried it,
	// absent included: the module renders no entry for an empty list, so writing
	// one would change a carried block — and the plan of an installation whose
	// config names no profile — for no fact at all. A union with a row in it is
	// always written, so nothing found is ever dropped.
	if len(mergedReview) > 0 {
		slices.SortFunc(mergedReview, func(a, b CredentialsReviewJob) int {
			return strings.Compare(credentialsReviewJobKey(a), credentialsReviewJobKey(b))
		})
		svc["review_job_accounts"] = mergedReview
	}
	// The shared table, read and unioned by the same rules, with the two
	// differences its own row shape gives. The first is that the repositories a
	// row carries are not part of its identity (credentialsSharedJobKey), so a
	// row the config still derives has its repositories taken from the config:
	// the profile's own allow_repos has moved where it moved, and a carried row
	// kept verbatim would hold an admission the config no longer states (a
	// repository switched off stays served, or one added stays refused and its
	// runs die at their first git command). What that replacement may NOT do is
	// narrow a row derived through sharedJobRepos' fallback, because the
	// fallback is the UPDATING machine's own record and not the installation's:
	// there the derived repositories are unioned with the carried ones
	// (sharedRowReposComeFromTheMachine, unionSharedRepos). A row the config no
	// longer derives is carried as it is, which is the direction every table
	// here is unioned in: a row for a job the installation still holds is a job
	// the service must keep admitting, and dropping it would refuse a caller
	// the deployment runs.
	var foundShared []CredentialsSharedJob
	if raw, ok := svc["shared_job_accounts"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &foundShared); err != nil {
			return nil, fmt.Errorf("the installation's credentials_service.shared_job_accounts is not a list of profile, provider, account and repos rows: %w", err)
		}
	}
	shared, err := CredentialsSharedJobAccounts(lc)
	if err != nil {
		return nil, err
	}
	mergedShared := make([]CredentialsSharedJob, 0, len(foundShared)+len(shared))
	mergedShared = append(mergedShared, foundShared...)
	at := make(map[string]int, len(mergedShared))
	for i, r := range mergedShared {
		at[credentialsSharedJobKey(r)] = i
	}
	// The repositories the MERGED coding table above runs jobs for, by
	// provider: the installation's own record of what it serves, every
	// machine's entries in it, and the list a machine-fallback row is refreshed
	// from.
	providerRepos := map[string][]string{}
	for _, j := range merged {
		canonical, err := projectpolicy.Canonical(j.Provider, j.Repo)
		if err != nil {
			continue
		}
		if !slices.Contains(providerRepos[j.Provider], canonical) {
			providerRepos[j.Provider] = append(providerRepos[j.Provider], canonical)
		}
	}
	for _, r := range shared {
		i, carried := at[credentialsSharedJobKey(r)]
		r.Repos = slices.Clone(r.Repos)
		// This machine's record is the fallback and never the whole of what
		// the profile serves (sharedJobRepos), so a row derived through it is
		// UNIONED with what the block carries and with the repositories the
		// merged coding table runs jobs for, rather than written over them: an
		// installation is updated from whichever machine runs the command, and
		// a config that does not record a teammate's repository — while the
		// coding table keeps that repository's job, unioned like every table
		// here — would otherwise narrow the row and refuse the shared run of a
		// repository the installation still runs jobs for. That is Check 580's
		// 403 again, one axis over. The boundary is the published policy either
		// way: projectpolicy.Admits is read by the service on every call
		// (credsvc's pairShared), so a repository this installation no longer
		// publishes is refused there whatever a row says.
		pr, derived := lc.Profiles[r.Profile]
		if derived && sharedRowReposComeFromTheMachine(pr) {
			var held []string
			if carried {
				held = mergedShared[i].Repos
			}
			r.Repos = unionSharedRepos(r.Repos, slices.Concat(held, providerRepos[pr.Provider]))
		}
		if carried {
			mergedShared[i] = r
			continue
		}
		at[credentialsSharedJobKey(r)] = len(mergedShared)
		mergedShared = append(mergedShared, r)
	}
	if len(mergedShared) > 0 {
		slices.SortFunc(mergedShared, func(a, b CredentialsSharedJob) int {
			return strings.Compare(credentialsSharedJobKey(a), credentialsSharedJobKey(b))
		})
		svc["shared_job_accounts"] = mergedShared
	}
	doc["credentials_service"] = svc
	return sortedJSON(doc)
}

// RefreshCredentialsServiceIdentity returns vars with the credentials service
// block's three identity names — signer_account, roster_service_accounts and
// implicit_admins — brought up to date with the installation the local config
// names now, and every other member of the block exactly what it was.
//
// The three names are the block's members this side derives from the local
// config, and the deploy is their only other writer (Credentials): the run that
// CREATES the block fills them in, and every later run carries the block as it
// is found (CarriedCredentialsService). The names are new in this release, so
// every installation that has the block from before it carries one without
// them — and the block is carried byte for byte, so nothing else ever adds
// them. That is the whole of LM13's acceptance at the installations that
// matter: the roster's `serviceAccount:` entries and the implicit admins are
// rendered to the service there too, the account that may knock and the account
// that may be served stay one list (the module makes the service's invoker
// grant for the same variable, iam.tf's credentials_roster_service_account),
// and the runs' signer reaches the process, without which LM14's probe has
// nothing to read and the minter grant can never move.
//
// The three are REPLACED, not unioned, which is where they part from the two
// job tables RefreshCredentialsServiceJobAccounts composes: a job row the
// refresh dropped is a job that holds an invoker grant and is refused by the
// service (the union's own comment), while a roster or implicit-admin member
// the config no longer names is a GRANT the service would keep for an account
// that no longer launches, or an admission the CLI's own gate has already
// withdrawn — a widening nothing can undo, and, for the implicit admins, a
// disagreement between the two ends of the one rule F29/R16 arm 1 states.
// Replacing is also what keeps an installation whose config has not changed
// converging on the bytes it already has, because the derivation is the
// deploy's own (credentialsServiceIdentity).
//
// A name with nothing to carry is DELETED rather than written empty, which is
// what the spec's `omitempty` tags write and what the module's own defaults
// read as the empty class: an installation whose roster names no service
// account, or whose config names no implicit admin, then carries exactly the
// bytes the run that deployed the service would have written.
//
// A config that cannot name the Firebase project the signer lives in is refused
// rather than written around (firebaseBackend), the direction the job tables'
// refresh takes: the caller then keeps the block it found, so an installation
// whose service is deployed but whose config no longer names the project is
// left with the names it has rather than a block with the signer dropped.
func RefreshCredentialsServiceIdentity(vars []byte, lc *localcfg.Config) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no credentials_service to refresh the identity names of")
	}
	b, err := firebaseBackend(lc, "the credentials service")
	if err != nil {
		return nil, err
	}
	signer, roster, admins := credentialsServiceIdentity(lc, b)
	if signer == "" {
		delete(svc, "signer_account")
	} else {
		svc["signer_account"] = signer
	}
	if len(roster) == 0 {
		delete(svc, "roster_service_accounts")
	} else {
		svc["roster_service_accounts"] = roster
	}
	if len(admins) == 0 {
		delete(svc, "implicit_admins")
	} else {
		svc["implicit_admins"] = admins
	}
	doc["credentials_service"] = svc
	return sortedJSON(doc)
}

// CredentialsReviewJobAccounts is the installation's review-only job variants
// as the module's var.credentials_service carries them: one row per variant
// the deployer creates, read off the derivation that creates them rather than
// spelled a second time (ProjectReviewJobs), so the accounts this service
// admits are the accounts the deployment runs.
//
// It is the same axis as CredentialsJobAccounts and one step further along it:
// the coding table is a row per repository job, this one a row per review
// variant of the installation's project jobs. An installation that deploys no
// variant at all gets an empty table and no error — an installation with no
// profile and no credentials service, whose review class is inert, which is
// the fail-closed state and not the refusal an empty coding table is. The
// implicit default review job is one of those variants and reaches the table
// the same way as every other (ProjectReviewJobs' implicitReviewVariant), so a
// project with no profile gets its row here on the run that deploys its job.
//
// The rows are sorted (profile, provider, account) and carry no duplicates.
func CredentialsReviewJobAccounts(lc *localcfg.Config) ([]CredentialsReviewJob, error) {
	// Nothing can be derived, and nothing else about the installation is read:
	// no profile deploys a coding job to derive a variant from, and with no
	// credentials service there is no implicit one to deploy either — so not
	// even the runs bucket, which only a job's own environment needs
	// (ProjectReviewJobs' own two rules, read here where they save the read).
	if len(lc.Profiles) == 0 && lc.CredentialsService == nil {
		return nil, nil
	}
	b, err := bucketName(lc)
	if err != nil {
		return nil, userErr("%v", err)
	}
	jobs, err := ProjectReviewJobs(lc, b, lc.UsesVertex())
	if err != nil {
		return nil, err
	}
	out := make([]CredentialsReviewJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, CredentialsReviewJob{Profile: j.Profile, Provider: j.Provider, Account: j.Account})
	}
	if len(out) == 0 {
		// An empty table leaves the member exactly as the document carried it
		// (RefreshCredentialsServiceJobAccounts' own rule for the union), and
		// the module renders no entry for an empty list: a written `[]` would
		// be a fact about the installation that is not one.
		return nil, nil
	}
	slices.SortFunc(out, func(a, b CredentialsReviewJob) int {
		if c := strings.Compare(a.Profile, b.Profile); c != 0 {
			return c
		}
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		return strings.Compare(a.Account, b.Account)
	})
	return slices.Compact(out), nil
}

// CredentialsSharedJobAccounts is the installation's SHARED project jobs as
// the module's var.credentials_service carries them: one row per
// (profile, provider, account) the deployer creates a shared job for
// (ProjectJobs' axis), with the repositories that profile serves.
//
// It is derived from the same two facts the deployer derives its jobs from and
// from no third one: the profiles of the local config, held to SharedJobProvider
// (the one predicate that says which providers a shared job serves) and to the
// account axis with the names this installation declares a KEY credential left
// out (config.ClaudeAccountTokenNames, which is the very filter projectJobs
// applies when it composes the jobs) — so the accounts this service admits are
// the accounts the deployment runs, which is the claim credsvc's
// TestSharedJobAccountsAreTheNamesProjectJobsDeploys makes about the other
// half. A profile is counted whatever its records say about the service
// switch: the row is the class the service admits, and the switch is the
// separate fact that decides whether a launch of that profile is handed this
// service's address at all (CredentialsServiceServesProfile).
//
// An installation that deploys no shared job at all gets an empty table and no
// error — the review table's own rule and for its own reason (the coding table
// above is the exception, and the service refuses an empty version of it): a
// service whose shared class is empty answers every caller of it with the
// class's refusal, which is the fail-closed state and not a deployment that
// cannot start.
func CredentialsSharedJobAccounts(lc *localcfg.Config) ([]CredentialsSharedJob, error) {
	var out []CredentialsSharedJob
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		if !SharedJobProvider(pr.Provider) {
			continue
		}
		served := config.ClaudeAccountTokenNames(lc.Credentials, config.ProjectJobAccounts(pr))
		repos := sharedJobRepos(lc, pr)
		for _, account := range served {
			out = append(out, CredentialsSharedJob{Profile: name, Provider: pr.Provider, Account: account, Repos: repos})
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	slices.SortFunc(out, func(a, b CredentialsSharedJob) int {
		return strings.Compare(credentialsSharedJobKey(a), credentialsSharedJobKey(b))
	})
	return out, nil
}

// sharedJobRepos is the repositories a shared job's row carries: what its
// profile serves, which is the profile's own allow_repos when it names one
// (projectpolicy.ProfileAllowRepos' rule) and, when it names none, the
// repositories this machine's config records for the profile's provider.
//
// The fallback is the installation's own record of its repositories and not
// the published project list, which is the same list with every other
// machine's shared entries in it (internal/cli's seedAllowRepos): reading it
// here would be a database call the deploy path deliberately makes none of
// (internal/cli's deployCredentialsService), and every entry the config does
// not record is a repository whose shared caller this service then refuses —
// which is the fail-closed direction for a value that is an admission, and
// never a widening of one. What is written is always a subset of what the
// project's own list admits, so the two gates the service reads a call
// through (projectpolicy.Admits) are read here as they are read there.
//
// The value is never nil: the member is REQUIRED and an empty one is the
// deny-all profile, while a missing one is a document the service refuses to
// read at all.
//
// The fallback above is this machine's record and not the installation's, so a
// refresh may not write a row over a carried one with it:
// sharedRowReposComeFromTheMachine is the predicate the refresh asks, and the
// union it makes there is why a machine whose config does not name a
// teammate's repository cannot narrow a row.
func sharedJobRepos(lc *localcfg.Config, pr config.JobProfile) []string {
	if len(pr.AllowRepos) > 0 {
		return slices.Clone(pr.AllowRepos)
	}
	out := []string{}
	for _, key := range slices.Sorted(maps.Keys(lc.Repos)) {
		entry := lc.Repos[key]
		if entry.Provider != pr.Provider {
			continue
		}
		canonical, err := projectpolicy.Canonical(entry.Provider, key)
		if err != nil {
			// A repository the config cannot canonicalise is one no admission
			// of this service could ever match: it is dropped rather than
			// written as a pattern that admits nothing.
			continue
		}
		out = append(out, canonical)
	}
	return out
}

// sharedRowReposComeFromTheMachine reports whether a shared row's repositories
// are the machine-recorded fallback rather than the profile's own allow_repos:
// it is sharedJobRepos' two branches, asked by the refresh, which may replace a
// carried row's repositories with the config's own when they are the profile's
// own statement and never when they are this machine's fallback.
//
// It is asked only of a profile the config still names: a carried row whose
// profile the config no longer derives is kept whole like every other table's,
// which is why the caller asks lc.Profiles for the profile and not this
// predicate alone (a missing profile is the zero JobProfile, which this would
// read as the fallback branch).
func sharedRowReposComeFromTheMachine(pr config.JobProfile) bool { return len(pr.AllowRepos) == 0 }

// unionSharedRepos is derived's entries followed by carried's that derived does
// not already hold, sorted: the row repositories a refresh writes when the
// fallback branch above is what derived them. The result is a superset of what
// the block carried, so a second refresh of the same document derives the same
// list and converges on it.
//
// Sorted rather than in either list's own order because the two lists belong to
// two machines: one that recorded a repository the other did not derives a
// different order for the same set, and a row whose content is the same but
// whose bytes differ plans a service update — a new revision — for a
// reordering an owner cannot see.
func unionSharedRepos(derived, carried []string) []string {
	out := slices.Clone(derived)
	for _, repo := range carried {
		if !slices.Contains(out, repo) {
			out = append(out, repo)
		}
	}
	slices.Sort(out)
	return out
}

// CredentialsServiceURLOf returns the credentials_service.url the installation
// root's tfvars carry, and "" when they carry none — either because no
// credentials_service is set at all or because it is the apply that created
// the service and the URL is not known yet.
//
// It is what an ordinary run carries forward (the stage's prepare): the URL is
// the audience of every caller's ID token, and a run that wrote the service's
// other facts without it would replace the revision's real audience with the
// module's unassigned placeholder, which admits nobody.
func CredentialsServiceURLOf(vars []byte) (string, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return "", err
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	url, _ := svc["url"].(string)
	return url, nil
}

// RepoCredentialsServiceDeployed returns whether a REPOSITORY root's tfvars
// carry the installation's credentials service: the key
// installation.credentials_service, present and not null. Unlike the
// installation root's own credentials_service, which sits at the top level of
// its document, a repository root carries its copy under installation, the
// object the workflow module reads (RepoVars, spec.go's RepoInstallation).
//
// The key is the whole of the switch's first flip for that root:
// repoCredentialsService makes it nil for every other answer, and the module
// reads null as "the service is off for this repository" — with it null the
// job keeps its model mounts and its App key mount and no account of it is
// granted roles/run.invoker, and with it set all three are the other way
// (modules/workflow/sa.tf). So a document that carries it is the record of a
// root that was applied with the service on, and one that does not is the
// record of a root that was applied with it off — including the root of an
// installation that has no service at all.
//
// It is the document the last run that applied this root handed Terraform, and
// it is read where the run's own fugaro.yaml cannot be: a launch from a
// directory that holds no checkout of the repository has no agent.auth to
// judge the switch on (internal/cli's credentialsServiceLaunchEnv), and the
// deployed root is then the fact the job's own containers carry.
func RepoCredentialsServiceDeployed(vars []byte) (bool, error) {
	if vars == nil {
		return false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return false, fmt.Errorf("the repository's terraform.tfvars.json is not readable: %w", err)
	}
	inst, _ := doc["installation"].(map[string]any)
	svc, ok := inst["credentials_service"]
	return ok && svc != nil, nil
}

// SetCredentialsServiceURL returns the installation root's tfvars with
// credentials_service.url set to url and every other entry — the service's own
// facts included — unchanged. It is the same read-modify-write of one entry
// SetCredentialsService makes, for the same reason: the document on disk
// carries the run's discovery, and rebuilding it from a spec would drop it.
//
// The URL is the one the module's own output carries back
// (infra.CredentialsService.URL, outputs.tf's
// google_cloud_run_v2_service.credentials[0].uri), never a value this package
// composes: the audience a Cloud Run front end checks a caller's ID token
// against is the URL it serves the revision on, project number and all.
func SetCredentialsServiceURL(vars []byte, url string) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, err
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no credentials_service to set the service's URL on")
	}
	svc["url"] = url
	doc["credentials_service"] = svc
	return sortedJSON(doc)
}

// CredentialsServiceImageOf returns the image the installation root's tfvars
// carry for the credentials service, and false when they carry no service at
// all (D147) or one with no image.
//
// It is the read an ordinary `fugaro update` makes to learn what this
// installation's service is: every run rewrites these tfvars from a spec that
// never carries the key (init.go's prepare) and carries the entry forward from
// this document, so the document is the only thing in the run that knows. An entry with no image answers false
// rather than pinning onto it: the module refuses a reference that is not
// digest-pinned, so the plan is already the refusal, and writing an image into
// an entry whose other facts are missing would describe half a service.
func CredentialsServiceImageOf(vars []byte) (image string, ok bool, err error) {
	if vars == nil {
		return "", false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return "", false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		return "", false, nil
	}
	image, _ = svc["image"].(string)
	if image == "" {
		return "", false, nil
	}
	return image, true, nil
}

// SetCredentialsServiceImage returns vars with the credentials service's image
// replaced by image, and every other entry — of the document and of the
// service's own entry — byte-for-byte what it was.
//
// The read-modify-write is the same one SetCredentialsService makes and for a
// sharper reason: the service's remaining facts (its account, its bundle, the
// Firebase database it reads, the job accounts it binds a caller to, the
// audience its callers mint their ID tokens for) are the installation's own
// record, written by the run that turned the service on, and a re-pin that
// rebuilt them from a spec would replace the audience with the module's
// placeholder and stop admitting every call.
//
// A document with no service is refused rather than given one: the image is a
// release's, and writing it here would turn the service on — account, bundle,
// grant and all — for an installation whose owner never opted in.
func SetCredentialsServiceImage(vars []byte, image string) ([]byte, error) {
	if !credsImageRE.MatchString(image) {
		return nil, fmt.Errorf("the credentials service's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits)", image)
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	svc, _ := doc["credentials_service"].(map[string]any)
	if svc == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no credentials_service to set the image of")
	}
	svc["image"] = image
	doc["credentials_service"] = svc
	return sortedJSON(doc)
}

// CredentialsServiceImageRelease returns the release the credentials service's
// image reference image names, and "" when it names none.
//
// It is the module's own reading of the same string — credentials.tf's
// credentials_image_release, which is what decides whether the container's
// port is named h2c — kept as the same pattern on purpose: the two sides must
// agree about which release an installation runs, or a re-pin would move the
// image and leave the port behind.
func CredentialsServiceImageRelease(image string) string {
	m := credsImageReleaseRE.FindStringSubmatch(image)
	if m == nil {
		return ""
	}
	return m[1]
}

// SetCredentialsService returns the installation root's tfvars vars — as
// InstallationVars wrote them — with credentials_service set to svc, and
// every other entry byte-for-byte what it was.
//
// It is a read-modify-write of the ONE key and never a rebuild of the
// document, because the tfvars that are on disk when the deploy runs are not
// what InstallationVars(spec) would produce: the run that wrote
// them had run discovery, so they carry the module's project_secrets (the
// live project-wide containers), adopt_legacy_registry and the run's imports
// — values only that run's discovery knew. Rewriting the document from the
// stage's own spec would plan the destruction of every project secret the
// discovery had found.
func SetCredentialsService(vars []byte, svc CredentialsServiceSpec) ([]byte, error) {
	raw, err := json.Marshal(svc)
	if err != nil {
		return nil, err
	}
	return setVarsKey(vars, "credentials_service", raw)
}

// CarriedCredentialsService returns vars with the credentials_service entry
// that the workdir's PREVIOUS tfvars carried, when vars carry none of their
// own.
//
// prev is those tfvars — infra.Workdir.PrevVars, which PrepareWorkdir read
// before it rebuilt the tree, or infra.ReadWorkdirVars — and nil when the
// workdir held none. It is handed over as bytes rather than read from a path
// here on purpose: the caller's workdir has already been prepared, so the file
// is gone, and a read at this point would answer "nothing to carry" for an
// installation that has the service (the bug C1.20's review found).
//
// The variable is the deploy's own (init_stages.go's ensureCredentialsService), and
// every run of init rewrites the tfvars from its spec — which never sets the
// key.
// Carried forward exactly as it was, present or absent, an installation that
// has opted in keeps the service across an ordinary run's rewrite; without
// that, the next plain `fugaro update` would plan the service, its account and
// its grant away (a plan the bundle container's prevent_destroy refuses,
// so the run fails rather than applies anything). An installation that never
// opted in carries no key and is unchanged, and the runbook's opt-out — the
// owner deleting the key by hand — is respected for the same reason: the
// entry is taken as it is found.
//
// The one member this does NOT take as it is found is verifier_accounts, which
// the block of an installation that opted in before this release does not
// carry at all (CSR14). verifiers is the effective list (localcfg's keep rule:
// the config's own when it states one, the published node's otherwise) and it
// is written into the block — `[]` included — or, when it is nil, deleted from
// it, which leaves the module's own verifier account as the default. Every
// other member is carried byte for byte, because a rebuild from this side
// knows nothing about a service the owner opted into.
//
// It has to be this way round for CLAUDE.md's HARD RULE. The stage that writes
// the block runs only when the owner asks for the service, and an installation
// at the previous version already has it: one ordinary `fugaro update` carries
// the block and creates the account, so the block has to carry the account's
// address in the same run or the account is admitted for nobody — and an owner
// who cleared the list has to get `[]`, not the default, or the service admits
// a caller the published node does not name.
func CarriedCredentialsService(vars, prev []byte, verifiers []string) ([]byte, error) {
	return carriedCredentialsService(vars, prev, func(block map[string]any) {
		if verifiers == nil {
			delete(block, "verifier_accounts")
			return
		}
		block["verifier_accounts"] = verifiers
	})
}

// CarriedCredentialsServiceAsFound is CarriedCredentialsService with the block
// taken exactly as it is found — verifier_accounts included — for the one
// caller that has no effective list to write: `fugaro setup --print-vars`
// (init.go), which prints the variables this machine's workdir holds and
// deliberately reads no cloud, so it cannot know whether the published node
// names a verifier. It must not pass nil to CarriedCredentialsService
// instead: on a config that is silent about the list — the ordinary state, and
// the only one this path can detect — a nil third argument DELETES the member,
// so the printed document dropped the owner's list the workdir carries while
// the same machine's `fugaro update` writes it into the block. What the
// member means to a reader is that the service admits those callers, so a
// deleted member reads as "the module's default" and the print disagrees with
// the installation it stands for.
//
// The two functions share one body so they cannot drift in what they do to
// every other member of the block.
func CarriedCredentialsServiceAsFound(vars, prev []byte) ([]byte, error) {
	return carriedCredentialsService(vars, prev, nil)
}

// carriedCredentialsService is the carry the two exported functions are:
// the previous document's credentials_service entry, taken as it is found,
// handed to rewrite for the members a caller knows better. rewrite is nil when
// the caller knows none (CarriedCredentialsServiceAsFound).
func carriedCredentialsService(vars, prev []byte, rewrite func(block map[string]any)) ([]byte, error) {
	if prev == nil {
		return vars, nil // no workdir held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// drops the service: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	svc, ok := previous["credentials_service"]
	if !ok || svc == nil {
		return vars, nil
	}
	if block, ok := svc.(map[string]any); ok && rewrite != nil {
		rewrite(block)
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc["credentials_service"] = svc
	return sortedJSON(doc)
}

// Intake is the secrets intake's block for an installation that has none:
// the module's own account and service names, running image.
//
// The image is the release's, and it is the one the credentials service's
// block carries: see IntakeSpec — one pin, two services, and the digest form
// is the module's own rule (var.intake's validation), so a caller that hands
// in a tag writes a document the plan refuses.
func Intake(image string) IntakeSpec {
	return IntakeSpec{AccountID: IntakeAccountID, Service: IntakeServiceName, Image: image}
}

// SetIntake returns the installation root's tfvars vars with intake set to
// spec, and every other entry byte-for-byte what it was. It is
// SetCredentialsService's read-modify-write of the ONE key and for the same
// reason: the document on disk carries the run's discovery — the live project
// secrets it found, its legacy registry, its imports — and rebuilding it from a
// spec would plan that discovery away.
func SetIntake(vars []byte, spec IntakeSpec) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return setVarsKey(vars, "intake", raw)
}

// IntakeOf returns the block the installation root's tfvars carry for the
// secrets intake, and false when they carry none (the variable is off), an
// empty one, or one whose service or image is missing — a block that names no
// service is not one a URL could be filled into, and the callers that ask are
// the ones whose next step is a write into it.
//
// It is what tells an ordinary run whether this installation has the intake at
// all, which is a question the run's own config cannot answer: the block is the
// record of what the earlier apply created.
func IntakeOf(vars []byte) (IntakeSpec, bool, error) {
	if vars == nil {
		return IntakeSpec{}, false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return IntakeSpec{}, false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, _ := doc["intake"].(map[string]any)
	if block == nil {
		return IntakeSpec{}, false, nil
	}
	spec := IntakeSpec{}
	spec.AccountID, _ = block["account_id"].(string)
	spec.Service, _ = block["service"].(string)
	spec.Image, _ = block["image"].(string)
	spec.URL, _ = block["url"].(string)
	spec.People = stringList(block["people"])
	if spec.Service == "" || spec.Image == "" {
		return IntakeSpec{}, false, nil
	}
	return spec, true, nil
}

// IntakeURLOf returns the intake.url the installation root's tfvars carry, and
// "" when they carry none — either because there is no intake at all or because
// it is the apply that created the service and the module's unassigned
// placeholder is on the revision.
//
// It is what an ordinary run compares the module's output against: the URL is
// the audience of the intake's callers' ID tokens, and a run that wrote the
// service without it would leave a revision that admits nobody, however healthy
// it looks.
func IntakeURLOf(vars []byte) (string, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return "", err
	}
	block, _ := doc["intake"].(map[string]any)
	url, _ := block["url"].(string)
	return url, nil
}

// LauncherAccountsOf returns the launcher_accounts the installation root's
// tfvars carry, and the empty list when they carry none — the module's own
// default, which is also what the key's omitempty writes for an installation
// whose launcher list names no `user:` member (InstallationSpec.LauncherAccounts,
// InstallationVars).
//
// It is read beside IntakeOf's people because the intake's class is the two
// TOGETHER: the block's people list is the roster's own, and whether it moves
// the class the module reads depends on what the launchers already carry
// (IntakeClass, intake.tf's local.intake_accounts).
func LauncherAccountsOf(vars []byte) ([]string, error) {
	if vars == nil {
		return []string{}, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, err
	}
	return stringList(doc["launcher_accounts"]), nil
}

// SetIntakeURL returns vars with intake.url set to url and every other entry —
// of the document and of the intake's own block — unchanged (the credentials
// service's own read-modify-write, SetCredentialsServiceURL).
//
// A document with no intake block is refused rather than given one: the URL is
// one service's, and writing it into a block this function invented would turn
// the intake on — its account, its two roles, its service and its invoker
// grants — for an installation whose run never resolved an image for it.
func SetIntakeURL(vars []byte, url string) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, err
	}
	block, _ := doc["intake"].(map[string]any)
	if block == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no intake to set the service's URL on")
	}
	block["url"] = url
	doc["intake"] = block
	return sortedJSON(doc)
}

// SetIntakeImage returns vars with the intake's image replaced by image, and
// every other entry byte-for-byte what it was — SetCredentialsServiceImage's
// re-pin, on the block that runs the same reference.
//
// It is how `fugaro update`'s credentials-service stage moves the intake onto
// this release with the service it shares the pin with: the two are one
// reference, and a run that moved only one would leave the intake running the
// release behind's process against the node this run published — the state
// credentials.tf's h2c decision and the intake's own node reads both turn on.
func SetIntakeImage(vars []byte, image string) ([]byte, error) {
	if !credsImageRE.MatchString(image) {
		return nil, fmt.Errorf("the secrets intake's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits)", image)
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, _ := doc["intake"].(map[string]any)
	if block == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no intake to set the image of")
	}
	block["image"] = image
	doc["intake"] = block
	return sortedJSON(doc)
}

// SetIntakePeople returns vars with the intake's second class list set to
// people, and every other entry — of the document and of the intake's own
// block — unchanged (SetIntakeURL's read-modify-write, on the block's other
// member).
//
// An empty list REMOVES the key rather than writing [], and that is the
// document's own shape: the module's default is the empty list, so the absent
// key and [] are one plan, and IntakeSpec.People's omitempty writes a block
// without it. It is also the state every installation that is not a workspace's
// project is in, and a run that wrote [] into one would put a diff in its
// tfvars for a fact it does not have.
//
// The list is sorted and deduplicated here, so two equal rosters give equal
// bytes (sortedJSON's promise) whatever order a caller read them in.
//
// A document with no intake block is refused rather than given one, for
// SetIntakeURL's reason: the people are a member of a block that turns on an
// account, two roles, a service and its invoker grants, and a caller that
// invented one would deploy the intake for an installation whose run never
// resolved an image for it.
func SetIntakePeople(vars []byte, people []string) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, err
	}
	block, _ := doc["intake"].(map[string]any)
	if block == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no intake to set the roster's people on")
	}
	sorted := append([]string{}, people...)
	slices.Sort(sorted)
	delete(block, "people")
	if sorted = slices.Compact(sorted); len(sorted) > 0 {
		arr := make([]any, 0, len(sorted))
		for _, p := range sorted {
			arr = append(arr, p)
		}
		block["people"] = arr
	}
	doc["intake"] = block
	return sortedJSON(doc)
}

// CarriedIntake returns vars with the intake entry the document prev carried,
// when vars carry none of their own — CarriedCredentialsService's carry, on the
// second variable this side writes and the spec never holds.
//
// The block is taken exactly as it is found, its URL included, because the URL
// is the audience: a run that rebuilt the block from the release's pin alone
// would put the module's unassigned placeholder back on the revision and stop
// admitting every caller until the same run's audience fill put it back.
//
// prev is that document — infra.Workdir.PrevVars, or the tfvars the workdir
// holds now, which is where the installation stage's own ensureIntakeBlock
// wrote the entry a few steps before prepare rewrites the file. An entry is
// carried forward present or absent: an installation that has the intake keeps
// it across an ordinary run's rewrite, and one that never had it is unchanged
// rather than given one by a run that resolved no image for it.
func CarriedIntake(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no workdir held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// drops the intake: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := previous["intake"]
	if !ok || block == nil {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc["intake"] = block
	return sortedJSON(doc)
}

// SetVoiceService returns the installation root's tfvars vars with
// voice_service set to spec, and every other entry byte-for-byte what it was.
// It is SetIntake's read-modify-write of the ONE key and for the same reason:
// the document on disk carries the run's discovery — the live project secrets
// it found, its legacy registry, its imports — and rebuilding it from a spec
// would plan that discovery away.
func SetVoiceService(vars []byte, spec VoiceServiceSpec) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return setVarsKey(vars, "voice_service", raw)
}

// VoiceServiceOf returns the block the installation root's tfvars carry for
// the voice hop, and false when they carry none (the variable is off), an
// empty one, or one whose service or image is missing — a block that names no
// service is not one a revision could be created from.
//
// It is what tells an ordinary run whether this installation has the hop at
// all, which is a question the run's own config cannot answer: the block is
// the record of what the earlier apply created, exactly as IntakeOf's answer
// is for the intake.
func VoiceServiceOf(vars []byte) (VoiceServiceSpec, bool, error) {
	if vars == nil {
		return VoiceServiceSpec{}, false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return VoiceServiceSpec{}, false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, _ := doc["voice_service"].(map[string]any)
	if block == nil {
		return VoiceServiceSpec{}, false, nil
	}
	spec := VoiceServiceSpec{}
	spec.AccountID, _ = block["account_id"].(string)
	spec.Service, _ = block["service"].(string)
	spec.Image, _ = block["image"].(string)
	if spec.Service == "" || spec.Image == "" {
		return VoiceServiceSpec{}, false, nil
	}
	return spec, true, nil
}

// VoiceServiceRecorded returns whether the installation root's tfvars record a
// decision about the voice hop at all: the key present with a block (the
// installation has the hop), or present as null (the owner has turned it off).
//
// It is not VoiceServiceOf with another name. That one answers whether a
// revision could be created from what the document holds, so a null block and a
// missing key are the same answer there; here they are opposite ones, and the
// difference is the one a run may not get wrong — only a document that records
// NOTHING may be given a block composed from the installation's own state
// (init.go's keepVoiceHop), because a null is the owner saying no, and a run
// that composed over it would put the service, its account and its public
// invoker binding back on every later `fugaro update` (SECURITY.md: the
// installation's own switch, which takes the service and its grant away).
func VoiceServiceRecorded(vars []byte) (bool, error) {
	if vars == nil {
		return false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	_, ok := doc["voice_service"]
	return ok, nil
}

// CarriedVoiceService returns vars with the voice_service entry the document
// prev carried, when vars carry none of their own — CarriedIntake's carry, on
// the third variable this side reads and the spec never holds.
//
// The block is taken exactly as it is found, and here that is the whole of
// it: unlike the intake's it carries no URL, so there is nothing in it a
// rebuild from this side would know better. It is also the one key of the three
// that nothing else writes: the block IS the hop's opt-in (VoiceServiceSpec),
// so this carry is not convergence but the opposite of it — an installation
// that never wrote one must come out of every run without one, and an
// installation that did must keep it. The consequence of dropping it is the
// sharper of the two: the block plans the hop's service, its account, its one
// role binding and its invoker grants away, and with them the only process that
// enforces the project's published voice policy, so a run that lost it would
// leave every paired phone refused until the owner wrote it back by hand.
//
// prev is that document — infra.Workdir.PrevVars, or the tfvars the workdir
// holds now, which is where the installation stage's own writes a few steps
// before prepare rewrites the file. An entry is carried forward present or
// absent: an installation that has the hop keeps it across an ordinary run's
// rewrite, and one that never had it is unchanged rather than given one.
//
// The key present as null is carried as null, and that is deliberate: null is
// the owner's own off switch, and dropping it here would hand keepVoiceHop a
// document that records nothing — which is what a machine that never held the
// document has — and let the hop be composed back onto an installation whose
// owner turned it off (VoiceServiceRecorded is the reader that tells the two
// apart).
func CarriedVoiceService(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no workdir held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// drops the hop: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := previous["voice_service"]
	if !ok {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc["voice_service"] = block
	return sortedJSON(doc)
}

// ProjectReviewJobsKey is the module's own variable name for the installation's
// review-only job variants, which is the key CarriedProjectReviewJobs reads and
// writes (InstallationSpec's field spells it in its own tag).
const ProjectReviewJobsKey = "project_review_jobs"

// CarriedProjectReviewJobs returns vars with the implicit default review jobs
// (ImplicitReviewJobName) that the document prev carries, when vars carry no job
// of that name.
//
// It is the carry for the review jobs the config cannot always compose. Whether
// the installation deploys one at all is the config's answer
// (implicitReviewVariant's gates), and one of those gates is a fact the config
// can lose AFTER the job is deployed: a second GitHub App ID recorded, or the
// last GitHub repository gone, leaves the config with no single App ID — and the
// job runs on one App's ID, so no reader composes it any more; the last
// Bitbucket repository gone is the same state for the Bitbucket job, whose gate
// is that a repository of its provider exists at all. The installation holds the
// job all the same, and a document rewritten without it is the plan of its
// destruction: RefuseProjectJobDeletes stops the whole update on that plan, with
// "restore the profile as it was" as the only remedy it can print — a profile an
// installation that declares none does not have. Every later run repeats it, so
// the installation stops converging for good. These jobs are deployed for nobody
// who asked for them (implicitReviewVariant), and they must never be the reason
// an installation that converges today stops converging — which is what each
// one's own gate says of the run that has not deployed it yet.
//
// The record is the installation's own tfvars, and each job is taken from it
// whole rather than recomposed: prepare, the writer this serves, holds the spec
// and not the config, and the spec's review set is exactly what cannot describe
// these jobs. Taken whole is also what makes the plan that follows a no-op — the
// job in the record is the job in the state, so nothing about it is proposed
// for a change.
//
// Three things it deliberately does not do. It never overwrites a job vars
// already carry: a config that composes the job again is the authority on it,
// and that run is what catches the job up with this release's image and
// environment. It carries no other review job: a profile's variant missing from
// the spec is a profile the config no longer declares, which is a fact OF the
// config, and the refusal RefuseProjectJobDeletes prints for it is that fact's
// remedy. And it carries only the jobs ImplicitReviewProviders names — the list
// the deployment composes them from — so a provider this release deploys no
// implicit job for has nothing carried for it either.
//
// prev is that document — infra.Workdir.PrevVars, or the tfvars the workdir
// holds now, which is where the installation stage's own deploy wrote the jobs a
// few steps before prepare rewrites the file.
func CarriedProjectReviewJobs(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no workdir held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// deletes the jobs: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	carried, _ := previous[ProjectReviewJobsKey].(map[string]any)
	implicit := map[string]any{}
	for _, provider := range ImplicitReviewProviders {
		if job, ok := carried[ImplicitReviewJobName(provider)]; ok && job != nil {
			implicit[ImplicitReviewJobName(provider)] = job
		}
	}
	if len(implicit) == 0 {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	jobs, _ := doc[ProjectReviewJobsKey].(map[string]any)
	added := false
	for _, name := range slices.Sorted(maps.Keys(implicit)) {
		if _, ok := jobs[name]; ok {
			continue // the spec composes the job itself: its own answer stands
		}
		if jobs == nil {
			jobs = make(map[string]any, len(implicit))
		}
		jobs[name] = implicit[name]
		added = true
	}
	if !added {
		return vars, nil
	}
	doc[ProjectReviewJobsKey] = jobs
	return sortedJSON(doc)
}

// ProjectJobsKey is the module's own variable name for the installation's
// shared project jobs, the key CarriedProjectJobs reads and writes
// (InstallationSpec's field spells it in its own tag).
const ProjectJobsKey = "project_jobs"

// ProjectJobProfilesOf is the profile names of the shared project jobs the
// tfvars document vars carries: what the installation holds, asked by the run
// that has to decide whether a profile the local config cannot compose is one
// whose job a carry may keep (internal/cli's heldDefaultProfiles).
//
// A job the document carries without a profile name is not a shared project
// job of a profile and is not named: nothing composes one, so no profile can be
// answered for by it.
func ProjectJobProfilesOf(vars []byte) (map[string]bool, error) {
	if vars == nil {
		return nil, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	jobs, _ := doc[ProjectJobsKey].(map[string]any)
	out := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		m, ok := job.(map[string]any)
		if !ok {
			continue
		}
		if profile, ok := m["profile"].(string); ok && profile != "" {
			out[profile] = true
		}
	}
	return out, nil
}

// CarriedProjectJobs returns vars with every shared job of the named profiles
// that the document prev carries and vars lack, taken whole from prev — the
// coding job of each (ProjectJobsKey) and every review-only variant of it
// (ProjectReviewJobsKey), by job name.
//
// It is the carry for the profile `fugaro update` writes itself (S5 of
// docs/shared-jobs-default), and it is the same carry as the one above for the
// same reason. The default profile of a provider the project's repositories use
// is a document the update PUT into the local config (R1), and the job it
// deploys is composed from a fact that config can lose afterwards: the one
// GitHub App ID its github repositories record (projectJobGitHubAppID). A
// project whose last GitHub repository moves to Bitbucket, or one that adds a
// repository on a second App, records no single ID any more — so no reader
// composes a job for the profile, while the installation holds the one it
// deployed, and the plan of every later run is that job's destruction:
// RefuseProjectJobDeletes stops the whole update on it, with "restore the
// profile as it was" as its only remedy — which is not a remedy for a profile
// the update wrote and the operator never touched. The jobs are kept, whole,
// exactly as the implicit review job is, so that a job the installation holds
// is never the reason an update that converges today stops converging.
//
// The review variants are carried with the coding job and never instead of it:
// ProjectReviewJobs derives one per (profile, provider, account) of the coding
// set, so a pruned coding job takes its own variants with it, and a document
// that holds the coding job alone is still a plan that destroys them.
//
// The caller names the profiles, and it names only the ones the config cannot
// compose now AND whose job the installation holds (internal/cli's
// heldDefaultProfiles). Nothing else is carried: a profile the config no longer
// declares, and one the installation never deployed a job for, is a fact of the
// config, and the refusal RefuseProjectJobDeletes prints for it is that fact's
// remedy — as is the refusal installOptions raises for a config that declares a
// github profile and records no App ID at all before any job is deployed.
func CarriedProjectJobs(vars, prev []byte, profiles []string) ([]byte, error) {
	if len(profiles) == 0 {
		return vars, nil // nothing to carry
	}
	want := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		want[p] = true
	}
	next, _, err := carriedProjectJobs(vars, prev, func(_ string, job map[string]any) bool {
		profile, _ := job["profile"].(string)
		return want[profile]
	})
	return next, err
}

// CarriedProjectJobAccounts returns vars with every shared job of the named
// (profile, provider, account) triples that the document prev carries and vars
// lack, taken whole from prev — the coding job of each (ProjectJobsKey) and its
// review-only variant (ProjectReviewJobsKey).
//
// It is the carry for the accounts this installation declares a KEY credential
// under while a profile of the local config lists them (MC22, D1669, D1670):
// the names ProjectJobs composes no job for at all, because every job of that
// axis exists to mount its account's Claude token and such a name's value lives
// under the credential's own derived secret instead (config.CredentialSecret),
// so the job would mount a container nothing writes a version to. The filter is
// this release's (ProjectKeyAccountJobs names what it drops), and the job an
// installation already holds is one an earlier release deployed: the deployer
// behind built a job for every value of the axis whose account had a
// project-wide container (projectAccountMount's own lookup), which is exactly
// the config that reaches here — a profile listing a key-typed name is written
// by hand, because `fugaro accounts add` refuses to put one in a profile's
// claude_accounts (accounts_add.go), and no command ever wrote one.
//
// Without this carry, that installation stops converging for good: the plan of
// every later run is the destruction of the job the state holds,
// RefuseProjectJobDeletes stops the whole update on that plan, and the remedy
// it prints — "restore the profile as it was" — is not one an operator can
// carry out here, because the name back in the profile's claude_accounts is
// what the filter drops the job for. It is the same failure the two carries
// above answer, and the same answer: the job is kept, whole, from the
// installation's own record, so the plan proposes no change to it
// (CLAUDE.md's HARD RULE: `fugaro update` is the whole upgrade, and a migration
// it needs ships in it).
//
// The triple is read off the record's own entry, which states it (ProjectJob's
// profile, provider and account), so no name is derived here and a job written
// by another release is carried as that release wrote it. One triple answers
// for both keys: a review variant of a NAMED account carries that account's own
// name (config.ReviewVariantAccount is the identity for every value but the
// empty one, and a key credential is never the profile's no-account job — its
// name is a credential's).
//
// What it does not do is the shared walk's rule: a job vars already carry is
// the spec's own answer and stays. And nothing else is carried — a triple the
// caller does not name is a name the config no longer declares, or a profile
// gone from it, which is a fact of the config, and the refusal
// RefuseProjectJobDeletes prints for it is that fact's remedy.
//
// prev is the installation's tfvars — infra.Workdir.PrevVars, or the tfvars the
// workdir holds now.
func CarriedProjectJobAccounts(vars, prev []byte, accounts []ProjectJobAccount) ([]byte, error) {
	if len(accounts) == 0 {
		return vars, nil // nothing to carry
	}
	want := make(map[ProjectJobAccount]bool, len(accounts))
	for _, a := range accounts {
		want[a] = true
	}
	next, _, err := carriedProjectJobs(vars, prev, func(_ string, job map[string]any) bool {
		profile, _ := job["profile"].(string)
		provider, _ := job["provider"].(string)
		account, _ := job["account"].(string)
		return want[ProjectJobAccount{Profile: profile, Provider: provider, Account: account}]
	})
	return next, err
}

// CarriedProjectJobNames returns vars with the named shared jobs that the
// document prev carries and vars lack, taken whole from prev — the coding job
// of each name (ProjectJobsKey) and its review-only variant
// (ProjectReviewJobsKey) — and the names it took, in order.
//
// It is the carry for the instances a retirement KEEPS because a run is on them
// or because whether one is could not be read (internal/cli's retireProjectJobs,
// issue #3686). Such an instance is left exactly as it is, and the job's name is
// exactly what the document this run composes does not carry — that is what
// makes the instance a retirement at all — so a state row left in place without
// the record's own entry beside it is a plan that destroys the job a run is on,
// and with it the service account and the grants that run's later calls reach
// through. Putting the record's entry back is what leaves the instance whole,
// and the name is the module's for_each key, so nothing is derived here: the job
// carried is the job the run that deployed it wrote (S10's own rule for the
// image beside it).
//
// The caller names only the instances the run keeps, and nothing else: a job
// the configuration no longer declares and no run is on is one this run
// retires, and carrying it back would put a job with nothing to manage it into
// the document, whose plan then creates it again.
//
// prev is the installation's tfvars as this machine held it before the run
// (infra.Workdir.PrevVars): a machine that has never planned this installation
// holds none, and a name it does not answer for is one the caller is told about
// rather than one silently carried as nothing.
func CarriedProjectJobNames(vars, prev []byte, names []string) ([]byte, []string, error) {
	if len(names) == 0 {
		return vars, nil, nil // nothing to carry
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	return carriedProjectJobs(vars, prev, func(name string, _ map[string]any) bool { return want[name] })
}

// carriedProjectJobs is the walk the three carries above share: every entry of
// the document prev that want accepts, and that vars lack, taken whole from
// prev — from the coding set and from the review-only variants, by job name —
// and the names it took, sorted.
//
// want reads a carried entry as the record states it, never as this release
// would compose it: what the carries answer for is a job an installation
// holds, and the record is the only thing that says which jobs those are. The
// name it is handed is the entry's key in the document, which is the module's
// for_each key and therefore the job's Cloud Run name.
func carriedProjectJobs(vars, prev []byte, want func(name string, job map[string]any) bool) ([]byte, []string, error) {
	if prev == nil {
		return vars, nil, nil // no workdir held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// deletes the job: fail rather than carry nothing forward.
		return nil, nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	var added []string
	for _, key := range []string{ProjectJobsKey, ProjectReviewJobsKey} {
		carried, _ := previous[key].(map[string]any)
		if len(carried) == 0 {
			continue
		}
		jobs, _ := doc[key].(map[string]any)
		for name, job := range carried {
			m, ok := job.(map[string]any)
			if !ok {
				continue
			}
			if !want(name, m) {
				continue
			}
			if _, ok := jobs[name]; ok {
				continue // the spec composes this job itself: its own answer stands
			}
			if jobs == nil {
				jobs = make(map[string]any, 1)
			}
			jobs[name] = job
			added = append(added, name)
		}
		if jobs != nil {
			doc[key] = jobs
		}
	}
	if len(added) == 0 {
		return vars, nil, nil
	}
	slices.Sort(added)
	added = slices.Compact(added)
	next, err := sortedJSON(doc)
	if err != nil {
		return nil, nil, err
	}
	return next, added, nil
}

// ManagedDeployerScopedKey is the module's own variable name for the deployer's
// scope, which is what creates the fence: the key these functions read and
// carry, and the one the installation's document is asked about.
const ManagedDeployerScopedKey = "managed_deployer_scoped"

// ManagedDeployerScopedRecorded is whether the installation's tfvars document
// vars records the deployer's scope: the key present and true.
//
// It is the installation's own record of the fence, and it is read where the
// question is what the INSTALLATION holds rather than what the project is now —
// `fugaro update --forget` refuses an installation that holds the fence
// (internal/cli's managedForgetRefusal), and the live creation fact alone does
// not answer that: the marks can come off the project (docs/gcp-setup.md, "What
// makes a project managed") while the fourteen stay live and this document goes
// on recording them, because the carry below never drops the key.
//
// nil is a workdir that holds no document and answers false; a document that
// cannot be read is an error, the same way CarriedManagedDeployerScoped treats
// one, because a tfvars this program cannot read is not evidence that the
// installation has no fence.
func ManagedDeployerScopedRecorded(vars []byte) (bool, error) {
	if vars == nil {
		return false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	scoped, _ := doc[ManagedDeployerScopedKey].(bool)
	return scoped, nil
}

// CarriedManagedDeployerScoped returns vars with the managed_deployer_scoped
// entry that the document prev carried, when vars carry none of their own.
//
// prev is the same pair of documents CarriedCredentialsService takes — the
// workdir's snapshot from before the run and the tfvars it holds now — and nil
// when neither holds any. vars carry a value of their own only when the caller
// decided it from the project's own creation fact (installRoot reads
// managed.IsManaged), which is the authority: this never overwrites one.
//
// The variable is what creates the managed deployer's seven scoped roles and
// their seven conditioned bindings, and every path that serializes the
// installation root's tfvars rebuilds them from a spec that does not carry it
// (the spec is built from the options, and only the fact read sets the field).
// Carried forward as it is found, present or absent, an installation that has
// them keeps them across any such rewrite — `--config-only`, the compute
// stage's read of the installation's outputs and `--print-vars`, whose
// document is a reading of the installation the owner has to be able to
// trust. (`fugaro update --forget` rewrites the same document too, and would
// otherwise delete all fourteen; it is refused an installation that records
// them before it gets here, so it no longer reaches this.) An installation that
// never used managed projects carries no key and is unchanged.
//
// What an entry found here means is the INSTALLATION's fence, not the project's
// mode, and that is the reading the rollback's refusal is built on
// (ManagedDeployerScopedRecorded): this carry puts the key back into every
// rewrite, so a project whose two marks have come off still has a document that
// records the fourteen, and the fourteen are still live in it. So is this entry
// one-way: the two marks come off by command (`fugaro managed unflag` removes
// the marker object and the project label), while nothing in this tree removes
// the key.
func CarriedManagedDeployerScoped(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no document held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a document
		// that drops the fence: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	scoped, ok := previous[ManagedDeployerScopedKey]
	if !ok || scoped == nil {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	if _, own := doc[ManagedDeployerScopedKey]; own {
		return vars, nil // the caller's own decision stands
	}
	doc[ManagedDeployerScopedKey] = scoped
	return sortedJSON(doc)
}

// OrchestratorKey is the installation root's variable for the resident
// orchestrator's block (mobile plan task P5.1b; the mobile orchestrator design's
// section 4.3) — the account and its bindings — and it is the key
// CarriedOrchestrator reads and keeps.
const OrchestratorKey = "orchestrator"

// CarriedOrchestrator returns vars with the orchestrator block the document prev
// carried: prepare's fifth carry, beside the credentials service's, the intake's,
// the implicit review job's and the deployer's scope.
//
// The block is the design's section 4.3 table and nothing else — the account
// fugaro-orch, the launcher's project role, the runs bucket's conditioned write
// and its unconditioned read, the token-minter role on the budget signer ACCOUNT
// and the job runner's role on each of the installation's jobs — and the
// installation holds it as STATE. The ONE writer is `fugaro update`
// (internal/cli's ensureOrchestratorBlock, in the credentials-service stage; through infra.Orchestrator and SetOrchestrator), and
// it writes the block only where the installation holds none: from the apply
// that creates the account onwards every path that re-serializes the root's
// tfvars rebuilds the document from a spec that does not carry the key, so
// without this carry the next `fugaro update` hands Terraform a document with no
// orchestrator block at all, and the plan is the account's and its bindings'
// removal — a google_service_account, which D1551 classifies as data-bearing
// ("an identity whose keys and grants are lost with it") and the removal rule
// therefore REFUSES (internal/infra/tf's RefusesRemovalType). An installation
// that opted in stops converging at its next update, and every update after it
// repeats the same refusal.
//
// The block is taken WHOLE and not recomposed: it is the installation's own
// record of what it opted into, and a run that rebuilt it from a spec would be a
// run re-deciding the account's grants (the service and the tick are force-new
// names, and the pairing container carries prevent_destroy). It is carried as it
// is found, present or absent, so an installation that never opted in is
// unchanged rather than given a block by a run that decided nothing.
//
// The block from prev WINS over one vars already carry, and that is not a
// detail: prev is each document of the pair below in turn, so the LAST one read
// is the tfvars the workdir holds NOW — where the run's own writers put what
// they write, the opt-in's block and the image the credentials-service stage
// re-pinned inside it — and a carry that kept the snapshot's answer would let
// the older document, the one from before the run, revert both. CarriedIntake
// and carriedCredentialsService take the previous document's block the same way,
// for the same reason (init.go's prepare says which two documents they are, in
// which order).
//
// prev is the same pair of documents the carries beside it take —
// infra.Workdir.PrevVars, the snapshot from before the run, and the tfvars the
// workdir holds now.
func CarriedOrchestrator(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no document held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// deletes the account: fail rather than carry nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := previous[OrchestratorKey]
	if !ok || block == nil {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc[OrchestratorKey] = block
	return sortedJSON(doc)
}

// OrchestratorRecorded is whether the installation root's tfvars document vars
// carries the resident orchestrator's block: the key present with a block (the
// installation has opted in), or absent or null (it has not).
//
// It is the reader of the same switch the module's own root reads — var.
// orchestrator is null by default and orchestrator.tf creates NOTHING when it
// is (its local.orchestrator_on) — and it exists because the block's presence
// is a fact about the INSTALLATION and not about a run's spec: the block is
// written once (CarriedOrchestrator names the writer), so everything the account
// is — the account itself, its five bindings, the caller class credentials.tf
// renders and the invoker grant orchestrator.tf adds — exists on exactly the
// installations this answers true for. The writer asks it before it writes, and
// the credentials-service stage asks it before it re-pins an image: an
// installation that holds no block is one no run recomposes one for.
//
// A key present as null is NOT recorded. VoiceServiceRecorded answers the other
// way for the same shape, because the question there is whether a run may
// compose a block over the owner's own no; here the question is whether the
// account exists, and the module creates it only when the variable is not null.
//
// nil is a workdir that holds no document and answers false; a document that
// cannot be read is an error, the same way CarriedOrchestrator treats one,
// because a tfvars this program cannot read is not evidence that the
// installation has not opted in.
func OrchestratorRecorded(vars []byte) (bool, error) {
	if vars == nil {
		return false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := doc[OrchestratorKey]
	return ok && block != nil, nil
}

// LicenceKey is the installation root's variable for the licence renewal job's
// block (plan K4.2 of docs/licence-key-v2) — the account, the job, the empty
// container and its two bucket conditions — and it is the key CarriedLicence
// reads and keeps.
const LicenceKey = "licence"

// CarriedLicence returns vars with the licence renewal job's block the document
// prev carried, when vars carry none of their own: the last of the keys this
// spec does not always carry, beside the credentials service's, the intake's,
// the implicit review jobs', the shared jobs' and their accounts', the
// deployer's scope and the orchestrator's.
//
// The block is not the installation's own composition — internal/cli's
// licenceJob writes it, and every value in it is recomputed from the local
// config on the run that writes it — but its PRESENCE is the installation's:
// the module creates the account, the job, the schedule and the empty container
// the renewal secret is written into the moment a document names the job, and
// the container carries prevent_destroy (licence.tf), so a document that loses
// the key while the installation holds them is a plan Terraform refuses rather
// than applies, on every later run, for an installation whose coordinator's own
// job is no longer deployed — a history image that is not tagged, a repository
// the config has since dropped, an App ID it no longer records
// (internal/cli's coordinatorJob recomputes that readiness on every run, and
// licenceJob writes the block only where it holds).
//
// What this carry does NOT do is change what the module plans in that state:
// licence.tf reads the block TOGETHER with the coordinator's own readiness
// (local.licence_on), so a block on an installation whose job is not deployed
// plans nothing either way, and the modules above leave the resources' fate to
// that second conjunct. What the carry settles is only that the document which
// is planned is never the thing that forgot the job — the readiness returning
// true is then a no-op for the resources rather than a re-creation, and a
// rewrite of this root by a path that decides nothing about the licence (the
// compute stage's read of the installation's outputs, --config-only,
// --print-vars, a rollback, which is what the carries above exist for) no
// longer drops the key. Whether the readiness SHOULD be able to take back a job
// the installation already holds is a decision about the module's switch, and
// this carry does not make it.
func CarriedLicence(vars, prev []byte) ([]byte, error) {
	if prev == nil {
		return vars, nil // no document held one: nothing to carry
	}
	previous, err := varsDoc(prev)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// drops the installation's record of the job: fail rather than carry
		// nothing forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := previous[LicenceKey]
	if !ok || block == nil {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	if _, own := doc[LicenceKey]; own {
		return vars, nil // the caller's own answer stands
	}
	doc[LicenceKey] = block
	return sortedJSON(doc)
}

// BudgetKey is the installation root's variable for the project's budget alert
// (Budget above), and it is the key CarriedBudget keeps.
const BudgetKey = "budget"

// CarriedBudget returns vars with the alert the installation already holds,
// when vars carry none of their own: prepare's carry for the one key whose
// source is not a document but the cloud.
//
// The figures are read by the caller, and they come from where a machine that
// never ran the apply can read them (R5 and R7 of
// docs/managed-flag-ergonomics): the amount and the currency from the project's
// creation fact, which is the record a rewrite by `fugaro managed flag` moves,
// and the billing account from the alert's own resource in the installation
// root's state, which is the only place an account survives a work directory
// that was cleaned, or a console on another host. Neither comes from the local
// work directory.
//
// nil carried is a run that answered the budget itself — its document has the
// key and it stands (D2215: a default never overwrites a recorded amount) — and
// an installation that holds no alert; a document that already carries the key
// is returned untouched, so the flags always replace what was carried.
func CarriedBudget(vars []byte, carried *Budget) ([]byte, error) {
	if carried == nil {
		return vars, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		// A tfvars that cannot be read is not a reason to write a plan that
		// deletes the installation's alert: fail rather than carry nothing
		// forward.
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	if own, ok := doc[BudgetKey]; ok && own != nil {
		return vars, nil // the caller's own answer stands
	}
	raw, err := json.Marshal(carried)
	if err != nil {
		return nil, err
	}
	return setVarsKey(vars, BudgetKey, raw)
}

// budgetResourceType is the resource type of the installation module's alert
// (deploy/terraform/gcp/modules/installation/budget.tf), the one resource a raw
// state document has to name for budgetBillingAccountOf to read an account out
// of it.
const budgetResourceType = "google_billing_budget"

// rawStateFormatVersion is the version a raw state object this build reads is
// written with. Terraform 1.x writes 4; a document of another version could
// have moved the fields read below, and reading it anyway would be a guess
// about what the installation holds.
const rawStateFormatVersion = 4

// budgetBillingAccountOf is the billing account the alert's own resource in a
// Terraform state object records: the one member of a state this carry reads,
// and the only copy of the account a machine that never ran the apply can
// reach (R7).
//
// A state that holds no budget resource answers found=false and no error — an
// installation with no alert has none, which is the state of every installation
// before the alert's first apply — and one that holds the resource with no
// account, or with an account that is not a string, is an error rather than a
// missing alert: that is a state this build cannot read as the thing it is, and
// carrying nothing from it would plan the alert's deletion.
func budgetBillingAccountOf(state []byte) (account string, found bool, err error) {
	var st struct {
		Version   int `json:"version"`
		Resources []struct {
			Mode      string `json:"mode"`
			Type      string `json:"type"`
			Instances []struct {
				Attributes map[string]json.RawMessage `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(state, &st); err != nil {
		return "", false, fmt.Errorf("reading a Terraform state document: %w", err)
	}
	if st.Version != rawStateFormatVersion {
		return "", false, fmt.Errorf("the state document is format version %d, and this build reads only %d", st.Version, rawStateFormatVersion)
	}
	for _, r := range st.Resources {
		if r.Type != budgetResourceType || (r.Mode != "" && r.Mode != "managed") {
			continue
		}
		for _, inst := range r.Instances {
			raw, ok := inst.Attributes["billing_account"]
			if !ok {
				return "", false, fmt.Errorf("the state's %s records no billing_account", budgetResourceType)
			}
			var s string
			if err := json.Unmarshal(raw, &s); err != nil || s == "" {
				return "", false, fmt.Errorf("the state's %s records a billing_account that is not an account", budgetResourceType)
			}
			return s, true, nil
		}
	}
	return "", false, nil
}

// stringList is a decoded JSON array as the list of strings it holds, empty for
// anything else: a member that is not a string is dropped rather than read as
// one, because the block a caller asks about is a document this program either
// wrote (where every member is a string) or one it refuses to act on at all.
func stringList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// varsDoc decodes an installation tfvars document, keeping numbers as
// json.Number so that encoding it again writes the same digits.
func varsDoc(vars []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(vars))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// setVarsKey returns the tfvars document vars with key set to the raw JSON
// value raw, every other entry unchanged, in sortedJSON's own bytes.
func setVarsKey(vars []byte, key string, raw json.RawMessage) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, err
	}
	var val any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&val); err != nil {
		return nil, err
	}
	doc[key] = val
	return sortedJSON(doc)
}

// InstallationNames are the installation's singleton names.
type InstallationNames struct {
	LegacyRegistry            string              `json:"legacy_registry"`
	BaseRegistry              string              `json:"base_registry"`
	GhcrRegistry              string              `json:"ghcr_registry"`
	SchedulerServiceAccountID string              `json:"scheduler_service_account_id"`
	RoleIDs                   InstallationRoleIDs `json:"role_ids"`
	Log                       LogNames            `json:"log"`
}

// InstallationRoleIDs are the custom roles' IDs (not their full names).
type InstallationRoleIDs struct {
	Launcher       string `json:"launcher"`
	JobRunner      string `json:"job_runner"`
	BuildSubmitter string `json:"build_submitter"`
	TagMover       string `json:"tag_mover"`
}

// LogNames are the log bucket, view, sink and exclusion that keep Fugaro's
// job logs apart.
type LogNames struct {
	Bucket    string `json:"bucket"`
	View      string `json:"view"`
	Sink      string `json:"sink"`
	Exclusion string `json:"exclusion"`
}

// BucketLifecycle is the runs bucket's lifecycle, in days.
type BucketLifecycle struct {
	RunsDays            int `json:"runs_days"`
	CacheCustomTimeDays int `json:"cache_custom_time_days"`
	CacheAgeDays        int `json:"cache_age_days"`
	// ReportsDays is the stored reports' own rule (R12's fifth, E3): reports/
	// is deleted this many days after an object is created, which is 18
	// months. It is written for every installation rather than left to the
	// module's default, so the member and the rule it is about are one value
	// in the plan a review reads.
	ReportsDays int `json:"reports_days"`
}

// RegistryCleanup switches Artifact Registry cleanup, and its dry run.
type RegistryCleanup struct {
	Enabled bool `json:"enabled"`
	DryRun  bool `json:"dry_run"`
}

// Installation is the spec of the installation lc describes.
func Installation(lc *localcfg.Config, o InstallOptions) (InstallationSpec, error) {
	bucket, err := bucketName(lc)
	if err != nil {
		return InstallationSpec{}, userErr("%v", err)
	}
	if !strings.HasPrefix(bucket, "fugaro-runs-") {
		return InstallationSpec{}, userErr("the runs bucket %s must be named fugaro-runs-…", bucket)
	}
	// bucket came from bucket_url when the local config sets one; localcfg
	// only charset-checks runs_bucket, so an invalid one reaches here
	// unvalidated. gcp.LauncherBucketCondition panics rather than widen on
	// a bad bucket name (H2's CEL expression must be byte-exact), so this
	// must refuse cleanly before calling it.
	if !gcp.ValidBucketName(bucket) {
		return InstallationSpec{}, userErr("the runs bucket %q has a byte outside a-z, 0-9, '.', '_' and '-'", bucket)
	}
	s := InstallationSpec{
		Project:       lc.GCPProject,
		FugaroProject: lc.Name,
		Region:        lc.Region,
		RunsBucket:    bucket,
		StateBucket:   StateBucketName(o.StateBucket, lc),
		Names: InstallationNames{
			LegacyRegistry:            LegacyRegistry,
			BaseRegistry:              BaseRegistry,
			GhcrRegistry:              GhcrRegistry,
			SchedulerServiceAccountID: SchedulerServiceAccountID,
			RoleIDs:                   InstallationRoleIDs{Launcher: RoleLauncher, JobRunner: RoleJobRunner, BuildSubmitter: RoleBuildSubmitter, TagMover: RoleTagMover},
			Log:                       LogNames{Bucket: LogBucket, View: LogView, Sink: LogSink, Exclusion: LogExclusion},
		},
		BucketLifecycle: BucketLifecycle{RunsDays: runsDays, CacheCustomTimeDays: cacheCustomTimeDays, CacheAgeDays: cacheAgeDays, ReportsDays: reportsDays},
		EnableVertex:    o.EnableVertex || lc.UsesVertex() || ChainsNameVertex(lc),
		ManageAPIs:      !o.SkipAPIs,
		Launchers:       members(o.Launchers, lc.Terraform.Operators),
		Operators:       members(o.Operators, lc.Terraform.Admins),
		Viewers:         members(o.Viewers, nil),
		LauncherBucketCondition: Condition{
			Title:      gcp.LauncherBucketConditionTitle,
			Expression: gcp.LauncherBucketCondition(bucket),
		},
		Budget:               o.Budget,
		AdoptLegacyRegistry:  o.AdoptLegacyRegistry,
		LogBucketDescription: LogBucketDescription,
		// The mapping's answer for this region: the region itself where Cloud
		// Logging offers a bucket there, else the place's multi-region, else
		// global with the reason stated (D3082). It is what a log bucket
		// CREATED from now on is in; discovery replaces it with where an
		// installation's own bucket already is.
		LogBucketLocation: place.For(lc.Region).LogBucket.Where,
		ProjectSecrets:    projectSecretContainers(lc),
	}
	// The shared job's Vertex GRANT is the API's own fact minus the modes that
	// refuse a Vertex run outright (VertexGrantForJob): a job's account is
	// planned no aiplatform permission on an enforcing project, where no run in
	// flight can be using one, and keeps it on an `observe` one until release 2
	// (X8). The review jobs below keep the whole fact: they are planned no
	// Vertex grant whatever this argument says (reviewVariant's own ruling), so
	// it reaches only their environment, where the two Vertex names are what an
	// `agent.auth: vertex` agent refuses to start without — and a variant that
	// cannot start is not a narrower variant.
	jobs, err := projectJobs(lc, bucket, s.EnableVertex && VertexGrantForJob(lc.BudgetMode()), o.CarriedJobProfiles)
	if err != nil {
		return InstallationSpec{}, err
	}
	s.ProjectJobs = jobs
	// And the accounts that filter drops, named for the carry that keeps what
	// the release behind deployed for them: a name the config declares a key
	// credential under must not reach a job of this composition (D1670), and a
	// job the installation already holds must not be planned away either (the
	// field's own comment).
	s.CarriedProjectJobAccounts = ProjectKeyAccountJobs(lc, o.CarriedJobProfiles)
	review, err := projectReviewJobs(lc, bucket, s.EnableVertex, o.CarriedJobProfiles)
	if err != nil {
		return InstallationSpec{}, err
	}
	s.ProjectReviewJobs = review
	s.ManagedDeployerScoped = o.ManagedDeployerScoped
	cc, err := CatalogCheck(lc, bucket, jobPaused(o.CatalogCheckPaused, false), s.ProjectSecrets)
	if err != nil {
		return InstallationSpec{}, err
	}
	s.CatalogCheck = cc
	rj, err := ReportsJob(lc, bucket, jobPaused(o.ReportsPaused, true))
	if err != nil {
		return InstallationSpec{}, err
	}
	s.Reports = rj
	// CredentialsService is deliberately NOT set from the options: the block
	// is the deploy's own (init_stages.go's ensureCredentialsService), and the
	// deploy never comes through here — it read-modify-writes the one key of the document the
	// installation stage wrote (infra.SetCredentialsService), because a
	// document rebuilt from a spec would drop the discovery's project_secrets
	// and plan their destruction.
	if o.NoLogIsolation {
		s.LogIsolation = new(false)
	}
	if o.BudgetBackend {
		h, err := History(lc)
		if err != nil {
			return InstallationSpec{}, err
		}
		s.EnableBudget, s.History = true, &h
		if o.Coordinator {
			c, err := Coordinator(lc)
			if err != nil {
				return InstallationSpec{}, err
			}
			s.Coordinator = &c
			l, err := Licence(lc, bucket)
			if err != nil {
				return InstallationSpec{}, err
			}
			s.Licence = &l
		}
	}
	// The front door's opt-in is refused rather than resolved when the
	// coordinator's is missing, and refused here — before a tfvars exists —
	// rather than left to the module's own validation, which says the same
	// thing in the plan: a flag that planned nothing would be read by its
	// owner as a front door that exists.
	if o.MCPServer {
		if !o.BudgetBackend || !o.Coordinator {
			return InstallationSpec{}, userErr("--mcp-server needs the coordinator's own opt-in: the front door is the coordinator's own service and serves the state the coordinator writes, and the coordinator itself is planned only on an installation that has the budget backend, which --firebase-project declares; so run fugaro update --firebase-project <gcp-project-id> --coordinator --mcp-server")
		}
		m, err := MCPServer(lc)
		if err != nil {
			return InstallationSpec{}, err
		}
		s.MCPServer = &m
	}
	if e := firstOf(o.AlertEmail, lc.Terraform.AlertEmail); e != "" {
		s.AlertEmail = &e
	}
	switch o.RegistryCleanup {
	case "", "dry-run":
		s.RegistryCleanup = RegistryCleanup{Enabled: true, DryRun: true}
	case "on":
		s.RegistryCleanup = RegistryCleanup{Enabled: true}
	case "off":
		// No policy, and a dry run: the installation outputs dry_run, which
		// every repository registry copies, so off must not turn deletion
		// on there.
		s.RegistryCleanup = RegistryCleanup{DryRun: true}
	default:
		return InstallationSpec{}, userErr("--registry-cleanup %q must be dry-run, on or off", o.RegistryCleanup)
	}
	return s, nil
}

// projectSecretContainers are the project-wide secret containers the local
// config makes eligible (design docs/project-secrets/spec.md §4): every
// project_secrets entry with a non-empty allow_data_to, every provider's own
// key unconditionally (§3a: a provider's key is eligible for project storage
// whether or not its allow_data_to list exists, and an absent list is a
// valid "every repository" state, not a reason to leave the container out)
// and every named Claude account with a non-empty allow_data_to (D1263). A
// reserved name with no project_secrets entry has no container at all.
//
// A workflow_secrets entry is no longer one of them (IS6, D3324): its value
// lives in the installation's own fugaro_p_<name> copy from the update that
// copied it (IS5/D3325), and the fetch reads it there, so a second container
// under the project-secret spelling is one no job mounts and no run asks for.
// Nothing here reads the project record — the installation root is applied
// before any repository is — but a name whose copy the record does not mark
// yet keeps its live container anyway: DiscoverInstallation unions the live
// containers in before the map reaches Terraform (N3), and that is what keeps
// a skipped name's mount, grants and copy standing until a later update copies
// it.
//
// This is only the config half: DiscoverInstallation unions the live
// containers in before the map reaches Terraform (N3).
func projectSecretContainers(lc *localcfg.Config) map[string]string {
	out := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(lc.ProjectSecrets)) {
		if len(lc.ProjectSecrets[name].AllowDataTo) > 0 {
			out[name] = gcp.ProjectSecretID(lc.Name, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(lc.Providers)) {
		if secret := lc.Providers[name].Secret; secret != "" {
			out[secret] = gcp.ProjectSecretID(lc.Name, secret)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(lc.ClaudeAccounts)) {
		if len(lc.ClaudeAccounts[name].AllowDataTo) > 0 {
			s := config.ClaudeAccountSecret(name)
			out[s] = gcp.ProjectSecretID(lc.Name, s)
		}
	}
	return out
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// StateBucketName is the installation's state bucket as InstallSpec resolves it:
// the flag's own value, else the local config's terraform.state_bucket — which
// is where a managed project's dedicated bucket is recorded — else the project's
// default name. A caller that holds no spec reads the bucket by this: the
// update's note about the runs in flight asks whether a repository's root is
// still in that state, and it asks it before the stage that builds the spec.
func StateBucketName(flag string, lc *localcfg.Config) string {
	if lc == nil {
		return flag
	}
	return firstOf(flag, lc.Terraform.StateBucket, "fugaro-tfstate-"+lc.GCPProject)
}

// members is flag, else the local config's, never nil (an empty list, not
// null, in the tfvars).
func members(flag, local []string) []string {
	m := flag
	if m == nil {
		m = local
	}
	return append([]string{}, m...)
}

// launchersWithOperators is what the roots grant launchers' access to:
// the launchers and the operators, each once, sorted. Operators get
// everything launchers get (they run, watch and cancel runs too), and the
// specs and the local config keep the two lists as given.
func launchersWithOperators(launchers, operators []string) []string {
	out := append(members(launchers, nil), operators...)
	slices.Sort(out)
	return slices.Compact(out)
}

// repoVars is the repository root's tfvars.
type repoVars struct {
	Project       string           `json:"project"`
	FugaroProject string           `json:"fugaro_project"`
	Region        string           `json:"region"`
	Installation  RepoInstallation `json:"installation"`
	GitHubAppID   *string          `json:"github_app_id"`
	Repo          RepoSpec         `json:"repo"`
}

// RepoVars is the repository root's terraform.tfvars.json for spec.
func RepoVars(spec RepoSpec) ([]byte, error) {
	v := repoVars{Project: spec.GCPProject, FugaroProject: spec.Installation.ProjectName, Region: spec.Region, Installation: spec.Installation, Repo: spec}
	if spec.GitHubAppID != "" {
		id := spec.GitHubAppID
		v.GitHubAppID = &id
	}
	v.Installation.Launchers = launchersWithOperators(v.Installation.Launchers, v.Installation.Operators)
	v.Installation.Operators = members(v.Installation.Operators, nil)
	return sortedJSON(v)
}

// InstallationVars is the installation root's terraform.tfvars.json.
func InstallationVars(spec InstallationSpec) ([]byte, error) {
	spec.Launchers = launchersWithOperators(spec.Launchers, spec.Operators)
	spec.Operators = members(spec.Operators, nil)
	// The launcher class is the same merged list, read as addresses: the
	// invoker grants below the root are made from the members, the two
	// services' class check from these, and one derivation is what keeps them
	// the same people (launchers.go, F31/F36).
	spec.LauncherAccounts = launcherAccounts(spec.Launchers)
	spec.Viewers = sortedUnique(spec.Viewers)
	return sortedJSON(spec)
}

// sortedJSON encodes v with every object's keys sorted, indented, and with
// a final newline, so equal specs give equal bytes and diffs stay small.
func sortedJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
