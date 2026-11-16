package config

import (
	"strings"
	"testing"
)

// The escalation ladder's two model grades (docs/outer-loop-escalation E2,
// D2039): `strong` and `strongest`, each the model a rung of the ladder runs
// and the credential or pool its calls go out on, the pair a craft carries.
// They are written in the project layer for every repository of the project
// and in a repository's own file — which the merge drops and records
// (internal/policy's tightenGrades), so the two spellings here are both keys
// this package has to admit and validate.

const gradeHead = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"

// TestProjectLayerModelGrades: the project layer's defaults.model_grades is
// read whole — the two grades, each half of each on its own — and an absent
// block is no block rather than two empty grades, which is what D2039 needs:
// a grade nobody set is not a grade, and the rung that needs one refuses and
// asks.
func TestProjectLayerModelGrades(t *testing.T) {
	l := mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"+
		"defaults:\n  model_grades:\n    strong: {model: anthropic/claude-sonnet-5.5, access: openrouter-main}\n    strongest: {model: anthropic/claude-opus-5.5}\n")
	g := l.Defaults.ModelGrades
	if got, want := g.Of(GradeStrong), (Craft{Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("defaults.model_grades.strong = %+v, want %+v", got, want)
	}
	if got, want := g.Of(GradeStrongest), (Craft{Model: "anthropic/claude-opus-5.5"}); got != want {
		t.Errorf("defaults.model_grades.strongest = %+v, want %+v: a grade that names no access takes the project's default credential", got, want)
	}
	if !g.Set(GradeStrong) || !g.Set(GradeStrongest) || g.Empty() {
		t.Errorf("the block writes both grades, and Set/Empty say %v/%v/%v", g.Set(GradeStrong), g.Set(GradeStrongest), g.Empty())
	}

	// A layer that writes none: the block is empty and Set answers false for
	// both, which is the state the ladder reads as "the rung refuses and asks"
	// and never as "run the default model" (D2039).
	none := mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\n")
	if !none.Defaults.ModelGrades.Empty() || none.Defaults.ModelGrades.Set(GradeStrong) {
		t.Errorf("a layer with no model_grades answers %+v, want no grade set", none.Defaults.ModelGrades)
	}
	// A name that is no grade is no grade: Of answers the zero pair and Set
	// false, so a reader cannot be handed a grade by a spelling the ladder does
	// not have.
	if got := none.Defaults.ModelGrades.Of("strongest-2"); got != (Craft{}) || none.Defaults.ModelGrades.Set("strongest-2") {
		t.Errorf("a name outside the ladder answers %+v, want nothing", got)
	}
}

// TestModelGradesAreValidatedWhereTheyAreWritten: a grade is a model choice,
// so it is held to the rules a craft's halves are (E2): a written grade names
// a model — an entry with no model is not an unset grade, and leaving the
// grade out is how a file says nothing about it — the model is a model ID, and
// the access is a credential or pool name of the right shape. Each refusal
// names the path in the document that wrote it, because the two documents that
// may write a grade spell it in two places.
func TestModelGradesAreValidatedWhereTheyAreWritten(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{"a grade with no model", "strong: {}\n", "defaults.model_grades.strong.model: a grade names the model its rung runs"},
		{"a grade with an access and no model", "strong: {access: openrouter-main}\n", "defaults.model_grades.strong.model: a grade names the model its rung runs"},
		{"a model that is not an ID", "strongest: {model: 'two words'}\n", "defaults.model_grades.strongest.model"},
		{"an access that is not a name", "strong: {model: anthropic/claude-sonnet-5.5, access: 'not a name'}\n", "defaults.model_grades.strong.access"},
		{"a member the block does not have", "strong: {model: anthropic/claude-sonnet-5.5, creds: x}\n", "defaults.model_grades.strong.creds: is not a fugaro.yaml key"},
		{"a grade the ladder does not have", "stronger: {model: anthropic/claude-sonnet-5.5}\n", "defaults.model_grades.stronger: is not a fugaro.yaml key"},
	} {
		_, ps := ParseProjectLayer([]byte(gradeHead+"defaults:\n  model_grades:\n    "+tc.block), LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
		if len(ps) == 0 {
			t.Errorf("%s: the layer was accepted, want a refusal at %s", tc.name, tc.want)
			continue
		}
		if got := strings.Join(problemTexts(ps), "\n"); !strings.Contains(got, tc.want) {
			t.Errorf("%s: refusals =\n%s\nwant one naming %s", tc.name, got, tc.want)
		}
	}

	// The repository's own file writes the same key at its top level, and it is
	// admitted there: the entry is dropped and recorded by the merge, which is
	// the counterpart of admitting it here (E2's note). Its rules are the same
	// ones, at the path the file writes.
	cfg, ps := Parse([]byte(minimalYAML +
		"model_grades:\n  strong: {model: anthropic/claude-sonnet-5.5, access: openrouter-main}\n"))
	if len(ps) > 0 {
		t.Fatalf("a repository file with a grade was refused: %v", ps)
	}
	if cfg.ModelGrades == nil || cfg.ModelGrades.Of(GradeStrong).Access != "openrouter-main" {
		t.Errorf("model_grades = %+v, want the grade the file wrote", cfg.ModelGrades)
	}
	_, ps = Parse([]byte(minimalYAML + "model_grades:\n  strong: {}\n"))
	if len(ps) == 0 {
		t.Fatal("a repository file with a grade that names no model was accepted")
	}
	if got := strings.Join(problemTexts(ps), "\n"); !strings.Contains(got, "model_grades.strong.model") {
		t.Errorf("the refusal =\n%s\nwant the path model_grades.strong.model", got)
	}
}

// problemTexts renders problems as Path: Message, the way the layer's own
// refusals read to an operator.
func problemTexts(ps []Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.Path == "" {
			out = append(out, p.Message)
			continue
		}
		out = append(out, p.Path+": "+p.Message)
	}
	return out
}
