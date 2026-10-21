package infra

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// The files and state prefixes of a root's workdir.
const (
	// StatePrefixInstallation is where the installation's state lives in
	// the state bucket; each repository's is under StatePrefixRepos.
	StatePrefixInstallation = "fugaro/installation"
	// StatePrefixFirebase is the Firebase root's state (operator-only, like
	// the installation's).
	StatePrefixFirebase = "fugaro/firebase"
	StatePrefixRepos    = "fugaro/repos/"
	// StatePrefixManaged is where the agent-authored root's state lives in
	// D77's dedicated bucket (managed.ManagedStateBucketName). It is a prefix
	// of a bucket of its own, so it carries none of the installation's state
	// and no part of the managed resource prefix (tf.ManagedPrefix, "fmgd-"):
	// the deployer's project-level role is conditioned on the latter, and its
	// access to the state is a binding on that bucket alone.
	StatePrefixManaged = "fugaro/managed"
	VarsFile           = "terraform.tfvars.json"
	BackendFile        = "backend.hcl"
	// BackendBlockFile is the root's own backend block: the file
	// Workdir.WriteBackendBlock writes, and the only backend terraform reads
	// in a managed apply's workdir.
	BackendBlockFile = "fugaro_backend.tf"
	// PlanFile is the saved plan that is shown, guarded and applied.
	PlanFile = "fugaro.tfplan"
)

// The roots the embedded tree holds.
var roots = map[string]bool{"installation": true, "repo": true, "firebase": true, "compute": true}

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

// FirebaseWorkdir is where fugaro init --firebase plans the Firebase root of
// project: $XDG_STATE_HOME/fugaro/terraform/<project>/firebase. It is a
// workdir of its own, so the installation's tfvars and saved plan are never
// overwritten by the Firebase root's.
func FirebaseWorkdir(getenv func(string) string, project string) (string, error) {
	dir, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "firebase"), nil
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

// ManagedWorkdir is where fugaro managed apply works on the agent-authored
// root of project: $XDG_STATE_HOME/fugaro/terraform/<project>/managed, beside
// the installation's workdir and never the checkout the root is read from
// (docs/design/managed-projects.md, D76). A workdir of its own, for the same
// reason the Firebase root's is one: the installation's tfvars, imports and
// saved plan must never be written by an apply of another root, and a plan
// saved by one root must never be gated and applied as another's.
func ManagedWorkdir(getenv func(string) string, project string) (string, error) {
	dir, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "managed"), nil
}

// Workdir is a disposable directory terraform runs a root in: Dir holds the
// CLI config and terraform's data directory, Root the root's files.
type Workdir struct {
	Dir, Root string
	// PrevVars is the root's terraform.tfvars.json as the workdir held it
	// BEFORE this one was prepared, or nil when it held none (a first run).
	//
	// PrepareWorkdir rebuilds the tree afresh, so by the time a caller runs
	// the file is gone — and a caller that has to carry ONE key of it forward
	// (infra.CarriedCredentialsService, the credentials stage's opt-in) has
	// only this copy. Reading it after the rebuild is the bug this field
	// exists to prevent: the read answers "there is no tfvars", the key is
	// dropped from the rewritten document, and the next plan is the service's
	// destruction — which the bundle container's prevent_destroy refuses, so
	// the run fails rather than applies anything.
	PrevVars []byte
}

// WorkdirVarsPath is where the root's tfvars live inside a workdir dir.
func WorkdirVarsPath(dir, root string) string {
	return filepath.Join(dir, "gcp", "roots", root, VarsFile)
}

