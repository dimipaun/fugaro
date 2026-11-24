package task

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// A repository's copy of a recipe is addressed by the same string its slug keys
// everything else by, so recipe.CopyName's readable half and hash prefix must
// not drift from Slug's. internal/recipe cannot call Slug (this package imports
// it for a task's recipe name), so it spells the derivation out and this is what
// holds the two spellings together -- which is also why the test lives here:
// internal/recipe's own tests importing this package would make every reader of
// the recipe format reach this one, and the CI read map with them.
func TestCopyNameMatchesSlug(t *testing.T) {
	for _, tc := range []struct{ provider, repo string }{
		{"github", "acme/app"},
		{"github", "acme/app-web"},
		{"github", "acme-app/web"},
		{"github", "acme/APP.git"},
		{"bitbucket", "acme/app"},
		{"fake", "acme/nested/group/app"},
		{"github", "a/" + strings.Repeat("b", 60)},
		{"github", "acme/.hidden"},
		{"github", "a/_b.c"},
		{"github", "_/_"},
	} {
		slug, err := Slug(tc.provider, tc.repo)
		if err != nil {
			t.Fatalf("Slug(%q, %q): %v", tc.provider, tc.repo, err)
		}
		readable, hash := slug[:len(slug)-17], slug[len(slug)-16:]
		want := "review." + readable + "-" + hash[:8]
		if got := recipe.CopyName("review", tc.provider, tc.repo); got != want {
			t.Errorf("recipe.CopyName(review, %q, %q) = %q, want %q", tc.provider, tc.repo, got, want)
		}
		if got := recipe.LookupOrder("review", tc.provider, tc.repo); !slices.Equal(got, []string{want, "review"}) {
			t.Errorf("recipe.LookupOrder(review, %q, %q) = %q, want %q", tc.provider, tc.repo, got, []string{want, "review"})
		}
	}
	// A repository this package refuses to key has no copy, so the resolution is
	// the name alone: the spelling is only held to Slug where Slug has an answer.
	for _, tc := range []struct{ provider, repo string }{
		{"", "acme/app"},
		{"GitHub", "acme/app"},
		{"github", ""},
		{"github", "acme"},
		{"github", "acme/../app"},
		{"github", "acme//app"},
		{"github", "acme/app/"},
		{"github", "https://github.com/acme/app"},
	} {
		if _, err := Slug(tc.provider, tc.repo); err == nil {
			t.Fatalf("Slug(%q, %q) is accepted, so this case pins no refusal", tc.provider, tc.repo)
		}
		if got := recipe.CopyName("review", tc.provider, tc.repo); got != "" {
			t.Errorf("recipe.CopyName(review, %q, %q) = %q, want empty", tc.provider, tc.repo, got)
		}
		if got := recipe.LookupOrder("review", tc.provider, tc.repo); !slices.Equal(got, []string{"review"}) {
			t.Errorf("recipe.LookupOrder(review, %q, %q) = %q, want the name alone", tc.provider, tc.repo, got)
		}
	}
}
