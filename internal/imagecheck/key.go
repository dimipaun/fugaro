package imagecheck

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// This file is the normalised dependency-set key (design
// docs/forge-authorship-images/shared-images.md §2): the name two
// repositories' images are shared under. Two repositories whose key files
// reduce to the same facts — the same packages at the same versions, the
// same toolchain, the same base — are one key, and so one image (§1).
//
// The key is deliberately blind to everything else: a file's order,
// comments and formatting, the repository's name, its workflow and its
// build and test commands are not facts about a dependency set, and a key
// that moved with them would fork an image per repository. The reduction
// from a key file to these facts is the caller's (§2.3): this file takes
// the reduced set and hashes it.

// KeyVersion is the reduction's version, the canonical form's `key v3`
// line (§2.4). A change to the reduction's rules starts a new key universe
// instead of silently reusing images built under the old ones.
const KeyVersion = 3

// Resolved is one repository's dependency set, already reduced per
// ecosystem (§2.3): the packages its key files name, at the versions they
// resolved to, the toolchain pins, and the base image the build ran FROM.
// Nothing else is an input — there is no field for a repository's name, its
// workflow or its commands, so none of them can reach the key.
type Resolved struct {
	// BaseRef and BaseDigest are the base the build ran FROM, as the
	// record's BaseRef and BaseDigest hold it.
	BaseRef    string
	BaseDigest string
	// Packages are one package per fact, as its name, version and integrity
	// written as three quoted string literals (reduce.go's factOf), and Tools
	// are "tool@version": the names and versions as the file states them, after
	// the ecosystem's own canonicalisation where it defines one (PyPI's PEP 503
	// normalisation, the Go module path as written), and the integrity or
	// checksum the file states for that version's artifact — an empty literal
	// when the file states none for that package (D618). Dev-only dependencies
	// are not exempt (§2.3).
	Packages []string
	Tools    []string
}

// Key is the normalised key of a resolved dependency set: the lower-case
// hex SHA-256 of the canonical form.
func Key(r Resolved) string { return hashOf([]byte(canonical(r))) }

// canonical is §2.4's canonical form: a version line, then one line per
// fact, deduplicated and sorted bytewise, ending in a single newline. The
// version line stays first — it is the form's version, not a fact — and
// the sort is what makes a file's order and a JSON or YAML key order
// unable to matter.
func canonical(r Resolved) string {
	lines := make([]string, 0, 1+len(r.Packages)+len(r.Tools))
	lines = append(lines, "base:"+quoted(r.BaseRef+"@"+r.BaseDigest))
	for _, p := range r.Packages {
		lines = append(lines, "pkg:"+quoted(p))
	}
	for _, t := range r.Tools {
		lines = append(lines, "tool:"+quoted(t))
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)
	return fmt.Sprintf("key v%d\n%s\n", KeyVersion, strings.Join(lines, "\n"))
}

// quoted writes a fact as a quoted Go string literal, so a name or a version
// is one line whatever it holds. A fact is text a key file supplied, and an
// unquoted newline in one would end its line and let the rest of it be read
// as further facts: a package named "left-pad@1.0.0\npkg:react@18.3.1" would
// hash as the two-package set and take its key, and so its image. Quoting
// escapes that newline, and any other control character, a quote or a
// backslash, so no fact can forge another.
func quoted(fact string) string { return strconv.Quote(fact) }
