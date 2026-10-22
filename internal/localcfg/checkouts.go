package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// The remembered checkouts: where this machine has a checkout of each
// repository a project enrolls.
//
// It is a machine-local convenience for the desktop console's first run
// (docs/design/desktop-console.md D13 and §3.4 rule 7): `fugaro config list
// --json` reads it and the console, which never reads ~/.config/fugaro
// itself, gets each installation's repositories and where they are checked
// out from that command's document. A path is therefore local state and
// nothing else — it is never published to the runs bucket, never merged into
// the shared config (Shared drops it by not knowing about it) and never
// printed by a command other than `config list` itself.
//
// It is a file of its own, not a key in <project>.yaml, for the reason the
// project config is a *document the operator owns*: `fugaro update` rewrites
// it with a diff and a .bak, every reader parses it strictly (Parse refuses
// an unknown key), and a machine-local path is not a fact about the
// installation that belongs in a file an operator reviews and commits into
// their own tooling. So this is state: it is best effort (a write that fails
// never fails the command that would have recorded it), it is dropped and
// rebuilt without ceremony, and its reader never trusts it — see
// `fugaro config list --json`, which re-validates every path it reads.
//
// It lives in a directory of its own under the config home (CheckoutsDir),
// created 0700, rather than beside the project configs: the config home is
// the operator's and may be 0755 — an existing directory's mode is left alone
// by MkdirAll, and this is not the file to chmod someone's ~/.config over —
// while the file itself is 0600 (the shape the console documents as its
// config-list contract, §6.8 of the JSON contract).
//
// Two writers racing lose one of the two updates: the load-modify-write is
// not locked, and the rename makes the file whole, not the pair of writes.
// The state is best effort by design — the loser's path is recorded the next
// time a command runs in that checkout — so no lock is taken and no caller
// has to know; the guarantee is that a reader never sees half a file, not
// that every concurrent write survives.

// CheckoutsVersion is the version of the checkouts document. A file whose
// version is not this one is refused rather than read as this version's (and
// never written over), so a newer fugaro's state survives an older one.
const CheckoutsVersion = 1

// The bounds. They are what keeps a state file from growing without limit as
// checkouts are cloned, moved and deleted, and they are small on purpose: the
// console shows a handful of paths per repository, and the one an operator
// uses is the one they used last.
//
// They are kept on the write (RecordCheckout drops the oldest) and applied
// again on the read (Bounded), because the state file is a file on this
// machine that this process did not write: a hand-edited one — or one written
// by a machine that has since cloned far more — must not cost a reader more
// than the bounds the writer keeps. Each checked path costs a stat and about
// three git executions, so the difference is a listing or a console hanging
// at startup.
const (
	// MaxCheckoutRepos is the most repositories remembered per project. The
	// repository recorded longest ago is dropped.
	MaxCheckoutRepos = 20
	// MaxCheckoutPaths is the most paths remembered per repository. The path
	// recorded longest ago is dropped.
	MaxCheckoutPaths = 5
	// MaxCheckoutRows is the most entries one read examines across the whole
	// document: ten projects at their full per-project bound
	// (MaxCheckoutRepos x MaxCheckoutPaths). Over it, the newest rows are the
	// ones examined and the reader says how many it did not.
	MaxCheckoutRows = 1000
	// maxCheckoutsBytes bounds the read: the document is a few hundred bytes
	// per entry, so anything near this is not one.
	maxCheckoutsBytes = 1 << 20
	// maxCheckoutPathLen bounds a recorded path, in bytes (PATH_MAX on Linux
	// is 4096): a longer one is not a path this machine can be in.
	maxCheckoutPathLen = 4096
)

// CheckoutsFile is the state file's name, inside CheckoutsDir.
const CheckoutsFile = "checkouts.json"

// CheckoutsDir is the directory the state file lives in, inside the config
// home: <config home>/state. It is a directory of its own so it can be 0700
// without touching the mode of the config home, which is the operator's
// (and may already be 0755).
const CheckoutsDir = "state"

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

