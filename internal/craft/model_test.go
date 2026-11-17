package craft

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// The model half's contract (docs/model-crafts/activation-contract.md, plan
// task MC11): the rungs a stage's model is chosen by, the craft each stage's
// model belongs to, and the answer this package gives for the craft rung alone.

// TestModelForAnswersTheCraftRung: the craft rung of every stage a run has, on
// the spec's own example (specs): the coder plans, implements and fixes; the
// first-line review is the review craft, which sets no model of its own and so
// answers the coder's; the senior review and the triage that weighs its
// findings are the senior-review craft; and level 3's adversarial review is the
// security-review craft, whose own model is the one it wrote.
func TestModelForAnswersTheCraftRung(t *testing.T) {
	crafts, ps := Resolve(specs(), false)
	if len(ps) > 0 {
		t.Fatalf("the spec's own example has problems: %v", ps)
	}
	for _, tc := range []struct {
		stage     string
		wantModel string
		wantCraft string
	}{
		{"plan", "deepseek/deepseek-v4.1-flash", config.CraftCoder},
		{"implement", "deepseek/deepseek-v4.1-flash", config.CraftCoder},
		{"fix", "deepseek/deepseek-v4.1-flash", config.CraftCoder},
		{"review_first", "deepseek/deepseek-v4.1-flash", config.CraftReview},
		{"review", "claude-opus-4", config.CraftSeniorReview},
		{"triage", "claude-opus-4", config.CraftSeniorReview},
		{"adversarial", "claude-opus-4-1", config.CraftSecurityReview},
	} {
		model, name := ModelFor(crafts, tc.stage)
		if model != tc.wantModel || name != tc.wantCraft {
			t.Errorf("ModelFor(%q) = (%q, %q), want (%q, %q)", tc.stage, model, name, tc.wantModel, tc.wantCraft)
		}
	}
	// The craft rung and nothing else: a craft no layer gives a model answers no
	// model at all, and still answers its craft. The two rungs below it
	// (agent.models.<role>, agent.model) are the run's, and a fallback invented
	// here would hide that the owner wrote no model for the craft.
	bare, _ := Resolve(nil, false)
	if model, name := ModelFor(bare, "implement"); model != "" || name != config.CraftCoder {
		t.Errorf("ModelFor with no model written for the coder = (%q, %q), want (\"\", %q): the craft rung answers the craft's own resolved model and never a rung below it", model, name, config.CraftCoder)
	}
}

// TestTheRungOrderIsTheContracts: D1660's five rungs, most specific first, as
// the contract states them. The order is the contract's and not a preference: a
// task's grade and a review level's own model are narrower statements about
// fewer stages than a craft is, so both overrule the craft's model, and the
// craft's overrules the role slot's and agent.model.
func TestTheRungOrderIsTheContracts(t *testing.T) {
	want := []string{
		"task.grade",
		"agent.levels.<level>.model",
		"agent.crafts.<craft>.model",
		"agent.models.<role>",
		"agent.model",
	}
	got := ModelRungs()
	if !slices.Equal(got, want) {
		t.Fatalf("ModelRungs() = %q, want %q", got, want)
	}
	// The list is the contract's and not the caller's: a caller that reorders
	// what it was handed must not reorder the contract for the next one.
	got[0] = "mutated"
	if next := ModelRungs(); next[0] != want[0] {
		t.Errorf("ModelRungs()[0] = %q after a caller wrote to an earlier answer, want %q: the contract's own list is not a caller's to edit", next[0], want[0])
	}
}

// TestAnUnknownStageAnswersNoCraft: a stage outside the run's vocabulary
// answers no craft rather than guessing the coder, which is what the map's own
// contract says — and the stages a run spends a model call on are exactly the
// ones that answer a craft, so a stage added to config.RunStages without one
// fails here rather than running the ladder's model with no craft's.
func TestAnUnknownStageAnswersNoCraft(t *testing.T) {
	crafts, _ := Resolve(specs(), false)
	for _, stage := range []string{
		"", "bootstrap", "render", "finalize", "writeback", "provision",
		"review-first", "coder", "senior-review", "security-review",
	} {
		if model, name := ModelFor(crafts, stage); model != "" || name != "" {
			t.Errorf("ModelFor(%q) = (%q, %q), want (\"\", \"\"): the name is no stage a run spends a model call on, so it belongs to no craft", stage, model, name)
		}
	}
	for _, stage := range config.RunStages {
		if _, name := ModelFor(crafts, stage); name == "" {
			t.Errorf("ModelFor(%q) answers no craft, and config.RunStages lists the stage as one a run spends a model call on", stage)
		}
	}
}
