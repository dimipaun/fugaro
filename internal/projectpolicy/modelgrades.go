package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// ModelGrades is the project's two model grades on this node (docs/outer-loop-
// escalation E2, reconciliation D2039): `strong` and `strongest`, each the
// model its rung of the escalation ladder runs and the credential or pool its
// calls go out on — the pair a craft carries (docs/model-crafts/spec.md §3),
// which is why the block's type is config's and not a respelling of it here.
//
// It travels on the owner-written node because the ladder's rungs are decided
// by the daemon that reads that node, not by the process that launches a run:
// which model a rung spends on, and whose credential or pool its calls go out
// on, is a decision the owner publishes. The project layer's
// defaults.model_grades is the other home D2039 gives it — the file an owner
// writes for every repository of the project — and a repository's fugaro.yaml
// may name one, which the merge drops and records (internal/policy's
// tightenGrades): a grade is a model choice the project owns (G3). Neither
// home creates a credential: a grade's access names one of the project's own,
// and the resolver refuses a name no layer holds (MC2).
//
// The pointer on Policy is what tells a node that writes no grades from one
// that writes an empty block, the rule every optional member of this node
// follows: a project that sets no grade publishes no member at all, and an
// absent member and `{}` are the same block — no grade — so the ladder's rungs
// refuse and ask (D2039) instead of running a model nobody chose.
type ModelGrades = config.ModelGrades

// modelGradesMembers is the block's own member spelling, read from the struct's
// json tags (carrierMembers) so a grade added there is known here without a
// second list to keep in step.
var modelGradesMembers = carrierMembers(reflect.TypeOf(config.ModelGrades{}))

// decodeModelGrades reads the node's `model_grades` member: the two grades, in
// the spelling this release documents, held to the rules a layer file's block is
// held to (config.ValidateModelGrades) — a written grade names a model, the
// model is a model ID, the access is a credential or pool name of the right
// shape.
//
// A block that writes neither grade is no block: the decoder answers nil, the
// way an empty orchestrator and an empty crafts block are none, so that a
// publisher that writes `{}` has changed nothing on the node.
func decodeModelGrades(raw json.RawMessage) (*config.ModelGrades, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: model_grades: %w", err)
	}
	if err := checkMemberNames("model_grades", names, modelGradesMembers); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var g config.ModelGrades
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("projectpolicy: model_grades: %w", err)
	}
	for _, prob := range config.ValidateModelGrades(g, "model_grades") {
		return nil, fmt.Errorf("projectpolicy: %s: %s", prob.Path, prob.Message)
	}
	if g.Empty() {
		return nil, nil
	}
	return &g, nil
}

// GradePriceProblems reports every grade of this node whose model the run's
// price table does not cover, with the path a refusal prints.
//
// It is the rule a budget puts on the ladder: a rung runs the model its grade
// names, and a model the catalogue does not price cannot be costed — the
// gateway's ceiling would be enforced on a model it cannot meter, which is the
// state config.CheckProviderPolicy refuses for every other model a run calls.
// The message is that function's, so an operator reads one wording for one
// problem wherever a model with no price is refused.
//
// prices is the run's table (internal/cli's priceTableFor over the node's
// model_prices and the catalogue), which is why this is not Decode's: a node is
// read where no catalogue exists — `fugaro doctor` meets one on a machine that
// has never fetched it — and a check that ran in the decoder would refuse a node
// over a table nobody asked for. nil is no table, and therefore no budget, which
// is the gate CheckProviderPolicy uses too: with no table nothing is refused.
func (p *Policy) GradePriceProblems(prices *pricing.Table) []config.Problem {
	if p == nil || p.ModelGrades == nil || prices == nil {
		return nil
	}
	var ps []config.Problem
	for _, name := range config.GradeNames {
		grade := p.ModelGrades.Of(name)
		if grade.Model == "" {
			continue
		}
		if _, ok := prices.Lookup(grade.Model); !ok {
			ps = append(ps, config.Problem{Path: "model_grades." + name + ".model",
				Message: fmt.Sprintf("%s has no price: add it under model_prices in the project config", config.CodeSpan(grade.Model))})
		}
	}
	return ps
}