// Enrolls reports whether the project config c enrolls the repository a
// checkout was recorded under: c's own repos: map names owner/name, and — when
// that entry names a provider — the same one. The map is keyed by the
// operator's own spelling, so the comparison folds case — with task.ASCIILower,
// the fold this tree adopts (configRepo's lookup, task.Slug's canonical form),
// which folds A-Z and nothing else: Unicode case folding would let two
// different names that look alike (U+212A KELVIN SIGN and "k") be read as one
// repository, and a name is the thing a link is built from. Two keys that do
// fold together ("acme/app" and "Acme/App") are resolved the way the rest of
// the tree resolves them — the exact spelling first (configRepo's rule), then
// the fold over the sorted keys — so the answer is the same on every read of
// unchanged files rather than whichever entry a map iteration reached first.
//
// It is a *local* fact, and that is the point: it reads the operator's own
// project config and nothing else. The published allow-list is the cloud's
// copy of the same decision (project policy publish seeds it from this map),
// and it is not read here — `fugaro config list --json` opens no bucket and
// takes no credentials.
//
// An entry with no provider: counts: provider: is optional there, and such an
// entry enrolls any provider. The provider compared here is the caller's — the
// one the checkout's origin host names (providerForHost), never the one its
// own fugaro.yaml claims.
//
// A nil config — one that could not be read — enrolls nothing: nothing can be
// proved about it, and the reader fails closed.
func (c *Config) Enrolls(provider, repo string) bool {
	if c == nil {
		return false
	}
	enrolls := func(r Repo) bool {
		return r.Provider == "" || task.ASCIILower(r.Provider) == task.ASCIILower(provider)
	}
	// The exact spelling first, as configRepo reads it: an entry the operator
	// wrote with upper-case letters is read as written where the repository is
	// written the same way.
	if r, ok := c.Repos[repo]; ok {
		return enrolls(r)
	}
	// Then the fold — and over the sorted keys, because two spellings can fold
	// together ("acme/app" and "Acme/App") and a gate that answered with
	// whichever one the map reached first would give a different verdict on
	// two reads of unchanged files. Sorted keys are what the rest of the tree
	// resolves such a duplicate by (init's key order), so the entry that
	// answers here is the one that answers there.
	folded := task.ASCIILower(repo)
	for _, name := range slices.Sorted(maps.Keys(c.Repos)) {
		if task.ASCIILower(name) == folded {
			return enrolls(c.Repos[name])
		}
	}
	return false
}

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
// $XDG_CONFIG_HOME/fugaro/state/checkouts.json, else
// ~/.config/fugaro/state/checkouts.json.
//
// The `state` directory is this file's own, created 0700 by the writer, and
// it is beside the projects/ directory the project configs are in rather than
// inside it: that directory is read as "the project configs" by Projects (and
// by anything else that looks at an operator's config directory), and a JSON
// state file among them is not one. The config home itself is not chmodded —
// see the package comment.
func CheckoutsPath(getenv func(string) string) (string, error) {
	d, err := configDir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, CheckoutsDir, CheckoutsFile), nil
}

// errCheckoutsVersion marks the one refusal RecordCheckout must not write
// over: a document a *newer* fugaro wrote. Every other refusal — JSON this
// build cannot parse, a byte count over the bound, a file that is not a
// regular file — is state that has gone wrong on this machine, and the
// writer replaces it (see RecordCheckout).
var errCheckoutsVersion = errors.New("checkouts document is from another version of fugaro")

// EmptyCheckouts is the document for a machine that has recorded nothing.
func EmptyCheckouts() *Checkouts {
	return &Checkouts{Version: CheckoutsVersion, Projects: map[string]map[string]CheckoutRepo{}}
}

// LoadCheckouts reads the remembered checkouts. A machine that has recorded
// none (no file, or one that was deleted) is an empty document and no error;
// a file that cannot be read, is larger than the bound, is not a regular file
// (a symlink, a FIFO, a directory), is not JSON, or is not this version, is an
// error — a reader that took it for "nothing recorded" would hide a state file
// that is there and wrong, and one that took a corrupt file for an empty one
// would then write over it.
//
// The file is read through config.ReadRegular: it is inspected and then
// opened, so a symlink is refused rather than followed, a FIFO is refused
// rather than blocked on, and a file that grew between the two calls is not
// read in full.
func LoadCheckouts(getenv func(string) string) (*Checkouts, error) {
	path, err := CheckoutsPath(getenv)
	if err != nil {
		return nil, err
	}
	data, err := config.ReadRegular(path, maxCheckoutsBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return EmptyCheckouts(), nil
	case err != nil:
		return nil, err
	}
	var c Checkouts
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s is not a checkouts document: %w", path, err)
	}
	if c.Version != CheckoutsVersion {
		return nil, fmt.Errorf("%s is version %d of the checkouts document, and this fugaro writes version %d: %w",
			path, c.Version, CheckoutsVersion, errCheckoutsVersion)
	}
	if c.Projects == nil {
		c.Projects = map[string]map[string]CheckoutRepo{}
	}
	return &c, nil
}

