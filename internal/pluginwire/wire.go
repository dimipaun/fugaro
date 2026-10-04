package pluginwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	return fmt.Sprintf("the %q marketplace in %s has a %q source, which fugaro does not rewrite; set its ref to a release tag by hand", Marketplace, e.Path, e.Kind)
}

// Change is a planned update of one settings file.
type Change struct {
	Path    string
	Tag     string // the ref the entry will carry
	Before  []byte // nil when the file did not exist
	After   []byte
	Changed bool   // false: the file already says this, nothing to write
	Note    string // for the user, such as a fork's repository kept
	existed bool
	perm    fs.FileMode
}

// Plan computes the merge of the plugin's two entries into the settings file
// at path (a project's .claude/settings.json) for a binary of the given
// version, without writing. Every other key and entry is kept; a fork's
// repository is kept and only its ref moved. It returns ErrDev for a dev
// build, ErrNotRelease for any other version that is not X.Y.Z, and
// *InvalidError or *ForeignError when the file cannot be merged into.
func Plan(path, version string) (*Change, error) {
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
	dirty, note, err := merge(&top, path, tag)
	if err != nil {
		return nil, err
	}
	ch.Note = note
	ch.Changed = dirty
	if dirty {
		ch.After = pretty(top)
	} else {
		ch.After = before
	}
	return ch, nil
}

// merge sets the two entries in top and reports whether anything changed.
func merge(top *object, path, tag string) (dirty bool, note string, err error) {
	bad := func(e error) error { return &InvalidError{path, e} }

	markets, _, err := objectAt(*top, "extraKnownMarketplaces")
	if err != nil {
		return false, "", bad(err)
	}
	entry, present, err := objectAt(markets, Marketplace)
	if err != nil {
		return false, "", bad(fmt.Errorf("extraKnownMarketplaces.%s: %w", Marketplace, err))
	}
	var source object
	if present {
		var hasSource bool
		source, hasSource, err = objectAt(entry, "source")
		if err != nil {
			return false, "", bad(fmt.Errorf("extraKnownMarketplaces.%s.source: %w", Marketplace, err))
		}
		if !hasSource {
			return false, "", bad(fmt.Errorf("extraKnownMarketplaces.%s has no source", Marketplace))
		}
		kind := stringAt(source, "source")
		if kind != "github" {
			return false, "", &ForeignError{path, kind}
		}
		repo := stringAt(source, "repo")
		if repo == "" {
			return false, "", bad(fmt.Errorf("extraKnownMarketplaces.%s.source has no repo", Marketplace))
		}
		if !strings.EqualFold(repo, Repo) {
			note = fmt.Sprintf("kept the repository %s of the %q marketplace and moved only its ref", repo, Marketplace)
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
		return false, "", bad(err)
	}
	if plugins.set(PluginID, json.RawMessage("true")) {
		dirty = true
	}
	if top.set("enabledPlugins", plugins.raw()) {
		dirty = true
	}
	return dirty, note, nil
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

// Apply writes the change atomically, keeping the file's mode (0644 when
// new). It writes nothing when the change is empty, and refuses, writing
// nothing, if the file changed since Plan read it. It never commits.
func (c *Change) Apply() error {
	if !c.Changed {
		return nil
	}
	return replaceFile(c.Path, c.After, c.Before, c.existed, c.perm)
}

// Wire is Plan followed by Apply, for a caller that has already confirmed.
func Wire(path, version string) (*Change, error) {
	c, err := Plan(path, version)
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
// directory that holds .claude/settings.json, or else the top of the
// checkout (the nearest directory with .git, a file in a worktree) where one
// would be created. The walk stops at the checkout's top, and the user's own
// home directory is never a project. ok is false outside a checkout.
func Locate(start string) (loc Location, ok bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Location{}, false
	}
	home, _ := os.UserHomeDir()
	for {
		if home != "" && sameDir(dir, home) {
			return Location{}, false
		}
		s := filepath.Join(dir, ".claude", "settings.json")
		if _, err := os.Lstat(s); err == nil {
			return Location{Root: dir, Settings: s, Exists: true}, true
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
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
	return lineDiff(strings.Split(strings.TrimSuffix(string(c.Before), "\n"), "\n"), strings.Split(strings.TrimSuffix(string(c.After), "\n"), "\n"), c.existed)
}

func lineDiff(a, b []string, existed bool) string {
	if !existed {
		a = nil
	}
	n, m := len(a), len(b)
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
