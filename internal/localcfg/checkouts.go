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
	// checkoutMissingGrace is how long a remembered path that is gone stays
	// remembered. A checkout is moved, renamed and cloned again all the time,
	// and the row is what the console offers a working directory from, so a
	// path that is gone is worth keeping for a while — the directory may come
	// back, and a machine that comes back to work in it should not have to
	// record it again — and worth dropping once nothing has brought it back.
	checkoutMissingGrace = 30 * 24 * time.Hour
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

// RepoFor is the repos: entry for repo, matched in canonical form
// (task.CanonicalRepo: ASCII-folded, without a trailing ".git"). It is the one
// lookup a launch resolves repos.<r>.base_branch through: `fugaro run`'s own
// cloudEnv.localRepo is this function, and `fugaro config list --json` reads
// its base_branch column through it as well, because the branch that document
// shows has to be the branch the run is cut from (docs/design/desktop-console.md
// §5.1 rule 11).
//
// Two entries can name one repository — "acme/app" and "acme/app.git", or two
// spellings the fold merges — and the entry that answers is the first of the
// sorted keys: the same entry on every read of unchanged files rather than
// whichever key a map iteration reached first (Enrolls' own reason, one
// function over).
//
// A repo that is not a canonical owner/name has no entry, and a nil config —
// one that could not be read — names no repository at all: both fail closed.
func (c *Config) RepoFor(repo string) (Repo, bool) {
	if c == nil {
		return Repo{}, false
	}
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return Repo{}, false
	}
	for _, name := range slices.Sorted(maps.Keys(c.Repos)) {
		if canonical, err := task.CanonicalRepo(name); err == nil && canonical == want {
			return c.Repos[name], true
		}
	}
	return Repo{}, false
}

