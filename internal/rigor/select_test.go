package rigor

import (
	"slices"
	"testing"
)

// TestSelectKeepsTheStrictestOfTheMatched pins D23's rule: when a run's paths
// match several rules the strictest decides, and every rule that matched is
// kept, strictest first, so the record can say which ones they were.
func TestSelectKeepsTheStrictestOfTheMatched(t *testing.T) {
	areas := []Area{
		{Name: "docs", Match: "docs/**", Mode: POC},
		{Name: "payments", Match: "internal/payments/**", Mode: Critical},
		{Name: "web", Match: "web/**", Mode: Startup},
		{Name: "db", Match: "internal/db/**", Mode: Pro},
	}
	got := Select(areas, []string{"web/admin/page.tsx", "internal/payments/card.go", "docs/readme.md"})
	want := []string{"payments", "web", "docs"}
	if names := NamesOf(got); !slices.Equal(names, want) {
		t.Fatalf("Select = %v; want %v (strictest first)", names, want)
	}
	if mode := Strictest(ModesOf(got)...); mode != Critical {
		t.Errorf("the strictest matched mode = %q; want %q", mode, Critical)
	}
}

// TestSelectLeavesTheModeAloneWhenNothingMatches pins that a run whose paths
// match no rule is not an error and sets nothing: the mode stays what the
// layers below it set, which is what a run with no declared paths and an
// empty diff gets.
func TestSelectLeavesTheModeAloneWhenNothingMatches(t *testing.T) {
	areas := []Area{{Name: "payments", Match: "internal/payments/**", Mode: Critical}}
	for _, paths := range [][]string{nil, {}, {"README.md"}, {"internal/paymentss/card.go"}} {
		if got := Select(areas, paths); len(got) != 0 {
			t.Errorf("Select(%v) = %v; want no match", paths, NamesOf(got))
		}
	}
}

// TestSelectDeduplicatesByNameKeepingTheStrictest pins the two-layer case
// (D20c): defaults.rigor.areas and fugaro.yaml's rigor.areas are both in
// effect, so one name can arrive twice, and the name is printed once with the
// strictest rule it carried. A record that listed it twice would say two
// rules decided when one idea did.
func TestSelectDeduplicatesByNameKeepingTheStrictest(t *testing.T) {
	// The project layer's rule is the weaker one and is read first, so a
	// dedupe that kept the first would resolve to Pro instead of Critical.
	areas := []Area{
		{Name: "payments", Match: "internal/payments/**", Mode: Pro},
		{Name: "payments", Match: "internal/payments/**", Mode: Critical},
	}
	got := Select(areas, []string{"internal/payments/card.go"})
	if names := NamesOf(got); !slices.Equal(names, []string{"payments"}) {
		t.Fatalf("Select = %v; want the name once", names)
	}
	if got[0].Mode != Critical {
		t.Errorf("the kept rule is %q; want the strictest, %q", got[0].Mode, Critical)
	}
}

// TestSelectDeduplicatesByNameNotByPattern pins what the dedupe key is: the
// name. The two layers are written by different people, so the same area can
// be described by two different globs that both match the run — a project
// rule saying `internal/payments/**` and a repository rule narrowing it to
// `internal/payments/**` plus a second path. Keying the dedupe on the pattern
// would keep both and print the name twice, which is the thing the dedupe
// exists to stop.
func TestSelectDeduplicatesByNameNotByPattern(t *testing.T) {
	areas := []Area{
		{Name: "payments", Match: "internal/payments/**", Mode: Pro},
		{Name: "payments", Match: "internal/billing/**", Mode: Critical},
	}
	got := Select(areas, []string{"internal/payments/card.go", "internal/billing/invoice.go"})
	if names := NamesOf(got); !slices.Equal(names, []string{"payments"}) {
		t.Fatalf("Select = %v; want the name once, whatever globs it was written with", names)
	}
	if got[0].Mode != Critical {
		t.Errorf("the kept rule is %q; want the strictest, %q", got[0].Mode, Critical)
	}
}

// TestSelectDedupeKeepsTheInPlaceOrder pins that a dedupe replacing a rule
// keeps it where it was found, so the strictest-first order is still the one
// the sort produced rather than the order the layers happened to be read in.
func TestSelectDedupeKeepsTheInPlaceOrder(t *testing.T) {
	// "alpha" is written first but is the strictest, and its second copy is
	// the weak one: the duplicate must not drag it down.
	areas := []Area{
		{Name: "alpha", Match: "a/**", Mode: Critical},
		{Name: "beta", Match: "b/**", Mode: Pro},
		{Name: "alpha", Match: "a/**", Mode: POC},
	}
	got := Select(areas, []string{"a/x.go", "b/y.go"})
	if names := NamesOf(got); !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("Select = %v; want [alpha beta]", names)
	}
	if got[0].Mode != Critical {
		t.Errorf("alpha = %q; a weaker duplicate must not lower it", got[0].Mode)
	}
}

