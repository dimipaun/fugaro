// Package localcfg reads the project configs, the local CLI config of each
// Fugaro project: $XDG_CONFIG_HOME/fugaro/projects/<name>.yaml (else under
// ~/.config), holding the project's name, its GCP project, region and
// buckets, and the onboarded repositories (design §2.4 and §5.4). fugaro
// init writes them; Select picks the one a command acts on.
package localcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// Config is the local CLI config.
type Config struct {
	Version int `yaml:"version"`
	// Name is the Fugaro project (config.ProjectNameRE), which the file
	// is named after.
	Name string `yaml:"name"`
	// GCPProject is the GCP project ID the installation lives in.
	GCPProject string `yaml:"gcp_project"`
	Region     string `yaml:"region"`
	RunsBucket string `yaml:"runs_bucket"`
	Bucket     string `yaml:"bucket_url,omitempty"`
	// Registry is the legacy shared image registry, which is only read:
	// to roll back, and to find the images existing jobs still run.
	Registry string `yaml:"registry,omitempty"`
	// RegistryHost is <region>-docker.pkg.dev/<gcp_project>, the prefix of
	// every image registry of the installation.
	RegistryHost string    `yaml:"registry_host,omitempty"`
	BaseImage    string    `yaml:"base_image,omitempty"`
	Build        Build     `yaml:"build"`
	Terraform    Terraform `yaml:"terraform,omitempty"`
	// ComputePrices override the compute list prices of a region, for
	// cost estimates (design §10.1).
	ComputePrices map[string]Price `yaml:"compute_prices,omitempty"`
	// LogView is the log view Fugaro job logs are read through
	// (projects/<p>/locations/global/buckets/<b>/views/<v>); empty reads
	// the project's default logs.
	LogView string `yaml:"log_view,omitempty"`
	// SchedulerRegion is where the daily image check's Cloud Scheduler
	// jobs live, which need not be the region the jobs run in.
	SchedulerRegion string `yaml:"scheduler_region,omitempty"`
	// Budget is the project's model spend guard (design §5.6); no block
	// means off.
	Budget *Budget `yaml:"budget,omitempty"`
	// ModelPrices replace the built-in price of a model (or add one), by
	// model ID, for the budget's accounting.
	ModelPrices map[string]ModelPrice `yaml:"model_prices,omitempty"`
	User        string                `yaml:"user,omitempty"`
	MaxParallel int                   `yaml:"max_parallel"`
	Endpoints   Endpoints             `yaml:"endpoints,omitempty"`
	Repos       map[string]Repo       `yaml:"repos"`
}

// Budget modes.
const (
	BudgetOff     = policy.ModeOff
	BudgetObserve = policy.ModeObserve
	BudgetEnforce = policy.ModeEnforce
)

// Budget is the project's per-run model spend guard.
type Budget struct {
	Mode      string  `yaml:"mode"`        // off | observe | enforce; "" is off
	PerRunUSD float64 `yaml:"per_run_usd"` // enforce: 0 < x <= 100000; observe: >= 0
	// MaxRunTokens is the ceiling on a run's total tokens; 0 is none. A
	// repository's fugaro.yaml may only lower it.
	MaxRunTokens int64 `yaml:"max_run_tokens,omitempty"`
	// AllowedModels, when set, are the only models a run may use. Explicit
	// model IDs; an empty list is refused (it would forbid every model).
	AllowedModels []string `yaml:"allowed_models,omitempty"`
	// RTDBURL is the project's Firebase Realtime Database (the budget
	// backend of M9b), which fugaro budget reads and writes. Set by fugaro
	// init --firebase; empty means the project has none.
	RTDBURL string `yaml:"rtdb_url,omitempty"`
	// FirebaseProject is the project's own Firebase project (the FP) and
	// FirebaseAPIKey its restricted web API key (not a secret: it may call
	// only Identity Toolkit and Secure Token). TokenSigner is the signer
	// account the launcher mints run tokens as. All four are written by
	// fugaro init --firebase from the Firebase root's outputs.
	FirebaseProject string `yaml:"firebase_project,omitempty"`
	FirebaseAPIKey  string `yaml:"firebase_api_key,omitempty"`
	TokenSigner     string `yaml:"token_signer,omitempty"`
	// Grace is how long a run keeps going when the backend is unreachable
	// before it halts (D14); 0 is the default, 3m, and it may only shorten
	// it (budget.MinGrace to budget.MaxGrace, the bounds the job enforces).
	Grace time.Duration `yaml:"unreachable_grace,omitempty"`
}