// ReadWorkdirVars reads the tfvars a workdir dir holds for root: nil when it
// holds none, and an error when one is there and cannot be read — a tfvars
// this program cannot read is not permission to carry nothing forward.
func ReadWorkdirVars(dir, root string) ([]byte, error) {
	b, err := os.ReadFile(WorkdirVarsPath(dir, root))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading the workdir's %s: %w", VarsFile, err)
	}
	return b, nil
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
	// The root's tfvars of the run before this one, read BEFORE the tree is
	// rebuilt: os.RemoveAll below deletes them with everything else, and a
	// caller that has to carry a key forward (CarriedCredentialsService) can
	// only do that from this copy (Workdir.PrevVars).
	prev, err := ReadWorkdirVars(dir, root)
	if err != nil {
		return nil, err
	}
	tree := filepath.Join(dir, "gcp")
	if err := os.RemoveAll(tree); err != nil {
		return nil, err
	}
	err = fs.WalkDir(terraform.FS, "gcp", func(p string, d fs.DirEntry, err error) error {
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
	return &Workdir{Dir: dir, Root: filepath.Join(tree, "roots", root), PrevVars: prev}, nil
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

// WriteBackendBlock writes the root's backend block: the file terraform reads
// the backend from, and the one this program sets itself rather than taking
// from the configuration it is about to apply (D77, Task 3.1's residual (a)).
//
// Why a block and not only the -backend-config settings: terraform's
// -backend-config configures a backend the CONFIGURATION declares, and a root
// that declares none keeps the implicit local backend and ignores the settings
// (init warns and carries on). The managed apply's state is D77's bucket or it
// is not an apply, so the block is written here, and the root's own files are
// refused before init if they carry one (managed's preflight), which leaves
// exactly one.
func (w *Workdir) WriteBackendBlock() error {
	hcl := "terraform {\n  backend \"gcs\" {}\n}\n"
	return os.WriteFile(filepath.Join(w.Root, BackendBlockFile), []byte(hcl), 0o600)
}

// managedLockSource is the embedded lock file a managed root's own is cut
// from: the Firebase root's, the one embedded root that pins both of Google's
// providers (hashicorp/google and hashicorp/google-beta), with the versions
// and hashes this release of fugaro was built and tested against.
const managedLockSource = "gcp/roots/firebase/.terraform.lock.hcl"

// LockFile is the name of terraform's dependency lock file in a root.
const LockFile = ".terraform.lock.hcl"

// WriteManagedLock writes the root's dependency lock file: the entries of the
// embedded lock for exactly the providers named (registry addresses, such as
// "registry.terraform.io/hashicorp/google"), in that file's own words.
//
// Why this program writes it rather than taking the root's: terraform init
// runs with -lockfile=readonly (tf.Init), so the lock file decides which
// provider package is accepted — its hashes are what a downloaded or cached
// package is checked against — and a lock the agent wrote would let the agent
// choose the binary that runs, at init and plan, with the deployer's
// credential. The root's own lock is refused by the managed preflight before
// this runs, and this one names the embedded versions and nothing else.
//
// Only the providers the root uses are written, because terraform refuses a
// read-only lock file that carries a provider the configuration does not
// require. A provider the embedded lock does not carry is an error: there is
// no hash this program can vouch for.
func (w *Workdir) WriteManagedLock(providers []string) error {
	src, err := fs.ReadFile(terraform.FS, managedLockSource)
	if err != nil {
		return err
	}
	blocks, err := lockBlocks(string(src))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Written by fugaro managed apply from the lock file embedded in fugaro.\n\n")
	for i, p := range providers {
		body, ok := blocks[p]
		if !ok {
			return fmt.Errorf("the lock file embedded in fugaro carries no entry for %s, so its package cannot be checked", p)
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(body)
	}
	return os.WriteFile(filepath.Join(w.Root, LockFile), []byte(b.String()), 0o600)
}

// lockBlocks splits a lock file into its provider blocks, by address. The
// file is terraform's own output (embedded, never a root's), so the shape is
// fixed: a block opens with `provider "ADDR" {` at the start of a line and
// closes with `}` alone on a line.
func lockBlocks(src string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.SplitAfter(src, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "provider \"") {
			continue
		}
		addr, _, ok := strings.Cut(strings.TrimPrefix(l, "provider \""), "\"")
		if !ok {
			return nil, fmt.Errorf("the embedded lock file: a provider line with no closing quote")
		}
		var b strings.Builder
		for ; i < len(lines); i++ {
			b.WriteString(lines[i])
			if strings.TrimRight(lines[i], "\r\n") == "}" {
				break
			}
		}
		out[addr] = b.String()
	}
	return out, nil
}

// CopyRoot prepares dir as the workdir of an agent-authored root read from
// src: the root's files are copied into dir/root afresh, so no file of an
// earlier run (or an older fugaro) is planned, and a .terraform directory
// already there is left alone rather than deleted, as PrepareWorkdir leaves
// the one beside its root. (That is not terraform's data directory here:
// TF_DATA_DIR is <workdir>/.terraform — internal/infra/tf/env.go — and the
// managed apply passes the workdir itself as that workdir while running
// terraform in the root, so the directory this keeps is not the one terraform
// reads.)
//
// The copy is not a convenience: the checkout belongs to the agent and to the
// user, and terraform writes into the directory it runs in (a plan file, a
// .terraform directory, the lock file). Running in the checkout would put
// those beside the configuration under review, and would let a later run of
// this command plan a workdir another root left behind.
//
// What it refuses: src that is not a directory, a symlink in src (a file or a
// directory — a managed apply reads the root's own tree, and a link out of it
// is not that), and a dir inside src — a workdir the copy would write while it
// reads the root. What it does not do: it does not read src's files, so what
// they contain is the preflight's question and not this function's.
func CopyRoot(dir, src string) (*Workdir, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the workdir %s is not an absolute path", dir)
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(absSrc)
	switch {
	case err != nil:
		return nil, fmt.Errorf("the root %s: %w", src, err)
	case !st.IsDir():
		return nil, fmt.Errorf("the root %s is not a directory", src)
	}
	root := filepath.Join(dir, "root")
	if rel, err := filepath.Rel(absSrc, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("the workdir %s is inside the root %s, and the copy would read what it writes", root, src)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	// The root's own directory is replaced, and a .terraform directory in it is
	// kept rather than deleted with the rest. It is not what saves the provider
	// downloads: terraform consults TF_DATA_DIR, which this workdir's own
	// directory is (the doc comment above), and the plugin cache the CLI config
	// names (tf.terraformrc) is what reuses providers across applies. The skip
	// is left as it stands, so this is the honest description of it rather than
	// a reason.
	data := filepath.Join(root, ".terraform")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name() == ".terraform" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return nil, err
		}
	}
	err = filepath.WalkDir(absSrc, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absSrc, p)
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, and a managed apply reads the root's own tree alone", p)
		}
		out := filepath.Join(root, rel)
		if d.IsDir() {
			if rel == ".git" {
				return fs.SkipDir
			}
			return os.MkdirAll(out, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		return nil, fmt.Errorf("copying the root %s into %s: %w", src, dir, err)
	}
	return &Workdir{Dir: dir, Root: root}, nil
}
