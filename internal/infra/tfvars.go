package infra

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The installation's singletons. The installation module checks their
// shapes; only these constants name them.
const (
	SchedulerServiceAccountID = "fugaro-scheduler"
	RoleLauncher              = "fugaroLauncher"
	RoleJobRunner             = "fugaroJobRunner"
	RoleBuildSubmitter        = "fugaroBuildSubmitter"
	BaseRegistry              = "fugaro-base"
	LegacyRegistry            = "fugaro"
	LogBucket                 = "fugaro"
	LogView                   = "fugaro-runs"
	LogSink                   = "fugaro-jobs"
	LogExclusion              = "fugaro-jobs-from-default"
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
	StateBucket  string
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
	Project             string            `json:"project"`
	Region              string            `json:"region"`
	RunsBucket          string            `json:"runs_bucket"`
	StateBucket         string            `json:"state_bucket"`
	Names               InstallationNames `json:"names"`
	BucketLifecycle     BucketLifecycle   `json:"bucket_lifecycle"`
	EnableVertex        bool              `json:"enable_vertex"`
	ManageAPIs          bool              `json:"manage_apis"`
	Launchers           []string          `json:"launchers"`
	Operators           []string          `json:"operators"`
	Budget              *Budget           `json:"budget"`
	AlertEmail          *string           `json:"alert_email"`
	RegistryCleanup     RegistryCleanup   `json:"registry_cleanup"`
	AdoptLegacyRegistry bool              `json:"adopt_legacy_registry"`
	// LogIsolation is nil for the module's default, which is on; only
	// --no-log-isolation and the rollback turn it off.
	LogIsolation *bool `json:"log_isolation,omitempty"`
}

// InstallationNames are the installation's singleton names.
type InstallationNames struct {
	LegacyRegistry            string              `json:"legacy_registry"`
	BaseRegistry              string              `json:"base_registry"`
	SchedulerServiceAccountID string              `json:"scheduler_service_account_id"`
	RoleIDs                   InstallationRoleIDs `json:"role_ids"`
	Log                       LogNames            `json:"log"`
}

// InstallationRoleIDs are the custom roles' IDs (not their full names).
type InstallationRoleIDs struct {
	Launcher       string `json:"launcher"`
	JobRunner      string `json:"job_runner"`
	BuildSubmitter string `json:"build_submitter"`
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
	s := InstallationSpec{
		Project:     lc.Project,
		Region:      lc.Region,
		RunsBucket:  bucket,
		StateBucket: firstOf(o.StateBucket, lc.Terraform.StateBucket, "fugaro-tfstate-"+lc.Project),
		Names: InstallationNames{
			LegacyRegistry:            LegacyRegistry,
			BaseRegistry:              BaseRegistry,
			SchedulerServiceAccountID: SchedulerServiceAccountID,
			RoleIDs:                   InstallationRoleIDs{Launcher: RoleLauncher, JobRunner: RoleJobRunner, BuildSubmitter: RoleBuildSubmitter},
			Log:                       LogNames{Bucket: LogBucket, View: LogView, Sink: LogSink, Exclusion: LogExclusion},
		},
		BucketLifecycle:     BucketLifecycle{RunsDays: runsDays, CacheCustomTimeDays: cacheCustomTimeDays, CacheAgeDays: cacheAgeDays},
		EnableVertex:        o.EnableVertex,
		ManageAPIs:          !o.SkipAPIs,
		Launchers:           members(o.Launchers, lc.Terraform.Launchers),
		Operators:           members(o.Operators, lc.Terraform.Operators),
		Budget:              o.Budget,
		AdoptLegacyRegistry: o.AdoptLegacyRegistry,
	}
	if o.NoLogIsolation {
		s.LogIsolation = new(false)
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
		s.RegistryCleanup = RegistryCleanup{}
	default:
		return InstallationSpec{}, userErr("--registry-cleanup %q must be dry-run, on or off", o.RegistryCleanup)
	}
	return s, nil
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

// repoVars is the repository root's tfvars.
type repoVars struct {
	Project      string           `json:"project"`
	Region       string           `json:"region"`
	Installation RepoInstallation `json:"installation"`
	GitHubAppID  *string          `json:"github_app_id"`
	Repo         RepoSpec         `json:"repo"`
}

// RepoVars is the repository root's terraform.tfvars.json for spec.
func RepoVars(spec RepoSpec) ([]byte, error) {
	v := repoVars{Project: spec.Project, Region: spec.Region, Installation: spec.Installation, Repo: spec}
	if spec.GitHubAppID != "" {
		id := spec.GitHubAppID
		v.GitHubAppID = &id
	}
	v.Installation.Launchers = members(v.Installation.Launchers, nil)
	v.Installation.Operators = members(v.Installation.Operators, nil)
	return sortedJSON(v)
}

// InstallationVars is the installation root's terraform.tfvars.json.
func InstallationVars(spec InstallationSpec) ([]byte, error) {
	spec.Launchers = members(spec.Launchers, nil)
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
