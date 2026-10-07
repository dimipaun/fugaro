package recipe

import (
	"slices"
	"testing"
)

func TestCatalog(t *testing.T) {
	if got := CatalogNames(); !slices.Equal(got, []string{"cheap-loop-senior", "claude-solo", "default"}) {
		t.Fatalf("names = %v", got)
	}
	want := map[string]struct {
		alias bool
		steps []Step
	}{
		"default":           {false, []Step{{Kind: StepFirstLine}, {Kind: StepReview}}},
		"cheap-loop-senior": {false, []Step{{Kind: StepFirstLine, MaxRounds: 2}, {Kind: StepReview, MaxRounds: 1}}},
		"claude-solo":       {true, []Step{{Kind: StepReview, MaxRounds: 1}}},
	}
	for _, name := range CatalogNames() {
		text, ok := CatalogText(name)
		if !ok {
			t.Fatalf("%s: no text", name)
		}
		r, ps := Parse(text)
		if len(ps) > 0 || r.Name != name || r.Description == "" {
			t.Fatalf("%s: %+v, %v", name, r, ps)
		}
		if w := want[name]; r.ReviewerIsCoder != w.alias || !slices.Equal(r.Steps, w.steps) {
			t.Errorf("%s = %+v, want %+v", name, r, w)
		}
	}
	if _, ok := CatalogText("nope"); ok {
		t.Fatal("an unknown name has text")
	}
	if _, ok := CatalogText("../catalog/default"); ok {
		t.Fatal("a path is a catalog name")
	}
}
