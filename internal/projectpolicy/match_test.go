package projectpolicy

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
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
		// The second argument is read as a repository, so a "*" in it is the
		// literal character the pattern's own "*" has to match -- and a
		// wildcard that aligns with it is still a wildcard: it stands for any
		// run, "*app" included, and "*" admits "*app" whole. Reading the
		// pattern's "*" as that literal consumes the wildcard, so
		// github/acme/* stops admitting a name it does admit, which is what
		// `repos rm github/acme/*app` reads when github/acme/* remains.
		{"github/acme/*", "github/acme/*app", true},
		{"github/*", "github/*app", true},
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

// TestMatchRepoAdmitsASubjectThatCarriesAStar: the second argument of MatchRepo
// is read as a repository by the runner, but by the CLI's admittedBy it is a
// pattern being removed ("github/acme/*" taken off the list), and a pattern can
// hold a star of its own. A wildcard admits it — the entry github/acme/* admits
// every repository in acme, so it admits the pattern for acme's repositories —
// and the matcher has to say so: comparing the pattern's "*" to the subject's
// "*" as equal bytes consumed the pattern's only star and recorded no backtrack
// point, so github/acme/* did not admit github/acme/*, and `fugaro project
// repos rm github/acme/*` read the removal as one that refuses runs when a
// wildcard was still on the list.
func TestMatchRepoAdmitsASubjectThatCarriesAStar(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		want             bool
	}{
		{"github/acme/*", "github/acme/*", true},
		{"github/acme/*", "github/acme/*ba", true},
		{"github/*/*", "github/*/*", true},
		{"github/ac*/*", "github/acme/*", true},
		{"github/acme/app", "github/acme/*", false},  // no wildcard in the entry
		{"github/*/app", "github/acme/*", false},     // the middle segment must be the literal "*"
		{"bitbucket/acme/*", "github/acme/*", false}, // another provider
	} {
		if got := MatchRepo(tc.pattern, tc.subject); got != tc.want {
			t.Errorf("MatchRepo(%q, %q) = %v, want %v", tc.pattern, tc.subject, got, tc.want)
		}
	}
}

// MatchRepo's second argument is a repository, and `repos rm` reads an
// allow-list *pattern* with it (internal/cli's admittedBy) to ask whether the
// list left by a removal still admits everything that entry admitted. A
// pattern is not a repository (no canonical repository carries a "*"), but as
// a string it is all letters, "-", "." and "*", so the matcher meets a "*" in
// the subject as well as in the pattern — and a "*" in the *subject* is a
// character like any other.
//
// matchSeg tested literal equality before its wildcard case, so when both
// positions held "*" it consumed them as a matching pair and never opened the
// wildcard: matchSeg("a*", "a*b") was false, and MatchRepo("github/*/*",
// "github/acme/*-web") with it. An operator removing github/acme/*-web while
// github/*/* was on the list was told "its runs are refused from now on" —
// about a security control the removal did not take away (the review of this
// change).
//
// Nothing here is an admission bypass: a canonical repository can never
// contain "*" (task.CanonicalRepo's own rule), so the cases below are the
// removal warning's reading and no run's admission.
func TestMatchRepoReadsAStarInTheSubject(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		want             bool
	}{
		{"github/*/*", "github/acme/*-web", true},
		{"github/ac*/*", "github/acme/*-web", true},
		{"github/acme/*-web", "github/acme/*-web", true}, // the entry admits itself
		{"github/*/*", "github/*/*", true},
		{"github/a*", "github/a*b", true}, // the segment case in the small
		{"github/*-web/*", "github/acme/*-web", false},
		{"github/*/app", "github/acme/*-web", false},
		{"github/acme/*", "github/acme/*-web", true},
	} {
		if got := MatchRepo(tc.pattern, tc.subject); got != tc.want {
			t.Errorf("MatchRepo(%q, %q) = %v, want %v", tc.pattern, tc.subject, got, tc.want)
		}
	}
	for _, tc := range []struct{ pattern, subject string }{
		{"a*", "a*b"},
		{"*", "*"},
		{"a*b", "a*b"},
		{"*b", "a*b"},
	} {
		if !matchSeg(tc.pattern, tc.subject) {
			t.Errorf("matchSeg(%q, %q) = false, want true: a \"*\" in the subject is a character, not the pattern's wildcard", tc.pattern, tc.subject)
		}
	}
}

