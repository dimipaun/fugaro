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
	// history job and its sweep Scheduler job. It is set by --firebase and
	// by a project config that records a Firebase project.
	BudgetBackend bool
	// ManagedDeployerScoped opts the installation in to the managed
	// deployer's scoped role and its conditioned binding (the module's
	// managed_deployer_scoped). Nothing sets it while internal/cli's
	// managedDeployerShipped is false (0.8.6): the binding's fmgd- fence is
	// not an IAM condition GCP accepts yet (deployer.tf's header).
	ManagedDeployerScoped bool
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
	// out of an installation without a budget backend.
	EnableBudget bool         `json:"enable_budget,omitempty"`
	History      *HistorySpec `json:"history,omitempty"`
	// ProjectSecrets are the installation module's project-wide secret
	// containers (logical name -> gcp.ProjectSecretID), which fugaro init
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
	// tfvars as before the variable existed and plans without the scoped
	// deployer role and its binding (InstallOptions.ManagedDeployerScoped).
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

// Credentials is the credentials service's spec for lc: the names the module
// uses, the bundle container the loader also derives, and the image the
// caller verified. The image must be pinned by digest (credsImageRE) — this
// is the check the stage's own test walks the tag form through, and it is the
// same rule the Terraform variable states, so a tfvars this function returns
// is never one the plan refuses for its image.
func Credentials(lc *localcfg.Config, image string) (CredentialsServiceSpec, error) {
	if !credsImageRE.MatchString(image) {
		return CredentialsServiceSpec{}, userErr("the credentials service's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits): a tag is mutable, and this service reads every model credential the installation holds", image)
	}
	return CredentialsServiceSpec{
		AccountID:    CredentialsAccountID,
		Service:      CredentialsServiceName,
		Image:        image,
		BundleSecret: gcp.ProjectSecretID(lc.Name, CredentialsBundleName),
	}, nil
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
// that, the next plain `fugaro init` would plan the service, its account and
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
