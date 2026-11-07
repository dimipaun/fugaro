package sensitivity_test

// The floor is the line every layer is measured against, so these are the cases
// that make it one: every row is a row a person could have written (named,
// bounded, labelled, parseable by the matcher), the names are unique, and the
// table is at least every path the tree already holds sensitive.
//
// The last of those is the one that matters, and it has two halves because the
// tree states the rule twice. The routing rule's categories are a fixture of
// thirty-two paths (testdata/paths.txt), and the classifier's own table is
// eleven rules in code. A floor that covered only the fixture would leave the
// published checksums, the install commands and the plan-graph artifacts out:
// they live under website/ and docs/ and are in no fixture, so only the second
// half can see their absence (reconciliation §6, F1, F22, D1505).

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// theFloorRows is how many rows reconciliation §6's table writes down, and the
// count changes only with that table.
const theFloorRows = 43

// TestFloorRowsAreNamedAndBounded holds the table's own shape: every row
// validates — a name within its bound and its closed character set, a match
// within its bound, a category in the vocabulary — and no name is written
// twice, because a name is a row's identity and a second row under it would be
// unreachable in a union.
func TestFloorRowsAreNamedAndBounded(t *testing.T) {
	floor := sensitivity.Floor()
	if len(floor) != theFloorRows {
		t.Errorf("the floor has %d rows, want %d (reconciliation §6): a row added or dropped here is a change to what every layer is measured against", len(floor), theFloorRows)
	}
	if len(floor) > sensitivity.MaxPatterns {
		t.Errorf("the floor has %d rows, more than the %d a layer may write", len(floor), sensitivity.MaxPatterns)
	}
	seen := make(map[string]bool, len(floor))
	for _, row := range floor {
		if got := row.Validate(); got != "" {
			t.Errorf("the floor's row %q (match %q, category %q) does not validate: %s", row.Name, row.Match, row.Category, got)
		}
		if seen[row.Name] {
			t.Errorf("the floor writes the name %q twice: a name is a row's identity", row.Name)
		}
		seen[row.Name] = true
	}
}

// TestFloorIsAtLeastWhatTheRoutingRuleNames is the two-way floor the design
// promises (D1505): every path the tree already calls security-sensitive is
// matched by a floor row, so no layer is ever asked to be narrower than what
// the engine itself holds.
//
// The first half is the routing rule's own fixture — the thirty-two paths
// across fifteen categories that TestRulesTableMatchesTheReviewerRoutingText
// holds against the routing text. The second half is the classifier's own
// table, whose eleven exceptions are rules rather than paths: a whole class
// (the plan-graph artifacts), three trees and seven named files. A floor that
// answered only to the fixture would pass the first half while the published
// checksums, the install commands, the website's manifests and the redesign
// scripts were matched by nothing, which is exactly the floor this design
// carried before review.
//
// Each half asks both halves of the contract about the case-flipped spelling of
// every path it holds as well, because that is where the two answers disagreed:
// the classifier's exceptions fold ASCII case and a match on the path's own
// bytes does not, so `docs/Pending-Workflows/ci.yml`, `website/Package.json` and
// `website/data/Install.yaml` were Sensitive to the classifier and named by no
// row — a change to the CI tree that could never reach level 3 on the machine
// whose filesystem resolves the two spellings into one file.
func TestFloorIsAtLeastWhatTheRoutingRuleNames(t *testing.T) {
	floor := sensitivity.Floor()

	// folded is the contract in one place: a path the classifier calls Sensitive
	// is a path the floor has to name, in the case the classifier matched it in.
	folded := func(p, what string) {
		t.Helper()
		variant := caseFlipped(p)
		if got := sensitivity.Classify([]string{variant}); got != sensitivity.Sensitive {
			t.Errorf("Classify([%q]) = %q, want %q: %q is %s, and a spelling the classifier does not call Sensitive would make this assertion vacuous", variant, got, sensitivity.Sensitive, p, what)
		}
		if got := sensitivity.Match([]string{variant}, floor); len(got) == 0 {
			t.Errorf("no floor row matches %q, the case-flipped spelling of %q, %s: the classifier calls it Sensitive, so a change spelled that way would never reach level 3", variant, p, what)
		}
	}

	paths := 0
	for _, s := range pathsFixture(t) {
		for _, p := range s.paths {
			paths++
			if got := sensitivity.Match([]string{p}, floor); len(got) == 0 {
				t.Errorf("no floor row matches %q, a path of the routing rule's %q category: the floor is the list every layer is measured against", p, s.category)
			}
			folded(p, "a path of the routing rule's "+s.category+" category")
		}
	}
	if paths < 20 {
		t.Errorf("testdata/paths.txt carries %d paths; a fixture that lost its paths would pass this half vacuously", paths)
	}

	exceptions := sensitivity.SensitiveExceptions()
	if len(exceptions) < 11 {
		t.Errorf("the classifier's exception table has %d rules, fewer than the eleven reconciliation §6 counts", len(exceptions))
	}
	for _, e := range exceptions {
		p := coveredPath(e)
		if !e.Covers(p) {
			t.Fatalf("the test's own path %q is not covered by the exception %q (%q): this half would be vacuous", p, e.Name, e.Prefix)
		}
		if got := sensitivity.Match([]string{p}, floor); len(got) == 0 {
			t.Errorf("no floor row matches %q, which the classifier's %q exception calls sensitive: a change to it would never reach level 3", p, e.Name)
		}
		folded(p, "covered by the classifier's "+e.Name+" exception")
	}
}

// caseFlipped flips the case of every ASCII letter in p: the spelling a
// filesystem that does not distinguish case resolves to the same file, and so
// the twin the classifier's table and the floor have to agree about. It is ASCII
// only, like the fold itself — a path carrying a byte outside it is refused by
// cleanRelative before any table is asked.
func caseFlipped(p string) string {
	out := []byte(p)
	for i, c := range out {
		switch {
		case c >= 'a' && c <= 'z':
			out[i] = c - ('a' - 'A')
		case c >= 'A' && c <= 'Z':
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

// coveredPath is one path an exception line covers, built from the line's own
// shape: a prefix ending in a slash is a tree, a prefix with a suffix is the
// class of files under it that end in those bytes, and anything else is one
// named file. It is asked back through Covers before it is used, so a shape
// this test guessed wrong is a failure here rather than an empty pass.
func coveredPath(e sensitivity.RuleRow) string {
	switch {
	case e.Suffix != "":
		return e.Prefix + "plans/a-plan" + e.Suffix
	case strings.HasSuffix(e.Prefix, "/"):
		return e.Prefix + "README.md"
	default:
		return e.Prefix
	}
}
