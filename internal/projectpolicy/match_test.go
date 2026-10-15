package projectpolicy

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/task"
)

func TestCanonical(t *testing.T) {
	for _, tc := range []struct {
		provider, repo, want string
		ok                   bool
	}{
		{"github", "acme/app", "github/acme/app", true},
		{"github", "Acme/App.git", "github/acme/app", true},
		{"bitbucket", "acme/group/app", "bitbucket/acme/group/app", true},
		{"github", "acme", "", false},        // no owner/name
		{"github", "https://x/y", "", false}, // a URL
		{"github", "acme/.git", "", false},   // a repository named .git
		{"github", "acme/../app", "", false}, // a .. segment
		{"GitHub", "acme/app", "", false},    // a provider is a kind, not a name
		{"", "acme/app", "", false},          // no provider
		{"git hub", "acme/app", "", false},   // not a kind
		{"github", "acme/app ", "", false},   // trailing space
	} {
		got, err := Canonical(tc.provider, tc.repo)
		if tc.ok != (err == nil) {
			t.Errorf("Canonical(%q, %q) err = %v, want ok=%v", tc.provider, tc.repo, err, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("Canonical(%q, %q) = %q, want %q", tc.provider, tc.repo, got, tc.want)
		}
	}
}

func TestMatchRepo(t *testing.T) {
	for _, tc := range []struct {
		pattern, canonical string
		want               bool
	}{
		{"*", "github/acme/app", true},
		{"*", "bitbucket/acme/app", true},
		{"github/acme/app", "github/acme/app", true},
		{"github/acme/*", "github/acme/app", true},
		{"github/acme/*", "github/acme/a/b", false}, // a wildcard never crosses a "/"
		{"github/*/app", "github/acme/app", true},
		{"*/acme/*", "github/acme/app", true}, // P5: a provider wildcard is legal
		{"*/acme/*", "bitbucket/acme/app", true},
		{"*/acme/*", "github/other/app", false},
		{"github/acme/service-*", "github/acme/service-web", true}, // the design's prefix glob (§4.3)
		{"github/acme/service-*", "github/acme/web", false},
		{"github/acme/*", "bitbucket/acme/app", false}, // cross-provider
		{"github/acme/app", "github/acme/app-web", false},
		{"github/acme", "github/acme/app", false}, // a bare acme/* is not a pattern
		{"github/acme/app", "", false},
		{"", "github/acme/app", false},
	} {
		if got := MatchRepo(tc.pattern, tc.canonical); got != tc.want {
			t.Errorf("MatchRepo(%q, %q) = %v, want %v", tc.pattern, tc.canonical, got, tc.want)
		}
	}
}

func TestMatchRepoWildcardSegmentsAreGreedy(t *testing.T) {
	for _, tc := range []struct{ pattern, canonical string }{
		{"github/*-web/*", "github/acme-web/app"}, // a wildcard before a literal inside a segment
		{"github/a*b*c", "github/axxbyyc"},        // two wildcards in one segment
		{"github/*", "github/acme-web"},           // a whole segment
	} {
		if !MatchRepo(tc.pattern, tc.canonical) {
			t.Errorf("MatchRepo(%q, %q) = false, want true", tc.pattern, tc.canonical)
		}
	}
	if MatchRepo("github/a*b*c", "github/axxbyy") {
		t.Error("a pattern with an unmatched literal suffix must not match")
	}
}

func TestValidPattern(t *testing.T) {
	for _, ok := range []string{"*", "github/acme/app", "github/acme/*", "*/acme/*", "github/acme/service-*", "bitbucket/group/sub/app"} {
		if err := ValidPattern(ok); err != nil {
			t.Errorf("ValidPattern(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/app.git", "github/acme/a b", "github//app", "github/acme/",
		"github/../app", "github/./app", "github/acme/..", "github/acme/."} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%q) = nil, want an error", bad)
		}
	}
}

