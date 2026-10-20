package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// The remembered checkouts: where this machine has a checkout of each
// repository a project enrolls.
//
// It is a machine-local convenience for the desktop console's first run
// (docs/design/desktop-console.md D13 and §3.4 rule 7): `fugaro project ls`
// reads it and the console, which never reads ~/.config/fugaro itself, gets
// each installation's repositories and where they are checked out from that
// command's document. A path is therefore local state and nothing else — it
// is never published to the runs bucket, never merged into the shared config
// (Shared drops it by not knowing about it) and never printed by a command
// other than `project ls` itself.
//
// It is a file of its own, not a key in <project>.yaml, for the reason the
// project config is a *document the operator owns*: `fugaro init` rewrites
// it with a diff and a .bak, every reader parses it strictly (Parse refuses
// an unknown key), and a machine-local path is not a fact about the
// installation that belongs in a file an operator reviews and commits into
// their own tooling. So this is state: it is best effort (a write that fails
// never fails the command that would have recorded it), it is dropped and
// rebuilt without ceremony, and its reader never trusts it — see
// `fugaro project ls`, which re-validates every path it reads.

// CheckoutsVersion is the version of the checkouts document. A file whose
// version is not this one is refused rather than read as this version's (and
// never written over), so a newer fugaro's state survives an older one.
const CheckoutsVersion = 1

// The bounds. They are what keeps a state file from growing without limit as
// checkouts are cloned, moved and deleted, and they are small on purpose: the
// console shows a handful of paths per repository, and the one an operator
// uses is the one they used last.
const (
	// MaxCheckoutRepos is the most repositories remembered per project. The
	// repository recorded longest ago is dropped.
	MaxCheckoutRepos = 20
	// MaxCheckoutPaths is the most paths remembered per repository. The path
	// recorded longest ago is dropped.
	MaxCheckoutPaths = 5
	// maxCheckoutsBytes bounds the read: the document is a few hundred bytes
	// per entry, so anything near this is not one.
	maxCheckoutsBytes = 1 << 20
	// maxCheckoutPathLen bounds a recorded path, in bytes (PATH_MAX on Linux
	// is 4096): a longer one is not a path this machine can be in.
	maxCheckoutPathLen = 4096
)

// CheckoutsFile is the state file's name, inside the config directory.
const CheckoutsFile = "checkouts.json"

// CheckoutIdentity is the identity a checkout is remembered by: the git
// provider and the repository's owner/name. It is the same pair
// `localcfg.Repo`'s map key and `task.Slug` are built from, so the same
// repository names the same entry whichever way it is reached.
type CheckoutIdentity struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
}

// Key is the identity as the document's map key: "<provider>/<owner>/<name>".
func (i CheckoutIdentity) Key() string { return i.Provider + "/" + i.Repo }

// CheckoutPath is one remembered checkout: an absolute path, and when it was
// first recorded (the order the bound drops by).
type CheckoutPath struct {
	Path       string    `json:"path"`
	RecordedAt time.Time `json:"recorded_at"`
}

// CheckoutRepo is one repository's remembered checkouts, as the document
// holds them. Provider and Repo repeat the map key so a reader does not have
// to take the key apart, and RecordedAt is when the last path was added to
// it: the order the per-project bound drops by.
type CheckoutRepo struct {
	Provider   string         `json:"provider"`
	Repo       string         `json:"repo"`
	RecordedAt time.Time      `json:"recorded_at"`
	Paths      []CheckoutPath `json:"paths"`
}

// Checkouts is the whole document: the repositories of each project this
// machine has recorded a checkout for.
type Checkouts struct {
	Version int `json:"version"`
	// Projects maps a project name (config.ProjectNameRE, the same names
	// projects/<name>.yaml uses) to its repositories, keyed by
	// CheckoutIdentity.Key.
	Projects map[string]map[string]CheckoutRepo `json:"projects"`
}

// CheckoutsPath is where the remembered checkouts live:
// $XDG_CONFIG_HOME/fugaro/checkouts.json, else ~/.config/fugaro/checkouts.json.
//
// It is beside the projects/ directory the project configs are in, not
// inside it: that directory is read as "the project configs" by Projects (and
// by anything else that looks at an operator's config directory), and a JSON
// state file among them is not one.
func CheckoutsPath(getenv func(string) string) (string, error) {
	d, err := configDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, CheckoutsFile), nil
}

