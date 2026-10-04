package pluginwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InvalidError is returned for a settings file that cannot be merged into:
// not JSON (comments included), not an object, a duplicate key, or an entry
// of an unexpected shape. The file is never rewritten; the caller prints
// Snippet and the error.
type InvalidError struct {
	Path string
	Err  error
}

func (e *InvalidError) Error() string { return fmt.Sprintf("%s cannot be updated: %v", e.Path, e.Err) }
func (e *InvalidError) Unwrap() error { return e.Err }

// ForeignError is returned when the fugaro marketplace entry is not a GitHub
// source, so there is no ref of ours to move.
type ForeignError struct{ Path, Kind string }

func (e *ForeignError) Error() string {
	return fmt.Sprintf("the %q marketplace in %s has a %s source, which fugaro does not rewrite; set its ref to a release tag by hand", Marketplace, e.Path, Printable(e.Kind))
}

// ForkError is returned when the change would move the ref of, or enable the
// plugin from, a marketplace repository that is not Repo and the caller did
// not allow forks. Nothing is written.
type ForkError struct{ Path, Repo string }

func (e *ForkError) Error() string {
	return fmt.Sprintf("the %q marketplace in %s names the repository %s, not %s; it is left as it is (pass --allow-fork to move its ref to this release's tag)", Marketplace, e.Path, Printable(e.Repo), Repo)
}

// Printable makes a string from a repository's files safe to print to a
// terminal: control characters (escape sequences) and invisible or
// direction-changing format characters (bidi overrides) become \uXXXX.
func Printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\t') || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Change is a planned update of one settings file.
type Change struct {
	Path    string
	Tag     string // the ref the entry will carry
	Before  []byte // nil when the file did not exist
	After   []byte
	Changed bool   // false: the file already says this, nothing to write
	Note    string // for the user, such as a fork's repository kept
	Foreign string // the marketplace repository when it is not Repo (printable)
	// Disabled: the repository sets enabledPlugins["fugaro@fugaro"] to false,
	// which is kept.
	Disabled bool
	existed  bool
	perm     fs.FileMode
}

// Plan computes the merge of the plugin's two entries into the settings file
// at path (a project's .claude/settings.json) for a binary of the given
// version, without writing. Every other key and entry is kept; a fork's
// repository is kept and only its ref moved, and only with allowFork
// (otherwise *ForkError and nothing changes); a plugin the repository
// disables stays disabled. It returns ErrDev for a dev build, ErrNotRelease
// for any other version that is not X.Y.Z, and *InvalidError or
// *ForeignError when the file cannot be merged into.
func Plan(path, version string, allowFork bool) (*Change, error) {
	tag, err := Tag(version)
	if err != nil {
		return nil, err
	}
	if err := checkPath(path); err != nil {
		return nil, err
	}
	before, perm, existed, err := readFile(path, maxSettingsBytes)
	if err != nil {
		return nil, err
	}
	if !existed {
		perm = 0o644
	}
	top := object{}
	if strings.TrimSpace(string(before)) != "" {
		if top, err = parseObject(before); err != nil {
			return nil, &InvalidError{path, err}
		}
	}
	ch := &Change{Path: path, Tag: tag, Before: before, existed: existed, perm: perm}
	dirty, foreign, disabled, err := merge(&top, path, tag)
	if err != nil {
		return nil, err
	}
	if foreign != "" {
		ch.Foreign = Printable(foreign)
		ch.Note = fmt.Sprintf("the %q marketplace names the repository %s, not %s", Marketplace, ch.Foreign, Repo)
		if dirty && !allowFork {
			return nil, &ForkError{path, foreign}
		}
		if dirty {
			ch.Note += "; only its ref was moved"
		}
	}
	if disabled {
		ch.Disabled = true
		if ch.Note != "" {
			ch.Note += "\n"
		}
		ch.Note += "the plugin is disabled by this repository (enabledPlugins " + PluginID + " is false); it was left disabled"
	}
	ch.Changed = dirty
	if dirty {
		ch.After = pretty(top)
	} else {
		ch.After = before
	}
	return ch, nil
}

