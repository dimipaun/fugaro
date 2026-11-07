package sensitivity_test

// The pattern list is a security control, and these are the cases that make it
// one: a row a layer writes adds to the floor and never removes from it, a path
// the matcher cannot place is placed by no rule at all, and a pattern a person
// can write but the matcher cannot read is a load error rather than a rule that
// silently never fires (D1502, D1503, D1506).
//
// What no test here can decide is the floor's own content — reconciliation §6's
// table is the design's reading of what the tree holds sensitive.
// TestFloorIsAtLeastWhatTheRoutingRuleNames (floor_test.go) is what holds it
// above the two machine-readable statements of that rule the tree can be asked.

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// rowNamed is the row a list carries under one name, and whether it carries
// one. A name is a row's identity, which is what makes a merge of layers a
// union rather than a concatenation.
func rowNamed(list []sensitivity.Pattern, name string) (sensitivity.Pattern, bool) {
	for _, row := range list {
		if row.Name == name {
			return row, true
		}
	}
	return sensitivity.Pattern{}, false
}

// TestPatternMustBeARelativeDoublestarGlob walks the refusals a row meets where
// it is written (D1502). The four glob checks are internal/rigor/area.go's own,
// in the same order and with the same words, so a pattern and a rigor rule are
// refused for the same reasons: an empty match, one that is not relative to the
// repository root, one that climbs out of it with a `..` component, and one
// doublestar cannot read. The other two refusals are not a glob's: a category
// outside the vocabulary is a label nothing can print, and a match longer than
// the bound is text a person reads in the record.
func TestPatternMustBeARelativeDoublestarGlob(t *testing.T) {
	valid := sensitivity.Pattern{Name: "credentials", Match: "internal/creds/**", Category: sensitivity.Credentials}
	if got := valid.Validate(); got != "" {
		t.Fatalf("the floor's own row shape is refused: %q", got)
	}
	for _, tc := range []struct {
		what string
		row  sensitivity.Pattern
	}{
		{"an empty match", sensitivity.Pattern{Name: "row", Category: sensitivity.Credentials}},
		{"a match of spaces", sensitivity.Pattern{Name: "row", Match: "   ", Category: sensitivity.Credentials}},
		{"a match absolute from the repository root", sensitivity.Pattern{Name: "row", Match: "/internal/creds/**", Category: sensitivity.Credentials}},
		{"a match that climbs out of the repository", sensitivity.Pattern{Name: "row", Match: "docs/../../internal/creds/**", Category: sensitivity.Credentials}},
		{"a match doublestar cannot read", sensitivity.Pattern{Name: "row", Match: "internal/[creds/**", Category: sensitivity.Credentials}},
		{"a match longer than the bound", sensitivity.Pattern{Name: "row", Match: "internal/" + strings.Repeat("x", sensitivity.MaxPatternMatch) + "/**", Category: sensitivity.Credentials}},
		{"a category outside the vocabulary", sensitivity.Pattern{Name: "row", Match: "internal/creds/**", Category: "secrets"}},
		{"no category at all", sensitivity.Pattern{Name: "row", Match: "internal/creds/**"}},
	} {
		if got := tc.row.Validate(); got == "" {
			t.Errorf("%s (match %q, category %q) is accepted: a row is refused where it is written, not skipped where it would have fired", tc.what, tc.row.Match, tc.row.Category)
		}
	}
}