// TestSelectSortsEqualRanksStably pins that rules of equal rank keep the
// order they were written in, so a run that matches two Pro rules prints them
// in the configuration's order rather than in whatever order a map walk gave.
func TestSelectSortsEqualRanksStably(t *testing.T) {
	areas := []Area{
		{Name: "one", Match: "a/**", Mode: Pro},
		{Name: "two", Match: "b/**", Mode: Pro},
		{Name: "three", Match: "c/**", Mode: Pro},
	}
	got := Select(areas, []string{"a/x", "b/x", "c/x"})
	if names := NamesOf(got); !slices.Equal(names, []string{"one", "two", "three"}) {
		t.Errorf("Select = %v; want the written order for equal ranks", names)
	}
}

// TestSelectMatchesWithDoublestar pins the matcher: the same doublestar
// library and the same shapes the rest of the tree matches with (F9), so a
// pattern an author writes means here what it means in a rebuild rule.
func TestSelectMatchesWithDoublestar(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"internal/payments/**", "internal/payments/card.go", true},
		{"internal/payments/**", "internal/payments/deep/er/card.go", true},
		{"internal/payments/**", "internal/paymentss/card.go", false},
		{"internal/payments/**", "internal/payments", true},
		{"**/*.go", "internal/rigor/mode.go", true},
		{"**/*.go", "README.md", false},
		{"*.go", "mode.go", true},
		{"*.go", "internal/mode.go", false},
		{"internal/{config,rigor}/**", "internal/rigor/mode.go", true},
		{"internal/{config,rigor}/**", "internal/runner/runner.go", false},
		{"internal/payments/*.go", "internal/payments/card.go", true},
		{"internal/payments/*.go", "internal/payments/deep/card.go", false},
		{"docs/**", "docs/rigor-modes/spec.md", true},
		{"a", "a", true},
		{"a", "b", false},
	}
	for _, c := range cases {
		if got := matches(c.pattern, []string{c.path}); got != c.want {
			t.Errorf("matches(%q, %q) = %v; want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// TestMatchesIsQuietOnABadPattern pins that a pattern the matcher refuses —
// which Area.Validate stops at load, so only a hand-built rule reaches here —
// produces no match rather than a panic: the resolver must not be crashable
// by a value a caller built itself.
func TestMatchesIsQuietOnABadPattern(t *testing.T) {
	for _, p := range []string{"", "internal/[", "internal/{a,b"} {
		if matches(p, []string{"internal/x.go", ""}) {
			t.Errorf("matches(%q, ...) matched; a pattern the matcher refuses is not a match", p)
		}
	}
}

// TestNamesAndModesOfEmptyAreNil pins that an empty set is a nil slice and
// not a zero-length one, so the run record leaves the field absent rather
// than writing an empty list a reader would read as "no areas matched, and I
// checked".
func TestNamesAndModesOfEmptyAreNil(t *testing.T) {
	if got := NamesOf(nil); got != nil {
		t.Errorf("NamesOf(nil) = %#v; want nil", got)
	}
	if got := ModesOf(nil); got != nil {
		t.Errorf("ModesOf(nil) = %#v; want nil", got)
	}
	if got := NamesOf([]Area{}); got != nil {
		t.Errorf("NamesOf(empty) = %#v; want nil", got)
	}
	if got := ModesOf([]Area{}); got != nil {
		t.Errorf("ModesOf(empty) = %#v; want nil", got)
	}
}

// TestConfigAreasUnionsTheTwoLayers pins D20c on the merge itself: a layer
// that writes its own area list does not replace the other layer's, the way a
// list in a narrower layer otherwise does (docs/project-layer.md §4). Both
// are in effect, which is why Config keeps them apart.
func TestConfigAreasUnionsTheTwoLayers(t *testing.T) {
	project := []Area{{Name: "proj", Match: "p/**", Mode: Pro}}
	repo := []Area{{Name: "repo", Match: "r/**", Mode: Critical}}
	cases := []struct {
		name string
		cfg  Config
		want []string
	}{
		{"neither", Config{}, nil},
		{"project only", Config{ProjectAreas: project}, []string{"proj"}},
		{"repo only", Config{RepoAreas: repo}, []string{"repo"}},
		{"both, project first", Config{ProjectAreas: project, RepoAreas: repo}, []string{"proj", "repo"}},
	}
	for _, c := range cases {
		if names := NamesOf(c.cfg.Areas()); !slices.Equal(names, c.want) {
			t.Errorf("%s: Areas() = %v; want %v", c.name, names, c.want)
		}
	}
}
