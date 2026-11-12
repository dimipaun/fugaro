package config

// The model grades of the escalation ladder (docs/outer-loop-escalation,
// reconciliation D2033, D2039): `strong` and `strongest`, the two models a
// rung of the ladder runs. The word "tier" is deliberately not used — "tier"
// already means a governance spend band here (`governance.tiers`) — and the
// ladder's steps are rungs R0-R4, which are not this.
//
// A grade is where the project says which model "one stronger single pass"
// means. It is a model choice the project owns (G3): the project layer sets it
// for every repository of it, and a repository's own file may name one, which
// the merge drops and records (internal/policy's tightenGrades) rather than
// refusing it, because a grade is a value and not authority — the thresholds
// that read the ladder are the keys a file may not hold at all (D2032).
//
// The plan ships no model default for either grade (D2039): a grade nobody set
// is not a grade, and the rung that needs one refuses and asks.
const (
	// GradeStrong is the grade "one stronger single pass" runs (R2).
	GradeStrong = "strong"
	// GradeStrongest is the grade the ladder's strongest single pass runs.
	GradeStrongest = "strongest"
)

// GradeNames are the two grades, in the ladder's own order: the stronger one
// second.
var GradeNames = []string{GradeStrong, GradeStrongest}

// ModelGrades is `model_grades`: the project layer's `defaults.model_grades`
// and a repository's own top-level block, the same key in both, as
// governance: and rigor: are.
//
// Each grade is a *Craft and not a Craft for the reason Orchestrator's fields
// are pointers: a grade written with no model is refused (ValidateModelGrades)
// and a grade left out is the refusal the rung itself makes (D2039), so the
// decoder has to tell "the owner wrote this grade and it names nothing" from
// "the owner wrote no such grade" — a value field cannot, because two empty
// blocks were then one piece of text.
//
// A grade's model and its access are the pair a craft carries
// (docs/model-crafts/spec.md §3), so a grade is validated exactly as a craft
// is and resolved by the same resolver (MC2): the model is a model ID wherever
// it is written, and the access names a credential or pool of the project's.
type ModelGrades struct {
	Strong    *Craft `yaml:"strong,omitempty" json:"strong,omitempty"`
	Strongest *Craft `yaml:"strongest,omitempty" json:"strongest,omitempty"`
}

// Of is the grade name's own settings; the zero Craft for a name that is no
// grade, and for a grade the block leaves out — the pair is what a caller
// reads, and whether the block wrote the grade at all is Set's answer.
func (g ModelGrades) Of(name string) Craft {
	if c := g.At(name); c != nil {
		return *c
	}
	return Craft{}
}

// At is the grade name's settings, nil for a grade this block does not write.
func (g ModelGrades) At(name string) *Craft {
	switch name {
	case GradeStrong:
		return g.Strong
	case GradeStrongest:
		return g.Strongest
	}
	return nil
}

// Set reports whether the block writes this grade at all, at any value: the
// question a value field cannot answer (ModelGrades), and the one a writer
// needs, because an absent grade is left out of a file rather than written
// empty.
func (g ModelGrades) Set(name string) bool { return g.At(name) != nil }

// Empty reports whether the block writes neither grade, which is what a
// decoder answers for an absent member — so a project that sets no grade
// publishes no `model_grades` at all, exactly as one that sets no craft
// publishes no `crafts` (the same rule DefaultClaudeAccount follows).
func (g ModelGrades) Empty() bool { return g.Strong == nil && g.Strongest == nil }

// ValidateModelGrades reports every rule a grades block breaks that its own
// block can answer, with `at` the block's path ("model_grades" in a repository
// file, "defaults.model_grades" in the project layer):
//
//   - a grade the block writes names a model. An entry with no model is not an
//     unset grade — an unset grade is the entry left out, and it is what makes
//     a rung refuse and ask (D2039) — so it is refused here rather than read as
//     "no grade", which would make a written grade silently do nothing.
//   - the model is a model ID, the shape agent.models.* and a craft's model are
//     held to (ModelIDProblem).
//   - the access is a credential or pool name of the right shape
//     (ValidCredentialRef). Whether the name is defined is not asked here, for
//     ValidateCrafts' reason: a repository's file names credentials the
//     project's layers hold, and a document that has no credentials block has
//     no answer to give (the resolver's business, MC2).
func ValidateModelGrades(g ModelGrades, at string) []Problem {
	var ps []Problem
	for _, name := range GradeNames {
		c := g.At(name)
		if c == nil {
			continue
		}
		grade := at + "." + name
		if c.Model == "" {
			ps = append(ps, Problem{Path: grade + ".model", Message: "a grade names the model its rung runs: an entry with no model is not an unset grade — leave the grade out, and the rung that needs one refuses and asks (D2039)"})
		} else if msg := ModelIDProblem(c.Model); msg != "" {
			ps = append(ps, Problem{Path: grade + ".model", Message: msg})
		}
		// An unset access is not a name at all: it is the project's default
		// credential (D1624), the same reading a craft's unset access has
		// (ValidateCrafts), and ValidCredentialRef refuses the empty string
		// because a credential name is never empty.
		if c.Access != "" {
			if msg := ValidCredentialRef(c.Access); msg != "" {
				ps = append(ps, Problem{Path: grade + ".access", Message: msg})
			}
		}
	}
	return ps
}
