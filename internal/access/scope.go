package access

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// ScopeKind is what a scope covers: the whole project, every repository the
// published policy admits, or a list of repository patterns. The zero value
// covers nothing and is deliberately not one of the three: a scope nobody
// parsed must grant nothing rather than fall into the widest case.
type ScopeKind uint8

const (
	// ScopeNone is the zero scope: it covers no repository and no project
	// action.
	ScopeNone ScopeKind = iota
	// ScopeProject is the whole project: every repository in it, and the
	// project-level settings.
	ScopeProject
	// ScopeAllRepos is every repository the policy's allow_repos admits at the
	// moment of the check, including one enrolled later, and no project-level
	// setting.
	ScopeAllRepos
	// ScopeRepos is the repositories a list of canonical patterns admits.
	ScopeRepos
)

// Scope is a parsed scope: a kind and, for ScopeRepos, the canonical patterns
// it covers, sorted and deduplicated so two spellings of one scope are one
// value.
type Scope struct {
	// Kind is what the scope covers.
	Kind ScopeKind
	// Repos is the canonical patterns of a ScopeRepos scope, and empty for
	// every other kind.
	Repos []string
}

// ParseScope parses one scope value, the grammar of D1583 as the owner
// confirmed it:
//
//   - "project" and "all-repos" are whole scopes, spelled exactly once and
//     never mixed with repositories. They are reserved words, and because a
//     canonical repository always carries a provider and a "/" they can never
//     be a repository: `fugaro project repos add` cannot be given either
//     (F6).
//   - Anything else is a comma-separated list of canonical repository
//     patterns, canonicalised here and stored in that spelling: the ASCII
//     lower case the repository tree folds with (task.ASCIILower) and no
//     trailing ".git" (F17.4). A case or ".git" variant is normalised, never
//     stored as written, so a grant is checked against one spelling only.
//   - A lone "*" is refused: it is `all-repos`' meaning, and `all-repos` is
//     reserved to a project admin, so accepting "*" would let an all-repos
//     holder mint the scope they do not hold. A pattern names its provider,
//     so "*" in the provider segment is refused too — MatchRepo admits
//     "*/acme/*" for the allow-list, and a grant is narrower than that.
//   - "*" stays inside one path segment: `**` is refused rather than read as
//     a second wildcard, because a pattern that looks like it crosses "/" is
//     the one a reader would misread as covering a nested repository.
//
// The errors are the sentences a command shows: a person typing a scope is
// the reader, and none of them is a question (no-prompt rule 1).
func ParseScope(value string) (Scope, error) {
	switch strings.TrimSpace(value) {
	case "project":
		return Scope{Kind: ScopeProject}, nil
	case "all-repos":
		return Scope{Kind: ScopeAllRepos}, nil
	case "":
		return Scope{}, fmt.Errorf("the scope is empty: name %q, %q, or the repositories it covers (as in \"github/acme/app\")", "project", "all-repos")
	case "*":
		return Scope{}, fmt.Errorf("%q is not a scope: write %q for every repository (a project admin grants it) or name the repositories it covers (as in \"github/acme/app\")", "*", "all-repos")
	}
	var repos []string
	for _, part := range strings.Split(value, ",") {
		entry := strings.TrimSpace(part)
		switch entry {
		case "project", "all-repos":
			return Scope{}, fmt.Errorf("%q is a whole scope and cannot be mixed with repositories: write %q alone, or a list of repositories", entry, entry)
		case "":
			return Scope{}, fmt.Errorf("the scope %q has an empty entry: name a repository (as in \"github/acme/app\") or write %q", value, "all-repos")
		}
		pattern, err := canonicalPattern(entry)
		if err != nil {
			return Scope{}, err
		}
		repos = append(repos, pattern)
	}
	slices.Sort(repos)
	return Scope{Kind: ScopeRepos, Repos: slices.Compact(repos)}, nil
}

// canonicalPattern returns the stored spelling of one repository pattern, and
// refuses an entry that cannot be a grant. The spelling is the one the
// repository tree folds with (task.ASCIILower, not strings.ToLower, whose
// Unicode folding merges names that read as two) and without the ".git" a
// clone URL carries, which is what F17.4 requires: a case or ".git" variant of
// a repository is one repository, and a grant is written once.
//
// projectpolicy.ValidPattern does the rest — the pattern must name a provider,
// an owner and a repository, its characters must be ones a repository can
// carry, it may not end in ".git" and it may not carry more stars than a typo
// would — and this function adds the two refusals a grant needs on top of the
// allow-list's grammar: no lone "*" (that is "all-repos") and no "*" in the
// provider segment.
func canonicalPattern(entry string) (string, error) {
	if entry == "*" {
		return "", fmt.Errorf("%q is not a pattern a grant can carry: write %q for every repository (a project admin grants it) or name the provider (as in \"github/acme/app\")", "*", "all-repos")
	}
	pattern := strings.TrimSuffix(task.ASCIILower(entry), ".git")
	if err := projectpolicy.ValidPattern(pattern); err != nil {
		return "", fmt.Errorf("the scope entry %q: %w", entry, err)
	}
	if strings.SplitN(pattern, "/", 2)[0] == "*" {
		return "", fmt.Errorf("the scope entry %q names no provider: a pattern may wildcard a provider for the allow-list, and a grant names the repositories it covers, so write the provider (as in \"github/acme/*\")", entry)
	}
	for _, segment := range strings.Split(pattern, "/") {
		if strings.Contains(segment, "**") {
			return "", fmt.Errorf("the scope entry %q carries %q: %q matches inside one path segment and never crosses %q, and a doubled star reads as if it did", entry, "**", "*", "/")
		}
	}
	return pattern, nil
}