// EmptyCheckouts is the document for a machine that has recorded nothing.
func EmptyCheckouts() *Checkouts {
	return &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{}}
}

// LoadCheckouts reads the remembered checkouts. A machine that has recorded
// none (no file, or one that was deleted) is an empty document and no error;
// a file that cannot be read, is larger than the bound, is not JSON, or is
// not this version, is an error — a reader that took it for "nothing
// recorded" would hide a state file that is there and wrong, and one that
// took a corrupt file for an empty one would then write over it.
func LoadCheckouts(getenv func(string) string) (*Checkouts, error) {
	path, err := CheckoutsPath(getenv)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyCheckouts(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > maxCheckoutsBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte bound for the remembered checkouts", path, fi.Size(), maxCheckoutsBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var c Checkouts
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s is not a checkouts document: %w", path, err)
	}
	if c.Version != CheckoutsVersion {
		return nil, fmt.Errorf("%s is version %d of the checkouts document, and this fugaro writes version %d", path, c.Version, CheckoutsVersion)
	}
	if c.Projects == nil {
		c.Projects = map[string]map[string]CheckoutRepo{}
	}
	return &c, nil
}

// ProjectCheckouts is project's remembered repositories, in identity order
// (the document's own maps are unordered, so a listing that ranged over them
// would print a different table on each read).
func (c *Checkouts) ProjectCheckouts(project string) []CheckoutRepo {
	m := c.Projects[project]
	out := make([]CheckoutRepo, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b CheckoutRepo) int {
		return strings.Compare(CheckoutIdentity{a.Provider, a.Repo}.Key(), CheckoutIdentity{b.Provider, b.Repo}.Key())
	})
	return out
}

// RecordCheckout remembers that path is a checkout of id for project.
//
// It is best effort: the caller ignores the error, and a command that could
// not record its checkout works exactly as it did before. Nothing here is
// ever fatal, and nothing here is printed — a path is local state (see the
// package comment above), so a refusal to record it is not worth a line on
// anyone's stderr.
//
// A path already recorded for that repository is left as it is: the file is
// written only when there is something new to write, so a command in a
// checkout this machine already knows writes nothing at all.
func RecordCheckout(getenv func(string) string, project string, id CheckoutIdentity, path string, now time.Time) error {
	if !config.ProjectNameRE.MatchString(project) {
		return fmt.Errorf("%q is not a project name", project)
	}
	if !slices.Contains(config.Providers, id.Provider) {
		return fmt.Errorf("%q is not a git provider (%s)", id.Provider, strings.Join(config.Providers, ", "))
	}
	if !repoRE.MatchString(id.Repo) {
		return fmt.Errorf("%q is not a repository (owner/name)", id.Repo)
	}
	if !filepath.IsAbs(path) || len(path) > maxCheckoutPathLen {
		// A relative path names nothing outside the process that recorded
		// it, and a path longer than this machine can have is not one.
		return fmt.Errorf("%q is not an absolute checkout path", path)
	}
	c, err := LoadCheckouts(getenv)
	if err != nil {
		// Never write over a document this fugaro does not understand.
		return err
	}
	proj := c.Projects[project]
	if proj == nil {
		proj = map[string]CheckoutRepo{}
	}
	key := id.Key()
	r := proj[key]
	if slices.ContainsFunc(r.Paths, func(p CheckoutPath) bool { return p.Path == path }) {
		return nil
	}
	r.Provider, r.Repo, r.RecordedAt = id.Provider, id.Repo, now
	r.Paths = append(r.Paths, CheckoutPath{Path: path, RecordedAt: now})
	if len(r.Paths) > MaxCheckoutPaths {
		r.Paths = r.Paths[len(r.Paths)-MaxCheckoutPaths:]
	}
	proj[key] = r
	for len(proj) > MaxCheckoutRepos {
		oldest := ""
		for k, v := range proj {
			if oldest == "" || v.RecordedAt.Before(proj[oldest].RecordedAt) || (v.RecordedAt.Equal(proj[oldest].RecordedAt) && k < oldest) {
				oldest = k
			}
		}
		delete(proj, oldest)
	}
	c.Projects[project] = proj
	file, err := CheckoutsPath(getenv)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeCacheFile(file, append(data, '\n'))
}
