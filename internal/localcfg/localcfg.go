// Package localcfg reads the local CLI config, ~/.config/fugaro/config.yaml
// (design §5.4): the installation's project, region and buckets, and the
// onboarded repositories. fugaro init writes it (M4's bootstrap script
// wrote the first ones, which still load).
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
	"github.com/dimipaun/fugaro/internal/config"
)

// Config is the local CLI config.
type Config struct {
	Version    int    `yaml:"version"`
	Project    string `yaml:"project"`
	Region     string `yaml:"region"`
	RunsBucket string `yaml:"runs_bucket"`
	Bucket     string `yaml:"bucket_url,omitempty"`
	// Registry is the legacy shared image registry, which is only read:
	// to roll back, and to find the images existing jobs still run.
	Registry string `yaml:"registry,omitempty"`
	// RegistryHost is <region>-docker.pkg.dev/<project>, the prefix of
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
	SchedulerRegion string          `yaml:"scheduler_region,omitempty"`
	User            string          `yaml:"user,omitempty"`
	MaxParallel     int             `yaml:"max_parallel"`
	Endpoints       Endpoints       `yaml:"endpoints,omitempty"`
	Repos           map[string]Repo `yaml:"repos"`
}

// Terraform is what fugaro init needs to plan the installation again: its
// state bucket, and the inputs that exist only as flags.
type Terraform struct {
	StateBucket string   `yaml:"state_bucket,omitempty"`
	AlertEmail  string   `yaml:"alert_email,omitempty"`
	Launchers   []string `yaml:"launchers,omitempty"` // IAM members, such as user:a@example.com
	Operators   []string `yaml:"operators,omitempty"`
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
	NoAuth           bool   `yaml:"no_auth,omitempty"` // send no credentials (fakes only)
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
)

// Path is where the config lives: $FUGARO_CONFIG, else
// $XDG_CONFIG_HOME/fugaro/config.yaml, else ~/.config/fugaro/config.yaml.
func Path(getenv func(string) string) (string, error) {
	if p := getenv("FUGARO_CONFIG"); p != "" {
		return p, nil
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "fugaro", "config.yaml"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set; set FUGARO_CONFIG to the local config file")
	}
	return filepath.Join(home, ".config", "fugaro", "config.yaml"), nil
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

// Parse decodes the config strictly, applies defaults and validates it.
func Parse(data []byte) (*Config, error) {
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

// checkRegistryHostProject refuses a registry_host naming another project
// than the file's own. It is checked on the file only: --project may point
// a read-only command such as ls at another project, where the registry
// is not used.
func (c *Config) checkRegistryHostProject() error {
	if m := registryHostRE.FindStringSubmatch(c.RegistryHost); m != nil && m[1] != c.Project {
		return fmt.Errorf("local config: registry_host %q names project %s, not %s", c.RegistryHost, m[1], c.Project)
	}
	return nil
}

func (c *Config) validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("local config: "+f, a...)) }
	if c.Version != 1 {
		bad("version must be 1")
	}
	if !projectRE.MatchString(c.Project) {
		bad("project %q is not a GCP project ID", c.Project)
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

// Override applies --project and --region when they are set, and
// validates the result: both go into resource paths.
func (c *Config) Override(project, region string) error {
	if project != "" {
		c.Project = project
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