// Valid reports whether the scope is one this package built: a known kind, a
// repository list that is not empty, and every pattern one ParseScope would
// accept. It is the resolver's gate — a scope that fails it covers nothing, so
// a value that reached the node without passing through ParseScope (a
// hand-written one, a future format, a truncation) grants nobody anything
// rather than falling into a wider case.
func (s Scope) Valid() bool {
	switch s.Kind {
	case ScopeProject, ScopeAllRepos:
		return len(s.Repos) == 0
	case ScopeRepos:
		if len(s.Repos) == 0 {
			return false
		}
		for _, pattern := range s.Repos {
			if _, err := canonicalPattern(pattern); err != nil {
				return false
			}
		}
		return true
	}
	return false
}

// SubsetOf reports whether every repository grantee covers is also covered by
// grantor: it is the rule of F17.1, the "a grant is a subset of the grantor's
// scope" test a grant must pass, so nobody widens their own reach or hands out
// a scope wider than the one they hold.
//
// It is deliberately conservative — false when unsure — because a wrong true
// here is an escalation and a wrong false is a refusal a person can read. The
// rules, in the order they are decided:
//
//   - project covers everything; all-repos covers all-repos and any repository
//     list; nothing but project covers project.
//   - Two repository lists compare per pattern: a grantee pattern is covered
//     when some grantor pattern covers it, and a pattern covers another only
//     when they have the SAME number of segments — a segment count difference
//     is a nested repository path, and `github/acme/*` does not cover
//     `github/acme/a/b`.
//   - Segment against segment: equal, or the grantor's is `*`, or the
//     grantor's ends in a `*` with a literal prefix that the grantee's own
//     literal prefix extends. So `api-*` is covered by `api*` and `api*` is
//     not covered by `api-*` (the grantee matches "apiX", which the grantor
//     does not). A grantor segment with an interior star (`a*b*`) and a
//     grantor segment that is not a prefix pattern at all are both "unsure"
//     and answer false, as does an unparsable scope on either side.
func SubsetOf(grantee, grantor Scope) bool {
	if !grantee.Valid() || !grantor.Valid() {
		return false
	}
	switch grantor.Kind {
	case ScopeProject:
		return true
	case ScopeAllRepos:
		return grantee.Kind != ScopeProject
	case ScopeRepos:
		if grantee.Kind != ScopeRepos {
			return false
		}
		for _, want := range grantee.Repos {
			if !slices.ContainsFunc(grantor.Repos, func(have string) bool { return patternSubset(want, have) }) {
				return false
			}
		}
		return true
	}
	return false
}

// patternSubset reports whether every repository one canonical pattern covers
// is covered by another. The two must compare segment against segment, and
// only when the segment counts are equal: a longer pattern names a nested
// repository path, which no shorter pattern can admit.
func patternSubset(grantee, grantor string) bool {
	gs, hs := strings.Split(grantee, "/"), strings.Split(grantor, "/")
	if len(gs) != len(hs) {
		return false
	}
	for i := range gs {
		if !segmentSubset(gs[i], hs[i]) {
			return false
		}
	}
	return true
}

// segmentSubset reports whether every string one pattern segment matches is
// matched by another. Pure literals and a single trailing star are the only
// grantor forms it reasons about, which is what makes it sound: every string a
// grantee segment matches begins with the grantee's own literal prefix, so if
// the grantor's literal prefix is a prefix of that, the grantor matches all of
// them. Anything else (an interior star on the grantor's side, a suffix
// pattern like `*bc`) is refused rather than reasoned about.
func segmentSubset(grantee, grantor string) bool {
	if grantee == grantor {
		return true
	}
	if grantor == "*" {
		return true
	}
	prefix, ok := strings.CutSuffix(grantor, "*")
	if !ok || strings.Contains(prefix, "*") {
		return false
	}
	return strings.HasPrefix(literalPrefix(grantee), prefix)
}

// literalPrefix is the part of a pattern segment before its first star: the
// longest string every match of the segment starts with.
func literalPrefix(segment string) string {
	if i := strings.IndexByte(segment, '*'); i >= 0 {
		return segment[:i]
	}
	return segment
}

// KnownMatches returns the repositories of knownRepos a scope covers, in the
// order they were given.
//
// knownRepos are the repositories a machine knows — the ones its own config
// lists and the ones its runs have seen — spelled canonically; it is the
// caller's list, built with projectpolicy.Canonical, because a machine's
// config keys repositories by owner and name while a grant is written with a
// provider. The answer is what `fugaro user ls` prints beside a pattern, and
// it is deliberately about the machine's list: when allow_repos holds patterns
// the set of repositories cannot be enumerated at all, so the answer is
// labelled "known" and never claims to be every repository the pattern admits
// (F27). A scope this package did not parse covers nothing and matches
// nothing.
func KnownMatches(scope Scope, knownRepos []string) []string {
	if !scope.Valid() {
		return nil
	}
	var out []string
	for _, repo := range knownRepos {
		if scopeCovers(scope, repo) {
			out = append(out, repo)
		}
	}
	return out
}

// scopeCovers reports whether a scope covers a canonical repository. It is the
// pattern half of a decision and knows nothing about allow_repos: Effective
// applies that gate, because a scope never admits a repository the published
// policy does not (F27).
func scopeCovers(scope Scope, repo string) bool {
	if repo == "" {
		return false
	}
	switch scope.Kind {
	case ScopeProject, ScopeAllRepos:
		return true
	case ScopeRepos:
		return slices.ContainsFunc(scope.Repos, func(pattern string) bool {
			return projectpolicy.MatchRepo(pattern, repo)
		})
	}
	return false
}