// Bounded is this document as a read may present it: for each of the named
// projects, the newest MaxCheckoutRepos repositories by their recorded time
// and the newest MaxCheckoutPaths paths of each, and MaxCheckoutRows rows in
// total across all of them (newest first). dropped is how many entries those
// bounds cut, so a reader can say the file is over them rather than present
// the trimmed list as the whole one.
//
// It is the same bound RecordCheckout keeps on the write, applied on the read
// because the file is one this process did not write. Nothing here reads the
// filesystem: the paths are not touched, only chosen.
func (c *Checkouts) Bounded(projects []string) (*Checkouts, int) {
	type row struct {
		project string
		repo    CheckoutRepo
		path    CheckoutPath
	}
	out := EmptyCheckouts()
	dropped := 0
	var rows []row
	for _, project := range projects {
		repos := c.ProjectCheckouts(project)
		// Every entry the file holds for this project, and every entry the
		// bounds will examine: dropped is the difference, counted in entries
		// (rows, one per path) rather than in repositories. A repository the
		// bound drops takes its paths with it — up to MaxCheckoutPaths of
		// them — so counting it as one entry would understate what a reader
		// did not examine by that much.
		held, examined := 0, 0
		for _, r := range repos {
			held += len(r.Paths)
		}
		if len(repos) > MaxCheckoutRepos {
			repos = newest(repos, func(r CheckoutRepo) time.Time { return r.RecordedAt }, MaxCheckoutRepos)
		}
		for _, r := range repos {
			r.Paths = newest(r.Paths, func(p CheckoutPath) time.Time { return p.RecordedAt }, MaxCheckoutPaths)
			examined += len(r.Paths)
			for _, p := range r.Paths {
				rows = append(rows, row{project: project, repo: r, path: p})
			}
		}
		dropped += held - examined
	}
	if len(rows) > MaxCheckoutRows {
		slices.SortStableFunc(rows, func(a, b row) int { return b.path.RecordedAt.Compare(a.path.RecordedAt) })
		dropped += len(rows) - MaxCheckoutRows
		rows = rows[:MaxCheckoutRows]
	}
	for _, r := range rows {
		proj := out.Projects[r.project]
		if proj == nil {
			proj = map[string]CheckoutRepo{}
			out.Projects[r.project] = proj
		}
		key := CheckoutIdentity{r.repo.Provider, r.repo.Repo}.Key()
		bounded := proj[key]
		bounded.Provider, bounded.Repo, bounded.RecordedAt = r.repo.Provider, r.repo.Repo, r.repo.RecordedAt
		bounded.Paths = append(bounded.Paths, r.path)
		proj[key] = bounded
	}
	return out, dropped
}

// newest is the n entries of items with the newest stamps, in the order items
// holds them: the entry recorded longest ago is what a bound drops, and a
// listing keeps the order the file has (the maps above are unordered, so the
// order here is what makes two reads of one file print the same thing).
// Ties go to the entry items holds later, which is the one written later.
func newest[T any](items []T, at func(T) time.Time, n int) []T {
	if len(items) <= n {
		return items
	}
	type indexed struct {
		i int
		t time.Time
	}
	idx := make([]indexed, len(items))
	for i, it := range items {
		idx[i] = indexed{i: i, t: at(it)}
	}
	slices.SortStableFunc(idx, func(a, b indexed) int { return a.t.Compare(b.t) })
	idx = idx[len(idx)-n:]
	slices.SortStableFunc(idx, func(a, b indexed) int { return a.i - b.i })
	out := make([]T, len(idx))
	for i, k := range idx {
		out[i] = items[k.i]
	}
	return out
}

// ProjectCheckouts is project's remembered repositories, in identity order
// (the document's own maps are unordered, so a listing that ranged over them
// would print a different table on each read). It is the document as it is
// held, bounds and all: a reader presents it through Bounded.
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
		if errors.Is(err, errCheckoutsVersion) {
			// Never write over a document a newer fugaro wrote: this build
			// cannot read it, and rebuilding it from nothing would drop that
			// fugaro's state.
			return err
		}
		// Everything else the read refuses — JSON this build cannot parse, a
		// byte count over the bound, a file that is not a regular file — is
		// this machine's state gone wrong, and this file is pure state: it is
		// dropped and rebuilt rather than left refusing every later write.
		// (The refusal is otherwise sticky: nothing but a person could clear
		// a zero-length file left by a crash, and recording would stay broken
		// until they did.)
		c = EmptyCheckouts()
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