// ModelPrice is one model's prices, in US dollars per million tokens. A
// field left out takes the list default (cache_write_5m 1.25, cache_write_1h
// 2, cache_read 0.1, web_search_per_1k 10), never 0; input_per_m and
// output_per_m are required.
type ModelPrice struct {
	InputPerM      *float64 `yaml:"input_per_m"`
	OutputPerM     *float64 `yaml:"output_per_m"`
	CacheWrite5m   *float64 `yaml:"cache_write_5m,omitempty"`
	CacheWrite1h   *float64 `yaml:"cache_write_1h,omitempty"`
	CacheRead      *float64 `yaml:"cache_read,omitempty"`
	WebSearchPer1k *float64 `yaml:"web_search_per_1k,omitempty"`
	// LongContext is the price of a call whose input passes a threshold,
	// for a model with a long-context tier; an override without it
	// replaces the model's tier with none.
	LongContext *LongContext `yaml:"long_context,omitempty"`
}

// LongContext is a long-context price tier.
type LongContext struct {
	AboveInputTokens int64    `yaml:"above_input_tokens"`
	InputPerM        *float64 `yaml:"input_per_m"`
	OutputPerM       *float64 `yaml:"output_per_m"`
}

// BudgetMode is the budget's mode: "off" when there is no budget block or
// its mode is empty.
func (c *Config) BudgetMode() string {
	if c.Budget == nil || c.Budget.Mode == "" {
		return BudgetOff
	}
	return c.Budget.Mode
}

// Overrides are the model prices as the pricing package takes them, with
// the list defaults applied to the fields left out.
func (c *Config) Overrides() (pricing.Overrides, error) {
	var errs []error
	o := pricing.Overrides{}
	for _, id := range slices.Sorted(maps.Keys(c.ModelPrices)) {
		mp := c.ModelPrices[id]
		if pricing.IsAlias(id) {
			errs = append(errs, fmt.Errorf("model_prices: %q is an alias: key prices by a model ID (such as claude-sonnet-5-5)", id))
			continue
		}
		if mp.InputPerM == nil || mp.OutputPerM == nil {
			errs = append(errs, fmt.Errorf("model_prices.%s: input_per_m and output_per_m are required", id))
			continue
		}
		or := func(p *float64, def float64) float64 {
			if p == nil {
				return def
			}
			return *p
		}
		r := pricing.Rates{
			InputPerM: *mp.InputPerM, OutputPerM: *mp.OutputPerM,
			CacheWrite5m:   or(mp.CacheWrite5m, pricing.DefaultCacheWrite5m),
			CacheWrite1h:   or(mp.CacheWrite1h, pricing.DefaultCacheWrite1h),
			CacheRead:      or(mp.CacheRead, pricing.DefaultCacheRead),
			WebSearchPer1k: or(mp.WebSearchPer1k, pricing.DefaultWebSearchPer1k),
		}
		if t := mp.LongContext; t != nil {
			if t.InputPerM == nil || t.OutputPerM == nil {
				errs = append(errs, fmt.Errorf("model_prices.%s.long_context: input_per_m and output_per_m are required", id))
				continue
			}
			r.LongContext = &pricing.Tier{AboveInputTokens: t.AboveInputTokens, InputPerM: *t.InputPerM, OutputPerM: *t.OutputPerM}
		}
		if err := r.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("model_prices.%s: %w", id, err))
			continue
		}
		o[id] = r
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	// Two keys naming one model, and anything else the table refuses.
	if _, err := pricing.Embedded().With(o); err != nil {
		return nil, fmt.Errorf("model_prices: %w", err)
	}
	return o, nil
}

