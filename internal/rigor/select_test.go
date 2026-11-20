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
		{Name: "docs", Match: "docs/**", Level: POC},
		{Name: "payments", Match: "internal/payments/**", Level: Critical},
		{Name: "web", Match: "web/**", Level: Startup},
		{Name: "db", Match: "internal/db/**", Level: Pro},
	}
	got := Select(areas, "", []string{"web/admin/page.tsx", "internal/payments/card.go", "docs/readme.md"})
	want := []string{"payments", "web", "docs"}
	if names := areaNames(got); !slices.Equal(names, want) {
		t.Fatalf("Select = %v; want %v (strictest first)", names, want)
	}
	if level := Strictest(RigorsOf(got)...); level != Critical {
		t.Errorf("the strictest matched level = %q; want %q", level, Critical)
	}
}

// TestSelectLeavesTheModeAloneWhenNothingMatches pins that a run whose paths
// match no rule is not an error and sets nothing: the level stays what the
// layers below it set, which is what a run with no declared paths and an
// empty diff gets.
func TestSelectLeavesTheModeAloneWhenNothingMatches(t *testing.T) {
	areas := []Area{{Name: "payments", Match: "internal/payments/**", Level: Critical}}
	for _, paths := range [][]string{nil, {}, {"README.md"}, {"internal/paymentss/card.go"}} {
		if got := Select(areas, "", paths); len(got) != 0 {
			t.Errorf("Select(%v) = %v; want no match", paths, areaNames(got))
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
		{Name: "payments", Match: "internal/payments/**", Level: Pro},
		{Name: "payments", Match: "internal/payments/**", Level: Critical},
	}
	got := Select(areas, "", []string{"internal/payments/card.go"})
	if names := areaNames(got); !slices.Equal(names, []string{"payments"}) {
		t.Fatalf("Select = %v; want the name once", names)
	}
	if got[0].Level != Critical {
		t.Errorf("the kept rule is %q; want the strictest, %q", got[0].Level, Critical)
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
		{Name: "payments", Match: "internal/payments/**", Level: Pro},
		{Name: "payments", Match: "internal/billing/**", Level: Critical},
	}
	got := Select(areas, "", []string{"internal/payments/card.go", "internal/billing/invoice.go"})
	if names := areaNames(got); !slices.Equal(names, []string{"payments"}) {
		t.Fatalf("Select = %v; want the name once, whatever globs it was written with", names)
	}
	if got[0].Level != Critical {
		t.Errorf("the kept rule is %q; want the strictest, %q", got[0].Level, Critical)
	}
}

// TestSelectDedupeKeepsTheInPlaceOrder pins that a dedupe replacing a rule
// keeps it where it was found, so the strictest-first order is still the one
// the sort produced rather than the order the layers happened to be read in.
func TestSelectDedupeKeepsTheInPlaceOrder(t *testing.T) {
	// "alpha" is written first but is the strictest, and its second copy is
	// the weak one: the duplicate must not drag it down.
	areas := []Area{
		{Name: "alpha", Match: "a/**", Level: Critical},
		{Name: "beta", Match: "b/**", Level: Pro},
		{Name: "alpha", Match: "a/**", Level: POC},
	}
	got := Select(areas, "", []string{"a/x.go", "b/y.go"})
	if names := areaNames(got); !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("Select = %v; want [alpha beta]", names)
	}
	if got[0].Level != Critical {
		t.Errorf("alpha = %q; a weaker duplicate must not lower it", got[0].Level)
	}
}