// merge sets the two entries in top and reports whether anything changed.
func merge(top *object, path, tag string) (dirty bool, foreign string, disabled bool, err error) {
	bad := func(e error) error { return &InvalidError{path, e} }

	markets, _, err := objectAt(*top, "extraKnownMarketplaces")
	if err != nil {
		return false, "", false, bad(err)
	}
	entry, present, err := objectAt(markets, Marketplace)
	if err != nil {
		return false, "", false, bad(fmt.Errorf("extraKnownMarketplaces.%s: %w", Marketplace, err))
	}
	var source object
	if present {
		var hasSource bool
		source, hasSource, err = objectAt(entry, "source")
		if err != nil {
			return false, "", false, bad(fmt.Errorf("extraKnownMarketplaces.%s.source: %w", Marketplace, err))
		}
		if !hasSource {
			return false, "", false, bad(fmt.Errorf("extraKnownMarketplaces.%s has no source", Marketplace))
		}
		kind := stringAt(source, "source")
		if kind != "github" {
			return false, "", false, &ForeignError{path, kind}
		}
		repo := stringAt(source, "repo")
		if repo == "" {
			return false, "", false, bad(fmt.Errorf("extraKnownMarketplaces.%s.source has no repo", Marketplace))
		}
		if !strings.EqualFold(repo, Repo) {
			foreign = repo
		}
	} else {
		source = object{{"source", marshalString("github")}, {"repo", marshalString(Repo)}}
	}
	if source.set("ref", marshalString(tag)) {
		dirty = true
	}
	if entry.set("source", source.raw()) {
		dirty = true
	}
	if markets.set(Marketplace, entry.raw()) {
		dirty = true
	}
	if top.set("extraKnownMarketplaces", markets.raw()) {
		dirty = true
	}

	plugins, _, err := objectAt(*top, "enabledPlugins")
	if err != nil {
		return false, "", false, bad(err)
	}
	if raw, ok := plugins.get(PluginID); ok && string(raw) == "false" {
		disabled = true // a repository's explicit "no" is not flipped
	} else if plugins.set(PluginID, json.RawMessage("true")) {
		dirty = true
	}
	if top.set("enabledPlugins", plugins.raw()) {
		dirty = true
	}
	return dirty, foreign, disabled, nil
}

func stringAt(o object, key string) string {
	raw, ok := o.get(key)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// Apply writes the change atomically (temporary file, then rename), keeping
// the file's mode (0644 when new). It writes nothing when the change is
// empty, and checks just before the rename that the file is still what Plan
// read, refusing otherwise. That check-then-rename is not a lock: an edit in
// the instant between them is not detected. It never commits.
func (c *Change) Apply() error {
	if !c.Changed {
		return nil
	}
	return replaceFile(c.Path, c.After, c.Before, c.existed, c.perm)
}

// Wire is Plan followed by Apply, for a caller that has already confirmed.
func Wire(path, version string, allowFork bool) (*Change, error) {
	c, err := Plan(path, version, allowFork)
	if err != nil {
		return nil, err
	}
	return c, c.Apply()
}

// Snippet is the two entries as the JSON a person can paste into
// .claude/settings.json by hand. A version that cannot be pinned gets the
// placeholder <release tag>.
func Snippet(version string) string {
	tag, err := Tag(version)
	if err != nil {
		tag = "<release tag>"
	}
	return fmt.Sprintf(`{
  "extraKnownMarketplaces": {
    "%s": { "source": { "source": "github", "repo": "%s", "ref": "%s" } }
  },
  "enabledPlugins": { "%s": true }
}
`, Marketplace, Repo, tag, PluginID)
}

// Location is where a checkout's project settings are.
type Location struct {
	Root     string // the checkout's top directory
	Settings string // Root/.claude/settings.json, or the nearest one below Root
	Exists   bool   // whether Settings exists
}

// Locate finds the project settings for start: walking up, the nearest
// directory that holds .claude/settings.json, provided the walk then finds
// .git (a file in a worktree) at or above it; or else the top of the checkout
// where one would be created. The walk stops at the checkout's top, and the
// user's own home directory is never a project. ok is false outside a
// checkout, whatever settings files lie above.
func Locate(start string) (loc Location, ok bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Location{}, false
	}
	home, _ := os.UserHomeDir()
	var found *Location
	for {
		if home != "" && sameDir(dir, home) {
			return Location{}, false
		}
		s := filepath.Join(dir, ".claude", "settings.json")
		if _, err := os.Lstat(s); err == nil && found == nil {
			found = &Location{Root: dir, Settings: s, Exists: true}
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			// A settings file counts only inside a checkout: .git at or above it.
			if found != nil {
				return *found, true
			}
			return Location{Root: dir, Settings: s}, true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return Location{}, false
		}
		dir = up
	}
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// Diff renders the change as a short line diff, "" when nothing changes.
func (c *Change) Diff() string {
	if !c.Changed {
		return ""
	}
	lines := func(b []byte) []string {
		ls := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		for i, l := range ls {
			ls[i] = Printable(l)
		}
		return ls
	}
	return lineDiff(lines(c.Before), lines(c.After), c.existed)
}

// maxDiffCells bounds the diff table: a settings file is a few dozen lines.
const maxDiffCells = 4_000_000

func lineDiff(a, b []string, existed bool) string {
	if !existed {
		a = nil
	}
	n, m := len(a), len(b)
	if n*m > maxDiffCells {
		return fmt.Sprintf("(the file changed: %d lines before, %d after; too long to diff here, use git diff)\n", n, m)
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out.WriteString("  " + a[i] + "\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out.WriteString("- " + a[i] + "\n")
			i++
		default:
			out.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	for ; i < n; i++ {
		out.WriteString("- " + a[i] + "\n")
	}
	for ; j < m; j++ {
		out.WriteString("+ " + b[j] + "\n")
	}
	return out.String()
}

// IsInvalid reports whether err says the file cannot be merged into.
func IsInvalid(err error) bool {
	var e *InvalidError
	return errors.As(err, &e)
}
