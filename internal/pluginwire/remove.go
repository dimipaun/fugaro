package pluginwire

import (
	"fmt"
	"io/fs"
	"strings"
)

// The removal of the plugin's two entries (U5 of
// docs/plugin-user-scope/implementation-plan.md, D943): the inverse of the
// merge this package used to make, and — with the merge deleted (D949) — the
// only thing in the tree that touches a pin at all. It takes
// extraKnownMarketplaces.fugaro and enabledPlugins["fugaro@fugaro"] out of a
// checkout's .claude/settings.json, keeps every other key and the file itself,
// and leaves a pin it cannot call its own exactly as it is: that is not a
// failure, it is a sentence beside the caller's own line (spec §8).

// Removal is a planned removal of the plugin's two entries from one settings
// file: the plan is computed from the file as it is, and Apply writes it.
type Removal struct {
	Path    string
	Before  []byte // the file as it was read, nil when it did not exist
	After   []byte // the file without the two entries
	Changed bool   // false: there is nothing of ours to take out
	existed bool
	perm    fs.FileMode
}

// PlanRemoval computes the removal of the plugin's two entries from the
// settings file at path (a project's .claude/settings.json), without writing.
// Every other key and entry is kept. A file that carries neither entry is no
// change at all — "nothing is removed twice" (spec §8) — and a pin that is not
// this package's to take out comes back as one of the refusal errors below,
// with the file untouched.
//
// The two entries go together or not at all: what the merge wrote is a pair
// (the marketplace and the plugin enabled from it), so a file carrying one of
// them and not the other is not a pin fugaro left and is left alone.
func PlanRemoval(path string) (*Removal, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	before, perm, existed, err := readFile(path, maxSettingsBytes)
	if err != nil {
		return nil, err
	}
	rem := &Removal{Path: path, Before: before, After: before, existed: existed, perm: perm}
	if !existed || strings.TrimSpace(string(before)) == "" {
		return rem, nil
	}
	top, err := parseObject(before)
	if err != nil {
		return nil, &InvalidError{path, err}
	}
	bad := func(e error) error { return &InvalidError{path, e} }
	markets, _, err := objectAt(top, "extraKnownMarketplaces")
	if err != nil {
		return nil, bad(err)
	}
	entry, present, err := objectAt(markets, Marketplace)
	if err != nil {
		return nil, bad(fmt.Errorf("extraKnownMarketplaces.%s: %w", Marketplace, err))
	}
	plugins, _, err := objectAt(top, "enabledPlugins")
	if err != nil {
		return nil, bad(err)
	}
	if !present {
		return rem, nil
	}
	source, hasSource, err := objectAt(entry, "source")
	if err != nil {
		return nil, bad(fmt.Errorf("extraKnownMarketplaces.%s.source: %w", Marketplace, err))
	}
	if !hasSource {
		return nil, bad(fmt.Errorf("extraKnownMarketplaces.%s has no source", Marketplace))
	}
	if kind := stringAt(source, "source"); kind != "github" {
		return nil, &ForeignError{Path: path, Kind: kind}
	}
	repo := stringAt(source, "repo")
	if repo == "" {
		return nil, bad(fmt.Errorf("extraKnownMarketplaces.%s.source has no repo", Marketplace))
	}
	if !strings.EqualFold(repo, Repo) {
		return nil, &ForkError{Path: path, Repo: repo}
	}
	ref := stringAt(source, "ref")
	if !tagRE.MatchString(ref) {
		return nil, &UnpinnedError{Path: path, Ref: ref}
	}
	raw, ok := plugins.get(PluginID)
	if !ok || string(raw) != "true" {
		// A repository's own "no" is not this package's to take out, and half
		// of the pair is not the pin the merge left either.
		return nil, &NotEnabledError{Path: path, Disabled: ok && string(raw) == "false"}
	}
	rem.Changed = true
	if i := markets.index(Marketplace); i >= 0 {
		markets = drop(markets, i)
	}
	if len(markets) == 0 {
		top = drop(top, top.index("extraKnownMarketplaces"))
	} else {
		top.set("extraKnownMarketplaces", markets.raw())
	}
	if i := plugins.index(PluginID); i >= 0 {
		plugins = drop(plugins, i)
	}
	if len(plugins) == 0 {
		top = drop(top, top.index("enabledPlugins"))
	} else {
		top.set("enabledPlugins", plugins.raw())
	}
	rem.After = pretty(top)
	return rem, nil
}