// TestSelectSortsEqualRanksStably pins that rules of equal rank keep the
// order they were written in, so a run that matches two Pro rules prints them
// in the configuration's order rather than in whatever order a map walk gave.
func TestSelectSortsEqualRanksStably(t *testing.T) {
	areas := []Area{
		{Name: "one", Match: "a/**", Level: Pro},
		{Name: "two", Match: "b/**", Level: Pro},
		{Name: "three", Match: "c/**", Level: Pro},
	}
	got := Select(areas, "", []string{"a/x", "b/x", "c/x"})
	if names := areaNames(got); !slices.Equal(names, []string{"one", "two", "three"}) {
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
		{"**/*.go", "internal/rigor/level.go", true},
		{"**/*.go", "README.md", false},
		{"*.go", "level.go", true},
		{"*.go", "internal/level.go", false},
		{"internal/{config,rigor}/**", "internal/rigor/level.go", true},
		{"internal/{config,rigor}/**", "internal/runner/runner.go", false},
		{"internal/payments/*.go", "internal/payments/card.go", true},
		{"internal/payments/*.go", "internal/payments/deep/card.go", false},
		{"docs/**", "docs/rigor-levels/spec.md", true},
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

// TestModesOfEmptyIsNil pins that an empty set is a nil slice and not a
// zero-length one, so the run record leaves the field absent rather than
// writing an empty list a reader would read as "no areas matched, and I
// checked".
func TestModesOfEmptyIsNil(t *testing.T) {
	if got := RigorsOf(nil); got != nil {
		t.Errorf("RigorsOf(nil) = %#v; want nil", got)
	}
	if got := RigorsOf([]Area{}); got != nil {
		t.Errorf("RigorsOf(empty) = %#v; want nil", got)
	}
}

// TestConfigAreasUnionsTheTwoLayers pins D20c on the merge itself: a layer
// that writes its own area list does not replace the other layer's, the way a
// list in a narrower layer otherwise does (docs/project-layer.md §4). Both
// are in effect, which is why Config keeps them apart.
func TestConfigAreasUnionsTheTwoLayers(t *testing.T) {
	project := []Area{{Name: "proj", Match: "p/**", Level: Pro}}
	repo := []Area{{Name: "repo", Match: "r/**", Level: Critical}}
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
		if names := areaNames(c.cfg.Areas()); !slices.Equal(names, c.want) {
			t.Errorf("%s: Areas() = %v; want %v", c.name, names, c.want)
		}
	}
}

// TestSelectMatchesAProjectsMembers is LD4 at the matcher: an area of the
// project layer is made of members, each a whole repository or a repository
// with the paths of it that are in the area, so a rule matches a run when the
// run's repository is one of its members and — for a member with paths — the
// run touches a path of it. A member of another repository never matches, and
// neither does a member with paths when the run touched none of them.
func TestSelectMatchesAProjectsMembers(t *testing.T) {
	areas := []Area{
		{Name: "console", Members: []Member{
			{Repo: "acme/console-web"},
			{Repo: "acme/app", Paths: []string{"ui/**"}},
		}, Level: Pro},
		{Name: "mobile", Members: []Member{
			{Repo: "acme/app", Paths: []string{"mobile/**"}},
		}, Level: Critical},
	}
	cases := []struct {
		repo  string
		paths []string
		want  []string
	}{
		{"acme/console-web", nil, []string{"console"}},
		{"acme/console-web", []string{"anything/at/all.go"}, []string{"console"}},
		{"acme/app", []string{"ui/button.tsx"}, []string{"console"}},
		{"acme/app", []string{"mobile/screen.tsx"}, []string{"mobile"}},
		{"acme/app", []string{"docs/readme.md"}, nil},
		{"acme/app", nil, nil},
		{"acme/payments-core", []string{"ui/button.tsx"}, nil},
		{"", []string{"ui/button.tsx"}, nil},
	}
	for _, c := range cases {
		if names := areaNames(Select(areas, c.repo, c.paths)); !slices.Equal(names, c.want) {
			t.Errorf("Select(%q, %v) = %v; want %v", c.repo, c.paths, names, c.want)
		}
	}
}

// TestSelectTakesTheStrictestOfOneLayersAreas pins D23 as amended: two areas
// of *one* layer that match one run resolve to the strictest of them, which is
// the normal case once members carry paths — acme/app's ui/ is in the console
// area and, through a whole-repo member, in a second area of the same project
// layer too. The weaker area is written first, so a rule that kept the first
// match rather than the strictest answers pro here instead of critical.
func TestSelectTakesTheStrictestOfOneLayersAreas(t *testing.T) {
	project := []Area{
		{Name: "console", Members: []Member{{Repo: "acme/app", Paths: []string{"ui/**"}}}, Level: Pro},
		{Name: "app", Members: []Member{{Repo: "acme/app"}}, Level: Critical},
	}
	got := Select(project, "acme/app", []string{"ui/button.tsx"})
	if names := areaNames(got); !slices.Equal(names, []string{"app", "console"}) {
		t.Fatalf("Select = %v; want [app console], strictest first", names)
	}
	if level := Strictest(RigorsOf(got)...); level != Critical {
		t.Errorf("the strictest of the layer's matched areas = %q; want %q", level, Critical)
	}
	// And the resolver reads that through: the layer's own level is the answer.
	cfg := Config{Project: POC, ProjectAreas: project}
	if r := Resolve(cfg, Request{Repo: "acme/app", Paths: []string{"ui/button.tsx"}}); r.Level != Critical || r.From != FromArea {
		t.Errorf("Resolve over one layer's two matched areas = (%q, %q); want (%q, %q)", r.Level, r.From, Critical, FromArea)
	}
}

// TestSelectMembersAreMuteWithoutARepo pins the fail-closed half: a member
// names one repository, so a caller that knows no repository — a resolution
// whose Request carries no repo, which is every run of a repository that is
// itself the area's subject — matches no member at all. A member read as
// matching a repo it was never compared against would put every project area
// in effect for every run.
func TestSelectMembersAreMuteWithoutARepo(t *testing.T) {
	areas := []Area{{Name: "console", Members: []Member{{Repo: "acme/app"}}, Level: Critical}}
	for _, paths := range [][]string{nil, {"ui/button.tsx"}} {
		if got := Select(areas, "", paths); len(got) != 0 {
			t.Errorf("Select(\"\", %v) = %v; want no match: a member names its repository", paths, areaNames(got))
		}
	}
	// A rule that writes a glob instead is about paths alone, so it still
	// matches without one (the shape v0.11.0 shipped, and a repository's own
	// rules).
	glob := []Area{{Name: "payments", Match: "internal/payments/**", Level: Critical}}
	if got := Select(glob, "", []string{"internal/payments/card.go"}); len(got) != 1 {
		t.Errorf("Select over a glob rule = %v; want the match", areaNames(got))
	}
}

// areaNames is the names of areas, in order, for a test that compares only
// which rules matched.
func areaNames(areas []Area) []string {
	var out []string
	for _, a := range areas {
		out = append(out, a.Name)
	}
	return out
}