// TestCanonicalAgreesWithTaskSlug is the property P5 exists to guarantee: for
// each spelling of one repository (case, ".git") Canonical returns one string,
// that string is what task.Slug hashes, and MatchRepo admits exactly the
// repository whose slug is task.Slug(provider, repo).
func TestCanonicalAgreesWithTaskSlug(t *testing.T) {
	const provider = "github"
	spellings := []string{"acme/app", "Acme/App", "ACME/APP.git"}
	var canonical string
	var slug string
	for i, repo := range spellings {
		c, err := Canonical(provider, repo)
		if err != nil {
			t.Fatalf("Canonical(%q, %q): %v", provider, repo, err)
		}
		s, err := task.Slug(provider, repo)
		if err != nil {
			t.Fatalf("task.Slug(%q, %q): %v", provider, repo, err)
		}
		if i == 0 {
			canonical, slug = c, s
		} else if c != canonical || s != slug {
			t.Errorf("%q: Canonical %q / slug %q, want %q / %q", repo, c, s, canonical, slug)
		}
	}
	if canonical != "github/acme/app" {
		t.Errorf("Canonical = %q, want github/acme/app", canonical)
	}
	// What MatchRepo admits on the allow-list is the repository the runner
	// then holds a slug for: the wildcard that covers it, and only that one.
	if !MatchRepo("github/acme/*", canonical) {
		t.Errorf("MatchRepo(%q, %q) must admit the repository with slug %q", "github/acme/*", canonical, slug)
	}
	if MatchRepo("github/other/*", canonical) {
		t.Errorf("MatchRepo(%q, %q) must not admit the repository with slug %q", "github/other/*", canonical, slug)
	}
}

// TestMatchSegmentIsLinear: the matcher used to backtrack over each "*", so a
// pattern with n stars against a name that does not match took about 2^n steps
// (six stars and a sixty-character name took four seconds; eight did not finish
// in two minutes). The allow-list is a security boundary: a pattern nobody
// meant to write must not be able to wedge the runner or the CLI, so the
// matcher is linear and the wildcard count is capped as well.
func TestMatchSegmentIsLinear(t *testing.T) {
	pattern := strings.Repeat("*a", 20) + "*b" // 21 stars
	name := strings.Repeat("a", 60)
	start := time.Now()
	done := make(chan bool, 1)
	go func() { done <- matchSeg(pattern, name) }()
	select {
	case got := <-done:
		if got {
			t.Errorf("matchSeg(%q, %q) = true, want false: the pattern ends in a literal the name does not have", pattern, name)
		}
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Errorf("matchSeg took %v for 21 stars against %d characters, want well under 100ms: the matcher must not backtrack", d, len(name))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("matchSeg did not finish in 5s for 21 stars against %d characters: it is exponential again", len(name))
	}
	// Every one of these must still be answered correctly by a linear matcher.
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"*a*b*c", "aabbbc", true},  // stars that must backtrack to match
		{"*a*b*c", "aabbbx", false}, // and to refuse
		{"**", "anything", true},    // adjacent stars are one wildcard
		{"", "x", false},            // no pattern, no match
		{"a*", "a", true},           // a star matching the empty run
		{"*", "", true},             // and against an empty name
		{"*b", "ab", true},          // a star that must consume the prefix
		{"*b", "ba", false},
	} {
		if got := matchSeg(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchSeg(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// TestValidPatternCapsWildcards: the matcher is linear now, so this is not
// about its cost; it is that a pattern with a dozen wildcards is a typo, not an
// intent, and refusing it at publish time is the check this function exists for.
func TestValidPatternCapsWildcards(t *testing.T) {
	ok := "github/acme/" + strings.Repeat("a*", maxPatternStars)
	if err := ValidPattern(ok); err != nil {
		t.Errorf("ValidPattern(%q) = %v, want nil: the cap itself is allowed", ok, err)
	}
	for _, bad := range []string{
		"github/acme/" + strings.Repeat("*", maxPatternStars+1),
		"github/acme/" + strings.Repeat("*a", maxPatternStars) + "a*",
		"*/" + strings.Repeat("*", maxPatternStars+1) + "/*",
	} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%q) = nil, want an error: more than %d wildcards", bad, maxPatternStars)
		}
	}
	for _, bad := range []string{"github/acme/" + strings.Repeat("a", maxPatternLen), "*" + strings.Repeat("a", maxPatternLen)} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%d characters) = nil, want an error: the pattern is too long", len(bad))
		}
	}
}

