package infra

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// The files and state prefixes of a root's workdir.
const (
	// StatePrefixInstallation is where the installation's state lives in
	// the state bucket; each repository's is under StatePrefixRepos.
	StatePrefixInstallation = "fugaro/installation"
	StatePrefixRepos        = "fugaro/repos/"
	VarsFile                = "terraform.tfvars.json"
	BackendFile             = "backend.hcl"
	// PlanFile is the saved plan that is shown, guarded and applied.
	PlanFile = "fugaro.tfplan"
)

// The roots the embedded tree holds.
var roots = map[string]bool{"installation": true, "repo": true}

var projectIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// xdgDir is $<envVar> when it is absolute (the XDG spec ignores a relative
// one), else $HOME/<fallback>.
func xdgDir(getenv func(string) string, envVar, fallback string) (string, error) {
	if d := getenv(envVar); filepath.IsAbs(d) {
		return d, nil
	}
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("neither %s nor HOME is an absolute path", envVar)
	}
	return filepath.Join(home, fallback), nil
}

// InstallationWorkdir is where fugaro init plans the installation of
// project: $XDG_STATE_HOME/fugaro/terraform/<project>/installation, by
// default under ~/.local/state.
func InstallationWorkdir(getenv func(string) string, project string) (string, error) {
	if !projectIDRE.MatchString(project) {
		return "", fmt.Errorf("project %q is not a GCP project ID", project)
	}
	state, err := xdgDir(getenv, "XDG_STATE_HOME", filepath.Join(".local", "state"))
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "fugaro", "terraform", project, "installation"), nil
}

// InstallationOutputsWorkdir is where fugaro init --repo reads the
// installation's outputs: a workdir of its own inside the installation's
// (which keeps only its gcp tree and terraform's data directory), so the
// installation's tfvars, imports and saved plan are never rewritten by a
// repository's run.
func InstallationOutputsWorkdir(getenv func(string) string, project string) (string, error) {
	dir, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "outputs"), nil
}

// PluginCache is terraform's provider cache,
// $XDG_CACHE_HOME/fugaro/terraform-plugins (by default under ~/.cache).
func PluginCache(getenv func(string) string) (string, error) {
	cache, err := xdgDir(getenv, "XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "fugaro", "terraform-plugins"), nil
}

// Workdir is a disposable directory terraform runs a root in: Dir holds the
// CLI config and terraform's data directory, Root the root's files.
type Workdir struct {
	Dir, Root string
}

// PrepareWorkdir makes dir (mode 0700) and writes the embedded tree into
// it afresh, so no file of an earlier run (or an older fugaro) is planned.
// Terraform's data directory (dir/.terraform) is kept. The root named root
// is where terraform runs.
func PrepareWorkdir(dir, root string) (*Workdir, error) {
	if !roots[root] {
		return nil, fmt.Errorf("no Terraform root %q", root)
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the workdir %s is not an absolute path", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory's mode alone.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	tree := filepath.Join(dir, "gcp")
	if err := os.RemoveAll(tree); err != nil {
		return nil, err
	}
	err := fs.WalkDir(terraform.FS, "gcp", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		b, err := fs.ReadFile(terraform.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		return nil, fmt.Errorf("writing the Terraform tree into %s: %w", dir, err)
	}
	return &Workdir{Dir: dir, Root: filepath.Join(tree, "roots", root)}, nil
}

// WriteVars writes the root's terraform.tfvars.json.
func (w *Workdir) WriteVars(data []byte) error {
	return os.WriteFile(filepath.Join(w.Root, VarsFile), data, 0o600)
}

var bucketNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)

// WriteBackend writes backend.hcl, the GCS backend's configuration (for
// anyone running terraform in the workdir by hand), and returns the same
// settings for terraform init.
func (w *Workdir) WriteBackend(bucket, prefix string) (map[string]string, error) {
	if !bucketNameRE.MatchString(bucket) {
		return nil, fmt.Errorf("the state bucket %q is not a bucket name", bucket)
	}
	if prefix == "" {
		return nil, errors.New("the state prefix is empty")
	}
	hcl := "bucket = " + strconv.Quote(bucket) + "\nprefix = " + strconv.Quote(prefix) + "\n"
	if err := os.WriteFile(filepath.Join(w.Root, BackendFile), []byte(hcl), 0o600); err != nil {
		return nil, err
	}
	return map[string]string{"bucket": bucket, "prefix": prefix}, nil
}
