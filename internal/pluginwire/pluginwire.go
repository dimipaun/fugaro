// Package pluginwire reads the Fugaro plugin's pin in a checkout's project
// settings and reports how it compares with the running binary (design section
// 4.4).
//
// It used to write that pin (section 4.3). Nothing in the tree writes
// extraKnownMarketplaces.fugaro or enabledPlugins["fugaro@fugaro"] any more
// (D949, answered 2026-11-04: the merge is deleted, U2 of
// docs/plugin-user-scope/implementation-plan.md): the plugin is installed in
// Claude Code at user scope, on the machine, by fugaro update-skills and fugaro
// update's plugin stage. What stays is the read side — how a checkout that
// still carries a pin is told so, and doctor's plugin-pin check — and the parse
// and rewrite machinery the removal of those two entries reuses. It reads only
// .claude/settings.json of a checkout and only those two entries, never follows
// a symlink, never rewrites a file it cannot parse, and never commits.
package pluginwire

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// Marketplace is the key of the marketplace entry in settings.
	Marketplace = "fugaro"
	// PluginID is the key of the plugin in enabledPlugins.
	PluginID = "fugaro@fugaro"
	// Repo is the marketplace repository the plugin comes from.
	Repo = "dimipaun/fugaro"
)

var (
	// ErrDev is returned for a binary without a release version: it has no
	// release tag, so a pin cannot be compared with it.
	ErrDev = errors.New("this is a dev build of fugaro, which has no release tag to compare the plugin's pin with")
	// ErrNotRelease is returned for a version that is not X.Y.Z.
	ErrNotRelease = errors.New("the version is not a release version X.Y.Z")
)

var semverRE = regexp.MustCompile(`^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// tagRE is the only ref the read side compares with.
var tagRE = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// semver is a parsed X.Y.Z.
type semver [3]int

func parseSemver(s string) (semver, bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func (a semver) cmp(b semver) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (a semver) String() string { return fmt.Sprintf("%d.%d.%d", a[0], a[1], a[2]) }

// Tag is the release tag of a binary's version: "0.2.0" (or "v0.2.0") is
// "v0.2.0". A dev build gives ErrDev; anything else that is not X.Y.Z (a
// pre-release, a dirty build, a branch name) gives ErrNotRelease.
func Tag(version string) (string, error) {
	switch version {
	case "", "dev":
		return "", ErrDev
	}
	v, ok := parseSemver(version)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotRelease, version)
	}
	return "v" + v.String(), nil
}

// ValidTag reports whether ref is a strict vX.Y.Z tag.
func ValidTag(ref string) bool { return tagRE.MatchString(ref) }

func trimV(s string) string { return strings.TrimPrefix(s, "v") }