// TestPatternNameReachesTheRecordBounded holds the name's own rules: it is what
// the record, the pull request body and fugaro watch print (D24), so the
// character set is closed here rather than escaped at each surface, and the
// three bounds are internal/rigor/area.go's own numbers — a pattern reaches the
// same three surfaces a rule name does, so a person writes a pattern under the
// bound a person writes a rigor rule under.
func TestPatternNameReachesTheRecordBounded(t *testing.T) {
	if sensitivity.MaxPatternName != rigor.MaxAreaName || sensitivity.MaxPatternMatch != rigor.MaxAreaPattern || sensitivity.MaxPatterns != rigor.MaxAreas {
		t.Errorf("the pattern bounds are %d/%d/%d, want rigor's own %d/%d/%d: a pattern reaches the same three surfaces a rule name does",
			sensitivity.MaxPatternName, sensitivity.MaxPatternMatch, sensitivity.MaxPatterns,
			rigor.MaxAreaName, rigor.MaxAreaPattern, rigor.MaxAreas)
	}

	atBound := sensitivity.Pattern{Name: strings.Repeat("n", sensitivity.MaxPatternName), Match: "internal/creds/**", Category: sensitivity.Credentials}
	if got := atBound.Validate(); got != "" {
		t.Errorf("a name of exactly %d characters is refused: %q", sensitivity.MaxPatternName, got)
	}
	for _, tc := range []struct {
		what string
		name string
	}{
		{"no name at all", ""},
		{"a name one character past the bound", strings.Repeat("n", sensitivity.MaxPatternName+1)},
		{"a name holding a newline", "credentials\nrow"},
		{"a name holding a terminal escape", "credentials\x1b[31mrow"},
		{"a name holding a slash", "credentials/row"},
		{"a name holding a letter outside ASCII", "crédentials"},
	} {
		row := sensitivity.Pattern{Name: tc.name, Match: "internal/creds/**", Category: sensitivity.Credentials}
		if got := row.Validate(); got == "" {
			t.Errorf("%s (%q) is accepted: the name is text a person reads in the record", tc.what, tc.name)
		}
	}
}

// TestPatternThatMatchesEverythingIsRefused is D1503's first half: a pattern
// that marks the whole repository sensitive is not a pattern, it is the
// project's always_level_3 setting (D1517). Refusing it where it is written is
// what keeps that intent from hiding inside a glob no reader of the record can
// see through.
func TestPatternThatMatchesEverythingIsRefused(t *testing.T) {
	for _, match := range []string{"*", "**", "", "   "} {
		row := sensitivity.Pattern{Name: "row", Match: match, Category: sensitivity.Credentials}
		if got := row.Validate(); got == "" {
			t.Errorf("a row whose match is %q is accepted: * and ** are always_level_3, not a pattern", match)
		}
	}
}

// TestPatternThatMatchesNothingIsNotRefused is D1503's other half: a project
// writes its list before its first commit to the tree it names, so a pattern
// that matches no path today is a list that is early and not one that is wrong.
// Nothing is refused at load; doctor says once that it matched nothing in the
// base (D1513).
func TestPatternThatMatchesNothingIsNotRefused(t *testing.T) {
	for _, match := range []string{"internal/nothing/here/**", "does-not-exist.yaml", "a-tree-with-no-commits-yet/**"} {
		row := sensitivity.Pattern{Name: "row", Match: match, Category: sensitivity.Credentials}
		if got := row.Validate(); got != "" {
			t.Errorf("a row whose match is %q is refused at load: %q", match, got)
		}
		if got := sensitivity.Match([]string{"internal/creds/state.go"}, []sensitivity.Pattern{row}); len(got) != 0 {
			t.Errorf("the row whose match is %q matched a path it does not name: %v", match, got)
		}
	}
}

// TestUnplaceablePathMatchesNoRuleAndClassifiesSensitive is the composition the
// fail-closed direction rests on (F22): a path the matcher cannot place is
// matched by no rule, and the caller's other ask — Classify — answers Sensitive
// for it. The cases that matter are the ones a matcher reading bytes would
// place: `docs/./plans/x.graph.json` and `docs//plans/x.graph.json` are the
// plan-graph row's own path spelled so that `**` swallows the extra component,
// `internal/creds/ſrate.go` is a floor file on a filesystem that folds
// case, and the NUL-suffixed name is not text at all.
func TestUnplaceablePathMatchesNoRuleAndClassifiesSensitive(t *testing.T) {
	floor := sensitivity.Floor()
	unplaceable := []string{
		"",
		"/internal/creds/state.go",
		"../internal/creds/state.go",
		"internal/creds/../gateway/client.go",
		`internal\creds\state.go`,
		"docs/./plans/a-plan.graph.json",
		"docs//plans/a-plan.graph.json",
		"internal/orchestrator//sensitivity/classify.go",
		"internal/creds/state.go\x00",
		"internal/creds/ſrate.go",
	}
	for _, p := range unplaceable {
		if got := sensitivity.Match([]string{p}, floor); len(got) != 0 {
			t.Errorf("Match([%q], Floor()) = %v: a path the matcher cannot place is placed by no rule, and the caller's ask is Classify", p, names(got))
		}
		if got := sensitivity.Classify([]string{p}); got != sensitivity.Sensitive {
			t.Errorf("Classify([%q]) = %q, want %q: an unplaceable path fails closed", p, got, sensitivity.Sensitive)
		}
	}
}