// drop returns o without its i-th member. A parent key whose last entry went
// goes with it: an empty object left behind is a key the file did not have, and
// Claude Code reads the same settings from a file without it.
func drop(o object, i int) object { return append(o[:i], o[i+1:]...) }

// Apply writes the removal atomically (temporary file, then rename), keeping
// the file's mode. It writes nothing when there is nothing to take out, and
// checks just before the rename that the file is still what PlanRemoval read,
// refusing otherwise. That check-then-rename is not a lock: an edit in the
// instant between them is not detected. The file is never deleted, and nothing
// is ever committed.
func (r *Removal) Apply() error {
	if !r.Changed {
		return nil
	}
	return replaceFile(r.Path, r.After, r.Before, r.existed, r.perm)
}

// InvalidError is returned for a settings file the removal cannot be planned
// in: not JSON (comments and trailing commas included), not an object, a
// duplicate key, or an entry of an unexpected shape. The sentence is the read
// side's own for the same file (Status), and the file is never rewritten.
type InvalidError struct {
	Path string
	Err  error
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%s is not usable settings: %v", e.Path, e.Err)
}
func (e *InvalidError) Unwrap() error { return e.Err }

// ForeignError is returned for the fugaro marketplace entry that is not a
// GitHub source: there is no repository of ours in it to take out.
type ForeignError struct{ Path, Kind string }

func (e *ForeignError) Error() string {
	return fmt.Sprintf("the %q marketplace in %s has a %s source, which fugaro does not rewrite", Marketplace, e.Path, Printable(e.Kind))
}

// ForkError is returned for a GitHub marketplace that names a repository which
// is not Repo: it is somebody's fork served under our marketplace's name, and
// taking the two entries out would remove their plugin.
type ForkError struct{ Path, Repo string }

func (e *ForkError) Error() string {
	return fmt.Sprintf("the %q marketplace in %s names the repository %s, not %s", Marketplace, e.Path, Printable(e.Repo), Repo)
}

// UnpinnedError is returned for the fugaro marketplace whose ref is not a
// release tag: a tag is the only ref the merge ever wrote, so a pin that
// carries anything else is not one fugaro left, and which pin it is is not
// this package's to guess. A ref that is empty is the same refusal in the read
// side's own words for it (Unpinned).
type UnpinnedError struct{ Path, Ref string }

func (e *UnpinnedError) Error() string {
	if e.Ref == "" {
		return fmt.Sprintf("the %q marketplace in %s has no ref, so it follows the default branch", Marketplace, e.Path)
	}
	return fmt.Sprintf("the %q marketplace in %s is pinned to %q, which is not a release tag vX.Y.Z", Marketplace, e.Path, Printable(e.Ref))
}

// NotEnabledError is returned for a settings file that carries the fugaro
// marketplace entry but does not enable the plugin from it: the file says the
// repository does not want it, and that is not this package's to take out.
type NotEnabledError struct {
	Path string
	// Disabled: the file holds enabledPlugins[PluginID] false, rather than
	// saying nothing about it.
	Disabled bool
}

func (e *NotEnabledError) Error() string {
	if e.Disabled {
		return fmt.Sprintf("the plugin %s is disabled by the repository (enabledPlugins is false in %s)", PluginID, e.Path)
	}
	return fmt.Sprintf("the plugin %s is not enabled in %s", PluginID, e.Path)
}
