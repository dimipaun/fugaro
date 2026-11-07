package failure

import (
	"slices"
	"testing"
)

// TestFailureClassesAreClosedAndPropertyComplete: the vocabulary and the table
// that describes it are the same set. Every value of Classes() has a row, no
// row is orphaned, and every row's kind is one of the six the report is allowed
// to print.
//
// The report renders a class's kind, retryable and needs-owner beside its name,
// so a class with no row is a blank cell and a row with no class is a figure
// nothing produces. Both are silent in the report and loud here, which is the
// only place they are cheap to fix.
func TestFailureClassesAreClosedAndPropertyComplete(t *testing.T) {
	classes := Classes()
	if len(classes) == 0 {
		t.Fatal("Classes() is empty: this test would pass against no vocabulary at all")
	}
	seen := map[Class]bool{}
	for _, c := range classes {
		if seen[c] {
			t.Errorf("Classes() names %q twice: the schema's enum and the report's rows are built from this list, so a repeat is a class with two positions", c)
		}
		seen[c] = true
		if _, ok := PropertiesOf(c); !ok {
			t.Errorf("Classes() carries %q and the property table has no row for it: the report would print its kind, retryable and needs-owner as blanks", c)
		}
	}
	for c := range properties {
		if !slices.Contains(classes, c) {
			t.Errorf("the property table has a row for %q, which is not one of Classes(): a class no run can write is a figure the report would count at zero forever", c)
		}
	}
	kinds := []Kind{KindQuality, KindBudget, KindInfra, KindExternal, KindConfig, KindOther}
	for c, p := range properties {
		if !slices.Contains(kinds, p.Kind) {
			t.Errorf("%q's kind is %q, which is not one of the vocabulary's six: the report prints it in a cell that is a closed value, never a word a row chose", c, p.Kind)
		}
	}
	// A value from a newer build is not in the vocabulary, and saying so is the
	// whole answer a reader gets: it is never a guess at the nearest row.
	if p, ok := PropertiesOf(Class("delivered_draft")); ok {
		t.Errorf("PropertiesOf(%q) = %+v, true: a value outside Classes() has no properties, and answering with one is how a class nobody wrote becomes a figure", "delivered_draft", p)
	}
}