// validateBudget checks the budget block and the model prices.
func (c *Config) validateBudget(bad func(string, ...any)) {
	if b := c.Budget; b != nil {
		if b.Mode != "" && !policy.ValidMode(b.Mode) {
			bad("budget.mode %q must be off, observe or enforce", b.Mode)
		}
		if b.MaxRunTokens < 0 {
			bad("budget.max_run_tokens %d: it must not be negative (0 is none)", b.MaxRunTokens)
		}
		if b.RTDBURL != "" {
			if err := rtdb.ValidateURL(b.RTDBURL, c.Endpoints.NoAuth); err != nil {
				bad("budget.rtdb_url: %v", err)
			}
		}
		if b.FirebaseProject != "" && !projectRE.MatchString(b.FirebaseProject) {
			bad("budget.firebase_project %q is not a GCP project ID", b.FirebaseProject)
		}
		if b.FirebaseAPIKey != "" && !apiKeyRE.MatchString(b.FirebaseAPIKey) {
			bad("budget.firebase_api_key is not a Google API key (20 to 128 letters, digits, - and _)")
		}
		if b.TokenSigner != "" && !signerRE.MatchString(b.TokenSigner) {
			bad("budget.token_signer %q is not a service account email (<id>@<project>.iam.gserviceaccount.com)", b.TokenSigner)
		}
		if b.Grace != 0 && (b.Grace < budget.MinGrace || b.Grace > budget.MaxGrace) {
			bad("budget.unreachable_grace %v: it must be from %v to %v (leave it out for 3m)", b.Grace, budget.MinGrace, budget.MaxGrace)
		}
		if b.AllowedModels != nil && len(b.AllowedModels) == 0 {
			bad("budget.allowed_models: it must list at least one model (an empty list, or one of only nulls, would forbid every model); leave it out for no restriction")
		}
		for i, m := range b.AllowedModels {
			if msg := config.CheckModelID(m); msg != "" {
				bad("budget.allowed_models[%d]: %s", i, msg)
			}
		}
		if i, ok := config.DuplicateModel(b.AllowedModels); ok {
			bad("budget.allowed_models[%d]: %s is listed twice; list each model once", i, b.AllowedModels[i])
		}
		if m, err := pricing.FromUSD(b.PerRunUSD); err != nil {
			bad("budget.per_run_usd %v: it must be a number from 0 to %d US dollars", b.PerRunUSD, pricing.MaxUSD)
		} else if b.PerRunUSD > 0 && m < 1 {
			bad("budget.per_run_usd %v: it rounds to nothing; the smallest cap is $0.000001", b.PerRunUSD)
		} else if b.Mode == BudgetEnforce && b.PerRunUSD <= 0 {
			bad("budget.per_run_usd must be more than 0 with budget.mode enforce (it is the per-run cap)")
		}
	}
	if _, err := c.Overrides(); err != nil {
		bad("%v", err)
	}
}

// Terraform is what fugaro init needs to plan the installation again: its
// state bucket, and the inputs that exist only as flags.
type Terraform struct {
	StateBucket string   `yaml:"state_bucket,omitempty"`
	AlertEmail  string   `yaml:"alert_email,omitempty"`
	Launchers   []string `yaml:"launchers,omitempty"` // IAM members, such as user:a@example.com
	Operators   []string `yaml:"operators,omitempty"`
	// BudgetAdmins are further budget admins on the Firebase project, IAM
	// members (user:, group: or serviceAccount:); the GCP project's owners
	// and editors are admins too, read when fugaro init --firebase runs.
	BudgetAdmins []string `yaml:"budget_admins,omitempty"`
}

// Price is a region's compute price per second, in US dollars.
type Price struct {
	VCPUSecondUSD float64 `yaml:"vcpu_second_usd"`
	GiBSecondUSD  float64 `yaml:"gib_second_usd"`
}

// PriceSource is the Source of overridden prices.
const PriceSource = "local override"

// MaxPriceUSD bounds a per-second price: list prices are around 1e-5, so
// anything above 1e-3 is a unit mistake (per hour, per month).
const MaxPriceUSD = 0.001

