package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
)

// TestStageCraftMapsAgree holds the two copies of F13's stage-to-craft map in
// step: the run's own (stageCraft, internal/runner/routing.go) and the craft
// package's, which craft.ModelFor reads to answer a stage. The craft package's
// copy is private and cannot be read directly, so it is read the way the run
// reads it — through ModelFor, whose second answer is the craft the stage
// answers — and the two are compared for every stage the run has.
//
// The map is what decides which craft's model a stage runs and which craft's
// credential its calls go out on, and the two halves have to be the same craft:
// a stage the runner sent to one craft's access while the craft package named
// another's model would spend on a credential its model half never agreed to.
// This is the test the contract and internal/craft/model.go claim exists
// ("held in step by the runner's own tests, which read both answers for every
// stage a run has").
//
// Hand mutation this fails on: adding a stage to one map and not the other —
// the coder craft's plan, or the triage stage's move to the senior review.
func TestStageCraftMapsAgree(t *testing.T) {
	// Every craft names its own model, so a stage answered by the wrong craft
	// answers a different model as well and the failure names both halves.
	crafts := map[string]craft.Effective{}
	for _, name := range config.CraftNames {
		crafts[name] = craft.Effective{Model: "model-" + name}
	}
	// merge_judge runs beside the loop and is not one of config.RunStages (E6),
	// so it is named here: it is a stage with a craft of its own, and a map that
	// lost the case would answer it no craft at all — which is a run's access
	// refused rather than the judge's calls served (C1 (c), F49).
	stages := append(append([]string{}, config.RunStages...), stageMergeJudge)
	for _, stage := range stages {
		model, name := craft.ModelFor(crafts, stage)
		if got := stageCraft(stage); got != name {
			t.Errorf("stageCraft(%q) = %q, but the craft package answers %q: the runner's map and internal/craft's disagree about which craft the stage runs", stage, got, name)
			continue
		}
		if name == "" {
			t.Errorf("stage %q is one of config.RunStages and answers no craft in either map", stage)
			continue
		}
		if model != crafts[name].Model {
			t.Errorf("craft.ModelFor(%q) = %q, want the %s craft's own %q", stage, model, name, crafts[name].Model)
		}
	}
	// A stage outside the vocabulary answers no craft in both, rather than
	// guessing one: the walk is what stagePool and stageAccount read.
	for _, stage := range []string{"", "summarize", "review_first_line"} {
		if got := stageCraft(stage); got != "" {
			t.Errorf("stageCraft(%q) = %q, want no craft: %q is not a stage of a run", stage, got, stage)
		}
		if model, name := craft.ModelFor(crafts, stage); model != "" || name != "" {
			t.Errorf("craft.ModelFor(%q) = (%q, %q), want no craft and no model", stage, model, name)
		}
	}
}