// TestAdmitRepoEmptyListIsDenyAll: an empty allow_repos list admits nothing,
// and that is not the same thing as an installation with no policy — the
// caller decides which of the two it is holding, and only the published-policy
// case reaches this function at all (internal/runner's admissionCheck returns
// before it when the policy node is absent). The design calls the distinction
// out by name ("Default empty means no repository is enrolled", P4), because
// the two are one line apart in the code that reads them: reading the empty
// list as "no restriction" would turn a policy that enrolls nobody into one
// that enrolls everybody, on every repository at once. Red if AdmitRepo ever
// treats "no patterns" as "matches".
func TestAdmitRepoEmptyListIsDenyAll(t *testing.T) {
	for name, patterns := range map[string][]string{
		"nil":          nil,
		"empty":        {},
		"blank string": {""},
	} {
		for _, repo := range []string{"github/acme/app", "github/acme/sandbox", "*", ""} {
			if AdmitRepo(patterns, repo) {
				t.Errorf("%s: AdmitRepo(%q, %q) = true, want false: an empty allow-list admits nothing", name, patterns, repo)
			}
		}
	}
	// The wildcard the design does use for "every repository" is an explicit
	// entry, never the absence of one.
	if !AdmitRepo([]string{"*"}, "github/acme/app") {
		t.Error(`AdmitRepo(["*"], "github/acme/app") = false, want true: ["*"] is the written-down spelling of "every repository"`)
	}
	// One entry among others decides on its own: a list is a union.
	if !AdmitRepo([]string{"github/other/repo", "github/acme/*"}, "github/acme/app") {
		t.Error("a list admits a repository its second pattern admits")
	}
	if AdmitRepo([]string{"github/other/repo", "bitbucket/acme/*"}, "github/acme/app") {
		t.Error("a list admits nothing beyond its patterns: the provider is part of the match")
	}
}

// TestNotAdmittedWordsTheDenyAllCase: the refusal is the run's and the
// launcher's, worded once (its doc says why), and the empty list is the case it
// has to say out loud — an operator who reads "allow_repos is []" would go
// looking for a list that is not there. Red if the empty case stops naming
// itself, or if the message stops telling the operator the command that fixes
// it.
func TestNotAdmittedWordsTheDenyAllCase(t *testing.T) {
	empty := NotAdmitted(nil, "github/acme/app")
	if !strings.Contains(empty, "empty") || !strings.Contains(empty, "admits no repository") {
		t.Errorf("NotAdmitted(nil, ...) = %q, want the deny-all case stated rather than quoted as []", empty)
	}
	if !strings.Contains(empty, "github/acme/app") {
		t.Errorf("NotAdmitted(nil, ...) = %q, want the repository named", empty)
	}
	if !strings.Contains(empty, "fugaro project repos add github/acme/app") {
		t.Errorf("NotAdmitted(nil, ...) = %q, want the command that enrolls it", empty)
	}
	// A non-empty list is quoted as it is: the operator sees which patterns
	// were there, which is the whole difference between "I forgot to enroll
	// it" and "I enrolled it under another name".
	listed := NotAdmitted([]string{"github/other/repo"}, "github/acme/app")
	if !strings.Contains(listed, `["github/other/repo"]`) {
		t.Errorf("NotAdmitted(list, ...) = %q, want the list quoted", listed)
	}
	if !strings.Contains(listed, "github/acme/app") {
		t.Errorf("NotAdmitted(list, ...) = %q, want the repository named", listed)
	}
}