func TestValidPattern(t *testing.T) {
	for _, ok := range []string{"*", "github/acme/app", "github/acme/*", "*/acme/*", "github/acme/service-*",
		// A repository path may nest: Canonical admits "acme/group/app"
		// (TestCanonical pins it), and MatchRepo compares segment against
		// segment, so this entry admits exactly bitbucket/acme/group/app. It is
		// not a longer spelling of "bitbucket/acme/group/*" — that is three
		// segments and matches nothing of the four-segment canonical — which is
		// why a cap of three was the wrong guard: it refused the entry that
		// admits the repository and left the wildcard form as the only way to
		// allow-list one (the review finding on this change).
		"bitbucket/group/sub/app",
		"bitbucket/acme/group/app",
		"bitbucket/acme/group/*",
	} {
		if err := ValidPattern(ok); err != nil {
			t.Errorf("ValidPattern(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/app.git", "github/acme/a b", "github//app", "github/acme/",
		"github/../app", "github/./app", "github/acme/..", "github/acme/.",
		// The first segment is a git provider *kind*, not any lower-case word:
		// task.CanonicalRepo takes a repository's kind from its own
		// git.provider, which config.Providers lists, so a pattern spelled with
		// anything else admits no repository. Accepting it published an entry
		// that silently enrolled nothing (the review finding on this check),
		// which is the one failure the function exists to prevent.
		"githubb/acme/app", "gitlab/acme/app", "gcp/acme/*", "githu*/acme/app"} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%q) = nil, want an error", bad)
		}
	}
	// Every kind a canonical repository may carry is accepted, and the message
	// for a kind that is not one names them: the operator's fix is the word,
	// not the shape (test.CanonicalRepo accepts "githubb" as a slug).
	for _, kind := range config.Providers {
		if err := ValidPattern(kind + "/acme/app"); err != nil {
			t.Errorf("ValidPattern(%q) = %v, want nil", kind+"/acme/app", err)
		}
	}
	err := ValidPattern("githubb/acme/app")
	if err == nil || !strings.Contains(err.Error(), strings.Join(config.Providers, ", ")) {
		t.Errorf("the refusal of a misspelt kind does not name the kinds: %v", err)
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
		// A "*" in the *subject* is an ordinary character to the pattern's
		// star, but not to a literal comparison: the pattern's star is never
		// literal, so it has to be recognized as a wildcard even when the
		// byte it sits on is the subject's own star. Reading it literally
		// instead consumed the pattern's only star and left no backtrack
		// point, so "*" refused "*ba".
		{"*", "*ba", true},
		{"*", "*", true},
		{"*a", "*ba", true},
		{"*b", "*b", true},
		{"a*b", "*ab", false}, // the subject's star is not the pattern's "a"
		// A "*" in the name is an ordinary character, not a wildcard: s is
		// not always a repository name. MatchRepo's callers read a pattern as
		// its second argument too — admittedBy (internal/cli/project_cmd.go)
		// asks whether the runs of a removed entry are still admitted — so the
		// wildcard case must be tested before the literal one. A star is a star
		// even where the name has a literal "*" under it: "*" admits "*app"
		// (the star takes the whole run) and "*b" admits "*ab" (it takes "*a").
		// Testing equality first spends the wildcard on one character and
		// loses it, and every one of these answered false.
		{"*", "*b", true},
		{"a*", "a*b", true},
		{"a*b", "a*b", true},
		{"*a", "*a", true},
		{"a*", "a*bc", true},
		{"a*", "ab*c", true},
		{"*", "*app", true},
		{"*b", "*ab", true},
	} {
		if got := matchSeg(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchSeg(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// refMatchSeg is a glob matcher written for its obviousness rather than for its
// cost: "*" matches any run of characters (the empty run included) and every
// other character is a literal, in the pattern and in the name alike.
// matchSeg's table cases are the ones a reader thinks of; this is what settles
// the ones nobody thought of.
func refMatchSeg(pattern, s string) bool {
	if pattern == "" {
		return s == ""
	}
	if pattern[0] == '*' {
		for k := 0; k <= len(s); k++ {
			if refMatchSeg(pattern[1:], s[k:]) {
				return true
			}
		}
		return false
	}
	return s != "" && pattern[0] == s[0] && refMatchSeg(pattern[1:], s[1:])
}

// TestMatchSegAgreesWithAReferenceGlob: every pattern and every name over
// {a, b, *} up to four characters — 121 of each, 14641 pairs — against a
// reference matcher. The alphabet includes "*" on purpose: matchSeg's second
// argument is a repository *or* a pattern (admittedBy reads a pattern being
// removed against the entries a removal leaves), so a "*" in the name is an
// ordinary character, and an implementation that tests literal equality before
// the wildcard silently loses the pattern's own star on exactly those inputs —
// 302 of the pairs below, which is the number this test printed the last time
// the two cases were the other way round.
func TestMatchSegAgreesWithAReferenceGlob(t *testing.T) {
	var all []string
	for n := 0; n <= 4; n++ {
		all = append(all, stringsOfLength(n)...)
	}
	bad := 0
	for _, pattern := range all {
		for _, name := range all {
			want := refMatchSeg(pattern, name)
			if got := matchSeg(pattern, name); got != want {
				bad++
				if bad <= 10 {
					t.Errorf("matchSeg(%q, %q) = %v, want %v", pattern, name, got, want)
				}
			}
		}
	}
	if bad > 10 {
		t.Errorf("%d pairs disagree with the reference glob, in all", bad)
	}
}

// stringsOfLength is every string of length n over the alphabet {a, b, *},
// the empty string included at n = 0: the exhaustive set the test above
// compares against, rather than a sample of it.
func stringsOfLength(n int) []string {
	if n == 0 {
		return []string{""}
	}
	var out []string
	for _, s := range stringsOfLength(n - 1) {
		for _, c := range []string{"a", "b", "*"} {
			out = append(out, s+c)
		}
	}
	return out
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
	for _, bad := range []string{"github/acme/" + strings.Repeat("a", MaxPatternLen), "*" + strings.Repeat("a", MaxPatternLen)} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%d characters) = nil, want an error: the pattern is too long", len(bad))
		}
	}
}
