// Package pluginwire wires the Fugaro plugin into a repository's project
// settings (design section 4.3) and reports how the wiring compares with the
// running binary (section 4.4).
//
// The settings file makes every teammate's agent load third-party skill text
// once they trust the folder, so this package is deliberately narrow: it
// touches only .claude/settings.json of a checkout, only the two entries
// extraKnownMarketplaces.fugaro and enabledPlugins["fugaro@fugaro"], only
// ever pins to a strict vX.Y.Z tag, never follows a symlink, never rewrites
// a file it cannot parse, and never commits.
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
	// Repo is the marketplace repository unless a fork's is already there.
	Repo = "dimipaun/fugaro"
)

var (
	// ErrDev is returned for a binary without a release version: it has no
	// tag to pin to, so nothing is written.
	ErrDev = errors.New("this is a dev build of fugaro, which has no release tag to pin the plugin to")
	// ErrNotRelease is returned for a version that is not X.Y.Z.
	ErrNotRelease = errors.New("the version is not a release version X.Y.Z")
)

var semverRE = regexp.MustCompile(`^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// tagRE is the only ref Wire writes.
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
