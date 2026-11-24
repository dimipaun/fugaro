package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// A recipe a repository keeps becomes the project's own copy of it, and the
// copy's name says which repository that is (D3302): the recipe's name, a dot,
// the readable half of the repository's slug and the first 8 hex of that slug's
// hash. The readable half alone does not tell two repositories apart —
// task.Slug merges "acme/app-web" and "acme-app/web" — so the hash half is what
// keeps their copies from colliding.
const (
	// maxNameLen is the longest recipe name (NameRE).
	maxNameLen = 40
	// maxCopyReadable is the longest readable half: a slug's readable part,
	// which task.Slug holds to 63 - 1 - 16.
	maxCopyReadable = 63 - 1 - 16
	// copyHashHex is the width of a copy's hash suffix, half of a slug's 16:
	// enough to keep the readable halves task.Slug merges apart.
	copyHashHex = 8
	// MaxCopyName is the longest copy name: a recipe name, the dot, the
	// readable half, the hyphen and 8 hex.
	MaxCopyName = maxNameLen + 1 + maxCopyReadable + 1 + copyHashHex
)

// copyReadableSrc is the readable half's shape: a slug's readable part, never
// empty and never ending in the "-" that separates it from the hash.
const copyReadableSrc = `[a-z0-9](?:[a-z0-9-]{0,44}[a-z0-9])?`

// CopyNameRE is a copy name: a recipe name, one dot, a readable half, a hyphen
// and 8 lower-case hex, at most MaxCopyName characters (D3302). It is a second
// grammar beside NameRE rather than a widening of it — NameRE still refuses
// every dot — so a dotted name that is not one of these stays refused.
var CopyNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?\.` + copyReadableSrc + `-[0-9a-f]{8}$`)

// IsCopyName reports whether name is a copy name (CopyNameRE).
func IsCopyName(name string) bool { return CopyNameRE.MatchString(name) }

// CopyName is the name of a repository's own copy of the recipe name (D3302),
// empty where there is no such name: a provider and repository task.Slug
// refuses, or a name that is not a recipe name — which a copy name is not, so a
// copy has no copy. What it returns is always a name IsCopyName accepts.
//
// The copy is the same recipe under another name, so the caller holds the
// repository to the spelling task.Slug keys everything else by (case, ".git")
// rather than this function repairing one.
func CopyName(name, provider, repo string) string {
	if !NameRE.MatchString(name) {
		return ""
	}
	readable, hash, ok := slugCopyParts(provider, repo)
	if !ok {
		return ""
	}
	return name + "." + readable + "-" + hash
}

// LookupOrder is where a name resolves for one repository, nearest first
// (D3303): the repository's own copy in the project store, then the name
// itself. The catalog is read after both.
//
// A repository with no copy — one task.Slug cannot key, or a name that is a
// copy already, since a copy of a copy does not exist — resolves by the name
// alone, which is where it resolved before the copies existed. So an
// unreadable remote or an inherited copy name changes no resolution.
func LookupOrder(name, provider, repo string) []string {
	if copy := CopyName(name, provider, repo); copy != "" {
		return []string{copy, name}
	}
	return []string{name}
}

// slugCopyParts is the readable half and the first 8 hex of task.Slug(provider,
// repo)'s hash, and it spells that derivation out rather than calling it:
// internal/task imports this package for a task's recipe name, so the two
// spellings are kept together by internal/task's TestCopyNameMatchesSlug.
var (
	copyProviderRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	copyRepoRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)
	copyUnsafeRE   = regexp.MustCompile(`[^a-z0-9]+`)
)

func slugCopyParts(provider, repo string) (readable, hash string, ok bool) {
	if !copyProviderRE.MatchString(provider) {
		return "", "", false
	}
	c := strings.TrimSuffix(asciiLower(repo), ".git")
	if !copyRepoRE.MatchString(repo) || !copyRepoRE.MatchString(c) {
		return "", "", false
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == "." || seg == ".." {
			return "", "", false
		}
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + c))
	readable = strings.Trim(copyUnsafeRE.ReplaceAllString(c, "-"), "-")
	if len(readable) > maxCopyReadable {
		readable = strings.TrimRight(readable[:maxCopyReadable], "-")
	}
	if readable == "" {
		readable = "repo"
	}
	return readable, hex.EncodeToString(sum[:])[:copyHashHex], true
}

// asciiLower is task.ASCIILower's fold and not strings.ToLower: Unicode case
// folding turns look-alike names into one (a Kelvin sign K into an ASCII "k"),
// which would give two repositories one copy name.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
