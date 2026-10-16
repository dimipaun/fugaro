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

// TestAdmitRepoAndNotAdmitted is the allow-list's own decision, tested where
// it lives. Both readers of it reach it from other packages — the runner's
// admission check, before any git credential exists, and the launcher's
// advisory twin — so the two rules that are only this pair's to state have no
// test of their own without this one: an empty list is deny-all (not "no
// policy", which is its caller's distinction to make), and the refusal names
// the list and the command that enrolls the repository. A change to either
// would otherwise show up as a moved string in another package's test rather
// than as a failure here.
//
// Mutation (verified, run and restored): admit everything from an empty list
// (`if len(patterns) == 0 { return true }` in AdmitRepo) — the deny-all case
// fails; and drop the empty-list branch from NotAdmitted so it prints the
// literal `[]` — the wording case fails.
func TestAdmitRepoAndNotAdmitted(t *testing.T) {
	allow := []string{"github/acme/*", "bitbucket/other/app"}
	for _, tc := range []struct {
		repo string
		want bool
	}{
		{"github/acme/app", true}, // through the pattern
		{"github/acme/anything", true},
		{"bitbucket/other/app", true}, // the concrete entry, another provider
		{"github/other/app", false},   // another owner
		{"bitbucket/other/other", false},
		{"github/acme", false}, // not canonical
		{"", false},
	} {
		if got := AdmitRepo(allow, tc.repo); got != tc.want {
			t.Errorf("AdmitRepo(%v, %q) = %v, want %v", allow, tc.repo, got, tc.want)
		}
	}
	// The wildcard is the design's explicit every-repository choice, so the
	// deny-all rule below is about an EMPTY list and not about this one.
	if !AdmitRepo([]string{"*"}, "github/acme/app") || !AdmitRepo([]string{"*"}, "bitbucket/other/app") {
		t.Error("the wildcard entry did not admit a repository")
	}
	// A published policy with an empty allow-list admits nothing: deny-all, and
	// the same for a list that is nil, which is what a node with no key decodes
	// to.
	for name, list := range map[string][]string{"nil": nil, "empty": {}} {
		if AdmitRepo(list, "github/acme/app") || AdmitRepo(list, "") {
			t.Errorf("%s: an empty allow-list admitted a repository: a published policy with an empty list is deny-all", name)
		}
		msg := NotAdmitted(list, "github/acme/app")
		for _, want := range []string{
			"empty, and a published policy with an empty allow-list admits no repository at all",
			"fugaro project repos add github/acme/app",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: the refusal does not say %q: %s", name, want, msg)
			}
		}
	}
	// With entries on the list the refusal prints them — the operator has to
	// see what the list actually says to know how far off the name is.
	msg := NotAdmitted(allow, "github/other/app")
	if !strings.Contains(msg, `"github/acme/*"`) || !strings.Contains(msg, "bitbucket/other/app") {
		t.Errorf("the refusal does not print the list: %s", msg)
	}
	if !strings.Contains(msg, "fugaro project repos add github/other/app") {
		t.Errorf("the refusal does not name the repository to enroll in the command that enrolls it: %s", msg)
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

// TestMatchRepoWildcardIsNeverLiteral is the bug the allow-list matcher had:
// matchSeg tested literal equality before it tested the "*", so a "*" in the
// pattern that lined up with a "*" character in its second argument was
// consumed as that literal and the wildcard was lost.
//
// That second argument is a repository to the runner's admission check, but it
// need not be one to every caller: `project repos rm` asks the same matcher
// whether a pattern being removed is still covered by an entry that remains
// (internal/cli's admittedBy), and there the second argument can itself be a
// pattern. MatchRepo("github/acme/*", "github/acme/*app") returned false, so
// removing github/acme/*app from a list that also holds github/acme/* printed
// "its runs are refused from now on" although the wildcard still admits every
// repository the removed entry did. A "*" in the pattern is always a wildcard;
// a literal comparison must not pre-empt it.
func TestMatchRepoWildcardIsNeverLiteral(t *testing.T) {
	for _, tc := range []struct {
		pattern, canonical string
		want               bool
	}{
		// The reported case: the pattern's "*" must swallow the second
		// argument's literal "*".
		{"github/acme/*", "github/acme/*app", true},
		{"github/acme/*", "github/acme/*", true},
		{"github/acme/*", "github/acme/*/*", false}, // a wildcard never crosses a "/"
		{"*/acme/*", "github/acme/*app", true},      // and it holds segment by segment
		{"github/*", "github/acme*", true},
		{"github/*/app", "github/*/app", true},
		// A pattern whose literal "*" ... does not match a pattern that is
		// wider: the removed entry did not admit every repository the
		// remaining one does.
		{"github/acme/*app", "github/acme/*", false},
		{"github/acme/*app", "github/acme/*/app", false}, // nor does a "/" appear
		// A literal "*" in the pattern (there is no escape in this syntax, so
		// every "*" is a wildcard) still matches the same characters.
		{"github/acme/*app", "github/acme/*app", true},
	} {
		if got := MatchRepo(tc.pattern, tc.canonical); got != tc.want {
			t.Errorf("MatchRepo(%q, %q) = %v, want %v", tc.pattern, tc.canonical, got, tc.want)
		}
	}
	// The same order inside one segment, where the star match lives. Literal
	// stars on both sides must not be read as each other.
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"*", "*", true},
		{"a*", "a*", true},
		{"*b", "*b", true},
		{"*b", "*a", false},
		{"a*b", "a*b", true},
		{"*app", "*app", true},
	} {
		if got := matchSeg(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchSeg(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
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