// CheckoutPath is one remembered checkout: an absolute path, when it was
// first recorded (the order the bound drops by), and — while it is gone — when
// that was first seen (the clock the prune drops by).
type CheckoutPath struct {
	Path       string    `json:"path"`
	RecordedAt time.Time `json:"recorded_at"`
	// MissingSince is when this path was first seen to be gone, absent while
	// it is there. It is the one field a document written before it existed
	// does not carry, and such a row is read as one seen gone for the first
	// time: the clock starts at the first observation rather than at a moment
	// nothing recorded.
	MissingSince *time.Time `json:"missing_since,omitempty"`
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

// tempRootsOf is the temporary directories the writer refuses a path under:
// the caller's own list, or — a caller that names none — the machine's own
// temporary directory, os.TempDir(), which is $TMPDIR, %TEMP% or /tmp. The
// caller names them because the machine's temporary directories are the
// caller's knowledge: the CLI's list is the fuller one (its own tempRoots,
// which also covers the fixed /tmp, /private/tmp and /var/folders a TMPDIR
// pointing elsewhere would hide), and a test's own checkout lives in the
// temporary directory, so a test states its own.
func tempRootsOf(tempRoots [][]string) []string {
	if len(tempRoots) > 0 {
		return tempRoots[0]
	}
	return []string{filepath.Clean(os.TempDir())}
}

// InTempRoots reports whether path is one of roots or inside one: the rule
// that says a path is the machine's own scratch space rather than a checkout
// this machine remembers. The writer refuses such a path (RecordCheckout) and
// the pruning drops a row under one — one rule, so the two cannot drift. The
// read refuses a row under one as well, from the row's own string and without
// resolving anything (InTempRootsAsWritten).
//
// The path is judged twice, as written and as it resolves, and either verdict
// is enough:
//
//   - As written, because a path that is a link out of a temporary directory
//     is still a path under it. An agent's scratchpad is made in the temporary
//     directory and the link can be re-pointed after the row is written, while
//     the row is what the console is offered a working directory from — so
//     /tmp/x counts as temporary however x resolves today.
//   - As it resolves, because on darwin the temporary directory is reached
//     through a link: os.TempDir() is /var/folders/... while git answers with
//     /private/var/folders/..., so a path recorded in one spelling and a root
//     named in the other are the same directory and must compare equal.
//
// A path that is gone is compared as it is written, since a link cannot be
// resolved once what it points at is gone — which is why the caller's list
// names the fixed temporary directories in the spelling the platform writes
// them (internal/cli/tempxdg.go's tempRoots).
func InTempRoots(path string, roots []string) bool {
	if underAnyRoot(path, roots) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && underAnyRoot(resolved, roots)
}

// InTempRootsAsWritten is the same rule judged without resolving the path: no
// link is followed and nothing of the row itself is looked at, so the verdict
// costs the row no filesystem access at all and a path that is not there is
// refused exactly like one that is. It is what the read decides a row by
// (internal/cli/config_list.go): the row is a string this machine did not
// write, and it is refused for what it says — nothing of it is stat'ed and no
// git runs in it, whether or not it is there and whatever it is a checkout of.
//
// It is enough there because a scratchpad an agent made is a path *in* the
// temporary directory, which is a fact of the string. The writer judges the
// resolved path as well (InTempRoots) — it must not record a link out of a
// temporary directory either — so the two rules part company about one row: a
// row already in the file whose path is written outside every root and is a
// link into one. The read reports that row as it finds it, and the next write
// takes it away (the prune judges the resolved path). The read is left
// string-only on purpose: its refusal is the contract's first reason, which is
// the one decided from the row's own strings before anything touches the disk,
// and a read that resolved the path would be deciding it by following a link.
func InTempRootsAsWritten(path string, roots []string) bool {
	return underAnyRoot(path, roots)
}

// underAnyRoot is the comparison itself: path, cleaned, against every root,
// cleaned and — where it resolves — resolved.
func underAnyRoot(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, r := range roots {
		for _, root := range rootSpellings(r) {
			if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// rootSpellings is a root as it is written and, when it resolves to something
// else, as it resolves: on darwin /tmp is /private/tmp, and either spelling
// names the same temporary directory.
func rootSpellings(root string) []string {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		// "" and "." are not directories this machine is under, and a relative
		// root would make every relative path temporary.
		return nil
	}
	out := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		out = append(out, resolved)
	}
	return out
}

// pathGone reports whether the directory a row names is not there: the same
// test the read makes (config_list.go's validateCheckoutPath, its `missing`),
// so a row the prune drops for age is one a listing was already reporting as
// missing.
func pathGone(path string) bool {
	fi, err := os.Stat(path)
	return err != nil || !fi.IsDir()
}

// prune drops the rows this document must not hold any more and reports
// whether it changed anything: a row whose path is under one of roots, and a
// row that has been gone for longer than checkoutMissingGrace. A row that is
// there again has its missing clock cleared, and a row seen gone for the
// first time is stamped with now.
//
// It runs on the write, and never on a read: `fugaro config list` re-validates
// every path it reads and drops nothing — a stale path is still there for the
// operator to see, and still there if the directory comes back — while the
// state file is what the console offers working directories from, so a row
// nothing wants any more is the writer's to remove. A temp row is the reader's
// to refuse and this loop's to remove: config_list.go reports one invalid and
// leaves the dropping to here, and here it is dropped whatever its age and
// whether it is still there — a scratchpad worktree an agent left behind is a
// live checkout, and the live one is exactly the row the console would
// otherwise offer.
//
// The filesystem is touched once per row, and only by the stat the `missing`
// verdict is: the git work of a full re-validation is the reader's, so a path
// that is a directory again but no longer a checkout of this repository keeps
// its row until the reader says what it is.
func (c *Checkouts) prune(now time.Time, roots []string) bool {
	changed := false
	for project, repos := range c.Projects {
		for key, r := range repos {
			kept := r.Paths[:0]
			for _, p := range r.Paths {
				switch {
				case InTempRoots(p.Path, roots):
					changed = true
					continue
				case pathGone(p.Path):
					if p.MissingSince == nil {
						since := now
						p.MissingSince = &since
						changed = true
					} else if now.Sub(*p.MissingSince) > checkoutMissingGrace {
						changed = true
						continue
					}
				case p.MissingSince != nil:
					p.MissingSince = nil
					changed = true
				}
				kept = append(kept, p)
			}
			if len(kept) == 0 {
				// A repository with no paths is a row a reader would print as
				// an empty one; the state holds what this machine remembers,
				// and it remembers nothing about it.
				delete(repos, key)
				changed = true
				continue
			}
			r.Paths = kept
			repos[key] = r
		}
		if len(repos) == 0 {
			delete(c.Projects, project)
		}
	}
	return changed
}

// RecordCheckout remembers that path is a checkout of id for project.
//
// It is best effort: the caller ignores the error, and a command that could
// not record its checkout works exactly as it did before. Nothing here is
// ever fatal, and nothing here is printed — a path is local state (see the
// package comment above), so a refusal to record it is not worth a line on
// anyone's stderr.
//
// tempRoots is the caller's list of the machine's temporary directories, and
// it is a caller's statement rather than this package's because the machine's
// temporary directories are the caller's knowledge (the CLI's tempRoots, the
// list its own XDG warnings read). A path under one of them, as it is written
// or as it resolves (see InTempRoots), is not recorded at all, and a row
// already under one is dropped from the document (see prune): a checkout an
// agent made in a scratchpad is not a checkout the operator works in, and a
// remembered path is what the console offers as a working directory. A caller
// that names none gets os.TempDir().
//
// A path already recorded for that repository is left as it is: the file is
// written only when there is something new to write, so a command in a
// checkout this machine already knows writes nothing at all — unless the
// pruning has something to write, which is a row gone for good, a row that is
// back, or a row under a temporary directory.
func RecordCheckout(getenv func(string) string, project string, id CheckoutIdentity, path string, now time.Time, tempRoots ...[]string) error {
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
	roots := tempRootsOf(tempRoots)
	if InTempRoots(path, roots) {
		// Not an error and not a word anywhere: the caller's command did its
		// own work, and a path this machine must not remember is a path it
		// does not remember. Nothing is written, so the pruning below is not
		// reached either — a scratchpad is not a checkout this machine works
		// in, and a command run in one changes no state at all.
		return nil
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
	// The pruning first, and whether or not this path is a new one: a machine
	// whose operator only ever works in checkouts this file already knows is
	// the machine whose stale rows would otherwise never be dropped.
	pruned := c.prune(now, roots)
	proj := c.Projects[project]
	if proj == nil {
		proj = map[string]CheckoutRepo{}
	}
	key := id.Key()
	r := proj[key]
	if slices.ContainsFunc(r.Paths, func(p CheckoutPath) bool { return p.Path == path }) {
		if !pruned {
			return nil
		}
		// The path is already remembered and the write is the pruning's: the
		// row keeps the time it was recorded at, which is the order the bound
		// drops by, and nothing is added to it.
		proj[key] = r
		c.Projects[project] = proj
		return writeCheckouts(getenv, c)
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
	return writeCheckouts(getenv, c)
}

// writeCheckouts writes c as this machine's state file: the file of its own
// under the config home, 0600 in a directory created 0700, written whole
// through a rename (writeCacheFile), so a reader never sees half a document.
func writeCheckouts(getenv func(string) string, c *Checkouts) error {
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
