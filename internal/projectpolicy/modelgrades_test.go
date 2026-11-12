package projectpolicy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// gradeNode is a node at this version carrying the two grades: the project
// sets `strong` on a model and a credential of its own, and `strongest` on a
// model alone.
func gradeNode() []byte {
	return []byte(`{"version": ` + itoa(Version) + `, "budget": {"mode": "enforce", "per_run_usd": 5},` +
		`"model_grades": {"strong": {"model": "anthropic/claude-sonnet-5.5", "access": "openrouter-main"},` +
		`"strongest": {"model": "anthropic/claude-opus-5.5"}}}`)
}

// TestGradeUnsetIsRefusal: a grade the node writes with no model is refused
// where the node is read, and never read as "no grade". The two are different
// pieces of text and the difference is the whole of D2039: an absent grade is
// what makes a rung refuse and ask the owner, while a grade written empty would
// let a rung run with no model at all — or, worse, read as set and fall back to
// a model the owner never chose for it. So the entry is refused, with the path
// of the member that is missing.
func TestGradeUnsetIsRefusal(t *testing.T) {
	for name, raw := range map[string]string{
		"an empty strong":         `{"version": ` + itoa(Version) + `, "model_grades": {"strong": {}}}`,
		"an access with no model": `{"version": ` + itoa(Version) + `, "model_grades": {"strongest": {"access": "openrouter-main"}}}`,
		"a null model":            `{"version": ` + itoa(Version) + `, "model_grades": {"strong": {"model": null}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil {
			t.Errorf("%s: Decode(%s) = (%v, %v), want a refusal and no policy", name, raw, p, err)
		}
		if !found {
			t.Errorf("%s: the node exists and cannot be used, so found must be true (fail closed)", name)
		}
		if !strings.Contains(err.Error(), "names the model") {
			t.Errorf("%s: err = %v, want the refusal that says a grade names its model", name, err)
		}
	}

	// The other direction, in one place: the grade left out is not a refusal —
	// it is the node a project that sets no grade publishes, and what makes a
	// rung refuse is the *resolution*, not the read (D2039).
	p, found, err := Decode([]byte(`{"version": ` + itoa(Version) + `, "model_grades": {"strong": {"model": "anthropic/claude-sonnet-5.5"}}}`))
	if err != nil || p == nil || !found {
		t.Fatalf("a node naming one grade = (%v, %v, %v), want the policy: an absent grade is the ladder's own refusal", p, found, err)
	}
	if p.ModelGrades.Set(config.GradeStrongest) {
		t.Error("the node's grades name strongest, which the node does not write")
	}
	if got := p.ModelGrades.Of(config.GradeStrong).Access; got != "" {
		t.Errorf("model_grades.strong.access = %q, want empty: an access left out is the project's default credential (D1624)", got)
	}
}

// TestGradeWithoutPriceRefused: a grade names a model the run calls, and a run
// under a budget is held to a model the price table covers — a model with no
// price cannot be costed, so the ceiling could not be enforced on it
// (config.CheckProviderPolicy refuses every other model a run calls for the
// same reason). The table is the run's, so a machine with none (no budget at
// all, and `fugaro doctor` on an installation that has never fetched the
// catalogue) refuses nothing: this is a rule about a run, not about a node.
func TestGradeWithoutPriceRefused(t *testing.T) {
	p, found, err := Decode(gradeNode())
	if err != nil || p == nil || !found {
		t.Fatalf("Decode of a node with grades = (%v, %v, %v), want the policy", p, found, err)
	}
	tbl := &pricing.Table{Models: map[string]pricing.Model{
		"anthropic/claude-sonnet-5.5": {ID: "anthropic/claude-sonnet-5.5"},
	}}
	ps := p.GradePriceProblems(tbl)
	if len(ps) != 1 {
		t.Fatalf("GradePriceProblems = %+v, want the one grade the table does not price", ps)
	}
	if ps[0].Path != "model_grades.strongest.model" {
		t.Errorf("the refusal names %s, want model_grades.strongest.model", ps[0].Path)
	}
	if !strings.Contains(ps[0].Message, "has no price") || !strings.Contains(ps[0].Message, "model_prices") {
		t.Errorf("the refusal reads %q, want the price rule's own wording and the key that fixes it", ps[0].Message)
	}

	// The priced grade, no table at all, and a node with no grades: nothing to
	// refuse, each for its own reason.
	tbl.Models["anthropic/claude-opus-5.5"] = pricing.Model{ID: "anthropic/claude-opus-5.5"}
	if ps := p.GradePriceProblems(tbl); len(ps) != 0 {
		t.Errorf("GradePriceProblems over a table that prices both = %+v, want none", ps)
	}
	if ps := p.GradePriceProblems(nil); len(ps) != 0 {
		t.Errorf("GradePriceProblems with no table = %+v, want none: with no budget on there is nothing to cost", ps)
	}
	bare, _, err := Decode([]byte(`{"version": ` + itoa(Version) + `, "budget": {"mode": "enforce", "per_run_usd": 5}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ps := bare.GradePriceProblems(tbl); len(ps) != 0 {
		t.Errorf("GradePriceProblems over a node that sets no grade = %+v, want none", ps)
	}
}

// TestModelGradesRoundTripAndAreStrict: the member is the block a publisher
// writes and a run reads, so the two directions are pinned — a node with grades
// encodes the two members it was read with, a node with none writes no member
// at all (the byte-for-byte promise every optional member keeps), and the block
// is read as strictly as the crafts' are: a misspelt grade, a grade spelled in
// another case, a member the block does not have, and an access that is not a
// credential name are all refused here rather than silently becoming a grade
// nobody set.
func TestModelGradesRoundTripAndAreStrict(t *testing.T) {
	p, _, err := Decode(gradeNode())
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Decode(out)
	if err != nil {
		t.Fatalf("the node this reader writes does not decode again: %v\n%s", err, out)
	}
	if got, want := back.ModelGrades.Of(config.GradeStrong), (config.Craft{Model: "anthropic/claude-sonnet-5.5", Access: "openrouter-main"}); got != want {
		t.Errorf("model_grades.strong = %+v, want %+v: the pair a publisher writes is the pair a run reads", got, want)
	}
	if !strings.Contains(string(out), `"model_grades"`) {
		t.Errorf("the written node names no model_grades member:\n%s", out)
	}

	empty, _, err := Decode([]byte(`{"version": ` + itoa(Version) + `, "budget": {"mode": "enforce", "per_run_usd": 5}}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err = json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "model_grades") {
		t.Errorf("a node that sets no grade writes one:\n%s", out)
	}
	// An empty block is the same answer as no member: nothing is set, so a
	// publisher that writes {} publishes exactly what a project with no grades
	// has.
	blank, _, err := Decode([]byte(`{"version": ` + itoa(Version) + `, "model_grades": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if blank.ModelGrades != nil {
		t.Errorf("model_grades = %+v, want none: a block that sets no grade is no block", blank.ModelGrades)
	}

	for name, raw := range map[string]string{
		"a misspelt grade":      `{"version": ` + itoa(Version) + `, "model_grades": {"strng": {"model": "anthropic/claude-sonnet-5.5"}}}`,
		"a grade in caps":       `{"version": ` + itoa(Version) + `, "model_grades": {"STRONG": {"model": "anthropic/claude-sonnet-5.5"}}}`,
		"an unknown member":     `{"version": ` + itoa(Version) + `, "model_grades": {"strong": {"model": "anthropic/claude-sonnet-5.5", "creds": "x"}}}`,
		"what it does not have": `{"version": ` + itoa(Version) + `, "model_grades": {"strong": {"model": "anthropic/claude-sonnet-5.5", "access": "not a name"}}}`,
	} {
		if p, _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("%s: Decode(%s) = (%+v, no error), want a refusal", name, raw, p)
		}
	}
}