// Build configures Cloud Build submissions.
type Build struct {
	// ServiceAccount is deprecated: each repository's builds run as its
	// own build account. It is still accepted, with a warning.
	ServiceAccount string `yaml:"service_account,omitempty"`
	MachineType    string `yaml:"machine_type"`
	Region         string `yaml:"region,omitempty"`
}

// Endpoints override API roots, for fakes and emulators.
type Endpoints struct {
	Run           string `yaml:"run,omitempty"`
	Logging       string `yaml:"logging,omitempty"`
	SecretManager string `yaml:"secret_manager,omitempty"`
	CloudBuild    string `yaml:"cloud_build,omitempty"`
	// The APIs only fugaro init calls: bucket and IAM policy reads and
	// writes, service accounts, registries, the project's number, and the
	// enable of Cloud Resource Manager.
	Storage          string `yaml:"storage,omitempty"`
	IAM              string `yaml:"iam,omitempty"`
	ArtifactRegistry string `yaml:"artifact_registry,omitempty"`
	ResourceManager  string `yaml:"resource_manager,omitempty"`
	CloudScheduler   string `yaml:"cloud_scheduler,omitempty"`
	ServiceUsage     string `yaml:"service_usage,omitempty"`
	// CloudBilling is read by fugaro init --firebase to check that the
	// Firebase project has billing.
	CloudBilling string `yaml:"cloud_billing,omitempty"`
	// FirebaseDatabase lists the Firebase project's databases (init --firebase).
	FirebaseDatabase string `yaml:"firebase_database,omitempty"`
	// IAMCredentials is where the launcher signs a run's budget token (signJwt).
	IAMCredentials string `yaml:"iam_credentials,omitempty"`
	NoAuth         bool   `yaml:"no_auth,omitempty"` // send no credentials (fakes only)
}

// Repo is an onboarded repository.
type Repo struct {
	// Provider is the git provider kind (config.Providers: github or
	// bitbucket), part of the repository's storage slug. Empty means the
	// checkout's fugaro.yaml git.provider decides; when both are set they
	// must agree. repos is keyed by owner/name, so one local config holds
	// one provider per owner/name.
	Provider   string   `yaml:"provider,omitempty"`
	BaseBranch string   `yaml:"base_branch,omitempty"`
	Workflows  []string `yaml:"workflows"`
	// GitHubAppID is the repository's GitHub App, for GitHub
	// repositories. It is not a secret.
	GitHubAppID string `yaml:"github_app_id,omitempty"`
	// Vertex records that one of the repository's workflows authenticates
	// its agent through Vertex AI (agent.auth: vertex), so fugaro init
	// enables the Vertex AI API. fugaro init --repo sets it.
	Vertex bool `yaml:"vertex,omitempty"`
}

// UsesVertex reports whether any repository the config records uses
// Vertex AI.
func (c *Config) UsesVertex() bool {
	for _, r := range c.Repos {
		if r.Vertex {
			return true
		}
	}
	return false
}

// ErrMissing means there is no local config yet.
var ErrMissing = errors.New("no local fugaro config")

