package policy

import (
	"slices"
	"testing"
)

// TestPolicyMergeGradesProjectOnly: a grade is the project's own model choice
// (G3, D2039). The ceiling defines the two grades — the model each rung runs
// and the credential or pool its calls go out on — and a later layer's entry
// is dropped and recorded, whether it names another model for a grade the
// project already sets or a grade the project sets nowhere. The red step of
// this change: while the merge has no rule over the grades, a repository's
// fugaro.yaml sets the model a rung reaches for, and every assertion here
// fails on the value that came through instead.
func TestPolicyMergeGradesProjectOnly(t *testing.T) {
	ceiling := Layer{Grades: Grades{
		"strong":    {Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"},
		"strongest": {Model: "anthropic/claude-opus-5.5"},
	}}
	repo := Layer{Grades: Grades{
		// The grade the project sets, with another model on it.
		"strong": {Model: "deepseek/deepseek-v4.1-flash"},
		// A grade the project names nowhere.
		"strongest-2": {Model: "deepseek/deepseek-v4-flash"},
	}}
	e := Merge(ceiling, repo)

	if got, want := e.Grades["strong"], (Grade{Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("merged model_grades.strong = %+v, want the project's %+v: a repository may not set a grade", got, want)
	}
	if _, ok := e.Grades["strongest-2"]; ok {
		t.Error("the merged grades hold the repository's strongest-2: a repository may not introduce a grade the project names nowhere")
	}
	for _, want := range []Ignored{
		{"model_grades.strong", "deepseek/deepseek-v4.1-flash", "anthropic/claude-sonnet-5.5 on openrouter-main", SourceCeiling},
		{"model_grades.strongest-2", "deepseek/deepseek-v4-flash", "", SourceCeiling},
	} {
		if !slices.Contains(e.Ignored, want) {
			t.Errorf("Ignored lacks %+v: every grade a later layer sets is recorded, with the project's value beside it where it has one\nignored: %+v", want, e.Ignored)
		}
	}
	if got := e.Sources[ModelGradeKey("strong")]; got != SourceCeiling {
		t.Errorf("Sources[model_grades.strong] = %q, want %q", got, SourceCeiling)
	}
	if _, ok := e.Sources[ModelGradeKey("strongest-2")]; ok {
		t.Errorf("Sources names model_grades.strongest-2 = %q: a grade no layer defined has no source", e.Sources[ModelGradeKey("strongest-2")])
	}
}

// TestPolicyMergeGradesAgreeingLayerIsNotRecorded: a later layer that names a
// grade exactly as the project's stands said nothing new, and Merge records
// only what moved (the rule every other key follows: equal values are not
// ignored). A rule that recorded it would warn every repository that re-states
// the project's grade.
func TestPolicyMergeGradesAgreeingLayerIsNotRecorded(t *testing.T) {
	g := Grades{"strong": {Model: "anthropic/claude-sonnet-5.5", Access: ""}}
	e := Merge(Layer{Grades: g}, Layer{Grades: g}, Layer{})
	if len(e.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none: the layer named the project's own grade", e.Ignored)
	}
	if got := e.Grades["strong"]; got != (Grade{Model: "anthropic/claude-sonnet-5.5"}) {
		t.Errorf("merged model_grades.strong = %+v, want the project's", got)
	}
}

// TestPolicyMergeGradesUnsetStaysUnset: a project that defines no grade and a
// layer under it that writes none either leave the merged grades empty, which
// is what the resolver reads as "no grade here" (D2039: an unset grade makes
// the rung refuse) — never an empty Grade that would read as a grade set to no
// model at all.
func TestPolicyMergeGradesUnsetStaysUnset(t *testing.T) {
	e := Merge(Layer{}, Layer{Grades: Grades{"": {Model: "anthropic/claude-sonnet-5.5"}}}, Layer{})
	if len(e.Grades) != 0 {
		t.Errorf("merged grades = %+v, want none: an unnamed grade and no grade are the same block", e.Grades)
	}
	if len(e.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none: a value under no grade name is not a grade", e.Ignored)
	}
}
