package projectpolicy

import (
	"testing"

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
	for _, bad := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/app.git", "github/acme/a b", "github//app", "github/acme/"} {
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
