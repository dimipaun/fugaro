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

// AdmitRepo reports whether any entry of an allow_repos list admits a
// canonical "<provider>/<owner>/<name>". An empty list admits nothing: a
// policy that is published with an empty allow_repos is deny-all, which is a
// different thing from an installation that has published no policy at all
// (the caller decides which of the two it is holding).
func AdmitRepo(patterns []string, canonical string) bool {
	for _, p := range patterns {
		if MatchRepo(p, canonical) {
			return true
		}
	}
	return false
}

// NotAdmitted is the refusal of a repository a published policy does not
// admit, worded once for both of its readers: the run's admission check
// (internal/runner, before any git credential exists) and the launcher's
// advisory twin (internal/cli, before any execution exists, plan task 1.5
// step 4). "The same message" is a property worth an implementation rather
// than a copied string: the two refusals must not drift apart, since an
// operator who sees the launcher's refusal and a run's is reading about one
// policy. What differs between the two — what stops, and when — is each
// caller's own sentence, appended to this one.
//
// allow is the policy's own allow_repos, and an empty list is the deny-all
// case the text says out loud: a policy that is published with no repository
// on its list admits nothing, which is not the same as an installation that
// has published no policy at all (the caller decides which of the two it
// holds, and only this one refuses).
func NotAdmitted(allow []string, canonical string) string {
	what := fmt.Sprintf("%q", allow)
	if len(allow) == 0 {
		what = "empty, and a published policy with an empty allow-list admits no repository at all"
	}
	return fmt.Sprintf("the project's policy does not admit %s: allow_repos is %s. The project's owner adds the repository with 'fugaro project repos add %s'", canonical, what, canonical)
}

const (
	// maxPatternStars is the most wildcards one allow_repos entry may carry.
	// The matcher is linear, so this is not about its cost: a pattern with a
	// dozen "*" is a typo rather than an intent, and the allow-list is what
	// decides which repositories a run may touch.
	maxPatternStars = 4
	// maxPatternLen bounds an allow_repos entry, so a pattern cannot be used to
	// grow the policy node without limit.
	maxPatternLen = 255
)

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
	if len(pattern) > maxPatternLen {
		return fmt.Errorf("the pattern is %d characters; at most %d are allowed", len(pattern), maxPatternLen)
	}
	segs := strings.Split(pattern, "/")
	if len(segs) < 3 {
		return fmt.Errorf("pattern %q must name a provider, an owner and a repository (as in \"github/acme/app\" or \"github/acme/*\")", pattern)
	}
	stars := 0
	for i, s := range segs {
		if s == "" {
			return fmt.Errorf("pattern %q has an empty segment", pattern)
		}
		stars += strings.Count(s, "*")
		// A "." or ".." cannot match: task.CanonicalRepo refuses such a
		// repository, so a pattern carrying one would silently enroll
		// nothing (the failure this function exists to prevent).
		if s == "." || s == ".." {
			return fmt.Errorf("pattern %q: %q is not a repository segment", pattern, s)
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
	if stars > maxPatternStars {
		return fmt.Errorf("pattern %q has %d wildcards; at most %d are allowed (a pattern with more is a typo, not an intent)", pattern, stars, maxPatternStars)
	}
	if strings.HasSuffix(segs[len(segs)-1], ".git") {
		return fmt.Errorf("pattern %q ends in .git: a canonical repository never does (task.CanonicalRepo trims it)", pattern)
	}
	return nil
}

// matchSeg matches one pattern segment against one canonical segment. "*"
// matches any run of characters within the segment, so "service-*" and "a*b*c"
// both work; everything else is literal.
//
// It is the classic two-pointer star match: the last "*" seen consumes as
// little as it can and is stretched one character at a time only when the rest
// of the pattern fails. That is at most len(pattern)*len(s) steps, where a
// backtracking scan of every star is exponential in their number (a pattern is
// attacker-chosen text on a node, so the cost must not be).
func matchSeg(pattern, s string) bool {
	var (
		p, i     int // the positions being compared
		star, at int // the last "*" and how much of s it consumes
		has      bool
	)
	for i < len(s) {
		switch {
		case p < len(pattern) && pattern[p] == s[i]:
			p++
			i++
		case p < len(pattern) && pattern[p] == '*':
			star, at, has = p, i, true
			p++
		case has:
			// The last "*" takes one more character and the rest of the
			// pattern is tried again from there.
			at++
			i = at
			p = star + 1
		default:
			return false
		}
	}
	// Whatever is left of the pattern may only be trailing stars.
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