var (
	projectRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	regionRE  = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
	bucketRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	repoRE    = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	logViewRE = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/locations/[a-z0-9-]+/buckets/[a-z0-9_-]+/views/[A-Za-z0-9_-]+$`)
	// registryHostRE captures the project of <region>-docker.pkg.dev/<project>.
	registryHostRE = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-docker\.pkg\.dev/([a-z][a-z0-9-]{4,28}[a-z0-9])$`)
	appIDRE        = regexp.MustCompile(`^[0-9]{1,20}$`)
	memberRE       = regexp.MustCompile(`^(user|group|serviceAccount|domain):[^\s]+$`)
	emailRE        = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	// budgetAdminRE is a member that may hold roles on the Firebase
	// project: no domain: and no wildcard.
	budgetAdminRE = regexp.MustCompile(`^(user|group|serviceAccount):[^\s*]+$`)
	apiKeyRE      = regexp.MustCompile(`^[A-Za-z0-9_-]{20,128}$`)
	signerRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`)
)

// configDir is $XDG_CONFIG_HOME/fugaro, else ~/.config/fugaro.
func configDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "fugaro"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("neither XDG_CONFIG_HOME nor HOME is set, so there is no project config directory; pass --config")
	}
	return filepath.Join(home, ".config", "fugaro"), nil
}

// ProjectsDir is where the project configs live:
// $XDG_CONFIG_HOME/fugaro/projects, else ~/.config/fugaro/projects.
func ProjectsDir(getenv func(string) string) (string, error) {
	d, err := configDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects"), nil
}

// ProjectPath is the project config of project name.
func ProjectPath(getenv func(string) string, name string) (string, error) {
	if !config.ProjectNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", name)
	}
	d, err := ProjectsDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name+".yaml"), nil
}

// legacyPath is where the local config lived before project configs; it
// isn't read any more, only mentioned when there is no project config.
func legacyPath(getenv func(string) string) string {
	d, err := configDir(getenv)
	if err != nil {
		return ""
	}
	return filepath.Join(d, "config.yaml")
}

// Projects are the names of the project configs, sorted: every
// projects/<name>.yaml file whose <name> is a project name (backups,
// directories and other files are not project configs; a symlink to a
// regular file is one).
func Projects(getenv func(string) string) ([]string, error) {
	d, err := ProjectsDir(getenv)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || !config.ProjectNameRE.MatchString(name) {
			continue
		}
		// A symlink counts when it leads to a regular file, as loading
		// it would follow it.
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(d, e.Name())); err != nil || !fi.Mode().IsRegular() {
				continue
			}
		} else if !e.Type().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// LoadProject loads project name's config and its path. The file must
// hold name: <name>: a projects/<name>.yaml naming another project is
// refused rather than loaded as either.
func LoadProject(getenv func(string) string, name string) (*Config, string, error) {
	path, err := ProjectPath(getenv, name)
	if err != nil {
		return nil, "", err
	}
	c, err := Load(path)
	if err != nil {
		return nil, path, err
	}
	if c.Name != name {
		return nil, path, fmt.Errorf("%s holds project %s, not %s: a project config's name: must be its file's name", path, c.Name, name)
	}
	return c, path, nil
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s: run fugaro init to write it", ErrMissing, path)
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// oldProjectKey is the refusal of a local config of before project
// configs, whose project: was the GCP project.
const oldProjectKey = "local config: `project:` is now `gcp_project:`, and the file lives at projects/<name>.yaml with name: <name>; see docs/design/m9-budget-and-dashboard.md §13.1"

// Parse decodes the config strictly, applies defaults and validates it.
func Parse(data []byte) (*Config, error) {
	var top map[string]yaml.Node
	if yaml.Unmarshal(data, &top) == nil {
		if _, ok := top["project"]; ok {
			return nil, errors.New(oldProjectKey)
		}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.MaxParallel == 0 {
		c.MaxParallel = 20
	}
	if c.Build.MachineType == "" {
		c.Build.MachineType = "E2_HIGHCPU_8"
	}
	if err := c.validate(); err != nil {
		return &c, err
	}
	return &c, c.checkRegistryHostProject()
}

// checkRegistryHostProject refuses a registry_host naming another GCP
// project than the file's own gcp_project: the file must be consistent.
func (c *Config) checkRegistryHostProject() error {
	if m := registryHostRE.FindStringSubmatch(c.RegistryHost); m != nil && m[1] != c.GCPProject {
		return fmt.Errorf("local config: registry_host %q names GCP project %s, not %s", c.RegistryHost, m[1], c.GCPProject)
	}
	return nil
}

func (c *Config) validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("local config: "+f, a...)) }
	if c.Version != 1 {
		bad("version must be 1")
	}
	if !config.ProjectNameRE.MatchString(c.Name) {
		bad("name %q is not a project name (1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit)", c.Name)
	}
	if !projectRE.MatchString(c.GCPProject) {
		bad("gcp_project %q is not a GCP project ID", c.GCPProject)
	}
	if !regionRE.MatchString(c.Region) {
		bad("region %q is not a region such as us-central1", c.Region)
	}
	if c.Bucket == "" && !bucketRE.MatchString(c.RunsBucket) {
		bad("runs_bucket %q is not a bucket name (or set bucket_url)", c.RunsBucket)
	}
	if c.MaxParallel < 1 {
		bad("max_parallel must be at least 1")
	}
	if c.RegistryHost != "" && !registryHostRE.MatchString(c.RegistryHost) {
		bad("registry_host %q is not <region>-docker.pkg.dev/<project>", c.RegistryHost)
	}
	if c.LogView != "" && !logViewRE.MatchString(c.LogView) {
		bad("log_view %q is not projects/<project>/locations/<location>/buckets/<bucket>/views/<view>", c.LogView)
	}
	if c.SchedulerRegion != "" && !regionRE.MatchString(c.SchedulerRegion) {
		bad("scheduler_region %q is not a region such as us-east4", c.SchedulerRegion)
	}
	if t := c.Terraform; t.StateBucket != "" && !bucketRE.MatchString(t.StateBucket) {
		bad("terraform.state_bucket %q is not a bucket name", t.StateBucket)
	}
	if e := c.Terraform.AlertEmail; e != "" && !emailRE.MatchString(e) {
		bad("terraform.alert_email %q is not an email address", e)
	}
	for _, l := range []struct {
		name    string
		members []string
	}{{"launchers", c.Terraform.Launchers}, {"operators", c.Terraform.Operators}} {
		for _, m := range l.members {
			if !memberRE.MatchString(m) {
				bad("terraform.%s: %q is not an IAM member (user:, group:, serviceAccount: or domain:)", l.name, m)
			}
		}
	}
	for _, m := range c.Terraform.BudgetAdmins {
		if !budgetAdminRE.MatchString(m) {
			bad("terraform.budget_admins: %q is not an IAM member (user:, group: or serviceAccount: and one address; no domain: and no wildcards)", m)
		}
	}
	c.validateBudget(bad)
	for _, region := range slices.Sorted(maps.Keys(c.ComputePrices)) {
		if !regionRE.MatchString(region) {
			bad("compute_prices: %q is not a region such as us-central1", region)
			continue
		}
		p := c.ComputePrices[region]
		for _, f := range []struct {
			name string
			v    float64
		}{{"vcpu_second_usd", p.VCPUSecondUSD}, {"gib_second_usd", p.GiBSecondUSD}} {
			if !ValidPrice(f.v) {
				bad("compute_prices.%s.%s is %v: it must be set, finite, more than 0 and at most %v (US dollars per second)", region, f.name, f.v, MaxPriceUSD)
			}
		}
	}
	for _, ep := range []struct{ name, url string }{
		{"run", c.Endpoints.Run}, {"logging", c.Endpoints.Logging},
		{"secret_manager", c.Endpoints.SecretManager}, {"cloud_build", c.Endpoints.CloudBuild},
		{"storage", c.Endpoints.Storage}, {"iam", c.Endpoints.IAM},
		{"artifact_registry", c.Endpoints.ArtifactRegistry}, {"resource_manager", c.Endpoints.ResourceManager},
		{"cloud_scheduler", c.Endpoints.CloudScheduler}, {"service_usage", c.Endpoints.ServiceUsage},
		{"cloud_billing", c.Endpoints.CloudBilling}, {"firebase_database", c.Endpoints.FirebaseDatabase},
		{"iam_credentials", c.Endpoints.IAMCredentials},
	} {
		if ep.url == "" {
			continue
		}
		if err := checkEndpoint(ep.url); err != nil {
			bad("endpoints.%s: %v", ep.name, err)
		}
	}
	for repo, r := range c.Repos {
		if !repoRE.MatchString(repo) {
			bad("repos: %q must look like owner/name", repo)
		}
		if r.Provider != "" && !slices.Contains(config.Providers, r.Provider) {
			bad("repos.%s: provider %q must be one of %s", repo, r.Provider, strings.Join(config.Providers, ", "))
		}
		if r.GitHubAppID != "" && !appIDRE.MatchString(r.GitHubAppID) {
			bad("repos.%s: github_app_id %q is not a GitHub App ID (1 to 20 digits)", repo, r.GitHubAppID)
		}
		for _, w := range r.Workflows {
			if !config.WorkflowNameRE.MatchString(w) {
				bad("repos.%s: workflow %q is not a workflow name", repo, w)
			}
		}
	}
	return errors.Join(errs...)
}

// Override applies --gcp-project and --region when they are set, and
// validates the result: both go into resource paths. --gcp-project can
// only agree with the config's gcp_project: pointing a project's config at
// another GCP project would act on another project through it.
func (c *Config) Override(gcpProject, region string) error {
	if gcpProject != "" && gcpProject != c.GCPProject {
		return fmt.Errorf("--gcp-project %s is not project %s's GCP project %s; a project config can't be pointed at another GCP project (select that project's config instead)", gcpProject, c.Name, c.GCPProject)
	}
	if region != "" {
		c.Region = region
	}
	return c.validate()
}

// checkEndpoint refuses an endpoint the operator's credentials must not
// be sent to: anything but https, or plain http to this machine (the
// fakes tests run), or one carrying userinfo.
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL", raw)
	}
	if u.User != nil || u.Host == "" {
		return fmt.Errorf("%q must be an https URL with a host and no userinfo", raw)
	}
	switch h := u.Hostname(); {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (h == "127.0.0.1" || h == "localhost" || h == "::1"):
		return nil
	}
	return fmt.Errorf("%q must be https (plain http only to 127.0.0.1, localhost or ::1)", raw)
}

// ValidPrice reports whether v is a usable per-second price: finite, more
// than 0 and at most MaxPriceUSD.
func ValidPrice(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= MaxPriceUSD
}

// PriceOverride is region's compute price override, and whether there is
// one; without one, callers use the backend's list price. (The list price
// isn't looked up here: the GCP backend's live tests read this package.)
func (c *Config) PriceOverride(region string) (backend.Prices, bool) {
	p, ok := c.ComputePrices[region]
	if !ok {
		return backend.Prices{}, false
	}
	return backend.Prices{VCPUSecondUSD: p.VCPUSecondUSD, GiBSecondUSD: p.GiBSecondUSD, Source: PriceSource}, true
}

// Warnings are the config's deprecated settings, which still load.
func (c *Config) Warnings() []string {
	var w []string
	if c.Build.ServiceAccount != "" {
		w = append(w, "build.service_account is deprecated and will be ignored: each repository's builds run as its own build account; fugaro init drops it")
	}
	return w
}

// BucketURL is the runs bucket as a gocloud URL.
func (c *Config) BucketURL() string {
	if c.Bucket != "" {
		return c.Bucket
	}
	return "gs://" + c.RunsBucket
}

// RunsBucketName is the runs bucket's name: runs_bucket, else the bucket
// of a gs:// bucket_url, else "".
func (c *Config) RunsBucketName() string {
	if c.RunsBucket != "" {
		return c.RunsBucket
	}
	if u, err := url.Parse(c.Bucket); err == nil && u.Scheme == "gs" && u.Host != "" {
		return u.Host
	}
	return ""
}

// RecordBucketURL is the bucket image builds record in
// (builds/<slug>/<workflow>/image.json), as gs://<name>: always the runs
// bucket, the only bucket a build account may write (under
// builds/<slug>/), never another bucket_url. fugaro image build, fugaro
// init's first build and the daily check send it; readiness, ls and
// image status read it. "" when there is no runs bucket name.
func (c *Config) RecordBucketURL() string {
	if name := c.RunsBucketName(); name != "" {
		return "gs://" + name
	}
	return ""
}

// BuildRegion is where Cloud Build runs.
func (c *Config) BuildRegion() string {
	if c.Build.Region != "" {
		return c.Build.Region
	}
	return c.Region
}

// Me is who runs are requested by: user, else git config user.email.
func (c *Config) Me(ctx context.Context) (string, error) {
	if c.User != "" {
		return c.User, nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "user.email")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if me := strings.TrimSpace(string(out)); err == nil && me != "" {
		return me, nil
	}
	return "", errors.New("cannot tell who you are: set user in the local config or git config user.email")
}

// Marshal encodes the config as YAML.
func (c *Config) Marshal() ([]byte, error) { return yaml.Marshal(c) }
