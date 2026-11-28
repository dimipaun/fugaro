package policy

import "testing"

// TestPolicyMergeGradesUnsetStaysUnset: a project whose grades block names only
// an unnamed grade leaves the merged grades empty, which is what the resolver
// reads as "no grade here" (D2039: an unset grade makes the rung refuse) — never
// an empty Grade that would read as a grade set to no model at all.
func TestPolicyMergeGradesUnsetStaysUnset(t *testing.T) {
	e := Merge(Layer{Grades: Grades{"": {Model: "anthropic/claude-sonnet-5.5"}}}, Layer{})
	if len(e.Grades) != 0 {
		t.Errorf("merged grades = %+v, want none: an unnamed grade and no grade are the same block", e.Grades)
	}
	if len(e.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none: a value under no grade name is not a grade", e.Ignored)
	}
}
