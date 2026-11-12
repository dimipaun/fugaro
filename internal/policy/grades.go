package policy

import (
	"maps"
	"slices"
)

// The model grades of the escalation ladder (docs/outer-loop-escalation
// E2, reconciliation D2039): the model a rung runs and, exactly as a craft
// carries one, the credential or pool its calls go out on. The names are
// `strong` and `strongest` (D2033) and they are the ones a repository's own
// file would write; internal/escalation owns the vocabulary a task and a
// threshold speak, and this package keeps a name as the layer wrote it for
// CraftLayer.Access's reason: a merge that quietly dropped a name it did not
// know would answer an unset grade.

// Grade is one grade as a layer writes it: the model ID, and the credential or
// the pool the grade's calls go out on. Both are the ones a craft carries —
// config.Craft's pair — so a grade is validated where a craft is
// (config.ValidateModelGrades) and resolved by the same resolver.
//
// The two halves are one value rather than two keys because a grade is one
// choice: a model with another credential on it is another grade, and a merge
// that judged the halves apart would honour half a choice the owner never
// wrote (a repository's model under the project's credential).
type Grade struct {
	// Model is the grade's model ID, "" for a grade that names none, which is
	// not a grade at all: a layer that writes an empty entry has written
	// nothing (tightenGrades).
	Model string
	// Access names the credential or pool the grade's calls go out on, "" for
	// the project's default credential (D1624), exactly as an unset craft's
	// access is.
	Access string
}

// Grades is one layer's grades by name: the project's own answer, which a
// later layer may not write at all (G3).
//
// A map and not a struct with two fields, unlike config.Crafts' four: the
// grades are a closed set too, and the set is internal/escalation's (Grade),
// which this package may not import — escalation imports this one. A name
// outside the vocabulary is refused where the block is written
// (config.ValidateModelGrades, which holds the layer file and the node to the
// two names), not here where the merger's only reader is a layer of its own.
type Grades map[string]Grade

// ModelGradeKey is the policy key of one grade ("model_grades.strong").
func ModelGradeKey(grade string) string { return "model_grades." + grade }

// tightenGrades applies one layer's grades. project is whether the layer is
// the merge's first: the project's own, which in a run is the ceiling — the
// owner's policy node or the copy of his project layer the job carries (F25).
//
// Only that layer may set a grade, and every other layer's entry is dropped
// and recorded. This is the strictest of the merge's project-only rules, and
// the reason is the one thing a grade decides: which model a rung reaches for.
// The credentials and pools of docs/model-crafts are a later layer's to select
// among because the owner names them; a grade is that selection already made,
// so a repository that could write one could hand its own runs the model the
// project reserved for another rung — and the ladder's whole point is that the
// stronger model arrives when the ladder says so and not when the repository
// does.
//
// A value equal to the effective one is not recorded: the layer said nothing
// new (Merge). An entry with no model is not a grade and is dropped without a
// record, which is the decoder's refusal to make where the block is written: a
// layer in Go that carries none has written nothing.
func (e *Effective) tightenGrades(g Grades, src string, project bool) {
	for _, name := range slices.Sorted(maps.Keys(g)) {
		grade := g[name]
		if name == "" || grade.Model == "" {
			continue
		}
		key := ModelGradeKey(name)
		if project {
			if e.Grades == nil {
				e.Grades = make(Grades, len(g))
			}
			e.Grades[name], e.Sources[key] = grade, src
			continue
		}
		cur, defined := e.Grades[name]
		if defined && cur == grade {
			continue
		}
		e.Ignored = append(e.Ignored, Ignored{key, gradeText(grade), gradeText(cur), e.craftSource(key)})
	}
}

// gradeText is a grade as an Ignored entry renders it: the model and, when the
// grade names one, the credential or pool its calls go out on.
//
// Empty is reserved for a grade with no effective counterpart at all
// (craftSource), the same reading poolStrategyText keeps for a pool's strategy:
// a reader that shows one has to tell "the project's grade lost to this value"
// from "the project names no such grade", and the effective field is the only
// thing that answers it.
func gradeText(g Grade) string {
	if g.Access == "" {
		return g.Model
	}
	return g.Model + " on " + g.Access
}