// TestAProjectPatternCannotRemoveAFloorRow is the direction the list must not
// be able to move (D1506): a later layer may write a row, and may not replace
// one. A project that writes the floor's own name for a tree of its own gets
// its row only if the name is free, and the floor's tree stays matched whoever
// else asks for that name.
func TestAProjectPatternCannotRemoveAFloorRow(t *testing.T) {
	const name = "credentials"
	floor := sensitivity.Floor()
	project := []sensitivity.Pattern{{Name: name, Match: "internal/nothing/here/**", Category: sensitivity.Credentials}}

	got := sensitivity.Union(floor, project)
	row, ok := rowNamed(got, name)
	if !ok {
		t.Fatalf("the union has no row named %q", name)
	}
	if row.Match != "internal/creds/**" {
		t.Errorf("the union's %q row is %q, want the floor's own %q: a layer may add a row and never replace one", name, row.Match, "internal/creds/**")
	}
	for _, p := range []string{"internal/creds/state.go", "SECURITY.md"} {
		if got := sensitivity.Match([]string{p}, sensitivity.Union(floor, project)); len(got) == 0 {
			t.Errorf("no rule matches %q after a project wrote the floor's own row name: the floor's row is what the name belongs to", p)
		}
	}
}

// TestUnionKeepsEveryLayersAdditions is the merge itself (D1506): the floor,
// then the project's rows, then the repository's, and every row of every layer
// is in the answer. Nothing intersects and nothing subtracts, which is what
// makes a repository's key monotone — it can only ever say more.
func TestUnionKeepsEveryLayersAdditions(t *testing.T) {
	floor := sensitivity.Floor()
	project := []sensitivity.Pattern{{Name: "project-row", Match: "internal/project/**", Category: sensitivity.Guards}}
	repo := []sensitivity.Pattern{
		{Name: "repo-row", Match: "internal/repo/**", Category: sensitivity.Guards},
		{Name: "repo-secrets", Match: "secrets/**", Category: sensitivity.Credentials},
	}
	got := sensitivity.Union(floor, project, repo)

	for _, layer := range [][]sensitivity.Pattern{floor, project, repo} {
		for _, want := range layer {
			row, ok := rowNamed(got, want.Name)
			if !ok {
				t.Errorf("the union dropped %q, which a layer wrote", want.Name)
				continue
			}
			if row != want {
				t.Errorf("the union's %q row is %+v, want the row that layer wrote (%+v)", want.Name, row, want)
			}
		}
	}
	if len(got) != len(floor)+len(project)+len(repo) {
		t.Errorf("the union has %d rows, want %d: every layer's rows with no name written twice", len(got), len(floor)+len(project)+len(repo))
	}
	if got[0].Name != floor[0].Name {
		t.Errorf("the union starts with %q, want the floor's own first row %q: the floor is the list every layer is added to", got[0].Name, floor[0].Name)
	}
	for _, p := range []string{"internal/creds/state.go", "internal/project/x.go", "internal/repo/y.go", "secrets/api.json"} {
		if matched := sensitivity.Match([]string{p}, got); len(matched) == 0 {
			t.Errorf("no rule of the merged list matches %q", p)
		}
	}
}

