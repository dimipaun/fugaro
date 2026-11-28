package policy

import "testing"

// TestTightenGradesIgnoresTheRepository: a grade is the project's own model
// choice, and the merge reads no other layer's (G3, D2039). A repository's
// fugaro.yaml cannot reach this rule at all: the two grade models left the
// file's vocabulary (config.LiftedKeys), so the reader drops them before a
// layer is built and names them where they are written, and the merge neither
// applies a repository's grade nor keeps it in the run's ignored list — the
// record was how a person learned of a grade the merge had dropped, and that is
// the reader's to say now.
//
// The red step: while tightenGrades has its repository half, the repository's
// two entries are recorded and the assertion on Ignored fails.
func TestTightenGradesIgnoresTheRepository(t *testing.T) {
	ceiling := Layer{Grades: Grades{
		"strong":    {Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"},
		"strongest": {Model: "anthropic/claude-opus-5.5"},
	}}
	repo := Layer{Grades: Grades{
		// The grade the project sets, with another model on it.
		"strong": {Model: "deepseek/deepseek-v4.1-flash"},
		// A grade the project names nowhere.
		"strongest-2": {Model: "deepseek/deepseek-v4-flash"},
		// The project's own grade, written again: neither a loosening nor a
		// change, and still no entry for the merge to keep.
		"strongest": {Model: "anthropic/claude-opus-5.5"},
	}}
	e := Merge(ceiling, repo)

	if got, want := e.Grades["strong"], (Grade{Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("merged model_grades.strong = %+v, want the project's %+v: a repository may not set a grade", got, want)
	}
	if _, ok := e.Grades["strongest-2"]; ok {
		t.Error("the merged grades hold the repository's strongest-2: a repository may not introduce a grade the project names nowhere")
	}
	if len(e.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none: the merge reads no repository's grades, so it drops none to record", e.Ignored)
	}
	if got := e.Sources[ModelGradeKey("strong")]; got != SourceCeiling {
		t.Errorf("Sources[model_grades.strong] = %q, want %q", got, SourceCeiling)
	}
	if _, ok := e.Sources[ModelGradeKey("strongest-2")]; ok {
		t.Errorf("Sources names model_grades.strongest-2 = %q: a grade no layer defined has no source", e.Sources[ModelGradeKey("strongest-2")])
	}
}
