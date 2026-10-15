package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestAModeArtifactRecipeIsRefusedByARunnerThatDoesNotImplementIt (Review
// Focus 10, plan P10): Task 1 ships the artifact format and this one
// refusal, and nothing else of the feature. A `mode: artifact` recipe
// launched at a runner that knows the format but not the mode must end
// failed at bootstrap with this refusal, naming the mode — never run as an
// implement run, which would push a branch and open a pull request for a
// recipe that asked for a file. The runner's recipe/spec mode cross-check
// has an arm for mode: implement and one for mode: review, so without this
// arm an artifact recipe falls through both and implement runs.
//
// Task 2 replaces the arm with the real mode and folds this file into
// artifact_test.go.
//
// Mutation (delete the recipe.ModeArtifact arm from runner.go's mode
// cross-check): this test fails — implement runs, the harness's scripted
// agent is called, a branch is pushed and the record is a draft, not an
// infra_error.
func TestAModeArtifactRecipeIsRefusedByARunnerThatDoesNotImplementIt(t *testing.T) {
	const yaml = "version: 1\nname: sizzle\nmode: artifact\noutputs: { globs: [out/*.mp4] }\nsteps:\n  - render: {}\n"
	h := newHarness(t, firstLineCfg(t, 2, ""), recipeSpec(embeddedTask("sizzle", yaml)))
	pushes := countPushes(t, h)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError ||
		!strings.Contains(rec.Reason, "mode: artifact, which this runner does not implement") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("outcome = %q, want none", rec.Outcome)
	}
	if n := len(h.agent.calls); n != 0 {
		t.Fatalf("%d agent calls, want none (implement must not run)", n)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
}
