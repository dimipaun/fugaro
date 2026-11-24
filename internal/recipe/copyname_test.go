package recipe

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The copy names here are spelled out rather than derived: they are what a
// project store holds, and internal/task's TestCopyNameMatchesSlug is what holds
// this package's spelling to task.Slug's own.
func TestCopyName(t *testing.T) {
	for _, tc := range []struct {
		name, provider, repo, want string
	}{
		{"review", "github", "acme/app", "review.acme-app-dc4d4e59"},
		{"default", "github", "acme/APP.git", "default.acme-app-dc4d4e59"},
		{"review", "bitbucket", "acme/app", "review.acme-app-e5c4c0c8"},
		{"review", "github", "_/_", "review.repo-ceeb753f"},
	} {
		got := CopyName(tc.name, tc.provider, tc.repo)
		if got != tc.want {
			t.Errorf("CopyName(%q, %q, %q) = %q, want %q", tc.name, tc.provider, tc.repo, got, tc.want)
		}
		if !IsCopyName(got) {
			t.Errorf("IsCopyName(%q) = false, want true", got)
		}
	}
}

// A slug's readable half merges distinct repositories, so the hash half is what
// keeps their copies apart (D3302's amendment).
func TestCopyNameKeepsCollidingSlugsApart(t *testing.T) {
	a := CopyName("review", "github", "acme/app-web")
	b := CopyName("review", "github", "acme-app/web")
	if a != "review.acme-app-web-6387380c" || b != "review.acme-app-web-64841666" {
		t.Fatalf("copies are %q and %q, want the two pinned names", a, b)
	}
	const shared = "review.acme-app-web-"
	if !strings.HasPrefix(a, shared) || !strings.HasPrefix(b, shared) {
		t.Fatalf("the two copies share no readable half: %q and %q", a, b)
	}
	if a == b {
		t.Fatalf("two repositories whose readable halves merge got one name: %q", a)
	}
	if a[len(a)-8:] == b[len(b)-8:] {
		t.Fatalf("the hash halves do not tell the two repositories apart: %q and %q", a, b)
	}
	if !IsCopyName(a) || !IsCopyName(b) {
		t.Errorf("IsCopyName refuses a copy this package built: %q, %q", a, b)
	}
}

func TestCopyNameAtMost96(t *testing.T) {
	if MaxCopyName != 96 {
		t.Fatalf("MaxCopyName = %d, want 96", MaxCopyName)
	}
	// The longest recipe name, the longest readable half (a slug's readable
	// part is 46), the dot, the hyphen and 8 hex.
	got := CopyName(strings.Repeat("n", 40), "github", "a/"+strings.Repeat("b", 44))
	if len(got) != MaxCopyName {
		t.Fatalf("the longest copy is %d characters (%q), want %d", len(got), got, MaxCopyName)
	}
	if !IsCopyName(got) {
		t.Errorf("IsCopyName(%q) = false, want true", got)
	}
	if IsCopyName(got+"a") || IsCopyName("n"+got) {
		t.Errorf("IsCopyName accepts a copy one character over the limit")
	}
	// A recipe name of 41 is over its own limit, however the name is spelled:
	// the half is built here rather than written out so that this line holds no
	// bare "." literal, which the CI read map reads as the package's directory.
	overLongName := fmt.Sprintf("%s.%s-0123abcd", strings.Repeat("n", 41), strings.Repeat("b", 46))
	if IsCopyName(overLongName) {
		t.Errorf("IsCopyName accepts a name over the recipe-name limit")
	}
}

func TestLookupOrder(t *testing.T) {
	want := []string{"review.acme-app-dc4d4e59", "review"}
	if got := LookupOrder("review", "github", "acme/app"); !slices.Equal(got, want) {
		t.Errorf("LookupOrder(review, github, acme/app) = %q, want %q", got, want)
	}
	// A repository no slug can be built from has no copy, and a name that is
	// already a copy has no copy of its own: both resolve by the name alone,
	// as they did before the copies existed.
	for _, tc := range []struct{ name, provider, repo string }{
		{"review", "github", "acme"},
		{"review", "", "acme/app"},
		{"review.acme-app-dc4d4e59", "github", "acme/app"},
	} {
		if got := LookupOrder(tc.name, tc.provider, tc.repo); !slices.Equal(got, []string{tc.name}) {
			t.Errorf("LookupOrder(%q, %q, %q) = %q, want the name alone", tc.name, tc.provider, tc.repo, got)
		}
	}
}

func TestIsCopyName(t *testing.T) {
	for _, name := range []string{
		"review",                     // no dot at all: NameRE's grammar
		"review.",                    // no readable half
		".acme-app-dc4d4e59",         // no recipe name
		"review.acme-app",            // no hash
		"review.acme-app-dc4d4e5",    // 7 hex
		"review.acme-app-dc4d4e59x",  // 9
		"review.acme-app-DC4D4E59",   // upper case hex
		"Review.acme-app-dc4d4e59",   // upper case name
		"review.acme/app-dc4d4e59",   // a path, not a readable half
		"review..acme-app-dc4d4e59",  // two dots
		"review.acme-app-dc4d4e59\n", // a trailing newline is part of the name
		"review.-acme-app-dc4d4e59",  // a readable half starting with "-"
		"review.acme-app--dc4d4e59",  // an empty hash
		"review.acme app-dc4d4e59",   // a space
		"review.acme-app-dc4d4e59.a", // what a copy of a copy would look like
	} {
		if IsCopyName(name) {
			t.Errorf("IsCopyName(%q) = true, want false", name)
		}
	}
	for _, name := range []string{
		"review.acme-app-dc4d4e59",
		"default.a-b-01234567",
		"review.repo-ceeb753f", // the readable half task.Slug falls back to
	} {
		if !IsCopyName(name) {
			t.Errorf("IsCopyName(%q) = false, want true", name)
		}
	}
}

// NameRE is the first grammar and still refuses every dot; a copy is a second
// grammar beside it, never a wider first one.
func TestNameREStillRefusesADot(t *testing.T) {
	cp := CopyName("review", "github", "acme/app")
	if !IsCopyName(cp) {
		t.Fatalf("CopyName built %q, which IsCopyName refuses", cp)
	}
	for _, name := range []string{cp, "review.acme-app", "review.", ".x"} {
		if NameRE.MatchString(name) {
			t.Errorf("NameRE accepts the dotted name %q", name)
		}
	}
}

// A copy name is a recipe name, one dot and the slug halves, so what CopyName
// returns is always a name IsCopyName accepts -- and a name there is no copy of
// (not a recipe name, or a copy already) has none.
func TestCopyNameIsAlwaysACopyNameOrEmpty(t *testing.T) {
	for _, name := range []string{"", "Review", "re view", "re/cipe", "review.acme-app-dc4d4e59",
		"a" + strings.Repeat("b", 40)} {
		if got := CopyName(name, "github", "acme/app"); got != "" {
			t.Errorf("CopyName(%q, github, acme/app) = %q, want empty", name, got)
		}
	}
	got := CopyName("review", "github", "acme/app")
	if got == "" || !IsCopyName(got) {
		t.Errorf("CopyName(review, github, acme/app) = %q, want a copy name", got)
	}
}
