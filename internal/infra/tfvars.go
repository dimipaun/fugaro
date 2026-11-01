package infra

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
// adopted bucket's lifecycle doesn't change.
const (
	runsDays            = 90
	cacheCustomTimeDays = 30
	cacheAgeDays        = 180
)

// InstallOptions are the installation's inputs that come from flags.
type InstallOptions struct {
	// StateBucket defaults to the local config's, else fugaro-tfstate-<project>.
	StateBucket string
	// EnableVertex enables the Vertex AI API; so does any repository the
	// local config records as using it.
	EnableVertex bool
	// SkipAPIs leaves the project's APIs to be managed elsewhere.
	SkipAPIs bool
	// Launchers and Operators are IAM members; nil means the local config's.
	Launchers, Operators []string
	Budget               *Budget
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
	// with --create-project --managed, and every later run against one,
	// read from the project's creation fact (init's installRoot) — so the
	// key follows the installation and not the run. What it does not settle
	// is whether the fence works: Check 68 has not been run
	// (deployer.tf's header, docs/design/managed-deployer-fence.md).
	ManagedDeployerScoped bool
	// Coordinator is the owner's opt-in to the coordinator's aggregation job
	// (P5, D194): with BudgetBackend, the installation declares the
	// coordinator's account, its read of the runs bucket and, once it can,
	// the job and its schedule. It is off unless fugaro update --coordinator
	// or a local config that records the opt-in sets it, so an installation
	// that never asked plans exactly what it planned before the job existed.
	Coordinator bool
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
	// LauncherBucketCondition limits the launchers' write grant on the runs
	// bucket to runs/ and tokens/ (gcp.LauncherBucketCondition).
	LauncherBucketCondition Condition       `json:"launcher_bucket_condition"`
	Budget                  *Budget         `json:"budget"`
	AlertEmail              *string         `json:"alert_email"`
	RegistryCleanup         RegistryCleanup `json:"registry_cleanup"`
	AdoptLegacyRegistry     bool            `json:"adopt_legacy_registry"`
	// LogBucketDescription is the log bucket's ownership mark.
	LogBucketDescription string `json:"log_bucket_description"`
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
	// ProjectSecrets are the installation module's project-wide secret
	// containers (logical name -> gcp.ProjectSecretID), which fugaro update
	// creates or adopts; their values are never set by Terraform (design
	// docs/design/project-secrets.md §4). Installation() fills it from the
	// local config's eligibility; DiscoverInstallation then unions in every
	// correctly-labelled container that already exists, so a config edit
	// can never shrink the module's for_each (N3).
	ProjectSecrets map[string]string `json:"project_secrets,omitempty"`
	// ProjectJobs are the installation's shared project jobs (design
	// project-centric §4.1): one Cloud Run job and one job service account
	// per (profile, provider, account), by job name. The key is left out of
	// an installation whose local config names no profile, so the plan of
	// one that had none is unchanged (infra.ProjectJobs).
	ProjectJobs map[string]ProjectJob `json:"project_jobs,omitempty"`
	// ManagedDeployerScoped is the module's managed_deployer_scoped: left out
	// unless set, so an installation that does not opt in writes the same
	// tfvars as before the variable existed and plans without the seven
	// per-service deployer roles and their seven conditioned bindings
	// (InstallOptions.ManagedDeployerScoped).
	ManagedDeployerScoped bool `json:"managed_deployer_scoped,omitempty"`
	// CredentialsService is the installation's credentials service (D146,
	// D147), left out until an owner runs init's credentials stage (C1.20) —
	// which is the only writer, and the only thing that can set it. Left out
	// rather than written as null, so an installation that has not opted in
	// writes exactly the tfvars it wrote before the variable existed, and the
	// module creates nothing for an absent one.
	CredentialsService *CredentialsServiceSpec `json:"credentials_service,omitempty"`
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

// The credentials service's singletons. The account is what reads the bundle,
// and nothing else is granted actAs on it (modules/installation/credentials.tf);
// the Cloud Run service's name is the name every job's invoker grant and the
// repository roots' CredentialsService output carry.
const (
	CredentialsAccountID = "fugaro-credentials"
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
// cannot be configured at all — the stage's marker gate (requireCredsRules)
// reads that same database, and a service with no database in its environment
// refuses to bind a port. Both halves are refused here by name rather than
// written empty, which is what the revision would refuse by.
func Credentials(lc *localcfg.Config, image string) (CredentialsServiceSpec, error) {
	if !credsImageRE.MatchString(image) {
		return CredentialsServiceSpec{}, userErr("the credentials service's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits): a tag is mutable, and this service reads every model credential the installation holds", image)
	}
	b := lc.Budget
	if b == nil || b.FirebaseProject == "" || b.RTDBURL == "" {
		return CredentialsServiceSpec{}, userErr("the credentials service reads the installation's Firebase database, and project %s's config names no Firebase project and database (budget.firebase_project and budget.rtdb_url): run 'fugaro update --firebase-project <gcp-project-id>' first, which writes both from the Firebase root's outputs", lc.Name)
	}
	if err := rtdb.ValidateURL(b.RTDBURL, true); err != nil {
		return CredentialsServiceSpec{}, userErr("budget.rtdb_url is not a Firebase Realtime Database URL: it must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app, with no credentials, path, query or fragment (a loopback address, which is the emulator, is admitted here). Run 'fugaro update --firebase-project <gcp-project-id>' to write it from the Firebase root's outputs")
	}
	jobs, err := CredentialsJobAccounts(lc)
	if err != nil {
		return CredentialsServiceSpec{}, err
	}
	return CredentialsServiceSpec{
		AccountID:       CredentialsAccountID,
		Service:         CredentialsServiceName,
		Image:           image,
		BundleSecret:    gcp.ProjectSecretID(lc.Name, CredentialsBundleName),
		FirebaseProject: b.FirebaseProject,
		RTDBURL:         b.RTDBURL,
		JobAccounts:     jobs,
	}, nil
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
	slices.SortFunc(out, func(a, b CredentialsJob) int {
		if c := strings.Compare(a.Provider, b.Provider); c != 0 {
			return c
		}
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return strings.Compare(a.Workflow, b.Workflow)
	})
	return out, nil
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
// all (the installation has not opted in, D147) or one with no image.
//
// It is the read an ordinary `fugaro update` makes to learn that this
// installation runs the service: the credentials stage is registered only
// under --provision-credentials and every other run rewrites these tfvars from
// a spec that never carries the key (init.go's prepare), so the document is
// the only thing in the run that knows. An entry with no image answers false
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
// document, because the tfvars that are on disk when the credentials stage
// runs are not what InstallationVars(spec) would produce: the run that wrote
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
// installation that has opted in (the bug C1.20's review found).
//
// The variable is the credentials stage's alone (C1.20), and every ordinary
// run of init rewrites the tfvars from its spec — which never sets the key.
// Carried forward exactly as it was, present or absent, an installation that
// has opted in keeps the service across an ordinary run's rewrite; without
// that, the next plain `fugaro update` would plan the service, its account and
// its grant away (a plan the bundle container's prevent_destroy refuses,
// so the run fails rather than applies anything). An installation that never
// opted in carries no key and is unchanged, and the runbook's opt-out — the
// owner deleting the key by hand — is respected for the same reason: the
// entry is taken as it is found.
func CarriedCredentialsService(vars, prev []byte) ([]byte, error) {
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
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	doc["credentials_service"] = svc
	return sortedJSON(doc)
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
// records the fourteen, and the fourteen are still live in it. The creation fact
// is one-way — nothing in this tree removes the marker object or the project
// label — and so is this entry.
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
		StateBucket:   firstOf(o.StateBucket, lc.Terraform.StateBucket, "fugaro-tfstate-"+lc.GCPProject),
		Names: InstallationNames{
			LegacyRegistry:            LegacyRegistry,
			BaseRegistry:              BaseRegistry,
			GhcrRegistry:              GhcrRegistry,
			SchedulerServiceAccountID: SchedulerServiceAccountID,
			RoleIDs:                   InstallationRoleIDs{Launcher: RoleLauncher, JobRunner: RoleJobRunner, BuildSubmitter: RoleBuildSubmitter, TagMover: RoleTagMover},
			Log:                       LogNames{Bucket: LogBucket, View: LogView, Sink: LogSink, Exclusion: LogExclusion},
		},
		BucketLifecycle: BucketLifecycle{RunsDays: runsDays, CacheCustomTimeDays: cacheCustomTimeDays, CacheAgeDays: cacheAgeDays},
		EnableVertex:    o.EnableVertex || lc.UsesVertex(),
		ManageAPIs:      !o.SkipAPIs,
		Launchers:       members(o.Launchers, lc.Terraform.Launchers),
		Operators:       members(o.Operators, lc.Terraform.Operators),
		LauncherBucketCondition: Condition{
			Title:      gcp.LauncherBucketConditionTitle,
			Expression: gcp.LauncherBucketCondition(bucket),
		},
		Budget:               o.Budget,
		AdoptLegacyRegistry:  o.AdoptLegacyRegistry,
		LogBucketDescription: LogBucketDescription,
		ProjectSecrets:       projectSecretContainers(lc),
	}
	jobs, err := ProjectJobs(lc, bucket, s.EnableVertex)
	if err != nil {
		return InstallationSpec{}, err
	}
	s.ProjectJobs = jobs
	s.ManagedDeployerScoped = o.ManagedDeployerScoped
	// CredentialsService is deliberately NOT set from the options: the opt-in
	// is the credentials stage's alone (C1.20), and that stage never comes
	// through here — it read-modify-writes the one key of the document the
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
		}
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
// config makes eligible (design docs/design/project-secrets.md §4): every
// project_secrets entry with a non-empty allow_data_to, every provider's own
// key unconditionally (§3a: a provider's key is eligible for project storage
// whether or not its allow_data_to list exists, and an absent list is a
// valid "every repository" state, not a reason to leave the container out),
// and every named Claude account with a non-empty allow_data_to. A reserved
// name with no project_secrets entry has no container at all.
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
