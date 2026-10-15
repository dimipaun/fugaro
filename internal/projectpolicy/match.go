package projectpolicy

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// Canonical is the one spelling of a repository in the policy and in the
// allow-list: "<provider>/<owner>/<name>", lower-cased, without a trailing
// ".git" ("github/acme/app"). It calls task's own CanonicalRepo and validates
// the provider kind with task's own check, so the allow-list and task.Slug
// agree by construction (P5): the repository whose slug is
// task.Slug(provider, repo) is exactly the one MatchRepo matches with this
// string.
func Canonical(provider, repo string) (string, error) {
	if !task.ValidProvider(provider) {
		return "", fmt.Errorf("git provider %q is not a provider kind", provider)
	}
	c, err := task.CanonicalRepo(repo)
	if err != nil {
		return "", err
	}
	return provider + "/" + c, nil
}

// MatchRepo reports whether a canonical "<provider>/<owner>/<name>" is
// admitted by an allow-list pattern. One implementation, used by the runner
// and the CLI.
//
// "*" alone admits every repository (the design's explicit ["*"] choice); any
// other pattern is provider-qualified and matches segment against segment, so
// "github/acme/*" admits github/acme/app and not github/acme/a/b, and
// "*/acme/*" is legal while a bare "acme/*" (which could not name a provider)
// is not. Within a segment "*" is a wildcard that never crosses a "/".
func MatchRepo(pattern, canonical string) bool {
	if pattern == "" || canonical == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	ps, cs := strings.Split(pattern, "/"), strings.Split(canonical, "/")
	if len(ps) != len(cs) {
		return false
	}
	for i := range ps {
		if !matchSeg(ps[i], cs[i]) {
			return false
		}
	}
	return true
}

// ValidPattern checks one allow_repos entry: "*", or a provider-qualified path
// whose segments each look like a repository segment with "*" wildcards. A
// pattern that cannot match a canonical repository is refused, so a typo fails
// at publish time (the CLI, task 1.6) instead of silently un-enrolling a
// repository; Decode applies the same check to a node it reads.
func ValidPattern(pattern string) error {
	if pattern == "*" {
		return nil
	}
	if pattern == "" {
		return fmt.Errorf("the pattern is empty (use \"*\" to allow every repository)")
	}
	segs := strings.Split(pattern, "/")
	if len(segs) < 3 {
		return fmt.Errorf("pattern %q must name a provider, an owner and a repository (as in \"github/acme/app\" or \"github/acme/*\")", pattern)
	}
	for i, s := range segs {
		if s == "" {
			return fmt.Errorf("pattern %q has an empty segment", pattern)
		}
		for j := 0; j < len(s); j++ {
			c := s[j]
			if c == '*' || c == '.' || c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
				continue
			}
			return fmt.Errorf("pattern %q: %q is not a repository character (names are lower-case; \"*\" is the wildcard)", pattern, string(c))
		}
		if i == 0 && s != "*" && !task.ValidProvider(s) {
			return fmt.Errorf("pattern %q: %q is not a provider kind", pattern, s)
		}
	}
	if strings.HasSuffix(segs[len(segs)-1], ".git") {
		return fmt.Errorf("pattern %q ends in .git: a canonical repository never does (task.CanonicalRepo trims it)", pattern)
	}
	return nil
}

// matchSeg matches one pattern segment against one canonical segment. "*"
// matches any run of characters within the segment (a greedy scan with
// backtracking, so "service-*" and "a*b*c" both work); everything else is
// literal.
func matchSeg(pattern, s string) bool {
	for {
		i := strings.IndexByte(pattern, '*')
		if i < 0 {
			return pattern == s
		}
		if !strings.HasPrefix(s, pattern[:i]) {
			return false
		}
		s = s[i:]
		pattern = pattern[i+1:]
		if pattern == "" {
			return true
		}
		// Try each position for the rest of the pattern.
		for k := 0; k <= len(s); k++ {
			if matchSeg(pattern, s[k:]) {
				return true
			}
		}
		return false
	}
}
