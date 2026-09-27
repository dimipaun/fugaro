// Package localcfg reads the local CLI config, ~/.config/fugaro/config.yaml
// (design §5.4): the installation's project, region and buckets, and the
// onboarded repositories. M4's bootstrap script writes it; M5's fugaro init
// takes over.
package localcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the local CLI config.
type Config struct {
	Version     int             `yaml:"version"`
	Project     string          `yaml:"project"`
	Region      string          `yaml:"region"`
	RunsBucket  string          `yaml:"runs_bucket"`
	Bucket      string          `yaml:"bucket_url,omitempty"`
	Registry    string          `yaml:"registry,omitempty"`
	BaseImage   string          `yaml:"base_image,omitempty"`
	Build       Build           `yaml:"build"`
	User        string          `yaml:"user,omitempty"`
	MaxParallel int             `yaml:"max_parallel"`
	Endpoints   Endpoints       `yaml:"endpoints,omitempty"`
	Repos       map[string]Repo `yaml:"repos"`
}

// Build configures Cloud Build submissions.
type Build struct {
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
	NoAuth        bool   `yaml:"no_auth,omitempty"` // send no credentials (fakes only)
}

// Repo is an onboarded repository.
type Repo struct {
	BaseBranch string   `yaml:"base_branch,omitempty"`
	Workflows  []string `yaml:"workflows"`
}

// ErrMissing means there is no local config yet.
var ErrMissing = errors.New("no local fugaro config")

var (
	projectRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	regionRE   = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
	bucketRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	repoRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	workflowRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
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
		return nil, fmt.Errorf("%w at %s: create it with deploy/bootstrap/gcp-m4.sh config (fugaro init replaces it in M5)", ErrMissing, path)
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
	return &c, c.validate()
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
	for repo, r := range c.Repos {
		if !repoRE.MatchString(repo) {
			bad("repos: %q must look like owner/name", repo)
		}
		for _, w := range r.Workflows {
			if !workflowRE.MatchString(w) {
				bad("repos.%s: workflow %q is not a workflow name", repo, w)
			}
		}
	}
	return errors.Join(errs...)
}

// Override applies --project and --region when they are set.
func (c *Config) Override(project, region string) {
	if project != "" {
		c.Project = project
	}
	if region != "" {
		c.Region = region
	}
}

// BucketURL is the runs bucket as a gocloud URL.
func (c *Config) BucketURL() string {
	if c.Bucket != "" {
		return c.Bucket
	}
	return "gs://" + c.RunsBucket
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