// TestUnionDeduplicatesByNameKeepingTheFirst holds the merge's tie rule: a name
// written twice is one row, and the first writer's row is the one that survives
// — the floor's ahead of a project's, a project's ahead of a repository's. That
// is the same direction unionReviewFloor already uses for a level (F23), and it
// is the whole reason a later layer cannot take a row away from an earlier one.
func TestUnionDeduplicatesByNameKeepingTheFirst(t *testing.T) {
	first := []sensitivity.Pattern{
		{Name: "row", Match: "internal/first/**", Category: sensitivity.Guards},
		{Name: "only-first", Match: "internal/only-first/**", Category: sensitivity.Prompts},
	}
	second := []sensitivity.Pattern{
		{Name: "row", Match: "internal/second/**", Category: sensitivity.Prompts},
		{Name: "only-second", Match: "internal/only-second/**", Category: sensitivity.Credentials},
	}
	third := []sensitivity.Pattern{{Name: "row", Match: "**/third/**", Category: sensitivity.Targets}}

	got := sensitivity.Union(first, second, third)
	if len(got) != 3 {
		t.Fatalf("the union of three lists sharing one name has %d rows, want 3: a name is a row's identity", len(got))
	}
	row, ok := rowNamed(got, "row")
	if !ok {
		t.Fatalf("the union has no row named %q", "row")
	}
	if row != first[0] {
		t.Errorf("the union kept %+v for the name %q, want the first writer's row %+v", row, "row", first[0])
	}
	if _, ok := rowNamed(got, "only-second"); !ok {
		t.Errorf("the union dropped a row whose name no earlier list wrote")
	}
	if empty := sensitivity.Union(); len(empty) != 0 {
		t.Errorf("Union() with no list is %v, want nothing: the caller who merges nothing is asking for nothing", empty)
	}
}

// TestMatchReportsEachRowOnce holds the answer's shape: the matched rows are
// what the caller names in the record, so a row matching two of a change's
// paths is one row and not two, and the order is the list's own so that the
// record reads the same way every time.
func TestMatchReportsEachRowOnce(t *testing.T) {
	list := []sensitivity.Pattern{
		{Name: "first", Match: "internal/creds/**", Category: sensitivity.Credentials},
		{Name: "second", Match: "internal/**", Category: sensitivity.Guards},
		{Name: "third", Match: "docs/**", Category: sensitivity.Artifacts},
	}
	got := names(sensitivity.Match([]string{"internal/creds/state.go", "internal/credsvc/wire.go", "internal/managed/apply.go"}, list))
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("Match = %v, want [first second]: the rows that matched, in the list's own order, each once", got)
	}
	if none := sensitivity.Match(nil, list); len(none) != 0 {
		t.Errorf("Match with no path is %v, want nothing", names(none))
	}
}

// TestMatchIsCheapOnAPathologicalPattern holds the cost of a published pattern.
// The matcher is the two-pointer star match config.MatchSegment documents: the
// last `*` seen consumes as little as it can and is stretched one character at
// a time only when the rest of the pattern fails, so the cost is at most
// len(pattern)*len(path) — not exponential in the number of stars. A pattern is
// text a project layer carries and a run reads from the base, so this is the
// property that keeps a list somebody else wrote from making a run quadratic,
// or worse.
//
// The failure mode is a hang rather than a wrong answer, which is why the case
// is sized so that a two-pointer matcher finishes in microseconds: forty stars
// against forty characters is 2^40 steps for a matcher that scans every star,
// and about 1,600 for this one. The deadline is generous on purpose — it is
// there to catch a blow-up, not to measure a machine.
func TestMatchIsCheapOnAPathologicalPattern(t *testing.T) {
	const stars = 40
	pattern := sensitivity.Pattern{Name: "pathological", Match: strings.Repeat("a*", stars) + "b", Category: sensitivity.Guards}
	unmatched := strings.Repeat("a", stars) + "c"

	start := time.Now()
	got := sensitivity.Match([]string{unmatched}, []sensitivity.Pattern{pattern})
	took := time.Since(start)
	if len(got) != 0 {
		t.Errorf("the pattern %q matched %q, which it does not name", pattern.Match, unmatched)
	}
	if took > 5*time.Second {
		t.Errorf("matching %d stars against %d characters took %s: the matcher backtracks over every star instead of holding the last one", stars, len(unmatched), took)
	}
}

// names is a list of rows as the record would print them.
func names(list []sensitivity.Pattern) []string {
	out := make([]string, 0, len(list))
	for _, row := range list {
		out = append(out, row.Name)
	}
	return out
}
