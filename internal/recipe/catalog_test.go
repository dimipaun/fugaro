package recipe

import (
	"slices"
	"testing"
)

// TestCatalogIsTheStarterSet: the catalog holds exactly the spec's starter
// set (design generic-tool.md §3.5), each one parses clean, every recipe but
// default has a use_when, and no recipe pins max_rounds outside mode: review
// (a catalog recipe takes the agent.* knobs, G6) other than the one-round
// cap mode: review itself requires, or a digest recipe's own single review
// round, which is the report run's shape rather than a knob.
func TestCatalogIsTheStarterSet(t *testing.T) {
	want := []string{"default", "lint-fix", "pick", "premium", "review-only", "run-digest", "solo", "standard", "test-and-fix"}
	if got := CatalogNames(); !slices.Equal(got, want) {
		t.Fatalf("catalog = %v", got)
	}
	for _, n := range want {
		data, _ := CatalogText(n)
		r, ps := Parse(data)
		if len(ps) != 0 {
			t.Fatalf("%s: %s", n, ProblemsText(ps))
		}
		if n != DefaultName && r.UseWhen == "" {
			t.Errorf("%s has no use_when", n)
		}
		digest := len(r.Steps) > 0 && r.Steps[0].Kind == StepDigest
		for _, s := range r.Steps {
			if s.MaxRounds != 0 && r.Mode != ModeReview && !digest {
				t.Errorf("%s pins max_rounds: catalog recipes take the knobs (G6)", n)
			}
		}
	}
	if _, ok := CatalogText("nope"); ok {
		t.Fatal("an unknown name has text")
	}
	if _, ok := CatalogText("../catalog/default"); ok {
		t.Fatal("a path is a catalog name")
	}
}

// TestCatalogRenamedNamesAreGone: the two 0.6 catalog recipes the 0.7.0
// catalog retired are not in the new catalog (they moved to recipe.Renamed).
func TestCatalogRenamedNamesAreGone(t *testing.T) {
	for old := range Renamed {
		if _, ok := CatalogText(old); ok {
			t.Errorf("%s is still in the catalog; it was renamed", old)
		}
	}
}

// TestCatalogTextIsACopy: a caller that edits the bytes cannot change the
// catalog for the next caller.
func TestCatalogTextIsACopy(t *testing.T) {
	a, _ := CatalogText("default")
	want := string(a)
	for i := range a {
		a[i] = 'X'
	}
	b, _ := CatalogText("default")
	if string(b) != want {
		t.Fatalf("second call = %q, want %q", b, want)
	}
}
